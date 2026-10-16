/*
 * RNAcofold batches for the Folding energy card (folding-energy.js).
 *
 * Loads the ViennaRNA WebAssembly build (vendor/viennarna) and folds each
 * pair of arms joined with "&", reporting progress.  Sequences never leave
 * the browser tab.
 */

"use strict";

// the page's ?build= cache-buster, carried to every script and the wasm
const BUILD_QUERY = self.location.search || "";
const ENGINE_DIRECTORY = new URL("./vendor/viennarna/", self.location.href).href;
const ENGINE_GLUE_URL = new URL("vienna-rna.js", ENGINE_DIRECTORY).href + BUILD_QUERY;

importScripts(new URL("./folding-energy.js", self.location.href).href + BUILD_QUERY);

let enginePromise = null;

function getEngine() {
  if (!enginePromise) {
    enginePromise = (async function () {
      try {
        importScripts(ENGINE_GLUE_URL);
      } catch (error) {
        throw new Error("ViennaRNA WebAssembly assets are missing at " + ENGINE_GLUE_URL + ". Build them with scripts/build-vienna-wasm.sh.");
      }
      if (typeof self.createHyb2Vienna !== "function") {
        throw new Error("The ViennaRNA WebAssembly loader did not expose the expected folding module.");
      }
      const module = await self.createHyb2Vienna({
        locateFile: function (file) { return new URL(file, ENGINE_DIRECTORY).href + BUILD_QUERY; },
        print: function () {},
        printErr: function () {},
        noInitialRun: true
      });
      if (typeof module._hyb2_vienna_cofold !== "function") {
        throw new Error("ViennaRNA WebAssembly is missing hyb2_vienna_cofold.");
      }
      return module;
    })();
    enginePromise.catch(function () { enginePromise = null; });
  }
  return enginePromise;
}

self.onmessage = async function (event) {
  const message = event.data || {};
  if (message.type !== "cofold-batch") {
    return;
  }
  try {
    const engine = await getEngine();
    const version = engine.UTF8ToString(engine._hyb2_vienna_version());
    const pairs = message.pairs || [];
    const energies = new Float64Array(pairs.length);
    const fold = self.Hyb2FoldingEnergy.cofold;
    for (let k = 0; k < pairs.length; k += 1) {
      energies[k] = fold(engine, pairs[k][0], pairs[k][1], message.temperature, message.minimumLoop);
      if (k % 25 === 24) {
        self.postMessage({ type: "progress", done: k + 1, total: pairs.length });
      }
    }
    self.postMessage({ type: "result", energies: energies, version: version }, [energies.buffer]);
  } catch (error) {
    self.postMessage({ type: "error", message: error && error.message ? error.message : String(error) });
  }
};
