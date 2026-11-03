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
        self.assertEqual(coverage.contact_path("/data/run.hyb2/S.hyb", "G"), Path("/data/run.hyb2/S.G.contact.txt"))
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

    def test_index_path_with_uri_characters(self):
        # Folder names such as "ZIKV #2" or "50% subset" must not change
        # which file SQLite opens, or how (the index stays read-only).
        folder = Path(self.tmp.name) / "ZIKV #2 50% a?mode=rw&x"
        folder.mkdir()
        copy = folder / "testData.hyb2idx"
        copy.write_bytes(self.index.read_bytes())
        self.assertEqual(query.summary([copy])["merged"]["records"], self.meta["records"])
        con = query._get(copy).con
        with self.assertRaises(Exception):
            con.execute("CREATE TABLE x (a)")

    def test_rebuilt_index_keeps_the_old_connection_open(self):
        # A thread still querying the old _Index after a rebuild must not hit
        # "Cannot operate on a closed database".
        import os

        copy = Path(self.tmp.name) / "rebuilt.hyb2idx"
        copy.write_bytes(self.index.read_bytes())
        old = query._get(copy)
        query.hybrids([copy], {"search": "zika"}, limit=1)  # a cached search table on it
        stat = copy.stat()
        os.utime(copy, ns=(stat.st_atime_ns, stat.st_mtime_ns + 1_000_000_000))
        new = query._get(copy)
        self.assertIsNot(new, old)
        self.assertEqual(old.con.execute("SELECT count(*) FROM hybrids").fetchone()[0], self.meta["records"])
        self.assertFalse(any(entry[0] is old for entry in query._found_tables.values()))
        self.assertEqual(query.summary([copy])["merged"]["records"], self.meta["records"])

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

    def test_search_pages_reuse_the_matches(self):
        # the matches of a query are kept for every index, so the next page
        # does not reread each .hyb (they were kept for 8 indexes in all)
        copies = []
        for i in range(10):
            copy = Path(self.tmp.name) / ("search%d.hyb2idx" % i)
            copy.write_bytes(self.index.read_bytes())
            copies.append(copy)
        scans = []
        real, saved = query._scan_search, query._FOUND_ROWS
        query._scan_search = lambda index, text: scans.append(index.path) or real(index, text)
        try:
            first = query.hybrids(copies, {"search": "zika"}, limit=50)
            second = query.hybrids(copies, {"search": "zika"}, limit=50, offset=50)
            self.assertEqual(len(scans), 10)
            single = query.hybrids([self.index], {"search": "zika"}, limit=0)["total"]
            self.assertEqual((first["total"], second["total"]), (10 * single, 10 * single))
            self.assertEqual(first["rows"] + second["rows"], query.hybrids(copies, {"search": "zika"}, limit=100)["rows"])
            # over the bound, the latest query stays whole and older ones go
            query._FOUND_ROWS = 1
            query.hybrids(copies, {"search": "18s"}, limit=1)
            query.hybrids(copies, {"search": "18s"}, limit=1, offset=1, order="reads")
            self.assertEqual(len(scans), 21)  # zika on the index itself, then 18s on the copies
            query.hybrids(copies[:1], {"search": "zika"}, limit=1)
            self.assertEqual(len(scans), 22)
            tables = [query._get(c).con.execute("SELECT count(*) FROM sqlite_temp_master WHERE type = 'table'").fetchone()[0]
                      for c in copies]
            self.assertEqual(tables, [1] + [0] * 9)
        finally:
            query._scan_search, query._FOUND_ROWS = real, saved

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

    def test_hybrids_record_details_id_and_either(self):
        """The web drawer's details (read coordinates, type source, line) and
        the ``id`` and ``either`` filters."""
        lines = HYB.read_text().splitlines()
        rid = 7
        got = query.hybrids([self.index], {"id": rid}, limit=5, with_line=True)
        self.assertEqual(got["total"], 1)
        row = got["rows"][0]
        cols = lines[rid - 1].split("\t")
        self.assertEqual((row["id"], row["line"]), (rid, rid))
        self.assertEqual((row["read_start1"], row["read_end1"], row["read_start2"], row["read_end2"]),
                         tuple(float(cols[k]) for k in (4, 5, 10, 11)))
        self.assertEqual(row["type_source"], "column_17")
        self.assertNotIn("line", query.hybrids([self.index], {"id": rid}, limit=1)["rows"][0])
        self.assertEqual(query._type_source(b"a\tb\tc\td\te\tf\tg\th\ti\tj\tk\tl\tm\tn\to".split(b"\t")), "inferred")
        self.assertEqual(query._type_source(b"1\t2\t3\t4\t5\t6\t7\t8\t9\t10\t11\t12\t13\t14\t15\t\t7\tType_1".split(b"\t")),
                         "column_17")
        # either: both arm orders of an inter-RNA cell
        one = {"gene1": "Zika_virusRNA", "gene2": "18S_rRNA"}
        two = {"gene1": "18S_rRNA", "gene2": "Zika_virusRNA"}
        total = lambda f: query.hybrids([self.index], f, limit=0)["total"]
        self.assertEqual(total({"either": [one, two]}), total(one) + total(two))
        self.assertEqual(total({"either": [one, {"gene1": "Unknown"}]}), total(one))
        self.assertEqual(total({"either": [{"gene1": "Unknown"}]}), 0)
        self.assertEqual(total({"either": [one, {}]}), total({}))
        with self.assertRaises(ValueError):
            query.hybrids([self.index], {"either": {"gene1": "x"}})
        with self.assertRaises(ValueError):
            query.hybrids([self.index], {"id": "7"})

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
        self.assertEqual((meta["skipped_comment"], meta["skipped_blank"]), (1, 1))


    def test_an_endless_line_fails_instead_of_loading_whole(self):
        from hyb2analysis import hybfile
        hyb = Path(self.tmp.name) / "endless.hyb"
        good = HYB.read_text().splitlines()[0]
        hyb.write_bytes(good.encode() + b"\n" + b"A" * (hybfile.MAX_LINE + 10))
        target = Path(self.tmp.name) / "endless.hyb2idx"
        with self.assertRaisesRegex(ValueError, "line 2 is longer than"):
            index.build(hyb, target)
        self.assertEqual(list(Path(self.tmp.name).glob("endless.hyb2idx*")), [])
        with self.assertRaisesRegex(ValueError, "longer than"):
            list(coverage.contact_lines(hyb, "G", exact_fallback_only=True))


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

    def test_max_cells_skips_bins_too_large_to_build(self):
        # a bin whose dense grid is over MAX_GRID_CELLS used to raise before
        # max_cells could coarsen it (a 1 Mb RNA at 10 nt)
        saved = query.MAX_GRID_CELLS
        query.MAX_GRID_CELLS = 20_000
        try:
            with self.assertRaises(ValueError):
                query.contact_bins([self.index], "Zika_virusRNA", 1)
            result = query.contact_bins([self.index], "Zika_virusRNA", 1, max_cells=10**6)
            self.assertEqual(result["bin_size"], 128)  # the first doubling of 1 nt with at most 20,000 cells
            self.assertTrue(result["adjusted"])
            self.assertEqual(as_cells(result), brute_contact(self.records, "Zika_virusRNA", 128))
            coarse = query.contact_bins([self.index], "Zika_virusRNA", 1, max_cells=300)
            self.assertLessEqual(coarse["cells"], 300)
            self.assertEqual(as_cells(coarse), brute_contact(self.records, "Zika_virusRNA", coarse["bin_size"]))
            compared = query.compare_bins([self.index], [self.half], "Zika_virusRNA", 1, max_cells=10**6)
            self.assertEqual(compared["bin_size"], 128)
            plain = query.compare_bins([self.index], [self.half], "Zika_virusRNA", 128)
            for key in ("x", "y", "a", "b", "a_count", "b_count", "effect"):
                self.assertEqual(compared[key], plain[key], key)
            with self.assertRaises(ValueError):
                query.contact_bins([self.index], "Zika_virusRNA", 1, max_cells=10**6, fixed_bin=True)
        finally:
            query.MAX_GRID_CELLS = saved
        # the doubling stops at the largest bin the grid takes, not past it
        widest = query.contact_bins([self.index], "Zika_virusRNA", 40_000, max_cells=0)
        self.assertEqual(widest["bin_size"], 100_000)

    def test_top_interactions_per_index_counts(self):
        both = query.top_interactions([self.index, self.half, "%s" % self.index], "Zika_virusRNA", 50, 25)
        index, half = (query.top_interactions([path], "Zika_virusRNA", 50, 10_000) for path in (self.index, self.half))
        cells = {}
        for result in (index, half):
            for row in result["rows"]:
                cells.setdefault((row["x"], row["y"]), []).append(row["count"])
        own = {(r["x"], r["y"]): r["count"] for r in index["rows"]}
        part = {(r["x"], r["y"]): r["count"] for r in half["rows"]}
        for row in both["rows"]:
            key = (row["x"], row["y"])
            self.assertEqual(row["per_index"], [own.get(key, 0), part.get(key, 0), own.get(key, 0)])
            self.assertEqual(row["count"], sum(row["per_index"]))

    def test_load_more_reads_on_from_the_last_page(self):
        # each page used to fetch every row before its offset from every index
        both = [self.index, self.half]
        for order in ("file", "reads", "support", "position", "dg"):
            for filters in ({}, {"gene": "Zika_virusRNA", "min_reads": 2}, {"types": ["Type_1"]}):
                query.close_all()
                fresh = [query.hybrids(both, filters, 37, offset, order=order) for offset in (0, 37, 74, 200, 1000)]
                query.close_all()
                paged = [query.hybrids(both, filters, 37, offset, order=order) for offset in (0, 37, 74, 200, 1000)]
                self.assertEqual(paged, fresh, (order, filters))
                ends = next(iter(query._cursors.values()))["ends"]
                self.assertTrue({37, 74, 111} <= set(ends), (order, filters, sorted(ends)))
        # a cursor only reads on: a page before it still comes from the start
        self.assertEqual(query.hybrids(both, {}, 5, 3, order="dg"), (query.close_all(), query.hybrids(both, {}, 5, 3, order="dg"))[1])

    def test_compare_bins_normalizes_per_million(self):
        # the web Compare page: per-dataset per million, condition = mean of its datasets, pseudocount 1
        result = query.compare_bins([self.index], [self.half], "Zika_virusRNA", 100)
        full = brute_contact(self.records, "Zika_virusRNA", 100)
        half = brute_contact(self.half_records, "Zika_virusRNA", 100)
        keys = sorted(set(full) | set(half))
        self.assertEqual(list(zip(result["x"], result["y"])), keys)
        self.assertEqual(result["a_count"], [full.get(k, 0) for k in keys])
        self.assertEqual(result["b_count"], [half.get(k, 0) for k in keys])
        lib_a, lib_b = len(self.records), len(self.half_records)
        self.assertEqual((result["library_a"], result["library_b"]), (lib_a, lib_b))
        self.assertEqual((result["pseudocount"], result["normalisation"]), (1.0, "library"))
        import math
        for k, key in enumerate(keys[:200]):
            a = full.get(key, 0) * 1e6 / lib_a
            b = half.get(key, 0) * 1e6 / lib_b
            self.assertAlmostEqual(result["a"][k], a, places=5)
            self.assertAlmostEqual(result["b"][k], b, places=5)
            self.assertAlmostEqual(result["effect"][k], math.log2((a + 1) / (b + 1)), places=5)
            self.assertAlmostEqual(result["log2ratio"][k], -result["effect"][k], places=6)
        # two datasets on one side: the mean of their per-million values, not the CPM of their sum
        pair = query.compare_bins([self.index, self.half], [self.index], "Zika_virusRNA", 100)
        for k, key in enumerate(list(zip(pair["x"], pair["y"]))[:200]):
            mean = (full.get(key, 0) * 1e6 / lib_a + half.get(key, 0) * 1e6 / lib_b) / 2
            self.assertAlmostEqual(pair["a"][k], mean, places=5)
        raw = query.compare_bins([self.index], [self.half], "Zika_virusRNA", 100, normalisation="none")
        self.assertEqual((raw["pseudocount"], raw["a"][:50]), (0.5, [float(v) for v in raw["a_count"][:50]]))
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



