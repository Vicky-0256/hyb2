# IRIS browser integration

The third prediction engine is a browser adaptation of IRIS v1.0 from
`/Users/user/Documents/CPLfold/IRIS`, upstream commit
`4057c5ac0e038144e414c27f01cfd7b5a5be926f` (JY-Zhou/IRIS).
The reference core and ensemble implementation are preserved unmodified in
`third_party/iris/`, with the upstream GPL v3 licence. `web/iris-core.js` is
an adaptation under GPL-3.0-or-later; it is not the upstream Python executable.

## Computation

1. Map prepared HYB arms from 1-based inclusive to 0-based half-open blocks.
   Apply upstream Gaussian support redistribution (mean arm length / 6),
   peak normalization, symmetric outer products, float32 epsilon threshold,
   and log1p transformation. Each HYB row contributes once.
2. Collect canonical stems above the support quantile, remove contained
   stems, and enumerate compatible single- and double-stem constraints.
3. Fold each constraint with the existing ViennaRNA 2.7.2 WebAssembly
   engine at 37 °C, using the default minimum hairpin loop size of 3.
4. Average-linkage agglomerative clustering on base-pair distance retains
   the minimum-energy member of each cluster. Distance ties can be resolved
   differently from scikit-learn; no strict bitwise identity is claimed.
5. Enumerate K-member ensembles. Solve nonnegative least squares on the
   symmetric pairing matrix using active-set enumeration (K <= 3), then
   normalize the coefficients. Score ensembles using the IRIS Boltzmann
   prior and exponential evidence likelihood. Work in log space: the common
   partition-function constant and likelihood constants cancel in the
   argmax. The support scaling lambda uses the same log-probability range.

## Explicit browser scope and edge cases

- One reference region, up to 300 nt and 2,000 HYB rows; no BAM parser.
- C is 1–50 (default 20), K is 1–3 (default 2), with C >= K.
  Upstream only invokes the LASSO prefilter above 50 candidates, so that
  branch is outside this browser implementation's supported scope.
- Support quantile defaults to 0.75; stem lengths default to 4, 5, 6, 7.
- Up to 50 retained stems, 1,000 constrained folds and 20,000 ensembles.
  Requests exceeding limits fail clearly; evidence is never silently sampled.
- Missing evidence, no stems, fewer candidates than K, and degenerate
  energy/support ranges fail explicitly. The upstream implementation can
  divide by zero or leave the selected ensemble unset in these cases.
- Original progress logging divides by zero for fewer than ten constraints;
  browser progress reporting handles small jobs without modifying scoring.
- The displayed member is initially the one with the largest mixture weight.
  Member weights are fitted proportions, not confidence probabilities.
- All members, weights, parameters and ensemble statistics are included in
  the structure JSON export. Dot-bracket/CT and image exports show the
  selected member. Changing a member updates Folded, Arc and Matrix views.
- CPLfold alpha/beta do not apply to IRIS. IRIS estimates its own mixture
  coefficients by nonnegative least squares.

## Verification

`node scripts/verify-iris-worker.cjs` exercises the actual production worker
and generated ViennaRNA WebAssembly, using a short synthetic fixture.

`node scripts/verify-iris-ui.cjs` checks reference-coordinate preparation,
Worker request parameters, member selection and complete JSON export.
`node scripts/verify-iris-core.cjs` checks symmetric Gaussian support,
nonnegative least squares (including singular cases), compatible stems,
clustering and degenerate inputs.
`python scripts/verify-iris-reference.py` runs the original Python classes
against the synthetic Worker output in `/tmp/hyb2-iris-test-result.json`
(requires NumPy, SciPy, scikit-learn and ViennaRNA). The tested fixture agrees
on ensemble members, weights (<1e-7), mean energy (<1e-5 kcal/mol), and mean
support (<1e-8). The full support matrix agrees within 1e-12. This is a
fixture-level parity check, not a claim of identity on all inputs.
