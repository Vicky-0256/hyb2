"""Command-line interface for CPLfold (``cplfold``) and CPLcofold (``cplcofold``).

Run as ``python -m cplfold.cli`` or through the ``cplfold`` and ``cplcofold``
console scripts (:func:`main` and :func:`cofold_main`). Invalid input, also in
a sequence or bonus file, exits with status 2; a file that cannot be read
exits with status 1.
"""

import sys
import argparse
from pathlib import Path


from .bonus import load_bonus_matrix_file
from .models import COFOLD_MODELS, MODELS, default_model
from .sequence import add_strand_break, normalise_sequence, read_sequence_file
from ._internal.defaults import FOLD_DEFAULTS, TWO_STRAND_MAX_PHASE2
from ._internal.errors import InputError
from ._internal.router import run


def main(argv=None):
    """The ``cplfold`` command: one strand, or two with '&', --sequence-two or --cutpoint."""
    return _main(argv, cofold_entry=False)


def cofold_main(argv=None):
    """The ``cplcofold`` command: two strands only, and two-record FASTA files."""
    return _main(argv, cofold_entry=True)


def _file_error(error):
    """The message alone: status 2 for invalid file contents, 1 when the file cannot be read."""
    if isinstance(error, InputError):
        print(error, file=sys.stderr)
        raise SystemExit(2) from error
    raise SystemExit(str(error)) from error


def _main(argv, *, cofold_entry):
    from ._internal.cofold_options import mode_options

    defaults = FOLD_DEFAULTS
    parser = argparse.ArgumentParser(
        prog="cplcofold" if cofold_entry else "cplfold",
        allow_abbrev=False,
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
  pseudoknot (default): Phase 1 plus constrained Phase 2; max-phase1={defaults['max_phase1']},
    max-phase2={TWO_STRAND_MAX_PHASE2} (two strands), delta={defaults['energy_delta']:g}. Cofold pseudoknot scores are experimental.
  standard: no Phase 2; max-phase1=1 and delta=0 unless explicitly overridden.
  --no-pseudoknot is a compatibility flag: it disables Phase 2 without changing
    candidate/window defaults. It conflicts with --mode pseudoknot.
  --max-phase2 is for two strands; one strand runs one Phase 2 fold per Phase 1
    structure.
  Alpha defaults to {defaults['alpha']:g} on every route. With a reads matrix, choose --alpha
    explicitly to guide the search.
  Beta adjusts pseudoknot ranking: effective_energy = energy + beta * phase1_energy.
    It does not alter physical energy and has no ranking effect without Phase 2.

Energy models:
  Cofold accepts {' or '.join(COFOLD_MODELS)} (default: {default_model(True)}); ordinary cofold uses Turner 37 C.
  Single-strand-only models: {', '.join(m for m in MODELS if m not in COFOLD_MODELS)}. The packaged Python
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
    parser.add_argument('--alpha', type=float, default=defaults['alpha'],
                        help=f"Reads scaling factor (default: {defaults['alpha']:g})")
    parser.add_argument('-b', '--beam', type=int, default=defaults['beam_size'],
                        help=f"Beam size for LinearFold (default: {defaults['beam_size']})")
    parser.add_argument('-d', '--delta', type=float, default=None,
                        help=f"Suboptimal energy window (default: {defaults['energy_delta']:g}; --mode standard: 0)")
    parser.add_argument('-n1', '--max-phase1', type=int, default=None,
                        help=f"Maximum Phase 1 structures (default: {defaults['max_phase1']}; --mode standard: 1)")
    parser.add_argument('-n2', '--max-phase2', type=int, default=defaults['max_phase2'],
                        help=f'Two-strand Phase 2 candidates per Phase 1 (default: {TWO_STRAND_MAX_PHASE2}); '
                             'not accepted for one strand, which runs one Phase 2 fold')
    parser.add_argument('-o', '--output', type=str, default=None,
                        help='Output file path')
    parser.add_argument('-m', '--model', type=str, default=defaults['energy_model'],
                        choices=COFOLD_MODELS if cofold_entry else MODELS,
                        help='Pseudoknot energy model for ranking when Phase 2 is on '
                             f"(single-strand default: {default_model(False)}; cofold default: {default_model(True)}); "
                             'pseudoknot-free runs rank with Turner 2004')
    parser.add_argument('--beta', type=float, default=defaults['beta'],
                        help=f"Pseudoknot energy bonus factor (default: {defaults['beta']}). "
                             'For pseudoknots, effective_energy = pk_energy + beta * phase1_energy. '
                             'This changes ranking, not physical energy.')
    parser.add_argument('--no-pseudoknot', action='store_true',
                        help='Only generate pseudoknot-free secondary structures; '
                             'skip Phase 2 (default: pseudoknots enabled)')
    parser.add_argument('-q', '--quiet', action='store_true',
                        help='Suppress verbose output')

    args = parser.parse_args(argv)

    if args.sequence_file is None:
        sequence = args.sequence
    else:
        try:
            sequence = read_sequence_file(args.sequence_file, cofold=cofold_entry or args.cofold)
        except (InputError, OSError) as error:
            _file_error(error)
    if args.sequence_two is not None:
        if '&' in sequence or '&' in args.sequence_two:
            parser.error('--sequence-two requires two individual sequences without separators')
        sequence = sequence + '&' + args.sequence_two
    try:
        sequence, cutpoint = normalise_sequence(sequence, args.cutpoint)
    except InputError as error:
        parser.error(str(error))
    if (cofold_entry or args.cofold) and cutpoint is None:
        label = 'cplcofold' if cofold_entry else '--cofold'
        parser.error(label + ' requires two strands, separated by &, --sequence-two, or --cutpoint')

    explicit = {}
    if args.no_pseudoknot:
        explicit['allow_pseudoknot'] = False
    if args.max_phase1 is not None:
        explicit['max_phase1'] = args.max_phase1
    if args.delta is not None:
        explicit['energy_delta'] = args.delta
    try:
        options = mode_options(args.mode, explicit)
    except ValueError as error:
        parser.error(str(error))

    bonus = None
    if args.bonus_matrix_file:
        try:
            bonus = load_bonus_matrix_file(args.bonus_matrix_file, len(sequence))
        except (InputError, OSError) as error:
            _file_error(error)

    # The shared validator checks every option, on both routes.
    try:
        results = run(
            add_strand_break(sequence, cutpoint), output_file=args.output, verbose=not args.quiet,
            pseudoknots=options.get('allow_pseudoknot', defaults['pseudoknots']),
            energy_model=args.model, bonus=bonus, alpha=args.alpha, beta=args.beta, beam_size=args.beam,
            energy_delta=options.get('energy_delta', defaults['energy_delta']),
            max_phase1=options.get('max_phase1', defaults['max_phase1']), max_phase2=args.max_phase2,
        )
    except InputError as error:
        parser.error(str(error))

    # A completed fold is successful even when no pseudoknot candidate wins.
    # Keep a non-zero status only for the unexpected empty-result case.
    return 0 if results else 1


if __name__ == '__main__':
    sys.exit(main())
