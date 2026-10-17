(function () {
  "use strict";

  const formatNumber = new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 });

  function renderInteractions(state) {
    const filters = state.filters || window.Hyb2Data.defaultInteractionFilters();
    const rnas = state.summary.rnaNames || [];
    const chimeraTypes = state.summary.chimeraTypes || [];
    // Rows and partner counts come from DataProvider.queryHybrids /
    // getTopInteractions via page-data.js; null while loading.
    const interactions = state.view && state.view.interactions || null;
    const partners = filters.rna && interactions ? interactions.partners || [] : [];
    const selectedRecord = findRecord(state, state.selectedRecordIndex);

    return [
      '<div class="page-heading">',
      "<div><h1>Interactions</h1><p>Filter, inspect, and export local HYB records.</p></div>",
      '<div class="inline-actions"><button class="button button-secondary" type="button" data-feature-action="export-filtered-csv">Export CSV</button><button class="button button-secondary" type="button" data-feature-action="export-filtered-hyb">Export HYB</button><button class="button button-secondary" type="button" data-feature-action="interactions-export-params">Export parameters</button></div>',
      "</div>",
      '<section class="analysis-controls data-card">',
      '<div class="control-grid control-grid-wide">',
      renderSelectControl("RNA", "rna", filters.rna, [{ value: "", label: "All RNAs" }].concat(rnas.map(function (rna) { return { value: rna, label: rna }; })), "interaction-filter"),
      renderSelectControl("Partner", "partner", filters.partner, partnerOptions(rnas, filters.rna), "interaction-filter"),
      renderSelectControl("Interaction type", "type", filters.type, [
        { value: "all", label: "All" },
        { value: "intra", label: "Intra-RNA" },
        { value: "inter", label: "Inter-RNA" },
        { value: "homodimer", label: "Homodimer proxy (overlap ≥ 5)" }
      ], "interaction-filter"),
      renderSelectControl("Chimera type", "chimeraType", filters.chimeraType, [{ value: "all", label: "All chimera types" }].concat(chimeraTypes.map(function (item) { return { value: item.name, label: item.name }; })), "interaction-filter"),
      renderSelectControl("Count mode", "countMode", filters.countMode, [
        { value: "support", label: "Cluster support" },
        { value: "records", label: "Records" }
      ], "interaction-filter"),
      renderTextControl("Search", "search", filters.search, "RNA, sequence ID, or sequence", "interaction-filter"),
      "</div>",
      '<details class="advanced-filters" data-open-key="interactions-advanced"' + openAttr("interactions-advanced") + '><summary>Advanced filters</summary>',
      '<div class="control-grid advanced-grid">',
      renderTextControl("Arm 1 start", "armOneStart", filters.armOneStart, "Any", "interaction-filter", "number", { min: 1, step: 1 }),
      renderTextControl("Arm 1 end", "armOneEnd", filters.armOneEnd, "Any", "interaction-filter", "number", { min: 1, step: 1 }),
      renderTextControl("Arm 2 start", "armTwoStart", filters.armTwoStart, "Any", "interaction-filter", "number", { min: 1, step: 1 }),
      renderTextControl("Arm 2 end", "armTwoEnd", filters.armTwoEnd, "Any", "interaction-filter", "number", { min: 1, step: 1 }),
      renderTextControl("Maximum e-value", "maxEvalue", filters.maxEvalue, "Any", "interaction-filter", "number"),
      renderTextControl("Minimum dG", "minDg", filters.minDg, "Any", "interaction-filter", "number"),
      renderTextControl("Maximum dG", "maxDg", filters.maxDg, "Any", "interaction-filter", "number"),
      renderTextControl("Minimum support", "minSupport", filters.minSupport, "1", "interaction-filter", "number"),
      "</div></details>",
      "</section>",
      filters.rna ? renderPartnerPanel(partners, filters, !interactions) : renderChooseRnaPanel(),
      renderInteractionTable(interactions, state),
      selectedRecord ? renderRecordDrawer(selectedRecord, state) : ""
    ].join("");
  }

  function renderPartnerPanel(partners, filters, loading) {
    const maximum = partners.length ? (filters.countMode === "records" ? partners[0].records : partners[0].support) : 1;
    const measure = filters.countMode === "records" ? "Records" : "Cluster support";

    return [
      '<section class="data-card partner-card">',
      '<div class="card-title-row"><div><h2>' + escape(filters.rna) + ' interaction partners</h2><span class="card-note">Click a partner to refine the table.</span></div><span class="card-kicker">' + measure + "</span></div>",
      partners.length ? '<div class="bar-list">' + partners.slice(0, 10).map(function (partner) {
        const value = filters.countMode === "records" ? partner.records : partner.support;
        return [
          '<button class="bar-row bar-row-button" type="button" data-feature-action="select-partner" data-partner="' + attribute(partner.name) + '">',
          '<span class="bar-name" title="' + attribute(partner.name) + '">' + escape(partner.name) + "</span>",
          '<span class="bar-track"><span class="bar-fill" style="width:' + Math.max(3, (value / maximum) * 100) + '%"></span></span>',
          '<span class="bar-value">' + format(value) + "</span>",
          "</button>"
        ].join("");
      }).join("") + "</div>" : (loading ? '<p class="empty-inline">Loading partners…</p>' : '<p class="empty-inline">No records match the current filters.</p>'),
      "</section>"
    ].join("");
  }

  function renderChooseRnaPanel() {
    return [
      '<section class="data-card partner-card">',
      '<div class="card-title-row"><h2>RNA partner analysis</h2><span class="card-kicker">Select an RNA</span></div>',
      '<p class="empty-inline">Choose one RNA in the filter bar to compare its interaction partners using real local records.</p>',
      "</section>"
    ].join("");
  }

  function renderInteractionTable(interactions, state) {
    const rowHeight = state.density === "compact" ? 38 : 46;
    // Initial row window only; the table height comes from CSS and
    // bindVirtualTable re-windows rows from the real clientHeight.
    const viewportHeight = 960;
    const scrollTop = state.interactionScrollTop || 0;
    const start = Math.max(0, Math.floor(scrollTop / rowHeight) - 4);
    const visible = Math.ceil(viewportHeight / rowHeight) + 8;
    const total = interactions ? interactions.total : 0;
    const rows = interactionWindow(interactions, start, visible);
    const note = !interactions
      ? "Loading matching records…"
      : interactions.error
      ? interactions.error
      : format(total) + " matching records · virtualised rows";

    return [
      '<section class="data-card interaction-table-card">',
      '<div class="card-title-row"><div><h2>Records</h2><span class="card-note">' + escape(note) + "</span></div><span class=\"card-kicker\">" + (state.shell === false ? "Project data" : "Local memory") + "</span></div>",
      '<div class="virtual-table-frame">',
      '<div class="virtual-table-head" role="row"><span>Sequence ID</span><span>RNA 1</span><span>Region 1 <span class="unit">(nt)</span></span><span>RNA 2</span><span>Region 2 <span class="unit">(nt)</span></span><span>Chimera type</span><span title="Cluster support: records merged into this one">Support</span><span>ΔG <span class="unit">(kcal/mol)</span></span></div>',
      '<div class="virtual-table" data-virtual-table role="region" aria-label="Interaction records" tabindex="0" style="height:' + viewportHeight + 'px">',
      '<div class="virtual-table-spacer" style="height:' + (total * rowHeight) + 'px"></div>',
      '<div class="virtual-table-rows" data-virtual-rows style="transform:translateY(' + (start * rowHeight) + 'px)">',
      renderVirtualRows(rows || []),
      "</div></div>",
      "</div>",
      "</section>"
    ].join("");
  }

  // Rows [start, start + count) from the loaded queryHybrids window, or null
  // when that range has not been fetched yet.
  function interactionWindow(interactions, start, count) {
    if (!interactions || !Array.isArray(interactions.rows)) {
      return [];
    }
    const end = Math.min(interactions.total, start + count);
    const offset = interactions.offset || 0;
    if (start < offset || end > offset + interactions.rows.length) {
      return null;
    }
    return interactions.rows.slice(start - offset, Math.max(start - offset, end - offset));
  }

  function renderVirtualRows(records) {
    return records.map(function (record) {
      return [
        '<button class="virtual-row" type="button" data-feature-action="open-record" data-record-index="' + record.index + '" role="row">',
        '<span title="' + attribute(record.id) + '">' + escape(record.id) + "</span>",
        '<span title="' + attribute(record.rnaOne) + '">' + escape(record.rnaOne) + "</span>",
        "<span>" + range(record.rnaOneStart, record.rnaOneEnd) + "</span>",
        '<span title="' + attribute(record.rnaTwo) + '">' + escape(record.rnaTwo) + "</span>",
        "<span>" + range(record.rnaTwoStart, record.rnaTwoEnd) + "</span>",
        '<span title="' + attribute(record.chimeraType) + '">' + escape(record.chimeraType) + "</span>",
        "<span>" + format(record.supportCount) + "</span>",
        "<span>" + (record.dg === null ? "—" : format(record.dg)) + "</span>",
        "</button>"
      ].join("");
    }).join("");
  }

  function renderRecordDrawer(record, state) {
    return [
      '<aside class="record-drawer" aria-label="Interaction record details">',
      '<div class="drawer-heading"><div><span class="drawer-kicker">Interaction record</span><h2>' + escape(record.id) + "</h2></div>",
      '<button class="icon-button" type="button" data-feature-action="close-record" aria-label="Close record details">×</button></div>',
      '<div class="drawer-section"><span class="detail-label">Sequence</span><code class="sequence-preview">' + escape(truncate(record.sequence, 180)) + "</code></div>",
      renderArmDetails("Arm 1", record.rnaOne, record.rnaOneStart, record.rnaOneEnd, record.rnaOneReadStart, record.rnaOneReadEnd, record.rnaOneEvalue),
      renderArmDetails("Arm 2", record.rnaTwo, record.rnaTwoStart, record.rnaTwoEnd, record.rnaTwoReadStart, record.rnaTwoReadEnd, record.rnaTwoEvalue),
      '<dl class="detail-grid"><div><dt>ΔG (kcal/mol)</dt><dd>' + (record.dg === null ? "—" : format(record.dg)) + '</dd></div><div><dt>Cluster support</dt><dd>' + format(record.supportCount) + '</dd></div><div><dt>Source reads</dt><dd>' + (record.rawReadCount === null ? "—" : format(record.rawReadCount)) + '</dd></div><div><dt>Overlap score</dt><dd>' + (record.overlapScore === null ? "—" : format(record.overlapScore)) + '</dd></div><div><dt>Homodimer proxy</dt><dd>' + (record.isHomodimer ? "Yes (overlap ≥ 5)" : "No") + '</dd></div><div><dt>Chimera type</dt><dd>' + escape(record.chimeraType) + '</dd></div><div><dt>Type source</dt><dd>' + escape(record.chimeraTypeSource === "column_17" ? "Column 17" : "Inferred from arms") + '</dd></div><div><dt>Source line</dt><dd>' + format(record.lineNumber) + "</dd></div></dl>",
      '<div class="drawer-actions">',
      '<button class="button button-secondary" type="button" data-feature-action="record-contact" data-record-index="' + record.index + '">Open in Contact Map</button>',
      '<button class="button button-secondary" type="button" data-feature-action="record-region" data-record-index="' + record.index + '" data-arm="1">Explore RNA 1 region</button>',
      '<button class="button button-secondary" type="button" data-feature-action="record-region" data-record-index="' + record.index + '" data-arm="2">Explore RNA 2 region</button>',
      '<button class="button button-secondary" type="button" data-feature-action="record-fold" data-record-index="' + record.index + '">Fold sequence</button>',
      '<button class="button button-secondary" type="button" data-feature-action="copy-record" data-record-index="' + record.index + '">Copy record</button>',
      "</div></aside>"
    ].join("");
  }

  function renderArmDetails(label, rna, start, end, readStart, readEnd, evalue) {
    return [
      '<section class="drawer-section"><h3>' + label + "</h3>",
      '<dl class="detail-list"><div><dt>RNA</dt><dd>' + escape(rna) + "</dd></div>",
      "<div><dt>RNA coordinates</dt><dd>" + range(start, end) + "</dd></div>",
      "<div><dt>Read coordinates</dt><dd>" + nullableRange(readStart, readEnd) + "</dd></div>",
      "<div><dt>E-value</dt><dd>" + (evalue === null ? "—" : format(evalue)) + "</dd></div></dl></section>"
    ].join("");
  }

  function renderContactMap(state) {
    const contact = state.contact;
    const rnas = state.summary.rnaNames || [];
    const chimeraTypes = state.summary.chimeraTypes || [];
    // contact.matrix is built from DataProvider.getContactBins by page-data.js.
    // While a rebuild is in flight the previous map stays on screen, dimmed
    // (matrixStale), or a placeholder is shown on first entry.
    const hasPair = !!(contact.rnaX && contact.rnaY);
    const matrix = hasPair ? contact.matrix || null : null;
    const building = hasPair && (!matrix || !!contact.matrixStale);
    const budgetHint = matrix && matrix.resourceBudget
      ? "Adaptive browser budget: up to " + format(matrix.resourceBudget.maximumCells) + " cells / " + format(matrix.resourceBudget.maximumBinContributions) + " bin contributions. "
      : "";
    const selection = contact.selection;
    // A cell's records/support equal the rows whose arms cover that bin pair.
    const selectedCell = selection && matrix && matrix.cellMap
      ? matrix.cellMap[selection.x + ":" + selection.y] || { records: 0, support: 0 }
      : { records: 0, support: 0 };

    return [
      '<div class="page-heading">',
      "<div><h1>Contact Map</h1><p>Fixed-width binning mirrors the current HYB2 coverage accumulation rule.</p></div>",
      '<div class="inline-actions"><button class="button button-secondary" type="button" data-feature-action="contact-download-svg">Download SVG</button><button class="button button-secondary" type="button" data-feature-action="contact-download-png">Download PNG</button></div>',
      "</div>",
      '<section class="analysis-controls data-card">',
      '<div class="contact-control-grid">',
      renderSelectControl("RNA X", "rnaX", contact.rnaX, rnas.map(function (rna) { return { value: rna, label: rna }; }), "contact-control"),
      renderSelectControl("RNA Y", "rnaY", contact.rnaY, rnas.map(function (rna) { return { value: rna, label: rna }; }), "contact-control"),
      renderSelectControl("Bin size", "binSize", String(contact.binSize), [
        { value: "5", label: "5 nt" },
        { value: "10", label: "10 nt" },
        { value: "25", label: "25 nt" },
        { value: "50", label: "50 nt" },
        { value: "100", label: "100 nt" }
      ], "contact-control"),
      renderSelectControl("Measure", "measure", contact.measure, [
        { value: "records", label: "Record count" },
        { value: "support", label: "Cluster support" }
      ], "contact-control"),
      renderSelectControl("Colour cap", "colourCap", contact.colourCap, [
        { value: "90", label: "90th percentile" },
        { value: "95", label: "95th percentile" },
        { value: "99", label: "99th percentile" },
        { value: "max", label: "Maximum" },
        { value: "custom", label: "Custom" }
      ], "contact-control"),
      renderSelectControl("Scale", "scale", contact.scale, [
        { value: "linear", label: "Linear" },
        { value: "log", label: "Log1p" }
      ], "contact-control"),
      contact.colourCap === "custom" ? renderTextControl("Custom cap", "customCap", contact.customCap, "Value", "contact-control", "number") : "",
      renderSelectControl("Orientation", "orientation", contact.orientation, [
        { value: "normalised", label: "Normalised pairs" },
        { value: "original", label: "Original arms (no swap)" }
      ], "contact-control"),
      renderSelectControl("Chimera type", "chimeraType", contact.chimeraType, [{ value: "all", label: "All chimera types" }].concat(chimeraTypes.map(function (item) { return { value: item.name, label: item.name }; })), "contact-control"),
      renderSelectControl("Homodimer subset", "homodimer", contact.homodimer || "all", [
        { value: "all", label: "All records" },
        { value: "only", label: "Overlap score ≥ 5" },
        { value: "exclude", label: "Exclude homodimer proxy" }
      ], "contact-control"),
      "</div>",
      '<div class="control-actions"><button class="button" type="button" data-feature-action="generate-contact">Generate</button><button class="quiet-button" type="button" data-feature-action="reset-contact">Reset map</button><button class="quiet-button" type="button" data-feature-action="reset-contact-zoom">Reset zoom</button><span class="control-hint">' + escape(budgetHint) + 'Drag across the map to zoom to a region, or click a cell to zoom one step. Double-click or Reset zoom returns to the full extent.</span></div>',
      "</section>",
      matrix && matrix.binSizeAdjusted ? '<p class="notice notice-warning">The bin size was increased to ' + format(matrix.binSize) + ' nt so the map stays within the cell budget.</p>' : "",
      matrix && matrix.cells.length ? [
        '<section class="contact-workbench' + (building ? " is-updating" : "") + '"' + (building ? ' aria-busy="true"' : "") + '>',
        '<div class="contact-navigator contact-workbench-main" aria-label="Interactive contact map">',
        renderContactStage("overview", "Contact map", "Drag to select a region. Click a cell to zoom and inspect it.", contact.overviewSelection, contact, matrix),
        building ? '<div class="contact-updating" role="status"><span></span>Updating map…</div>' : "",
        "</div>",
        '<aside class="contact-workbench-side">',
        renderContactSelection(contact, matrix, selectedCell, state),
        '<div class="heatmap-card data-card">',
        '<div class="heatmap-topline"><span class="card-kicker">Viridis · ' + escape(contact.scale === "log" ? "log1p display" : "linear display") + '</span><span class="heatmap-tooltip">Image exports use the currently visible extent.</span></div>',
        '<div class="heatmap-axis"><span>' + escape(contact.rnaX) + " · X axis</span><span>" + escape(contact.rnaY) + " · Y axis</span></div>",
        '<div class="heatmap-footer"><button class="quiet-button" type="button" data-feature-action="toggle-contact-data">' + (contact.showDataTable ? "Hide data table" : "Open data table") + '</button><button class="quiet-button" type="button" data-feature-action="contact-export-tsv">Export contact TSV</button><button class="quiet-button" type="button" data-feature-action="contact-export-params">Export parameters</button></div>',
        "</div>",
        "</aside>",
        "</section>",
        contact.showDataTable ? '<section class="data-card contact-data-card">' + renderContactDataTable(matrix) + "</section>" : ""
      ].join("") : (building ? renderContactBuilding() : renderNoContactData(contact, matrix)),
      "      <section class=\"method-note\"><strong>Method</strong><span>Each RNA arm covers every fixed-width bin between its start and end coordinates. The chosen measure is added to each bin pair: one per row for record count, or legacy cluster support when count metadata exists. Standard HYB2 overlap scores are preserved as annotations; overlap ≥ 5 is available separately as the original homodimer proxy.</span></section>"
    ].join("");
  }

  function renderContactStage(stage, title, instruction, selection, contact, matrix) {
    const unlocked = stage === "overview" || (stage === "zoom" && contact.overviewSelection) || (stage === "detail" && contact.zoomSelection);
    const rangeText = selection
      ? escape(contact.rnaX) + ":" + range(selection.x, selection.x + matrix.binSize - 1) + " × " + escape(contact.rnaY) + ":" + range(selection.y, selection.y + matrix.binSize - 1)
      : "Waiting for selection";
    const stageLabel = contact.overviewView ? "Zoomed extent" : "Full extent";

    return [
      '<article class="contact-stage data-card contact-stage-' + stage + '">',
      '<div class="contact-stage-heading"><div><span class="drawer-kicker">' + title + '</span><h2>' + stageLabel + '</h2></div><div class="contact-stage-heading-actions"><span class="card-kicker">' + rangeText + "</span><button class=\"quiet-button\" type=\"button\" data-feature-action=\"reset-contact-zoom\">Reset zoom</button></div></div>",
      '<p class="contact-stage-note">' + instruction + "</p>",
      unlocked
        ? '<div class="canvas-wrap contact-stage-canvas-wrap"><canvas id="contact-' + stage + '-canvas" class="contact-canvas contact-stage-canvas" width="1040" height="640" tabindex="0" data-contact-stage="' + stage + '" aria-label="' + title + ' contact map. ' + instruction + '"></canvas></div><span class="heatmap-tooltip" data-contact-tooltip="' + stage + '">Hover a cell for coordinates and counts.</span>'
        : '<div class="contact-stage-placeholder"><span>Selection needed</span><p>' + (stage === "zoom" ? "Choose a cell in the overview map to open this local map." : "Choose a cell in the local map to open this detailed map.") + "</p></div>",
      "</article>"
    ].join("");
  }

  function renderContactSelection(contact, matrix, cell, state) {
    if (!contact.selection) {
      return [
        '<aside class="selection-card data-card"><span class="drawer-kicker">Selected region</span><h2>Select a heatmap cell</h2>',
        "<p>Hover to inspect a bin. Click a cell to load its local record summary and continue into the Region Explorer.</p>",
        '<dl class="selection-stats"><div><dt>Records in map</dt><dd>' + format(matrix.recordsUsed) + "</dd></div><div><dt>Cluster support</dt><dd>" + format(matrix.totalSupport) + "</dd></div></dl></aside>"
      ].join("");
    }

    const selection = contact.selection;
    return [
      '<aside class="selection-card data-card"><span class="drawer-kicker">Selected region</span>',
      "<h2>" + escape(contact.rnaX) + ":" + range(selection.x, selection.x + matrix.binSize - 1) + "</h2>",
      "<h2>" + escape(contact.rnaY) + ":" + range(selection.y, selection.y + matrix.binSize - 1) + "</h2>",
      '<dl class="selection-stats"><div><dt>Records</dt><dd>' + format(cell.records) + "</dd></div><div><dt>Cluster support</dt><dd>" + format(cell.support) + "</dd></div></dl>",
      '<button class="button" type="button" data-feature-action="contact-explore-region">Explore region</button>',
      renderFoldRequestButton(state, "contact-map"),
      '<button class="quiet-button" type="button" data-feature-action="contact-export-selected">Export records</button>',
      "</aside>"
    ].join("");
  }

  function renderContactDataTable(matrix) {
    return [
      '<div class="contact-data-table data-table-wrap"><table class="data-table"><thead><tr><th>X position</th><th>Y position</th><th>Records</th><th>Cluster support</th></tr></thead><tbody>',
      matrix.cells.slice().sort(function (left, right) { return right.value - left.value; }).slice(0, 100).map(function (cell) {
        return "<tr><td>" + range(cell.x, cell.x + matrix.binSize - 1) + "</td><td>" + range(cell.y, cell.y + matrix.binSize - 1) + "</td><td>" + format(cell.records) + "</td><td>" + format(cell.support) + "</td></tr>";
      }).join(""),
      "</tbody></table></div>"
    ].join("");
  }

  function renderContactBuilding() {
    return [
      '<section class="contact-workbench is-building" aria-busy="true">',
      '<div class="contact-navigator contact-workbench-main"><article class="contact-stage data-card contact-stage-overview">',
      '<div class="contact-stage-heading"><div><span class="drawer-kicker">Contact map</span><h2>Building map</h2></div></div>',
      '<p class="contact-stage-note">Binning interaction arms locally in this tab.</p>',
      '<div class="canvas-wrap contact-stage-canvas-wrap contact-building-surface"><div class="contact-updating" role="status"><span></span>Building map…</div></div>',
      "</article></div>",
      '<aside class="contact-workbench-side"><aside class="selection-card data-card"><span class="drawer-kicker">Selected region</span><h2>Select a heatmap cell</h2><p>The map is being prepared.</p></aside></aside>',
      "</section>"
    ].join("");
  }

  // Embedded hosts (mount with shell:false and onRequestFold) route "fold this
  // region" themselves; the standalone website keeps its existing actions.
  function renderFoldRequestButton(state, source) {
    if (!state || !state.canRequestFold) {
      return "";
    }
    return '<button class="button button-secondary" type="button" data-feature-action="request-fold" data-source="' + attribute(source) + '">Fold this region</button>';
  }

  function renderNoContactData(contact, matrix) {
    const limited = matrix && matrix.limitExceeded && matrix.reason;
    const loading = !matrix && contact.rnaX && contact.rnaY;
    const message = limited
      ? matrix.reason
      : loading
      ? "Preparing the contact map…"
      : contact.rnaX && contact.rnaY
      ? "No interactions were found for the selected RNA pair."
      : "Choose two RNAs to generate a contact map.";
    const budgetNote = matrix && matrix.resourceBudget
      ? "Adaptive browser budget: up to " + format(matrix.resourceBudget.maximumCells) + " cells and " + format(matrix.resourceBudget.maximumBinContributions) + " bin contributions, estimated from browser memory and a short local performance probe."
      : "Contact map calculations stay local and are generated from the HYB records currently loaded in this tab.";
    return [
      '<section class="placeholder-card contact-empty"><div class="placeholder-inner"><span class="placeholder-label">' + (limited ? "Contact map limit" : "Contact map") + "</span><h2>" + escape(message) + "</h2>",
      "<p>" + escape(budgetNote) + "</p></div></section>"
    ].join("");
  }

  function renderViewpoint(state) {
    const viewpoint = state.viewpoint;
    const rnas = state.summary.rnaNames || [];
    const chimeraTypes = state.summary.chimeraTypes || [];
    const mappedReference = hasMappedReference(state, viewpoint.rna);
    if (viewpoint.rangeMode === "reference" && !mappedReference) {
      viewpoint.rangeMode = "coordinates";
      viewpoint.results = null;
    }
    // viewpoint.results is computed from DataProvider.getArms by page-data.js.
    const loading = !viewpoint.results;
    const results = viewpoint.results || { ready: false, bins: [], records: [] };
    const selected = viewpoint.selection && results.ready
      ? results.bins.find(function (bin) { return bin.start === viewpoint.selection.start; })
      : null;

    return [
      '<div class="page-heading">',
      '<div><h1>Viewpoint</h1><p>Map interaction abundance across a selected interval or the complete mapped reference.</p></div>',
      '<div class="inline-actions"><button class="button button-secondary" type="button" data-feature-action="viewpoint-download-svg">Download SVG</button><button class="button button-secondary" type="button" data-feature-action="viewpoint-download-png">Download PNG</button></div>',
      '</div>',
      '<section class="analysis-controls data-card">',
      '<div class="viewpoint-control-grid">',
      renderSelectControl("Anchor RNA", "rna", viewpoint.rna, rnas.map(function (rna) { return { value: rna, label: rna }; }), "viewpoint-control"),
      renderSelectControl("Partner", "partner", viewpoint.partner, partnerOptions(rnas, viewpoint.rna), "viewpoint-control"),
      renderSelectControl("Chimera type", "chimeraType", viewpoint.chimeraType, [{ value: "all", label: "All chimera types" }].concat(chimeraTypes.map(function (item) { return { value: item.name, label: item.name }; })), "viewpoint-control"),
      renderSelectControl("Homodimer subset", "homodimer", viewpoint.homodimer || "all", [
        { value: "all", label: "All records" },
        { value: "only", label: "Overlap score ≥ 5" },
        { value: "exclude", label: "Exclude homodimer proxy" }
      ], "viewpoint-control"),
      renderSelectControl("Coordinate range", "rangeMode", viewpoint.rangeMode || "coordinates", mappedReference ? [
        { value: "coordinates", label: "Selected coordinates" },
        { value: "reference", label: "Full mapped reference" }
      ] : [{ value: "coordinates", label: "Selected coordinates" }], "viewpoint-control"),
      renderTextControl("Start", "start", viewpoint.start, "Observed start", "viewpoint-control", "number", { min: 1, step: 1 }),
      renderTextControl("End", "end", viewpoint.end, "Observed end", "viewpoint-control", "number", { min: 1, step: 1 }),
      renderSelectControl("Resolution", "binSize", viewpoint.binSize, [
        { value: "1", label: "1 nt (legacy viewpoint)" },
        { value: "5", label: "5 nt mean" },
        { value: "10", label: "10 nt mean" },
        { value: "25", label: "25 nt mean" },
        { value: "50", label: "50 nt mean" },
        { value: "100", label: "100 nt mean" }
      ], "viewpoint-control"),
      renderSelectControl("Measure", "measure", viewpoint.measure, [
        { value: "records", label: "Record count" },
        { value: "support", label: "Cluster support" }
      ], "viewpoint-control"),
      '</div>',
      '<div class="control-actions"><button class="button" type="button" data-feature-action="generate-viewpoint">Generate</button><button class="quiet-button" type="button" data-feature-action="reset-viewpoint-selection">Reset selection</button><span class="control-hint">' + (viewpoint.rangeMode === "reference" ? "The full mapped reference uses coordinates 1 through its FASTA length; Start and End are ignored." : "At 1 nt resolution, each plotted value is the number of overlapping interaction arms, matching the legacy viewpoint interpretation.") + '</span></div>',
      '</section>',
      results.ready ? [
        '<section class="viewpoint-layout">',
        '<section class="viewpoint-card data-card">',
        '<div class="card-title-row"><div><h2>' + escape(results.rna) + ' interaction coverage</h2><span class="card-note">' + format(results.records.length) + ' matching records · ' + format(results.armContributions) + ' mapped arms</span></div><span class="card-kicker">' + escape(viewpoint.measure === "support" ? "Cluster support" : "Records") + '</span></div>',
        results.binSizeAdjusted ? '<p class="notice notice-warning">The requested resolution was increased to ' + format(results.binSize) + ' nt to keep this browser-local plot responsive.</p>' : '',
        '<div class="canvas-wrap viewpoint-canvas-wrap"><canvas id="viewpoint-canvas" class="viewpoint-canvas" width="1040" height="300" tabindex="0" aria-label="Viewpoint graph. Click a bar to select a nucleotide interval."></canvas></div>',
        '<div class="profile-axis"><span>' + format(results.start) + '</span><span>' + format(results.end) + '</span></div>',
        '<div class="heatmap-footer"><button class="quiet-button" type="button" data-feature-action="toggle-viewpoint-data">' + (viewpoint.showDataTable ? "Hide data table" : "Open data table") + '</button><button class="quiet-button" type="button" data-feature-action="viewpoint-export-tsv">Export viewpoint TSV</button><button class="quiet-button" type="button" data-feature-action="viewpoint-export-params">Export parameters</button></div>',
        viewpoint.showDataTable ? renderViewpointDataTable(results) : '',
        '</section>',
        renderViewpointSelection(viewpoint, results, selected),
        '</section>'
      ].join("") : '<section class="placeholder-card"><div class="placeholder-inner"><span class="placeholder-label">Viewpoint</span><h2>' + (loading ? "Preparing the viewpoint…" : escape(results.reason || "Choose an RNA and coordinate range to start.")) + '</h2><p>The graph uses local HYB arm coordinates; a reference FASTA is optional.</p></div></section>',
      '<section class="method-note"><strong>Method</strong><span>Each matching arm contributes across all of its inclusive RNA coordinates. One-nucleotide bins reproduce the original HYB2 viewpoint count; larger bins display mean coverage per nucleotide. A mapped FASTA can extend the graph across the complete reference, including zero-coverage positions.</span></section>'
    ].join("");
  }

  function renderViewpointSelection(viewpoint, results, selected) {
    if (!selected) {
      return '<aside class="selection-card data-card"><span class="drawer-kicker">Selected interval</span><h2>Select a viewpoint bar</h2><p>Click a plotted interval to send its coordinates to Region Explorer.</p><dl class="selection-stats"><div><dt>Maximum coverage</dt><dd>' + format(results.max) + '</dd></div><div><dt>Resolution</dt><dd>' + format(results.binSize) + ' nt</dd></div></dl></aside>';
    }

    return [
      '<aside class="selection-card data-card"><span class="drawer-kicker">Selected interval</span>',
      '<h2>' + escape(results.rna) + ':' + range(selected.start, selected.end) + '</h2>',
      '<dl class="selection-stats"><div><dt>Mean coverage</dt><dd>' + format(selected.value) + '</dd></div><div><dt>Mapped arms</dt><dd>' + format(results.armContributions) + '</dd></div></dl>',
      '<button class="button" type="button" data-feature-action="viewpoint-explore-region">Explore region</button>',
      '</aside>'
    ].join("");
  }

  function renderViewpointDataTable(results) {
    return [
      '<div class="contact-data-table data-table-wrap"><table class="data-table"><thead><tr><th>Start</th><th>End</th><th>Mean interaction coverage</th></tr></thead><tbody>',
      results.bins.slice().sort(function (left, right) { return right.value - left.value; }).slice(0, 150).map(function (bin) {
        return '<tr><td>' + format(bin.start) + '</td><td>' + format(bin.end) + '</td><td>' + format(bin.value) + '</td></tr>';
      }).join(""),
      '</tbody></table></div>'
    ].join("");
  }

  function renderComparison(state) {
    const comparison = state.comparison;
    const rnas = comparisonRnaNames(comparison);
    const normalisationUnit = comparison.measure === "support" ? "support units" : "HYB records";
    const comparisonValueDescription = (comparison.normalise === "library" ? "per-million-normalised " : "raw ") +
      (comparison.measure === "support" ? "cluster-support units" : "record counts");
    // comparison.result is built from per-dataset DataProvider.getContactBins
    // by page-data.js; datasets come from DataProvider.listDatasets().
    const result = comparison.loading
      ? { ready: false, reason: "Finish parsing the selected local datasets before generating or exporting a comparison.", groupA: [], groupB: [], cells: [], conservedCells: [] }
      : (comparison.result || { ready: false, reason: "Preparing the comparison…", groupA: [], groupB: [], cells: [], conservedCells: [] });
    const canAddDatasets = state.shell !== false;
    const groupA = result.groupA || [];
    const groupB = result.groupB || [];
    const selected = comparison.selection && result.ready ? window.Hyb2Data.comparisonCellAt(result, comparison.selection.x, comparison.selection.y) : null;

    return [
      '<div class="page-heading">',
      '<div><h1>Compare</h1><p>Compare multiple local HYB datasets: the DESeq2 differential test, contact-map effect sizes and the HYB2 similarity heatmap.</p></div>',
      '<div class="inline-actions">' + (canAddDatasets ? '<button class="button" type="button" data-feature-action="choose-comparison-files">Add HYB datasets</button>' : '') + '<button class="button button-secondary" type="button" data-feature-action="comparison-download-svg">Download effect SVG</button><button class="button button-secondary" type="button" data-feature-action="comparison-download-png">Download effect PNG</button></div>',
      '</div>',
      '<section class="comparison-note notice notice-warning"><strong>What this page can and cannot tell you</strong><span>Only the <b>differential test</b> (panel A) says whether a contact differs between ' + escape(comparison.conditionALabel) + ' and ' + escape(comparison.conditionBLabel) + ': it uses the replicates to estimate noise and reports an adjusted p-value (padj) for every 10-nt cell. It needs at least two biological replicates per condition. The effect map and similarity heatmap only describe the counts; they are not tests. Positions are nucleotides on each dataset\'s own RNA, so different strains are compared by position, not by alignment.</span><details class="comparison-note-details" data-open-key="compare-tech"' + openAttr("compare-tech") + '><summary>How the test was checked</summary><span>The test is a JavaScript port of HYB2 Desktop\'s port of DESeq2 1.46, not R itself. On the ZIKV virion comparison it finds the same significant cells and merged-interaction counts as R, with log2 fold changes and padj within 1e-6 (relative) of HYB2 Desktop. For R or the HYB2 command-line tool, use the exports under Map settings.</span></details></section>',
      '<section class="data-card comparison-datasets">',
      canAddDatasets
        ? '<div class="card-title-row"><div><h2>Datasets and conditions</h2><span class="card-note">The primary file is included as the first local dataset. Add one or more HYB files, then assign each to Condition A or B.</span></div><span class="card-kicker">' + format((comparison.datasets || []).length) + ((comparison.datasets || []).length === 1 ? ' local file' : ' local files') + '</span></div>'
        : '<div class="card-title-row"><div><h2>Datasets and conditions</h2><span class="card-note">Assign each project dataset to Condition A or B.</span></div><span class="card-kicker">' + format((comparison.datasets || []).length) + ' datasets</span></div>',
      comparison.loading ? '<p class="notice">Parsing ' + escape(comparison.loading.fileName) + ' locally (' + format(comparison.loading.completed + 1) + ' of ' + format(comparison.loading.total) + ')…</p>' : '',
      comparison.error ? '<p class="notice notice-error">' + escape(comparison.error) + '</p>' : '',
      renderComparisonDatasetTable(comparison),
      renderComparisonRnaPairs(comparison),
      '<div class="inline-actions">' + (canAddDatasets ? '<button class="quiet-button" type="button" data-feature-action="choose-comparison-files">Add datasets</button>' : '') + '<span class="card-note">For the original DESeq2 workflow, use at least two biological replicates per condition.</span></div>',
      '</section>',
      // the main question comes first: the DESeq2 test bins its own 10-nt
      // cells, independent of the map settings below
      window.Hyb2DeseqCard ? window.Hyb2DeseqCard.renderCard(state) : "",
      '<section class="analysis-controls data-card">',
      '<div class="card-title-row"><div><h2>Map settings</h2><span class="card-note">These settings change the effect map, the similarity heatmap and their exports. The differential test above bins its own 10-nt cells and is not affected by Bin size, Measure or Normalisation.</span></div></div>',
      '<div class="comparison-control-grid">',
      renderTextControl("Condition A label", "conditionALabel", comparison.conditionALabel, "Condition A", "comparison-control"),
      renderTextControl("Condition B label", "conditionBLabel", comparison.conditionBLabel, "Condition B", "comparison-control"),
      renderSelectControl("RNA X", "rnaX", comparison.rnaX, rnas.map(function (rna) { return { value: rna, label: rna }; }), "comparison-control"),
      renderSelectControl("RNA Y", "rnaY", comparison.rnaY, rnas.map(function (rna) { return { value: rna, label: rna }; }), "comparison-control"),
      renderSelectControl("Bin size", "binSize", comparison.binSize, [
        { value: "5", label: "5 nt" }, { value: "10", label: "10 nt" }, { value: "25", label: "25 nt" }, { value: "50", label: "50 nt" }, { value: "100", label: "100 nt" }
      ], "comparison-control"),
      renderSelectControl("Measure", "measure", comparison.measure, [
        { value: "records", label: "Record count" }, { value: "support", label: "Cluster support" }
      ], "comparison-control"),
      renderSelectControl("Normalisation", "normalise", comparison.normalise, [
        { value: "library", label: "Per million " + normalisationUnit }, { value: "none", label: "Raw mean count" }
      ], "comparison-control"),
      renderSelectControl("Similarity minimum", "similarityMinimum", comparison.similarityMinimum || "1", [
        { value: "1", label: "Every shared bin (HYB2 default)" }, { value: "2", label: "≥ 2 per condition" }, { value: "5", label: "≥ 5 per condition" },
        { value: "10", label: "≥ 10 per condition" }, { value: "20", label: "≥ 20 per condition" }, { value: "30", label: "≥ 30 per condition" }, { value: "50", label: "≥ 50 per condition" }
      ], "comparison-control"),
      // display only: bins at the smallest count are white, as in R, and
      // vanish on R's white panel; the grey panel (ggplot's theme_grey) shows them
      renderSelectControl("Similarity panel", "similarityPanel", comparison.similarityPanel || "white", [
        { value: "white", label: "White (as similarity_heatmap.R)" }, { value: "grey", label: "Light grey (shows bins at the minimum)" }
      ], "comparison-display"),
      '</div>',
      '<div class="control-actions"><button class="button" type="button" data-feature-action="generate-comparison">Update comparison</button><button class="quiet-button" type="button" data-feature-action="comparison-export-tsv">Export comparison TSV</button><button class="quiet-button" type="button" data-feature-action="comparison-export-params">Export parameters</button></div>',
      '<details class="comparison-export-more" data-open-key="compare-exports"' + openAttr("compare-exports") + '><summary>Exports for R or the HYB2 command-line tool</summary><div class="inline-actions"><button class="quiet-button" type="button" data-feature-action="comparison-export-deseq-counts">Export HYB2 count table</button><button class="quiet-button" type="button" data-feature-action="comparison-export-hyb2-names">Export HYB2 names.table</button><button class="quiet-button" type="button" data-feature-action="comparison-export-deseq-metadata">Export extended metadata</button></div>',
      // a rerun keeps the previous maps on screen: the progress shows here
      comparison.result ? renderComparisonProgress(comparison.progress) : "",
      '<p class="card-note">Use the headerless HYB2 names.table with the HYB2 command-line tool. Extended metadata has a header and provenance columns for audit or your own R workflow.</p></details>',
      '</section>',
      result.ready ? [
        '<section class="comparison-summary data-card"><dl class="selection-stats"><div><dt>' + escape(comparison.conditionALabel) + ' datasets</dt><dd>' + format(groupA.length) + '</dd></div><div><dt>' + escape(comparison.conditionBLabel) + ' datasets</dt><dd>' + format(groupB.length) + '</dd></div><div><dt>Comparable bins</dt><dd>' + format(result.cells.length) + '</dd></div><div><dt>Similarity bins</dt><dd>' + format(result.conservedCells.length) + '</dd></div></dl></section>',
        '<section class="comparison-map-grid">',
        renderComparisonMap("effect", "Mean log2 effect", comparison.conditionALabel + ' enriched → violet · ' + comparison.conditionBLabel + ' enriched → teal. Descriptive, not a test: bins with few records give strong colours by chance, so use the differential test (A) to judge a difference.', result),
        renderComparisonMap("conserved", "B · Similarity heatmap", "Bins in both conditions" + (result.similarityMinimum > 1 ? " (≥ " + format(result.similarityMinimum) + " each)" : "") + "; the smaller raw count, white at the smallest (" + format(result.similarityFloor) + ") to green at the 95% quantile cap (" + format(result.similarityCap) + ")" + (comparison.similarityPanel === "grey" ? " on a grey panel." : "; on R's white panel the smallest bins do not show (choose the grey panel to see them)."), result),
        '</section>',
        renderComparisonSelection(selected, comparison, result, state),
        comparison.showDataTable ? renderComparisonDataTable(result, comparison) : ''
      ].join("") : '<section class="placeholder-card"><div class="placeholder-inner"><span class="placeholder-label">Comparison</span><h2>' + escape(result.reason || "Add datasets to compare") + '</h2>' + (!comparison.loading && !comparison.result ? renderComparisonProgress(comparison.progress || { stage: "Starting", fraction: 0 }) : "") + '<p>All input files remain in tab memory and are parsed by local browser workers.</p></div></section>',
      // the arc map works from its own files, with or without a comparison here
      window.Hyb2ArcMap ? window.Hyb2ArcMap.renderCard(state.arcMap || window.Hyb2ArcMap.defaultState(), escape) : "",
      window.Hyb2ArcMap && window.Hyb2RegionPanels ? window.Hyb2RegionPanels.renderCard(state.arcMap, state.regionPanels, escape) : "",
      window.Hyb2ArcMap && window.Hyb2FoldingEnergy ? window.Hyb2FoldingEnergy.renderCard(state.arcMap, state.regionPanels, state.foldingEnergy, escape) : "",
      '<section class="method-note"><strong>Method</strong><span>For each dataset, HYB arms are accumulated into fixed contact bins. Each condition uses the mean of its dataset-level counts, optionally scaled per million ' + normalisationUnit + '. The effect map reports log2((A + pseudocount) / (B + pseudocount)); it is a descriptive effect size, not a significance test. The similarity heatmap follows HYB2 plot_similarity_map and similarity_heatmap.R: each condition\'s datasets are summed into one raw contact map (as if their .hyb files were concatenated), bins below the similarity minimum are dropped from each map, and a bin present in both maps shows the smaller count.</span></section>'
    ].join("");
  }

  function renderComparisonDatasetTable(comparison) {
    const datasets = comparison.datasets || [];
    return [
      '<div class="data-table-wrap comparison-dataset-table"><table class="data-table"><thead><tr><th>Dataset</th><th>Label</th><th>Condition</th><th>Valid records</th><th>Support</th><th></th></tr></thead><tbody>',
      datasets.map(function (dataset) {
        return '<tr><td title="' + attribute(dataset.fileName) + '">' + escape(dataset.fileName) + '</td><td><input type="text" value="' + attribute(dataset.label || "") + '" data-feature="comparison-dataset" data-key="label" data-dataset-id="' + attribute(dataset.id) + '" aria-label="Dataset label"></td><td><select data-feature="comparison-dataset" data-key="condition" data-dataset-id="' + attribute(dataset.id) + '" aria-label="Dataset condition"><option value="A"' + (dataset.condition === "A" ? " selected" : "") + '>Condition A</option><option value="B"' + (dataset.condition === "B" ? " selected" : "") + '>Condition B</option></select></td><td>' + format(dataset.summary.validRecords) + '</td><td>' + format(dataset.summary.supportInteractions) + '</td><td>' + (dataset.id === "primary" ? '<span class="card-note">Primary</span>' : '<button class="quiet-button" type="button" data-feature-action="remove-comparison-dataset" data-dataset-id="' + attribute(dataset.id) + '">Remove</button>') + '</td></tr>';
      }).join(""),
      '</tbody></table></div>'
    ].join("");
  }

  // datasets that use their own genome for a homodimer comparison (another
  // strain); the same rule as the differential test card
  function renderComparisonRnaPairs(comparison) {
    const substituted = window.Hyb2Data.comparisonRnaPlan(comparison).filter(function (item) { return item.substituted; });
    if (!substituted.length) {
      return "";
    }
    // one sentence per substitute RNA, naming every dataset that uses it
    const groups = [];
    substituted.forEach(function (item) {
      const name = item.dataset.label || item.dataset.fileName || item.dataset.id;
      const group = groups.find(function (entry) { return entry.rna === item.rnaX; });
      if (group) { group.names.push(name); } else { groups.push({ rna: item.rnaX, names: [name] }); }
    });
    return '<p class="notice comparison-rna-note"><strong>Compared by position.</strong> ' + groups.map(function (group) {
      return escape(group.names.join(" and ")) + (group.names.length === 1 ? " has" : " have") + " no " + escape(comparison.rnaX) + " records and " + (group.names.length === 1 ? "uses its" : "use their") + " own " + escape(group.rna) + ".";
    }).join(" ") + ' Bins are matched by nucleotide position, not by sequence alignment, as bin/hyb2_compare does. You can choose another RNA for each dataset in the differential test below.</p>';
  }

  function renderComparisonMap(kind, title, note, result) {
    return [
      '<section class="comparison-map-card data-card">',
      '<div class="card-title-row"><div><h2>' + escape(title) + '</h2><span class="card-note">' + escape(note) + '</span></div><span class="card-kicker">' + format(kind === "effect" ? result.cells.length : result.conservedCells.length) + ' bins</span></div>',
      '<div class="canvas-wrap comparison-canvas-wrap"><canvas id="comparison-' + kind + '-canvas" class="comparison-canvas" width="720" height="520" data-comparison-map="' + kind + '" tabindex="0" aria-label="' + escape(title) + ' map"></canvas></div>',
      '</section>'
    ].join("");
  }

  function renderComparisonSelection(selected, comparison, result, state) {
    if (!selected) {
      return '<section class="selection-card data-card comparison-selection"><span class="drawer-kicker">Selected comparison bin</span><h2>Select a cell in either map</h2><p>Inspect the two condition means and open the selected coordinates in Region Explorer.</p><button class="quiet-button" type="button" data-feature-action="toggle-comparison-data">' + (comparison.showDataTable ? "Hide comparison data table" : "Open comparison data table") + '</button></section>';
    }
    return [
      '<section class="selection-card data-card comparison-selection"><span class="drawer-kicker">Selected comparison bin</span>',
      '<h2>' + range(selected.x, selected.x + result.binSize - 1) + ' × ' + range(selected.y, selected.y + result.binSize - 1) + '</h2>',
      '<dl class="selection-stats"><div><dt>' + escape(comparison.conditionALabel) + ' mean</dt><dd>' + format(selected.conditionA) + '</dd></div><div><dt>' + escape(comparison.conditionBLabel) + ' mean</dt><dd>' + format(selected.conditionB) + '</dd></div><div><dt>Log2 effect</dt><dd>' + format(selected.effect) + '</dd></div><div><dt>Datasets present</dt><dd>' + format(selected.presentDatasets) + '</dd></div><div><dt>Similarity</dt><dd>' + format(selected.similarity) + '</dd></div></dl>',
      renderComparisonDatasetValues(selected, comparison),
      '<div class="inline-actions"><button class="button" type="button" data-feature-action="comparison-explore-region">Explore RNA X region</button>' + renderFoldRequestButton(state, "compare") + '<button class="quiet-button" type="button" data-feature-action="comparison-export-selected">Export dataset values</button><button class="quiet-button" type="button" data-feature-action="toggle-comparison-data">' + (comparison.showDataTable ? "Hide data table" : "Open data table") + '</button></div>',
      '</section>'
    ].join("");
  }

  function renderComparisonDatasetValues(selected, comparison) {
    const present = Object.create(null);
    (selected.datasets || []).forEach(function (item) { present[item.id] = item; });
    const rawLabel = comparison.measure === "support" ? "Raw support" : "Raw records";
    const analysisLabel = comparison.normalise === "library" ? "Per million" : "Analysis value";
    return [
      '<div class="data-table-wrap"><table class="data-table"><thead><tr><th>Dataset</th><th>Condition</th><th>' + rawLabel + '</th><th>' + analysisLabel + '</th></tr></thead><tbody>',
      (comparison.datasets || []).map(function (dataset) {
        const item = present[dataset.id];
        const conditionLabel = dataset.condition === "A" ? comparison.conditionALabel : comparison.conditionBLabel;
        return '<tr><td>' + escape(dataset.label || dataset.fileName || dataset.id) + '</td><td>' + escape(conditionLabel) + '</td><td>' + format(item ? item.rawValue : 0) + '</td><td>' + format(item ? item.value : 0) + '</td></tr>';
      }).join(""),
      '</tbody></table></div>'
    ].join("");
  }

  // stage and bar of a comparison being built (page-data.js updates them in place)
  function renderComparisonProgress(progress) {
    const percent = Math.round(100 * (progress && progress.fraction || 0));
    return '<div class="comparison-progress" data-comparison-progress' + (progress ? "" : " hidden") + '><p class="card-note" data-comparison-progress-label aria-live="polite">' +
      escape((progress && progress.stage || "Starting") + " · " + percent + "%") + '</p>' +
      '<div class="progress-bar" role="progressbar" aria-label="Comparison progress" aria-valuemin="0" aria-valuemax="100" aria-valuenow="' + percent + '"><span style="width:' + percent + '%"></span></div></div>';
  }

  const COMPARISON_TABLE_ROWS = 150;
  const COMPARISON_TABLE_SORTS = [
    { value: "effect", label: "Largest |log2 effect|" },
    { value: "similarity", label: "Largest similarity" },
    { value: "position", label: "Position (X, then Y)" }
  ];

  // One page of result.cells in the chosen order; the order is computed once
  // per result and sort key (Hyb2Data.comparisonOrder), so a render only
  // touches the rows shown.
  function renderComparisonDataTable(result, comparison) {
    const sort = COMPARISON_TABLE_SORTS.some(function (item) { return item.value === comparison.tableSort; }) ? comparison.tableSort : "effect";
    const total = result.cells.length;
    const pages = Math.max(1, Math.ceil(total / COMPARISON_TABLE_ROWS));
    const page = Math.max(0, Math.min(pages - 1, Math.floor(Number(comparison.tablePage) || 0)));
    const start = page * COMPARISON_TABLE_ROWS;
    const rows = window.Hyb2Data.comparisonOrderedCells(result, sort, start, COMPARISON_TABLE_ROWS);
    return [
      '<section class="data-card"><div class="card-title-row"><h2>Comparison bins</h2><button class="quiet-button" type="button" data-feature-action="toggle-comparison-data">Hide data table</button></div>',
      '<div class="inline-actions comparison-table-controls">',
      renderSelectControl("Sort bins by", "tableSort", sort, COMPARISON_TABLE_SORTS, "comparison-table"),
      '<span class="card-note">Bins ' + format(total ? start + 1 : 0) + '–' + format(start + rows.length) + ' of ' + format(total) + '</span>',
      '<button class="quiet-button" type="button" data-feature-action="comparison-table-page" data-page="' + (page - 1) + '"' + (page > 0 ? "" : " disabled") + '>Previous</button>',
      '<button class="quiet-button" type="button" data-feature-action="comparison-table-page" data-page="' + (page + 1) + '"' + (page < pages - 1 ? "" : " disabled") + '>Next</button>',
      '</div>',
      '<div class="data-table-wrap"><table class="data-table"><thead><tr><th>X interval</th><th>Y interval</th><th>' + escape(comparison.conditionALabel) + ' mean</th><th>' + escape(comparison.conditionBLabel) + ' mean</th><th>Log2 effect</th><th>Datasets present</th><th>Similarity</th></tr></thead><tbody>',
      rows.map(function (cell) {
        return '<tr><td>' + range(cell.x, cell.x + result.binSize - 1) + '</td><td>' + range(cell.y, cell.y + result.binSize - 1) + '</td><td>' + format(cell.conditionA) + '</td><td>' + format(cell.conditionB) + '</td><td>' + format(cell.effect) + '</td><td>' + format(cell.presentDatasets) + '</td><td>' + format(cell.similarity) + '</td></tr>';
      }).join(""),
      '</tbody></table></div></section>'
    ].join("");
  }

  function comparisonRnaNames(comparison) {
    const names = Object.create(null);
    (comparison.datasets || []).forEach(function (dataset) {
      (dataset.summary.rnaNames || []).forEach(function (name) { names[name] = true; });
    });
    return Object.keys(names).sort(function (left, right) { return left.localeCompare(right); });
  }

  function renderRegion(state) {
    const region = state.region;
    const rnas = state.summary.rnaNames || [];
    // region.results (DataProvider.queryHybrids({ region })) and the reference
    // slice (DataProvider.getSequence) are prepared by page-data.js.
    const loading = !region.results;
    const results = region.results || { records: [], support: 0, partners: [], profile: [], profileBinSize: 1 };
    const fastaRegion = regionReference(state);
    const partnerOptions = [{ value: "", label: "All partners" }].concat(rnas.map(function (rna) { return { value: rna, label: rna }; }));

    return [
      '<div class="page-heading"><div><h1>Region Explorer</h1><p>Find interactions that overlap a chosen RNA region.</p></div></div>',
      '<section class="analysis-controls data-card">',
      '<div class="region-control-grid">',
      renderSelectControl("Anchor RNA", "rna", region.rna, rnas.map(function (rna) { return { value: rna, label: rna }; }), "region-control"),
      renderTextControl("Start", "start", region.start, "Start", "region-control", "number", { min: 1, step: 1 }),
      renderTextControl("End", "end", region.end, "End", "region-control", "number", { min: 1, step: 1 }),
      renderSelectControl("Partner", "partner", region.partner, partnerOptions, "region-control"),
      renderTextControl("Partner start", "partnerStart", region.partnerStart, "Optional", "region-control", "number", { min: 1, step: 1 }),
      renderTextControl("Partner end", "partnerEnd", region.partnerEnd, "Optional", "region-control", "number", { min: 1, step: 1 }),
      renderSelectControl("Overlap rule", "overlap", region.overlap, [
        { value: "any", label: "Any overlap" },
        { value: "contained", label: "Fully contained" }
      ], "region-control"),
      "</div>",
      '<div class="control-actions"><button class="button" type="button" data-feature-action="explore-region">Explore</button>' + renderFoldRequestButton(state, "region") + '<span class="control-hint">Coordinates remain inclusive, matching the HYB record display.</span></div>',
      "</section>",
      region.rna && region.start !== "" && region.end !== "" ? [
        '<section class="region-results">',
        '<div class="region-summary data-card"><div class="card-title-row"><div><h2>' + escape(region.rna) + ":" + range(region.start, region.end) + '</h2><span class="card-note">' + (loading ? "Loading matching records…" : format(results.records.length) + " matching records") + (region.partner && region.partnerStart !== "" && region.partnerEnd !== "" ? " · " + escape(region.partner) + ":" + range(region.partnerStart, region.partnerEnd) : "") + '</span></div><span class="card-kicker">Local</span></div>',
        results.profileBinSizeAdjusted ? '<p class="notice notice-warning">The profile resolution was increased to ' + format(results.profileBinSize) + ' nt per bin to keep this browser-local plot responsive.</p>' : '',
        '<dl class="region-totals"><div><dt>Cluster support</dt><dd>' + format(results.support) + "</dd></div><div><dt>Partner RNAs</dt><dd>" + format(results.partners.length) + "</dd></div></dl>",
        '<div class="canvas-wrap region-canvas-wrap"><canvas id="region-profile-canvas" class="region-profile-canvas" width="960" height="210" aria-label="Interaction support position profile"></canvas></div>',
        '<div class="profile-axis"><span>' + format(region.start) + "</span><span>" + format(region.end) + "</span></div></div>",
        renderRegionPartners(results),
        "</section>",
        renderReferencePanel(fastaRegion, region, state),
        renderRegionRecords(results.records, region.showAllRecords),
        "</section>"
      ].join("") : renderEmptyRegion()
    ].join("");
  }

  function renderRegionPartners(results) {
    const max = results.partners.length ? results.partners[0].support : 1;
    return [
      '<section class="data-card region-partners"><div class="card-title-row"><h2>Top partners</h2><span class="card-kicker">Cluster support</span></div>',
      results.partners.length ? '<div class="bar-list">' + results.partners.slice(0, 8).map(function (partner) {
        return '<div class="bar-row"><span class="bar-name">' + escape(partner.name) + '</span><span class="bar-track"><span class="bar-fill" style="width:' + Math.max(3, (partner.support / max) * 100) + '%"></span></span><span class="bar-value">' + format(partner.support) + "</span></div>";
      }).join("") + "</div>" : '<p class="empty-inline">No matching interactions.</p>',
      "</section>"
    ].join("");
  }

  function renderReferencePanel(reference, region, state) {
    if (!reference) {
      return [
        '<section class="reference-panel data-card"><div><span class="drawer-kicker">Reference sequence unavailable</span><h2>Add a reference FASTA to extract this region.</h2>',
        "<p>Observed HYB coordinates remain available without a FASTA, but they do not provide the full reference sequence.</p></div>",
        (state && state.shell === false ? "" : '<button class="button" type="button" data-feature-action="choose-fasta">Add reference FASTA</button>') + "</section>"
      ].join("");
    }

    return [
      '<section class="reference-panel data-card"><div><span class="drawer-kicker">Reference sequence</span><h2>' + escape(reference.id) + ":" + range(reference.start, reference.end) + "</h2>",
      '<code class="reference-sequence">' + escape(truncate(reference.sequence, 260)) + "</code></div>",
      '<div class="inline-actions"><button class="button button-secondary" type="button" data-feature-action="copy-region-sequence">Copy sequence</button><button class="button button-secondary" type="button" data-feature-action="region-send-structure">Send to RNA Structure</button><button class="button button-secondary" type="button" data-feature-action="region-download-fasta">Download FASTA</button></div></section>'
    ].join("");
  }

  function renderRegionRecords(records, showAll) {
    const limit = showAll ? 100 : 20;
    return [
      '<section class="data-card"><div class="card-title-row"><h2>Matching records</h2><div class="inline-actions"><button class="quiet-button" type="button" data-feature-action="region-export-csv">Export CSV</button><button class="quiet-button" type="button" data-feature-action="region-export-hyb">Export HYB</button><button class="quiet-button" type="button" data-feature-action="region-export-params">Export parameters</button></div></div>',
      '<div class="data-table-wrap"><table class="data-table"><thead><tr><th>Sequence ID</th><th>RNA 1</th><th>Region 1</th><th>RNA 2</th><th>Region 2</th><th>Chimera type</th><th>Support</th></tr></thead><tbody>',
      records.slice(0, limit).map(function (record) {
        return "<tr><td>" + escape(record.id) + "</td><td>" + escape(record.rnaOne) + "</td><td>" + range(record.rnaOneStart, record.rnaOneEnd) + "</td><td>" + escape(record.rnaTwo) + "</td><td>" + range(record.rnaTwoStart, record.rnaTwoEnd) + "</td><td>" + escape(record.chimeraType) + "</td><td>" + format(record.supportCount) + "</td></tr>";
      }).join(""),
      "</tbody></table></div>",
      records.length > 20 ? '<div class="table-more"><button class="quiet-button" type="button" data-feature-action="region-toggle-records">' + (showAll ? "Show fewer records" : "Show " + format(Math.min(records.length, 100)) + " records") + '</button>' + (records.length > 100 && showAll ? '<span class="table-note">Showing the first 100 of ' + format(records.length) + " matching records. Export includes all local matches.</span>" : '<span class="table-note">Showing ' + format(Math.min(limit, records.length)) + " of " + format(records.length) + ". Export includes all local matches.</span>") + "</div>" : "",
      "</section>"
    ].join("");
  }

  function renderEmptyRegion() {
    return '<section class="placeholder-card"><div class="placeholder-inner"><span class="placeholder-label">Region Explorer</span><h2>Choose an RNA region to start.</h2><p>All calculations use only the records opened in this browser session.</p></div></section>';
  }

  function renderStructure(state) {
    const structure = state.structure;
    const rnas = state.summary.rnaNames || [];
    const sequenceInfo = getStructureSequence(state);
    const cplfold = structure.engine === "cplfold";
    const cplfoldAllowsPseudoknot = structure.cplfoldAllowPseudoknot !== false;
    const cplfoldGuided = cplfold && structure.cplfoldEvidence === "hyb-blocks";
    const sourceOptions = cplfoldGuided ? [
      { value: "reference", label: "Reference region" }
    ] : [
      { value: "reference", label: "Reference region" },
      { value: "record", label: "HYB record sequence" },
      { value: "paste", label: "Paste sequence" }
    ];
    const engineOptions = [
      { value: "viennarna", label: "ViennaRNA · nested MFE" },
      { value: "cplfold", label: "CPLfold · energy-guided folding" }
    ];
    const workflowOptions = [
      { value: "custom", label: "Custom engine and reference layout" },
      { value: "short-intramolecular", label: "Short-ranged intramolecular (<500 nt)" },
      { value: "long-intramolecular", label: "Long-ranged intramolecular" },
      { value: "intermolecular", label: "Intermolecular" },
      { value: "homodimer", label: "Homodimer (overlap ≥ 5)" }
    ];
    const constraintModeOptions = [
      { value: "none", label: "Plain MFE" },
      { value: "hyb-guided", label: "COMRADES(ViennaRNA)" },
      { value: "manual-hard-base-pairs", label: "Manual hard base pairs (expert)" }
    ];
    const manualConstraintMode = !cplfold && structure.constraintMode === "manual-hard-base-pairs";
    const guidedConstraintMode = !cplfold && structure.constraintMode === "hyb-guided";
    const selectableRecords = ((state.view && state.view.structureRecords) || []).slice(0, 500);
    const selectedStructureRecord = findRecord(state, structure.selectedRecordIndex);
    if (selectedStructureRecord && !selectableRecords.some(function (record) { return record.index === selectedStructureRecord.index; })) {
      selectableRecords.push(selectedStructureRecord);
    }
    const length = sequenceInfo.sequence ? sequenceInfo.sequence.length : 0;
    const lengthNotice = structureLengthNotice(length, structure.allowLarge, cplfold ? "cplfold" : "viennarna", structure);
    const running = structure.status === "running";
    const largeEnsembleNeedsConsent = guidedConstraintMode && Number(structure.randomFoldCount) > 100 && !structure.allowLargeEnsemble;
    const cplfoldLengthUnavailable = cplfold && cplfoldCannotRun(length, structure);
    const unavailable = !sequenceInfo.sequence || !!sequenceInfo.error ||
      (cplfold ? cplfoldLengthUnavailable : (length > 3000 || (length > 2000 && !structure.allowLarge))) ||
      largeEnsembleNeedsConsent;
    // Why the Predict button is disabled, said next to it.
    const unavailableReason = !unavailable ? "" :
      !sequenceInfo.sequence ? "Choose an RNA and region, a HYB record or paste a sequence first." :
      sequenceInfo.error ? sequenceInfo.error :
      cplfold && cplfoldLengthUnavailable ? (lengthNotice || "This sequence is too long for CPLfold in the browser; choose a shorter region.") :
      !cplfold && length > 3000 ? "The browser folds at most 3,000 nt with ViennaRNA; choose a shorter region." :
      !cplfold && length > 2000 && !structure.allowLarge ? "Tick the large-fold confirmation above to fold more than 2,000 nt." :
      largeEnsembleNeedsConsent ? "Confirm the large ensemble above (more than 100 random folds) to run it." : "";
    const methodBadge = cplfold
      ? "CPLfold + Pyodide · Browser"
      : guidedConstraintMode
      ? "RNAcofold + constrained RNAfold · Browser"
      : (manualConstraintMode ? "ViennaRNA hard pairs · Browser" : "ViennaRNA MFE · Browser");
    const actionLabel = cplfold
      ? "Predict CPLfold candidates"
      : guidedConstraintMode
      ? "Run HYB-guided ensemble"
      : (manualConstraintMode ? "Predict constrained MFE" : "Predict MFE structure");

    return [
      '<div class="page-heading"><div><h1>RNA Structure</h1><p>Run ViennaRNA MFE workflows or pure-Python CPLfold prediction from a sequence and optional HYB-derived evidence, entirely in this browser. Choose a folding workflow and structure topology below.</p></div></div>',
      '<section class="structure-layout">',
      '<section class="structure-setup data-card">',
      '<div class="card-title-row"><h2>Prediction setup</h2><span class="method-badge">' + methodBadge + "</span></div>",
      renderSelectControl("Folding workflow", "workflow", structure.workflow || "custom", workflowOptions, "structure-control"),
      '<p class="card-note legacy-workflow-note">Presets select ViennaRNA with HYB-guided evidence. Short-range uses one region; long-range uses two regions of the same RNA. Intermolecular uses two different RNAs; homodimer uses two copies of one RNA and overlap score ≥ 5. Paired layouts include the original 100 nt spacer. Choose Custom for CPLfold or manual settings.</p>',
      renderSelectControl("Prediction engine", "engine", structure.engine || "viennarna", engineOptions, "structure-control"),
      renderSelectControl("Sequence source", "source", structure.source, sourceOptions, "structure-control"),
      structure.source === "reference" ? [
        renderSelectControl(guidedConstraintMode ? "Region 1 RNA" : "RNA", "rna", structure.rna, rnas.map(function (rna) { return { value: rna, label: rna }; }), "structure-control"),
        '<div class="control-grid">' + renderTextControl("Start", "start", structure.start, "Start", "structure-control", "number", { min: 1, step: 1 }) + renderTextControl("End", "end", structure.end, "End", "structure-control", "number", { min: 1, step: 1 }) + "</div>"
      ].join("") : "",
      structure.source === "record" ? renderSelectControl("HYB record", "selectedRecordIndex", String(structure.selectedRecordIndex === null ? "" : structure.selectedRecordIndex), [{ value: "", label: "Choose a loaded record" }].concat(selectableRecords.map(function (record) { return { value: String(record.index), label: record.id + " · " + record.rnaOne + "–" + record.rnaTwo }; })), "structure-control") : "",
      structure.source === "record" ? '<div class="notice notice-warning"><strong>Hybrid-read sequence</strong> This is the chimeric sequence stored in the selected HYB record. It is not necessarily a complete reference RNA or genomic region.</div>' : "",
      structure.source === "paste" ? '<label class="control-field"><span>Paste RNA sequence</span><textarea class="sequence-textarea" rows="8" data-feature="structure-control" data-key="pastedSequence" placeholder="ACGU…">' + escape(structure.pastedSequence || "") + "</textarea></label>" : "",
      !cplfold ? '<div class="control-grid structure-thermo-grid"><label class="control-field"><span>Temperature</span><input type="number" min="0" max="100" step="0.1" value="' + attribute(structure.temperature) + '" data-feature="structure-control" data-key="temperature" aria-label="Folding temperature in degrees Celsius"><small>°C</small></label><label class="control-field"><span>Minimum hairpin loop</span><input type="number" min="0" max="12" value="' + attribute(structure.minimumLoop) + '" data-feature="structure-control" data-key="minimumLoop" aria-label="Minimum unpaired bases in a hairpin loop"><small>unpaired bases</small></label></div>' : "",
      !cplfold ? renderSelectControl("Folding mode", "constraintMode", structure.constraintMode || "none", constraintModeOptions, "structure-control") : "",
      guidedConstraintMode ? renderGuidedStructureSetup(structure, rnas, sequenceInfo) : "",
      cplfold ? renderCplfoldSetup(structure, sequenceInfo) : "",
      manualConstraintMode ? '<div class="structure-constraint-editor"><label class="control-field" for="structure-constraint-text"><span>Manual hard base pairs</span><textarea id="structure-constraint-text" class="sequence-textarea" rows="5" maxlength="20000" spellcheck="false" autocapitalize="off" autocomplete="off" data-feature="structure-control" data-key="constraintText" aria-describedby="structure-constraint-help" placeholder="4-18\n7-15">' + escape(structure.constraintText || "") + '</textarea><small id="structure-constraint-help">One 1-based i-j pair per line, relative to the prepared sequence shown here. Pairs must be canonical, non-crossing, use each nucleotide once, and satisfy the minimum loop size.</small></label></div>' : "",
      cplfold
        ? '<div class="fold-status fold-status-cplfold"><strong>Pure-Python CPLfold mode</strong><p>' + (cplfoldAllowsPseudoknot ? "LinearFold generates phase-1 candidates; a second constrained phase searches for crossing base pairs, and CPLfold/HotKnots energy models rank nested and pseudoknotted structures." : "LinearFold generates phase-1 secondary-structure candidates and skips the constrained Phase 2 pseudoknot search.") + ' The computation runs in a dedicated Pyodide worker.</p></div>'
        : guidedConstraintMode
        ? '<div class="fold-status fold-status-evidence"><strong>COMRADES(ViennaRNA)</strong><p>Each eligible HYB row is folded with RNAcofold. Base-pair frequencies are merged into ranked stems, fitted greedily as hard constraints, then optionally re-fitted in seeded random orders.</p></div>'
        : manualConstraintMode
        ? '<div class="fold-status fold-status-constraints"><strong>Manual hard-pair mode</strong><p>ViennaRNA will enforce the pairs you enter before calculating the MFE. HYB2 Web Lite does not infer these pairs from HYB or RNAcofold evidence.</p></div>'
        : '<div class="fold-status fold-status-mfe"><strong>Plain minimum-free-energy mode</strong><p>Runs the deployed ViennaRNA WebAssembly engine in a dedicated worker without base-pair constraints.</p></div>',
      !cplfold && length > 2000 && length <= 3000 ? '<label class="structure-large-consent"><input type="checkbox" data-feature="structure-control" data-key="allowLarge"' + (structure.allowLarge ? " checked" : "") + '><span>I understand that this large fold may consume substantial browser memory.</span></label>' : "",
      '<div class="structure-run-bar">',
      !running && unavailableReason ? '<p class="structure-run-reason" role="note">Cannot run yet: ' + escape(unavailableReason) + "</p>" : "",
      running ? renderStructureProgress(structure) : "",
      running ? '<button class="button button-secondary" type="button" data-feature-action="cancel-structure">' + (structure.operation === "capacity" ? "Cancel capacity test" : (structure.operation === "bonus-matrix" ? "Cancel input export" : "Cancel prediction")) + '</button>' : '<button class="button" type="button" data-feature-action="predict-structure"' + (unavailable ? " disabled" : "") + ">" + actionLabel + "</button>",
      "</div>",
      "</section>",
      '<section class="structure-result data-card">',
      '<div class="card-title-row"><div><h2>' + (structure.result ? "Structure result" : "Sequence preparation") + '</h2><span class="card-note">' + (structure.result && structure.result.label ? escape(structure.result.label) : (sequenceInfo.label ? escape(sequenceInfo.label) : "Choose a source")) + '</span></div><span class="card-kicker">' + format(structure.result && structure.result.sequence ? structure.result.sequence.length : length) + " nt</span></div>",
      sequenceInfo.error ? '<div class="notice notice-warning">' + escape(sequenceInfo.error) + "</div>" : "",
      lengthNotice ? '<div class="notice notice-warning">' + escape(lengthNotice) + "</div>" : "",
      structure.status === "error" ? '<div class="notice notice-error">' + escape(structure.message || (cplfold ? "Browser CPLfold could not finish the structure prediction." : "ViennaRNA WebAssembly could not finish the structure prediction.")) + "</div>" : "",
      structure.result ? renderStructureResult(structure.result, structure.selectedNucleotide, structure.viewMode) : renderStructurePreparation(sequenceInfo, structure),
      cplfold
        ? '<div class="method-limit"><strong>What the browser can fold</strong><span>CPLfold runs here without its fast compiled core, so this browser folds short regions only: up to 500 nt at most, and the capacity test above finds a safe length for this computer. With HYB evidence, each eligible record adds a bonus that pulls the bases of its two arms together. For longer RNAs, use HYB2 Desktop, or Download local inputs and run CPLfold on your own computer.</span><details class="method-limit-details"><summary>Technical details</summary><span>This is the vendored pure-Python CPLfold implementation running without Numba JIT. The browser capacity test measures a representative local run and estimates a session-specific recommendation; a 500 nt hard safety ceiling remains. HYB-guided mode converts each eligible row\'s two prepared intervals into the original IRIS-style Gaussian, symmetric, log1p bonus matrix. For longer or Numba-accelerated runs, use Download local inputs: it exports the prepared FASTA and, in HYB-guided mode, the same sparse bonus matrix as <code>cplfold-bonus-matrix.tsv</code>. Run <code>bin/cplfold --sequence-file cplfold-input.fasta --bonus-matrix-file cplfold-bonus-matrix.tsv --alpha 0.5</code> locally. Sequence-only mode requires only the FASTA. Public redistribution still requires resolution of the upstream CPLfold licence noted in the repository.</span></details></div>'
        : guidedConstraintMode
        ? '<div class="method-limit"><strong>Compatibility boundary</strong><span>This reproduces the ViennaRNA path after a HYB file: RNAcofold evidence, ranked F-stem constraints, iterative compatibility fitting, seeded randomised folds, COMRADES scoring, and evidence colouring. UNAFold remains an optional external CLI compatibility path, and pseudoknotted hard constraints are outside ViennaRNA dot-bracket output.</span></div>'
        : '<div class="method-limit"><strong>Scope</strong><span>Choose COMRADES(ViennaRNA) to derive constraints automatically from loaded HYB records and a mapped reference FASTA. Plain and manual modes remain available for independent sequence folding.</span></div>',
      "</section>",
      "</section>",
      window.Hyb2Evidence ? window.Hyb2Evidence.renderPanel(state) : ""
    ].join("");
  }

  function renderCplfoldSetup(structure, sequenceInfo) {
    const guided = structure.cplfoldEvidence === "hyb-blocks";
    const allowPseudoknot = structure.cplfoldAllowPseudoknot !== false;
    const assembly = sequenceInfo.assembly;
    return [
      '<section class="guided-fold-controls cplfold-controls" aria-label="CPLfold setup">',
      renderSelectControl("Experimental evidence", "cplfoldEvidence", structure.cplfoldEvidence || "hyb-blocks", [
        { value: "hyb-blocks", label: "HYB block bonus matrix" },
        { value: "none", label: "Sequence only" }
      ], "structure-control"),
      guided ? '<div class="guided-input-summary"><strong>' + format(assembly ? assembly.eligibleRecordCount : 0) + ' eligible HYB row' + (assembly && assembly.eligibleRecordCount === 1 ? "" : "s") + '</strong><span>one contribution per row · overlap_score and collapsed raw-read count are not weights · single reference region</span></div>' : "",
      renderCplfoldTopologyControl(allowPseudoknot),
      '<details class="structure-advanced" data-open-key="cplfold-advanced"' + openAttr("cplfold-advanced") + '>',
      '<summary>Advanced settings <span class="structure-advanced-values">' + escape(cplfoldAdvancedSummary(structure)) + '</span></summary>',
      '<p class="card-note">Defaults suit most RNAs. Evidence alpha sets how strongly HYB evidence pulls bases together (0 = energy only); beam and phase-1 candidates trade speed for a broader search; the energy window keeps candidates within that many kcal/mol of the best.</p>',
      '<div class="control-grid">',
      renderSelectControl("Beam size", "cplfoldBeam", String(structure.cplfoldBeam || "20"), [
        { value: "10", label: "10 · faster" },
        { value: "20", label: "20 · browser default" },
        { value: "50", label: "50 · slower" }
      ], "structure-control"),
      renderSelectControl("Phase-1 candidates", "cplfoldMaxPhase1", String(structure.cplfoldMaxPhase1 || "3"), [
        { value: "1", label: "1 · quickest" },
        { value: "3", label: "3 · browser default" },
        { value: "5", label: "5 · broader search" }
      ], "structure-control"),
      "</div>",
      '<div class="control-grid">' + renderTextControl("Energy window", "cplfoldEnergyDelta", structure.cplfoldEnergyDelta || "5", "5", "structure-control", "number", { min: 0, max: 50, step: 0.5 }) + renderSelectControl("Energy model", "cplfoldEnergyModel", structure.cplfoldEnergyModel || "DP09", [
        { value: "DP09", label: "DP09 · recommended" },
        { value: "DP03", label: "DP03" },
        { value: "CC06", label: "CC06" },
        { value: "CC09", label: "CC09" },
        { value: "RE", label: "Rivas–Eddy" }
      ], "structure-control") + "</div>",
      '<div class="control-grid">' + renderTextControl("Evidence alpha", "cplfoldAlpha", structure.cplfoldAlpha || "0.5", "0.5", "structure-control", "number", { min: 0, max: 1, step: 0.05 }) + renderTextControl("Pseudoknot beta", "cplfoldBeta", structure.cplfoldBeta || "0", "0", "structure-control", "number", { min: 0, max: 1, step: 0.05 }) + "</div>",
      '</details>',
      renderCplfoldCapacity(structure),
      renderCplfoldLocalCta(guided),
      '<p class="cplfold-runtime-note">CPLfold uses its bundled 37 °C Vienna-mode and pseudoknot energy tables; the ViennaRNA temperature control does not apply. First use loads about 15 MB of same-origin Pyodide and NumPy assets. The worker is released after each result to return its memory; later runs reinitialise from the browser cache. Results and candidates remain in this tab.</p>',
      "</section>"
    ].join("");
  }

  // "beam 20 · 3 candidates · ±5 kcal/mol · DP09 · alpha 0.5 · beta 0 · defaults"
  function cplfoldAdvancedSummary(structure) {
    const values = {
      beam: String(structure.cplfoldBeam || "20"), phase1: String(structure.cplfoldMaxPhase1 || "3"),
      delta: String(structure.cplfoldEnergyDelta || "5"), model: String(structure.cplfoldEnergyModel || "DP09"),
      alpha: String(structure.cplfoldAlpha || "0.5"), beta: String(structure.cplfoldBeta || "0")
    };
    const defaults = values.beam === "20" && values.phase1 === "3" && Number(values.delta) === 5 && values.model === "DP09" &&
      Number(values.alpha) === 0.5 && Number(values.beta) === 0;
    return "beam " + values.beam + " · " + values.phase1 + " candidates · ±" + values.delta + " kcal/mol · " + values.model +
      " · alpha " + values.alpha + " · beta " + values.beta + (defaults ? " · defaults" : " · changed");
  }

  function renderCplfoldTopologyControl(allowPseudoknot) {
    const options = [
      {
        value: "true",
        title: "Allow pseudoknots",
        description: "Run Phase 2 to search for crossing base pairs.",
        selected: allowPseudoknot
      },
      {
        value: "false",
        title: "Secondary structure only",
        description: "Run Phase 1 candidates and skip pseudoknot search.",
        selected: !allowPseudoknot
      }
    ];
    return '<fieldset class="cplfold-topology-control" aria-describedby="cplfold-topology-help"><legend>Structure topology</legend><div class="cplfold-topology-options" role="radiogroup" aria-label="CPLfold pseudoknot mode">' + options.map(function (option) {
      return '<label class="cplfold-topology-option' + (option.selected ? ' is-selected' : '') + '"><input type="radio" name="cplfold-topology" value="' + option.value + '" data-feature="structure-control" data-key="cplfoldAllowPseudoknot"' + (option.selected ? " checked" : "") + '><span><strong>' + option.title + '</strong><small>' + option.description + '</small></span></label>';
    }).join("") + '</div><p id="cplfold-topology-help" class="cplfold-topology-help">CPLfold is the browser engine that can search crossing pairs. ViennaRNA remains pseudoknot-free.</p></fieldset>';
  }

  function cplfoldLocalCommand(structure, guided) {
    if (window.Hyb2StructureUI && typeof window.Hyb2StructureUI.localCplfoldCommand === "function") {
      return window.Hyb2StructureUI.localCplfoldCommand(structure, guided);
    }

    const command = ["bin/cplfold", "--sequence-file", "cplfold-input.fasta"];
    if (guided) {
      command.push("--bonus-matrix-file", "cplfold-bonus-matrix.tsv");
    }
    command.push(
      "--beam", profileValue(structure && structure.cplfoldBeam, "20"),
      "--delta", profileValue(structure && structure.cplfoldEnergyDelta, "5"),
      "--max-phase1", profileValue(structure && structure.cplfoldMaxPhase1, "3"),
      "--max-phase2", "1",
      "--model", profileValue(structure && structure.cplfoldEnergyModel, "DP09").toUpperCase(),
      "--beta", profileValue(structure && structure.cplfoldBeta, "0")
    );
    if (guided) {
      command.push("--alpha", profileValue(structure && structure.cplfoldAlpha, "0.5"));
    }
    if (structure && structure.cplfoldAllowPseudoknot === false) {
      command.push("--no-pseudoknot");
    }
    return command.join(" ");
  }

  function formatCplfoldCommand(command) {
    const tokens = String(command || "").trim().split(/\s+/).filter(Boolean);
    if (!tokens.length) {
      return "";
    }

    const lines = [tokens.shift()];
    while (tokens.length) {
      const option = tokens.shift();
      const value = tokens.length && tokens[0].indexOf("-") !== 0 ? tokens.shift() : "";
      lines.push("  " + option + (value ? " " + value : ""));
    }
    return lines.join(" \\\n");
  }

  function renderCplfoldLocalCta(guided) {
    return [
      '<div class="cplfold-local-cta">',
      '<div><strong>Run longer sequences locally</strong><span>' + (guided ? "Download the prepared FASTA and bonus matrix, then run bin/cplfold on your own machine." : "Download the prepared FASTA, then run bin/cplfold on your own machine.") + '</span></div>',
      '<a class="button button-secondary" href="./local-cplfold.html" target="_blank" rel="noopener">Open local run guide</a>',
      "</div>"
    ].join("");
  }

  function renderCplfoldCapacity(structure) {
    if (cplfoldNativeAvailable()) {
      const desktop = window.Hyb2Desktop || {};
      const accelerated = !!(desktop.capabilities && desktop.capabilities.numbaJit === true);
      return '<div class="cplfold-capacity" aria-live="polite"><div class="cplfold-capacity-row"><div><span class="drawer-kicker">Desktop runtime</span><strong>Bundled NumPy/CPLfold' + (accelerated ? ' + Numba JIT' : '') + ' · up to ' + format(cplfoldNativeMaxLength()) + ' nt</strong><small>Using the installed desktop runtime. Browser capacity measurement is not required.</small></div></div><small class="cplfold-capacity-footnote">The desktop runtime accepts sequences up to ' + format(cplfoldNativeMaxLength()) + ' nt; longer runs are limited by available memory.' + (accelerated ? ' The first run may spend time compiling and caching Numba kernels.' : ' Numba acceleration is unavailable; NumPy mode remains active.') + '</small></div>';
    }
    const capacity = cplfoldCapacity(structure);
    const profileMatches = capacity.status === "ready" && capacity.profileKey === cplfoldProfileKey(structure);
    let headline = "Baseline " + format(capacity.baselineLength) + " nt";
    let detail = "Run a short local benchmark to estimate a longer limit for this browser.";
    if (capacity.status === "probing") {
      headline = "Measuring local capacity…";
      detail = "The Pyodide worker is running a representative " + format(capacity.probeLength || 75) + " nt CPLfold fold.";
    } else if (capacity.status === "ready" && profileMatches) {
      headline = "Recommended up to " + format(capacity.recommendedLength) + " nt";
      detail = "Estimated from a " + format(capacity.probeLength || 75) + " nt run in " + formatDuration(capacity.foldElapsedMs) + ". Retest after changing CPLfold settings.";
    } else if (capacity.status === "ready") {
      headline = "Retest required";
      detail = "CPLfold settings changed after the last capacity measurement.";
    } else if (capacity.status === "error") {
      headline = "Baseline " + format(capacity.baselineLength) + " nt";
      detail = capacity.message || "The capacity test could not finish; the conservative baseline remains available.";
    } else if (capacity.status === "cancelled") {
      detail = capacity.message || "The capacity test was cancelled; the conservative baseline remains available.";
    }
    const buttonLabel = capacity.status === "ready" ? "Measure again" : "Measure browser capacity";
    return [
      '<div class="cplfold-capacity" aria-live="polite">',
      '<div class="cplfold-capacity-row"><div><span class="drawer-kicker">Browser capacity</span><strong>' + escape(headline) + '</strong><small>' + escape(detail) + '</small></div>',
      '<button class="button button-secondary" type="button" data-feature-action="probe-cplfold-capacity"' + (structure.status === "running" ? " disabled" : "") + '>' + buttonLabel + '</button></div>',
      '<small class="cplfold-capacity-footnote">The recommendation is session-specific and conservative. It is an estimate, not a guarantee; the absolute browser ceiling is ' + format(capacity.hardCeiling) + ' nt.</small>',
      '</div>'
    ].join("");
  }

  function renderGuidedStructureSetup(structure, rnas, sequenceInfo) {
    const paired = structure.evidenceLayout === "paired";
    const assembly = sequenceInfo.assembly;
    const randomFoldCount = Number(structure.randomFoldCount) || 0;
    return [
      '<section class="guided-fold-controls" aria-label="HYB-guided folding setup">',
      renderSelectControl("Reference layout", "evidenceLayout", structure.evidenceLayout || "single", [
        { value: "single", label: "One reference region" },
        { value: "paired", label: "Two regions + original 100 nt spacer" }
      ], "structure-control"),
      paired ? [
        renderSelectControl("Region 2 RNA", "secondRna", structure.secondRna, rnas.map(function (rna) { return { value: rna, label: rna }; }), "structure-control"),
        '<div class="control-grid">' + renderTextControl("Region 2 start", "secondStart", structure.secondStart, "Start", "structure-control", "number", { min: 1, step: 1 }) + renderTextControl("Region 2 end", "secondEnd", structure.secondEnd, "End", "structure-control", "number", { min: 1, step: 1 }) + "</div>",
        '<label class="structure-check"><input type="checkbox" data-feature="structure-control" data-key="homodimerOnly"' + (structure.homodimerOnly ? " checked" : "") + '><span>Use only the original homodimer proxy (same RNA and HYB overlap score ≥ 5)</span></label>'
      ].join("") : "",
      '<div class="control-grid"><label class="control-field"><span>Ranked stem constraints</span><input type="number" min="1" max="75" step="1" value="' + attribute(structure.constraintLimit || "75") + '" data-feature="structure-control" data-key="constraintLimit"><small>Original default: 75</small></label>',
      renderSelectControl("Randomised folds", "randomFoldCount", String(structure.randomFoldCount || "0"), [
        { value: "0", label: "0 · ranked fold only" },
        { value: "10", label: "10 · quick ensemble" },
        { value: "100", label: "100 · extended ensemble" },
        { value: "1000", label: "1,000 · original cluster scale" }
      ], "structure-control") + "</div>",
      randomFoldCount > 0 ? '<label class="control-field"><span>Randomisation seed</span><input type="text" maxlength="128" value="' + attribute(structure.randomSeed || "HYB2-Web") + '" data-feature="structure-control" data-key="randomSeed"><small>Seeded Fisher–Yates ordering makes the browser ensemble reproducible.</small></label>' : "",
      randomFoldCount > 100 ? '<label class="structure-large-consent"><input type="checkbox" data-feature="structure-control" data-key="allowLargeEnsemble"' + (structure.allowLargeEnsemble ? " checked" : "") + '><span>I understand that 1,000 serial browser folds may take a long time; Cancel terminates the worker immediately.</span></label>' : "",
      assembly ? '<div class="guided-input-summary"><strong>' + format(assembly.eligibleRecordCount) + ' eligible HYB row' + (assembly.eligibleRecordCount === 1 ? "" : "s") + '</strong><span>from ' + format(assembly.inputRecordCount) + " loaded rows · " + format(assembly.skipped.antisense) + " antisense · " + format(assembly.skipped.outsideSelectedRegions) + " outside layout" + (assembly.homodimerOnly ? " · homodimer filter active" : "") + "</span></div>" : "",
      "</section>"
    ].join("");
  }

  function renderStructureProgress(structure) {
    const progress = Math.max(0, Math.min(100, Number(structure.progress) || 0));
    const cplfold = structure.engine === "cplfold";
    const capacity = structure.operation === "capacity";
    const bonusMatrixExport = structure.operation === "bonus-matrix";
    return [
      '<div class="structure-progress" role="status" aria-live="polite">',
      '<div><strong>' + (capacity ? "Measuring browser capacity" : (bonusMatrixExport ? "Preparing local inputs" : "Predicting locally")) + '</strong><span>' + escape(structure.message || (capacity ? "Preparing the local CPLfold capacity test…" : (bonusMatrixExport ? "Preparing the CPLfold bonus matrix…" : (cplfold ? "Preparing CPLfold calculation…" : "Preparing ViennaRNA MFE calculation…")))) + "</span></div>",
      '<div class="progress-bar" role="progressbar" aria-label="' + (capacity ? "CPLfold browser capacity test" : (bonusMatrixExport ? "CPLfold local input export" : (cplfold ? "CPLfold" : "ViennaRNA") + ' structure prediction')) + ' progress" aria-valuemin="0" aria-valuemax="100" aria-valuenow="' + progress + '"><span style="width:' + progress + '%"></span></div>',
      "</div>"
    ].join("");
  }

  function renderStructurePreparation(sequenceInfo, structure) {
    if (!sequenceInfo.sequence) {
      return '<div class="structure-placeholder"><span class="placeholder-label">Waiting for a sequence</span><p>Reference regions require a mapped FASTA. A HYB sequence can be selected from a record, or you can paste A, C, G and U directly.</p></div>';
    }

    const localCplfoldInput = structure && structure.engine === "cplfold"
      ? '<button class="button button-secondary" type="button" data-feature-action="download-local-cplfold-input" aria-label="Download local CPLfold inputs" title="Download prepared FASTA and HYB bonus matrix for local bin/cplfold">Download local inputs</button>'
      : "";
    return [
      sequenceInfo.assembly ? renderAssemblyPreview(sequenceInfo.assembly, sequenceInfo.evidenceKind) : "",
      '<code class="structure-sequence">' + escape(wrapSequence(sequenceInfo.sequence, 70)) + "</code>",
      '<div class="sequence-actions"><button class="button button-secondary" type="button" data-feature-action="copy-structure-sequence">Copy sequence</button><button class="button button-secondary" type="button" data-feature-action="structure-download-fasta">Download FASTA</button>' + localCplfoldInput + "</div>"
    ].join("");
  }

  function renderAssemblyPreview(assembly, evidenceKind) {
    const contribution = evidenceKind === "cplfold-hyb-blocks"
      ? " will contribute one interval-block observation to the CPLfold bonus matrix."
      : " will contribute one RNAcofold observation each.";
    return [
      '<section class="assembly-preview"><span class="drawer-kicker">Prepared reference layout</span><div class="assembly-segments">',
      (assembly.segments || []).map(function (segment) {
        return '<span><strong>' + escape(segment.rna) + "</strong> " + range(segment.referenceStart, segment.referenceEnd) + " → prepared " + range(segment.preparedStart, segment.preparedEnd) + "</span>";
      }).join(assembly.linker ? '<span class="assembly-linker">100 nt A/U spacer</span>' : ""),
      '</div><p>' + format(assembly.eligibleRecordCount) + ' eligible HYB row' + (assembly.eligibleRecordCount === 1 ? "" : "s") + contribution + "</p></section>"
    ].join("");
  }

  function renderStructureResult(result, selectedNucleotide, viewMode) {
    const pairs = result.pairs || [];
    const cplfold = result.engine === "CPLfold";
    const allowPseudoknot = resultAllowsPseudoknot(result);
    const cplfoldHasEvidence = cplfold && result.evidence && result.evidence.source === "hyb-block-intervals";
    const constraintCount = Math.max(0, Number(result.constraintCount) || 0);
    const manualConstrained = result.constraintMode === "hard-base-pairs" && constraintCount > 0;
    const guided = result.constraintMode === "hyb-guided";
    const requestedPairs = new Set((result.constraints || []).map(function (pair) {
      return pair.left + ":" + pair.right;
    }));
    const activeView = structureViewMode(viewMode);
    const inspectorInstruction = activeView === "folded"
      ? "Drag the molecule to adjust the layout; scroll or pinch to zoom and pan."
      : (result.sequence.length <= 700
        ? "Click a nucleotide marker to inspect it."
        : "Click along the sequence baseline, or focus it and use the arrow keys, to inspect a nucleotide.");
    return [
      '<section class="structure-result-summary" aria-label="' + (cplfold ? (allowPseudoknot ? "CPLfold pseudoknot candidate result" : "CPLfold pseudoknot-free result") : "ViennaRNA secondary-structure result") + '">',
      '<div class="structure-metric"><span>' + (cplfold ? "Energy" : "MFE") + '</span><strong>' + formatEnergy(result.energy) + '</strong><small>kcal/mol' + (cplfold ? " · " + escape(result.parameters && result.parameters.energyModel || "DP09") : "") + '</small></div>',
      '<div class="structure-metric"><span>Base pairs</span><strong>' + format(pairs.length) + "</strong><small>" + (cplfold ? escape(result.topology || "nested") + " topology" : "non-crossing pairs") + "</small></div>",
      '<div class="structure-metric"><span>Unpaired bases</span><strong>' + format(result.unpaired) + "</strong><small>of " + format(result.sequence.length) + " nt</small></div>",
      cplfold
        ? '<div class="structure-metric"><span>Candidate</span><strong>#' + format((Number(result.selectedCandidate) || 0) + 1) + '</strong><small>' + escape(result.structureType === "pseudoknot" && allowPseudoknot ? "phase 1 + crossing phase 2" : (allowPseudoknot ? "phase 1 nested" : "phase 1 secondary")) + '</small></div>'
        : '<div class="structure-metric"><span>Constraint mode</span><strong>' + (guided ? "HYB-guided" : (manualConstrained ? "Manual hard" : "None")) + "</strong><small>" + format(constraintCount) + " enforced pair" + (constraintCount === 1 ? "" : "s") + "</small></div>",
      guided ? '<div class="structure-metric"><span>COMRADES score</span><strong>' + format(result.comradesScore || 0) + '</strong><small>' + format(result.matchedEvidencePairs || 0) + " supported structure pairs</small></div>" : "",
      cplfold && Number(result.effectiveEnergy) !== Number(result.energy) ? '<div class="structure-metric"><span>Effective energy</span><strong>' + formatEnergy(result.effectiveEnergy) + '</strong><small>kcal/mol · beta-adjusted rank</small></div>' : "",
      '<div class="structure-metric"><span>Elapsed</span><strong>' + format(result.elapsedMs) + " ms</strong><small>" + (cplfold ? "total · runtime " + format(result.runtimeLoadMs) + " ms · fold " + format(result.foldElapsedMs) + " ms" : "local worker") + "</small></div>",
      "</section>",
      cplfold
        ? '<div class="structure-constraint-summary structure-cplfold-summary"><strong>' + escape(allowPseudoknot ? (result.topology === "pseudoknotted" ? "Pseudoknot candidate" : "Nested candidate") : "Pseudoknot search disabled") + '</strong><span>' + (allowPseudoknot ? "CPLfold ranked " + format((result.candidates || []).length) + ' unique candidate' + ((result.candidates || []).length === 1 ? "" : "s") + '. Parentheses show phase-1 pairs; square brackets show the crossing phase-2 layer.' : "CPLfold ranked " + format((result.candidates || []).length) + ' phase-1 secondary-structure candidate' + ((result.candidates || []).length === 1 ? "" : "s") + '. No crossing Phase 2 layer was generated.') + '</span></div>'
        : guided
        ? '<div class="structure-constraint-summary structure-evidence-summary"><strong>HYB-evidence-selected structure</strong><span>The selected ensemble member maximises the original nucleotide-summed COMRADES support score. Supported arcs are coloured by RNAcofold evidence; fitted hard-pair arcs remain thicker.</span></div>'
        : (manualConstrained ? '<div class="structure-constraint-summary"><strong>Manual hard-pair result</strong><span>ViennaRNA enforced ' + format(constraintCount) + " user-entered pair" + (constraintCount === 1 ? "" : "s") + ". These pairs were not generated from HYB interaction evidence.</span></div>" : ""),
      guided ? renderComradesResult(result) : (cplfold ? renderCplfoldResult(result) : ""),
      renderStructureDiagramPanel(result, activeView, cplfold, allowPseudoknot, guided, manualConstrained, inspectorInstruction),
      renderNucleotideInspector(result, selectedNucleotide),
      '<section class="structure-output-grid"><div><span class="drawer-kicker">Sequence</span><code class="structure-output-code">' + escape(wrapSequence(result.sequence, 64)) + '</code></div><div><span class="drawer-kicker">Dot-bracket</span><code class="structure-output-code">' + escape(wrapSequence(result.dotBracket, 64)) + "</code></div></section>",
      '<div class="sequence-actions"><button class="button button-secondary" type="button" data-feature-action="copy-structure-dot-bracket">Copy dot-bracket</button><button class="button button-secondary" type="button" data-feature-action="download-structure-dot-bracket">Download DBN</button><button class="button button-secondary" type="button" data-feature-action="download-structure-ct">Download CT</button><button class="button button-secondary" type="button" data-feature-action="download-structure-pairs">Download base pairs</button>' + (cplfold ? '<button class="button button-secondary" type="button" data-feature-action="download-local-cplfold-input" aria-label="Download local CPLfold inputs" title="Download prepared FASTA and HYB bonus matrix for local bin/cplfold">Download local inputs</button>' : "") + (guided ? '<button class="button button-secondary" type="button" data-feature-action="download-structure-evidence">Download evidence</button><button class="button button-secondary" type="button" data-feature-action="download-structure-constraints">Download constraints</button><button class="button button-secondary" type="button" data-feature-action="download-structure-ensemble">Download ensemble</button>' : "") + (cplfoldHasEvidence ? '<button class="button button-secondary" type="button" data-feature-action="download-cplfold-evidence">Download bonus matrix</button>' : "") + (cplfold ? '<button class="button button-secondary" type="button" data-feature-action="download-cplfold-candidates">Download candidates</button>' : "") + '<button class="button button-secondary" type="button" data-feature-action="download-structure-svg">Download SVG</button><button class="button button-secondary" type="button" data-feature-action="download-structure-png">Download PNG</button><button class="button button-secondary" type="button" data-feature-action="download-structure-report">Download report</button></div>',
      '<details class="base-pair-details"><summary>Base-pair list (' + format(pairs.length) + ")</summary>" + (pairs.length ? '<ol class="base-pair-list">' + pairs.map(function (pair) {
        const isHardConstraint = requestedPairs.has(pair.left + ":" + pair.right);
        const evidenceLabel = (guided || cplfold) && Number(pair.evidenceSupport) > 0 ? " · evidence " + formatEvidence(pair.evidenceSupport) : "";
        const pairLabel = cplfold ? escape(pair.layer || "primary") : (isHardConstraint ? "Hard pair" : "MFE pair");
        return '<li' + (isHardConstraint ? ' class="hard-constraint-pair"' : "") + "><span>" + format(pair.left) + " " + escape(pair.leftBase) + "</span><span>" + escape(pair.type) + "</span><span>" + format(pair.right) + " " + escape(pair.rightBase) + "</span><span>" + pairLabel + evidenceLabel + "</span></li>";
      }).join("") + "</ol>" : '<p class="empty-inline">The selected predictor did not place any base pairs.</p>') + "</details>"
    ].join("");
  }

  function structureViewMode(value) {
    return ["folded", "arc", "radial", "circular", "matrix"].indexOf(value) > -1 ? value : "folded";
  }

  function structureViewCopy(viewMode, cplfold, allowPseudoknot, guided, manualConstrained) {
    const descriptions = {
      folded: "The RNA backbone is laid out as a folded molecule: paired bases form stem rungs while unpaired bases open into hairpin and internal loops.",
      arc: cplfold
        ? (allowPseudoknot ? "Arc layers distinguish nested phase-1 pairs from crossing pseudoknot pairs; opacity reflects HYB bonus support when enabled." : "The arc diagram shows the pseudoknot-free Phase-1 candidate; opacity reflects HYB bonus support when enabled.")
        : (guided ? "Arc colour intensity shows aggregated RNAcofold evidence; fitted constraint arcs are thicker." : (manualConstrained ? "Manual hard-pair arcs are thicker and marked in the base-pair list; all other arcs minimise free energy around them." : "Each arc represents a ViennaRNA MFE base pair.")),
      radial: "Pairs are drawn from the sequence perimeter toward the centre so long-range contacts and nested depth can be compared at a glance.",
      circular: "The sequence runs around a circular backbone; chords make long-range and crossing contacts easy to scan.",
      matrix: "The symmetric contact matrix places each base pair at its two sequence coordinates; mirrored points preserve the full interaction map."
    };
    return descriptions[structureViewMode(viewMode)];
  }

  function renderStructureDiagramPanel(result, viewMode, cplfold, allowPseudoknot, guided, manualConstrained, inspectorInstruction) {
    const activeView = structureViewMode(viewMode);
    const titles = {
      folded: "Folded RNA structure",
      arc: "Arc diagram",
      radial: "Radial diagram",
      circular: "Circular contact view",
      matrix: "Base-pair matrix"
    };
    const labels = {
      folded: "Folded RNA secondary-structure diagram",
      arc: "RNA secondary-structure arc diagram",
      radial: "RNA secondary-structure radial diagram",
      circular: "RNA secondary-structure circular diagram",
      matrix: "RNA secondary-structure base-pair matrix"
    };
    const options = [
      ["folded", "Folded"],
      ["arc", "Arc"],
      ["radial", "Radial"],
      ["circular", "Circular"],
      ["matrix", "Matrix"]
    ];
    return '<section class="structure-diagram-panel"><div class="structure-diagram-heading"><div><h3>' + titles[activeView] + '</h3><p>' + structureViewCopy(activeView, cplfold, allowPseudoknot, guided, manualConstrained) + " " + inspectorInstruction + '</p></div><span class="method-badge">' + escape(result.algorithm || (cplfold ? "CPLfold" : "ViennaRNA MFE")) + '</span></div><div class="structure-view-toolbar"><span class="structure-view-label">View</span><div class="structure-view-options" role="group" aria-label="RNA structure visualization view">' + options.map(function (option) {
      return '<button class="structure-view-option' + (option[0] === activeView ? " is-active" : '') + '" type="button" data-feature-action="set-structure-view" data-view-mode="' + option[0] + '" aria-pressed="' + (option[0] === activeView ? "true" : "false") + '">' + option[1] + "</button>";
    }).join("") + '</div></div><div class="structure-diagram" data-structure-diagram data-view-mode="' + activeView + '" aria-label="' + labels[activeView] + '"></div></section>';
  }

  function renderCplfoldResult(result) {
    const candidates = result.candidates || [];
    const evidence = result.evidence || {};
    const maximumBonus = Number(evidence.maximumBonus) || 0;
    const allowPseudoknot = resultAllowsPseudoknot(result);
    return [
      '<section class="comrades-result cplfold-result" aria-label="CPLfold candidate search result">',
      '<div class="cplfold-mode-note"><strong>' + (allowPseudoknot ? "Pseudoknot search enabled" : "Pseudoknot search disabled") + '</strong><span>' + (allowPseudoknot ? "Phase 1 and the constrained Phase 2 are included in candidate ranking." : "Only Phase 1 secondary structures are ranked; beta is ignored and no crossing pairs are generated.") + '</span></div>',
      '<div class="comrades-stage-grid">',
      '<div><span>1 · HYB blocks</span><strong>' + countLabel(evidence.inputRecords, "row") + '</strong><small>' + (evidence.source === "hyb-block-intervals" ? format(evidence.uniqueBlocks) + " unique prepared interval blocks" : "sequence-only run") + '</small></div>',
      '<div><span>2 · Bonus matrix</span><strong>' + format(evidence.nonzeroUpperTriangleCells || 0) + ' upper-triangle cells</strong><small>maximum log1p bonus ' + formatEvidence(maximumBonus) + '</small></div>',
      '<div><span>3 · ' + (allowPseudoknot ? "Two-phase search" : "Secondary search") + '</span><strong>' + countLabel(candidates.length, "candidate") + '</strong><small>' + (allowPseudoknot ? "beam " + format(result.parameters && result.parameters.beamSize) + ' · ΔE ' + formatEvidence(result.parameters && result.parameters.energyDelta) : "Phase 2 skipped · beta ignored") + '</small></div>',
      '<div><span>4 · Selected topology</span><strong>' + escape(result.topology || "nested") + '</strong><small>' + (allowPseudoknot ? format(result.crossingPairs || 0) + ' base pairs participate in crossings' : "pseudoknot-free candidate") + '</small></div>',
      "</div>",
      evidence.source === "hyb-block-intervals" ? renderCplfoldBonusMap(result) : "",
      '<div class="candidate-table-intro"><strong>Select a candidate structure</strong><span>Click any row to update the RNA visualization and all result details below.</span></div>',
      '<div class="data-table-wrap"><table class="data-table compact-table cplfold-candidate-table"><thead><tr><th>Rank</th><th>Topology</th><th>Energy</th><th>Effective</th><th>Pairs</th><th>Dot-bracket</th><th>Status</th></tr></thead><tbody>',
      candidates.map(function (candidate, index) {
        const selected = index === Number(result.selectedCandidate || 0);
        return '<tr class="cplfold-candidate-row' + (selected ? ' is-selected' : "") + '" data-feature-action="select-cplfold-candidate" data-candidate-index="' + index + '" tabindex="0" role="button" aria-pressed="' + (selected ? "true" : "false") + '" aria-label="View CPLfold candidate ' + format(index + 1) + '"><td>#' + format(index + 1) + '</td><td><span class="candidate-type candidate-type-' + attribute(candidate.type) + '">' + escape(candidate.topology) + '</span></td><td>' + formatEnergy(candidate.energy) + '</td><td>' + formatEnergy(candidate.effectiveEnergy) + '</td><td>' + format((candidate.pairs || []).length) + '</td><td><code>' + escape(candidate.dotBracket) + '</code></td><td><span class="candidate-view-state">' + (selected ? "Visualized" : "View structure") + '</span></td></tr>';
      }).join(""),
      "</tbody></table></div>",
      "</section>"
    ].join("");
  }

  function renderCplfoldBonusMap(result) {
    const evidence = result.evidence || {};
    const entries = evidence.bonusEntries || [];
    const length = Math.max(1, result.sequence.length);
    const maximum = Math.max(1e-12, Number(evidence.maximumBonus) || 0);
    const size = 220;
    const inset = 22;
    const span = size - inset - 8;
    const cell = Math.max(1.25, span / length);
    const marks = entries.map(function (entry) {
      const opacity = Math.max(0.08, Math.min(1, Number(entry.value) / maximum));
      const x = inset + ((Number(entry.one) - 1) / length) * span;
      const y = inset + ((Number(entry.two) - 1) / length) * span;
      return '<rect x="' + x.toFixed(2) + '" y="' + y.toFixed(2) + '" width="' + cell.toFixed(2) + '" height="' + cell.toFixed(2) + '" rx="0.7" fill="currentColor" opacity="' + opacity.toFixed(3) + '"><title>' + format(entry.one) + ' ↔ ' + format(entry.two) + ' · bonus ' + formatEvidence(entry.value) + "</title></rect>";
    }).join("");
    return '<div class="cplfold-bonus-visual"><div><span class="drawer-kicker">HYB-derived CPLfold bonus matrix</span><p>Upper triangle · prepared-sequence coordinates · colour intensity is log1p Gaussian block support.</p></div><svg viewBox="0 0 ' + size + " " + size + '" role="img" aria-label="CPLfold HYB bonus matrix"><line x1="' + inset + '" y1="' + inset + '" x2="' + (inset + span) + '" y2="' + (inset + span) + '" stroke="currentColor" opacity="0.18"/><g>' + marks + '</g><text x="' + inset + '" y="14">1</text><text x="' + (inset + span) + '" y="14" text-anchor="end">' + length + '</text><text x="4" y="' + (inset + span) + '">' + length + "</text></svg></div>";
  }

  function renderComradesResult(result) {
    const evidence = result.evidence || {};
    const randomisation = result.randomisation || {};
    const constraints = result.acceptedStemConstraints || [];
    const structures = (randomisation.structures || []).slice().sort(function (left, right) {
      return right.comradesScore - left.comradesScore || left.energy - right.energy || left.run - right.run;
    });
    const maximumScore = structures.length ? Math.max(1, structures[0].comradesScore) : 1;
    return [
      '<section class="comrades-result" aria-label="HYB-guided evidence workflow result">',
      '<div class="comrades-stage-grid">',
      '<div><span>1 · RNAcofold evidence</span><strong>' + countLabel(evidence.uniqueBasePairs, "unique pair") + '</strong><small>' + countLabel(evidence.inputRecords, "eligible HYB row") + "; one observation per row</small></div>",
      '<div><span>2 · Ranked stems</span><strong>' + countLabel((evidence.rankedStems || []).length, "stem") + '</strong><small>Top ' + format(evidence.requestedStemConstraints || 0) + " sent to compatibility fitting</small></div>",
      '<div><span>3 · Accepted constraints</span><strong>' + countLabel(constraints.length, "stem") + '</strong><small>' + countLabel(result.constraintCount, "enforced base pair") + "</small></div>",
      '<div><span>4 · Structure ensemble</span><strong>' + countLabel((randomisation.completedFolds || 0) + 1, "structure") + '</strong><small>ranked fold + ' + countLabel(randomisation.completedFolds, "seeded random order") + "</small></div>",
      "</div>",
      structures.length ? '<div class="ensemble-bars" aria-label="Top COMRADES structure scores">' + structures.slice(0, 12).map(function (entry, index) {
        const width = Math.max(2, (entry.comradesScore / maximumScore) * 100);
        return '<div class="ensemble-bar-row"' + (entry.run === randomisation.selectedRun ? ' data-selected="true"' : "") + '><span>#' + (index + 1) + " · " + (entry.randomised ? "random " + entry.run : "ranked") + '</span><span class="ensemble-bar-track"><span style="width:' + width.toFixed(2) + '%"></span></span><strong>' + format(entry.comradesScore) + "</strong></div>";
      }).join("") + "</div>" : "",
      '<details class="base-pair-details"><summary>Accepted stem constraints (' + format(constraints.length) + ")</summary>" + (constraints.length ? '<div class="data-table-wrap"><table class="data-table compact-table"><thead><tr><th>Rank</th><th>F constraint</th><th>Length</th><th>Evidence support</th></tr></thead><tbody>' + constraints.slice(0, 75).map(function (constraint) {
        return "<tr><td>" + format(constraint.rank) + "</td><td><code>F " + format(constraint.left) + " " + format(constraint.right) + " " + format(constraint.length) + "</code></td><td>" + format(constraint.length) + "</td><td>" + format(constraint.support) + "</td></tr>";
      }).join("") + "</tbody></table></div>" : '<p class="empty-inline">No evidence stem was compatible with this sequence.</p>') + "</details>",
      '<details class="base-pair-details"><summary>Top RNAcofold evidence pairs (' + format((evidence.selectedBasePairs || []).length) + ")</summary>" + ((evidence.selectedBasePairs || []).length ? '<div class="data-table-wrap"><table class="data-table compact-table"><thead><tr><th>Prepared left</th><th>Prepared right</th><th>HYB-row observations</th></tr></thead><tbody>' + evidence.selectedBasePairs.slice(0, 100).map(function (pair) {
        return "<tr><td>" + format(pair.one) + "</td><td>" + format(pair.two) + "</td><td>" + format(pair.count) + "</td></tr>";
      }).join("") + "</tbody></table></div>" : '<p class="empty-inline">No RNAcofold evidence pairs were retained.</p>') + "</details>",
      "</section>"
    ].join("");
  }

  function renderNucleotideInspector(result, selectedNucleotide) {
    const cplfold = result.engine === "CPLfold";
    const position = Number(selectedNucleotide);
    if (!Number.isInteger(position) || position < 1 || position > result.sequence.length) {
      return '<section class="structure-nucleotide-inspector"><span class="drawer-kicker">Nucleotide inspector</span><p>' + (result.sequence.length <= 700 ? "Click a marker" : "Click or use the arrow keys along the sequence baseline") + ' to inspect a base and its ' + (cplfold ? "selected-candidate" : "MFE") + ' pairing partner.</p></section>';
    }
    const pair = (result.pairs || []).find(function (entry) { return entry.left === position || entry.right === position; });
    const base = result.sequence.charAt(position - 1);
    if (!pair) {
      return '<section class="structure-nucleotide-inspector"><span class="drawer-kicker">Nucleotide ' + format(position) + '</span><strong>' + escape(base) + '</strong><p>Unpaired in this ' + (cplfold ? "CPLfold candidate" : "ViennaRNA MFE structure") + '.</p></section>';
    }
    const partner = pair.left === position ? pair.right : pair.left;
    const partnerBase = pair.left === position ? pair.rightBase : pair.leftBase;
    return '<section class="structure-nucleotide-inspector"><span class="drawer-kicker">Nucleotide ' + format(position) + '</span><strong>' + escape(base) + '</strong><p>Paired with ' + format(partner) + " " + escape(partnerBase) + " · " + escape(pair.type) + (cplfold ? " · " + escape(pair.layer || "primary") + " layer" : "") + "</p></section>";
  }

  function getStructureSequence(state) {
    const structure = state.structure;
    const cplfoldGuided = structure.engine === "cplfold" && structure.cplfoldEvidence === "hyb-blocks";
    if (structure.status === "running" && structure.runningSequenceInfo) {
      return structure.runningSequenceInfo;
    }
    if (structure.source === "reference") {
      if (!state.reference) {
        return { sequence: "", label: "", error: "Reference FASTA required for a reference-region sequence." };
      }
      // Reference sequences (DataProvider.getSequence) and, for HYB-guided
      // modes, every row's arms (DataProvider.getArms) are prefetched by
      // page-data.js into state.view.structureEvidence.
      const guidedEvidence = (structure.engine !== "cplfold" && structure.constraintMode === "hyb-guided") || cplfoldGuided;
      const evidence = state.view && state.view.structureEvidence;
      const neededRnas = [structure.rna].concat(!cplfoldGuided && structure.evidenceLayout === "paired" ? [structure.secondRna] : []);
      if (!evidence || evidence.guided !== guidedEvidence || neededRnas.some(function (rna) { return rna && (evidence.rnas || []).indexOf(rna) === -1; })) {
        return { sequence: "", label: "", error: "Loading the reference sequence and HYB evidence…", loading: true };
      }
      if (evidence.error) {
        return { sequence: "", label: "", error: evidence.error };
      }
      const referenceFasta = evidence.fasta || { sequences: [], mapping: {} };
      if (guidedEvidence) {
        if (!window.Hyb2Comrades) {
          return { sequence: "", label: "", error: "The HYB-guided evidence module is unavailable." };
        }
        try {
          if (structure.workflow === "intermolecular" && structure.rna === structure.secondRna) {
            throw new Error("Intermolecular folding requires two different RNAs. Choose a different Region 2 RNA.");
          }
          const regions = [{ rna: structure.rna, start: structure.start, end: structure.end }];
          if (!cplfoldGuided && structure.evidenceLayout === "paired") {
            regions.push({ rna: structure.secondRna, start: structure.secondStart, end: structure.secondEnd });
          }
          const assembly = window.Hyb2Comrades.prepareReferenceAssembly(evidence.records || [], referenceFasta, {
            regions: regions,
            homodimerOnly: !cplfoldGuided && structure.evidenceLayout === "paired" && !!structure.homodimerOnly
          });
          return {
            sequence: assembly.sequence,
            label: assembly.label,
            sourceRna: assembly.segments[0].rna,
            sourceStart: assembly.segments[0].referenceStart,
            sourceEnd: assembly.segments[0].referenceEnd,
            sourceSegments: assembly.segments,
            assembly: assembly,
            evidenceKind: cplfoldGuided ? "cplfold-hyb-blocks" : "vienna-rna-cofold",
            error: assembly.evidenceArms.length
              ? ""
              : "No forward-strand HYB rows are fully contained in the selected region layout."
          };
        } catch (error) {
          return { sequence: "", label: "", error: error && error.message ? error.message : (cplfoldGuided ? "The CPLfold HYB reference region is invalid." : "The HYB-guided reference layout is invalid.") };
        }
      }
      const reference = window.Hyb2Data.extractReference(referenceFasta, structure.rna, structure.start, structure.end);
      if (!reference) {
        return { sequence: "", label: "", error: "Choose a mapped RNA and valid reference coordinates." };
      }
      const normalised = window.Hyb2Data.normaliseFoldSequence(reference.sequence);
      return {
        sequence: normalised.sequence,
        label: reference.id + ":" + range(reference.start, reference.end),
        sourceRna: structure.rna,
        sourceStart: reference.start,
        sourceEnd: reference.end,
        error: normalised.invalid.length ? "Unsupported characters: " + normalised.invalid.join(", ") : ""
      };
    }

    if (structure.source === "record") {
      const record = findRecord(state, structure.selectedRecordIndex);
      if (!record) {
        return { sequence: "", label: "", error: "Choose a HYB record sequence." };
      }
      const normalised = window.Hyb2Data.normaliseFoldSequence(record.sequence);
      return {
        sequence: normalised.sequence,
        label: record.id + " · HYB record sequence",
        error: normalised.invalid.length ? "Unsupported characters: " + normalised.invalid.join(", ") : ""
      };
    }

    const normalised = window.Hyb2Data.normaliseFoldSequence(structure.pastedSequence);
    return {
      sequence: normalised.sequence,
      label: normalised.sequence ? "Pasted sequence" : "",
      error: normalised.invalid.length ? "Only A, C, G and U are accepted. Unsupported characters: " + normalised.invalid.join(", ") : ""
    };
  }

  // Body of the Data drawer (app.js adds the title and the Start over
  // section): the loaded files first, then name matching and
  // reproducibility folded unless they need attention.
  function getFilesDialogBody(state) {
    const fasta = state.fasta;
    const buildCommit = window.HYB2_BUILD && window.HYB2_BUILD.commit;
    const rnaCount = (state.summary.rnaNames || []).length;
    const matchedCount = fasta ? Object.keys(fasta.mapping || {}).filter(function (key) { return fasta.mapping[key]; }).length : 0;
    const unmatched = fasta ? Math.max(0, rnaCount - matchedCount) : 0;
    const files = [
      '<div class="files-section"><span class="drawer-kicker">Interaction data</span><strong>' + escape(state.summary.fileName) + "</strong><span>" + formatBytes(state.summary.fileSize) + " · " + format(state.summary.validRecords) + ' valid records</span><div class="inline-actions"><button class="quiet-button" type="button" data-feature-action="replace-hyb">Replace</button><button class="quiet-button" type="button" data-feature-action="clear-hyb">Remove</button></div></div>',
      '<div class="files-section"><span class="drawer-kicker">Reference sequences</span>',
      fasta ? '<strong>' + escape(fasta.fileName) + "</strong><span>" + format(fasta.sequences.length) + (fasta.sequences.length === 1 ? " sequence" : " sequences") + "</span>" + (fasta.unsupportedSequenceEntries ? '<span class="notice notice-warning">' + format(fasta.unsupportedSequenceEntries) + " FASTA record" + (fasta.unsupportedSequenceEntries === 1 ? " contains" : "s contain") + " unsupported symbol" + ((fasta.unsupportedCharacters || []).length === 1 ? "" : "s") + " (" + escape((fasta.unsupportedCharacters || []).join(", ")) + "). Symbols were preserved so coordinates did not shift; affected regions cannot be folded.</span>" : "") : "<strong>No reference loaded</strong><span>Optional. Add a FASTA to see sequences, extract regions and predict structures.</span>",
      '<div class="inline-actions"><button class="quiet-button" type="button" data-feature-action="choose-fasta">' + (fasta ? "Replace" : "Add reference FASTA") + "</button>" + (fasta ? '<button class="quiet-button" type="button" data-feature-action="remove-fasta">Remove</button>' : "") + "</div></div>",
      renderCompareFiles(state)
    ].join("");
    const parts = ['<section class="drawer-section"><h3>Loaded files</h3>' + files + "</section>"];
    if (fasta) {
      parts.push(drawerFold("data-mapping", "Name matching",
        unmatched ? format(unmatched) + " of " + format(rnaCount) + " RNA names have no reference sequence" : "All " + format(rnaCount) + (rnaCount === 1 ? " RNA name matches" : " RNA names match") + " a reference sequence",
        unmatched > 0, renderFastaMapping(state), unmatched > 0));
    }
    parts.push(drawerFold("data-reproducibility", "Reproducibility", "Versions and file fingerprints for your methods section", false,
      '<div class="files-section"><span>App version: <code class="mono">0.5.0</code></span><span>Git commit: <code class="mono hash-value">' + escape(buildCommit || "Local development build") + '</code></span><span>HYB SHA-256: <code class="mono hash-value">' + escape(state.summary.sha256 || (state.summary.sha256Unavailable ? "Unavailable" : "Calculating locally…")) + "</code></span>" + (fasta ? '<span>FASTA SHA-256: <code class="mono hash-value">' + escape(fasta.sha256 || (fasta.sha256Unavailable ? "Unavailable" : "Calculating locally…")) + "</code></span>" : "") + "</div>"));
    return parts.join("");
  }

  // A folded drawer section; `fallbackOpen` applies until the user toggles it.
  function drawerFold(key, title, summary, fallbackOpen, body, attention) {
    const remembered = window.Hyb2OpenSections && key in window.Hyb2OpenSections ? window.Hyb2OpenSections[key] : fallbackOpen;
    return '<details class="drawer-section drawer-fold' + (attention ? " needs-attention" : "") + '" data-open-key="' + key + '"' + (remembered ? " open" : "") + '><summary><span class="drawer-fold-title">' + escape(title) + '</span><span class="drawer-fold-summary">' + escape(summary) + "</span></summary>" + body + "</details>";
  }

  // The other Compare datasets and the arc map files (the example loads
  // three replicates, two folds and a gene track besides the primary file).
  function renderCompareFiles(state) {
    const comparison = state.comparison || {};
    const datasets = comparison.datasets || [];
    const others = datasets.filter(function (dataset) { return dataset.id !== "primary"; });
    const primary = datasets.filter(function (dataset) { return dataset.id === "primary"; })[0];
    const arc = state.arcMap || {};
    const arcFiles = [["Structure A", arc.a], ["Structure B", arc.b], ["Gene track", arc.annotation]].filter(function (item) { return item[1] && item[1].name; });
    if (!others.length && !arcFiles.length) {
      return "";
    }
    const conditionLabel = function (condition) {
      const label = condition === "B" ? comparison.conditionBLabel : comparison.conditionALabel;
      return "condition " + (condition === "B" ? "B" : "A") + (label ? " (" + label + ")" : "");
    };
    const parts = [];
    if (others.length) {
      parts.push('<div class="files-section"><span class="drawer-kicker">Compare datasets</span>');
      others.forEach(function (dataset) {
        const summary = dataset.summary || {};
        parts.push("<strong>" + escape(dataset.fileName) + "</strong><span>" + escape((dataset.label ? dataset.label + " · " : "") + conditionLabel(dataset.condition)) + (Number.isFinite(summary.validRecords) ? " · " + format(summary.validRecords) + " valid records" : "") + "</span>");
      });
      parts.push("<span>" + (primary ? "The interaction data above is " + escape(conditionLabel(primary.condition)) + ". " : "") + "Add, relabel or reassign datasets on the Compare page.</span></div>");
    }
    if (arcFiles.length) {
      parts.push('<div class="files-section"><span class="drawer-kicker">Arc map files</span>');
      arcFiles.forEach(function (item) {
        parts.push("<strong>" + escape(item[1].name) + "</strong><span>" + escape(item[0]) + "</span>");
      });
      parts.push("</div>");
    }
    return parts.join("");
  }

  function renderFastaMapping(state) {
    const fasta = state.fasta;
    const rnas = state.summary.rnaNames || [];
    const editorRna = fasta.mappingEditorRna == null
      ? rnas.find(function (rna) { return !fasta.mapping[rna]; }) || rnas[0] || ""
      : fasta.mappingEditorRna;
    if (fasta.mappingEditorRna == null) {
      fasta.mappingEditorRna = editorRna;
      fasta.mappingEditorReference = fasta.mapping[editorRna] || "";
    }
    const editorReference = fasta.mappingEditorReference == null
      ? fasta.mapping[editorRna] || ""
      : fasta.mappingEditorReference;
    const preview = rnas.slice(0, 100);
    return [
      '<div class="fasta-mapping">',
      '<p class="card-note">Each RNA name in the HYB file is paired with the reference sequence of the same name. To fix one, pick the RNA, type the reference ID it should use, and apply.</p>',
      '<div class="control-grid"><label class="control-field"><span>HYB RNA</span><input type="search" list="fasta-rna-suggestions" value="' + attribute(editorRna) + '" data-feature="fasta-mapping-editor" data-key="mappingEditorRna"><datalist id="fasta-rna-suggestions">' + rnas.slice(0, 500).map(function (rna) { return '<option value="' + attribute(rna) + '"></option>'; }).join("") + '</datalist></label>',
      '<label class="control-field"><span>FASTA reference ID</span><input type="search" list="fasta-reference-suggestions" value="' + attribute(editorReference) + '" data-feature="fasta-mapping-editor" data-key="mappingEditorReference"><datalist id="fasta-reference-suggestions">' + fasta.sequences.slice(0, 500).map(function (entry) { return '<option value="' + attribute(entry.id) + '"></option>'; }).join("") + '</datalist></label></div>',
      '<div class="inline-actions"><button class="button button-secondary" type="button" data-feature-action="apply-fasta-mapping">Apply mapping</button><button class="quiet-button" type="button" data-feature-action="remove-current-fasta-mapping">Mark as not matched</button><button class="quiet-button" type="button" data-feature-action="export-fasta-mapping">Export matches</button></div>',
      '<div class="mapping-rows">' + preview.map(function (rna) {
        const mapped = fasta.mapping[rna] || "Not matched";
        return '<div class="mapping-row"><span title="' + attribute(rna) + '">' + escape(rna) + '</span><span title="' + attribute(mapped) + '">' + escape(mapped) + '</span><button class="quiet-button" type="button" data-feature-action="edit-fasta-mapping" data-rna="' + attribute(rna) + '">Edit</button></div>';
      }).join("") + "</div>",
      rnas.length > preview.length ? '<p class="table-note">Showing the first ' + format(preview.length) + " of " + format(rnas.length) + " RNA mappings. Enter any exact RNA name above to edit it.</p>" : "",
      "</div>"
    ].join("");
  }

  // " open" when the section with this key was left open (see app.js).
  function openAttr(key) {
    return window.Hyb2OpenSections && window.Hyb2OpenSections[key] ? " open" : "";
  }

  function renderSelectControl(label, key, value, options, feature) {
    const available = options || [];
    if (available.length > 750) {
      const selected = available.find(function (option) {
        return String(option.value) === String(value);
      });
      const suggestions = available.slice(0, 750);
      if (selected && !suggestions.some(function (option) { return String(option.value) === String(selected.value); })) {
        suggestions.push(selected);
      }
      const listId = "options-" + String(feature + "-" + key).replace(/[^a-z0-9_-]/gi, "-");
      return [
        '<label class="control-field"><span>' + escape(label) + "</span>",
        '<input type="search" list="' + attribute(listId) + '" value="' + attribute(value) + '" data-feature="' + attribute(feature) + '" data-key="' + attribute(key) + '" autocomplete="off">',
        '<datalist id="' + attribute(listId) + '">' + suggestions.map(function (option) {
          return '<option value="' + attribute(option.value) + '">' + escape(option.label) + "</option>";
        }).join("") + "</datalist>",
        '<small>Type an exact value. Showing ' + format(suggestions.length) + " of " + format(available.length) + " suggestions.</small></label>"
      ].join("");
    }
    return [
      '<label class="control-field"><span>' + escape(label) + "</span>",
      '<select data-feature="' + attribute(feature) + '" data-key="' + attribute(key) + '">',
      available.map(function (option) {
        return '<option value="' + attribute(option.value) + '"' + (String(option.value) === String(value) ? " selected" : "") + ">" + escape(option.label) + "</option>";
      }).join(""),
      "</select></label>"
    ].join("");
  }

  function renderTextControl(label, key, value, placeholder, feature, type, options) {
    const settings = options || {};
    const numericAttributes = (settings.min === undefined ? "" : ' min="' + attribute(settings.min) + '"') +
      (settings.max === undefined ? "" : ' max="' + attribute(settings.max) + '"') +
      (settings.step === undefined ? "" : ' step="' + attribute(settings.step) + '"');
    return [
      '<label class="control-field"><span>' + escape(label) + "</span>",
      '<input type="' + (type || "text") + '" value="' + attribute(value) + '" placeholder="' + attribute(placeholder) + '" data-feature="' + attribute(feature) + '" data-key="' + attribute(key) + '"' + numericAttributes + '>',
      "</label>"
    ].join("");
  }

  function partnerOptions(rnas, selectedRna) {
    const list = [{ value: "", label: "All partners" }];
    rnas.forEach(function (rna) {
      if (!selectedRna || rna !== selectedRna || true) {
        list.push({ value: rna, label: rna });
      }
    });
    return list;
  }

  // Looks a row up among the rows the page-data layer has already fetched
  // (open record, interaction window, structure choices, report sample).
  function findRecord(state, index) {
    if (index === null || index === undefined || index === "") {
      return null;
    }
    const target = String(index);
    const loaded = state && state.view || {};
    const singles = [loaded.selectedRecord, loaded.structureSelectedRecord];
    for (let item = 0; item < singles.length; item += 1) {
      if (singles[item] && singles[item].record && String(singles[item].record.index) === target) {
        return singles[item].record;
      }
    }
    const lists = [loaded.interactions && loaded.interactions.rows, loaded.structureRecords, loaded.report && loaded.report.filteredSample];
    for (let list = 0; list < lists.length; list += 1) {
      const match = Array.isArray(lists[list]) ? lists[list].find(function (record) { return String(record.index) === target; }) : null;
      if (match) {
        return match;
      }
    }
    return null;
  }

  function hasMappedReference(state, rna) {
    const reference = state && state.reference;
    return !!(reference && reference.mapped && rna && reference.mapped[rna]);
  }

  function regionReference(state) {
    const reference = state.view && state.view.regionReference;
    const region = state.region || {};
    if (!reference || !reference.params) {
      return null;
    }
    const params = reference.params;
    return params.rna === region.rna && String(params.start) === String(region.start) && String(params.end) === String(region.end)
      ? reference
      : null;
  }

  function cplfoldCapacity(structure) {
    if (cplfoldNativeAvailable()) {
      return {
        status: "native",
        mode: "desktop-native",
        baselineLength: 0,
        hardCeiling: cplfoldNativeMaxLength(),
        recommendedLength: cplfoldNativeMaxLength(),
        probeLength: 0,
        foldElapsedMs: null,
        profileKey: "desktop-native",
        message: "Using the desktop bundled NumPy/CPLfold runtime. Browser capacity measurement is not required."
      };
    }
    const raw = structure && structure.cplfoldCapacity && typeof structure.cplfoldCapacity === "object"
      ? structure.cplfoldCapacity
      : {};
    const baseline = Math.min(500, positiveInteger(raw.baselineLength, 75));
    const hardCeiling = Math.max(baseline, Math.min(500, positiveInteger(raw.hardCeiling, 500)));
    return {
      status: ["unknown", "probing", "ready", "error", "cancelled"].indexOf(raw.status) === -1 ? "unknown" : raw.status,
      baselineLength: baseline,
      hardCeiling: hardCeiling,
      recommendedLength: Math.max(baseline, Math.min(hardCeiling, positiveInteger(raw.recommendedLength, baseline))),
      probeLength: positiveInteger(raw.probeLength, baseline),
      foldElapsedMs: Number(raw.foldElapsedMs),
      profileKey: String(raw.profileKey || ""),
      message: String(raw.message || "")
    };
  }

  function positiveInteger(value, fallback) {
    const parsed = Number(value);
    return Number.isInteger(parsed) && parsed > 0 ? parsed : fallback;
  }

  function cplfoldNativeAvailable() {
    return !!(window.Hyb2Desktop && window.Hyb2Desktop.enabled && window.Hyb2Desktop.capabilities && window.Hyb2Desktop.capabilities.cplfoldNative === true);
  }

  // Native ceiling from the desktop runtime detector; 500 nt (the browser
  // ceiling) when the detector did not report one.
  function cplfoldNativeMaxLength() {
    const limits = window.Hyb2Desktop && window.Hyb2Desktop.limits;
    return positiveInteger(limits && limits.cplfoldMaxLength, 500);
  }

  function resultAllowsPseudoknot(result) {
    return !!result && result.allowPseudoknot !== false &&
      !(result.parameters && result.parameters.allowPseudoknot === false);
  }

  function cplfoldProfileKey(structure) {
    return [
      profileValue(structure && structure.cplfoldEvidence, "hyb-blocks"),
      profileValue(structure && structure.cplfoldBeam, "20"),
      profileValue(structure && structure.cplfoldMaxPhase1, "3"),
      profileValue(structure && structure.cplfoldEnergyDelta, "5"),
      profileValue(structure && structure.cplfoldEnergyModel, "DP09").toUpperCase(),
      profileValue(structure && structure.cplfoldAlpha, "0.5"),
      profileValue(structure && structure.cplfoldBeta, "0"),
      profileValue(structure && structure.cplfoldAllowPseudoknot, true) === "false" ? "nested" : "pseudoknot"
    ].join("|");
  }

  function profileValue(value, fallback) {
    return value === undefined || value === null || value === "" ? fallback : String(value);
  }

  function cplfoldCannotRun(length, structure) {
    if (cplfoldNativeAvailable()) {
      return length > cplfoldNativeMaxLength();
    }
    const capacity = cplfoldCapacity(structure);
    if (length > capacity.hardCeiling || length <= capacity.baselineLength) {
      return length > capacity.hardCeiling;
    }
    return capacity.status !== "ready" ||
      capacity.profileKey !== cplfoldProfileKey(structure) ||
      length > capacity.recommendedLength;
  }

  function formatDuration(value) {
    const milliseconds = Number(value);
    if (!Number.isFinite(milliseconds) || milliseconds < 0) {
      return "an unknown time";
    }
    if (milliseconds < 1000) {
      return Math.round(milliseconds) + " ms";
    }
    return (milliseconds / 1000).toFixed(1) + " s";
  }

  function structureLengthNotice(length, allowLarge, engine, structure) {
    if (engine === "cplfold") {
      if (cplfoldNativeAvailable()) {
        if (length > cplfoldNativeMaxLength()) {
          return "Desktop CPLfold is limited to " + format(cplfoldNativeMaxLength()) + " nt by the bundled runtime. Select a shorter region.";
        }
        return "Using the desktop bundled NumPy/CPLfold runtime (up to " + format(cplfoldNativeMaxLength()) + " nt); no browser capacity test is required.";
      }
      const capacity = cplfoldCapacity(structure);
      if (length > capacity.hardCeiling) {
        return "Browser CPLfold has a hard safety ceiling of " + format(capacity.hardCeiling) + " nt in this pure-Python Pyodide build. Select a shorter region or use bin/cplfold locally.";
      }
      if (length > capacity.baselineLength && capacity.status === "probing") {
        return "The browser CPLfold capacity test is running. Wait for it to finish before predicting this " + format(length) + " nt sequence.";
      }
      if (length > capacity.baselineLength && capacity.status !== "ready") {
        return "This " + format(length) + " nt sequence is above the browser baseline of " + format(capacity.baselineLength) + " nt. Run the local capacity test before predicting.";
      }
      if (length > capacity.baselineLength && capacity.profileKey !== cplfoldProfileKey(structure)) {
        return "CPLfold settings changed after the last browser capacity test. Measure capacity again before predicting this sequence.";
      }
      if (length > capacity.recommendedLength) {
        return "This browser capacity test recommends up to " + format(capacity.recommendedLength) + " nt for the current settings. Select a shorter region, reduce the search settings, or retest this browser.";
      }
      if (length > 50) {
        return "This " + format(length) + " nt CPLfold search may take tens of seconds in Pyodide. It runs in a worker and can be cancelled safely.";
      }
      return "";
    }
    if (length > 3000) {
      return "HYB2 Web Lite supports ViennaRNA browser folding up to 3,000 nt. Reduce this region before predicting.";
    }
    if (length > 2000 && !allowLarge) {
      return "This region contains " + format(length) + " nt. Browser folding can consume substantial memory; review and enable the large-region option to continue.";
    }
    if (length > 1000) {
      return "This " + format(length) + " nt sequence may take noticeably longer. ViennaRNA runs in a dedicated worker so the interface remains responsive.";
    }
    if (length > 500) {
      return "This " + format(length) + " nt sequence may take a moment to fold in the local ViennaRNA worker.";
    }
    return "";
  }

  function range(start, end) {
    return format(start) + "–" + format(end);
  }

  function nullableRange(start, end) {
    return start === null || end === null ? "—" : range(start, end);
  }

  function truncate(value, length) {
    const string = String(value || "");
    return string.length > length ? string.slice(0, length) + "…" : string;
  }

  function wrapSequence(sequence, length) {
    const pieces = [];
    for (let index = 0; index < sequence.length; index += length) {
      pieces.push(sequence.slice(index, index + length));
    }
    return pieces.join("\n");
  }

  function format(value) {
    return formatNumber.format(value || 0);
  }

  function countLabel(value, noun) {
    const count = Math.max(0, Number(value) || 0);
    return format(count) + " " + noun + (count === 1 ? "" : "s");
  }

  function formatEnergy(value) {
    return Number.isFinite(Number(value)) ? Number(value).toFixed(2) : "—";
  }

  function formatEvidence(value) {
    return Number.isFinite(Number(value)) ? Number(value).toFixed(3) : "—";
  }

  function formatBytes(bytes) {
    if (!bytes) {
      return "0 B";
    }
    const units = ["B", "KB", "MB", "GB"];
    const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    return formatNumber.format(bytes / Math.pow(1024, index)) + " " + units[index];
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

  window.Hyb2Pages = {
    renderInteractions: renderInteractions,
    renderContactMap: renderContactMap,
    renderViewpoint: renderViewpoint,
    renderRegion: renderRegion,
    renderComparison: renderComparison,
    renderComparisonDataTable: renderComparisonDataTable,
    renderStructure: renderStructure,
    renderStructureResult: renderStructureResult,
    renderVirtualRows: renderVirtualRows,
    getStructureSequence: getStructureSequence,
    cplfoldLocalCommand: cplfoldLocalCommand,
    getFilesDialogBody: getFilesDialogBody,
    findRecord: findRecord,
    hasMappedReference: hasMappedReference,
    regionReference: regionReference,
    interactionWindow: interactionWindow
  };
}());
