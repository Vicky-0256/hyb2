# HYB2 Web Lite

This directory is a static application intended for GitHub Pages. The deployed
artifact self-hosts its pinned WebAssembly/Python runtimes and makes no runtime
CDN or backend requests.

## Local preview

Run a static server from the repository root:

~~~bash
python3 -m http.server 4173 --directory web
~~~

Then open http://localhost:4173/.

## Deployment

The repository workflow at `.github/workflows/deploy-pages.yml` builds and
verifies this directory. Public artifact upload and deployment stay disabled
unless the repository Actions variable `CPLFOLD_DISTRIBUTION_APPROVED` is set
to `true`. Set it only after the CPLfold combined-distribution boundary below
has qualified licence review or the required permission. The published source
is the dedicated `feature/cplfold-web` branch; `main` remains unchanged. Choose
**GitHub Actions** as the publishing source in **Settings → Pages** before the
first deployment.

## Data access and embedding the analysis pages

Analysis pages (Overview, Interactions, Contact Map, Viewpoint, Region
Explorer, Compare, RNA Structure, Report) read data only through a
**DataProvider**; they never read files or parsed rows. The full contract,
with exact argument and return shapes, is the header comment of
`data-provider.js`. In short, every method is async and takes a selection
`{ datasetIds: [...] }` (one dataset, or several merged with counts summed):

| Method | Returns |
|---|---|
| `listDatasets()` | `[{ id, label, fileName, fileSize, sha256, condition, primary, validRecords, supportInteractions, rnaNames, reference }]` |
| `getSummary(sel)` | parser summary shape + `rnaExtents` |
| `getContactBins(sel, { rnaX, rnaY, binSize, measure, orientation, chimeraType, homodimer, maxCells?, region? })` | `{ binSize, length, length2, x: [], y: [], count: [], records: [], support: [], total, max, ... }` |
| `queryHybrids(sel, { filters?, region?, cell?, indices?, order?, offset, limit })` | `{ total, offset, limit, order, rows }` |
| `getTopInteractions(sel, { by: "partner" \| "pair" \| "bin", ... })` | `{ total, rows }` |
| `getSequence(sel, { rna, start?, end? })` | `{ rna, referenceId, start, end, length, sequence }` or `null` |
| `getArms(sel, { rna?, partner?, chimeraType?, homodimer?, start?, end? })` | columnar `{ total, index, id, rnaOne, oneStart, oneEnd, rnaTwo, twoStart, twoEnd, support, homodimer, chimeraType }` |

`BrowserProvider` implements it over the rows parsed by `parser.worker.js`;
the website shell registers each parsed file with `addDataset()` (Compare
files become extra datasets). `page-data.js` turns provider results into the
view state the synchronous page renderers use.

`app.js` auto-mounts the website on `#app`. A host can instead mount only the
analysis pages:

~~~js
window.HYB2_AUTO_MOUNT = false; // before app.js loads
await Hyb2Web.ready();
const view = Hyb2Web.mount(hostElement, {
  provider,                       // any DataProvider implementation
  shell: false,                   // no landing page, nav, uploads or help pages
  selection: { datasetIds: ["sample-1"] },
  page: "overview",
  onNavigate: ({ pageId, previousPageId, reason }) => {},
  onRequestFold: ({ source, region, partnerRegion, arms, selection }) => {}
});
view.navigate("contact-map");
await view.setSelection({ datasetIds: ["s1", "s2"] });
await view.refresh();   // re-query the provider (e.g. after new results)
// Cross-page paths: open a page focused on a bin or a region.
view.focus({ page: "contact-map", contact: { rnaX, rnaY, binSize, selection: { x, y } } });
view.focus({ page: "region", region: { rna, start, end, partner, partnerStart, partnerEnd } });
view.destroy();
~~~

With `onRequestFold`, Contact Map, Region Explorer and Compare offer "Fold this
region" and hand the region to the host instead of opening the structure page.
`Hyb2Web.loadProjectFiles()` and `restoreSession()` still work for the current
desktop build but are deprecated.

## Privacy model

The page has no analytics, API call, runtime CDN request, or server-side upload path. HYB data and folding inputs are processed in Web Workers and held in tab memory only. ViennaRNA, Pyodide, NumPy, and the CPLfold source archive are served from the same GitHub Pages artifact.

The landing flow is intentionally two-stage: after the HYB file is parsed, the page stays on a local file-review screen. Users can add or replace the optional reference FASTA there, then click **Enter analysis** to open the workspace. Interaction tables and contact maps do not require FASTA; reference-based RNA structure and full-reference Viewpoint do.

