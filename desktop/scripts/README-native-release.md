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
/tmp/cpython-runtime/bin/python3 -m pip install --no-index --only-binary=:all: --require-hashes \
  --find-links "$WHEELHOUSE" -r desktop/runtime/requirements-native.txt
/tmp/cpython-runtime/bin/python3 -m pip install --only-binary=:all: --require-hashes \
  -r desktop/runtime/requirements-evidence.txt
python3 desktop/scripts/acquire-bowtie2.py --platform linux-x86_64 --output /tmp/bowtie2
python3 desktop/scripts/stage-native.py \
  --standalone-python /tmp/cpython-runtime \
  --source-id "$CPYTHON_URL; sha256=$CPYTHON_SHA256" \
  --bowtie2 /tmp/bowtie2 \
  --output desktop/resources/native
python3 desktop/scripts/verify-native-release.py
```

Windows uses `python/python.exe` and `--platform windows-x86_64`; Linux and
macOS use the corresponding Unix platform identifier.  Never run `stage-native`
with a runtime downloaded for another OS or architecture: it executes a real
Numba and CPLfold smoke test and records the target platform in the manifest.

## Which project revision is in the runtime?

`source_id` identifies the CPython archive. `project_commit` in
`native-manifest.json` identifies the **packaged project files**, not CPython,
the wheels or Bowtie2. In a release job, leave the event's `GITHUB_SHA` set
for both staging and preflight. Each command requires it to equal checkout
`HEAD` and requires the staged project file names and raw bytes to match that
commit. Preflight also checks the manifest's recorded revision, its file
inventory and SHA-256 hashes, and the current project sources. Re-stage an
old runtime without `project_commit`; do not add a SHA by hand.

The native and browser builds take paths from the Git index and bytes from
the working tree. They support ordinary Git files, not Git symlinks or
submodules in the source inputs. `.gitattributes` preserves the required
checkout bytes on Windows; release checks do not normalize line endings.
For local builds, leave `GITHUB_SHA` **unset**. A clean set of packaged files
gets the full `HEAD` commit; edits, staged additions or staged deletions get
`local`. Changes outside those inputs don't affect the label. Use `git add`
to include a new input. An extracted source archive with no Git metadata
uses the same directory and cache exclusions and is always labeled `local`.

## Required release matrix

| target | runner | platform argument |
|---|---|---|
| Windows 11 x64 | `windows-2022` | `windows-x86_64` |
| Ubuntu x64 | `ubuntu-22.04` | `linux-x86_64` |
| macOS Intel (tooling checks) | `macos-15-intel` | `macos-x86_64` |
| macOS Apple Silicon | `macos-15` | `macos-aarch64` |

Windows x64, Linux x64 and macOS Apple Silicon build and unpack the desktop
application. Each packaged runtime contains its source manifest and ships
with its archive SHA-256. The Pages deployment and the manual release gate
reuse these package checks. Missing pinned archives or wheels fail the package
job even when distribution is disabled.

The tag workflow builds and checks the versioned installers on the runners.
It uploads them only if the repository Actions variable
`CPLFOLD_DISTRIBUTION_APPROVED` is `true`. Creating the GitHub Release also
requires a matching version tag on a commit on `main` and passing browser
and desktop checks. The manual `upload_debug_build` option requires the same
approval: that artifact contains the full bundled runtime too.

## Bowtie2

`acquire-bowtie2.py` downloads the pinned Bowtie2 2.5.5 release for the
target and checks it against the SHA-256 that GitHub publishes for that
release. It keeps only `bowtie2-align-s` and `bowtie2-build-s` (plus `.exe`
on Windows) and the GPL-3.0 `LICENSE`. These binaries run without the Perl
and Python wrappers, so FASTQ mapping works on Windows with no extra
installs.

`stage-native.py --bowtie2 DIR` copies them to `bin/` and checks them by
building an index and aligning a probe read. `verify-native-release.py`
requires Bowtie2 and repeats that check.

Bowtie2 publishes no macOS x86_64 build. An Intel Mac release needs a
self-built binary, or that target ships without FASTQ mapping (SAM input
still works).

## ViennaRNA

`requirements-native.txt` pins `ViennaRNA==2.7.2` next to NumPy, Numba and
llvmlite, each with the SHA-256 of its CPython 3.11 wheel for macOS arm64,
Windows amd64 and Linux x86_64 (installed with `--require-hashes`).
It is used for folding energy (`pipeline -e 1`), and binary wheels exist for
every target. `stage-native.py` checks the import with a dimer-fold smoke
test, and `verify-native-release.py` fails if ViennaRNA is missing.

## cutadapt

The pipeline trims the 3' adapter with cutadapt before collapsing reads
(`hyb2pipe --adapter`, gk-dev's mapping defaults). `requirements-native.txt`
pins `cutadapt==5.2` with `dnaio`, `xopen` and `backports.zstd`, plus `isal`
and `zlib-ng` on x86_64/AMD64 (xopen's markers), each with the SHA-256 of its
CPython 3.11 wheel for the three targets. The pipeline runs it as
`python -m cutadapt`. `stage-native.py` trims a test read, and
`verify-native-release.py` fails if cutadapt 5.2 is missing.

## Evidence worker (RNAcentral MCP)

`requirements-evidence.txt` locks the evidence worker's stack
(`desktop/runtime/evidence/`, contract `desktop/spec/EVIDENCE.md`): the MCP
Python SDK v1 (`mcp[cli]==1.30.0`), `rnacentral-mcp-server==0.1.1` and every
transitive dependency, 46 exact pins (`colorama` and `pywin32` only on
Windows). Each line carries the SHA-256 of every wheel pip selects for
CPython 3.11 on macOS arm64 (macOS 11 to 26 tags), Windows amd64 and Linux
x86_64 (manylinux 2.17 to 2.35), so `--require-hashes` installs succeed on all
three targets. pip and setuptools are not part of the lock. On Linux, pip on
`ubuntu-22.04` picks the `manylinux_2_34` cryptography wheel, so the shipped
runtime then needs glibc 2.34 or newer.

`stage-native.py` checks every applicable pin with `importlib.metadata`,
confirms the modules come from the staged tree, and runs the worker once
(`status` with `connect: true`, then `shutdown`): this starts the fixed
RNAcentral server and lists its tools, without a network request.
`verify-native-release.py` fails when the packages are missing or off-pin.

To change a pin: resolve the new set in a scratch venv made from the bundled
CPython, then for each target run
`pip download --only-binary=:all: --platform <tag> --python-version 3.11
--implementation cp --abi cp311 --abi abi3 --abi none -r <pins> -d <dir>`
(macOS `macosx_11_0_arm64`, `macosx_14_0_arm64` and `macosx_26_0_arm64`;
`win_amd64`; Linux `manylinux_2_17_x86_64` plus `manylinux2014_x86_64`, and
again with every `manylinux_2_NN_x86_64` from 2_35 down). Add the
Windows-only dependencies with `--no-deps`, because pip evaluates environment
markers against the host. Write each wheel's SHA-256 into the file, then
check it with `pip download --require-hashes` for each tag set and
`pip install --require-hashes --target <scratch>` on the host.
