import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const repository = path.resolve(scriptDirectory, "..", "..");
const runtimeDirectory = path.join(repository, "web", "vendor", "pyodide-cplfold");

[
  "pyodide.mjs",
  "pyodide.asm.js",
  "pyodide.asm.wasm",
  "python_stdlib.zip",
  "pyodide-lock.json",
  "numpy-2.2.5-cp313-cp313-pyemscripten_2025_0_wasm32.whl",
  "build-manifest.json",
  "Pyodide-LICENSE.txt",
  "CPLfold-LICENSE.txt",
  "CPLfold-THIRD_PARTY_NOTICES.md",
  "CPLfold-GPL-3.0-or-later.txt",
  "CPLfold-GPL-2.0-or-later.txt",
  "CPLfold-LinearFold-LICENSE.txt"
].forEach(function (name) {
  assert.ok(fs.statSync(path.join(runtimeDirectory, name)).size > 0, name + " must be built before the smoke test");
});

const manifest = JSON.parse(fs.readFileSync(path.join(runtimeDirectory, "build-manifest.json"), "utf8"));
assert.ok(manifest.assets && Object.keys(manifest.assets).length >= 11, "build must record all shipped asset hashes");
for (const [filename, digest] of Object.entries(manifest.assets)) {
  // Single file names, plus the one hash-named directory of the ViennaRNA wheel.
  assert.match(filename, /^(?:viennarna-[a-f0-9]{16}\/)?[A-Za-z0-9_][A-Za-z0-9_.-]*$/,
    "asset paths must be single file names or viennarna-<hash>/<wheel>");
  assert.ok(!filename.split("/").includes(".."), filename + " must stay inside the runtime directory");
  assert.match(digest, /^[a-f0-9]{64}$/);
  assert.equal(createHash("sha256").update(fs.readFileSync(path.join(runtimeDirectory, filename))).digest("hex"), digest, filename + " hash mismatch");
}
assert.equal(manifest.pyodide, "0.29.4");
assert.equal(manifest.python, "3.13.2");
assert.equal(manifest.numpy, "2.2.5");
assert.equal(manifest.cplfoldRevision, "24bab52");
assert.equal(manifest.bridgeVersion, "8");
assert.match(manifest.cplfoldArchiveFile, /^cplfold-python-[a-f0-9]{64}\.zip$/);
const archivePath = path.join(runtimeDirectory, manifest.cplfoldArchiveFile);
assert.ok(fs.statSync(archivePath).size > 0);
const archiveBytes = fs.readFileSync(archivePath);
assert.equal(createHash("sha256").update(archiveBytes).digest("hex"), manifest.cplfoldArchiveSha256);

const moduleUrl = pathToFileURL(path.join(runtimeDirectory, "pyodide.mjs")).href;
// Offline: in Node, Pyodide retries a package file it cannot read locally
// from cdnUrl (packageBaseUrl, else the jsDelivr CDN).  Pin packageBaseUrl to
// the runtime directory, as the worker does, and refuse any http(s) fetch so
// a missing file fails here instead of being downloaded.
const nativeFetch = globalThis.fetch;
const networkRequests = [];
globalThis.fetch = async function (input) {
  const href = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
  networkRequests.push(href);
  throw new TypeError("verify-cplfold-pyodide blocks network access: " + href);
};
const { loadPyodide } = await import(moduleUrl);
const pyodide = await loadPyodide({ indexURL: runtimeDirectory + path.sep, packageBaseUrl: runtimeDirectory + path.sep });
await pyodide.loadPackage("numpy");

pyodide.FS.writeFile("/tmp/cplfold-python.zip", archiveBytes);
pyodide.runPython([
  "import pathlib, sys, zipfile",
  "target = pathlib.Path('/opt/hyb2-cplfold')",
  "target.mkdir(parents=True, exist_ok=True)",
  "with zipfile.ZipFile('/tmp/cplfold-python.zip') as bundle:",
  "    bundle.extractall(target)",
  "sys.path.insert(0, str(target / 'cplfold'))"
].join("\n"));

