"""Numba-accelerated reads-to-bonus transform with strand-bounded Gaussians."""

import math
from collections import Counter
from functools import lru_cache
from math import ceil, floor
from collections.abc import Mapping
from numbers import Integral, Real
from os import PathLike
from pathlib import Path

import numpy as np

from ._internal.errors import InputError

__all__ = ["gaussian_sigma", "gaussian_bonus_matrix", "bonus_from_arms", "load_bonus_matrix_file"]

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


def _is_integer(value):
    return not isinstance(value, bool) and isinstance(value, Integral)


def _arm_counts(length, arms, cutpoint=None):
    """Validated read blocks: ``{(a, b, c, d): rows}`` from 1-based inclusive arms.

    ``arms`` is an iterable of ``(a, b, c, d)`` (one row each) or a mapping from
    a block to its positive integer row count. With ``cutpoint``, no arm may
    cross the strand break. ``length=None`` checks only the lower bounds.
    """
    if length is not None and (not _is_integer(length) or length < 1):
        raise InputError("length must be a positive integer")
    if cutpoint is not None and (not _is_integer(cutpoint) or not 0 < cutpoint < length):
        raise InputError("cutpoint must separate two nonempty strands in sequence_length")
    counts = Counter()
    try:
        items = arms.items() if isinstance(arms, Mapping) else ((arm, 1) for arm in arms)
        for arm, weight in items:
            if len(arm) != 4 or any(not _is_integer(x) for x in arm):
                raise InputError("Read arms must contain four integer coordinates")
            if not _is_integer(weight) or weight < 1:
                raise InputError("Read block weights must be positive integer counts")
            a, b, c, d = (int(x) for x in arm)
            if not (1 <= a <= b and 1 <= c <= d) or (length is not None and max(b, d) > length):
                raise InputError("Read arm lies outside the concatenated sequence")
            if cutpoint is not None and (a <= cutpoint < b or c <= cutpoint < d):
                raise InputError("An individual read arm cannot cross the strand break")
            counts[(a, b, c, d)] += int(weight)
    except TypeError as error:
        raise InputError("Read arms must contain four integer coordinates") from error
    return counts


def _mean_arm_length(counts):
    total = sum(w * (b - a + d - c + 2) for (a, b, c, d), w in counts.items())
    return total / (2 * sum(counts.values()))


def gaussian_sigma(counts):
    """Standard deviation of each arm's Gaussian profile: the mean arm length / 6.

    ``counts`` maps 1-based inclusive blocks ``(a, b, c, d)`` to positive integer
    row counts; an empty or invalid mapping raises :class:`cplfold.InputError`.
    """
    if not isinstance(counts, Mapping) or not counts:
        raise InputError("Gaussian sigma needs at least one read block")
    return _mean_arm_length(_arm_counts(None, counts)) / 6


def _gaussian_bonus_stripes(length, cutpoint, counts, sigma=None, use_numba=True):
    """Yield float64 stripes; only their upper triangles (diagonal included)
    are meaningful. An export consumes these without a full-size matrix.
    ``counts`` must already be valid; ``sigma=None`` uses the mean arm length / 6."""
    if sigma is None:
        sigma = _mean_arm_length(counts) / 6
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


def _gaussian_bonus_matrix(length, cutpoint, counts, sigma=None, use_numba=True):
    """Symmetric float64 matrix of the stripes of already valid ``counts``."""
    result = np.zeros((length, length), dtype=np.float64)
    for start, stripe in _gaussian_bonus_stripes(length, cutpoint, counts, sigma, use_numba):
        for i, values in enumerate(stripe, start):
            result[i, i:] = result[i:, i] = values[i:]
    return result


def _checked_sigma(sigma):
    if sigma is None:
        return None
    if isinstance(sigma, bool) or not isinstance(sigma, Real) or not math.isfinite(sigma) or sigma <= 0:
        raise InputError("sigma must be a finite positive number")
    return float(sigma)


def gaussian_bonus_matrix(length, counts, *, cutpoint=None, sigma=None):
    """IRIS's Gaussian read support as a symmetric float64 ``length`` x ``length`` matrix.

    ``counts`` maps 1-based inclusive read blocks ``(a, b, c, d)`` (arm one
    ``a..b``, arm two ``c..d``) to positive integer row counts. Each arm spreads
    as a Gaussian around its centre, within its strand when ``cutpoint`` is
    given; support below 2^-23 is zero and the result is log1p. ``sigma=None``
    uses :func:`gaussian_sigma`. Empty ``counts`` give a zero matrix. Invalid
    arguments raise :class:`cplfold.InputError`.
    """
    if not isinstance(counts, Mapping):
        raise InputError("counts must map read blocks to row counts")
    return bonus_from_arms(length, counts, cutpoint=cutpoint, sigma=sigma)


