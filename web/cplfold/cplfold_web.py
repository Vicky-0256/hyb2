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
import re
import sys
import types
from collections import Counter
from typing import Any


CPLFOLD_SOURCE_REVISION = "24bab52"
BRIDGE_VERSION = "6"
DEFAULT_MAX_SEQUENCE_LENGTH = 75
BROWSER_HARD_MAX_SEQUENCE_LENGTH = 500
# Native hosts (HYB2 Desktop) may raise the ceiling via
# set_host_hard_max_sequence_length(); the browser never calls it, so the
# Pyodide build keeps the 500 nt bound.
_hard_max_sequence_length = BROWSER_HARD_MAX_SEQUENCE_LENGTH
BONUS_EXPORT_MAX_SEQUENCE_LENGTH = 5_000
MAX_EVIDENCE_RECORDS = 50_000
ENERGY_MODELS = {"DP03", "DP09", "CC06", "CC09", "RE"}
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
from CPLfold import two_phase_pseudoknot_fold  # noqa: E402


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


def _sequence(value: Any, maximum: int = DEFAULT_MAX_SEQUENCE_LENGTH) -> str:
    sequence = re.sub(r"\s+", "", str(value or "")).upper().replace("T", "U")
    if not sequence:
        raise ValueError("CPLfold requires an RNA sequence.")
    if re.search(r"[^ACGU]", sequence):
        raise ValueError("CPLfold accepts only A, C, G and U.")
    if len(sequence) > _hard_max_sequence_length:
        if _hard_max_sequence_length == BROWSER_HARD_MAX_SEQUENCE_LENGTH:
            raise ValueError(
                f"Browser CPLfold has a hard safety ceiling of {BROWSER_HARD_MAX_SEQUENCE_LENGTH} nt in this pure-Python Pyodide build."
            )
        raise ValueError(f"CPLfold has a hard safety ceiling of {_hard_max_sequence_length} nt in this runtime.")
    if len(sequence) > maximum:
        raise ValueError(
            f"Browser CPLfold is limited to {maximum} nt for this capacity-tested request."
        )
    return sequence


def _bonus_export_sequence(value: Any) -> str:
    sequence = re.sub(r"\s+", "", str(value or "")).upper().replace("T", "U")
    if not sequence:
        raise ValueError("CPLfold requires an RNA sequence.")
    if re.search(r"[^ACGU]", sequence):
        raise ValueError("CPLfold accepts only A, C, G and U.")
    if len(sequence) > BONUS_EXPORT_MAX_SEQUENCE_LENGTH:
        raise ValueError(
            f"Bonus-matrix export is limited to {BONUS_EXPORT_MAX_SEQUENCE_LENGTH:,} nt in the browser."
        )
    return sequence


def _request_sequence_layout(
    payload: dict[str, Any], maximum: int = DEFAULT_MAX_SEQUENCE_LENGTH, *, export: bool = False
) -> tuple[str, int | None]:
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
    validate = _bonus_export_sequence if export else lambda value: _sequence(value, maximum)
    if not is_cofold:
        return validate(raw), None

    from Utils.strand_layout import normalise_sequence

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
    return validate(sequence), cutpoint


def set_host_hard_max_sequence_length(value: int) -> None:
    """Raise the hard ceiling for a native host; not used by the browser."""
    global _hard_max_sequence_length
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise ValueError("Host CPLfold hard maximum must be a positive integer.")
    _hard_max_sequence_length = value


