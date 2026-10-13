(function () {
  "use strict";
  var tauri = window.__TAURI__ || null;
  var invoke = tauri && tauri.core && tauri.core.invoke;
  var DESKTOP_VIEWS = ["home", "analysis", "tasks", "runtime"];
  var ACTIVE_TASK_STATES = ["queued", "running"];
  var RECENT_KEY = "hyb2.desktop.recentProjects";
  var RECENT_MAX = 8;
  var loaded = false;
  var loadingAnalysis = null;
  var assets = window.HYB2_ASSET_STATUS;
  var currentProjectPath = null;
  var currentManifest = null;
  var projectToken = 0;
  var projectInputs = { hyb: null, fasta: null };
  // ui_state is namespaced (contract C6): the desktop shell owns
  // desktop.route, the analysis workspace owns analysis.
  var uiState = { desktop: { route: "home" }, analysis: null };
  var analysisRestorePending = false;
  var uiStateSaveTimer = null;
  var tasks = {};
  var taskPollTimer = null;
  var taskPollBusy = false;

  if (assets) {
    var missing = [];
    if (!assets.vienna.assetsPresent) missing.push("ViennaRNA / COMRADES");
    if (!assets.cplfold.assetsPresent) missing.push("CPLfold (Pyodide)");
    byId("d-analysis-note").textContent = missing.length
      ? "Missing folding assets: " + missing.join(", ") + ". File import, contact maps and interaction analysis remain available. Native CPLfold requires the installed runtime."
      : "Folding runtime files are bundled; engine startup is checked when used. Native CPLfold is available from Task Center when a portable runtime is installed.";
  }

  // ---------------------------------------------------------------------------
  // Small helpers. Everything that comes from a manifest, the runtime or the
  // file system is written with textContent, never innerHTML.
  // ---------------------------------------------------------------------------
  function byId(id) {
    return document.getElementById(id);
  }
  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = String(text);
    return node;
  }
  function clear(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }
  function show(id, value) {
    byId(id).textContent = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  }
  function errorText(error) {
    if (!error) return "Unknown error";
    if (typeof error === "string") return error;
    if (error.message) return error.message;
    if (error.error && typeof error.error === "string") return error.error;
    if (error.errors && error.errors.length) return error.errors.join("\n");
    return JSON.stringify(error);
  }
  function basename(path) {
    var parts = String(path || "").split(/[\\/]+/);
    return parts[parts.length - 1] || String(path || "");
  }
  function isWindowsPath(path) {
    return /^[A-Za-z]:[\\/]/.test(path) || /^\\\\/.test(path);
  }
  function joinProjectPath(root, relative) {
    var sep = isWindowsPath(root) ? "\\" : "/";
    var base = String(root).replace(/[\\/]+$/, "");
    var rest = String(relative).replace(/^[\\/]+/, "").replace(/[\\/]+/g, sep);
    return base + sep + rest;
  }
  function safeRelative(relative) {
    var value = String(relative || "");
    return !!value && !/^[\\/]/.test(value) && !/^[A-Za-z]:/.test(value) && !/(^|[\\/])\.\.([\\/]|$)/.test(value);
  }
  function formatTime(value) {
    if (!value) return "—";
    var date = new Date(value);
    if (isNaN(date.getTime())) return String(value);
    return date.toLocaleString();
  }
  function formatBytes(value) {
    var size = Number(value);
    if (!isFinite(size) || size < 0) return "";
    if (size < 1024) return size + " B";
    if (size < 1024 * 1024) return (size / 1024).toFixed(1) + " KB";
    return (size / (1024 * 1024)).toFixed(1) + " MB";
  }

  // ---------------------------------------------------------------------------
  // ui_state persistence (contract C6)
  // ---------------------------------------------------------------------------
  function normalizeUiState(raw) {
    var result = { desktop: { route: "home" }, analysis: null };
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return result;
    if (Object.prototype.hasOwnProperty.call(raw, "desktop") || Object.prototype.hasOwnProperty.call(raw, "analysis")) {
      if (raw.desktop && typeof raw.desktop === "object" && DESKTOP_VIEWS.indexOf(raw.desktop.route) >= 0) {
        result.desktop.route = raw.desktop.route;
      }
      if (raw.analysis && typeof raw.analysis === "object" && !Array.isArray(raw.analysis)) {
        result.analysis = raw.analysis;
      }
      return result;
    }
    // Legacy flat {route, ...}: a desktop view name belongs to the shell,
    // anything else is an analysis-workspace snapshot.
    if (DESKTOP_VIEWS.indexOf(raw.route) >= 0) {
      result.desktop.route = raw.route;
      var rest = {};
      var keys = Object.keys(raw).filter(function (key) { return key !== "route"; });
      keys.forEach(function (key) { rest[key] = raw[key]; });
      result.analysis = keys.length ? rest : null;
    } else {
      result.analysis = raw;
    }
    return result;
  }
  function scheduleUiStateSave() {
    if (!currentProjectPath || !invoke) return;
    var path = currentProjectPath;
    clearTimeout(uiStateSaveTimer);
    uiStateSaveTimer = setTimeout(function () {
      uiStateSaveTimer = null;
      if (path !== currentProjectPath) return;
      var payload = { desktop: { route: uiState.desktop.route }, analysis: uiState.analysis || {} };
      invoke("project_update_ui_state", { request: { path: path, ui_state: payload } }).catch(function () {});
    }, 350);
  }

  // ---------------------------------------------------------------------------
  // Navigation
  // ---------------------------------------------------------------------------
  function activate(view, options) {
    if (DESKTOP_VIEWS.indexOf(view) < 0) view = "home";
    document.querySelectorAll(".d-nav").forEach(function (item) {
      var active = item.getAttribute("data-view") === view;
      item.classList.toggle("d-nav-active", active);
      if (active) item.setAttribute("aria-current", "page");
      else item.removeAttribute("aria-current");
    });
    document.querySelectorAll(".d-view").forEach(function (item) {
      item.classList.toggle("d-view-active", item.getAttribute("data-view") === view);
    });
    var changed = uiState.desktop.route !== view;
    uiState.desktop.route = view;
    if (changed && !(options && options.persist === false)) scheduleUiStateSave();
    if (view === "analysis" && !loaded) loadAnalysisWorkspace().catch(function () {});
    if (view === "tasks") refreshTaskList();
  }
  document.querySelectorAll(".d-nav").forEach(function (button) {
    button.addEventListener("click", function () { activate(button.getAttribute("data-view")); });
  });

  window.addEventListener("hyb2:session-change", function (event) {
    if (!currentProjectPath || !invoke || !event.detail || typeof event.detail !== "object") return;
    // The workspace renders its landing page before a deferred restore is
    // applied; do not let that transient snapshot overwrite the saved one.
    if (analysisRestorePending && (!event.detail.route || event.detail.route === "landing")) return;
    analysisRestorePending = false;
    if (JSON.stringify(event.detail) === JSON.stringify(uiState.analysis)) return;
    uiState.analysis = event.detail;
    scheduleUiStateSave();
  });
  window.addEventListener("hyb2:analysis-result", function (event) {
    if (!currentProjectPath || !invoke || !event.detail) return;
    var result = event.detail;
    var saved = {
      algorithm: result.algorithm, engine: result.engine, model: result.model,
      constraintMode: result.constraintMode, sequence: result.sequence,
      dotBracket: result.dotBracket, energy: result.energy,
      comradesScore: result.comradesScore, constraintCount: result.constraintCount,
      randomisation: result.randomisation, native: result.native === true,
      elapsedMs: result.elapsedMs
    };
    var path = currentProjectPath;
    invoke("project_save_analysis_run", { request: { path: path, result: saved } }).then(function (response) {
      if (response && response.run) {
        show("d-analysis-status", "Analysis result saved as " + response.run.run_id + ".");
        if (path === currentProjectPath && currentManifest) {
          currentManifest.runs = currentManifest.runs || [];
          currentManifest.runs.push({ id: response.run.run_id, kind: "structure-analysis", status: "succeeded", output_paths: [response.run.path] });
          renderSummary();
          renderRunHistory(currentManifest.runs);
        }
      }
    }).catch(function (error) { show("d-analysis-status", "Analysis completed but could not be saved: " + errorText(error)); });
  });

  // ---------------------------------------------------------------------------
  // Integrated analysis workspace
  // ---------------------------------------------------------------------------
  function appendStyle(file) {
    var link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = "./analysis/" + file;
    document.head.appendChild(link);
  }
  function appendScript(file) {
    return new Promise(function (resolve, reject) {
      var script = document.createElement("script");
      script.src = "./analysis/" + file;
      script.onload = resolve;
      script.onerror = function () { reject(new Error("Could not load desktop analysis asset " + file)); };
      document.body.appendChild(script);
    });
  }
  function loadAnalysisWorkspace() {
    if (loadingAnalysis) return loadingAnalysis;
    loadingAnalysis = (async function () {
      show("d-analysis-status", "Loading integrated analysis workspace…");
      window.HYB2_ASSET_BASE = "./analysis/";
      ["styles.css", "workspace.css", "analysis.css", "structure.css", "report.css", "vendor/fornac/fornac.css", "responsive.css"].forEach(appendStyle);
      var host = byId("d-analysis-host");
      var appRoot = document.createElement("div");
      appRoot.id = "app";
      appRoot.className = "app-shell desktop-integrated-analysis";
      host.appendChild(appRoot);
      // desktop-adapter.js must load first: it defines window.Hyb2Desktop
      // (native save dialog, native CPLfold) and starts runtime detection.
      var files = ["desktop-adapter.js", "vendor/d3/d3.v3.min.js", "vendor/fornac/fornac.js", "analysis-data.js", "comrades-analysis.js", "analysis-pages.js", "structure-controller.js", "structure-ui.js", "analysis-ui.js", "overview-page.js", "report-page.js", "app.js"];
      for (var index = 0; index < files.length; index += 1) {
        await appendScript(files[index]);
      }
      loaded = true;
      if (projectInputs.hyb || projectInputs.fasta) window.Hyb2Web.loadProjectFiles(projectInputs);
      if (uiState.analysis) window.Hyb2Web.restoreSession(uiState.analysis);
      show("d-analysis-status", "Integrated analysis workspace ready.");
      // Re-render once native capability detection settles so the structure
      // page switches to the native CPLfold path and its sequence limit.
      if (window.Hyb2Desktop && window.Hyb2Desktop.ready) {
        window.Hyb2Desktop.ready.then(function () {
          if (window.Hyb2Web && typeof window.Hyb2Web.refresh === "function") window.Hyb2Web.refresh();
        });
      }
    })().catch(function (error) {
      show("d-analysis-status", "Analysis could not load: " + errorText(error));
      throw error;
    });
    return loadingAnalysis;
  }

  // ---------------------------------------------------------------------------
  // Generic operation wrapper
  // ---------------------------------------------------------------------------
  async function operation(container, output, label, action) {
    var buttons = container.querySelectorAll("button");
    buttons.forEach(function (button) { button.disabled = true; });
    container.setAttribute("aria-busy", "true");
    show(output, label + "…");
    try {
      if (!invoke) throw new Error("Open the installed HYB2 Desktop application to use native project and runtime operations.");
      var result = await action();
      // An unavailable native engine is a structured capability result, not
      // an IPC transport failure; let the caller render the remediation.
      if (result && result.ok === false && result.status !== "unavailable") throw new Error((result.errors || [result.error || "Operation failed"]).join("\n"));
      return result;
    } catch (error) {
      show(output, label + " failed: " + errorText(error));
    } finally {
      buttons.forEach(function (button) { button.disabled = false; });
      container.removeAttribute("aria-busy");
    }
  }

  // ---------------------------------------------------------------------------
  // Recent projects (per-machine convenience; localStorage may be unavailable)
  // ---------------------------------------------------------------------------
  function readRecent() {
    try {
      var parsed = JSON.parse(window.localStorage.getItem(RECENT_KEY) || "[]");
      if (!Array.isArray(parsed)) return [];
      return parsed.filter(function (item) { return item && typeof item.path === "string" && item.path; }).slice(0, RECENT_MAX);
    } catch (_) {
      return [];
    }
  }
  function writeRecent(list) {
    try { window.localStorage.setItem(RECENT_KEY, JSON.stringify(list.slice(0, RECENT_MAX))); } catch (_) {}
  }
  function rememberProject(path, name) {
    var list = readRecent().filter(function (item) { return item.path !== path; });
    list.unshift({ path: path, name: String(name || basename(path)), opened_at: new Date().toISOString() });
    writeRecent(list);
    renderRecent();
  }
  function renderRecent() {
    var host = byId("d-recent-list");
    var list = readRecent();
    clear(host);
    byId("d-recent-empty").hidden = list.length > 0;
    list.forEach(function (item) {
      var row = el("li", "d-recent-item");
      var button = el("button", "d-recent-open");
      button.type = "button";
      button.appendChild(el("strong", null, item.name || basename(item.path)));
      button.appendChild(el("span", null, item.path));
      button.title = "Open " + item.path;
      button.addEventListener("click", function () {
        byId("d-open-path").value = item.path;
        openProject(item.path, byId("d-check-files").checked);
      });
      var forget = el("button", "d-btn d-btn-small", "Remove");
      forget.type = "button";
      forget.setAttribute("aria-label", "Remove " + item.path + " from recent projects");
      forget.addEventListener("click", function () {
        writeRecent(readRecent().filter(function (entry) { return entry.path !== item.path; }));
        renderRecent();
      });
      row.appendChild(button);
      row.appendChild(forget);
      host.appendChild(row);
    });
  }

  // ---------------------------------------------------------------------------
  // Project display
  // ---------------------------------------------------------------------------
  function displayProject(project) {
    var manifest = project.manifest || {};
    manifest.inputs = manifest.inputs || [];
    manifest.runs = manifest.runs || [];
    manifest.artifacts = manifest.artifacts || [];
    projectToken += 1;
    currentProjectPath = project.path;
    currentManifest = manifest;
    tasks = {};
    renderTasks();
    uiState = normalizeUiState(manifest.ui_state);
    analysisRestorePending = !!(uiState.analysis && uiState.analysis.route && uiState.analysis.route !== "landing");
    show("d-active-project", (manifest.name || basename(project.path)) + " · " + project.path);
    byId("d-task-project").textContent = project.path;
    renderSummary();
    byId("d-project-details").hidden = false;
    byId("d-open-path").value = project.path;
    rememberProject(project.path, manifest.name);
    syncProjectInputs(manifest.inputs);
    renderRunHistory(manifest.runs);
    if (loaded && uiState.analysis && window.Hyb2Web) window.Hyb2Web.restoreSession(uiState.analysis);
    refreshTaskList();
    activate(uiState.desktop.route, { persist: false });
  }
  function fillSelect(id, items, empty) {
    var select = byId(id);
    var previous = select.value;
    clear(select);
    if (!items.length) {
      var none = el("option", null, empty);
      none.value = "";
      select.appendChild(none);
    }
    items.forEach(function (item) {
      var option = el("option", null, (item.label ? item.label + " · " : "") + item.path);
      option.value = item.path;
      select.appendChild(option);
    });
    if (previous && items.some(function (item) { return item.path === previous; })) select.value = previous;
  }
  function renderPipelineInputs() {
    var inputs = (currentManifest && currentManifest.inputs) || [];
    var reads = inputs.filter(function (item) {
      return item.role === "fastq" || item.role === "sam" || item.role === "fasta" || /\.(fastq|fq)(\.gz)?$|\.sam$/i.test(item.path || "");
    });
    var references = inputs.filter(function (item) {
      return item.role === "reference" || item.role === "fasta" || /\.(fasta|fa)$/i.test(item.path || "");
    });
    fillSelect("d-pipeline-input", reads, "Import FASTQ, FASTA or SAM first");
    fillSelect("d-pipeline-reference", references, "Import a reference FASTA first");
  }
  function renderSummary() {
    renderPipelineInputs();
    var host = byId("d-project-summary");
    var manifest = currentManifest || {};
    clear(host);
    function row(label, value) {
      host.appendChild(el("dt", null, label));
      var dd = el("dd");
      if (value && value.nodeType) dd.appendChild(value);
      else dd.textContent = value === undefined || value === null || value === "" ? "—" : String(value);
      host.appendChild(dd);
    }
    row("Name", manifest.name);
    row("Folder", currentProjectPath);
    row("Description", manifest.description);
    row("Created", manifest.created_at ? formatTime(manifest.created_at) : "");
    row("Updated", manifest.updated_at ? formatTime(manifest.updated_at) : "");
    if (manifest.application && manifest.application.version) row("Created with", (manifest.application.name || "HYB2 Desktop") + " " + manifest.application.version);
    var inputs = manifest.inputs || [];
    if (inputs.length) {
      var list = el("ul", "d-summary-list");
      inputs.forEach(function (item) {
        var text = (item.role || "other").toUpperCase() + " · " + (item.label || basename(item.path)) + " (" + (item.path || "") + (item.size_bytes !== undefined ? ", " + formatBytes(item.size_bytes) : "") + ")";
        list.appendChild(el("li", null, text));
      });
      row("Inputs (" + inputs.length + ")", list);
    } else {
      row("Inputs", "None imported yet");
    }
    row("Runs", (manifest.runs || []).length);
    row("Artifacts", (manifest.artifacts || []).length);
  }
  function renderRunHistory(runs) {
    var host = byId("d-run-history-list");
    if (!host) return;
    clear(host);
    if (!runs.length) {
      host.appendChild(el("p", "d-muted", "No saved analysis runs."));
      return;
    }
    runs.slice().reverse().forEach(function (run) {
      var output = (run.output_paths && run.output_paths[0]) || "";
      var button = el("button", "d-run-item");
      button.type = "button";
      button.appendChild(el("strong", null, run.kind || "Structure analysis"));
      button.appendChild(el("span", null, run.id || ""));
      button.appendChild(el("small", null, (run.status || "unknown") + " · " + output));
      if (!output) button.disabled = true;
      button.addEventListener("click", async function () {
        try {
          var content = await readProjectText(output);
          var saved = JSON.parse(content);
          if (!loaded) await loadAnalysisWorkspace();
          if (window.Hyb2Web && window.Hyb2Web.showSavedStructure && window.Hyb2Web.showSavedStructure(saved)) {
            activate("analysis");
            show("d-run-result", "Restored " + (saved.algorithm || "saved structure") + " in the RNA Structure workspace.");
          } else {
            show("d-run-result", content);
          }
        } catch (error) { show("d-run-result", "Could not open saved result: " + errorText(error)); }
      });
      host.appendChild(button);
    });
  }

  // ---------------------------------------------------------------------------
  // Project files: asset protocol first, project_read_input as fallback (C4)
  // ---------------------------------------------------------------------------
  async function fetchProjectFile(relative) {
    var convert = tauri && tauri.core && tauri.core.convertFileSrc;
    if (typeof convert !== "function" || !currentProjectPath || !safeRelative(relative)) throw new Error("Asset protocol unavailable.");
    var response = await fetch(convert(joinProjectPath(currentProjectPath, relative)));
    if (!response.ok) throw new Error("Asset request failed with HTTP " + response.status);
    return response.blob();
  }
  async function readProjectText(relative) {
    try {
      var blob = await fetchProjectFile(relative);
      return await blob.text();
    } catch (_) {
      var result = await invoke("project_read_input", { request: { path: currentProjectPath, relative: relative } });
      if (!result || !result.input || typeof result.input.content !== "string") throw new Error("The project file could not be read.");
      return result.input.content;
    }
  }
  async function readProjectInput(item) {
    if (!invoke || !currentProjectPath || !item || !item.path) return null;
    var name = basename(item.path);
    try {
      var blob = await fetchProjectFile(item.path);
      return { name: name, path: item.path, file: new File([blob], name, { type: "text/plain" }) };
    } catch (_) {
      var result = await invoke("project_read_input", { request: { path: currentProjectPath, relative: item.path } });
      return result && result.input ? result.input : null;
    }
  }
  function inputSlot(item) {
    if (!item) return null;
    if (item.role === "hyb" || (item.role !== "fasta" && item.role !== "reference" && item.format === "hyb")) return "hyb";
    if (item.role === "fasta" || item.role === "reference" || item.format === "fasta") return "fasta";
    return null;
  }
  async function syncProjectInputs(inputs) {
    var token = projectToken;
    var selected = { hyb: null, fasta: null };
    (inputs || []).forEach(function (item) {
      var slot = inputSlot(item);
      if (slot) selected[slot] = item;
    });
    projectInputs = { hyb: null, fasta: null };
    try {
      var hyb = selected.hyb ? await readProjectInput(selected.hyb) : null;
      var fasta = selected.fasta ? await readProjectInput(selected.fasta) : null;
      if (token !== projectToken) return;
      projectInputs = { hyb: hyb, fasta: fasta };
      if (loaded && window.Hyb2Web && (hyb || fasta)) {
        window.Hyb2Web.loadProjectFiles(projectInputs);
        show("d-analysis-status", "Project inputs loaded into the integrated analysis workspace.");
      }
    } catch (error) {
      if (token === projectToken) show("d-analysis-status", "Project inputs could not be loaded: " + errorText(error));
    }
  }

  // ---------------------------------------------------------------------------
  // Project Home actions
  // ---------------------------------------------------------------------------
  async function browseInto(command, inputId, output) {
    if (!invoke) { show(output, "Folder dialogs are only available in the installed HYB2 Desktop application."); return; }
    try {
      var result = await invoke(command);
      if (result && typeof result.path === "string" && result.path) {
        byId(inputId).value = result.path;
        byId(inputId).focus();
      }
    } catch (error) {
      show(output, "Folder dialog failed: " + errorText(error));
    }
  }
  byId("d-open-browse").addEventListener("click", function () {
    browseInto("dialog_pick_project_folder", "d-open-path", "d-open-result");
  });
  byId("d-create-browse").addEventListener("click", function () {
    browseInto("dialog_pick_new_project_folder", "d-project-path", "d-project-result");
  });

  async function openProject(path, checkFiles) {
    var request = { path: String(path || "").trim(), check_files: !!checkFiles };
    if (!request.path) return;
    var result = await operation(byId("d-open-form"), "d-open-result", "Opening project", function () {
      return invoke("project_open", { request: request });
    });
    if (result) {
      show("d-open-result", "Project opened.");
      displayProject(result);
    }
  }
  byId("d-open-form").addEventListener("submit", async function (event) {
    event.preventDefault();
    var path = byId("d-open-path").value.trim();
    var checkFiles = byId("d-check-files").checked;
    if (event.submitter && event.submitter.value === "validate") {
      var result = await operation(event.currentTarget, "d-open-result", "Validating project", function () {
        return invoke("project_validate", { request: { path: path, check_files: checkFiles } });
      });
      if (result) show("d-open-result", "Project validation passed.");
      return;
    }
    openProject(path, checkFiles);
  });
  byId("d-create-form").addEventListener("submit", async function (event) {
    event.preventDefault();
    var request = { path: byId("d-project-path").value.trim(), name: byId("d-project-name").value.trim(), description: byId("d-project-description").value };
    var result = await operation(event.currentTarget, "d-project-result", "Creating project", function () {
      return invoke("project_create", { request: request });
    });
    if (result) {
      show("d-project-result", "Project created at " + request.path + ". Opening it…");
      byId("d-open-path").value = request.path;
      openProject(request.path, false);
    }
  });
  byId("d-import-button").addEventListener("click", async function () {
    if (!currentProjectPath) return;
    var path = currentProjectPath;
    var role = byId("d-input-role").value;
    var result = await operation(byId("d-import-form"), "d-import-result", "Importing input", function () {
      return invoke("project_import_input_dialog", { request: { path: path, role: role } });
    });
    if (!result) return;
    if (result.cancelled) { show("d-import-result", "Import cancelled."); return; }
    var imported = result.input;
    if (!imported) { show("d-import-result", "Import finished without an input record."); return; }
    show("d-import-result", "Imported " + imported.path + " (" + formatBytes(imported.size_bytes) + ")");
    if (path !== currentProjectPath || !currentManifest) return;
    currentManifest.inputs.push(imported);
    renderSummary();
    var slot = inputSlot(imported);
    if (slot) {
      var token = projectToken;
      try {
        var entry = await readProjectInput(imported);
        if (token !== projectToken) return;
        projectInputs[slot] = entry;
        if (loaded && window.Hyb2Web && (projectInputs.hyb || projectInputs.fasta)) window.Hyb2Web.loadProjectFiles(projectInputs);
      } catch (error) {
        show("d-import-result", "Imported, but the file could not be loaded into the workspace: " + errorText(error));
      }
    }
  });
  byId("d-project-analysis").addEventListener("click", function () { activate("analysis"); });

  // ---------------------------------------------------------------------------
  // Task Center: unified task API (contract C3)
  // ---------------------------------------------------------------------------
  function isActiveTask(task) {
    return !!task && ACTIVE_TASK_STATES.indexOf(task.status) >= 0;
  }
  function storeTask(snapshot) {
    if (!snapshot || !snapshot.task_id) return;
    tasks[snapshot.task_id] = Object.assign({}, tasks[snapshot.task_id] || {}, snapshot);
  }
  function taskOutputText(task) {
    if (task.error) return String(task.error);
    var output = task.output;
    if (output === undefined || output === null || output === "") {
      return task.run_dir ? String(task.run_dir) : "";
    }
    if (typeof output === "string") return output;
    if (typeof output === "object") {
      if (output.errors && output.errors.length) return output.errors.join("\n");
      if (output.error) return String(output.error);
      if (output.dotBracket || output.structure) {
        var structure = output.dotBracket || output.structure;
        return String(structure) + (output.energy !== undefined ? "  (" + output.energy + " kcal/mol)" : "");
      }
    }
    var text = JSON.stringify(output);
    return text.length > 400 ? text.slice(0, 400) + "…" : text;
  }
  function renderTasks() {
    var body = byId("d-task-rows");
    clear(body);
    var list = Object.keys(tasks).map(function (id) { return tasks[id]; });
    list.sort(function (a, b) { return String(b.started_at || "").localeCompare(String(a.started_at || "")); });
    byId("d-task-empty").hidden = list.length > 0;
    list.forEach(function (task) {
      var row = el("tr");
      row.appendChild(el("td", null, task.kind || "task"));
      var statusCell = el("td");
      statusCell.appendChild(el("span", "d-status d-status-" + String(task.status || "unknown").replace(/[^a-z]/g, ""), task.status || "unknown"));
      row.appendChild(statusCell);
      row.appendChild(el("td", null, formatTime(task.started_at)));
      row.appendChild(el("td", null, formatTime(task.finished_at)));
      var outputCell = el("td", "d-task-output");
      outputCell.appendChild(el("span", null, taskOutputText(task)));
      if (task.exit_code !== undefined && task.exit_code !== null && task.status !== "succeeded") {
        outputCell.appendChild(el("small", null, "exit code " + task.exit_code));
      }
      row.appendChild(outputCell);
      var actionCell = el("td");
      var hyb = task.status === "succeeded" && typeof task.output === "string" && /\.hyb$/i.test(task.output) ? projectRelative(task.output) : null;
      if (hyb) {
        var openButton = el("button", "d-btn d-btn-small", "Open in analysis");
        openButton.type = "button";
        openButton.addEventListener("click", function () { openPipelineResult(hyb, byId("d-pipeline-reference").value || null); });
        actionCell.appendChild(openButton);
      }
      if (isActiveTask(task)) {
        var cancel = el("button", "d-btn d-btn-small", "Cancel");
        cancel.type = "button";
        cancel.setAttribute("aria-label", "Cancel task " + task.task_id);
        cancel.addEventListener("click", function () { cancelTask(task.task_id, cancel); });
        actionCell.appendChild(cancel);
      }
      row.appendChild(actionCell);
      body.appendChild(row);
    });
    updateTaskPolling();
  }
  function projectRelative(absolute) {
    if (!currentProjectPath) return null;
    var root = currentProjectPath.replace(/[\\/]+$/, "");
    var value = String(absolute);
    if (value.indexOf(root) !== 0) return null;
    var rest = value.slice(root.length).replace(/^[\\/]+/, "").replace(/\\/g, "/");
    return rest && rest.split("/").indexOf("..") < 0 ? rest : null;
  }
  async function openPipelineResult(relative, referencePath) {
    try {
      var hyb = await readProjectInput({ path: relative });
      var reference = referencePath ? await readProjectInput({ path: referencePath }) : (projectInputs.fasta || null);
      projectInputs = { hyb: hyb, fasta: reference };
      if (!loaded) await loadAnalysisWorkspace();
      window.Hyb2Web.loadProjectFiles(projectInputs);
      activate("analysis");
      show("d-analysis-status", "Loaded pipeline result " + relative + ".");
    } catch (error) {
      show("d-task-state", "Could not open pipeline result: " + errorText(error));
    }
  }
  async function refreshTaskList() {
    if (!invoke || !currentProjectPath) return;
    var path = currentProjectPath;
    try {
      var result = await invoke("task_list", { request: { project_path: path } });
      if (path !== currentProjectPath) return;
      (result && result.tasks || []).forEach(storeTask);
      renderTasks();
    } catch (error) {
      show("d-task-state", "Could not list tasks: " + errorText(error));
    }
  }
  function updateTaskPolling() {
    var anyActive = Object.keys(tasks).some(function (id) { return isActiveTask(tasks[id]); });
    if (anyActive && !taskPollTimer) {
      taskPollTimer = setInterval(pollTasks, 1000);
    } else if (!anyActive && taskPollTimer) {
      clearInterval(taskPollTimer);
      taskPollTimer = null;
    }
  }
  async function pollTasks() {
    if (taskPollBusy || !invoke) return;
    taskPollBusy = true;
    var path = currentProjectPath;
    try {
      var active = Object.keys(tasks).filter(function (id) { return isActiveTask(tasks[id]); });
      for (var index = 0; index < active.length; index += 1) {
        try {
          var snapshot = await invoke("task_status", { request: { task_id: active[index], project_path: path } });
          if (path !== currentProjectPath) return;
          storeTask(snapshot);
        } catch (error) {
          show("d-task-state", "Task status failed: " + errorText(error));
        }
      }
      if (path === currentProjectPath) renderTasks();
    } finally {
      taskPollBusy = false;
    }
  }
  async function cancelTask(taskId, button) {
    if (!invoke) return;
    if (button) button.disabled = true;
    try {
      storeTask(await invoke("task_cancel", { request: { task_id: taskId } }));
      renderTasks();
    } catch (error) {
      show("d-task-state", "Cancel failed: " + errorText(error));
      if (button) button.disabled = false;
    }
  }
  async function startTask(form, label, request) {
    var result = await operation(form, "d-task-state", label, function () {
      if (!currentProjectPath) throw new Error("Open a project before starting a task.");
      request.project_path = currentProjectPath;
      return invoke("task_start", { request: request });
    });
    if (!result) return;
    if (result.status === "unavailable") {
      show("d-task-state", "Native " + request.kind + " unavailable: " + errorText(result.error || "portable runtime is not installed."));
      return;
    }
    storeTask(result);
    renderTasks();
    show("d-task-state", "Started " + (result.kind || request.kind) + " task " + (result.task_id || "") + ".");
  }
  byId("d-task-form").addEventListener("submit", function (event) {
    event.preventDefault();
    startTask(event.currentTarget, "Starting validation task", { kind: "validate_project" });
  });
  byId("d-pipeline-form").addEventListener("submit", function (event) {
    event.preventDefault();
    var input = byId("d-pipeline-input").value;
    var reference = byId("d-pipeline-reference").value;
    var threshold = Number(byId("d-pipeline-threshold").value);
    var overlap = Number(byId("d-pipeline-overlap").value);
    var problem = "";
    if (!input) problem = "Choose the reads or SAM file to process.";
    else if (!reference) problem = "Choose the reference FASTA used for mapping.";
    else if (!(threshold >= 0 && threshold <= 10)) problem = "BLAST threshold must be between 0 and 10.";
    else if (!Number.isInteger(overlap) || overlap < 0 || overlap > 1000) problem = "Maximum overlap must be an integer from 0 to 1000.";
    if (problem) { show("d-task-state", problem); return; }
    startTask(event.currentTarget, "Starting HYB2 pipeline", {
      kind: "pipeline", input: input, reference: reference,
      parameters: { blast_threshold: threshold, max_overlap: overlap, energy: byId("d-pipeline-energy").checked }
    });
  });
  byId("d-cplfold-form").addEventListener("submit", function (event) {
    event.preventDefault();
    var form = event.currentTarget;
    var sequence = byId("d-cplfold-sequence").value.replace(/\s+/g, "").toUpperCase();
    var beam = Number(byId("d-cplfold-beam").value || 20);
    var limit = window.Hyb2Desktop && window.Hyb2Desktop.limits && window.Hyb2Desktop.limits.cplfoldMaxLength;
    var problem = "";
    if (!/^[ACGUTN]+$/.test(sequence)) problem = "RNA sequence may contain only A, C, G, U, T or N.";
    else if (sequence.length < 2) problem = "RNA sequence must contain at least two bases.";
    else if (limit && sequence.length > limit) problem = "Native CPLfold accepts at most " + limit + " nt.";
    else if (!Number.isInteger(beam) || beam < 1 || beam > 200) problem = "Beam must be an integer from 1 to 200.";
    if (problem) { show("d-task-state", problem); return; }
    startTask(form, "Starting native CPLfold", { kind: "cplfold", sequence: sequence, parameters: { beam: beam } });
  });
  byId("d-task-refresh").addEventListener("click", function () { refreshTaskList(); });

  // ---------------------------------------------------------------------------
  // Runtime
  // ---------------------------------------------------------------------------
  byId("d-detect-runtime").addEventListener("click", async function () {
    var result = await operation(byId("d-view-runtime"), "d-runtime-result", "Detecting runtime", function () { return invoke("runtime_detect"); });
    if (result) show("d-runtime-result", result.result || result);
  });

  renderRecent();
  renderTasks();
})();
