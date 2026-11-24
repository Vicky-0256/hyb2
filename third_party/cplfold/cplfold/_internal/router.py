"""The one router of :func:`cplfold.fold` and the commands.

It joins ``partner`` to the sequence, picks the engine from the strand count,
resolves the default model, validates once (:mod:`.validation`) and runs the
engine on the compact sequence and its cutpoint.
"""

from ..models import default_model
from ..sequence import normalise_sequence
from .defaults import FOLD_DEFAULTS
from .errors import InputError
from .validation import validate_fold_inputs


def run(sequence, *, partner=None, output_file=None, verbose=False, **options):
    """Return the engine's result dicts (public :class:`cplfold.Prediction` names), best first.

    ``options`` are the keyword arguments of :func:`cplfold.fold`; omitted
    ones take :data:`FOLD_DEFAULTS`. ``output_file`` and ``verbose`` are the
    command's report; :func:`cplfold.fold` uses neither.
    """
    unknown = set(options) - set(FOLD_DEFAULTS)
    if unknown:
        raise TypeError(f"unexpected keyword argument {sorted(unknown)[0]!r}")
    options = {**FOLD_DEFAULTS, **options}
    if partner is not None:
        # Each strand on its own: an empty strand reports itself, not a misplaced '&'.
        for strand in (sequence, partner):
            if normalise_sequence(strand)[1] is not None:
                raise InputError("fold(sequence, partner=...) takes two individual strands without '&'")
        sequence = sequence + "&" + partner
    two_strands = isinstance(sequence, str) and "&" in sequence
    if two_strands:
        from . import cofold as engine
    else:
        from . import single as engine
    if options["energy_model"] is None:
        options["energy_model"] = default_model(two_strands)
    inputs = validate_fold_inputs(sequence, bonus_limits=engine.BONUS_LIMITS, **options)
    return engine.run(inputs, output_file=output_file, verbose=verbose)
