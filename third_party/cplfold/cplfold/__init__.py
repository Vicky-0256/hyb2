"""CPLfold: reads-guided two-phase RNA folding with pseudoknots, for one or two strands.

>>> import cplfold
>>> best = cplfold.fold("GGGAAACCCAGGGAAACCCUGGGAAACCCA")[0]
>>> best.structure, best.energy

Public modules: :mod:`cplfold.bonus` (reads-to-bonus matrices),
:mod:`cplfold.sequence` (sequence and strand layout), :mod:`cplfold.models`
(energy models) and :mod:`cplfold.cli` (the ``cplfold`` command).
Everything under ``cplfold._internal`` is private.
"""

from copy import deepcopy as _deepcopy
from types import MappingProxyType as _MappingProxyType

__version__ = "1.0.0"
__all__ = ["fold", "Prediction", "CofoldInputError", "__version__"]


class Prediction:
    """Immutable view of one candidate structure returned by :func:`fold`."""

    __slots__ = ("_data",)

    def __init__(self, data):
        object.__setattr__(self, "_data", dict(data))

    def __setattr__(self, name, value):
        raise AttributeError("Prediction is immutable")

    def __delattr__(self, name):
        raise AttributeError("Prediction is immutable")

    def __repr__(self):
        return f"Prediction(structure={self.structure!r}, energy={self.energy!r}, kind={self.kind!r})"

    structure = property(lambda self: self._data.get("structure"), doc="Dot-bracket structure ('&' between strands).")
    energy = property(lambda self: self._data.get("energy"), doc="Physical free energy (kcal/mol), without bonus.")
    effective_energy = property(lambda self: self._data.get("effective_energy"),
                                doc="Ranking energy: energy + beta * phase1_energy for pseudoknots.")
    kind = property(lambda self: self._data.get("type"), doc="'phase1' or 'pseudoknot'.")
    score = property(lambda self: self._data.get("score"), doc="Search score of the candidate.")
    phase1_structure = property(lambda self: self._data.get("phase1_struct"), doc="Phase 1 structure.")
    phase1_energy = property(lambda self: self._data.get("phase1_energy"), doc="Energy of the Phase 1 structure.")
    phase2_structure = property(lambda self: self._data.get("phase2_struct"),
                                doc="Phase 2 structure (None for Phase 1 candidates).")

    @property
    def details(self):
        """Read-only mapping of every original key (two-strand candidates have more)."""
        return _MappingProxyType(self._data)

    def as_dict(self):
        """A fresh copy of the original candidate dict: same keys, same order."""
        return _deepcopy(self._data)


def fold(sequence, *, partner=None, pseudoknots=True, energy_model=None,
         bonus=None, alpha=0.0, beta=0.0, beam_size=100, energy_delta=5.0,
         max_phase1=10, max_phase2=5):
    """Fold one RNA (or two strands joined by one ``&``), or ``sequence`` with ``partner``.

    Returns a list of :class:`Prediction`, best (lowest effective energy) first.
    ``energy_model=None`` selects the default model of the route
    (:data:`cplfold.models.DEFAULT_MODELS`). ``bonus`` is an optional N x N reads
    matrix over the sequence without separators, scaled by ``alpha``. Both routes
    normalise and validate input the same way; invalid input raises
    ``CofoldInputError``, a ``ValueError``. Never prints and never writes files.
    """
    options = dict(beam_size=beam_size, energy_delta=energy_delta, max_phase1=max_phase1,
                   max_phase2=max_phase2, bonus_matrix=bonus, alpha=alpha, beta=beta,
                   allow_pseudoknot=pseudoknots)
    if partner is None:
        from ._internal.single import two_phase_pseudoknot_fold
        results = two_phase_pseudoknot_fold(sequence, energy_model=energy_model, output_file=None,
                                            verbose=False, lv=True, **options)
    else:
        from ._internal.cofold import cofold
        if energy_model is not None:  # otherwise keep cofold's own default model
            options["energy_model"] = energy_model
        results = cofold(sequence, partner, verbose=False, output_file=None, **options)
    return [Prediction(result) for result in results]


def __getattr__(name):
    # Keep ``import cplfold.models`` free of NumPy/Numba: load the folding code on use.
    if name == "CofoldInputError":
        from ._internal.cofold import CofoldInputError
        return CofoldInputError
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
