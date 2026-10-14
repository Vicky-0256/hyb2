/*
 * HYB2 Desktop shell core: backend calls, the project model, the global
 * selection, small DOM helpers and ui_state persistence. Pages register on
 * window.HD.pages[id] = { render(container), onShow(), onProjectChange() }.
 *
 * Everything from a manifest, the runtime or the file system is written with
 * textContent (h() never sets innerHTML).
 */
(function () {
  "use strict";

  var tauri = window.__TAURI__ || null;
  var rawInvoke = tauri && tauri.core && tauri.core.invoke;
  var listeners = Object.create(null);
  var saveTimer = null;

  var HD = window.HD = {
    pages: Object.create(null),
    state: {
      projectPath: null,
      manifest: null,
      samples: [],
      conditions: [],
      references: [],
      structures: [],
      stats: Object.create(null),     // sample id -> {records, reads, ...} from analysis_summary
      selection: null,                // {kind: "sample"|"condition", id}
      referenceId: null,
      gene: null,
      page: "data",
      sub: { explore: "contact-map", qc: "samples" },
      compare: { a: null, b: null },
      tasks: Object.create(null),
      analysisSnapshot: null
    }
  };

  // ---------------------------------------------------------------- helpers

  HD.h = function (tag, attrs, children) {
    var node = document.createElement(tag);
    var options = attrs || {};
    Object.keys(options).forEach(function (key) {
      var value = options[key];
      if (value === undefined || value === null || value === false) return;
      if (key === "className") node.className = value;
      else if (key === "text") node.textContent = String(value);
      else if (key === "on") Object.keys(value).forEach(function (name) { node.addEventListener(name, value[name]); });
      else if (key === "dataset") Object.keys(value).forEach(function (name) { node.dataset[name] = value[name]; });
      else if (key === "style") node.setAttribute("style", value);
      else if (key in node && typeof value !== "string") node[key] = value;
      else node.setAttribute(key, value === true ? "" : String(value));
    });
    (Array.isArray(children) ? children : (children === undefined ? [] : [children])).forEach(function (child) {
      if (child === null || child === undefined || child === false) return;
      node.appendChild(typeof child === "object" ? child : document.createTextNode(String(child)));
    });
    return node;
  };
  HD.clear = function (node) {
    while (node && node.firstChild) node.removeChild(node.firstChild);
    return node;
  };
  HD.byId = function (id) { return document.getElementById(id); };
  HD.basename = function (path) {
    var parts = String(path || "").split(/[\\/]+/);
    return parts[parts.length - 1] || String(path || "");
  };
  HD.errorText = function (error) {
    if (!error) return "Unknown error";
    if (typeof error === "string") return error;
    if (error.message) return error.message;
    if (typeof error.error === "string") return error.error;
    if (error.errors && error.errors.length) return error.errors.join("\n");
    return JSON.stringify(error);
  };
  HD.formatBytes = function (value) {
    var size = Number(value);
    if (!isFinite(size) || size < 0) return "—";
    var units = ["B", "KB", "MB", "GB", "TB"];
    var unit = 0;
    while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1; }
    return (unit === 0 ? String(size) : size.toFixed(size >= 100 ? 0 : 1)) + " " + units[unit];
  };
  HD.formatNumber = function (value) {
    var number = Number(value);
    if (!isFinite(number)) return "—";
    return number.toLocaleString("en-US");
  };
  HD.formatCompact = function (value) {
    var number = Number(value);
    if (!isFinite(number)) return "—";
    if (number >= 1e6) return (number / 1e6).toFixed(number >= 1e7 ? 0 : 1) + " M";
    if (number >= 1e4) return Math.round(number / 1e3) + " k";
    return number.toLocaleString("en-US");
  };
  HD.formatDuration = function (seconds) {
    var s = Math.max(0, Math.round(Number(seconds) || 0));
    if (s < 60) return s + " s";
    if (s < 3600) return Math.floor(s / 60) + " min " + (s % 60 ? (s % 60) + " s" : "");
    return Math.floor(s / 3600) + " h " + Math.round((s % 3600) / 60) + " min";
  };
  HD.formatTime = function (value) {
    if (!value) return "—";
    var date = new Date(value);
    return isNaN(date.getTime()) ? String(value) : date.toLocaleString();
  };
  HD.percent = function (part, total) {
    return total ? (100 * part / total).toFixed(1) + "%" : "—";
  };

  var toastTimer = null;
  HD.toast = function (message, kind) {
    var node = HD.byId("d-toast");
    if (!node) return;
    node.textContent = message;
    node.className = "d-toast" + (kind ? " d-toast-" + kind : "");
    node.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { node.hidden = true; }, kind === "error" ? 9000 : 4500);
  };

  // ---------------------------------------------------------------- events

  HD.on = function (name, listener) {
    (listeners[name] = listeners[name] || []).push(listener);
  };
  HD.emit = function (name, detail) {
    (listeners[name] || []).slice().forEach(function (listener) {
      try { listener(detail); } catch (error) { if (window.console) window.console.error(error); }
    });
  };

  // ---------------------------------------------------------------- backend

  HD.available = typeof rawInvoke === "function";
  HD.invoke = function (command, args) {
    if (!rawInvoke) return Promise.reject(new Error("Open the installed HYB2 Desktop application to use project operations."));
    return Promise.resolve(rawInvoke(command, args || {})).then(function (result) {
      if (result && result.ok === false && result.status !== "unavailable") {
        throw new Error(HD.errorText(result));
      }
      return result;
    }, function (error) { throw new Error(HD.errorText(error)); });
  };
  // Project data operations (Rust `project_op`, control lane).
  HD.op = function (operation, payload) {
    var request = Object.assign({}, payload || {}, { operation: operation, path: HD.state.projectPath });
    return HD.invoke("project_op", { request: request });
  };
  // Analysis queries (Rust `analysis_query`, compute lane) -> result.result.
  HD.query = function (operation, payload) {
    var request = Object.assign({}, payload || {}, { operation: operation, project_path: HD.state.projectPath });
    return HD.invoke("analysis_query", { request: request }).then(function (response) {
      return response && response.result !== undefined ? response.result : response;
    });
  };
  HD.pickFiles = function (title, multiple) {
    return HD.invoke("dialog_pick_files", { request: { title: title, multiple: multiple !== false } }).then(function (result) {
      return (result && result.paths) || [];
    });
  };
  HD.startTask = function (request) {
    var payload = Object.assign({}, request, { project_path: HD.state.projectPath });
    return HD.invoke("task_start", { request: payload }).then(function (snapshot) {
      if (snapshot && snapshot.status === "unavailable") throw new Error(HD.errorText(snapshot.error || "engine unavailable"));
      HD.emit("task", snapshot);
      return snapshot;
    });
  };
  HD.resumeTask = function (taskId) {
    return HD.invoke("task_resume", { request: { task_id: taskId, project_path: HD.state.projectPath } }).then(function (snapshot) {
      HD.emit("task", snapshot);
      return snapshot;
    });
  };
  HD.cancelTask = function (taskId) {
    return HD.invoke("task_cancel", { request: { task_id: taskId, project_path: HD.state.projectPath } }).then(function (snapshot) {
      HD.emit("task", snapshot);
      return snapshot;
    });
  };

  // ---------------------------------------------------------------- project model

  HD.sample = function (id) {
    return HD.state.samples.find(function (sample) { return sample.id === id; }) || null;
  };
  HD.reference = function (id) {
    return HD.state.references.find(function (reference) { return reference.id === id; }) || null;
  };
  HD.isReady = function (sample) {
    return !!(sample && (sample.ready || (sample.outputs && sample.outputs.index)));
  };
  HD.readySamples = function () {
    return HD.state.samples.filter(HD.isReady);
  };
  HD.conditionSamples = function (name) {
    var condition = HD.state.conditions.find(function (item) { return item.name === name; });
    var ids = condition ? condition.sample_ids : HD.state.samples.filter(function (s) { return s.condition === name; }).map(function (s) { return s.id; });
    return ids.map(HD.sample).filter(Boolean);
  };
  function deriveConditions(samples) {
    var map = {};
    var order = [];
    samples.forEach(function (sample) {
      if (!sample.condition) return;
      if (!map[sample.condition]) { map[sample.condition] = []; order.push(sample.condition); }
      map[sample.condition].push(sample.id);
    });
    return order.map(function (name) { return { name: name, sample_ids: map[name] }; });
  }

  // Selection keys: "s:<sample id>" or "c:<condition>".
  HD.selectionKey = function (selection) {
    return selection ? (selection.kind === "condition" ? "c:" : "s:") + selection.id : "";
  };
  HD.parseSelectionKey = function (key) {
    var text = String(key || "");
    if (/^c:/.test(text)) return { kind: "condition", id: text.slice(2) };
    if (/^s:/.test(text)) return { kind: "sample", id: text.slice(2) };
    return null;
  };
  // Analysed sample ids behind a selection.
  HD.selectionIds = function (selection) {
    var current = selection === undefined ? HD.state.selection : selection;
    if (!current) return [];
    var samples = current.kind === "condition" ? HD.conditionSamples(current.id) : [HD.sample(current.id)].filter(Boolean);
    return samples.filter(HD.isReady).map(function (sample) { return sample.id; });
  };
  HD.selectionLabel = function (selection) {
    var current = selection === undefined ? HD.state.selection : selection;
    if (!current) return "nothing selected";
    if (current.kind === "condition") {
      var count = HD.selectionIds(current).length;
      return current.id + (count === 1 ? " (1 sample)" : " (" + count + " merged)");
    }
    var sample = HD.sample(current.id);
    return sample ? sample.name : current.id;
  };
  // Options for every selection picker: conditions first, then samples.
  HD.selectionOptions = function () {
    var options = [];
    HD.state.conditions.forEach(function (condition) {
      var ready = HD.selectionIds({ kind: "condition", id: condition.name }).length;
      if (ready) options.push({ key: "c:" + condition.name, label: condition.name + " · " + ready + (ready === 1 ? " sample" : " replicates merged"), group: "Conditions" });
    });
    HD.readySamples().forEach(function (sample) {
      options.push({ key: "s:" + sample.id, label: sample.name + (sample.condition ? " · " + sample.condition : ""), group: "Samples" });
    });
    return options;
  };
  HD.fillSelectionSelect = function (select, currentKey, emptyLabel) {
    HD.clear(select);
    var options = HD.selectionOptions();
    if (!options.length) {
      select.appendChild(HD.h("option", { value: "", text: emptyLabel || "No analysed samples yet" }));
      select.disabled = true;
      return "";
    }
    select.disabled = false;
    var groups = {};
    options.forEach(function (option) {
      var group = groups[option.group];
      if (!group) {
        group = groups[option.group] = HD.h("optgroup", { label: option.group });
        select.appendChild(group);
      }
      group.appendChild(HD.h("option", { value: option.key, text: option.label }));
    });
    var keys = options.map(function (option) { return option.key; });
    select.value = keys.indexOf(currentKey) >= 0 ? currentKey : keys[0];
    return select.value;
  };

  // Gene list of the selected reference (or every gene when none).
  HD.genes = function () {
    var reference = HD.reference(HD.state.referenceId);
    var list = reference ? reference.sequences || [] : [];
    if (!list.length) {
      HD.state.references.forEach(function (item) { list = list.concat(item.sequences || []); });
    }
    return list;
  };
  HD.geneLength = function (name) {
    var match = null;
    HD.state.references.forEach(function (reference) {
      (reference.sequences || []).forEach(function (sequence) { if (sequence.name === name) match = sequence.length; });
    });
    return match;
  };

  // Applies list_samples (or open_project) data and keeps the selection valid.
  HD.applyProjectData = function (data) {
    var state = HD.state;
    state.samples = (data.samples || []).map(function (sample) {
      return Object.assign({}, sample, { ready: HD.isReady(sample) });
    });
    state.references = data.references || state.references || [];
    state.conditions = data.conditions && data.conditions.length ? data.conditions : deriveConditions(state.samples);
    if (!state.referenceId || !HD.reference(state.referenceId)) {
      state.referenceId = state.references.length ? state.references[0].id : null;
    }
    var genes = HD.genes().map(function (gene) { return gene.name; });
    if (!state.gene || genes.indexOf(state.gene) < 0) state.gene = genes[0] || null;
    var keys = HD.selectionOptions().map(function (option) { return option.key; });
    if (!state.selection || keys.indexOf(HD.selectionKey(state.selection)) < 0) {
      state.selection = keys.length ? HD.parseSelectionKey(keys[0]) : null;
    }
  };

  // Reloads samples/conditions/references from the backend.
  HD.refreshProject = function () {
    var path = HD.state.projectPath;
    if (!path) return Promise.resolve();
    var before = HD.selectionIds().join(",") + "|" + HD.readySamples().map(function (s) { return s.id; }).join(",");
    return HD.op("list_samples").catch(function () {
      return HD.invoke("project_open", { request: { path: path, check_files: false } }).then(function (opened) {
        var manifest = opened.manifest || {};
        return { samples: manifest.samples || [], references: manifest.references || [] };
      });
    }).then(function (data) {
      if (path !== HD.state.projectPath) return;
      HD.applyProjectData(data);
      var after = HD.selectionIds().join(",") + "|" + HD.readySamples().map(function (s) { return s.id; }).join(",");
      HD.emit("project", { readyChanged: before !== after });
    });
  };

  // Per-sample counts for the analysed samples (one summary call).
  HD.loadStats = function () {
    var ids = HD.readySamples().map(function (sample) { return sample.id; });
    if (!ids.length) return Promise.resolve(HD.state.stats);
    return HD.query("analysis_summary", { sample_ids: ids }).then(function (result) {
      (result.indexes || []).forEach(function (part, index) { HD.state.stats[ids[index]] = part; });
      HD.emit("stats");
      return HD.state.stats;
    });
  };

  // ---------------------------------------------------------------- ui_state

  HD.saveUiState = function () {
    var state = HD.state;
    if (!state.projectPath || !HD.available) return;
    var path = state.projectPath;
    clearTimeout(saveTimer);
    saveTimer = setTimeout(function () {
      if (path !== HD.state.projectPath) return;
      var payload = {
        desktop: {
          route: state.page,
          sub: state.sub,
          selection: state.selection,
          reference_id: state.referenceId,
          gene: state.gene,
          compare: state.compare
        },
        analysis: state.analysisSnapshot || {}
      };
      HD.invoke("project_update_ui_state", { request: { path: path, ui_state: payload } }).catch(function () {});
    }, 400);
  };
  HD.restoreUiState = function (raw) {
    var state = HD.state;
    var desktop = raw && typeof raw === "object" && raw.desktop && typeof raw.desktop === "object" ? raw.desktop : {};
    var pages = ["data", "qc", "explore", "compare", "structure", "report"];
    state.page = pages.indexOf(desktop.route) >= 0 ? desktop.route : "data";
    state.sub = Object.assign({ explore: "contact-map", qc: "samples" }, desktop.sub && typeof desktop.sub === "object" ? desktop.sub : {});
    state.selection = desktop.selection && desktop.selection.id ? { kind: desktop.selection.kind === "condition" ? "condition" : "sample", id: String(desktop.selection.id) } : null;
    state.referenceId = typeof desktop.reference_id === "string" ? desktop.reference_id : null;
    state.gene = typeof desktop.gene === "string" ? desktop.gene : null;
    state.compare = Object.assign({ a: null, b: null }, desktop.compare && typeof desktop.compare === "object" ? desktop.compare : {});
    state.analysisSnapshot = raw && raw.analysis && typeof raw.analysis === "object" ? raw.analysis : null;
  };

  // ---------------------------------------------------------------- dialogs

  // Shows a modal; content(body, close) fills it. Returns a Promise resolved
  // with the value passed to close().
  HD.modal = function (title, content, options) {
    var dialog = HD.byId("d-dialog");
    var settings = options || {};
    HD.clear(dialog);
    dialog.className = "d-dialog" + (settings.wide ? " d-dialog-wide" : "");
    return new Promise(function (resolve) {
      var done = false;
      function close(value) {
        if (done) return;
        done = true;
        if (dialog.open) dialog.close();
        resolve(value);
      }
      var body = HD.h("div", { className: "d-dialog-body" });
      dialog.appendChild(HD.h("div", { className: "d-dialog-head" }, [
        HD.h("h2", { className: "d-h2", text: title }),
        HD.h("button", { type: "button", className: "d-btn d-btn-small d-btn-ghost", text: "Close", on: { click: function () { close(null); } } })
      ]));
      dialog.appendChild(body);
      dialog.onclose = function () { close(null); };
      content(body, close);
      if (typeof dialog.showModal === "function") dialog.showModal();
      else dialog.setAttribute("open", "");
    });
  };
  HD.confirm = function (title, message, okLabel) {
    return HD.modal(title, function (body, close) {
      body.appendChild(HD.h("p", { text: message }));
      body.appendChild(HD.h("div", { className: "d-actions" }, [
        HD.h("button", { type: "button", className: "d-btn", text: "Cancel", on: { click: function () { close(false); } } }),
        HD.h("button", { type: "button", className: "d-btn d-btn-primary", text: okLabel || "OK", on: { click: function () { close(true); } } })
      ]));
    });
  };

  // Disables the buttons inside `container` while `action` runs.
  HD.busy = function (container, action) {
    var buttons = container ? Array.prototype.slice.call(container.querySelectorAll("button")) : [];
    buttons.forEach(function (button) { button.disabled = true; });
    if (container) container.setAttribute("aria-busy", "true");
    return Promise.resolve().then(action).finally(function () {
      buttons.forEach(function (button) { button.disabled = false; });
      if (container) container.removeAttribute("aria-busy");
    });
  };

  // Status chip for a sample stage or task status.
  HD.chip = function (status, label) {
    var value = String(status || "pending");
    var text = label || ({ succeeded: "done", skipped: "—", pending: "pending", running: "running", queued: "queued", failed: "failed", interrupted: "interrupted", cancelled: "cancelled" }[value] || value);
    return HD.h("span", { className: "d-chip d-chip-" + value.replace(/[^a-z]/g, ""), text: text, title: value });
  };

  // Navigation (implemented in app.js).
  HD.navigate = function (page, options) { HD.emit("navigate", { page: page, options: options || {} }); };
}());
