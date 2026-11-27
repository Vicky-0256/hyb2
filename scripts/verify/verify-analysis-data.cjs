"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const coordinateCases = require("../../desktop/runtime/tests/coordinate_cases.json");

const repository = path.resolve(__dirname, "..", "..");
const context = vm.createContext({
  Number,
  String,
  Boolean,
  Object,
  Array,
  Math,
  RegExp,
  Set,
  Map,
  Intl,
  window: {}
});

vm.runInContext(fs.readFileSync(path.join(repository, "web", "support-kernels.js"), "utf8"), context);
vm.runInContext(
  fs.readFileSync(path.join(repository, "web", "analysis-data.js"), "utf8"),
  context,
  { filename: "analysis-data.js" }
);

const data = context.window.Hyb2Data;
const contactBudget = data.getContactResourceBudget();
assert.ok(contactBudget.maximumCells >= 50000);
assert.ok(contactBudget.maximumCells <= 1000000);
assert.ok(contactBudget.maximumBinContributions >= 2000000);
assert.ok(contactBudget.maximumBinContributions <= 20000000);
assert.ok(contactBudget.targetBuildMs > 0);
assert.ok(contactBudget.probeCellsPerMs > 0);
assert.ok(contactBudget.probeContributionsPerMs > 0);

function loadBudgetForProfile(deviceMemory, clockStep) {
  let tick = 0;
  const dynamicContext = vm.createContext({
    Number,
    String,
    Boolean,
    Object,
    Array,
    Math,
    RegExp,
    Set,
    Map,
    Intl,
    window: {
      navigator: { deviceMemory: deviceMemory, hardwareConcurrency: 4 },
      performance: { now: function () { tick += clockStep; return tick; } }
    }
  });
  vm.runInContext(
    fs.readFileSync(path.join(repository, "web", "analysis-data.js"), "utf8"),
    dynamicContext,
    { filename: "analysis-data-dynamic-budget.js" }
  );
  return dynamicContext.window.Hyb2Data.getContactResourceBudget();
}

const lowMemoryBudget = loadBudgetForProfile(0.5, 1);
const highMemoryBudget = loadBudgetForProfile(8, 1);
const slowBudget = loadBudgetForProfile(8, 100);
assert.ok(highMemoryBudget.maximumCells > lowMemoryBudget.maximumCells,
  "reported device memory should raise the adaptive contact-cell budget");
assert.ok(highMemoryBudget.maximumBinContributions > lowMemoryBudget.maximumBinContributions,
  "reported device memory should raise the adaptive contribution budget");
assert.ok(highMemoryBudget.maximumCells > slowBudget.maximumCells,
  "the local performance probe should lower the budget on a slower device");
// Compare bins its datasets off the page under the comparison limits: a slow
// probe (a busy machine) must not refuse a map the Contact page would not show
{
  let tick = 0;
  const slowContext = vm.createContext({ Number, String, Boolean, Object, Array, Math, RegExp, Set, Map, Intl,
    window: { navigator: { deviceMemory: 8, hardwareConcurrency: 4 }, performance: { now: function () { tick += 100; return tick; } } } });
  vm.runInContext(fs.readFileSync(path.join(repository, "web", "analysis-data.js"), "utf8"), slowContext, { filename: "analysis-data-slow.js" });
  const slow = slowContext.window.Hyb2Data;
  const limit = slow.getContactResourceBudget().maximumCells;
  const rows = [];
  for (let k = 0; k <= limit; k += 1) {
    const x = 1 + 10 * (k % 1000);
    const y = 1 + 10 * Math.floor(k / 1000);
    rows.push({ rnaOne: "RNA_A", rnaTwo: "RNA_A", rnaOneStart: x, rnaOneEnd: x, rnaTwoStart: y, rnaTwoEnd: y, supportCount: 1 });
  }
  const query = slow.comparisonContactQuery({ rnaX: "RNA_A", rnaY: "RNA_A", binSize: "10" });
  assert.equal(query.budget, "comparison");
  assert.equal(slow.buildContactBins(rows, Object.assign({}, query, { budget: undefined })).ready, false,
    "the Contact page budget refuses " + (limit + 1) + " cells on the slow profile");
  const bins = slow.buildContactBins(rows, query);
  assert.notEqual(bins.ready, false, "the comparison budget keeps them: " + bins.reason);
  assert.equal(bins.x.length, limit + 1);
}
const defaultStructure = data.defaultStructureState();
assert.equal(defaultStructure.engine, "cplfold", "CPLfold should be the default structure engine");
assert.equal(defaultStructure.constraintMode, "none", "Structure folding should default to plain MFE mode");
assert.equal(defaultStructure.constraintText, "", "Manual structure constraints should default to empty input");
assert.equal(defaultStructure.viewMode, "folded", "The molecule-like folded view should be the default structure visualization");
assert.equal(defaultStructure.cplfoldEvidence, "hyb-blocks", "CPLfold should default to the post-HYB evidence path when selected");
assert.equal(defaultStructure.cplfoldBeam, "200");
assert.equal(defaultStructure.cplfoldMaxPhase1, "1");
assert.equal(defaultStructure.cplfoldEnergyModel, "DP09");
assert.equal(defaultStructure.cplfoldAllowPseudoknot, false,
  "CPLfold should default to pseudoknot-free output");
assert.equal(defaultStructure.cplfoldCapacity.status, "unknown");
assert.equal(defaultStructure.cplfoldCapacity.baselineLength, 75);
assert.equal(defaultStructure.cplfoldCapacity.recommendedLength, 75);
assert.equal(defaultStructure.cplfoldCapacity.hardCeiling, 500);

const gappedFasta = data.parseFasta("\ufeff>RNA_gap\rac-\u0085g 1tß\r");
assert.equal(gappedFasta[0].sequence, "AC-G1Tß",
  "FASTA symbols must be preserved so downstream reference coordinates never shift silently");
assert.deepEqual(Array.from(gappedFasta[0].invalidCharacters), ["-", "1", "ß"]);

assert.throws(() => data.parseFasta(">A first\nACGU\n>A second\nUUUU"), /duplicate.*A/i);
const mapped = { mapping: { A: "B", B: "" }, sequences: data.parseFasta(">A\nCCCCC\n>B other\n🧬ATGATGATGATG") };
assert.equal(data.findFastaEntry(mapped, "B"), null);
assert.equal(data.extractReference(mapped, "A", 3, 12).sequence, "TGATGATGAT");
assert.equal(data.extractReference(mapped, "A").end, 13);
assert.throws(() => data.extractReference({ ...mapped, sequences: [...mapped.sequences, mapped.sequences[1]] }, "A"), /duplicate.*B/i);

const duplicateNameMapping = data.buildFastaMapping(["RNA_A"], [
  { id: "header_match", header: "RNA_A description" },
  { id: "RNA_A", header: "different_header" }
]);
assert.equal(duplicateNameMapping.RNA_A, "header_match",
  "an imported record's physical ID does not outrank the HYB ID from its original header");
const pipeNameMapping = data.buildFastaMapping(["Zika_virusRNA"], [
  { id: "Zika|virusRNA", header: "Zika|virusRNA", sequence: "ACGU" }
]);
assert.equal(pipeNameMapping.Zika_virusRNA, "Zika|virusRNA",
  "pipe-delimited FASTA IDs should match underscore-delimited HYB names");
assert.throws(() => data.parseFasta(">x_y\n\n>x-y\nACGU\n>last|trigger\nG"), /duplicate FASTA ID.*x-y/i,
  "validate rewritten IDs even on empty records before filtering them");
const hiddenTrigger = data.parseFasta(">x_y\nCC\n>empty|trigger\n");
for (const sequences of [hiddenTrigger, JSON.parse(JSON.stringify(hiddenTrigger))]) {
  assert.equal(data.extractReference({ sequences }, "x-y").sequence, "CC");
  assert.equal(data.extractReference({ sequences }, "x_y"), null);
}
const mutableRecords = [{ id: "x_y", header: "x_y", sequence: "CC" }, { id: "x-y", header: "x-y", sequence: "AA" }];
assert.equal(data.extractReference({ sequences: mutableRecords }, "x_y").sequence, "CC");
mutableRecords[1].header += " pipe|in-description";
assert.throws(() => data.extractReference({ sequences: mutableRecords, mapping: { other: "x_y" } }, "other"), /duplicate FASTA ID.*x-y/i,
  "an imported list is rechecked even on an explicit physical lookup");
const mappingSize = 20000;
const mappingRnas = Array.from({ length: mappingSize }, function (_, index) { return "RNA_" + index; });
const mappingSequences = mappingRnas.map(function (rna) { return { id: rna, header: rna + " description" }; });
const mappingStartedAt = Date.now();
const largeMapping = data.buildFastaMapping(mappingRnas, mappingSequences);
assert.equal(Object.keys(largeMapping).length, mappingSize);
assert.ok(Date.now() - mappingStartedAt < 1000,
  "FASTA matching should remain linear for transcriptome-sized identifier lists");

