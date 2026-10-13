(function () {
  "use strict";
  var invoke = window.__TAURI__ && window.__TAURI__.core && window.__TAURI__.core.invoke;
  var loaded = false;
  var loadingAnalysis = null;
  var activeTaskId = null;
  var pollTimer = null;
  var assets = window.HYB2_ASSET_STATUS;
  var currentProjectPath = null;
  var projectInputs = { hyb: null, fasta: null };
  var pendingUiState = null;
  var uiStateSaveTimer = null;
  if (assets) {
    var missing = [];
    if (!assets.vienna.assetsPresent) missing.push("ViennaRNA / COMRADES");
    if (!assets.cplfold.assetsPresent) missing.push("CPLfold (Pyodide)");
    document.querySelector(".analysis-note").textContent = missing.length
      ? "Missing folding assets: " + missing.join(", ") + ". File import, contact maps and interaction analysis remain available. Native CPLfold requires the installed runtime."
      : "Folding runtime files are bundled; engine startup is checked when used. Native CPLfold is available from Task Center when a portable runtime is installed.";
  }
  function show(id, value) {
    document.getElementById(id).textContent = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  }
  function errorText(error) {
    if (!error) return "Unknown error";
    if (typeof error === "string") return error;
    if (error.message) return error.message;
    if (error.error && typeof error.error === "string") return error.error;
    return JSON.stringify(error);
  }
  function taskText(status, prefix) {
    var label = prefix || "Task";
    var detail = status.error || status.output || status.run_dir || "";
    if (status.errors && status.errors.length) detail = status.errors.join("\n");
    return label + " " + (status.status || "unknown") + (detail ? "\n" + errorText(detail) : "");
  }
  function activate(view) {
    document.querySelectorAll(".nav").forEach(function (item) {
      item.classList.toggle("active", item.dataset.view === view);
      if (item.dataset.view === view) item.setAttribute("aria-current", "page");
      else item.removeAttribute("aria-current");
    });
    document.querySelectorAll(".view").forEach(function (item) { item.classList.toggle("active", item.id === view); });
    if (currentProjectPath && invoke) {
      pendingUiState = Object.assign({}, pendingUiState || {}, {route: view});
      clearTimeout(uiStateSaveTimer);
      uiStateSaveTimer = setTimeout(function () {
        invoke("project_update_ui_state", {request: {path: currentProjectPath, ui_state: pendingUiState}}).catch(function () {});
      }, 350);
    }
    if (view === "analysis" && !loaded) {
      loadAnalysisWorkspace();
    }
  }
  window.addEventListener("hyb2:session-change", function (event) {
    if (!currentProjectPath || !invoke || !event.detail) return;
    pendingUiState = event.detail;
    clearTimeout(uiStateSaveTimer);
    uiStateSaveTimer = setTimeout(function () {
      if (pendingUiState) invoke("project_update_ui_state", {request: {path: currentProjectPath, ui_state: pendingUiState}}).catch(function () {});
    }, 350);
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
    invoke("project_save_analysis_run", {request: {path: currentProjectPath, result: saved}}).then(function (response) {
      if (response && response.run) show("analysis-status", "Analysis result saved as " + response.run.run_id + ".");
    }).catch(function (error) { show("analysis-status", "Analysis completed but could not be saved: " + errorText(error)); });
  });
  document.querySelectorAll(".nav").forEach(function (button) {
    button.addEventListener("click", function () { activate(button.dataset.view); });
  });
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
  async function loadAnalysisWorkspace() {
    if (loadingAnalysis) return loadingAnalysis;
    loadingAnalysis = (async function () {
      show("analysis-status", "Loading integrated analysis workspace…");
      window.HYB2_ASSET_BASE = "./analysis/";
      ["styles.css", "workspace.css", "analysis.css", "structure.css", "report.css", "vendor/fornac/fornac.css", "responsive.css"].forEach(appendStyle);
      var host = document.getElementById("analysis-host");
      var appRoot = document.createElement("div");
      appRoot.id = "app";
      appRoot.className = "app-shell desktop-integrated-analysis";
      host.appendChild(appRoot);
      for (const file of ["vendor/d3/d3.v3.min.js", "vendor/fornac/fornac.js", "analysis-data.js", "comrades-analysis.js", "analysis-pages.js", "structure-controller.js", "structure-ui.js", "analysis-ui.js", "overview-page.js", "report-page.js", "app.js"]) {
        await appendScript(file);
      }
      loaded = true;
      if (projectInputs.hyb || projectInputs.fasta) window.Hyb2Web.loadProjectFiles(projectInputs);
      if (pendingUiState) window.Hyb2Web.restoreSession(pendingUiState);
      show("analysis-status", "Integrated analysis workspace ready.");
    })().catch(function (error) {
      show("analysis-status", "Analysis could not load: " + errorText(error));
      throw error;
    });
    return loadingAnalysis;
  }
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
      show(output, label + " failed: " + (error.message || String(error)));
    } finally {
      buttons.forEach(function (button) { button.disabled = false; });
      container.removeAttribute("aria-busy");
    }
  }
  function displayProject(project) {
    var manifest = project.manifest;
    currentProjectPath = project.path;
    pendingUiState = manifest.ui_state || null;
    show("active-project", manifest.name + " · " + project.path);
    show("project-summary", {
      name: manifest.name, path: project.path, description: manifest.description || "",
      inputs: manifest.inputs.length, runs: manifest.runs.length, artifacts: manifest.artifacts.length
    });
    document.getElementById("project-details").hidden = false;
    document.getElementById("open-path").value = project.path;
    document.getElementById("task-path").value = project.path;
    syncProjectInputs(manifest.inputs || []);
  }
  async function readProjectInput(item) {
    if (!invoke || !currentProjectPath || !item || !item.path) return null;
    var result = await invoke("project_read_input", {request: {path: currentProjectPath, relative: item.path}});
    return result && result.input ? result.input : null;
  }
  async function syncProjectInputs(inputs) {
    var selected = { hyb: null, fasta: null };
    for (const item of inputs || []) {
      if (item.role === "hyb" || item.format === "hyb") selected.hyb = item;
      if (item.role === "fasta" || item.role === "reference" || item.format === "fasta") selected.fasta = item;
    }
    try {
      projectInputs.hyb = selected.hyb ? await readProjectInput(selected.hyb) : null;
      projectInputs.fasta = selected.fasta ? await readProjectInput(selected.fasta) : null;
      if (window.Hyb2Web && (projectInputs.hyb || projectInputs.fasta)) {
        window.Hyb2Web.loadProjectFiles(projectInputs);
        show("analysis-status", "Project inputs loaded into the integrated analysis workspace.");
      }
    } catch (error) {
      show("analysis-status", "Project inputs could not be loaded: " + errorText(error));
    }
  }
  document.getElementById("import-form").addEventListener("submit", async function (event) {
    event.preventDefault();
    if (!currentProjectPath) return;
    var result = await operation(event.currentTarget, "import-result", "Importing input", async function () {
      return invoke("project_import_input", {request: {path: currentProjectPath, source: document.getElementById("input-source").value.trim(), role: document.getElementById("input-role").value}});
    });
    if (result) {
      show("import-result", "Imported " + result.input.path + " (" + result.input.size_bytes + " bytes)");
      var summary = document.getElementById("project-summary");
      try { var data = JSON.parse(summary.textContent); data.inputs += 1; summary.textContent = JSON.stringify(data, null, 2); } catch (_) {}
      var imported = result.input;
      if (imported && (imported.role === "hyb" || imported.role === "fasta" || imported.role === "reference")) {
        var inputs = [];
        try { inputs = JSON.parse(summary.textContent).inputs_manifest || []; } catch (_) {}
        if (imported.role === "hyb") projectInputs.hyb = await readProjectInput(imported);
        else projectInputs.fasta = await readProjectInput(imported);
        if (window.Hyb2Web && (projectInputs.hyb || projectInputs.fasta)) window.Hyb2Web.loadProjectFiles(projectInputs);
      }
    }
  });
  document.getElementById("project-analysis").addEventListener("click", function () { activate("analysis"); });
  document.querySelector('[data-view="tasks"]').addEventListener("click", function () {
    var current = document.getElementById("open-path").value.trim();
    if (current) document.getElementById("task-path").value = current;
  });
  document.getElementById("task-form").addEventListener("submit", async function (event) {
    event.preventDefault();
    var result = await operation(event.currentTarget, "task-state", "Running validation task", async function () {
      return invoke("task_run", {request: {path: document.getElementById("task-path").value.trim(), kind: "validate_project"}});
    });
    if (result) {
      var events = result.events || [];
      show("task-state", "Task " + result.task_id + " · " + result.status + "\n" + events.map(function (item) {
        var detail = item.line || item.error || (item.exitCode !== undefined ? "exit " + item.exitCode : "");
        return "[" + (item.type === "completed" ? "succeeded" : item.type) + "] " + detail;
      }).join("\n"));
    }
  });
  document.getElementById("cplfold-form").addEventListener("submit", async function (event) {
    event.preventDefault();
    var result = await operation(event.currentTarget, "task-state", "Running native CPLfold", async function () {
      var sequence = document.getElementById("cplfold-sequence").value.replace(/\s+/g, "").toUpperCase();
      if (!document.getElementById("task-path").value.trim()) throw new Error("Open a project before starting native CPLfold.");
      if (!/^[ACGUTN]+$/i.test(sequence)) throw new Error("RNA sequence may contain only A, C, G, U, T or N.");
      if (sequence.length < 2) throw new Error("RNA sequence must contain at least two bases.");
      var beam = Number(document.getElementById("cplfold-beam").value || 20);
      if (!Number.isInteger(beam) || beam < 1 || beam > 200) throw new Error("Beam must be an integer from 1 to 200.");
      var result = await invoke("task_start", {request: {project_path: document.getElementById("task-path").value.trim(), command: ["cplfold"], sequence: sequence, parameters: {beam: beam}}});
      activeTaskId = result.task_id || null;
      document.getElementById("cancel-task").disabled = !activeTaskId;
      if (pollTimer) clearInterval(pollTimer);
      pollTimer = setInterval(async function () {
        if (!activeTaskId) return;
        try { var status = await invoke("task_status", {request: {task_id: activeTaskId}}); show("task-state", taskText(status, "CPLfold")); if (["succeeded","failed","cancelled"].indexOf(status.status) >= 0) { clearInterval(pollTimer); pollTimer = null; activeTaskId = null; document.getElementById("cancel-task").disabled = true; } } catch (error) { show("task-state", "CPLfold status failed: " + errorText(error)); }
      }, 500);
      return result;
    });
    if (result) {
      if (result.status === "unavailable") {
        show("task-state", "Native CPLfold unavailable: " + errorText(result.error || "portable runtime is not installed."));
      } else {
        show("task-state", taskText(result, "CPLfold"));
      }
    }
  });
  document.getElementById("cancel-task").addEventListener("click", async function () {
    if (!activeTaskId || !invoke) return;
    try { var result = await invoke("task_cancel", {request: {task_id: activeTaskId}}); show("task-state", taskText(result, "CPLfold")); } catch (error) { show("task-state", "Cancel failed: " + errorText(error)); }
    activeTaskId = null; this.disabled = true; if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  });
  document.getElementById("create-form").addEventListener("submit", async function (event) {
    event.preventDefault();
    var request = {path: document.getElementById("project-path").value.trim(), name: document.getElementById("project-name").value.trim(), description: document.getElementById("project-description").value};
    var result = await operation(event.currentTarget, "project-result", "Creating project", async function () {
      return invoke("project_create", {request: request});
    });
    if (result) {
      show("project-result", "Project created: " + result.manifest);
      document.getElementById("open-path").value = request.path;
    }
  });
  document.getElementById("open-form").addEventListener("submit", async function (event) {
    event.preventDefault();
    var command = event.submitter && event.submitter.value === "validate" ? "project_validate" : "project_open";
    var request = {path: document.getElementById("open-path").value.trim(), check_files: document.getElementById("check-files").checked};
    var result = await operation(event.currentTarget, "open-result", command === "project_open" ? "Opening project" : "Validating project", async function () {
      return invoke(command, {request: request});
    });
    if (result) {
      show("open-result", command === "project_open" ? "Project opened." : "Project validation passed.");
      if (command === "project_open") displayProject(result);
    }
  });
  document.getElementById("detect-runtime").addEventListener("click", async function () {
    var result = await operation(document.getElementById("runtime"), "runtime-result", "Detecting runtime", async function () { return invoke("runtime_detect"); });
    if (result) show("runtime-result", result.result || result);
  });
})();
