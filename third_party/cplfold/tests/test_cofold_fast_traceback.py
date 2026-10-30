"""Rank-zero traceback must preserve the legacy retained-forest ordering.

These tests deliberately use _KBest as an independent traceback oracle on
the *same* inside chart.  They exercise exact structure selection, including
ties; re-running the parser alone could conceal a shared tie-breaking change.
Real Numba execution belongs in the separate process verifier.
"""

import importlib
import math
import random
import sys
import types
import unittest
from unittest import mock

import numpy as np


def _load_modules():
    # Match the existing unit suite's lightweight execution mode.  If a real
    # Numba module is already loaded, leave it intact for an explicit JIT run.
    if "numba" not in sys.modules:
        shim = types.ModuleType("numba")

        def njit(*args, **kwargs):
            if len(args) == 1 and callable(args[0]) and not kwargs:
                return args[0]
            return lambda function: function

        shim.njit = njit
        sys.modules["numba"] = shim
    return importlib.import_module("CPLcofold_parser"), importlib.import_module("CPLcofold")


def _matrix(n, rng, signed=False):
    matrix = np.zeros((n, n), dtype=np.float64)
    for left in range(n):
        for right in range(left + 1, n):
            # Nonbinary fractions expose differences in summation and ties.
            numerator = rng.randrange(-13 if signed else 0, 18)
            matrix[left, right] = matrix[right, left] = numerator / 7.0
    return matrix


class CofoldFastTracebackTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module, cls.cofold = _load_modules()

    def parser(self, cutpoint, beam=100, duplex=4.1, alpha=0.0, matrix=None):
        parser = self.module.BeamCKYCofoldParser(
            cutpoint=cutpoint, beam_size=beam, duplex_init=duplex,
            use_constraints=True,
        )
        parser.set_alpha(alpha)
        if matrix is not None:
            parser.set_bonus_matrix(matrix, len(matrix))
        return parser

    def assert_same_forest(self, parser, sequence, constraints=None, window_size=0):
        forest = parser._prepare(sequence, constraints)
        root = (self.module._C, 0, forest.n)
        oracle = self.module._KBest(forest).get(root, 0)
        # Both public methods reuse this exact forest, so differences cannot
        # be attributed to stochastic data, another beam, or chart pruning.
        with mock.patch.object(parser, "_prepare", return_value=forest) as prepare:
            parsed = parser.parse(sequence, cons=constraints)
            candidates = parser.parse_subopt(
                sequence, cons=constraints, energy_delta=0.0,
                max_structures=1, window_size=window_size,
            )
        self.assertEqual(prepare.call_count, 2)
        if oracle is None:
            self.assertIsNone(parsed[0])
            self.assertEqual(parsed[1], math.inf)
            self.assertEqual(candidates, [])
            return forest, oracle
        value, pairs = oracle
        structure = parser._structure(forest.n, pairs)
        self.assertEqual(parsed[0], structure)
        self.assertAlmostEqual(parsed[1], value / 100.0, places=9)
        self.assertEqual(len(candidates), 1)
        self.assertEqual(candidates[0][0], structure)
        self.assertAlmostEqual(candidates[0][1], value / 100.0, places=9)
        return forest, oracle

    def test_equal_energy_alternatives_keep_legacy_edge_order(self):
        cases = (
            ("GC", 1, 0.0, "..", "()"),
            ("ACUGUUCCCGGCUA", 3, 0.0, "((.)).........", ".(.......)...."),
            ("UGACCGGGA", 1, 0.0, "(.....)..", "(......)."),
        )
        for sequence, cutpoint, duplex, first, second in cases:
            with self.subTest(sequence=sequence):
                parser = self.parser(cutpoint, beam=0, duplex=duplex)
                forest, oracle = self.assert_same_forest(parser, sequence)
                kbest = self.module._KBest(forest)
                root = (self.module._C, 0, forest.n)
                alternative = kbest.get(root, 1)
                self.assertIsNotNone(alternative)
                self.assertEqual(oracle[0], alternative[0])
                self.assertEqual(parser._structure(forest.n, oracle[1]), first)
                self.assertEqual(parser._structure(forest.n, alternative[1]), second)

    def test_roundoff_sized_energy_difference_preserves_legacy_structure(self):
        # This is not a tie under the legacy float arithmetic: the paired
        # structure scores a tiny negative value and must precede all dots.
        sequence = "CGUCUUAAGCUCUUC"
        parser = self.parser(6, beam=0)
        forest, oracle = self.assert_same_forest(parser, sequence)
        alternative = self.module._KBest(forest).get((self.module._C, 0, forest.n), 1)
        self.assertEqual(parser._structure(forest.n, oracle[1]), ".((.....)).....")
        self.assertLess(oracle[0], alternative[0])
        self.assertLess(abs(oracle[0] - alternative[0]), 1e-10)

    def test_evidence_can_create_an_exact_bound_unbound_tie(self):
        matrix = np.array([[0.0, 4.1], [4.1, 0.0]])
        for beam in (0, 1, 3, 100):
            with self.subTest(beam=beam):
                parser = self.parser(1, beam=beam, alpha=1.0, matrix=matrix)
                forest, oracle = self.assert_same_forest(parser, "GC")
                alternative = self.module._KBest(forest).get((self.module._C, 0, 2), 1)
                self.assertEqual(oracle[0], alternative[0])
                self.assertEqual(parser._structure(2, oracle[1]), "..")
                self.assertEqual(parser._structure(2, alternative[1]), "()")

    def test_seeded_random_charts_match_with_fractional_and_signed_evidence(self):
        rng = random.Random(68121)
        modes = ((0.0, None), (0.0, False), (0.375, False), (1.25, True))
        for beam in (0, 1, 3, 100):
            for duplex in (0.0, 4.1):
                for alpha, signed in modes:
                    for _ in range(3):
                        n = rng.randrange(2, 22)
                        sequence = "".join(rng.choice("ACGU") for _ in range(n))
                        cutpoint = rng.randrange(1, n)
                        matrix = None if signed is None else _matrix(n, rng, signed)
                        with self.subTest(sequence=sequence, cutpoint=cutpoint, beam=beam,
                                          duplex=duplex, alpha=alpha, signed=signed):
                            self.assert_same_forest(
                                self.parser(cutpoint, beam, duplex, alpha, matrix), sequence,
                            )

    def test_forced_nested_and_partial_constraints_match_on_pruned_charts(self):
        cases = (
            ("GGGCCC", 3, "((()))"),
            ("AAAAGGGAAACCC", 4, "....(((...)))"),
            ("GGAAACGAAACC", 11, "((...)(...))"),
            ("GGAAACGAAACC", 1, "((...)(...))"),
            ("GGAAACGAAACCA", 12, "((...)(...))."),
            ("AGGAAACGAAACC", 1, ".((...)(...))"),
            # An outer duplex encloses ordinary stems on both strands;
            # these force the left and right exterior traceback branches.
            ("G" + "GGGAAACCC" * 2 + "C", 10,
             "(" + "(((...)))" * 2 + ")"),
            # A multibranch face contains one ordinary and one cross-nick stem.
            ("G" + "GGGAAACCC" + "GGGCCC" + "C", 13,
             "(" + "(((...)))" + "((()))" + ")"),
            ("GGGAAACCCGGGAAACCC", 9, "(((...((()))...)))"),
            ("GGGGCCCC", 4, "(??????)"),
            ("GGGGCCCC", 4, "?.????.?"),
            ("GGGGCCCC", 4, np.array([7, -1, -1, -1, -1, -1, -1, 0], dtype=np.int32)),
        )
        rng = random.Random(319)
        for sequence, cutpoint, constraints in cases:
            matrix = _matrix(len(sequence), rng, signed=True)
            for beam in (0, 1, 3, 100):
                for duplex in (0.0, 4.1):
                    with self.subTest(sequence=sequence, constraints=str(constraints), beam=beam, duplex=duplex):
                        self.assert_same_forest(
                            self.parser(cutpoint, beam, duplex, 0.375, matrix),
                            sequence, constraints,
                        )

    def test_forced_unpaired_constraints_ignore_favorable_pair_evidence(self):
        sequence = "GGGAAACCCGGGAAACCC"
        matrix = np.full((len(sequence), len(sequence)), 10.0)
        np.fill_diagonal(matrix, 0.0)
        for beam in (0, 1, 3, 100):
            for duplex in (0.0, 4.1):
                with self.subTest(beam=beam, duplex=duplex):
                    _, oracle = self.assert_same_forest(
                        self.parser(9, beam, duplex, 0.375, matrix),
                        sequence, "." * len(sequence), window_size=100,
                    )
                    self.assertEqual(oracle, (0.0, ()))

    def test_both_strand_faces_with_longer_stems_match(self):
        cases = (
            ("GGGAAACCCGGGCCCGGGAAACCC", 12),
            ("GGCGCGGCACCGUCCGCGGAACAAACGGA", 27),
            ("GGGAAACCCGGGAAACCCGGGAAACCCGGGAAACCC", 18),
        )
        rng = random.Random(809)
        for sequence, cutpoint in cases:
            matrix = _matrix(len(sequence), rng, signed=True)
            for beam in (0, 1, 3, 100):
                for duplex in (0.0, 4.1):
                    with self.subTest(sequence=sequence, beam=beam, duplex=duplex):
                        self.assert_same_forest(
                            self.parser(cutpoint, beam, duplex, 1.0 / 3.0, matrix), sequence,
                        )

    def test_rank_zero_skips_python_kbest_and_pair_start_lists(self):
        parser = self.parser(3)
        message = "Rank zero should use the compiled traceback"
        with mock.patch.object(self.module, "_KBest", side_effect=AssertionError(message)), \
                mock.patch.object(self.module._Forest, "pair_starts", new_callable=mock.PropertyMock,
                                  side_effect=AssertionError(message)):
            parsed = parser.parse("GGGCCC")
            candidates = parser.parse_subopt("GGGCCC", max_structures=1)
        self.assertEqual(parsed[0], "((()))")
        self.assertAlmostEqual(parsed[1], -2.5)
        self.assertEqual(candidates[0][0], parsed[0])
        self.assertAlmostEqual(candidates[0][1], parsed[1])

    def test_multiple_candidates_keep_the_legacy_lazy_enumerator(self):
        parser = self.parser(3)
        forest = parser._prepare("GGGCCC", None)
        root = (self.module._C, 0, forest.n)
        legacy_class = self.module._KBest
        kbest = legacy_class(forest)
        expected = []
        for rank in range(3):
            value, pairs = kbest.get(root, rank)
            expected.append((parser._structure(forest.n, pairs), value / 100.0))
        with mock.patch.object(parser, "_prepare", return_value=forest), \
                mock.patch.object(self.module, "_KBest", wraps=legacy_class) as legacy, \
                mock.patch.object(self.module._Forest, "best", side_effect=AssertionError("Multi-candidate path changed")):
            actual = parser.parse_subopt("GGGCCC", energy_delta=100.0, max_structures=3)
        legacy.assert_called_once_with(forest)
        self.assertEqual(actual, expected)

    def assert_nested_equal(self, actual, expected):
        if isinstance(expected, float):
            self.assertAlmostEqual(actual, expected, places=9)
        elif isinstance(expected, dict):
            self.assertEqual(actual.keys(), expected.keys())
            for key in expected:
                with self.subTest(field=key):
                    self.assert_nested_equal(actual[key], expected[key])
        elif isinstance(expected, (list, tuple)):
            self.assertEqual(len(actual), len(expected))
            for left, right in zip(actual, expected):
                self.assert_nested_equal(left, right)
        else:
            self.assertEqual(actual, expected)

    def assert_public_parity(self, first, second, **options):
        original_best = self.module._Forest.best
        fast_calls, legacy_calls = [], []

        def fast_best(forest):
            fast_calls.append(forest.duplex)
            return original_best(forest)

        def legacy_best(forest):
            legacy_calls.append(forest.duplex)
            return self.module._KBest(forest).get((self.module._C, 0, forest.n), 0)

        with mock.patch.object(self.module._Forest, "best", fast_best):
            actual = self.cofold.cofold(first, second, verbose=False, **options)
        with mock.patch.object(self.module._Forest, "best", legacy_best):
            expected = self.cofold.cofold(first, second, verbose=False, **options)
        self.assertTrue(fast_calls, "Public scenario did not exercise the optimized route")
        self.assertEqual(fast_calls, legacy_calls)
        self.assert_nested_equal(actual, expected)
        return actual

    def test_public_alpha_search_parity_with_rank_one_primary_candidate(self):
        matrix = np.full((4, 4), -1.0 / 7.0)
        np.fill_diagonal(matrix, 0.0)
        matrix[0, 3] = matrix[3, 0] = 5.5
        matrix[1, 2] = matrix[2, 1] = 4.0 / 7.0
        structures = []
        for alpha in (0.0, 0.375):
            with self.subTest(alpha=alpha):
                results = self.assert_public_parity(
                    "GG", "CC", beam_size=3, max_phase1=1, allow_pseudoknot=False,
                    alpha=alpha, bonus_matrix=matrix,
                )
                structures.append(results[0]["structure"])
        self.assertEqual(structures, ["..&..", "((&))"])

    def test_public_beta_pseudoknot_parity_when_both_phases_use_fast_path(self):
        sequence = "GGCGCGGCACCGUCCGCGGAACAAACGG"
        matrix = _matrix(len(sequence) + 1, random.Random(66), signed=True) * 0.025
        for alpha in (0.0, 0.375):
            for beta in (0.0, 0.75):
                with self.subTest(alpha=alpha, beta=beta):
                    results = self.assert_public_parity(
                        sequence, "A", beam_size=30, max_phase1=1, max_phase2=1,
                        energy_delta=12.0, alpha=alpha, beta=beta, bonus_matrix=matrix,
                    )
                    pseudoknots = [item for item in results if item["type"] == "pseudoknot"]
                    self.assertTrue(pseudoknots)
                    for item in pseudoknots:
                        self.assertAlmostEqual(
                            item["effective_energy"], item["energy"] + beta * item["phase1_energy"], places=9,
                        )

    def test_public_connected_pseudoknots_match_when_only_phase_two_is_fast(self):
        matrix = _matrix(8, random.Random(8), signed=True) * 0.05
        for alpha in (0.0, 0.375):
            with self.subTest(alpha=alpha):
                results = self.assert_public_parity(
                    "GGGG", "CCCC", beam_size=30, max_phase1=12, max_phase2=1,
                    energy_delta=12.0, alpha=alpha, beta=0.75, bonus_matrix=matrix,
                )
                pseudoknots = [item for item in results if item["type"] == "pseudoknot"]
                self.assertTrue(pseudoknots)
                self.assertTrue(any(item["phase2_association_cost"] == 0.0 for item in pseudoknots))


if __name__ == "__main__":
    unittest.main()
