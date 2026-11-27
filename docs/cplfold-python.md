# CPLfold Python integration

CPLfold is part of HYB2 and lives in `third_party/cplfold`. It was originally
imported from `https://github.com/Vicky-0256/CPLfold.git`, branch
`feature/pseudoknot-free-mode`, commit
`24bab521e0bcf2cff71f4567363aac1d5f5c97d5`, and is now developed here.

## Runtime boundary

CPLfold's folding and pseudoknot-energy path is Python source and does not call
a HotKnots executable, shared library, wrapper, or external process. It is not
a standard-library-only implementation: the complete predictor requires NumPy
and Numba, and Numba uses LLVM JIT compilation. ViennaRNA is optional. SciPy
and pysam are needed only by the optional PARIS BAM extraction script.

The local CLI, desktop runtime and GitHub Pages application share the vendored
Python sources and parameters. The Pages build packages them with Pyodide
0.29.4 and NumPy 2.2.5.
Pyodide does not provide Numba, so the browser bridge uses identity `njit`
decorators. The local CLI remains the supported route for longer workloads.
The browser measures a conservative session recommendation above a 75-nt
baseline, with a 500-nt hard browser ceiling. No server-side execution layer
is required.

By default in HYB-guided browser runs, each eligible row contributes its two prepared
reference intervals once to the original CPLfold/IRIS-style Gaussian,
symmetric outer-product and `log1p` bonus transform. The browser does not use
HYB overlap score or collapsed source-read count as matrix weights.

`pipeline/hyb2analysis/support_kernels.py` owns the selectable kernel IDs,
labels, pair estimators and default for both UIs. The browser build and desktop
release workflow export the catalog automatically. For a source checkout, run
`python3 -B scripts/build/export-support-kernels.py` after changing it and include
both generated files in the change (`--check` detects stale copies). This lets
the static page and native MyGo form show the choices before Python starts.

`pipeline/hyb2analysis/engine_parameters.py` is the source for the
Structure form ranges of
CPLfold, CPLcofold, ViennaRNA, COMRADES and IRIS; it takes the selectable
CPLfold/CPLcofold energy models from `cplfold.models`
(`third_party/cplfold/cplfold/models.py`). The same export command
updates both clients. Desktop single-strand tasks allow beam sizes up to
1000; browser tasks and all CPLcofold tasks stop at 200. The desktop mfold
backend has fixed temperature and minimum loop settings. These task limits
do not replace the standalone CLI's search options or platform memory limits.
For IRIS, edit `IRIS_DEFAULTS` in `pipeline/hyb2analysis/iris_parameters.py`
and run the same export. Both forms and the browser worker read the exported
defaults; the desktop runtime and `bin/iris` read the Python definition directly.
For CPLfold and CPLcofold, each numeric task parameter declares its default
beside its range. Omitting beam size, phase-1 count, energy delta or beta
selects 200, 1, 5 or 0, respectively. Omitted alpha is 0.5 for single-strand
tasks and 0 for two-strand tasks. Both guided forms explicitly start at
alpha 0.5; an explicit zero is never replaced by the default.
On both hosts, omitting `allowPseudoknot` runs only Phase 1. Only JSON `true`
enables Phase 2; `false` disables it. Strings, numbers and `null` are
rejected. Native tasks also accept `allow_pseudoknot`; the legacy native CLI
task accepts the inverse `no_pseudoknot`. The standalone CLI and Python API
keep their original two-phase default.
Rebuild the browser Python archive and desktop application/runtime together
with their generated assets; already installed clients do not update live.

`cplfold.sequence` (`third_party/cplfold/cplfold/sequence.py`) defines the engine's
alphabet, base aliases and ignored whitespace. Hyb2's paste rules for both
clients are in `pipeline/hyb2analysis/fold_text.py`, built on them. Each paste
box accepts one FASTA record or plain text: skip the header, whitespace and
ASCII position numbers, uppercase bases and convert T to U. For two strands,
use separate boxes or `A&B` in the first box. The break is the length of A
after cleanup, not a nucleotide. Reference, HYB record and prepared worker
inputs do not discard digits or FASTA headers as paste formatting. Engine
and platform length limits apply after normalization.

