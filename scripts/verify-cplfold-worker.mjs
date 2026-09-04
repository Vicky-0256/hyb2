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
    beta: 0
  }
});

const failure = messages.find(function (message) { return message.type === "error"; });
assert.equal(failure, undefined, failure && failure.message);
assert.ok(messages.some(function (message) { return message.type === "progress" && message.phase === "source"; }));
const completion = messages.find(function (message) { return message.type === "complete"; });
assert.ok(completion, "the actual worker message path must return a completed fold");
assert.equal(completion.result.bridgeVersion, "2");
assert.equal(completion.result.engineVersion, "af49f8e");
assert.equal(completion.result.dotBracket, "..(((((..[[[[)))))......]]]]");
assert.match(completion.result.runtimeManifest.archiveSha256, /^[a-f0-9]{64}$/);
assert.ok(completion.result.elapsedMs >= completion.result.foldElapsedMs);
assert.ok(completion.result.elapsedMs >= completion.result.runtimeLoadMs);

globalThis.fetch = nativeFetch;
delete globalThis.self;
process.stdout.write("cplfold-worker-contract: ok\n");
