/*
 * HYB2 Web Lite shared UI kit (WEB_UI_FIX_PLAN.md, contracts K1, K2, K5, K9).
 *
 *   Hyb2UiKit.niceTicks(min, max, target = 5)  K5: 4-7 ticks on 1/2/5 x 10^n,
 *                                              both ends included
 *   Hyb2UiKit.formatTick(value)                en-US grouping, trimmed decimals
 *   Hyb2UiKit.legend.html(opts)                K1: <div class="chart-legend">
 *   Hyb2UiKit.legend.svg(opts)                 K1: "<g>...</g>" for SVG exports
 *   Hyb2UiKit.legend.drawCanvas(ctx, opts)     K1: drawn into a canvas (PNG exports)
 *   K2: <details class="export-menu"> closes on an outside click, on Escape and
 *   after an item is chosen (one document-level listener, installed here).
 *   Hyb2UiKit.canvasToPdf(canvas, wPt, hPt) a one-page PDF Blob of the canvas
 *   Hyb2UiKit.desktop                          K9: the one source of HYB2 Desktop
 *                                              copy, limits and installer links
 *
 * Legend opts: { kind: "sequential" | "diverging", colorAt(t) -> css colour
 *   for t in [0, 1], min, max, ticks?, format?, title, lowLabel?, highLabel?,
 *   orientation?: "horizontal" | "vertical", x?, y?, width?, height?,
 *   ink?, muted?, font? }  (ink / muted / font only for svg and canvas)
 *
 * Callers guard with `window.Hyb2UiKit &&`: some tests load the pages without
 * this file, and a page then simply draws no legend.
 */
