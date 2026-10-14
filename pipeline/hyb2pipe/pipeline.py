"""``hyb2`` step 1 (FASTQ/FASTA/SAM -> .hyb) and ``sam2hyb`` in pure Python."""
from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable, Dict, List, Optional

from . import blast, collapse, energy, finalize, hybrids, inputs, sam

Progress = Callable[[str, str], None]


@dataclass
class Options:
    blast_threshold: float = 0.1  # -v
    max_overlap: float = 4  # -m / -g
    max_hits: int = 10  # -h (the legacy hyb2 wrapper never forwarded it)
    energy: bool = False  # -e
    threads: Optional[int] = None  # Bowtie2 threads and energy worker processes


@dataclass
class Result:
    hyb: Path
    homodimers: Path
    stats: Path
    sam: Optional[Path] = None
    reference: Optional[Path] = None
    counts: Dict[str, int] = field(default_factory=dict)


def _write(path: Path, lines: List[str]) -> Path:
    with open(path, "w", encoding="utf-8", newline="\n") as handle:
        for line in lines:
            handle.write(line + "\n")
    return path


def sam2hyb(sam_path: Path, reference: Path, out_prefix: Path, options: Options = Options(),
            progress: Progress = lambda stage, detail: None) -> Result:
    """Port of ``bin/sam2hyb`` (the SAM must already be ``sam_filt``-ed when
    it came from an external mapper, as ``hyb2`` does)."""
    counts: Dict[str, int] = {}
    progress("blast", "scoring alignments")
    blast_lines = list(sam.sam2blast(inputs.iter_lines(sam_path)))
    counts["alignments"] = len(blast_lines)
    progress("rank", "ranking reference genes")
    ranking = blast.reference_ranking(blast.mtophits(collapse.collapse_blast(blast_lines)))
    progress("hybrids", "calling chimeric reads")
    called = hybrids.mtop_hybrids(blast_lines, blast_threshold=options.blast_threshold,
                                  max_overlap=options.max_overlap, max_hits=options.max_hits)
    del blast_lines
    with_seq = hybrids.attach_sequences(called, inputs.iter_lines(sam_path))
    unique = hybrids.remove_duplicate_hybrids(ranking, with_seq)
    counts["hybrids_called"] = len(unique)
    if options.energy:
        progress("energy", "folding hybrid bits")
        unique = energy.add_energies(unique, inputs.iter_lines(reference), workers=options.threads)
    progress("annotate", "collapsing and annotating hybrids")
    final = finalize.add_chimera_types(finalize.add_overlaps(finalize.filter_homopolymers(collapse.collapse_hyb(unique))))
    counts["hybrids"] = len(final)
    out_prefix = Path(out_prefix)
    result = Result(
        hyb=_write(Path(str(out_prefix) + ".hyb"), final),
        homodimers=_write(Path(str(out_prefix) + "_homodimers.hyb"), finalize.homodimers(final)),
        stats=_write(Path(str(out_prefix) + "_hybrids.hyb_stats.txt"), finalize.hybrid_stats(final)),
        counts=counts,
    )
    progress("done", "%d hybrids" % len(final))
    return result


def run(input_path: Path, reference: Path, out_prefix: Path, options: Options = Options(),
        progress: Progress = lambda stage, detail: None, log=None) -> Result:
    """``hyb2 -i INPUT -d REFERENCE -o OUT`` step 1.  Outputs are written next
    to ``out_prefix``; a formatted reference copy goes there too if needed."""
    input_path, reference, out_prefix = Path(input_path), Path(reference), Path(out_prefix)
    out_prefix.parent.mkdir(parents=True, exist_ok=True)
    if inputs.needs_formatting(reference):
        progress("reference", "formatting reference FASTA")
        formatted = Path(str(out_prefix) + ".reference.hyb.fasta")
        lines = inputs.format_reference(inputs.iter_lines(reference))
        formatted.write_text("\n".join(lines), encoding="utf-8")
        reference = formatted
    name = input_path.name.lower()
    sam_path = Path(str(out_prefix) + ".sam")
    if name.endswith((".fastq.gz", ".fq.gz", ".fastq", ".fq", ".fasta", ".fa", ".fasta.gz", ".fa.gz")):
        index = inputs.bowtie2_index(reference, Path(str(out_prefix) + ".index"), log)
        reads = input_path
        if ".fastq" in name or ".fq" in name:
            progress("reads", "collapsing identical reads")
            reads = Path(str(out_prefix) + "_comp.fasta")
            _write(reads, inputs.collapse_reads(inputs.fastq_to_rows(inputs.iter_lines(input_path))))
        progress("map", "mapping reads with Bowtie2")
        inputs.bowtie2_map(reads, index, sam_path, threads=options.threads, log=log)
        if reads != input_path:
            reads.unlink()
    elif name.endswith(".sam"):
        progress("filter", "filtering SAM alignments")
        filtered = list(sam.sam_filt(inputs.iter_lines(input_path)))
        _write(sam_path, filtered)
        del filtered
    else:
        raise ValueError("input must be FASTQ(.gz), FASTA or SAM: %s" % input_path)
    result = sam2hyb(sam_path, reference, out_prefix, options, progress)
    result.sam, result.reference = sam_path, reference
    return result
