"""mfold 3.6 MFE folds with hard base pairs, for COMRADES(mfold)
(``fold_program: "mfold"`` of the structure task's COMRADES engine).

ViennaRNA's constrained fold is the default; mfold is the alternative the
original Hyb/COMRADES scripts offered through UNAFold's hybrid-ss-min.  The
hybrid-ss-min of OligoArrayAux silently ignores forced pairs, while mfold
3.6 (bioconda ``mfold``; free, not bundled) applies them, so it is the
UNAFold-family program HYB2 can run (bin/mfold_fold is the command-line
counterpart).

mfold is found as ``HYB2_MFOLD`` (the app's Preferences), else ``mfold`` on
PATH, else the usual conda and Homebrew locations.  Each fold runs in its
own temporary folder holding the sequence, the constraints (``F i j 1``
lines) and links to mfold's energy tables: the bioconda build finds its
tables only in the current folder, and mfold writes many files named after
the sequence.  mfold folds at 37 °C only here: given any temperature (T=)
it switches to its older version 2.3 tables (and the bioconda build's
newtemp fails), so only its default, version 3.0 energies at 37 °C, are
used.  Its minimum hairpin loop is fixed at 3.
"""
from __future__ import annotations

import glob
import math
import os
import re
import shutil
import signal
import subprocess
import tempfile
import time
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence, Tuple

from hyb2analysis.fold_text import checked_fold_text
from hyb2analysis.engine_parameters import engine_parameters

ENV = "HYB2_MFOLD"
INSTALL = "conda create -n mfold -c conda-forge -c bioconda mfold"
# where mfold is usually installed (conda environments, Homebrew, /usr/local)
PREFIXES = ("~/miniforge3", "~/mambaforge", "~/miniconda3", "~/anaconda3", "~/opt/miniconda3", "~/opt/anaconda3",
            "/opt/conda", "/opt/homebrew", "/opt/homebrew/Caskroom/miniforge/base", "/usr/local")
_RANGES = engine_parameters("desktop")["mfold"]["ranges"]
MIN_LOOP = _RANGES["minLoopSize"][0]
TEMPERATURE = _RANGES["temperature"][0]
# mfold is slower than ViennaRNA and refolds once per accepted stem
MAX_MFOLD_NT = 3000
TIMEOUT_S = 3600
_CT_HEADER = re.compile(r"\s*([0-9]+)\s+dG\s*=\s*"
                        r"([+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)(?=\s|$)")


class MfoldError(RuntimeError):
    """The folding program failed or produced an invalid result."""


class MfoldUnavailable(MfoldError):
    """No usable mfold."""


def _executable(path: str) -> bool:
    return os.path.isfile(path) and os.access(path, os.X_OK)


def find(path: Optional[str] = None) -> str:
    """mfold: ``path``, else HYB2_MFOLD, else PATH, else the usual install
    locations.  A path that is given must work; it is never replaced by
    another.  Raises MfoldUnavailable."""
    configured = path or os.environ.get(ENV)
    if configured:
        configured = os.path.expanduser(str(configured))
        if not _executable(configured):
            raise MfoldUnavailable(f"mfold is not an executable file at {configured}.")
        return configured
    found = shutil.which("mfold")
    if found:
        return found
    for prefix in PREFIXES:
        root = os.path.expanduser(prefix)
        for candidate in [os.path.join(root, "bin", "mfold")] + sorted(glob.glob(os.path.join(root, "envs", "*", "bin", "mfold"))):
            if _executable(candidate):
                return candidate
    raise MfoldUnavailable(f"mfold 3.6 was not found. Install it with: {INSTALL} (it is free for academic use but "
                           "not bundled with HYB2), or set its path in Preferences, or fold with ViennaRNA.")