const bridge = pyodide.pyimport("cplfold_web");
const matrixContract = JSON.parse(String(pyodide.runPython([
  "import json, math, numpy as np",
  "from cplfold_web import build_hyb_bonus_matrix",
  "matrix, summary = build_hyb_bonus_matrix(28, [(3, 8, 20, 25)])",
  "json.dumps({'symmetric': bool(np.allclose(matrix, matrix.T)), 'maximum': summary['maximumBonus'], 'rows': summary['inputRecords']})"
].join("\n"))));
assert.equal(matrixContract.symmetric, true);
assert.equal(matrixContract.rows, 1);
assert.ok(Math.abs(matrixContract.maximum - Math.log(2)) < 1e-6,
  "discrete Gaussian vectors must be peak-normalised before the original log1p transform");

// The shipped Python (not just the checkout) and the IRIS browser engine
// must see the same weak cells, duplicate blocks and asymmetric arm lengths.
const iris = createRequire(import.meta.url)("../../web/iris-core.js");
const repeated = { oneStart: 1, oneEnd: 9, twoStart: 23, twoEnd: 31 };
const arms = [repeated, repeated, { oneStart: 7, oneEnd: 12, twoStart: 27, twoEnd: 34 }];
const irisMatrix = iris.supportMatrix(40, arms);
const gaussian = JSON.parse(String(bridge.bonus_matrix_json(JSON.stringify({
  sequence: "A".repeat(40), evidenceMode: "hyb-blocks", supportKernel: "gaussian", evidenceArms: arms
}))));
const entries = new Map(gaussian.evidence.bonusEntries.map(({ one, two, value }) => [(one - 1) * 40 + two - 1, value]));
assert.ok(irisMatrix[20] > 2 ** -23 && irisMatrix[20] < 1e-6, "fixture includes a previously lost tail cell");
for (let i = 0; i < 40; i++) for (let j = i + 1; j < 40; j++) {
  const k = i * 40 + j;
  assert.equal(entries.has(k), irisMatrix[k] > 0, `Gaussian support mask at (${i + 1},${j + 1})`);
  if (entries.has(k)) assert.ok(Math.abs(entries.get(k) - irisMatrix[k]) < 1e-12, `Gaussian value at (${i + 1},${j + 1})`);
}

const exportContract = JSON.parse(String(bridge.bonus_matrix_json(JSON.stringify({
  sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
  evidenceMode: "hyb-blocks",
  evidenceArms: [{ oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 }]
}))));
assert.equal(exportContract.bridgeVersion, "8");
assert.equal(exportContract.evidence.inputRecords, 1);
assert.ok(exportContract.evidence.bonusEntries.length > 0);
assert.ok(exportContract.evidence.bonusEntries.every(function (entry) { return entry.one < entry.two; }));

const extendedExport = JSON.parse(String(bridge.bonus_matrix_json(JSON.stringify({
  sequence: "A".repeat(501),
  evidenceMode: "hyb-blocks",
  evidenceArms: [{ oneStart: 3, oneEnd: 8, twoStart: 493, twoEnd: 498 }]
}))));
assert.equal(extendedExport.sequence.length, 501);
assert.equal(extendedExport.evidence.inputRecords, 1);

const result = JSON.parse(String(bridge.fold_json(JSON.stringify({
  sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
  beamSize: 20,
  maxPhase1: 2,
  energyDelta: 5,
  energyModel: "DP09",
  alpha: 0.5,
  beta: 0,
  allowPseudoknot: true,
  evidenceMode: "hyb-blocks",
  evidenceArms: [
    { oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 },
    { oneStart: 4, oneEnd: 9, twoStart: 19, twoEnd: 24 }
  ]
}))));