`pipeline/hyb2analysis/reference.py` owns the Structure input minimums,
exported as `foldText.minWindowNt`, `minPastedNt` and `minSequenceNt`.
An explicitly selected single reference window needs 10 nt; a pasted single
strand needs 5 nt. A whole reference, a HYB record and each strand or fragment
of CPLcofold or paired COMRADES need at least one nucleotide. The 5-nt paste
cutoff is a single-strand input policy, not a requirement for an intermolecular
pair. A pasted or record sequence located in the reference keeps its source's
minimum; locating it does not turn it into a user-selected reference window.
Coordinates are 1-based and inclusive. Both clients reject a start after its
end instead of swapping the bounds, including in two-region layouts.

The request field `supportKernel` (the "Read support (B_ij)" setting on the web
and desktop pages; `support_kernel` for desktop structure tasks) chooses how a
row becomes support. Every kernel sums one contribution per row. Gaussian uses
the IRIS rule: accumulate in double precision, discard cells below `2^-23`
after summing all rows, then apply `log1p`. The two block kernels retain their
`1e-6` cutoff before `log1p`; the two Vienna pair kernels saturate their counts:

| `supportKernel` | Each row adds |
|---|---|
| `gaussian` (default) | a Gaussian around each arm centre (sigma = mean arm length / 6, peak 1), symmetric outer product: the IRIS support |
| `block` | 1 to every pair between its two arms |
| `block-normalized` | 1 / sqrt(arm one length x arm two length) to every pair between its two arms |
| `duplex` | 1 to the count C_ij of each pair that ViennaRNA's RNAduplex (37 °C, intermolecular pairs only) forms between its two arms; then B_ij = s C_ij / (C_ij + q), s = 3.6 kcal/mol, q = the mean C of the round(n/3) best-supported pairs |
| `comrades` | Jointly fold the two arms with ViennaRNA RNAcofold (37 °C, minimum hairpin loop 3). Arm-internal pairs compete in the fold but add no evidence. Add 1 for each cross-arm pair and use the same saturating transform as `duplex`. |

`duplex` is the support of the HYB2 manuscript for every analysis except the
IRIS benchmark. Arms that cannot pair (RNAduplex's `.&.`) add nothing. Native
hosts compute the duplexes with ViennaRNA's Python module (bundled with the
desktop runtime); from 4,000 unique arm blocks on, the blocks go in chunks of
1,000 to `duplexWorkers` forked processes (default: `HYB2_DUPLEX_WORKERS`, else
one per CPU; one process on Windows). The browser runs the same code: the CPLfold
worker loads ViennaRNA's own Python module, built for Pyodide by
`scripts/build/build-vienna-pyodide.sh` (about 1.9 MB, loaded on first use), and
gives the same pairs as the desktop. A host may instead pass the counts as
`evidencePairs` (`[[one, two, rows], ...]`, 1-based).
Desktop tasks saved with the old kernel name `hybridmin` run as `duplex`.
The removed `rnacofold` kernel applied `log1p` to RNAcofold counts. Old tasks
are not silently converted to `comrades`: choose the new transform before rerunning.

In a HYB-guided `comrades` or `duplex` fold, one table of raw counts serves two purposes.
CPLfold uses the transformed matrix B during candidate search. Effective energy
determines the final ranking, with additional tie-breakers for two strands. Each
candidate also receives a COMRADES score from the **original counts**, not B:
sum `min(C_ij, 1,000,000)` over its pairs and double the sum (both endpoints).
The ceiling comes from Lau's `bp_score.sh` invocation of `make_VARNA_scores_2.sh`.
It does not clip B or `comradesPairCounts`, the raw counts aligned with each candidate's pairs.
This score does not rerank CPLfold candidates. Standalone COMRADES(ViennaRNA)
at its default settings uses the same RNAcofold arm model as `comrades`, but
fits ranked stems as hard constraints instead of using B. Only constraint
selection is limited to the strongest 1,001 pairs; a pair is counted once,
whichever arm of the chimera was sequenced first, in selection and in scoring. Neither Vienna model promises the same
per-pair counts as the original UNAFold `hybrid-min`.

