// Real local-region -> controller -> CPLcofold worker -> Pyodide integration.
// The Worker adapter supplies Node's missing browser message transport only;
// all sequence preparation, request construction and folding code is real.
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath, pathToFileURL } from "node:url";

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
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
    assert.equal(done.result.engine, "CPLcofold");
    assert.equal(done.result.bridgeVersion, "6");
    completedRuns += 1;
    return done.result;
  } finally {
    receiver = null;
    active = false;
  }
}

class BrowserWorkerTransport {
  constructor(url) {
    assert.match(url, /cplfold\.worker\.mjs/);
    this.url = url;
    this.terminated = false;
    workers.push(this);
  }
  postMessage(message) {
    this.message = plain(message);
    this.pending = dispatch(this.message, this);
  }
  terminate() { this.terminated = true; }
}

const context = vm.createContext({
  console,
  Worker: BrowserWorkerTransport,
  document: { querySelector: () => null, documentElement: { dataset: {} } },
  window: { Hyb2Data: { normaliseFoldSequence(value) {
    const sequence = String(value || "").replace(/\s+/g, "").toUpperCase().replace(/T/g, "U");
    return { sequence, invalid: [...new Set(sequence.match(/[^ACGU]/g) || [])] };
  } } }
});
for (const filename of ["comrades-analysis.js", "analysis-pages.js", "structure-controller.js"]) {
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
    if (support[i][j] >= 1e-6) expected.set(`${i + 1}:${j + 1}`, Math.fround(Math.log1p(support[i][j])));
  }
  return expected;
}

try {
  // Both selected windows are valid even though their reference flanks and
  // the 5,000-nt gap contain unknown symbols. Those symbols keep their indices.
  const sameNoPk = await runController(fixture(true, false));
  assert.ok(Math.abs(sameNoPk.energy + 5.8) < 1e-9);
  assert.equal(sameNoPk.dotBracketWithBreak, "((((&))))");
  assert.ok(sameNoPk.candidates.every(c => c.type === "phase1" && c.crossingPairs === 0));

  // Invalid bases *inside* either window fail before starting a worker.
  for (const same of [true, false]) for (const strand of [0, 1]) {
    const invalid = fixture(same, false);
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
    assert.ok(Math.abs(entry.value - expected.get(`${entry.one}:${entry.two}`)) < 1e-7,
              "prepared reads must match an independent strand-bounded Gaussian calculation");
  }
  // An inter-strand-only export must have no within-strand Gaussian leakage.
  const matrix = await dispatch({ ...workers.at(-1).message, type: "cplfold-bonus-matrix",
                                 evidenceArms: workers.at(-1).message.evidenceArms.slice(0, 2) });
  assert.ok(matrix.evidence.bonusEntries.length > 0);
  assert.ok(matrix.evidence.bonusEntries.every(entry => entry.one <= 4 && entry.two > 4));
  assert.equal(matrix.sequence, "GGGGCCCC");
  assert.equal(matrix.cutpoint, 4);

  console.log(`PASS: ${completedRuns} real Pyodide requests; distant same-RNA and different-RNA windows, compact source coordinates, read remapping, alpha/beta, ${pk.length} PK candidates, and legacy validation.`);
} finally {
  globalThis.fetch = nativeFetch;
}
