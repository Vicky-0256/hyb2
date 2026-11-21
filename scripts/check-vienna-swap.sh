#!/usr/bin/env bash
# Run the desktop, pipeline, CPLfold and Go regression suites.
# HYB2_PYTHON selects the Python interpreter (default: the bundled runtime).
set -euo pipefail

if (( $# )); then
  echo "Usage: $0" >&2
  exit 2
fi

cd "$(dirname "${BASH_SOURCE[0]}")/.."
python=${HYB2_PYTHON:-desktop/resources/native/python/bin/python3}
if ! python_path=$(type -P -- "$python"); then
  echo "Cannot find Python '$python'. Set HYB2_PYTHON to the desktop runtime interpreter." >&2
  exit 127
fi
if [[ "$python_path" != /* ]]; then
  python_path=$PWD/$python_path
fi

export PYTHONDONTWRITEBYTECODE=1
status=0

echo "== Desktop runtime"
"$python_path" -m unittest discover -s desktop/runtime/tests -p 'test_*.py' || status=$?

echo "== Pipeline"
"$python_path" -m unittest discover -s pipeline/tests -p 'test_*.py' || status=$?

echo "== CPLfold"
(cd third_party/cplfold && "$python_path" -m unittest discover -s tests -p 'test_*.py') || status=$?

echo "== Go"
(cd desktop/app && HYB2_PYTHON="$python_path" go test ./...) || status=$?

exit "$status"
