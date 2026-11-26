"""Input preparation: ``fasta_hyb2_formatting``, collapsing FASTQ and FASTA
reads (``solexa2fasta.awk`` or ``mapping_fasta.awk``, then ``fasta2tab.awk`` +
``make_comp_fasta.pl``), and the Bowtie2 mapping step."""
from __future__ import annotations

import gzip
import hashlib
import heapq
import importlib.util
import io
import os
import re
import shlex
import shutil
import sys
import subprocess
import time
import zlib
from pathlib import Path
from typing import Callable, Dict, Iterable, Iterator, List, Optional, TextIO, Tuple

from . import awk

if __package__ == "hyb2pipe":  # standalone: PYTHONPATH=pipeline python -m hyb2pipe
    from hyb2analysis.reference import FASTA_NAMES, hyb2_reference_name, unindent_fasta_header, unique_fasta_id
else:
    from ..hyb2analysis.reference import FASTA_NAMES, hyb2_reference_name, unindent_fasta_header, unique_fasta_id

# gk-dev's mapping defaults (Kudla, U2 benchmark on the Brr2 CLASH data): with
# adapter trimming and these Bowtie2 settings 97% of the 2011 paper's U2
# chimeras are recovered vs 58% with the legacy -L 16 / --score-min L,18,0,
# mostly short (<= 20 nt) arms. Provisional, to be benchmarked on other data.
MAPPING_DEFAULTS = {
    "adapter": "TGGAATTCTCGGGTGCCAAGG",  # Illumina small-RNA 3' adapter (CLASH / CRAC); None skips trimming
    "min_length": 18,  # reads shorter than this after trimming are dropped
    "seed_length": 10,  # bowtie2 -L (legacy 16)
    "score_min": "L,15,0",  # bowtie2 --score-min (legacy L,18,0)
}


def bowtie2_options(seed_length: int = MAPPING_DEFAULTS["seed_length"], score_min: str = MAPPING_DEFAULTS["score_min"],
                    extra: Optional[str] = None) -> List[str]:
    """The Bowtie2 arguments of a run; *extra* (a shell-quoted string) is appended."""
    return (["-D", "20", "-R", "3", "-N", "0", "-L", str(seed_length), "-k", "20", "--local", "-i", "S,1,0.50",
             "--score-min", score_min, "--ma", "1", "--np", "0", "--mp", "2,2", "--rdg", "5,1", "--rfg", "5,1"]
            + shlex.split(extra or "") + ["--reorder"])


class MappingUnavailable(RuntimeError):
    pass


def open_text(path: Path) -> TextIO:
    if str(path).endswith(".gz"):
        return gzip.open(path, "rt", encoding="utf-8", errors="replace", newline="")
    return open(path, "r", encoding="utf-8", errors="replace", newline="")


def needs_formatting(fasta: Path) -> bool:
    """Check unique mapping IDs under the format selected by *all* headers."""
    ids = []
    formatted = False
    with open_text(fasta) as handle:
        for raw in handle:
            line = unindent_fasta_header(raw)
            if line.startswith(">"):
                formatted = formatted or FASTA_NAMES["trigger"] in line
                words = line[1:].split(None, 1)
                ids.append(words[0] if words else "")
    seen = set()
    for name in ids:
        unique_fasta_id(name, formatted, seen)
    return formatted


def format_reference(lines: Iterable[str]) -> List[str]:
    """Port of ``fasta_hyb2_formatting``: rename biotype separators, join
    sequence lines, and drop ``unknown``/``pseudogene`` records (GNU sed
    ``/re/,+1 d``).  Returns lines without newlines; the original wrote no
    final newline."""
    renamed = (hyb2_reference_name(unindent_fasta_header(raw.rstrip("\n"))) for raw in lines)
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


_SEQUENCE = re.compile(r"[A-Za-z*.\-\s]+")


def check_formatted(lines: List[str]) -> None:
    """Refuse output that is not one header and one sequence per record, with unique IDs."""
    start = 0
    while start < len(lines) and not lines[start].strip():
        start += 1
    lines = lines[start:]
    if not lines:
        raise ValueError("no sequences are left after formatting")
    seen = set()
    for number in range(0, len(lines), 2):
        header = lines[number]
        sequence = lines[number + 1] if number + 1 < len(lines) else ""
        words = header[1:].split(None, 1) if header.startswith(">") else []
        if not words:
            raise ValueError("expected a FASTA header with an ID: %r" % header[:80])
        if not sequence.strip() or sequence.startswith(">"):
            raise ValueError("record %s has no sequence" % words[0])
        if not _SEQUENCE.fullmatch(sequence):
            raise ValueError("record %s has a line that is not sequence (FASTA comment lines starting with ; or # "
                             "are not supported): %r" % (words[0], sequence[:80]))
        if words[0] in seen:
            raise ValueError("duplicate FASTA ID %r; give each reference record a unique ID" % words[0])
        seen.add(words[0])


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


