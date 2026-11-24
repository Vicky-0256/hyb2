import json
import tempfile
import unittest
from pathlib import Path

from desktop.runtime.bridge import handle
from desktop.runtime.project import new_project


def cplfold_run(project, repo_root, **request):
    # A bridge operation that creates runs/<task_id> itself (task_paths.secure_run_dir).
    return handle({'operation': 'cplfold_run', 'project_path': str(project), 'sequence': 'AUGC', **request},
                  repo_root=repo_root)


class BridgeSecurityTests(unittest.TestCase):
    def test_invalid_task_ids_and_empty_paths_do_not_write(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp) / 'project'
            new_project(project, 'Test')
            for task_id in ('../escape', '/tmp/escape', 'a/b', 'a\\b', '.', '1bad'):
                with self.assertRaises(ValueError):
                    cplfold_run(project, Path(tmp), task_id=task_id)
            with self.assertRaises(ValueError):
                handle({'operation': 'cplfold_run', 'project_path': '', 'sequence': 'AUGC'})
            self.assertEqual(list((project / 'runs').iterdir()), [])

    def test_runs_symlink_cannot_escape_project(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp) / 'project'
            new_project(project, 'Test')
            outside = Path(tmp) / 'outside'
            outside.mkdir()
            (project / 'runs').rmdir()
            (project / 'runs').symlink_to(outside, target_is_directory=True)
            with self.assertRaisesRegex(ValueError, 'escapes project'):
                cplfold_run(project, Path(tmp))
            self.assertEqual(list(outside.iterdir()), [])

    def test_invalid_project_is_rejected_before_run_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            (project / 'project.json').write_text('{}')
            with self.assertRaisesRegex(ValueError, 'invalid project'):
                cplfold_run(project, Path(tmp))
            self.assertFalse((project / 'runs').exists())

    def test_task_id_cannot_be_reused(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp) / 'project'
            new_project(project, 'Test')
            self.assertEqual(cplfold_run(project, Path(tmp), task_id='validation-test')['status'], 'unavailable')
            self.assertTrue((project / 'runs' / 'validation-test').is_dir())
            with self.assertRaises(FileExistsError):
                cplfold_run(project, Path(tmp), task_id='validation-test')

    def test_run_task_is_not_a_bridge_operation(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp) / 'project'
            new_project(project, 'Test')
            with self.assertRaisesRegex(ValueError, 'unsupported bridge operation'):
                handle({'operation': 'run_task', 'path': str(project)})
            self.assertEqual(list((project / 'runs').iterdir()), [])

    def test_cplfold_request_is_structured_and_reports_unavailable(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp) / 'project'
            new_project(project, 'Test')
            result = handle({'operation': 'cplfold_run', 'project_path': str(project), 'sequence': 'AUGC', 'parameters': {'alpha': 0.5}}, repo_root=Path(tmp))
            self.assertFalse(result['ok'])
            self.assertEqual(result['status'], 'unavailable')
            with self.assertRaises(ValueError):
                handle({'operation': 'cplfold_run', 'project_path': str(project), 'sequence': 'AUGX'}, repo_root=Path(tmp))

    def test_cplfold_run_uses_the_shared_desktop_ranges(self):
        from hyb2analysis.engine_parameters import engine_parameters

        ranges = engine_parameters('desktop')['cplfold']['ranges']
        names = {'alpha': 'alpha', 'beta': 'beta', 'beam': 'beamSize', 'delta': 'energyDelta', 'max_phase1': 'maxPhase1'}
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / 'root'
            (root / 'bin').mkdir(parents=True)
            # stands in for bin/cplfold: records its arguments and environment
            (root / 'bin' / 'cplfold').write_text(
                'import json, os, sys\n'
                'out = sys.argv[sys.argv.index("--output") + 1]\n'
                'open(out, "w").write(json.dumps({"argv": sys.argv[1:], "numba": os.environ.get("NUMBA_CACHE_DIR")}))\n')
            project = Path(tmp) / 'project'
            new_project(project, 'Test')
            for key, name in names.items():
                lo, hi = ranges[name]
                integer = key in ('beam', 'max_phase1')
                step = 1 if integer else 0.5
                for bad in (lo - step, hi + step, float('nan')):
                    with self.subTest(key=key, value=bad), self.assertRaisesRegex(ValueError, 'out of bounds'):
                        cplfold_run(project, root, parameters={key: bad})
                for good in (lo, hi):
                    with self.subTest(key=key, value=good):
                        result = cplfold_run(project, root, parameters={key: good})
                        self.assertTrue(result['ok'], result)
                        recorded = json.loads(Path(result['output']).read_text())
                        self.assertIn(str(good), recorded['argv'])
                        # the per-run Numba cache is the one Numba reads
                        self.assertEqual(recorded['numba'], str(Path(result['run_dir']) / 'numba-cache'))


if __name__ == '__main__':
    unittest.main()
