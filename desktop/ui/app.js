/*
 * HYB2 Desktop shell: start screen, project switcher, top bar (reference,
 * gene, selection, task indicator), the single navigation
 * (① Data … ⑥ Report), the task drawer, Preferences and Help.
 */
(function () {
  "use strict";

  var HD = window.HD;
  var h = HD.h;
  var state = HD.state;
  var PAGES = ["data", "qc", "explore", "compare", "structure", "report"];
  var RECENT_KEY = "hyb2.desktop.recentProjects";
  var ACTIVE = ["queued", "running"];
  var RESUMABLE = ["interrupted", "failed", "cancelled"];
  var pollTimer = null;
  var pollBusy = false;
  var lastProjectRefresh = 0;
  var rates = Object.create(null);
  var taskLabels = Object.create(null);
  var rendered = Object.create(null);
  var sessionStart = HD.sessionStart = new Date().toISOString();

  // ---------------------------------------------------------------- recent projects

  function readRecent() {
    try {
      var list = JSON.parse(window.localStorage.getItem(RECENT_KEY) || "[]");
      return Array.isArray(list) ? list.filter(function (item) { return item && typeof item.path === "string" && item.path; }).slice(0, 8) : [];
    } catch (_) { return []; }
  }
  function writeRecent(list) {
    try { window.localStorage.setItem(RECENT_KEY, JSON.stringify(list.slice(0, 8))); } catch (_) {}
  }
  function rememberProject(path, name) {
    var list = readRecent().filter(function (item) { return item.path !== path; });
    list.unshift({ path: path, name: String(name || HD.basename(path)), opened_at: new Date().toISOString() });
    writeRecent(list);
  }
  function renderRecent() {
    var host = HD.byId("d-recent-list");
    var list = readRecent();
    HD.clear(host);
    HD.byId("d-recent-empty").hidden = list.length > 0;
    list.forEach(function (item) {
      host.appendChild(h("li", { className: "d-recent-item" }, [
        h("button", { type: "button", className: "d-recent-open", title: "Open " + item.path, on: { click: function () { openProject(item.path); } } }, [
          h("strong", { text: item.name || HD.basename(item.path) }),
          h("span", { text: item.path })
        ]),
        h("button", { type: "button", className: "d-btn d-btn-small d-btn-ghost", text: "Forget", "aria-label": "Forget " + item.path, on: { click: function () {
          writeRecent(readRecent().filter(function (entry) { return entry.path !== item.path; }));
          renderRecent();
        } } })
      ]));
    });
  }

  // ---------------------------------------------------------------- start screen / projects

  function setStartStatus(text) { HD.byId("d-start-status").textContent = text || ""; }

  function showStart() {
    stopPolling();
    state.projectPath = null;
    state.manifest = null;
    HD.byId("d-shell").hidden = true;
    HD.byId("d-start").hidden = false;
    document.title = "HYB2 Desktop";
    renderRecent();
    HD.emit("project-closed");
  }

  async function openProject(path) {
    var target = String(path || "").trim();
    if (!target) return;
    setStartStatus("Opening " + target + "…");
    try {
      var opened = await HD.invoke("project_open", { request: { path: target, check_files: false } });
      await displayProject(opened);
      setStartStatus("");
    } catch (error) {
      setStartStatus("Could not open the project: " + HD.errorText(error));
      HD.toast("Could not open the project: " + HD.errorText(error), "error");
    }
  }

  async function displayProject(opened) {
    var manifest = opened.manifest || {};
    state.projectPath = opened.path;
    state.manifest = manifest;
    state.tasks = Object.create(null);
    state.stats = Object.create(null);
    state.structures = [];
    rendered = Object.create(null);
    HD.restoreUiState(manifest.ui_state);
    HD.applyProjectData({ samples: manifest.samples || [], references: manifest.references || [] });
    rememberProject(opened.path, manifest.name);
    HD.byId("d-project-name").textContent = manifest.name || HD.basename(opened.path);
    document.title = (manifest.name || HD.basename(opened.path)) + " · HYB2 Desktop";
    HD.byId("d-start").hidden = true;
    HD.byId("d-shell").hidden = false;
    HD.emit("project-opened");
    renderTopbar();
    activate(state.page, { persist: false });
    refreshTasks();
    HD.refreshProject().then(function () { return HD.loadStats(); }).catch(function () {});
  }

  HD.byId("d-open-form").addEventListener("submit", function (event) {
    event.preventDefault();
    openProject(HD.byId("d-open-path").value);
  });
  HD.byId("d-create-form").addEventListener("submit", async function (event) {
    event.preventDefault();
    var form = event.currentTarget;
    var request = { path: HD.byId("d-create-path").value.trim(), name: HD.byId("d-create-name").value.trim(), description: "" };
    await HD.busy(form, async function () {
      try {
        setStartStatus("Creating project…");
        await HD.invoke("project_create", { request: request });
        await openProject(request.path);
      } catch (error) {
        setStartStatus("Could not create the project: " + HD.errorText(error));
      }
    });
  });
  async function browse(command, inputId) {
    try {
      var result = await HD.invoke(command);
      if (result && result.path) {
        HD.byId(inputId).value = result.path;
        return result.path;
      }
    } catch (error) {
      setStartStatus("Folder dialog failed: " + HD.errorText(error));
    }
    return null;
  }
  HD.byId("d-open-browse").addEventListener("click", async function () {
    var path = await browse("dialog_pick_project_folder", "d-open-path");
    if (path) openProject(path);
  });
  HD.byId("d-create-browse").addEventListener("click", function () { browse("dialog_pick_new_project_folder", "d-create-path"); });

  // ---------------------------------------------------------------- menus

  function toggleMenu(button, menu, fill) {
    var open = menu.hidden;
    closeMenus();
    if (!open) return;
    if (fill) fill(menu);
    menu.hidden = false;
    button.setAttribute("aria-expanded", "true");
    var first = menu.querySelector("button");
    if (first) first.focus();
  }
  function closeMenus() {
    document.querySelectorAll(".d-menu").forEach(function (menu) { menu.hidden = true; });
    document.querySelectorAll("[aria-haspopup=menu]").forEach(function (button) { button.setAttribute("aria-expanded", "false"); });
  }
  document.addEventListener("click", function (event) {
    if (!event.target.closest(".d-menu-anchor")) closeMenus();
  });
  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape") closeMenus();
  });

  HD.byId("d-project-button").addEventListener("click", function () {
    toggleMenu(this, HD.byId("d-project-menu"), function (menu) {
      HD.clear(menu);
      menu.appendChild(h("div", { className: "d-menu-label", text: state.projectPath }));
      readRecent().filter(function (item) { return item.path !== state.projectPath; }).slice(0, 5).forEach(function (item) {
        menu.appendChild(h("button", { type: "button", className: "d-menu-item", role: "menuitem", title: item.path, text: item.name, on: { click: function () { closeMenus(); openProject(item.path); } } }));
      });
      menu.appendChild(h("hr", { className: "d-menu-sep" }));
      menu.appendChild(h("button", { type: "button", className: "d-menu-item", role: "menuitem", text: "Open project…", on: { click: async function () {
        closeMenus();
        var result = await HD.invoke("dialog_pick_project_folder").catch(function () { return null; });
        if (result && result.path) openProject(result.path);
      } } }));
      menu.appendChild(h("button", { type: "button", className: "d-menu-item", role: "menuitem", text: "New project…", on: { click: function () { closeMenus(); showStart(); HD.byId("d-create-name").focus(); } } }));
      menu.appendChild(h("button", { type: "button", className: "d-menu-item", role: "menuitem", text: "Validate project files", on: { click: function () { closeMenus(); validateProject(); } } }));
      menu.appendChild(h("button", { type: "button", className: "d-menu-item", role: "menuitem", text: "Close project", on: { click: function () { closeMenus(); showStart(); } } }));
    });
  });
  async function validateProject() {
    try {
      await HD.invoke("project_validate", { request: { path: state.projectPath, check_files: true } });
      HD.toast("Project manifest and referenced files are valid.");
    } catch (error) {
      HD.toast("Validation failed: " + HD.errorText(error), "error");
    }
  }

  // ---------------------------------------------------------------- top bar

  function renderTopbar() {
    var refSelect = HD.byId("d-reference");
    HD.clear(refSelect);
    if (!state.references.length) {
      refSelect.appendChild(h("option", { value: "", text: "No reference" }));
      refSelect.disabled = true;
    } else {
      refSelect.disabled = false;
      state.references.forEach(function (reference) {
        refSelect.appendChild(h("option", { value: reference.id, text: reference.label || reference.id }));
      });
      refSelect.value = state.referenceId || state.references[0].id;
    }
    var geneSelect = HD.byId("d-gene");
    HD.clear(geneSelect);
    var genes = HD.genes();
    if (!genes.length) {
      geneSelect.appendChild(h("option", { value: "", text: "—" }));
      geneSelect.disabled = true;
    } else {
      geneSelect.disabled = false;
      genes.forEach(function (gene) {
        geneSelect.appendChild(h("option", { value: gene.name, text: gene.name + " (" + HD.formatNumber(gene.length) + " nt)" }));
      });
      geneSelect.value = state.gene || genes[0].name;
    }
    geneSelect.title = state.gene || "";
    var key = HD.fillSelectionSelect(HD.byId("d-selection"), HD.selectionKey(state.selection), "No analysed samples yet");
    if (key && key !== HD.selectionKey(state.selection)) state.selection = HD.parseSelectionKey(key);
  }
  HD.byId("d-reference").addEventListener("change", function () {
    state.referenceId = this.value || null;
    var genes = HD.genes();
    state.gene = genes.length ? genes[0].name : null;
    renderTopbar();
    HD.saveUiState();
    HD.emit("gene");
  });
  HD.byId("d-gene").addEventListener("change", function () {
    state.gene = this.value || null;
    this.title = state.gene || "";
    HD.saveUiState();
    HD.emit("gene");
  });
  HD.byId("d-selection").addEventListener("change", function () {
    HD.setSelection(HD.parseSelectionKey(this.value));
  });
  HD.setSelection = function (selection) {
    state.selection = selection;
    HD.byId("d-selection").value = HD.selectionKey(selection);
    HD.saveUiState();
    HD.emit("selection");
  };
  HD.on("project", function () {
    renderTopbar();
  });

  // ---------------------------------------------------------------- navigation

  function activate(page, options) {
    if (PAGES.indexOf(page) < 0) page = "data";
    var changed = state.page !== page;
    state.page = page;
    document.querySelectorAll(".d-nav-item").forEach(function (item) {
      var active = item.dataset.page === page;
      item.classList.toggle("d-nav-active", active);
      if (active) item.setAttribute("aria-current", "page");
      else item.removeAttribute("aria-current");
    });
    document.querySelectorAll(".d-page").forEach(function (section) {
      section.hidden = section.dataset.page !== page;
    });
    var module = HD.pages[page];
    var container = HD.byId("d-page-" + page);
    if (module) {
      if (!rendered[page]) {
        rendered[page] = true;
        HD.clear(container);
        module.render(container);
      }
      if (module.onShow) module.onShow(options || {});
    }
    if (changed) window.scrollTo(0, 0);
    if (changed && !(options && options.persist === false)) HD.saveUiState();
  }
  document.querySelectorAll(".d-nav-item").forEach(function (button) {
    button.addEventListener("click", function () { activate(button.dataset.page); });
  });
  HD.on("navigate", function (detail) { activate(detail.page, detail.options); });

  // ---------------------------------------------------------------- tasks

  HD.taskLabel = function (task) {
    if (taskLabels[task.task_id]) return taskLabels[task.task_id];
    var label = null;
    state.samples.forEach(function (sample) {
      Object.keys(sample.stages || {}).forEach(function (stage) {
        if (sample.stages[stage] && sample.stages[stage].task_id === task.task_id) label = sample.name;
      });
    });
    var structure = (state.structures || []).find(function (item) { return item.id === task.task_id || item.task_id === task.task_id; });
    if (structure) {
      var evidence = (structure.sample_ids || []).map(function (id) { var sample = HD.sample(id); return sample ? sample.name : id; }).join(" + ");
      return "Structure · " + evidence + " · " + (structure.mode === "genome" ? "whole RNA" : (structure.windows || []).length + " window(s)");
    }
    var kinds = { sample_pipeline: "Pipeline", analysis_index: "Index", structure: "Structure", cplfold: "CPLfold", validate_project: "Validation", pipeline: "Pipeline" };
    return (kinds[task.kind] || task.kind || "Task") + (label ? " · " + label : "");
  };
  HD.setTaskLabel = function (taskId, label) { taskLabels[taskId] = label; };

  function storeTask(snapshot) {
    if (!snapshot || !snapshot.task_id) return null;
    var previous = state.tasks[snapshot.task_id];
    var next = Object.assign({}, previous || {}, snapshot);
    state.tasks[snapshot.task_id] = next;
    trackRate(next);
    return { previous: previous, next: next };
  }
  function trackRate(task) {
    var progress = task.progress || {};
    var done = Number(progress.bytes_done);
    var total = Number(progress.bytes_total);
    if (!isFinite(done) || !isFinite(total) || !total) return;
    var now = Date.now() / 1000;
    var rate = rates[task.task_id];
    if (!rate || rate.stage !== progress.stage || done < rate.last) {
      rates[task.task_id] = { stage: progress.stage, t0: now, d0: done, last: done, eta: null };
      return;
    }
    rate.last = done;
    var elapsed = now - rate.t0;
    if (elapsed > 2 && done > rate.d0) {
      var perSecond = (done - rate.d0) / elapsed;
      rate.eta = (total - done) / perSecond;
    }
  }
  HD.taskProgress = function (task) {
    var progress = task && task.progress || {};
    var done = Number(progress.bytes_done);
    var total = Number(progress.bytes_total);
    var fraction = isFinite(done) && isFinite(total) && total > 0 ? Math.min(1, done / total) : null;
    var rate = rates[task && task.task_id];
    return {
      fraction: fraction,
      stage: progress.stage || null,
      detail: progress.detail || "",
      units: progress.units || "bytes",
      done: isFinite(done) ? done : null,
      total: isFinite(total) ? total : null,
      eta: rate && rate.eta !== null && isFinite(rate.eta) ? rate.eta : null
    };
  };
  HD.isActiveTask = function (task) { return !!task && ACTIVE.indexOf(task.status) >= 0; };
  HD.activeTasks = function () {
    return Object.keys(state.tasks).map(function (id) { return state.tasks[id]; }).filter(HD.isActiveTask);
  };
  // Latest task for a sample stage (by the manifest's task_id).
  HD.sampleTask = function (sample) {
    var best = null;
    Object.keys(sample.stages || {}).forEach(function (stage) {
      var id = sample.stages[stage] && sample.stages[stage].task_id;
      var task = id && state.tasks[id];
      if (task && (!best || String(task.started_at || "") > String(best.started_at || ""))) best = task;
    });
    return best;
  };

  HD.progressBar = function (task) {
    var info = HD.taskProgress(task);
    var percent = info.fraction === null ? null : Math.round(info.fraction * 100);
    var bar = h("div", { className: "d-progress" + (percent === null ? " d-progress-indeterminate" : ""), role: "progressbar", "aria-valuemin": "0", "aria-valuemax": "100", "aria-valuenow": percent === null ? null : String(percent) }, [
      h("span", { style: "width:" + (percent === null ? 35 : percent) + "%" })
    ]);
    var parts = [];
    if (info.stage) parts.push(info.stage);
    if (percent !== null) parts.push(percent + "%");
    if (!info.total) { /* no measurable total for this stage */ }
    else if (info.done !== null && info.units === "bytes") parts.push(HD.formatBytes(info.done) + " / " + HD.formatBytes(info.total));
    else if (info.done !== null) parts.push(info.done + " / " + info.total + (info.units && info.units !== "none" ? " " + info.units : ""));
    if (info.eta !== null) parts.push("about " + HD.formatDuration(info.eta) + " left");
    return h("div", { className: "d-progress-wrap" }, [bar, h("small", { className: "d-muted", text: parts.join(" · ") || (task.status === "queued" ? "waiting for CPU / memory budget" : "starting…") })]);
  };

  function renderIndicator() {
    var active = HD.activeTasks();
    var all = Object.keys(state.tasks).map(function (id) { return state.tasks[id]; });
    var interrupted = all.filter(function (task) { return task.status === "interrupted"; }).length;
    // Failures are flagged only for tasks that ended during this session.
    var failed = all.filter(function (task) { return task.status === "failed" && String(task.finished_at || "") >= sessionStart; }).length;
    var text;
    var fraction = null;
    if (active.length) {
      var done = 0;
      var total = 0;
      active.forEach(function (task) {
        var info = HD.taskProgress(task);
        if (info.fraction !== null) { done += info.fraction; total += 1; }
      });
      fraction = total ? done / total : null;
      text = active.length + (active.length === 1 ? " task" : " tasks") + (fraction !== null ? " · " + Math.round(fraction * 100) + "%" : " running");
    } else if (interrupted) {
      text = interrupted + " interrupted";
    } else if (failed) {
      text = failed + " failed";
    } else {
      text = all.length ? "Tasks idle" : "No tasks";
    }
    var button = HD.byId("d-task-indicator");
    button.classList.toggle("d-task-indicator-active", active.length > 0);
    button.classList.toggle("d-task-indicator-warn", !active.length && (interrupted + failed) > 0);
    HD.byId("d-task-indicator-text").textContent = text;
    var bar = HD.byId("d-task-indicator-bar");
    bar.parentNode.hidden = !active.length;
    bar.style.width = (fraction === null ? 30 : Math.round(fraction * 100)) + "%";
    bar.parentNode.classList.toggle("d-mini-bar-indeterminate", fraction === null);
  }

  function renderDrawer() {
    var body = HD.byId("d-drawer-body");
    if (HD.byId("d-drawer").hidden) return;
    HD.clear(body);
    var list = Object.keys(state.tasks).map(function (id) { return state.tasks[id]; });
    list.sort(function (a, b) {
      var activeOrder = (HD.isActiveTask(b) ? 1 : 0) - (HD.isActiveTask(a) ? 1 : 0);
      return activeOrder || String(b.started_at || "").localeCompare(String(a.started_at || ""));
    });
    if (!list.length) {
      body.appendChild(h("p", { className: "d-muted", text: "No tasks in this project yet. Runs started from Data, Compare and Structure appear here." }));
      return;
    }
    var resumable = list.filter(function (task) { return task.status === "interrupted"; });
    if (resumable.length > 1) {
      body.appendChild(h("button", { type: "button", className: "d-btn d-btn-primary d-btn-block", text: "Resume all interrupted (" + resumable.length + ")", on: { click: function () {
        resumable.forEach(function (task) { resume(task.task_id); });
      } } }));
    }
    list.slice(0, 60).forEach(function (task) {
      var item = h("div", { className: "d-task" + (HD.isActiveTask(task) ? " d-task-active" : "") });
      item.appendChild(h("div", { className: "d-task-head" }, [
        h("strong", { text: HD.taskLabel(task) }),
        HD.chip(task.status)
      ]));
      if (HD.isActiveTask(task)) item.appendChild(HD.progressBar(task));
      var meta = [HD.formatTime(task.started_at)];
      if (task.finished_at) meta.push("finished " + HD.formatTime(task.finished_at));
      if (task.resumed_from) meta.push("resumed from " + task.resumed_from);
      item.appendChild(h("small", { className: "d-muted d-task-meta", text: meta.join(" · ") }));
      if (task.error && !HD.isActiveTask(task)) item.appendChild(h("p", { className: "d-task-error", text: String(task.error).slice(0, 600) }));
      var actions = h("div", { className: "d-task-actions" });
      if (HD.isActiveTask(task)) {
        actions.appendChild(h("button", { type: "button", className: "d-btn d-btn-small", text: "Cancel", on: { click: function () { cancel(task.task_id); } } }));
      }
      if (RESUMABLE.indexOf(task.status) >= 0 && task.kind !== "validate_project") {
        actions.appendChild(h("button", { type: "button", className: "d-btn d-btn-small", text: "Resume", on: { click: function () { resume(task.task_id); } } }));
      }
      if (task.status === "succeeded" && task.kind === "structure") {
        actions.appendChild(h("button", { type: "button", className: "d-btn d-btn-small", text: "View structure", on: { click: function () {
          closeDrawer();
          HD.navigate("structure", { structureId: task.task_id });
        } } }));
      }
      if (actions.firstChild) item.appendChild(actions);
      body.appendChild(item);
    });
  }
  function openDrawer() {
    HD.byId("d-drawer").hidden = false;
    HD.byId("d-task-indicator").setAttribute("aria-expanded", "true");
    renderDrawer();
  }
  function closeDrawer() {
    HD.byId("d-drawer").hidden = true;
    HD.byId("d-task-indicator").setAttribute("aria-expanded", "false");
  }
  HD.openTasks = openDrawer;
  HD.byId("d-task-indicator").addEventListener("click", function () {
    if (HD.byId("d-drawer").hidden) openDrawer(); else closeDrawer();
  });
  HD.byId("d-drawer-close").addEventListener("click", closeDrawer);

  async function cancel(taskId) {
    try { await HD.cancelTask(taskId); } catch (error) { HD.toast("Cancel failed: " + HD.errorText(error), "error"); }
  }
  async function resume(taskId) {
    try {
      var label = HD.taskLabel(state.tasks[taskId] || { task_id: taskId });
      var snapshot = await HD.resumeTask(taskId);
      if (snapshot && snapshot.task_id) HD.setTaskLabel(snapshot.task_id, label);
      HD.toast("Resumed " + label + ".");
    } catch (error) {
      HD.toast("Resume failed: " + HD.errorText(error), "error");
    }
  }
  HD.resume = resume;

  function tasksChanged(transitions) {
    renderIndicator();
    renderDrawer();
    HD.emit("tasks", transitions || []);
    var finished = (transitions || []).filter(function (item) {
      return item.previous && HD.isActiveTask(item.previous) && !HD.isActiveTask(item.next);
    });
    var running = HD.activeTasks().some(function (task) { return task.kind === "sample_pipeline" || task.kind === "analysis_index"; });
    if (finished.length || (running && Date.now() - lastProjectRefresh > 4000)) {
      lastProjectRefresh = Date.now();
      HD.refreshProject().then(function () {
        if (finished.some(function (item) { return item.next.kind === "sample_pipeline" || item.next.kind === "analysis_index"; })) return HD.loadStats();
      }).catch(function () {});
    }
    finished.forEach(function (item) {
      var label = HD.taskLabel(item.next);
      if (item.next.status === "succeeded") HD.toast(label + " finished.");
      else if (item.next.status === "failed") HD.toast(label + " failed: " + String(item.next.error || "see Tasks").slice(0, 200), "error");
      if (item.next.kind === "structure") HD.emit("structures");
    });
    updatePolling();
  }

  async function refreshTasks() {
    if (!state.projectPath) return;
    var path = state.projectPath;
    try {
      var result = await HD.invoke("task_list", { request: { project_path: path } });
      if (path !== state.projectPath) return;
      var transitions = (result && result.tasks || []).map(storeTask).filter(Boolean);
      tasksChanged(transitions);
    } catch (error) {
      HD.toast("Could not list tasks: " + HD.errorText(error), "error");
    }
  }
  HD.refreshTasks = refreshTasks;
  HD.on("task", function (snapshot) {
    var transition = storeTask(snapshot);
    tasksChanged(transition ? [transition] : []);
  });

  function updatePolling() {
    var active = HD.activeTasks().length > 0;
    if (active && !pollTimer) pollTimer = setInterval(poll, 1500);
    else if (!active && pollTimer) stopPolling();
  }
  function stopPolling() {
    if (pollTimer) clearInterval(pollTimer);
    pollTimer = null;
  }
  async function poll() {
    if (pollBusy || !state.projectPath) return;
    pollBusy = true;
    var path = state.projectPath;
    try {
      var transitions = [];
      var active = HD.activeTasks();
      for (var index = 0; index < active.length; index += 1) {
        try {
          var snapshot = await HD.invoke("task_status", { request: { task_id: active[index].task_id, project_path: path } });
          if (path !== state.projectPath) return;
          var transition = storeTask(snapshot);
          if (transition) transitions.push(transition);
        } catch (error) { /* transient; next poll retries */ }
      }
      tasksChanged(transitions);
    } finally {
      pollBusy = false;
    }
  }

  // ---------------------------------------------------------------- preferences

  function openPreferences() {
    HD.modal("Preferences", function (body) {
      var budgetSection = h("section", { className: "d-section" }, [h("h3", { className: "d-h3", text: "Resources" }), h("p", { className: "d-muted", text: "Loading…" })]);
      var themeSection = h("section", { className: "d-section" }, [h("h3", { className: "d-h3", text: "Appearance" })]);
      var runtimeSection = h("section", { className: "d-section" }, [h("h3", { className: "d-h3", text: "Runtime environment" })]);
      body.appendChild(budgetSection);
      body.appendChild(themeSection);
      body.appendChild(runtimeSection);

      var themeSelect = h("select", { className: "d-select", id: "d-pref-theme" }, [
        h("option", { value: "system", text: "Follow system" }),
        h("option", { value: "light", text: "Light" }),
        h("option", { value: "dark", text: "Dark" })
      ]);
      themeSelect.value = window.Hyb2Theme ? window.Hyb2Theme.get() : "system";
      themeSelect.addEventListener("change", function () { if (window.Hyb2Theme) window.Hyb2Theme.set(themeSelect.value); HD.emit("theme"); });
      themeSection.appendChild(h("label", { className: "d-label", for: "d-pref-theme", text: "Colour theme" }));
      themeSection.appendChild(themeSelect);

      function renderBudget(prefs) {
        HD.clear(budgetSection);
        budgetSection.appendChild(h("h3", { className: "d-h3", text: "Resources" }));
        var system = prefs.system || {};
        var budget = prefs.budget || {};
        budgetSection.appendChild(h("p", { className: "d-muted", text: "Tasks claim CPU cores and memory from this budget; a task that does not fit waits in the queue. This computer: " + (system.cpus || "?") + " cores, " + HD.formatBytes((system.mem_mb || 0) * 1048576) + " memory." }));
        var cpu = h("input", { className: "d-input d-input-narrow", type: "number", min: "1", max: String(system.cpus || 256), step: "1", id: "d-pref-cpu", value: String(budget.cpu || "") });
        var mem = h("input", { className: "d-input d-input-narrow", type: "number", min: "1", max: String(Math.ceil((system.mem_mb || 1048576) / 1024)), step: "0.5", id: "d-pref-mem", value: budget.mem_mb ? String(Math.round(budget.mem_mb / 102.4) / 10) : "" });
        budgetSection.appendChild(h("div", { className: "d-form-grid" }, [
          h("label", { className: "d-label", for: "d-pref-cpu", text: "CPU cores" }), cpu,
          h("label", { className: "d-label", for: "d-pref-mem", text: "Memory (GB)" }), mem
        ]));
        var status = h("p", { className: "d-status-line", role: "status" , text: prefs.custom_budget ? "Custom budget." : "Default budget (all cores, 75% of memory)." });
        budgetSection.appendChild(h("div", { className: "d-actions" }, [
          h("button", { type: "button", className: "d-btn", text: "Reset to defaults", on: { click: function () { save(null); } } }),
          h("button", { type: "button", className: "d-btn d-btn-primary", text: "Save budget", on: { click: function () {
            var cores = Math.floor(Number(cpu.value));
            var gb = Number(mem.value);
            if (!(cores >= 1) || !(gb > 0)) { status.textContent = "Enter at least 1 core and a positive memory size."; return; }
            save({ cpu: cores, mem_mb: Math.round(gb * 1024) });
          } } })
        ]));
        budgetSection.appendChild(status);
        function save(next) {
          HD.invoke("preferences_set", { request: { budget: next } }).then(function (result) {
            renderBudget(result);
            HD.state.preferences = result;
            HD.toast("Resource budget saved.");
          }).catch(function (error) { status.textContent = "Could not save: " + HD.errorText(error); });
        }
      }
      HD.invoke("preferences_get").then(function (prefs) { HD.state.preferences = prefs; renderBudget(prefs); }).catch(function (error) {
        HD.clear(budgetSection);
        budgetSection.appendChild(h("h3", { className: "d-h3", text: "Resources" }));
        budgetSection.appendChild(h("p", { className: "d-muted", text: "Preferences are unavailable: " + HD.errorText(error) }));
      });

      var runtimeOut = h("div", { className: "d-runtime" }, [h("p", { className: "d-muted", text: "Checking bundled components…" })]);
      runtimeSection.appendChild(runtimeOut);
      HD.invoke("runtime_detect").then(function (result) { renderRuntime(runtimeOut, result && result.result || result); }).catch(function (error) {
        HD.clear(runtimeOut);
        runtimeOut.appendChild(h("p", { className: "d-task-error", text: "Runtime detection failed: " + HD.errorText(error) }));
      });
    }, { wide: true });
  }
  function renderRuntime(host, info) {
    HD.clear(host);
    // Detector output: { platform, probes: [{ id, status, version, summary, ... }] }.
    var rows = (info && Array.isArray(info.probes) ? info.probes : []).map(function (probe) {
      return { name: probe.id, ok: probe.status === "available", status: probe.status, detail: [probe.version, probe.summary].filter(Boolean).join(" · ") };
    });
    if (info && info.platform) host.appendChild(h("p", { className: "d-muted", text: [info.platform.os, info.platform.architecture, info.platform.python ? "Python " + info.platform.python : null].filter(Boolean).join(" · ") }));
    if (rows.length) {
      var table = h("table", { className: "d-table d-table-compact" }, [h("tbody", {}, rows.map(function (row) {
        return h("tr", {}, [h("td", { text: row.name }), h("td", {}, [HD.chip(row.ok ? "succeeded" : "failed", row.ok ? "available" : (row.status || "missing"))]), h("td", { className: "d-muted d-wrap", text: String(row.detail).slice(0, 200) })]);
      }))]);
      host.appendChild(table);
    }
    host.appendChild(h("details", { className: "d-details" }, [h("summary", { text: "Full diagnostics" }), h("pre", { className: "d-pre", text: JSON.stringify(info, null, 2) })]));
  }
  HD.byId("d-prefs-button").addEventListener("click", openPreferences);
  HD.openPreferences = openPreferences;

  // ---------------------------------------------------------------- help

  var WORKFLOW = [
    ["1 Data", "Add a reference FASTA, then import raw reads (FASTQ/FASTA, gzipped or not), SAM or finished .hyb files — one by one or from a sample sheet. Files stay where they are (linked) unless you choose to copy them. Select samples and run them: mapping → .hyb → analysis index. Interrupted runs resume from the last finished stage."],
    ["2 QC", "Per-sample record and read counts, chimera types, intra/inter-molecular share and replicate agreement."],
    ["3 Explore", "Interactions, contact map, viewpoint and region views for the current selection in the top bar: one sample, or a condition with its replicates merged."],
    ["4 Compare", "Choose two conditions (or samples). Differential contact bins are listed with actions to locate them on the contact map or fold the region with each condition's evidence."],
    ["5 Structure", "Whole-RNA folds (pseudoknot-free) and window folds (with pseudoknots) using every chimera of the selection as evidence; view arcs and compare two structures."],
    ["6 Report", "Assemble figures and tables from the current analysis into a printable report."]
  ];
  async function openHelp(topic) {
    var overview = window.Hyb2Overview;
    if (topic === "validation") {
      var ids = HD.selectionIds();
      if (!ids.length || !HD.workspace) { HD.toast("Select an analysed sample first."); return; }
      var summary;
      try { summary = await HD.workspace.provider().getSummary({ datasetIds: ids }); } catch (error) { HD.toast(HD.errorText(error), "error"); return; }
      HD.modal("Validation · " + HD.selectionLabel(), function (body) { body.appendChild(webContent(overview && overview.renderValidation({ summary: summary }))); }, { wide: true });
      return;
    }
    if (topic === "methods" || topic === "file-format") {
      var html = overview ? (topic === "methods" ? overview.renderMethods() : overview.renderFileFormat()) : "";
      HD.modal(topic === "methods" ? "Methods" : "File format", function (body) { body.appendChild(webContent(html)); }, { wide: true });
      return;
    }
    HD.modal("Workflow guide", function (body) {
      WORKFLOW.forEach(function (entry) {
        body.appendChild(h("h3", { className: "d-h3", text: entry[0] }));
        body.appendChild(h("p", { text: entry[1] }));
      });
      body.appendChild(h("p", { className: "d-muted", text: "Runtime components and the CPU / memory budget are under Preferences. Tasks are in the drawer behind the task indicator in the top bar." }));
    }, { wide: true });
  }
  // Static help templates shipped with the analysis pages (web/overview-page.js).
  function webContent(html) {
    var wrap = h("div", { className: "d-web-content workspace-embedded" });
    wrap.innerHTML = String(html || "");
    return wrap;
  }
  HD.byId("d-help-button").addEventListener("click", function () { toggleMenu(this, HD.byId("d-help-menu")); });
  document.querySelectorAll("[data-help]").forEach(function (item) {
    item.addEventListener("click", function () { closeMenus(); openHelp(item.dataset.help); });
  });

  // ---------------------------------------------------------------- startup

  window.addEventListener("beforeunload", function (event) {
    if (HD.activeTasks().length) {
      event.preventDefault();
      event.returnValue = "Tasks are still running. They will be marked interrupted and can be resumed.";
    }
  });

  renderRecent();
  if (!HD.available) {
    HD.byId("d-start").hidden = false;
    setStartStatus("Native project operations are only available in the installed HYB2 Desktop application.");
    return;
  }
  var recent = readRecent();
  if (recent.length) {
    HD.byId("d-open-path").value = recent[0].path;
  }
  showStart();
}());
