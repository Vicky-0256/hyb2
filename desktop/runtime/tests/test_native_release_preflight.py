from contextlib import ExitStack, redirect_stderr, redirect_stdout
import importlib.util
import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

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
        self.web = load_script(ROOT.parent / 'scripts' / 'build' / 'bundle_sources.py')
        for directory in self.stage.SOURCE_DIRS:
            (self.repo / directory).mkdir(parents=True)
        for name in (*self.stage.SOURCE_FILES, *self.web.NOTICE_PATHS):
            self.write(name)
        self.shared = 'third_party/cplfold/cplfold/_internal/shared.py'
        self.write(self.shared, 'original')
        self.write('third_party/cplfold/cplfold/_internal/energy_params/tracked.dat')
        self.write('pipeline/main.py')
        self.write('pipeline/tests/fixture.py')
        self.write('pipeline/data/local.txt')
        self.write('pipeline/__pycache__/bytecode.pyc')

    def write(self, name, content='sample'):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content.encode('utf-8'))  # shared sources ship with LF on all platforms

    def track_sources(self):
        if not shutil.which('git'):
            self.skipTest('Git checkout test needs Git')
        subprocess.run(['git', 'init', '-q'], cwd=self.repo, check=True)
        subprocess.run(['git', 'add', '-f', '.'], cwd=self.repo, check=True)

    def manifest(self):
        return {p.relative_to(self.native).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                for p in self.native.rglob('*') if p.is_file() and p != self.native / 'native-manifest.json'}

    def check_selection(self):
        self.stage.stage_sources(self.repo, self.native)
        files = self.manifest()
        self.assertEqual(self.stage.verify_project_sources(self.repo, self.native, files, None), 'local')
        self.assertNotIn('pipeline/tests/fixture.py', files)
        self.assertNotIn('pipeline/data/local.txt', files)
        self.assertNotIn('pipeline/__pycache__/bytecode.pyc', files)
        package, assets = self.root / 'browser', self.root / 'assets'
        self.web.stage_web_sources(self.repo, package, assets)
        for root in (self.native / 'third_party/cplfold', package):
            self.assertEqual((root / 'cplfold/_internal/shared.py').read_text(), 'local edit')
            self.assertTrue((root / 'cplfold/_internal/energy_params/tracked.dat').is_file())
            self.assertFalse((root / 'cplfold/_internal/peer.py').exists())
            self.assertFalse((root / 'cplfold/_internal/energy_params/peer.dat').exists())
        self.assertTrue((package / 'cplfold_web.py').is_file())
        # Only the bridge's settings modules come from pipeline/.
        self.assertEqual(sorted(p.name for p in (package / 'hyb2analysis').iterdir()),
                         ['__init__.py', 'engine_parameters.py', 'fold_text.py', 'iris_parameters.py', 'support_kernels.py'])
        self.assertFalse((package / 'main.py').exists())
        self.assertTrue((assets / 'CPLfold-LICENSE.txt').is_file())

    def test_checkout_packages_tracked_paths_with_working_tree_contents(self):
        self.track_sources()
        self.write(self.shared, 'local edit')
        self.write('third_party/cplfold/cplfold/_internal/peer.py')
        self.write('third_party/cplfold/cplfold/_internal/energy_params/peer.dat')
        self.check_selection()

    def test_packagers_refuse_untracked_fold_dependencies(self):
        shared = 'pipeline/hyb2analysis/iris_parameters.py'
        dependencies = (shared, 'pipeline/hyb2analysis/iris.py', 'pipeline/hyb2analysis/reference.py')
        self.track_sources()
        for name in dependencies:
            with self.subTest(source=name):
                subprocess.run(['git', 'rm', '-q', '--cached', '--', name], cwd=self.repo, check=True)
                with self.assertRaisesRegex(ValueError, f'missing from the Git index: {name}'):
                    self.stage.stage_sources(self.repo, self.native)
                if name == shared:
                    with self.assertRaisesRegex(ValueError, f'missing from the Git index: {name}'):
                        self.web.stage_web_sources(self.repo, self.root / 'browser', self.root / 'assets')
                else:  # The browser uses its JS IRIS engine, not the desktop pipeline.
                    self.web.stage_web_sources(self.repo, self.root / 'browser', self.root / 'assets')
            subprocess.run(['git', 'add', '--', name], cwd=self.repo, check=True)

    def test_source_archive_needs_no_git_metadata(self):
        self.write(self.shared, 'local edit')
        self.write('third_party/cplfold/archive_helper.py', 'number = 1\n')
        self.write('web/cplfold/bridge_support/nested.py', 'number = 2\n')
        self.check_selection()
        self.assertTrue((self.root / 'browser/archive_helper.py').is_file())
        self.assertTrue((self.root / 'browser/bridge_support/nested.py').is_file())

    def test_core_update_reaches_both_packages(self):
        # Not under cplfold/: the native probe's desktop runtime has already imported the real package.
        self.write('third_party/cplfold/new_core_helper.py', 'from new_core_package.nested import value\n')
        self.write('third_party/cplfold/new_core_package/__init__.py', '')
        self.write('third_party/cplfold/new_core_package/nested.py', 'from split_support.core import value\n')
        core = 'third_party/cplfold/split_support/core.py'
        self.write('web/cplfold/cplfold_web.py',
                   'from new_core_helper import value\nfrom bridge_helper import increment\nmarker = value + increment\n')
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

        # Isolated interpreters have no import path into the original fixture.
        browser_probe = 'import sys; sys.path.insert(0, sys.argv[1]); import cplfold_web; print(cplfold_web.marker)'
        native_probe = ('import sys; from pathlib import Path; sys.path.insert(0, sys.argv[1]); '
                        'from desktop.runtime.structure import load_cplfold_web; '
                        'print(load_cplfold_web(Path(sys.argv[2])).marker)')
        package = self.root / 'browser'
        # The second commit changes just the core, not either consumer or its bridge.
        for source, expected in [('value = 70\n', '73'), ('value = 6 * 7\n', '45')]:
            with self.subTest(expected=expected):
                self.write(core, source)
                subprocess.run(['git', 'add', '--', core], cwd=self.repo, check=True)
                head = self.commit_fixture()
                self.stage.stage_sources(self.repo, self.native)
                self.assertEqual(self.stage.verify_project_sources(self.repo, self.native, self.manifest(), head), head)
                staged = self.web.stage_web_sources(self.repo, package, self.root / 'assets')
                self.assertEqual(self.web.browser_build_commit(self.repo, staged, head), head)
                for name in [*excluded, 'untracked_core.py', 'untracked_bridge.py']:
                    self.assertFalse((package / name).exists(), name)
                self.assertFalse((self.native / 'third_party/cplfold/untracked_core.py').exists())
                self.assertFalse((self.native / 'web/cplfold/untracked_bridge.py').exists())
                archive = shutil.make_archive(str(self.root / 'browser-modules'), 'zip', root_dir=package)
                for args in ([browser_probe, archive], [native_probe, str(ROOT.parent), str(self.native)]):
                    result = subprocess.run([sys.executable, '-I', '-B', '-c', *args],
                                            cwd=self.root, text=True, capture_output=True)
                    self.assertEqual((result.returncode, result.stdout.strip()), (0, expected), result.stderr)

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

    def test_browser_still_requires_the_cplfold_package(self):
        shutil.rmtree(self.repo / 'third_party/cplfold/cplfold')
        with self.assertRaisesRegex(ValueError, 'missing: third_party/cplfold/cplfold'):
            self.web.stage_web_sources(self.repo, self.root / 'browser', self.root / 'assets')

    def test_browser_ignores_symlinks_in_excluded_docs_but_rejects_package_symlinks(self):
        self.write('third_party/cplfold/README.md')
        docs_link = self.repo / 'third_party/cplfold/docs/README.md'
        docs_link.parent.mkdir(parents=True)
        docs_link.symlink_to('../README.md')
        self.track_sources()
        package, assets = self.root / 'browser', self.root / 'assets'
        self.web.stage_web_sources(self.repo, package, assets)
        self.assertFalse((package / 'docs/README.md').exists())
        head = self.commit_fixture()
        paths = self.web.web_source_layout(self.repo)
        sources = {name: self.repo / name for name in paths.keys() | self.web.NOTICE_PATHS.keys()}
        self.assertEqual(self.web.browser_build_commit(self.repo, sources, head), head)

        package_link = self.repo / 'third_party/cplfold/cplfold/_internal/package.py'
        package_link.symlink_to('../../README.md')
        subprocess.run(['git', 'add', '--', 'third_party/cplfold/cplfold/_internal/package.py'], cwd=self.repo, check=True)
        with self.assertRaisesRegex(ValueError, 'unsupported Git mode 120000.*_internal/package.py'):
            self.web.stage_web_sources(self.repo, package, assets)

    def commit_fixture(self):
        subprocess.run(['git', '-c', 'user.name=Fixture', '-c', 'user.email=test@example.invalid',
                        '-c', 'commit.gpgsign=false', 'commit', '-qm', 'fixture', '--allow-empty'],
                       cwd=self.repo, check=True)
        return subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=self.repo, text=True).strip()

    def native_tool(self, command, sha=None, error=None):
        # Exercise both CLI entry points and the real manifest. Only the external
        # interpreter/Bowtie smoke is stubbed; no fixture executable is run.
        out, err = io.StringIO(), io.StringIO()
        if command == 'stage':
            module = self.stage
            python, bowtie = self.root / 'cpython', self.root / 'bowtie'
            inputs = [python / ('python.exe' if os.name == 'nt' else 'bin/python3'),
                      *(bowtie / 'bin' / n for n in self.stage.BOWTIE_BINARIES), bowtie / 'LICENSE', bowtie / 'VERSION']
            for path in inputs:
                path.parent.mkdir(parents=True, exist_ok=True)
                path.touch()
            target = self.root / 'refused' if error else self.native
            args = ['--output', str(target), '--standalone-python', str(python), '--bowtie2', str(bowtie),
                    '--source-id', 'fixture; sha256=' + '0' * 64]
        else:
            module = self.verify
            args = ['--resource-root', str(self.native)]
        pins = self.stage.evidence_pins()
        details = {'base_prefix': 'python', 'bowtie2': True, 'viennarna': True, 'cutadapt': {'version': '5.2'}, 'evidence': {
            'mcp': pins['mcp'], 'rnacentral-mcp-server': pins['rnacentral-mcp-server'],
            'packages': len(pins), 'worker_status': 'passed'}}
        with ExitStack() as stack:
            stack.enter_context(patch.dict(os.environ))
            os.environ.pop('GITHUB_SHA', None)
            if sha is not None:
                os.environ['GITHUB_SHA'] = sha
            stack.enter_context(patch.object(sys, 'argv', [module.__file__, *args]))
            stack.enter_context(patch.object(module, 'ROOT', self.repo))
            stack.enter_context(patch.object(self.verify, 'staging', self.stage))
            smoke = stack.enter_context(patch.object(self.stage, 'smoke', return_value=details))
            stack.enter_context(redirect_stdout(out))
            stack.enter_context(redirect_stderr(err))
            if error:
                if command == 'stage':
                    with self.assertRaises(ValueError) as caught:
                        module.main()
                    self.assertIn(error, str(caught.exception))
                    self.assertFalse(target.exists())
                else:
                    self.assertEqual(module.main(), 2)
                    self.assertIn(error, err.getvalue())
                smoke.assert_not_called()
                return
            self.assertIn(module.main(), (None, 0), err.getvalue())
            smoke.assert_called_once()
        report = json.loads(out.getvalue())
        manifest = json.loads((self.native / 'native-manifest.json').read_text())
        self.assertEqual(report['project_commit'], manifest['project_commit'])
        if command == 'verify':
            self.assertIs(report['ok'], True)
        return report['project_commit']

    def reseal_native(self, revision):
        path = self.native / 'native-manifest.json'
        manifest = json.loads(path.read_text())
        manifest['files'] = self.manifest()  # self-consistent even for missing, extra or stale bytes
        if revision is None:
            manifest.pop('project_commit')
        else:
            manifest['project_commit'] = revision
        path.write_text(json.dumps(manifest))

    def test_native_release_checks_event_and_manifest_even_for_identical_sources(self):
        self.track_sources()
        head = self.commit_fixture()
        self.write('pipeline/tests/fixture.py', 'unrelated working-tree edit')
        self.write('pipeline/untracked.dat')
        self.assertEqual(self.native_tool('stage', head), head)
        self.assertEqual(self.native_tool('verify', head), head)
        for revision in ('local', None):
            self.reseal_native(revision)
            self.native_tool('verify', head, error='project_commit mismatch')
        self.reseal_native(head)
        second = self.commit_fixture()  # same project files, different commit
        self.native_tool('verify', second, error='project_commit mismatch')
        for command in ('stage', 'verify'):
            for wrong in (head, ''):
                self.native_tool(command, wrong, error='does not match checkout HEAD')

    def test_native_archive_and_edited_resource_cannot_be_promoted_to_a_release(self):
        with patch.dict(os.environ, PATH=str(self.root / 'no-git-on-path')):
            self.assertEqual(self.native_tool('stage'), 'local')
            self.assertEqual(self.native_tool('verify'), 'local')
            for command in ('stage', 'verify'):
                self.native_tool(command, 'a' * 40, error='cannot verify GITHUB_SHA without a Git HEAD')
        shutil.rmtree(self.native)
        self.track_sources()
        head = self.commit_fixture()
        name = 'third_party/cplfold/cplfold/_internal/energy_params/tracked.dat'
        self.write(name, 'locally edited resource')
        subprocess.run(['git', 'add', name], cwd=self.repo, check=True)
        self.assertEqual(self.native_tool('stage'), 'local')
        self.assertEqual((self.native / name).read_text(), 'locally edited resource')
        self.assertEqual(self.native_tool('verify'), 'local')
        self.reseal_native(head)
        self.native_tool('verify', error='project_commit mismatch')
        for command in ('stage', 'verify'):
            self.native_tool(command, head, error=f'packaged native sources differ from HEAD (changed: {name})')

    def test_native_compares_names_to_commit_not_just_current_index(self):
        self.track_sources()
        head = self.commit_fixture()
        name = 'pipeline/main.py'
        subprocess.run(['git', 'rm', '--cached', name], cwd=self.repo, check=True, capture_output=True)
        self.assertEqual(self.native_tool('stage'), 'local')
        self.reseal_native(head)
        for command in ('stage', 'verify'):
            self.native_tool(command, head, error=f'missing: {name}')
        shutil.rmtree(self.native)
        self.write('pipeline/new.txt')
        subprocess.run(['git', 'add', name, 'pipeline/new.txt'], cwd=self.repo, check=True)
        self.assertEqual(self.native_tool('stage'), 'local')
        self.reseal_native(head)
        for command in ('stage', 'verify'):
            self.native_tool(command, head, error='unexpected: pipeline/new.txt')

    def test_native_preflight_rejects_rehashed_altered_inventory(self):
        self.track_sources()
        head = self.commit_fixture()
        self.assertEqual(self.native_tool('stage'), head)
        sample = self.native / 'pipeline/main.py'
        sample.unlink()
        self.reseal_native(head)
        self.native_tool('verify', head, error='missing bundled sources: pipeline/main.py')
        shutil.copy2(self.repo / 'pipeline/main.py', sample)
        extra = self.native / 'bin/unexpected'
        extra.touch()
        self.reseal_native(head)
        self.native_tool('verify', head, error='unexpected bundled sources: bin/unexpected')
        extra.unlink()
        sample.write_text('changed only in the bundle')
        self.reseal_native(head)
        self.native_tool('verify', head, error='bundled source is stale')

    def test_windows_checkout_preserves_raw_bytes_and_filtered_diffs_are_not_enough(self):
        name = 'pipeline/main.py'
        source = self.repo / name
        source.write_bytes(b'# fixture\n')
        binary = 'web/assets/examples/fixture.bin'
        (self.repo / binary).write_bytes(b'\0\r\n\n')
        shutil.copyfile(ROOT.parent / '.gitattributes', self.repo / '.gitattributes')
        self.track_sources()
        head = self.commit_fixture()
        subprocess.run(['git', 'config', 'core.autocrlf', 'true'], cwd=self.repo, check=True)
        source.unlink()
        (self.repo / binary).unlink()
        subprocess.run(['git', 'checkout-index', '--', name, binary], cwd=self.repo, check=True)
        self.assertEqual(source.read_bytes(), b'# fixture\n')
        self.assertEqual((self.repo / binary).read_bytes(), b'\0\r\n\n')
        self.assertEqual(self.native_tool('stage', head), head)
        self.assertEqual(self.native_tool('verify', head), head)
        source.write_bytes(b'# fixture\r\n')
        subprocess.run(['git', 'diff', '--quiet', '--', name], cwd=self.repo, check=True, capture_output=True)
        self.native_tool('stage', head, error=f'changed: {name}')

    def test_symlink_placeholder_is_not_package_source_even_if_index_mode_is_relabelled(self):
        # Populate Git metadata directly: the Windows checkout can present the
        # symlink's blob (its target path) as an ordinary file, not an OS link.
        self.write(self.shared, 'actual.py')
        self.track_sources()
        oid = subprocess.check_output(['git', 'rev-parse', ':' + self.shared], cwd=self.repo, text=True).strip()
        for mode in ('120000', '100644'):
            subprocess.run(['git', 'update-index', '--cacheinfo', f'{mode},{oid},{self.shared}'], cwd=self.repo, check=True)
            if mode == '120000':
                head = self.commit_fixture()
            self.native_tool('stage', head, error='unsupported Git mode 120000')
            self.browser_tool(self.root / 'browser', self.root / 'assets', sha=head, error='unsupported Git mode 120000')

    def test_gitlink_instead_of_package_root_fails_explicitly_in_index_and_head(self):
        self.track_sources()
        first = self.commit_fixture()
        name = self.web.CPLFOLD
        subprocess.run(['git', 'rm', '-qr', '--cached', name], cwd=self.repo, check=True)
        subprocess.run(['git', 'update-index', '--add', '--cacheinfo', f'160000,{first},{name}'], cwd=self.repo, check=True)
        head = self.commit_fixture()
        for tree in ('index', 'head'):
            if tree == 'head':
                subprocess.run(['git', 'reset', '-q', first, '--', name], cwd=self.repo, check=True)
            self.native_tool('stage', head, error=f'not Git mode 160000: {name}')
            self.browser_tool(self.root / 'browser', self.root / 'assets', sha=head, error=f'not Git mode 160000: {name}')

    def browser_tool(self, *args, sha=None, error=None):
        script = self.repo / 'scripts' / 'build' / 'bundle_sources.py'
        script.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(self.web.__file__, script)
        env = os.environ.copy()
        env.pop('GITHUB_SHA', None)
        if sha:
            env['GITHUB_SHA'] = sha
        result = subprocess.run([sys.executable, '-B', script, *args], env=env, text=True, capture_output=True)
        if error is None:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(error, result.stderr)
        return result.stdout.strip()

    def seal_browser_zip(self, package, assets, commit):
        # Re-hash after each edit: a mismatch below cannot be explained by a bad ZIP digest.
        archive = Path(shutil.make_archive(str(self.root / 'fixture'), 'zip', package.parent, package.name))
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        filename = f'cplfold-python-{digest}.zip'
        archive.replace(assets / filename)
        hashes = {target: hashlib.sha256((assets / target).read_bytes()).hexdigest()
                  for target in self.web.WEB_NOTICES.values()}
        manifest = {'buildCommit': commit, 'cplfoldArchiveFile': filename, 'cplfoldArchiveSha256': digest,
                    'assets': {**hashes, filename: digest}}
        (assets / 'build-manifest.json').write_text(json.dumps(manifest), encoding='utf-8')

    def test_browser_build_and_verify_use_head_even_when_packaged_bytes_are_unchanged(self):
        generated = ['web/build-info.js', 'web/releases.js', 'third_party/cplfold/docs/example.py']
        for name in generated:
            self.write(name)
        self.track_sources()
        first = self.commit_fixture()
        for name in generated:
            self.write(name, 'unrelated edits')
        self.write('third_party/cplfold/cplfold/_internal/untracked.dat')
        package, assets = self.root / 'cplfold', self.root / 'assets'
        self.assertEqual(self.browser_tool(package, assets), first)
        self.seal_browser_zip(package, assets, first)
        self.browser_tool('--verify', assets)
        self.browser_tool('--verify', assets, sha=first)

        second = self.commit_fixture()  # same package files; only the expected commit changes
        self.browser_tool('--verify', assets, error=f'buildCommit mismatch: expected {second!r}')
        for args in ((package, assets), ('--verify', assets)):
            self.browser_tool(*args, sha=first, error='does not match checkout HEAD')

    def test_browser_verify_rejects_wrong_contents_with_self_consistent_manifests(self):
        name = 'cplfold/_internal/energy_params/tracked.dat'
        self.write('web/cplfold/bridge_package/helper.py')
        self.track_sources()
        head = self.commit_fixture()
        package, assets = self.root / 'cplfold', self.root / 'assets'
        self.assertEqual(self.browser_tool(package, assets, sha=head), head)
        (package / name).unlink()
        self.seal_browser_zip(package, assets, head)
        self.browser_tool('--verify', assets, sha=head, error=f'missing bundled sources: cplfold/{name}')
        shutil.copyfile(self.repo / 'third_party/cplfold' / name, package / name)
        (package / 'extra.py').write_text('extra')
        self.seal_browser_zip(package, assets, head)
        self.browser_tool('--verify', assets, sha=head, error='unexpected bundled sources: cplfold/extra.py')
        (package / 'extra.py').unlink()
        (package / 'bridge_package/helper.py').write_text('stale')
        self.seal_browser_zip(package, assets, head)
        self.browser_tool('--verify', assets, sha=head, error='stale bundled source: cplfold/bridge_package/helper.py')
        shutil.copyfile(self.repo / 'web/cplfold/bridge_package/helper.py', package / 'bridge_package/helper.py')
        (assets / 'CPLfold-LICENSE.txt').write_text('stale notice')
        self.seal_browser_zip(package, assets, head)
        self.browser_tool('--verify', assets, sha=head, error='stale bundled notice: CPLfold-LICENSE.txt')

    def test_browser_archive_and_working_copy_builds_are_local(self):
        package, assets = self.root / 'cplfold', self.root / 'assets'
        self.assertEqual(self.browser_tool(package, assets), 'local')  # no Git metadata
        self.seal_browser_zip(package, assets, 'local')
        self.browser_tool('--verify', assets)
        self.browser_tool('--verify', assets, sha='a' * 40, error='cannot verify GITHUB_SHA without a Git HEAD')

        self.track_sources()
        head = self.commit_fixture()
        self.write(self.shared, 'working-tree edit')
        self.assertEqual(self.browser_tool(package, assets), 'local')
        self.assertEqual((package / 'cplfold/_internal/shared.py').read_text(), 'working-tree edit')
        self.seal_browser_zip(package, assets, 'local')
        self.browser_tool('--verify', assets)
        for args in ((package, assets), ('--verify', assets)):
            self.browser_tool(*args, sha=head, error='packaged browser sources differ from HEAD')

        self.write(self.shared, 'original')
        subprocess.run(['git', 'rm', '--cached', 'third_party/cplfold/cplfold/_internal/energy_params/tracked.dat'],
                       cwd=self.repo, check=True, capture_output=True)
        self.assertEqual(self.browser_tool(package, assets), 'local')  # staged deletion, no changed file bytes

    def test_preflight_finds_missing_extra_and_changed_sources(self):
        self.track_sources()
        self.stage.stage_sources(self.repo, self.native)
        original = self.manifest()
        self.write('pipeline/added.py')
        subprocess.run(['git', 'add', 'pipeline/added.py'], cwd=self.repo, check=True)
        with self.assertRaisesRegex(ValueError, r'missing bundled sources: pipeline/added\.py'):
            self.stage.verify_project_sources(self.repo, self.native, original, None)
        shutil.copy2(self.repo / 'pipeline/added.py', self.native / 'pipeline/added.py')
        (self.native / 'pipeline/extra.py').write_text('extra')
        with self.assertRaisesRegex(ValueError, r'unexpected bundled sources: pipeline/extra\.py'):
            self.stage.verify_project_sources(self.repo, self.native, self.manifest(), None)
        (self.native / 'pipeline/extra.py').unlink()
        self.write('pipeline/added.py', 'changed after staging')
        with self.assertRaisesRegex(ValueError, r'bundled source is stale.*pipeline/added\.py'):
            self.stage.verify_project_sources(self.repo, self.native, self.manifest(), None)

    def test_removed_tracked_input_is_reported(self):
        self.track_sources()
        (self.repo / self.shared).unlink()
        with self.assertRaisesRegex(ValueError, 'package source is missing.*_internal/shared.py'):
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
