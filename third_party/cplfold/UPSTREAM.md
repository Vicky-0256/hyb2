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
- `CPLfold_parser.py` beam pruning (`beam_prune_deterministic`) follows
  LinearFold's `beam_prune`: with beam k, the threshold is the (k+1)-th best
  score (LinearFold calls `quickselect(scores, 0, size - 1, size - beam)`,
  whose last argument is a 1-based ascending rank) and only the candidates
  strictly below it are erased. So k+1 candidates survive when the scores are
  distinct, plus every candidate tied with the threshold; beam 0 does not
  prune. `tests/test_beam_prune.py` pins the rule. Upstream kept exactly k,
  dropping tied candidates by index; with integer Turner scores (alpha = 0)
  this lost good paths. Three rules were measured side by side on 2026-11-15
  (pseudoknot-free, alpha 0, beam 100): the upstream cap, the intermediate
  rule of commit `8bd0ad4` (threshold at the k-th best score, ties kept: one
  candidate narrower than LinearFold) and the exact rule. The 10,807-nt ZIKV
  PE243 genome folds to -3413.2, -3722.1 and -3717.5 kcal/mol (RNAfold MFE
  -3866.4, not re-measured). On 50 random RNAs (`random.Random(7)`, ten each
  of 120, 200, 300, 400 and 600 nt) the mean gap to the RNAfold MFE is 1.38,
  0.25 and 0.24 kcal/mol. A beam search is not monotone in its width: the
  exact rule is 4.6 kcal/mol above the intermediate one on the ZIKV genome
  and lower on one of the 50 RNAs (the other 49 structures are identical).
  Fold time does not change measurably: the ZIKV fold takes 71 to 79 s of CPU
  time with every rule (JIT warm, one thread, a loaded machine, two or three
  runs each), of which the beam search itself is 4.1 s with the cap and 4.3
  to 4.7 s with the exact rule. The "about 2.7 times" of commit `8bd0ad4`
  (239 s against 89 s) timed a freshly edited copy without a warm-up, so it
  included that copy's JIT compilation. Because the exact rule keeps one
  candidate more than upstream even without ties, structures can differ from
  upstream with a non-integer bonus too (under the intermediate rule they
  were identical). The M-beam index is n x n (int32) so that the survivors
  cannot overflow it. The score a candidate is ranked by
  (`beam_prune_score`) also follows LinearFold: a candidate whose exterior
  prefix `bestC[i - 1]` is unreachable gets `VALUE_MIN`, so such candidates
  tie and are not pruned against each other. A prefix is unreachable only
  under forced-pair constraints (`(`/`)` passed to `parse(seq, cons=...)`);
  Hyb2 itself passes only `.`/`?` masks, for which results are unchanged.
  These changes are not upstream yet.
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
