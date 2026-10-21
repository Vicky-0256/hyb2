"""2-D layout of a secondary structure (ViennaRNA naview), for the
Structure page's folded view, as the web app's forna drawing."""
from __future__ import annotations

from typing import Any, Dict

MAX_LAYOUT_LENGTH = 30_000


def structure_layout(dbn: Any) -> Dict[str, Any]:
    """``{"x": [], "y": []}``, one point per nucleotide, for a nested
    dot-bracket string (pseudoknotted pairs are left out by the caller)."""
    if not isinstance(dbn, str) or not dbn:
        raise ValueError("dbn must be a non-empty dot-bracket string")
    if len(dbn) > MAX_LAYOUT_LENGTH:
        raise ValueError("the folded view draws at most %d nt" % MAX_LAYOUT_LENGTH)
    if set(dbn) - set(".()"):
        raise ValueError("dbn may only hold '.', '(' and ')'")
    depth = 0
    for ch in dbn:
        depth += {"(": 1, ")": -1}.get(ch, 0)
        if depth < 0:
            break
    if depth != 0:
        raise ValueError("dbn brackets are unbalanced")
    import RNA  # the bundled ViennaRNA
    points = RNA.naview_xy_coordinates(dbn)[:len(dbn)]  # it adds one point past the end
    return {"x": [round(p.X, 2) for p in points], "y": [round(p.Y, 2) for p in points]}
