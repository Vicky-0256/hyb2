"""Evidence-guided CPLfold structure runs (bridge operation ``structure_run``).

Two modes share one request shape::

    {"operation": "structure_run", "project_path": ..., "task_id": ..., "run_dir": ...,
     "sample_ids": [...] | "hyb_paths": [...], "reference": <fasta path>, "gene_id": ...,
     "mode": "genome" | "windows", "windows": [{"start": 1, "end": 1500}, ...],
     "parameters": {"alpha": 0.5, "beam": 20, "allow_pseudoknot": ..., ...},
     "workers": <int>, "memory_budget_mb": <int>}

* ``genome`` folds the whole reference sequence once (pseudoknot-free by
  default: Phase 1 only) with the HYB bonus of every intramolecular chimera
  of ``gene_id``.
* ``cofold`` (engine ``cplcofold``) folds two regions as two strands with
  CPLcofold: ``"regions": [{"gene_id", "start", "end"}, {...}]``, of one RNA
  or of two RNAs of the samples' reference.  The evidence is every chimera
  with one arm in each region, or both arms in one region, as the web's
  two-region CPLcofold layout.
* ``windows`` folds each window separately (pseudoknots allowed by default),
  with the chimeras whose two arms both lie inside that window, in a process
  pool: one window per process, ``numba.set_num_threads(1)``, and
  ``min(cpu, memory budget / per-window estimate, windows)`` workers.

``parameters.evidence`` ``"none"`` folds the sequence alone (the web's
sequence-only CPLfold); the samples then only give the reference.

``"source": "paste" | "record"`` folds a pasted or HYB record ``sequence``
(CPLcofold: ``sequence_two``) instead of a reference region; see
:func:`_sequence_source` for when its chimeras are used.

The .hyb files are streamed in the sidecar; arms never pass through the
WebView.  Results go to ``<project>/structures/<task_id>/`` (structure.dbn,
pairs.tsv, structure.ct, meta.json); the task's ``run_dir`` gets
``progress.json`` while running and ``result.json`` at the end.
"""
from __future__ import annotations

import importlib.util
import json
import math
import os
import re
import sys
import time
from collections import Counter
from concurrent.futures import ProcessPoolExecutor, as_completed
from datetime import datetime, timezone
from multiprocessing import get_context
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple

from .detector import CPLFOLD_MEMORY_FRACTION, cplfold_limits, cplfold_memory_estimate, memory_snapshot
from .project import _atomic_write_json, load_manifest, safe_relative_path, validate_project_directory
from .samples import _hyb2_reference_name
from . import trust as _trust
from . import comrades as _comrades
from . import vienna as _vienna

_TASK_ID_CHARS = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-"
MAX_WINDOWS = 512
MAX_HYB_FILES = 64
MIN_WINDOW_NT = 10
# The web RNA Structure page's CPLfold defaults (web/analysis-data.js
# defaultStructureState), so a fold of the same inputs gives the same result.
DEFAULTS = {
    "alpha": 0.5,
    "beam": 20,
    "max_phase1": 3,
    "energy_delta": 5.0,
    "beta": 0.0,
    "energy_model": "DP09",
}
_BOUNDS = {
    "alpha": (0.0, 1.0, float),
    "beam": (1, 1000, int),
    "max_phase1": (1, 20, int),
    "energy_delta": (0.0, 50.0, float),
    "beta": (0.0, 1.0, float),
}
_ALIASES = {"beamSize": "beam", "maxPhase1": "max_phase1", "energyDelta": "energy_delta", "delta": "energy_delta",
            "energyModel": "energy_model", "model": "energy_model", "allowPseudoknot": "allow_pseudoknot"}
ENERGY_MODELS = {"DP03", "DP09", "CC06", "CC09", "RE"}
# CPLcofold has no nicked CC/RE pseudoknot models, and the shared module's
# beam bound (web/cplfold/cplfold_web.py fold).
COFOLD_MODELS = {"DP03", "DP09"}
COFOLD_MAX_BEAM = 200
BRACKET_CLOSE = {")": "(", "]": "[", "}": "{", ">": "<"}
# CPLfold candidates kept per record for the desktop's candidate table (the
# web lists them all: at most 20 phase-1 candidates, one phase 2 each).
CANDIDATE_LIMIT = 40
# The bonus matrix is written for a local CPLfold run (bin/cplfold
# --bonus-matrix-file) up to this many upper-triangle cells (~50 MB).
BONUS_EXPORT_MAX_CELLS = 2_000_000
BONUS_TRANSFORM = "IRIS-style Gaussian arm blocks; symmetric outer product; threshold 1e-6; log1p"

# cplfold_web modules loaded in this process, keyed by source path.
_MODULES: Dict[str, Any] = {}


# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

def _now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _peak_rss_bytes() -> int:
    try:
        import resource  # POSIX only; Windows has no getrusage
    except ImportError:
        return 0
    value = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    return int(value if sys.platform == "darwin" else value * 1024)


def load_cplfold_web(root: Path) -> Any:
    """Import ``web/cplfold/cplfold_web.py`` with the vendored CPLfold on the path."""
    source = Path(root) / "web" / "cplfold" / "cplfold_web.py"
    key = str(source)
    if key in _MODULES:
        return _MODULES[key]
    if not source.is_file():
        raise FileNotFoundError("native CPLfold source is not bundled")
    for extra in (str(Path(root) / "third_party" / "cplfold"), str(source.parent)):
        if extra not in sys.path:
            sys.path.insert(0, extra)
    spec = importlib.util.spec_from_file_location("hyb2_structure_cplfold", source)
    if spec is None or spec.loader is None:
        raise ImportError("native CPLfold module cannot be loaded")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    _MODULES[key] = module
    return module


class ProgressWriter:
    """Atomic ``run_dir/progress.json`` writes, throttled to ~2 per second."""

    def __init__(self, run_dir: Optional[Path], interval: float = 0.5):
        self.path = run_dir / "progress.json" if run_dir is not None else None
        self.interval = interval
        self._last = 0.0
        # What is folded ({lengths, beam, workers}): the desktop estimates
        # the time left from it.
        self.work: Optional[Dict[str, Any]] = None

    def __call__(self, stage: str, detail: str, done: Optional[int] = None, total: Optional[int] = None,
                 *, units: str = "bytes", force: bool = False) -> None:
        now = time.monotonic()
        if self.path is None or (not force and now - self._last < self.interval):
            return
        self._last = now
        payload = {"stage": stage, "detail": detail, "bytes_done": done, "bytes_total": total,
                   "units": units, "updated_at": _now()}
        if self.work:
            payload["work"] = self.work
        try:
            _atomic_write_json(self.path, payload)
        except OSError:
            pass


def _compiled_kernels() -> Optional[int]:
    """Numba's cached kernels (NUMBA_CACHE_DIR), or None when unknown: a fold
    that adds some compiled them, which is not folding speed."""
    cache = os.environ.get("NUMBA_CACHE_DIR")
    if not cache or not Path(cache).is_dir():
        return None
    return sum(1 for _ in Path(cache).rglob("*.nbc"))


def _env_int(name: str) -> Optional[int]:
    try:
        value = int(os.environ.get(name, ""))
    except ValueError:
        return None
    return value if value > 0 else None


def _safe_task_id(value: Any) -> str:
    task_id = str(value or ("structure-" + os.urandom(8).hex()))
    if not task_id or len(task_id) > 64 or not task_id[0].isalpha() or any(c not in _TASK_ID_CHARS for c in task_id):
        raise ValueError("task_id must be a safe identifier")
    return task_id


def _real_child_dir(parent: Path, name: str, *, exist_ok: bool) -> Path:
    """``parent/name`` as a real directory, never following a symlink."""
    if parent.exists() and (parent.is_symlink() or not parent.is_dir()):
        raise ValueError(f"project {parent.name} directory must be a real directory")
    parent.mkdir(mode=0o700, exist_ok=True)
    target = parent / name
    if target.is_symlink():
        raise ValueError(f"{parent.name}/{name} must not be a symlink")
    if target.exists():
        if not exist_ok or not target.is_dir():
            raise FileExistsError(f"{parent.name}/{name} already exists")
        return target
    target.mkdir(mode=0o700)
    return target


def _resolve_run_dir(project: Path, task_id: str, requested: Any) -> Path:
    runs_root = (project / "runs").resolve()
    if requested is not None:
        run_dir = Path(str(requested)).expanduser().resolve()
        if not run_dir.is_dir() or run_dir.parent != runs_root:
            raise ValueError("run_dir must be an existing direct child of project runs")
        return run_dir
    return _real_child_dir(project / "runs", task_id, exist_ok=False)


def _input_file(project: Path, value: Any, label: str) -> Path:
    """A project-relative path inside the project, or an absolute path (linked data)."""
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a non-empty path")
    if os.path.isabs(value) or value.startswith("~"):
        _trust.require(project, value, label)
        target = Path(value).expanduser().resolve()
    else:
        if not safe_relative_path(value):
            raise ValueError(f"{label} must be a safe project-relative or absolute path")
        target = (project / value).resolve()
        if project not in target.parents:
            raise ValueError(f"{label} escapes the project")
    if not target.is_file():
        raise ValueError(f"{label} does not exist: {value}")
    return target


def read_fasta(path: Path) -> List[Tuple[str, str]]:
    """``(name, sequence)`` records.  Names follow ``samples.read_fasta_sequences``:
    hyb2 ``|``-headers are renamed the way the pipeline does, so they match
    .hyb gene IDs; otherwise the first header word."""
    headers: List[str] = []
    chunks: List[List[str]] = []
    opener = __import__("gzip").open if str(path).endswith(".gz") else open
    with opener(path, "rt", encoding="utf-8", errors="replace") as handle:
        for line in handle:
            line = line.strip()
            if line.startswith(">"):
                headers.append(line[1:].strip())
                chunks.append([])
            elif line and chunks:
                chunks[-1].append(line)
    formatted = any("|" in header for header in headers)
    records = []
    for header, parts in zip(headers, chunks):
        name = _hyb2_reference_name(header) if formatted else header
        records.append(((name.split() or [""])[0], "".join(parts)))
    return records


def _reference_sequence(path: Path, gene_id: Optional[str]) -> Tuple[str, str]:
    records = read_fasta(path)
    if not records:
        raise ValueError("reference FASTA has no sequences")
    chosen = None
    if gene_id is not None:
        chosen = next((record for record in records if record[0] == gene_id), None)
        if chosen is None:
            # Even with one sequence: folding it under gene_id's name would
            # mislabel the result (e.g. evidence of another reference).
            raise ValueError(f"gene_id {gene_id} is not a sequence of the reference FASTA")
    if chosen is None:
        if len(records) > 1:
            raise ValueError("the reference FASTA has several sequences; give gene_id")
        chosen = records[0]
    name, sequence = chosen
    sequence = sequence.upper().replace("T", "U")
    bad = sorted(set(sequence) - set("ACGU"))
    if bad:
        raise ValueError("CPLfold accepts only A, C, G and U; the reference contains " + ", ".join(bad))
    return gene_id or name, sequence


