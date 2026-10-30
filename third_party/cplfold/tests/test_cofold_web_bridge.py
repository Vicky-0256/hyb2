"""Real-backend regressions for the browser's compact-coordinate bridge.

Run with NUMBA_DISABLE_JIT=1 for the same algorithm without compilation;
the isolated subprocess also exercises the browser's identity-decorator shim.
"""

import importlib.util
import json
import math
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import patch

import numpy as np


ROOT = Path(__file__).resolve().parents[3]
CORE = ROOT / "third_party" / "cplfold"
BRIDGE = ROOT / "web" / "cplfold" / "cplfold_web.py"
sys.path.insert(0, str(CORE))
spec = importlib.util.spec_from_file_location("_cofold_web_bridge_tests", BRIDGE)
bridge = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = bridge
spec.loader.exec_module(bridge)

from CPLcofold import two_phase_cofold
from CPLfold import get_pairs_from_structure
from Utils.cofold_bonus import build_cofold_bonus_matrix


def arm(a, b, c, d):
    return dict(oneStart=a, oneEnd=b, twoStart=c, twoEnd=d)


def request(**changes):
    payload = dict(sequence="GGG&CCC", beamSize=5, maxPhase1=1,
                   energyDelta=0, alpha=0, beta=0, allowPseudoknot=False)
    payload.update(changes)
    return payload


