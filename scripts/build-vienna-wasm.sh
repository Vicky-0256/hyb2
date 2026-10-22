#!/usr/bin/env bash

# Build a narrow RNAlib WebAssembly module for the static HYB2 Web site.
#
# Requirements: curl, tar, shasum/sha256sum, pkg-config and an active Emscripten SDK
# (emcc, emconfigure, emmake). The deployment workflow supplies the pinned SDK.

set -euo pipefail

readonly HYB2_REPOSITORY_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly VIENNA_VERSION="2.7.2"
readonly VIENNA_TARBALL="ViennaRNA-${VIENNA_VERSION}.tar.gz"
# The official ViennaRNA GitHub release asset. The University of Vienna
# download (www.tbi.univie.ac.at/RNA/download/sourcecode/2_7_x/, pinned at
# 1ab5f4a4…) answers "Missing link ID" since 2026-10-22; this is a different
# tarball of the same release, so it has its own digest.
readonly VIENNA_SOURCE_URL="https://github.com/ViennaRNA/ViennaRNA/releases/download/v${VIENNA_VERSION}/${VIENNA_TARBALL}"
readonly VIENNA_SHA256="4cb7aaca00dfac92cb36b030c9111487c170b90568759bd4ffedd8318cc9384f"
readonly OUTPUT_DIRECTORY="${HYB2_REPOSITORY_ROOT}/web/vendor/viennarna"
readonly WRAPPER_SOURCE="${OUTPUT_DIRECTORY}/hyb2_vienna_fold.c"

for hyb2_command in curl tar pkg-config emcc emconfigure emmake node; do
  command -v "${hyb2_command}" >/dev/null || {
    printf 'Missing required command: %s\n' "${hyb2_command}" >&2
    exit 1
  }
done

if command -v sha256sum >/dev/null 2>&1; then
  hyb2_sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  hyb2_sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  printf 'Missing SHA-256 tool: install coreutils (sha256sum) or use the macOS shasum command.\n' >&2
  exit 1
fi

if [[ ! -f "${WRAPPER_SOURCE}" ]]; then
  printf 'Missing ViennaRNA wrapper source: %s\n' "${WRAPPER_SOURCE}" >&2
  exit 1
fi

hyb2_created_build_directory=0
if [[ -n "${HYB2_VIENNA_BUILD_DIR:-}" ]]; then
  hyb2_build_directory="${HYB2_VIENNA_BUILD_DIR}"
  mkdir -p "${hyb2_build_directory}"
else
  hyb2_build_directory="$(mktemp -d "${TMPDIR:-/tmp}/hyb2-vienna-wasm.XXXXXX")"
  hyb2_created_build_directory=1
fi

cleanup() {
  if [[ "${hyb2_created_build_directory}" -eq 1 && "${HYB2_VIENNA_KEEP_BUILD:-0}" != "1" ]]; then
    rm -rf -- "${hyb2_build_directory}"
  fi
}
trap cleanup EXIT

hyb2_tarball_path="${hyb2_build_directory}/${VIENNA_TARBALL}"
hyb2_source_directory="${hyb2_build_directory}/ViennaRNA-${VIENNA_VERSION}"

printf 'Downloading ViennaRNA %s source…\n' "${VIENNA_VERSION}"
curl --fail --location --retry 3 --silent --show-error \
  --output "${hyb2_tarball_path}" "${VIENNA_SOURCE_URL}"
hyb2_actual_sha256="$(hyb2_sha256 "${hyb2_tarball_path}")"
if [[ "${hyb2_actual_sha256}" != "${VIENNA_SHA256}" ]]; then
  printf 'ViennaRNA source SHA-256 mismatch: expected %s, got %s\n' "${VIENNA_SHA256}" "${hyb2_actual_sha256}" >&2
  exit 1
fi

tar -xzf "${hyb2_tarball_path}" -C "${hyb2_build_directory}"
test -d "${hyb2_source_directory}"

pushd "${hyb2_source_directory}" >/dev/null
emconfigure ./configure \
  --host=wasm32-unknown-emscripten \
  --disable-shared \
  --enable-static \
  --disable-pthreads \
  --disable-openmp \
  --disable-simd \
  --disable-naview \
  --without-svm \
  --without-swig \
  --without-perl \
  --without-python \
  --without-kinfold \
  --without-forester \
  --without-rnalocmin \
  --without-rnaxplorer \
  --without-doc \
  --disable-unittests
emmake make -C src/ViennaRNA -j2 libRNA.la
popd >/dev/null

hyb2_library="${hyb2_source_directory}/src/ViennaRNA/.libs/libRNA.a"
if [[ ! -f "${hyb2_library}" ]]; then
  printf 'Expected static RNAlib archive was not built: %s\n' "${hyb2_library}" >&2
  exit 1
fi

mkdir -p "${OUTPUT_DIRECTORY}"
rm -f -- "${OUTPUT_DIRECTORY}/vienna-rna.js" \
  "${OUTPUT_DIRECTORY}/vienna-rna.wasm" \
  "${OUTPUT_DIRECTORY}/build-manifest.json" \
  "${OUTPUT_DIRECTORY}/COPYING-ViennaRNA.txt"

emcc "${WRAPPER_SOURCE}" "${hyb2_library}" \
  -I"${hyb2_source_directory}" \
  -I"${hyb2_source_directory}/src" \
  -O3 \
  -s MODULARIZE=1 \
  -s EXPORT_NAME=createHyb2Vienna \
  -s ENVIRONMENT='web,worker,node' \
  -s ALLOW_MEMORY_GROWTH=1 \
  -s INITIAL_MEMORY=67108864 \
  -s NO_EXIT_RUNTIME=1 \
  -s EXPORTED_FUNCTIONS='["_hyb2_vienna_mfe","_hyb2_vienna_mfe_constrained","_hyb2_vienna_cofold","_hyb2_vienna_version","_malloc","_free"]' \
  -s EXPORTED_RUNTIME_METHODS='["lengthBytesUTF8","stringToUTF8","UTF8ToString"]' \
  "-DHYB2_VIENNA_VERSION=\"${VIENNA_VERSION}\"" \
  -o "${OUTPUT_DIRECTORY}/vienna-rna.js"

cp "${hyb2_source_directory}/COPYING" "${OUTPUT_DIRECTORY}/COPYING-ViennaRNA.txt"
printf '{\n  "package": "ViennaRNA",\n  "version": "%s",\n  "source": "%s",\n  "sourceSha256": "%s",\n  "buildToolchain": "Emscripten 3.1.57",\n  "entry": "vienna-rna.js"\n}\n' \
  "${VIENNA_VERSION}" "${VIENNA_SOURCE_URL}" "${VIENNA_SHA256}" > "${OUTPUT_DIRECTORY}/build-manifest.json"

node "${HYB2_REPOSITORY_ROOT}/scripts/verify-vienna-wasm.cjs" "${OUTPUT_DIRECTORY}/vienna-rna.js"
printf 'Built ViennaRNA WebAssembly assets in %s\n' "${OUTPUT_DIRECTORY}"
