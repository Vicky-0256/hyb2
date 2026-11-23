"""Small JSON bridge between the HYB2 web worker and pure-Python CPLfold.

The browser build intentionally runs without Numba.  Pyodide does not ship a
Numba package, so the decorators used by the upstream parser are replaced by
identity decorators before CPLfold is imported.  The algorithm itself is not
changed; only JIT compilation is disabled.
"""

from __future__ import annotations

import json
import math
import os
import sys
import types
from collections import Counter
from typing import Any


CPLFOLD_SOURCE_REVISION = "24bab52"
BRIDGE_VERSION = "8"
DEFAULT_MAX_SEQUENCE_LENGTH = 75
BROWSER_HARD_MAX_SEQUENCE_LENGTH = 500
BONUS_EXPORT_MAX_SEQUENCE_LENGTH = 5_000
MAX_EVIDENCE_RECORDS = 50_000
BRACKETS = {"(": ")", "[": "]", "{": "}", "<": ">"}
LAYER_NAMES = {
    "(": "primary",
    "[": "pseudoknot-1",
    "{": "pseudoknot-2",
    "<": "pseudoknot-3",
}


def _install_numba_fallback() -> None:
    """Expose the subset of numba used by CPLfold as identity decorators."""

    force_fallback = os.environ.get("CPLFOLD_FORCE_PURE_PYTHON") == "1"
    if not force_fallback:
        try:
            import numba  # noqa: F401

            return
        except ImportError:
            pass

    module = types.ModuleType("numba")

    def njit(*decorator_args: Any, **_decorator_kwargs: Any):
        if len(decorator_args) == 1 and callable(decorator_args[0]):
            return decorator_args[0]

        def decorate(function):
            return function

        return decorate

    module.njit = njit
    sys.modules["numba"] = module


_install_numba_fallback()

import numpy as np  # noqa: E402
import cplfold  # noqa: E402
from cplfold.bonus import cofold_counts, gaussian_bonus_matrix, gaussian_bonus_stripes, gaussian_sigma, mean_arm_length  # noqa: E402
from cplfold.sequence import add_strand_break, normalise_sequence, strip_structure_break  # noqa: E402
from hyb2analysis.fold_text import FOLD_TEXT, fold_text  # noqa: E402
from hyb2analysis.engine_parameters import engine_parameters, task_allow_pseudoknot  # noqa: E402
from hyb2analysis.support_kernels import DEFAULT_SUPPORT_KERNEL, PAIR_MODELS, SUPPORT_KERNELS  # noqa: E402


def _number(value: Any, minimum: float, maximum: float, label: str) -> float:
    if isinstance(value, bool):
        raise ValueError(f"{label} must be between {minimum} and {maximum}.")
    try:
        parsed = float(value)
    except (TypeError, ValueError) as error:
        raise ValueError(f"{label} must be between {minimum} and {maximum}.") from error
    if not math.isfinite(parsed) or parsed < minimum or parsed > maximum:
        raise ValueError(f"{label} must be between {minimum} and {maximum}.")
    return parsed


def _integer(value: Any, minimum: int, maximum: int, label: str) -> int:
    parsed = _number(value, minimum, maximum, label)
    if not parsed.is_integer():
        raise ValueError(f"{label} must be a whole number from {minimum} to {maximum}.")
    return int(parsed)


def _request_sequence_layout(payload: dict[str, Any]) -> tuple[str, int | None]:
    """Validate two-strand input while retaining the single-strand contract.

    All downstream coordinates omit ``&``.  The nick is the length of A,
    never an additional character in the sequence or bonus matrix.
    """
    raw = payload.get("sequence")
    second = payload.get("sequenceTwo")
    cutpoint = payload.get("cutpoint")
    requested_cofold = payload.get("cofold", False)
    if not isinstance(requested_cofold, bool):
        raise ValueError("cofold must be a boolean.")
    is_cofold = second is not None or cutpoint is not None or requested_cofold or (
        isinstance(raw, str) and "&" in raw
    )
    if not is_cofold:
        sequence = fold_text(raw)
        if not sequence:
            raise ValueError("CPLfold requires an RNA sequence.")
        if set(sequence).difference(FOLD_TEXT["alphabet"]):
            raise ValueError("CPLfold accepts only A, C, G and U.")
        return sequence, None

    if not isinstance(raw, str) or (second is not None and not isinstance(second, str)):
        raise ValueError("CPLcofold requires two RNA sequence strings.")
    if second is not None:
        if "&" in raw or "&" in second:
            raise ValueError("Supply sequence and sequenceTwo without '&', or one sequence containing '&'.")
        raw = raw + "&" + second
    # The shared validator requires an actual integer (not bool, float or a
    # numeric string), and checks it against a literal '&' after normalisation.
    sequence, cutpoint = normalise_sequence(raw, cutpoint)
    if cutpoint is None:
        raise ValueError("CPLcofold requires two nonempty strands, using sequenceTwo, '&' or cutpoint.")
    return sequence, cutpoint


def _normalise_arms(
    raw_arms: Any, sequence_length: int, cutpoint: int | None = None
) -> list[tuple[int, int, int, int]]:
    if raw_arms is None:
        return []
    if not isinstance(raw_arms, list):
        raise ValueError("HYB evidence arms must be an array.")
    if len(raw_arms) > MAX_EVIDENCE_RECORDS:
        raise ValueError(
            f"This region contains more than {MAX_EVIDENCE_RECORDS:,} eligible HYB rows. Select a smaller region."
        )

    arms: list[tuple[int, int, int, int]] = []
    for index, arm in enumerate(raw_arms):
        if not isinstance(arm, dict):
            raise ValueError(f"HYB evidence row {index + 1} is invalid.")
        try:
            values = [arm[key] for key in ("oneStart", "oneEnd", "twoStart", "twoEnd")]
            if cutpoint is None:
                one_start, one_end, two_start, two_end = map(int, values)
            else:
                one_start, one_end, two_start, two_end = (
                    _integer(value, 1, sequence_length, "HYB arm coordinate") for value in values
                )
        except (KeyError, TypeError, ValueError) as error:
            raise ValueError(f"HYB evidence row {index + 1} has invalid prepared coordinates.") from error
        if not (
            1 <= one_start <= one_end <= sequence_length
            and 1 <= two_start <= two_end <= sequence_length
        ):
            raise ValueError(f"HYB evidence row {index + 1} falls outside the prepared sequence.")
        if cutpoint is not None and (
            one_start <= cutpoint < one_end or two_start <= cutpoint < two_end
        ):
            raise ValueError(f"HYB evidence row {index + 1}: an individual read arm cannot cross the strand break.")
        arms.append((one_start, one_end, two_start, two_end))
    return arms


def _normalise_arm_counts(raw: Any, sequence_length: int, cutpoint: int) -> dict:
    """``[[one_start, one_end, two_start, two_end, rows], ...]`` as a block ->
    row count mapping, checked as ``_normalise_arms`` checks rows."""
    if not isinstance(raw, list):
        raise ValueError("HYB evidence counts must be an array.")
    counts: Counter = Counter()
    for index, item in enumerate(raw):
        try:
            if len(item) != 5:
                raise ValueError
            a, b, c, d = (_integer(value, 1, sequence_length, "HYB arm coordinate") for value in item[:4])
            rows = _integer(item[4], 1, 2**53, "HYB row count")
        except (TypeError, ValueError) as error:
            raise ValueError(f"HYB evidence block {index + 1} is invalid.") from error
        if not (a <= b and c <= d):
            raise ValueError(f"HYB evidence block {index + 1} falls outside the prepared sequence.")
        if a <= cutpoint < b or c <= cutpoint < d:
            raise ValueError(f"HYB evidence block {index + 1}: an individual read arm cannot cross the strand break.")
        counts[(a, b, c, d)] += rows
    return dict(counts)


