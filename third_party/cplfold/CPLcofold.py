"""Two-strand CPLfold: Numba search, reads guidance, and two-phase pseudoknots.

Nested candidates use Turner cofold energies. Connected pseudoknots use the
explicitly experimental Turner/DP extension described by ``energy_metadata``;
its ranking energy must not be presented as a validated binding free energy.
"""

import math
from numbers import Integral

import numpy as np

from Utils.strand_layout import add_strand_break, normalise_sequence
from Utils.cofold_options import mode_options


class CofoldInputError(ValueError):
    """Expected input/range errors that CLI callers can report without traceback."""


def _positive_integer(value, name, allow_zero=False):
    minimum = 0 if allow_zero else 1
    if isinstance(value, bool) or not isinstance(value, Integral) or value < minimum:
        raise CofoldInputError(f"{name} must be an integer >= {minimum}")
    return int(value)


def _nonnegative(value, name):
    if isinstance(value, (bool, np.bool_)):
        raise CofoldInputError(f"{name} must be finite and nonnegative")
    try:
        value = float(value)
    except (TypeError, ValueError, OverflowError) as error:
        raise CofoldInputError(f"{name} must be finite and nonnegative") from error
    if not math.isfinite(value) or value < 0:
        raise CofoldInputError(f"{name} must be finite and nonnegative")
    return value


def _finite(value, name):
    """Reject arithmetic overflow before a candidate can enter ranking/JSON."""
    try:
        value = float(value)
    except (TypeError, ValueError, OverflowError) as error:
        raise CofoldInputError(f"{name} exceeds the finite numeric range") from error
    if not math.isfinite(value):
        raise CofoldInputError(f"{name} exceeds the finite numeric range")
    return value


def _bonus_matrix(value, length):
    if value is None:
        return None
    if np.iscomplexobj(value):
        raise CofoldInputError("Bonus matrix must contain real finite values")
    try:
        matrix = np.asarray(value, dtype=np.float64)
    except (TypeError, ValueError, OverflowError) as error:
        raise CofoldInputError("Bonus matrix must contain real finite values") from error
    if matrix.ndim == 1 and matrix.size == length * length:
        matrix = matrix.reshape(length, length)
    if matrix.shape != (length, length):
        raise CofoldInputError("Bonus matrix must be N by N, where N excludes the '&' separator")
    if not np.isfinite(matrix).all():
        raise CofoldInputError("Bonus matrix must contain only finite values")
    if not np.allclose(matrix, matrix.T, rtol=0, atol=1e-7):
        raise CofoldInputError("Bonus matrix must be symmetric")
    return np.ascontiguousarray(matrix)


def cofold(sequence_a, sequence_b, *, mode=None, **kwargs):
    """Fold two RNA molecules, retaining alpha reads guidance and beta ranking.

    Strands are given in their own 5'-to-3' orientations. Returned structures
    contain ``&``; bonus coordinates refer to A+B with no separator. Inputs
    remain in the caller's order, including two identical homodimer strands.
    ``mode='standard'`` disables pseudoknots and defaults to one candidate with
    zero energy window; explicit max_phase1/energy_delta override those defaults.
    ``mode='pseudoknot'`` explicitly enables Phase 2. Omitting mode preserves
    the existing API defaults. A contradictory allow_pseudoknot is rejected.
    """
    first, first_cut = normalise_sequence(sequence_a)
    second, second_cut = normalise_sequence(sequence_b)
    if first_cut is not None or second_cut is not None or "cutpoint" in kwargs:
        raise CofoldInputError("cofold(a, b) takes two individual strands without a cutpoint")
    return two_phase_cofold(first + second, cutpoint=len(first), **mode_options(mode, kwargs))


