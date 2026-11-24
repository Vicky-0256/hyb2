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
* ``windows`` folds each window separately (pseudoknot-free by default, as
  the whole RNA; ``allow_pseudoknot`` turns pseudoknots on), with the
  chimeras whose two arms both lie inside that window, in a process pool: one window per process, ``numba.set_num_threads(1)``, and
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
import tempfile
import time
from collections import Counter
from contextlib import contextmanager
from concurrent.futures import ProcessPoolExecutor, as_completed
from datetime import datetime, timezone
from itertools import chain
from multiprocessing import get_context
from pathlib import Path
from typing import Any, Callable, Dict, Iterator, List, Optional, Sequence, Tuple

from hyb2analysis import hybfile
from hyb2analysis.reference import fasta_record_chunks, fasta_records, parse_coordinate, read_fasta, reference_sequence, validate_input_length, validate_window
from hyb2analysis.fold_text import FOLD_TEXT, checked_fold_text, fold_text, unsupported_bases
from hyb2analysis.engine_parameters import engine_parameters, task_allow_pseudoknot
from hyb2analysis.support_kernels import DEFAULT_SUPPORT_KERNEL, PAIR_MODELS as PAIR_ESTIMATORS, SUPPORT_KERNELS

from .detector import CPLFOLD_MEMORY_FRACTION, cplfold_limits, cplfold_memory_estimate, memory_snapshot
from .name_matching import fasta_id_for_rna
from .project import _atomic_write_json, load_manifest, safe_relative_path, validate_project_directory
from .task_paths import (evidence_inputs as _evidence_inputs, input_file as _input_file,
                         real_child_dir as _real_child_dir, resolve_run_dir as _resolve_run_dir, safe_task_id)
from .samples import _open_text as _open_reference
from . import comrades as _comrades
from . import vienna as _vienna

MAX_WINDOWS = 512
_ENGINES = engine_parameters("desktop")
# Tasks saved before RNAduplex counted the read support: the old name of the
# duplex kernel, and the executable path they could carry (now ignored).
LEGACY_SUPPORT_KERNELS = {"hybridmin": "duplex"}  # legacy-name: kernel of tasks saved before RNAduplex
LEGACY_PARAMETERS = ("hybrid_min", "hybridMin")  # legacy-name: hybrid-min path of tasks saved before RNAduplex
_NUMERIC_PARAMETERS = {"alpha": ("alpha", float), "beam": ("beamSize", int),
                       "max_phase1": ("maxPhase1", int), "energy_delta": ("energyDelta", float),
                       "beta": ("beta", float)}
_ALIASES = {"beamSize": "beam", "maxPhase1": "max_phase1", "energyDelta": "energy_delta", "delta": "energy_delta",
            "energyModel": "energy_model", "model": "energy_model", "supportKernel": "support_kernel"}
BRACKET_CLOSE = {")": "(", "]": "[", "}": "{", ">": "<"}
# CPLfold candidates kept per record for the desktop's candidate table (the
# web lists them all: at most 20 phase-1 candidates, one phase 2 each).
CANDIDATE_LIMIT = 40
# The bonus matrix is written for a local CPLfold run (bin/cplfold
# --bonus-matrix-file) up to this many upper-triangle cells (~50 MB).
BONUS_EXPORT_MAX_CELLS = 2_000_000
BONUS_TRANSFORM = "IRIS Gaussian arm blocks; symmetric float64 accumulation; threshold 2^-23; log1p"

# cplfold_web modules loaded in this process, keyed by source path.
_MODULES: Dict[str, Any] = {}


# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

def _now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _darwin_peak_footprint_bytes() -> int:
    """Largest memory footprint this process has had, as macOS accounts it.

    ``ru_maxrss`` counts only pages resident in RAM, and macOS compresses idle
    pages instead of keeping them resident, so on a loaded machine it reports
    well under half of what a genome fold needs. The kernel's footprint figure
    includes compressed pages. Returns 0 when it cannot be read.
    """
    try:
        import ctypes

        fields = 35  # rusage_info_v4: a 16-byte UUID, then 35 unsigned 64-bit counters
        buffer = (ctypes.c_uint64 * (2 + fields))()
        libproc = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
        libproc.proc_pid_rusage.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_void_p]
        libproc.proc_pid_rusage.restype = ctypes.c_int
        if libproc.proc_pid_rusage(os.getpid(), 4, ctypes.byref(buffer)) != 0:
            return 0
        return int(buffer[2 + 28])  # ri_lifetime_max_phys_footprint
    except (OSError, AttributeError, ValueError):
        return 0


def _peak_rss_bytes() -> int:
    try:
        import resource  # POSIX only; Windows has no getrusage
    except ImportError:
        return 0
    value = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    if sys.platform == "darwin":
        return max(int(value), _darwin_peak_footprint_bytes())
    return int(value * 1024)


def load_cplfold_web(root: Path) -> Any:
    """Import ``web/cplfold/cplfold_web.py`` of ``root``.

    The ``cplfold`` and ``hyb2analysis`` import roots of this runtime are
    already on the path (``desktop/runtime/__init__``); those of ``root`` are
    added only for a resource root other than this checkout."""
    source = Path(root) / "web" / "cplfold" / "cplfold_web.py"
    key = str(source)
    if key in _MODULES:
        return _MODULES[key]
    if not source.is_file():
        raise FileNotFoundError("native CPLfold source is not bundled")
    for extra in (str(Path(root) / "third_party" / "cplfold"), str(Path(root) / "pipeline"), str(source.parent)):
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


