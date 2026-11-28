"use strict";

/*
 * Exercise the browser Worker protocol against the generated Emscripten module
 * in Node's worker_threads runtime. This is intentionally separate from the
 * native-module smoke test: it proves that the site-level message boundary,
 * asset lookup, and result serialization all remain compatible.
 */

const assert = require("node:assert/strict");
const { Worker } = require("node:worker_threads");
const path = require("node:path");
const fs = require("node:fs");

const workerSource = path.resolve(__dirname, "..", "..", "web", "structure.worker.js");
const shimSource = `
  const { parentPort } = require("node:worker_threads");
  const fs = require("node:fs");
  const vm = require("node:vm");
  const { fileURLToPath, pathToFileURL } = require("node:url");
  const workerSource = ${JSON.stringify(workerSource)};

  global.self = global;
  global.self.location = { href: pathToFileURL(workerSource).href };
  global.self.postMessage = function (message) { parentPort.postMessage(message); };
  global.require = require;
  global.importScripts = function () {
    for (const url of arguments) {
      const filename = url.startsWith("file:") ? fileURLToPath(url) : url;
      vm.runInThisContext(fs.readFileSync(filename, "utf8"), { filename });
    }
  };

  vm.runInThisContext(fs.readFileSync(workerSource, "utf8"), { filename: workerSource });
  parentPort.on("message", function (data) { global.self.onmessage({ data }); });
`;

// cofoldWithVienna frees every allocated buffer when a later allocation fails.
function verifyCofoldAllocationCleanup() {
  const vm = require("node:vm");
  const sandbox = { URL, self: { location: { href: "file:///web/structure.worker.js" } } };
  vm.createContext(sandbox);
  sandbox.importScripts = url => vm.runInContext(fs.readFileSync(path.join(path.dirname(workerSource), path.basename(url)), "utf8"), sandbox);
  vm.runInContext(fs.readFileSync(workerSource, "utf8") + "\nself.cofoldWithVienna = cofoldWithVienna;", sandbox, { filename: workerSource });
  for (const failAt of [2, 3]) {
    let next = 0;
    const live = new Set();
    const engine = {
      lengthBytesUTF8: (value) => value.length,
      stringToUTF8: function () {},
      _malloc: function () { next += 1; if (next === failAt) return 0; live.add(next * 16); return next * 16; },
      _free: function (pointer) { assert.ok(live.delete(pointer), "freed an unallocated pointer"); }
    };
    assert.throws(() => sandbox.self.cofoldWithVienna(engine, "GGG", "CCC", 37, 3), /could not allocate/);
    assert.equal(live.size, 0, "allocation " + failAt + " failing must not leak earlier buffers");
  }
  process.stdout.write("structure-worker-cofold-allocation-cleanup: ok\n");
}

async function verifyFoldAlphabet() {
  const worker = new Worker(shimSource + '\nHyb2FoldText.alphabet = "ACG";', { eval: true });
  try {
    for (const type of ["fold", "comrades-fold", "iris-fold", "comrades-evidence"]) {
      const response = await waitForTerminal(worker, [], {type, sequence: "GGGGtAAACCCC", evidenceArms: []});
      assert.equal(response.type, "error", type);
      assert.match(response.message, /unsupported symbols: U/, type);
    }
    const valid = {sequence: "gggg aaaacccc", evidenceArms: []};
    const noEvidence = await waitForTerminal(worker, [], {...valid, type: "iris-fold"});
    assert.match(noEvidence.message, /requires HYB evidence/);
    const badArm = await waitForTerminal(worker, [], {...valid, type: "comrades-evidence",
      evidenceArms: [{oneStart: 1, twoStart: 9, sequenceOne: "GGGG", sequenceTwo: "CUCC"}]});
    assert.match(badArm.message, /Prepared HYB arm 1 contains an invalid RNA sequence/);
  } finally {
    await worker.terminate();
  }
}