def brute_viewpoint(recs, gene, lo, hi, width, partner=None, types=None, measure="records"):
    cov = [0.0] * (hi - lo + 1)
    records = reads = arms = 0
    partners = {}
    g = gene.encode()
    for r in recs:
        if types and r.chimera_type.decode() not in types:
            continue
        hit = None
        for own, other, s, e, mate in ((r.gene1, r.gene2, r.start1, r.end1, r.gene2), (r.gene2, r.gene1, r.start2, r.end2, r.gene1)):
            if own != g or (partner and other != partner.encode()) or s > hi or e < lo:
                continue
            arms += 1
            for p in range(max(s, lo), min(e, hi) + 1):
                cov[p - lo] += 1 if measure == "records" else r.reads
            hit = hit or mate.decode()
        if hit:
            records += 1
            reads += r.reads
            entry = partners.setdefault(hit, [0, 0])
            entry[0] += 1
            entry[1] += r.reads
    starts = list(range(lo, hi + 1, width))
    values = [sum(cov[s - lo:min(s + width - 1, hi) - lo + 1]) / (min(s + width - 1, hi) - s + 1) for s in starts]
    return records, reads, arms, starts, values, partners


def brute_region(recs, gene, lo, hi, partner=None, plo=None, phi=None, overlap="any", types=None, measure="support"):
    def ov(s, e, a, b):
        return (s >= a and e <= b) if overlap == "contained" else (s <= b and e >= a)
    g = gene.encode()
    span = hi - lo + 1
    width = 50 if span > 1000 else 25 if span > 500 else 10
    profile = [0] * (-(-span // width))
    hits, partners, groups = [], {}, {}
    for r in recs:
        if types and r.chimera_type.decode() not in types:
            continue
        forward = r.gene1 == g and ov(r.start1, r.end1, lo, hi) and (not partner or r.gene2 == partner.encode())
        reverse = r.gene2 == g and ov(r.start2, r.end2, lo, hi) and (not partner or r.gene1 == partner.encode())
        if plo is not None:
            forward = forward and ov(r.start2, r.end2, plo, phi)
            reverse = reverse and ov(r.start1, r.end1, plo, phi)
        if not (forward or reverse):
            continue
        hits.append(r)
        name = (r.gene2 if forward else r.gene1).decode()
        entry = partners.setdefault(name, [0, 0])
        entry[0] += 1
        entry[1] += r.reads
        for own, s, e in ((r.gene1, r.start1, r.end1), (r.gene2, r.start2, r.end2)):
            if own == g and s <= hi and e >= lo:
                for b in range((max(s, lo) - lo) // width, (min(e, hi) - lo) // width + 1):
                    profile[b] += {"records": 1, "support": r.support, "reads": r.reads}[measure]
        key = (r.start1, r.end1, r.start2, r.end2, r.chimera_type.decode(), r.gene1.decode(), r.gene2.decode())
        group = groups.setdefault(key, [0, 0])
        group[0] += 1
        group[1] += r.reads
    return hits, partners, profile, groups


class ViewpointRegionTest(unittest.TestCase):
    """analysis_viewpoint / analysis_region against brute force over the records."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        cls.index = Path(cls.tmp.name) / "testData.hyb2idx"
        index.build(HYB, cls.index)
        cls.records = records()
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

    def check_viewpoint(self, result, expected):
        records, reads, arms, starts, values, partners = expected
        self.assertEqual((result["records"], result["reads"], result["arms"]), (records, reads, arms))
        self.assertEqual(result["bins"]["start"], starts)
        for got, want in zip(result["bins"]["value"], values):
            self.assertAlmostEqual(got, want, places=3)
        self.assertEqual({p["name"]: [p["records"], p["reads"]] for p in result["partners"]}, partners)

    def test_viewpoint_matches_legacy_plot_viewpoint(self):
        # golden/testData_Zika_virusRNA.gplot: the legacy hyb2 step 2
        # (`plot_viewpoint -a Zika_virusRNA`: hyb2blast.awk + blast2gplot.pl)
        # on testData.hyb, from TomHarcus's python-migration branch
        # (fixtures/legacy_run/direct_viewpoint). It counts, per nucleotide,
        # the arms of the hybrids with both arms on the gene.
        lines = (GOLDEN / "testData_Zika_virusRNA.gplot").read_text().splitlines()
        self.assertEqual(lines[0], "#test_Zika_virusRNA_10807")
        legacy = [int(line.split("\t")[1]) for line in lines[1:]]
        result = query.viewpoint([self.index], "Zika_virusRNA", 1, 10807, partner="Zika_virusRNA")
        self.assertEqual(result["bins"]["start"], list(range(1, 10808)))
        self.assertEqual(result["bins"]["value"], legacy)

    def test_viewpoint_matches_brute_force(self):
        recs = self.records
        result = query.viewpoint([self.index], "Zika_virusRNA", 2000, 2600)
        self.check_viewpoint(result, brute_viewpoint(recs, "Zika_virusRNA", 2000, 2600, 1))
        self.assertEqual((result["start"], result["end"], result["bin_size"], result["adjusted"]), (2000, 2600, 1, False))
        result = query.viewpoint([self.index], "Zika_virusRNA", 100, 9000, bin_size=25, measure="reads", types=["Type_1"])
        self.check_viewpoint(result, brute_viewpoint(recs, "Zika_virusRNA", 100, 9000, 25, types=["Type_1"], measure="reads"))
        result = query.viewpoint([self.index], "18S_rRNA", 1, 1900, bin_size=10, partner="Zika_virusRNA")
        self.check_viewpoint(result, brute_viewpoint(recs, "18S_rRNA", 1, 1900, 10, partner="Zika_virusRNA"))
        merged = query.viewpoint([self.index, self.half], "Zika_virusRNA", 3000, 3400, bin_size=5)
        self.check_viewpoint(merged, brute_viewpoint(recs + self.half_records, "Zika_virusRNA", 3000, 3400, 5))
        # defaults: the observed start to the observed end (the web's); the
        # full mapped reference is 1 to the reference length
        whole = query.viewpoint([self.index], "Zika_virusRNA", lengths={"Zika_virusRNA": 10807})
        observed_end = max(max(r.end1 for r in recs if r.gene1 == b"Zika_virusRNA"),
                           max(r.end2 for r in recs if r.gene2 == b"Zika_virusRNA"))
        self.assertEqual((whole["start"], whole["end"]), (min(min(r.start1 for r in recs if r.gene1 == b"Zika_virusRNA"),
                                                              min(r.start2 for r in recs if r.gene2 == b"Zika_virusRNA")), observed_end))
        full = query.viewpoint([self.index], "Zika_virusRNA", 500, 600, lengths={"Zika_virusRNA": 10807}, full_reference=True)
        self.assertEqual((full["start"], full["end"]), (1, 10807))
        self.assertEqual(query.viewpoint([self.index], "Zika_virusRNA", full_reference=True)["bins"]["value"], [])
        # the bin grows past 30,000 bins
        wide = query.viewpoint([self.index], "Zika_virusRNA", 1, 90_000, lengths={"Zika_virusRNA": 90_000})
        self.assertTrue(wide["adjusted"])
        self.assertEqual(wide["bin_size"], 3)
        self.assertEqual(query.viewpoint([self.index], "Missing")["bins"]["value"], [])
        for bad in ({"measure": "x"}, {"bin_size": 0}, {"start": 10, "end": 5}, {"start": 1.5}):
            with self.assertRaises(ValueError, msg=bad):
                query.viewpoint([self.index], "Zika_virusRNA", **bad)

    def test_viewpoint_far_end_costs_no_memory(self):
        # an end far past the gene used to allocate one float per nucleotide up to it (80 GB here)
        huge = query.viewpoint([self.index], "Zika_virusRNA", 1, 10**10)
        width = -(-10**10 // query.MAX_PROFILE_BINS)
        self.assertEqual((huge["end"], huge["bin_size"], len(huge["bins"]["value"])), (10**10, width, query.MAX_PROFILE_BINS))
        records, _, _, _, per_nt, _ = brute_viewpoint(self.records, "Zika_virusRNA", 1, 11_000, 1)
        self.assertEqual(huge["records"], records)
        self.assertEqual(huge["bins"]["value"][0], sum(per_nt) / width)
        self.assertEqual(set(huge["bins"]["value"][1:]), {0.0})
        past = query.viewpoint([self.index], "Zika_virusRNA", 10**9, 10**10)
        self.assertEqual((past["records"], past["max"]), (0, 0.0))
        self.assertEqual(query.viewpoint([self.index], "Missing", 1, 10**10)["max"], 0.0)

    def test_viewpoint_values_keep_full_precision(self):
        # the web writes the mean per nucleotide unrounded (a 7 nt bin is not 4 decimals)
        result = query.viewpoint([self.index], "Zika_virusRNA", 1000, 1700, bin_size=7)
        *_, values, _ = brute_viewpoint(self.records, "Zika_virusRNA", 1000, 1700, 7)
        self.assertEqual(result["bins"]["value"], values)
        self.assertTrue(any(round(v, 4) != v for v in values))

    def check_region(self, result, recs, *args, **kwargs):
        hits, partners, profile, groups = brute_region(recs, *args, **kwargs)
        self.assertEqual(result["records"], len(hits))
        self.assertEqual(result["reads"], sum(r.reads for r in hits))
        self.assertEqual({p["name"]: [p["records"], p["reads"]] for p in result["partners"]}, partners)
        self.assertEqual(result["profile"]["value"], profile)
        # the strongest distinct interactions (ties at the cut may pick any of the tied rows)
        ranked = sorted(groups.values(), key=lambda v: (-v[1], -v[0]))
        got = [((r["start1"], r["end1"], r["start2"], r["end2"], r["type"], r["gene1"], r["gene2"]), [r["records"], r["reads"]])
               for r in result["rows"]]
        self.assertEqual([v for _, v in got], ranked[:len(got)])
        self.assertEqual(len({k for k, _ in got}), len(got))
        for key, value in got:
            self.assertEqual(groups[key], value, key)
        return groups

    def test_region_matches_brute_force(self):
        recs = self.records
        result = query.region([self.index], "Zika_virusRNA", 3000, 3200, limit=20)
        groups = self.check_region(result, recs, "Zika_virusRNA", 3000, 3200)
        self.assertEqual(result["distinct"], len(groups))
        self.assertEqual(result["support"], result["records"])  # 18-column sam2hyb lines: one per row
        reads = query.region([self.index], "Zika_virusRNA", 3000, 3200, limit=20, measure="reads")
        self.check_region(reads, recs, "Zika_virusRNA", 3000, 3200, measure="reads")
        self.assertEqual(len(result["rows"]), 20)
        self.assertTrue(result["rows_exact"])
        result = query.region([self.index], "Zika_virusRNA", 2000, 4000, partner="Zika_virusRNA", partner_start=5000,
                              partner_end=9000, overlap="contained", types=["Type_1", "Type_2"], limit=50)
        self.check_region(result, recs, "Zika_virusRNA", 2000, 4000, "Zika_virusRNA", 5000, 9000, "contained", ["Type_1", "Type_2"])
        result = query.region([self.index], "18S_rRNA", 1, 1900, partner="Zika_virusRNA")
        self.check_region(result, recs, "18S_rRNA", 1, 1900, "Zika_virusRNA")
        self.assertEqual(result["profile"]["bin_size"], 50)
        merged = query.region([self.index, self.half], "Zika_virusRNA", 1, 10807, limit=30)
        self.check_region(merged, recs + self.half_records, "Zika_virusRNA", 1, 10807)
        self.assertIsNone(merged["distinct"])
        self.assertEqual(query.region([self.index], "Missing", 1, 100)["records"], 0)
        for bad in ({"start": 5, "end": 1}, {"start": 1, "end": 5, "partner_start": 1}, {"start": 1, "end": 5, "partner_start": 1, "partner_end": 9},
                    {"start": 1, "end": 5, "overlap": "x"}, {"start": None, "end": 5}, {"start": 1, "end": 5, "limit": 10**6}):
            with self.assertRaises(ValueError, msg=bad):
                query.region([self.index], "Zika_virusRNA", **bad)

if __name__ == "__main__":
    unittest.main()


class SupportTest(unittest.TestCase):
    """Cluster support as the web parser counts it (web/parser.worker.js parseAnnotations)."""

    ARMS = "\t".join(["seq", ".", "G", "1", "20", "{s1}", "{e1}", "1e-5", "G", "21", "40", "{s2}", "{e2}", "1e-5"])

    def line(self, rid, tail, s1=1, e1=20, s2=101, e2=120):
        return (rid + "\t" + self.ARMS.format(s1=s1, e1=e1, s2=s2, e2=e2) + tail + "\n").encode()

    def test_layouts(self):
        cases = [
            ("\t\t7\tType_1", 1, 7),           # sam2hyb 18 columns
            ("\t6\tType_1", 1, 6),             # 17 columns
            ("\t4", 4, None),                  # legacy clustered: column 16 count
            ("\tcount_total=3", 3, None),       # legacy count_total metadata
            ("\t2.5", 1, 2.5),                 # partial: column 16 is an overlap score
            ("\t0", 1, 0),
            ("", 1, None),                     # 15 columns
        ]
        for tail, support, overlap in cases:
            record = hybfile.parse(self.line("r_2", tail))
            self.assertEqual((record.support, record.overlap), (support, overlap), tail)
            self.assertEqual(record.reads, 2)
        for tail in ("\tcount_total=abc", "\tcount_total=0", "\t99999999999999999999"):
            with self.assertRaises(ValueError, msg=tail):
                hybfile.parse(self.line("r_1", tail))

    def test_interaction_filters(self):
        """The web's Interactions filters: homodimer proxy, search, min support."""
        with tempfile.TemporaryDirectory() as tmp:
            hyb = Path(tmp) / "f.hyb"
            other = "\t".join(["seq", ".", "G", "1", "20", "1", "20", "1e-5", "H", "21", "40", "5", "30", "1e-5"])
            hyb.write_bytes(self.line("a_5", "\t4") + self.line("b_1", "\tcount_total=3", 11, 30)
                            + self.line("c_2", "\t\t7\tType_1") + ("d_1\t" + other + "\t\t9\tInter\n").encode())
            path = Path(tmp) / "f.hyb2idx"
            index.build(hyb, path)

            def ids(filters, order="file"):
                return [r["name"] for r in query.hybrids([path], filters, order=order)["rows"]]
            self.assertEqual(ids({"homodimer": "only"}), ["c_2"])          # same RNA, overlap 7
            self.assertEqual(ids({"homodimer": "exclude"}), ["a_5", "b_1", "d_1"])
            self.assertEqual(ids({"min_support": 3}), ["a_5", "b_1"])
            # the web's RNA + partner match either arm order; gene1 is arm 1 only
            self.assertEqual(ids({"gene1": "H"}), [])
            self.assertEqual(ids({"gene": "H"}), ["d_1"])
            self.assertEqual(ids({"partner": "H"}), ["d_1"])
            self.assertEqual(ids({"gene": "H", "partner": "G"}), ["d_1"])
            self.assertEqual(ids({"gene": "G", "partner": "G"}), ["a_5", "b_1", "c_2"])
            self.assertEqual(ids({"gene": "G", "partner": "nope"}), [])
            # with arm positions too (web/analysis-data.js filterRecords): the
            # RNA stays on either arm, each range applies to its own arm
            self.assertEqual(ids({"gene": "H", "region": {"x": [1, 20]}}), ["d_1"])
            self.assertEqual(ids({"gene": "H", "region": {"y": [1, 4]}}), [])
            self.assertEqual(ids({"gene": "H", "partner": "G", "region": {"x": [15, 20], "y": [5, 10]}}), ["d_1"])
            self.assertEqual(ids({"gene": "G", "region": {"y": [101, 101]}}), ["a_5", "b_1", "c_2"])
            self.assertEqual(ids({"partner": "G", "region": {"x": [25, 30]}}), ["b_1"])
            self.assertEqual(ids({}, "support"), ["a_5", "b_1", "c_2", "d_1"])
            self.assertEqual(query.hybrids([path], {}, order="support")["rows"][0]["support"], 4)
            # id, sequence, RNA names, type and overlap, joined by spaces, any case
            self.assertEqual(ids({"search": "C_2"}), ["c_2"])
            self.assertEqual(ids({"search": " g h "}), ["d_1"])
            self.assertEqual(ids({"search": "inter 9"}), ["d_1"])
            self.assertEqual(ids({"search": "type_1"}), ["a_5", "b_1", "c_2"])   # a and b inferred
            self.assertEqual(ids({"search": "   "}), ["a_5", "b_1", "c_2", "d_1"])
            self.assertEqual(query.hybrids([path], {"search": "seq"}, limit=0)["total"], 4)
            # the contact map and viewpoint subsets: only + exclude = all
            for measure in ("records", "support", "reads"):
                maps = {h: as_cells(query.contact_bins([path], "G", 10, measure=measure, homodimer=h)) for h in (None, "only", "exclude")}
                for cell, value in maps[None].items():
                    self.assertEqual(maps["only"].get(cell, 0) + maps["exclude"].get(cell, 0), value, (measure, cell))
                views = {h: query.viewpoint([path], "G", 1, 140, measure=measure, homodimer=h) for h in (None, "only", "exclude")}
                for i, value in enumerate(views[None]["bins"]["value"]):
                    self.assertAlmostEqual(views["only"]["bins"]["value"][i] + views["exclude"]["bins"]["value"][i], value)
            self.assertEqual(query.contact_bins([path], "G", 10, homodimer="only")["total"], 1)
            self.assertEqual(query.contact_bins([path], "G", 10, gene2="H", homodimer="only")["cells"], 0)
            self.assertEqual(query.viewpoint([path], "G", homodimer="only")["records"], 1)
            self.assertEqual([p["name"] for p in query.viewpoint([path], "G", homodimer="exclude")["partners"]], ["G", "H"])
            for call in (lambda: query.contact_bins([path], "G", 10, homodimer="all"),
                         lambda: query.viewpoint([path], "G", homodimer="both")):
                with self.assertRaises(ValueError):
                    call()
            for bad in ({"homodimer": "yes"}, {"search": 3}):
                with self.assertRaises(ValueError, msg=bad):
                    query.hybrids([path], bad)
            # search needs the .hyb the index was built from
            hyb.write_bytes(hyb.read_bytes() + b"\n")
            with self.assertRaises(ValueError):
                query.hybrids([path], {"search": "new"})
            query.close_all()

    def test_import_check_counts(self):
        """The web's Import check: skipped comment and blank lines apart,
        standard 17-column records, legacy column-16 counts, per-index parts;
        an index built before the split reports None for them."""
        import json
        import sqlite3
        with tempfile.TemporaryDirectory() as tmp:
            hyb = Path(tmp) / "f.hyb"
            hyb.write_bytes(b"# head\n  # indented\n\n \n" + self.line("a_5", "\t4") + self.line("b_1", "\tcount_total=3")
                            + self.line("c_2", "\t\t7\tType_1") + self.line("d_2", "\t6\tType_1") + self.line("e", "")
                            + b"bad\tline\n")
            path = Path(tmp) / "f.hyb2idx"
            meta = index.build(hyb, path)
            self.assertEqual((meta["skipped"], meta["skipped_comment"], meta["skipped_blank"]), (4, 2, 2))
            self.assertEqual((meta["canonical_records"], meta["legacy_column16"]), (2, 1))
            view = query.overview([path, path])
            self.assertEqual((view["skipped_comment"], view["skipped_blank"], view["canonical_records"],
                              view["legacy_column16"], view["read_records"]), (4, 4, 4, 2, 8))
            self.assertEqual(view["indexes"], [{"records": 5, "invalid": 1, "homodimer_records": 2}] * 2)
            part = query.summary([path], pairs=False)["indexes"][0]
            self.assertEqual((part["unique_rnas"], part["unique_pairs"], part["records_with_reads"]), (1, 1, 4))
            query.close_all()
            con = sqlite3.connect(path)
            con.execute("DELETE FROM meta WHERE key IN ('skipped_comment', 'skipped_blank', 'canonical_records', 'legacy_column16')")
            con.commit()
            con.close()
            old = query.overview([path])
            self.assertEqual((old["skipped"], old["skipped_comment"], old["canonical_records"]), (4, None, None))
            self.assertEqual(json.loads(json.dumps(old))["legacy_column16"], None)
            query.close_all()

    def test_names_sort_as_the_web(self):
        """Ties sort as the web's localeCompare (ICU root): punctuation before
        digits before letters; accents, then lower before upper case, break ties."""
        web = ["a b", "a_b", "a.b", "ab", "Ab", "AB", "e1", "E1", "é1", "hsa-mir-21", "hsa-miR-21-5p",
               "RNA_1", "RNA-1", "rna1", "Rna1", "RNA1", "RNA10", "RNA2", "z_9", "Z9"]
        self.assertEqual(sorted(reversed(web), key=query._name_key), web)

    def test_unsafe_integers_and_empty_regions(self):
        """Numbers past 2^53 are handled as the web does instead of failing the
        build; an empty region needs no grid however large it is."""
        with tempfile.TemporaryDirectory() as tmp:
            hyb = Path(tmp) / "f.hyb"
            big = "99999999999999999999"
            hyb.write_bytes(self.line("a_5", "\t4") + self.line("b_" + big, "\t4")
                            + self.line("c_1", "\t4", e1=big))
            path = Path(tmp) / "f.hyb2idx"
            meta = index.build(hyb, path)
            self.assertEqual((meta["records"], meta["invalid"]), (2, 1))   # c: coordinate; b: no read count
            self.assertEqual(query.top_interactions([path], "G", 1, region={"x": [5_000_000, 10_000_000],
                                                                           "y": [5_000_000, 10_000_000]})["rows"], [])
            query.close_all()

    def test_index_support_and_old_index_fallback(self):
        import sqlite3
        with tempfile.TemporaryDirectory() as tmp:
            hyb = Path(tmp) / "legacy.hyb"
            hyb.write_bytes(self.line("a_5", "\t4") + self.line("b_1", "\tcount_total=3", 11, 30)
                            + self.line("c_2", "\t\t7\tType_1"))
            path = Path(tmp) / "legacy.hyb2idx"
            meta = index.build(hyb, path)
            self.assertEqual((meta["records"], meta["support"], meta["reads"]), (3, 8, 8))
            maps = {m: query.contact_bins([path], "G", 10, measure=m) for m in ("records", "support", "reads")}
            self.assertEqual([maps[m]["total"] for m in maps], [3, 8, 8])
            # (0, 100) is covered by a (column 16 count 4) and c (one row); b starts at 11
            self.assertEqual(as_cells(maps["support"])[(0, 100)], 5)
            view = query.viewpoint([path], "G", 1, 40, measure="support")
            self.assertEqual(view["bins"]["value"][:12], [5] * 10 + [8, 8])
            self.assertEqual(query.region([path], "G", 1, 40)["profile"]["value"][:2], [5, 8])
            self.assertEqual(query.compare_bins([path], [path], "G", 10, measure="support")["library_a"], 8)
            # the web's Overview: arms count once each, so one RNA has twice the records
            view = query.overview([path, path], limit=0)
            self.assertEqual((view["records"], view["support"], view["reads"], view["read_records"]), (6, 16, 16, 6))
            self.assertEqual((view["unique_rnas"], view["unique_pairs"], view["rnas"], view["pairs"]), (1, 1, [], []))
            rna, = query.overview([path])["rnas"]
            self.assertEqual(rna, {"name": "G", "records": 6, "support": 16, "reads": 16, "read_records": 6,
                                   "overlap_total": 14, "overlap_records": 2})
            self.assertEqual(query.overview([path])["types"], [{"name": "Type_1", "records": 3, "support": 8}])
            # an index built before the support column: support falls back to one per record
            query.close_all()
            con = sqlite3.connect(path)
            con.execute("ALTER TABLE rects DROP COLUMN support")
            con.execute("DELETE FROM meta WHERE key = 'support'")
            con.commit()
            con.close()
            old = query.contact_bins([path], "G", 10, measure="support")
            self.assertEqual(old["total"], 3)
            self.assertEqual(query.compare_bins([path], [path], "G", 10, measure="support")["library_a"], 3)
            self.assertEqual(query.overview([path])["support"], 3)
            query.close_all()