def _parameters(raw: Any, mode: str) -> Dict[str, Any]:
    raw = raw if isinstance(raw, dict) else {}
    if "supportKernel" in raw and "support_kernel" in raw and raw["supportKernel"] != raw["support_kernel"]:
        raise ValueError("conflicting supportKernel and support_kernel parameters")
    engine = _ENGINES["cplcofold" if mode == "cofold" else "cplfold"]
    values = {"energy_model": engine["defaultModel"], "support_kernel": DEFAULT_SUPPORT_KERNEL, "evidence": "hyb",
              "allow_pseudoknot": task_allow_pseudoknot(raw, "allow_pseudoknot", default=engine["defaults"]["allowPseudoknot"])}
    resolved = {key: engine["defaults"][param] for key, (param, _) in _NUMERIC_PARAMETERS.items()}
    resolved.update((_ALIASES.get(key, key), value) for key, value in raw.items() if key not in ("allowPseudoknot", "allow_pseudoknot"))
    for key, value in resolved.items():
        if key in _NUMERIC_PARAMETERS:
            param, kind = _NUMERIC_PARAMETERS[key]
            lo, hi = engine["ranges"][param]
            if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) \
                    or not lo <= value <= hi or (kind is int and value != int(value)):
                raise ValueError(f"structure parameter out of bounds: {key}")
            values[key] = kind(value)
        elif key == "evidence":
            if value not in ("hyb", "none"):
                raise ValueError("evidence must be 'hyb' (the chimeras) or 'none' (the sequence alone)")
            values[key] = value
        elif key == "energy_model":
            model = str(value).upper()
            models = [option["value"] for option in engine["models"]]
            if model not in models:
                raise ValueError("energy_model must be one of " + ", ".join(models))
            values[key] = model
        elif key == "support_kernel":
            if isinstance(value, str):
                value = LEGACY_SUPPORT_KERNELS.get(value, value)
            if value not in SUPPORT_KERNELS:
                raise ValueError("support_kernel must be one of " + ", ".join(SUPPORT_KERNELS) + f"; got {value!r}")
            values[key] = value
        elif key in LEGACY_PARAMETERS:
            continue
        else:
            raise ValueError(f"unknown structure parameter: {key}")
    return values


def _windows(raw: Any, length: int, *, source: Optional[Dict[str, Any]] = None) -> List[Tuple[int, int]]:
    # Source is produced by _sequence_source, never read from private request fields.
    if not isinstance(raw, list) or not raw:
        raise ValueError("windows mode needs a non-empty windows list")
    if len(raw) > MAX_WINDOWS:
        raise ValueError(f"at most {MAX_WINDOWS} windows per run")
    windows = []
    for index, window in enumerate(raw):
        try:
            start, end = parse_coordinate(window["start"]), parse_coordinate(window["end"])
        except (KeyError, TypeError, ValueError) as error:
            raise ValueError(f"window {index + 1} needs integer start and end") from error
        validate_window(start, end, length, index + 1, source=source["kind"] if source else "reference")
        if (start, end) in windows:
            # one result per window: a repeat would fold twice and overwrite its files
            raise ValueError(f"window {index + 1} ({start}-{end}) repeats window {windows.index((start, end)) + 1}; "
                             "give each window once")
        windows.append((start, end))
    return windows


# ---------------------------------------------------------------------------
# pasted and HYB record sequences (request "source": "paste" | "record")
# ---------------------------------------------------------------------------

def _rna_names_for_records(names: Sequence[str], inputs: Dict[str, Any]) -> Dict[str, str]:
    """Invert the matches only where a physical FASTA record identifies a single evidence name."""
    matches = inputs["matches"]
    candidates = {name: {rna for rna, target in matches.items() if target == name} for name in names}
    for name, rnas in candidates.items():
        if name not in matches:
            rnas.add(name)
    # A -> B and automatic B -> B are both possible. Don't choose the first
    # alias: check which candidate RNAs actually occur in the selected HYBs.
    ambiguous = [rnas for rnas in candidates.values() if len(rnas) > 1]
    if ambiguous:
        wanted = {rna.encode() for rnas in ambiguous for rna in rnas}
        observed = set()
        for path in inputs["hyb_paths"]:
            for line in hybfile.file_lines(path):
                try:
                    row = hybfile.parse(line)
                except ValueError:
                    continue
                if row is not None:
                    observed.update(wanted.intersection((row.gene1, row.gene2)))
        for rnas in ambiguous:
            rnas.intersection_update(name.decode() for name in observed)
    return {name: next(iter(rnas)) for name, rnas in candidates.items() if len(rnas) == 1}


def _locate_strands(strands: List[str], inputs: Dict[str, Any]) -> List[Tuple[str, str, int, int]]:
    """(HYB RNA, FASTA ID, start, end) for each strand, or [] if not uniquely assignable."""
    hits: List[List[Tuple[str, int, int]]] = [[] for _ in strands]
    overlap = max(map(len, strands)) - 1
    for name, _, chunks in fasta_record_chunks(inputs["reference"], open_text=_open_reference):
        # Search blocks, not whole chromosomes. Carry enough overlap to see
        # hits spanning blocks, and count only hits ending in the new block.
        tail, parts, size, offset = "", [], 0, 0
        for chunk in chain(chunks, (None,)):
            if chunk is not None:
                parts.append(chunk)
                size += len(chunk)
                if size < max(64 * 1024, overlap):
                    continue
            block = tail + fold_text("".join(parts))
            base = offset - len(tail)
            for strand, found in zip(strands, hits):
                start = max(0, len(tail) - len(strand) + 1)
                while len(found) < 2 and (at := block.find(strand, start)) != -1:
                    found.append((name, base + at + 1, base + at + len(strand)))
                    start = at + 1
            offset = base + len(block)
            tail, parts, size = block[-overlap:] if overlap else "", [], 0
    locations = [found[0] for found in hits if len(found) == 1]
    if len(locations) != len(strands):
        return []
    rnas = _rna_names_for_records([name for name, _, _ in locations], inputs)
    if any(name not in rnas for name, _, _ in locations):
        return []
    return [(rnas[name], name, start, end) for name, start, end in locations]


