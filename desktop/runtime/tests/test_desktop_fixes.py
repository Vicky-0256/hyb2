import io
import json
import os
import subprocess
import sys
import tempfile
import threading
import unittest
from pathlib import Path
from unittest import mock

from desktop.runtime import bridge
from desktop.runtime.bridge import handle, serve
from desktop.runtime.detector import detect_runtime
from desktop.runtime.project import new_project, register_task_run, save_analysis_run, update_ui_state, validate_project_directory

ROOT = Path(__file__).resolve().parents[3]


class ServeModeTests(unittest.TestCase):
    def test_serve_subprocess_one_line_per_request(self):
        lines = [json.dumps({"id": 1, "request": {"operation": "nope"}}), "not json", "", json.dumps({"id": 2, "request": {"operation": "validate_project", "path": ""}}), json.dumps({"id": 3})]
        completed = subprocess.run([sys.executable, "-m", "desktop.runtime.bridge", "--repo-root", str(ROOT), "--serve"], input="\n".join(lines) + "\n", cwd=ROOT, capture_output=True, text=True, timeout=120)
        self.assertEqual(completed.returncode, 0, completed.stderr)
        out = [json.loads(line) for line in completed.stdout.splitlines()]
        self.assertEqual([item["id"] for item in out], [1, None, 2, 3])
        self.assertTrue(all(item["response"]["ok"] is False and item["response"]["error"] for item in out))
        self.assertIn("unsupported bridge operation", out[0]["response"]["error"])

    def test_serve_redirects_stray_prints_and_handles_exceptions(self):
        def noisy(request, repo_root=None):
            print("stray output from a library")
            if request.get("boom"): raise RuntimeError("kaboom")
            return {"ok": True, "echo": request.get("value")}
        stdin = io.BytesIO(b'{"id": 7, "request": {"value": 1}}\n{"id": 8, "request": {"boom": true}}\n')
        stdout, stderr = io.StringIO(), io.StringIO()
        with mock.patch.object(bridge, "handle", noisy), mock.patch.object(sys, "stderr", stderr):
            self.assertEqual(serve(None, stdin=stdin, stdout=stdout), 0)
        out = [json.loads(line) for line in stdout.getvalue().splitlines()]
        self.assertEqual(out, [{"id": 7, "response": {"ok": True, "echo": 1}}, {"id": 8, "response": {"ok": False, "error": "kaboom"}}])
        self.assertIn("stray output", stderr.getvalue())


class ServeEncodingTests(unittest.TestCase):
    def test_unencodable_response_fails_only_its_request(self):
        import numpy as np
        def handler(request, repo_root=None):
            return {"ok": True, "path": Path("/x")} if request.get("bad") else {"ok": True, "n": np.int64(3)}
        stdin = io.BytesIO(b'{"id": 1, "request": {"bad": true}}\n{"id": 2, "request": {}}\n')
        stdout = io.StringIO()
        with mock.patch.object(bridge, "handle", handler):
            self.assertEqual(serve(None, stdin=stdin, stdout=stdout), 0)
        out = [json.loads(line) for line in stdout.getvalue().splitlines()]
        self.assertEqual([o["id"] for o in out], [1, 2])
        self.assertFalse(out[0]["response"]["ok"])
        self.assertIn("could not be encoded", out[0]["response"]["error"])
        self.assertEqual(out[1]["response"], {"ok": True, "n": 3})
        self.assertEqual(json.loads(bridge._dumps_line({"r": np.float32("nan")})), {"r": None})

    def test_project_json_has_no_nan(self):
        from desktop.runtime.project import _atomic_write_json
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "x.json"
            _atomic_write_json(path, {"a": float("inf"), "b": [float("nan"), 1.5]})
            self.assertEqual(json.loads(path.read_text(), parse_constant=lambda c: self.fail(c)), {"a": None, "b": [None, 1.5]})


