"""CPLfold: reads-guided two-phase RNA folding with pseudoknots, for one or two strands.

>>> import cplfold
>>> best = cplfold.fold("GGGAAACCCAGGGAAACCCUGGGAAACCCA")[0]
>>> best.structure, best.energy

Public modules: :mod:`cplfold.bonus` (reads-to-bonus matrices),
:mod:`cplfold.sequence` (sequence and strand layout), :mod:`cplfold.models`
(energy models) and :mod:`cplfold.cli` (the ``cplfold`` and ``cplcofold``
commands). Invalid input raises :class:`InputError` everywhere. Everything
under ``cplfold._internal`` is private.
"""

from copy import deepcopy as _deepcopy
from types import MappingProxyType as _MappingProxyType

from ._internal.defaults import FOLD_DEFAULTS as _DEFAULTS
from ._internal.errors import InputError

__version__ = "1.0.0"
__all__ = ["fold", "Prediction", "InputError", "__version__"]


def _frozen(value):
    """Read-only copy of a value: mappings become proxies, lists tuples."""
    if isinstance(value, dict):
        return _MappingProxyType({key: _frozen(item) for key, item in value.items()})
    if isinstance(value, (list, tuple)):
        return tuple(_frozen(item) for item in value)
    return value


class Prediction:
    """Immutable result of :func:`fold`: one candidate structure.

    Both routes (one strand, two strands) have the same fields; ``details``
    holds the extras of a route. Coordinates are 0-based on the compact
    sequence (no ``&``). Created by :func:`fold`; the constructor takes a
    mapping with exactly the :attr:`FIELDS`.
    """

    FIELDS = ("structure", "energy", "effective_energy", "kind", "score", "phase1_structure",
              "phase1_energy", "phase2_structure", "cutpoint", "pairs", "energy_model",
              "read_support", "details")
    __slots__ = ("_data", "_details")

    def __init__(self, data):
        data = dict(data)
        if set(data) != set(self.FIELDS):
            missing, extra = set(self.FIELDS) - set(data), set(data) - set(self.FIELDS)
            raise TypeError(f"Prediction fields differ: missing {sorted(missing)}, unexpected {sorted(extra)}")
        # Independent of the caller's dict and its nested values.
        data = {name: _deepcopy(data[name]) for name in self.FIELDS}
        data["pairs"] = tuple((int(i), int(j)) for i, j in data["pairs"])
        data["details"] = dict(data["details"])
        object.__setattr__(self, "_data", data)
        object.__setattr__(self, "_details", _frozen(data["details"]))

    def __setattr__(self, name, value):
        raise AttributeError("Prediction is immutable")

    def __delattr__(self, name):
        raise AttributeError("Prediction is immutable")

    def __repr__(self):
        return f"Prediction(structure={self.structure!r}, energy={self.energy!r}, kind={self.kind!r})"

    structure = property(lambda self: self._data["structure"], doc="Dot-bracket structure ('&' between strands).")
    energy = property(lambda self: self._data["energy"], doc="Physical free energy (kcal/mol), without bonus.")
    effective_energy = property(lambda self: self._data["effective_energy"],
                                doc="Ranking energy: energy + beta * phase1_energy for pseudoknots, else energy.")
    kind = property(lambda self: self._data["kind"], doc="'phase1' or 'pseudoknot'.")
    score = property(lambda self: self._data["score"], doc="Search score of the candidate.")
    phase1_structure = property(lambda self: self._data["phase1_structure"],
                                doc="Phase 1 structure (the structure itself for Phase 1 candidates).")
    phase1_energy = property(lambda self: self._data["phase1_energy"], doc="Energy of the Phase 1 structure.")
    phase2_structure = property(lambda self: self._data["phase2_structure"],
                                doc="Phase 2 structure (None for Phase 1 candidates).")
    cutpoint = property(lambda self: self._data["cutpoint"],
                        doc="Length of the first strand; None for one strand.")
    pairs = property(lambda self: self._data["pairs"],
                     doc="Sorted tuple of 0-based (i, j) pairs, i < j, on the compact sequence.")
    energy_model = property(lambda self: self._data["energy_model"],
                            doc="Model of energy: the pseudoknot ranking model (e.g. 'DP09') or 'Turner 2004'.")
    read_support = property(lambda self: self._data["read_support"],
                            doc="Sum of the bonus matrix over the pairs; 0.0 without a bonus.")

    @property
    def details(self):
        """Read-only mapping of the route's extras (empty for one strand).

        Nested values are read-only too: dicts are mappings, lists are tuples.
        """
        return self._details

    def as_dict(self):
        """A plain deep copy keyed by :attr:`FIELDS`, in that order."""
        data = _deepcopy(self._data)
        data["pairs"] = list(data["pairs"])
        return data


def fold(sequence, *, partner=None, pseudoknots=_DEFAULTS["pseudoknots"],
         energy_model=_DEFAULTS["energy_model"], bonus=_DEFAULTS["bonus"], alpha=_DEFAULTS["alpha"],
         beta=_DEFAULTS["beta"], beam_size=_DEFAULTS["beam_size"], energy_delta=_DEFAULTS["energy_delta"],
         max_phase1=_DEFAULTS["max_phase1"], max_phase2=_DEFAULTS["max_phase2"]):
    """Fold one RNA, or two strands: ``"A&B"`` or ``sequence="A", partner="B"``.

    Returns a list of :class:`Prediction`, best (lowest effective energy) first.
    The sequence is upper-cased, T becomes U and whitespace is removed.

    ``pseudoknots`` runs Phase 2. ``energy_model`` is the pseudoknot ranking
    model (:data:`cplfold.models.MODELS`; two strands accept only
    :data:`cplfold.models.COFOLD_MODELS`); None selects
    :func:`cplfold.models.default_model`. It is validated also when pseudoknots
    are off, when one strand ranks with Turner 2004 (see
    :attr:`Prediction.energy_model`). ``bonus`` is an optional symmetric N x N
    reads matrix over the sequence without ``&``, scaled by ``alpha`` in the
    search. ``beta`` favours pseudoknots in ranking only. ``max_phase2`` limits
    the constrained Phase 2 candidates per Phase 1 structure of two strands
    (None: 5); one strand runs one Phase 2 fold per Phase 1 structure and
    accepts only None.

    Invalid arguments and inputs whose magnitude overflows the numeric range
    raise :class:`InputError`, a ``ValueError``. Never prints, never writes files.
    """
    from ._internal.router import run
    results = run(sequence, partner=partner, pseudoknots=pseudoknots, energy_model=energy_model,
                  bonus=bonus, alpha=alpha, beta=beta, beam_size=beam_size, energy_delta=energy_delta,
                  max_phase1=max_phase1, max_phase2=max_phase2)
    return [Prediction(result) for result in results]