def _parameters(raw: Any, mode: str) -> Dict[str, Any]:
    raw = raw if isinstance(raw, dict) else {}
    values: Dict[str, Any] = dict(DEFAULTS)
    values["allow_pseudoknot"] = False  # the web's default topology: secondary structure only
    values["evidence"] = "hyb"
    for key, value in raw.items():
        key = _ALIASES.get(key, key)
        if key in _BOUNDS:
            lo, hi, kind = _BOUNDS[key]
            if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) \
                    or not lo <= value <= hi or (kind is int and value != int(value)):
                raise ValueError(f"structure parameter out of bounds: {key}")
            values[key] = kind(value)
        elif key == "allow_pseudoknot":
            if not isinstance(value, bool):
                raise ValueError("allow_pseudoknot must be a boolean")
            values[key] = value
        elif key == "evidence":
            if value not in ("hyb", "none"):
                raise ValueError("evidence must be 'hyb' (the chimeras) or 'none' (the sequence alone)")
            values[key] = value
        elif key == "energy_model":
            model = str(value).upper()
            if model not in ENERGY_MODELS:
                raise ValueError("energy_model must be one of DP03, DP09, CC06, CC09 or RE")
            values[key] = model
        else:
            raise ValueError(f"unknown structure parameter: {key}")
    return values


def _windows(raw: Any, length: int) -> List[Tuple[int, int]]:
    if not isinstance(raw, list) or not raw:
        raise ValueError("windows mode needs a non-empty windows list")
    if len(raw) > MAX_WINDOWS:
        raise ValueError(f"at most {MAX_WINDOWS} windows per run")
    windows = []
    for index, window in enumerate(raw):
        try:
            start, end = int(window["start"]), int(window["end"])
        except (KeyError, TypeError, ValueError) as error:
            raise ValueError(f"window {index + 1} needs integer start and end") from error
        if not 1 <= start <= end <= length:
            raise ValueError(f"window {index + 1} ({start}-{end}) is outside 1-{length}")
        if end - start + 1 < MIN_WINDOW_NT:
            raise ValueError(f"window {index + 1} is shorter than {MIN_WINDOW_NT} nt")
        windows.append((start, end))
    return windows


def _evidence_inputs(project: Path, request: Dict[str, Any]) -> Dict[str, Any]:
    """Resolve .hyb paths, the reference FASTA and gene_id from samples or explicit paths."""
    sample_ids = request.get("sample_ids")
    hyb_paths = request.get("hyb_paths")
    reference = request.get("reference")
    gene_id = request.get("gene_id")
    if gene_id is not None and (not isinstance(gene_id, str) or not gene_id.strip()):
        raise ValueError("gene_id must be a non-empty string")
    samples_used: List[Dict[str, Any]] = []
    paths: List[Path] = []
    if sample_ids:
        if not isinstance(sample_ids, list) or not all(isinstance(item, str) for item in sample_ids):
            raise ValueError("sample_ids must be a list of sample ids")
        manifest = json.loads((project / "project.json").read_text(encoding="utf-8"))
        samples = {sample.get("id"): sample for sample in manifest.get("samples") or [] if isinstance(sample, dict)}
        references = {item.get("id"): item for item in manifest.get("references") or [] if isinstance(item, dict)}
        reference_ids = set()
        genes = set()
        for sample_id in sample_ids:
            sample = samples.get(sample_id)
            if sample is None:
                raise ValueError(f"unknown sample: {sample_id}")
            hyb = (sample.get("outputs") or {}).get("hyb")
            reads = sample.get("reads") or {}
            if not hyb and reads.get("format") == "hyb":
                hyb = reads.get("path")  # a linked or copied .hyb is its own output
            if not hyb:
                raise ValueError(f"sample {sample_id} has no .hyb output yet")
            paths.append(_input_file(project, hyb, f"sample {sample_id} .hyb"))
            reference_ids.add(sample.get("reference_id"))
            if sample.get("gene_id"):
                genes.add(sample["gene_id"])
            samples_used.append({"id": sample_id, "hyb": str(paths[-1]), "reference_id": sample.get("reference_id"),
                                 "gene_id": sample.get("gene_id"), "condition": sample.get("condition")})
        if reference is None:
            if len(reference_ids) != 1 or None in reference_ids:
                raise ValueError("the selected samples must share one reference")
            entry = references.get(next(iter(reference_ids)))
            if not entry or not entry.get("path"):
                raise ValueError("the samples' reference is missing from the project")
            reference = entry["path"]
        if gene_id is None and len(genes) == 1:
            gene_id = next(iter(genes))
        elif gene_id is None and len(genes) > 1:
            raise ValueError("the selected samples have different gene_id values; give gene_id")
    if hyb_paths:
        if not isinstance(hyb_paths, list) or len(hyb_paths) > MAX_HYB_FILES:
            raise ValueError(f"hyb_paths must be a list of at most {MAX_HYB_FILES} paths")
        paths.extend(_input_file(project, value, "hyb path") for value in hyb_paths)
    if not paths:
        raise ValueError("structure_run needs sample_ids or hyb_paths")
    if len(paths) > MAX_HYB_FILES:
        raise ValueError(f"at most {MAX_HYB_FILES} .hyb files per run")
    if reference is None:
        raise ValueError("structure_run needs a reference FASTA (reference) when no samples are given")
    reference_path = _input_file(project, reference, "reference")
    return {"hyb_paths": paths, "reference": reference_path, "gene_id": gene_id, "samples": samples_used}


# ---------------------------------------------------------------------------
# pasted and HYB record sequences (request "source": "paste" | "record")
# ---------------------------------------------------------------------------

def _clean_sequence(raw: Any) -> str:
    """A pasted sequence read as the web does (normaliseFoldSequence): FASTA
    header lines and whitespace dropped, upper case, T read as U."""
    if not isinstance(raw, str):
        raise ValueError("sequence must be text")
    lines = [line for line in raw.replace("\r", "").split("\n") if not line.strip().startswith(">")]
    return "".join("".join(lines).split()).upper().replace("T", "U")


def _locate(strand: str, records: Dict[str, str]) -> Optional[Tuple[str, int, int]]:
    """``(gene, start, end)`` where strand occurs in the reference, when it
    occurs exactly once; None when absent or repeated."""
    hits: List[Tuple[str, int, int]] = []
    for name, sequence in records.items():
        at = sequence.find(strand)
        while at != -1 and len(hits) < 2:
            hits.append((name, at + 1, at + len(strand)))
            at = sequence.find(strand, at + 1)
        if len(hits) > 1:
            return None
    return hits[0] if hits else None


def _sequence_source(request: Dict[str, Any], project: Path, engine: str,
                     mode: str) -> Tuple[Dict[str, Any], Optional[Dict[str, Any]]]:
    """A pasted or HYB record sequence (``sequence``, and for CPLcofold
    ``sequence_two`` or one "A&B" sequence).  As the web, chimeras guide only
    the fold of a reference region: when evidence is asked for and every
    strand occurs exactly once in the samples' reference, it is folded as
    that region (one window, or CPLcofold's two regions); otherwise from the
    sequence alone (COMRADES, which needs chimeras, cannot run).  Returns
    the request to run and the source to record (None: a reference region)."""
    request = {key: value for key, value in request.items() if not key.startswith("_")}
    source = request.get("source", "reference")
    if source == "reference":
        if request.get("sequence") is not None or request.get("sequence_two") is not None:
            raise ValueError("a sequence needs source 'paste' or 'record'")
        return request, None
    if source not in ("paste", "record"):
        raise ValueError("source must be 'reference', 'paste' or 'record'")
    if mode not in ("genome", "cofold") or request.get("windows") or request.get("regions"):
        raise ValueError("a pasted or HYB record sequence is folded whole, or as two strands with CPLcofold")
    first = _clean_sequence(request.get("sequence"))
    second = _clean_sequence(request["sequence_two"]) if request.get("sequence_two") is not None else ""
    if mode == "cofold":
        if source == "record":
            raise ValueError("a HYB record sequence is one chimeric read, not two strands")
        if "&" in first:
            parts = first.split("&")
            if len(parts) != 2 or second:
                raise ValueError("give RNA A and RNA B separately, or one A&B sequence with RNA B empty")
            first, second = parts
        strands = [first, second]
    elif second:
        raise ValueError("sequence_two is only for CPLcofold")
    else:
        strands = [first]
    what = "pasted sequence" if source == "paste" else "HYB record sequence"
    for index, strand in enumerate(strands):
        name = what if len(strands) == 1 else ("RNA A", "RNA B")[index]
        bad = sorted(set(strand) - set("ACGU"))
        if bad:
            raise ValueError(f"the {name} may contain only A, C, G and U (T is read as U); found " + ", ".join(bad))
        if len(strand) < MIN_WINDOW_NT:
            raise ValueError(f"the {name} is shorter than {MIN_WINDOW_NT} nt")
    label = "_".join(str(request.get("gene_id") or "pasted").split())
    names = [label] if len(strands) == 1 else [label + "_A", label + "_B"]
    params = request.get("parameters") if isinstance(request.get("parameters"), dict) else {}
    guided = engine == "comrades" or (engine in ("cplfold", "cplcofold") and params.get("evidence", "hyb") != "none")
    found: List[Optional[Tuple[str, int, int]]] = []
    if guided:
        inputs = _evidence_inputs(project, request)
        records = {name: sequence.upper().replace("T", "U") for name, sequence in read_fasta(inputs["reference"])}
        found = [_locate(strand, records) for strand in strands]
    out = {key: value for key, value in request.items() if key not in ("source", "sequence", "sequence_two")}
    recorded: Dict[str, Any] = {"kind": source, "label": label, "lengths": [len(strand) for strand in strands]}
    if found and all(found):
        recorded["mapped_to"] = [f"{gene}:{start}-{end}" for gene, start, end in found]  # type: ignore[misc]
        regions = [{"gene_id": gene, "start": start, "end": end} for gene, start, end in found]  # type: ignore[misc]
        if mode == "cofold":
            out["regions"] = regions
        else:
            out.update(gene_id=regions[0]["gene_id"], mode="windows", windows=[{"start": regions[0]["start"], "end": regions[0]["end"]}])
        return out, recorded
    if engine == "comrades":
        raise ValueError(f"COMRADES(ViennaRNA) needs the chimeras of a reference region, and the {what} does not occur "
                         "exactly once in the samples' reference: fold it with ViennaRNA MFE or sequence-only CPLfold, "
                         "or choose Reference region")
    if guided:
        out["parameters"] = {**params, "evidence": "none"}
        recorded["evidence"] = "none: the sequence does not occur exactly once in the samples' reference"
    # folded from the sequence alone, under its label (two strands: two regions)
    out["gene_id"], out["_strands"] = names[0], dict(zip(names, strands))
    if mode == "cofold":
        out["regions"] = [{"gene_id": name, "start": 1, "end": len(strand)} for name, strand in zip(names, strands)]
    return out, recorded