class CofoldWebBridgeTests(unittest.TestCase):
    def test_single_strand_full_result_preserves_previous_contract(self):
        sequence, structure = "GGGAAACCC", "(((...)))"
        pairs = [dict(left=i, right=10-i, leftBase="G", rightBase="C", type="G–C",
                      layer="primary", bracket="()", evidenceSupport=0.0, bonusSupport=0.0)
                 for i in (1, 2, 3)]
        energy = -1.2  # Turner 2004 (ViennaRNA eval_structure), the pseudoknot-free ranking model.
        candidate = dict(index=0, dotBracket=structure, type="phase1", topology="nested",
                         energy=energy, effectiveEnergy=energy, phase1Energy=energy,
                         score=-1.2, phase1Structure=structure, phase2Structure=None,
                         pairs=pairs, crossingPairs=0, unpaired=3)
        expected = dict(
            algorithm="CPLfold pseudoknot-free secondary-structure prediction",
            model="LinearFold Vienna-mode scoring with Turner 2004 energy ranking",
            engine="CPLfold", engineVersion="24bab52", bridgeVersion="6", allowPseudoknot=False,
            runtime="Pyodide 0.29.4; Python 3.13.2; NumPy 2.2.5; Numba identity fallback",
            maxSequenceLength=75, sequence=sequence, dotBracket=structure, pairs=pairs,
            unpaired=3, energy=energy, effectiveEnergy=energy, phase1Energy=energy, score=-1.2,
            phase1Structure=structure, phase2Structure=None, energyUnit="kcal/mol",
            structureType="phase1", topology="nested", crossingPairs=0,
            constraintMode="none", constraintSource="none", constraintCount=0, constraints=[],
            evidenceMode="none", evidenceSource="none", selectedCandidate=0, candidates=[candidate],
            parameters=dict(beamSize=5, energyDelta=0.0, maxPhase1=1, energyModel="DP09",
                            alpha=0.0, beta=0.0, allowPseudoknot=False, linearFoldMode="Vienna", maxPhase2=1),
            evidence=dict(source="none", inputRecords=0, uniqueBlocks=0, nonzeroBonusCells=0,
                          nonzeroUpperTriangleCells=0, maximumBonus=0.0, bonusEntries=[], evidenceWeight="none"),
            nucleotideSupport=[0.0]*9, maximumNucleotideSupport=0.0,
        )
        self.assertEqual(bridge.fold(request(sequence=sequence)), expected)

    def test_three_input_forms_produce_identical_results(self):
        for a, b in (("GGG", "CCC"), ("A", "U"), ("G", "CCGC"), ("GCGA", "UCGC")):
            with self.subTest(a=a, b=b):
                marked = bridge.fold(request(sequence=a + "&" + b))
                separate = bridge.fold(request(sequence=a, sequenceTwo=b))
                compact = bridge.fold(request(sequence=a + b, cutpoint=len(a)))
                self.assertEqual(marked, separate)
                self.assertEqual(marked, compact)
                self.assertEqual(marked["displaySequence"], a + "&" + b)
                self.assertEqual(marked["strandLengths"], [len(a), len(b)])
        self.assertEqual(bridge.fold(request(sequence=" ggg \n & cCc ")), bridge.fold(request()))
        self.assertEqual(bridge.fold(request(sequence="a", sequenceTwo="t"))["displaySequence"], "A&U")

    def test_compact_pair_indices_and_display_fields_are_consistent(self):
        result = bridge.fold(request(sequence="GGGG&CCCC", maxPhase1=12,
                                     energyDelta=12, allowPseudoknot=True))
        self.assertTrue(any(c["type"] == "pseudoknot" for c in result["candidates"]))
        for candidate in result["candidates"]:
            cp = result["cutpoint"]
            structure = candidate["dotBracket"]
            self.assertEqual(len(structure), len(result["sequence"]))
            self.assertEqual(candidate["dotBracketWithBreak"], structure[:cp] + "&" + structure[cp:])
            graph = {(i + 1, j + 1) for i, j in get_pairs_from_structure(structure)}
            self.assertEqual(graph, {(p["left"], p["right"]) for p in candidate["pairs"]})
            for name in ("phase1Structure", "phase2Structure"):
                phase = candidate[name]
                self.assertEqual(candidate[name + "WithBreak"], None if phase is None else phase[:cp] + "&" + phase[cp:])
            for pair in candidate["pairs"]:
                for side in ("left", "right"):
                    position = pair[side]
                    self.assertEqual(pair[side + "Base"], result["sequence"][position - 1])
                    self.assertEqual(pair[side + "Strand"], "A" if position <= cp else "B")
                    self.assertEqual(pair[side + "StrandPosition"], position if position <= cp else position - cp)
                self.assertEqual(pair["intermolecular"], pair["left"] <= cp < pair["right"])
            self.assertEqual(candidate["intermolecularPairs"], sum(p["intermolecular"] for p in candidate["pairs"]))

    def test_candidates_match_real_cofold_backend_for_both_models(self):
        for model in ("DP03", "DP09"):
            with self.subTest(model=model):
                result = bridge.fold(request(sequence="GGGG&CCCC", maxPhase1=12,
                                             energyDelta=12, allowPseudoknot=True, energyModel=model))
                raw = two_phase_cofold("GGGGCCCC", cutpoint=4, beam_size=5, max_phase1=12,
                                       max_phase2=1, energy_delta=12, alpha=0, beta=0,
                                       energy_model=model, allow_pseudoknot=True, verbose=False)
                self.assertEqual(len(result["candidates"]), len(raw))
                for candidate, original in zip(result["candidates"], raw):
                    self.assertEqual(candidate["dotBracketWithBreak"], original["structure"])
                    self.assertEqual(candidate["energy"], original["energy"])
                    self.assertEqual(candidate["effectiveEnergy"], original["effective_energy"])
                    self.assertEqual(candidate["energyMetadata"], original["energy_metadata"])
                    self.assertEqual(candidate["energyBreakdown"], original["energy_breakdown"])
                    if candidate["type"] == "pseudoknot":
                        self.assertEqual(candidate["energyMetadata"]["energy_kind"], "heuristic")
                self.assertEqual(result["energyMetadata"], result["candidates"][0]["energyMetadata"])

    def test_alpha_and_beta_reach_both_phase_search_and_ranking(self):
        payload = request(sequence="GGGG&CCCC", maxPhase1=12, energyDelta=12,
                          allowPseudoknot=True, evidenceMode="hyb-blocks", evidenceArms=[arm(1, 4, 5, 8)])
        unguided = bridge.fold(payload)
        guided = bridge.fold(dict(payload, alpha=0.6))
        weighted = bridge.fold(dict(payload, alpha=0.6, beta=0.4))
        self.assertNotEqual(unguided["score"], guided["score"])
        by_graph = {c["dotBracket"]: c["energy"] for c in guided["candidates"]}
        self.assertTrue(any(c["type"] == "pseudoknot" for c in weighted["candidates"]))
        for candidate in weighted["candidates"]:
            self.assertEqual(candidate["energy"], by_graph[candidate["dotBracket"]])
            self.assertAlmostEqual(candidate["readBonus"], 0.6 * candidate["readSupport"])
            expected = candidate["energy"]
            if candidate["type"] == "pseudoknot":
                expected += 0.4 * candidate["phase1Energy"]
                self.assertIn("phase2AssociationCost", candidate)
            self.assertAlmostEqual(candidate["effectiveEnergy"], expected)

    def test_omitted_alpha_defaults_to_zero_only_for_cofold(self):
        payload = request(sequence="GGGG&CCCC", evidenceMode="hyb-blocks", evidenceArms=[arm(1, 4, 5, 8)])
        explicit = bridge.fold(payload)
        del payload["alpha"]
        self.assertEqual(bridge.fold(payload), explicit)
        self.assertEqual(bridge.fold(dict(sequence="GGGAAACCC", maxPhase1=1))["parameters"]["alpha"], 0.5)

    def test_read_gaussians_never_cross_individual_strand_boundaries(self):
        for arms in ([(1, 3, 4, 4)], [(5, 6, 7, 8)], [(3, 4, 5, 6)]):
            with self.subTest(arms=arms):
                actual, summary = bridge.build_cofold_hyb_bonus_matrix(8, 4, arms)
                np.testing.assert_array_equal(actual, build_cofold_bonus_matrix(8, 4, arms))
                if arms[0][0] <= 4 < arms[0][2]:
                    self.assertFalse(np.any(actual[:4, :4]))
                    self.assertFalse(np.any(actual[4:, 4:]))
                else:
                    other = slice(4, 8) if arms[0][0] <= 4 else slice(0, 4)
                    self.assertFalse(np.any(actual[other, :]))
                    self.assertFalse(np.any(actual[:, other]))
                self.assertTrue(summary["strandBounded"])

    def test_sparse_export_matches_core_float64_accumulation_and_duplicate_weights(self):
        cases = [(8, 4, [(1, 4, 5, 8)]), (8, 4, [(4, 4, 8, 8)] * 3),
                 (12, 5, [(1, 3, 2, 5), (6, 10, 8, 12), (3, 5, 6, 7)] * 2)]
        for n, cp, arms in cases:
            with self.subTest(n=n, cp=cp, arms=arms):
                dense = build_cofold_bonus_matrix(n, cp, arms)
                payload = dict(sequence="A"*n, cutpoint=cp, evidenceMode="hyb-blocks",
                               evidenceArms=[arm(*positions) for positions in arms])
                exported = bridge.bonus_matrix(payload)
                summary = exported["evidence"]
                expected = {(i + 1, j + 1): float(dense[i, j]) for i in range(n) for j in range(i + 1, n) if dense[i, j]}
                actual = {(e["one"], e["two"]): e["value"] for e in summary["bonusEntries"]}
                self.assertEqual(actual.keys(), expected.keys())
                np.testing.assert_allclose(list(actual.values()), list(expected.values()), rtol=1e-7, atol=1e-10)
                self.assertEqual(summary["nonzeroBonusCells"], np.count_nonzero(dense))
                self.assertEqual(summary["maximumBonus"], float(np.max(dense)))
                self.assertEqual(summary["inputRecords"], len(arms))
                self.assertEqual(summary["uniqueBlocks"], len(set(arms)))

    def test_export_accepts_long_total_length_without_dense_fold_builder(self):
        payload = dict(sequence="G" * 301, sequenceTwo="C" * 302, evidenceMode="hyb-blocks",
                       evidenceArms=[arm(300, 301, 302, 303)])
        with patch.object(bridge, "build_cofold_hyb_bonus_matrix", side_effect=AssertionError("dense allocation")):
            result = bridge.bonus_matrix(payload)
        self.assertEqual(result["strandLengths"], [301, 302])
        self.assertTrue(result["evidence"]["bonusEntries"])
        self.assertTrue(all(e["one"] <= 301 < e["two"] for e in result["evidence"]["bonusEntries"]))
        with self.assertRaisesRegex(ValueError, "5,000"):
            bridge.bonus_matrix(dict(sequence="A" * 2500, sequenceTwo="U" * 2501))

    def test_limits_apply_to_total_nucleotides_not_ampersand_or_individual_strands(self):
        self.assertEqual(len(bridge.fold(request(maxSequenceLength=6))["sequence"]), 6)
        with self.assertRaisesRegex(ValueError, "limited to 5 nt"):
            bridge.fold(request(maxSequenceLength=5))
        with self.assertRaisesRegex(ValueError, "hard safety ceiling"):
            bridge.fold(request(sequence="A"*250, sequenceTwo="U"*251))

    def test_invalid_layout_is_rejected_by_fold_and_export(self):
        invalid = [dict(sequence="&CCC"), dict(sequence="GGG&"), dict(sequence="G&C&C"),
                   dict(sequence="GGG", sequenceTwo=""), dict(sequence="GGG&CCC", sequenceTwo="A"),
                   dict(sequence="GGGCCC", cutpoint=0), dict(sequence="GGGCCC", cutpoint=6),
                   dict(sequence="GGGCCC", cutpoint=True), dict(sequence="GGGCCC", cutpoint="3"),
                   dict(sequence="GGGCCC", cutpoint=3.0), dict(sequence="GGG&CCC", cutpoint=2),
                   dict(sequence="GGG", cofold=True), dict(sequence="GGN&CCC"),
                   dict(sequence="GGG", sequenceTwo=123)]
        for payload in invalid:
            for method in (bridge.fold, bridge.bonus_matrix):
                with self.subTest(payload=payload, method=method.__name__), self.assertRaises(ValueError):
                    method(payload)

    def test_cross_nick_or_invalid_read_arms_are_rejected(self):
        for bad in (arm(3, 5, 6, 8), arm(1, 3, 4, 5), arm(0, 2, 5, 7),
                    arm(1.5, 3, 5, 7), arm(True, 3, 5, 7), arm(1, 3, 5, 9)):
            payload = request(sequence="GGGG&CCCC", evidenceMode="hyb-blocks", evidenceArms=[bad])
            for method in (bridge.fold, bridge.bonus_matrix):
                with self.subTest(arm=bad, method=method.__name__), self.assertRaises(ValueError):
                    method(payload)

    def test_unsupported_cofold_models_remain_available_to_single_strands(self):
        for model in ("CC06", "CC09", "RE"):
            with self.subTest(model=model):
                with self.assertRaisesRegex(ValueError, "only DP03 or DP09"):
                    bridge.fold(request(energyModel=model))
                self.assertEqual(bridge.fold(request(sequence="GGGAAACCC", energyModel=model))["engine"], "CPLfold")

    def test_json_result_has_finite_scores_and_preserves_compact_coordinates(self):
        result = json.loads(bridge.fold_json(json.dumps(request())))
        self.assertEqual(result["engine"], "CPLcofold")
        for key in ("energy", "effectiveEnergy", "phase1Energy", "score", "readSupport", "readBonus"):
            self.assertTrue(math.isfinite(result[key]), key)
        for candidate in result["candidates"]:
            self.assertEqual(len(candidate["dotBracket"]), len(result["sequence"]))
        exported = json.loads(bridge.bonus_matrix_json(json.dumps(request())))
        self.assertEqual(exported["displaySequence"], result["displaySequence"])

    def test_real_backend_works_with_browser_numba_identity_fallback(self):
        code = """
import json, sys
sys.path[:0] = [sys.argv[1], sys.argv[2]]
import cplfold_web
assert not cplfold_web._NUMBA_AVAILABLE
result = cplfold_web.fold({'sequence': 'GGG&CCC', 'maxPhase1': 1, 'energyDelta': 0,
                           'alpha': 0, 'allowPseudoknot': False})
assert result['energy'] == -2.5
assert result['engine'] == 'CPLcofold'
print(json.dumps({'structure': result['dotBracketWithBreak'], 'version': result['bridgeVersion']}))
"""
        environment = dict(os.environ, CPLFOLD_FORCE_PURE_PYTHON="1", PYTHONDONTWRITEBYTECODE="1")
        completed = subprocess.run([sys.executable, "-c", code, str(CORE), str(BRIDGE.parent)],
                                   env=environment, capture_output=True, text=True, check=True, timeout=30)
        self.assertEqual(json.loads(completed.stdout), {"structure": "(((&)))", "version": "6"})


if __name__ == "__main__":
    unittest.main()
