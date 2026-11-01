#!/usr/bin/env python3
"""Verify cofold with isolated real-Numba/Python runs and the public CLI.

Unit tests install a lightweight Numba shim. This script intentionally runs
fresh interpreters and requires compiled nopython signatures in the JIT run.
Run with HYB2_PYTHON, .venv-cplfold/bin/python, or a Python with NumPy/Numba.
"""

from __future__ import annotations

import json
import math
import os
from pathlib import Path
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[1]
VENDOR = ROOT / "third_party" / "cplfold"
REFERENCE_CASES = (
    ("GGGCCC", 3, "((()))", -2.5),
    ("GGGGCCCC", 4, "(((())))", -5.8),
    ("GGGAAACCC", 3, "(((...)))", -3.0),
    ("AAAAGGGAAACCC", 4, "....(((...)))", -1.2),
    ("CCCGGGAAACCCGGG", 3, "((((((...))))))", -5.7),
    ("GGGAAACCCGGGAAACCC", 9, "(((...((()))...)))", -7.1),
)


def _finite_number(value, label):
    try:
        if isinstance(value, bool):
            raise ValueError("boolean is not an energy")
        number = float(value)
    except (TypeError, ValueError, OverflowError) as error:
        raise AssertionError(f"{label} must be a finite number: {value!r}") from error
    if not math.isfinite(number):
        raise AssertionError(f"{label} must be a finite number: {value!r}")
    return number


def _assert_close(actual, expected, label, tolerance=1e-6):
    actual = _finite_number(actual, label)
    expected = _finite_number(expected, f"Expected {label}")
    if not math.isclose(actual, expected, rel_tol=0.0, abs_tol=tolerance):
        raise AssertionError(f"{label}: expected {expected!r}, got {actual!r}")


def _strict_json_dumps(value):
    return json.dumps(value, sort_keys=True, allow_nan=False)


def _strict_json_loads(text):
    def reject_constant(value):
        raise ValueError(f"Non-finite JSON constant: {value}")

    def finite_float(value):
        number = float(value)
        if not math.isfinite(number):
            raise ValueError(f"JSON number exceeds the finite float range: {value}")
        return number

    return json.loads(text, parse_constant=reject_constant, parse_float=finite_float)


