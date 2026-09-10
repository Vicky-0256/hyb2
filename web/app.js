(function () {
  "use strict";

  const app = document.getElementById("app");
  const numberFormatter = new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 });
  const percentFormatter = new Intl.NumberFormat("en-US", { style: "percent", maximumFractionDigits: 1 });
  const exampleAssets = {
    hyb: {
      url: "./assets/examples/ZIKV_1-10807_example.hyb",
      fileName: "ZIKV_1-10807_example.hyb",
      type: "text/plain"
    },
    fasta: {
      url: "./assets/examples/ZIKV_1-10807.fasta",
      fileName: "ZIKV_1-10807.fasta",
      type: "text/plain"
    }
  };

  const state = {
    activePage: "landing",
    dialog: null,
    file: null,
    summary: null,
    records: [],
    fasta: null,
    filters: null,
    interactionResults: [],
    interactionScrollTop: 0,
    selectedRecordIndex: null,
    contact: null,
    region: null,
    viewpoint: null,
    comparison: null,
    structure: null,
    worker: null,
    structureWorker: null,
    comparisonParsers: new Set(),
    comparisonLoadSession: null,
    parseSession: null,
    fastaLoadSession: null,
    exampleLoadSession: null,
    landingFastaFile: null,
    loading: null,
    error: null,
    theme: window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light",
    density: "comfortable",
    tweaksOpen: false,
    toastTimer: null,
    pointerActionPending: false,
    pendingCommittedInputRender: false
  };

  const analysisPages = [
    { id: "overview", label: "Overview" },
    { id: "interactions", label: "Interactions" },
    { id: "contact-map", label: "Contact Map" },
    { id: "viewpoint", label: "Viewpoint" },
    { id: "region", label: "Region Explorer" },
    { id: "compare", label: "Compare" },
    { id: "structure", label: "RNA Structure" }
  ];

  app.addEventListener("pointerdown", handlePointerDown);
  app.addEventListener("click", handleClick);
  app.addEventListener("click", finishPointerAction);
  app.addEventListener("pointercancel", finishPointerAction);
  app.addEventListener("input", handleInput);
  app.addEventListener("change", handleChange);
  app.addEventListener("keydown", handleKeydown);
  app.addEventListener("dragover", handleDragOver);
  app.addEventListener("dragleave", handleDragLeave);
  app.addEventListener("drop", handleDrop);
  window.addEventListener("hashchange", handleHashChange);

  syncRoute();
  render();

  function syncRoute() {
    const hash = window.location.hash.replace(/^#\/?/, "");
    const parts = hash.split("?");
    const route = parts[0];
    const query = new URLSearchParams(parts[1] || "");

    if (!state.summary) {
      state.activePage = "landing";
      return;
    }

    if (!route || route === "workspace/overview") {
      state.activePage = "overview";
      return;
    }

    if (route.indexOf("workspace/") === 0) {
      const candidate = route.slice("workspace/".length);
      state.activePage = analysisPages.some(function (page) { return page.id === candidate; })
        ? candidate
        : "overview";
      if (state.activePage === "interactions" && state.filters) {
        state.filters.rna = query.get("rna") || "";
        state.filters.partner = query.get("partner") || "";
      }
      return;
    }

    state.activePage = ["methods", "file-format", "privacy", "validation"].indexOf(route) > -1
      ? route
      : "overview";
  }

  function handleHashChange() {
    const previousPage = state.activePage;
    syncRoute();
    render();
    if (previousPage !== state.activePage) {
      window.scrollTo(0, 0);
    }
  }

  function render() {
    app.innerHTML = state.summary ? renderWorkspace() : renderLanding();
    document.documentElement.dataset.theme = state.theme;
    app.dataset.density = state.density;
    const themeColor = document.querySelector('meta[name="theme-color"]');
    if (themeColor) {
      themeColor.setAttribute("content", state.theme === "dark" ? "#132c47" : "#f5f8fa");
    }
    document.title = state.summary
      ? pageTitle(state.activePage) + " · HYB2 Web Lite"
      : "HYB2 Web Lite";

    if (state.summary && window.Hyb2UI) {
      window.Hyb2UI.afterRender(state, featureApi());
    }
  }

  function renderLanding() {
    return [
      '<div class="landing">',
      '  <header class="landing-header">',
      renderBrand(),
      '    <div class="header-actions">',
      '      <button class="local-chip" type="button" data-action="open-privacy">Local-only processing</button>',
      '      <button class="text-button" type="button" data-action="open-help">Help</button>',
      '      <button class="icon-button" type="button" data-action="toggle-theme" aria-label="Toggle color theme">' + themeGlyph() + "</button>",
      "    </div>",
      "  </header>",
      '  <main id="main-content" class="landing-main">',
      '    <section class="landing-copy" aria-labelledby="landing-title">',
      '      <span class="eyebrow">RNA interaction analysis · browser-local</span>',
      '      <h1 id="landing-title">Open your HYB file.<br><span>Explore locally.</span></h1>',
      '      <p class="landing-lede">Inspect RNA–RNA interactions, validate records, and prepare contact-map analysis without sending your research data to a server.</p>',
      '      <ul class="promise-list">',
      "        <li>No account or server upload required</li>",
      "        <li>Files remain in this browser session</li>",
      "        <li>Built for HYB-format interaction records</li>",
      "      </ul>",
      "    </section>",
      '    <section class="landing-panel" aria-label="Open a HYB file">',
      renderFilePanel(),
      "    </section>",
      "  </main>",
      renderDialog(),
      '  <div id="toast" class="toast" role="status" aria-live="polite"></div>',
      "</div>"
    ].join("");
  }

  function renderBrand() {
    const logoAsset = state.theme === "dark"
      ? "./assets/hyb2-logo-dark.png"
      : "./assets/hyb2-logo.png";
    return [
      '<a class="brand" href="#/" aria-label="HYB2 Web Lite home">',
      '  <span class="brand-mark"><img src="' + logoAsset + '" alt="HYB2"></span>',
      "  <span>",
      '    <span class="brand-name">Web</span>',
      '    <span class="brand-subtitle">Lite</span>',
      "  </span>",
      "</a>"
    ].join("");
  }

  function renderFilePanel() {
    if (state.loading) {
      return renderLoadingPanel();
    }

    if (state.error) {
      return [
        '<div class="file-card"><div class="file-card-inner load-state">',
        '  <div class="parse-error"><strong>' + escapeHtml(state.error.title) + "</strong><br>" + escapeHtml(state.error.message) + "</div>",
        '  <div class="dialog-actions">',
        '    <button class="button button-secondary" type="button" data-action="reset-file">Choose another file</button>',
        "  </div>",
        "</div></div>"
      ].join("");
    }

    const landingFastaFile = state.landingFastaFile;
    return [
      '<div class="file-card"><div class="file-card-inner">',
      '  <div class="file-card-header">',
      "    <div>",
      "      <h2>Start with interaction data</h2>",
      "      <p>Choose one HYB file to begin a local analysis session.</p>",
      "    </div>",
      '    <span class="v0-chip">Local-only</span>',
      "  </div>",
      '  <section class="example-strip" aria-labelledby="example-title">',
      '    <div class="example-strip-heading">',
      '      <div><span class="example-kicker">Included example</span><h3 id="example-title">Try the ZIKV interaction dataset</h3></div>',
      '      <span class="example-badge">10,000-record subset</span>',
      "    </div>",
      '    <p class="example-copy">Load a browser-sized subset of 1-Livefire1_virus-virus.hyb and the matching ZIKV_1-10807 reference FASTA.</p>',
      '    <div class="example-files" aria-label="Included example files">',
      '      <div class="example-file"><span class="example-file-kind">HYB</span><code>ZIKV_1-10807_example.hyb</code></div>',
      '      <div class="example-file"><span class="example-file-kind">FASTA</span><code>ZIKV_1-10807.fasta</code></div>',
      "    </div>",
      '    <div class="example-actions">',
      '      <button class="button" type="button" data-action="load-example">Try example</button>',
      '      <a class="quiet-button example-link" href="./assets/examples/ZIKV_1-10807_example.hyb" download>Download HYB</a>',
      '      <a class="quiet-button example-link" href="./assets/examples/ZIKV_1-10807.fasta" download>Download FASTA</a>',
      "    </div>",
      "  </section>",
      '  <input id="hyb-file-input" class="visually-hidden" type="file" accept=".hyb,.txt,text/plain" aria-label="Choose a HYB file">',
      '  <input id="fasta-file-input" class="visually-hidden" type="file" accept=".fa,.fasta,.fna,.fas,text/plain" aria-label="Choose an optional reference FASTA file">',
      '  <div class="dropzone" data-dropzone role="button" tabindex="0" aria-describedby="file-input-hint">',
      '    <div class="dropzone-content">',
      '      <span class="dropzone-kicker">HYB input</span>',
      '      <h3 class="dropzone-title">Drop a .hyb file here</h3>',
      '      <p class="dropzone-copy">or choose a file from this device</p>',
      '      <button class="button" type="button" data-action="choose-hyb">Choose HYB file</button>',
      '      <span id="file-input-hint" class="dropzone-hint">Accepts .hyb and tab-delimited .txt files</span>',
      "    </div>",
      "  </div>",
      '  <section class="reference-upload" aria-labelledby="reference-upload-title">',
      '    <div class="reference-upload-copy">',
      '      <span class="reference-upload-kicker">Optional reference</span>',
      '      <h3 id="reference-upload-title">Add a FASTA for RNA structure</h3>',
      '      <p>Choose it now or add it later from the workspace.</p>',
      "    </div>",
      '    <div class="reference-upload-actions">',
      '      <span class="reference-file-name' + (landingFastaFile ? "" : " is-empty") + '" title="' + escapeAttribute(landingFastaFile ? landingFastaFile.name : "No FASTA selected") + '">' + escapeHtml(landingFastaFile ? landingFastaFile.name : "No FASTA selected") + "</span>",
      '      <button class="quiet-button" type="button" data-action="choose-fasta">' + (landingFastaFile ? "Replace FASTA" : "Choose FASTA") + "</button>",
      landingFastaFile ? '      <button class="quiet-button" type="button" data-action="remove-landing-fasta">Remove</button>' : "",
      "    </div>",
      "  </section>",
      '  <p class="privacy-note">Files are read and processed locally in this browser. This page has no upload endpoint.</p>',
      '  <p class="card-footnote">Recommended working size: up to 50 MB. Larger files are streamed to a dedicated worker to keep the interface responsive.</p>',
      "</div></div>"
    ].join("");
  }

  function renderLoadingPanel() {
    const load = state.loading;
    const percent = Math.max(0, Math.min(100, load.percent || 0));
    const stages = [
      "Loading example files",
      "Reading file",
      "Detecting format",
      "Parsing HYB records",
      "Building summary"
    ];
    const stageIndex = load.stage === "Loading example files" ? 0 : load.stage === "Reading file" ? 1 : load.stage === "Detecting format" ? 2 : load.stage === "Building summary" ? 4 : 3;

    return [
      '<div class="file-card"><div class="file-card-inner load-state">',
      '  <p class="load-file-name" title="' + escapeAttribute(load.fileName) + '">' + escapeHtml(load.fileName) + "</p>",
      '  <p class="load-status">' + escapeHtml(load.stage) + " · " + formatNumber(load.parsedRecords || 0) + " valid records found</p>",
      '  <div class="progress-bar" role="progressbar" aria-label="Local HYB parsing progress" aria-valuemin="0" aria-valuemax="100" aria-valuenow="' + percent + '"><span style="width:' + percent + '%"></span></div>',
      '  <ol class="parse-steps">',
      stages.map(function (stage, index) {
        const className = index < stageIndex ? " is-complete" : index === stageIndex ? " is-current" : "";
        return '<li class="parse-step' + className + '">' + stage + "</li>";
      }).join(""),
      "  </ol>",
      '  <p class="card-footnote" aria-live="polite">' + (load.totalBytes ? formatBytes(load.processedBytes || 0) + " of " + formatBytes(load.totalBytes || 0) + " read locally" : "Preparing the included example locally") + "</p>",
      "</div></div>"
    ].join("");
  }

  function renderWorkspace() {
    return [
      '<div class="workspace">',
      renderWorkspaceHeader(),
      renderSidebar(),
      '  <main id="main-content" class="workspace-main">',
      '    <div class="content-wrap">',
      renderPage(),
      "    </div>",
      "  </main>",
      renderMobileNav(),
      renderTweaks(),
      renderDialog(),
      '  <input id="hyb-file-input" class="visually-hidden" type="file" accept=".hyb,.txt,text/plain" aria-label="Replace HYB file">',
      '  <input id="comparison-file-input" class="visually-hidden" type="file" accept=".hyb,.txt,text/plain" multiple aria-label="Add HYB datasets for comparison">',
      '  <input id="fasta-file-input" class="visually-hidden" type="file" accept=".fa,.fasta,.fna,.fas,text/plain" aria-label="Choose a reference FASTA file">',
      '  <div id="toast" class="toast" role="status" aria-live="polite"></div>',
      "</div>"
    ].join("");
  }

  function renderWorkspaceHeader() {
    const summary = state.summary;
    return [
      '  <header class="workspace-header">',
      '    <div class="workspace-header-left">',
      renderBrand(),
      '      <div class="header-file">',
      '        <span class="header-file-name" title="' + escapeAttribute(summary.fileName) + '">' + escapeHtml(summary.fileName) + "</span>",
      '        <span class="ready-chip">Ready</span>',
      "      </div>",
      "    </div>",
      '    <div class="toolbar-actions">',
      '      <button class="local-chip" type="button" data-action="open-privacy">Local-only processing</button>',
      '      <button class="text-button" type="button" data-action="open-files">Files</button>',
      '      <button class="text-button" type="button" data-action="open-help">Help</button>',
      '      <button class="icon-button" type="button" data-action="toggle-theme" aria-label="Toggle color theme">' + themeGlyph() + "</button>",
      "    </div>",
      "  </header>"
    ].join("");
  }

  function renderTweaks() {
    if (!state.tweaksOpen) {
      return '<button class="tweaks-trigger" type="button" data-action="toggle-tweaks">Tweaks</button>';
    }

    return [
      '<aside class="tweaks-panel" aria-label="Display tweaks">',
      '<div class="card-title-row"><h2>Tweaks</h2><button class="icon-button" type="button" data-action="toggle-tweaks" aria-label="Close tweaks">×</button></div>',
      '<label class="control-field"><span>Appearance</span><button class="quiet-button tweaks-choice" type="button" data-action="toggle-theme">' + (state.theme === "light" ? "Switch to dark" : "Switch to light") + "</button></label>",
      '<label class="control-field"><span>Data density</span><select id="density-input"><option value="comfortable"' + (state.density === "comfortable" ? " selected" : "") + '>Comfortable</option><option value="compact"' + (state.density === "compact" ? " selected" : "") + ">Compact</option></select></label>",
      "</aside>"
    ].join("");
  }

  function renderSidebar() {
    const summary = state.summary;
    return [
      '  <aside class="sidebar" aria-label="Workspace navigation">',
      '    <div class="sidebar-file">',
      '      <span class="sidebar-label">Data</span>',
      '      <span class="sidebar-file-name" title="' + escapeAttribute(summary.fileName) + '">' + escapeHtml(summary.fileName) + "</span>",
      '      <span class="sidebar-file-info">' + formatBytes(summary.fileSize) + " · " + formatNumber(summary.validRecords) + " records</span>",
      "    </div>",
      '    <nav class="sidebar-nav" aria-label="Analysis">',
      '      <span class="nav-label">Analysis</span>',
      analysisPages.map(renderNavButton).join(""),
      "    </nav>",
      '    <nav class="sidebar-nav" aria-label="Data management">',
      '      <span class="nav-label">Data management</span>',
      '      <button class="nav-button" type="button" data-action="open-files">Files</button>',
      renderNavButton({ id: "validation", label: "Validation" }),
      renderNavButton({ id: "methods", label: "Methods" }),
      renderNavButton({ id: "file-format", label: "File format" }),
      renderNavButton({ id: "privacy", label: "Privacy" }),
      "    </nav>",
      '    <div class="sidebar-footer">',
      '      <div class="local-status"><strong>Files stay local</strong><span>Stored only in this tab’s memory.</span></div>',
      '      <button class="clear-session quiet-button" type="button" data-action="clear-session">Clear session</button>',
      "    </div>",
      "  </aside>"
    ].join("");
  }

  function renderMobileNav() {
    return [
      '<nav class="mobile-nav" aria-label="Analysis">',
      analysisPages.map(renderNavButton).join(""),
      "</nav>"
    ].join("");
  }

  function renderNavButton(page) {
    const active = state.activePage === page.id ? " is-active" : "";
    return '<button class="nav-button' + active + '" type="button" data-action="navigate" data-page="' + page.id + '">' + escapeHtml(page.label) + "</button>";
  }

  function renderPage() {
    if (state.activePage === "overview") {
      return window.Hyb2Overview.render(state);
    }

    if (state.activePage === "validation") {
      return window.Hyb2Overview.renderValidation(state);
    }

    if (state.activePage === "methods") {
      return window.Hyb2Overview.renderMethods();
    }

    if (state.activePage === "file-format") {
      return window.Hyb2Overview.renderFileFormat();
    }

    if (state.activePage === "privacy") {
      return window.Hyb2Overview.renderPrivacy();
    }

    if (state.activePage === "interactions") {
      return window.Hyb2Pages.renderInteractions(state);
    }

    if (state.activePage === "contact-map") {
      return window.Hyb2Pages.renderContactMap(state);
    }

    if (state.activePage === "viewpoint") {
      return window.Hyb2Pages.renderViewpoint(state);
    }

    if (state.activePage === "region") {
      return window.Hyb2Pages.renderRegion(state);
    }

    if (state.activePage === "compare") {
      return window.Hyb2Pages.renderComparison(state);
    }

    if (state.activePage === "structure") {
      return window.Hyb2Pages.renderStructure(state);
    }

    return window.Hyb2Overview.render(state);
  }

  function renderDialog() {
    if (!state.dialog) {
      return "";
    }

    if (state.dialog === "privacy") {
      return [
        '<div class="pop-layer">',
        '  <section class="dialog" role="dialog" aria-modal="true" aria-labelledby="privacy-dialog-title" data-dialog>',
        '    <h2 id="privacy-dialog-title">Your data stays on this device</h2>',
        "    <p>HYB2 Web Lite is static client-side software. It has no upload flow.</p>",
        "    <ul>",
        "      <li>HYB files are read locally by this tab.</li>",
        "      <li>Parsing happens in a dedicated browser worker.</li>",
        "      <li>Results are held in memory and are not saved to a server.</li>",
        "      <li>Refreshing or clearing the session removes loaded data.</li>",
        "    </ul>",
        '    <div class="dialog-actions"><button class="button" type="button" data-action="close-dialog">Close</button></div>',
        "  </section>",
        "</div>"
      ].join("");
    }

    if (state.dialog === "clear") {
      return [
        '<div class="pop-layer">',
        '  <section class="dialog" role="dialog" aria-modal="true" aria-labelledby="clear-dialog-title" data-dialog>',
        '    <h2 id="clear-dialog-title">Clear this analysis?</h2>',
        "    <p>This removes the HYB file, validation report, and generated results from memory in this browser tab.</p>",
        '    <div class="dialog-actions"><button class="button button-secondary" type="button" data-action="close-dialog">Cancel</button><button class="button button-danger" type="button" data-action="confirm-clear">Clear analysis</button></div>',
        "  </section>",
        "</div>"
      ].join("");
    }

    if (state.dialog === "files" && state.summary) {
      return [
        '<div class="pop-layer">',
        '  <section class="dialog files-dialog" role="dialog" aria-modal="true" aria-labelledby="files-dialog-title" data-dialog>',
        window.Hyb2Pages.getFilesDialogBody(state),
        '    <div class="dialog-actions"><button class="button" type="button" data-action="close-dialog">Close</button></div>',
        "  </section>",
        "</div>"
      ].join("");
    }

    return [
      '<div class="pop-layer">',
      '  <section class="dialog" role="dialog" aria-modal="true" aria-labelledby="help-dialog-title" data-dialog>',
      '    <h2 id="help-dialog-title">HYB2 Web Lite</h2>',
      "    <p>Choose a local .hyb or tab-delimited .txt file. HYB parsing, filtering, contact maps, region exploration, FASTA mapping, and exports run without a server connection.</p>",
      '    <div class="dialog-actions"><button class="button" type="button" data-action="close-dialog">Close</button></div>',
      "  </section>",
      "</div>"
    ].join("");
  }

  function handleClick(event) {
    const dialogLayer = event.target.closest(".pop-layer");
    if (dialogLayer && event.target === dialogLayer) {
      state.dialog = null;
      render();
      return;
    }

    const featureNode = event.target.closest("[data-feature-action]");
    if (featureNode && window.Hyb2UI.handleAction(state, featureNode, featureApi())) {
      return;
    }

    const actionNode = event.target.closest("[data-action]");
    if (!actionNode) {
      return;
    }

    const action = actionNode.dataset.action;

    if (action === "choose-hyb") {
      const input = document.getElementById("hyb-file-input");
      if (input) {
        input.click();
      }
      return;
    }

    if (action === "load-example") {
      loadExample();
      return;
    }

    if (action === "choose-fasta") {
      const input = document.getElementById("fasta-file-input");
      if (input) {
        input.click();
      }
      return;
    }

    if (action === "remove-landing-fasta") {
      state.landingFastaFile = null;
      render();
      return;
    }

    if (action === "open-privacy") {
      state.dialog = "privacy";
      render();
      return;
    }

    if (action === "open-help") {
      state.dialog = "help";
      render();
      return;
    }

    if (action === "open-files") {
      state.dialog = "files";
      render();
      return;
    }

    if (action === "close-dialog") {
      state.dialog = null;
      render();
      return;
    }

    if (action === "toggle-theme") {
      state.theme = state.theme === "light" ? "dark" : "light";
      render();
      return;
    }

    if (action === "toggle-tweaks") {
      state.tweaksOpen = !state.tweaksOpen;
      render();
      return;
    }

    if (action === "navigate") {
      navigate(actionNode.dataset.page);
      return;
    }

    if (action === "download-summary") {
      downloadSummary();
      return;
    }

    if (action === "download-rna-counts") {
      downloadRnaCounts();
      return;
    }

    if (action === "download-rna-pairs") {
      downloadRnaPairs();
      return;
    }

    if (action === "download-validation") {
      downloadValidationReport();
      return;
    }

    if (action === "clear-session") {
      state.dialog = "clear";
      render();
      return;
    }

    if (action === "confirm-clear") {
      clearSession();
      return;
    }

    if (action === "reset-file") {
      resetToLanding();
      return;
    }

    if (action === "show-upcoming") {
      showToast(actionNode.dataset.target + " is outside this v0 preview.");
    }
  }

  function handlePointerDown(event) {
    state.pointerActionPending = !!event.target.closest("button, a, [data-feature-action], [data-action]");
  }

  function finishPointerAction(event) {
    if (event && event.target && event.target.matches && event.target.matches('input[type="file"]')) {
      return;
    }
    const pendingRender = state.pendingCommittedInputRender;
    state.pointerActionPending = false;
    state.pendingCommittedInputRender = false;
    if (!pendingRender) {
      return;
    }
    const target = event && event.target && event.target.closest
      ? event.target.closest("[data-feature-action], [data-action]")
      : null;
    const action = target ? (target.dataset.featureAction || target.dataset.action || "") : "";
    if (["choose-hyb", "choose-fasta", "choose-comparison-files", "replace-hyb"].indexOf(action) === -1) {
      window.setTimeout(render, 0);
    }
  }

  function handleChange(event) {
    if (event.target.id === "density-input") {
      state.density = event.target.value === "compact" ? "compact" : "comfortable";
      render();
      return;
    }

    if (event.target.id === "hyb-file-input") {
      const file = event.target.files && event.target.files[0];
      if (file) {
        startParsing(file);
      }
      event.target.value = "";
      return;
    }

    if (event.target.id === "fasta-file-input") {
      const file = event.target.files && event.target.files[0];
      if (file) {
        if (state.summary) {
          loadFasta(file);
        } else {
          state.landingFastaFile = file;
          state.error = null;
          render();
        }
      }
      event.target.value = "";
      return;
    }

    if (event.target.id === "comparison-file-input") {
      const files = event.target.files ? Array.from(event.target.files) : [];
      if (files.length) {
        loadComparisonFiles(files);
      }
      event.target.value = "";
      return;
    }

    if (event.target.closest("[data-feature]")) {
      const api = featureApi();
      if (isTextEntry(event.target)) {
        api.render = function () { scheduleCommittedInputRender(event.target); };
      }
      window.Hyb2UI.handleChange(state, event.target, api);
    }
  }

  function handleInput(event) {
    const element = event.target;
    if (!element || !element.dataset || !element.dataset.feature) {
      return;
    }
    if (!isTextEntry(element)) {
      return;
    }
    window.Hyb2UI.handleInput(state, element);
  }

  function isTextEntry(element) {
    return element && (element.tagName === "TEXTAREA" || element.type === "text" || element.type === "search" || element.type === "number");
  }

  function scheduleCommittedInputRender(source) {
    const activeElement = document.activeElement;
    const actionHasFocus = activeElement && activeElement.closest && activeElement.closest("button, a, [data-feature-action], [data-action]");
    if (state.pointerActionPending || actionHasFocus) {
      state.pendingCommittedInputRender = true;
      return;
    }
    const focused = focusIdentity(activeElement);
    window.setTimeout(function () {
      render();
      restoreFocus(focused, source);
    }, 0);
  }

  function focusIdentity(element) {
    if (!element || !app.contains(element)) {
      return null;
    }
    return {
      id: element.id || "",
      feature: element.dataset && element.dataset.feature || "",
      key: element.dataset && element.dataset.key || ""
    };
  }

  function restoreFocus(identity, source) {
    let replacement = identity && identity.id ? document.getElementById(identity.id) : null;
    if (!replacement && identity && identity.feature) {
      replacement = Array.from(document.querySelectorAll("[data-feature]")).find(function (element) {
        return element.dataset.feature === identity.feature && element.dataset.key === identity.key;
      });
    }
    if (!replacement && source && source.dataset) {
      replacement = Array.from(document.querySelectorAll("[data-feature]")).find(function (element) {
        return element.dataset.feature === source.dataset.feature && element.dataset.key === source.dataset.key;
      });
    }
    if (replacement && typeof replacement.focus === "function") {
      replacement.focus();
    }
  }

  function handleKeydown(event) {
    const dropzone = event.target.closest("[data-dropzone]");

    if (dropzone && (event.key === "Enter" || event.key === " ")) {
      event.preventDefault();
      const input = document.getElementById("hyb-file-input");
      if (input) {
        input.click();
      }
    }

    if (event.key === "Escape" && state.dialog) {
      state.dialog = null;
      render();
    }
  }

  function handleDragOver(event) {
    const dropzone = event.target.closest("[data-dropzone]");
    if (!dropzone) {
      return;
    }

    event.preventDefault();
    dropzone.classList.add("is-dragging");
  }

  function handleDragLeave(event) {
    const dropzone = event.target.closest("[data-dropzone]");
    if (dropzone) {
      dropzone.classList.remove("is-dragging");
    }
  }

  function handleDrop(event) {
    const dropzone = event.target.closest("[data-dropzone]");
    if (!dropzone) {
      return;
    }

    event.preventDefault();
    dropzone.classList.remove("is-dragging");
    const file = event.dataTransfer && event.dataTransfer.files && event.dataTransfer.files[0];

    if (file) {
      startParsing(file);
    }
  }

  async function loadExample() {
    const session = {};
    state.exampleLoadSession = session;
    state.dialog = null;
    state.error = null;
    state.loading = {
      fileName: "Zika interaction example",
      totalBytes: 0,
      processedBytes: 0,
      parsedRecords: 0,
      percent: 0,
      stage: "Loading example files"
    };
    render();

    try {
      const responses = await Promise.all([
        fetch(exampleAssets.hyb.url, { cache: "no-store" }),
        fetch(exampleAssets.fasta.url, { cache: "no-store" })
      ]);
      if (state.exampleLoadSession !== session) {
        return;
      }
      responses.forEach(function (response) {
        if (!response.ok) {
          throw new Error("The example asset could not be read (HTTP " + response.status + ").");
        }
      });
      const blobs = await Promise.all(responses.map(function (response) { return response.blob(); }));
      if (state.exampleLoadSession !== session) {
        return;
      }
      const hybFile = createExampleFile(blobs[0], exampleAssets.hyb);
      const fastaFile = createExampleFile(blobs[1], exampleAssets.fasta);
      state.exampleLoadSession = null;
      startParsing(hybFile, { referenceFile: fastaFile });
    } catch (error) {
      if (state.exampleLoadSession !== session) {
        return;
      }
      state.exampleLoadSession = null;
      state.loading = null;
      state.error = {
        title: "The example could not be loaded",
        message: error && error.message ? error.message : "The included example files are unavailable."
      };
      render();
    }
  }

  function createExampleFile(blob, asset) {
    if (typeof File === "function") {
      return new File([blob], asset.fileName, { type: asset.type });
    }
    Object.defineProperty(blob, "name", { value: asset.fileName });
    return blob;
  }

  function startParsing(file, options) {
    terminateWorker();
    cancelComparisonLoads();
    invalidateFastaLoad();
    const parseOptions = options || {};
    const referenceFile = parseOptions.referenceFile || state.landingFastaFile || null;
    state.landingFastaFile = null;
    state.exampleLoadSession = null;
    const parseSession = {
      file: file,
      referenceFile: referenceFile
    };
    state.parseSession = parseSession;
    state.dialog = null;
    state.file = file;
    state.summary = null;
    state.records = [];
    state.filters = null;
    state.interactionResults = [];
    state.interactionScrollTop = 0;
    state.selectedRecordIndex = null;
    state.contact = null;
    state.region = null;
    state.viewpoint = null;
    state.comparison = null;
    state.structure = null;
    state.error = null;
    state.loading = {
      fileName: file.name || "Untitled file",
      totalBytes: file.size,
      processedBytes: 0,
      parsedRecords: 0,
      percent: 0,
      stage: "Reading file"
    };
    render();

    try {
      const worker = new Worker("./parser.worker.js");
      state.worker = worker;

      function parseSessionIsCurrent() {
        return state.parseSession === parseSession && state.file === file;
      }

      worker.onmessage = function (messageEvent) {
        if (!parseSessionIsCurrent()) {
          return;
        }
        const message = messageEvent.data || {};

        if (message.type === "progress") {
          const total = message.totalBytes || file.size || 1;
          state.loading = {
            fileName: file.name || "Untitled file",
            totalBytes: total,
            processedBytes: message.processedBytes || 0,
            parsedRecords: message.parsedRecords || 0,
            percent: Math.min(95, Math.round(((message.processedBytes || 0) / total) * 95)),
            stage: message.stage || "Parsing HYB records"
          };
          render();
          return;
        }

        if (message.type === "complete") {
          terminateWorker();
          const summary = message.summary;

          if (!summary || !summary.validRecords) {
            state.loading = null;
            state.error = {
              title: "This does not appear to be a valid HYB file",
              message: "No records with at least 15 tab-separated columns, RNA names, and valid RNA coordinates were found."
            };
            render();
            return;
          }

          state.loading = {
            fileName: file.name || "Untitled file",
            totalBytes: file.size,
            processedBytes: file.size,
            parsedRecords: summary.validRecords,
            percent: 100,
            stage: "Building summary"
          };
          render();

          window.setTimeout(function () {
            if (!parseSessionIsCurrent()) {
              return;
            }
            state.summary = summary;
            state.records = (message.records || []).map(function (record, index) {
              record.index = index;
              return record;
            });
            rebuildFastaMapping(summary);
            state.filters = window.Hyb2Data.defaultInteractionFilters();
            state.interactionResults = state.records;
            state.interactionScrollTop = 0;
            state.selectedRecordIndex = null;
            state.contact = window.Hyb2Data.defaultContactState(summary);
            state.region = window.Hyb2Data.defaultRegionState(summary, state.records);
            state.viewpoint = window.Hyb2Data.defaultViewpointState(summary, state.records);
            state.comparison = window.Hyb2Data.defaultComparisonState(summary, state.records);
            state.structure = window.Hyb2Data.defaultStructureState();
            state.structure.rna = state.region.rna;
            state.structure.start = state.region.start;
            state.structure.end = state.region.end;
            const structurePair = summary.pairCounts && summary.pairCounts[0];
            const secondStructureRna = structurePair ? structurePair.rnaTwo : state.region.rna;
            const secondStructureExtent = window.Hyb2Data.getRnaExtent(state.records, secondStructureRna);
            state.structure.secondRna = secondStructureRna;
            state.structure.secondStart = secondStructureExtent ? secondStructureExtent.min : state.region.start;
            state.structure.secondEnd = secondStructureExtent
              ? Math.min(secondStructureExtent.max, secondStructureExtent.min + 200)
              : state.region.end;
            state.loading = null;
            navigate("overview");
            if (parseSession.referenceFile) {
              loadFasta(parseSession.referenceFile);
            }
            digestFile(file).then(function (hash) {
              if (state.summary === summary) {
                state.summary.sha256 = hash;
                state.summary.sha256Unavailable = !hash;
                if (state.dialog === "files") {
                  render();
                }
              }
            }).catch(function () {
              if (state.summary === summary) {
                state.summary.sha256 = "";
                state.summary.sha256Unavailable = true;
                if (state.dialog === "files") {
                  render();
                }
              }
            });
          }, 220);
          return;
        }

        if (message.type === "error") {
          terminateWorker();
          state.loading = null;
          state.error = {
            title: "Local parsing could not finish",
            message: message.message || "The file could not be processed in this browser."
          };
          render();
        }
      };

      worker.onerror = function () {
        if (!parseSessionIsCurrent()) {
          return;
        }
        terminateWorker();
        state.loading = null;
        state.error = {
          title: "Local parsing could not finish",
          message: "The browser worker stopped unexpectedly. No file content was uploaded."
        };
        render();
      };

      worker.postMessage({ file: file });
    } catch (error) {
      if (state.parseSession !== parseSession || state.file !== file) {
        return;
      }
      state.loading = null;
      state.error = {
        title: "This browser cannot start the local parser",
        message: error && error.message ? error.message : "No data was uploaded."
      };
      render();
    }
  }

  function rebuildFastaMapping(summary) {
    if (!state.fasta) {
      return;
    }

    const previousMapping = state.fasta.mapping || {};
    const sequenceIds = new Set((state.fasta.sequences || []).map(function (entry) { return entry.id; }));
    const mapping = window.Hyb2Data.buildFastaMapping(summary.rnaNames || [], state.fasta.sequences || []);
    (summary.rnaNames || []).forEach(function (rna) {
      if (previousMapping[rna] && sequenceIds.has(previousMapping[rna])) {
        mapping[rna] = previousMapping[rna];
      }
    });
    state.fasta.mapping = mapping;
    const rnas = summary.rnaNames || [];
    if (rnas.indexOf(state.fasta.mappingEditorRna) === -1) {
      state.fasta.mappingEditorRna = rnas.find(function (rna) { return !mapping[rna]; }) || rnas[0] || "";
    }
    state.fasta.mappingEditorReference = mapping[state.fasta.mappingEditorRna] || "";
  }

  function navigate(page) {
    const isAnalysis = analysisPages.some(function (item) { return item.id === page; });
    const nextHash = isAnalysis ? "#/workspace/" + page : "#/" + page;
    setRouteHash(nextHash);
  }

  function setRouteHash(nextHash) {
    if (window.location.hash === nextHash) {
      syncRoute();
      render();
      window.scrollTo(0, 0);
      return;
    }
    window.location.hash = nextHash;
  }

  function featureApi() {
    return {
      render: render,
      navigate: navigate,
      chooseFasta: chooseFasta,
      chooseHyb: chooseHyb,
      chooseComparisonFiles: chooseComparisonFiles,
      removeFasta: removeFasta,
      clearSession: clearSession,
      download: downloadText,
      downloadBlob: downloadBlob,
      showToast: showToast,
      copyText: copyText,
      updateInteractionHash: updateInteractionHash
    };
  }

  function chooseHyb() {
    const input = document.getElementById("hyb-file-input");
    if (input) {
      input.click();
    }
  }

  function chooseComparisonFiles() {
    const input = document.getElementById("comparison-file-input");
    if (input) {
      input.click();
    }
  }

  function chooseFasta() {
    const input = document.getElementById("fasta-file-input");
    if (input) {
      input.click();
    }
  }

  function invalidateFastaLoad() {
    state.fastaLoadSession = null;
  }

  function removeFasta() {
    invalidateFastaLoad();
    state.fasta = null;
    if (state.viewpoint) {
      state.viewpoint.results = null;
      state.viewpoint.rangeMode = "coordinates";
    }
    if (state.structure && state.structure.source === "reference" && window.Hyb2Structure) {
      window.Hyb2Structure.reset(state, { clearConstraints: true });
    }
    render();
  }

  async function loadFasta(file) {
    const primaryFile = state.file;
    const primarySummary = state.summary;
    const fastaLoadSession = { file: file, primaryFile: primaryFile, primarySummary: primarySummary };
    state.fastaLoadSession = fastaLoadSession;

    function fastaLoadSessionIsCurrent() {
      return state.fastaLoadSession === fastaLoadSession &&
        state.file === primaryFile &&
        state.summary === primarySummary;
    }

    if (!primaryFile || !primarySummary) {
      invalidateFastaLoad();
      return;
    }

    let loadedFasta = null;
    try {
      showToast("Reading reference FASTA locally…");
      const text = await file.text();
      if (!fastaLoadSessionIsCurrent()) {
        return;
      }
      const sequences = window.Hyb2Data.parseFasta(text);

      if (!sequences.length) {
        throw new Error("No FASTA records with a header and sequence were found.");
      }

      const unsupportedEntries = sequences.filter(function (entry) {
        return entry.invalidCharacters && entry.invalidCharacters.length;
      });
      const unsupportedCharacters = Array.from(new Set(unsupportedEntries.flatMap(function (entry) {
        return entry.invalidCharacters;
      })));

      const mapping = window.Hyb2Data.buildFastaMapping(primarySummary.rnaNames || [], sequences);
      const mappingEditorRna = (primarySummary.rnaNames || []).find(function (rna) { return !mapping[rna]; }) || (primarySummary.rnaNames || [])[0] || "";
      loadedFasta = {
        fileName: file.name || "reference.fasta",
        fileSize: file.size,
        sequences: sequences,
        mapping: mapping,
        mappingEditorRna: mappingEditorRna,
        mappingEditorReference: mapping[mappingEditorRna] || "",
        unsupportedSequenceEntries: unsupportedEntries.length,
        unsupportedCharacters: unsupportedCharacters,
        sha256: "",
        sha256Unavailable: false
      };
      if (!fastaLoadSessionIsCurrent()) {
        return;
      }
      state.fasta = loadedFasta;
      if (state.viewpoint) {
        state.viewpoint.results = null;
        if (state.viewpoint.rangeMode === "reference" &&
            !window.Hyb2Data.findFastaEntry(loadedFasta, state.viewpoint.rna)) {
          state.viewpoint.rangeMode = "coordinates";
        }
      }
      if (state.structure && state.structure.source === "reference" && window.Hyb2Structure) {
        window.Hyb2Structure.reset(state, { clearConstraints: true });
      }
      render();
      showToast(unsupportedEntries.length
        ? "Reference FASTA loaded with unsupported symbols preserved; affected regions cannot be folded until corrected."
        : "Reference FASTA loaded locally.");
      const hash = await digestFile(file);
      if (state.fasta !== loadedFasta) {
        return;
      }
      loadedFasta.sha256 = hash;
      loadedFasta.sha256Unavailable = !hash;
      if (state.fastaLoadSession === fastaLoadSession) {
        invalidateFastaLoad();
      }
      if (state.dialog === "files") {
        render();
      }
    } catch (error) {
      if (loadedFasta) {
        if (state.fasta !== loadedFasta) {
          return;
        }
        loadedFasta.sha256 = "";
        loadedFasta.sha256Unavailable = true;
        if (state.fastaLoadSession === fastaLoadSession) {
          invalidateFastaLoad();
        }
        if (state.dialog === "files") {
          render();
        }
        showToast("Reference FASTA loaded, but its SHA-256 fingerprint is unavailable.");
        return;
      }
      if (!fastaLoadSessionIsCurrent()) {
        return;
      }
      invalidateFastaLoad();
      showToast(error && error.message ? error.message : "The FASTA file could not be parsed.");
    }
  }

  async function loadComparisonFiles(files) {
    const comparisonSession = state.comparison;
    const primaryFile = state.file;

    if (!comparisonSession || !files.length) {
      return;
    }

    cancelComparisonLoads();
    const comparisonLoadSession = {};
    state.comparisonLoadSession = comparisonLoadSession;

    function comparisonSessionIsCurrent() {
      return state.comparison === comparisonSession &&
        state.file === primaryFile &&
        state.comparisonLoadSession === comparisonLoadSession;
    }

    comparisonSession.error = "";
    comparisonSession.loading = { total: files.length, completed: 0, fileName: files[0].name || "HYB file" };
    comparisonSession.result = null;
    comparisonSession.selection = null;
    render();

    for (let index = 0; index < files.length; index += 1) {
      const file = files[index];
      if (!comparisonSessionIsCurrent()) {
        return;
      }
      comparisonSession.loading = { total: files.length, completed: index, fileName: file.name || "HYB file" };
      render();

      try {
        const parsed = await parseComparisonFile(file);
        if (!comparisonSessionIsCurrent()) {
          return;
        }
        if (!parsed.summary.validRecords) {
          throw new Error("No valid HYB records were found.");
        }
        let sha256 = "";
        try {
          sha256 = await digestFile(file);
        } catch (digestError) {
          sha256 = "";
        }
        if (!comparisonSessionIsCurrent()) {
          return;
        }
        parsed.summary.sha256 = sha256;
        parsed.summary.sha256Unavailable = !sha256;
        const datasetId = "comparison_" + Date.now().toString(36) + "_" + index;
        const records = parsed.records.map(function (record, recordIndex) {
          record.index = recordIndex;
          return record;
        });
        comparisonSession.datasets.push({
          id: datasetId,
          fileName: file.name || "comparison.hyb",
          fileSize: file.size,
          sha256: sha256 || null,
          summary: parsed.summary,
          records: records,
          condition: "B",
          label: file.name ? file.name.replace(/\.(hyb|txt)$/i, "") : "Dataset " + (comparisonSession.datasets.length + 1)
        });
        comparisonSession.result = null;
        comparisonSession.selection = null;
      } catch (error) {
        if (!comparisonSessionIsCurrent()) {
          return;
        }
        comparisonSession.error = "Could not add " + (file.name || "one dataset") + ": " + (error && error.message ? error.message : "local parsing failed.");
      }
    }

    if (!comparisonSessionIsCurrent()) {
      return;
    }
    comparisonSession.loading = null;
    comparisonSession.result = null;
    state.comparisonLoadSession = null;
    render();
    showToast("Comparison datasets were parsed locally.");
  }

  function parseComparisonFile(file) {
    return new Promise(function (resolve, reject) {
      let worker;
      let settled = false;
      const parser = {
        cancel: function () {
          if (settled) {
            return;
          }
          settled = true;
          if (worker) {
            worker.terminate();
          }
          state.comparisonParsers.delete(parser);
          const error = new Error("Comparison parsing was cancelled.");
          error.name = "AbortError";
          reject(error);
        }
      };

      function settle(callback, value) {
        if (settled) {
          return;
        }
        settled = true;
        if (worker) {
          worker.terminate();
        }
        state.comparisonParsers.delete(parser);
        callback(value);
      }

      try {
        worker = new Worker("./parser.worker.js");
      } catch (error) {
        settle(reject, error);
        return;
      }
      state.comparisonParsers.add(parser);

      worker.onmessage = function (event) {
        const message = event.data || {};
        if (message.type === "complete") {
          settle(resolve, { summary: message.summary, records: message.records || [] });
        }
        if (message.type === "error") {
          settle(reject, new Error(message.message || "The local parser could not read this file."));
        }
      };
      worker.onerror = function () {
        settle(reject, new Error("The local parser stopped unexpectedly."));
      };
      try {
        worker.postMessage({ file: file });
      } catch (error) {
        settle(reject, error);
      }
    });
  }

  function cancelComparisonLoads() {
    state.comparisonLoadSession = null;
    Array.from(state.comparisonParsers).forEach(function (parser) {
      parser.cancel();
    });
    state.comparisonParsers.clear();
  }

  function updateInteractionHash() {
    if (!state.summary || state.activePage !== "interactions") {
      return;
    }
    const filters = state.filters || {};
    const parameters = new URLSearchParams();
    if (filters.rna) {
      parameters.set("rna", filters.rna);
    }
    if (filters.partner) {
      parameters.set("partner", filters.partner);
    }
    const suffix = parameters.toString();
    history.replaceState(null, "", "#/workspace/interactions" + (suffix ? "?" + suffix : ""));
  }

  function copyText(text, confirmation) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () {
        showToast(confirmation);
      }).catch(function () {
        fallbackCopy(text, confirmation);
      });
      return;
    }
    fallbackCopy(text, confirmation);
  }

  function fallbackCopy(text, confirmation) {
    const textArea = document.createElement("textarea");
    textArea.value = text;
    textArea.style.position = "fixed";
    textArea.style.opacity = "0";
    document.body.appendChild(textArea);
    textArea.select();
    try {
      document.execCommand("copy");
      showToast(confirmation);
    } catch (error) {
      showToast("Copy is unavailable in this browser.");
    }
    textArea.remove();
  }

  async function digestFile(file) {
    if (!window.crypto || !window.crypto.subtle) {
      return "";
    }
    const buffer = await file.arrayBuffer();
    const digest = await window.crypto.subtle.digest("SHA-256", buffer);
    return Array.from(new Uint8Array(digest)).map(function (byte) {
      return byte.toString(16).padStart(2, "0");
    }).join("");
  }

  function clearSession() {
    terminateWorker();
    cancelComparisonLoads();
    invalidateFastaLoad();
    state.exampleLoadSession = null;
    state.landingFastaFile = null;
    state.parseSession = null;
    state.dialog = null;
    state.file = null;
    state.summary = null;
    state.records = [];
    state.fasta = null;
    state.filters = null;
    state.interactionResults = [];
    state.interactionScrollTop = 0;
    state.selectedRecordIndex = null;
    state.contact = null;
    state.region = null;
    state.viewpoint = null;
    state.comparison = null;
    state.structure = null;
    state.loading = null;
    state.error = null;
    setRouteHash("#/");
  }

  function resetToLanding() {
    terminateWorker();
    cancelComparisonLoads();
    invalidateFastaLoad();
    state.exampleLoadSession = null;
    state.landingFastaFile = null;
    state.parseSession = null;
    state.file = null;
    state.summary = null;
    state.records = [];
    state.fasta = null;
    state.filters = null;
    state.interactionResults = [];
    state.interactionScrollTop = 0;
    state.selectedRecordIndex = null;
    state.contact = null;
    state.region = null;
    state.viewpoint = null;
    state.comparison = null;
    state.structure = null;
    state.loading = null;
    state.error = null;
    state.activePage = "landing";
    render();
  }

  function terminateWorker() {
    if (state.worker) {
      state.worker.terminate();
      state.worker = null;
    }
    if (window.Hyb2Structure) {
      window.Hyb2Structure.terminate(state);
    }
  }

  function downloadSummary() {
    const payload = {
      application: "HYB2 Web Lite",
      version: "0.5.0",
      buildCommit: window.HYB2_BUILD && window.HYB2_BUILD.commit || null,
      sourceFile: {
        name: state.summary.fileName,
        bytes: state.summary.fileSize,
        sha256: state.summary.sha256 || null,
        sha256Status: state.summary.sha256
          ? "available"
          : (state.summary.sha256Unavailable ? "unavailable" : "pending")
      },
      analysis: "overview",
      generatedLocally: true,
      summary: state.summary
    };
    downloadText("hyb2-overview-summary.json", JSON.stringify(payload, null, 2), "application/json");
    showToast("Summary JSON downloaded locally.");
  }

  function downloadRnaCounts() {
    downloadText("rna_counts.csv", window.Hyb2Data.rnaCountsToCsv(state.records || []), "text/csv");
    showToast("RNA counts CSV downloaded locally.");
  }

  function downloadRnaPairs() {
    downloadText("rna_pairs.csv", window.Hyb2Data.rnaPairsToCsv(state.records || []), "text/csv");
    showToast("RNA-pair counts CSV downloaded locally.");
  }

  function downloadValidationReport() {
    const rows = [["line", "error", "content"]].concat(state.summary.errors.map(function (error) {
      return [error.line, error.error, error.content];
    }));
    const csv = rows.map(function (row) {
      return row.map(csvCell).join(",");
    }).join("\n");
    const sampled = !!state.summary.errorSampleTruncated;
    downloadText(sampled ? "hyb2-validation-error-sample.csv" : "hyb2-validation-report.csv", csv, "text/csv");
    showToast(sampled
      ? "Validation error sample downloaded locally; the Validation page shows how many details were omitted."
      : "Validation report downloaded locally.");
  }

  function downloadText(fileName, content, type) {
    const blob = new Blob([content], { type: type + ";charset=utf-8" });
    downloadBlob(fileName, blob);
  }

  function downloadBlob(fileName, blob) {
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = fileName;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    window.setTimeout(function () { URL.revokeObjectURL(url); }, 0);
  }

  function showToast(message) {
    const toast = document.getElementById("toast");
    if (!toast) {
      return;
    }

    toast.textContent = message;
    toast.classList.add("is-visible");
    window.clearTimeout(state.toastTimer);
    state.toastTimer = window.setTimeout(function () {
      toast.classList.remove("is-visible");
    }, 2900);
  }

  function pageTitle(page) {
    const allPages = analysisPages.concat([
      { id: "validation", label: "Validation" },
      { id: "methods", label: "Methods" },
      { id: "file-format", label: "File format" },
      { id: "privacy", label: "Privacy" }
    ]);
    const match = allPages.find(function (item) { return item.id === page; });
    return match ? match.label : "HYB2 Web Lite";
  }

  function themeGlyph() {
    return state.theme === "light" ? "◐" : "◑";
  }

  function formatNumber(value) {
    return numberFormatter.format(value || 0);
  }

  function formatBytes(bytes) {
    if (!bytes) {
      return "0 B";
    }

    const units = ["B", "KB", "MB", "GB"];
    const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    const value = bytes / Math.pow(1024, index);
    return new Intl.NumberFormat("en-US", { maximumFractionDigits: index ? 1 : 0 }).format(value) + " " + units[index];
  }

  function escapeHtml(value) {
    return String(value == null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#039;");
  }

  function escapeAttribute(value) {
    return escapeHtml(value).replace(/`/g, "&#096;");
  }

  function csvCell(value) {
    const cell = String(value == null ? "" : value);
    return '"' + cell.replace(/"/g, '""') + '"';
  }
}());
