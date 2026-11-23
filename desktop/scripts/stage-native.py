#!/usr/bin/env python3
"""Stage a native runtime; never silently convert a host venv into a portable app."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
from bundle_sources import CPLFOLD_SOURCE_DIRS, REQUIRED_FOLD_SOURCES, package_build_commit, selected_files

# Repository sources copied into every runtime (verify-native-release checks they are current).
# web/assets/examples is the example data set (desktop/runtime/example.py).
SOURCE_DIRS = ['desktop/runtime', 'desktop/task-runner', 'desktop/spec', *CPLFOLD_SOURCE_DIRS, 'pipeline',
               'web/assets/examples', 'third_party/iris']
SOURCE_FILES = ['bin/cplfold', *REQUIRED_FOLD_SOURCES, 'pipeline/hyb2analysis/iris.py', 'pipeline/hyb2analysis/reference.py']
BOWTIE_BINARIES = [name + ('.exe' if os.name == 'nt' else '') for name in ('bowtie2-align-s', 'bowtie2-build-s')]
BOWTIE_FILES = {'third_party/bowtie2/LICENSE', 'third_party/bowtie2/VERSION', *('bin/' + n for n in BOWTIE_BINARIES)}
PINS = {"numpy": "1.24.4", "numba": "0.58.1", "llvmlite": "0.41.1"}
PROBE = '''import json,sys,platform,numpy,numba,llvmlite
@numba.njit
def total(x):
    return x.sum()
assert total(numpy.arange(10)) == 45
assert total.nopython_signatures
print(json.dumps({"python":platform.python_version(),"base_prefix":sys.base_prefix,"numpy":numpy.__version__,"numba":numba.__version__,"llvmlite":llvmlite.__version__,"numba_jit":True,"module_paths":[numpy.__file__,numba.__file__,llvmlite.__file__]}))
'''


def source_files(repository):
    return selected_files(repository, SOURCE_DIRS, SOURCE_FILES)


def is_project_source(name):
    # Only the acquired interpreter/wheels and Bowtie2 are external inputs.
    # A stray file outside the normal source directories must not evade verification.
    return not name.startswith('python/') and name not in BOWTIE_FILES


def stage_sources(repository, stage):
    names = source_files(repository)
    for name in names:
        destination = stage / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(repository / name, destination)
    return names


def native_build_commit(repository, stage, names, requested_sha):
    return package_build_commit(repository, {name: stage / name for name in names}, requested_sha,
                                directories=SOURCE_DIRS, required_files=SOURCE_FILES, select=set, package='native')


def verify_project_sources(repository, stage, files, requested_sha):
    """Check final inventory and bytes against the checkout; independently derive the revision from staged bytes."""
    expected = set(source_files(repository))
    bundled = {name for name in files if is_project_source(name)}
    if expected != bundled:
        issues = []
        if expected - bundled:
            issues.append('missing bundled sources: ' + ', '.join(sorted(expected - bundled)))
        if bundled - expected:
            issues.append('unexpected bundled sources: ' + ', '.join(sorted(bundled - expected)))
        raise ValueError('\n'.join(issues))
    for name in sorted(expected):
        if hashlib.sha256((repository / name).read_bytes()).hexdigest() != files[name]:
            raise ValueError(f'bundled source is stale (re-run stage-native.py): {name}')
    return native_build_commit(repository, stage, sorted(bundled), requested_sha)


def run(args, cwd, env):
    return subprocess.run(args, cwd=cwd, env=env, text=True, capture_output=True,
                          check=True, timeout=300).stdout


def inside(root, path):
    """``path`` relative to the runtime root, in POSIX form."""
    return Path(path).resolve().relative_to(Path(root).resolve()).as_posix()


RELOCATABLE_SHEBANG = (b'#!/bin/sh\n'
                       b"'''exec' \"$(dirname -- \"$(realpath -- \"$0\")\")/python3\" \"$0\" \"$@\"\n"
                       b"' '''\n")


def host_shebang(path):
    """The interpreter a script's first line names by absolute path, or None.

    ``/bin/sh`` and ``/usr/bin/env`` exist on every POSIX target; any other
    absolute interpreter is a file on the machine that built the runtime.
    """
    with open(path, 'rb') as handle:
        first = handle.readline(4096)
    if not first.startswith(b'#!'):
        return None
    words = first[2:].split()
    interpreter = words[0].decode('utf-8', 'replace') if words else ''
    return None if interpreter in ('/bin/sh', '/usr/bin/env') else interpreter


def relocate_scripts(python_root):
    """Point pip-installed console scripts at the interpreter beside them.

    pip writes the installing interpreter's absolute path into each script, so
    the scripts stop working once the runtime moves and they carry the build
    machine's path into the archive. The replacement is the form the
    standalone CPython distribution uses for its own scripts.
    """
    changed = []
    directory = Path(python_root) / 'bin'
    for script in sorted(directory.iterdir()) if directory.is_dir() else []:
        if script.is_symlink() or not script.is_file() or host_shebang(script) is None:
            continue
        body = script.read_bytes()
        script.write_bytes(RELOCATABLE_SHEBANG + body[body.index(b'\n') + 1:])
        changed.append(script.name)
    return changed


def smoke(root, executable, portable):
    env = {k: v for k, v in os.environ.items()
           if not k.startswith(('PYTHON', 'NUMBA')) and k not in ('VIRTUAL_ENV',)}
    env['PYTHONDONTWRITEBYTECODE'] = '1'
    with tempfile.TemporaryDirectory(prefix='hyb2-native-smoke-') as cache:
        env['NUMBA_CACHE_DIR'] = cache
        # -B is required in addition to PYTHONDONTWRITEBYTECODE because -I
        # isolates user environment variables on some Python builds.
        details = json.loads(run([str(executable), '-I', '-B', '-c', PROBE], root, env))
        if portable and not details['python'].startswith('3.11.'):
            raise ValueError('Portable runtime must use CPython 3.11.x for these dependency pins')
        for package, version in PINS.items():
            if details[package] != version:
                raise ValueError(f'{package}: expected {version}, got {details[package]}')
        if portable:
            for path in [details['base_prefix'], *details['module_paths']]:
                if not Path(path).resolve().is_relative_to(root.resolve()):
                    raise ValueError(f'Runtime depends on host files: {path}')
            # Record where the modules sit inside the runtime, not where the
            # runtime sat on the build machine.
            details['base_prefix'] = inside(root, details['base_prefix'])
            details['module_paths'] = [inside(root, path) for path in details['module_paths']]
        output = run([str(executable), '-B', str(root / 'bin/cplfold'), '--sequence',
                      'GGCGCGGCACCGUCCGCGGAACAAACGG', '--beam', '20',
                      '--max-phase1', '3', '--max-phase2', '2'], root, env)
        if '..(((((..[[[[)))))......]]]]' not in output or 'Energy: -8.02 kcal/mol' not in output:
            raise ValueError('CPLfold native smoke did not match reference structure/energy:\n' + output)
        details['cplfold_reference_fold'] = 'passed'
        # The CLI does not import the app's Python bridge. Exercise it from
        # the staged directory so omitted dependencies and old Gaussians fail.
        probe = '''import sys
from pathlib import Path
sys.path.insert(0, str(Path.cwd()))
from desktop.runtime.structure import load_cplfold_web
from pipeline.hyb2analysis.iris import support_matrix
blocks = [(1, 9, 23, 31)] * 2 + [(7, 12, 27, 34)]
fields = ("oneStart", "oneEnd", "twoStart", "twoEnd")
iris = support_matrix(40, [dict(zip(fields, b)) for b in blocks])
bonus, _ = load_cplfold_web(Path.cwd()).build_hyb_bonus_matrix_fast(40, blocks)
if not (2**-23 < iris[0, 20] < 1e-6 and abs(iris - bonus).max() < 1e-12):
    raise ValueError("packaged IRIS and CPLfold Gaussian support mismatch")
'''
        run([str(executable), '-I', '-B', '-c', probe], root, env)
        details['gaussian_parity'] = 'passed'
        details['bowtie2'] = bowtie2_smoke(root, cache)
        details['viennarna'] = viennarna_smoke(root, executable, env)
        details['evidence'] = evidence_smoke(root, executable, env, portable)
    return details


VIENNA_PROBE = '''import json, RNA
structure, mfe = RNA.fold_compound("GGGGAAAACCCC&GGGGUUUUCCCC").mfe_dimer()
print(json.dumps({"version": RNA.__version__, "structure": structure, "mfe": round(mfe, 2)}))
'''


def viennarna_smoke(root, executable, env):
    """Folding energy for hyb2pipe -e 1 comes from the ViennaRNA wheel."""
    try:
        details = json.loads(run([str(executable), '-I', '-B', '-c', VIENNA_PROBE], root, env))
    except subprocess.CalledProcessError:
        return None
    if details['version'] != '2.7.2' or details['mfe'] >= 0:
        raise ValueError('ViennaRNA smoke failed: %s' % details)
    return details


EVIDENCE_REQUIREMENTS = ROOT / 'desktop/runtime/requirements-evidence.txt'
EVIDENCE_PROBE = r'''import importlib.metadata, json, sys
expected = json.loads(sys.argv[1])
try:
    import mcp, rnacentral_sequence_search
    from rnacentral_sequence_search import server
except ImportError as error:
    print(json.dumps({"missing": str(error)}))
    raise SystemExit(0)
found, wrong = {}, {}
for name, version in expected.items():
    try:
        found[name] = importlib.metadata.version(name)
    except importlib.metadata.PackageNotFoundError:
        found[name] = None
    if found[name] != version:
        wrong[name] = found[name]
print(json.dumps({"mcp": found.get("mcp"), "rnacentral-mcp-server": found.get("rnacentral-mcp-server"),
                  "packages": len(found), "wrong": wrong,
                  "module_paths": [mcp.__file__, rnacentral_sequence_search.__file__]}))
'''


def evidence_pins(platform_name=None):
    """Exact pins of requirements-evidence.txt that apply to this platform."""
    platform_name = platform_name or sys.platform
    pins = {}
    for line in EVIDENCE_REQUIREMENTS.read_text(encoding='utf-8').splitlines():
        line = line.split('#', 1)[0].strip().rstrip('\\').strip()
        if not line or line.startswith('--'):
            continue
        requirement, _, marker = line.partition(';')
        name, _, version = requirement.strip().partition('==')
        marker = marker.strip()
        if marker == 'sys_platform == "win32"' and platform_name != 'win32':
            continue
        if marker and marker != 'sys_platform == "win32"':
            raise ValueError(f'unsupported marker in requirements-evidence.txt: {marker}')
        pins[name.strip().lower()] = version.strip()
    return pins


def evidence_smoke(root, executable, env, portable):
    """RNAcentral MCP client stack: exact pins, then a real stdio session.

    Returns None when the evidence packages are absent (verify-native-release
    rejects that); raises when they are present but wrong.  The session check
    starts the fixed RNAcentral server through the evidence worker and lists
    its tools; it makes no network request.
    """
    details = json.loads(run([str(executable), '-I', '-B', '-c', EVIDENCE_PROBE, json.dumps(evidence_pins())], root, env))
    if 'missing' in details:
        return None
    if details['wrong']:
        raise ValueError('Evidence packages do not match requirements-evidence.txt: %s' % details['wrong'])
    if portable:
        for path in details['module_paths']:
            if not Path(path).resolve().is_relative_to(root.resolve()):
                raise ValueError(f'Evidence runtime depends on host files: {path}')
    requests = ''.join(json.dumps({'id': i, 'request': r}) + '\n' for i, r in
                       enumerate(({'operation': 'status', 'connect': True}, {'operation': 'shutdown'}), 1))
    worker_env = {k: v for k, v in env.items() if k in ('PATH', 'HOME', 'SYSTEMROOT', 'TEMP', 'TMP')}
    worker_env.update(PYTHONDONTWRITEBYTECODE='1', PYTHONUNBUFFERED='1', PYTHONIOENCODING='utf-8')
    completed = subprocess.run([str(executable), '-B', '-m', 'desktop.runtime.evidence.worker', '--serve'],
                               cwd=root, env=worker_env, input=requests, text=True, capture_output=True,
                               check=True, timeout=120)
    status = json.loads(completed.stdout.splitlines()[0])['response']
    if not status.get('ok') or status.get('tools') != ['get_rna_description', 'map_rna_id']:
        raise ValueError('Evidence worker smoke failed: %s' % status)
    details.pop('module_paths')
    details.pop('wrong')
    details['worker_status'] = 'passed'
    details['tools'] = status['tools']
    return details


def bowtie2_smoke(root, scratch):
    """Build an index and map one read with the staged -s binaries."""
    suffix = '.exe' if os.name == 'nt' else ''
    align, build = root / 'bin' / ('bowtie2-align-s' + suffix), root / 'bin' / ('bowtie2-build-s' + suffix)
    if not align.is_file() or not build.is_file():
        return None
    reference = 'GGCGCGGCACCGUCCGCGGAACAAACGGAUCGAUCGGAUCCGAUGCUAGCUAGGCUAGCGAUCGAUCGAUGCAUGC'.replace('U', 'T')
    work = Path(scratch)
    # LF bytes: text mode writes CRLF on Windows, which Bowtie2 rejects as reads.
    (work / 'ref.fa').write_bytes(('>ref\n' + reference * 3 + '\n').encode())
    (work / 'read.fa').write_bytes(('>r1\n' + reference[10:60] + '\n').encode())
    subprocess.run([str(build), str(work / 'ref.fa'), str(work / 'idx')], capture_output=True, check=True, timeout=300)
    sam = subprocess.run([str(align), '--local', '-x', str(work / 'idx'), '-f', str(work / 'read.fa')],
                         capture_output=True, text=True, check=True, timeout=300).stdout
    if not any(line.startswith('r1\t') and line.split('\t')[2] == 'ref' for line in sam.splitlines()):
        raise ValueError('Bowtie2 smoke did not align the probe read:\n' + sam)
    version = subprocess.run([str(align), '--version'], capture_output=True, text=True, check=True, timeout=60).stdout.split('\n')[0].split()[-1]
    return {'version': version, 'align': 'passed'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--standalone-python', type=Path,
                        help='Extracted relocatable CPython 3.11 directory, with pinned dependencies installed')
    source.add_argument('--development-python', type=Path,
                        help='Host interpreter: produces nonportable development resources only')
    parser.add_argument('--output', type=Path, default=ROOT / 'desktop/resources/native')
    parser.add_argument('--source-id', help='Standalone source URL/version and SHA256 for provenance')
    parser.add_argument('--bowtie2', type=Path, help='Output of acquire-bowtie2.py for this platform')
    args = parser.parse_args()
    output = args.output.absolute()
    if output.exists():
        parser.error(f'Output already exists; choose an empty destination: {output}')
    portable = args.standalone_python is not None
    if portable and not args.source_id:
        parser.error('--source-id is required for portable runtime provenance')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='native-stage-', dir=output.parent) as temp:
        stage = Path(temp) / 'native'
        stage.mkdir()
        relative_python = Path('python/python.exe' if os.name == 'nt' else 'python/bin/python3')
        interpreter = stage / relative_python
        names = stage_sources(ROOT, stage)
        requested_sha = os.environ.get('GITHUB_SHA')
        if requested_sha is not None:
            # Do not copy a large interpreter or execute project code from a mismatched checkout.
            native_build_commit(ROOT, stage, names, requested_sha)
        if portable:
            source_dir = args.standalone_python.resolve()
            if (source_dir / 'pyvenv.cfg').exists():
                parser.error('Virtual environments are not standalone CPython distributions')
            shutil.copytree(source_dir, stage / 'python', symlinks=True)
            for entry in (stage / 'python').rglob('*'):
                if entry.is_symlink() and not entry.resolve().is_relative_to((stage / 'python').resolve()):
                    raise ValueError(f'Standalone Python has external symlink: {entry}')
            if os.name != 'nt':
                relocate_scripts(stage / 'python')
        else:
            interpreter.parent.mkdir(parents=True)
            if os.name == 'nt':
                raise ValueError('Development staging on Windows requires standalone mode')
            import shlex
            interpreter.write_text('#!/bin/sh\nexec ' + shlex.quote(str(args.development_python.absolute())) + ' \"$@\"\n')
            interpreter.chmod(0o755)
        if args.bowtie2:
            for name in BOWTIE_BINARIES:
                shutil.copy2(args.bowtie2 / 'bin' / name, stage / 'bin' / name)
            (stage / 'third_party/bowtie2').mkdir(parents=True)
            for name in ('LICENSE', 'VERSION'):
                shutil.copy2(args.bowtie2 / name, stage / 'third_party/bowtie2' / name)
        details = smoke(stage, interpreter, portable)
        # Never ship interpreter caches generated by smoke checks. Besides
        # wasting space, they make the manifest machine- and version-specific.
        for cache in stage.rglob('__pycache__'):
            shutil.rmtree(cache)
        for cache in stage.rglob('*.pyc'):
            cache.unlink()
        manifest = {'manifest_version': '1.0', 'portable': portable,
                    'mode': 'standalone' if portable else 'development-external-python',
                    'platform': platform.system(), 'architecture': platform.machine(),
                    'python_executable': relative_python.as_posix(),
                    'source_id': args.source_id if portable else str(args.development_python.absolute()),
                    'checks': details, 'files': {}}
        for file in sorted(stage.rglob('*')):
            if file.is_symlink() and not file.relative_to(stage).as_posix().startswith('python/'):
                raise ValueError(f'unexpected symlink outside the staged Python: {file}')
            if file.is_file() and not file.is_symlink():
                manifest['files'][file.relative_to(stage).as_posix()] = hashlib.sha256(file.read_bytes()).hexdigest()
        manifest['project_commit'] = verify_project_sources(ROOT, stage, manifest['files'], requested_sha)
        (stage / 'native-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        stage.rename(output)
    print(json.dumps({'output': str(output), 'portable': portable, 'project_commit': manifest['project_commit'],
                      'checks': details}, indent=2))


if __name__ == '__main__':
    try:
        main()
    except subprocess.CalledProcessError as error:
        print(error.stdout, file=sys.stderr)
        print(error.stderr, file=sys.stderr)
        raise SystemExit(error.returncode)
    except (OSError, ValueError) as error:
        raise SystemExit(f'native staging failed: {error}')
