/*
 * Pure-Python CPLfold worker for the static GitHub Pages application.
 *
 * Pyodide, NumPy, the CPLfold source archive and parameter files are all
 * loaded from same-origin build artifacts. Input data never leaves this
 * worker or the browser tab.
 */

const BASELINE_SEQUENCE_LENGTH = 75;
const HARD_MAX_SEQUENCE_LENGTH = 500;
const CAPACITY_PROBE_LENGTH = 75;
const RUNTIME_DIRECTORY = new URL("./vendor/pyodide-cplfold/", self.location.href);
const PYODIDE_MODULE_URL = new URL("pyodide.mjs", RUNTIME_DIRECTORY);
const EXPECTED_CPLFOLD_REVISION = "24bab52";
const EXPECTED_BRIDGE_VERSION = "6";
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
    const maximumSequenceLength = requestedMaximum(message.maxSequenceLength);
    validateSequence(sequence, maximumSequenceLength);
    const requestStartedAt = Date.now();
    postProgress("Loading the local Python WebAssembly runtime", 5, { phase: "runtime" });
    const runtimeStartedAt = Date.now();
    const runtime = await getRuntime();
    const runtimeLoadMs = Date.now() - runtimeStartedAt;

    postProgress(
      message.evidenceMode === "hyb-blocks"
        ? "Building the HYB block bonus matrix"
        : "Preparing sequence-only " + engine,
      46,
      { phase: "evidence" }
    );
    await yieldToEventLoop();
    postProgress(
      message.allowPseudoknot === false
        ? "Running " + engine + " phase 1 without pseudoknot search"
        : "Running " + engine + " phase 1 and pseudoknot phase 2",
      56,
      { phase: "cplfold" }
    );

    const startedAt = Date.now();
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
      allowPseudoknot: message.allowPseudoknot !== false,
      maxSequenceLength: maximumSequenceLength
    }));
    const result = JSON.parse(String(response));
    result.foldElapsedMs = Date.now() - startedAt;
    result.runtimeLoadMs = runtimeLoadMs;
    result.elapsedMs = Date.now() - requestStartedAt;
    result.runtimeManifest = {
      buildCommit: runtime.manifest.buildCommit,
      archiveSha256: runtime.manifest.cplfoldArchiveSha256
    };
    if (result.engineVersion !== EXPECTED_CPLFOLD_REVISION || result.bridgeVersion !== EXPECTED_BRIDGE_VERSION) {
      throw new Error("The loaded CPLfold source does not match this web worker build.");
    }

    postProgress(
      message.allowPseudoknot === false
        ? "Preparing pseudoknot-free secondary-structure candidates"
        : "Preparing pseudoknot candidates and arc layers",
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
    const pairs = Array.isArray(message.pairs) ? message.pairs : [];
    const runtime = await getRuntime();
    const energies = new Float64Array(pairs.length);
    for (let index = 0; index < pairs.length; index += 1) {
      const input = normaliseInput({ sequence: pairs[index][0], sequenceTwo: pairs[index][1] });
      validateSequence(input.sequence, HARD_MAX_SEQUENCE_LENGTH);
      const result = JSON.parse(String(runtime.bridge.fold_json(JSON.stringify({
        sequence: input.sequence,
        cutpoint: input.cutpoint,
        evidenceMode: "none",
        evidenceArms: [],
        energyModel: message.energyModel,
        allowPseudoknot: message.allowPseudoknot === true,
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
    if (message.cofold !== undefined && typeof message.cofold !== "boolean") {
      throw new Error("cofold must be a boolean.");
    }
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
      beamSize: message.beamSize,
      maxPhase1: message.maxPhase1,
      energyDelta: message.energyDelta,
      energyModel: message.energyModel,
      alpha: message.alpha,
      beta: message.beta,
      allowPseudoknot: message.allowPseudoknot !== false,
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
        buildCommit: runtime.manifest.buildCommit,
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
    const requestStartedAt = Date.now();
    postProgress("Loading the local Python WebAssembly runtime", 5, {
      phase: "runtime",
      operation: "bonus-matrix"
    });
    const runtimeStartedAt = Date.now();
    const runtime = await getRuntime();
    const runtimeLoadMs = Date.now() - runtimeStartedAt;

    postProgress("Building the HYB block bonus matrix for local download", 58, {
      phase: "evidence",
      operation: "bonus-matrix"
    });
    await yieldToEventLoop();
    const response = runtime.bridge.bonus_matrix_json(JSON.stringify({
      sequence: sequence,
      cutpoint: input.cutpoint,
      evidenceMode: message.evidenceMode === "hyb-blocks" ? "hyb-blocks" : "none",
      evidenceArms: message.evidenceArms || []
    }));
    const result = JSON.parse(String(response));
    result.runtimeLoadMs = runtimeLoadMs;
    result.elapsedMs = Date.now() - requestStartedAt;
    result.runtimeManifest = {
      buildCommit: runtime.manifest.buildCommit,
      archiveSha256: runtime.manifest.cplfoldArchiveSha256
    };
    if (result.engineVersion !== EXPECTED_CPLFOLD_REVISION || result.bridgeVersion !== EXPECTED_BRIDGE_VERSION) {
      throw new Error("The loaded CPLfold source does not match this web worker build.");
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

async function initialiseRuntime() {
  if (typeof WebAssembly !== "object") {
    throw new Error("This browser does not provide WebAssembly, which is required for browser CPLfold.");
  }

  let loadPyodide;
  try {
    ({ loadPyodide } = await import(PYODIDE_MODULE_URL.href));
  } catch (error) {
    throw new Error("The local Pyodide runtime is unavailable. Build the static site with scripts/build-cplfold-web.sh.");
  }

  let pyodide;
  try {
    const runtimeIndex = RUNTIME_DIRECTORY.protocol === "file:"
      ? decodeURIComponent(RUNTIME_DIRECTORY.pathname)
      : RUNTIME_DIRECTORY.href;
    pyodide = await loadPyodide({ indexURL: runtimeIndex });
    postProgress("Loading the pinned local NumPy package", 22, { phase: "numpy" });
    await pyodide.loadPackage("numpy");
  } catch (error) {
    throw new Error("The pinned Pyodide or NumPy assets could not be initialised from this site.");
  }

  postProgress("Loading the vendored pure-Python CPLfold source", 34, { phase: "source" });
  let sourceBundle;
  try {
    sourceBundle = await loadVerifiedSourceBundle();
  } catch (error) {
    throw new Error(error && error.message
      ? error.message
      : "The local CPLfold Python archive is unavailable. Build the static site with scripts/build-cplfold-web.sh.");
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

async function loadVerifiedSourceBundle() {
  const manifestUrl = new URL("build-manifest.json", RUNTIME_DIRECTORY);
  manifestUrl.searchParams.set("build", WORKER_BUILD);
  const manifestResponse = await fetch(manifestUrl.href, { cache: "no-store" });
  if (!manifestResponse.ok) {
    throw new Error("The local CPLfold build manifest is unavailable. Build the static site with scripts/build-cplfold-web.sh.");
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

function validateSequence(sequence, maximum) {
  if (!sequence) {
    throw new Error("Choose a valid RNA sequence first.");
  }
  if (/[^ACGU]/.test(sequence)) {
    throw new Error("CPLfold accepts only A, C, G and U.");
  }
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
    return String(value === undefined || value === null ? "" : value).replace(/\s/g, "").toUpperCase().replace(/T/g, "U");
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
  if (!sequence || /[^ACGU]/.test(sequence)) {
    throw new Error("CPLfold accepts nonempty RNA strands containing only A, C, G and U.");
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
