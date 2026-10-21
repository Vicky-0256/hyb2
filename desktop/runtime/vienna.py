"""ViennaRNA MFE folds for the structure task (``engine: "viennarna"``), as
the web RNA Structure page's ViennaRNA mode (web/vendor/viennarna/
hyb2_vienna_fold.c): Turner 2004 parameters, a temperature and minimum
hairpin loop, and optional hard base pairs enforced in every loop context.
"""
from __future__ import annotations

import math
import time
from typing import Any, Dict, List, Sequence, Tuple

# A whole 10.8 kb genome folds in about 5 minutes and 0.7 GB (MFE is cubic
# in time, quadratic in memory); past this the fold is impractical.
MAX_VIENNA_NT = 30_000
_REFERENCE_NT, _REFERENCE_SECONDS, _REFERENCE_MB = 10_807, 285.0, 670.0
CANONICAL = {"AU", "UA", "CG", "GC", "GU", "UG"}


def parameters(raw: Any) -> Dict[str, Any]:
    """``{temperature, min_loop, hard_pairs}``, with the web's bounds."""
    raw = raw if isinstance(raw, dict) else {}
    unknown = set(raw) - {"temperature", "min_loop", "minimumLoop", "hard_pairs", "hardPairs"}
    if unknown:
        raise ValueError("unknown ViennaRNA parameter: " + ", ".join(sorted(unknown)))
    temperature = raw.get("temperature", 37)
    if isinstance(temperature, bool) or not isinstance(temperature, (int, float)) or not math.isfinite(temperature) \
            or not 0 <= temperature <= 100:
        raise ValueError("Folding temperature must be a number from 0 to 100 °C.")
    loop = raw.get("min_loop", raw.get("minimumLoop", 3))
    if isinstance(loop, bool) or not isinstance(loop, int) or not 0 <= loop <= 12:
        raise ValueError("Minimum hairpin loop must be a whole number from 0 to 12.")
    pairs = raw.get("hard_pairs", raw.get("hardPairs", []))
    if not isinstance(pairs, list):
        raise ValueError("hard_pairs must be a list of [i, j] positions")
    out = []
    for pair in pairs:
        if not (isinstance(pair, (list, tuple)) and len(pair) == 2
                and all(isinstance(v, int) and not isinstance(v, bool) for v in pair)):
            raise ValueError("each hard pair must be [i, j] with whole-number positions")
        out.append((int(pair[0]), int(pair[1])))
    return {"temperature": float(temperature), "min_loop": loop, "hard_pairs": out}


def check_hard_pairs(pairs: Sequence[Tuple[int, int]], sequence: str, min_loop: int, offset: int = 1) -> List[Tuple[int, int]]:
    """The web's parseManualConstraints rules, on positions from ``offset``;
    returns the pairs in 1-based positions of ``sequence``, sorted."""
    if len(pairs) > len(sequence) // 2:
        raise ValueError("There cannot be more constrained pairs than half the sequence length.")
    used = set()
    local = []
    for number, (i, j) in enumerate(pairs, 1):
        li, lj = i - offset + 1, j - offset + 1
        label = f"Hard pair {number} ({i}-{j})"
        if not 1 <= li < lj <= len(sequence):
            raise ValueError(f"{label} must satisfy {offset} <= i < j <= {offset + len(sequence) - 1}.")
        if lj - li - 1 < min_loop:
            raise ValueError(f"{label} violates the minimum hairpin loop of {min_loop}.")
        if li in used or lj in used:
            raise ValueError(f"{label} reuses a nucleotide that already has a constrained partner.")
        if sequence[li - 1] + sequence[lj - 1] not in CANONICAL:
            raise ValueError(f"{label} is not an A-U, G-C or G-U base pair in the sequence.")
        used.update((li, lj))
        local.append((li, lj))
    local.sort()
    for a, (i, j) in enumerate(local):
        for k, l in local[a + 1:]:
            if i < k < j < l:
                raise ValueError("Hard base pairs must not cross: ViennaRNA returns a non-pseudoknotted structure.")
    return local


def estimate(length: int) -> Tuple[float, float]:
    """Rough (seconds, MB) of one MFE fold, from a measured 10.8 kb fold."""
    ratio = length / _REFERENCE_NT
    return _REFERENCE_SECONDS * ratio ** 3, 60 + _REFERENCE_MB * ratio ** 2


def _pairs(structure: str) -> List[Tuple[int, int]]:
    stack, pairs = [], []
    for position, ch in enumerate(structure, 1):
        if ch == "(":
            stack.append(position)
        elif ch == ")":
            pairs.append((stack.pop(), position))
    return sorted(pairs)


def fold(sequence: str, params: Dict[str, Any], hard_pairs: Sequence[Tuple[int, int]] = ()) -> Dict[str, Any]:
    """One MFE fold; a record in the shape of structure.fold_one's."""
    import RNA  # the bundled ViennaRNA

    sequence = sequence.upper().replace("T", "U")
    if set(sequence) - set("ACGU"):
        raise ValueError("ViennaRNA folds A, C, G and U only; the sequence holds other letters")
    started = time.monotonic()
    md = RNA.md()
    md.temperature = params["temperature"]
    md.min_loop_size = params["min_loop"]
    fc = RNA.fold_compound(sequence, md, RNA.OPTION_MFE)
    for i, j in hard_pairs:
        fc.hc_add_bp(i, j, RNA.CONSTRAINT_CONTEXT_ALL_LOOPS | RNA.CONSTRAINT_CONTEXT_ENFORCE)
    structure, energy = fc.mfe()
    if not math.isfinite(energy):
        raise ValueError("ViennaRNA could not calculate an MFE structure that satisfies all hard base pairs.")
    pairs = _pairs(structure)
    missing = set(hard_pairs) - set(pairs)
    if missing:
        raise ValueError("ViennaRNA's structure lacks hard pair(s) " + ", ".join(f"{i}-{j}" for i, j in sorted(missing)))
    return {
        "structure": structure,
        "energy": round(float(energy), 2),
        "type": "constrained MFE" if hard_pairs else "MFE",
        "pairs": [(i, j, "()", 0.0) for i, j in pairs],
        "hard_pairs": [list(p) for p in hard_pairs],
        "evidence": {},
        "seconds": {"bonus": 0.0, "fold": round(time.monotonic() - started, 2)},
    }


def version() -> str:
    import RNA
    return getattr(RNA, "__version__", "unknown")
