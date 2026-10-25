"""COMRADES(ViennaRNA): the web RNA Structure page's HYB-guided ViennaRNA
mode (web/comrades-analysis.js and the comrades-fold path of
web/structure.worker.js), for one region of one RNA.

Each eligible chimera (both arms inside the region) is cofolded with
RNAcofold; the intermolecular pairs it forms are counted as evidence, the
strongest 1,001 are merged into stems, the stems become hard-pair
constraints, and ViennaRNA MFE folds are fitted with the constraints added
one by one (in rank order, then in seeded random orders). The structure
whose pairs carry the most evidence wins.
"""
from __future__ import annotations

import gzip
import math
import sys
import time
from pathlib import Path
from typing import Any, Callable, Dict, Iterable, List, Optional, Sequence, Tuple

from . import vienna

ORIGINAL_EVIDENCE_LIMIT = 1001
MAX_EVIDENCE_RECORDS = 50_000
MAX_RANDOM_FOLDS = 1000
# The web folds at most 3,000 nt; each accepted stem refolds the region.
MAX_COMRADES_NT = 3000
CANONICAL = vienna.CANONICAL
DEFAULTS = {"temperature": 37.0, "min_loop": 3, "constraint_limit": 75, "random_folds": 0, "seed": "HYB2-Web",
            "homodimer_only": False}


def parameters(raw: Any) -> Dict[str, Any]:
    """The web's COMRADES(ViennaRNA) settings, checked as structure-controller.js."""
    raw = raw if isinstance(raw, dict) else {}
    known = {"temperature", "min_loop", "constraint_limit", "random_folds", "seed", "homodimer_only"}
    unknown = set(raw) - known
    if unknown:
        raise ValueError("unknown COMRADES parameter: " + ", ".join(sorted(unknown)))
    base = vienna.parameters({k: raw[k] for k in ("temperature", "min_loop") if k in raw})
    out = dict(DEFAULTS, temperature=base["temperature"], min_loop=base["min_loop"])
    for key, lo, hi, label in (("constraint_limit", 1, 75, "Evidence stem constraints"),
                               ("random_folds", 0, MAX_RANDOM_FOLDS, "Randomised folds")):
        if key in raw:
            value = raw[key]
            if isinstance(value, bool) or not isinstance(value, int) or not lo <= value <= hi:
                raise ValueError(f"{label} must be a whole number from {lo} to {hi}.")
            out[key] = value
    if "seed" in raw:
        if not isinstance(raw["seed"], str) or not raw["seed"].strip(_JS_SPACE):
            raise ValueError("Enter a reproducibility seed for randomised constraint ordering.")
        out["seed"] = js_seed(raw["seed"])
    if "homodimer_only" in raw:
        if not isinstance(raw["homodimer_only"], bool):
            raise ValueError("homodimer_only must be a boolean")
        out["homodimer_only"] = raw["homodimer_only"]
    return out


# ---------------------------------------------------------------------------
# evidence arms (prepareReferenceAssembly for one region)
# ---------------------------------------------------------------------------

def _hybfile():
    try:
        from hyb2analysis import hybfile  # the pipeline package, staged with the runtime
    except ImportError:
        root = Path(__file__).resolve().parents[2] / "pipeline"
        sys.path.insert(0, str(root))
        from hyb2analysis import hybfile
    return hybfile


def _lines(path: Path) -> Iterable[bytes]:
    opener = gzip.open if path.name.endswith(".gz") else open
    with opener(path, "rb") as handle:
        yield from handle


def evidence_arms(hyb_paths: Sequence[Path], gene: str, sequence: str, start: int, end: int,
                  homodimer_only: bool = False) -> Tuple[List[Dict[str, Any]], Dict[str, int]]:
    """The arms of every record with both arms on ``gene`` inside
    ``start``-``end``, in region positions (1 = ``start``), file order."""
    hybfile = _hybfile()
    name = gene.encode()
    arms: List[Dict[str, Any]] = []
    skipped = {"outsideSelectedRegions": 0, "homodimerFilter": 0, "invalidSequence": 0, "invalidRows": 0}
    region = sequence[start - 1:end]
    for path in hyb_paths:
        for line in _lines(path):
            try:
                record = hybfile.parse(line)
            except ValueError:
                skipped["invalidRows"] += 1
                continue
            if record is None:
                continue
            homodimer = record.gene1 == record.gene2 and record.overlap is not None and record.overlap >= 5
            if homodimer_only and not homodimer:
                skipped["homodimerFilter"] += 1
                continue
            if not (record.gene1 == name and record.gene2 == name and start <= record.start1 and record.end1 <= end
                    and start <= record.start2 and record.end2 <= end):
                skipped["outsideSelectedRegions"] += 1
                continue
            one = region[record.start1 - start:record.end1 - start + 1]
            two = region[record.start2 - start:record.end2 - start + 1]
            if set(one + two) - set("ACGU"):
                skipped["invalidSequence"] += 1
                continue
            arms.append({"one": one, "two": two, "oneStart": record.start1 - start + 1, "twoStart": record.start2 - start + 1})
            if len(arms) > MAX_EVIDENCE_RECORDS:
                raise ValueError(f"This region contains more than {MAX_EVIDENCE_RECORDS} eligible HYB rows. "
                                 "Select a smaller region before running RNAcofold evidence generation.")
    return arms, skipped


