"""Accepted HYB2 task ranges for the fold engines; no numerical dependencies.

The folding models come from :mod:`cplfold.models`.

Ranges are inclusive except IRIS fraction's upper endpoint; IRIS C also
has to be at least K. Search/resource budgets and CLI limits are separate.
"""

import math

from cplfold.models import COFOLD_MODELS, MODELS, default_model

from .iris_parameters import IRIS_DEFAULTS, IRIS_K_RANGE_GRAMMAR, IRIS_RANGES, checked_iris_defaults

# HYB2 task defaults (not standalone CLI search budgets): minimum, maximum, default.
# Beam 200 and one Phase-1 candidate (the MFE alone, without the suboptimal
# outside pass) recover all 53 pairs of the 2011 yeast U2 structure (beam 20:
# 35). Without pseudoknots, further candidates are ranked by Turner energy
# without the read bonus, so they can only trade read support for energy.
CPL_SEARCH = dict(beamSize=(1, 200, 200), maxPhase1=(1, 20, 1), energyDelta=(0, 50, 5.0),
                  alpha=(0, 1, 0.5), beta=(0, 1, 0.0))
# The strand count of each Hyb2 fold engine, for cplfold.models.default_model.
_TWO_STRANDS = {"cplfold": False, "cplcofold": True}
DEFAULT_ALLOW_PSEUDOKNOT = False  # HYB2 tasks opt in to Phase 2; standalone CLI defaults are separate.
VIENNA_SETTINGS = dict(temperature=(0, 100, 37), minLoopSize=(0, 12, 3))
COMRADES_COUNTS = dict(constraintLimit=(1, 75, 75), randomFoldCount=(0, 1000, 0))


def task_allow_pseudoknot(parameters, *aliases, default=DEFAULT_ALLOW_PSEUDOKNOT):
    """Only omission selects the default. Native tasks may also accept a legacy spelling."""
    selected = None
    for key in ("allowPseudoknot", *aliases):
        if key not in parameters:
            continue
        value = parameters[key]
        if not isinstance(value, bool):
            raise ValueError(f"{key} must be a boolean")
        if key == "no_pseudoknot":  # inverse CLI flag in native cplfold_run requests
            value = not value
        if selected is not None and value != selected:
            raise ValueError("conflicting pseudoknot parameters")
        selected = value
    return default if selected is None else selected


def engine_parameters(platform):
    if platform not in ("desktop", "web"):
        raise ValueError(f"unknown fold host: {platform}")
    if not isinstance(DEFAULT_ALLOW_PSEUDOKNOT, bool):
        raise ValueError("allowPseudoknot: default must be a boolean")

    def model_settings(engine, models, settings):
        default = default_model(two_strands=_TWO_STRANDS[engine])
        if default not in models:
            raise ValueError(f"{engine}: default model {default!r} must be one of {', '.join(models)}")
        labels = {"RE": "RE (Rivas–Eddy)" if platform == "desktop" else "Rivas–Eddy"}
        if platform == "web":
            labels[default] = labels.get(default, default) + " · recommended"
        options = [{"value": model, "label": labels.get(model, model)}
                   for model in sorted(models, key=lambda model: model != default)]
        ranges, defaults = {}, {"allowPseudoknot": DEFAULT_ALLOW_PSEUDOKNOT}
        for key, (lo, hi, value) in settings.items():
            if (type(value) not in (int, float) or not lo <= value <= hi
                    or (key in ("beamSize", "maxPhase1") and value != int(value))):
                raise ValueError(f"{engine}.{key}: invalid default {value!r} for range {lo} to {hi}")
            ranges[key], defaults[key] = (lo, hi), value
        return dict(defaultModel=default, models=options, ranges=ranges, defaults=defaults)

    def vienna_settings(engine, settings):
        ranges, defaults = {}, {}
        for key, (lo, hi, value) in settings.items():
            # Counts and loop sizes travel as integers, not booleans or whole-valued floats.
            number = type(value) is int or (key == "temperature" and type(value) is float and math.isfinite(value))
            if not number or not lo <= value <= hi:
                raise ValueError(f"{engine}.{key}: invalid default {value!r} for range {lo} to {hi}")
            ranges[key], defaults[key] = (lo, hi), value
        return dict(ranges=ranges, defaults=defaults)

    beam_lo, _, beam_default = CPL_SEARCH["beamSize"]
    single = dict(CPL_SEARCH, beamSize=(beam_lo, 1000, beam_default)) if platform == "desktop" else CPL_SEARCH
    cofold = dict(CPL_SEARCH, alpha=(*CPL_SEARCH["alpha"][:2], 0.0))
    iris_defaults = checked_iris_defaults(IRIS_DEFAULTS)
    engines = {
        "cplfold": model_settings("cplfold", MODELS, single),
        "cplcofold": model_settings("cplcofold", COFOLD_MODELS, cofold),
        "vienna": vienna_settings("vienna", VIENNA_SETTINGS),
        "comrades": vienna_settings("comrades", {**VIENNA_SETTINGS, **COMRADES_COUNTS}),
        "iris": dict(ranges=IRIS_RANGES.copy(), defaults=iris_defaults, parameterKeys=list(iris_defaults),
                     kRangeGrammar=IRIS_K_RANGE_GRAMMAR.copy()),
    }
    if platform == "desktop":
        engines["mfold"] = dict(ranges=dict(temperature=(37, 37), minLoopSize=(3, 3)))
    return engines
