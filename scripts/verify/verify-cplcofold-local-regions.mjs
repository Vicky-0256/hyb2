// Real local-region -> controller -> CPLcofold worker -> Pyodide integration.
// The Worker adapter supplies Node's missing browser message transport only;
// all sequence preparation, request construction and folding code is real.
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath, pathToFileURL } from "node:url";

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const workerPath = path.join(repository, "web", "cplfold.worker.mjs");
const plain = value => JSON.parse(JSON.stringify(value));
const nativeFetch = globalThis.fetch;
let receiver = null;
let active = false;
let messages = [];
let completedRuns = 0;
const workers = [];

globalThis.self = {
  location: { href: pathToFileURL(workerPath).href + "?build=local" },
  crypto: globalThis.crypto,
  postMessage(message) {
    messages.push(message);
    if (receiver && !receiver.terminated) receiver.onmessage({ data: message });
  }
};
globalThis.fetch = async function (input, options) {
  const url = new URL(typeof input === "string" ? input : input.url);
  if (url.protocol !== "file:") {
    throw new Error("This local integration test must not fetch a remote URL: " + url);
  }
  try {
    return new Response(fs.readFileSync(fileURLToPath(url)), { status: 200 });
  } catch (_error) {
    return new Response("Not found", { status: 404 });
  }
};
await import(pathToFileURL(workerPath).href + "?local-regions-integration=1");
assert.equal(typeof self.onmessage, "function");

async function dispatch(payload, target = null) {
  assert.equal(active, false, "the real worker runs one request at a time");
  active = true;
  receiver = target;
  messages = [];
  try {
    await self.onmessage({ data: payload });
    const failure = messages.find(message => message.type === "error");
    assert.equal(failure, undefined, failure && failure.message);
    const expected = payload.type === "cplfold-bonus-matrix" ? "bonus-matrix-complete" : "complete";
    const done = messages.find(message => message.type === expected);
    assert.ok(done, "the actual Pyodide worker must finish " + payload.type);
    assert.equal(done.result.engine, payload.cutpoint ? "CPLcofold" : "CPLfold");
    assert.equal(done.result.bridgeVersion, "8");
    completedRuns += 1;
    return done.result;
  } finally {
    receiver = null;
    active = false;
  }
}

class BrowserWorkerTransport {
  constructor(url) {
    assert.match(url, /cplfold\.worker\.mjs|structure\.worker\.js/);
    this.url = url;
    this.terminated = false;
    workers.push(this);
  }
  postMessage(message) {
    this.message = plain(message);
    // A sequence-only fold's ViennaRNA request is recorded, not folded:
    // verify-structure-worker.cjs runs that worker.
    this.pending = /cplfold\.worker\.mjs/.test(this.url) ? dispatch(this.message, this) : Promise.resolve();
  }
  terminate() { this.terminated = true; }
}

const context = vm.createContext({
  console,
  Worker: BrowserWorkerTransport,
  document: { querySelector: () => null, documentElement: { dataset: {} } },
  window: {}
});
for (const filename of ["support-kernels.js", "analysis-data.js", "comrades-analysis.js", "analysis-pages.js", "structure-controller.js"]) {
  vm.runInContext(fs.readFileSync(path.join(repository, "web", filename), "utf8"), context, { filename });
}
const { Hyb2Pages: pages, Hyb2Structure: controller, Hyb2Comrades: comrades } = context.window;
const api = { toasts: [], render() {}, showToast(message) { this.toasts.push(message); } };

function record(id, one, a, b, two, c, d) {
  return { id, rnaOne: one, rnaOneStart: a, rnaOneEnd: b, rnaTwo: two, rnaTwoStart: c, rnaTwoEnd: d };
}

