"""Adapt the shared IRIS predictor to desktop project indexes, jobs and results."""
from __future__ import annotations

import time
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence, Tuple

from pipeline.hyb2analysis.iris import MAX_IRIS_NT, MIN_LOOP, TEMPERATURE, diagnose, parameters, region_arms, run


def diagnose_indexes(index_paths: Sequence[Path], gene: str, start: int, end: int,
                     reference_length: Optional[int]) -> Dict[str, Any]:
    """:func:`diagnose` from the samples' analysis indexes (bridge operation
    ``analysis_iris_region``): the form's check before an IRIS run."""
    from hyb2analysis import query

    if not isinstance(gene, str) or not gene:
        raise ValueError("gene is required")
    if isinstance(start, bool) or isinstance(end, bool) or not isinstance(start, int) or not isinstance(end, int) \
            or not 1 <= start <= end:
        raise ValueError("start and end must be integers with 1 <= start <= end")
    total = 0
    rows: List[Tuple[str, int, int, str, int, int]] = []
    extent = 0
    for index in query._indexes(list(index_paths)):
        total += int(index.meta.get("records") or 0)
        gid = index.gene(gene)
        if gid is None:
            continue
        extent = max(extent, int(index.genes[gene][1] or 0))
        for s1, e1, s2, e2 in index.con.execute("SELECT s1, e1, s2, e2 FROM hybrids WHERE g1 = ? AND g2 = ? ORDER BY id",
                                                (gid, gid)):
            rows.append((gene, int(s1), int(e1), gene, int(s2), int(e2)))
    length = reference_length if reference_length else max(extent, end)
    found = diagnose(rows, total, gene, start, min(end, length), length)
    found["region"] = {"rna": gene, "start": start, "end": end}
    found["length"] = end - start + 1
    return found


# ---------------------------------------------------------------------------
# the structure task (structure.py dispatches engine "iris" here)
# ---------------------------------------------------------------------------

def _record(result: Dict[str, Any], region: str, start: int, end: int, gene: str) -> Dict[str, Any]:
    """A record in the shape of structure.fold_one's: the selected member
    (the heaviest) with every member as a candidate."""
    members = result["candidates"]
    selected = members[0]
    candidates = []
    for k, member in enumerate(members):
        candidates.append({
            "rank": k + 1, "type": "IRIS ensemble member", "topology": "nested", "energy": member["energy"],
            "effective_energy": None, "phase1_energy": None, "score": None, "structure": member["dotBracket"],
            "pairs": len(member["pairs"]), "pseudoknot_pairs": 0, "pairs_with_bonus": 0, "bonus": None,
            "weight": member["weight"], "coefficient": member["coefficient"], "constraints": member["constraints"],
        })
    iris = {
        "engineVersion": result["engineVersion"], "algorithm": result["algorithm"], "model": result["model"],
        "temperature": result["temperature"], "minimumLoop": result["minimumLoop"],
        "constraintMode": result["constraintMode"], "parameters": result["parameters"], "ensemble": result["ensemble"],
        "evidence": result["evidence"], "warnings": result["warnings"], "selectedMember": 0,
        "members": [{"member": k + 1, "weight": m["weight"], "coefficient": m["coefficient"], "energy": m["energy"],
                     "dotBracket": m["dotBracket"], "pairs": [list(p) for p in m["pairs"]],
                     "constraints": m["constraints"]} for k, m in enumerate(members)],
    }
    return {
        "structure": selected["dotBracket"], "energy": selected["energy"], "type": "IRIS ensemble member",
        "pairs": [(i, j, "()", None) for i, j in selected["pairs"]],
        "hard_pairs": selected["constraints"], "evidence": {"source": "iris-gaussian-support",
                                                          "inputRecords": result["evidence"]["inputRecords"]},
        "candidates": candidates, "candidates_found": len(candidates), "nucleotide_support": None,
        "iris": iris, "start": start, "end": end, "gene_id": gene,
    }