`log1p` compresses each cell but does not put the kernels on one scale. On the
ZILM virion reads of genome positions 1-1200, the summed upper-triangle support
is 2.27 x 10^6 for `block`, 1.13 x 10^6 for `gaussian` and 0.42 x 10^6 for
`block-normalized`, so the same alpha weights the reads differently under each
kernel. Choose alpha for each kernel separately (for example by held-out
icSHAPE log-likelihood, or by the chimeric consistency score) before comparing
kernels.

For two strands every arm lies on one strand, so the block kernels stay on their strands as the
Gaussian does. A bonus-matrix export records a non-default kernel in a
`# support_kernel=` comment and in its `# transform=` line.

## Install and run

Use CPython 3.11 for the maintained CI environment. The same pinned dependency
set was also verified locally on CPython 3.8.10:

```bash
python3.11 -m venv .venv-cplfold
. .venv-cplfold/bin/activate
python -m pip install -r requirements/cplfold-core.txt
bin/cplfold --sequence GGCGCGGCACCGUCCGCGGAACAAACGG \
  --beam 20 --max-phase1 3 --max-phase2 2
```

HYB2 Web Lite can download the prepared sequence as a single-record FASTA. In
HYB-guided CPLfold mode it also downloads the sparse upper-triangle bonus
matrix used by the browser, as `cplfold-bonus-matrix.tsv`. The local CLI
reconstructs the symmetric dense NumPy matrix before folding:

```bash
bin/cplfold --sequence-file cplfold-input.fasta \
  --bonus-matrix-file cplfold-bonus-matrix.tsv \
  --beam 200 --delta 5 --max-phase1 1 --max-phase2 1 \
  --model DP09 --alpha 0.5 --beta 0
```

`--sequence-file` also accepts a plain sequence text file. FASTA headers and
line wrapping are removed, and multiple FASTA records are rejected. The bonus
matrix uses 1-based prepared-sequence coordinates, includes a declared
`sequence_length`, and is symmetric when loaded. Sequence-only CPLfold mode
does not need the matrix file.

To keep only pseudoknot-free secondary structures, add `--no-pseudoknot`. This
runs Phase 1 and skips the constrained Phase 2; in that mode `--beta` is
ignored.

The browser can export the matrix for local handoff independently of whether
the pure-Python browser fold fits its 500-nt prediction ceiling. Matrix export
is capped at 5,000 nt because the HYB transform is inherently quadratic; the
local predictor has its separate machine-dependent guard below.

The Hyb2 wrapper starts CPLfold as an isolated Python process, assigns a
private mode-`0700` Numba cache below the user's cache directory, and rejects
inputs longer than 1,000 nt by default. The parser allocates multiple
quadratic arrays, so this guard prevents accidental large-memory jobs. Set a
deliberate override only after sizing the machine:

```bash
HYB2_CPLFOLD_MAX_NT=1500 bin/cplfold --sequence-file cplfold-input.fasta
```

The web and desktop receive this limit as `foldText.localMaxNt`. For a longer
sequence, the command they show with its local inputs sets
`HYB2_CPLFOLD_MAX_NT` to that sequence's length, both strands together, so it
runs as shown while a longer FASTA is still refused.

Run the full vendored regression suite and a real-Numba smoke test with:

```bash
python scripts/verify/verify-cplfold-python.py
```

One strand runs exactly one constrained Phase 2 fold per Phase 1 structure,
as upstream commit `24bab52` does, so `cplfold.fold` accepts only
`max_phase2=None` there and the package command rejects `--max-phase2` with
status 2. `bin/cplfold` drops a valid one-strand `--max-phase2`, which never
changed a result, so existing Hyb2 command lines keep working. Only the
two-strand route uses it as a search limit (below); Hyb2's `fold_prepared`
passes `max_phase2=1` there.

## Frozen Python API (cplfold 1.0)

`third_party/cplfold/README.md` lists every public name and the `Prediction`
fields. In short:

```python
cplfold.fold(sequence, *, partner=None, pseudoknots=True, energy_model=None, bonus=None,
             alpha=0.0, beta=0.0, beam_size=100, energy_delta=5.0, max_phase1=10,
             max_phase2=None)            # -> list[cplfold.Prediction], best first
cplfold.Prediction                       # structure, energy, effective_energy, kind, score,
                                         # phase1_structure, phase1_energy, phase2_structure,
                                         # cutpoint, pairs, energy_model, read_support, details
cplfold.InputError                       # a ValueError
cplfold.sequence: normalise_sequence, add_strand_break, read_sequence_file, clean,
                  ALPHABET, ALIASES, WHITESPACE
cplfold.bonus:    load_bonus_matrix_file, gaussian_bonus_matrix, bonus_from_arms, gaussian_sigma
cplfold.models:   MODELS, COFOLD_MODELS, default_model(two_strands)
cplfold.cli:      main(argv=None), cofold_main(argv=None)
```

`energy_model` is the pseudoknot ranking model; it is validated also with
`pseudoknots=False`, when one strand ranks with Turner 2004 and
`Prediction.energy_model` says so. Errors: `cplfold.InputError` for invalid
arguments, sequences, file contents and input magnitudes that overflow the
numeric range; `OSError` when a file cannot be read. The commands exit with
status 2 and 1 for these, printing the message alone.

Changes before 1.0 that Hyb2 callers were updated for: `CofoldInputError` →
`InputError`; one `Prediction` schema with the public names (two-strand extras
in `details`); `DEFAULT_MODELS` → `default_model()`; `cofold_counts` and
`build_cofold_bonus_matrix` → `bonus_from_arms`; `gaussian_bonus_matrix(length,
counts, *, cutpoint=None, sigma=None)`; `gaussian_bonus_stripes`,
`mean_arm_length`, `strip_structure_break` and `cli.main(cofold_entry=)` are
private; the package command's one-strand alpha default is 0 (Hyb2's wrapper
passes 0.5). Hyb2 imports the private bonus helpers it needs, as it pins the package.

## Two-strand CPLcofold (Python and Numba)

The `CPLcofold` branch adds a Python cofold parser whose dynamic-programming,
single-best traceback, and local energy kernels run in Numba nopython mode.
Lazy k-best enumeration for multiple candidates and two-phase orchestration
remain Python. No external LinearCoFold or
ViennaRNA executable is required. Both strands are supplied in their own
5'-to-3' orientations; input order is preserved, including identical strands.

```bash
# Quote '&' so the shell does not interpret it as a background operator.
bin/cplcofold --mode pseudoknot --sequence 'GGGG&CCCC' --beam 100 \
  --max-phase1 10 --max-phase2 5 --alpha 0.5 --beta 0.3

# Ordinary cofold: no pseudoknots, one Phase-1 candidate, delta=0.
bin/cplcofold --mode standard --sequence GGGG --sequence-two CCCC

# The dedicated entry point accepts two-record FASTA directly.
bin/cplcofold --mode pseudoknot --sequence-file two-strands.fasta \
  --bonus-matrix-file reads-bonus.tsv --alpha 0.5 --beta 0.3
```

`bin/cplcofold` requires two strands and calls the same Python backend as
`cplfold.fold(a, partner=b)`. Its default is explicitly `pseudoknot` mode, matching
the existing API: alpha=0, beta=0, beam=100, delta=5, max_phase1=10 and
max_phase2=5. Reads affect the search only when a matrix and a nonzero alpha
are supplied. `--mode standard` selects the single-candidate, pseudoknot-free
configuration used for the warmed cofold performance comparisons. Multiple
candidates and pseudoknot search take additional work. The package commands
(`python -m cplfold.cli`, `cplfold`, `cplcofold`) default to alpha=0 on every
route, matching `cplfold.fold()`. Hyb2's `bin/cplfold` wrapper keeps alpha=0.5
for one strand by passing `--alpha 0.5` when `--alpha` is omitted.
Both command help pages describe their defaults and supported input forms.

