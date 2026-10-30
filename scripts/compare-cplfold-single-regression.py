#!/usr/bin/env python3
"""Compare valid single-chain predictions in a Git revision and the worktree.

The old tree is exported with git archive into a temporary directory. Separate
isolated interpreters import each implementation and use separate Numba caches.
No checkout, reset, native RNA package, network, or timing comparison is used.
"""

from __future__ import annotations

import argparse
import contextlib
from datetime import datetime, timezone
import hashlib
import io
import json
import math
import os
from pathlib import Path, PurePosixPath
import platform
import random
import re
import shlex
import subprocess
import sys
import tarfile
import tempfile
import traceback


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = Path(__file__).resolve()
DEFAULT_BASELINE = "aabae11"
KNOWN_PK = "GGCGCGGCACCGUCCGCGGAACAAACGG"
MODELS = ("DP09", "DP03", "CC06", "CC09", "RE")
HELIX = ((10, 28), (11, 27), (12, 26), (13, 25))  # 1-based canonical pairs.
REAL_DBN = "third_party/cplfold/data/bpRNA_RFAM_5220.dbn"
REAL_NPY = "third_party/cplfold/data/bpRNA_RFAM_5220_paris_scores.npy"
ATOL = 1e-9
EXPANSION_SEED = 20261027
EXPANSION_LENGTHS = (1, 2, 4, 7, 15, 30, 60, 90)
NUMERIC_FIELDS = ("energy", "effective_energy", "phase1_energy", "score")
REQUIRED_FIELDS = set(NUMERIC_FIELDS) | {
    "structure", "phase1_struct", "phase2_struct", "type",
}


def strict_loads(text):
    def reject(value):
        raise ValueError("Non-finite JSON constant: " + value)

    def finite_float(value):
        number = float(value)
        if not math.isfinite(number):
            raise ValueError("JSON number exceeds the finite range: " + value)
        return number

    return json.loads(text, parse_constant=reject, parse_float=finite_float)


def json_text(value):
    return json.dumps(value, indent=2, sort_keys=True, allow_nan=False) + "\n"


def sha_bytes(contents):
    return hashlib.sha256(contents).hexdigest()


def sha(path):
    return sha_bytes(path.read_bytes())


def source_manifest(repository):
    vendor = repository / "third_party/cplfold"
    files = [repository / "bin/cplfold"]
    files += [p for p in vendor.rglob("*") if p.is_file() and "__pycache__" not in p.parts
              and "tests" not in p.parts and (p.suffix == ".py"
              or "energy_params" in p.parts or p.relative_to(repository).as_posix() in (REAL_DBN, REAL_NPY))]
    return {p.relative_to(repository).as_posix(): sha(p) for p in sorted(files)}


def export_baseline(revision, destination):
    commit = subprocess.run(
        ["git", "rev-parse", "--verify", revision + "^{commit}"], cwd=ROOT,
        check=True, capture_output=True, text=True,
    ).stdout.strip()
    archive = subprocess.run(
        ["git", "archive", "--format=tar", commit, "third_party/cplfold", "bin/cplfold"],
        cwd=ROOT, check=True, capture_output=True,
    ).stdout
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:") as stream:
        for member in stream.getmembers():
            name = PurePosixPath(member.name)
            if name.is_absolute() or ".." in name.parts:
                raise ValueError("Unsafe Git archive member: " + member.name)
            target = destination.joinpath(*name.parts)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            elif member.isfile():
                target.parent.mkdir(parents=True, exist_ok=True)
                with stream.extractfile(member) as source:
                    target.write_bytes(source.read())
                target.chmod(member.mode & 0o777)
            else:
                raise ValueError("Unexpected non-file Git archive member: " + member.name)
    return commit, sha_bytes(archive)


def make_cases(real_sequence):
    cases = []
    defaults = dict(beam_size=20, energy_delta=5.0, max_phase1=5, max_phase2=2,
                    energy_model="DP09", alpha=0.0, beta=0.0,
                    allow_pseudoknot=True, lv=True)

    def add(identifier, sequence=KNOWN_PK, bonus=None, **changes):
        cases.append(dict(id=identifier, sequence=sequence, length=len(sequence),
                          bonus=bonus, options=dict(defaults, **changes)))

    for name, sequence in (("unpaired", "AAAAAAAA"), ("hairpin", "GGGAAACCC"),
                           ("short_stem", "GCGAAAACGC"), ("small", "GCAAAAGC"),
                           ("known_pk", KNOWN_PK)):
        for enabled in (False, True):
            add("control_" + name + ("_pk" if enabled else "_nested"), sequence,
                allow_pseudoknot=enabled)
    for model in MODELS:
        for beta in (0.0, 0.3, 1.0):
            add(f"model_{model}_beta_{beta:g}", energy_model=model, beta=beta)
    for alpha in (0.0, 0.5, 1.0):
        add(f"alpha_helix_{alpha:g}", bonus="synthetic_helix", alpha=alpha)
    for beam in (1, 20, 100, 0):
        for maximum in (1, 5):
            for delta in (0.0, 5.0):
                add(f"search_b{beam}_n{maximum}_d{delta:g}", bonus="synthetic_helix",
                    beam_size=beam, max_phase1=maximum, energy_delta=delta, alpha=0.5, beta=0.3)
    rng = random.Random(522028)
    for index, length in enumerate((8, 12, 16, 20, 24, 28, 32, 36)):
        sequence = "".join(rng.choice("ACGU") for _ in range(length))
        add(f"random_{index + 1:02d}", sequence,
            beam_size=(1, 20, 100, 0)[index % 4], energy_delta=0.0 if index % 2 else 5.0,
            max_phase1=1 if index % 3 == 0 else 5, allow_pseudoknot=bool(index % 2),
            energy_model=MODELS[index % len(MODELS)])
    for enabled in (False, True):
        for alpha in (0.0, 0.5, 1.0):
            add(f"real_paris_{'pk' if enabled else 'nested'}_a{alpha:g}", real_sequence,
                bonus="real_paris", beam_size=100, alpha=alpha,
                beta=0.3 if enabled else 0.0, allow_pseudoknot=enabled,
                max_phase1=5 if enabled else 1, energy_delta=5.0 if enabled else 0.0)
    add("real_paris_beam20", real_sequence, bonus="real_paris", beam_size=20,
        alpha=0.5, allow_pseudoknot=False)
    add("real_paris_unpruned", real_sequence, bonus="real_paris", beam_size=0,
        alpha=0.5, allow_pseudoknot=False, max_phase1=1, energy_delta=0.0)
    add("contrafold_no_reads", lv=False)
    add("contrafold_with_reads", lv=False, bonus="synthetic_helix", alpha=0.5, beta=0.3)
    cases.extend(make_expanded_cases(cases, defaults))
    assert len({case["id"] for case in cases}) == len(cases)
    return cases


