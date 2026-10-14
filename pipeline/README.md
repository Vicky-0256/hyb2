# hyb2pipe — HYB2 step 1 in pure Python

`hyb2pipe` replaces the Perl/awk/bash chain behind `bin/hyb2` step 1
(`sam_filt`, `sam2hyb` and the `bowtie2_*2sam` wrappers), so the pipeline runs
anywhere CPython 3.9+ runs, including Windows, with no Perl, awk, sed or sort.

```bash
PYTHONPATH=pipeline python3 -m hyb2pipe -i reads.fastq.gz -d reference.fasta -o out/sample
PYTHONPATH=pipeline python3 -m hyb2pipe -i aligned.sam -d reference.fasta -o out/sample -e 1
```

The `-i -d -o -v -m -h -e` flags match `hyb2`. The command writes
`<out>.hyb`, `<out>_homodimers.hyb`, `<out>_hybrids.hyb_stats.txt` and
`<out>.sam`. In the desktop app the same code runs as the `pipeline` task kind.

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
