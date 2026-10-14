/*
 * ④ Compare: condition (or sample) A vs B from the project.
 *
 * The main path is the differential test: a `compare` task runs DESeq2
 * (desktop/runtime/compare.py, numpy port validated against R) on the 10-nt
 * contact maps of the replicates, and its registered result
 * (`list_comparisons` / `read_comparison`) is shown as a padj-weighted map of
 * the significant cells plus the merged interactions. Each interaction can be
 * located on the contact map, opened in Region, or folded once with A's and
 * once with B's evidence.
 *
 * "Quick view" keeps the descriptive comparison: the backend `compare_bins`
 * query (library-size normalised, no statistics). When it is unavailable the
 * built-in web comparison is offered with the same groups.
 */
(function () {
  "use strict";

  var HD = window.HD;
  var h = HD.h;
  var state = HD.state;
  var root = null;
  var controls = null;
  var listCard = null;
  var results = null;
  var fallbackSlot = null;

  var test = { minCount: 2, range: 100, padj: 0.05 };
  var comparisons = [];
  var listError = null;
  var loadingList = false;
  var selectedId = null;    // comparison shown in results
  var cache = Object.create(null);
  var view = { direction: "all", picked: null, top: 100 };

  var mode = "test";        // "test" | "quick"
  var data = null;          // last compare_bins result + parameters
  var busy = false;
  var error = null;
  var params = { binSize: 50, minCount: 5, top: 50 };
  var picked = null;        // index of the highlighted quick-view cell

  function render(container) {
    root = container;
    container.appendChild(h("div", { className: "d-page-head" }, [
      h("div", {}, [
        h("h1", { className: "d-h1", text: "Compare" }),
        h("p", { className: "d-muted", text: "Differential contacts between two conditions: DESeq2 on the 10-nt contact maps of the replicates (as hyb2_compare), merged into interactions." })
      ])
    ]));
    controls = h("section", { className: "d-card d-compare-controls" });
    listCard = h("section", { className: "d-card" });
    results = h("div");
    fallbackSlot = h("div", { className: "d-slot" });
    fallbackSlot.hidden = true;
    container.appendChild(controls);
    container.appendChild(listCard);
    container.appendChild(results);
    container.appendChild(fallbackSlot);
  }

  function defaultGroups() {
    var options = HD.selectionOptions();
    var conditions = options.filter(function (option) { return option.group === "Conditions"; });
    var pool = conditions.length >= 2 ? conditions : options;
    var keys = options.map(function (option) { return option.key; });
    if (!state.compare.a || keys.indexOf(state.compare.a) < 0) state.compare.a = pool[0] ? pool[0].key : null;
    if (!state.compare.b || keys.indexOf(state.compare.b) < 0 || state.compare.b === state.compare.a) {
      var other = pool.find(function (option) { return option.key !== state.compare.a; });
      state.compare.b = other ? other.key : null;
    }
  }

  function onShow(options) {
    if (!root) return;
    defaultGroups();
    if (options && options.comparisonId) { selectedId = options.comparisonId; mode = "test"; }
    drawControls();
    drawList();
    drawResults();
    loadList();
  }

  // ---------------------------------------------------------------- controls

  function numberField(id, label, value, min, step, onChange) {
    var input = h("input", { className: "d-input d-input-narrow", type: "number", min: String(min), step: String(step), id: id, value: String(value) });
    input.addEventListener("change", function () { onChange(input.value); });
    return h("div", { className: "d-field" }, [h("label", { className: "d-label", for: id, text: label }), input]);
  }

  function drawControls() {
    HD.clear(controls);
    var options = HD.selectionOptions();
    if (options.length < 2) {
      controls.appendChild(h("p", { className: "d-muted", text: "Comparisons need two analysed conditions or samples. Set conditions on the Data page and run the samples." }));
      return;
    }
    var a = h("select", { className: "d-select", id: "d-compare-a" });
    var b = h("select", { className: "d-select", id: "d-compare-b" });
    HD.fillSelectionSelect(a, state.compare.a);
    HD.fillSelectionSelect(b, state.compare.b);
    a.addEventListener("change", function () { state.compare.a = a.value; HD.saveUiState(); drawControls(); });
    b.addEventListener("change", function () { state.compare.b = b.value; HD.saveUiState(); drawControls(); });
    var groupA = HD.selectionIds(HD.parseSelectionKey(state.compare.a));
    var groupB = HD.selectionIds(HD.parseSelectionKey(state.compare.b));
    controls.appendChild(h("div", { className: "d-compare-grid d-compare-grid-test" }, [
      h("div", { className: "d-field" }, [h("label", { className: "d-label d-label-a", for: "d-compare-a", text: "A (condition one)" }), a]),
      h("span", { className: "d-vs", text: "vs" }),
      h("div", { className: "d-field" }, [h("label", { className: "d-label d-label-b", for: "d-compare-b", text: "B (condition two)" }), b]),
      h("div", { className: "d-field d-field-end" }, [
        h("span", { className: "d-label", text: groupA.length + " + " + groupB.length + " replicates" }),
        h("div", { className: "d-row d-row-tight" }, [
          h("button", { type: "button", className: "d-btn d-btn-primary", id: "d-compare-run", text: "Run differential test", on: { click: runTest } }),
          h("button", { type: "button", className: "d-btn", text: busy ? "Binning…" : "Quick view", title: "Library-size normalised difference, no statistics", disabled: busy, on: { click: compute } })
        ])
      ])
    ]));
    var settings = h("details", { className: "d-compare-settings" }, [
      h("summary", { text: "Settings" }),
      h("div", { className: "d-row" }, [
        numberField("d-test-min", "Test: keep cells with total count >", test.minCount, 0, 1, function (value) { test.minCount = Math.max(0, Math.round(Number(value) || 0)); }),
        numberField("d-test-range", "Merge range (nt)", test.range, 0, 10, function (value) { test.range = Math.max(0, Math.round(Number(value) || 0)); }),
        numberField("d-test-padj", "padj cutoff", test.padj, 0.0001, 0.01, function (value) { var v = Number(value); test.padj = v > 0 && v <= 1 ? v : 0.05; }),
        h("div", { className: "d-field" }, [h("label", { className: "d-label", for: "d-compare-bin", text: "Quick view bin" }), (function () {
          var bin = h("select", { className: "d-select", id: "d-compare-bin" }, [10, 25, 50, 100, 200].map(function (size) { return h("option", { value: String(size), text: size + " nt" }); }));
          bin.value = String(params.binSize);
          bin.addEventListener("change", function () { params.binSize = Number(bin.value); });
          return bin;
        }())]),
        numberField("d-compare-min", "Quick view min. count", params.minCount, 0, 1, function (value) { params.minCount = Math.max(0, Number(value) || 0); if (mode === "quick") drawResults(); })
      ]),
      h("p", { className: "d-muted d-small", text: "The test follows hyb2_compare: DESeq2 Wald test (condition one vs two, alpha 0.01, independent filtering), cells with padj below the cutoff are significant, and significant cells within the merge range of a stronger one are merged into it." })
    ]);
    controls.appendChild(settings);
  }

  function hasHyb(id) {
    var sample = HD.sample(id);
    if (!sample) return false;
    return !!((sample.outputs && sample.outputs.hyb) || (sample.reads && sample.reads.format === "hyb"));
  }

  async function runTest() {
    var keyA = state.compare.a;
    var keyB = state.compare.b;
    var groupA = HD.selectionIds(HD.parseSelectionKey(keyA));
    var groupB = HD.selectionIds(HD.parseSelectionKey(keyB));
    if (!groupA.length || !groupB.length) { HD.toast("Choose two analysed groups."); return; }
    if (groupA.some(function (id) { return groupB.indexOf(id) >= 0; })) { HD.toast("A and B share samples; choose two different groups."); return; }
    if (groupA.length + groupB.length < 3) { HD.toast("The test needs at least three samples (two replicates in one condition)."); return; }
    var missing = groupA.concat(groupB).filter(function (id) { return !hasHyb(id); });
    if (missing.length) { HD.toast("Run these samples first: " + missing.map(function (id) { var s = HD.sample(id); return s ? s.name : id; }).join(", ")); return; }
    var labelA = HD.selectionLabel(HD.parseSelectionKey(keyA));
    var labelB = HD.selectionLabel(HD.parseSelectionKey(keyB));
    try {
      var snapshot = await HD.startTask({
        kind: "compare", samples_one: groupA, samples_two: groupB,
        parameters: { min_count: test.minCount, range: test.range, padj_cutoff: test.padj, label_one: labelA, label_two: labelB }
      });
      HD.setTaskLabel(snapshot.task_id, "Differential test · " + labelA + " vs " + labelB);
      selectedId = snapshot.task_id;
      mode = "test";
      drawList();
      drawResults();
    } catch (failure) {
      HD.toast("Could not start the test: " + HD.errorText(failure), "error");
    }
  }

  // ---------------------------------------------------------------- results list

  async function loadList() {
    if (!state.projectPath || loadingList) return;
    loadingList = true;
    try {
      var result = await HD.op("list_comparisons");
      comparisons = result.comparisons || [];
      listError = null;
    } catch (failure) {
      listError = HD.errorText(failure);
    } finally {
      loadingList = false;
    }
    if (!selectedId && comparisons.length && mode === "test") selectedId = comparisons[0].id;
    drawList();
    drawResults();
  }

  function sampleNames(ids) {
    return (ids || []).map(function (id) { var sample = HD.sample(id); return sample ? sample.name : id; }).join(", ");
  }

  function drawList() {
    if (!listCard) return;
    HD.clear(listCard);
    listCard.appendChild(h("div", { className: "d-card-head" }, [h("h2", { className: "d-h2", text: "Differential tests" }), h("button", { type: "button", className: "d-btn d-btn-small", text: "Refresh", on: { click: loadList } })]));
    var running = HD.activeTasks().filter(function (task) { return task.kind === "compare"; });
    running.forEach(function (task) {
      listCard.appendChild(h("div", { className: "d-structure-item d-structure-running" }, [
        h("strong", { text: HD.taskLabel(task) }), HD.progressBar(task),
        h("button", { type: "button", className: "d-btn d-btn-small", text: "Cancel", on: { click: function () { HD.cancelTask(task.task_id); } } })
      ]));
    });
    Object.keys(state.tasks).map(function (id) { return state.tasks[id]; }).filter(function (task) {
      return task.kind === "compare" && (task.status === "interrupted" || (task.status === "failed" && String(task.finished_at || "") >= (HD.sessionStart || "")));
    }).slice(0, 3).forEach(function (task) {
      listCard.appendChild(h("div", { className: "d-structure-item" }, [
        h("strong", { text: HD.taskLabel(task) }), HD.chip(task.status),
        task.error ? h("small", { className: "d-task-error", text: String(task.error).slice(0, 300) }) : null,
        h("button", { type: "button", className: "d-btn d-btn-small", text: "Resume", on: { click: function () { HD.resume(task.task_id); } } })
      ]));
    });
    if (listError) listCard.appendChild(h("p", { className: "d-task-error", text: "Could not list comparisons: " + listError }));
    if (!comparisons.length && !running.length) {
      listCard.appendChild(h("p", { className: "d-muted", text: "No differential tests yet. Pick A and B, then Run differential test." }));
      return;
    }
    comparisons.forEach(function (item) {
      var summary = item.summary || {};
      var selected = mode === "test" && selectedId === item.id;
      listCard.appendChild(h("div", { className: "d-structure-item" + (selected ? " d-structure-selected" : "") }, [
        h("button", { type: "button", className: "d-link d-structure-title", text: (item.label_one || sampleNames(item.samples_one)) + " vs " + (item.label_two || sampleNames(item.samples_two)), on: { click: function () { show(item.id); } } }),
        h("small", { className: "d-muted", text: HD.formatNumber(summary.significant || 0) + " significant cells (" + HD.formatNumber(summary.significant_up || 0) + " higher in A, " + HD.formatNumber(summary.significant_down || 0) + " in B) · " + HD.formatNumber(summary.interactions || 0) + " interactions · " + HD.formatTime(item.created_at) + (item.available === false ? " · files missing" : "") })
      ]));
    });
  }

  function show(id) {
    selectedId = id;
    mode = "test";
    view.picked = null;
    fallbackSlot.hidden = true;
    drawList();
    drawResults();
  }

  function readComparison(id) {
    if (!cache[id]) {
      cache[id] = HD.op("read_comparison", { comparison_id: id, limit: 250000 }).catch(function (failure) { delete cache[id]; throw failure; });
    }
    return cache[id];
  }

  // ---------------------------------------------------------------- results

  function drawResults() {
    if (!results) return;
    HD.clear(results);
    if (mode === "quick") { drawQuick(); return; }
    if (!selectedId) return;
    var entry = comparisons.find(function (item) { return item.id === selectedId; });
    if (!entry) {
      var task = state.tasks[selectedId];
      if (task && HD.isActiveTask(task)) results.appendChild(h("section", { className: "d-card" }, h("p", { className: "d-muted", text: "The test is running; results appear here when it finishes." })));
      return;
    }
    var card = h("section", { className: "d-card" }, h("p", { className: "d-muted", text: "Loading results…" }));
    results.appendChild(card);
    var id = selectedId;
    readComparison(id).then(function (result) {
      if (selectedId !== id || mode !== "test") return;
      drawTestResult(card, entry, result);
    }, function (failure) {
      if (selectedId !== id) return;
      HD.clear(card);
      card.appendChild(h("p", { className: "d-task-error", text: "Could not read the comparison: " + HD.errorText(failure) }));
    });
  }

  // Group keys/genes for Locate / Region / Fold from a comparison entry.
  function testContext(entry) {
    function keyFor(ids) {
      var conditions = {};
      ids.forEach(function (id) { var sample = HD.sample(id); if (sample && sample.condition) conditions[sample.condition] = true; });
      var names = Object.keys(conditions);
      if (names.length === 1) {
        var all = HD.selectionIds({ kind: "condition", id: names[0] });
        if (all.length === ids.length && ids.every(function (id) { return all.indexOf(id) >= 0; })) return "c:" + names[0];
      }
      return "s:" + ids[0];
    }
    function geneFor(ids) {
      var sample = HD.sample(ids[0]);
      return (entry.parameters && entry.parameters.gene_id) || (sample && sample.gene_id) || (entry.genes || [])[0] || state.gene;
    }
    var one = entry.samples_one || [];
    var two = entry.samples_two || [];
    return { a: keyFor(one), b: keyFor(two), idsA: one, idsB: two, geneA: geneFor(one), geneB: geneFor(two), bin: 10,
             labelA: entry.label_one || sampleNames(one), labelB: entry.label_two || sampleNames(two),
             pad: (entry.parameters && entry.parameters.range) || 100 };
  }

  function drawTestResult(card, entry, result) {
    HD.clear(card);
    var summary = result.summary || {};
    var ctx = testContext(entry);
    var genes = entry.genes || [];
    card.appendChild(h("div", { className: "d-card-head" }, [
      h("h2", { className: "d-h2", text: ctx.labelA + " vs " + ctx.labelB }),
      h("small", { className: "d-muted", text: (genes.join(", ") || "") + " · DESeq2 on " + HD.formatNumber(summary.cells_tested || 0) + " cells (10 nt) · padj < " + ((entry.parameters || {}).padj_cutoff || 0.05) + " · log2FC = A / B" })
    ]));
    card.appendChild(h("div", { className: "d-stat-row" }, [
      stat("Significant cells", HD.formatNumber(summary.significant || 0)),
      stat("Higher in A", HD.formatNumber(summary.significant_up || 0), "d-pos"),
      stat("Higher in B", HD.formatNumber(summary.significant_down || 0), "d-neg"),
      stat("Interactions", HD.formatNumber(summary.interactions_up || 0) + " / " + HD.formatNumber(summary.interactions_down || 0)),
      stat("Size factors", (summary.size_factors || []).map(function (value) { return value.toFixed(2); }).join(" "))
    ]));
    if (result.truncated) card.appendChild(h("p", { className: "d-muted d-small", text: "The map shows the " + HD.formatNumber(result.cells.x.length) + " strongest of " + HD.formatNumber(result.cells_total) + " significant cells." }));
    var layout = h("div", { className: "d-compare-layout" });
    var mapHost = h("div", { className: "d-compare-map" });
    var tableHost = h("div", { className: "d-compare-table" });
    layout.appendChild(mapHost);
    layout.appendChild(tableHost);
    card.appendChild(layout);
    var length = 0;
    genes.forEach(function (gene) { length = Math.max(length, HD.geneLength(gene) || 0); });
    if (!length) length = (summary.max_coordinate || 0) + 10;
    var interactions = (result.interactions || []).slice().sort(function (left, right) { return (left.padj || 0) - (right.padj || 0); });
    var shown = interactions.filter(function (row) { return view.direction === "all" || row.direction === view.direction; });
    drawTestHeatmap(mapHost, result.cells, length, shown, ctx, function () { drawTestResult(card, entry, result); });

    var filter = h("div", { className: "d-segmented", role: "group", "aria-label": "Direction" }, [["all", "All"], ["up", "Higher in A"], ["down", "Higher in B"]].map(function (pair) {
      return h("button", { type: "button", className: "d-seg" + (view.direction === pair[0] ? " d-seg-active" : ""), text: pair[1], on: { click: function () { view.direction = pair[0]; view.picked = null; drawTestResult(card, entry, result); } } });
    }));
    tableHost.appendChild(h("div", { className: "d-card-head" }, [h("strong", { text: "Merged interactions (" + HD.formatNumber(shown.length) + ")" }), filter]));
    var top = shown.slice(0, view.top);
    tableHost.appendChild(h("div", { className: "d-table-wrap d-table-scroll" }, h("table", { className: "d-table d-table-compact" }, [
      h("thead", {}, h("tr", {}, ["Arm 1", "Arm 2", "log2 A/B", "padj", "Mean", ""].map(function (label) { return h("th", { text: label }); }))),
      h("tbody", {}, top.map(function (row) {
        var key = row.x + "_" + row.y + "_" + row.direction;
        return h("tr", { className: view.picked === key ? "d-row-active" : "", on: { click: function () { view.picked = key; drawTestResult(card, entry, result); } } }, [
          h("td", { text: row.x + "–" + (row.x + 9) }),
          h("td", { text: row.y + "–" + (row.y + 9) }),
          h("td", { className: "d-num " + (row.log2FoldChange > 0 ? "d-pos" : "d-neg"), text: row.log2FoldChange == null ? "—" : row.log2FoldChange.toFixed(2) }),
          h("td", { className: "d-num", text: row.padj == null ? "—" : Number(row.padj).toExponential(1) }),
          h("td", { className: "d-num", text: row.baseMean == null ? "—" : HD.formatNumber(Math.round(row.baseMean)) }),
          h("td", { className: "d-row-actions" }, actionButtons({ x: row.x, y: row.y, higherInB: row.log2FoldChange < 0 }, ctx))
        ]);
      }))
    ])));
    if (shown.length > top.length) tableHost.appendChild(h("button", { type: "button", className: "d-btn d-btn-small", text: "Show more", on: { click: function () { view.top += 100; drawTestResult(card, entry, result); } } }));
    if (!shown.length) tableHost.appendChild(h("p", { className: "d-muted", text: "No significant interaction in this direction." }));
    tableHost.appendChild(h("p", { className: "d-muted d-small", text: "Files: " + Object.keys(entry.paths || {}).map(function (key) { return entry.paths[key]; }).filter(Boolean).join(", ") }));
  }

  function stat(label, value, cls) {
    return h("div", { className: "d-stat" }, [h("small", { className: "d-muted", text: label }), h("strong", { className: cls || "", text: value })]);
  }

  function drawTestHeatmap(host, cells, length, interactions, ctx, redraw) {
    var size = 360;
    var ratio = window.devicePixelRatio || 1;
    var canvas = h("canvas", { className: "d-heatmap", role: "img", "aria-label": "Significant cells: red higher in A, blue higher in B; stronger colour, smaller padj" });
    canvas.width = size * ratio;
    canvas.height = size * ratio;
    canvas.style.width = size + "px";
    canvas.style.height = size + "px";
    host.appendChild(canvas);
    host.appendChild(h("div", { className: "d-legend" }, [h("span", { className: "d-legend-a", text: "higher in " + ctx.labelA }), h("span", { className: "d-legend-b", text: "higher in " + ctx.labelB })]));
    var ctx2d = canvas.getContext("2d");
    ctx2d.setTransform(ratio, 0, 0, ratio, 0, 0);
    var styles = window.getComputedStyle(document.documentElement);
    ctx2d.fillStyle = styles.getPropertyValue("--surface-subtle").trim() || "#f7fafb";
    ctx2d.fillRect(0, 0, size, size);
    var scale = size / Math.max(1, length);
    var cell = Math.max(1, 10 * scale);
    var xs = cells.x || [];
    // Draw weakest first so strong cells stay on top.
    for (var index = xs.length - 1; index >= 0; index -= 1) {
      var lfc = cells.log2FoldChange[index];
      if (lfc == null) continue;
      if (view.direction === "up" && lfc < 0) continue;
      if (view.direction === "down" && lfc > 0) continue;
      var padj = cells.padj[index] || 1e-300;
      var strength = Math.min(1, -Math.log10(Math.max(padj, 1e-300)) / 20);
      ctx2d.fillStyle = lfc > 0 ? "rgba(214, 69, 65," + (0.2 + 0.8 * strength) + ")" : "rgba(43, 108, 196," + (0.2 + 0.8 * strength) + ")";
      ctx2d.fillRect(xs[index] * scale, cells.y[index] * scale, cell, cell);
    }
    var pickedRow = interactions.find(function (row) { return row.x + "_" + row.y + "_" + row.direction === view.picked; });
    if (pickedRow) {
      ctx2d.strokeStyle = styles.getPropertyValue("--ink").trim() || "#000";
      ctx2d.lineWidth = 2;
      var r = Math.max(4, ctx.pad * scale);
      ctx2d.strokeRect(pickedRow.x * scale - r, pickedRow.y * scale - r, 2 * r + cell, 2 * r + cell);
    }
    canvas.addEventListener("click", function (event) {
      var rect = canvas.getBoundingClientRect();
      var x = (event.clientX - rect.left) / scale;
      var y = (event.clientY - rect.top) / scale;
      var best = null;
      var bestDistance = Infinity;
      interactions.forEach(function (row) {
        var distance = Math.max(Math.abs(row.x - x), Math.abs(row.y - y));
        if (distance < bestDistance) { bestDistance = distance; best = row; }
      });
      if (best && bestDistance <= Math.max(ctx.pad, 10 / scale)) {
        view.picked = best.x + "_" + best.y + "_" + best.direction;
        redraw();
      }
    });
  }

  // ---------------------------------------------------------------- actions

  function actionButtons(row, ctx) {
    return [
      h("button", { type: "button", className: "d-btn d-btn-small", text: "Locate", title: "Show this cell on the contact map of the group where it is stronger", on: { click: function (event) { event.stopPropagation(); locate(row, ctx, "contact-map"); } } }),
      h("button", { type: "button", className: "d-btn d-btn-small", text: "Region", title: "Open both arms in Region", on: { click: function (event) { event.stopPropagation(); locate(row, ctx, "region"); } } }),
      h("button", { type: "button", className: "d-btn d-btn-small", text: "Fold A & B", title: "Fold the window spanning both arms with A's and with B's evidence", on: { click: function (event) { event.stopPropagation(); foldBoth(row, ctx); } } })
    ];
  }

  async function locate(row, ctx, page) {
    var useB = row.higherInB;
    var key = useB ? ctx.b : ctx.a;
    var ids = useB ? ctx.idsB : ctx.idsA;
    var gene = useB ? ctx.geneB : ctx.geneA;
    HD.setSelection(HD.parseSelectionKey(key));
    HD.state.sub.explore = page;
    HD.navigate("explore");
    try {
      var current = HD.workspace.view() || await HD.workspace.ensureMounted(page);
      await current.setSelection({ datasetIds: ids });
      current.focus({
        page: page,
        contact: page === "contact-map" ? { rnaX: gene, rnaY: gene, binSize: ctx.bin, selection: { x: row.x, y: row.y } } : null,
        region: page === "region" ? { rna: gene, start: row.x, end: row.x + ctx.bin - 1, partner: gene, partnerStart: row.y, partnerEnd: row.y + ctx.bin - 1 } : null
      });
    } catch (failure) {
      HD.toast(HD.errorText(failure), "error");
    }
  }

  async function foldBoth(row, ctx) {
    var pad = ctx.pad || 0;
    var groups = [[ctx.a, ctx.idsA, ctx.geneA, ctx.labelA], [ctx.b, ctx.idsB, ctx.geneB, ctx.labelB]];
    var start = Math.max(1, Math.min(row.x, row.y) - pad);
    var end = Math.max(row.x, row.y) + ctx.bin - 1 + pad;
    var length = HD.geneLength(ctx.geneA);
    if (length) end = Math.min(end, length);
    var ok = await HD.confirm("Fold " + ctx.geneA + ":" + start + "–" + end, "Two window folds (pseudoknots allowed) of " + (end - start + 1) + " nt: one with " + ctx.labelA + " evidence, one with " + ctx.labelB + " evidence. They appear on the Structure page.", "Start both");
    if (!ok) return;
    var started = [];
    for (var index = 0; index < groups.length; index += 1) {
      try {
        var geneLength = HD.geneLength(groups[index][2]);
        var snapshot = await HD.startTask({ kind: "structure", sample_ids: groups[index][1], gene_id: groups[index][2], mode: "windows", windows: [{ start: start, end: geneLength ? Math.min(end, geneLength) : end }], parameters: { allow_pseudoknot: true } });
        HD.setTaskLabel(snapshot.task_id, "Structure · " + groups[index][3] + " · " + start + "-" + end);
        started.push(snapshot.task_id);
      } catch (failure) {
        HD.toast("Could not start the fold: " + HD.errorText(failure), "error");
      }
    }
    if (started.length) {
      if (HD.structure && HD.structure.expectPair && started.length === 2) HD.structure.expectPair(started[0], started[1]);
      HD.navigate("structure");
    }
  }

  // ---------------------------------------------------------------- quick view (compare_bins)

  async function compute() {
    var groupA = HD.selectionIds(HD.parseSelectionKey(state.compare.a));
    var groupB = HD.selectionIds(HD.parseSelectionKey(state.compare.b));
    if (!groupA.length || !groupB.length) { HD.toast("Choose two analysed groups."); return; }
    if (state.compare.a === state.compare.b) { HD.toast("A and B are the same selection."); return; }
    if (!state.gene) { HD.toast("Choose a gene in the top bar."); return; }
    mode = "quick";
    busy = true;
    error = null;
    picked = null;
    fallbackSlot.hidden = true;
    drawControls();
    drawList();
    drawResults();
    try {
      var result = await HD.query("compare_bins", { sample_ids_a: groupA, sample_ids_b: groupB, gene: state.gene, bin_size: params.binSize, max_cells: 400000 });
      data = { result: result, a: state.compare.a, b: state.compare.b, gene: state.gene, idsA: groupA, idsB: groupB };
    } catch (failure) {
      data = null;
      error = HD.errorText(failure);
    } finally {
      busy = false;
      drawControls();
      drawResults();
    }
  }

  function quickContext() {
    return { a: data.a, b: data.b, idsA: data.idsA, idsB: data.idsB, geneA: data.gene, geneB: data.gene, bin: data.result.bin_size,
             labelA: HD.selectionLabel(HD.parseSelectionKey(data.a)), labelB: HD.selectionLabel(HD.parseSelectionKey(data.b)), pad: 0 };
  }

  function rows() {
    if (!data) return [];
    var result = data.result;
    var list = [];
    var xs = result.x || [];
    for (var index = 0; index < xs.length; index += 1) {
      // Raw counts (a_count/b_count); a/b are library-normalised values.
      var a = Number((result.a_count || result.a)[index]) || 0;
      var b = Number((result.b_count || result.b)[index]) || 0;
      if (a + b < params.minCount) continue;
      // Backend log2ratio = log2(B / A) (CPM normalised); shown as A / B.
      var ratio = result.log2ratio ? result.log2ratio[index] : null;
      list.push({ index: index, x: xs[index], y: result.y[index], a: a, b: b, ratio: typeof ratio === "number" ? -ratio : null });
    }
    list.sort(function (left, right) {
      return Math.abs(right.ratio || 0) - Math.abs(left.ratio || 0) || (right.a + right.b) - (left.a + left.b);
    });
    return list;
  }

  function drawQuick() {
    if (busy) {
      results.appendChild(h("section", { className: "d-card" }, h("p", { className: "d-muted", text: "Binning both groups…" })));
      return;
    }
    if (error) {
      results.appendChild(h("section", { className: "d-card" }, [
        h("p", { className: "d-task-error", text: "The quick comparison is unavailable: " + error }),
        h("button", { type: "button", className: "d-btn", text: "Use the built-in comparison instead", on: { click: showFallback } })
      ]));
      return;
    }
    if (!data) return;
    var result = data.result;
    var list = rows();
    var ctx = quickContext();
    var card = h("section", { className: "d-card" });
    card.appendChild(h("div", { className: "d-card-head" }, [
      h("h2", { className: "d-h2", text: "Quick view · " + ctx.labelA + " vs " + ctx.labelB }),
      h("small", { className: "d-muted", text: data.gene + " · " + result.bin_size + "-nt bins · " + HD.formatNumber((result.x || []).length) + " cells" + (result.library_a != null ? " · libraries " + HD.formatNumber(result.library_a) + " / " + HD.formatNumber(result.library_b) : "") + " · no statistics; use the differential test for padj" })
    ]));
    var layout = h("div", { className: "d-compare-layout" });
    var mapHost = h("div", { className: "d-compare-map" });
    layout.appendChild(mapHost);
    var tableHost = h("div", { className: "d-compare-table" });
    layout.appendChild(tableHost);
    card.appendChild(layout);
    results.appendChild(card);
    drawQuickHeatmap(mapHost, result, list, ctx);
    var top = list.slice(0, params.top);
    tableHost.appendChild(h("div", { className: "d-table-wrap d-table-scroll" }, h("table", { className: "d-table d-table-compact" }, [
      h("thead", {}, h("tr", {}, ["Arm 1", "Arm 2", "A", "B", "log2 A/B", ""].map(function (label) { return h("th", { text: label }); }))),
      h("tbody", {}, top.map(function (row) {
        var end = function (start) { return start + result.bin_size - 1; };
        return h("tr", { className: picked === row.index ? "d-row-active" : "", on: { click: function () { picked = row.index; drawResults(); } } }, [
          h("td", { text: row.x + "–" + end(row.x) }),
          h("td", { text: row.y + "–" + end(row.y) }),
          h("td", { className: "d-num", text: HD.formatNumber(row.a) }),
          h("td", { className: "d-num", text: HD.formatNumber(row.b) }),
          h("td", { className: "d-num " + (row.ratio > 0 ? "d-pos" : "d-neg"), text: row.ratio === null ? "—" : row.ratio.toFixed(2) }),
          h("td", { className: "d-row-actions" }, actionButtons({ x: row.x, y: row.y, higherInB: row.b > row.a }, ctx))
        ]);
      }))
    ])));
    if (!top.length) tableHost.appendChild(h("p", { className: "d-muted", text: "No bin reaches the minimum count." }));
  }

  function drawQuickHeatmap(host, result, list, ctx) {
    var size = 360;
    var ratio = window.devicePixelRatio || 1;
    var canvas = h("canvas", { className: "d-heatmap", role: "img", "aria-label": "Normalised contact difference, red: higher in A, blue: higher in B" });
    canvas.width = size * ratio;
    canvas.height = size * ratio;
    canvas.style.width = size + "px";
    canvas.style.height = size + "px";
    host.appendChild(canvas);
    host.appendChild(h("div", { className: "d-legend" }, [h("span", { className: "d-legend-a", text: "higher in " + ctx.labelA }), h("span", { className: "d-legend-b", text: "higher in " + ctx.labelB })]));
    var ctx2d = canvas.getContext("2d");
    ctx2d.setTransform(ratio, 0, 0, ratio, 0, 0);
    var styles = window.getComputedStyle(document.documentElement);
    ctx2d.fillStyle = styles.getPropertyValue("--surface-subtle").trim() || "#f7fafb";
    ctx2d.fillRect(0, 0, size, size);
    var length = result.length;
    if (!length) {
      length = 1;
      (result.x || []).forEach(function (x, cell) { length = Math.max(length, x, result.y[cell]); });
      length += result.bin_size;
    }
    var scale = size / length;
    var cell = Math.max(1, result.bin_size * scale);
    list.forEach(function (row) {
      if (row.ratio === null) return;
      var strength = Math.min(1, Math.abs(row.ratio) / 3);
      ctx2d.fillStyle = row.ratio > 0 ? "rgba(214, 69, 65," + (0.15 + 0.85 * strength) + ")" : "rgba(43, 108, 196," + (0.15 + 0.85 * strength) + ")";
      ctx2d.fillRect(row.x * scale, row.y * scale, cell, cell);
    });
    var pickedRow = list.find(function (row) { return row.index === picked; });
    if (pickedRow) {
      ctx2d.strokeStyle = styles.getPropertyValue("--ink").trim() || "#000";
      ctx2d.lineWidth = 2;
      ctx2d.strokeRect(pickedRow.x * scale - 3, pickedRow.y * scale - 3, cell + 6, cell + 6);
    }
    canvas.addEventListener("click", function (event) {
      var rect = canvas.getBoundingClientRect();
      var x = (event.clientX - rect.left) / scale;
      var y = (event.clientY - rect.top) / scale;
      var hit = list.find(function (row) { return x >= row.x && x < row.x + result.bin_size && y >= row.y && y < row.y + result.bin_size; });
      if (hit) { picked = hit.index; drawResults(); }
    });
  }

  // Built-in web comparison with the same groups (when compare_bins fails).
  async function showFallback() {
    var provider = HD.workspace.provider();
    var groupA = HD.selectionIds(HD.parseSelectionKey(state.compare.a));
    var groupB = HD.selectionIds(HD.parseSelectionKey(state.compare.b));
    provider.setCompareGroups({ A: groupA, B: groupB });
    fallbackSlot.hidden = false;
    try {
      await HD.workspace.show(fallbackSlot, "compare");
    } catch (failure) {
      HD.toast(HD.errorText(failure), "error");
    }
  }

  HD.on("project", function () { if (state.page === "compare") { drawControls(); drawList(); } });
  HD.on("gene", function () { if (state.page === "compare") drawControls(); });
  HD.on("theme", function () { if (state.page === "compare") drawResults(); });
  HD.on("tasks", function () { if (state.page === "compare") drawList(); });
  HD.on("comparisons", function () { cache = Object.create(null); loadList(); });
  HD.on("project-opened", function () {
    data = null; error = null; picked = null; comparisons = []; selectedId = null; mode = "test";
    cache = Object.create(null); view = { direction: "all", picked: null, top: 100 };
  });

  HD.pages.compare = { render: render, onShow: onShow };
}());