# ---------------------------------------------------------------------------
# the analysis core (comrades-analysis.js)
# ---------------------------------------------------------------------------

def _dot_pairs(structure: str) -> List[Tuple[int, int]]:
    stack, pairs = [], []
    for position, ch in enumerate(structure, 1):
        if ch == "(":
            stack.append(position)
        elif ch == ")":
            pairs.append((stack.pop(), position))
    return sorted(pairs)


def cofold(one: str, two: str, temperature: float, min_loop: int) -> List[Tuple[int, int]]:
    """RNAcofold MFE pairs of one&two (hyb2_vienna_cofold), positions over one+two."""
    import RNA
    md = RNA.md()
    md.temperature = temperature
    md.min_loop_size = min_loop
    fc = RNA.fold_compound(one + "&" + two, md, RNA.OPTION_MFE | RNA.OPTION_HYBRID)
    structure, energy = fc.mfe_dimer()
    structure = structure.replace("&", "")
    if not math.isfinite(energy) or len(structure) != len(one) + len(two):
        raise ValueError("ViennaRNA RNAcofold returned an invalid two-strand structure.")
    return _dot_pairs(structure)


def map_cofold_pairs(job: Dict[str, Any], pairs: Sequence[Tuple[int, int]]) -> List[Tuple[int, int]]:
    first, total = len(job["one"]), len(job["one"]) + len(job["two"])
    return [(job["oneStart"] + left - 1, job["twoStart"] + right - first - 1)
            for left, right in pairs if 1 <= left <= first < right <= total]


def aggregate_evidence(mapped: Iterable[Tuple[int, int]]) -> List[Dict[str, int]]:
    counts: Dict[Tuple[int, int], int] = {}
    for one, two in mapped:
        if one >= 1 and two >= 1 and one != two:
            counts[(one, two)] = counts.get((one, two), 0) + 1
    entries = [{"one": one, "two": two, "count": n} for (one, two), n in counts.items()]
    return sorted(entries, key=lambda e: (-e["count"], e["one"], e["two"]))


def select_fragment_evidence(evidence, begin: int, end: int, limit: int = ORIGINAL_EVIDENCE_LIMIT):
    kept = [e for e in evidence if begin <= e["one"] <= end and begin <= e["two"] <= end]
    return sorted(kept, key=lambda e: (-e["count"], e["one"], e["two"]))[:limit]


def merge_touching_stems(evidence) -> List[Dict[str, Any]]:
    stems: List[Dict[str, Any]] = []
    for entry in sorted(evidence, key=lambda e: (e["one"], e["two"])):
        one, two, count = entry["one"], entry["two"], entry["count"]
        existing = next((s for s in stems if one == s["oneEnd"] + 1 and two == s["twoStart"] - 1), None)
        if existing:
            existing["oneStart"] = min(existing["oneStart"], one)
            existing["oneEnd"] = max(existing["oneEnd"], one)
            existing["twoStart"] = min(existing["twoStart"], two)
            existing["twoEnd"] = max(existing["twoEnd"], two)
            existing["length"] += 1
            existing["support"] += count
            existing["evidencePairs"].append({"one": one, "two": two, "count": count})
        else:
            stems.append({"oneStart": one, "oneEnd": one, "twoStart": two, "twoEnd": two, "length": 1, "support": count,
                          "evidencePairs": [{"one": one, "two": two, "count": count}]})
    stems.sort(key=lambda s: (-s["support"], -s["length"], s["oneStart"], s["twoStart"]))
    return [dict(stem, rank=index + 1) for index, stem in enumerate(stems)]


def _expand(constraint) -> List[Tuple[int, int]]:
    return [(constraint["left"] + k, constraint["right"] - k) for k in range(constraint["length"])]


def stems_to_constraints(stems, limit: int) -> List[Dict[str, Any]]:
    out = []
    for stem in stems:
        overlap = 1 + min(stem["oneEnd"], stem["twoEnd"]) - max(stem["oneStart"], stem["twoStart"])
        if overlap > 0:
            continue
        if len(out) == limit:
            break
        c = sorted((stem["oneStart"], stem["oneEnd"], stem["twoStart"], stem["twoEnd"]))
        constraint = {"id": f"F{len(out) + 1}", "rank": stem["rank"], "left": c[0], "right": c[3],
                      "length": c[1] - c[0] + 1, "support": stem["support"]}
        constraint["pairs"] = _expand(constraint)
        out.append(constraint)
    return out


