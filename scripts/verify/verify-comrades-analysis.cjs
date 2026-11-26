"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const coordinateCases = require("../../desktop/runtime/tests/coordinate_cases.json");

const comrades = require(path.resolve(__dirname, "..", "..", "web", "comrades-analysis.js"));

const fasta = {
  mapping: { RNA_A: "ref-a", RNA_B: "ref-b" },
  sequences: [
    { id: "ref-a", sequence: "AACCGGUUAACCGGUUAACCGGUU" },
    { id: "ref-b", sequence: "GGGGAAAACCCCUUUUGGGGAAAA" }
  ]
};
const records = [
  {
    id: "forward", index: 0, rnaOne: "RNA_A", rnaOneStart: 2, rnaOneEnd: 5,
    rnaTwo: "RNA_A", rnaTwoStart: 10, rnaTwoEnd: 13, isHomodimer: false
  },
  {
    id: "antisense", index: 1, rnaOne: "RNA_A", rnaOneStart: 5, rnaOneEnd: 2,
    rnaTwo: "RNA_A", rnaTwoStart: 10, rnaTwoEnd: 13, isHomodimer: false
  },
  {
    id: "paired-reverse", index: 2, rnaOne: "RNA_B", rnaOneStart: 4, rnaOneEnd: 7,
    rnaTwo: "RNA_A", rnaTwoStart: 3, rnaTwoEnd: 6, isHomodimer: false
  }
];

const single = comrades.prepareReferenceAssembly(records, fasta, {
  regions: [{ rna: "RNA_A", start: 1, end: 16 }]
});
assert.equal(single.sequence, "AACCGGUUAACCGGUU");
assert.equal(single.eligibleRecordCount, 1);
assert.equal(single.skipped.antisense, 1);
assert.equal(single.skipped.outsideSelectedRegions, 1);
assert.equal(single.evidenceArms[0].sequenceOne, "ACCG");
assert.equal(single.evidenceArms[0].oneStart, 2);
assert.equal(single.evidenceArms[0].twoStart, 10);

const paired = comrades.prepareReferenceAssembly(records, fasta, {
  regions: [
    { rna: "RNA_A", start: 1, end: 10 },
    { rna: "RNA_B", start: 1, end: 10 }
  ]
});
assert.equal(paired.sequence.length, 120);
assert.equal(paired.linker.length, 100);
assert.equal(paired.linker.sequence, "A".repeat(50) + "U".repeat(50));
assert.equal(paired.eligibleRecordCount, 1);
assert.equal(paired.evidenceArms[0].reversedFromHyb, true);
assert.equal(paired.evidenceArms[0].oneStart, 3);
assert.equal(paired.evidenceArms[0].twoStart, 114);

// Raw coordinates must be checked before Number can coerce a type or round
// decimal text to an integer. Test the public preparation boundary, each side.
{
  const invalid = coordinateCases.invalid.concat(coordinateCases.nonFinite.map(Number));
  for (const cofold of [false, true]) {
    for (const count of [1, 2]) {
      const original = [{rna: "RNA_A", start: 1, end: 20}, {rna: "RNA_B", start: 3, end: 14}].slice(0, count);
      for (let index = 0; index < count; index++) {
        for (const endpoint of ["start", "end"]) {
          for (const value of invalid) {
            const regions = original.map(region => ({...region}));
            regions[index][endpoint] = value;
            assert.throws(() => comrades.prepareReferenceAssembly([], fasta, {regions, cofold}), /1-based start\/end/,
              `cofold=${cofold}, region=${index + 1}/${count}, ${endpoint}=${JSON.stringify(value)} (${typeof value})`);
          }
        }
      }
    }
    for (const {input: [start, end], expected} of coordinateCases.validWindows) {
      const prepared = comrades.prepareReferenceAssembly([], fasta, {regions: [{rna: "RNA_A", start, end}], cofold});
      assert.equal(prepared.sequence, fasta.sequences[0].sequence.slice(expected[0] - 1, expected[1]));
      assert.deepEqual([prepared.segments[0].referenceStart, prepared.segments[0].referenceEnd], expected);
    }
    const regions = [{rna: "RNA_A", start: " 2 ", end: "11"}, {rna: "RNA_B", start: 3, end: 14}];
    const prepared = comrades.prepareReferenceAssembly([], fasta, {regions, cofold});
    const spacer = cofold ? "" : "A".repeat(50) + "U".repeat(50);
    assert.equal(prepared.sequence, "ACCGGUUAAC" + spacer + "GGAAAACCCCUU");
    assert.deepEqual(prepared.segments.map(segment => [segment.referenceStart, segment.referenceEnd, segment.length]), [[2, 11, 10], [3, 14, 12]]);
    assert.deepEqual(prepared.segments.map(segment => [segment.preparedStart, segment.preparedEnd]), [[1, 10], [11 + spacer.length, 22 + spacer.length]]);
    for (const {input: [start, end]} of coordinateCases.safeMaximumWindows) {
      assert.throws(() => comrades.prepareReferenceAssembly([], fasta, {regions: [{rna: "RNA_A", start, end}], cofold}), /exceeds the mapped FASTA length/);
    }
  }
}

