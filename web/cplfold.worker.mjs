/*
 * Pure-Python CPLfold worker for the static GitHub Pages application.
 *
 * Pyodide, NumPy, the CPLfold source archive and parameter files are all
 * loaded from same-origin build artifacts. Input data never leaves this
 * worker or the browser tab.
 */
import "./support-kernels.js";

const BASELINE_SEQUENCE_LENGTH = 75;
const HARD_MAX_SEQUENCE_LENGTH = 500;
const CAPACITY_PROBE_LENGTH = 75;
const RUNTIME_DIRECTORY = new URL("./vendor/pyodide-cplfold/", self.location.href);
const PYODIDE_MODULE_URL = new URL("pyodide.mjs", RUNTIME_DIRECTORY);
const EXPECTED_CPLFOLD_REVISION = "24bab52";
const EXPECTED_BRIDGE_VERSION = "8";
const WORKER_BUILD = new URL(self.location.href).searchParams.get("build") || "local";

let runtimePromise = null;

self.onmessage = async function (event) {
  const message = event.data || {};
  if (message.type === "cplfold-capacity") {
    await runCapacityProbe(message);
    return;
  }
  if (message.type === "cplfold-bonus-matrix") {
    await runBonusMatrixExport(message);
    return;
  }
  if (message.type === "cplfold-energy-batch") {
    await runEnergyBatch(message);
    return;
  }
  if (message.type !== "cplfold") {
    return;
  }

  try {
    const input = normaliseInput(message);
    const sequence = input.sequence;
    const engine = input.cutpoint === undefined ? "CPLfold" : "CPLcofold";
    const allowPseudoknot = globalThis.Hyb2CplfoldSettings.readTopology(input.cutpoint !== undefined, message.allowPseudoknot);
    const kernel = requestedSupportKernel(message.supportKernel);
    const vienna = needsVienna(message, kernel);
    const maximumSequenceLength = requestedMaximum(message.maxSequenceLength);
    validateSequenceLength(sequence, maximumSequenceLength);
    const requestStartedAt = Date.now();
    postProgress("Loading the local Python WebAssembly runtime", 5, { phase: "runtime" });
    const runtimeStartedAt = Date.now();
    const runtime = await getRuntime();
    if (vienna) await ensureVienna(runtime);
    const runtimeLoadMs = Date.now() - runtimeStartedAt;

    const foldStage = allowPseudoknot
      ? "Running " + engine + " phase 1 and pseudoknot phase 2"
      : "Running " + engine + " phase 1 without pseudoknot search";
    postProgress(
      vienna
        ? pairStage(kernel) + " for the HYB read support"
        : message.evidenceMode === "hyb-blocks"
        ? "Building the HYB bonus matrix"
        : "Preparing sequence-only " + engine,
      46,
      { phase: "evidence" }
    );
    await yieldToEventLoop();
    let foldStagePosted = false;
    const postFoldStage = function () {
      if (!foldStagePosted) {
        foldStagePosted = true;
        postProgress(foldStage, 56, { phase: "cplfold" });
      }
    };
    if (!vienna) {
      postFoldStage();
    }

    const startedAt = Date.now();
    // When arm pairs are computed inside this call, their
    // progress (46-55 %) arrives through pairProgress, then the fold stage.
    const response = runtime.bridge.fold_json(JSON.stringify({
      sequence: sequence,
      cutpoint: input.cutpoint,
      evidenceMode: message.evidenceMode,
      evidenceArms: message.evidenceArms || [],
      beamSize: message.beamSize,
      energyDelta: message.energyDelta,
      maxPhase1: message.maxPhase1,
      energyModel: message.energyModel,
      alpha: message.alpha,
      beta: message.beta,
      allowPseudoknot,
      supportKernel: kernel,
      evidencePairs: message.evidencePairs,
      saturatingQ: message.saturatingQ,
      maxSequenceLength: maximumSequenceLength
    }), vienna ? pairProgress(kernel, 46, 55, postFoldStage) : undefined);
    // If no HYB block reached the pair callback (it never fired), the fold
    // stage is still reported, once fold_json returns, before the result.
    postFoldStage();
    const result = JSON.parse(String(response));
    result.foldElapsedMs = Date.now() - startedAt;
    result.runtimeLoadMs = runtimeLoadMs;
    result.elapsedMs = Date.now() - requestStartedAt;
    result.runtimeManifest = {
      buildCommit: runtime.manifest.buildCommit || "unknown",
      archiveSha256: runtime.manifest.cplfoldArchiveSha256
    };
    if (result.engineVersion !== EXPECTED_CPLFOLD_REVISION || result.bridgeVersion !== EXPECTED_BRIDGE_VERSION) {
      throw new Error("The loaded CPLfold source does not match this web worker build.");
    }
    if (!result.evidence || result.evidence.supportKernel !== kernel) {
      throw new Error("The CPLfold runtime did not use the requested read-support kernel. Reload the runtime.");
    }

    postProgress(
      allowPseudoknot
        ? "Preparing pseudoknot candidates and arc layers"
        : "Preparing pseudoknot-free secondary-structure candidates",
      96,
      { phase: "result" }
    );
    self.postMessage({ type: "complete", result: result });
  } catch (error) {
    self.postMessage({
      type: "error",
      message: readableError(error)
    });
  }
};

