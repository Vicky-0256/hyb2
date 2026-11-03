"use strict";

// Structure controller: one long-lived browser CPLfold worker (Pyodide loads
// once), the capacity timeout starting after the runtime is ready, and
// cancellation of desktop native CPLfold jobs.

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const repository = path.resolve(__dirname, "..");
const workers = [];
function FakeWorker(url, options) {
  this.url = url;
  this.options = options || null;
  this.messages = [];
  this.terminated = false;
  workers.push(this);
}
FakeWorker.prototype.postMessage = function (message) { this.messages.push(message); };
FakeWorker.prototype.terminate = function () { this.terminated = true; };

const timers = [];
const window = {
  setTimeout: function (callback, ms) { timers.push({ callback: callback, ms: ms, cleared: false }); return timers.length; },
  clearTimeout: function (id) { if (timers[id - 1]) timers[id - 1].cleared = true; },
  Hyb2Pages: {
    getStructureSequence: function (state) {
      return { sequence: state.sequence, label: "test", sourceSegments: null, evidenceDatasets: [], evidenceDatasetIds: [] };
    }
  }
};
const context = vm.createContext({ Worker: FakeWorker, window: window, Promise: Promise });
vm.runInContext(fs.readFileSync(path.join(repository, "web", "structure-controller.js"), "utf8"), context, { filename: "structure-controller.js" });
const S = window.Hyb2Structure;

