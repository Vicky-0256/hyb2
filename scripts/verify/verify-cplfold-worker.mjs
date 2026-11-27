import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const repository = path.resolve(scriptDirectory, "..", "..");
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
// Any other URL is refused and recorded: these tests run offline, and in Node
// Pyodide falls back to downloading a package it cannot read locally (from
// its cdnUrl, the jsDelivr CDN unless packageBaseUrl is set), so a regression
// must fail here rather than download.
const networkRequests = [];
globalThis.fetch = async function (input, options) {
  const url = new URL(typeof input === "string" ? input : input.url);
  if (url.protocol !== "file:") {
    networkRequests.push(url.href);
    throw new TypeError("verify-cplfold-worker blocks network access: " + url.href);
  }
  try {
    const bytes = fs.readFileSync(fileURLToPath(url));
    return new Response(bytes, { status: 200 });
  } catch (error) {
    return new Response("Not found", { status: 404 });
  }
};

const workerModule = await import(pathToFileURL(workerPath).href + "?worker-contract=1");
assert.equal(typeof self.onmessage, "function");

// An older page can still send the retired kernel (with or without its own
// pairs). Reject before loading Pyodide, not after computing a different B_ij.
for (const type of ["cplfold", "cplfold-bonus-matrix", "cplfold-capacity"]) {
  for (const sequence of ["GGGAAACCC", "GGG&CCC"]) {
    await self.onmessage({ data: { type, sequence, supportKernel: "rnacofold", evidenceMode: "hyb-blocks",
      evidenceArms: [{ oneStart: 1, oneEnd: 3, twoStart: sequence.includes("&") ? 4 : 7, twoEnd: sequence.includes("&") ? 6 : 9 }],
      ...(sequence.includes("&") ? { evidencePairs: [[1, 6, 1]] } : {}) } });
    assert.deepEqual(messages.map(message => message.type), ["error"], type + " must reject the removed kernel without running");
    assert.match(messages[0].message, /support.*kernel/i);
    messages.length = 0;
  }
}

// The Pyodide options: over http(s) a deployed build cache-busts the lock and
// keeps the wheels resolving against the runtime directory; a local build and
// file: (these Node tests) keep Pyodide's default lock location.
{
  const { pyodideOptionsFor } = workerModule;
  const runtimeUrl = "https://example.org/hyb2/vendor/pyodide-cplfold/";
  assert.deepEqual(pyodideOptionsFor(runtimeUrl, "abc123"), {
    indexURL: runtimeUrl,
    lockFileURL: runtimeUrl + "pyodide-lock.json?build=abc123",
    packageBaseUrl: runtimeUrl
  });
  assert.deepEqual(pyodideOptionsFor("http://localhost:8769/vendor/pyodide-cplfold/", "abc123").lockFileURL,
    "http://localhost:8769/vendor/pyodide-cplfold/pyodide-lock.json?build=abc123");
  assert.deepEqual(pyodideOptionsFor(runtimeUrl, "local"), { indexURL: runtimeUrl });
  // file: always pins packageBaseUrl to the local directory (a decoded path
  // with a trailing slash), so Node never falls back to the CDN.
  for (const build of ["abc123", "local", ""]) {
    assert.deepEqual(pyodideOptionsFor("file:///opt/site%20copy/vendor/pyodide-cplfold/", build),
      { indexURL: "/opt/site copy/vendor/pyodide-cplfold/", packageBaseUrl: "/opt/site copy/vendor/pyodide-cplfold/" });
  }
  assert.deepEqual(pyodideOptionsFor("file:///opt/runtime", "local"),
    { indexURL: "/opt/runtime/", packageBaseUrl: "/opt/runtime/" });
}

