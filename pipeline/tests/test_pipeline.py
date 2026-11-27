"""Golden and unit tests for the Python port of hyb2 step 1.

The golden files were produced by the legacy ``sam_filt`` + ``sam2hyb -e 0``
on ``data/testData.sam`` (macOS one-true-awk 20200816, LANG=C.UTF-8).
"""
import gzip
import importlib.util
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE.parent))

from hyb2pipe import Options, awk, blast, collapse, energy, extsort, finalize, hybrids, inputs, run, sam, sam2hyb  # noqa: E402


class GoldenTest(unittest.TestCase):
    def test_testdata_sam_matches_legacy_byte_for_byte(self):
        with tempfile.TemporaryDirectory() as tmp:
            packed = Path(tmp) / "testData.sam.gz"
            packed.write_bytes(gzip.compress((ROOT / "data/testData.sam").read_bytes()))
            for source in (ROOT / "data/testData.sam", packed):
                with self.subTest(source=source.name):
                    result = run(source, ROOT / "data/Zika_18S_formatted.fasta", Path(tmp) / ("out-" + source.name) / "testData")
                    for produced, golden in ((result.hyb, "testData.hyb"), (result.homodimers, "testData_homodimers.hyb"),
                                             (result.stats, "testData_hybrids.hyb_stats.txt")):
                        self.assertEqual(produced.read_bytes(), (HERE / "golden" / golden).read_bytes(), golden)
                    self.assertEqual(result.counts["hybrids"], 10411)


class AwkTest(unittest.TestCase):
    def test_one_true_awk_iteration_order(self):
        # keys "1".."n" pushed on bucket heads; hash("1") = 49 so "2"..; growth at 101 keys
        array = awk.BwkArray()
        for i in range(1, 4):
            array[str(i)] = i
        self.assertEqual(list(array.keys()), ["2", "3", "1"])
        big = awk.BwkArray()
        for i in range(1, 150):
            big[str(i)] = i
        self.assertEqual(sorted(big.keys(), key=int), [str(i) for i in range(1, 150)])

    def test_comparisons_follow_awk_strnum_rules(self):
        self.assertEqual(awk.compare("10", "9"), 1)
        self.assertEqual(awk.compare("1.2e-14", "1.2E-14"), 0)
        self.assertEqual(awk.compare("abc", "abd"), -1)
        self.assertEqual(awk.compare_const(".", 5), -1)
        self.assertTrue(awk.truthy("x"))
        self.assertFalse(awk.truthy("0.0"))

    def test_sort_key_and_last_resort(self):
        lines = ["b\tAC\tx", "a\tAC\ty", "c\tAB\tz"]
        self.assertEqual(awk.sort_lines_by_field(lines, 2), ["c\tAB\tz", "b\tAC\tx", "a\tAC\ty"])

    def test_substr_clipping(self):
        self.assertEqual(awk.substr("ACGUACGU", 0, 3), "AC")
        self.assertEqual(awk.substr("ACGU", 3, 10), "GU")