class ComradesSeedTests(unittest.TestCase):
    def test_an_endless_gzip_hyb_line_fails_instead_of_loading_whole(self):
        import gzip
        from desktop.runtime import comrades
        from hyb2analysis import hybfile
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "s.hyb.gz"
            with gzip.open(path, "wb") as out:
                out.write(b"A" * (hybfile.MAX_LINE + 1))
            with self.assertRaisesRegex(ValueError, "longer than"):
                comrades.evidence_arms([path], "G", "ACGU", 1, 4)

    def test_evidence_keeps_twenty_record_ids_a_pair(self):
        from desktop.runtime import comrades
        mapped = [(1, 9, f"r{k}") for k in range(25)] + [(2, 8, None), (2, 8, "x")]
        entries = comrades.aggregate_evidence(mapped)
        self.assertEqual([(e["one"], e["two"], e["count"]) for e in entries], [(1, 9, 25), (2, 8, 2)])
        self.assertEqual(entries[0]["recordIds"], [f"r{k}" for k in range(20)])
        self.assertEqual(entries[1]["recordIds"], ["x"])
        self.assertEqual(comrades._record_id(b"read7\tACGU", 3), "read7")
        self.assertEqual(comrades._record_id(b"\tACGU", 3), "line_3")

    def test_a_base_pair_is_counted_once_whichever_arm_was_sequenced_first(self):
        # Chimeras occur in both arm orders; (1, 9) and (9, 1) halved the pair's
        # support and gave the same constraint twice.
        from desktop.runtime import comrades
        mapped = [(1, 9, "a"), (9, 1, "b"), (2, 8, "a"), (8, 2, "b"), (8, 2, "c"), (5, 5, "d")]
        entries = comrades.aggregate_evidence(mapped)
        self.assertEqual([(e["one"], e["two"], e["count"], e["recordIds"]) for e in entries],
                         [(2, 8, 3, ["a", "b", "c"]), (1, 9, 2, ["a", "b"])])
        constraints = comrades.stems_to_constraints(comrades.merge_touching_stems(entries), 75)
        self.assertEqual([(c["left"], c["right"], c["length"], c["support"]) for c in constraints], [(1, 9, 2, 5)])

    def test_weak_pairs_are_trimmed_from_the_ends_of_a_stem(self):
        # hyb2 gk-dev's test_bp2hyb_trims_weak_helix_ends: 10-50..14-46 is well
        # supported, 9-51 and 15-45 extend it with 2 and 1 observations.
        from desktop.runtime import comrades
        counts = [(9, 51, 2), (10, 50, 100), (11, 49, 100), (12, 48, 90), (13, 47, 80), (14, 46, 100), (15, 45, 1),
                  (30, 70, 7), (31, 69, 1)]
        evidence = [{"one": one, "two": two, "count": count} for one, two, count in counts]

        def stems(*arguments):
            return [(s["rank"], s["oneStart"], s["oneEnd"], s["twoStart"], s["twoEnd"], s["length"], s["support"],
                     [p["count"] for p in s["evidencePairs"]]) for s in comrades.merge_touching_stems(evidence, *arguments)]

        # 1 of 7 is above 5%, so the second stem keeps both pairs
        self.assertEqual(stems(), [(1, 10, 14, 46, 50, 5, 470, [100, 100, 90, 80, 100]), (2, 30, 31, 69, 70, 2, 8, [7, 1])])
        self.assertEqual(stems(0), [(1, 9, 15, 45, 51, 7, 473, [2, 100, 100, 90, 80, 100, 1]), (2, 30, 31, 69, 70, 2, 8, [7, 1])])
        self.assertEqual(stems(0.2)[1], (2, 30, 30, 70, 70, 1, 7, [7]))
        trimmed = comrades.stems_to_constraints(comrades.merge_touching_stems(evidence), 75)[0]
        self.assertEqual(trimmed["pairs"], [(10, 50), (11, 49), (12, 48), (13, 47), (14, 46)])

    def test_seed_is_trimmed_and_cut_as_the_web(self):
        from desktop.runtime import comrades
        # String(seed).trim().slice(0, 128): UTF-16 units, JavaScript whitespace
        self.assertEqual(comrades.js_seed("\ufeff run1 \t"), "run1")
        self.assertEqual(comrades.js_seed("abc\x1c"), "abc\x1c")
        self.assertEqual(comrades.js_seed("\U0001F600" * 100), "\U0001F600" * 64)
        self.assertEqual(comrades.hash_seed(comrades.js_seed("\U0001F600" * 100)), 817844933)  # node

    def test_seed_cut_through_an_emoji(self):
        from desktop.runtime import comrades, structure
        seed = comrades.js_seed("a" * 127 + "\U0001F600")
        self.assertEqual(comrades.hash_seed(seed), 1758622465)  # node: the half emoji is hashed
        label = comrades.seed_label(seed)
        self.assertEqual(label, "a" * 127 + "\ufffd")
        with tempfile.TemporaryDirectory() as tmp:  # and the .dbn header can be written
            structure._atomic_write_text(Path(tmp) / "x.dbn", structure._describe(
                {"engine": "COMRADES(ViennaRNA)", "temperature": 37.0, "min_loop": 3, "random_folds": 1, "seed": label}))

    def test_empty_sequence(self):
        from desktop.runtime import vienna
        with self.assertRaisesRegex(ValueError, "no sequence"):
            vienna.fold("", {"temperature": 37, "min_loop": 3})


