"""The shared IRIS predictor, its source CLI and the desktop project adapter."""
import gzip
import json
import os
import random
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

# desktop.runtime first: it puts the cplfold and hyb2analysis import roots on the path
from desktop.runtime.bridge import _dumps_line, handle
from desktop.runtime.project import new_project
from hyb2analysis import iris
from hyb2analysis.engine_parameters import engine_parameters
from hyb2analysis.iris_parameters import IRIS_DEFAULTS, IRIS_K_RANGE_GRAMMAR, checked_iris_defaults

ROOT = Path(__file__).resolve().parents[3]
NODE = shutil.which("node")
REFERENCE_JS = Path(__file__).resolve().parent / "web_iris_reference.js"
GENE = "IRIS_virusRNA"
COMPLEMENT = {"A": "U", "U": "A", "G": "C", "C": "G"}


def fixture(seed=7, length=140):
    """A sequence with three designed helices and chimeras around them
    (plus a few between distant helices), arms 1-based inclusive."""
    rnd = random.Random(seed)
    seq = [rnd.choice("ACGU") for _ in range(length)]
    helices = ((5, 60, 8), (70, 130, 9), (20, 45, 6))
    for i, j, k in helices:
        for t in range(k):
            seq[j - t] = COMPLEMENT[seq[i + t]]
    arms = []
    for (i, j, k), n in zip(helices + ((10, 120, 6),), (30, 20, 10, 5)):
        for _ in range(n):
            a, b = max(0, i + rnd.randint(-3, 2)), max(0, j - k + 1 + rnd.randint(-2, 3))
            la, lb = rnd.randint(10, 18), rnd.randint(10, 18)
            arms.append({"oneStart": a + 1, "oneEnd": min(length, a + la), "twoStart": b + 1, "twoEnd": min(length, b + lb)})
    return "".join(seq), arms


def _key(pairs):
    return ",".join(f"{l}-{r}" for l, r in pairs)


