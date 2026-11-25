"""Command line: the step-1 options of ``bin/hyb2``.

    python -m hyb2pipe -i reads.fastq.gz -d reference.fasta -o out/sample
"""
from __future__ import annotations

import argparse
import json
import math
import sys
from pathlib import Path

from .energy import EnergyUnavailable
from .inputs import MappingUnavailable
from .pipeline import Options, run


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog="hyb2pipe", add_help=False,
                                     description="FASTQ/FASTA/SAM to .hyb (pure-Python port of hyb2 step 1)")
    parser.add_argument("--help", action="help")
    parser.add_argument("-i", dest="input", required=True, type=Path, help="input FASTQ(.gz), FASTA(.gz) or SAM(.gz)")
    parser.add_argument("-d", dest="reference", required=True, type=Path, help="FASTA used for mapping")
    parser.add_argument("-o", dest="out", required=True, type=Path, help="output prefix")
    parser.add_argument("-v", dest="blast_threshold", type=float, default=0.1, help="BLAST threshold (0.1)")
    parser.add_argument("-m", dest="max_overlap", type=float, default=4, help="maximum overlap (4)")
    parser.add_argument("-h", dest="max_hits", type=int, default=10, help="maximum hits per sequence (10)")
    parser.add_argument("-e", dest="energy", type=int, choices=(0, 1), default=0, help="1 = add folding energy")
    parser.add_argument("--energy-engine", choices=("rnaduplex", "rnacofold"), default="rnaduplex",
                        help="dG with -e 1: rnaduplex (ViennaRNA, intermolecular pairs as Hyb; default) "
                             "or rnacofold (legacy hyb2)")
    parser.add_argument("-p", dest="threads", type=int, default=None, help="Bowtie2 threads")
    parser.add_argument("--keep-sam", choices=("none", "gz", "plain"), default="none",
                        help="also write <out>.sam (plain) or <out>.sam.gz (gz); default none")
    parser.add_argument("--tmp-dir", type=Path, default=None, help="directory for work and sort files (default <out>.work)")
    parser.add_argument("--memory-mb", type=int, default=None, help="memory budget for sorting and counting (default 1024)")
    parser.add_argument("--no-resume", action="store_true", help="ignore <out>.checkpoint.json and start over")
    parser.add_argument("--json-progress", action="store_true", help="print one JSON event per stage on stdout")
    args = parser.parse_args(argv)

    def progress(stage: str, detail: str) -> None:
        if args.json_progress:
            print(json.dumps({"type": "progress", "stage": stage, "detail": detail}), flush=True)
        else:
            print("[%s] %s" % (stage, detail), file=sys.stderr, flush=True)

    def progress_bytes(stage: str, done: int, total: int) -> None:
        if args.json_progress:
            print(json.dumps({"type": "progress", "stage": stage, "detail": "", "bytes_done": done,
                              "bytes_total": total}), flush=True)

    options = Options(blast_threshold=args.blast_threshold, max_overlap=args.max_overlap,
                      max_hits=args.max_hits, energy=bool(args.energy), threads=args.threads,
                      energy_engine=args.energy_engine,
                      keep_sam=args.keep_sam, tmp_dir=args.tmp_dir, resume=not args.no_resume)
    if args.memory_mb:
        options.memory_mb = args.memory_mb
    try:
        for flag, value in (("-v", args.blast_threshold), ("-m", args.max_overlap)):
            if not math.isfinite(value):
                raise ValueError("%s must be a finite number" % flag)
        result = run(args.input, args.reference, args.out, options, progress, log=sys.stderr,
                     progress_bytes=progress_bytes)
    # EOFError: a truncated .gz input
    except (EnergyUnavailable, MappingUnavailable, ValueError, OSError, EOFError) as error:
        if isinstance(error, EOFError):
            error = "%s: %s" % (args.input, error)
        if args.json_progress:
            print(json.dumps({"type": "error", "error": str(error)}), flush=True)
        else:
            print("error: %s" % error, file=sys.stderr)
        return 2
    for warning in _warnings(result, options.energy, args.input, args.reference):
        print("warning: %s" % warning, file=sys.stderr, flush=True)
    summary = {"type": "result", "hyb": str(result.hyb), "homodimers": str(result.homodimers),
               "stats": str(result.stats), "sam": str(result.sam) if result.sam else None, "counts": result.counts}
    print(json.dumps(summary) if args.json_progress else result.hyb)
    return 0


def _warnings(result, energy: bool, source: Path, reference: Path):
    """Results that are valid but likely not what was meant; the exit status stays 0."""
    if not result.counts.get("hybrids"):
        yield ("no hybrids found in %s. Check that it holds single-end chimeric reads (FASTQ or FASTA), "
               "or their alignments to the -d reference (SAM)." % source)
        return
    if not energy:
        return
    from .energy import reference_table
    from .inputs import format_reference, iter_lines

    known = set(reference_table(format_reference(iter_lines(reference))))
    missing = rows = 0
    absent = set()
    with open(result.hyb, encoding="utf-8") as hyb:
        for line in hyb:
            cols = line.rstrip("\n").split("\t")
            if line.startswith("#") or len(cols) < 10:
                continue
            rows += 1
            if cols[2] == ".":
                missing += 1
                absent.update(gene for gene in (cols[3], cols[9]) if gene not in known)
    if not missing:
        return
    message = "%d of %d hybrids have no dG ('.')." % (missing, rows)
    if absent:
        shown = sorted(absent)
        message += " These genes are not in the -d reference: %s%s." % (
            ", ".join(shown[:5]), " and %d more" % (len(shown) - 5) if len(shown) > 5 else "")
    message += (" A hybrid also has no dG when its arms form no duplex or when an arm maps antisense"
                " (or, with --energy-engine rnacofold, when the read ID does not key the legacy energy table).")
    yield message


if __name__ == "__main__":
    raise SystemExit(main())