def _checked_payload(parser_module, bonus_module, evaluator, two_phase_pseudoknot_fold, np):
    """Check runtime outputs; explicit dependencies keep fault tests isolated."""

    vectors = []
    for sequence, cutpoint, structure, energy in REFERENCE_CASES:
        parser = parser_module.BeamCKYCofoldParser(cutpoint=cutpoint, beam_size=100, use_constraints=True)
        parsed = parser.parse(sequence)
        if parsed[0] != structure:
            raise AssertionError(f"LinearCoFold reference mismatch: {sequence}, {parsed[:2]}")
        _assert_close(parsed[1], energy, f"LinearCoFold reference energy for {sequence}")
        physical = evaluator.compute_energy(sequence, structure, cutpoint)
        _assert_close(physical["energy"], energy, f"Physical cofold energy for {sequence}")
        vectors.append([sequence, cutpoint, parsed[0], round(parsed[1], 7), round(physical["energy"], 7)])

    parser = parser_module.BeamCKYCofoldParser(cutpoint=2, beam_size=20, use_constraints=True)
    matrix = np.zeros((4, 4), dtype=np.float64)
    matrix[0, 3] = matrix[3, 0] = 5.0
    matrix[1, 2] = matrix[2, 1] = 5.0
    parser.set_bonus_matrix(matrix, 4)
    parser.set_alpha(1.0)
    guided = parser.parse("GGCC")
    if guided[0] != "(())":
        raise AssertionError(f"Cross-nick evidence bonus mismatch: {guided[:2]}")
    _assert_close(guided[1], -9.2, "Cross-nick evidence search score")
    forced = parser.parse("GGCC", cons="....")
    if forced[0] != "....":
        raise AssertionError(f"Unpaired constraints failed: {forced[:2]}")
    _assert_close(forced[1], 0.0, "Unpaired constrained energy")

    results = two_phase_pseudoknot_fold("GGG&CCC", verbose=False, beam_size=20, max_phase1=3, allow_pseudoknot=False)
    if results[0]["structure"] != "(((&)))":
        raise AssertionError("Public cofold result disagrees with the parser")
    _assert_close(results[0]["energy"], -2.5, "Public cofold energy")

    crossing = two_phase_pseudoknot_fold("GGGG&CCCC", verbose=False, beam_size=30, max_phase1=12, max_phase2=3, energy_delta=12.0, beta=0.5)
    pseudoknot = next(item for item in crossing if item["structure"] == "(([[&))]]")
    _assert_close(pseudoknot["energy"], 1.766, "Generated pseudoknot energy")
    _assert_close(pseudoknot["energy_breakdown"]["duplex_init"], 4.1, "Pseudoknot association energy")
    expected_ranking = pseudoknot["energy"] + 0.5 * _finite_number(pseudoknot["phase1_energy"], "Phase 1 energy")
    _assert_close(pseudoknot["effective_energy"], expected_ranking, "Beta-adjusted ranking energy")

    # Invariants exercise model correctness in both execution modes, beyond
    # checking the two implementations against the same fixed score.
    original_pk = evaluator.compute_energy("AAUUAAA", "([)]...", 2)
    swapped_pk = evaluator.compute_energy("UUAAAAA", "([...)]", 5)
    _assert_close(original_pk["energy"], swapped_pk["energy"], "Strand-swap pseudoknot energy", 1e-8)
    sequence_a = "GGCGCGGCACCGUCCGCGGAACAAACGG"
    structure_a = "..(((((..[[[[)))))......]]]]"
    nested_a = "..(((((......))))).........."
    reference_gap = (
        _finite_number(evaluator.compute_energy(sequence_a + "A", structure_a + ".", len(sequence_a))["energy"], "Reference pseudoknot energy")
        - _finite_number(evaluator.compute_energy(sequence_a + "A", nested_a + ".", len(sequence_a))["energy"], "Reference nested energy")
    )
    for repeats in (1, 2):
        sequence_b, structure_b = "GGGAAACCC" * repeats, "(((...)))" * repeats
        gap = (
            _finite_number(evaluator.compute_energy(sequence_a + sequence_b, structure_a + structure_b, len(sequence_a))["energy"], "Combined pseudoknot energy")
            - _finite_number(evaluator.compute_energy(sequence_a + sequence_b, nested_a + structure_b, len(sequence_a))["energy"], "Combined nested energy")
        )
        _assert_close(gap, reference_gap, "Independent-monomer ranking gap", 1e-8)
    no_evidence = two_phase_pseudoknot_fold(
        "GGG&CCC", alpha=1e308, allow_pseudoknot=False, verbose=False,
        beam_size=20, max_phase1=1,
    )
    if no_evidence[0]["structure"] != "(((&)))":
        raise AssertionError("Alpha changed a prediction without evidence")
    for key, expected in (("energy", -2.5), ("score", -2.5), ("effective_energy", -2.5),
                          ("read_support", 0.0), ("read_bonus", 0.0)):
        _assert_close(no_evidence[0][key], expected, f"Evidence-free {key}", 1e-8)
    try:
        two_phase_pseudoknot_fold(
            "GGGG&CCCC", beta=1e308, energy_delta=10, verbose=False,
        )
    except ValueError:
        pass
    else:
        raise AssertionError("Overflowing beta ranking was not rejected")

    reads = bonus_module.build_cofold_bonus_matrix(8, 4, [(2, 3, 6, 7)] * 2)
    if not np.isfinite(reads).all():
        raise AssertionError("Read-bonus matrix must contain only finite numbers")
    if np.any(reads[:4, :4]) or np.any(reads[4:, 4:]):
        raise AssertionError("Read evidence leaked across a strand boundary or was weighted after log1p")
    _assert_close(float(reads.max()), float(np.log(3.0)), "Weighted read-bonus maximum")

    # Keep timing and dispatcher objects out of the deterministic payload.
    payload = {
        "vectors": vectors,
        "guided": [guided[0], round(guided[1], 7)],
        "forced": [forced[0], round(forced[1], 7)],
        "results": results,
        "pseudoknot": pseudoknot,
        "strand_swap_energy": round(original_pk["energy"], 8),
        "independent_monomer_gap": round(reference_gap, 8),
        "no_evidence": no_evidence[0],
        "read_bonus": np.round(reads.astype(np.float64), 7).tolist(),
    }
    # Validate every nested numeric field, including fields not used by a
    # particular reference comparison, before Python/JIT payload comparison.
    _strict_json_dumps(payload)
    return payload


