"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const repository = path.resolve(__dirname, "..");
const appSource = fs.readFileSync(path.join(repository, "web", "app.js"), "utf8");
const analysisPagesSource = fs.readFileSync(path.join(repository, "web", "analysis-pages.js"), "utf8");
const overviewPageSource = fs.readFileSync(path.join(repository, "web", "overview-page.js"), "utf8");
const indexSource = fs.readFileSync(path.join(repository, "web", "index.html"), "utf8");
const workflowSource = fs.readFileSync(path.join(repository, ".github", "workflows", "deploy-pages.yml"), "utf8");
const context = vm.createContext({
  Intl,
  Number,
  String,
  Boolean,
  Object,
  Array,
  Math,
  RegExp,
  Set,
  Map,
  window: {}
});

vm.runInContext(
  fs.readFileSync(path.join(repository, "web", "analysis-ui.js"), "utf8"),
  context,
  { filename: "analysis-ui.js" }
);
context.window.HYB2_BUILD = { commit: "test-commit" };

function invokeEmptyExport(action, state) {
  const events = { downloads: [], toasts: [] };
  const handled = context.window.Hyb2UI.handleAction(
    state,
    { dataset: { featureAction: action } },
    {
      download: function () { events.downloads.push("text"); },
      downloadBlob: function () { events.downloads.push("blob"); },
      showToast: function (message) { events.toasts.push(message); }
    }
  );

  assert.equal(handled, true, action + " should be handled");
  assert.deepEqual(events.downloads, [], action + " should not download an empty visual");
  assert.equal(events.toasts.length, 1, action + " should explain why no file was downloaded");
  assert.match(events.toasts[0], /with data before downloading/);
}

const emptyStates = {
  contact: { contact: { matrix: null } },
  viewpoint: { viewpoint: { results: { ready: false, bins: [] } } },
  comparison: { comparison: { result: { ready: false, cells: [] } } }
};

["contact-download-svg", "contact-download-png"].forEach(function (action) {
  invokeEmptyExport(action, emptyStates.contact);
});
["viewpoint-download-svg", "viewpoint-download-png"].forEach(function (action) {
  invokeEmptyExport(action, emptyStates.viewpoint);
});
["comparison-download-svg", "comparison-download-png"].forEach(function (action) {
  invokeEmptyExport(action, emptyStates.comparison);
});
invokeEmptyExport("comparison-export-tsv", emptyStates.comparison);
invokeEmptyExport("comparison-export-deseq-counts", emptyStates.comparison);
invokeEmptyExport("comparison-export-hyb2-names", emptyStates.comparison);
invokeEmptyExport("comparison-export-deseq-metadata", emptyStates.comparison);

assert.equal(context.window.Hyb2UI.visualExportReady({ contact: { matrix: { cells: [{ x: 0, y: 0 }] } } }, "contact"), true);
assert.equal(context.window.Hyb2UI.visualExportReady({ viewpoint: { results: { ready: true, bins: [{ start: 1 }] } } }, "viewpoint"), true);
assert.equal(context.window.Hyb2UI.visualExportReady({ comparison: { result: { ready: true, cells: [{ x: 0, y: 0 }] } } }, "comparison"), true);

assert.deepEqual(
  JSON.parse(JSON.stringify(context.window.Hyb2UI.contactYAxisLabels({ yStart: 10, yEnd: 30 }, 10))),
  { top: 10, bottom: 39 },
  "Contact Y-axis labels must follow the same top-to-bottom order as cell rendering and hit testing"
);
const capParameters = context.window.Hyb2UI.contactParameters({
  summary: { fileName: "fixture.hyb", sha256: "abc" },
  contact: {
    rnaX: "A", rnaY: "B", binSize: 10, measure: "records", colourCap: "custom", customCap: "",
    scale: "linear", orientation: "normalised", chimeraType: "all", homodimer: "all", matrix: { cap: 7 }
  }
});
assert.equal(capParameters.parameters.customCap, null, "a blank custom cap must not be exported as zero");
assert.equal(capParameters.parameters.effectiveColourCap, 7, "parameter export must record the cap actually applied");
const contactExportState = {
  summary: { fileName: "fixture.hyb", fileSize: 88, sha256: "abc" },
  contact: {
    rnaX: "RNA_A", rnaY: "RNA_B", binSize: 10, measure: "records", colourCap: "max", customCap: "",
    scale: "linear", orientation: "normalised", chimeraType: "all", homodimer: "all",
    zoomSelection: { x: 30, y: 130 },
    detailView: { xStart: 20, xEnd: 40, yStart: 120, yEnd: 150 },
    selection: { x: 30, y: 130 },
    matrix: {
      xMin: 0, xMax: 90, yMin: 100, yMax: 190, binSize: 10, cap: 1, max: 1,
      cells: [{ x: 20, y: 120, value: 1 }]
    }
  }
};
const contactExportParameters = context.window.Hyb2UI.contactParameters(contactExportState);
assert.deepEqual(JSON.parse(JSON.stringify(contactExportParameters.parameters.exportedView)), {
  stage: "detail", xStart: 20, xEnd: 49, yStart: 120, yEnd: 159
});
assert.deepEqual(JSON.parse(JSON.stringify(contactExportParameters.parameters.selectedCell)), {
  xStart: 30, xEnd: 39, yStart: 130, yEnd: 139
});
assert.equal(contactExportParameters.sourceHyb.fileSize, 88);
const contactSvg = context.window.Hyb2UI.buildContactSvg(contactExportState);
assert.match(contactSvg, />120<\/text>/, "Contact SVG must label the top Y coordinate");
assert.match(contactSvg, />159<\/text>/, "Contact SVG must label the bottom Y coordinate");
contactExportState.contact.zoomSelection = null;
contactExportState.contact.overviewView = { xStart: 10, xEnd: 60, yStart: 110, yEnd: 170 };
const overviewExport = context.window.Hyb2UI.contactParameters(contactExportState);
assert.deepEqual(JSON.parse(JSON.stringify(overviewExport.parameters.exportedView)), {
  stage: "overview", xStart: 10, xEnd: 69, yStart: 110, yEnd: 179
}, "when detail is locked, parameters must describe the overview canvas used by PNG export");