class ParameterTests(unittest.TestCase):
    def test_defaults_and_messages(self):
        self.assertEqual(iris.parameters(None), {"K": 2, "C": 20, "fraction": 0.75, "kRange": [4, 5, 6, 7]})
        self.assertEqual(iris.parameters({"K": "3", "C": 3, "fraction": 0, "kRange": "7, 5,5  4"})["kRange"], [4, 5, 7])
        for value in (None, ""):
            self.assertEqual(iris.parameters({"kRange": value})["kRange"], [4, 5, 6, 7])
        for zero in (0, "0"):
            self.assertEqual(iris.parameters({"fraction": zero})["fraction"], 0)
        for blank in ("", " \t"):
            with self.assertRaisesRegex(ValueError, "support quantile"):
                iris.parameters({"fraction": blank})
        for raw, message in (({"K": 4}, "IRIS ensemble size K must be an integer from 1 to 3."),
                             ({"K": 1.5}, "IRIS ensemble size K must be an integer from 1 to 3."),
                             ({"K": 3, "C": 2}, "IRIS candidate count C must be an integer from 3 to 50."),
                             ({"C": 51}, "IRIS candidate count C must be an integer from 2 to 50."),
                             ({"fraction": 1}, "IRIS support quantile must be at least 0 and below 1."),
                             ({"fraction": -0.1}, "IRIS support quantile must be at least 0 and below 1."),
                             ({"fraction": "x"}, "IRIS support quantile must be at least 0 and below 1."),
                             ({"kRange": "4 13"}, "IRIS stem length must be an integer from 2 to 12."),
                             ({"kRange": "4 x"}, "IRIS stem length must be an integer from 2 to 12."),
                             ({"kRange": "4 ,"}, "IRIS stem length must be an integer from 2 to 12.")):
            with self.assertRaises(ValueError, msg=raw) as caught:
                iris.parameters(raw)
            self.assertEqual(str(caught.exception), message)
        with self.assertRaisesRegex(ValueError, "unknown IRIS parameter: beam"):
            iris.parameters({"beam": 20})

    def test_k_range_contract(self):
        cases = json.loads((ROOT / "desktop/app/views/testdata/iris_k_range.json").read_text(encoding="utf-8"))
        for text, want in cases["validText"].items():
            with self.subTest(text=text):
                self.assertEqual(iris.parameters({"kRange": text})["kRange"], want)
                self.assertEqual(checked_iris_defaults(dict(IRIS_DEFAULTS, kRange=text))["kRange"], text)
        self.assertEqual(iris.parameters({"kRange": cases["validArray"]})["kRange"], cases["normalizedArray"])
        for value in cases["invalidText"] + cases["invalidValues"]:
            with self.subTest(task=value), self.assertRaisesRegex(ValueError, "IRIS stem length"):
                iris.parameters({"kRange": value})
        for value in ["", cases["validArray"], *cases["invalidText"]]:
            with self.subTest(published=value), self.assertRaises(ValueError):
                checked_iris_defaults(dict(IRIS_DEFAULTS, kRange=value))

    def test_scalar_padding_follows_the_catalog(self):
        # Remove only the outer padding in the source: neither host may trim independently.
        with patch.dict(IRIS_K_RANGE_GRAMMAR, scalar=f"^{IRIS_K_RANGE_GRAMMAR['number']}$"):
            self.assertEqual(iris.parameters({"K": "2"})["K"], 2)
            with self.assertRaisesRegex(ValueError, "IRIS ensemble size K"):
                iris.parameters({"K": " 2 "})
            if NODE:
                script = """globalThis.Hyb2EngineParameters = {iris: JSON.parse(process.argv[1])};
const p = require(process.argv[2]).parameters, assert = require('node:assert/strict');
assert.equal(p({K: '2'}).K, 2);
assert.throws(() => p({K: ' 2 '}), /IRIS ensemble size K/);"""
                subprocess.run([NODE, "-e", script, json.dumps(engine_parameters("web")["iris"]),
                                str(ROOT / "web/iris-core.js")], check=True)

    def test_core_defaults_reach_both_catalogs_and_omitted_parameters(self):
        changed = {"K": 1, "C": 9, "fraction": 0.5, "kRange": "3 5"}
        with patch.dict(IRIS_DEFAULTS, changed):
            for host in ("web", "desktop"):
                self.assertEqual(engine_parameters(host)["iris"]["defaults"], changed)
            self.assertEqual(iris.parameters({}), dict(changed, kRange=[3, 5]))

    def test_both_catalogs_reject_invalid_core_defaults(self):
        invalid = [dict(IRIS_DEFAULTS, **edit) for edit in (
            {"stemLength": 4}, {"K": True}, {"K": "2"}, {"K": 1.5}, {"K": 0}, {"K": 4},
            {"C": False}, {"C": "20"}, {"C": 3.5}, {"C": 51}, {"K": 3, "C": 2},
            {"fraction": False}, {"fraction": "0"}, {"fraction": -0.1}, {"fraction": 1}, {"fraction": float("nan")},
            {"kRange": [2, 12]}, {"kRange": None}, {"kRange": " "}, {"kRange": "2,"}, {"kRange": "2 3.5"},
            {"kRange": "1 2"}, {"kRange": "13"}, {"kRange": "1_2"},
        )]
        invalid.extend({k: v for k, v in IRIS_DEFAULTS.items() if k != missing} for missing in IRIS_DEFAULTS)
        for defaults in invalid:
            with patch.dict(IRIS_DEFAULTS, defaults, clear=True):
                for host in ("web", "desktop"):
                    with self.subTest(host=host, defaults=defaults), self.assertRaises(ValueError):
                        engine_parameters(host)

    def test_export_validates_defaults_before_writing_either_asset(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            # Run the actual CLI, without site-packages or the folding runtime.
            inputs = ["scripts/build/export-support-kernels.py", "scripts/build/fold-text.js", "scripts/build/cplfold-settings.js",
                      "bin/cplfold"]
            inputs += [f"pipeline/hyb2analysis/{name}.py" for name in
                       ("__init__", "reference", "support_kernels", "engine_parameters", "fold_text", "iris_parameters")]
            inputs += [f"third_party/cplfold/cplfold/{name}.py" for name in
                       ("__init__", "models", "sequence", "_internal/__init__", "_internal/model_tables", "_internal/cli_input",
                        "_internal/errors", "_internal/defaults")]
            for name in inputs:
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / name, target)
            desktop = root / "desktop/app/views/support-kernels.json"
            web = root / "web/support-kernels.js"
            for asset in (desktop, web):
                asset.parent.mkdir(parents=True)
                asset.write_bytes(b"previous catalog")

            config = root / "pipeline/hyb2analysis/iris_parameters.py"
            source = config.read_text(encoding="utf-8")

            def export(edit):
                config.write_text(source + f"\nIRIS_DEFAULTS.update({edit!r})\n", encoding="utf-8")
                return subprocess.run([sys.executable, "-IBS", "scripts/build/export-support-kernels.py"],
                                      cwd=root, capture_output=True, text=True)

            for edit in ({"K": 3, "C": 2}, {"fraction": 1}, {"kRange": "13"}, {"kRange": ""}, {"kRange": "4,,6"}):
                with self.subTest(edit=edit):
                    result = export(edit)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("iris", result.stderr.lower())
                    self.assertEqual([desktop.read_bytes(), web.read_bytes()], [b"previous catalog"] * 2)

            changed = {"K": 3, "C": 3, "fraction": 0, "kRange": "12, 2 12"}
            result = export(changed)
            self.assertEqual(result.returncode, 0, result.stderr)
            desktop_engines = json.loads(desktop.read_text(encoding="utf-8"))["engines"]
            web_json = web.read_text(encoding="utf-8").split("globalThis.Hyb2EngineParameters = ", 1)[1]
            web_engines, _ = json.JSONDecoder().raw_decode(web_json)
            for engines in (desktop_engines, web_engines):
                self.assertEqual(engines["iris"]["defaults"], changed)
                self.assertEqual(engines["iris"]["parameterKeys"], ["K", "C", "fraction", "kRange"])
            expected = dict(changed, kRange=[2, 12])
            with patch.dict(IRIS_DEFAULTS, desktop_engines["iris"]["defaults"]):
                self.assertEqual(iris.parameters({}), expected)
            if NODE:
                script = "require(process.argv[1]); console.log(JSON.stringify(require(process.argv[2]).parameters({})));"
                browser = subprocess.run([NODE, "-e", script, str(web), str(ROOT / "web/iris-core.js")],
                                         check=True, capture_output=True, text=True)
                self.assertEqual(json.loads(browser.stdout), expected)

    def test_evidence_errors(self):
        sequence, arms = fixture()
        with self.assertRaisesRegex(ValueError, "IRIS requires HYB evidence within the selected reference region."):
            iris.support_matrix(len(sequence), [])
        with self.assertRaisesRegex(ValueError, "IRIS evidence coordinates are outside the prepared sequence."):
            iris.support_matrix(len(sequence), [{"oneStart": 1, "oneEnd": 10, "twoStart": 130, "twoEnd": 141}])
        with self.assertRaisesRegex(ValueError, "unsupported symbols: N"):
            iris.run("ACGN", arms, {})
        with self.assertRaisesRegex(ValueError, "IRIS requires 1–1,000 canonical RNA nucleotides."):
            iris.run("A" * 1001, arms, {})
        with self.assertRaisesRegex(ValueError, "IRIS found no supported canonical stems"):
            iris.run("A" * 140, arms, {})
        with self.assertRaisesRegex(ValueError, "IRIS desktop limit: more than 3 constrained folds"):
            iris.collect_constraints(iris.collect_stems(sequence, iris.support_matrix(len(sequence), arms),
                                                        iris.parameters({})), max_folds=3)

    def test_folder_reuse_matches_fresh_folds(self):
        # one fold compound with its hard constraints reset each time
        from desktop.runtime import vienna
        sequence, arms = fixture()
        stems = iris.collect_stems(sequence, iris.support_matrix(len(sequence), arms), iris.parameters({}))
        folder = iris.Folder(sequence)
        for pairs in iris.collect_constraints(stems)[::17]:
            reused = folder(sequence, pairs)
            fresh = vienna.fold(sequence, {"temperature": 37.0, "min_loop": 3}, pairs)
            self.assertEqual(reused["dotBracket"], fresh["structure"])
            self.assertAlmostEqual(reused["energy"], fresh["energy"], places=2)

    def test_diagnose(self):
        rows = [("X", 10, 20, "X", 40, 50), ("X", 500, 510, "X", 900, 910), ("X", 1, 5, "Y", 1, 5)]
        found = iris.diagnose(rows, 3, "X", 100, 300, 2000)
        self.assertEqual(found["counts"], {"total": 3, "sameRna": 2, "forward": 2, "contained": 0, "tooWide": 0})
        self.assertEqual(found["message"], "No complete HYB arm pair is inside X:100-300. Both arms of each row must fit "
                                           "inside the same region (IRIS limit: 1,000 nt).")
        # nearest complete row: 10-50, a 1,000-nt window centred on it, clamped at 1
        self.assertEqual(found["suggestion"], {"rna": "X", "start": 1, "end": 1000})
        self.assertEqual(iris.diagnose([], 0, "X", 1, 10, 100)["message"], "No HYB records are loaded for the active dataset selection.")
        self.assertIn("No HYB rows have both arms on Y.", iris.diagnose(rows, 3, "Y", 1, 10, 100)["message"])
        self.assertIn("reverse or invalid", iris.diagnose([("X", 9, 1, "X", 4, 2)], 1, "X", 1, 10, 100)["message"])
        wide = iris.diagnose([("X", 1, 10, "X", 1500, 1510)], 1, "X", 1, 100, 2000)
        self.assertTrue(wide["message"].endswith("Every forward arm pair spans more than 1,000 nt, so none fits the desktop limit."))
        self.assertIsNone(wide["suggestion"])
        inside = iris.diagnose(rows, 3, "X", 1, 60, 2000)
        self.assertEqual((inside["counts"]["contained"], inside["message"]), (1, ""))


