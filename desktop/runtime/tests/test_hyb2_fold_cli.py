"""Exercise the public shell commands with short, synthetic reference records."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


BIN = Path(__file__).resolve().parents[3] / "bin"


@unittest.skipUnless(os.name == "posix" and shutil.which("bash"), "legacy CLI uses POSIX tools")
class Hyb2FoldCliTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.tools = self.root / "tools"
        self.tools.mkdir()
        self.env = dict(os.environ, PATH=os.pathsep.join((str(self.tools), str(BIN), os.environ["PATH"])))
        for name in ("SGE_TASK_ID", "DB_1", "DB_2", "FASTA_1", "FASTA_2"):
            self.env.pop(name, None)
        self.first = self.root / "first reference" / "ref.fa"
        self.second = self.root / "second reference" / "ref.fa"
        for path, text in ((self.first, ">alpha\nGGGGAAAACCCC\n"), (self.second, ">beta\nCCCCAAAAGGGG\n")):
            path.parent.mkdir()
            path.write_text(text)
        (self.root / "sample.hyb").write_text(
            "r1\tGGGGCCCC\t-1.0\talpha\t1\t4\t1\t4\t0\talpha\t5\t8\t9\t12\t0\t10\n"
            "r2\tGGGGCCCC\t-1.0\talpha\t1\t4\t1\t4\t0\tbeta\t5\t8\t9\t12\t0\t10\n")
        self.tool("contact_density_map_indiv.R", 'test -s "$1"\nprintf pdf > "$3.pdf"')
        self.tool("cdm_indiv_zoom.R", 'test -s "$1"\nprintf pdf > "${9}_${2}-${3}_${4}-${5}.pdf"')
        self.tool("cdm_2genes.R", 'test -s "$1"\nprintf pdf > "$5.pdf"')
        self.tool("viewpoint_graph.R", 'test -s "$1"\nprintf pdf > "${1%.gplot}_viewpoint.pdf"')
        self.tool("VARNAcmd.sh", "exit 0")
        self.tool("java", '''
test "$1" = "-jar"
echo "$2" > called-jar
test "$2" = "path with spaces.jar"
while [ "$#" -gt 0 ]; do
    if [ "$1" = "-o" ]; then echo '<svg/>' > "$2"; break; fi
    shift
done''')
        # Keep the shell orchestrators, scoring scripts and verifier real while
        # avoiding a dependency on ViennaRNA, mfold or R in the unit suite.
        self.tool("comradesMakeConstraints_2", '''
prefix=${2%.hyb}
: > "${prefix}.1-${8}_folding_constraints.txt"
echo '1 12 1' > "${prefix}.basepair_scores.txt"''')
        self.tool("RNAfold", """awk 'NR == 1 {print; next} {print; gsub(/./, "."); print $0 " (0.00)"}'""")
        self.tool("b2ct", """awk 'NR == 1 {name = substr($1, 2)}
