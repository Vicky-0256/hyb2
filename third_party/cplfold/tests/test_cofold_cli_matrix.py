"""Exercise complete CLI input routes with the same two-strand predictions.

Children explicitly use pure Python to keep ordinary test discovery cheap.
Real-JIT API and prediction parity are covered by the separate verifier.
"""

import math
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[3]
WRAPPER = ROOT / "bin/cplfold"


class CofoldCliMatrixTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="cofold-cli-matrix-")
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.environment = os.environ.copy()
        for name in ("PYTHONPATH", "PYTHONHOME"):
            self.environment.pop(name, None)
        self.environment.update(NUMBA_DISABLE_JIT="1", PYTHONDONTWRITEBYTECODE="1",
                                NUMBA_CACHE_DIR=str(self.directory / "numba"),
                                HYB2_CPLFOLD_MAX_NT="1000")
        self.options = ["--beam", "20", "--max-phase1", "1", "--max-phase2", "1",
                        "--delta", "0", "--no-pseudoknot"]

    def run_cli(self, arguments, success=True, maximum=None, expected_error=None):
        environment = dict(self.environment)
        if maximum is not None:
            environment["HYB2_CPLFOLD_MAX_NT"] = str(maximum)
        result = subprocess.run([sys.executable, str(WRAPPER), *arguments, *self.options],
                                cwd=ROOT, env=environment, capture_output=True,
                                text=True, timeout=60)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertNotRegex(result.stdout, r"(?m)^S-\d+: ")
            self.assertIsNotNone(expected_error, "A rejection must identify the expected validation error")
            self.assertIn(expected_error, result.stdout + result.stderr)
            self.assertNotIn("Traceback (most recent call last)", result.stdout + result.stderr)
        return result

    def report(self, arguments):
        output = self.run_cli(arguments).stdout
        positions = list(re.finditer(r"(?m)^Sequence: ", output))
        self.assertEqual(len(positions), 1, output)
        report = output[positions[0].start():]
        candidates = re.findall(r"(?m)^S-(\d+): ([().]+&[().]+)\n[ \t]+Energy: (\S+) kcal/mol$", report)
        self.assertEqual([item[0] for item in candidates], ["1"], report)
        energies = re.findall(r"(?m)^\s+(?:Effective )?Energy: (\S+) kcal/mol$", report)
        self.assertGreaterEqual(len(energies), 2, report)
        for value in [item[2] for item in candidates] + energies:
            self.assertTrue(math.isfinite(float(value)), report)
        return report

    def routes(self, first, second):
        two = self.directory / "two.fasta"
        two.write_text(f">first\n{first}\n>second\n{second}\n")
        one = self.directory / "first.fasta"
        one.write_text(f">first\n{first}\n")
        joined = self.directory / "joined.fasta"
        joined.write_text(f">joined\n{first}{second}\n")
        return [
            ["--sequence", first + "&" + second],
            ["--sequence", first, "--sequence-two", second],
            ["--sequence", first + second, "--cutpoint", str(len(first))],
            ["--sequence-file", str(two), "--cofold"],
            ["--sequence-file", str(joined), "--cutpoint", str(len(first)), "--cofold"],
            ["--sequence-file", str(one), "--sequence-two", second, "--cofold"],
        ]

    def test_six_routes_preserve_structure_energy_and_original_strand_order(self):
        for first, second in (("G", "C"), ("GGG", "AAACCC"), ("GGGAAACCC", "AAAA")):
            with self.subTest(first=first, second=second):
                reports = [self.report(route) for route in self.routes(first, second)]
                self.assertTrue(all(report == reports[0] for report in reports))
                self.assertIn("Sequence: " + first + "&" + second, reports[0])
                self.assertIn(f"Length: {len(first) + len(second)} nt", reports[0])

    def test_case_dna_letters_and_whitespace_normalize_before_cutpoint(self):
        expected = self.report(["--sequence", "GGU&ACC"])
        for arguments in (["--sequence", " ggt & a cc "],
                          ["--sequence", "ggt", "--sequence-two", " a cc "],
                          ["--sequence", " gg t a cc ", "--cutpoint", "3"]):
            with self.subTest(arguments=arguments):
                self.assertEqual(self.report(arguments), expected)

    def test_reads_file_has_the_same_effect_through_all_six_routes(self):
        bonus = self.directory / "bonus.tsv"
        bonus.write_text("# sequence_length=4\n"
                         "prepared_position_1\tprepared_position_2\tlog1p_gaussian_hyb_bonus\n"
                         "1\t4\t5\n2\t3\t5\n")
        baseline = self.report(["--sequence", "GG&CC"])
        self.assertIn("S-1: ..&..", baseline)
        guided = []
        for route in self.routes("GG", "CC"):
            guided.append(self.report(route + ["--bonus-matrix-file", str(bonus), "--alpha", "1"]))
        self.assertTrue(all(report == guided[0] for report in guided))
        self.assertIn("S-1: ((&))", guided[0])
        self.assertIn("Energy: 0.80 kcal/mol", guided[0])

    def test_malformed_inputs_fail_without_a_prediction(self):
        malformed = {
            "three": (">a\nG\n>b\nC\n>c\nA\n", "exactly two FASTA records"),
            "empty": (">a\n>b\nC\n", "one nonempty RNA strand"),
            "preamble": ("GG\n>a\nC\n>b\nA\n", "must follow the first record header"),
            "separator": (">a\nG&C\n>b\nA\n", "one nonempty RNA strand"),
        }
        arguments = [
            (["--sequence", "G&&C"], "exactly two strands"),
            (["--sequence", "&GC"], "cutpoint must separate two nonempty RNA strands"),
            (["--sequence", "GC&"], "cutpoint must separate two nonempty RNA strands"),
            (["--sequence", "GN&C"], "only A, C, G and U"),
            (["--sequence", "GC", "--cofold"], "--cofold requires two strands"),
            (["--sequence", "GC", "--cutpoint", "0"], "cutpoint must separate two nonempty RNA strands"),
            (["--sequence", "GC", "--cutpoint", "2"], "cutpoint must separate two nonempty RNA strands"),
            (["--sequence", "G&CC", "--cutpoint", "2"], "cutpoint does not match"),
            (["--sequence", "G&C", "--sequence-two", "A"], "individual sequences without separators"),
        ]
        for name, (text, expected_error) in malformed.items():
            path = self.directory / (name + ".fasta")
            path.write_text(text)
            arguments.append((["--sequence-file", str(path), "--cofold"], expected_error))
        valid_two = self.directory / "two.fasta"
        valid_two.write_text(">a\nGG\n>b\nCC\n")
        arguments.append((["--sequence-file", str(valid_two)], "exactly one FASTA record"))
        for command, expected_error in arguments:
            with self.subTest(arguments=command):
                self.run_cli(command, success=False, expected_error=expected_error)

    def test_length_guard_counts_both_strands_without_counting_the_separator(self):
        for route in self.routes("GGG", "CCC"):
            with self.subTest(route=route):
                self.run_cli(route, maximum=6)
                rejected = self.run_cli(route, maximum=5, success=False,
                                        expected_error="safety limit is 5 nt")
                self.assertIn("input is 6 nt", rejected.stdout + rejected.stderr)
                self.assertIn("safety limit is 5 nt", rejected.stdout + rejected.stderr)


if __name__ == "__main__":
    unittest.main()
