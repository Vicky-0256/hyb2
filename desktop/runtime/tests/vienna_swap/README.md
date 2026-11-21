# ViennaRNA replacement of hybrid-min: reference data

`hybridmin_dg_testData.tsv` is the .hyb dG column (column 3) that `hyb2pipe`
wrote for `data/testData.sam` with UNAFold's hybrid-min (OligoArrayAux 3.8.1;
RNA, 37 °C, 1 M Na+) on 2026-11-21, before hybrid-min was replaced. Every other
column of that run equals `pipeline/tests/golden/testData.hyb`.
`test_vienna_swap.py` compares the ViennaRNA dG column with it.

The ZIKV regression in `test_vienna_swap.py` reads the chimera arms and the
hybrid-min pair counts C_ij that `kernel_experiments` cached in
`jobs/zikv_new_b/` (outside this repository); it is skipped where they are
absent. Its RNAduplex values were computed with ViennaRNA 2.7.2
`RNA.duplexfold` (default model, 37 °C) on the same arms.
