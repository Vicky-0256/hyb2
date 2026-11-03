"""IRIS evidence-guided ensembles (structure task engine ``"iris"``): the
web RNA Structure page's IRIS mode (web/iris-core.js, the iris-fold path of
web/structure.worker.js and diagnoseIrisRegion of web/comrades-analysis.js),
a browser adaptation of JY-Zhou/IRIS v1.0 (Scoring, Generating, Picking;
GPL-3.0, https://github.com/JY-Zhou/IRIS).

For one region of one RNA:

1. every chimera with both arms inside the region is redistributed as two
   Gaussians (sigma = mean arm length / 6) whose symmetric outer product is
   added to a support matrix; values below 2^-23 become 0, the rest log1p;
2. canonical stems of each length in ``kRange`` (loop >= 4) whose mean
   support exceeds the ``fraction`` quantile of the positive support are
   kept, stems contained in a longer kept stem removed;
3. every stem, and every compatible pair of stems, is a hard-pair
   constraint set, folded once with ViennaRNA (37 °C, minimum loop 3);
4. the candidates are clustered by average linkage down to ``C`` leaders
   (the lowest energy of each cluster);
5. every ``K``-subset of the leaders gets exact non-negative least-squares
   weights; the subset maximising -E/RT + lambda * mean support wins and
   its members are sorted by weight.

The arithmetic follows iris-core.js step by step (same summation orders),
so the same inputs give the same members, weights and energies. The web's
browser limits (300 nt, 2,000 evidence rows) are raised for the desktop;
the fold and ensemble guards keep the web's values (constants below).
"""
from __future__ import annotations

import itertools
import math
import time
from pathlib import Path
from typing import Any, Callable, Dict, Iterable, List, Optional, Sequence, Tuple

ENGINE_VERSION = "1.0-desktop.1"
EPS = 2.0 ** -23
# Desktop limits. The web's 300 nt / 2,000 rows are browser limits. Here the
# support matrix (n x n float64, 8 MB at 1,000 nt) and stem enumeration are
# cheap; the cost is the ViennaRNA folds, one per stem and stem pair. The
# web stops at 1,000; here the bound follows the region's length
# (max_folds), so that the folds take about FOLD_BUDGET_S at most: 1,000 at
# 1,000 nt (0.8 s a fold) up to MAX_FOLDS_CAP (6,000) at 500 nt and below.
# Evidence rows share COMRADES(ViennaRNA)'s bound: duplicates are merged
# before the outer products.
MAX_IRIS_NT = 1000
MAX_EVIDENCE_ROWS = 50_000
# The web's guards, kept: constrained folds per run, the candidate count C
# (C <= 50 makes the upstream LASSO prefilter unnecessary) and the K-subsets
# scored by exact NNLS.
MAX_FOLDS = 1000
MAX_FOLDS_CAP = 6000
FOLD_BUDGET_S = 900.0
MAX_CANDIDATES = 50
MAX_ENSEMBLES = 20_000
# The recovery suggestion is a window of the region limit, centred on the
# nearest complete chimera.
SUGGESTION_WINDOW = MAX_IRIS_NT
TEMPERATURE = 37.0
MIN_LOOP = 3
RT = 0.00198717 * 310.15
VALID = {"AU", "UA", "GC", "CG", "GU", "UG"}
DEFAULTS = {"K": 2, "C": 20, "fraction": 0.75, "kRange": "4 5 6 7"}
WARNING = ("Desktop adaptation of the web's browser IRIS: HYB arms replace BAM input; C ≤ 50, so the upstream "
           "LASSO prefilter is not needed. Tie-breaking can differ from scikit-learn.")
MODEL = ("IRIS desktop adaptation; Gaussian HYB support, constrained ViennaRNA candidates, average-linkage "
         "clustering and Bayesian ensemble selection")


def _fmt(n: int) -> str:
    return f"{n:,}"


# ---------------------------------------------------------------------------
# parameters (iris-core.js parameters)
# ---------------------------------------------------------------------------

def _integer(value: Any, lo: int, hi: int, name: str) -> int:
    """JavaScript Number(value) read as an integer in [lo, hi]."""
    number: Optional[float] = None
    if isinstance(value, bool):
        number = float(value)
    elif isinstance(value, (int, float)):
        number = float(value)
    elif isinstance(value, str):
        text = value.strip()
        try:
            number = 0.0 if text == "" else float(text)
        except ValueError:
            number = None
    if number is None or not math.isfinite(number) or number != int(number) or not lo <= number <= hi:
        raise ValueError(f"{name} must be an integer from {lo} to {hi}.")
    return int(number)


