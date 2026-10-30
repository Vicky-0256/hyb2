"""Public cofold structures use '&' for display, never for base coordinates."""

import importlib
import sys
import types
import unittest


def _load_modules():
    if "numba" not in sys.modules:
        shim = types.ModuleType("numba")

        def njit(*args, **kwargs):
            if len(args) == 1 and callable(args[0]) and not kwargs:
                return args[0]
            return lambda function: function

        shim.njit = njit
        sys.modules["numba"] = shim
    return importlib.import_module("CPLfold"), importlib.import_module("CPLcofold_parser")


def _marked(structure, cutpoint=5):
    return structure[:cutpoint] + "&" + structure[cutpoint:]


class CofoldStructureHelperTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.api, cls.parser_module = _load_modules()

    def test_public_nested_result_roundtrips_to_compact_parser_constraints(self):
        result = self.api.cofold("GGG", "CCC", allow_pseudoknot=False,
                                 max_phase1=1, verbose=False)[0]
        self.assertEqual(result["structure"], "(((&)))")
        self.assertEqual(self.api.get_pairs_from_structure(result["structure"]),
                         [(2, 3), (1, 4), (0, 5)])
        constraint = self.api.structure_to_constraint(result["structure"])
        self.assertEqual(constraint, "......")
        parser = self.parser_module.BeamCKYCofoldParser(cutpoint=3, use_constraints=True)
        self.assertEqual(parser.parse("GGGCCC", cons=constraint)[:2], ("......", 0.0))

    def test_public_pseudoknot_result_masks_every_bracket_family(self):
        results = self.api.cofold("GGGG", "CCCC", max_phase1=12, max_phase2=3,
                                  energy_delta=12, verbose=False)
        result = next(item for item in results if item["type"] == "pseudoknot")
        paired = {base for pair in self.api.get_pairs_from_structure(result["structure"])
                  for base in pair}
        constraint = self.api.structure_to_constraint(result["structure"])
        self.assertEqual(len(constraint), 8)
        self.assertEqual({i for i, symbol in enumerate(constraint) if symbol == '.'}, paired)
        parser = self.parser_module.BeamCKYCofoldParser(cutpoint=4, use_constraints=True)
        folded = parser.parse("GGGGCCCC", cons=constraint)[0]
        self.assertTrue(all(folded[i] == '.' for i in paired))
        self.assertEqual(set(self.api.get_pairs_from_structure(result["structure"])),
                         set(self.api.get_pairs_from_structure(result["phase1_struct"])) |
                         set(self.api.get_pairs_from_structure(result["phase2_struct"])))

    def test_compact_and_marked_merges_preserve_the_same_pair_graph(self):
        first, second = "((....))..", "..((....))"
        expected = "(([[..))]]"
        self.assertEqual(self.api.merge_structures(first, second), expected)
        for a, b in ((_marked(first), _marked(second)),
                     (_marked(first), second), (first, _marked(second))):
            with self.subTest(first=a, second=b):
                merged = self.api.merge_structures(a, b)
                self.assertEqual(merged, _marked(expected))
                self.assertEqual(set(self.api.get_pairs_from_structure(merged)),
                                 {(0, 7), (1, 6), (2, 9), (3, 8)})

    def test_merge_rejects_different_boundaries_and_compact_pair_conflicts(self):
        self.assertIsNone(self.api.merge_structures(_marked("((....))..", 4),
                                                    _marked("..((....))", 5)))
        self.assertIsNone(self.api.merge_structures("(&)..", "(..)"))
        self.assertIsNone(self.api.merge_structures("(&)", "..."))

    def test_duplicate_pairs_remain_valid_when_only_one_input_has_a_boundary(self):
        self.assertEqual(self.api.merge_structures("(&)", "()"), "(&)")
        self.assertEqual(self.api.merge_structures("()", "(&)"), "(&)")

    def test_supported_pseudoknot_alphabets_have_compact_coordinates(self):
        structure = "([{&.)]}"
        self.assertEqual(self.api.get_pairs_from_structure(structure), [(0, 4), (1, 5), (2, 6)])
        self.assertEqual(self.api.structure_to_constraint(structure), "...?...")
        self.assertEqual(self.api.get_pairs_from_structure("([)]"), [(0, 2), (1, 3)])

    def test_bracket_collision_cannot_silently_change_a_merged_pair_graph(self):
        self.assertIsNone(self.api.merge_structures("[[..]]..", "..(....)"))
        self.assertIsNone(self.api.merge_structures("........", "([)]...."))
        self.assertEqual(self.api.merge_structures("{..}..", "..(..)"), "{.[}.]")

    def test_malformed_layouts_are_rejected(self):
        for value in (None, "&..", "..&", ".&.&.", "([)", ".)", "(.x.)", "<..>"):
            with self.subTest(structure=value):
                with self.assertRaises(ValueError):
                    self.api.get_pairs_from_structure(value)
                with self.assertRaises(ValueError):
                    self.api.structure_to_constraint(value)
                self.assertIsNone(self.api.merge_structures(value, "....."))

    def test_valid_single_strand_behavior_and_empty_helpers_are_preserved(self):
        self.assertEqual(self.api.get_pairs_from_structure(".((...))."), [(2, 6), (1, 7)])
        self.assertEqual(self.api.structure_to_constraint(".((...))."), "?..???..?")
        self.assertEqual(self.api.get_pairs_from_structure(""), [])
        self.assertEqual(self.api.structure_to_constraint(""), "")
        self.assertEqual(self.api.merge_structures("", ""), "")


if __name__ == "__main__":
    unittest.main()
