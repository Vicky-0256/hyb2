/*
 * Canvas arc diagram for long RNA structures (10 kb genomes and windows).
 * One structure draws its arcs above the axis; a second one (side-by-side
 * comparison) draws below. Pairs present in both are drawn in the neutral
 * "shared" colour. Drag to zoom, double-click to reset.
 */
(function () {
  "use strict";

  var OPEN = "([{<ABCDEFGHIJKLMNOPQRSTUVWXYZ";
  var CLOSE = ")]}>abcdefghijklmnopqrstuvwxyz";

  // Dot-bracket (with pseudoknot brackets) -> [{i, j, layer}] (1-based).
  function parseDbn(dbn, offset) {
    var shift = (offset || 1) - 1;
    var stacks = {};
    var pairs = [];
    String(dbn || "").split("").forEach(function (character, index) {
      var open = OPEN.indexOf(character);
      if (open >= 0) {
        (stacks[open] = stacks[open] || []).push(index + 1);
        return;
      }
      var close = CLOSE.indexOf(character);
      if (close >= 0 && stacks[close] && stacks[close].length) {
        pairs.push({ i: stacks[close].pop() + shift, j: index + 1 + shift, layer: close });
      }
    });
    return pairs;
  }

  function cssVar(name, fallback) {
    var value = window.getComputedStyle(document.documentElement).getPropertyValue(name);
    return value && value.trim() ? value.trim() : fallback;
  }

  function create(container, options) {
    var settings = options || {};
    var height = settings.height || 380;
    var wrap = document.createElement("div");
    wrap.className = "d-arc";
    var canvas = document.createElement("canvas");
    canvas.className = "d-arc-canvas";
    canvas.setAttribute("role", "img");
    canvas.setAttribute("aria-label", "Arc diagram of base pairs");
    var info = document.createElement("div");
    info.className = "d-arc-info";
    wrap.appendChild(canvas);
    wrap.appendChild(info);
    container.appendChild(wrap);

    var model = { start: 1, end: 100, tracks: [] };
    var view = { start: 1, end: 100 };
    var drag = null;
    var onRange = settings.onRange || null;

    function pairKey(pair) { return pair.i + ":" + pair.j; }

    function draw() {
      var ratio = window.devicePixelRatio || 1;
      var width = Math.max(320, wrap.clientWidth || container.clientWidth || 800);
      canvas.width = Math.round(width * ratio);
      canvas.height = Math.round(height * ratio);
      canvas.style.width = width + "px";
      canvas.style.height = height + "px";
      var ctx = canvas.getContext("2d");
      ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
      ctx.clearRect(0, 0, width, height);
      var ink = cssVar("--ink", "#132c47");
      var muted = cssVar("--muted", "#607286");
      var line = cssVar("--line-strong", "#c2d3db");
      var colours = [cssVar("--teal", "#00a09d"), cssVar("--accent", "#6a4ff2")];
      var shared = cssVar("--muted-strong", "#38516a");
      var pk = cssVar("--warning", "#a96614");
      var left = 44;
      var right = 16;
      var two = model.tracks.length > 1;
      var axis = two ? height / 2 : height - 34;
      var span = Math.max(1, view.end - view.start + 1);
      var scale = (width - left - right) / span;
      var maxArc = two ? axis - 16 : axis - 14;
      function px(position) { return left + (position - view.start + 0.5) * scale; }

      // axis + ticks
      ctx.strokeStyle = line;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(left, axis + 0.5);
      ctx.lineTo(width - right, axis + 0.5);
      ctx.stroke();
      var step = Math.pow(10, Math.floor(Math.log10(span / 4)));
      if (span / step > 10) step *= 2;
      if (span / step > 10) step *= 2.5;
      ctx.fillStyle = muted;
      ctx.font = "11px system-ui, sans-serif";
      ctx.textAlign = "center";
      for (var tick = Math.ceil(view.start / step) * step; tick <= view.end; tick += step) {
        var x = px(tick);
        ctx.beginPath();
        ctx.moveTo(x, axis - 3);
        ctx.lineTo(x, axis + 4);
        ctx.stroke();
        ctx.fillText(String(tick), x, axis + 16);
      }

      var sets = model.tracks.map(function (track) {
        var set = new Set();
        track.pairs.forEach(function (pair) { set.add(pairKey(pair)); });
        return set;
      });
      // Vertical scale: the widest visible arc reaches the full height
      // (short-range structures stay readable), at most 4x a semicircle.
      var widest = 1;
      model.tracks.forEach(function (track) {
        track.pairs.forEach(function (pair) {
          if (pair.j < view.start || pair.i > view.end) return;
          widest = Math.max(widest, (Math.min(pair.j, view.end + span) - Math.max(pair.i, view.start - span)) * scale / 2);
        });
      });
      var stretch = Math.min(4, maxArc / widest);
      model.tracks.forEach(function (track, index) {
        var up = index === 0;
        var other = sets[1 - index];
        var visible = 0;
        ctx.lineWidth = scale > 2 ? 1.2 : 0.8;
        track.pairs.forEach(function (pair) {
          if (pair.j < view.start || pair.i > view.end) return;
          var x1 = px(pair.i);
          var x2 = px(pair.j);
          var radius = (x2 - x1) / 2;
          if (radius < 0.15) return;
          visible += 1;
          var common = two && other && other.has(pairKey(pair));
          ctx.strokeStyle = pair.layer > 0 ? pk : (common ? shared : colours[index % 2]);
          ctx.globalAlpha = common ? 0.55 : 0.85;
          var rise = Math.min(maxArc, radius * stretch);
          ctx.beginPath();
          ctx.ellipse((x1 + x2) / 2, axis, radius, rise, 0, up ? Math.PI : 0, up ? 2 * Math.PI : Math.PI);
          ctx.stroke();
        });
        ctx.globalAlpha = 1;
        ctx.fillStyle = colours[index % 2];
        ctx.textAlign = "left";
        ctx.font = "600 12px system-ui, sans-serif";
        ctx.fillText(track.label + " · " + visible + " arcs in view", left, up ? 14 : height - 6);
      });
      if (!model.tracks.length) {
        ctx.fillStyle = muted;
        ctx.textAlign = "center";
        ctx.fillText("Choose a structure to display.", width / 2, height / 2);
      }
      if (drag && drag.x2 !== null) {
        ctx.fillStyle = colours[0];
        ctx.globalAlpha = 0.15;
        ctx.fillRect(Math.min(drag.x1, drag.x2), 0, Math.abs(drag.x2 - drag.x1), height);
        ctx.globalAlpha = 1;
      }
      ctx.fillStyle = ink;
      info.textContent = "Showing " + view.start + "–" + view.end + " of " + model.start + "–" + model.end + ". Drag to zoom, double-click to reset.";
      canvas._toPosition = function (clientX) {
        var rect = canvas.getBoundingClientRect();
        return Math.round(view.start + (clientX - rect.left - left) / scale);
      };
    }

    function clampRange(start, end) {
      var s = Math.max(model.start, Math.min(start, end));
      var e = Math.min(model.end, Math.max(start, end));
      if (e - s < 10) { e = Math.min(model.end, s + 10); s = Math.max(model.start, e - 10); }
      return { start: s, end: e };
    }
    function setRange(start, end, silent) {
      var range = clampRange(start, end);
      view.start = range.start;
      view.end = range.end;
      draw();
      if (onRange && !silent) onRange(view.start, view.end);
    }

    canvas.addEventListener("pointerdown", function (event) {
      var rect = canvas.getBoundingClientRect();
      drag = { x1: event.clientX - rect.left, x2: null, p1: canvas._toPosition(event.clientX) };
      canvas.setPointerCapture(event.pointerId);
    });
    canvas.addEventListener("pointermove", function (event) {
      if (!drag) return;
      var rect = canvas.getBoundingClientRect();
      drag.x2 = event.clientX - rect.left;
      draw();
    });
    canvas.addEventListener("pointerup", function (event) {
      if (!drag) return;
      var p2 = canvas._toPosition(event.clientX);
      var moved = drag.x2 !== null && Math.abs(drag.x2 - drag.x1) > 6;
      var p1 = drag.p1;
      drag = null;
      if (moved) setRange(Math.min(p1, p2), Math.max(p1, p2));
      else draw();
    });
    canvas.addEventListener("dblclick", function () { setRange(model.start, model.end); });

    var resizeObserver = typeof ResizeObserver === "function" ? new ResizeObserver(function () { draw(); }) : null;
    if (resizeObserver) resizeObserver.observe(wrap);

    return {
      setData: function (next) {
        model.start = next.start || 1;
        model.end = Math.max(model.start + 1, next.end || model.start + 1);
        model.tracks = next.tracks || [];
        var keep = next.keepRange && view.start >= model.start && view.end <= model.end;
        if (!keep) { view.start = model.start; view.end = model.end; }
        draw();
      },
      setRange: setRange,
      getRange: function () { return { start: view.start, end: view.end }; },
      redraw: draw,
      destroy: function () { if (resizeObserver) resizeObserver.disconnect(); wrap.remove(); }
    };
  }

  window.HD = window.HD || {};
  window.HD.ArcView = { create: create, parseDbn: parseDbn };
}());
