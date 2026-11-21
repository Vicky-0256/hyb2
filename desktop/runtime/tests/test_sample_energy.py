"""samples[].energy: how a sample run computed the .hyb dG column (RNAduplex
on every platform)."""
import dataclasses
import json
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

from desktop.runtime.project import new_project, validate_manifest
from desktop.runtime.sample_run import build_options, energy_record
from desktop.runtime.samples import add_sample, set_sample_stage

ROOT = Path(__file__).resolve().parents[3]
SAM = ROOT / "data" / "testData.sam"
REFERENCE = ROOT / "data" / "Zika_18S_formatted.fasta"
DUPLEX = {"engine": "rnaduplex", "program": "ViennaRNA RNAduplex", "version": "2.7.2", "executable": None,
          "parameters": "RNA, 37 \u00b0C (ViennaRNA defaults)", "scope": "each hybrid's two arms, intermolecular pairs only"}


@dataclasses.dataclass
class _Options:
    energy: bool = False
    energy_engine: str = "rnaduplex"


class EnergyRecordTest(unittest.TestCase):
    def test_record_from_result(self):
        record = energy_record(SimpleNamespace(energy=dict(DUPLEX, extra=[1])))
        self.assertEqual({k: record[k] for k in DUPLEX}, DUPLEX)
        self.assertEqual(record["source"], "computed")
        self.assertNotIn("extra", record)
        self.assertIsNone(energy_record(SimpleNamespace(energy=None)))
        self.assertIsNone(energy_record(SimpleNamespace()))  # a pipeline before Result.energy
        self.assertIsNone(energy_record(SimpleNamespace(energy={"engine": ""})))
        self.assertIsNone(energy_record(SimpleNamespace(energy={"engine": "rnaduplex", "version": None}))["version"])

    def test_stored_per_sample_and_cleared(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp) / "p.hyb2proj"
            new_project(project, "energy")
            sample = add_sample(project, SAM, reference=REFERENCE)
            read = lambda: json.loads((project / "project.json").read_text(encoding="utf-8"))  # noqa: E731
            set_sample_stage(project, sample["id"], "hyb", "succeeded", task_id="t1",
                             energy=energy_record(SimpleNamespace(energy=DUPLEX)))
            manifest = read()
            self.assertEqual(validate_manifest(manifest), [])
            self.assertEqual(manifest["samples"][0]["energy"]["version"], "2.7.2")
            # a stage update without energy keeps it; energy=None removes it
            set_sample_stage(project, sample["id"], "index", "succeeded", task_id="t1", claim=False)
            self.assertEqual(read()["samples"][0]["energy"]["engine"], "rnaduplex")
            set_sample_stage(project, sample["id"], "index", "succeeded", task_id="t1", claim=False, energy=None)
            self.assertNotIn("energy", read()["samples"][0])
            manifest["samples"][0]["energy"] = {"engine": "rnaduplex", "version": 3}
            self.assertTrue(any(".energy.version" in e for e in validate_manifest(manifest)))
            manifest["samples"][0]["energy"] = {"version": "3"}
            self.assertTrue(any(".energy must be an object with an engine" in e for e in validate_manifest(manifest)))

    def test_schema_allows_energy(self):
        schema = json.loads((ROOT / "desktop" / "spec" / "project.schema.json").read_text(encoding="utf-8"))
        self.assertIn("energy", schema["$defs"]["sample"]["properties"])


class EngineChoiceTest(unittest.TestCase):
    def test_every_platform_uses_rnaduplex(self):
        for platform in ("win32", "darwin", "linux"):
            with mock.patch.object(sys, "platform", platform):
                options, ignored = build_options(_Options, {})
            self.assertEqual((options.energy_engine, options.energy, ignored), ("rnaduplex", True, []))

    def test_a_saved_executable_path_is_ignored(self):
        old = "hybrid_min"  # legacy-name: sample setting of versions before RNAduplex
        options, ignored = build_options(_Options, {old: "C:\\x\\prog.exe"})
        self.assertEqual((options.energy_engine, ignored), ("rnaduplex", []))


if __name__ == "__main__":
    unittest.main()
