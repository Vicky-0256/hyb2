"""HYB binding energies: column 3 of a sample's .hyb, recalculated.

Bridge operation ``batch_energy_run`` (a task).  Request::

    {"operation": "batch_energy_run", "project_path": ..., "task_id": ..., "run_dir": ...,
     "sample_id": ..., "workers": 4,
     "parameters": {"engine": "rnacofold" | "cplcofold" | "cplcofold-pk",
                    "records": "all" | "filtered", "filters": {<analysis_hybrids filters>}}}

As the web's "HYB binding energies" tool (web/batch-tools.js): the two arms
of each chosen record are cut from the sample's reference (columns 4, 7, 8
and 10, 13, 14; 1-based, inclusive, T read as U) and cofolded with

* ``rnacofold``: ViennaRNA RNAcofold MFE, default model (37 C), as the HYB2
  pipeline's sam2hyb;
* ``cplcofold``: CPLcofold (``two_phase_cofold`` through
  web/cplfold/cplfold_web.py) with the web batch's settings: DP09, no HYB
  evidence, beam 20, energy delta 5, three phase-1 candidates, pseudoknots
  off;
* ``cplcofold-pk``: the same with the pseudoknot phase, whose DP09 term is
  an experimental ranking score, not a validated free energy.

``records: "filtered"`` takes the rows matching ``filters`` (the Interactions
page's) in the sample's analysis index, which must be built from the
current .hyb.  Only column 3 of updated rows changes (two decimals);
comments, invalid rows, extra columns and line endings are kept.  Rows
that are unmapped, antisense, ambiguous, over the length limit (2,000 nt
of both arms for RNAcofold, 500 for CPLcofold) or not chosen keep their
energy and are listed in the audit.

The run directory gets ``energies.hyb``, ``audit.tsv`` (line id status
old_energy new_energy note), ``parameters.json`` and
``hyb2-energies-<engine>.zip`` (the three under ``hyb2-energies-<engine>/``,
as the web's ZIP), plus ``progress.json`` and ``result.json``.
"""
from __future__ import annotations

import gzip
import json
import math
import os
import re
import sys
import time
import zipfile
from concurrent.futures import ProcessPoolExecutor, as_completed
from multiprocessing import get_context
from pathlib import Path
from typing import Any, Dict, List, Optional, Set, Tuple

from . import trust as _trust
from .project import _atomic_write_json, load_manifest, safe_relative_path, validate_project_directory
from .samples import _hyb2_reference_name
from .structure import ProgressWriter, _evidence_inputs, _now, _resolve_run_dir, load_cplfold_web, read_fasta

# The web batch's engines: label, combined arm limit, pairs per job.
ENGINES: Dict[str, Dict[str, Any]] = {
    "rnacofold": {"name": "ViennaRNA RNAcofold", "max_length": 2000, "chunk": 64},
    "cplcofold": {"name": "CPLcofold, pseudoknots off", "max_length": 500, "chunk": 4, "pseudoknots": False},
    "cplcofold-pk": {"name": "CPLcofold, pseudoknots allowed (DP09 ranking score)", "max_length": 500, "chunk": 4,
                     "pseudoknots": True},
}
# cplfold_web.fold's defaults for a two-strand request, which the web's
# cplfold-energy-batch uses.
CPLCOFOLD_SETTINGS = {"beamSize": 20, "energyDelta": 5, "maxPhase1": 3, "energyModel": "DP09", "alpha": 0.0, "beta": 0.0}
BATCH_LIMIT = 2000
AUDIT_HEADER = "line\tid\tstatus\told_energy\tnew_energy\tnote"
_TASK_ID_CHARS = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-"
_BASES = re.compile(r"^[ACGU]+$")


def _safe_task_id(value: Any) -> str:
    task_id = str(value or ("batch-energy-" + os.urandom(8).hex()))
    if not task_id or len(task_id) > 64 or not task_id[0].isalpha() or any(c not in _TASK_ID_CHARS for c in task_id):
        raise ValueError("task_id must be a safe identifier")
    return task_id