const reproducibilityState = {
  summary: { fileName: "primary.hyb", fileSize: 321, sha256: "primary-sha" },
  fasta: { fileName: "reference.fa", fileSize: 99, sha256: "fasta-sha", mapping: { RNA_A: "ref_A" } },
  filters: { rna: "RNA_A", chimeraType: "Type_2", minSupport: "3" },
  interactionResults: [{}, {}],
  viewpoint: {
    rna: "RNA_A", partner: "RNA_B", chimeraType: "Type_2", homodimer: "exclude",
    rangeMode: "reference", start: "5", end: "40", binSize: "10", measure: "support",
    results: { start: 1, end: 80, binSize: 10, records: [{}, {}], armContributions: 2, totalCoverage: 44 }
  },
  region: {
    rna: "RNA_A", start: "5", end: "40", partner: "RNA_B", overlap: "contained",
    results: { profileBinSize: 2, records: [{}], support: 7, partners: [{ name: "RNA_B" }] }
  },
  comparison: {
    conditionALabel: "Control", conditionBLabel: "Treatment", rnaX: "RNA_A", rnaY: "RNA_B",
    binSize: "25", measure: "records", normalise: "none",
    datasets: [
      { id: "primary", label: "P", condition: "A", fileName: "primary.hyb", fileSize: 321, summary: { sha256: "primary-sha", validRecords: 4, supportInteractions: 5 } },
      { id: "secondary", label: "S", condition: "B", fileName: "second.hyb", fileSize: 123, sha256: "second-sha", summary: { validRecords: 3, supportInteractions: 6 } }
    ],
    selection: { x: 50, y: 75 },
    result: { ready: true, binSize: 25, cells: [{ x: 50, y: 75 }], conservedCells: [] }
  }
};
const interactionReport = context.window.Hyb2UI.interactionParameters(reproducibilityState);
assert.equal(interactionReport.version, "0.5.0");
assert.equal(interactionReport.buildCommit, "test-commit");
assert.equal(interactionReport.sourceHyb.sha256, "primary-sha");
assert.equal(interactionReport.sourceHyb.sha256Status, "available");
assert.equal(interactionReport.parameters.chimeraType, "Type_2");
assert.equal(interactionReport.result.matchingRecords, 2);
const viewpointReport = context.window.Hyb2UI.viewpointParameters(reproducibilityState);
assert.equal(viewpointReport.sourceFasta.mappedReferenceId, "ref_A");
assert.equal(viewpointReport.parameters.measure, "cluster-support");
assert.equal(viewpointReport.parameters.effectiveEnd, 80);
const regionReport = context.window.Hyb2UI.regionParameters(reproducibilityState);
assert.equal(regionReport.parameters.overlapRule, "contained");
assert.equal(regionReport.result.clusterSupport, 7);
const comparisonReport = context.window.Hyb2UI.comparisonParameters(reproducibilityState);
assert.equal(comparisonReport.parameters.conditionALabel, "Control");
assert.equal(comparisonReport.parameters.conditionBLabel, "Treatment");
assert.equal(comparisonReport.parameters.normalisation, "none");
assert.equal(comparisonReport.parameters.pseudocount, 0.5);
assert.equal(comparisonReport.datasets[1].sha256, "second-sha");
assert.deepEqual(JSON.parse(JSON.stringify(comparisonReport.result.selectedBin)), { xStart: 50, xEnd: 74, yStart: 75, yEnd: 99 });
assert.equal(comparisonReport.methodScope.replicateAwareStatisticalTesting, false);
assert.equal(comparisonReport.methodScope.webRBaseRuntimeSmoke, "recorded-manual-pass");
assert.equal(comparisonReport.methodScope.webRRuntimeValidated, false);
assert.equal(comparisonReport.methodScope.webRRuntimeVendored, false);
assert.equal(comparisonReport.methodScope.webRDeploymentCiSmoke, false);
assert.equal(comparisonReport.methodScope.pinnedDeSeq2ClosureValidated, false);
assert.equal(comparisonReport.methodScope.deSeq2WasmReady, false);
assert.match(comparisonReport.methodScope.releaseBlocker, /DESeq2 1\.52\.0 WebAssembly closure.*numerical parity/);
assert.equal(comparisonReport.methodScope.localCliExportAvailable, true);
assert.equal(comparisonReport.datasets[1].sha256Status, "available");
assert.match(analysisPagesSource, /renderNoContactData\(contact, matrix\)/);
assert.match(analysisPagesSource, /matrix && matrix\.limitExceeded && matrix\.reason/,
  "a contact work-limit failure must surface its actionable reason instead of claiming there were no records");