def _fold_sequence(request: Dict[str, Any], inputs: Dict[str, Any]) -> Tuple[str, str]:
    """``(gene_id, sequence)`` to fold: a pasted or HYB record sequence, else
    gene_id's reference sequence."""
    strands = request.get("_strands")
    if strands:
        return request["gene_id"], strands[request["gene_id"]]
    return _reference_sequence(inputs["reference"], inputs["gene_id"])


def _record_source(result: Dict[str, Any], source: Dict[str, Any]) -> None:
    """Name a pasted or HYB record sequence in a finished result: the
    project.json entry's parameters, meta.json and result.json."""
    entry = result["structure"]
    entry["parameters"] = {**entry.get("parameters", {}), "sequence_source": source["kind"],
                           "sequence_label": source["label"],
                           **({"sequence_mapped_to": source["mapped_to"]} if "mapped_to" in source else {})}
    meta_path = Path(result["outputs"]["meta"])
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    meta["parameters"], meta["sequence_source"] = entry["parameters"], source
    if "mapped_to" not in source and meta.get("mode") in ("genome", "windows"):
        meta["coordinates"] = "1-based inclusive positions on the folded sequence (pairs.tsv i/j, ct column 6)"
    _atomic_write_json(meta_path, meta)
    _atomic_write_json(Path(result["run_dir"]) / "result.json", result)


# ---------------------------------------------------------------------------
# folding (runs in the sidecar for genome mode, in pool workers for windows)
# ---------------------------------------------------------------------------

def _pairs(structure: str) -> List[Tuple[int, int, str]]:
    stacks: Dict[str, List[int]] = {opening: [] for opening in BRACKET_CLOSE.values()}
    pairs = []
    for position, character in enumerate(structure, 1):
        if character in stacks:
            stacks[character].append(position)
        elif character in BRACKET_CLOSE:
            opening = BRACKET_CLOSE[character]
            if not stacks[opening]:
                raise ValueError("CPLfold returned unbalanced dot-bracket notation")
            pairs.append((stacks[opening].pop(), position, opening + character))
    pairs.sort()
    return pairs


def _number(value: Any) -> Optional[float]:
    """A JSON number from CPLfold's (possibly NumPy) value; None when absent or not finite."""
    try:
        number = float(value)
    except (TypeError, ValueError):
        return None
    return number if math.isfinite(number) else None


def _candidate(rank: int, raw: Dict[str, Any], structure: str,
               pairs: List[Tuple[int, int, str, Optional[float]]]) -> Dict[str, Any]:
    """One CPLfold candidate as the desktop lists it (the web's candidate:
    energy, beta-adjusted effective energy, phase-1 energy, LinearFold
    score), with each pair's HYB bonus aligned with the dot-bracket's pairs."""
    knotted = sum(1 for pair in pairs if pair[2] != "()")
    kind = str(raw.get("type") or "phase1")
    with_bonus = any(pair[3] is not None for pair in pairs)
    return {
        "rank": rank, "type": kind, "topology": "pseudoknotted" if knotted or kind == "pseudoknot" else "nested",
        "energy": _number(raw.get("energy")), "effective_energy": _number(raw.get("effective_energy")),
        "phase1_energy": _number(raw.get("phase1_energy")), "score": _number(raw.get("score")),
        "structure": structure, "pairs": len(pairs), "pseudoknot_pairs": knotted,
        "pairs_with_bonus": sum(1 for pair in pairs if (pair[3] or 0) > 0),
        "bonus": [pair[3] for pair in pairs] if with_bonus else None,
    }


def _bonus_header(length: int, label: str, cutpoint: Optional[int] = None) -> str:
    """The web's bonus-matrix TSV header (web/structure-ui.js cplfoldEvidenceTsv),
    which CPLfold.py load_bonus_matrix_file reads; the region line is a comment."""
    lines = ["# HYB2 CPLfold bonus matrix", f"# region={label}", f"# sequence_length={length}",
             "# coordinate_system=prepared-sequence-1-based-inclusive", f"# transform={BONUS_TRANSFORM}"]
    if cutpoint is not None:
        lines += [f"# cutpoint={cutpoint}", f"# strand_lengths={cutpoint},{length - cutpoint}",
                  "# cofold_evidence_smoothing=within_each_strand"]
    lines.append("prepared_position_1\tprepared_position_2\tlog1p_gaussian_hyb_bonus")
    return "\n".join(lines) + "\n"


def write_bonus_matrix(path: Path, bonus: Any, label: str) -> int:
    """The sparse upper triangle of a dense bonus matrix, 1-based, eight
    decimals as the web's export; returns the cells written."""
    import numpy as np

    n = int(bonus.shape[0])
    temporary = path.with_name("." + path.name + ".tmp")
    cells = 0
    with open(temporary, "w", encoding="utf-8") as handle:
        handle.write(_bonus_header(n, label))
        for row in range(n - 1):
            values = bonus[row, row + 1:]
            columns = np.flatnonzero(values)
            if columns.size:
                handle.write("".join(f"{row + 1}\t{row + 2 + int(column)}\t{float(values[column]):.8f}\n"
                                     for column in columns))
                cells += int(columns.size)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)
    return cells


def _nucleotide_support(bonus: Any) -> Optional[List[float]]:
    """Each nucleotide's strongest HYB bonus (the web's nucleotideSupport)."""
    if bonus is None:
        return None
    return [round(float(value), 6) for value in bonus.max(axis=1)]


def fold_one(module: Any, sequence: str, arms: Any, params: Dict[str, Any],
             bonus_path: Optional[str] = None, label: str = "") -> Dict[str, Any]:
    """Build the bonus from compressed arms and fold; returns a JSON-able record.
    With ``bonus_path`` the bonus matrix is also written there (when it is
    small enough, see BONUS_EXPORT_MAX_CELLS)."""
    started = time.monotonic()
    bonus, evidence = module.build_hyb_bonus_matrix_fast(len(sequence), arms)
    bonus_seconds = time.monotonic() - started
    bonus_file = None
    if bonus_path and bonus is not None and int(evidence.get("nonzeroUpperTriangleCells") or 0) <= BONUS_EXPORT_MAX_CELLS:
        write_bonus_matrix(Path(bonus_path), bonus, label)
        bonus_file = Path(bonus_path).name
    started = time.monotonic()
    results = module.two_phase_pseudoknot_fold(
        seq=sequence, beam_size=params["beam"], energy_delta=params["energy_delta"],
        max_phase1=params["max_phase1"], max_phase2=1, energy_model=params["energy_model"],
        output_file=None, verbose=False, lv=True, bonus_matrix=bonus,
        alpha=params["alpha"] if bonus is not None else 0.0, beta=params["beta"],
        allow_pseudoknot=params["allow_pseudoknot"])
    fold_seconds = time.monotonic() - started
    if not results:
        raise ValueError("CPLfold did not produce a structure")
    # bonus None: folded from the sequence alone, the pairs carry no HYB evidence
    candidates = []
    for rank, raw in enumerate(results[:CANDIDATE_LIMIT]):
        folded = [(i, j, bracket, float(bonus[i - 1, j - 1]) if bonus is not None else None)
                  for i, j, bracket in _pairs(raw["structure"])]
        candidates.append((raw, folded))
    best, pairs = candidates[0]
    support = _nucleotide_support(bonus)
    del bonus
    return {
        "structure": best["structure"],
        "energy": best.get("energy"),
        "type": best.get("type"),
        "pairs": pairs,
        "evidence": evidence,
        "candidates": [_candidate(rank, raw, raw["structure"], folded) for rank, (raw, folded) in enumerate(candidates)],
        "candidates_found": len(results),
        "nucleotide_support": support,
        "bonus_matrix": bonus_file,
        "seconds": {"bonus": round(bonus_seconds, 2), "fold": round(fold_seconds, 2)},
        "peak_rss_bytes": _peak_rss_bytes(),
    }


def _worker_init(root: str) -> None:
    os.environ["NUMBA_NUM_THREADS"] = "1"
    try:
        import numba

        numba.set_num_threads(1)
    except (ImportError, AttributeError, ValueError):
        pass
    load_cplfold_web(Path(root))


def _fold_window(root: str, index: int, sequence: str, blocks: List[Tuple[int, int, int, int]],
                 weights: List[int], params: Dict[str, Any], bonus_path: Optional[str] = None,
                 label: str = "") -> Tuple[int, Dict[str, Any]]:
    module = load_cplfold_web(Path(root))
    arms = dict(zip(blocks, weights))
    return index, fold_one(module, sequence, arms, params, bonus_path, label)


# ---------------------------------------------------------------------------
# output
# ---------------------------------------------------------------------------

def _atomic_write_text(path: Path, text: str) -> None:
    temporary = path.with_name("." + path.name + ".tmp")
    with open(temporary, "w", encoding="utf-8") as handle:
        handle.write(text)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)


def _describe(params: Dict[str, Any]) -> str:
    """The .dbn header's account of how a record was folded."""
    if params.get("engine") == "COMRADES(ViennaRNA)":
        return (f"COMRADES(ViennaRNA), {params['temperature']:g} °C, min loop {params['min_loop']}, "
                f"{params['random_folds']} randomised folds, seed {_comrades.seed_label(params['seed'])}")
    if params.get("engine") == "ViennaRNA":
        hard = f", {len(params['hard_pairs'])} hard pairs" if params.get("hard_pairs") else ""
        return f"ViennaRNA MFE, {params['temperature']:g} °C, min loop {params['min_loop']}{hard}"
    mode = "pseudoknot" if params["allow_pseudoknot"] else "pseudoknot-free"
    name = "CPLcofold" if params.get("engine") == "CPLcofold" else "CPLfold"
    if params.get("evidence") == "none":
        return f"{name} {mode}, sequence only, beam {params['beam']}"
    return f"{name} {mode}, alpha {params['alpha']}, beam {params['beam']}"