// HYB binding energies with CPLcofold: pairs [[armOne, armTwo], ...], each
// folded sequence-only as two strands; replies with the selected
// candidate's energy (kcal/mol) per pair, as the batch energy tool expects.
async function runEnergyBatch(message) {
  try {
    const allowPseudoknot = globalThis.Hyb2CplfoldSettings.readTopology(true, message.allowPseudoknot);
    const pairs = Array.isArray(message.pairs) ? message.pairs : [];
    const runtime = await getRuntime();
    const energies = new Float64Array(pairs.length);
    for (let index = 0; index < pairs.length; index += 1) {
      const input = normaliseInput({ sequence: pairs[index][0], sequenceTwo: pairs[index][1] });
      validateSequenceLength(input.sequence, HARD_MAX_SEQUENCE_LENGTH);
      const result = JSON.parse(String(runtime.bridge.fold_json(JSON.stringify({
        sequence: input.sequence,
        cutpoint: input.cutpoint,
        evidenceMode: "none",
        evidenceArms: [],
        energyModel: message.energyModel,
        allowPseudoknot,
        maxSequenceLength: HARD_MAX_SEQUENCE_LENGTH
      }))));
      if (result.engineVersion !== EXPECTED_CPLFOLD_REVISION || result.bridgeVersion !== EXPECTED_BRIDGE_VERSION) {
        throw new Error("The loaded CPLfold source does not match this web worker build.");
      }
      const energy = Number(result.energy);
      if (!Number.isFinite(energy)) {
        throw new Error("CPLcofold returned no energy for pair " + (index + 1) + ".");
      }
      energies[index] = Math.round(energy * 100) / 100;
    }
    self.postMessage({ type: "result", energies: energies, version: "CPLfold " + EXPECTED_CPLFOLD_REVISION }, [energies.buffer]);
  } catch (error) {
    self.postMessage({ type: "error", message: readableError(error) });
  }
}

