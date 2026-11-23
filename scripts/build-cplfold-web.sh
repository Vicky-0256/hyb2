#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
python3 -B "$repository_root/scripts/export-support-kernels.py"
# shellcheck source=pyodide-toolchain.env
source "$repository_root/scripts/pyodide-toolchain.env"
final_directory="$repository_root/web/vendor/pyodide-cplfold"
# The runtime is built in a staging directory next to it (same filesystem)
# and swapped in only once complete, so a failed build leaves the previous
# runtime untouched.
output_directory="$(dirname "$final_directory")/.pyodide-cplfold.staging.$$"
previous_directory="$(dirname "$final_directory")/.pyodide-cplfold.previous.$$"
pyodide_version="$PYODIDE_VERSION"
pyodide_base_url="https://cdn.jsdelivr.net/pyodide/v${pyodide_version}/full"
pyodide_license_url="https://raw.githubusercontent.com/pyodide/pyodide/${pyodide_version}/LICENSE"
vendor_directory="$(dirname "$final_directory")"
# One build at a time: two concurrent swaps race (one mv finds its source
# gone, or a staging directory lands inside the new runtime), and the sweep
# below must not remove another live build's directories.  mkdir is atomic on
# every filesystem, so the lock is a directory holding the owner's PID.
lock_directory="$vendor_directory/.pyodide-cplfold.lock"
lock_timeout="${HYB2_CPLFOLD_LOCK_TIMEOUT:-600}"
lock_held=0
build_directory=""

if command -v sha256sum >/dev/null 2>&1; then
  hyb2_sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  hyb2_sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  printf 'Missing SHA-256 tool: install coreutils or use macOS shasum.\n' >&2
  exit 1
fi

# ViennaRNA's Python module for Pyodide (scripts/build-vienna-pyodide.sh):
# the duplex read-support kernel imports RNA in the browser as the desktop
# does. The separate COMRADES and HYB dG tools use vendor/viennarna.
# The Python wheel is added to pyodide-lock.json,
# so the worker loads it with pyodide.loadPackage("viennarna") and Pyodide
# checks its SHA-256.  Checked first, so a missing wheel fails before any
# download or change under web/vendor.
vienna_wheel="viennarna-2.7.2-cp313-cp313-pyemscripten_2025_0_wasm32.whl"
vienna_source="${HYB2_VIENNA_PYODIDE_WHEEL:-${HYB2_BUILD_TOOLS:-$repository_root/.build-tools}/viennarna-pyodide/$vienna_wheel}"
if [[ ! -s "$vienna_source" ]]; then
  printf 'Missing ViennaRNA Pyodide wheel %s: run scripts/build-vienna-pyodide.sh first (or set HYB2_VIENNA_PYODIDE_WHEEL).\n' "$vienna_source" >&2
  exit 1
fi

cleanup() {
  [[ -n "$build_directory" ]] && rm -rf "$build_directory"
  rm -rf "$output_directory"
  # interrupted between the two moves of the swap: put the old runtime back
  if [[ -d "$previous_directory" && ! -e "$final_directory" ]]; then
    mv "$previous_directory" "$final_directory"
  fi
  rm -rf "$previous_directory"
  if [[ "$lock_held" -eq 1 ]]; then
    rm -rf "$lock_directory"
  fi
}
trap cleanup EXIT
# a signal runs the EXIT trap too (bash runs it on exit, not on every signal)
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

mkdir -p "$vendor_directory"
lock_waited=0
until mkdir "$lock_directory" 2>/dev/null; do
  lock_owner="$(cat "$lock_directory/pid" 2>/dev/null || true)"
  if [[ -n "$lock_owner" ]] && ! kill -0 "$lock_owner" 2>/dev/null; then
    # The owner died without its cleanup (SIGKILL).  Take the lock over in
    # place, under a second mkdir, so two waiters cannot both take it: the
    # one that loses finds the winner's live PID and keeps waiting.
    if mkdir "$lock_directory/takeover" 2>/dev/null; then
      if [[ "$(cat "$lock_directory/pid" 2>/dev/null || true)" == "$lock_owner" ]]; then
        printf '%s\n' "$$" > "$lock_directory/pid"
        rmdir "$lock_directory/takeover"
        printf 'Took over the build lock of dead process %s\n' "$lock_owner" >&2
        break
      fi
      rmdir "$lock_directory/takeover" 2>/dev/null || true
    elif [[ -z "$(find "$lock_directory/takeover" -maxdepth 0 -mmin -1 2>/dev/null)" ]]; then
      # a taker killed mid-takeover a minute ago
      rmdir "$lock_directory/takeover" 2>/dev/null || true
      continue
    fi
  elif [[ -z "$lock_owner" && -d "$lock_directory" && -z "$(find "$lock_directory" -maxdepth 0 -mmin -1 2>/dev/null)" ]]; then
    # no PID a minute after mkdir: its owner was killed in between
    rm -rf "$lock_directory"
    continue
  fi
  if (( lock_waited >= lock_timeout )); then
    printf 'Another build-cplfold-web.sh (PID %s) still holds %s after %s s; giving up.\n' "${lock_owner:-?}" "$lock_directory" "$lock_waited" >&2
    exit 1
  fi
  if (( lock_waited % 30 == 0 )); then
    printf 'Waiting for another build-cplfold-web.sh (PID %s) to finish…\n' "${lock_owner:-?}" >&2
  fi
  sleep 1
  lock_waited=$((lock_waited + 1))