assert.equal(result.engine, "CPLfold");
assert.equal(result.engineVersion, "24bab52");
assert.equal(result.bridgeVersion, "8");
assert.equal(result.allowPseudoknot, true);
assert.equal(result.dotBracket, "..(((((..[[[[)))))......]]]]");
assert.equal(result.topology, "pseudoknotted");
assert.equal(result.structureType, "pseudoknot");
assert.equal(result.evidence.inputRecords, 2);
assert.equal(result.evidence.source, "hyb-block-intervals");
assert.ok(result.evidence.bonusEntries.length > 0);
assert.equal(result.evidence.nonzeroUpperTriangleCells, result.evidence.bonusEntries.length);
assert.ok(result.evidence.bonusEntries.every(function (entry) { return entry.one < entry.two; }));
assert.ok(result.pairs.some(function (pair) { return pair.layer === "primary"; }));
assert.ok(result.pairs.some(function (pair) { return pair.layer === "pseudoknot-1"; }));
assert.ok(result.candidates.length >= 2);
assert.equal(result.constraintMode, "none");
assert.equal(result.constraintSource, "none");
assert.equal(result.evidenceMode, "hyb-blocks");
assert.equal(result.evidenceSource, "hyb-block-bonus-matrix");

// Precomputed RNAduplex counts let us check the saturating transform before
// loading ViennaRNA. The browser normally computes the pairs in Pyodide below.
const duplexExport = JSON.parse(String(bridge.bonus_matrix_json(JSON.stringify({
  sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
  evidenceMode: "hyb-blocks",
  supportKernel: "duplex",
  evidenceArms: [{ oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 }],
  evidencePairs: [[3, 25, 2], [4, 24, 4], [5, 23, 10]]
}))));
assert.equal(duplexExport.evidence.supportKernel, "duplex");
assert.ok(Math.abs(duplexExport.evidence.saturatingQ - 16 / 3) < 1e-6);
const duplexValues = Object.fromEntries(duplexExport.evidence.bonusEntries.map(function (entry) { return [entry.one + "," + entry.two, entry.value]; }));
[[3, 25, 2], [4, 24, 4], [5, 23, 10]].forEach(function ([i, j, c]) {
  assert.ok(Math.abs(duplexValues[i + "," + j] - 3.6 * c / (c + 16 / 3)) < 1e-5, "saturated duplex bonus at " + i + "," + j);
});
assert.throws(function () {
  bridge.bonus_matrix_json(JSON.stringify({
    sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG", evidenceMode: "hyb-blocks", supportKernel: "duplex",
    evidenceArms: [{ oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 }]
  }));
}, /evidencePairs/);

// With ViennaRNA's own Python module (scripts/build/build-vienna-pyodide.sh) the
// browser computes the duplex pairs itself, exactly as the desktop does: the
// expected values are the native runtime's for the same request.
assert.equal(manifest.viennarna, "2.7.2");
const lock = JSON.parse(fs.readFileSync(path.join(runtimeDirectory, "pyodide-lock.json"), "utf8"));
// The wheel is content-addressed (viennarna-<first 16 hex of its SHA-256>/),
// so a cached old wheel can never meet a new lock after a deploy.
const viennaFile = lock.packages.viennarna.file_name;
const viennaMatch = /^viennarna-([a-f0-9]{16})\/(viennarna-2\.7\.2-cp313-cp313-pyemscripten_2025_0_wasm32\.whl)$/.exec(viennaFile);
assert.ok(viennaMatch, "the lock must name the hash-named ViennaRNA wheel, not " + viennaFile);
const viennaDigest = createHash("sha256").update(fs.readFileSync(path.join(runtimeDirectory, viennaFile))).digest("hex");
assert.equal(viennaDigest.slice(0, 16), viennaMatch[1], "the wheel directory must be named after the wheel's SHA-256");
assert.equal(lock.packages.viennarna.sha256, viennaDigest);
assert.equal(manifest.assets[viennaFile], viennaDigest);
assert.deepEqual(fs.readdirSync(runtimeDirectory).filter(function (name) { return name.startsWith("viennarna-"); }),
  [viennaFile.split("/")[0]], "stale ViennaRNA wheels or directories must be removed");