function record(options) {
  return Object.assign({
    id: "read_1",
    raw: "fixture",
    lineNumber: 1,
    sequence: "ACGUACGU",
    rnaOne: "RNA_A",
    rnaOneStart: 1,
    rnaOneEnd: 1,
    rnaTwo: "RNA_B",
    rnaTwoStart: 1,
    rnaTwoEnd: 1,
    supportCount: 1,
    supportSource: "record",
    rawReadCount: 1,
    overlapScore: 0,
    isHomodimer: false,
    chimeraType: "Intermolecular",
    chimeraTypeSource: "input",
    dg: null
  }, options || {});
}

function contact(options) {
  return Object.assign({
    rnaX: "RNA_A",
    rnaY: "RNA_B",
    binSize: 10,
    measure: "records",
    colourCap: "max",
    customCap: "",
    scale: "linear",
    orientation: "normalised",
    chimeraType: "all",
    homodimer: "all"
  }, options || {});
}

// One HYB row contributes once to every cell in the arm-by-arm bin rectangle.
// Support is retained as a separate measure and must not multiply record counts.
const spanning = record({
  id: "span_7",
  rnaOneStart: 8,
  rnaOneEnd: 12,
  rnaTwoStart: 19,
  rnaTwoEnd: 21,
  supportCount: 4,
  rawReadCount: 7
});
const spanningMatrix = data.buildContactMatrix([spanning], contact());
assert.equal(spanningMatrix.ready, true);
assert.equal(spanningMatrix.maximumCells, contactBudget.maximumCells);
assert.equal(spanningMatrix.maximumBinContributions, contactBudget.maximumBinContributions);
assert.equal(spanningMatrix.resourceBudget.maximumCells, contactBudget.maximumCells);
assert.equal(spanningMatrix.recordsUsed, 1);
assert.equal(spanningMatrix.totalSupport, 4);
assert.equal(spanningMatrix.cellContributions, 4);
assert.deepEqual(
  Array.from(spanningMatrix.cells, function (cell) { return cell.x + ":" + cell.y; }).sort(),
  ["0:10", "0:20", "10:10", "10:20"]
);
spanningMatrix.cells.forEach(function (cell) {
  assert.equal(cell.records, 1);
  assert.equal(cell.support, 4);
  assert.equal(cell.value, 1);
});
const supportMatrix = data.buildContactMatrix([spanning], contact({ measure: "support" }));
supportMatrix.cells.forEach(function (cell) { assert.equal(cell.value, 4); });
const invalidBinSizeMatrix = data.buildContactMatrix([spanning], contact({ binSize: -10 }));
assert.equal(invalidBinSizeMatrix.ready, true);
assert.equal(invalidBinSizeMatrix.binSize, 10,
  "A non-positive bin size must fall back safely instead of creating a non-terminating loop");

// Cross-check the browser implementation against HYB2's plot_hybrids_3.awk in
// the same entire-arm, 10-nt bin mode. The expected cells are that script's
// output for the .hyb row (tab-separated)
//   span_7 ACGUACGU . rDNA 1 4 8 12 1e-4 rDNA 5 8 19 21 2e-4
// with "awk -f plot_hybrids_3.awk BIN_SIZE=10 USE_ENTIRE_HYBRIDS=1"
// (one-true-awk 20200816), recorded when the script, unchanged from
// https://github.com/Jylau14/hyb2, left this repository's bin/.
const awkCells = { "0:10": 1, "0:20": 1, "10:10": 1, "10:20": 1 };
const browserParityMatrix = data.buildContactMatrix([
  record({ rnaOne: "rDNA", rnaOneStart: 8, rnaOneEnd: 12, rnaTwo: "rDNA", rnaTwoStart: 19, rnaTwoEnd: 21 })
], contact({ rnaX: "rDNA", rnaY: "rDNA", binSize: 10 }));
const browserCells = Object.create(null);
browserParityMatrix.cells.forEach(function (cell) { browserCells[cell.x + ":" + cell.y] = cell.records; });
assert.deepEqual(Object.assign({}, browserCells), awkCells,
  "browser contact accumulation must match plot_hybrids_3.awk entire-arm bins");

// A syntactically valid row with enormous arm extents must be rejected before
// entering the nested bin loops. The returned reason is suitable for the UI.
const pathologicalContactRecord = record({
  id: "huge_extent",
  rnaOneStart: 1,
  rnaOneEnd: 1000000000,
  rnaTwoStart: 1,
  rnaTwoEnd: 1000000000
});
const pathologicalStartedAt = Date.now();
const pathologicalMatrix = data.buildContactMatrix([pathologicalContactRecord], contact());
assert.equal(pathologicalMatrix.ready, false);
assert.equal(pathologicalMatrix.limitExceeded, true);
assert.equal(pathologicalMatrix.cells.length, 0);
assert.equal(pathologicalMatrix.cellContributions, 0,
  "A pathological row must be rejected before any bin-cell expansion");
assert.match(pathologicalMatrix.reason, /Increase the bin size|correct unusually large arm coordinates/);
assert.ok(pathologicalMatrix.maximumCells > 0);
assert.ok(pathologicalMatrix.maximumBinContributions >= pathologicalMatrix.maximumCells);
assert.ok(Date.now() - pathologicalStartedAt < 1000,
  "A pathological contact extent should be rejected in bounded time");

// Normalised orientation includes a reversed RNA order in the same selected map.
const reversed = record({
  id: "reverse_2",
  rnaOne: "RNA_B",
  rnaOneStart: 25,
  rnaOneEnd: 25,
  rnaTwo: "RNA_A",
  rnaTwoStart: 15,
  rnaTwoEnd: 15,
  supportCount: 2
});
const normalised = data.buildContactMatrix([reversed], contact());
assert.equal(normalised.cellMap["10:20"].records, 1);
assert.equal(data.buildContactMatrix([reversed], contact({ orientation: "original" })).recordsUsed, 0);

// The original HYB2 homodimer proxy is same-RNA plus overlap score >= 5.
const homodimer = record({
  id: "homodimer_9",
  rnaOne: "RNA_H",
  rnaOneStart: 2,
  rnaOneEnd: 3,
  rnaTwo: "RNA_H",
  rnaTwoStart: 12,
  rnaTwoEnd: 12,
  rawReadCount: 9,
  overlapScore: 5,
  isHomodimer: true
});
const sameRnaNonHomodimer = record({
  id: "same_rna_3",
  rnaOne: "RNA_H",
  rnaOneStart: 4,
  rnaOneEnd: 4,
  rnaTwo: "RNA_H",
  rnaTwoStart: 14,
  rnaTwoEnd: 14,
  overlapScore: 4,
  isHomodimer: false
});
const homodimerContact = contact({ rnaX: "RNA_H", rnaY: "RNA_H" });
assert.equal(data.buildContactMatrix([homodimer, sameRnaNonHomodimer], Object.assign({}, homodimerContact, { homodimer: "only" })).recordsUsed, 1);
assert.equal(data.buildContactMatrix([homodimer, sameRnaNonHomodimer], Object.assign({}, homodimerContact, { homodimer: "exclude" })).recordsUsed, 1);
assert.deepEqual(
  Array.from(data.getCellRecords([homodimer, sameRnaNonHomodimer], Object.assign({}, homodimerContact, { homodimer: "only" }), 0, 10), function (item) { return item.id; }),
  ["homodimer_9"]
);
assert.deepEqual(
  Array.from(data.getCellRecords([homodimer, sameRnaNonHomodimer], Object.assign({}, homodimerContact, { homodimer: "exclude" }), 0, 10), function (item) { return item.id; }),
  ["same_rna_3"]
);

// Region profiles count every matching arm. A same-RNA record can therefore
// contribute to two distinct positions while remaining one matching record.
const regionRecord = record({
  rnaOne: "RNA_H",
  rnaOneStart: 2,
  rnaOneEnd: 2,
  rnaTwo: "RNA_H",
  rnaTwoStart: 12,
  rnaTwoEnd: 12,
  supportCount: 3
});
const region = data.getRegionResults({
  records: [regionRecord],
  region: { rna: "RNA_H", start: "1", end: "20", partner: "", overlap: "any" }
});
assert.equal(region.records.length, 1);
assert.equal(region.support, 3);
assert.deepEqual(Array.from(region.profile, function (bin) { return bin.value; }), [3, 3]);
assert.deepEqual(Array.from(region.profile, function (bin) { return [bin.start, bin.end]; }), [[1, 10], [11, 20]],
  "region profile bins must retain one-based coordinates and stop at the requested end");