done
lock_held=1
[[ "$(cat "$lock_directory/pid" 2>/dev/null || true)" == "$$" ]] || printf '%s\n' "$$" > "$lock_directory/pid"

# Leftovers of builds that were killed before their own cleanup ran (no live
# build can own one now: they all take the lock first).  A build killed
# between the two moves of its swap left no runtime, only .previous.<pid>
# with the old one: restore the newest such directory instead of deleting it.
if [[ ! -e "$final_directory" ]]; then
  newest_previous=""
  for leftover in "$vendor_directory"/.pyodide-cplfold.previous.*; do
    [[ -d "$leftover" ]] || continue
    [[ "${leftover##*.}" != "$$" ]] || continue
    kill -0 "${leftover##*.}" 2>/dev/null && continue
    if [[ -z "$newest_previous" || "$leftover" -nt "$newest_previous" ]]; then
      newest_previous="$leftover"
    fi
  done
  if [[ -n "$newest_previous" ]]; then
    mv "$newest_previous" "$final_directory"
    printf 'Restored the runtime of an interrupted build from %s\n' "$newest_previous" >&2
  fi
fi
for leftover in "$vendor_directory"/.pyodide-cplfold.staging.* "$vendor_directory"/.pyodide-cplfold.previous.*; do
  [[ -d "$leftover" ]] || continue
  if ! kill -0 "${leftover##*.}" 2>/dev/null; then
    rm -rf "$leftover"
  fi
done

build_directory="$(mktemp -d)"
download_directory="$build_directory/downloads"
staging_directory="$build_directory/cplfold"

mkdir -p "$download_directory" "$staging_directory/cplfold/_internal/energy_params" "$output_directory"
# keep the runtime directory's own .gitignore (it ignores everything else)
if [[ -f "$final_directory/.gitignore" ]]; then
  cp -p "$final_directory/.gitignore" "$output_directory/.gitignore"
else
  printf '*\n!.gitignore\n' > "$output_directory/.gitignore"
fi

download_and_verify() {
  local name="$1"
  local expected="$2"
  local url="$3"
  local cached="${HYB2_PYODIDE_CACHE:-}/$name"
  local destination="$download_directory/$name"

  if [[ -n "${HYB2_PYODIDE_CACHE:-}" && -f "$cached" ]]; then
    cp "$cached" "$destination"
  else
    curl -L --fail --silent --show-error "$url" -o "$destination"
  fi
  # macOS ships a BSD ``sha256sum`` compatibility command without GNU's
  # ``--check --status`` flags. Compute the digest explicitly so the same
  # reproducible build works on macOS and Linux.
  local actual
  actual="$(hyb2_sha256 "$destination")"
  if [[ "$actual" != "$expected" ]]; then
    printf 'Checksum failed for %s (expected %s, got %s)\n' "$name" "$expected" "$actual" >&2
    exit 1
  fi
  cp "$destination" "$output_directory/$name"
}

download_and_verify "pyodide.mjs" "8fdfed5eaf81bde14bcdeaeea11f2672675b2362248f8537446b6fda5e4a4751" "$pyodide_base_url/pyodide.mjs"
download_and_verify "pyodide.asm.js" "fe75e97ef2c7a10c41f23b96344c553c7c1c62821bb206080dbb24941c06d5f3" "$pyodide_base_url/pyodide.asm.js"
download_and_verify "pyodide.asm.wasm" "10090fe41e019ae669d512e1f747021a8db2aaab0f6dd6f85fa9368c55d681e3" "$pyodide_base_url/pyodide.asm.wasm"
download_and_verify "python_stdlib.zip" "92cb24faa546818f3ef4050fd5bd2b6487bd2042efed2113af141d035f30efb4" "$pyodide_base_url/python_stdlib.zip"
download_and_verify "pyodide-lock.json" "14d2c2dba101277999e17135e653d8f15389ad1437f53eae213bf0c3cdff723d" "$pyodide_base_url/pyodide-lock.json"
download_and_verify "numpy-2.2.5-cp313-cp313-pyemscripten_2025_0_wasm32.whl" "800c98edc0c864dfa49f07005680c699b4b42b84eae1f8cb19d35b3634e7f05c" "$pyodide_base_url/numpy-2.2.5-cp313-cp313-pyemscripten_2025_0_wasm32.whl"
download_and_verify "Pyodide-LICENSE.txt" "1f256ecad192880510e84ad60474eab7589218784b9a50bc7ceee34c2b91f1d5" "$pyodide_license_url"

build_commit="$(python3 "$repository_root/scripts/bundle_sources.py" "$staging_directory" "$output_directory")"