def _write_outputs(out_dir: Path, records: List[Dict[str, Any]], params: Dict[str, Any]) -> Dict[str, str]:
    dbn, ct = [], []
    tsv = ["window_start\twindow_end\ti\tj\tbracket\tbonus"]
    describe = _describe(params)
    for record in records:
        start, end = record["start"], record["end"]
        energy = record["energy"]
        energy_text = f"{energy:.2f}" if isinstance(energy, (int, float)) else "NA"
        label = f"{record['gene_id']}:{start}-{end}"
        dbn.append(f">{label} {describe}, energy {energy_text}\n"
                   f"{record['sequence']}\n{record['structure']}\n")
        partner = {}
        for i, j, bracket, bonus in record["pairs"]:
            partner[i], partner[j] = j, i
            # the bonus at full precision (the web's pairs TSV); empty without HYB evidence
            bonus_text = "" if bonus is None else repr(float(bonus))
            tsv.append(f"{start}\t{end}\t{start + i - 1}\t{start + j - 1}\t{bracket}\t{bonus_text}")
        n = len(record["sequence"])
        ct.append(f"{n}\tENERGY = {energy_text}\t{label}\n")
        for k, base in enumerate(record["sequence"], 1):
            ct.append(f"{k}\t{base}\t{k - 1}\t{k + 1 if k < n else 0}\t{partner.get(k, 0)}\t{start + k - 1}\n")
    paths = {"dbn": out_dir / "structure.dbn", "pairs": out_dir / "pairs.tsv", "ct": out_dir / "structure.ct"}
    _atomic_write_text(paths["dbn"], "".join(dbn))
    _atomic_write_text(paths["pairs"], "\n".join(tsv) + "\n")
    _atomic_write_text(paths["ct"], "".join(ct))
    _write_candidates(out_dir, records, paths)
    return {key: str(value) for key, value in paths.items()}


def _write_candidates(out_dir: Path, records: List[Dict[str, Any]], paths: Dict[str, Path]) -> None:
    """candidates.json: each CPLfold record's candidates (best first) and
    its nucleotides' HYB support, aligned with the records of structure.dbn.
    Nothing for engines without candidates (ViennaRNA, COMRADES)."""
    if not any(record.get("candidates") for record in records):
        return
    body = {"version": 1, "records": [
        {"start": record["start"], "end": record["end"], "candidates": record.get("candidates") or [],
         "candidates_found": record.get("candidates_found", len(record.get("candidates") or [])),
         "nucleotide_support": record.get("nucleotide_support")} for record in records]}
    paths["candidates"] = out_dir / "candidates.json"
    _atomic_write_json(paths["candidates"], body)


def _summary(record: Dict[str, Any]) -> Dict[str, Any]:
    evidence = record["evidence"]
    pairs = record["pairs"]
    return {
        "start": record["start"], "end": record["end"], "length": len(record["sequence"]),
        "energy": record["energy"], "structure_type": record["type"],
        "pairs": len(pairs),
        "pairs_with_bonus": sum(1 for pair in pairs if (pair[3] or 0) > 0),
        "pseudoknot_pairs": sum(1 for pair in pairs if pair[2] != "()"),
        "long_range_pairs_gt_1000": sum(1 for pair in pairs if pair[1] - pair[0] > 1000),
        "evidence": {key: evidence.get(key) for key in (
            "source", "builder", "inputRecords", "uniqueBlocks", "meanArmLength", "gaussianStandardDeviation",
            "nonzeroBonusCells", "maximumBonus", "truncationTail", "transform", "evidenceWeight")},
        "seconds": record["seconds"],
        "peak_rss_bytes": record.get("peak_rss_bytes"),
        **({"candidates": len(record["candidates"]), "bonus_matrix": record.get("bonus_matrix")}
           if record.get("candidates") else {}),
        **({"comrades": {"evidence": record["evidence"], "randomisation": record["randomisation"]}} if "randomisation" in record else {}),
    }


# ---------------------------------------------------------------------------
# entry point
# ---------------------------------------------------------------------------

def _memory_limits(request: Dict[str, Any]) -> Dict[str, Any]:
    """CPLfold's length limits under the task's memory budget."""
    budget_mb = request.get("memory_budget_mb")
    if budget_mb is not None and (isinstance(budget_mb, bool) or not isinstance(budget_mb, int) or budget_mb < 64):
        raise ValueError("memory_budget_mb must be an integer >= 64")
    # A scheduled task runs under the scheduler's memory budget (request or
    # HYB2_MEMORY_BUDGET_MB), not the momentary free memory; without one it
    # gets the scheduler's default share of physical memory.
    task_mem_mb = _env_int("HYB2_TASK_MEM_MB")
    if budget_mb is None and task_mem_mb is not None:
        # Budget granted by the desktop scheduler for this task.
        budget_mb = task_mem_mb
    if budget_mb is not None:
        budget: Optional[int] = budget_mb * 1024 * 1024
    elif os.environ.get("HYB2_MEMORY_BUDGET_MB"):
        budget = None
    else:
        total = memory_snapshot().get("total_bytes")
        budget = int(total * CPLFOLD_MEMORY_FRACTION) if total else None
    return cplfold_limits(budget)


