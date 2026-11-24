# HYB2 IRIS command line

`bin/iris` predicts a weighted ensemble for one RNA region from a reference FASTA and one or more HYB evidence files. It needs Python 3.11, NumPy and the ViennaRNA Python bindings. It does not need a HYB2 project, a GUI, a server, or Numba.

From the source directory, with a Python 3.11 virtual environment active:

```sh
python -m pip install numpy ViennaRNA==2.7.2
python bin/iris --reference ref.fa --gene toyRNA --start 31 --end 170 --hyb reads.hyb -o ensemble.json
```

Replace `toyRNA` with the reference record ID used in the HYB file. Positions are 1-based and inclusive. Both arms must be on that reference and fully inside the window. An explicit window must be 10–1,000 nt; omit both bounds to select the whole record (at most 1,000 nt). Use `--gene` for multi-record FASTA. FASTA and HYB files may be gzip compressed (`.gz`). Repeat `--hyb` to pool samples. `--help` lists the ensemble and stem parameters.

JSON includes every selected member's dot-bracket, weight, energy and region-local pairs, the effective parameters, and ensemble statistics. Member position 1 is the first base of the selected window. Without `-o`, JSON goes to stdout; with `-o`, the destination must not already exist.

## Ensemble parameters

`--ensemble-size +2.0 --candidate-count 2e1 --fraction .75` selects `K=2`, `C=20` and `fraction=0.75`. Each flag takes one ASCII decimal number, optionally surrounded by ASCII whitespace. The browser worker and desktop tasks accept the same text or a finite JSON number. Empty or whitespace-only strings, arrays, objects, booleans, `0x2`, `1_2`, `NaN` and infinities are invalid.

After conversion to double precision, `K` must be an integer from 1 to 3 and `C` an integer from `K` to 50. The support quantile accepts `0` but not `1` (`0 ≤ fraction < 1`). Omit a flag, or omit the task field or send `null`, to use its catalog default.

## Stem lengths

`--stem-lengths 4 6` and `--stem-lengths '4, 6'` both select lengths 4 and 6. The CLI, browser worker and desktop form share the [`kRange` grammar](../pipeline/hyb2analysis/iris_parameters.py):

- Each number uses ASCII decimal notation. A sign, decimal point or base-ten exponent is allowed (`+4`, `4.0`, `4e0`). After conversion to double precision, each value must be a whole number from 2 through 12.
- Separate numbers with ASCII whitespace or a single comma. Whitespace may surround commas and the list. Leading, trailing or repeated commas are invalid, as are `1_2`, `0xA`, `4.5` and non-ASCII digits.
- A task may instead send a nonempty array of numbers, not strings. The result is always a sorted list without duplicates: `7, 5,5  4` and `[7,5,5,4]` both give `[4,5,7]`.
- An omitted or null task field, or the literal empty string `""`, selects the catalog default (currently `4 5 6 7`). Whitespace-only text and empty arrays are errors. The catalog itself must specify a nonempty, valid string.

## Source distribution

The file list in [`scripts/build/iris-cli-files.txt`](../scripts/build/iris-cli-files.txt) defines the **IRIS CLI only**. It includes the shared Python computation, input readers, parameter definitions and licence notices, but no Web or Desktop application sources, datasets, binaries or Git history. Use the list from the repository root to create and inspect a local archive:

```sh
COPYFILE_DISABLE=1 tar -czf hyb2-iris-cli.tar.gz -T scripts/build/iris-cli-files.txt
tar -tzf hyb2-iris-cli.tar.gz
```

For a public CLI repository, start a new history from the inspected archive. Do not publish the internal repository and then delete the product directories: earlier commits can still contain their source. This archive does not build the website or a desktop installer.

The IRIS method is adapted from [JY-Zhou/IRIS v1.0](https://github.com/JY-Zhou/IRIS), source revision `4057c5ac0e038144e414c27f01cfd7b5a5be926f` ([GPLv3](../third_party/iris/LICENSE)). The package also uses the `cplfold.sequence` and `cplfold.bonus` modules of [CPLfold](../third_party/cplfold/LICENSE); keep the [third-party notices](../third_party/cplfold/THIRD_PARTY_NOTICES.md). NumPy and ViennaRNA are installed separately. The file list is a packaging boundary, not licence clearance for any other distribution.
