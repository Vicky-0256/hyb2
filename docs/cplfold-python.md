# CPLfold Python integration

The `feature/cplfold-python` branch includes a source snapshot of CPLfold at
`third_party/cplfold`. The snapshot was imported from
`https://github.com/Vicky-0256/CPLfold.git`, branch
`feature/pseudoknot-free-mode`, commit
`24bab521e0bcf2cff71f4567363aac1d5f5c97d5`.

## Runtime boundary

CPLfold's folding and pseudoknot-energy path is Python source and does not call
a HotKnots executable, shared library, wrapper, or external process. It is not
a standard-library-only implementation: the complete predictor requires NumPy
and Numba, and Numba uses LLVM JIT compilation. ViennaRNA is optional. SciPy
and pysam are needed only by the optional PARIS BAM extraction script.

The `feature/cplfold-python` branch adds the reproducible local CLI layer. The
separate `feature/cplfold-web` branch packages the same pure-Python sources,
parameters, Pyodide 0.29.4, and NumPy 2.2.5 as a static GitHub Pages artifact.
Pyodide does not provide Numba, so the browser bridge uses identity `njit`
decorators. The local CLI remains the supported route for longer workloads.
The browser measures a conservative session recommendation above a 75-nt
baseline, with a 500-nt hard browser ceiling. No server-side execution layer
is required.

For HYB-guided browser runs, each eligible row contributes its two prepared
reference intervals once to the original CPLfold/IRIS-style Gaussian,
symmetric outer-product and `log1p` bonus transform. The browser does not use
HYB overlap score or collapsed source-read count as matrix weights.

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
  --beam 20 --delta 5 --max-phase1 3 --max-phase2 1 \
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

Run the full vendored regression suite and a real-Numba smoke test with:

```bash
python scripts/verify-cplfold-python.py
```

The current `--max-phase2` option is retained for upstream CLI compatibility,
but upstream commit `24bab52` does not use it to enumerate multiple Phase 2
structures. Do not interpret that option as an effective search limit yet.

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
`CPLfold.cofold(a, b)`. Its default is explicitly `pseudoknot` mode, matching
the existing API: alpha=0, beta=0, beam=100, delta=5, max_phase1=10 and
max_phase2=5. Reads affect the search only when a matrix and a nonzero alpha
are supplied. `--mode standard` selects the single-candidate, pseudoknot-free
configuration used for the warmed cofold performance comparisons. Multiple
candidates and pseudoknot search take additional work. The original
`bin/cplfold` retains alpha=0.5 for single-strand compatibility; all two-strand
CLI routes now default to alpha=0, matching the Python API.
Both command help pages describe their defaults and supported input forms.

Alternatively, `--sequence GGGGCCCC --cutpoint 4` supplies the concatenated
sequence and the length of strand A. Sequence limits count nucleotides in
both strands, excluding `&`. Bonus-matrix dimensions and TSV coordinates
also exclude `&`: strand A is `1..cutpoint`, strand B starts at
`cutpoint+1`. A two-record FASTA requires `--cofold` with the legacy
`bin/cplfold` wrapper; the dedicated `bin/cplcofold` already enables it.
Ordinary single-record input retains the existing behavior. `--no-pseudoknot` is optional, and beta
is only applied to pseudoknot candidates. `--max-phase2` is effective for the
new cofold path and controls distinct constrained candidates per Phase 1.

`parse()` and `parse_subopt(max_structures=1)` use the compiled single-best
traceback. It walks the retained chart without constructing Python candidate
heaps or pair-index lists. Candidate order and floating-point addition order
match the general k-best path, including fractional reads weights and ties;
the inside chart, constraints, association cost, and alpha/beta scoring rules
are unchanged. Requests for multiple candidates retain lazy k-best enumeration.

For the Python API, put `third_party/cplfold` on the Python import path:

