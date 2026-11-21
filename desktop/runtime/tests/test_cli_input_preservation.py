"""File identity, formatting, and path forwarding in the public shell commands."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


BIN = Path(__file__).resolve().parents[3] / "bin"
SAM = ("@HD\tVN:1.6\n@SQ\tSN:alpha\tLN:12\n"
       "read/1\t0\talpha\t1\t60\t4M\t*\t0\t0\tGGGG\tIIII\tNM:i:0\tAS:i:4\n"
       "other\t0\talpha\t1\t60\t2M1N2M1N2M\t*\t0\t0\tGGGGAA\tIIIIII\tNM:i:0\tAS:i:6\n")
HYB = "r1\tGGGGCCCC\t-1.0\talpha\t1\t4\t1\t4\t0\tbeta\t5\t8\t9\t12\t0\t10\n"


@unittest.skipUnless(os.name == "posix" and shutil.which("bash"), "the public shell CLI uses POSIX tools")
class CliInputPreservationTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.tools = self.root / "tools"
        self.tools.mkdir()
        self.env = dict(os.environ, PATH=os.pathsep.join((str(self.tools), str(BIN), os.environ["PATH"])),
                        CAPTURE=str(self.root))
        for name in ("GENE_1", "GENE_2", "DB_1", "DB_2", "X_COORD", "Y_COORD", "LEN", "OUT"):
            self.env.pop(name, None)
        for renderer in ("contact_density_map_indiv.R", "cdm_2genes.R", "cdm_indiv_zoom.R"):
            self.tool(renderer, 'test -f "$1"; printf "%s\\n" "$1" >> "$CAPTURE/rendered"')

    def tool(self, name, body):
        script = self.tools / name
        script.write_text("#!/bin/sh\nset -e\n" + body + "\n")
        script.chmod(0o755)

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(text.encode())
        return path

    def run_command(self, name, *args):
        return subprocess.run([str(BIN / name), *map(str, args)], cwd=self.root,
                              env=self.env, capture_output=True, text=True, timeout=15)

    def test_formatter_preserves_inputs_transforms_wrapped_records_and_fails_atomically(self):
        body = ">alpha|protein_coding\r\nGGGG\r\nAAAA\r\n>skip||kind\r\nCCCC\r\n>beta|type\r\nUUUU\r\n"
        for extension in ("fa", "fasta"):
            with self.subTest(extension=extension):
                source = self.write(f"{extension}.fasta dir/original.v2.{extension}", body)
                output = source.with_suffix(".hyb.fasta")
                result = self.run_command("fasta_hyb2_formatting", "-i", source, "-P")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(Path(result.stdout.strip()), output)
                self.assertEqual(output.read_bytes(), b">alpha_mRNA\nGGGGAAAA\n>beta_type\nUUUU")
                self.assertEqual(source.read_bytes(), body.encode())

                source.write_bytes(b"not a FASTA\n")
                before = output.read_bytes()
                failed = self.run_command("fasta_hyb2_formatting", "-i", source)
                self.assertNotEqual(failed.returncode, 0)
                self.assertEqual(source.read_bytes(), b"not a FASTA\n")
                self.assertEqual(output.read_bytes(), before)
                self.assertFalse(list(source.parent.glob("*.tmp.*")))

    def test_two_references_keep_both_originals_and_distinct_formatted_outputs(self):
        hyb = self.write("sample.hyb", HYB)
        self.tool("plot_viewpoint", '''
while getopts 'i:a:b:1:2:' opt; do
    case "$opt" in 1) first=$OPTARG;; 2) second=$OPTARG;; esac
done
printf '%s\\n' "$first" "$second" > "$CAPTURE/references"''')
        for first_name, second_name in (("one/shared.v2.fa", "two/shared.v2.fasta"),
                                       ("same/genes.fa", "same/genes.fasta"),
                                       ("reserved/genes.fa", "reserved/genes.hyb.fasta")):
            with self.subTest(first=first_name, second=second_name):
                first = self.write(first_name, ">alpha|kind\nGGGG\n")
                second = self.write(second_name, ">beta|kind\nCCCC\n")
                result = self.run_command("hyb2", "-i", hyb, "-d", first, "-2", second,
                                          "-a", "alpha", "-b", "beta")
                self.assertEqual(result.returncode, 0, result.stderr)
                formatted = [Path(path) for path in (self.root / "references").read_text().splitlines()]
                self.assertEqual([path.read_bytes() for path in formatted], [b">alpha_kind\nGGGG", b">beta_kind\nCCCC"])
                self.assertEqual((first.read_bytes(), second.read_bytes()), (b">alpha|kind\nGGGG\n", b">beta|kind\nCCCC\n"))
                self.assertTrue({first, second}.isdisjoint(formatted))

    def test_failed_formatting_in_either_reference_preserves_both_inputs(self):
        hyb = self.write("sample.hyb", HYB)
        for invalid in ("first", "second"):
            with self.subTest(invalid=invalid):
                first = self.write(invalid + "/first.fa", "bad|input\n" if invalid == "first" else ">alpha|kind\nAAAA\n")
                second = self.write(invalid + "/second.fasta", "bad|input\n" if invalid == "second" else ">beta|kind\nUUUU\n")
                original = (first.read_bytes(), second.read_bytes())
                saved = (first if invalid == "first" else second).with_suffix(".hyb.fasta")
                saved.write_text("saved formatted reference\n")
                result = self.run_command("hyb2", "-i", hyb, "-d", first, "-2", second)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("Analysis completed.", result.stdout)
                self.assertEqual((first.read_bytes(), second.read_bytes()), original)
                self.assertEqual(saved.read_text(), "saved formatted reference\n")

    def test_filtered_sam_is_separate_from_source_on_success_and_failure(self):
        self.tool("sam2hyb", '''
while getopts 'i:d:o:v:g:h:e:r:' opt; do
    case "$opt" in i) input=$OPTARG;; o) output=$OPTARG;; h) max_hits=$OPTARG;; esac
done
test "$max_hits" = 7 || exit 2
cp -- "$input" "$CAPTURE/filtered"
test "${FAIL_CONVERSION:-0}" = 0 || exit 9
: > "$output.hyb"''')
        for case, alias, fail in (("success", True, False), ("different", False, False),
                                  ("failure", True, True), ("failed-distinct", False, True), ("hardlink", True, False)):
            with self.subTest(case=case):
                source = self.write(f"{case}.sam inputs/same name.sam", SAM)
                prefix = source.with_suffix("") if alias and case != "hardlink" else source.parent / "converted file"
                published = Path(str(prefix) + ".sam")
                if case == "hardlink":
                    os.link(source, published)
                if fail and not alias:
                    published.write_bytes(b"last complete filtered output\n")
                self.env["FAIL_CONVERSION"] = str(int(fail))
                result = self.run_command("hyb2", "-i", source, "-o", prefix, "-h", "7")
                self.assertEqual(result.returncode == 0, not fail, result.stderr)
                self.assertEqual(source.read_bytes(), SAM.encode())
                filtered = (self.root / "filtered").read_text()
                self.assertIn("read_1\t", filtered)
                self.assertNotIn("other\t", filtered)
                expected = SAM.encode() if alias else filtered.encode()
                if fail and not alias:
                    expected = b"last complete filtered output\n"
                self.assertEqual(published.read_bytes(), expected)
                self.assertFalse(list(source.parent.glob("*.filter.*")))

    def test_filter_failure_cannot_publish_a_partial_sam(self):
        self.tool("sam_filt", "echo partial; exit 9")
        self.tool("sam2hyb", 'touch "$CAPTURE/converted"')
        source = self.write("reads.sam", SAM)
        saved = self.write("previous.sam", "previous filtered output\n")
        for prefix in ("reads", "previous"):
            with self.subTest(prefix=prefix):
                result = self.run_command("hyb2", "-i", source, "-o", prefix)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((self.root / "converted").exists())
                self.assertEqual(source.read_bytes(), SAM.encode())
                self.assertEqual(saved.read_text(), "previous filtered output\n")
                self.assertFalse(list(self.root.glob("*.filter.*")))

    def test_hyb_subdirectory_and_spaces_reach_coverage_viewpoint_and_fold(self):
        hyb = self.write("parent.hyb directory/in put.v2.hyb", HYB)
        ref = self.write("alpha.fa", ">alpha\nGGGGAAAACCCC\n>beta\nCCCCAAAAGGGG\n")
        for name in ("plot_viewpoint", "hyb2_fold"):
            self.tool(name, 'test "$1" = "-i"; test -f "$2"; printf "%s\\n" "$2" > "$CAPTURE/' + name + '"')
        relative = hyb.relative_to(self.root)
        result = self.run_command("hyb2", "-i", relative, "-d", ref, "-a", "alpha", "-b", "beta",
                                  "-x", "1", "-y", "1", "-l", "12")
        self.assertEqual(result.returncode, 0, result.stderr)
        for name in ("plot_viewpoint", "hyb2_fold"):
            self.assertEqual((self.root / name).read_text().strip(), str(relative))
        first = hyb.with_suffix(".alpha.contact.txt")
        mixed = hyb.with_suffix(".alpha-beta.contact.txt")
        rendered = (self.root / "rendered").read_text().splitlines()
        self.assertEqual(rendered, [str(path.relative_to(self.root)) for path in (first, mixed, mixed)])
        self.assertIn("x\ty\tcount\n", mixed.read_text())
        self.assertEqual(hyb.read_bytes(), HYB.encode())
        alias = self.run_command("plot_cdm", "-i", hyb, "-a", "alpha", "-b", "beta")
        self.assertEqual(alias.returncode, 0, alias.stderr)
        self.assertEqual((self.root / "rendered").read_text().splitlines()[-1], str(mixed))