NR == 2 {n = length($0); print n, "ENERGY = 0.0", name
         for (i=1; i<=n; i++) print i, substr($0,i,1), i-1, (i<n ? i+1 : 0), 0, i}'""")

    def tool(self, name, body):
        path = self.tools / name
        path.write_text("#!/bin/sh\nset -e\n" + body + "\n")
        path.chmod(0o755)

    def invoke(self, command, *args):
        return subprocess.run(["bash", str(BIN / command), *map(str, args)], cwd=self.root,
                              env=self.env, capture_output=True, text=True, timeout=30)

    def fold(self, command, *reference_args):
        return self.invoke(command, "-i", "sample.hyb", "-a", "alpha", "-x", "1", "-l", "12", *reference_args)

    def ct_sequence(self, filename):
        return "".join(line.split()[1] for line in (self.root / filename).read_text().splitlines()[1:])

    def test_top_level_single_and_two_reference_folds(self):
        single = self.fold("hyb2", "-d", self.first)
        self.assertEqual(single.returncode, 0, single.stderr)
        self.assertIn("Analysis completed.", single.stdout)
        self.assertEqual(self.ct_sequence("alpha_1-12.fasta.ct"), "GGGGAAAACCCC")
        self.assertTrue((self.root / "sample_alpha.gplot").is_file())

        two = self.fold("hyb2", "-1", self.first, "-2", self.second, "-b", "beta", "-y", "1")
        self.assertEqual(two.returncode, 0, two.stderr)
        sequence = self.ct_sequence("alpha-1-12_beta-1-12.fasta.ct")
        self.assertEqual((len(sequence), sequence[:12], sequence[-12:]), (124, "GGGGAAAACCCC", "CCCCAAAAGGGG"))
        self.assertTrue((self.root / "sample_alpha_beta.gplot").is_file())
        self.assertFalse((self.root / "called-jar").exists())

    def test_standalone_d_option_reads_local_input_and_preserves_jar_argument(self):
        # Exercise the POSIX Java launcher without opening a native or WSL GUI.
        self.tool("uname", "echo Linux")
        local = self.root / "ref.fa"
        shutil.copy2(self.first, local)
        for mode in ("0", "1"):
            with self.subTest(interactive=mode):
                done = self.fold("hyb2_fold", "-d", local.name, "-j", "path with spaces.jar", "-0", mode)
                self.assertEqual(done.returncode, 0, done.stderr)
        self.assertTrue((self.root / "sample.alpha_1-12.fasta_plot.svg").is_file())
        self.assertEqual((self.root / "called-jar").read_text().strip(), "path with spaces.jar")
        self.assertEqual(local.read_bytes(), self.first.read_bytes())
        self.assertEqual(self.ct_sequence("alpha_1-12.fasta.ct"), "GGGGAAAACCCC")

    def test_backend_failure_aborts_public_analysis(self):
        self.tool("RNAfold", "exit 9")
        done = self.fold("hyb2", "-d", self.first)
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("RNAfold failed", done.stderr)
        self.assertNotIn("Analysis completed.", done.stdout)
        self.assertFalse((self.root / "alpha_1-12.fasta.ct").exists())
        self.tool("mfold_check", "exit 0")
        self.tool("mfold_fold", "exit 17")
        done = self.fold("hyb2", "-d", self.first, "-r", "2")
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("mfold_fold failed with exit status 17", done.stderr)
        self.assertNotIn("Analysis completed.", done.stdout)

    def test_input_conversion_failure_aborts_analysis(self):
        self.tool("bowtie2_fastq2sam", "exit 19")
        (self.root / "reads.fastq").touch()
        done = self.invoke("hyb2", "-i", "reads.fastq", "-d", self.first, "-o", "reads")
        self.assertNotEqual(done.returncode, 0)
        self.assertNotIn("Analysis completed.", done.stdout)

    def test_invalid_arguments_fail_and_coverage_only_skips_viewpoint(self):
        for command in ("hyb2", "hyb2_fold", "plot_viewpoint"):
            for args in (("-Z",), ("-d",), ("-i", "sample.hyb", "unexpected")):
                with self.subTest(command=command, args=args):
                    self.assertEqual(self.invoke(command, *args).returncode, 2)
        self.tool("plot_viewpoint", "exit 19")
        done = self.invoke("hyb2", "-i", "sample.hyb", "-a", "alpha")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertIn("Analysis completed.", done.stdout)

    def enable_scoring(self):
        (self.tools / "comradesMakeConstraints_2").unlink()
        self.tool("RNAcofold", """awk 'NR%2 {print; next}
            {print; dot=$0; gsub(/[^&]/,".",dot)
             if ($0=="GGGG&CCCC") dot="((((&))))"; print dot " (-3.00)"}'""")
        self.tool("RNAfold", """awk 'NR%2 {print; next}
            {print; dot=$0; gsub(/./,".",dot)
             if ($0=="GGGGAAAACCCC") dot="((((....))))"; print dot " (-3.00)"}'""")
        self.tool("b2ct", """awk 'NR%3==1 {name=substr($1,2)}
            NR%3==2 {seq=$0}
            NR%3==0 {
                n=length(seq); d=0
                for(i=1;i<=n;i++) {c=substr($1,i,1); pair[i]=0
                    if(c=="(") stack[++d]=i
                    if(c==")") {j=stack[d--]; pair[i]=j; pair[j]=i}}
                print n,"ENERGY = -3.0",name
                for(i=1;i<=n;i++) print i,substr(seq,i,1),i-1,(i<n?i+1:0),pair[i],i
            }'""")

    def test_nested_hyb_with_spaces_matches_plain_scores_through_public_fold(self):
        self.enable_scoring()
        nested = self.root / "data.hyb" / "sub folder" / "sample one.hyb"
        nested.parent.mkdir(parents=True)
        shutil.copyfile(self.root / "sample.hyb", nested)
        results = []
        for source in (self.root / "sample.hyb", nested):
            done = self.invoke("hyb2_fold", "-i", source, "-a", "alpha", "-d", self.first,
                               "-x", "1", "-l", "12", "-j", "path with spaces.jar")
            self.assertEqual(done.returncode, 0, done.stderr)
            prefix = source.with_suffix("")
            scores = Path(str(prefix) + "_alpha_1-12.basepair_scores.txt")
            names = Path(str(prefix) + "_alpha_1-12_names_scores.txt")
            results.append((scores.read_bytes(), names.read_text().rstrip().rsplit("\t", 1)[1]))
            self.assertTrue(Path(str(prefix) + ".alpha_1-12.fasta_plot.svg").is_file())
        expected = b"1\t12\t1\n2\t11\t1\n3\t10\t1\n4\t9\t1\n"
        self.assertEqual(results, [(expected, "8"), (expected, "8")])

    def test_score_faults_abort_and_remove_stale_or_partial_public_results(self):
        self.enable_scoring()
        for tool, body in (("RNAcofold", "exit 9"), ("RNAcofold", "exit 0"), ("b2ct", "exit 11"),
                           ("combine_hyb_merge_touching.pl", "exit 12"), ("bp_score.sh", "exit 14")):
            with self.subTest(tool=tool, body=body):
                path = self.tools / tool
                original = path.read_bytes() if path.exists() else None
                products = [self.root / name for name in (
                    "alpha_1-12.fasta.ct", "alpha_1-12.fasta.vienna", "sample_alpha_1-12.basepair_scores.txt",
                    "sample_alpha_1-12.1-12_folding_constraints.txt", "sample_alpha_1-12.1-12_folding_constraints.txt.accepted",
                    "sample_alpha_1-12_names_scores.txt", "sample__alpha_1-12.fasta.VARNA_log2scores.txt")]
                for product in products:
                    product.write_text("last run")
                self.tool(tool, body)
                done = self.fold("hyb2", "-d", self.first)
                self.assertNotEqual(done.returncode, 0, done.stdout)
                self.assertNotIn("Analysis completed.", done.stdout)
                self.assertEqual([p.name for p in products if p.exists()], [])
                if original is None:
                    path.unlink()
                else:
                    path.write_bytes(original)

    def test_valid_zero_scores_for_unpaired_arms_and_empty_selection(self):
        self.enable_scoring()
        self.first.write_text(">alpha\nAAAAAAAAAAAA\n")
        scores = self.root / "sample_alpha_1-12.basepair_scores.txt"
        for empty in (False, True):
            with self.subTest(empty=empty):
                if empty:
                    (self.root / "sample.hyb").write_text("")
                    self.tool("RNAcofold", "exit 33")
                done = self.fold("hyb2_fold", "-d", self.first)
                self.assertEqual(done.returncode, 0, done.stderr)
                self.assertEqual(scores.read_bytes(), b"")
                self.assertEqual(self.ct_sequence("alpha_1-12.fasta.ct"), "A" * 12)

    def test_reference_identity_wrapping_and_bounds(self):
        self.first.write_bytes(b">alpha.extra\nAAAAAAAAAAAA\n>alpha descriptive text\r\nGGGG\r\nAAAA\r\nCCCC\r\n")
        self.second.write_text(">alpha different source\nCCCC\nAAAA\nGGGG\n")
        before = [p.read_bytes() for p in (self.first, self.second)]
        done = self.fold("hyb2_fold", "-d", self.first, "-2", self.second, "-b", "alpha", "-y", "1")
        self.assertEqual(done.returncode, 0, done.stderr)
        combined = self.ct_sequence("alpha-1-12_alpha-1-12.fasta.ct")
        self.assertEqual((len(combined), combined[:12], combined[-12:]), (124, "GGGGAAAACCCC", "CCCCAAAAGGGG"))
        for args, output in (
                (("-x", "2"), "alpha_2-13.fasta.ct"),
                (("-2", self.second, "-b", "alpha", "-y", "2"), "alpha-1-12_alpha-2-13.fasta.ct"),
                (("-a", "alph"), "alph_1-12.fasta.ct")):
            with self.subTest(args=args):
                (self.root / output).write_text("stale")
                done = self.fold("hyb2_fold", "-d", self.first, *args)
                self.assertNotEqual(done.returncode, 0)
                self.assertFalse((self.root / output).exists())
        self.assertEqual(before, [p.read_bytes() for p in (self.first, self.second)])
        with self.first.open("a") as f:
            f.write(">alpha another copy\nAAAAAAAAAAAA\n")
        self.assertNotEqual(self.fold("hyb2_fold", "-d", self.first).returncode, 0)

    def test_viewpoint_exact_wrapped_reference_with_spaces_and_same_id_channels(self):
        nested = self.root / "input dir" / "sample one.hyb"
        nested.parent.mkdir()
        shutil.copyfile(self.root / "sample.hyb", nested)
        self.first.write_text(">alphax\nAAAA\n>alpha description\nGGGG\nAAAA\nCCCC\n")
        done = self.invoke("plot_viewpoint", "-i", nested, "-a", "alpha", "-d", self.first)
        self.assertEqual(done.returncode, 0, done.stderr)
        prefix = nested.with_suffix("")
        coverage = Path(str(prefix) + "_alpha.gplot").read_text().splitlines()[1:]
        self.assertEqual([int(row.split()[1]) for row in coverage], [1]*4 + [0]*4 + [1]*4)
        self.second.write_text(">alpha another reference\nAAAAAA\nAAAAAAAA\n")
        done = self.invoke("plot_viewpoint", "-i", nested, "-a", "alpha", "-1", self.first,
                           "-b", "alpha", "-2", self.second)
        self.assertEqual(done.returncode, 0, done.stderr)
        for channel, expected_len in ((1, 12), (2, 14)):
            gplot = Path(str(prefix) + f"_alpha_alpha_ref{channel}.gplot")
            self.assertEqual(len(gplot.read_text().splitlines()) - 1, expected_len)

    @unittest.skipUnless(all(shutil.which(t) for t in ("RNAfold", "RNAcofold", "b2ct")), "requires native ViennaRNA/b2ct")
    def test_real_public_score_equivalence_and_failing_cofold(self):
        for name in ("comradesMakeConstraints_2", "RNAfold", "b2ct"):
            (self.tools / name).unlink()
        nested = self.root / "project.hyb" / "data sets" / "sample one.hyb"
        nested.parent.mkdir(parents=True)
        shutil.copy2(self.root / "sample.hyb", nested)
        outputs = []
        for path in (self.root / "sample.hyb", nested):
            done = self.invoke("hyb2", "-i", path, "-a", "alpha", "-d", self.first, "-x", "1", "-l", "12")
            self.assertEqual(done.returncode, 0, done.stderr)
            self.assertIn("Analysis completed.", done.stdout)
            saved = Path(str(path.with_suffix("")) + "_alpha_1-12.basepair_scores.txt")
            outputs.append(saved.read_bytes())
        self.assertEqual(outputs[0], b"1\t12\t1\n2\t11\t1\n3\t10\t1\n4\t9\t1\n")
        self.assertEqual(outputs[0], outputs[1])
        self.tool("RNAcofold", 'echo "injected scoring failure" >&2; exit 9')
        done = self.invoke("hyb2", "-i", nested, "-a", "alpha", "-d", self.first, "-x", "1", "-l", "12")
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("injected scoring failure", done.stderr)
        self.assertNotIn("Analysis completed.", done.stdout)
        self.assertFalse(saved.exists())
        self.assertFalse((self.root / "alpha_1-12.fasta.ct").exists())

    def test_standalone_scorer_rejects_truncated_windows_in_any_reference_record(self):
        self.enable_scoring()
        source = self.root / "sample.hyb"
        self.first.write_text(">alpha details\nGGGGAAAA\nCCCC\n>beta\nGGGG\n")
        scores = self.root / "sample.basepair_scores.txt"
        fragment = self.first.parent / "ref_1-12.fasta"
        for out in (scores, fragment):
            out.write_text("stale")
        done = self.invoke("comradesMakeConstraints_2", "-i", source, "-f", self.first, "-b", "1", "-e", "12")
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("beta (4 bases)", done.stderr)
        self.assertFalse(scores.exists())
        self.assertFalse(fragment.exists())
        self.first.write_text(">alpha details\nGGGGAAAA\nCCCC\n>beta\nGGGGAAAACCCC\n")
        done = self.invoke("comradesMakeConstraints_2", "-i", source, "-f", self.first, "-b", "1", "-e", "12")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual(fragment.read_text().splitlines(), [">alpha", "GGGGAAAACCCC", ">beta", "GGGGAAAACCCC"])
        self.assertEqual(scores.read_text().splitlines(), [f"{i}\t{13-i}\t2" for i in range(1, 5)])

    def test_numeric_reference_identifiers_are_distinct(self):
        self.first.write_text(">1\nAAA\n>01 exact\nGGGG\nAAAA\nCCCC\n")
        src = self.root / "sample.hyb"
        original = src.read_text().splitlines()[0]
        src.write_text(original.replace("alpha", "01") + "\n" + original.replace("alpha", "1") + "\n")
        done = self.invoke("plot_viewpoint", "-i", src, "-d", self.first, "-a", "01")
        self.assertEqual(done.returncode, 0, done.stderr)
        values = (self.root / "sample_01.gplot").read_text().splitlines()[1:]
        self.assertEqual([int(r.split()[1]) for r in values], [1]*4+[0]*4+[1]*4)
        done = self.invoke("hyb2_fold", "-i", src, "-d", self.first, "-a", "01", "-x", "1", "-l", "12")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual(self.ct_sequence("01_1-12.fasta.ct"), "GGGGAAAACCCC")
