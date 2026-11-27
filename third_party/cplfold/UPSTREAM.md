# Origin

Since 2026-11-23 CPLfold is developed as part of Hyb2, in this directory.
It is no longer synced from the repository below by `git subtree pull`; make
changes here. The import record below and the list of Hyb2-local changes
remain for provenance.

- Repository: `https://github.com/Vicky-0256/CPLfold.git`
- Upstream branch: `feature/pseudoknot-free-mode`
- Upstream commit: `24bab521e0bcf2cff71f4567363aac1d5f5c97d5`
- Import method: squashed Git subtree
- Destination: `third_party/cplfold`

The snapshot contains Python sources and data tracked by upstream. It excludes
the upstream Git database, Python bytecode, Numba caches, shared libraries, and
other ignored local artifacts.

## Hyb2-local changes after import

- On 2026-11-23 the sources became the Python package `cplfold` (one import
  name, relative imports, public API in `cplfold/__init__.py`, CLI in
  `cplfold/cli.py`, project file `pyproject.toml`). Files were moved, not
  edited: `CPLfold.py` is `cplfold/_internal/single.py` (its `main` is in
  `cplfold/cli.py` and `load_bonus_matrix_file` in `cplfold/bonus.py`), `CPLcofold.py` is
  `cplfold/_internal/cofold.py`, `CPLfold_parser.py` is
  `cplfold/_internal/parser.py`, `CPLcofold_parser.py` is
  `cplfold/_internal/cofold_parser.py`, `Utils/cofold_bonus.py` is
  `cplfold/bonus.py`, `Utils/strand_layout.py` is `cplfold/sequence.py`, and
  the other `Utils/` modules and `Utils/energy_params` are under
  `cplfold/_internal/`. The model tables of `Utils/engine_parameters.py` are
  `cplfold/models.py`; its Hyb2 task settings, `iris_parameters.py` and
  `support_kernels.py` moved to Hyb2's `pipeline/hyb2analysis/`. Results are
  unchanged. Entries below use the current paths.
- The `CPLcofold` branch adds `cplfold/_internal/cofold.py`, a strand-aware Numba parser,
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
- `cplfold/_internal/single.py` treats a completed Phase-1-only prediction as CLI success.
- `cplfold/_internal/single.py` supports explicit pseudoknot-free prediction through
  `--no-pseudoknot` and `allow_pseudoknot=False`.
- `tests/test_cli_exit_status.py` locks that exit-status behavior.
- `cplfold/_internal/single.py` accepts Hyb2's sparse bonus-matrix TSV export through
  `--bonus-matrix-file` and applies it with `--alpha`.
- `cplfold/_internal/parser.py` beam pruning (`beam_prune_deterministic`) follows
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
- `cplfold/_internal/parser.py` compiles the outside pass with Numba. The new
  `_outside_kernel` and its `_outside_*` scoring helpers are verbatim copies
  of `BeamCKYParserHyper._outside` and its methods, with `self` dropped and
  the list tables swapped for the existing int32 arrays; fastmath is off.
  Output is byte-identical to the original (kept as `_outside_python`;
  `CPLFOLD_PYTHON_OUTSIDE=1` selects it). 600 nt drops from 270 s to about
  1 s. This change is not upstream yet.
- `cplfold/_internal/single.py` `phase1_fold` takes one Phase-1 candidate
  from `parse` rather than `parse_subopt(max_structures=1)`, whose first
  result is the same MFE structure and score after an outside pass it does
  not use (the CPLcofold parser already did this). Results are identical;
  yeast U2 (1,175 nt, beam 200) drops from 2.4 s to 0.5 s, and a pure-Python
  200-nt fold from 56 s to 31 s. This change is not upstream yet.
- `cplfold/_internal/hotknots_energy.py` walks its loop and closed-region
  trees with explicit stacks instead of recursion (`_score_node`,
  `_is_pk_free`, `_top_pseudoknots`, `_descendants`, `_score_re_node`). The
  trees have a node per nested base pair, so the recursive walks exceeded
  Python's recursion limit: full-length yeast U2 (1,175 nt) with pseudoknots
  (beam 100, ten Phase-1 candidates) stopped with `RecursionError` while
  scoring its sixth candidate. The walks visit nodes in the recursive order:
  2,420 evaluations (484 structures under every energy model: U2 candidates
  and random structures with up to three crossing helices) gave
  bit-identical energies and breakdowns. This change is not upstream yet.
- `cplfold/_internal/cofold_energy.py` adds a CPLcofold structure's energy
  terms left to right instead of with `sum()`. Since Python 3.12, `sum()`
  of floats compensates its rounding error, so the same terms gave totals
  that differ in the last bit between Python versions. When DP09's band
  scaling puts a total exactly on a half cent, its two-decimal energy then
  differs by 0.01: with pseudoknots, 5 of 120 ZIKV arm pairs did between the
  web (Pyodide, Python 3.13) and the desktop (Python 3.11), for example
  -35.45 and -35.46. Every Python version now gives what 3.11 gave, so
  energies under Python 3.11 are unchanged. This change is not upstream
  yet.
- `LICENSE` is the author's GPL-3.0-or-later grant for CPLfold's own code
  (2026-11-23), not in upstream commit `24bab52`. Copies already
  distributed under the earlier GPL-2.0-or-later grant (2026-10-17) keep it. `LICENSES/`,
  `THIRD_PARTY_NOTICES.md`, and this file record provenance and the
  distribution constraints of the LinearFold- and HotKnots-derived parts.

Upstream commit `24bab52` does not identify an exact LinearFold source revision
or the exact HotKnots 2.0 archive/checksum used to create the Python-derived
files. That missing derivation metadata must be resolved before treating this
snapshot as independently reproducible provenance.
