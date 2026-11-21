"""``pipeline_energy_check``: the dG engine each sample's run would use, and
whether it is missing (read-only; same resolution as build_options)."""
import json
import shutil
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from desktop.runtime.bridge import handle
from desktop.runtime.project import new_project
from desktop.runtime.sample_run import build_options, energy_settings
from desktop.runtime.samples import add_sample

ROOT = Path(__file__).resolve().parents[3]
SAM = ROOT / "data" / "testData.sam"
REFERENCE = ROOT / "data" / "Zika_18S_formatted.fasta"
GOLDEN = ROOT / "pipeline" / "tests" / "golden" / "testData.hyb"
KEYS = {"needs_engine", "engine", "problem", "note"}
NONE = {"needs_engine": False, "engine": None, "problem": None, "note": None}


def _fake_duplex_folder(workers=None):
    return lambda pairs: [-(len(a) + len(b)) / 4.0 for a, b in pairs]


class EnergyCheckTest(unittest.TestCase):
    def setUp(self):
        package = str(ROOT / "pipeline")
        if package not in sys.path:
            sys.path.insert(0, package)
            self.addCleanup(sys.path.remove, package)
        from hyb2pipe import energy
        self.energy = energy
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.project = self.dir / "p.hyb2proj"
        new_project(self.project, "energy check")

    def check(self, ids, resuming=False):
        response = handle({"operation": "pipeline_energy_check", "path": str(self.project), "sample_ids": ids,
                           "resuming": resuming}, repo_root=ROOT)
        self.assertTrue(response["ok"], response)
        self.assertEqual(set(response["samples"]), set(ids))
        for entry in response["samples"].values():
            self.assertEqual(set(entry), KEYS)
        return response["samples"]

    def add(self, source=SAM, **settings):
        return add_sample(self.project, source, reference=REFERENCE, settings=settings or None)["id"]

    def without_vienna(self):
        return mock.patch.multiple(self.energy, duplex_folder=mock.Mock(side_effect=self.energy.EnergyUnavailable(
            "the dG column needs the ViennaRNA Python package or RNAduplex on PATH")))

    def test_rnaduplex_present(self):
        sample = self.add()
        entry = self.check([sample])[sample]
        self.assertEqual(entry, {"needs_engine": True, "engine": "rnaduplex", "problem": None, "note": None})

    def test_rnaduplex_missing(self):
        sample = self.add()
        with self.without_vienna():
            entry = self.check([sample])[sample]
        self.assertTrue(entry["needs_engine"])
        self.assertEqual(entry["engine"], "rnaduplex")
        self.assertIn("RNAduplex", entry["problem"])

    def test_every_platform_uses_rnaduplex(self):
        sample = self.add()
        with mock.patch.object(sys, "platform", "win32"):
            entry = self.check([sample])[sample]
        self.assertEqual(entry["engine"], "rnaduplex")
        self.assertEqual(energy_settings({}), {"energy": True, "energy_engine": "rnaduplex"})

    def test_saved_executable_setting_is_ignored(self):
        old = "hybrid_min"  # legacy-name: sample setting of versions before RNAduplex
        sample = self.add(**{old: str(self.dir / "nope")})
        self.assertEqual(self.check([sample])[sample]["problem"], None)
        self.assertNotIn(old, energy_settings({old: "/x"}))

    def test_settings_energy_false(self):
        sample = self.add(energy=False)
        with self.without_vienna():
            entry = self.check([sample])[sample]
        self.assertEqual(entry, NONE)

    def test_hyb_sample_and_unknown(self):
        hyb = self.dir / "given.hyb"
        shutil.copyfile(GOLDEN, hyb)
        sample = add_sample(self.project, hyb, reference=REFERENCE, mode="copied")["id"]
        with self.without_vienna():
            entries = self.check([sample, "nope"])
        self.assertEqual(entries[sample], NONE)
        self.assertEqual(entries["nope"]["problem"], "unknown sample")
        self.assertFalse(entries["nope"]["needs_engine"])

    def test_read_only(self):
        sample = self.add()
        before = (self.project / "project.json").read_bytes()
        self.check([sample], resuming=True)
        self.assertEqual((self.project / "project.json").read_bytes(), before)
        self.assertFalse((self.project / "samples" / sample).exists())

    def test_resuming_past_energy(self):
        from hyb2pipe import Options, pipeline as pipeline_module, run
        sample = self.add()
        out_prefix = self.project / "samples" / sample / "testData"
        options, _ = build_options(Options, {})
        # the energy stage finishes, then the run stops in annotate
        with mock.patch.object(self.energy, "duplex_folder", _fake_duplex_folder), \
                mock.patch.object(pipeline_module, "_annotate", side_effect=KeyboardInterrupt):
            with self.assertRaises(KeyboardInterrupt):
                run(SAM, REFERENCE, out_prefix, options)
        with self.without_vienna():
            resumed = self.check([sample], resuming=True)[sample]
            fresh = self.check([sample], resuming=False)[sample]
        self.assertEqual(resumed, {"needs_engine": False, "engine": "rnaduplex", "problem": None, "note": None})
        self.assertTrue(fresh["needs_engine"])
        self.assertIn("RNAduplex", fresh["problem"])
        # the sample run resumes without ViennaRNA, and says it did not run energy
        run_dir = self.project / "runs" / "sample-resume"
        run_dir.mkdir(parents=True)
        with self.without_vienna():
            result = handle({"operation": "sample_pipeline_run", "project_path": str(self.project),
                             "sample_id": sample, "task_id": "sample-resume", "run_dir": str(run_dir)}, repo_root=ROOT)
        self.assertTrue(result["ok"], result)
        self.assertEqual(result["performance"]["energy"], {"engine": "rnaduplex", "ran": False})
        stored = json.loads((self.project / "project.json").read_text(encoding="utf-8"))
        energy = next(s for s in stored["samples"] if s["id"] == sample)["energy"]
        self.assertEqual((energy["engine"], energy["program"]), ("rnaduplex", "ViennaRNA RNAduplex"))


if __name__ == "__main__":
    unittest.main()
