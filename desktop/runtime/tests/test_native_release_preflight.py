import importlib.util
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CHECK = ROOT / 'scripts' / 'verify-native-release.py'


def load_script(path):
    spec = importlib.util.spec_from_file_location(path.stem, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_stage():
    return load_script(ROOT / 'scripts' / 'stage-native.py')


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


class PackagedSourceTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.repo = self.root / 'source'
        self.native = self.root / 'native'
        self.stage = load_stage()
        self.verify = load_script(CHECK)
        self.web = load_script(ROOT.parent / 'scripts' / 'bundle_sources.py')
        for directory in self.stage.SOURCE_DIRS:
            (self.repo / directory).mkdir(parents=True)
        self.write('bin/cplfold')
        self.write('web/cplfold/cplfold_web.py')
        for name in (*self.web.WEB_MODULES, *self.web.WEB_NOTICES):
            self.write('third_party/cplfold/' + name)
        self.shared = 'third_party/cplfold/Utils/shared.py'
        self.write(self.shared, 'original')
        self.write('third_party/cplfold/Utils/energy_params/tracked.dat')
        self.write('pipeline/main.py')
        self.write('pipeline/tests/fixture.py')
        self.write('pipeline/data/local.txt')
        self.write('pipeline/__pycache__/bytecode.pyc')

    def write(self, name, content='sample'):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding='utf-8')

    def track_sources(self):
        if not shutil.which('git'):
            self.skipTest('Git checkout test needs Git')
        subprocess.run(['git', 'init', '-q'], cwd=self.repo, check=True)
        subprocess.run(['git', 'add', '-f', '.'], cwd=self.repo, check=True)

    def manifest(self):
        return {p.relative_to(self.native).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                for p in self.native.rglob('*') if p.is_file()}

    def check_selection(self):
        self.stage.stage_sources(self.repo, self.native)
        files = self.manifest()
        self.verify.verify_sources(self.repo, files, self.stage)
        self.assertNotIn('pipeline/tests/fixture.py', files)
        self.assertNotIn('pipeline/data/local.txt', files)
        self.assertNotIn('pipeline/__pycache__/bytecode.pyc', files)
        package, assets = self.root / 'browser', self.root / 'assets'
        self.web.stage_web_sources(self.repo, package, assets)
        for root in (self.native / 'third_party/cplfold', package):
            self.assertEqual((root / 'Utils/shared.py').read_text(), 'local edit')
            self.assertTrue((root / 'Utils/energy_params/tracked.dat').is_file())
            self.assertFalse((root / 'Utils/peer.py').exists())
            self.assertFalse((root / 'Utils/energy_params/peer.dat').exists())
        self.assertTrue((package / 'cplfold_web.py').is_file())
        self.assertTrue((assets / 'CPLfold-LICENSE.txt').is_file())

    def test_checkout_packages_tracked_paths_with_working_tree_contents(self):
        self.track_sources()
        self.write(self.shared, 'local edit')
        self.write('third_party/cplfold/Utils/peer.py')
        self.write('third_party/cplfold/Utils/energy_params/peer.dat')
        self.check_selection()

    def test_source_archive_needs_no_git_metadata(self):
        self.write(self.shared, 'local edit')
        self.check_selection()

    def test_preflight_finds_missing_extra_and_changed_sources(self):
        self.track_sources()
        self.stage.stage_sources(self.repo, self.native)
        original = self.manifest()
        self.write('pipeline/added.py')
        subprocess.run(['git', 'add', 'pipeline/added.py'], cwd=self.repo, check=True)
        with self.assertRaisesRegex(ValueError, r'missing bundled sources: pipeline/added\.py'):
            self.verify.verify_sources(self.repo, original, self.stage)
        shutil.copy2(self.repo / 'pipeline/added.py', self.native / 'pipeline/added.py')
        (self.native / 'pipeline/extra.py').write_text('extra')
        with self.assertRaisesRegex(ValueError, r'unexpected bundled sources: pipeline/extra\.py'):
            self.verify.verify_sources(self.repo, self.manifest(), self.stage)
        (self.native / 'pipeline/extra.py').unlink()
        self.write('pipeline/added.py', 'changed after staging')
        with self.assertRaisesRegex(ValueError, r'bundled source is stale.*pipeline/added\.py'):
            self.verify.verify_sources(self.repo, self.manifest(), self.stage)

    def test_removed_tracked_input_is_reported(self):
        self.track_sources()
        (self.repo / self.shared).unlink()
        with self.assertRaisesRegex(ValueError, 'package source is missing.*Utils/shared.py'):
            self.stage.stage_sources(self.repo, self.native)


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