def parameters(raw: Any) -> Dict[str, Any]:
    """``{K, C, fraction, kRange}`` checked as the web (defaults 2, 20, 0.75, "4 5 6 7")."""
    raw = raw if isinstance(raw, dict) else {}
    unknown = set(raw) - {"K", "C", "fraction", "kRange"}
    if unknown:
        raise ValueError("unknown IRIS parameter: " + ", ".join(sorted(unknown)))
    K = _integer(DEFAULTS["K"] if raw.get("K") is None else raw["K"], 1, 3, "IRIS ensemble size K")
    C = _integer(DEFAULTS["C"] if raw.get("C") is None else raw["C"], K, MAX_CANDIDATES, "IRIS candidate count C")
    fraction_raw = DEFAULTS["fraction"] if raw.get("fraction") is None else raw["fraction"]
    try:
        fraction = float(fraction_raw) if not isinstance(fraction_raw, bool) else float("nan")
    except (TypeError, ValueError):
        fraction = float("nan")
    if not math.isfinite(fraction) or fraction < 0 or fraction >= 1:
        raise ValueError("IRIS support quantile must be at least 0 and below 1.")
    k_raw = raw.get("kRange")
    if isinstance(k_raw, (list, tuple)):
        tokens: List[Any] = list(k_raw) or [""]
    else:  # String(p.kRange || "4 5 6 7")
        tokens = _split_js(str(k_raw) if k_raw else DEFAULTS["kRange"])
    k_range = sorted({_integer(k, 2, 12, "IRIS stem length") for k in tokens})
    return {"K": K, "C": C, "fraction": fraction, "kRange": k_range}


def _split_js(text: str) -> List[str]:
    """String(text).trim().split(/[ ,]+/)."""
    text = text.strip()
    out, current, separator = [], "", False
    for ch in text:
        if ch in " ,":
            if not separator:
                out.append(current)
                current = ""
            separator = True
        else:
            current += ch
            separator = False
    out.append(current)
    return out


# ---------------------------------------------------------------------------
# evidence: the support matrix and stems
# ---------------------------------------------------------------------------

def support_matrix(n: int, arms: Sequence[Dict[str, int]]):
    """The n x n log1p Gaussian support of the arms ({oneStart, oneEnd,
    twoStart, twoEnd}, 1-based inclusive), as a numpy array."""
    import numpy as np

    if not arms:
        raise ValueError("IRIS requires HYB evidence within the selected reference region.")
    if len(arms) > MAX_EVIDENCE_ROWS:
        raise ValueError(f"IRIS desktop limit: {_fmt(MAX_EVIDENCE_ROWS)} evidence rows. Select a smaller region.")
    blocks = []
    for arm in arms:
        try:
            b = (int(arm["oneStart"]) - 1, int(arm["oneEnd"]), int(arm["twoStart"]) - 1, int(arm["twoEnd"]))
        except (KeyError, TypeError, ValueError):
            raise ValueError("IRIS evidence coordinates are outside the prepared sequence.") from None
        if b[0] < 0 or b[2] < 0 or b[1] <= b[0] or b[3] <= b[2] or b[1] > n or b[3] > n:
            raise ValueError("IRIS evidence coordinates are outside the prepared sequence.")
        blocks.append(b)
    total = 0
    for b in blocks:  # the web's reduce: integer lengths, exact
        total = total + b[1] - b[0] + b[3] - b[2]
    sd = total / (2 * len(blocks) * 6)
    # Each block adds g(mu_l) g(mu_r)^T + g(mu_r) g(mu_l)^T; gaussians depend
    # only on the midpoint, so count the midpoint pairs and multiply once.
    centres: Dict[float, int] = {}
    counts: Dict[Tuple[int, int], int] = {}
    for b in blocks:
        left, right = (b[0] + b[1]) / 2, (b[2] + b[3]) / 2
        a = centres.setdefault(left, len(centres))
        c = centres.setdefault(right, len(centres))
        counts[(a, c)] = counts.get((a, c), 0) + 1
    positions = np.arange(n, dtype=np.float64)
    mus = np.array(sorted(centres, key=centres.get), dtype=np.float64)
    gauss = np.exp(-0.5 * ((positions[None, :] - mus[:, None]) / sd) ** 2)
    gauss /= gauss.max(axis=1, keepdims=True)
    weights = np.zeros((len(mus), len(mus)), dtype=np.float64)
    for (a, c), count in counts.items():
        weights[a, c] += count
    half = gauss.T @ (weights @ gauss)  # sum over blocks of l r^T
    matrix = half + half.T
    out = np.zeros_like(matrix)
    keep = matrix >= EPS
    out[keep] = np.log1p(matrix[keep])
    return out


def _quantile(values, q: float) -> float:
    """iris-core.js quantile: linear interpolation in the sorted values."""
    import numpy as np

    a = np.sort(np.asarray(values, dtype=np.float64))
    x = (len(a) - 1) * q
    lo = math.floor(x)
    hi = math.ceil(x)
    return float(a[lo]) + (float(a[hi]) - float(a[lo])) * (x - lo)