async function runCapacityProbe(message) {
  try {
    const kernel = requestedSupportKernel(message.supportKernel);
    if (message.cofold !== undefined && typeof message.cofold !== "boolean") {
      throw new Error("cofold must be a boolean.");
    }
    const allowPseudoknot = globalThis.Hyb2CplfoldSettings.readTopology(message.cofold === true, message.allowPseudoknot);
    const probeLength = probeLengthValue(message.probeLength);
    const sequence = buildProbeSequence(probeLength);
    const cutpoint = message.cofold === true ? Math.floor(probeLength / 2) : undefined;
    const evidenceMode = message.evidenceMode === "hyb-blocks" ? "hyb-blocks" : "none";
    const evidenceArms = evidenceMode === "hyb-blocks" ? buildProbeEvidenceArms(probeLength) : [];
    postProgress("Loading the local Python WebAssembly runtime", 5, {
      phase: "runtime",
      operation: "capacity"
    });
    const runtimeStartedAt = Date.now();
    const runtime = await getRuntime();
    if (needsVienna({evidenceMode}, kernel)) await ensureVienna(runtime, "capacity");
    const runtimeLoadMs = Date.now() - runtimeStartedAt;
    // The page starts its probe timeout here, and elapsedMs excludes the
    // one-off runtime load: the probe measures folding, not downloads.
    self.postMessage({ type: "runtime-ready", operation: "capacity", runtimeLoadMs: runtimeLoadMs });
    const requestStartedAt = Date.now();

    postProgress("Preparing a representative " + probeLength + " nt sequence", 46, {
      phase: "capacity",
      operation: "capacity"
    });
    await yieldToEventLoop();
    postProgress("Running the local CPLfold capacity probe", 56, {
      phase: "capacity",
      operation: "capacity"
    });

    const startedAt = Date.now();
    const response = runtime.bridge.fold_json(JSON.stringify({
      sequence: sequence,
      cutpoint: cutpoint,
      evidenceMode: evidenceMode,
      evidenceArms: evidenceArms,
      supportKernel: kernel,
      beamSize: message.beamSize,
      maxPhase1: message.maxPhase1,
      energyDelta: message.energyDelta,
      energyModel: message.energyModel,
      alpha: message.alpha,
      beta: message.beta,
      allowPseudoknot,
      maxSequenceLength: HARD_MAX_SEQUENCE_LENGTH
    }));
    const result = JSON.parse(String(response));
    if (result.engineVersion !== EXPECTED_CPLFOLD_REVISION || result.bridgeVersion !== EXPECTED_BRIDGE_VERSION) {
      throw new Error("The loaded CPLfold source does not match this web worker build.");
    }
    const foldElapsedMs = Date.now() - startedAt;
    postProgress("Recording the browser capacity estimate", 96, {
      phase: "capacity",
      operation: "capacity"
    });
    self.postMessage({
      type: "capacity-complete",
      sample: {
        length: probeLength,
        foldElapsedMs: foldElapsedMs,
        elapsedMs: Date.now() - requestStartedAt,
        candidateCount: (result.candidates || []).length
      },
      runtimeLoadMs: runtimeLoadMs,
      elapsedMs: Date.now() - requestStartedAt,
      runtimeManifest: {
        buildCommit: runtime.manifest.buildCommit || "unknown",
        archiveSha256: runtime.manifest.cplfoldArchiveSha256
      },
      hardware: browserHardwareProfile(),
      parameters: {
        beamSize: result.parameters && result.parameters.beamSize,
        maxPhase1: result.parameters && result.parameters.maxPhase1,
        energyDelta: result.parameters && result.parameters.energyDelta,
        energyModel: result.parameters && result.parameters.energyModel,
        alpha: result.parameters && result.parameters.alpha,
        beta: result.parameters && result.parameters.beta,
        allowPseudoknot: result.parameters && result.parameters.allowPseudoknot,
        evidenceMode: evidenceMode,
        supportKernel: result.parameters && result.parameters.supportKernel,
        cofold: cutpoint !== undefined
      }
    });
  } catch (error) {
    self.postMessage({
      type: "error",
      operation: "capacity",
      message: readableError(error)
    });
  }
}

