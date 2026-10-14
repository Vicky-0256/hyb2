# hyb2pipe — HYB2 step 1 in pure Python

`hyb2pipe` replaces the Perl/awk/bash chain behind `bin/hyb2` step 1
(`sam_filt`, `sam2hyb` and the `bowtie2_*2sam` wrappers), so the pipeline runs
anywhere CPython 3.9+ runs, including Windows, with no Perl, awk, sed or sort.

```bash
PYTHONPATH=pipeline python3 -m hyb2pipe -i reads.fastq.gz -d reference.fasta -o out/sample
PYTHONPATH=pipeline python3 -m hyb2pipe -i aligned.sam -d reference.fasta -o out/sample -e 1
```

The `-i -d -o -v -m -h -e` flags match `hyb2`. The command writes
`<out>.hyb`, `<out>_homodimers.hyb` and `<out>_hybrids.hyb_stats.txt`, plus
`<out>.sam` or `<out>.sam.gz` with `--keep-sam plain|gz` (default: no SAM).
In the desktop app the same code runs as the `pipeline` task kind.

## Memory, temp files and resuming

Every stage reads one file and writes another, so memory does not grow with
the input: FASTQ reads are counted in a table that spills to hash buckets,
Bowtie2's output is piped straight into hybrid calling one read at a time,
and the collapse steps are external sorts (sorted chunks merged with
`heapq.merge`, same keys and tie-breaks as `sort -kN`).

- `--memory-mb N` (default 1024, or a quarter of `$HYB2_TASK_MEM_MB`) sizes
  the in-memory chunks; peak RSS is about 0.5 GB for the Python process
  plus Bowtie2.
- `--tmp-dir DIR` holds work and sort files (default `<out>.work/`). They
  peak at about twice the size of the collapsed reads FASTA (7.7 GB for
  the 33M-read ZAFR_REP1) and are deleted at the end.
- `<out>.checkpoint.json` records finished stages. Rerunning the same
  command (same input, reference and options) continues after the last
  finished stage; `--no-resume` starts over.
- `-p` (Bowtie2 threads and worker processes) defaults to
  `$HYB2_TASK_CPUS`, else all CPUs.
- With `--json-progress`, long stages also print
  `{"type": "progress", "stage", "bytes_done", "bytes_total"}` events.

The gene ranking used to break ties between a read's hybrids needs the whole
BLAST table; it is only built when the reference has more than one sequence
(with a single sequence the ranking cannot change the result).

`bench/bench.py` measures wall time, peak RSS and temp disk of one run;
`tests/test_zafr_regression.py` checks a 2M-read ZAFR_REP1 subset against
a golden made before the streaming rewrite (needs `zika_files/`).

| reads (ZAFR_REP1) | before: time / peak RSS | now: time / peak RSS (Python, Python + Bowtie2) / temp disk |
|---|---|---|
| 2 M | 71 s / 1.7 GB | 41 s / 0.40, 0.85 GB / 0.5 GB |
| 8 M | 258 s / 7.1 GB | 160 s / 0.50, 1.7 GB / 2.0 GB |
| 32.9 M (whole `.fastq.gz`) | ≈ 18 min / ≈ 29 GB (extrapolated) | 662 s / 0.50, 1.7 GB / 7.7 GB |

10-core M-series Mac, bundled runtime, Bowtie2 on all cores. Bowtie2 takes
most of the time (≈ 550 s of the full run); its RSS stays near 230 MB. The
Python + Bowtie2 peak is during FASTQ collapsing, when four bucket workers
run at once. The whole-sample `.hyb` has the same 224,154 lines as the
legacy `reproduce_zikv/results_combined/ZAFR_REP1.hyb`; only their order
differs (the legacy run had no `--reorder`).

## Compatibility

`tests/test_pipeline.py` checks that `data/testData.sam` gives byte-identical
output to the legacy scripts, which were run with macOS one-true-awk 20200816
and `LANG=C.UTF-8`. The port reproduces the legacy quirks that decide which
duplicate line is kept: awk hash iteration order, `sort` last-resort ordering,
and the collapse scripts dropping each group's first line.

It deliberately differs from the legacy scripts in four places:

- `-h` (maximum hits) is honoured. The legacy `hyb2` wrapper never passed it
  on to `sam2hyb`.
- With `-e 1`, dG is written as a plain `%.2f` number. The legacy `sed`
  wrote a TAB inside the value whenever |dG| < 10, which shifted every later
  column.
- Bowtie2 runs with `--reorder`. Legacy multi-threaded runs wrote alignments
  in a different order on each run.
- When reference genes tie in the ranking, the first-seen gene wins. Perl's
  hash order made this random.

## External tools

- **Bowtie2:** needed for FASTQ and FASTA input only. The pipeline looks
  in `$HYB2PIPE_TOOL_DIR` first (the desktop bundle's `bin/`), then on the
  PATH. It prefers `bowtie2-align-s` / `bowtie2-build-s`, because the
  `bowtie2` wrapper needs Perl. The desktop runtime ships these binaries;
  see `desktop/scripts/acquire-bowtie2.py`.
- **ViennaRNA:** needed only for `-e 1`. The pipeline uses the Python `RNA`
  module, or `RNAcofold` on the PATH if the module is missing. The desktop
  runtime bundles the ViennaRNA 2.7.2 wheel, which exists for Windows, macOS
  and Linux. ViennaRNA's licence allows free redistribution with credit; a
  commercial product needs the authors' permission. In-process cofolding
  is spread over one worker process per CPU: about 12 s for the 10k hybrids
  in `testData.sam`, against 63 s in a single process.

Steps 2 and 3 of `hyb2` (contact maps, viewpoint, folding) are already
provided by the web and desktop analysis pages. `hyb2_compare` (R/DESeq2) has
not been ported.

## Analysis backend (`hyb2analysis`)

`hyb2analysis` (needs numpy) serves the desktop analysis pages from large
.hyb files without loading them into the WebView:

- `coverage.write_contact(hyb, gene_id, out)` writes `hyb2_coverage`'s
  `contact.txt` byte-for-byte (one-true-awk `for (k in a)` order included);
  `coverage.write_for_table(contact, out, name)` writes `hyb2_compare`'s
  forTable. Both stream; memory depends on gene length, not file size.
- `index.build(hyb, index_path)` writes a per-sample SQLite index (hybrid
  coordinates, read counts, dG, type, and per gene pair the distinct arm
  rectangles that every contact map is drawn from).
- `query.summary / contact_bins / hybrids / top_interactions` accept a list
  of indexes and sum replicates.