async function verifyFoldParameters(worker, hasEngine) {
  const left = "G".repeat(12), right = "C".repeat(12);
  for (const type of ["fold", "comrades-fold"]) {
    const request = {type, sequence: left + "A".repeat(14) + right,
      evidenceArms: [{oneStart: 1, twoStart: 27, sequenceOne: left, sequenceTwo: right}]};
    for (const [key, invalid, diagnostic] of [
      ["temperature", [101, -1, NaN, "37", null, false, undefined], /Folding temperature.*0 to 100/],
      ["minimumLoop", [13, -1, 3.5, "3", null, false, undefined], /Minimum hairpin loop.*0 to 12/]
    ]) {
      for (const value of invalid) {
        const messages = [];
        const reply = await waitForTerminal(worker, messages, {...request, [key]: value});
        const label = `${type}: ${key}=${String(value)}`;
        assert.deepEqual(messages.map(message => message.type), ["error"], label);
        assert.match(reply.message, diagnostic, label);
      }
    }
    if (!hasEngine) continue;
    for (const [parameters, expected] of [
      [{}, [37, 3]], [{temperature: 0, minimumLoop: 12}, [0, 12]],
      [{temperature: 100, minimumLoop: 0}, [100, 0]], [{temperature: 24.5}, [24.5, 3]]
    ]) {
      const reply = await waitForTerminal(worker, [], {...request, ...parameters});
      assert.equal(reply.type, "complete", reply.message);
      assert.deepEqual([reply.result.temperature, reply.result.minimumLoop], expected, type);
    }
  }
}

async function verifyComradesCounts(worker, hasEngine) {
  // Even if validation regresses, these arms cannot start a large ensemble.
  const unpaired = {type: "comrades-fold", sequence: "A".repeat(12),
    evidenceArms: [{oneStart: 1, twoStart: 9, sequenceOne: "AAAA", sequenceTwo: "AAAA"}]};
  for (const [key, outOfRange, diagnostic] of [
    ["randomFoldCount", [-1, 1001], /Random-fold count.*0 to 1000/],
    ["constraintLimit", [0, 76], /Constraint count.*1 to 75/]
  ]) {
    for (const value of [true, false, null, "", "1", [1], {}, undefined, NaN, Infinity, 1.5, ...outOfRange]) {
      const messages = [];
      const reply = await waitForTerminal(worker, messages, {...unpaired, [key]: value});
      const label = `${key}=${String(value)} (${typeof value})`;
      assert.deepEqual(messages.map(message => message.type), ["error"], label);
      assert.match(reply.message, diagnostic, label);
    }
  }

  const missingEngine = /ViennaRNA WebAssembly assets are missing/;
  const upper = await waitForTerminal(worker, [], {...unpaired, randomFoldCount: 1000});
  assert.equal(upper.type, "error");
  assert.match(upper.message, hasEngine ? /RNAcofold did not produce a non-overlapping base-pair stem/ : missingEngine);

  // Competing short stems expose the default cap of 75, but only one can fit:
  // every stem uses the same left arm.
  const request = {type: "comrades-fold", sequence: "GGGGAAAA" + "C".repeat(79),
    evidenceArms: Array.from({length: 76}, (_, i) => ({
      oneStart: 1, twoStart: 9 + i, sequenceOne: "GGGG", sequenceTwo: "CCCC"
    }))};
  for (const [parameters, folds, stems] of [
    [{}, 0, 75], [{randomFoldCount: 0, constraintLimit: 1}, 0, 1],
    [{randomFoldCount: 1, constraintLimit: 75}, 1, 75]
  ]) {
    const reply = await waitForTerminal(worker, [], {...request, ...parameters});
    if (!hasEngine) {
      assert.equal(reply.type, "error");
      assert.match(reply.message, missingEngine);
      continue;
    }
    assert.equal(reply.type, "complete", reply.message);
    const {randomisation, evidence} = reply.result;
    assert.equal(randomisation.requestedFolds, folds);
    assert.equal(randomisation.completedFolds, folds);
    assert.equal(randomisation.structures.length, folds + 1);
    assert.equal(evidence.requestedStemConstraints, stems);
  }
}

