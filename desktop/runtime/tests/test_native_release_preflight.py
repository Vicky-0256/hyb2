import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CHECK = ROOT / 'scripts' / 'verify-native-release.py'


def load_stage():
    spec = importlib.util.spec_from_file_location('native_stage_under_test', ROOT / 'scripts' / 'stage-native.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

class NativeReleasePreflightTests(unittest.TestCase):
    def test_missing_resource_fails_with_acquisition_instructions(self):
        with tempfile.TemporaryDirectory() as d:
            p = subprocess.run([sys.executable, str(CHECK), '--resource-root', d], text=True, capture_output=True)
        self.assertEqual(p.returncode, 2)
        self.assertIn('Acquire a verified relocatable CPython 3.11.x', p.stderr)

    def test_development_manifest_is_never_release_ready(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            (root / 'native-manifest.json').write_text(json.dumps({'manifest_version':'1.0', 'portable': False, 'mode': 'development-external-python'}))
            p = subprocess.run([sys.executable, str(CHECK), '--resource-root', d], text=True, capture_output=True)
        self.assertEqual(p.returncode, 2)
        self.assertIn('not portable standalone', p.stderr)


@unittest.skipIf(os.name == 'nt', 'console scripts are launchers on Windows')
class RelocatableScriptTests(unittest.TestCase):
    def test_host_shebang_names_only_build_machine_interpreters(self):
        stage = load_stage()
        with tempfile.TemporaryDirectory() as d:
            cases = {'#!/bin/sh\n': None, '#!/usr/bin/env python3\n': None, 'import sys\n': None,
                     '#!/tmp/build/cpython/bin/python3\nimport sys\n': '/tmp/build/cpython/bin/python3',
                     '#!/tmp/build/cpython/bin/python3 -E\n': '/tmp/build/cpython/bin/python3'}
            for index, (text, expected) in enumerate(cases.items()):
                script = Path(d) / ('script%d' % index)
                script.write_text(text)
                self.assertEqual(stage.host_shebang(script), expected, text)

    def test_relocated_script_runs_with_the_interpreter_beside_it(self):
        stage = load_stage()
        with tempfile.TemporaryDirectory() as d:
            bin_dir = Path(d) / 'python' / 'bin'
            bin_dir.mkdir(parents=True)
            os.symlink(sys.executable, bin_dir / 'python3')
            script = bin_dir / 'tool'
            script.write_text('#!/nonexistent/build/bin/python3\n# -*- coding: UTF-8 -*-\nimport sys\nprint("ran", sys.argv[1])\n')
            script.chmod(0o755)
            untouched = bin_dir / 'config'
            untouched.write_text('#!/bin/sh\necho config\n')
            self.assertEqual(stage.relocate_scripts(Path(d) / 'python'), ['tool'])
            self.assertIsNone(stage.host_shebang(script))
            self.assertEqual(untouched.read_text(), '#!/bin/sh\necho config\n')
            self.assertEqual(os.stat(script).st_mode & 0o777, 0o755)
            moved = Path(d) / 'moved'
            (Path(d) / 'python').rename(moved)
            p = subprocess.run([str(moved / 'bin' / 'tool'), 'argument'], text=True, capture_output=True)
        self.assertEqual((p.returncode, p.stdout.strip()), (0, 'ran argument'), p.stderr)

    def test_recorded_paths_are_relative_to_the_runtime(self):
        stage = load_stage()
        with tempfile.TemporaryDirectory() as d:
            module = Path(d) / 'python' / 'lib' / 'numpy' / '__init__.py'
            module.parent.mkdir(parents=True)
            module.write_text('')
            self.assertEqual(stage.inside(d, module), 'python/lib/numpy/__init__.py')
            self.assertEqual(stage.inside(d, Path(d) / 'python'), 'python')


if __name__ == '__main__':
    unittest.main()
