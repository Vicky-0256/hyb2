"""Numeric boundary regressions for cofold search and result metadata."""

import importlib
import json
from pathlib import Path
import runpy
import sys
import tempfile
import types
import unittest
from unittest import mock
import warnings

import numpy as np


def _load_modules():
    if "numba" not in sys.modules:
        shim = types.ModuleType("numba")

        def njit(*args, **kwargs):
            if len(args) == 1 and callable(args[0]) and not kwargs:
                return args[0]
            return lambda function: function

        shim.njit = njit
        sys.modules["numba"] = shim
    return (
        importlib.import_module("CPLcofold"),
        importlib.import_module("CPLcofold_parser"),
        importlib.import_module("Utils.cofold_energy"),
    )


def _duplex_matrix(value, pairs=((0, 5), (1, 4), (2, 3))):
    matrix = np.zeros((6, 6), dtype=np.float64)
    for left, right in pairs:
        matrix[left, right] = matrix[right, left] = value
    return matrix


class CofoldNumericTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cofold_module, cls.parser_module, cls.energy_module = _load_modules()

    def fold(self, **kwargs):
        options = dict(verbose=False, allow_pseudoknot=False, max_phase1=1)
        options.update(kwargs)
        return self.cofold_module.cofold("GGG", "CCC", **options)

    def test_huge_alpha_has_no_effect_without_nonzero_evidence(self):
        baseline = self.fold()[0]
        for matrix in (None, np.zeros((6, 6))):
            with self.subTest(matrix=matrix is not None), warnings.catch_warnings():
                warnings.simplefilter("error", RuntimeWarning)
                result = self.fold(alpha=1e308, bonus_matrix=matrix)[0]
                for key in ("structure", "score", "energy", "effective_energy"):
                    self.assertEqual(result[key], baseline[key])
                self.assertEqual(result["read_support"], 0.0)
                self.assertEqual(result["read_bonus"], 0.0)
                json.dumps(result, allow_nan=False)

    def test_scale_tiny_evidence_before_converting_to_centikcal(self):
        with warnings.catch_warnings():
            warnings.simplefilter("error", RuntimeWarning)
            result = self.fold(alpha=1e308, bonus_matrix=_duplex_matrix(1e-308))[0]
        self.assertEqual(result["structure"], "(((&)))")
        self.assertAlmostEqual(result["score"], -5.5, places=6)
        self.assertAlmostEqual(result["energy"], -2.5, places=6)
        self.assertAlmostEqual(result["read_bonus"], 3.0, places=6)
        json.dumps(result, allow_nan=False)

    def test_alpha_zero_ignores_large_evidence_in_search(self):
        matrix = _duplex_matrix(1e308, pairs=((0, 5),))
        result = self.fold(alpha=0.0, bonus_matrix=matrix)[0]
        self.assertEqual(result["structure"], "(((&)))")
        self.assertAlmostEqual(result["score"], -2.5, places=6)
        self.assertEqual(result["read_support"], 1e308)
        self.assertEqual(result["read_bonus"], 0.0)
        json.dumps(result, allow_nan=False)

    def test_scaled_evidence_overflow_is_rejected_before_numba(self):
        for value, alpha in ((1e308, 1.0), (-1e308, 1.0), (1.0, 1e308), (1e29, 1.0), (1e27, 1.0)):
            with self.subTest(value=value, alpha=alpha):
                parser = self.parser_module.BeamCKYCofoldParser(cutpoint=3)
                parser.set_alpha(alpha)
                parser.set_bonus_matrix(_duplex_matrix(value), 6)
                with mock.patch.object(self.parser_module, "_face_energies") as faces, mock.patch.object(self.parser_module, "_inside") as inside:
                    with self.assertRaisesRegex(ValueError, "Scaled bonus"):
                        parser.parse("GGGCCC")
                    faces.assert_not_called()
                    inside.assert_not_called()

    def test_read_support_sum_overflow_is_rejected_even_at_alpha_zero(self):
        with self.assertRaisesRegex(ValueError, "Read support"):
            self.fold(alpha=0.0, bonus_matrix=_duplex_matrix(1e308))

    def test_beta_overflow_is_rejected_for_a_real_pseudoknot(self):
        with self.assertRaisesRegex(ValueError, "Effective energy"):
            self.cofold_module.cofold(
                "GGCGCGGCACCGUCCGCGGAACAAACGG", "A", verbose=False,
                beam_size=20, max_phase1=2, max_phase2=1, beta=1e308,
            )

    def test_large_beta_does_not_change_secondary_only_results(self):
        baseline = self.fold()[0]
        result = self.fold(beta=1e308)[0]
        self.assertEqual(result["effective_energy"], baseline["energy"])
        json.dumps(result, allow_nan=False)

    def test_result_boundary_rejects_nonfinite_search_scores(self):
        for score in (float("inf"), float("-inf"), float("nan")):
            with self.subTest(score=score), mock.patch.object(
                self.parser_module.BeamCKYCofoldParser, "parse_subopt", return_value=[("((()))", score)]
            ), self.assertRaisesRegex(ValueError, "Search score"):
                self.fold()

    def test_result_boundary_rejects_nonfinite_physical_energy(self):
        with mock.patch.object(self.energy_module.CofoldEnergy, "compute_energy", return_value={"energy": float("nan")}), self.assertRaisesRegex(ValueError, "Physical energy"):
            self.fold()

    def test_result_boundary_rejects_read_bonus_overflow(self):
        # Bypass both early scaling guards to independently verify the public
        # result boundary also protects callers from a non-finite bonus.
        with mock.patch.object(self.cofold_module, "_validate_search_bonus"), mock.patch.object(self.parser_module.BeamCKYCofoldParser, "parse_subopt", return_value=[("((()))", -2.5)]), self.assertRaisesRegex(ValueError, "Read bonus"):
            self.fold(alpha=2.0, bonus_matrix=_duplex_matrix(3e307))

    def test_invalid_multipliers_have_consistent_value_errors(self):
        for value in (-1, float("nan"), float("inf"), float("-inf"), True, np.bool_(True), None, 10 ** 1000):
            with self.subTest(value=str(value)[:20]):
                parser = self.parser_module.BeamCKYCofoldParser(cutpoint=3)
                with self.assertRaises(ValueError):
                    parser.set_alpha(value)
                with self.assertRaises(ValueError):
                    parser.parse_subopt("GGGCCC", energy_delta=value)
                for name in ("alpha", "beta"):
                    with self.subTest(name=name), self.assertRaises(ValueError):
                        self.fold(**{name: value})

    def test_alpha_zero_still_validates_the_input_matrix(self):
        for matrix in (np.zeros((7, 7)), np.full((6, 6), np.nan), np.full((6, 6), np.inf), np.ones((6, 6), dtype=complex)):
            with self.subTest(shape=matrix.shape, dtype=matrix.dtype), self.assertRaises(ValueError):
                self.fold(alpha=0.0, bonus_matrix=matrix)

    def test_signed_finite_pair_penalties_remain_supported(self):
        parser = self.parser_module.BeamCKYCofoldParser(cutpoint=3)
        parser.set_bonus_matrix(_duplex_matrix(-1.0, pairs=((0, 5),)), 6)
        parser.set_alpha(1.0)
        result = parser.parse("GGGCCC", cons="((()))")
        self.assertEqual(result[0], "((()))")
        self.assertAlmostEqual(result[1], -1.5, places=6)

    def test_invalid_or_overflowing_association_cost_is_rejected(self):
        for value in (-1.0, float("nan"), float("inf"), True, 10 ** 1000):
            with self.subTest(value=str(value)[:20]), self.assertRaises(ValueError):
                self.parser_module.BeamCKYCofoldParser(cutpoint=3, duplex_init=value)
        parser = self.parser_module.BeamCKYCofoldParser(cutpoint=3, duplex_init=1e308)
        with mock.patch.object(self.parser_module, "_inside") as inside, self.assertRaisesRegex(ValueError, "Duplex initiation"):
            parser.parse("GGGCCC")
        inside.assert_not_called()


class CofoldFastaPreambleTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.wrapper = runpy.run_path(str(Path(__file__).resolve().parents[3] / "bin" / "cplfold"))

    def read(self, contents, cofold=True):
        with tempfile.TemporaryDirectory(prefix="cofold-fasta-regression-") as directory:
            path = Path(directory) / "input.fasta"
            path.write_text(contents, encoding="utf-8")
            return self.wrapper["_read_sequence_file"](path, cofold=cofold)

    def test_sequence_preamble_before_a_fasta_header_is_rejected(self):
        for contents in ("AAA\n>A\nGGG\n>B\nCCC\n", "AAA\n>A\nGGG\n"):
            with self.subTest(contents=contents), self.assertRaisesRegex(SystemExit, "first record header"):
                self.read(contents)

    def test_plain_sequences_and_valid_fasta_remain_supported(self):
        self.assertEqual(self.read("GGG&CCC\n"), "GGG&CCC")
        self.assertEqual(self.read(">A\nGGG\n>B\nCCC\n"), "GGG&CCC")
        self.assertEqual(self.read(">A\nGGG\n", cofold=False), "GGG")


if __name__ == "__main__":
    unittest.main()