assert.match(overviewPageSource, /data-action="download-rna-counts"/,
  "Overview must expose the planned RNA-count CSV export");
assert.match(overviewPageSource, /data-action="download-rna-pairs"/,
  "Overview must expose the planned RNA-pair CSV export");
["interactions-export-params", "viewpoint-export-params", "region-export-params", "comparison-export-params"].forEach(function (action) {
  assert.match(analysisPagesSource, new RegExp('data-feature-action="' + action + '"'),
    action + " must expose a companion reproducibility report");
});
assert.match(analysisPagesSource, /data-feature-action="comparison-export-selected"/,
  "a selected comparison bin must expose its per-dataset values for audit and export");
assert.match(analysisPagesSource, /data-feature-action="comparison-export-deseq-counts"/,
  "Compare must expose its raw feature-by-sample matrix for the validated local DESeq2 fallback");
assert.match(analysisPagesSource, /data-feature-action="comparison-export-hyb2-names"/,
  "Compare must expose the two-column headerless names.table required by the legacy HYB2 CLI");
assert.match(analysisPagesSource, /data-feature-action="comparison-export-deseq-metadata"/,
  "Compare must retain extended headered metadata for audit and custom R workflows");
assert.match(analysisPagesSource, /headerless HYB2 names\.table[\s\S]*?Extended metadata has a header/,
  "Compare must distinguish legacy CLI input from extended metadata");

const comparisonExportState = {
  comparison: { datasets: [{ id: "a", condition: "A" }], result: { ready: true, cells: [{ x: 0, y: 0 }] } }
};
const comparisonExportEvents = { downloads: [], toasts: [] };
context.window.Hyb2Data = {
  comparisonCountMatrixToTsv: function () { return "counts"; },
  comparisonLegacyNamesToTsv: function () { return "names"; },
  comparisonSampleMetadataToTsv: function () { return "metadata"; }
};
const comparisonExportApi = {
  download: function (name, contents, mediaType) {
    comparisonExportEvents.downloads.push({ name: name, contents: contents, mediaType: mediaType });
  },
  showToast: function (message) { comparisonExportEvents.toasts.push(message); }
};
context.window.Hyb2UI.handleAction(
  comparisonExportState,
  { dataset: { featureAction: "comparison-export-deseq-counts" } },
  comparisonExportApi
);
context.window.Hyb2UI.handleAction(
  comparisonExportState,
  { dataset: { featureAction: "comparison-export-hyb2-names" } },
  comparisonExportApi
);
context.window.Hyb2UI.handleAction(
  comparisonExportState,
  { dataset: { featureAction: "comparison-export-deseq-metadata" } },
  comparisonExportApi
);
assert.deepEqual(JSON.parse(JSON.stringify(comparisonExportEvents.downloads)), [
  { name: "hyb2-web.table.txt", contents: "counts", mediaType: "text/tab-separated-values" },
  { name: "hyb2-web_names.table", contents: "names", mediaType: "text/tab-separated-values" },
  { name: "deseq2-sample-metadata.tsv", contents: "metadata", mediaType: "text/tab-separated-values" }
]);
comparisonExportEvents.downloads.length = 0;
comparisonExportEvents.toasts.length = 0;
context.window.Hyb2Data.comparisonCountMatrixToTsv = function () {
  throw new Error("Cannot export HYB2/DESeq2 counts: count exceeds the legacy R integer maximum.");
};
context.window.Hyb2UI.handleAction(
  comparisonExportState,
  { dataset: { featureAction: "comparison-export-deseq-counts" } },
  comparisonExportApi
);
assert.deepEqual(comparisonExportEvents.downloads, [], "An invalid count matrix must not trigger a download");
assert.match(comparisonExportEvents.toasts[0], /exceeds the legacy R integer maximum/,
  "Count-export failures must surface an actionable error to the user");
assert.match(analysisPagesSource, /item \? item\.rawValue : 0/,
  "the selected-bin breakdown must display explicit zeroes for datasets without that bin");
assert.match(appSource, /downloadText\("rna_counts\.csv", window\.Hyb2Data\.rnaCountsToCsv\(state\.records \|\| \[\]\), "text\/csv"\)/,
  "the RNA-count control must download a complete record-derived CSV");
