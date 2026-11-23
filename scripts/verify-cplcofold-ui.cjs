"use strict";
// UI/controller contracts only: no folding runtime or network requests.
const assert = require("node:assert/strict"), fs = require("node:fs"), path = require("node:path"), vm = require("node:vm");
const coordinateCases = require("../desktop/runtime/tests/coordinate_cases.json");
const root = path.resolve(__dirname, ".."), workers = [];
let workerRequests = 0;
class Worker {
  constructor(url) { this.url = url; workers.push(this); }
  postMessage(message) { workerRequests++; this.message = message; }
  terminate() { this.terminated = true; }
}
class Element {
  constructor(tag) { this.tag = tag; this.attributes = {}; this.children = []; }
  setAttribute(key, value) { this.attributes[key] = String(value); }
  getAttribute(key) { return this.attributes[key]; }
  appendChild(child) { this.children.push(child); return child; }
}
const document = {querySelector: () => null, documentElement: {dataset: {}}, createElementNS: (_, tag) => new Element(tag)};
const context = vm.createContext({console, Worker, document,
  getComputedStyle: () => ({getPropertyValue: () => "#123456"}),
  window: {}
});
for (const file of ["support-kernels.js", "analysis-data.js", "comrades-analysis.js", "analysis-pages.js", "structure-controller.js", "structure-ui.js", "analysis-ui.js"]) {
  vm.runInContext(fs.readFileSync(path.join(root, "web", file), "utf8"), context, {filename: file});
}
const {Hyb2Pages: pages, Hyb2Structure: controller, Hyb2StructureUI: ui, Hyb2UI: controls} = context.window;
const plain = value => JSON.parse(JSON.stringify(value));
const api = {toasts: [], downloads: [], copies: [], navigations: [], navigate(page) { this.navigations.push(page); }, render() {}, showToast(message) { this.toasts.push(message); },
  download(name, contents) { this.downloads.push({name, contents}); }, copyText(text) { this.copies.push(text); }};