Alternatively, `--sequence GGGGCCCC --cutpoint 4` supplies the concatenated
sequence and the length of strand A. Sequence limits count nucleotides in
both strands, excluding `&`. Bonus-matrix dimensions and TSV coordinates
also exclude `&`: strand A is `1..cutpoint`, strand B starts at
`cutpoint+1`. A two-record FASTA requires `--cofold` with the legacy
`bin/cplfold` wrapper; the dedicated `bin/cplcofold` already enables it.
Ordinary single-record input retains the existing behavior. `--no-pseudoknot` is optional, and beta
is only applied to pseudoknot candidates. `--max-phase2` is effective for the
two-strand path and controls distinct constrained candidates per Phase 1; for
one strand it is rejected (exit status 2), because one strand runs exactly one
Phase 2 fold per Phase 1 structure. Every invalid input, also in a sequence or
bonus file, exits with status 2; a file that cannot be read exits with status 1.

`parse()` and `parse_subopt(max_structures=1)` use the compiled single-best
traceback. It walks the retained chart without constructing Python candidate
heaps or pair-index lists. Candidate order and floating-point addition order
match the general k-best path, including fractional reads weights and ties;
the inside chart, constraints, association cost, and alpha/beta scoring rules
are unchanged. Requests for multiple candidates retain lazy k-best enumeration.

For the Python API, put `third_party/cplfold` on the Python import path (or
`pip install third_party/cplfold`) and import the package `cplfold`:

```python
import cplfold
from cplfold.bonus import bonus_from_arms

a, b = "GGGG", "CCCC"
# Prepared A+B coordinates, 1-based inclusive: two arm intervals per read.
arms = [(1, 3, 6, 8), (2, 4, 5, 7)]
bonus = bonus_from_arms(len(a) + len(b), arms, cutpoint=len(a))
results = cplfold.fold(a, partner=b, bonus=bonus, alpha=0.5, beta=0.3,
                       beam_size=100, max_phase1=10, max_phase2=5)
best = results[0]
print(best.structure, best.energy, best.effective_energy)
print(best.details["energy_metadata"])

# Ordinary single-candidate cofold, as `--mode standard` on the CLI.
ordinary = cplfold.fold(a, partner=b, pseudoknots=False, max_phase1=1, energy_delta=0.0)
```

`cplfold.fold(a + "&" + b, ...)` folds the same two strands. `fold` returns
`cplfold.Prediction` objects with the same fields on both routes:
`structure`, `energy`, `effective_energy`, `kind`, `score`, `phase1_structure`,
`phase1_energy`, `phase2_structure`, `cutpoint` (None for one strand), `pairs`
(0-based compact pairs), `energy_model` (`"Turner 2004"` for nested cofold
candidates and pseudoknot-free single-strand runs, otherwise the ranking
model), `read_support` (0.0 without a bonus) and `details` (read-only; for two
strands `strand_lengths`, `intermolecular_pairs`, `read_bonus`,
`energy_metadata`, `energy_breakdown` and, for pseudoknots,
`phase2_association_cost`). `as_dict()` returns a plain deep copy keyed by
those names. `fold` never prints or writes files; invalid input raises
`cplfold.InputError`, a `ValueError`.
`cplfold.bonus.load_bonus_matrix_file` and `cplfold.sequence.read_sequence_file`
raise `OSError` (`FileNotFoundError` for a missing file) or
`cplfold.InputError` for malformed contents; they never exit the process.
`cplfold.models.default_model(two_strands)` gives the model of
`energy_model=None`; `cplfold.sequence.ALIASES` is a read-only mapping.

The structure helpers in `cplfold._internal.single` (private) accept compact
or `&`-marked structures. Prefer `Prediction.pairs` and `Prediction.cutpoint`.

The reads builder supports intra- and intermolecular arms. It clips each
Gaussian to its own strand, sums one contribution per read before `log1p`,
and compiles its accumulation kernel with Numba. Each arm must lie on one
strand; a read can connect two strands. The resulting matrix is symmetric.
An externally supplied matrix must also be symmetric, finite, and N-by-N.
Scaling and result metadata are checked for overflow before a candidate can
enter ranking. Inputs outside the parser's numeric range raise `cplfold.InputError`;
they do not produce NaN or infinite scores. With absent or zero evidence,
alpha has no effect, including very large finite alpha values.

