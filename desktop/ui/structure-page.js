/*
 * ⑤ Structure: evidence-guided CPLfold runs (Rust task `structure`): the
 * whole RNA (pseudoknot-free) or windows (pseudoknots allowed), with every
 * chimera of the chosen selection as evidence. Lists saved results
 * (list_structures), shows them as arc diagrams (read_structure) and
 * compares two structures side by side.
 */
(function () {
  "use strict";

  var HD = window.HD;
  var h = HD.h;
  var state = HD.state;
  var root = null;
  var formCard = null;
  var listCard = null;
  var viewerCard = null;
  var arc = null;
  var cache = Object.create(null);
  var form = { gene: null, evidence: null, mode: "genome", windows: "", pseudoknot: false, workers: null, prefillNote: "" };
  var viewer = { a: null, b: null, record: "all" };
  var listError = null;
  var loadingList = false;
  var pendingPair = null;   // [taskA, taskB] from Compare "Fold A & B"

  function render(container) {
    root = container;
    container.appendChild(h("div", { className: "d-page-head" }, [
      h("div", {}, [
        h("h1", { className: "d-h1", text: "Structure" }),
        h("p", { className: "d-muted", text: "CPLfold with chimeric reads as evidence: whole RNA without pseudoknots, or windows with pseudoknots." })
      ])
    ]));
    var grid = h("div", { className: "d-structure-grid" });
    formCard = h("section", { className: "d-card" });
    listCard = h("section", { className: "d-card" });
    viewerCard = h("section", { className: "d-card d-structure-viewer" });
    grid.appendChild(h("div", { className: "d-structure-side" }, [formCard, listCard]));
    grid.appendChild(viewerCard);
    container.appendChild(grid);
    drawViewer();
  }

  function onShow(options) {
    if (!root) return;
    var prefill = options && options.prefill;
    if (prefill) {
      form.gene = prefill.gene || form.gene;
      form.mode = "windows";
      form.pseudoknot = true;
      form.windows = (prefill.windows || []).map(function (window) { return window.start + "-" + window.end; }).join("\n");
      if (prefill.selectionKey) form.evidence = prefill.selectionKey;
      form.prefillNote = "Prefilled from " + (prefill.source || "a fold request") + ". Adjust the window and press Run.";
    }
    drawForm();
    loadList().then(function () {
      if (options && options.structureId) {
        viewer.a = options.structureId;
        viewer.record = "all";
        drawViewer();
      }
    });
  }

  // ---------------------------------------------------------------- new fold

  function budgetCpu() {
    var prefs = state.preferences;
    return prefs && prefs.budget && prefs.budget.cpu ? prefs.budget.cpu : 4;
  }

  function drawForm() {
    if (!formCard) return;
    HD.clear(formCard);
    formCard.appendChild(h("h2", { className: "d-h2", text: "New fold" }));
    if (form.prefillNote) formCard.appendChild(h("p", { className: "d-notice", text: form.prefillNote }));
    var genes = HD.genes();
    var gene = h("select", { className: "d-select", id: "d-fold-gene" }, genes.map(function (item) { return h("option", { value: item.name, text: item.name + " · " + HD.formatNumber(item.length) + " nt" }); }));
    gene.value = form.gene && genes.some(function (item) { return item.name === form.gene; }) ? form.gene : (state.gene || (genes[0] && genes[0].name) || "");
    gene.addEventListener("change", function () { form.gene = gene.value; });
    var evidence = h("select", { className: "d-select", id: "d-fold-evidence" });
    var evidenceKey = HD.fillSelectionSelect(evidence, form.evidence || HD.selectionKey(state.selection), "No analysed samples");
    form.evidence = evidenceKey;
    evidence.addEventListener("change", function () { form.evidence = evidence.value; });
    var genome = h("input", { type: "radio", name: "d-fold-mode", id: "d-fold-genome", value: "genome", checked: form.mode === "genome" });
    var windows = h("input", { type: "radio", name: "d-fold-mode", id: "d-fold-windows", value: "windows", checked: form.mode === "windows" });
    var windowText = h("textarea", { className: "d-input d-textarea", id: "d-fold-window-list", rows: "4", placeholder: "One window per line, e.g.\n1-400\n2300-2700", spellcheck: "false" });
    windowText.value = form.windows;
    windowText.addEventListener("input", function () { form.windows = windowText.value; });
    var pk = h("input", { type: "checkbox", id: "d-fold-pk", checked: form.mode === "windows" ? form.pseudoknot !== false : false });
    var workers = h("input", { className: "d-input d-input-narrow", type: "number", min: "1", max: "256", id: "d-fold-workers", value: String(form.workers || budgetCpu()) });
    workers.addEventListener("input", function () { form.workers = Math.floor(Number(workers.value)) || null; });
    function syncMode() {
      form.mode = windows.checked ? "windows" : "genome";
      windowRow.hidden = form.mode !== "windows";
      pkRow.hidden = form.mode !== "windows";
    }
    genome.addEventListener("change", syncMode);
    windows.addEventListener("change", syncMode);
    pk.addEventListener("change", function () { form.pseudoknot = pk.checked; });
    var windowRow = h("div", { className: "d-field" }, [h("label", { className: "d-label", for: "d-fold-window-list", text: "Windows (1-based, inclusive)" }), windowText]);
    var pkRow = h("label", { className: "d-check" }, [pk, " Allow pseudoknots in windows"]);
    formCard.appendChild(h("div", { className: "d-field" }, [h("label", { className: "d-label", for: "d-fold-gene", text: "RNA" }), gene]));
    formCard.appendChild(h("div", { className: "d-field" }, [h("label", { className: "d-label", for: "d-fold-evidence", text: "Evidence (chimeras of)" }), evidence]));
    formCard.appendChild(h("fieldset", { className: "d-fieldset" }, [
      h("legend", { className: "d-label", text: "Mode" }),
      h("label", { className: "d-check" }, [genome, " Whole RNA, pseudoknot-free"]),
      h("label", { className: "d-check" }, [windows, " Windows"])
    ]));
    formCard.appendChild(windowRow);
    formCard.appendChild(pkRow);
    formCard.appendChild(h("div", { className: "d-field d-field-inline" }, [h("label", { className: "d-label", for: "d-fold-workers", text: "Parallel windows" }), workers]));
    var status = h("p", { className: "d-status-line", role: "status" });
    var actions = h("div", { className: "d-actions" }, [h("button", { type: "button", className: "d-btn d-btn-primary", text: "Run fold", on: { click: function () {
      HD.busy(actions, function () { return runFold(status); });
    } } })]);
    formCard.appendChild(actions);
    formCard.appendChild(status);
    syncMode();
  }

  function parseWindows(text, length) {
    var windows = [];
    var errors = [];
    String(text || "").split(/[\n,;]+/).map(function (line) { return line.trim(); }).filter(Boolean).forEach(function (line) {
      var match = /^(\d+)\s*[-–:.\s]+\s*(\d+)$/.exec(line);
      if (!match) { errors.push("Not a window: " + line); return; }
      var start = Number(match[1]);
      var end = Number(match[2]);
      if (start > end) { var t = start; start = end; end = t; }
      if (start < 1 || (length && end > length)) { errors.push(line + " is outside 1-" + (length || "?")); return; }
      if (end - start + 1 < 20) { errors.push(line + " is shorter than 20 nt"); return; }
      windows.push({ start: start, end: end });
    });
    return { windows: windows, errors: errors };
  }

  async function runFold(status) {
    var gene = HD.byId("d-fold-gene").value;
    var selection = HD.parseSelectionKey(form.evidence);
    var ids = HD.selectionIds(selection);
    if (!gene) { status.textContent = "Choose the RNA to fold."; return; }
    if (!ids.length) { status.textContent = "Choose analysed samples as evidence."; return; }
    var request = { kind: "structure", sample_ids: ids, gene_id: gene, mode: form.mode, parameters: {} };
    var workers = Math.floor(Number(HD.byId("d-fold-workers").value));
    if (workers >= 1) request.workers = workers;
    if (form.mode === "windows") {
      var parsed = parseWindows(form.windows, HD.geneLength(gene));
      if (parsed.errors.length) { status.textContent = parsed.errors.join("; "); return; }
      if (!parsed.windows.length) { status.textContent = "Add at least one window."; return; }
      request.windows = parsed.windows;
      request.parameters.allow_pseudoknot = HD.byId("d-fold-pk").checked;
    }
    try {
      status.textContent = "Starting…";
      var snapshot = await HD.startTask(request);
      HD.setTaskLabel(snapshot.task_id, "Structure · " + HD.selectionLabel(selection) + " · " + (form.mode === "genome" ? "whole RNA" : request.windows.length + " window" + (request.windows.length === 1 ? "" : "s")));
      status.textContent = "Started. Progress is shown in the list and the task drawer.";
      form.prefillNote = "";
      drawList();
    } catch (error) {
      status.textContent = "Could not start: " + HD.errorText(error);
    }
  }

  // ---------------------------------------------------------------- list

  async function loadList() {
    if (!state.projectPath || loadingList) return;
    loadingList = true;
    try {
      var result = await HD.op("list_structures");
      state.structures = result.structures || [];
      listError = null;
    } catch (error) {
      listError = HD.errorText(error);
    } finally {
      loadingList = false;
    }
    if (pendingPair) {
      var ids = state.structures.map(function (item) { return item.id; });
      if (ids.indexOf(pendingPair[0]) >= 0) { viewer.a = pendingPair[0]; viewer.record = "all"; }
      if (ids.indexOf(pendingPair[0]) >= 0 && ids.indexOf(pendingPair[1]) >= 0) { viewer.b = pendingPair[1]; pendingPair = null; }
    }
    drawList();
    drawViewer();
  }

  function structureLabel(item) {
    var evidence = (item.sample_ids || item.samples || []).map(function (id) { var sample = HD.sample(id); return sample ? sample.name : id; });
    var mode = item.mode === "genome" ? "whole RNA" : (item.windows || []).length + " window" + ((item.windows || []).length === 1 ? "" : "s");
    return { title: (item.gene_id || "") + " · " + mode, evidence: evidence.join(" + ") };
  }

  function drawList() {
    if (!listCard) return;
    HD.clear(listCard);
    listCard.appendChild(h("div", { className: "d-card-head" }, [h("h2", { className: "d-h2", text: "Results" }), h("button", { type: "button", className: "d-btn d-btn-small", text: "Refresh", on: { click: loadList } })]));
    var running = HD.activeTasks().filter(function (task) { return task.kind === "structure"; });
    running.forEach(function (task) {
      listCard.appendChild(h("div", { className: "d-structure-item d-structure-running" }, [
        h("strong", { text: HD.taskLabel(task) }), HD.progressBar(task),
        h("button", { type: "button", className: "d-btn d-btn-small", text: "Cancel", on: { click: function () { HD.cancelTask(task.task_id); } } })
      ]));
    });
    var failed = Object.keys(state.tasks).map(function (id) { return state.tasks[id]; }).filter(function (task) {
      return task.kind === "structure" && (task.status === "interrupted" || (task.status === "failed" && String(task.finished_at || "") >= (HD.sessionStart || "")));
    }).slice(0, 3);
    failed.forEach(function (task) {
      listCard.appendChild(h("div", { className: "d-structure-item" }, [
        h("strong", { text: HD.taskLabel(task) }), HD.chip(task.status),
        task.error ? h("small", { className: "d-task-error", text: String(task.error).slice(0, 300) }) : null,
        h("button", { type: "button", className: "d-btn d-btn-small", text: "Resume", on: { click: function () { HD.resume(task.task_id); } } })
      ]));
    });
    if (listError) listCard.appendChild(h("p", { className: "d-task-error", text: "Could not list structures: " + listError }));
    if (!state.structures.length && !running.length) {
      listCard.appendChild(h("p", { className: "d-muted", text: "No structures yet." }));
      return;
    }
    state.structures.forEach(function (item) {
      var label = structureLabel(item);
      var isA = viewer.a === item.id;
      var isB = viewer.b === item.id;
      listCard.appendChild(h("div", { className: "d-structure-item" + (isA || isB ? " d-structure-selected" : "") }, [
        h("button", { type: "button", className: "d-link d-structure-title", text: label.title, on: { click: function () { viewer.a = item.id; viewer.record = "all"; drawList(); drawViewer(); } } }),
        h("small", { className: "d-muted", text: "Evidence: " + (label.evidence || "—") + " · " + HD.formatTime(item.created_at) }),
        h("div", { className: "d-row d-row-tight" }, [
          isA ? h("span", { className: "d-badge d-badge-a", text: "A" }) : h("button", { type: "button", className: "d-btn d-btn-small", text: "Show", on: { click: function () { viewer.a = item.id; viewer.record = "all"; drawList(); drawViewer(); } } }),
          isB ? h("span", { className: "d-badge d-badge-b", text: "B" }) : h("button", { type: "button", className: "d-btn d-btn-small", text: "Compare as B", disabled: !viewer.a || isA, on: { click: function () { viewer.b = item.id; drawList(); drawViewer(); } } })
        ])
      ]));
    });
  }

  // ---------------------------------------------------------------- viewer

  function readStructure(id) {
    if (!cache[id]) {
      cache[id] = HD.op("read_structure", { structure_id: id }).catch(function (error) { delete cache[id]; throw error; });
    }
    return cache[id];
  }

  function recordPairs(record) {
    var start = record.window_start || 1;
    if (record.dbn) return HD.ArcView.parseDbn(record.dbn, start);
    // read_structure pairs are 1-based gene coordinates.
    return (record.pairs || []).map(function (pair) {
      return { i: Number(pair[0]), j: Number(pair[1]), layer: 0 };
    });
  }

  function pickRecords(data, choice) {
    var records = data.records || [];
    if (choice === "all" || records.length <= 1) return records;
    return records.filter(function (record) { return record.window_start + "-" + record.window_end === choice; });
  }

  async function drawViewer() {
    if (!viewerCard) return;
    HD.clear(viewerCard);
    var head = h("div", { className: "d-card-head" }, [h("h2", { className: "d-h2", text: "Viewer" })]);
    viewerCard.appendChild(head);
    if (!viewer.a) {
      viewerCard.appendChild(h("p", { className: "d-muted", text: state.structures.length ? "Choose a result to display; add a second one with “Compare as B”." : "Run a fold to see its arc diagram here." }));
      arc = null;
      return;
    }
    var status = h("p", { className: "d-muted", text: "Loading…" });
    viewerCard.appendChild(status);
    var a, b = null;
    try {
      a = await readStructure(viewer.a);
      if (viewer.b) b = await readStructure(viewer.b);
    } catch (error) {
      status.textContent = "Could not read the structure: " + HD.errorText(error);
      return;
    }
    status.remove();
    var metaA = state.structures.find(function (item) { return item.id === viewer.a; }) || { id: viewer.a, gene_id: (a.meta || {}).gene_id, mode: (a.meta || {}).mode };
    var metaB = viewer.b ? (state.structures.find(function (item) { return item.id === viewer.b; }) || { id: viewer.b }) : null;
    var windowChoices = (a.records || []).length > 1 ? a.records.map(function (record) { return record.window_start + "-" + record.window_end; }) : [];
    if (windowChoices.length && viewer.record !== "all" && windowChoices.indexOf(viewer.record) < 0) viewer.record = "all";
    var controls = h("div", { className: "d-row d-row-wrap" });
    if (windowChoices.length) {
      var windowSelect = h("select", { className: "d-select", id: "d-viewer-window", "aria-label": "Window" }, [h("option", { value: "all", text: "All windows" })].concat(windowChoices.map(function (choice) { return h("option", { value: choice, text: "Window " + choice }); })));
      windowSelect.value = viewer.record;
      windowSelect.addEventListener("change", function () { viewer.record = windowSelect.value; drawViewer(); });
      controls.appendChild(windowSelect);
    }
    if (viewer.b) controls.appendChild(h("button", { type: "button", className: "d-btn d-btn-small", text: "Clear B", on: { click: function () { viewer.b = null; drawList(); drawViewer(); } } }));
    controls.appendChild(h("button", { type: "button", className: "d-btn d-btn-small", text: "Save .dbn", on: { click: function () { saveDbn(a, metaA); } } }));
    head.appendChild(controls);

    var recordsA = pickRecords(a, viewer.record);
    var recordsB = b ? pickRecords(b, viewer.record === "all" ? "all" : viewer.record) : [];
    if (b && viewer.record !== "all" && !recordsB.length) recordsB = b.records || [];
    var pairsA = [].concat.apply([], recordsA.map(recordPairs));
    var pairsB = [].concat.apply([], recordsB.map(recordPairs));
    var starts = recordsA.concat(recordsB).map(function (record) { return record.window_start || 1; });
    var ends = recordsA.concat(recordsB).map(function (record) { return record.window_end || (record.window_start || 1) + (record.sequence || "").length - 1; });
    var start = Math.min.apply(null, starts.length ? starts : [1]);
    var end = Math.max.apply(null, ends.length ? ends : [100]);
    var labelA = "A · " + structureLabel(metaA).evidence;
    var labelB = metaB ? "B · " + structureLabel(metaB).evidence : null;
    var tracks = [{ label: labelA, pairs: pairsA }];
    if (b) tracks.push({ label: labelB, pairs: pairsB });
    var arcHost = h("div", { className: "d-arc-host" });
    viewerCard.appendChild(arcHost);
    arc = HD.ArcView.create(arcHost, { height: b ? 440 : 340 });
    arc.setData({ start: start, end: end, tracks: tracks });
    // Compare like with like: pairs are counted only inside the regions
    // both structures cover (e.g. a window fold against a whole-RNA fold).
    var statsA = pairsA;
    var statsB = pairsB;
    if (b) {
      var spans = function (records) {
        return records.map(function (record) {
          var from = record.window_start || 1;
          return [from, record.window_end || from + (record.sequence || "").length - 1];
        });
      };
      var inside = function (list) {
        return function (pair) { return list.some(function (span) { return pair.i >= span[0] && pair.j <= span[1]; }); };
      };
      var spansA = spans(recordsA);
      var spansB = spans(recordsB);
      statsA = pairsA.filter(inside(spansB));
      statsB = pairsB.filter(inside(spansA));
      var coverA = spansA.reduce(function (sum, span) { return sum + span[1] - span[0] + 1; }, 0);
      var coverB = spansB.reduce(function (sum, span) { return sum + span[1] - span[0] + 1; }, 0);
      if (coverA !== coverB) viewerCard.appendChild(h("p", { className: "d-muted", text: "Pair statistics count pairs inside the regions both structures cover." }));
    }
    viewerCard.appendChild(statsTable(recordsA, statsA, recordsB, statsB, !!b));
    var meta = a.meta || {};
    var timings = meta.timings || {};
    viewerCard.appendChild(h("p", { className: "d-muted", text: [
      "Engine " + (meta.engine || "CPLfold"),
      meta.parameters ? (meta.parameters.allow_pseudoknot ? "pseudoknots allowed" : "pseudoknot-free") + ", alpha " + meta.parameters.alpha : null,
      timings.total_s ? "took " + HD.formatDuration(timings.total_s) : null,
      meta.evidence_inputs ? "evidence: " + (meta.evidence_inputs.rows || "") : null
    ].filter(Boolean).join(" · ") }));
  }

  function statsTable(recordsA, pairsA, recordsB, pairsB, compare) {
    var keyOf = function (pair) { return pair.i + ":" + pair.j; };
    var setA = new Set(pairsA.map(keyOf));
    var setB = new Set(pairsB.map(keyOf));
    var shared = 0;
    setA.forEach(function (key) { if (setB.has(key)) shared += 1; });
    function energy(records) {
      var values = records.map(function (record) { return record.energy; }).filter(function (value) { return typeof value === "number"; });
      return values.length ? values.reduce(function (sum, value) { return sum + value; }, 0).toFixed(1) + " kcal/mol" : "—";
    }
    function length(records) {
      return records.reduce(function (sum, record) { return sum + (record.sequence || "").length; }, 0);
    }
    var rows = [
      ["Length (nt)", HD.formatNumber(length(recordsA)), compare ? HD.formatNumber(length(recordsB)) : null],
      ["Base pairs", HD.formatNumber(pairsA.length), compare ? HD.formatNumber(pairsB.length) : null],
      ["Pseudoknotted pairs", HD.formatNumber(pairsA.filter(function (pair) { return pair.layer > 0; }).length), compare ? HD.formatNumber(pairsB.filter(function (pair) { return pair.layer > 0; }).length) : null],
      ["Energy", energy(recordsA), compare ? energy(recordsB) : null]
    ];
    if (compare) {
      rows.push(["Shared pairs", HD.formatNumber(shared), HD.formatNumber(shared)]);
      rows.push(["Only in this structure", HD.formatNumber(setA.size - shared), HD.formatNumber(setB.size - shared)]);
      var union = setA.size + setB.size - shared;
      rows.push(["Pair agreement (Jaccard)", union ? (shared / union).toFixed(3) : "—", ""]);
    }
    return h("div", { className: "d-table-wrap" }, h("table", { className: "d-table d-table-compact" }, [
      h("thead", {}, h("tr", {}, [h("th", { text: "" }), h("th", { text: "A" }), compare ? h("th", { text: "B" }) : null])),
      h("tbody", {}, rows.map(function (row) {
        return h("tr", {}, [h("th", { scope: "row", text: row[0] }), h("td", { className: "d-num", text: row[1] }), compare ? h("td", { className: "d-num", text: row[2] }) : null]);
      }))
    ]));
  }

  async function saveDbn(data, meta) {
    var text = (data.records || []).map(function (record) {
      return ">" + (meta.gene_id || "RNA") + ":" + record.window_start + "-" + record.window_end + (typeof record.energy === "number" ? " " + record.energy : "") + "\n" + record.sequence + "\n" + record.dbn + "\n";
    }).join("");
    var name = (meta.gene_id || "structure") + "_" + meta.id + ".dbn";
    var handled = window.Hyb2Desktop && window.Hyb2Desktop.saveFile ? await window.Hyb2Desktop.saveFile(name, new Blob([text], { type: "text/plain" })) : false;
    if (!handled) HD.toast("Saving needs the desktop application.");
  }

  // ---------------------------------------------------------------- wiring

  HD.on("tasks", function () { if (state.page === "structure") drawList(); });
  HD.on("structures", function () { loadList(); });
  HD.on("selection", function () { if (state.page === "structure") drawForm(); });
  HD.on("gene", function () { form.gene = state.gene; if (state.page === "structure") drawForm(); });
  HD.on("theme", function () { if (arc) arc.redraw(); });
  HD.on("project-opened", function () {
    cache = Object.create(null);
    viewer = { a: null, b: null, record: "all" };
    form = { gene: null, evidence: null, mode: "genome", windows: "", pseudoknot: false, workers: null, prefillNote: "" };
  });

  HD.structure = { loadList: loadList, expectPair: function (a, b) { pendingPair = [a, b]; } };
  HD.pages.structure = { render: render, onShow: onShow };
}());
