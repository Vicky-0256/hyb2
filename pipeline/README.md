# hyb2pipe — HYB2 step 1 in pure Python

`hyb2pipe` replaces the Perl/awk/bash chain behind `bin/hyb2` step 1
(`sam_filt`, `sam2hyb` and the `bowtie2_*2sam` wrappers), so the pipeline runs
anywhere CPython 3.9+ runs, including Windows, with no Perl, awk, sed or sort.

```bash
PYTHONPATH=pipeline python3 -m hyb2pipe -i reads.fastq.gz -d reference.fasta -o out/sample
PYTHONPATH=pipeline python3 -m hyb2pipe -i aligned.sam -d reference.fasta -o out/sample -e 1
```

Install the Python dependencies with `python -m pip install -r pipeline/requirements.txt`;
FASTQ and FASTA input also need Bowtie2 (see [External tools](#external-tools)).
Or install `hyb2pipe` and `hyb2analysis` as a package, with the `hyb2pipe`
command and no `PYTHONPATH`:
`python -m pip install ./third_party/cplfold ./pipeline` (cplfold is used by
hyb2analysis's IRIS and engine settings and is not on PyPI).

The `-i -d -o -v -m -h -e` flags match `hyb2` (`-v` and `-m` accept
scientific notation such as `1e-5` in both). One difference is on purpose:
with `-e 1`, `hyb2pipe` computes dG with RNAduplex and `hyb2` with RNAcofold;
`--energy-engine rnacofold` gives `hyb2`'s values (see Compatibility). The
desktop app and the web pages use RNAduplex too. The command writes
`<out>.hyb`, `<out>_homodimers.hyb` and `<out>_hybrids.hyb_stats.txt`, plus
`<out>.sam` or `<out>.sam.gz` with `--keep-sam plain|gz` (default: no SAM).
In the desktop app the same code runs as the `pipeline` task kind.

## Memory, temp files and resuming

Every stage reads one file and writes another, so memory does not grow with
the input: FASTQ and FASTA reads are counted in a table that spills to hash buckets,
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
  finished stage; `--no-resume` starts over. A rerun that changes only the
  energy settings (`-e`, `--energy-engine`) keeps the stages before `energy`
  while their work files are still there.
- `-p` (Bowtie2 threads and worker processes) defaults to
  `$HYB2_TASK_CPUS`, else all CPUs.
- With `--json-progress`, long stages also print
  `{"type": "progress", "stage", "bytes_done", "bytes_total"}` events.

The gene ranking used to break ties between a read's hybrids needs the whole
BLAST table; it is only built when the reference has more than one sequence
(with a single sequence the ranking cannot change the result).

`scripts/benchmarks/bench.py` measures wall time, peak RSS and temp disk of one run;
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
`tests/test_analysis.py` also checks `query.viewpoint` (gene and partner
`Zika_virusRNA`) against the legacy `plot_viewpoint` output for the same
`testData.hyb` (`golden/testData_Zika_virusRNA.gplot`, from the
`python-migration` branch's `fixtures/legacy_run/direct_viewpoint`): equal at
all 10,807 nucleotides.

Both `hyb2pipe` and `bin/sam_filt` keep only SAM headers and records with
flag 0, 16, 256 or 272 (single-end primary and secondary alignments), and
treat the rest the same way, with the same stderr messages:

- a record with the paired bit (0x1) is an error (Hyb2 expects single-end
  reads): `sam_filt` exits non-zero and `hyb2pipe` raises `ValueError`;
- unmapped records (0x4) are skipped and counted;
- supplementary alignments (0x800) are skipped and counted with a warning,
  because chimeric arms reported that way are not used; the mapper should
  report them as secondary alignments (for example `bwa mem -M`);
- any other flag (for example QC-fail 0x200, duplicate 0x400, or a flag that
  is not a decimal number) is skipped and counted with a warning.

Hyb2 takes the number of identical reads from the read name, `<rank>_<count>`
(for example `1_523`). Both `hyb2pipe` and `bin/hyb2` therefore collapse FASTA
reads before mapping, as they do FASTQ reads: identical reads become one read
named `<rank>_<count>`. FASTA reads that all have such a name already
(`<rank>_<count>` or `<rank>-<barcodes>_<count>`, as Hyb and Hyb2 write them)
are mapped as they are, which keeps their counts. This follows Hyb and the
`gk-dev` branch of hyb2. Before, raw FASTA reads kept their names: every count
was read as zero (`bin/hyb2` stopped with a division by zero), and reads named
with a leading `@` (`>@HWUSI-...`, as `solexa2fasta.awk` writes them) became
SAM lines that read as header lines, which ended in an empty `.hyb`.

A SAM input is not renamed, so its read names must carry the count. Both
programs stop with the same message when the first read name does not end in
`_<count>`, or when a read name starts with `@`; map the reads with Hyb2
instead. Names with colons (`a:b:c`) are still renamed to `...-a-b-c_1` by
`sam_filt`.

Before, `sam2blast_3` stopped at the first paired or supplementary record.
When `hyb2pipe` writes `<out>.hyb` it deletes `<out>.hyb.inputs`, the record
with which `bin/hyb2` decides whether its step 1 output is still current.
Both rename reference IDs by the same rules (`hyb2pipe/reference_cli.py`,
used by `bin/hyb2` and `bin/fasta_hyb2_formatting`).

It deliberately differs from the legacy scripts in these places:

- With `-e 1`, dG comes from ViennaRNA's RNAduplex (37 °C, pairs between
  the two arms only, the hybridisation step of the original Hyb pipeline)
  for each line's own two arms, so it does not depend on the read-name
  format. Arms that form no pair get an empty dG. `--energy-engine
  rnacofold` gives the legacy hyb2 behaviour (RNAcofold, keyed by the first
  two `_` fields of the read ID). The desktop app always runs with `-e 1`
  and RNAduplex. `Result.energy` and the checkpoint's `energy` stage record
  the engine, program, ViennaRNA version, parameters and scope. For a CLI run,
  `executable` is the path used to calculate dG and `version` comes from
  that program's `--version` output. For a Python run, `executable` is
  `None` and `version` comes from the module. Missing version information
  stays `None` on resume. With energy off, `Result.energy` is `None`.
- With `-e 1`, dG is written as a plain `%.2f` number. The legacy `sed`
  wrote a TAB inside the value whenever |dG| < 10, which shifted every later
  column.
- Bowtie2 runs with `--reorder`. Legacy multi-threaded runs wrote alignments
  in a different order on each run (the `bin/bowtie2_*2sam` wrappers now use
  `--reorder` too).
- When reference genes tie in the ranking, the first-seen gene wins. Perl's
  hash order made this random.

## External tools

- **Bowtie2:** needed for FASTQ and FASTA input only. The pipeline looks
  in `$HYB2PIPE_TOOL_DIR` first (the desktop bundle's `bin/`), then on the
  PATH. It prefers `bowtie2-align-s` / `bowtie2-build-s`, because the
  `bowtie2` wrapper needs Perl. The desktop runtime ships these binaries;
  see `desktop/scripts/acquire-bowtie2.py`.
- **ViennaRNA:** needed for `-e 1` (and so for every desktop sample run).
  The pipeline uses the Python `RNA` module, or `RNAduplex` (`RNAcofold`
  with `--energy-engine rnacofold`) on the PATH if the module is missing. A
  run without it stops before mapping; a run resumed past its `energy` stage
  (same energy settings) does not need it, and
  `energy_stage_pending(input, reference, out, options)` tells which, from
  the checkpoint alone. `Result.energy_ran` says whether this invocation
  computed the dG column. The desktop
  runtime bundles the ViennaRNA 2.7.2 wheel, which exists for Windows, macOS
  and Linux. ViennaRNA's licence allows free redistribution with credit; a
  commercial product needs the authors' permission. In-process RNAduplex
  (and cofolding) is spread over one worker process per CPU: about 13 s for
  the 10k hybrids in `testData.sam` on 10 workers. Cite Lorenz et al. (2011),
  ViennaRNA Package 2.0, Algorithms Mol Biol 6:26.

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
- `differential.compare(contacts, names, condition_one, out_dir)` is
  `hyb2_compare` without the plots: one count table over the samples'
  contact.txt cells, the DESeq2 Wald test (condition one vs two, rows with
  total count > `min_count`, alpha 0.01), `padj < 0.05` cells, and the greedy
  ±`range` merge into interactions (results.tsv, significant.tsv,
  interactions.tsv, summary.json). The legacy split's dummy (0, 0) cell is not
  reproduced.
- `deseq.deseq_two_conditions(counts, condition_one)` is a numpy port of
  DESeq2 1.46 for a two-level design (size factors, Cox-Reid gene-wise
  dispersions with the C++ line search, parametric trend with the locfit
  fallback, MAP shrinkage, IRLS with the optim fallback, Cook's filtering,
  independent filtering with R's `lowess`, BH). numba compiles the per-row
  loops when installed; without it the same code runs in plain Python. On all
  14 `reproduce_zikv/compare_*` runs the padj rank correlation with R is at
  least 0.9994 (1.0 on 13). The one gap is the prior variance of the log
  dispersions when there are 1-3 residual degrees of freedom: R estimates it
  from 1e4 random draws (`set.seed(2)`) and loess; here the draws are replaced
  by the exact distribution, which is the limit R approaches as the number of
  draws grows (`compare_BRA_human_vs_insect_min30`: 0.801 vs R's 0.825;
  54,088 of R's 54,120 significant cells shared).