def _structure_run(request: Dict[str, Any], repo_root: Optional[Path], state: Dict[str, Any]) -> Dict[str, Any]:
    started = time.monotonic()
    raw_path = request.get("project_path")
    if not isinstance(raw_path, str) or not raw_path.strip():
        raise ValueError("project path must be a non-empty string")
    project = Path(raw_path).expanduser().resolve()
    errors = validate_project_directory(project)
    if errors:
        raise ValueError("invalid project: " + "; ".join(errors))
    task_id = _safe_task_id(request.get("task_id"))
    mode = request.get("mode", "genome")
    engine = request.get("engine", "cplfold")
    if engine not in {"cplfold", "cplcofold", "viennarna", "comrades"}:
        raise ValueError("engine must be 'cplfold', 'cplcofold', 'viennarna' or 'comrades'")
    if (engine == "cplcofold") != (mode == "cofold"):
        raise ValueError("mode 'cofold' goes with engine 'cplcofold'")
    if mode == "paired" and engine != "comrades":
        raise ValueError("mode 'paired' goes with engine 'comrades'")
    if mode not in {"genome", "windows", "cofold", "paired"}:
        raise ValueError("mode must be 'genome', 'windows', 'cofold' or 'paired'")
    request, state["source"] = _sequence_source(request, project, engine, mode)
    mode = request.get("mode", "genome")
    if engine == "cplcofold":
        return _cofold_run(request, project, task_id, state, started, repo_root)
    if engine != "cplfold":
        return _vienna_run(request, project, task_id, mode, state, started, engine)
    params = _parameters(request.get("parameters"), mode)
    inputs = _evidence_inputs(project, request)
    gene_id, sequence = _fold_sequence(request, inputs)
    windows = _windows(request.get("windows"), len(sequence)) if mode == "windows" else [(1, len(sequence))]

    limits = _memory_limits(request)
    key = "pseudoknot_max_nt" if params["allow_pseudoknot"] else "pseudoknot_free_max_nt"
    longest = max(end - start + 1 for start, end in windows)
    if longest > int(limits[key]):
        hint = " Use windows mode for long RNAs." if mode == "genome" else ""
        raise ValueError(f"{longest} nt exceeds the CPLfold limit of {limits[key]} nt for this memory budget "
                         f"({'pseudoknot' if params['allow_pseudoknot'] else 'pseudoknot-free'}).{hint}")

    run_dir = _resolve_run_dir(project, task_id, request.get("run_dir"))
    state.update(run_dir=run_dir, task_id=task_id)
    out_dir = _real_child_dir(project / "structures", task_id, exist_ok=True)
    progress = ProgressWriter(run_dir)
    root = Path(repo_root or Path(__file__).resolve().parents[2]).resolve()

    try:
        module = load_cplfold_web(root)
    except (FileNotFoundError, ImportError) as error:
        result = {"ok": False, "status": "unavailable", "task_id": task_id, "run_dir": str(run_dir), "error": str(error)}
        _atomic_write_json(run_dir / "result.json", result)
        return result

    # What will be folded, known before the evidence is read, so the desktop
    # can estimate the time left from the start.
    estimate = cplfold_memory_estimate(longest, params["allow_pseudoknot"])
    cpu = os.cpu_count() or 1
    task_cpus = _env_int("HYB2_TASK_CPUS")
    if task_cpus is not None:
        cpu = min(cpu, task_cpus)
    requested_workers = request.get("workers")
    if requested_workers is not None and (isinstance(requested_workers, bool) or not isinstance(requested_workers, int) or requested_workers < 1):
        raise ValueError("workers must be a positive integer")
    memory_workers = max(1, int(limits["memory_budget_bytes"]) // max(estimate, 1))
    workers = min(cpu, memory_workers, len(windows))
    if requested_workers is not None:
        workers = min(workers, requested_workers)
    progress.work = {"lengths": [end - start + 1 for start, end in windows], "beam": params.get("beam"), "workers": workers}

    # 1. stream the evidence
    total_bytes = sum(path.stat().st_size for path in inputs["hyb_paths"])
    progress("evidence", "reading .hyb", 0, total_bytes, force=True)
    t0 = time.monotonic()
    if params["evidence"] == "none":
        # sequence only: no chimera is read, the fold has no bonus
        collected = {"outOfRangeRows": 0}
        counters = [Counter() for _ in windows]
    else:
        collected = module.collect_hyb_arms(
            [str(path) for path in inputs["hyb_paths"]], gene_id, len(sequence),
            windows=windows if mode == "windows" else None,
            progress=lambda done: progress("evidence", "reading .hyb", done, total_bytes))
        counters = collected["windows"] if mode == "windows" else [collected["genome"]]
        if not any(counters):
            raise ValueError(f"no intramolecular {gene_id} chimeras fall inside the requested region")
    evidence_seconds = time.monotonic() - t0
    progress("evidence", "done", total_bytes, total_bytes, force=True)

    # 2. fold
    records: List[Dict[str, Any]] = [None] * len(windows)  # type: ignore[list-item]
    kernels_before = _compiled_kernels()
    t0 = time.monotonic()

    def finish(index: int, record: Dict[str, Any]) -> None:
        start, end = windows[index]
        record.update({"start": start, "end": end, "gene_id": gene_id, "sequence": sequence[start - 1:end]})
        records[index] = record

    # each window's bonus matrix, for a local CPLfold run of that window
    bonus_paths = [str(out_dir / ("bonus_matrix.tsv" if len(windows) == 1 else f"bonus_matrix_{start}-{end}.tsv"))
                   for start, end in windows]
    labels = [f"{gene_id}:{start}-{end}" for start, end in windows]
    if workers <= 1:
        for index, ((start, end), arms) in enumerate(zip(windows, counters)):
            progress("fold", f"window {index + 1}/{len(windows)} ({start}-{end})" if mode == "windows" else "folding",
                     index, len(windows), units="windows", force=True)
            finish(index, fold_one(module, sequence[start - 1:end], arms, params, bonus_paths[index], labels[index]))
    else:
        done = 0
        context = get_context("spawn")
        with ProcessPoolExecutor(max_workers=workers, mp_context=context, initializer=_worker_init,
                                 initargs=(str(root),)) as pool:
            futures = [pool.submit(_fold_window, str(root), index, sequence[start - 1:end],
                                   list(arms.keys()), list(arms.values()), params, bonus_paths[index], labels[index])
                       for index, ((start, end), arms) in enumerate(zip(windows, counters))]
            progress("fold", f"0/{len(windows)} windows on {workers} workers", 0, len(windows), units="windows", force=True)
            for future in as_completed(futures):
                index, record = future.result()
                finish(index, record)
                done += 1
                progress("fold", f"{done}/{len(windows)} windows on {workers} workers", done, len(windows),
                         units="windows", force=True)
    fold_seconds = time.monotonic() - t0

    # 3. write
    progress("write", "writing results", None, None, units="none", force=True)
    outputs = _write_outputs(out_dir, records, params)
    summaries = [_summary(record) for record in records]
    meta = {
        "version": 1,
        "task_id": task_id,
        "mode": mode,
        "engine": "CPLfold",
        "engine_version": getattr(module, "CPLFOLD_SOURCE_REVISION", None),
        "gene_id": gene_id,
        "reference": {"path": str(inputs["reference"]), "length": len(sequence)},
        "evidence_inputs": {
            "samples": inputs["samples"],
            "hyb": [{"path": str(path), "size_bytes": path.stat().st_size} for path in inputs["hyb_paths"]],
            "rows": "none: sequence only, no chimera is used" if params["evidence"] == "none"
            else "intramolecular chimeras (both arms on gene_id) whose arms lie inside the folded region",
            "out_of_range_rows": collected.get("outOfRangeRows", 0),
        },
        "parameters": {**params, "linear_fold_mode": "Vienna", "max_phase2": 1},
        "coordinates": "1-based inclusive positions on the gene_id reference sequence (windows, pairs.tsv i/j, ct column 6)",
        "windows": [{"start": start, "end": end} for start, end in windows],
        "workers": workers,
        "limits": {key: limits.get(key) for key in ("pseudoknot_free_max_nt", "pseudoknot_max_nt", "memory_budget_bytes", "source")},
        "estimated_peak_bytes_per_fold": estimate,
        "records": summaries,
        "timings": {"evidence_s": round(evidence_seconds, 2), "fold_s": round(fold_seconds, 2),
                    "total_s": round(time.monotonic() - started, 2)},
        "created_at": _now(),
    }
    _atomic_write_json(out_dir / "meta.json", meta)
    outputs["meta"] = str(out_dir / "meta.json")
    relative_dir = f"structures/{task_id}"
    energies = [item["energy"] for item in summaries]
    summary = {"records": len(records), "pairs": sum(item["pairs"] for item in summaries),
               "pairs_with_bonus": sum(item["pairs_with_bonus"] for item in summaries),
               "pseudoknot_pairs": sum(item["pseudoknot_pairs"] for item in summaries),
               "energy": energies[0] if len(energies) == 1 else None, "energies": energies,
               "length": sum(item["length"] for item in summaries),
               "workers": workers, "timings": meta["timings"]}
    performance = {**progress.work, "seconds": round(fold_seconds, 2)}
    kernels_after = _compiled_kernels()
    if kernels_before is None or kernels_after is None or kernels_after > kernels_before:
        performance["compiled"] = True  # includes compiling the kernels (first fold after an install)
    result = {
        "ok": True, "status": "succeeded", "task_id": task_id, "run_dir": str(run_dir),
        "output": outputs["dbn"], "output_dir": str(out_dir), "outputs": outputs,
        "performance": performance,
        # A project.json structures[] entry; register_task_run copies it into
        # the manifest when the task finishes.
        "structure": {"id": task_id, "task_id": task_id, "sample_ids": [sample["id"] for sample in inputs["samples"]],
                      "gene_id": gene_id, "mode": mode, "windows": meta["windows"], "parameters": meta["parameters"],
                      "created_at": meta["created_at"], "path": relative_dir,
                      "paths": {key: f"{relative_dir}/{Path(value).name}" for key, value in outputs.items()},
                      "summary": summary},
        "summary": summary,
    }
    progress("done", "finished", None, None, units="none", force=True)
    _atomic_write_json(run_dir / "result.json", result)
    return result


def _vienna_run(request: Dict[str, Any], project: Path, task_id: str, mode: str, state: Dict[str, Any],
                started: float, engine: str) -> Dict[str, Any]:
    """ViennaRNA MFE (engine "viennarna", vienna.py: the samples only give the
    reference) or COMRADES(ViennaRNA) (engine "comrades", comrades.py: their
    chimeras inside each window are the evidence) of the whole RNA or each
    window."""
    if mode == "paired":
        return _paired_run(request, project, task_id, state, started)
    comrades = engine == "comrades"
    params = (_comrades if comrades else _vienna).parameters(request.get("parameters"))
    inputs = _evidence_inputs(project, request)
    gene_id, sequence = _fold_sequence(request, inputs)
    sequence = sequence.upper().replace("T", "U")
    windows = _windows(request.get("windows"), len(sequence)) if mode == "windows" else [(1, len(sequence))]
    longest = max(end - start + 1 for start, end in windows)
    limit = _comrades.MAX_COMRADES_NT if comrades else _vienna.MAX_VIENNA_NT
    if longest > limit:
        raise ValueError(f"{longest} nt exceeds the {'COMRADES(ViennaRNA)' if comrades else 'ViennaRNA'} limit of {limit} nt; fold windows")
    # each hard pair applies to the windows that hold both its ends
    hard = params.get("hard_pairs", [])
    per_window = [[(i, j) for i, j in hard if start <= i and j <= end] for start, end in windows]
    outside = [f"{i}-{j}" for i, j in hard if not any(start <= i and j <= end for start, end in windows)]
    if outside:
        raise ValueError("hard pair(s) " + ", ".join(outside) + " are not inside a folded window")
    local = [_vienna.check_hard_pairs(pairs, sequence[start - 1:end], params["min_loop"], start)
             for pairs, (start, end) in zip(per_window, windows)]
    arms = []
    if comrades:
        for start, end in windows:
            found, _ = _comrades.evidence_arms(inputs["hyb_paths"], gene_id, sequence, start, end, params["homodimer_only"])
            if not found:
                raise ValueError(f"no {gene_id} chimeras have both arms inside {start}-{end}")
            arms.append(found)

    run_dir = _resolve_run_dir(project, task_id, request.get("run_dir"))
    state.update(run_dir=run_dir, task_id=task_id)
    out_dir = _real_child_dir(project / "structures", task_id, exist_ok=True)
    progress = ProgressWriter(run_dir)
    progress.work = {"lengths": [end - start + 1 for start, end in windows], "engine": engine, "workers": 1}
    records: List[Dict[str, Any]] = []
    t0 = time.monotonic()
    for index, ((start, end), pairs) in enumerate(zip(windows, local)):
        progress("fold", f"window {index + 1}/{len(windows)} ({start}-{end})" if mode == "windows" else "folding",
                 index, len(windows), units="windows", force=True)
        if comrades:
            record = _comrades.fold(sequence[start - 1:end], arms[index], params)
        else:
            record = _vienna.fold(sequence[start - 1:end], params, pairs)
        record.update({"start": start, "end": end, "gene_id": gene_id, "sequence": sequence[start - 1:end]})
        records.append(record)
    fold_seconds = time.monotonic() - t0

    progress("write", "writing results", None, None, units="none", force=True)
    name = "COMRADES(ViennaRNA)" if comrades else "ViennaRNA"
    if comrades:
        # the stem constraints the chosen structures were fitted with, on the RNA
        hard = [(record["start"] + i - 1, record["start"] + j - 1) for record in records for i, j in record["hard_pairs"]]
        stored = {"engine": name, **{k: params[k] for k in ("temperature", "min_loop", "constraint_limit", "random_folds",
                                                              "seed", "homodimer_only")}}
        stored["seed"] = _comrades.seed_label(stored["seed"])  # stored as UTF-8 text
    else:
        stored = {"engine": name, "temperature": params["temperature"], "min_loop": params["min_loop"]}
    stored["hard_pairs"] = [list(p) for p in hard]
    outputs = _write_outputs(out_dir, records, stored)
    summaries = [_summary(record) for record in records]
    meta = {
        "version": 1, "task_id": task_id, "mode": mode, "engine": name, "engine_version": _vienna.version(),
        "gene_id": gene_id, "reference": {"path": str(inputs["reference"]), "length": len(sequence)},
        "evidence_inputs": {"samples": inputs["samples"],
                            "hyb": [{"path": str(path), "size_bytes": path.stat().st_size} for path in inputs["hyb_paths"]] if comrades else [],
                            "rows": ("intramolecular chimeras (both arms on gene_id) inside the window, one RNAcofold each"
                                     + (", homodimer proxies only" if params.get("homodimer_only") else "")) if comrades
                            else "none: ViennaRNA MFE uses the sequence (and any hard pairs) only"},
        "parameters": stored,
        "coordinates": "1-based inclusive positions on the gene_id reference sequence (windows, pairs.tsv i/j, ct column 6)",
        "windows": [{"start": start, "end": end} for start, end in windows],
        "workers": 1, "records": summaries,
        "timings": {"evidence_s": 0.0, "fold_s": round(fold_seconds, 2), "total_s": round(time.monotonic() - started, 2)},
        "created_at": _now(),
    }
    _atomic_write_json(out_dir / "meta.json", meta)
    outputs["meta"] = str(out_dir / "meta.json")
    relative_dir = f"structures/{task_id}"
    energies = [item["energy"] for item in summaries]
    summary = {"records": len(records), "pairs": sum(item["pairs"] for item in summaries),
               "pairs_with_bonus": sum(item["pairs_with_bonus"] for item in summaries),
               "pseudoknot_pairs": 0, "energy": energies[0] if len(energies) == 1 else None, "energies": energies,
               "length": sum(item["length"] for item in summaries), "workers": 1, "timings": meta["timings"],
               "engine": name}
    result = {
        "ok": True, "status": "succeeded", "task_id": task_id, "run_dir": str(run_dir),
        "output": outputs["dbn"], "output_dir": str(out_dir), "outputs": outputs,
        "performance": {**progress.work, "seconds": round(fold_seconds, 2)},
        "structure": {"id": task_id, "task_id": task_id, "sample_ids": [sample["id"] for sample in inputs["samples"]],
                      "gene_id": gene_id, "mode": mode, "windows": meta["windows"], "parameters": stored,
                      "created_at": meta["created_at"], "path": relative_dir,
                      "paths": {key: f"{relative_dir}/{Path(value).name}" for key, value in outputs.items()},
                      "summary": summary},
        "summary": summary,
    }
    progress("done", "finished", None, None, units="none", force=True)
    _atomic_write_json(run_dir / "result.json", result)
    return result


# ---------------------------------------------------------------------------
# CPLcofold: two regions as two strands (engine "cplcofold", mode "cofold")
# ---------------------------------------------------------------------------

def _cofold_regions(raw: Any, records: Dict[str, str]) -> List[Dict[str, Any]]:
    """The two regions, checked against the reference sequences."""
    if not isinstance(raw, list) or len(raw) != 2:
        raise ValueError("cofold mode needs exactly two regions")
    regions = []
    for index, region in enumerate(raw, 1):
        try:
            gene, start, end = region["gene_id"], int(region["start"]), int(region["end"])
        except (KeyError, TypeError, ValueError) as error:
            raise ValueError(f"region {index} needs gene_id, start and end") from error
        if not isinstance(gene, str) or gene not in records:
            raise ValueError(f"region {index}: {gene} is not a sequence of the reference FASTA")
        sequence = records[gene]
        if not 1 <= start <= end <= len(sequence):
            raise ValueError(f"region {index} ({start}-{end}) is outside 1-{len(sequence)} of {gene}")
        if end - start + 1 < MIN_WINDOW_NT:
            raise ValueError(f"region {index} is shorter than {MIN_WINDOW_NT} nt")
        part = sequence[start - 1:end]
        bad = sorted(set(part) - set("ACGU"))
        if bad:
            raise ValueError(f"region {index} ({gene}:{start}-{end}) contains unsupported symbols: " + ", ".join(bad))
        regions.append({"gene_id": gene, "start": start, "end": end, "sequence": part})
    regions[0]["prepared_start"], regions[0]["prepared_end"] = 1, len(regions[0]["sequence"])
    regions[1]["prepared_start"] = len(regions[0]["sequence"]) + 1
    regions[1]["prepared_end"] = len(regions[0]["sequence"]) + len(regions[1]["sequence"])
    return regions


def cofold_arms(rows, regions: List[Dict[str, Any]]) -> Tuple[Counter, Dict[str, int]]:
    """The web's two-region CPLcofold evidence (comrades-analysis.js
    prepareReferenceAssembly with cofold): a row whose arms lie one in each
    region joins the strands (oriented region 1 first); otherwise a row with
    both arms in one region is intramolecular evidence of that strand.  As
    the web, the joining test comes first, so a row inside the overlap of two
    overlapping regions joins them.  Returns blocks in A+B coordinates
    (1-based inclusive) with their row counts."""
    def inside(gene, s, e, region):
        return gene == region["gene_id"] and s <= e and region["start"] <= s and e <= region["end"]

    def prepared(s, e, region):
        shift = region["prepared_start"] - region["start"]
        return s + shift, e + shift

    arms: Counter = Counter()
    counts = {"between": 0, "within_one": 0, "within_two": 0, "outside": 0}
    first, second = regions
    for g1, a0, a1, g2, b0, b1 in rows:
        if inside(g1, a0, a1, first) and inside(g2, b0, b1, second):
            arms[prepared(a0, a1, first) + prepared(b0, b1, second)] += 1; counts["between"] += 1
        elif inside(g2, b0, b1, first) and inside(g1, a0, a1, second):
            arms[prepared(b0, b1, first) + prepared(a0, a1, second)] += 1; counts["between"] += 1
        elif inside(g1, a0, a1, first) and inside(g2, b0, b1, first):
            arms[prepared(a0, a1, first) + prepared(b0, b1, first)] += 1; counts["within_one"] += 1
        elif inside(g1, a0, a1, second) and inside(g2, b0, b1, second):
            arms[prepared(a0, a1, second) + prepared(b0, b1, second)] += 1; counts["within_two"] += 1
        else:
            counts["outside"] += 1
    return arms, counts


def _cofold_label(regions: List[Dict[str, Any]]) -> str:
    return " + ".join(f"{r['gene_id']}:{r['start']}-{r['end']}" for r in regions)


def regions_overlap(regions: List[Dict[str, Any]]) -> bool:
    one, two = regions
    return one["gene_id"] == two["gene_id"] and one["start"] <= two["end"] and two["start"] <= one["end"]


def cofold_positions(regions: List[Dict[str, Any]]) -> List[int]:
    """Where each A+B nucleotide is drawn: its RNA position when both regions
    are on one RNA and do not overlap, else its A+B index (two RNAs share no
    axis, and overlapping regions would give one position to two bases)."""
    if regions[0]["gene_id"] == regions[1]["gene_id"] and not regions_overlap(regions):
        return [p for r in regions for p in range(r["start"], r["end"] + 1)]
    return list(range(1, regions[1]["prepared_end"] + 1))


def _write_cofold_outputs(out_dir: Path, regions: List[Dict[str, Any]], record: Dict[str, Any],
                          params: Dict[str, Any]) -> Dict[str, str]:
    """structure.dbn with '&' at the strand break, pairs.tsv in A+B positions
    with each end's RNA and position, and a two-strand .ct whose last column
    is each base's position on its own RNA."""
    cut = regions[0]["prepared_end"]
    sequence, structure = record["sequence"], record["structure"]
    energy = record["energy"]
    energy_text = f"{energy:.2f}" if isinstance(energy, (int, float)) else "NA"
    label = _cofold_label(regions)
    dbn = (f">{label} {_describe(params)}, energy {energy_text}\n"
           f"{sequence[:cut]}&{sequence[cut:]}\n{structure[:cut]}&{structure[cut:]}\n")
    where = [(r["gene_id"], p) for r in regions for p in range(r["start"], r["end"] + 1)]
    tsv = ["window_start\twindow_end\ti\tj\tbracket\tbonus\tgene_i\tposition_i\tgene_j\tposition_j\tintermolecular"]
    partner = {}
    for i, j, bracket, bonus in record["pairs"]:
        partner[i], partner[j] = j, i
        bonus_text = "" if bonus is None else repr(float(bonus))
        (gi, pi), (gj, pj) = where[i - 1], where[j - 1]
        tsv.append(f"1\t{len(sequence)}\t{i}\t{j}\t{bracket}\t{bonus_text}\t{gi}\t{pi}\t{gj}\t{pj}\t{int(i <= cut < j)}")
    n = len(sequence)
    ct = [f"{n}\tENERGY = {energy_text}\t{label}\n"]
    for k, base in enumerate(sequence, 1):
        # no backbone across the strand break
        before = 0 if k == cut + 1 else k - 1
        after = 0 if k == cut or k == n else k + 1
        ct.append(f"{k}\t{base}\t{before}\t{after}\t{partner.get(k, 0)}\t{where[k - 1][1]}\n")
    paths = {"dbn": out_dir / "structure.dbn", "pairs": out_dir / "pairs.tsv", "ct": out_dir / "structure.ct"}
    _atomic_write_text(paths["dbn"], dbn)
    _atomic_write_text(paths["pairs"], "\n".join(tsv) + "\n")
    _atomic_write_text(paths["ct"], "".join(ct))
    _write_candidates(out_dir, [record], paths)
    return {key: str(value) for key, value in paths.items()}


def _finish_two_strand(*, task_id: str, mode: str, engine: str, engine_version: Any, run_dir: Path, out_dir: Path,
                       progress: ProgressWriter, regions: List[Dict[str, Any]], record: Dict[str, Any],
                       stored: Dict[str, Any], inputs: Dict[str, Any], hyb_used: bool, rows: str,
                       extra_record: Dict[str, Any], extra_meta: Dict[str, Any], timings: Dict[str, float],
                       fold_seconds: float) -> Dict[str, Any]:
    """Write a two-strand result (CPLcofold, or COMRADES of two regions):
    the files, meta.json, result.json and the project.json entry."""
    progress("write", "writing results", None, None, units="none", force=True)
    outputs = _write_cofold_outputs(out_dir, regions, record, stored)
    cut = regions[0]["prepared_end"]
    pairs = record["pairs"]
    summary_record = _summary(record)
    between = sum(1 for i, j, _, _ in pairs if i <= cut < j)
    segments = [{key: r[key] for key in ("gene_id", "start", "end", "prepared_start", "prepared_end")} for r in regions]
    summary_record.update({"cutpoint": cut, "segments": segments, "intermolecular_pairs": between,
                           "overlapping_regions": regions_overlap(regions), **extra_record})
    same_rna = regions[0]["gene_id"] == regions[1]["gene_id"]
    gene_id = regions[0]["gene_id"] if same_rna else f"{regions[0]['gene_id']} + {regions[1]['gene_id']}"
    drawn = ("RNA positions" if same_rna and not regions_overlap(regions) else
             "A+B indices (two RNAs, or overlapping regions)")
    meta = {
        "version": 1, "task_id": task_id, "mode": mode, "engine": engine, "engine_version": engine_version,
        "gene_id": gene_id, "reference": {"path": str(inputs["reference"])}, "regions": segments,
        "evidence_inputs": {"samples": inputs["samples"],
                            "hyb": [{"path": str(path), "size_bytes": path.stat().st_size} for path in inputs["hyb_paths"]]
                            if hyb_used else [], "rows": rows},
        "parameters": stored,
        "coordinates": ("A+B: region 1 then region 2, 1-based inclusive (pairs.tsv i/j); "
                        "gene_i/position_i give each end on its RNA; ct column 6 is the RNA position; "
                        "pairs are drawn on " + drawn),
        "windows": [{"start": r["start"], "end": r["end"]} for r in regions],
        "workers": 1, "records": [summary_record],
        "timings": timings,
        "created_at": _now(), **extra_meta,
    }
    _atomic_write_json(out_dir / "meta.json", meta)
    outputs["meta"] = str(out_dir / "meta.json")
    relative_dir = f"structures/{task_id}"
    summary = {"records": 1, "pairs": len(pairs), "pairs_with_bonus": summary_record["pairs_with_bonus"],
               "pseudoknot_pairs": summary_record["pseudoknot_pairs"], "intermolecular_pairs": between,
               "energy": record["energy"], "energies": [record["energy"]], "length": len(record["sequence"]),
               "workers": 1, "timings": meta["timings"], "engine": engine}
    result = {
        "ok": True, "status": "succeeded", "task_id": task_id, "run_dir": str(run_dir),
        "output": outputs["dbn"], "output_dir": str(out_dir), "outputs": outputs,
        "performance": {**progress.work, "seconds": round(fold_seconds, 2)},
        "structure": {"id": task_id, "task_id": task_id, "sample_ids": [sample["id"] for sample in inputs["samples"]],
                      "gene_id": gene_id, "mode": mode, "windows": meta["windows"], "regions": segments,
                      "parameters": stored, "created_at": meta["created_at"], "path": relative_dir,
                      "paths": {key: f"{relative_dir}/{Path(value).name}" for key, value in outputs.items()},
                      "summary": summary},
        "summary": summary,
    }
    progress("done", "finished", None, None, units="none", force=True)
    _atomic_write_json(run_dir / "result.json", result)
    return result


def _cofold_run(request: Dict[str, Any], project: Path, task_id: str, state: Dict[str, Any],
                started: float, repo_root: Optional[Path]) -> Dict[str, Any]:
    """Two regions folded as two strands with CPLcofold (the web's
    two-region CPLfold layout), with or without the chimeras."""
    params = _parameters(request.get("parameters"), "cofold")
    if params["energy_model"] not in COFOLD_MODELS:
        raise ValueError("CPLcofold supports the DP03 and DP09 energy models only")
    if params["beam"] > COFOLD_MAX_BEAM:
        raise ValueError(f"CPLcofold beam size is at most {COFOLD_MAX_BEAM}")
    inputs = _evidence_inputs(project, request)
    # pasted strands are their own records
    records = request.get("_strands") or {name: sequence.upper().replace("T", "U")
                                          for name, sequence in read_fasta(inputs["reference"])}
    regions = _cofold_regions(request.get("regions"), records)
    sequence = regions[0]["sequence"] + regions[1]["sequence"]
    limits = _memory_limits(request)
    key = "pseudoknot_max_nt" if params["allow_pseudoknot"] else "pseudoknot_free_max_nt"
    if len(sequence) > int(limits[key]):
        raise ValueError(f"{len(sequence)} nt exceeds the CPLfold limit of {limits[key]} nt for this memory budget "
                         f"({'pseudoknot' if params['allow_pseudoknot'] else 'pseudoknot-free'}); choose shorter regions.")

    run_dir = _resolve_run_dir(project, task_id, request.get("run_dir"))
    state.update(run_dir=run_dir, task_id=task_id)
    out_dir = _real_child_dir(project / "structures", task_id, exist_ok=True)
    progress = ProgressWriter(run_dir)
    root = Path(repo_root or Path(__file__).resolve().parents[2]).resolve()
    try:
        module = load_cplfold_web(root)
    except (FileNotFoundError, ImportError) as error:
        result = {"ok": False, "status": "unavailable", "task_id": task_id, "run_dir": str(run_dir), "error": str(error)}
        _atomic_write_json(run_dir / "result.json", result)
        return result
    progress.work = {"lengths": [len(sequence)], "beam": params["beam"], "workers": 1}

    # 1. the evidence, as blocks with row counts (memory grows with the
    #    distinct blocks, not with the read depth)
    t0 = time.monotonic()
    arms: Counter = Counter()
    counts = {"between": 0, "within_one": 0, "within_two": 0, "outside": 0}
    if params["evidence"] == "hyb":
        total_bytes = sum(path.stat().st_size for path in inputs["hyb_paths"])
        progress("evidence", "reading .hyb", 0, total_bytes, force=True)
        rows = module.iter_hyb_rows([str(path) for path in inputs["hyb_paths"]], {r["gene_id"] for r in regions},
                                    lambda done: progress("evidence", "reading .hyb", done, total_bytes))
        arms, counts = cofold_arms(rows, regions)
        if not arms:
            raise ValueError(f"no chimeras have both arms inside {_cofold_label(regions)}")
        progress("evidence", "done", total_bytes, total_bytes, force=True)
    evidence_seconds = time.monotonic() - t0

    # 2. fold with the shared module (the web's CPLcofold request)
    progress("fold", "folding", 0, 1, units="windows", force=True)
    if hasattr(module, "set_host_hard_max_sequence_length"):
        module.set_host_hard_max_sequence_length(max(len(sequence), 1))
    t0 = time.monotonic()
    folded = module.fold({
        "sequence": regions[0]["sequence"], "sequenceTwo": regions[1]["sequence"],
        "evidenceMode": "hyb-blocks" if arms else "none",
        "evidenceArmCounts": [[a, b, c, d, n] for (a, b, c, d), n in arms.items()],
        "beamSize": params["beam"], "maxPhase1": params["max_phase1"], "energyDelta": params["energy_delta"],
        "energyModel": params["energy_model"], "alpha": params["alpha"] if arms else 0.0, "beta": params["beta"],
        "allowPseudoknot": params["allow_pseudoknot"], "maxSequenceLength": len(sequence)})
    fold_seconds = time.monotonic() - t0
    pairs = [(pair["left"], pair["right"], pair["bracket"], float(pair["bonusSupport"]) if arms else None)
             for pair in folded["pairs"]]
    raw_evidence = folded.get("evidence") or {}
    evidence = {k: v for k, v in raw_evidence.items() if k not in ("bonusEntries", "nucleotideSupport")}
    # the web's candidates, on A+B indices as the dot-bracket
    candidates = []
    for rank, candidate in enumerate((folded.get("candidates") or [])[:CANDIDATE_LIMIT]):
        raw = {"type": candidate.get("type"), "energy": candidate.get("energy"),
               "effective_energy": candidate.get("effectiveEnergy"), "phase1_energy": candidate.get("phase1Energy"),
               "score": candidate.get("score")}
        candidate_pairs = [(pair["left"], pair["right"], pair["bracket"], float(pair["bonusSupport"]) if arms else None)
                           for pair in candidate.get("pairs") or []]
        candidates.append(_candidate(rank, raw, candidate["dotBracket"], candidate_pairs))
    bonus_file = None
    entries = raw_evidence.get("bonusEntries") or []
    if arms and entries and len(entries) <= BONUS_EXPORT_MAX_CELLS:
        # the web's two-strand export: 17 significant digits, with the cutpoint
        header = _bonus_header(len(sequence), _cofold_label(regions), regions[0]["prepared_end"])
        _atomic_write_text(out_dir / "bonus_matrix.tsv", header + "".join(
            f"{int(entry['one'])}\t{int(entry['two'])}\t{float(entry['value']):.17g}\n" for entry in entries))
        bonus_file = "bonus_matrix.tsv"
    support = folded.get("nucleotideSupport") if arms else None
    record = {"structure": folded["dotBracket"], "energy": folded.get("energy"), "type": folded.get("structureType"),
              "pairs": pairs, "evidence": evidence, "sequence": sequence, "start": 1, "end": len(sequence),
              "candidates": candidates, "candidates_found": len(folded.get("candidates") or []),
              "nucleotide_support": [round(float(value), 6) for value in support] if support else None,
              "bonus_matrix": bonus_file,
              "seconds": {"bonus": 0.0, "fold": round(fold_seconds, 2)}, "peak_rss_bytes": _peak_rss_bytes()}
    rows_text = ("none: sequence only, no chimera is used" if params["evidence"] == "none" else
                 f"chimeras with one arm in each region ({counts['between']}) or both arms in region 1 "
                 f"({counts['within_one']}) or region 2 ({counts['within_two']})")
    return _finish_two_strand(
        task_id=task_id, mode="cofold", engine="CPLcofold", engine_version=getattr(module, "CPLFOLD_SOURCE_REVISION", None),
        run_dir=run_dir, out_dir=out_dir, progress=progress, regions=regions, record=record,
        stored={**params, "engine": "CPLcofold", "linear_fold_mode": "Vienna", "max_phase2": 1},
        inputs=inputs, hyb_used=params["evidence"] == "hyb", rows=rows_text, extra_record={"evidence_rows": counts},
        extra_meta={"limits": {key: limits.get(key) for key in ("pseudoknot_free_max_nt", "pseudoknot_max_nt",
                                                                "memory_budget_bytes", "source")}},
        timings={"evidence_s": round(evidence_seconds, 2), "fold_s": round(fold_seconds, 2),
                 "total_s": round(time.monotonic() - started, 2)},
        fold_seconds=fold_seconds)


def _paired_run(request: Dict[str, Any], project: Path, task_id: str, state: Dict[str, Any],
                started: float) -> Dict[str, Any]:
    """COMRADES(ViennaRNA) of two regions (mode "paired"): the web's
    long-range, intermolecular and homodimer layouts.  As the web, the
    regions are joined by the 100-nt A/U spacer, every chimera with one arm
    in each region is cofolded, and the joined sequence is folded; the result
    keeps the two regions (pairs with a spacer base are left out and
    counted), so it is stored and drawn as two strands."""
    params = _comrades.parameters(request.get("parameters"))
    inputs = _evidence_inputs(project, request)
    records = {name: sequence.upper().replace("T", "U") for name, sequence in read_fasta(inputs["reference"])}
    regions = _cofold_regions(request.get("regions"), records)
    one, two = regions[0]["sequence"], regions[1]["sequence"]
    prepared = one + _comrades.LINKER + two
    if len(prepared) > _comrades.MAX_COMRADES_NT:
        raise ValueError(f"{len(prepared)} nt (both regions and the 100-nt spacer) exceeds the COMRADES(ViennaRNA) "
                         f"limit of {_comrades.MAX_COMRADES_NT} nt; choose shorter regions")
    t0 = time.monotonic()
    layout = [dict(regions[0], prepared_start=1), dict(regions[1], prepared_start=len(one) + len(_comrades.LINKER) + 1)]
    arms, skipped = _comrades.paired_arms(inputs["hyb_paths"], layout, prepared, params["homodimer_only"])
    if not arms:
        raise ValueError(f"no chimeras have one arm in each of {_cofold_label(regions)}")
    evidence_seconds = time.monotonic() - t0

    run_dir = _resolve_run_dir(project, task_id, request.get("run_dir"))
    state.update(run_dir=run_dir, task_id=task_id)
    out_dir = _real_child_dir(project / "structures", task_id, exist_ok=True)
    progress = ProgressWriter(run_dir)
    progress.work = {"lengths": [len(prepared)], "engine": "comrades", "workers": 1}
    progress("fold", "folding", 0, 1, units="windows", force=True)
    t0 = time.monotonic()
    folded = _comrades.fold(prepared, arms, params)
    fold_seconds = time.monotonic() - t0

    # keep the two regions: A+B positions, without the spacer
    low, high = len(one) + 1, len(one) + len(_comrades.LINKER)

    def local(k: int) -> Optional[int]:
        return k if k < low else (k - len(_comrades.LINKER) if k > high else None)

    pairs = [(local(i), local(j), bracket, bonus) for i, j, bracket, bonus in folded["pairs"]
             if local(i) is not None and local(j) is not None]
    dot = ["."] * (len(one) + len(two))
    for i, j, _, _ in pairs:
        dot[i - 1], dot[j - 1] = "(", ")"
    positions = cofold_positions(regions)
    hard = [[positions[local(i) - 1], positions[local(j) - 1]] for i, j in folded["hard_pairs"]
            if local(i) is not None and local(j) is not None]
    record = dict(folded, structure="".join(dot), pairs=pairs, sequence=one + two, start=1, end=len(one) + len(two))
    stored = {"engine": "COMRADES(ViennaRNA)", **{k: params[k] for k in ("temperature", "min_loop", "constraint_limit",
                                                                       "random_folds", "seed", "homodimer_only")},
              "layout": "two regions joined by the 100-nt A/U spacer", "hard_pairs": hard}
    stored["seed"] = _comrades.seed_label(stored["seed"])
    rows_text = (f"chimeras with one arm in each region ({len(arms)}), one RNAcofold each"
                 + (", homodimer proxies only" if params["homodimer_only"] else ""))
    return _finish_two_strand(
        task_id=task_id, mode="paired", engine="COMRADES(ViennaRNA)", engine_version=_vienna.version(),
        run_dir=run_dir, out_dir=out_dir, progress=progress, regions=regions, record=record, stored=stored,
        inputs=inputs, hyb_used=True, rows=rows_text,
        extra_record={"spacer_pairs_left_out": len(folded["pairs"]) - len(pairs),
                      "energy_of": "region 1 + 100-nt A/U spacer + region 2, as folded"},
        extra_meta={"evidence_skipped": skipped},
        timings={"evidence_s": round(evidence_seconds, 2), "fold_s": round(fold_seconds, 2),
                 "total_s": round(time.monotonic() - started, 2)},
        fold_seconds=fold_seconds)


def structure_run(request: Dict[str, Any], repo_root: Optional[Path] = None) -> Dict[str, Any]:
    """Bridge entry point.  Invalid requests raise ``ValueError``; failures after
    the run directory exists are reported in ``result.json`` and returned."""
    state: Dict[str, Any] = {}
    try:
        result = _structure_run(request, repo_root, state)
        if state.get("source") and result.get("ok"):
            _record_source(result, state["source"])
        return result
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
# registered results (bridge operations list_structures / read_structure)
# ---------------------------------------------------------------------------

def _structure_file(project: Path, entry: Dict[str, Any], key: str) -> Optional[Path]:
    """A registered output file under ``structures/``, or None when absent/unsafe."""
    relative = (entry.get("paths") or {}).get(key) if isinstance(entry.get("paths"), dict) else None
    if not safe_relative_path(relative):
        return None
    target = (project / relative).resolve()
    base = (project / "structures").resolve()
    if base not in target.parents or not target.is_file():
        return None
    return target


def list_structures(project: Path) -> List[Dict[str, Any]]:
    """``structures[]`` of the manifest, newest first, each with ``available``
    (its dot-bracket file still exists)."""
    project = Path(project).expanduser().resolve()
    manifest = load_manifest(project)
    out = []
    for entry in manifest.get("structures") or []:
        if isinstance(entry, dict):
            out.append({**entry, "available": _structure_file(project, entry, "dbn") is not None})
    out.sort(key=lambda item: str(item.get("created_at") or ""), reverse=True)
    return out


def parse_dbn(text: str) -> List[Dict[str, Any]]:
    """Records (header, sequence, structure triples) of a structure.dbn from :func:`_write_outputs`."""
    lines = [line.rstrip("\r") for line in text.splitlines() if line.strip()]
    if len(lines) % 3:
        raise ValueError("structure.dbn is malformed (expected header, sequence, structure triples)")
    records = []
    for index in range(0, len(lines), 3):
        header, sequence, structure = lines[index], lines[index + 1], lines[index + 2]
        # a CPLcofold record marks the strand break with '&' in both lines
        cutpoint = sequence.find("&") if "&" in sequence and sequence.find("&") == structure.find("&") else None
        if cutpoint is not None:
            sequence, structure = sequence.replace("&", ""), structure.replace("&", "")
        if not header.startswith(">") or len(sequence) != len(structure):
            raise ValueError("structure.dbn is malformed near record %d" % (index // 3 + 1))
        words = header[1:].split()
        label = words[0] if words else ""
        start = 1
        gene, _, span = label.rpartition(":")
        if gene and "-" in span:
            try:
                start = int(span.split("-", 1)[0])
            except ValueError:
                start = 1
        energy = None
        if "energy " in header:
            try:
                energy = float(header.rsplit("energy ", 1)[1].split()[0])
            except (ValueError, IndexError):
                energy = None
        records.append({"label": label, "window_start": start, "window_end": start + len(sequence) - 1,
                        "sequence": sequence, "dbn": structure, "energy": energy,
                        **({"cutpoint": cutpoint} if cutpoint is not None else {})})
    return records


def _pair_bonus(path: Optional[Path]) -> Dict[Tuple[int, int, int], float]:
    """``(window_start, i, j) -> bonus`` from a pairs.tsv of :func:`_write_outputs`."""
    out: Dict[Tuple[int, int, int], float] = {}
    if path is None:
        return out
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError:
        return out
    for line in lines[1:]:
        cells = line.split("\t")
        if len(cells) < 6:
            continue
        try:
            out[(int(cells[0]), int(cells[2]), int(cells[3]))] = float(cells[5])
        except ValueError:
            continue
    return out


def _record_extras(project: Path, entry: Dict[str, Any], meta: Dict[str, Any], count: int) -> List[Dict[str, Any]]:
    """Per record: ``candidates`` (best first, from candidates.json),
    ``nucleotide_support`` and ``bonus_matrix_path`` (the window's bonus
    matrix TSV, absolute, when it was written); empty for other engines."""
    extras: List[Dict[str, Any]] = [{} for _ in range(count)]
    # next to structure.dbn (project.json registers only the files every engine writes)
    dbn = _structure_file(project, entry, "dbn")
    path = dbn.parent / "candidates.json" if dbn is not None else None
    if path is not None and path.is_file() and not path.is_symlink():
        try:
            loaded = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            loaded = {}
        rows = loaded.get("records") if isinstance(loaded, dict) else None
        for index, row in enumerate(rows if isinstance(rows, list) else []):
            if index < count and isinstance(row, dict):
                extras[index].update(candidates=row.get("candidates") or [], selected_candidate=0,
                                     nucleotide_support=row.get("nucleotide_support"))
    summaries = meta.get("records") if isinstance(meta.get("records"), list) else []
    for index, summary in enumerate(summaries[:count]):
        name = summary.get("bonus_matrix") if isinstance(summary, dict) else None
        # a bare file name next to structure.dbn, never a path
        if dbn is None or not isinstance(name, str) or not re.fullmatch(r"bonus_matrix[0-9_-]*\.tsv", name):
            continue
        target = dbn.parent / name
        if target.is_file() and not target.is_symlink():
            extras[index]["bonus_matrix_path"] = str(target)
    return extras


def _read_cofold(entry: Dict[str, Any], meta: Dict[str, Any], parsed: List[Dict[str, Any]],
                 bonus: Dict[Tuple[int, int, int], float]) -> Dict[str, Any]:
    """A two-strand result (CPLcofold, or COMRADES of two regions): one
    record over A+B, with ``positions`` (where
    each nucleotide is drawn, see :func:`cofold_positions`), ``cutpoint`` and
    the two ``segments``; ``pairs`` are in those positions."""
    summary = (meta.get("records") or [{}])[0]
    segments = summary.get("segments") or meta.get("regions") or []
    if len(parsed) != 1 or len(segments) != 2:
        raise ValueError("the CPLcofold result is malformed")
    record = parsed[0]
    positions = cofold_positions(segments)
    if len(positions) != len(record["sequence"]):
        raise ValueError("the CPLcofold result does not match its regions")
    local = [(i, j) for i, j, _ in _pairs(record["dbn"])]
    energy = summary["energy"] if "energy" in summary else record["energy"]
    return {"structure": entry, "meta": meta, "records": [{
        "window_start": min(positions), "window_end": max(positions),
        "sequence": record["sequence"], "dbn": record["dbn"],
        "pairs": [[positions[i - 1], positions[j - 1]] for i, j in local],
        "bonus": [bonus.get((1, i, j)) for i, j in local] if bonus else None,
        "energy": energy, "positions": positions, "cutpoint": summary.get("cutpoint", segments[0]["prepared_end"]),
        "segments": segments,
    }]}


def read_structure(project: Path, structure_id: str) -> Dict[str, Any]:
    """``{structure, meta, records: [{window_start, window_end, sequence, dbn, pairs, bonus, energy}]}``,
    a CPLfold record also with ``candidates``, ``selected_candidate``,
    ``nucleotide_support`` and ``bonus_matrix_path`` (:func:`_record_extras`).

    Coordinates are 1-based positions on the gene: ``window_start``/``window_end``
    bound each folded window and ``pairs`` lists ``[i, j]`` gene positions.
    """
    project = Path(project).expanduser().resolve()
    manifest = load_manifest(project)
    entry = next((item for item in manifest.get("structures") or []
                  if isinstance(item, dict) and item.get("id") == structure_id), None)
    if entry is None:
        raise ValueError("structure does not exist: %s" % structure_id)
    dbn_path = _structure_file(project, entry, "dbn")
    if dbn_path is None:
        raise ValueError("structure files are missing: %s" % structure_id)
    meta_path = _structure_file(project, entry, "meta")
    meta: Dict[str, Any] = {}
    if meta_path is not None:
        try:
            loaded = json.loads(meta_path.read_text(encoding="utf-8"))
            meta = loaded if isinstance(loaded, dict) else {}
        except ValueError:
            meta = {}
    parsed = parse_dbn(dbn_path.read_text(encoding="utf-8"))
    extras = _record_extras(project, entry, meta, len(parsed))
    if meta.get("mode") in ("cofold", "paired"):
        result = _read_cofold(entry, meta, parsed, _pair_bonus(_structure_file(project, entry, "pairs")))
        result["records"][0].update(extras[0])
        return result
    bonus = _pair_bonus(_structure_file(project, entry, "pairs"))
    summaries = meta.get("records") if isinstance(meta.get("records"), list) else []
    records = []
    for index, record in enumerate(parsed):
        start = record["window_start"]
        summary = summaries[index] if index < len(summaries) and isinstance(summaries[index], dict) else {}
        if type(summary.get("start")) is int:
            start = summary["start"]
        energy = summary["energy"] if "energy" in summary else record["energy"]
        pairs = [[start + i - 1, start + j - 1] for i, j, _ in _pairs(record["dbn"])]
        records.append({
            "window_start": start, "window_end": start + len(record["sequence"]) - 1,
            "sequence": record["sequence"], "dbn": record["dbn"], "pairs": pairs,
            # chimera evidence (HYB bonus) of each pair, from pairs.tsv; null when unknown
            "bonus": [bonus.get((start, i, j)) for i, j in pairs] if bonus else None,
            "energy": energy,
            **extras[index],
        })
    return {"structure": entry, "meta": meta, "records": records}
