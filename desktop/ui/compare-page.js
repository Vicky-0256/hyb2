/*
 * ④ Compare: condition (or sample) A vs B from the project. Differential
 * contact bins come from the backend `compare_bins` query; each bin can be
 * located on the contact map, opened in Region, or folded once with A's and
 * once with B's evidence for a side-by-side structure comparison. When
 * compare_bins is unavailable the built-in web comparison is offered with
 * the same groups.
 */
(function () {
  "use strict";

  var HD = window.HD;
  var h = HD.h;
  var state = HD.state;
  var root = null;
  var controls = null;
  var results = null;
  var fallbackSlot = null;
  var data = null;          // last compare_bins result + parameters
  var busy = false;
  var error = null;
  var params = { binSize: 50, minCount: 5, top: 50 };
  var picked = null;        // index of the highlighted cell

  function render(container) {
    root = container;
    container.appendChild(h("div", { className: "d-page-head" }, [
      h("div", {}, [
        h("h1", { className: "d-h1", text: "Compare" }),
        h("p", { className: "d-muted", text: "Library-size normalised contact differences between two conditions. Replicates of a condition are merged." })
      ])
    ]));
    controls = h("section", { className: "d-card d-compare-controls" });
    results = h("div");
    fallbackSlot = h("div", { className: "d-slot" });
    fallbackSlot.hidden = true;
    container.appendChild(controls);
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

  function onShow() {
    if (!root) return;
    defaultGroups();
    drawControls();
    drawResults();
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
    a.addEventListener("change", function () { state.compare.a = a.value; HD.saveUiState(); });
    b.addEventListener("change", function () { state.compare.b = b.value; HD.saveUiState(); });
    var bin = h("select", { className: "d-select", id: "d-compare-bin" }, [10, 25, 50, 100, 200].map(function (size) { return h("option", { value: String(size), text: size + " nt" }); }));
    bin.value = String(params.binSize);
    bin.addEventListener("change", function () { params.binSize = Number(bin.value); });
    var minCount = h("input", { className: "d-input d-input-narrow", type: "number", min: "0", id: "d-compare-min", value: String(params.minCount) });
    minCount.addEventListener("input", function () { params.minCount = Math.max(0, Number(minCount.value) || 0); drawResults(); });
    controls.appendChild(h("div", { className: "d-compare-grid" }, [
      h("div", { className: "d-field" }, [h("label", { className: "d-label d-label-a", for: "d-compare-a", text: "A" }), a]),
      h("span", { className: "d-vs", text: "vs" }),
      h("div", { className: "d-field" }, [h("label", { className: "d-label d-label-b", for: "d-compare-b", text: "B" }), b]),
      h("div", { className: "d-field" }, [h("label", { className: "d-label", for: "d-compare-bin", text: "Bin" }), bin]),
      h("div", { className: "d-field" }, [h("label", { className: "d-label", for: "d-compare-min", text: "Min. count" }), minCount]),
      h("div", { className: "d-field d-field-end" }, [h("span", { className: "d-label", text: state.gene || "no gene" }),
        h("button", { type: "button", className: "d-btn d-btn-primary", text: busy ? "Comparing…" : "Compare", disabled: busy, on: { click: compute } })])
    ]));
  }

  async function compute() {
    var groupA = HD.selectionIds(HD.parseSelectionKey(state.compare.a));
    var groupB = HD.selectionIds(HD.parseSelectionKey(state.compare.b));
    if (!groupA.length || !groupB.length) { HD.toast("Choose two analysed groups."); return; }
    if (state.compare.a === state.compare.b) { HD.toast("A and B are the same selection."); return; }
    if (!state.gene) { HD.toast("Choose a gene in the top bar."); return; }
    busy = true;
    error = null;
    picked = null;
    fallbackSlot.hidden = true;
    drawControls();
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
      list.push({ index: index, x: xs[index], y: result.y[index], a: a, b: b, ratio: typeof ratio === "number" ? -ratio : null, padj: result.padj ? result.padj[index] : null });
    }
    list.sort(function (left, right) {
      if (left.padj != null && right.padj != null && left.padj !== right.padj) return left.padj - right.padj;
      return Math.abs(right.ratio || 0) - Math.abs(left.ratio || 0) || (right.a + right.b) - (left.a + left.b);
    });
    return list;
  }

  function drawResults() {
    if (!results) return;
    HD.clear(results);
    if (busy) {
      results.appendChild(h("section", { className: "d-card" }, h("p", { className: "d-muted", text: "Binning both groups…" })));
      return;
    }
    if (error) {
      results.appendChild(h("section", { className: "d-card" }, [
        h("p", { className: "d-task-error", text: "The differential comparison is unavailable: " + error }),
        h("button", { type: "button", className: "d-btn", text: "Use the built-in comparison instead", on: { click: showFallback } })
      ]));
      return;
    }
    if (!data) {
      results.appendChild(h("section", { className: "d-card" }, h("p", { className: "d-muted", text: "Pick A and B, then Compare." })));
      return;
    }
    var result = data.result;
    var list = rows();
    var labelA = HD.selectionLabel(HD.parseSelectionKey(data.a));
    var labelB = HD.selectionLabel(HD.parseSelectionKey(data.b));
    var card = h("section", { className: "d-card" });
    card.appendChild(h("div", { className: "d-card-head" }, [
      h("h2", { className: "d-h2", text: labelA + " vs " + labelB }),
      h("small", { className: "d-muted", text: data.gene + " · " + result.bin_size + "-nt bins · " + HD.formatNumber((result.x || []).length) + " cells" + (result.library_a != null ? " · libraries " + HD.formatNumber(result.library_a) + " / " + HD.formatNumber(result.library_b) : "") + (result.normalization ? " · " + result.normalization : "") })
    ]));
    var layout = h("div", { className: "d-compare-layout" });
    var mapHost = h("div", { className: "d-compare-map" });
    layout.appendChild(mapHost);
    var tableHost = h("div", { className: "d-compare-table" });
    layout.appendChild(tableHost);
    card.appendChild(layout);
    results.appendChild(card);
    drawHeatmap(mapHost, result, list);
    var top = list.slice(0, params.top);
    tableHost.appendChild(h("div", { className: "d-table-wrap d-table-scroll" }, h("table", { className: "d-table d-table-compact" }, [
      h("thead", {}, h("tr", {}, ["Arm 1", "Arm 2", "A", "B", "log2 A/B", ""].concat(result.padj ? ["padj"] : []).map(function (label) { return h("th", { text: label }); }))),
      h("tbody", {}, top.map(function (row) {
        var end = function (start) { return start + result.bin_size - 1; };
        var tr = h("tr", { className: picked === row.index ? "d-row-active" : "", on: { click: function () { picked = row.index; drawResults(); } } }, [
          h("td", { text: row.x + "–" + end(row.x) }),
          h("td", { text: row.y + "–" + end(row.y) }),
          h("td", { className: "d-num", text: HD.formatNumber(row.a) }),
          h("td", { className: "d-num", text: HD.formatNumber(row.b) }),
          h("td", { className: "d-num " + (row.ratio > 0 ? "d-pos" : "d-neg"), text: row.ratio === null ? "—" : row.ratio.toFixed(2) }),
          h("td", { className: "d-row-actions" }, [
            h("button", { type: "button", className: "d-btn d-btn-small", text: "Locate", title: "Show this bin on the contact map of the stronger group", on: { click: function (event) { event.stopPropagation(); locate(row, "contact-map"); } } }),
            h("button", { type: "button", className: "d-btn d-btn-small", text: "Region", title: "Open both arms in Region", on: { click: function (event) { event.stopPropagation(); locate(row, "region"); } } }),
            h("button", { type: "button", className: "d-btn d-btn-small", text: "Fold A & B", title: "Fold the window spanning both arms with A's and with B's evidence", on: { click: function (event) { event.stopPropagation(); foldBoth(row); } } })
          ])
        ].concat(result.padj ? [h("td", { className: "d-num", text: row.padj == null ? "—" : Number(row.padj).toExponential(1) })] : []));
        return tr;
      }))
    ])));
    if (!top.length) tableHost.appendChild(h("p", { className: "d-muted", text: "No bin reaches the minimum count." }));
  }

  function drawHeatmap(host, result, list) {
    var size = 360;
    var ratio = window.devicePixelRatio || 1;
    var canvas = h("canvas", { className: "d-heatmap", role: "img", "aria-label": "Differential contact map, red: higher in A, blue: higher in B" });
    canvas.width = size * ratio;
    canvas.height = size * ratio;
    canvas.style.width = size + "px";
    canvas.style.height = size + "px";
    host.appendChild(canvas);
    host.appendChild(h("div", { className: "d-legend" }, [h("span", { className: "d-legend-a", text: "higher in A" }), h("span", { className: "d-legend-b", text: "higher in B" })]));
    var ctx = canvas.getContext("2d");
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    var styles = window.getComputedStyle(document.documentElement);
    ctx.fillStyle = styles.getPropertyValue("--surface-subtle").trim() || "#f7fafb";
    ctx.fillRect(0, 0, size, size);
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
      ctx.fillStyle = row.ratio > 0 ? "rgba(214, 69, 65," + (0.15 + 0.85 * strength) + ")" : "rgba(43, 108, 196," + (0.15 + 0.85 * strength) + ")";
      ctx.fillRect(row.x * scale, row.y * scale, cell, cell);
    });
    var pickedRow = list.find(function (row) { return row.index === picked; });
    if (pickedRow) {
      ctx.strokeStyle = styles.getPropertyValue("--ink").trim() || "#000";
      ctx.lineWidth = 2;
      ctx.strokeRect(pickedRow.x * scale - 3, pickedRow.y * scale - 3, cell + 6, cell + 6);
    }
    canvas.addEventListener("click", function (event) {
      var rect = canvas.getBoundingClientRect();
      var x = (event.clientX - rect.left) / scale;
      var y = (event.clientY - rect.top) / scale;
      var hit = list.find(function (row) { return x >= row.x && x < row.x + result.bin_size && y >= row.y && y < row.y + result.bin_size; });
      if (hit) { picked = hit.index; drawResults(); }
    });
  }

  async function locate(row, page) {
    var bin = data.result.bin_size;
    // Show the bin in the group where it is stronger.
    var useB = row.b > row.a;
    var key = useB ? data.b : data.a;
    var ids = useB ? data.idsB : data.idsA;
    HD.setSelection(HD.parseSelectionKey(key));
    HD.state.sub.explore = page;
    HD.navigate("explore");
    try {
      var view = HD.workspace.view() || await HD.workspace.ensureMounted(page);
      await view.setSelection({ datasetIds: ids });
      view.focus({
        page: page,
        contact: page === "contact-map" ? { rnaX: data.gene, rnaY: data.gene, binSize: bin, selection: { x: row.x, y: row.y } } : null,
        region: page === "region" ? { rna: data.gene, start: row.x, end: row.x + bin - 1, partner: data.gene, partnerStart: row.y, partnerEnd: row.y + bin - 1 } : null
      });
    } catch (failure) {
      HD.toast(HD.errorText(failure), "error");
    }
  }

  async function foldBoth(row) {
    var bin = data.result.bin_size;
    var start = Math.max(1, Math.min(row.x, row.y));
    var end = Math.max(row.x, row.y) + bin - 1;
    var length = HD.geneLength(data.gene);
    if (length) end = Math.min(end, length);
    var ok = await HD.confirm("Fold " + data.gene + ":" + start + "–" + end, "Two window folds (pseudoknots allowed) of " + (end - start + 1) + " nt: one with " + HD.selectionLabel(HD.parseSelectionKey(data.a)) + " evidence, one with " + HD.selectionLabel(HD.parseSelectionKey(data.b)) + " evidence. They appear on the Structure page.", "Start both");
    if (!ok) return;
    var started = [];
    var groups = [[data.a, data.idsA], [data.b, data.idsB]];
    for (var index = 0; index < groups.length; index += 1) {
      try {
        var snapshot = await HD.startTask({ kind: "structure", sample_ids: groups[index][1], gene_id: data.gene, mode: "windows", windows: [{ start: start, end: end }], parameters: { allow_pseudoknot: true } });
        HD.setTaskLabel(snapshot.task_id, "Structure · " + HD.selectionLabel(HD.parseSelectionKey(groups[index][0])) + " · " + start + "-" + end);
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

  HD.on("project", function () { if (state.page === "compare") onShow(); });
  HD.on("gene", function () { if (state.page === "compare") drawControls(); });
  HD.on("theme", function () { if (state.page === "compare") drawResults(); });
  HD.on("project-opened", function () { data = null; error = null; picked = null; });

  HD.pages.compare = { render: render, onShow: onShow };
}());
