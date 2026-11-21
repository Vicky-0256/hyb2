"""``hyb2`` step 1 (FASTQ/FASTA/SAM -> .hyb) and ``sam2hyb`` in pure Python.

Every stage reads a file and writes a file, so memory does not grow with
the input (see ``Options.memory_mb``):

``collapse``  FASTQ -> collapsed FASTA (hash buckets + external merge)
``map``       Bowtie2 stdout (or the SAM input) is read once, line by line:
              ``sam2blast`` rows feed the streaming ``get_mtop_hybrids``
              caller, whose hybrids get the read's SAM sequence at once;
              the BLAST table is kept only when the reference has more than
              one sequence (gene ranking).  The SAM is not stored unless
              ``keep_sam`` asks for it.
``rank``      external ``collapse_blast`` + ``mtophits`` -> gene ranking
``hybrids``   ``remove_duplicate_hybrids`` (one read at a time)
``energy``    optional ``-e 1`` folding, in chunks
``annotate``  external ``collapse_hyb`` + the streaming annotation steps

``<out>.checkpoint.json`` records finished stages; a rerun with the same
input, reference and options continues after the last finished one.  The
energy settings guard only ``energy`` and ``annotate`` (``energy_key``): a
rerun that changes only them keeps the earlier stages.
"""
from __future__ import annotations

import gzip
import hashlib
import json
import os
import shutil
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Dict, Iterable, List, Optional, TextIO

from . import blast, collapse, energy, extsort, finalize, hybrids, inputs, sam

Progress = Callable[[str, str], None]
ProgressBytes = Callable[[str, int, int], None]

CHECKPOINT_VERSION = 1
# bump when a change alters what a stage writes, so old checkpoints are not reused
STAGE_FORMAT = "2026-10-14"
_FASTQ = (".fastq.gz", ".fq.gz", ".fastq", ".fq")
_FASTA = (".fasta", ".fa", ".fasta.gz", ".fa.gz")


def _env_int(name: str) -> Optional[int]:
    try:
        value = int(os.environ.get(name, ""))
    except ValueError:
        return None
    return value if value > 0 else None