def structure_run(request: Dict[str, Any], project: Path, task_id: str, mode: str, state: Dict[str, Any],
                  started: float) -> Dict[str, Any]:
    """IRIS of one reference region (the whole RNA or one window), written as
    the other engines' results: structure.dbn/pairs.tsv/structure.ct hold the
    heaviest member, candidates.json every member and meta.json the ensemble."""
    from collections import Counter

    from . import structure as st
    from . import task_paths
    from . import vienna as _vienna

    params = parameters(request.get("parameters"))
    inputs = task_paths.evidence_inputs(project, request)
    gene_id, sequence = st._fold_sequence(request, inputs)
    windows = st._windows(request.get("windows"), len(sequence)) if mode == "windows" else [(1, len(sequence))]
    if len(windows) != 1:
        raise ValueError("IRIS folds one reference region: give one window")
    start, end = windows[0]
    if end - start + 1 > MAX_IRIS_NT:
        raise ValueError(f"IRIS desktop limit is {MAX_IRIS_NT:,} nt; select a smaller region "
                         f"({gene_id}:{start}-{end} is {end - start + 1:,} nt)")
    region = sequence[start - 1:end]

    run_dir = task_paths.resolve_run_dir(project, task_id, request.get("run_dir"))
    state.update(run_dir=run_dir, task_id=task_id)
    progress = st.ProgressWriter(run_dir)
    progress("iris", "Reading the chimeric reads", 2, 100, units="percent", force=True)
    t0 = time.monotonic()
    arms, found = region_arms(inputs["hyb_paths"], gene_id, sequence, start, end)
    evidence_s = time.monotonic() - t0
    if not arms:
        raise ValueError(found["message"] or "IRIS requires HYB evidence within the selected reference region.")
    t1 = time.monotonic()
    last = [-1]

    def report(stage: str, percent: int) -> None:
        progress("iris", stage, percent, 100, units="percent", force=percent != last[0] and not stage.startswith("Generating"))
        last[0] = percent

    result = run(region, arms, params, report)
    fold_s = time.monotonic() - t1
    record = _record(result, f"{gene_id}:{start}-{end}", start, end, gene_id)
    record["sequence"] = region
    record["seconds"] = {"bonus": round(evidence_s, 2), "fold": round(fold_s, 2)}
    blocks = Counter((a["oneStart"], a["oneEnd"], a["twoStart"], a["twoEnd"]) for a in arms)
    record["arms"] = st._arms_sample(blocks)

    progress("write", "writing results", None, None, units="none", force=True)
    out_dir = task_paths.real_child_dir(project / "structures", task_id, exist_ok=True)
    stored = {"engine": "IRIS", "temperature": TEMPERATURE, "min_loop": MIN_LOOP, **params,
              "kRange": " ".join(str(k) for k in params["kRange"]), "hard_pairs": [
                  [start + i - 1, start + j - 1] for i, j in record["hard_pairs"]]}
    outputs = st._write_outputs(out_dir, [record], stored)
    summary_record = st._summary(record)
    summary_record["iris"] = record["iris"]
    meta = {
        "version": 1, "task_id": task_id, "mode": mode, "engine": "IRIS", "engine_version": result["engineVersion"],
        "vienna_version": _vienna.version(),
        "gene_id": gene_id, "reference": {"path": str(inputs["reference"]), "length": len(sequence)},
        "evidence_inputs": {"samples": inputs["samples"],
                            "hyb": [{"path": str(path), "size_bytes": path.stat().st_size} for path in inputs["hyb_paths"]],
                            "rows": "intramolecular chimeras (both arms on gene_id) inside the region; one Gaussian "
                                    "support block per row",
                            "diagnosis": found},
        "parameters": stored,
        "coordinates": "1-based inclusive positions on the gene_id reference sequence (windows, pairs.tsv i/j, ct column 6); "
                       "IRIS members and constraints in region positions",
        "windows": [{"start": start, "end": end}],
        "workers": 1, "records": [summary_record],
        "timings": {"evidence_s": round(evidence_s, 2), "fold_s": round(fold_s, 2),
                    "total_s": round(time.monotonic() - started, 2)},
        "created_at": st._now(),
    }
    st._atomic_write_json(out_dir / "meta.json", meta)
    outputs["meta"] = str(out_dir / "meta.json")
    relative_dir = f"structures/{task_id}"
    summary = {"records": 1, "pairs": summary_record["pairs"], "pairs_with_bonus": 0, "pseudoknot_pairs": 0,
               "energy": summary_record["energy"], "energies": [summary_record["energy"]],
               "length": summary_record["length"], "workers": 1, "timings": meta["timings"], "engine": "IRIS",
               "members": len(result["candidates"]), "weight": result["candidates"][0]["weight"]}
    out = {
        "ok": True, "status": "succeeded", "task_id": task_id, "run_dir": str(run_dir),
        "output": outputs["dbn"], "output_dir": str(out_dir), "outputs": outputs,
        "performance": {"lengths": [end - start + 1], "engine": "iris", "workers": 1, "seconds": round(fold_s, 2)},
        "structure": {"id": task_id, "task_id": task_id, "sample_ids": [sample["id"] for sample in inputs["samples"]],
                      "gene_id": gene_id, "mode": mode, "windows": meta["windows"], "parameters": stored,
                      "created_at": meta["created_at"], "path": relative_dir,
                      "paths": {key: f"{relative_dir}/{Path(value).name}" for key, value in outputs.items()},
                      "summary": summary},
        "summary": summary,
    }
    progress("done", "finished", None, None, units="none", force=True)
    st._atomic_write_json(run_dir / "result.json", out)
    return out
