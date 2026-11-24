"""mfold must return a complete CT for the requested sequence before either caller publishes it."""
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock

from desktop.runtime import comrades, mfold


ROOT = Path(__file__).resolve().parents[3]
SEQUENCE = "GGGAAACCC"
HEADER = "9  dG = -3.90   [initially -3.90] s"
PARTNERS = (8, 7, 0, 0, 0, 0, 2, 1, 0)


def ct(partners=PARTNERS, header=HEADER):
    rows = [header]
    for i, (base, partner) in enumerate(zip(SEQUENCE, partners), 1):
        following = i + 1 if i < len(SEQUENCE) else 0
        rows.append(f"{i}\t{base}\t{i - 1}\t{following}\t{partner}\t{i}")
    return "\n".join(rows) + "\n"


def failures():
    yield "process failure with complete CT", 9, ct()
    yield "process failure with header only", 9, HEADER + "\n"
    yield "empty CT", 0, ""
    yield "header only", 0, HEADER + "\n"
    yield "missing final base", 0, "\n".join(ct().splitlines()[:-1]) + "\n"
    yield "wrong sequence length", 0, ct(header=HEADER.replace("9  dG", "8  dG"))
    yield "invalid energy", 0, ct(header="9 dG = -1.5oops s")
    yield "non-finite energy", 0, ct(header="9 dG = 1e999 s")
    yield "missing columns", 0, ct().replace("1\tG\t0\t2\t8\t1", "1\tG\t0\t2\t8")
    for label, column, replacement in (
        ("out-of-order row", 0, "2"), ("different base", 1, "C"),
        ("broken backbone", 2, "7"), ("invalid next base", 3, "3"),
        ("invalid natural index", 5, "0"), ("self pair", 4, "1"),
        ("out-of-range pair", 4, "10"), ("negative pair", 4, "-1"),
        ("non-reciprocal pair", 4, "0"),
    ):
        lines = ct().splitlines()
        row = lines[1].split()
        row[column] = replacement
        lines[1] = "\t".join(row)
        yield label, 0, "\n".join(lines) + "\n"
    yield "crossing pairs", 0, ct((7, 8, 0, 0, 0, 0, 1, 2, 0))


class MfoldOutputTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.tables = self.root / "tables"
        self.tables.mkdir()

    def native(self, data, status, executable=sys.executable, diagnostic=""):
        def run(args, *, cwd, **kwargs):
            self.assertTrue((cwd / args[0]).is_file())
            (cwd / "s.ct").write_text(data)
            return subprocess.CompletedProcess(args, status, diagnostic, "")

        with mock.patch.object(mfold, "data_dir", return_value=self.tables), mock.patch.object(mfold, "_run", run):
            return mfold.fold(SEQUENCE, {}, executable=executable)

    def test_relative_executable(self):
        self.addCleanup(os.chdir, Path.cwd())
        os.chdir(self.root)
        binary = self.root / "local-mfold"
        binary.touch()
        binary.chmod(0o755)
        with mock.patch.dict(os.environ, {mfold.ENV: "./local-mfold"}):
            for executable in (None, "./local-mfold"):
                with self.subTest(executable=executable):
                    self.assertEqual(self.native(ct(), 0, executable)["energy"], -3.9)

    def test_native(self):
        record = self.native(ct() + ct((0,) * len(SEQUENCE), "9 dG = 0 s"), 0)
        self.assertEqual((record["structure"], record["energy"]), ("((....)).", -3.9))
        for label, code, data in failures():
            with self.subTest(label=label):
                with self.assertRaises(mfold.MfoldError) as error:
                    self.native(data, code)
                if code:
                    self.assertIn("exit status 9", str(error.exception))

    @unittest.skipIf(os.name == "nt", "POSIX process groups")
    def test_timeout_ends_the_programs_mfold_started(self):
        # mfold is a shell script: its folding programs are its children
        pid_file = self.root / "child.pid"
        script = self.root / "mfold"
        script.write_text(f"#!/bin/sh\nsleep 300 &\necho $! > '{pid_file}'\nwait\n")
        script.chmod(0o755)

        def child_alive(pid):
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                return False
            # an exited child of this process (never here) would be a zombie
            return subprocess.run(["ps", "-o", "stat=", "-p", str(pid)], capture_output=True, text=True).stdout.strip()[:1] not in ("", "Z")

        with mock.patch.object(mfold, "data_dir", return_value=self.tables), mock.patch.object(mfold, "TIMEOUT_S", 2):
            with self.assertRaisesRegex(mfold.MfoldError, "did not finish"):
                mfold.fold(SEQUENCE, {}, executable=str(script))
        pid = int(pid_file.read_text())
        self.addCleanup(lambda: child_alive(pid) and os.kill(pid, 9))
        deadline = time.monotonic() + 10
        while child_alive(pid) and time.monotonic() < deadline:
            time.sleep(0.1)
        self.assertFalse(child_alive(pid), "mfold's child outlived the timeout")

    @unittest.skipIf(os.name == "nt", "POSIX process groups")
    def test_cancel_ends_the_programs_mfold_started(self):
        # the app cancels a task by SIGKILLing the sidecar's process group
        pid_file = self.root / "child.pid"
        script = self.root / "mfold"
        script.write_text(f"#!/bin/sh\nsleep 300 &\necho $! > '{pid_file}.tmp'\nmv '{pid_file}.tmp' '{pid_file}'\nwait\n")
        script.chmod(0o755)
        sidecar = ("import sys\nfrom pathlib import Path\nfrom unittest import mock\nfrom desktop.runtime import mfold\n"
                   "with mock.patch.object(mfold, 'data_dir', return_value=Path(sys.argv[1])):\n"
                   "    mfold.fold('GGGAAACCC', {}, executable=sys.argv[2])\n")
        helper = subprocess.Popen([sys.executable, "-B", "-c", sidecar, str(self.tables), str(script)], cwd=ROOT,
                                  start_new_session=True, stdin=subprocess.DEVNULL,
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.addCleanup(helper.wait)
        self.addCleanup(lambda: helper.poll() is None and os.killpg(helper.pid, 9))
        deadline = time.monotonic() + 30
        while not pid_file.exists() and helper.poll() is None and time.monotonic() < deadline:
            time.sleep(0.1)
        self.assertTrue(pid_file.exists(), "the fake mfold did not start its child")
        pid = int(pid_file.read_text())

        def child_alive():
            ps = subprocess.run(["ps", "-o", "stat=", "-p", str(pid)], capture_output=True, text=True)
            return ps.stdout.strip()[:1] not in ("", "Z")

        self.addCleanup(lambda: child_alive() and os.kill(pid, 9))
        os.killpg(helper.pid, 9)
        deadline = time.monotonic() + 10
        while child_alive() and time.monotonic() < deadline:
            time.sleep(0.1)
        self.assertFalse(child_alive(), "mfold's child outlived the cancel")

    def test_fit_propagates_tool_errors_and_can_reject_an_infeasible_stem(self):
        stem = {"id": "F1", "rank": 1, "pairs": [(1, 8)]}
        params = {"fold_program": "mfold", "min_loop": 3}

        def fit(data, code, diagnostic=""):
            def folder(sequence, parameters, pairs):
                return self.native(data, code, diagnostic=diagnostic) if pairs else self.native(ct(), 0)
            return comrades._fit(SEQUENCE, [stem], params, folder)

        for data, code in [(HEADER + "\n", 0), (ct(), 9)]:
            with self.subTest(code=code), self.assertRaises(mfold.MfoldError):
                fit(data, code)
        record, accepted, rejected = fit("", 1, "Save file is empty. No foldings.\nJob Aborted")
        self.assertEqual((record["energy"], accepted, rejected[0]["reason"]), (-3.9, [], "mfold-incompatible"))

    @unittest.skipUnless(os.name == "posix" and shutil.which("bash"), "the legacy shell command requires POSIX bash")
    def test_shell(self):
        binary = self.root / "fake mfold"
        binary.write_text('#!/bin/sh\ncp "$TEST_CT" s.ct || exit 10\nexit "$TEST_STATUS"\n')
        binary.chmod(0o755)
        datdir = self.root / "mfold_datdir"
        datdir.write_text('#!/bin/sh\nprintf "%s\\n" "$TEST_TABLES"\n')
        datdir.chmod(0o755)
        (self.tables / "stack.dat").touch()
        fasta, out = self.root / "input.fa", self.root / "output.ct"
        fasta.write_bytes(b">first description\r\nGGG AAA\r\nCCC\r\n>second\r\nCCCC\r\n")
        source = self.root / "from-mfold.ct"

        def run(data, status):
            source.write_text(data)
            env = dict(os.environ, MFOLD="./" + binary.name, TEST_TABLES=str(self.tables), TEST_CT=str(source), TEST_STATUS=str(status))
            return subprocess.run(["bash", str(ROOT / "bin/mfold_fold"), str(fasta), "-", str(out)],
                                  cwd=self.root, env=env, capture_output=True, text=True, umask=0o027)

        result = run(ct() + ct((0,) * len(SEQUENCE), "9 dG = 0 s"), 0)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(out.read_text().splitlines(), ["9\tdG = -3.90\tfirst", *ct().splitlines()[1:]])
        self.assertEqual(out.stat().st_mode & 0o777, 0o640)
        for label, code, data in failures():
            with self.subTest(label=label):
                out.write_text(ct())
                result = run(data, code)
                self.assertNotEqual(result.returncode, 0)
                if code:
                    self.assertIn("exit status 9", result.stderr)
                self.assertFalse(out.exists())
                self.assertEqual(list(self.root.glob("output.ct.tmp.*")), [])