def flatten(constraints) -> List[Tuple[int, int]]:
    pairs = [pair for constraint in constraints for pair in constraint["pairs"]]
    return sorted(pairs, key=lambda p: (p[0], -p[1]))


def validate_constraint_set(constraints, sequence: str, min_loop: int) -> Tuple[bool, str, List[Tuple[int, int]]]:
    pairs = flatten(constraints)
    endpoints = set()
    for index, (left, right) in enumerate(pairs):
        if left < 1 or right > len(sequence) or left >= right:
            return False, "outside-sequence", pairs
        if right - left - 1 < min_loop:
            return False, "minimum-loop", pairs
        if sequence[left - 1] + sequence[right - 1] not in CANONICAL:
            return False, "non-canonical", pairs
        if left in endpoints or right in endpoints:
            return False, "reused-endpoint", pairs
        endpoints.update((left, right))
        for other_left, other_right in pairs[:index]:
            if other_left < left < other_right < right or left < other_left < right < other_right:
                return False, "crossing-pairs", pairs
    return True, "", pairs


def _imul(a: int, b: int) -> int:
    """JavaScript's Math.imul: the low 32 bits of a*b, as a signed integer."""
    product = (a * b) & 0xFFFFFFFF
    return product - 0x100000000 if product & 0x80000000 else product


# JavaScript's String.prototype.trim whitespace (not Python's: no \x1c-\x1f
# or \x85, but U+FEFF).
_JS_SPACE = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"


def js_seed(text: str) -> str:
    """The web's ``String(seed).trim().slice(0, 128)``: 128 UTF-16 code units.
    A cut through an emoji leaves half of it, as in the web; that lone
    surrogate is hashed (hash_seed) but cannot be written as UTF-8, so
    seed_label shows it."""
    units = text.strip(_JS_SPACE).encode("utf-16-le", "surrogatepass")[:256]
    return units.decode("utf-16-le", "surrogatepass")


def seed_label(seed: str) -> str:
    """The seed as text to store and show: a lone surrogate as U+FFFD."""
    return "".join("\ufffd" if 0xD800 <= ord(char) <= 0xDFFF else char for char in seed)


def hash_seed(text: str) -> int:
    """FNV-1a over the UTF-16 code units (JavaScript's charCodeAt)."""
    data = text.encode("utf-16-le", "surrogatepass")
    value = 2166136261
    for k in range(0, len(data), 2):
        value = (value ^ (data[k] | data[k + 1] << 8)) & 0xFFFFFFFF
        value = _imul(value, 16777619) & 0xFFFFFFFF
    return value


def mulberry32(seed: int) -> Callable[[], float]:
    state = [seed & 0xFFFFFFFF]

    def random() -> float:
        state[0] = (state[0] + 0x6D2B79F5) & 0xFFFFFFFF
        value = state[0]
        value = _imul(value ^ (value >> 15), value | 1) & 0xFFFFFFFF
        value ^= (value + (_imul(value ^ (value >> 7), value | 61) & 0xFFFFFFFF)) & 0xFFFFFFFF
        return ((value ^ (value >> 14)) & 0xFFFFFFFF) / 4294967296

    return random


def seeded_shuffle(items: Sequence[Any], seed: str) -> List[Any]:
    shuffled = list(items)
    random = mulberry32(hash_seed(seed))
    for index in range(len(shuffled) - 1, 0, -1):
        swap = math.floor(random() * (index + 1))
        shuffled[index], shuffled[swap] = shuffled[swap], shuffled[index]
    return shuffled


def score_structure_pairs(pairs, evidence, length: int) -> Dict[str, Any]:
    support: Dict[Tuple[int, int], int] = {}
    for e in evidence:
        key = (min(e["one"], e["two"]), max(e["one"], e["two"]))
        support[key] = support.get(key, 0) + e["count"]
    matched, matched_support, per_pair = 0, 0, []
    for left, right in pairs:
        s = support.get((min(left, right), max(left, right)), 0)
        per_pair.append(s)
        if s > 0:
            matched += 1
            matched_support += s
    return {"pair_support": per_pair, "matched_pairs": matched, "matched_support": matched_support,
            "score": matched_support * 2}


# ---------------------------------------------------------------------------
# the fold (runComradesFold)
# ---------------------------------------------------------------------------