def _index_files(prefix: Path) -> List[Path]:
    """The Bowtie2 index files ``<prefix>.*.bt2`` / ``.bt2l``."""
    if not prefix.parent.is_dir():
        return []
    start = prefix.name + "."
    return [path for path in prefix.parent.iterdir()
            if path.name.startswith(start) and path.name.endswith((".bt2", ".bt2l"))
            and path.name[len(start):].split(".")[0] in ("1", "2", "3", "4", "rev")]


def bowtie2_index(fasta: Path, prefix: Path, log: Optional[TextIO] = None) -> Path:
    """Build (or reuse) the index at ``prefix``.  ``<prefix>.sha1`` records the
    SHA-1 of the FASTA it was built from and is written only after a complete
    build, so an index of another reference or an interrupted build is rebuilt."""
    digest = hashlib.sha1()
    with open(fasta, "rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    stamp = Path(str(prefix) + ".sha1")
    try:
        built_from = stamp.read_text(encoding="utf-8").strip()
    except OSError:
        built_from = None
    if built_from == digest.hexdigest() and _index_files(prefix):
        return prefix
    # bowtie2-build is a Python wrapper; the -s binary works without it (Windows).
    exe = _tool("bowtie2-build-s", "bowtie2-build")
    if not exe:
        raise MappingUnavailable("bowtie2-build was not found; install Bowtie2 or provide a SAM file")
    stamp.unlink(missing_ok=True)
    part = Path(str(prefix) + ".part")
    for path in _index_files(part):
        path.unlink()
    subprocess.run([exe, str(fasta), str(part)], check=True, stdout=log or subprocess.DEVNULL, stderr=log or subprocess.DEVNULL)
    for path in _index_files(prefix):
        path.unlink()
    for path in _index_files(part):
        os.replace(path, path.with_name(prefix.name + path.name[len(part.name):]))
    stamp.write_text(digest.hexdigest() + "\n", encoding="utf-8")
    return prefix


def trim_adapters(reads: Path, out: Path, report: Path, adapter: str, min_length: int, threads: int) -> Path:
    """3' adapter trimming with cutadapt; *out* gets the format of *reads*
    (uncompressed) and *report* cutadapt's report."""
    # The desktop runtime bundles cutadapt as a package of its own Python.
    if importlib.util.find_spec("cutadapt"):
        command = [sys.executable, "-m", "cutadapt"]
    elif _tool("cutadapt"):
        command = [_tool("cutadapt")]
    else:
        raise MappingUnavailable("cutadapt not found: install it (conda install -c bioconda cutadapt) "
                                 "or switch adapter trimming off (--adapter none)")
    with open(report, "w", encoding="utf-8") as handle:
        code = subprocess.run(command + ["-a", adapter, "-m", str(min_length), "-j", str(threads), "-o", str(out),
                                         str(reads)], stdout=handle, stderr=subprocess.STDOUT).returncode
    text = report.read_text(encoding="utf-8", errors="replace")
    if code:
        lines = text.strip().splitlines()
        raise ValueError("cutadapt failed on %s: %s" % (reads, lines[-1] if lines else "exit status %d" % code))
    processed, written = (_report_count(text, label) for label in ("Total reads processed:", "Reads written (passing filters):"))
    if processed and not written:
        raise ValueError("no reads are left after adapter trimming: all %d reads of %s are shorter than %d nt "
                         "without the adapter (cutadapt report: %s); lower the minimum length or switch "
                         "trimming off (--adapter none)" % (processed, reads, min_length, report))
    return out


def _report_count(text: str, label: str) -> int:
    """A read count from cutadapt's report, such as ``Reads written (passing filters): 1,234 (93.1%)``."""
    match = re.search(re.escape(label) + r"\s*([0-9,]+)", text)
    return int(match.group(1).replace(",", "")) if match else 0


def iter_lines(path: Path) -> Iterator[str]:
    with open_text(path) as handle:
        for line in handle:
            yield line.rstrip("\r\n")


class ProgressReader:
    """``iter_lines`` that also reports how many bytes of the file (compressed
    bytes for .gz) have been read, for progress reporting."""

    def __init__(self, path: Path) -> None:
        self.path = Path(path)
        self.total = self.path.stat().st_size
        self._raw = open(self.path, "rb")
        stream = gzip.GzipFile(fileobj=self._raw, mode="rb") if str(path).endswith(".gz") else self._raw
        self._text = io.TextIOWrapper(stream, encoding="utf-8", errors="replace", newline="")

    def position(self) -> int:
        try:
            return self._raw.tell()
        except (OSError, ValueError):
            return 0

    def __iter__(self) -> Iterator[str]:
        try:
            for line in self._text:
                yield line.rstrip("\r\n")
        finally:
            self.close()

    def close(self) -> None:
        self._text.close()
        self._raw.close()


class Throttle:
    """Calls ``callback(stage, done, total)`` at most every ``interval`` seconds."""

    def __init__(self, callback: Optional[Callable[[str, int, int], None]], stage: str, interval: float = 0.5) -> None:
        self.callback, self.stage, self.interval = callback, stage, interval
        self.next = 0.0

    def __call__(self, done: int, total: int, force: bool = False) -> None:
        if self.callback is None:
            return
        now = time.monotonic()
        if force or now >= self.next:
            self.next = now + self.interval
            self.callback(self.stage, int(done), int(total))


def iter_fastq_rows(lines: Iterable[str], names=True) -> Iterator[Tuple[Optional[str], str]]:
    """Streaming ``fastq_to_rows``: (name, sequence) per row.  ``names`` may
    be a one-item list that the caller flips to False to skip building names
    (only barcoded collapsing reads them); the name is then None."""
    want = names if isinstance(names, list) else [names]
    it = iter(lines)
    for raw in it:
        if not raw.startswith("@"):
            continue
        line = raw.rstrip("\r\n")
        # solexa2fasta: '>' + the header's fields joined by single spaces
        name = " ".join(line.split()) if want[0] else None
        seq = next(it, None)
        next(it, None)
        next(it, None)
        if seq is None:
            # the header is printed twice and becomes two empty records
            yield name, ""
            yield name, ""
            continue
        seq = seq.rstrip("\r\n")
        # fasta2tab skips '' and '#' lines and starts a record at '>'
        if seq == "" or seq.startswith("#"):
            yield name, ""
        elif seq.startswith(">"):
            yield name, ""
            yield (seq[1:] if want[0] else None), ""
        else:
            yield name, seq


_NUCLEOTIDES = re.compile(r"[ACGTURYSWKMBDHVN]+", re.IGNORECASE)
# read names as collapsing writes them: <rank>_<count>, or <rank>-<barcodes>_<count>
_COLLAPSED_ID = re.compile(r"[0-9]+(-[0-9]+)?_[0-9]+")


def fasta_is_collapsed(path: Path) -> bool:
    """Whether every read of a FASTA file is already named ``<rank>_<count>``
    (written by ``collapse_fastq``, ``make_comp_fasta.pl`` or Hyb).  Such reads
    are mapped as they are, which keeps their counts."""
    found = False
    for line in iter_lines(path):
        if line.startswith(">"):
            words = line[1:].split()
            if not words or not _COLLAPSED_ID.fullmatch(words[0]):
                return False
            found = True
    return found


def iter_fasta_rows(lines: Iterable[str], names=True) -> Iterator[Tuple[str, str]]:
    """``mapping_fasta.awk -v reads=1 | fasta2tab.awk``: (header, sequence) per
    FASTA record, with the same checks and messages.  ``names`` is accepted
    for ``collapse_fastq`` and ignored."""
    def invalid(reason: str) -> ValueError:
        return ValueError("Invalid FASTA at line %d: %s" % (number, reason))

    name: Optional[str] = None
    parts: List[str] = []
    number = 0
    for number, raw in enumerate(lines, 1):
        line = raw.rstrip("\r\n")
        if line[:1] in ("#", ";") or not line.strip():
            continue
        if line.startswith(">"):
            if name is not None:
                if not parts:
                    raise invalid("record has no sequence")
                yield name, "".join(parts)
            name = line[1:].lstrip()
            if not name:
                raise invalid("missing record identifier")
            if "\t" in name:
                raise invalid("read name contains a tab")
            parts = []
        elif name is None:
            raise invalid("expected a record header")
        elif not _NUCLEOTIDES.fullmatch(line):
            raise invalid("expected unspaced nucleotide sequence")
        else:
            parts.append(line)
    if name is None or not parts:
        raise invalid("expected a nonempty record")
    yield name, "".join(parts)


_BARCODED = re.compile(r"^[^\t]*_[A-Z]*\t")
_MAX_COUNT = 10 ** 15
_ENTRY_BYTES = 250  # rough CPython cost of one dict entry with its key, beyond the sequence


def _rank_record(seq: str, count: int, barcoded: Optional[int]) -> str:
    """``collapse_reads``' sort key (-count[, -barcodes], line) as a plain string."""
    if barcoded is None:
        return "%015d%s\t%d" % (_MAX_COUNT - count, seq, count)
    return "%015d%015d%s\t%d\t%d" % (_MAX_COUNT - count, _MAX_COUNT - barcoded, seq, count, barcoded)


def _bucket_run(bucket: str, barcodes: bool, out: str) -> Tuple[str, int]:
    """Sum one bucket's spilled counts and write its sorted rank records."""
    counts: Dict[str, int] = {}
    if barcodes:
        pairs: Dict[Tuple[str, str], int] = {}
        with open(bucket, "r", encoding="utf-8", newline="\n") as handle:
            for record in handle:
                seq, code, count = record[:-1].rsplit("\0", 2)
                pairs[(seq, code)] = 1
                counts[seq] = counts.get(seq, 0) + int(count)
        barcoded: Dict[str, int] = {}
        for seq, _ in pairs:
            barcoded[seq] = barcoded.get(seq, 0) + 1
        del pairs
        records = [_rank_record(seq, count, barcoded[seq]) for seq, count in counts.items()]
    else:
        with open(bucket, "r", encoding="utf-8", newline="\n") as handle:
            for record in handle:
                seq, count = record[:-1].rsplit("\0", 1)
                counts[seq] = counts.get(seq, 0) + int(count)
        records = [_rank_record(seq, count, None) for seq, count in counts.items()]
    del counts
    records.sort()
    with open(out, "w", encoding="utf-8", newline="\n", buffering=1 << 20) as handle:
        for record in records:
            handle.write(record + "\n")
    os.unlink(bucket)
    return out, len(records)


def collapse_fastq(source: Path, out_fasta: Path, tmp_dir: Path, memory_mb: float = 1024, workers: int = 1,
                   progress_bytes: Optional[Callable[[str, int, int], None]] = None,
                   read_rows: Callable[..., Iterator[Tuple[Optional[str], str]]] = iter_fastq_rows) -> Dict[str, int]:
    """``collapse_reads(fastq_to_rows(...))`` written to ``out_fasta`` with
    bounded memory.  Counts are kept in a dict; when it outgrows half of
    ``memory_mb`` it is spilled into hash buckets on disk, each bucket is
    summed and rank-sorted on its own (in ``workers`` processes) and the
    sorted buckets are merged.  Output is identical to ``collapse_reads``.
    ``read_rows`` turns the file's lines into (name, sequence) rows."""
    reader = ProgressReader(source)
    report = Throttle(progress_bytes, "reads")
    budget = max(memory_mb, 1e-6) * 2 ** 20 / 2
    first = None
    want_names = [True]
    rows = read_rows(reader, names=want_names)
    for first in rows:
        break
    barcodes = first is not None and _BARCODED.match(first[0] + "\t" + first[1]) is not None
    want_names[0] = barcodes
    counts: Dict = {}
    size = 0
    reads = 0
    buckets: List[Path] = []
    spill_dir = Path(tmp_dir) / "collapse"

    def spill() -> None:
        nonlocal counts, size
        if not buckets:
            fraction = max(reader.position() / max(reader.total, 1), 1e-6)
            estimate = (size / fraction) * 1.2
            number = int(min(256, max(4, -(-estimate // budget))))
            spill_dir.mkdir(parents=True, exist_ok=True)
            buckets.extend(spill_dir / ("bucket%03d" % i) for i in range(number))
        handles = [open(path, "a", encoding="utf-8", newline="\n", buffering=1 << 18) for path in buckets]
        try:
            number = len(buckets)
            if barcodes:
                for (seq, code), count in counts.items():
                    handles[zlib.crc32(seq.encode("utf-8", "surrogatepass")) % number].write("%s\0%s\0%d\n" % (seq, code, count))
            else:
                for seq, count in counts.items():
                    handles[zlib.crc32(seq.encode("utf-8", "surrogatepass")) % number].write("%s\0%d\n" % (seq, count))
        finally:
            for handle in handles:
                handle.close()
        counts, size = {}, 0

    def chained():
        if first is not None:
            yield first
        yield from rows

    try:
        for name, seq in chained():
            reads += 1
            key = (seq, name.split("_")[-1]) if barcodes else seq
            count = counts.get(key)
            if count is None:
                counts[key] = 1
                size += len(seq) + _ENTRY_BYTES
                if size > budget:
                    spill()
            else:
                counts[key] = count + 1
            if not reads & 0xFFFF:
                report(reader.position(), reader.total)
    finally:
        reader.close()
    report(reader.total, reader.total, force=True)

    if buckets:
        spill()
        runs: List[str] = []
        jobs = [(str(path), barcodes, str(path) + ".sorted") for path in buckets if path.exists()]
        workers = max(1, min(workers, len(jobs), 4))
        if workers > 1:
            from concurrent.futures import ProcessPoolExecutor
            with ProcessPoolExecutor(max_workers=workers) as pool:
                runs = [path for path, _ in pool.map(_bucket_run, *zip(*jobs))]
        else:
            runs = [_bucket_run(*job)[0] for job in jobs]
        handles = [open(path, "r", encoding="utf-8", newline="\n") for path in runs]
        ranked: Iterable[str] = heapq.merge(*[(line[:-1] for line in handle) for handle in handles])
    else:
        handles = []
        if barcodes:
            per_seq: Dict[str, int] = {}
            per_bc: Dict[str, int] = {}
            for (seq, _), count in counts.items():
                per_seq[seq] = per_seq.get(seq, 0) + count
                per_bc[seq] = per_bc.get(seq, 0) + 1
            records = [_rank_record(seq, count, per_bc[seq]) for seq, count in per_seq.items()]
            del per_seq, per_bc
        else:
            records = [_rank_record(seq, count, None) for seq, count in counts.items()]
        counts = {}
        records.sort()
        ranked = records
    prefix = 30 if barcodes else 15
    unique = 0
    try:
        with open(out_fasta, "w", encoding="utf-8", newline="\n", buffering=1 << 20) as out:
            for unique, record in enumerate(ranked, 1):
                f = awk.fields(record[prefix:])
                if barcodes:
                    out.write(">%d-%s_%s\n%s\n" % (unique, awk.field(f, 3), awk.field(f, 2), awk.field(f, 1)))
                else:
                    out.write(">%d_%s\n%s\n" % (unique, awk.field(f, 2), awk.field(f, 1)))
    finally:
        for handle in handles:
            handle.close()
        if buckets:
            shutil.rmtree(spill_dir, ignore_errors=True)
    return {"reads": reads, "unique_reads": unique}


def collapse_fasta(source: Path, out_fasta: Path, tmp_dir: Path, **options) -> Dict[str, int]:
    """``collapse_fastq`` for FASTA reads, as ``bin/bowtie2_fasta2sam`` does:
    ``mapping_fasta.awk -v reads=1 | fasta2tab.awk | make_comp_fasta.pl``."""
    return collapse_fastq(source, out_fasta, tmp_dir, read_rows=iter_fasta_rows, **options)


class Bowtie2Stream:
    """Bowtie2 with its SAM output read line by line from a pipe."""

    def __init__(self, reads_fasta: Path, index: Path, *, threads: Optional[int] = None, log: Optional[TextIO] = None,
                 options: Optional[List[str]] = None) -> None:
        # The bowtie2 wrapper is Perl; call the aligner binary directly when present.
        exe = _tool("bowtie2-align-s", "bowtie2")
        if not exe:
            raise MappingUnavailable("bowtie2 was not found; install Bowtie2 or provide a SAM file")
        args = [exe] + (bowtie2_options() if options is None else options) + ["-p", str(threads or os.cpu_count() or 1), "-x", str(index), "-f", str(reads_fasta)]
        self.args = args
        self.proc = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=log or subprocess.DEVNULL)
        self._text = io.TextIOWrapper(self.proc.stdout, encoding="utf-8", errors="replace", newline="")

    def __iter__(self) -> Iterator[str]:
        for line in self._text:
            if not line.endswith("\n"):
                # Bowtie2 ends every SAM line with a newline; a cut-off last
                # line means it died mid-write, so report that, not the
                # parse error the partial record would cause downstream.
                code = self.proc.wait()
                if code:
                    raise subprocess.CalledProcessError(code, self.args)
            yield line.rstrip("\r\n")
        code = self.proc.wait()
        if code:
            raise subprocess.CalledProcessError(code, self.args)

    def close(self) -> None:
        if self.proc.poll() is None:
            self.proc.kill()
        self.proc.wait()
        self._text.close()
