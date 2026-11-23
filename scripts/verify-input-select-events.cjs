"use strict";

// Actual mounted app.js listeners, native-control event order and a manual
// task queue. No browser, folding runtime or network requests are needed.
const assert = require("node:assert/strict"), fs = require("node:fs"), path = require("node:path"), vm = require("node:vm");
const root = path.resolve(__dirname, "..");
const appFile = process.argv[2] || path.join(root, "web/app.js");

function harness() {
  const timers = new Map(), actions = [], fileClicks = [], data = {pending: null};
  let nextTimer = 0, context, appState;
  class Control {
    constructor(host, tag, type, dataset, value = "", id = "") {
      Object.assign(this, {host, tagName: tag.toUpperCase(), type, dataset, value, id, isConnected: true});
    }
    matches(selector) {
      return selector.split(",").some(part => {
        part = part.trim();
        if (/^[a-z]+$/i.test(part)) return this.tagName.toLowerCase() === part.toLowerCase();
        if (part === 'input[type="file"]') return this.tagName === "INPUT" && this.type === "file";
        const attr = part.match(/^\[data-([\w-]+)\]$/);
        return !!attr && Object.hasOwn(this.dataset, attr[1].replace(/-([a-z])/g, (_, char) => char.toUpperCase()));
      });
    }
    closest(selector) { return this.matches(selector) ? this : null; }
    hasAttribute(name) { return name === "title"; }
    get attributes() { return []; }
    // the real DOM call: the kept node takes this one's place in the page
    replaceWith(node) {
      this.host.controls[this.host.controls.indexOf(this)] = node;
      this.isConnected = false; node.isConnected = true;
    }
    get selectedOptions() { return [{text: this.value}]; }
    focus() { context.document.activeElement = this; }
    click() {
      assert.equal(this.isConnected, true, "file picker target must remain mounted");
      fileClicks.push(this.id); this.host.emit("click", this);
    }
  }
  class Host {
    constructor() {
      this.listeners = new Map(); this.controls = []; this.renders = 0; this.dataset = {};
      const classes = new Set();
      this.classList = {add: key => classes.add(key), remove: key => classes.delete(key), contains: key => classes.has(key)};
    }
    addEventListener(type, listener) {
      if (!this.listeners.has(type)) this.listeners.set(type, []);
      this.listeners.get(type).push(listener);
    }
    removeEventListener(type, listener) { this.listeners.set(type, (this.listeners.get(type) || []).filter(item => item !== listener)); }
    contains(node) { return this.controls.includes(node) && node.isConnected; }
    querySelector() { return null; }
    set innerHTML(value) {
      this.html = value;
      this.renders++;
      this.controls.forEach(control => { control.isConnected = false; });
      if (context && context.document.activeElement && !context.document.activeElement.isConnected) context.document.activeElement = null;
      this.controls = [];
      if (!value || !appState) return;
      const add = (tag, type, dataset, fieldValue, id) => this.controls.push(new Control(this, tag, type, dataset, fieldValue, id));
      for (const key of ["start", "end"]) add("input", "number", {feature: "structure-control", key}, appState.structure[key]);
      for (const key of ["workflow", "engine"]) add("select", "select-one", {feature: "structure-control", key}, appState.structure[key]);
      add("button", "button", {featureAction: "test-action"});
      add("summary", "", {});
      for (const action of ["choose-hyb", "choose-fasta", "add-fasta"]) add("button", "button", {action});
      for (const featureAction of ["choose-comparison-files", "replace-hyb", "apply-fasta-mapping", "remove-current-fasta-mapping"]) add("button", "button", {featureAction});
      for (const id of ["hyb-file-input", "fasta-file-input", "comparison-file-input"]) add("input", "file", {}, "", id);
      // Export/candidate controls come from the real page markup below, not
      // private app functions. The DOM stand-in only routes their clicks.
      for (const match of value.matchAll(/<(button|input|tr)\b([^>]*)>/g)) {
        const dataset = Object.fromEntries(Array.from(match[2].matchAll(/\bdata-([\w-]+)="([^"]*)"/g),
          attr => [attr[1].replace(/-([a-z])/g, (_, char) => char.toUpperCase()), attr[2]]));
        if (/^report-/.test(dataset.action) || dataset.featureAction === "select-cplfold-candidate") {
          add(match[1], match[1] === "input" ? "checkbox" : "button", dataset);
        }
      }
    }
    get(key) { return this.controls.find(control => control.dataset.key === key || control.dataset.action === key || control.dataset.featureAction === key || control.id === key); }
    emit(type, target, extra = {}) {
      assert.equal(target.isConnected, true, `${type} must originate on a mounted control`);
      for (const listener of [...(this.listeners.get(type) || [])]) listener({type, target, preventDefault() {}, ...extra});
    }
  }
  const host = new Host();
  context = {
    console, Intl, URLSearchParams, File, Blob, TextEncoder, crypto: globalThis.crypto, navigator: {}, HYB2_AUTO_MOUNT: false,
    setTimeout(callback) { const id = ++nextTimer; timers.set(id, callback); return id; },
    clearTimeout(id) { timers.delete(id); }, addEventListener() {}, removeEventListener() {},
    document: {
      activeElement: null, documentElement: {dataset: {}},
      getElementById(id) { return host.get(id) || null; }, querySelector() { return null; },
      querySelectorAll(selector) { return host.controls.filter(control => control.matches(selector)); }
    },
    Hyb2Provider: {implementsProvider() { return true; }},
    Hyb2PageData: {
      freshView() { return {}; },
      createController(settings) {
        appState = settings.state; appState.summary = {rnaNames: ["A", "B"]};
        appState.structure = context.Hyb2Data.defaultStructureState();
        Object.assign(appState.structure, {rna: "A", secondRna: "B", start: "3", end: "6", secondStart: "15", secondEnd: "18"});
        // data.pending: a page-data fetch still under way (the app keeps the
        // page on screen and redraws when it resolves)
        return {setProvider() {}, ensure() { return data.pending; }, destroy() {},
          queryHybrids() { return data.queryError ? Promise.reject(data.queryError) : Promise.resolve({rows: [], total: 0}); }};
      }
    },
    // Stand-in rendering retains the relevant browser effect: innerHTML
    // replacement disconnects every old control and resets its focus.
    Hyb2Pages: {renderStructure() { return "<form></form>"; }}
  };
  context.window = context; vm.createContext(context);
  for (const file of ["support-kernels.js", "analysis-data.js", "analysis-ui.js"]) vm.runInContext(fs.readFileSync(path.join(root, "web", file), "utf8"), context, {filename: file});
  const handleAction = context.Hyb2UI.handleAction;
  context.Hyb2UI.handleAction = (state, element, api) => {
    if (element.dataset.featureAction === "test-action") { actions.push({mounted: element.isConnected, start: state.structure.start}); return true; }
    return handleAction(state, element, api);
  };
  vm.runInContext(fs.readFileSync(appFile, "utf8"), context, {filename: appFile});
  const mount = () => context.Hyb2Web.mount(host, {provider: {}, shell: false, page: "structure"});
  const controller = mount(); host.renders = 0;
  function flush() {
    let iterations = 0;
    while (timers.size) {
      assert.ok(++iterations < 30, "timer queue must settle");
      const [id, callback] = timers.entries().next().value; timers.delete(id); callback();
    }
  }
  function dirty() {
    const input = host.get("start"); input.focus(); input.value = "4"; host.emit("input", input);
    assert.equal(appState.structure.start, "4"); assert.equal(host.renders, 0); return input;
  }
  function openFromDirty() {
    const input = dirty(), select = host.get("workflow");
    host.emit("pointerdown", select); host.emit("change", input);
    select.focus(); host.emit("click", select); flush();
    assert.equal(host.renders, 0, "opening select after numeric commit must not replace its DOM node");
    assert.equal(select.isConnected, true); return select;
  }
  return {host, context, state: () => appState, controller, mount, flush, dirty, openFromDirty, actions, fileClicks, data};
}

