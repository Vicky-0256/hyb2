import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const repository = path.resolve(scriptDirectory, "..");
const workerPath = path.join(repository, "web", "cplfold.worker.mjs");
const messages = [];
const nativeFetch = globalThis.fetch;

globalThis.self = {
  location: { href: pathToFileURL(workerPath).href + "?build=local" },
  crypto: globalThis.crypto,
  postMessage: function (message) { messages.push(message); }
};

// Node's Fetch implementation does not read file: URLs. The browser worker
// uses fetch for same-origin files, so provide only that narrow local adapter.
globalThis.fetch = async function (input, options) {
  const url = new URL(typeof input === "string" ? input : input.url);
  if (url.protocol !== "file:") {
    return nativeFetch(input, options);
  }
  try {
    const bytes = fs.readFileSync(fileURLToPath(url));
    return new Response(bytes, { status: 200 });
  } catch (error) {
    return new Response("Not found", { status: 404 });
  }
};

await import(pathToFileURL(workerPath).href + "?worker-contract=1");
assert.equal(typeof self.onmessage, "function");

await self.onmessage({
  data: {
    type: "cplfold",
    sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
    evidenceMode: "hyb-blocks",
    evidenceArms: [
      { oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 },
      { oneStart: 4, oneEnd: 9, twoStart: 19, twoEnd: 24 }
    ],
    beamSize: 20,
    maxPhase1: 2,
    energyDelta: 5,
    energyModel: "DP09",
    alpha: 0.5,
    beta: 0,
    allowPseudoknot: true
  }
});

const failure = messages.find(function (message) { return message.type === "error"; });
assert.equal(failure, undefined, failure && failure.message);
assert.ok(messages.some(function (message) { return message.type === "progress" && message.phase === "source"; }));
const completion = messages.find(function (message) { return message.type === "complete"; });
assert.ok(completion, "the actual worker message path must return a completed fold");
assert.equal(completion.result.bridgeVersion, "6");
assert.equal(completion.result.engineVersion, "24bab52");
assert.equal(completion.result.allowPseudoknot, true);
assert.equal(completion.result.dotBracket, "..(((((..[[[[)))))......]]]]");
assert.equal(completion.result.maxSequenceLength, 75);
assert.match(completion.result.runtimeManifest.archiveSha256, /^[a-f0-9]{64}$/);
assert.ok(completion.result.elapsedMs >= completion.result.foldElapsedMs);
assert.ok(completion.result.elapsedMs >= completion.result.runtimeLoadMs);

messages.length = 0;
await self.onmessage({
  data: {
    type: "cplfold-capacity",
    probeLength: 25,
    evidenceMode: "none",
    beamSize: 1,
    maxPhase1: 1,
    energyDelta: 0,
    energyModel: "DP09",
    alpha: 0,
    beta: 0,
    allowPseudoknot: true
  }
});
const capacityFailure = messages.find(function (message) { return message.type === "error"; });
assert.equal(capacityFailure, undefined, capacityFailure && capacityFailure.message);
const capacityCompletion = messages.find(function (message) { return message.type === "capacity-complete"; });
assert.ok(capacityCompletion, "the worker capacity probe must return a completed sample");
assert.equal(capacityCompletion.sample.length, 25);
assert.ok(capacityCompletion.sample.foldElapsedMs >= 0);
assert.equal(capacityCompletion.parameters.beamSize, 1);
assert.match(capacityCompletion.runtimeManifest.archiveSha256, /^[a-f0-9]{64}$/);
// One worker keeps its runtime: the probe reuses it, reports ready before the
// fold (the page starts its timeout then) and excludes the load from elapsedMs.
const readyIndex = messages.findIndex(function (message) { return message.type === "runtime-ready"; });
assert.ok(readyIndex >= 0, "the capacity probe must report when the runtime is ready");
assert.ok(readyIndex < messages.indexOf(capacityCompletion));
assert.ok(!messages.some(function (message) { return message.type === "progress" && message.phase === "source"; }),
  "a second request in the same worker must not reload the CPLfold source");
assert.ok(capacityCompletion.elapsedMs >= capacityCompletion.sample.foldElapsedMs);

