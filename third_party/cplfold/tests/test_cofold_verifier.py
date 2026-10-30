"""Fault-injection tests for the standalone cofold verifier, without JIT."""

import importlib.util
import math
from pathlib import Path
import sys
import types
import unittest
from unittest import mock

import numpy as np


SCRIPT = Path(__file__).resolve().parents[3] / "scripts" / "verify-cplcofold-python.py"


def load_verifier():
    # Loading the CLI as a module must neither import/replace Numba nor add
    # its vendor directory to global sys.path. Only worker() needs those.
    spec = importlib.util.spec_from_file_location("cofold_verifier_under_test", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


VERIFIER = load_verifier()
NONFINITE = (float("nan"), float("inf"), float("-inf"))


class FixtureRuntime:
    """Deterministic valid engine replies, with per-call corruption hooks.

    These doubles test verifier rejection, not folding correctness. They do
    not import a folding module or modify an existing suite's Numba shim.
    """

    def __init__(self):
        self.parse_fault = None
        self.energy_fault = None
        self.fold_fault = None
        self.bonus_fault = None
        self.parse_calls = []
        self.references = {seq: (structure, energy) for seq, _, structure, energy in VERIFIER.REFERENCE_CASES}
        runtime = self

        class Parser:
            def __init__(self, **kwargs):
                pass

            def set_bonus_matrix(self, matrix, length):
                pass

            def set_alpha(self, alpha):
                pass

            def parse(self, sequence, cons=None):
                runtime.parse_calls.append((sequence, cons))
                if sequence == "GGCC":
                    structure, energy = ("....", 0.0) if cons else ("(())", -9.2)
                else:
                    structure, energy = runtime.references[sequence]
                if runtime.parse_fault:
                    energy = runtime.parse_fault(sequence, cons, energy)
                return structure, energy, 0, 0.0

        self.parser_module = types.SimpleNamespace(BeamCKYCofoldParser=Parser)
        self.bonus_module = types.SimpleNamespace(build_cofold_bonus_matrix=self.bonus)

    def compute_energy(self, sequence, structure, cutpoint):
        if sequence in self.references:
            energy = self.references[sequence][1]
        elif sequence in ("AAUUAAA", "UUAAAAA"):
            energy = 8.94
        else:
            energy = -10.0 if "[" in structure else -5.0
        if self.energy_fault:
            energy = self.energy_fault(sequence, structure, energy)
        return {"energy": energy}

    def fold(self, sequence, **kwargs):
        if kwargs.get("beta") == 1e308:
            raise ValueError("Effective energy overflow")
        if sequence == "GGGG&CCCC":
            result = {
                "structure": "(([[&))]]", "energy": 1.766,
                "phase1_energy": -2.0, "effective_energy": 0.766,
                "energy_breakdown": {"duplex_init": 4.1},
            }
        else:
            result = {
                "structure": "(((&)))", "energy": -2.5, "score": -2.5,
                "effective_energy": -2.5, "read_support": 0.0, "read_bonus": 0.0,
            }
        if self.fold_fault:
            self.fold_fault(sequence, kwargs, result)
        return [result]

    def bonus(self, length, cutpoint, arms):
        result = np.zeros((8, 8))
        result[0, 4] = result[4, 0] = math.log(3.0)
        if self.bonus_fault:
            self.bonus_fault(result)
        return result

    def check(self):
        return VERIFIER._checked_payload(self.parser_module, self.bonus_module, self, self.fold, np)


class CofoldVerifierFaultTests(unittest.TestCase):
    def test_valid_fixture_runtime_passes_and_round_trips(self):
        payload = FixtureRuntime().check()
        self.assertEqual(VERIFIER._strict_json_loads(VERIFIER._strict_json_dumps(payload)), payload)

    def test_nan_in_only_gggaaaccc_reference_parse_is_rejected(self):
        runtime = FixtureRuntime()
        runtime.parse_fault = lambda seq, cons, energy: float("nan") if seq == "GGGAAACCC" else energy
        with self.assertRaisesRegex(AssertionError, "GGGAAACCC.*finite"):
            runtime.check()
        self.assertEqual(runtime.parse_calls, [(seq, None) for seq, *_ in VERIFIER.REFERENCE_CASES[:3]])

    def test_nonfinite_guided_and_forced_scores_are_rejected(self):
        for value in NONFINITE:
            for constrained in (False, True):
                with self.subTest(value=value, constrained=constrained):
                    runtime = FixtureRuntime()
                    runtime.parse_fault = lambda seq, cons, energy: value if seq == "GGCC" and bool(cons) == constrained else energy
                    with self.assertRaisesRegex(AssertionError, "finite"):
                        runtime.check()

    def test_nonfinite_physical_energy_and_invariant_inputs_are_rejected(self):
        for sequence in ("GGGAAACCC", "AAUUAAA", "GGCGCGGCACCGUCCGCGGAACAAACGGA"):
            for value in NONFINITE:
                with self.subTest(sequence=sequence, value=value):
                    runtime = FixtureRuntime()
                    runtime.energy_fault = lambda seq, structure, energy: value if seq == sequence else energy
                    with self.assertRaisesRegex(AssertionError, "finite"):
                        runtime.check()

    def test_nonfinite_pseudoknot_ranking_fields_are_rejected(self):
        for field in ("energy", "phase1_energy", "effective_energy", "duplex_init"):
            for value in NONFINITE:
                with self.subTest(field=field, value=value):
                    runtime = FixtureRuntime()

                    def corrupt(sequence, kwargs, result):
                        if sequence == "GGGG&CCCC":
                            target = result["energy_breakdown"] if field == "duplex_init" else result
                            target[field] = value

                    runtime.fold_fault = corrupt
                    with self.assertRaisesRegex(AssertionError, "finite"):
                        runtime.check()

    def test_nonfinite_bonus_matrix_is_rejected_before_maximum_comparison(self):
        for value in NONFINITE:
            with self.subTest(value=value):
                runtime = FixtureRuntime()
                runtime.bonus_fault = lambda matrix: matrix.__setitem__((1, 5), value)
                with self.assertRaisesRegex(AssertionError, "Read-bonus.*finite"):
                    runtime.check()

    def test_nonfinite_evidence_free_fields_are_rejected(self):
        for field in ("energy", "score", "effective_energy", "read_support", "read_bonus"):
            with self.subTest(field=field):
                runtime = FixtureRuntime()

                def corrupt(sequence, kwargs, result):
                    if kwargs.get("alpha") == 1e308:
                        result[field] = float("nan")

                runtime.fold_fault = corrupt
                with self.assertRaisesRegex(AssertionError, "finite"):
                    runtime.check()

    def test_nonfinite_payload_fields_not_used_in_comparisons_are_rejected(self):
        for field in ("score", "read_bonus", "nested"):
            for value in NONFINITE:
                with self.subTest(field=field, value=value):
                    runtime = FixtureRuntime()

                    def corrupt(sequence, kwargs, result):
                        if sequence == "GGG&CCC" and "alpha" not in kwargs:
                            result[field] = {"extra": [value]} if field == "nested" else value

                    runtime.fold_fault = corrupt
                    with self.assertRaises(ValueError):
                        runtime.check()


class CofoldVerifierJsonTests(unittest.TestCase):
    def test_serialization_rejects_nested_nonfinite_numbers(self):
        for value in NONFINITE:
            with self.subTest(value=value), self.assertRaises(ValueError):
                VERIFIER._strict_json_dumps({"payload": [{"energy": value}]})

    def test_parsing_rejects_nonstandard_nonfinite_constants(self):
        for value in ("NaN", "Infinity", "-Infinity"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                VERIFIER._strict_json_loads('{"payload":[{"energy":' + value + '}]}')

    def test_parsing_rejects_valid_json_exponents_that_overflow_float(self):
        for value in ("1e999", "-1e999", "1.8e308"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                VERIFIER._strict_json_loads('{"payload":[{"energy":' + value + '}]}')

    def test_parent_rejects_nonfinite_worker_json_before_payload_equality(self):
        for value in ("NaN", "Infinity", "1e999"):
            response = '{"jit_disabled":false,"payload":{"energy":' + value + '}}'
            with self.subTest(value=value), mock.patch.object(VERIFIER, "run", return_value=response) as run:
                with self.assertRaises(ValueError):
                    VERIFIER.main()
                run.assert_called_once()

    def test_finite_json_round_trip_preserves_flags_and_extreme_finite_numbers(self):
        payload = {"values": [1e308, -1e308, 1e-300, 0, True, False, None, "NaN"]}
        self.assertEqual(VERIFIER._strict_json_loads(VERIFIER._strict_json_dumps(payload)), payload)

    def test_loading_verifier_does_not_mutate_numba_or_import_path(self):
        missing = object()
        numba_before = sys.modules.get("numba", missing)
        path_before = list(sys.path)
        load_verifier()
        self.assertIs(sys.modules.get("numba", missing), numba_before)
        self.assertEqual(sys.path, path_before)


if __name__ == "__main__":
    unittest.main()