async function verifyCatalogDefaults(hasEngine) {
  const request = {sequence: "GGGG" + "A".repeat(12) + "C".repeat(8),
    evidenceArms: Array.from({length: 4}, (_, i) => ({
      oneStart: 1, twoStart: 17 + i, sequenceOne: "GGGG", sequenceTwo: "CCCC"
    }))};
  for (const limit of [2, 75]) {
    const worker = new Worker(shimSource + `
      Object.assign(Hyb2EngineParameters.vienna.defaults, {temperature: 26.5, minLoopSize: 4});
      Object.assign(Hyb2EngineParameters.comrades.defaults, {temperature: 29.5, minLoopSize: 5, randomFoldCount: 1, constraintLimit: ${limit}});
      Hyb2EngineParameters.comrades.ranges.constraintLimit = [1, 50];`, {eval: true});
    try {
      if (limit === 75) {
        // A stale in-memory default must fail just like an explicit 75, before loading the engine.
        for (const parameters of [{}, {constraintLimit: 75}]) {
          const messages = [];
          const reply = await waitForTerminal(worker, messages, {...request, type: "comrades-fold", ...parameters});
          assert.deepEqual(messages.map(m => m.type), ["error"]);
          assert.match(reply.message, /Constraint count.*1 to 50/);
        }
        continue;
      }
      for (const [type, temperature, loop] of [["fold", 26.5, 4], ["comrades-fold", 29.5, 5], ["comrades-evidence", 37, 3]]) {
        const reply = await waitForTerminal(worker, [], {...request, type});
        if (!hasEngine) {
          assert.equal(reply.type, "error");
          assert.match(reply.message, /ViennaRNA WebAssembly assets are missing/);
          continue;
        }
        assert.equal(reply.type, "complete", reply.message);
        const result = reply.result || reply.evidence;
        assert.deepEqual([result.temperature, result.minimumLoop], [temperature, loop], type);
        if (type === "comrades-fold") {
          assert.equal(result.evidence.requestedStemConstraints, 2);
          assert.equal(result.randomisation.completedFolds, 1);
        }
      }
    } finally {
      await worker.terminate();
    }
  }
}

