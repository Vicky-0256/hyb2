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
        for name in (*self.web.REQUIRED_CPLFOLD_MODULES, *self.web.WEB_NOTICES):
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
        self.write('third_party/cplfold/archive_helper.py', 'number = 1\n')
        self.write('web/cplfold/bridge_support/nested.py', 'number = 2\n')
        self.check_selection()
        self.assertTrue((self.root / 'browser/archive_helper.py').is_file())
        self.assertTrue((self.root / 'browser/bridge_support/nested.py').is_file())

    def test_new_core_and_bridge_modules_import_from_both_packages(self):
        self.write('third_party/cplfold/CPLfold_parser.py', 'from new_core_helper import value\n')
        self.write('third_party/cplfold/new_core_helper.py', 'from new_core_package.nested import value\n')
        self.write('third_party/cplfold/new_core_package/__init__.py', '')
        self.write('third_party/cplfold/new_core_package/nested.py', 'from split_support.core import value\n')
        self.write('third_party/cplfold/split_support/core.py', 'value = 70\n')
        self.write('web/cplfold/cplfold_web.py',
                   'from CPLfold_parser import value\nfrom bridge_helper import increment\nmarker = value + increment\n')
        self.write('web/cplfold/bridge_helper.py', 'from bridge_package.nested import increment\n')
        self.write('web/cplfold/bridge_package/__init__.py', '')
        self.write('web/cplfold/bridge_package/nested.py', 'from split_support.bridge import increment\n')
        self.write('web/cplfold/split_support/bridge.py', 'increment = 3\n')
        excluded = ['example.py', 'example_with_paris.py', 'extract_paris_scores.py', 'docs/conf.py',
                    'tests/test_local.py', 'data/record.py', 'build-product.so']
        for name in excluded:
            self.write('third_party/cplfold/' + name)
        self.track_sources()
        self.write('third_party/cplfold/untracked_core.py')
        self.write('web/cplfold/untracked_bridge.py')

        self.stage.stage_sources(self.repo, self.native)
        self.verify.verify_sources(self.repo, self.manifest(), self.stage)
        package = self.root / 'browser'
        self.web.stage_web_sources(self.repo, package, self.root / 'assets')
        for name in [*excluded, 'untracked_core.py', 'untracked_bridge.py']:
            self.assertFalse((package / name).exists(), name)
        self.assertFalse((self.native / 'third_party/cplfold/untracked_core.py').exists())
        self.assertFalse((self.native / 'web/cplfold/untracked_bridge.py').exists())

        # Isolated interpreters have no import path into the original fixture.
        archive = shutil.make_archive(str(self.root / 'browser-modules'), 'zip', root_dir=package)
        browser_probe = 'import sys; sys.path.insert(0, sys.argv[1]); import cplfold_web; print(cplfold_web.marker)'
        native_probe = ('import sys; from pathlib import Path; sys.path.insert(0, sys.argv[1]); '
                        'from desktop.runtime.structure import load_cplfold_web; '
                        'print(load_cplfold_web(Path(sys.argv[2])).marker)')
        for args in ([browser_probe, archive], [native_probe, str(ROOT.parent), str(self.native)]):
            result = subprocess.run([sys.executable, '-I', '-B', '-c', *args],
                                    cwd=self.root, text=True, capture_output=True)
            self.assertEqual((result.returncode, result.stdout.strip()), (0, '73'), result.stderr)

    def test_conflicting_module_and_package_do_not_overwrite_each_other(self):
        self.write('third_party/cplfold/ambiguous.py')
        for child in ('__init__.py', 'nested.py'):
            with self.subTest(child=child):
                name = 'web/cplfold/ambiguous/' + child
                self.write(name)
                with self.assertRaisesRegex(ValueError, 'conflicting browser modules: .*ambiguous'):
                    self.web.stage_web_sources(self.repo, self.root / 'browser', self.root / 'assets')
                (self.repo / name).unlink()

    def test_package_resources_load_in_native_and_browser_from_archive_and_git(self):
        sources = {'shared_catalog': 'third_party/cplfold', 'bridge_catalog': 'web/cplfold'}
        for name, source in sources.items():
            prefix = f'{source}/{name}'
            self.write(prefix + '/__init__.py', '')
            self.write(prefix + '/defaults.json', '{"marker": 271}')
            self.write(prefix + '/data/current', name)
        self.write('third_party/cplfold/notes/local.txt')
        self.write('third_party/cplfold/data/sample.json')
        for mode in ('archive', 'checkout'):
            with self.subTest(mode=mode):
                if mode == 'checkout':
                    self.track_sources()
                    self.write('web/cplfold/bridge_catalog/untracked.json')
                native, browser = self.root / mode / 'native', self.root / mode / 'browser'
                self.stage.stage_sources(self.repo, native)
                self.web.stage_web_sources(self.repo, browser, self.root / mode / 'assets')
                self.assertFalse((browser / 'notes/local.txt').exists())
                self.assertFalse((browser / 'data/sample.json').exists())
                self.assertFalse((browser / 'bridge_catalog/untracked.json').exists())
                self.assertFalse((native / 'web/cplfold/bridge_catalog/untracked.json').exists())
                archive = shutil.make_archive(str(self.root / mode / 'source'), 'zip', root_dir=browser)
                probe = ('import sys, json; from importlib.resources import files; '
                         'sys.path.insert(0, sys.argv[1]); root = files(sys.argv[2]); '
                         'print(json.loads((root / "defaults.json").read_text())["marker"], '
                         '(root / "data" / "current").read_text())')
                for name, source in sources.items():
                    for path in (native / source, archive):
                        result = subprocess.run([sys.executable, '-I', '-B', '-c', probe, path, name],
                                                cwd=self.root, text=True, capture_output=True)
                        self.assertEqual((result.returncode, result.stdout.strip()),
                                         (0, '271 ' + name), result.stderr)

    def test_namespace_resource_collisions_are_reported_before_staging(self):
        self.write('third_party/cplfold/catalog/core.py')
        self.write('third_party/cplfold/catalog/defaults')
        for suffix in ('defaults', 'defaults/current', '__init__.py'):
            with self.subTest(suffix=suffix):
                name = 'web/cplfold/catalog/' + suffix
                self.write(name)
                with self.assertRaisesRegex(ValueError, 'conflicting browser .*catalog'):
                    self.web.stage_web_sources(self.repo, self.root / 'browser', self.root / 'assets')
                self.assertFalse((self.root / 'browser').exists())
                (self.repo / name).unlink()

    def test_browser_still_requires_utils(self):
        shutil.rmtree(self.repo / 'third_party/cplfold/Utils')
        with self.assertRaisesRegex(ValueError, 'missing: third_party/cplfold/Utils'):
            self.web.stage_web_sources(self.repo, self.root / 'browser', self.root / 'assets')

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
