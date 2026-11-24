"""Default arguments of :func:`cplfold.fold` and of the ``cplfold`` commands.

One mapping, read by the signature of ``fold()``, the command-line defaults and
the command's help text. Standard library only.
"""

from types import MappingProxyType

FOLD_DEFAULTS = MappingProxyType({
    "pseudoknots": True,
    "energy_model": None,   # cplfold.models.default_model() of the route
    "bonus": None,
    "alpha": 0.0,
    "beta": 0.0,
    "beam_size": 100,
    "energy_delta": 5.0,
    "max_phase1": 10,
    "max_phase2": None,     # TWO_STRAND_MAX_PHASE2 for two strands; one strand accepts only None
})
TWO_STRAND_MAX_PHASE2 = 5
