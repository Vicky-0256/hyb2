"""Input normalisation and validation shared by the one- and two-strand routes.

Both :func:`cplfold._internal.single.two_phase_pseudoknot_fold` and
:func:`cplfold._internal.cofold.two_phase_cofold` call :func:`validate_fold_inputs`
once, before any search, so the same input is accepted, normalised or rejected
with the same :class:`CofoldInputError` (a ``ValueError``) on either route.
"""

import math
from numbers import Integral

import numpy as np

from ..models import COFOLD_MODELS, MODELS
from ..sequence import normalise_sequence


class CofoldInputError(ValueError):
    """Expected input/range errors that CLI callers can report without traceback."""


def normalise(sequence, cutpoint=None):
    """:func:`cplfold.sequence.normalise_sequence`, raising :class:`CofoldInputError`."""
    try:
        return normalise_sequence(sequence, cutpoint)
    except ValueError as error:
        raise CofoldInputError(str(error)) from error


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


def _choices(names):
    names = sorted(names)
    return names[0] if len(names) == 1 else ", ".join(names[:-1]) + " or " + names[-1]


def validate_fold_inputs(seq, cutpoint, *, two_strands, lv, beam_size, energy_delta,
                         max_phase1, max_phase2, energy_model, bonus_matrix, alpha,
                         beta, allow_pseudoknot):
    """Return the normalised inputs of one fold, or raise :class:`CofoldInputError`.

    The sequence is upper-cased, T becomes U, whitespace is removed and one '&'
    becomes ``cutpoint``. ``energy_model`` is upper-cased and must be a model of
    the route (checked even when pseudoknots are off). The bonus matrix must be
    a finite, symmetric N x N matrix over the compact sequence.
    """
    seq, cutpoint = normalise(seq, cutpoint)
    if two_strands:
        if cutpoint is None:
            raise CofoldInputError("Cofold requires two strands: provide '&' or cutpoint")
        if not lv:
            raise CofoldInputError("Cofold currently supports Vienna/Turner mode only (lv=True)")
        label, models = "Cofold", COFOLD_MODELS
    else:
        if cutpoint is not None:
            raise CofoldInputError("Single-strand folding takes one strand without a cutpoint")
        label, models = "CPLfold", MODELS
    if not isinstance(allow_pseudoknot, bool):
        raise CofoldInputError("allow_pseudoknot must be a boolean")
    energy_model = str(energy_model).upper()
    if energy_model not in models:
        raise CofoldInputError(f"{label} supports {_choices(models)}")
    return dict(
        seq=seq, cutpoint=cutpoint, energy_model=energy_model, allow_pseudoknot=allow_pseudoknot,
        beam_size=_positive_integer(beam_size, "beam_size", allow_zero=True),
        max_phase1=_positive_integer(max_phase1, "max_phase1"),
        max_phase2=_positive_integer(max_phase2, "max_phase2"),
        energy_delta=_nonnegative(energy_delta, "energy_delta"),
        alpha=_nonnegative(alpha, "alpha"),
        beta=_nonnegative(beta, "beta"),
        bonus_matrix=_bonus_matrix(bonus_matrix, len(seq)),
    )
