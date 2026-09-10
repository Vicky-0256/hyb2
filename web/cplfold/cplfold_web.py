"""Small JSON bridge between the HYB2 web worker and pure-Python CPLfold.

The browser build intentionally runs without Numba.  Pyodide does not ship a
Numba package, so the decorators used by the upstream parser are replaced by
identity decorators before CPLfold is imported.  The algorithm itself is not
changed; only JIT compilation is disabled.
"""

from __future__ import annotations

import json
import math
import os
import re
import sys
import types
from collections import Counter
from typing import Any


CPLFOLD_SOURCE_REVISION = "af49f8e"
BRIDGE_VERSION = "4"
DEFAULT_MAX_SEQUENCE_LENGTH = 75
BROWSER_HARD_MAX_SEQUENCE_LENGTH = 500
BONUS_EXPORT_MAX_SEQUENCE_LENGTH = 5_000
MAX_EVIDENCE_RECORDS = 50_000
ENERGY_MODELS = {"DP03", "DP09", "CC06", "CC09", "RE"}
BRACKETS = {"(": ")", "[": "]", "{": "}", "<": ">"}
LAYER_NAMES = {
    "(": "primary",
    "[": "pseudoknot-1",
    "{": "pseudoknot-2",
    "<": "pseudoknot-3",
}


def _install_numba_fallback() -> None:
    """Expose the subset of numba used by CPLfold as identity decorators."""

    force_fallback = os.environ.get("CPLFOLD_FORCE_PURE_PYTHON") == "1"
    if not force_fallback:
        try:
            import numba  # noqa: F401

            return
        except ImportError:
            pass

    module = types.ModuleType("numba")

    def njit(*decorator_args: Any, **_decorator_kwargs: Any):
        if len(decorator_args) == 1 and callable(decorator_args[0]):
            return decorator_args[0]

        def decorate(function):
            return function

        return decorate

    module.njit = njit
    sys.modules["numba"] = module


_install_numba_fallback()

import numpy as np  # noqa: E402
from CPLfold import two_phase_pseudoknot_fold  # noqa: E402


def _number(value: Any, minimum: float, maximum: float, label: str) -> float:
    if isinstance(value, bool):
        raise ValueError(f"{label} must be between {minimum} and {maximum}.")
    try:
        parsed = float(value)
    except (TypeError, ValueError) as error:
        raise ValueError(f"{label} must be between {minimum} and {maximum}.") from error
    if not math.isfinite(parsed) or parsed < minimum or parsed > maximum:
        raise ValueError(f"{label} must be between {minimum} and {maximum}.")
    return parsed


def _integer(value: Any, minimum: int, maximum: int, label: str) -> int:
    parsed = _number(value, minimum, maximum, label)
    if not parsed.is_integer():
        raise ValueError(f"{label} must be a whole number from {minimum} to {maximum}.")
    return int(parsed)


def _sequence(value: Any, maximum: int = DEFAULT_MAX_SEQUENCE_LENGTH) -> str:
    sequence = re.sub(r"\s+", "", str(value or "")).upper().replace("T", "U")
    if not sequence:
        raise ValueError("CPLfold requires an RNA sequence.")
    if re.search(r"[^ACGU]", sequence):
        raise ValueError("CPLfold accepts only A, C, G and U.")
    if len(sequence) > BROWSER_HARD_MAX_SEQUENCE_LENGTH:
        raise ValueError(
            f"Browser CPLfold has a hard safety ceiling of {BROWSER_HARD_MAX_SEQUENCE_LENGTH} nt in this pure-Python Pyodide build."
        )
    if len(sequence) > maximum:
        raise ValueError(
            f"Browser CPLfold is limited to {maximum} nt for this capacity-tested request."
        )
    return sequence


def _bonus_export_sequence(value: Any) -> str:
    sequence = re.sub(r"\s+", "", str(value or "")).upper().replace("T", "U")
    if not sequence:
        raise ValueError("CPLfold requires an RNA sequence.")
    if re.search(r"[^ACGU]", sequence):
        raise ValueError("CPLfold accepts only A, C, G and U.")
    if len(sequence) > BONUS_EXPORT_MAX_SEQUENCE_LENGTH:
        raise ValueError(
            f"Bonus-matrix export is limited to {BONUS_EXPORT_MAX_SEQUENCE_LENGTH:,} nt in the browser."
        )
    return sequence