def _sequence_source(request: Dict[str, Any], project: Path, engine: str,
                     mode: str) -> Tuple[Dict[str, Any], Optional[Dict[str, Any]]]:
    """A pasted or HYB record sequence (``sequence``, and for CPLcofold
    ``sequence_two`` or one "A&B" sequence).  As the web, chimeras guide only
    the fold of a reference region: when evidence is asked for and every
    strand identifies one position and one HYB RNA, it is folded as that
    region (one window, or CPLcofold's two regions); otherwise from the
    sequence alone (COMRADES, which needs chimeras, cannot run). Returns
    the request to run and the source to record (None: a reference region)."""
    request = {key: value for key, value in request.items() if not key.startswith("_")}
    source = request.get("source", "reference")
    if source == "reference":
        if request.get("sequence") is not None or request.get("sequence_two") is not None:
            raise ValueError("a sequence needs source 'paste' or 'record'")
        return request, None
    if source not in ("paste", "record"):
        raise ValueError("source must be 'reference', 'paste' or 'record'")
    if engine == "iris":
        raise ValueError("IRIS uses one reference region with HYB evidence: choose Reference region")
    if mode not in ("genome", "cofold") or request.get("windows") or request.get("regions"):
        raise ValueError("a pasted or HYB record sequence is folded whole, or as two strands with CPLcofold")
    first = fold_text(request.get("sequence"), pasted=source == "paste")
    second = fold_text(request["sequence_two"], pasted=source == "paste") if request.get("sequence_two") is not None else ""
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
        bad = sorted(set(strand) - set(FOLD_TEXT["alphabet"]))
        if bad:
            raise ValueError(f"the {name} may contain only A, C, G and U (T is read as U); found " + ", ".join(bad))
        validate_input_length(len(strand), f"the {name}", source=source, paired=len(strands) == 2)
    label = "_".join(str(request.get("gene_id") or "pasted").split())
    names = [label] if len(strands) == 1 else [label + "_A", label + "_B"]
    params = request.get("parameters") if isinstance(request.get("parameters"), dict) else {}
    guided = engine == "comrades" or (engine in ("cplfold", "cplcofold") and params.get("evidence", "hyb") != "none")
    found = _locate_strands(strands, _evidence_inputs(project, request)) if guided else []
    out = {key: value for key, value in request.items() if key not in ("source", "sequence", "sequence_two")}
    recorded: Dict[str, Any] = {"kind": source, "label": label, "lengths": [len(strand) for strand in strands]}
    if found:
        recorded["mapped_to"] = [f"{gene}:{start}-{end}" for gene, _, start, end in found]
        regions = [{"gene_id": gene, "start": start, "end": end} for gene, _, start, end in found]
        out["_located_ids"] = {gene: fasta_id for gene, fasta_id, _, _ in found}
        if mode == "cofold":
            out["regions"] = regions
        else:
            out.update(gene_id=regions[0]["gene_id"], mode="windows", windows=[{"start": regions[0]["start"], "end": regions[0]["end"]}])
        return out, recorded
    if engine == "comrades":
        raise ValueError(f"COMRADES needs the chimeras of a reference region, and the {what} does not identify "
                         "a unique reference region and HYB RNA: fold it with ViennaRNA MFE or sequence-only CPLfold, "
                         "or choose Reference region")
    if guided:
        out["parameters"] = {**params, "evidence": "none"}
        recorded["evidence"] = "none: the sequence does not identify a unique reference region and HYB RNA"
    # folded from the sequence alone, under its label (two strands: two regions)
    out["gene_id"], out["_strands"] = names[0], dict(zip(names, strands))
    if mode == "cofold":
        out["regions"] = [{"gene_id": name, "start": 1, "end": len(strand)} for name, strand in zip(names, strands)]
    return out, recorded


def _fold_sequence(request: Dict[str, Any], inputs: Dict[str, Any]) -> Tuple[str, str]:
    """Return the HYB name for evidence/results and the bases from its FASTA match (or pasted record)."""
    strands = request.get("_strands")
    if strands:
        return request["gene_id"], strands[request["gene_id"]]
    gene = inputs["gene_id"]
    matches = request.get("_located_ids", inputs["matches"])
    target = fasta_id_for_rna(gene, matches) if gene is not None else None
    name, sequence = reference_sequence(inputs["reference"], target, open_text=_open_reference)
    if gene is None:
        gene = _rna_names_for_records([name], inputs).get(name)
        if gene is None:
            raise ValueError(f"reference sequence {name!r} does not identify one matched HYB RNA; give gene_id")
    if request.get("mode", "genome") == "genome":
        validate_input_length(len(sequence), "Reference sequence")
    return gene, sequence


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
    kind = str(raw.get("kind") or "phase1")
    with_bonus = any(pair[3] is not None for pair in pairs)
    return {
        "rank": rank, "type": kind, "topology": "pseudoknotted" if knotted or kind == "pseudoknot" else "nested",
        "energy": _number(raw.get("energy")), "effective_energy": _number(raw.get("effective_energy")),
        "phase1_energy": _number(raw.get("phase1_energy")), "score": _number(raw.get("score")),
        "structure": structure, "pairs": len(pairs), "pseudoknot_pairs": knotted,
        "pairs_with_bonus": sum(1 for pair in pairs if (pair[3] or 0) > 0),
        "bonus": [pair[3] for pair in pairs] if with_bonus else None,
    }


@contextmanager
def _atomic_writer(path: Path) -> Iterator[Any]:
    """A text handle whose content replaces ``path`` when the block ends.  The
    temporary file has a unique name (two writers of one directory never
    share it) and is removed when the block fails."""
    descriptor, temporary = tempfile.mkstemp(dir=path.parent, prefix="." + path.name + ".", suffix=".tmp")
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            yield handle
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise


def _bonus_header(length: int, label: str, cutpoint: Optional[int] = None,
                  transform: str = BONUS_TRANSFORM, kernel: str = "gaussian") -> str:
    """The web's bonus-matrix TSV header (web/structure-ui.js cplfoldEvidenceTsv),
    which cplfold.bonus.load_bonus_matrix_file reads; the region line is a comment.
    The value column keeps its name for every support kernel."""
    lines = ["# HYB2 CPLfold bonus matrix", f"# region={label}", f"# sequence_length={length}",
             "# coordinate_system=prepared-sequence-1-based-inclusive", f"# transform={transform}"]
    if kernel != "gaussian":
        lines.append(f"# support_kernel={kernel}")
    if cutpoint is not None:
        lines += [f"# cutpoint={cutpoint}", f"# strand_lengths={cutpoint},{length - cutpoint}",
                  "# cofold_evidence_smoothing=within_each_strand"]
    lines.append("prepared_position_1\tprepared_position_2\tlog1p_gaussian_hyb_bonus")
    return "\n".join(lines) + "\n"


def write_bonus_matrix(path: Path, bonus: Any, label: str, transform: str = BONUS_TRANSFORM,
                       kernel: str = "gaussian") -> int:
    """The sparse upper triangle of a dense bonus matrix, 1-based, eight
    decimals as the web's export; returns the cells written."""
    import numpy as np

    n = int(bonus.shape[0])
    cells = 0
    with _atomic_writer(path) as handle:
        handle.write(_bonus_header(n, label, transform=transform, kernel=kernel))
        for row in range(n - 1):
            values = bonus[row, row + 1:]
            columns = np.flatnonzero(values)
            if columns.size:
                handle.write("".join(f"{row + 1}\t{row + 2 + int(column)}\t{float(values[column]):.8f}\n"
                                     for column in columns))
                cells += int(columns.size)
    return cells


