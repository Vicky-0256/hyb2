"""Mode/default/input contracts for shared CLI and dedicated cofold entry."""

import contextlib
import importlib
import io
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import types
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[3]


def load_modules():
    if "numba" not in sys.modules:
        shim = types.ModuleType("numba")
        def njit(*args, **kwargs):
            return args[0] if len(args) == 1 and callable(args[0]) and not kwargs else lambda function: function
        shim.njit = njit
        sys.modules["numba"] = shim
    return importlib.import_module("cplfold._internal.single"), importlib.import_module("cplfold._internal.cofold")


class CofoldEntryContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.api, cls.cofold = load_modules()
        cls.cli = importlib.import_module("cplfold.cli")

    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="cofold-entry-contract-")
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)

    def dispatch(self, arguments, dedicated=False):
        with mock.patch.object(self.cli, "two_phase_pseudoknot_fold", return_value=[{"type": "phase1"}]) as fold:
            self.assertEqual(self.cli.main(arguments, cofold_entry=dedicated), 0)
        return fold.call_args.kwargs

    def reject(self, arguments, text, dedicated=True):
        stderr = io.StringIO()
        with mock.patch.object(self.cli, "two_phase_pseudoknot_fold") as fold, \
             contextlib.redirect_stderr(stderr), self.assertRaises(SystemExit) as raised:
            self.cli.main(arguments, cofold_entry=dedicated)
        fold.assert_not_called()
        self.assertIn(text, stderr.getvalue() + str(raised.exception))
        self.assertNotIn("Traceback", stderr.getvalue())

    def test_defaults_preserve_single_strand_and_unify_all_cofold_routes(self):
        single = self.dispatch(["-s", "GAAAC", "-q"])
        self.assertEqual(single["alpha"], 0.5)
        for dedicated in (False, True):
            with self.subTest(dedicated=dedicated):
                values = self.dispatch(["-s", "GG&CC", "-q"], dedicated)
                self.assertEqual(values["alpha"], 0.0)
                self.assertEqual(values["beta"], 0.0)
                self.assertTrue(values["allow_pseudoknot"])
                self.assertEqual((values["max_phase1"], values["max_phase2"], values["energy_delta"]), (10, 5, 5.0))
        # Single-strand input follows the cofold policy: negative alpha/beta are rejected.
        self.reject(["-s", "GAAAC", "--alpha", "-0.5"], "--alpha must be nonnegative", dedicated=False)
        self.reject(["-s", "GAAAC", "--beta", "-0.3"], "--beta must be nonnegative", dedicated=False)

    def test_distinct_model_defaults(self):
        if str(ROOT / "pipeline") not in sys.path:
            sys.path.insert(0, str(ROOT / "pipeline"))
        from hyb2analysis.engine_parameters import DEFAULT_MODELS, engine_parameters
        with mock.patch.dict(DEFAULT_MODELS, cplfold="CC09", cplcofold="DP03"):
            for host in ("desktop", "web"):
                engines = engine_parameters(host)
                self.assertEqual(engines["cplfold"]["defaultModel"], "CC09")
                self.assertEqual(engines["cplcofold"]["defaultModel"], "DP03")
            self.assertEqual(self.dispatch(["-s", "GAAAC"])["energy_model"], "CC09")
            self.assertEqual(self.dispatch(["-s", "GG&CC"])["energy_model"], "DP03")
            self.assertEqual(self.dispatch(["-s", "GG&CC", "--model", "DP09"], True)["energy_model"], "DP09")
        with mock.patch.dict(DEFAULT_MODELS, cplcofold="CC09"), self.assertRaisesRegex(ValueError, "cplcofold.*CC09"):
            engine_parameters("web")

    def test_modes_have_explicit_overrides_and_legacy_flag_keeps_candidate_defaults(self):
        standard = self.dispatch(["-s", "GG&CC", "--mode", "standard"], True)
        self.assertFalse(standard["allow_pseudoknot"])
        self.assertEqual((standard["max_phase1"], standard["energy_delta"]), (1, 0.0))
        changed = self.dispatch(["-s", "GG&CC", "--mode", "standard", "--max-phase1", "3", "--delta", "2"], True)
        self.assertEqual((changed["max_phase1"], changed["energy_delta"]), (3, 2.0))
        legacy = self.dispatch(["-s", "GG&CC", "--no-pseudoknot"], True)
        self.assertFalse(legacy["allow_pseudoknot"])
        self.assertEqual((legacy["max_phase1"], legacy["energy_delta"]), (10, 5.0))
        self.reject(["-s", "GG&CC", "--mode", "pseudoknot", "--no-pseudoknot"], "conflicts")

    def test_api_optional_mode_preserves_omitted_behavior_and_rejects_conflicts(self):
        for mode, kwargs, expected in (
            (None, {}, {}),
            ("standard", {}, dict(allow_pseudoknot=False, max_phase1=1, energy_delta=0.0)),
            ("pseudoknot", {}, dict(allow_pseudoknot=True)),
            ("standard", dict(max_phase1=3, energy_delta=2.0), dict(allow_pseudoknot=False, max_phase1=3, energy_delta=2.0)),
        ):
            with self.subTest(mode=mode, kwargs=kwargs), mock.patch.object(self.cofold, "two_phase_cofold", return_value=[]) as fold:
                self.api.cofold("GG", "CC", mode=mode, **kwargs)
                self.assertEqual(fold.call_args.args, ("GGCC",))
                self.assertEqual(fold.call_args.kwargs, dict(cutpoint=2, **expected))
        for mode, enabled in (("standard", True), ("pseudoknot", False), ("unknown", True)):
            with self.subTest(mode=mode), self.assertRaises(ValueError):
                self.api.cofold("GG", "CC", mode=mode, allow_pseudoknot=enabled)

    def test_standard_api_matches_explicit_existing_api(self):
        actual = self.api.cofold("GGG", "CCC", mode="standard", verbose=False)
        expected = self.api.cofold("GGG", "CCC", allow_pseudoknot=False, max_phase1=1, energy_delta=0.0, verbose=False)
        self.assertEqual(actual, expected)
        self.assertEqual(actual[0]["structure"], "(((&)))")

    def test_known_parameters_and_model_errors_are_rejected_before_search(self):
        cases = [(["--beam", "-1"], "--beam"), (["--max-phase1", "0"], "--max-phase1"),
                 (["--max-phase2", "0"], "--max-phase2"), (["--alpha", "nan"], "finite"),
                 (["--beta", "inf"], "finite"), (["--alpha", "-1"], "nonnegative"),
                 (["--beta", "-1"], "nonnegative"), (["--delta", "-1"], "nonnegative"),
                 (["--model", "CC09"], "invalid choice")]
        for options, expected in cases:
            with self.subTest(options=options):
                self.reject(["-s", "GG&CC", *options], expected)
        self.reject(["-s", "GG&CC", "--model", "CC09"], "supports DP03 or DP09", dedicated=False)
        self.assertEqual(self.dispatch(["-s", "GAAAC", "--model", "CC09"])["energy_model"], "CC09")

    def test_dedicated_entry_and_direct_fasta_validation(self):
        self.reject(["-s", "GGCC"], "cplcofold requires two strands")
        for value, error in (("G&&C", "exactly two strands"), ("GN&C", "only A, C, G and U"), ("&GC", "two nonempty RNA strands")):
            with self.subTest(value=value):
                self.reject(["-s", value], error)
        two = self.directory / "two.fasta"
        two.write_text(">A\nGG\n>B\nCC\n")
        actual = self.dispatch(["--sequence-file", str(two)], True)
        self.assertEqual((actual["seq"], actual["cutpoint"]), ("GGCC", 2))
        self.reject(["--sequence-file", str(two), "--sequence-two", "A"], "individual sequences without separators")
        self.reject(["--sequence-file", str(two), "-s", "GG&CC"], "not allowed with argument")

    def test_bonus_file_preserves_exported_scores_in_both_triangles(self):
        path = self.directory / "bonus.tsv"
        path.write_text("1\t4\t4.87533164\n2\t3\t1.0000000000000002\n")
        scores = self.cli.load_bonus_matrix_file(str(path), 4).tolist()
        for i, j, expected in ((0, 3, 4.87533164), (1, 2, 1.0000000000000002)):
            self.assertEqual((scores[i][j], scores[j][i]), (expected, expected))

    def test_bonus_file_errors_are_clean_and_float32_overflow_is_rejected(self):
        self.reject(["-s", "GG&CC", "--bonus-matrix-file", str(self.directory / "missing")], "was not found")
        for index, (body, error) in enumerate((
            ("1\t4\tNaN\n", "non-finite"), ("1\t4\t1e100\n", "float32 range"),
            ("0\t4\t2\n", "coordinates from 1 to 4"), ("1\t4\tbad\n", "invalid coordinate or score"),
            ("1\t4\t2\n4\t1\t2\n", "repeats coordinate pair"),
        )):
            path = self.directory / f"bad-{index}.tsv"
            path.write_text(body)
            with self.subTest(body=body):
                self.reject(["-s", "GG&CC", "--bonus-matrix-file", str(path)], error)

    def test_only_expected_input_errors_are_simplified(self):
        for error in (RuntimeError("internal defect"), ValueError("internal value defect")):
            with self.subTest(error=type(error).__name__), mock.patch.object(self.cli, "two_phase_pseudoknot_fold", side_effect=error):
                with self.assertRaises(type(error)):
                    self.cli.main(["-s", "GG&CC"], cofold_entry=True)
        for arguments, dedicated in ((["-s", "GG&CC"], True), (["-s", "GAAAC"], False)):
            with self.subTest(arguments=arguments), \
                 mock.patch.object(self.cli, "two_phase_pseudoknot_fold", side_effect=self.cofold.CofoldInputError("Scaled bonus exceeds range")), \
                 contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as error:
                self.cli.main(arguments, cofold_entry=dedicated)
            self.assertEqual(error.exception.code, 2)

    def test_single_strand_cli_reports_invalid_bonus_matrix_as_usage_error(self):
        path = self.directory / "bonus.tsv"
        path.write_text("1\t5\t2\n")
        stderr = io.StringIO()
        with mock.patch.object(self.api, "validate_fold_inputs", side_effect=self.cofold.CofoldInputError("Bonus matrix must be symmetric")), \
             contextlib.redirect_stderr(stderr), self.assertRaises(SystemExit) as raised:
            self.cli.main(["-s", "GAAAC", "--bonus-matrix-file", str(path), "-q"])
        self.assertEqual(raised.exception.code, 2)
        self.assertIn("Bonus matrix must be symmetric", stderr.getvalue())
        self.assertNotIn("Traceback", stderr.getvalue())