messages.length = 0;
await self.onmessage({
  data: {
    type: "cplfold-bonus-matrix",
    sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
    evidenceMode: "hyb-blocks",
    evidenceArms: [
      { oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 }
    ]
  }
});
const matrixFailure = messages.find(function (message) { return message.type === "error"; });
assert.equal(matrixFailure, undefined, matrixFailure && matrixFailure.message);
const matrixCompletion = messages.find(function (message) { return message.type === "bonus-matrix-complete"; });
assert.ok(matrixCompletion, "the worker must export the HYB bonus matrix independently of a fold");
assert.equal(matrixCompletion.result.bridgeVersion, "6");
assert.equal(matrixCompletion.result.evidence.inputRecords, 1);
assert.ok(matrixCompletion.result.evidence.bonusEntries.length > 0);

messages.length = 0;
await self.onmessage({
  data: {
    type: "cplfold",
    sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG" + "GCAU".repeat(12),
    maxSequenceLength: 125,
    evidenceMode: "none",
    beamSize: 1,
    maxPhase1: 1,
    energyDelta: 0,
    energyModel: "DP09",
    alpha: 0,
    beta: 0,
    allowPseudoknot: true
  }
});
const extendedFailure = messages.find(function (message) { return message.type === "error"; });
assert.equal(extendedFailure, undefined, extendedFailure && extendedFailure.message);
const extendedCompletion = messages.find(function (message) { return message.type === "complete"; });
assert.ok(extendedCompletion, "the worker must execute a sequence above the baseline when the request is within the tested capacity");
assert.equal(extendedCompletion.result.sequence.length, 76);
assert.equal(extendedCompletion.result.maxSequenceLength, 125);

messages.length = 0;
await self.onmessage({
  data: {
    type: "cplfold",
    sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
    evidenceMode: "none",
    beamSize: 1,
    maxPhase1: 1,
    energyDelta: 0,
    energyModel: "DP09",
    alpha: 0,
    beta: 0.5,
    allowPseudoknot: false
  }
});
const pseudoknotFreeFailure = messages.find(function (message) { return message.type === "error"; });
assert.equal(pseudoknotFreeFailure, undefined, pseudoknotFreeFailure && pseudoknotFreeFailure.message);
const pseudoknotFreeCompletion = messages.find(function (message) { return message.type === "complete"; });
assert.ok(pseudoknotFreeCompletion, "the worker must complete a pseudoknot-free fold");
assert.equal(pseudoknotFreeCompletion.result.allowPseudoknot, false);
assert.equal(pseudoknotFreeCompletion.result.crossingPairs, 0);
assert.ok(pseudoknotFreeCompletion.result.candidates.every(function (candidate) {
  return candidate.type !== "pseudoknot" && candidate.crossingPairs === 0;
}));

// Exercise all supported nick encodings through the actual worker + Pyodide.
const cofoldParameters = {
  type: "cplfold", evidenceMode: "none", beamSize: 20, maxPhase1: 1,
  energyDelta: 0, energyModel: "DP09", alpha: 0, beta: 0,
  allowPseudoknot: false, maxSequenceLength: 8
};
const cofoldResults = [];
for (const input of [
  { sequence: "gggg & cccc" },
  { sequence: "GGGG", sequenceTwo: "CCCC" },
  { sequence: "GGGGCCCC", cutpoint: 4 }
]) {
  messages.length = 0;
  await self.onmessage({ data: { ...cofoldParameters, ...input } });
  const error = messages.find(message => message.type === "error");
  assert.equal(error, undefined, error && error.message);
  const result = messages.find(message => message.type === "complete").result;
  assert.equal(result.engine, "CPLcofold");
  assert.equal(result.cutpoint, 4);
  assert.deepEqual(result.strandLengths, [4, 4]);
  assert.equal(result.sequence, "GGGGCCCC");
  assert.equal(result.displaySequence, "GGGG&CCCC");
  assert.equal(result.dotBracket.length, 8);
  assert.equal(result.dotBracketWithBreak[4], "&");
  assert.ok(Number.isFinite(result.energy));
  assert.ok(result.pairs.length > 0);
  assert.ok(result.pairs.every(pair => pair.left <= 4 && pair.right > 4 && pair.intermolecular));
  cofoldResults.push([result.dotBracket, result.energy, result.pairs]);
}
assert.deepEqual(cofoldResults[0], cofoldResults[1]);
assert.deepEqual(cofoldResults[0], cofoldResults[2]);

