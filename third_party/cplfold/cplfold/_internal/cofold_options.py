"""Explicit cofold modes without changing the legacy omitted-mode defaults."""


def mode_options(mode, options):
    """Apply mode defaults while respecting explicit candidate-count/window options."""
    result = dict(options)
    if mode is None:
        return result
    if mode not in ("standard", "pseudoknot"):
        raise ValueError("mode must be 'standard' or 'pseudoknot'")
    enabled = mode == "pseudoknot"
    if "allow_pseudoknot" in result and result["allow_pseudoknot"] is not enabled:
        raise ValueError(f"mode='{mode}' conflicts with allow_pseudoknot={result['allow_pseudoknot']!r}")
    result["allow_pseudoknot"] = enabled
    if not enabled:
        result.setdefault("max_phase1", 1)
        result.setdefault("energy_delta", 0.0)
    return result
