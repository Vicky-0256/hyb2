/*
 * The embedded analysis pages (web/app.js mounted with shell:false and a
 * DesktopProvider). One mount is shared: its host element moves into the
 * slot of whichever desktop page shows it (③ Explore, ⑥ Report, the QC
 * overview and the Compare fallback). The desktop shell owns navigation and
 * the selection; "Fold this region" goes to ⑤ Structure.
 */
(function () {
  "use strict";

  var HD = window.HD;
  var h = HD.h;
  var state = HD.state;
  var provider = null;
  var view = null;
  var mounting = null;
  var host = h("div", { className: "d-workspace-host app-shell desktop-integrated-analysis" });
  var currentSlot = null;
  var EXPLORE_TABS = [
    { id: "interactions", label: "Interactions" },
    { id: "contact-map", label: "Contact map" },
    { id: "viewpoint", label: "Viewpoint" },
    { id: "region", label: "Region" }
  ];
  // Web page -> desktop page that hosts it.
  var HOME = { overview: "qc", interactions: "explore", "contact-map": "explore", viewpoint: "explore", region: "explore", structure: "explore", compare: "compare", report: "report" };

  function getProvider() {
    if (!provider) {
      provider = new window.Hyb2DesktopProvider.DesktopProvider({ query: HD.query, readSequence: readSequence });
      provider.setProject({ samples: state.samples, references: state.references });
    }
    return provider;
  }

  // Reference sequence for Region / sequence views. Needs a Python
  // `read_reference_sequence` project operation; copied references fall back
  // to project_read_input. Returns null when neither is available.
  async function readSequence(referenceId, name) {
    try {
      var result = await HD.invoke("project_op", { request: { operation: "read_reference_sequence", path: state.projectPath, reference_id: referenceId, name: name } });
      if (result && typeof result.sequence === "string") return { sequence: result.sequence };
    } catch (_) { /* operation not available yet */ }
    var owner = state.references.find(function (reference) {
      return (reference.sequences || []).some(function (sequence) { return sequence.name === name; });
    });
    if (!owner || !owner.path || /^([\\/]|[A-Za-z]:)/.test(owner.path)) return null;
    try {
      var read = await HD.invoke("project_read_input", { request: { path: state.projectPath, relative: owner.path } });
      var fasta = window.Hyb2Data.parseFasta(read.input.content);
      var entry = window.Hyb2Data.findFastaEntry(fasta, name) || (fasta.sequences || [])[0];
      return entry ? { sequence: entry.sequence } : null;
    } catch (_) {
      return null;
    }
  }

  function selection() {
    return { datasetIds: HD.selectionIds() };
  }

  async function ensureMounted(page) {
    if (view) return view;
    if (mounting) return mounting;
    mounting = (async function () {
      await window.Hyb2Web.ready();
      view = window.Hyb2Web.mount(host, {
        provider: getProvider(),
        shell: false,
        selection: selection(),
        page: page,
        onNavigate: onNavigate,
        onRequestFold: onRequestFold,
        onError: function (error) { if (window.console) window.console.warn("analysis:", error); }
      });
      if (state.analysisSnapshot && window.Hyb2Web.restoreSession) {
        var snapshot = Object.assign({}, state.analysisSnapshot);
        delete snapshot.route;
        delete snapshot.theme;
        window.Hyb2Web.restoreSession(snapshot);
      }
      return view;
    }());
    try { return await mounting; } finally { mounting = null; }
  }

  function onNavigate(detail) {
    var page = HOME[detail.pageId] || "explore";
    if (page === "explore" && detail.pageId !== state.sub.explore) {
      state.sub.explore = detail.pageId;
      renderTabs();
      HD.saveUiState();
    }
    if (detail.reason !== "host" && page !== state.page) HD.navigate(page, { fromWorkspace: true });
  }

  function onRequestFold(request) {
    var region = request && request.region;
    if (!region) return;
    var length = HD.geneLength ? HD.geneLength(region.rna) : null;
    var clamp = function (span) {
      return { start: Math.max(1, span.start), end: length ? Math.min(length, span.end) : span.end };
    };
    var windows = [clamp(region)];
    if (request.partnerRegion && request.partnerRegion.rna === region.rna) windows.push(clamp(request.partnerRegion));
    HD.navigate("structure", { prefill: { gene: region.rna, windows: windows, source: request.source, selectionKey: HD.selectionKey(state.selection) } });
  }

  // Places the shared host into `slot` and shows web page `pageId`.
  async function show(slot, pageId) {
    if (currentSlot !== slot) {
      slot.appendChild(host);
      currentSlot = slot;
    }
    var ids = HD.selectionIds();
    if (!ids.length) {
      host.hidden = true;
      return null;
    }
    host.hidden = false;
    if (pageId !== "compare" && provider && provider.groups) provider.setCompareGroups(null);
    var mounted = await ensureMounted(pageId);
    var current = window.Hyb2Provider.normaliseSelection(mounted.getSelection());
    if (!window.Hyb2Provider.sameSelection(current, selection())) await mounted.setSelection(selection());
    if (mounted.getPageId() !== pageId) mounted.navigate(pageId);
    return mounted;
  }

  // ---------------------------------------------------------------- Explore page

  var exploreRoot = null;
  var tabsHost = null;
  var exploreSlot = null;
  var exploreEmpty = null;

  function renderExplore(container) {
    exploreRoot = container;
    tabsHost = h("div", { className: "d-tabs", role: "tablist", "aria-label": "Explore views" });
    exploreEmpty = h("div", { className: "d-empty d-card" });
    exploreSlot = h("div", { className: "d-slot" });
    container.appendChild(h("div", { className: "d-page-head d-page-head-tight" }, [
      h("div", {}, [h("h1", { className: "d-h1", text: "Explore" }), h("p", { className: "d-muted d-selection-note" })]),
      tabsHost
    ]));
    container.appendChild(exploreEmpty);
    container.appendChild(exploreSlot);
    renderTabs();
  }
  function renderTabs() {
    if (!tabsHost) return;
    HD.clear(tabsHost);
    EXPLORE_TABS.concat(state.sub.explore === "structure" ? [{ id: "structure", label: "Record fold" }] : []).forEach(function (tab) {
      var active = state.sub.explore === tab.id;
      tabsHost.appendChild(h("button", { type: "button", role: "tab", className: "d-tab" + (active ? " d-tab-active" : ""), "aria-selected": active ? "true" : "false", text: tab.label, on: { click: function () {
        state.sub.explore = tab.id;
        HD.saveUiState();
        renderTabs();
        showExplore();
      } } }));
    });
  }
  function selectionNote(root) {
    var note = root && root.querySelector(".d-selection-note");
    if (note) note.textContent = HD.selectionIds().length ? "Selection: " + HD.selectionLabel() + " · change it in the top bar." : "";
  }
  function emptyState(node, message) {
    HD.clear(node);
    node.hidden = !message;
    if (!message) return;
    node.appendChild(h("p", { text: message }));
    node.appendChild(h("button", { type: "button", className: "d-btn d-btn-primary", text: "Go to Data", on: { click: function () { HD.navigate("data"); } } }));
  }
  function showExplore() {
    if (!exploreRoot) return;
    renderTabs();
    selectionNote(exploreRoot);
    var ids = HD.selectionIds();
    emptyState(exploreEmpty, ids.length ? "" : "No analysed sample yet. Run a sample on the Data page; it appears here when its analysis index is built.");
    var page = EXPLORE_TABS.some(function (tab) { return tab.id === state.sub.explore; }) || state.sub.explore === "structure" ? state.sub.explore : "contact-map";
    show(exploreSlot, page).catch(reportError);
  }

  // ---------------------------------------------------------------- Report page

  var reportRoot = null;
  var reportSlot = null;
  var reportEmpty = null;
  function renderReport(container) {
    reportRoot = container;
    reportEmpty = h("div", { className: "d-empty d-card" });
    reportSlot = h("div", { className: "d-slot" });
    container.appendChild(h("div", { className: "d-page-head d-page-head-tight" }, [
      h("div", {}, [h("h1", { className: "d-h1", text: "Report" }), h("p", { className: "d-muted d-selection-note" })])
    ]));
    container.appendChild(reportEmpty);
    container.appendChild(reportSlot);
  }
  function showReport() {
    selectionNote(reportRoot);
    emptyState(reportEmpty, HD.selectionIds().length ? "" : "Run at least one sample to build a report.");
    show(reportSlot, "report").catch(reportError);
  }

  function reportError(error) {
    HD.toast("Analysis view: " + HD.errorText(error), "error");
  }

  // ---------------------------------------------------------------- wiring

  HD.on("selection", function () {
    if (!view) return;
    view.setSelection(selection()).catch(reportError);
    selectionNote(exploreRoot);
    selectionNote(reportRoot);
  });
  HD.on("project", function (detail) {
    if (!provider) return;
    provider.setProject({ samples: state.samples, references: state.references });
    if (view && detail && detail.readyChanged) {
      view.setSelection(selection()).then(function () { return view.refresh(); }).catch(reportError);
    }
    if (state.page === "explore") showExplore();
    if (state.page === "report") showReport();
  });
  HD.on("project-opened", function () {
    if (view) view.destroy();
    view = null;
    provider = null;
    currentSlot = null;
  });
  HD.on("project-closed", function () {
    if (view) view.destroy();
    view = null;
    provider = null;
    currentSlot = null;
  });
  window.addEventListener("hyb2:session-change", function (event) {
    if (!state.projectPath || !event.detail || typeof event.detail !== "object") return;
    var snapshot = Object.assign({}, event.detail);
    delete snapshot.theme;
    if (JSON.stringify(snapshot) === JSON.stringify(state.analysisSnapshot)) return;
    state.analysisSnapshot = snapshot;
    HD.saveUiState();
  });

  HD.workspace = {
    provider: getProvider,
    show: show,
    view: function () { return view; },
    ensureMounted: ensureMounted
  };
  HD.pages.explore = { render: renderExplore, onShow: showExplore };
  HD.pages.report = { render: renderReport, onShow: showReport };
}());
