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
        for renderer in ("contact_density_map_indiv.R", "cdm_indiv_zoom.R", "cdm_2genes.R"):
            self.tool(renderer, "exit 0")
        self.tool("viewpoint_graph.R", 'test -s "$1"')
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
