/*
 * ② QC: per-sample counts from the analysis indexes (one analysis_summary
 * call), replicate agreement per condition (contact-map correlation at a
 * coarse bin) and the analysis Overview of the current selection.
 */
(function () {
  "use strict";

  var HD = window.HD;
  var h = HD.h;
  var state = HD.state;
  var root = null;
  var tabsHost = null;
  var body = null;
  var overviewSlot = null;
  var correlation = { key: null, rows: null, busy: false, error: null, binSize: 100 };

  function render(container) {
    root = container;
    tabsHost = h("div", { className: "d-tabs", role: "tablist", "aria-label": "QC views" });
    body = h("div");
    overviewSlot = h("div", { className: "d-slot" });
    container.appendChild(h("div", { className: "d-page-head d-page-head-tight" }, [
      h("div", {}, [h("h1", { className: "d-h1", text: "Quality control" }), h("p", { className: "d-muted", text: "Analysed samples: library size, chimera composition and replicate agreement." })]),
      tabsHost
    ]));
    container.appendChild(body);
    container.appendChild(overviewSlot);
  }

  function renderTabs() {
    HD.clear(tabsHost);
    [{ id: "samples", label: "Samples" }, { id: "overview", label: "Overview of selection" }].forEach(function (tab) {
      var active = state.sub.qc === tab.id;
      tabsHost.appendChild(h("button", { type: "button", role: "tab", className: "d-tab" + (active ? " d-tab-active" : ""), "aria-selected": active ? "true" : "false", text: tab.label, on: { click: function () {
        state.sub.qc = tab.id;
        HD.saveUiState();
        onShow();
      } } }));
    });
  }

  function onShow() {
    if (!root) return;
    renderTabs();
    if (state.sub.qc === "overview") {
      HD.clear(body);
      if (!HD.selectionIds().length) {
        body.appendChild(h("div", { className: "d-empty d-card" }, h("p", { text: "No analysed sample selected." })));
      }
      overviewSlot.hidden = false;
      HD.workspace.show(overviewSlot, "overview").catch(function (error) { HD.toast(HD.errorText(error), "error"); });
      return;
    }
    overviewSlot.hidden = true;
    drawSamples();
    var missing = HD.readySamples().some(function (sample) { return !state.stats[sample.id]; });
    if (missing) HD.loadStats().catch(function (error) { HD.toast("QC counts: " + HD.errorText(error), "error"); });
  }

  function drawSamples() {
    if (!body || state.sub.qc !== "samples") return;
    HD.clear(body);
    var ready = HD.readySamples();
    if (!ready.length) {
      body.appendChild(h("div", { className: "d-empty d-card" }, [
        h("p", { text: "No analysed samples yet." }),
        h("button", { type: "button", className: "d-btn d-btn-primary", text: "Go to Data", on: { click: function () { HD.navigate("data"); } } })
      ]));
      return;
    }
    var typeNames = [];
    ready.forEach(function (sample) {
      var stats = state.stats[sample.id];
      (stats && stats.types || []).forEach(function (type) { if (typeNames.indexOf(type.name) < 0 && typeNames.length < 4) typeNames.push(type.name); });
    });
    var head = ["Sample", "Condition", "Rep", "Hybrids", "Reads", "Reads / hybrid", "With ΔG", "Intra", "Inter"].concat(typeNames);
    var maxRecords = Math.max.apply(null, ready.map(function (sample) { return (state.stats[sample.id] || {}).records || 0; }).concat([1]));
    var rows = ready.map(function (sample) {
      var stats = state.stats[sample.id];
      if (!stats) return h("tr", {}, [h("td", { text: sample.name }), h("td", { colSpan: head.length - 1, className: "d-muted", text: "loading…" })]);
      var types = {};
      (stats.types || []).forEach(function (type) { types[type.name] = type.records; });
      var bar = h("span", { className: "d-inline-bar", style: "width:" + Math.max(2, Math.round(60 * stats.records / maxRecords)) + "px" });
      return h("tr", {}, [
        h("td", {}, h("button", { type: "button", className: "d-link", text: sample.name, title: "Select and explore", on: { click: function () { HD.setSelection({ kind: "sample", id: sample.id }); HD.navigate("explore"); } } })),
        h("td", { text: sample.condition || "—" }),
        h("td", { text: sample.replicate == null ? "—" : String(sample.replicate) }),
        h("td", { className: "d-num" }, [bar, " ", HD.formatNumber(stats.records)]),
        h("td", { className: "d-num", text: HD.formatNumber(stats.reads) }),
        h("td", { className: "d-num", text: stats.records ? (stats.reads / stats.records).toFixed(2) : "—" }),
        h("td", { className: "d-num", text: HD.percent(stats.with_dg, stats.records) }),
        h("td", { className: "d-num", text: HD.percent(stats.intra_records, stats.records) }),
        h("td", { className: "d-num", text: HD.percent(stats.inter_records, stats.records) })
      ].concat(typeNames.map(function (name) { return h("td", { className: "d-num", text: HD.percent(types[name] || 0, stats.records) }); })));
    });
    var card = h("section", { className: "d-card" }, [
      h("div", { className: "d-card-head" }, [h("h2", { className: "d-h2", text: "Per-sample summary" }),
        h("button", { type: "button", className: "d-btn d-btn-small", text: "Refresh", on: { click: function () { state.stats = Object.create(null); onShow(); } } })]),
      h("div", { className: "d-table-wrap" }, h("table", { className: "d-table" }, [
        h("thead", {}, h("tr", {}, head.map(function (label) { return h("th", { text: label }); }))),
        h("tbody", {}, rows)
      ]))
    ]);
    body.appendChild(card);
    body.appendChild(renderCorrelation());
  }

  // ---------------------------------------------------------------- replicates

  function replicateGroups() {
    return state.conditions.map(function (condition) {
      return { name: condition.name, ids: HD.selectionIds({ kind: "condition", id: condition.name }) };
    }).filter(function (group) { return group.ids.length >= 2; });
  }

  function renderCorrelation() {
    var card = h("section", { className: "d-card" });
    card.appendChild(h("div", { className: "d-card-head" }, [
      h("h2", { className: "d-h2", text: "Replicate agreement" }),
      h("button", { type: "button", className: "d-btn d-btn-small", text: correlation.busy ? "Computing…" : "Compute", disabled: correlation.busy || !state.gene, on: { click: computeCorrelation } })
    ]));
    var groups = replicateGroups();
    if (!groups.length) {
      card.appendChild(h("p", { className: "d-muted", text: "Conditions with at least two analysed replicates appear here." }));
      return card;
    }
    card.appendChild(h("p", { className: "d-muted", text: "Pearson correlation of log(1 + count) over " + correlation.binSize + "-nt contact bins of " + (state.gene || "the selected gene") + ", per replicate pair; bins empty in both are skipped." }));
    if (correlation.error) card.appendChild(h("p", { className: "d-task-error", text: correlation.error }));
    if (correlation.rows && correlation.key === correlationKey()) {
      card.appendChild(h("div", { className: "d-table-wrap" }, h("table", { className: "d-table d-table-compact" }, [
        h("thead", {}, h("tr", {}, ["Condition", "Replicate A", "Replicate B", "r", "Shared bins"].map(function (label) { return h("th", { text: label }); }))),
        h("tbody", {}, correlation.rows.map(function (row) {
          return h("tr", {}, [h("td", { text: row.condition }), h("td", { text: row.a }), h("td", { text: row.b }),
            h("td", { className: "d-num" + (row.r < 0.8 ? " d-warning" : ""), text: row.r === null ? "—" : row.r.toFixed(3) }), h("td", { className: "d-num", text: HD.formatNumber(row.cells) })]);
        }))
      ])));
    } else {
      card.appendChild(h("p", { text: groups.map(function (group) { return group.name + " (" + group.ids.length + ")"; }).join(", ") }));
    }
    return card;
  }

  function correlationKey() {
    return [state.gene, correlation.binSize].concat(replicateGroups().map(function (group) { return group.name + ":" + group.ids.join(","); })).join("|");
  }

  async function computeCorrelation() {
    var groups = replicateGroups();
    if (!groups.length || !state.gene) return;
    correlation.busy = true;
    correlation.error = null;
    drawSamples();
    try {
      var bins = Object.create(null);
      var ids = [];
      groups.forEach(function (group) { group.ids.forEach(function (id) { if (ids.indexOf(id) < 0) ids.push(id); }); });
      for (var index = 0; index < ids.length; index += 1) {
        var result = await HD.query("analysis_contact_bins", { sample_ids: [ids[index]], gene: state.gene, bin_size: correlation.binSize, measure: "records" });
        var map = new Map();
        (result.x || []).forEach(function (x, cell) { map.set(x + ":" + result.y[cell], result.count[cell]); });
        bins[ids[index]] = map;
      }
      var rows = [];
      groups.forEach(function (group) {
        for (var i = 0; i < group.ids.length; i += 1) {
          for (var j = i + 1; j < group.ids.length; j += 1) {
            var pair = pearson(bins[group.ids[i]], bins[group.ids[j]]);
            rows.push({ condition: group.name, a: (HD.sample(group.ids[i]) || {}).name, b: (HD.sample(group.ids[j]) || {}).name, r: pair.r, cells: pair.n });
          }
        }
      });
      correlation.rows = rows;
      correlation.key = correlationKey();
    } catch (error) {
      correlation.error = "Could not compute: " + HD.errorText(error);
    } finally {
      correlation.busy = false;
      drawSamples();
    }
  }

  function pearson(left, right) {
    var keys = new Set();
    left.forEach(function (_, key) { keys.add(key); });
    right.forEach(function (_, key) { keys.add(key); });
    var n = 0, sx = 0, sy = 0, sxx = 0, syy = 0, sxy = 0;
    keys.forEach(function (key) {
      var x = Math.log1p(left.get(key) || 0);
      var y = Math.log1p(right.get(key) || 0);
      n += 1; sx += x; sy += y; sxx += x * x; syy += y * y; sxy += x * y;
    });
    if (n < 3) return { r: null, n: n };
    var cov = sxy - sx * sy / n;
    var vx = sxx - sx * sx / n;
    var vy = syy - sy * sy / n;
    return { r: vx > 0 && vy > 0 ? cov / Math.sqrt(vx * vy) : null, n: n };
  }

  HD.on("stats", drawSamples);
  HD.on("project", function () { if (state.page === "qc") onShow(); });
  HD.on("gene", drawSamples);
  HD.on("project-opened", function () { correlation = { key: null, rows: null, busy: false, error: null, binSize: 100 }; });

  HD.pages.qc = { render: render, onShow: onShow };
}());
