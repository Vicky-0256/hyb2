#!/usr/bin/env python3
"""
CPLfold - COMRADES-guided Pseudoknot LinearFold

A two-phase algorithm for RNA secondary structure prediction with pseudoknots,
integrating experimental COMRADES/PARIS data as folding constraints.

Algorithm:
1. Phase 1: Generate suboptimal structures using LinearFold with bonus matrix
2. Phase 2: For each Phase 1 structure, mask paired positions and fold again
3. Merge Phase 1 and Phase 2 structures to form pseudoknots
4. Rank the candidates of a run with one energy model, without the bonus: Turner 2004 when
   pseudoknots are off; the HotKnots-derived DP/CC/RE evaluator when they are on

Key Parameters:
- alpha: Scaling factor for COMRADES/PARIS bonus matrix (0.0-1.0)
- beta: Pseudoknot preference factor for ranking (0.0-1.0)

Dependencies:
- LinearFold: https://github.com/LinearFold/LinearFold
  Huang et al. (2019) Bioinformatics 35(14):i295-i304
- HotKnots 2.0: source code and parameters for the Python energy-calculation port;
  its structure-search algorithm and native executable are not runtime dependencies
  https://www.cs.ubc.ca/labs/algorithms/Software/HotKnots/
  Ren et al. (2005) RNA 11(10):1494-1504

Author: Ke Wang
"""

import sys
import os
import argparse
import math
from pathlib import Path
from typing import List, Dict, Tuple, Optional

import numpy as np

# Import CPLfold parser
from .parser import BeamCKYParserHyper, VALUE_MIN_FLOAT
from .validation import CofoldInputError, _validate_search_bonus, validate_fold_inputs

from ..models import COFOLD_MODELS, DEFAULT_MODELS
from .hotknots_energy import HotKnotsEnergy
from .turner_energy import turner_energy
from ..sequence import normalise_sequence


def _structure_layout(structure: str) -> Tuple[str, Optional[int], List[Tuple[int, int]]]:
    """Validate supported dot-bracket notation and remove its display nick.

    Bracket families are balanced independently, so pseudoknots such as
    ``([)]`` remain valid. Empty compact structures retain legacy behavior.
    """
    if not isinstance(structure, str):
        raise ValueError("Structure must be a string")
    boundary = None
    if '&' in structure:
        if structure.count('&') != 1:
            raise ValueError("Structure accepts at most one '&' strand separator")
        boundary = structure.index('&')
        if boundary == 0 or boundary == len(structure) - 1:
            raise ValueError("Structure strand separator must separate two nonempty strands")
        structure = structure.replace('&', '')

    pairs = []
    stacks = {'(': [], '[': [], '{': []}
    closing = {')': '(', ']': '[', '}': '{'}
    for index, symbol in enumerate(structure):
        if symbol in stacks:
            stacks[symbol].append(index)
        elif symbol in closing:
            stack = stacks[closing[symbol]]
            if not stack:
                raise ValueError("Unbalanced structure brackets")
            pairs.append((stack.pop(), index))
        elif symbol != '.':
            raise ValueError("Structure accepts only '.', '()', '[]', '{}' and '&'")
    if any(stacks.values()):
        raise ValueError("Unbalanced structure brackets")
    return structure, boundary, pairs


def structure_to_constraint(structure: str) -> str:
    """Return a compact Phase 2 mask for a compact or strand-marked structure.

    All paired positions in ``()``, ``[]`` and ``{}`` become ``.`` (forced
    unpaired); dots become ``?`` (free). The optional ``&`` is omitted because
    constraints use nucleotide coordinates. Malformed input raises ValueError.
    """
    compact, _, _ = _structure_layout(structure)
    return ''.join('?' if symbol == '.' else '.' for symbol in compact)


def get_pairs_from_structure(structure: str) -> List[Tuple[int, int]]:
    """
    Extract compact zero-based nucleotide pairs from a structure string.

    Args:
        structure: Dot-bracket structure (supports (), [], {}), optionally
                   containing one internal '&' strand separator.

    Returns:
        List of (i, j) pairs where i < j. '&' never occupies a coordinate.
        Malformed separators, unsupported symbols or unbalanced brackets
        raise ValueError. Valid single-strand pair ordering is unchanged.
    """
    return _structure_layout(structure)[2]