// Failure messages: the package, the first real error line, and the advice.
{
  const { packageFailureMessage } = workerModule;
  // A non-OK response: missing file or a server error during a deploy.
  const missing = packageFailureMessage("viennarna", [
    "The following error occurred while loading viennarna:",
    "Failed to load 'https://example.org/vendor/pyodide-cplfold/viennarna-0123456789abcdef/viennarna.whl': request failed."
  ]);
  assert.match(missing, /viennarna/);
  assert.match(missing, /request failed\. The file could not be downloaded \(missing or server error\)/);
  assert.doesNotMatch(missing, /\.\./, "a trailing period in the detail must not double up");
  assert.match(missing, /build-vienna-pyodide\.sh/);
  assert.match(missing, /reload the page/);
  assert.doesNotMatch(missing, /missing from this site/);
  // The lock has no entry for the package: also a build problem.
  const unlisted = packageFailureMessage("viennarna", ["Error: No known package with name 'viennarna'"]);
  assert.match(unlisted, /No known package with name 'viennarna'\. This site's pyodide-lock\.json does not list it/);
  assert.match(unlisted, /build-vienna-pyodide\.sh, then scripts\/build\/build-cplfold-web\.sh/);
  const unlistedNumpy = packageFailureMessage("numpy", ["No known package with name 'numpy'"]);
  assert.match(unlistedNumpy, /run scripts\/build\/build-cplfold-web\.sh;/);
  assert.doesNotMatch(unlistedNumpy, /build-vienna-pyodide/);
  // Real integrity/digest errors (Node, or a browser that names them).
  const integrity = packageFailureMessage("viennarna", [
    "The following error occurred while loading viennarna:",
    "Failed to find a valid digest in the 'integrity' attribute for resource 'viennarna.whl'"
  ]);
  assert.match(integrity, /integrity/);
  assert.match(integrity, /inconsistent/);
  assert.match(integrity, /reload the page/);
  assert.doesNotMatch(integrity, /build-vienna-pyodide/);
  // Browsers report an SRI mismatch of fetch(url, {integrity}) as the same
  // generic TypeError as a network failure: Chrome, Firefox, Safari.
  for (const [error, text] of [
    ["TypeError: Failed to fetch", "Failed to fetch"],
    ["TypeError: NetworkError when attempting to fetch resource.", "NetworkError when attempting to fetch resource"],
    ["TypeError: Load failed", "Load failed"]
  ]) {
    const network = packageFailureMessage("viennarna", ["The following error occurred while loading viennarna:", error]);
    assert.equal(network, "The local ViennaRNA Python module (viennarna) could not be loaded: " + text
      + ". A network problem or inconsistent deployed files (a partial or ongoing deploy): reload the page.");
  }
  const other = packageFailureMessage("numpy", ["RuntimeError: something else."]);
  assert.match(other, /numpy could not be loaded: something else\. Check the connection/);
  assert.doesNotMatch(other, /\n/);
  assert.match(packageFailureMessage("numpy", ["x".repeat(300)]), /x\.\.\. Check/, "a truncated detail keeps its ellipsis");
  assert.match(packageFailureMessage("numpy", []), /numpy could not be loaded: Pyodide did not report it as loaded/);
}

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
assert.equal(completion.result.bridgeVersion, "8");
assert.equal(completion.result.engineVersion, "24bab52");
assert.equal(completion.result.allowPseudoknot, true);
assert.equal(completion.result.evidence.supportKernel, "gaussian");
assert.equal(completion.result.dotBracket, "..(((((..[[[[)))))......]]]]");
assert.equal(completion.result.maxSequenceLength, 75);
assert.match(completion.result.runtimeManifest.archiveSha256, /^[a-f0-9]{64}$/);
assert.ok(completion.result.elapsedMs >= completion.result.foldElapsedMs);
assert.ok(completion.result.elapsedMs >= completion.result.runtimeLoadMs);

// The duplex read support loads ViennaRNA's Python module on first use and
// computes the pairs in the worker; no evidencePairs are sent.
messages.length = 0;
await self.onmessage({
  data: {
    type: "cplfold-bonus-matrix",
    sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
    evidenceMode: "hyb-blocks",
    supportKernel: "duplex",
    evidenceArms: [
      { oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 },
      { oneStart: 4, oneEnd: 9, twoStart: 19, twoEnd: 24 },
      { oneStart: 1, oneEnd: 10, twoStart: 15, twoEnd: 28 }
    ]
  }
});
const duplexFailure = messages.find(function (message) { return message.type === "error"; });
assert.equal(duplexFailure, undefined, duplexFailure && duplexFailure.message);
assert.ok(messages.some(function (message) { return message.type === "progress" && message.phase === "viennarna"; }));
assert.ok(messages.some(function (message) {
  return message.type === "progress" && message.phase === "rna-duplex" && message.completed === 3 && message.total === 3;
}), "the duplex step reports its blocks");
const duplexExport = messages.find(function (message) { return message.type === "bonus-matrix-complete"; });
assert.equal(duplexExport.result.evidence.supportKernel, "duplex");
assert.equal(duplexExport.result.evidence.duplexesWithPairs, 3);
assert.equal(duplexExport.result.evidence.bonusEntries.length, 5);

messages.length = 0;
await self.onmessage({
  data: {
    type: "cplfold",
    sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
    evidenceMode: "hyb-blocks",
    supportKernel: "duplex",
    evidenceArms: [
      { oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 },
      { oneStart: 4, oneEnd: 9, twoStart: 19, twoEnd: 24 }
    ],
    beamSize: 20, maxPhase1: 2, energyDelta: 5, energyModel: "DP09", alpha: 0.5, beta: 0, allowPseudoknot: true
  }
});
const duplexFoldFailure = messages.find(function (message) { return message.type === "error"; });
assert.equal(duplexFoldFailure, undefined, duplexFoldFailure && duplexFoldFailure.message);
const stages = messages.filter(function (message) { return message.type === "progress"; }).map(function (message) { return message.phase; });
assert.ok(stages.indexOf("rna-duplex") !== -1 && stages.indexOf("rna-duplex") < stages.indexOf("cplfold"),
  "RNAduplex progress comes before the fold stage: " + stages.join(","));
assert.equal(messages.find(function (message) { return message.type === "complete"; }).result.evidence.supportKernel, "duplex");

// The fold stage is posted once: by the pair callback after the last block,
// and otherwise (no block reached the callback) once fold_json returns.
assert.equal(stages.filter(function (phase) { return phase === "cplfold"; }).length, 1, stages.join(","));

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
assert.equal(matrixCompletion.result.bridgeVersion, "8");
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

// Two nested candidates of this ZIKV window tie at -14.30 kcal/mol. With
// pseudoknots on, CPLfold ranks one-strand candidates by their HotKnots
// energy, which adds its loops' energies left to right on every Python
// version, so the tie breaks as on the desktop (Python 3.11). Python 3.12's
// compensated sum() gave Pyodide a last bit that ranked the other first.
messages.length = 0;
await self.onmessage({
  data: {
    type: "cplfold",
    sequence: "GUGCUGGUAGUCAUGAUCUUGGGAGGAUUUUCAAUGAGUGACCUGGCCAAGCUUGUGAUCCUGAUGGGUGCUACUUUCGCAGAAAUGAACACUGGAGGAG",
    maxSequenceLength: 100, evidenceMode: "none", beamSize: 100, maxPhase1: 10, energyDelta: 5,
    energyModel: "DP09", alpha: 0, beta: 0, allowPseudoknot: true
  }
});
assert.equal(messages.find(message => message.type === "error"), undefined);
assert.equal(messages.find(message => message.type === "complete").result.dotBracket,
  "...(((((.(((((((.(((((.(((...(((...)))...)))..))))).)))))))((....)).(((.......))).........))))).....",
  "tied candidates rank as on the desktop");

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

const originalAlphabet = globalThis.Hyb2FoldText.alphabet;
try {
  globalThis.Hyb2FoldText.alphabet = "ACG";
  for (const input of [{ sequence: "GGGU&CCCC" }, { sequence: "GGGG", sequenceTwo: "CCCt" }]) {
    messages.length = 0;
    await self.onmessage({ data: { ...cofoldParameters, ...input } });
    assert.deepEqual(messages.map(message => message.type), ["error"], "reject excluded bases before folding");
    assert.match(messages[0].message, /U/);
  }
  messages.length = 0;
  await self.onmessage({ data: { ...cofoldParameters, sequence: "ggg & ccccc" } });
  const result = messages.find(message => message.type === "complete").result;
  assert.equal(result.sequence, "GGGCCCCC");
  assert.equal(result.cutpoint, 3);
} finally {
  globalThis.Hyb2FoldText.alphabet = originalAlphabet;
}

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
// The third pair needs the batch's beam 200 to reach RNAcofold's -15.1 (beam
// 20 gives -12.2).
messages.length = 0;
await self.onmessage({ data: {
  type: "cplfold-energy-batch", allowPseudoknot: false, energyModel: "DP09",
  pairs: [
    ["AGAGACUUCGUGGAGGGCAUGUCAGGUGGGACCUGGGUUGAUGUUGUCUUGGAACAUGGAGG", "ACCCUGCAGGGACCUCAGGAUCUCCGAUCCUAGACAAAUGUGGAAGAGUGAUAGGACUCU"],
    ["GGGGAAAC", "GUUUCCCC"],
    ["GUAACCCUUGGGGCCAGUGCAUGGCUCAUG", "UCAUAUUCCUUGUGCACUGCGGCAUU"]
  ]
} });
assert.equal(messages.find(message => message.type === "error"), undefined);
const energyBatch = messages.find(message => message.type === "result");
assert.ok(energyBatch, "the energy batch replies with a result");
assert.equal(energyBatch.energies.length, 3);
assert.equal(energyBatch.energies[0], -37);
assert.ok(energyBatch.energies[1] < 0, "complementary strands bind");
assert.equal(energyBatch.energies[2], -15.1, "the batch searches with beam 200");
assert.match(energyBatch.version, /^CPLfold /);
// With pseudoknots the batch's three candidates matter, as on the desktop: on
// this pair one candidate (the RNA Structure defaults) gives -4.1.
messages.length = 0;
await self.onmessage({ data: {
  type: "cplfold-energy-batch", allowPseudoknot: true, energyModel: "DP09",
  pairs: [["CAUGGAUGGCCCCAG", "AAUGUGGUAGGACU"]]
} });
assert.equal(messages.find(message => message.type === "error"), undefined);
assert.deepEqual(Array.from(messages.find(message => message.type === "result").energies), [-5.92],
  "the batch keeps its own preset, not the structure defaults");
// This ZIKV arm pair's pseudoknotted terms add up to exactly -35.455, a half
// cent. CPLfold adds them left to right on every Python version, which gives
// a double just past the half: -35.46, the desktop's value. Python 3.12's
// compensated sum() gave Pyodide a double just short of it, and -35.45.
messages.length = 0;
await self.onmessage({ data: {
  type: "cplfold-energy-batch", allowPseudoknot: true, energyModel: "DP09",
  pairs: [["UUAUGACUGCCACACCACCAGGAACCCGUGAUGCGUUUCCUGACU", "CUGAAGAAGAAGCAGUUAACUGUCUUGGAUCUGCAUCCAGGAGCCGGAAA"]]
} });
assert.equal(messages.find(message => message.type === "error"), undefined);
assert.deepEqual(Array.from(messages.find(message => message.type === "result").energies), [-35.46],
  "a half-cent total rounds as on the desktop");
messages.length = 0;
await self.onmessage({ data: { type: "cplfold-energy-batch", pairs: [["ACGN", "GCU"]] } });
assert.ok(messages.find(message => message.type === "error"), "invalid bases fail the batch");
// Batch energies round as the desktop's Python round(energy, 2): from the
// double itself, so a score just past a half cent stays past it (rounding
// energy * 100 gives -40.4 and -46.99 for the first two), and a double
// exactly halfway goes to the even digit.
assert.deepEqual([-40.405, -46.995000000000005, -40.125, 0.625, 0.375, 2.675, -37].map(workerModule.roundEnergy),
  [-40.41, -47, -40.12, 0.62, 0.38, 2.67, -37]);

// A failed ViennaRNA load: Pyodide resolves loadPackage without the package,
// so the worker must say which package failed and why, and a retry after the
// files are fixed must succeed.  Runs on a copy of the runtime; web/vendor is
// untouched.  This exercises Node's own loader (fs reads, then Pyodide's
// unpack of the wheel), which skips the lock's SHA-256 integrity check that
// a browser fetch applies; the browser's wording for that is covered by the
// packageFailureMessage unit tests above.
//  1. Wheel missing (in the lock, absent on disk): Pyodide in Node would retry
//     it from cdnUrl; with packageBaseUrl pinned to the runtime directory that
//     retry stays local, so the load fails offline with no http(s) request
//     (the fetch guard above refuses and records any).
//  2. Wheel truncated (an interrupted or partial deploy): "not a zip file".
//  3. Wheel restored: the retry succeeds in the same worker.
{
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "hyb2-cplfold-retry-"));
  try {
    const runtimeSource = path.join(repository, "web", "vendor", "pyodide-cplfold");
    const runtimeCopy = path.join(scratch, "web", "vendor", "pyodide-cplfold");
    fs.cpSync(runtimeSource, runtimeCopy, { recursive: true });
    const workerCopy = path.join(scratch, "web", "cplfold.worker.mjs");
    fs.copyFileSync(workerPath, workerCopy);
    fs.copyFileSync(path.join(repository, "web", "support-kernels.js"), path.join(scratch, "web", "support-kernels.js"));
    const lock = JSON.parse(fs.readFileSync(path.join(runtimeCopy, "pyodide-lock.json"), "utf8"));
    const wheel = path.join(runtimeCopy, lock.packages.viennarna.file_name);
    const goodWheel = fs.readFileSync(wheel);
    fs.rmSync(wheel);

    self.location.href = pathToFileURL(workerCopy).href + "?build=local";
    await import(pathToFileURL(workerCopy).href + "?worker-retry=1");
    const duplexRequest = {
      type: "cplfold-bonus-matrix",
      sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
      evidenceMode: "hyb-blocks",
      supportKernel: "duplex",
      evidenceArms: [
        { oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 },
        { oneStart: 4, oneEnd: 9, twoStart: 19, twoEnd: 24 },
        { oneStart: 1, oneEnd: 10, twoStart: 15, twoEnd: 28 }
      ]
    };
    networkRequests.length = 0;
    messages.length = 0;
    await self.onmessage({ data: duplexRequest });
    assert.deepEqual(networkRequests, [], "a missing local wheel must not be fetched from the network");
    const missingFailure = messages.find(message => message.type === "error");
    assert.ok(missingFailure, "a missing ViennaRNA wheel must fail the request");
    assert.match(missingFailure.message, /ViennaRNA Python module \(viennarna\) could not be loaded/);
    assert.match(missingFailure.message, /ENOENT|no such file/i);
    assert.match(missingFailure.message, /could not be downloaded \(missing or server error\)/);
    assert.match(missingFailure.message, /build-vienna-pyodide\.sh/);
    assert.ok(!messages.some(message => message.type === "bonus-matrix-complete"));

    fs.writeFileSync(wheel, goodWheel.subarray(0, 100000));
    messages.length = 0;
    await self.onmessage({ data: duplexRequest });
    const loadFailure = messages.find(message => message.type === "error");
    assert.ok(loadFailure, "a truncated ViennaRNA wheel must fail the request");
    assert.match(loadFailure.message, /ViennaRNA Python module \(viennarna\) could not be loaded/);
    assert.match(loadFailure.message, /is not a zip file/);
    assert.match(loadFailure.message, /inconsistent.*reload the page/);
    assert.doesNotMatch(loadFailure.message, /build-vienna-pyodide/);
    assert.ok(!messages.some(message => message.type === "bonus-matrix-complete"));

    fs.writeFileSync(wheel, goodWheel);
    messages.length = 0;
    await self.onmessage({ data: duplexRequest });
    const retryFailure = messages.find(message => message.type === "error");
    assert.equal(retryFailure, undefined, retryFailure && retryFailure.message);
    const retried = messages.find(message => message.type === "bonus-matrix-complete");
    assert.ok(retried, "the retry after restoring the wheel must succeed");
    assert.equal(retried.result.evidence.duplexesWithPairs, 3);
  } finally {
    fs.rmSync(scratch, { recursive: true, force: true });
  }
}
assert.deepEqual(networkRequests, [], "the worker tests must not touch the network");

globalThis.fetch = nativeFetch;
delete globalThis.self;
process.stdout.write("cplfold-worker-contract: ok\n");