assert.equal(data.getRegionResults({
  records: [regionRecord],
  region: { rna: "RNA_H", start: "1.5", end: "20", partner: "", overlap: "any" }
}).records.length, 0, "region coordinates must be positive integers");
assert.equal(data.getRegionResults({
  records: [regionRecord],
  region: { rna: "RNA_H", start: "0", end: "20", partner: "", overlap: "any" }
}).records.length, 0, "zero is not a valid HYB coordinate");
const cellNavigationRegion = data.getRegionResults({
  records: [
    record({ id: "same_cell", rnaOneStart: 10, rnaOneEnd: 15, rnaTwoStart: 30, rnaTwoEnd: 35 }),
    record({ id: "wrong_y", rnaOneStart: 10, rnaOneEnd: 15, rnaTwoStart: 100, rnaTwoEnd: 105 }),
    record({ id: "reversed_cell", rnaOne: "RNA_B", rnaOneStart: 32, rnaOneEnd: 36, rnaTwo: "RNA_A", rnaTwoStart: 12, rnaTwoEnd: 18 })
  ],
  region: {
    rna: "RNA_A", start: "10", end: "19", partner: "RNA_B",
    partnerStart: "30", partnerEnd: "39", overlap: "any"
  }
});
assert.deepEqual(Array.from(cellNavigationRegion.records, function (item) { return item.id; }), ["same_cell", "reversed_cell"],
  "Region navigation from a map cell must require one orientation to overlap both X and Y intervals");
assert.equal(data.getRegionResults({
  records: [regionRecord],
  region: { rna: "RNA_H", start: "1", end: "20", partner: "RNA_H", partnerStart: "30", partnerEnd: "", overlap: "any" }
}).records.length, 0, "a partial partner-coordinate filter must not be silently ignored");
const hugeRegion = data.getRegionResults({
  records: [regionRecord],
  region: { rna: "RNA_H", start: "1", end: "1000000000000", partner: "", overlap: "any" }
});
assert.ok(hugeRegion.profile.length <= 30000, "a large valid range must have a bounded profile allocation");
assert.equal(hugeRegion.profileBinSizeAdjusted, true);
assert.ok(hugeRegion.profileBinSize > 50);
assert.equal(hugeRegion.profileBinUpdates, 2,
  "Two point arms should update only their two directly computed bins, not scan the full 30,000-bin profile twice");
assert.equal(hugeRegion.profileRangeUpdates, 2,
  "Two clipped Region arms should require exactly two constant-time range updates");
assert.equal(hugeRegion.profile[0].value, 6,
  "Both same-RNA arms remain independent support contributions when they share a coarse bin");

// Region range updates preserve the original whole-bin overlap semantics,
// including arms that touch only one inclusive boundary coordinate.
const regionRangeExact = data.getRegionResults({
  records: [
    record({ id: "region_wide", rnaOneStart: 5, rnaOneEnd: 25, supportCount: 2 }),
    record({ id: "region_left_edge", rnaOneStart: 10, rnaOneEnd: 10, supportCount: 3 }),
    record({ id: "region_middle", rnaOneStart: 11, rnaOneEnd: 20, supportCount: 4 })
  ],
  region: { rna: "RNA_A", start: "1", end: "30", partner: "", overlap: "any" }
});
assert.deepEqual(Array.from(regionRangeExact.profile, function (bin) { return bin.value; }), [5, 6, 2]);
assert.equal(regionRangeExact.profileBinUpdates, 5,
  "Region profileBinUpdates must remain the number of logical arm/bin contributions");
assert.equal(regionRangeExact.profileRangeUpdates, 3,
  "Three matching arms should be represented by three bounded range updates");

const invalidFilterState = {
  records: [regionRecord],
  filters: Object.assign(data.defaultInteractionFilters(), { armOneStart: "-1" })
};
assert.equal(data.getFilteredRecords(invalidFilterState).length, 0,
  "an invalid coordinate filter must not be silently treated as an empty filter");
invalidFilterState.filters.armOneStart = "1.5";
assert.equal(data.getFilteredRecords(invalidFilterState).length, 0,
  "fractional interaction coordinates must be rejected");
// A negative e-value cap is impossible: it matches nothing (also records
// without e-values) and the Interactions page names it in a notice, like a
// negative or reversed arm range and a reversed dG range.
{
  const noEvalue = Object.assign({}, regionRecord, { rnaOneEvalue: null, rnaTwoEvalue: null });
  const capState = { records: [regionRecord, noEvalue], filters: Object.assign(data.defaultInteractionFilters(), { maxEvalue: "-1" }) };
  assert.equal(data.getFilteredRecords(capState).length, 0, "a negative Maximum e-value matches no record");
  capState.filters.maxEvalue = "";
  assert.equal(data.getFilteredRecords(capState).length, 2, "a cleared Maximum e-value filters nothing");
  const pagesSandbox = vm.createContext({ console: console, document: { querySelector: function () { return null; }, documentElement: { dataset: {} } },
    getComputedStyle: function () { return { getPropertyValue: function () { return "#123456"; } }; }, window: {} });
  ["support-kernels.js", "analysis-data.js", "comrades-analysis.js", "analysis-pages.js"].forEach(function (file) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "..", "..", "web", file), "utf8"), pagesSandbox, { filename: file });
  });
  const notice = function (overrides) {
    const html = pagesSandbox.window.Hyb2Pages.renderInteractions({
      summary: { rnaNames: ["A"], chimeraTypes: [] }, records: [],
      filters: Object.assign(pagesSandbox.window.Hyb2Data.defaultInteractionFilters(), overrides),
      view: { interactions: { records: [], total: 0, partners: [] } }
    });
    const match = html.match(/data-range-issue="interactions">([^<]*)/);
    return match ? match[1] : "";
  };
  assert.match(notice({ maxEvalue: "-1" }), /^Maximum e-value -1 is below 0.*No record can match until this is corrected\.$/);
  assert.match(notice({ armOneStart: "-50" }), /^Arm 1 start and Arm 1 end must be whole numbers/);
  assert.match(notice({ armOneStart: "5000", armOneEnd: "100" }), /^Arm 1 start 5,000 is after Arm 1 end 100\./);
  assert.match(notice({ armTwoStart: "0" }), /^Arm 2 start and Arm 2 end must be whole numbers/);
  assert.match(notice({ minDg: "-5", maxDg: "-20" }), /^Minimum dG -5 is greater than Maximum dG -20\./);
  assert.equal(notice({ maxEvalue: "0", armOneStart: "5", armOneEnd: "5" }), "", "possible filters get no notice");
}

// Viewpoint coverage is inclusive per nucleotide, including both arms of a
// same-RNA record. A mapped reference range retains uncovered zero-value bins.
const viewpointRecords = [
  record({ id: "left", rnaOneStart: 2, rnaOneEnd: 4, rnaTwo: "RNA_B", rnaTwoStart: 20, rnaTwoEnd: 20 }),
  record({ id: "right", rnaOne: "RNA_C", rnaOneStart: 30, rnaOneEnd: 30, rnaTwo: "RNA_A", rnaTwoStart: 4, rnaTwoEnd: 5 }),
  record({
    id: "self",
    rnaOne: "RNA_A",
    rnaOneStart: 1,
    rnaOneEnd: 1,
    rnaTwo: "RNA_A",
    rnaTwoStart: 6,
    rnaTwoEnd: 6,
    overlapScore: 7,
    isHomodimer: true
  })
];
const viewpointState = {
  records: viewpointRecords,
  fasta: {
    sequences: [{ id: "ref_A", header: "ref_A", sequence: "ACGUACGU" }],
    mapping: { RNA_A: "ref_A" }
  },
  viewpoint: {
    rna: "RNA_A",
    partner: "",
    chimeraType: "all",
    homodimer: "all",
    rangeMode: "reference",
    start: "2",
    end: "5",
    binSize: "1",
    measure: "records"
  }
};
const viewpoint = data.getViewpointResults(viewpointState);
assert.equal(viewpoint.ready, true);
assert.equal(viewpoint.start, 1);
assert.equal(viewpoint.end, 8);
assert.equal(viewpoint.bins.length, 8);
assert.deepEqual(Array.from(viewpoint.bins, function (bin) { return bin.value; }), [1, 1, 1, 2, 1, 1, 0, 0]);
assert.equal(viewpoint.totalCoverage, 7);
assert.equal(viewpoint.armContributions, 4);
assert.equal(data.extractReference(viewpointState.fasta, "RNA_A", "1.5", "5"), null);
assert.equal(data.extractReference(viewpointState.fasta, "RNA_A", "0", "5"), null);
assert.equal(data.extractReference(viewpointState.fasta, "RNA_A", "-1", "5"), null);
const clampedReference = data.extractReference(viewpointState.fasta, "RNA_A", "2", "99");
assert.equal(clampedReference.start, 2);
assert.equal(clampedReference.end, 8);
assert.equal(clampedReference.sequence.length, 7);