def _fit(sequence: str, ordered, params: Dict[str, Any]):
    accepted, rejected, result = [], [], None
    for candidate in ordered:
        ok, reason, pairs = validate_constraint_set(accepted + [candidate], sequence, params["min_loop"])
        if not ok:
            rejected.append({"id": candidate["id"], "rank": candidate["rank"], "reason": reason})
            continue
        try:
            result = vienna.fold(sequence, params, pairs)
            accepted.append(candidate)
        except ValueError:
            rejected.append({"id": candidate["id"], "rank": candidate["rank"], "reason": "vienna-incompatible"})
    if result is None:
        result = vienna.fold(sequence, params, [])
    return result, accepted, rejected


def fold(sequence: str, arms: Sequence[Dict[str, Any]], params: Dict[str, Any],
         progress: Optional[Callable[[str, int, int], None]] = None) -> Dict[str, Any]:
    """The web's COMRADES(ViennaRNA) result for one region: a record in the
    shape of structure.fold_one's, with the evidence and the ensemble."""
    started = time.monotonic()
    sequence = sequence.upper().replace("T", "U")
    if not arms:
        raise ValueError("No HYB records have both arms inside the selected region.")
    mapped: List[Tuple[int, int]] = []
    with_pairs = 0
    cache: Dict[Tuple[str, str], List[Tuple[int, int]]] = {}
    for index, job in enumerate(arms):
        key = (job["one"], job["two"])
        if key not in cache:
            cache[key] = cofold(job["one"], job["two"], params["temperature"], params["min_loop"])
        pairs = map_cofold_pairs(job, cache[key])
        if pairs:
            with_pairs += 1
            mapped.extend(pairs)
        if progress and (index == len(arms) - 1 or index % max(1, len(arms) // 80) == 0):
            progress("RNAcofold evidence", index + 1, len(arms))
    all_evidence = aggregate_evidence(mapped)
    selected = select_fragment_evidence(all_evidence, 1, len(sequence))
    stems = merge_touching_stems(selected)
    constraints = stems_to_constraints(stems, params["constraint_limit"])
    if not selected or not constraints:
        raise ValueError("RNAcofold did not produce a non-overlapping base-pair stem that can constrain the selected region.")

    candidates = []

    def annotate(fitted, run: int, randomised: bool):
        result, accepted, rejected = fitted
        pairs = [(i, j) for i, j, _, _ in result["pairs"]]
        score = score_structure_pairs(pairs, selected, len(sequence))
        candidates.append({"result": result, "accepted": accepted, "rejected": rejected, "run": run,
                           "randomised": randomised, "score": score})

    annotate(_fit(sequence, constraints, params), 0, False)
    for run in range(1, params["random_folds"] + 1):
        annotate(_fit(sequence, seeded_shuffle(constraints, f"{params['seed']}:{run}"), params), run, True)
        if progress:
            progress("randomised folds", run, params["random_folds"])
    candidates.sort(key=lambda c: (-c["score"]["score"], -c["score"]["matched_pairs"], c["result"]["energy"], c["run"]))
    best = candidates[0]
    result = dict(best["result"])
    support = best["score"]["pair_support"]
    # the pair "bonus" the desktop shades arcs by: each pair's RNAcofold evidence count
    result["pairs"] = [(i, j, bracket, float(support[k])) for k, (i, j, bracket, _) in enumerate(result["pairs"])]
    result["type"] = "COMRADES(ViennaRNA)"
    result["hard_pairs"] = [list(p) for p in flatten(best["accepted"])]
    result["evidence"] = {
        "source": "hyb-rna-cofold-evidence", "inputRecords": len(arms), "cofoldsWithIntermolecularPairs": with_pairs,
        "mappedPairObservations": len(mapped), "uniqueBasePairs": len(all_evidence),
        "selectedBasePairs": len(selected), "rankedStems": len(stems), "requestedStemConstraints": len(constraints),
        "acceptedStemConstraints": [{k: c[k] for k in ("id", "rank", "left", "right", "length", "support")} for c in best["accepted"]],
        "rejectedStemConstraints": best["rejected"], "evidenceLimit": ORIGINAL_EVIDENCE_LIMIT,
        "evidenceWeight": "one per eligible HYB row", "comradesScore": best["score"]["score"],
        "matchedEvidencePairs": best["score"]["matched_pairs"],
    }
    result["randomisation"] = {
        "requestedFolds": params["random_folds"], "seed": seed_label(params["seed"]), "selectedRun": best["run"],
        "selectedRunWasRandomised": best["randomised"],
        "structures": [{"run": c["run"], "randomised": c["randomised"], "energy": c["result"]["energy"],
                        "comradesScore": c["score"]["score"], "matchedEvidencePairs": c["score"]["matched_pairs"],
                        "acceptedStemConstraints": [a["id"] for a in c["accepted"]],
                        "rejectedStemConstraints": len(c["rejected"]), "dotBracket": c["result"]["structure"]}
                       for c in candidates],
    }
    result["seconds"] = {"bonus": 0.0, "fold": round(time.monotonic() - started, 2)}
    return result
