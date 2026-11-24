"""Numba-accelerated reads-to-bonus transform with strand-bounded Gaussians."""

import math
from collections import Counter
from functools import lru_cache
from math import ceil, floor
from numbers import Integral
from pathlib import Path

import numpy as np

from ._internal.cli_input import CofoldInputError

__all__ = ["mean_arm_length", "gaussian_sigma", "gaussian_bonus_stripes", "gaussian_bonus_matrix",
           "cofold_counts", "build_cofold_bonus_matrix", "load_bonus_matrix_file"]

_IRIS_THRESHOLD = 2.0 ** -23  # IRIS's EPS: smaller support is zero


def _accumulate_support(target, left, right, weight):
    for i in range(len(left)):
        for j in range(len(right)):
            target[i, j] += (left[i] * right[j]) * weight


try:
    from numba import njit
except ImportError:
    pass  # Standalone IRIS only needs NumPy.
else:
    # The native IRIS and CPLfold entry points use different package roots;
    # avoid caching a module name unavailable in the other interpreter.
    _accumulate_support = njit(cache=False)(_accumulate_support)


def mean_arm_length(counts):
    total = sum(w * (b - a + d - c + 2) for (a, b, c, d), w in counts.items())
    return total / (2 * sum(counts.values()))


def gaussian_sigma(counts):
    """Standard deviation of each arm's Gaussian profile: the mean arm length / 6."""
    return mean_arm_length(counts) / 6


