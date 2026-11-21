#!/usr/bin/env bash

# Build ViennaRNA's own Python module (import RNA) for the browser CPLfold
# runtime, as a Pyodide 0.29.4 wheel (versions pinned in
# scripts/pyodide-toolchain.env):
#
#   .build-tools/viennarna-pyodide/viennarna-2.7.2-cp313-cp313-pyemscripten_2025_0_wasm32.whl
#
# (.build-tools is HYB2_BUILD_TOOLS when set; HYB2_VIENNA_PYODIDE_OUTPUT
# overrides the directory).  The wheel is a build product, not a vendored
# file: build-cplfold-web.sh reads it from the same place
# (HYB2_VIENNA_PYODIDE_WHEEL overrides the path).
#
# RNAlib is built as PIC static libraries with Pyodide's Emscripten (4.0.9,
# patched by pyodide-build), the SWIG wrapper shipped in the ViennaRNA source
# (interfaces/Python/RNA_wrap.cpp, so SWIG is not needed) is compiled
# against Pyodide's Python 3.13 headers, and both are linked into the side
# module RNA/_RNA.cpython-313-wasm32-emscripten.so.  OpenMP and pthreads are
# off (Pyodide has no threads); SVM z-scores and naview are on, as the
# wrapper needs them.  build-cplfold-web.sh adds the wheel to the Pyodide
# lock file, so the browser loads it with pyodide.loadPackage("viennarna").
#
# Toolchain, under .build-tools/ (HYB2_BUILD_TOOLS; git ignores it):
#   - Python 3.13 is not downloaded: set HYB2_PYTHON313, or put python3.13 on
#     PATH, or install one at .build-tools/python313/bin/python3
#   - downloaded on first use: pyodide-build and every dependency, pinned
#     with hashes in scripts/pyodide-build-requirements.txt, into
#     .build-tools/pyodide-venv (rebuilt when that file changes), and the
#     Pyodide cross-build environment and its Emscripten SDK into
#     .build-tools/pyodide-xbuildenv
#
# HYB2_VIENNA_TARBALL may name a local copy of the ViennaRNA source tarball
# (it must exist); when it is unset the release is downloaded into the
# temporary build directory.  Its SHA-256 is checked either way.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly ROOT
# shellcheck source=pyodide-toolchain.env
source "${ROOT}/scripts/pyodide-toolchain.env"
if [[ -z "${PYODIDE_VERSION:-}" || -z "${PYODIDE_EMSCRIPTEN_VERSION:-}" ]]; then
  printf 'PYODIDE_VERSION and PYODIDE_EMSCRIPTEN_VERSION must be set in %s\n' "${ROOT}/scripts/pyodide-toolchain.env" >&2
  exit 1
fi
readonly PYODIDE_VERSION PYODIDE_EMSCRIPTEN_VERSION
readonly VIENNA_VERSION="2.7.2"
readonly VIENNA_TARBALL="ViennaRNA-${VIENNA_VERSION}.tar.gz"
readonly VIENNA_SOURCE_URL="https://github.com/ViennaRNA/ViennaRNA/releases/download/v${VIENNA_VERSION}/${VIENNA_TARBALL}"
readonly VIENNA_SHA256="4cb7aaca00dfac92cb36b030c9111487c170b90568759bd4ffedd8318cc9384f"
readonly REQUIREMENTS="${ROOT}/scripts/pyodide-build-requirements.txt"
# the pyodide-build version comes from the requirements file
PYODIDE_BUILD_VERSION="$(sed -n 's/^pyodide-build==\([^ ]*\).*/\1/p' "${REQUIREMENTS}")"
readonly PYODIDE_BUILD_VERSION
if [[ -z "${PYODIDE_BUILD_VERSION}" ]]; then
  printf 'No pyodide-build pin in %s\n' "${REQUIREMENTS}" >&2
  exit 1
fi
readonly WHEEL_TAG="cp313-cp313-pyemscripten_2025_0_wasm32"
readonly WHEEL_NAME="viennarna-${VIENNA_VERSION}-${WHEEL_TAG}.whl"
readonly EXT_SUFFIX=".cpython-313-wasm32-emscripten.so"
readonly TOOLS="${HYB2_BUILD_TOOLS:-${ROOT}/.build-tools}"
readonly OUTPUT_DIRECTORY="${HYB2_VIENNA_PYODIDE_OUTPUT:-${TOOLS}/viennarna-pyodide}"
# checked before the toolchain is set up, which can take minutes
if [[ -n "${HYB2_VIENNA_TARBALL:-}" && ! -f "${HYB2_VIENNA_TARBALL}" ]]; then
  printf 'HYB2_VIENNA_TARBALL names a missing file: %s (unset it to download the release)\n' "${HYB2_VIENNA_TARBALL}" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  hyb2_sha256() { sha256sum "$1" | awk '{print $1}'; }
else
  hyb2_sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
fi

# ---- toolchain
python313="${HYB2_PYTHON313:-}"
if [[ -z "${python313}" ]]; then
  if command -v python3.13 >/dev/null 2>&1; then
    python313="$(command -v python3.13)"
  else
    python313="${TOOLS}/python313/bin/python3"
  fi