def _parameters(raw: Any) -> Dict[str, Any]:
    raw = raw if isinstance(raw, dict) else {}
    unknown = sorted(set(raw) - {"engine", "records", "filters"})
    if unknown:
        raise ValueError("unknown batch energy parameters: " + ", ".join(unknown))
    engine = raw.get("engine", "rnacofold")
    if engine not in ENGINES:
        raise ValueError("engine must be rnacofold, cplcofold or cplcofold-pk")
    records = raw.get("records", "all")
    if records not in ("all", "filtered"):
        raise ValueError("records must be 'all' or 'filtered'")
    filters = raw.get("filters")
    if filters is not None and not isinstance(filters, dict):
        raise ValueError("filters must be an object")
    return {"engine": engine, "records": records, "filters": dict(filters or {}) if records == "filtered" else None}


# ---------------------------------------------------------------------------
# records and arms
# ---------------------------------------------------------------------------

def _pipeline_modules(root: Path) -> Tuple[Any, Any, Any]:
    """hyb2analysis (hybfile, index, query): the rules that decide which lines are records."""
    package = Path(root) / "pipeline"
    if not (package / "hyb2analysis" / "__init__.py").is_file():
        raise ValueError("the analysis backend (hyb2analysis) is not bundled")
    if str(package) not in sys.path:
        sys.path.insert(0, str(package))
    from hyb2analysis import hybfile, index, query

    return hybfile, index, query


def _is_record(hybfile: Any, index: Any, line: bytes) -> bool:
    """A valid record, as the analysis index counts them (its ids are their ordinals)."""
    if index._fast_parse(line.split(b"\t")) is not None:
        return True
    try:
        return hybfile.parse(line) is not None
    except ValueError:
        return False


def _open_hyb(path: Path):
    return gzip.open(path, "rb") if str(path).endswith(".gz") else open(path, "rb", buffering=1 << 20)


def reference_sequences(path: Path) -> Dict[str, str]:
    """Name -> sequence; names as read_fasta gives them (the .hyb gene IDs)."""
    return {name: sequence for name, sequence in read_fasta(path)}


def arms(cols: List[str], sequences: Dict[str, str], max_length: int, engine_label: str) -> Tuple[str, str]:
    """The two arm sequences of a .hyb row (web: Hyb2Batch.prepareEnergy); ValueError says why not."""
    if len(cols) < 15:
        raise ValueError("Incomplete HYB row")
    out = []
    for name_col, start_col, end_col in ((3, 6, 7), (9, 12, 13)):
        name = cols[name_col].strip()
        sequence = sequences.get(name)
        if sequence is None:
            sequence = sequences.get(_hyb2_reference_name(name)) if "|" in name else None
        if sequence is None:
            raise ValueError("No mapped FASTA for " + name)
        try:
            start, end = int(cols[start_col].strip()), int(cols[end_col].strip())
        except ValueError:
            start = end = 0
        if start < 1 or end < start:
            raise ValueError("Invalid or antisense reference interval")
        if end > len(sequence):
            raise ValueError("Interval exceeds FASTA length")
        arm = sequence[start - 1:end].upper().replace("T", "U")
        if not _BASES.match(arm):
            raise ValueError("Unsupported or ambiguous reference bases")
        out.append(arm)
    total = len(out[0]) + len(out[1])
    if total > BATCH_LIMIT:
        raise ValueError(f"Combined arms exceed the {BATCH_LIMIT:,} nt batch limit")
    if total > max_length:
        raise ValueError(f"Combined arms exceed the {max_length:,} nt {engine_label} limit")
    return out[0], out[1]


def rewrite_energy(line: bytes, energy: float) -> bytes:
    """Column 3 replaced by ``energy`` with two decimals; everything else as it was."""
    parts = line.split(b"\t", 3)
    parts[2] = ("%.2f" % energy).encode("ascii")
    return b"\t".join(parts)


# ---------------------------------------------------------------------------
# folding
# ---------------------------------------------------------------------------

def fold_pairs(root: str, engine: str, pairs: List[Tuple[str, str]]) -> List[float]:
    """The binding energy of each (arm 1, arm 2), kcal/mol, two decimals."""
    if engine == "rnacofold":
        from .energy import cofold

        return [cofold(a, b) for a, b in pairs]
    spec = ENGINES[engine]
    module = load_cplfold_web(Path(root))
    out = []
    for a, b in pairs:
        result = module.fold({"sequence": a, "sequenceTwo": b, "evidenceMode": "none", "evidenceArms": [],
                              "allowPseudoknot": spec["pseudoknots"], "maxSequenceLength": spec["max_length"],
                              **CPLCOFOLD_SETTINGS})
        energy = result.get("energy")
        if isinstance(energy, bool) or not isinstance(energy, (int, float)) or not math.isfinite(energy):
            raise ValueError("CPLcofold returned no energy")
        out.append(round(float(energy), 2))
    return out


