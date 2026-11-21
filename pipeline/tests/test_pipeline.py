"""Golden and unit tests for the Python port of hyb2 step 1.

The golden files were produced by the legacy ``sam_filt`` + ``sam2hyb -e 0``
on ``data/testData.sam`` (macOS one-true-awk 20200816, LANG=C.UTF-8).
"""
import json
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE.parent))

from hyb2pipe import Options, awk, blast, collapse, energy, extsort, finalize, hybrids, inputs, run, sam, sam2hyb  # noqa: E402


class GoldenTest(unittest.TestCase):
    def test_testdata_sam_matches_legacy_byte_for_byte(self):
        with tempfile.TemporaryDirectory() as tmp:
            result = run(ROOT / "data/testData.sam", ROOT / "data/Zika_18S_formatted.fasta", Path(tmp) / "out/testData")
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
        self.assertEqual(out[0].split("\t")[5], "010S20M300S30S5S")
        self.assertEqual(out[1].split("\t")[1:4], ["256", "g", "430"])

    def test_sam_filt_drops_double_splice_and_normalises_ids(self):
        self.assertEqual(list(sam.sam_filt(["r\t0\tg\t1\t1\t5M1N5M1N5M\t*"])), [])
        out = list(sam.sam_filt(["M1:7:FC:3:1101:1:2\t0\tg\t1\t40\t5M\t*"]))
        self.assertTrue(out[0].startswith("1101-1-2_1\t0\tg\t1"))

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


if __name__ == "__main__":
    unittest.main()
