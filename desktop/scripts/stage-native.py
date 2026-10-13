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
PINS = {"numpy": "1.24.4", "numba": "0.58.1", "llvmlite": "0.41.1"}
PROBE = '''import json,sys,platform,numpy,numba,llvmlite
@numba.njit
def total(x):
    return x.sum()
assert total(numpy.arange(10)) == 45
assert total.nopython_signatures
print(json.dumps({"python":platform.python_version(),"base_prefix":sys.base_prefix,"numpy":numpy.__version__,"numba":numba.__version__,"llvmlite":llvmlite.__version__,"numba_jit":True,"module_paths":[numpy.__file__,numba.__file__,llvmlite.__file__]}))
'''


def run(args, cwd, env):
    return subprocess.run(args, cwd=cwd, env=env, text=True, capture_output=True,
                          check=True, timeout=300).stdout


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
        output = run([str(executable), '-B', str(root / 'bin/cplfold'), '--sequence',
                      'GGCGCGGCACCGUCCGCGGAACAAACGG', '--beam', '20',
                      '--max-phase1', '3', '--max-phase2', '2'], root, env)
        if '..(((((..[[[[)))))......]]]]' not in output or 'Energy: -8.02 kcal/mol' not in output:
            raise ValueError('CPLfold native smoke did not match reference structure/energy:\n' + output)
        details['cplfold_reference_fold'] = 'passed'
    return details


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--standalone-python', type=Path,
                        help='Extracted relocatable CPython 3.11 directory, with pinned dependencies installed')
    source.add_argument('--development-python', type=Path,
                        help='Host interpreter: produces nonportable development resources only')
    parser.add_argument('--output', type=Path, default=ROOT / 'desktop/resources/native')
    parser.add_argument('--source-id', help='Standalone source URL/version and SHA256 for provenance')
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
        if portable:
            source_dir = args.standalone_python.resolve()
            if (source_dir / 'pyvenv.cfg').exists():
                parser.error('Virtual environments are not standalone CPython distributions')
            shutil.copytree(source_dir, stage / 'python', symlinks=True)
            for entry in (stage / 'python').rglob('*'):
                if entry.is_symlink() and not entry.resolve().is_relative_to((stage / 'python').resolve()):
                    raise ValueError(f'Standalone Python has external symlink: {entry}')
        else:
            interpreter.parent.mkdir(parents=True)
            if os.name == 'nt':
                raise ValueError('Development staging on Windows requires standalone mode')
            import shlex
            interpreter.write_text('#!/bin/sh\nexec ' + shlex.quote(str(args.development_python.absolute())) + ' \"$@\"\n')
            interpreter.chmod(0o755)
        for relative in ['desktop/runtime', 'desktop/task-runner', 'desktop/spec', 'third_party/cplfold', 'web/cplfold']:
            shutil.copytree(ROOT / relative, stage / relative,
                            ignore=shutil.ignore_patterns('__pycache__', '*.pyc', '*.nbc', '*.nbi', 'tests', 'data'))
        (stage / 'bin').mkdir()
        shutil.copy2(ROOT / 'bin/cplfold', stage / 'bin/cplfold')
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
            if file.is_file() and not file.is_symlink():
                manifest['files'][file.relative_to(stage).as_posix()] = hashlib.sha256(file.read_bytes()).hexdigest()
        (stage / 'native-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        stage.rename(output)
    print(json.dumps({'output': str(output), 'portable': portable, 'checks': details}, indent=2))


if __name__ == '__main__':
    try:
        main()
    except subprocess.CalledProcessError as error:
        print(error.stdout, file=sys.stderr)
        print(error.stderr, file=sys.stderr)
        raise SystemExit(error.returncode)