def _fold_chunk(root: str, engine: str, start: int, pairs: List[Tuple[str, str]]) -> Tuple[int, List[float]]:
    return start, fold_pairs(root, engine, pairs)


def _worker_init(root: str, engine: str) -> None:
    os.environ["NUMBA_NUM_THREADS"] = "1"
    if engine == "rnacofold":
        return
    try:
        import numba

        numba.set_num_threads(1)
    except (ImportError, AttributeError, ValueError):
        pass
    load_cplfold_web(Path(root))


def engine_version(root: Path, engine: str) -> Optional[str]:
    if engine == "rnacofold":
        from .energy import vienna_version

        version = vienna_version()
        return "ViennaRNA " + version if version else None
    try:
        return "CPLfold " + str(getattr(load_cplfold_web(root), "CPLFOLD_SOURCE_REVISION", "?"))
    except (OSError, ImportError):
        return None


# ---------------------------------------------------------------------------
# filtered records
# ---------------------------------------------------------------------------

def _index_path(project: Path, sample: Dict[str, Any]) -> Path:
    index = (sample.get("outputs") or {}).get("index") if isinstance(sample.get("outputs"), dict) else None
    if not isinstance(index, str) or not index:
        raise ValueError("the sample has no analysis index yet: build it on the Data page to use the Interactions filters")
    if os.path.isabs(index):
        _trust.require(project, index, "sample index")
        return Path(index).resolve()
    if not safe_relative_path(index):
        raise ValueError("sample index must be a project-relative or absolute path")
    target = (project / index).resolve()
    if project not in target.parents:
        raise ValueError("sample index escapes the project")
    return target


def selected_ids(root: Path, index_path: Path, hyb_path: Path, filters: Dict[str, Any]) -> Set[int]:
    """Ids (valid-record ordinals) of the index rows matching ``filters``."""
    _, _, query = _pipeline_modules(root)
    meta = query._get(index_path).meta
    stat = hyb_path.stat()
    if stat.st_size != meta.get("source_size") or stat.st_mtime_ns != meta.get("source_mtime_ns"):
        raise ValueError("the sample's analysis index is older than its .hyb file: rebuild the index first")
    ids: Set[int] = set()
    offset = 0
    while True:
        page = query.hybrids([index_path], filters, query.MAX_LIMIT, offset, order="file", with_sequence=False)
        rows = page["rows"]
        ids.update(int(row["id"]) for row in rows)
        offset += len(rows)
        if not rows or offset >= page["total"]:
            return ids


# ---------------------------------------------------------------------------
# task
# ---------------------------------------------------------------------------

def _tsv(value: Any) -> str:
    return re.sub(r"[\t\r\n]", " ", "" if value is None else str(value))


def _workers(request: Dict[str, Any], jobs: int) -> int:
    value = request.get("workers", 1)
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise ValueError("workers must be a positive integer")
    return max(1, min(value, os.cpu_count() or 1, jobs))


def _duration(seconds: float) -> str:
    seconds = max(1, round(seconds))
    return f"{seconds} s" if seconds < 90 else f"{round(seconds / 60)} min" if seconds < 5400 else f"{seconds / 3600:.1f} h"