def _validate_search_bonus(matrix, alpha, impossible):
    """Check the parser's existing numeric range before starting its search."""
    if matrix is None or alpha == 0.0 or not np.any(matrix):
        return
    try:
        with np.errstate(over="raise", invalid="raise"):
            scaled = matrix * alpha
            scaled *= 100.0
    except FloatingPointError as error:
        raise CofoldInputError("Scaled bonus exceeds the finite numeric range") from error
    if np.max(np.abs(scaled)) >= impossible / (4.0 * max(1, len(matrix) // 2)):
        raise CofoldInputError("Scaled bonus total exceeds the parser numeric range")


def two_phase_cofold(seq, cutpoint=None, beam_size=100, energy_delta=5.0,
                     max_phase1=10, max_phase2=5, energy_model="DP09",
                     output_file=None, verbose=True, lv=True, bonus_matrix=None,
                     alpha=0.0, beta=0.0, allow_pseudoknot=True):
    """Two-phase cofold search; every generated candidate is scored as a whole.

    Alpha changes both phases' search objectives, E - alpha * pair support.
    Beta retains the original CPLfold ranking rule for crossing candidates:
    E_effective = E_merged + beta * E_phase1. It does not alter physical energy.
    ``max_phase2`` is effective here, unlike the legacy single-strand path.
    """
    seq, cutpoint = normalise_sequence(seq, cutpoint)
    if cutpoint is None:
        raise CofoldInputError("Cofold requires two strands: provide '&' or cutpoint")
    if not lv:
        raise CofoldInputError("Cofold currently supports Vienna/Turner mode only (lv=True)")
    if not isinstance(allow_pseudoknot, bool):
        raise CofoldInputError("allow_pseudoknot must be a boolean")
    energy_model = str(energy_model).upper()
    if energy_model not in {"DP03", "DP09"}:
        raise CofoldInputError("Cofold supports DP03 or DP09; CC/RE nicked pseudoloop models are not implemented")
    beam_size = _positive_integer(beam_size, "beam_size", allow_zero=True)
    max_phase1 = _positive_integer(max_phase1, "max_phase1")
    max_phase2 = _positive_integer(max_phase2, "max_phase2")
    energy_delta = _nonnegative(energy_delta, "energy_delta")
    alpha = _nonnegative(alpha, "alpha")
    beta = _nonnegative(beta, "beta")
    bonus_matrix = _bonus_matrix(bonus_matrix, len(seq))

    # Lazy imports keep the established single-strand startup/JIT path intact.
    from CPLcofold_parser import BeamCKYCofoldParser, _INF
    from Utils.cofold_energy import CofoldEnergy
    from CPLfold import (get_pairs_from_structure, is_pseudoknot,
                         merge_structures, output_results, structure_to_constraint)

    _validate_search_bonus(bonus_matrix, alpha, _INF)

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
        try:
            support = math.fsum(float(bonus_matrix[i, j]) for i, j in pairs) if bonus_matrix is not None else 0.0
        except OverflowError as error:
            raise ValueError("Read support exceeds the finite numeric range") from error
        support = _finite(support, "Read support")
        read_bonus = 0.0 if alpha == 0.0 else _finite(alpha * support, "Read bonus")
        candidate = {
            "structure": add_strand_break(structure, cutpoint),
            "energy": energy,
            "effective_energy": effective,
            "type": "pseudoknot" if crossing else "phase1",
            "phase1_struct": add_strand_break(phase1_structure, cutpoint),
            "phase1_energy": phase1_energy,
            "phase2_struct": add_strand_break(phase2_structure, cutpoint) if crossing else None,
            "score": search_score,
            "cutpoint": cutpoint,
            "strand_lengths": [cutpoint, len(seq) - cutpoint],
            "intermolecular_pairs": sum(i < cutpoint <= j for i, j in pairs),
            "read_support": support,
            "read_bonus": read_bonus,
            "alpha": alpha,
            "beta": beta,
            "energy_metadata": dict(scored.get("metadata", {})),
            "energy_breakdown": dict(scored.get("breakdown", {})),
        }
        if crossing:
            candidate["phase2_association_cost"] = association_cost
        previous = candidates.get(key)
        if previous is None or (effective, search_score) < (previous["effective_energy"], previous["score"]):
            candidates[key] = candidate

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
    from CPLfold import main
    raise SystemExit(main(cofold_entry=True))
