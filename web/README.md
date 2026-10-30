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

## Analysis scope

Every loaded HYB file is analysed: after comparison datasets load, Overview,
Interactions, Viewpoint, Region Explorer, RNA Structure and the Report read
all loaded datasets merged (counts summed, rows concatenated), unless another
scope was chosen in the header (all merged, one condition merged, or one
dataset). The Contact Map is never merged: it draws the one dataset chosen in
its Dataset control (default: the header's dataset when the scope is one,
else the primary file), its RNA X/Y list that dataset's RNAs, and a selected
cell's records and the parameter export name it. Overview's Per-dataset summary shows each file's
records, source reads, RNAs and pairs, whether it is in the current scope, and
condition totals. Compare keeps its own condition assignment. A Contact Map
that exceeds the browser budget at the requested bin size doubles the bin size
and says so. HYB binding energies rewrite the primary file only.

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

The landing flow is intentionally two-stage: after the HYB file is parsed, the page stays on a local file-review screen. Users can add or replace the optional reference FASTA there, then click **Enter analysis** to open the workspace. Interaction tables and contact maps do not require FASTA; reference-based RNA structure and full-reference Viewpoint do. Datasets on different genomes need each genome's sequence: choose several FASTA files at once, or use **Add FASTA** (Data panel or file-review screen) to join another file to the loaded reference instead of replacing it; matches already made, including manual ones, are kept.

## Shared UI conventions

`ui-kit.js` / `ui-kit.css` hold what the pages share: colour bars
(`Hyb2UiKit.legend.html / svg / drawCanvas`, drawn into PNG and SVG exports
as well as on the page), round axis ticks (`Hyb2UiKit.niceTicks`), and the
Export menu (`<details class="export-menu">`: every download of a page sits
in one menu, closed by an outside click, Escape or a chosen item). Tables
mark number columns `.num` (right-aligned, tabular figures) and long names
`.cell-truncate` with the full name in `title`. Image exports are drawn in
the light theme whatever the page's theme. Labels use the `--text-label`
(12px) and `--text-caps` (11px, uppercase) tokens in `styles.css`.
`scripts/ui-screenshots.py` takes before/after screenshots with the example
loaded and both Compare tests run; `scripts/verify-ui-kit.cjs` checks the kit.

## Report and result exports

After entering the workspace, choose **Report** in the header or sidebar. The
report page consolidates the current session's summary, validation, interaction
filters, Contact Map, Viewpoint, Region Explorer, Compare (with panels A to G
of the summary figure) and RNA Structure state, with the scope, version and
generation time at the top.

Everything the report states comes from one result record (`results.js`):
13 steps (S01 to S13) and 41 results (R01 to R41) with fixed ids, units,
definitions and a status (`ok`, `no_result`, `not_run`, `running`, `failed`,
`stale`, `pending`). A step that did not run is `not_run` and its results are
null, never 0. The page is in five parts with a contents
list: analysis objects and status (with each step's scope and an optional
purpose the researcher writes), main results, figures and data, methods and
sources, limitations and warnings. Numbers carry their result ids (for
example "Cells · R10") and every chart and table names the data file behind
it.

**Export** offers three packages, all built in the tab without uploading:

- **For people**: Print / Save PDF, or one offline HTML file with the styles
  and images inlined.
- **For AI**: `README.md`, a template `report.md` (no language model),
  `summary.json`, `data_dictionary.json`, `provenance.json`, `evidence.json`,
  bounded tables and `manifest.json` (sizes and SHA-256), as a ZIP or file by
  file, plus **Copy AI summary**.
- **For further analysis**: the full tables (every filtered record up to one
  million, every comparison cell, significant cells, regions, folding
  energies), the structures as dot-bracket and the figure SVGs, with the same
  dictionary, provenance and manifest.

Both packages carry `schema/` with a JSON Schema (2020-12) for each HYB2
JSON file (summary, provenance, data dictionary, evidence, structure; each
file names its own in `$schema`) and, under `results/`, every structure as
dot-bracket and as JSON with base pairs, coordinates and candidates.

Every result also has a `nature` (count, setting, descriptive,
statistical_test, prediction, derived_from_prediction, external_source),
each step its completion time where the module keeps one, and the record a
run id that changes when the inputs, settings, step results or completion
times change. Explanatory notes cite the steps, results or sources they
rest on. Unticking steps under Analysis status makes a "Selected results
report": the page and every export leave those steps out and say so. The
export panel lists each file's fields and lets the researcher leave files
out; README.md and manifest.json record what was left out. The input files
are never included, only their names and digests.
`scripts/verify-results.cjs` checks that the report page, Markdown, JSON and
tables agree; `verify-example.cjs` checks the record on the included example.