def gaussian_bonus_stripes(length, cutpoint, counts, sigma=None, use_numba=True):
    """Yield float64 stripes; only their upper triangles (diagonal included)
    are meaningful. An export consumes these without a full-size matrix.
    ``sigma=None`` uses :func:`gaussian_sigma` of ``counts``."""
    if sigma is None:
        sigma = gaussian_sigma(counts)
    height = 32
    stripes = {}
    jit = use_numba and hasattr(_accumulate_support, "nopython_signatures")

    @lru_cache(None)
    def profile(mean, lo, hi):
        # Beyond 39 sigma exp(-z²/2) rounds to zero even in binary64.
        # No representable tail is discarded before summing the evidence.
        start = max(lo, floor(mean - 39 * sigma))
        stop = min(hi, ceil(mean + 39 * sigma) + 1)
        g = np.exp(-0.5 * ((np.arange(start, stop, dtype=np.float64) - mean) / sigma) ** 2)
        return start, g / g.max()

    def arm(a, b):
        lo, hi = ((0, length) if cutpoint is None else
                  (0, cutpoint) if a <= cutpoint else (cutpoint, length))
        return profile((a - 1 + b) / 2.0, lo, hi)

    for (a, b, c, d), weight in counts.items():
        first, second = arm(a, b), arm(c, d)
        for (i, left), (j, right) in ((first, second), (second, first)):
            block = (i, left, j, right, weight)
            for k in range(i // height, (min(i + len(left), j + len(right)) - 1) // height + 1):
                stripes.setdefault(k, []).append(block)

    for k in sorted(stripes):
        row = k * height
        support = np.zeros((min(height, length - row), length), dtype=np.float64)
        for i, left, j, right, weight in stripes.pop(k):
            start, stop = max(i, row), min(i + len(left), row + len(support))
            target = support[start-row:stop-row, j:j+len(right)]
            part = left[start-i:stop-i]
            if jit:
                _accumulate_support(target, part, right, float(weight))
            else:
                target += np.outer(part, right) * weight
        support[support < _IRIS_THRESHOLD] = 0.0
        yield row, np.log1p(support, out=support)


def gaussian_bonus_matrix(length, cutpoint, counts, sigma=None, use_numba=True):
    """Symmetric float64 matrix of the stripes; ``sigma=None`` uses :func:`gaussian_sigma`."""
    result = np.zeros((length, length), dtype=np.float64)
    for start, stripe in gaussian_bonus_stripes(length, cutpoint, counts, sigma, use_numba):
        for i, values in enumerate(stripe, start):
            result[i, i:] = result[i:, i] = values[i:]
    return result


def cofold_counts(sequence_length, cutpoint, arms):
    """Validate compact A+B coordinates before spreading a row or counted block."""
    if (isinstance(sequence_length, bool) or not isinstance(sequence_length, Integral)
            or isinstance(cutpoint, bool) or not isinstance(cutpoint, Integral)
            or not 0 < cutpoint < sequence_length):
        raise ValueError("cutpoint must separate two nonempty strands in sequence_length")
    counts = Counter()
    for arm, weight in (arms.items() if isinstance(arms, dict) else ((arm, 1) for arm in arms)):
        if len(arm) != 4 or any(isinstance(x, bool) or not isinstance(x, Integral) for x in arm):
            raise ValueError("Read arms must contain four integer coordinates")
        if isinstance(weight, bool) or not isinstance(weight, Integral) or weight < 1:
            raise ValueError("Read block weights must be positive integer counts")
        a, b, c, d = (int(x) for x in arm)
        if not (1 <= a <= b <= sequence_length and 1 <= c <= d <= sequence_length):
            raise ValueError("Read arm lies outside the concatenated sequence")
        if a <= cutpoint < b or c <= cutpoint < d:
            raise ValueError("An individual read arm cannot cross the strand break")
        counts[(a, b, c, d)] += int(weight)
    return counts


def build_cofold_bonus_matrix(sequence_length, cutpoint, arms):
    """Return a symmetric float64 matrix; each 1-based inclusive interval
    lies on one strand and the '&' separator has no coordinate."""
    counts = cofold_counts(sequence_length, cutpoint, arms)
    if not counts:
        return np.zeros((sequence_length, sequence_length), dtype=np.float64)
    return gaussian_bonus_matrix(sequence_length, cutpoint, counts)


def load_bonus_matrix_file(path: str, sequence_length: int) -> np.ndarray:
    """Load Hyb2's sparse, upper-triangle bonus-matrix TSV export.

    The web export stores 1-based prepared-sequence coordinates and only one
    value for each upper-triangle cell. Keep the written scores in float64;
    the single-strand parser narrows them to float32 at its input boundary.
    Comment lines beginning with ``#`` carry optional metadata such as the
    declared sequence length.

    A missing or unreadable file raises ``OSError`` (``FileNotFoundError`` when
    it does not exist); a malformed file raises
    :class:`cplfold.CofoldInputError`, a ``ValueError``.
    """

    matrix_path = Path(path).expanduser()
    try:
        contents = matrix_path.read_text(encoding="utf-8")
    except FileNotFoundError as error:
        raise FileNotFoundError(f"CPLfold bonus matrix file was not found: {matrix_path}") from error
    except UnicodeError as error:
        raise CofoldInputError(f"CPLfold bonus matrix file is not valid UTF-8: {matrix_path}") from error
    except OSError as error:
        raise OSError(f"Could not read CPLfold bonus matrix file {matrix_path}: {error}") from error

    matrix = np.zeros((sequence_length, sequence_length), dtype=np.float64)
    declared_length = None
    seen_pairs = set()
    data_rows = 0

    for line_number, raw_line in enumerate(contents.splitlines(), 1):
        stripped = raw_line.strip()
        if not stripped:
            continue
        if stripped.startswith("#"):
            metadata = stripped[1:].strip()
            if metadata.startswith("sequence_length="):
                raw_length = metadata.split("=", 1)[1].strip()
                try:
                    declared_length = int(raw_length)
                except ValueError as error:
                    raise CofoldInputError(
                        f"CPLfold bonus matrix line {line_number} has an invalid sequence_length"
                    ) from error
            continue

        fields = raw_line.rstrip("\r\n").split("\t")
        if len(fields) == 1:
            fields = stripped.split()
        if fields[0].strip().lower() == "prepared_position_1":
            if len(fields) < 3:
                raise CofoldInputError(
                    f"CPLfold bonus matrix header on line {line_number} must contain three columns"
                )
            continue
        if len(fields) != 3:
            raise CofoldInputError(
                f"CPLfold bonus matrix line {line_number} must contain three tab-separated columns"
            )

        try:
            one = int(fields[0])
            two = int(fields[1])
            value = float(fields[2])
        except ValueError as error:
            raise CofoldInputError(
                f"CPLfold bonus matrix line {line_number} contains an invalid coordinate or score"
            ) from error

        if not 1 <= one <= sequence_length or not 1 <= two <= sequence_length:
            raise CofoldInputError(
                f"CPLfold bonus matrix line {line_number} must use coordinates from 1 to {sequence_length}"
            )
        if not math.isfinite(value):
            raise CofoldInputError(f"CPLfold bonus matrix line {line_number} contains a non-finite score")
        if abs(value) > np.finfo(np.float32).max:
            raise CofoldInputError(f"CPLfold bonus matrix line {line_number} exceeds the supported float32 range")

        left, right = sorted((one, two))
        pair = (left, right)
        if pair in seen_pairs:
            raise CofoldInputError(
                f"CPLfold bonus matrix line {line_number} repeats coordinate pair {left}-{right}"
            )
        seen_pairs.add(pair)
        matrix[left - 1, right - 1] = matrix[right - 1, left - 1] = value
        data_rows += 1

    if declared_length is not None and declared_length != sequence_length:
        raise CofoldInputError(
            "CPLfold bonus matrix sequence_length="
            f"{declared_length} does not match the sequence length {sequence_length}"
        )
    if data_rows == 0 and declared_length is None:
        raise CofoldInputError(
            "CPLfold bonus matrix contains no data rows and does not declare sequence_length"
        )
    return matrix
