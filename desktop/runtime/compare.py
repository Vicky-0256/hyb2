"""Differential comparison of two conditions (bridge operation ``compare_run``).

Request::

    {"operation": "compare_run", "project_path": ..., "task_id": ..., "run_dir": ...,
     "samples_one": [sample ids], "samples_two": [sample ids],
     "parameters": {"min_count": 2, "range": 100, "padj_cutoff": 0.05, "similarity_min": 1,
                    "label_one": "...", "label_two": "...", "gene_id": null, "gene2": null}}

Each sample contributes its contact.txt (10-nt cells of its own target gene,
as in ``hyb2_compare``; ``gene_id`` forces one gene for every sample).  A
sample whose contact.txt is missing, or was made for another gene, gets one
made from its .hyb under the comparison directory.  ``gene2`` other than the
sample's gene makes an inter-RNA map instead (RNA X = ``gene_id`` or the
sample's gene, RNA Y = ``gene2``), counted as the web's differential test
counts it (``collectArms`` in web/deseq-card.js, ``binArms`` in
web/deseq.js): X x Y hybrids as they are, Y x X hybrids with their arms
swapped, so x is always a position on X.  The statistics are
:mod:`hyb2analysis.differential` (DESeq2 Wald test, condition one vs two).

Results go to ``<project>/comparisons/<task_id>/`` (results.tsv,
significant.tsv, interactions.tsv, similarity.tsv, summary.json, contacts/);
similarity.tsv is HYB2's similarity heatmap (bin/plot_similarity_map) of
the two conditions' merged maps, cells below ``similarity_min`` dropped; the task's
``run_dir`` gets ``progress.json`` and ``result.json``.  ``register_task_run``
copies the ``comparison`` entry of the result into ``comparisons[]``.

The directory also gets the web Compare page's exports, byte-identical for
the same counts (:data:`EXPORT_FILES`): the raw DESeq2 count table of every
cell (``comparisonCountMatrixToTsv``), HYB2's headerless names.table, the
extended sample metadata, and ``top_maps.json``: the cells of the web's
"Top 10 local maps per direction" (``topRegions`` / ``mapBatch`` in
web/batch-tools.js), i.e. the ten smallest-padj interactions of each
direction, each replicate's 10-nt cells within +-150 nt of them, and the
histogram of every replicate's cell counts, from which the desktop takes
the contrast cap when it writes the ZIP.
"""
from __future__ import annotations

import json
import math
import os
import re
import sys
import time
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Sequence, Tuple

import numpy as np

from .project import _atomic_write_json, load_manifest, safe_relative_path, validate_project_directory
from .structure import ProgressWriter, _env_int, _input_file, _now, _real_child_dir, _resolve_run_dir, _safe_task_id

MAX_SAMPLES = 64
PATH_KEYS = ("results", "significant", "interactions", "similarity", "summary")
_FILES = {"results": "results.tsv", "significant": "significant.tsv", "interactions": "interactions.tsv",
          "similarity": "similarity.tsv", "summary": "summary.json"}
# the web Compare page's exports, next to the results (see the module docstring)
EXPORT_FILES = {"counts": "counts.table.txt", "names": "names.table", "metadata": "sample_metadata.tsv",
                "top_maps": "top_maps.json"}
BIN = 10
TOP_PER_DIRECTION = 10
TOP_FLANK = 150
_SHIFT = 1 << 32
_CHUNK = 16384
_MAX_SAFE = 2 ** 53 - 1  # Number.isSafeInteger
# Bounds of an inter-RNA map's rows, past which a row is malformed (and
# skipped): a position fits the 32-bit half of a cell key, and an arm spans
# at most _MAX_ARM nt, so one row adds at most (_MAX_ARM / BIN + 1)^2 cells;
# a batch is folded into the map before it holds _BATCH_CELLS cells.
_MAX_POSITION = 2 ** 31 - 1
_MAX_ARM = 10_000
_BATCH_CELLS = 1 << 22


def _pipeline_path(repo_root: Optional[Path]) -> None:
    root = Path(repo_root or Path(__file__).resolve().parents[2]).resolve()
    pipeline = str(root / "pipeline")
    if pipeline not in sys.path:
        sys.path.insert(0, pipeline)


