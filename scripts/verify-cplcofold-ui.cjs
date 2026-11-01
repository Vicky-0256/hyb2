"use strict";
// UI/controller contracts only: no folding runtime or network requests.
const assert = require("node:assert/strict"), fs = require("node:fs"), path = require("node:path"), vm = require("node:vm");
const root = path.resolve(__dirname, ".."), workers = [];
class Worker {
  constructor(url) { this.url = url; workers.push(this); }
  postMessage(message) { this.message = message; }
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
  window: {Hyb2Data: {normaliseFoldSequence(value) {
    const sequence = String(value || "").replace(/\s+/g, "").toUpperCase().replace(/T/g, "U");
    return {sequence, invalid: [...new Set(sequence.match(/[^ACGU]/g) || [])]};
  }}}
});
for (const file of ["comrades-analysis.js", "analysis-pages.js", "structure-controller.js", "structure-ui.js", "analysis-ui.js"]) {
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
const changed = state({cplfoldMode: "single", cplfoldEnergyModel: "CC09"});
controls.handleChange(changed, {dataset: {feature: "structure-control", key: "cplfoldMode"}, value: "cofold", type: "select-one"}, api);
assert.equal(changed.structure.cplfoldEnergyModel, "DP09"); assert.equal(changed.structure.workflow, "custom");
current = state(); assert.equal(controller.predict(current, api), true);
assert.match(workers.at(-1).url, /cplfold\.worker\.mjs/);
assert.deepEqual(plain([workers.at(-1).message.sequence, workers.at(-1).message.cutpoint]), ["GGGCCC", 3]);
assert.equal(workers.at(-1).message.allowPseudoknot, false); controller.cancel(current);
assert.equal(controller.predict(state({cplfoldEnergyModel: "CC09"}), api), false);
assert.match(api.toasts.at(-1), /DP03 and DP09/);
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
assert.doesNotMatch(pages.renderStructureResult(current.structure.result, null, "folded", true), /data-view-mode="folded"|data-view-mode="circular"|toggle-structure-editor/);
ui.selectCplfoldCandidate(current.structure.result, 0);
const report = ui.structureReport(current, current.structure.result);
assert.equal(report.source.cutpoint, 3); assert.equal(report.source.displaySequence, "GGG&CCC");
assert.equal(report.prediction.dotBracketWithBreak, "(((&)))"); assert.equal(report.prediction.energyMetadata.energy_kind, "thermodynamic");
assert.match(ui.cplfoldCandidatesTsv(current.structure.result), /dot_bracket\tcutpoint/);
assert.match(ui.cplfoldCandidatesTsv(current.structure.result), /\(\(\(&\)\)\)\t3/);
assert.match(ui.basePairTsv(current.structure.result), /cutpoint\tleft_strand\tleft_strand_position/);
assert.match(ui.cplfoldEvidenceTsv(current.structure.result), /# cutpoint=3/);
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
  const target = state({source: "reference", workflow: "custom", rna: "A", start: "3", end: "8",
    secondRna: "A", secondStart: "13", secondEnd: "18", cplfoldEvidence: "hyb-blocks", ...overrides});
  target.summary.rnaNames = ["A", "B"];
  target.reference = {mapping: {A: "A", B: "B"}};
  target.view = {structureEvidence: {guided: target.structure.cplfoldEvidence === "hyb-blocks", rnas: ["A", "B"], records: localRecords,
    fasta: {mapping: {A: "A", B: "B"}, sequences: [{id: "A", sequence: localSequence}, {id: "B", sequence: localSequence}]}}};
  return target;
}
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
  target.region = {rna: "A", start: "3", end: "8", partner, partnerStart, partnerEnd};
  target.view.regionReference = {id: "A", start: 3, end: 8, sequence: "GGGCCC", params: {rna: "A", start: "3", end: "8"}};
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
assert.match(api.toasts.at(-1), /DP03 and DP09/);

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

console.log("cplcofold-ui-controller-export-contract: ok");