@unittest.skipUnless(NODE, "node runs the web's iris-core.js")

def _scan_cluster(candidates, C):
    """The clustering as iris-core.js does it: each step scans every pair
    for the first smallest distance in row-major order."""
    n = len(candidates)
    if n <= C:
        return candidates
    sets = [set(map(tuple, c["pairs"])) for c in candidates]
    d = [[len(sets[i]) + len(sets[j]) - 2 * len(sets[i] & sets[j]) for j in range(n)] for i in range(n)]
    groups = {i: [i] for i in range(n)}
    while len(groups) > C:
        alive = sorted(groups)
        a, b = min(((i, j) for x, i in enumerate(alive) for j in alive[x + 1:]), key=lambda ij: d[ij[0]][ij[1]])
        la, lb = len(groups[a]), len(groups[b])
        for k in alive:
            if k not in (a, b):
                d[a][k] = d[k][a] = (d[a][k] * la + d[b][k] * lb) / (la + lb)
        groups[a] += groups.pop(b)
    return [min((candidates[i] for i in groups[k]), key=lambda c: c["energy"]) for k in sorted(groups)]


class ClusterTests(unittest.TestCase):
    def test_matches_the_full_scan(self):
        # few distinct pairs, so equal distances (ties) are frequent
        rnd = random.Random(3)
        pool = [(i, i + rnd.randint(4, 30)) for i in range(1, 12)]
        for _ in range(150):
            n = rnd.randint(3, 40)
            candidates = [{"pairs": sorted(rnd.sample(pool, rnd.randint(0, 5))), "energy": float(rnd.randint(-5, 0))}
                          for _ in range(n)]
            C = rnd.randint(1, n - 1)
            self.assertEqual([c["pairs"] for c in iris.cluster(candidates, C)],
                             [c["pairs"] for c in _scan_cluster(candidates, C)])

    def test_fold_bound_follows_the_length(self):
        self.assertEqual(iris.max_folds(300), iris.MAX_FOLDS_CAP)
        self.assertEqual(iris.max_folds(1000), 1097)
        self.assertEqual(iris.max_folds(5000), iris.MAX_FOLDS)