// Preparation callers must obey the same source/layout policy as the form,
// without counting the artificial COMRADES spacer toward either fragment.
{
  const rules = globalThis.Hyb2FoldText;
  const original = {minWindowNt: rules.minWindowNt, minSequenceNt: rules.minSequenceNt};
  const ref = {sequences: [{id: "A", sequence: "g\u0085ggg"}, {id: "B", sequence: "cccc"}]};
  for (const cofold of [false, true]) {
    // Even a full-span explicit single window keeps the window minimum.
    assert.throws(() => comrades.prepareReferenceAssembly([], ref, {regions: [{rna: "A", start: 1, end: 4}], cofold}), /at least 10 nt/);
    for (const length of [1, 4]) {
      const regions = [{rna: "A", start: 1, end: length}, {rna: "B", start: 1, end: length}];
      const prepared = comrades.prepareReferenceAssembly([], ref, {regions, cofold});
      assert.deepEqual(prepared.segments.map(s => s.length), [length, length]);
      assert.equal(prepared.sequence, "G".repeat(length) + (cofold ? "" : "A".repeat(50) + "U".repeat(50)) + "C".repeat(length));
      for (const index of [0, 1]) {
        const backwards = regions.map(r => ({...r}));
        backwards[index] = {...backwards[index], start: 4, end: 1};
        assert.throws(() => comrades.prepareReferenceAssembly([], ref, {regions: backwards, cofold}), /1-based start\/end/);
      }
    }
  }
  try {
    rules.minSequenceNt = 3;
    rules.minWindowNt = 11;
    for (const cofold of [false, true]) {
      for (const [one, two] of [[2, 3], [3, 2]]) {
        const regions = [{rna: "A", start: 1, end: one}, {rna: "B", start: 1, end: two}];
        assert.throws(() => comrades.prepareReferenceAssembly([], ref, {regions, cofold}), /at least 3 nt/);
      }
      const regions = [{rna: "A", start: 1, end: 3}, {rna: "B", start: 1, end: 3}];
      assert.deepEqual(comrades.prepareReferenceAssembly([], ref, {regions, cofold}).segments.map(s => s.length), [3, 3]);
    }
    const single = {sequences: [{id: "A", sequence: "G".repeat(12)}]};
    assert.throws(() => comrades.prepareReferenceAssembly([], single, {regions: [{rna: "A", start: 1, end: 10}]}), /at least 11 nt/);
    assert.equal(comrades.prepareReferenceAssembly([], single, {regions: [{rna: "A", start: 1, end: 11}]}).sequence, "G".repeat(11));
  } finally { Object.assign(rules, original); }
  assert.throws(() => comrades.prepareReferenceAssembly([], fasta, {regions: [{rna: "RNA_A", start: 1, end: 9}]}), /at least 10 nt/);
  assert.equal(comrades.prepareReferenceAssembly([], fasta, {regions: [{rna: "RNA_A", start: 1, end: 10}]}).sequence, "AACCGGUUAA");
}

// Only the COMRADES spacer contributes U; the same two strands are valid without it.
{
  const rules = globalThis.Hyb2FoldText, original = rules.alphabet;
  const ref = {sequences: [{id: "X", sequence: "GGGGCCCC"}]};
  const regions = [{rna: "X", start: 1, end: 4}, {rna: "X", start: 5, end: 8}];
  try {
    rules.alphabet = "ACG";
    assert.throws(() => comrades.prepareReferenceAssembly([], ref, {regions}), /spacer contains unsupported symbols: U/);
    assert.equal(comrades.prepareReferenceAssembly([], ref, {regions, cofold: true}).sequence, "GGGGCCCC");
  } finally { rules.alphabet = original; }
}

