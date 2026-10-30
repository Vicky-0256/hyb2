"""Cofold regressions using artificial sequences and LinearCoFold oracles.

The fixed secondary-structure vectors below come from the vendored C++
LinearCoFold reference (commit 13c0beee5b11), using an unlimited beam.  Only
the final structure/energy is used: its verbose unbound-energy decomposition
incorrectly includes a duplex-initiation term.  No reference executable or
ViennaRNA installation is required to run these tests.
"""

import importlib
import math
import sys
import types
import unittest

import numpy as np


# Single-strand synthetic regression from the existing CPLfold test suite.
SINGLE_SEQUENCE = "GGCGCGGCACCGUCCGCGGAACAAACGG"
SINGLE_STRUCTURE = "..(((((..[[[[)))))......]]]]"

# Input order deliberately matches the C++ output order.  The Python API must
# preserve the caller's strand order, including when the first strand is longer.
REFERENCE_CASES = (
    ("G&C", ".&.", 0.0),
    ("GG&CC", "..&..", 0.0),
    ("GGG&CCC", "(((&)))", -2.5),
    ("GGGG&CCCC", "((((&))))", -5.8),
    ("GCGC&GCGC", "((((&))))", -5.1),
    ("GGG&AAACCC", "(((&...)))", -3.0),
    ("AGGG&CCCA", ".(((&))).", -4.0),
    ("AAAA&AAAA", "....&....", 0.0),
    ("AAAA&GGGAAACCC", "....&(((...)))", -1.2),
    ("CCC&GGGAAACCCGGG", "(((&(((...))))))", -5.7),
    ("GGGAAACCC&GGGAAACCC", "(((...(((&)))...)))", -7.1),
    ("GGGAAACCCGGG&CCCGGGAAACCC", "(((...((((((&))))))...)))", -16.1),
)

# Forced positive-energy structures probe faces that are absent from an MFE.
FORCED_REFERENCE_CASES = (
    ("CCUCGG&A", "(....)&.", 2.5),
    ("GGAAACGAAAC&C", "((...)(...)&)", 11.7),
    ("G&GAAACGAAACC", "(&(...)(...))", 11.8),
)


def _load_modules():
    # Existing tests use the same shim.  Real Numba compilation is tested in a
    # fresh interpreter by scripts/verify-cplcofold-python.py.
    if "numba" not in sys.modules:
        shim = types.ModuleType("numba")

        def njit(*args, **kwargs):
            if len(args) == 1 and callable(args[0]) and not kwargs:
                return args[0]
            return lambda function: function

        shim.njit = njit
        sys.modules["numba"] = shim
    return (
        importlib.import_module("CPLfold"),
        importlib.import_module("CPLcofold_parser"),
        importlib.import_module("Utils.cofold_energy"),
    )


def _split(value):
    first, second = value.split("&")
    return first + second, len(first)


def _pairs(structure):
    stacks = {symbol: [] for symbol in "([{<"}
    opening = {")": "(", "]": "[", "}": "{", ">": "<"}
    result = []
    for position, symbol in enumerate(structure.replace("&", "")):
        if symbol in stacks:
            stacks[symbol].append(position)
        elif symbol in opening:
            result.append((stacks[opening[symbol]].pop(), position))
    return sorted(result)


class CofoldParserTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        _, cls.module, _ = _load_modules()

    def parser(self, cutpoint, **kwargs):
        return self.module.BeamCKYCofoldParser(
            cutpoint=cutpoint, beam_size=100, use_constraints=True, **kwargs
        )

    def test_cpp_secondary_structure_reference_vectors(self):
        for sequence, structure, expected in REFERENCE_CASES:
            with self.subTest(sequence=sequence):
                compact, cutpoint = _split(sequence)
                actual = self.parser(cutpoint).parse(compact)
                self.assertEqual(actual[0], structure.replace("&", ""))
                self.assertAlmostEqual(actual[1], expected, places=6)

    def test_short_cross_strand_pair_has_no_hairpin_penalty(self):
        for sequence, cutpoint, structure, expected in (
            ("GC", 1, "()", 4.1),
            ("GGCC", 2, "(())", 0.8),
            ("GGGCCC", 3, "((()))", -2.5),
        ):
            with self.subTest(sequence=sequence):
                actual = self.parser(cutpoint).parse(sequence, cons=structure)
                self.assertEqual(actual[0], structure)
                self.assertAlmostEqual(actual[1], expected, places=6)

    def test_special_hairpin_and_boundary_nicked_multiloop_reference_energies(self):
        for sequence, structure, expected in FORCED_REFERENCE_CASES:
            with self.subTest(sequence=sequence):
                compact, cutpoint = _split(sequence)
                forced = structure.replace("&", "")
                actual = self.parser(cutpoint).parse(compact, cons=forced)
                self.assertEqual(actual[0], forced)
                self.assertAlmostEqual(actual[1], expected, places=6)

    def test_duplex_initiation_is_charged_once_for_an_entire_stem(self):
        ordinary = self.parser(4, duplex_init=4.1).parse("GGGGCCCC", cons="(((())))")
        doubled = self.parser(4, duplex_init=8.2).parse("GGGGCCCC", cons="(((())))")
        self.assertEqual(ordinary[0], doubled[0])
        self.assertAlmostEqual(doubled[1] - ordinary[1], 4.1, places=6)

    def test_forced_unpaired_positions_prevent_binding(self):
        actual = self.parser(3).parse("GGGCCC", cons="......")
        self.assertEqual(actual[0], "......")
        self.assertAlmostEqual(actual[1], 0.0, places=8)

    def test_constraint_array_and_string_have_identical_coordinates(self):
        parser = self.parser(3)
        string = parser.parse("GGGCCC", cons="((()))")
        array = parser.parse("GGGCCC", cons=np.array([5, 4, 3, 2, 1, 0]))
        self.assertEqual(string[:2], array[:2])

    def test_impossible_or_malformed_forced_pairs_are_rejected(self):
        cases = (
            ("GCAA", 2, "()??"),  # Short pair on one strand.
            ("AAAA", 2, "(())"),  # Noncanonical pairs.
            ("GGCC", 2, "((.)"),  # Unbalanced string.
            ("GGCC", 2, "???"),  # Incorrect coordinate count.
            ("GGCC", 2, np.array([3, -1, -1, -1])),  # Asymmetric partner array.
            ("GGCC", 2, np.array([2, 3, 0, 1])),  # Forced crossing pairs in a nested phase.
        )
        for sequence, cutpoint, constraint in cases:
            with self.subTest(sequence=sequence, constraint=str(constraint)):
                with self.assertRaises(ValueError):
                    self.parser(cutpoint).parse(sequence, cons=constraint)

    def test_evidence_can_select_a_duplex_that_is_not_thermodynamically_bound(self):
        matrix = np.zeros((4, 4))
        matrix[0, 3] = matrix[3, 0] = 5.0
        matrix[1, 2] = matrix[2, 1] = 5.0
        parser = self.parser(2)
        parser.set_bonus_matrix(matrix, 4)
        parser.set_alpha(0.0)
        self.assertEqual(parser.parse("GGCC")[0], "....")
        parser.set_alpha(1.0)
        guided = parser.parse("GGCC")
        self.assertEqual(guided[0], "(())")
        self.assertAlmostEqual(guided[1], -9.2, places=6)

    def test_adjacent_cross_nick_bonus_is_counted_once_per_pair(self):
        matrix = np.zeros((6, 6))
        for left, right in ((0, 5), (1, 4), (2, 3)):
            matrix[left, right] = matrix[right, left] = 2.0
        parser = self.parser(3)
        parser.set_bonus_matrix(matrix, 6)
        parser.set_alpha(0.5)
        result = parser.parse("GGGCCC", cons="((()))")
        self.assertEqual(result[0], "((()))")
        self.assertAlmostEqual(result[1], -5.5, places=6)

    def test_suboptimals_are_unique_sorted_and_obey_constraints(self):
        parser = self.parser(3)
        structures = parser.parse_subopt("GGGCCC", energy_delta=10.0, max_structures=10)
        self.assertTrue(structures)
        self.assertEqual(structures[0][0], "((()))")
        self.assertEqual(len(structures), len({item[0] for item in structures}))
        self.assertEqual([item[1] for item in structures], sorted(item[1] for item in structures))
        forced = parser.parse_subopt("GGGCCC", cons="......", max_structures=10)
        self.assertEqual(forced, [("......", 0.0)])


class CofoldEnergyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        _, _, cls.module = _load_modules()
        cls.evaluator = cls.module.CofoldEnergy()

    def test_cpp_secondary_energies_include_internal_loops_and_nicked_faces(self):
        for sequence, structure, expected in REFERENCE_CASES + FORCED_REFERENCE_CASES:
            with self.subTest(sequence=sequence):
                compact, cutpoint = _split(sequence)
                actual = self.evaluator.compute_energy(compact, structure.replace("&", ""), cutpoint)
                self.assertAlmostEqual(actual["energy"], expected, places=6)
                self.assertAlmostEqual(sum(actual["breakdown"].values()), actual["energy"], places=6)
                self.assertEqual(actual["metadata"]["energy_kind"], "thermodynamic")

    def test_single_interstrand_pair_and_unbound_strands_have_distinct_energies(self):
        bound = self.evaluator.compute_energy("GC", "()", 1)
        free = self.evaluator.compute_energy("GC", "..", 1)
        self.assertAlmostEqual(bound["energy"], 4.1, places=6)
        self.assertAlmostEqual(free["energy"], 0.0, places=6)

    def test_last_packaged_special_hairpin_motif_is_recognized(self):
        # CCUCGG is the final hexanucleotide entry in the tetraloop string;
        # trailing whitespace must not truncate it when motifs are indexed.
        result = self.evaluator.compute_energy("ACCUCGG", ".(....)", 1)
        self.assertAlmostEqual(result["energy"], 2.5, places=6)

    def test_short_intramolecular_hairpin_is_rejected_at_an_actual_strand_boundary(self):
        with self.assertRaises(ValueError):
            self.evaluator.compute_energy("GCAA", "()..", 2)

    def test_nick_changes_loop_energy_without_changing_nucleotide_coordinates(self):
        sequence = "GGGAAACCC"
        structure = "(((...)))"
        nicked = self.evaluator.compute_energy(sequence, structure, 3)
        disconnected = self.evaluator.compute_energy("A" + sequence, "." + structure, 1)
        self.assertAlmostEqual(nicked["energy"], -3.0, places=6)
        self.assertAlmostEqual(disconnected["energy"], -1.2, places=6)

    def test_intramolecular_pseudoknot_uses_the_common_cofold_energy_baseline(self):
        result = self.evaluator.compute_energy(SINGLE_SEQUENCE + "A", SINGLE_STRUCTURE + ".", len(SINGLE_SEQUENCE))
        self.assertAlmostEqual(result["energy"], -15.346, places=4)
        self.assertEqual(result["metadata"]["local_energy_baseline"], "Turner-LinearCoFold")
        self.assertEqual(result["metadata"]["energy_model_version"], "strand-graph-v2")
        self.assertEqual(result["breakdown"]["duplex_init"], 0.0)

    def test_connected_pseudoknot_is_explicitly_marked_heuristic(self):
        result = self.evaluator.compute_energy("GGGGCCCC", "(([[))]]", 4, model="DP09")
        self.assertTrue(math.isfinite(result["energy"]))
        self.assertEqual(result["metadata"]["energy_kind"], "heuristic")
        self.assertAlmostEqual(result["breakdown"]["duplex_init"], 4.1, places=6)
        self.assertAlmostEqual(sum(result["breakdown"].values()), result["energy"], places=6)
        for recoloured in ("[[((]]))", "AA[[aa]]"):
            with self.subTest(structure=recoloured):
                equivalent = self.evaluator.compute_energy("GGGGCCCC", recoloured, 4, model="DP09")
                self.assertAlmostEqual(equivalent["energy"], result["energy"], places=8)

    def test_connected_pseudoknot_never_silently_falls_back_from_cc_or_re(self):
        for model in ("CC06", "CC09", "RE"):
            with self.subTest(model=model), self.assertRaises(ValueError):
                self.evaluator.compute_energy("GGGGCCCC", "(([[))]]", 4, model=model)


class CofoldIntegrationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cplfold, _, cls.energy_module = _load_modules()

    def fold(self, sequence, **kwargs):
        options = dict(verbose=False, beam_size=30, max_phase1=4, max_phase2=2, allow_pseudoknot=False)
        options.update(kwargs)
        return self.cplfold.two_phase_pseudoknot_fold(sequence, **options)

    def test_ampersand_and_explicit_cutpoint_return_identical_results(self):
        marked = self.fold("GGG&CCC")
        compact = self.fold("GGGCCC", cutpoint=3)
        self.assertEqual(marked, compact)
        wrapper = importlib.import_module("CPLcofold")
        self.assertEqual(marked, wrapper.cofold("ggg", "ccc", verbose=False, beam_size=30, max_phase1=4, max_phase2=2, allow_pseudoknot=False))
        self.assertEqual(marked[0]["structure"], "(((&)))")
        self.assertAlmostEqual(marked[0]["energy"], -2.5, places=6)
        self.assertEqual(marked[0]["cutpoint"], 3)
        self.assertEqual(marked[0]["strand_lengths"], [3, 3])
        self.assertEqual(marked[0]["intermolecular_pairs"], 3)
        self.assertEqual(marked[0]["energy_metadata"]["energy_kind"], "thermodynamic")

    def test_long_first_strand_order_and_coordinate_metadata_are_preserved(self):
        result = self.fold("GGGAAACCCGGG&CCC")[0]
        self.assertEqual(result["structure"].index("&"), 12)
        self.assertEqual(result["cutpoint"], 12)
        self.assertEqual(result["strand_lengths"], [12, 3])
        self.assertEqual(result["intermolecular_pairs"], sum(i < 12 <= j for i, j in _pairs(result["structure"])))
        self.assertTrue(any(j < 12 for i, j in _pairs(result["structure"])))
        self.assertGreater(result["intermolecular_pairs"], 0)

    def test_no_binding_result_retains_separate_intramolecular_fold(self):
        result = self.fold("AAAA&GGGAAACCC")[0]
        self.assertEqual(result["structure"], "....&(((...)))")
        self.assertEqual(result["intermolecular_pairs"], 0)
        self.assertAlmostEqual(result["energy"], -1.2, places=6)

    def test_physical_energy_is_independent_of_read_bonus_alpha(self):
        matrix = np.zeros((6, 6))
        for left, right in ((0, 5), (1, 4), (2, 3)):
            matrix[left, right] = matrix[right, left] = 2.0
        plain = self.fold("GGG&CCC", bonus_matrix=matrix, alpha=0.0)[0]
        guided = self.fold("GGG&CCC", bonus_matrix=matrix, alpha=0.5)[0]
        self.assertEqual(plain["structure"], guided["structure"])
        self.assertAlmostEqual(plain["energy"], -2.5, places=6)
        self.assertAlmostEqual(guided["energy"], plain["energy"], places=8)
        self.assertAlmostEqual(guided["score"] - plain["score"], -3.0, places=6)

    def test_generated_interstrand_pseudoknot_scores_one_association(self):
        results = self.fold("GGGG&CCCC", allow_pseudoknot=True, max_phase1=12, max_phase2=3, energy_delta=12.0)
        candidate = next(item for item in results if item["structure"] == "(([[&))]]")
        self.assertEqual(candidate["type"], "pseudoknot")
        self.assertEqual(candidate["intermolecular_pairs"], 4)
        self.assertEqual(candidate["phase2_association_cost"], 0.0)
        self.assertAlmostEqual(candidate["energy_breakdown"]["duplex_init"], 4.1, places=6)
        self.assertAlmostEqual(candidate["energy"], 1.766, places=6)
        self.assertEqual(candidate["energy_metadata"]["energy_kind"], "heuristic")

    def test_beta_reranks_real_pseudoknot_candidates_without_changing_physical_energy(self):
        sequence = SINGLE_SEQUENCE + "&A"
        plain = self.fold(sequence, allow_pseudoknot=True, max_phase1=6, energy_delta=8.0, beta=0.0)
        biased = self.fold(sequence, allow_pseudoknot=True, max_phase1=6, energy_delta=8.0, beta=0.75)
        self.assertTrue(any(item["type"] == "pseudoknot" for item in plain))
        by_structure = {item["structure"]: item for item in plain}
        self.assertEqual(set(by_structure), {item["structure"] for item in biased})
        self.assertNotEqual([item["structure"] for item in plain], [item["structure"] for item in biased])
        for item in biased:
            original = by_structure[item["structure"]]
            self.assertAlmostEqual(item["energy"], original["energy"], places=8)
            expected = item["energy"]
            if item["type"] == "pseudoknot":
                expected += 0.75 * item["phase1_energy"]
                self.assertIn("&", item["phase1_struct"])
                self.assertIn("&", item["phase2_struct"])
            self.assertAlmostEqual(item["effective_energy"], expected, places=8)
        self.assertEqual([item["effective_energy"] for item in biased], sorted(item["effective_energy"] for item in biased))

    def test_invalid_boundaries_and_bases_are_rejected(self):
        cases = (
            ("&GC", None), ("GC&", None), ("G&C&G", None),
            ("GC", 0), ("GC", 2), ("GC", -1), ("GC", 1.5),
            ("GC", True), ("G&C", 2), ("GX&C", None),
        )
        for sequence, cutpoint in cases:
            with self.subTest(sequence=sequence, cutpoint=cutpoint), self.assertRaises(ValueError):
                self.fold(sequence, cutpoint=cutpoint)

    def test_bonus_matrix_uses_compact_coordinates_and_rejects_bad_values(self):
        malformed = [np.zeros((7, 7)), np.zeros((5, 5)), np.full((6, 6), np.nan), np.full((6, 6), np.inf)]
        asymmetric = np.zeros((6, 6))
        asymmetric[0, 5] = 1.0
        malformed.append(asymmetric)
        for matrix in malformed:
            with self.subTest(shape=matrix.shape), self.assertRaises(ValueError):
                self.fold("GGG&CCC", bonus_matrix=matrix, alpha=0.5)

    def test_single_chain_reference_remains_unchanged(self):
        results = self.fold(SINGLE_SEQUENCE, allow_pseudoknot=True, beam_size=20, max_phase1=3)
        self.assertEqual(results[0]["structure"], SINGLE_STRUCTURE)
        self.assertAlmostEqual(results[0]["energy"], -8.0204, places=4)
        self.assertNotIn("&", results[0]["structure"])


class CofoldBonusTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        _load_modules()
        cls.module = importlib.import_module("Utils.cofold_bonus")

    def test_interstrand_reads_have_no_intramolecular_gaussian_leakage(self):
        matrix = self.module.build_cofold_bonus_matrix(8, 4, [(2, 4, 5, 7)])
        np.testing.assert_array_equal(matrix[:4, :4], np.zeros((4, 4)))
        np.testing.assert_array_equal(matrix[4:, 4:], np.zeros((4, 4)))
        np.testing.assert_array_equal(matrix, matrix.T)
        self.assertGreater(np.max(matrix[:4, 4:]), 0.0)

    def test_intramolecular_reads_have_no_cross_nick_gaussian_leakage(self):
        for arms, start, end in ([(1, 2, 3, 4)], 0, 4), ([(5, 6, 7, 8)], 4, 8):
            with self.subTest(arms=arms):
                matrix = self.module.build_cofold_bonus_matrix(8, 4, arms)
                np.testing.assert_array_equal(matrix[:4, 4:], np.zeros((4, 4)))
                np.testing.assert_array_equal(matrix[4:, :4], np.zeros((4, 4)))
                self.assertGreater(np.max(matrix[start:end, start:end]), 0.0)
                outside = matrix.copy()
                outside[start:end, start:end] = 0.0
                self.assertFalse(np.any(outside))

    def test_duplicate_reads_add_support_before_the_log_transform(self):
        arms = [(2, 3, 6, 7)]
        once = self.module.build_cofold_bonus_matrix(8, 4, arms)
        twice = self.module.build_cofold_bonus_matrix(8, 4, arms * 2)
        # This distinguishes summing support then log1p from summing log bonuses.
        np.testing.assert_allclose(twice, np.log1p(2 * np.expm1(once)), atol=2e-7)
        self.assertAlmostEqual(float(once.max()), math.log(2.0), places=6)
        self.assertAlmostEqual(float(twice.max()), math.log(3.0), places=6)

    def test_individual_read_arms_must_not_span_the_nick(self):
        malformed = [(3, 5, 6, 8), (1, 2, 4, 6), (0, 2, 6, 7), (1, 2, 6, 9), (1.0, 2, 6, 7)]
        for arm in malformed:
            with self.subTest(arm=arm), self.assertRaises(ValueError):
                self.module.build_cofold_bonus_matrix(8, 4, [arm])

    def test_empty_reads_give_a_zero_bonus_in_compact_coordinates(self):
        matrix = self.module.build_cofold_bonus_matrix(8, 4, [])
        self.assertEqual(matrix.shape, (8, 8))
        self.assertFalse(np.any(matrix))


if __name__ == "__main__":
    unittest.main()
