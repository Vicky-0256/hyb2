"""Structure runs: code review fixes (pool failure, nested windows, repeated
windows, COMRADES evidence and progress, region 2 before region 1)."""
import json
import os
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock

from desktop.runtime.bridge import handle
from desktop.runtime.tests import test_structure as base
from desktop.runtime.tests.test_structure import GENE, ROOT, _register


def _fixture(case):
    """A TestCase base with ``case``'s fixture (setUp, tearDown and helpers)
    but none of its tests, which test_structure runs."""
    names = {key: value for key, value in vars(case).items() if not key.startswith("test")}
    return type("_" + case.__name__, (unittest.TestCase,), names)


StructureFixture = _fixture(base.StructureRunTests)
ComradesFixture = _fixture(base.ComradesRunTests)
CofoldFixture = _fixture(base.CofoldRunTests)


def _slow_or_failing(index, marker_dir):
    """A pool job: job 0 fails soon, every other one takes long."""
    if index == 0:
        time.sleep(0.5)
        raise RuntimeError("window 1 failed: out of memory")
    time.sleep(30)
    Path(marker_dir, f"done-{index}").write_text("finished")
    return index


class PoolFailureTests(unittest.TestCase):
    def test_first_failure_stops_the_pool(self):
        from desktop.runtime.structure import run_in_pool
        with tempfile.TemporaryDirectory() as tmp:
            started = time.monotonic()
            seen = []
            with self.assertRaisesRegex(RuntimeError, "window 1 failed: out of memory"):
                run_in_pool(2, _slow_or_failing, [(index, tmp) for index in range(6)], seen.append)
            # neither the running job nor the queued ones are waited for
            self.assertLess(time.monotonic() - started, 20)
            self.assertEqual(seen, [])
            time.sleep(0.5)
            self.assertEqual(list(Path(tmp).iterdir()), [])

    def test_results_arrive_as_they_finish(self):
        from desktop.runtime.structure import run_in_pool
        seen = []
        run_in_pool(2, pow, [(2, k) for k in range(5)], seen.append)
        self.assertEqual(sorted(seen), [1, 2, 4, 8, 16])