def data_dir(executable: str) -> Path:
    """mfold's energy tables: what ``mfold_datdir`` (next to mfold) prints, else
    ``<prefix>/share/mfold``."""
    bin_dir = Path(executable).resolve().parent
    helper = bin_dir / "mfold_datdir"
    candidates = []
    if _executable(str(helper)):
        try:
            done = subprocess.run([str(helper)], stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=10)
            if done.stdout.strip():
                candidates.append(Path(done.stdout.strip()))
        except (OSError, subprocess.SubprocessError):
            pass
    candidates.append(bin_dir.parent / "share" / "mfold")
    for candidate in candidates:
        if list(candidate.glob("*.dat")):
            return candidate
    raise MfoldUnavailable(f"mfold's energy tables (*.dat) were not found next to {executable}.")


def _env(executable: str) -> Dict[str, str]:
    """mfold's environment: its own folder first on PATH, as the mfold script
    runs its helpers (auxgen, nafold, ...) by name."""
    env = dict(os.environ)
    env["PATH"] = str(Path(executable).resolve().parent) + os.pathsep + env.get("PATH", "")
    return env


def _run(args: List[str], *, cwd: Path, env: Dict[str, str]) -> subprocess.CompletedProcess:
    """``subprocess.run`` of mfold, but a timeout ends its whole process group:
    mfold is a shell script whose folding programs are its children."""
    group: Dict[str, Any] = ({"creationflags": subprocess.CREATE_NEW_PROCESS_GROUP} if os.name == "nt"
                             else {"start_new_session": True})
    with subprocess.Popen(args, cwd=cwd, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          text=True, errors="replace", env=env, **group) as process:
        try:
            stdout, stderr = process.communicate(timeout=TIMEOUT_S)
        except BaseException:  # the timeout, or the task being interrupted
            _kill_group(process)
            raise
    return subprocess.CompletedProcess(args, process.returncode, stdout, stderr)


def _kill_group(process: subprocess.Popen) -> None:
    if os.name == "nt":
        subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], check=False,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    else:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except OSError:  # the group has already ended
            pass
    process.kill()


def version(executable: Optional[str] = None) -> Optional[str]:
    """"3.6" from ``mfold -V`` ("mfold version 3.6"); None when unknown."""
    try:
        executable = executable or find()
        done = subprocess.run([executable, "-V"], stdin=subprocess.DEVNULL, capture_output=True, text=True,
                              errors="replace", timeout=20, env=_env(executable))
    except (OSError, subprocess.SubprocessError, MfoldUnavailable):
        return None
    match = re.search(r"mfold version\s+(\d+(?:\.\d+)+)", (done.stdout or "") + (done.stderr or ""))
    return match.group(1) if match else None


def _pairs_from_ct(path: Path, sequence: str) -> Tuple[float, List[Tuple[int, int]]]:
    """Read the first complete CT structure for this single linear sequence."""
    length = len(sequence)
    partners = []
    with path.open(encoding="utf-8", errors="replace") as ct:
        header = _CT_HEADER.match(ct.readline())
        if not header:
            raise ValueError("Invalid mfold CT header.")
        energy = float(header[2])
        if not math.isfinite(energy):
            raise ValueError("Invalid mfold CT free energy.")
        if int(header[1]) != length:
            raise ValueError(f"mfold folded {header[1]} nt of a {length} nt sequence.")
        for i, base in enumerate(sequence, 1):
            cols = ct.readline().split()
            numbers = cols[:1] + cols[2:6]
            if len(cols) < 6 or not all(c.isascii() and c.isdecimal() for c in numbers):
                raise ValueError(f"Missing or invalid mfold CT row {i}.")
            index, prev, following, partner, number = map(int, numbers)
            if (index, prev, following, number) != (i, i - 1, i + 1 if i < length else 0, i):
                raise ValueError(f"Invalid nucleotide indices in mfold CT row {i}.")
            if cols[1].upper().replace("T", "U") != base:
                raise ValueError(f"mfold CT row {i} disagrees with the input sequence.")
            if partner == i or partner > length:
                raise ValueError(f"Invalid partner in mfold CT row {i}.")
            partners.append(partner)

    pairs, closing = [], []
    for i, j in enumerate(partners, 1):
        if j and partners[j - 1] != i:
            raise ValueError(f"Non-reciprocal pairing in mfold CT row {i}.")
        if j > i:
            pairs.append((i, j))
            closing.append(j)
        elif j and (not closing or closing.pop() != i):
            raise ValueError(f"Crossing pairs in mfold CT row {i}.")
    return energy, pairs


