(function () {
  "use strict";

  const SVG_NS = "http://www.w3.org/2000/svg";

  function afterRender(state, api) {
    const result = state.structure && state.structure.result;
    const target = document.querySelector("[data-structure-diagram]");
    if (!result || !target) {
      return;
    }

    target.replaceChildren(buildArcDiagram(result, state.structure.selectedNucleotide));
    bindNucleotideMarkers(target, state, api);
  }

  function handleAction(action, state, api, element) {
    if (action === "predict-structure") {
      if (!window.Hyb2Structure) {
        api.showToast("The local structure predictor is unavailable in this browser.");
        return true;
      }
      window.Hyb2Structure.predict(state, api);
      return true;
    }

    if (action === "cancel-structure") {
      if (window.Hyb2Structure) {
        window.Hyb2Structure.cancel(state);
      }
      api.render();
      api.showToast("Local structure prediction cancelled.");
      return true;
    }

    const result = state.structure && state.structure.result;
    if (action === "select-cplfold-candidate") {
      const candidateIndex = Number(element && element.dataset && element.dataset.candidateIndex);
      if (!result || result.engine !== "CPLfold" || !selectCplfoldCandidate(result, candidateIndex)) {
        api.showToast("That CPLfold candidate is unavailable.");
        return true;
      }
      state.structure.selectedNucleotide = null;
      api.render();
      api.showToast("CPLfold candidate " + (candidateIndex + 1) + " selected.");
      return true;
    }
    if (!result || [
      "copy-structure-dot-bracket",
      "download-structure-dot-bracket",
      "download-structure-ct",
      "download-structure-pairs",
      "download-structure-evidence",
      "download-structure-constraints",
      "download-structure-ensemble",
      "download-cplfold-evidence",
      "download-cplfold-candidates",
      "download-structure-svg",
      "download-structure-png",
      "download-structure-report"
    ].indexOf(action) === -1) {
      return false;
    }

    if (action === "copy-structure-dot-bracket") {
      api.copyText(result.dotBracket, "Dot-bracket notation copied to the clipboard.");
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
      api.showToast("RNAcofold evidence table downloaded locally.");
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
      api.download("cplfold-hyb-bonus-matrix.tsv", cplfoldEvidenceTsv(result), "text/tab-separated-values");
      api.showToast("CPLfold HYB bonus matrix downloaded locally.");
      return true;
    }

    if (action === "download-cplfold-candidates") {
      api.download("cplfold-candidates.tsv", cplfoldCandidatesTsv(result), "text/tab-separated-values");
      api.showToast("CPLfold candidates downloaded locally.");
      return true;
    }

    if (action === "download-structure-svg") {
      const svg = document.querySelector("[data-structure-diagram] svg");
      if (!svg) {
        api.showToast("The structure diagram is not ready to export yet.");
        return true;
      }
      api.download("rna-secondary-structure-arcs.svg", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" + new XMLSerializer().serializeToString(svg), "image/svg+xml");
      api.showToast("Structure SVG downloaded locally.");
      return true;
    }

    if (action === "download-structure-png") {
      downloadStructurePng(api);
      return true;
    }

    api.download("rna-secondary-structure-report.json", JSON.stringify(structureReport(state, result), null, 2), "application/json");
    api.showToast("Structure report downloaded locally.");
    return true;
  }

  function selectCplfoldCandidate(result, index) {
    const candidates = result.candidates || [];
    if (!Number.isInteger(index) || index < 0 || index >= candidates.length) {
      return false;
    }
    const candidate = candidates[index];
    result.selectedCandidate = index;
    result.dotBracket = candidate.dotBracket;
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
    const cplfold = result.engine === "CPLfold";
    const selectedCandidateIndex = cplfold ? Math.max(0, Number(result.selectedCandidate) || 0) : null;
    return {
      application: "HYB2 Web Lite",
      version: "0.5.0",
      buildCommit: window.HYB2_BUILD && window.HYB2_BUILD.commit || null,
      generatedLocally: true,
      sourceHyb: {
        fileName: state.summary.fileName,
        fileSize: state.summary.fileSize == null ? null : state.summary.fileSize,
        sha256: state.summary.sha256 || null,
        sha256Status: state.summary.sha256
          ? "available"
          : (state.summary.sha256Unavailable ? "unavailable" : "pending")
      },
      sourceFasta: result.source === "reference" && state.fasta ? {
        fileName: state.fasta.fileName,
        fileSize: state.fasta.fileSize == null ? null : state.fasta.fileSize,
        sha256: state.fasta.sha256 || null,
        sha256Status: state.fasta.sha256
          ? "available"
          : (state.fasta.sha256Unavailable ? "unavailable" : "pending")
      } : null,
      source: {
        label: result.label,
        type: result.source,
        sequence: result.sequence,
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
        mfe: cplfold ? null : result.energy,
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
        pairs: result.pairs,
        elapsedMs: result.elapsedMs,
        runtimeLoadMs: cplfold ? result.runtimeLoadMs : null,
        foldElapsedMs: cplfold ? result.foldElapsedMs : null,
        comradesScore: guided ? result.comradesScore : null,
        matchedEvidencePairs: guided ? result.matchedEvidencePairs : null,
        structureType: cplfold ? result.structureType : null,
        topology: cplfold ? result.topology : "nested",
        crossingPairs: cplfold ? result.crossingPairs : 0,
        selectedCandidateIndex: selectedCandidateIndex,
        selectedCandidateRank: cplfold ? selectedCandidateIndex + 1 : null,
        evidenceMode: cplfold ? result.evidenceMode : null,
        evidenceSource: cplfold ? result.evidenceSource : null
      },
      hybGuidedEvidence: guided ? result.evidence : null,
      cplfoldEvidence: cplfold ? result.evidence : null,
      cplfoldParameters: cplfold ? result.parameters : null,
      cplfoldCandidates: cplfold ? result.candidates : null,
      randomisation: guided ? result.randomisation : null,
      acceptedStemConstraints: guided ? result.acceptedStemConstraints : null,
      rejectedStemConstraints: guided ? result.rejectedStemConstraints : null,
      methodScope: {
        manualHardBasePairsApplied: !guided && !cplfold && constraintCount > 0,
        automaticHybEvidenceConstraintGeneration: guided,
        automaticRnaCofoldEvidenceSelection: guided,
        iterativeCompatibleConstraintFitting: guided,
        randomisedConstraintOrderFolding: guided && !!(result.randomisation && result.randomisation.completedFolds),
        supportBasedStructureScoring: guided,
        cplfoldPurePythonBrowserExecution: cplfold,
        cplfoldPseudoknotPrediction: cplfold,
        cplfoldHybBlockBonusMatrix: cplfold && result.evidenceSource === "hyb-block-bonus-matrix",
        numbaJitAvailable: cplfold ? false : null,
        browserCplfoldMaximumLength: cplfold ? 75 : null,
        unafoldExecution: "external-cli-only",
        note: cplfold
          ? "The browser ran the vendored pure-Python CPLfold two-phase algorithm in Pyodide. HYB-guided runs transform prepared arm intervals with the CPLfold IRIS-style Gaussian, symmetric outer-product and log1p bonus pipeline. Numba JIT is unavailable in this runtime, so sequences are limited to 75 nt."
          : guided
          ? "The browser reproduced the ViennaRNA HYB2 post-HYB chain: per-row RNAcofold evidence, ranked stem construction, greedy compatible hard-constraint fitting, optional seeded randomised folds, and COMRADES support scoring."
          : constraintCount
          ? "The listed hard base pairs were entered manually by the user and enforced by ViennaRNA before MFE folding. They were not inferred from HYB or RNAcofold evidence."
          : "This is an unconstrained ViennaRNA global MFE fold. No HYB or RNAcofold evidence-derived constraints were generated."
      }
    };
  }

  function buildArcDiagram(result, selectedNucleotide) {
    const width = 920;
    const height = 300;
    const left = 42;
    const right = width - 28;
    const baseline = 244;
    const sequence = result.sequence || "";
    const length = sequence.length || 1;
    const colors = colorTokens();
    const maximumEvidence = Math.max(0, Number(result.maximumNucleotideSupport) || 0);
    const guided = result.constraintMode === "hyb-guided";
    const cplfold = result.engine === "CPLfold";
    const cplfoldEvidence = cplfold && result.evidence && result.evidence.source === "hyb-block-intervals";
    const hardPairs = new Set((result.constraints || []).map(function (pair) {
      return pair.left + ":" + pair.right;
    }));
    const svg = element("svg", {
      xmlns: SVG_NS,
      viewBox: "0 0 " + width + " " + height,
      width: "100%",
      height: "300",
      role: "img",
      "aria-label": "Arc diagram for " + length + " nucleotide RNA secondary structure"
    });

    svg.appendChild(element("title", {}, cplfold ? "Selected CPLfold RNA structure with pseudoknot layers" : "Predicted non-crossing RNA base pairs"));
    svg.appendChild(element("rect", { x: 0, y: 0, width: width, height: height, rx: 8, fill: colors.surface }));
    svg.appendChild(element("line", { x1: left, y1: baseline, x2: right, y2: baseline, stroke: colors.line, "stroke-width": 1.5 }));

    (result.pairs || []).forEach(function (pair) {
      const hardConstraint = hardPairs.has(pair.left + ":" + pair.right);
      const start = xFor(pair.left, length, left, right);
      const end = xFor(pair.right, length, left, right);
      const center = (start + end) / 2;
      const span = Math.max(1, pair.right - pair.left);
      const lift = Math.min(195, 24 + Math.sqrt(span / length) * 190);
      const path = element("path", {
        d: "M " + start.toFixed(2) + " " + baseline + " Q " + center.toFixed(2) + " " + (baseline - lift).toFixed(2) + " " + end.toFixed(2) + " " + baseline,
        fill: "none",
        stroke: cplfold ? cplfoldPairColor(pair, colors) : (guided ? evidenceColor(Number(pair.evidenceSupport) || 0, maximumEvidence, colors) : pairColor(pair.type, colors)),
        "stroke-width": cplfold && pair.layer !== "primary" ? 3 : (hardConstraint ? 3.7 : (guided && Number(pair.evidenceSupport) > 0 ? 2.5 : (pair.type === "G–C" || pair.type === "C–G" ? 2.1 : 1.6))),
        "stroke-linecap": "round",
        opacity: cplfoldEvidence
          ? Math.max(0.32, Math.min(0.96, 0.32 + (Number(pair.evidenceSupport) || 0) / Math.max(maximumEvidence, 1e-12) * 0.64))
          : (guided && !Number(pair.evidenceSupport) ? 0.34 : 0.9)
      });
      path.appendChild(element("title", {}, (cplfold
        ? (pair.layer || "primary") + " CPLfold pair: "
        : (hardConstraint ? (guided ? "HYB-fitted hard pair: " : "Manual hard pair: ") : "MFE pair: ")) + pair.left + " " + pair.leftBase + " paired with " + pair.right + " " + pair.rightBase + " (" + pair.type + ")" + (guided ? "; RNAcofold evidence " + (Number(pair.evidenceSupport) || 0) : (cplfoldEvidence ? "; HYB bonus " + (Number(pair.evidenceSupport) || 0).toFixed(4) : ""))));
      svg.appendChild(path);
    });

    markerTicks(svg, result, length, left, right, baseline, colors, selectedNucleotide);
    legend(svg, colors, result);
    return svg;
  }

  function downloadStructurePng(api) {
    const svg = document.querySelector("[data-structure-diagram] svg");
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
      const exportSvg = svg.cloneNode(true);
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
              api.downloadBlob("rna-secondary-structure-arcs.png", blob);
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
          fill: selected ? colors.accent : ((result.constraintMode === "hyb-guided" || (result.engine === "CPLfold" && result.evidence && result.evidence.source === "hyb-block-intervals")) ? nucleotideEvidenceColor(result, position, colors) : colors.surface),
          stroke: selected ? colors.accent : colors.muted,
          "stroke-width": selected ? 2.3 : 1.1,
          "data-structure-position": position,
          tabindex: 0,
          role: "button",
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
      track.appendChild(element("title", {}, "Click the baseline or use Left, Right, Home and End to inspect a nucleotide"));
      svg.appendChild(track);
    }
  }

  function bindNucleotideMarkers(target, state, api) {
    if (!api) {
      return;
    }
    Array.from(target.querySelectorAll("[data-structure-position]")).forEach(function (marker) {
      function selectMarker() {
        const position = Number(marker.getAttribute("data-structure-position"));
        state.structure.selectedNucleotide = position;
        api.render();
        const replacement = document.querySelector('[data-structure-position="' + position + '"]');
        if (replacement) {
          replacement.focus();
        }
      }
      marker.addEventListener("click", selectMarker);
      marker.addEventListener("keydown", function (event) {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          selectMarker();
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
        let next = current;
        if (event.key === "ArrowLeft" || event.key === "ArrowDown") next -= 1;
        if (event.key === "ArrowRight" || event.key === "ArrowUp") next += 1;
        if (event.key === "Home") next = 1;
        if (event.key === "End") next = length;
        if (next !== current || event.key === "Home" || event.key === "End") {
          event.preventDefault();
          selectPosition(next, true);
        }
      });
    }
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

  function legend(svg, colors, result) {
    const guided = result.constraintMode === "hyb-guided";
    const cplfold = result.engine === "CPLfold";
    const items = cplfold
      ? [{ label: "phase 1", color: colors.primary }, { label: "pseudoknot", color: colors.accent }, { label: "deeper layer", color: colors.warning }]
      : guided
      ? [{ label: "high evidence", color: colors.warning }, { label: "low evidence", color: colors.primary }, { label: "MFE only", color: colors.lineStrong }]
      : [{ label: "G–C", color: colors.primary }, { label: "A–U", color: colors.accent }, { label: "G–U", color: colors.muted }];
    items.forEach(function (item, index) {
      const x = 44 + index * ((guided || cplfold) ? 132 : 98);
      svg.appendChild(element("line", { x1: x, y1: 24, x2: x + 17, y2: 24, stroke: item.color, "stroke-width": 2.5, "stroke-linecap": "round" }));
      svg.appendChild(element("text", { x: x + 24, y: 28, fill: colors.muted, "font-size": 11, "font-family": "ui-monospace, monospace" }, item.label));
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

  function colorTokens() {
    const style = getComputedStyle(document.documentElement);
    return {
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
    const support = result.nucleotideSupport && Number(result.nucleotideSupport[position - 1]) || 0;
    return evidenceColor(support, Number(result.maximumNucleotideSupport) || 0, colors);
  }

  function dotBracketFile(result) {
    const energy = Number.isFinite(Number(result.energy)) ? " (" + Number(result.energy).toFixed(2) + ")" : "";
    return ">" + (result.label || (result.engine === "CPLfold" ? "HYB2_Web_CPLfold" : "HYB2_Web_ViennaRNA_MFE")) + "\n" + result.sequence + "\n" + result.dotBracket + energy + "\n";
  }

  function basePairTsv(result) {
    const hardPairs = new Set((result.constraints || []).map(function (pair) {
      return pair.left + ":" + pair.right;
    }));
    const cplfold = result.engine === "CPLfold";
    const guided = result.constraintMode === "hyb-guided";
    const evidenceKind = cplfold
      ? (result.evidence && result.evidence.source === "hyb-block-intervals" ? "log1p_hyb_bonus" : "none")
      : (guided ? "rna_cofold_observations" : "none");
    // Keep the original ViennaRNA columns first for downstream compatibility,
    // then add engine-neutral, explicitly typed fields.
    const rows = [["left_position", "left_base", "right_position", "right_base", "pair_type", "hard_constraint", "rna_cofold_evidence", "mfe_kcal_per_mol", "evidence_kind", "evidence_support", "pair_layer", "selected_energy_kcal_per_mol", "engine"]];
    (result.pairs || []).forEach(function (pair) {
      const support = Number(pair.evidenceSupport) || 0;
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
    });
    return rows.map(function (row) { return row.join("\t"); }).join("\n") + "\n";
  }

  function evidenceTsv(result) {
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
        entry.energy,
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
    const rows = [["prepared_position_1", "prepared_position_2", "log1p_gaussian_hyb_bonus"]];
    const evidence = result.evidence || {};
    (evidence.bonusEntries || []).forEach(function (entry) {
      rows.push([entry.one, entry.two, Number(entry.value).toFixed(6)]);
    });
    return rows.map(function (row) { return row.join("\t"); }).join("\n") + "\n";
  }

  function cplfoldCandidatesTsv(result) {
    const rows = [["rank", "selected", "type", "topology", "energy_kcal_per_mol", "effective_energy_kcal_per_mol", "phase1_energy_kcal_per_mol", "linear_fold_score", "base_pairs", "crossing_pairs", "dot_bracket"]];
    (result.candidates || []).forEach(function (candidate, index) {
      rows.push([
        index + 1,
        index === Number(result.selectedCandidate || 0) ? "yes" : "no",
        candidate.type,
        candidate.topology,
        candidate.energy == null ? "" : candidate.energy,
        candidate.effectiveEnergy == null ? "" : candidate.effectiveEnergy,
        candidate.phase1Energy == null ? "" : candidate.phase1Energy,
        candidate.score == null ? "" : candidate.score,
        (candidate.pairs || []).length,
        candidate.crossingPairs || 0,
        candidate.dotBracket
      ]);
    });
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
    const rows = [sequence.length + " ENERGY = " + energy + " " + (result.label || (result.engine === "CPLfold" ? "HYB2_Web_CPLfold" : "HYB2_Web_ViennaRNA_MFE"))];
    for (let position = 1; position <= sequence.length; position += 1) {
      rows.push([
        position,
        sequence.charAt(position - 1),
        position === 1 ? 0 : position - 1,
        position === sequence.length ? 0 : position + 1,
        pairLookup.get(position) || 0,
        position
      ].join("\t"));
    }
    return rows.join("\n") + "\n";
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
    selectCplfoldCandidate: selectCplfoldCandidate
  };
}());