def bonus_from_arms(length, arms, *, cutpoint=None, sigma=None):
    """The :func:`gaussian_bonus_matrix` of read rows, for one strand or two.

    ``arms`` lists ``(a, b, c, d)`` rows (1-based inclusive coordinates on the
    sequence without ``&``), or maps each such block to its row count. With
    ``cutpoint`` (two strands) no arm may cross the strand break. No rows give
    a zero matrix. Invalid arguments raise :class:`cplfold.InputError`.
    """
    sigma = _checked_sigma(sigma)
    counts = _arm_counts(length, arms, cutpoint)
    if not counts:
        return np.zeros((length, length), dtype=np.float64)
    return _gaussian_bonus_matrix(length, cutpoint, counts, sigma)


def load_bonus_matrix_file(path: str, sequence_length: int) -> np.ndarray:
    """Load Hyb2's sparse, upper-triangle bonus-matrix TSV export.

    The web export stores 1-based prepared-sequence coordinates and only one
    value for each upper-triangle cell. Keep the written scores in float64;
    the single-strand parser narrows them to float32 at its input boundary.
    Comment lines beginning with ``#`` carry optional metadata such as the
    declared sequence length.

    A missing or unreadable file raises ``OSError`` (``FileNotFoundError`` when
    it does not exist); a malformed file raises
    :class:`cplfold.InputError`, a ``ValueError``, as do a ``path`` that is not
    a path and a ``sequence_length`` that is not a positive integer.
    """
    if not isinstance(path, (str, PathLike)):
        raise InputError("CPLfold bonus matrix path must be a string or path")
    if not _is_integer(sequence_length) or sequence_length < 1:
        raise InputError("sequence_length must be a positive integer")

    matrix_path = Path(path).expanduser()
    try:
        contents = matrix_path.read_text(encoding="utf-8")
    except FileNotFoundError as error:
        raise FileNotFoundError(f"CPLfold bonus matrix file was not found: {matrix_path}") from error
    except UnicodeError as error:
        raise InputError(f"CPLfold bonus matrix file is not valid UTF-8: {matrix_path}") from error
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
                    raise InputError(
                        f"CPLfold bonus matrix line {line_number} has an invalid sequence_length"
                    ) from error
            continue

        fields = raw_line.rstrip("\r\n").split("\t")
        if len(fields) == 1:
            fields = stripped.split()
        if fields[0].strip().lower() == "prepared_position_1":
            if len(fields) < 3:
                raise InputError(
                    f"CPLfold bonus matrix header on line {line_number} must contain three columns"
                )
            continue
        if len(fields) != 3:
            raise InputError(
                f"CPLfold bonus matrix line {line_number} must contain three tab-separated columns"
            )

        try:
            one = int(fields[0])
            two = int(fields[1])
            value = float(fields[2])
        except ValueError as error:
            raise InputError(
                f"CPLfold bonus matrix line {line_number} contains an invalid coordinate or score"
            ) from error

        if not 1 <= one <= sequence_length or not 1 <= two <= sequence_length:
            raise InputError(
                f"CPLfold bonus matrix line {line_number} must use coordinates from 1 to {sequence_length}"
            )
        if not math.isfinite(value):
            raise InputError(f"CPLfold bonus matrix line {line_number} contains a non-finite score")
        if abs(value) > np.finfo(np.float32).max:
            raise InputError(f"CPLfold bonus matrix line {line_number} exceeds the supported float32 range")

        left, right = sorted((one, two))
        pair = (left, right)
        if pair in seen_pairs:
            raise InputError(
                f"CPLfold bonus matrix line {line_number} repeats coordinate pair {left}-{right}"
            )
        seen_pairs.add(pair)
        matrix[left - 1, right - 1] = matrix[right - 1, left - 1] = value
        data_rows += 1

    if declared_length is not None and declared_length != sequence_length:
        raise InputError(
            "CPLfold bonus matrix sequence_length="
            f"{declared_length} does not match the sequence length {sequence_length}"
        )
    if data_rows == 0 and declared_length is None:
        raise InputError(
            "CPLfold bonus matrix contains no data rows and does not declare sequence_length"
        )
    return matrix
