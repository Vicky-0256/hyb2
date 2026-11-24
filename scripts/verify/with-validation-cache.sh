#!/usr/bin/env bash
# Run one foreground validation command with disposable, inherited caches.
set -euo pipefail

if (( $# == 0 )); then
  echo "Usage: $0 command [argument ...]" >&2
  exit 2
fi

cache=$(mktemp -d "${TMPDIR:-/tmp}/hyb2-validation.XXXXXX")
trap 'rm -rf -- "$cache"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# -B alone does not protect Python interpreters started by subprocesses or exec.
export PYTHONDONTWRITEBYTECODE=1
export PYTHONPYCACHEPREFIX="$cache/python"
export NUMBA_CACHE_DIR="$cache/numba"
export XDG_CACHE_HOME="$cache/xdg"
export GOCACHE="$cache/go-build"

"$@"
