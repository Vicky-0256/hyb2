"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const vm = require("node:vm");
const childProcess = require("node:child_process");

const repository = path.resolve(__dirname, "..");
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

vm.runInContext(
  fs.readFileSync(path.join(repository, "web", "analysis-data.js"), "utf8"),
  context,
  { filename: "analysis-data.js" }
);

const data = context.window.Hyb2Data;
const defaultStructure = data.defaultStructureState();
assert.equal(defaultStructure.constraintMode, "none", "Structure folding should default to plain MFE mode");
assert.equal(defaultStructure.constraintText, "", "Manual structure constraints should default to empty input");

const gappedFasta = data.parseFasta(">RNA_gap\nAC-G 1U\n");
assert.equal(gappedFasta[0].sequence, "AC-G1U",
  "FASTA symbols must be preserved so downstream reference coordinates never shift silently");
assert.deepEqual(Array.from(gappedFasta[0].invalidCharacters), ["-", "1"]);

const duplicateNameMapping = data.buildFastaMapping(["RNA_A"], [
  { id: "header_match", header: "RNA_A" },
  { id: "RNA_A", header: "different_header" }
]);
assert.equal(duplicateNameMapping.RNA_A, "header_match",
  "an exact full-header match must retain precedence over a later identifier-only match");
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

// Cross-check the browser implementation against the bundled AWK reference,
// configured for the same entire-arm, 10-nt bin mode.
const awkPath = path.join(repository, "bin", "plot_hybrids_3.awk");
const awkFixture = [
  "span_7", "ACGUACGU", ".", "rDNA", "1", "4", "8", "12", "1e-4",
  "rDNA", "5", "8", "19", "21", "2e-4"
].join("\t") + "\n";
const awkFixtureDirectory = fs.mkdtempSync(path.join(os.tmpdir(), "hyb2-awk-parity-"));
const awkFixturePath = path.join(awkFixtureDirectory, "fixture.hyb");
fs.writeFileSync(awkFixturePath, awkFixture, "utf8");
const awkRun = childProcess.spawnSync("awk", [
  "-f", awkPath, "BIN_SIZE=10", "USE_ENTIRE_HYBRIDS=1", awkFixturePath
], { encoding: "utf8" });
fs.unlinkSync(awkFixturePath);
fs.rmdirSync(awkFixtureDirectory);
assert.equal(awkRun.status, 0, awkRun.stderr || "bundled plot_hybrids_3.awk parity run failed");
const awkCells = Object.create(null);
awkRun.stdout.split(/\r?\n/).filter(function (row) { return row && row.charAt(0) !== "#"; }).forEach(function (row) {
  const fields = row.split("\t");
  awkCells[fields[0] + ":" + fields[1]] = Number(fields[2]);
});
const browserParityMatrix = data.buildContactMatrix([
  record({ rnaOne: "rDNA", rnaOneStart: 8, rnaOneEnd: 12, rnaTwo: "rDNA", rnaTwoStart: 19, rnaTwoEnd: 21 })
], contact({ rnaX: "rDNA", rnaY: "rDNA", binSize: 10 }));
const browserCells = Object.create(null);
browserParityMatrix.cells.forEach(function (cell) { browserCells[cell.x + ":" + cell.y] = cell.records; });
assert.deepEqual(Object.assign({}, browserCells), Object.assign({}, awkCells),
  "browser contact accumulation must match bundled plot_hybrids_3.awk entire-arm bins");

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
].join("\n"));
assert.equal(data.rnaPairsToCsv(overviewCountRecords), [
  '"rna_1","rna_2","hyb_records","source_reads","source_read_count_records","cluster_support","overlap_score_total","overlap_score_records"',
  '"RNA_A","RNA_B","2","4","1","4","2","1"',
  '"RNA_A","RNA_A","1","6","1","2","5","1"'
].join("\n"));
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
].join("\n"), "DESeq2 export must use raw integer counts and explicit zeroes, not condition means or normalised values");
assert.equal(data.comparisonSampleMetadataToTsv(deSeq2Comparison), [
  "sample_id\tcondition\tcondition_label\tfile_name",
  "a\tcondition_one\tControl\ta.hyb",
  "b\tcondition_two\tTreatment\tb.hyb",
  "absent\tcondition_two\tTreatment\tabsent.hyb"
].join("\n"), "DESeq2 metadata columns must follow count-matrix sample order and retain condition labels");
assert.equal(data.comparisonLegacyNamesToTsv(deSeq2Comparison), [
  "a\tcondition_one",
  "b\tcondition_two",
  "absent\tcondition_two"
].join("\n"), "Legacy HYB2 names.table must contain exactly two columns without a header");

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
  "feature_id\ta\n0_0\t2147483647",
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
const disjointComparisonStartedAt = Date.now();
const disjointComparison = data.buildComparisonResults({
  datasets: [
    {
      id: "wide-a",
      label: "Wide A",
      condition: "A",
      summary: { validRecords: 1 },
      records: [record({ rnaOneStart: 1, rnaOneEnd: 224, rnaTwoStart: 1, rnaTwoEnd: 224 })]
    },
    {
      id: "wide-b",
      label: "Wide B",
      condition: "B",
      summary: { validRecords: 1 },
      records: [record({ rnaOneStart: 225, rnaOneEnd: 448, rnaTwoStart: 225, rnaTwoEnd: 448 })]
    }
  ],
  rnaX: "RNA_A",
  rnaY: "RNA_B",
  binSize: "1",
  measure: "records",
  normalise: "none"
});
assert.equal(disjointComparison.ready, false);
assert.equal(disjointComparison.limitExceeded, true);
assert.equal(disjointComparison.cells.length, 0);
assert.equal(disjointComparison.maximumCells, 100000);
assert.match(disjointComparison.reason, /combined maps cover more than 100,000 distinct cells/i);
assert.ok(Date.now() - disjointComparisonStartedAt < 3000,
  "Comparison union overflow should stop before constructing full result and lookup arrays");

process.stdout.write("analysis-data-contract: ok\n");