def collect_stems(sequence: str, support, params: Dict[str, Any]) -> List[Dict[str, int]]:
    """Supported canonical stems ``{l, r, k}`` (0-based starts), longest first."""
    import numpy as np

    n = len(sequence)
    positive = support[support > 0]
    if positive.size == 0:
        raise ValueError("IRIS evidence support is empty.")
    threshold = _quantile(positive, params["fraction"])
    codes = {"A": 0, "C": 1, "G": 2, "U": 3}
    seq = np.array([codes[ch] for ch in sequence], dtype=np.int8)
    canon = np.zeros((4, 4), dtype=bool)
    for pair in VALID:
        canon[codes[pair[0]], codes[pair[1]]] = True
    pairable = canon[seq[:, None], seq[None, :]]
    stems: List[Dict[str, int]] = []
    for k in params["kRange"]:
        m = n - k + 1
        if m <= 0:
            continue
        valid = np.ones((m, m), dtype=bool)
        for t in range(k):  # sequence[l+t] pairs sequence[r+k-1-t]
            valid &= pairable[t:t + m, k - 1 - t:k - 1 - t + m]
        valid &= np.triu(np.ones((m, m), dtype=bool), k + 4)  # r >= l + k + 4
        if not valid.any():
            continue
        # block sums in the web's order: rows i, then columns j
        sums = np.zeros((m, m), dtype=np.float64)
        for a in range(k):
            for b in range(k):
                sums += support[a:a + m, b:b + m]
        means = sums / (k * k)
        for l, r in zip(*np.nonzero(valid & (means > threshold))):
            stems.append({"l": int(l), "r": int(r), "k": k})
    stems.sort(key=lambda s: -s["k"])  # stable, as Array.prototype.sort
    clean: List[Dict[str, int]] = []
    for s in stems:
        if not any(s["l"] >= t["l"] and s["l"] + s["k"] <= t["l"] + t["k"] and s["r"] >= t["r"]
                   and s["r"] + s["k"] <= t["r"] + t["k"] for t in clean):
            clean.append(s)
    if not clean:
        raise ValueError("IRIS found no supported canonical stems at these settings.")
    return clean


def constraints_for(stems: Sequence[Dict[str, int]]) -> Optional[List[Tuple[int, int]]]:
    """The 1-based hard pairs of the stems, or None when they share a base or cross."""
    pairs = [(s["l"] + i + 1, s["r"] + s["k"] - i) for s in stems for i in range(s["k"])]
    occupied = set()
    for left, right in pairs:
        if left in occupied or right in occupied:
            return None
        occupied.add(left)
        occupied.add(right)
    for a_left, a_right in pairs:
        for b_left, b_right in pairs:
            if a_left < b_left < a_right < b_right:
                return None
    return pairs


def fold_seconds(n: int) -> float:
    """One constrained ViennaRNA MFE fold of n nt (measured: 0.018 s at
    200 nt, 0.04 s at 300, 0.14 s at 500, 0.82 s at 1,000)."""
    return 0.82 * (max(n, 1) / 1000.0) ** 2.5