async function runBonusMatrixExport(message) {
  try {
    const input = normaliseInput(message);
    const sequence = input.sequence;
    const kernel = requestedSupportKernel(message.supportKernel);
    const vienna = needsVienna(message, kernel);
    const requestStartedAt = Date.now();
    postProgress("Loading the local Python WebAssembly runtime", 5, {
      phase: "runtime",
      operation: "bonus-matrix"
    });
    const runtimeStartedAt = Date.now();
    const runtime = await getRuntime();
    if (vienna) await ensureVienna(runtime, "bonus-matrix");
    const runtimeLoadMs = Date.now() - runtimeStartedAt;

    postProgress(vienna ? pairStage(kernel) + " for the bonus matrix" : "Building the HYB bonus matrix for local download", 58, {
      phase: "evidence",
      operation: "bonus-matrix"
    });
    await yieldToEventLoop();
    const response = runtime.bridge.bonus_matrix_json(JSON.stringify({
      sequence: sequence,
      cutpoint: input.cutpoint,
      evidenceMode: message.evidenceMode === "hyb-blocks" ? "hyb-blocks" : "none",
      evidenceArms: message.evidenceArms || [],
      supportKernel: kernel,
      evidencePairs: message.evidencePairs,
      saturatingQ: message.saturatingQ
    }), vienna ? pairProgress(kernel, 58, 94, null, "bonus-matrix") : undefined);
    const result = JSON.parse(String(response));
    result.runtimeLoadMs = runtimeLoadMs;
    result.elapsedMs = Date.now() - requestStartedAt;
    result.runtimeManifest = {
      buildCommit: runtime.manifest.buildCommit || "unknown",
      archiveSha256: runtime.manifest.cplfoldArchiveSha256
    };
    if (result.engineVersion !== EXPECTED_CPLFOLD_REVISION || result.bridgeVersion !== EXPECTED_BRIDGE_VERSION) {
      throw new Error("The loaded CPLfold source does not match this web worker build.");
    }
    if (!result.evidence || result.evidence.supportKernel !== kernel) {
      throw new Error("The CPLfold runtime did not export the requested read-support kernel. Reload the runtime.");
    }

    postProgress("Preparing the downloadable FASTA and bonus matrix", 96, {
      phase: "result",
      operation: "bonus-matrix"
    });
    self.postMessage({ type: "bonus-matrix-complete", result: result });
  } catch (error) {
    self.postMessage({
      type: "error",
      operation: "bonus-matrix",
      message: readableError(error)
    });
  }
}

function getRuntime() {
  if (!runtimePromise) {
    runtimePromise = initialiseRuntime();
  }
  return runtimePromise;
}

// GitHub Pages caches files for about ten minutes and Pyodide fetches
// pyodide-lock.json itself, so over http(s) the lock gets this build's
// cache-buster: a fresh index.html must not pair with a cached old lock (the
// ViennaRNA wheel it names lives under a hash-named directory, so the lock
// alone decides which wheel is fetched).  packageBaseUrl is passed explicitly
// so the wheels still resolve against the runtime directory, not the query.
// Under file: (Node tests) Pyodide reads the lock with fs, where a query
// string would break the path, so the default lock location is kept; but
// packageBaseUrl is always set there too, to the same directory as a decoded
// path with a trailing slash.  In Node, Pyodide 0.29.4 retries a package file
// it cannot read locally from cdnUrl, and cdnUrl = packageBaseUrl ?? the
// jsDelivr CDN: without it a missing wheel would be downloaded from the
// internet instead of failing.  (Node's loader strips "file://" without
// percent-decoding, so a path is used, not a file: URL.)
// Pure (no worker globals) so scripts/verify/verify-cplfold-worker.mjs can check it.
// indexURL is a file path under file: (Pyodide reads it with fs there).
export function pyodideOptionsFor(runtimeDirectoryUrl, build) {
  const directory = new URL(runtimeDirectoryUrl);
  if (directory.protocol === "file:") {
    let local = decodeURIComponent(directory.pathname);
    if (!local.endsWith("/")) {
      local += "/";
    }
    return { indexURL: local, packageBaseUrl: local };
  }
  const options = { indexURL: directory.href };
  if ((directory.protocol === "http:" || directory.protocol === "https:") && build && build !== "local") {
    const lockUrl = new URL("pyodide-lock.json", directory);
    lockUrl.searchParams.set("build", build);
    options.lockFileURL = lockUrl.href;
    options.packageBaseUrl = directory.href;
  }
  return options;
}

function pyodideOptions() {
  return pyodideOptionsFor(RUNTIME_DIRECTORY.href, WORKER_BUILD);
}

