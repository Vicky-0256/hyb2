import importlib.util
import unittest
from pathlib import Path
from unittest.mock import patch

from desktop.runtime.bridge import _load_cplfold_module, handle

ROOT = Path(__file__).resolve().parents[3]


class WarmupTest(unittest.TestCase):
    def test_reports_unavailable_without_cplfold_sources(self):
        result = handle({"operation": "warmup"}, repo_root=ROOT / "desktop")
        self.assertEqual(result["status"], "unavailable")

    @unittest.skipUnless(importlib.util.find_spec("numpy"), "numpy not installed")
    def test_folds_the_reference_hairpin(self):
        _load_cplfold_module(ROOT)
        from hyb2analysis.engine_parameters import CPL_SEARCH

        # A valid catalog change may rule out yesterday's default beam.
        with patch.dict(CPL_SEARCH, beamSize=(21, 200, 27)):
            result = handle({"operation": "warmup"}, repo_root=ROOT)
        self.assertEqual(result["status"], "warm")
        self.assertGreaterEqual(result["seconds"], 0)


if __name__ == "__main__":
    unittest.main()