function state(overrides = {}) {
  return {summary: {rnaNames: []}, records: [], structure: {
    engine: "cplfold", cplfoldMode: "cofold", source: "paste", pastedSequence: "GGG", cplfoldSequenceTwo: "CCC",
    cplfoldEvidence: "none", cplfoldBeam: "20", cplfoldMaxPhase1: "1", cplfoldEnergyDelta: "0",
    cplfoldEnergyModel: "DP09", cplfoldAlpha: "0.5", cplfoldBeta: "0", cplfoldAllowPseudoknot: false,
    status: "idle", result: null, viewMode: "folded", ...overrides
  }};
}
let current = state({pastedSequence: " ggt ", cplfoldSequenceTwo: " aCC "});
const numbered = state({pastedSequence: "\ufeff>label & 1\r1 GGGGAAAACCCC&ggttttcccc", cplfoldSequenceTwo: ""});
const numberedInfo = pages.getStructureSequence(numbered);
assert.deepEqual([numberedInfo.sequence, numberedInfo.cutpoint], ["GGGGAAAACCCCGGUUUUCCCC", 12]);
assert.match(pages.getStructureSequence(state({pastedSequence: ">a\nggg\n>b\nccc"})).error, /2 FASTA records/);
assert.deepEqual(plain(context.window.Hyb2Data.normaliseFoldSequence("1 ggt", false).invalid), ["1"]);
// Removing U from the input contract must block even a DNA T normalized to U.
{
  const rules = context.Hyb2FoldText, original = rules.alphabet;
  try {
    rules.alphabet = "ACG";
    const count = workers.length;
    assert.equal(controller.predict(numbered, api), false);
    assert.equal(workers.length, count);
    assert.equal(pages.getStructureSequence(state({pastedSequence: "ggg&ccc", cplfoldSequenceTwo: ""})).displaySequence, "GGG&CCC");
  } finally { rules.alphabet = original; }
}
let info = pages.getStructureSequence(current);
assert.deepEqual(plain([info.sequence, info.displaySequence, info.cutpoint, info.strandLengths, info.error]), ["GGUACC", "GGU&ACC", 3, [3, 3], ""]);
assert.match(pages.renderStructure(current), /Two local regions · CPLcofold/);
assert.match(pages.renderStructure(current), /data-key="cplfoldSequenceTwo"/);
assert.match(pages.renderStructure(current), /Predict CPLcofold candidates/);
assert.doesNotMatch(pages.renderStructure(current), /value="CC09"|value="RE"/);
for (const [a, b] of [["G&", ""], ["&C", ""], ["G&C&A", ""], ["G&C", "A"], ["", "C"], ["G", ""], ["GN", "C"], ["G", "C&"]]) {
  const invalid = state({pastedSequence: a, cplfoldSequenceTwo: b});
  assert.ok(pages.getStructureSequence(invalid).error, `${a}/${b} must fail`);
  const count = workers.length;
  assert.equal(controller.predict(invalid, api), false); assert.equal(workers.length, count);
}
const joined = pages.getStructureSequence(state({pastedSequence: "GGG&CCC", cplfoldSequenceTwo: ""}));
assert.equal(joined.sequence, "GGGCCC"); assert.equal(joined.cutpoint, 3);
assert.ok(pages.getStructureSequence(state({cplfoldMode: "single", pastedSequence: "GGG&CCC"})).error);
for (const length of [1, 4]) {
  const target = state({pastedSequence: ">A\r1 " + "g".repeat(length), cplfoldSequenceTwo: "c\u0085".repeat(length)});
  assert.equal(pages.getStructureSequence(target).displaySequence, "G".repeat(length) + "&" + "C".repeat(length));
  assert.equal(controller.predict(target, api), true);
  assert.equal(workers.at(-1).message.cutpoint, length);
  controller.cancel(target);
}
{
  const rules = context.Hyb2FoldText;
  const original = {minSequenceNt: rules.minSequenceNt, minPastedNt: rules.minPastedNt, minWindowNt: rules.minWindowNt};
  try {
    Object.assign(rules, {minSequenceNt: 3, minPastedNt: 20, minWindowNt: 30});
    for (const [a, b] of [["GG", "CCC"], ["GGG", "CC"], ["GG&CCC", ""], ["GGG&CC", ""]]) {
      const target = state({pastedSequence: a, cplfoldSequenceTwo: b});
      const before = workers.length;
      assert.match(pages.getStructureSequence(target).error, /at least 3 nt/);
      assert.equal(controller.predict(target, api), false);
      assert.equal(workers.length, before);
    }
    const target = state({pastedSequence: "GGG", cplfoldSequenceTwo: "CCC"});
    assert.equal(controller.predict(target, api), true,
      "two pasted fragments do not inherit the single-paste or single-window cutoff");
    controller.cancel(target);
  } finally { Object.assign(rules, original); }
}
const changed = state({cplfoldMode: "single", cplfoldEnergyModel: "CC09"});
controls.handleChange(changed, {dataset: {feature: "structure-control", key: "cplfoldMode"}, value: "cofold", type: "select-one"}, api);
assert.equal(changed.structure.cplfoldEnergyModel, "DP09"); assert.equal(changed.structure.workflow, "custom");
current = state(); assert.equal(controller.predict(current, api), true);
assert.match(workers.at(-1).url, /cplfold\.worker\.mjs/);
assert.deepEqual(plain([workers.at(-1).message.sequence, workers.at(-1).message.cutpoint]), ["GGGCCC", 3]);
assert.equal(workers.at(-1).message.allowPseudoknot, false); controller.cancel(current);
function assertRejectsLocalOptions(target, issue) {
  const count = workers.length, requests = workerRequests;
  const downloads = api.downloads.length, toasts = api.toasts.length;
  assert.equal(ui.handleAction("download-local-cplfold-input", target, api), true);
  assert.equal(api.downloads.length, downloads, "no partial FASTA or bonus TSV");
  assert.deepEqual([workers.length, workerRequests], [count, requests], "no worker started or sent a request");
  assert.equal(api.toasts.length, toasts + 1);
  const message = api.toasts.at(-1);
  assert.match(message, issue);
  assert.doesNotMatch(message, /bin\/cpl(?:co)?fold/);
  assert.throws(() => ui.localCplfoldCommand(target.structure, target.structure.cplfoldEvidence === "hyb-blocks"), {message});
  return message;
}
function assertRejectsModel(target) {
  const workerCount = workers.length, runId = target.structure.runId;
  const engine = target.structure.cplfoldMode === "cofold" ? "CPLcofold" : "CPLfold";
  const message = assertRejectsLocalOptions(target, new RegExp(engine + " energy model"));
  assert.ok(message.includes(context.Hyb2EngineParameters[engine.toLowerCase()].models.map(model => model.value).join(", ")));
  assert.equal(controller.predict(target, api), false);
  assert.equal(api.toasts.at(-1), message);
  assert.equal(controller.probeCplfoldCapacity(target, api), false);
  assert.equal(api.toasts.at(-1), message);
  assert.equal(workers.length, workerCount);
  assert.equal(target.structure.runId, runId, "no prediction, probe or matrix request, even on a reused worker");
}
assertRejectsModel(state({cplfoldEnergyModel: "CC09"}));
assertRejectsModel(state({cplfoldMode: "single", cplfoldEnergyModel: "retired", pastedSequence: "GGGCCC"}));
for (const [mode, input] of [["cofold", "dp03"], ["cofold", "dP09"], ["single", "cc09"]]) {
  const target = state({cplfoldMode: mode, cplfoldEnergyModel: input, pastedSequence: "GGGCCC"});
  assert.equal(controller.predict(target, api), true);
  assert.equal(target.structureWorker.message.energyModel, input.toUpperCase());
  controller.cancel(target);
  const downloads = api.downloads.length;
  ui.handleAction("download-local-cplfold-input", target, api);
  assert.deepEqual(api.downloads.slice(downloads).map(file => file.name), ["cplfold-input.fasta"]);
  assert.ok(api.toasts.at(-1).includes("--model " + input.toUpperCase() + " "));
}
const badTopology = state({cplfoldAllowPseudoknot: "false"});
const workerCount = workers.length, downloadCount = api.downloads.length;
assert.doesNotMatch(pages.renderStructure(badTopology), /data-key="cplfoldAllowPseudoknot" checked/);
assert.equal(controller.predict(badTopology, api), false);
assert.equal(controller.probeCplfoldCapacity(badTopology, api), false);
ui.handleAction("download-local-cplfold-input", badTopology, api);
assert.match(api.toasts.at(-1), /must be a boolean/);
assert.equal(workers.length, workerCount);
assert.equal(api.downloads.length, downloadCount);
// A changed catalog must drive the picker, mode transition, local command,
// request payload and limits, without a second list of models in the UI.
{
  const catalog = context.Hyb2EngineParameters;
  const original = catalog.cplcofold;
  catalog.cplcofold = {...original, models: [{value: "DP03", label: "DP03"}, {value: "NEW", label: "New cofold model"}], defaultModel: "NEW",
    ranges: {...original.ranges, energyDelta: [0, 8], beamSize: [1, 10]}, defaults: {...original.defaults, beamSize: 7, allowPseudoknot: true}};
  try {
    const target = state({cplfoldMode: "single"});
    const retained = state({cplfoldMode: "single", cplfoldEnergyModel: "DP03"});
    controls.handleChange(retained, {dataset: {feature: "structure-control", key: "cplfoldMode"}, value: "cofold", type: "select-one"}, api);
    assert.equal(retained.structure.cplfoldEnergyModel, "DP03");
    controls.handleChange(target, {dataset: {feature: "structure-control", key: "cplfoldMode"}, value: "cofold", type: "select-one"}, api);
    assert.equal(target.structure.cplfoldEnergyModel, "NEW");
    delete target.structure.cplfoldBeam;
    delete target.structure.cplfoldAllowPseudoknot;
    assert.match(pages.renderStructure(target), /value="true"[^>]*data-key="cplfoldAllowPseudoknot" checked/);
    assert.match(ui.localCplfoldCommand(target.structure, false), /--mode pseudoknot/);
    assert.match(pages.renderStructure(target), /value="7" selected>7 · browser default/);
    assert.match(ui.localCplfoldCommand(target.structure, false), /--beam 7 /);
    assert.match(pages.renderStructure(target), /value="NEW" selected>New cofold model/);
    for (const input of ["new", undefined, null, ""]) {
      target.structure.cplfoldEnergyModel = input;
      assert.match(ui.localCplfoldCommand(target.structure, false), /--model NEW\b/);
      assert.equal(controller.predict(target, api), true);
      assert.equal(workers.at(-1).message.energyModel, "NEW");
      assert.equal(workers.at(-1).message.beamSize, 7);
      assert.equal(workers.at(-1).message.allowPseudoknot, true);
      controller.cancel(target);
    }
    target.structure.cplfoldEnergyDelta = "9";
    assert.equal(controller.predict(target, api), false);
    assert.match(api.toasts.at(-1), /from 0 to 8/);
    const stale = state({cplfoldBeam: "7"}); // DP09 was removed; all other parameters are valid.
    const html = pages.renderStructure(stale);
    assert.match(html, /selected disabled>Choose a supported model/);
    assert.doesNotMatch(html, /bin\/cplcofold --sequence-file/);
    assertRejectsModel(stale);
  } finally { catalog.cplcofold = original; }
}
// A desktop runtime declaring only single-chain support must use browser limits and routing.
let nativeCalls = 0;
context.window.Hyb2Desktop = {enabled: true, capabilities: {cplfoldNative: true}, limits: {cplfoldMaxLength: 2000}, predictCplfold() { nativeCalls++; return Promise.resolve({}); }};
const long = state({pastedSequence: "G".repeat(40), cplfoldSequenceTwo: "C".repeat(40)});
assert.equal(controller.predict(long, api), false); assert.match(api.toasts.at(-1), /80 nt.*75 nt browser baseline/);
assert.match(pages.renderStructure(long), /CPLcofold \+ Pyodide · Browser/);
assert.doesNotMatch(pages.renderStructure(long), /Using the desktop bundled/);
assert.equal(controller.predict(state(), api), true); assert.equal(nativeCalls, 0);
const probe = state(); assert.equal(controller.probeCplfoldCapacity(probe, api), true);
assert.equal(workers.at(-1).message.cofold, true);
const doubleProfile = probe.structure.cplfoldCapacity.profileKey; controller.cancel(probe);
delete context.window.Hyb2Desktop;
const singleProbe = state({cplfoldMode: "single"}); assert.equal(controller.probeCplfoldCapacity(singleProbe, api), true);
assert.equal(workers.at(-1).message.cofold, false);
assert.notEqual(singleProbe.structure.cplfoldCapacity.profileKey, doubleProfile); controller.cancel(singleProbe);
const stale = state({pastedSequence: "G".repeat(40), cplfoldSequenceTwo: "C".repeat(40),
  cplfoldCapacity: {status: "ready", baselineLength: 75, hardCeiling: 500, recommendedLength: 200, profileKey: singleProbe.structure.cplfoldCapacity.profileKey}});
assert.equal(controller.predict(stale, api), false); assert.match(api.toasts.at(-1), /settings changed/i);
// Real reference coordinates map to compact A+B, retaining inter- and intra-strand reads.
const ref = state({source: "reference", rna: "A", start: "2", end: "7", secondRna: "B", secondStart: "3", secondEnd: "8", cplfoldEvidence: "hyb-blocks"});
ref.reference = {mapping: {A: "A", B: "B"}};
const records = [
  {id: "cross", index: 0, rnaOne: "A", rnaOneStart: 3, rnaOneEnd: 4, rnaTwo: "B", rnaTwoStart: 4, rnaTwoEnd: 5},
  {id: "intra-A", index: 1, rnaOne: "A", rnaOneStart: 2, rnaOneEnd: 3, rnaTwo: "A", rnaTwoStart: 6, rnaTwoEnd: 7},
  {id: "intra-B", index: 2, rnaOne: "B", rnaOneStart: 3, rnaOneEnd: 4, rnaTwo: "B", rnaTwoStart: 7, rnaTwoEnd: 8}
];
ref.view = {structureEvidence: {guided: true, rnas: ["A", "B"], records,
  fasta: {mapping: {A: "A", B: "B"}, sequences: [{id: "A", sequence: "AGGGCCCA"}, {id: "B", sequence: "AAGGGCCCAA"}]}}};
