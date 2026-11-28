"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const repository = path.resolve(__dirname, "..", "..");
const context = vm.createContext({
  Intl,
  Number,
  String,
  Boolean,
  Object,
  Array,
  Math,
  Date,
  RegExp,
  Set,
  Map,
  JSON,
  window: {}
});

context.window.HYB2_BUILD = { commit: "report-test-commit" };
context.window.Hyb2Data = {
  getFilteredRecords: function (state) { return state.records.filter(function (record) { return !state.filters || !state.filters.rna || record.rnaOne === state.filters.rna || record.rnaTwo === state.filters.rna; }); },
  getPartnerCounts: function () { return [{ name: "RNA_B", records: 2, support: 5 }]; },
  buildContactMatrix: function () {
    return {
      ready: true,
      cells: [{ x: 10, y: 20, value: 3, records: 2, support: 5 }],
      cellMap: { "10:20": { x: 10, y: 20, value: 3, records: 2, support: 5 } },
      binSize: 10,
      xMin: 10,
      xMax: 19,
      yMin: 20,
      yMax: 29,
      recordsUsed: 2,
      totalSupport: 5,
      cellContributions: 2,
      maximumCells: 100000,
      maximumBinContributions: 2000000,
      max: 3,
      cap: 3
    };
  },
  getViewpointResults: function () {
    return {
      ready: true,
      rna: "RNA_A",
      start: 10,
      end: 30,
      binSize: 1,
      records: [{ id: "read_1" }],
      armContributions: 2,
      max: 2,
      totalCoverage: 20,
      bins: [{ start: 10, end: 10, value: 2 }, { start: 11, end: 11, value: 1 }]
    };
  },
  getRegionResults: function () {
    return {
      records: [{ id: "read_1", rnaOne: "RNA_A", rnaOneStart: 10, rnaOneEnd: 12, rnaTwo: "RNA_B", rnaTwoStart: 20, rnaTwoEnd: 22, supportCount: 3 }],
      support: 3,
      partners: [{ name: "RNA_B", records: 1, support: 3 }],
      profile: [{ start: 10, end: 12, value: 3 }],
      profileBinSize: 3
    };
  },
  buildComparisonResults: function () {
    const cell = { x: 10, y: 20, conditionA: 3, conditionB: 1, effect: 1.5, presentDatasets: 2, rawA: 3, rawB: 1, similarity: 1, datasets: [] };
    return {
      ready: true,
      groupA: [{ id: "primary" }],
      groupB: [{ id: "secondary" }],
      cells: [cell],
      cellMap: { "10:20": cell },
      conservedCells: [cell],
      binSize: 10,
      effectCap: 2,
      similarityCap: 1
    };
  },
  findFastaEntry: function () { return null; },
  extractReference: function () { return null; }
};
context.window.Hyb2UI = {
  contactParameters: function (state) { return { parameters: { rnaX: state.contact.rnaX, rnaY: state.contact.rnaY, binSize: 10 } }; },
  viewpointParameters: function () { return { parameters: { rna: "RNA_A" }, result: { matchingRecords: 1 } }; },
  regionParameters: function () { return { parameters: { rna: "RNA_A", overlapRule: "any" }, result: { matchingRecords: 1 } }; },
  comparisonParameters: function () { return { parameters: { rnaX: "RNA_A", rnaY: "RNA_B" }, result: { ready: true } }; }
};
context.window.Hyb2StructureUI = {
  structureReport: function (state, result) {
    return {
      source: { label: result.label, type: result.source },
      prediction: { topology: result.topology, structureType: result.structureType },
      sourceHyb: { fileName: state.summary.fileName }
    };
  }
};

vm.runInContext(fs.readFileSync(path.join(repository, "web", "support-kernels.js"), "utf8"), context, { filename: "support-kernels.js" });
vm.runInContext(fs.readFileSync(path.join(repository, "web", "results.js"), "utf8"), context, { filename: "results.js" });
vm.runInContext(fs.readFileSync(path.join(repository, "web", "report-page.js"), "utf8"), context, { filename: "report-page.js" });