def _parameters(raw: Any) -> Dict[str, Any]:
    if raw is None:
        raw = {}
    if not isinstance(raw, dict):
        raise ValueError("parameters must be an object")
    known = {"min_count", "range", "padj_cutoff", "similarity_min", "label_one", "label_two", "gene_id", "gene2"}
    unknown = sorted(set(raw) - known)
    if unknown:
        raise ValueError("unknown compare parameters: " + ", ".join(unknown))
    params: Dict[str, Any] = {"min_count": 2, "range": 100, "padj_cutoff": 0.05, "similarity_min": 1}
    for key, (low, high) in {"min_count": (0, 1_000_000), "range": (0, 100_000),
                             "similarity_min": (1, 1_000_000)}.items():
        if key in raw:
            value = raw[key]
            if isinstance(value, bool) or not isinstance(value, int) or not low <= value <= high:
                raise ValueError(f"{key} must be an integer between {low} and {high}")
            params[key] = value
    if "padj_cutoff" in raw:
        value = raw["padj_cutoff"]
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not 0 < value <= 1:
            raise ValueError("padj_cutoff must be a number in (0, 1]")
        params["padj_cutoff"] = float(value)
    for key in ("label_one", "label_two", "gene_id", "gene2"):
        value = raw.get(key)
        if value is not None and (not isinstance(value, str) or not value.strip() or len(value) > 256):
            raise ValueError(f"{key} must be a non-empty string of at most 256 characters")
        params[key] = value.strip() if isinstance(value, str) else None
    return params


def _sample_list(value: Any, label: str) -> List[str]:
    if not isinstance(value, list) or not value or not all(isinstance(item, str) and item for item in value):
        raise ValueError(f"{label} must be a non-empty list of sample ids")
    if len(set(value)) != len(value):
        raise ValueError(f"{label} lists a sample twice")
    return list(value)


def _safe_name(text: str) -> str:
    return "".join(c if c.isalnum() or c in "._-" else "_" for c in text)


def _sample_hyb(sample: Dict[str, Any]) -> Optional[str]:
    """The sample's .hyb: its pipeline output, or its reads when they are one."""
    hyb = (sample.get("outputs") or {}).get("hyb")
    reads = sample.get("reads") or {}
    if not hyb and reads.get("format") == "hyb":
        hyb = reads.get("path")
    return hyb if isinstance(hyb, str) and hyb else None


def _contact_inputs(project: Path, out_dir: Path, sample_ids: List[str], gene_override: Optional[str],
                    gene2: Optional[str], progress: ProgressWriter) -> List[Dict[str, Any]]:
    from hyb2analysis import coverage

    manifest = load_manifest(project)
    samples = {sample.get("id"): sample for sample in manifest.get("samples") or [] if isinstance(sample, dict)}
    inputs = []
    for sample_id in sample_ids:
        sample = samples.get(sample_id)
        if sample is None:
            raise ValueError(f"unknown sample: {sample_id}")
        gene = gene_override or sample.get("gene_id")
        if not gene:
            raise ValueError(f"sample {sample_id} has no target gene (gene_id); set it on the Data page")
        partner = gene2 or gene
        outputs = sample.get("outputs") or {}
        hyb = _sample_hyb(sample)
        contact: Optional[Path] = None
        records: Optional[int] = None
        if outputs.get("contact") and gene == sample.get("gene_id") and partner == gene:
            try:
                contact = _input_file(project, outputs["contact"], f"sample {sample_id} contact map")
            except ValueError:
                contact = None
        made = False
        if contact is None:
            if not hyb:
                raise ValueError(f"sample {sample_id} has no .hyb output yet; run it first")
            hyb_path = _input_file(project, hyb, f"sample {sample_id} .hyb")
            contacts_dir = _real_child_dir(out_dir, "contacts", exist_ok=True)
            if partner == gene:
                contact = contacts_dir / f"{sample_id}.{_safe_name(gene)}.contact.txt"
                progress("contacts", f"contact map of {sample_id} ({gene})", None, None, units="none", force=True)
                coverage.write_contact(hyb_path, gene, contact)
            else:
                contact = contacts_dir / f"{sample_id}.{_safe_name(gene)}__{_safe_name(partner)}.contact.txt"
                progress("contacts", f"contact map of {sample_id} ({gene} x {partner})", None, None,
                         units="none", force=True)
                records = write_pair_contact(hyb_path, gene, partner, contact)
                if not records:
                    # as the web: every cell would have a zero here, so no size factors
                    raise ValueError(f"sample {sample_id} has no {gene} x {partner} records. Every cell would have a "
                                     "zero count in that sample, so size factors cannot be computed; leave it out "
                                     "of the test or choose another RNA pair.")
            made = True
        inputs.append({"id": sample_id, "gene_id": gene, "gene2": partner, "contact": contact, "generated": made,
                       "records": records, "condition": sample.get("condition"),
                       "label": sample.get("name") or sample_id, "file_name": Path(hyb).name if hyb else ""})
    return inputs