def make_expanded_cases(existing, defaults):
    """Append synthetic boundary/composition cases and explicit paired checks.

    The original cases are never mutated. Each random sequence has its own
    seed, so adding another case cannot alter an existing generated sequence.
    Fixed motifs and input transformations are labelled separately from draws.
    """
    added = []
    by_id = {case["id"]: case for case in existing}

    def append(identifier, sequence, category, seed, options, **metadata):
        canonical = sequence.upper().replace("T", "U")
        if not canonical or any(base not in "ACGU" for base in canonical):
            raise ValueError("Expanded cases must remain valid single-chain inputs")
        case = dict(id=identifier, sequence=sequence, length=len(sequence), bonus=None,
                    options=dict(options), synthetic=True, category=category, seed=seed,
                    actual_gc_fraction=sum(base in "GC" for base in canonical) / len(canonical),
                    **metadata)
        added.append(case)
        by_id[identifier] = case

    for length_index, length in enumerate(EXPANSION_LENGTHS):
        for composition_index, (composition, target_gc) in enumerate(
                (("low", 0.1), ("balanced", 0.5), ("high", 0.9))):
            seed = EXPANSION_SEED + 100 * length + composition_index
            rng = random.Random(seed)
            gc_count = round(length * target_gc)
            bases = [rng.choice("GC") for _ in range(gc_count)]
            bases += [rng.choice("AU") for _ in range(length - gc_count)]
            rng.shuffle(bases)
            options = dict(defaults,
                beam_size=(0, 1, 20, 100)[(2 * length_index + composition_index) % 4],
                max_phase1=1 if length <= 4 or (length_index + composition_index) % 3 == 0 else 5,
                energy_delta=0.0 if length <= 4 or composition_index == 0 else 5.0,
                allow_pseudoknot=(length_index + composition_index) % 2 == 0)
            append(f"expanded_gc_n{length}_{composition}", "".join(bases), "seeded_gc",
                   seed, options, target_gc_fraction=target_gc,
                   generation="Exact nearest-integer GC count, seeded base draws and shuffle")

    patterns = (
        (1, "U", 100, False), (2, "GC", 1, True), (4, "A", 20, False),
        (7, "C", 0, True), (15, "A", 1, False), (30, "AU", 20, True),
        (60, "GC", 100, False), (90, "GCAU", 20, True),
    )
    for index, (length, motif, beam, enabled) in enumerate(patterns):
        sequence = (motif * ((length + len(motif) - 1) // len(motif)))[:length]
        append(f"expanded_pattern_n{length}_{motif.lower()}", sequence,
               "homopolymer" if len(motif) == 1 else "repeated_motif",
               EXPANSION_SEED + 20000 + index,
               dict(defaults, beam_size=beam, allow_pseudoknot=enabled,
                    max_phase1=1 if length <= 7 else 5,
                    energy_delta=0.0 if length <= 7 else 5.0),
               motif=motif, generation="Fixed motif repetition and truncation; seed is provenance only")

    def paired(identifier, reference_id, kind, transform=None, **changes):
        reference = by_id[reference_id]
        sequence = transform(reference["sequence"]) if transform else reference["sequence"]
        append(identifier, sequence, "paired_invariant", reference.get("seed", EXPANSION_SEED),
               dict(reference["options"], **changes),
               invariant=dict(kind=kind, reference_case=reference_id),
               generation="Deterministic transformation of the named reference case")

    def mixed_dna(sequence):
        dna = sequence.upper().replace("U", "T")
        return "".join(base.lower() if index % 2 else base for index, base in enumerate(dna))

    paired("expanded_normalize_known_lower_rna", "control_known_pk_pk", "sequence_normalization",
           transform=str.lower)
    paired("expanded_normalize_known_mixed_dna", "control_known_pk_pk", "sequence_normalization",
           transform=mixed_dna)
    paired("expanded_normalize_seeded_lower_dna", "expanded_gc_n15_balanced", "sequence_normalization",
           transform=lambda value: value.lower().replace("u", "t"))
    paired("expanded_normalize_long_upper_dna", "expanded_gc_n90_low", "sequence_normalization",
           transform=lambda value: value.replace("U", "T"))
    for identifier, reference, alpha in (
        ("known05", "control_known_pk_pk", 0.5), ("known1", "control_known_pk_pk", 1.0),
        ("repeat60", "expanded_pattern_n60_gc", 0.5), ("seed90", "expanded_gc_n90_low", 1.0),
    ):
        paired("expanded_alpha_no_matrix_" + identifier, reference, "alpha_without_matrix", alpha=alpha)
    for identifier, reference, beta in (
        ("known03", "control_known_pk_nested", 0.3), ("known1", "control_known_pk_nested", 1.0),
        ("repeat60", "expanded_pattern_n60_gc", 1.0), ("seed90", "expanded_gc_n90_low", 0.3),
    ):
        paired("expanded_beta_no_pk_" + identifier, reference, "beta_without_pseudoknot", beta=beta)
    return added


def sparse_tsv(matrix):
    lines = [f"# sequence_length={len(matrix)}",
             "prepared_position_1\tprepared_position_2\tlog1p_gaussian_hyb_bonus"]
    for i in range(len(matrix)):
        for j in range(i + 1, len(matrix)):
            if matrix[i, j] != 0:
                # Preserve the exact float32 NPY values; do not use the
                # separately rounded repository text matrix or re-transform.
                lines.append(f"{i + 1}\t{j + 1}\t{float(matrix[i, j]):.17g}")
    return "\n".join(lines) + "\n"


def prepare_inputs():
    import numpy as np

    dbn = (ROOT / REAL_DBN).read_bytes()
    npy = (ROOT / REAL_NPY).read_bytes()
    sequences = [line.strip() for line in dbn.decode().splitlines()
                 if re.fullmatch("[ACGU]+", line.strip())]
    if len(sequences) != 1 or len(sequences[0]) != 207:
        raise ValueError("Expected the repository's 207-nt bpRNA_RFAM_5220 sequence")
    matrix = np.load(io.BytesIO(npy), allow_pickle=False)
    if (matrix.shape != (207, 207) or not np.isfinite(matrix).all()
            or not np.array_equal(matrix, matrix.T) or not np.any(matrix)):
        raise ValueError("Real PARIS matrix must be finite, symmetric, nonzero and 207x207")
    synthetic = np.zeros((len(KNOWN_PK), len(KNOWN_PK)), dtype=np.float32)
    for left, right in HELIX:
        if KNOWN_PK[left - 1] + KNOWN_PK[right - 1] not in ("AU", "UA", "CG", "GC", "GU", "UG"):
            raise ValueError("Synthetic evidence must support an actual canonical pair")
        synthetic[left - 1, right - 1] = synthetic[right - 1, left - 1] = 20.0
    buffer = io.BytesIO()
    np.save(buffer, synthetic, allow_pickle=False)
    files = {
        "inputs/bpRNA_RFAM_5220.dbn": dbn,
        "inputs/real_paris.npy": npy,
        "inputs/synthetic_helix.npy": buffer.getvalue(),
        "inputs/real_paris.tsv": sparse_tsv(matrix).encode(),
        "inputs/synthetic_helix.tsv": sparse_tsv(synthetic).encode(),
        "inputs/known_pk.fasta": (">known_single_chain_pk\n" + KNOWN_PK + "\n").encode(),
        "inputs/bpRNA_RFAM_5220.fasta": (">bpRNA_RFAM_5220\n" + sequences[0] + "\n").encode(),
    }
    description = dict(
        real_sequence_source=REAL_DBN, real_sequence_sha256=sha_bytes(dbn),
        real_matrix_source=REAL_NPY, real_matrix_sha256=sha_bytes(npy),
        real_matrix_shape=list(matrix.shape), real_matrix_dtype=str(matrix.dtype),
        real_matrix_nonzero=int(np.count_nonzero(matrix)), real_matrix_max=float(matrix.max()),
        cli_matrix_conversion="Every nonzero upper-triangle NPY value, 1-based coordinates, 17 significant digits; no rescaling",
        synthetic_helix_pairs_1based=[list(pair) for pair in HELIX], synthetic_pair_bonus=20.0,
        random_seed=522028, expansion_seed=EXPANSION_SEED,
        expansion_lengths=list(EXPANSION_LENGTHS),
        expansion_description="Synthetic GC-controlled draws, fixed motifs and paired normalization/ignored-parameter invariants; each new case records its seed and generation method",
    )
    return files, make_cases(sequences[0]), description


def validate_predictions(predictions, length):
    if not isinstance(predictions, list) or not predictions:
        raise ValueError("A valid API case must return at least one candidate")
    for item in predictions:
        if not isinstance(item, dict) or not REQUIRED_FIELDS <= item.keys():
            raise ValueError("Incomplete public API candidate")
        for key in NUMERIC_FIELDS:
            value = item[key]
            if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
                raise ValueError(f"{key} is not a finite numeric score: {value!r}")
        for key in ("structure", "phase1_struct", "phase2_struct"):
            value = item[key]
            if key == "phase2_struct" and value is None:
                continue
            if not isinstance(value, str) or len(value) != length or "&" in value:
                raise ValueError("Invalid single-chain structure field: " + key)
        if item["type"] not in ("phase1", "pseudoknot"):
            raise ValueError("Unexpected candidate type")
    if any(a["effective_energy"] > b["effective_energy"]
           for a, b in zip(predictions, predictions[1:])):
        raise ValueError("Public API candidates are not ordered by effective energy")


def worker(repository, config_path):
    repository = repository.resolve()
    config = strict_loads(config_path.read_text())
    sys.path.insert(0, str(repository / "third_party/cplfold"))
    import numpy as np
    import numba
    import CPLfold
    import CPLfold_parser
    import Utils.hotknots_energy as energy_module

    if numba.config.DISABLE_JIT:
        raise RuntimeError("Actual Numba JIT is required")
    imported = {}
    for module in (CPLfold, CPLfold_parser, energy_module):
        filename = Path(module.__file__).resolve()
        filename.relative_to(repository / "third_party/cplfold")
        imported[module.__name__] = str(filename)
    inputs = config_path.parent / "inputs"
    matrices = {name: np.load(inputs / (name + ".npy"), allow_pickle=False)
                for name in ("real_paris", "synthetic_helix")}

    def numpy_scalar(value):
        if isinstance(value, np.generic):
            return value.item()
        raise TypeError("Unsupported public API value: " + type(value).__name__)

    def compiled_signatures():
        return {name: [str(signature) for signature in value.nopython_signatures]
                for name, value in vars(CPLfold_parser).items()
                if hasattr(value, "nopython_signatures") and value.nopython_signatures}

    rows = []
    signatures_before_expansion = None
    for case in config["cases"]:
        if case.get("synthetic") and signatures_before_expansion is None:
            signatures_before_expansion = compiled_signatures()
        captured = io.StringIO()
        try:
            with contextlib.redirect_stdout(captured):
                predictions = CPLfold.two_phase_pseudoknot_fold(
                    case["sequence"], verbose=False,
                    bonus_matrix=matrices[case["bonus"]].copy() if case["bonus"] else None,
                    **case["options"],
                )
            # Convert NumPy scalar returns without rounding or dropping any
            # fields. The strict round trip rejects nonfinite nested values.
            predictions = strict_loads(json.dumps(
                predictions, allow_nan=False, default=numpy_scalar,
            ))
            validate_predictions(predictions, case["length"])
            rows.append(dict(id=case["id"], predictions=predictions, stdout=captured.getvalue()))
        except Exception as error:
            rows.append(dict(id=case["id"], error=dict(type=type(error).__name__, message=str(error),
                             traceback=traceback.format_exc()), stdout=captured.getvalue()))
    signatures = compiled_signatures()
    print(json_text(dict(results=rows, imported_modules=imported,
        numba=numba.__version__, numpy=np.__version__, numba_disabled=bool(numba.config.DISABLE_JIT),
        nopython_signatures=signatures,
        nopython_signatures_before_expansion=signatures_before_expansion,
        nopython_signatures_unchanged_by_expansion=signatures_before_expansion == signatures)), end="")


def differences(before, after, path="result"):
    """Compare all fields and list order; tolerate only tiny numeric roundoff."""
    if isinstance(before, bool) or isinstance(after, bool):
        return [] if type(before) is type(after) and before == after else [path + ": boolean changed"]
    if isinstance(before, (int, float)) and isinstance(after, (int, float)):
        return [] if math.isclose(before, after, rel_tol=0.0, abs_tol=ATOL) else [f"{path}: {before!r} != {after!r}"]
    if type(before) is not type(after):
        return [path + ": type changed"]
    if isinstance(before, dict):
        if before.keys() != after.keys():
            return [path + ": dictionary fields changed"]
        return [message for key in before for message in differences(before[key], after[key], path + "." + key)]
    if isinstance(before, list):
        if len(before) != len(after):
            return [f"{path}: candidate/list count {len(before)} != {len(after)}"]
        return [message for index, (left, right) in enumerate(zip(before, after))
                for message in differences(left, right, f"{path}[{index}]")]
    return [] if before == after else [f"{path}: {before!r} != {after!r}"]


def paired_invariants(payload, cases):
    rows = {row["id"]: row.get("predictions", []) for row in payload["results"]}
    definitions = {case["id"]: case for case in cases}
    counts = {kind: dict(checked=0, passed=0) for kind in
              ("sequence_normalization", "alpha_without_matrix", "beta_without_pseudoknot")}
    results, failures = [], []
    for case in cases:
        invariant = case.get("invariant")
        if not invariant:
            continue
        kind, reference_id = invariant["kind"], invariant["reference_case"]
        reference = definitions.get(reference_id)
        valid_definition = reference is not None and kind in counts
        if valid_definition:
            left_options, right_options = reference["options"].copy(), case["options"].copy()
            if kind == "sequence_normalization":
                valid_definition = (reference["sequence"].upper().replace("T", "U")
                                    == case["sequence"].upper().replace("T", "U")
                                    and reference["sequence"] != case["sequence"]
                                    and reference["bonus"] == case["bonus"])
            elif kind == "alpha_without_matrix":
                valid_definition = (left_options.pop("alpha") == 0.0 and right_options.pop("alpha") > 0.0
                                    and reference["bonus"] is None and case["bonus"] is None
                                    and reference["sequence"] == case["sequence"])
            else:
                valid_definition = (left_options.pop("beta") == 0.0 and right_options.pop("beta") > 0.0
                                    and not reference["options"]["allow_pseudoknot"]
                                    and not case["options"]["allow_pseudoknot"]
                                    and reference["bonus"] == case["bonus"]
                                    and reference["sequence"] == case["sequence"])
            valid_definition = valid_definition and left_options == right_options
        left, right = rows.get(reference_id, []), rows.get(case["id"], [])
        equal = bool(valid_definition and left and right and left == right)
        if kind in counts:
            counts[kind]["checked"] += 1
            counts[kind]["passed"] += int(equal)
        messages = differences(left, right) if left and right else ["Missing or empty paired predictions"]
        if not valid_definition:
            messages.append("Invalid or vacuous invariant definition")
        if not equal and not messages:
            messages.append("Paired predictions differ despite being within the numerical tolerance")
        results.append(dict(id=case["id"], reference_case=reference_id, kind=kind,
                            equal=equal, differences=messages))
        if not equal:
            failures.append(f"Paired invariant {case['id']} ({kind}) failed")
    for kind, count in counts.items():
        if not count["checked"]:
            failures.append("Empty paired invariant coverage: " + kind)
    return dict(counts=counts, results=results, failures=failures)


def coverage(payload, cases):
    rows = {row["id"]: row.get("predictions", []) for row in payload["results"]}
    failures = []
    for name in ("parse_loop_vienna", "parse_loop_contrafold"):
        if not payload["nopython_signatures"].get(name):
            failures.append("Missing real nopython signature: " + name)
    if payload["numba_disabled"]:
        failures.append("JIT was disabled")
    unchanged_signatures = (payload.get("nopython_signatures_before_expansion") == payload["nopython_signatures"]
                            and payload.get("nopython_signatures_unchanged_by_expansion") is True)
    if not unchanged_signatures:
        failures.append("Expanded inputs changed the parser nopython signatures")
    pseudoknot_cases = sum(any(item["type"] == "pseudoknot" for item in values) for values in rows.values())
    multi_cases = sum(len(values) > 1 for values in rows.values())
    multi_phase1 = sum(sum(item["type"] == "phase1" for item in values) > 1 for values in rows.values())
    if not pseudoknot_cases or not multi_cases or not multi_phase1:
        failures.append("Empty pseudoknot or multiple-candidate coverage")
    alpha_effects = {}
    for group, first, guided in (
        ("synthetic_helix", "alpha_helix_0", "alpha_helix_0.5"),
        ("real_paris", "real_paris_nested_a0", "real_paris_nested_a0.5"),
    ):
        left, right = rows.get(first, []), rows.get(guided, [])
        projection = lambda values: [(item["structure"], item["score"]) for item in values]
        alpha_effects[group] = bool(left and right and projection(left) != projection(right))
        if not alpha_effects[group]:
            failures.append("Alpha did not change candidates/search scores for " + group)
    beta_changes = 0
    for model in MODELS:
        base = {item["structure"]: item for item in rows.get(f"model_{model}_beta_0", [])}
        for beta in (0.3, 1.0):
            guided = rows.get(f"model_{model}_beta_{beta:g}", [])
            if not base or set(base) != {item["structure"] for item in guided}:
                failures.append("Beta changed or emptied the candidate set: " + model)
            for item in guided:
                original = base.get(item["structure"])
                if not original or not math.isclose(item["energy"], original["energy"], abs_tol=ATOL, rel_tol=0):
                    failures.append("Beta changed the candidate set or physical energy: " + model)
                    continue
                expected = item["energy"] + (beta * item["phase1_energy"] if item["type"] == "pseudoknot" else 0)
                if not math.isclose(item["effective_energy"], expected, abs_tol=ATOL, rel_tol=0):
                    failures.append("Beta ranking formula failed: " + model)
                beta_changes += int(item["type"] == "pseudoknot" and item["effective_energy"] != original["effective_energy"])
    if not beta_changes:
        failures.append("Beta did not change a real pseudoknot effective energy")
    invariants = paired_invariants(payload, cases)
    failures.extend(invariants["failures"])
    return dict(pseudoknot_cases=pseudoknot_cases, multi_candidate_cases=multi_cases,
                multiple_phase1_cases=multi_phase1, alpha_effects=alpha_effects,
                beta_changed_pseudoknot_scores=beta_changes, paired_invariants=invariants,
                jit_signatures_unchanged_by_expansion=unchanged_signatures,
                failures=failures)


def cli_cases(inputs):
    common = ["--beam", "20", "--max-phase1", "5", "--max-phase2", "2", "--delta", "5", "--beta", "0.3"]
    short = ["--beam", "1", "--max-phase1", "1", "--delta", "0", "--no-pseudoknot"]
    return [
        ("sequence_known_pk", ["--sequence", KNOWN_PK, "--alpha", "0"] + common),
        ("fasta_helix_alpha0", ["--sequence-file", str(inputs / "known_pk.fasta"),
          "--bonus-matrix-file", str(inputs / "synthetic_helix.tsv"), "--alpha", "0"] + common),
        ("fasta_helix_alpha05", ["--sequence-file", str(inputs / "known_pk.fasta"),
          "--bonus-matrix-file", str(inputs / "synthetic_helix.tsv"), "--alpha", "0.5"] + common),
        ("fasta_real_paris", ["--sequence-file", str(inputs / "bpRNA_RFAM_5220.fasta"),
          "--bonus-matrix-file", str(inputs / "real_paris.tsv"), "--alpha", "0.5",
          "--beam", "100", "--max-phase1", "1", "--delta", "0", "--no-pseudoknot"]),
        ("sequence_hairpin_nested", ["--sequence", "GGGAAACCC", "--alpha", "0"] + short),
    ]


def prediction_report(stdout):
    starts = list(re.finditer(r"^Sequence: ", stdout, re.MULTILINE))
    if len(starts) != 1:
        raise ValueError("Expected exactly one complete CLI prediction report")
    report = stdout[starts[0].start():].strip()
    if not re.search(r"^(?:P1|S|PK)-\d+: [().\[\]{}<>]+$", report, re.MULTILINE):
        raise ValueError("CLI did not print any candidate")
    energies = re.findall(r"(?:Effective )?Energy: (\S+) kcal/mol", report)
    if not energies or any(not math.isfinite(float(value)) for value in energies):
        raise ValueError("CLI printed a missing or non-finite energy")
    return report


def subprocess_result(command, environment, cwd, timeout):
    try:
        result = subprocess.run(command, cwd=cwd, env=environment, capture_output=True,
                                text=True, timeout=timeout, check=False)
    except subprocess.TimeoutExpired as error:
        def decoded(value):
            return value.decode(errors="replace") if isinstance(value, bytes) else value or ""
        return dict(returncode=None, timed_out=True, stdout=decoded(error.stdout),
                    stderr=decoded(error.stderr) + f"\nSubprocess exceeded {timeout:g} seconds.\n")
    return dict(returncode=result.returncode, stdout=result.stdout, stderr=result.stderr)


def render_report(data):
    rows = data["api_comparisons"]
    passed = sum(row["equal"] for row in rows)
    expanded = [case for case in data["cases"] if case.get("synthetic")]
    lines = ["# CPLfold single-chain regression comparison", "", f"Run: {data['run_utc']}", "",
        f"Baseline: `{data['baseline_commit']}`; current implementation: the recorded worktree source hashes.", "",
        f"API: {passed}/{len(rows)} complete ordered candidate lists match. "
        f"CLI: {sum(row['equal'] for row in data['cli_comparisons'])}/{len(data['cli_comparisons'])} valid single-chain commands match.", "",
        f"Overall: **{'PASS' if data['passed'] else 'FAIL'}**.", "",
        "Comparison includes every returned dictionary field and candidate order: structures, physical/effective energies, "
        "search scores, phase-1/phase-2 provenance and candidate type. All required scores must be finite; strings and fields "
        f"match exactly, numerical absolute tolerance is {ATOL:g} (relative tolerance zero). "
        f"Exactly equal API payloads: {sum(row['exact_equal'] for row in rows)}/{len(rows)}.", "",
        "Both versions run in separate isolated interpreters with real Numba. The baseline is exported using git archive "
        "into a temporary directory; the worktree is never checked out or reset. Separate per-version caches are reused "
        "by that version's API worker and CLI commands. No speed or biological-accuracy claim is made.", "",
        "The 207-nt input and its actual repository PARIS NPY matrix are snapshotted under inputs/. Both versions receive "
        "identical matrix bytes. CLI TSV values are derived directly from the same NPY with 17 significant digits, without "
        "rescaling; the separately rounded repository TXT matrix is not substituted. Synthetic guidance supports four "
        "canonical pairs in the known 28-nt example, with an explicit alpha-effect check.", "",
        f"The expanded panel adds {len(expanded)} explicitly synthetic cases to the unchanged original "
        f"{len(data['cases']) - len(expanded)} cases. It covers lengths "
        + ", ".join(map(str, sorted({case['length'] for case in expanded})))
        + " nt, low/balanced/high GC compositions, homopolymers, repeated motifs, lowercase RNA and mixed-case T inputs. "
        f"The generation seed is {EXPANSION_SEED}; each added case records its own seed, method and actual GC fraction. "
        "Fixed motifs are marked separately from random draws. These are software regression fixtures, not biological samples.", "",
        "## Coverage guards", ""]
    for label, evidence in data["coverage"].items():
        lines.append(f"- {label}: {evidence['pseudoknot_cases']} PK cases, {evidence['multi_candidate_cases']} multi-candidate cases, "
                     f"{evidence['multiple_phase1_cases']} cases with multiple phase-1 candidates; "
                     f"alpha effects {evidence['alpha_effects']}; {evidence['beta_changed_pseudoknot_scores']} changed PK ranking scores.")
        for kind, count in evidence["paired_invariants"]["counts"].items():
            lines.append(f"  - {kind}: {count['passed']}/{count['checked']} paired full-result equalities.")
    lines += ["", "Both parse_loop_vienna and parse_loop_contrafold must have nopython signatures. "
              "The expanded inputs must leave the parser's compiled signatures unchanged, and the baseline/current "
              f"signature sets must match (this run: {data['jit_signatures_equal']}). "
              "Full signatures before/after expansion and imported module locations are retained in each worker JSON. "
              "Paired checks require exact equality of complete ordered results after sequence normalization, "
              "changing alpha without a matrix, or changing beta with pseudoknot generation disabled.", "",
              "## API cases", "", "| Case | nt | Model | Beam | alpha | beta | PK | Candidates old/new | Equal |",
              "|---|---:|---|---:|---:|---:|---|---:|---|"]
    for case, row in zip(data["cases"], rows):
        options = case["options"]
        lines.append(f"| {case['id']} | {case['length']} | {options['energy_model']} | {options['beam_size']} | "
                     f"{options['alpha']:g} | {options['beta']:g} | {options['allow_pseudoknot']} | "
                     f"{row['baseline_candidates']}/{row['current_candidates']} | {row['equal']} |")
    lines += ["", "## CLI and input-validation scope", "",
        "CLI checks compare exit status and complete prediction reports for --sequence, single-record FASTA and "
        "synthetic/real read-matrix files. Both versions must succeed; the guided CLI must also change its prediction "
        "when alpha changes from zero to 0.5. Verbose progress messages are retained in logs but are outside prediction equality.", "",
        "This verdict concerns valid single-chain prediction behavior. New cofold arguments, stricter sequence validation "
        "and rejection of malformed FASTA preambles are intentional input-validation changes; this report does not claim "
        "that malformed inputs or help/error text are unchanged.", "", "## Failures", ""]
    lines += ["- " + message for message in data["failures"]] or ["None."]
    lines += ["", "## Reproduce", "", "```sh", data["reproduce_command"], "```", ""]
    return "\n".join(lines)


def _safe_artifact_path(output, name):
    if not isinstance(name, str):
        raise ValueError("Managed artifact paths must be strings")
    relative = PurePosixPath(name)
    if (not name or name in (".", "exports.json") or relative.is_absolute()
            or ".." in relative.parts or relative.as_posix() != name):
        raise ValueError("Unsafe managed artifact path: " + name)
    destination = output
    for part in relative.parts:
        destination = destination / part
        if destination.is_symlink():
            raise ValueError("Managed artifact paths must not contain symbolic links")
    if not destination.resolve().is_relative_to(output):
        raise ValueError("Managed artifact path escapes the output directory")
    return destination


def write_artifacts(output, files):
    """Replace files atomically, remove obsolete owned files, publish hashes last.

    The previous manifest defines ownership; unrelated files are preserved.
    This is not a directory-wide transaction. An interrupted replacement is
    detectable because the last published manifest no longer matches its files.
    """
    output = Path(output).resolve()
    manifest_path = output / "exports.json"
    if manifest_path.is_symlink():
        raise ValueError("The artifact manifest must not be a symbolic link")
    old = {}
    if manifest_path.exists():
        previous = strict_loads(manifest_path.read_text())
        if (not isinstance(previous, dict) or previous.get("schema_version") != 1
                or not isinstance(previous.get("files"), dict)):
            raise ValueError("Invalid artifact ownership manifest")
        old = previous["files"]
        if any(not isinstance(digest, str) or re.fullmatch(r"[0-9a-f]{64}", digest) is None
               for digest in old.values()):
            raise ValueError("Invalid artifact ownership hash")
    # Validate every path before changing existing published files.
    old_targets = {name: _safe_artifact_path(output, name) for name in old}
    targets = {name: _safe_artifact_path(output, name) for name in files}
    encoded = {}
    for name, contents in files.items():
        if targets[name].is_dir():
            raise ValueError("Managed artifact target is a directory: " + name)
        if not isinstance(contents, (str, bytes)):
            raise TypeError("Artifact contents must be str or bytes")
        encoded[name] = contents if isinstance(contents, bytes) else contents.encode("utf-8")
    manifest = {name: sha_bytes(encoded[name]) for name in sorted(encoded)}
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".artifacts-", dir=output) as directory:
        stage = Path(directory)
        for name, contents in encoded.items():
            destination = stage / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(contents)
        (stage / "exports.json").write_text(json_text(dict(schema_version=1, files=manifest)))
        for name, destination in targets.items():
            destination.parent.mkdir(parents=True, exist_ok=True)
            os.replace(stage / name, destination)
        for name in old.keys() - files.keys():
            if old_targets[name].is_file():
                old_targets[name].unlink()
        os.replace(stage / "exports.json", manifest_path)


def run_status_files(status, *, stage=None, process=None):
    """Invalidate an earlier PASS before work starts or when a worker fails."""
    if status not in ("running", "failed"):
        raise ValueError("Expected an unfinished or failed run status")
    data = dict(schema_version=1, run_utc=datetime.now(timezone.utc).isoformat(),
                passed=False, status=status)
    if stage is not None:
        data["stage"] = stage
    if process is not None:
        data["process"] = process
    verdict = "RUNNING" if status == "running" else "FAIL"
    report = ("# CPLfold single-chain regression comparison\n\n"
              f"Run: {data['run_utc']}\n\nOverall: **{verdict}**.\n\n"
              "This run has not produced a successful comparison verdict.\n")
    if stage is not None:
        report += f"\nStage: {stage}. See failure.json and logs/ for details.\n"
    return {"results.json": json_text(data), "report.md": report}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", default=DEFAULT_BASELINE)
    parser.add_argument("--output", type=Path, default=ROOT / "artifacts/cplfold-single-expanded")
    parser.add_argument("--python", default=os.environ.get("HYB2_PYTHON") or str(ROOT / ".venv-cplfold/bin/python"))
    parser.add_argument("--timeout", type=float, default=1800.0, help="Per subprocess timeout in seconds")
    parser.add_argument("--list-cases", action="store_true", help="Print case definitions without importing a folding engine")
    args = parser.parse_args()
    if not math.isfinite(args.timeout) or args.timeout <= 0:
        parser.error("--timeout must be finite and positive")
    if not args.list_cases:
        executed_source = SCRIPT.read_bytes()
        initial_files = run_status_files("running")
        initial_files["executed-runner.py"] = executed_source
        write_artifacts(args.output, initial_files)
    files, cases, input_description = prepare_inputs()
    if args.list_cases:
        print(json_text(dict(cases=cases, inputs=input_description)), end="")
        return 0
    files["executed-runner.py"] = executed_source
    runner = str(Path(args.python).expanduser().absolute())
    current_before = source_manifest(ROOT)
    script_digest = sha_bytes(executed_source)
    failures, worker_payloads, cli_payloads = [], {}, {}
    with tempfile.TemporaryDirectory(prefix="cplfold-single-regression-") as directory:
        temporary = Path(directory)
        baseline_root = temporary / "baseline"
        commit, archive_digest = export_baseline(args.baseline, baseline_root)
        baseline_manifest = source_manifest(baseline_root)
        for name in (REAL_DBN, REAL_NPY):
            if baseline_manifest[name] != current_before[name]:
                raise ValueError("Baseline and worktree fixture bytes differ: " + name)
        for name, contents in files.items():
            path = temporary / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(contents)
        config_path = temporary / "cases.json"
        config_path.write_text(json_text(dict(cases=cases)))
        for label, repository in (("baseline", baseline_root), ("current", ROOT)):
            environment = os.environ.copy()
            for key in ("NUMBA_DISABLE_JIT", "PYTHONPATH", "PYTHONHOME"):
                environment.pop(key, None)
            environment["NUMBA_CACHE_DIR"] = str(temporary / (label + "-numba-cache"))
            environment["PYTHONDONTWRITEBYTECODE"] = "1"
            print(f"Running {label}: {len(cases)} API cases with real Numba; initial compilation may take several minutes.", flush=True)
            run = subprocess_result([runner, "-I", "-B", str(SCRIPT), "--worker", str(repository), str(config_path)],
                                    environment, temporary, args.timeout)
            files[f"logs/{label}-worker.stdout"] = run["stdout"]
            files[f"logs/{label}-worker.stderr"] = run["stderr"]
            if run["returncode"] != 0:
                files["failure.json"] = json_text(dict(stage=label + " worker", process=run))
                files.update(run_status_files("failed", stage=label + " worker", process=run))
                write_artifacts(args.output, files)
                raise SystemExit(f"{label} worker failed; see {args.output / ('logs/' + label + '-worker.stderr')}")
            payload = strict_loads(run["stdout"])
            if [row["id"] for row in payload["results"]] != [case["id"] for case in cases]:
                raise ValueError("Worker changed case order or omitted a case")
            worker_payloads[label] = payload
            files[label + "-worker.json"] = json_text(payload)
            print(f"Completed {label} API cases; checking five CLI commands.", flush=True)
            cli_payloads[label] = []
            for identifier, arguments in cli_cases(temporary / "inputs"):
                run = subprocess_result([runner, "-I", "-B", str(repository / "bin/cplfold"), *arguments],
                                        environment, temporary, args.timeout)
                normalized = [value.replace(str(temporary), "<fixtures>") for value in arguments]
                record = dict(id=identifier, arguments=normalized, **run)
                try:
                    record["prediction_report"] = prediction_report(run["stdout"])
                except ValueError as error:
                    record["error"] = str(error)
                cli_payloads[label].append(record)
                files[f"logs/{label}-cli-{identifier}.stdout"] = run["stdout"]
                files[f"logs/{label}-cli-{identifier}.stderr"] = run["stderr"]
        if source_manifest(ROOT) != current_before or sha(SCRIPT) != script_digest:
            failures.append("Worktree source or comparison script changed during the run")
        if source_manifest(baseline_root) != baseline_manifest:
            failures.append("Exported baseline source changed during the run")
    api_rows = []
    for before, after in zip(worker_payloads["baseline"]["results"], worker_payloads["current"]["results"]):
        left, right = before.get("predictions", []), after.get("predictions", [])
        errors = differences(left, right)
        for label, row in (("baseline", before), ("current", after)):
            if "error" in row:
                errors.append(f"{label} raised {row['error']['type']}: {row['error']['message']}")
        api_rows.append(dict(id=before["id"], equal=not errors, exact_equal=bool(left and right and left == right),
                             baseline_candidates=len(left), current_candidates=len(right), differences=errors))
        failures.extend(before["id"] + ": " + error for error in errors)
    jit_signatures_equal = (worker_payloads["baseline"]["nopython_signatures"]
                            == worker_payloads["current"]["nopython_signatures"])
    if not jit_signatures_equal:
        failures.append("Baseline and current parser nopython signatures differ")
    coverage_rows = {label: coverage(payload, cases) for label, payload in worker_payloads.items()}
    for label, evidence in coverage_rows.items():
        failures.extend(label + ": " + message for message in evidence["failures"])
    cli_rows = []
    for before, after in zip(cli_payloads["baseline"], cli_payloads["current"]):
        equal = (before["returncode"] == after["returncode"] == 0 and "error" not in before and "error" not in after
                 and before["prediction_report"] == after["prediction_report"])
        cli_rows.append(dict(id=before["id"], equal=equal,
                             baseline_returncode=before["returncode"], current_returncode=after["returncode"]))
        if not equal:
            failures.append("CLI prediction or exit status differs: " + before["id"])
    for label, rows in cli_payloads.items():
        reports = {row["id"]: row.get("prediction_report") for row in rows}
        if not reports["fasta_helix_alpha0"] or reports["fasta_helix_alpha0"] == reports["fasta_helix_alpha05"]:
            failures.append(label + ": CLI synthetic evidence did not change predictions at alpha=0.5")
        if reports["sequence_known_pk"] != reports["fasta_helix_alpha0"]:
            failures.append(label + ": zero-alpha reads/FASTA changed the sequence-only prediction")
    data = dict(schema_version=1, run_utc=datetime.now(timezone.utc).isoformat(),
        baseline_commit=commit, baseline_archive_sha256=archive_digest,
        current_git_head=subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip(),
        baseline_source_sha256=baseline_manifest, current_source_sha256=current_before,
        comparison_script_sha256=script_digest, inputs=input_description, cases=cases,
        numeric_absolute_tolerance=ATOL, python_executable=runner, driver_python=sys.version,
        platform=platform.platform(), timeout_seconds=args.timeout,
        api_comparisons=api_rows, cli_comparisons=cli_rows, cli_results=cli_payloads,
        coverage=coverage_rows, jit_signatures_equal=jit_signatures_equal,
        failures=failures, passed=not failures,
        status="passed" if not failures else "failed",
        reproduce_command=shlex.join([runner, str(SCRIPT), "--baseline", commit, "--output", str(args.output.resolve()),
                                      "--python", runner, "--timeout", str(args.timeout)]))
    files["fixtures.json"] = json_text(dict(cases=cases, inputs=input_description))
    files["results.json"] = json_text(data)
    files["report.md"] = render_report(data)
    write_artifacts(args.output, files)
    print(f"{'PASS' if data['passed'] else 'FAIL'}: {sum(row['equal'] for row in api_rows)}/{len(api_rows)} API cases; "
          f"{sum(row['equal'] for row in cli_rows)}/{len(cli_rows)} CLI cases. Report: {args.output / 'report.md'}")
    return 0 if data["passed"] else 1


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--worker":
        worker(Path(sys.argv[2]), Path(sys.argv[3]))
    else:
        raise SystemExit(main())