def _request_maximum(value: Any) -> int:
    return _integer(
        DEFAULT_MAX_SEQUENCE_LENGTH if value is None else value,
        1,
        _hard_max_sequence_length,
        "Browser CPLfold maximum sequence length",
    )


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
    sequence_length: int, arms: list[tuple[int, int, int, int]]
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """Reproduce CPLfold's IRIS-style HYB/PARIS block-to-bonus transform.

    Coordinates are 1-based inclusive in the web data model.  They are
    converted to zero-based half-open block centres before Gaussian spreading,
    matching ``extract_paris_scores.py``.  Every eligible HYB row contributes
    once; duplicate coordinate blocks are compressed with an integer weight.
    """

    if not arms:
        return None, {
            "source": "none",
            "inputRecords": 0,
            "uniqueBlocks": 0,
            "nonzeroBonusCells": 0,
            "nonzeroUpperTriangleCells": 0,
            "maximumBonus": 0.0,
            "bonusEntries": [],
            "evidenceWeight": "none",
        }

    arm_lengths = [end - start + 1 for arm in arms for start, end in ((arm[0], arm[1]), (arm[2], arm[3]))]
    standard_deviation = max(float(np.mean(arm_lengths)) / 6.0, 1e-6)
    positions = np.arange(sequence_length, dtype=np.float32)
    support = np.zeros((sequence_length, sequence_length), dtype=np.float32)
    compressed = Counter(arms)

    for (one_start, one_end, two_start, two_end), weight in compressed.items():
        one_mean = ((one_start - 1) + one_end) / 2.0
        two_mean = ((two_start - 1) + two_end) / 2.0
        one_support = np.exp(-0.5 * ((positions - one_mean) / standard_deviation) ** 2)
        two_support = np.exp(-0.5 * ((positions - two_mean) / standard_deviation) ** 2)
        one_support /= np.max(one_support)
        two_support /= np.max(two_support)
        contribution = np.outer(one_support, two_support)
        support += np.float32(weight) * (contribution + contribution.T)

    support[support < np.float32(1e-6)] = np.float32(0.0)
    bonus = np.log1p(support).astype(np.float32, copy=False)
    maximum = float(np.max(bonus)) if bonus.size else 0.0
    nucleotide_support = np.max(bonus, axis=1).tolist() if bonus.size else []
    bonus_entries = [
        {"one": int(one + 1), "two": int(two + 1), "value": float(bonus[one, two])}
        for one, two in zip(*np.nonzero(np.triu(bonus, k=1)))
    ]
    return bonus, {
        "source": "hyb-block-intervals",
        "transform": "IRIS-style Gaussian arm blocks; symmetric outer product; threshold 1e-6; log1p",
        "coordinateSystem": "prepared-sequence, 1-based inclusive input",
        "inputRecords": len(arms),
        "uniqueBlocks": len(compressed),
        "meanArmLength": float(np.mean(arm_lengths)),
        "gaussianStandardDeviation": standard_deviation,
        "nonzeroBonusCells": int(np.count_nonzero(bonus)),
        "nonzeroUpperTriangleCells": len(bonus_entries),
        "maximumBonus": maximum,
        "bonusEntries": bonus_entries,
        "nucleotideSupport": nucleotide_support,
        "evidenceWeight": "one per eligible HYB row; overlap_score and collapsed raw-read count are not weights",
    }