for (const [input, errorPattern] of [
  [{ sequence: "GGGG&CCCC", maxSequenceLength: 7 }, /limited to 7 nt/],
  [{ sequence: "GGGG&" }, /two nonempty/],
  [{ sequence: "GG&CC&AA" }, /two nonempty/],
  [{ sequence: "GGGG&CCCC", cutpoint: 3 }, /does not match/],
  [{ sequence: "GGGGCCCC", cutpoint: true }, /cutpoint must/],
  [{ sequence: "GGGGCCCC", cutpoint: "4" }, /cutpoint must/],
  [{ sequence: "GGGG&CCCC", cofold: "true" }, /cofold must be a boolean/],
  [{ sequence: "GGGGCCCC", cofold: true }, /two RNA strands/],
  [{ sequence: "GGGG", sequenceTwo: "" }, /two nonempty/],
  [{ sequence: "GGGG&CCCC", energyModel: "CC09" }, /DP03.*DP09/]
]) {
  messages.length = 0;
  await self.onmessage({ data: { ...cofoldParameters, ...input } });
  const error = messages.find(message => message.type === "error");
  assert.ok(error, JSON.stringify(input) + " must fail");
  assert.match(error.message, errorPattern);
  assert.ok(!messages.some(message => message.type === "complete"));
}

messages.length = 0;
await self.onmessage({ data: {
  type: "cplfold-bonus-matrix", sequence: "GGGG&CCCC",
  evidenceMode: "hyb-blocks",
  evidenceArms: [{ oneStart: 3, oneEnd: 4, twoStart: 5, twoEnd: 6 }]
} });
assert.equal(messages.find(message => message.type === "error"), undefined);
const cofoldMatrix = messages.find(message => message.type === "bonus-matrix-complete").result;
assert.equal(cofoldMatrix.engine, "CPLcofold");
assert.equal(cofoldMatrix.cutpoint, 4);
assert.ok(cofoldMatrix.evidence.bonusEntries.length > 0);
assert.ok(cofoldMatrix.evidence.bonusEntries.every(entry => entry.one <= 4 && entry.two > 4),
  "intermolecular reads must never leak into either intramolecular block");

messages.length = 0;
await self.onmessage({ data: {
  ...cofoldParameters, type: "cplfold-capacity", cofold: true, probeLength: 25
} });
assert.equal(messages.find(message => message.type === "error"), undefined);
const cofoldCapacity = messages.find(message => message.type === "capacity-complete");
assert.ok(cofoldCapacity);
assert.equal(cofoldCapacity.sample.length, 25);
assert.equal(cofoldCapacity.parameters.cofold, true);

// HYB binding energies: the batch message folds each arm pair as two strands;
// on this ZIKV pair nested CPLcofold matches RNAcofold 2.6.4 (-37.00).
messages.length = 0;
await self.onmessage({ data: {
  type: "cplfold-energy-batch", allowPseudoknot: false, energyModel: "DP09",
  pairs: [
    ["AGAGACUUCGUGGAGGGCAUGUCAGGUGGGACCUGGGUUGAUGUUGUCUUGGAACAUGGAGG", "ACCCUGCAGGGACCUCAGGAUCUCCGAUCCUAGACAAAUGUGGAAGAGUGAUAGGACUCU"],
    ["GGGGAAAC", "GUUUCCCC"]
  ]
} });
assert.equal(messages.find(message => message.type === "error"), undefined);
const energyBatch = messages.find(message => message.type === "result");
assert.ok(energyBatch, "the energy batch replies with a result");
assert.equal(energyBatch.energies.length, 2);
assert.equal(energyBatch.energies[0], -37);
assert.ok(energyBatch.energies[1] < 0, "complementary strands bind");
assert.match(energyBatch.version, /^CPLfold /);
messages.length = 0;
await self.onmessage({ data: { type: "cplfold-energy-batch", pairs: [["ACGN", "GCU"]] } });
assert.ok(messages.find(message => message.type === "error"), "invalid bases fail the batch");

globalThis.fetch = nativeFetch;
delete globalThis.self;
process.stdout.write("cplfold-worker-contract: ok\n");