def _request_maximum(value: Any) -> int:
    return _integer(
        DEFAULT_MAX_SEQUENCE_LENGTH if value is None else value,
        1,
        BROWSER_HARD_MAX_SEQUENCE_LENGTH,
        "Browser CPLfold maximum sequence length",
    )


def _normalise_arms(raw_arms: Any, sequence_length: int) -> list[tuple[int, int, int, int]]:
    if raw_arms is None:
        return []
    if not isinstance(raw_arms, list):
        raise ValueError("HYB evidence arms must be an array.")
    if len(raw_arms) > MAX_EVIDENCE_RECORDS:
        raise ValueError(
            f"This region contains more than {MAX_EVIDENCE_RECORDS:,} eligible HYB rows. Select a smaller region."
        )

    arms: list[tuple[int, int, int, int]] = []
    for index, arm in enumerate(raw_arms):
        if not isinstance(arm, dict):
            raise ValueError(f"HYB evidence row {index + 1} is invalid.")
        try:
            one_start = int(arm["oneStart"])
            one_end = int(arm["oneEnd"])
            two_start = int(arm["twoStart"])
            two_end = int(arm["twoEnd"])
        except (KeyError, TypeError, ValueError) as error:
            raise ValueError(f"HYB evidence row {index + 1} has invalid prepared coordinates.") from error
        if not (
            1 <= one_start <= one_end <= sequence_length
            and 1 <= two_start <= two_end <= sequence_length
        ):
            raise ValueError(f"HYB evidence row {index + 1} falls outside the prepared sequence.")
        arms.append((one_start, one_end, two_start, two_end))
    return arms


def build_hyb_bonus_matrix(
    sequence_length: int, arms: list[tuple[int, int, int, int]]
) -> tuple[np.ndarray | None, dict[str, Any]]:
    """Reproduce CPLfold's IRIS-style HYB/PARIS block-to-bonus transform.

    Coordinates are 1-based inclusive in the web data model.  They are
    converted to zero-based half-open block centres before Gaussian spreading,
    matching ``extract_paris_scores.py``.  Every eligible HYB row contributes
    once; duplicate coordinate blocks are compressed with an integer weight.
    """

    if not arms:
        return None, {
            "source": "none",
            "inputRecords": 0,
            "uniqueBlocks": 0,
            "nonzeroBonusCells": 0,
            "nonzeroUpperTriangleCells": 0,
            "maximumBonus": 0.0,
            "bonusEntries": [],
            "evidenceWeight": "none",
        }

    arm_lengths = [end - start + 1 for arm in arms for start, end in ((arm[0], arm[1]), (arm[2], arm[3]))]
    standard_deviation = max(float(np.mean(arm_lengths)) / 6.0, 1e-6)
    positions = np.arange(sequence_length, dtype=np.float32)
    support = np.zeros((sequence_length, sequence_length), dtype=np.float32)
    compressed = Counter(arms)

    for (one_start, one_end, two_start, two_end), weight in compressed.items():
        one_mean = ((one_start - 1) + one_end) / 2.0
        two_mean = ((two_start - 1) + two_end) / 2.0
        one_support = np.exp(-0.5 * ((positions - one_mean) / standard_deviation) ** 2)
        two_support = np.exp(-0.5 * ((positions - two_mean) / standard_deviation) ** 2)
        one_support /= np.max(one_support)
        two_support /= np.max(two_support)
        contribution = np.outer(one_support, two_support)
        support += np.float32(weight) * (contribution + contribution.T)

    support[support < np.float32(1e-6)] = np.float32(0.0)
    bonus = np.log1p(support).astype(np.float32, copy=False)
    maximum = float(np.max(bonus)) if bonus.size else 0.0
    nucleotide_support = np.max(bonus, axis=1).tolist() if bonus.size else []
    bonus_entries = [
        {"one": int(one + 1), "two": int(two + 1), "value": float(bonus[one, two])}
        for one, two in zip(*np.nonzero(np.triu(bonus, k=1)))
    ]
    return bonus, {
        "source": "hyb-block-intervals",
        "transform": "IRIS-style Gaussian arm blocks; symmetric outer product; threshold 1e-6; log1p",
        "coordinateSystem": "prepared-sequence, 1-based inclusive input",
        "inputRecords": len(arms),
        "uniqueBlocks": len(compressed),
        "meanArmLength": float(np.mean(arm_lengths)),
        "gaussianStandardDeviation": standard_deviation,
        "nonzeroBonusCells": int(np.count_nonzero(bonus)),
        "nonzeroUpperTriangleCells": len(bonus_entries),
        "maximumBonus": maximum,
        "bonusEntries": bonus_entries,
        "nucleotideSupport": nucleotide_support,
        "evidenceWeight": "one per eligible HYB row; overlap_score and collapsed raw-read count are not weights",
    }


