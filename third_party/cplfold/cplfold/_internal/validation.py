"""The one validator of :func:`cplfold.fold` and the commands, for both routes.

:func:`validate_fold_inputs` normalises the sequence and checks every option,
including the numeric range of the bonus for the engine that will fold it, and
returns :class:`FoldInputs`. The engines receive only validated inputs.
"""

import math
from dataclasses import dataclass
from numbers import Integral
from typing import Any, Optional

import numpy as np

from ..models import COFOLD_MODELS, MODELS
from ..sequence import normalise_sequence
from .defaults import TWO_STRAND_MAX_PHASE2
from .errors import InputError


@dataclass(frozen=True)
class BonusLimits:
    """The numeric range of one engine's bonus input.

    ``element``: largest absolute matrix value (None: any finite float64).
    ``search``: the parser's impossible score; a scaled bonus total must stay below it.
    """
    element: Optional[float]
    search: float


@dataclass(frozen=True)
class FoldInputs:
    """Validated inputs of one fold. ``sequence`` is compact (no '&')."""
    sequence: str
    cutpoint: Optional[int]
    energy_model: str
    pseudoknots: bool
    beam_size: int
    energy_delta: float
    max_phase1: int
    max_phase2: Optional[int]  # None for one strand
    alpha: float
    beta: float
    bonus: Any  # None or a C-contiguous float64 N x N array

    @property
    def two_strands(self):
        return self.cutpoint is not None


def _positive_integer(value, name, allow_zero=False):
    minimum = 0 if allow_zero else 1
    if isinstance(value, bool) or not isinstance(value, Integral) or value < minimum:
        raise InputError(f"{name} must be an integer >= {minimum}")
    return int(value)


def _nonnegative(value, name):
    if isinstance(value, (bool, np.bool_)):
        raise InputError(f"{name} must be finite and nonnegative")
    try:
        value = float(value)
    except (TypeError, ValueError, OverflowError) as error:
        raise InputError(f"{name} must be finite and nonnegative") from error
    if not math.isfinite(value) or value < 0:
        raise InputError(f"{name} must be finite and nonnegative")
    return value


def _bonus_matrix(value, length, limits, alpha):
    if value is None:
        return None
    if np.iscomplexobj(value):
        raise InputError("Bonus matrix must contain real finite values")
    try:
        matrix = np.asarray(value, dtype=np.float64)
    except (TypeError, ValueError, OverflowError) as error:
        raise InputError("Bonus matrix must contain real finite values") from error
    if matrix.ndim == 1 and matrix.size == length * length:
        matrix = matrix.reshape(length, length)
    if matrix.shape != (length, length):
        raise InputError("Bonus matrix must be N by N, where N excludes the '&' separator")
    if not np.isfinite(matrix).all():
        raise InputError("Bonus matrix must contain only finite values")
    if not np.allclose(matrix, matrix.T, rtol=0, atol=1e-7):
        raise InputError("Bonus matrix must be symmetric")
    matrix = np.ascontiguousarray(matrix)
    if limits.element is not None and np.max(np.abs(matrix)) > limits.element:
        raise InputError("Bonus matrix exceeds the supported float32 range")
    # The parser's numeric range, checked before any search starts.
    if alpha != 0.0 and np.any(matrix):
        try:
            with np.errstate(over="raise", invalid="raise"):
                scaled = matrix * alpha
                scaled *= 100.0
        except FloatingPointError as error:
            raise InputError("Scaled bonus exceeds the finite numeric range") from error
        if np.max(np.abs(scaled)) >= limits.search / (4.0 * max(1, len(matrix) // 2)):
            raise InputError("Scaled bonus total exceeds the parser numeric range")
    return matrix


def _choices(names):
    names = sorted(names)
    return names[0] if len(names) == 1 else ", ".join(names[:-1]) + " or " + names[-1]


def validate_fold_inputs(sequence, *, bonus_limits, energy_model, pseudoknots, bonus, alpha, beta,
                         beam_size, energy_delta, max_phase1, max_phase2):
    """Return :class:`FoldInputs`, or raise :class:`cplfold.InputError`.

    The sequence is upper-cased, T becomes U, whitespace is removed and one '&'
    becomes ``cutpoint``. ``energy_model`` (already resolved, never None) is
    upper-cased and must be a model of the route, also when pseudoknots are
    off. ``max_phase2=None`` selects the two-strand default; one strand accepts
    only None. The bonus must be a finite, symmetric N x N matrix over the
    compact sequence within ``bonus_limits``, the folding engine's range.
    """
    sequence, cutpoint = normalise_sequence(sequence)
    if cutpoint is None:
        label, models = "CPLfold", MODELS
        if max_phase2 is not None:
            raise InputError("max_phase2 applies to two strands only: "
                             "one strand runs one Phase 2 fold per Phase 1 structure")
    else:
        label, models = "Cofold", COFOLD_MODELS
        max_phase2 = _positive_integer(TWO_STRAND_MAX_PHASE2 if max_phase2 is None else max_phase2, "max_phase2")
    if not isinstance(pseudoknots, bool):
        raise InputError("allow_pseudoknot must be a boolean")
    energy_model = str(energy_model).upper()
    if energy_model not in models:
        raise InputError(f"{label} supports {_choices(models)}")
    beam_size = _positive_integer(beam_size, "beam_size", allow_zero=True)
    max_phase1 = _positive_integer(max_phase1, "max_phase1")
    energy_delta = _nonnegative(energy_delta, "energy_delta")
    alpha = _nonnegative(alpha, "alpha")
    beta = _nonnegative(beta, "beta")
    return FoldInputs(
        sequence=sequence, cutpoint=cutpoint, energy_model=energy_model, pseudoknots=pseudoknots,
        beam_size=beam_size, energy_delta=energy_delta, max_phase1=max_phase1, max_phase2=max_phase2,
        alpha=alpha, beta=beta, bonus=_bonus_matrix(bonus, len(sequence), bonus_limits, alpha),
    )