assert.match(appSource, /downloadText\("rna_pairs\.csv", window\.Hyb2Data\.rnaPairsToCsv\(state\.records \|\| \[\]\), "text\/csv"\)/,
  "the RNA-pair control must download a complete record-derived CSV");

const comparisonCells = [
  { x: 0, y: 0, presentDatasets: 2 },
  { x: 20, y: 0, presentDatasets: 2 },
  { x: 0, y: 10, presentDatasets: 1 },
  { x: 20, y: 20, presentDatasets: 2 }
];
const comparisonState = {
  comparison: {
    result: {
      ready: true,
      cells: comparisonCells,
      conservedCells: comparisonCells.filter(function (cell) { return cell.presentDatasets >= 2; })
    },
    selection: null,
    activeMap: null
  }
};

assert.equal(context.window.Hyb2UI.moveComparisonSelection(comparisonState, "effect", "ArrowRight"), true);
assert.equal(comparisonState.comparison.selection.x, 20);
assert.equal(comparisonState.comparison.selection.y, 0);
assert.equal(comparisonState.comparison.activeMap, "effect");
assert.equal(context.window.Hyb2UI.moveComparisonSelection(comparisonState, "effect", "ArrowDown"), true);
assert.equal(comparisonState.comparison.selection.x, 20);
assert.equal(comparisonState.comparison.selection.y, 20);
comparisonState.comparison.selection = null;
assert.equal(context.window.Hyb2UI.moveComparisonSelection(comparisonState, "conserved", "ArrowDown"), true);
assert.equal(comparisonState.comparison.selection.x, 20);
assert.equal(comparisonState.comparison.selection.y, 20);
assert.equal(comparisonState.comparison.activeMap, "conserved");

const comparisonRemovalState = {
  comparison: {
    datasets: [
      { id: "primary", summary: { rnaNames: ["RNA_A", "RNA_B"] } },
      { id: "secondary", summary: { rnaNames: ["RNA_UNIQUE"] } }
    ],
    rnaX: "RNA_UNIQUE",
    rnaY: "RNA_UNIQUE",
    result: { stale: true },
    selection: { x: 1, y: 1 }
  }
};
context.window.Hyb2UI.handleAction(
  comparisonRemovalState,
  { dataset: { featureAction: "remove-comparison-dataset", datasetId: "secondary" } },
  { render: function () {}, showToast: function () {} }
);
assert.equal(comparisonRemovalState.comparison.rnaX, "RNA_A");
assert.equal(comparisonRemovalState.comparison.rnaY, "RNA_A");
assert.equal(comparisonRemovalState.comparison.result, null);
assert.equal(comparisonRemovalState.comparison.selection, null);

const typedComparisonState = { comparison: { conditionALabel: "Old", result: { stale: true }, selection: { x: 1, y: 1 } } };
assert.equal(context.window.Hyb2UI.handleInput(typedComparisonState, {
  dataset: { feature: "comparison-control", key: "conditionALabel" }, value: "New label"
}), true);
assert.equal(typedComparisonState.comparison.conditionALabel, "New label",
  "delegated input must persist unblurred text before an asynchronous render");
assert.equal(typedComparisonState.comparison.result, null);

const partnerSelectionState = { filters: { partner: "" }, selectedRecordIndex: 7, interactionScrollTop: 12 };
context.window.Hyb2UI.handleAction(partnerSelectionState, {
  dataset: { featureAction: "select-partner", partner: "RNA_B" }
}, { updateInteractionHash: function () {}, render: function () {} });
assert.equal(partnerSelectionState.selectedRecordIndex, null,
  "selecting a partner must close a record drawer that may not belong to the filtered results");

context.window.Hyb2Pages = {
  findRecord: function () {
    return { rnaOne: "RNA_A", rnaOneStart: 11, rnaOneEnd: 20, rnaTwo: "RNA_B", rnaTwoStart: 31, rnaTwoEnd: 40 };
  }
};
const navigationApi = {
  chooseHybCalls: 0,
  removeFastaCalls: 0,
  toasts: [],
  lastNavigation: null,
  navigate: function (page) { this.lastNavigation = page; },
  render: function () {},
  showToast: function (message) { this.toasts.push(message); },
  chooseHyb: function () { this.chooseHybCalls += 1; },
  removeFasta: function () {
    this.removeFastaCalls += 1;
    fastaState.fasta = null;
    fastaState.viewpoint.results = null;
    fastaState.viewpoint.rangeMode = "coordinates";
  }
};
const regionState = {
  records: [],
  region: { results: { stale: true } },
  contact: { rnaX: "RNA_A", rnaY: "RNA_B", selection: { x: 10, y: 30 }, matrix: { binSize: 10 } },
  viewpoint: { rna: "RNA_A", partner: "RNA_B", selection: { start: 11 }, results: { bins: [{ start: 11, end: 20 }] } },
  comparison: { rnaX: "RNA_A", rnaY: "RNA_B", selection: { x: 10, y: 30 }, result: { binSize: 10 } }
};
["record-region", "contact-explore-region", "viewpoint-explore-region", "comparison-explore-region"].forEach(function (action) {
  regionState.region.results = { stale: true };
  context.window.Hyb2UI.handleAction(regionState, { dataset: { featureAction: action, recordIndex: "0", arm: "1" } }, navigationApi);
  assert.equal(regionState.region.results, null, action + " should invalidate cached region results");
});
assert.equal(regionState.region.partnerStart, 30,
  "map-to-region navigation must retain the selected Y-bin start");