def default_memory_mb() -> int:
    """1024 MB, capped at a quarter of ``$HYB2_TASK_MEM_MB`` (the desktop scheduler's grant)."""
    granted = _env_int("HYB2_TASK_MEM_MB")
    return max(64, min(1024, granted // 4)) if granted else 1024


def default_threads() -> int:
    """``$HYB2_TASK_CPUS`` (the desktop scheduler's grant), else all CPUs."""
    return _env_int("HYB2_TASK_CPUS") or os.cpu_count() or 1


@dataclass
class Options:
    blast_threshold: float = 0.1  # -v
    max_overlap: float = 4  # -m / -g
    max_hits: int = 10  # -h (the legacy hyb2 wrapper never forwarded it)
    energy: bool = False  # -e
    threads: Optional[int] = None  # Bowtie2 threads and worker processes (default: default_threads())
    keep_sam: str = "none"  # "none" | "gz" | "plain": also write <out>.sam(.gz)
    tmp_dir: Optional[Path] = None  # work and external-sort files (default: <out>.work/)
    memory_mb: int = field(default_factory=default_memory_mb)  # budget for in-memory sort chunks and count tables
    resume: bool = True  # continue from <out>.checkpoint.json when it matches
    energy_engine: str = "rnaduplex"  # "rnaduplex" (ViennaRNA, intermolecular as Hyb) or "rnacofold" (legacy hyb2)


@dataclass
class Result:
    hyb: Path
    homodimers: Path
    stats: Path
    sam: Optional[Path] = None
    reference: Optional[Path] = None
    counts: Dict[str, int] = field(default_factory=dict)
    # how the dG column was computed (None without energy): engine, program,
    # version, executable, parameters, scope (see energy.provenance)
    energy: Optional[Dict[str, Any]] = None
    # whether this invocation computed the dG column (False when it was
    # resumed past the energy stage, or energy is off)
    energy_ran: bool = False


def _recorded_energy(ck: "_Checkpoint", options: Options) -> Optional[Dict[str, Any]]:
    """Read the provenance from the energy stage that wrote the dG column."""
    if not options.energy:
        return None
    record = ck.data["stages"].get("energy") or {}
    recorded = record.get("provenance")
    # Older checkpoints did not record the executable or its version.
    return recorded if isinstance(recorded, dict) else energy.describe_engine(options.energy_engine)


class _NeedBlastTable(Exception):
    pass


def _threads(options: Options) -> int:
    return options.threads or default_threads()


def _fingerprint(path: Path) -> dict:
    stat = Path(path).stat()
    return {"path": str(Path(path).resolve()), "size": stat.st_size, "mtime": stat.st_mtime}


def _write_lines(path: Path, lines: Iterable[str]) -> Path:
    with open(path, "w", encoding="utf-8", newline="\n") as handle:
        for line in lines:
            handle.write(line + "\n")
    return path


# Stage order, and the work files each stage reads (to rerun a stage, the
# stages that wrote its inputs must still have them on disk).
_STAGES = ("collapse", "map", "rank", "hybrids", "energy", "annotate")
_NEEDS = {"map": ("comp.fasta",), "rank": ("blast.tsv",), "hybrids": ("called.tsv", "overrides.tsv", "ranking.txt"),
          "energy": ("unique.tsv",), "annotate": ("unique.tsv", "energy.tsv")}
# guarded by ``energy_key`` instead of the identity: changing the energy
# settings reruns only these
_ENERGY_STAGES = ("energy", "annotate")
_ENERGY_OPTIONS = ("energy", "energy_engine")


def _energy_key(options: Options) -> dict:
    return {"energy": bool(options.energy), "engine": options.energy_engine if options.energy else None}


def _old_energy_key(old: dict) -> dict:
    """The energy settings of a checkpoint written before ``energy_key`` (the
    energy flag was in the identity then; its only engine was RNAcofold)."""
    opts = old.get("options") or {}
    on = bool(opts.get("energy"))
    return {"energy": on, "engine": (opts.get("energy_engine") or "rnacofold") if on else None}


def _strip_energy(identity: dict) -> dict:
    out = dict(identity)
    if isinstance(out.get("options"), dict):
        out["options"] = {k: v for k, v in out["options"].items() if k not in _ENERGY_OPTIONS}
    return out


def _work_dir(out_prefix: Path, options: Options) -> Path:
    if options.tmp_dir is not None:
        digest = hashlib.sha1(str(out_prefix.resolve()).encode("utf-8")).hexdigest()[:12]
        return Path(options.tmp_dir) / ("hyb2pipe-" + digest)
    return Path(str(out_prefix) + ".work")


def _identity(source: str, input_path: Path, reference: Path, work: Path, options: Options) -> dict:
    """What a checkpoint must match to be resumed (energy settings aside)."""
    return {
        "input": _fingerprint(input_path), "reference": _fingerprint(reference),
        "options": {"blast_threshold": options.blast_threshold, "max_overlap": options.max_overlap,
                    "max_hits": options.max_hits, "keep_sam": options.keep_sam,
                    "source": source, "work": str(work.resolve()), "format": STAGE_FORMAT},
    }


def _energy_pending(ck: "_Checkpoint") -> bool:
    """Whether the energy stage still has to run (an energy-on run whose
    checkpoint finished neither it nor annotate, which comes after it)."""
    return not ck.done("annotate") and not ck.done("energy")


class _Checkpoint:
    def __init__(self, path: Optional[Path], identity: dict, resume: bool, energy_key: Optional[dict] = None,
                 save: bool = True) -> None:
        self.path = path
        self.data = {"version": CHECKPOINT_VERSION, **identity, "energy_key": energy_key, "stages": {}}
        if path is not None and resume and path.is_file():
            try:
                old = json.loads(path.read_text(encoding="utf-8"))
            except (OSError, ValueError):
                old = None
            if isinstance(old, dict) and old.get("version") == CHECKPOINT_VERSION:
                stripped = _strip_energy(old)
                if all(stripped.get(key) == value for key, value in identity.items()):
                    stages = old.get("stages") or {}
                    self.data["stages"] = stages if isinstance(stages, dict) else {}
                    previous = old["energy_key"] if "energy_key" in old else _old_energy_key(old)
                    if previous != energy_key:
                        self._drop(_ENERGY_STAGES)
        if save:
            self._save()

    def _intact(self, stage: str, name: str) -> bool:
        record = self.data["stages"].get(stage) or {}
        for item in record.get("outputs", []):
            if Path(item["path"]).name == name:
                path = Path(item["path"])
                return not item.get("released") and path.is_file() and path.stat().st_size == item["size"]
        return True  # not written by this stage

    def _drop(self, stages: Iterable[str]) -> None:
        """Forget ``stages``, and the earlier stages whose outputs they need
        but which are already released or gone."""
        records = self.data["stages"]
        dropped = set(stages)
        changed = True
        while changed:
            changed = False
            for stage in sorted(dropped, key=_STAGES.index):
                for name in _NEEDS.get(stage, ()):
                    for producer in _STAGES[:_STAGES.index(stage)]:
                        if producer in records and producer not in dropped and not self._intact(producer, name):
                            dropped.add(producer)
                            changed = True
        for stage in dropped:
            records.pop(stage, None)

    def _save(self) -> None:
        if self.path is None:
            return
        part = Path(str(self.path) + ".part")
        part.write_text(json.dumps(self.data, indent=1), encoding="utf-8")
        os.replace(part, self.path)

    def done(self, stage: str) -> Optional[dict]:
        """The stage's record if it finished and its kept outputs are intact."""
        record = self.data["stages"].get(stage)
        if not record or not record.get("done"):
            return None
        for item in record.get("outputs", []):
            if item.get("released"):
                continue
            path = Path(item["path"])
            if not path.is_file() or path.stat().st_size != item["size"]:
                return None
        return record

    def finish(self, stage: str, outputs: List[Path], **extra) -> None:
        self.data["stages"][stage] = {"done": True, "outputs": [{"path": str(p), "size": p.stat().st_size} for p in outputs],
                                      **extra}
        self._save()

    def release(self, path: Path) -> None:
        """Delete an intermediate file whose consumers have all finished."""
        for record in self.data["stages"].values():
            for item in record.get("outputs", []):
                if item["path"] == str(path):
                    item["released"] = True
        self._save()
        if path.exists():
            path.unlink()


def _open_sam_copy(path: Path, mode: str) -> TextIO:
    if mode == "gz":
        return gzip.open(path, "wt", compresslevel=3, encoding="utf-8", newline="\n")
    return open(path, "w", encoding="utf-8", newline="\n", buffering=1 << 20)


def _scan(lines: Iterable[str], work: Path, options: Options, *, unique_reads: bool, blast_table: Optional[bool],
          sam_copy: Optional[TextIO] = None, report: Callable[[str], None] = lambda key: None) -> Dict[str, int]:
    """One pass over SAM lines: ``sam2blast`` -> ``get_mtop_hybrids`` with
    each hybrid's read sequence (``getseqs_sam``) noted at once.

    Writes ``called.tsv`` (``SEQ \\0 hybrid`` per called hybrid, in the
    legacy order), ``overrides.tsv`` and, when ``blast_table`` is True (or
    None and the SAM header lists several references), ``blast.tsv``.

    ``getseqs_sam`` takes the SEQ of the *last* SAM line of a read.  When the
    reads are unique and grouped (Bowtie2 on our collapsed FASTA) that is the
    last line before the read's hybrids are called.  Otherwise a Bloom
    filter of reads with hybrids catches reads that reappear later, and
    their final SEQ goes to ``overrides.tsv``.
    """
    caller = hybrids.MtopCaller(blast_threshold=options.blast_threshold, max_overlap=options.max_overlap,
                                max_hits=options.max_hits)
    called = open(work / "called.tsv", "w", encoding="utf-8", newline="\n", buffering=1 << 20)
    table: Optional[TextIO] = None
    check = not unique_reads
    wanted = extsort.Bloom(32) if check else None
    overrides: Dict[str, str] = {}
    run_key: Optional[str] = None
    run_seq = ""
    cur_seq = ""
    dblen = 0
    headers_sq = 0
    alignments = 0
    count_called = 0
    genes: set = set()
    decided = blast_table is not None
    number = 0

    def emit(name: Optional[str], lines_out: List[str], seq: str) -> None:
        nonlocal count_called
        if wanted is not None:
            wanted.add(name or "")
        for item in lines_out:
            called.write(seq + "\0" + item + "\n")
        count_called += len(lines_out)

    try:
        for number, line in enumerate(lines, 1):
            if sam_copy is not None:
                sam_copy.write(line + "\n")
            li = line.strip()
            arr = li.split()
            key = arr[0] if arr else ""
            seq10 = arr[9] if len(arr) > 9 else ""
            if check:
                if key != run_key:
                    if run_key is not None and run_key in wanted:
                        overrides[run_key] = run_seq
                    run_key = key
                run_seq = seq10
            if not li.startswith("@"):
                row = sam.blast_row(arr, dblen)
                if row is not None:
                    alignments += 1
                    if not decided:
                        decided, blast_table = True, headers_sq != 1
                    gene = arr[2]
                    if gene not in genes:
                        genes.add(gene)
                        if len(genes) > 1 and not blast_table:
                            raise _NeedBlastTable()
                    if blast_table:
                        if table is None:
                            table = open(work / "blast.tsv", "w", encoding="utf-8", newline="\n", buffering=1 << 20)
                        table.write(row + "\n")
                    name = caller.name
                    out = caller.feed(row)
                    if out:
                        emit(name, out, cur_seq)
            elif li.startswith("@SQ"):
                dblen += int(li.split("LN:")[1])
                headers_sq += 1
            if key == caller.name:
                cur_seq = seq10
            if not number & 0xFFFF:
                report(key)
        if check and run_key is not None and run_key in wanted:
            overrides[run_key] = run_seq
        name = caller.name
        out = caller.finish()
        if out:
            emit(name, out, cur_seq)
    finally:
        called.close()
        if table is not None:
            table.close()
    _write_lines(work / "overrides.tsv", ("%s\0%s" % item for item in overrides.items()))
    return {"sam_lines": number, "alignments": alignments, "hybrids_raw": count_called, "genes": len(genes),
            "blast_table": int(table is not None)}


def _attached(work: Path) -> Iterable[str]:
    """``called.tsv`` with each hybrid's read sequence in column 2 (``txt2hyb.awk``)."""
    overrides: Dict[str, str] = {}
    for record in collapse.iter_file(work / "overrides.tsv"):
        key, value = record.split("\0", 1)
        overrides[key] = value
    for record in collapse.iter_file(work / "called.tsv"):
        seq, line = record.split("\0", 1)
        if overrides:
            seq = overrides.get(hybrids.awk.field(hybrids.awk.fields(line), 1), seq)
        attached = hybrids.attach_sequence(line, seq)
        if attached is not None:
            yield attached


def _annotate(collapsed: Path, out_prefix: Path) -> Dict[str, Path]:
    """Homopolymer filter, overlaps, chimera types, homodimers and stats, streaming."""
    paths = {"hyb": Path(str(out_prefix) + ".hyb"), "homodimers": Path(str(out_prefix) + "_homodimers.hyb"),
             "stats": Path(str(out_prefix) + "_hybrids.hyb_stats.txt")}
    parts = {key: Path(str(path) + ".part") for key, path in paths.items()}
    total = [0]
    with open(parts["hyb"], "w", encoding="utf-8", newline="\n") as hyb, \
            open(parts["homodimers"], "w", encoding="utf-8", newline="\n") as homo:
        def final_lines():
            chunk: List[str] = []
            source = collapse.iter_file(collapsed)
            while True:
                chunk = [line for _, line in zip(range(20000), source)]
                if not chunk:
                    return
                done = finalize.add_chimera_types(finalize.add_overlaps(finalize.filter_homopolymers(chunk)))
                for line in done:
                    hyb.write(line + "\n")
                for line in finalize.homodimers(done):
                    homo.write(line + "\n")
                total[0] += len(done)
                yield from done

        stats = finalize.hybrid_stats(final_lines())
    _write_lines(parts["stats"], stats)
    for key, path in paths.items():
        os.replace(parts[key], path)
    paths["count"] = total[0]  # type: ignore[assignment]
    return paths


def _process(source: str, input_path: Path, reference: Path, out_prefix: Path, options: Options, progress: Progress,
             progress_bytes: Optional[ProgressBytes], log, checkpointed: bool) -> Result:
    out_prefix.parent.mkdir(parents=True, exist_ok=True)
    original_reference = reference
    if inputs.needs_formatting(reference):
        progress("reference", "formatting reference FASTA")
        formatted = Path(str(out_prefix) + ".reference.hyb.fasta")
        lines = inputs.format_reference(inputs.iter_lines(reference))
        formatted.write_text("\n".join(lines), encoding="utf-8")
        reference = formatted
    if options.energy and options.energy_engine not in energy.ENGINES:
        raise ValueError("energy_engine must be one of %s: %r" % (", ".join(energy.ENGINES), options.energy_engine))
    if options.keep_sam not in ("none", "gz", "plain"):
        raise ValueError("keep_sam must be none, gz or plain: %r" % options.keep_sam)
    work = _work_dir(out_prefix, options)
    identity = _identity(source, input_path, original_reference, work, options)
    ck_path = Path(str(out_prefix) + ".checkpoint.json") if checkpointed else None
    ck = _Checkpoint(ck_path, identity, options.resume and checkpointed, _energy_key(options))
    # the dG folder is resolved only when the energy stage runs in this
    # invocation (a run resumed past it needs none), and then before mapping:
    # a run that cannot write dG stops at once.
    folder = None
    ran_energy = False
    if options.energy and _energy_pending(ck):
        folder = energy.engine_folder(options.energy_engine, _threads(options))
    if not ck.data["stages"] and work.exists():
        shutil.rmtree(work, ignore_errors=True)
    work.mkdir(parents=True, exist_ok=True)
    memory = max(1, options.memory_mb)
    threads = _threads(options)
    counts: Dict[str, int] = {}
    sam_kept: Optional[Path] = None

    finished = ck.done("annotate")
    if finished:
        counts = dict(finished.get("counts", {}))
        sam_info = (ck.done("map") or {}).get("sam")
        progress("done", "%d hybrids (from checkpoint)" % counts.get("hybrids", 0))
        shutil.rmtree(work, ignore_errors=True)
        return Result(hyb=Path(str(out_prefix) + ".hyb"), homodimers=Path(str(out_prefix) + "_homodimers.hyb"),
                      stats=Path(str(out_prefix) + "_hybrids.hyb_stats.txt"),
                      sam=Path(sam_info) if sam_info else None, reference=reference, counts=counts,
                      energy=_recorded_energy(ck, options))

    # ---- collapse (FASTQ only)
    reads = input_path
    if source == "fastq":
        record = ck.done("collapse")
        reads = work / "comp.fasta"
        if record:
            counts.update(record.get("counts", {}))
        else:
            progress("reads", "collapsing identical reads")
            counts.update(inputs.collapse_fastq(input_path, reads, work, memory_mb=memory, workers=threads,
                                                progress_bytes=progress_bytes))
            ck.finish("collapse", [reads], counts=dict(counts))

    # ---- map: Bowtie2 or SAM input -> called hybrids (+ BLAST table)
    record = ck.done("map")
    if record:
        counts.update(record.get("counts", {}))
        sam_kept = Path(record["sam"]) if record.get("sam") else None
    else:
        if source in ("fastq", "fasta"):
            index = inputs.bowtie2_index(reference, Path(str(out_prefix) + ".index"), log)
        blast_table: Optional[bool] = None
        while True:
            sam_copy = None
            sam_kept = None
            if options.keep_sam != "none" and source != "sam_raw":
                sam_kept = Path(str(out_prefix) + (".sam.gz" if options.keep_sam == "gz" else ".sam"))
                if source == "sam" and sam_kept.resolve() == input_path.resolve():
                    sam_kept = None  # never overwrite the file being read
            if sam_kept is not None:
                sam_copy = _open_sam_copy(Path(str(sam_kept) + ".part"), options.keep_sam)
            stream = None
            try:
                if source in ("fastq", "fasta"):
                    progress("map", "mapping reads with Bowtie2")
                    stream = inputs.Bowtie2Stream(reads, index, threads=threads, log=log)
                    lines: Iterable[str] = stream
                    total = reads.stat().st_size
                    unique = counts.get("unique_reads", 0)
                    throttle = inputs.Throttle(progress_bytes, "map")

                    def report(key: str) -> None:
                        # collapsed reads are named <rank>_<count> and Bowtie2 --reorder keeps their order
                        if unique and source == "fastq":
                            try:
                                throttle(total * int(key.split("_", 1)[0]) // unique, total)
                            except ValueError:
                                pass
                else:
                    progress("blast", "scoring alignments")
                    reader = inputs.ProgressReader(input_path)
                    stream = reader
                    lines = sam.sam_filt(reader) if source == "sam" else reader
                    throttle = inputs.Throttle(progress_bytes, "blast")

                    def report(key: str) -> None:
                        throttle(reader.position(), reader.total)
                scanned = _scan(lines, work, options, unique_reads=(source == "fastq"), blast_table=blast_table,
                                sam_copy=sam_copy, report=report)
            except _NeedBlastTable:
                blast_table = True
                continue
            finally:
                if stream is not None:
                    stream.close()
                if sam_copy is not None:
                    sam_copy.close()
            break
        if sam_kept is not None:
            os.replace(Path(str(sam_kept) + ".part"), sam_kept)
        counts["alignments"] = scanned["alignments"]
        outputs = [work / "called.tsv", work / "overrides.tsv"]
        if scanned["blast_table"]:
            outputs.append(work / "blast.tsv")
        if sam_kept is not None:
            outputs.append(sam_kept)
        ck.finish("map", outputs, counts={"alignments": scanned["alignments"]}, blast_table=scanned["blast_table"],
                  sam=str(sam_kept) if sam_kept else None)
        record = ck.done("map")
    if source == "fastq":
        ck.release(work / "comp.fasta")
    if source == "sam_raw":
        sam_kept = input_path

    # ---- rank (only with several reference sequences)
    ranking: List[str] = []
    if record.get("blast_table"):
        done = ck.done("rank")
        if done:
            ranking = (work / "ranking.txt").read_text(encoding="utf-8").splitlines()
        else:
            progress("rank", "ranking reference genes")
            collapse.collapse_file(work / "blast.tsv", work / "blast_collapsed.tsv", 13, collapse.blast_key, work, memory)
            ranking = blast.ranking_file(work / "blast_collapsed.tsv", work, memory)
            _write_lines(work / "ranking.txt", ranking)
            ck.finish("rank", [work / "ranking.txt"])
        ck.release(work / "blast_collapsed.tsv")
        ck.release(work / "blast.tsv")

    # ---- hybrids: one per read
    done = ck.done("hybrids")
    if done:
        counts.update(done.get("counts", {}))
    else:
        progress("hybrids", "calling chimeric reads")
        n = 0
        with open(work / "unique.tsv", "w", encoding="utf-8", newline="\n", buffering=1 << 20) as out:
            for line in hybrids.iter_unique_hybrids(ranking, _attached(work)):
                out.write(line + "\n")
                n += 1
        counts["hybrids_called"] = n
        ck.finish("hybrids", [work / "unique.tsv"], counts={"hybrids_called": n})
    ck.release(work / "called.tsv")
    unique_path = work / "unique.tsv"

    # ---- energy
    if options.energy:
        if not ck.done("energy"):
            if folder is None:
                # its record went stale during this run (an output vanished)
                folder = energy.engine_folder(options.energy_engine, threads)
            provenance = energy.provenance(options.energy_engine, folder=folder)
            ran_energy = True
            if options.energy_engine == "rnaduplex":
                progress("energy", "RNAduplex dG of each hybrid's arms")
                energy.add_line_energies_file(unique_path, work / "energy.tsv", inputs.iter_lines(reference), folder)
            else:
                progress("energy", "folding hybrid bits")
                energy.add_energies_file(unique_path, work / "energy.tsv", inputs.iter_lines(reference), work,
                                         folder=folder)
            ck.finish("energy", [work / "energy.tsv"], provenance=provenance)
        ck.release(unique_path)
        unique_path = work / "energy.tsv"

    # ---- annotate
    progress("annotate", "collapsing and annotating hybrids")
    collapse.collapse_file(unique_path, work / "collapsed.tsv", 2, collapse.hyb_key, work, memory)
    paths = _annotate(work / "collapsed.tsv", out_prefix)
    counts["hybrids"] = int(paths["count"])  # type: ignore[arg-type]
    ck.finish("annotate", [paths["hyb"], paths["homodimers"], paths["stats"]], counts=dict(counts))
    shutil.rmtree(work, ignore_errors=True)
    progress("done", "%d hybrids" % counts["hybrids"])
    return Result(hyb=paths["hyb"], homodimers=paths["homodimers"], stats=paths["stats"], sam=sam_kept,
                  reference=reference, counts=counts, energy=_recorded_energy(ck, options),
                  energy_ran=ran_energy)


def sam2hyb(sam_path: Path, reference: Path, out_prefix: Path, options: Options = Options(),
            progress: Progress = lambda stage, detail: None,
            progress_bytes: Optional[ProgressBytes] = None) -> Result:
    """Port of ``bin/sam2hyb`` (the SAM must already be ``sam_filt``-ed when
    it came from an external mapper, as ``hyb2`` does).  No checkpoint."""
    return _process("sam_raw", Path(sam_path), Path(reference), Path(out_prefix), options, progress, progress_bytes,
                    None, checkpointed=False)


def run(input_path: Path, reference: Path, out_prefix: Path, options: Options = Options(),
        progress: Progress = lambda stage, detail: None, log=None,
        progress_bytes: Optional[ProgressBytes] = None) -> Result:
    """``hyb2 -i INPUT -d REFERENCE -o OUT`` step 1.  Outputs are written next
    to ``out_prefix``; a formatted reference copy and the Bowtie2 index go
    there too.  ``progress_bytes(stage, done, total)`` reports input bytes
    consumed by the long stages."""
    input_path, reference, out_prefix = Path(input_path), Path(reference), Path(out_prefix)
    return _process(_source(input_path), input_path, reference, out_prefix, options, progress, progress_bytes, log,
                    checkpointed=True)


def _source(input_path: Path) -> str:
    name = Path(input_path).name.lower()
    if name.endswith(_FASTQ):
        return "fastq"
    if name.endswith(_FASTA):
        return "fasta"
    if name.endswith(".sam"):
        return "sam"
    raise ValueError("input must be FASTQ(.gz), FASTA or SAM: %s" % input_path)


def energy_stage_pending(input_path: Path, reference: Path, out_prefix: Path, options: Options = Options()) -> bool:
    """Whether ``run(input_path, reference, out_prefix, options)`` would run
    the energy stage now, i.e. needs its energy engine (ViennaRNA).  False
    when energy is off or ``<out_prefix>.checkpoint.json`` already finished
    the stage with the same energy settings.  Reads the checkpoint only."""
    if not options.energy:
        return False
    input_path, out_prefix = Path(input_path), Path(out_prefix)
    try:
        identity = _identity(_source(input_path), input_path, Path(reference), _work_dir(out_prefix, options), options)
    except (OSError, ValueError):
        return True
    ck = _Checkpoint(Path(str(out_prefix) + ".checkpoint.json"), identity, options.resume, _energy_key(options),
                     save=False)
    return _energy_pending(ck)