class WebCrossCheckTests(unittest.TestCase):
    """The same inputs through web/iris-core.js (node) and iris.py: the same
    support, stems, constraint sets, members, weights and energies. The web
    folds with ViennaRNA WebAssembly; here both use the desktop's folds of
    the same constraint sets (the web's C wrapper and vienna.py set the same
    model: Turner 2004, 37 °C, min loop 3, enforced pairs)."""

    def _web(self, sequence, arms, params):
        stems = iris.collect_stems(sequence, iris.support_matrix(len(sequence), arms), iris.parameters(params))
        folder = iris.Folder(sequence)
        folds = {}
        for pairs in iris.collect_constraints(stems):
            fold = folder(sequence, pairs)
            folds[_key(pairs)] = {"dotBracket": fold["dotBracket"], "energy": fold["energy"]}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "request.json"
            path.write_text(json.dumps({"sequence": sequence, "arms": arms, "parameters": params, "folds": folds}))
            out = subprocess.run([NODE, str(REFERENCE_JS), str(path)], capture_output=True, text=True, timeout=120, check=True)
        web = json.loads(out.stdout)
        self.assertNotIn("error", web, web.get("error"))
        self.assertNotIn("missingFolds", web)
        return web

    def _check(self, sequence, arms, params):
        web = self._web(sequence, arms, params)
        n = len(sequence)
        support = iris.support_matrix(n, arms)
        self.assertLess(max(abs(a - b) for a, b in zip(support.ravel().tolist(), web["support"])), 1e-12)
        p = iris.parameters(params)
        self.assertEqual(p["kRange"], web["parameters"]["kRange"])
        stems = iris.collect_stems(sequence, support, p)
        self.assertEqual(stems, web["stems"])
        self.assertEqual([_key(c) for c in iris.collect_constraints(stems)], web["constraints"])
        stages = []
        result = iris.run(sequence, arms, params, lambda stage, percent: stages.append([stage, percent]))
        self.assertEqual(stages, web["result"]["stages"])
        members = result["candidates"]
        self.assertEqual(len(members), len(web["result"]["members"]))
        for mine, theirs in zip(members, web["result"]["members"]):
            self.assertEqual(mine["dotBracket"], theirs["dotBracket"])
            self.assertEqual(_key(mine["constraints"]), theirs["constraints"])
            self.assertEqual(mine["energy"], theirs["energy"])
            self.assertAlmostEqual(mine["weight"], theirs["weight"], places=12)
            self.assertAlmostEqual(mine["coefficient"], theirs["coefficient"], places=12)
        for key in ("testedEnsembles", "candidateCount", "generatedCount"):
            self.assertEqual(result["ensemble"][key], web["result"]["ensemble"][key], key)
        for key in ("meanEnergy", "meanSupport", "lambda"):
            self.assertAlmostEqual(result["ensemble"][key], web["result"]["ensemble"][key], places=9)
        self.assertEqual(result["selectedCandidate"], 0)
        return result

    def test_default_parameters(self):
        sequence, arms = fixture()
        result = self._check(sequence, arms, {"K": 2, "C": 20, "fraction": 0.75, "kRange": "4 5 6 7"})
        self.assertEqual(len(result["candidates"]), 2)
        self.assertGreater(result["ensemble"]["generatedCount"], 20)  # clustering ran

    def test_three_members_few_candidates(self):
        sequence, arms = fixture(seed=3)
        result = self._check(sequence, arms, {"K": 3, "C": 6, "fraction": 0.6, "kRange": "4,6"})
        self.assertLessEqual(len(result["candidates"]), 3)
        self.assertAlmostEqual(sum(m["weight"] for m in result["candidates"]), 1.0, places=12)

    def test_single_member(self):
        sequence, arms = fixture(seed=11)
        self._check(sequence, arms, {"K": 1, "C": 4, "fraction": 0.9, "kRange": "5"})


