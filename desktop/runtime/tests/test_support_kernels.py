"""The read-support kernels of the shared CPLfold module
(web/cplfold/cplfold_web.py SUPPORT_KERNELS) and their desktop parameter."""
import math
import tempfile
import unittest
from collections import Counter
from pathlib import Path

from desktop.runtime import structure

ROOT = Path(__file__).resolve().parents[3]
SEQUENCE = "GGGAAACCCAGCUUCGGCUGGGAAACCCAAAGGCUAGCUAGCCUUUUAGCGCGAAAGCGCU"
# a doubled row, a row whose arms overlap, and one more
ARMS = [(1, 9, 25, 33), (1, 9, 25, 33), (10, 20, 15, 24), (34, 44, 50, 60)]


def _payload(**extra):
    payload = {"sequence": SEQUENCE, "evidenceMode": "hyb-blocks", "maxSequenceLength": 75, "beamSize": 20,
               "allowPseudoknot": False,
               "evidenceArms": [{"oneStart": a, "oneEnd": b, "twoStart": c, "twoEnd": d} for a, b, c, d in ARMS]}
    payload.update(extra)
    return payload


def _has_vienna():
    try:
        import RNA  # noqa: F401
        return True
    except ImportError:
        return False


class SupportKernelTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        try:
            cls.web = structure.load_cplfold_web(ROOT)
        except ImportError as error:  # pragma: no cover
            raise unittest.SkipTest("cplfold_web dependencies unavailable: %s" % error)

    def test_kernel_names(self):
        self.assertEqual(self.web.SUPPORT_KERNELS, structure.SUPPORT_KERNELS)
        self.assertEqual(self.web.support_kernel(None), "gaussian")
        with self.assertRaises(ValueError):
            self.web.support_kernel("box")

    def test_block_counts_every_pair_between_the_arms(self):
        import numpy as np
        bonus, summary = self.web.build_block_bonus_matrix(len(SEQUENCE), ARMS)
        self.assertTrue(np.array_equal(bonus, bonus.T))
        self.assertAlmostEqual(float(bonus[0, 24]), math.log1p(2), places=6)  # the doubled row
        self.assertEqual(float(bonus[0, 40]), 0.0)
        # arms 10-20 and 15-24 overlap: a cell inside both gets the row in both orientations
        self.assertAlmostEqual(float(bonus[15, 16]), math.log1p(2), places=6)
        self.assertEqual((summary["inputRecords"], summary["uniqueBlocks"]), (4, 3))

    def test_block_normalized_divides_by_the_arm_lengths(self):
        import numpy as np
        plain, _ = self.web.build_block_bonus_matrix(len(SEQUENCE), ARMS)
        bonus, summary = self.web.build_block_bonus_matrix(len(SEQUENCE), ARMS, normalized=True)
        self.assertAlmostEqual(float(bonus[0, 24]), math.log1p(2 / 9), places=6)
        self.assertTrue(np.array_equal(bonus > 0, plain > 0))
        self.assertEqual(summary["supportKernel"], "block-normalized")

    def test_block_export_matches_the_dense_matrix(self):
        for normalized in (False, True):
            bonus, _ = self.web.build_block_bonus_matrix(len(SEQUENCE), ARMS, normalized=normalized)
            dense = {(e["one"], e["two"]): e["value"] for e in self.web.bonus_matrix_listing(bonus)["bonusEntries"]}
            export = {(e["one"], e["two"]): e["value"]
                      for e in self.web._block_bonus_export(len(SEQUENCE), ARMS, normalized)["bonusEntries"]}
            self.assertEqual(dense.keys(), export.keys())
            for key, value in dense.items():
                self.assertAlmostEqual(export[key], value, places=6)

    def test_rnacofold_pairs_from_the_host(self):
        pairs = self.web._normalise_evidence_pairs([{"one": 3, "two": 30, "count": 2}, [30, 3, 1], [5, 28, 4]],
                                                   len(SEQUENCE))
        self.assertEqual(pairs, Counter({(3, 30): 3, (5, 28): 4}))
        for bad in ([[3, 3, 1]], [[0, 5, 1]], [[3, 5, 0]], "x"):
            with self.assertRaises(ValueError):
                self.web._normalise_evidence_pairs(bad, len(SEQUENCE))
        result = self.web.fold(_payload(supportKernel="rnacofold", evidencePairs=[[3, 30, 2], [5, 28, 4]]))
        self.assertEqual(sorted((e["one"], e["two"]) for e in result["evidence"]["bonusEntries"]), [(3, 30), (5, 28)])
        self.assertAlmostEqual(result["evidence"]["maximumBonus"], math.log1p(4), places=6)
        self.assertEqual(result["evidence"]["source"], "hyb-block-intervals")

    def test_fold_and_export_record_the_kernel(self):
        for kernel in ("gaussian", "block", "block-normalized"):
            result = self.web.fold(_payload(supportKernel=kernel))
            self.assertEqual(result["parameters"]["supportKernel"], kernel)
            self.assertEqual(result["evidence"]["supportKernel"], kernel)
            exported = self.web.bonus_matrix(_payload(supportKernel=kernel))
            self.assertEqual(exported["evidence"]["supportKernel"], kernel)
            self.assertTrue(exported["evidence"]["bonusEntries"])

    def test_gaussian_is_the_default(self):
        default = self.web.fold(_payload())
        explicit = self.web.fold(_payload(supportKernel="gaussian"))
        self.assertEqual(default["evidence"]["bonusEntries"], explicit["evidence"]["bonusEntries"])
        self.assertEqual(default["evidence"]["transform"],
                         "IRIS-style Gaussian arm blocks; symmetric outer product; threshold 1e-6; log1p")

    def test_two_strands_keep_their_arms(self):
        payload = _payload(sequence=SEQUENCE[:30], sequenceTwo=SEQUENCE[30:], alpha=0.5,
                           evidenceArms=[{"oneStart": 1, "oneEnd": 9, "twoStart": 35, "twoEnd": 44}])
        for kernel in ("block", "block-normalized"):
            result = self.web.fold(dict(payload, supportKernel=kernel))
            self.assertEqual((result["engine"], result["cutpoint"]), ("CPLcofold", 30))
            self.assertTrue(result["evidence"]["strandBounded"])
        with self.assertRaises(ValueError):
            self.web.fold(dict(payload, supportKernel="block",
                               evidenceArms=[{"oneStart": 25, "oneEnd": 35, "twoStart": 40, "twoEnd": 44}]))

    def test_rnacofold_without_evidence_needs_no_vienna(self):
        result = self.web.fold(_payload(evidenceMode="none", evidenceArms=[], supportKernel="rnacofold"))
        self.assertEqual(result["evidence"]["supportKernel"], "rnacofold")

    @unittest.skipUnless(_has_vienna(), "ViennaRNA's Python module is not installed")
    def test_rnacofold_matches_the_comrades_evidence(self):
        from desktop.runtime import comrades
        counts, summary = self.web.rnacofold_pair_counts(SEQUENCE, ARMS)
        mapped = []
        for a, b, c, d in ARMS:
            job = {"one": SEQUENCE[a - 1:b], "two": SEQUENCE[c - 1:d], "oneStart": a, "twoStart": c}
            mapped += comrades.map_cofold_pairs(job, comrades.cofold(job["one"], job["two"], 37.0, 3))
        reference = Counter()
        for entry in comrades.aggregate_evidence(mapped):
            reference[(min(entry["one"], entry["two"]), max(entry["one"], entry["two"]))] += entry["count"]
        self.assertEqual(counts, reference)
        self.assertEqual(summary["cofoldedBlocks"], 3)

    def test_duplex_saturates_the_host_counts(self):
        # q = mean of the round(61 / 3) = 20 best counts; only three pairs, so q = (2 + 4 + 10) / 3
        pairs = [[3, 30, 2], [5, 28, 4], [6, 27, 10]]
        q = 16 / 3
        result = self.web.fold(_payload(supportKernel="duplex", evidencePairs=pairs))
        evidence = result["evidence"]
        self.assertEqual(evidence["supportKernel"], "duplex")
        self.assertAlmostEqual(evidence["saturatingQ"], q, places=6)
        values = {(e["one"], e["two"]): e["value"] for e in evidence["bonusEntries"]}
        for i, j, c in pairs:
            self.assertAlmostEqual(values[(i, j)], 3.6 * c / (c + q), places=5)
        exported = self.web.bonus_matrix(_payload(supportKernel="duplex", evidencePairs=pairs))
        for entry in exported["evidence"]["bonusEntries"]:
            self.assertAlmostEqual(entry["value"], values[(entry["one"], entry["two"])], places=5)

    def test_duplex_takes_a_given_saturating_q(self):
        """``saturatingQ`` fixes what alpha means, so a region can be folded on
        a longer fold's scale. Derived from the fold itself it changes with the
        folded length, and the same alpha is then a different bonus."""
        pairs = [[3, 30, 2], [5, 28, 4], [6, 27, 10]]
        own = 16 / 3
        for request in (self.web.fold, self.web.bonus_matrix):
            evidence = request(_payload(supportKernel="duplex", evidencePairs=pairs,
                                        saturatingQ=1233.0))["evidence"]
            self.assertAlmostEqual(evidence["saturatingQ"], 1233.0, places=6)
            values = {(e["one"], e["two"]): e["value"] for e in evidence["bonusEntries"]}
            for i, j, c in pairs:
                self.assertAlmostEqual(values[(i, j)], 3.6 * c / (c + 1233.0), places=5)
                self.assertNotAlmostEqual(values[(i, j)], 3.6 * c / (c + own), places=5)

    def test_a_bad_saturating_q_is_refused(self):
        pairs = [[3, 30, 2]]
        for bad in (0, -1, "many", float("inf"), float("nan")):
            with self.assertRaises(ValueError) as caught:
                self.web.fold(_payload(supportKernel="duplex", evidencePairs=pairs, saturatingQ=bad))
            self.assertIn("saturatingQ", str(caught.exception))

    def test_duplex_on_two_strands(self):
        payload = _payload(sequence=SEQUENCE[:30], sequenceTwo=SEQUENCE[30:], alpha=0.5, supportKernel="duplex",
                           evidenceArms=[{"oneStart": 1, "oneEnd": 9, "twoStart": 35, "twoEnd": 44}],
                           evidencePairs=[[3, 40, 5]])
        result = self.web.fold(payload)
        self.assertEqual((result["engine"], result["cutpoint"]), ("CPLcofold", 30))
        self.assertAlmostEqual(result["evidence"]["maximumBonus"], 1.8, places=5)   # C = q: s / 2

    @unittest.skipUnless(_has_vienna(), "ViennaRNA's Python module is not installed")
    def test_duplex_counts_intermolecular_pairs(self):
        counts, summary = self.web.duplex_pair_counts(SEQUENCE, ARMS, workers=1)
        self.assertEqual(summary["hybridisedBlocks"], 3)  # the three distinct blocks
        for (i, j), n in counts.items():
            self.assertLess(i, j)
            in_block = [(a, b, c, d) for a, b, c, d in ARMS
                        if (a <= i <= b and c <= j <= d) or (c <= i <= d and a <= j <= b)]
            self.assertTrue(in_block)
            self.assertLessEqual(n, len(ARMS))

    @unittest.skipUnless(_has_vienna(), "ViennaRNA's Python module is not installed")
    def test_duplex_parallel_matches_serial(self):
        import random
        rng = random.Random(1)
        arms = []
        for _ in range(60):
            a = rng.randint(1, 20)
            c = rng.randint(30, 45)
            arms.append((a, a + rng.randint(6, 12), c, c + rng.randint(6, 14)))
        saved = (self.web.DUPLEX_CHUNK, self.web.DUPLEX_PARALLEL_MIN)
        self.web.DUPLEX_CHUNK, self.web.DUPLEX_PARALLEL_MIN = 7, 1          # several chunks, forked
        try:
            serial, _ = self.web.duplex_pair_counts(SEQUENCE, arms, workers=1)
            calls = []
            parallel, summary = self.web.duplex_pair_counts(SEQUENCE, arms, workers=4,
                                                            progress=lambda d, t: calls.append((d, t)))
        finally:
            self.web.DUPLEX_CHUNK, self.web.DUPLEX_PARALLEL_MIN = saved
        self.assertEqual(serial, parallel)
        self.assertIn(summary["duplexWorkers"], (1, 4))  # 1 where fork is unavailable
        self.assertEqual(calls[-1][0], calls[-1][1])

    def test_duplex_cofold_with_arm_counts(self):
        # desktop CPLcofold: blocks with row counts (evidenceArmCounts) and the host's counts
        payload = {"sequence": SEQUENCE[:30], "sequenceTwo": SEQUENCE[30:], "evidenceMode": "hyb-blocks",
                   "evidenceArmCounts": [[1, 9, 35, 44, 3]], "evidencePairs": [[3, 40, 5]],
                   "supportKernel": "duplex", "alpha": 0.5, "beamSize": 20, "allowPseudoknot": False,
                   "maxSequenceLength": 75}
        result = self.web.fold(payload)
        self.assertEqual(result["engine"], "CPLcofold")
        self.assertEqual(result["evidence"]["inputRecords"], 3)
        self.assertAlmostEqual(result["evidence"]["maximumBonus"], 1.8, places=5)

    @unittest.skipUnless(_has_vienna(), "ViennaRNA's Python module is not installed")
    def test_rnacofold_reports_progress(self):
        calls = []
        self.web.build_support_bonus_matrix(len(SEQUENCE), ARMS, "rnacofold", sequence=SEQUENCE,
                                            progress=lambda done, total: calls.append((done, total)))
        self.assertEqual(calls[-1], (3, 3))  # the last block is always reported


