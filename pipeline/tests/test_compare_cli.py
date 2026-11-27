"""Unit tests for the pure-Python hyb2analysis CLI (compare, coverage, viewpoint)."""
from __future__ import annotations

import contextlib
import gzip
import inspect
import io
import itertools
import json
import os
import random
import shutil
import subprocess
import sys
import tempfile
import unittest
import xml.etree.ElementTree as ET
from pathlib import Path

HERE = Path(__file__).resolve().parent
PIPELINE_DIR = HERE.parent
REPO = PIPELINE_DIR.parent
sys.path.insert(0, str(PIPELINE_DIR))

from hyb2analysis import compare_cli, coverage, differential, index  # noqa: E402

GENE = "TEST_RNA"
GENE_B = "PARTNER_RNA"
GENE_C = "OTHER_RNA"


def _make_intra_hyb(path: Path, seed: int, enriched: int = 0, length: int = 400) -> Path:
    # 3000 background hybrids give most cells real counts, so DESeq2's dispersion
    # trend is not dominated by cells of 0-3 reads (with 800 the enrichment was
    # missed for some seeds)
    rng = random.Random(seed)
    lines = []
    for k in range(3000):
        a0 = rng.randint(1, length - 40)
        b0 = rng.randint(1, length - 40)
        lines.append(
            f"{k}_1\tACGU\t.\t{GENE}\t1\t20\t{a0}\t{a0 + 18}\t1e-5\t"
            f"{GENE}\t21\t40\t{b0}\t{b0 + 18}\t1e-5\t\t-8.0\tType_1\n"
        )
    for k in range(enriched):
        a0 = 60 + rng.randint(0, 10)
        b0 = 260 + rng.randint(0, 10)
        lines.append(
            f"e{k}_1\tACGU\t.\t{GENE}\t1\t20\t{a0}\t{a0 + 15}\t1e-5\t"
            f"{GENE}\t21\t40\t{b0}\t{b0 + 15}\t1e-5\t\t-12.0\tType_1\n"
        )
    path.write_text("".join(lines), encoding="utf-8")
    return path


def _make_pair_hyb(path: Path, seed: int, enriched: int = 0) -> Path:
    rng = random.Random(seed)
    lines = []
    for k in range(600 + enriched):
        if k < 600:
            x0 = rng.randint(1, 300)
            y0 = rng.randint(1, 250)
        else:
            x0 = 100 + rng.randint(0, 10)
            y0 = 180 + rng.randint(0, 10)
        if k % 2 == 0:
            a, b = (GENE, x0, x0 + 15), (GENE_B, y0, y0 + 15)
        else:
            a, b = (GENE_B, y0, y0 + 15), (GENE, x0, x0 + 15)
        lines.append(
            f"{k}_1\tACGU\t.\t{a[0]}\t1\t20\t{a[1]}\t{a[2]}\t1e-5\t"
            f"{b[0]}\t21\t40\t{b[1]}\t{b[2]}\t1e-5\t\t-9.0\tIntermolecular\n"
        )
    path.write_text("".join(lines), encoding="utf-8")
    return path


def _row(name: str, g1: str, s1: int, e1: int, g2: str, s2: int, e2: int, reads: int = 1, overlap: str = ".") -> str:
    return (f"{name}_{reads}\tACGU\t.\t{g1}\t1\t20\t{s1}\t{e1}\t1e-5\t"
            f"{g2}\t21\t40\t{s2}\t{e2}\t1e-5\t\t{overlap}\t.\n")


