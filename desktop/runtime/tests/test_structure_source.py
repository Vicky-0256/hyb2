"""structure_run of a pasted or HYB record sequence ("source": "paste" | "record")."""
import json
import tempfile
import unittest
from pathlib import Path

from desktop.runtime.bridge import handle
from desktop.runtime.project import _structure_entry, new_project

from .test_structure import GENE, ROOT, _sequence

ROW = "r{k}\tACGU\t.\t{g1}\t1\t20\t{a0}\t{a1}\t1e-5\t{g2}\t21\t40\t{b0}\t{b1}\t1e-5\t\t-10\tType_1\n"


class SequenceSourceTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.project = Path(self.tmp.name) / "p.hyb2proj"
        new_project(self.project, "Source")
        (self.project / "data").mkdir()
        self.one, self.two = _sequence(240, seed=3), _sequence(120, seed=8)
        (self.project / "data" / "ref.fa").write_text(f">{GENE} test\n{self.one}\n>OTHER_RNA\n{self.two}\n")
        rows = [ROW.format(k=k, g1=GENE, a0=45, a1=70, g2=GENE, b0=150, b1=175) for k in range(6)]
        rows += [ROW.format(k=10 + k, g1=GENE, a0=5, a1=30, g2="OTHER_RNA", b0=60, b1=85) for k in range(4)]
        (self.project / "data" / "a.hyb").write_text("".join(rows))
        # not in the reference (another seed), so folded from the sequence alone
        self.foreign = _sequence(90, seed=21)

    def tearDown(self):
        self.tmp.cleanup()

    def _run(self, **extra):
        request = {"operation": "structure_run", "project_path": str(self.project), "reference": "data/ref.fa",
                   "hyb_paths": ["data/a.hyb"], "gene_id": "pasted_sequence", "mode": "genome", "source": "paste"}
        request.update(extra)
        return handle(request, repo_root=ROOT)

    def _meta(self, task):
        return json.loads((self.project / "structures" / task / "meta.json").read_text())

    def _read(self, task, result):
        """Register the result as the task layer does, then read it back."""
        from desktop.runtime.structure import read_structure
        entry = _structure_entry(self.project.resolve(), task, result)
        manifest = json.loads((self.project / "project.json").read_text())
        manifest["structures"] = [entry]
        (self.project / "project.json").write_text(json.dumps(manifest))
        return entry, read_structure(self.project, task)

    def test_pasted_sequence_not_in_reference_is_sequence_only(self):
        # read as the web reads a paste: FASTA header, whitespace, lower case, T
        pasted = ">mine\n" + self.foreign[:45].lower().replace("u", "t") + "\n  " + self.foreign[45:] + "\n"
        result = self._run(task_id="p1", sequence=pasted)
        self.assertTrue(result["ok"], result)
        meta = self._meta("p1")
        self.assertEqual(meta["sequence_source"]["kind"], "paste")
        self.assertNotIn("mapped_to", meta["sequence_source"])
        self.assertEqual(meta["parameters"]["evidence"], "none")
        self.assertEqual(meta["parameters"]["sequence_source"], "paste")
        self.assertIn("folded sequence", meta["coordinates"])
        dbn = (self.project / "structures" / "p1" / "structure.dbn").read_text().splitlines()
        self.assertTrue(dbn[0].startswith(">pasted_sequence:1-90 "), dbn[0])
        self.assertEqual(dbn[1], self.foreign)
        ct = (self.project / "structures" / "p1" / "structure.ct").read_text().splitlines()
        self.assertEqual(len(ct), 91)
        # result.json and the project.json entry name the source
        stored = json.loads((Path(result["run_dir"]) / "result.json").read_text())
        self.assertEqual(stored["structure"]["parameters"]["sequence_source"], "paste")
        entry, read = self._read("p1", result)
        self.assertEqual((entry["gene_id"], entry["parameters"]["sequence_label"]), ("pasted_sequence", "pasted_sequence"))
        record = read["records"][0]
        self.assertEqual((record["window_start"], record["window_end"], record["sequence"]), (1, 90, self.foreign))

    def test_pasted_region_of_the_reference_uses_its_chimeras(self):
        result = self._run(task_id="p2", sequence=self.one[39:200])
        self.assertTrue(result["ok"], result)
        meta = self._meta("p2")
        self.assertEqual(meta["sequence_source"]["mapped_to"], [f"{GENE}:40-200"])
        self.assertEqual((meta["mode"], meta["windows"], meta["gene_id"]), ("windows", [{"start": 40, "end": 200}], GENE))
        self.assertEqual(meta["parameters"]["evidence"], "hyb")
        self.assertEqual(meta["records"][0]["evidence"]["inputRecords"], 6)
        # asked for sequence only, it is not searched for and stays sequence only
        alone = self._run(task_id="p3", sequence=self.one[39:200], parameters={"evidence": "none"})
        self.assertTrue(alone["ok"], alone)
        self.assertEqual(self._meta("p3")["gene_id"], "pasted_sequence")

    def test_record_sequence_with_viennarna(self):
        result = self._run(task_id="r1", source="record", engine="viennarna", gene_id="record 17_3",
                           sequence=self.foreign, parameters={"min_loop": 3})
        self.assertTrue(result["ok"], result)
        meta = self._meta("r1")
        self.assertEqual((meta["engine"], meta["gene_id"]), ("ViennaRNA", "record_17_3"))
        self.assertEqual(meta["parameters"]["sequence_source"], "record")

    def test_comrades_needs_a_reference_region(self):
        with self.assertRaisesRegex(ValueError, "ViennaRNA MFE or sequence-only CPLfold"):
            self._run(task_id="c1", engine="comrades", sequence=self.foreign)

    def test_two_pasted_strands_with_cplcofold(self):
        cofold = {"engine": "cplcofold", "mode": "cofold", "gene_id": "pasted"}
        result = self._run(task_id="co1", sequence=self.foreign[:50], sequence_two=self.foreign[50:], **cofold)
        self.assertTrue(result["ok"], result)
        meta = self._meta("co1")
        self.assertEqual([r["gene_id"] for r in meta["regions"]], ["pasted_A", "pasted_B"])
        self.assertTrue(meta["evidence_inputs"]["rows"].startswith("none"))
        dbn = (self.project / "structures" / "co1" / "structure.dbn").read_text().splitlines()
        self.assertEqual(dbn[1], self.foreign[:50] + "&" + self.foreign[50:])
        _, read = self._read("co1", result)
        self.assertEqual(read["records"][0]["cutpoint"], 50)
        # one A&B sequence, the RNA B box empty
        same = self._run(task_id="co2", sequence=self.foreign[:50] + "&" + self.foreign[50:], **cofold)
        self.assertTrue(same["ok"], same)
        self.assertEqual((self.project / "structures" / "co2" / "structure.dbn").read_text().splitlines()[1], dbn[1])
        # both strands are regions of the reference: folded as those regions, with their chimeras
        mapped = self._run(task_id="co3", sequence=self.one[:80], sequence_two=self.two[40:100], **cofold)
        self.assertTrue(mapped["ok"], mapped)
        meta = self._meta("co3")
        self.assertEqual(meta["sequence_source"]["mapped_to"], [f"{GENE}:1-80", "OTHER_RNA:41-100"])
        self.assertEqual(meta["records"][0]["evidence_rows"]["between"], 4)

    def test_rejects(self):
        for extra in ({"sequence": self.foreign[:30] + "N"}, {"sequence": "ACGUACG"}, {"sequence": 5},
                      {"sequence": self.foreign, "mode": "windows", "windows": [{"start": 1, "end": 40}]},
                      {"sequence": self.foreign, "sequence_two": self.foreign},
                      {"sequence": self.foreign, "source": "reference"}, {"sequence": self.foreign, "source": "file"},
                      {"sequence": self.foreign, "source": "record", "engine": "cplcofold", "mode": "cofold",
                       "sequence_two": self.foreign},
                      {"sequence": "A&C&G", "engine": "cplcofold", "mode": "cofold"}):
            with self.assertRaises(ValueError, msg=extra):
                self._run(task_id="bad", **extra)


if __name__ == "__main__":
    unittest.main()