function fixture(sameRna, guided, overrides = {}) {
  const first = sameRna ? "RNA_shared" : "RNA_A";
  const second = sameRna ? first : "RNA_B";
  const a = sameRna ? 1001 : 201;
  const b = sameRna ? 6005 : 301;
  const mapping = sameRna ? { [first]: "reference_shared" } : { [first]: "reference_A", [second]: "reference_B" };
  const sequences = sameRna
    ? [{ id: "reference_shared", sequence: "N".repeat(1000) + "GGGG" + "N".repeat(5000) + "CCCC" + "X".repeat(150) }]
    : [{ id: "reference_A", sequence: "N".repeat(200) + "GGGG" + "X".repeat(100) },
       { id: "reference_B", sequence: "X".repeat(300) + "CCCC" + "N".repeat(200) }];
  const records = [
    record("cross-forward", first, a, a + 3, second, b, b + 3),
    record("cross-reversed", second, b, b + 3, first, a, a + 3),
    record("within-A", first, a, a + 1, first, a + 2, a + 3),
    record("within-B", second, b, b + 1, second, b + 2, b + 3),
    record("partially-outside", first, a - 1, a + 1, second, b, b + 1),
    record("reverse-coordinates", first, a + 1, a, second, b, b + 1)
  ].map((item, index) => ({ ...item, index }));
  return {
    summary: { rnaNames: [...new Set([first, second])] }, records,
    reference: { mapping },
    view: { structureEvidence: { guided, rnas: [...new Set([first, second])], records,
                                 fasta: { mapping, sequences } } },
    structure: {
      engine: "cplfold", cplfoldMode: "cofold", source: "reference", workflow: sameRna ? "long-intramolecular" : "intermolecular",
      rna: first, start: String(a), end: String(a + 3), secondRna: second, secondStart: String(b), secondEnd: String(b + 3),
      cplfoldEvidence: guided ? "hyb-blocks" : "none", cplfoldBeam: "20", cplfoldMaxPhase1: "12",
      cplfoldEnergyDelta: "12", cplfoldEnergyModel: "DP09", cplfoldAlpha: "0", cplfoldBeta: "0",
      cplfoldAllowPseudoknot: false, status: "idle", result: null, viewMode: "folded", ...overrides
    }
  };
}

function assertSelection(state) {
  const info = plain(pages.getStructureSequence(state));
  assert.equal(info.error, "");
  assert.equal(info.sequence, "GGGGCCCC");
  assert.equal(info.displaySequence, "GGGG&CCCC");
  assert.equal(info.cutpoint, 4);
  assert.deepEqual(info.strandLengths, [4, 4]);
  assert.equal(info.assembly.linker, null);
  assert.equal(info.sequence.length, 8, "neither the long reference gap nor a synthetic linker is folded");
  assert.deepEqual(info.sourceSegments.map(s => [s.rna, s.referenceStart, s.referenceEnd, s.preparedStart, s.preparedEnd]), [
    [state.structure.rna, Number(state.structure.start), Number(state.structure.end), 1, 4],
    [state.structure.secondRna, Number(state.structure.secondStart), Number(state.structure.secondEnd), 5, 8]
  ]);
  if (state.structure.cplfoldEvidence === "hyb-blocks") {
    assert.deepEqual(info.assembly.evidenceArms.map(a => [a.oneStart, a.oneEnd, a.twoStart, a.twoEnd]),
                     [[1, 4, 5, 8], [1, 4, 5, 8], [1, 2, 3, 4], [5, 6, 7, 8]]);
    assert.deepEqual(info.assembly.evidenceArms.map(a => a.reversedFromHyb), [false, true, false, false]);
    assert.equal(info.assembly.skipped.outsideSelectedRegions, 1);
    assert.equal(info.assembly.skipped.antisense, 1);
  }
  return info;
}