const assembly = pages.getStructureSequence(ref);
assert.equal(assembly.error, ""); assert.equal(assembly.sequence, "GGGCCCGGGCCC");
assert.equal(assembly.cutpoint, 6); assert.equal(assembly.assembly.linker, null);
assert.match(pages.renderStructure(ref), /Region 2 start/);
assert.deepEqual(plain(assembly.assembly.evidenceArms.map(a => [a.oneStart, a.oneEnd, a.twoStart, a.twoEnd])), [[2, 3, 8, 9], [1, 2, 5, 6], [7, 8, 11, 12]]);
assert.deepEqual(plain(assembly.sourceSegments.map(s => [s.preparedStart, s.preparedEnd])), [[1, 6], [7, 12]]);
for (const fields of [{start: "7", end: "2"}, {secondStart: "8", secondEnd: "3"}]) {
  const target = {...ref, structure: {...ref.structure, ...fields}};
  const before = workers.length;
  assert.match(pages.getStructureSequence(target).error, /start must not be after the end/);
  assert.equal(controller.predict(target, api), false);
  assert.equal(workers.length, before, "reversed regions are rejected, not swapped");
}
// Preparation sees the original state values, not String(array) or a rounded
// decimal. Cover sequence-only and guided single windows as well as both sides.
{
  const modes = [
    {engine: "cplfold", cplfoldMode: "single", cplfoldEvidence: "none"},
    {engine: "cplfold", cplfoldMode: "single", cplfoldEvidence: "hyb-blocks"},
    {engine: "viennarna", constraintMode: "none"},
    {engine: "cplfold", cplfoldMode: "cofold", cplfoldEvidence: "none"},
    {engine: "viennarna", constraintMode: "hyb-guided", evidenceLayout: "paired"}
  ];
  // Blank fields can omit a single-window bound; explicit null stays invalid.
  const invalid = coordinateCases.invalid
    .filter(value => typeof value !== "string" || value.trim() !== "")
    .concat(coordinateCases.nonFinite.map(Number));
  // Keep A/B at 8/10 nt; offset controls belong to the 24 nt direct-assembly fixture.
  const singleWindows = coordinateCases.validWindows.filter(({expected}) => expected[0] === 1 && expected[1] === 10);
  for (const mode of modes) {
    const paired = mode.cplfoldMode === "cofold" || mode.evidenceLayout === "paired";
    const guided = mode.cplfoldEvidence === "hyb-blocks" || mode.constraintMode === "hyb-guided";
    const base = {...ref, structure: {...ref.structure, cplfoldMode: "single", ...mode,
      rna: paired ? "A" : "B", start: 1, end: paired ? 8 : 10, secondStart: 1, secondEnd: 10},
      view: {structureEvidence: {...ref.view.structureEvidence, guided}}};
    assert.equal(pages.getStructureSequence(base).error, "");
    for (const key of paired ? ["start", "end", "secondStart", "secondEnd"] : ["start", "end"]) {
      for (const value of invalid) {
        const target = {...base, structure: {...base.structure, [key]: value}};
        const result = pages.getStructureSequence(target);
        const label = JSON.stringify({mode, key, value});
        assert.match(result.error, /whole numbers|Enter/, label);
        assert.equal(result.sequence, "", label);
        const before = workers.length;
        assert.equal(controller.predict(target, api), false, label);
        assert.equal(workers.length, before, label);
      }
    }
    if (!paired) {
      for (const {input: [start, end], expected} of singleWindows) {
        const result = pages.getStructureSequence({...base, structure: {...base.structure, start, end}});
        assert.deepEqual([result.sequence, result.sourceStart, result.sourceEnd, result.error], ["AAGGGCCCAA", expected[0], expected[1], ""]);
      }
    }
  }
  const asymmetric = pages.getStructureSequence({...ref, structure: {...ref.structure, secondEnd: " 9 "}});
  assert.deepEqual([asymmetric.sequence, asymmetric.cutpoint, asymmetric.error], ["GGGCCCGGGCCCA", 6, ""]);
  assert.deepEqual(plain(asymmetric.sourceSegments.map(segment => [segment.referenceStart, segment.referenceEnd, segment.preparedStart, segment.preparedEnd])),
    [[2, 7, 1, 6], [3, 9, 7, 13]]);
}
{
  const rules = context.Hyb2FoldText, original = rules.minSequenceNt;
  try {
    rules.minSequenceNt = 7;
    for (const comrades of [false, true]) {
      const target = {...ref, structure: {...ref.structure, ...(comrades
        ? {engine: "viennarna", constraintMode: "hyb-guided", evidenceLayout: "paired"} : {})}};
      const before = workers.length;
      assert.match(pages.getStructureSequence(target).error, /at least 7 nt/);
      assert.equal(controller.predict(target, api), false);
      assert.equal(workers.length, before);
    }
  } finally { rules.minSequenceNt = original; }
}
// A scope change preserves the historical fold, but Setup's local inputs
// belong to the current evidence. Equal sequence, nick and kernel are not
// evidence provenance: red supports (1,8), blue supports (2,7).
for (const mode of ["single", "cofold"]) {
  const sequence = mode === "cofold" ? "GGGGCCCC" : "GGGGCCCCAA";
  const red = {engine: mode === "cofold" ? "CPLcofold" : "CPLfold", sequence, cutpoint: mode === "cofold" ? 4 : null,
    evidenceDatasetIds: ["red"], evidenceDatasets: ["Red"],
    rawHybArms: [{recordId: "same-row", oneStart: 1, oneEnd: 1, twoStart: 8, twoEnd: 8}],
    evidence: {source: "hyb-block-intervals", supportKernel: "block", bonusEntries: [{one: 1, two: 8, value: Math.LN2}]}};
  const target = state({cplfoldMode: mode, source: "reference", rna: "A", start: "1", end: mode === "cofold" ? "4" : String(sequence.length),
    secondRna: "B", secondStart: "1", secondEnd: "4", cplfoldEvidence: "hyb-blocks", cplfoldKernel: "block",
    evidenceDatasets: "scope", status: "complete", result: red});
  target.reference = {mapping: {A: "A", B: "B"}};
  target.view = {structureEvidence: {guided: true, rnas: ["A", "B"],
    records: [{id: "same-row", index: 0, rnaOne: "A", rnaOneStart: 2, rnaOneEnd: 2,
      rnaTwo: mode === "cofold" ? "B" : "A", rnaTwoStart: mode === "cofold" ? 3 : 7, rnaTwoEnd: mode === "cofold" ? 3 : 7}],
    fasta: {mapping: {A: "A", B: "B"}, sequences: [{id: "A", sequence: mode === "cofold" ? "GGGG" : sequence}, {id: "B", sequence: "CCCC"}]}}};
  const blue = {sequence, cutpoint: red.cutpoint, evidence: {...red.evidence, bonusEntries: [{one: 2, two: 7, value: Math.LN2}]}};
  // New dataset, then new rows under the historical dataset's unchanged ID and label.
  for (const [id, label] of [["blue", "Blue"], ["red", "Red"]]) {
    target.selection = {datasetIds: [id]};
    Object.assign(target.view.structureEvidence, {datasetIds: [id], datasets: [label]});
    const downloads = api.downloads.length, requests = workerRequests;
    ui.handleAction("download-local-cplfold-input", target, api);
    assert.equal(api.downloads.length, downloads, "neither file is downloaded before the current matrix succeeds");
    assert.equal(workerRequests, requests + 1);
    const worker = target.structureWorker;
    assert.deepEqual([worker.message.type, worker.message.sequence, worker.message.cutpoint ?? null, worker.message.supportKernel],
      ["cplfold-bonus-matrix", sequence, red.cutpoint, "block"]);
    assert.deepEqual(plain(worker.message.evidenceArms.map(a => [a.recordId, a.oneStart, a.oneEnd, a.twoStart, a.twoEnd])), [["same-row", 2, 2, 7, 7]]);
    ui.handleAction("download-local-cplfold-input", target, api);
    assert.match(api.toasts.at(-1), /finish or cancel/);
    assert.deepEqual([api.downloads.length, workerRequests], [downloads, requests + 1], "a second click leaves the running export alone");
    worker.onmessage({data: {type: "bonus-matrix-complete", result: blue}});
    const localFiles = api.downloads.slice(downloads);
    assert.deepEqual(localFiles.map(file => file.name), ["cplfold-input.fasta", "cplfold-bonus-matrix.tsv"]);
    assert.match(localFiles[1].contents, /\n2\t7\t0\.69314718/, mode + ": current blue evidence, not the red fold's matrix");
    assert.doesNotMatch(localFiles[1].contents, /\n1\t8\t/);
    assert.equal(localFiles[0].contents, mode === "cofold" ? ">HYB2_Web_RNA_A\nGGGG\n>HYB2_Web_RNA_B\nCCCC\n" : ">HYB2_Web_prepared_sequence\n" + sequence + "\n");
    assert.equal(target.structure.result, red, "matrix-only generation leaves the historical fold intact");
  }
  ui.handleAction("download-cplfold-evidence", target, api);
  assert.match(api.downloads.at(-1).contents, /\n1\t8\t0\.69314718/, "the result's explicit matrix export remains historical");
  assert.doesNotMatch(api.downloads.at(-1).contents, /\n2\t7\t/);
  for (const outcome of ["error", "crash", "cancel"]) {
    const count = api.downloads.length;
    ui.handleAction("download-local-cplfold-input", target, api);
    const worker = target.structureWorker;
    if (outcome === "error") worker.onmessage({data: {type: "error", message: "Synthetic current-matrix failure"}});
    else if (outcome === "crash") worker.onerror();
    else ui.handleAction("cancel-structure", target, api);
    worker.onmessage({data: {type: "bonus-matrix-complete", result: blue}});
    assert.equal(worker.terminated, true);
    assert.equal(api.downloads.length, count, outcome + ": no partial FASTA and no fallback to the saved matrix");
    assert.equal(target.structure.result, red);
  }
}
// Validate command options before requesting any matrix, even with a saved prediction.
const cachedMatrix = {sequence: assembly.sequence, cutpoint: assembly.cutpoint,
  evidence: {source: "hyb-block-intervals", bonusEntries: [{one: 2, two: 9, value: 1}]}};