assert.equal(regionState.region.partnerEnd, 39,
  "map-to-region navigation must retain the selected Y-bin end");
regionState.region.results = { stale: true };
assert.equal(
  context.window.Hyb2UI.handleAction(regionState, { dataset: { featureAction: "explore-region" } }, navigationApi),
  true,
  "the Region Explorer primary action must be live"
);
assert.equal(regionState.region.results, null, "exploring a region should invalidate its cached result before rendering");

context.window.Hyb2Data = {
  findFastaEntry: function (fasta, rna) {
    const id = fasta && fasta.mapping && fasta.mapping[rna];
    return id && (fasta.sequences || []).find(function (entry) { return entry.id === id; }) || null;
  }
};
const fastaState = {
  summary: { rnaNames: ["RNA_A", "RNA_B"] },
  fasta: {
    mapping: {}, sequences: [{ id: "reference_A", sequence: "ACGU" }],
    mappingEditorRna: "RNA_A", mappingEditorReference: ""
  },
  viewpoint: { rna: "RNA_A", rangeMode: "reference", results: { stale: true } }
};
assert.equal(context.window.Hyb2UI.handleInput(fastaState, {
  dataset: { feature: "fasta-mapping-editor", key: "mappingEditorReference" }, value: "reference_A"
}), true);
assert.equal(fastaState.fasta.mappingEditorReference, "reference_A",
  "search-input text must be synchronised before an asynchronous render");
const mappingReferenceInput = { value: "stale-reference" };
let mappingEditorRenderCalls = 0;
assert.equal(context.window.Hyb2UI.handleChange(fastaState, {
  dataset: { feature: "fasta-mapping-editor", key: "mappingEditorRna" },
  value: "RNA_B",
  closest: function () {
    return { querySelector: function () { return mappingReferenceInput; } };
  }
}, {
  render: function () { mappingEditorRenderCalls += 1; }
}), true);
assert.equal(fastaState.fasta.mappingEditorRna, "RNA_B");
assert.equal(fastaState.fasta.mappingEditorReference, "",
  "changing the edited RNA should synchronise its current mapping");
assert.equal(mappingReferenceInput.value, "",
  "the mounted reference input should be updated without replacing either editor control");
assert.equal(mappingEditorRenderCalls, 0,
  "moving between FASTA mapping search fields must not rebuild the dialog DOM");
fastaState.fasta.mappingEditorRna = "RNA_A";
fastaState.fasta.mappingEditorReference = "reference_A";
context.window.Hyb2UI.handleAction(
  fastaState,
  { dataset: { featureAction: "apply-fasta-mapping" } },
  navigationApi
);
assert.equal(fastaState.fasta.mapping.RNA_A, "reference_A",
  "the bounded FASTA editor must apply an exact mapping");
assert.equal(fastaState.viewpoint.rangeMode, "reference",
  "applying a valid mapping should retain full-reference Viewpoint mode");
context.window.Hyb2UI.handleAction(
  fastaState,
  { dataset: { featureAction: "remove-current-fasta-mapping" } },
  navigationApi
);
assert.equal(fastaState.fasta.mapping.RNA_A, undefined,
  "the bounded FASTA editor must remove the currently edited mapping");
context.window.Hyb2UI.handleAction(
  fastaState,
  { dataset: { featureAction: "edit-fasta-mapping", rna: "RNA_B" } },
  navigationApi
);
assert.equal(fastaState.fasta.mappingEditorRna, "RNA_B");
fastaState.viewpoint.rangeMode = "reference";
context.window.Hyb2UI.handleChange(
  fastaState,
  { dataset: { feature: "fasta-mapping", rna: "RNA_A" }, value: "reference_A" },
  navigationApi
);
assert.equal(fastaState.viewpoint.results, null, "FASTA mapping changes should invalidate viewpoint results");
assert.equal(fastaState.viewpoint.rangeMode, "reference", "a valid mapping should retain full-reference mode");
context.window.Hyb2UI.handleChange(
  fastaState,
  { dataset: { feature: "fasta-mapping", rna: "RNA_A" }, value: "" },
  navigationApi
);
assert.equal(fastaState.viewpoint.rangeMode, "coordinates", "removing the anchor mapping should leave full-reference mode");
fastaState.viewpoint.rangeMode = "reference";
fastaState.viewpoint.results = { stale: true };
context.window.Hyb2UI.handleAction(fastaState, { dataset: { featureAction: "remove-fasta" } }, navigationApi);
assert.equal(navigationApi.removeFastaCalls, 1, "removing FASTA should delegate to the app session owner");
assert.equal(fastaState.fasta, null, "removing FASTA should clear the loaded reference");
assert.equal(fastaState.viewpoint.results, null, "removing FASTA should invalidate viewpoint results");
assert.equal(fastaState.viewpoint.rangeMode, "coordinates", "removing FASTA should leave full-reference mode");