const state = {
  summary: {
    fileName: "<bad>.hyb",
    fileSize: 1234,
    validRecords: 2,
    invalidRecords: 1,
    skippedBlankLines: 1,
    skippedCommentLines: 1,
    withDg: 2,
    overlapScoreRecords: 2,
    rawReadCountRecords: 1,
    homodimerRecords: 0,
    supportInteractions: 5,
    uniqueRNAs: 2,
    uniquePairs: 1,
    rnaNames: ["RNA_A", "RNA_B"],
    errors: [{ line: 4, error: "bad row", content: "<invalid>" }]
  },
  records: [
    { id: "read_1", rnaOne: "RNA_A", rnaOneStart: 10, rnaOneEnd: 12, rnaTwo: "RNA_B", rnaTwoStart: 20, rnaTwoEnd: 22, supportCount: 3, rawReadCount: 2, chimeraType: "Type_1", dg: -3.2 },
    { id: "read_2", rnaOne: "RNA_A", rnaOneStart: 11, rnaOneEnd: 13, rnaTwo: "RNA_B", rnaTwoStart: 21, rnaTwoEnd: 23, supportCount: 2, rawReadCount: null, chimeraType: "Type_1", dg: null }
  ],
  filters: { rna: "RNA_A", partner: "RNA_B", type: "all", chimeraType: "all", minSupport: "1", countMode: "support" },
  fasta: { fileName: "reference.fasta", fileSize: 99, sha256: "fasta-sha", sequences: [{ id: "RNA_A", sequence: "ACGU" }], mapping: { RNA_A: "RNA_A" } },
  contact: { rnaX: "RNA_A", rnaY: "RNA_B", binSize: 10, measure: "records", scale: "linear", orientation: "normalised", chimeraType: "all", homodimer: "all", selection: { x: 10, y: 20 }, matrix: null },
  viewpoint: { rna: "RNA_A", rangeMode: "coordinates", start: 10, end: 30, binSize: "1", measure: "records", results: null },
  region: { rna: "RNA_A", start: 10, end: 30, overlap: "any", results: null },
  comparison: {
    rnaX: "RNA_A", rnaY: "RNA_B", binSize: "10", measure: "records", normalise: "library", conditionALabel: "Control", conditionBLabel: "Treatment", result: null,
    datasets: [
      { id: "primary", fileName: "primary.hyb", label: "Primary", condition: "A", summary: { validRecords: 2, supportInteractions: 5 }, records: [] },
      { id: "secondary", fileName: "secondary.hyb", label: "Secondary", condition: "B", summary: { validRecords: 2, supportInteractions: 4 }, records: [] }
    ]
  },
  structure: {
    engine: "cplfold",
    source: "reference",
    rna: "RNA_A",
    start: 1,
    end: 4,
    cplfoldAllowPseudoknot: true,
    constraintMode: "none",
    result: {
      engine: "CPLfold",
      source: "reference",
      sequence: "ACGU",
      dotBracket: "(())",
      pairs: [{ left: 1, right: 4, leftBase: "A", rightBase: "U", type: "A-U" }],
      energy: -2.1,
      energyUnit: "kcal/mol",
      topology: "nested",
      crossingPairs: 0,
      selectedCandidate: 0,
      candidates: [{ type: "nested", topology: "nested", energy: -2.1, pairs: [{ left: 1, right: 4 }], crossingPairs: 0, dotBracket: "(())" }]
    }
  }
};

// The report reads analysis data that web/page-data.js fetched through the
// DataProvider: page result slots, state.reference (DatasetInfo.reference)
// and state.view.report (top filtered rows, total, partner counts).
state.contact.matrix = context.window.Hyb2Data.buildContactMatrix();
state.viewpoint.results = context.window.Hyb2Data.getViewpointResults();
state.region.results = context.window.Hyb2Data.getRegionResults();
state.comparison.result = context.window.Hyb2Data.buildComparisonResults();
state.reference = { fileName: "reference.fasta", fileSize: 99, sha256: "fasta-sha", sequenceCount: 1, mapping: { RNA_A: "RNA_A" }, mapped: { RNA_A: { referenceId: "RNA_A", length: 4 } } };
state.view = {
  report: {
    filteredTotal: 2,
    filteredSample: context.window.Hyb2Data.getFilteredRecords(state),
    partnerCounts: context.window.Hyb2Data.getPartnerCounts()
  }
};