# The ViennaRNA wheel goes into a directory named after its own SHA-256, so a
# new wheel has a new URL: GitHub Pages caches files for about ten minutes,
# and a cached old wheel under a fresh lock would fail Pyodide's integrity
# check.
vienna_hash="$(hyb2_sha256 "$vienna_source")"
vienna_directory="viennarna-${vienna_hash:0:16}"
vienna_asset="$vienna_directory/$vienna_wheel"
# (The staging directory starts empty, so wheels and archives of earlier
# builds are not carried over.)
mkdir -p "$output_directory/$vienna_directory"
cp "$vienna_source" "$output_directory/$vienna_asset"

archive_staging="$build_directory/cplfold-python.zip"
# Sorted entries, fixed timestamps, fixed file modes and a fixed compression
# level, written by zipfile rather than zip(1): zip stores each file's mode,
# which follows the umask and filesystem.  The same sources give the same
# archive bytes, hence the same file name.
python3 - "$build_directory" "$archive_staging" <<'PY_ARCHIVE'
import os, sys, zipfile
from pathlib import Path
root, archive_path = Path(sys.argv[1]), sys.argv[2]
names = []
for directory, subdirectories, files in os.walk(root / "cplfold"):
    relative = Path(directory).relative_to(root).as_posix()
    names.append(relative + "/")
    names.extend(f"{relative}/{name}" for name in files)
with zipfile.ZipFile(archive_path, "w") as archive:
    for name in sorted(names):
        info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
        info.create_system = 3
        if name.endswith("/"):
            info.external_attr = (0o40755 << 16) | 0x10
            archive.writestr(info, b"")
        else:
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o100644 << 16
            archive.writestr(info, (root / name).read_bytes(), compresslevel=9)
PY_ARCHIVE

archive_hash="$(hyb2_sha256 "$archive_staging")"
archive_file="cplfold-python-${archive_hash}.zip"
cp "$archive_staging" "$output_directory/$archive_file"
printf '{\n  "pyodide": "%s",\n  "python": "3.13.2",\n  "numpy": "2.2.5",\n  "cplfoldRevision": "24bab52",\n  "bridgeVersion": "8",\n  "buildCommit": "%s",\n  "cplfoldArchiveFile": "%s",\n  "cplfoldArchiveSha256": "%s"\n}\n' \
  "$pyodide_version" "$build_commit" "$archive_file" "$archive_hash" > "$output_directory/build-manifest.json"

python3 - "$output_directory" "$vienna_asset" <<'PY_VIENNA_LOCK'
import hashlib, json, sys
from pathlib import Path
root, wheel = Path(sys.argv[1]), sys.argv[2]
lock_path = root / "pyodide-lock.json"
lock = json.loads(lock_path.read_text(encoding="utf-8"))
lock["packages"]["viennarna"] = {
    "depends": [], "file_name": wheel, "imports": ["RNA", "ViennaRNA"], "install_dir": "site",
    "name": "viennarna", "package_type": "package",
    "sha256": hashlib.sha256((root / wheel).read_bytes()).hexdigest(),
    "unvendored_tests": False, "version": "2.7.2"}
lock_path.write_text(json.dumps(lock, separators=(",", ":"), sort_keys=True) + "\n", encoding="utf-8")
PY_VIENNA_LOCK

# Record hashes for every shipped static asset, not only the Python archive.
python3 - "$output_directory" "$vienna_asset" <<'PY_ASSET_MANIFEST'
import hashlib, json, sys
from pathlib import Path
root = Path(sys.argv[1])
manifest_path = root / "build-manifest.json"
manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
names = ["pyodide.mjs", "pyodide.asm.js", "pyodide.asm.wasm", "python_stdlib.zip",
         "pyodide-lock.json", "numpy-2.2.5-cp313-cp313-pyemscripten_2025_0_wasm32.whl",
         "Pyodide-LICENSE.txt", "CPLfold-LICENSE.txt", "CPLfold-THIRD_PARTY_NOTICES.md",
         "CPLfold-GPL-3.0-or-later.txt", "CPLfold-GPL-2.0-or-later.txt", "CPLfold-LinearFold-LICENSE.txt", "CPLfold-LinearCoFold-LICENSE.txt",
         "CPLfold-ViennaRNA-COPYING.txt",
         sys.argv[2],
         manifest["cplfoldArchiveFile"]]
manifest["viennarna"] = "2.7.2"
manifest["assets"] = {name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in names}
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
PY_ASSET_MANIFEST

# swap the complete runtime in
if [[ -e "$final_directory" ]]; then
  mv "$final_directory" "$previous_directory"
fi
# (the lock keeps other builds out; anything else that recreated it would
# otherwise get the staging directory moved inside it)
if [[ -e "$final_directory" ]]; then
  printf '%s reappeared during the swap; leaving it as it is.\n' "$final_directory" >&2
  exit 1
fi
mv "$output_directory" "$final_directory"
rm -rf "$previous_directory"

printf 'Built browser CPLfold runtime in %s\n' "$final_directory"