# ---------------------------------------------------------------------------
# inter-RNA contact maps (the web's collectArms + binArms)
# ---------------------------------------------------------------------------

def _coordinate(field: bytes) -> Optional[int]:
    """parser.worker.js parseCoordinate: digits only, a safe integer >= 1."""
    text = field.strip()
    if not text or not text.isdigit():
        return None
    value = int(text)
    return value if 1 <= value <= _MAX_SAFE else None


def _pair_arms(handle, gene_x: bytes, gene_y: bytes,
               skipped: Optional[Dict[str, int]] = None) -> Iterable[Tuple[int, int, int, int]]:
    """(x start, x end, y start, y end) of the X x Y hybrids, Y x X ones swapped.

    Rows the web's parser rejects are skipped as it skips them: blank and
    ``#`` lines, fewer than 15 tab-separated columns, an empty RNA name, a
    coordinate that is not a whole number >= 1, or an arm whose start is
    after its end.  An X x Y row with a position past ``_MAX_POSITION`` or
    an arm longer than ``_MAX_ARM`` nt is malformed: it is skipped too (a
    single one would otherwise fill memory) and counted in
    ``skipped["out_of_range"]``.
    """
    from hyb2analysis import hybfile

    for number, line in enumerate(hybfile.lines(handle)):
        line = line.rstrip(b"\r\n")
        if number == 0 and line.startswith(b"\xef\xbb\xbf"):
            line = line[3:]
        if not line.strip() or line.lstrip().startswith(b"#"):
            continue
        columns = line.split(b"\t")
        if len(columns) < 15:
            continue
        one, two = columns[3].strip(), columns[9].strip()
        if one == gene_x and two == gene_y:
            swap = False
        elif one == gene_y and two == gene_x:
            swap = True
        else:
            continue
        arms = [_coordinate(columns[k]) for k in (6, 7, 12, 13)]
        if any(value is None for value in arms) or arms[0] > arms[1] or arms[2] > arms[3]:
            continue
        if max(arms) > _MAX_POSITION or arms[1] - arms[0] >= _MAX_ARM or arms[3] - arms[2] >= _MAX_ARM:
            if skipped is not None:
                skipped["out_of_range"] = skipped.get("out_of_range", 0) + 1
            continue
        yield (arms[2], arms[3], arms[0], arms[1]) if swap else (arms[0], arms[1], arms[2], arms[3])


