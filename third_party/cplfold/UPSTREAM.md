# Vendored snapshot

- Repository: `https://github.com/Vicky-0256/CPLfold.git`
- Upstream branch: `feature/pseudoknot-free-mode`
- Upstream commit: `24bab521e0bcf2cff71f4567363aac1d5f5c97d5`
- Import method: squashed Git subtree
- Destination: `third_party/cplfold`

The snapshot contains Python sources and data tracked by upstream. It excludes
the upstream Git database, Python bytecode, Numba caches, shared libraries, and
other ignored local artifacts.

## Hyb2-local changes after import

- The `CPLcofold` branch adds `CPLcofold.py`, a strand-aware Numba parser,
  lazy k-best candidate enumeration, a strand-bounded reads bonus builder,
  and cofold energies. Alpha guides both phases; beta retains the existing
  pseudoknot reranking formula. The cofold pseudoknot Turner/DP extension
  is explicitly experimental, with model metadata on every result. Its
  strand-graph scoring follows covalent links at strand breaks and uses one
  Turner local-energy baseline for connected and disconnected candidates.
  Numeric guards reject overflowing search/ranking values.
- The CLI accepts `A&B`, `--sequence-two`, `--cutpoint`, and an explicitly
  selected two-record FASTA (`bin/cplfold --cofold --sequence-file ...`).
  Nucleotide and bonus coordinates omit `&`. The original single-strand
  parser and HotKnots evaluator are retained.
- `CPLfold.py` treats a completed Phase-1-only prediction as CLI success.
- `CPLfold.py` supports explicit pseudoknot-free prediction through
  `--no-pseudoknot` and `allow_pseudoknot=False`.
- `tests/test_cli_exit_status.py` locks that exit-status behavior.
- `CPLfold.py` accepts Hyb2's sparse bonus-matrix TSV export through
  `--bonus-matrix-file` and applies it with `--alpha`.
- `CPLfold_parser.py` beam pruning (`beam_prune_deterministic`) keeps every
  candidate that scores at least the k-th best, ties included, as LinearFold's
  `beam_prune` does (it erases only the candidates strictly below the
  threshold). Upstream capped the kept set at k, dropping tied candidates by
  index; with integer Turner scores (alpha = 0) this lost good paths: the
  10,807-nt ZIKV PE243 genome at beam 100 folded to -3413.2 kcal/mol, and
  to -3722.1 with ties kept (RNAfold MFE -3866.4). On 50 random RNAs of
  120-600 nt at alpha 0 the mean gap to the MFE fell from 1.38 to 0.25
  kcal/mol; with a non-integer bonus (no ties) the structures are identical.
  The M-beam index is now n x n (int32) so that ties cannot overflow it.
  Folds with many ties (alpha = 0, long RNAs) take longer: about 2.7 times
  for the ZIKV genome at beam 100. This change is not upstream yet.
- `CPLfold_parser.py` compiles the outside pass with Numba. The new
  `_outside_kernel` and its `_outside_*` scoring helpers are verbatim copies
  of `BeamCKYParserHyper._outside` and its methods, with `self` dropped and
  the list tables swapped for the existing int32 arrays; fastmath is off.
  Output is byte-identical to the original (kept as `_outside_python`;
  `CPLFOLD_PYTHON_OUTSIDE=1` selects it). 600 nt drops from 270 s to about
  1 s. This change is not upstream yet.
- `LICENSE` is the author's GPL-2.0-or-later grant for CPLfold's own code
  (2026-10-17), not in upstream commit `24bab52`. `LICENSES/`,
  `THIRD_PARTY_NOTICES.md`, and this file record provenance and the
  distribution constraints of the LinearFold- and HotKnots-derived parts.

Upstream commit `24bab52` does not identify an exact LinearFold source revision
or the exact HotKnots 2.0 archive/checksum used to create the Python-derived
files. That missing derivation metadata must be resolved before treating this
snapshot as independently reproducible provenance.