fi
if [[ ! -x "${python313}" ]] || ! "${python313}" -c 'import sys; assert sys.version_info[:2] == (3, 13)' 2>/dev/null; then
  printf 'Python 3.13 is required (pyodide-build must match Pyodide %s): set HYB2_PYTHON313.\n' "${PYODIDE_VERSION}" >&2
  exit 1
fi
venv="${TOOLS}/pyodide-venv"
requirements_stamp="${venv}/.hyb2-requirements.sha256"
requirements_sha256="$(hyb2_sha256 "${REQUIREMENTS}")"
# a cached venv whose interpreter moved or changed no longer runs, and one
# installed from other pins is stale: rebuild it
if [[ "$(cat "${requirements_stamp}" 2>/dev/null || true)" != "${requirements_sha256}" ]] \
   || ! "${venv}/bin/python" -c 'import sys; assert sys.version_info[:2] == (3, 13)' 2>/dev/null \
   || ! "${venv}/bin/pyodide" --version >/dev/null 2>&1 \
   || [[ "$("${venv}/bin/python" -c 'import importlib.metadata as m; print(m.version("pyodide-build"))' 2>/dev/null)" != "${PYODIDE_BUILD_VERSION}" ]]; then
  rm -rf -- "${venv}"
  "${python313}" -m venv "${venv}"
  "${venv}/bin/pip" install --quiet --require-hashes --only-binary=:all: -r "${REQUIREMENTS}"
  printf '%s\n' "${requirements_sha256}" > "${requirements_stamp}"
fi
export PYODIDE_XBUILDENV_PATH="${TOOLS}/pyodide-xbuildenv"
# another emsdk sourced in the calling shell (scripts/verify-release-gate.sh
# builds the 3.1.57 engine first) must not configure Pyodide's Emscripten
unset EMSDK EMSDK_NODE EM_CONFIG EM_CACHE
# emsdk's own scripts need Python 3.10+ (macOS ships 3.9)
export EMSDK_PYTHON="${python313}"
if [[ "$("${venv}/bin/pyodide" xbuildenv version 2>/dev/null || true)" != "${PYODIDE_VERSION}" ]]; then
  "${venv}/bin/pyodide" xbuildenv install "${PYODIDE_VERSION}"
fi
"${venv}/bin/pyodide" xbuildenv install-emscripten >/dev/null
emscripten_version="$("${venv}/bin/pyodide" config get emscripten_version)"
if [[ "${emscripten_version}" != "${PYODIDE_EMSCRIPTEN_VERSION}" ]]; then
  printf 'Pyodide %s uses Emscripten %s, but scripts/pyodide-toolchain.env pins %s.\n' "${PYODIDE_VERSION}" "${emscripten_version}" "${PYODIDE_EMSCRIPTEN_VERSION}" >&2
  exit 1
fi
python_include="$("${venv}/bin/pyodide" config get python_include_dir)"
emsdk_directory="${PYODIDE_XBUILDENV_PATH}/${PYODIDE_VERSION}/emsdk"
export EMSDK_QUIET=1
# shellcheck disable=SC1091
source "${emsdk_directory}/emsdk_env.sh" >/dev/null
if [[ "$(emcc -dumpversion)" != "${emscripten_version}" ]]; then
  printf 'Expected Emscripten %s for Pyodide %s, found %s.\n' "${emscripten_version}" "${PYODIDE_VERSION}" "$(emcc -dumpversion)" >&2
  exit 1
fi

# ---- source
build="$(mktemp -d "${TMPDIR:-/tmp}/hyb2-vienna-pyodide.XXXXXX")"
cleanup() { [[ "${HYB2_VIENNA_KEEP_BUILD:-0}" == "1" ]] || rm -rf -- "${build}"; }
trap cleanup EXIT
if [[ -n "${HYB2_VIENNA_TARBALL:-}" ]]; then
  tarball="${HYB2_VIENNA_TARBALL}"
  printf 'Using local ViennaRNA %s source: %s\n' "${VIENNA_VERSION}" "${tarball}"
else
  tarball="${build}/${VIENNA_TARBALL}"
  curl --fail --location --retry 3 --silent --show-error --output "${tarball}" "${VIENNA_SOURCE_URL}"
fi
if [[ "$(hyb2_sha256 "${tarball}")" != "${VIENNA_SHA256}" ]]; then
  printf 'ViennaRNA source SHA-256 mismatch: %s\n' "${tarball}" >&2
  exit 1
fi
tar -xzf "${tarball}" -C "${build}"
source_directory="${build}/ViennaRNA-${VIENNA_VERSION}"