await pyodide.loadPackage("viennarna");
const vienna = JSON.parse(String(pyodide.runPython([
  "import RNA, json",
  "d = RNA.duplexfold('GGGAAACCCUUUGGG', 'CCCAAAGGGUUUCCC')",
  "n = RNA.duplexfold('AAAA', 'AAAA')",
  "s, e = RNA.cofold('GGGGGG&CCCCCC')",
  "json.dumps([RNA.__version__, d.structure, round(d.energy, 2), n.structure, s, round(e, 2)])"
].join("\n"))));
assert.deepEqual(vienna, ["2.7.2", "(((((((((((((((&)))))))))))))))", -28.1, ".&.", "(((((())))))", -12.4]);
const browserDuplex = JSON.parse(String(bridge.bonus_matrix_json(JSON.stringify({
  sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
  evidenceMode: "hyb-blocks",
  supportKernel: "duplex",
  evidenceArms: [
    { oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 },
    { oneStart: 4, oneEnd: 9, twoStart: 19, twoEnd: 24 },
    { oneStart: 1, oneEnd: 10, twoStart: 15, twoEnd: 28 }
  ]
}))));
assert.equal(browserDuplex.evidence.pairCounter, "ViennaRNA 2.7.2 RNAduplex, 37 C");
assert.equal(browserDuplex.evidence.duplexesWithPairs, 3);
assert.ok(Math.abs(browserDuplex.evidence.saturatingQ - 1.2) < 1e-9);
assert.deepEqual(browserDuplex.evidence.bonusEntries.map(function (entry) {
  return [entry.one, entry.two, Math.round(entry.value * 1e6) / 1e6];
}), [[3, 18, 1.636364], [4, 17, 1.636364], [5, 16, 1.636364], [6, 15, 1.636364], [6, 22, 2.25]]);

assert.throws(function () {
  bridge.fold_json(JSON.stringify({
    sequence: "A".repeat(76),
    evidenceMode: "none"
  }));
}, /limited to 75 nt/);

const extendedResult = JSON.parse(String(bridge.fold_json(JSON.stringify({
  sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG" + "GCAU".repeat(12),
  maxSequenceLength: 125,
  beamSize: 1,
  maxPhase1: 1,
  energyDelta: 0,
  energyModel: "DP09",
  alpha: 0,
  beta: 0,
  allowPseudoknot: true,
  evidenceMode: "none"
}))));
assert.equal(extendedResult.sequence.length, 76);
assert.equal(extendedResult.maxSequenceLength, 125);

const pseudoknotFreeResult = JSON.parse(String(bridge.fold_json(JSON.stringify({
  sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
  beamSize: 20,
  maxPhase1: 2,
  energyDelta: 5,
  energyModel: "DP09",
  alpha: 0.5,
  beta: 0.5,
  allowPseudoknot: false,
  evidenceMode: "hyb-blocks",
  evidenceArms: [
    { oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 },
    { oneStart: 4, oneEnd: 9, twoStart: 19, twoEnd: 24 }
  ]
}))));
assert.equal(pseudoknotFreeResult.allowPseudoknot, false);
assert.equal(pseudoknotFreeResult.parameters.allowPseudoknot, false);
assert.equal(pseudoknotFreeResult.crossingPairs, 0);
assert.ok(pseudoknotFreeResult.candidates.every(function (candidate) {
  return candidate.type !== "pseudoknot" && candidate.crossingPairs === 0;
}));

assert.throws(function () {
  bridge.fold_json(JSON.stringify({
    sequence: "A".repeat(501),
    maxSequenceLength: 500,
    evidenceMode: "none"
  }));
}, /hard safety ceiling of 500 nt/);

bridge.destroy();
assert.deepEqual(networkRequests, [], "the Pyodide smoke test must not touch the network");
globalThis.fetch = nativeFetch;
process.stdout.write("cplfold-pyodide-smoke: ok\n");