// Pyodide 0.29.4's loadPackage does not reject when a package fails: it logs
// "Failed to load <name>" and resolves with an array that omits it.  This
// captures the error log, checks the package really loaded and throws an
// Error that names the package and the first underlying error.
export async function loadPinnedPackage(pyodide, name) {
  const errors = [];
  let loaded;
  try {
    loaded = await pyodide.loadPackage(name, {
      errorCallback: function (line) { errors.push(String(line)); },
      messageCallback: function () {}
    });
  } catch (error) {
    errors.push(error && error.message ? String(error.message) : String(error));
  }
  // A package loaded earlier is not in the returned array, only in loadedPackages.
  const present = (Array.isArray(loaded) && loaded.some(function (entry) { return entry && entry.name === name; }))
    || Boolean(pyodide.loadedPackages && pyodide.loadedPackages[name]);
  if (present) {
    return;
  }
  throw new Error(packageFailureMessage(name, errors));
}

// One line (readableError keeps only the last line of a message): the
// package, the first real error line, then what to do about it.
const BROWSER_FETCH_FAILURE = /\b(?:Failed to fetch|NetworkError when attempting to fetch resource|Load failed)\b/;

export function packageFailureMessage(name, errors) {
  // Drop one trailing period (the advice adds its own), not an ellipsis.
  const detail = (firstErrorLine(errors) || "Pyodide did not report it as loaded").replace(/([^.])\.$/, "$1");
  const label = name === "viennarna" ? "The local ViennaRNA Python module (viennarna)" : "The pinned Pyodide package " + name;
  const head = label + " could not be loaded: " + detail + (detail.endsWith("...") ? " " : ". ");
  const build = "if you built the site locally, run "
    + (name === "viennarna" ? "scripts/build/build-vienna-pyodide.sh, then " : "") + "scripts/build/build-cplfold-web.sh; on the deployed site, reload the page.";
  if (/No known package with name/i.test(detail)) {
    return head + "This site's pyodide-lock.json does not list it: " + build;
  }
  // "request failed" is any non-OK response: a missing file, or a 5xx while
  // the site is being redeployed.
  if (/\b404\b|not found|ENOENT|no such file|request failed/i.test(detail)) {
    return head + "The file could not be downloaded (missing or server error): " + build;
  }
  // A browser reports a failed fetch(url, {integrity}) SRI check with the same
  // generic TypeError as a network failure (Chrome, Firefox, Safari wording).
  if (BROWSER_FETCH_FAILURE.test(detail)) {
    return head + "A network problem or inconsistent deployed files (a partial or ongoing deploy): reload the page.";
  }
  if (/integrity|digest|sha-?256|not a zip file|BadZipFile|ReadError/i.test(detail)) {
    return head + "The deployed runtime files are inconsistent with each other (a partial or ongoing deploy); reload the page.";
  }
  return head + "Check the connection and reload the page; if it keeps failing, the deployed runtime files may be inconsistent.";
}

// Pyodide logs "The following error occurred while loading <name>:" and then
// the error (often a Python traceback, whose last line is the useful one).
function firstErrorLine(errors) {
  for (const entry of errors || []) {
    const lines = String(entry).split(/\r?\n/).map(function (line) { return line.trim(); }).filter(Boolean);
    if (!lines.length || /^The following error occurred while loading/i.test(lines[0]) && lines.length === 1) {
      continue;
    }
    const line = lines[lines.length - 1].replace(/^(?:[A-Za-z_.]*Error|Exception):\s*/, "");
    return line.length > 240 ? line.slice(0, 237) + "..." : line;
  }
  return "";
}