class SharedInputValidationTests(unittest.TestCase):
    """fold() applies one validator to both routes: same error type and message."""

    SINGLE = "GGGAAACCC"
    PAIR = ("GGGA", "AACCC")

    @classmethod
    def setUpClass(cls):
        cls.api, cls.cofold = load_modules()
        cls.cplfold = importlib.import_module("cplfold")
        cls.parser_module = importlib.import_module("cplfold._internal.parser")
        cls.cofold_parser = importlib.import_module("cplfold._internal.cofold_parser")

    def routes(self, **kwargs):
        """Yield (route name, callable) for the single, '&' and partner routes."""
        first, second = self.PAIR
        yield "single", lambda: self.cplfold.fold(self.SINGLE, **kwargs)
        yield "ampersand", lambda: self.cplfold.fold(first + "&" + second, **kwargs)
        yield "partner", lambda: self.cplfold.fold(first, partner=second, **kwargs)

    def assert_rejected_before_search(self, text, **kwargs):
        messages = {}
        with mock.patch.object(self.parser_module.BeamCKYParserHyper, "parse_subopt") as single, \
             mock.patch.object(self.cofold_parser.BeamCKYCofoldParser, "parse_subopt") as pair:
            for route, call in self.routes(**kwargs):
                with self.subTest(route=route, **{key: repr(value)[:40] for key, value in kwargs.items()}):
                    with self.assertRaises(self.cplfold.CofoldInputError) as raised:
                        call()
                    self.assertIsInstance(raised.exception, ValueError)
                    self.assertIn(text, str(raised.exception))
                    messages[route] = str(raised.exception)
            single.assert_not_called()
            pair.assert_not_called()
        return messages

    def test_public_error_is_the_shared_value_error(self):
        validation = importlib.import_module("cplfold._internal.validation")
        self.assertIs(self.cplfold.CofoldInputError, self.cofold.CofoldInputError)
        self.assertIs(self.cplfold.CofoldInputError, validation.CofoldInputError)
        self.assertTrue(issubclass(self.cplfold.CofoldInputError, ValueError))

    def test_wrong_sized_bonus_matrix_is_rejected(self):
        import numpy as np
        for size in (3, 5, 10):
            for alpha in (0.0, 1.0):
                self.assert_rejected_before_search("Bonus matrix must be N by N", bonus=np.ones((size, size)), alpha=alpha)
        self.assert_rejected_before_search("Bonus matrix must be N by N", bonus=np.ones(9 * 9 + 1), alpha=1.0)

    def test_nonfinite_or_asymmetric_bonus_matrix_is_rejected(self):
        import numpy as np
        for value in (float("nan"), float("inf"), -float("inf")):
            matrix = np.zeros((9, 9))
            matrix[0, 8] = matrix[8, 0] = value
            self.assert_rejected_before_search("finite values", bonus=matrix, alpha=1.0)
        matrix = np.zeros((9, 9))
        matrix[0, 8] = 1.0
        self.assert_rejected_before_search("symmetric", bonus=matrix, alpha=1.0)

    def test_sequence_type_and_emptiness_are_rejected(self):
        fold = self.cplfold.fold
        for text, calls in (
            ("must be a string", (lambda: fold(123), lambda: fold(None), lambda: fold("GGGA", partner=123))),
            ("must not be empty", (lambda: fold(""), lambda: fold("  \n"), lambda: fold("GGGA", partner=""))),
            ("two nonempty RNA strands", (lambda: fold("GGGA&"),)),
            ("only A, C, G and U", (lambda: fold("GGGXAACCC"), lambda: fold("GGXA&CC"))),
        ):
            for index, call in enumerate(calls):
                with self.subTest(text=text, index=index), \
                     self.assertRaisesRegex(self.cplfold.CofoldInputError, text):
                    call()

    def test_numeric_options_follow_the_cofold_policy(self):
        for name, value, text in (
            ("beam_size", -1, "beam_size must be an integer >= 0"),
            ("beam_size", 2.5, "beam_size must be an integer >= 0"),
            ("max_phase1", 0, "max_phase1 must be an integer >= 1"),
            ("max_phase2", 0, "max_phase2 must be an integer >= 1"),
            ("alpha", -1.0, "alpha must be finite and nonnegative"),
            ("alpha", float("nan"), "alpha must be finite and nonnegative"),
            ("beta", -1.0, "beta must be finite and nonnegative"),
            ("energy_delta", -1.0, "energy_delta must be finite and nonnegative"),
            ("energy_delta", float("inf"), "energy_delta must be finite and nonnegative"),
            ("pseudoknots", "yes", "allow_pseudoknot must be a boolean"),
        ):
            messages = self.assert_rejected_before_search(text, **{name: value})
            self.assertEqual(len(set(messages.values())), 1, messages)

    def test_invalid_energy_model_is_rejected_even_without_pseudoknots(self):
        for pseudoknots in (True, False):
            messages = self.assert_rejected_before_search("supports", energy_model="XX", pseudoknots=pseudoknots)
            self.assertEqual(messages["single"], "CPLfold supports CC06, CC09, DP03, DP09 or RE")
            self.assertEqual(messages["partner"], "Cofold supports DP03 or DP09")

    def test_single_strand_large_scaled_bonus_is_rejected(self):
        import numpy as np
        with mock.patch.object(self.parser_module.BeamCKYParserHyper, "parse_subopt") as search:
            for bonus, alpha in ((1e17, 1.0), (1e39, 1e-40)):
                with self.subTest(bonus=bonus), self.assertRaises(self.cplfold.CofoldInputError):
                    self.cplfold.fold(self.SINGLE, bonus=np.full((9, 9), bonus), alpha=alpha)
            search.assert_not_called()

    def test_both_routes_normalise_case_whitespace_and_model_name(self):
        with mock.patch.object(self.parser_module.BeamCKYParserHyper, "parse_subopt", return_value=[("(((...)))", 1.0)]) as search:
            spaced = self.cplfold.fold(" ggg aaa\tccc\n", energy_model="dp09", pseudoknots=False)
            plain = self.cplfold.fold("GGGAAACCC", energy_model="DP09", pseudoknots=False)
        self.assertEqual([p.as_dict() for p in spaced], [p.as_dict() for p in plain])
        self.assertEqual(search.call_args.args[0], "GGGAAACCC")
        self.assertEqual(self.cplfold.fold("tttaaa", pseudoknots=False, beam_size=0, max_phase1=1)[0].structure, "......")
        pair = self.cplfold.fold("ggg a & u ccc", energy_model="dp09", pseudoknots=False)
        self.assertEqual(pair[0].structure, "((((&))))")
        matrix = [[0.0] * 9 for _ in range(9)]
        self.assertEqual(self.cplfold.fold(self.SINGLE, bonus=matrix, alpha=1)[0].as_dict(),
                         self.cplfold.fold(self.SINGLE)[0].as_dict())


class CofoldEntryProcessTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="cofold-entry-process-")
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.environment = os.environ.copy()
        for name in ("PYTHONPATH", "PYTHONHOME"):
            self.environment.pop(name, None)
        self.environment.update(NUMBA_DISABLE_JIT="1", PYTHONDONTWRITEBYTECODE="1",
                                NUMBA_CACHE_DIR=str(self.directory / "cache"), HYB2_CPLFOLD_MAX_NT="1000")

    def run_cli(self, args, entry="bin/cplcofold", success=True, module=None):
        command = ["-m", module] if module else [str(ROOT / entry)]
        result = subprocess.run([sys.executable, *command, *args], cwd=ROOT / "third_party" / "cplfold" if module else ROOT,
                                env=self.environment, capture_output=True, text=True, timeout=60)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        self.assertNotIn("Traceback", result.stdout + result.stderr)
        return result

    @staticmethod
    def report(output):
        return output[output.index("Sequence: "):]

    def test_dedicated_help_exposes_real_file_and_mode_contract(self):
        output = self.run_cli(["--help"]).stdout
        for text in ("--sequence-file", "--mode {standard,pseudoknot}", "--model {DP03,DP09}", "default mode: pseudoknot"):
            self.assertIn(text, output)
        self.assertNotIn("--model {DP03,DP09,CC06", output)

    def test_all_dedicated_input_routes_and_direct_python_file_entry_agree(self):
        two, one, joined = (self.directory / name for name in ("two.fa", "one.fa", "joined.fa"))
        two.write_text(">A\nGGG\n>B\nCCC\n")
        one.write_text(">A\nGGG\n")
        joined.write_text(">joined\nGGGCCC\n")
        routes = [["-s", "GGG&CCC"], ["-s", "GGG", "--sequence-two", "CCC"],
                  ["-s", "GGGCCC", "--cutpoint", "3"], ["--sequence-file", str(two)],
                  ["--sequence-file", str(one), "--sequence-two", "CCC"],
                  ["--sequence-file", str(joined), "--cutpoint", "3"]]
        reports = [self.report(self.run_cli([*route, "--mode", "standard"]).stdout) for route in routes]
        reports.append(self.report(self.run_cli(["--sequence-file", str(two), "--mode", "standard"],
                                               module="cplfold._internal.cofold").stdout))
        self.assertTrue(all(report == reports[0] for report in reports))
        self.assertIn("S-1: (((&)))", reports[0])
        self.assertIn("Secondary Structures: 1", reports[0])

    def test_actual_reads_default_is_zero_for_both_cofold_commands(self):
        matrix = self.directory / "cofold.tsv"
        matrix.write_text("# sequence_length=4\n1\t4\t5\n2\t3\t5\n")
        args = ["-s", "GG&CC", "--mode", "standard", "--bonus-matrix-file", str(matrix)]
        dedicated = self.report(self.run_cli(args).stdout)
        legacy = self.report(self.run_cli(args, entry="bin/cplfold").stdout)
        self.assertEqual(dedicated, legacy)
        self.assertIn("S-1: ..&..", dedicated)
        guided = self.report(self.run_cli([*args, "--alpha", "1"]).stdout)
        self.assertIn("S-1: ((&))", guided)
        self.assertNotEqual(dedicated, guided)
        _, backend = load_modules()
        import numpy as np
        bonus = np.zeros((4, 4))
        for i, j in ((0, 3), (1, 2)):
            bonus[i, j] = bonus[j, i] = 5
        self.assertEqual(backend.cofold("GG", "CC", mode="standard", bonus_matrix=bonus, verbose=False)[0]["structure"], "..&..")
        self.assertEqual(backend.cofold("GG", "CC", mode="standard", bonus_matrix=bonus, alpha=1, verbose=False)[0]["structure"], "((&))")

    def test_actual_single_strand_reads_default_remains_half(self):
        matrix = self.directory / "single.tsv"
        matrix.write_text("# sequence_length=5\n1\t5\t20\n")
        args = ["-s", "GAAAC", "--no-pseudoknot", "--max-phase1", "1", "--delta", "0", "--bonus-matrix-file", str(matrix)]
        default = self.report(self.run_cli(args, entry="bin/cplfold").stdout)
        explicit = self.report(self.run_cli([*args, "--alpha", "0.5"], entry="bin/cplfold").stdout)
        zero = self.report(self.run_cli([*args, "--alpha", "0"], entry="bin/cplfold").stdout)
        self.assertEqual(default, explicit)
        self.assertIn("Pseudoknot Structures: disabled", default)
        self.assertIn("S-1: (...)", default)
        self.assertIn("S-1: .....", zero)

    def test_quiet_output_and_known_error_subprocesses(self):
        output = self.directory / "result.txt"
        result = self.run_cli(["-s", "GGG&CCC", "--mode", "standard", "-q", "-o", str(output)])
        self.assertEqual((result.stdout, result.stderr), ("", ""))
        self.assertIn("S-1: (((&)))", output.read_text())
        for args in (["-s", "GGGCCC"], ["-s", "GG&CC", "--alpha", "nan"],
                     ["-s", "GG&CC", "--mode", "pseudoknot", "--no-pseudoknot"]):
            with self.subTest(args=args):
                self.run_cli(args, success=False)
        matrix = self.directory / "huge.tsv"
        matrix.write_text("# sequence_length=4\n1\t4\t5\n")
        rejected = self.run_cli(["-s", "GG&CC", "--bonus-matrix-file", str(matrix), "--alpha", "1e308"], success=False)
        self.assertIn("Scaled bonus", rejected.stderr)
        rejected = self.run_cli(["-s", "GGGG&CCCC", "--beta", "1e308", "--max-phase1", "2",
                                 "--max-phase2", "1", "--delta", "10"], success=False)
        self.assertIn("Effective energy", rejected.stderr)


if __name__ == "__main__":
    unittest.main()
