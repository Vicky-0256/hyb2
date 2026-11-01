"use strict";

// Actual mounted app.js listeners, native-control event order and a manual
// task queue. No browser, folding runtime or network requests are needed.
const assert = require("node:assert/strict"), fs = require("node:fs"), path = require("node:path"), vm = require("node:vm");
const root = path.resolve(__dirname, "..");
const appFile = process.argv[2] || path.join(root, "web/app.js");

function harness() {
  const timers = new Map(), actions = [], fileClicks = [];
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
      this.classList = {add() {}, remove() {}};
    }
    addEventListener(type, listener) {
      if (!this.listeners.has(type)) this.listeners.set(type, []);
      this.listeners.get(type).push(listener);
    }
    removeEventListener(type, listener) { this.listeners.set(type, (this.listeners.get(type) || []).filter(item => item !== listener)); }
    contains(node) { return this.controls.includes(node) && node.isConnected; }
    querySelector() { return null; }
    set innerHTML(value) {
      this.renders++;
      this.controls.forEach(control => { control.isConnected = false; });
      if (context && context.document.activeElement && !context.document.activeElement.isConnected) context.document.activeElement = null;
      this.controls = [];
      if (!value || !appState) return;
      const add = (tag, type, dataset, fieldValue, id) => this.controls.push(new Control(this, tag, type, dataset, fieldValue, id));
      for (const key of ["start", "end"]) add("input", "number", {feature: "structure-control", key}, appState.structure[key]);
      for (const key of ["workflow", "engine"]) add("select", "select-one", {feature: "structure-control", key}, appState.structure[key]);
      add("button", "button", {featureAction: "test-action"});
      for (const action of ["choose-hyb", "choose-fasta"]) add("button", "button", {action});
      for (const featureAction of ["choose-comparison-files", "replace-hyb"]) add("button", "button", {featureAction});
      for (const id of ["hyb-file-input", "fasta-file-input", "comparison-file-input"]) add("input", "file", {}, "", id);
    }
    get(key) { return this.controls.find(control => control.dataset.key === key || control.dataset.action === key || control.dataset.featureAction === key || control.id === key); }
    emit(type, target, extra = {}) {
      assert.equal(target.isConnected, true, `${type} must originate on a mounted control`);
      for (const listener of [...(this.listeners.get(type) || [])]) listener({type, target, preventDefault() {}, ...extra});
    }
  }
  const host = new Host();
  context = {
    console, Intl, URLSearchParams, navigator: {}, HYB2_AUTO_MOUNT: false,
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
        return {setProvider() {}, ensure() { return null; }, destroy() {}};
      }
    },
    // Stand-in rendering retains the relevant browser effect: innerHTML
    // replacement disconnects every old control and resets its focus.
    Hyb2Pages: {renderStructure() { return "<form></form>"; }}
  };
  context.window = context; vm.createContext(context);
  for (const file of ["analysis-data.js", "analysis-ui.js"]) vm.runInContext(fs.readFileSync(path.join(root, "web", file), "utf8"), context, {filename: file});
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
  return {host, context, state: () => appState, controller, mount, flush, dirty, openFromDirty, actions, fileClicks};
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
// Keyboard or mouse can reach a select after a callback is already queued.
for (const pointer of [false, true]) {
  const h = harness(), input = h.dirty(), select = h.host.get("workflow");
  h.host.emit("change", input); if (pointer) h.host.emit("pointerdown", select);
  select.focus(); h.flush(); assert.equal(h.host.renders, 0, "queued commit must respect a subsequently focused select");
  select.value = "short-intramolecular"; h.host.emit("change", select); h.flush();
  assert.equal(h.state().structure.workflow, "short-intramolecular"); assert.equal(h.host.renders, 1);
}
// A quick keyboard selection can commit before the older task even starts.
{
  const h = harness(), input = h.dirty(), select = h.host.get("workflow");
  h.host.emit("change", input); select.focus(); select.value = "long-intramolecular";
  h.host.emit("change", select); assert.equal(h.host.renders, 1);
  h.flush(); assert.equal(h.host.renders, 1, "select commit cancels an already queued numeric callback");
}
// Moving between selects must not flush in the gap between blur and focus,
// including when focusout follows pointerdown on the new select.
for (const pointer of [false, true]) {
  const h = harness(), select = h.openFromDirty(), next = h.host.get("engine");
  if (pointer) h.host.emit("pointerdown", next);
  h.host.emit("focusout", select, {relatedTarget: next}); next.focus(); h.flush(); assert.equal(h.host.renders, 0);
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
console.log("input-select-event-contract: ok (18 event sequences)");
