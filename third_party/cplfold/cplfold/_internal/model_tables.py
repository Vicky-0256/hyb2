"""Parameter file (``None`` for RE) and HotKnots-derived family of each ranking model.

Only the energy evaluator reads the file names; :mod:`cplfold.models` publishes
the model names. Standard library only, so ``import cplfold.models`` stays light.
"""

MODEL_TABLES = {
    "DP03": ("parameters_DP03.txt", "DP"),
    "DP09": ("parameters_DP09.txt", "DP"),
    "CC06": ("parameters_CC06.txt", "CC"),
    "CC09": ("parameters_CC09.txt", "CC"),
    "RE": (None, "RE"),
}
