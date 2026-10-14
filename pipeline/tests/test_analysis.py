"""Tests for hyb2analysis: legacy-identical contact.txt/forTable, index and queries.

The golden ``testData.<gene>.contact.txt`` / ``.forTable.txt`` files were made
from ``golden/testData.hyb`` with the legacy commands under macOS
``/usr/bin/awk`` (one-true-awk 20200816)::

    plot_hybrids_3.awk USE_ENTIRE_HYBRIDS=1 BIN_SIZE=10 GENE_1=G GENE_2=G testData.hyb \
        | sed '2s/^/x\\ty\\tcount\\n/' > testData.G.contact.txt
    sort -k3,3nr testData.G.contact.txt | awk -v a=testData.G '!/^#/{print $1 "_" $2 "\\t" a "=" $3}'

The Zika files are large, so only their sha256 is kept.
"""
import hashlib
import random
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from hyb2analysis import coverage, hybfile, index, query  # noqa: E402
from hyb2pipe import awk  # noqa: E402

GOLDEN = HERE / "golden"
HYB = GOLDEN / "testData.hyb"
ZIKA_SHA256 = {
    "contact": "9373b0422f4465cae981a32fc88342b48412c884ab8b553b0e73d68a66f1f43f",
    "forTable": "7b35a78227b3dd3059ec968d49ce210707bbac66a43231f380786e1e8fcfe9ca",
}


def records():
    with open(HYB, "rb") as handle:
        return [r for r in (hybfile.parse(line) for line in handle) if r is not None]