async function initialiseRuntime() {
  if (typeof WebAssembly !== "object") {
    throw new Error("This browser does not provide WebAssembly, which is required for browser CPLfold.");
  }

  let loadPyodide;
  try {
    ({ loadPyodide } = await import(PYODIDE_MODULE_URL.href));
  } catch (error) {
    throw new Error("The local Pyodide runtime is unavailable. Build the static site with scripts/build/build-cplfold-web.sh.");
  }

  let pyodide;
  try {
    pyodide = await loadPyodide(pyodideOptions());
  } catch (error) {
    throw new Error("The pinned Pyodide assets could not be initialised from this site.");
  }
  postProgress("Loading the pinned local NumPy package", 22, { phase: "numpy" });
  await loadPinnedPackage(pyodide, "numpy");

  postProgress("Loading the vendored pure-Python CPLfold source", 34, { phase: "source" });
  let sourceBundle;
  try {
    sourceBundle = await loadVerifiedSourceBundle();
  } catch (error) {
    throw new Error(error && error.message
      ? error.message
      : "The local CPLfold Python archive is unavailable. Build the static site with scripts/build/build-cplfold-web.sh.");
  }

  try {
    pyodide.FS.writeFile("/tmp/cplfold-python.zip", sourceBundle.archive);
    pyodide.runPython([
      "import pathlib, sys, zipfile",
      "target = pathlib.Path('/opt/hyb2-cplfold')",
      "target.mkdir(parents=True, exist_ok=True)",
      "with zipfile.ZipFile('/tmp/cplfold-python.zip') as bundle:",
      "    bundle.extractall(target)",
      "source = str(target / 'cplfold')",
      "if source not in sys.path:",
      "    sys.path.insert(0, source)"
    ].join("\n"));
    const bridge = pyodide.pyimport("cplfold_web");
    if (!bridge || typeof bridge.fold_json !== "function" || typeof bridge.bonus_matrix_json !== "function") {
      throw new Error("Missing CPLfold bridge functions");
    }
    return { pyodide: pyodide, bridge: bridge, manifest: sourceBundle.manifest };
  } catch (error) {
    throw new Error("The pure-Python CPLfold source could not be imported in Pyodide.");
  }
}

function requestedSupportKernel(value) {
  const kernel = value == null || value === "" ? globalThis.Hyb2SupportKernels.defaultKernel : String(value).trim().toLowerCase();
  if (!kernelOption(kernel)) {
    throw new Error("Choose a supported CPLfold read-support kernel.");
  }
  return kernel;
}

// Both pair estimators use the same locally bundled, SHA-verified ViennaRNA wheel.
let viennaPromise = null;

function kernelOption(kernel) {
  return globalThis.Hyb2SupportKernels.options.find(option => option.value === kernel);
}

function needsVienna(message, kernel) {
  // Only omission requests an arm fold. Pass explicit counts, including []
  // and invalid inputs, to the bridge for exactly the same validation as native.
  return message.evidenceMode === "hyb-blocks" && !!kernelOption(kernel).pairModel && message.evidencePairs === undefined;
}

function pairStage(kernel) {
  return kernelOption(kernel).pairModel + " of each chimera's arms";
}

// progress(blocksDone, blocks) for the bridge: the pair step's share of the
// bar runs from `from` to `to` %; done() follows the last block.
function pairProgress(kernel, from, to, done, operation) {
  return function (completed, total) {
    const finished = Number(completed) >= Number(total);
    postProgress(pairStage(kernel) + " " + Number(completed).toLocaleString("en-US") + " / " + Number(total).toLocaleString("en-US"),
      from + Math.round((to - from) * Math.min(1, Number(completed) / Math.max(1, Number(total)))),
      Object.assign({ phase: kernelOption(kernel).pairModel === "RNAcofold" ? "rna-cofold" : "rna-duplex",
        completed: Number(completed), total: Number(total) }, operation ? { operation: operation } : {}));
    if (finished && done) {
      done();
    }
  };
}

async function ensureVienna(runtime, operation) {
  if (!viennaPromise) {
    postProgress("Loading the local ViennaRNA Python module", 40, Object.assign({ phase: "viennarna" },
      operation ? { operation: operation } : {}));
    viennaPromise = loadPinnedPackage(runtime.pyodide, "viennarna").then(function () {
      try {
        runtime.pyodide.runPython("import RNA");
      } catch (error) {
        const lines = String(error && error.message || error).split(/\r?\n/).filter(function (line) { return line.trim(); });
        throw new Error("The local ViennaRNA Python module (viennarna) loaded but import RNA failed: "
          + (lines.length ? lines[lines.length - 1].trim() : "unknown error") + ". Rebuild it with scripts/build/build-vienna-pyodide.sh, then scripts/build/build-cplfold-web.sh.");
      }
    });
    // Reset on failure so the next request retries the load.
    viennaPromise.catch(function () { viennaPromise = null; });
  }
  await viennaPromise;
}