async function main() {
  verifyCofoldAllocationCleanup();
  await verifyFoldAlphabet();
  const generatedAsset = path.resolve(__dirname, "..", "..", "web", "vendor", "viennarna", "vienna-rna.js");
  const hasEngine = fs.existsSync(generatedAsset);
  await verifyCatalogDefaults(hasEngine);
  const worker = new Worker(shimSource, { eval: true });
  const messages = [];

  try {
    await verifyComradesCounts(worker, hasEngine);
    await verifyFoldParameters(worker, hasEngine);
    if (!hasEngine) {
      const missing = await waitForTerminal(worker, [], {
        type: "fold",
        sequence: "GCGCUUCGCC",
        temperature: 37,
        minimumLoop: 3
      });
      assert.equal(missing.type, "error");
      assert.match(missing.message, /ViennaRNA WebAssembly assets are missing/);
      process.stdout.write("structure-worker-assets-missing: diagnostic ok\n");
      return;
    }

    const unconstrainedMessage = await waitForTerminal(worker, messages, {
      type: "fold",
      sequence: "GCGCUUCGCC",
      temperature: 37,
      minimumLoop: 3
    });
    assert.equal(unconstrainedMessage.type, "complete", unconstrainedMessage.message);
    const result = unconstrainedMessage.result;
    assert.equal(result.engine, "ViennaRNA");
    assert.equal(result.engineVersion, "2.7.2");
    assert.equal(Number.isFinite(result.energy), true);
    assert.equal(result.dotBracket.length, 10);
    assert.match(result.dotBracket, /^[.()]+$/);
    assert.equal(result.pairs.length * 2 + result.unpaired, 10);
    assert.equal(result.constraintMode, "none");
    assert.equal(result.constraintCount, 0);
    assert.deepEqual(result.constraints, []);
    assert.equal(hasPair(result, 4, 8), false,
      "The constrained integration pair should differ from the unconstrained MFE");

    const constrainedMessage = await waitForTerminal(worker, messages, {
      type: "fold",
      sequence: "GCGCUUCGCC",
      temperature: 37,
      minimumLoop: 3,
      constraints: [{ left: 3, right: 9 }, { left: 4, right: 8 }]
    });
    assert.equal(constrainedMessage.type, "complete", constrainedMessage.message);
    const constrained = constrainedMessage.result;
    assert.equal(Number.isFinite(constrained.energy), true);
    assert.equal(constrained.constraintMode, "hard-base-pairs");
    assert.equal(constrained.constraintCount, 2);
    assert.deepEqual(constrained.constraints, [{ left: 3, right: 9 }, { left: 4, right: 8 }]);
    assert.equal(hasPair(constrained, 3, 9), true,
      "Expected the Worker result to contain the first requested hard-constrained pair");
    assert.equal(hasPair(constrained, 4, 8), true,
      "Expected the Worker result to contain the second requested hard-constrained pair");

    const conflictingMessage = await waitForTerminal(worker, messages, {
      type: "fold",
      sequence: "GCGCUUCGCC",
      temperature: 37,
      minimumLoop: 3,
      constraints: [{ left: 3, right: 9 }, { left: 3, right: 10 }]
    });
    assert.equal(conflictingMessage.type, "error");
    assert.match(conflictingMessage.message, /reuses a nucleotide/);

    const crossingMessage = await waitForTerminal(worker, messages, {
      type: "fold",
      sequence: "GCGCUUCGCC",
      temperature: 37,
      minimumLoop: 3,
      constraints: [{ left: 1, right: 7 }, { left: 3, right: 9 }]
    });
    assert.equal(crossingMessage.type, "error");
    assert.match(crossingMessage.message, /must not cross/);

    const comradesMessage = await waitForTerminal(worker, messages, {
      type: "comrades-fold",
      sequence: "GGGGGGAAAACCCCCC",
      temperature: 37,
      minimumLoop: 3,
      constraintLimit: 75,
      randomFoldCount: 2,
      seed: "worker-contract",
      evidenceArms: [
        { recordId: "read-1", recordIndex: 1, sequenceOne: "GGGGGG", sequenceTwo: "CCCCCC", oneStart: 1, twoStart: 11 },
        { recordId: "read-2", recordIndex: 2, sequenceOne: "GGGGGG", sequenceTwo: "CCCCCC", oneStart: 1, twoStart: 11 }
      ]
    });
    assert.equal(comradesMessage.type, "complete", comradesMessage.message);
    const comrades = comradesMessage.result;
    assert.equal(comrades.constraintMode, "hyb-guided");
    assert.equal(comrades.constraintSource, "hyb-rna-cofold-evidence");
    assert.equal(comrades.evidence.inputRecords, 2);
    assert.equal(comrades.evidence.cofoldsWithIntermolecularPairs, 2);
    assert.ok(comrades.evidence.uniqueBasePairs > 0);
    assert.ok(comrades.evidence.rankedStems.length > 0);
    assert.ok(comrades.constraintCount > 0);
    assert.ok(comrades.comradesScore > 0);
    assert.equal(comrades.randomisation.completedFolds, 2);
    assert.equal(comrades.randomisation.structures.length, 3,
      "the ensemble should contain the ranked fold plus both randomised folds");
    assert.equal(comrades.randomisation.seed, "worker-contract");
    assert.ok(comrades.pairs.some(function (pair) { return pair.evidenceSupport > 0; }));

    assert.ok(messages.some(function (message) { return message.type === "progress"; }));
    assert.ok(messages.some(function (message) {
      return message.type === "progress" && message.details && message.details.phase === "rna-cofold";
    }));

    // Post-fold colouring uses the same COMRADES evidence, even when the
    // page sends prepared arm coordinates without the arm sequences.
    const coordinateArms = [
      { recordId: "read-1", oneStart: 1, oneEnd: 6, twoStart: 11, twoEnd: 16 },
      { recordId: "read-2", oneStart: 1, oneEnd: 6, twoStart: 11, twoEnd: 16 }
    ];
    const supportMessage = await waitForTerminal(worker, messages, {
      type: "comrades-evidence", sequence: "GGGGGGAAAACCCCCC", evidenceArms: coordinateArms
    });
    assert.equal(supportMessage.type, "complete", supportMessage.message);
    assert.deepEqual(supportMessage.evidence, {
      pairs: comrades.evidence.selectedBasePairs.map(({ one, two, count }) => ({ one, two, count })),
      inputRecords: 2, cofoldsWithIntermolecularPairs: 2, temperature: 37, minimumLoop: 3
    });
    await verifyUntruncatedScores(worker);
    await verifyU2MatchesOriginal(worker);
    process.stdout.write("structure-worker-integration: ok\n");
  } finally {
    await worker.terminate();
  }
}