def _nucleotide_support(bonus: Any) -> Optional[List[float]]:
    """Each nucleotide's strongest HYB bonus (the web's nucleotideSupport)."""
    if bonus is None:
        return None
    return [round(float(value), 6) for value in bonus.max(axis=1)]


def fold_one(module: Any, sequence: str, arms: Any, params: Dict[str, Any],
             bonus_path: Optional[str] = None, label: str = "",
             cofold_progress: Optional[Callable[[int, int], None]] = None,
             duplex_workers: Optional[int] = None) -> Dict[str, Any]:
    """Build the bonus from compressed arms and fold; returns a JSON-able record.
    With ``bonus_path`` the bonus matrix is also written there (when it is
    small enough, see BONUS_EXPORT_MAX_CELLS). ``cofold_progress(done, total)``
    follows arm-pair read support, which uses at most ``duplex_workers`` processes
    (None: the CPU count)."""
    started = time.monotonic()
    kernel = params.get("support_kernel", DEFAULT_SUPPORT_KERNEL)
    counts = None
    if kernel == "gaussian":
        bonus, evidence = module.build_hyb_bonus_matrix_fast(len(sequence), arms)
    elif kernel in PAIR_ESTIMATORS and params.get("evidence") != "none":
        counts, counter_info = (module.arm_pair_counts(sequence, arms, kernel=kernel, progress=cofold_progress, workers=duplex_workers)
                                if arms else (Counter(), {}))
        bonus, evidence = module.build_support_bonus_matrix(len(sequence), arms, kernel, pairs=counts)
        evidence.update(counter_info)
    else:
        bonus, evidence = module.build_support_bonus_matrix(len(sequence), arms, kernel, sequence=sequence)
    evidence["supportKernel"] = kernel
    bonus_seconds = time.monotonic() - started
    bonus_file = None
    if bonus_path and bonus is not None and int(evidence.get("nonzeroUpperTriangleCells") or 0) <= BONUS_EXPORT_MAX_CELLS:
        write_bonus_matrix(Path(bonus_path), bonus, label, evidence.get("transform") or BONUS_TRANSFORM, kernel)
        bonus_file = Path(bonus_path).name
    started = time.monotonic()
    results = module.fold_prepared(
        sequence, None, bonus, pseudoknots=params["allow_pseudoknot"], energy_model=params["energy_model"],
        alpha=params["alpha"] if bonus is not None else 0.0, beta=params["beta"], beam_size=params["beam"],
        energy_delta=params["energy_delta"], max_phase1=params["max_phase1"])
    fold_seconds = time.monotonic() - started
    if not results:
        raise ValueError("CPLfold did not produce a structure")
    # bonus None: folded from the sequence alone, the pairs carry no HYB evidence
    candidates = []
    for rank, prediction in enumerate(results[:CANDIDATE_LIMIT]):
        folded = [(i, j, bracket, float(bonus[i - 1, j - 1]) if bonus is not None else None)
                  for i, j, bracket in _pairs(prediction.structure)]
        candidate = _candidate(rank, prediction.as_dict(), prediction.structure, folded)
        if counts is not None:
            candidate.update(module.score_evidence_pairs(counts, ((i, j) for i, j, _, _ in folded)), support_kernel=kernel)
        candidates.append(candidate)
        if rank == 0:
            pairs = folded
    best = results[0]
    support = _nucleotide_support(bonus)
    del bonus
    return {
        "structure": best.structure,
        "energy": best.energy,
        "type": best.kind,
        "pairs": pairs,
        "evidence": evidence,
        "candidates": candidates,
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
    # windows fold in parallel worker processes, which compute their duplexes in-process
    return index, fold_one(module, sequence, arms, params, bonus_path, label, duplex_workers=1)


def run_in_pool(workers: int, function: Callable[..., Any], jobs: Sequence[Tuple[Any, ...]],
                on_result: Callable[[Any], None], *, initializer: Optional[Callable[..., None]] = None,
                initargs: Tuple[Any, ...] = ()) -> None:
    """``function(*job)`` of each job in ``workers`` spawned processes,
    ``on_result`` called with each result as it finishes.  The first failure
    is raised at once: the queued jobs are cancelled and the running ones
    stopped, rather than waited for as leaving ``with ProcessPoolExecutor``
    would."""
    pool = ProcessPoolExecutor(max_workers=workers, mp_context=get_context("spawn"), initializer=initializer,
                               initargs=initargs)
    try:
        futures = [pool.submit(function, *job) for job in jobs]
        for future in as_completed(futures):
            on_result(future.result())
    except BaseException:
        stop_pool(pool)
        raise
    pool.shutdown(wait=True)


def stop_pool(pool: ProcessPoolExecutor) -> None:
    """Cancel a pool's queued jobs and end its workers without waiting
    for the jobs they run."""
    processes = list((getattr(pool, "_processes", None) or {}).values())
    pool.shutdown(wait=False, cancel_futures=True)
    for process in processes:
        if process.is_alive():
            process.terminate()
    for process in processes:
        process.join(timeout=5)
        if process.is_alive():
            process.kill()


# ---------------------------------------------------------------------------
# output
# ---------------------------------------------------------------------------

def _atomic_write_text(path: Path, text: str) -> None:
    with _atomic_writer(path) as handle:
        handle.write(text)


def _describe(params: Dict[str, Any]) -> str:
    """The .dbn header's account of how a record was folded."""
    if str(params.get("engine", "")).startswith("COMRADES("):
        return (f"{params['engine']}, {params['temperature']:g} °C, min loop {params['min_loop']}, "
                f"{params['random_folds']} randomised folds, seed {_comrades.seed_label(params['seed'])}")
    if params.get("engine") == "IRIS":
        return (f"IRIS ensemble member 1 (heaviest), K {params['K']}, C {params['C']}, support quantile "
                f"{params['fraction']:g}, stem lengths {params['kRange']}, {params['temperature']:g} °C, min loop {params['min_loop']}")
    if params.get("engine") == "ViennaRNA":
        hard = f", {len(params['hard_pairs'])} hard pairs" if params.get("hard_pairs") else ""
        return f"ViennaRNA MFE, {params['temperature']:g} °C, min loop {params['min_loop']}{hard}"
    mode = "pseudoknot" if params["allow_pseudoknot"] else "pseudoknot-free"
    name = "CPLcofold" if params.get("engine") == "CPLcofold" else "CPLfold"
    if params.get("evidence") == "none":
        return f"{name} {mode}, sequence only, beam {params['beam']}"
    kernel = params.get("support_kernel", DEFAULT_SUPPORT_KERNEL)
    support = "" if kernel == "gaussian" else f", {kernel} support"
    if kernel in PAIR_ESTIMATORS:
        support += f" ({PAIR_ESTIMATORS[kernel]}, saturating)"
    return f"{name} {mode}, alpha {params['alpha']}, beam {params['beam']}{support}"


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
    _write_arms(out_dir, records, paths)
    return {key: str(value) for key, value in paths.items()}


# The raw HYB arms drawn under the arc diagram (the web's "Raw HYB
# interactions · below"): at most this many interval blocks per window.
ARMS_SHOWN = 2000


def _arms_sample(counter: Any) -> Dict[str, Any]:
    """A window's evidence for display: its HYB rows, its unique arm blocks
    and the ARMS_SHOWN most frequent blocks as [a0, a1, b0, b1, rows] in
    window coordinates (1-based, inclusive)."""
    items = sorted(counter.items(), key=lambda item: (-item[1], item[0])) if counter else []
    return {"rows": int(sum(counter.values())) if counter else 0, "unique": len(items),
            "shown": [[int(a0), int(a1), int(b0), int(b1), int(n)] for (a0, a1, b0, b1), n in items[:ARMS_SHOWN]]}


def _write_arms(out_dir: Path, records: List[Dict[str, Any]], paths: Dict[str, Path]) -> None:
    """arms.json: each record's evidence sample (:func:`_arms_sample`),
    aligned with the records of structure.dbn.  As the web's rawHybArms:
    written whenever the fold maps the samples' chimeras onto the sequence
    (CPLfold with HYB evidence, CPLcofold of two regions, COMRADES, IRIS),
    so a record with no row says so; nothing for ViennaRNA and
    sequence-only CPLfold, which map none."""
    if not any(isinstance(record.get("arms"), dict) for record in records):
        return
    paths["arms"] = out_dir / "arms.json"
    _atomic_write_json(paths["arms"], {"version": 1,
                                       "records": [record.get("arms") or _arms_sample(None) for record in records]})


def _blocks(arms: List[Dict[str, Any]], move: Optional[Callable[[int], int]] = None) -> Counter:
    """COMRADES evidence arms ({one, two, oneStart, twoStart}) as interval
    blocks with their row counts; ``move`` maps each position."""
    move = move or (lambda k: k)
    return Counter((move(a["oneStart"]), move(a["oneStart"] + len(a["one"]) - 1),
                    move(a["twoStart"]), move(a["twoStart"] + len(a["two"]) - 1)) for a in arms)


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
    summary = {key: evidence.get(key) for key in (
        "source", "builder", "supportKernel", "inputRecords", "uniqueBlocks", "meanArmLength", "gaussianStandardDeviation",
        "nonzeroBonusCells", "nonzeroUpperTriangleCells", "maximumBonus", "truncationTail", "transform", "evidenceWeight")}
    summary.update({key: evidence[key] for key in ("pairCounter", "hybridisedBlocks", "duplexesWithPairs", "duplexWorkers",
                                                  "supportedPairs", "saturatingQ", "saturatingScale") if key in evidence})
    return {
        "start": record["start"], "end": record["end"], "length": len(record["sequence"]),
        "energy": record["energy"], "structure_type": record["type"],
        "pairs": len(pairs),
        "pairs_with_bonus": sum(1 for pair in pairs if (pair[3] or 0) > 0),
        "pseudoknot_pairs": sum(1 for pair in pairs if pair[2] != "()"),
        "long_range_pairs_gt_1000": sum(1 for pair in pairs if pair[1] - pair[0] > 1000),
        "evidence": summary,
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
    task_id = safe_task_id(request.get("task_id"), "structure-")
    mode = request.get("mode", "genome")
    engine = request.get("engine", "cplfold")
    if engine not in {"cplfold", "cplcofold", "viennarna", "comrades", "iris"}:
        raise ValueError("engine must be 'cplfold', 'cplcofold', 'viennarna', 'comrades' or 'iris'")
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
    if engine == "iris":
        from .iris import structure_run as _iris_run  # IRIS: one reference region (iris.py)
        return _iris_run(request, project, task_id, mode, state, started)
    if engine != "cplfold":
        return _vienna_run(request, project, task_id, mode, state, started, engine)
    params = _parameters(request.get("parameters"), mode)
    inputs = _evidence_inputs(project, request)
    gene_id, sequence = _fold_sequence(request, inputs)
    windows = _windows(request.get("windows"), len(sequence), source=state["source"]) if mode == "windows" else [(1, len(sequence))]

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
    progress.work = {"lengths": [end - start + 1 for start, end in windows], "beam": params.get("beam"), "workers": workers,
                     "pseudoknot": params["allow_pseudoknot"], "guided": params["evidence"] == "hyb"}

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
        if params["evidence"] != "none":
            record["arms"] = _arms_sample(counters[index])  # sequence only maps no chimera
        records[index] = record

    # each window's bonus matrix, for a local CPLfold run of that window
    bonus_paths = [str(out_dir / ("bonus_matrix.tsv" if len(windows) == 1 else f"bonus_matrix_{start}-{end}.tsv"))
                   for start, end in windows]
    labels = [f"{gene_id}:{start}-{end}" for start, end in windows]
    estimator = PAIR_ESTIMATORS.get(params["support_kernel"]) if params["evidence"] == "hyb" else None
    step = f" · {estimator} read support" if estimator else ""
    if workers <= 1:
        for index, ((start, end), arms) in enumerate(zip(windows, counters)):
            detail = f"window {index + 1}/{len(windows)} ({start}-{end})" if mode == "windows" else "folding"
            progress("fold", detail + step, index, len(windows), units="windows", force=True)

            def pair_progress(done: int, total: int, index: int = index) -> None:
                progress("fold", f"{estimator} read support {done:,}/{total:,} arm blocks",
                         index, len(windows), units="windows")
            finish(index, fold_one(module, sequence[start - 1:end], arms, params, bonus_paths[index], labels[index],
                                   pair_progress, duplex_workers=cpu))
    else:
        done = 0

        def folded(result: Tuple[int, Dict[str, Any]]) -> None:
            nonlocal done
            finish(*result)
            done += 1
            progress("fold", f"{done}/{len(windows)} windows on {workers} workers{step}", done, len(windows),
                     units="windows", force=True)

        progress("fold", f"0/{len(windows)} windows on {workers} workers{step}", 0, len(windows), units="windows", force=True)
        run_in_pool(workers, _fold_window,
                    [(str(root), index, sequence[start - 1:end], list(arms.keys()), list(arms.values()), params,
                      bonus_paths[index], labels[index])
                     for index, ((start, end), arms) in enumerate(zip(windows, counters))],
                    folded, initializer=_worker_init, initargs=(str(root),))
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


def _comrades_evidence(progress: ProgressWriter, hyb_paths: Sequence[Path],
                       read: Callable[[Callable[[int], None]], Any]) -> Any:
    """``read(report)`` of the COMRADES evidence, the .hyb bytes read
    reported as the "evidence" stage."""
    total = sum(path.stat().st_size for path in hyb_paths)
    progress("evidence", "reading .hyb", 0, total, force=True)
    found = read(lambda done: progress("evidence", "reading .hyb", done, total))
    progress("evidence", "done", total, total, force=True)
    return found


# The share of a COMRADES fold's progress given to the RNAcofold evidence;
# the constrained ViennaRNA fits take the rest.
_COFOLD_SHARE = 0.5


def _comrades_progress(progress: ProgressWriter, what: str, index: int, count: int) -> Callable[[str, int, int], None]:
    """:func:`comrades.fold`'s progress as the "fold" stage: window
    ``index`` of ``count`` advances from ``index`` to ``index + 1`` windows."""
    def report(stage: str, done: int, total: int) -> None:
        part = done / total if total else 1.0
        share = _COFOLD_SHARE * part if stage == "RNAcofold evidence" else _COFOLD_SHARE + (1 - _COFOLD_SHARE) * part
        progress("fold", f"{what}: {stage} {done}/{total}", round(index + share, 4), count, units="windows")
    return report


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
    windows = _windows(request.get("windows"), len(sequence), source=state["source"]) if mode == "windows" else [(1, len(sequence))]
    longest = max(end - start + 1 for start, end in windows)
    limit = _comrades.MAX_COMRADES_NT if comrades else _vienna.MAX_VIENNA_NT
    if longest > limit:
        raise ValueError(f"{longest} nt exceeds the {_comrades.engine_name(params) if comrades else 'ViennaRNA'} limit of {limit} nt; fold windows")
    if comrades:
        _comrades._folder(params)  # a missing mfold fails before the evidence is read
    # each hard pair applies to the windows that hold both its ends
    hard = params.get("hard_pairs", [])
    per_window = [[(i, j) for i, j in hard if start <= i and j <= end] for start, end in windows]
    outside = [f"{i}-{j}" for i, j in hard if not any(start <= i and j <= end for start, end in windows)]
    if outside:
        raise ValueError("hard pair(s) " + ", ".join(outside) + " are not inside a folded window")
    local = [_vienna.check_hard_pairs(pairs, sequence[start - 1:end], params["min_loop"], start)
             for pairs, (start, end) in zip(per_window, windows)]

    run_dir = _resolve_run_dir(project, task_id, request.get("run_dir"))
    state.update(run_dir=run_dir, task_id=task_id)
    out_dir = _real_child_dir(project / "structures", task_id, exist_ok=True)
    progress = ProgressWriter(run_dir)
    progress.work = {"lengths": [end - start + 1 for start, end in windows], "engine": engine, "workers": 1}
    arms = []
    t0 = time.monotonic()
    if comrades:
        # every window's chimeras in one pass over the .hyb files
        arms = [found for found, _ in _comrades_evidence(
            progress, inputs["hyb_paths"], lambda report: _comrades.window_arms(
                inputs["hyb_paths"], gene_id, sequence, windows, params["homodimer_only"], report))]
        for found, (start, end) in zip(arms, windows):
            if not found:
                raise ValueError(f"no {gene_id} chimeras have both arms inside {start}-{end}")
    evidence_seconds = time.monotonic() - t0
    records: List[Dict[str, Any]] = []
    t0 = time.monotonic()
    for index, ((start, end), pairs) in enumerate(zip(windows, local)):
        what = f"window {index + 1}/{len(windows)} ({start}-{end})" if mode == "windows" else "folding"
        progress("fold", what, index, len(windows), units="windows", force=True)
        if comrades:
            record = _comrades.fold(sequence[start - 1:end], arms[index], params,
                                    _comrades_progress(progress, what, index, len(windows)))
        else:
            record = _vienna.fold(sequence[start - 1:end], params, pairs)
        record.update({"start": start, "end": end, "gene_id": gene_id, "sequence": sequence[start - 1:end]})
        if comrades:
            record["arms"] = _arms_sample(_blocks(arms[index]))  # window positions
        records.append(record)
    fold_seconds = time.monotonic() - t0

    progress("write", "writing results", None, None, units="none", force=True)
    name = _comrades.engine_name(params) if comrades else "ViennaRNA"
    if comrades:
        # the stem constraints the chosen structures were fitted with, on the RNA
        hard = [(record["start"] + i - 1, record["start"] + j - 1) for record in records for i, j in record["hard_pairs"]]
        stored = {"engine": name, **{k: params[k] for k in ("temperature", "min_loop", "constraint_limit", "random_folds",
                                                              "seed", "homodimer_only", "fold_program")}}
        stored["seed"] = _comrades.seed_label(stored["seed"])  # stored as UTF-8 text
    else:
        stored = {"engine": name, "temperature": params["temperature"], "min_loop": params["min_loop"]}
    stored["hard_pairs"] = [list(p) for p in hard]
    outputs = _write_outputs(out_dir, records, stored)
    summaries = [_summary(record) for record in records]
    meta = {
        "version": 1, "task_id": task_id, "mode": mode, "engine": name,
        "engine_version": _comrades.fold_program_version(params) if comrades else _vienna.version(),
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

def _two_regions(request: Dict[str, Any], inputs: Dict[str, Any], *, cofold: bool) -> List[Dict[str, Any]]:
    """Only CPLcofold treats the excised windows as independent strands and ignores their flanks."""
    raw = request.get("regions")
    if not isinstance(raw, list) or len(raw) != 2:
        raise ValueError("cofold mode needs exactly two regions")
    selected = []
    strands = request.get("_strands")  # sequence-only pasted strands are their own records
    matches = request.get("_located_ids", inputs["matches"])
    for index, region in enumerate(raw, 1):
        try:
            gene, start, end = region["gene_id"], parse_coordinate(region["start"]), parse_coordinate(region["end"])
        except (KeyError, TypeError, ValueError) as error:
            raise ValueError(f"region {index} needs gene_id, start and end") from error
        if not isinstance(gene, str):
            raise ValueError(f"region {index} needs an RNA name")
        selected.append((gene, gene if strands is not None else fasta_id_for_rna(gene, matches), start, end))
    targets = {target for _, target, _, _ in selected}
    records = strands if strands is not None else {
        name: fold_text(sequence) for name, _, sequence in fasta_records(inputs["reference"], targets, open_text=_open_reference)}
    regions = []
    for index, (gene, target, start, end) in enumerate(selected, 1):
        if target not in records:
            raise ValueError(f"region {index}: {target} is not a sequence of the reference FASTA")
        sequence = records[target]
        if not 1 <= start <= end <= len(sequence):
            raise ValueError(f"region {index} ({start}-{end}) is outside 1-{len(sequence)} of {gene}")
        validate_input_length(end - start + 1, f"region {index}", paired=True)
        part = sequence[start - 1:end]
        bad = unsupported_bases(part if cofold else sequence)
        if bad:
            label = f"region {index} ({gene}:{start}-{end})" if cofold else f"Reference sequence {gene}"
            raise ValueError(f"{label} contains unsupported symbols: " + ", ".join(sorted(bad)))
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
    _write_arms(out_dir, [record], paths)
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


def _task_cpus() -> int:
    """The CPUs this task may use: the CPU count, capped by HYB2_TASK_CPUS."""
    cpu = os.cpu_count() or 1
    task_cpus = _env_int("HYB2_TASK_CPUS")
    return max(1, min(cpu, task_cpus)) if task_cpus is not None else cpu


def _cofold_run(request: Dict[str, Any], project: Path, task_id: str, state: Dict[str, Any],
                started: float, repo_root: Optional[Path]) -> Dict[str, Any]:
    """Two regions folded as two strands with CPLcofold (the web's
    two-region CPLfold layout), with or without the chimeras."""
    params = _parameters(request.get("parameters"), "cofold")
    inputs = _evidence_inputs(project, request)
    regions = _two_regions(request, inputs, cofold=True)
    sequence = regions[0]["sequence"] + regions[1]["sequence"]
    limits = _memory_limits(request)
    key = "pseudoknot_max_nt" if params["allow_pseudoknot"] else "pseudoknot_free_max_nt"
    hard_max_length = int(limits[key])
    if len(sequence) > hard_max_length:
        raise ValueError(f"{len(sequence)} nt exceeds the CPLfold limit of {hard_max_length} nt for this memory budget "
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
    progress.work = {"lengths": [len(sequence)], "beam": params["beam"], "workers": 1,
                     "pseudoknot": params["allow_pseudoknot"], "guided": params["evidence"] == "hyb"}

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
    estimator = PAIR_ESTIMATORS.get(params["support_kernel"]) if arms else None
    progress("fold", f"folding · {estimator} read support" if estimator else "folding", 0, 1, units="windows", force=True)

    def pair_progress(done: int, total: int) -> None:
        progress("fold", f"{estimator} read support {done:,}/{total:,} arm blocks", 0, 1, units="windows")

    t0 = time.monotonic()
    folded = module.fold({
        "sequence": regions[0]["sequence"], "sequenceTwo": regions[1]["sequence"],
        "evidenceMode": "hyb-blocks" if arms else "none",
        "evidenceArmCounts": [[a, b, c, d, n] for (a, b, c, d), n in arms.items()],
        "beamSize": params["beam"], "maxPhase1": params["max_phase1"], "energyDelta": params["energy_delta"],
        "energyModel": params["energy_model"], "alpha": params["alpha"] if arms else 0.0, "beta": params["beta"],
        "allowPseudoknot": params["allow_pseudoknot"], "maxSequenceLength": len(sequence),
        "supportKernel": params.get("support_kernel", DEFAULT_SUPPORT_KERNEL),
        # Arm-pair estimation may run in parallel: respect the task's CPU claim.
        "duplexWorkers": _task_cpus()}, progress=pair_progress if estimator else None,
        host="desktop", hard_max_length=hard_max_length)
    fold_seconds = time.monotonic() - t0
    pairs = [(pair["left"], pair["right"], pair["bracket"], float(pair["bonusSupport"]) if arms else None)
             for pair in folded["pairs"]]
    raw_evidence = folded.get("evidence") or {}
    evidence = {k: v for k, v in raw_evidence.items() if k not in ("bonusEntries", "nucleotideSupport")}
    # the web's candidates, on A+B indices as the dot-bracket
    candidates = []
    for rank, candidate in enumerate((folded.get("candidates") or [])[:CANDIDATE_LIMIT]):
        raw = {"kind": candidate.get("type"), "energy": candidate.get("energy"),
               "effective_energy": candidate.get("effectiveEnergy"), "phase1_energy": candidate.get("phase1Energy"),
               "score": candidate.get("score")}
        candidate_pairs = [(pair["left"], pair["right"], pair["bracket"], float(pair["bonusSupport"]) if arms else None)
                           for pair in candidate.get("pairs") or []]
        stored_candidate = _candidate(rank, raw, candidate["dotBracket"], candidate_pairs)
        if params["support_kernel"] in PAIR_ESTIMATORS and arms:
            stored_candidate.update(comradesScore=candidate["comradesScore"], comradesPairCounts=candidate["comradesPairCounts"],
                                    support_kernel=params["support_kernel"])
        candidates.append(stored_candidate)
    bonus_file = None
    entries = raw_evidence.get("bonusEntries") or []
    if arms and entries and len(entries) <= BONUS_EXPORT_MAX_CELLS:
        # the web's two-strand export: 17 significant digits, with the cutpoint
        header = _bonus_header(len(sequence), _cofold_label(regions), regions[0]["prepared_end"],
                               transform=raw_evidence.get("transform") or BONUS_TRANSFORM,
                               kernel=params.get("support_kernel", DEFAULT_SUPPORT_KERNEL))
        _atomic_write_text(out_dir / "bonus_matrix.tsv", header + "".join(
            f"{int(entry['one'])}\t{int(entry['two'])}\t{float(entry['value']):.17g}\n" for entry in entries))
        bonus_file = "bonus_matrix.tsv"
    support = folded.get("nucleotideSupport") if arms else None
    record = {"structure": folded["dotBracket"], "energy": folded.get("energy"), "type": folded.get("structureType"),
              "pairs": pairs, "evidence": evidence, "sequence": sequence, "start": 1, "end": len(sequence),
              "candidates": candidates, "candidates_found": len(folded.get("candidates") or []),
              "nucleotide_support": [round(float(value), 6) for value in support] if support else None,
              "bonus_matrix": bonus_file,
              # the web's rawHybArms in A+B positions: none mapped for a sequence-only
              # fold of two reference regions, not attached for pasted strands
              "arms": None if request.get("_strands") else _arms_sample(arms),
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
                                                                "memory_budget_bytes", "source")},
                    # the selected candidate's energy model and kind (the web's
                    # energyMetadata: "heuristic" for an experimental pseudoknot energy)
                    "energy_metadata": dict(folded.get("energyMetadata") or {})},
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
    regions = _two_regions(request, inputs, cofold=False)
    one, two = regions[0]["sequence"], regions[1]["sequence"]
    prepared = one + checked_fold_text(_comrades.LINKER, "COMRADES spacer") + two
    if len(prepared) > _comrades.MAX_COMRADES_NT:
        raise ValueError(f"{len(prepared)} nt (both regions and the 100-nt spacer) exceeds the COMRADES "
                         f"limit of {_comrades.MAX_COMRADES_NT} nt; choose shorter regions")
    _comrades._folder(params)  # a missing mfold fails before the evidence is read

    run_dir = _resolve_run_dir(project, task_id, request.get("run_dir"))
    state.update(run_dir=run_dir, task_id=task_id)
    out_dir = _real_child_dir(project / "structures", task_id, exist_ok=True)
    progress = ProgressWriter(run_dir)
    progress.work = {"lengths": [len(prepared)], "engine": "comrades", "workers": 1}
    t0 = time.monotonic()
    layout = [dict(regions[0], prepared_start=1), dict(regions[1], prepared_start=len(one) + len(_comrades.LINKER) + 1)]
    arms, skipped = _comrades_evidence(progress, inputs["hyb_paths"], lambda report: _comrades.paired_arms(
        inputs["hyb_paths"], layout, prepared, params["homodimer_only"], report))
    if not arms:
        raise ValueError(f"no chimeras have one arm in each of {_cofold_label(regions)}")
    evidence_seconds = time.monotonic() - t0
    progress("fold", "folding", 0, 1, units="windows", force=True)
    t0 = time.monotonic()
    folded = _comrades.fold(prepared, arms, params, _comrades_progress(progress, "folding", 0, 1))
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
    # on the RNA, i < j also when region 2 lies before region 1
    hard = [sorted((positions[local(i) - 1], positions[local(j) - 1])) for i, j in folded["hard_pairs"]
            if local(i) is not None and local(j) is not None]
    record = dict(folded, structure="".join(dot), pairs=pairs, sequence=one + two, start=1, end=len(one) + len(two),
                  arms=_arms_sample(_blocks(arms, local)))  # A+B positions, without the spacer
    stored = {"engine": _comrades.engine_name(params), **{k: params[k] for k in ("temperature", "min_loop", "constraint_limit",
                                                                       "random_folds", "seed", "homodimer_only", "fold_program")},
              "layout": "two regions joined by the 100-nt A/U spacer", "hard_pairs": hard}
    stored["seed"] = _comrades.seed_label(stored["seed"])
    rows_text = (f"chimeras with one arm in each region ({len(arms)}), one RNAcofold each"
                 + (", homodimer proxies only" if params["homodimer_only"] else ""))
    return _finish_two_strand(
        task_id=task_id, mode="paired", engine=_comrades.engine_name(params), engine_version=_comrades.fold_program_version(params),
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


PairBonus = Dict[Tuple[int, Optional[int], int, int], float]


def _pair_bonus(path: Optional[Path]) -> PairBonus:
    """``(window_start, window_end, i, j) -> bonus`` from a pairs.tsv of
    :func:`_write_outputs`: keyed by the whole window, as nested windows
    (1-500 and 1-1000) share a start.  A file without a window_end column
    gives ``None`` for it (see :func:`_bonus_of`)."""
    out: PairBonus = {}
    if path is None:
        return out
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError:
        return out
    header = lines[0].split("\t") if lines else []
    try:
        columns = [header.index(name) for name in ("window_start", "i", "j", "bonus")]
    except ValueError:
        columns = [0, 2, 3, 5]  # the order every pairs.tsv was written in
    end_column = header.index("window_end") if "window_end" in header else None
    for line in lines[1:]:
        cells = line.split("\t")
        if len(cells) <= max(columns + [end_column or 0]):
            continue
        try:
            start, i, j, bonus = (cells[k] for k in columns)
            end = int(cells[end_column]) if end_column is not None else None
            out[(int(start), end, int(i), int(j))] = float(bonus)
        except ValueError:
            continue
    return out


def _bonus_of(bonus: PairBonus, start: int, end: int, i: int, j: int) -> Optional[float]:
    """A pair's bonus in window ``start``-``end``, also from a pairs.tsv
    without window_end."""
    value = bonus.get((start, end, i, j))
    return bonus.get((start, None, i, j)) if value is None else value


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
    arms = dbn.parent / "arms.json" if dbn is not None else None
    if arms is not None and arms.is_file() and not arms.is_symlink():
        try:
            loaded = json.loads(arms.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            loaded = {}
        rows = loaded.get("records") if isinstance(loaded, dict) else None
        for index, row in enumerate(rows if isinstance(rows, list) else []):
            if index < count and isinstance(row, dict):
                extras[index]["arms"] = row
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
                 bonus: PairBonus) -> Dict[str, Any]:
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
    n = len(record["sequence"])
    return {"structure": entry, "meta": meta, "records": [{
        "window_start": min(positions), "window_end": max(positions),
        "sequence": record["sequence"], "dbn": record["dbn"],
        # i < j also when region 2 lies before region 1 on the RNA
        "pairs": [sorted((positions[i - 1], positions[j - 1])) for i, j in local],
        "bonus": [_bonus_of(bonus, 1, n, i, j) for i, j in local] if bonus else None,
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
            "bonus": [_bonus_of(bonus, start, start + len(record["sequence"]) - 1, i, j) for i, j in pairs]
            if bonus else None,
            "energy": energy,
            **extras[index],
        })
    return {"structure": entry, "meta": meta, "records": records}
