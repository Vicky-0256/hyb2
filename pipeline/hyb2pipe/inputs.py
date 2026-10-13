"""Input preparation: ``fasta_hyb2_formatting``, ``solexa2fasta.awk`` +
``fasta2tab.awk`` + ``make_comp_fasta.pl``, and the Bowtie2 mapping step."""
from __future__ import annotations

import gzip
import os
import re
import shutil
import subprocess
from pathlib import Path
from typing import Dict, Iterable, Iterator, List, Optional, TextIO

from . import awk

BOWTIE2_ARGS = ["-D", "20", "-R", "3", "-N", "0", "-L", "16", "-k", "20", "--local", "-i", "S,1,0.50",
                "--score-min", "L,18,0", "--ma", "1", "--np", "0", "--mp", "2,2", "--rdg", "5,1", "--rfg", "5,1", "--reorder"]


class MappingUnavailable(RuntimeError):
    pass


def open_text(path: Path) -> TextIO:
    if str(path).endswith(".gz"):
        return gzip.open(path, "rt", encoding="utf-8", errors="replace", newline="")
    return open(path, "r", encoding="utf-8", errors="replace", newline="")


def needs_formatting(fasta: Path) -> bool:
    with open_text(fasta) as handle:
        return any("|" in line for line in handle)


def format_reference(lines: Iterable[str]) -> List[str]:
    """Port of ``fasta_hyb2_formatting``: rename biotype separators, join
    sequence lines, and drop ``unknown``/``pseudogene`` records (GNU sed
    ``/re/,+1 d``).  Returns lines without newlines; the original wrote no
    final newline."""
    renamed = []
    for raw in lines:
        line = raw.rstrip("\n").replace("_", "-").replace("protein-coding", "mRNA").replace("|", "_")
        renamed.append(line.replace("__", "_unknown_"))
    # awk: first line + "\n", headers as "\n" + header + "\n", sequence lines unterminated
    text = "".join((line + "\n") if number == 0 else ("\n" + line + "\n") if line.startswith(">") else line
                   for number, line in enumerate(renamed))
    joined = text.split("\n")
    if joined and joined[-1] == "":
        joined.pop()
    out: List[str] = []
    ends: Dict[str, int] = {}
    for number, line in enumerate(joined, 1):
        deleted = False
        for name in ("unknown", "pseudogene"):
            end = ends.get(name)
            if end is not None and number <= end:
                deleted = True
                break
            ends.pop(name, None)
            if name in line:
                ends[name] = number + 1
                deleted = True
                break
        if not deleted:
            out.append(line)
    return out


def fastq_to_rows(lines: Iterable[str]) -> List[List[str]]:
    """``solexa2fasta.awk | fasta2tab.awk``: [name, sequence] per read."""
    it = iter(lines)
    fasta: List[str] = []
    for raw in it:
        line = raw.rstrip("\r\n")
        if not line.startswith("@"):
            continue
        f = awk.fields(line)
        f[0] = ">" + f[0]
        header = " ".join(f)
        fasta.append(header)
        seq = next(it, None)
        fasta.append(header if seq is None else seq.rstrip("\r\n"))
        next(it, None)
        next(it, None)
    rows: List[List[str]] = []
    for line in fasta:
        if line.startswith("#") or line == "":
            continue
        if line.startswith(">"):
            rows.append([line[1:], ""])
        elif rows:
            rows[-1][1] += line
    return rows


def collapse_reads(rows: List[List[str]]) -> List[str]:
    """Port of ``make_comp_fasta.pl``: unique sequences named ``rank_count``."""
    barcodes = bool(rows) and re.match(r"^[^\t]*_[A-Z]*\t", rows[0][0] + "\t" + rows[0][1]) is not None
    counts: Dict[str, int] = {}
    barcoded: Dict[str, int] = {}
    seen = set()
    for name, seq in rows:
        counts[seq] = counts.get(seq, 0) + 1
        if barcodes:
            code = name.split("_")[-1]
            if (seq, code) not in seen:
                seen.add((seq, code))
                barcoded[seq] = barcoded.get(seq, 0) + 1
    if barcodes:
        lines = ["%s\t%d\t%d" % (seq, counts[seq], barcoded[seq]) for seq in counts]
        lines.sort(key=lambda line: (-counts[line.split("\t")[0]], -barcoded[line.split("\t")[0]], line.encode()))
    else:
        lines = ["%s\t%d" % (seq, counts[seq]) for seq in counts]
        lines.sort(key=lambda line: (-counts[line.split("\t")[0]], line.encode()))
    out = []
    for number, line in enumerate(lines, 1):
        f = awk.fields(line)
        if barcodes:
            out += [">%d-%s_%s" % (number, awk.field(f, 3), awk.field(f, 2)), awk.field(f, 1)]
        else:
            out += [">%d_%s" % (number, awk.field(f, 2)), awk.field(f, 1)]
    return out


def _tool(*names: str) -> Optional[str]:
    """``HYB2PIPE_TOOL_DIR`` (the desktop bundle's ``bin/``) first, then PATH."""
    bundled = os.environ.get("HYB2PIPE_TOOL_DIR")
    for name in names:
        if bundled:
            found = shutil.which(name, path=bundled)
            if found:
                return found
    for name in names:
        found = shutil.which(name)
        if found:
            return found
    return None


def bowtie2_index(fasta: Path, prefix: Path, log: Optional[TextIO] = None) -> Path:
    if Path(str(prefix) + ".1.bt2").exists() or Path(str(prefix) + ".1.bt2l").exists():
        return prefix
    # bowtie2-build is a Python wrapper; the -s binary works without it (Windows).
    exe = _tool("bowtie2-build-s", "bowtie2-build")
    if not exe:
        raise MappingUnavailable("bowtie2-build was not found; install Bowtie2 or provide a SAM file")
    subprocess.run([exe, str(fasta), str(prefix)], check=True, stdout=log or subprocess.DEVNULL, stderr=log or subprocess.DEVNULL)
    return prefix


def bowtie2_map(reads_fasta: Path, index: Path, sam: Path, *, threads: Optional[int] = None, log: Optional[TextIO] = None) -> None:
    # The bowtie2 wrapper is Perl; call the aligner binary directly when present.
    exe = _tool("bowtie2-align-s", "bowtie2")
    if not exe:
        raise MappingUnavailable("bowtie2 was not found; install Bowtie2 or provide a SAM file")
    args = [exe] + BOWTIE2_ARGS + ["-p", str(threads or os.cpu_count() or 1), "-x", str(index), "-f", str(reads_fasta)]
    with open(sam, "w", encoding="utf-8", newline="\n") as out:
        subprocess.run(args, check=True, stdout=out, stderr=log or subprocess.DEVNULL)


def iter_lines(path: Path) -> Iterator[str]:
    with open_text(path) as handle:
        for line in handle:
            yield line.rstrip("\r\n")