def _batch_energy_run(request: Dict[str, Any], repo_root: Optional[Path], state: Dict[str, Any]) -> Dict[str, Any]:
    started = time.monotonic()
    root = Path(repo_root or Path(__file__).resolve().parents[2]).resolve()
    raw_path = request.get("project_path")
    if not isinstance(raw_path, str) or not raw_path.strip():
        raise ValueError("project path must be a non-empty string")
    project = Path(raw_path).expanduser().resolve()
    errors = validate_project_directory(project)
    if errors:
        raise ValueError("invalid project: " + "; ".join(errors))
    task_id = _safe_task_id(request.get("task_id"))
    sample_id = request.get("sample_id")
    if not isinstance(sample_id, str) or not sample_id:
        raise ValueError("sample_id is required")
    params = _parameters(request.get("parameters"))
    engine = ENGINES[params["engine"]]
    inputs = _evidence_inputs(project, {"sample_ids": [sample_id]})
    hyb_path, reference = inputs["hyb_paths"][0], inputs["reference"]
    sample = next(s for s in load_manifest(project).get("samples") or [] if isinstance(s, dict) and s.get("id") == sample_id)
    run_dir = _resolve_run_dir(project, task_id, request.get("run_dir"))
    state.update(run_dir=run_dir, task_id=task_id)
    progress = ProgressWriter(run_dir)

    progress("prepare", "reading the reference", None, None, units="none", force=True)
    sequences = reference_sequences(reference)
    chosen: Optional[Set[int]] = None
    if params["records"] == "filtered":
        progress("prepare", "selecting the records matching the Interactions filters", None, None, units="none", force=True)
        chosen = selected_ids(root, _index_path(project, sample), hyb_path, params["filters"])
    hybfile, index, _ = _pipeline_modules(root)

    # 1. the records, their arms or why they keep their energy
    total_bytes = hyb_path.stat().st_size
    label = "CPLcofold" if params["engine"] != "rnacofold" else "batch"
    notes: List[Tuple[int, str, str, Optional[int], str]] = []  # line, id, old, item, note
    pairs: List[Tuple[str, str]] = []
    with _open_hyb(hyb_path) as handle:
        record = 0
        for line_no, line in enumerate(hybfile.lines(handle), 1):
            if not _is_record(hybfile, index, line):
                continue
            record += 1
            cols = line.rstrip(b"\r\n").decode("utf-8", "replace").split("\t")
            rid, old = cols[0].lstrip("﻿"), cols[2] if len(cols) > 2 else ""
            if chosen is not None and record not in chosen:
                notes.append((line_no, rid, old, None, "not selected by the Interactions filters"))
                continue
            try:
                pairs.append(arms(cols, sequences, engine["max_length"], label))
                notes.append((line_no, rid, old, len(pairs) - 1, ""))
            except ValueError as error:
                notes.append((line_no, rid, old, None, str(error)))
            if record % 20000 == 0:
                progress("read", "reading the .hyb records", None, None, units="none")
    if not notes:
        raise ValueError("No HYB records available.")

    # 2. fold, chunk by chunk; energies come back in input order
    energies: List[Optional[float]] = [None] * len(pairs)
    chunk = engine["chunk"]
    starts = list(range(0, len(pairs), chunk))
    workers = _workers(request, max(len(starts), 1))
    fold_started = time.monotonic()
    done = 0

    def report() -> None:
        left = len(pairs) - done
        rate = done / max(time.monotonic() - fold_started, 1e-9)
        tail = f"; about {_duration(left / rate)} left." if done and left else "."
        progress("fold", f"{done:,} / {len(pairs):,} energies calculated with {engine['name']} on {workers} "
                 f"worker{'s' if workers != 1 else ''}{tail}", done, len(pairs), units="records")

    progress("fold", f"starting {engine['name']} for {len(pairs):,} records", 0, len(pairs), units="records", force=True)
    if workers <= 1:
        for start in starts:
            energies[start:start + chunk] = fold_pairs(str(root), params["engine"], pairs[start:start + chunk])
            done += len(pairs[start:start + chunk])
            report()
    else:
        with ProcessPoolExecutor(max_workers=workers, mp_context=get_context("spawn"), initializer=_worker_init,
                                 initargs=(str(root), params["engine"])) as pool:
            futures = [pool.submit(_fold_chunk, str(root), params["engine"], start, pairs[start:start + chunk]) for start in starts]
            for future in as_completed(futures):
                start, values = future.result()
                energies[start:start + len(values)] = values
                done += len(values)
                report()
    fold_seconds = time.monotonic() - fold_started

    # 3. the updated .hyb (only column 3 of updated rows), audit and parameters
    progress("write", "writing the updated .hyb", None, None, units="none", force=True)
    audit = [AUDIT_HEADER]
    updates: Dict[int, Tuple[str, float]] = {}
    updated = unchanged = 0
    for line_no, rid, old, item, note in notes:
        if item is None:
            unchanged += 1
            audit.append("\t".join(_tsv(v) for v in (line_no, rid, "unchanged", old, old, note)))
            continue
        energy = energies[item]
        if energy is None or not math.isfinite(energy):
            raise ValueError("Invalid energy returned")
        updates[line_no] = (old, energy)
        updated += 1
        audit.append("\t".join(_tsv(v) for v in (line_no, rid, "updated", old, "%.2f" % energy, "")))
    out_hyb = run_dir / "energies.hyb"
    temporary = out_hyb.with_name("." + out_hyb.name + ".tmp")
    with _open_hyb(hyb_path) as source, open(temporary, "wb") as target:
        for line_no, line in enumerate(hybfile.lines(source), 1):
            update = updates.get(line_no)
            if update is not None:
                parts = line.split(b"\t", 3)
                if len(parts) < 4 or parts[2].decode("utf-8", "replace") != update[0]:
                    raise ValueError(f"Source row changed at line {line_no}")
                line = rewrite_energy(line, update[1])
            target.write(line)
    os.replace(temporary, out_hyb)
    audit_text = "\n".join(audit) + "\n"
    (run_dir / "audit.tsv").write_text(audit_text, encoding="utf-8")
    metadata = {
        "engine": engine["name"],
        "engineVersion": engine_version(root, params["engine"]),
        "temperature": 37,
        "minimumLoop": 3 if params["engine"] == "rnacofold" else None,
        "energyModel": "Turner 2004 (ViennaRNA default)" if params["engine"] == "rnacofold" else "CPLfold DP09",
        "pseudoknots": params["engine"] == "cplcofold-pk",
        "cplcofold": dict(CPLCOFOLD_SETTINGS, evidence="none") if params["engine"] != "rnacofold" else None,
        "records": "interaction-filters" if params["records"] == "filtered" else "all",
        "filters": params["filters"],
        "updated": updated, "unchanged": unchanged, "validRecords": len(notes),
        "workers": workers, "elapsedSeconds": round(fold_seconds, 1),
        "sample": sample_id, "sourceFile": hyb_path.name, "reference": reference.name,
        "maximumCombinedArmLength": engine["max_length"], "completedAt": _now(),
    }
    parameters_text = json.dumps(metadata, indent=2) + "\n"
    (run_dir / "parameters.json").write_text(parameters_text, encoding="utf-8")
    base = "hyb2-energies-" + params["engine"]
    archive = run_dir / (base + ".zip")
    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.write(out_hyb, base + "/energies.hyb")
        zf.writestr(base + "/audit.tsv", audit_text)
        zf.writestr(base + "/parameters.json", parameters_text)
    summary = {"updated": updated, "unchanged": unchanged, "valid_records": len(notes), "engine": params["engine"],
               "engine_name": engine["name"], "records": params["records"], "sample_id": sample_id,
               "source_file": hyb_path.name, "workers": workers}
    message = (f"{updated:,} energies updated with {engine['name']} in {_duration(time.monotonic() - started)}; "
               f"{unchanged:,} valid rows unchanged. Comments and unparsed source lines are preserved.")
    result = {"ok": True, "status": "succeeded", "task_id": task_id, "run_dir": str(run_dir), "output": str(out_hyb),
              "outputs": {"hyb": str(out_hyb), "audit": str(run_dir / "audit.tsv"), "parameters": str(run_dir / "parameters.json"),
                          "zip": str(archive)},
              "summary": summary, "message": message, "seconds": round(time.monotonic() - started, 2)}
    progress("done", message, None, None, units="none", force=True)
    _atomic_write_json(run_dir / "result.json", result)
    return result


def batch_energy_run(request: Dict[str, Any], repo_root: Optional[Path] = None) -> Dict[str, Any]:
    """Bridge entry point; failures after the run directory exists go to result.json."""
    state: Dict[str, Any] = {}
    try:
        return _batch_energy_run(request, repo_root, state)
    except Exception as error:  # noqa: BLE001 - every failure must reach result.json
        run_dir = state.get("run_dir")
        if run_dir is None:
            raise
        result = {"ok": False, "status": "failed", "task_id": state.get("task_id"), "run_dir": str(run_dir),
                  "error": str(error) or error.__class__.__name__}
        ProgressWriter(run_dir)("failed", result["error"], None, None, units="none", force=True)
        _atomic_write_json(run_dir / "result.json", result)
        return result