class DesktopParameterTests(unittest.TestCase):
    def test_support_kernel_parameter(self):
        self.assertEqual(structure._parameters({}, "genome")["support_kernel"], "gaussian")
        for kernel in structure.SUPPORT_KERNELS:
            self.assertEqual(structure._parameters({"supportKernel": kernel}, "genome")["support_kernel"], kernel)
        with self.assertRaises(ValueError):
            structure._parameters({"support_kernel": "box"}, "genome")

    def test_tasks_saved_before_rnaduplex(self):
        old_kernel, old_path = "hybridmin", "hybrid_min"  # legacy-name: kernel and path of tasks before RNAduplex
        params = structure._parameters({"support_kernel": old_kernel, old_path: "/nonexistent/x", "beam": 20}, "genome")
        self.assertEqual(params["support_kernel"], "duplex")
        self.assertNotIn(old_path, params)

    @unittest.skipUnless(_has_vienna(), "ViennaRNA's Python module is not installed")
    def test_fold_one_runs_the_duplex_kernel(self):
        try:
            module = structure.load_cplfold_web(ROOT)
        except ImportError as error:  # pragma: no cover
            raise unittest.SkipTest("cplfold_web dependencies unavailable: %s" % error)
        params = structure._parameters({"support_kernel": "duplex", "beam": 20}, "genome")
        record = structure.fold_one(module, SEQUENCE, Counter(ARMS), params, None, "test", duplex_workers=1)
        self.assertEqual(record["evidence"]["supportKernel"], "duplex")

    def test_bonus_header_names_the_kernel(self):
        gaussian = structure._bonus_header(10, "r")
        self.assertNotIn("support_kernel", gaussian)
        self.assertIn(f"# transform={structure.BONUS_TRANSFORM}", gaussian)
        block = structure._bonus_header(10, "r", transform="Full arm blocks", kernel="block")
        self.assertIn("# support_kernel=block", block)
        self.assertIn("# transform=Full arm blocks", block)

    def test_fold_one_uses_the_kernel(self):
        try:
            module = structure.load_cplfold_web(ROOT)
        except ImportError as error:  # pragma: no cover
            raise unittest.SkipTest("cplfold_web dependencies unavailable: %s" % error)
        params = structure._parameters({"support_kernel": "block", "beam": 20}, "genome")
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "bonus.tsv"
            record = structure.fold_one(module, SEQUENCE, Counter(ARMS), params, str(path), "test")
            self.assertEqual(record["evidence"]["supportKernel"], "block")
            text = path.read_text()
            self.assertIn("# support_kernel=block", text)
            # float32 matrix: log1p(2) to six decimals
            self.assertIn("\n1\t25\t%.6f" % math.log1p(2), text)


if __name__ == "__main__":
    unittest.main()