def has_new_pairs(structure: str) -> bool:
    """
    Check if a structure has any base pairs.

    Args:
        structure: Dot-bracket structure

    Returns:
        True if structure contains at least one pair
    """
    return '(' in structure or '[' in structure or '{' in structure


def pairs_conflict(pairs1: List[Tuple[int, int]], pairs2: List[Tuple[int, int]]) -> bool:
    """
    Check if two sets of pairs conflict (same position paired differently).

    Args:
        pairs1: First set of pairs
        pairs2: Second set of pairs

    Returns:
        True if there's a conflict
    """
    paired1 = {}
    for i, j in pairs1:
        paired1[i] = j
        paired1[j] = i

    for i, j in pairs2:
        if i in paired1 and paired1[i] != j:
            return True
        if j in paired1 and paired1[j] != i:
            return True

    return False


def is_pseudoknot(pairs1: List[Tuple[int, int]], pairs2: List[Tuple[int, int]]) -> bool:
    """
    Check if two sets of pairs form a pseudoknot.
    A pseudoknot occurs when pairs cross: i < k < j < l where (i,j) and (k,l) are pairs.

    Args:
        pairs1: First set of pairs (from Phase 1)
        pairs2: Second set of pairs (from Phase 2)

    Returns:
        True if pairs form a pseudoknot
    """
    for i1, j1 in pairs1:
        for i2, j2 in pairs2:
            # Check for crossing: i1 < i2 < j1 < j2 or i2 < i1 < j2 < j1
            if i1 < i2 < j1 < j2 or i2 < i1 < j2 < j1:
                return True
    return False


def merge_structures(struct1: str, struct2: str) -> Optional[str]:
    """
    Merge Phase 1 and Phase 2 structures into a pseudoknotted structure.

    New Phase 2 pairs are converted to [] notation. Compact and strand-marked
    inputs may be mixed: their nucleotide lengths must match, and a boundary
    supplied by either input is preserved. Two explicit boundaries must agree.

    Args:
        struct1: Phase 1 structure (uses '(' and ')')
        struct2: Phase 2 structure (uses '(' and ')')

    Returns:
        Merged structure, or None for malformed input, conflicting pairs or
        boundaries, unequal nucleotide lengths, or a bracket collision that
        cannot preserve the pair graph using the existing [] merge convention.
    """
    try:
        compact1, boundary1, pairs1 = _structure_layout(struct1)
        compact2, boundary2, pairs2 = _structure_layout(struct2)
    except ValueError:
        return None
    if len(compact1) != len(compact2):
        return None
    if boundary1 is not None and boundary2 is not None and boundary1 != boundary2:
        return None
    boundary = boundary1 if boundary1 is not None else boundary2

    # Check for conflicts
    if pairs_conflict(pairs1, pairs2):
        return None

    # Build merged structure
    result = list(compact1)  # Work only in nucleotide coordinates.

    # Add Phase 2 pairs using [] notation
    for i, j in pairs2:
        if result[i] == '.' and result[j] == '.':
            result[i] = '['
            result[j] = ']'

    merged = ''.join(result)
    # Existing [] pairs or a pseudoknotted second input must not silently be
    # re-paired when all newly added pairs use the Phase 2 bracket family.
    if set(get_pairs_from_structure(merged)) != set(pairs1) | set(pairs2):
        return None
    if boundary is not None:
        merged = merged[:boundary] + '&' + merged[boundary:]
    return merged


def phase1_fold(seq: str, parser: BeamCKYParserHyper,
                energy_delta: float = 5.0,
                max_structures: int = 10) -> List[Tuple[str, float]]:
    """
    Phase 1: Generate suboptimal structures using LinearFold.

    Args:
        seq: RNA sequence
        parser: LinearFold parser instance
        energy_delta: Energy range for suboptimal structures
        max_structures: Maximum number of structures to return

    Returns:
        List of (structure, score) tuples
    """
    results = parser.parse_subopt(seq, energy_delta=energy_delta, max_structures=max_structures)
    return results[:max_structures]


