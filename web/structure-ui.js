(function () {
  "use strict";

  const SVG_NS = "http://www.w3.org/2000/svg";

  // Results already brought into view (selecting a candidate changes the
  // result in place, so it is not revealed again).
  const revealedResults = typeof WeakSet === "function" ? new WeakSet() : null;
  let diagramObserver = null;
  let fornaContainer = null;
  // When the current fold started (UI only): Cancel asks first once a fold
  // has run this long.
  const LONG_RUN_MS = 10000;
  let runStartedAt = null;

  function afterRender(state, api) {
    if (diagramObserver) {
      diagramObserver.disconnect();
      diagramObserver = null;
    }
    fornaContainer = null;
    showLocalCommand(state);
    const result = state.structure && state.structure.result;
    const target = document.querySelector("[data-structure-diagram]");
    if (!result || !target) {
      return;
    }

    const viewMode = resultViewMode(result, state.structure.viewMode);
    state.structure.viewMode = viewMode;
    if (state.structure.editorEnabled && window.Hyb2StructureEditor) {
      window.Hyb2StructureEditor.mount(target, state, api);
    } else if (viewMode === "folded") {
      renderFornaDiagram(target, result);
    } else {
      target.replaceChildren(buildStructureDiagram(result, state.structure.selectedNucleotide, viewMode));
      bindNucleotideMarkers(target, state, api);
      if (viewMode === "arc") {
        keepSidewaysScroll(target, result);
      }
    }
    observeDiagramSize(target);
    if (revealedResults && !revealedResults.has(result)) {
      revealedResults.add(result);
      revealNewResult(result);
    }
  }

  // The shell command for the downloaded local CPLfold inputs, shown under
  // the page heading with a Copy button until dismissed.
  function showLocalCommand(state) {
    const command = state.structure && state.structure.engine === "cplfold" ? state.structure.localCommand : "";
    const heading = document.querySelector(".structure-page-heading");
    if (!command || !heading || !heading.parentNode || typeof heading.insertAdjacentElement !== "function") {
      return;
    }
    const box = document.createElement("section");
    box.className = "structure-local-command";
    box.setAttribute("aria-labelledby", "structure-local-command-title");
    const title = document.createElement("p");
    title.id = "structure-local-command-title";
    title.className = "structure-local-command-title";
    title.textContent = "Run the downloaded inputs locally";
    const code = document.createElement("pre");
    code.className = "structure-local-command-code";
    code.tabIndex = 0;
    code.setAttribute("aria-label", "Local CPLfold command");
    const text = document.createElement("code");
    text.textContent = command;
    code.appendChild(text);
    const actions = document.createElement("div");
    actions.className = "inline-actions";
    actions.innerHTML = '<button class="button button-secondary button-compact" type="button" data-feature-action="copy-local-cplfold-command">Copy command</button>' +
      '<button class="quiet-button" type="button" data-feature-action="dismiss-local-cplfold-command">Dismiss</button>';
    box.append(title, code, actions);
    heading.insertAdjacentElement("afterend", box);
  }

  // On a phone the arc view keeps its drawn width and scrolls sideways in
  // its box (fix-structure.css). Tapping a marker redraws the whole page,
  // so the new box takes over the old one's scroll position; a new result
  // starts from the left. The line above the box (shown by the same phone
  // rule) says that it scrolls.
  let arcScroll = { result: null, left: 0 };

  function keepSidewaysScroll(target, result) {
    if (arcScroll.result === result) {
      target.scrollLeft = arcScroll.left;
    } else {
      arcScroll = { result: result, left: 0 };
    }
    if (typeof target.addEventListener === "function") {
      target.addEventListener("scroll", function () {
        arcScroll.left = target.scrollLeft;
      }, { passive: true });
    }
    const before = target.previousElementSibling;
    const hinted = before && before.classList && before.classList.contains("structure-scroll-hint");
    if (!hinted && target.parentNode && typeof target.parentNode.insertBefore === "function") {
      const hint = document.createElement("p");
      hint.className = "card-note structure-scroll-hint";
      hint.textContent = "This diagram is wider than the screen: swipe sideways to see the rest.";
      target.parentNode.insertBefore(hint, target);
    }
  }

  function prefersReducedMotion() {
    const motion = document.documentElement.dataset.motion;
    return motion === "reduced" || (motion !== "full" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  }

  // E5 + E7: a new result folds the setup to its summary line, brings the
  // result heading into view (no smooth scroll under reduced motion), moves
  // focus to that heading so screen readers announce it, and says the
  // headline numbers in the live region.
  function revealNewResult(result) {
    window.Hyb2OpenSections = window.Hyb2OpenSections || Object.create(null);
    window.Hyb2OpenSections["structure-setup"] = false;
    const setup = document.querySelector(".structure-setup-panel");
    if (setup) {
      setup.open = false;
    }
    const heading = document.getElementById("structure-result-heading");
    const reveal = function () {
      if (!heading || !heading.isConnected) {
        return;
      }
      if (typeof heading.scrollIntoView === "function") {
        heading.scrollIntoView({ behavior: prefersReducedMotion() ? "auto" : "smooth", block: "start" });
      }
      if (typeof heading.focus === "function") {
        heading.focus({ preventScroll: true });
      }
    };
    if (typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(reveal);
    } else {
      reveal();
    }
    const live = document.querySelector("[data-structure-announce]");
    if (live) {
      const summary = "Structure ready · " + formatEnergyText(result.energy) + " kcal/mol · " + (result.pairs || []).length + " pair" + ((result.pairs || []).length === 1 ? "" : "s");
      setTimeout(function () {
        if (live.isConnected) {
          live.textContent = summary;
        }
      }, 150);
    }
  }

  // Energies read the same on this page, on the Report and in the DBN/CT
  // files: two decimals and an ASCII minus ("-25.50").
  function formatEnergyText(value) {
    const number = Number(value);
    return Number.isFinite(number) ? number.toFixed(2) : "—";
  }

  // E6: the diagram follows its container's width (window resize, setup
  // folded or opened). The SVG views scale by themselves; fornac needs
  // setSize. Redraws are throttled to one per frame after the size settles.
  function observeDiagramSize(target) {
    if (typeof ResizeObserver !== "function") {
      return;
    }
    let lastWidth = target.clientWidth;
    let timer = null;
    diagramObserver = new ResizeObserver(function () {
      const width = target.clientWidth;
      if (Math.abs(width - lastWidth) < 2) {
        return;
      }
      lastWidth = width;
      clearTimeout(timer);
      timer = setTimeout(function () {
        if (fornaContainer && typeof fornaContainer.setSize === "function") {
          try {
            fornaContainer.setSize();
          } catch (error) {
            // a failed resize keeps the previous layout
          }
        }
      }, 120);
    });
    diagramObserver.observe(target);
  }

  function renderFornaDiagram(target, result) {
    target.replaceChildren();
    if (!window.fornac || typeof window.fornac.FornaContainer !== "function") {
      const unavailable = document.createElement("p");
      unavailable.className = "structure-render-error";
      unavailable.textContent = "The fornac RNA renderer is unavailable. Select Arc to inspect this structure.";
      target.appendChild(unavailable);
      return;
    }
    const mount = document.createElement("div");
    mount.id = "forna-structure-" + Math.random().toString(36).slice(2);
    mount.className = "forna-structure-mount";
    target.appendChild(mount);
    // fornac gives its mount tabindex=1 and focuses it while constructing,
    // which scrolled the page to the diagram and took focus from the
    // control the user had pressed. Ignore that focus call and put the
    // mount back in normal tab order.
    mount.focus = function () {};
    try {
      const container = new window.fornac.FornaContainer("#" + mount.id, {
        animation: false,
        zoomable: true,
        initialSize: [Math.max(320, target.clientWidth || 760), Math.max(320, target.clientHeight || 560)],
        labelInterval: result.sequence.length <= 200 ? 10 : 25
      });
      delete mount.focus;
      mount.setAttribute("tabindex", "0");
      container.addRNA(result.dotBracket, {
        sequence: result.sequence,
        name: result.label || (result.engine || "ViennaRNA")
      });
      container.setSize();
      fornaContainer = container;
      bindFornaControls(target, mount, container);
      bindTouchActivation(mount);

    } catch (error) {
      target.replaceChildren();
      const failed = document.createElement("p");
      failed.className = "structure-render-error";
      failed.textContent = "fornac could not lay out this structure. Select Arc or Matrix to inspect all base pairs.";
      target.appendChild(failed);
    }
  }

  // On a touch screen a swipe over the folded view scrolls the page: fornac
  // only gets touches once the canvas has been tapped (or focused, or the
  // view expanded), and a tap outside gives them back to the page. CSS
  // switches touch-action with the same is-active class.
  function bindTouchActivation(mount) {
    const active = function () {
      return mount.classList.contains("is-active") || !!(mount.closest && mount.closest(".is-expanded, :fullscreen"));
    };
    let tap = null;
    const hold = function (event) {
      if (active()) return;
      // keep the touch from fornac's zoom handler so the browser scrolls
      event.stopPropagation();
      if (event.type === "touchstart") {
        tap = event.touches.length === 1 ? { x: event.touches[0].clientX, y: event.touches[0].clientY } : null;
      } else if (event.type === "touchmove" && tap && event.touches.length === 1) {
        if (Math.hypot(event.touches[0].clientX - tap.x, event.touches[0].clientY - tap.y) > 10) tap = null;
      } else if (event.type === "touchend" && tap) {
        tap = null;
        mount.classList.add("is-active");
      }
    };
    ["touchstart", "touchmove", "touchend"].forEach(function (type) {
      mount.addEventListener(type, hold, { capture: true, passive: true });
    });
    mount.addEventListener("focus", function () { mount.classList.add("is-active"); });
    mount.addEventListener("pointerdown", function (event) {
      if (event.pointerType !== "touch") mount.classList.add("is-active");
    });
    document.addEventListener("pointerdown", function outside(event) {
      if (!mount.isConnected) {
        document.removeEventListener("pointerdown", outside, true);
        return;
      }
      if (!mount.contains(event.target)) mount.classList.remove("is-active");
    }, true);
  }

  // View-only controls use fornac's existing zoom state, so wheel, drag and
  // buttons stay in sync without rebuilding the result or altering pairs.
  function bindFornaControls(target, mount, container) {
    const toolbar = document.createElement("div");
    toolbar.className = "forna-view-controls";
    toolbar.setAttribute("role", "group");
    toolbar.setAttribute("aria-label", "Folded structure view controls");
    const zoom = function (factor) {
      const current = container.zoomer.scale();
      const extent = container.zoomer.scaleExtent();
      const next = Math.max(extent[0], Math.min(extent[1], current * factor));
      const translate = container.zoomer.translate();
      const centre = [container.options.svgW / 2, container.options.svgH / 2];
      container.zoomer.scale(next).translate(centre.map(function (value, index) {
        return value - (value - translate[index]) * next / current;
      }));
      container.options.svg.call(container.zoomer.event);
    };
    const button = function (label, action) {
      const control = document.createElement("button");
      control.type = "button";
      control.className = "button button-secondary";
      control.textContent = label;
      control.addEventListener("click", action);
      toolbar.appendChild(control);
      return control;
    };
    button("Zoom in", function () { zoom(1.25); });
    button("Zoom out", function () { zoom(0.8); });
    button("Fit to view", function () { container.centerView(0); });
    const expanded = function () { return target.classList.contains("is-expanded"); };
    const normalHint = "forna · Scroll or pinch to zoom. Drag the canvas to pan; on a touch screen, tap it first. Keyboard: + / − to zoom, arrows to pan, 0 to fit.";
    const syncViewSize = function () {
      full.textContent = expanded() ? "Exit expanded view" : document.fullscreenElement === target ? "Exit full screen" : "Full screen";
      hint.textContent = expanded() ? "Expanded within this page. Press Escape or Exit expanded view to return. Scroll to zoom; drag to pan." : normalHint;
      container.setSize();
    };
    const full = button("Full screen", async function () {
      if (expanded()) {
        target.classList.remove("is-expanded");
        syncViewSize();
        return;
      }
      try {
        if (document.fullscreenElement === target) {
          await document.exitFullscreen();
        } else if (typeof target.requestFullscreen === "function") {
          await target.requestFullscreen();
        } else {
          throw new Error("Native fullscreen unavailable");
        }
      } catch (error) {
        // A browser may deny native fullscreen; this stays inside the page.
        target.classList.add("is-expanded");
        hint.textContent = "Expanded within this page. Press Escape or Exit expanded view to return. Scroll to zoom; drag to pan.";
      }
      syncViewSize();
    });
    target.onfullscreenchange = syncViewSize;
    target.onkeydown = function (event) {
      if (event.key === "Escape" && expanded()) {
        target.classList.remove("is-expanded");
        syncViewSize();
        event.preventDefault();
        event.stopPropagation();
      }
    };
    const hint = document.createElement("p");
    hint.className = "card-note forna-view-hint";
    hint.textContent = normalHint;
    mount.setAttribute("role", "group");
    mount.setAttribute("aria-label", "Interactive folded RNA structure. Scroll to zoom; drag to pan.");
    mount.addEventListener("keydown", function (event) {
      if (event.ctrlKey || event.metaKey || event.altKey) { return; }
      if (event.key === "+" || event.key === "=") { zoom(1.25); }
      else if (event.key === "-" || event.key === "_") { zoom(0.8); }
      else if (event.key === "0") { container.centerView(0); }
      else if (["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].indexOf(event.key) >= 0) {
        const delta = { ArrowLeft: [-40, 0], ArrowRight: [40, 0], ArrowUp: [0, -40], ArrowDown: [0, 40] }[event.key];
        container.zoomer.translate(container.zoomer.translate().map(function (value, index) { return value + delta[index]; }));
        container.options.svg.call(container.zoomer.event);
      } else { return; }
      event.preventDefault();
      event.stopPropagation();
    });
    target.insertBefore(toolbar, mount);
    target.appendChild(hint);
  }

  function handleAction(action, state, api, element) {
    if (action === "use-iris-evidence-region") {
      if (!state.structure || state.structure.engine !== "iris" || state.structure.status === "running") return true;
      // Recompute from the current data rather than trusting stale button coordinates.
      const info = window.Hyb2Pages.getStructureSequence(state);
      const region = info.irisDiagnostics && info.irisDiagnostics.suggestion;
      if (!region) { api.showToast("No complete HYB arm pair fits the 300 nt IRIS browser limit for this RNA. Enter a shorter region by hand, or choose CPLfold for a longer one."); return true; }
      if (window.Hyb2Structure) window.Hyb2Structure.reset(state, { clearConstraints: true });
      delete state.structure.defaultRegionEnd;
      Object.assign(state.structure, { start: String(region.start), end: String(region.end),
        result: null, selectedNucleotide: null, status: "idle", message: "", progress: null });
      api.render();
      api.showToast("Region updated. Review the interval, then run IRIS.");
      return true;
    }
    if (action === "structure-use-hyb-evidence") {
      // Back from the default setup's sequence-only fallback (analysis-pages.js
      // syncDefaultStructureEvidence). From here the evidence mode is the
      // user's own, so Predict waits for a region that holds HYB rows.
      if (!state.structure || state.structure.status === "running") return true;
      if (window.Hyb2Structure) window.Hyb2Structure.reset(state, { clearConstraints: true });
      delete state.structure.evidenceFallback;
      state.structure.evidenceChosen = true;
      state.structure.cplfoldEvidence = "hyb-blocks";
      state.structure.source = "reference";
      api.render();
      // the button is gone: focus moves to the control it changed
      const control = typeof document !== "undefined" && typeof document.querySelector === "function"
        ? document.querySelector('[data-feature="structure-control"][data-key="cplfoldEvidence"]') : null;
      if (control && typeof control.focus === "function") control.focus();
      return true;
    }
    if (action === "predict-structure") {
      if (!window.Hyb2Structure) {
        api.showToast("The local structure predictor is unavailable in this browser. Reload the page; if it stays unavailable, open HYB2 in a current Chrome, Edge, Firefox or Safari.");
        return true;
      }
      window.Hyb2Structure.predict(state, api);
      runStartedAt = { structure: state.structure, time: Date.now() };
      return true;
    }

    if (action === "probe-cplfold-capacity") {
      if (!window.Hyb2Structure || typeof window.Hyb2Structure.probeCplfoldCapacity !== "function") {
        api.showToast("The local CPLfold capacity test is unavailable in this browser. Reload the page, or select a region of 75 nt or less, which needs no test.");
        return true;
      }
      window.Hyb2Structure.probeCplfoldCapacity(state, api);
      return true;
    }

    if (action === "cancel-structure") {
      const structure = state.structure;
      const capacityProbe = structure && structure.operation === "capacity";
      const bonusMatrixExport = structure && structure.operation === "bonus-matrix";
      const operation = structure && structure.operation;
      const runId = structure && structure.runId;
      const cancelNow = function (confirmed) {
        // the run may have finished or been replaced while the dialog was open
        if (confirmed && (state.structure !== structure || structure.status !== "running" || structure.operation !== operation || structure.runId !== runId)) {
          api.render();
          return;
        }
        if (window.Hyb2Structure) {
          window.Hyb2Structure.cancel(state);
        }
        api.render();
        api.showToast(capacityProbe
          ? "Browser CPLfold capacity test cancelled."
          : (bonusMatrixExport ? "CPLfold local input export cancelled." : "Local structure prediction cancelled."));
      };
      // A capacity test or a fold that has run for a while loses that work
      // when cancelled: ask first. Short runs and exports cancel at once.
      const elapsed = structure && runStartedAt && runStartedAt.structure === structure ? Date.now() - runStartedAt.time : 0;
      const longRun = structure && structure.status === "running" && (capacityProbe || (!bonusMatrixExport && elapsed >= LONG_RUN_MS));
      if (longRun && typeof window.Hyb2Confirm === "function") {
        window.Hyb2Confirm({
          title: capacityProbe ? "Cancel the capacity test?" : "Cancel this prediction?",
          body: capacityProbe
            ? "The test stops and its measurement so far is discarded. You can run it again later; it takes up to a minute."
            : "The fold stops and the work done so far is discarded. Predicting again starts from the beginning.",
          confirmLabel: capacityProbe ? "Cancel test" : "Cancel prediction",
          cancelLabel: "Keep running",
          onConfirm: function () { cancelNow(true); }
        });
        return true;
      }
      cancelNow(false);
      return true;
    }

    if (action === "toggle-structure-editor") {
      // Two-strand results draw both strands, with no backbone across the break.
      state.structure.editorEnabled = !state.structure.editorEnabled;
      api.render();
      return true;
    }

    if (action === "set-structure-view") {
      state.structure.editorEnabled = false;
      const viewMode = structureViewMode(element && element.dataset && element.dataset.viewMode);
      if (!state.structure) {
        return true;
      }
      state.structure.viewMode = resultViewMode(state.structure.result, viewMode);
      api.render();
      return true;
    }

    if (action === "copy-local-cplfold-command") {
      if (state.structure && state.structure.localCommand) api.copyText(state.structure.localCommand, "CPLfold command copied to the clipboard.");
      return true;
    }
    if (action === "dismiss-local-cplfold-command") {
      if (state.structure) delete state.structure.localCommand;
      api.render();
      return true;
    }

    if (action === "download-local-cplfold-input") {
      return downloadLocalCplfoldInput(state, api);
    }

    const result = state.structure && state.structure.result;
    if (action === "select-iris-candidate") {
      const index = Number(element && element.dataset && element.dataset.candidateIndex);
      if (!result || result.engine !== "IRIS" || !selectCplfoldCandidate(result, index)) { api.showToast("That IRIS member is unavailable."); return true; }
      state.structure.selectedNucleotide = null; api.render();
      api.showToast("IRIS ensemble member " + (index + 1) + " selected."); return true;
    }
    if (action === "select-cplfold-candidate") {
      const candidateIndex = Number(element && element.dataset && element.dataset.candidateIndex);
      if (!result || !isCplfoldResult(result) || !selectCplfoldCandidate(result, candidateIndex)) {
        api.showToast("That CPLfold candidate is unavailable.");
        return true;
      }
      state.structure.selectedNucleotide = null;
      api.render();
      // only when the diagram is off screen
      const revealDiagram = function () {
        if (typeof document === "undefined") {
          return;
        }
        const diagram = document.querySelector("[data-structure-diagram]");
        if (!diagram || typeof diagram.scrollIntoView !== "function") {
          return;
        }
        const box = typeof diagram.getBoundingClientRect === "function" ? diagram.getBoundingClientRect() : null;
        const visible = box && box.bottom > 80 && box.top < (window.innerHeight || 0) - 80;
        if (!visible) {
          diagram.scrollIntoView({ behavior: prefersReducedMotion() ? "auto" : "smooth", block: "start" });
        }
      };
      if (typeof window.requestAnimationFrame === "function") {
        window.requestAnimationFrame(revealDiagram);
      } else {
        revealDiagram();
      }
      api.showToast(result.engine + " candidate " + (candidateIndex + 1) + " selected.");
      return true;
    }
    if (!result || [
      "copy-structure-dot-bracket",
      "download-structure-dot-bracket",
      "download-structure-ct",
      "download-structure-pairs",
      "download-structure-evidence",
      "download-structure-scored-evidence",
      "download-structure-constraints",
      "download-structure-ensemble",
      "download-iris-ensemble",
      "download-cplfold-evidence",
      "download-cplfold-candidates",
      "download-structure-svg",
      "download-structure-png",
      "download-structure-report"
    ].indexOf(action) === -1) {
      return false;
    }

    if (action === "download-iris-ensemble" && result.engine === "IRIS") {
      const rows = [["member", "selected", "weight", "coefficient", "energy_kcal_per_mol", "dot_bracket"]];
      (result.candidates || []).forEach(function(c,i) { rows.push([i+1, i === result.selectedCandidate ? "yes" : "no", c.weight, c.coefficient, Number.isFinite(Number(c.energy)) ? Number(c.energy).toFixed(2) : c.energy, c.dotBracket]); });
      api.download("iris-ensemble.tsv", rows.map(function(row) { return row.join("\t"); }).join("\n") + "\n", "text/tab-separated-values");
      api.showToast("IRIS weighted ensemble downloaded locally."); return true;
    }
    if (action === "copy-structure-dot-bracket") {
      api.copyText(withStrandBreak(result.dotBracket, result.cutpoint), "Dot-bracket notation copied to the clipboard.");
      return true;
    }

    if (action === "download-structure-dot-bracket") {
      api.download("rna-secondary-structure.dbn", dotBracketFile(result), "text/plain");
      api.showToast("Dot-bracket file downloaded locally.");
      return true;
    }

    if (action === "download-structure-ct") {
      api.download("rna-secondary-structure.ct", ctFile(result), "text/plain");
      api.showToast("CT file downloaded locally.");
      return true;
    }

    if (action === "download-structure-pairs") {
      api.download("rna-secondary-structure-pairs.tsv", basePairTsv(result), "text/tab-separated-values");
      api.showToast("Base-pair table downloaded locally.");
      return true;
    }

    if (action === "download-structure-evidence") {
      api.download("rna-cofold-evidence.tsv", evidenceTsv(result), "text/tab-separated-values");
      api.showToast("RNAcofold constraint shortlist downloaded locally.");
      return true;
    }

    if (action === "download-structure-scored-evidence") {
      if (!Array.isArray(result.evidence?.scoredEvidencePairs)) return false;
      api.download("rna-cofold-scored-evidence.tsv", evidenceTsv(result, true), "text/tab-separated-values");
      api.showToast("Complete scored RNAcofold evidence downloaded locally.");
      return true;
    }

    if (action === "download-structure-constraints") {
      api.download("hyb-guided-folding-constraints.tsv", constraintsTsv(result), "text/tab-separated-values");
      api.showToast("Fitted stem constraints downloaded locally.");
      return true;
    }

    if (action === "download-structure-ensemble") {
      api.download("hyb-guided-structure-ensemble.tsv", ensembleTsv(result), "text/tab-separated-values");
      api.showToast("Randomised structure ensemble downloaded locally.");
      return true;
    }

    if (action === "download-cplfold-evidence") {
      api.download("cplfold-bonus-matrix.tsv", cplfoldEvidenceTsv(result), "text/tab-separated-values");
      api.showToast("CPLfold HYB bonus matrix downloaded locally.");
      return true;
    }

    if (action === "download-cplfold-candidates") {
      api.download("cplfold-candidates.tsv", cplfoldCandidatesTsv(result), "text/tab-separated-values");
      api.showToast("CPLfold candidates downloaded locally.");
      return true;
    }

    if (action === "download-structure-svg") {
      const svg = lightExportSvg(state);
      if (!svg) {
        api.showToast("The structure diagram is not ready to export yet.");
        return true;
      }
      const viewMode = structureViewMode(state.structure && state.structure.viewMode);
      api.download("rna-secondary-structure-" + structureViewFileStem(viewMode) + ".svg", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" + new XMLSerializer().serializeToString(svg), "image/svg+xml");
      api.showToast("Structure SVG downloaded locally.");
      return true;
    }

    if (action === "download-structure-png") {
      downloadStructurePng(api, structureViewMode(state.structure && state.structure.viewMode), lightExportSvg(state));
      return true;
    }

    api.download("rna-secondary-structure-report.json", JSON.stringify(structureReport(state, result), null, 2), "application/json");
    api.showToast("Structure report downloaded locally.");
    return true;
  }

  function downloadLocalCplfoldInput(state, api) {
    if (!state.structure || state.structure.engine !== "cplfold") {
      api.showToast("Select CPLfold before downloading a local input.");
      return true;
    }

    // A provider refresh keeps the old reference and rows visible until the new ones arrive.
    if (state.structure.source === "reference" && state.view?.loading?.structureEvidence) {
      api.showToast("Wait for the current reference sequence and HYB evidence to load.");
      return true;
    }
    const prepared = window.Hyb2Pages?.getStructureSequence?.(state);
    if (prepared?.error || !prepared?.sequence) {
      api.showToast(prepared?.error || "Choose a valid sequence first.");
      return true;
    }

    const guided = state.structure.cplfoldEvidence === "hyb-blocks";
    let command;
    try {
      command = localCplfoldCommand(state.structure, guided);
    } catch (error) {
      api.showToast(error.message);
      return true;
    }

    const { sequence, cutpoint } = prepared;
    const fasta = cutpoint
      ? ">HYB2_Web_RNA_A\n" + sequence.slice(0, cutpoint) + "\n>HYB2_Web_RNA_B\n" + sequence.slice(cutpoint) + "\n"
      : ">HYB2_Web_prepared_sequence\n" + sequence + "\n";
    function downloadInputs(matrixResult) {
      const matrix = guided ? cplfoldEvidenceTsv(matrixResult) : null;
      api.download("cplfold-input.fasta", fasta, "text/plain");
      if (guided) api.download("cplfold-bonus-matrix.tsv", matrix, "text/tab-separated-values");
      // the command stays on the page (below the heading) to read and copy
      if (state.structure) state.structure.localCommand = command;
      api.render();
      api.showToast((guided ? "CPLfold local inputs downloaded. Run: " : "CPLfold FASTA downloaded. Run: ") + command + " (shown with a Copy button below the page heading)");
    }

    if (!guided) {
      downloadInputs();
    } else if (typeof window.Hyb2Structure?.downloadCplfoldBonusMatrix === "function") {
      // Only the result's separate matrix export uses historical evidence. Wait for both current inputs.
      window.Hyb2Structure.downloadCplfoldBonusMatrix(state, api, prepared, downloadInputs);
    } else {
      api.showToast("The local CPLfold bonus-matrix exporter is unavailable in this browser. Reload the page, or set Experimental evidence to Sequence only to download the FASTA alone.");
    }
    return true;
  }

  function localCplfoldCommand(structure, guided) {
    return globalThis.Hyb2CplfoldSettings.localCommand(structure, guided);
  }

  function selectCplfoldCandidate(result, index) {
    const candidates = result.candidates || [];
    if (!Number.isInteger(index) || index < 0 || index >= candidates.length) {
      return false;
    }
    const candidate = candidates[index];
    result.selectedCandidate = index;
    if (result.engine === "IRIS") { result.constraints = candidate.constraints || []; result.constraintCount = result.constraints.length; }
    result.dotBracket = candidate.dotBracket;
    ["dotBracketWithBreak", "phase1StructureWithBreak", "phase2StructureWithBreak", "energyMetadata", "energyBreakdown", "intermolecularPairs", "comradesPairCounts", "comradesScore"].forEach(function (key) {
      if (candidate[key] !== undefined) result[key] = candidate[key];
    });
    result.pairs = candidate.pairs || [];
    result.unpaired = candidate.unpaired;
    result.energy = candidate.energy;
    result.effectiveEnergy = candidate.effectiveEnergy;
    result.phase1Energy = candidate.phase1Energy;
    result.phase1Structure = candidate.phase1Structure;
    result.phase2Structure = candidate.phase2Structure;
    result.score = candidate.score;
    result.structureType = candidate.type;
    result.topology = candidate.topology;
    result.crossingPairs = candidate.crossingPairs;
    return true;
  }

  function structureReport(state, result) {
    const constraintCount = Math.max(0, Number(result.constraintCount) || 0);
    const guided = result.constraintMode === "hyb-guided";
    const cplfold = isCplfoldResult(result);
    const pairScored = cplfold && Array.isArray(result.comradesPairCounts);
    const cplfoldAllowsPseudoknot = cplfold && resultAllowsPseudoknot(result);
    const allowPseudoknot = resultAllowsPseudoknot(result);
    const capacity = cplfold && state.structure && state.structure.cplfoldCapacity
      ? state.structure.cplfoldCapacity
      : null;
    const baselineLength = capacity && Number.isInteger(Number(capacity.baselineLength))
      ? Number(capacity.baselineLength)
      : 75;
    const hardCeiling = capacity && Number.isInteger(Number(capacity.hardCeiling))
      ? Number(capacity.hardCeiling)
      : 500;
    const capacityMatches = capacity && capacity.status === "ready" && capacity.profileKey === cplfoldProfileKey(state.structure);
    const recommendedLength = capacityMatches
      ? Math.max(baselineLength, Math.min(hardCeiling, Number(capacity.recommendedLength) || baselineLength))
      : (result.maxSequenceLength || baselineLength);
    const selectedCandidateIndex = cplfold ? Math.max(0, Number(result.selectedCandidate) || 0) : null;
    return {
      iris: result.engine === "IRIS" ? {parameters: result.parameters, ensemble: result.ensemble, members: result.candidates, selectedMember: result.selectedCandidate, warnings: result.warnings} : undefined,
      application: "HYB2 Web Lite",
      version: "0.5.0",
      buildCommit: window.HYB2_BUILD && window.HYB2_BUILD.commit || "unknown",
      generatedLocally: true,
      // The files the fold's HYB evidence came from (else the analysis scope);
      // with several, Hyb2UI.sourceHyb lists each file.
      sourceHyb: window.Hyb2UI && typeof window.Hyb2UI.sourceHyb === "function" ? window.Hyb2UI.sourceHyb(evidenceSourceState(state, result)) : {
        fileName: state.summary.fileName,
        fileSize: state.summary.fileSize == null ? null : state.summary.fileSize,
        sha256: state.summary.sha256 || null,
        sha256Status: state.summary.sha256
          ? "available"
          : (state.summary.sha256Unavailable ? "unavailable" : "pending")
      },
      evidenceDatasets: result.evidenceDatasets || [],
      sourceFasta: result.source === "reference" && state.reference ? {
        fileName: state.reference.fileName,
        fileSize: state.reference.fileSize == null ? null : state.reference.fileSize,
        sha256: state.reference.sha256 || null,
        sha256Status: state.reference.sha256
          ? "available"
          : (state.reference.sha256Unavailable ? "unavailable" : "pending")
      } : null,
      source: {
        label: result.label,
        type: result.source,
        sequence: result.sequence,
        displaySequence: withStrandBreak(result.sequence, result.cutpoint),
        cutpoint: result.cutpoint || null,
        strandLengths: result.strandLengths || [result.sequence.length],
        rna: result.sourceRna || null,
        start: result.sourceStart == null ? null : result.sourceStart,
        end: result.sourceEnd == null ? null : result.sourceEnd,
        segments: result.sourceSegments || null,
        assembly: result.assembly || null
      },
      prediction: {
        algorithm: result.algorithm,
        model: result.model,
        engine: result.engine,
        engineVersion: result.engineVersion,
        bridgeVersion: cplfold ? result.bridgeVersion : null,
        runtime: cplfold ? result.runtime : null,
        temperatureCelsius: result.temperature,
        minimumLoop: result.minimumLoop,
        mfe: cplfold || result.engine === "IRIS" ? null : result.energy,
        energy: result.energy,
        effectiveEnergy: cplfold ? result.effectiveEnergy : null,
        phase1Energy: cplfold ? result.phase1Energy : null,
        phase1Structure: cplfold ? result.phase1Structure : null,
        phase2Structure: cplfold ? result.phase2Structure : null,
        linearFoldScore: cplfold ? result.score : null,
        energyUnit: result.energyUnit || "kcal/mol",
        constraintMode: result.constraintMode || "none",
        constraintSource: result.constraintSource || (constraintCount ? "manual-user-input" : "none"),
        constraintCount: constraintCount,
        constraints: result.constraints || [],
        dotBracket: result.dotBracket,
        dotBracketWithBreak: withStrandBreak(result.dotBracket, result.cutpoint),
        cutpoint: result.cutpoint || null,
        strandLengths: result.strandLengths || [result.sequence.length],
        energyMetadata: result.energyMetadata || null,
        energyBreakdown: result.energyBreakdown || null,
        intermolecularPairs: result.intermolecularPairs || 0,
        pairs: result.pairs,
        elapsedMs: result.elapsedMs,
        runtimeLoadMs: cplfold ? result.runtimeLoadMs : null,
        foldElapsedMs: cplfold ? result.foldElapsedMs : null,
        browserRequestMaximumLength: cplfold ? result.maxSequenceLength || null : null,
        comradesScore: guided || pairScored ? result.comradesScore : null,
        ...(pairScored ? {comradesPairCounts: result.comradesPairCounts} : {}),
        matchedEvidencePairs: pairScored ? result.comradesPairCounts.filter(count => count > 0).length : (guided ? result.matchedEvidencePairs : null),
        structureType: cplfold ? result.structureType : null,
        topology: cplfold ? result.topology : "nested",
        crossingPairs: cplfold ? result.crossingPairs : 0,
        pseudoknotAllowed: cplfold ? allowPseudoknot : false,
        selectedCandidateIndex: selectedCandidateIndex,
        selectedCandidateRank: cplfold ? selectedCandidateIndex + 1 : null,
        evidenceMode: cplfold ? result.evidenceMode : null,
        evidenceSource: cplfold ? result.evidenceSource : null
      },
      visualization: {
        viewMode: structureViewMode(state.structure && state.structure.viewMode)
      },
      hybGuidedEvidence: guided ? result.evidence : null,
      ...(result.comradesEvidence ? { comradesEvidence: result.comradesEvidence } : {}),
      cplfoldEvidence: cplfold ? result.evidence : null,
      cplfoldParameters: cplfold ? result.parameters : null,
      cplfoldCandidates: cplfold ? result.candidates : null,
      randomisation: guided ? result.randomisation : null,
      acceptedStemConstraints: guided ? result.acceptedStemConstraints : null,
      rejectedStemConstraints: guided ? result.rejectedStemConstraints : null,
      methodScope: {
        manualHardBasePairsApplied: result.engine !== "IRIS" && !guided && !cplfold && constraintCount > 0,
        automaticHybEvidenceConstraintGeneration: guided || result.engine === "IRIS",
        automaticRnaCofoldEvidenceSelection: guided || (pairScored && window.Hyb2Comrades.pairCountSource(result) === "RNAcofold"),
        iterativeCompatibleConstraintFitting: guided,
        randomisedConstraintOrderFolding: guided && !!(result.randomisation && result.randomisation.completedFolds),
        supportBasedStructureScoring: guided || pairScored || result.engine === "IRIS",
        cplfoldPurePythonBrowserExecution: cplfold,
        cplfoldPseudoknotPrediction: cplfold && allowPseudoknot,
        cplfoldPseudoknotSearchRequested: cplfold ? allowPseudoknot : false,
        cplfoldHybBlockBonusMatrix: cplfold && result.evidenceSource === "hyb-block-bonus-matrix",
        numbaJitAvailable: cplfold ? false : null,
        browserCplfoldMaximumLength: cplfold ? recommendedLength : null,
        browserCplfoldBaselineLength: cplfold ? baselineLength : null,
        browserCplfoldHardCeiling: cplfold ? hardCeiling : null,
        browserCplfoldCapacity: cplfold ? capacity : null,
        unafoldExecution: "external-cli-only",
        irisBrowserAdaptation: result.engine === "IRIS",
        note: pairScored ? (window.Hyb2Pages.cplfoldBonusKind(result.evidence) === "saturating_hyb_bonus" ? "The saturating bonus B_ij and drawing colours use" : "Drawing colours use") + " the full raw counts C_ij (" + window.Hyb2Comrades.pairCountSource(result) + "). The selected candidate's COMRADES score counts min(C_ij, " + window.Hyb2Comrades.scoreCapLabel() + ") at each paired endpoint." : result.engine === "IRIS" ? result.model + ". " + (result.warnings || []).join(" ") : cplfold
          ? (allowPseudoknot ? "CPLfold used its two-phase candidate search." : "Phase 2 was skipped; only pseudoknot-free candidates were searched.") + (result.evidence?.source === "hyb-block-intervals" ? " Folding bonus: " + (result.evidence.transform || result.evidence.supportKernel || "Gaussian arm support (log1p)") + "." : " No HYB bonus was used.")
          : guided
          ? "RNAcofold evidence supplied the ranked stem constraints and COMRADES structure scores. " + (result.evidence?.scoringVersion === "all-pairs-capped-v1" ? "Scores use all eligible pairs with both orientations combined. Each paired endpoint contributes up to " + window.Hyb2Comrades.scoreCapLabel() + "; drawing colours use the full raw counts." : "The scoring version was not recorded; scores are reported as saved.")
          : constraintCount
          ? "The listed hard base pairs were entered manually by the user and enforced by ViennaRNA before MFE folding. They were not inferred from HYB or RNAcofold evidence."
          : "This is an unconstrained ViennaRNA global MFE fold. No HYB or RNAcofold evidence-derived constraints were generated."
      }
    };
  }

  function isCplfoldResult(result) {
    return !!result && (result.engine === "CPLfold" || result.engine === "CPLcofold");
  }

  function resultAllowsPseudoknot(result) {
    return !!result && result.allowPseudoknot !== false &&
      !(result.parameters && result.parameters.allowPseudoknot === false);
  }

  function cplfoldProfileKey(structure) {
    return globalThis.Hyb2CplfoldSettings.profileKey(structure);
  }

  function withStrandBreak(text, cutpoint) {
    const compact = String(text || "").replace(/&/g, "");
    return Number.isInteger(cutpoint) && cutpoint > 0 && cutpoint < compact.length
      ? compact.slice(0, cutpoint) + "&" + compact.slice(cutpoint) : compact;
  }

  function resultViewMode(result, value) {
    return result && result.cutpoint ? (value === "matrix" ? "matrix" : "arc") : structureViewMode(value);
  }

  function structureViewMode(value) {
    return ["folded", "arc", "radial", "circular", "matrix"].indexOf(value) > -1 ? value : "folded";
  }

  function structureViewFileStem(viewMode) {
    return structureViewMode(viewMode) === "arc" ? "arcs" : structureViewMode(viewMode);
  }

  function buildStructureDiagram(result, selectedNucleotide, viewMode) {
    if (Array.isArray(result.comradesPairCounts)) {
      const counts = window.Hyb2Comrades.rawNucleotideSupport(result);
      result = Object.assign({}, result, {rawCounts: counts, maximumRawCount: Math.max(0, ...result.comradesPairCounts)});
    }
    switch (resultViewMode(result, viewMode)) {
      case "radial":
        return buildRadialDiagram(result, selectedNucleotide);
      case "circular":
        return buildCircularDiagram(result, selectedNucleotide);
      case "matrix":
        return buildMatrixDiagram(result, selectedNucleotide);
      default:
        return buildArcDiagram(result, selectedNucleotide);
    }
  }

  function buildArcDiagram(result, selectedNucleotide) {
    const width = 920;
    const height = 540;
    const left = 42;
    const right = width - 28;
    const baseline = 244;
    const sequence = result.sequence || "";
    const length = sequence.length || 1;
    const colors = colorTokens();
    const cplfold = isCplfoldResult(result);
    const cplfoldAllowsPseudoknot = cplfold && resultAllowsPseudoknot(result);
    const hardPairs = structureHardPairs(result);
    const svg = element("svg", {
      xmlns: SVG_NS,
      viewBox: "0 0 " + width + " " + height,
      width: "100%",
      height: String(height),
      role: "group",
      "aria-label": "Predicted base pairs above and mapped raw HYB interactions below, " + length + " nucleotides"
    });
    svg.appendChild(element("desc", {}, diagramDescription(result, length)));

    svg.appendChild(element("title", {}, cplfold
      ? (cplfoldAllowsPseudoknot ? "Selected CPLfold RNA structure with pseudoknot layers" : "Selected CPLfold pseudoknot-free RNA structure")
      : "Predicted non-crossing RNA base pairs"));
    svg.appendChild(element("rect", { x: 0, y: 0, width: width, height: height, rx: 8, fill: colors.surface }));
    if (result.cutpoint) {
      const nick = (xFor(result.cutpoint, length, left, right) + xFor(result.cutpoint + 1, length, left, right)) / 2;
      const halfGap = Math.min(9, (right - left) / Math.max(2, length - 1) * 0.3);
      svg.appendChild(element("line", { x1: left, y1: baseline, x2: nick - halfGap, y2: baseline, stroke: colors.line, "stroke-width": 1.5, "data-strand": "A" }));
      svg.appendChild(element("line", { x1: nick + halfGap, y1: baseline, x2: right, y2: baseline, stroke: colors.line, "stroke-width": 1.5, "data-strand": "B" }));
      svg.appendChild(element("text", { x: nick, y: baseline + 35, "text-anchor": "middle", fill: colors.muted, "font-size": 11, "data-chain-break": String(result.cutpoint) }, "A | B"));
    } else {
      svg.appendChild(element("line", { x1: left, y1: baseline, x2: right, y2: baseline, stroke: colors.line, "stroke-width": 1.5 }));
    }

    (result.pairs || []).forEach(function (pair) {
      const style = structurePairStyle(result, pair, colors, hardPairs);
      const start = xFor(pair.left, length, left, right);
      const end = xFor(pair.right, length, left, right);
      const center = (start + end) / 2;
      const span = Math.max(1, pair.right - pair.left);
      const lift = Math.min(195, 24 + Math.sqrt(span / length) * 190);
      const path = element("path", {
        d: "M " + start.toFixed(2) + " " + baseline + " Q " + center.toFixed(2) + " " + (baseline - lift).toFixed(2) + " " + end.toFixed(2) + " " + baseline,
        "data-arc-source": "prediction",
        fill: "none",
        stroke: style.stroke,
        "stroke-width": style.strokeWidth,
        "stroke-linecap": "round",
        opacity: style.opacity
      });
      path.appendChild(element("title", {}, structurePairTitle(result, pair, style)));
      svg.appendChild(path);
    });

    svg.appendChild(element("text", { x: left, y: 49, fill: colors.ink, "font-size": 12 }, "Predicted structure · above"));
    const raw = Array.isArray(result.rawHybArms) ? result.rawHybArms : null;
    const valid = (raw || []).filter(function (arm) {
      const positions = [arm.oneStart, arm.oneEnd, arm.twoStart, arm.twoEnd];
      return positions.every(function (position) { return Number.isInteger(position) && position >= 1 && position <= length; }) &&
        arm.oneStart <= arm.oneEnd && arm.twoStart <= arm.twoEnd;
    });
    // Bound SVG size without silently claiming that every record is shown.
    const shown = valid.slice(0, 2000);
    shown.forEach(function (arm) {
      const first = (arm.oneStart + arm.oneEnd) / 2;
      const second = (arm.twoStart + arm.twoEnd) / 2;
      const start = xFor(Math.min(first, second), length, left, right);
      const end = xFor(Math.max(first, second), length, left, right);
      const lift = Math.min(400, 36 + Math.sqrt(Math.abs(second - first) / length) * 360);
      const path = element("path", {
        "data-arc-source": "raw-hyb",
        d: "M " + start.toFixed(2) + " " + baseline + " Q " + ((start + end) / 2).toFixed(2) + " " + (baseline + lift).toFixed(2) + " " + end.toFixed(2) + " " + baseline,
        fill: "none", stroke: colors.accent, "stroke-width": 1.1, opacity: valid.length < 100 ? 0.6 : 0.3
      });
      path.appendChild(element("title", {}, "HYB record " + (arm.recordId || "") + ": prepared-sequence arms " +
        arm.oneStart + "–" + arm.oneEnd + " / " + arm.twoStart + "–" + arm.twoEnd + "; arc joins arm midpoints, not a predicted base pair"));
      svg.appendChild(path);
    });
    const rawLabel = raw === null
      ? "Raw HYB data not attached to this result"
      : valid.length ? "Raw HYB interactions · below · " + shown.length + " / " + valid.length + " mapped records shown"
      : "No eligible HYB records mapped to this structure";
    svg.appendChild(element("text", { x: left, y: 501, fill: colors.ink, "font-size": 12 }, rawLabel));
    svg.appendChild(element("text", { x: left, y: 521, fill: colors.muted, "font-size": 11 },
      raw === null ? "No synthetic or predicted arcs are substituted for missing raw data." :
      "Arm midpoints on prepared-sequence coordinates; eligible mapped input records only." + (valid.length > shown.length ? " First 2,000 displayed." : "")));
    markerTicks(svg, result, length, left, right, baseline, colors, selectedNucleotide);
    legend(svg, colors, result);
    return svg;
  }

  function buildRadialDiagram(result, selectedNucleotide) {
    const width = 680;
    const height = 650;
    const centerX = 340;
    const centerY = 324;
    const radius = 224;
    const sequence = result.sequence || "";
    const length = Math.max(1, sequence.length);
    const colors = colorTokens();
    const pairs = result.pairs || [];
    const svg = structureSvg(width, height, "Radial diagram for " + length + " nucleotide RNA secondary structure", isCplfoldResult(result) ? "Selected CPLfold radial structure" : "Predicted radial RNA secondary structure", colors, result);
    const hardPairs = structureHardPairs(result);

    [0.25, 0.5, 0.75].forEach(function (ratio) {
      svg.appendChild(element("circle", {
        cx: centerX,
        cy: centerY,
        r: (radius * ratio).toFixed(2),
        fill: "none",
        stroke: colors.line,
        "stroke-width": 1,
        "stroke-dasharray": "2 5",
        opacity: 0.8
      }));
    });
    svg.appendChild(element("circle", {
      cx: centerX,
      cy: centerY,
      r: radius,
      fill: "none",
      stroke: colors.lineStrong,
      "stroke-width": 1.5
    }));

    pairs.forEach(function (pair) {
      const start = circlePoint(pair.left, length, centerX, centerY, radius);
      const end = circlePoint(pair.right, length, centerX, centerY, radius);
      const depth = pairDepth(pair, pairs);
      const pull = Math.max(0.16, 0.64 - Math.min(depth, 9) * 0.045);
      const midpointX = (start.x + end.x) / 2;
      const midpointY = (start.y + end.y) / 2;
      const controlX = centerX + (midpointX - centerX) * pull;
      const controlY = centerY + (midpointY - centerY) * pull;
      const style = structurePairStyle(result, pair, colors, hardPairs);
      const path = element("path", {
        d: "M " + start.x.toFixed(2) + " " + start.y.toFixed(2) + " Q " + controlX.toFixed(2) + " " + controlY.toFixed(2) + " " + end.x.toFixed(2) + " " + end.y.toFixed(2),
        fill: "none",
        stroke: style.stroke,
        "stroke-width": style.strokeWidth,
        "stroke-linecap": "round",
        "stroke-dasharray": style.dash,
        opacity: style.opacity
      });
      path.appendChild(element("title", {}, structurePairTitle(result, pair, style)));
      svg.appendChild(path);
    });

    appendSelectedRingRay(svg, length, centerX, centerY, radius, selectedNucleotide, colors);
    appendCircularTicks(svg, length, centerX, centerY, radius, colors);
    appendRingMarkers(svg, result, selectedNucleotide, centerX, centerY, radius, colors);
    legend(svg, colors, result, { x: 44, y: 24, roomy: true });
    return svg;
  }

  function buildCircularDiagram(result, selectedNucleotide) {
    const width = 680;
    const height = 650;
    const centerX = 340;
    const centerY = 324;
    const radius = 224;
    const sequence = result.sequence || "";
    const length = Math.max(1, sequence.length);
    const colors = colorTokens();
    const pairs = result.pairs || [];
    const svg = structureSvg(width, height, "Circular diagram for " + length + " nucleotide RNA secondary structure", isCplfoldResult(result) ? "Selected CPLfold circular structure" : "Predicted circular RNA secondary structure", colors, result);
    const hardPairs = structureHardPairs(result);

    svg.appendChild(element("circle", {
      cx: centerX,
      cy: centerY,
      r: radius,
      fill: "none",
      stroke: colors.lineStrong,
      "stroke-width": 2
    }));
    svg.appendChild(element("circle", {
      cx: centerX,
      cy: centerY,
      r: radius - 13,
      fill: "none",
      stroke: colors.line,
      "stroke-width": 1,
      "stroke-dasharray": "1 5"
    }));

    pairs.forEach(function (pair) {
      const start = circlePoint(pair.left, length, centerX, centerY, radius - 4);
      const end = circlePoint(pair.right, length, centerX, centerY, radius - 4);
      const style = structurePairStyle(result, pair, colors, hardPairs);
      const chord = element("line", {
        x1: start.x.toFixed(2),
        y1: start.y.toFixed(2),
        x2: end.x.toFixed(2),
        y2: end.y.toFixed(2),
        stroke: style.stroke,
        "stroke-width": style.strokeWidth,
        "stroke-linecap": "round",
        "stroke-dasharray": style.dash,
        opacity: style.opacity
      });
      chord.appendChild(element("title", {}, structurePairTitle(result, pair, style)));
      svg.appendChild(chord);
    });

    appendSelectedRingRay(svg, length, centerX, centerY, radius, selectedNucleotide, colors);
    appendCircularTicks(svg, length, centerX, centerY, radius, colors);
    appendRingMarkers(svg, result, selectedNucleotide, centerX, centerY, radius, colors);
    legend(svg, colors, result, { x: 44, y: 24, roomy: true });
    return svg;
  }

  function buildMatrixDiagram(result, selectedNucleotide) {
    const width = 760;
    const height = 560;
    const plotLeft = 104;
    const plotTop = 62;
    const plotSize = 390;
    const plotBottom = plotTop + plotSize;
    const sequence = result.sequence || "";
    const length = Math.max(1, sequence.length);
    const colors = colorTokens();
    const pairs = result.pairs || [];
    const svg = structureSvg(width, height, "Base-pair matrix for " + length + " nucleotide RNA secondary structure", isCplfoldResult(result) ? "Selected CPLfold base-pair matrix" : "Predicted RNA base-pair matrix", colors, result);
    const hardPairs = structureHardPairs(result);

    svg.appendChild(element("rect", {
      x: plotLeft,
      y: plotTop,
      width: plotSize,
      height: plotSize,
      fill: colors.surface,
      stroke: colors.lineStrong,
      "stroke-width": 1.2
    }));
    svg.appendChild(element("line", {
      x1: plotLeft,
      y1: plotTop,
      x2: plotLeft + plotSize,
      y2: plotBottom,
      stroke: colors.line,
      "stroke-width": 1,
      "stroke-dasharray": "3 5"
    }));
    appendMatrixTicks(svg, length, plotLeft, plotTop, plotSize, colors);

    pairs.forEach(function (pair) {
      const first = matrixPoint(pair.left, pair.right, length, plotLeft, plotTop, plotSize);
      const second = matrixPoint(pair.right, pair.left, length, plotLeft, plotTop, plotSize);
      const style = structurePairStyle(result, pair, colors, hardPairs);
      const point = element("circle", {
        cx: first.x.toFixed(2),
        cy: first.y.toFixed(2),
        r: length <= 180 ? 4 : 3,
        fill: style.stroke,
        stroke: colors.surface,
        "stroke-width": 0.8,
        opacity: style.opacity
      });
      point.appendChild(element("title", {}, structurePairTitle(result, pair, style) + " · matrix point"));
      svg.appendChild(point);
      if (pair.left !== pair.right) {
        const mirror = element("circle", {
          cx: second.x.toFixed(2),
          cy: second.y.toFixed(2),
          r: length <= 180 ? 4 : 3,
          fill: style.stroke,
          stroke: colors.surface,
          "stroke-width": 0.8,
          opacity: style.opacity
        });
        mirror.appendChild(element("title", {}, structurePairTitle(result, pair, style) + " · mirrored matrix point"));
        svg.appendChild(mirror);
      }
    });

    const selected = Number(selectedNucleotide);
    if (Number.isInteger(selected) && selected >= 1 && selected <= length) {
      const selectedX = matrixScale(selected, length, plotLeft, plotSize);
      const selectedY = matrixScale(selected, length, plotTop, plotSize);
      svg.appendChild(element("line", { x1: selectedX, y1: plotTop, x2: selectedX, y2: plotBottom, stroke: colors.accent, "stroke-width": 1.2, opacity: 0.55 }));
      svg.appendChild(element("line", { x1: plotLeft, y1: selectedY, x2: plotLeft + plotSize, y2: selectedY, stroke: colors.accent, "stroke-width": 1.2, opacity: 0.55 }));
    }
    appendMatrixPositionControls(svg, result, selectedNucleotide, plotLeft, plotBottom + 36, plotSize, colors);
    svg.appendChild(element("text", { x: plotLeft + plotSize / 2, y: height - 8, fill: colors.muted, "font-size": 11, "text-anchor": "middle", "font-family": "ui-monospace, monospace" }, "sequence position"));
    legend(svg, colors, result, { x: 44, y: 24 });
    return svg;
  }

  function structureSvg(width, height, label, title, colors, result) {
    const svg = element("svg", {
      xmlns: SVG_NS,
      viewBox: "0 0 " + width + " " + height,
      width: "100%",
      height: String(height),
      role: "group",
      "aria-label": label
    });
    svg.appendChild(element("title", {}, title));
    if (result) {
      svg.appendChild(element("desc", {}, diagramDescription(result, Math.max(1, (result.sequence || "").length))));
    }
    svg.appendChild(element("rect", { x: 0, y: 0, width: width, height: height, rx: 8, fill: colors.surface }));
    return svg;
  }

  function structureHardPairs(result) {
    return new Set((result.constraints || []).map(function (pair) {
      return pair.left + ":" + pair.right;
    }));
  }

  function structurePairStyle(result, pair, colors, hardPairs) {
    const constraints = hardPairs || structureHardPairs(result);
    const support = result.rawCounts ? result.rawCounts[pair.left - 1] : Number(pair.evidenceSupport) || 0;
    const guided = result.constraintMode === "hyb-guided";
    const cplfold = isCplfoldResult(result);
    const cplfoldEvidence = cplfold && result.evidence && result.evidence.source === "hyb-block-intervals";
    const maximumEvidence = result.rawCounts ? result.maximumRawCount : Math.max(0, Number(cplfoldEvidence ? result.evidence.maximumBonus : result.maximumNucleotideSupport) || 0);
    const hardConstraint = constraints.has(pair.left + ":" + pair.right);
    return {
      stroke: cplfold ? cplfoldPairColor(pair, colors) : (guided ? evidenceColor(support, maximumEvidence, colors) : pairColor(pair.type, colors)),
      strokeWidth: cplfold && pair.layer !== "primary" ? 3 : (hardConstraint ? 3.7 : (guided && support > 0 ? 2.5 : ((pair.type === "G–C" || pair.type === "C–G") ? 2.1 : 1.6))),
      opacity: cplfoldEvidence
        ? Math.max(0.32, Math.min(0.96, 0.32 + support / Math.max(maximumEvidence, 1e-12) * 0.64))
        : (guided && !support ? 0.34 : 0.9),
      dash: cplfold && pair.layer && pair.layer !== "primary" ? "5 3" : "",
      hardConstraint: hardConstraint
    };
  }

  function structurePairTitle(result, pair, style) {
    const cplfold = isCplfoldResult(result);
    const guided = result.constraintMode === "hyb-guided";
    const cplfoldEvidence = cplfold && result.evidence && result.evidence.source === "hyb-block-intervals";
    const prefix = cplfold
      ? (pair.layer || "primary") + " CPLfold pair: "
      : (style.hardConstraint ? (guided ? "HYB-fitted hard pair: " : "Manual hard pair: ") : "MFE pair: ");
    const support = Number(result.rawCounts ? pair.bonusSupport : pair.evidenceSupport) || 0;
    const evidence = guided ? "; RNAcofold evidence " + support : (cplfoldEvidence ? "; HYB bonus " + support.toFixed(4) : "");
    const raw = result.rawCounts ? "; raw pair count " + result.rawCounts[pair.left - 1] : "";
    return prefix + pair.left + " " + pair.leftBase + " paired with " + pair.right + " " + pair.rightBase + " (" + pair.type + ")" + evidence + raw;
  }

  function circlePoint(position, length, centerX, centerY, radius) {
    const angle = -Math.PI / 2 + (2 * Math.PI * (position - 1)) / Math.max(1, length);
    return {
      x: centerX + Math.cos(angle) * radius,
      y: centerY + Math.sin(angle) * radius
    };
  }

  function pairDepth(pair, pairs) {
    return pairs.reduce(function (depth, other) {
      return other.left < pair.left && other.right > pair.right ? depth + 1 : depth;
    }, 0);
  }

  function appendRingMarkers(svg, result, selectedNucleotide, centerX, centerY, radius, colors) {
    const length = Math.max(1, (result.sequence || "").length);
    if (length > 700) {
      appendInspectionTrack(svg, result, selectedNucleotide, 110, 602, 460, colors);
      return;
    }
    const markerRadius = length <= 120 ? 4.5 : (length <= 360 ? 3 : 1.9);
    for (let position = 1; position <= length; position += 1) {
      const point = circlePoint(position, length, centerX, centerY, radius);
      svg.appendChild(structureMarker(result, position, point.x, point.y, colors, selectedNucleotide, markerRadius));
    }
  }

  function appendSelectedRingRay(svg, length, centerX, centerY, radius, selectedNucleotide, colors) {
    const selected = Number(selectedNucleotide);
    if (!Number.isInteger(selected) || selected < 1 || selected > length) {
      return;
    }
    const point = circlePoint(selected, length, centerX, centerY, radius - 8);
    svg.appendChild(element("line", {
      x1: centerX,
      y1: centerY,
      x2: point.x.toFixed(2),
      y2: point.y.toFixed(2),
      stroke: colors.accent,
      "stroke-width": 1.2,
      "stroke-dasharray": "4 4",
      opacity: 0.45,
      "pointer-events": "none"
    }));
  }

  function appendCircularTicks(svg, length, centerX, centerY, radius, colors) {
    // P4: the first and last positions meet at the top of the ring, where
    // "1" and "600" were drawn over each other; they now sit either side of
    // the top tick ("600 | 1"), reading clockwise.
    const endsMeet = length > 1 && 1 / length < 0.08;
    tickPositions(length).forEach(function (position) {
      const inner = circlePoint(position, length, centerX, centerY, radius + 4);
      const outer = circlePoint(position, length, centerX, centerY, radius + 13);
      const label = circlePoint(position, length, centerX, centerY, radius + 32);
      let anchor = "middle";
      let labelX = label.x;
      if (endsMeet && position === 1) {
        anchor = "start";
        labelX = outer.x + 4;
      } else if (endsMeet && position === length) {
        anchor = "end";
        labelX = outer.x - 4;
      }
      svg.appendChild(element("line", { x1: inner.x, y1: inner.y, x2: outer.x, y2: outer.y, stroke: colors.muted, "stroke-width": 1 }));
      svg.appendChild(element("text", { class: "structure-ring-label", x: labelX, y: label.y + 4, fill: colors.muted, "font-size": 11, "text-anchor": anchor, "font-family": "ui-monospace, monospace" }, String(position)));
    });
  }

  function appendMatrixTicks(svg, length, plotLeft, plotTop, plotSize, colors) {
    tickPositions(length).forEach(function (position) {
      const x = matrixScale(position, length, plotLeft, plotSize);
      const y = matrixScale(position, length, plotTop, plotSize);
      svg.appendChild(element("line", { x1: x, y1: plotTop, x2: x, y2: plotTop + plotSize, stroke: colors.line, "stroke-width": 1, opacity: 0.45 }));
      svg.appendChild(element("line", { x1: plotLeft, y1: y, x2: plotLeft + plotSize, y2: y, stroke: colors.line, "stroke-width": 1, opacity: 0.45 }));
      svg.appendChild(element("text", { x: x, y: plotTop + plotSize + 17, fill: colors.muted, "font-size": 10, "text-anchor": "middle", "font-family": "ui-monospace, monospace" }, String(position)));
      svg.appendChild(element("text", { x: plotLeft - 9, y: y + 3, fill: colors.muted, "font-size": 10, "text-anchor": "end", "font-family": "ui-monospace, monospace" }, String(position)));
    });
    svg.appendChild(element("text", { x: plotLeft - 43, y: plotTop + plotSize / 2, fill: colors.muted, "font-size": 11, "text-anchor": "middle", transform: "rotate(-90 " + (plotLeft - 43) + " " + (plotTop + plotSize / 2) + ")" }, "position"));
  }

  function appendMatrixPositionControls(svg, result, selectedNucleotide, x, y, width, colors) {
    const length = Math.max(1, (result.sequence || "").length);
    if (length > 700) {
      appendInspectionTrack(svg, result, selectedNucleotide, x, y, width, colors);
      return;
    }
    const markerRadius = length <= 120 ? 4.5 : (length <= 360 ? 3 : 1.9);
    for (let position = 1; position <= length; position += 1) {
      const markerX = linearScale(position, length, x, width);
      svg.appendChild(element("line", { x1: markerX, y1: y - 7, x2: markerX, y2: y + 5, stroke: colors.muted, "stroke-width": 1 }));
      svg.appendChild(structureMarker(result, position, markerX, y, colors, selectedNucleotide, markerRadius));
    }
  }

  function structureMarker(result, position, x, y, colors, selectedNucleotide, radius) {
    const sequence = result.sequence || "";
    const selected = Number(selectedNucleotide) === position;
    const evidenceDriven = result.constraintMode === "hyb-guided" || (isCplfoldResult(result) && result.evidence && result.evidence.source === "hyb-block-intervals");
    const marker = element("circle", {
      cx: Number(x).toFixed(2),
      cy: Number(y).toFixed(2),
      r: radius,
      fill: selected ? colors.accent : (evidenceDriven ? nucleotideEvidenceColor(result, position, colors) : colors.surface),
      stroke: selected ? colors.accent : colors.muted,
      "stroke-width": selected ? 2.3 : 1.1,
      "data-structure-position": position,
      tabindex: position === rovingPosition(selectedNucleotide, sequence.length) ? 0 : -1,
      role: "button",
      "aria-current": selected ? "true" : "false",
      "aria-label": "Inspect nucleotide " + position + ", " + sequence.charAt(position - 1)
    });
    marker.appendChild(element("title", {}, nucleotideTitle(result, position)));
    return marker;
  }

  function appendInspectionTrack(svg, result, selectedNucleotide, x, y, width, colors) {
    const length = Math.max(1, (result.sequence || "").length);
    const selected = Number.isInteger(Number(selectedNucleotide))
      ? Math.max(1, Math.min(length, Number(selectedNucleotide)))
      : 1;
    svg.appendChild(element("line", { x1: x, y1: y, x2: x + width, y2: y, stroke: colors.lineStrong, "stroke-width": 2 }));
    const track = element("rect", {
      x: x,
      y: y - 14,
      width: width,
      height: 28,
      fill: "transparent",
      "data-structure-track": "true",
      tabindex: 0,
      role: "slider",
      "aria-label": "Inspect nucleotide along the sequence baseline",
      "aria-valuemin": 1,
      "aria-valuemax": length,
      "aria-valuenow": selected
    });
    track.appendChild(element("title", {}, "Click the sequence track or use the arrow keys, Page Up, Page Down, Home and End to inspect a nucleotide"));
    svg.appendChild(track);
    const thumbX = linearScale(selected, length, x, width);
    svg.appendChild(element("circle", { cx: thumbX, cy: y, r: 6, fill: colors.accent, stroke: colors.surface, "stroke-width": 2, "pointer-events": "none" }));
    svg.appendChild(element("text", { x: x, y: y + 30, fill: colors.muted, "font-size": 11, "font-family": "ui-monospace, monospace" }, "1"));
    svg.appendChild(element("text", { x: x + width, y: y + 30, fill: colors.muted, "font-size": 11, "text-anchor": "end", "font-family": "ui-monospace, monospace" }, String(length)));
  }

  function tickPositions(length) {
    const values = [1, Math.round(1 + (length - 1) * 0.25), Math.round(1 + (length - 1) * 0.5), Math.round(1 + (length - 1) * 0.75), length];
    return values.filter(function (position, index) {
      return values.indexOf(position) === index;
    });
  }

  function matrixPoint(one, two, length, plotLeft, plotTop, plotSize) {
    return {
      x: matrixScale(one, length, plotLeft, plotSize),
      y: matrixScale(two, length, plotTop, plotSize)
    };
  }

  function matrixScale(position, length, start, span) {
    return start + ((position - 1) / Math.max(1, length - 1)) * span;
  }

  function linearScale(position, length, start, span) {
    return start + ((position - 1) / Math.max(1, length - 1)) * span;
  }

  // The diagram for an image export, light whatever the page theme: the
  // drawn views (arc, radial, circular, matrix) are drawn again with the
  // light colours; the folded view (fornac) carries its own colours and is
  // copied.  Both get a white background (the page's is transparent).
  function lightExportSvg(state) {
    const shown = document.querySelector("[data-structure-diagram] svg");
    const result = state && state.structure && state.structure.result;
    if (!shown || !result) {
      return null;
    }
    const viewMode = resultViewMode(result, state.structure.viewMode);
    let svg = null;
    if (state.structure.editorEnabled && window.Hyb2StructureEditor) {
      const source = window.Hyb2StructureEditor.svg(window.Hyb2StructureEditor.session(state).data, null);
      svg = new DOMParser().parseFromString(source, "image/svg+xml").documentElement;
    }
    if (!svg && viewMode !== "folded") {
      exportColors = LIGHT_EXPORT_COLORS;
      try {
        svg = buildStructureDiagram(result, state.structure.selectedNucleotide, viewMode);
      } catch (error) {
        svg = null; // no SVG DOM to draw into (tests): export what is shown
      } finally {
        exportColors = null;
      }
    }
    if (!svg) {
      svg = typeof shown.cloneNode === "function" ? shown.cloneNode(true) : shown;
    }
    if (typeof svg.getAttribute === "function" && !svg.getAttribute("xmlns") && typeof svg.setAttribute === "function") {
      svg.setAttribute("xmlns", SVG_NS);
    }
    if (typeof document.createElementNS !== "function" || typeof svg.insertBefore !== "function") {
      return svg;
    }
    const box = String(svg.getAttribute("viewBox") || "").trim().split(/[\s,]+/).map(Number);
    const background = document.createElementNS(SVG_NS, "rect");
    if (box.length === 4 && box.every(Number.isFinite)) {
      background.setAttribute("x", String(box[0]));
      background.setAttribute("y", String(box[1]));
      background.setAttribute("width", String(box[2]));
      background.setAttribute("height", String(box[3]));
    } else {
      background.setAttribute("width", "100%");
      background.setAttribute("height", "100%");
    }
    background.setAttribute("fill", "#ffffff");
    svg.insertBefore(background, svg.firstChild);
    return svg;
  }

  function downloadStructurePng(api, viewMode, exportSource) {
    const svg = exportSource || document.querySelector("[data-structure-diagram] svg");
    if (!svg) {
      api.showToast("The structure diagram is not ready to export yet.");
      return;
    }

    let sourceUrl = "";
    let finished = false;
    function cleanUp() {
      if (sourceUrl) {
        URL.revokeObjectURL(sourceUrl);
        sourceUrl = "";
      }
    }
    function fail() {
      if (finished) {
        return;
      }
      finished = true;
      cleanUp();
      api.showToast("This browser could not create the structure PNG. Download the SVG instead.");
    }

    try {
      const dimensions = svgDimensions(svg);
      const scale = 2;
      // a light export copy is already a copy; the page's diagram is cloned
      const exportSvg = exportSource ? svg : svg.cloneNode(true);
      exportSvg.setAttribute("width", String(dimensions.width * scale));
      exportSvg.setAttribute("height", String(dimensions.height * scale));
      const source = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" + new XMLSerializer().serializeToString(exportSvg);
      sourceUrl = URL.createObjectURL(new Blob([source], { type: "image/svg+xml;charset=utf-8" }));

      const image = new Image();
      image.onload = function () {
        cleanUp();
        if (finished) {
          return;
        }
        try {
          const canvas = document.createElement("canvas");
          canvas.width = dimensions.width * scale;
          canvas.height = dimensions.height * scale;
          const context = canvas.getContext("2d");
          if (!context || typeof canvas.toBlob !== "function") {
            fail();
            return;
          }
          context.drawImage(image, 0, 0, canvas.width, canvas.height);
          canvas.toBlob(function (blob) {
            if (!blob) {
              fail();
              return;
            }
            try {
              api.downloadBlob("rna-secondary-structure-" + structureViewFileStem(viewMode) + ".png", blob);
              finished = true;
              api.showToast("Structure PNG downloaded locally.");
            } catch (error) {
              fail();
            }
          }, "image/png");
        } catch (error) {
          fail();
        }
      };
      image.onerror = fail;
      image.src = sourceUrl;
    } catch (error) {
      fail();
    }
  }

  function svgDimensions(svg) {
    const viewBox = String(svg.getAttribute("viewBox") || "").trim().split(/[\s,]+/).map(Number);
    if (viewBox.length === 4 && Number.isFinite(viewBox[2]) && viewBox[2] > 0 && Number.isFinite(viewBox[3]) && viewBox[3] > 0) {
      return { width: Math.ceil(viewBox[2]), height: Math.ceil(viewBox[3]) };
    }
    const width = Number.parseFloat(svg.getAttribute("width"));
    const height = Number.parseFloat(svg.getAttribute("height"));
    if (!Number.isFinite(width) || width <= 0 || !Number.isFinite(height) || height <= 0) {
      throw new Error("The structure SVG has no rasterisable dimensions.");
    }
    return { width: Math.ceil(width), height: Math.ceil(height) };
  }

  // The nucleotide markers are one Tab stop (the inspected nucleotide, else
  // the first); arrow keys move between them (bindNucleotideMarkers).
  function rovingPosition(selectedNucleotide, length) {
    const selected = Number(selectedNucleotide);
    return Number.isInteger(selected) && selected >= 1 && selected <= length ? selected : 1;
  }

  // The text description of a drawn view, read with its label.
  function diagramDescription(result, length) {
    const pairs = (result.pairs || []).length;
    const keys = length > 700
      ? "Tab to the sequence track, then use the arrow keys, Page Up, Page Down, Home and End to inspect a nucleotide."
      : "Tab to the nucleotide markers, then use the arrow keys, Page Up, Page Down, Home and End to move between nucleotides.";
    return length + " nucleotides, " + pairs + " base pair" + (pairs === 1 ? "" : "s") + ". " + keys;
  }

  function markerTicks(svg, result, length, left, right, baseline, colors, selectedNucleotide) {
    const sequence = result.sequence || "";
    const count = length <= 36 ? length : 5;
    for (let index = 0; index < count; index += 1) {
      const position = count === length ? index + 1 : Math.round(1 + (index / (count - 1 || 1)) * (length - 1));
      const x = xFor(position, length, left, right);
      svg.appendChild(element("line", { x1: x, y1: baseline - 4, x2: x, y2: baseline + 7, stroke: colors.muted, "stroke-width": 1 }));
      svg.appendChild(element("text", { x: x, y: baseline + 23, fill: colors.muted, "font-size": 11, "text-anchor": "middle", "font-family": "ui-monospace, monospace" }, length <= 36 ? sequence.charAt(position - 1) + " " + position : String(position)));
    }

    if (length <= 700) {
      for (let position = 1; position <= length; position += 1) {
        const x = xFor(position, length, left, right);
        const selected = Number(selectedNucleotide) === position;
        const marker = element("circle", {
          cx: x.toFixed(2),
          cy: baseline,
          r: length <= 120 ? 4.5 : 3.2,
          fill: selected ? colors.accent : ((result.constraintMode === "hyb-guided" || (isCplfoldResult(result) && result.evidence && result.evidence.source === "hyb-block-intervals")) ? nucleotideEvidenceColor(result, position, colors) : colors.surface),
          stroke: selected ? colors.accent : colors.muted,
          "stroke-width": selected ? 2.3 : 1.1,
          "data-structure-position": position,
          tabindex: position === rovingPosition(selectedNucleotide, length) ? 0 : -1,
          role: "button",
          "aria-current": selected ? "true" : "false",
          "aria-label": "Inspect nucleotide " + position + ", " + sequence.charAt(position - 1)
        });
        marker.appendChild(element("title", {}, nucleotideTitle(result, position)));
        svg.appendChild(marker);
      }
    } else {
      const selected = Number.isInteger(Number(selectedNucleotide))
        ? Math.max(1, Math.min(length, Number(selectedNucleotide)))
        : 1;
      const track = element("rect", {
        x: left,
        y: baseline - 12,
        width: right - left,
        height: 24,
        fill: "transparent",
        "data-structure-track": "true",
        tabindex: 0,
        role: "slider",
        "aria-label": "Inspect nucleotide along the sequence baseline",
        "aria-valuemin": 1,
        "aria-valuemax": length,
        "aria-valuenow": selected
      });
      track.appendChild(element("title", {}, "Click the baseline or use the arrow keys, Page Up, Page Down, Home and End to inspect a nucleotide"));
      svg.appendChild(track);
    }
  }

  function bindNucleotideMarkers(target, state, api) {
    if (!api) {
      return;
    }
    const markerCount = target.querySelectorAll("[data-structure-position]").length;
    Array.from(target.querySelectorAll("[data-structure-position]")).forEach(function (marker) {
      function selectMarker(position) {
        state.structure.selectedNucleotide = position;
        api.render();
        const replacement = document.querySelector('[data-structure-position="' + position + '"]');
        if (replacement) {
          replacement.focus();
        }
      }
      const own = Number(marker.getAttribute("data-structure-position"));
      marker.addEventListener("click", function () { selectMarker(own); });
      // One Tab stop for all markers: arrows, Page Up/Down (10) and
      // Home/End move the inspected nucleotide and the focus with it.
      marker.addEventListener("keydown", function (event) {
        if (event.altKey || event.ctrlKey || event.metaKey) return;
        const next = nextPosition(event.key, own, markerCount);
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          selectMarker(own);
        } else if (next !== null) {
          event.preventDefault();
          selectMarker(next);
        }
      });
    });
    const track = target.querySelector("[data-structure-track]");
    if (track) {
      const length = state.structure.result.sequence.length;
      function selectPosition(position, restoreFocus) {
        state.structure.selectedNucleotide = Math.max(1, Math.min(length, position));
        api.render();
        if (restoreFocus) {
          const replacement = document.querySelector("[data-structure-track]");
          if (replacement) {
            replacement.focus();
          }
        }
      }
      track.addEventListener("click", function (event) {
        const bounds = track.getBoundingClientRect();
        const ratio = bounds.width > 0 ? Math.max(0, Math.min(1, (event.clientX - bounds.left) / bounds.width)) : 0;
        selectPosition(Math.round(1 + ratio * (length - 1)), true);
      });
      track.addEventListener("keydown", function (event) {
        const current = Number.isInteger(Number(state.structure.selectedNucleotide))
          ? Number(state.structure.selectedNucleotide)
          : 1;
        const next = event.altKey || event.ctrlKey || event.metaKey ? null : nextPosition(event.key, current, length);
        if (next !== null) {
          event.preventDefault();
          selectPosition(next, true);
        }
      });
    }
  }

  // The position a navigation key moves to, or null for other keys.
  function nextPosition(key, current, length) {
    const step = { ArrowLeft: -1, ArrowDown: -1, ArrowRight: 1, ArrowUp: 1, PageDown: -10, PageUp: 10 }[key];
    if (key === "Home") return 1;
    if (key === "End") return length;
    return step ? Math.max(1, Math.min(length, current + step)) : null;
  }

  function nucleotideTitle(result, position) {
    const pair = (result.pairs || []).find(function (entry) { return entry.left === position || entry.right === position; });
    const base = result.sequence.charAt(position - 1);
    if (!pair) {
      return position + " " + base + ": unpaired";
    }
    const partner = pair.left === position ? pair.right : pair.left;
    return position + " " + base + ": paired with " + partner + " (" + pair.type + ")";
  }

  function legend(svg, colors, result, options) {
    const settings = options || {};
    const startX = settings.x == null ? 44 : settings.x;
    const y = settings.y == null ? 24 : settings.y;
    const guided = result.constraintMode === "hyb-guided";
    const cplfold = isCplfoldResult(result);
    const cplfoldAllowsPseudoknot = cplfold && resultAllowsPseudoknot(result);
    const spacing = settings.spacing == null ? ((guided || cplfold) ? 132 : 98) : settings.spacing;
    const items = cplfold
      ? (cplfoldAllowsPseudoknot
        ? [{ label: "phase 1", color: colors.primary }, { label: "pseudoknot", color: colors.accent }, { label: "deeper layer", color: colors.warning }]
        : [{ label: "phase 1", color: colors.primary }])
      : guided
      ? [{ label: "high evidence", color: colors.warning }, { label: "low evidence", color: colors.primary }, { label: "MFE only", color: colors.lineStrong }]
      : [{ label: "G–C", color: colors.primary }, { label: "A–U", color: colors.accent }, { label: "G–U", color: colors.muted }];
    // settings.roomy (ring views): each item leaves room for its label at
    // the larger phone size (fix-structure.css, about 20 units of monospace)
    let x = startX;
    items.forEach(function (item) {
      svg.appendChild(element("line", { x1: x, y1: y, x2: x + 17, y2: y, stroke: item.color, "stroke-width": 2.5, "stroke-linecap": "round" }));
      svg.appendChild(element("text", { class: "structure-legend-label", x: x + 24, y: y + 4, fill: colors.muted, "font-size": 11, "font-family": "ui-monospace, monospace" }, item.label));
      x += settings.roomy ? Math.max(spacing, 24 + item.label.length * 12.5 + 20) : spacing;
    });
  }

  function element(name, attributes, content) {
    const node = document.createElementNS(SVG_NS, name);
    Object.keys(attributes || {}).forEach(function (key) { node.setAttribute(key, String(attributes[key])); });
    if (content !== undefined) {
      node.textContent = content;
    }
    return node;
  }

  function xFor(position, length, left, right) {
    return length <= 1 ? (left + right) / 2 : left + ((position - 1) / (length - 1)) * (right - left);
  }

  // image exports are drawn in the light theme whatever the page's theme
  // (as the other pages' exports): these are styles.css's light values
  const LIGHT_EXPORT_COLORS = {
    ink: "#222222",
    primary: "#3C5488",
    accent: "#E64B35",
    muted: "#5b6c80",
    line: "#dce7ec",
    lineStrong: "#c2d3db",
    warning: "#9c5e12",
    surface: "#f7fafb"
  };
  let exportColors = null;

  function colorTokens() {
    if (exportColors) {
      return Object.assign({}, exportColors);
    }
    const style = getComputedStyle(document.documentElement);
    return {
      ink: style.getPropertyValue("--ink").trim(),
      primary: style.getPropertyValue("--primary").trim(),
      accent: style.getPropertyValue("--accent").trim(),
      muted: style.getPropertyValue("--muted").trim(),
      line: style.getPropertyValue("--line").trim(),
      lineStrong: style.getPropertyValue("--line-strong").trim(),
      warning: style.getPropertyValue("--warning").trim(),
      surface: style.getPropertyValue("--surface-subtle").trim()
    };
  }

  function pairColor(type, colors) {
    if (type === "G–C" || type === "C–G") {
      return colors.primary;
    }
    if (type === "A–U" || type === "U–A") {
      return colors.accent;
    }
    return colors.muted;
  }

  function cplfoldPairColor(pair, colors) {
    if (pair.layer === "pseudoknot-1") {
      return colors.accent;
    }
    if (pair.layer && pair.layer !== "primary") {
      return colors.warning;
    }
    return colors.primary;
  }

  function evidenceColor(support, maximum, colors) {
    if (!(support > 0) || !(maximum > 0)) {
      return colors.lineStrong;
    }
    return support / maximum >= 0.55 ? colors.warning : colors.primary;
  }

  function nucleotideEvidenceColor(result, position, colors) {
    if (result.rawCounts) return evidenceColor(result.rawCounts[position - 1], result.maximumRawCount, colors);
    const support = result.nucleotideSupport && Number(result.nucleotideSupport[position - 1]) || 0;
    return evidenceColor(support, Number(result.maximumNucleotideSupport) || 0, colors);
  }

  function dotBracketFile(result) {
    const energy = Number.isFinite(Number(result.energy)) ? " (" + Number(result.energy).toFixed(2) + ")" : "";
    return ">" + (result.label || ("HYB2_Web_" + (result.engine || "ViennaRNA_MFE"))) + "\n" + withStrandBreak(result.sequence, result.cutpoint) + "\n" + withStrandBreak(result.dotBracket, result.cutpoint) + energy + "\n";
  }

  function basePairTsv(result) {
    const hardPairs = new Set((result.constraints || []).map(function (pair) {
      return pair.left + ":" + pair.right;
    }));
    const cplfold = isCplfoldResult(result);
    const guided = result.constraintMode === "hyb-guided";
    const rawCounts = result.comradesPairCounts;
    const evidenceKind = cplfold ? window.Hyb2Pages.cplfoldBonusKind(result.evidence)
      : (guided ? "rna_cofold_observations" : "none");
    // Keep the original ViennaRNA columns first for downstream compatibility,
    // then add engine-neutral, explicitly typed fields.
    const rows = [["left_position", "left_base", "right_position", "right_base", "pair_type", "hard_constraint", "rna_cofold_evidence", "mfe_kcal_per_mol", "evidence_kind", "evidence_support", "pair_layer", "selected_energy_kcal_per_mol", "engine"]];
    if (result.cutpoint) rows[0].push("cutpoint", "left_strand", "left_strand_position", "right_strand", "right_strand_position");
    if (rawCounts) rows[0].push("comrades_pair_count");
    (result.pairs || []).forEach(function (pair, index) {
      const support = Number(rawCounts ? pair.bonusSupport : pair.evidenceSupport) || 0;
      rows.push([
        pair.left,
        pair.leftBase,
        pair.right,
        pair.rightBase,
        pair.type,
        hardPairs.has(pair.left + ":" + pair.right) ? "yes" : "no",
        cplfold ? "" : support,
        cplfold ? "" : (Number.isFinite(Number(result.energy)) ? Number(result.energy).toFixed(2) : ""),
        evidenceKind,
        support,
        pair.layer || "nested",
        Number.isFinite(Number(result.energy)) ? Number(result.energy).toFixed(2) : "",
        result.engine || "ViennaRNA"
      ]);
      if (result.cutpoint) rows[rows.length - 1].push(result.cutpoint,
        pair.left <= result.cutpoint ? "A" : "B", pair.left <= result.cutpoint ? pair.left : pair.left - result.cutpoint,
        pair.right <= result.cutpoint ? "A" : "B", pair.right <= result.cutpoint ? pair.right : pair.right - result.cutpoint);
      if (rawCounts) rows[rows.length - 1].push(rawCounts[index]);
    });
    return rows.map(function (row) { return row.join("\t"); }).join("\n") + "\n";
  }

  function evidenceTsv(result, scored) {
    if (scored) {
      const evidence = result.evidence;
      return "# scoring_version=" + (evidence.scoringVersion || "unrecorded") + "\nprepared_left\tprepared_right\traw_pair_count\n" +
        evidence.scoredEvidencePairs.map(pair => [pair.one, pair.two, pair.count].join("\t") + "\n").join("");
    }
    const rows = [["prepared_position_1", "prepared_position_2", "hyb_row_observations", "example_record_ids"]];
    const evidence = result.evidence && result.evidence.selectedBasePairs || [];
    evidence.forEach(function (pair) {
      rows.push([pair.one, pair.two, pair.count, (pair.recordIds || []).join(",")]);
    });
    return rows.map(function (row) { return row.join("\t"); }).join("\n") + "\n";
  }

  function constraintsTsv(result) {
    const rows = [["constraint_id", "original_rank", "command", "left", "right", "stem_length", "evidence_support", "accepted"]];
    (result.acceptedStemConstraints || []).forEach(function (constraint) {
      rows.push([constraint.id, constraint.rank, "F " + constraint.left + " " + constraint.right + " " + constraint.length, constraint.left, constraint.right, constraint.length, constraint.support, "yes"]);
    });
    (result.rejectedStemConstraints || []).forEach(function (constraint) {
      rows.push([constraint.id, constraint.rank, "", "", "", "", "", "no:" + constraint.reason]);
    });
    return rows.map(function (row) { return row.join("\t"); }).join("\n") + "\n";
  }

  function ensembleTsv(result) {
    const rows = [["run", "randomised", "selected", "mfe_kcal_per_mol", "comrades_score", "matched_evidence_pairs", "accepted_stem_constraints", "rejected_stem_constraints", "dot_bracket"]];
    const randomisation = result.randomisation || {};
    (randomisation.structures || []).forEach(function (entry) {
      rows.push([
        entry.run,
        entry.randomised ? "yes" : "no",
        entry.run === randomisation.selectedRun ? "yes" : "no",
        // ViennaRNA energies are float32; print them as the UI, DBN and CT do
        Number.isFinite(Number(entry.energy)) && entry.energy !== null && entry.energy !== "" ? Number(entry.energy).toFixed(2) : "",
        entry.comradesScore,
        entry.matchedEvidencePairs,
        (entry.acceptedStemConstraints || []).join(","),
        entry.rejectedStemConstraints,
        entry.dotBracket
      ]);
    });
    return rows.map(function (row) { return row.join("\t"); }).join("\n") + "\n";
  }

  function cplfoldEvidenceTsv(result) {
    const evidence = result.evidence || {};
    const sequenceLength = result.sequence ? String(result.sequence).length : Number(evidence.sequenceLength) || 0;
    const kernel = evidence.supportKernel || "gaussian";
    // Preserve the original Gaussian header only for a Gaussian matrix.
    const rows = [
      "# HYB2 CPLfold bonus matrix",
      "# sequence_length=" + sequenceLength,
      "# coordinate_system=prepared-sequence-1-based-inclusive",
      "# transform=" + (evidence.transform || (kernel === "gaussian" ? "IRIS-style Gaussian arm blocks; symmetric outer product; threshold 1e-6; log1p" : window.Hyb2Pages.cplfoldBonusKind(evidence)))
    ];
    if (kernel !== "gaussian") rows.push("# support_kernel=" + kernel);
    if (result.cutpoint) rows.push("# cutpoint=" + result.cutpoint, "# strand_lengths=" + result.cutpoint + "," + (sequenceLength - result.cutpoint), "# cofold_evidence_smoothing=within_each_strand");
    rows.push(["prepared_position_1", "prepared_position_2", kernel === "gaussian" ? "log1p_gaussian_hyb_bonus" : window.Hyb2Pages.cplfoldBonusKind(evidence)].join("\t"));
    (evidence.bonusEntries || []).forEach(function (entry) {
      rows.push([entry.one, entry.two, result.cutpoint ? Number(entry.value).toPrecision(17) : Number(entry.value).toFixed(8)].join("\t"));
    });
    return rows.join("\n") + "\n";
  }

  function kcal(value) {
    return value != null && value !== "" && Number.isFinite(Number(value)) ? Number(value).toFixed(2) : "NA";
  }

  function cplfoldCandidatesTsv(result) {
    const rows = [["rank", "selected", "type", "topology", "energy_kcal_per_mol", "effective_energy_kcal_per_mol", "phase1_energy_kcal_per_mol", "linear_fold_score", "base_pairs", "crossing_pairs", "dot_bracket"]];
    const scored = (result.candidates || []).some(candidate => candidate.comradesScore != null);
    (result.candidates || []).forEach(function (candidate, index) {
      rows.push([
        index + 1,
        index === Number(result.selectedCandidate || 0) ? "yes" : "no",
        candidate.type,
        candidate.topology,
        // kcal/mol (and the kcal/mol-scale fold score) to 2 decimals, as the pairs TSV; NA when missing
        kcal(candidate.energy),
        kcal(candidate.effectiveEnergy),
        kcal(candidate.phase1Energy),
        kcal(candidate.score),
        (candidate.pairs || []).length,
        candidate.crossingPairs || 0,
        withStrandBreak(candidate.dotBracket, result.cutpoint)
      ]);
      if (result.cutpoint) rows[rows.length - 1].push(result.cutpoint);
      if (scored) rows[rows.length - 1].push(result.evidence?.supportKernel, candidate.comradesScore);
    });
    if (result.cutpoint) rows[0].push("cutpoint");
    if (scored) rows[0].push("support_kernel", "comrades_score");
    return rows.map(function (row) { return row.join("\t"); }).join("\n") + "\n";
  }

  function ctFile(result) {
    const sequence = result.sequence || "";
    const pairLookup = new Map();
    (result.pairs || []).forEach(function (pair) {
      pairLookup.set(pair.left, pair.right);
      pairLookup.set(pair.right, pair.left);
    });
    const energy = Number.isFinite(Number(result.energy)) ? Number(result.energy).toFixed(2) : "NA";
    const rows = [sequence.length + " ENERGY = " + energy + " " + (result.label || ("HYB2_Web_" + (result.engine || "ViennaRNA_MFE")))];
    for (let position = 1; position <= sequence.length; position += 1) {
      rows.push([
        position,
        sequence.charAt(position - 1),
        position === 1 || position === result.cutpoint + 1 ? 0 : position - 1,
        position === sequence.length || position === result.cutpoint ? 0 : position + 1,
        pairLookup.get(position) || 0,
        position
      ].join("\t"));
    }
    return rows.join("\n") + "\n";
  }

  // The state as seen by the evidence datasets of a guided fold, so its
  // provenance names those files rather than the current header scope.
  function evidenceSourceState(state, result) {
    const ids = result.evidenceDatasetIds || [];
    const datasets = (state.datasets || []).filter(function (dataset) { return ids.indexOf(dataset.id) !== -1; });
    if (!datasets.length) {
      return state;
    }
    const fingerprint = function (dataset) {
      return dataset.sha256 || (dataset.primary && state.primarySummary && state.primarySummary.sha256) || null;
    };
    const summary = datasets.length === 1 ? {
      fileName: datasets[0].fileName,
      fileSize: datasets[0].fileSize,
      sha256: fingerprint(datasets[0]),
      sha256Unavailable: !!datasets[0].sha256Unavailable
    } : {
      fileName: datasets.map(function (dataset) { return dataset.fileName; }).join(" + "),
      fileSize: datasets.reduce(function (total, dataset) { return total + (Number(dataset.fileSize) || 0); }, 0)
    };
    return Object.assign({}, state, { summary: summary, selection: { datasetIds: datasets.map(function (dataset) { return dataset.id; }) } });
  }

  window.Hyb2StructureUI = {
    afterRender: afterRender,
    handleAction: handleAction,
    structureReport: structureReport,
    basePairTsv: basePairTsv,
    evidenceTsv: evidenceTsv,
    constraintsTsv: constraintsTsv,
    ensembleTsv: ensembleTsv,
    cplfoldEvidenceTsv: cplfoldEvidenceTsv,
    cplfoldCandidatesTsv: cplfoldCandidatesTsv,
    localCplfoldCommand: localCplfoldCommand,
    selectCplfoldCandidate: selectCplfoldCandidate,
    buildStructureDiagram: buildStructureDiagram
  };
}());