// Cofold ignores invalid flanks, but each unsupported symbol still occupies
// one reference position, including symbols encoded by two UTF-16 code units.
{
  const ref = {sequences: [{id: "X", sequence: "🧬A\u0085cgtNccgg"}]};
  const regions = [{rna: "X", start: 2, end: 5}, {rna: "X", start: 7, end: 10}];
  assert.equal(comrades.prepareReferenceAssembly([], ref, {regions, cofold: true}).sequence, "ACGUCCGG");
  assert.throws(() => comrades.prepareReferenceAssembly([], ref, {regions}), /unsupported symbols/);
  assert.throws(() => comrades.prepareReferenceAssembly([], ref, {regions: [{...regions[0], end: 6}, regions[1]], cofold: true}), /unsupported symbols: N/);
}

const cofoldMapped = comrades.mapCofoldPairs({
  sequenceOne: "GGGG", sequenceTwo: "CCCC", oneStart: 5, twoStart: 20, recordId: "x"
}, [
  { left: 1, right: 8 }, { left: 2, right: 7 },
  { left: 1, right: 4 }, { left: 5, right: 8 }
]);
assert.deepEqual(cofoldMapped, [
  { one: 5, two: 23, recordId: "x" },
  { one: 6, two: 22, recordId: "x" }
]);

const evidence = comrades.aggregateEvidence([
  { one: 5, two: 23, recordId: "a" },
  { one: 6, two: 22, recordId: "a" },
  { one: 5, two: 23, recordId: "b" },
  { one: 8, two: 18, recordId: "c" }
]);
assert.deepEqual(evidence.map(function (entry) { return [entry.one, entry.two, entry.count]; }), [
  [5, 23, 2], [6, 22, 1], [8, 18, 1]
]);

const stems = comrades.mergeTouchingStems(evidence);
assert.equal(stems.length, 2);
assert.deepEqual(
  { oneStart: stems[0].oneStart, oneEnd: stems[0].oneEnd, twoStart: stems[0].twoStart, twoEnd: stems[0].twoEnd, support: stems[0].support, length: stems[0].length },
  { oneStart: 5, oneEnd: 6, twoStart: 22, twoEnd: 23, support: 3, length: 2 }
);
const constraints = comrades.stemsToConstraints(stems, 75);
assert.deepEqual(constraints[0].pairs, [{ left: 5, right: 23 }, { left: 6, right: 22 }]);
assert.equal(constraints[0].support, 3);

// Chimeras occur in both arm orders: a base pair is counted once, with `one`
// the lower position. (1, 9) and (9, 1) halved the pair's support and gave the
// same constraint twice.
const bothOrders = comrades.aggregateEvidence([
  { one: 1, two: 9, recordId: "a" }, { one: 9, two: 1, recordId: "b" },
  { one: 2, two: 8, recordId: "a" }, { one: 8, two: 2, recordId: "b" }, { one: 8, two: 2, recordId: "c" },
  { one: 5, two: 5, recordId: "d" }
]);
assert.deepEqual(bothOrders.map(function (entry) { return [entry.one, entry.two, entry.count, entry.recordIds]; }), [
  [2, 8, 3, ["a", "b", "c"]], [1, 9, 2, ["a", "b"]]
]);
assert.deepEqual(
  comrades.stemsToConstraints(comrades.mergeTouchingStems(bothOrders), 75)
    .map(function (item) { return [item.left, item.right, item.length, item.support]; }),
  [[1, 9, 2, 5]]
);

// Weak pairs are trimmed from the ends of a stem (hyb2 gk-dev's
// test_bp2hyb_trims_weak_helix_ends, and the same cases as the Python runtime
// and bin/bp2hyb.sh): 10-50..14-46 is well supported, 9-51 and 15-45 extend it
// with 2 and 1 observations.
const weakEnds = [
  [9, 51, 2], [10, 50, 100], [11, 49, 100], [12, 48, 90], [13, 47, 80], [14, 46, 100], [15, 45, 1],
  [30, 70, 7], [31, 69, 1]
].map(function (item) { return { one: item[0], two: item[1], count: item[2] }; });
function stemRows(trimFraction) {
  return comrades.mergeTouchingStems(weakEnds, trimFraction).map(function (stem) {
    return [stem.rank, stem.oneStart, stem.oneEnd, stem.twoStart, stem.twoEnd, stem.length, stem.support,
      stem.evidencePairs.map(function (pair) { return pair.count; })];
  });
}
// 1 of 7 is above 5%, so the second stem keeps both pairs
assert.deepEqual(stemRows(), [[1, 10, 14, 46, 50, 5, 470, [100, 100, 90, 80, 100]], [2, 30, 31, 69, 70, 2, 8, [7, 1]]]);
assert.deepEqual(stemRows(0), [[1, 9, 15, 45, 51, 7, 473, [2, 100, 100, 90, 80, 100, 1]], [2, 30, 31, 69, 70, 2, 8, [7, 1]]]);
assert.deepEqual(stemRows(0.2)[1], [2, 30, 30, 70, 70, 1, 7, [7]]);
assert.deepEqual(comrades.stemsToConstraints(comrades.mergeTouchingStems(weakEnds), 75)[0].pairs,
  [{ left: 10, right: 50 }, { left: 11, right: 49 }, { left: 12, right: 48 }, { left: 13, right: 47 }, { left: 14, right: 46 }]);