(function (root) {
  "use strict";

  const STOPS = 16;
  const INK = "#132c47";
  const MUTED = "#5b6c80";
  const FONT = "ui-sans-serif, system-ui, -apple-system, \"Segoe UI\", sans-serif";
  let gradientId = 0;

  function escapeText(value) {
    return String(value === null || value === undefined ? "" : value).replace(/[&<>"']/g, function (ch) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" }[ch];
    });
  }

  function formatTick(value) {
    if (!Number.isFinite(value)) {
      return "";
    }
    const magnitude = Math.abs(value);
    const digits = magnitude === 0 || magnitude >= 100 ? 0 : magnitude >= 10 ? 1 : magnitude >= 1 ? 2 : 3;
    return Number(value).toLocaleString("en-US", { maximumFractionDigits: digits });
  }

  // K5: interior ticks on a 1/2/5 x 10^n step chosen so that, with both ends,
  // there are 4-7 ticks; interior ticks closer than half a step to an end are
  // dropped so end labels do not collide (half a step: 0-11 gives 0..8, 11)
  function niceTicks(min, max, target) {
    const lo = Number(min);
    const hi = Number(max);
    if (!Number.isFinite(lo) || !Number.isFinite(hi)) {
      return [];
    }
    if (hi === lo) {
      return [lo];
    }
    const low = Math.min(lo, hi);
    const high = Math.max(lo, hi);
    const want = Math.max(2, Math.min(7, Math.round(target || 5)));
    const span = high - low;
    const candidates = [];
    const base = Math.pow(10, Math.floor(Math.log10(span / Math.max(1, want - 1))) - 1);
    [1, 2, 5, 10, 20, 50, 100, 200, 500, 1000].forEach(function (m) { candidates.push(base * m); });
    let best = null;
    candidates.forEach(function (step) {
      const ticks = [low];
      for (let v = Math.ceil(low / step) * step; v < high; v += step) {
        const value = Math.round(v / step) * step;
        if (value - low >= step * 0.5 && high - value > step * 0.5) {
          ticks.push(Number(value.toPrecision(12)));
        }
      }
      ticks.push(high);
      if (ticks.length >= 4 && ticks.length <= 7) {
        const score = Math.abs(ticks.length - want);
        if (!best || score < best.score || (score === best.score && step > best.step)) {
          best = { ticks: ticks, score: score, step: step };
        }
      }
    });
    if (best) {
      return best.ticks;
    }
    // too small a span for 4 nice ticks: the ends and the middle
    return [low, Number(((low + high) / 2).toPrecision(12)), high];
  }

  // ---- K1 legend

  function settings(opts) {
    const o = Object.assign({ kind: "sequential", orientation: "horizontal", min: 0, max: 1, title: "" }, opts || {});
    if (o.kind === "diverging" && !(opts && Object.prototype.hasOwnProperty.call(opts, "min"))) {
      o.min = -Math.abs(o.max);
    }
    o.ticks = Array.isArray(o.ticks) ? o.ticks : niceTicks(o.min, o.max, 4);
    o.format = typeof o.format === "function" ? o.format : formatTick;
    o.colorAt = typeof o.colorAt === "function" ? o.colorAt : function (t) { return "rgb(" + Math.round(255 * (1 - t)) + "," + Math.round(255 * (1 - t)) + "," + Math.round(255 * (1 - t)) + ")"; };
    const horizontal = o.orientation !== "vertical";
    o.width = Number.isFinite(o.width) ? o.width : (horizontal ? 220 : 12);
    o.height = Number.isFinite(o.height) ? o.height : (horizontal ? 10 : 160);
    o.x = Number.isFinite(o.x) ? o.x : 0;
    o.y = Number.isFinite(o.y) ? o.y : 0;
    o.ink = o.ink || INK;
    o.muted = o.muted || MUTED;
    o.font = o.font || FONT;
    return o;
  }

  function fraction(o, value) {
    const span = o.max - o.min;
    return span ? Math.max(0, Math.min(1, (value - o.min) / span)) : 0;
  }

  function stops(o) {
    const out = [];
    for (let i = 0; i <= STOPS; i += 1) {
      out.push({ t: i / STOPS, color: o.colorAt(i / STOPS) });
    }
    return out;
  }

  function html(opts) {
    const o = settings(opts);
    const vertical = o.orientation === "vertical";
    const gradient = "linear-gradient(" + (vertical ? "to top" : "to right") + ", " + stops(o).map(function (s) {
      return s.color + " " + (100 * s.t).toFixed(1) + "%";
    }).join(", ") + ")";
    const ticks = o.ticks.map(function (value) {
      const at = (100 * fraction(o, value)).toFixed(2) + "%";
      return '<span class="chart-legend-tick" style="' + (vertical ? "bottom" : "left") + ":" + at + '">' + escapeText(o.format(value)) + "</span>";
    }).join("");
    const ends = o.lowLabel || o.highLabel
      ? '<div class="chart-legend-ends"><span>' + escapeText(o.lowLabel || "") + "</span><span>" + escapeText(o.highLabel || "") + "</span></div>"
      : "";
    const label = o.title + " · " + o.format(o.min) + " to " + o.format(o.max) + (o.lowLabel ? "; low: " + o.lowLabel : "") + (o.highLabel ? "; high: " + o.highLabel : "");
    return '<div class="chart-legend chart-legend--' + (vertical ? "vertical" : "horizontal") + " chart-legend--" + escapeText(o.kind) + '" role="img" aria-label="' + escapeText(label) + '">' +
      (o.title ? '<span class="chart-legend-title">' + escapeText(o.title) + "</span>" : "") +
      '<div class="chart-legend-scale"><span class="chart-legend-bar" style="background:' + escapeText(gradient) + '"></span><div class="chart-legend-ticks">' + ticks + "</div></div>" +
      ends + "</div>";
  }

  // "<g>...</g>"; with a horizontal bar the title sits above it and the tick
  // labels below; with a vertical bar the labels sit to its right.  The block
  // is about height + 32 px tall (horizontal) or width + 60 px wide (vertical).
  function svg(opts) {
    const o = settings(opts);
    const vertical = o.orientation === "vertical";
    gradientId += 1;
    const id = "hyb2-legend-" + gradientId + "-" + Math.random().toString(36).slice(2, 8);
    const parts = ['<g class="chart-legend" transform="translate(' + o.x + "," + o.y + ')" font-family="' + escapeText(o.font) + '" font-size="10">'];
    parts.push('<defs><linearGradient id="' + id + '" x1="0" y1="' + (vertical ? 1 : 0) + '" x2="' + (vertical ? 0 : 1) + '" y2="0">' +
      stops(o).map(function (s) { return '<stop offset="' + s.t.toFixed(4) + '" stop-color="' + escapeText(s.color) + '"/>'; }).join("") + "</linearGradient></defs>");
    const top = o.title && !vertical ? 14 : 0;
    if (o.title) {
      parts.push('<text x="0" y="' + (vertical ? -6 : 10) + '" fill="' + o.ink + '" font-weight="600">' + escapeText(o.title) + "</text>");
    }
    parts.push('<rect x="0" y="' + top + '" width="' + o.width + '" height="' + o.height + '" fill="url(#' + id + ')" stroke="' + o.muted + '" stroke-width="0.5"/>');
    o.ticks.forEach(function (value) {
      const f = fraction(o, value);
      if (vertical) {
        const y = top + o.height * (1 - f);
        parts.push('<line x1="' + o.width + '" x2="' + (o.width + 4) + '" y1="' + y.toFixed(1) + '" y2="' + y.toFixed(1) + '" stroke="' + o.muted + '"/>');
        parts.push('<text x="' + (o.width + 6) + '" y="' + (y + 3.5).toFixed(1) + '" fill="' + o.ink + '">' + escapeText(o.format(value)) + "</text>");
      } else {
        const x = o.width * f;
        const anchor = f <= 0.001 ? "start" : f >= 0.999 ? "end" : "middle";
        parts.push('<line x1="' + x.toFixed(1) + '" x2="' + x.toFixed(1) + '" y1="' + (top + o.height) + '" y2="' + (top + o.height + 4) + '" stroke="' + o.muted + '"/>');
        parts.push('<text x="' + x.toFixed(1) + '" y="' + (top + o.height + 14) + '" text-anchor="' + anchor + '" fill="' + o.ink + '">' + escapeText(o.format(value)) + "</text>");
      }
    });
    if (o.lowLabel || o.highLabel) {
      if (vertical) {
        parts.push('<text x="0" y="' + (top + o.height + 14) + '" fill="' + o.muted + '">' + escapeText(o.lowLabel || "") + "</text>");
        parts.push('<text x="0" y="' + (top - 18) + '" fill="' + o.muted + '">' + escapeText(o.highLabel || "") + "</text>");
      } else {
        parts.push('<text x="0" y="' + (top + o.height + 28) + '" fill="' + o.muted + '">' + escapeText(o.lowLabel || "") + "</text>");
        parts.push('<text x="' + o.width + '" y="' + (top + o.height + 28) + '" text-anchor="end" fill="' + o.muted + '">' + escapeText(o.highLabel || "") + "</text>");
      }
    }
    parts.push("</g>");
    return parts.join("");
  }

  function drawCanvas(ctx, opts) {
    if (!ctx) {
      return;
    }
    const o = settings(opts);
    const vertical = o.orientation === "vertical";
    const top = o.title && !vertical ? 14 : 0;
    ctx.save();
    ctx.translate(o.x, o.y);
    ctx.font = "10px " + o.font;
    ctx.textBaseline = "alphabetic";
    if (o.title) {
      ctx.fillStyle = o.ink;
      ctx.font = "600 10px " + o.font;
      ctx.textAlign = "left";
      ctx.fillText(o.title, 0, vertical ? -6 : 10);
      ctx.font = "10px " + o.font;
    }
    const gradient = vertical ? ctx.createLinearGradient(0, top + o.height, 0, top) : ctx.createLinearGradient(0, 0, o.width, 0);
    stops(o).forEach(function (s) { gradient.addColorStop(s.t, s.color); });
    ctx.fillStyle = gradient;
    ctx.fillRect(0, top, o.width, o.height);
    ctx.strokeStyle = o.muted;
    ctx.lineWidth = 0.5;
    ctx.strokeRect(0, top, o.width, o.height);
    ctx.fillStyle = o.ink;
    ctx.lineWidth = 1;
    o.ticks.forEach(function (value) {
      const f = fraction(o, value);
      ctx.beginPath();
      if (vertical) {
        const y = top + o.height * (1 - f);
        ctx.moveTo(o.width, y);
        ctx.lineTo(o.width + 4, y);
        ctx.stroke();
        ctx.textAlign = "left";
        ctx.fillText(o.format(value), o.width + 6, y + 3.5);
      } else {
        const x = o.width * f;
        ctx.moveTo(x, top + o.height);
        ctx.lineTo(x, top + o.height + 4);
        ctx.stroke();
        ctx.textAlign = f <= 0.001 ? "left" : f >= 0.999 ? "right" : "center";
        ctx.fillText(o.format(value), x, top + o.height + 14);
      }
    });
    if (o.lowLabel || o.highLabel) {
      ctx.fillStyle = o.muted;
      if (vertical) {
        ctx.textAlign = "left";
        ctx.fillText(o.lowLabel || "", 0, top + o.height + 14);
        ctx.fillText(o.highLabel || "", 0, top - 18);
      } else {
        ctx.textAlign = "left";
        ctx.fillText(o.lowLabel || "", 0, top + o.height + 28);
        ctx.textAlign = "right";
        ctx.fillText(o.highLabel || "", o.width, top + o.height + 28);
      }
    }
    ctx.restore();
  }

  // ---- K2 export menu: one listener for every <details class="export-menu">

  function closeMenus(except) {
    if (typeof document === "undefined") {
      return;
    }
    document.querySelectorAll("details.export-menu[open]").forEach(function (menu) {
      if (menu !== except) {
        menu.removeAttribute("open");
      }
    });
  }

  if (typeof document !== "undefined" && document.addEventListener) {
    document.addEventListener("click", function (event) {
      const target = event.target && event.target.closest ? event.target : null;
      const menu = target ? target.closest("details.export-menu") : null;
      if (!menu) {
        closeMenus(null);
        return;
      }
      closeMenus(menu);
      // a chosen item closes its menu after the page's own handler has run
      if (target.closest(".export-menu-item")) {
        setTimeout(function () { menu.removeAttribute("open"); }, 0);
      }
    }, true);
    // an opened list that would leave the viewport on the left opens to the
    // right instead (toggle does not bubble; a capture listener still sees it)
    document.addEventListener("toggle", function (event) {
      const menu = event.target;
      if (!menu || !menu.matches || !menu.matches("details.export-menu") || !menu.open) {
        return;
      }
      menu.classList.remove("export-menu--start");
      const list = menu.querySelector(".export-menu-list");
      if (list && list.getBoundingClientRect().left < 8) {
        menu.classList.add("export-menu--start");
      }
    }, true);
    document.addEventListener("keydown", function (event) {
      if (event.key !== "Escape") {
        return;
      }
      const open = document.querySelector("details.export-menu[open]");
      if (open) {
        open.removeAttribute("open");
        const summary = open.querySelector("summary");
        if (summary && summary.focus) {
          summary.focus();
        }
        event.stopPropagation();
      }
    }, true);
  }


  // ---- K9: everything the site says about HYB2 Desktop comes from here.
  // Installer names match release_name in .github/workflows/desktop-release.yml.
  // Desktop CPLfold has no fixed length limit: desktop/runtime/detector.py
  // (cplfold_limits) allows floor(sqrt((budget - base) / bytes_per_nt2)) nt,
  // the budget being 75 % of memory. The site quotes the pseudoknot-free
  // figure for an 8 GB computer, rounded down to 100 nt.
  const DESKTOP_FOLD_MODEL = { baseBytes: 192 * 1024 * 1024, bytesPerNt2Free: 144, memoryFraction: 0.75, exampleGb: 8 };
  const DESKTOP_LIMITS = {
    webCplfoldNt: 500,
    cplfoldExampleGb: DESKTOP_FOLD_MODEL.exampleGb,
    cplfoldExampleNt: Math.floor(Math.sqrt((DESKTOP_FOLD_MODEL.exampleGb * Math.pow(1024, 3) * DESKTOP_FOLD_MODEL.memoryFraction -
      DESKTOP_FOLD_MODEL.baseBytes) / DESKTOP_FOLD_MODEL.bytesPerNt2Free) / 100) * 100
  };
  // "about 6,500 nt with 8 GB of memory"
  DESKTOP_LIMITS.cplfoldText = "about " + formatTick(DESKTOP_LIMITS.cplfoldExampleNt) + " nt with " + DESKTOP_LIMITS.cplfoldExampleGb + " GB of memory";
  const DESKTOP_CTA = {
    "raw-reads": "Starting from raw reads (FASTQ, FASTA or SAM)? HYB2 Desktop maps them and writes the .hyb file.",
    "long-fold": "Longer than " + formatTick(DESKTOP_LIMITS.webCplfoldNt) + " nt? HYB2 Desktop runs CPLfold natively with no capacity test; " +
      "its limit grows with memory: " + DESKTOP_LIMITS.cplfoldText + ".",
    overview: "The full pipeline on your computer, from raw reads to structures."
  };
  // The installers are the GitHub releases' files, public since the
  // repository is (2026-10-17). False hides every download (downloadUrl and
  // checksumsUrl return "", cta() opens the Desktop drawer only).
  const DESKTOP_DOWNLOADS_PUBLIC = true;
  const desktop = {
    name: "HYB2 Desktop",
    downloadsAvailable: DESKTOP_DOWNLOADS_PUBLIC,
    notAvailable: "HYB2 Desktop is not available for public download yet.",
    releasesUrl: "https://github.com/Vicky-0256/hyb2/releases",
    checksumsFile: "SHA256SUMS.txt",
    platforms: [
      { id: "mac-arm64", os: "mac", label: "macOS · Apple silicon", file: "HYB2-Desktop-macOS-arm64.dmg" },
      { id: "win-x64", os: "windows", label: "Windows · 64-bit", file: "HYB2-Desktop-Windows-x64-Setup.exe" },
      { id: "linux-deb", os: "linux", label: "Linux · .deb", file: "HYB2-Desktop-Linux-x64.deb" },
      { id: "linux-tar", os: "linux", label: "Linux · .tar.gz", file: "HYB2-Desktop-Linux-x64.tar.gz" }
    ],
    unavailable: "Intel Macs and ARM Linux/Windows have no installer yet; use the command-line pipeline (README).",
    limits: DESKTOP_LIMITS,
    tagline: "The full HYB2 pipeline on your computer: from raw reads to .hyb files, analysis and longer RNA structures.",
    capabilities: [
      { title: "Raw reads → .hyb", text: "FASTQ, FASTA or SAM, mapped with Bowtie2, hybrids called and checked" },
      { title: "Same analysis pages", text: "The same analysis pages as Web, saved as projects on disk" },
      { title: "Native CPLfold", text: "Set by memory, " + DESKTOP_LIMITS.cplfoldText + " (browser: " + formatTick(DESKTOP_LIMITS.webCplfoldNt) + " nt), no capacity test" },
      { title: "Comparisons", text: "DESeq2 comparisons and QC" },
      { title: "Offline", text: "Works offline; data stays on your computer" }
    ],
    isInsideDesktop: function () {
      return !!(root.Hyb2Desktop && root.Hyb2Desktop.enabled);
    },
    // the first platform for this computer's OS (Linux: the .deb), or null.
    // iPadOS Safari reports a Mac; a touch screen with several touch points
    // tells it apart (Macs report 0).
    detectPlatform: function (userAgent, maxTouchPoints) {
      const nav = root.navigator || {};
      const text = String(userAgent != null ? userAgent :
        ((nav.userAgentData && nav.userAgentData.platform) || nav.platform || nav.userAgent || "")).toLowerCase();
      const touches = Number(maxTouchPoints != null ? maxTouchPoints : userAgent != null ? 0 : nav.maxTouchPoints) || 0;
      if (/iphone|ipad|ipod/.test(text) || (text.indexOf("mac") !== -1 && touches > 1)) {
        return null;
      }
      const os = text.indexOf("mac") !== -1 ? "mac" : text.indexOf("win") !== -1 ? "windows" :
        text.indexOf("linux") !== -1 && text.indexOf("android") === -1 ? "linux" : "";
      return desktop.platforms.filter(function (item) { return item.os === os; })[0] || null;
    },
    downloadUrl: function (platform) {
      const file = typeof platform === "string" ? platform : platform && platform.file;
      return desktop.downloadsAvailable && file ? desktop.releasesUrl + "/latest/download/" + file : "";
    },
    checksumsUrl: function () {
      return desktop.downloadUrl(desktop.checksumsFile);
    },
    // The published releases, newest first, as web/releases.js lists them
    // when the site is deployed (scripts/write-releases.mjs): [{ tag,
    // published: "YYYY-MM-DD", files: [names] }]. Empty when it is missing.
    releases: function () {
      const list = root.HYB2_RELEASES;
      return Array.isArray(list) ? list.filter(function (release) {
        return release && typeof release.tag === "string" && Array.isArray(release.files);
      }) : [];
    },
    // A file of one release (tag), as downloadUrl gives the latest's.
    releaseDownloadUrl: function (tag, platform) {
      const file = typeof platform === "string" ? platform : platform && platform.file;
      return desktop.downloadsAvailable && tag && file
        ? desktop.releasesUrl + "/download/" + encodeURIComponent(tag) + "/" + file : "";
    },
    // <a class="desktop-cta" data-action="open-desktop">: the app opens the
    // Desktop drawer on click (and calls preventDefault). Without that handler
    // the link goes to the release page, or nowhere while downloads are private.
    cta: function (context) {
      if (desktop.isInsideDesktop() || !DESKTOP_CTA[context]) {
        return "";
      }
      const href = desktop.downloadsAvailable ? desktop.releasesUrl + "/latest" : "#hyb2-desktop";
      const label = desktop.downloadsAvailable ? "Get HYB2 Desktop" : "About HYB2 Desktop";
      return '<a class="desktop-cta" href="' + href + '" data-action="open-desktop" data-desktop-context="' +
        escapeText(context) + '">' + escapeText(DESKTOP_CTA[context]) + " <strong>" + label + "</strong></a>";
    }
  };

  // Coordinate relationships from Lau/hyb2/bin/chim_types and
  // hyb_chim_types.awk. Internal identifiers remain unchanged for filtering.
  const chimeraPatterns = {
    Type_1: ["Separated · 5′ arm first", [20, 85, 125, 195], "Same RNA; the first arm ends before the second starts."],
    Type_2: ["Separated · 3′ arm first", [125, 195, 20, 85], "Same RNA; the first arm starts after the second ends."],
    Type_3: ["Overlap · 5′ arm starts first", [20, 130, 85, 195], "Same RNA; the first arm starts earlier and ends inside the second."],
    Type_4: ["Shared end · 5′ arm longer", [20, 195, 85, 195], "Same RNA; the first arm starts earlier; both end together."],
    Type_5: ["5′ arm contains 3′ arm", [20, 195, 70, 145], "Same RNA; the second interval lies strictly inside the first."],
    Type_6: ["Shared start · 5′ arm longer", [20, 195, 20, 125], "Same RNA; both start together; the first ends later."],
    Type_7: ["Overlap · 3′ arm starts first", [85, 195, 20, 130], "Same RNA; the second arm starts earlier and ends inside the first."],
    Type_8: ["Identical intervals", [20, 195, 20, 195], "Same RNA; both start and end coordinates match."],
    Type_9: ["Shared end · 3′ arm longer", [85, 195, 20, 195], "Same RNA; the second arm starts earlier; both end together."],
    Type_10: ["Shared start · 3′ arm longer", [20, 125, 20, 195], "Same RNA; both start together; the second ends later."],
    Type_11: ["3′ arm contains 5′ arm", [70, 145, 20, 195], "Same RNA; the first interval lies strictly inside the second."],
    Type_12: ["Touching · 5′ arm first", [20, 105, 105, 195], "Same RNA; the first arm's end equals the second arm's start."],
    Type_13: ["Touching · 3′ arm first", [105, 195, 20, 105], "Same RNA; the first arm's start equals the second arm's end."],
    Intermolecular: ["Different RNAs", [20, 145, 70, 195], "The two arms map to different RNA identifiers."]
  };
  function chimeraInfo(value) {
    const key = String(value).replace(/^Type[_ ]?(\d+)$/i, "Type_$1");
    return chimeraPatterns[key] || [String(value || "Unclassified"), null, "No source-defined diagram is available for this label."];
  }
  function chimeraDiagram(value) {
    const info = chimeraInfo(value), spans = info[1];
    if (!spans) { return ""; }
    return '<svg class="chimera-diagram" viewBox="0 0 270 62" role="img" aria-label="' + escapeText(info[0] + ". " + info[2]) + '"><title>' + escapeText(info[2]) + '</title>' +
      '<text x="0" y="20">5′ arm</text><text x="0" y="44">3′ arm</text>' +
      '<path d="M65 17H265M65 41H265" stroke="currentColor" opacity=".2"/>' +
      '<rect x="' + (spans[0] + 60) + '" y="11" width="' + (spans[1] - spans[0]) + '" height="12" rx="3" fill="#3C5488"/>' +
      '<rect x="' + (spans[2] + 60) + '" y="35" width="' + (spans[3] - spans[2]) + '" height="12" rx="3" fill="#E64B35"/>' +
      '<text x="65" y="60">' + (value === "Intermolecular" ? "Separate RNA references" : "Reference position →") + '</text></svg>';
  }
  root.Hyb2ChimeraTypes = {
    label: function (value) { return chimeraInfo(value)[0]; },
    diagram: chimeraDiagram,
    guide: function () {
      return '<details class="chimera-guide"><summary>How to read these patterns</summary><p>5′ and 3′ identify the two arms in the read. Their positions are shown on the reference, not as a predicted fold. Shared boundaries follow the source coordinate comparisons exactly.</p><div class="chimera-guide-grid">' +
        Object.keys(chimeraPatterns).map(function (key) { const info = chimeraInfo(key); return '<div><strong>' + escapeText(info[0]) + '</strong>' + chimeraDiagram(key) + '<p>' + escapeText(info[2]) + '</p></div>'; }).join("") +
        '</div><p>Source: HYB2 bin/chim_types and bin/hyb_chim_types.awk (Lau). Original classification codes remain in data exports.</p></details>';
    }
  };

  // A one-page PDF holding the canvas as a lossless image (Flate, via the
  // browser's CompressionStream; JPEG at full quality where that is missing),
  // widthPt x heightPt points.
  async function canvasToPdf(canvas, widthPt, heightPt) {
    const encoder = new TextEncoder();
    const width = canvas.width;
    const height = canvas.height;
    let imageBytes;
    let filter;
    if (typeof CompressionStream === "function") {
      const rgba = canvas.getContext("2d").getImageData(0, 0, width, height).data;
      const rgb = new Uint8Array(width * height * 3);
      for (let source = 0, target = 0; source < rgba.length; source += 4, target += 3) {
        rgb[target] = rgba[source];
        rgb[target + 1] = rgba[source + 1];
        rgb[target + 2] = rgba[source + 2];
      }
      const stream = new Blob([rgb]).stream().pipeThrough(new CompressionStream("deflate"));
      imageBytes = new Uint8Array(await new Response(stream).arrayBuffer());
      filter = "/FlateDecode";
    } else {
      const jpeg = await new Promise(function (resolve, reject) {
        canvas.toBlob(function (blob) { return blob ? resolve(blob) : reject(new Error("JPEG encoding failed")); }, "image/jpeg", 1);
      });
      imageBytes = new Uint8Array(await jpeg.arrayBuffer());
      filter = "/DCTDecode";
    }
    const w = widthPt.toFixed(2);
    const h = heightPt.toFixed(2);
    const content = "q " + w + " 0 0 " + h + " 0 0 cm /Im0 Do Q";
    const parts = [];
    const offsets = [];
    let length = 0;
    const push = function (chunk) {
      const bytes = typeof chunk === "string" ? encoder.encode(chunk) : chunk;
      parts.push(bytes);
      length += bytes.length;
    };
    const object = function (body) {
      offsets.push(length);
      push(offsets.length + " 0 obj\n" + body + "\nendobj\n");
    };
    push("%PDF-1.4\n");
    object("<< /Type /Catalog /Pages 2 0 R >>");
    object("<< /Type /Pages /Kids [3 0 R] /Count 1 >>");
    object("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 " + w + " " + h + "] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>");
    offsets.push(length);
    push("4 0 obj\n<< /Type /XObject /Subtype /Image /Width " + width + " /Height " + height + " /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter " + filter + " /Length " + imageBytes.length + " >>\nstream\n");
    push(imageBytes);
    push("\nendstream\nendobj\n");
    object("<< /Length " + content.length + " >>\nstream\n" + content + "\nendstream");
    const xref = length;
    push("xref\n0 " + (offsets.length + 1) + "\n0000000000 65535 f \n" + offsets.map(function (offset) {
      return String(offset).padStart(10, "0") + " 00000 n \n";
    }).join("") + "trailer\n<< /Size " + (offsets.length + 1) + " /Root 1 0 R >>\nstartxref\n" + xref + "\n%%EOF\n");
    return new Blob(parts, { type: "application/pdf" });
  }

  root.Hyb2UiKit = {
    niceTicks: niceTicks,
    formatTick: formatTick,
    legend: { html: html, svg: svg, drawCanvas: drawCanvas },
    closeExportMenus: function () { closeMenus(null); },
    desktop: desktop,
    canvasToPdf: canvasToPdf
  };
}(typeof window !== "undefined" ? window : globalThis));