def brute_contact(recs, gene, bin_size, measure="records", types=None):
    cells = {}
    gene = gene.encode()
    for r in recs:
        if r.gene1 != gene or r.gene2 != gene or (types and r.chimera_type.decode() not in types):
            continue
        weight = 1 if measure == "records" else r.reads
        for i in range(r.start1 // bin_size, r.end1 // bin_size + 1):
            for j in range(r.start2 // bin_size, r.end2 // bin_size + 1):
                cells[(i * bin_size, j * bin_size)] = cells.get((i * bin_size, j * bin_size), 0) + weight
    return {k: v for k, v in cells.items() if v}


def as_cells(result):
    return dict(zip(zip(result["x"], result["y"]), result["count"]))


class CoverageTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)

    def tearDown(self):
        self.tmp.cleanup()

    def test_contact_and_for_table_match_legacy(self):
        out = coverage.write_contact(HYB, "18S_rRNA", self.dir / "testData.18S_rRNA.contact.txt")
        self.assertEqual(out.read_bytes(), (GOLDEN / "testData.18S_rRNA.contact.txt").read_bytes())
        table = coverage.write_for_table(out, self.dir / "t.forTable.txt", name="testData.18S_rRNA")
        self.assertEqual(table.read_bytes(), (GOLDEN / "testData.18S_rRNA.forTable.txt").read_bytes())

    def test_large_gene_matches_legacy_hash(self):
        out = coverage.write_contact(HYB, "Zika_virusRNA", self.dir / "z.contact.txt")
        self.assertEqual(hashlib.sha256(out.read_bytes()).hexdigest(), ZIKA_SHA256["contact"])
        table = coverage.write_for_table(out, self.dir / "z.forTable.txt", name="testData.Zika_virusRNA")
        self.assertEqual(hashlib.sha256(table.read_bytes()).hexdigest(), ZIKA_SHA256["forTable"])

    def test_reference_port_agrees_with_fast_path(self):
        fast = list(coverage.contact_lines(HYB, "Zika_virusRNA"))
        slow = list(coverage.contact_lines(HYB, "Zika_virusRNA", exact_fallback_only=True))
        self.assertEqual(fast, slow)

    def test_absent_gene_has_no_header_line(self):
        out = coverage.write_contact(HYB, "NoSuchGene", self.dir / "n.contact.txt")
        self.assertEqual(out.read_bytes(), (GOLDEN / "testData.NoSuchGene.contact.txt").read_bytes())
        self.assertEqual(coverage.write_for_table(out, self.dir / "n.forTable.txt").read_bytes(), b"")

    def test_default_names_follow_the_shell_substitution(self):
        self.assertEqual(coverage.contact_path("/a/S.hyb", "G"), Path("/a/S.G.contact.txt"))
        self.assertEqual(coverage.for_table_name("/a/S.G.contact.txt"), "/a/S.G")

    def test_unusual_coordinates_use_the_exact_fallback(self):
        hyb = self.dir / "odd.hyb"
        hyb.write_text("a_1\tACGT\t.\tG\t1\t4\t25.5\t47\t1\tG\t5\t9\t3\t18\t1\n"
                       "b_2\tACGT\t.\tG\t1\t4\t12\t30\t1\tG\t5\t9\t1\t9\t1\n")
        lines = list(coverage.contact_lines(hyb, "G"))
        self.assertEqual(sorted(lines), sorted(coverage.contact_lines(hyb, "G", exact_fallback_only=True)))
        self.assertEqual(sum(int(line.split(b"\t")[2]) for line in lines), 3 * 2 + 3 * 1)

    def test_bwk_order_matches_awk_array(self):
        rng = random.Random(7)
        for total in (0, 1, 99, 100, 101, 401, 1601, 7000):
            keys, seen = [], set()
            while len(keys) < total:
                key = "%d\t%d" % (rng.randrange(0, 11000, 10), rng.randrange(0, 11000, 10))
                if key not in seen:
                    seen.add(key)
                    keys.append(key)
            array = awk.BwkArray()
            for key in keys:
                array[key] = 1
            order = coverage.bwk_order(coverage._hash_keys([k.encode() for k in keys]))
            self.assertEqual([keys[i] for i in order.tolist()], list(array.keys()), total)


class IndexQueryTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        cls.index = Path(cls.tmp.name) / "testData.hyb2idx"
        cls.meta = index.build(HYB, cls.index)
        cls.records = records()

    @classmethod
    def tearDownClass(cls):
        query.close_all()
        cls.tmp.cleanup()

    def test_meta_and_summary(self):
        self.assertEqual(self.meta["records"], 10411)
        self.assertEqual(self.meta["invalid"], 0)
        result = query.summary([self.index])
        merged = result["merged"]
        self.assertEqual(merged["records"], 10411)
        self.assertEqual(merged["reads"], sum(r.reads for r in self.records))
        self.assertEqual({g["name"] for g in merged["genes"]}, {"Zika_virusRNA", "18S_rRNA"})
        zika = next(g for g in merged["genes"] if g["name"] == "Zika_virusRNA")
        self.assertEqual(zika["length"], max(max(r.end1 for r in self.records if r.gene1 == b"Zika_virusRNA"),
                                             max(r.end2 for r in self.records if r.gene2 == b"Zika_virusRNA")))
        self.assertEqual(sum(t["records"] for t in merged["types"]), 10411)
        pair = next(p for p in merged["pairs"] if p["gene1"] == "Zika_virusRNA" and p["gene2"] == "18S_rRNA")
        self.assertEqual(pair["records"], 947)
        doubled = query.summary([self.index, self.index])["merged"]
        self.assertEqual(doubled["records"], 2 * 10411)
        self.assertEqual(doubled["genes"][0]["records"], 2 * merged["genes"][0]["records"])

    def test_contact_bins_at_10_equal_contact_txt(self):
        result = query.contact_bins([self.index], "18S_rRNA", 10)
        legacy = {}
        for line in (GOLDEN / "testData.18S_rRNA.contact.txt").read_text().splitlines()[2:]:
            x, y, c = map(int, line.split("\t"))
            legacy[(x, y)] = c
        self.assertEqual(as_cells(result), legacy)
        self.assertEqual(result["cells"], len(legacy))
        self.assertEqual(result["total"], 365)

    def test_contact_bins_any_bin_size_and_measure(self):
        for bin_size in (5, 7, 25, 100):
            for measure in ("records", "reads"):
                got = as_cells(query.contact_bins([self.index], "Zika_virusRNA", bin_size, measure=measure))
                self.assertEqual(got, brute_contact(self.records, "Zika_virusRNA", bin_size, measure), (bin_size, measure))

    def test_contact_bins_types_region_and_merge(self):
        full = brute_contact(self.records, "Zika_virusRNA", 10, types=["Type_1"])
        got = query.contact_bins([self.index], "Zika_virusRNA", 10, types=["Type_1"])
        self.assertEqual(as_cells(got), full)
        for bin_size in (1, 10):
            region = {"x": [2000, 3999], "y": [5000, 7999]}
            got = query.contact_bins([self.index], "Zika_virusRNA", bin_size, region)
            expected = {k: v for k, v in brute_contact(self.records, "Zika_virusRNA", bin_size).items()
                        if 2000 <= k[0] <= 3999 and 5000 <= k[1] <= 7999}
            self.assertEqual(as_cells(got), expected)
        merged = query.contact_bins([self.index, self.index], "Zika_virusRNA", 50)
        single = query.contact_bins([self.index], "Zika_virusRNA", 50)
        self.assertEqual(merged["count"], [2 * c for c in single["count"]])
        self.assertEqual(query.contact_bins([self.index], "Unknown", 10)["cells"], 0)

    def test_contact_bins_max_cells_coarsens(self):
        result = query.contact_bins([self.index], "Zika_virusRNA", 10, max_cells=500)
        self.assertTrue(result["adjusted"])
        self.assertLessEqual(result["cells"], 500)
        self.assertEqual(as_cells(result), brute_contact(self.records, "Zika_virusRNA", result["bin_size"]))

    def test_top_interactions(self):
        result = query.top_interactions([self.index, self.index], "Zika_virusRNA", 25, 10)
        cells = brute_contact(self.records, "Zika_virusRNA", 25)
        expected = sorted(cells.items(), key=lambda kv: (-kv[1], kv[0]))[:10]
        self.assertEqual([(r["x"], r["y"]) for r in result["rows"]], [k for k, _ in expected])
        self.assertEqual([r["count"] for r in result["rows"]], [2 * v for _, v in expected])
        self.assertEqual(result["rows"][0]["per_index"], [expected[0][1]] * 2)

    def test_hybrids_filters(self):
        recs = self.records
        result = query.hybrids([self.index], {"gene1": "Zika_virusRNA", "gene2": "Zika_virusRNA",
                                              "region": {"x": [3000, 3200]}}, limit=5)
        expected = [i for i, r in enumerate(recs, 1) if r.gene1 == r.gene2 == b"Zika_virusRNA" and r.end1 >= 3000 and r.start1 <= 3200]
        self.assertEqual(result["total"], len(expected))
        self.assertEqual([row["id"] for row in result["rows"]], expected[:5])
        first = result["rows"][0]
        line = HYB.read_text().splitlines()[expected[0] - 1].split("\t")
        self.assertEqual((first["name"], first["sequence"], first["start1"], first["type"]), (line[0], line[1], int(line[6]), line[17]))

        by_reads = query.hybrids([self.index], {"types": ["Type_2"], "min_reads": 2}, limit=20, offset=3, order="reads")
        expected = sorted(((r.reads, i) for i, r in enumerate(recs, 1) if r.chimera_type == b"Type_2" and r.reads >= 2),
                          key=lambda t: (-t[0], t[1]))
        self.assertEqual(by_reads["total"], len(expected))
        self.assertEqual([(row["reads"], row["id"]) for row in by_reads["rows"]], expected[3:23])

        both = query.hybrids([self.index, self.index], {"gene": "18S_rRNA"}, limit=4, offset=1, order="position",
                             with_sequence=False)
        self.assertEqual(both["total"], 2 * sum(1 for r in recs if b"18S_rRNA" in (r.gene1, r.gene2)))
        self.assertEqual([row["index"] for row in both["rows"]][:1], [1])  # same row from the second index comes next
        self.assertIsNone(both["rows"][0]["name"])
        self.assertEqual(query.hybrids([self.index], {"gene1": "Unknown"})["total"], 0)

    def test_bad_arguments(self):
        with self.assertRaises(ValueError):
            query.contact_bins([self.index], "Zika_virusRNA", 0)
        with self.assertRaises(ValueError):
            query.contact_bins([self.index], "Zika_virusRNA", 1)  # full map at 1 nt is too large
        with self.assertRaises(ValueError):
            query.hybrids([self.index], {}, limit=10**6)
        with self.assertRaises(ValueError):
            query.summary([Path(self.tmp.name) / "missing"])
        bogus = Path(self.tmp.name) / "bogus.hyb2idx"
        bogus.write_bytes(b"not sqlite")
        with self.assertRaises(ValueError):
            query.summary([bogus])

    def test_index_skips_comments_and_counts_invalid_lines(self):
        hyb = Path(self.tmp.name) / "mixed.hyb"
        good = HYB.read_text().splitlines()[0]
        hyb.write_text("# comment\n\n" + good + "\nbroken\tline\n" + good.replace("\t", "\t", 1) + "\n")
        meta = index.build(hyb, Path(self.tmp.name) / "mixed.hyb2idx")
        self.assertEqual((meta["records"], meta["invalid"], meta["skipped"]), (2, 1, 2))


def brute_pair(recs, gene, gene2, bin_size, both=False):
    """Cells of the (gene, gene2) map; ``both`` adds (gene2, gene) rows with swapped arms."""
    cells = {}
    g, g2 = gene.encode(), gene2.encode()
    for r in recs:
        if r.gene1 == g and r.gene2 == g2:
            spans = (r.start1, r.end1, r.start2, r.end2)
        elif both and g != g2 and r.gene1 == g2 and r.gene2 == g:
            spans = (r.start2, r.end2, r.start1, r.end1)
        else:
            continue
        for i in range(spans[0] // bin_size, spans[1] // bin_size + 1):
            for j in range(spans[2] // bin_size, spans[3] // bin_size + 1):
                key = (i * bin_size, j * bin_size)
                cells[key] = cells.get(key, 0) + 1
    return cells


class ProviderGapsTest(unittest.TestCase):
    """extents/lengths, orientation, fixed_bin and compare_bins (DesktopProvider needs)."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        cls.index = Path(cls.tmp.name) / "testData.hyb2idx"
        index.build(HYB, cls.index)
        cls.records = records()
        # a second "condition": only the first half of the file
        half = Path(cls.tmp.name) / "half.hyb"
        lines = HYB.read_text().splitlines(keepends=True)
        half.write_text("".join(lines[: len(lines) // 2]))
        cls.half = Path(cls.tmp.name) / "half.hyb2idx"
        index.build(half, cls.half)
        cls.half_records = [r for r in (hybfile.parse(line.encode()) for line in lines[: len(lines) // 2]) if r is not None]

    @classmethod
    def tearDownClass(cls):
        query.close_all()
        cls.tmp.cleanup()

    def expected_extents(self, recs):
        out = {}
        for r in recs:
            for gene, start, end in ((r.gene1, r.start1, r.end1), (r.gene2, r.start2, r.end2)):
                lo, hi = out.get(gene.decode(), (start, end))
                out[gene.decode()] = (min(lo, start), max(hi, end))
        return {k: {"min": lo, "max": hi} for k, (lo, hi) in out.items()}

    def test_summary_extents_and_reference_lengths(self):
        result = query.summary([self.index, self.half], lengths={"Zika_virusRNA": 10807})
        self.assertEqual(result["merged"]["extents"], self.expected_extents(self.records))
        self.assertEqual(result["indexes"][1]["extents"], self.expected_extents(self.half_records))
        genes = {g["name"]: g for g in result["merged"]["genes"]}
        self.assertEqual((genes["Zika_virusRNA"]["length"], genes["Zika_virusRNA"]["length_source"]), (10807, "reference"))
        self.assertEqual(genes["18S_rRNA"]["length"], genes["18S_rRNA"]["max_end"])
        self.assertEqual(genes["18S_rRNA"]["length_source"], "observed")
        with self.assertRaises(ValueError):
            query.summary([self.index], lengths={"Zika_virusRNA": "long"})

    def test_extents_fall_back_for_indexes_without_min_start(self):
        import shutil
        import sqlite3

        old = Path(self.tmp.name) / "old.hyb2idx"
        shutil.copy(self.index, old)
        con = sqlite3.connect(str(old))
        con.execute("ALTER TABLE genes DROP COLUMN min_start")
        con.commit()
        con.close()
        self.assertEqual(query.summary([old])["merged"]["extents"], self.expected_extents(self.records))

    def test_orientation_both_merges_swapped_pairs(self):
        for bin_size in (50, 200):
            oriented = query.contact_bins([self.index], "Zika_virusRNA", bin_size, gene2="18S_rRNA")
            both = query.contact_bins([self.index], "Zika_virusRNA", bin_size, gene2="18S_rRNA", orientation="both")
            self.assertEqual(as_cells(oriented), brute_pair(self.records, "Zika_virusRNA", "18S_rRNA", bin_size))
            self.assertEqual(as_cells(both), brute_pair(self.records, "Zika_virusRNA", "18S_rRNA", bin_size, both=True))
            self.assertEqual(both["orientation"], "both")
        swapped = sum(1 for r in self.records if r.gene1 == b"18S_rRNA" and r.gene2 == b"Zika_virusRNA")
        self.assertGreater(swapped, 0)  # the fixture has both orientations
        self.assertEqual(both["total"], oriented["total"] + swapped)
        # intramolecular maps keep the legacy (unmirrored) rule
        self.assertEqual(query.contact_bins([self.index], "Zika_virusRNA", 100, orientation="both")["count"],
                         query.contact_bins([self.index], "Zika_virusRNA", 100)["count"])
        top = query.top_interactions([self.index], "18S_rRNA", 100, 5, gene2="Zika_virusRNA", orientation="both")
        expected = sorted(brute_pair(self.records, "18S_rRNA", "Zika_virusRNA", 100, both=True).items(),
                          key=lambda kv: (-kv[1], kv[0]))[:5]
        self.assertEqual([(row["x"], row["y"], row["count"]) for row in top["rows"]], [(x, y, c) for (x, y), c in expected])
        with self.assertRaises(ValueError):
            query.contact_bins([self.index], "Zika_virusRNA", 10, orientation="normalised")

    def test_fixed_bin_ignores_max_cells(self):
        fixed = query.contact_bins([self.index], "Zika_virusRNA", 10, max_cells=500, fixed_bin=True)
        self.assertEqual((fixed["bin_size"], fixed["adjusted"], fixed["fixed_bin"]), (10, False, True))
        self.assertGreater(fixed["cells"], 500)
        with self.assertRaises(ValueError):  # the dense-grid guard still applies
            query.contact_bins([self.index], "Zika_virusRNA", 1, max_cells=10, fixed_bin=True)
        self.assertEqual(query.contact_bins([self.index], "Zika_virusRNA", 10, lengths={"Zika_virusRNA": 10807})["length"], 10807)

    def test_compare_bins_normalizes_per_million(self):
        result = query.compare_bins([self.index], [self.half], "Zika_virusRNA", 100)
        full = brute_contact(self.records, "Zika_virusRNA", 100)
        half = brute_contact(self.half_records, "Zika_virusRNA", 100)
        keys = sorted(set(full) | set(half))
        self.assertEqual(list(zip(result["x"], result["y"])), keys)
        self.assertEqual(result["a_count"], [full.get(k, 0) for k in keys])
        self.assertEqual(result["b_count"], [half.get(k, 0) for k in keys])
        lib_a, lib_b = len(self.records), len(self.half_records)
        self.assertEqual((result["library_a"], result["library_b"]), (lib_a, lib_b))
        self.assertAlmostEqual(result["pseudocount"], 1e6 / lib_b)
        import math
        for k, key in enumerate(keys[:200]):
            a = full.get(key, 0) * 1e6 / lib_a
            b = half.get(key, 0) * 1e6 / lib_b
            self.assertAlmostEqual(result["a"][k], a, places=5)
            self.assertAlmostEqual(result["b"][k], b, places=5)
            self.assertAlmostEqual(result["log2ratio"][k], math.log2((b + 1e6 / lib_b) / (a + 1e6 / lib_b)), places=5)
        self.assertEqual(result["normalization"], "per_million_records")
        same = query.compare_bins([self.index, self.index], [self.index], "Zika_virusRNA", 250)
        self.assertEqual(same["a"], same["b"])
        coarse = query.compare_bins([self.index], [self.half], "Zika_virusRNA", 10, max_cells=300)
        self.assertTrue(coarse["adjusted"])
        self.assertLessEqual(coarse["cells"], 300)
        fixed = query.compare_bins([self.index], [self.half], "Zika_virusRNA", 10, max_cells=300, fixed_bin=True)
        self.assertEqual(fixed["bin_size"], 10)
        region = query.compare_bins([self.index], [self.half], "Zika_virusRNA", 50, {"x": [1000, 2999], "y": [4000, 8999]})
        self.assertTrue(all(1000 <= x <= 2999 for x in region["x"]) and all(4000 <= y <= 8999 for y in region["y"]))
        empty = query.compare_bins([self.index], [self.half], "Missing", 50)
        self.assertEqual((empty["cells"], empty["x"]), (0, []))


if __name__ == "__main__":
    unittest.main()
