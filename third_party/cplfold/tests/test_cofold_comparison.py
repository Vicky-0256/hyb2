"""Strict reference verdicts and synchronized local-window export regressions.

These tests use synthetic RNA fixtures and controlled independent rescores.
They do not compile a reference executable, contact the network, or fold data.
"""

import csv
import hashlib
import importlib.util
import io
import json
import math
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[3]


def load_comparator():
    name = "cplcofold_comparison_regression_target"
    if name not in sys.modules:
        spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / "compare-cplcofold-linearcofold.py")
        module = importlib.util.module_from_spec(spec)
        sys.modules[name] = module
        spec.loader.exec_module(module)
    return sys.modules[name]


def fixture(identifier="local_pair", sequence_a="GGG", sequence_b="CCC", start_a=11, start_b=51):
    return {
        "id": identifier,
        "name": "Synthetic local RNA windows",
        "biological_provenance_verified": False,
        "sequence_a": sequence_a,
        "sequence_b": sequence_b,
        "strand_a": "RNA_A local window",
        "strand_b": "RNA_B local window",
        "sources": [],
        "notes": "Synthetic comparator regression fixture.",
        "source_regions": {
            "a": {"rna": "RNA_A", "start": start_a, "end": start_a + len(sequence_a) - 1,
                  "source_sequence_length": 1000},
            "b": {"rna": "RNA_B", "start": start_b, "end": start_b + len(sequence_b) - 1,
                  "source_sequence_length": 1000},
        },
    }


def comparison_data(module, cases):
    fixtures = {"schema_version": 1, "description": "Synthetic regression cases", "cases": cases}
    rows = []
    for case in cases:
        first, second = case["sequence_a"], case["sequence_b"]
        count = min(len(first), len(second))
        structure = "(" * count + "." * (len(first) - count) + "&" + "." * (len(second) - count) + ")" * count
        sequence = first + "&" + second
        energy = -2.5 if count == 3 else -5.8
        for beam in (100, 0):
            prediction = {"structure": structure, "energy": energy}
            verdict = module.compare_prediction(sequence, prediction, structure, energy, energy, energy)
            rows.append(dict(case_id=case["id"], biological_provenance_verified=False, beam=beam,
                             listed_sequence=sequence, compared_sequence=sequence,
                             lengths=[len(first), len(second)], reference_swapped_strands=False,
                             sources=[], **verdict))
    return dict(fixtures=fixtures, results=rows, numba_disabled=True,
                parameters=dict(alpha=0.0, beta=0.0, allow_pseudoknot=False, energy_delta=0.0, max_phase1=1),
                run_utc="2026-10-27T12:00:00+00:00",
                fixture_sha256=hashlib.sha256(json.dumps(fixtures, sort_keys=True).encode()).hexdigest())


def fasta_sequences(text):
    records, current = [], []
    for line in text.splitlines():
        if line.startswith(">"):
            if current:
                records.append("".join(current))
                current = []
        elif line.strip():
            current.append(line.strip())
    if current:
        records.append("".join(current))
    return records


class ComparatorVerdictTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = load_comparator()

    def compare(self, **overrides):
        options = dict(sequence="GGG&CCC", prediction={"structure": "(((&)))", "energy": -2.5},
                       reference_structure="(((&)))", reference_energy=-2.5,
                       reference_rescored_energy=-2.5, prediction_rescored_energy=-2.5)
        options.update(overrides)
        return self.module.compare_prediction(
            options["sequence"], options["prediction"], options["reference_structure"],
            options["reference_energy"], options["reference_rescored_energy"],
            options["prediction_rescored_energy"])

    def test_structure_validation_uses_compact_coordinates(self):
        actual = self.module.validate_structure("GGG&CCC", "(((&)))")
        self.assertEqual([tuple(pair) for pair in actual], [(0, 5), (1, 4), (2, 3)])

    def test_wrong_boundary_length_or_brackets_are_rejected(self):
        malformed = {
            "wrong cutpoint": "((&()))", "too long": "(((&))).", "too short": "(((&))",
            "unclosed opener": "(((&))(", "unmatched closer": ")))&(((",
            "unsupported bracket": "(((&))]", "missing separator": "((()))",
            "duplicate separator": "(((&&)))",
        }
        for reason, structure in malformed.items():
            with self.subTest(reason=reason), self.assertRaises(ValueError):
                self.module.validate_structure("GGG&CCC", structure)
            with self.subTest(reason=reason, field="reference"), self.assertRaises(ValueError):
                self.compare(reference_structure=structure)
            with self.subTest(reason=reason, field="prediction"), self.assertRaises(ValueError):
                self.compare(prediction={"structure": structure, "energy": -2.5})

    def test_nonfinite_reported_or_rescored_energy_is_rejected(self):
        for value in (math.nan, math.inf, -math.inf):
            for field in ("reference_energy", "reference_rescored_energy", "prediction_rescored_energy"):
                with self.subTest(value=value, field=field), self.assertRaises(ValueError):
                    self.compare(**{field: value})
            with self.subTest(value=value, field="prediction.energy"), self.assertRaises(ValueError):
                self.compare(prediction={"structure": "(((&)))", "energy": value})

    def test_matching_reported_values_cannot_hide_rescoring_failure(self):
        changesets = ({"reference_rescored_energy": -1.5}, {"prediction_rescored_energy": -1.5},
                      {"reference_rescored_energy": -1.5, "prediction_rescored_energy": -1.5})
        for changes in changesets:
            with self.subTest(changes=changes):
                result = self.compare(**changes)
                self.assertEqual(result["status"], "scoring_mismatch")
                self.assertFalse(result["comparison_ok"])
                self.assertTrue(result["energy_equal"])
                self.assertTrue(result["pair_sets_equal"])

    def test_distinct_structures_are_ties_only_after_both_rescores_match(self):
        # These canonical one-pair structures both score 1.80 with Turner.
        options = dict(prediction={"structure": ".(.&)..", "energy": 1.8},
                       reference_structure="(..&.).", reference_energy=1.8,
                       reference_rescored_energy=1.8, prediction_rescored_energy=1.8)
        result = self.compare(**options)
        self.assertEqual(result["status"], "equal_energy_tie")
        self.assertTrue(result["comparison_ok"])
        self.assertFalse(result["pair_sets_equal"])
        self.assertFalse(result["structure_equal"])
        self.assertTrue(result["reference_scoring_equal"])
        self.assertTrue(result["prediction_scoring_equal"])
        options["reference_rescored_energy"] = 2.8
        result = self.compare(**options)
        self.assertEqual(result["status"], "scoring_mismatch")
        self.assertFalse(result["comparison_ok"])

    def test_valid_identical_predictions_are_successful(self):
        result = self.compare()
        self.assertEqual(result["status"], "identical")
        for field in ("comparison_ok", "energy_equal", "pair_sets_equal", "structure_equal"):
            self.assertTrue(result[field])

    def test_valid_energy_difference_is_not_successful(self):
        result = self.compare(reference_structure="...&...", reference_energy=0.0,
                              reference_rescored_energy=0.0)
        self.assertEqual(result["status"], "energy_difference")
        self.assertFalse(result["comparison_ok"])


class ComparatorCoordinateTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = load_comparator()

    def test_reference_swap_preserves_original_source_region_identity(self):
        case = fixture(sequence_a="GGGG", sequence_b="CCC", start_a=11, start_b=51)
        rows = self.module.mapped_pairs(case, {"compared_sequence": "CCC&GGGG", "structure": "(((&)))."})
        self.assertEqual(len(rows), 3)
        self.assertEqual(rows[0]["rna_1"], "RNA_B")
        self.assertEqual(rows[0]["window_position_1"], 1)
        self.assertEqual(rows[0]["source_position_1"], 51)
        self.assertEqual(rows[0]["rna_2"], "RNA_A")
        self.assertEqual(rows[0]["window_position_2"], 3)
        self.assertEqual(rows[0]["source_position_2"], 13)
        self.assertEqual(rows[0]["pair_type"], "intermolecular")

    def test_homodimer_strands_keep_distinct_source_regions(self):
        case = fixture(sequence_a="GCGC", sequence_b="GCGC", start_a=11, start_b=51)
        rows = self.module.mapped_pairs(case, {"compared_sequence": "GCGC&GCGC", "structure": "((((&))))"})
        self.assertEqual(rows[0]["rna_1"], "RNA_A")
        self.assertEqual(rows[0]["source_position_1"], 11)
        self.assertEqual(rows[0]["rna_2"], "RNA_B")
        self.assertEqual(rows[0]["source_position_2"], 54)

    def test_without_source_regions_no_absolute_positions_are_invented(self):
        case = fixture()
        del case["source_regions"]
        rows = self.module.mapped_pairs(case, {"compared_sequence": "GGG&CCC", "structure": "(((&)))"})
        self.assertEqual(len(rows), 3)
        for row in rows:
            self.assertIsNone(row["source_position_1"])
            self.assertIsNone(row["source_position_2"])

    def test_mapped_pairs_reject_an_unrelated_reference_sequence(self):
        with self.assertRaises(ValueError):
            self.module.mapped_pairs(fixture(), {"compared_sequence": "AAA&UUU", "structure": "(((&)))"})


class ComparatorExportTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = load_comparator()

    def export(self, output, cases):
        files = self.module.build_exports(comparison_data(self.module, cases))
        self.module.write_exports(output, files)
        manifest = json.loads((output / "exports.json").read_text())
        self.assertEqual(set(manifest["files"]), set(files))
        for relative, contents in files.items():
            self.assertEqual((output / relative).read_text(), contents)
            expected_hash = hashlib.sha256((output / relative).read_bytes()).hexdigest()
            self.assertEqual(manifest["files"][relative], expected_hash)
        return files

    def test_rerun_updates_fasta_summary_and_pairs_for_changed_windows(self):
        with tempfile.TemporaryDirectory(prefix="cofold-comparator-regression-") as directory:
            output = Path(directory)
            before = self.export(output, [fixture()])
            second = fixture(sequence_a="GGGG", sequence_b="CCCC", start_a=101, start_b=301)
            after = self.export(output, [second])
            required = {"input.fasta", "summary.json", "pairs-beam100.tsv", "pairs-beam0.tsv"}
            self.assertTrue(required.issubset(after))
            for name in required:
                self.assertNotEqual(before[name], after[name], name)
            self.assertEqual(fasta_sequences(after["input.fasta"]), ["GGGG", "CCCC"])
            summary = json.loads(after["summary.json"])
            self.assertEqual(summary["sequence"], "GGGG&CCCC")
            self.assertEqual(summary["structure"], "((((&))))")
            self.assertEqual(summary["cutpoint"], 4)
            self.assertEqual(summary["strand_lengths"], [4, 4])
            self.assertEqual(summary["source_regions"], second["source_regions"])
            for name in ("pairs-beam100.tsv", "pairs-beam0.tsv"):
                rows = list(csv.DictReader(io.StringIO(after[name]), delimiter="\t"))
                self.assertEqual(len(rows), 4)
                self.assertEqual(int(rows[0]["source_position_1"]), 101)
                self.assertEqual(int(rows[0]["source_position_2"]), 304)

    def test_multi_case_rerun_cleans_obsolete_exports_and_preserves_unowned_files(self):
        with tempfile.TemporaryDirectory(prefix="cofold-comparator-regression-") as directory:
            output = Path(directory)
            old = self.export(output, [fixture(identifier="old_case")])
            # Extra beam output is owned by the previous manifest as well.
            old["pairs-beam7.tsv"] = old["pairs-beam100.tsv"]
            self.module.write_exports(output, old)
            (output / "user-notes.txt").write_text("Keep this unrelated file.\n")
            files = self.export(output, [fixture(identifier="case_a"), fixture(identifier="case_b")])
            for name in ("input.fasta", "summary.json", "pairs-beam100.tsv", "pairs-beam0.tsv", "pairs-beam7.tsv"):
                self.assertFalse((output / name).exists(), name)
            for identifier in ("case_a", "case_b"):
                for name in ("input.fasta", "summary.json", "pairs-beam100.tsv", "pairs-beam0.tsv"):
                    self.assertIn(f"cases/{identifier}/{name}", files)
            self.assertEqual((output / "user-notes.txt").read_text(), "Keep this unrelated file.\n")
            self.export(output, [fixture(identifier="final_case")])
            for identifier in ("case_a", "case_b"):
                for name in ("input.fasta", "summary.json", "pairs-beam100.tsv", "pairs-beam0.tsv"):
                    self.assertFalse((output / "cases" / identifier / name).exists())
            self.assertTrue((output / "input.fasta").exists())
            self.assertEqual((output / "user-notes.txt").read_text(), "Keep this unrelated file.\n")

    def test_legacy_root_auxiliary_files_are_removed_without_a_prior_manifest(self):
        with tempfile.TemporaryDirectory(prefix="cofold-comparator-regression-") as directory:
            output = Path(directory)
            legacy = ("input.fasta", "summary.json", "pairs-beam100.tsv", "pairs-beam0.tsv")
            for name in legacy:
                (output / name).write_text("old untracked auxiliary export\n")
            self.export(output, [fixture(identifier="case_a"), fixture(identifier="case_b")])
            for name in legacy:
                self.assertFalse((output / name).exists())

    def test_staging_failure_keeps_all_previously_published_files(self):
        with tempfile.TemporaryDirectory(prefix="cofold-comparator-regression-") as directory:
            output = Path(directory)
            self.export(output, [fixture()])
            previous = {p.name: p.read_bytes() for p in output.iterdir() if p.is_file()}
            files = self.module.build_exports(comparison_data(self.module, [
                fixture(sequence_a="GGGG", sequence_b="CCCC", start_a=101, start_b=301)]))
            original_write = Path.write_bytes

            def fail_staged_summary(path, contents):
                if path.name == "summary.json":
                    raise OSError("Injected staging failure")
                return original_write(path, contents)

            with mock.patch.object(Path, "write_bytes", new=fail_staged_summary):
                with self.assertRaisesRegex(OSError, "Injected staging failure"):
                    self.module.write_exports(output, files)
            actual = {p.name: p.read_bytes() for p in output.iterdir() if p.is_file()}
            self.assertEqual(previous, actual)

    def test_in_root_symlink_aliases_cannot_collide_with_obsolete_cleanup(self):
        for new_path in ("b/summary.json", "shared/summary.json"):
            with self.subTest(new_path=new_path), tempfile.TemporaryDirectory(
                    prefix="cofold-export-alias-") as directory:
                output = Path(directory)
                shared = output / "shared"
                shared.mkdir()
                (output / "a").symlink_to(shared, target_is_directory=True)
                (output / "b").symlink_to(shared, target_is_directory=True)
                original = b"previous published summary\n"
                (shared / "summary.json").write_bytes(original)
                # A manifest published before symlink rejection was added.
                # Cleanup of a/summary.json must not delete the new target
                # reached through b/summary.json or the genuine shared path.
                manifest = {"schema_version": 1, "files": {
                    "a/summary.json": hashlib.sha256(original).hexdigest()}}
                manifest_path = output / "exports.json"
                manifest_path.write_text(json.dumps(manifest) + "\n")
                previous_manifest = manifest_path.read_bytes()
                with self.assertRaises(ValueError):
                    self.module.write_exports(output, {new_path: "replacement summary\n"})
                self.assertEqual((shared / "summary.json").read_bytes(), original)
                self.assertEqual(manifest_path.read_bytes(), previous_manifest)
                self.assertTrue((output / "a").is_symlink())
                self.assertTrue((output / "b").is_symlink())
                self.assertFalse(list(output.glob(".exports-*")))

    def test_escaping_symlink_is_rejected_before_published_files_change(self):
        with tempfile.TemporaryDirectory(prefix="cofold-export-escape-") as directory:
            base = Path(directory)
            output, outside = base / "output", base / "outside"
            outside.mkdir()
            outside_summary = outside / "summary.json"
            outside_summary.write_text("unrelated external file\n")
            self.export(output, [fixture()])
            previous = {p.name: p.read_bytes() for p in output.iterdir() if p.is_file()}
            (output / "escape").symlink_to(outside, target_is_directory=True)
            with self.assertRaises(ValueError):
                self.module.write_exports(output, {
                    "input.fasta": ">changed\nAAAA\n",
                    "escape/summary.json": "must not escape\n",
                })
            actual = {p.name: p.read_bytes() for p in output.iterdir() if p.is_file()}
            self.assertEqual(actual, previous)
            self.assertEqual(outside_summary.read_text(), "unrelated external file\n")
            self.assertTrue((output / "escape").is_symlink())
            self.assertFalse(list(output.glob(".exports-*")))


if __name__ == "__main__":
    unittest.main()
