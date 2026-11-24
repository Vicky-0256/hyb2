"""The browser bridge reads kernel transforms and the COMRADES score rule from
hyb2analysis.support_kernels, the source of the generated UI catalogs.

Run from the repository root with a Python that has NumPy, for example
desktop/resources/native/python/bin/python3 -B -m unittest pipeline.tests.test_support_kernel_catalog
"""
import importlib.util
import json
from collections import Counter
from pathlib import Path
import sys
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path[:0] = [str(ROOT / "third_party" / "cplfold"), str(ROOT / "pipeline")]
spec = importlib.util.spec_from_file_location("_support_kernel_catalog_bridge", ROOT / "web" / "cplfold" / "cplfold_web.py")
bridge = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = bridge
spec.loader.exec_module(bridge)

from hyb2analysis import support_kernels  # noqa: E402


class SupportKernelCatalogTests(unittest.TestCase):
    def test_bridge_records_the_catalog_transform(self):
        self.assertIs(bridge.SUPPORT_TRANSFORMS, support_kernels.SUPPORT_TRANSFORMS)
        for option in support_kernels.KERNEL_OPTIONS:
            saturating = option["value"] in support_kernels.PAIR_MODELS
            self.assertEqual(option["bonus"], "saturating_hyb_bonus" if saturating else "log1p_hyb_bonus")
            self.assertTrue(option["transform"].endswith("best-supported pairs" if saturating else "log1p"))
        summary = bridge._summary(6, "block", Counter({(1, 2, 5, 6): 1}), 4, 4, 1.0)
        self.assertEqual(summary["transform"], support_kernels.SUPPORT_TRANSFORMS["block"])

    def test_generated_catalog_carries_the_score_rule(self):
        desktop = json.loads((ROOT / "desktop/app/views/support-kernels.json").read_text(encoding="utf-8"))
        self.assertEqual(desktop["comradesScore"], support_kernels.COMRADES_SCORE)
        self.assertEqual([option["bonus"] for option in desktop["options"]],
                         [option["bonus"] for option in support_kernels.KERNEL_OPTIONS])
        web = (ROOT / "web/support-kernels.js").read_text(encoding="utf-8")
        self.assertIn('"comradesScore": {\n    "endpointCap": 1000000,\n    "endpointsPerPair": 2\n  }', web)

    def test_comrades_score_caps_each_endpoint(self):
        # The same counts as scripts/verify/verify-comrades-analysis.cjs: the JS and Python scores agree.
        counts = Counter({(1, 8): 999_999, (2, 7): 1_000_000, (3, 6): 1_500_000})
        scored = bridge.score_evidence_pairs(counts, [(8, 1), (2, 7), (3, 6), (4, 5)])
        self.assertEqual(scored, {"comradesPairCounts": [999_999, 1_000_000, 1_500_000, 0], "comradesScore": 5_999_998})

    def test_single_strand_arm_coordinates_are_whole_numbers(self):
        def arm(value):
            return [dict(oneStart=1, oneEnd=value, twoStart=8, twoEnd=10)]
        self.assertEqual(bridge._normalise_arms(arm(3), 10), [(1, 3, 8, 10)])
        self.assertEqual(bridge._normalise_arms(arm(3.0), 10), [(1, 3, 8, 10)])
        for bad in (5.9, True, None, "x", 11, 0):
            with self.subTest(value=bad), self.assertRaisesRegex(ValueError, "row 1"):
                bridge._normalise_arms(arm(bad), 10)


if __name__ == "__main__":
    unittest.main()
