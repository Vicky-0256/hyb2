"""Golden and unit tests for the Python port of hyb2 step 1.

The golden files were produced by the legacy ``sam_filt`` + ``sam2hyb -e 0``
on ``data/testData.sam`` (macOS one-true-awk 20200816, LANG=C.UTF-8).
"""
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE.parent))

from hyb2pipe import Options, awk, collapse, energy, finalize, inputs, run, sam  # noqa: E402


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
