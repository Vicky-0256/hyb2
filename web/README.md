# HYB2 Web Lite

This directory is a dependency-free static application intended for GitHub Pages.

## Local preview

Run a static server from the repository root:

~~~bash
python3 -m http.server 4173 --directory web
~~~

Then open http://localhost:4173/.

## Deployment

The repository workflow at .github/workflows/deploy-pages.yml uploads this directory as the GitHub Pages artifact. In repository **Settings → Pages**, choose **GitHub Actions** as the publishing source once the workflow is pushed to main.

## Privacy model

The page has no external runtime dependency, analytics, API call, CDN asset, or server-side upload path. HYB data and folding inputs are processed in Web Workers and held in tab memory only.

FASTA parsing removes layout whitespace only. Unsupported symbols are preserved and reported instead of being deleted, so reference coordinates never shift silently; structure folding rejects a selected region that contains them.

## HYB semantics and analysis coverage

- Standard 17-column HYB2 files retain column 16 as `overlap_score` and column 17 as `chimera_type`. Overlap scores are annotations, not read counts.
- For collapsed HYB2 IDs, the second underscore-delimited ID field is recovered separately as the source raw-read count. It is provenance/summary information, never a Contact Map or Viewpoint weight; those views count one per row in record mode or use explicitly selected legacy cluster support.
- Legacy clustered 16-column files retain `count_total=…` metadata as cluster support. Positive-integer legacy column 16 inputs are marked as ambiguous in Validation because they lack the standard column-17 type field; zero, negative, or decimal numeric values are retained as overlap scores from a partial 17-column row.
- The original HYB2 homodimer proxy is exposed separately from chimera type: both arms must name the same RNA and the column-16 overlap score must be at least 5.
- The workspace includes interaction filtering by chimera type and homodimer proxy, a three-stage contact-map navigator, a per-nucleotide Viewpoint graph, and local multi-file comparison maps. Viewpoint can use selected coordinates or the full mapped reference range after a matching FASTA is loaded.
- The Compare page provides descriptive library-size-normalised effect sizes and conserved bins. It exports `hyb2-web.table.txt` plus the two-column, headerless `hyb2-web_names.table` expected by the existing `bin/DESeq_run.R`, and a separate extended metadata TSV for audit or custom R adapters. Counts that cannot survive the legacy R integer conversion are rejected instead of being rounded or silently converted to zero. The page deliberately does not claim DESeq2 p-values or adjusted p-values while the pinned WebR package image remains behind its feasibility gate.
- A recorded manual browser smoke ran the official WebR 0.6.0 release asset and base R 4.6.0 from a local static origin with `PostMessage`. The site does not vendor that runtime and deployment CI does not rerun the smoke. The stable package repository lacks DESeq2; a separate R-universe development probe for DESeq2 1.53.2 resolved 42 of 43 packages and missed `locfit`, but it is explicitly not evidence for the pinned DESeq2 1.52.0 target. Reproducible harness, recorded result, package-index snapshot, and release gate are in `vendor/webr-deseq2/` and `../scripts/webr-smoke/`.
- Standard ViennaRNA MFE and RNAcofold are built as one static WebAssembly asset during deployment. HYB-guided structure mode runs one RNAcofold observation per eligible HYB row, aggregates base-pair frequencies, merges touching pairs into ranked stems, fits compatible hard constraints iteratively, optionally evaluates up to 1,000 seeded random orders, scores every result by COMRADES support, and colours the selected structure by evidence. UNAFold remains an optional external CLI compatibility path.

## Local structure prediction

The structure workspace loads a pinned ViennaRNA 2.7.2 RNAlib build in a dedicated Web Worker. It reports a global MFE in kcal/mol, dot-bracket notation, CT and base-pair exports, and an interactive SVG arc diagram. The normal UI accepts up to 2,000 nt; users may explicitly continue up to 3,000 nt after a browser-memory warning. Plain MFE remains the default; an expert may instead choose **Manual hard base pairs** and enter one 1-based `i-j` pair per line, relative to the prepared sequence.

Manual pairs must be in range, non-crossing canonical or G–U pairs, satisfy the selected minimum-loop size, and never reuse a nucleotide. The controller validates them before starting the worker; ViennaRNA enforces them, and the result is checked and labelled in the diagram and base-pair export. Report JSON records `constraintSource: "manual-user-input"` and explicitly marks automatic HYB/RNAcofold evidence generation as false for that mode.

**HYB-guided RNAcofold evidence** requires a loaded HYB file and mapped reference FASTA. Single-region mode covers short-range intramolecular interactions. Paired-region mode reproduces the original two-fragment assembly with the 100-nt `50 A + 50 U` RNA spacer and supports long-range, intermolecular, and overlap-score-defined homodimer subsets. Every eligible HYB row contributes one observation, independent of overlap score or collapsed-read provenance. The browser preserves the original 1,001-row evidence-selection nuance and 75-stem default. General pseudoknots remain outside the ViennaRNA dot-bracket result.

FASTQ/SAM alignment and HYB construction remain in the local CLI. The static application starts at `.hyb`; it never uploads those files or invokes a backend.

For an engine-enabled local preview, install `curl`, `tar`, `sha256sum`,
`pkg-config`, and Node, activate Emscripten, then run
`bash scripts/build-vienna-wasm.sh` before serving `web/`. The deployment
workflow pins Emscripten 3.1.57 and performs this build automatically.
