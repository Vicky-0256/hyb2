(function () {
  "use strict";

  const numberFormatter = new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 });
  const percentFormatter = new Intl.NumberFormat("en-US", { style: "percent", maximumFractionDigits: 1 });

  function render(state) {
    const summary = state.summary;
    const validPercentage = summary.validRecords + summary.invalidRecords
      ? summary.validRecords / (summary.validRecords + summary.invalidRecords)
      : 0;
    const dgPercentage = summary.validRecords ? summary.withDg / summary.validRecords : 0;
    const rawReadValue = summary.rawReadCountRecords ? format(summary.rawReadInteractions) : "—";
    const rawReadDescription = summary.rawReadCountRecords
      ? "Recovered from " + format(summary.rawReadCountRecords) + " sequence IDs"
      : "No HYB2 collapsed-read counts found in sequence IDs";

    return [
      '      <div class="page-heading">',
      "        <div>",
      "          <h1>Overview</h1>",
      '          <p><span class="mono">' + escape(summary.fileName) + "</span> · loaded locally · " + format(summary.validRecords) + " valid records</p>",
      "        </div>",
      '        <div class="inline-actions"><button class="button button-secondary" type="button" data-action="download-summary">Export summary</button><button class="button button-secondary" type="button" data-action="download-rna-counts">Export RNA counts</button><button class="button button-secondary" type="button" data-action="download-rna-pairs">Export RNA pairs</button></div>',
      "      </div>",
      renderReferenceStatus(state),
      '      <section class="metric-grid" aria-label="Dataset summary">',
      renderMetric("HYB records", format(summary.validRecords), "One valid tab-delimited record per row"),
      renderMetric("Source reads", rawReadValue, rawReadDescription),
      renderMetric("Unique RNAs", format(summary.uniqueRNAs), "Distinct RNA names across both arms"),
      renderMetric("RNA pairs", format(summary.uniquePairs), "RNA arm order is normalised for summary"),
      "      </section>",
      summary.ambiguousColumn16Records ? '<section class="notice notice-warning"><strong>Legacy column 16 detected.</strong> ' + format(summary.ambiguousColumn16Records) + ' 16-column rows contain a positive integer in column 16 without a chimera-type column. They are treated as legacy cluster support, following the bundled HYB2 merge format. If these rows are instead truncated standard 17-column records, add column 17 before analysis.</section>' : "",
      '      <section class="overview-grid">',
      renderTopRnas(summary.rnaCounts),
      renderInteractionTypes(summary),
      "      </section>",
      renderTopPairs(summary.pairCounts),
      '      <section class="data-card">',
      '        <div class="card-title-row"><h2>Data quality</h2><span class="card-kicker">Local validation</span></div>',
      '        <div class="quality-grid">',
      renderQualityStat(percentFormatter.format(validPercentage), "valid data rows"),
      renderQualityStat(percentFormatter.format(dgPercentage), "include a numeric dG value"),
      renderQualityStat(format(summary.homodimerRecords), "homodimer-proxy records (overlap ≥ 5)"),
      renderQualityStat(format(summary.invalidRecords), "invalid rows skipped"),
      "        </div>",
      "      </section>"
    ].join("");
  }

  function renderReferenceStatus(state) {
    if (state.fasta) {
      const mapped = Object.keys(state.fasta.mapping || {}).length;
      return [
        '      <section class="overview-callout" aria-label="Reference FASTA status">',
        "        <div>",
        "          <h2>Reference sequences loaded</h2>",
        "          <p>" + format(state.fasta.sequences.length) + " FASTA sequences · " + format(mapped) + " of " + format(state.summary.rnaNames.length) + " HYB RNA names matched locally.</p>",
        "        </div>",
        '        <button class="quiet-button" type="button" data-action="open-files">Review mapping</button>',
        "      </section>"
      ].join("");
    }

    return [
      '      <section class="overview-callout" aria-label="Reference FASTA status">',
      "        <div>",
      "          <h2>Reference sequences are not loaded</h2>",
      "          <p>Interaction analysis and contact maps are available now. Add a local FASTA to extract reference regions and prepare structure input.</p>",
      "        </div>",
      '        <button class="quiet-button" type="button" data-feature-action="choose-fasta">Add reference FASTA</button>',
      "      </section>"
    ].join("");
  }

  function renderMetric(label, value, description) {
    return [
      '        <article class="metric-card">',
      '          <span class="metric-label">' + escape(label) + "</span>",
      '          <strong class="metric-value">' + escape(value) + "</strong>",
      '          <span class="metric-description">' + escape(description) + "</span>",
      "        </article>"
    ].join("");
  }

  function renderTopRnas(items) {
    const maximum = items.length ? items[0].support : 1;
    return [
      '        <section class="data-card">',
      '          <div class="card-title-row"><h2>Top interacting RNAs</h2><span class="card-kicker">Cluster support</span></div>',
      '          <div class="bar-list">',
      items.slice(0, 5).map(function (item) {
        return [
          '            <div class="bar-row">',
          '              <span class="bar-name" title="' + attribute(item.name) + '">' + escape(item.name) + "</span>",
          '              <span class="bar-track"><span class="bar-fill" style="width:' + Math.max(3, (item.support / maximum) * 100) + '%"></span></span>',
          '              <span class="bar-value">' + format(item.support) + "</span>",
          "            </div>"
        ].join("");
      }).join(""),
      "          </div>",
      "        </section>"
    ].join("");
  }

  function renderInteractionTypes(summary) {
    const types = summary.chimeraTypes || [];
    const total = summary.validRecords || 1;
    return [
      '        <section class="data-card">',
      '          <div class="card-title-row"><h2>Chimera types</h2><span class="card-kicker">Column 17 / inferred</span></div>',
      '          <div class="type-split">',
      types.slice(0, 4).map(function (item) { return renderType(item.name, item.records / total, item.records); }).join(""),
      "          </div>",
      '          <button class="quiet-button" type="button" data-action="navigate" data-page="interactions">View interactions</button>',
      "        </section>"
    ].join("");
  }

  function renderType(label, fraction, count) {
    return [
      '            <div class="type-item">',
      "              <strong>" + escape(label) + "</strong>",
      "              <span>" + percentFormatter.format(fraction) + "</span>",
      '              <div class="type-meter"><span style="width:' + (fraction * 100) + '%"></span></div>',
      "              <span>" + format(count) + " records</span>",
      "            </div>"
    ].join("");
  }

  function renderTopPairs(items) {
    return [
      '      <section class="data-card">',
      '        <div class="card-title-row"><h2>Top RNA pairs</h2><span class="card-kicker">Normalised pairs</span></div>',
      '        <div class="data-table-wrap">',
      '          <table class="data-table">',
      "            <thead><tr><th>RNA 1</th><th>RNA 2</th><th>HYB records</th><th>Source reads</th><th>Cluster support</th></tr></thead>",
      "            <tbody>",
      items.slice(0, 6).map(function (item) {
        return "<tr><td>" + escape(item.rnaOne) + "</td><td>" + escape(item.rnaTwo) + "</td><td>" + format(item.records) + "</td><td>" + (item.rawReadCountRecords ? format(item.rawReads) : "—") + "</td><td>" + format(item.support) + "</td></tr>";
      }).join(""),
      "            </tbody>",
      "          </table>",
      "        </div>",
      "      </section>"
    ].join("");
  }

  function renderQualityStat(value, label) {
    return '<div class="quality-stat"><strong>' + escape(value) + "</strong><span>" + escape(label) + "</span></div>";
  }

  function renderValidation(state) {
    const summary = state.summary;
    const notices = [];
    if (summary.invalidRecords > 0) {
      notices.push('<p class="notice notice-warning">' + format(summary.invalidRecords) + " invalid rows were skipped. The file remains usable with its valid records.</p>");
    } else {
      notices.push('<p class="notice">No invalid data rows were found during local validation.</p>');
    }
    if (summary.ambiguousColumn16Records) {
      notices.push('<p class="notice notice-warning">' + format(summary.ambiguousColumn16Records) + ' rows use a positive-integer legacy column 16. They are interpreted as legacy cluster counts; the input has no column 17 type for those rows.</p>');
    }
    if (summary.errorSampleTruncated) {
      notices.push('<p class="notice notice-warning">The downloadable error sample contains the first ' + format(summary.errorSampleCount) + ' invalid rows; ' + format(summary.omittedErrorCount) + ' additional invalid-row details were omitted to keep browser memory bounded. The total invalid count above remains complete.</p>');
    }

    return [
      '      <div class="page-heading">',
      "        <div>",
      "          <h1>Validation</h1>",
      "          <p>Format checks completed in this browser while the file was read.</p>",
      "        </div>",
      "      </div>",
      '      <section class="validation-card">',
      "        <h2>" + escape(summary.fileName) + " is ready</h2>",
      '        <p class="validation-intro">The parser expects at least 15 tab-separated fields, RNA names in both arms, and positive one-based RNA start/end coordinates. Standard HYB2 17-column annotations are retained explicitly.</p>',
      '        <div class="validation-summary">',
      renderValidationItem(summary.validRecords, "Valid records"),
      renderValidationItem(summary.skippedCommentLines, "Skipped comment lines"),
      renderValidationItem(summary.skippedBlankLines, "Skipped blank lines"),
      renderValidationItem(summary.invalidRecords, "Invalid records"),
      renderValidationItem(summary.canonicalRecords || 0, "Standard 17-column records"),
      renderValidationItem(summary.overlapScoreRecords || 0, "Records with overlap score"),
      renderValidationItem(summary.rawReadCountRecords || 0, "IDs with collapsed-read count"),
      renderValidationItem(summary.homodimerRecords || 0, "Homodimer-proxy records"),
      "        </div>",
      notices.join(""),
      '        <div class="validation-actions">',
      '          <button class="button" type="button" data-action="navigate" data-page="overview">Continue with ' + format(summary.validRecords) + " valid records</button>",
      summary.invalidRecords ? '<button class="button button-secondary" type="button" data-action="download-validation">' + (summary.errorSampleTruncated ? "Download error sample" : "Download validation report") + '</button>' : "",
      "        </div>",
      "      </section>"
    ].join("");
  }

  function renderValidationItem(value, label) {
    return '<div class="validation-item"><strong>' + format(value) + "</strong><span>" + escape(label) + "</span></div>";
  }

  function renderMethods() {
    return [
      '      <div class="page-heading"><div><h1>Methods</h1><p>Local analysis rules, stated explicitly for reproducibility.</p></div></div>',
      '      <section class="data-card">',
      '        <div class="card-title-row"><h2>HYB file parsing</h2><span class="card-kicker">v2 semantics</span></div>',
      '        <p class="notice">Lines beginning with <span class="mono">#</span> and blank lines are skipped. A valid record has at least 15 tab-separated columns, a name for each RNA arm, and positive one-based RNA start/end coordinates in columns 7–8 and 13–14.</p>',
      '        <div class="card-title-row"><h2>Counts</h2></div>',
      '        <p class="notice">Record count equals the number of valid rows. For collapsed HYB2 IDs, the second underscore-delimited ID field is retained separately as the source raw-read count, matching the bundled <span class="mono">collapse_hyb_2.sh</span> and <span class="mono">hybrid_stats_2</span> convention. It never changes Contact Map or Viewpoint row weighting. In the standard 17-column layout, column 16 is an <strong>Overlap Score</strong>, not a count. Cluster support is read only from legacy <span class="mono">count_total=…</span> metadata or an untyped legacy positive-integer column 16.</p>',
      '        <div class="card-title-row"><h2>Chimera type</h2></div>',
      '        <p class="notice">Column 17 is retained as the HYB2 chimera type. For legacy 15/16-column records without that field, the website deterministically derives the legacy Type_1–Type_13 or Intermolecular label from the two RNA arms, matching the bundled <span class="mono">hyb_chim_types.awk</span> rule.</p>',
      '        <div class="card-title-row"><h2>Homodimer proxy</h2></div>',
      '        <p class="notice">A record is marked as the original HYB2 homodimer proxy only when both arms map to the same RNA and the standard overlap score in column 16 is at least 5. This flag is independent of the column 17 chimera type and is available as a filter.</p>',
      '        <div class="card-title-row"><h2>Pair normalisation</h2></div>',
      '        <p class="notice">Overview pair summaries order RNA names alphabetically, so A–B and B–A are counted together. The Contact Map preserves a selected X/Y axis and swaps reversed input arms onto that axis.</p>',
      '        <div class="card-title-row"><h2>Contact-map binning</h2></div>',
      '        <p class="notice">For every HYB record, all bins covered by arm 1 are paired with all bins covered by arm 2. The chosen count measure is added to each bin pair. Log1p only changes display colour scaling.</p>',
      '        <div class="card-title-row"><h2>Viewpoint and comparison</h2></div>',
      '        <p class="notice">Viewpoint plots interaction-arm coverage at one-nucleotide resolution or as binned mean coverage. Compare displays descriptive library-size-normalised effects and conserved bins, and exports the legacy HYB2 raw count and two-column names tables plus separate extended metadata. An official WebR 0.6 asset passed a recorded manual static-origin smoke, but it is not shipped or rerun in deployment CI; production DESeq2 remains gated until the pinned 1.52.0 WASM closure loads from static assets and passes native-R numerical parity.</p>',
      '        <div class="card-title-row"><h2>RNA structure</h2></div>',
      '        <p class="notice">The structure workspace accepts reference regions, HYB record sequences, and pasted sequences. Plain and manual modes run pinned ViennaRNA MFE WebAssembly. HYB-guided mode reproduces the post-HYB ViennaRNA path: one RNAcofold observation per eligible HYB row, base-pair-frequency aggregation, touching-stem ranking, iterative compatible F-constraint fitting, up to 1,000 seeded randomised folds, COMRADES scoring, and evidence-coloured structure arcs. UNAFold remains an optional external CLI compatibility path.</p>',
      "      </section>"
    ].join("");
  }

  function renderFileFormat() {
    const rows = [
      ["1", "Sequence / read ID; collapsed HYB2 IDs store source-read count in the second underscore-delimited field"],
      ["2", "Hybrid sequence"],
      ["3", "dG"],
      ["4", "Arm 1 RNA"],
      ["5–6", "Arm 1 read start / end"],
      ["7–8", "Arm 1 RNA start / end"],
      ["9", "Arm 1 e-value"],
      ["10", "Arm 2 RNA"],
      ["11–12", "Arm 2 read start / end"],
      ["13–14", "Arm 2 RNA start / end"],
      ["15", "Arm 2 e-value"],
      ["16", "Overlap Score in standard 17-column HYB2; legacy cluster count/metadata in older 16-column files"],
      ["17", "Type of Chimera in standard HYB2 output"]
    ];

    return [
      '      <div class="page-heading"><div><h1>File format</h1><p>The local validator accepts the HYB record layout described below.</p></div></div>',
      '      <section class="data-card">',
      '        <div class="card-title-row"><h2>Expected tab-delimited fields</h2><span class="card-kicker">1-indexed</span></div>',
      '        <div class="data-table-wrap"><table class="data-table"><thead><tr><th>Column</th><th>Field</th></tr></thead><tbody>',
      rows.map(function (row) { return "<tr><td>" + row[0] + "</td><td>" + row[1] + "</td></tr>"; }).join(""),
      "        </tbody></table></div>",
      "      </section>"
    ].join("");
  }

  function renderPrivacy() {
    return [
      '      <div class="page-heading"><div><h1>Privacy</h1><p>What happens to data opened in HYB2 Web Lite.</p></div></div>',
      '      <section class="data-card">',
      '        <div class="card-title-row"><h2>Local by design</h2><span class="card-kicker">No backend</span></div>',
      '        <div class="notice">HYB2 Web Lite is a static GitHub Pages application. The selected HYB file is passed from the file picker to a Web Worker inside the same browser tab. No upload, API request, analytics call, cloud save, or persistent browser database is used.</div>',
      '        <div class="card-title-row"><h2>Session lifetime</h2></div>',
      '        <div class="notice">Parsed results exist only in tab memory. Choosing “Clear session”, refreshing the page, or closing the tab removes the data from this version.</div>',
      "      </section>"
    ].join("");
  }

  function format(value) {
    return numberFormatter.format(value || 0);
  }

  function escape(value) {
    return String(value == null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#039;");
  }

  function attribute(value) {
    return escape(value).replace(/`/g, "&#096;");
  }

  window.Hyb2Overview = {
    render: render,
    renderValidation: renderValidation,
    renderMethods: renderMethods,
    renderFileFormat: renderFileFormat,
    renderPrivacy: renderPrivacy
  };
}());