class StageTest(unittest.TestCase):
    def test_sam_filt_splits_single_splice(self):
        line = "r1\t0\tg\t100\t40\t10S20M300N30M5S\t*\t0\t0\tSEQ\tQUAL\tAS:i:50\tNM:i:0"
        out = list(sam.sam_filt([line]))
        self.assertEqual(len(out), 2)
        self.assertEqual(out[0].split("\t")[5], "10S20M35S")
        self.assertEqual(out[1].split("\t")[1:6], ["256", "g", "420", "40", "30S30M5S"])

    def test_sam_filt_splice_arms_with_indels_match_legacy_script(self):
        # 3S10M2I8M1D5M | 200N | 25M4S on a 57-nt read: 24 reference and 28 read bases before the N
        lines = ["@SQ\tSN:g\tLN:5000"]
        for flag in ("0", "16"):
            lines.append("r%s\t%s\tg\t1000\t40\t3S10M2I8M1D5M200N25M4S\t*\t0\t0\t%s\t*\tAS:i:40\tNM:i:3"
                         % (flag, flag, "C" * 57))
        out = list(sam.sam_filt(lines))
        # what bin/sam_filt (one-true-awk 20200816) printed for these lines: each arm
        # keeps the other fields and gets the empty fields 14-20 the script printed
        tail = "\t*\t0\t0\t%s\t*\tAS:i:40\tNM:i:3" % ("C" * 57) + "\t" * 7
        self.assertEqual(out, ["@SQ\tSN:g\tLN:5000",
                               "r0\t0\tg\t1000\t40\t3S10M2I8M1D5M29S" + tail, "r0\t256\tg\t1224\t40\t28S25M4S" + tail,
                               "r16\t16\tg\t1000\t40\t3S10M2I8M1D5M29S" + tail, "r16\t272\tg\t1224\t40\t28S25M4S" + tail])
        rows = [line.split("\t") for line in out[1:]]
        blast_rows = [sam.blast_row(row, 5000).split("\t")[6:10] for row in rows]
        self.assertEqual(blast_rows, [["4", "28", "1000", "1023"], ["29", "53", "1224", "1248"],
                                      ["30", "54", "1023", "1000"], ["5", "29", "1248", "1224"]])

    def test_sam_filt_drops_double_splice_and_normalises_ids(self):
        self.assertEqual(list(sam.sam_filt(["r\t0\tg\t1\t1\t5M1N5M1N5M\t*"])), [])
        out = list(sam.sam_filt(["M1:7:FC:3:1101:1:2\t0\tg\t1\t40\t5M\t*"]))
        self.assertTrue(out[0].startswith("1101-1-2_1\t0\tg\t1"))

    def test_sam_filt_counts_skipped_records_by_kind(self):
        read = "ACGT" * 10
        rows = ["@SQ\tSN:toy\tLN:160",
                f"read_1\t0\ttoy\t1\t60\t20M20S\t*\t0\t0\t{read}\t*\tAS:i:40\tNM:i:0",
                f"read_1\t2048\ttoy\t41\t60\t20M20H\t*\t0\t0\t{read[:20]}\t*\tAS:i:40\tNM:i:0",
                f"read_1\t256\ttoy\t81\t60\t20S20M\t*\t0\t0\t{read}\t*\tAS:i:40\tNM:i:0",
                f"M1:7:FC:3:1101:1:2\t2064\ttoy\t1\t60\t20M\t*\t0\t0\t{read[:20]}\t*\tAS:i:20\tNM:i:0",
                f"dup\t1024\ttoy\t1\t60\t20M\t*\t0\t0\t{read[:20]}\t*\tAS:i:20\tNM:i:0",
                f"qc\t528\ttoy\t1\t60\t20M\t*\t0\t0\t{read[:20]}\t*\tAS:i:20\tNM:i:0",
                f"none\t4\t*\t0\t0\t*\t*\t0\t0\t{read}\t*"]
        stderr = io.StringIO()
        kept = list(sam.sam_filt(rows, stderr))
        self.assertEqual([row.split("\t")[1] for row in kept], ["SN:toy", "0", "256"])
        messages = stderr.getvalue().splitlines()
        self.assertEqual(len(messages), 3)
        self.assertIn("skipped 1 unmapped SAM records (flag 0x4)", messages[0])
        self.assertIn("warning: skipped 2 supplementary alignments (flag 0x800)", messages[1])
        self.assertIn("chimeric arms reported as supplementary alignments are not used", messages[1])
        self.assertIn("warning: skipped 2 SAM records whose flag is not 0, 16, 256 or 272", messages[2])
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "mixed.sam"
            path.write_text("\n".join(rows) + "\n")
            with mock.patch("sys.stderr", io.StringIO()):
                result = run(path, ROOT / "data/Zika_18S_formatted.fasta", Path(tmp) / "out")
            self.assertEqual(result.counts["hybrids"], 1)

    def test_chimera_types_drop_unmatched_intramolecular(self):
        base = ["id", "SEQ", ".", "G", "1", "10", "50", "60", "1e-5", "G", "11", "20", "50", "60", "1e-5", "", "0"]
        self.assertEqual(finalize.add_chimera_types(["\t".join(base)]), ["\t".join(base) + "\tType_8"])

    def test_collapse_keeps_one_line_per_sequence(self):
        lines = ["a_1\tAAA\t.", "b_1\tAAA\t.", "c_1\tCCC\t."]
        result = collapse.collapse_hyb(lines)
        self.assertEqual(sorted(line.split("\t")[1] for line in result), ["AAA", "CCC"])

    def test_reference_formatting(self):
        fasta = [">ENSG_1|GENE|protein_coding", "ACGU", "ACGU", ">X||y", "GG", ">Z|pseudogene", "AA", ">W|lncRNA", "CC"]
        self.assertEqual(inputs.format_reference(fasta),
                         [">ENSG-1_GENE_mRNA", "ACGUACGU", ">W_lncRNA", "CC"])

    def test_reference_files_follow_the_formatting_rules(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            # CRLF lines, a wrapped sequence and an empty biotype field (|| becomes _unknown_: dropped)
            for name, text, expected in (
                    ("wrapped", ">alpha|protein_coding\r\nGGGG\r\nAAAA\r\n>skip||kind\r\nCCCC\r\n>b|t\r\nUU\r\n",
                     [">alpha_mRNA", "GGGGAAAA", ">b_t", "UU"]),
                    ("ensembl", ">ENSG1_ENST1|Zika_virus|protein_coding\nAC\n", [">ENSG1-ENST1_Zika-virus_mRNA", "AC"])):
                with self.subTest(name=name):
                    source = base / (name + ".fa")
                    source.write_bytes(text.encode())
                    self.assertTrue(inputs.needs_formatting(source))
                    self.assertEqual(inputs.format_reference(inputs.iter_lines(source)), expected)
            for name, text, error in (("comment", ">a|x\nAC\n#note\nGU\n", "comment"),
                                      ("duplicate", ">a|x\nAC\n>a|x\nGU\n", "duplicate FASTA ID")):
                with self.subTest(name=name):
                    source = base / (name + ".fa")
                    source.write_text(text)
                    with self.assertRaises(ValueError) as caught:
                        run(ROOT / "data/testData.sam", source, base / name)
                    self.assertIn(error, str(caught.exception))
            with self.assertRaises(ValueError):
                inputs.needs_formatting(base / "duplicate.fa")
            # a '|' outside the headers does not rename the reference
            plain = base / "plain.fa"
            plain.write_text(">a\nAC\n;c|d\n>b\nGU\n")
            self.assertFalse(inputs.needs_formatting(plain))

    def test_fastq_collapse(self):
        fastq = ["@r1 x", "ACGT", "+", "IIII", "@r2", "ACGT", "+", "IIII", "@r3", "GGGG", "+", "IIII"]
        self.assertEqual(inputs.collapse_reads(inputs.fastq_to_rows(fastq)), [">1_2", "ACGT", ">2_1", "GGGG"])

    def test_energy_uses_plain_numbers(self):
        hyb = ["r_1\tACGU\t.\tg\t1\t4\t1\t4\t1e-3\tg\t5\t8\t5\t8\t1e-3\t"]
        out = energy.add_energies(hyb, [">g", "ACGUACGU"], folder=lambda pairs: [-9.5 for _ in pairs])
        self.assertEqual(out[0].split("\t")[2], "-9.50")


def _has_viennarna():
    try:
        import RNA  # noqa: F401
        return True
    except ImportError:
        return False


class ViennaEnergyTest(unittest.TestCase):
    @unittest.skipUnless(_has_viennarna(), "ViennaRNA Python package not installed")
    def test_energy_keeps_columns_and_adds_numeric_dg(self):
        golden = (HERE / "golden/testData.hyb").read_text().splitlines()[:200]
        stripped = []
        for line in golden:  # back to the pre-annotation 16-field shape
            cols = line.split("\t")
            stripped.append("\t".join(cols[:16]))
        out = energy.add_energies(stripped, inputs.iter_lines(ROOT / "data/Zika_18S_formatted.fasta"))
        self.assertEqual(len(out), len(stripped))
        for before, after in zip(stripped, out):
            a, b = before.split("\t"), after.split("\t")
            self.assertEqual(len(a), len(b))
            self.assertEqual(a[:2] + a[3:], b[:2] + b[3:])
            self.assertLess(float(b[2]), 0)

    @unittest.skipUnless(_has_viennarna(), "ViennaRNA Python package not installed")
    def test_parallel_folding_matches_serial(self):
        hyb = (HERE / "golden/testData.hyb").read_text().splitlines()[:600]
        pairs = [(a, b) for _, a, b in energy.bit_pairs(hyb, energy.reference_table(inputs.iter_lines(ROOT / "data/Zika_18S_formatted.fasta")))]
        self.assertEqual(energy.default_folder(3)(pairs), energy.default_folder()(pairs))


def _legacy_sam2hyb(sam_lines, reference_lines, options, folder=None):
    """The in-memory composition the streaming pipeline must reproduce."""
    blast_lines = list(sam.sam2blast(sam_lines))
    ranking = blast.reference_ranking(blast.mtophits(collapse.collapse_blast(blast_lines)))
    called = hybrids.mtop_hybrids(blast_lines, blast_threshold=options.blast_threshold,
                                  max_overlap=options.max_overlap, max_hits=options.max_hits)
    unique = hybrids.remove_duplicate_hybrids(ranking, hybrids.attach_sequences(called, sam_lines))
    if options.energy:
        unique = energy.add_energies(unique, reference_lines, folder=folder)
    final = finalize.add_chimera_types(finalize.add_overlaps(finalize.filter_homopolymers(collapse.collapse_hyb(unique))))
    return final, finalize.homodimers(final), finalize.hybrid_stats(final)


def _fake_folder(pairs):
    return [None if len(a) % 7 == 0 else -(len(a) * 3 + len(b)) / 4.0 for a, b in pairs]


class DuplexEnergyTest(unittest.TestCase):
    """The .hyb dG column from ViennaRNA's RNAduplex (the default engine)."""

    def _hyb(self):
        hyb = (HERE / "golden/testData.hyb").read_text().splitlines()[:200] \
            if (HERE / "golden/testData.hyb").exists() else None
        if not hyb:
            self.skipTest("no golden testData.hyb")
        return hyb

    def test_line_energies_do_not_depend_on_read_names(self):
        reference = list(inputs.iter_lines(ROOT / "data/Zika_18S_formatted.fasta"))
        lines = [line for line in self._hyb() if line and not line.startswith("#")][:60]
        renamed = ["read:%d/abc\t%s" % (i, line.split("\t", 1)[1]) for i, line in enumerate(lines)]
        fold = lambda pairs: [-(len(a) + len(b)) / 4.0 for a, b in pairs]  # noqa: E731
        with tempfile.TemporaryDirectory() as tmp:
            outs = []
            for name, body in (("a", lines), ("b", renamed)):
                src, dst = Path(tmp) / (name + ".tsv"), Path(tmp) / (name + ".out")
                src.write_text("".join(line + "\n" for line in body))
                energy.add_line_energies_file(src, dst, reference, fold, chunk=7)
                outs.append([line.split("\t")[2] for line in dst.read_text().splitlines()])
        self.assertEqual(outs[0], outs[1])
        self.assertTrue(all(value != "." for value in outs[0]), outs[0][:5])

    def test_each_distinct_arm_pair_is_folded_once(self):
        reference = list(inputs.iter_lines(ROOT / "data/Zika_18S_formatted.fasta"))
        lines = [line for line in self._hyb() if line and not line.startswith("#")][:40]
        # every hybrid three times under other names, spread over chunks
        body = ["r%d_%d\t%s" % (k, i, line.split("\t", 1)[1]) for k in range(3) for i, line in enumerate(lines)]
        calls = []

        def fold(pairs):  # order-sensitive, so a value given to the wrong pair shows
            calls.extend(pairs)
            return [-(len(a) * 7 + len(b) + sum(map(ord, a[:3]))) / 8.0 for a, b in pairs]

        table = energy.reference_table(reference)
        pairs = [(p[1], p[2]) for p in (energy.bit_pair(line, table) for line in body) if p]
        self.assertEqual(len(pairs), len(body))
        expected = ["%.2f" % value for value in fold(pairs)]
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "in.tsv"
            src.write_text("".join(line + "\n" for line in body))
            for limit in (energy._FOLD_CACHE_LIMIT, 30):  # 30: the cache is cleared mid-file
                calls.clear()
                dst = Path(tmp) / ("out%d.tsv" % limit)
                with mock.patch.object(energy, "_FOLD_CACHE_LIMIT", limit):
                    energy.add_line_energies_file(src, dst, reference, fold, chunk=17)
                self.assertEqual([line.split("\t")[2] for line in dst.read_text().splitlines()], expected)
                if limit > len(body):
                    self.assertEqual(sorted(calls), sorted(set(pairs)))

    def test_missing_vienna_stops_before_mapping(self):
        from unittest import mock
        with tempfile.TemporaryDirectory() as tmp, mock.patch.dict(sys.modules, {"RNA": None}), \
                mock.patch.object(energy.shutil, "which", lambda name: None):
            path = Path(tmp) / "in.sam"
            path.write_text((ROOT / "data/testData.sam").read_text()[:20000])
            with self.assertRaises(energy.EnergyUnavailable) as caught:
                sam2hyb(path, ROOT / "data/Zika_18S_formatted.fasta", Path(tmp) / "out/x", Options(energy=True))
            self.assertIn("RNAduplex", str(caught.exception))
            self.assertFalse((Path(tmp) / "out/x.work").exists() and any((Path(tmp) / "out/x.work").iterdir()))

    @unittest.skipUnless(_has_viennarna(), "ViennaRNA Python package not installed")
    def test_rnaduplex_values(self):
        pairs = [("GGGAAACCCUUUGGG", "CCCAAAGGGUUUCCC"), ("AAAAAAAA", "AAAAAAAA"), ("", "ACGU"),
                 ("GCGCGCATAT", "ATATGCGCGC")]
        values = energy.duplex_folder(1)(pairs)
        self.assertAlmostEqual(values[0], -28.1, places=1)
        self.assertIsNone(values[1])  # no pair (RNAduplex: ".&.", 100000)
        self.assertIsNone(values[2])  # empty arm: not folded
        self.assertAlmostEqual(values[3], -16.0, places=1)  # T read as U
        many = [(a, b) for a, b in pairs if a] * 300
        self.assertEqual(energy.duplex_folder(4)(many), energy.duplex_folder(1)(many))

    @unittest.skipUnless(_has_viennarna() and shutil.which("RNAduplex"), "needs ViennaRNA and the RNAduplex program")
    def test_rnaduplex_program_matches_the_module(self):
        pairs = [("GGGAAACCCUUUGGG", "CCCAAAGGGUUUCCC"), ("AAAAAAAA", "AAAAAAAA"), ("", "ACGU"),
                 ("GCGCGCATAT", "ATATGCGCGC")]
        self.assertEqual(energy._duplex_cli_chunk(shutil.which("RNAduplex"), pairs), energy.duplex_folder(1)(pairs))

    def test_unknown_engine_is_refused(self):
        with self.assertRaises(ValueError):
            energy.engine_folder("mfold")


def _fake_duplex_folder(workers=None):
    """Stand-in for RNAduplex in the run tests: dG = -(len(a) + len(b)) / 4."""
    return lambda pairs: [-(len(a) + len(b)) / 4.0 for a, b in pairs]


def _no_duplex_folder(workers=None):
    raise energy.EnergyUnavailable("the dG column needs the ViennaRNA Python package or RNAduplex on PATH")


class EnergyBackendTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.base = Path(tmp.name)
        self.ref = self.base / "reference.fa"
        self.ref.write_text(">toy\n" + "ACGT" * 40 + "\n")
        self.sam = self.base / "reads.sam"
        read = "ACGT" * 10
        self.sam.write_text("@SQ\tSN:toy\tLN:160\n"
                            f"read_1\t0\ttoy\t1\t60\t20M20S\t*\t0\t0\t{read}\t*\tAS:i:40\tNM:i:0\n"
                            f"read_1\t256\ttoy\t81\t60\t20S20M\t*\t0\t0\t{read}\t*\tAS:i:40\tNM:i:0\n")

    def test_original_positional_options_work_through_sam2hyb(self):
        args = (0.1, 4, 10, True, 2, "plain", self.base / "work", 8, False)
        options = Options(*args)
        self.assertEqual((options.blast_threshold, options.max_overlap, options.max_hits, options.energy,
                          options.threads, options.keep_sam, options.tmp_dir, options.memory_mb, options.resume), args)
        self.assertEqual(options.energy_engine, "rnaduplex")
        self.assertEqual(Options(*args, energy_engine="rnacofold").energy_engine, "rnacofold")
        with mock.patch.object(energy, "duplex_folder", side_effect=_fake_duplex_folder) as select:
            result = sam2hyb(self.sam, self.ref, self.base / "legacy", options)
        select.assert_called_once_with(2)
        self.assertEqual(result.counts["hybrids"], 1)

    def test_cli_run_and_resume_keep_the_selected_program_and_version(self):
        for engine, program in (("rnaduplex", "RNAduplex"), ("rnacofold", "RNAcofold")):
            with self.subTest(engine=engine):
                executable = str(self.base / program)
                options = Options(energy=True, threads=1, energy_engine=engine)
                out = self.base / engine
                checkpoint = Path(str(out) + ".checkpoint.json")

                def invoke(args, **kwargs):
                    self.assertEqual(args[0], executable)
                    if args[1:] == ["--version"]:
                        return subprocess.CompletedProcess(args, 0, program + " 2.7.3-test\n")
                    self.assertIn("--noconv", args)
                    self.assertIn("ACG", kwargs["input"])
                    return subprocess.CompletedProcess(args, 0, "((((&)))) (-3.50)\n")

                with mock.patch.dict(sys.modules, {"RNA": None}), \
                        mock.patch.object(energy.shutil, "which", return_value=executable) as which, \
                        mock.patch.object(energy.subprocess, "run", side_effect=invoke) as command:
                    result = run(self.sam, self.ref, out, options)
                which.assert_called_once_with(program)
                self.assertEqual(result.hyb.read_text().split("\t")[2], "-3.50")
                self.assertEqual(result.energy["engine"], engine)
                self.assertEqual(result.energy["executable"], executable)
                self.assertEqual(result.energy["version"], "2.7.3-test")
                self.assertEqual(command.call_count, 2)  # the version and the actual fold
                saved = json.loads(checkpoint.read_text())
                self.assertEqual(saved["stages"]["energy"]["provenance"], result.energy)
                with mock.patch.object(energy, "engine_folder", side_effect=AssertionError("selected on resume")), \
                        mock.patch.object(energy, "provenance", side_effect=AssertionError("probed on resume")):
                    resumed = run(self.sam, self.ref, out, options)
                    self.assertFalse(resumed.energy_ran)
                    self.assertEqual(resumed.energy, result.energy)
                    del saved["stages"]["energy"]["provenance"]
                    checkpoint.write_text(json.dumps(saved))
                    legacy = run(self.sam, self.ref, out, options)
                self.assertEqual(legacy.energy["engine"], engine)
                self.assertIsNone(legacy.energy["version"])
                self.assertIsNone(legacy.energy["executable"])


class DuplexRunTest(unittest.TestCase):
    """Result.energy and resuming across energy settings."""

    def test_provenance(self):
        record = energy.provenance("rnaduplex")
        self.assertEqual(record["engine"], "rnaduplex")
        self.assertEqual(record["program"], "ViennaRNA RNAduplex")
        if _has_viennarna():
            import RNA
            self.assertEqual(record["version"], RNA.__version__)

    def test_energy_settings_resume_earlier_stages(self):
        from unittest import mock
        from hyb2pipe import pipeline as pipeline_module
        sam_in, ref = ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta"
        with tempfile.TemporaryDirectory() as tmp, mock.patch.object(energy, "duplex_folder", _fake_duplex_folder):
            on = Options(energy=True)
            fresh = run(sam_in, ref, Path(tmp) / "fresh/x", on)
            expected = energy.provenance("rnaduplex")
            self.assertEqual(fresh.energy, expected)
            dg = [line.split("\t")[2] for line in fresh.hyb.read_text().splitlines() if not line.startswith("#")]
            self.assertTrue(dg and all(value.startswith("-") for value in dg), dg[:5])

            # an energy=False run interrupted in its last stage, its checkpoint
            # rewritten in the format from before energy_key
            out = Path(tmp) / "out/x"
            with mock.patch.object(pipeline_module, "_annotate", side_effect=KeyboardInterrupt):
                with self.assertRaises(KeyboardInterrupt):
                    run(sam_in, ref, out)
            ck_path = Path(str(out) + ".checkpoint.json")
            old = json.loads(ck_path.read_text())
            del old["energy_key"]
            old["options"]["energy"] = False
            ck_path.write_text(json.dumps(old))
            stages = []
            resumed = run(sam_in, ref, out, on, progress=lambda stage, detail: stages.append(stage))
            self.assertEqual(stages, ["energy", "annotate", "done"])
            self.assertEqual(resumed.hyb.read_bytes(), fresh.hyb.read_bytes())
            self.assertEqual(resumed.counts, fresh.counts)
            self.assertEqual(resumed.energy, expected)
            # a finished run reports the recorded provenance
            stages = []
            again = run(sam_in, ref, out, on, progress=lambda stage, detail: stages.append(stage))
            self.assertEqual(stages, ["done"])
            self.assertEqual(again.energy, expected)
            # energy off again: the work files are gone, so it reruns from the start
            stages = []
            off = run(sam_in, ref, out, progress=lambda stage, detail: stages.append(stage))
            self.assertIn("blast", stages)
            self.assertIsNone(off.energy)
            self.assertEqual(off.hyb.read_bytes(), (HERE / "golden/testData.hyb").read_bytes())

            # a changed option other than energy still restarts
            out2 = Path(tmp) / "out2/x"
            with mock.patch.object(pipeline_module, "_annotate", side_effect=KeyboardInterrupt):
                with self.assertRaises(KeyboardInterrupt):
                    run(sam_in, ref, out2)
            stages = []
            run(sam_in, ref, out2, Options(energy=True, max_hits=9), progress=lambda stage, detail: stages.append(stage))
            self.assertIn("blast", stages)
            self.assertIn("hybrids", stages)

    def test_resume_past_energy_needs_no_vienna(self):
        from unittest import mock
        from hyb2pipe import energy_stage_pending
        from hyb2pipe import pipeline as pipeline_module
        sam_in, ref = ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta"
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "out/x"
            on = Options(energy=True)
            self.assertTrue(energy_stage_pending(sam_in, ref, out, on))
            self.assertFalse(energy_stage_pending(sam_in, ref, out, Options()))
            # the energy stage finishes, then the run is interrupted in annotate
            with mock.patch.object(energy, "duplex_folder", _fake_duplex_folder), \
                    mock.patch.object(pipeline_module, "_annotate", side_effect=KeyboardInterrupt):
                with self.assertRaises(KeyboardInterrupt):
                    run(sam_in, ref, out, on)
            self.assertFalse(energy_stage_pending(sam_in, ref, out, Options(energy=True)))
            self.assertTrue(energy_stage_pending(sam_in, ref, out, Options(energy=True, resume=False)))
            self.assertTrue(energy_stage_pending(sam_in, ref, out, Options(energy=True, energy_engine="rnacofold")))
            self.assertTrue(energy_stage_pending(sam_in, ref, out, Options(energy=True, max_hits=9)))
            expected = energy.provenance("rnaduplex")
            with mock.patch.object(energy, "duplex_folder", _no_duplex_folder):
                stages = []
                resumed = run(sam_in, ref, out, Options(energy=True), progress=lambda stage, detail: stages.append(stage))
                self.assertEqual(stages, ["annotate", "done"])
                self.assertEqual(resumed.energy, expected)  # the recorded provenance
                self.assertFalse(resumed.energy_ran)
                again = run(sam_in, ref, out, Options(energy=True))
                self.assertEqual(again.energy, expected)
                self.assertFalse(again.energy_ran)
                self.assertFalse(energy_stage_pending(sam_in, ref, out, Options(energy=True)))
            dg = [line.split("\t")[2] for line in resumed.hyb.read_text().splitlines() if not line.startswith("#")]
            self.assertTrue(dg and all(value.startswith("-") for value in dg), dg[:5])

            # a fresh run without a dG engine still stops before mapping
            fresh = Path(tmp) / "fresh/x"
            stages = []
            with mock.patch.object(energy, "duplex_folder", _no_duplex_folder):
                with self.assertRaises(energy.EnergyUnavailable):
                    run(sam_in, ref, fresh, Options(energy=True), progress=lambda stage, detail: stages.append(stage))
            self.assertFalse(set(stages) & {"map", "blast", "hybrids", "energy", "annotate"}, stages)
            self.assertFalse(Path(str(fresh) + ".work").exists())
            with mock.patch.object(energy, "duplex_folder", _fake_duplex_folder):
                ran = run(sam_in, ref, fresh, on)
            self.assertTrue(ran.energy_ran)


class StreamingTest(unittest.TestCase):
    """The bounded-memory stages against the in-memory ports, with tiny
    memory budgets so every external sort spills to disk."""

    @classmethod
    def setUpClass(cls):
        cls.filtered = list(sam.sam_filt(inputs.iter_lines(ROOT / "data/testData.sam")))
        cls.reference = list(inputs.iter_lines(ROOT / "data/Zika_18S_formatted.fasta"))

    def _compare(self, lines, options=Options(), folder=None):
        from unittest import mock
        expected = _legacy_sam2hyb(lines, self.reference, options, folder)
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "in.sam"
            path.write_text("".join(line + "\n" for line in lines))
            with mock.patch.object(energy, "default_folder", lambda workers=None: folder):
                result = sam2hyb(path, ROOT / "data/Zika_18S_formatted.fasta", Path(tmp) / "out/x", options)
            got = [p.read_text().splitlines() for p in (result.hyb, result.homodimers, result.stats)]
        self.assertEqual(got[0], expected[0])
        self.assertEqual(got[1], expected[1])
        self.assertEqual(got[2], expected[2])
        return result

    def test_reads_out_of_order(self):
        # interleave blocks so reads reappear after their hybrids were called
        body = [line for line in self.filtered if not line.startswith("@")]
        header = [line for line in self.filtered if line.startswith("@")]
        blocks = [body[i:i + 5] for i in range(0, len(body), 5)]
        shuffled = header + [line for pair in zip(blocks[::2], blocks[1::2] + [[]]) for block in pair for line in block[::-1]]
        self._compare(shuffled[:40000], Options(memory_mb=1))

    def test_energy_in_chunks_with_repeated_ids(self):
        body = [line for line in self.filtered if not line.startswith("@")][:30000]
        header = [line for line in self.filtered if line.startswith("@")]
        # the same reads twice, far apart: repeated read IDs reach the energy step
        result = self._compare(header + body + body[:8000], Options(memory_mb=1, energy=True, energy_engine="rnacofold"), _fake_folder)
        self.assertGreater(result.counts["hybrids"], 0)

    def test_energy_file_matches_in_memory(self):
        hyb = (HERE / "golden/testData.hyb").read_text().splitlines()[:3000]
        lines = ["\t".join(line.split("\t")[:16]) for line in hyb]
        # IDs that collide with other lines' energy keys and lines without '_' in the ID
        lines += [lines[0], lines[5].replace("_", "", 1), "a_b_c" + lines[7][lines[7].index("\t"):]]
        expected = energy.add_energies(lines, self.reference, folder=_fake_folder)
        with tempfile.TemporaryDirectory() as tmp:
            src, dst = Path(tmp) / "u.tsv", Path(tmp) / "e.tsv"
            src.write_text("".join(line + "\n" for line in lines))
            energy.add_energies_file(src, dst, self.reference, Path(tmp), folder=_fake_folder, chunk=97)
            self.assertEqual(dst.read_text().splitlines(), expected)

    def test_collapse_file_matches_collapse(self):
        import random
        rng = random.Random(1)
        with tempfile.TemporaryDirectory() as tmp:
            for trial in range(200):
                blast_like = trial % 2 == 1
                lines = []
                for _ in range(rng.randint(0, 60)):
                    read = "r%d_%d" % (rng.randint(1, 8), rng.randint(1, 3))
                    seq = "".join(rng.choice("AC") for _ in range(rng.randint(1, 4)))
                    if blast_like:
                        lines.append("\t".join([read, rng.choice("gh"), "99", "1", "0", "0", "1", "5", str(rng.randint(1, 9)),
                                                "9", "1e-%d" % rng.randint(1, 3), "20.0", seq]))
                    else:
                        lines.append("\t".join([read, seq, ".", "g", str(rng.randint(1, 3)), "5", "", ""]))
                field, key = (13, collapse.blast_key) if blast_like else (2, collapse.hyb_key)
                src, dst = Path(tmp) / "in", Path(tmp) / "out"
                src.write_text("".join(line + "\n" for line in lines))
                collapse._collapse_file(src, dst, field, key, Path(tmp), 0.0005)
                self.assertEqual(dst.read_text().splitlines(), collapse._collapse(lines, field, key), trial)

    def test_fastq_collapse_spills_identically(self):
        import random
        rng = random.Random(2)
        with tempfile.TemporaryDirectory() as tmp:
            for trial in range(60):
                barcoded = trial % 3 == 0
                lines = []
                for i in range(rng.randint(0, 300)):
                    name = "@r%d" % i + ("_%s" % rng.choice(["AAC", "GT", "T"]) if barcoded else " x  y")
                    lines += [name, rng.choice(["", "#x", ">zz", "A", "AC", "ACG", "CA", "GG", "T"]), "+", "II"]
                    if rng.random() < 0.05:
                        lines.append("junk")
                if rng.random() < 0.2:
                    lines = lines[:-rng.randint(1, 3)]
                src, out = Path(tmp) / "a.fastq", Path(tmp) / "o.fa"
                src.write_text("".join(line + "\n" for line in lines))
                inputs.collapse_fastq(src, out, Path(tmp), memory_mb=0.0002 if trial % 2 else 100, workers=1)
                self.assertEqual(out.read_text().splitlines(), inputs.collapse_reads(inputs.fastq_to_rows(lines)), trial)

    def test_external_sort_merges_runs(self):
        import random
        rng = random.Random(3)
        records = ["".join(rng.choice("ab\0c") for _ in range(rng.randint(0, 6))) for _ in range(5000)]
        with tempfile.TemporaryDirectory() as tmp:
            self.assertEqual(list(extsort.sort_records(iter(records), Path(tmp), 0.001)), sorted(records))

    def test_resume_skips_finished_stages(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "out/testData"
            first = run(ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta", out)
            checkpoint = json.loads(Path(str(out) + ".checkpoint.json").read_text())
            self.assertTrue(all(stage["done"] for stage in checkpoint["stages"].values()))
            self.assertFalse(Path(str(out) + ".work").exists())
            stages = []
            again = run(ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta", out,
                        progress=lambda stage, detail: stages.append(stage))
            self.assertEqual(stages, ["done"])
            self.assertEqual(again.counts, first.counts)
            # a run interrupted in the last stage continues there
            from unittest import mock
            from hyb2pipe import pipeline as pipeline_module
            out2 = Path(tmp) / "out2/testData"
            with mock.patch.object(pipeline_module, "_annotate", side_effect=KeyboardInterrupt):
                with self.assertRaises(KeyboardInterrupt):
                    run(ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta", out2)
            stages = []
            resumed = run(ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta", out2,
                          progress=lambda stage, detail: stages.append(stage))
            self.assertEqual(stages, ["annotate", "done"])
            self.assertEqual(resumed.hyb.read_bytes(), (HERE / "golden/testData.hyb").read_bytes())
            self.assertEqual(resumed.counts, first.counts)
            # changed options invalidate the checkpoint
            stages = []
            run(ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta", out, Options(max_hits=9),
                progress=lambda stage, detail: stages.append(stage))
            self.assertIn("blast", stages)

    def test_keep_sam_options(self):
        with tempfile.TemporaryDirectory() as tmp:
            none = run(ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta", Path(tmp) / "a")
            self.assertIsNone(none.sam)
            plain = run(ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta", Path(tmp) / "b",
                        Options(keep_sam="plain", tmp_dir=Path(tmp) / "t"))
            self.assertEqual(plain.sam.read_text().splitlines(), self.filtered)
            packed = run(ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta", Path(tmp) / "c",
                         Options(keep_sam="gz"))
            import gzip
            self.assertEqual(gzip.decompress(packed.sam.read_bytes()), plain.sam.read_bytes())
            self.assertEqual(list((Path(tmp) / "t").iterdir()), [])


class Bowtie2StreamTest(unittest.TestCase):
    # The stand-in Bowtie2 is a shell script, which Windows cannot execute.
    @unittest.skipIf(sys.platform == "win32", "needs a POSIX shell script as the fake bowtie2")
    def test_killed_bowtie2_reports_exit_not_parse_error(self):
        import os
        import subprocess
        with tempfile.TemporaryDirectory() as tmp:
            fake = Path(tmp) / "bowtie2-align-s"
            fake.write_text("#!/bin/sh\nprintf '@HD\\tVN:1.0\\nread_1\\t0\\tgen'\nkill -9 $$\n")
            fake.chmod(0o755)
            old = os.environ.get("HYB2PIPE_TOOL_DIR")
            os.environ["HYB2PIPE_TOOL_DIR"] = tmp
            try:
                stream = inputs.Bowtie2Stream(Path(tmp) / "r.fa", Path(tmp) / "idx", threads=1)
                lines = []
                with self.assertRaises(subprocess.CalledProcessError):
                    for line in stream:
                        lines.append(line)
                stream.close()
            finally:
                if old is None:
                    os.environ.pop("HYB2PIPE_TOOL_DIR", None)
                else:
                    os.environ["HYB2PIPE_TOOL_DIR"] = old
            self.assertEqual(lines, ["@HD\tVN:1.0"])


class CliTest(unittest.TestCase):
    def test_rejects_unknown_input_type(self):
        from hyb2pipe.__main__ import main
        with tempfile.TemporaryDirectory() as tmp:
            bad = Path(tmp) / "x.txt"
            bad.write_text("")
            self.assertEqual(main(["-i", str(bad), "-d", str(ROOT / "data/Zika_18S_formatted.fasta"), "-o", str(Path(tmp) / "o")]), 2)

    def _main(self, *args):
        from hyb2pipe.__main__ import main
        err = io.StringIO()
        with mock.patch.object(sys, "stderr", err), mock.patch.object(sys, "stdout", io.StringIO()):
            status = main([str(a) for a in args])
        return status, err.getvalue()

    def test_nonfinite_thresholds_fail_before_changing_outputs(self):
        from hyb2pipe.__main__ import main
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            reference, reads, output = base / "ref.fa", base / "reads.sam", base / "out"
            reference.write_text(">toy\n" + "ACGT" * 40 + "\n")
            sequence = "ACGT" * 10
            reads.write_text("@SQ\tSN:toy\tLN:160\n"
                             f"read_1\t0\ttoy\t1\t60\t20M20S\t*\t0\t0\t{sequence}\t*\tAS:i:40\tNM:i:0\n"
                             f"read_1\t256\ttoy\t81\t60\t20S20M\t*\t0\t0\t{sequence}\t*\tAS:i:40\tNM:i:0\n")
            args = ["-i", str(reads), "-d", str(reference), "-o", str(output), "-p", "1"]
            status, err = self._main(*args, "-v", "1e-1", "-m", "4e0")
            self.assertEqual(status, 0, err)
            self.assertEqual(len(output.with_suffix(".hyb").read_text().splitlines()), 1)
            before = {path: path.read_bytes() for path in base.rglob("*") if path.is_file()}

            for option in ("-v", "-m"):
                for value in ("nan", "inf", "-inf", "1e309"):
                    for json_progress in (False, True):
                        with self.subTest(option=option, value=value, json_progress=json_progress):
                            stdout, stderr = io.StringIO(), io.StringIO()
                            flags = ["--json-progress"] if json_progress else []
                            with mock.patch.object(sys, "stdout", stdout), mock.patch.object(sys, "stderr", stderr):
                                status = main([*args, option + "=" + value, *flags])
                            self.assertEqual(status, 2)
                            if json_progress:
                                message = json.loads(stdout.getvalue())
                                self.assertEqual(message["type"], "error")
                                detail = message["error"]
                                self.assertEqual(stderr.getvalue(), "")
                            else:
                                self.assertEqual(stdout.getvalue(), "")
                                detail = stderr.getvalue()
                            self.assertIn(option, detail)
                            self.assertIn("finite number", detail)
                            after = {path: path.read_bytes() for path in base.rglob("*") if path.is_file()}
                            self.assertEqual(after, before)

    def test_truncated_gzip_is_an_input_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            packed = Path(tmp) / "reads.sam.gz"
            packed.write_bytes(gzip.compress((ROOT / "data/testData.sam").read_bytes())[:5000])
            status, err = self._main("-i", packed, "-d", ROOT / "data/Zika_18S_formatted.fasta", "-o", Path(tmp) / "o")
            self.assertEqual(status, 2)
            self.assertIn("error: %s: Compressed file ended" % packed, err)

    def test_warns_when_no_hybrids_are_found(self):
        with tempfile.TemporaryDirectory() as tmp:
            junk = Path(tmp) / "junk.sam"
            junk.write_text("not a sam\n")
            status, err = self._main("-i", junk, "-d", ROOT / "data/Zika_18S_formatted.fasta", "-o", Path(tmp) / "o")
            self.assertEqual(status, 0)
            self.assertIn("warning: no hybrids found in", err)

    @unittest.skipUnless(energy._vienna_version(), "ViennaRNA not installed")
    def test_warns_when_reference_lacks_a_gene_for_dg(self):
        with tempfile.TemporaryDirectory() as tmp:
            reads = Path(tmp) / "reads.sam"
            reads.write_text("".join((ROOT / "data/testData.sam").read_text().splitlines(keepends=True)[:4000]))
            full = ROOT / "data/Zika_18S_formatted.fasta"
            status, err = self._main("-i", reads, "-d", full, "-o", Path(tmp) / "a", "-e", 1)
            self.assertEqual((status, "warning" in err), (0, False))
            text = full.read_text()
            start = text.index(">18S_rRNA")
            no18s = Path(tmp) / "no18s.fasta"
            no18s.write_text(text[:start] + text[text.index(">", start + 1):])
            status, err = self._main("-i", reads, "-d", no18s, "-o", Path(tmp) / "b", "-e", 1)
            self.assertEqual(status, 0)
            rows = [line.split("\t") for line in (Path(tmp) / "b.hyb").read_text().splitlines()]
            missing = sum(1 for cols in rows if cols[2] == ".")
            self.assertGreater(missing, 0)
            self.assertIn("warning: %d of %d hybrids have no dG" % (missing, len(rows)), err)
            self.assertIn("These genes are not in the -d reference: 18S_rRNA.", err)

    def test_dg_warning_names_only_genes_missing_from_the_reference(self):
        from hyb2pipe.__main__ import _warnings
        with tempfile.TemporaryDirectory() as tmp:
            hyb = Path(tmp) / "x.hyb"
            row = ["r1_1", "AAAA", ".", "Zika_virusRNA", "1", "4", "1", "4", "1e-5", "18S_rRNA", "5", "8", "1", "4", "1e-5"]
            hyb.write_text("\t".join(row) + "\n" + "\t".join(["r2_1", "AAAA", "-5.00"] + row[3:]) + "\n")
            # both genes are in the reference the run used, whose IDs needed no
            # rewriting: a '.' there means no duplex, not a missing gene
            result = mock.Mock(hyb=hyb, counts={"hybrids": 2}, reference=ROOT / "data/Zika_18S_formatted.fasta")
            [message] = _warnings(result, True, Path("in.sam"))
            self.assertIn("1 of 2 hybrids have no dG", message)
            self.assertNotIn("not in the -d reference", message)
            self.assertIn("form no duplex", message)
            result.reference = Path(tmp) / "zika.fa"
            result.reference.write_text(">Zika_virusRNA\nACGU\n")
            [message] = _warnings(result, True, Path("in.sam"))
            self.assertIn("These genes are not in the -d reference: 18S_rRNA.", message)

    def test_dg_warning_reads_the_reference_only_when_a_dg_is_missing(self):
        from hyb2pipe.__main__ import _warnings
        with tempfile.TemporaryDirectory() as tmp:
            hyb = Path(tmp) / "ok.hyb"
            hyb.write_text("# header\n"
                           "r1_1\tAAAA\t-4.50\tGeneA\t1\t4\t1\t4\t1e-5\tGeneB\t5\t8\t1\t4\t1e-5\n"
                           "r2_1\tCCCC\t-6.10\tGeneA\t1\t4\t2\t5\t1e-5\tGeneA\t5\t8\t6\t9\t1e-5\n")
            result = mock.Mock(hyb=hyb, counts={"hybrids": 2}, reference=Path(tmp) / "does_not_exist.fasta")
            with mock.patch("hyb2pipe.inputs.iter_lines") as iter_lines, \
                    mock.patch("hyb2pipe.energy.reference_table") as reference_table:
                self.assertEqual(list(_warnings(result, True, Path("in.sam"))), [])
            iter_lines.assert_not_called()
            reference_table.assert_not_called()

    def test_integer_options_outside_their_range_exit_with_status_2(self):
        from hyb2pipe.__main__ import main
        base = ["-i", "reads.sam", "-d", "ref.fa", "-o", "out"]
        result = mock.Mock(hyb=Path("out.hyb"), homodimers=Path("out_homodimers.hyb"), stats=Path("out_stats.txt"),
                           sam=None, counts={"hybrids": 0})

        def accepted(*args):
            with mock.patch("hyb2pipe.__main__.run", return_value=result) as pipeline, \
                    mock.patch.object(sys, "stdout", io.StringIO()), mock.patch.object(sys, "stderr", io.StringIO()):
                self.assertEqual(main([*base, *args]), 0)
            return pipeline.call_args[0][3]

        defaults = accepted()
        self.assertEqual((defaults.threads, defaults.min_length, defaults.seed_length), (None, 18, 10))
        # both sides of every bound: --seed-length is Bowtie2's -L, which takes 4 to 31
        for option, field, rejected, allowed in (("--memory-mb", "memory_mb", ("-1", "0"), ("1",)),
                                                 ("-p", "threads", ("-1", "0"), ("1",)),
                                                 ("--min-length", "min_length", ("-1", "0"), ("1",)),
                                                 ("--seed-length", "seed_length", ("0", "3", "32"), ("4", "31"))):
            for value in allowed:
                with self.subTest(option=option, value=value):
                    self.assertEqual(getattr(accepted(option, value), field), int(value))
            for value in rejected:
                for json_progress in (False, True):
                    with self.subTest(option=option, value=value, json_progress=json_progress):
                        stdout, stderr = io.StringIO(), io.StringIO()
                        flags = ["--json-progress"] if json_progress else []
                        with mock.patch("hyb2pipe.__main__.run") as pipeline, mock.patch.object(sys, "stdout", stdout), \
                                mock.patch.object(sys, "stderr", stderr):
                            status = main([*base, option, value, *flags])
                        pipeline.assert_not_called()
                        self.assertEqual(status, 2)
                        if json_progress:
                            self.assertEqual(stderr.getvalue(), "")
                            detail = json.loads(stdout.getvalue())["error"]
                        else:
                            self.assertEqual(stdout.getvalue(), "")
                            detail = stderr.getvalue()
                        self.assertIn(option, detail)
                        self.assertIn("from 4 to 31" if option == "--seed-length" else "positive integer", detail)

    def test_scientific_blast_threshold(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "out"
            status, err = self._main("-i", ROOT / "data/testData.sam", "-d", ROOT / "data/Zika_18S_formatted.fasta",
                                     "-o", out, "-v", "1e-5")
            self.assertEqual(status, 0, err)
            self.assertEqual(len(Path(str(out) + ".hyb").read_text().splitlines()), 10013)  # 10411 with the default 0.1

    def test_paired_sam_records_are_an_error(self):
        read = "ACGT" * 10
        rows = ["@SQ\tSN:toy\tLN:160",
                f"read_1\t0\ttoy\t1\t60\t20M20S\t*\t0\t0\t{read}\t*\tAS:i:40\tNM:i:0",
                f"pair\t99\ttoy\t1\t60\t20M\t=\t81\t100\t{read[:20]}\t*\tAS:i:20\tNM:i:0"]
        message = ("sam_filt: SAM line 3 (read pair) has flag 99, which marks a paired-end read (0x1); "
                   "Hyb2 expects single-end reads, so map the reads as single-end")
        with self.assertRaises(ValueError) as caught:
            list(sam.sam_filt(rows, io.StringIO()))
        self.assertEqual(str(caught.exception), message)
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "paired.sam"
            path.write_text("\n".join(rows) + "\n")
            status, err = self._main("-i", path, "-d", ROOT / "data/Zika_18S_formatted.fasta", "-o", Path(tmp) / "py/out")
            self.assertEqual(status, 2)
            self.assertIn(message, err)
            self.assertFalse((Path(tmp) / "py/out.hyb").exists())

    def test_sam_read_names_without_a_count_or_starting_with_at_are_an_error(self):
        read = "ACGT" * 10
        hint = ("Give Hyb2 the reads instead (FASTA or FASTQ): it collapses identical reads and names them "
                "<rank>_<count> before mapping.")
        cases = {"read": 'read name "read" does not end in _<count> (as in 1_523), from which Hyb2 takes the number '
                         "of identical reads. " + hint,
                 "@read_2": 'read name "@read_2" starts with @, so its SAM lines read as header lines. ' + hint}
        with tempfile.TemporaryDirectory() as tmp:
            for number, (name, message) in enumerate(cases.items()):
                with self.subTest(name=name):
                    path = Path(tmp) / ("names%d.sam" % number)
                    path.write_text("@HD\tVN:1.0\n@SQ\tSN:toy\tLN:160\n@PG\tID:bowtie2\n@CO\tany text\n"
                                    "%s\t0\ttoy\t1\t60\t20M20S\t*\t0\t0\t%s\t*\tAS:i:40\tNM:i:0\n" % (name, read))
                    out = Path(tmp) / ("py%d" % number) / "out"
                    status, err = self._main("-i", path, "-d", ROOT / "data/Zika_18S_formatted.fasta", "-o", out)
                    self.assertEqual(status, 2)
                    self.assertIn(message, err)
                    self.assertFalse(Path(str(out) + ".hyb").exists())

    @unittest.skipUnless(inputs._tool("bowtie2-align-s", "bowtie2") and inputs._tool("bowtie2-build-s", "bowtie2-build"), "Bowtie2 not installed")
    def test_fasta_reads_are_collapsed_and_counted_unless_already_collapsed(self):
        # Raw FASTA reads have no <rank>_<count> name, so they are collapsed and
        # renamed as FASTQ reads are; a read named >@... (as solexa2fasta.awk
        # wrote them) gives the same .hyb, and reads already named so are mapped
        # as they are.  Trimming is off, so cutadapt is not needed.
        rows = [line.split("\t") for line in inputs.iter_lines(ROOT / "data/testData.sam") if not line.startswith("@")]
        names, copies = [], {}
        for row in rows:
            if row[1] == "0" and row[0] not in names and len(names) < 1500:
                names.append(row[0])
                copies[row[9]] = 1 + len(copies) % 3
        raw = [(seq, copy) for copy in range(3) for seq, count in copies.items() if copy < count]
        texts = {"plain": "".join(">r%d.%d\n%s\n" % (i, copy, seq) for i, (seq, copy) in enumerate(raw)),
                 "at": "".join(">@r%d.%d extra\n%s\n" % (i, copy, seq) for i, (seq, copy) in enumerate(raw)),
                 "collapsed": "".join(">%s\n%s\n" % item for item in zip(names, copies))}
        outputs = {}
        with tempfile.TemporaryDirectory() as tmp:
            for label, text in texts.items():
                reads = Path(tmp) / (label + ".fasta")
                reads.write_text(text)
                result = run(reads, ROOT / "data/Zika_18S_formatted.fasta", Path(tmp) / label / "out",
                             Options(threads=2, adapter=None))
                outputs[label] = result.hyb.read_bytes()
        self.assertEqual(outputs["at"], outputs["plain"])
        counted = 0
        for row in outputs["plain"].decode().splitlines():
            name, seq = row.split("\t")[:2]
            self.assertRegex(name, r"^[0-9]+_[0-9]+$")
            if seq in copies:
                self.assertEqual(int(name.split("_")[1]), copies[seq], row)
                counted += 1
        self.assertGreater(counted, 100)
        kept = {row.split("\t")[0] for row in outputs["collapsed"].decode().splitlines()}
        self.assertGreater(len(kept), 100)
        self.assertLessEqual(kept, set(names))

    # trim_adapters runs the cutadapt package of this Python, else cutadapt on the PATH
    @unittest.skipUnless(inputs._tool("bowtie2-align-s", "bowtie2") and inputs._tool("bowtie2-build-s", "bowtie2-build")
                         and (importlib.util.find_spec("cutadapt") or inputs._tool("cutadapt")), "needs Bowtie2 and cutadapt")
    def test_reads_are_adapter_trimmed_before_collapsing(self):
        # Reads ending in the 3' adapter give the same .hyb as the same reads
        # trimmed by hand and mapped without trimming; reads under 18 nt
        # without the adapter are dropped.
        adapter = inputs.MAPPING_DEFAULTS["adapter"]
        rows = [line.split("\t") for line in inputs.iter_lines(ROOT / "data/testData.sam") if not line.startswith("@")]
        reads = list(dict.fromkeys(row[9] for row in rows if row[1] == "0"))[:1500]
        raw = "".join(">r%d\n%s%s\n" % (i, seq, adapter) for i, seq in enumerate(reads))
        raw += ">short\nACGTACGTACGT%s\n" % adapter
        trimmed = "".join(">r%d\n%s\n" % (i, seq) for i, seq in enumerate(reads) if len(seq) >= 18)
        reference = ROOT / "data/Zika_18S_formatted.fasta"
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / "raw.fasta").write_text(raw)
            (base / "trimmed.fasta").write_text(trimmed)
            expected = run(base / "trimmed.fasta", reference, base / "by_hand/out", Options(threads=2, adapter=None))
            result = run(base / "raw.fasta", reference, base / "py/out", Options(threads=2))
            self.assertGreater(expected.counts["hybrids"], 0)
            self.assertEqual(result.hyb.read_bytes(), expected.hyb.read_bytes())
            self.assertEqual(result.counts["reads"], len(reads))  # the short read is gone
            self.assertIn("Reads written (passing filters):", (base / "py/out.cutadapt.log").read_text())

    @unittest.skipUnless(inputs._tool("bowtie2-align-s", "bowtie2") and inputs._tool("bowtie2-build-s", "bowtie2-build"), "Bowtie2 not installed")
    def test_fastq_path_maps_with_bowtie2(self):
        with tempfile.TemporaryDirectory() as tmp:
            reads = Path(tmp) / "reads.fastq"
            rows = [line.split("\t") for line in inputs.iter_lines(ROOT / "data/testData.sam") if not line.startswith("@")]
            seen, out = set(), []
            for row in rows:
                if row[1] == "0" and row[0] not in seen and len(seen) < 3000:
                    seen.add(row[0])
                    out += ["@" + row[0], row[9], "+", row[10]]
            reads.write_text("\n".join(out) + "\n")
            result = run(reads, ROOT / "data/Zika_18S_formatted.fasta", Path(tmp) / "fq", Options(threads=2))
            self.assertGreater(result.counts["hybrids"], 0)
            # the same output with another reference maps against that reference, not the old index
            renamed = Path(tmp) / "renamed.fasta"
            names = {"Zika_virusRNA": "geneC_mRNA", "18S_rRNA": "geneD_rRNA", "PAX6_mRNA": "geneE_mRNA"}
            text = (ROOT / "data/Zika_18S_formatted.fasta").read_text()
            for old, new in names.items():
                text = text.replace(">" + old, ">" + new)
            renamed.write_text(text)
            again = run(reads, renamed, Path(tmp) / "fq", Options(threads=2))
            self.assertGreater(again.counts["hybrids"], 0)
            hyb = again.hyb.read_text()
            self.assertIn("geneD_rRNA", hyb)
            self.assertEqual([old for old in names if old in hyb], [])

    def test_bowtie2_index_is_tied_to_the_reference_and_never_partial(self):
        with tempfile.TemporaryDirectory() as tmp:
            calls = []

            def build(args, **kwargs):
                calls.append(args)
                Path(args[2] + ".1.bt2").write_text(Path(args[1]).read_text())
                if len(calls) == 3:
                    raise subprocess.CalledProcessError(1, args)

            ref, prefix = Path(tmp) / "ref.fa", Path(tmp) / "out.index"
            with mock.patch.object(inputs, "_tool", return_value="bowtie2-build"), \
                    mock.patch.object(inputs.subprocess, "run", side_effect=build):
                ref.write_text(">a\nACGU\n")
                inputs.bowtie2_index(ref, prefix)
                inputs.bowtie2_index(ref, prefix)
                self.assertEqual(len(calls), 1)  # same reference: reused
                ref.write_text(">c\nGGCC\n")
                inputs.bowtie2_index(ref, prefix)
                self.assertEqual(len(calls), 2)  # other reference: rebuilt
                self.assertEqual(Path(str(prefix) + ".1.bt2").read_text(), ">c\nGGCC\n")
                ref.write_text(">d\nUUUU\n")
                with self.assertRaises(subprocess.CalledProcessError):
                    inputs.bowtie2_index(ref, prefix)  # interrupted build
                inputs.bowtie2_index(ref, prefix)
                self.assertEqual(len(calls), 4)  # the interrupted build is not reused
                self.assertEqual(Path(str(prefix) + ".1.bt2").read_text(), ">d\nUUUU\n")


if __name__ == "__main__":
    unittest.main()