```python
from CPLfold import cofold
from Utils.cofold_bonus import build_cofold_bonus_matrix

a, b = "GGGG", "CCCC"
# Prepared A+B coordinates, 1-based inclusive: two arm intervals per read.
arms = [(1, 3, 6, 8), (2, 4, 5, 7)]
bonus = build_cofold_bonus_matrix(len(a) + len(b), len(a), arms)
results = cofold(a, b, mode="pseudoknot", bonus_matrix=bonus, alpha=0.5, beta=0.3,
                 beam_size=100, max_phase1=10, max_phase2=5, verbose=False)
best = results[0]
print(best["structure"], best["energy"], best["effective_energy"])
print(best["energy_metadata"])

# Ordinary single-candidate cofold with matching CLI defaults.
ordinary = cofold(a, b, mode="standard", verbose=False)
```

The existing API also accepts `two_phase_pseudoknot_fold(a + "&" + b, ...)`
or `two_phase_pseudoknot_fold(a + b, cutpoint=len(a), ...)`.

The public structure helpers accept compact or `&`-marked structures.
`get_pairs_from_structure` returns zero-based nucleotide pairs, excluding
`&`; `structure_to_constraint` returns a compact mask and masks all paired
positions in `()`, `[]`, and `{}`. `merge_structures` preserves a boundary
provided by either input and rejects inconsistent boundaries or pair conflicts.
Malformed structures raise `ValueError` in the extraction/mask helpers;
an invalid merge returns `None`.

The reads builder supports intra- and intermolecular arms. It clips each
Gaussian to its own strand, sums one contribution per read before `log1p`,
and compiles its accumulation kernel with Numba. Each arm must lie on one
strand; a read can connect two strands. The resulting matrix is symmetric.
An externally supplied matrix must also be symmetric, finite, and N-by-N.
Scaling and result metadata are checked for overflow before a candidate can
enter ranking. Inputs outside the parser's numeric range raise `ValueError`;
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
bash scripts/build-cplfold-web.sh
node scripts/verify-cplfold-worker.mjs
node scripts/verify-cplfold-parameters.mjs
node scripts/verify-cplcofold-ui.cjs
node scripts/verify-cplcofold-local-regions.mjs
```

```bash
python scripts/verify-cplfold-python.py
python scripts/verify-cplcofold-python.py
```

The second script verifies actual nopython compilation, Python/JIT parity,
reference cofold energies, reads bonuses, strand-order invariance,
independent-monomer energy consistency, overflow handling, and the
sequence/FASTA CLI paths. Numeric checks reject NaN and infinity explicitly;
the JSON exchange between Python and JIT runs also rejects nonfinite values.

To compare the public cofold API against a local C++ LinearCoFold checkout:

```bash
.venv-cplfold/bin/python scripts/compare-cplcofold-linearcofold.py \
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
.venv-cplfold/bin/python scripts/compare-cplcofold-linearcofold.py \
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
.venv-cplfold/bin/python scripts/benchmark-cplcofold-local.py
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
.venv-cplfold/bin/python scripts/benchmark-cplcofold-local.py --skip-cold \
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
.venv-cplfold/bin/python scripts/verify-cplfold-python.py
env -u NUMBA_DISABLE_JIT .venv-cplfold/bin/python third_party/cplfold/tests/test_cofold_properties.py -v
.venv-cplfold/bin/python scripts/compare-cplfold-single-regression.py \
  --baseline aabae11 --output artifacts/cplfold-single-expanded
.venv-cplfold/bin/python scripts/compare-cplcofold-generated.py \
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

## Updating the snapshot

The code was imported as a squashed Git subtree. `UPSTREAM.md` records the
small Hyb2-local changes made after import. After reviewing a new upstream
commit and its license state, update it with `git subtree pull` rather than by
copying the source directory. This avoids importing the upstream `.git`,
`__pycache__`, and machine-specific Numba cache files.

## Distribution status

Read `third_party/cplfold/THIRD_PARTY_NOTICES.md` before publishing this branch.
The LinearFold-derived parser and HotKnots-derived GPL energy evaluator have
different upstream license terms. This integration records both sets of terms
but does not claim that their combination has received a single redistribution
license. Obtain maintainer permission or qualified license review before public
redistribution or merging this branch into a published release.