async function loadVerifiedSourceBundle() {
  const manifestUrl = new URL("build-manifest.json", RUNTIME_DIRECTORY);
  manifestUrl.searchParams.set("build", WORKER_BUILD);
  const manifestResponse = await fetch(manifestUrl.href, { cache: "no-store" });
  if (!manifestResponse.ok) {
    throw new Error("The local CPLfold build manifest is unavailable. Build the static site with scripts/build/build-cplfold-web.sh.");
  }
  const manifest = await manifestResponse.json();
  const hash = String(manifest.cplfoldArchiveSha256 || "").toLowerCase();
  const archiveFile = String(manifest.cplfoldArchiveFile || "");
  if (manifest.cplfoldRevision !== EXPECTED_CPLFOLD_REVISION ||
      manifest.bridgeVersion !== EXPECTED_BRIDGE_VERSION ||
      !/^[a-f0-9]{64}$/.test(hash) ||
      archiveFile !== "cplfold-python-" + hash + ".zip") {
    throw new Error("The local CPLfold build manifest is incompatible with this worker.");
  }
  if (WORKER_BUILD !== "local" && manifest.buildCommit !== WORKER_BUILD) {
    throw new Error("The CPLfold runtime manifest belongs to a different deployed Git commit. Reload this page.");
  }

  const archiveUrl = new URL(archiveFile, RUNTIME_DIRECTORY);
  const archiveResponse = await fetch(archiveUrl.href, { cache: "force-cache" });
  if (!archiveResponse.ok) {
    throw new Error("The content-addressed CPLfold Python archive is unavailable.");
  }
  const archive = new Uint8Array(await archiveResponse.arrayBuffer());
  const actualHash = await sha256Hex(archive);
  if (actualHash !== hash) {
    throw new Error("The CPLfold Python archive failed its SHA-256 integrity check.");
  }
  return { archive: archive, manifest: manifest };
}

async function sha256Hex(bytes) {
  if (!self.crypto || !self.crypto.subtle) {
    throw new Error("This browser cannot verify the CPLfold runtime archive with SHA-256.");
  }
  const digest = new Uint8Array(await self.crypto.subtle.digest("SHA-256", bytes));
  return Array.from(digest).map(function (value) { return value.toString(16).padStart(2, "0"); }).join("");
}

function requestedMaximum(value) {
  if (value === undefined || value === null || value === "") {
    return BASELINE_SEQUENCE_LENGTH;
  }
  const maximum = Number(value);
  if (!Number.isInteger(maximum) || maximum < 1) {
    throw new Error("Browser CPLfold maximum sequence length must be a positive whole number.");
  }
  if (maximum > HARD_MAX_SEQUENCE_LENGTH) {
    throw new Error("Browser CPLfold has a hard safety ceiling of " + HARD_MAX_SEQUENCE_LENGTH + " nt.");
  }
  return maximum;
}

function validateSequenceLength(sequence, maximum) {
  if (sequence.length > HARD_MAX_SEQUENCE_LENGTH) {
    throw new Error(
      "Browser CPLfold has a hard safety ceiling of " + HARD_MAX_SEQUENCE_LENGTH +
      " nt in this pure-Python Pyodide build. Select a shorter reference region."
    );
  }
  if (sequence.length > maximum) {
    throw new Error(
      "Browser CPLfold is limited to " + maximum +
      " nt for this capacity-tested request. Run the browser capacity test again or select a shorter region."
    );
  }
}