Alpha is retained in **both** search phases as
`search_energy = local_cofold_energy - alpha * sum(pair_support)`.
Phase 2 masks Phase 1 pairs and keeps the same nick and bonus matrix. When
Phase 1 already connects the strands, Phase 2 searches with zero additional
association cost. The merged structure is independently re-evaluated as a
whole, with a single association cost. Beta retains the original rule:
`effective_energy = energy + beta * phase1_energy` for pseudoknot candidates;
ordinary Phase 1 candidates use `effective_energy = energy`. Alpha is not
subtracted a second time during final energy ranking. Therefore increasing
alpha can change the candidate pool without necessarily changing the final
top candidate. Results additionally expose `cutpoint`, `strand_lengths`,
`intermolecular_pairs`, `read_support`, `read_bonus`, and energy metadata.

### Energy-model scope

| Candidate | Energy treatment |
|---|---|
| Pseudoknot-free cofold | Turner tables at 37 C, dangles=2, nicked faces treated as exterior; association cost 4.10 kcal/mol once if bound |
| Connected structure containing pseudoknots | **Experimental Turner/DP03 or Turner/DP09 ranking heuristic**; metadata reports `energy_kind="heuristic"` and the assumptions |
| Disconnected strands containing intramolecular pseudoknots | The same Turner/DP cofold heuristic, with no association cost; ordinary monomer contributions use the same Turner parameters as pseudoknot-free candidates |

Pseudoknot scoring follows actual base pairs and covalent backbone links;
the strand break terminates a backbone path. Ordinary loop faces use Turner
terms. Crossing-pair components and bands are identified without choosing
one strand as the first strand. The DP extension applies a component
initiation penalty, band penalties, and an unpaired-base penalty to bounded
same-strand regions, excluding exterior tails and bases already scored in
ordinary loops. True stacks and other intact internal loops use the selected
DP `stP` and `intP` factors respectively. The old linear-tree `Psm`, `Psp`,
and `Pps` context terms are not applied across a nick.

This fixes strand-order-dependent scores and avoids changing an independent
monomer's energy merely because the other monomer contains a pseudoknot.
Cofold intramolecular pseudoknot scores consequently need not equal the
legacy single-strand HotKnots scores; the original single-strand API retains
its original energy model. The cofold extension has **not been
thermodynamically validated**. Its scores are not validated binding free
energies or affinities. Every pair is retained in evaluation; no pseudoknot
is silently flattened. Metadata identifies the scoring revision.
CC06/CC09/RE and CONTRAfold are rejected for the new cofold API, rather than
silently substituted. All existing single-strand models remain available.

The chart stores dense quadratic arrays and prunes paired states by beam.
It does not claim the original C++ LinearCoFold's linear-space/time bounds.
The first native invocation compiles kernels; subsequent calls reuse the
Numba cache. To check the pure-Python implementation, set
`NUMBA_DISABLE_JIT=1`.

### Browser input and coordinates

In **RNA Structure**, select **CPLfold** and **Folding layout → Two local
regions · CPLcofold**. With **Reference region** as the source, set the RNA
name and 1-based inclusive start/end for **Region 1** and **Region 2**. Both
can refer to the same RNA with different coordinates, or to different RNAs.
The **Folding workflow** presets also support one region, two regions of the
same RNA, and two regions of different RNAs while keeping CPLfold selected.
Switching to two regions preserves reads mode, alpha, beta, and pseudoknot
settings. The resulting calculation uses CPLcofold.

Only the two extracted fragments are folded as separate strands. For example,
two 30-nt windows count as 60 nt even if separated by thousands of reference
bases. The omitted interval is not modelled as a covalent connection, and no
artificial linker is added. Unknown symbols outside the selected windows do
not block cofold; invalid symbols inside a window are rejected without
shifting coordinates. The reference path retains eligible HYB reads and
remaps them to the two selected fragments.

You can also paste two sequences. Region Explorer's **Send both regions to
CPLcofold** transfers the anchor and an explicitly bounded partner region
when CPLfold is selected, including a partner on the same RNA. Sending a
single region clears any previous second region. Both lengths count toward
the browser limit. Capacity estimates are
kept separately for single- and two-strand settings. Browser execution uses
Pyodide without Numba; use `bin/cplcofold` locally for JIT acceleration.

