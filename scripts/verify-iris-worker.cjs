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

const workerSource = path.resolve(__dirname, "..", "web", "structure.worker.js");
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

function waitForTerminal(worker, messages, request) {
  return new Promise(function (resolve, reject) {
    const timeout = setTimeout(function () {
      reject(new Error("Timed out waiting for the ViennaRNA structure worker result."));
    }, 30000);

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


async function main() {
 const worker=new Worker(shimSource,{eval:true});
 const request={type:"iris-fold",sequence:"GGCGCGGCACCGUCCGCGGAACAAACGG",evidenceArms:[
 {oneStart:3,oneEnd:8,twoStart:13,twoEnd:18},
 {oneStart:10,oneEnd:14,twoStart:23,twoEnd:27}
 ],parameters:{K:2,C:20,fraction:0.75,kRange:"4 5 6 7"}};
 try {
  const response=await waitForTerminal(worker,[],request);
  assert.equal(response.type,"complete",response.message);
  const r=response.result;
  assert.equal(r.engine,"IRIS");
  assert.equal(r.candidates.length,2);
  assert.ok(Math.abs(r.candidates.reduce((s,c)=>s+c.weight,0)-1)<1e-10);
  assert.ok(r.candidates.every(c=>c.weight>=0 && c.pairs.length && Number.isFinite(c.energy)));
  assert.equal(r.dotBracket,r.candidates[0].dotBracket);
  assert.ok(r.ensemble.testedEnsembles>0);
  // IRIS results carry RNAcofold evidence so the drawing is coloured by COMRADES support.
  assert.equal(r.comradesEvidence.inputRecords,2);
  assert.ok(Array.isArray(r.comradesEvidence.pairs)&&r.comradesEvidence.pairs.every(p=>p.one>=1&&p.two<=request.sequence.length&&p.count>=1));
  const scored=await waitForTerminal(worker,[],{type:"comrades-evidence",sequence:request.sequence,evidenceArms:request.evidenceArms});
  assert.equal(scored.type,"complete",scored.message);
  assert.deepEqual(scored.evidence.pairs,r.comradesEvidence.pairs);
  fs.writeFileSync("/tmp/hyb2-iris-test-result.json",JSON.stringify({...r,sequence:request.sequence,label:"Synthetic IRIS test",rawHybArms:request.evidenceArms},null,2));
  console.log("iris-worker: ok",JSON.stringify(r.ensemble));
  for (const invalid of [{evidenceArms:[]},{sequence:"A".repeat(301)},{parameters:{K:4}},{parameters:{C:51}},{parameters:{fraction:1}}]) {
   const failure=await waitForTerminal(worker,[],{...request,...invalid});
   assert.equal(failure.type,"error");
  }
  console.log("iris-worker-invalid-inputs: ok");
 } finally {await worker.terminate();}
}
main().catch(e=>{console.error(e);process.exitCode=1;});
