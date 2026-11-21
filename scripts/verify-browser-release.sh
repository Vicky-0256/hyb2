#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
node scripts/verify-vienna-wasm.cjs web/vendor/viennarna/vienna-rna.js
node scripts/verify-cplfold-pyodide.mjs
node scripts/verify-cplfold-worker.mjs
node scripts/verify-structure-worker.cjs
node scripts/verify-structure-render.cjs
node scripts/verify-cplfold-worker-lifecycle.cjs
node scripts/verify-protocol-contract.cjs
node scripts/verify-ui-contract.cjs
node scripts/verify-desktop-entry.cjs
node scripts/verify-analysis-data.cjs

test -s web/vendor/viennarna/vienna-rna.js
test -s web/vendor/viennarna/vienna-rna.wasm
test -s web/vendor/pyodide-cplfold/pyodide.mjs
test -s web/vendor/pyodide-cplfold/pyodide.asm.wasm
vienna_wheel="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["packages"]["viennarna"]["file_name"])' web/vendor/pyodide-cplfold/pyodide-lock.json)"
[[ "$vienna_wheel" =~ ^viennarna-[0-9a-f]{16}/viennarna-2\.7\.2-cp313-cp313-pyemscripten_2025_0_wasm32\.whl$ ]]
test -s "web/vendor/pyodide-cplfold/$vienna_wheel"
