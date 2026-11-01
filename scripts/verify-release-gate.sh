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

echo '[1/7] Build and verify ViennaRNA WebAssembly'
bash scripts/build-vienna-wasm.sh
node scripts/verify-vienna-wasm.cjs web/vendor/viennarna/vienna-rna.js

echo '[2/7] Build browser CPLfold/Pyodide runtime'
bash scripts/build-cplfold-web.sh

echo '[3/7] Verify browser CPLfold runtime'
node scripts/verify-cplfold-pyodide.mjs
node scripts/verify-cplfold-worker.mjs

echo '[4/7] Verify structure worker against ViennaRNA'
node scripts/verify-structure-worker.cjs
node scripts/verify-structure-render.cjs

echo '[5/7] Verify application contracts'
node scripts/verify-protocol-contract.cjs
node scripts/verify-ui-contract.cjs
node scripts/verify-analysis-data.cjs

echo '[6/7] Verify native desktop runtime'
python3 desktop/scripts/verify-native-release.py

echo '[7/7] Verify release asset inventory'
test -s web/vendor/viennarna/vienna-rna.js
test -s web/vendor/viennarna/vienna-rna.wasm
test -s web/vendor/pyodide-cplfold/pyodide.mjs
test -s web/vendor/pyodide-cplfold/pyodide.asm.wasm
test -s desktop/resources/native/native-manifest.json
echo 'HYB2 release gate: PASS'