class IrisRunTests(unittest.TestCase):
    """engine "iris" through the bridge: one region of a project's RNA."""
    offset = 30

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.project = Path(self.tmp.name) / "p.hyb2proj"
        new_project(self.project, "IRIS")
        (self.project / "data").mkdir()
        self.region, self.arms = fixture()
        rnd = random.Random(5)
        self.sequence = "".join(rnd.choice("ACGU") for _ in range(self.offset)) + self.region + \
            "".join(rnd.choice("ACGU") for _ in range(60))
        (self.project / "data" / "ref.fa").write_text(f">{GENE} test\n{self.sequence}\n")
        rows = []
        for k, a in enumerate(self.arms):
            rows.append(f"{k}_1\tACGU\t.\t{GENE}\t1\t20\t{a['oneStart'] + self.offset}\t{a['oneEnd'] + self.offset}\t1e-5\t"
                        f"{GENE}\t21\t40\t{a['twoStart'] + self.offset}\t{a['twoEnd'] + self.offset}\t1e-5\t\t-10\tType_1\n")
        # outside the region, and on another RNA: not evidence
        rows.append(f"x_1\tACGU\t.\t{GENE}\t1\t20\t5\t20\t1e-5\t{GENE}\t21\t40\t150\t165\t1e-5\t\t-10\tType_1\n")
        rows.append(f"y_1\tACGU\t.\t{GENE}\t1\t20\t40\t50\t1e-5\tOTHER\t21\t40\t1\t10\t1e-5\t\t-10\tType_1\n")
        (self.project / "data" / "a.hyb").write_text("".join(rows))

    def tearDown(self):
        self.tmp.cleanup()

    def _run(self, **extra):
        request = {"operation": "structure_run", "project_path": str(self.project), "reference": "data/ref.fa",
                   "hyb_paths": ["data/a.hyb"], "gene_id": GENE, "engine": "iris", "mode": "windows",
                   "windows": [{"start": self.offset + 1, "end": self.offset + len(self.region)}]}
        request.update(extra)
        return handle(request, repo_root=ROOT)

    def test_run_writes_the_ensemble(self):
        result = self._run(task_id="i1", parameters={"K": 2, "C": 20})
        self.assertTrue(result["ok"], result)
        expected = iris.run(self.region, self.arms, {"K": 2, "C": 20})
        out = self.project / "structures" / "i1"
        meta = json.loads((out / "meta.json").read_text())
        self.assertEqual(meta["engine"], "IRIS")
        self.assertEqual(meta["parameters"]["kRange"], "4 5 6 7")
        record = meta["records"][0]
        self.assertEqual(record["iris"]["evidence"]["inputRecords"], len(self.arms))
        members = record["iris"]["members"]
        self.assertEqual([m["dotBracket"] for m in members], [m["dotBracket"] for m in expected["candidates"]])
        self.assertEqual([m["weight"] for m in members], [m["weight"] for m in expected["candidates"]])
        self.assertEqual(record["iris"]["ensemble"]["testedEnsembles"], expected["ensemble"]["testedEnsembles"])
        dbn = (out / "structure.dbn").read_text().splitlines()
        self.assertTrue(dbn[0].startswith(f">{GENE}:31-170 IRIS ensemble member 1 (heaviest), K 2, C 20"), dbn[0])
        self.assertEqual(dbn[2], members[0]["dotBracket"])
        candidates = json.loads((out / "candidates.json").read_text())["records"][0]["candidates"]
        self.assertEqual([c["structure"] for c in candidates], [m["dotBracket"] for m in members])
        self.assertEqual(candidates[0]["weight"], members[0]["weight"])
        # the heaviest member's constraints are its hard pairs, on the RNA
        hard = {tuple(p) for p in meta["parameters"]["hard_pairs"]}
        self.assertEqual(hard, {(i + self.offset, j + self.offset) for i, j in members[0]["constraints"]})
        arms = json.loads((out / "arms.json").read_text())["records"][0]
        self.assertEqual(arms["rows"], len(self.arms))
        from desktop.runtime.project import register_task_run
        (Path(result["run_dir"]) / "status.json").write_text(json.dumps({"task_id": "i1", "kind": "structure", "status": "succeeded"}))
        register_task_run(self.project, "i1")
        listed = handle({"operation": "read_structure", "path": str(self.project), "structure_id": "i1"}, repo_root=ROOT)
        rec = listed["records"][0]
        self.assertEqual(rec["window_start"], 31)
        self.assertEqual(len(rec["candidates"]), len(members))
        self.assertEqual(listed["meta"]["records"][0]["iris"]["selectedMember"], 0)
        progress = json.loads((Path(result["run_dir"]) / "progress.json").read_text())
        self.assertEqual(progress["stage"], "done")

    def test_standalone_source_cli_matches_desktop_window(self):
        public = Path(self.tmp.name) / 'standalone'
        for name in (ROOT / 'scripts/build/iris-cli-files.txt').read_text().splitlines():
            source = (ROOT / name).resolve().relative_to(ROOT)
            self.assertNotIn(source.parts[0], {'desktop', 'web', '.git'})
            target = public / source
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / source, target)
        data = self.project / 'data'
        rows = (data / 'a.hyb').read_bytes().splitlines(keepends=True)
        split = len(rows) // 2
        (public / 'one.hyb').write_bytes(b''.join(rows[:split]))
        (public / 'two.hyb.gz').write_bytes(gzip.compress(b''.join(rows[split:])))
        ref = public / 'ref.fa.gz'
        ref.write_bytes(gzip.compress((data / 'ref.fa').read_bytes()))
        second = public / 'two.hyb.gz'
        if os.name != 'nt':  # Windows can restrict symlink creation.
            (public / 'two.hyb').symlink_to(second.name)
            (public / 'ref.fa').symlink_to(ref.name)
            second, ref = public / 'two.hyb', public / 'ref.fa'
        args = [sys.executable, '-IB', 'bin/iris', '--reference', ref.name, '--hyb', 'one.hyb', '--hyb', second.name,
                '--gene', GENE, '--start', '31', '--end', '170']
        for flags, message in ((['--stem-lengths', '4', '1_2'], 'IRIS stem length'),
                               (['--ensemble-size', '0_2'], 'IRIS ensemble size K'),
                               (['--candidate-count', '1_2'], 'IRIS candidate count C'),
                               (['--fraction', '0.7_5'], 'IRIS support quantile')):
            with self.subTest(flags=flags):
                bad = subprocess.run([*args, *flags], cwd=public, capture_output=True, text=True)
                self.assertNotEqual(bad.returncode, 0)
                self.assertIn(message, bad.stderr)
        result = subprocess.run([*args, '--ensemble-size', '+2.0', '--candidate-count', '7e0', '--fraction', '.75',
                                 '--stem-lengths', '7, 5,5', '4'], cwd=public, capture_output=True, text=True, check=True)
        cli = json.loads(result.stdout)
        self.assertEqual(cli['parameters'], {'K': 2, 'C': 7, 'fraction': 0.75, 'kRange': [4, 5, 7]})
        native = self._run(task_id='cli-control', parameters={'C': 7, 'kRange': [4, 5, 7]})
        self.assertTrue(native['ok'], native)
        desktop = json.loads(Path(native['outputs']['meta']).read_text())['records'][0]['iris']
        self.assertEqual(cli['region'], dict(gene=GENE, start=31, end=170, sequence=self.region))
        self.assertEqual(cli['evidence']['inputRecords'], len(self.arms))
        self.assertEqual(cli['ensemble'], desktop['ensemble'])
        self.assertEqual(len(cli['candidates']), 2)
        for candidate, member in zip(cli['candidates'], desktop['members'], strict=True):
            for field in ('dotBracket', 'pairs', 'constraints', 'energy', 'weight'):
                self.assertEqual(candidate[field], member[field])

    def test_scalar_task_parameters(self):
        cases = json.loads(Path(__file__).with_name("iris_scalars.json").read_text(encoding="utf-8"))
        for i, (params, want) in enumerate(cases["valid"]):
            with self.subTest(params=params):
                result = self._run(task_id=f"scalar-{i}", parameters=params)
                self.assertTrue(result["ok"], result)
                meta = json.loads(Path(result["outputs"]["meta"]).read_text())
                actual = meta["records"][0]["iris"]["parameters"]
                self.assertEqual(actual, dict(want, kRange=[4, 5, 6, 7]))
                self.assertEqual((type(actual["K"]), type(actual["C"]), type(actual["fraction"])), (int, int, float))
        for field, case in cases["invalid"].items():
            for i, value in enumerate([*case["values"], float("nan"), float("inf"), -float("inf")]):
                with self.subTest(field=field, value=value):
                    with self.assertRaises(ValueError) as caught:
                        self._run(task_id=f"bad-{field}-{i}", parameters={field: value})
                    self.assertEqual(str(caught.exception), case["message"])

    def test_refusals(self):
        for extra, message in (({"parameters": {"K": 4}}, "IRIS ensemble size K"),
                               ({"parameters": {"K": 2, "stemLength": 4, "beam": 20}}, "^unknown IRIS parameter: beam, stemLength$"),
                               ({"windows": [{"start": 1, "end": 40}, {"start": 50, "end": 100}]}, "one window"),
                               ({"source": "paste", "sequence": "ACGU" * 10, "mode": "genome", "windows": None},
                                "Reference region")):
            with self.assertRaisesRegex(ValueError, message, msg=extra):
                self._run(task_id="bad", **extra)
        # no chimera inside: the run fails with the web's diagnosis
        failed = self._run(task_id="none", windows=[{"start": 171, "end": 230}])
        self.assertFalse(failed["ok"])
        self.assertIn(f"No complete HYB arm pair is inside {GENE}:171-230", failed["error"])

    def test_region_check_from_the_index(self):
        sys.path.insert(0, str(ROOT / "pipeline"))
        from hyb2analysis import index, query

        (self.project / "samples" / "S1").mkdir(parents=True)
        index.build(self.project / "data" / "a.hyb", self.project / "samples" / "S1" / "a.hyb2idx")
        manifest = json.loads((self.project / "project.json").read_text())
        manifest["references"] = [{"id": "R", "path": "data/ref.fa", "sequences": [{"name": GENE, "length": len(self.sequence)}]}]
        manifest["samples"] = [{"id": "S1", "reference_id": "R", "outputs": {"index": "samples/S1/a.hyb2idx"}}]
        (self.project / "project.json").write_text(json.dumps(manifest))
        try:
            def check(start, end):
                response = handle({"operation": "analysis_iris_region", "project_path": str(self.project),
                                   "sample_ids": ["S1"], "gene": GENE, "start": start, "end": end}, repo_root=ROOT)
                json.loads(_dumps_line(response))
                return response["result"]
            inside = check(31, 170)
            self.assertEqual(inside["counts"], {"total": len(self.arms) + 2, "sameRna": len(self.arms) + 1,
                                                "forward": len(self.arms) + 1, "contained": len(self.arms), "tooWide": 0})
            self.assertEqual(inside["message"], "")
            outside = check(171, 230)
            self.assertEqual(outside["counts"]["contained"], 0)
            self.assertTrue(outside["message"].startswith(f"No complete HYB arm pair is inside {GENE}:171-230."))
            self.assertEqual(outside["suggestion"], {"rna": GENE, "start": 1, "end": len(self.sequence)})
            # the same diagnosis as the run's own reading of the .hyb
            _, found = iris.region_arms([self.project / "data" / "a.hyb"], GENE, self.sequence, 171, 230)
            self.assertEqual((found["counts"], found["suggestion"]), (outside["counts"], outside["suggestion"]))
        finally:
            query.close_all()


if __name__ == "__main__":
    unittest.main()