The version-6 JSON bridge accepts `sequence` plus `sequenceTwo`, `A&B`, or a
compact sequence plus integer `cutpoint`. Results keep `sequence`,
`dotBracket`, and all 1-based pair/read coordinates in compact A+B space;
`displaySequence`, `dotBracketWithBreak`, `cutpoint`, and `strandLengths`
preserve the two molecules for display and export. The engine is reported as
`CPLcofold`; this path is separate from the ViennaRNA RNAcofold evidence tool.
The same alpha/beta scoring and experimental pseudoknot-energy metadata are
retained. The reads transform clips each arm's Gaussian at its strand boundary.

After building the browser runtime, verify the actual worker, parameter
effects, and input/render/export paths with:

```bash
bash scripts/build/build-cplfold-web.sh
node scripts/verify/verify-cplfold-worker.mjs
node scripts/verify/verify-cplfold-parameters.mjs
node scripts/verify/verify-cplcofold-ui.cjs
node scripts/verify/verify-cplcofold-local-regions.mjs
```

```bash
python scripts/verify/verify-cplfold-python.py
python scripts/verify/verify-cplcofold-python.py
```

The second script verifies actual nopython compilation, Python/JIT parity,
reference cofold energies, reads bonuses, strand-order invariance,
independent-monomer energy consistency, overflow handling, and the
sequence/FASTA CLI paths. Numeric checks reject NaN and infinity explicitly;
the JSON exchange between Python and JIT runs also rejects nonfinite values.

To compare the public cofold API against a local C++ LinearCoFold checkout:

```bash
.venv-cplfold/bin/python scripts/benchmarks/compare-cplcofold-linearcofold.py \
  --reference ../VincentZhangHe-cofold-copartition-13c0beee5b11
```

The comparison uses alpha=0, disables pseudoknots, and checks beam sizes 100
and 0. It compiles the reference into a temporary directory and records
source hashes, full sequences, structures, energies, and pair-set differences.
The comparison validates structure lengths and strand boundaries and rescores
both predictions independently. A scoring mismatch or energy difference exits
with a nonzero status. Valid equal-energy alternatives are recorded as
`equal_energy_tie`; only matching structures are labelled `identical`.
The source-annotated panel contains three human mature/star miRNA duplexes
and yeast U4/U6 RNAs from PDB 5GAP; an upstream README example is recorded
separately because its biological identity is unverified. See
[the comparison report](../artifacts/cplcofold-comparison/report.md) and
[the exact results](../artifacts/cplcofold-comparison/results.json).
This is an implementation-agreement check, not a comparison with experimentally
measured energies or native secondary structures. The finite-beam parsers
prune different state families, so agreement on the panel does not guarantee
identical predictions for every input.

For the recorded local U4(1–35)/U6(50–90) example:

```bash
.venv-cplfold/bin/python scripts/benchmarks/compare-cplcofold-linearcofold.py \
  --fixtures artifacts/cplcofold-local-regions/fixtures.json \
  --output artifacts/cplcofold-local-regions
```

Each run refreshes the fixture snapshot, report, results, input FASTA, per-beam
pair tables, and summary together. Auxiliary files are at the output root for
one case or under `cases/<id>/` for multiple cases. FASTA keeps the fixture's
listed strand order; summaries identify the order used for comparison, and
pair tables map back to the appropriate source RNA even when the reference
swaps strands. Local source coordinates are 1-based inclusive and exclude
`&`. Missing source regions yield blank source-position columns, rather than
invented full-RNA coordinates.

`exports.json` records hashes for the managed output files and is written last.
Each file is replaced atomically after staging; the whole directory is not a
single transaction. Obsolete managed exports are removed on rerun while
unrelated files are retained. Both tested beams have their own pair table and
summary entry, so differences between beams remain visible.

To reproduce the local-window timing comparison, run:

```bash
.venv-cplfold/bin/python scripts/benchmarks/benchmark-cplcofold-local.py
```

This measures two U4/U6 window sizes with alpha=0 and pseudoknots disabled.
It records an empty-cache first call, warmed API/parser calls, and actual
fresh-process CLI runs with the Numba disk cache populated. The C++ parser is
timed through a thin wrapper that includes the unchanged reference source;
its original executable also checks results and CLI timings. All measured
outputs must match the reference structure and energy. See the
[timing report](../artifacts/cplcofold-local-benchmark/report.md) for timing
scopes, raw-sample locations, and reproduction options.