viewpointState.viewpoint.rangeMode = "coordinates";
viewpointState.viewpoint.start = "1.5";
assert.equal(data.getViewpointResults(viewpointState).ready, false, "viewpoint coordinates must be positive integers");
viewpointState.viewpoint.start = "0";
assert.equal(data.getViewpointResults(viewpointState).ready, false, "zero is not a valid viewpoint coordinate");
// An unusable typed range says why: whole numbers from 1 to the RNA's last
// position (its mapped reference length), start <= end. Nothing out of range
// is accepted silently; valid input gives no message.
assert.match(data.getViewpointResults(viewpointState).reason, /^Start and End must be whole numbers from 1 to \d+\.$/);
assert.equal(data.coordinateLimit(10807, { min: 1, max: 10805 }), 10807);
assert.equal(data.coordinateLimit(100, { min: 1, max: 140 }), 140, "records past a mismatched reference keep their coordinates valid");
assert.equal(data.coordinateLimit(null, { min: 1, max: 140 }), null, "no reference: the RNA length is unknown");
assert.equal(data.coordinateRangeIssue("1", "10807", { limit: 10807 }), "");
assert.equal(data.coordinateRangeIssue("", "", { limit: 10807 }), "", "blank fields are left to the caller");
assert.equal(data.coordinateRangeIssue("3000", "2000", { limit: 10807 }), "Start 3,000 is after End 2,000. The start must not be after the end.");
// Range queries allow omitted bounds; direct assembly requires coordinates.
const invalidCoordinates = coordinateCases.invalid
  .filter(value => value !== null && (typeof value !== "string" || value.trim() !== ""))
  .concat(coordinateCases.nonFinite.map(Number));
for (const value of invalidCoordinates) {
  assert.equal(data.coordinateRangeIssue(value, "", { start: "Arm 1 start", end: "Arm 1 end" }), "Arm 1 start and Arm 1 end must be whole numbers, 1 or more.");
  for (const [start, end] of [[value, "50"], ["1", value]]) {
    assert.equal(data.coordinateRangeIssue(start, end, {limit: 10807}), "Start and End must be whole numbers from 1 to 10,807.",
      `raw bounds ${JSON.stringify([start, end])} (${typeof value})`);
    assert.equal(data.extractReference(viewpointState.fasta, "RNA_A", start, end), null);
  }
}
for (const {input: [start, end]} of coordinateCases.validWindows.concat(coordinateCases.safeMaximumWindows)) {
  assert.equal(data.coordinateRangeIssue(start, end, {}), "");
}
assert.equal(data.coordinateRangeIssue(null, null, {}), "", "shared range queries retain null as an omitted bound");
assert.deepEqual(data.extractReference(viewpointState.fasta, "RNA_A", null, null), data.extractReference(viewpointState.fasta, "RNA_A"));
assert.deepEqual(data.extractReference(viewpointState.fasta, "RNA_A", null, "5"), data.extractReference(viewpointState.fasta, "RNA_A", "1", "5"));
assert.deepEqual(data.extractReference(viewpointState.fasta, "RNA_A", "2", null), data.extractReference(viewpointState.fasta, "RNA_A", "2", "8"));
assert.equal(data.coordinateRangeIssue("1", "999999", { limit: 10807, rna: "RNA_A" }), "End 999,999 is beyond the last position of RNA_A (10,807). Use whole numbers from 1 to 10,807.");
assert.equal(data.coordinateRangeIssue("1", "999999", {}), "", "without a known length only the form is checked");
assert.equal(data.coordinateRangeIssue("5", "", { paired: true, start: "Partner start", end: "Partner end" }), "Enter both Partner start and Partner end, or clear both.");
assert.equal(data.viewpointRangeIssue({ rna: "RNA_A", rangeMode: "coordinates", start: "1", end: "999999" }, { referenceLength: 10807, extent: { min: 1, max: 10800 } }),
  "End 999,999 is beyond the last position of RNA_A (10,807). Use whole numbers from 1 to 10,807.");
assert.equal(data.viewpointRangeIssue({ rna: "RNA_A", rangeMode: "reference", start: "0", end: "999999" }, { referenceLength: 10807 }), "", "the full-reference mode ignores Start and End");
assert.equal(data.viewpointRange({ rna: "RNA_A", rangeMode: "coordinates", start: "1", end: "999999" }, { referenceLength: 10807, extent: { min: 1, max: 10800 } }), null, "an out-of-range viewpoint is not queried");
assert.match(data.viewpointRangeIssue({ rna: "RNA_A", start: "500", end: "" }, { extent: { min: 1, max: 140 } }), /^Start 500 is after the last coordinate in the data for RNA_A \(140\)/);
assert.equal(data.regionRangeIssue({ rna: "RNA_A", start: "1", end: "201", partner: "", partnerStart: "", partnerEnd: "" }, { limit: 10807 }), "");
assert.equal(data.regionRangeIssue({ rna: "RNA_A", start: "", end: "" }, { limit: 10807 }), "Enter Start and End to explore a region: whole numbers from 1 to 10,807.");
assert.equal(data.regionRangeIssue({ rna: "RNA_A", start: "5", end: " " }, {}), "Enter End to explore a region: whole numbers from 1 or more.");
assert.equal(data.regionRangeIssue({ rna: "", start: "", end: "" }, { limit: 10807 }), "", "no RNA (no data): nothing to say");
assert.equal(data.regionRangeIssue({ rna: "RNA_A", start: "10800", end: "20000" }, { limit: 10807 }), "End 20,000 is beyond the last position of RNA_A (10,807). Use whole numbers from 1 to 10,807.");
assert.equal(data.regionRangeIssue({ rna: "RNA_A", start: "300", end: "200" }, { limit: 10807 }), "Start 300 is after End 200. The start must not be after the end.");
assert.match(data.regionRangeIssue({ rna: "RNA_A", start: "1", end: "201", partner: "", partnerStart: "10000", partnerEnd: "10807" }, { limit: 10807 }), /^Choose a Partner RNA before entering Partner start and Partner end/);
assert.equal(data.regionRangeIssue({ rna: "RNA_A", start: "1", end: "201", partner: "RNA_B", partnerStart: "10000", partnerEnd: "10900" }, { limit: 10807, partnerLimit: 10676 }),
  "Partner end 10,900 is beyond the last position of RNA_B (10,676). Use whole numbers from 1 to 10,676.");
viewpointState.viewpoint.start = "";
viewpointState.viewpoint.end = "";
const observedViewpoint = data.getViewpointResults(viewpointState);
assert.equal(observedViewpoint.start, 1);
assert.equal(observedViewpoint.end, 6,
  "blank selected coordinates must use the observed HYB extent even when FASTA is available");
viewpointState.viewpoint.rangeMode = "reference";
viewpointState.viewpoint.start = "2";
viewpointState.viewpoint.end = "5";

const missingReferenceState = {
  records: viewpointRecords,
  fasta: viewpointState.fasta,
  viewpoint: Object.assign({}, viewpointState.viewpoint, { rna: "RNA_missing" })
};
assert.equal(data.getViewpointResults(missingReferenceState).ready, false,
  "full-reference mode requires a FASTA mapping for the selected RNA");

viewpointState.viewpoint.homodimer = "only";
const homodimerViewpoint = data.getViewpointResults(viewpointState);
assert.deepEqual(Array.from(homodimerViewpoint.bins, function (bin) { return bin.value; }), [1, 0, 0, 0, 0, 1, 0, 0]);
assert.equal(homodimerViewpoint.records.length, 1);
assert.equal(homodimerViewpoint.armContributions, 2);
viewpointState.viewpoint.homodimer = "exclude";
const nonHomodimerViewpoint = data.getViewpointResults(viewpointState);
assert.deepEqual(Array.from(nonHomodimerViewpoint.bins, function (bin) { return bin.value; }), [0, 1, 1, 2, 1, 0, 0, 0]);
assert.equal(nonHomodimerViewpoint.totalCoverage, 5);

// Viewpoint range accumulation must preserve exact inclusive base coverage
// for partial boundary bins, full middle bins, clipping, and both arms of a
// same-RNA record.
const viewpointRangeExact = data.getViewpointResults({
  records: [
    record({ id: "view_wide", rnaOneStart: 5, rnaOneEnd: 23, supportCount: 2 }),
    record({
      id: "view_reversed",
      rnaOne: "RNA_B",
      rnaOneStart: 50,
      rnaOneEnd: 50,
      rnaTwo: "RNA_A",
      rnaTwoStart: 2,
      rnaTwoEnd: 2,
      supportCount: 3
    }),
    record({
      id: "view_self",
      rnaOne: "RNA_A",
      rnaOneStart: 11,
      rnaOneEnd: 12,
      rnaTwo: "RNA_A",
      rnaTwoStart: 24,
      rnaTwoEnd: 30,
      supportCount: 1
    })
  ],
  viewpoint: {
    rna: "RNA_A",
    partner: "",
    chimeraType: "all",
    homodimer: "all",
    rangeMode: "coordinates",
    start: "2",
    end: "24",
    binSize: "10",
    measure: "support"
  }
});
assert.equal(viewpointRangeExact.ready, true);
assert.deepEqual(
  Array.from(viewpointRangeExact.bins, function (bin) { return [bin.start, bin.end, bin.coverageTotal]; }),
  [[2, 11, 18], [12, 21, 21], [22, 24, 5]]
);
assert.ok(Math.abs(viewpointRangeExact.bins[0].value - 1.8) < 1e-12);
assert.ok(Math.abs(viewpointRangeExact.bins[1].value - 2.1) < 1e-12);
assert.ok(Math.abs(viewpointRangeExact.bins[2].value - (5 / 3)) < 1e-12);
assert.equal(viewpointRangeExact.totalCoverage, 44);
assert.equal(viewpointRangeExact.armContributions, 4);

