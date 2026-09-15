(function () {
  "use strict";

  const REPORT_VERSION = "0.5.0";
  const MAX_TABLE_ROWS = 12;
  const MAX_DATASET_ROWS = 15;
  const MAX_VISUAL_CELLS = 4200;
  const MAX_VISUAL_BINS = 180;
  const numberFormatter = new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 });

  function render(state) {
    const context = getReportContext(state || {});
    return [
      '<article class="report-page" data-report-page>',
      renderHero(context),
      renderSummary(context),
      renderSource(context),
      renderValidation(context),
      renderFileFormat(),
      renderInteractions(context),
      renderContactMap(context),
      renderViewpoint(context),
      renderRegion(context),
      renderComparison(context),
      renderStructure(context),
      renderMethods(),
      '</article>'
    ].join("");
  }

  function afterRender(state) {
    if (typeof document === "undefined") {
      return;
    }
    const target = document.querySelector("[data-report-structure-diagram]");
    if (!target || !window.Hyb2StructureUI || typeof window.Hyb2StructureUI.buildStructureDiagram !== "function") {
      return;
    }
    const result = state && state.structure && state.structure.result;
    if (!result) {
      return;
    }
    try {
      const diagram = window.Hyb2StructureUI.buildStructureDiagram(result, null, "arc");
      if (typeof target.replaceChildren === "function") {
        target.replaceChildren(diagram);
      } else {
        while (target.firstChild) {
          target.removeChild(target.firstChild);
        }
        target.appendChild(diagram);
      }
    } catch (error) {
      target.textContent = "The printable structure diagram is unavailable. Use the Structure page SVG export.";
    }
  }

  function getReportContext(state) {
    const source = state || {};
    const reportState = Object.assign({}, source);
    reportState.summary = source.summary || {};
    reportState.records = Array.isArray(source.records) ? source.records : [];
    reportState.filters = Object.assign({}, source.filters || {});
    ["contact", "viewpoint", "region", "comparison", "structure"].forEach(function (key) {
      reportState[key] = source[key] ? Object.assign({}, source[key]) : source[key];
    });

    const data = window.Hyb2Data || {};
    const derived = { contact: false, viewpoint: false, region: false, comparison: false };

    if (reportState.contact && !reportState.contact.matrix && reportState.contact.rnaX && reportState.contact.rnaY && reportState.records.length && typeof data.buildContactMatrix === "function") {
      try {
        reportState.contact.matrix = data.buildContactMatrix(reportState.records, reportState.contact);
        derived.contact = true;
      } catch (error) {
        reportState.contact.matrix = { ready: false, reason: "Contact Map could not be prepared for the report snapshot." };
      }
    }

    if (reportState.viewpoint && !reportState.viewpoint.results && typeof data.getViewpointResults === "function") {
      try {
        reportState.viewpoint.results = data.getViewpointResults(reportState);
        derived.viewpoint = true;
      } catch (error) {
        reportState.viewpoint.results = { ready: false, bins: [], records: [], reason: "Viewpoint could not be prepared for the report snapshot." };
      }
    }

    if (reportState.region && !reportState.region.results && typeof data.getRegionResults === "function") {
      try {
        reportState.region.results = data.getRegionResults(reportState);
        derived.region = true;
      } catch (error) {
        reportState.region.results = { records: [], partners: [], profile: [], support: 0, reason: "Region Explorer could not be prepared for the report snapshot." };
      }
    }

    if (reportState.comparison && !reportState.comparison.result && !reportState.comparison.loading && typeof data.buildComparisonResults === "function") {
      try {
        reportState.comparison.result = data.buildComparisonResults(reportState.comparison);
        derived.comparison = true;
      } catch (error) {
        reportState.comparison.result = { ready: false, cells: [], conservedCells: [], reason: "Comparison could not be prepared for the report snapshot." };
      }
    }

    let filteredRecords = reportState.records;
    if (typeof data.getFilteredRecords === "function") {
      try {
        filteredRecords = data.getFilteredRecords(reportState);
      } catch (error) {
        filteredRecords = [];
      }
    }

    let partnerCounts = [];
    if (typeof data.getPartnerCounts === "function") {
      try {
        partnerCounts = data.getPartnerCounts(filteredRecords, reportState.filters.rna || "", reportState.filters.countMode || "support");
      } catch (error) {
        partnerCounts = [];
      }
    }

    return {
      state: reportState,
      filteredRecords: filteredRecords,
      partnerCounts: partnerCounts,
      derived: derived,
      generatedAt: new Date().toISOString()
    };
  }

  function renderHero(context) {
    const summary = context.state.summary || {};
    const fileName = summary.fileName || "Untitled HYB file";
    return [
      '<header class="report-hero">',
      '  <div class="report-hero-top">',
      '    <div class="report-identity">',
      '      <span class="report-logo-wrap report-logo-light"><img src="./assets/hyb2-logo.png" alt="HYB2"></span>',
      '      <span class="report-logo-wrap report-logo-dark"><img src="./assets/hyb2-logo-dark.png" alt="HYB2"></span>',
      '      <span><span class="report-product">HYB2 Web Lite</span><span class="report-document-label">Consolidated analysis report</span></span>',
      "    </div>",
      '    <div class="report-actions" aria-label="Report actions">',
      '      <button class="button" type="button" data-action="print-report">Print / Save PDF</button>',
      '      <button class="button button-secondary" type="button" data-action="download-full-report">Download report data</button>',
      '      <button class="quiet-button" type="button" data-action="navigate" data-page="overview">Back to workspace</button>',
      "    </div>",
      "  </div>",
      '  <div class="report-hero-copy">',
      '    <span class="report-kicker">Local session snapshot</span>',
      '    <h1>Analysis report</h1>',
      '    <p class="report-lede">A print-ready view of the current HYB2 workspace, including configured analyses, generated results, selected intervals, and reproducibility details.</p>',
      '    <div class="report-hero-meta"><span><strong>Source</strong> <code>' + escape(fileName) + '</code></span><span><strong>Generated</strong> <code>' + escape(formatTimestamp(context.generatedAt)) + '</code></span><span class="report-status is-ready">Processed locally</span></div>',
      "  </div>",
      '  <p class="report-print-note">Use your browser\'s Print dialog and choose <strong>Save as PDF</strong>. The report is generated in this tab and no data is uploaded.</p>',
      "</header>"
    ].join("");
  }

  function renderSummary(context) {
    const summary = context.state.summary || {};
    const totalRows = number(summary.validRecords) + number(summary.invalidRecords);
    const validRatio = totalRows ? number(summary.validRecords) / totalRows : 0;
    const fasta = context.state.fasta;
    const mapped = fasta ? mappedFastaCount(fasta) : 0;
    const fastaValue = fasta ? formatNumber(fasta.sequences ? fasta.sequences.length : 0) + " sequences" : "Optional / not loaded";
    const fastaDetail = fasta ? formatNumber(mapped) + " mapped HYB RNA names" : "Required for reference-based structure";
    return section(
      "summary",
      "Executive summary",
      "Session snapshot",
      '<span class="report-status is-ready">Ready</span>',
      [
        '<div class="report-metric-grid">',
        metric("Valid HYB records", formatNumber(summary.validRecords), "Rows available for analysis"),
        metric("Valid ratio", formatPercent(validRatio), formatNumber(summary.invalidRecords) + " invalid rows skipped"),
        metric("Unique RNAs", formatNumber(summary.uniqueRNAs), "Distinct names across both arms"),
        metric("RNA pairs", formatNumber(summary.uniquePairs), "Normalised pair identities"),
        metric("Cluster support", formatNumber(summary.supportInteractions), "Support units retained from HYB metadata"),
        metric("Reference FASTA", fastaValue, fastaDetail),
        "</div>",
        '<div class="report-grid report-analysis-grid report-summary-breakdown">',
        '<div class="report-subsection"><h3>Top interacting RNAs</h3>' + renderTopRnaTable(summary.rnaCounts) + '</div>',
        '<div class="report-subsection"><h3>Top RNA pairs</h3>' + renderTopPairTable(summary.pairCounts) + '</div>',
        "</div>",
        '<div class="report-callout"><strong>Report scope</strong><span>Tables and inline visuals are bounded for reliable browser printing. Existing page-level CSV, TSV, HYB, SVG, PNG, FASTA, and structure exports remain the complete data path.</span></div>'
      ].join("")
    );
  }

  function renderSource(context) {
    const state = context.state;
    const summary = state.summary || {};
    const fasta = state.fasta;
    const fastaMapped = mappedFastaCount(fasta);
    const sourceHyb = sourceHybMetadata(state);
    const sourceFasta = sourceFastaMetadata(state);
    return section(
      "source",
      "Source and provenance",
      "Input files",
      '<span class="report-status is-ready">Local only</span>',
      [
        '<div class="report-grid report-source-grid">',
        '<div class="report-subsection"><h3>HYB input</h3>' + definitionList([
          ["File", sourceHyb.fileName, true],
          ["Size", formatBytes(sourceHyb.fileSize)],
          ["SHA-256", sourceHyb.sha256 || sourceHyb.sha256Status, true],
          ["Valid records", formatNumber(summary.validRecords)]
        ]) + '</div>',
        '<div class="report-subsection"><h3>Reference FASTA</h3>' + definitionList([
          ["File", sourceFasta ? sourceFasta.fileName : "Not loaded", !!sourceFasta],
          ["Size", sourceFasta ? formatBytes(sourceFasta.fileSize) : "Optional"],
          ["SHA-256", sourceFasta ? (sourceFasta.sha256 || sourceFasta.sha256Status) : "Not available", !!sourceFasta],
          ["Mapped names", sourceFasta ? formatNumber(fastaMapped) + " / " + formatNumber((summary.rnaNames || []).length) : "0"]
        ]) + '</div>',
        '<div class="report-subsection"><h3>Build context</h3>' + definitionList([
          ["Application", "HYB2 Web Lite"],
          ["Version", REPORT_VERSION, true],
          ["Build commit", window.HYB2_BUILD && window.HYB2_BUILD.commit || "Local development build", true],
          ["Processing", "Browser-local; no server upload"]
        ]) + '</div>',
        "</div>",
        '<p class="report-note">' + (fasta ? "The loaded FASTA is available for reference-based Viewpoint and RNA Structure workflows." : "No FASTA was loaded in this session. Interaction analysis and contact maps remain available; reference-based structure workflows will remain unavailable until a FASTA is added.") + '</p>'
      ].join("")
    );
  }

  function renderValidation(context) {
    const summary = context.state.summary || {};
    const errors = Array.isArray(summary.errors) ? summary.errors.slice(0, MAX_TABLE_ROWS) : [];
    const errorRows = errors.map(function (item) {
      return [formatNumber(item.line), escape(item.error || "Invalid row"), '<code>' + escape(truncateText(item.content || "", 120)) + '</code>'];
    });
    const status = number(summary.invalidRecords) ? '<span class="report-status is-warning">Review needed</span>' : '<span class="report-status is-ready">Passed</span>';
    return section(
      "validation",
      "Validation",
      "Parser and data quality",
      status,
      [
        '<div class="report-metric-grid report-metric-grid--compact">',
        metric("Valid records", formatNumber(summary.validRecords), "Retained"),
        metric("Invalid records", formatNumber(summary.invalidRecords), "Skipped"),
        metric("Blank lines", formatNumber(summary.skippedBlankLines), "Ignored"),
        metric("Comment lines", formatNumber(summary.skippedCommentLines), "Ignored"),
        metric("Numeric dG", formatNumber(summary.withDg), "Valid records with dG"),
        metric("Overlap score", formatNumber(summary.overlapScoreRecords), "Standard column 16"),
        metric("Collapsed IDs", formatNumber(summary.rawReadCountRecords), "Raw-read metadata found"),
        metric("Homodimer proxy", formatNumber(summary.homodimerRecords), "Overlap score >= 5"),
        "</div>",
        summary.ambiguousColumn16Records ? '<div class="report-callout report-callout--warning"><strong>Legacy column 16 detected</strong><span>' + formatNumber(summary.ambiguousColumn16Records) + ' rows were interpreted as legacy cluster support because no column 17 chimera type was present.</span></div>' : "",
        summary.errorSampleTruncated ? '<p class="report-note">The browser retained the first ' + formatNumber(summary.errorSampleCount) + ' validation details and omitted ' + formatNumber(summary.omittedErrorCount) + ' additional error details from memory. The complete invalid-row count remains ' + formatNumber(summary.invalidRecords) + '.</p>' : "",
        errors.length ? '<div class="report-table-wrap">' + table(["Line", "Error", "Content sample"], errorRows) + '</div>' : '<div class="report-empty is-ready">No invalid row details were retained.</div>'
      ].join("")
    );
  }

  function renderTopRnaTable(items) {
    const rows = (Array.isArray(items) ? items : []).slice(0, 8).map(function (item) {
      return [escape(item.name || ""), formatNumber(item.records), formatNumber(item.support)];
    });
    return rows.length ? table(["RNA", "Records", "Support"], rows) : '<div class="report-empty">No RNA summary is available.</div>';
  }

  function renderTopPairTable(items) {
    const rows = (Array.isArray(items) ? items : []).slice(0, 8).map(function (item) {
      return [escape(item.rnaOne || ""), escape(item.rnaTwo || ""), formatNumber(item.records), formatNumber(item.support)];
    });
    return rows.length ? table(["RNA 1", "RNA 2", "Records", "Support"], rows) : '<div class="report-empty">No RNA-pair summary is available.</div>';
  }

  function renderInteractions(context) {
    const state = context.state;
    const filters = state.filters || {};
    const records = context.filteredRecords || [];
    const partners = context.partnerCounts || [];
    const rows = boundedItems(records, MAX_TABLE_ROWS, sortRecords).map(function (record) {
      return [
        '<code>' + escape(truncateText(record.id || "", 34)) + '</code>',
        escape((record.rnaOne || "") + " <-> " + (record.rnaTwo || "")),
        '<code>' + escape(range(record.rnaOneStart, record.rnaOneEnd)) + '</code>',
        '<code>' + escape(range(record.rnaTwoStart, record.rnaTwoEnd)) + '</code>',
        escape(record.chimeraType || "Unknown"),
        formatNumber(record.supportCount),
        record.dg == null ? "-" : formatNumber(record.dg)
      ];
    });
    const filterEntries = [];
    if (filters.rna) filterEntries.push(["RNA", filters.rna]);
    if (filters.partner) filterEntries.push(["Partner", filters.partner]);
    if (filters.type && filters.type !== "all") filterEntries.push(["Class", filters.type]);
    if (filters.chimeraType && filters.chimeraType !== "all") filterEntries.push(["Chimera type", filters.chimeraType]);
    if (filters.minSupport && filters.minSupport !== "1") filterEntries.push(["Minimum support", filters.minSupport]);
    if (filters.search) filterEntries.push(["Search", filters.search]);
    const partnerRows = partners.slice(0, 10).map(function (item) {
      return [escape(item.name), formatNumber(item.records), formatNumber(item.support)];
    });
    return section(
      "interactions",
      "Interactions",
      "Filtered record view",
      '<span class="report-status is-ready">' + formatNumber(records.length) + " matches</span>",
      [
        '<div class="report-grid report-analysis-grid">',
        '<div class="report-subsection"><h3>Current filters</h3>' + (filterEntries.length ? definitionList(filterEntries) : '<div class="report-empty">No additional filters; all valid records are included.</div>') + '</div>',
        '<div class="report-subsection"><h3>Selected RNA partners</h3>' + (filters.rna && partnerRows.length ? table(["Partner", "Records", "Support"], partnerRows) : '<div class="report-empty">Choose an RNA on the Interactions page to populate partner counts.</div>') + '</div>',
        "</div>",
        '<div class="report-subsection report-subsection--table"><div class="report-subsection-heading"><h3>Top matching records</h3><span class="report-note-inline">Showing ' + formatNumber(Math.min(records.length, MAX_TABLE_ROWS)) + ' of ' + formatNumber(records.length) + '</span></div>',
        rows.length ? table(["Sequence ID", "RNA pair", "RNA 1 interval", "RNA 2 interval", "Chimera type", "Support", "dG"], rows) : '<div class="report-empty">No records match the current filter state.</div>',
        records.length > MAX_TABLE_ROWS ? '<p class="report-note">The full filtered table remains available from the Interactions page export.</p>' : "",
        "</div>"
      ].join("")
    );
  }

  function renderFileFormat() {
    const rows = [
      ["1", "Sequence / read ID"],
      ["2", "Hybrid sequence"],
      ["3", "dG"],
      ["4", "Arm 1 RNA"],
      ["5-6", "Arm 1 read start / end"],
      ["7-8", "Arm 1 RNA start / end"],
      ["9", "Arm 1 e-value"],
      ["10", "Arm 2 RNA"],
      ["11-12", "Arm 2 read start / end"],
      ["13-14", "Arm 2 RNA start / end"],
      ["15", "Arm 2 e-value"],
      ["16", "Overlap score in standard 17-column HYB2; legacy cluster support in older layouts"],
      ["17", "Chimera type in standard HYB2 output"]
    ];
    return section(
      "file-format",
      "HYB File Format",
      "Input layout",
      '<span class="report-status is-ready">Reference</span>',
      '<div class="report-subsection report-subsection--table"><p class="report-note">The local parser accepts the HYB record layout below. Collapsed source-read counts are retained as provenance and are not reused as Contact Map or Viewpoint weights.</p>' + table(["Column", "Field"], rows.map(function (row) { return [escape(row[0]), escape(row[1])]; })) + '</div>'
    );
  }

  function renderContactMap(context) {
    const state = context.state;
    const contact = state.contact || {};
    const matrix = contact.matrix;
    const params = parameterSnapshot("contactParameters", state, {
      rnaX: contact.rnaX || "",
      rnaY: contact.rnaY || "",
      binSize: number(contact.binSize, 10),
      measure: contact.measure || "records",
      orientation: contact.orientation || "normalised",
      chimeraType: contact.chimeraType || "all",
      homodimerSubset: contact.homodimer || "all"
    });
    const ready = !!(matrix && matrix.ready !== false && Array.isArray(matrix.cells) && matrix.cells.length);
    const status = matrix && matrix.limitExceeded
      ? '<span class="report-status is-warning">Limit reached</span>'
      : ready
      ? '<span class="report-status is-ready">Generated</span>'
      : '<span class="report-status is-pending">Configured</span>';
    const selectedCell = findMatrixCell(matrix, contact.selection);
    const visual = ready ? visualBlock(renderHeatmap(matrix, contact), matrix.cells.length > MAX_VISUAL_CELLS ? "Inline heatmap capped at " + formatNumber(MAX_VISUAL_CELLS) + " cells for printing." : "") : "";
    return section(
      "contact-map",
      "Contact Map",
      "RNA pair bin accumulation",
      status,
      [
        '<div class="report-grid report-analysis-grid">',
        '<div class="report-subsection"><h3>Parameters</h3>' + parameterList(params.parameters) + '</div>',
        '<div class="report-subsection"><h3>Result</h3>' + (ready ? definitionList([
          ["Cells", formatNumber(matrix.cells.length)],
          ["Records used", formatNumber(matrix.recordsUsed)],
          ["Total support", formatNumber(matrix.totalSupport)],
          ["Bin contributions", formatNumber(matrix.cellContributions)],
          ["Display cap", formatNumber(matrix.cap)],
          ["Selected cell", selectedCell ? range(selectedCell.x, selectedCell.x + number(matrix.binSize, 1) - 1) + " x " + range(selectedCell.y, selectedCell.y + number(matrix.binSize, 1) - 1) : "None"]
        ]) : '<div class="report-empty">' + escape(matrix && matrix.reason || "Generate a Contact Map from the workspace to include its result here.") + '</div>') + '</div>',
        "</div>",
        ready ? '<div class="report-visual-wrap"><div class="report-subsection-heading"><h3>Printable heatmap</h3><span class="report-note-inline">' + escape(contact.rnaX || "RNA X") + ' x ' + escape(contact.rnaY || "RNA Y") + '</span></div>' + visual + '</div>' : "",
        matrix && matrix.resourceBudget ? '<p class="report-note">Adaptive browser budget: up to ' + formatNumber(matrix.maximumCells) + ' cells and ' + formatNumber(matrix.maximumBinContributions) + ' bin contributions for this device profile.</p>' : ""
      ].join("")
    );
  }

  function renderViewpoint(context) {
    const state = context.state;
    const viewpoint = state.viewpoint || {};
    const results = viewpoint.results;
    const ready = !!(results && results.ready && Array.isArray(results.bins) && results.bins.length);
    const params = parameterSnapshot("viewpointParameters", state, {
      rna: viewpoint.rna || "",
      partner: viewpoint.partner || "All partners",
      rangeMode: viewpoint.rangeMode || "coordinates",
      start: viewpoint.start || "",
      end: viewpoint.end || "",
      binSize: viewpoint.binSize || "1",
      measure: viewpoint.measure || "records"
    });
    return section(
      "viewpoint",
      "Viewpoint",
      "Interaction-arm coverage",
      ready ? '<span class="report-status is-ready">Generated</span>' : '<span class="report-status is-pending">Configured</span>',
      [
        '<div class="report-grid report-analysis-grid">',
        '<div class="report-subsection"><h3>Parameters</h3>' + parameterList(params.parameters) + '</div>',
        '<div class="report-subsection"><h3>Result</h3>' + (ready ? definitionList([
          ["Effective range", range(results.start, results.end)],
          ["Resolution", formatNumber(results.binSize) + " nt"],
          ["Matching records", formatNumber((results.records || []).length)],
          ["Mapped arms", formatNumber(results.armContributions)],
          ["Maximum coverage", formatNumber(results.max)],
          ["Total coverage", formatNumber(results.totalCoverage)]
        ]) : '<div class="report-empty">Generate Viewpoint coverage from the workspace to include its result here.</div>') + '</div>',
        "</div>",
        ready ? '<div class="report-visual-wrap"><div class="report-subsection-heading"><h3>Printable coverage profile</h3><span class="report-note-inline">' + escape(results.rna || viewpoint.rna || "RNA") + '</span></div>' + visualBlock(renderCoverageSvg(results.bins, results.max, "#00a09d", viewpoint.selection && viewpoint.selection.start), results.bins.length > MAX_VISUAL_BINS ? "Coverage bars downsampled to " + formatNumber(MAX_VISUAL_BINS) + " buckets for printing." : "") + '</div>' : "",
        results && results.binSizeAdjusted ? '<p class="report-note">The requested resolution was adjusted to ' + formatNumber(results.binSize) + ' nt to keep the browser profile within its maximum bin count.</p>' : ""
      ].join("")
    );
  }

  function renderRegion(context) {
    const state = context.state;
    const region = state.region || {};
    const results = region.results;
    const ready = !!(results && (Array.isArray(results.records) || Array.isArray(results.profile)) && region.rna);
    const params = parameterSnapshot("regionParameters", state, {
      rna: region.rna || "",
      start: region.start || "",
      end: region.end || "",
      partner: region.partner || "All partners",
      partnerStart: region.partnerStart || "",
      partnerEnd: region.partnerEnd || "",
      overlap: region.overlap || "any"
    });
    const partnerRows = results && Array.isArray(results.partners) ? results.partners.slice(0, 10).map(function (item) {
      return [escape(item.name), formatNumber(item.records), formatNumber(item.support)];
    }) : [];
    const recordRows = results && Array.isArray(results.records) ? boundedItems(results.records, MAX_TABLE_ROWS, sortRecords).map(function (record) {
      return [escape(truncateText(record.id || "", 34)), escape(record.rnaOne || ""), escape(range(record.rnaOneStart, record.rnaOneEnd)), escape(record.rnaTwo || ""), escape(range(record.rnaTwoStart, record.rnaTwoEnd)), formatNumber(record.supportCount)];
    }) : [];
    return section(
      "region",
      "Region Explorer",
      "Inclusive coordinate overlap",
      ready ? '<span class="report-status is-ready">Configured</span>' : '<span class="report-status is-pending">Configured</span>',
      [
        '<div class="report-grid report-analysis-grid">',
        '<div class="report-subsection"><h3>Parameters</h3>' + parameterList(params.parameters) + '</div>',
        '<div class="report-subsection"><h3>Result</h3>' + (results ? definitionList([
          ["Matching records", formatNumber((results.records || []).length)],
          ["Cluster support", formatNumber(results.support)],
          ["Partner RNAs", formatNumber((results.partners || []).length)],
          ["Profile resolution", formatNumber(results.profileBinSize) + " nt"]
        ]) : '<div class="report-empty">Explore a region from the workspace to include its result here.</div>') + '</div>',
        "</div>",
        results && results.profile && results.profile.length ? '<div class="report-visual-wrap"><div class="report-subsection-heading"><h3>Printable region profile</h3><span class="report-note-inline">' + escape(region.rna || "RNA") + '</span></div>' + visualBlock(renderCoverageSvg(results.profile, maxSeriesValue(results.profile), "#6a4ff2", null), results.profile.length > MAX_VISUAL_BINS ? "Profile bars downsampled to " + formatNumber(MAX_VISUAL_BINS) + " buckets for printing." : "") + '</div>' : "",
        '<div class="report-grid report-analysis-grid">',
        '<div class="report-subsection"><h3>Top partners</h3>' + (partnerRows.length ? table(["Partner", "Records", "Support"], partnerRows) : '<div class="report-empty">No partner records are available for this region.</div>') + '</div>',
        '<div class="report-subsection"><h3>Matching record sample</h3>' + (recordRows.length ? table(["Sequence ID", "RNA 1", "Region 1", "RNA 2", "Region 2", "Support"], recordRows) : '<div class="report-empty">No matching records are available for this region.</div>') + '</div>',
        "</div>",
        results && results.records && results.records.length > MAX_TABLE_ROWS ? '<p class="report-note">Showing the first ' + formatNumber(MAX_TABLE_ROWS) + ' matching records. The Region Explorer export contains all local matches.</p>' : ""
      ].join("")
    );
  }

  function renderComparison(context) {
    const state = context.state;
    const comparison = state.comparison || {};
    const result = comparison.result;
    const ready = !!(result && result.ready && Array.isArray(result.cells) && result.cells.length);
    const datasets = Array.isArray(comparison.datasets) ? comparison.datasets : [];
    const params = parameterSnapshot("comparisonParameters", state, {
      conditionALabel: comparison.conditionALabel || "Condition A",
      conditionBLabel: comparison.conditionBLabel || "Condition B",
      rnaX: comparison.rnaX || "",
      rnaY: comparison.rnaY || "",
      binSize: comparison.binSize || "",
      measure: comparison.measure || "records",
      normalise: comparison.normalise || "library"
    });
    const datasetRows = datasets.slice(0, MAX_DATASET_ROWS).map(function (dataset) {
      const summary = dataset.summary || {};
      return [escape(truncateText(dataset.fileName || "Dataset", 36)), escape(dataset.label || ""), escape(dataset.condition === "A" ? (comparison.conditionALabel || "Condition A") : (comparison.conditionBLabel || "Condition B")), formatNumber(summary.validRecords), formatNumber(summary.supportInteractions)];
    });
    const selected = result && comparison.selection ? findComparisonCell(result, comparison.selection) : null;
    const selectedDetail = selected ? definitionList([
      [comparison.conditionALabel || "Condition A", formatNumber(selected.conditionA)],
      [comparison.conditionBLabel || "Condition B", formatNumber(selected.conditionB)],
      ["Log2 effect", formatNumber(selected.effect)],
      ["Datasets present", formatNumber(selected.presentDatasets)],
      ["Coordinates", range(selected.x, selected.x + number(result.binSize, 1) - 1) + " x " + range(selected.y, selected.y + number(result.binSize, 1) - 1)]
    ]) : '<div class="report-empty">No comparison bin is selected.</div>';
    return section(
      "comparison",
      "Compare",
      "Multi-dataset effect map",
      ready ? '<span class="report-status is-ready">Generated</span>' : '<span class="report-status is-pending">Configured</span>',
      [
        '<div class="report-subsection report-subsection--table"><div class="report-subsection-heading"><h3>Datasets and conditions</h3><span class="report-note-inline">' + formatNumber(datasets.length) + ' local files</span></div>',
        datasetRows.length ? table(["File", "Label", "Condition", "Valid records", "Support"], datasetRows) : '<div class="report-empty">Add a second local HYB dataset to configure a comparison.</div>',
        datasets.length > MAX_DATASET_ROWS ? '<p class="report-note">Only the first ' + formatNumber(MAX_DATASET_ROWS) + ' datasets are shown in the printable report.</p>' : "",
        "</div>",
        '<div class="report-grid report-analysis-grid">',
        '<div class="report-subsection"><h3>Parameters</h3>' + parameterList(params.parameters) + '</div>',
        '<div class="report-subsection"><h3>Result</h3>' + (ready ? definitionList([
          ["Condition A datasets", formatNumber((result.groupA || []).length)],
          ["Condition B datasets", formatNumber((result.groupB || []).length)],
          ["Comparable bins", formatNumber(result.cells.length)],
          ["Conserved bins", formatNumber((result.conservedCells || []).length)],
          ["Selected bin", selected ? "Included below" : "None"]
        ]) : '<div class="report-empty">' + escape(result && result.reason || "Update the comparison from the workspace to include its result here.") + '</div>') + '</div>',
        "</div>",
        ready ? '<div class="report-visual-pair"><div class="report-visual-wrap"><div class="report-subsection-heading"><h3>Mean log2 effect</h3><span class="report-note-inline">violet = A enriched; teal = B enriched</span></div>' + visualBlock(renderComparisonMapSvg(result, comparison, "effect"), result.cells.length > MAX_VISUAL_CELLS ? "Effect map capped at " + formatNumber(MAX_VISUAL_CELLS) + " cells for printing." : "") + '</div><div class="report-visual-wrap"><div class="report-subsection-heading"><h3>Conserved bins</h3><span class="report-note-inline">teal intensity = datasets present</span></div>' + visualBlock(renderComparisonMapSvg(result, comparison, "conserved"), (result.conservedCells || []).length > MAX_VISUAL_CELLS ? "Conserved map capped at " + formatNumber(MAX_VISUAL_CELLS) + " cells for printing." : "") + '</div></div>' : "",
        '<div class="report-subsection"><h3>Selected comparison bin</h3>' + selectedDetail + '</div>',
        '<p class="report-note">Comparison effects are descriptive library-size-normalised values. The report does not claim DESeq2 significance or adjusted p-values.</p>'
      ].join("")
    );
  }

  function renderStructure(context) {
    const state = context.state;
    const structure = state.structure || {};
    const result = structure.result;
    const sequenceInfo = !result && window.Hyb2Pages && typeof window.Hyb2Pages.getStructureSequence === "function"
      ? safeCall(function () { return window.Hyb2Pages.getStructureSequence(state); }, null)
      : null;
    const ready = !!(result && result.sequence);
    const cplfold = result && result.engine === "CPLfold";
    const candidateRows = cplfold && Array.isArray(result.candidates) ? result.candidates.slice(0, MAX_TABLE_ROWS).map(function (candidate, index) {
      return [formatNumber(index + 1), escape(candidate.type || ""), escape(candidate.topology || "nested"), candidate.energy == null ? "-" : formatNumber(candidate.energy), formatNumber((candidate.pairs || []).length), formatNumber(candidate.crossingPairs || 0), '<code>' + escape(truncateText(candidate.dotBracket || "", 90)) + '</code>'];
    }) : [];
    const structureReportData = ready && window.Hyb2StructureUI && typeof window.Hyb2StructureUI.structureReport === "function"
      ? safeCall(function () { return window.Hyb2StructureUI.structureReport(state, result); }, null)
      : null;
    const source = ready ? (structureReportData && structureReportData.source || {}) : {};
    const prediction = ready ? (structureReportData && structureReportData.prediction || {}) : {};
    return section(
      "structure",
      "RNA Structure",
      "Secondary-structure prediction",
      ready ? '<span class="report-status is-ready">Result available</span>' : '<span class="report-status is-pending">Not run</span>',
      [
        '<div class="report-grid report-analysis-grid">',
        '<div class="report-subsection"><h3>Setup</h3>' + definitionList([
          ["Engine", structure.engine === "cplfold" ? "CPLfold / Pyodide" : "ViennaRNA WebAssembly"],
          ["Source", structure.source || "Not selected"],
          ["RNA", structure.rna || "-", true],
          ["Range", structure.start && structure.end ? range(structure.start, structure.end) : "-"],
          ["Constraint mode", structure.constraintMode || "none"],
          ["Pseudoknot search", structure.engine === "cplfold" ? (structure.cplfoldAllowPseudoknot === false ? "Disabled" : "Allowed") : "Outside ViennaRNA mode"]
        ]) + '</div>',
        '<div class="report-subsection"><h3>Result</h3>' + (ready ? definitionList([
          ["Prepared sequence", formatNumber(result.sequence.length) + " nt"],
          ["Energy", result.energy == null ? "-" : formatNumber(result.energy) + " " + (result.energyUnit || "kcal/mol")],
          ["Topology", prediction.topology || result.topology || "nested"],
          ["Base pairs", formatNumber((result.pairs || []).length)],
          ["Crossing pairs", formatNumber(result.crossingPairs || 0)],
          ["Selected candidate", cplfold ? formatNumber(number(result.selectedCandidate, 0) + 1) : "MFE"]
        ]) : '<div class="report-empty">' + escape(sequenceInfo && sequenceInfo.error || "Run a structure prediction from the workspace to include its result here.") + '</div>') + '</div>',
        "</div>",
        ready ? '<div class="report-structure-result"><div class="report-subsection-heading"><h3>Printable secondary-structure view</h3><span class="report-note-inline">Arc layout used for the report snapshot</span></div><div class="report-structure-diagram" data-report-structure-diagram></div><p class="report-note">The report uses a stable Arc layout so it prints consistently. Use the RNA Structure page SVG/PNG controls to export folded, radial, circular, or matrix views.</p></div>' : "",
        ready ? '<div class="report-grid report-analysis-grid">' : "",
        ready ? '<div class="report-subsection"><h3>Sequence</h3><pre class="report-sequence"><code>' + escape(truncateText(result.sequence || "", 1400)) + '</code></pre><p class="report-note">The complete prepared sequence remains available through the Structure page FASTA export.</p></div>' : "",
        ready ? '<div class="report-subsection"><h3>Dot-bracket</h3><pre class="report-sequence"><code>' + escape(truncateText(result.dotBracket || "", 1400)) + '</code></pre><p class="report-note">Selected candidate: ' + escape(prediction.structureType || result.structureType || "MFE") + '; view the complete notation through the DBN export.</p></div>' : "",
        ready ? "</div>" : "",
        cplfold && candidateRows.length ? '<div class="report-subsection report-subsection--table"><div class="report-subsection-heading"><h3>CPLfold candidates</h3><span class="report-note-inline">Showing ' + formatNumber(Math.min(result.candidates.length, MAX_TABLE_ROWS)) + ' of ' + formatNumber(result.candidates.length) + '</span></div>' + table(["Rank", "Type", "Topology", "Energy", "Pairs", "Crossing", "Dot-bracket"], candidateRows) + '</div>' : "",
        ready && cplfold ? '<p class="report-note">CPLfold candidates preserve the selected pseudoknot setting and HYB bonus-matrix provenance. The complete candidate and bonus-matrix exports remain available from the RNA Structure page.</p>' : ""
      ].join("")
    );
  }

  function renderMethods() {
    return section(
      "methods",
      "Methods and privacy",
      "Interpretation notes",
      '<span class="report-status is-ready">Documented</span>',
      [
        '<div class="report-grid report-analysis-grid">',
        '<div class="report-subsection"><h3>Analysis semantics</h3><p>HYB records are validated locally. Contact Map accumulates selected records into inclusive coordinate bins. Viewpoint and Region Explorer use inclusive RNA coordinates. Compare reports descriptive effect sizes rather than statistical significance.</p></div>',
        '<div class="report-subsection"><h3>RNA structure</h3><p>ViennaRNA runs in a dedicated WebAssembly worker. CPLfold runs as pure Python in Pyodide and can search pseudoknot candidates. HYB-guided CPLfold exports retain the bonus matrix as a separate input.</p></div>',
        '<div class="report-subsection"><h3>Privacy</h3><p>This is a static browser-local application. Files, derived records, and analysis results stay in this tab\'s memory. Printing and JSON download happen locally through browser APIs.</p></div>',
        "</div>",
        '<div class="report-callout"><strong>Reproducibility boundary</strong><span>This consolidated document is a readable snapshot. Use the page-level parameter reports and raw exports when a complete machine-readable analysis package is required.</span></div>'
      ].join("")
    );
  }

  function getData(state) {
    const context = getReportContext(state || {});
    const reportState = context.state;
    const summary = reportState.summary || {};
    const contact = reportState.contact || {};
    const matrix = contact.matrix;
    const viewpoint = reportState.viewpoint || {};
    const viewpointResult = viewpoint.results;
    const region = reportState.region || {};
    const regionResult = region.results;
    const comparison = reportState.comparison || {};
    const comparisonResult = comparison.result;
    const structure = reportState.structure || {};
    const structureResult = structure.result;
    const structurePayload = structureResult && window.Hyb2StructureUI && typeof window.Hyb2StructureUI.structureReport === "function"
      ? safeCall(function () { return window.Hyb2StructureUI.structureReport(reportState, structureResult); }, { result: structureResult })
      : { setup: pickStructureSetup(structure), result: structureResult || null };

    return {
      application: "HYB2 Web Lite",
      version: REPORT_VERSION,
      buildCommit: window.HYB2_BUILD && window.HYB2_BUILD.commit || null,
      generatedLocally: true,
      generatedAt: context.generatedAt,
      reportScope: {
        kind: "consolidated-session-snapshot",
        boundedTables: true,
        rawExportsRemainAvailable: true
      },
      sourceHyb: sourceHybMetadata(reportState),
      sourceFasta: sourceFastaMetadata(reportState),
      summary: jsonSafe(summary, 3, 200),
      validation: {
        invalidRecords: number(summary.invalidRecords),
        errors: jsonSafe((summary.errors || []).slice(0, MAX_TABLE_ROWS), 3, 50),
        errorSampleTruncated: !!summary.errorSampleTruncated,
        omittedErrorCount: number(summary.omittedErrorCount)
      },
      analyses: {
        interactions: {
          parameters: jsonSafe(reportState.filters || {}, 3, 50),
          result: {
            matchingRecords: context.filteredRecords.length,
            topPartners: jsonSafe(context.partnerCounts.slice(0, 10), 3, 50),
            sampleRecords: jsonSafe(boundedItems(context.filteredRecords, MAX_TABLE_ROWS, sortRecords), 3, 50)
          }
        },
        contactMap: {
          parameters: jsonSafe(parameterSnapshot("contactParameters", reportState, contact), 4, 100),
          result: matrix ? {
            ready: matrix.ready !== false,
            reason: matrix.reason || null,
            cells: Array.isArray(matrix.cells) ? matrix.cells.length : 0,
            recordsUsed: number(matrix.recordsUsed),
            totalSupport: number(matrix.totalSupport),
            binSize: number(matrix.binSize, 10),
            maximumCells: number(matrix.maximumCells),
            maximumBinContributions: number(matrix.maximumBinContributions),
            selectedCell: jsonSafe(findMatrixCell(matrix, contact.selection), 3, 20),
            visualCells: jsonSafe(boundedItems(matrix.cells || [], MAX_VISUAL_CELLS, sortCells), 3, 20)
          } : { ready: false, reason: "Not generated", cells: 0 }
        },
        viewpoint: {
          parameters: jsonSafe(parameterSnapshot("viewpointParameters", reportState, viewpoint), 4, 100),
          result: viewpointResult ? {
            ready: viewpointResult.ready !== false,
            start: viewpointResult.start,
            end: viewpointResult.end,
            binSize: viewpointResult.binSize,
            matchingRecords: Array.isArray(viewpointResult.records) ? viewpointResult.records.length : 0,
            armContributions: number(viewpointResult.armContributions),
            totalCoverage: number(viewpointResult.totalCoverage),
            bins: jsonSafe((viewpointResult.bins || []).slice(0, MAX_VISUAL_BINS), 3, 20)
          } : { ready: false, reason: "Not generated", bins: [] }
        },
        region: {
          parameters: jsonSafe(parameterSnapshot("regionParameters", reportState, region), 4, 100),
          result: regionResult ? {
            matchingRecords: Array.isArray(regionResult.records) ? regionResult.records.length : 0,
            support: number(regionResult.support),
            partners: jsonSafe((regionResult.partners || []).slice(0, 10), 3, 50),
            profile: jsonSafe((regionResult.profile || []).slice(0, MAX_VISUAL_BINS), 3, 20),
            sampleRecords: jsonSafe(boundedItems(regionResult.records || [], MAX_TABLE_ROWS, sortRecords), 3, 50)
          } : { ready: false, reason: "Not generated" }
        },
        comparison: {
          parameters: jsonSafe(parameterSnapshot("comparisonParameters", reportState, comparison), 4, 150),
          datasets: jsonSafe(comparisonDatasets(comparison), 3, 50),
          result: comparisonResult ? {
            ready: comparisonResult.ready === true,
            reason: comparisonResult.reason || null,
            comparableBins: Array.isArray(comparisonResult.cells) ? comparisonResult.cells.length : 0,
            conservedBins: Array.isArray(comparisonResult.conservedCells) ? comparisonResult.conservedCells.length : 0,
            selectedCell: jsonSafe(findComparisonCell(comparisonResult, comparison.selection), 3, 50),
            effectCells: jsonSafe(boundedItems(comparisonResult.cells || [], MAX_VISUAL_CELLS, sortEffectCells), 3, 50)
          } : { ready: false, reason: "Not generated" }
        },
        structure: jsonSafe(structurePayload, 5, 1000)
      },
      methods: {
        browserLocal: true,
        reportUsesBoundedVisuals: true,
        completeExportsRemainOnPages: true
      }
    };
  }

  function section(id, title, kicker, status, body) {
    return [
      '<section class="report-section report-section--' + id + '" id="report-' + id + '">',
      '  <div class="report-section-heading"><div><span class="report-kicker">' + escape(kicker) + '</span><h2>' + escape(title) + '</h2></div>' + status + '</div>',
      '  <div class="report-section-body">', body, "</div>",
      "</section>"
    ].join("");
  }

  function metric(label, value, detail) {
    return '<article class="report-metric"><span class="report-metric-label">' + escape(label) + '</span><strong class="report-metric-value">' + escape(value) + '</strong><span class="report-metric-detail">' + escape(detail) + '</span></article>';
  }

  function definitionList(entries) {
    return '<dl class="report-definition-list">' + entries.filter(Boolean).map(function (entry) {
      const value = entry[1] == null || entry[1] === "" ? "-" : entry[1];
      return '<div><dt>' + escape(entry[0]) + '</dt><dd' + (entry[2] ? ' class="is-mono"' : "") + '>' + escape(value) + '</dd></div>';
    }).join("") + "</dl>";
  }

  function parameterList(parameters) {
    if (!parameters || typeof parameters !== "object") {
      return '<div class="report-empty">No parameter snapshot is available.</div>';
    }
    const entries = Object.keys(parameters).filter(function (key) {
      return parameters[key] !== undefined && parameters[key] !== null && parameters[key] !== "";
    }).map(function (key) {
      return [humanize(key), formatParameter(parameters[key]), isMonoParameter(key)];
    });
    return entries.length ? definitionList(entries) : '<div class="report-empty">No parameter values were set.</div>';
  }

  function table(headers, rows) {
    return '<table class="report-table"><thead><tr>' + headers.map(function (header) { return '<th scope="col">' + escape(header) + '</th>'; }).join("") + '</tr></thead><tbody>' + rows.map(function (row) {
      return '<tr>' + row.map(function (cell) { return '<td>' + cell + '</td>'; }).join("") + '</tr>';
    }).join("") + '</tbody></table>';
  }

  function visualBlock(svg, note) {
    return '<div class="report-visual">' + svg + (note ? '<p class="report-note-inline">' + escape(note) + '</p>' : "") + '</div>';
  }

  function renderHeatmap(matrix, contact) {
    const cells = Array.isArray(matrix.cells) ? matrix.cells : [];
    if (!cells.length) {
      return '<div class="report-empty">No contact-map cells were generated.</div>';
    }
    const shown = boundedItems(cells, MAX_VISUAL_CELLS, function (left, right) {
      return visualCellValue(right, contact) - visualCellValue(left, contact) || sortCells(left, right);
    });
    const binSize = Math.max(1, number(matrix.binSize, 1));
    const xStart = extrema(cells, "x", "min");
    const yStart = extrema(cells, "y", "min");
    const xEnd = extrema(cells, "x", "max") + binSize;
    const yEnd = extrema(cells, "y", "max") + binSize;
    const plotLeft = 58;
    const plotTop = 28;
    const plotWidth = 650;
    const plotHeight = 300;
    const cap = Math.max(0, number(matrix.cap, number(matrix.max, 0)));
    const selected = contact && contact.selection;
    const rects = shown.map(function (cell) {
      const x = plotLeft + ((number(cell.x) - xStart) / Math.max(1, xEnd - xStart)) * plotWidth;
      const y = plotTop + ((number(cell.y) - yStart) / Math.max(1, yEnd - yStart)) * plotHeight;
      const width = Math.max(1, binSize / Math.max(1, xEnd - xStart) * plotWidth + 0.5);
      const height = Math.max(1, binSize / Math.max(1, yEnd - yStart) * plotHeight + 0.5);
      const ratio = cap ? Math.max(0, Math.min(1, visualCellValue(cell, contact) / cap)) : 0;
      const isSelected = selected && number(selected.x) === number(cell.x) && number(selected.y) === number(cell.y);
      return '<rect x="' + fixed(x) + '" y="' + fixed(y) + '" width="' + fixed(width) + '" height="' + fixed(height) + '" fill="' + brandHeatColor(ratio) + '"' + (isSelected ? ' stroke="#132c47" stroke-width="2"' : "") + '><title>' + escape(range(cell.x, number(cell.x) + binSize - 1) + " x " + range(cell.y, number(cell.y) + binSize - 1) + "; value " + formatNumber(cell.value)) + '</title></rect>';
    }).join("");
    return [
      '<svg class="report-svg report-heatmap-svg" viewBox="0 0 760 360" role="img" aria-label="Printable contact map">',
      '<title>Contact map for ' + escape(contact && contact.rnaX || "RNA X") + ' and ' + escape(contact && contact.rnaY || "RNA Y") + '</title>',
      '<rect x="0" y="0" width="760" height="360" fill="transparent"></rect>',
      '<rect x="' + plotLeft + '" y="' + plotTop + '" width="' + plotWidth + '" height="' + plotHeight + '" fill="#f7fafb" stroke="#c2d3db"></rect>',
      rects,
      '<text x="' + plotLeft + '" y="' + (plotTop + plotHeight + 24) + '" font-family="monospace" font-size="11" fill="#607286">' + escape(String(xStart)) + '</text>',
      '<text x="' + (plotLeft + plotWidth) + '" y="' + (plotTop + plotHeight + 24) + '" text-anchor="end" font-family="monospace" font-size="11" fill="#607286">' + escape(String(xEnd - 1)) + '</text>',
      '<text x="' + (plotLeft - 8) + '" y="' + (plotTop + 4) + '" text-anchor="end" font-family="monospace" font-size="11" fill="#607286">' + escape(String(yStart)) + '</text>',
      '<text x="' + (plotLeft - 8) + '" y="' + (plotTop + plotHeight) + '" text-anchor="end" font-family="monospace" font-size="11" fill="#607286">' + escape(String(yEnd - 1)) + '</text>',
      '<text x="' + (plotLeft + plotWidth / 2) + '" y="' + (plotTop + plotHeight + 44) + '" text-anchor="middle" font-family="system-ui, sans-serif" font-size="11" fill="#607286">' + escape(contact && contact.rnaX || "RNA X") + ' coordinate</text>',
      '<text x="16" y="' + (plotTop + plotHeight / 2) + '" transform="rotate(-90 16 ' + (plotTop + plotHeight / 2) + ')" text-anchor="middle" font-family="system-ui, sans-serif" font-size="11" fill="#607286">' + escape(contact && contact.rnaY || "RNA Y") + ' coordinate</text>',
      '</svg>'
    ].join("");
  }

  function renderCoverageSvg(items, maximum, color, selectedStart) {
    const bins = downsample(items || [], MAX_VISUAL_BINS);
    if (!bins.length) {
      return '<div class="report-empty">No coverage bins were generated.</div>';
    }
    const plotLeft = 46;
    const plotTop = 22;
    const plotWidth = 680;
    const plotHeight = 155;
    const maxValue = Math.max(0, number(maximum, maxSeriesValue(bins)));
    const barWidth = plotWidth / bins.length;
    const bars = bins.map(function (bin, index) {
      const value = Math.max(0, number(bin.value));
      const height = maxValue ? value / maxValue * plotHeight : 0;
      const selected = selectedStart != null && number(selectedStart) >= number(bin.start) && number(selectedStart) <= number(bin.end);
      return '<rect x="' + fixed(plotLeft + index * barWidth) + '" y="' + fixed(plotTop + plotHeight - height) + '" width="' + fixed(Math.max(1, barWidth - 0.5)) + '" height="' + fixed(height) + '" fill="' + (selected ? "#6a4ff2" : color) + '"><title>' + escape(range(bin.start, bin.end) + "; value " + formatNumber(value)) + '</title></rect>';
    }).join("");
    return [
      '<svg class="report-svg report-coverage-svg" viewBox="0 0 760 220" role="img" aria-label="Printable coverage profile">',
      '<title>RNA interaction coverage profile</title>',
      '<line x1="' + plotLeft + '" y1="' + (plotTop + plotHeight) + '" x2="' + (plotLeft + plotWidth) + '" y2="' + (plotTop + plotHeight) + '" stroke="#c2d3db"></line>',
      bars,
      '<text x="' + plotLeft + '" y="' + (plotTop + plotHeight + 22) + '" font-family="monospace" font-size="11" fill="#607286">' + escape(String(bins[0].start)) + '</text>',
      '<text x="' + (plotLeft + plotWidth) + '" y="' + (plotTop + plotHeight + 22) + '" text-anchor="end" font-family="monospace" font-size="11" fill="#607286">' + escape(String(bins[bins.length - 1].end)) + '</text>',
      '<text x="' + (plotLeft - 8) + '" y="' + (plotTop + 4) + '" text-anchor="end" font-family="monospace" font-size="11" fill="#607286">' + escape(formatNumber(maxValue)) + '</text>',
      '</svg>'
    ].join("");
  }

  function renderComparisonMapSvg(result, comparison, mode) {
    const source = mode === "conserved" ? (result.conservedCells || []) : (result.cells || []);
    if (!source.length) {
      return '<div class="report-empty">No bins are available for this comparison view.</div>';
    }
    const shown = boundedItems(source, MAX_VISUAL_CELLS, mode === "conserved" ? sortConservedCells : sortEffectCells);
    const binSize = Math.max(1, number(result.binSize, 1));
    const xStart = extrema(source, "x", "min");
    const yStart = extrema(source, "y", "min");
    const xEnd = extrema(source, "x", "max") + binSize;
    const yEnd = extrema(source, "y", "max") + binSize;
    const plotLeft = 52;
    const plotTop = 24;
    const plotWidth = 650;
    const plotHeight = 300;
    const cap = Math.max(0.25, number(result.effectCap, 1));
    const conservedMax = Math.max(1, number(result.conservedMax, 1));
    const selected = comparison.selection;
    const rects = shown.map(function (cell) {
      const x = plotLeft + ((number(cell.x) - xStart) / Math.max(1, xEnd - xStart)) * plotWidth;
      const y = plotTop + ((number(cell.y) - yStart) / Math.max(1, yEnd - yStart)) * plotHeight;
      const width = Math.max(1, binSize / Math.max(1, xEnd - xStart) * plotWidth + 0.5);
      const height = Math.max(1, binSize / Math.max(1, yEnd - yStart) * plotHeight + 0.5);
      const ratio = mode === "conserved"
        ? Math.max(0, Math.min(1, number(cell.presentDatasets) / conservedMax))
        : number(cell.effect) / cap;
      const fill = mode === "conserved" ? conservedColor(ratio) : effectColor(ratio);
      const isSelected = selected && number(selected.x) === number(cell.x) && number(selected.y) === number(cell.y);
      return '<rect x="' + fixed(x) + '" y="' + fixed(y) + '" width="' + fixed(width) + '" height="' + fixed(height) + '" fill="' + fill + '"' + (isSelected ? ' stroke="#132c47" stroke-width="2"' : "") + '><title>' + escape(range(cell.x, number(cell.x) + binSize - 1) + " x " + range(cell.y, number(cell.y) + binSize - 1) + (mode === "conserved" ? "; datasets " + formatNumber(cell.presentDatasets) : "; effect " + formatNumber(cell.effect))) + '</title></rect>';
    }).join("");
    return [
      '<svg class="report-svg report-heatmap-svg" viewBox="0 0 760 360" role="img" aria-label="Printable comparison map">',
      '<title>' + escape(mode === "conserved" ? "Conserved comparison bins" : "Mean log2 comparison effect") + '</title>',
      '<rect x="' + plotLeft + '" y="' + plotTop + '" width="' + plotWidth + '" height="' + plotHeight + '" fill="#f7fafb" stroke="#c2d3db"></rect>',
      rects,
      '<text x="' + plotLeft + '" y="' + (plotTop + plotHeight + 24) + '" font-family="monospace" font-size="11" fill="#607286">' + escape(String(xStart)) + '</text>',
      '<text x="' + (plotLeft + plotWidth) + '" y="' + (plotTop + plotHeight + 24) + '" text-anchor="end" font-family="monospace" font-size="11" fill="#607286">' + escape(String(xEnd - 1)) + '</text>',
      '<text x="' + (plotLeft - 8) + '" y="' + (plotTop + 4) + '" text-anchor="end" font-family="monospace" font-size="11" fill="#607286">' + escape(String(yStart)) + '</text>',
      '<text x="' + (plotLeft - 8) + '" y="' + (plotTop + plotHeight) + '" text-anchor="end" font-family="monospace" font-size="11" fill="#607286">' + escape(String(yEnd - 1)) + '</text>',
      '</svg>'
    ].join("");
  }

  function parameterSnapshot(name, state, fallback) {
    if (window.Hyb2UI && typeof window.Hyb2UI[name] === "function") {
      const result = safeCall(function () { return window.Hyb2UI[name](state); }, null);
      if (result) {
        return result;
      }
    }
    return { parameters: fallback || {} };
  }

  function sourceHybMetadata(state) {
    const summary = state.summary || {};
    return {
      fileName: summary.fileName || "",
      fileSize: summary.fileSize == null ? null : summary.fileSize,
      sha256: summary.sha256 || null,
      sha256Status: summary.sha256 ? "available" : (summary.sha256Unavailable ? "unavailable" : "pending")
    };
  }

  function sourceFastaMetadata(state) {
    const fasta = state.fasta;
    if (!fasta) {
      return null;
    }
    return {
      fileName: fasta.fileName || "",
      fileSize: fasta.fileSize == null ? null : fasta.fileSize,
      sha256: fasta.sha256 || null,
      sha256Status: fasta.sha256 ? "available" : (fasta.sha256Unavailable ? "unavailable" : "pending"),
      sequenceCount: Array.isArray(fasta.sequences) ? fasta.sequences.length : 0,
      mappedRnas: mappedFastaCount(fasta)
    };
  }

  function comparisonDatasets(comparison) {
    return (comparison && comparison.datasets || []).map(function (dataset) {
      const summary = dataset.summary || {};
      return {
        id: dataset.id || null,
        label: dataset.label || "",
        condition: dataset.condition || null,
        fileName: dataset.fileName || summary.fileName || "",
        fileSize: dataset.fileSize == null ? (summary.fileSize == null ? null : summary.fileSize) : dataset.fileSize,
        sha256: dataset.sha256 || summary.sha256 || null,
        validRecords: number(summary.validRecords),
        supportInteractions: number(summary.supportInteractions)
      };
    });
  }

  function pickStructureSetup(structure) {
    return {
      engine: structure.engine || "viennarna",
      source: structure.source || "",
      rna: structure.rna || "",
      start: structure.start || null,
      end: structure.end || null,
      constraintMode: structure.constraintMode || "none",
      cplfoldEvidence: structure.cplfoldEvidence || null,
      cplfoldAllowPseudoknot: structure.cplfoldAllowPseudoknot !== false
    };
  }

  function findMatrixCell(matrix, selection) {
    if (!matrix || !selection) {
      return null;
    }
    if (matrix.cellMap && matrix.cellMap[selection.x + ":" + selection.y]) {
      return matrix.cellMap[selection.x + ":" + selection.y];
    }
    return (matrix.cells || []).find(function (cell) { return number(cell.x) === number(selection.x) && number(cell.y) === number(selection.y); }) || null;
  }

  function findComparisonCell(result, selection) {
    if (!result || !selection) {
      return null;
    }
    if (result.cellMap && result.cellMap[selection.x + ":" + selection.y]) {
      return result.cellMap[selection.x + ":" + selection.y];
    }
    return (result.cells || []).find(function (cell) { return number(cell.x) === number(selection.x) && number(cell.y) === number(selection.y); }) || null;
  }

  function downsample(items, maximum) {
    const source = Array.isArray(items) ? items : [];
    if (source.length <= maximum) {
      return source;
    }
    const step = Math.ceil(source.length / maximum);
    const output = [];
    for (let index = 0; index < source.length; index += step) {
      const group = source.slice(index, index + step);
      output.push({
        start: group[0].start,
        end: group[group.length - 1].end,
        value: group.reduce(function (total, item) { return total + number(item.value); }, 0) / group.length
      });
    }
    return output;
  }

  function boundedItems(items, maximum, sorter) {
    const source = Array.isArray(items) ? items : [];
    if (source.length <= maximum * 8) {
      return source.slice().sort(sorter).slice(0, maximum);
    }
    const step = Math.max(1, Math.ceil(source.length / (maximum * 4)));
    const sampled = [];
    for (let index = 0; index < source.length && sampled.length < maximum * 4; index += step) {
      sampled.push(source[index]);
    }
    return sampled.sort(sorter).slice(0, maximum);
  }

  function extrema(items, key, direction) {
    const values = Array.isArray(items) ? items : [];
    if (!values.length) {
      return 0;
    }
    return values.reduce(function (current, item) {
      const value = number(item && item[key]);
      return direction === "max" ? Math.max(current, value) : Math.min(current, value);
    }, direction === "max" ? -Infinity : Infinity);
  }

  function maxSeriesValue(items) {
    return (items || []).reduce(function (maximum, item) { return Math.max(maximum, number(item.value)); }, 0);
  }

  function visualCellValue(cell, contact) {
    const value = number(cell && cell.value);
    return contact && contact.scale === "log" ? Math.log1p(Math.max(0, value)) : value;
  }

  function sortRecords(left, right) {
    return number(right.supportCount) - number(left.supportCount) || number(right.rawReadCount) - number(left.rawReadCount) || String(left.id || "").localeCompare(String(right.id || ""));
  }

  function sortCells(left, right) {
    return number(right.value) - number(left.value) || number(left.x) - number(right.x) || number(left.y) - number(right.y);
  }

  function sortEffectCells(left, right) {
    return Math.abs(number(right.effect)) - Math.abs(number(left.effect)) || number(left.x) - number(right.x) || number(left.y) - number(right.y);
  }

  function sortConservedCells(left, right) {
    return number(right.presentDatasets) - number(left.presentDatasets) || number(left.x) - number(right.x) || number(left.y) - number(right.y);
  }

  function brandHeatColor(ratio) {
    const value = Math.max(0, Math.min(1, number(ratio)));
    if (value < 0.5) {
      return interpolateColor("#132c47", "#00a09d", value * 2);
    }
    return interpolateColor("#00a09d", "#6a4ff2", (value - 0.5) * 2);
  }

  function conservedColor(ratio) {
    return interpolateColor("#dce7ec", "#00a09d", Math.max(0, Math.min(1, number(ratio))));
  }

  function effectColor(value) {
    const bounded = Math.max(-1, Math.min(1, number(value)));
    return bounded >= 0
      ? interpolateColor("#f7fafb", "#6a4ff2", bounded)
      : interpolateColor("#f7fafb", "#00a09d", Math.abs(bounded));
  }

  function interpolateColor(from, to, ratio) {
    const first = hexRgb(from);
    const second = hexRgb(to);
    const t = Math.max(0, Math.min(1, number(ratio)));
    return "rgb(" + Math.round(first[0] + (second[0] - first[0]) * t) + ", " + Math.round(first[1] + (second[1] - first[1]) * t) + ", " + Math.round(first[2] + (second[2] - first[2]) * t) + ")";
  }

  function hexRgb(value) {
    const hex = String(value || "#000000").replace("#", "");
    return [parseInt(hex.slice(0, 2), 16), parseInt(hex.slice(2, 4), 16), parseInt(hex.slice(4, 6), 16)];
  }

  function mappedFastaCount(fasta) {
    return fasta && fasta.mapping ? Object.keys(fasta.mapping).filter(function (key) { return fasta.mapping[key]; }).length : 0;
  }

  function humanize(value) {
    return String(value || "").replace(/([a-z])([A-Z])/g, "$1 $2").replace(/_/g, " ").replace(/\b\w/g, function (letter) { return letter.toUpperCase(); });
  }

  function isMonoParameter(key) {
    return /rna|start|end|bin|cap|coordinate|range|hash|reference|mode/i.test(String(key || ""));
  }

  function formatParameter(value) {
    if (value === true) return "Yes";
    if (value === false) return "No";
    if (value && typeof value === "object") {
      return JSON.stringify(value);
    }
    return String(value == null || value === "" ? "-" : value);
  }

  function formatNumber(value, fallback) {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? numberFormatter.format(parsed) : (fallback == null ? "-" : String(fallback));
  }

  function formatPercent(value) {
    const parsed = Number(value);
    if (!Number.isFinite(parsed)) return "-";
    return new Intl.NumberFormat("en-US", { style: "percent", maximumFractionDigits: 1 }).format(parsed);
  }

  function formatBytes(value) {
    const bytes = Number(value);
    if (!Number.isFinite(bytes) || bytes <= 0) return "-";
    const units = ["B", "KB", "MB", "GB"];
    const index = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
    return numberFormatter.format(bytes / Math.pow(1024, index)) + " " + units[index];
  }

  function formatTimestamp(value) {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? "Local time" : date.toISOString().replace("T", " ").replace(".000Z", " UTC");
  }

  function range(start, end) {
    if (start == null || end == null || start === "" || end === "") return "-";
    return String(start) + "-" + String(end);
  }

  function number(value, fallback) {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : (fallback == null ? 0 : fallback);
  }

  function fixed(value) {
    return number(value).toFixed(2);
  }

  function truncateText(value, maximum) {
    const text = String(value == null ? "" : value);
    if (text.length <= maximum) return text;
    const head = Math.ceil(maximum * 0.62);
    const tail = Math.max(1, maximum - head - 18);
    return text.slice(0, head) + " ... [truncated] ... " + text.slice(-tail);
  }

  function safeCall(callback, fallback) {
    try {
      return callback();
    } catch (error) {
      return fallback;
    }
  }

  function jsonSafe(value, depth, breadth) {
    if (value === undefined) return null;
    if (value === null || typeof value === "string" || typeof value === "boolean") return value;
    if (typeof value === "number") return Number.isFinite(value) ? value : null;
    if (depth <= 0) return "[truncated]";
    if (Array.isArray(value)) {
      return value.slice(0, breadth).map(function (item) { return jsonSafe(item, depth - 1, breadth); });
    }
    if (typeof value === "object") {
      const output = {};
      Object.keys(value).slice(0, breadth).forEach(function (key) {
        output[key] = jsonSafe(value[key], depth - 1, breadth);
      });
      return output;
    }
    return String(value);
  }

  function escape(value) {
    return String(value == null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#039;");
  }

  window.Hyb2Report = {
    render: render,
    afterRender: afterRender,
    getData: getData
  };
}());