def worker() -> None:
    sys.path.insert(0, str(VENDOR))
    import numba
    import numpy as np
    import CPLcofold_parser as parser_module
    import Utils.cofold_bonus as bonus_module
    from CPLfold import two_phase_pseudoknot_fold
    from Utils.cofold_energy import CofoldEnergy

    payload = _checked_payload(parser_module, bonus_module, CofoldEnergy(), two_phase_pseudoknot_fold, np)
    compiled = {
        name: len(value.nopython_signatures)
        for name, value in vars(parser_module).items()
        if hasattr(value, "nopython_signatures") and value.nopython_signatures
    }
    disabled = bool(numba.config.DISABLE_JIT)
    if not disabled and not compiled:
        raise AssertionError("No cofold Numba kernel compiled in nopython mode")
    if not disabled and not parser_module._traceback_best.nopython_signatures:
        raise AssertionError("The single-best traceback kernel did not compile in nopython mode")
    if not disabled and not bonus_module._accumulate_support.nopython_signatures:
        raise AssertionError("The read-bonus kernel did not compile in nopython mode")
    print(_strict_json_dumps({"payload": payload, "jit_disabled": disabled, "compiled": compiled}))


def run(command: list[str], environment: dict[str, str], *, success: bool = True) -> str:
    completed = subprocess.run(command, cwd=ROOT, env=environment, text=True,
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               check=False, timeout=300)
    if (completed.returncode == 0) != success:
        raise SystemExit(f"Unexpected command status {completed.returncode}: {command}\n{completed.stdout}")
    return completed.stdout


def main() -> None:
    configured = os.environ.get("HYB2_PYTHON")
    candidates = [Path(configured).expanduser() if configured else None,
                  ROOT / ".venv-cplfold" / "bin" / "python", Path(sys.executable)]
    runner = next(path for path in candidates if path and path.is_file())
    with tempfile.TemporaryDirectory(prefix="hyb2-cplcofold-") as directory:
        environment = os.environ.copy()
        environment["PYTHONDONTWRITEBYTECODE"] = "1"
        environment["NUMBA_CACHE_DIR"] = str(Path(directory) / "numba")
        environment.pop("NUMBA_DISABLE_JIT", None)
        jit = _strict_json_loads(run([str(runner), str(Path(__file__).resolve()), "--worker"], environment))
        plain_environment = environment.copy()
        plain_environment["NUMBA_DISABLE_JIT"] = "1"
        plain = _strict_json_loads(run([str(runner), str(Path(__file__).resolve()), "--worker"], plain_environment))
        if jit["jit_disabled"] or not plain["jit_disabled"]:
            raise SystemExit("JIT/Python parity check did not run both execution modes")
        if jit["payload"] != plain["payload"]:
            raise SystemExit("Numba and plain-Python cofold results differ")

        wrapper = [str(runner), str(ROOT / "bin" / "cplfold")]
        options = ["--beam", "20", "--max-phase1", "1", "--no-pseudoknot"]
        cli = run(wrapper + ["--sequence", "GGG&CCC"] + options, environment)
        if "(((&)))" not in cli or "Energy: -2.50 kcal/mol" not in cli:
            raise SystemExit(f"Cofold CLI lost its strand boundary or physical energy:\n{cli}")

        fasta = Path(directory) / "two-strands.fasta"
        fasta.write_text(">strand_A\nGGG\n>strand_B\nCCC\n", encoding="utf-8")
        fasta_cli = run(wrapper + ["--sequence-file", str(fasta), "--cofold"] + options, plain_environment)
        if "(((&)))" not in fasta_cli:
            raise SystemExit("Explicit two-record FASTA cofold input did not fold both strands")
        run(wrapper + ["--sequence-file", str(fasta)] + options, plain_environment, success=False)

        bonus = Path(directory) / "bonus.tsv"
        bonus.write_text("# sequence_length=4\nprepared_position_1\tprepared_position_2\tlog1p_gaussian_hyb_bonus\n1\t4\t5\n2\t3\t5\n", encoding="utf-8")
        guided_cli = run(wrapper + ["--sequence", "GG&CC", "--bonus-matrix-file", str(bonus), "--alpha", "1"] + options, plain_environment)
        if "((&))" not in guided_cli or "Energy: 0.80 kcal/mol" not in guided_cli:
            raise SystemExit(f"Cofold CLI confused evidence score with physical energy:\n{guided_cli}")
        for invalid in ("&GGG", "GGG&", "GG&CC&AA"):
            run(wrapper + ["--sequence", invalid] + options, plain_environment, success=False)

    print("CPLcofold Python verification passed: six C++ reference vectors, real nopython compilation, Python/JIT parity, constraints, strand-bounded read bonuses, strand-order and independent-monomer energy invariants, overflow handling, and sequence/FASTA CLI input.")
    print("Reference duplex: GGG&CCC => (((&))), physical energy -2.50 kcal/mol.")


if __name__ == "__main__":
    if "--worker" in sys.argv[1:]:
        worker()
    else:
        main()
