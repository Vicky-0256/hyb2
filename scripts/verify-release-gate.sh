#!/usr/bin/env bash
set -euo pipefail

# Deterministic release order. A release is valid only when both browser
# engines and the native desktop runtime pass their checks.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

# Use a project-local SDK when one has been prepared by the reproducible
# developer setup. CI sources its pinned emsdk in the workflow before calling
# the individual steps; without either environment, fail with the tool name
# rather than producing a misleading asset error.
if ! command -v emcc >/dev/null 2>&1 && [[ -f "$root/.build-tools/emsdk/emsdk_env.sh" ]]; then
  # shellcheck disable=SC1091
  source "$root/.build-tools/emsdk/emsdk_env.sh"
fi
if ! command -v emcc >/dev/null 2>&1; then
  echo "Emscripten emcc is required. Source emsdk_env.sh or install the pinned 3.1.57 SDK." >&2
  exit 2
fi

echo '[1/5] Check the public folding commands'
python3 -B scripts/verify-folding-cli.py

echo '[2/5] Build ViennaRNA WebAssembly'
bash scripts/build-vienna-wasm.sh

echo '[3/5] Build browser CPLfold/Pyodide runtime with the ViennaRNA Python module'
bash scripts/build-vienna-pyodide.sh
bash scripts/build-cplfold-web.sh

echo '[4/5] Verify browser engines, application contracts and release assets'
bash scripts/verify-browser-release.sh

echo '[5/5] Verify native desktop runtime'
python3 desktop/scripts/verify-native-release.py
echo 'HYB2 release gate: PASS'
