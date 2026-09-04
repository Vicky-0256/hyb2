# HYB2 Web Lite — brand notes

## Current asset state

- **Product:** HYB2 Web Lite, a browser-local workspace for HYB RNA interaction files.
- **Logo:** \`assets/hyb2-logo-placeholder.svg\` is an intentionally blank, local placeholder approved by the project owner on 2026-09-02. It must be replaced with the official HYB2 logo before a public brand launch.
- **External network assets:** none at runtime. The deployed artifact self-hosts pinned ViennaRNA, Pyodide, NumPy, and CPLfold files; it deliberately loads no remote images, fonts, scripts, analytics, or CDN resources after deployment.

## Design system

- **Narrative role:** a scientific analysis workspace, with a calm file-opening entry point rather than a marketing landing page.
- **Primary viewport:** desktop research workflows at laptop distance; tablet and phone layouts preserve loading and summary reading.
- **Visual temperature:** restrained, precise, and trustworthy.
- **Capacity:** dense analysis controls after a file is loaded; generous whitespace before loading.
- **Palette:** background \`#F6F8FB\`, surface \`#FFFFFF\`, ink \`#172033\`, muted \`#5F6B7A\`, border \`#DDE3EC\`, primary \`#176B87\`, primary-hover \`#12556C\`, accent \`#6D5BD0\`, success \`#167A5A\`, warning \`#A96614\`, error \`#B42318\`.
- **Typography:** locally available system sans for interface copy; locally available monospace for sequence, coordinates, and data values.
- **Spacing:** 8 px base unit.
- **Radius:** 12 px content surfaces; 8 px controls; compact status pills.
- **Elevation:** hairline borders and a single, diffuse low-contrast shadow level.
- **Motion:** 140–220 ms opacity/position feedback; disabled under \`prefers-reduced-motion\`.

## Current implementation scope

- Local HYB loading, streamed worker parsing, explicit standard-17-column / legacy-cluster semantics, chimera-type and homodimer-proxy filtering, virtualised record browsing, and local CSV/HYB/JSON exports. Standard column 16 is the overlap score, column 17 is chimera type, and collapsed source-read counts are recovered separately from the second underscore-delimited ID field without being reused as map weights.
- Contact maps use the existing HYB2 fixed-bin accumulation rule in three linked local canvases: overview → local map → bin detail. Data-table, TSV, SVG, and PNG exports run locally.
- Viewpoint provides per-nucleotide interaction-arm coverage (or larger-bin mean coverage), can switch between selected coordinates and the full mapped reference range, and can pass an interval to Region Explorer.
- Region Explorer, FASTA parsing, explainable exact-name matching, manual mapping, sequence extraction, and FASTA export run locally.
- Compare accepts multiple local HYB files, supports two assigned conditions, and visualises descriptive library-size-normalised log2 effects plus bins conserved across datasets. It exports legacy-HYB2-compatible raw counts and a two-column names table, plus separate extended metadata, but does not make statistical-significance claims until the pinned WebR package image passes its release gate.
- The RNA Structure workspace runs pinned ViennaRNA 2.7.2 MFE and RNAcofold functions in a dedicated Web Worker. Plain MFE is the default. Its expert **Manual hard base pairs** mode accepts one 1-based `i-j` pair per line, validates the pairs before calculation, enforces them in ViennaRNA, and labels them in the result and exports.
- **HYB-guided RNAcofold evidence** reproduces the browser-suitable post-HYB ViennaRNA path: per-row cofolding, base-pair-frequency aggregation, touching-stem ranking, greedy compatibility fitting, seeded randomised constraint orders, COMRADES scoring, and evidence-coloured arcs. It does not claim general pseudoknot prediction.
- **CPLfold** is a separate pure-Python/Pyodide engine for nested and pseudoknotted candidates. Its HYB-guided mode visualises the IRIS-style interval-block bonus matrix and uses distinct arc colours for phase-1 and crossing phase-2 pairs. Candidate selection changes every structure result and export consistently. The browser-only implementation is capped at 75 nt because Numba JIT is unavailable.
- GitHub Pages deployment uses a static artifact workflow; no back-end, analytics, or runtime CDN is required. Third-party scientific runtimes are pinned and self-hosted in the artifact.
- FASTQ/SAM alignment and `.hyb` construction remain in the local CLI. UNAFold remains an optional external CLI compatibility route. A manual static-origin smoke was recorded for the official WebR 0.6.0 asset, but the runtime is not shipped or rerun in deployment CI; in-browser DESeq2 remains disabled until the pinned 1.52.0 dependency image loads from static assets and passes native-R numerical parity.