async function runController(state) {
  const info = assertSelection(state);
  assert.equal(controller.predict(state, api), true, api.toasts.at(-1));
  const worker = workers.at(-1);
  assert.equal(worker.message.sequence, info.sequence);
  assert.equal(worker.message.cutpoint, info.cutpoint);
  assert.equal(worker.message.evidenceMode, state.structure.cplfoldEvidence);
  await worker.pending;
  assert.equal(state.structure.status, "complete", state.structure.message);
  const result = plain(state.structure.result);
  assert.deepEqual(result.sourceSegments, info.sourceSegments);
  assert.equal(result.source, "reference");
  assert.equal(result.assembly.linker, null);
  assert.equal(result.sequence, info.sequence);
  assert.equal(result.cutpoint, info.cutpoint);
  assert.equal(result.dotBracket.length, 8);
  assert.match(result.runtimeManifest.archiveSha256, /^[a-f0-9]{64}$/);
  for (const pair of result.pairs) {
    for (const side of ["left", "right"]) {
      const position = pair[side];
      const segment = result.sourceSegments.find(s => s.preparedStart <= position && position <= s.preparedEnd);
      assert.ok(segment, "every compact pair coordinate maps back to a selected reference window");
      const original = segment.referenceStart + position - segment.preparedStart;
      assert.ok(segment.referenceStart <= original && original <= segment.referenceEnd);
      const source = state.view.structureEvidence.fasta.sequences.find(s => s.id === segment.referenceId);
      assert.equal(source.sequence[original - 1], result.sequence[position - 1]);
      assert.equal(pair[side + "Base"], result.sequence[position - 1]);
    }
  }
  return result;
}

function expectedUpperBonus(arms) {
  const n = 8, cp = 4;
  const sigma = arms.reduce((sum, a) => sum + a.oneEnd - a.oneStart + 1 + a.twoEnd - a.twoStart + 1, 0) / (12 * arms.length);
  const support = Array.from({ length: n }, () => Array(n).fill(0));
  function vector(start, end) {
    const lower = start <= cp ? 0 : cp, upper = start <= cp ? cp : n;
    const values = Array.from({ length: n }, (_, i) => i < lower || i >= upper ? 0 : Math.exp(-0.5 * ((i - (start - 1 + end) / 2) / sigma) ** 2));
    const peak = Math.max(...values);
    return values.map(value => value / peak);
  }
  for (const arm of arms) {
    const one = vector(arm.oneStart, arm.oneEnd), two = vector(arm.twoStart, arm.twoEnd);
    for (let i = 0; i < n; i++) for (let j = 0; j < n; j++) {
      support[i][j] += one[i] * two[j];
      support[j][i] += one[i] * two[j];
    }
  }
  const expected = new Map();
  for (let i = 0; i < n; i++) for (let j = i + 1; j < n; j++) {
    if (support[i][j] >= 2 ** -23) expected.set(`${i + 1}:${j + 1}`, Math.log1p(support[i][j]));
  }
  return expected;
}

// The length policy of each sequence source and layout. A sequence-only fold
// is ViennaRNA MFE for one region and RNAcofold for two (analysis-data.js
// structureSteps): this form, saved before the three setup steps as
// sequence-only CPLfold, folds so.
function policyFixture(source, length, overrides = {}) {
  const mapping = {A: "A", B: "B"};
  const fasta = {mapping, sequences: [{id: "A", sequence: "g\u0085".repeat(length)}, {id: "B", sequence: "c".repeat(length)}]};
  const records = [{id: "read", index: 0, sequence: " g".repeat(length)}];
  return {
    summary: {rnaNames: ["A", "B"]}, records, reference: {mapping},
    view: {structureRecords: records, structureEvidence: {fasta, rnas: ["A", "B"], guided: false, records: []}},
    structure: {engine: "cplfold", source, cplfoldMode: "single", cplfoldEvidence: "none", workflow: "custom",
      pastedSequence: ">A\r1 " + "g".repeat(length), cplfoldSequenceTwo: " c".repeat(length), selectedRecordIndex: 0,
      rna: "A", start: "", end: "", secondRna: "B", secondStart: "1", secondEnd: String(length),
      cplfoldBeam: "20", cplfoldMaxPhase1: "1", cplfoldEnergyDelta: "0", cplfoldEnergyModel: "DP09",
      cplfoldAlpha: "0", cplfoldBeta: "0", cplfoldAllowPseudoknot: false, status: "idle", result: null, ...overrides}
  };
}

