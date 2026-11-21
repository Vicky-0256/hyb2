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
BRIDGE_VERSION = "7"
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


# ---------------------------------------------------------------------------
# Support kernels.
#
# The chimeras of a fold become its bonus matrix B in one of five ways.  Each
# sums one contribution per eligible HYB row (repeated coordinates once, with
# their row count as weight w).  The first four zero cells below 1e-6 and apply
# log1p; duplex saturates its counts instead (below).  The transforms do not
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
#   rnacofold         the two arm sequences are cofolded with RNAcofold, as the
#                     COMRADES procedure does, and each intermolecular pair it
#                     forms counts w; no other cell receives support.
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
# ---------------------------------------------------------------------------

SUPPORT_KERNELS = ("gaussian", "block", "block-normalized", "rnacofold", "duplex")
PAIR_KERNELS = ("rnacofold", "duplex")
# B = s C / (C + q): s calibrated so that alpha is the ratio of the bonus of the
# folded structure to its free energy (kernel_experiments/config.py BONUS_SCALE,
# Vienna backend); q the mean count of the round(n x SATURATING_TOP) best pairs.
SATURATING_SCALE = 3.6
SATURATING_TOP = 1.0 / 3.0
DEFAULT_SUPPORT_KERNEL = "gaussian"
SUPPORT_TRANSFORMS = {
    "gaussian": "IRIS-style Gaussian arm blocks; symmetric outer product; threshold 1e-6; log1p",
    "block": "Full arm blocks, w per cell of arm one x arm two; symmetric; threshold 1e-6; log1p",
    "block-normalized": "Full arm blocks, w / sqrt(len one x len two) per cell; symmetric; threshold 1e-6; log1p",
    "rnacofold": "RNAcofold intermolecular pairs of the two arms, w per pair (COMRADES evidence); symmetric; log1p",
    "duplex": "RNAduplex pairs of the two arms, C = rows per pair (COMRADES evidence); symmetric; "
                 "s C / (C + q), s = 3.6 kcal/mol, q = mean C of the n/3 best-supported pairs",
}
# The COMRADES(ViennaRNA) evidence settings (desktop/runtime/comrades.py, web/structure.worker.js).
RNACOFOLD_TEMPERATURE = 37.0
RNACOFOLD_MIN_LOOP = 3
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
    arm_total = sum(w * ((b - a + 1) + (d - c + 1)) for (a, b, c, d), w in compressed.items())
    return {
        "source": "hyb-block-intervals",
        "supportKernel": kernel,
        "transform": SUPPORT_TRANSFORMS[kernel],
        "coordinateSystem": "prepared-sequence, 1-based inclusive input",
        "inputRecords": int(records),
        "uniqueBlocks": len(compressed),
        "meanArmLength": arm_total / (2.0 * records),
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


def _dot_bracket_pairs(structure: str) -> list[tuple[int, int]]:
    stack, pairs = [], []
    for position, character in enumerate(structure, 1):
        if character == "(":
            stack.append(position)
        elif character == ")":
            pairs.append((stack.pop(), position))
    return pairs


def rnacofold_pair_counts(
    sequence: str,
    arms: Any,
    *,
    temperature: float = RNACOFOLD_TEMPERATURE,
    min_loop: int = RNACOFOLD_MIN_LOOP,
    progress: Any = None,
) -> tuple[Counter, dict[str, Any]]:
    """COMRADES evidence: each row's two arms cofolded with RNAcofold.

    Returns ``{(i, j): rows}`` (1-based, i < j) for the intermolecular pairs of
    every cofold, and a summary.  Identical blocks fold identically, so each
    unique block is cofolded once and counted with its row count.  Needs
    ViennaRNA's Python module; the browser computes the same pairs in its
    ViennaRNA worker and passes them as ``evidencePairs``.
    """
    try:
        import RNA  # noqa: PLC0415
    except ImportError as error:
        raise ValueError("The rnacofold support kernel needs ViennaRNA's Python module; "
                         "supply evidencePairs computed by the host instead.") from error
    compressed = _checked_blocks(len(sequence), arms)
    model = RNA.md()
    model.temperature = float(temperature)
    model.min_loop_size = int(min_loop)
    counts: Counter = Counter()
    with_pairs = 0
    for index, (block, weight) in enumerate(compressed.items()):
        one_start, one_end, two_start, two_end = block
        one, two = sequence[one_start - 1:one_end], sequence[two_start - 1:two_end]
        compound = RNA.fold_compound(one + "&" + two, model, RNA.OPTION_MFE | RNA.OPTION_HYBRID)
        structure, energy = compound.mfe_dimer()
        structure = structure.replace("&", "")
        if not math.isfinite(energy) or len(structure) != len(one) + len(two):
            raise ValueError("ViennaRNA RNAcofold returned an invalid two-strand structure.")
        found = 0
        for left, right in _dot_bracket_pairs(structure):
            if left <= len(one) < right:
                i, j = one_start + left - 1, two_start + right - len(one) - 1
                if i != j:
                    counts[(min(i, j), max(i, j))] += weight
                    found += 1
        if found:
            with_pairs += weight
        if progress is not None and ((index + 1) % 256 == 0 or index + 1 == len(compressed)):
            progress(index + 1, len(compressed))
    return counts, {
        "cofoldedBlocks": len(compressed),
        "cofoldsWithIntermolecularPairs": int(with_pairs),
        "temperature": float(temperature),
        "minimumLoop": int(min_loop),
    }


DUPLEX_CHUNK = 1000
# below this many blocks the duplexes are computed in this process
DUPLEX_PARALLEL_MIN = 4000
# in one process, progress is reported every this many blocks
DUPLEX_PROGRESS_STEP = 100


def _duplex_chunk(sequence: str, blocks: list) -> tuple[Counter, int]:
    """Intermolecular pairs (genome i < j) of each block's RNAduplex duplex,
    weighted by the block's row count, and the rows whose arms formed one."""
    import RNA  # noqa: PLC0415

    counts: Counter = Counter()
    with_pairs = 0
    for (one_start, one_end, two_start, two_end), weight in blocks:
        hit = RNA.duplexfold(sequence[one_start - 1:one_end], sequence[two_start - 1:two_end])
        left, _, right = hit.structure.partition("&")
        opens = [k for k, ch in enumerate(left) if ch == "("]
        if not opens:
            # arms that cannot pair, e.g. both purine-rich: RNAduplex returns
            # ".&." with dG 100000; they add nothing
            continue
        closes = [k for k, ch in enumerate(right) if ch == ")"]
        if len(opens) != len(closes) or not math.isfinite(hit.energy):
            raise ValueError("ViennaRNA RNAduplex returned an invalid duplex.")
        # the duplex covers one[i - len(left) + 1 .. i] and two[j .. j + len(right) - 1] (1-based)
        first_one = one_start + hit.i - len(left)
        first_two = two_start + hit.j - 1
        found = 0
        for left_index, right_index in zip(opens, reversed(closes)):
            i, j = first_one + left_index, first_two + right_index
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


def _duplex_forked(sequence: str, blocks: list, workers: int, progress: Any) -> tuple[Counter, int]:
    """``_duplex_chunk`` over forked processes.  Forking passes the sequence and
    the blocks without pickling a function (this module is loaded from its
    file, so a child could not import it by name); each child sends back the
    counts of its chunks."""
    import multiprocessing  # noqa: PLC0415

    context = multiprocessing.get_context("fork")
    chunks = [blocks[start:start + DUPLEX_CHUNK] for start in range(0, len(blocks), DUPLEX_CHUNK)]
    results = context.Queue()

    def run(share: list) -> None:
        try:
            for chunk in share:
                results.put(("chunk", len(chunk), _duplex_chunk(sequence, chunk)))
        except BaseException as error:  # noqa: BLE001
            results.put(("error", 0, f"{type(error).__name__}: {error}"))

    processes = [context.Process(target=run, args=(chunks[index::workers],), daemon=True)
                 for index in range(workers)]
    for process in processes:
        process.start()
    counts: Counter = Counter()
    with_pairs = done = received = 0
    try:
        while received < len(chunks):
            kind, size, value = results.get()
            if kind == "error":
                raise ValueError(f"ViennaRNA RNAduplex failed in a worker process: {value}")
            partial, rows = value
            counts.update(partial)
            with_pairs += rows
            done += size
            received += 1
            if progress is not None:
                progress(done, len(blocks))
    finally:
        for process in processes:
            if process.is_alive():
                process.terminate()
            process.join()
    return counts, with_pairs


def duplex_pair_counts(sequence: str, arms: Any, *, progress: Any = None,
                       workers: int | None = None) -> tuple[Counter, dict[str, Any]]:
    """COMRADES evidence C_ij: each row's two arms hybridised with ViennaRNA's
    RNAduplex, which forms intermolecular pairs only, as the hybrid step of
    COMRADES' comradesMakeConstraints does.

    Returns ``{(i, j): rows}`` (1-based, i < j) and a summary.  Each unique
    block is hybridised once and counted with its row count.  Arms that cannot
    pair add nothing.  From DUPLEX_PARALLEL_MIN blocks on, the blocks are
    shared among ``workers`` forked processes where the platform forks.
    Needs ViennaRNA's Python module; other hosts pass the counts as
    ``evidencePairs``.
    """
    try:
        import RNA  # noqa: PLC0415
    except ImportError as error:
        raise ValueError("The duplex support kernel needs ViennaRNA's Python module; "
                         "supply evidencePairs computed by the host instead.") from error
    blocks = sorted(_checked_blocks(len(sequence), arms).items())
    workers = min(_duplex_workers(workers), max(1, -(-len(blocks) // DUPLEX_CHUNK)))
    if workers > 1 and len(blocks) >= DUPLEX_PARALLEL_MIN:
        counts, with_pairs = _duplex_forked(sequence, blocks, workers, progress)
    else:
        workers = 1
        counts, with_pairs = Counter(), 0
        for start in range(0, len(blocks), DUPLEX_PROGRESS_STEP):
            partial, rows = _duplex_chunk(sequence, blocks[start:start + DUPLEX_PROGRESS_STEP])
            counts.update(partial)
            with_pairs += rows
            if progress is not None:
                progress(min(start + DUPLEX_PROGRESS_STEP, len(blocks)), len(blocks))
    return counts, {"hybridisedBlocks": len(blocks), "duplexesWithPairs": int(with_pairs),
                    "duplexWorkers": workers,
                    "pairCounter": "ViennaRNA %s RNAduplex, 37 C" % getattr(RNA, "__version__", "?")}



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


def _pair_counts_for(kernel: str, sequence: str, arms: Any, progress: Any = None,
                     duplex_workers: int | None = None) -> tuple[Counter, dict[str, Any]]:
    if kernel == "duplex":
        return duplex_pair_counts(sequence, arms, progress=progress, workers=duplex_workers)
    return rnacofold_pair_counts(sequence, arms, progress=progress)


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


def _pair_summary(sequence_length: int, compressed: Counter, pair_counts: Counter, bonus_cells: int,
                  upper_cells: int, maximum: float, kernel: str = "rnacofold",
                  q: Any = None) -> dict[str, Any]:
    if compressed:
        summary = _summary(sequence_length, kernel, compressed, bonus_cells, upper_cells, maximum)
    else:
        summary = _empty_support(kernel)
        summary.update({"transform": SUPPORT_TRANSFORMS[kernel], "nonzeroBonusCells": int(bonus_cells),
                        "nonzeroUpperTriangleCells": int(upper_cells), "maximumBonus": float(maximum)})
    summary["supportedPairs"] = len(pair_counts)
    if kernel == "duplex":
        # The q actually used, not a fresh one: a caller that passes the
        # genome's q must not be told the window's.
        summary.update({"saturatingScale": SATURATING_SCALE,
                        "saturatingQ": resolve_q(sequence_length, pair_counts, q)})
    return summary


def _request_duplex(payload: dict[str, Any]) -> dict[str, Any]:
    """Native hosts' duplex setting: ``duplexWorkers`` (None: one per CPU)."""
    workers = payload.get("duplexWorkers")
    if workers is not None:
        workers = _integer(workers, 1, 1024, "duplex workers")
    return {"duplex_workers": workers}


def _request_pairs(payload: dict[str, Any], kernel: str, evidence_mode: str, sequence_length: int) -> Counter | None:
    """The host's pair counts (``evidencePairs``) for a pair kernel: RNAcofold
    pairs for rnacofold, RNAduplex pairs for duplex; None when it sent
    none, so they are computed here."""
    raw = payload.get("evidencePairs")
    if raw is None or kernel not in PAIR_KERNELS or evidence_mode != "hyb-blocks":
        return None
    return _normalise_evidence_pairs(raw, sequence_length)


def _pair_value(kernel: str, count: Any, q: float) -> Any:
    """The bonus of a pair with ``count`` rows: log1p, or s C / (C + q)."""
    if kernel == "duplex":
        return np.float32(SATURATING_SCALE) * count / (count + np.float32(q))
    return np.log1p(count)


def build_pair_bonus_matrix(
    sequence_length: int, pair_counts: Counter, arms: Any = None, kernel: str = "rnacofold",
    q: Any = None,
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """A pair kernel's matrix: each counted pair gets ``log1p`` of its rows
    (rnacofold) or s C / (C + q) (duplex); every other cell is zero.

    ``q`` as in ``resolve_q``: None derives it from this fold, a number holds
    it fixed so the same alpha means the same bonus across fold lengths."""
    compressed = _checked_blocks(sequence_length, arms or [])
    if not pair_counts:
        return None, _pair_summary(sequence_length, compressed, pair_counts, 0, 0, 0.0, kernel, q)
    support = np.zeros((sequence_length, sequence_length), dtype=np.float32)
    ones = np.fromiter((i - 1 for i, _ in pair_counts), dtype=np.int64, count=len(pair_counts))
    twos = np.fromiter((j - 1 for _, j in pair_counts), dtype=np.int64, count=len(pair_counts))
    rows = np.fromiter(pair_counts.values(), dtype=np.float32, count=len(pair_counts))
    if kernel == "duplex":
        rows = _pair_value(kernel, rows, resolve_q(sequence_length, pair_counts, q)).astype(np.float32)
        support[ones, twos] = rows
        support[twos, ones] = rows
        bonus = support
    else:
        support[ones, twos] = rows
        support[twos, ones] = rows
        bonus = _finish_support(support)
    return bonus, _pair_summary(sequence_length, compressed, pair_counts, *_dense_counts(bonus), kernel, q)


def _pair_bonus_export(sequence_length: int, pair_counts: Counter, arms: Any = None,
                       kernel: str = "rnacofold", q: Any = None) -> dict[str, Any]:
    """Sparse upper-triangle export of ``build_pair_bonus_matrix``."""
    compressed = _checked_blocks(sequence_length, arms or [])
    scale = resolve_q(sequence_length, pair_counts, q)
    entries = [{"one": i, "two": j, "value": float(_pair_value(kernel, np.float32(rows), scale))}
               for (i, j), rows in sorted(pair_counts.items())]
    summary = _pair_summary(sequence_length, compressed, pair_counts, 2 * len(entries), len(entries),
                            max((entry["value"] for entry in entries), default=0.0), kernel, scale)
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
    strands; the Gaussian kernel is clipped at the nick.  The rnacofold kernel
    uses ``pairs`` when given, otherwise it cofolds the arms of ``sequence``,
    calling ``progress(blocks_done, blocks)`` as it goes; the duplex kernel
    likewise hybridises them with RNAduplex (``duplex_workers`` processes) and
    takes ``q`` as in ``resolve_q``.
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
            pairs, extra = _pair_counts_for(kernel, sequence, arms, progress=progress,
                                            duplex_workers=duplex_workers)
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


def fold(payload: dict[str, Any], progress: Any = None) -> dict[str, Any]:
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
    kernel = support_kernel(payload.get("supportKernel"))
    pairs = _request_pairs(payload, kernel, evidence_mode, len(sequence))
    if kernel != "gaussian":
        bonus_matrix, evidence = build_support_bonus_matrix(
            len(sequence), arms, kernel, cutpoint=cutpoint, sequence=sequence, pairs=pairs,
            q=payload.get("saturatingQ"), progress=progress, **_request_duplex(payload))
        evidence.update(bonus_matrix_listing(bonus_matrix))
    elif cutpoint is not None:
        bonus_matrix, evidence = build_cofold_hyb_bonus_matrix(len(sequence), cutpoint, arms)
    elif arms and _NUMBA_AVAILABLE:
        # Native hosts: same transform without one n-by-n outer product per
        # block (values agree with the dense builder to ~1e-6).
        bonus_matrix, evidence = build_hyb_bonus_matrix_fast(len(sequence), arms)
        evidence.update(bonus_matrix_listing(bonus_matrix))
    else:
        bonus_matrix, evidence = build_hyb_bonus_matrix(len(sequence), arms)
    evidence["supportKernel"] = kernel

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
            "supportKernel": kernel,
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


def bonus_matrix(payload: dict[str, Any], progress: Any = None) -> dict[str, Any]:
    """Prepare the exact sparse bonus matrix used by a guided local run."""

    sequence, cutpoint = _request_sequence_layout(payload, export=True)
    evidence_mode = str(payload.get("evidenceMode", "none"))
    if evidence_mode not in {"none", "hyb-blocks"}:
        raise ValueError("CPLfold evidence mode must be 'none' or 'hyb-blocks'.")
    arms = _normalise_arms(payload.get("evidenceArms"), len(sequence), cutpoint) if evidence_mode == "hyb-blocks" else []
    if evidence_mode == "hyb-blocks" and not arms:
        raise ValueError("No eligible HYB rows are fully contained in the selected reference region.")
    kernel = support_kernel(payload.get("supportKernel"))
    if kernel == "gaussian":
        evidence = (build_hyb_bonus_matrix_export(len(sequence), arms) if cutpoint is None
                    else _cofold_bonus_matrix_export(len(sequence), cutpoint, arms))
    elif kernel in ("block", "block-normalized"):
        evidence = _block_bonus_export(len(sequence), arms, kernel == "block-normalized")
    else:
        pairs = _request_pairs(payload, kernel, evidence_mode, len(sequence))
        extra: dict[str, Any] = {}
        if pairs is None:
            pairs, extra = (_pair_counts_for(kernel, sequence, arms, progress=progress, **_request_duplex(payload))
                            if arms else (Counter(), {}))
        evidence = _pair_bonus_export(len(sequence), pairs, arms, kernel, payload.get("saturatingQ"))
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
        from Utils.strand_layout import add_strand_break

        result.update({
            "engine": "CPLcofold",
            "displaySequence": add_strand_break(sequence, cutpoint),
            "cutpoint": cutpoint,
            "strandLengths": [cutpoint, len(sequence) - cutpoint],
        })
    return result


# ``progress(blocks_done, blocks)``, when given, is called while the rnacofold
# or duplex kernel computes its pairs (the browser worker reports it).
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
    "rnacofold_pair_counts",
    "duplex_pair_counts",
    "support_kernel",
    "collect_hyb_arms",
    "iter_hyb_arms",
    "iter_hyb_rows",
    "bonus_matrix",
    "bonus_matrix_json",
    "fold",
    "fold_json",
]