def build_hyb_bonus_matrix_export(
    sequence_length: int, arms: list[tuple[int, int, int, int]]
) -> dict[str, Any]:
    """Build the sparse TSV representation without allocating an n-by-n array.

    Folding still uses ``build_hyb_bonus_matrix`` because the parser needs a
    dense matrix.  Downloads only need non-zero upper-triangle cells, so this
    path keeps the same float32 arithmetic while avoiding a second quadratic
    allocation and permits local handoff for sequences above the browser fold
    ceiling.
    """

    if not arms:
        return {
            "source": "none",
            "inputRecords": 0,
            "uniqueBlocks": 0,
            "nonzeroBonusCells": 0,
            "nonzeroUpperTriangleCells": 0,
            "maximumBonus": 0.0,
            "bonusEntries": [],
            "evidenceWeight": "none",
        }

    arm_lengths = [
        end - start + 1
        for arm in arms
        for start, end in ((arm[0], arm[1]), (arm[2], arm[3]))
    ]
    standard_deviation = max(float(np.mean(arm_lengths)) / 6.0, 1e-6)
    positions = np.arange(sequence_length, dtype=np.float32)
    compressed = Counter(arms)
    upper_support: dict[tuple[int, int], np.float32] = {}
    diagonal_support = np.zeros(sequence_length, dtype=np.float32)

    for (one_start, one_end, two_start, two_end), weight in compressed.items():
        one_mean = ((one_start - 1) + one_end) / 2.0
        two_mean = ((two_start - 1) + two_end) / 2.0
        one_support = np.exp(-0.5 * ((positions - one_mean) / standard_deviation) ** 2)
        two_support = np.exp(-0.5 * ((positions - two_mean) / standard_deviation) ** 2)
        one_support /= np.max(one_support)
        two_support /= np.max(two_support)
        active = np.flatnonzero((one_support > 0) | (two_support > 0))
        weight32 = np.float32(weight)

        for position in active:
            diagonal_contribution = weight32 * np.float32(
                np.float32(one_support[position] * two_support[position]) * np.float32(2.0)
            )
            diagonal_support[position] = np.float32(
                diagonal_support[position] + diagonal_contribution
            )

        for offset, left in enumerate(active[:-1]):
            for right in active[offset + 1:]:
                contribution = weight32 * np.float32(
                    np.float32(one_support[left] * two_support[right])
                    + np.float32(one_support[right] * two_support[left])
                )
                if contribution == 0:
                    continue
                key = (int(left), int(right))
                upper_support[key] = np.float32(
                    upper_support.get(key, np.float32(0.0)) + contribution
                )

    threshold = np.float32(1e-6)
    diagonal_bonus = np.log1p(np.where(diagonal_support >= threshold, diagonal_support, 0.0)).astype(
        np.float32, copy=False
    )
    entries = []
    for (left, right), support in sorted(upper_support.items()):
        if support < threshold:
            continue
        entries.append({
            "one": left + 1,
            "two": right + 1,
            "value": float(np.log1p(np.float32(support))),
        })

    maximum = float(np.max(diagonal_bonus)) if diagonal_bonus.size else 0.0
    if entries:
        maximum = max(maximum, max(float(entry["value"]) for entry in entries))
    nonzero_diagonal = int(np.count_nonzero(diagonal_bonus))
    return {
        "source": "hyb-block-intervals",
        "transform": "IRIS-style Gaussian arm blocks; symmetric outer product; threshold 1e-6; log1p",
        "coordinateSystem": "prepared-sequence, 1-based inclusive input",
        "inputRecords": len(arms),
        "uniqueBlocks": len(compressed),
        "meanArmLength": float(np.mean(arm_lengths)),
        "gaussianStandardDeviation": standard_deviation,
        "nonzeroBonusCells": nonzero_diagonal + 2 * len(entries),
        "nonzeroUpperTriangleCells": len(entries),
        "maximumBonus": maximum,
        "bonusEntries": entries,
        "evidenceWeight": "one per eligible HYB row; overlap_score and collapsed raw-read count are not weights",
    }


def _pair_type(sequence: str, left: int, right: int) -> str:
    return f"{sequence[left - 1]}–{sequence[right - 1]}"