async function verifyUntruncatedScores(worker) {
  // Tile two homopolymer arms. Each GGGG/CCCC read contributes exactly four
  // antiparallel observations, independently of the constraint shortlist.
  const sequence = "G".repeat(40) + "AAAA" + "C".repeat(40), evidenceArms = [], counts = new Map();
  for (let g = 1; g <= 37; g++) for (let c = 45; c <= 81; c++) {
    evidenceArms.push({oneStart: g, twoStart: c, sequenceOne: "GGGG", sequenceTwo: "CCCC"});
    for (let i = 0; i < 4; i++) {
      const key = `${g + i}:${c + 3 - i}`;
      counts.set(key, (counts.get(key) || 0) + 1);
    }
  }
  const fold = await waitForTerminal(worker, [], {type: "comrades-fold", sequence, evidenceArms,
    constraintLimit: 1, randomFoldCount: 1, seed: "full-count-control"});
  assert.equal(fold.type, "complete", fold.message);
  const result = fold.result;
  const shortlist = new Set(result.evidence.selectedBasePairs.map(p => `${p.one}:${p.two}`));
  assert.equal(shortlist.size, 1001);
  assert.equal(result.evidence.uniqueBasePairs, counts.size);
  let expected = 0, excludedSupport = 0;
  for (const {left, right, evidenceSupport} of result.pairs) {
    const key = `${left}:${right}`, raw = counts.get(key) || 0;
    expected += 2 * raw;
    if (!shortlist.has(key)) excludedSupport += raw;
    assert.equal(evidenceSupport, raw);
    assert.equal(result.nucleotideSupport[left - 1], raw);
    assert.equal(result.nucleotideSupport[right - 1], raw);
  }
  assert.ok(excludedSupport > 0, "a scored structure pair must be absent from the 1,001 constraint candidates");
  assert.equal(result.comradesScore, expected);
  assert.equal(result.randomisation.structures.length, 2);
  for (const member of result.randomisation.structures) {
    assert.equal(member.dotBracket, result.dotBracket, "one constraint makes ranked and shuffled orders identical");
    assert.equal(member.comradesScore, expected);
  }
  const post = await waitForTerminal(worker, [], {type: "comrades-evidence", sequence, evidenceArms});
  assert.equal(post.type, "complete", post.message);
  assert.deepEqual(new Map(post.evidence.pairs.map(p => [`${p.one}:${p.two}`, p.count])), counts);
}