# ---- RNAlib, then the SWIG wrapper, linked as a Pyodide side module
# (flags: pyodide config get cflags / cxxflags / ldflags)
readonly COMMON_FLAGS="-O2 -g0 -fPIC -fwasm-exceptions -sSUPPORT_LONGJMP"
pushd "${source_directory}" >/dev/null
printf 'Configuring RNAlib %s with Emscripten %s…\n' "${VIENNA_VERSION}" "${emscripten_version}"
emconfigure ./configure \
  --host=wasm32-unknown-emscripten \
  --disable-shared --enable-static --with-pic \
  --disable-pthreads --disable-openmp --disable-simd \
  --without-swig --without-perl --without-python \
  --without-kinfold --without-forester --without-rnalocmin --without-rnaxplorer \
  --without-doc --disable-unittests --without-gsl --without-json \
  CFLAGS="${COMMON_FLAGS}" CXXFLAGS="${COMMON_FLAGS}" > "${build}/configure.log" 2>&1 \
  || { tail -40 "${build}/configure.log" >&2; exit 1; }
printf 'Building RNAlib…\n'
emmake make -C src/ViennaRNA -j"${HYB2_BUILD_JOBS:-4}" > "${build}/make.log" 2>&1 \
  || { tail -40 "${build}/make.log" >&2; exit 1; }
printf 'Compiling the Python wrapper…\n'
# CPython's inline assertions include __FILE__ in the side module.
em++ -c ${COMMON_FLAGS} -Oz -fno-strict-aliasing -Wno-unused-label -Wno-unused-variable \
  "-ffile-prefix-map=${python_include}=cpython/include" "-ffile-prefix-map=${source_directory}=." \
  -I"${python_include}" -DVRNA_WITH_NAVIEW_LAYOUT -DVRNA_WITH_SVM -DVRNA_LOG_NO_DEBUG_RNALIB \
  -I"${source_directory}/src" -I"${source_directory}/src/ViennaRNA" \
  interfaces/Python/RNA_wrap.cpp -o "${build}/RNA_wrap.o"
em++ ${COMMON_FLAGS} -s WASM_BIGINT -s SIDE_MODULE=1 -Oz \
  "${build}/RNA_wrap.o" src/ViennaRNA/.libs/libRNA.a -o "${build}/_RNA${EXT_SUFFIX}"
popd >/dev/null

# ---- wheel (fixed timestamps, so the same inputs give the same bytes),
# written next to its final path and renamed into place, so an interrupted
# build cannot leave a truncated wheel there
mkdir -p "${OUTPUT_DIRECTORY}"
wheel_partial="${OUTPUT_DIRECTORY}/.${WHEEL_NAME}.partial.$$"
cleanup() {
  rm -f -- "${wheel_partial}"
  [[ "${HYB2_VIENNA_KEEP_BUILD:-0}" == "1" ]] || rm -rf -- "${build}"
}
"${python313}" - "${source_directory}" "${build}/_RNA${EXT_SUFFIX}" "${wheel_partial}" \
  "${VIENNA_VERSION}" "${WHEEL_TAG}" "${EXT_SUFFIX}" "${PYODIDE_VERSION}" <<'PY_WHEEL'
import base64, hashlib, sys, zipfile
from pathlib import Path
source, module, wheel, version, tag, suffix, pyodide = sys.argv[1:]
python = Path(source) / "interfaces/Python"
dist = f"viennarna-{version}.dist-info"
files = {
    "RNA/__init__.py": (python / "RNA/__init__.py.in").read_text().replace("@PACKAGE_VERSION@", version).encode(),
    "RNA/RNA.py": (python / "RNA/RNA.py").read_bytes(),
    f"RNA/_RNA{suffix}": Path(module).read_bytes(),
    "ViennaRNA/__init__.py": (python / "ViennaRNA/__init__.py").read_bytes(),
    f"{dist}/licenses/COPYING": (Path(source) / "COPYING").read_bytes(),
    f"{dist}/METADATA": (f"Metadata-Version: 2.1\nName: ViennaRNA\nVersion: {version}\n"
                         f"Summary: ViennaRNA Package Python bindings, built for Pyodide {pyodide} by HYB2\n"
                         "Home-page: https://www.tbi.univie.ac.at/RNA\nAuthor: Ronny Lorenz\n"
                         "License-File: COPYING\n").encode(),
    f"{dist}/WHEEL": f"Wheel-Version: 1.0\nGenerator: hyb2 build-vienna-pyodide.sh\nRoot-Is-Purelib: false\nTag: {tag}\n".encode(),
    f"{dist}/top_level.txt": b"RNA\nViennaRNA\n",
}
record = [f"{name},sha256={base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b'=').decode()},{len(data)}"
          for name, data in files.items()]
files[f"{dist}/RECORD"] = ("\n".join(record + [f"{dist}/RECORD,,"]) + "\n").encode()
with zipfile.ZipFile(wheel, "w") as archive:
    for name in sorted(files):
        info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
        info.compress_type = zipfile.ZIP_DEFLATED
        info.external_attr = 0o644 << 16
        archive.writestr(info, files[name], compresslevel=9)
PY_WHEEL
mv -f -- "${wheel_partial}" "${OUTPUT_DIRECTORY}/${WHEEL_NAME}"
printf 'Built %s (%s bytes)\n' "${OUTPUT_DIRECTORY}/${WHEEL_NAME}" "$(wc -c < "${OUTPUT_DIRECTORY}/${WHEEL_NAME}" | tr -d ' ')"