// Thousands of full-span arms should perform O(arms + bins) work rather than
// O(arms * bins). The explicit counters make this complexity contract stable
// without relying only on wall-clock timing in CI.
const fullSpanArmCount = 4000;
const fullSpanEnd = 900000;
const fullSpanRecords = Array.from({ length: fullSpanArmCount }, function (_, index) {
  return record({
    id: "full_span_" + index,
    rnaOneStart: 1,
    rnaOneEnd: fullSpanEnd,
    supportCount: 2
  });
});
const boundedRegionStartedAt = Date.now();
const boundedRegion = data.getRegionResults({
  records: fullSpanRecords,
  region: { rna: "RNA_A", start: "1", end: String(fullSpanEnd), partner: "", overlap: "any" }
});
assert.equal(boundedRegion.profile.length, 18000);
assert.equal(boundedRegion.profileRangeUpdates, fullSpanArmCount);
assert.equal(boundedRegion.profileBinUpdates, fullSpanArmCount * boundedRegion.profile.length,
  "Logical Region bin contributions remain observable without executing every contribution");
assert.equal(boundedRegion.profile[0].value, fullSpanArmCount * 2);
assert.equal(boundedRegion.profile[boundedRegion.profile.length - 1].value, fullSpanArmCount * 2);
assert.ok(Date.now() - boundedRegionStartedAt < 1000,
  "Thousands of full-span Region arms should complete in bounded time");

const boundedViewpointStartedAt = Date.now();
const boundedViewpoint = data.getViewpointResults({
  records: fullSpanRecords,
  viewpoint: {
    rna: "RNA_A",
    partner: "",
    chimeraType: "all",
    homodimer: "all",
    rangeMode: "coordinates",
    start: "1",
    end: String(fullSpanEnd),
    binSize: "1",
    measure: "support"
  }
});
assert.equal(boundedViewpoint.ready, true);
assert.equal(boundedViewpoint.bins.length, 30000);
assert.equal(boundedViewpoint.armContributions, fullSpanArmCount);
assert.equal(boundedViewpoint.coverageBoundaryUpdates, fullSpanArmCount * 2);
assert.equal(boundedViewpoint.coverageRangeUpdates, fullSpanArmCount);
assert.equal(boundedViewpoint.bins[0].value, fullSpanArmCount * 2);
assert.equal(boundedViewpoint.bins[boundedViewpoint.bins.length - 1].value, fullSpanArmCount * 2);
assert.equal(boundedViewpoint.totalCoverage, fullSpanArmCount * fullSpanEnd * 2);
assert.ok(Date.now() - boundedViewpointStartedAt < 1000,
  "Thousands of full-span Viewpoint arms should complete in bounded time");

// CSV exports preserve raw-read provenance and the derived homodimer flag.
const csv = data.recordsToCsv([homodimer]);
const csvLines = csv.split("\n");
assert.match(csvLines[0], /"raw_read_count"/);
assert.match(csvLines[0], /"homodimer_overlap_ge_5"/);
assert.match(csvLines[1], /"9"/);
assert.match(csvLines[1], /"true"/);
// every text table export ends with exactly one newline
[
  csv,
  data.recordsToCsv([homodimer], {}),
  data.contactToTsv({ binSize: 10, cells: [{ x: 1, y: 11, records: 1, support: 2 }] }),
  data.viewpointToTsv({ bins: [{ start: 1, end: 10, value: 0.5 }] }),
  data.viewpointToTsv(null),
  data.comparisonToTsv(null),
  data.comparisonSelectedToTsv(null, null)
].forEach(function (text, index) {
  assert.ok(/[^\n]\n$/.test(text), "text export " + index + " must end with exactly one newline");
});
assert.equal(data.comparisonLegacyNamesToTsv({ datasets: [] }), "", "an empty names table stays empty");
// merged records name their dataset next to `line`
{
  const merged = data.recordsToCsv([Object.assign({}, homodimer, { datasetId: "d2" })], { d2: { label: "ZAFR_REP2", fileName: "ZAFR_REP2.hyb.gz" } }).split("\n");
  assert.match(merged[0], /"dataset","dataset_file","line"$/);
  assert.match(merged[1], /"ZAFR_REP2","ZAFR_REP2\.hyb\.gz","[0-9]+"$/);
}
// bin coordinates: HYB2 bin keys (first key 0), the last bin's end clamped to the RNA length
{
  const lines = data.contactToTsv({ binSize: 20, length: 10807, length2: 10807, cells: [{ x: 0, y: 20, records: 1, support: 1 }, { x: 10800, y: 10780, records: 2, support: 2 }] }).split("\n");
  assert.equal(lines[1], "0\t19\t20\t39\t1\t1");
  assert.equal(lines[2], "10800\t10807\t10780\t10799\t2\t2", "the last bin ends at the RNA end");
  assert.equal(data.binEnd(10800, 20, null), 10819, "no length: no clamp");
  const compared = data.comparisonToTsv({ binSize: 10, cells: [{ x: 10800, y: 0, conditionA: 36.616151788477524, conditionB: 0, effect: 5.233280360557341, presentDatasets: 2, rawA: 2, rawB: 0, similarity: 0 },
    { x: 0, y: 0, conditionA: 1.234567890123e-9, conditionB: NaN, effect: -0.1, presentDatasets: 1, rawA: 1, rawB: 0, similarity: 0 }] }, { x: 10807, y: 10807 }).split("\n");
  assert.equal(compared[1], "10800\t10807\t0\t9\t36.6162\t0\t5.23328\t2\t2\t0\t0", "6 significant digits, clamped last bin");
  assert.equal(compared[2], "0\t9\t0\t9\t1.23457e-9\tNA\t-0.1\t1\t1\t0\t0", "exponent form for tiny values, NA when not finite");
  assert.equal(data.formatSignificant(838111), "838111");
  assert.deepEqual(JSON.parse(JSON.stringify(data.comparisonRnaLengths({ rnaX: "A", rnaY: "B", datasets: [] }, null, { A: { max: 120 }, B: { max: 90 } }))), { x: 120, y: 90 });
}

// Overview count exports include every record, retain missing provenance as a
// blank value, and use the same arm/pair aggregation semantics as the parser.
const overviewCountRecords = [
  record({ id: "counts_1", rnaOne: "RNA_A", rnaTwo: "RNA_B", supportCount: 1, rawReadCount: 4, overlapScore: 2 }),
  record({ id: "counts_2", rnaOne: "RNA_B", rnaTwo: "RNA_A", supportCount: 3, rawReadCount: null, overlapScore: null }),
  record({ id: "counts_3", rnaOne: "RNA_A", rnaTwo: "RNA_A", supportCount: 2, rawReadCount: 6, overlapScore: 5 })
];
assert.equal(data.rnaCountsToCsv(overviewCountRecords), [
  '"rna","arm_occurrences","source_reads","source_read_count_arms","cluster_support","overlap_score_total","overlap_score_arms"',
  '"RNA_A","4","16","3","8","12","3"',
  '"RNA_B","2","4","1","4","2","1"'
].join("\n") + "\n");
assert.equal(data.rnaPairsToCsv(overviewCountRecords), [
  '"rna_1","rna_2","hyb_records","source_reads","source_read_count_records","cluster_support","overlap_score_total","overlap_score_records"',
  '"RNA_A","RNA_B","2","4","1","4","2","1"',
  '"RNA_A","RNA_A","1","6","1","2","5","1"'
].join("\n") + "\n");
assert.match(data.rnaPairsToCsv([record({ rnaOne: 'RNA,"quoted"', rnaTwo: "RNA_B" })]), /"RNA,""quoted"""/,
  "Overview count exports must CSV-escape RNA names");