class PairBonusTests(unittest.TestCase):
    def test_native_operations_never_ignore_an_explicit_removed_kernel(self):
        from desktop.runtime.project import new_project

        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp) / "toy.hyb2proj"
            new_project(project, "Synthetic")
            for op in ("structure_run", "cplfold_predict", "cplfold_run"):
                base_request = {"project_path": str(project), "operation": op, "sequence": "GGGAAACCC" * 3}
                if op == "structure_run":
                    base_request["source"] = "paste"
                for key in ("support_kernel", "supportKernel"):
                    for field in ({key: "rnacofold"}, {"parameters": {key: "rnacofold", "evidence": "none"}}):
                        with self.subTest(op=op, field=field), self.assertRaisesRegex(ValueError, "kernel"):
                            handle({**base_request, **field}, repo_root=ROOT)
            self.assertFalse(any((project / "runs").iterdir()))

    def test_arm_counts_score_candidates_and_build_the_exported_bonus(self):
        from desktop.runtime import structure
        from desktop.runtime.project import new_project

        sequence = "AAGGGGAAAACCCCAAAAGGGGAA"
        # Native cofold sets the cached module's host sequence ceiling. Keep
        # the six native requests together, but do not change later web tests.
        with mock.patch.dict(structure._MODULES, clear=True), tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp) / "toy.hyb2proj"
            new_project(project, "Synthetic hairpin")
            (project / "toy.fa").write_text(">toy\n" + sequence + "\n")
            # The longer arm can form its own hairpin. Three reads with the
            # short G arm therefore contribute only in the RNAduplex mode.
            blocks = [(3, 14, 19, 20)] * 3 + [(11, 14, 19, 22)] * 2
            (project / "toy.hyb").write_text("".join(
                f"r{k}\t{sequence[a-1:b]}{sequence[c-1:d]}\t.\ttoy\t1\t{b-a+1}\t{a}\t{b}\t.\ttoy\t{b-a+2}\t{b-a+d-c+2}\t{c}\t{d}\t.\n"
                for k, (a, b, c, d) in enumerate(blocks)))
            for kernel, estimator in (("duplex", "RNAduplex"), ("comrades", "RNAcofold")):
                counts = {(11, 22): 2, (12, 21): 2, (13, 20): 2, (14, 19): 2}
                if kernel == "duplex":
                    counts.update({(11, 20): 3, (12, 19): 3})
                for mode, shift, extra in (
                    ("genome", 0, {}), ("windows", 2, {"windows": [{"start": 3, "end": 22}]}),
                    ("cofold", 2, {"engine": "cplcofold", "regions": [{"gene_id": "toy", "start": 3, "end": 14},
                                                                    {"gene_id": "toy", "start": 15, "end": 24}]})
                ):
                    with self.subTest(kernel=kernel, mode=mode):
                        task = f"{kernel}-{mode}"
                        spy = _Spy()
                        with spy.patch:
                            result = handle({"operation": "structure_run", "project_path": str(project), "task_id": task, "mode": mode,
                                             "reference": "toy.fa", "hyb_paths": ["toy.hyb"], "gene_id": "toy", "workers": 1,
                                             "parameters": {"support_kernel": kernel, "allow_pseudoknot": False}, **extra}, repo_root=ROOT)
                        self.assertTrue(result["ok"], result)
                        self.assertTrue(any(estimator + " read support" in s[1] for s in spy.seen), spy.seen)
                        _register(project, task, result)
                        loaded = structure.read_structure(project, task)
                        evidence = loaded["meta"]["records"][0]["evidence"]
                        self.assertIn(estimator, evidence["pairCounter"])
                        record = loaded["records"][0]
                        rows = Path(record["bonus_matrix_path"]).read_text().splitlines()
                        self.assertIn(f"# support_kernel={kernel}", rows)
                        matrix = {(int(i) + shift, int(j) + shift): float(b) for i, j, b in
                                  (line.split("\t") for line in rows if line[:1].isdigit())}
                        self.assertEqual(matrix.keys(), counts.keys())
                        for pair, count in counts.items():
                            self.assertAlmostEqual(matrix[pair], 3.6 * count / (count + evidence["saturatingQ"]), places=6)
                        self.assertTrue(record["candidates"])
                        for cand in record["candidates"]:
                            local = structure._pairs(cand["structure"])
                            expected = [counts.get((i + shift, j + shift), 0) for i, j, _ in local]
                            self.assertEqual(cand["support_kernel"], kernel)
                            self.assertEqual((cand["comradesScore"], cand["comradesPairCounts"]), (2 * sum(expected), expected))
                            for (i, j, _), b in zip(local, cand["bonus"] or []):
                                self.assertAlmostEqual(b, matrix.get((i + shift, j + shift), 0), places=6)

    def test_nested_windows_keep_their_own_bonus(self):
        from desktop.runtime.structure import _bonus_of, _pair_bonus
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "pairs.tsv"
            path.write_text("window_start\twindow_end\ti\tj\tbracket\tbonus\n"
                            "1\t500\t10\t40\t()\t0.25\n1\t1000\t10\t40\t()\t0.75\n1\t1000\t12\t38\t()\t\n")
            bonus = _pair_bonus(path)
            self.assertEqual((_bonus_of(bonus, 1, 500, 10, 40), _bonus_of(bonus, 1, 1000, 10, 40)), (0.25, 0.75))
            self.assertIsNone(_bonus_of(bonus, 1, 1000, 12, 38))  # no HYB evidence
            # a pairs.tsv without window_end is still read
            path.write_text("window_start\ti\tj\tbracket\tbonus\n1\t10\t40\t()\t0.5\n")
            self.assertEqual(_bonus_of(_pair_bonus(path), 1, 500, 10, 40), 0.5)


