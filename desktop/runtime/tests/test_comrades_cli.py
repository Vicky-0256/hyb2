"""Public COMRADES shell entry points, using the mfold preflight's synthetic helix."""
import os
import shutil
import subprocess
import tempfile
import time
import unittest
from pathlib import Path


BIN = Path(__file__).resolve().parents[3] / "bin"
SEQ = "GGGGAAAACCCCAUUUUUUUUUUUAAAAAAAAAA"
FORCE = "F 14 34 3"
CONFLICT = "F 14 33 3"
PAIRS = {(14, 34), (15, 33), (16, 32)}


def partners(ct):
    rows = [row.split() for row in ct.splitlines()[1:]]
    return {(int(row[0]), int(row[4])) for row in rows if int(row[0]) < int(row[4])}


@unittest.skipUnless(os.name == "posix" and shutil.which("bash"), "the legacy shell CLI requires POSIX bash")
class ComradesCliTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.tools = self.root / "tools"
        self.tools.mkdir()
        self.fasta = self.root / "toy.fa"
        self.fasta.write_text(">toy\n" + SEQ + "\n")
        self.constraints = self.root / "constraints"
        self.constraints.write_text(FORCE + "\n")
        self.env = dict(os.environ, PATH=os.pathsep.join((str(self.tools), str(BIN), os.environ["PATH"])),
                        FIXTURES=str(self.root))
        self.env.pop("SGE_TASK_ID", None)
        self.env.pop("MFOLD", None)
        self.env.pop("HYBRID_SS_MIN", None)
        for name, pairs in (("forced", PAIRS), ("partial", {(14, 34)}), ("free", set())):
            paired = {i: j for a, b in pairs for i, j in ((a, b), (b, a))}
            rows = [f"{len(SEQ)} dG = -3.0 toy"]
            dot = ""
            for i, base in enumerate(SEQ, 1):
                j = paired.get(i, 0)
                rows.append(f"{i}\t{base}\t{i-1}\t{i+1 if i < len(SEQ) else 0}\t{j}\t{i}")
                dot += "." if not j else "(" if j > i else ")"
            (self.root / (name + ".ct")).write_text("\n".join(rows) + "\n")
            (self.root / (name + ".dbn")).write_text(f">toy\n{SEQ}\n{dot} (-3.00)\n")

    def tool(self, name, body):
        path = self.tools / name
        path.write_text("#!/bin/sh\nset -e\n" + body + "\n")
        path.chmod(0o755)

    def run_cli(self, *arguments, env=None):
        return subprocess.run(["bash", str(BIN / "comradesFold2"), "-i", self.fasta.name, "-c",
                               self.constraints.name, *arguments], cwd=self.root, env=env or self.env,
                              capture_output=True, text=True)

    def fake_vienna(self, rnafold=None, b2ct=None):
        self.tool("RNAfold", rnafold or """
if [ "$#" = 2 ]; then cat "$FIXTURES/free.dbn"; else cat "$FIXTURES/forced.dbn"; fi""")
        self.tool("b2ct", b2ct or """
input=$(cat)
for kind in forced partial free; do
    if [ "$input" = "$(cat "$FIXTURES/$kind.dbn")" ]; then cat "$FIXTURES/$kind.ct"; exit 0; fi
done
exit 1""")

    def fake_mfold(self, body):
        self.tool("mfold_datdir", 'echo "$FIXTURES"')
        (self.root / "stack.dat").touch()
        # The public command must pass the real mfold_check preflight before
        # the injected behavior applies to the user's trial/final fold.
        self.tool("mfold", """
if [ "$(head -n 1 s.seq)" = ">t" ]; then cp "$FIXTURES/forced.ct" s.ct; exit 0; fi
""" + body)

    def assert_failed_cleanly(self, *arguments):
        files = [self.root / ("toy.fa." + ext + suffix)
                 for ext in ("ct", "vienna", "aux") for suffix in ("", ".saved")]
        files.append(self.root / "constraints.accepted")
        for f in files:
            f.write_text("stale result")
        done = self.run_cli("-o", "saved", *arguments)
        self.assertNotEqual(done.returncode, 0, (done.stdout, done.stderr))
        self.assertEqual([f.name for f in files if f.exists()], [], done.stderr)
        self.assertEqual(list(self.root.glob("*.tmp.*")) + list(self.root.glob("*.fold.*")), [])
        return done.stderr

    def assert_forced(self):
        self.assertEqual((self.root / "toy.fa.aux").read_text().strip(), FORCE)
        self.assertEqual((self.root / "constraints.accepted").read_text().strip(), FORCE)
        self.assertTrue(PAIRS <= partners((self.root / "toy.fa.ct").read_text()))
        structure = (self.root / "toy.fa.vienna").read_text().splitlines()[2].split()[0]
        self.assertEqual(len(structure), len(SEQ))

    def test_public_default_keeps_the_whole_helix_and_output_copies(self):
        self.fake_vienna()
        # The final constraint line need not end with a newline.
        self.constraints.write_text(FORCE)
        done = self.run_cli("-o", "saved")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assert_forced()
        for ext in ("ct", "vienna", "aux"):
            self.assertEqual((self.root / f"toy.fa.{ext}").read_bytes(), (self.root / f"toy.fa.{ext}.saved").read_bytes())

    def test_vienna_and_b2ct_errors_cannot_publish_a_result(self):
        for body in ('cat "$FIXTURES/forced.dbn"; exit 9', 'exit 0'):
            with self.subTest(RNAfold=body):
                self.fake_vienna(rnafold=body)
                self.assert_failed_cleanly()
        for body in ('cat "$FIXTURES/forced.ct"; exit 9', 'exit 0', 'head -n 2 "$FIXTURES/forced.ct"',
                     'sed "1s/-3.0/-7.0/" "$FIXTURES/forced.ct"'):
            with self.subTest(b2ct=body):
                self.fake_vienna(b2ct=body)
                self.assert_failed_cleanly()

    def test_mfold_execution_or_output_errors_abort_trials_and_final(self):
        for constraints in (FORCE, ""):
            self.constraints.write_text(constraints)
            for body in ('cp "$FIXTURES/forced.ct" s.ct; exit 9', 'echo "Save file is empty. No foldings."; exit 9',
                         'exit 0', 'head -n 2 "$FIXTURES/forced.ct" > s.ct'):
                with self.subTest(constraints=constraints, body=body):
                    self.fake_mfold(body)
                    self.assert_failed_cleanly("-r", "2")

    def test_mfold_preflight_rejects_a_partially_forced_helix(self):
        self.fake_mfold("")
        self.tool("mfold", 'cp "$FIXTURES/partial.ct" s.ct')
        self.assertIn("did not fold a test sequence", self.assert_failed_cleanly("-r", "2"))
        self.tool("mfold_fold", 'head -n 18 "$FIXTURES/forced.ct" > "$3"')
        self.assertIn("test CT is incomplete", self.assert_failed_cleanly("-r", "2"))

    def test_legacy_unafold_success_and_tool_error(self):
        prefix = """
for input do :; done
if [ "$(head -n 1 "$input")" = ">t" ]; then cp "$FIXTURES/forced.ct" "$input.ct"; exit 0; fi
"""
        self.tool("hybrid-ss-min", prefix + 'cp "$FIXTURES/forced.ct" "$input.ct"')
        done = self.run_cli("-r", "0")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assert_forced()
        self.tool("hybrid-ss-min", prefix + 'cp "$FIXTURES/forced.ct" "$input.ct"; exit 9')
        self.assert_failed_cleanly("-r", "0")

    def test_mfold_public_command_uses_only_the_first_fasta_record(self):
        self.fasta.write_text(">toy description\n" + SEQ + "\n>another\nAAAA\n")
        self.fake_mfold('cp "$FIXTURES/forced.ct" s.ct')
        done = self.run_cli("-r", "2")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assert_forced()

    def test_concurrent_sge_tasks_only_share_the_original_inputs(self):
        self.env["SGE_TASK_ID"] = "7"
        self.fake_vienna(rnafold="""
if [ "$SGE_TASK_ID" = 7 ]; then
    touch "$FIXTURES/entered"
    while [ ! -e "$FIXTURES/release" ]; do sleep 0.05; done
fi
cat "$FIXTURES/forced.dbn" """)
        # qsub spools the job script under a numeric name without its helpers.
        spool = self.root / "spool" / "1071"
        spool.parent.mkdir()
        shutil.copy2(BIN / "comradesFold2", spool)
        first = subprocess.Popen([str(spool), "-i", self.fasta.name, "-c",
                                  self.constraints.name, "-s", "1", "-o", "saved"], cwd=self.root, env=self.env,
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            deadline = time.monotonic() + 10
            while not (self.root / "entered").exists() and first.poll() is None and time.monotonic() < deadline:
                time.sleep(0.02)
            self.assertTrue((self.root / "entered").exists())
            collision = self.run_cli("-o", "saved")
            self.assertNotEqual(collision.returncode, 0)
            self.assertIn("busy", collision.stderr)
            other = self.run_cli("-o", "saved", env=dict(self.env, SGE_TASK_ID="8"))
            self.assertEqual(other.returncode, 0, other.stderr)
        finally:
            (self.root / "release").touch()
            _, stderr = first.communicate(timeout=10)
        self.assertEqual(first.returncode, 0, stderr)
        for task in ("7", "8"):
            fasta = self.root / ("toy.fa" + task)
            self.assertEqual(fasta.read_text(), self.fasta.read_text())
            self.assertEqual((self.root / f"constraints{task}.accepted").read_text().strip(), FORCE)
            for ext in ("ct", "vienna", "aux"):
                out = self.root / f"{fasta.name}.{ext}"
                self.assertEqual(out.read_bytes(), (self.root / (out.name + ".saved")).read_bytes())
            self.assertTrue(PAIRS <= partners((self.root / f"{fasta.name}.ct").read_text()))

    def test_failed_empty_or_inconsistent_conversion_is_an_error(self):
        self.fake_mfold('cp "$FIXTURES/forced.ct" s.ct')
        for body in ('cat "$FIXTURES/forced.dbn"; exit 9', 'exit 0', 'echo incomplete', 'cat "$FIXTURES/free.dbn"',
                     'sed "1s/toy/old-run/" "$FIXTURES/forced.dbn"'):
            with self.subTest(body=body):
                self.tool("Ct2B_GK_3.pl", body)
                self.assert_failed_cleanly("-r", "2")

    def test_failed_publication_removes_partial_and_stale_results(self):
        self.fake_vienna()
        self.tool("mv", 'for dest do :; done\n[ "$dest" != toy.fa.vienna ] || exit 9\nexec /bin/mv "$@"')
        self.assert_failed_cleanly()

    def test_partial_helix_is_rejected_but_losing_an_accepted_helix_is_fatal(self):
        # The old check accepted a stem when its first pair alone appeared.
        self.fake_vienna(rnafold='if [ "$#" = 2 ]; then cat "$FIXTURES/free.dbn"; else cat "$FIXTURES/partial.dbn"; fi')
        done = self.run_cli()
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual((self.root / "toy.fa.aux").read_text(), "")
        self.assertEqual(partners((self.root / "toy.fa.ct").read_text()), set())
        self.fake_vienna(rnafold="""
if [ -e "$FIXTURES/trial" ]; then cat "$FIXTURES/partial.dbn"; else touch "$FIXTURES/trial"; cat "$FIXTURES/forced.dbn"; fi""")
        self.assert_failed_cleanly()

    def test_hyb2_fold_stops_at_public_folding_error(self):
        self.fake_vienna(rnafold="exit 9")
        self.fake_mfold("exit 9")
        self.tool("VARNAcmd.sh", "exit 0")
        self.tool("comradesMakeConstraints_2", 'printf "F 14 34 3\\n" > "${2%.hyb}.1-34_folding_constraints.txt"')
        case = self.root / "caller"
        case.mkdir()
        (case / "sample.hyb").touch()
        for backend in ("1", "2"):
            with self.subTest(backend=backend):
                done = subprocess.run(["bash", str(BIN / "hyb2_fold"), "-i", "sample.hyb", "-a", "toy",
                                       "-d", str(self.fasta), "-x", "1", "-l", "34", "-r", backend],
                                      cwd=case, env=self.env, capture_output=True, text=True)
                self.assertNotEqual(done.returncode, 0)
                self.assertIn("Welcome to comradesFold2", done.stdout)
                self.assertNotIn("Calculating Basepair Scores", done.stdout)
                self.assertFalse((case / "toy_1-34.fasta.ct").exists())

    @unittest.skipUnless(shutil.which("RNAfold") and shutil.which("b2ct"), "requires RNAfold and b2ct on PATH")
    def test_real_vienna_feasible_and_infeasible_trials(self):
        def reference(*flags):
            rna = subprocess.run(["RNAfold", "--noconv", "--noPS", *flags], input=self.fasta.read_text(),
                                 env=self.env, check=True, capture_output=True, text=True)
            ct = subprocess.run(["b2ct"], input=rna.stdout, env=self.env, check=True, capture_output=True, text=True)
            return partners(ct.stdout)
        self.assertTrue(PAIRS.isdisjoint(reference()))
        self.assertTrue(PAIRS <= reference("--commands=" + str(self.constraints)))
        self.constraints.write_text(FORCE + "\n" + CONFLICT + "\n")
        done = self.run_cli()
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assert_forced()

    @unittest.skipUnless(shutil.which("mfold") and shutil.which("mfold_datdir"), "requires mfold 3.6 and its tables on PATH")
    def test_real_mfold_feasible_and_infeasible_trials(self):
        self.constraints.write_text(FORCE + "\n" + CONFLICT + "\n")
        done = self.run_cli("-r", "2")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assert_forced()
        self.constraints.write_text("P 1-2 5-6\nP 14 O 2\n")
        out = self.root / "native.ct"
        done = subprocess.run([str(BIN / "mfold_fold"), str(self.fasta), str(self.constraints), str(out)],
                              cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(done.returncode, 0, done.stderr)
        result = partners(out.read_text())
        self.assertFalse(any(i in (1, 2) and j in (5, 6) for i, j in result))
        self.assertFalse(any(i in (14, 15) or j in (14, 15) for i, j in result))
