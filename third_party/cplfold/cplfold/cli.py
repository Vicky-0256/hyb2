"""Command-line interface for CPLfold (``cplfold``) and CPLcofold (``cplcofold``).

Run as ``python -m cplfold.cli`` or through the ``cplfold`` console script.
"""

import sys
import argparse
import math
from pathlib import Path


from .bonus import load_bonus_matrix_file
from .models import COFOLD_MODELS, DEFAULT_MODELS
from .sequence import normalise_sequence
from ._internal.hotknots_energy import HotKnotsEnergy
from ._internal.single import two_phase_pseudoknot_fold
from ._internal.cofold import CofoldInputError


def main(argv=None, *, cofold_entry=False):
    """Shared CLI; the dedicated entry requires two strands and cofold defaults."""
    from ._internal.cli_input import read_sequence_file
    from ._internal.cofold_options import mode_options

    parser = argparse.ArgumentParser(
        prog="cplcofold" if cofold_entry else "cplfold",
        description=("Two-strand CPLcofold prediction (default mode: pseudoknot)."
                     if cofold_entry else "CPLfold single-strand and two-strand prediction (default mode: pseudoknot)."),
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=f"""
Examples:
  bin/cplcofold --sequence 'GGG&CCC' --mode standard
  bin/cplcofold --sequence GGG --sequence-two CCC --mode pseudoknot
  bin/cplcofold --sequence-file two-strands.fasta --mode standard
  bin/cplcofold --sequence 'GG&CC' --bonus-matrix-file bonus.tsv --alpha 1
  bin/cplfold --sequence-file single-strand.fasta --alpha 0.5

Modes and defaults:
  pseudoknot (default): Phase 1 plus constrained Phase 2; max-phase1=10,
    max-phase2=5, delta=5. Cofold pseudoknot scores are experimental.
  standard: no Phase 2; max-phase1=1 and delta=0 unless explicitly overridden.
  --no-pseudoknot is a compatibility flag: it disables Phase 2 without changing
    candidate/window defaults. It conflicts with --mode pseudoknot.
  Alpha defaults to 0 for every two-strand route and 0.5 for legacy single-strand
    input. With a reads matrix, choose --alpha explicitly to guide the search.
  Beta adjusts pseudoknot ranking: effective_energy = energy + beta * phase1_energy.
    It does not alter physical energy and has no ranking effect without Phase 2.

Energy models:
  Cofold accepts {' or '.join(COFOLD_MODELS)} (default: {DEFAULT_MODELS['cplcofold']}); ordinary cofold uses Turner 37 C.
  Single-strand-only models: {', '.join(m for m in HotKnotsEnergy.MODELS if m not in COFOLD_MODELS)}. The packaged Python
    HotKnots-derived evaluator does not invoke a HotKnots executable.
  --model ranks pseudoknot runs. Pseudoknot-free single-strand runs rank every
    candidate with Turner 2004 (ViennaRNA rules), the model of the search itself.
        """
    )

    inputs = parser.add_mutually_exclusive_group(required=True)
    inputs.add_argument('-s', '--sequence',
                        help="RNA sequence (ACGU or ACGT), or two strands separated by '&'")
    inputs.add_argument('--sequence-file', type=Path,
                        help='Plain RNA/FASTA input; two records require cplcofold or --cofold')
    parser.add_argument('--sequence-two', default=None,
                        help='Second RNA strand; the first is supplied with --sequence')
    parser.add_argument('--cutpoint', type=int, default=None,
                        help='Length of strand A when --sequence contains A+B without a separator')
    parser.add_argument('--cofold', action='store_true',
                        help='Require two-strand input and allow two-record FASTA (implicit in cplcofold)')
    parser.add_argument('--mode', choices=['standard', 'pseudoknot'], default=None,
                        help='standard: ordinary cofold/MFE defaults; pseudoknot: two phases (default)')
    parser.add_argument('--bonus-matrix-file', type=str, default=None,
                        help='Sparse upper-triangle bonus matrix TSV file '
                             '(1-based prepared-sequence coordinates)')
    parser.add_argument('--alpha', type=float, default=None,
                        help='Reads scaling factor (default: 0 for cofold; 0.5 for single-strand input)')
    parser.add_argument('-b', '--beam', type=int, default=100,
                        help='Beam size for LinearFold (default: 100)')
    parser.add_argument('-d', '--delta', type=float, default=None,
                        help='Suboptimal energy window (default: 5; --mode standard: 0)')
    parser.add_argument('-n1', '--max-phase1', type=int, default=None,
                        help='Maximum Phase 1 structures (default: 10; --mode standard: 1)')
    parser.add_argument('-n2', '--max-phase2', type=int, default=5,
                        help='Cofold Phase 2 candidates per Phase 1 (default: 5); legacy single-strand uses one Phase 2 fold')
    parser.add_argument('-o', '--output', type=str, default=None,
                        help='Output file path')
    parser.add_argument('-m', '--model', type=str,
                        choices=COFOLD_MODELS if cofold_entry else tuple(HotKnotsEnergy.MODELS),
                        help='Pseudoknot energy model for ranking when Phase 2 is on '
                             f"(single-strand default: {DEFAULT_MODELS['cplfold']}; cofold default: {DEFAULT_MODELS['cplcofold']}); "
                             'pseudoknot-free runs rank with Turner 2004')
    parser.add_argument('--beta', type=float, default=0.0,
                        help='Pseudoknot energy bonus factor (default: 0.0). '
                             'For pseudoknots, effective_energy = pk_energy + beta * phase1_energy. '
                             'This changes ranking, not physical energy.')
    parser.add_argument('--no-pseudoknot', action='store_true',
                        help='Only generate pseudoknot-free secondary structures; '
                             'skip Phase 2 (default: pseudoknots enabled)')
    parser.add_argument('-q', '--quiet', action='store_true',
                        help='Suppress verbose output')

    args = parser.parse_args(argv)

    sequence = (read_sequence_file(args.sequence_file, cofold=cofold_entry or args.cofold)
                if args.sequence_file is not None else args.sequence)
    if args.sequence_two is not None:
        if '&' in sequence or '&' in args.sequence_two:
            parser.error('--sequence-two requires two individual sequences without separators')
        sequence = sequence + '&' + args.sequence_two
    try:
        sequence, cutpoint = normalise_sequence(sequence, args.cutpoint)
    except ValueError as error:
        parser.error(str(error))
    if (cofold_entry or args.cofold) and cutpoint is None:
        label = 'cplcofold' if cofold_entry else '--cofold'
        parser.error(label + ' requires two strands, separated by &, --sequence-two, or --cutpoint')
    if args.model is None:
        args.model = DEFAULT_MODELS['cplfold' if cutpoint is None else 'cplcofold']

    # Validate known user options before invoking the folding implementation.
    try:
        explicit = {}
        if args.no_pseudoknot:
            explicit['allow_pseudoknot'] = False
        if args.max_phase1 is not None:
            explicit['max_phase1'] = args.max_phase1
        if args.delta is not None:
            explicit['energy_delta'] = args.delta
        options = mode_options(args.mode, explicit)
        options.setdefault('allow_pseudoknot', True)
        options.setdefault('max_phase1', 10)
        options.setdefault('energy_delta', 5.0)
        alpha = args.alpha if args.alpha is not None else (0.0 if cutpoint is not None else 0.5)
        for name, value in (('beam', args.beam), ('max-phase1', options['max_phase1']), ('max-phase2', args.max_phase2)):
            minimum = 0 if name == 'beam' else 1
            if value < minimum:
                raise ValueError(f'--{name} must be an integer >= {minimum}')
        for name, value in (('alpha', alpha), ('beta', args.beta), ('delta', options['energy_delta'])):
            if not math.isfinite(value):
                raise ValueError(f'--{name} must be finite')
            if value < 0 and (cutpoint is not None or name == 'delta'):
                raise ValueError(f'--{name} must be nonnegative')
        if cutpoint is not None and args.model not in COFOLD_MODELS:
            raise ValueError('Cofold supports ' + ' or '.join(sorted(COFOLD_MODELS)))
    except ValueError as error:
        parser.error(str(error))

    bonus_matrix = None
    if args.bonus_matrix_file:
        bonus_matrix = load_bonus_matrix_file(args.bonus_matrix_file, len(sequence))

    expected_input_errors = (CofoldInputError,) if cutpoint is not None else ()
    try:
        results = two_phase_pseudoknot_fold(
            seq=sequence, beam_size=args.beam, energy_model=args.model,
            max_phase2=args.max_phase2, output_file=args.output,
            verbose=not args.quiet, bonus_matrix=bonus_matrix,
            alpha=alpha, beta=args.beta, cutpoint=cutpoint, **options,
        )
    except expected_input_errors as error:
        parser.error(str(error))

    # A completed fold is successful even when no pseudoknot candidate wins.
    # Keep a non-zero status only for the unexpected empty-result case.
    return 0 if results else 1


if __name__ == '__main__':
    sys.exit(main())
