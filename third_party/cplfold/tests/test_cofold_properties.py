"""Seeded public-API and pair-graph properties on small artificial RNAs.

Run this file alone with NUMBA_DISABLE_JIT=1 for ordinary Python execution,
or without that variable for real Numba compilation. This module installs no
Numba shim; an existing suite-wide shim is left intact. No reference binary,
network, long RNA, or benchmark is needed. The 15 methods exercise 92 fixed
case/parameter combinations, including explicitly required nonempty PKs.
"""

import importlib
import itertools
import json
import math
from pathlib import Path
import random
import sys
import unittest

import numpy as np


SEED = 20261027
COMPLEMENT = str.maketrans("ACGU", "UGCA")
CANONICAL = {"AU", "UA", "CG", "GC", "GU", "UG"}
CLOSING = {"(": ")", "[": "]", "{": "}", "<": ">"}


def _reverse_complement(sequence):
    return sequence.translate(COMPLEMENT)[::-1]


def _generated_cases():
    rng = random.Random(SEED)
    cases = []
    for first_length, second_length in ((1, 1), (1, 7), (8, 1), (3, 4),
                                         (4, 3), (4, 4), (5, 7), (7, 5)):
        first = "".join(rng.choice("ACGU") for _ in range(first_length))
        second = "".join(rng.choice("ACGU") for _ in range(second_length))
        width = min(first_length, second_length)
        if width >= 3:
            # Include real duplexes as well as the one-base boundary cases.
            core = "".join(rng.choice("GC") for _ in range(width))
            first = first[:-width] + core
            second = _reverse_complement(core) + second[width:]
        cases.append((first, second))
    return tuple(cases)


CASES = _generated_cases()


def _graph(structure):
    """Independent bracket reader; preserve families for strand permutation."""
    stacks = {opener: [] for opener in CLOSING}
    openers = {closer: opener for opener, closer in CLOSING.items()}
    pairs = []
    for position, symbol in enumerate(structure.replace("&", "")):
        if symbol in stacks:
            stacks[symbol].append(position)
        elif symbol in openers:
            opener = openers[symbol]
            if not stacks[opener]:
                raise AssertionError("Unbalanced closing bracket")
            pairs.append((stacks[opener].pop(), position, opener))
        elif symbol != ".":
            raise AssertionError("Unsupported structure symbol")
    if any(stacks.values()):
        raise AssertionError("Unbalanced opening bracket")
    return tuple(sorted(pairs))


def _pairs(structure):
    return tuple((i, j) for i, j, _ in _graph(structure))


def _structure(length, graph):
    result = ["."] * length
    for i, j, opener in graph:
        if result[i] != "." or result[j] != ".":
            raise AssertionError("Generated graph reuses a nucleotide")
        result[i], result[j] = opener, CLOSING[opener]
    return "".join(result)


def _matrix(sequence, cutpoint, seed):
    rng = random.Random(seed)
    result = np.zeros((len(sequence), len(sequence)), dtype=np.float64)
    for i in range(len(sequence)):
        for j in range(i + 1, len(sequence)):
            if sequence[i] + sequence[j] in CANONICAL and (i < cutpoint <= j or j - i >= 4):
                result[i, j] = result[j, i] = rng.randrange(-4, 21) / 8.0
    return result


def _target_matrix(length, weighted_pairs):
    result = np.zeros((length, length), dtype=np.float64)
    for i, j, weight in weighted_pairs:
        result[i, j] = result[j, i] = weight
    return result


def _without(results, *fields):
    return [{key: value for key, value in row.items() if key not in fields}
            for row in results]


class CofoldPropertyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        directory = str(Path(__file__).resolve().parents[1])
        if directory not in sys.path:
            sys.path.insert(0, directory)
        # Import real Numba in an independent process, respecting DISABLE_JIT.
        importlib.import_module("numba")
        cls.api = importlib.import_module("CPLfold")
        cls.parser_module = importlib.import_module("CPLcofold_parser")
        cls.energy = importlib.import_module("Utils.cofold_energy").CofoldEnergy()

    def fold(self, first, second, **kwargs):
        options = dict(verbose=False, beam_size=0, energy_delta=6.0,
                       max_phase1=3, max_phase2=2, allow_pseudoknot=False)
        options.update(kwargs)
        return self.api.cofold(first, second, **options)

    def parser(self, cutpoint, alpha=0.0, matrix=None, beam=0):
        parser = self.parser_module.BeamCKYCofoldParser(
            cutpoint=cutpoint, beam_size=beam, use_constraints=True)
        parser.set_alpha(alpha)
        if matrix is not None:
            parser.set_bonus_matrix(matrix, len(matrix))
        return parser

    def assert_results(self, first, second, results, matrix=None):
        sequence, cutpoint = first + second, len(first)
        self.assertTrue(results)
        json.dumps(results, allow_nan=False)
        keys, order = [], []
        for item in results:
            structure = item["structure"]
            for field in ("structure", "phase1_struct", "phase2_struct"):
                if item[field] is not None:
                    self.assertEqual(item[field].count("&"), 1)
                    self.assertEqual(item[field].index("&"), cutpoint)
                    self.assertEqual(len(item[field]) - 1, len(sequence))
            self.assertEqual(item["cutpoint"], cutpoint)
            self.assertEqual(item["strand_lengths"], [cutpoint, len(second)])
            pairs = _pairs(structure)
            endpoints = [position for pair in pairs for position in pair]
            self.assertEqual(len(endpoints), len(set(endpoints)))
            for i, j in pairs:
                self.assertTrue(0 <= i < j < len(sequence))
                self.assertIn(sequence[i] + sequence[j], CANONICAL)
                if (i < cutpoint) == (j < cutpoint):
                    self.assertGreaterEqual(j - i, 4)
            interstrand = sum(i < cutpoint <= j for i, j in pairs)
            self.assertEqual(item["intermolecular_pairs"], interstrand)
            self.assertEqual(item["energy_breakdown"]["duplex_init"], 4.1 if interstrand else 0.0)
            self.assertAlmostEqual(sum(item["energy_breakdown"].values()), item["energy"], places=8)
            model = item["energy_metadata"]["requested_model"]
            physical = self.energy.compute_energy(sequence, structure, cutpoint, model)["energy"]
            self.assertAlmostEqual(item["energy"], physical, places=8)
            first_energy = self.energy.compute_energy(sequence, item["phase1_struct"], cutpoint)["energy"]
            self.assertAlmostEqual(item["phase1_energy"], first_energy, places=8)
            for field in ("energy", "effective_energy", "phase1_energy", "score", "read_support", "read_bonus"):
                self.assertTrue(math.isfinite(item[field]), field)
            support = math.fsum(float(matrix[i, j]) for i, j in pairs) if matrix is not None else 0.0
            self.assertAlmostEqual(item["read_support"], support, places=8)
            self.assertAlmostEqual(item["read_bonus"], item["alpha"] * support, places=8)
            first_pairs = _pairs(item["phase1_struct"])
            expected_score = first_energy - item["alpha"] * support
            expected_effective = item["energy"]
            if item["type"] == "pseudoknot":
                second_pairs = _pairs(item["phase2_struct"])
                self.assertEqual(set(pairs), set(first_pairs) | set(second_pairs))
                self.assertTrue(any(i < k < j < l or k < i < l < j
                                    for i, j in first_pairs for k, l in second_pairs))
                self.assertEqual(set(p for pair in first_pairs for p in pair) &
                                 set(p for pair in second_pairs for p in pair), set())
                first_bound = any(i < cutpoint <= j for i, j in first_pairs)
                second_bound = any(i < cutpoint <= j for i, j in second_pairs)
                self.assertEqual(item["phase2_association_cost"], 0.0 if first_bound else 4.1)
                second_energy = self.energy.compute_energy(sequence, item["phase2_struct"], cutpoint)["energy"]
                expected_score += second_energy - (4.1 if first_bound and second_bound else 0.0)
                expected_effective += item["beta"] * first_energy
                self.assertEqual(item["energy_metadata"]["energy_kind"], "heuristic")
            else:
                self.assertEqual(pairs, first_pairs)
                self.assertIsNone(item["phase2_struct"])
                self.assertEqual(item["energy_metadata"]["energy_kind"], "thermodynamic")
            self.assertAlmostEqual(item["score"], expected_score, places=8)
            self.assertAlmostEqual(item["effective_energy"], expected_effective, places=8)
            keys.append(pairs)
            order.append((item["effective_energy"], item["score"], structure))
        self.assertEqual(len(keys), len(set(keys)))
        self.assertEqual(order, sorted(order))

    def test_three_public_input_forms_are_equivalent(self):
        for index, (first, second) in enumerate(CASES):
            with self.subTest(case=index, lengths=(len(first), len(second))):
                matrix = _matrix(first + second, len(first), SEED + index)
                options = dict(verbose=False, beam_size=3 if index % 2 else 0,
                               energy_delta=6.0, max_phase1=3, max_phase2=2,
                               allow_pseudoknot=index >= 6, alpha=0.375,
                               bonus_matrix=matrix)
                separate = self.api.cofold(first, second, **options)
                marked = self.api.two_phase_pseudoknot_fold(first + "&" + second, **options)
                compact = self.api.two_phase_pseudoknot_fold(first + second, cutpoint=len(first), **options)
                self.assertEqual(separate, marked)
                self.assertEqual(separate, compact)
                self.assert_results(first, second, separate, matrix)

    def test_alpha_zero_ignores_seeded_evidence_in_search(self):
        observed_support = False
        for index, (first, second) in enumerate(CASES[2:]):
            with self.subTest(case=index):
                matrix = _matrix(first + second, len(first), SEED + 20 + index)
                options = dict(allow_pseudoknot=index >= 4)
                plain = self.fold(first, second, **options)
                guided = self.fold(first, second, alpha=0.0, bonus_matrix=matrix, **options)
                self.assertEqual(_without(plain, "read_support"), _without(guided, "read_support"))
                self.assert_results(first, second, guided, matrix)
                observed_support |= any(item["read_support"] != 0 for item in guided)
        self.assertTrue(observed_support, "The alpha=0 comparison must include actual supported pairs")

    def test_nonzero_alpha_without_evidence_preserves_candidates(self):
        for index, (first, second) in enumerate(CASES[2:]):
            with self.subTest(case=index):
                options = dict(allow_pseudoknot=index >= 4)
                plain = self.fold(first, second, **options)
                changed = self.fold(first, second, alpha=(0.25, 2.5, 17.0)[index % 3], **options)
                self.assertEqual(_without(plain, "alpha"), _without(changed, "alpha"))
                self.assert_results(first, second, changed)

    def test_zero_matrix_matches_absent_matrix_with_active_alpha(self):
        for first, second in CASES[4:]:
            with self.subTest(first=first, second=second):
                matrix = np.zeros((len(first + second),) * 2)
                plain = self.fold(first, second, alpha=1.25)
                zero = self.fold(first, second, alpha=1.25, bonus_matrix=matrix)
                self.assertEqual(plain, zero)

    def test_beta_has_no_effect_when_pseudoknots_are_disabled(self):
        for index, (first, second) in enumerate(CASES[2:]):
            with self.subTest(case=index):
                matrix = _matrix(first + second, len(first), SEED + 40 + index)
                options = dict(alpha=0.5, bonus_matrix=matrix)
                plain = self.fold(first, second, beta=0.0, **options)
                biased = self.fold(first, second, beta=(0.2, 4.0, 1000.0)[index % 3], **options)
                self.assertEqual(_without(plain, "beta"), _without(biased, "beta"))
                self.assert_results(first, second, biased, matrix)

    def test_beta_preserves_real_pk_graphs_and_changes_only_the_ranking_term(self):
        reordered = 0
        for first_band, second_band in (("GC", "CG"), ("CG", "GC"), ("GCG", "CCG")):
            width = len(first_band)
            first = first_band + second_band
            second = _reverse_complement(first_band) + _reverse_complement(second_band)
            matrix = _target_matrix(len(first + second),
                [(i, len(first) + width - 1 - i, 12.0) for i in range(width)] +
                [(width + i, len(first + second) - 1 - i, 7.0) for i in range(width)])
            options = dict(alpha=1.0, bonus_matrix=matrix, allow_pseudoknot=True,
                           energy_delta=0.0, max_phase1=1, max_phase2=1)
            plain = self.fold(first, second, beta=0.0, **options)
            self.assertTrue(any(item["type"] == "pseudoknot" for item in plain))
            original = {_pairs(item["structure"]): item for item in plain}
            for beta in (0.75, 32.0):
                with self.subTest(first=first, second=second, beta=beta):
                    biased = self.fold(first, second, beta=beta, **options)
                    self.assertEqual(set(original), {_pairs(item["structure"]) for item in biased})
                    for item in biased:
                        self.assertAlmostEqual(item["energy"], original[_pairs(item["structure"])]["energy"], places=8)
                    self.assert_results(first, second, biased, matrix)
                    reordered += [x["structure"] for x in plain] != [x["structure"] for x in biased]
        self.assertGreater(reordered, 0, "Beta probes must actually reorder a nonempty PK candidate set")

    def test_canonical_reads_select_phase_one_pairs(self):
        for bases in itertools.product("AU", repeat=2):
            first = "".join(bases)
            second = first.translate(COMPLEMENT)
            with self.subTest(first=first, second=second):
                matrix = _target_matrix(4, [(0, 2, 12.0)])
                options = dict(bonus_matrix=matrix, max_phase1=1, energy_delta=0.0)
                plain = self.fold(first, second, alpha=0.0, **options)
                guided = self.fold(first, second, alpha=1.0, **options)
                self.assertEqual(_pairs(plain[0]["structure"]), ())
                self.assertEqual(_pairs(guided[0]["structure"]), ((0, 2),))
                self.assert_results(first, second, guided, matrix)

    def test_second_phase_reads_create_crossing_pairs_without_changing_phase_one(self):
        observed_pks = 0
        for bases in itertools.product("AU", repeat=2):
            first = "".join(bases)
            second = first.translate(COMPLEMENT)
            with self.subTest(first=first, second=second):
                strong = _target_matrix(4, [(0, 2, 12.0)])
                both = _target_matrix(4, [(0, 2, 12.0), (1, 3, 7.0)])
                options = dict(alpha=1.0, allow_pseudoknot=True, energy_delta=0.0,
                               max_phase1=1, max_phase2=1)
                first_only = self.fold(first, second, bonus_matrix=strong, **options)
                guided = self.fold(first, second, bonus_matrix=both, **options)
                self.assertFalse(any(item["type"] == "pseudoknot" for item in first_only))
                pks = [item for item in guided if item["type"] == "pseudoknot"]
                self.assertEqual(len(pks), 1)
                self.assertEqual(pks[0]["phase1_struct"], first_only[0]["structure"])
                self.assertEqual(_pairs(pks[0]["phase2_struct"]), ((1, 3),))
                self.assertEqual(_pairs(pks[0]["structure"]), ((0, 2), (1, 3)))
                self.assert_results(first, second, guided, both)
                observed_pks += len(pks)
        self.assertEqual(observed_pks, 4)

    def test_partial_and_forced_constraint_strings_match_partner_arrays(self):
        rng = random.Random(SEED + 60)
        cases = []
        for first, second in CASES[3:7]:
            width = min(len(first), len(second))
            pairs = [(len(first) - width + i, len(first) + width - 1 - i)
                     for i in range(min(2, width))]
            cases.append((first, second, pairs))
        stem = "".join(rng.choice("GC") for _ in range(2))
        hairpin = stem + "".join(rng.choice("ACGU") for _ in range(3)) + _reverse_complement(stem)
        cases.extend(((hairpin, "A", [(0, 6), (1, 5)]),
                      ("U", hairpin, [(1, 7), (2, 6)])))
        for index, (first, second, forced_pairs) in enumerate(cases):
            with self.subTest(case=index):
                n = len(first + second)
                symbols = ["?" if rng.randrange(2) else "." for _ in range(n)]
                partners = np.array([-1 if symbol == "?" else -2 for symbol in symbols], dtype=np.int32)
                for i, j in forced_pairs:
                    symbols[i], symbols[j] = "(", ")"
                    partners[i], partners[j] = j, i
                constraint = "".join(symbols)
                parser = self.parser(len(first), beam=2)
                from_string = parser.parse_subopt(first + second, cons=constraint, energy_delta=4.0, max_structures=4)
                from_array = parser.parse_subopt(first + second, cons=partners, energy_delta=4.0, max_structures=4)
                self.assertEqual(from_string, from_array)
                self.assertTrue(from_string)
                for structure, _ in from_string:
                    actual_pairs = set(_pairs(structure))
                    self.assertTrue(set(forced_pairs) <= actual_pairs)
                    endpoints = {position for pair in actual_pairs for position in pair}
                    self.assertFalse(any(index in endpoints for index, symbol in enumerate(constraint) if symbol == "."))

    def test_suboptimals_are_unique_sorted_and_within_the_search_window(self):
        for index, (first, second) in enumerate(CASES[2:]):
            with self.subTest(case=index):
                sequence = first + second
                matrix = _matrix(sequence, len(first), SEED + 80 + index)
                alpha, delta = 0.375, (0.0, 2.5, 7.0)[index % 3]
                parser = self.parser(len(first), alpha=alpha, matrix=matrix, beam=3 if index % 2 else 0)
                mfe = parser.parse(sequence)
                candidates = parser.parse_subopt(sequence, energy_delta=delta, max_structures=7)
                self.assertTrue(candidates)
                self.assertEqual(len(candidates), len({item[0] for item in candidates}))
                energies = [item[1] for item in candidates]
                self.assertEqual(energies, sorted(energies))
                self.assertAlmostEqual(energies[0], mfe[1], places=8)
                for structure, score in candidates:
                    self.assertLessEqual(score, mfe[1] + delta + 1e-8)
                    physical = self.energy.compute_energy(sequence, structure, len(first))["energy"]
                    support = math.fsum(matrix[i, j] for i, j in _pairs(structure))
                    self.assertAlmostEqual(score, physical - alpha * support, places=8)

    def test_larger_suboptimal_window_keeps_previous_candidates(self):
        for first, second in CASES[3:7]:
            with self.subTest(first=first, second=second):
                parser = self.parser(len(first))
                small = parser.parse_subopt(first + second, energy_delta=0.0, max_structures=8)
                large = parser.parse_subopt(first + second, energy_delta=8.0, max_structures=8)
                self.assertTrue(set(small) <= set(large))

    def test_short_and_asymmetric_boundaries_keep_finite_valid_pair_graphs(self):
        for index, (first, second) in enumerate(CASES):
            with self.subTest(case=index, cutpoint=len(first)):
                matrix = _matrix(first + second, len(first), SEED + 100 + index)
                results = self.fold(first, second, alpha=0.75, beta=0.25,
                                    bonus_matrix=matrix, allow_pseudoknot=True)
                self.assert_results(first, second, results, matrix)

    def test_swapping_strands_preserves_the_same_reindexed_graph_energy(self):
        # Generate nucleotides for legal graphs, rather than asserting that a
        # separate pruned search must choose the same tied optimum after swap.
        templates = ((8, 1, ((1, 7, "("), (2, 6, "("))),
                     (9, 8, ((0, 7, "("), (1, 6, "("))),
                     (8, 3, ((0, 7, "("), (1, 6, "("), (2, 5, "("))),
                     (9, 6, ((1, 8, "("), (2, 7, "("), (3, 6, "("))),
                     (7, 2, ((0, 3, "("), (1, 5, "["))),
                     (7, 5, ((1, 5, "("), (3, 6, "["))),
                     (10, 9, ((0, 5, "("), (2, 8, "["))),
                     (10, 1, ((1, 6, "("), (3, 9, "["))))
        rng = random.Random(SEED + 120)
        observed_pk = 0
        for n, cutpoint, graph in templates:
            bases = [rng.choice("ACGU") for _ in range(n)]
            for i, j, _ in graph:
                bases[i], bases[j] = rng.choice(sorted(CANONICAL))
            sequence = "".join(bases)
            structure = _structure(n, graph)
            swapped_graph = []
            for i, j, family in graph:
                left, right = sorted(((i - cutpoint) % n, (j - cutpoint) % n))
                swapped_graph.append((left, right, family))
            swapped_sequence = sequence[cutpoint:] + sequence[:cutpoint]
            swapped_structure = _structure(n, swapped_graph)
            self.assertEqual(_pairs(swapped_structure), tuple(sorted((i, j) for i, j, _ in swapped_graph)))
            mapped_back = {tuple(sorted(((i + cutpoint) % n, (j + cutpoint) % n)))
                           for i, j in _pairs(swapped_structure)}
            self.assertEqual(mapped_back, set(_pairs(structure)))
            for model in ("DP03", "DP09"):
                with self.subTest(n=n, cutpoint=cutpoint, model=model):
                    original = self.energy.compute_energy(sequence, structure, cutpoint, model)
                    swapped = self.energy.compute_energy(swapped_sequence, swapped_structure, n - cutpoint, model)
                    self.assertAlmostEqual(original["energy"], swapped["energy"], places=8)
                    self.assertEqual(original["metadata"]["interstrand_pairs"], swapped["metadata"]["interstrand_pairs"])
                    self.assertEqual(original["metadata"]["pseudoknot_components"], swapped["metadata"]["pseudoknot_components"])
                    observed_pk += original["metadata"]["energy_kind"] == "heuristic"
                    json.dumps((original, swapped), allow_nan=False)
        self.assertEqual(observed_pk, 8)

    def test_rescaling_reads_and_inverse_alpha_preserves_the_search_objective(self):
        for index, (first, second) in enumerate(CASES[4:]):
            with self.subTest(case=index):
                matrix = _matrix(first + second, len(first), SEED + 140 + index)
                options = dict(allow_pseudoknot=index >= 2)
                original = self.fold(first, second, alpha=0.5, bonus_matrix=matrix, **options)
                rescaled = self.fold(first, second, alpha=0.25, bonus_matrix=matrix * 2.0, **options)
                self.assertEqual(_without(original, "alpha", "read_support"),
                                 _without(rescaled, "alpha", "read_support"))
                self.assert_results(first, second, rescaled, matrix * 2.0)

    def test_forced_unpaired_bases_override_favorable_cross_strand_reads(self):
        for first, second in CASES[4:]:
            with self.subTest(first=first, second=second):
                sequence, cutpoint = first + second, len(first)
                matrix = _target_matrix(len(sequence), [(i, j, 20.0)
                    for i in range(cutpoint) for j in range(cutpoint, len(sequence))
                    if sequence[i] + sequence[j] in CANONICAL])
                parser = self.parser(cutpoint, alpha=1.0, matrix=matrix)
                free = parser.parse(sequence)
                blocked = parser.parse(sequence, cons="." * cutpoint + "?" * len(second))
                self.assertTrue(any(i < cutpoint <= j for i, j in _pairs(free[0])))
                self.assertFalse(any(i < cutpoint for i, _ in _pairs(blocked[0])))


if __name__ == "__main__":
    unittest.main()