function cplfoldState() {
  return {
    sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
    structureWorker: null,
    structure: {
      engine: "cplfold", cplfoldEvidence: "none", cplfoldBeam: "20", cplfoldMaxPhase1: "3",
      cplfoldEnergyDelta: "5", cplfoldEnergyModel: "DP09", cplfoldAlpha: "0.5", cplfoldBeta: "0",
      status: "idle", runId: 0, result: null
    }
  };
}
const toasts = [];
const api = { render: function () {}, showToast: function (message) { toasts.push(message); } };
const complete = { type: "complete", result: { engine: "CPLfold", sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG", dotBracket: "............................", pairs: [], candidates: [] } };

// Predictions and capacity probes share one worker; finishing does not terminate it.
const state = cplfoldState();
assert.equal(S.predict(state, api), true);
assert.equal(workers.length, 1);
const first = workers[0];
assert.equal(first.messages.at(-1).type, "cplfold");
first.onmessage({ data: complete });
assert.equal(state.structure.status, "complete");
assert.equal(first.terminated, false, "a finished prediction keeps the CPLfold runtime");
assert.equal(state.structure.result.runId, state.structure.runId, "results carry their run id for the drawing session");
assert.equal(S.predict(state, api), true);
assert.equal(workers.length, 1, "a second prediction reuses the loaded worker");
first.onmessage({ data: complete });

// The 60 s probe budget starts when the worker reports its runtime ready.
assert.equal(S.probeCplfoldCapacity(state, api), true);
assert.equal(workers.length, 1, "the capacity probe reuses the loaded worker");
assert.equal(first.messages.at(-1).type, "cplfold-capacity");
assert.equal(timers.length, 0, "no timeout runs while Pyodide and NumPy load");
first.onmessage({ data: { type: "progress", percent: 5, stage: "Loading", phase: "runtime" } });
assert.equal(timers.length, 0);
first.onmessage({ data: { type: "runtime-ready", operation: "capacity", runtimeLoadMs: 70000 } });
assert.equal(timers.length, 1);
assert.equal(timers[0].ms, 60000);
first.onmessage({ data: { type: "capacity-complete", sample: { length: 75, foldElapsedMs: 2000, elapsedMs: 2100 }, elapsedMs: 2100, runtimeLoadMs: 70000 } });
assert.equal(timers[0].cleared, true);
assert.equal(state.structure.cplfoldCapacity.status, "ready");
assert.equal(state.structure.cplfoldCapacity.elapsedMs, 2100, "elapsedMs excludes the runtime load");
assert.equal(first.terminated, false);

// A timed-out probe cannot be interrupted inside Pyodide: the worker is replaced.
assert.equal(S.probeCplfoldCapacity(state, api), true);
first.onmessage({ data: { type: "runtime-ready", operation: "capacity", runtimeLoadMs: 0 } });
timers.at(-1).callback();
assert.equal(first.terminated, true);
assert.equal(state.structure.cplfoldCapacity.status, "error");
assert.equal(S.predict(state, api), true);
assert.equal(workers.length, 2, "the next request starts a fresh worker");
const second = workers[1];

// Cancel terminates the running worker; errors do too.
S.cancel(state);
assert.equal(second.terminated, true);
assert.equal(S.predict(state, api), true);
const third = workers[2];
third.onmessage({ data: { type: "error", message: "boom" } });
assert.equal(third.terminated, true);
assert.equal(state.structure.status, "error");
assert.equal(S.predict(state, api), true);
assert.equal(workers.length, 4);
workers[3].onerror();
assert.equal(workers[3].terminated, true);

// Bonus-matrix exports reuse the same worker.
assert.equal(S.predict(state, api), true);
const fifth = workers[4];
fifth.onmessage({ data: complete });
const prepared = { sequence: state.sequence, assembly: { evidenceArms: [{ oneStart: 3, oneEnd: 8, twoStart: 20, twoEnd: 25 }] } };
let exported = null;
assert.equal(S.downloadCplfoldBonusMatrix(state, api, prepared, function (result) { exported = result; }), true);
assert.equal(workers.length, 5);
assert.equal(fifth.messages.at(-1).type, "cplfold-bonus-matrix");
fifth.onmessage({ data: { type: "bonus-matrix-complete", result: { ok: true } } });
assert.deepEqual(JSON.parse(JSON.stringify(exported)), { ok: true });
assert.equal(fifth.terminated, false);

// Desktop native CPLfold: cancel asks the shell to stop the job.
(async function () {
  const cancelled = [];
  let resolveNative;
  window.Hyb2Desktop = {
    enabled: true,
    capabilities: { cplfoldNative: true },
    limits: { cplfoldMaxLength: 2000 },
    predictCplfold: function () { return new Promise(function (resolve) { resolveNative = resolve; }); },
    cancelCplfold: function (job) { cancelled.push(job); return Promise.resolve(true); }
  };
  const nativeState = cplfoldState();
  assert.equal(S.predict(nativeState, api), true);
  assert.equal(workers.length, 5, "native CPLfold does not start a browser worker");
  const runId = nativeState.structure.runId;
  S.cancel(nativeState);
  assert.equal(cancelled.length, 1, "cancelling a native job calls the desktop cancel hook");
  assert.equal(cancelled[0].runId, runId);
  assert.equal(nativeState.structure.status, "idle");
  resolveNative({ ok: true, engine: "CPLfold", dotBracket: "", pairs: [] });
  await new Promise(function (resolve) { setImmediate(resolve); });
  assert.equal(nativeState.structure.result, null, "the cancelled job's late answer is dropped");
  S.cancel(nativeState);
  assert.equal(cancelled.length, 1, "an idle cancel does not call the desktop again");

  // A finished native job is not cancelled by a later reset.
  assert.equal(S.predict(nativeState, api), true);
  resolveNative({ ok: true, engine: "CPLfold", sequence: nativeState.sequence, dotBracket: "", pairs: [] });
  await new Promise(function (resolve) { setImmediate(resolve); });
  assert.equal(nativeState.structure.status, "complete");
  S.reset(nativeState);
  assert.equal(cancelled.length, 1);

  // Shells without the hook are tolerated.
  delete window.Hyb2Desktop.cancelCplfold;
  assert.equal(S.predict(nativeState, api), true);
  S.cancel(nativeState);
  assert.equal(nativeState.structure.status, "idle");

  // desktop-adapter.js exposes the hook and treats a missing command as false.
  const adapterSource = fs.readFileSync(path.join(repository, "web", "desktop-adapter.js"), "utf8");
  const invoked = [];
  const adapterWindow = { __TAURI__: { core: { invoke: function (name, args) {
    invoked.push({ name: name, args: args });
    if (name === "runtime_detect") return Promise.resolve({ capabilities: [] });
    return name === "cplfold_cancel" ? Promise.reject(new Error("unknown command")) : Promise.resolve({});
  } } } };
  vm.runInContext(adapterSource, vm.createContext({ window: adapterWindow, Promise: Promise }), { filename: "desktop-adapter.js" });
  assert.equal(typeof adapterWindow.Hyb2Desktop.cancelCplfold, "function");
  assert.equal(await adapterWindow.Hyb2Desktop.cancelCplfold({ runId: 7 }), false);
  const cancelCall = invoked.find(function (call) { return call.name === "cplfold_cancel"; });
  assert.equal(cancelCall.args.request.run_id, 7);

  process.stdout.write("cplfold-worker-lifecycle: ok\n");
}()).catch(function (error) {
  process.stderr.write((error && error.stack) || String(error));
  process.exit(1);
});