class NestedWindowRunTests(StructureFixture):
    def test_nested_windows_read_back_their_bonus(self):
        from desktop.runtime.structure import read_structure
        windows = [{"start": 1, "end": 160}, {"start": 1, "end": 320}]
        result = handle(self._request(task_id="nested", mode="windows", windows=windows, workers=1,
                                      parameters={"beam": 20}), repo_root=ROOT)
        self.assertTrue(result["ok"], result)
        rows = {}
        for line in (self.project / "structures" / "nested" / "pairs.tsv").read_text().splitlines()[1:]:
            start, end, i, j, _, bonus = line.split("\t")
            rows[(int(start), int(end), int(i), int(j))] = float(bonus)
        _register(self.project, "nested", result)
        records = read_structure(self.project, "nested")["records"]
        self.assertEqual([(r["window_start"], r["window_end"]) for r in records], [(1, 160), (1, 320)])
        for record in records:
            want = [rows[(1, record["window_end"], i, j)] for i, j in record["pairs"]]
            self.assertEqual(record["bonus"], want)

    def test_repeated_window_is_refused(self):
        from desktop.runtime.structure import _parameters, _windows
        with self.assertRaisesRegex(ValueError, r"window 3 \(1-160\) repeats window 1; give each window once"):
            _windows([{"start": 1, "end": 160}, {"start": 100, "end": 300}, {"start": 1, "end": 160}], 320)
        with self.assertRaisesRegex(ValueError, "repeats window 1"):
            handle(self._request(task_id="twice", mode="windows",
                                 windows=[{"start": 1, "end": 160}, {"start": 1, "end": 160}]), repo_root=ROOT)
        # as the module docstring says: windows are pseudoknot-free unless asked
        self.assertFalse(_parameters({}, "windows")["allow_pseudoknot"])

    def test_bonus_matrix_temporary_name_is_unique(self):
        import numpy as np
        from desktop.runtime.structure import write_bonus_matrix
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "bonus_matrix.tsv"
            # the old fixed temporary name, taken by another writer
            (Path(tmp) / ".bonus_matrix.tsv.tmp").mkdir()
            bonus = np.zeros((12, 12))
            bonus[0, 5] = bonus[5, 0] = 0.5
            self.assertEqual(write_bonus_matrix(path, bonus, "G:1-12"), 1)
            self.assertTrue(path.read_text().endswith("1\t6\t0.50000000\n"))
            self.assertEqual(sorted(p.name for p in Path(tmp).iterdir()), [".bonus_matrix.tsv.tmp", "bonus_matrix.tsv"])
            # a failed write leaves the old file and no temporary
            with self.assertRaises(AttributeError):
                write_bonus_matrix(path, None, "G:1-12")
            self.assertTrue(path.read_text().endswith("1\t6\t0.50000000\n"))
            self.assertEqual(len(list(Path(tmp).iterdir())), 2)


class _Spy:
    """Records every progress.json write of a run."""

    def __init__(self):
        from desktop.runtime import structure
        self.seen = []
        self.original = structure.ProgressWriter.__call__
        spy = self

        def call(writer, stage, detail, done=None, total=None, *, units="bytes", force=False):
            spy.seen.append((stage, detail, done, total, units))
            return spy.original(writer, stage, detail, done, total, units=units, force=force)

        self.patch = mock.patch.object(structure.ProgressWriter, "__call__", call)


