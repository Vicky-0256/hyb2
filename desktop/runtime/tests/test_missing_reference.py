"""Samples that cannot run because they have no reference.

Reads (not .hyb) are mapped to the sample's reference; without one the
pipeline refuses to start. check_inputs reports such samples so the user
learns it before running, not from a failed task.
"""
import gzip
import tempfile
import unittest
from pathlib import Path

from desktop.runtime.bridge import handle
from desktop.runtime.project import new_project

ROOT = Path(__file__).resolve().parents[3]


class MissingReferenceTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.project = self.dir / "p.hyb2proj"
        new_project(self.project, "Refs")
        self.reads = self.dir / "WT_rep1.fastq.gz"
        with gzip.open(self.reads, "wt") as out:
            out.write("@r\nACGT\n+\nIIII\n")
        self.fasta = self.dir / "ref.fasta"
        self.fasta.write_text(">Gene_A\nACGUACGUAC\n", encoding="utf-8")

    def tearDown(self):
        self.tmp.cleanup()

    def call(self, operation, **request):
        return handle({"operation": operation, "path": str(self.project), **request}, repo_root=ROOT)

    def test_reads_without_a_reference_are_reported(self):
        self.call("import_sample_rows", rows=[{"path": str(self.reads), "name": "WT_rep1"}], mode="linked")
        checked = self.call("check_inputs")
        self.assertFalse(checked["all_ok"], checked)
        self.assertEqual(checked["no_reference"], ["WT_rep1"])
        self.assertEqual(checked["summary"]["no_reference"], 1)
        # The reads file itself is fine: nothing to relink.
        self.assertEqual({item["status"] for item in checked["items"]}, {"ok"})

    def test_a_sample_with_a_reference_is_ok(self):
        self.call("import_sample_rows", rows=[{"path": str(self.reads), "name": "WT_rep1", "reference": str(self.fasta)}],
                  mode="linked")
        checked = self.call("check_inputs")
        self.assertTrue(checked["all_ok"], checked)
        self.assertEqual(checked["no_reference"], [])

    def test_hyb_samples_need_no_reference(self):
        hyb = self.dir / "s.hyb"
        hyb.write_text("1\tACGT\t.\tGene_A\t1\t4\t1\t4\t0.1\tGene_A\t1\t4\t1\t4\t0.1\n", encoding="utf-8")
        self.call("import_sample_rows", rows=[{"path": str(hyb), "name": "S"}], mode="linked")
        self.assertEqual(self.call("check_inputs")["no_reference"], [])


if __name__ == "__main__":
    unittest.main()
