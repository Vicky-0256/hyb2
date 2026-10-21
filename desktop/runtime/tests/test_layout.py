import tempfile
import unittest
from pathlib import Path

from desktop.runtime.bridge import handle
from desktop.runtime.layout import structure_layout
from desktop.runtime.project import new_project

ROOT = Path(__file__).resolve().parents[3]


class LayoutTest(unittest.TestCase):
    """The folded view's naview layout of a nested structure."""

    def test_layout(self):
        dbn = "((((....))))..((((....))))"
        out = structure_layout(dbn)
        self.assertEqual((len(out["x"]), len(out["y"])), (len(dbn), len(dbn)))
        # paired bases sit a stem's width apart; neighbours about one step
        step = ((out["x"][1] - out["x"][0]) ** 2 + (out["y"][1] - out["y"][0]) ** 2) ** 0.5
        self.assertGreater(step, 1)
        self.assertEqual(len(set(zip(out["x"], out["y"]))), len(dbn))
        for bad in ("", "((..)", "(..))", "([..])", 5, "A" * 3):
            with self.assertRaises(ValueError, msg=repr(bad)):
                structure_layout(bad)

    def test_bridge(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp) / "p.hyb2proj"
            new_project(project, "Layout")
            out = handle({"operation": "structure_layout", "path": str(project), "dbn": "((...))"}, repo_root=ROOT)
            self.assertTrue(out["ok"])
            self.assertEqual(len(out["x"]), 7)
            with self.assertRaises(ValueError):
                handle({"operation": "structure_layout", "path": str(project), "dbn": ")("}, repo_root=ROOT)


if __name__ == "__main__":
    unittest.main()
