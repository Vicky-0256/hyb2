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
import os
import re
import shutil
import subprocess
import tempfile
import time
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence, Tuple

ENV = "HYB2_MFOLD"
INSTALL = "conda create -n mfold -c conda-forge -c bioconda mfold"
# where mfold is usually installed (conda environments, Homebrew, /usr/local)
PREFIXES = ("~/miniforge3", "~/mambaforge", "~/miniconda3", "~/anaconda3", "~/opt/miniconda3", "~/opt/anaconda3",
            "/opt/conda", "/opt/homebrew", "/opt/homebrew/Caskroom/miniforge/base", "/usr/local")
MIN_LOOP = 3
TEMPERATURE = 37.0
# mfold is slower than ViennaRNA and refolds once per accepted stem
MAX_MFOLD_NT = 3000
TIMEOUT_S = 3600


class MfoldUnavailable(RuntimeError):
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


def _pairs_from_ct(path: Path) -> Tuple[float, int, List[Tuple[int, int]]]:
    """The first structure of an mfold .ct: its energy, length and pairs (i < j)."""
    lines = path.read_text(errors="replace").splitlines()
    if not lines:
        raise ValueError("mfold wrote an empty structure file.")
    header = lines[0].split()
    length = int(header[0])
    match = re.search(r"dG\s*=\s*(-?\d+(?:\.\d+)?)", lines[0])
    if not match:
        raise ValueError("mfold's structure file has no free energy.")
    pairs = []
    for line in lines[1:length + 1]:
        cols = line.split()
        i, j = int(cols[0]), int(cols[4])
        if j > i:
            pairs.append((i, j))
    return float(match.group(1)), length, pairs


def _dot_bracket(length: int, pairs: Sequence[Tuple[int, int]]) -> str:
    dot = ["."] * length
    for i, j in pairs:
        dot[i - 1], dot[j - 1] = "(", ")"
    return "".join(dot)


def fold(sequence: str, params: Dict[str, Any], hard_pairs: Sequence[Tuple[int, int]] = (),
         executable: Optional[str] = None) -> Dict[str, Any]:
    """One mfold MFE fold with ``hard_pairs`` forced; a record in the shape of
    vienna.fold's.  Raises ValueError when mfold's structure lacks a hard
    pair (it cannot satisfy them all), MfoldUnavailable without mfold."""
    sequence = sequence.upper().replace("T", "U")
    if not sequence:
        raise ValueError("there is no sequence to fold")
    if set(sequence) - set("ACGU"):
        raise ValueError("mfold folds A, C, G and U only; the sequence holds other letters")
    if params.get("min_loop", MIN_LOOP) != MIN_LOOP:
        raise ValueError(f"mfold's minimum hairpin loop is fixed at {MIN_LOOP}; set Minimum hairpin loop to {MIN_LOOP}.")
    if float(params.get("temperature", TEMPERATURE)) != TEMPERATURE:
        raise ValueError("mfold folds at 37 °C only (its version 3.0 energies); set Temperature to 37 or fold with ViennaRNA.")
    executable = executable or find()
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
            done = subprocess.run(args, cwd=directory, stdin=subprocess.DEVNULL, capture_output=True, text=True,
                                  errors="replace", timeout=TIMEOUT_S, env=_env(executable))
        except OSError as error:
            raise MfoldUnavailable(f"mfold could not be started ({executable}): {error}") from error
        except subprocess.TimeoutExpired as error:
            raise ValueError(f"mfold did not finish within {TIMEOUT_S // 60} minutes.") from error
        ct = directory / "s.ct"
        if not ct.is_file() or ct.stat().st_size == 0:
            tail = " / ".join((done.stdout or "").strip().splitlines()[-2:])
            raise ValueError("mfold found no structure" + (f" ({tail})" if tail else "") + ".")
        energy, length, pairs = _pairs_from_ct(ct)
    if length != len(sequence):
        raise ValueError(f"mfold folded {length} nt of a {len(sequence)} nt sequence.")
    missing = set(hard_pairs) - set(pairs)
    if missing:
        raise ValueError("mfold's structure lacks hard pair(s) " + ", ".join(f"{i}-{j}" for i, j in sorted(missing)))
    return {
        "structure": _dot_bracket(length, pairs),
        "energy": round(energy, 2),
        "type": "constrained MFE" if hard_pairs else "MFE",
        "pairs": [(i, j, "()", None) for i, j in pairs],
        "hard_pairs": [list(p) for p in hard_pairs],
        "evidence": {},
        "seconds": {"bonus": 0.0, "fold": round(time.monotonic() - started, 2)},
    }