// Multi-sample maps average datasets within each condition and report a
// descriptive effect. Statistical DESeq2 inference is intentionally separate.
const comparisonRecord = record({ rnaOneStart: 1, rnaOneEnd: 1, rnaTwoStart: 1, rnaTwoEnd: 1 });
const comparison = data.buildComparisonResults({
  datasets: [
    { id: "a", label: "A", condition: "A", summary: { validRecords: 2 }, records: [comparisonRecord, record({ id: "a2" })] },
    { id: "b", label: "B", condition: "B", summary: { validRecords: 1 }, records: [record({ id: "b1" })] }
  ],
  rnaX: "RNA_A",
  rnaY: "RNA_B",
  binSize: "10",
  measure: "records",
  normalise: "none"
});
assert.equal(comparison.ready, true);
assert.equal(comparison.cells.length, 1);
assert.equal(comparison.cells[0].conditionA, 2);
assert.equal(comparison.cells[0].conditionB, 1);
assert.equal(comparison.cells[0].presentDatasets, 2);
assert.ok(Math.abs(comparison.cells[0].effect - Math.log2(2.5 / 1.5)) < 1e-12);
assert.equal(comparison.cells[0].datasets[0].rawValue, 2);
assert.equal(comparison.cells[0].datasets[0].value, 2);
assert.equal(comparison.cells[0].datasets[1].rawValue, 1);
assert.equal(comparison.cells[0].similarity, 1, "similarity is the smaller summed raw count");
assert.equal(comparison.conservedCells.length, 1);

// Similarity heatmap = HYB2 plot_similarity_map on one merged map per
// condition: replicates are summed, each map keeps bins >= the minimum, and a
// bin in both maps scores min(A, B).  Expected values were computed by hand
// from the awk in bin/plot_similarity_map.
const simRecord = function (id, start) {
  return record({ id: id, rnaOne: "RNA_A", rnaTwo: "RNA_A", rnaOneStart: start, rnaOneEnd: start, rnaTwoStart: 500, rnaTwoEnd: 500 });
};
const repeat = function (prefix, start, n) {
  return Array.from({ length: n }, function (_, i) { return simRecord(prefix + i, start); });
};
const similarityInput = {
  datasets: [
    // bin 0: A 3+4 = 7, B 5 -> 5; bin 100: A 2, B 9 -> 2; bin 200: A only
    { id: "a1", condition: "A", summary: { validRecords: 6 }, records: repeat("a1_0_", 1, 3).concat(repeat("a1_1_", 101, 2), repeat("a1_2_", 201, 1)) },
    { id: "a2", condition: "A", summary: { validRecords: 4 }, records: repeat("a2_0_", 1, 4) },
    { id: "b1", condition: "B", summary: { validRecords: 14 }, records: repeat("b1_0_", 1, 5).concat(repeat("b1_1_", 101, 9)) }
  ],
  rnaX: "RNA_A",
  rnaY: "RNA_A",
  binSize: "10",
  measure: "records",
  normalise: "library"
};
const similarity = data.buildComparisonResults(similarityInput);
const simByX = Object.fromEntries(similarity.cells.map(function (cell) { return [cell.x, cell]; }));
assert.equal(simByX[0].rawA, 7);
assert.equal(simByX[0].rawB, 5);
assert.equal(simByX[0].similarity, 5, "library normalisation must not change the raw similarity count");
assert.equal(simByX[100].similarity, 2);
assert.equal(simByX[200].similarity, 0, "a bin missing from one condition is not similar");
assert.deepEqual(similarity.conservedCells.map(function (cell) { return cell.x; }), [0, 100]);
assert.equal(similarity.similarityMinimum, 1);
const similarityFiltered = data.buildComparisonResults(Object.assign({}, similarityInput, { similarityMinimum: "3" }));
assert.deepEqual(similarityFiltered.conservedCells.map(function (cell) { return [cell.x, cell.similarity]; }), [[0, 5]],
  "bins below the minimum in either condition are dropped before taking the minimum");
// similarity_heatmap.R's scale_fill_gradient spans the data range: white sits
// at the smallest plotted count, not at zero.
assert.equal(similarity.similarityFloor, 2, "the colour floor is the smallest similarity count");
assert.equal(similarityFiltered.similarityFloor, 5, "the floor follows the similarity minimum filter");
assert.match(data.comparisonToTsv(similarity).split("\n")[0], /\tcondition_a_raw\tcondition_b_raw\tsimilarity$/);
const selectedDatasetTsv = data.comparisonSelectedToTsv({ datasets: [
  { id: "a", label: "A sample", condition: "A", fileName: "a.hyb" },
  { id: "b", label: "B sample", condition: "B", fileName: "b.hyb" },
  { id: "absent", label: "Absent\tname", condition: "B", fileName: "absent.hyb" }
] }, comparison.cells[0], comparison.binSize).split("\n");
assert.equal(selectedDatasetTsv[0], "x_start\tx_end\ty_start\ty_end\tdataset_id\tlabel\tcondition\tfile_name\traw_value\tanalysis_value");
assert.equal(selectedDatasetTsv[1], "0\t9\t0\t9\ta\tA sample\tA\ta.hyb\t2\t2");
assert.equal(selectedDatasetTsv[2], "0\t9\t0\t9\tb\tB sample\tB\tb.hyb\t1\t1");
assert.equal(selectedDatasetTsv[3], "0\t9\t0\t9\tabsent\tAbsent name\tB\tabsent.hyb\t0\t0",
  "Selected-bin dataset export must include explicit zeros for absent datasets and remain valid TSV");

const deSeq2Comparison = {
  conditionALabel: "Control",
  conditionBLabel: "Treatment",
  datasets: [
    { id: "a", label: "A sample", condition: "A", fileName: "a.hyb" },
    { id: "b", label: "B sample", condition: "B", fileName: "b.hyb" },
    { id: "absent", label: "Absent sample", condition: "B", fileName: "absent.hyb" }
  ]
};
assert.equal(data.comparisonCountMatrixToTsv(deSeq2Comparison, comparison), [
  "feature_id\ta\tb\tabsent",
  "0_0\t2\t1\t0"
].join("\n") + "\n", "DESeq2 export must use raw integer counts and explicit zeroes, not condition means or normalised values");
assert.equal(data.comparisonSampleMetadataToTsv(deSeq2Comparison), [
  "sample_id\tcondition\tcondition_label\tfile_name",
  "a\tcondition_one\tControl\ta.hyb",
  "b\tcondition_two\tTreatment\tb.hyb",
  "absent\tcondition_two\tTreatment\tabsent.hyb"
].join("\n") + "\n","DESeq2 metadata columns must follow count-matrix sample order and retain condition labels");
assert.equal(data.comparisonLegacyNamesToTsv(deSeq2Comparison), [
  "a\tcondition_one",
  "b\tcondition_two",
  "absent\tcondition_two"
].join("\n") + "\n","Legacy HYB2 names.table must contain exactly two columns without a header");

const legacyCountBoundaryComparison = {
  datasets: [{ id: "a" }]
};
function comparisonResultWithRawCount(rawValue) {
  return {
    ready: true,
    cells: [{ x: 0, y: 0, datasets: [{ id: "a", rawValue: rawValue }] }]
  };
}
assert.equal(
  data.comparisonCountMatrixToTsv(legacyCountBoundaryComparison, comparisonResultWithRawCount(2147483647)),
  "feature_id\ta\n0_0\t2147483647\n",
  "The largest legacy R integer count must remain exportable without rounding"
);
assert.throws(
  function () { data.comparisonCountMatrixToTsv(legacyCountBoundaryComparison, comparisonResultWithRawCount(1.5)); },
  /not a non-negative safe integer.*Record count/i,
  "Fractional counts must be rejected instead of rounded"
);
assert.throws(
  function () { data.comparisonCountMatrixToTsv(legacyCountBoundaryComparison, comparisonResultWithRawCount(Number.MAX_SAFE_INTEGER + 1)); },
  /not a non-negative safe integer/i,
  "Unsafe JavaScript integers must be rejected instead of silently losing precision"
);
assert.throws(
  function () { data.comparisonCountMatrixToTsv(legacyCountBoundaryComparison, comparisonResultWithRawCount(2147483648)); },
  /legacy R integer maximum of 2,147,483,647.*64-bit workflow/i,
  "Counts that overflow the legacy R pipeline must fail with an actionable alternative"
);

const boundedComparison = data.buildComparisonResults({
  datasets: [
    { id: "huge-a", label: "Huge A", condition: "A", summary: { validRecords: 1 }, records: [pathologicalContactRecord] },
    { id: "normal-b", label: "Normal B", condition: "B", summary: { validRecords: 1 }, records: [comparisonRecord] }
  ],
  rnaX: "RNA_A",
  rnaY: "RNA_B",
  binSize: "10",
  measure: "records",
  normalise: "none"
});
assert.equal(boundedComparison.ready, false);
assert.equal(boundedComparison.cells.length, 0);
assert.match(boundedComparison.reason, /Comparison not generated.*Contact map not generated/);