## Printable report

After entering the workspace, choose **Report** in the header or sidebar. The
report page consolidates the current session's summary, validation, interaction
filters, Contact Map, Viewpoint, Region Explorer, Compare, and RNA Structure
state in one print-oriented document. **Print / Save PDF** opens the browser
print dialog; choose **Save as PDF** there. A **Download report data** action
also saves a bounded JSON snapshot locally. Large tables and visual maps are
sampled for reliable printing; the existing page-level raw data and visual
exports remain available when the complete dataset is needed.

FASTA parsing removes layout whitespace only. Unsupported symbols are preserved and reported instead of being deleted, so reference coordinates never shift silently; structure folding rejects a selected region that contains them.

## HYB semantics and analysis coverage

- Standard 17-column HYB2 files retain column 16 as `overlap_score` and column 17 as `chimera_type`. Overlap scores are annotations, not read counts.
- For collapsed HYB2 IDs, the second underscore-delimited ID field is recovered separately as the source raw-read count. It is provenance/summary information, never a Contact Map or Viewpoint weight; those views count one per row in record mode or use explicitly selected legacy cluster support.
- Legacy clustered 16-column files retain `count_total=…` metadata as cluster support. Positive-integer legacy column 16 inputs are marked as ambiguous in Validation because they lack the standard column-17 type field; zero, negative, or decimal numeric values are retained as overlap scores from a partial 17-column row.
- The original HYB2 homodimer proxy is exposed separately from chimera type: both arms must name the same RNA and the column-16 overlap score must be at least 5.
- The workspace includes interaction filtering by chimera type and homodimer proxy, a single large, zoomable contact map, a per-nucleotide Viewpoint graph, and local multi-file comparison maps. Viewpoint can use selected coordinates or the full mapped reference range after a matching FASTA is loaded.
- The Compare page provides descriptive library-size-normalised effect sizes and the HYB2 similarity heatmap. The similarity heatmap follows `bin/plot_similarity_map` and `bin/similarity_heatmap.R` run on one merged contact map per condition: each condition's datasets are summed (as if their .hyb files were concatenated), bins below the chosen similarity minimum are dropped from each map, a bin present in both maps scores the smaller raw count, and the white-to-forestgreen colour scale is capped at the 95% quantile. On the ZIKV human (Livefire1-3) vs mosquito (Aag1-2) data with minimum 30 it reproduces all 201,576 bins of the CLI result exactly. The browser comparison limit (100,000 distinct cells) means a whole ~10.8 kb genome needs a bin size of 50 nt or more. It exports `hyb2-web.table.txt` plus the two-column, headerless `hyb2-web_names.table` expected by the existing `bin/DESeq_run.R`, and a separate extended metadata TSV for audit or custom R adapters. Counts that cannot survive the legacy R integer conversion are rejected instead of being rounded or silently converted to zero. The page deliberately does not claim DESeq2 p-values or adjusted p-values while the pinned WebR package image remains behind its feasibility gate.
- A recorded manual browser smoke ran the official WebR 0.6.0 release asset and base R 4.6.0 from a local static origin with `PostMessage`. The site does not vendor that runtime and deployment CI does not rerun the smoke. The stable package repository lacks DESeq2; a separate R-universe development probe for DESeq2 1.53.2 resolved 42 of 43 packages and missed `locfit`, but it is explicitly not evidence for the pinned DESeq2 1.52.0 target. Reproducible harness, recorded result, package-index snapshot, and release gate are in `vendor/webr-deseq2/` and `../scripts/webr-smoke/`.
- Standard ViennaRNA MFE and RNAcofold are built as one static WebAssembly asset during deployment. HYB-guided structure mode runs one RNAcofold observation per eligible HYB row, aggregates base-pair frequencies, merges touching pairs into ranked stems, fits compatible hard constraints iteratively, optionally evaluates up to 1,000 seeded random orders, scores every result by COMRADES support, and colours the selected structure by evidence. UNAFold remains an optional external CLI compatibility path.
- Pure-Python CPLfold is available as a separate browser engine. Sequence-only mode predicts nested and pseudoknotted candidates. HYB-guided mode converts the two prepared intervals from every eligible HYB row into CPLfold's IRIS-style Gaussian, symmetric outer-product, thresholded `log1p` bonus matrix; overlap score and collapsed source-read count are never reused as weights. The result includes a bonus-matrix view, switchable ranked candidates, pseudoknot-aware arc layers, CT/DBN/SVG/PNG output, base-pair TSV, bonus TSV, candidate TSV, and a provenance report.