for (const result of [null, cachedMatrix]) {
  const target = overrides => ({...ref, structure: {...ref.structure, status: result ? "complete" : "idle", result, ...overrides}});
  assertRejectsModel(target({cplfoldEnergyModel: "CC09"}));
  for (const [key, values, issue] of [
    ["cplfoldBeam", [-1, 2.5, "2e1", true], /--beam.*integer/],
    ["cplfoldMaxPhase1", [0, "1.5", "1 --quiet"], /--max-phase1.*integer/],
    ["cplfoldEnergyDelta", [-1, "", "0x10", NaN, "1e309"], /--delta/],
    ["cplfoldAlpha", [-0.5, "NaN"], /--alpha/], ["cplfoldBeta", [-1, Infinity], /--beta/],
    ["cplfoldKernel", ["rnacofold"], /supported CPLfold read-support kernel/],
    ["cplfoldAllowPseudoknot", ["false"], /must be a boolean/]
  ]) {
    for (const value of values) assertRejectsLocalOptions(target({[key]: value}), issue);
  }

  const valid = target({cplfoldBeam: 0, cplfoldEnergyDelta: 0, cplfoldAlpha: 0, cplfoldBeta: 0});
  const downloads = api.downloads.length, requests = workerRequests;
  ui.handleAction("download-local-cplfold-input", valid, api);
  assert.equal(workerRequests, requests + 1);
  assert.equal(api.downloads.length, downloads, "wait for both guided files");
  const worker = valid.structureWorker;
  assert.equal(worker.message.type, "cplfold-bonus-matrix");
  worker.onmessage({data: {type: "bonus-matrix-complete", result: cachedMatrix}});
  assert.deepEqual(api.downloads.slice(downloads).map(file => file.name), ["cplfold-input.fasta", "cplfold-bonus-matrix.tsv"]);
  assert.match(api.downloads.at(-1).contents, /\n2\t9\t1\./);
  assert.match(api.toasts.at(-1), /bin\/cplcofold .*--beam 0 --delta 0 .*--beta 0 --alpha 0/);
}
// The provider can leave its previous rows in the view while loading their replacement.
for (const view of [{structureEvidence: null}, {...ref.view, loading: {structureEvidence: true}}]) {
  const target = {...ref, view, structure: {...ref.structure, status: "complete", result: cachedMatrix}};
  const before = [workerRequests, api.downloads.length];
  ui.handleAction("download-local-cplfold-input", target, api);
  assert.deepEqual([workerRequests, api.downloads.length], before);
  assert.match(api.toasts.at(-1), /load/i);
  assert.equal(target.structure.result, cachedMatrix);
}
// A prediction is not a substitute for a missing input-preparation module, even for FASTA only.
delete context.window.Hyb2Pages;
try {
  for (const evidence of ["none", "hyb-blocks"]) {
    const target = state({result: cachedMatrix, cplfoldEvidence: evidence});
    const before = [workerRequests, api.downloads.length];
    ui.handleAction("download-local-cplfold-input", target, api);
    assert.deepEqual([workerRequests, api.downloads.length], before);
    assert.match(api.toasts.at(-1), /valid sequence/);
  }
} finally { context.window.Hyb2Pages = pages; }
// A saved guided form without any evidence must not silently export just the sequence.
const missingEvidence = state({cplfoldMode: "single", cplfoldEvidence: "hyb-blocks", pastedSequence: "GGGCCC"});
const beforeMissing = [workers.length, workerRequests, api.downloads.length];
ui.handleAction("download-local-cplfold-input", missingEvidence, api);
assert.deepEqual([workers.length, workerRequests, api.downloads.length], beforeMissing);
assert.match(api.toasts.at(-1), /No eligible HYB rows/);

// Correcting the selected kernel restores the About command; the matrix must use that kernel.
const editable = {...ref, structure: {...ref.structure, cplfoldKernel: "rnacofold"}};
assert.doesNotMatch(pages.renderStructure(editable), /bin\/cplcofold --sequence-file/);
change(editable, "cplfoldKernel", "block");
assert.match(pages.renderStructure(editable), /bin\/cplcofold .*--bonus-matrix-file/);
editable.structure.result = cachedMatrix; // old Gaussian matrix cannot stand in for block support
const pendingDownloads = api.downloads.length;
ui.handleAction("download-local-cplfold-input", editable, api);
assert.equal(editable.structureWorker.message.supportKernel, "block");
editable.structureWorker.onmessage({data: {type: "error", message: "Synthetic matrix failure"}});
assert.equal(api.downloads.length, pendingDownloads);

const paste = state({cplfoldEnergyDelta: "-1", cplfoldKernel: "rnacofold"});
assertRejectsLocalOptions(paste, /--delta.*nonnegative/);
assert.match(pages.renderStructure(paste), /--delta must be nonnegative/);
assert.doesNotMatch(pages.renderStructure(paste), /bin\/cplcofold --sequence-file/);
change(paste, "cplfoldEnergyDelta", "0");
assert.match(pages.renderStructure(paste), /bin\/cplcofold .*--delta 0/);
ui.handleAction("download-local-cplfold-input", paste, api);
assert.match(api.toasts.at(-1), /bin\/cplcofold .*--delta 0/); // no kernel needed in sequence-only mode