// Individually valid maps can still have a pathological combined union. The
// comparison-wide cap must stop the merge before it allocates unbounded result,
// sort, and lookup arrays.
function disjointBins(offset, count) {
  const bins = { ready: true, x: new Array(count), y: new Array(count), count: new Array(count) };
  for (let index = 0; index < count; index += 1) {
    bins.x[index] = 10 * (offset + (index % 1000));
    bins.y[index] = 10 * Math.floor(index / 1000);
    bins.count[index] = 1;
  }
  return bins;
}
const disjointComparisonStartedAt = Date.now();
const disjointComparison = data.buildComparisonResults({
  datasets: [
    { id: "wide-a", label: "Wide A", condition: "A", summary: { validRecords: 1 }, contactBins: disjointBins(0, 800000) },
    { id: "wide-b", label: "Wide B", condition: "B", summary: { validRecords: 1 }, contactBins: disjointBins(1000, 800000) }
  ],
  rnaX: "RNA_A",
  rnaY: "RNA_B",
  binSize: "10",
  measure: "records",
  normalise: "none"
});
assert.equal(disjointComparison.ready, false);
assert.equal(disjointComparison.limitExceeded, true);
assert.equal(disjointComparison.cells.length, 0);
assert.equal(disjointComparison.maximumCells, 1500000);
assert.match(disjointComparison.reason, /combined maps cover more than 1,500,000 distinct cells/i);
assert.ok(Date.now() - disjointComparisonStartedAt < 5000,
  "Comparison union overflow should stop before constructing full result and lookup arrays");

// The colour cap is found by selection rather than sorting; it must equal
// the sorted-quantile definition, and getCell/cellMap must agree.
(function () {
  let seed = 7;
  const next = function (limit) {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed % limit;
  };
  const randomRecords = [];
  for (let index = 0; index < 400; index += 1) {
    const oneStart = next(300);
    const twoStart = next(300);
    randomRecords.push(record({
      id: "random_" + index,
      rnaOneStart: oneStart,
      rnaOneEnd: oneStart + next(40),
      rnaTwoStart: twoStart,
      rnaTwoEnd: twoStart + next(40),
      supportCount: 1 + next(5)
    }));
  }
  const sortedQuantile = function (values, fraction) {
    const position = Math.max(0, Math.min(values.length - 1, (values.length - 1) * fraction));
    const lower = Math.floor(position);
    const upper = Math.ceil(position);
    return values[lower] + (values[upper] - values[lower]) * (position - lower);
  };
  ["90", "95", "99", "max"].forEach(function (colourCap) {
    ["linear", "log"].forEach(function (scale) {
      ["records", "support"].forEach(function (measure) {
        const matrix = data.buildContactMatrix(randomRecords, contact({ binSize: 5, colourCap: colourCap, scale: scale, measure: measure }));
        const display = Array.from(matrix.cells, function (cell) {
          return scale === "log" ? Math.log1p(cell.value) : cell.value;
        }).sort(function (left, right) { return left - right; });
        const expected = colourCap === "max" ? display[display.length - 1] : sortedQuantile(display, Number(colourCap) / 100);
        assert.ok(Math.abs(matrix.cap - expected) < 1e-12, "cap " + colourCap + "/" + scale + "/" + measure + ": " + matrix.cap + " vs " + expected);
        assert.equal(matrix.max, display[display.length - 1]);
      });
    });
  });
  const lookupMatrix = data.buildContactMatrix(randomRecords, contact({ binSize: 5 }));
  assert.equal(Object.keys(lookupMatrix.cellMap).length, lookupMatrix.cells.length);
  lookupMatrix.cells.forEach(function (cell) {
    assert.equal(lookupMatrix.getCell(cell.x, cell.y), cell);
    assert.equal(lookupMatrix.cellMap[cell.x + ":" + cell.y], cell);
  });
  assert.equal(lookupMatrix.getCell(5000, 5000), null);
}());
// Large comparisons (over 100,000 cells) build a cell's dataset values on
// demand; lookups go through comparisonCellAt().
{
  const large = data.buildComparisonResults({
    datasets: [
      { id: "large-a", label: "Large A", condition: "A", summary: { validRecords: 10 }, contactBins: disjointBins(0, 120000) },
      { id: "large-b", label: "Large B", condition: "B", summary: { validRecords: 10 }, contactBins: disjointBins(500, 120000) }
    ],
    rnaX: "RNA_A",
    rnaY: "RNA_A",
    binSize: "10",
    measure: "records",
    normalise: "none"
  });
  assert.equal(large.ready, true);
  assert.equal(large.cells.length, 120000 + 60000);
  assert.equal(large.cellMap, null, "no cellMap object for large results");
  const shared = data.comparisonCellAt(large, 5000, 0);
  assert.equal(shared.presentDatasets, 2);
  assert.deepEqual(JSON.parse(JSON.stringify(shared.datasets)), [
    { id: "large-a", label: "Large A", condition: "A", rawValue: 1, value: 1 },
    { id: "large-b", label: "Large B", condition: "B", rawValue: 1, value: 1 }
  ]);
  assert.equal(shared.similarity, 1);
  assert.equal(Object.keys(shared).indexOf("datasets"), -1, "lazy dataset values stay out of JSON exports");
  assert.equal(data.comparisonCellAt(large, 5, 0), null);
  assert.equal(large.cells[0].x, 0);
  assert.equal(large.cells[1].y, 10, "cells are sorted by x, then y");
  const matrix = data.comparisonCountMatrixToTsv({ datasets: [{ id: "large-a" }, { id: "large-b" }] }, large).split("\n");
  assert.equal(matrix.pop(), "", "the count table ends with exactly one newline");
  assert.equal(matrix.length, 1 + large.cells.length);
  assert.equal(matrix[1], "0_0\t1\t0");
}

// Per-dataset RNA pairs: a homodimer comparison uses each dataset's own
// genome when the chosen RNA is absent (another strain), as hyb2_compare
// compares every dataset's map by position.
{
  const afr = "ZIKV_African_KX377335-1_virusRNA";
  const ilm = "ZIKV_FrenchPolynesian_KJ776791-2_virusRNA";
  const strain = function (id, rna, condition) {
    return {
      id: id, label: id, condition: condition, summary: { validRecords: 1, rnaNames: [rna, "other_RNA"] },
      records: [record({ rnaOne: rna, rnaTwo: rna, rnaOneStart: 101, rnaOneEnd: 120, rnaTwoStart: 301, rnaTwoEnd: 310 })]
    };
  };
  const strains = { datasets: [strain("afr", afr, "A"), strain("ilm", ilm, "B")], rnaX: afr, rnaY: afr, binSize: "10", measure: "records", normalise: "none" };
  const plan = data.comparisonRnaPlan(strains);
  assert.deepEqual(plan.map(function (item) { return [item.rnaX, item.substituted]; }), [[afr, false], [ilm, true]]);
  assert.equal(data.comparisonContactQuery(strains, strains.datasets[1]).rnaX, ilm);
  const crossStrain = data.buildComparisonResults(strains);
  assert.equal(crossStrain.ready, true);
  assert.equal(data.comparisonCellAt(crossStrain, 100, 300).presentDatasets, 2, "both strains' cells are compared by position");
  assert.deepEqual(JSON.parse(JSON.stringify(crossStrain.rnaPairs)), [{ id: "ilm", label: "ilm", rnaX: ilm, rnaY: ilm }]);
  // a choice in the differential test card applies here too
  strains.deseq = { rnaByDataset: { ilm: "other_RNA" } };
  assert.equal(data.comparisonRnaPlan(strains)[1].rnaX, "other_RNA");
}