const replaceState = { dialog: "files" };
context.window.Hyb2UI.handleAction(replaceState, { dataset: { featureAction: "replace-hyb" } }, navigationApi);
assert.equal(replaceState.dialog, null, "replacing HYB must close the Files dialog before landing renders");
assert.equal(navigationApi.chooseHybCalls, 1);

let structureResetCount = 0;
const structureResetOptions = [];
context.window.Hyb2Structure = {
  reset: function (state, options) {
    structureResetCount += 1;
    structureResetOptions.push(options || {});
    state.structure.result = null;
    state.structure.selectedNucleotide = null;
    if (options && options.clearConstraints) {
      state.structure.constraintText = "";
    }
  }
};
const structureControlState = {
  structure: { source: "paste", pastedSequence: "AAAA", constraintMode: "none", constraintText: "", selectedNucleotide: 3, result: { stale: true } }
};
context.window.Hyb2UI.handleChange(
  structureControlState,
  { dataset: { feature: "structure-control", key: "constraintMode" }, type: "select-one", value: "manual-hard-base-pairs" },
  navigationApi
);
assert.equal(structureControlState.structure.constraintMode, "manual-hard-base-pairs");
assert.equal(structureControlState.structure.result, null);
context.window.Hyb2UI.handleChange(
  structureControlState,
  { dataset: { feature: "structure-control", key: "constraintText" }, type: "textarea", value: "4-18\n7-15" },
  navigationApi
);
assert.equal(structureControlState.structure.constraintText, "4-18\n7-15");
assert.equal(structureResetCount, 2, "Manual constraint controls should invalidate an old structure result");
assert.equal(structureResetOptions[1].clearConstraints, false,
  "editing constraints for the same sequence must not clear the constraint editor");
context.window.Hyb2UI.handleInput(
  structureControlState,
  { dataset: { feature: "structure-control", key: "pastedSequence" }, type: "textarea", value: "CCCC" }
);
assert.equal(structureResetOptions.at(-1).clearConstraints, true);
assert.equal(structureControlState.structure.constraintMode, "manual-hard-base-pairs");
assert.equal(structureControlState.structure.constraintText, "",
  "changing the folded sequence must clear constraints authored for its predecessor");
assert.equal(structureControlState.structure.selectedNucleotide, null);

Object.assign(structureControlState.structure, {
  engine: "viennarna",
  source: "paste",
  cplfoldEvidence: "hyb-blocks",
  constraintText: "4-18"
});
context.window.Hyb2UI.handleChange(
  structureControlState,
  { dataset: { feature: "structure-control", key: "engine" }, type: "select-one", value: "cplfold" },
  navigationApi
);
assert.equal(structureControlState.structure.engine, "cplfold");
assert.equal(structureControlState.structure.source, "reference",
  "HYB-guided CPLfold must use a mapped reference region");
assert.equal(structureControlState.structure.constraintText, "",
  "switching engines must clear coordinate-specific ViennaRNA hard pairs");
context.window.Hyb2UI.handleChange(
  structureControlState,
  { dataset: { feature: "structure-control", key: "source" }, type: "select-one", value: "paste" },
  navigationApi
);
assert.equal(structureControlState.structure.cplfoldEvidence, "none",
  "a non-reference CPLfold source must switch to sequence-only evidence semantics");

Object.assign(structureControlState.structure, {
  engine: "cplfold",
  source: "reference",
  selectedRecordIndex: null,
  constraintMode: "hyb-guided",
  cplfoldEvidence: "hyb-blocks",
  result: { stale: true }
});
context.window.Hyb2UI.handleAction(
  structureControlState,
  { dataset: { featureAction: "record-fold", recordIndex: "7" } },
  navigationApi
);
assert.equal(structureControlState.structure.source, "record");
assert.equal(structureControlState.structure.selectedRecordIndex, 7);
assert.equal(structureControlState.structure.constraintMode, "none",
  "folding a read must not retain reference-coordinate ViennaRNA evidence");
assert.equal(structureControlState.structure.cplfoldEvidence, "none",
  "folding a read must not retain reference-coordinate CPLfold evidence");
assert.equal(navigationApi.lastNavigation, "structure");

assert.match(appSource, /addEventListener\("hashchange", handleHashChange\)/);
assert.match(appSource, /addEventListener\("input", handleInput\)/,
  "text controls must synchronise state before blur so asynchronous renders cannot erase typing");
assert.match(appSource, /element\.type === "search"/,
  "searchable large-list and FASTA mapping controls must synchronise input before blur");