class ComradesEvidenceTests(ComradesFixture):
    def test_only_scalar_score_caps_the_matched_endpoint_counts(self):
        from desktop.runtime.comrades import score_structure_pairs

        counts = {(1, 12): 1_100_000, (2, 11): 1_000_000, (3, 10): 7}
        scored = score_structure_pairs([(12, 1), (2, 11), (3, 10), (4, 9)], counts)
        self.assertEqual(scored, {"pair_support": [1_100_000, 1_000_000, 7, 0], "matched_pairs": 3,
                                  "matched_support": 2_100_007, "score": 4_000_014})

    def test_score_uses_observations_outside_the_constraint_shortlist(self):
        from desktop.runtime import comrades

        sequence = "G" * 8 + "C" * 1016

        def observation(i, j):
            return {"one": sequence[i-1:i+3], "two": sequence[j-1:j+3], "oneStart": i, "twoStart": j}

        # 1,001 higher-count pairs share an endpoint; only (1, 20) is fitted.
        arms = [observation(1, j) for j in range(20, 1021) for _ in range(3)]
        arms += [observation(2, 19), observation(19, 2)]  # two raw observations outside the shortlist

        def candidate(pairs, energy):
            dot = ["."] * len(sequence)
            for i, j in pairs:
                dot[i-1], dot[j-1] = "(", ")"
            return {"structure": "".join(dot), "pairs": [(i, j, "()", None) for i, j in pairs], "energy": energy}

        # Both candidates obey the fitted constraint. Without the tail counts,
        # the lower-energy ranked candidate would win. (3, 18) has no observations.
        folder = mock.Mock(side_effect=[candidate([(1, 20), (3, 18)], -2),
                                        candidate([(1, 20), (2, 19), (3, 18)], -1)])
        with mock.patch.object(comrades, "cofold", return_value=[(1, 5)]), mock.patch.object(comrades, "_folder", return_value=folder):
            record = comrades.fold(sequence, arms, comrades.parameters({"constraint_limit": 1, "random_folds": 1}))
        self.assertEqual([call.args[2] for call in folder.call_args_list], [[(1, 20)], [(1, 20)]])
        ev, ensemble = record["evidence"], record["randomisation"]
        shortlisted = [(e["one"], e["two"]) for e in ev["selectedEvidencePairs"]]
        self.assertEqual(shortlisted, [(1, j) for j in range(20, 1021)])
        self.assertEqual(ev["scoredBasePairs"], 1002)
        self.assertEqual(ev["scoringVersion"], "all-pairs-capped-v1")
        scored = ev["scoredEvidencePairs"]
        self.assertEqual(len(scored), 1002)
        counts = {(p["one"], p["two"]): p["count"] for p in scored}
        self.assertEqual(counts, {**dict.fromkeys(shortlisted, 3), (2, 19): 2})
        for entry in ensemble["structures"]:
            raw = [counts.get(pair, 0) for pair in comrades._dot_pairs(entry["dotBracket"])]
            self.assertEqual(entry["matchedEvidencePairs"], sum(n > 0 for n in raw))
            self.assertEqual(entry["comradesScore"], sum(2 * min(n, 1_000_000) for n in raw))
        self.assertEqual([(c["run"], c["comradesScore"]) for c in ensemble["structures"]], [(1, 10), (0, 6)])
        self.assertEqual((ensemble["selectedRun"], ev["matchedEvidencePairs"], ev["comradesScore"]), (1, 2, 10))
        self.assertEqual(record["pairs"], [(1, 20, "()", 3), (2, 19, "()", 2), (3, 18, "()", 0)])

    def test_windows_read_the_hyb_once_with_progress(self):
        from desktop.runtime import comrades
        windows = [(1, 130), (100, 260), (1, 260)]
        spy = _Spy()
        with spy.patch, mock.patch.object(comrades, "_records", wraps=comrades._records) as records:
            result = self._run(task_id="cw", mode="windows", windows=[{"start": s, "end": e} for s, e in windows])
        self.assertTrue(result["ok"], result)
        self.assertEqual(records.call_count, 1)
        # the same arms as reading each window alone
        meta = json.loads((self.project / "structures" / "cw" / "meta.json").read_text())
        for (start, end), summary in zip(windows, meta["records"]):
            alone, _ = comrades.evidence_arms([self.project / "data" / "a.hyb"], GENE, self.sequence, start, end)
            self.assertEqual(summary["comrades"]["evidence"]["inputRecords"], len(alone))
        # the evidence is reported before folding, then real progress inside each window
        stages = [s[0] for s in spy.seen]
        self.assertEqual(stages[0], "evidence")
        self.assertLess(max(k for k, s in enumerate(stages) if s == "evidence"), stages.index("fold"))
        inside = [s for s in spy.seen if s[0] == "fold" and s[2] is not None and s[2] != int(s[2])]
        self.assertTrue(inside, spy.seen)
        self.assertTrue(all(0 < s[2] < 3 and s[3] == 3 for s in inside))
        self.assertEqual([s[2] for s in inside], sorted(s[2] for s in inside))
        self.assertTrue(any("RNAcofold evidence" in s[1] for s in inside))
        self.assertTrue(any(s[0] == "fold" and "constrained folds" in s[1] for s in spy.seen))