## Local structure prediction

The structure workspace loads a pinned ViennaRNA 2.7.2 RNAlib build in a dedicated Web Worker. It reports a global MFE in kcal/mol, dot-bracket notation, CT and base-pair exports, and interactive SVG views for arc, radial, circular-contact, and symmetric base-pair matrix layouts. Arc remains the default; all views share nucleotide inspection, HYB/RNAcofold evidence colouring, CPLfold layer styling, and SVG/PNG export. For ViennaRNA, the normal UI accepts up to 2,000 nt; users may explicitly continue up to 3,000 nt after a browser-memory warning. CPLfold is the default engine and starts in pseudoknot-free mode; the **web** path has a separate 500-nt hard safety ceiling and a session-specific capacity recommendation measured in the browser. The desktop app can advertise a validated bundled Python/NumPy/CPLfold runtime, optionally with Numba JIT acceleration; when that capability is present, the app skips browser capacity measurement and delegates resource limits to its native task runner. Users may enable CPLfold pseudoknot search explicitly. ViennaRNA remains available for nested MFE, COMRADES(ViennaRNA), and manual hard base pairs; an expert may instead choose **Manual hard base pairs** and enter one 1-based `i-j` pair per line, relative to the prepared sequence.

Manual pairs must be in range, non-crossing canonical or G–U pairs, satisfy the selected minimum-loop size, and never reuse a nucleotide. The controller validates them before starting the worker; ViennaRNA enforces them, and the result is checked and labelled in the diagram and base-pair export. Report JSON records `constraintSource: "manual-user-input"` and explicitly marks automatic HYB/RNAcofold evidence generation as false for that mode.

**COMRADES(ViennaRNA)** requires a loaded HYB file and mapped reference FASTA. Single-region mode covers short-range intramolecular interactions. Paired-region mode reproduces the original two-fragment assembly with the 100-nt `50 A + 50 U` RNA spacer and supports long-range, intermolecular, and overlap-score-defined homodimer subsets. Every eligible HYB row contributes one observation, independent of overlap score or collapsed-read provenance. The browser preserves the original 1,001-row evidence-selection nuance and 75-stem default. General pseudoknots remain outside the ViennaRNA dot-bracket result.

**Optional CPLfold pseudoknot search** runs the vendored pure-Python two-phase
algorithm in Pyodide 0.29.4 with NumPy 2.2.5. Pyodide does not ship Numba, so
the browser bridge replaces `numba.njit` with an identity decorator. The
structure page measures a representative local run when a longer request is
needed and derives a conservative session-specific length recommendation. The
bridge and worker retain a 500-nt hard safety ceiling because CPLfold allocates
quadratic sequence-state arrays. CPLfold uses its bundled 37 °C energy tables;
the separate ViennaRNA temperature control does not apply. HYB block evidence is currently restricted to
one mapped reference region; this avoids the original Vienna workflow's 100-nt
artificial spacer becoming foldable CPLfold sequence. Longer or Numba-accelerated
CPLfold jobs use the local `bin/cplfold` command.

The vendored CPLfold snapshot has an unresolved combined-distribution licence
boundary. Review `third_party/cplfold/THIRD_PARTY_NOTICES.md` and obtain the
necessary permission or exception before publishing this branch publicly.

The molecule-like RNA secondary-structure view uses the locally vendored
`fornac` 1.2.0 renderer from ViennaRNA (Apache-2.0) with D3 3.5.17. The pinned
browser assets and license texts are stored under `vendor/fornac/` and
`vendor/d3/`; sequence and dot-bracket data remain in the browser.

FASTQ/SAM alignment and HYB construction remain in the local CLI. The static application starts at `.hyb`; it never uploads those files or invokes a backend.

For an engine-enabled local preview, install `curl`, `tar`, `zip`, `sha256sum`,
`pkg-config`, and Node, activate Emscripten, then run
`bash scripts/build-vienna-wasm.sh` and `bash scripts/build-cplfold-web.sh`
before serving `web/`. The deployment workflow pins Emscripten 3.1.57,
Pyodide 0.29.4, Python 3.13.2, and NumPy 2.2.5, verifies downloaded hashes,
packages the Python bridge under a content-addressed filename, verifies that
archive again inside the browser worker, and performs both builds
automatically.
