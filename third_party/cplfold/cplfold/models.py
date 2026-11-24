"""Energy models for ranking CPLfold candidates; no numerical dependencies.

``MODELS`` lists every ranking model; two-strand folding accepts only the
``COFOLD_MODELS`` (the DP family). :func:`default_model` gives the model used
when :func:`cplfold.fold` receives ``energy_model=None``.
"""

from ._internal.model_tables import MODEL_TABLES as _TABLES

__all__ = ["MODELS", "COFOLD_MODELS", "default_model"]

MODELS = tuple(_TABLES)
COFOLD_MODELS = tuple(model for model, (_, family) in _TABLES.items() if family == "DP")
_DEFAULT_MODELS = {False: "DP09", True: "DP09"}  # keyed by two_strands; tests may patch this source


def default_model(two_strands: bool) -> str:
    """The ranking model of ``fold(energy_model=None)`` for one strand or for two."""
    return _DEFAULT_MODELS[bool(two_strands)]
