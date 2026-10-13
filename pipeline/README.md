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
- **ViennaRNA:** needed only for `-e 1`, either the Python `RNA` module or
  `RNAcofold` on the PATH.

Steps 2 and 3 of `hyb2` (contact maps, viewpoint, folding) are already
provided by the web and desktop analysis pages. `hyb2_compare` (R/DESeq2) has
not been ported.