class ReversedRegionTests(CofoldFixture):
    """Region 2 before region 1 on the RNA: A stays region 1."""

    def _check(self, task_id, result):
        from desktop.runtime.structure import read_structure
        self.assertTrue(result["ok"], result)
        out = self.project / "structures" / task_id
        meta = json.loads((out / "meta.json").read_text())
        self.assertEqual([(r["start"], r["end"]) for r in meta["regions"]], [(170, 230), (1, 60)])
        self.assertEqual(meta["records"][0]["cutpoint"], 61)
        dbn = (out / "structure.dbn").read_text().splitlines()
        self.assertEqual(dbn[1], self.one[169:230] + "&" + self.one[:60])
        sequence = dbn[1].replace("&", "")
        where = list(range(170, 231)) + list(range(1, 61))
        # pairs.tsv: A+B i < j, each end's RNA position and base
        rows = [line.split("\t") for line in (out / "pairs.tsv").read_text().splitlines()[1:]]
        self.assertTrue(rows)
        for row in rows:
            i, j, pi, pj = int(row[2]), int(row[3]), int(row[7]), int(row[9])
            self.assertLess(i, j)
            self.assertEqual((pi, pj), (where[i - 1], where[j - 1]))
            self.assertEqual((self.one[pi - 1], self.one[pj - 1]), (sequence[i - 1], sequence[j - 1]))
            self.assertEqual(row[10], str(int(i <= 61 < j)))
        # the .ct: symmetric partners, column 6 the RNA position of its base
        ct = [line.split("\t") for line in (out / "structure.ct").read_text().splitlines()[1:]]
        partner = {int(c[0]): int(c[4]) for c in ct}
        for c in ct:
            k, base, position = int(c[0]), c[1], int(c[5])
            self.assertEqual((position, self.one[position - 1]), (where[k - 1], base))
            if partner[k]:
                self.assertEqual(partner[partner[k]], k)
        self.assertEqual({(min(i, j), max(i, j)) for i, j in partner.items() if j},
                         {(int(r[2]), int(r[3])) for r in rows})
        # read back: drawn on the RNA, i < j, the bonus of each pair from pairs.tsv
        _register(self.project, task_id, result)
        record = read_structure(self.project, task_id)["records"][0]
        self.assertEqual((record["positions"][0], record["positions"][61], record["cutpoint"]), (170, 1, 61))
        by_rna = {(min(int(r[7]), int(r[9])), max(int(r[7]), int(r[9]))): (float(r[5]) if r[5] else None) for r in rows}
        for (i, j), bonus in zip(record["pairs"], record["bonus"] or [None] * len(record["pairs"])):
            self.assertLess(i, j)
            self.assertIn((i, j), by_rna)
            if record["bonus"]:
                self.assertEqual(bonus, by_rna[(i, j)])
        # arms.json in A+B positions; a joining row has its region 1 (A) arm first
        arms = record["arms"]
        self.assertEqual(arms["rows"], sum(b[4] for b in arms["shown"]))
        for a0, a1, b0, b1, _ in arms["shown"]:
            self.assertTrue(1 <= a0 <= a1 <= 121 and 1 <= b0 <= b1 <= 121)
            if (a0 <= 61) != (b0 <= 61):
                self.assertLessEqual(a1, 61)
        return meta, record

    def test_cofold(self):
        result = self._run([(GENE, 170, 230), (GENE, 1, 60)], task_id="rev1")
        meta, record = self._check("rev1", result)
        rows = meta["records"][0]["evidence_rows"]
        self.assertEqual((rows["between"], rows["within_one"], rows["within_two"]), (9, 0, 2))
        self.assertEqual(record["arms"]["rows"], 11)

    def test_paired(self):
        result = self._run([(GENE, 170, 230), (GENE, 1, 60)], task_id="rev2", engine="comrades", mode="paired")
        meta, record = self._check("rev2", result)
        self.assertEqual(record["arms"]["rows"], 9)
        hard = [tuple(p) for p in meta["parameters"]["hard_pairs"]]
        self.assertTrue(hard and all(i < j for i, j in hard))
        self.assertTrue(set(hard) <= {tuple(p) for p in record["pairs"]})


class PairedEvidenceTests(CofoldFixture):
    def test_paired_progress(self):
        spy = _Spy()
        with spy.patch:
            result = self._run([(GENE, 1, 60), (GENE, 170, 230)], task_id="pp", engine="comrades", mode="paired")
        self.assertTrue(result["ok"], result)
        stages = [s[0] for s in spy.seen]
        self.assertEqual(stages[0], "evidence")
        self.assertTrue(any(s[0] == "fold" and s[2] is not None and 0 < s[2] < 1 for s in spy.seen), spy.seen)


if __name__ == "__main__":
    unittest.main()
