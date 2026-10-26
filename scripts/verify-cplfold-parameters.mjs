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

// Fixed short test fixture: no user or biological dataset is loaded.
const fixture = {
  type: "cplfold", sequence: "GGCGCGGCACCGUCCGCGGAACAAACGG",
  evidenceMode: "hyb-blocks",
  evidenceArms: [{oneStart:3, oneEnd:8, twoStart:13, twoEnd:18},
                 {oneStart:4, oneEnd:9, twoStart:14, twoEnd:19}],
  beamSize:20, maxPhase1:2, energyDelta:5, energyModel:"DP09",
  alpha:0, beta:0, allowPseudoknot:true
};
async function run(overrides) {
  messages.length = 0;
  await self.onmessage({data:{...fixture, ...overrides}});
  const error = messages.find(m => m.type === "error");
  assert.equal(error, undefined, error && error.message);
  const done = messages.find(m => m.type === "complete");
  assert.ok(done);
  for (const key of ["alpha", "beta", "allowPseudoknot"])
    assert.equal(done.result.parameters[key], {...fixture,...overrides}[key]);
  return done.result;
}
try {
  const a0 = await run({alpha:0});
  const a1 = await run({alpha:0.5});
  const common = a1.candidates.map(c => [a0.candidates.find(d => d.dotBracket === c.dotBracket), c])
    .filter(([before]) => before);
  assert.ok(common.some(([before,after]) => Math.abs(before.score-after.score)>1e-6),
    "alpha must change actual parser scores, not merely result metadata");
  const b1 = await run({alpha:0.5,beta:0.5});
  const pk = b1.candidates.filter(c => c.type === "pseudoknot");
  assert.ok(pk.length>0, "fixture must exercise pseudoknot ranking");
  for (const after of pk) {
    const before = a1.candidates.find(c => c.dotBracket === after.dotBracket);
    assert.ok(before);
    assert.ok(Math.abs(after.energy-before.energy)<1e-8);
    assert.ok(Math.abs(after.effectiveEnergy-(after.energy+0.5*after.phase1Energy))<1e-8);
    assert.ok(Math.abs(after.effectiveEnergy-before.effectiveEnergy)>1e-6);
  }
  const off0 = await run({alpha:0.5,allowPseudoknot:false,beta:0});
  const off1 = await run({alpha:0.5,allowPseudoknot:false,beta:0.5});
  assert.deepEqual(off0.candidates,off1.candidates,"beta must have no effect with pseudoknots disabled");
  const noEvidence0 = await run({evidenceMode:"none",evidenceArms:[],alpha:0});
  const noEvidence1 = await run({evidenceMode:"none",evidenceArms:[],alpha:0.5});
  assert.deepEqual(noEvidence0.candidates,noEvidence1.candidates,"alpha needs an evidence matrix");
  console.log(JSON.stringify({
    archiveSha256:a1.runtimeManifest.archiveSha256,
    alphaScoreChanges:common.map(([a,b])=>({before:a.score,after:b.score})),
    betaEnergyChecks:pk.map(c=>({energy:c.energy,phase1Energy:c.phase1Energy,effectiveEnergy:c.effectiveEnergy})),
    betaDisabledControl:"passed",alphaNoEvidenceControl:"passed"
  },null,2));
  console.log("cplfold-parameter-effects: ok");
} finally {
  globalThis.fetch=nativeFetch;
  delete globalThis.self;
}