# Records the .hyb and index paths of coverage/viewpoint have to read alike
EDGE_ROWS = "".join((
    "# a comment line\n",
    _row("r1", GENE, 10, 30, GENE, 20, 40, reads=3, overlap="2"),  # overlapping arms
    _row("r2", GENE, 50, 60, GENE, 55, 70, reads=2, overlap="8"),  # a homodimer (overlap score >= 5)
    _row("r3", GENE_B, 200, 215, GENE, 100, 115, reads=4),         # GENE x GENE_B listed GENE_B first
    _row("r4", GENE, 120, 135, GENE_B, 230, 245),
    _row("r5", GENE, 480, 500, GENE_C, 5, 20),                     # GENE's last arm end, with a third RNA
    _row("r6", GENE, 90, 130, GENE, 140, 160, reads=5),            # past GENE's 150 nt in the reference
    _row("bad", GENE, 0, 10, GENE_B, 1, 10),                       # a coordinate of 0: not a record
    "not a hyb line\n",
))


def _cells(contact: bytes) -> dict:
    return {(int(x), int(y)): int(count) for x, y, count in
            (line.split(b"\t") for line in contact.splitlines() if line[:1].isdigit())}


def _parse_svg(path: Path) -> ET.Element:
    self_text = path.read_text(encoding="utf-8")
    root = ET.fromstring(self_text)
    assert root.tag.endswith("svg"), root.tag
    return root


class CompareCliTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)

    def tearDown(self):
        self.tmp.cleanup()

    def test_compare_one_two_hyb_writes_tables_and_svgs(self):
        a1 = _make_intra_hyb(self.dir / "a1.hyb", seed=1, enriched=220)
        a2 = _make_intra_hyb(self.dir / "a2.hyb", seed=2, enriched=240)
        b1 = _make_intra_hyb(self.dir / "b1.hyb", seed=3, enriched=0)
        b2 = _make_intra_hyb(self.dir / "b2.hyb", seed=4, enriched=0)
        out = self.dir / "cmp_out"

        rc = compare_cli.main([
            "compare",
            "--one", str(a1), str(a2),
            "--two", str(b1), str(b2),
            "-a", GENE,
            "-m", "2",
            "-r", "100",
            "--padj", "0.05",
            "--similarity-min", "2",
            "-o", str(out),
        ])
        self.assertEqual(rc, 0)
        for name in ("results.tsv", "significant.tsv", "interactions.tsv", "similarity.tsv",
                     "summary.json", "differential_map.svg", "similarity_heatmap.svg"):
            self.assertTrue((out / name).is_file(), f"missing {name}")

        summary = json.loads((out / "summary.json").read_text(encoding="utf-8"))
        self.assertGreater(summary["significant_up"], 0)
        self.assertGreater(summary["interactions_up"], 0)
        self.assertGreater(summary["similarity_cells"], 0)

        interactions = (out / "interactions.tsv").read_text(encoding="utf-8").splitlines()
        self.assertGreater(len(interactions), 1)
        top = interactions[1].split("\t")
        self.assertEqual(top[0], "up")
        self.assertTrue(50 <= int(top[1]) <= 90 and 250 <= int(top[2]) <= 290, top)

        diff_svg = _parse_svg(out / "differential_map.svg")
        sim_svg = _parse_svg(out / "similarity_heatmap.svg")
        ns = {"svg": "http://www.w3.org/2000/svg"}
        self.assertGreater(len(diff_svg.findall(".//svg:g/svg:rect", ns)), 0)
        self.assertGreater(len(sim_svg.findall(".//svg:g/svg:rect", ns)), 0)

    def test_compare_sample_table_three_col_and_two_col(self):
        a1 = _make_intra_hyb(self.dir / "a1.hyb", seed=11, enriched=200)
        a2 = _make_intra_hyb(self.dir / "a2.hyb", seed=12, enriched=210)
        b1 = _make_intra_hyb(self.dir / "b1.hyb", seed=13, enriched=0)
        b2 = _make_intra_hyb(self.dir / "b2.hyb", seed=14, enriched=0)

        a1_contact = coverage.write_contact(a1, GENE, self.dir / "a1.contact.txt")
        b1_contact = coverage.write_contact(b1, GENE, self.dir / "b1.contact.txt")

        table3 = self.dir / "samples_3col.tsv"
        table3.write_text(
            "# hyb\tcontact\tcondition\n"
            f"{a1.name}\t{a1_contact.name}\tcondition_one\n"
            f"{a2.name}\t-\tcondition_one\n"
            f"{b1.name}\t{b1_contact.name}\tcondition_two\n"
            f"{b2.name}\t-\tcondition_two\n",
            encoding="utf-8",
        )
        out3 = self.dir / "out3"
        self.assertEqual(compare_cli.main(["compare", "-i", str(table3), "-a", GENE, "-o", str(out3)]), 0)
        self.assertTrue((out3 / "differential_map.svg").is_file())
        self.assertTrue((out3 / "similarity_heatmap.svg").is_file())
        self.assertGreater(json.loads((out3 / "summary.json").read_text())["significant_up"], 0)

        a2_contact = coverage.write_contact(a2, GENE, self.dir / "a2.contact.txt")
        b2_contact = coverage.write_contact(b2, GENE, self.dir / "b2.contact.txt")
        table2 = self.dir / "samples_2col.tsv"
        table2.write_text(
            f"{a1_contact.name}\tone\n"
            f"{a2_contact.name}\tcondition_one\n"
            f"{b1_contact.name}\ttwo\n"
            f"{b2_contact.name}\tcondition_two\n",
            encoding="utf-8",
        )
        out2 = self.dir / "out2"
        # All contact maps precomputed: -a/--gene is optional here
        self.assertEqual(compare_cli.main(["compare", "-i", str(table2), "-o", str(out2)]), 0)
        self.assertEqual(
            json.loads((out2 / "summary.json").read_text())["significant_up"],
            json.loads((out3 / "summary.json").read_text())["significant_up"],
        )

        # A fourth column is the gene of the row's map (each desktop sample has
        # its own), and -a overrides it: b2's contact.txt, of another gene, is
        # then not used and b2's map is made from its .hyb, as the desktop does.
        wrong = coverage.write_contact(a1, GENE, self.dir / "wrong.contact.txt")  # a1's map, not b2's
        table4 = self.dir / "samples_4col.tsv"
        table4.write_text(
            f"{a1.name}\t-\tcondition_one\t{GENE}\n"
            f"{a2.name}\t-\tcondition_one\t{GENE}\n"
            f"{b1.name}\t-\tcondition_two\t{GENE}\n"
            f"{b2.name}\t{wrong.name}\tcondition_two\t{GENE_C}\n",
            encoding="utf-8",
        )
        out4 = self.dir / "out4"
        self.assertEqual(compare_cli.main(["compare", "-i", str(table4), "-a", GENE, "-o", str(out4)]), 0)
        self.assertEqual((out4 / "results.tsv").read_bytes(), (out3 / "results.tsv").read_bytes())
        # without -a each row's map is of its own gene: b2's is the given contact.txt
        out5 = self.dir / "out5"
        self.assertEqual(compare_cli.main(["compare", "-i", str(table4), "-o", str(out5)]), 0)
        self.assertEqual(sorted(path.name for path in (out5 / "contacts").iterdir()),
                         [f"a1.{GENE}.contact.txt", f"a2.{GENE}.contact.txt", f"b1.{GENE}.contact.txt"])

    def test_legacy_compare_input_table(self):
        # data/hyb2_compare_input.table is hyb2_compare's layout: <hyb> <contact.txt>
        # <condition>, separated by spaces; relative paths are found next to the table
        table = REPO / "data" / "hyb2_compare_input.table"
        rows = compare_cli._parse_sample_table(table)
        self.assertEqual([(hyb.name, contact.name, one, gene) for hyb, contact, one, gene in rows],
                         [("1.hyb", "1.contact.txt", True, None), ("2.hyb", "2.contact.txt", True, None),
                          ("3.hyb", "3.contact.txt", False, None), ("4.hyb", "4.contact.txt", False, None)])
        # With its .hyb files next to a copy and no contact.txt, -a makes the maps
        copy = Path(shutil.copy(table, self.dir / table.name))
        for number, (seed, enriched) in enumerate(((1, 220), (2, 240), (3, 0), (4, 0)), start=1):
            _make_intra_hyb(self.dir / f"{number}.hyb", seed=seed, enriched=enriched)
        out = self.dir / "legacy"
        self.assertEqual(compare_cli.main(["compare", "-i", str(copy), "-a", GENE, "-o", str(out)]), 0)
        self.assertEqual(sorted(path.name for path in (out / "contacts").iterdir()),
                         [f"{number}.{GENE}.contact.txt" for number in range(1, 5)])
        direct = self.dir / "direct"
        self.assertEqual(compare_cli.main([
            "compare", "--one", str(self.dir / "1.hyb"), str(self.dir / "2.hyb"),
            "--two", str(self.dir / "3.hyb"), str(self.dir / "4.hyb"), "-a", GENE, "-o", str(direct)]), 0)
        self.assertEqual((out / "results.tsv").read_bytes(), (direct / "results.tsv").read_bytes())
        self.assertGreater(json.loads((out / "summary.json").read_text())["significant_up"], 0)

    def test_compare_defaults_are_those_of_differential_compare_and_the_desktop(self):
        args = compare_cli.build_parser().parse_args(["compare", "-o", str(self.dir)])
        own = inspect.signature(differential.compare).parameters
        self.assertEqual(
            (args.min_count, args.range, args.alpha, args.padj, args.similarity_min),
            tuple(own[name].default for name in ("min_count", "radius", "alpha", "padj_cutoff", "similarity_min")),
        )
        # desktop/runtime/compare.py (_parameters): min_count 2, range 100,
        # padj_cutoff 0.05, similarity_min 1, no alpha (differential.compare's
        # 0.01), on 10-nt cells
        self.assertEqual((args.min_count, args.range, args.padj, args.similarity_min, args.alpha),
                         (2, 100, 0.05, 1, 0.01))
        self.assertEqual(compare_cli.BIN_SIZE, 10)

    def test_compare_inter_rna_and_empty_significant_svg(self):
        a1 = _make_pair_hyb(self.dir / "pa1.hyb", seed=21, enriched=200)
        a2 = _make_pair_hyb(self.dir / "pa2.hyb", seed=22, enriched=200)
        b1 = _make_pair_hyb(self.dir / "pb1.hyb", seed=23, enriched=0)
        b2 = _make_pair_hyb(self.dir / "pb2.hyb", seed=24, enriched=0)
        out = self.dir / "pair_out"

        rc = compare_cli.main([
            "compare",
            "--one", str(a1), str(a2),
            "--two", str(b1), str(b2),
            "-a", GENE, "-b", GENE_B,
            "-o", str(out),
        ])
        self.assertEqual(rc, 0)
        top = (out / "interactions.tsv").read_text(encoding="utf-8").splitlines()[1].split("\t")
        self.assertEqual(top[0], "up")
        self.assertTrue(90 <= int(top[1]) <= 130 and 170 <= int(top[2]) <= 210, top)
        svg_text = (out / "differential_map.svg").read_text(encoding="utf-8")
        self.assertIn(GENE, svg_text)
        self.assertIn(GENE_B, svg_text)

        # hyb2_compare's table with (intramolecular) contact maps: -b asks for
        # inter-RNA maps, so they are made from the .hyb files instead
        table = self.dir / "pairs.table"
        table.write_text("".join(
            f"{hyb.name}  {coverage.write_contact(hyb, GENE, self.dir / (hyb.stem + '.contact.txt')).name}  {condition}\n"
            for hyb, condition in ((a1, "condition_one"), (a2, "condition_one"), (b1, "condition_two"), (b2, "condition_two"))
        ), encoding="utf-8")
        out_table = self.dir / "pair_table_out"
        self.assertEqual(compare_cli.main(["compare", "-i", str(table), "-a", GENE, "-b", GENE_B, "-o", str(out_table)]), 0)
        self.assertEqual((out_table / "results.tsv").read_bytes(), (out / "results.tsv").read_bytes())

        # Both conditions drawn from one distribution: no significant cell, and a
        # valid SVG that says so.  (Identical replicates would make DESeq2's
        # dispersion fit fail, as it does in R.)
        b3 = _make_pair_hyb(self.dir / "pb3.hyb", seed=25, enriched=0)
        b4 = _make_pair_hyb(self.dir / "pb4.hyb", seed=26, enriched=0)
        out_empty = self.dir / "empty_sig"
        rc2 = compare_cli.main([
            "compare",
            "--one", str(b1), str(b2),
            "--two", str(b3), str(b4),
            "-a", GENE, "-b", GENE_B,
            "--padj", "1e-6",
            "-o", str(out_empty),
        ])
        self.assertEqual(rc2, 0)
        _parse_svg(out_empty / "differential_map.svg")
        self.assertIn("No significant cells", (out_empty / "differential_map.svg").read_text(encoding="utf-8"))

    def test_errors_exit_with_status_2_and_one_line(self):
        h1 = _make_intra_hyb(self.dir / "h1.hyb", seed=1)
        h2 = _make_intra_hyb(self.dir / "h2.hyb", seed=2)
        h3 = _make_intra_hyb(self.dir / "h3.hyb", seed=3)
        bad_table = self.dir / "bad.tsv"
        bad_table.write_text(f"{h1}\t-\tcondition_three\n", encoding="utf-8")
        truncated = self.dir / "truncated.hyb"
        truncated.write_bytes(gzip.compress(h1.read_bytes())[:200])
        reference = gzip.compress(f">{GENE}\n{h1.read_text()[:4000]}\n".encode())
        truncated_reference = self.dir / "truncated.fa.gz"
        truncated_reference.write_bytes(reference[:len(reference) // 2])
        blocker = self.dir / "a_file"
        blocker.write_text("", encoding="utf-8")
        not_written = self.dir / "not_written"
        cases = {
            "no command": [],
            "no -o": ["compare", "--one", str(h1)],
            "no -a": ["coverage", "-i", str(h1)],
            "unknown measure": ["viewpoint", "-i", str(h1), "-a", GENE, "--measure", "hybrids"],
            "no gene for maps from .hyb files": ["compare", "--one", str(h1), str(h2), "--two", str(h3), "-o", str(not_written)],
            "unknown condition": ["compare", "-i", str(bad_table), "-a", GENE, "-o", str(not_written)],
            "negative minimum count": ["compare", "--one", str(h1), str(h2), "--two", str(h3), "-a", GENE,
                                       "-m", "-1", "-o", str(not_written)],
            "two samples": ["compare", "--one", str(h1), "--two", str(h2), "-a", GENE, "-o", str(not_written)],
            "bin size 0": ["coverage", "-i", str(h1), "-a", GENE, "--bin-size", "0", "-o", str(not_written)],
            "truncated gzip": ["viewpoint", "-i", str(truncated), "-a", GENE, "-o", str(not_written)],
            "truncated gzip reference": ["viewpoint", "-i", str(h1), "-a", GENE, "-d", str(truncated_reference),
                                         "-o", str(not_written)],
            "missing input": ["viewpoint", "-i", str(self.dir / "missing.hyb"), "-a", GENE, "-o", str(not_written)],
            "output under a file (OSError)": ["coverage", "-i", str(h1), "-a", GENE, "-o", str(blocker / "out")],
        }
        for label, argv in cases.items():
            with self.subTest(label):
                stderr = io.StringIO()
                with contextlib.redirect_stderr(stderr), contextlib.redirect_stdout(io.StringIO()):
                    try:
                        code = compare_cli.main(argv)
                    except SystemExit as exit_:
                        code = exit_.code
                self.assertEqual(code, 2)
                self.assertEqual(len(stderr.getvalue().splitlines()), 1, stderr.getvalue())
        self.assertFalse(not_written.exists())  # the checks come before any output

    def test_coverage_hyb_gz_inter_rna_and_sqlite(self):
        hyb = _make_intra_hyb(self.dir / "sample.hyb", seed=31, enriched=30)
        hyb_gz = self.dir / "sample.hyb.gz"
        hyb_gz.write_bytes(gzip.compress(hyb.read_bytes()))
        idx = self.dir / "sample.hyb2idx"
        index.build(hyb, idx)

        out_hyb = self.dir / "cov_hyb"
        out_gz = self.dir / "cov_gz"
        out_idx = self.dir / "cov_idx"
        self.assertEqual(compare_cli.main(["coverage", "-i", str(hyb), "-a", GENE, "-o", str(out_hyb)]), 0)
        self.assertEqual(compare_cli.main(["coverage", "-i", str(hyb_gz), "-a", GENE, "-o", str(out_gz)]), 0)
        self.assertEqual(compare_cli.main(["coverage", "-i", str(idx), "-a", GENE, "-o", str(out_idx)]), 0)

        hyb_contact = out_hyb / f"sample.{GENE}.contact.txt"
        gz_contact = out_gz / f"sample.{GENE}.contact.txt"
        idx_contact = out_idx / f"sample.{GENE}.contact.txt"
        self.assertTrue(hyb_contact.is_file())
        self.assertEqual(hyb_contact.read_bytes(), gz_contact.read_bytes())

        # Exact same cell counts between .hyb and .sqlite index
        self.assertEqual(_cells(hyb_contact.read_bytes()), _cells(idx_contact.read_bytes()))
        for d in (out_hyb, out_gz, out_idx):
            svg = _parse_svg(d / "contact_map.svg")
            ns = {"svg": "http://www.w3.org/2000/svg"}
            self.assertGreater(len(svg.findall(".//svg:g/svg:rect", ns)), 0)

        # Inter-RNA coverage with explicit .contact.txt target
        pair_hyb = _make_pair_hyb(self.dir / "pair.hyb", seed=32, enriched=25)
        pair_idx = self.dir / "pair.sqlite"
        index.build(pair_hyb, pair_idx)
        custom_tsv = self.dir / "cov_pair" / "custom.contact.txt"
        custom_idx_dir = self.dir / "cov_pair_idx"
        self.assertEqual(
            compare_cli.main(["coverage", "-i", str(pair_hyb), "-a", GENE, "-b", GENE_B, "-o", str(custom_tsv)]), 0
        )
        self.assertEqual(
            compare_cli.main(["coverage", "-i", str(pair_idx), "-a", GENE, "-b", GENE_B, "-o", str(custom_idx_dir)]), 0
        )
        self.assertEqual(
            _cells(custom_tsv.read_bytes()),
            _cells((custom_idx_dir / f"pair.{GENE}-{GENE_B}.contact.txt").read_bytes()),
        )
        _parse_svg(custom_tsv.parent / "contact_map.svg")

    def test_hyb_and_index_agree_on_edge_cases(self):
        # The same .hyb as text, as gzip under a .hyb name (told by its first
        # bytes) and as an index give byte-identical profiles and SVGs and the
        # same cells; the expected values are worked out from EDGE_ROWS by hand.
        plain, packed = self.dir / "plain" / "edge.hyb", self.dir / "packed" / "edge.hyb"
        for path in (plain, packed):
            path.parent.mkdir()
        plain.write_text(EDGE_ROWS, encoding="utf-8")
        packed.write_bytes(gzip.compress(plain.read_bytes()))
        idx = self.dir / "edge.hyb2idx"
        index.build(plain, idx)
        ref = self.dir / "ref.fa"
        ref.write_text(f">{GENE}\n" + "A" * 150 + f"\n>{GENE_B}\n" + "C" * 300 + "\n", encoding="utf-8")
        numbers = itertools.count()

        def run(command, *options):
            results, warnings = [], []
            for src in (plain, packed, idx):
                out = self.dir / f"out{next(numbers)}"
                stderr = io.StringIO()
                with contextlib.redirect_stderr(stderr), contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(compare_cli.main([command, "-i", str(src), "-o", str(out), *options]), 0)
                if command == "coverage":
                    results.append((_cells(next(out.glob("*.contact.txt")).read_bytes()),
                                    (out / "contact_map.svg").read_bytes()))
                else:
                    results.append((next(out.glob("*.gplot")).read_text(), (out / "viewpoint.svg").read_bytes()))
                warnings.append(stderr.getvalue())
            self.assertEqual(results[1], results[0], options)
            self.assertEqual(results[2], results[0], options)
            return results[0][0], results[0][1], warnings[0]

        def profile(gplot):
            header, *rows = gplot.splitlines()
            return header, {int(pos): float(value) for pos, value in (row.split("\t") for row in rows)}

        # GENE x GENE_B: r3 (listed GENE_B first) and r4; the row with a
        # coordinate of 0 and the line that is not a record are left out
        pair = {(x, y): 1 for x in (100, 110) for y in (200, 210)}
        pair.update({(x, y): 1 for x in (120, 130) for y in (230, 240)})
        cells, _, warning = run("coverage", "-a", GENE, "-b", GENE_B)
        self.assertEqual(cells, pair)
        self.assertIn("skipped 2 lines that are not .hyb records", warning)
        self.assertEqual(run("coverage", "-a", GENE_B, "-b", GENE)[0], {(y, x): n for (x, y), n in pair.items()})
        self.assertEqual(run("coverage", "-a", GENE, "-b", GENE_B, "--measure", "reads")[0],
                         {cell: 4 if cell[0] < 120 else 1 for cell in pair})
        self.assertEqual(run("coverage", "-a", GENE, "-b", GENE_B, "--homodimer", "only")[0], {})  # intramolecular only
        # GENE x GENE: r1 (arms overlap), r2 (the homodimer) and r6
        r1 = {(x, y): 1 for x in (10, 20, 30) for y in (20, 30, 40)}
        r2 = {(x, y): 1 for x in (50, 60) for y in (50, 60, 70)}
        r6 = {(x, y): 1 for x in range(90, 131, 10) for y in (140, 150, 160)}
        self.assertEqual(run("coverage", "-a", GENE)[0], {**r1, **r2, **r6})
        self.assertEqual(run("coverage", "-a", GENE, "--homodimer", "only")[0], r2)
        self.assertEqual(run("coverage", "-a", GENE, "--homodimer", "exclude")[0], {**r1, **r6})
        run("coverage", "-a", GENE, "--bin-size", "7", "--measure", "support")
        run("coverage", "-a", GENE_C, "-b", GENE, "--bin-size", "1", "--measure", "reads")

        # the default end is GENE's last arm end in any record (r5, with GENE_C)
        gplot, _, _ = run("viewpoint", "-a", GENE, "-b", GENE_B)
        self.assertEqual(profile(gplot), (f"#edge_{GENE}_{GENE_B}_500",
                                          {pos: float(100 <= pos <= 115 or 120 <= pos <= 135) for pos in range(1, 501)}))
        # records: those with an arm in the range, here r3, r4 and r6 (both arms)
        gplot, svg, _ = run("viewpoint", "-a", GENE, "--start", "100", "--end", "140")
        self.assertIn(b"records: 3 |", svg)
        expected = {pos: float(sum(start <= pos <= end for start, end in ((100, 115), (120, 135), (90, 130), (140, 160))))
                    for pos in range(100, 141)}
        self.assertEqual(profile(gplot), (f"#edge_{GENE}_140", expected))
        # the homodimer r2 alone, both of its arms; no inter-RNA record
        gplot, svg, _ = run("viewpoint", "-a", GENE, "--homodimer", "only")
        self.assertIn(b"records: 1 |", svg)
        self.assertEqual(profile(gplot), (f"#edge_{GENE}_500",
                                          {pos: float((50 <= pos <= 60) + (55 <= pos <= 70)) for pos in range(1, 501)}))
        # -d ends the profile at GENE's 150 nt: r6's second arm is cut there, r5 is outside
        gplot, svg, _ = run("viewpoint", "-a", GENE, "-d", str(ref))
        self.assertIn(b"records: 5 |", svg)
        header, values = profile(gplot)
        self.assertEqual((header, len(values), values[125], values[150]), (f"#edge_{GENE}_150", 150, 2.0, 1.0))
        run("viewpoint", "-a", GENE, "-b", GENE_C, "--bin-size", "7", "--measure", "reads", "--homodimer", "exclude")
        run("viewpoint", "-a", GENE_B, "-d", str(ref), "--end", "1000", "--measure", "support")
        self.assertEqual(run("viewpoint", "-a", "ABSENT")[0], "#edge_ABSENT_0\n")

    def test_viewpoint_hyb_and_sqlite_and_reference(self):
        hyb = _make_pair_hyb(self.dir / "vp.hyb", seed=41, enriched=20)
        idx = self.dir / "vp.sqlite"
        index.build(hyb, idx)
        ref = self.dir / "ref.fa"
        ref.write_text(f">{GENE}\n" + "ACGU" * 100 + f"\n>{GENE_B}\n" + "ACGU" * 80 + "\n", encoding="utf-8")

        out_hyb = self.dir / "vp_hyb"
        out_idx = self.dir / "vp_idx"
        self.assertEqual(
            compare_cli.main(["viewpoint", "-i", str(hyb), "-a", GENE, "-b", GENE_B, "-d", str(ref), "-o", str(out_hyb)]),
            0,
        )
        self.assertEqual(
            compare_cli.main(["viewpoint", "-i", str(idx), "-a", GENE, "-b", GENE_B, "-d", str(ref), "-o", str(out_idx)]),
            0,
        )

        gplot_hyb = out_hyb / f"vp_{GENE}_{GENE_B}.gplot"
        gplot_idx = out_idx / f"vp_{GENE}_{GENE_B}.gplot"
        self.assertTrue(gplot_hyb.is_file())
        lines_h = gplot_hyb.read_text(encoding="utf-8").splitlines()
        lines_i = gplot_idx.read_text(encoding="utf-8").splitlines()
        self.assertTrue(lines_h[0].startswith("#"))
        self.assertEqual(lines_h, lines_i)
        self.assertEqual(len(lines_h), 401)  # header + positions 1..400

        svg = _parse_svg(out_hyb / "viewpoint.svg")
        ns = {"svg": "http://www.w3.org/2000/svg"}
        self.assertIsNotNone(svg.find(".//svg:polyline", ns))
        _parse_svg(out_idx / "viewpoint.svg")

    def test_module_cli_subprocess_and_svg_downsampling(self):
        hyb = _make_intra_hyb(self.dir / "sub.hyb", seed=51, enriched=10)
        out = self.dir / "sub_out"
        env = dict(os.environ)
        env["PYTHONPATH"] = str(PIPELINE_DIR)
        proc = subprocess.run(
            [sys.executable, "-B", "-m", "hyb2analysis", "coverage", "-i", str(hyb), "-a", GENE, "-o", str(out)],
            env=env,
            capture_output=True,
            text=True,
            check=True,
        )
        self.assertIn("contact.txt", proc.stdout)
        _parse_svg(out / "contact_map.svg")

        # Downsampling path when > _MAX_SVG_CELLS
        xs = [10 * (i % 120) for i in range(9500)]
        ys = [10 * (i // 120) for i in range(9500)]
        vals = [float(i % 20 + 1) for i in range(9500)]
        big_svg = self.dir / "big.svg"
        compare_cli._render_heatmap_svg(
            big_svg,
            title="Big",
            subtitle="Downsampled",
            xlabel="X",
            ylabel="Y",
            legend_title="Count",
            xs=xs,
            ys=ys,
            values=vals,
        )
        root = _parse_svg(big_svg)
        ns = {"svg": "http://www.w3.org/2000/svg"}
        rects = root.findall(".//svg:g/svg:rect", ns)
        self.assertGreater(len(rects), 0)
        self.assertLessEqual(len(rects), 9500)


if __name__ == "__main__":
    unittest.main()
