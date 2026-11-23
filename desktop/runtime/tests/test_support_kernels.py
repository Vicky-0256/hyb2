"""The read-support kernels of the shared CPLfold module
(web/cplfold/cplfold_web.py SUPPORT_KERNELS) and their desktop parameter."""
import hashlib
import json
import math
import tempfile
import unittest
from collections import Counter
from decimal import Decimal, localcontext
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

    @unittest.skipUnless(_has_vienna(), "ViennaRNA's Python module is not installed")
    def test_file_evidence_counts_only_valid_hyb_rows(self):
        row = "read_24\tGGGCCC\t.\ttoy\t1\t3\t1\t3\t.\ttoy\t4\t6\t7\t9\t."
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "toy.hyb"
            path.write_text("\n".join(["\ufeff  #" + row, row, row.rsplit("\t", 1)[0],
                                       row.replace("\t7\t9", "\t+7\t9"), row + "\tcount_total=0",
                                       row + "\tcount_total=1000000"]) + "\n", encoding="utf-8")
            blocks = self.web.collect_hyb_arms(path, "toy", 9)["genome"]
        self.assertEqual(blocks, {(1, 3, 7, 9): 2})
        for kernel in ("comrades", "duplex"):
            result = structure.fold_one(self.web, "GGGAAACCC", blocks,
                                        structure._parameters({"support_kernel": kernel, "alpha": 0}, "genome"),
                                        None, "toy", duplex_workers=1)
            self.assertEqual(result["evidence"]["inputRecords"], 2)
            candidate = result["candidates"][0]
            self.assertEqual((candidate["comradesPairCounts"], candidate["comradesScore"]), ([2, 2, 2], 12))

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

    def test_duplex_normalises_and_validates_host_counts(self):
        pairs = [{"one": 3, "two": 30, "count": 2}, [30, 3, 1], [5, 28, 4]]
        result = self.web.bonus_matrix(_payload(supportKernel="duplex", evidencePairs=pairs, saturatingQ=3))
        entries = {(e["one"], e["two"]): e["value"] for e in result["evidence"]["bonusEntries"]}
        self.assertEqual(set(entries), {(3, 30), (5, 28)})
        self.assertAlmostEqual(entries[(3, 30)], 1.8, places=6)
        self.assertAlmostEqual(entries[(5, 28)], 3.6 * 4 / 7, places=6)
        for bad in ([[3, 3, 1]], [[0, 5, 1]], [[3, 5, 0]], "x", None):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                self.web.bonus_matrix(_payload(supportKernel="duplex", evidencePairs=bad))

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
                         "IRIS Gaussian arm blocks; symmetric float64 accumulation; threshold 2^-23; log1p")

    def test_gaussian_matches_independent_iris_reference(self):
        import numpy as np

        # Independent transcription of IRIS Scoring.compute_PARIS_support
        # (third_party/iris, snapshot 4057c5ac) and IRIS EPS = 2^-23.
        # Decimal avoids sharing production profiles, accumulation or rounding.
        n = len(SEQUENCE)
        blocks = [((a - 1, b), (c - 1, d)) for a, b, c, d in ARMS]
        with localcontext() as context:
            context.prec = 80
            sigma = Decimal(sum(hi - lo for block in blocks for lo, hi in block)) / (12 * len(blocks))
            profiles = []
            for block in blocks:
                vectors = []
                for lo, hi in block:
                    centre = (Decimal(lo) + hi) / 2
                    density = [(-((Decimal(i) - centre) ** 2) / (2 * sigma ** 2)).exp() for i in range(n)]
                    peak = max(density)
                    vectors.append([value / peak for value in density])
                profiles.append(vectors)
            expected = []
            for i in range(n):
                for j in range(i + 1, n):
                    support = sum((left[i] * right[j] + right[i] * left[j]
                                   for left, right in profiles), Decimal(0))
                    if support >= Decimal(2) ** -23:
                        expected.append((i + 1, j + 1, float((1 + support).ln())))

        rounded = [(i, j, round(value, 7)) for i, j, value in expected]
        self.assertEqual((hashlib.sha256(json.dumps(rounded).encode()).hexdigest()[:16], len(rounded)),
                         ("790bd16a79ee8851", 618))
        for request in (self.web.fold, self.web.bonus_matrix):
            with self.subTest(request=request.__name__):
                result = request(_payload(supportKernel="gaussian"))
                actual = sorted((entry["one"], entry["two"], float(entry["value"]))
                                for entry in result["evidence"]["bonusEntries"])
                # Exact coordinates, count and seven-decimal values preserve
                # the old digest's strictness; also check unrounded values.
                self.assertEqual([(i, j, round(value, 7)) for i, j, value in actual], rounded)
                np.testing.assert_allclose([value for _, _, value in actual],
                                           [value for _, _, value in expected], atol=1e-14, rtol=0)

    def test_gaussian_sums_weak_tails_before_threshold(self):
        import numpy as np
        from pipeline.hyb2analysis.iris import support_matrix
        from cplfold.bonus import build_cofold_bonus_matrix

        # Ten distinct blocks share centres (12, 48) and sigma=1. At (6, 48)
        # each adds exp(-18), below the cutoff; their sum is just above it.
        n = 64
        blocks = [(13 - k, 12 + k, 43 + k, 54 - k) for k in range(1, 6)]
        blocks += [(c, d, a, b) for a, b, c, d in blocks]
        fields = ("oneStart", "oneEnd", "twoStart", "twoEnd")
        iris = support_matrix(n, [dict(zip(fields, block)) for block in blocks])
        self.assertAlmostEqual(iris[6, 48], math.log1p(10 * math.exp(-18)), delta=1e-18)
        self.assertEqual(iris[6, 49], 0)
        for cutpoint in (None, 32):
            # Both centres are far enough from the nick that clipping cannot
            # change a surviving cell. Also check the dedicated boundary tests.
            fast, _ = self.web.build_hyb_bonus_matrix_fast(n, Counter(blocks), cutpoint=cutpoint)
            dense = (self.web.build_hyb_bonus_matrix(n, blocks)[0] if cutpoint is None else
                     build_cofold_bonus_matrix(n, cutpoint, blocks))
            for matrix in (dense, fast):
                np.testing.assert_array_equal(matrix > 0, iris > 0)
                np.testing.assert_allclose(matrix, iris, atol=1e-14, rtol=0)
            summary = self.web.build_hyb_bonus_matrix_export(n, blocks, cutpoint)
            self.assertEqual(summary["bonusEntries"], self.web.bonus_matrix_listing(fast)["bonusEntries"])
            self.assertEqual(summary["nonzeroBonusCells"], np.count_nonzero(iris))

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

    def test_removed_kernel_is_rejected_even_with_saved_counts(self):
        for request in (self.web.fold, self.web.bonus_matrix):
            for extra in ({}, {"evidencePairs": [[3, 30, 2]]}, {"evidenceMode": "none"}):
                with self.subTest(request=request.__name__, extra=extra), self.assertRaisesRegex(ValueError, "kernel"):
                    request(_payload(supportKernel="rnacofold", **extra))
        # The old Python helper default was the removed kernel, not duplex.
        for options in ({}, {"kernel": "rnacofold"}):
            with self.subTest(options=options), self.assertRaisesRegex(ValueError, "duplex"):
                self.web.build_pair_bonus_matrix(len(SEQUENCE), Counter({(3, 30): 2}), **options)

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
    def test_comrades_counts_only_cross_arm_pairs_after_joint_folding(self):
        # The four-pair hairpin in arm 1 survives. Only its three-base tail
        # binds arm 2. Two identical rows and a reversed row support each pair.
        sequence = "GGGGAAAACCCCGGG" + "AAA" + "CCC"
        arms = [(1, 15, 19, 21)] * 2 + [(19, 21, 1, 15)]
        counts, _ = self.web.arm_pair_counts(sequence, arms, kernel="comrades")
        self.assertEqual(counts, {(13, 21): 3, (14, 20): 3, (15, 19): 3})
        # Remove that tail from the observed arm. The same two arms now give
        # zero cross-arm pairs with RNAcofold, but three with RNAduplex.
        request = _payload(sequence=sequence, evidenceArms=[{"oneStart": 1, "oneEnd": 12, "twoStart": 19, "twoEnd": 21}])
        self.assertEqual(self.web.bonus_matrix(dict(request, supportKernel="comrades"))["evidence"]["bonusEntries"], [])
        self.assertEqual(len(self.web.bonus_matrix(dict(request, supportKernel="duplex"))["evidence"]["bonusEntries"]), 3)

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

    def test_compressed_two_strand_export_matches_expanded_rows(self):
        blocks = [[1, 3, 7, 9, 4], [4, 4, 8, 9, 1]]
        arms = [dict(oneStart=a, oneEnd=b, twoStart=c, twoEnd=d) for a, b, c, d, n in blocks for _ in range(n)]
        for kernel in self.web.SUPPORT_KERNELS:
            with self.subTest(kernel=kernel):
                base = dict(sequence="GGGACC&CCC", evidenceMode="hyb-blocks", supportKernel=kernel,
                            evidencePairs=[[1, 9, 4], [4, 8, 1]])
                expanded = self.web.bonus_matrix(dict(base, evidenceArms=arms))["evidence"]
                counted = self.web.bonus_matrix(dict(base, evidenceArmCounts=blocks))["evidence"]
                self.assertEqual(counted, expanded)
                self.assertEqual(counted["inputRecords"], 5)

    def test_empty_duplex_evidence_matches_export(self):
        payload = _payload(supportKernel="duplex", evidencePairs=[])
        for request in (self.web.fold, self.web.bonus_matrix):
            result = request(payload)
            evidence = result["evidence"]
            self.assertEqual(evidence["bonusEntries"], [])
            self.assertEqual(evidence["saturatingQ"], 0)
            if "candidates" in result:
                self.assertEqual(result["comradesScore"], 0)

    def test_pair_kernels_score_the_fold_from_raw_counts_not_the_bonus(self):
        arms = [{"oneStart": 1, "oneEnd": 3, "twoStart": 7, "twoEnd": 9}] * 2
        payload = _payload(sequence="GGGAAACCC", evidenceArms=arms, alpha=0)
        # With alpha=0 the three nested pairs stay fixed. (1,7) has ample
        # evidence but is not formed; (2,8) forms but has no supplied evidence.
        for kernel in ("comrades", "duplex"):
            for q in (2, 12):
                request = dict(payload, supportKernel=kernel, evidencePairs=[[9, 1, 2], [3, 7, 5], [1, 7, 99]], saturatingQ=q)
                result = self.web.fold(request)
                self.assertEqual(result["dotBracket"], "(((...)))")
                self.assertEqual(result["comradesPairCounts"], [2, 0, 5])
                self.assertEqual(result["comradesScore"], 14)
                self.assertEqual(result["candidates"][0]["comradesScore"], 14)
                self.assertAlmostEqual(result["pairs"][0]["bonusSupport"], 3.6 * 2 / (2 + q), places=6)
                self.assertEqual(self.web.bonus_matrix(request)["evidence"]["bonusEntries"], result["evidence"]["bonusEntries"])
            if _has_vienna():
                computed = self.web.fold(dict(payload, supportKernel=kernel))
                self.assertEqual(computed["comradesPairCounts"], [2, 2, 2])
                self.assertEqual(computed["comradesScore"], 12)

    def test_endpoint_ceiling_does_not_clip_the_evidence_or_bonus(self):
        payload = _payload(sequence="GGGAAACCC", evidenceArms=[{"oneStart": 1, "oneEnd": 3, "twoStart": 7, "twoEnd": 9}],
                           supportKernel="comrades", evidencePairs=[[1, 9, 1_500_000], [9, 1, 500_000], [3, 7, 17]],
                           saturatingQ=1_000_000, alpha=0)
        result = self.web.fold(payload)
        self.assertEqual(result["comradesPairCounts"], [2_000_000, 0, 17])
        self.assertEqual(result["comradesScore"], 2_000_034)
        self.assertAlmostEqual(result["pairs"][0]["bonusSupport"], 2.4, places=6)

    def test_native_prediction_preserves_the_strand_break_and_evidence_scale(self):
        from desktop.runtime.bridge import handle

        request = {"operation": "cplfold_predict", "sequence": "GGGCCC", "cutpoint": 3,
                   "evidenceMode": "hyb-blocks", "evidenceArms": [{"oneStart": 1, "oneEnd": 3, "twoStart": 4, "twoEnd": 6}],
                   "evidencePairs": [[6, 1, 2], [2, 5, 4]], "saturatingQ": 8,
                   "parameters": {"supportKernel": "comrades", "allowPseudoknot": False, "alpha": 0}}
        result = handle(request, repo_root=ROOT)
        self.assertEqual((result["engine"], result.get("dotBracketWithBreak")), ("CPLcofold", "(((&)))"))
        self.assertEqual(result["comradesPairCounts"], [2, 4, 0])
        self.assertEqual(result["comradesScore"], 12)
        self.assertAlmostEqual(result["pairs"][0]["bonusSupport"], 0.72, places=6)
        with self.assertRaisesRegex(ValueError, "evidencePairs"):
            handle(dict(request, evidencePairs=None), repo_root=ROOT)
        for layout in ({"sequence": "GGG", "sequenceTwo": "CCC"}, {"sequence": "GGG&CCC"}):
            with self.subTest(layout=layout):
                split = {**request, **layout, "parameters": {"supportKernel": "comrades", "allowPseudoknot": False}}
                del split["cutpoint"]
                same = handle(split, repo_root=ROOT)
                self.assertEqual((same["engine"], same["displaySequence"], same["parameters"]["alpha"], same["comradesScore"]),
                                 ("CPLcofold", "GGG&CCC", 0, 12))


class DesktopParameterTests(unittest.TestCase):
    def test_support_kernel_parameter(self):
        self.assertEqual(structure._parameters({}, "genome")["support_kernel"], "gaussian")
        for kernel in structure.SUPPORT_KERNELS:
            self.assertEqual(structure._parameters({"supportKernel": kernel}, "genome")["support_kernel"], kernel)
        for key in ("support_kernel", "supportKernel"):
            with self.subTest(key=key), self.assertRaises(ValueError):
                structure._parameters({key: "rnacofold"}, "genome")

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