const compatible = comrades.validateConstraintSet(constraints.slice(0, 1), "AAAAGGAAAAAAAAACCAAAACCC", 3);
assert.equal(compatible.valid, true);
const crossing = comrades.validateConstraintSet([
  { left: 1, right: 10, length: 1 },
  { left: 5, right: 15, length: 1 }
], "GAAAGAAAACAAAAC", 3);
assert.equal(crossing.valid, false);
assert.equal(crossing.reason, "crossing-pairs");

assert.deepEqual(comrades.seededShuffle([1, 2, 3, 4, 5], "seed"), comrades.seededShuffle([1, 2, 3, 4, 5], "seed"));
assert.notDeepEqual(comrades.seededShuffle([1, 2, 3, 4, 5], "seed"), comrades.seededShuffle([1, 2, 3, 4, 5], "other"));

const scored = comrades.scoreStructurePairs([
  { left: 5, right: 23 }, { left: 8, right: 18 }, { left: 2, right: 4 }
], evidence, 25);
assert.equal(scored.matchedPairs, 2);
assert.equal(scored.matchedPairSupport, 3);
assert.equal(scored.comradesScore, 6, "the original VARNA-score sum assigns each supported pair score to both nucleotides");
assert.equal(scored.nucleotideSupport[4], 2);
assert.equal(scored.nucleotideSupport[22], 2);

// Cap each endpoint after combining observations in both orientations. The
// matched-pair total, pair annotations and values used for colour stay raw.
const boundaryPairs = [{left: 1, right: 8}, {left: 2, right: 7}, {left: 3, right: 6}];
const capped = comrades.scoreStructurePairs(boundaryPairs, [
  {one: 1, two: 8, count: 999_999}, {one: 2, two: 7, count: 1_000_000},
  {one: 3, two: 6, count: 800_000}, {one: 6, two: 3, count: 700_000}
], 8);
assert.equal(capped.comradesScore, 5_999_998);
assert.equal(capped.matchedPairSupport, 3_499_999);
assert.deepEqual(capped.pairs.map(pair => pair.evidenceSupport), [999_999, 1_000_000, 1_500_000]);
assert.deepEqual(capped.nucleotideSupport, [999_999, 1_000_000, 1_500_000, 0, 0, 1_500_000, 1_000_000, 999_999]);
assert.equal(capped.maximumNucleotideSupport, 1_500_000);

// The cap and endpoint count come from the generated catalog
// (support_kernels.COMRADES_SCORE), which the Python runtime also reads.
const scoreRule = globalThis.Hyb2SupportKernels.comradesScore;
assert.deepEqual({...scoreRule}, {endpointCap: 1_000_000, endpointsPerPair: 2});
assert.equal(comrades.scoreCapLabel(), "1,000,000");
globalThis.Hyb2SupportKernels.comradesScore = {endpointCap: 10, endpointsPerPair: 3};
assert.equal(comrades.scoreStructurePairs(boundaryPairs.slice(0, 1), [{one: 1, two: 8, count: 12}], 8).comradesScore, 30);
assert.equal(comrades.scoreCapLabel(), "10");
globalThis.Hyb2SupportKernels.comradesScore = scoreRule;

const many = Array.from({ length: 1005 }, function (_, index) {
  return { one: index + 1, two: 3000 - index, count: 1005 - index };
});
assert.equal(comrades.selectFragmentEvidence(many, 1, 3000).length, 1001,
  "the legacy awk printed<=1000 condition emits 1001 rows and is preserved intentionally");

process.stdout.write("comrades-analysis-contract: ok\n");
