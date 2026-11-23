"""Energy models for ranking CPLfold candidates; no numerical dependencies.

``MODELS`` lists every ranking model; two-strand folding accepts only the
``COFOLD_MODELS`` (the DP family). ``DEFAULT_MODELS`` gives the model used when
:func:`cplfold.fold` receives ``energy_model=None``, for one strand (``"cplfold"``)
and for two (``"cplcofold"``).
"""

from ._internal.model_tables import MODEL_TABLES as _TABLES

__all__ = ["MODELS", "COFOLD_MODELS", "DEFAULT_MODELS"]

MODELS = tuple(_TABLES)
COFOLD_MODELS = tuple(model for model, (_, family) in _TABLES.items() if family == "DP")
DEFAULT_MODELS = {"cplfold": "DP09", "cplcofold": "DP09"}
