"""Two-strand CPLfold: Numba search, reads guidance, and two-phase pseudoknots.

Nested candidates use Turner cofold energies. Connected pseudoknots use the
explicitly experimental Turner/DP extension described by ``energy_metadata``;
its ranking energy must not be presented as a validated binding free energy.
"""

import math

from ..sequence import add_strand_break
from .cofold_parser import BeamCKYCofoldParser, _INF
from .errors import InputError
from .validation import BonusLimits, FoldInputs

BONUS_LIMITS = BonusLimits(element=None, search=_INF)


def _finite(value, name):
    """Reject arithmetic overflow before a candidate can enter ranking/JSON."""
    try:
        value = float(value)
    except (TypeError, ValueError, OverflowError) as error:
        raise InputError(f"{name} exceeds the finite numeric range") from error
    if not math.isfinite(value):
        raise InputError(f"{name} exceeds the finite numeric range")
    return value


def run(inputs: FoldInputs, *, output_file=None, verbose=False):
    """The two-strand engine: two-phase cofold search of validated inputs.

    Every generated candidate is scored as a whole. Alpha changes both phases'
    search objectives, E - alpha * pair support. Beta retains the original
    CPLfold ranking rule for crossing candidates:
    E_effective = E_merged + beta * E_phase1. It does not alter physical energy.
    Each Phase 1 structure gets up to ``max_phase2`` constrained candidates.
    Candidates with the same pair set keep the best (effective energy, score);
    results are sorted by (effective energy, score, structure).
    """
    seq, cutpoint, energy_model = inputs.sequence, inputs.cutpoint, inputs.energy_model
    beam_size, max_phase1, max_phase2 = inputs.beam_size, inputs.max_phase1, inputs.max_phase2
    energy_delta, alpha, beta = inputs.energy_delta, inputs.alpha, inputs.beta
    bonus_matrix, allow_pseudoknot = inputs.bonus, inputs.pseudoknots

    from .cofold_energy import CofoldEnergy
    from .single import (TURNER_MODEL, candidate, get_pairs_from_structure, is_pseudoknot,
                         merge_structures, output_results, read_support, structure_to_constraint)

    def make_parser(association_cost=4.1):
        parser = BeamCKYCofoldParser(
            beam_size=beam_size, lv=True, use_constraints=True,
            is_verbose=False, cutpoint=cutpoint, duplex_init=association_cost,
        )
        parser.set_alpha(alpha)
        if bonus_matrix is not None:
            parser.set_bonus_matrix(bonus_matrix, len(seq))
        return parser

    evaluator = CofoldEnergy()
    candidates = {}

    def evaluate(structure):
        return evaluator.compute_energy(seq, structure, cutpoint, model=energy_model)

    def retain(structure, scored, phase1_structure, phase1_energy,
               phase2_structure, search_score, association_cost=None):
        pairs = sorted(get_pairs_from_structure(structure))
        key = tuple(pairs)
        crossing = phase2_structure is not None
        energy = _finite(scored["energy"], "Physical energy")
        phase1_energy = _finite(phase1_energy, "Phase 1 energy")
        search_score = _finite(search_score, "Search score")
        effective = _finite(energy + beta * phase1_energy, "Effective energy") if crossing else energy
        support = read_support(bonus_matrix, pairs)
        details = {
            "strand_lengths": [cutpoint, len(seq) - cutpoint],
            "intermolecular_pairs": sum(i < cutpoint <= j for i, j in pairs),
            "read_bonus": 0.0 if alpha == 0.0 else _finite(alpha * support, "Read bonus"),
            "energy_metadata": dict(scored.get("metadata", {})),
            "energy_breakdown": dict(scored.get("breakdown", {})),
        }
        if crossing:
            details["phase2_association_cost"] = association_cost
        result = candidate(
            add_strand_break(structure, cutpoint), energy, effective, "pseudoknot" if crossing else "phase1",
            search_score, add_strand_break(phase1_structure, cutpoint), phase1_energy,
            add_strand_break(phase2_structure, cutpoint) if crossing else None, cutpoint, pairs,
            # Nested cofold energies are Turner terms whatever the model; crossing ones use its DP terms.
            energy_model if crossing else TURNER_MODEL, support, details)
        previous = candidates.get(key)
        if previous is None or (effective, search_score) < (previous["effective_energy"], previous["score"]):
            candidates[key] = result

    if verbose:
        print(f"Running CPLcofold: {cutpoint} + {len(seq) - cutpoint} nt; beam={beam_size}; alpha={alpha}; beta={beta}")
    parser = make_parser()
    first_candidates = parser.parse_subopt(seq, energy_delta=energy_delta, max_structures=max_phase1)
    # Avoid retaining the first inside chart during subsequent constrained folds.
    del parser
    for index, (first, first_score) in enumerate(first_candidates):
        scored_first = evaluate(first)
        first_energy = float(scored_first["energy"])
        retain(first, scored_first, first, first_energy, None, first_score)
        if not allow_pseudoknot:
            continue
        first_pairs = get_pairs_from_structure(first)
        # Once Phase 1 connects the strands, Phase 2 searches in a bound context.
        association_cost = 0.0 if any(i < cutpoint <= j for i, j in first_pairs) else 4.1
        parser = make_parser(association_cost)
        second_candidates = parser.parse_subopt(
            seq, cons=structure_to_constraint(first), energy_delta=energy_delta,
            max_structures=max_phase2,
        )
        del parser
        for second, second_score in second_candidates:
            second_pairs = get_pairs_from_structure(second)
            if not is_pseudoknot(first_pairs, second_pairs):
                continue
            merged = merge_structures(first, second)
            if merged is not None:
                retain(merged, evaluate(merged), first, first_energy, second,
                       first_score + second_score, association_cost)
        if verbose:
            print(f"  Phase 1 candidate {index + 1}/{len(first_candidates)}: searched {len(second_candidates)} constrained candidates")

    results = sorted(candidates.values(), key=lambda item: (item["effective_energy"], item["score"], item["structure"]))
    output_results(results, add_strand_break(seq, cutpoint), output_file, verbose,
                   allow_pseudoknot=allow_pseudoknot)
    return results


if __name__ == "__main__":
    from ..cli import cofold_main
    raise SystemExit(cofold_main())
