"""Numba-accelerated reads-to-bonus transform with strand-bounded Gaussians."""

from collections import Counter
from numbers import Integral

import numpy as np
from numba import njit


@njit(cache=True)
def _accumulate_support(length, cutpoint, blocks, weights, sigma):
    support = np.zeros((length, length), dtype=np.float64)
    first = np.zeros(length, dtype=np.float64)
    second = np.zeros(length, dtype=np.float64)
    for row in range(len(blocks)):
        a, b, c, d = blocks[row]
        start_a = 0 if a <= cutpoint else cutpoint
        end_a = cutpoint if a <= cutpoint else length
        start_b = 0 if c <= cutpoint else cutpoint
        end_b = cutpoint if c <= cutpoint else length
        # Match the existing IRIS half-open interval-centre convention.
        mean_a = ((a - 1) + b) / 2.0
        mean_b = ((c - 1) + d) / 2.0
        peak_a = 0.0
        peak_b = 0.0
        for i in range(start_a, end_a):
            first[i] = np.exp(-0.5 * ((i - mean_a) / sigma) ** 2)
            peak_a = max(peak_a, first[i])
        for j in range(start_b, end_b):
            second[j] = np.exp(-0.5 * ((j - mean_b) / sigma) ** 2)
            peak_b = max(peak_b, second[j])
        for i in range(start_a, end_a):
            for j in range(start_b, end_b):
                value = weights[row] * (first[i] / peak_a) * (second[j] / peak_b)
                support[i, j] += value
                support[j, i] += value
    for i in range(length):
        for j in range(length):
            if support[i, j] < 1e-6:
                support[i, j] = 0.0
            else:
                support[i, j] = np.log1p(support[i, j])
    return support


def build_cofold_bonus_matrix(sequence_length, cutpoint, arms):
    """Return a symmetric N-by-N reads bonus, with no artificial nick leakage.

    ``arms`` contains (one_start, one_end, two_start, two_end), in 1-based
    inclusive coordinates of A+B. Every row contributes once, including
    repeated rows; each interval must lie entirely on one strand. Both intra-
    and intermolecular evidence is accepted. The '&' separator has no index.
    """
    if (isinstance(sequence_length, bool) or not isinstance(sequence_length, Integral)
            or isinstance(cutpoint, bool) or not isinstance(cutpoint, Integral)
            or not 0 < cutpoint < sequence_length):
        raise ValueError("cutpoint must separate two nonempty strands in sequence_length")
    validated = []
    for arm in arms:
        if len(arm) != 4 or any(isinstance(x, bool) or not isinstance(x, Integral) for x in arm):
            raise ValueError("Read arms must contain four integer coordinates")
        a, b, c, d = (int(x) for x in arm)
        if not (1 <= a <= b <= sequence_length and 1 <= c <= d <= sequence_length):
            raise ValueError("Read arm lies outside the concatenated sequence")
        if a <= cutpoint < b or c <= cutpoint < d:
            raise ValueError("An individual read arm cannot cross the strand break")
        validated.append((a, b, c, d))
    if not validated:
        return np.zeros((sequence_length, sequence_length), dtype=np.float32)
    sigma = sum(b - a + 1 + d - c + 1 for a, b, c, d in validated) / (12.0 * len(validated))
    compressed = Counter(validated)
    blocks = np.asarray(list(compressed), dtype=np.int64)
    weights = np.asarray(list(compressed.values()), dtype=np.float64)
    return _accumulate_support(int(sequence_length), int(cutpoint), blocks, weights, sigma).astype(np.float32)
