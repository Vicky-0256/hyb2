"""IRIS request fields and their defaults. stemLength bounds each entry of kRange; it is not a field."""

import re

IRIS_DEFAULTS = {"K": 2, "C": 20, "fraction": 0.75, "kRange": "4 5 6 7"}
# fraction excludes its upper bound, and C must be at least K.
IRIS_RANGES = dict(K=(1, 3), C=(1, 50), fraction=(0, 1), stemLength=(2, 12))
_DEFAULT_TYPES = {"K": (int,), "C": (int,), "fraction": (int, float), "kRange": (str,)}

# Exported to both UIs: validate the whole field before extracting decimals.
# ASCII whitespace alone separates numbers; one comma may have padding on either side.
_SPACE = r"[\x09-\x0d ]"
_NUMBER = r"[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?"
IRIS_K_RANGE_GRAMMAR = {
    "number": _NUMBER,
    "scalar": rf"^{_SPACE}*{_NUMBER}{_SPACE}*$",
    "list": rf"^{_SPACE}*{_NUMBER}(?:(?:{_SPACE}*,{_SPACE}*|{_SPACE}+){_NUMBER})*{_SPACE}*$",
}


def _stem_lengths(value):
    if isinstance(value, str) and re.fullmatch(IRIS_K_RANGE_GRAMMAR["list"], value):
        value = [float(m.group()) for m in re.finditer(IRIS_K_RANGE_GRAMMAR["number"], value)]
    lo, hi = IRIS_RANGES["stemLength"]
    if (not isinstance(value, (list, tuple)) or not value
            or any(type(n) not in (int, float) or not lo <= n <= hi or n != int(n) for n in value)):
        raise ValueError(f"IRIS stem length must be an integer from {lo} to {hi}.")
    return sorted({int(n) for n in value})


def _scalar(value):
    if isinstance(value, str) and re.fullmatch(IRIS_K_RANGE_GRAMMAR["scalar"], value):
        return float(value)
    return value if type(value) in (int, float) else float("nan")


def _integer(value, lo, hi, name):
    value = _scalar(value)
    if not lo <= value <= hi or value != int(value):
        raise ValueError(f"{name} must be an integer from {lo} to {hi}.")
    return int(value)


def parameters(raw):
    """Normalize request arguments, including numeric form strings and lists of stem lengths."""
    raw = raw if isinstance(raw, dict) else {}
    unknown = raw.keys() - _DEFAULT_TYPES.keys()
    if unknown:
        raise ValueError("unknown IRIS parameter: " + ", ".join(sorted(unknown)))
    K = _integer(IRIS_DEFAULTS["K"] if raw.get("K") is None else raw["K"], *IRIS_RANGES["K"], "IRIS ensemble size K")
    c_min, c_max = IRIS_RANGES["C"]
    C = _integer(IRIS_DEFAULTS["C"] if raw.get("C") is None else raw["C"], max(K, c_min), c_max, "IRIS candidate count C")
    fraction = _scalar(IRIS_DEFAULTS["fraction"] if raw.get("fraction") is None else raw["fraction"])
    lo, hi = IRIS_RANGES["fraction"]
    if not lo <= fraction < hi:
        raise ValueError(f"IRIS support quantile must be at least {lo} and below {hi}.")
    stems = raw.get("kRange")
    if stems is None or (isinstance(stems, str) and stems == ""):
        stems = IRIS_DEFAULTS["kRange"]
    return {"K": K, "C": C, "fraction": float(fraction), "kRange": _stem_lengths(stems)}


def checked_iris_defaults(defaults):
    """Check the published wire representation, then apply the actual request range checks."""
    if not isinstance(defaults, dict) or defaults.keys() != _DEFAULT_TYPES.keys():
        raise ValueError("IRIS defaults must contain exactly: " + ", ".join(_DEFAULT_TYPES))
    defaults = defaults.copy()
    for key, types in _DEFAULT_TYPES.items():
        if type(defaults[key]) not in types:  # bool is not a JSON number
            raise ValueError(f"IRIS {key}: invalid default type {type(defaults[key]).__name__}")
    if defaults["kRange"] == "":  # An empty task field selects a default; it cannot itself be the default.
        raise ValueError("IRIS kRange default must not be empty")
    parameters(defaults)
    return defaults  # The catalog carries the original stem string, not the normalized integer list.
