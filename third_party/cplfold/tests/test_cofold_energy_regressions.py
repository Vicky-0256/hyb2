"""Regressions for physical-graph cofold PK scoring and a common baseline."""

import importlib
import json
import math
import sys
import types
import unittest

PK_SEQUENCE = "GGCGCGGCACCGUCCGCGGAACAAACGG"
PK_STRUCTURE = "..(((((..[[[[)))))......]]]]"


def load_energy():
    # Match the existing fast unit suite. Separate verification covers real
    # Numba compilation and equality with the plain-Python execution path.
    if "numba" not in sys.modules:
        shim = types.ModuleType("numba")

        def njit(*args, **kwargs):
            if len(args) == 1 and callable(args[0]) and not kwargs:
                return args[0]
            return lambda function: function

        shim.njit = njit
        sys.modules["numba"] = shim
    return importlib.import_module("Utils.cofold_energy")


def swap_strands(sequence, structure, cutpoint):
    from Utils.hotknots_energy import parse_dot_bracket
    pairs, _ = parse_dot_bracket(structure)
    closes = {"(": ")", "[": "]", "{": "}", "<": ">", "A": "a"}
    n = len(sequence)
    result = ["."] * n
    for pair in pairs:
        first, second = sorted(((pair.left - cutpoint) % n, (pair.right - cutpoint) % n))
        result[first], result[second] = pair.level, closes[pair.level]
    return sequence[cutpoint:] + sequence[:cutpoint], "".join(result), n - cutpoint


class CofoldEnergyGraphRegressionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = load_energy()
        cls.evaluator = cls.module.CofoldEnergy()

    def score(self, sequence, structure, cutpoint, model="DP09"):
        result = self.evaluator.compute_energy(sequence, structure, cutpoint, model)
        self.assertTrue(math.isfinite(result["energy"]))
        self.assertAlmostEqual(result["energy"], sum(result["breakdown"].values()), places=10)
        json.dumps(result, allow_nan=False)
        return result

    def test_exterior_tail_is_not_reclassified_as_pseudoloop_after_swap(self):
        for model in ("DP03", "DP09"):
            with self.subTest(model=model):
                first = self.score("AAUUAAA", "([)]...", 2, model)
                swapped = self.score("UUAAAAA", "([...)]", 5, model)
                self.assertAlmostEqual(first["energy"], swapped["energy"], places=10)
                self.assertEqual(first["breakdown"]["pseudoknot_unpaired"], 0.0)
                self.assertEqual(swapped["breakdown"]["pseudoknot_unpaired"], 0.0)

    def test_real_backbone_gap_is_counted_but_tail_is_not(self):
        for model in ("DP03", "DP09"):
            with self.subTest(model=model):
                gap = self.score("AAAAAUU", "(...[)]", 5, model)
                tail = self.score("AAUUAAA", "([)]...", 2, model)
                self.assertEqual(gap["metadata"]["pseudoloop_unpaired_bases"], 3)
                self.assertEqual(tail["metadata"]["pseudoloop_unpaired_bases"], 0)
                self.assertGreater(gap["breakdown"]["pseudoknot_unpaired"], 0.0)

    def test_nicks_inside_bands_keep_band_count_under_strand_swap(self):
        for cutpoint in (1, 3, 5, 7):
            for model in ("DP03", "DP09"):
                with self.subTest(cutpoint=cutpoint, model=model):
                    args = ("UACGUGCG", "(([[))]]", cutpoint)
                    first = self.score(*args, model)
                    swapped = self.score(*swap_strands(*args), model)
                    self.assertAlmostEqual(first["energy"], swapped["energy"], places=10)
                    self.assertEqual(first["metadata"]["bands_per_pseudoknot"],
                                     swapped["metadata"]["bands_per_pseudoknot"])

    def test_complex_pks_are_invariant_at_every_nick(self):
        from Utils.hotknots_energy import parse_dot_bracket
        structures = (
            "..(((((..[[[[)))))......]]]]",
            "(((......[[[...)))...{{{...]]]......}}}",
            "((...)(.((...)(..[))))...]",
            "(({{{{{..<<<<}}}}}......>>>>...[[...))...]]",
            "(.{{{{{..<<<<}}}}}......>>>>.)",
            "((.(...).[[...))...]]",
            "(.((...[[[..)))....]]]",
        )
        for structure in structures:
            pairs, _ = parse_dot_bracket(structure)
            sequence = ["A"] * len(structure)
            for index, pair in enumerate(pairs):
                sequence[pair.left], sequence[pair.right] = ("GC", "AU", "UG")[index % 3]
            sequence = "".join(sequence)
            for cutpoint in range(1, len(sequence)):
                for model in ("DP03", "DP09"):
                    with self.subTest(structure=structure, cutpoint=cutpoint, model=model):
                        original = self.score(sequence, structure, cutpoint, model)
                        swapped = self.score(*swap_strands(sequence, structure, cutpoint), model)
                        self.assertAlmostEqual(original["energy"], swapped["energy"], places=9)

    def test_independent_ordinary_monomer_always_uses_turner_baseline(self):
        monomers = (
            ("GGGAAACCC", "(((...)))"),
            ("GGGAAACCC" * 2, "(((...)))" * 2),
            ("CCUCGG", "(....)"),
            ("GGAGAAACACC", "((.(...).))"),
            ("G" + "GGGAAACCC" + "A" + "GGGAAACCC" + "C",
             "(" + "(((...)))" + "." + "(((...)))" + ")"),
        )
        for model in ("DP03", "DP09"):
            for second, second_structure in monomers:
                with self.subTest(model=model, monomer=second_structure):
                    sequence = PK_SEQUENCE + second
                    cutpoint = len(PK_SEQUENCE)
                    common = self.score(sequence, PK_STRUCTURE + "." * len(second), cutpoint, model)
                    combined = self.score(sequence, PK_STRUCTURE + second_structure, cutpoint, model)
                    ordinary = self.score(sequence, "." * cutpoint + second_structure, cutpoint, model)
                    self.assertAlmostEqual(combined["energy"] - common["energy"], ordinary["energy"], places=10)
                    self.assertEqual(ordinary["metadata"]["energy_kind"], "thermodynamic")

    def test_independent_pk_monomers_are_additive_in_same_model(self):
        second = "GGGGAAAACCCCAAAA"
        second_structure = "(([[....))]]...."
        for model in ("DP03", "DP09"):
            with self.subTest(model=model):
                first = self.score(PK_SEQUENCE + "A", PK_STRUCTURE + ".", len(PK_SEQUENCE), model)
                other = self.score(second + "A", second_structure + ".", len(second), model)
                combined = self.score(PK_SEQUENCE + second, PK_STRUCTURE + second_structure,
                                      len(PK_SEQUENCE), model)
                self.assertAlmostEqual(combined["energy"], first["energy"] + other["energy"], places=10)
                self.assertEqual(combined["breakdown"]["duplex_init"], 0.0)

    def test_appended_exterior_hairpin_keeps_ordinary_energy(self):
        for model in ("DP03", "DP09"):
            for hairpin, folded in (("CCUCGG", "(....)"), ("GGGAAACCC" * 2, "(((...)))" * 2)):
                with self.subTest(model=model, folded=folded):
                    sequence = "AA" + "UUAAAAA" + hairpin
                    prefix = "([)]" + "." * 5
                    unpaired = self.score(sequence, prefix + "." * len(hairpin), 2, model)
                    added = self.score(sequence, prefix + folded, 2, model)
                    ordinary = self.score(sequence, "." * 9 + folded, 2, model)
                    self.assertAlmostEqual(added["energy"] - unpaired["energy"], ordinary["energy"], places=10)

    def test_metadata_identifies_changed_experimental_model(self):
        for model in ("DP03", "DP09"):
            connected = self.score("GGGGCCCC", "(([[))]]", 4, model)
            disconnected = self.score(PK_SEQUENCE + "A", PK_STRUCTURE + ".", len(PK_SEQUENCE), model)
            for result in (connected, disconnected):
                self.assertEqual(result["metadata"]["energy_kind"], "heuristic")
                self.assertEqual(result["metadata"]["energy_model_version"], "strand-graph-v2")
                self.assertEqual(result["metadata"]["local_energy_baseline"], "Turner-LinearCoFold")
                self.assertEqual(result["metadata"]["requested_model"], model)
            self.assertEqual(connected["breakdown"]["duplex_init"], 4.1)
            self.assertEqual(disconnected["breakdown"]["duplex_init"], 0.0)

    def test_original_single_strand_hotknots_is_unchanged(self):
        from Utils.hotknots_energy import HotKnotsEnergy
        original = HotKnotsEnergy().compute_energy(PK_SEQUENCE, PK_STRUCTURE, "DP09")
        self.assertAlmostEqual(original["energy"], -8.0204, places=4)


if __name__ == "__main__":
    unittest.main()