def build_hyb_bonus_matrix_export(
    sequence_length: int, arms: list[tuple[int, int, int, int]]
) -> dict[str, Any]:
    """Build the sparse TSV representation without allocating an n-by-n array.

    Folding still uses ``build_hyb_bonus_matrix`` because the parser needs a
    dense matrix.  Downloads only need non-zero upper-triangle cells, so this
    path keeps the same float32 arithmetic while avoiding a second quadratic
    allocation and permits local handoff for sequences above the browser fold
    ceiling.
    """

    if not arms:
        return {
            "source": "none",
            "inputRecords": 0,
            "uniqueBlocks": 0,
            "nonzeroBonusCells": 0,
            "nonzeroUpperTriangleCells": 0,
            "maximumBonus": 0.0,
            "bonusEntries": [],
            "evidenceWeight": "none",
        }

    arm_lengths = [
        end - start + 1
        for arm in arms
        for start, end in ((arm[0], arm[1]), (arm[2], arm[3]))
    ]
    standard_deviation = max(float(np.mean(arm_lengths)) / 6.0, 1e-6)
    positions = np.arange(sequence_length, dtype=np.float32)
    compressed = Counter(arms)
    upper_support: dict[tuple[int, int], np.float32] = {}
    diagonal_support = np.zeros(sequence_length, dtype=np.float32)

    for (one_start, one_end, two_start, two_end), weight in compressed.items():
        one_mean = ((one_start - 1) + one_end) / 2.0
        two_mean = ((two_start - 1) + two_end) / 2.0
        one_support = np.exp(-0.5 * ((positions - one_mean) / standard_deviation) ** 2)
        two_support = np.exp(-0.5 * ((positions - two_mean) / standard_deviation) ** 2)
        one_support /= np.max(one_support)
        two_support /= np.max(two_support)
        active = np.flatnonzero((one_support > 0) | (two_support > 0))
        weight32 = np.float32(weight)

        for position in active:
            diagonal_contribution = weight32 * np.float32(
                np.float32(one_support[position] * two_support[position]) * np.float32(2.0)
            )
            diagonal_support[position] = np.float32(
                diagonal_support[position] + diagonal_contribution
            )

        for offset, left in enumerate(active[:-1]):
            for right in active[offset + 1:]:
                contribution = weight32 * np.float32(
                    np.float32(one_support[left] * two_support[right])
                    + np.float32(one_support[right] * two_support[left])
                )
                if contribution == 0:
                    continue
                key = (int(left), int(right))
                upper_support[key] = np.float32(
                    upper_support.get(key, np.float32(0.0)) + contribution
                )

    threshold = np.float32(1e-6)
    diagonal_bonus = np.log1p(np.where(diagonal_support >= threshold, diagonal_support, 0.0)).astype(
        np.float32, copy=False
    )
    entries = []
    for (left, right), support in sorted(upper_support.items()):
        if support < threshold:
            continue
        entries.append({
            "one": left + 1,
            "two": right + 1,
            "value": float(np.log1p(np.float32(support))),
        })

    maximum = float(np.max(diagonal_bonus)) if diagonal_bonus.size else 0.0
    if entries:
        maximum = max(maximum, max(float(entry["value"]) for entry in entries))
    nonzero_diagonal = int(np.count_nonzero(diagonal_bonus))
    return {
        "source": "hyb-block-intervals",
        "transform": "IRIS-style Gaussian arm blocks; symmetric outer product; threshold 1e-6; log1p",
        "coordinateSystem": "prepared-sequence, 1-based inclusive input",
        "inputRecords": len(arms),
        "uniqueBlocks": len(compressed),
        "meanArmLength": float(np.mean(arm_lengths)),
        "gaussianStandardDeviation": standard_deviation,
        "nonzeroBonusCells": nonzero_diagonal + 2 * len(entries),
        "nonzeroUpperTriangleCells": len(entries),
        "maximumBonus": maximum,
        "bonusEntries": entries,
        "evidenceWeight": "one per eligible HYB row; overlap_score and collapsed raw-read count are not weights",
    }


def _cofold_evidence_summary(
    sequence_length: int, cutpoint: int, arms: Any
) -> dict[str, Any]:
    """``arms`` is a list of (one_start, one_end, two_start, two_end) rows or
    a mapping from such a block to its row count."""
    summary = build_hyb_bonus_matrix_export(sequence_length, [])
    summary.update({
        "coordinateSystem": "compact A+B, 1-based inclusive input; '&' has no coordinate",
        "cutpoint": cutpoint,
        "strandLengths": [cutpoint, sequence_length - cutpoint],
        "strandBounded": True,
    })
    counts = _compress_arms(arms)
    if counts:
        rows = sum(counts.values())
        mean_length = sum(w * (b - a + 1 + d - c + 1) for (a, b, c, d), w in counts.items()) / (2.0 * rows)
        summary.update({
            "source": "hyb-block-intervals",
            "transform": "Strand-bounded IRIS-style Gaussian arm blocks; symmetric outer product; threshold 1e-6; log1p",
            "inputRecords": rows,
            "uniqueBlocks": len(counts),
            "meanArmLength": mean_length,
            "gaussianStandardDeviation": mean_length / 6.0,
            "evidenceWeight": "one per eligible HYB row; overlap_score and collapsed raw-read count are not weights",
        })
    return summary