def pair_cells(hyb: Path, gene_x: str, gene_y: str,
               skipped: Optional[Dict[str, int]] = None) -> Tuple[np.ndarray, np.ndarray, int]:
    """``(keys, counts, records)`` of the 10-nt X x Y cells of a .hyb.

    Each hybrid adds one to every cell its x arm x y arm covers (bins
    ``floor(position / 10)``, as binArms); ``keys`` pack ``x << 32 | y`` of
    the cell starts, sorted.  Malformed rows (``_pair_arms``) are left out
    and counted in ``skipped``.
    """
    if gene_x == gene_y:
        raise ValueError("an inter-RNA map needs two different RNAs")
    parts_keys: List[np.ndarray] = []
    parts_counts: List[np.ndarray] = []
    records = 0

    def flush(batch: List[Tuple[int, int, int, int]]) -> None:
        arr = np.asarray(batch, dtype=np.int64) // BIN
        a, b, e, f = arr[:, 0], arr[:, 1], arr[:, 2], arr[:, 3]
        nx, ny = b - a + 1, f - e + 1
        cells = nx * ny
        owner = np.repeat(np.arange(len(cells)), cells)
        offset = np.arange(int(cells.sum()), dtype=np.int64) - np.repeat(np.cumsum(cells) - cells, cells)
        keys = (a[owner] + offset // ny[owner]) * BIN * _SHIFT + (e[owner] + offset % ny[owner]) * BIN
        unique, counts = np.unique(keys, return_counts=True)
        parts_keys.append(unique)
        parts_counts.append(counts)

    batch: List[Tuple[int, int, int, int]] = []
    cells = 0
    with open(hyb, "rb") as handle:
        for arms in _pair_arms(handle, gene_x.encode("utf-8"), gene_y.encode("utf-8"), skipped):
            batch.append(arms)
            records += 1
            cells += (arms[1] // BIN - arms[0] // BIN + 1) * (arms[3] // BIN - arms[2] // BIN + 1)
            if len(batch) >= _CHUNK or cells >= _BATCH_CELLS:
                flush(batch)
                batch, cells = [], 0
    if batch:
        flush(batch)
    if not parts_keys:
        return np.zeros(0, dtype=np.int64), np.zeros(0, dtype=np.int64), 0
    keys = np.concatenate(parts_keys)
    counts = np.concatenate(parts_counts)
    unique, inverse = np.unique(keys, return_inverse=True)
    return unique, np.bincount(inverse, weights=counts, minlength=len(unique)).astype(np.int64), records


def write_pair_contact(hyb: Path, gene_x: str, gene_y: str, out: Path) -> int:
    """Write the X x Y cells as a contact.txt (``x y count`` by x, then y);
    returns the number of hybrids counted."""
    skipped: Dict[str, int] = {}
    keys, counts, records = pair_cells(hyb, gene_x, gene_y, skipped)
    lines = ["# hybrids between %s and %s\n" % (gene_x, gene_y), "x\ty\tcount\n"]
    if skipped.get("out_of_range"):
        lines.insert(1, "# %d malformed rows skipped (a position past %d or an arm longer than %d nt)\n"
                     % (skipped["out_of_range"], _MAX_POSITION, _MAX_ARM))
    lines.extend("%d\t%d\t%d\n" % (key >> 32, key & 0xFFFFFFFF, count)
                 for key, count in zip(keys.tolist(), counts.tolist()))
    tmp = out.with_name("." + out.name + ".tmp")
    tmp.write_text("".join(lines), encoding="utf-8")
    os.replace(tmp, out)
    return records


# ---------------------------------------------------------------------------
# the web Compare page's exports
# ---------------------------------------------------------------------------

def _tsv_cell(value: Any) -> str:
    """analysis-data.js tsvCell: tabs and line breaks become one space."""
    return re.sub(r"[\t\r\n]+", " ", "" if value is None else str(value))


def _condition(flag: bool) -> str:
    return "condition_one" if flag else "condition_two"


def count_table_text(keys: np.ndarray, table: np.ndarray, sample_ids: Sequence[str]) -> str:
    """comparisonCountMatrixToTsv: ``feature_id`` then one raw-count column per
    sample, one ``x_y`` row per cell by x then y; no final newline."""
    rows = ["\t".join(["feature_id"] + [_tsv_cell(s) for s in sample_ids])]
    xs = (keys >> 32).tolist()
    ys = (keys & 0xFFFFFFFF).tolist()
    for x, y, counts in zip(xs, ys, table.tolist()):
        rows.append("%d_%d\t" % (x, y) + "\t".join(str(int(v)) for v in counts))
    return "\n".join(rows)


def names_table_text(sample_ids: Sequence[str], condition_one: Sequence[bool]) -> str:
    """comparisonLegacyNamesToTsv: ``id<TAB>condition_one|two`` lines, no header."""
    return "\n".join(_tsv_cell(s) + "\t" + _condition(flag) for s, flag in zip(sample_ids, condition_one))


def metadata_text(inputs: Sequence[Dict[str, Any]], condition_one: Sequence[bool], label_one: str,
                  label_two: str) -> str:
    """comparisonSampleMetadataToTsv: sample_id, condition, condition_label, file_name."""
    rows = ["sample_id\tcondition\tcondition_label\tfile_name"]
    for item, flag in zip(inputs, condition_one):
        rows.append("\t".join(_tsv_cell(v) for v in (item["id"], _condition(flag), label_one if flag else label_two,
                                                    item.get("file_name") or "")))
    return "\n".join(rows)


def top_regions(interactions: Sequence[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """batch-tools.js topRegions: per direction (up, then down) the ten
    interactions with the smallest padj (ties in table order), each with a
    +-150 nt window (lower bound 1)."""
    regions = []
    for direction in ("up", "down"):
        rows = [row for row in interactions if row.get("direction") == direction and row.get("padj") is not None
                and math.isfinite(row["padj"]) and row["padj"] >= 0]
        rows.sort(key=lambda row: row["padj"])  # stable: ties keep table order
        for rank, row in enumerate(rows[:TOP_PER_DIRECTION], start=1):
            x, y = int(row["x"]), int(row["y"])
            regions.append({"direction": direction, "rank": rank, "x": x, "y": y, "padj": row["padj"],
                            "log2FoldChange": row.get("log2FoldChange"),
                            "x1": max(1, x - TOP_FLANK), "x2": x + TOP_FLANK,
                            "y1": max(1, y - TOP_FLANK), "y2": y + TOP_FLANK})
    return regions


def top_maps(keys: np.ndarray, table: np.ndarray, regions: Sequence[Dict[str, Any]]) -> Dict[str, Any]:
    """batch-tools.js mapBatch before the cap: each region's cells
    ``[x, y, count]`` per sample (by x, then y), and the histogram
    ``[[count, cells], ...]`` of every sample's non-empty cells (the pooled
    distribution whose quantile is the shared contrast cap)."""
    xs = keys >> 32
    ys = keys & 0xFFFFFFFF
    values, frequency = np.unique(table[table > 0], return_counts=True)
    panels = []
    for region in regions:
        inside = (xs >= region["x1"]) & (xs <= region["x2"]) & (ys >= region["y1"]) & (ys <= region["y2"])
        rows = np.flatnonzero(inside)
        panel = []
        for column in range(table.shape[1]):
            hit = rows[table[rows, column] > 0]
            panel.append([[int(xs[i]), int(ys[i]), int(table[i, column])] for i in hit])
        panels.append(panel)
    return {"panels": panels, "histogram": [[int(v), int(n)] for v, n in zip(values, frequency)]}


def _write_exports(out_dir: Path, inputs: Sequence[Dict[str, Any]], condition_one: Sequence[bool],
                   label_one: str, label_two: str) -> Dict[str, str]:
    from hyb2analysis import differential

    keys, table = differential.count_table([item["contact"] for item in inputs])
    ids = [item["id"] for item in inputs]
    texts = {"counts": count_table_text(keys, table, ids), "names": names_table_text(ids, condition_one),
             "metadata": metadata_text(inputs, condition_one, label_one, label_two)}
    for key, text in texts.items():
        (out_dir / EXPORT_FILES[key]).write_text(text, encoding="utf-8", newline="\n")
    regions = top_regions(differential.read_interactions(out_dir / "interactions.tsv"))
    maps = {"version": 1, "bin_size": BIN, "flank": TOP_FLANK, "top_per_direction": TOP_PER_DIRECTION,
            "labels": {"A": label_one, "B": label_two}, "regions": regions,
            "samples": [{"id": item["id"], "label": item["label"], "file_name": item.get("file_name") or "",
                         "condition": "A" if flag else "B", "rna_x": item["gene_id"], "rna_y": item["gene2"],
                         "records": item.get("records")} for item, flag in zip(inputs, condition_one)],
            **top_maps(keys, table, regions)}
    _atomic_write_json(out_dir / EXPORT_FILES["top_maps"], maps)
    return dict(EXPORT_FILES)


def _compare_run(request: Dict[str, Any], repo_root: Optional[Path], state: Dict[str, Any]) -> Dict[str, Any]:
    started = time.monotonic()
    raw_path = request.get("project_path")
    if not isinstance(raw_path, str) or not raw_path.strip():
        raise ValueError("project path must be a non-empty string")
    project = Path(raw_path).expanduser().resolve()
    errors = validate_project_directory(project)
    if errors:
        raise ValueError("invalid project: " + "; ".join(errors))
    task_id = _safe_task_id(request.get("task_id") or ("compare-" + os.urandom(8).hex()))
    one = _sample_list(request.get("samples_one"), "samples_one")
    two = _sample_list(request.get("samples_two"), "samples_two")
    if set(one) & set(two):
        raise ValueError("a sample cannot be in both conditions")
    if len(one) + len(two) < 3:
        raise ValueError("a comparison needs at least three samples (two replicates in one condition)")
    if len(one) + len(two) > MAX_SAMPLES:
        raise ValueError(f"at most {MAX_SAMPLES} samples per comparison")
    params = _parameters(request.get("parameters"))

    run_dir = _resolve_run_dir(project, task_id, request.get("run_dir"))
    state.update(run_dir=run_dir, task_id=task_id)
    out_dir = _real_child_dir(project / "comparisons", task_id, exist_ok=True)
    progress = ProgressWriter(run_dir)
    _pipeline_path(repo_root)
    cpus = _env_int("HYB2_TASK_CPUS")
    if cpus:
        try:
            import numba

            numba.set_num_threads(max(1, min(cpus, numba.config.NUMBA_NUM_THREADS)))
        except ImportError:
            pass
    from hyb2analysis import differential

    progress("contacts", "collecting contact maps", None, None, units="none", force=True)
    inputs = _contact_inputs(project, out_dir, one + two, params["gene_id"], params["gene2"], progress)

    def say(stage: str, detail: str) -> None:
        progress(stage, detail, None, None, units="none", force=True)

    summary = differential.compare(
        [item["contact"] for item in inputs], [item["id"] for item in inputs],
        [item["id"] in one for item in inputs], out_dir,
        min_count=params["min_count"], radius=params["range"], padj_cutoff=params["padj_cutoff"],
        similarity_min=params["similarity_min"], progress=say)
    created = _now()
    relative_dir = f"comparisons/{task_id}"
    label_one = params["label_one"] or ", ".join(one)
    label_two = params["label_two"] or ", ".join(two)
    say("exports", "writing the count table and local maps")
    exports = _write_exports(out_dir, inputs, [item["id"] in one for item in inputs], label_one, label_two)
    meta_extra = {"task_id": task_id, "label_one": label_one, "label_two": label_two,
                  "inputs": [{"id": item["id"], "gene_id": item["gene_id"], "gene2": item["gene2"],
                              "contact": str(item["contact"]), "generated": item["generated"],
                              "records": item["records"], "file_name": item["file_name"]} for item in inputs],
                  "exports": exports, "created_at": created}
    summary.update(meta_extra)
    _atomic_write_json(out_dir / "summary.json", summary)
    genes = sorted({item["gene_id"] for item in inputs})
    compact = {key: summary[key] for key in ("cells_tested", "significant", "significant_up", "significant_down",
                                             "interactions", "interactions_up", "interactions_down", "max_coordinate",
                                             "similarity_cells", "similarity_cap")}
    compact["timings"] = summary["timings"]
    comparison = {
        "id": task_id, "task_id": task_id, "samples_one": one, "samples_two": two,
        "label_one": label_one, "label_two": label_two, "genes": genes,
        "parameters": {key: params[key] for key in ("min_count", "range", "padj_cutoff", "similarity_min", "gene_id",
                                                    "gene2")},
        "created_at": created, "path": relative_dir,
        "paths": {key: f"{relative_dir}/{name}" for key, name in _FILES.items()},
        "summary": compact,
    }
    result = {"ok": True, "status": "succeeded", "task_id": task_id, "run_dir": str(run_dir),
              "output": str(out_dir / "significant.tsv"), "output_dir": str(out_dir),
              "comparison": comparison, "summary": compact,
              "seconds": round(time.monotonic() - started, 2)}
    progress("done", "finished", None, None, units="none", force=True)
    _atomic_write_json(run_dir / "result.json", result)
    return result


def compare_run(request: Dict[str, Any], repo_root: Optional[Path] = None) -> Dict[str, Any]:
    """Bridge entry point.  Invalid requests raise ``ValueError``; failures after
    the run directory exists are reported in ``result.json`` and returned."""
    state: Dict[str, Any] = {}
    try:
        return _compare_run(request, repo_root, state)
    except Exception as error:  # noqa: BLE001 - every failure must reach result.json
        run_dir = state.get("run_dir")
        if run_dir is None:
            raise
        result = {"ok": False, "status": "failed", "task_id": state.get("task_id"), "run_dir": str(run_dir),
                  "error": str(error) or error.__class__.__name__}
        ProgressWriter(run_dir)("failed", result["error"], None, None, units="none", force=True)
        _atomic_write_json(run_dir / "result.json", result)
        return result


# ---------------------------------------------------------------------------
# registered results (list_comparisons / read_comparison)
# ---------------------------------------------------------------------------

def _comparison_file(project: Path, entry: Dict[str, Any], key: str) -> Optional[Path]:
    relative = (entry.get("paths") or {}).get(key) if isinstance(entry.get("paths"), dict) else None
    if not safe_relative_path(relative):
        return None
    target = (project / relative).resolve()
    base = (project / "comparisons").resolve()
    if base not in target.parents or not target.is_file():
        return None
    return target


def list_comparisons(project: Path) -> List[Dict[str, Any]]:
    """``comparisons[]`` newest first, each with ``available`` (its files still exist)."""
    project = Path(project).expanduser().resolve()
    manifest = load_manifest(project)
    out = []
    for entry in manifest.get("comparisons") or []:
        if isinstance(entry, dict):
            out.append({**entry, "available": _comparison_file(project, entry, "significant") is not None})
    out.sort(key=lambda item: str(item.get("created_at") or ""), reverse=True)
    return out


def read_comparison(project: Path, comparison_id: str, *, limit: Optional[int] = 250_000) -> Dict[str, Any]:
    """``{comparison, summary, cells: {x, y, log2FoldChange, padj, baseMean}, cells_total,
    truncated, interactions: [...], similarity: {x, y, count} | null}``; cells are the
    significant 10-nt cells by padj; similarity is null for comparisons made
    before it existed."""
    _pipeline_path(None)
    from hyb2analysis import differential

    project = Path(project).expanduser().resolve()
    manifest = load_manifest(project)
    entry = next((item for item in manifest.get("comparisons") or []
                  if isinstance(item, dict) and item.get("id") == comparison_id), None)
    if entry is None:
        raise ValueError("comparison does not exist: %s" % comparison_id)
    significant = _comparison_file(project, entry, "significant")
    interactions = _comparison_file(project, entry, "interactions")
    if significant is None or interactions is None:
        raise ValueError("comparison files are missing: %s" % comparison_id)
    if limit is not None and (isinstance(limit, bool) or not isinstance(limit, int) or limit < 1):
        raise ValueError("limit must be a positive integer")
    summary: Dict[str, Any] = {}
    summary_path = _comparison_file(project, entry, "summary")
    if summary_path is not None:
        try:
            loaded = json.loads(summary_path.read_text(encoding="utf-8"))
            summary = loaded if isinstance(loaded, dict) else {}
        except ValueError:
            summary = {}
    cells = differential.read_significant(significant, limit=limit)
    total = int(summary.get("significant", len(cells["x"])))
    similarity_path = _comparison_file(project, entry, "similarity")
    similarity = differential.read_similarity(similarity_path, limit=limit) if similarity_path is not None else None
    return {"comparison": entry, "summary": summary, "cells": cells, "cells_total": total,
            "truncated": len(cells["x"]) < total, "interactions": differential.read_interactions(interactions),
            "similarity": similarity}