def _dot_bracket(length: int, pairs: Sequence[Tuple[int, int]]) -> str:
    dot = ["."] * length
    for i, j in pairs:
        dot[i - 1], dot[j - 1] = "(", ")"
    return "".join(dot)


def fold(sequence: str, params: Dict[str, Any], hard_pairs: Sequence[Tuple[int, int]] = (),
         executable: Optional[str] = None) -> Dict[str, Any]:
    """Return an MFE fold with the requested hard pairs in vienna.fold's format.

    Infeasible constraints raise ValueError so COMRADES can try another stem.
    Tool failures and malformed structures raise MfoldError.
    """
    sequence = checked_fold_text(sequence)
    if not sequence:
        raise ValueError("there is no sequence to fold")
    if params.get("min_loop", MIN_LOOP) != MIN_LOOP:
        raise ValueError(f"mfold's minimum hairpin loop is fixed at {MIN_LOOP}; set Minimum hairpin loop to {MIN_LOOP}.")
    if float(params.get("temperature", TEMPERATURE)) != TEMPERATURE:
        raise ValueError(f"mfold folds at {TEMPERATURE:g} °C only (its version 3.0 energies); "
                         f"set Temperature to {TEMPERATURE:g} or fold with ViennaRNA.")
    executable = str(Path(executable or find()).resolve())
    tables = data_dir(executable)
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="hyb2-mfold-") as name:
        directory = Path(name)
        # every table: another temperature recomputes them from the .dh/.dg files (newtemp)
        for table in tables.iterdir():
            if table.is_file():
                os.symlink(table, directory / table.name)
        # 60 per line: nafold (Fortran) finds no folding in a long single line
        lines = "".join(sequence[k:k + 60] + "\n" for k in range(0, len(sequence), 60))
        (directory / "s.seq").write_text(">s\n" + lines)
        args = [executable, "SEQ=s.seq", "NA=RNA", "MAX=1"]  # no T=: version 3.0 energies at 37 °C
        if hard_pairs:
            (directory / "s.aux").write_text("".join(f"F {i} {j} 1\n" for i, j in hard_pairs))
            args.append("AUX=s.aux")
        try:
            done = _run(args, cwd=directory, env=_env(executable))
        except OSError as error:
            raise MfoldUnavailable(f"mfold could not be started ({executable}): {error}") from error
        except subprocess.TimeoutExpired as error:
            raise MfoldError(f"mfold did not finish within {TIMEOUT_S // 60} minutes.") from error
        ct = directory / "s.ct"
        has_ct = ct.is_file() and ct.stat().st_size > 0
        if done.returncode or not has_ct:
            output = "\n".join((done.stdout or "", done.stderr or ""))
            if not has_ct and done.returncode in (0, 1) and re.search(r"\bno foldings?\b", output, re.IGNORECASE):
                # mfold 3.6 reports infeasible constraints this way. COMRADES can try the next stem.
                raise ValueError("mfold found no structure.")
            message = "mfold failed" if has_ct else "mfold did not produce a CT structure"
            if done.returncode:
                message += f" (exit status {done.returncode})"
            tail = " / ".join((done.stderr or done.stdout or "").strip().splitlines()[-2:])
            raise MfoldError(message + (f": {tail}" if tail else "."))
        try:
            energy, pairs = _pairs_from_ct(ct, sequence)
        except (OSError, ValueError) as error:
            raise MfoldError(str(error)) from error
    missing = set(hard_pairs) - set(pairs)
    if missing:
        raise ValueError("mfold's structure lacks hard pair(s) " + ", ".join(f"{i}-{j}" for i, j in sorted(missing)))
    return {
        "structure": _dot_bracket(len(sequence), pairs),
        "energy": round(energy, 2),
        "type": "constrained MFE" if hard_pairs else "MFE",
        "pairs": [(i, j, "()", None) for i, j in pairs],
        "hard_pairs": [list(p) for p in hard_pairs],
        "evidence": {},
        "seconds": {"bonus": 0.0, "fold": round(time.monotonic() - started, 2)},
    }