async function checkPolicy(name, state, sequence, issue) {
  const info = pages.getStructureSequence(state);
  if (issue) {
    const before = workers.length, completed = completedRuns;
    assert.match(info.error, issue, name);
    assert.equal(controller.predict(state, api), false, name);
    assert.deepEqual([workers.length, completedRuns, active], [before, completed, false], "no request for " + name);
    console.log(JSON.stringify({case: name, accepted: false, error: info.error}));
    return;
  }
  assert.equal(info.error, "", name);
  assert.equal(info.sequence, sequence, name);
  assert.equal(controller.predict(state, api), true, name + ": " + api.toasts.at(-1));
  // ViennaRNA's request: one sequence ("fold"), or strands A and B ("cofold")
  const request = state.structureWorker.message;
  assert.match(state.structureWorker.url, /structure\.worker\.js/, name);
  assert.deepEqual(request.type === "cofold" ? [request.sequenceA, request.sequenceB] : [request.sequence],
    info.cutpoint ? [sequence.slice(0, info.cutpoint), sequence.slice(info.cutpoint)] : [sequence], name);
  assert.equal(request.type, info.cutpoint ? "cofold" : "fold", name);
  controller.cancel(state);
  console.log(JSON.stringify({case: name, accepted: true, request: request.type, sequence, cutpoint: info.cutpoint ?? null}));
}

