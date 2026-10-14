"""Optional folding energy (``sam2hyb -e 1``): ``hyb2fasta_bits_allRNAs.awk``,
``RNAcofold`` and ``add_dG_hyb_2.pl``.

Deliberate fix: the legacy ``sed 's/ /\\t/g'`` turned RNAcofold's ``( -9.50)``
into a dG value with an embedded TAB, shifting every later column for
|dG| < 10.  Here dG is always written as a plain ``%.2f`` number.
"""
from __future__ import annotations

import re
import shutil
import subprocess
from concurrent.futures import ProcessPoolExecutor
from typing import Callable, Dict, Iterable, List, Optional, Sequence, Tuple

from . import awk

Folder = Callable[[Sequence[Tuple[str, str]]], List[Optional[float]]]


class EnergyUnavailable(RuntimeError):
    pass


def reference_table(fasta_lines: Iterable[str]) -> Dict[str, str]:
    """``fasta2tab.awk`` followed by awk ``seq[$1] = $2`` (first header word)."""
    rows: List[List[str]] = []
    for raw in fasta_lines:
        line = raw.rstrip("\n")
        if line.startswith("#") or line == "":
            continue
        if line.startswith(">"):
            rows.append([line[1:] + "\t"])
        elif rows:
            rows[-1].append(line)
    table: Dict[str, str] = {}
    for row in rows:
        f = awk.fields("".join(row))
        table[awk.field(f, 1)] = awk.field(f, 2)
    return table


def bit_pairs(hyb_lines: Iterable[str], table: Dict[str, str]) -> List[Tuple[str, str, str]]:
    """(energy_key, bit1_seq, bit2_seq) for every sense/sense hybrid."""
    pairs = []
    for line in hyb_lines:
        f = awk.fields(line)
        get = lambda n: awk.field(f, n)
        if awk.compare(get(8), get(7)) < 0 or awk.compare(get(14), get(13)) < 0:
            continue
        s1 = awk.substr(table.get(get(4), ""), awk.to_num(get(7)), 1 + awk.to_num(get(8)) - awk.to_num(get(7)))
        s2 = awk.substr(table.get(get(10), ""), awk.to_num(get(13)), 1 + awk.to_num(get(14)) - awk.to_num(get(13)))
        # add_dG keys energies by the first two '_'-separated parts of the bit name
        key = re.match(r"^([^_]*_[^_]*)", "%s_%s_%s_%s" % (get(1), get(4), get(7), get(8)))
        pairs.append((key.group(1) if key else "", s1, s2))
    return pairs


def _vienna_chunk(pairs: Sequence[Tuple[str, str]]) -> List[float]:
    import RNA  # type: ignore
    return [RNA.fold_compound(a + "&" + b).mfe_dimer()[1] for a, b in pairs]


def default_folder(workers: Optional[int] = None) -> Folder:
    """ViennaRNA Python bindings (spread over ``workers`` processes when given), else an
    ``RNAcofold`` executable.  Each pair folds independently, so the result
    does not depend on the number of workers."""
    try:
        import RNA  # type: ignore  # noqa: F401

        def fold(pairs):
            pairs = list(pairs)
            # Opt-in: parallel speed-up is unmeasured on a quiet machine, so default to one process.
            count = max(1, min(workers or 1, len(pairs) // 200 or 1))
            if count == 1:
                return _vienna_chunk(pairs)
            size = -(-len(pairs) // (count * 4))
            chunks = [pairs[i:i + size] for i in range(0, len(pairs), size)]
            with ProcessPoolExecutor(max_workers=count) as pool:
                return [value for chunk in pool.map(_vienna_chunk, chunks) for value in chunk]
        return fold
    except ImportError:
        pass
    exe = shutil.which("RNAcofold")
    if exe:
        def fold(pairs):
            if not pairs:
                return []
            text = "".join(">p%d\n%s&%s\n" % (i, a, b) for i, (a, b) in enumerate(pairs))
            done = subprocess.run([exe, "--noconv", "--noPS"], input=text, capture_output=True, text=True, check=True)
            values = [float(m) for m in re.findall(r"\(\s*(-?\d+\.\d+)\)\s*$", done.stdout, re.M)]
            if len(values) != len(pairs):
                raise EnergyUnavailable("RNAcofold returned %d energies for %d pairs" % (len(values), len(pairs)))
            return values
        return fold
    raise EnergyUnavailable("folding energy needs the ViennaRNA Python package or RNAcofold on PATH")


def add_energies(hyb_lines: Sequence[str], fasta_lines: Iterable[str], folder: Optional[Folder] = None,
                 workers: Optional[int] = None) -> List[str]:
    folder = folder or default_folder(workers)
    pairs = bit_pairs(hyb_lines, reference_table(fasta_lines))
    energies = folder([(a, b) for _, a, b in pairs])
    dg: Dict[str, str] = {}
    for (key, _, _), value in zip(pairs, energies):
        if value is not None:
            dg[key] = "%.2f" % value
    out = []
    for line in hyb_lines:
        cols = line.split("\t")
        if cols[0].startswith("#") or not re.search(r"[a-zA-Z0-9]", cols[0]):
            out.append(line)
            continue
        value = dg.get(cols[0], "")
        if len(cols) < 3:
            cols += [""] * (3 - len(cols))
        cols[2] = value if re.search(r"[0-9]", value) else "."
        out.append("\t".join(cols))
    return out