For the optimized single-candidate path, excluding cold compilation:

```bash
.venv-cplfold/bin/python scripts/benchmarks/benchmark-cplcofold-local.py --skip-cold \
  --fixtures artifacts/cplcofold-local-benchmark/fixtures.json \
  --output artifacts/cplcofold-local-optimized
```

This retains per-setting warmups and reports warmed API latency separately
from cached CLI startup. The earlier benchmark remains available as the
pre-optimization record; current measurements are in the
[optimized comparison](../artifacts/cplcofold-local-optimized/report.md).

## Generated consistency checks

The expanded checks use deterministic synthetic inputs and the pinned RNA
fixtures, including short and asymmetric strands, GC-controlled sequences,
repeated motifs, several beams, reads guidance and pseudoknot ranking:

```bash
.venv-cplfold/bin/python scripts/verify/verify-cplfold-python.py
env -u NUMBA_DISABLE_JIT .venv-cplfold/bin/python third_party/cplfold/tests/test_cofold_properties.py -v
.venv-cplfold/bin/python scripts/benchmarks/compare-cplfold-single-regression.py \
  --baseline aabae11 --output artifacts/cplfold-single-expanded
.venv-cplfold/bin/python scripts/benchmarks/compare-cplcofold-generated.py \
  --output artifacts/cplcofold-generated/full
```

The single-chain comparison checks all returned fields and candidate order
in separate baseline/worktree interpreters. The property tests include
nonempty first- and second-phase reads effects, actual beta-induced ranking
changes, input-route equivalence, constraints, energy windows and reindexed
pair-graph energies. The CLI matrix checks six input routes, rejects unexpected
crashes as validation successes, and requires every printed energy to be finite.

The generated C++ comparison runs 80 synthetic pairs and two pinned U4/U6
windows at beams 0, 1, 20 and 100. It preserves energy differences and
equal-energy alternative structures as distinct outcomes. Its current full
panel reports differences, including one energy difference at beam 0/100;
it therefore exits with status 1. This is a failed implementation-agreement
check, not a reason to discard the cases or redefine them as passes. See the
[full generated comparison](../artifacts/cplcofold-generated/full/report.md),
[single-chain comparison](../artifacts/cplfold-single-expanded/report.md),
and [combined validation record](../artifacts/cplcofold-expanded-validation/report.md).

## Development

CPLfold is part of Hyb2 and is developed in `third_party/cplfold/`. It is no
longer synced from the former CPLfold repository with `git subtree pull`.
`UPSTREAM.md` records the original import and the changes made since. Do not
commit `__pycache__` or machine-specific Numba cache files.

Change shared CPLfold/CPLcofold algorithms in `third_party/cplfold/` and their
shared Python adapter in `web/cplfold/`. Do not copy edits into generated web
assets or the staged desktop runtime. On a push changing either path on
`CPLcofold`, `integration/unified-release` or `main`, CI builds the website and
desktop packages from the same commit and checks their source against Git.

## Distribution status

Read `third_party/cplfold/THIRD_PARTY_NOTICES.md` before publishing this branch.
The LinearFold-derived parser and HotKnots-derived GPL energy evaluator have
different upstream license terms. This integration records both sets of terms
but does not claim that their combination has received a single redistribution
license. Obtain maintainer permission or qualified license review before public
redistribution or merging this branch into a published release.

Pages deployment, tagged desktop releases and uploads of bundled desktop
artifacts all require the repository Actions variable
`CPLFOLD_DISTRIBUTION_APPROVED=true`. Leave it unset until approval; browser
and desktop build checks still run without it. Once it is set, these replace
the published website:

- A push to `main` that touches the paths listed in
  `.github/workflows/deploy-pages.yml` (among them `web/`, `desktop/`,
  `pipeline/`, `third_party/` and `bin/`), or a manual run of that workflow on
  `main`, deploys the site built from that commit once its browser and desktop
  builds pass. It does not deploy if `main` has moved on, or if a desktop
  release published since the build changed the installer list.
- A desktop release (a `v*` tag) builds the site at its tag commit and deploys
  it, unless a newer version is already published.
