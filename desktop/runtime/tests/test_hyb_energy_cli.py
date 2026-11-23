"""Round trips and failure atomicity for the legacy SAM → HYB CLI, using synthetic reads."""
import gzip
import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
BIN = ROOT / "bin"
STRONG = ("ACGT" * 5, "ACGT" * 5)
WEAK = ("GA" * 10, "AGAAGAGACCCCGAGAGAGA")
WEAK_PAIRED = ("GA" * 10, "AGAGAGAGAGAGAGAGCTCT")
UNPAIRED = ("GA" * 10, "AG" * 10)


@unittest.skipUnless(os.name == "posix" and shutil.which("bash"), "the legacy shell CLI requires POSIX bash")
class HybEnergyCliTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.tools = self.root / "tools"
        self.tools.mkdir()
        (self.tools / "python").symlink_to(sys.executable)
        self.env = {**os.environ, "PATH": os.pathsep.join(map(str, (self.tools, BIN, os.environ["PATH"]))),
                    "PYTHONDONTWRITEBYTECODE": "1"}
        self.fasta = self.root / "reference source.fa"
        self.sam = self.root / "reads.sam"
        self.inputs(WEAK)

    def inputs(self, arms):
        first, second = arms
        reference = first + "ACGT" * 15 + second + "ACGT" * 15
        self.fasta.write_text(">toy descriptive header\n" + reference[:43] + "\n" + reference[43:] + "\n")
        read = first + second
        self.sam.write_text("@SQ\tSN:toy\tLN:160\n" + "".join(
            f"read_1\t{flag}\ttoy\t{pos}\t60\t{cigar}\t*\t0\t0\t{read}\t*\tAS:i:40\tNM:i:0\n"
            for flag, pos, cigar in [(0, 1, "20M20S"), (256, 81, "20S20M")]
        ))

    def run_cmd(self, args, **env):
        return subprocess.run(list(map(str, args)), cwd=self.root, env={**self.env, **env},
                              text=True, capture_output=True, timeout=30)

    def convert(self, prefix, *, public=False, energy="1", **env):
        cli = BIN / ("hyb2" if public else "sam2hyb")
        return self.run_cmd([cli, "-i", self.sam.name, "-d", self.fasta, "-o", prefix, "-e", energy], **env)

    def fake_cofold(self):
        tool = self.tools / "RNAcofold"
        tool.write_text("""#!/usr/bin/env python
import os, sys
assert sys.argv[1:] == ["--noconv", "--noPS"]
lines = sys.stdin.read().splitlines()
mode = os.environ.get("COFOLD_TEST", "")
for header, sequence in zip(lines[::2], lines[1::2]):
    if mode == "empty": continue
    print(header if mode != "wrong-id" else ">unrelated")
    print(sequence if mode != "wrong-sequence" else sequence[::-1])
    if mode == "truncated": continue
    if mode == "malformed": print("... ( inf)"); continue
    structure = "".join("&" if c == "&" else "." for c in sequence)
    if mode == "unbalanced": structure = ")" + structure[1:]
    print(structure, "(" + os.environ.get("COFOLD_ENERGY", "  0.00") + ")")
if mode == "extra": print(">extra\\nGG&GG\\n..&.. (0.00)")
if mode == "exit": sys.exit(9)
""")
        tool.chmod(0o755)

    def rows(self, prefix):
        return [row.split("\t") for row in (self.root / (prefix + ".hyb")).read_text().splitlines()]

    def web_record(self, prefix):
        script = """
const fs = require("node:fs"), vm = require("node:vm");
const scope = {self: {postMessage() {}}, TextDecoder};
vm.runInNewContext(fs.readFileSync(process.argv[1], "utf8"), scope);
const line = fs.readFileSync(process.argv[2], "utf8").trimEnd();
console.log(JSON.stringify(scope.parseRecord(line, 1)));
"""
        done = self.run_cmd(["node", "-e", script, ROOT / "web/parser.worker.js", prefix + ".hyb"])
        self.assertEqual(done.returncode, 0, done.stderr)
        parsed = json.loads(done.stdout)
        self.assertTrue(parsed["ok"], parsed)
        self.assertEqual(parsed["record"]["inputLayout"], "hyb2_sam2hyb_18_column")
        return parsed["record"]

    def test_public_energy_preserves_all_other_columns(self):
        self.fake_cofold()
        baseline = self.convert("baseline", public=True, energy="0")
        self.assertEqual(baseline.returncode, 0, baseline.stderr)
        reference, = self.rows("baseline")
        for index, (text, expected) in enumerate([("  0.00", "0.00"), (" -1.20", "-1.20"),
                                                  (" +0.75", "0.75"), ("-34.10", "-34.10")]):
            with self.subTest(energy=text):
                prefix = f"trial{index}"
                done = self.convert(prefix, public=True, COFOLD_ENERGY=text)
                self.assertEqual(done.returncode, 0, done.stderr)
                row, = self.rows(prefix)
                self.assertEqual(len(row), 18)
                self.assertEqual(row[2], expected)
                self.assertEqual(row[:2] + row[3:], reference[:2] + reference[3:])
                self.assertEqual(row[3], "toy")
        self.assertFalse(list(self.root.glob("*.tab")))

    @unittest.skipUnless(shutil.which("node"), "the web reader requires Node")
    def test_web_reader_and_paths_with_spaces(self):
        self.fake_cofold()
        self.sam = self.sam.rename(self.root / "input reads.sam")
        prefix = "results with spaces"
        done = self.convert(prefix, COFOLD_ENERGY=" +0.75")
        self.assertEqual(done.returncode, 0, done.stderr)
        record = self.web_record(prefix)
        self.assertEqual((record["rnaOne"], record["rnaTwo"], record["dg"]), ("toy", "toy", 0.75))

    def test_backend_failure_or_invalid_result_never_publishes_hyb(self):
        self.fake_cofold()
        for mode in ("exit", "empty", "truncated", "wrong-id", "wrong-sequence", "malformed", "unbalanced", "extra"):
            with self.subTest(mode=mode):
                done = self.convert("out", COFOLD_TEST=mode)
                self.assertNotEqual(done.returncode, 0, done.stdout)
                self.assertIn("RNAcofold", done.stderr)
                for file in ("out.hyb", "out_homodimers.hyb", "out_hybrids.hyb_stats.txt"):
                    self.assertFalse((self.root / file).exists(), file)
        self.assertFalse(list(self.root.glob(".sam2hyb.*")))

    def test_invalid_or_missing_reference_fails(self):
        self.fake_cofold()
        for content in (None, "", ">toy\n", ">different\nAGAG\n", ">toy\nAGAG\n"):
            with self.subTest(content=content):
                if content is None:
                    self.fasta.unlink()
                else:
                    self.fasta.write_text(content)
                done = self.convert("bad")
                self.assertNotEqual(done.returncode, 0)
                self.assertIn("reference", done.stderr.lower())
                self.assertFalse((self.root / "bad.hyb").exists())

    def test_empty_sam_has_no_pairs_to_fold(self):
        self.fake_cofold()
        self.sam.write_text("@SQ\tSN:toy\tLN:160\n")
        done = self.convert("empty", public=True, COFOLD_TEST="exit")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual(self.rows("empty"), [])

    def fake_mapper(self, name):
        mapper = self.tools / name
        mapper.write_text('''#!/bin/sh
set -e
while getopts i:d:o: arg; do
    case "$arg" in i) reads=$OPTARG;; d) ref=$OPTARG;; o) out=$OPTARG;; *) exit 2;; esac
done
test -s "$reads"
test -s "$ref"
cp "$MAPPED_SAM" "$out.sam"
echo "mapper diagnostic" > "$out.blast.err"
''')
        mapper.chmod(0o755)

    def test_format_dispatch_never_consumes_an_unrelated_output_sam(self):
        for suffix, mapper in [("fq", "bowtie2_fastq2sam"), ("fq.gz", "bowtie2_fastq.gz2sam"),
                               ("fa", "bowtie2_fasta2sam"), ("other", None)]:
            with self.subTest(suffix=suffix):
                reads = self.root / ("reads." + suffix)
                reads.write_text(">toy\nACGT\n" if suffix == "fa" else "@toy\nACGT\n+\nIIII\n")
                if suffix.endswith(".gz"):
                    reads.write_bytes(gzip.compress(reads.read_bytes()))
                prefix = "trial-" + suffix
                stale = self.root / (prefix + ".sam")
                stale.write_text("@SQ\tSN:unrelated\tLN:4\n")
                if mapper:
                    self.fake_mapper(mapper)
                result = self.run_cmd([BIN / "hyb2", "-i", reads, "-d", self.fasta, "-o", prefix],
                                      MAPPED_SAM=str(self.sam))
                if mapper:
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(stale.read_bytes(), self.sam.read_bytes())
                    self.assertEqual(len(self.rows(prefix)), 1)
                    (self.tools / mapper).unlink()
                else:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertFalse((self.root / (prefix + ".hyb")).exists())
                    self.assertIn("unrelated", stale.read_text())

    def test_existing_hyb_is_the_completion_marker_even_without_a_reference(self):
        ready = self.root / "ready.hyb"
        ready.write_text("")
        reads = self.root / "reads.fastq"
        reads.write_text("@toy\nACGT\n+\nIIII\n")
        for source, prefix in ((ready, "ignored/"), (reads, "ready")):
            with self.subTest(source=source):
                before = set(self.root.iterdir())
                done = self.run_cmd([BIN / "hyb2", "-i", source, "-o", prefix])
                self.assertEqual(done.returncode, 0, done.stderr)
                self.assertIn("Hyb File Exist.", done.stdout)
                self.assertEqual(set(self.root.iterdir()), before)
                self.assertEqual(ready.read_bytes(), b"")

    def test_directory_like_and_missing_output_prefix_do_not_publish(self):
        folder = self.root / "results"
        folder.mkdir()
        for name in ("results.hyb", "results_homodimers.hyb", "results_hybrids.hyb_stats.txt"):
            (self.root / name).write_text("previous " + name)
        for name, args in [("hyb2", []), ("hyb2", ["-o", "results/"]), ("sam2hyb", ["-o", "results/"])]:
            with self.subTest(cli=name, args=args):
                before = {p: p.read_bytes() for p in self.root.rglob("*") if p.is_file() and not p.is_symlink()}
                done = self.run_cmd([BIN / name, "-i", self.sam, "-e", "0", *args])
                self.assertNotEqual(done.returncode, 0)
                self.assertEqual({p: p.read_bytes() for p in self.root.rglob("*") if p.is_file() and not p.is_symlink()}, before)

    def test_nested_pipeline_failure_does_not_replace_a_completed_conversion(self):
        self.fake_cofold()
        self.assertEqual(self.convert("out").returncode, 0)
        paths = [self.root / name for name in ("out.hyb", "out_homodimers.hyb", "out_hybrids.hyb_stats.txt")]
        old = [p.read_bytes() for p in paths]
        self.assertTrue(old[0])
        awk = self.tools / "awk"
        awk.write_text('''#!/bin/sh
if [ "$2" = read_hits.txt ] && [ "$3" = input.sam ]; then echo "getseqs awk failed" >&2; exit 23; fi
exec /usr/bin/awk "$@"
''')
        awk.chmod(0o755)
        done = self.convert("out", energy="0")
        self.assertIn("getseqs awk failed", done.stderr)
        self.assertNotEqual(done.returncode, 0)
        self.assertEqual([p.read_bytes() for p in paths], old)
        awk.unlink()
        sort = self.tools / "sort"
        sort.write_text('''#!/bin/sh
if [ "$1" = "$FAIL_KEY" ]; then echo "collapse sort failed" >&2; exit 24; fi
exec /usr/bin/sort "$@"
''')
        sort.chmod(0o755)
        for key in ("-k13", "-k2"):
            with self.subTest(collapse_key=key):
                done = self.convert("out", energy="0", FAIL_KEY=key)
                self.assertIn("collapse sort failed", done.stderr)
                self.assertNotEqual(done.returncode, 0)
                self.assertEqual([p.read_bytes() for p in paths], old)

    def test_conversion_failure_keeps_prior_mapping_and_both_formatted_references(self):
        reads = self.root / "reads.fastq"
        reads.write_text("@toy\nACGT\n+\nIIII\n")
        self.fake_mapper("bowtie2_fastq2sam")
        self.fake_cofold()
        dirs = [self.root / name for name in ("first refs", "second refs", "output dir")]
        for directory in dirs:
            directory.mkdir()
        refs = [directory / "ref.fa" for directory in dirs[:2]]
        for ref in refs:
            ref.write_text(">toy|kind\n" + "ACGT" * 50 + "\n")
        self.sam.write_text(self.sam.read_text().replace("toy", "toy_kind"))
        prefix = dirs[2] / "out"
        saved = [ref.with_suffix(".hyb.fasta") for ref in refs] + [prefix.with_suffix(".sam"),
                 dirs[2] / "out_homodimers.hyb", dirs[2] / "out_hybrids.hyb_stats.txt"]
        for path in saved:
            path.write_text("previous: " + path.name)
        before = {p: p.read_bytes() for p in [reads, *refs, *saved]}
        args = [BIN / "hyb2", "-i", reads, "-1", refs[0], "-2", refs[1], "-o", prefix, "-e", "1"]
        done = self.run_cmd(args, MAPPED_SAM=str(self.sam), COFOLD_TEST="exit")
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("RNAcofold", done.stderr)
        self.assertEqual({p: p.read_bytes() for p in before}, before)
        self.assertFalse(prefix.with_suffix(".hyb").exists())
        self.assertEqual(prefix.with_suffix(".blast.err").read_text(), "mapper diagnostic\n")
        success = self.run_cmd(args, MAPPED_SAM=str(self.sam))
        self.assertEqual(success.returncode, 0, success.stderr)
        self.assertEqual(len(self.rows(str(prefix))), 1)
        self.assertEqual(prefix.with_suffix(".sam").read_bytes(), self.sam.read_bytes())
        for ref in refs:
            self.assertEqual(ref.with_suffix(".hyb.fasta").read_text(), ">toy_kind\n" + "ACGT" * 50)

    def test_second_reference_sidecar_alias_cannot_be_published_over_input(self):
        self.fake_mapper("bowtie2_fastq2sam")
        reads = self.root / "reads.fastq"
        reads.write_text("@toy\nACGT\n+\nIIII\n")
        original = self.fasta.read_bytes()
        for suffix in ("_homodimers.hyb", "_hybrids.hyb_stats.txt", ".sam", ".blast.err"):
            with self.subTest(suffix=suffix):
                directory = self.root / suffix
                directory.mkdir()
                sidecar = directory / ("out" + suffix)
                os.link(self.fasta, sidecar)
                ref = directory / "other.fa"
                ref.write_text(">other|kind\nACGT\n")
                source = reads if suffix.startswith(".") else self.sam
                done = self.run_cmd([BIN / "hyb2", "-i", source, "-1", ref, "-2", self.fasta, "-o", directory / "out"],
                                    MAPPED_SAM=str(self.sam))
                self.assertNotEqual(done.returncode, 0)
                self.assertFalse(ref.with_suffix(".hyb.fasta").exists())
                self.assertFalse((directory / "out.hyb").exists())
                self.assertTrue(self.fasta.samefile(sidecar))
                self.assertEqual(self.fasta.read_bytes(), original)

    def test_incomplete_or_absent_retained_records_leave_both_prior_references(self):
        hyb = self.root / "ready.hyb"
        hyb.write_text("")  # zero interactions is a legitimate completed analysis
        first = self.root / "first.fa"
        first.write_text(">first|kind\nACGT\n")
        second = self.root / "second.fa"
        formatted = [p.with_suffix(".hyb.fasta") for p in (first, second)]
        for bad in (">empty|one\n>populated|two\nGG\n", ">full|one\nGG\n>empty|last\n \t\n",
                    ">full|one\nGG\n>  \nACGT\n", ">empty|one\n;only a comment\n", ">discard|pseudogene\nGG\n"):
            with self.subTest(bad=bad):
                for path in formatted:
                    path.write_text("previous formatted output")
                second.write_text(bad)
                done = self.run_cmd([BIN / "hyb2", "-i", hyb, "-1", first, "-2", second])
                self.assertNotEqual(done.returncode, 0)
                self.assertEqual([p.read_text() for p in formatted], ["previous formatted output"] * 2)
                self.assertEqual(second.read_text(), bad)

    def test_output_directories_and_input_hard_links_are_protected(self):
        for suffix in (".hyb", "_homodimers.hyb", "_hybrids.hyb_stats.txt"):
            target = self.root / ("out" + suffix)
            with self.subTest(directory=target.name):
                target.mkdir()
                done = self.convert("out", energy="0")
                self.assertNotEqual(done.returncode, 0)
                self.assertIn("output conflicts", done.stderr)
                self.assertEqual(list(target.iterdir()), [])
                target.rmdir()
            for source in (self.sam, self.fasta):
                with self.subTest(output=target.name, source=source.name):
                    before = source.read_bytes()
                    os.link(source, target)
                    done = self.convert("out", energy="0")
                    self.assertNotEqual(done.returncode, 0)
                    self.assertTrue(source.samefile(target))
                    self.assertEqual(source.read_bytes(), before)
                    target.unlink()

    def test_publish_failure_restores_previous_files(self):
        work = self.root / "working"
        work.mkdir()
        for name in ("result.hyb", "stats.txt", "homodimers.hyb"):
            (work / name).write_text("new")
        # A formatted reference may live on a separate mount. Reject every
        # rename crossing this directory boundary, including during rollback.
        refs = self.root / "refs"
        ref_work = refs / "staging"
        ref_work.mkdir(parents=True)
        staged = ref_work / "reference.fasta"
        ref = refs / "reference.hyb.fasta"
        staged.write_text("new reference")
        ref.write_text("old reference")
        primary = self.root / "out.hyb"
        stats = self.root / "out_hybrids.hyb_stats.txt"
        primary.write_text("old HYB")
        stats.write_text("old stats")
        outputs = [(staged, ref), (work / "homodimers.hyb", self.root / "out_homodimers.hyb"),
                   (work / "stats.txt", stats), (work / "result.hyb", primary)]
        publish = runpy.run_path(str(BIN / "sam2hyb_publish.py"))["publish"]
        replace = os.replace

        def fail_second_publish(source, dest):
            self.assertEqual(refs in source.parents, refs in dest.parents, "cross-device rename")
            if source == work / "stats.txt":
                raise OSError("simulated stats publication failure")
            return replace(source, dest)

        with patch("os.replace", side_effect=fail_second_publish):
            with self.assertRaisesRegex(OSError, "simulated"):
                publish(work, outputs, [self.sam, self.fasta])
        self.assertEqual(ref.read_text(), "old reference")
        self.assertEqual(primary.read_text(), "old HYB")
        self.assertEqual(stats.read_text(), "old stats")
        self.assertFalse((self.root / "out_homodimers.hyb").exists())
        self.assertFalse((work / ".publishing").exists())

    def test_two_file_helper_preserves_hyb_annotations_and_missing_energies(self):
        row = ["read_lane_1", "AUGCAUGC", ".", "toy_A", "1", "4", "2", "5", "0",
               "toy_B", "5", "8", "7", "10", "0", "", ".", "Intermolecular", "custom=value", ""]
        short = ["short_1", *row[1:]]
        other = ["not_scored", *row[1:]]
        path = self.root / "input.hyb"
        path.write_bytes(("# header\r\n\r\n" + "\n".join("\t".join(r) for r in (row, short, other))).encode())
        result_file = self.root / "input.vienna"
        # Older exports removed ampersands and converted alignment spaces to tabs.
        result_file.write_text("read_lane_1_toy_A_2_5-read_lane_1_toy_B_7_10\nAUGCAUGC\n........\t(\t-1.50)\n"
                               "short_1_toy_A-short_1_toy_B\nAUGCAUGC\n........\t(\t0.00)\n")
        result = subprocess.run([BIN / "add_dG_hyb_2.pl", path, result_file], capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        row[2] = "-1.50"
        short[2] = "0.00"
        self.assertEqual(result.stdout.decode(), "# header\r\n\r\n" + "\n".join("\t".join(r) for r in (row, short, other)))

    def test_repeated_ids_are_joined_by_row_and_reverse_arms_are_unscored(self):
        seq = "AGAG"
        self.fasta.write_text(">toy\n" + seq * 8 + "\n")
        row = ["read_lane_1", seq * 2, ".", "toy", "1", "4", "1", "4", "0",
               "toy", "5", "8", "9", "12", "0"]
        rows = [row.copy() for _ in range(3)]
        rows[1][6:8] = ["4", "1"]
        rows[2][12:14] = ["13", "16"]
        hyb = self.root / "rows.hyb"
        hyb.write_text("".join("\t".join(fields) + "\n" for fields in rows))
        prepare = self.run_cmd(["awk", "-f", BIN / "sam2hyb_cofold.awk", self.fasta, hyb])
        self.assertEqual(prepare.returncode, 0, prepare.stderr)
        self.assertEqual(prepare.stdout.splitlines(), [">hyb2-row-1", seq + "&" + seq, ">hyb2-row-3", seq + "&" + seq])
        pairs = self.root / "pairs.fa"
        pairs.write_text(prepare.stdout)
        output = self.root / "results.vienna"
        output.write_text(f">hyb2-row-1\n{seq}&{seq}\n....&.... ( -1.20)\n"
                          f">hyb2-row-3\n{seq}&{seq}\n....&.... ( +0.20)\n")
        added = self.run_cmd([BIN / "add_dG_hyb_2.pl", hyb, output, pairs])
        self.assertEqual(added.returncode, 0, added.stderr)
        self.assertEqual([line.split("\t")[2] for line in added.stdout.splitlines()], ["-1.20", ".", "0.20"])

    @unittest.skipUnless(shutil.which("RNAcofold") and shutil.which("node"), "requires RNAcofold and Node on PATH")
    def test_real_cofold_web_roundtrip(self):
        for label, arms in (("strong", STRONG), ("weak", WEAK_PAIRED), ("zero", WEAK), ("unpaired", UNPAIRED)):
            with self.subTest(label=label):
                self.inputs(arms)
                direct = subprocess.run(["RNAcofold", "--noconv", "--noPS"], cwd=self.root,
                                        input="&".join(arms) + "\n", text=True, capture_output=True, check=True)
                expected = float(direct.stdout.rstrip().rsplit("(", 1)[1].rstrip(")"))
                done = self.convert(label, public=True)
                self.assertEqual(done.returncode, 0, done.stderr)
                self.assertEqual(self.rows(label)[0][2], f"{expected:.2f}")
                self.assertEqual(self.web_record(label)["dg"], expected)
                if label == "strong":
                    self.assertLess(expected, -10.0)
                elif label == "weak":
                    self.assertLess(-10.0, expected)
                    self.assertLess(expected, 0.0)
                else:
                    self.assertEqual(expected, 0.0)