try {
  for (const length of [4, 5, 7]) {
    await checkPolicy(`paste ${length} nt`, policyFixture("paste", length), "G".repeat(length), length < 5 ? /at least 5 nt/ : null);
  }
  for (const source of ["record", "reference"]) for (const length of [0, 1]) {
    await checkPolicy(`${source} ${length} nt`, policyFixture(source, length), "G".repeat(length), length ? null : /at least 1 nt/);
  }
  const wholeReference = policyFixture("reference", 4);
  await checkPolicy("whole reference 4 nt with blank bounds", wholeReference, "GGGG");
  Object.assign(wholeReference.structure, {start: "1", end: "4"});
  await checkPolicy("explicit 1-4 window on the same 4 nt reference", wholeReference, null, /at least 10 nt/);
  for (const source of ["paste", "reference"]) for (const length of [1, 4]) {
    await checkPolicy(`${source} ${length}+${length} nt`, policyFixture(source, length, {cplfoldMode: "cofold", start: "1", end: String(length)}),
      "G".repeat(length) + "C".repeat(length));
  }
  await checkPolicy("empty second pasted strand", policyFixture("paste", 4, {cplfoldMode: "cofold", cplfoldSequenceTwo: "\u0085"}), null, /at least 1 nt/);
  for (const length of [9, 10]) {
    await checkPolicy(`single window ${length} nt`, policyFixture("reference", 12, {start: "2", end: String(length + 1)}),
      "G".repeat(length), length < 10 ? /at least 10 nt/ : null);
  }
  await checkPolicy("reversed single window", policyFixture("reference", 12, {start: "11", end: "2"}), null, /start must not be after the end/);
  for (const bounds of [{start: "4", end: "1"}, {start: "1", end: "4", secondStart: "4", secondEnd: "1"}]) {
    await checkPolicy("reversed two-region window " + JSON.stringify(bounds), policyFixture("reference", 4, {cplfoldMode: "cofold", ...bounds}),
      null, /start must not be after the end/);
  }
  const rules = context.Hyb2FoldText;
  const original = {minPastedNt: rules.minPastedNt, minSequenceNt: rules.minSequenceNt, minWindowNt: rules.minWindowNt};
  try {
    Object.assign(rules, {minPastedNt: 6, minSequenceNt: 3, minWindowNt: 11});
    await checkPolicy("changed paste minimum rejects 5", policyFixture("paste", 5), null, /at least 6 nt/);
    await checkPolicy("changed paste minimum accepts 6", policyFixture("paste", 6), "G".repeat(6));
    for (const source of ["record", "reference"]) {
      await checkPolicy(`changed sequence minimum rejects ${source} 1`, policyFixture(source, 1), null, /at least 3 nt/);
      await checkPolicy(`changed sequence minimum accepts ${source} 3`, policyFixture(source, 3), "GGG");
    }
    for (const source of ["paste", "reference"]) {
      await checkPolicy(`changed fragment minimum rejects ${source} 2+2`, policyFixture(source, 2, {cplfoldMode: "cofold", start: "1", end: "2"}), null, /at least 3 nt/);
      await checkPolicy(`changed fragment minimum accepts ${source} 3+3`, policyFixture(source, 3, {cplfoldMode: "cofold", start: "1", end: "3"}), "GGGCCC");
    }
    await checkPolicy("changed window minimum rejects 10", policyFixture("reference", 12, {start: "1", end: "10"}), null, /at least 11 nt/);
    await checkPolicy("changed window minimum accepts 11", policyFixture("reference", 12, {start: "1", end: "11"}), "G".repeat(11));
    Object.assign(rules, original, {minSequenceNt: 11});
    await checkPolicy("sequence minimum does not replace paste minimum", policyFixture("paste", 5), "G".repeat(5));
    await checkPolicy("sequence minimum does not replace window minimum", policyFixture("reference", 12, {start: "1", end: "10"}), "G".repeat(10));
    for (const source of ["record", "reference"]) {
      await checkPolicy(`raised sequence minimum rejects ${source} 10`, policyFixture(source, 10), null, /at least 11 nt/);
      await checkPolicy(`raised sequence minimum accepts ${source} 11`, policyFixture(source, 11), "G".repeat(11));
    }
    const prepared = await dispatch({type: "cplfold", sequence: "G", evidenceMode: "none", allowPseudoknot: false});
    assert.equal(prepared.sequence, "G", "prepared engine input does not acquire a task/form cutoff");
  } finally { Object.assign(rules, original); }

  // Both selected windows are valid even though their reference flanks and
  // the 5,000-nt gap contain unknown symbols. Those symbols keep their indices.
  // (alpha 0: the HYB rows do not change this fold.)
  const sameNoPk = await runController(fixture(true, true));
  assert.ok(Math.abs(sameNoPk.energy + 5.8) < 1e-9);
  assert.equal(sameNoPk.dotBracketWithBreak, "((((&))))");
  assert.ok(sameNoPk.candidates.every(c => c.type === "phase1" && c.crossingPairs === 0));

  // Invalid bases *inside* either window fail before starting a worker, for
  // CPLcofold (guided) and RNAcofold (sequence only).
  for (const same of [true, false]) for (const strand of [0, 1]) for (const guided of [true, false]) {
    const invalid = fixture(same, guided);
    const selected = assertSelection(invalid).sourceSegments[strand];
    const entry = invalid.view.structureEvidence.fasta.sequences.find(s => s.id === selected.referenceId);
    const index = selected.referenceStart;
    entry.sequence = entry.sequence.slice(0, index) + "N" + entry.sequence.slice(index + 1);
    const prepared = pages.getStructureSequence(invalid);
    assert.match(prepared.error, /Selected region .* contains unsupported symbols: N/);
    const before = workers.length;
    assert.equal(controller.predict(invalid, api), false);
    assert.equal(workers.length, before);
  }
  const legacy = fixture(false, true);
  const legacyRegions = [{ rna: "RNA_A", start: 201, end: 204 }, { rna: "RNA_B", start: 301, end: 304 }];
  for (const regions of [legacyRegions.slice(0, 1), legacyRegions]) {
    assert.throws(() => comrades.prepareReferenceAssembly(legacy.records, legacy.view.structureEvidence.fasta, { regions }),
                  /mapped FASTA sequence .* unsupported symbols/, "single/Vienna validation remains unchanged");
  }
  const cleanFasta = { sequences: [{ id: "A", sequence: "GGGG" }, { id: "B", sequence: "CCCC" }] };
  const oldAssembly = comrades.prepareReferenceAssembly([], cleanFasta, { regions: [{ rna: "A", start: 1, end: 4 }, { rna: "B", start: 1, end: 4 }] });
  assert.equal(oldAssembly.linker.length, 100, "the legacy two-region linker is unchanged");

  const zero = await runController(fixture(true, true, { cplfoldAllowPseudoknot: true }));
  const alpha = await runController(fixture(true, true, { cplfoldAllowPseudoknot: true, cplfoldAlpha: "0.5" }));
  const beta = await runController(fixture(true, true, { cplfoldAllowPseudoknot: true, cplfoldAlpha: "0.5", cplfoldBeta: "0.5" }));
  assert.ok(alpha.candidates.some(after => {
    const before = zero.candidates.find(candidate => candidate.dotBracket === after.dotBracket);
    return before && Math.abs(before.score - after.score) > 1e-6;
  }), "remapped HYB reads must change actual search scores when alpha changes");
  const pk = beta.candidates.filter(candidate => candidate.type === "pseudoknot");
  assert.ok(pk.length > 0, "the local-window test must actually return scored crossing candidates");
  let changedRanking = false;
  for (const candidate of beta.candidates) {
    const previous = alpha.candidates.find(other => other.dotBracket === candidate.dotBracket);
    assert.ok(previous, "beta does not create a new pair graph");
    assert.equal(candidate.energy, previous.energy, "beta preserves physical energy");
    const expected = candidate.energy + (candidate.type === "pseudoknot" ? 0.5 * candidate.phase1Energy : 0);
    assert.ok(Math.abs(candidate.effectiveEnergy - expected) < 1e-9);
    if (candidate.type === "pseudoknot") {
      assert.equal(candidate.energyMetadata.energy_kind, "heuristic");
      changedRanking ||= Math.abs(candidate.effectiveEnergy - previous.effectiveEnergy) > 1e-6;
    }
  }
  assert.ok(changedRanking, "beta must alter an actual PK ranking energy");

  const different = await runController(fixture(false, true, { cplfoldAlpha: "0.5" }));
  assert.ok(Math.abs(different.energy + 5.8) < 1e-9);
  assert.equal(different.evidence.inputRecords, 4);
  assert.equal(different.evidence.uniqueBlocks, 3, "reversed HYB orientation becomes the same weighted block");
  const expected = expectedUpperBonus(workers.at(-1).message.evidenceArms);
  assert.equal(different.evidence.bonusEntries.length, expected.size);
  for (const entry of different.evidence.bonusEntries) {
    assert.ok(Math.abs(entry.value - expected.get(`${entry.one}:${entry.two}`)) < 1e-12,
              "prepared reads must match an independent strand-bounded Gaussian calculation");
  }
  // An inter-strand-only export must have no within-strand Gaussian leakage.
  const matrix = await dispatch({ ...workers.at(-1).message, type: "cplfold-bonus-matrix",
                                 evidenceArms: workers.at(-1).message.evidenceArms.slice(0, 2) });
  assert.ok(matrix.evidence.bonusEntries.length > 0);
  assert.ok(matrix.evidence.bonusEntries.every(entry => entry.one <= 4 && entry.two > 4));
  assert.equal(matrix.sequence, "GGGGCCCC");
  assert.equal(matrix.cutpoint, 4);

  // A worker caller may supply C instead of estimating arm pairs. Neither an
  // empty table nor a bad table means "re-estimate". Q affects B but never C.
  for (const supportKernel of ["comrades", "duplex"]) for (const type of ["cplfold", "cplfold-bonus-matrix"]) {
    const inputs = {type, sequence: "GGGGCCCC", cutpoint: 4, supportKernel, allowPseudoknot: false, evidenceMode: "hyb-blocks",
      evidenceArms: [{oneStart: 1, oneEnd: 4, twoStart: 5, twoEnd: 8}], saturatingQ: 15};
    for (const [evidencePairs, q, raw, bonus, score] of [
      [[], 15, 0, 0, 0], [[[1, 8, 3], [8, 1, 2]], 15, 5, 0.9, 10],
      [[[1, 8, 1_500_000]], 1_500_000, 1_500_000, 1.8, 2_000_000]
    ]) {
      const res = await dispatch({...inputs, evidencePairs, saturatingQ: q});
      assert.equal(res.evidence.pairCounter, "host-supplied evidencePairs");
      assert.equal(res.evidence.saturatingQ, q);
      assert.ok(!messages.some(m => ["viennarna", "rna-cofold", "rna-duplex"].includes(m.phase)));
      const cells = res.evidence.bonusEntries;
      assert.equal(cells.length, evidencePairs.length ? 1 : 0);
      if (cells.length) {
        assert.deepEqual([cells[0].one, cells[0].two], [1, 8]);
        assert.ok(Math.abs(cells[0].value - bonus) < 1e-6, "B must use the uncapped count");
      }
      if (type === "cplfold") {
        assert.equal(res.comradesScore, score);
        for (const member of [res, ...res.candidates]) {
          const counts = member.pairs.map(p => p.left === 1 && p.right === 8 ? raw : 0);
          assert.deepEqual(member.comradesPairCounts, counts);
          assert.equal(member.comradesScore, counts.includes(raw) ? score : 0);
        }
      }
    }
    for (const [overrides, reason] of [[{evidencePairs: {}}, /evidencePairs must be an array/], [{evidencePairs: null}, /evidencePairs must be an array/],
      [{evidencePairs: [[1, 1, 1]]}, /pairs a position with itself/], [{evidencePairs: [], saturatingQ: -1}, /saturatingQ/]]) {
      messages = [];
      await self.onmessage({data: {...inputs, ...overrides}});
      assert.equal(messages.at(-1).type, "error");
      assert.match(messages.at(-1).message, reason);
      assert.ok(!messages.some(m => ["viennarna", "rna-cofold", "rna-duplex"].includes(m.phase)));
    }
  }

  // Arm 1 has a four-pair hairpin and a GGG tail. RNAcofold keeps the
  // hairpin, but only the tail's three cross-arm pairs contribute to C.
  const arm = {oneStart: 1, oneEnd: 15, twoStart: 19, twoEnd: 21};
  const reverse = {oneStart: 19, oneEnd: 21, twoStart: 1, twoEnd: 15};
  for (const cutpoint of [undefined, 18]) {
    const inputs = {sequence: "GGGGAAAACCCCGGGAAACCC", cutpoint, evidenceMode: "hyb-blocks", supportKernel: "comrades",
      evidenceArms: [arm, arm, reverse], allowPseudoknot: false};
    const exported = await dispatch({...inputs, type: "cplfold-bonus-matrix"});
    const keys = ["13:21", "14:20", "15:19"];
    assert.deepEqual(exported.evidence.bonusEntries.map(e => `${e.one}:${e.two}`), keys);
    assert.ok(exported.evidence.bonusEntries.every(e => Math.abs(e.value - 1.8) < 1e-6)); // B=3.6*3/(3+3), not C=3.
    assert.ok(messages.some(m => m.phase === "rna-cofold" && m.stage.includes("RNAcofold")));
    const folded = await dispatch({...inputs, type: "cplfold"});
    assert.equal(folded.evidence.supportKernel, "comrades");
    assert.match(folded.evidence.pairCounter, /RNAcofold/);
    assert.deepEqual(folded.evidence.bonusEntries, exported.evidence.bonusEntries);
    assert.ok(folded.comradesScore > 0);
    for (const member of [folded, ...folded.candidates]) {
      const expected = member.pairs.map(p => keys.includes(`${p.left}:${p.right}`) ? 3 : 0);
      assert.deepEqual(member.comradesPairCounts, expected);
      assert.equal(member.comradesScore, 2 * expected.reduce((sum, c) => sum + c, 0));
    }
    const progress = messages.filter(m => m.type === "progress").map(m => m.phase);
    assert.ok(progress.indexOf("rna-cofold") >= 0 && progress.indexOf("rna-cofold") < progress.indexOf("cplfold"));
    assert.ok(!progress.includes("rna-duplex"));
    // Removing the tail leaves no cross-arm pair under RNAcofold; RNAduplex
    // cannot form the hairpin and predicts three pairs on the same arms.
    for (const [supportKernel, expected] of [["comrades", 0], ["duplex", 3]]) {
      const withoutTail = await dispatch({...inputs, supportKernel, evidenceArms: [{...arm, oneEnd: 12}], type: "cplfold-bonus-matrix"});
      assert.equal(withoutTail.evidence.bonusEntries.length, expected);
    }
  }
  console.log(`PASS: ${completedRuns} real Pyodide requests; source/layout length policy, local windows, remapping, alpha/beta, ${pk.length} PK candidates, legacy validation, and both arm models.`);
} finally {
  globalThis.fetch = nativeFetch;
}