def _pairs(sequence: str, structure: str, bonus_matrix: np.ndarray | None) -> list[dict[str, Any]]:
    stacks = {opening: [] for opening in BRACKETS}
    closing = {closing: opening for opening, closing in BRACKETS.items()}
    pairs: list[dict[str, Any]] = []

    for offset, character in enumerate(structure):
        position = offset + 1
        if character in stacks:
            stacks[character].append(position)
        elif character in closing:
            opening = closing[character]
            if not stacks[opening]:
                raise ValueError("CPLfold returned unbalanced dot-bracket notation.")
            left = stacks[opening].pop()
            bonus = float(bonus_matrix[left - 1, position - 1]) if bonus_matrix is not None else 0.0
            pairs.append(
                {
                    "left": left,
                    "right": position,
                    "leftBase": sequence[left - 1],
                    "rightBase": sequence[position - 1],
                    "type": _pair_type(sequence, left, position),
                    "layer": LAYER_NAMES[opening],
                    "bracket": opening + character,
                    "evidenceSupport": bonus,
                    "bonusSupport": bonus,
                }
            )

    if any(stacks.values()):
        raise ValueError("CPLfold returned unbalanced dot-bracket notation.")
    pairs.sort(key=lambda pair: (pair["left"], pair["right"]))
    return pairs


def _crossing_pair_count(pairs: list[dict[str, Any]]) -> int:
    crossing: set[tuple[int, int]] = set()
    for first_index, first in enumerate(pairs):
        for second in pairs[first_index + 1 :]:
            if (
                first["left"] < second["left"] < first["right"] < second["right"]
                or second["left"] < first["left"] < second["right"] < first["right"]
            ):
                crossing.add((first["left"], first["right"]))
                crossing.add((second["left"], second["right"]))
    return len(crossing)


def _candidate(
    sequence: str,
    raw: dict[str, Any],
    index: int,
    bonus_matrix: np.ndarray | None,
) -> dict[str, Any]:
    structure = str(raw.get("structure") or "")
    if len(structure) != len(sequence):
        raise ValueError("CPLfold returned a structure with the wrong length.")
    pairs = _pairs(sequence, structure, bonus_matrix)
    crossing_pairs = _crossing_pair_count(pairs)
    structure_type = str(raw.get("type") or "phase1")
    return {
        "index": index,
        "dotBracket": structure,
        "type": structure_type,
        "topology": "pseudoknotted" if crossing_pairs or structure_type == "pseudoknot" else "nested",
        "energy": raw.get("energy"),
        "effectiveEnergy": raw.get("effective_energy"),
        "phase1Energy": raw.get("phase1_energy"),
        "score": raw.get("score"),
        "phase1Structure": raw.get("phase1_struct"),
        "phase2Structure": raw.get("phase2_struct"),
        "pairs": pairs,
        "crossingPairs": crossing_pairs,
        "unpaired": len(sequence) - 2 * len(pairs),
    }