assert.match(appSource, /if \(isTextEntry\(event\.target\)\) \{\s*api\.render = function \(\) \{ scheduleCommittedInputRender\(event\.target\); \};/,
  "text-field blur must use the committed-input rendering path");
assert.match(appSource, /addEventListener\("pointerdown", handlePointerDown\)[\s\S]*?addEventListener\("click", finishPointerAction\)/,
  "pointer actions must keep a blur-triggered render from replacing their click target");
assert.match(analysisPagesSource, /available\.length > 750/,
  "large RNA collections must switch away from an unbounded native select");
assert.match(analysisPagesSource, /rnas\.slice\(0, 500\)/);
assert.match(analysisPagesSource, /fasta\.sequences\.slice\(0, 500\)/);
assert.doesNotMatch(analysisPagesSource, /rnas\.map[\s\S]{0,300}fasta\.sequences\.map/,
  "FASTA mapping controls must not build an RNA-by-reference Cartesian option set");
assert.match(appSource, /function handleHashChange\(\)\s*{\s*const previousPage = state\.activePage;\s*syncRoute\(\);\s*render\(\);[\s\S]*?previousPage !== state\.activePage[\s\S]*?window\.scrollTo\(0, 0\)/,
  "page navigation must reveal the new page heading instead of retaining another page's scroll offset");
assert.doesNotMatch(appSource, /<div class="pop-layer" data-action="close-dialog">/);
assert.match(appSource, /dialogLayer && event\.target === dialogLayer/);
assert.match(appSource, /state\.dialog === "files" && state\.summary/,
  "Files dialog rendering must be defensive while a replacement HYB is loading");
assert.match(appSource, /state\.dialog = null;\s*state\.file = file;/,
  "starting any HYB parse must close an open workspace dialog");
assert.match(appSource, /state\.summary = null;\s*state\.records = \[\];\s*state\.filters = null;\s*state\.interactionResults = \[\];/,
  "replacing a HYB must release old primary records and derived interaction state before parsing");
assert.match(appSource, /rebuildFastaMapping\(summary\)/,
  "replacing HYB must rebuild the retained FASTA mapping for the new RNA names");
assert.match(appSource, /state\.parseSession === parseSession && state\.file === file/,
  "primary-file parsing must guard delayed callbacks with a session identity");

const fastaLoaderMatch = appSource.match(/async function loadFasta\(file\)\s*{([\s\S]*?)\n  async function loadComparisonFiles/);
assert.ok(fastaLoaderMatch, "The FASTA loader should remain available for session-safety checks");
const fastaLoaderSource = fastaLoaderMatch[1];
assert.match(fastaLoaderSource, /const primaryFile = state\.file;/);
assert.match(fastaLoaderSource, /const primarySummary = state\.summary;/);
assert.match(fastaLoaderSource, /state\.fastaLoadSession = fastaLoadSession;/);
assert.match(
  fastaLoaderSource,
  /return state\.fastaLoadSession === fastaLoadSession &&\s*state\.file === primaryFile &&\s*state\.summary === primarySummary;/,
  "FASTA loads must belong to the same token, primary file, and parsed summary"
);
assert.match(fastaLoaderSource, /const text = await file\.text\(\);\s*if \(!fastaLoadSessionIsCurrent\(\)\)/,
  "FASTA text reads must re-check their captured analysis session");
assert.match(fastaLoaderSource, /const mapping = window\.Hyb2Data\.buildFastaMapping\(primarySummary\.rnaNames \|\| \[\], sequences\)/,
  "FASTA mapping must use the captured summary rather than a replacement analysis");
assert.match(fastaLoaderSource, /const hash = await digestFile\(file\);\s*if \(state\.fasta !== loadedFasta\)/,
  "FASTA digest completion must use file identity so a retained reference can finish hashing across a primary-HYB replacement");
assert.match(fastaLoaderSource, /if \(state\.fastaLoadSession === fastaLoadSession\) \{\s*invalidateFastaLoad\(\);\s*}/,
  "an old retained-FASTA digest must not invalidate a newer FASTA load token");
assert.match(fastaLoaderSource, /catch \(error\) \{\s*if \(loadedFasta\) \{\s*if \(state\.fasta !== loadedFasta\)/,
  "FASTA hash failures must update only the same retained reference object");
assert.match(fastaLoaderSource, /loadedFasta\.sha256Unavailable = true;[\s\S]*?render\(\);[\s\S]*?fingerprint is unavailable/,
  "a FASTA hash failure must leave an explicit terminal unavailable state");
assert.match(appSource, /digestFile\(file\)\.then[\s\S]*?state\.summary\.sha256Unavailable = !hash;[\s\S]*?\.catch[\s\S]*?state\.summary\.sha256Unavailable = true;/,
  "primary HYB hashing must handle failures without an unhandled rejection or a permanent pending label");
assert.match(analysisPagesSource, /sha256Unavailable \? "Unavailable" : "Calculating locally…"/,
  "the Files drawer must distinguish terminal hash failure from pending calculation");
assert.match(indexSource, /<script src="\.\/build-info\.js" defer><\/script>/);
assert.match(workflowSource, /GITHUB_SHA[\s\S]*?> web\/build-info\.js/,
  "the Pages artifact must record the exact deployed Git commit");
assert.match(workflowSource, /Configure GitHub Pages[\s\S]*?if: \$\{\{ vars\.CPLFOLD_DISTRIBUTION_APPROVED == 'true' \}\}/,
  "Pages configuration must remain behind the CPLfold redistribution approval gate");
assert.match(workflowSource, /Upload GitHub Pages artifact[\s\S]*?if: \$\{\{ vars\.CPLFOLD_DISTRIBUTION_APPROVED == 'true' \}\}/,
  "the browser CPLfold artifact must not be published without explicit licence approval");
assert.match(workflowSource, /deploy:\s*\n\s*if: \$\{\{ vars\.CPLFOLD_DISTRIBUTION_APPROVED == 'true' \}\}/,
  "the deploy job must remain behind the CPLfold redistribution approval gate");
assert.match(analysisPagesSource, /Git commit:/,
  "the Files drawer must display deployed build provenance");
assert.match(appSource, /removeFasta: removeFasta/,
  "the feature UI must delegate FASTA removal to the app session owner");
assert.match(appSource, /function startParsing\(file\)\s*{\s*terminateWorker\(\);\s*cancelComparisonLoads\(\);\s*invalidateFastaLoad\(\);/,
  "starting a replacement primary HYB must cancel pending parser and FASTA work");
assert.match(appSource, /function removeFasta\(\)\s*{\s*invalidateFastaLoad\(\);/,
  "removing the reference must cancel pending FASTA work");
assert.match(appSource, /function clearSession\(\)\s*{\s*terminateWorker\(\);\s*cancelComparisonLoads\(\);\s*invalidateFastaLoad\(\);/,
  "clearing the analysis must cancel pending comparison and FASTA work");
assert.match(appSource, /function resetToLanding\(\)\s*{\s*terminateWorker\(\);\s*cancelComparisonLoads\(\);\s*invalidateFastaLoad\(\);/,
  "resetting to the landing page must cancel pending comparison and FASTA work");
const resetSource = appSource.match(/function resetToLanding\(\)\s*{([\s\S]*?)\n  function terminateWorker/);
assert.ok(resetSource);
["summary", "records", "fasta", "filters", "interactionResults", "contact", "region", "viewpoint", "comparison", "structure"].forEach(function (field) {
  assert.match(resetSource[1], new RegExp("state\\." + field + "\\s*="),
    "resetting after a failed replacement must release " + field);
});

const comparisonLoaderMatch = appSource.match(/async function loadComparisonFiles\(files\)\s*{([\s\S]*?)\n  function parseComparisonFile/);
assert.ok(comparisonLoaderMatch, "The comparison loader should remain available for session-safety checks");
const comparisonLoaderSource = comparisonLoaderMatch[1];
assert.match(comparisonLoaderSource, /const comparisonSession = state\.comparison;/);
assert.match(comparisonLoaderSource, /const primaryFile = state\.file;/);
assert.match(comparisonLoaderSource, /cancelComparisonLoads\(\);\s*const comparisonLoadSession = \{\};\s*state\.comparisonLoadSession = comparisonLoadSession;/,
  "a new comparison batch must cancel and supersede any prior batch");
assert.match(comparisonLoaderSource, /state\.comparison === comparisonSession &&\s*state\.file === primaryFile &&\s*state\.comparisonLoadSession === comparisonLoadSession;/,
  "comparison callbacks must belong to the captured dataset, primary file, and load token");
assert.match(comparisonLoaderSource, /const parsed = await parseComparisonFile\(file\);\s*if \(!comparisonSessionIsCurrent\(\)\)\s*{\s*return;/);
assert.ok(
  (comparisonLoaderSource.match(/comparisonSessionIsCurrent\(\)/g) || []).length >= 5,
  "Every asynchronous comparison phase should verify that its primary session is still current"
);
assert.doesNotMatch(
  comparisonLoaderSource,
  /state\.comparison\.(?:datasets|error|loading|result)/,
  "Comparison mutations must target the captured session, never a replacement session"
);

const comparisonParserSource = appSource.match(/function parseComparisonFile\(file\)\s*{([\s\S]*?)\n  function cancelComparisonLoads/);
assert.ok(comparisonParserSource, "Comparison parsing should expose a cancellable operation boundary");
assert.match(comparisonParserSource[1], /state\.comparisonParsers\.add\(parser\)/,
  "each comparison worker must be registered while active");
assert.match(comparisonParserSource[1], /state\.comparisonParsers\.delete\(parser\)/,
  "settled comparison workers must leave the active registry");
assert.match(comparisonParserSource[1], /error\.name = "AbortError";\s*reject\(error\);/,
  "cancelling comparison parsing must settle its pending Promise");
assert.match(appSource, /function cancelComparisonLoads\(\)\s*{\s*state\.comparisonLoadSession = null;\s*Array\.from\(state\.comparisonParsers\)\.forEach[\s\S]*?parser\.cancel\(\);[\s\S]*?state\.comparisonParsers\.clear\(\);\s*}/,
  "comparison cancellation must terminate every active parser and invalidate its batch token");

process.stdout.write("ui-contract: ok\n");
