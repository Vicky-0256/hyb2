# HYB2 COMRADES command line

`bin/comrades` folds one RNA region, or two regions joined by a 100-nt spacer, with COMRADES, from a reference FASTA and one or more HYB evidence files ([COMRADES and IRIS](../README.md#comrades-and-iris)). It runs the Python code of the desktop app's COMRADES engine, so it gives the desktop app's result. It needs Python 3.11 and the ViennaRNA Python bindings. It does not need NumPy, a HYB2 project, a GUI or a server.

From the source directory, with a Python 3.11 virtual environment active:

```sh
python -m pip install ViennaRNA==2.7.2
python bin/comrades --reference data/U2.fasta --hyb data/U2-U2.hyb.gz -o u2.json
```

This folds the yeast U2 snRNA (LSR1, 1,175 nt) with its 2,413 U2-U2 chimeras from the Brr2 data of Kudla et al. 2011 (PNAS 108:10010) in a few seconds.

## Input

Positions are 1-based and inclusive. Omit `--start` and `--end` to fold the whole record; an explicit window must be at least 10 nt. COMRADES folds at most 3,000 nt. Use `--gene` for a multi-record FASTA: it is the record ID, which is the RNA's name in the HYB file. A chimera is evidence when both of its arms are on that RNA and inside the region. FASTA and HYB files may be gzip compressed (`.gz`). Repeat `--hyb` to pool samples.

Any of `--start-two`, `--end-two` and `--gene-two` adds a second region: on the same RNA, or on the RNA that `--gene-two` names. As in the original `hyb2_fold`, the regions are joined by a spacer of 50 A and 50 U, and the evidence is the chimeras with one arm in each region, in either order. The 3,000-nt limit counts both regions and the spacer.

## Parameters

| Flag | Default | Meaning |
|---|---|---|
| `--constraint-limit` | 75 | Most evidence stems tried as constraints, 1 to 75 |
| `--random-folds` | 0 | Extra fits with the stems in seeded random orders, 0 to 1,000 |
| `--seed` | `HYB2-Web` | Seed of the random orders; a seed gives the same orders as in the web and desktop apps |
| `--temperature` | 37 | Folding temperature in °C, 0 to 100 |
| `--min-loop` | 3 | Minimum hairpin loop, 0 to 12 |
| `--homodimer-only` | off | Use only the chimeras whose arms are on one RNA and overlap by at least 5 nt |
| `--fold-program` | `viennarna` | `mfold` fits the stems with mfold 3.6 instead of ViennaRNA |

The defaults are the web and desktop apps' ([`engine_parameters.py`](../pipeline/hyb2analysis/engine_parameters.py)). RNAcofold makes the evidence at the same temperature and minimum loop.

`--fold-program mfold` needs an mfold 3.6 that you install, for example with `conda create -n mfold -c conda-forge -c bioconda mfold`. `bin/comrades` runs the `mfold` script that `HYB2_MFOLD` names, else `mfold` on `PATH`, else one in the usual conda and Homebrew locations. mfold folds at 37 °C with a minimum loop of 3 only.

## Output

The JSON gives the engine and its version, the effective parameters, the regions, the folded `sequence` with its `structure` (dot-bracket) and `energy` (kcal/mol), `pairs` and `constraints`. Each item of `pairs` is `[i, j, n]`: a base pair and its RNAcofold observations. `constraints` lists the base pairs of the accepted stems. `evidence` holds the counts behind them: the HYB rows used, the pair observations, the strongest evidence pairs (`selectedEvidencePairs`, at most 1,001, each with up to 20 HYB read IDs), and the accepted and rejected stem constraints. `randomisation` lists every fit, and `skipped` counts the HYB rows that were not used, by reason. Positions in `pairs`, `constraints` and `evidence` are 1-based in `sequence`. Without `-o`, JSON goes to stdout; with `-o`, the destination must not already exist.

With two regions, `sequence` is region 1, the spacer and region 2, as folded, and `energy` is the energy of that sequence. `withoutSpacer` gives the two regions as two strands, as the desktop app stores them: the sequence and structure with `&` between the regions, as RNAcofold writes them, and the pairs and constraints numbered along region 1 and then region 2, not counting the `&`. Its `spacerPairsLeftOut` counts the pairs that used a spacer base.

## Comparison with the original hyb2_fold

For one region, `bin/comrades` follows the original `hyb2_fold`, which ran `comradesMakeConstraints_2` and `comradesFold2 -r 1` ([`5398ef3`](https://github.com/Vicky-0256/hyb2/tree/5398ef3/bin)). On U2 with the defaults and the ViennaRNA 2.7.2 programs, both give the same structure and dG (−433.6 kcal/mol), the same four accepted stems (1088–1154, 1097–1143, 138–1170 and 150–1157) and the same RNAcofold pair counts. [`test_comrades.py`](../desktop/runtime/tests/test_comrades.py) checks this for the CLI and the desktop app, and [`verify-structure-worker.cjs`](../scripts/verify/verify-structure-worker.cjs) for the web app.

With two regions, `bin/comrades` and the two apps cofold each chimera's arms in the order of its HYB row, as `hyb2_fold` did; the order can change the pairs that RNAcofold reports. On U2 126–175 with 1126–1175, both give the same structure and dG (−63.3 kcal/mol), the same accepted stems (138–1170, 12 bp, and 150–1157) and the same pair counts, which the same tests check. One difference remains: a chimera whose two arms both lie in both regions, as in overlapping regions or the same region twice (a homodimer), was used twice by `hyb2_fold`, once in each placement, and is used once here, with its first arm in region 1.

## Source distribution

The file list in [`scripts/build/comrades-cli-files.txt`](../scripts/build/comrades-cli-files.txt) defines the **COMRADES CLI only**. It includes the shared Python computation, input readers, parameter definitions and licence notices, but no Web or Desktop application sources, datasets, binaries or Git history. Use the list from the repository root to create and inspect a local archive:

```sh
COPYFILE_DISABLE=1 tar -czf hyb2-comrades-cli.tar.gz -T scripts/build/comrades-cli-files.txt
tar -tzf hyb2-comrades-cli.tar.gz
```

For a public CLI repository, start a new history from the inspected archive. Do not publish the internal repository and then delete the product directories: earlier commits can still contain their source. The archive leaves out the U2 example in `data/`.

[`comrades.py`](../pipeline/hyb2analysis/comrades.py) ports the method of the original `hyb2_fold` scripts; Hyb2 is under the [GPLv3](../LICENSE). The package also uses the `cplfold.sequence` and `cplfold.models` modules of [CPLfold](../third_party/cplfold/LICENSE); keep the [third-party notices](../third_party/cplfold/THIRD_PARTY_NOTICES.md). ViennaRNA, and mfold if you use it, are installed separately. The file list is a packaging boundary, not licence clearance for any other distribution.
