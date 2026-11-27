import io
import json
import os
import sys
import random
import tempfile
import unittest
import zipfile
from pathlib import Path

from desktop.runtime.bridge import handle
from desktop.runtime.project import new_project

ROOT = Path(__file__).resolve().parents[3]
GENE_A, GENE_B = "TEST_A_virusRNA", "TEST_B_virusRNA"


def _random(length, seed):
    rng = random.Random(seed)
    return "".join(rng.choice("ACGU") for _ in range(length))


def _row(name, g1, s1, e1, g2, s2, e2, energy="-10.5", end="\n"):
    return f"{name}\tACGU\t{energy}\t{g1}\t1\t20\t{s1}\t{e1}\t1e-5\t{g2}\t21\t40\t{s2}\t{e2}\t1e-5\t\t-10\tType_1{end}"


class BatchEnergyTests(unittest.TestCase):
    """batch_energy_run through the bridge on a one-sample project."""

    def setUp(self):
        from desktop.runtime.samples import add_reference, add_sample

        self.tmp = tempfile.TemporaryDirectory()
        base = Path(self.tmp.name)
        self.project = base / "p.hyb2proj"
        new_project(self.project, "Batch energy")
        self.seq_a, self.seq_b = _random(400, 1), _random(300, 2).replace("U", "T")  # B written as DNA
        ref = base / "ref.fa"
        ref.write_text(f">{GENE_A}\n{self.seq_a}\n>{GENE_B}\n{self.seq_b}\n")
        reference = add_reference(self.project, ref, mode="linked")["reference"]
        self.lines = [
            "# a comment line\n",
            _row("1_1", GENE_A, 10, 30, GENE_A, 200, 222),
            _row("2_1", GENE_A, 10, 30, "MISSING_RNA", 5, 20),
            "not a hyb line\n",
            _row("3_1", GENE_A, 390, 420, GENE_A, 1, 20),  # past the end of A
            _row("4_6", GENE_A, 50, 70, GENE_B, 100, 125, energy=".", end="\r\n"),
        ]
        self.hyb = base / "s.hyb"
        self.hyb.write_bytes("".join(self.lines).encode())
        self.sample = add_sample(self.project, self.hyb, name="s1", reference_id=reference["id"], gene_id=GENE_A,
                                 mode="linked")["id"]

    def tearDown(self):
        self.tmp.cleanup()

    def _run(self, task_id, **extra):
        run = self.project / "runs" / task_id
        run.mkdir(parents=True)
        request = {"operation": "batch_energy_run", "project_path": str(self.project), "task_id": task_id,
                   "run_dir": str(run), "sample_id": self.sample}
        request.update(extra)
        return handle(request, repo_root=ROOT), run

    def _audit(self, run):
        lines = (run / "audit.tsv").read_text().splitlines()
        self.assertEqual(lines[0], "line\tid\tstatus\told_energy\tnew_energy\tnote")
        return [line.split("\t") for line in lines[1:]]

    def test_rnacofold_all_records(self):
        from desktop.runtime.energy import cofold

        result, run = self._run("batch-energy-1", parameters={"engine": "rnacofold"})
        self.assertTrue(result["ok"], result)
        self.assertEqual((result["summary"]["updated"], result["summary"]["unchanged"]), (2, 2))
        self.assertTrue(result["message"].endswith("; 2 valid rows unchanged. Comments and unparsed source lines are preserved. "
                                                   "Download the audit for details."), result["message"])
        one =cofold(self.seq_a[9:30], self.seq_a[199:222])
        four = cofold(self.seq_a[49:70], self.seq_b[99:125].replace("T", "U"))
        out = (run / "energies.hyb").read_bytes().decode()
        want = list(self.lines)
        want[1] = want[1].replace("\t-10.5\t", "\t%.2f\t" % one, 1)
        want[5] = want[5].replace("\t.\t", "\t%.2f\t" % four, 1)
        self.assertEqual(out, "".join(want))  # comments, invalid rows and the CRLF kept
        audit = self._audit(run)
        self.assertEqual([(r[0], r[1], r[2]) for r in audit],
                         [("2", "1_1", "updated"), ("3", "2_1", "unchanged"), ("5", "3_1", "unchanged"), ("6", "4_6", "updated")])
        self.assertEqual(audit[1][5], "No mapped FASTA for MISSING_RNA")
        self.assertEqual(audit[2][5], "Interval exceeds FASTA length")
        self.assertEqual((audit[3][3], audit[3][4]), (".", "%.2f" % four))
        meta = json.loads((run / "parameters.json").read_text())
        self.assertEqual((meta["engine"], meta["records"], meta["minimumLoop"], meta["updated"]), ("ViennaRNA RNAcofold", "all", 3, 2))
        with zipfile.ZipFile(run / "hyb2-energies-rnacofold.zip") as zf:
            self.assertEqual(sorted(zf.namelist()), ["hyb2-energies-rnacofold/audit.tsv", "hyb2-energies-rnacofold/energies.hyb",
                                                     "hyb2-energies-rnacofold/parameters.json"])
            self.assertEqual(zf.read("hyb2-energies-rnacofold/energies.hyb").decode(), out)
        self.assertEqual(json.loads((run / "result.json").read_text())["outputs"]["hyb"], str((run / "energies.hyb").resolve()))
        self.assertTrue((run / "progress.json").is_file())

    def test_changed_source_does_not_publish_energies(self):
        import gzip
        from unittest.mock import patch

        from desktop.runtime import batch_energy
        from desktop.runtime.project import load_manifest
        from desktop.runtime.samples import add_sample

        original = list(self.lines)
        original[1] = original[1].replace("\t-10.5\t", "\t.\t", 1)
        data = "".join(original).encode()
        # Both computed rows have the same old energy. Checking only column 3
        # misses this exchange and assigns each row the other row's energy.
        swapped = list(original)
        swapped[1], swapped[5] = swapped[5], swapped[1]
        variants = {
            "unchanged": data,
            "reordered": "".join(swapped).encode(),
            "truncated": "".join(original[:-1]).encode(),
            "unmapped-row": data.replace(b"MISSING_RNA", b"OTHER_RNA"),
            "comment": data.replace(b"a comment", b"a changed comment"),
        }
        fold = batch_energy.fold_pairs
        plain_sample = self.sample
        reference = load_manifest(self.project)["references"][0]["id"]
        for compressed in (False, True):
            source = self.hyb.with_suffix(".hyb.gz") if compressed else self.hyb
            source.write_bytes(gzip.compress(data) if compressed else data)
            self.sample = add_sample(self.project, source, name="gzip", reference_id=reference, gene_id=GENE_A,
                                     mode="linked")["id"] if compressed else plain_sample
            for name, changed in variants.items():
                with self.subTest(compressed=compressed, change=name):
                    source.write_bytes(gzip.compress(data) if compressed else data)
                    energies = []

                    def change_after_fold(root, engine, pairs):
                        values = fold(root, engine, pairs)
                        energies.extend(values)
                        source.write_bytes(gzip.compress(changed) if compressed else changed)
                        return values

                    with patch.object(batch_energy, "fold_pairs", change_after_fold):
                        result, run = self._run(f"changed-{compressed}-{name}", parameters={"engine": "rnacofold"})
                    self.assertEqual(len(energies), 2)
                    self.assertNotEqual(energies[0], energies[1])
                    self.assertEqual(gzip.decompress(source.read_bytes()) if compressed else source.read_bytes(), changed)
                    if name == "unchanged":
                        self.assertTrue(result["ok"], result)
                        self.assertEqual([row[4] for row in self._audit(run) if row[2] == "updated"],
                                         [f"{value:.2f}" for value in energies])
                        continue
                    self.assertFalse(result["ok"], result)
                    self.assertIn("changed", result["error"].lower())
                    for output in ("energies.hyb", ".energies.hyb.tmp", "audit.tsv", "parameters.json",
                                   "hyb2-energies-rnacofold.zip"):
                        self.assertFalse((run / output).exists(), output)
                    self.assertFalse(json.loads((run / "result.json").read_text())["ok"])

    def test_batch_uses_the_selected_reference_match_for_each_arm(self):
        from desktop.runtime.energy import cofold
        from desktop.runtime.project import load_manifest

        ref = load_manifest(self.project)["references"][0]["id"]
        for rna, target in [(GENE_A, GENE_B), (GENE_B, "")]:
            handle({"operation": "set_reference_match", "project_path": str(self.project),
                    "reference_id": ref, "rna": rna, "sequence": target}, repo_root=ROOT)
        result, run = self._run("mapped", parameters={"engine": "rnacofold"})
        self.assertEqual(result["summary"]["updated"], 1)
        audit = self._audit(run)
        self.assertEqual(audit[0][4], "%.2f" % cofold(self.seq_b[9:30].replace("T", "U"), self.seq_b[199:222].replace("T", "U")))
        self.assertIn("not matched", audit[3][5])

    def test_only_selected_arms_follow_the_shared_alphabet(self):
        from unittest.mock import patch

        from desktop.runtime.energy import cofold
        from desktop.runtime.samples import add_sample
        from hyb2analysis.fold_text import FOLD_TEXT

        ref = self.hyb.with_name("toy.fa")
        ref.write_text(">toy\nNacgggTcccgc?\n")
        rows = [
            _row("1_1", "toy", 2, 7, "toy", 8, 12),  # T in arm 1
            _row("2_1", "toy", 2, 6, "toy", 7, 12),  # T in arm 2
            _row("3_1", "toy", 2, 6, "toy", 8, 12),  # T, N and ? outside the windows
        ]
        hyb = ref.with_suffix(".hyb")
        hyb.write_bytes("".join(rows).encode())
        sample = add_sample(self.project, hyb, reference=ref, gene_id="toy")["id"]
        with patch.dict(FOLD_TEXT, alphabet="ACG"):
            result, run = self._run("batch-windows", sample_id=sample, parameters={"engine": "rnacofold"})

        self.assertTrue(result["ok"], result)
        energy = "%.2f" % cofold("ACGGG", "CCCGC")
        note = "Unsupported or ambiguous reference bases"
        self.assertEqual(self._audit(run), [["1", "1_1", "unchanged", "-10.5", "-10.5", note],
                                            ["2", "2_1", "unchanged", "-10.5", "-10.5", note],
                                            ["3", "3_1", "updated", "-10.5", energy, ""]])
        expected = rows[0] + rows[1] + rows[2].replace("\t-10.5\t", f"\t{energy}\t", 1)
        self.assertEqual((run / "energies.hyb").read_bytes(), expected.encode())

    def test_parallel_workers_match(self):
        one, run_one = self._run("batch-energy-1", parameters={"engine": "rnacofold"})
        two, run_two = self._run("batch-energy-2", parameters={"engine": "rnacofold"}, workers=2)
        self.assertTrue(one["ok"] and two["ok"], (one, two))
        self.assertEqual((run_one / "energies.hyb").read_bytes(), (run_two / "energies.hyb").read_bytes())

    def test_filtered_records_use_the_index(self):
        built = handle({"operation": "analysis_index_build", "project_path": str(self.project), "sample_id": self.sample,
                        "task_id": "index-1", "run_dir": str(self._mkrun("index-1"))}, repo_root=ROOT)
        self.assertTrue(built["ok"], built)
        result, run = self._run("batch-energy-1", parameters={"engine": "rnacofold", "records": "filtered",
                                                               "filters": {"min_reads": 3}})
        self.assertTrue(result["ok"], result)
        audit = self._audit(run)
        self.assertEqual([r[2] for r in audit], ["unchanged", "unchanged", "unchanged", "updated"])
        self.assertEqual(audit[0][5], "not selected by the Interactions filters")
        self.assertEqual(json.loads((run / "parameters.json").read_text())["filters"], {"min_reads": 3})
        # the Interactions page's RNA with arm positions (web semantics): the
        # RNA on either arm, each range on its own arm
        for task, filters, want in (("batch-energy-3", {"gene": GENE_B, "region": {"x": [50, 60]}}, "4_6"),
                                    ("batch-energy-4", {"gene": GENE_A, "region": {"y": [195, 200]}}, "1_1")):
            result, run = self._run(task, parameters={"engine": "rnacofold", "records": "filtered", "filters": filters})
            self.assertTrue(result["ok"], result)
            self.assertEqual([r[1] for r in self._audit(run) if r[2] == "updated"], [want], filters)
        # a .hyb changed after its index was built is refused
        self.hyb.write_bytes(self.hyb.read_bytes() + b"\n")
        stale, _ = self._run("batch-energy-2", parameters={"records": "filtered", "filters": {}})
        self.assertFalse(stale["ok"])
        self.assertIn("rebuild the index", stale["error"])

    def test_cplcofold_matches_the_shared_module(self):
        from unittest.mock import patch
        from desktop.runtime.structure import load_cplfold_web

        # 44 nt, then 550 nt: over CPLcofold's 500 nt limit (RNAcofold's is 2,000)
        self.hyb.write_bytes((self.lines[0] + self.lines[1] + _row("5_1", GENE_A, 1, 300, GENE_B, 1, 250)).encode())
        module = load_cplfold_web(ROOT)
        with patch.object(module, "fold", wraps=module.fold) as fold:
            result, run = self._run("batch-energy-1", parameters={"engine": "cplcofold"})
        self.assertTrue(result["ok"], result)
        fold.assert_called_once()
        self.assertEqual(fold.call_args.kwargs, {"host": "desktop", "hard_max_length": 500})
        # The advertised batch preset is independent of the regular cofold defaults.
        preset = dict(beamSize=200, energyDelta=5, maxPhase1=3, energyModel="DP09", alpha=0, beta=0)
        payload, = fold.call_args.args
        self.assertEqual({key: payload[key] for key in preset}, preset)
        audit = self._audit(run)
        self.assertEqual([r[2] for r in audit], ["updated", "unchanged"])
        self.assertEqual(audit[1][5], "Combined arms exceed the 500 nt CPLcofold limit")
        want = module.fold({"sequence": self.seq_a[9:30], "sequenceTwo": self.seq_a[199:222], "evidenceMode": "none",
                            "evidenceArms": [], "allowPseudoknot": False, "maxSequenceLength": 500, **preset})
        self.assertEqual(audit[0][4], "%.2f" % round(want["energy"], 2))
        meta = json.loads((run / "parameters.json").read_text())
        self.assertEqual((meta["energyModel"], meta["pseudoknots"], meta["maximumCombinedArmLength"]), ("CPLfold DP09", False, 500))
        self.assertEqual(meta["cplcofold"], dict(preset, evidence="none"))

    def test_rnaduplex_engine(self):
        """RNAduplex, as the pipeline's dG column (bundled ViennaRNA); arms that
        form no pair keep the row's energy."""
        from unittest import mock

        import RNA

        result, run = self._run("batch-energy-1", parameters={"engine": "rnaduplex"}, workers=2)
        self.assertTrue(result["ok"], result)
        self.assertEqual((result["summary"]["updated"], result["summary"]["unchanged"]), (2, 2))
        self.assertEqual(result["summary"]["engine_name"], "ViennaRNA RNAduplex")
        dg = lambda a, b: "%.2f" % RNA.duplexfold(a, b).energy  # noqa: E731
        out = (run / "energies.hyb").read_bytes().decode()
        want = list(self.lines)
        want[1] = want[1].replace("\t-10.5\t", "\t%s\t" % dg(self.seq_a[9:30], self.seq_a[199:222]), 1)
        want[5] = want[5].replace("\t.\t", "\t%s\t" % dg(self.seq_a[49:70], self.seq_b[99:125].replace("T", "U")), 1)
        self.assertEqual(out, "".join(want))
        meta = json.loads((run / "parameters.json").read_text())
        self.assertEqual((meta["engineVersion"], meta["executable"], meta["cplcofold"]), ("ViennaRNA " + RNA.__version__, None, None))
        self.assertTrue((run / "hyb2-energies-rnaduplex.zip").is_file())

        # arms with no pair: kept, and the audit says why
        from desktop.runtime import batch_energy
        energy = batch_energy._pipe_energy(ROOT)
        with mock.patch.object(energy, "duplex_folder", lambda workers=None: lambda pairs: [None] * len(pairs)):
            result, run = self._run("batch-energy-2", parameters={"engine": "rnaduplex"})
        self.assertEqual((result["summary"]["updated"], result["summary"]["unchanged"]), (0, 4))
        self.assertEqual(self._audit(run)[0][5], "RNAduplex found no pair between the arms")

        # without ViennaRNA the run fails and says so
        def missing(workers=None):
            raise energy.EnergyUnavailable("the dG column needs the ViennaRNA Python package or RNAduplex on PATH")

        with mock.patch.object(energy, "duplex_folder", missing):
            result, run = self._run("batch-energy-3", parameters={"engine": "rnaduplex"})
        self.assertFalse(result["ok"])
        self.assertIn("RNAduplex", result["error"])

    def test_rejects_bad_requests(self):
        base = {"operation": "batch_energy_run", "project_path": str(self.project), "sample_id": self.sample}
        for bad in ({"engine": "linearfold"}, {"records": "some"}, {"other": 1}, {"records": "filtered", "filters": []}):
            with self.assertRaises(ValueError):
                handle({**base, "parameters": bad}, repo_root=ROOT)
        with self.assertRaises(ValueError):
            handle({**base, "sample_id": "nope"}, repo_root=ROOT)
        # no analysis index yet: the filtered mode says so in result.json
        result, run = self._run("batch-energy-1", parameters={"records": "filtered", "filters": {}})
        self.assertFalse(result["ok"])
        self.assertIn("no analysis index", json.loads((run / "result.json").read_text())["error"])

    def test_non_finite_energy_keeps_the_row(self):
        from unittest import mock

        from desktop.runtime import batch_energy

        real = batch_energy.fold_pairs

        def fold(root, engine, pairs):
            values = real(root, engine, pairs)
            return [float("nan")] + values[1:]

        with mock.patch.object(batch_energy, "fold_pairs", fold):
            result, run = self._run("batch-energy-1", parameters={"engine": "rnacofold"})
        self.assertTrue(result["ok"], result)
        self.assertEqual((result["summary"]["updated"], result["summary"]["unchanged"]), (1, 3))
        audit = self._audit(run)
        self.assertEqual((audit[0][2], audit[0][4], audit[0][5]), ("unchanged", "-10.5", "invalid energy returned"))
        self.assertEqual(audit[3][2], "updated")
        out = (run / "energies.hyb").read_bytes().decode().splitlines(keepends=True)
        self.assertEqual(out[1], self.lines[1])  # its energy is kept

    def test_failing_chunk_cancels_the_others(self):
        from concurrent.futures import Future
        from unittest import mock

        from desktop.runtime import batch_energy

        pools = []

        class Pool:
            def __init__(self, *args, **kwargs):
                self.futures, self.shutdowns = [], []
                pools.append(self)

            def submit(self, fn, root, engine, start, pairs):
                future = Future()
                if not self.futures:
                    future.set_exception(RuntimeError("fold crashed"))
                self.futures.append(future)  # the others never finish
                return future

            def shutdown(self, wait=True, cancel_futures=False):
                self.shutdowns.append((wait, cancel_futures))

        with mock.patch.object(batch_energy, "ProcessPoolExecutor", Pool), \
                mock.patch.dict(batch_energy.ENGINES["rnacofold"], {"chunk": 1}):
            result, run = self._run("batch-energy-1", parameters={"engine": "rnacofold"}, workers=2)
        self.assertFalse(result["ok"])
        self.assertIn("fold crashed", json.loads((run / "result.json").read_text())["error"])
        self.assertEqual(pools[0].shutdowns, [(False, True)])

    def _mkrun(self, task_id):
        run = self.project / "runs" / task_id
        run.mkdir(parents=True)
        return run


class RewriteTests(unittest.TestCase):
    def test_only_column_three_changes(self):
        from desktop.runtime.batch_energy import rewrite_energy

        self.assertEqual(rewrite_energy(b"a\tb\t-1\td\te\r\n", -12.345), b"a\tb\t-12.35\td\te\r\n")
        self.assertEqual(rewrite_energy(b"a\tb\t.\td", 0.0), b"a\tb\t0.00\td")


if __name__ == "__main__":
    unittest.main()
