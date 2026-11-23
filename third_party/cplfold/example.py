#!/usr/bin/env python3
"""
CPLfold Example - COMRADES-guided Pseudoknot LinearFold

Example usage demonstrating:
1. Basic pseudoknot prediction
2. Using beta parameter for pseudoknot preference
3. Integrating COMRADES/PARIS bonus matrix

Author: Ke Wang
"""

import numpy as np

import cplfold


def show(results, count=3):
    """Print the best candidates (cplfold.fold itself never prints)."""
    for rank, prediction in enumerate(results[:count], 1):
        print(f"S-{rank}: {prediction.structure}  {prediction.energy:.2f} kcal/mol  ({prediction.kind})")


def example_basic():
    """Basic usage example."""
    print("=" * 60)
    print("Example 1: Basic Usage")
    print("=" * 60)

    # A known pseudoknot sequence
    seq = "GGCGCGGCACCGUCCGCGGAACAAACGG"

    results = cplfold.fold(
        seq,
        beam_size=100,
        energy_delta=5.0,
        max_phase1=5,
    )
    show(results)

    return results


def example_with_beta():
    """Example with beta parameter for pseudoknot preference."""
    print("\n" + "=" * 60)
    print("Example 2: With Beta Parameter")
    print("=" * 60)

    seq = "GGCGCGGCACCGUCCGCGGAACAAACGG"

    results = cplfold.fold(
        seq,
        beam_size=100,
        energy_delta=5.0,
        max_phase1=5,
        beta=0.3,  # Favor pseudoknots in ranking
    )
    show(results)

    return results


def example_with_bonus_matrix():
    """Example with COMRADES/PARIS bonus matrix."""
    print("\n" + "=" * 60)
    print("Example 3: With Bonus Matrix (simulated)")
    print("=" * 60)

    seq = "GGCGCGGCACCGUCCGCGGAACAAACGG"
    n = len(seq)

    # Create a simulated bonus matrix (in real usage, load from COMRADES data)
    bonus_matrix = np.zeros((n, n), dtype=np.float32)

    # Add some simulated bonuses for expected base pairs
    # In real usage, this would come from experimental data
    for i in range(5):
        j = n - i - 1
        bonus_matrix[i, j] = 1.0
        bonus_matrix[j, i] = 1.0

    results = cplfold.fold(
        seq,
        beam_size=100,
        energy_delta=5.0,
        max_phase1=5,
        bonus=bonus_matrix,
        alpha=0.5,
        beta=0.3,
    )
    show(results)

    return results


if __name__ == "__main__":
    print("CPLfold Example Usage")
    print("=" * 60)

    # Run examples
    example_basic()

    # Uncomment to run more examples:
    # example_with_beta()
    # example_with_bonus_matrix()