def build_cofold_hyb_bonus_matrix(
    sequence_length: int, cutpoint: int, arms: Any
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """Use the shared native/browser cofold transform, clipping at the nick.

    ``arms`` is a list of rows, or (native hosts with many chimeras) a
    mapping from a block to its row count: the same matrix without one
    entry per row."""
    from Utils.cofold_bonus import build_cofold_bonus_matrix

    summary = _cofold_evidence_summary(sequence_length, cutpoint, arms)
    if not arms:
        return None, summary
    if isinstance(arms, dict):
        bonus = _weighted_cofold_bonus(sequence_length, cutpoint, arms)
    else:
        bonus = build_cofold_bonus_matrix(sequence_length, cutpoint, arms)
    summary.update(bonus_matrix_listing(bonus))
    summary.update({
        "nonzeroBonusCells": int(np.count_nonzero(bonus)),
        "nonzeroUpperTriangleCells": len(summary["bonusEntries"]),
        "maximumBonus": float(np.max(bonus)),
    })
    return bonus, summary


def _weighted_cofold_bonus(sequence_length: int, cutpoint: int, counts: dict) -> np.ndarray:
    """Utils.cofold_bonus.build_cofold_bonus_matrix for blocks with row counts
    (it compresses repeated rows the same way)."""
    from Utils.cofold_bonus import _accumulate_support

    blocks = list(counts)
    weights = [float(counts[block]) for block in blocks]
    rows = sum(weights)
    sigma = sum(w * (b - a + 1 + d - c + 1) for (a, b, c, d), w in zip(blocks, weights)) / (12.0 * rows)
    return _accumulate_support(int(sequence_length), int(cutpoint), np.asarray(blocks, dtype=np.int64),
                               np.asarray(weights, dtype=np.float64), sigma).astype(np.float32)


def _cofold_bonus_matrix_export(
    sequence_length: int, cutpoint: int, arms: list[tuple[int, int, int, int]]
) -> dict[str, Any]:
    """Sparse equivalent of Utils.cofold_bonus for validated input arms.

    Accumulate in float64 and cast after log1p, exactly as the cofold builder
    does.  Each vector is normalised within its own strand.  Keeping only
    positive Gaussian entries avoids a dense N-by-N export allocation, without
    truncating small nonzero terms before repeated evidence is accumulated.
    """
    summary = _cofold_evidence_summary(sequence_length, cutpoint, arms)
    if not arms:
        return summary
    sigma = sum(b - a + 1 + d - c + 1 for a, b, c, d in arms) / (12.0 * len(arms))
    upper_support: dict[tuple[int, int], float] = {}
    diagonal = np.zeros(sequence_length, dtype=np.float64)

    def gaussian(start: int, end: int):
        lower, upper = (0, cutpoint) if start <= cutpoint else (cutpoint, sequence_length)
        positions = np.arange(lower, upper, dtype=np.int64)
        values = np.exp(-0.5 * ((positions - ((start - 1) + end) / 2.0) / sigma) ** 2)
        values /= np.max(values)
        return [(int(position), float(value)) for position, value in zip(positions, values) if value > 0.0]

    for (a, b, c, d), weight in Counter(arms).items():
        first, second = gaussian(a, b), gaussian(c, d)
        for i, one in first:
            for j, two in second:
                value = float(weight) * one * two
                if value == 0.0:
                    continue
                if i == j:
                    diagonal[i] += value
                    diagonal[i] += value
                else:
                    key = (min(i, j), max(i, j))
                    upper_support[key] = upper_support.get(key, 0.0) + value

    diagonal_bonus = np.log1p(np.where(diagonal >= 1e-6, diagonal, 0.0)).astype(np.float32)
    entries = [
        {"one": i + 1, "two": j + 1, "value": float(np.float32(np.log1p(value)))}
        for (i, j), value in sorted(upper_support.items()) if value >= 1e-6
    ]
    maximum = max(float(np.max(diagonal_bonus)), max((entry["value"] for entry in entries), default=0.0))
    summary.update({
        "nonzeroBonusCells": int(np.count_nonzero(diagonal_bonus)) + 2 * len(entries),
        "nonzeroUpperTriangleCells": len(entries),
        "maximumBonus": maximum,
        "bonusEntries": entries,
    })
    return summary


# ---------------------------------------------------------------------------
# Sparse (windowed) bonus matrix for long sequences.
#
# Same transform as ``build_hyb_bonus_matrix`` -- Gaussian arm blocks with
# sigma = mean arm length / 6, each arm normalised to peak 1, symmetric outer
# product, cells below 1e-6 zeroed, log1p -- but each block is evaluated only
# where it can matter.  A block of weight w is truncated where one arm factor
# drops below ``tail / w`` (about +-5.7 sigma for w = 1, growing with
# sqrt(log w)), so every dropped cell contribution is below ``tail``.  The
# dense builder materialises one n-by-n outer product per unique block, which
# is quadratic per block; this one is O(window^2) per block.
# ---------------------------------------------------------------------------

DEFAULT_SPARSE_TAIL = 1e-7
# The identity fallback installed above has no __version__.
_NUMBA_AVAILABLE = hasattr(sys.modules.get("numba"), "__version__")

from numba import njit  # noqa: E402  (real Numba, or the identity fallback above)


def _block_half_width(weight: float, standard_deviation: float, tail: float) -> int:
    ratio = max(float(weight), 1.0) / tail
    return int(math.ceil(standard_deviation * math.sqrt(2.0 * math.log(ratio)))) if ratio > 1.0 else 0


# cache=False: this module is loaded under several names (spec_from_file_location),
# and a cached Numba environment would record an unimportable module name.
@njit(cache=False)
def _accumulate_gaussian_blocks(support, blocks, weights, standard_deviation, tail):  # pragma: no cover - JIT
    n = support.shape[0]
    for k in range(blocks.shape[0]):
        one_mean = ((blocks[k, 0] - 1) + blocks[k, 1]) / 2.0
        two_mean = ((blocks[k, 2] - 1) + blocks[k, 3]) / 2.0
        weight = weights[k]
        ratio = max(weight, 1.0) / tail
        half = 0
        if ratio > 1.0:
            half = int(math.ceil(standard_deviation * math.sqrt(2.0 * math.log(ratio))))
        one_centre = int(math.floor(one_mean + 0.5))
        two_centre = int(math.floor(two_mean + 0.5))
        one_lo = max(0, one_centre - half)
        one_hi = min(n - 1, one_centre + half)
        two_lo = max(0, two_centre - half)
        two_hi = min(n - 1, two_centre + half)
        if one_lo > one_hi or two_lo > two_hi:
            continue
        # Peak normalisation over integer positions, as in the dense builder.
        one_peak = 0.0
        for x in range(one_lo, one_hi + 1):
            value = math.exp(-0.5 * ((x - one_mean) / standard_deviation) ** 2)
            if value > one_peak:
                one_peak = value
        two_peak = 0.0
        for y in range(two_lo, two_hi + 1):
            value = math.exp(-0.5 * ((y - two_mean) / standard_deviation) ** 2)
            if value > two_peak:
                two_peak = value
        if one_peak <= 0.0 or two_peak <= 0.0:
            continue
        for x in range(one_lo, one_hi + 1):
            gx = weight * math.exp(-0.5 * ((x - one_mean) / standard_deviation) ** 2) / one_peak
            for y in range(two_lo, two_hi + 1):
                contribution = gx * math.exp(-0.5 * ((y - two_mean) / standard_deviation) ** 2) / two_peak
                support[x, y] += contribution
                support[y, x] += contribution


def _accumulate_gaussian_blocks_numpy(support, blocks, weights, standard_deviation, tail) -> None:
    """Vectorised fallback used when Numba is unavailable (e.g. Pyodide)."""
    n = support.shape[0]
    for (one_start, one_end, two_start, two_end), weight in zip(blocks.tolist(), weights.tolist()):
        half = _block_half_width(weight, standard_deviation, tail)
        means = (((one_start - 1) + one_end) / 2.0, ((two_start - 1) + two_end) / 2.0)
        factors = []
        for mean in means:
            centre = int(math.floor(mean + 0.5))
            lo, hi = max(0, centre - half), min(n - 1, centre + half)
            if lo > hi:
                break
            values = np.exp(-0.5 * ((np.arange(lo, hi + 1, dtype=np.float64) - mean) / standard_deviation) ** 2)
            factors.append((lo, hi, values / values.max()))
        if len(factors) != 2:
            continue
        (one_lo, one_hi, one), (two_lo, two_hi, two) = factors
        contribution = (weight * np.outer(one, two)).astype(np.float32)
        support[one_lo:one_hi + 1, two_lo:two_hi + 1] += contribution
        support[two_lo:two_hi + 1, one_lo:one_hi + 1] += contribution.T


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
    tail: float = DEFAULT_SPARSE_TAIL,
    use_numba: bool | None = None,
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """Long-sequence variant of ``build_hyb_bonus_matrix``.

    ``arms`` is an iterable of ``(one_start, one_end, two_start, two_end)``
    tuples (1-based inclusive, prepared-sequence coordinates) or a mapping /
    ``Counter`` from such a tuple to its row count, e.g. the output of
    ``collect_hyb_arms``.  Returns the dense float32 bonus matrix the parser
    needs (allocated once; log1p is applied in place) and an evidence summary.
    Per-cell bonusEntries are not listed (a genome has millions); use
    ``bonus_matrix_upper_entries`` when needed.
    """

    compressed = _compress_arms(arms)
    if not compressed:
        return None, {
            "source": "none",
            "inputRecords": 0,
            "uniqueBlocks": 0,
            "nonzeroBonusCells": 0,
            "nonzeroUpperTriangleCells": 0,
            "maximumBonus": 0.0,
            "evidenceWeight": "none",
        }
    for one_start, one_end, two_start, two_end in compressed:
        if not (
            1 <= one_start <= one_end <= sequence_length
            and 1 <= two_start <= two_end <= sequence_length
        ):
            raise ValueError("HYB evidence falls outside the prepared sequence.")

    blocks = np.array(list(compressed.keys()), dtype=np.int64).reshape(-1, 4)
    weights = np.array(list(compressed.values()), dtype=np.float64)
    records = float(weights.sum())
    arm_length_total = float(
        np.sum(weights * ((blocks[:, 1] - blocks[:, 0] + 1) + (blocks[:, 3] - blocks[:, 2] + 1)))
    )
    mean_arm_length = arm_length_total / (2.0 * records)
    standard_deviation = max(mean_arm_length / 6.0, 1e-6)

    support = np.zeros((sequence_length, sequence_length), dtype=np.float32)
    jit = _NUMBA_AVAILABLE if use_numba is None else use_numba
    if jit:
        _accumulate_gaussian_blocks(support, blocks, weights, standard_deviation, float(tail))
    else:
        _accumulate_gaussian_blocks_numpy(support, blocks, weights, standard_deviation, float(tail))
    support[support < np.float32(1e-6)] = np.float32(0.0)
    bonus = np.log1p(support, out=support)
    nonzero = int(np.count_nonzero(bonus))
    nonzero_diagonal = int(np.count_nonzero(np.diagonal(bonus)))
    return bonus, {
        "source": "hyb-block-intervals",
        "transform": "IRIS-style Gaussian arm blocks; symmetric outer product; threshold 1e-6; log1p",
        "builder": "sparse-window" + ("-numba" if jit else "-numpy"),
        "truncationTail": float(tail),
        "coordinateSystem": "prepared-sequence, 1-based inclusive input",
        "inputRecords": int(records),
        "uniqueBlocks": len(compressed),
        "meanArmLength": mean_arm_length,
        "gaussianStandardDeviation": standard_deviation,
        "nonzeroBonusCells": nonzero,
        "nonzeroUpperTriangleCells": (nonzero - nonzero_diagonal) // 2,
        "maximumBonus": float(bonus.max()) if bonus.size else 0.0,
        "evidenceWeight": "one per eligible HYB row; overlap_score and collapsed raw-read count are not weights",
    }


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
    """

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
            for raw in handle:
                done = base + compressed.tell() if compressed is not None else done + len(raw)
                if progress is not None and done - reported >= 4 << 20:
                    reported = done
                    progress(done)
                fields = raw.split(b"\t", 14)
                if len(fields) < 14:
                    continue
                # padded RNA names match, as hyb2analysis.hybfile.parse strips them
                gene_one, gene_two = fields[3].strip(), fields[9].strip()
                if intramolecular and gene_one != gene_two:
                    continue
                if wanted is not None and (gene_one not in wanted or gene_two not in wanted):
                    continue
                try:
                    a0, a1, b0, b1 = int(fields[6]), int(fields[7]), int(fields[12]), int(fields[13])
                except ValueError:
                    continue
                one = gene_one.decode("utf-8", "replace")
                two = one if gene_two == gene_one else gene_two.decode("utf-8", "replace")
                yield one, a0, a1, two, b0, b1
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
        from Utils.strand_layout import strip_structure_break

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
        from Utils.strand_layout import add_strand_break

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


def fold(payload: dict[str, Any]) -> dict[str, Any]:
    maximum_sequence_length = _request_maximum(payload.get("maxSequenceLength"))
    sequence, cutpoint = _request_sequence_layout(payload, maximum_sequence_length)
    beam_size = _integer(payload.get("beamSize", 20), 1, 200, "Beam size")
    max_phase1 = _integer(payload.get("maxPhase1", 3), 1, 20, "Phase-1 candidate count")
    energy_delta = _number(payload.get("energyDelta", 5), 0.0, 50.0, "Energy delta")
    alpha = _number(payload.get("alpha", 0.5 if cutpoint is None else 0.0), 0.0, 1.0, "Evidence alpha")
    beta = _number(payload.get("beta", 0.0), 0.0, 1.0, "Pseudoknot beta")
    allow_pseudoknot = payload.get("allowPseudoknot", True)
    if not isinstance(allow_pseudoknot, bool):
        raise ValueError("allowPseudoknot must be a boolean.")
    energy_model = str(payload.get("energyModel", "DP09")).upper()
    if cutpoint is not None and energy_model not in {"DP03", "DP09"}:
        raise ValueError("CPLcofold supports only DP03 or DP09; nicked CC/RE pseudoknot models are not implemented.")
    if energy_model not in ENERGY_MODELS:
        raise ValueError("Energy model must be one of DP03, DP09, CC06, CC09 or RE.")

    evidence_mode = str(payload.get("evidenceMode", "none"))
    if evidence_mode not in {"none", "hyb-blocks"}:
        raise ValueError("CPLfold evidence mode must be 'none' or 'hyb-blocks'.")
    counted = payload.get("evidenceArmCounts")
    if counted is not None and evidence_mode == "hyb-blocks":
        # native hosts: blocks with row counts (CPLcofold only), not one entry per row
        if cutpoint is None:
            raise ValueError("evidenceArmCounts is for two-strand CPLcofold requests.")
        arms = _normalise_arm_counts(counted, len(sequence), cutpoint)
    else:
        arms = _normalise_arms(payload.get("evidenceArms"), len(sequence), cutpoint) if evidence_mode == "hyb-blocks" else []
    if evidence_mode == "hyb-blocks" and not arms:
        raise ValueError("No eligible HYB rows are fully contained in the selected reference region.")
    if cutpoint is not None:
        bonus_matrix, evidence = build_cofold_hyb_bonus_matrix(len(sequence), cutpoint, arms)
    elif arms and _NUMBA_AVAILABLE:
        # Native hosts: same transform without one n-by-n outer product per
        # block (values agree with the dense builder to ~1e-6).
        bonus_matrix, evidence = build_hyb_bonus_matrix_fast(len(sequence), arms)
        evidence.update(bonus_matrix_listing(bonus_matrix))
    else:
        bonus_matrix, evidence = build_hyb_bonus_matrix(len(sequence), arms)

    fold_function = two_phase_pseudoknot_fold
    layout_parameters = {}
    if cutpoint is not None:
        from CPLcofold import two_phase_cofold

        fold_function = two_phase_cofold
        layout_parameters["cutpoint"] = cutpoint
    raw_candidates = fold_function(
        seq=sequence,
        beam_size=beam_size,
        energy_delta=energy_delta,
        max_phase1=max_phase1,
        max_phase2=1,
        energy_model=energy_model,
        output_file=None,
        verbose=False,
        lv=True,
        bonus_matrix=bonus_matrix,
        alpha=alpha,
        beta=beta,
        allow_pseudoknot=allow_pseudoknot,
        **layout_parameters,
    )
    candidates = [
        _candidate(sequence, candidate, index, bonus_matrix, cutpoint)
        for index, candidate in enumerate(raw_candidates)
    ]
    if not candidates:
        raise ValueError("CPLfold did not produce a structure for this sequence and parameter set.")

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
            "linearFoldMode": "Vienna",
            "maxPhase2": 1,
        },
        "evidence": evidence,
        "nucleotideSupport": nucleotide_support,
        "maximumNucleotideSupport": max(nucleotide_support, default=0.0),
    }
    if cutpoint is not None:
        from Utils.strand_layout import add_strand_break

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


def bonus_matrix(payload: dict[str, Any]) -> dict[str, Any]:
    """Prepare the exact sparse bonus matrix used by a guided local run."""

    sequence, cutpoint = _request_sequence_layout(payload, export=True)
    evidence_mode = str(payload.get("evidenceMode", "none"))
    if evidence_mode not in {"none", "hyb-blocks"}:
        raise ValueError("CPLfold evidence mode must be 'none' or 'hyb-blocks'.")
    arms = _normalise_arms(payload.get("evidenceArms"), len(sequence), cutpoint) if evidence_mode == "hyb-blocks" else []
    if evidence_mode == "hyb-blocks" and not arms:
        raise ValueError("No eligible HYB rows are fully contained in the selected reference region.")
    evidence = (build_hyb_bonus_matrix_export(len(sequence), arms) if cutpoint is None
                else _cofold_bonus_matrix_export(len(sequence), cutpoint, arms))
    result = {
        "engine": "CPLfold",
        "engineVersion": CPLFOLD_SOURCE_REVISION,
        "bridgeVersion": BRIDGE_VERSION,
        "sequence": sequence,
        "evidenceMode": evidence_mode,
        "evidence": evidence,
    }
    if cutpoint is not None:
        from Utils.strand_layout import add_strand_break

        result.update({
            "engine": "CPLcofold",
            "displaySequence": add_strand_break(sequence, cutpoint),
            "cutpoint": cutpoint,
            "strandLengths": [cutpoint, len(sequence) - cutpoint],
        })
    return result


def fold_json(payload_json: str) -> str:
    payload = json.loads(payload_json)
    if not isinstance(payload, dict):
        raise ValueError("The CPLfold request must be a JSON object.")
    return json.dumps(fold(payload), separators=(",", ":"), allow_nan=False)


def bonus_matrix_json(payload_json: str) -> str:
    payload = json.loads(payload_json)
    if not isinstance(payload, dict):
        raise ValueError("The CPLfold bonus-matrix request must be a JSON object.")
    return json.dumps(bonus_matrix(payload), separators=(",", ":"), allow_nan=False)


__all__ = [
    "build_hyb_bonus_matrix",
    "build_hyb_bonus_matrix_export",
    "build_hyb_bonus_matrix_fast",
    "collect_hyb_arms",
    "iter_hyb_arms",
    "iter_hyb_rows",
    "bonus_matrix",
    "bonus_matrix_json",
    "fold",
    "fold_json",
]