def phase2_fold(seq: str, parser: BeamCKYParserHyper,
                constraint: str) -> Tuple[Optional[str], float]:
    """
    Phase 2: Fold with constraints to find complementary structure.

    Args:
        seq: RNA sequence
        parser: LinearFold parser instance (must have use_constraints=True)
        constraint: Constraint string from structure_to_constraint()

    Returns:
        Tuple of (structure, score) or (None, 0) if failed
    """
    result = parser.parse(seq, cons=constraint)
    if result and result[0]:
        return result[0], result[1]
    return None, 0.0


def compute_energy_hotknots(seq: str, structure: str, hk: HotKnotsEnergy,
                             model: str = "DP09") -> float:
    """
    Compute structure energy using the Python port of HotKnots' energy evaluator.

    The implementation is derived from HotKnots 2.0 energy-calculation code and
    parameters, but it does not invoke the HotKnots package or executable.

    Args:
        seq: RNA sequence
        structure: Dot-bracket structure (can include [] for pseudoknots)
        hk: HotKnotsEnergy instance
        model: Energy model (DP03, DP09, CC06, CC09, RE)

    Returns:
        Energy in kcal/mol
    """
    result = hk.compute_energy(seq, structure, model=model)
    return float(result['energy'])


def output_results(all_structures: List[Dict], seq: str,
                   output_file: Optional[str], verbose: bool = True,
                   allow_pseudoknot: bool = True) -> None:
    """
    Output results to stdout and optionally to file.

    Args:
        all_structures: List of structure dictionaries
        seq: Original RNA sequence
        output_file: Optional file path for output
        verbose: Whether to print to stdout
        allow_pseudoknot: Whether pseudoknot candidates were searched
    """
    lines = []

    lines.append(f"Sequence: {seq}")
    lines.append(f"Length: {len(seq.replace('&', ''))} nt")
    if '&' in seq:
        lines.append("Strand lengths: " + " + ".join(str(len(part)) for part in seq.split('&')) + " nt")
        lines.append("Cofold secondary structures: Turner 37 C; alpha guides search, beta adjusts pseudoknot ranking.")
        if any(s.get('energy_metadata', {}).get('energy_kind') == 'heuristic' for s in all_structures):
            lines.append("Pseudoknot ranking uses experimental Turner/DP cofold scores; these are not validated binding free energies.")
    lines.append("=" * 60)
    lines.append("")

    # Group by type
    phase1_structs = [s for s in all_structures if s['type'] == 'phase1']
    pk_structs = [s for s in all_structures if s['type'] == 'pseudoknot']

    secondary_label = "Phase 1 Structures" if allow_pseudoknot else "Secondary Structures"
    secondary_id = "P1" if allow_pseudoknot else "S"

    lines.append(f"{secondary_label}: {len(phase1_structs)}")
    if allow_pseudoknot:
        lines.append(f"Pseudoknot Structures: {len(pk_structs)}")
    else:
        lines.append("Pseudoknot Structures: disabled")
    lines.append("")

    # Output ordinary secondary structures
    if phase1_structs:
        lines.append(f"--- {secondary_label} ---")
        for i, s in enumerate(phase1_structs, 1):
            energy_str = f"{s['energy']:.2f}" if s['energy'] is not None else "N/A"
            lines.append(f"{secondary_id}-{i}: {s['structure']}")
            lines.append(f"       Energy: {energy_str} kcal/mol")
        lines.append("")

    # Output pseudoknot structures
    if pk_structs:
        lines.append("--- Pseudoknot Structures ---")
        for i, s in enumerate(pk_structs, 1):
            energy_str = f"{s['energy']:.2f}" if s['energy'] is not None else "N/A"
            lines.append(f"PK-{i}: {s['structure']}")
            lines.append(f"       Energy: {energy_str} kcal/mol")
            if s.get('phase1_struct'):
                lines.append(f"       Phase 1: {s['phase1_struct']}")
            if s.get('phase2_struct'):
                lines.append(f"       Phase 2: {s['phase2_struct']}")
        lines.append("")

    # Best structures summary
    lines.append("=" * 60)
    lines.append("FINAL RESULTS (sorted by effective energy)")
    lines.append("=" * 60)

    if all_structures:
        # Best overall structure
        best = all_structures[0]
        energy_str = f"{best['energy']:.2f}" if best['energy'] is not None else "N/A"
        lines.append("")
        lines.append("Best Overall Structure:")
        lines.append(f"  {best['structure']}")
        lines.append(f"  Energy: {energy_str} kcal/mol")
        # Show effective energy if different from actual energy
        if best.get('effective_energy') is not None and best['effective_energy'] != best['energy']:
            eff_str = f"{best['effective_energy']:.2f}"
            lines.append(f"  Effective Energy: {eff_str} kcal/mol")
        lines.append(f"  Type: {best['type']}")

        # Best pseudoknot structure (if different from best overall)
        if pk_structs:
            # Sort pseudoknots by effective energy
            pk_sorted = sorted(pk_structs, key=lambda x: x['effective_energy'] if x.get('effective_energy') is not None else float('inf'))
            best_pk = pk_sorted[0]
            pk_energy_str = f"{best_pk['energy']:.2f}" if best_pk['energy'] is not None else "N/A"

            lines.append("")
            lines.append("Best Pseudoknot Structure:")
            lines.append(f"  {best_pk['structure']}")
            lines.append(f"  Energy: {pk_energy_str} kcal/mol")
            # Show effective energy if different
            if best_pk.get('effective_energy') is not None and best_pk['effective_energy'] != best_pk['energy']:
                pk_eff_str = f"{best_pk['effective_energy']:.2f}"
                lines.append(f"  Effective Energy: {pk_eff_str} kcal/mol")

            # Compare with best overall
            if best['type'] == 'pseudoknot':
                lines.append("  ** This IS the best overall structure **")
            else:
                if best['energy'] is not None and best_pk['energy'] is not None:
                    diff = best_pk['energy'] - best['energy']
                    heuristic = any(s.get('energy_metadata', {}).get('energy_kind') == 'heuristic'
                                    for s in (best, best_pk))
                    label = "Model score difference" if heuristic else "Energy difference"
                    lines.append(f"  {label} from best: {diff:+.2f} kcal/mol")
                    if heuristic:
                        lines.append("  (Experimental cofold pseudoknot scores do not establish relative thermodynamic stability)")
                    elif diff > 0:
                        lines.append("  (Pseudoknot is less stable than non-pseudoknot structure)")
                    else:
                        lines.append("  (Pseudoknot is more stable)")
    else:
        lines.append("No structures found.")

    output_text = '\n'.join(lines)

    # Print to stdout
    if verbose:
        print(output_text)

    # Write to file
    if output_file:
        with open(output_file, 'w') as f:
            f.write(output_text)
            f.write('\n')
        if verbose:
            print(f"\nResults saved to: {output_file}")