// The legacy single-strand CLI accepts signed, finite alpha/beta; cofold does not.
const signed = {...ref.structure, cplfoldMode: "single", cplfoldAlpha: "-0.5", cplfoldBeta: "-2e-8"};
assert.match(ui.localCplfoldCommand(signed, true), /^bin\/cplfold .*--beta=-2e-8 --alpha -0.5\b/);
assert.throws(() => ui.localCplfoldCommand({...signed, cplfoldBeta: -Infinity}, true), /--beta.*finite/);
assert.equal(controller.predict(ref, api), true); assert.equal(workers.at(-1).message.cutpoint, 6);
assert.equal(workers.at(-1).message.evidenceArms.length, 3); controller.cancel(ref);
const legacy = context.window.Hyb2Comrades.prepareReferenceAssembly(records, ref.view.structureEvidence.fasta, {
  regions: [{rna: "A", start: 2, end: 7}, {rna: "B", start: 3, end: 8}]
});
assert.equal(legacy.linker.length, 100); assert.equal(legacy.evidenceArms.length, 1);
assert.equal(controller.downloadCplfoldBonusMatrix(ref, api, assembly, () => {}), true);
assert.equal(workers.at(-1).message.type, "cplfold-bonus-matrix"); assert.equal(workers.at(-1).message.cutpoint, 6); controller.cancel(ref);
const pairs = [{left: 1, right: 6, leftBase: "G", rightBase: "C", type: "G–C", layer: "primary"},
  {left: 2, right: 5, leftBase: "G", rightBase: "C", type: "G–C", layer: "primary"},
  {left: 3, right: 4, leftBase: "G", rightBase: "C", type: "G–C", layer: "primary"}];
const candidate = {dotBracket: "((()))", dotBracketWithBreak: "(((&)))", pairs, unpaired: 0, energy: -2.5, effectiveEnergy: -2.5,
  phase1Energy: -2.5, phase1Structure: "((()))", phase2Structure: null, score: -2.5, type: "phase1", topology: "nested", crossingPairs: 0,
  energyMetadata: {energy_kind: "thermodynamic", energy_model: "Turner-LinearCoFold"}, intermolecularPairs: 3};
const result = {...candidate, sequence: "GGGCCC", displaySequence: "GGG&CCC", engine: "CPLcofold", cutpoint: 3, strandLengths: [3, 3],
  selectedCandidate: 0, candidates: [candidate, {...candidate, dotBracket: "......", dotBracketWithBreak: "...&...", pairs: [], unpaired: 6,
    energyMetadata: {energy_kind: "heuristic", energy_model: "experimental-test-model"}, intermolecularPairs: 0}],
  parameters: {allowPseudoknot: false, energyModel: "DP09"}, allowPseudoknot: false, evidence: {source: "none", bonusEntries: []}};
