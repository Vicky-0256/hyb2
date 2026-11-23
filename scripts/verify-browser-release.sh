#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
for file in web/*.js web/*.mjs scripts/webr-smoke/spike.mjs; do node --check "$file"; done
python3 -B scripts/bundle_sources.py --verify web/vendor/pyodide-cplfold
for check in scripts/verify-*.cjs scripts/verify-*.mjs; do
  echo "Checking $check"
  case "$check" in
    scripts/verify-vienna-wasm.cjs) node "$check" web/vendor/viennarna/vienna-rna.js ;;
    *) node "$check" ;;
  esac
done

test -s web/build-info.js
test -s web/vendor/viennarna/COPYING-ViennaRNA.txt
test -s web/vendor/viennarna/vienna-rna.js
test -s web/vendor/viennarna/vienna-rna.wasm
test -s web/vendor/pyodide-cplfold/pyodide.mjs
test -s web/vendor/pyodide-cplfold/pyodide.asm.wasm
vienna_wheel="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["packages"]["viennarna"]["file_name"])' web/vendor/pyodide-cplfold/pyodide-lock.json)"
[[ "$vienna_wheel" =~ ^viennarna-[0-9a-f]{16}/viennarna-2\.7\.2-cp313-cp313-pyemscripten_2025_0_wasm32\.whl$ ]]
test -s "web/vendor/pyodide-cplfold/$vienna_wheel"