const report = context.window.Hyb2Report;
const html = report.render(state);
{
  const bwState = JSON.parse(JSON.stringify(state));
  bwState.contact.palette = "monochrome";
  const bwHtml = report.render(bwState);
  const map = bwHtml.match(/<svg[^>]*class="[^"]*report-heatmap-svg[^"]*"[\s\S]*?<\/svg>/);
  assert.ok(map, "monochrome report contains the contact map");
  assert.match(map[0], /fill="rgb\(0, 0, 0\)"/, "maximum count is black in the report");
  for (const match of map[0].matchAll(/(?:fill|stroke)="rgb\((\d+), (\d+), (\d+)\)"/g)) {
    assert.equal(match[1], match[2]);
    assert.equal(match[2], match[3]);
  }
  assert.match(bwHtml, /White and black/);
}

assert.match(html, /data-report-page/);
assert.match(html, /assets\/hyb2-logo\.png/);
assert.match(html, /Print \/ Save PDF/);
["Executive summary", "Validation", "HYB File Format", "Interactions", "Contact Map", "Viewpoint", "Region Explorer", "Compare", "RNA Structure"].forEach(function (heading) {
  assert.match(html, new RegExp(heading.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")), heading + " must be present in the report");
});
assert.match(html, /&lt;bad&gt;\.hyb/, "user-controlled file names must be escaped");
assert.doesNotMatch(html, />undefined</, "report markup must not expose undefined values");
// similarity_heatmap.R: a single-valued scale (floor = cap) maps to the
// midpoint of the white-to-teal CIELAB ramp, like scales::rescale.
assert.match(html, /fill="rgb\(153, 208, 193\)"><title>[^<]*; similarity 1</,
  "the similarity heatmap must use similarity_heatmap.R's data-range colour scale");
// the similarity panel is R's white unless the grey display panel is chosen
const similaritySvg = function (markup) { return markup.slice(markup.lastIndexOf('<svg class="report-svg report-heatmap-svg"')); };
assert.match(similaritySvg(html), /<rect x="52" y="24" width="650" height="300" fill="#ffffff"/, "white similarity panel by default");
state.comparison.similarityPanel = "grey";
assert.match(similaritySvg(report.render(state)), /<rect x="52" y="24" width="650" height="300" fill="#ebebeb"/, "grey similarity panel");
delete state.comparison.similarityPanel;

// section status chips come from the result record (results.js)
assert.match(html, /data-step="S04" data-status="ok"/, "the contact map chip is the record's S04 status");
assert.match(html, /data-step="S12" data-status="ok"/);
assert.match(html, /Results at a glance/);

const data = report.getData(state);
assert.equal(data.application, "HYB2 Web Lite");
assert.equal(data.reportScope.kind, "consolidated-session-snapshot");
assert.equal(data.sourceHyb.fileName, "<bad>.hyb");
assert.equal(data.analyses.interactions.result.matchingRecords, 2);
assert.equal(data.analyses.contactMap.result.cells, 1);
assert.equal(data.analyses.viewpoint.result.bins.length, 2);
assert.equal(data.analyses.region.result.matchingRecords, 1);
assert.equal(data.analyses.comparison.result.comparableBins, 1);
assert.equal(data.analyses.structure.sourceHyb.fileName, "<bad>.hyb");

const cofoldState = JSON.parse(JSON.stringify(state));
cofoldState.structure.cplfoldMode = "cofold";
Object.assign(cofoldState.structure.result, {
  engine: "CPLcofold", cutpoint: 2, strandLengths: [2, 2],
  displaySequence: "AC&GU", dotBracketWithBreak: "((&))",
  energyMetadata: { energy_kind: "heuristic" }
});
cofoldState.structure.result.candidates[0].dotBracketWithBreak = "((&))";
const cofoldHtml = report.render(cofoldState);
assert.match(cofoldHtml, /CPLcofold/);
assert.match(cofoldHtml, /AC&amp;GU/);
assert.match(cofoldHtml, /\(\(&amp;\)\)/);
assert.match(cofoldHtml, /Two RNA strands/);
assert.match(cofoldHtml, /experimental ranking heuristic/);
assert.match(cofoldHtml, /CPLfold candidates/, "cofold retains the candidate table");
// A two-region (reference) CPLcofold fold lists both regions in the Setup card.
const twoRegionState = JSON.parse(JSON.stringify(cofoldState));
twoRegionState.structure.result.sourceSegments = [
  { rna: "RNA_A", referenceStart: 1, referenceEnd: 2, preparedStart: 1, preparedEnd: 2 },
  { rna: "RNA_B", referenceStart: 7, referenceEnd: 8, preparedStart: 3, preparedEnd: 4 }];
const twoRegionHtml = report.render(twoRegionState);
assert.match(twoRegionHtml, /<dt>Region 1 RNA<\/dt>\s*<dd[^>]*>RNA_A<\/dd>[\s\S]*<dt>Region 1 range<\/dt>\s*<dd[^>]*>1-2<\/dd>[\s\S]*<dt>Region 2 RNA<\/dt>\s*<dd[^>]*>RNA_B<\/dd>[\s\S]*<dt>Region 2 range<\/dt>\s*<dd[^>]*>7-8<\/dd>/, "both regions in the Setup card");
// RNAcofold (sequence only, two regions): ViennaRNA's two-strand result is
// reported as CPLcofold's, without a candidate table; before a result the
// Setup names the algorithm chosen.
const rnacofoldState = JSON.parse(JSON.stringify(cofoldState));
Object.assign(rnacofoldState.structure, { engine: "viennarna", constraintMode: "none", cplfoldMode: "cofold", evidenceLayout: "paired" });
Object.assign(rnacofoldState.structure.result, { engine: "ViennaRNA", algorithm: "RNAcofold", constraintMode: "none", energyMetadata: null });
delete rnacofoldState.structure.result.candidates;
const rnacofoldHtml = report.render(rnacofoldState);
assert.match(rnacofoldHtml, /<dt>Engine<\/dt>\s*<dd[^>]*>ViennaRNA · RNAcofold<\/dd>/);
assert.match(rnacofoldHtml, /AC&amp;GU/);
assert.match(rnacofoldHtml, /\(\(&amp;\)\)/);
assert.match(rnacofoldHtml, /Two RNA strands/);
assert.match(rnacofoldHtml, /<dt>HYB evidence<\/dt>\s*<dd[^>]*>None \(sequence only\)<\/dd>/);
assert.doesNotMatch(rnacofoldHtml, /CPLfold candidates/);
rnacofoldState.structure.result = null;
rnacofoldState.structure.status = "idle";
{
  // the steps come from the page's analysis-data.js; this file's Hyb2Data is a stand-in
  const sandbox = vm.createContext({ window: {} });
  for (const file of ["support-kernels.js", "analysis-data.js"]) {
    vm.runInContext(fs.readFileSync(path.join(repository, "web", file), "utf8"), sandbox, { filename: file });
  }
  context.window.Hyb2Data.structureSteps = sandbox.window.Hyb2Data.structureSteps;
  try {
    assert.match(report.render(rnacofoldState), /<dt>Engine<\/dt>\s*<dd[^>]*>RNAcofold<\/dd>/);
  } finally { delete context.window.Hyb2Data.structureSteps; }
}
assert.equal(report.getData(rnacofoldState).analyses.structure.setup.cplfoldEvidence, null, "a ViennaRNA setup has no CPLfold evidence");
assert.match(html, /<dt>RNA<\/dt>\s*<dd[^>]*>-<\/dd>[\s\S]*<dt>Range<\/dt>\s*<dd[^>]*>-<\/dd>/,
  "a saved result without source coordinates does not borrow the current form's RNA or range");
assert.doesNotMatch(html, /<dt>Region 2 RNA<\/dt>/);

// A fold that no HYB evidence guided must never be reported as read-guided.
// The fixture's CPLfold result carries no evidence, so it is sequence only.
assert.match(html, /prediction from the sequence alone; no HYB evidence guided it/, "a sequence-only fold is named as such in the limitations");
assert.doesNotMatch(html, /guided by chimeric reads/, "a sequence-only fold is not called read-guided");
assert.match(html, /<dt>HYB evidence<\/dt>\s*<dd[^>]*>None \(sequence only\)<\/dd>/, "the Setup list states the HYB evidence used");
assert.match(html, /No HYB evidence guided this fold \(sequence only\)/);
assert.doesNotMatch(html, /HYB bonus-matrix provenance/, "no bonus-matrix provenance without a bonus matrix");
[
  [{ evidence: { source: "hyb-block-intervals" } }, "HYB block bonus matrix", /HYB bonus-matrix provenance/],
  [{ constraintMode: "hyb-guided", engine: "ViennaRNA WebAssembly" }, "COMRADES constraints \\(ViennaRNA\\)", null],
  [{ engine: "IRIS" }, "IRIS weighted ensemble", null]
].forEach(function (guidedCase) {
  const guidedState = JSON.parse(JSON.stringify(state));
  Object.assign(guidedState.structure.result, guidedCase[0]);
  const guidedHtml = report.render(guidedState);
  assert.match(guidedHtml, /computational prediction guided by chimeric reads/, guidedCase[1] + ": a read-guided fold is named as such");
  assert.doesNotMatch(guidedHtml, /from the sequence alone/, guidedCase[1]);
  assert.match(guidedHtml, new RegExp("<dt>HYB evidence</dt>\\s*<dd[^>]*>" + guidedCase[1] + "</dd>"), guidedCase[1] + " in the Setup list");
  if (guidedCase[2]) assert.match(guidedHtml, guidedCase[2]);
});

// ---- Export panel (Report -> Export -> For people / For AI / For further analysis)
const read = function (file) { return fs.readFileSync(path.join(repository, "web", file), "utf8"); };

// On a phone the panel opened below the fold with focus on <body>, so the
// tap seemed to do nothing. report-page.js now brings it into view and
// focuses its heading; a redraw keeps focus on the control that had it and
// Close gives it back to the Export button. Run against a stand-in document.
{
  const listeners = [];
  const element = function (attrs, top, height, inPanel) {
    return {
      attrs: attrs || {}, inPanel: !!inPanel, scrolled: [], disabled: false, offsetHeight: height || 44,
      getAttribute: function (name) { return Object.prototype.hasOwnProperty.call(this.attrs, name) ? this.attrs[name] : null; },
      getBoundingClientRect: function () { return { top: top || 0, height: height || 44 }; },
      focus: function (options) { doc.activeElement = this; this.focusOptions = options; },
      // a copy: the options object belongs to the page's realm
      scrollIntoView: function (options) { this.scrolled.push(Object.assign({}, options)); },
      closest: function (selector) {
        if (selector === "[data-report-export-panel]") return this.inPanel ? this : null;
        return selector === '[data-action="' + this.attrs["data-action"] + '"]' ? this : null;
      }
    };
  };
  const panelAt = function (top, height, controls) {
    const panel = element({}, top, height, true);
    panel.title = element({}, top + 22, 26, true);
    panel.querySelector = function (selector) { return selector === "[data-report-export-title]" ? panel.title : null; };
    panel.querySelectorAll = function () { return controls || []; };
    return panel;
  };
  const doc = {
    body: element(), panel: null, summary: element({}, -300, 44),
    addEventListener: function (type, listener, capture) { listeners.push([type, listener, capture]); },
    querySelector: function (selector) {
      return selector === "[data-report-export-panel]" ? doc.panel : selector === ".report-export-menu > summary" ? doc.summary : null;
    }
  };
  doc.activeElement = doc.body;
  context.document = doc;
  context.window.innerHeight = 844;
  context.window.getComputedStyle = function () { return { scrollMarginTop: "72px" }; };
  vm.runInContext(read("report-page.js"), context, { filename: "report-page.js" });
  const live = context.window.Hyb2Report;
  const click = function (target) { listeners.forEach(function (entry) { if (entry[0] === "click") entry[1]({ target: target }); }); };
  // app.js: render() builds the markup while the old page is in the document,
  // replaces it (focus falls to <body>), then calls afterRender()
  const redraw = function (panel) {
    live.render(state);
    doc.activeElement = doc.body;
    doc.panel = panel;
    live.afterRender(state);
  };
  assert.ok(listeners.some(function (entry) { return entry[0] === "click" && entry[2] === true; }),
    "Close is read in the capture phase, before app.js redraws");

  // chosen on a phone: the panel is at 873 px in an 844 px viewport
  state.reportExport = { audience: "ai", status: "preparing", list: [], error: "" };
  const opened = panelAt(873, 283);
  redraw(opened);
  assert.deepEqual(opened.scrolled, [{ block: "start", behavior: "instant" }],
    "a chosen export scrolls its panel into view at once (a smooth scroll is cancelled by the next redraw)");
  assert.equal(doc.activeElement, opened.title, "and focuses the panel heading");
  assert.equal(opened.title.focusOptions.preventScroll, true);

  // the package's files arrive: same export, new markup
  state.reportExport.status = "ready";
  const ready = panelAt(72, 2835);
  doc.activeElement = opened.title;
  redraw(ready);
  assert.equal(doc.activeElement, ready.title, "focus stays on the heading when the files arrive");
  assert.deepEqual(ready.scrolled, [], "a redraw of the same export does not scroll again");

  // leaving a file out redraws: focus stays on that box
  const box = element({ "data-action": "report-export-toggle", "data-path": "tables/b.tsv" }, 400, 20, true);
  const after = [element({ "data-action": "report-export-toggle", "data-path": "tables/a.tsv" }, 0, 0, true),
    element({ "data-action": "report-export-toggle", "data-path": "tables/b.tsv" }, 0, 0, true)];
  doc.activeElement = box;
  redraw(panelAt(72, 2900, after));
  assert.equal(doc.activeElement, after[1], "a redraw gives focus back to the control that had it");
  assert.equal(after[1].focusOptions.preventScroll, true);

  // focus elsewhere on the page is not taken
  doc.activeElement = element({ "data-action": "report-select" }, 900, 20);
  redraw(panelAt(72, 2900, after));
  assert.equal(doc.activeElement, doc.body, "focus outside the panel is left alone");

  // Close: focus returns to the Export button, brought back into view
  const close = element({ "data-action": "report-export-close" }, 80, 30, true);
  click(close);
  state.reportExport = null;
  doc.activeElement = close;
  redraw(null);
  assert.equal(doc.activeElement, doc.summary, "Close gives focus back to the Export button");
  assert.deepEqual(doc.summary.scrolled, [{ block: "center", behavior: "instant" }]);

  // a panel that is already on screen (desktop: 638 px of 900) is not moved
  context.window.innerHeight = 900;
  state.reportExport = { audience: "people", status: "ready", list: [], error: "" };
  const desktop = panelAt(638, 176);
  redraw(desktop);
  assert.deepEqual(desktop.scrolled, [], "a panel already in view is focused without scrolling");
  assert.equal(doc.activeElement, desktop.title);

  // ...but one that is still preparing will grow: its buttons must fit too
  state.reportExport = { audience: "ai", status: "preparing", list: [], error: "" };
  const growing = panelAt(720, 150);
  redraw(growing);
  assert.deepEqual(growing.scrolled, [{ block: "start", behavior: "instant" }], "room is made for the buttons of a panel still preparing");

  // closed by another action (a step is unticked): the Export button does not take focus
  doc.summary.scrolled = [];
  click(element({ "data-action": "report-select" }, 900, 20));
  state.reportExport = null;
  doc.activeElement = doc.body;
  redraw(null);
  assert.equal(doc.activeElement, doc.body, "only Close moves focus to the Export button");
  assert.deepEqual(doc.summary.scrolled, []);

  // the heading can take focus, and has no id: app.js restores focus by id
  // after a redraw without preventScroll, which would pull the page back
  state.reportExport = { audience: "people", status: "ready", list: [], error: "" };
  const panelHtml = live.render(state).match(/<section class="report-export-panel"[\s\S]*?<\/section>/)[0];
  assert.match(panelHtml, /<h2 tabindex="-1" data-report-export-title>Export · For people<\/h2>/);
  assert.doesNotMatch(panelHtml, /<h2[^>]*\bid=/);
  state.reportExport = null;
}

// On a phone the Export menu was as wide as its 96 px button: ui-kit.css
// loads after report.css, so .report-export-menu { position: static } lost to
// .export-menu { position: relative } and the list stayed anchored to the button.
{
  const sheets = Array.from(read("index.html").matchAll(/href="\.\/([\w-]+\.css)"/g), function (match) { return match[1]; });
  assert.ok(sheets.indexOf("ui-kit.css") > sheets.indexOf("report.css"), "ui-kit.css loads after report.css");
  assert.match(read("ui-kit.css"), /\n\.export-menu \{\s*position: relative;/);
  const phone = read("report.css").match(/@media \(max-width: 680px\) \{[\s\S]*?\n\}\n/)[0];
  assert.match(phone, /\.report-actions \.report-export-menu \{\s*position: static;/,
    "the phone rule needs two classes to outrank ui-kit.css's .export-menu");
  assert.match(phone, /\.report-export-menu \.export-menu-list \{\s*right: auto;\s*left: 0;\s*width: min\(320px, 100%\);\s*min-width: 0;/,
    "the list takes the actions row's width, up to 320px");
}

// The Import check page is titled as its sidebar entry, and a row header
// (a file name, a dataset label) is shown as written.
{
  const overview = read("overview-page.js");
  assert.match(overview, /<h1>Import check<\/h1>/);
  assert.doesNotMatch(overview, /<h1>Validation<\/h1>/);
  assert.match(read("app.js"), /id: "validation", label: "Import check"/, "the sidebar calls the page Import check");
  assert.match(overview, /<th scope=\\"row\\">" \+ escape\(dataset\.fileName/, "the File column is a row header");
  assert.match(read("ui-kit.css"), /tbody th\[scope="row"\] \{\s*letter-spacing: 0;\s*text-transform: none;/,
    "row headers are not upper-cased: file names are case-sensitive");
}

// "Download all (…, ZIP)" states the size of the ZIP: the files, manifest.json
// and the ZIP's own records. It used to add up the files only.
context.TextEncoder = TextEncoder;
context.window.crypto = globalThis.crypto;
const results = context.window.Hyb2Results;
const exportRecord = results.build(state, {});
const exportFiles = results.files(exportRecord, "ai", { exclude: { "report.md": true } });
const exportEntries = exportFiles.map(function (file) {
  return { path: file.path, mediaType: file.mediaType, description: file.description, bytes: Buffer.byteLength(file.text) };
});
state.reportExport = { audience: "ai", status: "ready", error: "", list: exportEntries.map(function (entry) {
  return { path: entry.path, description: entry.description, fields: [], bytes: entry.bytes, excluded: false, required: false };
}).concat([{ path: "report.md", description: "", fields: [], bytes: 999, excluded: true, required: false },
  { path: "manifest.json", description: "", fields: [], bytes: 0, excluded: false, required: true }]) };
const zipLabel = context.window.Hyb2Report.render(state).match(/data-action="report-export-zip">([^<]*)</)[1];
state.reportExport = null;

results.withManifest(exportFiles, exportRecord).then(function (packaged) {
  const actual = results.zipBytes(packaged, "hyb2-result").length;
  assert.equal(JSON.parse(packaged[packaged.length - 1].text).leftOutByResearcher[0], "report.md");
  assert.equal(results.packageBytes(exportEntries, exportRecord, "hyb2-result", exportFiles.omitted), actual,
    "packageBytes gives the size of the ZIP before it is built");
  // the panel's list carries no media types, so the page's figure is within a few bytes per file
  const listed = exportEntries.map(function (entry) { return { path: entry.path, description: entry.description, bytes: entry.bytes }; });
  assert.ok(Math.abs(results.packageBytes(listed, exportRecord, "hyb2-result", exportFiles.omitted) - actual) <= 10 * listed.length);
  const content = exportEntries.reduce(function (sum, entry) { return sum + entry.bytes; }, 0);
  assert.ok(actual > content, "the ZIP is larger than its files");
  const shown = zipLabel.match(/^Download all \(about ([\d.,]+) (B|KB|MB), ZIP\)$/);
  assert.ok(shown, "the size on the button is marked as approximate: " + zipLabel);
  const shownBytes = Number(shown[1].replace(/,/g, "")) * Math.pow(1024, ["B", "KB", "MB"].indexOf(shown[2]));
  assert.ok(Math.abs(shownBytes - actual) <= actual * 0.006, zipLabel + " must be the ZIP's size (" + actual + " B) to three digits");
  assert.ok(Math.abs(shownBytes - content) > actual * 0.006, "the fixture must tell the ZIP's size from the sum of its files");
  console.log("Report render contract passed.");
});