// The web's COMRADES, from the page's .hyb parser and region assembly to the
// worker, on the yeast U2 snRNA and its 2,413 U2-U2 chimeras in data/: the
// structure, dG, accepted stems and RNAcofold pair counts of the original
// hyb2_fold, for the whole RNA and for 126-175 with 1126-1175 joined by the
// spacer, as desktop/runtime/tests/test_comrades.py checks for the CLI and
// the desktop app (it says where u2_hyb2_fold/ comes from).
async function verifyU2MatchesOriginal(worker) {
  const vm = require("node:vm");
  const zlib = require("node:zlib");
  const root = path.resolve(__dirname, "..", "..");
  const lines = file => fs.readFileSync(file, "utf8").split(/\r?\n/).filter(Boolean);
  const original = name => lines(path.join(root, "desktop", "runtime", "tests", "u2_hyb2_fold", name));

  const parser = vm.createContext({ Number, String, Boolean, Object, Array, Math, RegExp, Intl, TextDecoder,
    self: { postMessage: function () {} } });
  vm.runInContext(fs.readFileSync(path.join(root, "web", "parser.worker.js"), "utf8"), parser, { filename: "parser.worker.js" });
  const hyb = zlib.gunzipSync(fs.readFileSync(path.join(root, "data", "U2-U2.hyb.gz"))).toString("utf8");
  const records = hyb.split("\n").filter(Boolean).map(function (line, index) {
    const parsed = parser.parseRecord(line, index + 1);
    assert.ok(parsed.ok, line);
    return Object.assign(parsed.record, { index });
  });
  const [header, ...sequence] = lines(path.join(root, "data", "U2.fasta"));
  const gene = header.slice(1).trim();
  const fasta = { sequences: [{ id: gene, header: gene, sequence: sequence.join("") }] };
  const comrades = require(path.join(root, "web", "comrades-analysis.js"));

  // Two regions need each chimera's arms cofolded in the .hyb's order, as
  // hyb2_fold's; region 1's arm first gives 139-1169 (11 bp), not 138-1170.
  for (const [run, regions, arms] of [["hyb2_fold", [[1, 1175]], 2413],
    ["hyb2_fold_126-175_1126-1175", [[126, 175], [1126, 1175]], 517]]) {
    const [, structure, energy] = /^(\S+)\s+\(\s*(\S+)\)$/.exec(original(run + ".vienna")[2]);
    const stems = original(run + ".accepted").map(line => line.split(/\s+/).slice(1).map(Number));
    const counts = new Map();
    for (const line of original(run + ".basepair_scores.txt")) {
      const [i, j, n] = line.split(/\s+/).map(Number), key = Math.min(i, j) + ":" + Math.max(i, j);
      counts.set(key, (counts.get(key) || 0) + n);
    }
    const assembly = comrades.prepareReferenceAssembly(records, fasta, {
      regions: regions.map(([start, end]) => ({ rna: gene, start, end })) });
    assert.equal(assembly.evidenceArms.length, arms, run);

    // The whole RNA takes about 9 s on an Apple M-series Mac; a shared CI runner can be several times slower.
    const reply = await waitForTerminal(worker, [], { type: "comrades-fold", sequence: assembly.sequence,
      evidenceArms: assembly.evidenceArms }, 180000);
    assert.equal(reply.type, "complete", reply.message);
    const result = reply.result;
    assert.equal(result.dotBracket, structure, run); // two regions: with the spacer, as the original's
    assert.equal(result.energy.toFixed(2), Number(energy).toFixed(2), run);
    assert.deepEqual(result.acceptedStemConstraints.map(stem => [stem.left, stem.right, stem.length]), stems, run);
    assert.deepEqual(new Map(result.evidence.scoredEvidencePairs.map(
      pair => [Math.min(pair.one, pair.two) + ":" + Math.max(pair.one, pair.two), pair.count])), counts, run);
  }
  process.stdout.write("structure-worker-u2-original-hyb2-fold: ok\n");
}

function hasPair(result, left, right) {
  return result.pairs.some(function (pair) {
    return pair.left === left && pair.right === right;
  });
}

function waitForTerminal(worker, messages, request, timeoutMs = 30000) {
  return new Promise(function (resolve, reject) {
    const timeout = setTimeout(function () {
      reject(new Error("Timed out waiting for the ViennaRNA structure worker result."));
    }, timeoutMs);

    function cleanup() {
      clearTimeout(timeout);
      worker.off("message", onMessage);
      worker.off("error", onError);
    }

    function onMessage(message) {
      messages.push(message);
      if (message.type === "error" || message.type === "complete") {
        cleanup();
        resolve(message);
      }
    }

    function onError(error) {
      cleanup();
      reject(error);
    }

    worker.on("message", onMessage);
    worker.on("error", onError);
    worker.postMessage(request);
  });
}

main().catch(function (error) {
  process.stderr.write((error && error.stack) || String(error));
  process.stderr.write("\n");
  process.exitCode = 1;
});
