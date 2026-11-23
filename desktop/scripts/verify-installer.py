#!/usr/bin/env python3
"""Verify the runtime delivered by one final MyGo installer on its target runner."""
import argparse
from contextlib import contextmanager
import hashlib
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[2]
ARCHIVE = 'native.tar.gz'


@contextmanager
def resources(package, work):
    """Open only this distribution, never a sibling build/ or staging directory."""
    dest = work / 'installed'
    dest.mkdir()
    if package.suffix == '.dmg':
        subprocess.run(['hdiutil', 'attach', '-readonly', '-nobrowse', '-mountpoint', dest, package], check=True)
        try:
            app = dest / 'HYB2 Desktop.app/Contents'
            yield app / 'Resources', app / 'MacOS/HYB2 Desktop'
        finally:
            subprocess.run(['hdiutil', 'detach', dest], check=True)
    elif package.name.endswith('-Setup.exe') and os.name == 'nt':
        # NSIS takes the rest of the raw command line after /D= (no quotes).
        subprocess.run(f'"{package}" /S /D={dest}', check=True, timeout=600)
        yield dest, dest / 'HYB2 Desktop.exe'
    elif package.suffix == '.deb':
        subprocess.run(['dpkg-deb', '--extract', package, dest], check=True)
        app = dest / 'opt/hyb2-desktop'
        yield app, app / 'hyb2-desktop'
    elif package.name.endswith('.tar.gz') and package.name != ARCHIVE:
        with tarfile.open(package, 'r:gz') as outer:
            outer.extractall(dest, filter='data')
        yield dest, dest / 'hyb2-desktop'
    else:
        raise ValueError(f'unsupported installer for this runner: {package}')


def check_package(package, verify):
    with tempfile.TemporaryDirectory(prefix='hyb2-installer-') as temp:
        work = Path(temp).resolve()
        with resources(package.resolve(strict=True), work) as (app, executable):
            archive = app / ARCHIVE
            checksum = app / (ARCHIVE + '.sha256')
            for path in (executable, archive, checksum):
                if path.resolve() != path or not path.is_file() or not path.stat().st_size:
                    raise ValueError(f'{package.name}: missing or linked package member: {path.name}')
            if not os.access(executable, os.X_OK):
                raise ValueError(f'{package.name}: application is not executable')
            with archive.open('rb') as handle:
                digest = hashlib.file_digest(handle, 'sha256').hexdigest()
            if checksum.read_text().split() != [digest, ARCHIVE]:
                raise ValueError(f'{package.name}: incorrect packaged runtime checksum')
            stage = work / 'unpacked'
            with tarfile.open(archive, 'r:gz') as runtime:
                runtime.extractall(stage, filter='data')
            verify(app, stage)
            print(f'{package.name}: verified packaged runtime {digest}', flush=True)


def preflight(app, stage):
    subprocess.run([sys.executable, '-B', ROOT / 'desktop/scripts/verify-native-release.py',
                    '--resource-root', stage], check=True)
    subprocess.run(['go', 'test', '-count=1', '-run', '^TestPackedRuntime$', '-v', '.'],
                   cwd=ROOT / 'desktop/app', env=dict(os.environ, HYB2_PACKED_RUNTIME=str(app)), check=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('package', type=Path, nargs='+')
    for package in parser.parse_args().package:
        check_package(package, preflight)
