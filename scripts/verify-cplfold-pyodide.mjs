import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const repository = path.resolve(scriptDirectory, "..");
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
  "CPLfold-THIRD_PARTY_NOTICES.md",
  "CPLfold-GPL-2.0-or-later.txt",
  "CPLfold-LinearFold-LICENSE.txt"
].forEach(function (name) {
  assert.ok(fs.statSync(path.join(runtimeDirectory, name)).size > 0, name + " must be built before the smoke test");
});

const manifest = JSON.parse(fs.readFileSync(path.join(runtimeDirectory, "build-manifest.json"), "utf8"));
assert.equal(manifest.pyodide, "0.29.4");
assert.equal(manifest.python, "3.13.2");
assert.equal(manifest.numpy, "2.2.5");
assert.equal(manifest.cplfoldRevision, "af49f8e");
assert.equal(manifest.bridgeVersion, "2");
assert.match(manifest.cplfoldArchiveFile, /^cplfold-python-[a-f0-9]{64}\.zip$/);
const archivePath = path.join(runtimeDirectory, manifest.cplfoldArchiveFile);
assert.ok(fs.statSync(archivePath).size > 0);
const archiveBytes = fs.readFileSync(archivePath);
assert.equal(createHash("sha256").update(archiveBytes).digest("hex"), manifest.cplfoldArchiveSha256);

const moduleUrl = pathToFileURL(path.join(runtimeDirectory, "pyodide.mjs")).href;
const { loadPyodide } = await import(moduleUrl);
const pyodide = await loadPyodide({ indexURL: runtimeDirectory + path.sep });
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

const result = JSON.parse(String(bridge.fold_json(JSON.stringify({
  sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
  beamSize: 20,
  maxPhase1: 2,
  energyDelta: 5,
  energyModel: "DP09",
  alpha: 0.5,
  beta: 0,
  evidenceMode: "hyb-blocks",
  evidenceArms: [
    { oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 },
    { oneStart: 4, oneEnd: 9, twoStart: 19, twoEnd: 24 }
  ]
}))));

assert.equal(result.engine, "CPLfold");
assert.equal(result.engineVersion, "af49f8e");
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

assert.throws(function () {
  bridge.fold_json(JSON.stringify({
    sequence: "A".repeat(76),
    evidenceMode: "none"
  }));
}, /limited to 75 nt/);

bridge.destroy();
process.stdout.write("cplfold-pyodide-smoke: ok\n");
