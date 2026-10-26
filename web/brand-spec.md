# HYB2 Web Lite — brand notes

Name, logo, palette and voice shared with HYB2 Desktop:
the brand guidelines (kept outside this repository). This file adds what is specific to the web app.

## Current asset state

- **Product:** HYB2 Web Lite, a browser-local workspace for HYB RNA interaction files.
- **Logo:** \`assets/hyb2-logo.png\` is the light-theme HYB2 wordmark; \`assets/hyb2-logo-dark.png\` is used automatically for the dark theme. Placement follows the brand guidelines.
- **External network assets:** none at runtime. The deployed artifact self-hosts pinned ViennaRNA, Pyodide, NumPy, and CPLfold files; it deliberately loads no remote images, fonts, scripts, analytics, or CDN resources after deployment.

## Design system

- **Narrative role:** a scientific analysis workspace, with a calm file-opening entry point rather than a marketing landing page.
- **Primary viewport:** desktop research workflows at laptop distance; tablet and phone layouts preserve loading and summary reading.
- **Visual temperature:** restrained, precise, and trustworthy.
- **Capacity:** dense analysis controls after a file is loaded; generous whitespace before loading.
- **Palette:** `#E64B35`, `#4DBBD5`, `#00A087`, `#3C5488`, `#F39B7F`, `#8491B4`, `#91D1C2`, `#DC0000`, `#7E6148`, `#B09C85`. Navy leads light-theme controls; cyan leads dark-theme controls. Vermilion/salmon provide emphasis, teal/mint success, brown warnings, and red errors. Neutral grey tracks follow the summary figure. Text and neutral surfaces use contrast-safe related shades. Existing raster wordmarks are preserved. Comparison figures follow `zafr_vs_zilm_summary.pdf`: condition one #E64B35, condition two #4DBBD5, similarity white–#00A087, tracks #CCCCCC/#A6A6A6 and structure windows #333333/#999999. Data colors are fixed across themes; numerical rules are unchanged.
- **Typography:** locally available system sans for interface copy; locally available monospace for sequence, coordinates, and data values.
- **Spacing:** 8 px base unit, with primary layout gaps at 8/16/24/32 px.
- **Radius:** 8 px content surfaces; 6 px controls; compact status pills.
- **Elevation:** hairline borders and a tight low-contrast shadow level inside the workspace. The landing page alone may use a faint dot lattice, a slow cyan/vermilion aura and the cyan→teal→vermilion brand gradient as a rule on the file panel.
- **Motion:** \`motion.css\` and \`motion.js\`. Entrances play only when the scene changes (first landing visit, landing panel stage, workspace entry, page change); re-renders within a scene paint the final state. Landing intro ≈1.5 s, workspace page change ≈0.5 s, hover/press feedback 80–260 ms. The landing hero is an *illustrative* arc diagram (labelled as such, generated, not user data). Theme switches use a circular View Transition from the pressed control. Everything is disabled under \`prefers-reduced-motion\` or the Tweaks panel's **Reduce motion** setting, which persists locally.
- **Interaction:** \`interaction.js\` / \`interaction.css\` act only through the app's own controls. ⌘K / Ctrl+K or \`/\` opens a command palette (pages, this page's visible actions, general commands, fuzzy match). \`g\` then a letter jumps to a page (o, i, c, v, r, p, s, e) and reveals the letters in the sidebar; \`[\` / \`]\` step through analysis pages. Analysis canvases show a crosshair and a pointer-following readout of the same text as the status line or native title. Dialogs take focus, trap Tab, close on Escape, and return focus to their opener.

## Current implementation scope

- Local HYB loading, streamed worker parsing, explicit standard-17-column / legacy-cluster semantics, chimera-type and homodimer-proxy filtering, virtualised record browsing, and local CSV/HYB/JSON exports. Standard column 16 is the overlap score, column 17 is chimera type, and collapsed source-read counts are recovered separately from the second underscore-delimited ID field without being reused as map weights.
- Contact Map offers `Palette → Colour / White and black`. Monochrome uses white for low values and black for high values on a white plot in either theme; PNG, SVG, report figures and export metadata follow the selection. Switching palette preserves data and zoom.
- Contact maps use the existing HYB2 fixed-bin accumulation rule in one large interactive canvas with region selection and resettable zoom. Data-table, TSV, SVG, and PNG exports run locally.
- Viewpoint provides per-nucleotide interaction-arm coverage (or larger-bin mean coverage), can switch between selected coordinates and the full mapped reference range, and can pass an interval to Region Explorer.
- Region Explorer, FASTA parsing, explainable exact-name matching, manual mapping, sequence extraction, and FASTA export run locally.
- Compare accepts multiple local HYB files, supports two assigned conditions, and visualises descriptive library-size-normalised log2 effects plus bins conserved across datasets. It exports legacy-HYB2-compatible raw counts and a two-column names table, plus separate extended metadata, but does not make statistical-significance claims until the pinned WebR package image passes its release gate.
- The RNA Structure workspace runs pinned ViennaRNA 2.7.2 MFE and RNAcofold functions in a dedicated Web Worker. CPLfold is the default engine and starts in pseudoknot-free mode; ViennaRNA remains available for nested MFE and evidence-guided workflows. Its expert **Manual hard base pairs** mode accepts one 1-based `i-j` pair per line, validates the pairs before calculation, enforces them in ViennaRNA, and labels them in the result and exports.
- **COMRADES(ViennaRNA)** reproduces the browser-suitable post-HYB ViennaRNA path: per-row cofolding, base-pair-frequency aggregation, touching-stem ranking, greedy compatibility fitting, seeded randomised constraint orders, COMRADES scoring, and evidence-coloured arcs. It does not claim general pseudoknot prediction.
- **CPLfold** is a separate pure-Python/Pyodide engine for nested and pseudoknotted candidates. Its HYB-guided mode visualises the IRIS-style interval-block bonus matrix and uses distinct arc colours for phase-1 and crossing phase-2 pairs. Candidate selection changes every structure result and export consistently. Because Numba JIT is unavailable, the browser measures a representative local run and derives a conservative session-specific recommendation above a 75-nt baseline, with a 500-nt hard safety ceiling.
- GitHub Pages deployment uses a static artifact workflow; no back-end, analytics, or runtime CDN is required. Third-party scientific runtimes are pinned and self-hosted in the artifact.
- FASTQ/SAM alignment and `.hyb` construction remain in the local CLI. UNAFold remains an optional external CLI compatibility route. A manual static-origin smoke was recorded for the official WebR 0.6.0 asset, but the runtime is not shipped or rerun in deployment CI; in-browser DESeq2 remains disabled until the pinned 1.52.0 dependency image loads from static assets and passes native-R numerical parity.

## Compare arc map

Panel C defaults to HYB-derived merged differential interactions from panel A or an imported interaction TSV, with condition one above in #E64B35 and condition two below in #4DBBD5. Predicted structure pairs are an explicit optional overlay. Arc height follows span and opacity follows the existing −log10(padj), saturating at 20. A completed comparison populates an empty arc map and refreshes its own previous result; manually imported tables are preserved. Structure-only inputs show an explanatory empty state by default.

## Interaction diagrams and navigation

- Structure Arc uses predicted pairs above and the saved, mapped raw HYB arm intervals below. Lower arcs join arm midpoints and never claim base-pair resolution. Missing raw inputs have an explicit empty state; displays above 2,000 mapped rows disclose the limit. These are the eligible records in the prepared-sequence coordinate system, not every input-file record.
- Compare differential, effect and similarity maps support wheel zoom, drag pan, zoom buttons and reset. Viewports reset for new results; on-screen hit testing and map image downloads use the visible range.
- Chimera patterns use SVG interval diagrams and descriptive names from Lau/hyb2/bin/chim_types and bin/hyb_chim_types.awk. Original classification codes are retained internally and in machine-readable exports. The complete guide covers the thirteen same-RNA relations plus different-RNA mappings.
