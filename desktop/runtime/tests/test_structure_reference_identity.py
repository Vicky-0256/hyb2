"""A HYB name identifies evidence; its reference match identifies the bases."""
import json
import tempfile
import unittest
from pathlib import Path

from desktop.runtime.bridge import handle
from desktop.runtime.project import load_manifest, new_project


ROOT = Path(__file__).resolve().parents[3]
SEQUENCES = {"A": "ACACUUACAGAAUCUG", "B": "AGGGGGGAAAAACCCCCCUGUACGUAGCAUCUUUGA",
             "C": "AUGUAAGUUCUG", "D": "GUGCAACCCCCCUUGGGGAACCGAACCUUGAC"}
REGIONS = [{"gene_id": "A", "start": 1, "end": 23}, {"gene_id": "C", "start": 4, "end": 24}]


class StructureReferenceIdentityTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.project = Path(tmp.name) / "names.hyb2proj"
        new_project(self.project, "Names")
        self.fasta = self.project / "inputs" / "ref.fa"
        self.fasta.write_text("".join(f">{name} description\n{seq}\n" for name, seq in SEQUENCES.items()))
        self.ref = self.call("add_reference", source=str(self.fasta))["reference"]["id"]
        hyb = self.project / "inputs" / "reads.hyb"
        # Only HYB A and C have evidence; B and D are FASTA IDs, not chimera names.
        hyb.write_text("intra_1\tACGU\t.\tA\t1\t6\t2\t7\t.\tA\t7\t12\t11\t16\t.\n"
                       "between_1\tACGU\t.\tA\t1\t6\t2\t7\t.\tC\t7\t12\t7\t12\t.\n")
        self.sample = self.call("add_sample", source=str(hyb), reference_id=self.ref, gene_id="A")["sample"]["id"]
        self.match("A", "B")
        self.match("C", "D")

    def call(self, operation, **fields):
        return handle(dict(operation=operation, project_path=str(self.project), **fields), repo_root=ROOT)

    def match(self, rna, sequence):
        self.call("set_reference_match", reference_id=self.ref, rna=rna, sequence=sequence)

    def fold(self, **fields):
        return self.call("structure_run", sample_ids=[self.sample], **fields)

    def assert_fold(self, result, gene, sequence, evidence_rows):
        self.assertTrue(result["ok"], result)
        paths = result["outputs"]
        header, bases, _ = Path(paths["dbn"]).read_text().splitlines()
        self.assertEqual(bases, sequence)
        self.assertTrue(header.startswith(">" + gene.split(" + ")[0] + ":"), header)
        meta = json.loads(Path(paths["meta"]).read_text())
        self.assertEqual((result["structure"]["gene_id"], meta["gene_id"]), (gene, gene))
        self.assertEqual(json.loads(Path(paths["arms"]).read_text())["records"][0]["rows"], evidence_rows)
        return meta

    def test_single_uses_the_previewed_bases_and_original_evidence_name(self):
        self.match("B", "C")  # a match names a physical record, not another lookup
        preview = self.call("read_reference_sequence", reference_id=self.ref, name="A")
        self.assertEqual((preview["name"], preview["sequence_name"], preview["sequence"], preview["length"]),
                         ("A", "B", SEQUENCES["B"], len(SEQUENCES["B"])))
        meta = self.assert_fold(self.fold(), "A", preview["sequence"], 1)
        self.assertEqual(meta["reference"]["length"], preview["length"])
        self.assertEqual(meta["windows"], [{"start": 1, "end": preview["length"]}])

    def test_implicit_single_record_does_not_mislabel_the_evidence(self):
        other = self.fasta.with_name("only.fa")
        other.write_text(">B\n" + SEQUENCES["B"] + "\n")
        ref = self.call("add_reference", source=str(other))["reference"]["id"]
        self.call("set_reference_match", reference_id=ref, rna="A", sequence="B")
        fields = {"reference": "inputs/only.fa", "hyb_paths": ["inputs/reads.hyb"]}
        self.assert_fold(self.call("structure_run", **fields), "A", SEQUENCES["B"], 1)
        with (self.project / "inputs" / "reads.hyb").open("a") as handle:
            handle.write("also_1\tACGU\t.\tB\t1\t6\t2\t7\t.\tB\t7\t12\t11\t16\t.\n")
        with self.assertRaisesRegex(ValueError, "give gene_id"):
            self.call("structure_run", **fields)

    def test_two_regions_resolve_independently_in_both_engines(self):
        self.match("B", "")  # unmatched HYB B does not invalidate A's target FASTA B
        for engine, mode, rows in (("cplcofold", "cofold", 2), ("comrades", "paired", 1)):
            with self.subTest(engine=engine):
                meta = self.assert_fold(self.fold(engine=engine, mode=mode, regions=REGIONS), "A + C",
                                        SEQUENCES["B"][:23] + "&" + SEQUENCES["D"][3:24], rows)
                self.assertEqual([{key: r[key] for key in ("gene_id", "start", "end")} for r in meta["regions"]], REGIONS)

    def test_explicitly_unmatched_is_not_an_exact_name_fallback(self):
        self.match("A", "")
        cases = [{}, {"engine": "cplcofold", "mode": "cofold"}, {"engine": "comrades", "mode": "paired"}]
        for fields in cases:
            if "mode" in fields:
                fields["regions"] = [dict(r, start=1, end=12) for r in REGIONS]
            with self.subTest(fields=fields), self.assertRaisesRegex(ValueError, "A is marked as not matched"):
                self.fold(**fields)
        with self.assertRaisesRegex(ValueError, "A is marked as not matched"):
            self.call("read_reference_sequence", reference_id=self.ref, name="A")

    def test_guided_paste_and_record_do_not_follow_a_second_match(self):
        self.match("B", "C")
        for source in ("paste", "record"):
            with self.subTest(source=source):
                seq = SEQUENCES["B"][:23]
                meta = self.assert_fold(self.fold(source=source, gene_id="input label", sequence=seq), "A", seq, 1)
                self.assertEqual(meta["sequence_source"]["mapped_to"], ["A:1-23"])
        first, second = SEQUENCES["B"][:23], SEQUENCES["D"][3:24]
        meta = self.assert_fold(self.fold(source="paste", engine="cplcofold", mode="cofold", gene_id="input label",
                                          sequence=first, sequence_two=second), "A + C", first + "&" + second, 2)
        self.assertEqual(meta["sequence_source"]["mapped_to"], ["A:1-23", "C:4-24"])

    def test_guided_search_counts_block_boundary_hits_once(self):
        from desktop.runtime.structure import _locate_strands

        short, long = SEQUENCES["D"][:11], SEQUENCES["B"][:23]
        split = 64 * 1024
        prefix = "A" * (split - len(short) - 8)
        bases = prefix + short + "CCC" + long
        # The short match fits in the previous block's overlap. The long one straddles the break.
        self.fasta.write_text(">B\n" + bases[:split] + "\n" + bases[split:] + "\n")
        inputs = {"reference": self.fasta, "matches": {"A": "B", "B": ""}, "hyb_paths": []}
        self.assertEqual(_locate_strands([short, long], inputs),
                         [("A", "B", len(prefix) + 1, len(prefix) + len(short)),
                          ("A", "B", len(bases) - len(long) + 1, len(bases))])
        with self.fasta.open("a") as handle:
            handle.write(">X\n" + short + "\n")
        self.assertEqual(_locate_strands([short, long], inputs), [])

    def test_guided_paste_does_not_guess_between_two_evidence_names(self):
        with (self.project / "inputs" / "reads.hyb").open("a") as out:
            out.write("another_1\tACGU\t.\tB\t1\t6\t2\t7\t.\tB\t7\t12\t11\t16\t.\n")
        paste = {"source": "paste", "sequence": SEQUENCES["B"], "gene_id": "label"}
        result = self.fold(**paste)
        self.assertTrue(result["ok"], result)
        self.assertEqual(result["structure"]["parameters"]["evidence"], "none")
        self.assertNotIn("sequence_mapped_to", result["structure"]["parameters"])
        with self.assertRaisesRegex(ValueError, "choose Reference region"):
            self.fold(engine="comrades", **paste)
        # Remove B as a possible evidence name; A now identifies the match unambiguously.
        self.match("B", "")
        self.assert_fold(self.fold(**paste), "A", SEQUENCES["B"], 1)

    def test_import_rejects_duplicate_effective_ids_without_registering_the_file(self):
        other = self.fasta.with_name("dups.fa")
        for headers, effective in ((["X one", "X two"], "X"), (["x_y one", "x-y two", "late|header"], "x-y")):
            other.write_text("".join(f">{header}\nACGU\n" for header in headers))
            with self.subTest(headers=headers), self.assertRaisesRegex(ValueError, f"duplicate FASTA ID.*{effective}"):
                self.call("add_reference", source=str(other))
        self.assertEqual([r["id"] for r in load_manifest(self.project)["references"]], [self.ref])

    def test_existing_file_is_checked_even_for_a_different_requested_id(self):
        # What was registered is no longer what's on disk. This duplicate is last.
        with self.fasta.open("a") as out:
            out.write(">D duplicate\nAAAAAAAAAAAA\n")
        for operation, fields in [("read_reference_sequence", {"reference_id": self.ref, "name": "A"}),
                                  ("structure_run", {}), ("structure_run", {"engine": "cplcofold", "mode": "cofold"}),
                                  ("structure_run", {"engine": "comrades", "mode": "paired"}),
                                  ("structure_run", {"source": "paste", "sequence": SEQUENCES["B"]})]:
            if operation == "structure_run":
                fields["sample_ids"] = [self.sample]
            if fields.get("mode") in ("cofold", "paired"):
                fields["regions"] = [dict(r, start=1, end=12) for r in REGIONS]
            with self.subTest(operation=operation, fields=fields), self.assertRaisesRegex(ValueError, "duplicate FASTA ID.*D"):
                self.call(operation, **fields)
        with self.assertRaisesRegex(ValueError, "duplicate FASTA ID.*D"):
            self.call("structure_run", reference="inputs/ref.fa", hyb_paths=["inputs/reads.hyb"])

    def test_automatic_name_and_different_explicit_reference(self):
        other = self.fasta.with_name("other.fa")
        other.write_text(">A\n" + SEQUENCES["A"] + "\n")
        self.assert_fold(self.fold(reference="inputs/other.fa"), "A", SEQUENCES["A"], 1)
        explicit = self.call("structure_run", reference="inputs/ref.fa", hyb_paths=["inputs/reads.hyb"], gene_id="A")
        self.assert_fold(explicit, "A", SEQUENCES["B"], 1)
        self.match("A", None)
        self.assert_fold(self.fold(), "A", SEQUENCES["A"], 1)


if __name__ == "__main__":
    unittest.main()
