"""The web's Name matching for a project: HYB RNA names vs reference
sequence names, manual matches kept on the reference and applied where the
runtime looks an RNA up (desktop/runtime/name_matching.py)."""
import json
import os
import shutil
import subprocess
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

    def test_explicitly_unmatched_without_an_automatic_match_is_persisted(self):
        ref_id = self.reference["id"]
        self.call(operation="set_reference_match", reference_id=ref_id, rna="geneC", sequence="")
        self.assertEqual(load_manifest(self.project)["references"][0]["matches"], {"geneC": ""})
        with self.assertRaisesRegex(ValueError, "geneC is marked as not matched"):
            self.call(operation="read_reference_sequence", reference_id=ref_id, name="geneC")

    def test_legacy_duplicate_ids_and_missing_match_targets_do_not_supply_lengths(self):
        from desktop.runtime.name_matching import reference_lengths, resolve

        ref = {"sequences": [{"name": "A", "length": 16}], "matches": {"A": "missing"}}
        self.assertIsNone(resolve(ref, "A"))
        self.assertNotIn("A", reference_lengths(ref))
        ref["sequences"].extend([{"name": "A", "length": 21}, {"name": "C", "length": 37}])
        ref["matches"] = {"B": "A"}
        self.assertIsNone(resolve(ref, "A"))
        self.assertIsNone(resolve(ref, "B"))
        self.assertEqual(reference_lengths(ref), {"C": 37})

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


@unittest.skipUnless(shutil.which("node"), "Node.js is required for the cross-host fixture")
class CrossHostFastaTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.project = Path(tmp.name) / "names.hyb2proj"
        new_project(self.project, "Names")

    def reference(self, records, pipe_indent=""):
        with tempfile.NamedTemporaryFile(dir=self.project / "inputs", suffix=".fa", mode="w",
                                         encoding="utf-8", delete=False) as f:
            f.writelines(f"{pipe_indent if '|' in header else ''}>{header}\n{seq}\n" for header, seq in records)
        return Path(f.name)

    def web(self, path, records, names, manual=None):
        fixture = Path(__file__).with_name("web_fasta_reference.js")
        payload = dict(text=path.read_text(encoding="utf-8"), records=records, names=list(names), manual=manual)
        return json.loads(subprocess.check_output(["node", str(fixture)], text=True, input=json.dumps(payload)))

    def compare(self, records, expected, native_manual=None, web_manual=None, *, path=None):
        from desktop.runtime.batch_energy import arms, reference_sequences
        from hyb2pipe import inputs

        path = path or self.reference(records)
        ref = samples.add_reference(self.project, path)["reference"]
        if inputs.needs_formatting(path):
            lines = inputs.format_reference(inputs.iter_lines(path))
            self.assertEqual([s["name"] for s in ref["sequences"]], [s[1:].split()[0] for s in lines if s.startswith(">")])
        for rna, target in (native_manual or {}).items():
            handle(dict(operation="set_reference_match", project_path=str(self.project), reference_id=ref["id"],
                        rna=rna, sequence=target), repo_root=ROOT)
        sequences = reference_sequences(path)
        native = {}
        for rna, record in expected.items():
            read = dict(operation="read_reference_sequence", project_path=str(self.project), reference_id=ref["id"], name=rna)
            end = str(len(record[1]) if record else 1)
            cols = ["row", "ACGU", ".", rna, "1", end, "1", end, ".", rna, "1", end, "1", end, "."]
            if record is None:
                with self.assertRaisesRegex(ValueError, "marked as not matched|not in reference"):
                    handle(read, repo_root=ROOT)
                with self.assertRaisesRegex(ValueError, "marked as not matched|No mapped FASTA"):
                    arms(cols, sequences, 500, "test", native_manual)
                native[rna] = None
            else:
                preview = handle(read, repo_root=ROOT)
                strand, _ = arms(cols, sequences, 500, "test", native_manual)
                native[rna] = [preview["header"], preview["sequence"], strand]
                self.assertEqual(native[rna], [record[0], record[1], record[1]])
        for browser in self.web(path, records, expected, web_manual):
            self.assertEqual(browser, native)

    def test_reported_collision_and_one_hop_manual_choices_in_both_orders(self):
        a, c = ("A|B", "A" * 12), ("A_B", "C" * 12)
        for records in ([a, c], [c, a]):
            with self.subTest(records=records):
                self.compare(records, {"A_B": a, "A-B": c, "A|B": None})
                # Desktop manual targets are effective IDs; Web targets the original record ID.
                self.compare(records, {"A_B": c, "A-B": None, "other": a, "A|B": None},
                             {"A_B": "A-B", "A-B": "", "other": "A_B", "A|B": ""},
                             {"A_B": "A_B", "A-B": "", "other": "A|B", "A|B": ""})

    def test_no_pipe_and_pipeline_substitutions(self):
        a, c = ("A-B description", "A" * 12), ("A_B plain", "C" * 12)
        self.compare([c, a], {"A_B": c, "A-B": a})
        piped = ("Gene_x|protein_coding original description", "G" * 12)
        self.compare([c, piped], {"A_B": None, "A-B": c, "Gene-x_mRNA": piped})

    def test_standalone_cli_writes_the_records_used_for_preview_and_folding(self):
        piped, plain = ("A|B", "GGGG"), ("A_B", "CCCC")
        tmp = self.project.parent
        sam = tmp / "empty.sam"  # take the SAM path: no mapper or folding installation needed
        sam.write_text("@HD\tVN:1.6\tSO:unsorted\n")
        env = dict(os.environ, PYTHONPATH=str(ROOT / "pipeline"))
        for records in ([piped, plain], [plain, piped]):
            for indent in ("", "\ufeff", "\t"):
                with self.subTest(records=records, indent=indent):
                    path = self.reference(records, pipe_indent=indent)
                    out = tmp / path.stem  # fresh output per case; no checkpoint reuse
                    cmd = [sys.executable, "-BS", "-m", "hyb2pipe", "-i", str(sam), "-d", str(path), "-o", str(out)]
                    result = subprocess.run(cmd, cwd=tmp, env=env, capture_output=True, text=True)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    lines = [">A_B", "GGGG", ">A-B", "CCCC"] if records[0] == piped else [">A-B", "CCCC", ">A_B", "GGGG"]
                    self.assertEqual(out.with_suffix(".reference.hyb.fasta").read_bytes(), os.linesep.join(lines).encode())
                    self.compare(records, {"A_B": piped, "A-B": plain}, path=path)

    def test_duplicate_effective_ids_fail_on_import_in_either_order(self):
        from hyb2pipe import inputs

        a, c = ("A-B", "A" * 12), ("A_B", "C" * 12)
        for records in ([a, c], [c, a]):
            records = records + [("last description|trigger", "G" * 12)]
            path = self.reference(records)
            with self.assertRaisesRegex(ValueError, "duplicate FASTA ID.*A-B"):
                samples.add_reference(self.project, path)
            with self.assertRaisesRegex(ValueError, "duplicate FASTA ID.*A-B"):
                inputs.needs_formatting(path)
            for browser in self.web(path, records, ["last"]):
                self.assertRegex(browser["error"], "Duplicate FASTA ID.*A-B")
        self.assertFalse(load_manifest(self.project)["references"])


if __name__ == "__main__":
    unittest.main()