class ManifestConcurrencyTests(unittest.TestCase):
    def test_concurrent_writers_do_not_lose_runs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "p"; new_project(root, "P")
            errors = []
            def runs():
                try:
                    for i in range(10): save_analysis_run(root, {"algorithm": "structure", "i": i})
                except Exception as error: errors.append(error)
            def states():
                try:
                    for i in range(20): update_ui_state(root, {"desktop": {"route": "analysis"}, "analysis": {"n": i}})
                except Exception as error: errors.append(error)
            threads = [threading.Thread(target=runs) for _ in range(3)] + [threading.Thread(target=states) for _ in range(2)]
            for thread in threads: thread.start()
            for thread in threads: thread.join()
            self.assertEqual(errors, [])
            manifest = json.loads((root / "project.json").read_text())
            self.assertEqual(len(manifest["runs"]), 30)
            self.assertEqual(len(manifest["artifacts"]), 30)
            self.assertEqual(validate_project_directory(root, check_files=True), [])
            self.assertEqual([p.name for p in root.iterdir() if p.name.endswith(".tmp")], [])

    def test_concurrent_processes_do_not_lose_runs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "p"; new_project(root, "P")
            code = "import sys\nfrom pathlib import Path\nfrom desktop.runtime.project import save_analysis_run\nfor i in range(8): save_analysis_run(Path(sys.argv[1]), {'algorithm': 'structure'})\n"
            procs = [subprocess.Popen([sys.executable, "-c", code, str(root)], cwd=ROOT) for _ in range(4)]
            self.assertEqual([proc.wait(timeout=120) for proc in procs], [0, 0, 0, 0])
            self.assertEqual(len(json.loads((root / "project.json").read_text())["runs"]), 32)