current = state(); controller.predict(current, api); workers.at(-1).onmessage({data: {type: "complete", result}});
assert.equal(current.structure.result.engine, "CPLcofold"); assert.match(current.structure.message, /CPLcofold completed/);
assert.equal(current.structure.result.cutpoint, 3); assert.equal(current.structure.result.sequence.length, 6);
assert.equal(ui.handleAction("select-cplfold-candidate", current, api, {dataset: {candidateIndex: "1"}}), true);
assert.equal(current.structure.result.dotBracketWithBreak, "...&..."); assert.equal(current.structure.result.energyMetadata.energy_kind, "heuristic");
assert.match(pages.renderStructureResult(current.structure.result, null, "folded", true), /Experimental pseudoknot energy/);
// A fold that no HYB row guided is labelled so under the result title; a HYB-guided one is not.
assert.match(pages.renderStructureResult(current.structure.result, null, "folded", true), /class="card-note structure-result-evidence">Sequence only: no HYB evidence guided this fold\./);
assert.doesNotMatch(pages.renderStructureResult({...current.structure.result, evidence: {source: "hyb-block-intervals", bonusEntries: []}}, null, "folded", true), /Sequence only: no HYB evidence/);
assert.doesNotMatch(pages.renderStructureResult(current.structure.result, null, "folded", true), /data-view-mode="folded"|data-view-mode="circular"|toggle-structure-editor/);
ui.selectCplfoldCandidate(current.structure.result, 0);
const report = ui.structureReport(current, current.structure.result);
assert.equal(report.source.cutpoint, 3); assert.equal(report.source.displaySequence, "GGG&CCC");
assert.equal(report.prediction.dotBracketWithBreak, "(((&)))"); assert.equal(report.prediction.energyMetadata.energy_kind, "thermodynamic");
assert.match(ui.cplfoldCandidatesTsv(current.structure.result), /dot_bracket\tcutpoint/);
assert.match(ui.cplfoldCandidatesTsv(current.structure.result), /\(\(\(&\)\)\)\t3/);
assert.match(ui.basePairTsv(current.structure.result), /cutpoint\tleft_strand\tleft_strand_position/);
assert.match(ui.cplfoldEvidenceTsv(current.structure.result), /# cutpoint=3/);
const historicalResult = {...current.structure.result, evidence: {source: "hyb-block-intervals", supportKernel: "rnacofold",
  transform: "RNAcofold intermolecular pairs; log1p", bonusEntries: [{one: 1, two: 6, value: 1}]}};
assert.match(pages.renderStructureResult(historicalResult, null, "folded", true), / · RNAcofold pairs<\/small>/);
assert.match(ui.cplfoldEvidenceTsv(historicalResult), /# transform=RNAcofold intermolecular pairs; log1p\n# support_kernel=rnacofold\n/);
ui.handleAction("copy-structure-dot-bracket", current, api); assert.equal(api.copies.at(-1), "(((&)))");
ui.handleAction("download-structure-dot-bracket", current, api); assert.match(api.downloads.at(-1).contents, /GGG&CCC\n\(\(\(&\)\)\)/);
ui.handleAction("download-structure-ct", current, api);
const ct = api.downloads.at(-1).contents.trim().split("\n").slice(1).map(line => line.split("\t"));
assert.deepEqual(ct[2].slice(2, 4), ["2", "0"]); assert.deepEqual(ct[3].slice(2, 4), ["0", "5"]);
ui.handleAction("download-local-cplfold-input", current, api);
assert.equal(api.downloads.at(-1).contents, ">HYB2_Web_RNA_A\nGGG\n>HYB2_Web_RNA_B\nCCC\n");
assert.match(ui.localCplfoldCommand(current.structure, false), /^bin\/cplcofold .*--mode standard/);
assert.match(ui.localCplfoldCommand({...current.structure, cplfoldAllowPseudoknot: true}, false), /--mode pseudoknot/);
assert.match(pages.renderStructureExportMenu(current.structure.result), /download-cplfold-candidates/);
const diagram = ui.buildStructureDiagram(current.structure.result, null, "folded");
const backbones = diagram.children.filter(node => node.attributes["data-strand"]);
assert.equal(backbones.length, 2);
assert.ok(Number(backbones[0].attributes.x2) < Number(backbones[1].attributes.x1), "backbone must have a visible gap at the nick");
assert.equal(diagram.children.find(node => node.attributes["data-chain-break"]).attributes["data-chain-break"], "3");
// A stale single-chain worker response must not become a seemingly valid cofold result.
const wrongRuntime = state();
controller.predict(wrongRuntime, api);
workers.at(-1).onmessage({data: {type: "complete", result: {...result, engine: "CPLfold", cutpoint: undefined}}});
assert.equal(wrongRuntime.structure.status, "error");
assert.equal(wrongRuntime.structure.result, null);
assert.match(api.toasts.at(-1), /did not preserve the two RNA strands/);
const overCeiling = state({pastedSequence: "G".repeat(251), cplfoldSequenceTwo: "C".repeat(250)});
assert.equal(controller.predict(overCeiling, api), false);
assert.match(api.toasts.at(-1), /hard safety ceiling of 500/);
// The local CLI can use larger inputs and search budgets than the browser.
const localBeam = context.Hyb2EngineParameters.cplcofold.ranges.beamSize[1] + 1;
Object.assign(overCeiling.structure, {cplfoldBeam: String(localBeam), cplfoldEnergyDelta: "51", cplfoldMaxPhase1: "21", cplfoldBeta: "1.5"});
ui.handleAction("download-local-cplfold-input", overCeiling, api);
assert.equal(api.downloads.at(-1).contents, ">HYB2_Web_RNA_A\n" + "G".repeat(251) + "\n>HYB2_Web_RNA_B\n" + "C".repeat(250) + "\n");
assert.ok(api.toasts.at(-1).includes("--beam " + localBeam + " --delta 51 --max-phase1 21 "));
assert.match(api.toasts.at(-1), /--beta 1.5\b/);
const sequenceReference = state({...ref.structure, cplfoldEvidence: "none", status: "idle"});
sequenceReference.reference = ref.reference;
sequenceReference.view = {structureEvidence: {...ref.view.structureEvidence, guided: false, records: []}};
assert.equal(pages.getStructureSequence(sequenceReference).sequence, "GGGCCCGGGCCC");
assert.equal(pages.getStructureSequence(sequenceReference).error, "");
// The fallback command generator works before the structure UI has loaded too.
const savedUi = context.window.Hyb2StructureUI;
delete context.window.Hyb2StructureUI;
assert.match(pages.cplfoldLocalCommand(current.structure, false), /^bin\/cplcofold .*--mode standard/);
context.window.Hyb2StructureUI = savedUi;


// Workflow controls must never change the selected CPLfold evidence/ranking options.
function change(target, key, value, handler = "handleChange") {
  const element = {dataset: {feature: "structure-control", key}, value, type: "select-one"};
  assert.equal(controls[handler](target, element, api), true);
}
function rankingOptions(target) {
  const s = target.structure;
  return [s.cplfoldAlpha, s.cplfoldBeta, s.cplfoldAllowPseudoknot, s.cplfoldEvidence];
}
for (const handler of ["handleChange", "handleInput"]) {
  for (const evidence of ["none", "hyb-blocks"]) {
    for (const workflow of ["long-intramolecular", "intermolecular", "homodimer", "short-intramolecular"]) {
      const target = state({source: "reference", cplfoldMode: "single", rna: "A", start: "3", end: "8",
        secondRna: "B", secondStart: "13", secondEnd: "18", cplfoldAlpha: "0.75", cplfoldBeta: "0.3",
        cplfoldAllowPseudoknot: true, cplfoldEvidence: evidence});
      target.summary.rnaNames = ["A", "B"];
      const options = rankingOptions(target);
      change(target, "workflow", workflow, handler);
      assert.equal(target.structure.engine, "cplfold", `${workflow} retains CPLfold`);
      assert.equal(target.structure.cplfoldMode, workflow === "short-intramolecular" ? "single" : "cofold");
      assert.equal(target.structure.evidenceLayout, workflow === "short-intramolecular" ? "single" : "paired");
      assert.equal(target.structure.source, "reference");
      assert.deepEqual(rankingOptions(target), options);
      assert.deepEqual([target.structure.start, target.structure.end], ["3", "8"]);
      // Homodimer explicitly means two copies of region 1; other presets retain both coordinate selections.
      assert.deepEqual([target.structure.secondStart, target.structure.secondEnd], workflow === "homodimer" ? ["3", "8"] : ["13", "18"]);
      assert.equal(target.structure.secondRna, ["long-intramolecular", "homodimer"].includes(workflow) ? "A" : "B");
      assert.equal(target.structure.homodimerOnly, workflow === "homodimer");
      change(target, "workflow", "short-intramolecular", handler);
      assert.equal(target.structure.cplfoldMode, "single"); assert.deepEqual(rankingOptions(target), options);
      assert.equal(target.structure.homodimerOnly, false);
    }
  }
}
for (const workflow of ["long-intramolecular", "intermolecular", "homodimer", "short-intramolecular"]) {
  const target = state({engine: "viennarna", source: "reference", rna: "A", start: "3", end: "8", secondRna: "B", secondStart: "13", secondEnd: "18"});
  target.summary.rnaNames = ["A", "B"];
  change(target, "workflow", workflow);
  assert.equal(target.structure.engine, "viennarna"); assert.equal(target.structure.constraintMode, "hyb-guided");
  assert.equal(target.structure.evidenceLayout, workflow === "short-intramolecular" ? "single" : "paired");
  assert.equal(target.structure.homodimerOnly, workflow === "homodimer");
}
const fromPairedVienna = state({engine: "viennarna", source: "reference", evidenceLayout: "paired", cplfoldMode: "single",
  rna: "A", start: "3", end: "8", secondRna: "A", secondStart: "13", secondEnd: "18", cplfoldEnergyModel: "CC09"});
change(fromPairedVienna, "engine", "cplfold");
assert.equal(fromPairedVienna.structure.cplfoldMode, "cofold");
assert.equal(fromPairedVienna.structure.cplfoldEnergyModel, "DP09");
assert.deepEqual([fromPairedVienna.structure.rna, fromPairedVienna.structure.start, fromPairedVienna.structure.end,
  fromPairedVienna.structure.secondRna, fromPairedVienna.structure.secondStart, fromPairedVienna.structure.secondEnd], ["A", "3", "8", "A", "13", "18"]);

const localSequence = "AAGGGCCCAAAACCCGGGUU";
const localRecords = [
  {id: "local-cross", index: 0, rnaOne: "A", rnaOneStart: 4, rnaOneEnd: 5, rnaTwo: "A", rnaTwoStart: 14, rnaTwoEnd: 15},
  {id: "local-intra-A", index: 1, rnaOne: "A", rnaOneStart: 3, rnaOneEnd: 4, rnaTwo: "A", rnaTwoStart: 7, rnaTwoEnd: 8},
  {id: "local-intra-B", index: 2, rnaOne: "A", rnaOneStart: 13, rnaOneEnd: 14, rnaTwo: "A", rnaTwoStart: 17, rnaTwoEnd: 18}
];
function localState(overrides = {}) {
  const target = state({source: "reference", workflow: "custom", rna: "A", start: "3", end: overrides.cplfoldMode === "single" ? "12" : "8",
    secondRna: "A", secondStart: "13", secondEnd: "18", cplfoldEvidence: "hyb-blocks", ...overrides});
  target.summary.rnaNames = ["A", "B"];
  target.reference = {mapping: {A: "A", B: "B"}};
  target.view = {structureEvidence: {guided: target.structure.cplfoldEvidence === "hyb-blocks", rnas: ["A", "B"], records: localRecords,
    fasta: {mapping: {A: "A", B: "B"}, sequences: [{id: "A", sequence: localSequence}, {id: "B", sequence: localSequence}]}}};
  return target;
}

const catalog = context.Hyb2SupportKernels;
const kernels = Array.from(catalog.options, option => option.value);
for (const known of ["gaussian", "block", "block-normalized", "duplex", "comrades"]) assert.ok(kernels.includes(known));
for (const layout of ["single", "cofold"]) {
  const target = localState({cplfoldMode: layout});
  const select = pages.renderStructure(target).match(/<select[^>]+data-key="cplfoldKernel"[^>]*>(.*?)<\/select>/)[1];
  assert.deepEqual(Array.from(select.matchAll(/<option value="([^"]+)"/g), match => match[1]), kernels);
  assert.match(select, /value="gaussian" selected/);
  for (const kernel of kernels) {
    change(target, "cplfoldKernel", kernel);
    assert.equal(controller.predict(target, api), true);
    assert.equal(target.structureWorker.message.type, "cplfold");
    assert.equal(target.structureWorker.message.supportKernel, kernel);
    controller.cancel(target);
    assert.equal(controller.downloadCplfoldBonusMatrix(target, api), true);
    assert.equal(target.structureWorker.message.type, "cplfold-bonus-matrix");
    assert.equal(target.structureWorker.message.supportKernel, kernel);
    controller.cancel(target);
    assert.equal(controller.probeCplfoldCapacity(target, api), true);
    assert.equal(target.structureWorker.message.supportKernel, kernel);
    controller.cancel(target);
  }

  target.structure.cplfoldKernel = "rnacofold";
  const count = workers.length;
  assert.match(pages.renderStructure(target), /value="" selected disabled>Choose a supported kernel/);
  assert.equal(controller.predict(target, api), false);
  assert.match(api.toasts.at(-1), /support.*kernel/i);
  assert.equal(controller.downloadCplfoldBonusMatrix(target, api), false);
  assert.match(api.toasts.at(-1), /support.*kernel/i);
  assert.equal(target.structure.cplfoldKernel, "rnacofold");
  assert.equal(workers.length, count, "a stale selection must not start a fold or matrix export");
}

// A future catalog option is selectable and submitted without editing a JS list.
const extra = {value: "test-support", label: "Catalog test support", pairModel: "RNAcofold"};
context.Hyb2SupportKernels = {defaultKernel: extra.value, options: [...catalog.options, extra]};
const extension = localState();
assert.equal(context.Hyb2CplfoldSettings.readKernel(), extra.value);
assert.match(pages.renderStructure(extension), /value="test-support" selected>Catalog test support/);
assert.match(pages.renderStructure(extension), /Read support method: Catalog test support/);
assert.equal(controller.predict(extension, api), true);
assert.equal(extension.structureWorker.message.supportKernel, extra.value);
assert.equal(context.window.Hyb2Comrades.pairCountSource({evidence: {supportKernel: extra.value}}), "RNAcofold");
const newEvidence = {source: "hyb-block-intervals", supportKernel: extra.value};
assert.equal(pages.cplfoldBonusKind(newEvidence), "hyb_bonus", "a pair model alone does not identify the transform");
assert.equal(pages.cplfoldBonusKind({...newEvidence, saturatingQ: 2}), "saturating_hyb_bonus");
controller.cancel(extension);
context.Hyb2SupportKernels = catalog;

const sameRna = localState();
const localInfo = pages.getStructureSequence(sameRna);
assert.equal(localInfo.error, ""); assert.equal(localInfo.sequence, "GGGCCCCCCGGG");
assert.equal(localInfo.displaySequence, "GGGCCC&CCCGGG"); assert.equal(localInfo.cutpoint, 6);
assert.equal(localInfo.assembly.linker, null);
assert.deepEqual(plain(localInfo.sourceSegments.map(segment => [segment.rna, segment.referenceStart, segment.referenceEnd, segment.preparedStart, segment.preparedEnd])),
  [["A", 3, 8, 1, 6], ["A", 13, 18, 7, 12]]);
const sourcePosition = position => {
  const segment = localInfo.sourceSegments.find(s => s.preparedStart <= position && position <= s.preparedEnd);
  return segment.referenceStart + position - segment.preparedStart;
};
assert.deepEqual([1, 6, 7, 12].map(sourcePosition), [3, 8, 13, 18]);
assert.deepEqual(plain(localInfo.assembly.evidenceArms.map(a => [a.oneStart, a.oneEnd, a.twoStart, a.twoEnd])), [[2, 3, 8, 9], [1, 2, 5, 6], [7, 8, 11, 12]]);
assert.match(pages.renderStructure(sameRna), /data-key="workflow"/);
assert.match(pages.renderStructure(sameRna), /Region 1 start/);
assert.match(pages.renderStructure(sameRna), /Region 2 start/);
assert.match(pages.renderStructure(sameRna), /no intervening reference sequence/);
assert.equal(controller.predict(sameRna, api), true);
assert.deepEqual(plain([workers.at(-1).message.sequence, workers.at(-1).message.cutpoint]), ["GGGCCCCCCGGG", 6]);
controller.cancel(sameRna);
assert.match(pages.getStructureSequence(localState({workflow: "intermolecular"})).error, /different RNAs/);
assert.equal(pages.getStructureSequence(localState({workflow: "custom"})).error, "");
assert.match(pages.getStructureSequence(localState({workflow: "long-intramolecular", secondRna: "B"})).error, /same RNA/);
assert.match(pages.getStructureSequence(localState({workflow: "homodimer"})).error, /two copies of the same region/);

const homodimer = localState();
homodimer.view.structureEvidence.records = [
  {...localRecords[1], id: "homodimer", index: 10, isHomodimer: true},
  {...localRecords[1], id: "ordinary", index: 11, isHomodimer: false}
];
change(homodimer, "workflow", "homodimer");
assert.equal(pages.getStructureSequence(homodimer).assembly.evidenceArms.length, 1);
assert.equal(pages.getStructureSequence(homodimer).assembly.skipped.homodimerFilter, 1);
assert.match(pages.renderStructure(homodimer), /Homodimer filter active/);
change(homodimer, "workflow", "custom");
assert.equal(homodimer.structure.homodimerOnly, false);
assert.equal(pages.getStructureSequence(homodimer).assembly.evidenceArms.length, 2);
assert.doesNotMatch(pages.renderStructure(homodimer), /Homodimer filter active/);
// Editing a homodimer copy explicitly leaves the identical-copies preset and filter.
change(homodimer, "workflow", "homodimer");
change(homodimer, "secondStart", "4");
assert.equal(homodimer.structure.workflow, "custom"); assert.equal(homodimer.structure.homodimerOnly, false);

function regionHandoff(partner, partnerStart, partnerEnd) {
  const target = localState({cplfoldMode: "cofold", secondRna: "STALE", secondStart: "1", secondEnd: "2",
    cplfoldEvidence: "none", cplfoldAlpha: "0.75", cplfoldBeta: "0.3", cplfoldAllowPseudoknot: true});
  target.canRequestFold = false;
  const end = partnerStart === "" && partnerEnd === "" ? 12 : 8;
  target.region = {rna: "A", start: "3", end: String(end), partner, partnerStart, partnerEnd};
  target.view.regionReference = {id: "A", start: 3, end, sequence: localSequence.slice(2, end), params: {rna: "A", start: "3", end: String(end)}};
  return target;
}
for (const partner of ["A", "B"]) {
  const target = regionHandoff(partner, "13", "18"), options = rankingOptions(target), navigations = api.navigations.length;
  assert.deepEqual(plain(pages.getRegionPartner(target.region)), {rna: partner, start: 13, end: 18});
  controls.handleAction(target, {dataset: {featureAction: "region-send-structure"}}, api);
  assert.equal(api.navigations.length, navigations + 1); assert.equal(api.navigations.at(-1), "structure");
  assert.equal(target.structure.engine, "cplfold"); assert.equal(target.structure.cplfoldMode, "cofold");
  assert.equal(target.structure.evidenceLayout, "paired"); assert.equal(target.structure.workflow, "custom");
  assert.deepEqual([target.structure.rna, target.structure.start, target.structure.end, target.structure.secondRna, target.structure.secondStart, target.structure.secondEnd], ["A", 3, 8, partner, 13, 18]);
  assert.deepEqual(rankingOptions(target), options);
  assert.equal(pages.getStructureSequence(target).sequence.length, 12);
  assert.equal(pages.getStructureSequence(target).cutpoint, 6);
}
for (const partner of ["", "A"]) {
  const target = regionHandoff(partner, "", ""), navigations = api.navigations.length;
  controls.handleAction(target, {dataset: {featureAction: "region-send-structure"}}, api);
  assert.equal(api.navigations.length, navigations + 1);
  assert.equal(target.structure.cplfoldMode, "single"); assert.equal(target.structure.evidenceLayout, "single");
  assert.deepEqual([target.structure.secondRna, target.structure.secondStart, target.structure.secondEnd], ["", "", ""]);
  assert.equal(pages.getStructureSequence(target).sequence.length, 10);
}
for (const [partner, start, end] of [["A", "13", ""], ["A", "", "18"], ["A", "0", "18"], ["A", 0, 0], ["A", "18", "13"], ["A", "13.5", "18"], ["", "13", "18"], ["A", "bad", "18"]]) {
  const target = regionHandoff(partner, start, end), original = plain(target.structure), navigations = api.navigations.length;
  controls.handleAction(target, {dataset: {featureAction: "region-send-structure"}}, api);
  assert.equal(api.navigations.length, navigations, `${partner}:${start}-${end} must not navigate`);
  assert.deepEqual(plain(target.structure), original);
  assert.match(api.toasts.at(-1), /valid 1-based start\/end coordinates/);
}

// Empty Region 2 selects a distinct reference for the inter-RNA preset.
const emptySecondRna = localState({secondRna: ""});
change(emptySecondRna, "workflow", "intermolecular");
assert.equal(emptySecondRna.structure.secondRna, "B");
assert.equal(pages.getStructureSequence(emptySecondRna).error, "");
// Changing a ranking parameter must not silently repair an invalid energy model.
const invalidModel = state({cplfoldEnergyModel: "CC09"});
change(invalidModel, "cplfoldAlpha", "0.75");
assert.equal(invalidModel.structure.cplfoldEnergyModel, "CC09");
assert.equal(controller.predict(invalidModel, api), false);
assert.ok(api.toasts.at(-1).includes(context.Hyb2EngineParameters.cplcofold.models.map(model => model.value).join(", ")));

for (const secondRna of ["A", "B"]) {
  const target = localState({secondRna, cplfoldEvidence: "none"});
  const copyCount = api.copies.length, downloadCount = api.downloads.length;
  controls.handleAction(target, {dataset: {featureAction: "copy-structure-sequence"}}, api);
  assert.equal(api.copies.length, copyCount + 1);
  assert.equal(api.copies.at(-1), "GGGCCC&CCCGGG");
  controls.handleAction(target, {dataset: {featureAction: "structure-download-fasta"}}, api);
  assert.equal(api.downloads.length, downloadCount + 1);
  assert.deepEqual(api.downloads.at(-1), {name: "structure-input.fasta", contents: ">HYB2_Web_RNA_A\nGGGCCC\n>HYB2_Web_RNA_B\nCCCGGG\n"});
}
const genericSingle = state({cplfoldMode: "single", pastedSequence: "GGGCCC"});
controls.handleAction(genericSingle, {dataset: {featureAction: "copy-structure-sequence"}}, api);
assert.equal(api.copies.at(-1), "GGGCCC");
controls.handleAction(genericSingle, {dataset: {featureAction: "structure-download-fasta"}}, api);
assert.equal(api.downloads.at(-1).contents.split("\n").filter(line => line.startsWith(">")).length, 1);
assert.match(api.downloads.at(-1).contents, /\nGGGCCC\n$/);

// A record is a single sequence even after a two-window reference workflow.
const selectedRecord = localState({cplfoldEvidence: "hyb-blocks", homodimerOnly: true, constraintMode: "hyb-guided"});
controls.handleAction(selectedRecord, {dataset: {featureAction: "record-fold", recordIndex: "2"}}, api);
assert.equal(selectedRecord.structure.source, "record");
assert.equal(selectedRecord.structure.selectedRecordIndex, 2);
assert.equal(selectedRecord.structure.cplfoldMode, "single");
assert.equal(selectedRecord.structure.evidenceLayout, "single");
assert.equal(selectedRecord.structure.workflow, "custom");
assert.equal(selectedRecord.structure.homodimerOnly, false);
assert.equal(selectedRecord.structure.cplfoldEvidence, "none");
assert.equal(selectedRecord.structure.constraintMode, "none");
assert.equal(api.navigations.at(-1), "structure");

// Leaving the default setup's sequence-only fallback is the user's own choice of HYB evidence.
const fallback = state({cplfoldMode: "single", source: "reference", rna: "A", start: 1, end: 75, defaultRegionEnd: 201, evidenceFallback: "A|1|75|", evidenceSeen: "none"});
assert.equal(ui.handleAction("structure-use-hyb-evidence", fallback, api), true);
assert.deepEqual(plain([fallback.structure.cplfoldEvidence, fallback.structure.evidenceChosen, fallback.structure.evidenceFallback === undefined, fallback.structure.source]), ["hyb-blocks", true, true, "reference"]);
assert.equal(pages.syncDefaultStructureEvidence(fallback), false);
assert.equal(fallback.structure.cplfoldEvidence, "hyb-blocks", "the default-setup fallback leaves a chosen evidence mode alone");

// Both pair kernels bypass the independent post-scoring worker. Exercise each
// transport and strand layout; switching candidates must switch C, never B.
async function verifyPairScoring() {
  vm.runInContext(fs.readFileSync(path.join(root, "web", "structure-editor.js"), "utf8"), context);
  const editor = context.window.Hyb2StructureEditor;
  for (const kernel of ["comrades", "duplex"]) for (const layout of ["single", "cofold"]) for (const native of [false, true]) {
    const target = localState({cplfoldMode: layout, cplfoldKernel: kernel, ...(layout === "single" ? {end: "18"} : {})});
    const {sequence, cutpoint} = pages.getStructureSequence(target);
    const end = sequence.lastIndexOf("C") + 1;
    const pairList = [0, 1].map((i) => ({...pairs[i], left: 1 + i, right: end - i, bonusSupport: i ? 0.6 : 1.8, evidenceSupport: i ? 0.6 : 1.8}));
    function member(list, counts, score) {
      const db = Array(sequence.length).fill(".");
      list.forEach(p => { db[p.left - 1] = "("; db[p.right - 1] = ")"; });
      return {...candidate, pairs: list, dotBracket: db.join(""), unpaired: sequence.length - 2 * list.length,
        comradesPairCounts: counts, comradesScore: score};
    }
    const first = member(pairList, [10, 2], 24), second = member(pairList.slice(1), [2], 4);
    const response = {...first, engine: cutpoint ? "CPLcofold" : "CPLfold", sequence, cutpoint, candidates: [first, second],
      parameters: {supportKernel: kernel}, evidence: {source: "hyb-block-intervals", supportKernel: kernel, maximumBonus: 1.8},
      nucleotideSupport: Array(sequence.length).fill(1.8), maximumNucleotideSupport: 1.8};
    let reply = response;
    if (native) context.window.Hyb2Desktop = {enabled: true, capabilities: {cplfoldNative: true, cplcofoldNative: true},
      predictCplfold: request => { assert.equal(request.parameters.supportKernel, kernel); return Promise.resolve(reply); }};
    async function run() {
      assert.equal(controller.predict(target, api), true);
      const count = workers.length;
      if (native) await new Promise(resolve => setImmediate(resolve));
      else target.structureWorker.onmessage({data: {type: "complete", result: reply}});
      assert.equal(workers.length, count, "no second arm estimator should start");
    }
    await run();
    assert.equal(target.structure.status, "complete", target.structure.message);
    const selected = target.structure.result, model = kernel === "comrades" ? "RNAcofold" : "RNAduplex";
    assert.match(pages.renderStructureResult(selected), new RegExp("COMRADES score</span><strong>24</strong><small>" + model + " counts · up to 1,000,000 per paired endpoint"));
    const drawing = editor.evidenceFor(selected);
    assert.equal(drawing.kind, model + " supporting observations");
    assert.deepEqual([drawing.values[0], drawing.values[end - 1], drawing.values[1], drawing.values[end - 2], drawing.values[2]], [10, 10, 2, 2, 0]);
    const arcs = ui.buildStructureDiagram(selected, null, "arc").children.filter(node => node.attributes["data-arc-source"] === "prediction");
    assert.match(arcs[1].children[0].textContent, /HYB bonus 0\.6000; raw pair count 2/);
    assert.equal(Number(arcs[1].attributes.opacity), 0.32 + 0.64 * 2 / 10);
    assert.equal(ui.selectCplfoldCandidate(selected, 1), true);
    assert.match(pages.renderStructureResult(selected), /COMRADES score<\/span><strong>4<\/strong>/);
    assert.equal(editor.evidenceFor(selected).values[0], 0);
    assert.equal(ui.structureReport(target, selected).prediction.comradesScore, 4);
    assert.deepEqual(ui.basePairTsv(selected).trim().split("\n")[1].split("\t").slice(8, 10), ["saturating_hyb_bonus", "0.6"]);
    assert.equal(ui.basePairTsv(selected).trim().split("\t").at(-1), "2");
    const capped = member(pairList.slice(0, 1), [2_000_000], 2_000_000);
    reply = {...response, ...capped, candidates: [capped], evidence: {...response.evidence, pairCounter: "host-supplied evidencePairs", saturatingQ: 2_000_000}};
    await run();
    assert.equal(target.structure.status, "complete", target.structure.message);
    const cappedReport = ui.structureReport(target, target.structure.result);
    assert.equal(cappedReport.prediction.comradesScore, 2_000_000);
    assert.match(cappedReport.methodScope.note, /host-supplied.*min\(C_ij, 1,000,000\)/);
    assert.equal(editor.evidenceFor(target.structure.result).values[0], 2_000_000);
    for (const bad of [{...response, comradesScore: undefined}, {...response, candidates: [first, {...second, comradesPairCounts: []}]},
      {...reply, comradesScore: 4_000_000},
      {...response, evidence: {...response.evidence, supportKernel: "gaussian"}}]) {
      reply = bad;
      await run();
      assert.equal(target.structure.status, "error");
      assert.equal(target.structure.result, null);
      assert.match(target.structure.message, /raw COMRADES counts and scores/);
    }
    delete context.window.Hyb2Desktop;
  }
}
verifyPairScoring().then(() => console.log("cplcofold-ui-controller-export-contract: ok")).catch(error => { console.error(error); process.exitCode = 1; });
