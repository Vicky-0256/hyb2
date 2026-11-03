"""The web's Name matching for a project: HYB RNA names vs reference
sequence names, manual matches kept on the reference and applied where the
runtime looks an RNA up (desktop/runtime/name_matching.py)."""
import json
import sys
import tempfile
import unittest
from pathlib import Path

from desktop.runtime import samples
from desktop.runtime.bridge import _dumps_line, handle
from desktop.runtime.project import load_manifest, new_project

ROOT = Path(__file__).resolve().parents[3]
ARMS = "\t".join(["seq", ".", "{g1}", "1", "20", "1", "20", "1e-5", "{g2}", "21", "40", "30", "50", "1e-5"])


class NameMatchingTest(unittest.TestCase):
    def setUp(self):
        sys.path.insert(0, str(ROOT / "pipeline"))
        from hyb2analysis import index

        self.tmp = tempfile.TemporaryDirectory()
        base = Path(self.tmp.name)
        self.project = base / "p.hyb2proj"
        new_project(self.project, "Names")
        fasta = base / "ref.fasta"
        fasta.write_text(">geneA first\nACGTACGTAC\nGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGTACGT\n"
                         ">geneB\nACGUNNRY-acgu\n>other\nacgtacgt\n")
        self.reference = samples.add_reference(self.project, fasta, label="Ref")["reference"]
        hyb = base / "s.hyb"
        hyb.write_text("".join("r%d_2\t%s\n" % (i, ARMS.format(g1=g1, g2=g2))
                               for i, (g1, g2) in enumerate([("geneA", "geneB"), ("geneA", "geneC"), ("geneD", "geneA")])))
        sample = samples.add_sample(self.project, hyb, name="S1", reference_id=self.reference["id"])
        (self.project / "samples" / "S1").mkdir(parents=True, exist_ok=True)
        index.build(hyb, self.project / "samples" / "S1" / "s.hyb2idx")
        manifest = json.loads((self.project / "project.json").read_text())
        manifest["samples"][0]["outputs"]["index"] = "samples/S1/s.hyb2idx"
        (self.project / "project.json").write_text(json.dumps(manifest))
        self.sample_id = sample["id"]

    def tearDown(self):
        from hyb2analysis import query
        query.close_all()
        self.tmp.cleanup()

    def call(self, **request):
        request.setdefault("project_path", str(self.project))
        response = handle(request, repo_root=ROOT)
        json.loads(_dumps_line(response))
        return response

    def matching(self):
        return self.call(operation="analysis_reference_matching", sample_ids=[self.sample_id])["result"]

    def test_symbols_are_kept_when_the_reference_is_added(self):
        # the web's parseFasta: case and whitespace aside, anything but ACGTU
        self.assertEqual(self.reference["unsupported_symbols"], {"entries": 1, "characters": ["-", "N", "R", "Y"]})

    def test_matching_and_manual_matches(self):
        result = self.matching()
        self.assertEqual((result["rna_names"], result["matched"], result["sequences"]), (4, 2, 3))
        ref, = result["references"]
        self.assertEqual([(r["name"], r["match"]) for r in ref["rnas"]],
                         [("geneA", "geneA"), ("geneB", "geneB"), ("geneC", None), ("geneD", None)])
        self.assertEqual((ref["unsupported_entries"], ref["unsupported_characters"]), (1, ["-", "N", "R", "Y"]))
        self.assertEqual(ref["sequence_names"], ["geneA", "geneB", "other"])
        # the web parser's rnaNames
        self.assertEqual(result["rna_name_list"], ["geneA", "geneB", "geneC", "geneD"])

        ref_id = self.reference["id"]
        self.call(operation="set_reference_match", reference_id=ref_id, rna="geneC", sequence="other")
        self.call(operation="set_reference_match", reference_id=ref_id, rna="geneB", sequence="")
        self.assertEqual(load_manifest(self.project)["references"][0]["matches"], {"geneC": "other", "geneB": ""})
        rows = {r["name"]: (r["match"], r["manual"]) for r in self.matching()["references"][0]["rnas"]}
        self.assertEqual(rows, {"geneA": ("geneA", False), "geneB": (None, True), "geneC": ("other", True), "geneD": (None, False)})

        # applied where the runtime looks the RNA up
        read = self.call(operation="read_reference_sequence", reference_id=ref_id, name="geneC")
        self.assertEqual((read["name"], read["sequence_name"], read["sequence"]), ("geneC", "other", "acgtacgt"))
        with self.assertRaises(ValueError):
            self.call(operation="read_reference_sequence", reference_id=ref_id, name="geneB")
        summary = self.call(operation="analysis_summary", sample_ids=[self.sample_id], pairs=False)["result"]["merged"]
        sources = {g["name"]: (g["length"], g["length_source"]) for g in summary["genes"]}
        self.assertEqual(sources["geneC"], (8, "reference"))
        self.assertEqual(sources["geneB"][1], "observed")

        # back to the automatic match; an unknown sequence is refused
        self.call(operation="set_reference_match", reference_id=ref_id, rna="geneB", sequence="geneB")
        self.call(operation="set_reference_match", reference_id=ref_id, rna="geneC", sequence=None)
        self.assertNotIn("matches", load_manifest(self.project)["references"][0])
        for bad in ({"rna": "geneC", "sequence": "nope"}, {"rna": "", "sequence": "other"}, {"rna": "geneC", "sequence": 3}):
            with self.assertRaises(ValueError, msg=bad):
                self.call(operation="set_reference_match", reference_id=ref_id, **bad)

    def test_several_samples_name_list_in_web_order(self):
        from desktop.runtime.name_matching import reference_matching
        manifest = load_manifest(self.project)
        names = {"a": ["b", "Z"], "b": ["a", "Y", "b"]}
        result = reference_matching(self.project, manifest, ["a", "b"], lambda sample_id: names[sample_id])
        # localeCompare: case folded first, so "a" < "b" < "Y" < "Z"
        self.assertEqual(result["rna_name_list"], ["a", "b", "Y", "Z"])
        self.assertEqual(result["rna_names"], 4)

    def test_reference_lengths_do_not_depend_on_match_order(self):
        from desktop.runtime.name_matching import reference_lengths, resolve

        sequences = [{"name": "B", "length": 50}, {"name": "C", "length": 70}]
        for matches in ({"A": "B", "B": ""}, {"B": "", "A": "B"}, {"B": "C", "A": "B"}, {"A": "B", "B": "C"}):
            reference = {"sequences": sequences, "matches": matches}
            lengths = reference_lengths(reference)
            self.assertEqual(lengths["A"], 50, matches)  # sequence B, as resolve reads it
            self.assertEqual(resolve(reference, "A"), "B")
            if matches["B"]:
                self.assertEqual(lengths["B"], 70, matches)
            else:
                self.assertNotIn("B", lengths, matches)

    def test_older_reference_reads_its_symbols_from_the_file(self):
        manifest = json.loads((self.project / "project.json").read_text())
        del manifest["references"][0]["unsupported_symbols"]
        (self.project / "project.json").write_text(json.dumps(manifest))
        ref, = self.matching()["references"]
        self.assertEqual((ref["unsupported_entries"], ref["unsupported_characters"]), (1, ["-", "N", "R", "Y"]))


if __name__ == "__main__":
    unittest.main()