// Opening click is not a committed choice; change must apply the workflow
// once, cancelling any older render from the numeric/text field.
{
  const h = harness(), select = h.openFromDirty(); select.value = "long-intramolecular";
  h.host.emit("input", select);
  assert.equal(h.state().structure.workflow, "custom", "select input is not a second change handler");
  h.host.emit("change", select);
  assert.equal(h.state().structure.workflow, "long-intramolecular");
  assert.equal(h.state().structure.cplfoldMode, "cofold"); assert.equal(h.host.renders, 1);
  h.flush(); assert.equal(h.host.renders, 1, "no stale numeric render after select change");
  assert.equal(h.state().pendingCommittedInputRender, false);
}
// Cancel then blur/Tab: refresh once and restore the newly focused field.
{
  const h = harness(), select = h.openFromDirty(), next = h.host.get("end");
  h.host.emit("keydown", select, {key: "Escape"});
  h.host.emit("focusout", select, {relatedTarget: next}); next.focus(); h.flush();
  assert.equal(h.host.renders, 1); assert.equal(h.state().structure.workflow, "custom");
  assert.equal(h.context.document.activeElement.dataset.key, "end");
  assert.equal(h.state().pendingCommittedInputRender, false);
}
// A select pressed with the mouse, or given a key, after a callback is
// already queued is in use: its list may be open, so it is not replaced.
for (const pointer of [false, true]) {
  const h = harness(), input = h.dirty(), select = h.host.get("workflow");
  h.host.emit("change", input); if (pointer) h.host.emit("pointerdown", select);
  select.focus(); if (!pointer) h.host.emit("keydown", select, {key: "ArrowDown"});
  h.flush(); assert.equal(h.host.renders, 0, "queued commit must respect a select in use");
  select.value = "short-intramolecular"; h.host.emit("change", select); h.flush();
  assert.equal(h.state().structure.workflow, "short-intramolecular"); assert.equal(h.host.renders, 1);
}
// A select that focus only passed to with Tab is not in use: the page is
// redrawn with the committed value and the select keeps focus.
{
  const h = harness(), input = h.dirty(), select = h.host.get("workflow");
  h.host.emit("change", input); h.host.emit("keydown", input, {key: "Tab"}); select.focus(); h.flush();
  assert.equal(h.host.renders, 1, "Tab to a select must not hold back the committed redraw");
  const current = h.context.document.activeElement;
  assert.equal(current.dataset.key, "workflow"); assert.equal(current.isConnected, true);
  current.value = "short-intramolecular"; h.host.emit("change", current); h.flush();
  assert.equal(h.state().structure.workflow, "short-intramolecular"); assert.equal(h.host.renders, 2);
  assert.equal(h.state().pendingCommittedInputRender, false);
}
// Typing goes on in the next field while the committed redraw runs: that
// field is kept (caret and selection stay) when the redraw did not change
// its value, and replaced when it did.
for (const changed of [false, true]) {
  const h = harness(), input = h.dirty(), next = h.host.get("end");
  h.host.emit("change", input); next.focus(); next.value = "61"; h.host.emit("input", next);
  if (changed) h.state().structure.end = "60";
  // the committed redraw, then the one that follows typing in a Structure
  // field (app.js followTyping); a field the redraw replaced is not followed
  h.flush(); assert.equal(h.host.renders, changed ? 1 : 2);
  const current = h.context.document.activeElement;
  assert.equal(current.dataset.key, "end"); assert.equal(current.isConnected, true);
  assert.equal(current === next, !changed, changed ? "a changed value shows the new field" : "the field being typed in must be kept");
  assert.equal(current.value, changed ? "60" : "61"); assert.equal(h.host.get("end"), current);
}
// Text typed but not yet committed is never overwritten by a redraw someone
// else asked for, even when the page would draw another value there.
{
  const h = harness(), input = h.dirty(), next = h.host.get("end");
  h.host.emit("change", input); next.focus(); h.host.emit("focusin", next);
  next.value = "61"; // typed, no change event yet; the page still holds "6"
  h.flush(); assert.equal(h.host.renders, 1);
  const current = h.context.document.activeElement;
  assert.equal(current, next, "uncommitted text must stay in its field"); assert.equal(current.value, "61");
  assert.equal(current.isConnected, true); assert.equal(h.host.get("end"), next);
}
// Typing in an RNA Structure range field redraws the page around it from the
// first key (setup line and length follow), without waiting for a commit.
{
  const h = harness(), input = h.host.get("start");
  input.focus(); h.host.emit("focusin", input); input.value = "5"; h.host.emit("input", input);
  assert.equal(h.host.renders, 0, "no redraw on the key itself");
  h.flush(); assert.equal(h.host.renders, 1, "the page follows typing");
  assert.equal(h.context.document.activeElement, input, "the typed field stays"); assert.equal(input.isConnected, true);
  assert.equal(input.value, "5"); assert.equal(h.state().structure.start, "5");
}
// A select changed from the keyboard keeps focus through its own redraw.
{
  const h = harness(), select = h.host.get("workflow");
  select.focus(); h.host.emit("keydown", select, {key: "l"}); select.value = "long-intramolecular";
  h.host.emit("change", select); assert.equal(h.host.renders, 1);
  const current = h.context.document.activeElement;
  assert.ok(current && current !== select, "the redraw replaced the select");
  assert.equal(current.dataset.key, "workflow"); assert.equal(current.isConnected, true);
}
// A press on a section's summary commits the field and ends in a click that
// toggles the section: the redraw waits for that click.
{
  const h = harness(), input = h.dirty(), summary = h.host.controls.find(control => control.tagName === "SUMMARY");
  h.host.emit("pointerdown", summary); h.host.emit("change", input); summary.focus(); h.flush();
  assert.equal(h.host.renders, 0, "the pressed summary must still be mounted for its click");
  assert.equal(summary.isConnected, true);
  h.host.emit("click", summary); h.flush(); assert.equal(h.host.renders, 1);
}
// A quick keyboard selection can commit before the older task even starts.
{
  const h = harness(), input = h.dirty(), select = h.host.get("workflow");
  h.host.emit("change", input); select.focus(); select.value = "long-intramolecular";
  h.host.emit("change", select); assert.equal(h.host.renders, 1);
  h.flush(); assert.equal(h.host.renders, 1, "select commit cancels an already queued numeric callback");
}
// Moving from one select to another that is then used (mouse press or a
// key) must not flush in the gap between blur and focus.
for (const pointer of [false, true]) {
  const h = harness(), select = h.openFromDirty(), next = h.host.get("engine");
  if (pointer) h.host.emit("pointerdown", next);
  h.host.emit("focusout", select, {relatedTarget: next}); next.focus();
  if (!pointer) h.host.emit("keydown", next, {key: " "});
  h.flush(); assert.equal(h.host.renders, 0);
  next.value = "viennarna"; h.host.emit("change", next); h.flush();
  assert.equal(h.state().structure.engine, "viennarna"); assert.equal(h.host.renders, 1);
}
// Ordinary actions receive the edited value before a deferred redraw.
for (const fromSelect of [false, true]) {
  const h = harness(), select = fromSelect ? h.openFromDirty() : null;
  const input = select ? null : h.dirty(), button = h.host.get("test-action");
  h.host.emit("pointerdown", button);
  if (select) h.host.emit("focusout", select, {relatedTarget: button}); else h.host.emit("change", input);
  button.focus(); h.flush(); assert.equal(h.host.renders, 0);
  h.host.emit("click", button); assert.deepEqual(h.actions, [{mounted: true, start: "4"}]);
  h.flush(); assert.equal(h.host.renders, 1);
}
// Existing file actions must not redraw after opening the native picker.
// Its input emits a nested click before the outer button's click finishes.
// Replace HYB intentionally closes its dialog before opening the picker.
for (const [action, id] of [["choose-hyb", "hyb-file-input"], ["choose-fasta", "fasta-file-input"], ["choose-comparison-files", "comparison-file-input"], ["replace-hyb", "hyb-file-input"]]) {
  for (const alreadyQueued of [false, true]) {
    const h = harness(), input = h.dirty(), button = h.host.get(action);
    if (alreadyQueued) h.host.emit("change", input);
    h.host.emit("pointerdown", button);
    if (!alreadyQueued) h.host.emit("change", input);
    button.focus(); h.host.emit("click", button);
    const renders = h.host.renders;
    assert.equal(renders, action === "replace-hyb" ? 1 : 0, action);
    h.flush(); assert.equal(h.host.renders, renders, `${action}: no deferred redraw after picker opens`);
    assert.deepEqual(h.fileClicks, [id]); assert.equal(h.state().pendingCommittedInputRender, false);
  }
}
// Destroy cancels queued work and removes every listener; remount is clean.
{
  const h = harness(), input = h.dirty(); h.host.emit("change", input);
  assert.equal(h.host.listeners.get("focusout").length, 1); h.controller.destroy();
  assert.ok([...h.host.listeners.values()].every(listeners => listeners.length === 0));
  const fresh = h.mount(), renders = h.host.renders;
  h.flush(); assert.equal(h.host.renders, renders, "old commit must not redraw a remounted host");
  assert.equal(h.host.listeners.get("focusout").length, 1);
  fresh.destroy(); assert.ok([...h.host.listeners.values()].every(listeners => listeners.length === 0));
}
// Focus survives a press that redraws the page: a button replaced by another
// (Run → Running…, Predict ↔ Cancel) shares data-focus-key with it, the
// replacement stays focusable, and buttons sharing an action are told apart
// by their other data attributes (the structure view switch).
{
  const web = file => fs.readFileSync(path.join(root, "web", file), "utf8");
  const app = fs.readFileSync(appFile, "utf8"), deseq = web("deseq-card.js"), pages = web("analysis-pages.js");
  assert.match(deseq, /aria-disabled="true" data-focus-key="deseq-run">Running…/, "Running… keeps focus (aria-disabled, not disabled)");
  assert.match(deseq, /data-feature-action="deseq-run" data-focus-key="deseq-run"/);
  assert.match(pages, /data-feature-action="cancel-structure" data-focus-key="structure-run"/);
  assert.match(pages, /data-feature-action="predict-structure" data-focus-key="structure-run"/);
  assert.match(app, /controlRow\(element\) === identity\.row/);
  assert.match(app, /function settleFocus\(/);
  assert.match(app, /getElementById\("main-content"\)/);
}
// The committed redraw may first wait for the page's data. A select opened
// during that wait is not replaced when the data lands; the redraw follows
// its focusout. Without a select in use the redraw happens at once.
(async function () {
  for (const used of [false, true]) {
    const h = harness(), input = h.dirty(), select = h.host.get("workflow");
    let arrive; h.data.pending = new Promise(resolve => { arrive = resolve; });
    h.host.emit("change", input); h.host.emit("keydown", input, {key: "Tab"}); select.focus(); h.flush();
    assert.equal(h.host.renders, 0, "the page stays on screen while its data loads");
    if (used) h.host.emit("keydown", select, {key: "ArrowDown"});
    h.data.pending = null; arrive(); await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.host.renders, used ? 0 : 1, used ? "data must not replace a select in use" : "data redraws the page");
    if (!used) continue;
    assert.equal(select.isConnected, true); assert.equal(h.state().pendingCommittedInputRender, true);
    const next = h.host.get("end");
    h.host.emit("focusout", select, {relatedTarget: next}); next.focus(); h.flush();
    assert.equal(h.host.renders, 1); assert.equal(h.context.document.activeElement.dataset.key, "end");
    assert.equal(h.state().pendingCommittedInputRender, false);
  }
  // Compare page report: a field committed by pressing another text field,
  // whose committed redraw waits for data. The pressed field is selected
  // whole (select all) before the data lands: the redraw keeps that node, so
  // its value and selection stay and the next keys replace the selection.
  for (const typedBefore of [false, true]) {
    const h = harness(); let input = h.dirty();
    if (typedBefore) {
      // the field was typed in and committed earlier (Condition A label "ZIKV7")
      const next = h.host.get("end"); h.host.emit("change", input); next.focus(); h.host.emit("focusin", next);
      next.value = "9"; h.host.emit("input", next); h.host.emit("change", next); h.flush();
      input = h.host.get("start"); input.focus(); h.host.emit("focusin", input);
    }
    let arrive; h.data.pending = new Promise(resolve => { arrive = resolve; });
    input.value = "5"; h.host.emit("input", input);
    const field = h.host.get("end"), renders = h.host.renders;
    h.host.emit("pointerdown", field); h.host.emit("change", input); field.focus(); h.host.emit("focusin", field);
    h.host.emit("click", field); h.flush();
    assert.equal(h.host.renders, renders, "the page stays while the committed redraw waits for data");
    Object.assign(field, {selectionStart: 0, selectionEnd: field.value.length}); // select all
    h.data.pending = null; arrive(); await new Promise(resolve => setImmediate(resolve)); h.flush();
    assert.equal(h.host.renders, renders + 1, "the data redraws the page");
    const current = h.context.document.activeElement;
    assert.equal(current, field, "the selected field must be kept through the redraw"); assert.equal(current.isConnected, true);
    assert.equal(h.host.get("end"), field);
    assert.deepEqual([current.value, current.selectionStart, current.selectionEnd], [field.value, 0, field.value.length], "value and select-all survive");
  }
  // The first pipe in an appended file changes the original A_B's HYB ID to
  // A-B. Rebuild automatic matches, but never reinterpret a physical choice.
  for (const action of [null, "apply-fasta-mapping", "remove-current-fasta-mapping"]) {
    const h = harness(), state = h.state();
    state.file = new File([""], "fixture.hyb");
    state.summary.rnaNames = ["A_B", "A-B"];
    const upload = async (text, name, append) => {
      h.host.emit("click", h.host.get(append ? "add-fasta" : "choose-fasta"));
      const input = h.host.get("fasta-file-input");
      input.files = [new File([text], name)];
      h.host.emit("change", input);
      await new Promise(resolve => setImmediate(resolve));
    };
    const bases = rna => h.context.Hyb2Data.extractReference(state.fasta, rna)?.sequence ?? null;
    const c = "C".repeat(12), a = "A".repeat(12);
    await upload(">A_B\n" + c, "plain.fa", false);
    assert.equal(bases("A_B"), c);
    if (action) {
      state.fasta.mappingEditorRna = "A_B"; state.fasta.mappingEditorReference = "A_B";
      h.host.emit("click", h.host.get(action));
    }
    await upload(">A|B\n" + a, "piped.fa", true);
    assert.equal(bases("A-B"), c, "the appended reference contains both physical records");
    assert.equal(bases("A_B"), !action ? a : action === "apply-fasta-mapping" ? c : null, String(action));
  }
  // An open export panel does not pin the report to the candidate that was
  // selected when it opened. Exercise mounted app listeners, real result
  // serialization and real ZIP bytes; only the provider and save sink stand in.
  const waitFor = async predicate => {
    const deadline = Date.now() + 5000;
    while (!predicate()) {
      assert.ok(Date.now() < deadline, "report action must settle");
      await new Promise(resolve => setTimeout(resolve, 5));
    }
  };
  const unzip = async blob => {
    const bytes = Buffer.from(await blob.arrayBuffer()), files = {};
    let at = 0;
    while (bytes.readUInt32LE(at) === 0x04034b50) {
      assert.equal(bytes.readUInt16LE(at + 8), 0, "stored ZIP entries");
      const size = bytes.readUInt32LE(at + 18), length = bytes.readUInt16LE(at + 26), extra = bytes.readUInt16LE(at + 28);
      const name = bytes.subarray(at + 30, at + 30 + length).toString().replace(/^hyb2-result\//, "");
      const start = at + 30 + length + extra;
      files[name] = bytes.subarray(start, start + size).toString(); at = start + size;
    }
    return files;
  };
  for (const audience of ["ai", "analysis"]) {
    const h = harness(), state = h.state(), downloads = [], copies = [];
    const excludedFile = audience === "ai" ? "report.md" : "tables/top_rnas.tsv";
    for (const file of ["analysis-pages.js", "structure-ui.js", "results.js", "report-page.js"]) {
      vm.runInContext(fs.readFileSync(path.join(root, "web", file), "utf8"), h.context, {filename: file});
    }
    h.context.Hyb2Desktop = {enabled: true, saveFile(name, blob) { downloads.push({name, blob}); return Promise.resolve(true); }};
    h.context.navigator.clipboard = {writeText(text) { copies.push(text); return Promise.resolve(); }};
    state.summary = {fileName: "candidates.hyb", rnaNames: ["A", "B"], validRecords: 0, invalidRecords: 0};
    state.view = {report: {filteredTotal: 0, filteredSample: [], partnerCounts: []}};
    const pair = (left, right) => ({left, right, leftBase: "G", rightBase: "C", type: "G–C", layer: "primary"});
    const first = {dotBracket: "(......)", energy: -1, pairs: [pair(1, 8)], unpaired: 6, type: "phase1", topology: "nested", crossingPairs: 0};
    const second = {...first, dotBracket: "((....))", energy: -2, pairs: [pair(1, 8), pair(2, 7)], unpaired: 4};
    Object.assign(state.structure, {engine: "cplfold", cplfoldMode: "cofold", source: "paste", pastedSequence: "GGGG", cplfoldSequenceTwo: "CCCC", cplfoldEvidence: "none",
      status: "complete", result: {...first, engine: "CPLcofold", source: "paste", sequence: "GGGGCCCC", cutpoint: 4,
        strandLengths: [4, 4], selectedCandidate: 0, candidates: [first, second], evidence: {source: "none"}}});
    const click = dataset => {
      const control = h.host.controls.find(node => Object.entries(dataset).every(([key, value]) => node.dataset[key] === value));
      assert.ok(control, "rendered control " + JSON.stringify(dataset)); h.host.emit("click", control);
    };
    const save = async dataset => {
      const before = downloads.length; click(dataset); await waitFor(() => downloads.length > before); return downloads.at(-1).blob;
    };
    const select = index => {
      h.controller.navigate("structure");
      click({featureAction: "select-cplfold-candidate", candidateIndex: String(index)});
      h.controller.navigate("report");
      assert.match(h.host.html, new RegExp('data-result="R38">-' + (index + 1) + '\\.00<'), "report page uses the selected energy");
    };
    const assertCandidate = (text, index) => {
      const result = JSON.parse(text);
      assert.deepEqual([result.selectedCandidate, result.energy], [index + 1, -(index + 1)], audience + ": download agrees with the selected candidate");
    };
    h.controller.navigate("report");
    click({action: "report-export", audience}); await waitFor(() => state.reportExport?.status === "ready");
    click({action: "report-export-toggle", path: excludedFile});
    select(1);
    // Each export route must be first after a switch: another route must
    // not repair its stale cache and hide a regression.
    assertCandidate(await (await save({action: "report-export-file", audience, path: "results/structure_prediction.json"})).text(), 1);
    for (const index of [0, 1]) {
      select(index); // ZIP is first after each switch: no file download to repair a stale cache.
      const files = await unzip(await save({action: "report-export-zip"}));
      assertCandidate(files["results/structure_prediction.json"], index);
      assert.equal(JSON.parse(files["summary.json"]).results.find(result => result.id === "R38").value, -(index + 1));
      assert.equal(files[excludedFile], undefined);
      assert.deepEqual(JSON.parse(files["manifest.json"]).leftOutByResearcher, [excludedFile], "the user's exclusions survive refreshed exports");
      assert.equal(state.reportExport.list.find(file => file.path === excludedFile).excluded, true);
    }
    if (audience === "ai") {
      select(0);
      click({action: "report-export-copy"}); await waitFor(() => copies.length > 0);
      assert.match(copies.at(-1), /\| R38 \| S12 \| Predicted free energy \| -1\.00 \|/, "the copied AI summary is current too");
    }
    h.data.queryError = new Error("Synthetic report refresh failure");
    for (const action of ["report-export-file", "report-export-zip", ...(audience === "ai" ? ["report-export-copy"] : [])]) {
      const beforeFailure = [downloads.length, copies.length];
      state.toastMessage = "";
      click(action === "report-export-file" ? {action, audience, path: "results/structure_prediction.json"} : {action});
      await waitFor(() => state.toastMessage === "Synthetic report refresh failure" || downloads.length > beforeFailure[0] || copies.length > beforeFailure[1]);
      assert.deepEqual([downloads.length, copies.length], beforeFailure, action + ": failed refresh must not export a cached candidate");
    }
    h.data.queryError = null;
    click({action: "report-export-close"}); select(0);
    assertCandidate(await (await save({action: "report-export-file", audience: "analysis", path: "results/structure_prediction.json"})).text(), 0);
    h.controller.destroy();
  }
  console.log("input-select-event-contract: ok (FASTA append and current-candidate report file/ZIP exports)");
})().catch(error => { console.error(error); process.exit(1); });
