# Native runtime acquisition and release matrix

The native sidecar is built **on the target operating system and CPU**.  The
repository does not contain guessed download URLs or generated binaries.

## Acquire a verified archive

Obtain a relocatable CPython 3.11 `install_only` archive from the approved
upstream release, record its exact URL and SHA-256, then run on the matching
runner:

```bash
python3 desktop/scripts/acquire-native-runtime.py \
  --platform linux-x86_64 \
  --url "$CPYTHON_URL" \
  --sha256 "$CPYTHON_SHA256" \
  --output /tmp/cpython-runtime
```

Use `--archive path/to/archive.tar.gz` for an air-gapped build.  The command
rejects mismatched hashes, path traversal, links, and archives with multiple
top-level directories.  It does not install Python packages.

Install only the pinned wheels with the acquired interpreter, then stage and
verify on the same runner:

```bash
/tmp/cpython-runtime/bin/python3 -m pip install --no-index --only-binary=:all: \
  --find-links "$WHEELHOUSE" -r desktop/runtime/requirements-native.txt
python3 desktop/scripts/stage-native.py \
  --standalone-python /tmp/cpython-runtime \
  --source-id "$CPYTHON_URL; sha256=$CPYTHON_SHA256" \
  --output desktop/resources/native
python3 desktop/scripts/verify-native-release.py
```

Windows uses `python/python.exe` and `--platform windows-x86_64`; Linux and
macOS use the corresponding Unix platform identifier.  Never run `stage-native`
with a runtime downloaded for another OS or architecture: it executes a real
Numba and CPLfold smoke test and records the target platform in the manifest.

## Required release matrix

| target | runner | platform argument |
|---|---|---|
| Windows 11 x64 | `windows-2022` | `windows-x86_64` |
| Ubuntu x64 | `ubuntu-22.04` | `linux-x86_64` |
| macOS Intel | `macos-13` | `macos-x86_64` |
| macOS Apple Silicon | `macos-14` | `macos-aarch64` |

Each job must publish the staged `native-manifest.json` and the archive SHA256
as build artifacts.  A missing URL/SHA or an unavailable target wheel is a
failed release, never a reason to fall back to a host virtualenv.