def two_phase_pseudoknot_fold(seq: str,
                               beam_size: int = 100,
                               energy_delta: float = 5.0,
                               max_phase1: int = 10,
                               max_phase2: int = 5,
                               energy_model: Optional[str] = None,
                               output_file: Optional[str] = None,
                               verbose: bool = True,
                               lv: bool = True,
                               bonus_matrix = None,
                               alpha: float = 0.0,
                               beta: float = 0.0,
                               allow_pseudoknot: bool = True,
                               cutpoint: Optional[int] = None) -> List[Dict]:
    """
    Main function: Two-phase pseudoknot prediction algorithm.

    Args:
        seq: RNA sequence
        beam_size: LinearFold beam size
        energy_delta: Energy range for suboptimal structures
        max_phase1: Maximum Phase 1 structures to process
        max_phase2: Maximum Phase 2 structures per Phase 1
        energy_model: Pseudoknot energy model (DP03, DP09, CC06, CC09, RE); used only when
                      allow_pseudoknot is True. Pseudoknot-free runs rank with Turner 2004.
        output_file: Optional output file path
        verbose: Whether to print progress
        lv: Whether to use Vienna mode (True) or CONTRAfold mode (False)
        bonus_matrix: Optional PARIS/COMRADES bonus matrix (numpy array)
        alpha: Scaling factor for bonus matrix (0.0-1.0)
        beta: Pseudoknot energy bonus factor (0.0-1.0). For pseudoknot structures,
              the sorting energy = pseudoknot_energy + beta * phase1_energy.
              Since RNA energies are negative, this makes pseudoknots more
              favorable in ranking, improving recall of pseudoknot structures.
        allow_pseudoknot: Whether to run Phase 2 and return pseudoknot candidates.
                         Defaults to True. When False, only Phase 1 secondary
                         structures are generated and returned.
        cutpoint: Optional length of strand A for two-strand cofolding. The
                  sequence may alternatively contain one '&'. Bonus matrices
                  exclude the separator. Cofold preserves alpha and beta;
                  cofold pseudoknot energies are explicitly experimental.

    Returns:
        List of structure dictionaries sorted by effective energy
    """
    cofold = cutpoint is not None or (isinstance(seq, str) and '&' in seq)
    if energy_model is None:
        energy_model = DEFAULT_MODELS["cplcofold" if cofold else "cplfold"]
    if cofold:
        from .cofold import two_phase_cofold
        return two_phase_cofold(
            seq, cutpoint=cutpoint, beam_size=beam_size, energy_delta=energy_delta,
            max_phase1=max_phase1, max_phase2=max_phase2, energy_model=energy_model,
            output_file=output_file, verbose=verbose, lv=lv, bonus_matrix=bonus_matrix,
            alpha=alpha, beta=beta, allow_pseudoknot=allow_pseudoknot,
        )
    # The same normalisation and checks as the cofold route (CofoldInputError, a ValueError).
    inputs = validate_fold_inputs(
        seq, None, two_strands=False, lv=lv, beam_size=beam_size, energy_delta=energy_delta,
        max_phase1=max_phase1, max_phase2=max_phase2, energy_model=energy_model,
        bonus_matrix=bonus_matrix, alpha=alpha, beta=beta, allow_pseudoknot=allow_pseudoknot,
    )
    seq, energy_model = inputs["seq"], inputs["energy_model"]
    beam_size, max_phase1, max_phase2 = inputs["beam_size"], inputs["max_phase1"], inputs["max_phase2"]
    energy_delta, alpha, beta = inputs["energy_delta"], inputs["alpha"], inputs["beta"]
    bonus_matrix = inputs["bonus_matrix"]
    # This parser narrows the bonus to float32 and keeps scores above VALUE_MIN_FLOAT.
    if bonus_matrix is not None and np.max(np.abs(bonus_matrix)) > np.finfo(np.float32).max:
        raise CofoldInputError("Bonus matrix exceeds the supported float32 range")
    _validate_search_bonus(bonus_matrix, alpha, -VALUE_MIN_FLOAT)

    if verbose:
        print("Running CPLfold RNA structure prediction...")
        print(f"Sequence length: {len(seq)}")
        print(f"Beam size: {beam_size}")
        print(f"Energy delta: {energy_delta}")
        print(f"Ranking energy model: {energy_model if allow_pseudoknot else 'Turner 2004'}")
        print(f"LinearFold mode: {'Vienna' if lv else 'CONTRAfold'}")
        print(f"Structure mode: {'Pseudoknot-enabled' if allow_pseudoknot else 'Pseudoknot-free'}")
        if bonus_matrix is not None:
            print(f"Using bonus matrix with alpha: {alpha}")
        if allow_pseudoknot and beta != 0.0:
            print(f"Pseudoknot energy bonus (beta): {beta}")
        elif not allow_pseudoknot and beta != 0.0:
            print("Ignoring beta because pseudoknot generation is disabled")
        print("")

    # Constraints are needed only by the pseudoknot-enabled Phase 2.
    parser = BeamCKYParserHyper(
        beam_size=beam_size,
        lv=lv,  # Vienna mode (True) or CONTRAfold mode (False)
        use_constraints=allow_pseudoknot,
        is_verbose=False
    )
    
    # Set bonus matrix if provided
    if bonus_matrix is not None:
        parser.set_alpha(alpha)
        parser.set_bonus_matrix(bonus_matrix, len(seq))

    # Load CPLfold's packaged DP/CC/RE parameters once.
    hk = HotKnotsEnergy()

    all_structures = []
    seen_structures = set()  # Avoid duplicates

    # Phase 1: Get suboptimal structures
    if verbose:
        print("Phase 1: Generating suboptimal structures...")

    phase1_results = phase1_fold(seq, parser, energy_delta, max_phase1)

    if verbose:
        print(f"  Found {len(phase1_results)} Phase 1 structures")

    for idx, (struct1, score1) in enumerate(phase1_results):
        if verbose:
            print(f"\nProcessing Phase 1 structure {idx + 1}/{len(phase1_results)}")
            print(f"  Structure: {struct1}")

        # Skip if already seen
        if struct1 in seen_structures:
            continue
        seen_structures.add(struct1)

        # One model ranks all candidates of a run: the pseudoknot model when merged candidates
        # can occur, Turner 2004 (the search's own nested model) when they cannot.
        energy1 = (compute_energy_hotknots(seq, struct1, hk, energy_model) if allow_pseudoknot
                   else turner_energy(seq, struct1))

        all_structures.append({
            'structure': struct1,
            'energy': energy1,
            'effective_energy': energy1,  # For phase1, effective = actual energy
            'type': 'phase1',
            'phase1_struct': struct1,
            'phase1_energy': energy1,
            'phase2_struct': None,
            'score': score1
        })

        if verbose:
            print(f"  Energy: {energy1:.2f} kcal/mol")

        if not allow_pseudoknot:
            continue

        # Phase 2: Generate constraint and fold
        constraint = structure_to_constraint(struct1)

        if verbose:
            print(f"  Phase 2: Folding with constraints...")
            print(f"  Constraint: {constraint}")

        struct2, score2 = phase2_fold(seq, parser, constraint)

        if struct2 and has_new_pairs(struct2):
            if verbose:
                print(f"  Phase 2 structure: {struct2}")

            # Get pairs for pseudoknot check
            pairs1 = get_pairs_from_structure(struct1)
            pairs2 = get_pairs_from_structure(struct2)

            if verbose:
                print(f"  Phase 1 pairs: {len(pairs1)}, Phase 2 pairs: {len(pairs2)}")

            # Check if it forms a pseudoknot
            if is_pseudoknot(pairs1, pairs2):
                # Merge structures
                merged = merge_structures(struct1, struct2)

                if verbose:
                    print(f"  Merged structure: {merged}")

                if (
                    merged
                    and merged not in seen_structures
                ):
                    # Calculate merged structure energy
                    energy_merged = compute_energy_hotknots(seq, merged, hk, energy_model)
                    seen_structures.add(merged)

                    # Calculate effective energy for sorting
                    # effective_energy = pseudoknot_energy + beta * phase1_energy
                    # Since energies are negative, this makes pseudoknots more favorable
                    effective_energy = energy_merged + beta * energy1

                    all_structures.append({
                        'structure': merged,
                        'energy': energy_merged,
                        'effective_energy': effective_energy,
                        'type': 'pseudoknot',
                        'phase1_struct': struct1,
                        'phase1_energy': energy1,
                        'phase2_struct': struct2,
                        'score': score1 + score2
                    })

                    if verbose:
                        energy_str = f"{energy_merged:.2f}" if energy_merged is not None else "N/A"
                        print(f"  ** Pseudoknot found! **")
                        print(f"  Pseudoknot energy: {energy_str} kcal/mol")
                        if beta != 0.0 and effective_energy is not None:
                            print(f"  Effective energy (with beta={beta}): {effective_energy:.2f} kcal/mol")
                elif merged in seen_structures:
                    if verbose:
                        print(f"  (Duplicate structure, skipped)")
            else:
                if verbose:
                    print(f"  No pseudoknot formed (pairs don't cross)")
        else:
            if verbose:
                if struct2:
                    print(f"  Phase 2 structure: {struct2}")
                    print(f"  No new pairs found in Phase 2 (all positions unpaired)")
                else:
                    print(f"  Phase 2 folding returned no structure")

    # Sort by effective energy (None values go to end)
    # For pseudoknots with beta > 0, effective_energy = energy + beta * phase1_energy
    # This makes pseudoknots more favorable since energies are negative
    all_structures.sort(key=lambda x: x['effective_energy'] if x['effective_energy'] is not None else float('inf'))

    # Output results
    if verbose:
        print("\n")
    output_results(all_structures, seq, output_file, verbose, allow_pseudoknot=allow_pseudoknot)

    return all_structures


def cofold(sequence_a: str, sequence_b: str, **kwargs) -> List[Dict]:
    """Predict two strands with CPLfold's alpha-guided two-phase/beta workflow."""
    from .cofold import cofold as predict_cofold
    return predict_cofold(sequence_a, sequence_b, **kwargs)
