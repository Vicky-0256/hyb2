# CPLfold Python integration

The `feature/cplfold-python` branch includes a source snapshot of CPLfold at
`third_party/cplfold`. The snapshot was imported from
`https://github.com/Vicky-0256/CPLfold.git`, branch
`feature/standalone-pseudoknot-energy`, commit
`af49f8ea3177adba089fa480d49259ec1ba14d50`.

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

HYB2 Web Lite can download the prepared sequence as a single-record FASTA.
Run that file locally with the same wrapper:

```bash
bin/cplfold --sequence-file cplfold-input.fasta \
  --beam 20 --max-phase1 3 --max-phase2 2
```

`--sequence-file` also accepts a plain sequence text file. FASTA headers and
line wrapping are removed, and multiple FASTA records are rejected. The web
download contains the prepared RNA sequence only; browser-only HYB bonus
evidence is not passed to the local sequence-based CLI.

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
but upstream commit `af49f8e` does not use it to enumerate multiple Phase 2
structures. Do not interpret that option as an effective search limit yet.

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