// Keep all pair/read coordinates in compact A+B space. The nick is metadata,
// never a nucleotide; validate it before loading the relatively large runtime.
function normaliseInput(message) {
  if (message.cofold !== undefined && typeof message.cofold !== "boolean") {
    throw new Error("cofold must be a boolean.");
  }
  const clean = function (value) {
    if (value != null && typeof value !== "string") throw new Error("RNA sequence must be text.");
    const { sequence, invalid } = globalThis.Hyb2FoldText.read(value);
    const bad = invalid.filter(base => base !== "&"); // the strand layout below validates the separator
    if (bad.length) throw new Error("RNA sequence contains unsupported symbols: " + bad.join(", "));
    return sequence;
  };
  let sequence = clean(message.sequence);
  let cutpoint = message.cutpoint;
  if (cutpoint === null) { cutpoint = undefined; }
  if (message.sequenceTwo !== undefined && message.sequenceTwo !== null) {
    const second = clean(message.sequenceTwo);
    if (!sequence || !second || sequence.includes("&") || second.includes("&")) {
      throw new Error("Supply two nonempty RNA strands without '&' when using sequenceTwo.");
    }
    if (cutpoint !== undefined && cutpoint !== sequence.length) {
      throw new Error("cutpoint does not match the length of RNA A.");
    }
    cutpoint = sequence.length;
    sequence += second;
  } else if (sequence.includes("&")) {
    const strands = sequence.split("&");
    if (strands.length !== 2 || !strands[0] || !strands[1]) {
      throw new Error("CPLcofold requires exactly two nonempty strands separated by '&'.");
    }
    if (cutpoint !== undefined && cutpoint !== strands[0].length) {
      throw new Error("cutpoint does not match the '&' strand break.");
    }
    cutpoint = strands[0].length;
    sequence = strands.join("");
  }
  if (!sequence) {
    throw new Error("CPLfold requires a nonempty RNA sequence.");
  }
  if (cutpoint !== undefined && (!Number.isInteger(cutpoint) || cutpoint <= 0 || cutpoint >= sequence.length)) {
    throw new Error("cutpoint must separate two nonempty RNA strands.");
  }
  if (message.cofold === true && cutpoint === undefined) {
    throw new Error("CPLcofold requires two RNA strands and a strand break.");
  }
  return { sequence: sequence, cutpoint: cutpoint };
}

function probeLengthValue(value) {
  if (value === undefined || value === null || value === "") {
    return CAPACITY_PROBE_LENGTH;
  }
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed < 25 || parsed > CAPACITY_PROBE_LENGTH) {
    throw new Error("The browser CPLfold capacity probe length must be a whole number from 25 to " + CAPACITY_PROBE_LENGTH + ".");
  }
  return parsed;
}

function buildProbeSequence(length) {
  const motif = "GCAU";
  let sequence = "";
  while (sequence.length < length) {
    sequence += motif;
  }
  return sequence.slice(0, length);
}

function buildProbeEvidenceArms(length) {
  const armLength = Math.min(8, Math.max(4, Math.floor(length / 8)));
  return [{
    oneStart: 2,
    oneEnd: 1 + armLength,
    twoStart: length - armLength,
    twoEnd: length - 1
  }];
}

function browserHardwareProfile() {
  const browserNavigator = typeof navigator === "object" ? navigator : null;
  return {
    hardwareConcurrency: browserNavigator && Number.isInteger(browserNavigator.hardwareConcurrency)
      ? browserNavigator.hardwareConcurrency
      : null,
    deviceMemory: browserNavigator && Number.isFinite(Number(browserNavigator.deviceMemory))
      ? Number(browserNavigator.deviceMemory)
      : null
  };
}

function postProgress(stage, percent, detail) {
  self.postMessage(Object.assign({ type: "progress", stage: stage, percent: percent }, detail || {}));
}

function readableError(error) {
  const message = error && error.message ? String(error.message) : String(error || "Browser CPLfold could not finish the prediction.");
  const lines = message.split(/\r?\n/).map(function (line) { return line.trim(); }).filter(Boolean);
  const last = lines.length ? lines[lines.length - 1] : message;
  return last.replace(/^(ValueError|RuntimeError|Error):\s*/, "") || "Browser CPLfold could not finish the prediction.";
}

function yieldToEventLoop() {
  return new Promise(function (resolve) { setTimeout(resolve, 0); });
}
