# hyb2pipe — HYB2 step 1 in pure Python

`hyb2pipe` is HYB2's step 1: it trims the adapter and collapses identical
reads, maps them with Bowtie2, filters the alignments and calls the hybrids,
with their dG on request. It runs anywhere CPython 3.9+ runs, including
Windows, with no Perl, awk, sed or sort.

```bash
PYTHONPATH=pipeline python3 -m hyb2pipe -i reads.fastq.gz -d reference.fasta -o out/sample
PYTHONPATH=pipeline python3 -m hyb2pipe -i aligned.sam -d reference.fasta -o out/sample -e 1
```

Install the Python dependencies with `python -m pip install -r pipeline/requirements.txt`;
FASTQ and FASTA input also need Bowtie2 (see [External tools](#external-tools)).
Or install `hyb2pipe` and `hyb2analysis` as a package, with the `hyb2pipe`
and `hyb2analysis` commands and no `PYTHONPATH`:
`python -m pip install ./third_party/cplfold ./pipeline` (cplfold is used by
hyb2analysis's IRIS and engine settings and is not on PyPI).

`-v` and `-m` accept scientific notation such as `1e-5`. With `-e 1`, dG
comes from RNAduplex, as in the desktop app and the web pages;
`--energy-engine rnacofold` gives the RNAcofold values of the original hyb2
(see [Differences from the original scripts](#differences-from-the-original-scripts)).
The command writes `<out>.hyb`, `<out>_homodimers.hyb` and
`<out>_hybrids.hyb_stats.txt`, plus `<out>.sam` or `<out>.sam.gz` with
`--keep-sam plain|gz` (default: no SAM). Invalid input or options stop it
with exit status 2 and a one-line message. In the desktop app the same code
runs as the `pipeline` task kind.

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

## Golden files

`tests/test_pipeline.py` checks that `data/testData.sam` gives byte-identical
output to golden files made with the original HYB2 scripts (`sam_filt` and
`sam2hyb -e 0`, macOS one-true-awk 20200816, `LANG=C.UTF-8`). hyb2pipe keeps
the quirks that decide which duplicate line is kept: awk hash iteration
order, `sort` last-resort ordering, and the collapse steps dropping each
group's first line. `tests/test_analysis.py` checks `query.viewpoint` (gene
and partner `Zika_virusRNA`) against `golden/testData_Zika_virusRNA.gplot`,
the original `plot_viewpoint` profile of the same `testData.hyb` (from the
`python-migration` branch's `fixtures/legacy_run/direct_viewpoint`): equal at
all 10,807 nucleotides.

## SAM records, read names and reference IDs

`hyb2pipe` keeps only SAM headers and records with flag 0, 16, 256 or 272
(single-end primary and secondary alignments):

- a record with the paired bit (0x1) is an error: Hyb2 expects single-end
  reads;
- unmapped records (0x4) are skipped and counted on stderr;
- supplementary alignments (0x800) are skipped and counted with a warning,
  because chimeric arms reported that way are not used; the mapper should
  report them as secondary alignments (for example `bwa mem -M`);
- any other flag (for example QC-fail 0x200, duplicate 0x400, or a flag that
  is not a decimal number) is skipped and counted with a warning.

Hyb2 takes the number of identical reads from the read name, `<rank>_<count>`
(for example `1_523`), so `hyb2pipe` collapses FASTA reads before mapping, as
it does FASTQ reads: identical reads become one read named `<rank>_<count>`.
FASTA reads that all have such a name already (`<rank>_<count>` or
`<rank>-<barcodes>_<count>`, as Hyb and Hyb2 write them) are mapped as they
are, which keeps their counts (as Hyb and the `gk-dev` branch of hyb2 do).

A SAM input is not renamed, so its read names must carry the count:
`hyb2pipe` stops when the first read name does not end in `_<count>`, or when
a read name starts with `@` (its lines would read as SAM header lines); give
it the reads instead. A read name with colons (`a:b:c:d:e`) becomes its last
three fields joined by `-`, with `_1` (`c-d-e_1`).

When a header of the reference FASTA contains `|`, `hyb2pipe` maps to a copy,
`<out>.reference.hyb.fasta`, whose headers are rewritten in this order: `_`
to `-`, `protein-coding` to `mRNA`, `|` to `_`, `__` to `_unknown_`
(`>Zika|virusRNA` becomes `>Zika_virusRNA`); records whose header then
contains `unknown` or `pseudogene` are left out. The `.hyb` uses these IDs.

## Differences from the original scripts

`hyb2pipe` deliberately differs from the original HYB2 scripts in these places:

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
- Bowtie2 runs with `--reorder`, so a multi-threaded run writes the same
  alignments in the same order every time. The original runs did not.
- When reference genes tie in the ranking, the first-seen gene wins. Perl's
  hash order made this random.

## External tools

- **Bowtie2:** needed for FASTQ and FASTA input only. The pipeline looks
  in `$HYB2PIPE_TOOL_DIR` first (the desktop bundle's `bin/`), then on the
  PATH. It prefers `bowtie2-align-s` / `bowtie2-build-s`, because the
  `bowtie2` wrapper needs Perl. The desktop runtime ships these binaries;
  see `desktop/scripts/acquire-bowtie2.py`.
- **cutadapt:** needed for FASTQ and FASTA input unless `--adapter none`
  (already-collapsed FASTA is never trimmed). The pipeline runs the cutadapt
  package of its own Python (`python -m cutadapt`, as the desktop runtime
  bundles it), else `cutadapt` on the PATH. It trims the 3' adapter
  (`--adapter`, default TGGAATTCTCGGGTGCCAAGG) and drops reads shorter than
  `--min-length` (18), then Bowtie2 maps with `--seed-length` 10 and
  `--score-min` L,15,0 (legacy hyb2: 16 and L,18,0; `--bowtie2-args` adds
  more). These are gk-dev's mapping defaults from the U2 benchmark on the
  Brr2 CLASH data. The report goes to `<out>.cutadapt.log`.
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

HYB2's later steps (contact maps, viewpoints, the differential comparison)
are in `hyb2analysis`, below, and in the web and desktop analysis pages, which
also fold.

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
  interactions.tsv, similarity.tsv, summary.json). The legacy split's dummy
  (0, 0) cell is not reproduced.
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

### Command line: `python3 -m hyb2analysis compare|coverage|viewpoint`

Each command writes a table and an SVG (no R, no plotting library) and prints
the table's path; invalid input or options exit with status 2 and a one-line
message. `coverage` and `viewpoint` read (`-i`) a `.hyb`, gzip-compressed or
not, or an index from `index.build`; for valid records both give the same
cells and profiles.

- `coverage -i IN -a GENE [-b GENE2] [-o DIR or FILE.contact.txt]`: the
  contact map of GENE (x = arm 1, y = arm 2) or of GENE x GENE2 (GENE on x,
  whichever arm it is), as `x<TAB>y<TAB>count` cells of `--bin-size` nt
  (default 10), weighted by `--measure records|support|reads`, with
  `--homodimer all|only|exclude`; and `contact_map.svg`. With the defaults, a
  `.hyb` gives `coverage.write_contact`'s contact.txt.
- `viewpoint -i IN -a GENE [-b PARTNER] [-d REFERENCE.fasta] [--start N]
  [--end N] [-o DIR or FILE.gplot]`: the coverage of each nucleotide of GENE
  by the arms on it (mean per nucleotide with `--bin-size`), from `--start`
  (default 1) to `--end`, else GENE's length in `-d`, else its last arm end,
  as `position<TAB>value` lines after a `#<name>_<gene>_<end>` line; and
  `viewpoint.svg`.
- `compare --one A.hyb B.hyb --two C.hyb D.hyb -a GENE [-b GENE2] -o DIR`
  (or `-i TABLE`): the desktop's comparison, with its defaults (`-m 2`,
  `-r 100`, `--padj 0.05`, `--similarity-min 1`, DESeq2 alpha 0.01). Each
  sample's map is made as the desktop makes it: `coverage.write_contact` for
  GENE, or with `-b` the GENE x GENE2 cells. It writes `differential.compare`'s
  results.tsv, significant.tsv, interactions.tsv, similarity.tsv and
  summary.json, the maps it made under `contacts/`, `differential_map.svg`
  and `similarity_heatmap.svg`. A TABLE row is `<hyb> <contact.txt or ->
  <condition_one|condition_two> [<gene>]` (TAB- or space-separated, as in
  `data/hyb2_compare_input.table`), or `<hyb or contact.txt> <condition>`.
  Relative paths are also looked up next to the table. The fourth column is
  the gene of the row's map (`-a` overrides it). A contact.txt is used when it
  exists and is the map asked for, or when the row has no `.hyb`; otherwise
  the map is made from the `.hyb`.

On the bundled test data:

```bash
PYTHONPATH=pipeline python3 -m hyb2pipe -i data/testData.sam -d data/Zika_18S.fasta -o out/testData
PYTHONPATH=pipeline python3 -m hyb2analysis coverage -i out/testData.hyb -a Zika_virusRNA -o out/coverage
PYTHONPATH=pipeline python3 -m hyb2analysis viewpoint -i out/testData.hyb -a Zika_virusRNA -d data/Zika_18S.fasta -o out/viewpoint
```

`out/coverage/testData.Zika_virusRNA.contact.txt` and
`out/viewpoint/testData_Zika_virusRNA.gplot` (10,807 positions) have their
SVGs next to them.