def max_folds(n: int) -> int:
    """The constrained folds allowed for a region of n nt: FOLD_BUDGET_S of
    folding, between the web's MAX_FOLDS and MAX_FOLDS_CAP (the clustering
    holds an n x n distance matrix of the candidates)."""
    return int(min(MAX_FOLDS_CAP, max(MAX_FOLDS, FOLD_BUDGET_S // fold_seconds(n))))


def collect_constraints(stems: Sequence[Dict[str, int]], max_folds: int = MAX_FOLDS) -> List[List[Tuple[int, int]]]:
    """Every single stem, then every compatible pair of stems."""
    constraints: List[List[Tuple[int, int]]] = []

    def add(group) -> None:
        pairs = constraints_for(group)
        if pairs is None:
            return
        if len(constraints) >= max_folds:
            raise ValueError(f"IRIS desktop limit: more than {_fmt(max_folds)} constrained folds. "
                             "Select a smaller region or a higher support quantile.")
        constraints.append(pairs)

    for stem in stems:
        add([stem])
    for i in range(len(stems)):
        for j in range(i + 1, len(stems)):
            add([stems[i], stems[j]])
    return constraints


# ---------------------------------------------------------------------------
# candidates: constrained ViennaRNA folds
# ---------------------------------------------------------------------------

def _dot_pairs(structure: str) -> List[Tuple[int, int]]:
    stack, pairs = [], []
    for position, ch in enumerate(structure, 1):
        if ch == "(":
            stack.append(position)
        elif ch == ")":
            pairs.append((stack.pop(), position))
    return sorted(pairs)


class Folder:
    """One ViennaRNA fold compound reused for every constraint set (its hard
    constraints reset each time), at 37 °C with minimum loop 3."""

    def __init__(self, sequence: str):
        import RNA
        md = RNA.md()
        md.temperature = TEMPERATURE
        md.min_loop_size = MIN_LOOP
        self.RNA = RNA
        self.fc = RNA.fold_compound(sequence, md, RNA.OPTION_MFE)

    def __call__(self, sequence: str, constraints: Sequence[Tuple[int, int]]) -> Dict[str, Any]:
        RNA, fc = self.RNA, self.fc
        fc.hc_init()
        for i, j in constraints:
            fc.hc_add_bp(i, j, RNA.CONSTRAINT_CONTEXT_ALL_LOOPS | RNA.CONSTRAINT_CONTEXT_ENFORCE)
        structure, energy = fc.mfe()
        pairs = _dot_pairs(structure) if math.isfinite(energy) else []
        if not math.isfinite(energy) or set(constraints) - set(pairs):
            raise ValueError("ViennaRNA could not calculate an MFE structure that satisfies all requested base-pair constraints.")
        return {"dotBracket": structure, "pairs": pairs, "energy": float(energy),
                "constraints": [list(p) for p in constraints]}


# ---------------------------------------------------------------------------
# clustering and the ensemble (iris-core.js cluster, solve, nnls, pick)
# ---------------------------------------------------------------------------

def _distances(candidates: List[Dict[str, Any]]):
    """The base-pair distance |A| + |B| - 2|A ∩ B| of every two candidates."""
    import numpy as np

    index: Dict[Tuple[int, int], int] = {}
    rows, cols = [], []
    for i, c in enumerate(candidates):
        for pair in c["pairs"]:
            rows.append(i)
            cols.append(index.setdefault(tuple(pair), len(index)))
    n = len(candidates)
    x = np.zeros((n, max(len(index), 1)), dtype=np.float32)
    x[rows, cols] = 1.0
    sizes = x.sum(axis=1, dtype=np.float64)
    # exact: the counts stay far below 2^24
    shared = (x @ x.T).astype(np.float64)
    return sizes[:, None] + sizes[None, :] - 2.0 * shared


def cluster(candidates: List[Dict[str, Any]], C: int) -> List[Dict[str, Any]]:
    """Average-linkage clustering down to C clusters; each cluster's lowest
    energy candidate, in the order of the clusters' first members.

    iris-core.js merges, each step, the first (i, j), i < j, in row-major
    order with the smallest distance. Each row keeps its nearest later
    neighbour (the first one at the smallest distance), so a step looks at
    n row minima rather than the n x n matrix: the same merges, in O(n^2)."""
    import numpy as np

    if len(candidates) <= C:
        return candidates
    n = len(candidates)
    d = _distances(candidates)
    np.fill_diagonal(d, 0.0)
    groups: Dict[int, List[int]] = {i: [i] for i in range(n)}
    alive = np.ones(n, dtype=bool)
    later = np.arange(n)
    best = np.full(n, np.inf)
    arg = np.full(n, -1, dtype=np.int64)

    def refresh(r: int) -> None:
        # the first alive j > r at the smallest distance
        row = np.where(alive & (later > r), d[r], np.inf)
        j = int(np.argmin(row))
        best[r], arg[r] = (row[j], j) if np.isfinite(row[j]) else (np.inf, -1)

    for r in range(n):
        refresh(r)
    while len(groups) > C:
        # argmin takes the first row at the smallest minimum: row-major order
        a = int(np.argmin(best))
        b = int(arg[a])
        ga, gb = groups[a], groups[b]
        merged = (d[a] * len(ga) + d[b] * len(gb)) / (len(ga) + len(gb))
        others = alive.copy()
        others[a] = others[b] = False
        d[a, others] = merged[others]
        d[others, a] = merged[others]
        groups[a] = ga + gb
        del groups[b]
        alive[b] = False
        best[b], arg[b] = np.inf, -1
        refresh(a)
        # rows before a: their entry for a changed and the one for b went
        stale = np.flatnonzero(alive & (later < a) & ((arg == a) | (arg == b)))
        for r in stale:
            refresh(int(r))
        rows = np.flatnonzero(alive & (later < a) & (arg != a) & (arg != b))
        if rows.size:
            value = d[rows, a]
            better = (value < best[rows]) | ((value == best[rows]) & (a < arg[rows]))
            best[rows[better]], arg[rows[better]] = value[better], a
        # rows between a and b lost b
        for r in np.flatnonzero(alive & (later > a) & (later < b) & (arg == b)):
            refresh(int(r))
    leaders = []
    for key in sorted(groups):
        indices = groups[key]
        lead = indices[0]
        for i in indices:
            if candidates[i]["energy"] < candidates[lead]["energy"]:
                lead = i
        leaders.append(candidates[lead])
    return leaders


def _solve(matrix: List[List[float]], rhs: List[float]) -> Optional[List[float]]:
    n = len(rhs)
    a = [list(row) + [rhs[i]] for i, row in enumerate(matrix)]
    for k in range(n):
        pivot = k
        for i in range(k + 1, n):
            if abs(a[i][k]) > abs(a[pivot][k]):
                pivot = i
        if abs(a[pivot][k]) < 1e-10:
            return None
        a[k], a[pivot] = a[pivot], a[k]
        v = a[k][k]
        for j in range(k, n + 1):
            a[k][j] /= v
        for i in range(n):
            if i != k:
                f = a[i][k]
                for j in range(k, n + 1):
                    a[i][j] -= f * a[k][j]
    return [row[n] for row in a]


def nnls(gram: List[List[float]], rhs: List[float]) -> List[float]:
    """Exact non-negative least squares for K <= 3, by every active set."""
    n = len(rhs)
    best, loss = [0.0] * n, 0.0
    for mask in range(1, 2 ** n):
        indices = [i for i in range(n) if mask & (1 << i)]
        x = _solve([[gram[i][j] for j in indices] for i in indices], [rhs[i] for i in indices])
        if x is None or any(v < -1e-9 for v in x):
            continue
        full = [0.0] * n
        for k, i in enumerate(indices):
            full[i] = max(0.0, x[k])
        objective = 0.0
        for i in range(n):
            objective -= 2 * full[i] * rhs[i]
            for j in range(n):
                objective += full[i] * gram[i][j] * full[j]
        if objective < loss:
            loss, best = objective, full
    return best


def pick(candidates: List[Dict[str, Any]], support, n: int, K: int, max_ensembles: int = MAX_ENSEMBLES) -> Dict[str, Any]:
    """The K-subset of the candidates with the best -E/RT + lambda * mean support."""
    if len(candidates) < K:
        raise ValueError(f"IRIS generated only {len(candidates)} candidate(s); reduce ensemble size K.")
    sets = [set(c["pairs"]) for c in candidates]
    means = []
    for c in candidates:
        total = 0.0
        for left, right in c["pairs"]:
            total += float(support[left - 1, right - 1])
        if not c["pairs"]:
            raise ValueError("IRIS cannot score an unpaired candidate.")
        means.append(total / len(c["pairs"]))
    energies = [c["energy"] for c in candidates]
    spread = max(means) - min(means)
    log_range = (max(energies) - min(energies)) / RT
    if spread <= 1e-12 or log_range <= 1e-12:
        raise ValueError("IRIS cannot identify an ensemble: candidate support or energy has zero range.")
    lam = log_range / spread
    rhs = [2 * len(c["pairs"]) * means[i] for i, c in enumerate(candidates)]
    gram = [[2 * len(a & b) for b in sets] for a in sets]
    best: Optional[Dict[str, Any]] = None
    count = 0
    for indices in itertools.combinations(range(len(candidates)), K):
        count += 1
        if count > max_ensembles:
            raise ValueError("IRIS desktop limit: too many candidate ensembles. Reduce C or K.")
        weights = nnls([[gram[i][j] for j in indices] for i in indices], [rhs[i] for i in indices])
        total = 0.0
        for w in weights:
            total += w
        if total <= 1e-12:
            continue
        proportions = [w / total for w in weights]
        energy = 0.0
        mean_support = 0.0
        for k, i in enumerate(indices):
            energy += proportions[k] * energies[i]
        for k, i in enumerate(indices):
            mean_support += proportions[k] * means[i]
        score = -energy / RT + lam * mean_support
        if best is None or score > best["score"]:
            best = {"indices": list(indices), "weights": weights, "proportions": proportions, "energy": energy,
                    "meanSupport": mean_support, "score": score}
    if best is None:
        raise ValueError("IRIS could not fit positive ensemble weights.")
    best.update(lambda_=lam, testedEnsembles=count)
    return best


def run(sequence: str, arms: Sequence[Dict[str, int]], raw_params: Any,
        progress: Optional[Callable[[str, int], None]] = None,
        fold: Optional[Callable[[str, Sequence[Tuple[int, int]]], Dict[str, Any]]] = None) -> Dict[str, Any]:
    """iris-core.js run: the weighted ensemble of one region (members best
    weight first, positions 1-based on the region)."""
    sequence = str(sequence or "").upper().replace("T", "U")
    if not sequence or set(sequence) - set("ACGU") or len(sequence) > MAX_IRIS_NT:
        raise ValueError(f"IRIS requires 1–{_fmt(MAX_IRIS_NT)} canonical RNA nucleotides.")
    params = parameters(raw_params)
    n = len(sequence)
    report = progress or (lambda stage, percent: None)
    report("Redistributing HYB evidence", 8)
    support = support_matrix(n, arms)
    stems = collect_stems(sequence, support, params)
    constraints = collect_constraints(stems, max_folds(n))
    fold = fold or Folder(sequence)
    candidates = []
    for i, pairs in enumerate(constraints):
        candidates.append(fold(sequence, pairs))
        # Math.round: halves round up
        report(f"Generating IRIS candidates {i + 1} / {len(constraints)}",
               15 + math.floor(55 * (i + 1) / len(constraints) + 0.5))
    report("Clustering candidate structures", 74)
    leaders = cluster(candidates, params["C"])
    report("Selecting the IRIS ensemble", 85)
    chosen = pick(leaders, support, n, params["K"])
    members = [dict(leaders[i], weight=chosen["proportions"][k], coefficient=chosen["weights"][k])
               for k, i in enumerate(chosen["indices"])]
    members.sort(key=lambda m: -m["weight"])
    return {
        "engine": "IRIS", "engineVersion": ENGINE_VERSION, "algorithm": "IRIS evidence-guided ensemble", "model": MODEL,
        "temperature": TEMPERATURE, "minimumLoop": MIN_LOOP, "constraintMode": "iris-ensemble",
        "constraintSource": "iris-hyb-stems", "evidenceMode": "hyb-blocks", "evidenceSource": "iris-gaussian-support",
        "candidates": members, "selectedCandidate": 0, "parameters": params,
        "ensemble": {"meanEnergy": chosen["energy"], "meanSupport": chosen["meanSupport"], "lambda": chosen["lambda_"],
                     "testedEnsembles": chosen["testedEnsembles"], "candidateCount": len(leaders),
                     "generatedCount": len(candidates)},
        "evidence": {"inputRecords": len(arms), "stemCount": len(stems)},
        "warnings": [WARNING],
        "support": support,
    }


# ---------------------------------------------------------------------------
# evidence rows of a region, and why there are none (diagnoseIrisRegion)
# ---------------------------------------------------------------------------

def diagnose(rows: Iterable[Tuple[str, int, int, str, int, int]], total: int, gene: str, start: int, end: int,
             reference_length: int, window: int = SUGGESTION_WINDOW) -> Dict[str, Any]:
    """The web's diagnoseIrisRegion over (gene1, start1, end1, gene2,
    start2, end2) rows: counts, a ``window``-nt suggestion centred on the
    nearest complete forward row, and the message when none is inside."""
    counts = {"total": total, "sameRna": 0, "forward": 0, "contained": 0, "tooWide": 0}
    suggestion = None
    best = math.inf
    for g1, s1, e1, g2, s2, e2 in rows:
        if g1 != gene or g2 != gene:
            continue
        counts["sameRna"] += 1
        if not (s1 <= e1 and s2 <= e2):
            continue
        counts["forward"] += 1
        lo, hi = min(s1, s2), max(e1, e2)
        if lo >= start and hi <= end:
            counts["contained"] += 1
        if hi - lo + 1 > window:
            counts["tooWide"] += 1
            continue
        if lo < 1 or hi > reference_length:
            continue
        distance = abs((lo + hi) / 2 - (start + end) / 2)
        if distance < best:
            best = distance
            first = max(1, min(math.floor((lo + hi - (window - 1)) / 2), reference_length - (window - 1)))
            suggestion = {"rna": gene, "start": first, "end": min(reference_length, first + window - 1)}
    limit = f"IRIS limit: {_fmt(MAX_IRIS_NT)} nt"
    if not counts["total"]:
        message = "No HYB records are loaded for the active dataset selection."
    elif not counts["sameRna"]:
        message = f"No HYB rows have both arms on {gene}. IRIS requires one RNA region; cross-RNA interactions are not supported here."
    elif not counts["forward"]:
        message = "All HYB rows on this RNA have reverse or invalid arm coordinates; this IRIS input adapter requires forward coordinates."
    elif not counts["contained"]:
        message = (f"No complete HYB arm pair is inside {gene}:{start}-{end}. Both arms of each row must fit inside the "
                   f"same region ({limit}).")
    else:
        message = ""
    if counts["forward"] and counts["tooWide"] == counts["forward"]:
        message += f" Every forward arm pair spans more than {_fmt(window)} nt, so none fits the desktop limit."
    return {"counts": counts, "suggestion": suggestion, "message": message.strip(), "window": window,
            "limit": MAX_IRIS_NT}


def region_arms(hyb_paths: Sequence[Path], gene: str, sequence: str, start: int,
                end: int) -> Tuple[List[Dict[str, Any]], Dict[str, Any]]:
    """The arms of every chimera with both arms on ``gene`` inside
    ``start``-``end`` (region positions, 1 = ``start``, file order), and the
    region's diagnosis (:func:`diagnose`) from the same rows."""
    from . import comrades as _comrades

    hybfile = _comrades._hybfile()
    name = gene.encode()
    region = sequence[start - 1:end]
    arms: List[Dict[str, Any]] = []
    rows: List[Tuple[str, int, int, str, int, int]] = []
    total = 0
    skipped = {"invalidSequence": 0, "invalidRows": 0}
    for path in hyb_paths:
        for line in _comrades._lines(path):
            try:
                record = hybfile.parse(line)
            except ValueError:
                skipped["invalidRows"] += 1
                continue
            if record is None:
                continue
            total += 1
            if record.gene1 != name or record.gene2 != name:
                continue
            rows.append((gene, record.start1, record.end1, gene, record.start2, record.end2))
            if not (start <= record.start1 <= record.end1 <= end and start <= record.start2 <= record.end2 <= end):
                continue
            one = region[record.start1 - start:record.end1 - start + 1]
            two = region[record.start2 - start:record.end2 - start + 1]
            if set(one + two) - set("ACGU"):
                skipped["invalidSequence"] += 1
                continue
            arms.append({"oneStart": record.start1 - start + 1, "oneEnd": record.end1 - start + 1,
                         "twoStart": record.start2 - start + 1, "twoEnd": record.end2 - start + 1})
            if len(arms) > MAX_EVIDENCE_ROWS:
                raise ValueError(f"IRIS desktop limit: {_fmt(MAX_EVIDENCE_ROWS)} evidence rows. Select a smaller region.")
    found = diagnose(rows, total, gene, start, end, len(sequence))
    found["skipped"] = skipped
    return arms, found


def diagnose_indexes(index_paths: Sequence[Path], gene: str, start: int, end: int,
                     reference_length: Optional[int]) -> Dict[str, Any]:
    """:func:`diagnose` from the samples' analysis indexes (bridge operation
    ``analysis_iris_region``): the form's check before an IRIS run."""
    from hyb2analysis import query

    if not isinstance(gene, str) or not gene:
        raise ValueError("gene is required")
    if isinstance(start, bool) or isinstance(end, bool) or not isinstance(start, int) or not isinstance(end, int) \
            or not 1 <= start <= end:
        raise ValueError("start and end must be integers with 1 <= start <= end")
    total = 0
    rows: List[Tuple[str, int, int, str, int, int]] = []
    extent = 0
    for index in query._indexes(list(index_paths)):
        total += int(index.meta.get("records") or 0)
        gid = index.gene(gene)
        if gid is None:
            continue
        extent = max(extent, int(index.genes[gene][1] or 0))
        for s1, e1, s2, e2 in index.con.execute("SELECT s1, e1, s2, e2 FROM hybrids WHERE g1 = ? AND g2 = ? ORDER BY id",
                                                (gid, gid)):
            rows.append((gene, int(s1), int(e1), gene, int(s2), int(e2)))
    length = reference_length if reference_length else max(extent, end)
    found = diagnose(rows, total, gene, start, min(end, length), length)
    found["region"] = {"rna": gene, "start": start, "end": end}
    found["length"] = end - start + 1
    return found


# ---------------------------------------------------------------------------
# the structure task (structure.py dispatches engine "iris" here)
# ---------------------------------------------------------------------------

def _record(result: Dict[str, Any], region: str, start: int, end: int, gene: str) -> Dict[str, Any]:
    """A record in the shape of structure.fold_one's: the selected member
    (the heaviest) with every member as a candidate."""
    members = result["candidates"]
    selected = members[0]
    candidates = []
    for k, member in enumerate(members):
        candidates.append({
            "rank": k + 1, "type": "IRIS ensemble member", "topology": "nested", "energy": member["energy"],
            "effective_energy": None, "phase1_energy": None, "score": None, "structure": member["dotBracket"],
            "pairs": len(member["pairs"]), "pseudoknot_pairs": 0, "pairs_with_bonus": 0, "bonus": None,
            "weight": member["weight"], "coefficient": member["coefficient"], "constraints": member["constraints"],
        })
    iris = {
        "engineVersion": result["engineVersion"], "algorithm": result["algorithm"], "model": result["model"],
        "temperature": result["temperature"], "minimumLoop": result["minimumLoop"],
        "constraintMode": result["constraintMode"], "parameters": result["parameters"], "ensemble": result["ensemble"],
        "evidence": result["evidence"], "warnings": result["warnings"], "selectedMember": 0,
        "members": [{"member": k + 1, "weight": m["weight"], "coefficient": m["coefficient"], "energy": m["energy"],
                     "dotBracket": m["dotBracket"], "pairs": [list(p) for p in m["pairs"]],
                     "constraints": m["constraints"]} for k, m in enumerate(members)],
    }
    return {
        "structure": selected["dotBracket"], "energy": selected["energy"], "type": "IRIS ensemble member",
        "pairs": [(i, j, "()", None) for i, j in selected["pairs"]],
        "hard_pairs": selected["constraints"], "evidence": {"source": "iris-gaussian-support",
                                                          "inputRecords": result["evidence"]["inputRecords"]},
        "candidates": candidates, "candidates_found": len(candidates), "nucleotide_support": None,
        "iris": iris, "start": start, "end": end, "gene_id": gene,
    }


def structure_run(request: Dict[str, Any], project: Path, task_id: str, mode: str, state: Dict[str, Any],
                  started: float) -> Dict[str, Any]:
    """IRIS of one reference region (the whole RNA or one window), written as
    the other engines' results: structure.dbn/pairs.tsv/structure.ct hold the
    heaviest member, candidates.json every member and meta.json the ensemble."""
    from collections import Counter

    from . import structure as st
    from . import vienna as _vienna

    params = parameters(request.get("parameters"))
    inputs = st._evidence_inputs(project, request)
    gene_id, sequence = st._fold_sequence(request, inputs)
    sequence = sequence.upper().replace("T", "U")
    windows = st._windows(request.get("windows"), len(sequence)) if mode == "windows" else [(1, len(sequence))]
    if len(windows) != 1:
        raise ValueError("IRIS folds one reference region: give one window")
    start, end = windows[0]
    if end - start + 1 > MAX_IRIS_NT:
        raise ValueError(f"IRIS desktop limit is {_fmt(MAX_IRIS_NT)} nt; select a smaller region "
                         f"({gene_id}:{start}-{end} is {_fmt(end - start + 1)} nt)")
    region = sequence[start - 1:end]
    if set(region) - set("ACGU"):
        raise ValueError(f"IRIS requires 1–{_fmt(MAX_IRIS_NT)} canonical RNA nucleotides.")

    run_dir = st._resolve_run_dir(project, task_id, request.get("run_dir"))
    state.update(run_dir=run_dir, task_id=task_id)
    progress = st.ProgressWriter(run_dir)
    progress("iris", "Reading the chimeric reads", 2, 100, units="percent", force=True)
    t0 = time.monotonic()
    arms, found = region_arms(inputs["hyb_paths"], gene_id, sequence, start, end)
    evidence_s = time.monotonic() - t0
    if not arms:
        raise ValueError(found["message"] or "IRIS requires HYB evidence within the selected reference region.")
    t1 = time.monotonic()
    last = [-1]

    def report(stage: str, percent: int) -> None:
        progress("iris", stage, percent, 100, units="percent", force=percent != last[0] and not stage.startswith("Generating"))
        last[0] = percent

    result = run(region, arms, params, report)
    fold_s = time.monotonic() - t1
    record = _record(result, f"{gene_id}:{start}-{end}", start, end, gene_id)
    record["sequence"] = region
    record["seconds"] = {"bonus": round(evidence_s, 2), "fold": round(fold_s, 2)}
    blocks = Counter((a["oneStart"], a["oneEnd"], a["twoStart"], a["twoEnd"]) for a in arms)
    record["arms"] = st._arms_sample(blocks)

    progress("write", "writing results", None, None, units="none", force=True)
    out_dir = st._real_child_dir(project / "structures", task_id, exist_ok=True)
    stored = {"engine": "IRIS", "temperature": TEMPERATURE, "min_loop": MIN_LOOP, **params,
              "kRange": " ".join(str(k) for k in params["kRange"]), "hard_pairs": [
                  [start + i - 1, start + j - 1] for i, j in record["hard_pairs"]]}
    outputs = st._write_outputs(out_dir, [record], stored)
    summary_record = st._summary(record)
    summary_record["iris"] = record["iris"]
    meta = {
        "version": 1, "task_id": task_id, "mode": mode, "engine": "IRIS", "engine_version": result["engineVersion"],
        "vienna_version": _vienna.version(),
        "gene_id": gene_id, "reference": {"path": str(inputs["reference"]), "length": len(sequence)},
        "evidence_inputs": {"samples": inputs["samples"],
                            "hyb": [{"path": str(path), "size_bytes": path.stat().st_size} for path in inputs["hyb_paths"]],
                            "rows": "intramolecular chimeras (both arms on gene_id) inside the region; one Gaussian "
                                    "support block per row",
                            "diagnosis": found},
        "parameters": stored,
        "coordinates": "1-based inclusive positions on the gene_id reference sequence (windows, pairs.tsv i/j, ct column 6); "
                       "IRIS members and constraints in region positions",
        "windows": [{"start": start, "end": end}],
        "workers": 1, "records": [summary_record],
        "timings": {"evidence_s": round(evidence_s, 2), "fold_s": round(fold_s, 2),
                    "total_s": round(time.monotonic() - started, 2)},
        "created_at": st._now(),
    }
    st._atomic_write_json(out_dir / "meta.json", meta)
    outputs["meta"] = str(out_dir / "meta.json")
    relative_dir = f"structures/{task_id}"
    summary = {"records": 1, "pairs": summary_record["pairs"], "pairs_with_bonus": 0, "pseudoknot_pairs": 0,
               "energy": summary_record["energy"], "energies": [summary_record["energy"]],
               "length": summary_record["length"], "workers": 1, "timings": meta["timings"], "engine": "IRIS",
               "members": len(result["candidates"]), "weight": result["candidates"][0]["weight"]}
    out = {
        "ok": True, "status": "succeeded", "task_id": task_id, "run_dir": str(run_dir),
        "output": outputs["dbn"], "output_dir": str(out_dir), "outputs": outputs,
        "performance": {"lengths": [end - start + 1], "engine": "iris", "workers": 1, "seconds": round(fold_s, 2)},
        "structure": {"id": task_id, "task_id": task_id, "sample_ids": [sample["id"] for sample in inputs["samples"]],
                      "gene_id": gene_id, "mode": mode, "windows": meta["windows"], "parameters": stored,
                      "created_at": meta["created_at"], "path": relative_dir,
                      "paths": {key: f"{relative_dir}/{Path(value).name}" for key, value in outputs.items()},
                      "summary": summary},
        "summary": summary,
    }
    progress("done", "finished", None, None, units="none", force=True)
    st._atomic_write_json(run_dir / "result.json", out)
    return out