class UiStateAndVersionTests(unittest.TestCase):
    def test_new_project_default_and_nested_ui_state(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "p"
            with mock.patch.dict(os.environ, {"HYB2_APP_VERSION": "9.8.7"}):
                manifest = json.loads(new_project(root, "P").read_text())
            self.assertEqual(manifest["application"]["version"], "9.8.7")
            self.assertEqual(manifest["ui_state"], {"desktop": {"route": "home"}, "analysis": {}})
            state = {"desktop": {"route": "tasks"}, "analysis": {"page": "structure", "region": {"start": 1, "end": 50}, "files": ["inputs/a.hyb"]}}
            result = handle({"operation": "update_ui_state", "path": str(root), "ui_state": state})
            self.assertEqual(result["ui_state"], state)
            self.assertEqual(json.loads((root / "project.json").read_text())["ui_state"], state)
            with self.assertRaises(ValueError):
                update_ui_state(root, {"desktop": {"route": "home"}, "analysis": {"file": "/etc/passwd"}})

    def test_new_project_without_env_uses_default_version(self):
        with tempfile.TemporaryDirectory() as tmp, mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("HYB2_APP_VERSION", None)
            manifest = json.loads(new_project(Path(tmp) / "p", "P").read_text())
            self.assertEqual(manifest["application"]["version"], "0.1.3")


class RegisterTaskRunTests(unittest.TestCase):
    def _task(self, root, task_id, **status):
        run_dir = root / "runs" / task_id; run_dir.mkdir()
        (run_dir / "stdout.log").write_text("out"); (run_dir / "stderr.log").write_text("")
        (run_dir / "cplfold.out").write_text("....")
        base = {"task_id": task_id, "kind": "cplfold", "status": "succeeded", "exit_code": 0, "error": None, "run_dir": str(run_dir),
                "log_paths": [str(run_dir / "stdout.log"), "runs/%s/stderr.log" % task_id], "started_at": "2026-10-13T00:00:00Z", "finished_at": "2026-10-13T00:01:00Z", "output": None}
        base.update(status)
        (run_dir / "status.json").write_text(json.dumps(base))
        (run_dir / "result.json").write_text(json.dumps({"ok": True, "output": str(run_dir / "cplfold.out")}))
        return run_dir

    def test_register_is_idempotent_and_relative(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "p"; new_project(root, "P")
            self._task(root, "cplfold-1")
            first = handle({"operation": "register_task_run", "path": str(root), "task_id": "cplfold-1"})
            second = handle({"operation": "register_task_run", "path": str(root), "task_id": "cplfold-1"})
            self.assertEqual(first["run"], second["run"])
            runs = json.loads((root / "project.json").read_text())["runs"]
            self.assertEqual(len(runs), 1)
            run = runs[0]
            self.assertEqual((run["id"], run["kind"], run["status"], run["exit_code"]), ("cplfold-1", "cplfold", "succeeded", 0))
            self.assertEqual(run["output_paths"], ["runs/cplfold-1/cplfold.out", "runs/cplfold-1/result.json"])
            self.assertEqual(run["log_paths"], ["runs/cplfold-1/stdout.log", "runs/cplfold-1/stderr.log"])
            self.assertEqual(validate_project_directory(root, check_files=True), [])

    def test_register_updates_status_and_rejects_escapes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "p"; new_project(root, "P")
            outside = Path(tmp) / "secret.txt"; outside.write_text("x")
            run_dir = self._task(root, "task-2", status="failed", error="boom", log_paths=[str(outside), "../secret.txt", "/etc/passwd"], output={"output": str(outside)})
            (run_dir / "result.json").write_text(json.dumps({"ok": False, "output": "../../secret.txt"}))
            run = register_task_run(root, "task-2")
            self.assertEqual(run["status"], "failed")
            self.assertEqual(run["error"], {"code": "task_failed", "message": "boom"})
            self.assertEqual(run["output_paths"], ["runs/task-2/result.json"])
            self.assertEqual(run["log_paths"], [])
            status = json.loads((run_dir / "status.json").read_text()); status.update(status="succeeded", error=None)
            (run_dir / "status.json").write_text(json.dumps(status))
            self.assertEqual(register_task_run(root, "task-2")["status"], "succeeded")
            self.assertEqual(len(json.loads((root / "project.json").read_text())["runs"]), 1)
            for bad in ("../x", "a/b", "", "1bad"):
                with self.assertRaises(ValueError):
                    handle({"operation": "register_task_run", "path": str(root), "task_id": bad})
            with self.assertRaises(ValueError):
                register_task_run(root, "missing-task")


class CplfoldLimitTests(unittest.TestCase):
    def _fold_module(self):
        try:
            from desktop.runtime.structure import load_cplfold_web
            return load_cplfold_web(ROOT)
        except ImportError as error:
            self.skipTest("cplfold_web dependencies unavailable: %s" % error)

    def test_detector_reports_native_max_length(self):
        with mock.patch.dict(os.environ, {"HYB2_CPLFOLD_MAX_NT": "1234"}):
            capability = detect_runtime(ROOT).capability("structure.cplfold_native").to_dict()
        self.assertEqual(capability["details"]["max_sequence_length"], 1234)

    def test_native_predictions_do_not_change_browser_limits(self):
        module = self._fold_module()
        with mock.patch.dict(os.environ, {"HYB2_CPLFOLD_MAX_NT": "1000"}):
            result = handle({"operation": "cplfold_predict", "sequence": "A" * 900}, repo_root=ROOT)
        self.assertTrue(result["ok"])
        self.assertEqual((result["maxSequenceLength"], result["dotBracket"]), (1000, "." * 900))
        with self.assertRaisesRegex(ValueError, "500"):
            module.fold_json(json.dumps({"sequence": "A" * 501, "maxSequenceLength": 501, "hard_max_length": 1000}))
        with mock.patch.dict(os.environ, {"HYB2_CPLFOLD_MAX_NT": "44"}), self.assertRaisesRegex(ValueError, "44 nt"):
            handle({"operation": "cplfold_predict", "sequence": "A" * 45}, repo_root=ROOT)
        ordinary = json.loads(module.fold_json(json.dumps({"sequence": "A" * 75})))
        self.assertEqual((ordinary["maxSequenceLength"], ordinary["dotBracket"]), (75, "." * 75))

    def test_interleaved_beam_limits_by_host_and_strand_layout(self):
        module = self._fold_module()  # the same cached module as cplfold_predict
        # The native host can have exactly the browser's length ceiling. It still
        # selects its own beam policy; neither the last call nor the JSON selects it.
        with mock.patch.dict(os.environ, {"HYB2_CPLFOLD_MAX_NT": "500"}):
            for sequence in ("GGGAAACCC", "GGG&CCC"):
                native = {"operation": "cplfold_predict", "sequence": sequence, "host": "web"}
                browser = {"sequence": sequence, "host": "desktop"}
                for beam in (200, 201, 1000, 1001):
                    with self.subTest(host="desktop", sequence=sequence, beam=beam):
                        request = dict(native, parameters={"beam": beam, "host": "web"})
                        if beam <= (200 if "&" in sequence else 1000):
                            self.assertEqual(handle(request, repo_root=ROOT)["parameters"]["beamSize"], beam)
                        else:
                            with self.assertRaisesRegex(ValueError, "Beam size"):
                                handle(request, repo_root=ROOT)
                    with self.subTest(host="web", sequence=sequence, beam=beam):
                        request_json = json.dumps(dict(browser, beamSize=beam))
                        if beam == 200:
                            self.assertEqual(json.loads(module.fold_json(request_json))["parameters"]["beamSize"], 200)
                        else:
                            with self.assertRaisesRegex(ValueError, "Beam size"):
                                module.fold_json(request_json)

    def test_catalog_defaults_and_explicit_overrides_reach_predict(self):
        module = self._fold_module()
        from hyb2analysis.engine_parameters import CPL_SEARCH

        changed = dict(beamSize=27, maxPhase1=4, energyDelta=6.5, alpha=0.25, beta=0.75)
        with mock.patch.dict(CPL_SEARCH, {key: (*CPL_SEARCH[key][:2], value) for key, value in changed.items()}):
            for sequence, alpha in (("GGGAAACCC", 0.25), ("GGG&CCC", 0.0)):
                request = {"operation": "cplfold_predict", "sequence": sequence}
                expected = dict(changed, alpha=alpha)
                with self.subTest(sequence=sequence, supplied=False):
                    browser = json.loads(module.fold_json(json.dumps({"sequence": sequence})))["parameters"]
                    native = handle(request, repo_root=ROOT)["parameters"]
                    self.assertEqual({k: browser[k] for k in changed}, expected)
                    self.assertEqual({k: native[k] for k in changed}, expected)
                for settings in (dict(beam=17, max_phase1=2, delta=0, model="DP03"),
                                 dict(beamSize=17, maxPhase1=2, energyDelta=0, energyModel="DP03")):
                    with self.subTest(sequence=sequence, supplied=settings):
                        actual = handle(dict(request, parameters=dict(settings, alpha=0, beta=0)), repo_root=ROOT)["parameters"]
                        explicit = dict(beamSize=17, maxPhase1=2, energyDelta=0, energyModel="DP03", alpha=0, beta=0)
                        self.assertEqual({k: actual[k] for k in explicit}, explicit)


class PeakMemoryTests(unittest.TestCase):
    @unittest.skipUnless(sys.platform == "darwin", "macOS footprint accounting")
    def test_peak_includes_memory_macos_does_not_keep_resident(self):
        from desktop.runtime import structure

        block = bytearray(64 * 1024 * 1024)
        for offset in range(0, len(block), 4096):
            block[offset] = 1
        footprint = structure._darwin_peak_footprint_bytes()
        self.assertGreaterEqual(footprint, len(block))
        self.assertGreaterEqual(structure._peak_rss_bytes(), footprint)

    def test_peak_falls_back_to_resident_size_when_footprint_is_unreadable(self):
        from desktop.runtime import structure

        with mock.patch.object(structure, "_darwin_peak_footprint_bytes", return_value=0):
            self.assertGreater(structure._peak_rss_bytes(), 0)


class TaskMainTests(unittest.TestCase):
    """The single-shot task bridge always leaves a result.json the shell can show."""

    def run_main(self, handler):
        with tempfile.TemporaryDirectory() as tmp:
            request = Path(tmp) / "task-request.json"
            request.write_text(json.dumps({"operation": "validate_project", "path": tmp}))
            argv = ["bridge", "--request", str(request)]
            with mock.patch.object(sys, "argv", argv), mock.patch.object(bridge, "handle", handler), \
                    mock.patch("sys.stdout", new_callable=io.StringIO) as out, mock.patch("sys.stderr", new_callable=io.StringIO):
                code = bridge.main()
            return code, json.loads((Path(tmp) / "result.json").read_text()), json.loads(out.getvalue())

    def test_an_unexpected_exception_is_reported(self):
        def boom(request, repo_root=None):
            raise KeyError("sample_id")
        code, result, printed = self.run_main(boom)
        self.assertEqual(code, 1)
        self.assertEqual(result, {"ok": False, "error": "KeyError: 'sample_id'"})
        self.assertEqual(printed, result)

    def test_a_result_with_odd_values_still_succeeds(self):
        code, result, printed = self.run_main(lambda request, repo_root=None: {"ok": True, "path": Path("/x/y"), "nan": float("nan"), "name": "é"})
        self.assertEqual(code, 0)
        self.assertEqual(result, {"ok": True, "path": "/x/y", "nan": None, "name": "é"})
        self.assertEqual(printed, result)


if __name__ == "__main__":
    unittest.main()