// ---- fast paths give the slow paths' answers
{
  // |effect| order: descending, ties in position order (radix on the float bits)
  let seed = 7;
  const random = function () { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
  [0, 1, 2, 5, 1000, 20000].forEach(function (n) {
    const values = new Float64Array(n);
    for (let i = 0; i < n; i += 1) {
      const r = random();
      values[i] = r < .2 ? 0 : r < .4 ? Math.round(r * 10) / 4 : r < .41 ? 5e-324 : r * 37;
    }
    const expected = Array.from({ length: n }, function (_, i) { return i; })
      .sort(function (left, right) { return values[right] - values[left] || left - right; });
    assert.deepEqual(Array.from(data.effectOrderOf(values)), expected, "effect order of " + n + " values");
  });
  // a negative or non-finite value falls back to the comparator sort
  assert.deepEqual(Array.from(data.effectOrderOf(Float64Array.from([1, Infinity, 2]))), [1, 2, 0]);
  // the dense cell index and the Map index agree, including outside the box
  const dense = data.cellIndex(10, 100, 20, 60, 10);
  const map = data.cellIndex(0, 0, 0, 0, 0);
  assert.equal(dense.dense, true);
  assert.equal(map.dense, false);
  [[10, 20], [100, 60], [50, 30], [40, 40]].forEach(function (cell, row) { dense.set(cell[0], cell[1], row); map.set(cell[0], cell[1], row); });
  [[10, 20], [100, 60], [50, 30], [40, 40], [10, 30], [0, 20], [110, 60], [15, 20], [10, 70]].forEach(function (cell) {
    assert.equal(dense.get(cell[0], cell[1]), map.get(cell[0], cell[1]), "cell " + cell);
  });
  assert.deepEqual(Array.from(dense.rowsInOrder(4)), [0, 3, 2, 1], "rows by x, then y");
  // a box over the limit uses the Map
  assert.equal(data.cellIndex(0, 30000, 0, 30000, 10).dense, false);
}

// the Contact Map as a bins x bins matrix over the RNA extents, zeros filled
{
  const csv = data.contactMatrixToCsv({ binSize: 10, xMin: 1, xMax: 40, yMin: 1, yMax: 25,
    cells: [{ x: 11, y: 1, value: 3 }, { x: 21, y: 11, value: 5 }] });
  assert.deepEqual(csv.split("\n"), [
    "y_start/x_start,1,11,21,31",
    "1,0,3,0,0",
    "11,0,0,5,0",
    "21,0,0,0,0",
    ""
  ], "the matrix CSV ends with exactly one newline");
  assert.throws(function () { data.contactMatrixToCsv({ binSize: 1, cells: [] }); }, /Generate a contact map/);
  assert.throws(function () {
    data.contactMatrixToCsv({ binSize: 1, xMin: 1, xMax: 3000, yMin: 1, yMax: 3000, cells: [{ x: 1, y: 1, value: 1 }] });
  }, /use larger bins/);
}

// download names from FASTA IDs are safe on every OS
assert.equal(data.safeFileName("sp|P12345|ZIKV/NS1:a*b?\"c<d>e\u0001_1-200.fasta"), "sp_P12345_ZIKV_NS1_a_b__c_d_e__1-200.fasta");
assert.equal(data.safeFileName("..\\..\\x.txt "), "_.._x.txt");
assert.equal(data.safeFileName(""), "download");
assert.equal(data.safeFileName("plain-name_1.csv"), "plain-name_1.csv");
assert.equal(data.safeFileName("a".repeat(400) + ".fasta").length, 180);
assert.ok(data.safeFileName("a".repeat(400) + ".fasta").endsWith(".fasta"));
{
  // the desktop adapter keeps its own copy (it loads first); both agree
  const adapter = fs.readFileSync(path.join(repository, "web", "desktop-adapter.js"), "utf8");
  assert.match(adapter, /suggested_name: safeFileName\(fileName\)/);
  const desktopContext = vm.createContext({ window: { __TAURI__: { core: { invoke: async function (name, args) { if (name === "file_save_dialog") desktopContext.saved = args; return { saved: true }; } } } },
    FileReader: function () { this.readAsDataURL = function () { this.result = "data:x;base64,QQ=="; this.onload(); }; }, Blob: function () {} });
  vm.runInContext(adapter, desktopContext);
  desktopContext.window.Hyb2Desktop.saveFile("chr1:10-20|x.fasta", new desktopContext.Blob()).then(function () {
    assert.equal(desktopContext.saved.request.suggested_name, data.safeFileName("chr1:10-20|x.fasta"));
  });
}

// incremental SHA-256 equals node:crypto for any split of the input
{
  const crypto = require("node:crypto");
  [0, 1, 55, 56, 63, 64, 65, 119, 120, 1000, 200003].forEach(function (length) {
    const bytes = crypto.randomBytes(length);
    const expected = crypto.createHash("sha256").update(bytes).digest("hex");
    const whole = data.createSha256();
    whole.update(new Uint8Array(bytes));
    assert.equal(whole.hex(), expected, "SHA-256 of " + length + " bytes");
    const split = data.createSha256();
    for (let at = 0; at < length;) {
      const step = 1 + Math.floor(Math.random() * 97);
      split.update(new Uint8Array(bytes.subarray(at, Math.min(length, at + step))));
      at += step;
    }
    assert.equal(split.hex(), expected, "SHA-256 of " + length + " bytes in slices");
  });
}

// search matches a chimera type as stored (Type_3), as shown (Type 3) and by
// its pattern name when ui-kit.js has defined the labels
{
  const typed = [{ id: "r1", sequence: "", rnaOne: "A", rnaTwo: "A", chimeraType: "Type_3", overlapScore: null },
    { id: "r2", sequence: "", rnaOne: "A", rnaTwo: "B", chimeraType: "Intermolecular", overlapScore: null }];
  const search = function (text) {
    return data.filterRecords(typed, Object.assign(data.defaultInteractionFilters(), { search: text })).map(function (r) { return r.id; }).join(",");
  };
  assert.equal(search("Type_3"), "r1");
  assert.equal(search("type 3"), "r1");
  assert.equal(search("intermolecular"), "r2");
  context.window.Hyb2ChimeraTypes = { label: function (value) { return value === "Type_3" ? "Overlap · 5′ arm starts first" : value; } };
  assert.equal(search("overlap · 5′"), "r1");
  delete context.window.Hyb2ChimeraTypes;
}

// partnerWindow: the O(n log n) scan returns what the former per-arm rescan
// returned, and one partner with 50k arms sharing a start stays fast
{
  const legacyPartnerWindow = function (records, rna, start, end, width) {
    const size = Math.max(1, Number(width) || 201);
    const first = Number(start), last = Number(end);
    const arms = new Map();
    (records || []).forEach(function (record) {
      [[record.rnaOne, record.rnaOneStart, record.rnaOneEnd, record.rnaTwo, record.rnaTwoStart, record.rnaTwoEnd],
       [record.rnaTwo, record.rnaTwoStart, record.rnaTwoEnd, record.rnaOne, record.rnaOneStart, record.rnaOneEnd]].forEach(function (side) {
        if (side[0] !== rna || side[1] < first || side[2] > last) return;
        if (side[3] === rna && side[4] <= last && side[5] >= first) return;
        if (!arms.has(side[3])) arms.set(side[3], []);
        arms.get(side[3]).push([side[4], side[5]]);
      });
    });
    let best = null;
    arms.forEach(function (list, partner) {
      list.sort(function (a, b) { return a[0] - b[0] || a[1] - b[1]; });
      for (let i = 0, j = 0; i < list.length; i += 1) {
        while (j < list.length && list[j][0] < list[i][0] + size) j += 1;
        let count = 0;
        for (let k = i; k < j; k += 1) if (list[k][1] <= list[i][0] + size - 1) count += 1;
        if (!best || count > best.records) best = { rna: partner, start: list[i][0], end: list[i][0] + size - 1, records: count };
      }
    });
    const extent = best && data.getRnaExtent(records, best.rna);
    if (extent && best.end > extent.max) {
      best.start = Math.max(1, extent.max - size + 1);
      best.end = extent.max;
    }
    return best;
  };
  let seed = 12345;
  const random = function (limit) {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed % limit;
  };
  const rnas = ["A", "B", "C"];
  for (let round = 0; round < 300; round += 1) {
    const records = [];
    const n = 1 + random(80);
    const span = 50 + random(1500);
    for (let r = 0; r < n; r += 1) {
      const oneStart = 1 + random(span), twoStart = 1 + random(span);
      // a few malformed arms (end before start) keep the comparison honest
      const oneEnd = oneStart + random(60) - (random(20) === 0 ? 10 : 0);
      const twoEnd = twoStart + random(60) - (random(20) === 0 ? 10 : 0);
      records.push({ rnaOne: rnas[random(3)], rnaOneStart: oneStart, rnaOneEnd: oneEnd,
        rnaTwo: rnas[random(3)], rnaTwoStart: twoStart, rnaTwoEnd: twoEnd });
    }
    const from = 1 + random(span), to = from + random(400);
    const width = [undefined, 1, 5, 25, 201][random(5)];
    assert.deepEqual(JSON.parse(JSON.stringify(data.partnerWindow(records, "A", from, to, width))),
      JSON.parse(JSON.stringify(legacyPartnerWindow(records, "A", from, to, width))),
      "partnerWindow matches the former scan (round " + round + ")");
  }
  const crowded = [];
  for (let r = 0; r < 50000; r += 1) {
    crowded.push({ rnaOne: "miR-1", rnaOneStart: 1, rnaOneEnd: 22, rnaTwo: "target", rnaTwoStart: 1 + (r % 3), rnaTwoEnd: 25 + (r % 7) });
  }
  const started = process.hrtime.bigint();
  const crowdedWindow = data.partnerWindow(crowded, "miR-1", 1, 22);
  const crowdedMs = Number(process.hrtime.bigint() - started) / 1e6;
  assert.equal(crowdedWindow.records, 50000);
  assert.equal(crowdedWindow.rna, "target");
  assert.ok(crowdedMs < 500, "partnerWindow over 50k arms at one start takes " + crowdedMs.toFixed(0) + " ms (< 500 ms)");
}

process.stdout.write("analysis-data-contract: ok\n");
