"""File identity, formatting, and path forwarding in the public shell commands."""
import gzip
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
SEQ = "ACGTTAGGCTAACCTGATCGTACCGATTCGGA"
REAL_BOWTIE = shutil.which("bowtie2") and shutil.which("bowtie2-build")


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
        self.tool("contact_density_map_indiv.R", 'test -f "$1"; printf "%s\\n" "$1" >> "$CAPTURE/rendered"; printf pdf > "$3.pdf"')
        self.tool("cdm_2genes.R", 'test -f "$1"; printf "%s\\n" "$1" >> "$CAPTURE/rendered"; printf pdf > "$5.pdf"')
        self.tool("cdm_indiv_zoom.R", 'test -f "$1"; printf "%s\\n" "$1" >> "$CAPTURE/rendered"; printf pdf > "$9_$2-$3_$4-$5.pdf"')
        self.tool("viewpoint_graph.R", 'test -f "$1"; printf pdf > "${1%.gplot}_viewpoint.pdf"')

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

    def reads(self, kind, path):
        if kind == "fasta":
            return self.write(path, f">first\n{SEQ}\n>second\n{SEQ}\n")
        fastq = "".join(f"@{name}\n{SEQ}\n+\n{'I' * len(SEQ)}\n" for name in ("first", "second"))
        source = self.write(path, fastq)
        if kind == "fastq.gz":
            source.write_bytes(gzip.compress(source.read_bytes()))
        return source

    @unittest.skipUnless(REAL_BOWTIE, "requires bowtie2 and bowtie2-build")
    def test_real_fastq_never_uses_reference_or_neighbor_as_collapse_scratch(self):
        ref = self.write("out_comp.fasta", f">reference\n{SEQ}\n")
        source = self.reads("fastq", "reads.fastq")
        tab = self.write("reads.tab", "preexisting table\n")
        protected = {p: p.read_bytes() for p in (ref, source, tab)}
        result = self.run_command("bowtie2_fastq2sam", "-i", source.name, "-d", ref.name, "-o", "out")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual({p: p.read_bytes() if p.exists() else None for p in protected}, protected)
        self.assertIn("1_2\t0\treference\t1\t", (self.root / "out.sam").read_text())
        self.assertEqual(set(self.root.iterdir()), set(protected) | {self.tools, self.root / "out.sam", self.root / "out.blast.err"})

    @unittest.skipUnless(REAL_BOWTIE, "requires bowtie2 and bowtie2-build")
    def test_real_wrappers_ignore_six_old_shards_after_header_only_change(self):
        ref = self.write("ref.fasta", f">yesterday\n{SEQ}\n")
        subprocess.run(["bowtie2-build", "ref.fasta", "ref"], cwd=self.root,
                       check=True, capture_output=True, timeout=15)
        self.assertEqual(len(list(self.root.glob("ref.*.bt2"))), 6)
        self.write("ref.tab", f"yesterday\t{SEQ}\n")
        ref.write_text(f">today\n{SEQ}\n")
        for kind in ("fastq", "fasta", "fastq.gz"):
            with self.subTest(kind=kind):
                source = self.reads(kind, f"reads.{kind}")
                protected = {p: p.read_bytes() for p in [*self.root.glob("ref.*"), source]}
                result = self.run_command(f"bowtie2_{kind}2sam", "-i", source.name, "-d", ref.name, "-o", "out")
                self.assertEqual(result.returncode, 0, result.stderr)
                sam = (self.root / "out.sam").read_text().splitlines()
                self.assertIn(f"@SQ\tSN:today\tLN:{len(SEQ)}", sam)
                records = [line.split("\t")[:4] for line in sam if not line.startswith("@")]
                self.assertEqual(records, [["1_2", "0", "today", "1"]])  # two identical reads, collapsed
                self.assertEqual({p: p.read_bytes() for p in protected}, protected)

    @unittest.skipUnless(REAL_BOWTIE, "requires bowtie2 and bowtie2-build")
    def test_real_private_index_for_fa_and_failed_build_preserves_previous_sam(self):
        source = self.reads("fasta", "reads.fasta")
        ref = self.write("ref.fa", ">no_bases\n")
        sam = self.write("out.sam", SAM)
        result = self.run_command("bowtie2_fasta2sam", "-i", source.name, "-d", ref.name, "-o", "out")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(sam.read_text(), SAM)
        self.assertEqual(ref.read_text(), ">no_bases\n")
        ref.write_text(f">today\n{SEQ}\n")
        result = self.run_command("bowtie2_fasta2sam", "-i", source.name, "-d", ref.name, "-o", "out")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"@SQ\tSN:today\tLN:{len(SEQ)}\n", sam.read_text())
        self.assertEqual(set(self.root.glob("ref*")), {ref})
        complete = sam.read_bytes()
        source.write_text("not a FASTA read\n")
        result = self.run_command("bowtie2_fasta2sam", "-i", source.name, "-d", ref.name, "-o", "out")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(sam.read_bytes(), complete)
        self.assertEqual(source.read_text(), "not a FASTA read\n")

    @unittest.skipUnless(REAL_BOWTIE, "requires bowtie2 and bowtie2-build")
    def test_real_mapper_cannot_fall_back_to_an_unrelated_global_index(self):
        ref = self.write("ref.fa", f">yesterday\n{SEQ}\n")
        subprocess.run(["bowtie2-build", "ref.fa", "index"], cwd=self.root,
                       check=True, capture_output=True, timeout=15)
        self.env["BOWTIE2_INDEXES"] = str(self.root)
        self.tool("bowtie2-build", "exit 0")  # Simulate a build that left no shards.
        ref.write_text(f">today\n{SEQ}\n")
        source = self.reads("fasta", "reads.fasta")
        sam = self.write("out.sam", SAM)
        result = self.run_command("bowtie2_fasta2sam", "-i", source, "-d", ref, "-o", "out")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(sam.read_text(), SAM)

    def test_mapping_output_aliases_do_not_replace_reads_or_reference(self):
        self.tool("bowtie2-build", 'touch "$CAPTURE/builder-ran"')
        source = self.reads("fastq", "reads.fq")
        ref = self.write("ref.fa", f">today\n{SEQ}\n")
        protected = {p: p.read_bytes() for p in (source, ref)}
        for suffix, original in ((".sam", source), (".blast.err", ref)):
            for link in (os.link, os.symlink):
                with self.subTest(suffix=suffix, link=link.__name__):
                    target = self.root / ("out" + suffix)
                    link(original, target)
                    result = self.run_command("bowtie2_fastq2sam", "-i", source, "-d", ref, "-o", "out")
                    self.assertNotEqual(result.returncode, 0)
                    self.assertFalse((self.root / "builder-ran").exists())
                    self.assertTrue(target.samefile(original))
                    self.assertEqual({p: p.read_bytes() for p in protected}, protected)
                    target.unlink()

    def test_partial_mapping_failure_preserves_sam_for_each_input_format(self):
        self.tool("bowtie2-build", 'test -s "$1"; printf index > "$2.1.bt2"')
        self.tool("bowtie2", 'echo partial; echo "mapper stopped" >&2; exit 19')
        for kind in ("fastq", "fasta", "fastq.gz"):
            with self.subTest(kind=kind):
                source = self.reads(kind, f"quoted [1]/{kind}/some reads.{kind}")
                ref = self.write(source.parent / "ref.fa", f">today\n{SEQ}\n")
                prefix = source.parent / "out"
                sam = self.write(source.parent / "out.sam", SAM)
                protected = {p: p.read_bytes() for p in (source, ref, sam)}
                result = self.run_command(f"bowtie2_{kind}2sam", "-i", source, "-d", ref, "-o", prefix)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual({p: p.read_bytes() for p in protected}, protected)
                log = prefix.with_suffix(".blast.err")
                self.assertEqual(log.read_text(), "mapper stopped\n")
                self.assertEqual(set(source.parent.iterdir()), set(protected) | {log})

    def test_empty_mapper_output_does_not_replace_previous_sam(self):
        self.tool("bowtie2-build", 'echo index > "$2.1.bt2"')
        self.tool("bowtie2", "exit 0")
        source = self.reads("fasta", "reads.fa")
        ref = self.write("ref.fa", f">today\n{SEQ}\n")
        sam = self.write("out.sam", SAM)
        result = self.run_command("bowtie2_fasta2sam", "-i", source, "-d", ref, "-o", "out")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("empty SAM", result.stderr)
        self.assertEqual(sam.read_text(), SAM)

    def test_incomplete_or_empty_fastq_is_not_mapped_or_published(self):
        self.tool("bowtie2-build", 'touch "$CAPTURE/builder-ran"')
        self.tool("bowtie2", 'touch "$CAPTURE/mapper-ran"; echo unexpected')
        ref = self.write("ref.fasta", f">today\n{SEQ}\n")
        sam = self.write("out.sam", SAM)
        for kind in ("fastq", "fastq.gz"):
            for data in (b"", b"@truncated\n", b"@ok\nACGT\n+\nIIII\n@partial\nACGT\n+\n",
                         b"@short\nACGT\n+\nI\n", b"@bad\nACGT\nseparator\nIIII\n"):
                with self.subTest(kind=kind, data=data):
                    source = self.root / ("reads." + kind)
                    contents = gzip.compress(data) if kind == "fastq.gz" else data
                    source.write_bytes(contents)
                    result = self.run_command(f"bowtie2_{kind}2sam", "-i", source, "-d", ref, "-o", "out")
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("FASTQ", result.stderr)
                    self.assertEqual(source.read_bytes(), contents)
                    self.assertEqual(sam.read_text(), SAM)
                    self.assertFalse((self.root / "builder-ran").exists())
                    self.assertFalse((self.root / "mapper-ran").exists())

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
: > "${output}_homodimers.hyb"
: > "${output}_hybrids.hyb_stats.txt"
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

    def test_shared_read_collapser_preserves_rank_and_count_headers(self):
        for rows, expected in (
            ("a\tGGGG\nb\tCCCC\nc\tGGGG\n", ">1_2\nGGGG\n>2_1\nCCCC\n"),
            ("", ""),
            # Same read count: the sequence with more distinct barcodes ranks first.
            ("a_T\tCCCC\nb_T\tCCCC\nc_T\tGGGG\nd_A\tGGGG\n", ">1-2_2\nGGGG\n>2-1_2\nCCCC\n"),
        ):
            with self.subTest(expected=expected):
                source = self.write("input rows.tab", rows)
                before = set(self.root.iterdir())
                result = self.run_command("make_comp_fasta.pl", source)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout, expected)
                self.assertEqual(source.read_text(), rows)
                self.assertEqual(set(self.root.iterdir()), before)

    def test_incomplete_tab_rows_do_not_become_phantom_sequences(self):
        for rows in ("\n", "name\t\n", "name\t \n"):
            with self.subTest(rows=rows):
                source = self.write("input.tab", rows)
                result = self.run_command("make_comp_fasta.pl", source)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")
                self.assertEqual(source.read_text(), rows)

    def test_compressed_fastq_preserves_neighbors_and_only_publishes_complete_sam(self):
        self.tool("bowtie2-build", 'test -s "$1"; printf index > "$2.1.bt2"')
        self.tool("bowtie2", '''
while [ "$#" -gt 0 ]; do
    case "$1" in
        -f) cp -- "$2" "$CAPTURE/mapped.fa";;
        -x) test "$(cat "$2.1.bt2")" = index;;
    esac
    shift
done
echo "mapper diagnostic" >&2
if [ "$FAIL_MAP" = 1 ]; then echo partial; exit 19; fi
cat "$CAPTURE/expected.sam"''')
        self.tool("sam2hyb", '''
while getopts 'i:d:o:v:g:h:e:r:' opt; do
    case "$opt" in i) input=$OPTARG;; o) output=$OPTARG;; esac
done
cmp "$input" "$CAPTURE/expected.sam"
: > "${output}_homodimers.hyb"
: > "${output}_hybrids.hyb_stats.txt"
: > "$output.hyb"''')
        self.write("expected.sam", SAM)
        for directory, fail in (("simple", True), ("reference.fasta dir [1]", False)):
            with self.subTest(directory=directory):
                source = self.root / directory / "reads.fastq.gz"
                ref = self.write(f"{directory}/reference.fasta", ">alpha\nACGTACGT\n")
                prefix = ref.parent / "out"
                saved_sam = self.write(f"{directory}/out.sam", SAM.replace("read/1", "previous"))
                before_sam = saved_sam.read_bytes()
                # Two identical reads with the same barcode and one with a distinct barcode.
                reads = b"@a_TT\nACGT\n+\nIIII\n@b_TT\nACGT\n+\nIIII\n@c_AA\nACGT\n+\nIIII\n"
                source.write_bytes(gzip.compress(reads))
                neighbors = ["reads.fastq", "reads.tab", "out_comp.fasta", "reference.tab", "reference.1.bt2"]
                if not fail:
                    # A full set of filenames still says nothing about which reference was indexed.
                    neighbors += [f"reference.{part}.bt2l" for part in (1, 2, 3, 4, "rev.1", "rev.2")]
                protected = [source, ref] + [self.write(f"{directory}/{name}", "user's " + name) for name in neighbors]
                original = {path: path.read_bytes() for path in protected}
                expected_files = set(ref.parent.iterdir()) | {prefix.with_suffix(".blast.err")}
                if not fail:
                    expected_files.update(Path(str(prefix) + s) for s in (".hyb", ".hyb.inputs", "_homodimers.hyb", "_hybrids.hyb_stats.txt"))
                self.env["FAIL_MAP"] = str(int(fail))
                result = self.run_command("hyb2", "-i", source.relative_to(self.root), "-d", ref.relative_to(self.root),
                                          "-o", prefix.relative_to(self.root))
                self.assertEqual(result.returncode == 0, not fail, result.stderr)
                for path, contents in original.items():
                    self.assertEqual(path.read_bytes(), contents, path)
                self.assertEqual(saved_sam.read_bytes(), before_sam if fail else SAM.encode())
                self.assertEqual(prefix.with_suffix(".blast.err").read_text(), "mapper diagnostic\n")
                self.assertEqual((self.root / "mapped.fa").read_text(), ">1-2_3\nACGT\n")
                self.assertEqual(set(ref.parent.iterdir()), expected_files)

    def test_compressed_fastq_conversion_errors_do_not_reach_bowtie(self):
        self.tool("bowtie2-build", "exit 0")
        self.tool("bowtie2", 'touch "$CAPTURE/mapper-ran"')
        ref = self.write("reference.fasta", ">alpha\nACGT\n")
        source = self.root / "reads.fastq.gz"
        saved = self.write("out.sam", SAM)
        log = self.write("out.blast.err", "previous diagnostic\n")
        reads = gzip.compress(b"@one\nACGT\n+\nIIII\n")
        for failed_stage in ("gzip", "solexa2fasta.awk", "sort"):
            with self.subTest(failed_stage=failed_stage):
                compressed = reads[:-4] if failed_stage == "gzip" else reads
                source.write_bytes(compressed)
                if failed_stage != "gzip":
                    self.tool(failed_stage, 'cat >/dev/null; echo partial; echo "stage failed" >&2; exit 21')
                before = set(self.root.iterdir())
                result = self.run_command("hyb2", "-i", source, "-d", ref, "-o", "out")
                if failed_stage != "gzip":
                    (self.tools / failed_stage).unlink()
                    self.assertIn("stage failed", result.stderr)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((self.root / "mapper-ran").exists())
                self.assertEqual(source.read_bytes(), compressed)
                self.assertEqual(saved.read_bytes(), SAM.encode())
                self.assertEqual(log.read_text(), "previous diagnostic\n")
                self.assertEqual(set(self.root.iterdir()), before)

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
        self.assertEqual(rendered, [path.name for path in (first, mixed, mixed)])
        self.assertIn("x\ty\tcount\n", mixed.read_text())
        self.assertEqual(hyb.read_bytes(), HYB.encode())
        alias = self.run_command("plot_cdm", "-i", hyb, "-a", "alpha", "-b", "beta")
        self.assertEqual(alias.returncode, 0, alias.stderr)
        self.assertEqual((self.root / "rendered").read_text().splitlines()[-1], mixed.name)