def fold(payload: dict[str, Any]) -> dict[str, Any]:
    maximum_sequence_length = _request_maximum(payload.get("maxSequenceLength"))
    sequence = _sequence(payload.get("sequence"), maximum_sequence_length)
    beam_size = _integer(payload.get("beamSize", 20), 1, 200, "Beam size")
    max_phase1 = _integer(payload.get("maxPhase1", 3), 1, 20, "Phase-1 candidate count")
    energy_delta = _number(payload.get("energyDelta", 5), 0.0, 50.0, "Energy delta")
    alpha = _number(payload.get("alpha", 0.5), 0.0, 1.0, "Evidence alpha")
    beta = _number(payload.get("beta", 0.0), 0.0, 1.0, "Pseudoknot beta")
    energy_model = str(payload.get("energyModel", "DP09")).upper()
    if energy_model not in ENERGY_MODELS:
        raise ValueError("Energy model must be one of DP03, DP09, CC06, CC09 or RE.")

    evidence_mode = str(payload.get("evidenceMode", "none"))
    if evidence_mode not in {"none", "hyb-blocks"}:
        raise ValueError("CPLfold evidence mode must be 'none' or 'hyb-blocks'.")
    arms = _normalise_arms(payload.get("evidenceArms"), len(sequence)) if evidence_mode == "hyb-blocks" else []
    if evidence_mode == "hyb-blocks" and not arms:
        raise ValueError("No eligible HYB rows are fully contained in the selected reference region.")
    bonus_matrix, evidence = build_hyb_bonus_matrix(len(sequence), arms)

    raw_candidates = two_phase_pseudoknot_fold(
        seq=sequence,
        beam_size=beam_size,
        energy_delta=energy_delta,
        max_phase1=max_phase1,
        max_phase2=1,
        energy_model=energy_model,
        output_file=None,
        verbose=False,
        lv=True,
        bonus_matrix=bonus_matrix,
        alpha=alpha,
        beta=beta,
    )
    candidates = [
        _candidate(sequence, candidate, index, bonus_matrix)
        for index, candidate in enumerate(raw_candidates)
    ]
    if not candidates:
        raise ValueError("CPLfold did not produce a structure for this sequence and parameter set.")

    best = candidates[0]
    nucleotide_support = evidence.pop("nucleotideSupport", [0.0] * len(sequence))
    return {
        "algorithm": "CPLfold two-phase pseudoknot prediction",
        "model": f"LinearFold Vienna-mode scoring with CPLfold/HotKnots {energy_model} energy ranking",
        "engine": "CPLfold",
        "engineVersion": CPLFOLD_SOURCE_REVISION,
        "bridgeVersion": BRIDGE_VERSION,
        "runtime": "Pyodide 0.29.4; Python 3.13.2; NumPy 2.2.5; Numba identity fallback",
        "maxSequenceLength": maximum_sequence_length,
        "sequence": sequence,
        "dotBracket": best["dotBracket"],
        "pairs": best["pairs"],
        "unpaired": best["unpaired"],
        "energy": best["energy"],
        "effectiveEnergy": best["effectiveEnergy"],
        "phase1Energy": best["phase1Energy"],
        "score": best["score"],
        "phase1Structure": best["phase1Structure"],
        "phase2Structure": best["phase2Structure"],
        "energyUnit": "kcal/mol",
        "structureType": best["type"],
        "topology": best["topology"],
        "crossingPairs": best["crossingPairs"],
        # The HYB matrix is a soft score bonus, not an enforced pair constraint.
        "constraintMode": "none",
        "constraintSource": "none",
        "constraintCount": 0,
        "constraints": [],
        "evidenceMode": evidence_mode,
        "evidenceSource": "hyb-block-bonus-matrix" if arms else "none",
        "selectedCandidate": 0,
        "candidates": candidates,
        "parameters": {
            "beamSize": beam_size,
            "energyDelta": energy_delta,
            "maxPhase1": max_phase1,
            "energyModel": energy_model,
            "alpha": alpha,
            "beta": beta,
            "linearFoldMode": "Vienna",
            "maxPhase2": 1,
        },
        "evidence": evidence,
        "nucleotideSupport": nucleotide_support,
        "maximumNucleotideSupport": max(nucleotide_support, default=0.0),
    }


def bonus_matrix(payload: dict[str, Any]) -> dict[str, Any]:
    """Prepare the exact sparse bonus matrix used by a guided local run."""

    sequence = _bonus_export_sequence(payload.get("sequence"))
    evidence_mode = str(payload.get("evidenceMode", "none"))
    if evidence_mode not in {"none", "hyb-blocks"}:
        raise ValueError("CPLfold evidence mode must be 'none' or 'hyb-blocks'.")
    arms = _normalise_arms(payload.get("evidenceArms"), len(sequence)) if evidence_mode == "hyb-blocks" else []
    if evidence_mode == "hyb-blocks" and not arms:
        raise ValueError("No eligible HYB rows are fully contained in the selected reference region.")
    evidence = build_hyb_bonus_matrix_export(len(sequence), arms)
    return {
        "engine": "CPLfold",
        "engineVersion": CPLFOLD_SOURCE_REVISION,
        "bridgeVersion": BRIDGE_VERSION,
        "sequence": sequence,
        "evidenceMode": evidence_mode,
        "evidence": evidence,
    }


def fold_json(payload_json: str) -> str:
    payload = json.loads(payload_json)
    if not isinstance(payload, dict):
        raise ValueError("The CPLfold request must be a JSON object.")
    return json.dumps(fold(payload), separators=(",", ":"), allow_nan=False)


def bonus_matrix_json(payload_json: str) -> str:
    payload = json.loads(payload_json)
    if not isinstance(payload, dict):
        raise ValueError("The CPLfold bonus-matrix request must be a JSON object.")
    return json.dumps(bonus_matrix(payload), separators=(",", ":"), allow_nan=False)


__all__ = [
    "build_hyb_bonus_matrix",
    "build_hyb_bonus_matrix_export",
    "bonus_matrix",
    "bonus_matrix_json",
    "fold",
    "fold_json",
]