## Included example

**Try example** on the landing page loads the ZIKV virion comparison of the
ZAFR vs ZASI summary figure (`example.js`, files in
`assets/examples/zikv-virion/`, 6.9 MB, fetched only when it is used):
1-in-10 subsets (every 10th record) of ZAFR_REP1/2 (African lineage,
`ZIKV_African_KX377335-1_virusRNA`) and ZILM_REP2/3 (ZASI, French Polynesian,
`ZIKV_FrenchPolynesian_KJ776791-2_virusRNA`), gzip-compressed and
decompressed by the parser worker (`DecompressionStream`; any `.hyb.gz` can
be opened the same way), the African genome FASTA, CPLfold pseudoknot-free
genome folds of both lineages (`ZAFR.dbn`, `ZILM.dbn`) and the NC_035889
GenBank record (the ZASI genome FASTA joins the African one in a single
reference). ZAFR_REP1 is the primary file; once it is parsed the other
three replicates are fetched and parsed in workers for the Compare page
(conditions ZAFR / ZASI, 10-nt bins, similarity minimum 10) and the arc map
gets both folds and the gene track. The analysis pages then read all four
datasets merged (the header's scope selector also offers each condition
merged and each dataset alone), and Overview lists every dataset with its own
counts and each condition's totals. When the differential test finishes, an
arc map that has structures but no interaction table draws its merged
interactions, so panels A–G of the summary figure need no further file.
Because the data are a subset the numbers are not the published ones; the
original pipeline on the same subsets (`scripts/make-example-reference.sh`:
`plot_hybrids_3.awk`, `bin/hyb2_compare` with R DESeq2, `plot_similarity_map`,
`select_regions_cplfold.py`, `folding_energy.py`) gives the reference that
`scripts/verify-example.cjs` checks the browser code against: 20,958
similarity cells with cap 45; 8,353 significant cells (the browser has one
more, a cell at padj 0.05000 in R and 0.04999 here), 133 / 243 merged
interactions at padj < 0.05 and 66 / 153 at padj < 0.01 (one ZAFR-enriched
interaction is the exactly tied neighbour of R's pick); region A 355↔10265
(29 pairs in ZAFR, 0 in ZASI), region B 1625↔1905 (27 in ZASI, 0 in ZAFR);
folding energies n 66 / 153, paired t-test p 2.63e-8 (R on hyb2_compare's
interactions: 3.21e-8, the tie) / 3.22e-21. With
`HYB2_EXAMPLE_REFERENCE=<dir>` it also compares the similarity map, the
significant cells, the interactions and every RNAcofold energy one by one.

FASTA parsing removes layout whitespace only. Unsupported symbols are preserved and reported instead of being deleted, so reference coordinates never shift silently; structure folding rejects a selected region that contains them.

## HYB semantics and analysis coverage

- Standard 17-column HYB2 files retain column 16 as `overlap_score` and column 17 as `chimera_type`. Overlap scores are annotations, not read counts.
- For collapsed HYB2 IDs, the second underscore-delimited ID field is recovered separately as the source raw-read count. It is provenance/summary information, never a Contact Map or Viewpoint weight; those views count one per row in record mode or use explicitly selected legacy cluster support.
- Legacy clustered 16-column files retain `count_total=…` metadata as cluster support. Positive-integer legacy column 16 inputs are marked as ambiguous in Validation because they lack the standard column-17 type field; zero, negative, or decimal numeric values are retained as overlap scores from a partial 17-column row.
- The original HYB2 homodimer proxy is exposed separately from chimera type: both arms must name the same RNA and the column-16 overlap score must be at least 5.
- The Contact Map has eight colour styles: Mint–navy (follows the page theme), White–red (Juicebox style), White–blue, Heat (yellow–red), Viridis, Magma reversed, White and black, and Black and white (inverted); every style but Mint–navy is drawn on its own paper (white, or black for the inverted greys) on screen and in exports. It downloads as SVG (vector), PDF (one page, lossless image), PNG (2×) and print PNG (4×), all in the chosen style and visible extent, plus the contact cells (TSV), the bins × bins matrix of the whole map (CSV) and the parameters (JSON).
- The workspace includes interaction filtering by chimera type and homodimer proxy, a single large, zoomable contact map, a per-nucleotide Viewpoint graph, and local multi-file comparison maps. Viewpoint can use selected coordinates or the full mapped reference range after a matching FASTA is loaded.
- The Compare page provides descriptive library-size-normalised effect sizes and the HYB2 similarity heatmap. The similarity heatmap follows `bin/plot_similarity_map` and `bin/similarity_heatmap.R` run on one merged contact map per condition: each condition's datasets are summed (as if their .hyb files were concatenated), bins below the chosen similarity minimum are dropped from each map, a bin present in both maps scores the smaller raw count, and the counts are capped at their 95% quantile and drawn with a white-to-`#00A087` gradient matching the summary figure over the data range (white is the smallest plotted count, not zero; teal is the cap; CIELAB interpolation as in ggplot2). As in R, those smallest bins vanish on the white panel; the "Similarity panel" option draws the map on ggplot's grey panel (#EBEBEB) instead, changing only the display. The similarity minimum is a display filter: it equals running `awk '$3 >= MIN'` on each condition's merged contact.txt before `plot_similarity_map` (1 keeps every bin, the HYB2 default). On the ZIKV human (Livefire1-3) vs mosquito (Aag1-2) data with minimum 30 it reproduces all 201,576 bins of that reproduction (one merged, filtered contact.txt per condition passed to `plot_similarity_map -a A -i B`) exactly. This one-map-per-condition definition is the same as `plot_similarity_map` with one file per side and HYB2 Desktop's `differential.py`, and it intentionally differs from `bin/hyb2_compare` with several replicates per side: hyb2_compare concatenates the replicate contact.txt files and pairs consecutive occurrences of each bin (A1/A2, A2/B1, B1/B2, ...), so its minima mix within-condition replicate pairs with between-condition ones. The comparison holds up to 1,500,000 distinct cells (4,000,000 dataset cells): a whole ~10.8 kb genome works at 10 nt (the included example covers 838,111 cells). Results above 100,000 cells keep each dataset's values in typed arrays and build a cell's per-dataset values on demand (look cells up with `Hyb2Data.comparisonCellAt(result, x, y)`; `cellMap` is only filled for smaller results), the canvas maps are drawn once into a cached layer, and the effect-map SVG keeps the 100,000 strongest bins. For a homodimer comparison (RNA X = RNA Y) a dataset without that RNA contributes its own RNA with the closest name (another strain's genome, as ZAFR vs ZILM) and is compared bin by bin on its own coordinates, as `bin/hyb2_compare` compares each dataset's contact map by position (`Hyb2Data.comparisonRnaPlan`); the page says so, and the differential test card below uses the same choice and lets you change it. It exports `hyb2-web.table.txt` plus the two-column, headerless `hyb2-web_names.table` expected by the existing `bin/DESeq_run.R`, and a separate extended metadata TSV for audit or custom R adapters. Counts that cannot survive the legacy R integer conversion are rejected instead of being rounded or silently converted to zero. Adjusted p-values on the Compare page come only from the in-browser differential test below (a port, not R; the pinned WebR package image remains behind its feasibility gate).
- A recorded manual browser smoke ran the official WebR 0.6.0 release asset and base R 4.6.0 from a local static origin with `PostMessage`. The site does not vendor that runtime and deployment CI does not rerun the smoke. The stable package repository lacks DESeq2; a separate R-universe development probe for DESeq2 1.53.2 resolved 42 of 43 packages and missed `locfit`, but it is explicitly not evidence for the pinned DESeq2 1.52.0 target. Reproducible harness, recorded result, package-index snapshot, and release gate are in `vendor/webr-deseq2/` and `../scripts/webr-smoke/`.
- The Compare page runs the **HYB2 differential test** in the browser (`deseq.js`, `deseq-card.js`, `deseq.worker.js`): a JavaScript port of HYB2 Desktop's numpy port of the DESeq2 1.46 Wald test (`pipeline/hyb2analysis/deseq.py`, `differential.py`) followed by hyb2_compare's greedy merge of significant cells (padj order, within the merge range in x and y, per direction). Each dataset's arms are binned into 10-nt cells with the HYB2 coverage rule (record counts, identical to `plot_hybrids_3.awk USE_ENTIRE_HYBRIDS=1` contact.txt on the ZIKV files), independently of the descriptive maps; rows with a summed count above the min count are tested. It needs three or more datasets with two replicates in at least one condition; datasets can be left out, and a homodimer map may use another strain's genome in the datasets that lack the selected RNA. The test runs in a worker that spreads the per-row fits over helper workers (bit-identical results). Output: panel A, the differential coverage map (significant cells, red higher in A, blue higher in B, opacity by -log10 padj as Desktop), SVG/PNG, the merged interactions (`interactions.tsv` format) and significant cells as TSV, "Use in arc map", and a section in the printable report and the parameters JSON. Validation (`scripts/verify-deseq.cjs`, real data with `HYB2_DESEQ_REAL`): on ZAFR_REP1/2 vs ZILM_REP2/3 (1,038,539 tested cells) it gives the same 83,023 significant cells as R (20,660 up / 62,363 down), 668 / 1,725 merged interactions at padj < 0.05 and 378 / 1,243 at padj < 0.01 (one interaction is an exactly tied neighbour of R's pick: ties are merged in table order, as the Desktop port does), size factors 1.0299, 1.6076, 0.8491, 0.8093; over every tested cell log2 fold changes agree with the numpy port to 2e-9 and padj to 3e-7 (relative). On the 1-in-10 subsamples of the same files the gap is wider (padj within 0.3%, one cell on either side of 0.05; the merged interactions differ by one tied neighbour), because V8's `Math.log` is not always correctly rounded and the C library's is. A whole genome takes about 20 s single-threaded in Node.
- The Compare page also holds an **Arc map**: one or two structures (.dbn, windows placed by `name:start-end` headers, or .ct), the merged differential interactions of a comparison (HYB2 Desktop `interactions.tsv` or hyb2_compare `*_enrichment.txt`; padj cutoff, higher in condition one above, two below) and a gene track (GenBank, GFF3/GTF, BED or TSV), drawn as a PARIS-style SVG. Structure B, the cells of either condition and a GenBank annotation may sit on another reference (another strain): they are moved onto A's coordinates by a banded global alignment of the sequences (`arc-map.js`, the same algorithm as HYB2 Desktop's `seqmap`), and shared pairs are counted on A's coordinates. On the ZIKV ZAFR and ZILM genome folds it maps ZILM i to ZAFR i-1 after the indel at 72 and draws the 378 / 1,243 interactions with padj < 0.01 of the AFR vs ILM virion test; NC_035889's mature peptides land on ZAFR one nucleotide earlier.
- Below the arc map, two cards use the same files. **Region structures (D, E)** (`region-panels.js`) picks, for each direction, the most significant interaction (padj cutoff, sorted by padj) that the enriched lineage's own structure forms (a pair within ±reach nt of both arms, B's pairs moved onto A by alignment), cuts windows (extent of those pairs ± flank) from both structures and draws them joined by a 3-nt linker (ViennaRNA's simple polygon layout), with inter-window pairs in the lineage colour and bases paired outside hollow; a dropdown overrides the interaction. It reproduces `select_regions_cplfold.py` / `window_from_structure.py` exactly on the ZIKV folds. **Folding energy (F, G)** (`folding-energy.js`, `folding-energy.worker.js`) folds 2 × L-nt fragments of every significant interaction with RNAcofold (ViennaRNA WebAssembly, 37 °C) in both lineages and draws violins with a paired t-test (Student t via the regularised incomplete beta); on AFR vs ILM all 1,616 × 2 energies equal the RNAcofold CLI's and the p-values equal R's. FASTA references can be loaded when a structure file has no sequence. Checks: `scripts/verify-region-panels.cjs`, `scripts/verify-folding-energy.cjs` (`HYB2_PANELS_REAL=<dir>` for the real data).
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
the separate ViennaRNA temperature control does not apply. Choose **Folding
layout → Two local regions · CPLcofold** in the CPLfold panel and enter each
region's RNA name and inclusive start/end coordinates. The regions may come
from the same RNA or different RNAs; the corresponding workflow presets keep
CPLfold selected and preserve reads/alpha/beta/pseudoknot settings. The two
extracted fragments are folded as separate strands, excluding the intervening
reference sequence. Unknown symbols outside the selected windows do not
prevent folding. Two pasted sequences are also supported. Region Explorer
can transfer both an anchor and a bounded partner to CPLcofold. Without a
transfer, region 2 starts as region 1's most-supported partner window: the
201-nt window holding the most other arms of records with one arm in region 1
(kept on the RNA's mapped extent), never a copy of region 1. RNA Structure folds any RNA that a loaded dataset
maps to the reference, and its HYB evidence is, by default, every loaded
dataset with rows on the folded RNAs (replicates merged); "HYB evidence from"
can instead limit it to the header's analysis scope or to one dataset. The included example
loads both genomes (ZAFR.fasta + ZILM.fasta) in one reference: ZAFR regions
use ZAFR_REP1 + ZAFR_REP2 and ZASI regions ZILM_REP2 + ZILM_REP3; a ZAFR–ZASI
CPLcofold pair has no chimeras joining the lineages, so its evidence stays
within each strand. The sequence panel
marks the chain break with & on both rows; positions exclude it. The drawing
editor accepts two-strand results: no backbone joins the strands, automatic
end labels name A 5′/3′ and B 5′/3′, and edited CT and DBN keep the break.
Reference regions retain their eligible HYB reads with a
real strand break and no artificial spacer. All matrix/pair coordinates refer
to compact A+B; downloads preserve the break. Alpha guides both search phases,
and beta ranks pseudoknot candidates. Two-strand models are DP03 and DP09;
pseudoknot energies are explicitly experimental ranking heuristics. Capacity
tests distinguish single- and two-strand modes and count total nucleotides.
Longer or Numba-accelerated jobs use `bin/cplfold` for one RNA or
`bin/cplcofold --mode standard|pseudoknot` for two RNAs. This cofold predictor
is distinct from the ViennaRNA RNAcofold tool used to prepare COMRADES evidence.

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

### IRIS ensemble prediction

The Structure page offers IRIS as a third engine. It uses prepared HYB arms
and the existing local ViennaRNA WebAssembly runtime to return weighted
ensemble members. No added server, Python runtime download, or paid service
is needed. See `../docs/iris-browser.md` for original-source attribution,
browser limits, numerical parity tests and the differences from Python IRIS.
The browser adaptation source is `iris-core.js`; the upstream licence is
available at `vendor/iris/LICENSE`.

## Batch exports and drawing editor

- **Interactions → HYB binding energies** calculates column 3 for the valid
  records of the primary file: all of them, or only those matching the
  Interactions filters (the others keep their energy, audited as not
  selected). Mapped FASTA intervals (1-based inclusive, columns 7–8 and 13–14)
  supply the two arms, as in Lau's `hyb2fasta_bits_allRNAs.awk`; read-sequence
  slices are not substituted. Engines: **RNAcofold** (ViennaRNA, 37 °C,
  minimum loop 3, CLI-style two-decimal energies; the HYB2 pipeline's
  `sam2hyb` method and the default), **CPLcofold, nested** (Turner energies,
  beam search; on the ZIKV example it matched RNAcofold on 10 of 13 records and
  was up to 1.1 kcal/mol less negative on the others) and **CPLcofold with
  pseudoknots** (adds CPLfold's experimental DP09 pseudoknot score).
  CPLcofold runs as pure Python here, about 100 times slower per record
  (≈ 1.7 s against ≈ 15 ms), and is limited to 500 nt per pair; filter first,
  or use HYB2 Desktop or `bin/cplcofold` for whole files. Records are split
  over parallel workers (up to 8 for RNAcofold, 4 for CPLcofold, one core
  kept free): the 22,416-record ZAFR_REP1 example takes about a minute with
  RNAcofold instead of six. Browser workers inherit the tab's priority, so a
  Chrome window in the background runs several times slower. Download the
  updated HYB, or a ZIP with a per-row audit and parameters. Only column 3 is
  replaced in the source File; comments, blank/invalid lines, extra columns and
  line endings survive (gzip inputs produce an uncompressed HYB). Missing
  references, reversed intervals, ambiguous bases and combined arms over 2,000
  nt keep their previous column 3 and receive an audit reason. Runtime failures
  and cancellation never publish a partial result. The loaded dataset is not
  changed; load the downloaded HYB to analyse its updated energies.
- **Compare → differential result → Export Top 10 local maps per direction**
  ranks merged significant interactions by padj separately for up/down, keeping
  the result order for ties. Fewer than ten interactions yields fewer maps.
  Each interaction gets a ±150 nt window (minimum coordinate 1), one panel per
  included replicate, 10-nt full-arm record counts and a shared white–black
  scale. As in Lau's `contact_density_map_zoom.R`, the cap is a configurable pooled
  quantile of all nonzero full-map replicate cells (Lau `-q`, 0–1, default
  0.95; recorded in ZIP parameters and preview), with a ceiling for the
  colour scale and CIELAB interpolation. Replicates retain their own RNA
  coordinates, including any RNA substitution recorded by the test; this is
  not sequence-aligned. ZIP includes one SVG and raw-count TSV per interaction,
  an index, parameters and `preview.html` for offline viewing / printing to PDF.
  Stale tests cannot be exported. Processing runs in a cancellable worker;
  replicates over 50 million cell contributions or 1.5 million distinct cells fail explicitly.
- **Structure result → Edit drawing / return to prediction** opens a browser
  drawing editor modelled on VARNA. Layouts: Radiate (VARNA's default: loops
  as circles, straight helices, flat or circular exterior loop, top-level
  domains spaced so they do not collide), the ViennaRNA RNA polygon, circular
  and linear with arc pairs, each with adjustable base spacing; crossing pairs
  are drawn but do not shape the layout. The editor opens with Radiate and
  takes the full result width: a toolbar (undo/redo, zoom, layout, export),
  the canvas, and a side panel whose style changes apply at once; on narrow
  screens the panel moves below a canvas that stays in view. Plain scrolling
  scrolls the page; Ctrl/⌘ + scroll or a pinch zooms. Selection: click a
  base, Shift-click or Shift-drag a box to extend, double-click for its
  domain (helix plus everything it closes) or expand to the enclosing domain;
  drag or arrow-key bases (one undo step per drag), rotate a selection by any angle (domains
  turn about their closing pair) or mirror it, rotate or mirror the whole
  drawing, pan/zoom. Styles: per-base fill, letter and outline colours and
  radius; per-pair colour and width; global pair style (simple, Leontis–Westhof
  glyphs for cis Watson–Crick pairs: G·U a filled dot, other non-canonical
  pairs an open circle; RNAViz; none), straight or arc pairs, font, letter
  size, numbering interval/visibility/colour with outward ticks, backbone
  colour/width, background and title colour/size. Base colours start
  from COMRADES support for every engine: RNAcofold folds each eligible HYB
  record's two arms, the intermolecular pairs are mapped to the prepared
  sequence and counted once per record, and each base takes the count of the
  pair it forms in the shown structure (0 when unpaired or unsupported).
  COMRADES(ViennaRNA) scores its own result; IRIS and CPLfold results carry the
  same RNAcofold evidence (37 °C, minimum hairpin 3) and are scored against
  the selected candidate. A structure that shares no exact pair with the
  evidence stays uncoloured, and the editor says so. Colours use Lau's
  `plot_VARNA` RGB stops/interpolation over 0 to the structure's maximum
  (zero is white), with an optional website palette; they are
  original-prediction values, never re-estimated after editing. CPLfold's
  bonus matrix still sets the arc-view pair opacity. A VARNA-style
  colour map accepts one value per base or "position value" lines (e.g. SHAPE
  reactivities) with automatic or fixed minimum/maximum, a caption and Lau,
  website, heat, blue–white–red, greyscale or viridis palettes. Manual colours
  override either scale; Restore evidence colours and Clear colours undo
  them. Annotations: text anchored to selected bases, a helix or a loop (VARNA
  B/H/L types) or a free position, each with its own colour and size; region
  highlights with opacity and width; arrow/dot/triangle chemical-probe markers
  with direction and intensity; automatic 5′/3′, helix and hairpin labels.
  Hairpin detection rejects crossing structures instead of guessing. Group
  labels follow their bases when moved. Pair addition/removal, 50-step
  undo/redo and JSON save/load cover all of this. Exports: edited SVG and EPS
  (opacity flattened onto the background), edited CT, BPSEQ and DBN (crossing
  pairs as `[]`, `{}`, `<>`), and a VARNAcmd shell command that redraws the
  structure in the Java VARNA application with the same colours, colour map,
  highlights, annotations and probe marks (VARNA computes its own layout, so
  dragged positions are not carried over). Existing SVG/PNG image exports
  include the full edited drawing, without selection highlights. CT/DBN
  prediction exports still represent the prediction: edited pairs do not
  change its energy or evidence scores. A different predicted sequence/structure
  starts a new drawing. This is a browser editor, not the Java VARNA
  application: it has no NAView layout, does not read native VARNA session
  files and does not record Leontis–Westhof edges for non-canonical pairs.
  Drawing JSON is the editable session format.

`node scripts/verify-batch-tools.cjs` covers reference extraction, column-only
rewriting, map ranks/counts/caps/archives, drawing validation, CT and undo/redo.
`node scripts/verify-structure-editor.cjs` checks the Radiate geometry on
ViennaRNA structures (fixed backbone and pair lengths, no overlapping bases),
domains, styles, annotations, colour maps and the EPS/BPSEQ/DBN/VARNA exports.