def build_hyb_bonus_matrix(
    sequence_length: int, arms: Any, *, cutpoint: int | None = None
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """The parser matrix together with the per-cell listing used on the web."""
    bonus, summary = build_hyb_bonus_matrix_fast(sequence_length, arms, cutpoint=cutpoint)
    if bonus is not None:
        summary.update(bonus_matrix_listing(bonus))
    return bonus, summary


def build_hyb_bonus_matrix_export(
    sequence_length: int, arms: Any, cutpoint: int | None = None
) -> dict[str, Any]:
    """Emit nonzero upper-triangle cells, without materializing the fold matrix."""
    counts = _checked_blocks(sequence_length, arms) if cutpoint is None else cofold_counts(sequence_length, cutpoint, arms)
    summary = _gaussian_summary(sequence_length, cutpoint, counts)
    entries = summary["bonusEntries"] = []
    if counts:
        for start, stripe in gaussian_bonus_stripes(sequence_length, cutpoint, counts, summary["gaussianStandardDeviation"]):
            for i, values in enumerate(stripe, start):
                summary["nonzeroBonusCells"] += int(values[i] != 0)
                summary["maximumBonus"] = max(summary["maximumBonus"], float(values[i:].max()))
                for j in np.flatnonzero(values[i + 1:]) + i + 1:
                    entries.append({"one": i + 1, "two": int(j + 1), "value": float(values[j])})
    summary["nonzeroBonusCells"] += 2 * len(entries)
    summary["nonzeroUpperTriangleCells"] = len(entries)
    return summary


def _gaussian_summary(
    sequence_length: int, cutpoint: int | None, counts: Counter
) -> dict[str, Any]:
    summary = _empty_support("gaussian")
    if counts:
        summary = _summary(sequence_length, "gaussian", counts, 0, 0, 0.0)
        summary["gaussianStandardDeviation"] = gaussian_sigma(counts)
    if cutpoint is not None:
        summary.update({
            "coordinateSystem": "compact A+B, 1-based inclusive input; '&' has no coordinate",
            "cutpoint": cutpoint,
            "strandLengths": [cutpoint, sequence_length - cutpoint],
            "strandBounded": True,
        })
        if counts:
            summary["transform"] = "Strand-bounded " + summary["transform"]
    return summary


def build_cofold_hyb_bonus_matrix(
    sequence_length: int, cutpoint: int, arms: Any
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """Use the shared native/browser cofold transform, clipping at the nick.

    ``arms`` is a list of rows, or (native hosts with many chimeras) a
    mapping from a block to its row count: the same matrix without one
    entry per row."""
    return build_hyb_bonus_matrix(sequence_length, arms, cutpoint=cutpoint)


# The identity fallback installed above has no __version__.
_NUMBA_AVAILABLE = hasattr(sys.modules.get("numba"), "__version__")


def _compress_arms(arms: Any) -> Counter:
    if isinstance(arms, Counter):
        return arms
    if isinstance(arms, dict):
        return Counter({tuple(int(v) for v in key): int(weight) for key, weight in arms.items()})
    return Counter(tuple(int(v) for v in arm) for arm in arms)


def build_hyb_bonus_matrix_fast(
    sequence_length: int,
    arms: Any,
    *,
    cutpoint: int | None = None,
    use_numba: bool | None = None,
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """Build the parser's matrix from rows or block counts, without a per-cell listing."""
    counts = _checked_blocks(sequence_length, arms) if cutpoint is None else cofold_counts(sequence_length, cutpoint, arms)
    summary = _gaussian_summary(sequence_length, cutpoint, counts)
    if not counts:
        return None, summary
    jit = _NUMBA_AVAILABLE and use_numba is not False
    bonus = gaussian_bonus_matrix(sequence_length, cutpoint, counts, summary["gaussianStandardDeviation"], jit)
    nonzero, upper, maximum = _dense_counts(bonus)
    summary.update(builder="row-stripes-" + ("numba" if jit else "numpy"), nonzeroBonusCells=nonzero,
                   nonzeroUpperTriangleCells=upper, maximumBonus=maximum)
    return bonus, summary


def bonus_matrix_listing(bonus: np.ndarray | None) -> dict[str, Any]:
    """``bonusEntries`` and ``nucleotideSupport`` as the dense builder lists them."""
    if bonus is None or not bonus.size:
        return {"bonusEntries": [], "nucleotideSupport": []}
    ones, twos = np.nonzero(bonus)
    upper = ones < twos
    ones, twos = ones[upper], twos[upper]
    return {
        "bonusEntries": [
            {"one": int(one + 1), "two": int(two + 1), "value": float(bonus[one, two])}
            for one, two in zip(ones.tolist(), twos.tolist())
        ],
        "nucleotideSupport": np.max(bonus, axis=1).tolist(),
    }


# ---------------------------------------------------------------------------
# Support kernels.
#
# The chimeras of a fold become its bonus matrix B in one of five ways. Each
# sums one contribution per eligible HYB row (repeated coordinates once, with
# their row count as weight w).  The first three zero cells below 1e-6 and apply
# log1p; the Vienna pair kernels saturate their counts instead. The transforms do not
# equalise the kernels: on the same reads their total support differs
# several-fold (block > gaussian > block-normalized), so an alpha chosen for
# one kernel does not carry over to another.
#
#   gaussian          Gaussian arm profiles (sigma = mean arm length / 6, each
#                     normalised to peak 1), symmetric outer product: the
#                     support of IRIS (extract_paris_scores.py).  Default.
#   block             every position of one arm against every position of the
#                     other, w per cell.
#   block-normalized  as block, w / sqrt(len one * len two) per cell.
#   duplex            C_ij, the rows whose two arms hybridise (ViennaRNA
#                     RNAduplex: intermolecular pairs only, as the hybrid
#                     step of COMRADES' comradesMakeConstraints) with the
#                     pair (i, j), saturated instead of log1p:
#                     B_ij = s C_ij / (C_ij + q), q the mean C of the n/3
#                     best-supported pairs, s = SATURATING_SCALE kcal/mol.
#                     The manuscript's B for every analysis except the IRIS
#                     benchmark.  Native hosts compute the duplexes with
#                     ViennaRNA's Python module; other hosts pass the counts
#                     as evidencePairs.
#   comrades          same transform, but C counts the cross-arm pairs of the
#                     joint RNAcofold MFE, including competition from arm hairpins.
# ---------------------------------------------------------------------------

# B = s C / (C + q): s calibrated so that alpha is the ratio of the bonus of the
# folded structure to its free energy (kernel_experiments/config.py BONUS_SCALE,
# Vienna backend); q the mean count of the round(n x SATURATING_TOP) best pairs.
SATURATING_SCALE = 3.6
SATURATING_TOP = 1.0 / 3.0
SUPPORT_TRANSFORMS = {
    "gaussian": "IRIS Gaussian arm blocks; symmetric float64 accumulation; threshold 2^-23; log1p",
    "block": "Full arm blocks, w per cell of arm one x arm two; symmetric; threshold 1e-6; log1p",
    "block-normalized": "Full arm blocks, w / sqrt(len one x len two) per cell; symmetric; threshold 1e-6; log1p",
    **{kernel: f"{model} cross-arm pairs, C = observed pair counts; symmetric; "
               "s C / (C + q), s = 3.6 kcal/mol, q = mean C of the n/3 best-supported pairs"
       for kernel, model in PAIR_MODELS.items()},
}
MAX_EVIDENCE_PAIRS = 5_000_000
_EVIDENCE_WEIGHT = "one per eligible HYB row; overlap_score and collapsed raw-read count are not weights"


def support_kernel(value: Any) -> str:
    """The requested support kernel; None or "" is the default."""
    kernel = DEFAULT_SUPPORT_KERNEL if value is None or value == "" else str(value).strip().lower()
    if kernel not in SUPPORT_KERNELS:
        raise ValueError("Support kernel must be one of " + ", ".join(SUPPORT_KERNELS) + ".")
    return kernel


def _empty_support(kernel: str) -> dict[str, Any]:
    return {
        "source": "none",
        "supportKernel": kernel,
        "inputRecords": 0,
        "uniqueBlocks": 0,
        "nonzeroBonusCells": 0,
        "nonzeroUpperTriangleCells": 0,
        "maximumBonus": 0.0,
        "bonusEntries": [],
        "evidenceWeight": "none",
    }


def _checked_blocks(sequence_length: int, arms: Any) -> Counter:
    compressed = _compress_arms(arms)
    for one_start, one_end, two_start, two_end in compressed:
        if not (1 <= one_start <= one_end <= sequence_length and 1 <= two_start <= two_end <= sequence_length):
            raise ValueError("HYB evidence falls outside the prepared sequence.")
    return compressed


def _block_cell_value(block: tuple[int, int, int, int], weight: float, normalized: bool) -> float:
    one_start, one_end, two_start, two_end = block
    if not normalized:
        return float(weight)
    return float(weight) / math.sqrt((one_end - one_start + 1) * (two_end - two_start + 1))


def _summary(sequence_length: int, kernel: str, compressed: Counter, bonus_cells: int, upper_cells: int,
             maximum: float) -> dict[str, Any]:
    records = sum(compressed.values())
    return {
        "source": "hyb-block-intervals",
        "supportKernel": kernel,
        "transform": SUPPORT_TRANSFORMS[kernel],
        "coordinateSystem": "prepared-sequence, 1-based inclusive input",
        "inputRecords": int(records),
        "uniqueBlocks": len(compressed),
        "meanArmLength": mean_arm_length(compressed),
        "nonzeroBonusCells": int(bonus_cells),
        "nonzeroUpperTriangleCells": int(upper_cells),
        "maximumBonus": float(maximum),
        "evidenceWeight": _EVIDENCE_WEIGHT,
    }


def _finish_support(support: np.ndarray) -> np.ndarray:
    support[support < np.float32(1e-6)] = np.float32(0.0)
    return np.log1p(support, out=support)


def _dense_counts(bonus: np.ndarray) -> tuple[int, int, float]:
    nonzero = int(np.count_nonzero(bonus))
    diagonal = int(np.count_nonzero(np.diagonal(bonus)))
    return nonzero, (nonzero - diagonal) // 2, float(bonus.max()) if bonus.size else 0.0


def _block_rows(sequence_length: int, compressed: Counter, normalized: bool):
    """Yield ``(row, values)`` for every row that a block covers, values the
    float32 support of that row before the threshold and log1p.

    Each row of HYB evidence is the rectangle arm one x arm two plus its
    transpose.  Summing the rectangles cell by cell costs their total area
    (about 10^9 cells for a genome's reads); a sweep over the rows instead
    keeps one difference vector over the columns, changed only where a
    rectangle starts or ends, and takes one cumulative sum per covered row.
    The vector is float64 so that a value and its cancelling end leave no
    residue near the 1e-6 threshold, and it is reset whenever no rectangle is
    open.
    """
    n = sequence_length
    blocks = np.array(list(compressed.keys()), dtype=np.int64).reshape(-1, 4) - 1  # 0-based inclusive
    weights = np.array(list(compressed.values()), dtype=np.float64)
    if normalized:
        weights = weights / np.sqrt((blocks[:, 1] - blocks[:, 0] + 1.0) * (blocks[:, 3] - blocks[:, 2] + 1.0))
    one_start, one_end, two_start, two_end = blocks.T
    # the rectangle (rows arm one, columns arm two) and its transpose
    row_start = np.concatenate([one_start, two_start])
    row_end = np.concatenate([one_end, two_end]) + 1
    column_start = np.concatenate([two_start, one_start])
    column_end = np.concatenate([two_end, one_end]) + 1
    value = np.concatenate([weights, weights])
    event_row = np.concatenate([row_start, row_start, row_end, row_end])
    event_column = np.concatenate([column_start, column_end, column_start, column_end])
    event_value = np.concatenate([value, -value, -value, value])
    event_open = np.concatenate([np.ones(row_start.size), np.zeros(row_start.size),
                                 -np.ones(row_end.size), np.zeros(row_end.size)]).astype(np.int64)
    order = np.argsort(event_row, kind="stable")
    event_row, event_column = event_row[order], event_column[order]
    event_value, event_open = event_value[order], event_open[order]
    bounds = np.searchsorted(event_row, np.arange(n + 1))
    difference = np.zeros(n + 1, dtype=np.float64)
    open_rectangles = 0
    for row in range(n):
        first, last = bounds[row], bounds[row + 1]
        if first < last:
            np.add.at(difference, event_column[first:last], event_value[first:last])
            open_rectangles += int(event_open[first:last].sum())
            if open_rectangles == 0:
                difference[:] = 0.0
        if open_rectangles:
            yield row, np.cumsum(difference[:n]).astype(np.float32)


def build_block_bonus_matrix(
    sequence_length: int, arms: Any, *, normalized: bool = False
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """Full arm blocks: each row adds its value to every cell of arm one x
    arm two and of its transpose, so a cell inside both arms (overlapping
    arms) gets it twice, as the Gaussian outer product does."""
    kernel = "block-normalized" if normalized else "block"
    compressed = _checked_blocks(sequence_length, arms)
    if not compressed:
        return None, _empty_support(kernel)
    support = np.zeros((sequence_length, sequence_length), dtype=np.float32)
    for row, values in _block_rows(sequence_length, compressed, normalized):
        support[row] = values
    bonus = _finish_support(support)
    return bonus, _summary(sequence_length, kernel, compressed, *_dense_counts(bonus))


def _block_bonus_export(sequence_length: int, arms: Any, normalized: bool) -> dict[str, Any]:
    """Sparse upper-triangle export of ``build_block_bonus_matrix``, one row
    at a time, so no sequence_length x sequence_length array is allocated."""
    kernel = "block-normalized" if normalized else "block"
    compressed = _checked_blocks(sequence_length, arms)
    if not compressed:
        return _empty_support(kernel)
    entries: list[dict[str, Any]] = []
    diagonal_cells = 0
    maximum = 0.0
    threshold = np.float32(1e-6)
    for row, values in _block_rows(sequence_length, compressed, normalized):
        if values[row] >= threshold:
            diagonal_cells += 1
            maximum = max(maximum, float(np.log1p(values[row])))
        upper = values[row + 1:]
        columns = np.flatnonzero(upper >= threshold)
        if columns.size:
            logged = np.log1p(upper[columns])
            maximum = max(maximum, float(logged.max()))
            entries.extend({"one": row + 1, "two": row + 2 + column, "value": value}
                           for column, value in zip(columns.tolist(), logged.tolist()))
    summary = _summary(sequence_length, kernel, compressed, diagonal_cells + 2 * len(entries), len(entries), maximum)
    summary["bonusEntries"] = entries
    return summary


DUPLEX_CHUNK = 1000
# below this many blocks the duplexes are computed in this process
DUPLEX_PARALLEL_MIN = 4000
# in one process, progress is reported every this many blocks
DUPLEX_PROGRESS_STEP = 100


def _duplex_offsets(one: str, two: str):
    """RNAduplex returns a local interaction; offsets are relative to each full arm."""
    import RNA

    hit = RNA.duplexfold(one, two)
    left, _, right = hit.structure.partition("&")
    opens = [k for k, ch in enumerate(left) if ch == "("]
    closes = [k for k, ch in enumerate(right) if ch == ")"]
    if len(opens) != len(closes) or not math.isfinite(hit.energy):
        raise ValueError("ViennaRNA RNAduplex returned an invalid duplex.")
    for i, j in zip(opens, reversed(closes)):
        yield hit.i - len(left) + i, hit.j - 1 + j


def _cofold_offsets(one: str, two: str):
    """Both arms may fold internally; report only pairs across the cut, once."""
    import RNA

    md = RNA.md()
    md.temperature = 37.0
    md.min_loop_size = 3
    fc = RNA.fold_compound(one + "&" + two, md, RNA.OPTION_MFE | RNA.OPTION_HYBRID)
    structure, energy = fc.mfe_dimer()
    compact = structure.replace("&", "")
    if len(compact) != len(one) + len(two) or not math.isfinite(energy):
        raise ValueError("ViennaRNA RNAcofold returned an invalid two-strand structure.")
    partner = RNA.ptable(compact)
    for i in range(1, len(one) + 1):
        j = partner[i]
        if j > len(one):
            yield i - 1, j - len(one) - 1


def _pair_chunk(sequence: str, blocks: list, kernel: str) -> tuple[Counter, int]:
    """Map arm offsets to ordered reference coordinates, weighted by row multiplicity."""
    offsets = {"RNAcofold": _cofold_offsets, "RNAduplex": _duplex_offsets}[PAIR_MODELS[kernel]]
    counts: Counter = Counter()
    with_pairs = 0
    for (one_start, one_end, two_start, two_end), weight in blocks:
        found = 0
        for a, b in offsets(sequence[one_start - 1:one_end], sequence[two_start - 1:two_end]):
            i, j = one_start + a, two_start + b
            if i != j:
                counts[(min(i, j), max(i, j))] += weight
                found += 1
        if found:
            with_pairs += weight
    return counts, with_pairs


def _duplex_workers(workers: int | None) -> int:
    """Parallel duplex processes: ``workers``, else HYB2_DUPLEX_WORKERS, else
    one per CPU.  One where processes cannot be forked (Windows, the browser)."""
    if sys.platform in ("win32", "emscripten"):
        return 1
    try:
        import multiprocessing  # noqa: PLC0415

        if "fork" not in multiprocessing.get_all_start_methods():
            return 1
    except ImportError:
        return 1
    if workers is None:
        try:
            workers = int(os.environ.get("HYB2_DUPLEX_WORKERS") or 0) or (os.cpu_count() or 1)
        except ValueError as error:
            raise ValueError("HYB2_DUPLEX_WORKERS must be a whole number.") from error
    return max(1, int(workers))


def _pairs_forked(sequence: str, blocks: list, kernel: str, workers: int, progress: Any) -> tuple[Counter, int]:
    """``_pair_chunk`` over forked processes. Forking passes the sequence and
    the blocks without pickling a function (this module is loaded from its
    file, so a child could not import it by name); each child sends back the
    counts of its chunks."""
    import multiprocessing  # noqa: PLC0415
    from multiprocessing.connection import wait

    context = multiprocessing.get_context("fork")
    chunks = [blocks[start:start + DUPLEX_CHUNK] for start in range(0, len(blocks), DUPLEX_CHUNK)]

    def run(sender, share: list) -> None:
        try:
            for chunk in share:
                sender.send((len(chunk), _pair_chunk(sequence, chunk, kernel)))
        except BaseException as error:  # noqa: BLE001
            sender.send((0, f"{type(error).__name__}: {error}"))

    readers = {}
    counts: Counter = Counter()
    with_pairs = done = 0
    failure = f"ViennaRNA {PAIR_MODELS[kernel]} failed in a worker process"
    try:
        for index in range(workers):
            # Only this child inherits the write end. Its death produces EOF,
            # even halfway through a message while other workers are running.
            reader, sender = context.Pipe(duplex=False)
            process = context.Process(target=run, args=(sender, chunks[index::workers]), daemon=True)
            try:
                process.start()
            except BaseException:
                reader.close()
                raise
            finally:
                sender.close()
            readers[reader] = process
        while readers:
            for reader in wait(readers):
                try:
                    size, value = reader.recv()
                except EOFError:
                    process = readers.pop(reader)
                    reader.close()
                    process.join()
                    if process.exitcode:
                        raise ValueError(f"{failure}: exit code {process.exitcode}") from None
                    continue
                except OSError as error:
                    raise ValueError(f"{failure}: incomplete result") from error
                if not size:
                    raise ValueError(f"{failure}: {value}")
                partial, rows = value
                counts.update(partial)
                with_pairs += rows
                done += size
                if progress is not None:
                    progress(done, len(blocks))
        if done != len(blocks):
            raise ValueError(f"{failure}: incomplete result")
    finally:
        for reader, process in readers.items():
            reader.close()
            if process.is_alive():
                process.terminate()
            process.join()
    return counts, with_pairs


def arm_pair_counts(sequence: str, arms: Any, *, kernel: str, progress: Any = None,
                    workers: int | None = None) -> tuple[Counter, dict[str, Any]]:
    """Raw C_ij of the chosen Vienna model, before scoring or saturation.

    Returns ``{(i, j): rows}`` (1-based, i < j) and a summary.  Each unique
    block is hybridised once and counted with its row count.  Arms that cannot
    pair add nothing.  From DUPLEX_PARALLEL_MIN blocks on, the blocks are
    shared among ``workers`` forked processes where the platform forks.
    Needs ViennaRNA's Python module; other hosts pass the counts as
    ``evidencePairs``.
    """
    if kernel not in PAIR_MODELS:
        raise ValueError("Arm pair counts require a 'comrades' or 'duplex' support kernel.")
    try:
        import RNA  # noqa: PLC0415
    except ImportError as error:
        raise ValueError(f"The {kernel} support kernel needs ViennaRNA's Python module; "
                         "supply evidencePairs computed by the host instead.") from error
    blocks = sorted(_checked_blocks(len(sequence), arms).items())
    workers = min(_duplex_workers(workers), max(1, -(-len(blocks) // DUPLEX_CHUNK)))
    if workers > 1 and len(blocks) >= DUPLEX_PARALLEL_MIN:
        counts, with_pairs = _pairs_forked(sequence, blocks, kernel, workers, progress)
    else:
        workers = 1
        counts, with_pairs = Counter(), 0
        for start in range(0, len(blocks), DUPLEX_PROGRESS_STEP):
            partial, rows = _pair_chunk(sequence, blocks[start:start + DUPLEX_PROGRESS_STEP], kernel)
            counts.update(partial)
            with_pairs += rows
            if progress is not None:
                progress(min(start + DUPLEX_PROGRESS_STEP, len(blocks)), len(blocks))
    return counts, {"hybridisedBlocks": len(blocks), "duplexesWithPairs": int(with_pairs),
                    "duplexWorkers": workers,
                    "pairCounter": f"ViennaRNA {getattr(RNA, '__version__', '?')} {PAIR_MODELS[kernel]}, 37 C"}


def duplex_pair_counts(sequence: str, arms: Any, *, progress: Any = None,
                       workers: int | None = None) -> tuple[Counter, dict[str, Any]]:
    """Public RNAduplex counter; unlike RNAcofold, arms cannot fold internally."""
    return arm_pair_counts(sequence, arms, kernel="duplex", progress=progress, workers=workers)


def saturating_q(sequence_length: int, pair_counts: Counter) -> float:
    """The mean count of the round(n x SATURATING_TOP) best-supported pairs."""
    top = sorted(pair_counts.values(), reverse=True)[:max(1, round(sequence_length * SATURATING_TOP))]
    return float(sum(top)) / len(top) if top else 0.0


def resolve_q(sequence_length: int, pair_counts: Counter, q: Any = None) -> float:
    """``q`` as given, else ``saturating_q`` of this fold.

    q sets the count at which a pair reaches half of SATURATING_SCALE, so it
    fixes what alpha means.  Derived from the fold itself it changes with the
    folded length: the ZIKV genome gives q near 1,233 while a 400 nt window of
    the same reads gives a far smaller one, and the same alpha is then a much
    stronger bonus in the window.  Pass the q of the longer fold to compare
    alphas across lengths, or to fold a region on the genome's scale."""
    if q is None:
        return saturating_q(sequence_length, pair_counts)
    try:
        value = float(q)
    except (TypeError, ValueError) as error:
        raise ValueError("saturatingQ must be a number.") from error
    if not math.isfinite(value) or value <= 0.0:
        raise ValueError("saturatingQ must be a positive, finite number.")
    return value


def _normalise_evidence_pairs(raw: Any, sequence_length: int) -> Counter:
    """``[{one, two, count}, ...]`` or ``[[one, two, count], ...]`` (1-based)
    as ``{(i, j): count}`` with i < j; repeated pairs are summed."""
    if not isinstance(raw, list):
        raise ValueError("evidencePairs must be an array.")
    if len(raw) > MAX_EVIDENCE_PAIRS:
        raise ValueError(f"evidencePairs is limited to {MAX_EVIDENCE_PAIRS:,} pairs.")
    counts: Counter = Counter()
    for index, item in enumerate(raw):
        try:
            values = (item["one"], item["two"], item["count"]) if isinstance(item, dict) else tuple(item)
            if len(values) != 3:
                raise ValueError
            one = _integer(values[0], 1, sequence_length, "Evidence pair position")
            two = _integer(values[1], 1, sequence_length, "Evidence pair position")
            count = _integer(values[2], 1, 2**53, "Evidence pair count")
        except (KeyError, TypeError, ValueError) as error:
            raise ValueError(f"Evidence pair {index + 1} is invalid.") from error
        if one == two:
            raise ValueError(f"Evidence pair {index + 1} pairs a position with itself.")
        counts[(min(one, two), max(one, two))] += count
    return counts


def score_evidence_pairs(counts: Counter, pairs: Any) -> dict[str, Any]:
    """Sum both endpoints as in Lau's bp_score.sh (make_VARNA_scores_2.sh -m 1000000)."""
    matched = [counts.get((min(i, j), max(i, j)), 0) for i, j in pairs]
    return {"comradesPairCounts": matched, "comradesScore": 2 * sum(min(c, 1_000_000) for c in matched)}


def _pair_summary(kernel: str, sequence_length: int, compressed: Counter, pair_counts: Counter, bonus_cells: int,
                  upper_cells: int, maximum: float, q: float) -> dict[str, Any]:
    if compressed:
        summary = _summary(sequence_length, kernel, compressed, bonus_cells, upper_cells, maximum)
    else:
        summary = _empty_support(kernel)
        summary.update({"transform": SUPPORT_TRANSFORMS[kernel], "nonzeroBonusCells": int(bonus_cells),
                        "nonzeroUpperTriangleCells": int(upper_cells), "maximumBonus": float(maximum)})
    summary.update({"supportedPairs": len(pair_counts), "saturatingScale": SATURATING_SCALE, "saturatingQ": q})
    return summary


def _request_duplex_workers(payload: dict[str, Any]) -> int | None:
    """Native hosts' duplex setting: ``duplexWorkers`` (None: one per CPU)."""
    workers = payload.get("duplexWorkers")
    if workers is not None:
        workers = _integer(workers, 1, 1024, "duplex workers")
    return workers


def _request_pairs(payload: dict[str, Any], kernel: str, sequence: str, arms: Any, progress: Any):
    """One raw table for both the bonus and every candidate's structure score."""
    if not arms:
        return Counter(), {}
    if "evidencePairs" in payload:
        return _normalise_evidence_pairs(payload["evidencePairs"], len(sequence)), {"pairCounter": "host-supplied evidencePairs"}
    return arm_pair_counts(sequence, arms, kernel=kernel, progress=progress, workers=_request_duplex_workers(payload))


def _pair_value(count: Any, q: float) -> Any:
    return np.float32(SATURATING_SCALE) * count / (count + np.float32(q))


def build_pair_bonus_matrix(
    sequence_length: int, pair_counts: Counter, arms: Any = None, kernel: str | None = None,
    q: Any = None,
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """With an explicit pair model, each counted pair gets s C / (C + q).

    ``q`` as in ``resolve_q``: None derives it from this fold, a number holds
    it fixed so the same alpha means the same bonus across fold lengths."""
    if kernel not in PAIR_MODELS:
        raise ValueError("Pair bonus matrices require an explicit 'comrades' or 'duplex' support kernel.")
    compressed = _checked_blocks(sequence_length, arms or [])
    scale = resolve_q(sequence_length, pair_counts, q)
    if not pair_counts:
        return None, _pair_summary(kernel, sequence_length, compressed, pair_counts, 0, 0, 0.0, scale)
    bonus = np.zeros((sequence_length, sequence_length), dtype=np.float32)
    ones = np.fromiter((i - 1 for i, _ in pair_counts), dtype=np.int64, count=len(pair_counts))
    twos = np.fromiter((j - 1 for _, j in pair_counts), dtype=np.int64, count=len(pair_counts))
    rows = np.fromiter(pair_counts.values(), dtype=np.float32, count=len(pair_counts))
    values = _pair_value(rows, scale)
    bonus[ones, twos] = values
    bonus[twos, ones] = values
    return bonus, _pair_summary(kernel, sequence_length, compressed, pair_counts, *_dense_counts(bonus), scale)


def _pair_bonus_export(kernel: str, sequence_length: int, pair_counts: Counter, arms: Any, q: Any) -> dict[str, Any]:
    """Sparse upper-triangle export of ``build_pair_bonus_matrix``."""
    compressed = _checked_blocks(sequence_length, arms or [])
    scale = resolve_q(sequence_length, pair_counts, q)
    entries = [{"one": i, "two": j, "value": float(_pair_value(np.float32(rows), scale))}
               for (i, j), rows in sorted(pair_counts.items())]
    summary = _pair_summary(kernel, sequence_length, compressed, pair_counts, 2 * len(entries), len(entries),
                            max((entry["value"] for entry in entries), default=0.0), scale)
    summary["bonusEntries"] = entries
    return summary


def build_support_bonus_matrix(
    sequence_length: int,
    arms: Any,
    kernel: str = DEFAULT_SUPPORT_KERNEL,
    *,
    cutpoint: int | None = None,
    sequence: str | None = None,
    pairs: Counter | None = None,
    progress: Any = None,
    duplex_workers: int | None = None,
    q: Any = None,
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """The bonus matrix of ``arms`` under ``kernel``, without per-cell listing.

    ``arms`` is a list of ``(one_start, one_end, two_start, two_end)`` rows or
    a mapping from a block to its row count.  For two strands (``cutpoint``)
    every arm already lies on one strand, so a full block stays on its
    strands; the Gaussian kernel is clipped at the nick. The Vienna kernels
    use ``pairs`` when given, otherwise they compute them from ``sequence``.
    """
    kernel = support_kernel(kernel)
    if kernel == "gaussian":
        if cutpoint is not None:
            bonus, summary = build_cofold_hyb_bonus_matrix(sequence_length, cutpoint, arms)
        else:
            bonus, summary = build_hyb_bonus_matrix_fast(sequence_length, arms)
    elif kernel in ("block", "block-normalized"):
        bonus, summary = build_block_bonus_matrix(sequence_length, arms, normalized=kernel == "block-normalized")
    else:
        extra: dict[str, Any] = {}
        if pairs is None and not _compress_arms(arms):
            pairs = Counter()
        elif pairs is None:
            if sequence is None:
                raise ValueError(f"The {kernel} support kernel needs the sequence or evidencePairs.")
            pairs, extra = arm_pair_counts(sequence, arms, kernel=kernel, progress=progress, workers=duplex_workers)
        bonus, summary = build_pair_bonus_matrix(sequence_length, pairs, arms, kernel, q)
        summary.update(extra)
    summary["supportKernel"] = kernel
    if cutpoint is not None:
        summary.update({
            "coordinateSystem": "compact A+B, 1-based inclusive input; '&' has no coordinate",
            "cutpoint": cutpoint,
            "strandLengths": [cutpoint, sequence_length - cutpoint],
            "strandBounded": True,
        })
    return bonus, summary


def _open_hyb(path: Any):
    import gzip

    text = str(path)
    return gzip.open(text, "rb") if text.endswith(".gz") else open(text, "rb")


def iter_hyb_rows(paths: Any, genes: Any = None, progress: Any = None, *, intramolecular: bool = False):
    """Stream HYB rows as ``(gene_one, a0, a1, gene_two, b0, b1)``.

    With ``genes`` (names) only rows whose two arms map to those genes
    (columns 4 and 10) are kept; with ``intramolecular`` only rows whose arms
    map to one gene.  Coordinates are the reference positions in columns 7-8
    and 13-14 (1-based inclusive).  ``progress(bytes_done)`` is called about
    every 4 MiB.
    Native file input uses the staged pipeline parser. Browser JSON input
    has already passed through parser.worker.js and does not import it.
    """
    from hyb2analysis import hybfile

    if isinstance(paths, (str, os.PathLike)):
        paths = [paths]
    wanted = None if genes is None else {str(gene).encode("utf-8") for gene in genes}
    base = 0
    reported = 0
    for path in paths:
        done = base
        with _open_hyb(path) as handle:
            # For .gz report compressed bytes so progress matches file sizes.
            compressed = getattr(handle, "fileobj", None)
            for raw in hybfile.lines(handle):
                done = base + compressed.tell() if compressed is not None else done + len(raw)
                if progress is not None and done - reported >= 4 << 20:
                    reported = done
                    progress(done)
                try:
                    record = hybfile.parse(raw)
                except ValueError:
                    continue
                if record is None:
                    continue
                gene_one, gene_two = record.gene1, record.gene2
                if intramolecular and gene_one != gene_two:
                    continue
                if wanted is not None and (gene_one not in wanted or gene_two not in wanted):
                    continue
                one = gene_one.decode("utf-8", "replace")
                two = one if gene_two == gene_one else gene_two.decode("utf-8", "replace")
                yield one, record.start1, record.end1, two, record.start2, record.end2
        base += os.path.getsize(path)
    if progress is not None:
        progress(base)


def iter_hyb_arms(paths: Any, gene_id: str | None = None, progress: Any = None):
    """Stream intramolecular HYB rows as ``(gene, a0, a1, b0, b1)``.

    A row is intramolecular when both arms map to the same gene (columns 4
    and 10); with ``gene_id`` only that gene is kept (see ``iter_hyb_rows``).
    """

    genes = None if gene_id is None else [gene_id]
    for gene, a0, a1, _two, b0, b1 in iter_hyb_rows(paths, genes, progress, intramolecular=True):
        yield gene, a0, a1, b0, b1


def collect_hyb_arms(
    paths: Any,
    gene_id: str | None,
    sequence_length: int,
    windows: list[tuple[int, int]] | None = None,
    progress: Any = None,
) -> dict[str, Any]:
    """Compress the HYB evidence for a genome fold or for windows.

    Without ``windows`` returns ``{"genome": Counter}`` of rows whose arms
    lie within ``1..sequence_length``.  With windows (1-based inclusive
    ``(start, end)``) each window gets the rows whose two arms both lie
    inside it, shifted to window coordinates, under ``"windows"``.
    """

    genome: Counter = Counter()
    per_window: list[Counter] = [Counter() for _ in windows or []]
    skipped = 0
    for _gene, a0, a1, b0, b1 in iter_hyb_arms(paths, gene_id, progress):
        if not (1 <= a0 <= a1 <= sequence_length and 1 <= b0 <= b1 <= sequence_length):
            skipped += 1
            continue
        if windows is None:
            genome[(a0, a1, b0, b1)] += 1
            continue
        for index, (start, end) in enumerate(windows):
            if start <= a0 and a1 <= end and start <= b0 and b1 <= end:
                shift = start - 1
                per_window[index][(a0 - shift, a1 - shift, b0 - shift, b1 - shift)] += 1
    result: dict[str, Any] = {"outOfRangeRows": skipped}
    if windows is None:
        result["genome"] = genome
    else:
        result["windows"] = per_window
    return result


def _pair_type(sequence: str, left: int, right: int) -> str:
    return f"{sequence[left - 1]}–{sequence[right - 1]}"


def _pairs(sequence: str, structure: str, bonus_matrix: np.ndarray | None) -> list[dict[str, Any]]:
    stacks = {opening: [] for opening in BRACKETS}
    closing = {closing: opening for opening, closing in BRACKETS.items()}
    pairs: list[dict[str, Any]] = []

    for offset, character in enumerate(structure):
        position = offset + 1
        if character in stacks:
            stacks[character].append(position)
        elif character in closing:
            opening = closing[character]
            if not stacks[opening]:
                raise ValueError("CPLfold returned unbalanced dot-bracket notation.")
            left = stacks[opening].pop()
            bonus = float(bonus_matrix[left - 1, position - 1]) if bonus_matrix is not None else 0.0
            pairs.append(
                {
                    "left": left,
                    "right": position,
                    "leftBase": sequence[left - 1],
                    "rightBase": sequence[position - 1],
                    "type": _pair_type(sequence, left, position),
                    "layer": LAYER_NAMES[opening],
                    "bracket": opening + character,
                    "evidenceSupport": bonus,
                    "bonusSupport": bonus,
                }
            )

    if any(stacks.values()):
        raise ValueError("CPLfold returned unbalanced dot-bracket notation.")
    pairs.sort(key=lambda pair: (pair["left"], pair["right"]))
    return pairs


def _crossing_pair_count(pairs: list[dict[str, Any]]) -> int:
    crossing: set[tuple[int, int]] = set()
    for first_index, first in enumerate(pairs):
        for second in pairs[first_index + 1 :]:
            if (
                first["left"] < second["left"] < first["right"] < second["right"]
                or second["left"] < first["left"] < second["right"] < first["right"]
            ):
                crossing.add((first["left"], first["right"]))
                crossing.add((second["left"], second["right"]))
    return len(crossing)


def _candidate(
    sequence: str,
    raw: dict[str, Any],
    index: int,
    bonus_matrix: np.ndarray | None,
    cutpoint: int | None = None,
) -> dict[str, Any]:
    structure = str(raw.get("structure") or "")
    phase1 = raw.get("phase1_struct")
    phase2 = raw.get("phase2_struct")
    if cutpoint is not None:
        structure = strip_structure_break(structure, cutpoint, len(sequence))
        phase1 = strip_structure_break(phase1, cutpoint, len(sequence)) if phase1 is not None else None
        phase2 = strip_structure_break(phase2, cutpoint, len(sequence)) if phase2 is not None else None
    if len(structure) != len(sequence):
        raise ValueError("CPLfold returned a structure with the wrong length.")
    pairs = _pairs(sequence, structure, bonus_matrix)
    crossing_pairs = _crossing_pair_count(pairs)
    structure_type = str(raw.get("type") or "phase1")
    candidate = {
        "index": index,
        "dotBracket": structure,
        "type": structure_type,
        "topology": "pseudoknotted" if crossing_pairs or structure_type == "pseudoknot" else "nested",
        "energy": raw.get("energy"),
        "effectiveEnergy": raw.get("effective_energy"),
        "phase1Energy": raw.get("phase1_energy"),
        "score": raw.get("score"),
        "phase1Structure": phase1,
        "phase2Structure": phase2,
        "pairs": pairs,
        "crossingPairs": crossing_pairs,
        "unpaired": len(sequence) - 2 * len(pairs),
    }
    if cutpoint is not None:
        for pair in pairs:
            for side in ("left", "right"):
                position = pair[side]
                pair[side + "Strand"] = "A" if position <= cutpoint else "B"
                pair[side + "StrandPosition"] = position if position <= cutpoint else position - cutpoint
            pair["intermolecular"] = pair["left"] <= cutpoint < pair["right"]
        candidate.update({
            "dotBracketWithBreak": add_strand_break(structure, cutpoint),
            "phase1StructureWithBreak": add_strand_break(phase1, cutpoint) if phase1 is not None else None,
            "phase2StructureWithBreak": add_strand_break(phase2, cutpoint) if phase2 is not None else None,
            "cutpoint": cutpoint,
            "strandLengths": [cutpoint, len(sequence) - cutpoint],
            "intermolecularPairs": sum(pair["intermolecular"] for pair in pairs),
            "energyMetadata": dict(raw.get("energy_metadata", {})),
            "energyBreakdown": dict(raw.get("energy_breakdown", {})),
            "readSupport": raw.get("read_support", 0.0),
            "readBonus": raw.get("read_bonus", 0.0),
        })
        if "phase2_association_cost" in raw:
            candidate["phase2AssociationCost"] = raw["phase2_association_cost"]
    return candidate


def _request_evidence(payload: dict[str, Any], length: int, cutpoint: int | None):
    mode = str(payload.get("evidenceMode", "none"))
    if mode == "none":
        return mode, []
    if mode != "hyb-blocks":
        raise ValueError("CPLfold evidence mode must be 'none' or 'hyb-blocks'.")
    counted = payload.get("evidenceArmCounts")
    if counted is not None:
        if cutpoint is None:
            raise ValueError("evidenceArmCounts is for two-strand CPLcofold requests.")
        arms = _normalise_arm_counts(counted, length, cutpoint)
    else:
        arms = _normalise_arms(payload.get("evidenceArms"), length, cutpoint)
    if not arms:
        raise ValueError("No eligible HYB rows are fully contained in the selected reference region.")
    return mode, arms


def fold_prepared(sequence: str, cutpoint: int | None, bonus: np.ndarray | None, *, pseudoknots: bool,
                  energy_model: str, alpha: float, beta: float, beam_size: int, energy_delta: float,
                  max_phase1: int) -> list[dict[str, Any]]:
    """Fold a prepared Hyb2 task; the web and desktop hosts both call this.

    ``sequence`` is compact (no '&'); ``cutpoint`` separates two strands and
    ``bonus`` uses the same compact coordinates. Hyb2 tasks keep one Phase-2
    structure per Phase-1 candidate. Returns candidate dicts, best first.
    """
    predictions = cplfold.fold(
        sequence if cutpoint is None else add_strand_break(sequence, cutpoint),
        pseudoknots=pseudoknots, energy_model=energy_model, bonus=bonus, alpha=alpha, beta=beta,
        beam_size=beam_size, energy_delta=energy_delta, max_phase1=max_phase1, max_phase2=1)
    return [prediction.as_dict() for prediction in predictions]


def fold(payload: dict[str, Any], progress: Any = None, *, host: str = "web",
         hard_max_length: int = BROWSER_HARD_MAX_SEQUENCE_LENGTH) -> dict[str, Any]:
    """The caller selects the parameter catalog and length ceiling, never the JSON request."""
    if isinstance(hard_max_length, bool) or not isinstance(hard_max_length, int) or hard_max_length < 1:
        raise ValueError("Host CPLfold hard maximum must be a positive integer.")
    label = "Browser CPLfold" if host == "web" else "CPLfold"
    requested = payload.get("maxSequenceLength")
    maximum_sequence_length = _integer(DEFAULT_MAX_SEQUENCE_LENGTH if requested is None else requested,
                                       1, hard_max_length, f"{label} maximum sequence length")
    sequence, cutpoint = _request_sequence_layout(payload)
    if len(sequence) > hard_max_length:
        runtime = "this pure-Python Pyodide build" if host == "web" else "this runtime"
        raise ValueError(f"{label} has a hard safety ceiling of {hard_max_length} nt in {runtime}.")
    if len(sequence) > maximum_sequence_length:
        raise ValueError(f"{label} is limited to {maximum_sequence_length} nt for this capacity-tested request.")

    engine = engine_parameters(host)["cplfold" if cutpoint is None else "cplcofold"]
    ranges, defaults = engine["ranges"], engine["defaults"]
    beam_size = _integer(payload.get("beamSize", defaults["beamSize"]), *ranges["beamSize"], "Beam size")
    max_phase1 = _integer(payload.get("maxPhase1", defaults["maxPhase1"]), *ranges["maxPhase1"], "Phase-1 candidate count")
    energy_delta = _number(payload.get("energyDelta", defaults["energyDelta"]), *ranges["energyDelta"], "Energy delta")
    alpha = _number(payload.get("alpha", defaults["alpha"]), *ranges["alpha"], "Evidence alpha")
    beta = _number(payload.get("beta", defaults["beta"]), *ranges["beta"], "Pseudoknot beta")
    allow_pseudoknot = task_allow_pseudoknot(payload, default=defaults["allowPseudoknot"])
    energy_model = str(payload.get("energyModel", engine["defaultModel"])).upper()
    models = [option["value"] for option in engine["models"]]
    if energy_model not in models:
        if cutpoint is not None:
            raise ValueError("CPLcofold supports only " + " or ".join(sorted(models)) + ".")
        raise ValueError("Energy model must be one of " + ", ".join(models) + ".")

    evidence_mode, arms = _request_evidence(payload, len(sequence), cutpoint)
    kernel = support_kernel(payload.get("supportKernel"))
    counts = None
    if kernel != "gaussian":
        count_summary = {}
        if kernel in PAIR_MODELS and arms:
            counts, count_summary = _request_pairs(payload, kernel, sequence, arms, progress)
        bonus_matrix, evidence = build_support_bonus_matrix(
            len(sequence), arms, kernel, cutpoint=cutpoint, pairs=counts, q=payload.get("saturatingQ"))
        evidence.update(count_summary)
        evidence.update(bonus_matrix_listing(bonus_matrix))
    elif cutpoint is not None:
        bonus_matrix, evidence = build_cofold_hyb_bonus_matrix(len(sequence), cutpoint, arms)
    else:
        bonus_matrix, evidence = build_hyb_bonus_matrix(len(sequence), arms)
    evidence["supportKernel"] = kernel

    raw_candidates = fold_prepared(
        sequence, cutpoint, bonus_matrix, pseudoknots=allow_pseudoknot, energy_model=energy_model,
        alpha=alpha, beta=beta, beam_size=beam_size, energy_delta=energy_delta, max_phase1=max_phase1)
    candidates = [
        _candidate(sequence, raw, index, bonus_matrix, cutpoint)
        for index, raw in enumerate(raw_candidates)
    ]
    if not candidates:
        raise ValueError("CPLfold did not produce a structure for this sequence and parameter set.")

    if counts is not None:
        for candidate in candidates:
            selected = ((p["left"], p["right"]) for p in candidate["pairs"])
            candidate.update(score_evidence_pairs(counts, selected))
    best = candidates[0]
    nucleotide_support = evidence.pop("nucleotideSupport", [0.0] * len(sequence))
    result = {
        "algorithm": "CPLfold two-phase pseudoknot prediction" if allow_pseudoknot else "CPLfold pseudoknot-free secondary-structure prediction",
        "model": (f"LinearFold Vienna-mode scoring with CPLfold/HotKnots {energy_model} energy ranking" if allow_pseudoknot
                  else "LinearFold Vienna-mode scoring with Turner 2004 energy ranking"),
        "engine": "CPLfold",
        "engineVersion": CPLFOLD_SOURCE_REVISION,
        "bridgeVersion": BRIDGE_VERSION,
        "allowPseudoknot": allow_pseudoknot,
        "runtime": "Pyodide 0.29.4; Python 3.13.2; NumPy 2.2.5; Numba identity fallback",
        "maxSequenceLength": maximum_sequence_length,
        "sequence": sequence,
        "dotBracket": best["dotBracket"],
        "pairs": best["pairs"],
        "unpaired": best["unpaired"],
        "energy": best["energy"],
        "effectiveEnergy": best["effectiveEnergy"],
        "phase1Energy": best["phase1Energy"],
        "score": best["score"],
        "phase1Structure": best["phase1Structure"],
        "phase2Structure": best["phase2Structure"],
        "energyUnit": "kcal/mol",
        "structureType": best["type"],
        "topology": best["topology"],
        "crossingPairs": best["crossingPairs"],
        # The HYB matrix is a soft score bonus, not an enforced pair constraint.
        "constraintMode": "none",
        "constraintSource": "none",
        "constraintCount": 0,
        "constraints": [],
        "evidenceMode": evidence_mode,
        "evidenceSource": "hyb-block-bonus-matrix" if arms else "none",
        "selectedCandidate": 0,
        "candidates": candidates,
        "parameters": {
            "beamSize": beam_size,
            "energyDelta": energy_delta,
            "maxPhase1": max_phase1,
            "energyModel": energy_model,
            "alpha": alpha,
            "beta": beta,
            "allowPseudoknot": allow_pseudoknot,
            "supportKernel": kernel,
            "linearFoldMode": "Vienna",
            "maxPhase2": 1,
        },
        "evidence": evidence,
        "nucleotideSupport": nucleotide_support,
        "maximumNucleotideSupport": max(nucleotide_support, default=0.0),
    }
    if counts is not None:
        result.update(comradesPairCounts=best["comradesPairCounts"], comradesScore=best["comradesScore"])
    if cutpoint is not None:
        result.update({
            "algorithm": "CPLcofold two-strand two-phase pseudoknot prediction" if allow_pseudoknot else "CPLcofold pseudoknot-free two-strand prediction",
            "model": f"Nick-aware Turner cofold scoring; experimental Turner/{energy_model} strand-graph pseudoknot ranking",
            "engine": "CPLcofold",
            "displaySequence": add_strand_break(sequence, cutpoint),
        })
        for key in ("dotBracketWithBreak", "phase1StructureWithBreak", "phase2StructureWithBreak",
                    "cutpoint", "strandLengths", "intermolecularPairs", "energyMetadata", "energyBreakdown",
                    "readSupport", "readBonus", "phase2AssociationCost"):
            if key in best:
                result[key] = best[key]
    return result


def bonus_matrix(payload: dict[str, Any], progress: Any = None) -> dict[str, Any]:
    """Prepare the exact sparse bonus matrix used by a guided local run."""

    sequence, cutpoint = _request_sequence_layout(payload)
    if len(sequence) > BONUS_EXPORT_MAX_SEQUENCE_LENGTH:
        raise ValueError(f"Bonus-matrix export is limited to {BONUS_EXPORT_MAX_SEQUENCE_LENGTH:,} nt in the browser.")
    evidence_mode, arms = _request_evidence(payload, len(sequence), cutpoint)
    kernel = support_kernel(payload.get("supportKernel"))
    if kernel == "gaussian":
        evidence = build_hyb_bonus_matrix_export(len(sequence), arms, cutpoint)
    elif kernel in ("block", "block-normalized"):
        evidence = _block_bonus_export(len(sequence), arms, kernel == "block-normalized")
    else:
        pairs, extra = _request_pairs(payload, kernel, sequence, arms, progress)
        evidence = _pair_bonus_export(kernel, len(sequence), pairs, arms, payload.get("saturatingQ"))
        evidence.update(extra)
    evidence["supportKernel"] = kernel
    if kernel != "gaussian" and cutpoint is not None:
        evidence.update({
            "coordinateSystem": "compact A+B, 1-based inclusive input; '&' has no coordinate",
            "cutpoint": cutpoint,
            "strandLengths": [cutpoint, len(sequence) - cutpoint],
            "strandBounded": True,
        })
    result = {
        "engine": "CPLfold",
        "engineVersion": CPLFOLD_SOURCE_REVISION,
        "bridgeVersion": BRIDGE_VERSION,
        "sequence": sequence,
        "evidenceMode": evidence_mode,
        "supportKernel": kernel,
        "evidence": evidence,
    }
    if cutpoint is not None:
        result.update({
            "engine": "CPLcofold",
            "displaySequence": add_strand_break(sequence, cutpoint),
            "cutpoint": cutpoint,
            "strandLengths": [cutpoint, len(sequence) - cutpoint],
        })
    return result


# ``progress(blocks_done, blocks)`` reports the duplex kernel's pair counting.
def fold_json(payload_json: str, progress: Any = None) -> str:
    payload = json.loads(payload_json)
    if not isinstance(payload, dict):
        raise ValueError("The CPLfold request must be a JSON object.")
    return json.dumps(fold(payload, progress), separators=(",", ":"), allow_nan=False)


def bonus_matrix_json(payload_json: str, progress: Any = None) -> str:
    payload = json.loads(payload_json)
    if not isinstance(payload, dict):
        raise ValueError("The CPLfold bonus-matrix request must be a JSON object.")
    return json.dumps(bonus_matrix(payload, progress), separators=(",", ":"), allow_nan=False)


__all__ = [
    "SUPPORT_KERNELS",
    "build_block_bonus_matrix",
    "build_hyb_bonus_matrix",
    "build_hyb_bonus_matrix_export",
    "build_hyb_bonus_matrix_fast",
    "build_pair_bonus_matrix",
    "build_support_bonus_matrix",
    "arm_pair_counts",
    "duplex_pair_counts",
    "score_evidence_pairs",
    "support_kernel",
    "collect_hyb_arms",
    "iter_hyb_arms",
    "iter_hyb_rows",
    "bonus_matrix",
    "bonus_matrix_json",
    "fold",
    "fold_json",
]
