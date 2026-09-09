(function () {
  "use strict";

  const BRAND_SCALE = [
    [19, 44, 71],
    [0, 160, 157],
    [106, 79, 242]
  ];

  function afterRender(state, api) {
    if (state.activePage === "interactions") {
      bindVirtualTable(state);
    }
    if (state.activePage === "contact-map") {
      bindContactCanvases(state, api);
    }
    if (state.activePage === "viewpoint") {
      bindViewpointCanvas(state, api);
    }
    if (state.activePage === "region") {
      drawRegionProfile(state);
    }
    if (state.activePage === "compare") {
      bindComparisonCanvases(state, api);
    }
    if (state.activePage === "structure" && window.Hyb2StructureUI) {
      window.Hyb2StructureUI.afterRender(state, api);
    }
  }

  function renderAndFocus(api, selector) {
    api.render();
    const replacement = document.querySelector(selector);
    if (replacement && typeof replacement.focus === "function") {
      replacement.focus();
    }
  }

  function bindVirtualTable(state) {
    const table = document.querySelector("[data-virtual-table]");
    if (!table) {
      return;
    }

    table.scrollTop = state.interactionScrollTop || 0;
    table.addEventListener("scroll", function () {
      state.interactionScrollTop = table.scrollTop;
      updateVirtualRows(state);
    });
  }

  function updateVirtualRows(state) {
    const table = document.querySelector("[data-virtual-table]");
    const rowsContainer = document.querySelector("[data-virtual-rows]");
    if (!table || !rowsContainer) {
      return;
    }

    const records = state.interactionResults || [];
    const rowHeight = state.density === "compact" ? 38 : 46;
    const start = Math.max(0, Math.floor(table.scrollTop / rowHeight) - 4);
    const visible = Math.ceil(table.clientHeight / rowHeight) + 8;
    rowsContainer.style.transform = "translateY(" + (start * rowHeight) + "px)";
    rowsContainer.innerHTML = window.Hyb2Pages.renderVirtualRows(records.slice(start, start + visible));
  }

  function bindContactCanvas(state, api) {
    const canvas = document.getElementById("contact-canvas");
    if (!canvas || !state.contact.matrix || !state.contact.matrix.cells.length) {
      return;
    }

    drawContactCanvas(state);

    canvas.addEventListener("pointermove", function (event) {
      const point = contactPointFromEvent(state, canvas, event);
      if (!point) {
        return;
      }

      if (state.contact.dragStart && event.buttons === 1) {
        panContactView(state, point);
        scheduleLegacyContactDraw(state, canvas);
        return;
      }

      updateContactTooltip(state, point);
    });

    canvas.addEventListener("pointerleave", function () {
      const tooltip = document.querySelector("[data-contact-tooltip]");
      if (tooltip) {
        tooltip.textContent = "Hover a cell for coordinates and counts.";
      }
    });

    canvas.addEventListener("pointerdown", function (event) {
      const point = contactPointFromEvent(state, canvas, event);
      if (!point) {
        return;
      }
      state.contact.dragStart = {
        x: point.x,
        y: point.y,
        view: getContactView(state)
      };
      state.contact.dragMoved = false;
      canvas.setPointerCapture(event.pointerId);
    });

    canvas.addEventListener("pointerup", function (event) {
      const point = contactPointFromEvent(state, canvas, event);
      const moved = state.contact.dragMoved;
      state.contact.dragStart = null;
      if (canvas.hasPointerCapture(event.pointerId)) {
        canvas.releasePointerCapture(event.pointerId);
      }
      if (!point || moved) {
        return;
      }
      state.contact.selection = { x: point.x, y: point.y };
      renderAndFocus(api, "#contact-canvas");
    });

    canvas.addEventListener("dblclick", function () {
      state.contact.view = null;
      state.contact.selection = null;
      renderAndFocus(api, "#contact-canvas");
    });

    canvas.addEventListener("wheel", function (event) {
      event.preventDefault();
      const point = contactPointFromEvent(state, canvas, event);
      if (!point) {
        return;
      }
      zoomContactView(state, point, event.deltaY > 0 ? 1.35 : 0.74);
      scheduleLegacyContactDraw(state, canvas);
    }, { passive: false });

    canvas.addEventListener("keydown", function (event) {
      const keys = ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"];
      if (keys.indexOf(event.key) === -1) {
        return;
      }
      event.preventDefault();
      moveContactSelection(state, event.key);
      renderAndFocus(api, "#contact-canvas");
    });
  }

  function bindContactCanvases(state, api) {
    const canvases = Array.from(document.querySelectorAll("[data-contact-stage]"));
    if (!canvases.length || !state.contact || !state.contact.matrix || !state.contact.matrix.cells.length) {
      return;
    }

    canvases.forEach(function (canvas) {
      const stage = canvas.dataset.contactStage;
      drawContactStageCanvas(state, canvas, stage);

      canvas.addEventListener("pointermove", function (event) {
        const point = contactStagePointFromEvent(state, canvas, stage, event);
        if (!point) {
          return;
        }
        const drag = state.contact.stageDrag;
        if (drag && drag.stage === stage && event.buttons === 1) {
          panContactStageView(state, stage, point);
          scheduleContactStageDraw(state, canvas, stage);
          return;
        }
        updateContactStageTooltip(state, stage, point);
      });

      canvas.addEventListener("pointerleave", function () {
        const tooltip = document.querySelector('[data-contact-tooltip="' + stage + '"]');
        if (tooltip) {
          tooltip.textContent = "Hover a cell for coordinates and counts.";
        }
      });

      canvas.addEventListener("pointerdown", function (event) {
        const point = contactStagePointFromEvent(state, canvas, stage, event);
        if (!point) {
          return;
        }
        state.contact.stageDrag = {
          stage: stage,
          x: point.x,
          y: point.y,
          view: getContactStageView(state, stage)
        };
        state.contact.stageDragMoved = false;
        canvas.setPointerCapture(event.pointerId);
      });

      canvas.addEventListener("pointerup", function (event) {
        const point = contactStagePointFromEvent(state, canvas, stage, event);
        const moved = state.contact.stageDragMoved;
        state.contact.stageDrag = null;
        if (canvas.hasPointerCapture(event.pointerId)) {
          canvas.releasePointerCapture(event.pointerId);
        }
        if (!point || moved) {
          return;
        }
        selectContactStagePoint(state, stage, point);
        renderAndFocus(api, "#contact-" + stage + "-canvas");
      });

      canvas.addEventListener("wheel", function (event) {
        event.preventDefault();
        const point = contactStagePointFromEvent(state, canvas, stage, event);
        if (!point) {
          return;
        }
        zoomContactStageView(state, stage, point, event.deltaY > 0 ? 1.35 : 0.74);
        scheduleContactStageDraw(state, canvas, stage);
      }, { passive: false });

      canvas.addEventListener("dblclick", function () {
        setContactStageView(state, stage, null);
        if (stage === "overview") {
          state.contact.overviewSelection = null;
          state.contact.zoomSelection = null;
          state.contact.selection = null;
          state.contact.zoomView = null;
          state.contact.detailView = null;
        }
        if (stage === "zoom") {
          state.contact.zoomSelection = null;
          state.contact.selection = null;
          state.contact.detailView = null;
        }
        if (stage === "detail") {
          state.contact.selection = null;
        }
        renderAndFocus(api, "#contact-" + stage + "-canvas");
      });

      canvas.addEventListener("keydown", function (event) {
        const keys = ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"];
        if (keys.indexOf(event.key) === -1) {
          return;
        }
        event.preventDefault();
        moveContactStageSelection(state, stage, event.key);
        renderAndFocus(api, "#contact-" + stage + "-canvas");
      });
    });
  }

  function scheduleContactStageDraw(state, canvas, stage) {
    state.contact.pendingStageDraws = state.contact.pendingStageDraws || Object.create(null);
    if (state.contact.pendingStageDraws[stage]) {
      return;
    }
    if (!window.requestAnimationFrame) {
      drawContactStageCanvas(state, canvas, stage);
      return;
    }
    state.contact.pendingStageDraws[stage] = true;
    window.requestAnimationFrame(function () {
      state.contact.pendingStageDraws[stage] = false;
      if (canvas.isConnected) {
        drawContactStageCanvas(state, canvas, stage);
      }
    });
  }

  function forEachVisibleContactCell(matrix, geometry, callback) {
    const visibleGridSize = geometry.xCount * geometry.yCount;
    if (matrix.cellMap && visibleGridSize < matrix.cells.length) {
      for (let xIndex = 0; xIndex < geometry.xCount; xIndex += 1) {
        const x = geometry.xStart + xIndex * matrix.binSize;
        for (let yIndex = 0; yIndex < geometry.yCount; yIndex += 1) {
          const y = geometry.yStart + yIndex * matrix.binSize;
          const cell = matrix.cellMap[x + ":" + y];
          if (cell) {
            callback(cell);
          }
        }
      }
      return;
    }
    matrix.cells.forEach(function (cell) {
      if (cell.x >= geometry.xStart && cell.x <= geometry.xEnd && cell.y >= geometry.yStart && cell.y <= geometry.yEnd) {
        callback(cell);
      }
    });
  }

  function drawContactStageCanvas(state, canvas, stage) {
    const matrix = state.contact.matrix;
    const dimensions = prepareCanvas(canvas);
    const context = dimensions.context;
    const style = getComputedStyle(document.documentElement);
    const ink = style.getPropertyValue("--ink").trim();
    const muted = style.getPropertyValue("--muted").trim();
    const line = style.getPropertyValue("--line").trim();
    const surface = style.getPropertyValue("--surface").trim();
    const geometry = getContactStageGeometry(state, stage, dimensions.width, dimensions.height);
    state.contact.stageGeometry = state.contact.stageGeometry || {};
    state.contact.stageGeometry[stage] = geometry;

    context.fillStyle = surface;
    context.fillRect(0, 0, dimensions.width, dimensions.height);
    context.fillStyle = line;
    context.fillRect(geometry.left, geometry.top, geometry.plotWidth, geometry.plotHeight);

    forEachVisibleContactCell(matrix, geometry, function (cell) {
      const visualValue = state.contact.scale === "log" ? Math.log1p(cell.value) : cell.value;
      const ratio = geometry.cap ? Math.max(0, Math.min(1, visualValue / geometry.cap)) : 0;
      context.fillStyle = viridis(ratio);
      context.fillRect(
        geometry.left + ((cell.x - geometry.xStart) / matrix.binSize) * geometry.cellWidth,
        geometry.top + ((cell.y - geometry.yStart) / matrix.binSize) * geometry.cellHeight,
        Math.ceil(geometry.cellWidth) + .4,
        Math.ceil(geometry.cellHeight) + .4
      );
    });

    const selection = getContactStageSelection(state, stage);
    if (selection && selection.x >= geometry.xStart && selection.x <= geometry.xEnd && selection.y >= geometry.yStart && selection.y <= geometry.yEnd) {
      context.strokeStyle = ink;
      context.lineWidth = 2;
      context.strokeRect(
        geometry.left + ((selection.x - geometry.xStart) / matrix.binSize) * geometry.cellWidth + 1,
        geometry.top + ((selection.y - geometry.yStart) / matrix.binSize) * geometry.cellHeight + 1,
        Math.max(2, geometry.cellWidth - 2),
        Math.max(2, geometry.cellHeight - 2)
      );
    }

    context.font = "11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
    context.fillStyle = muted;
    context.textAlign = "left";
    context.fillText(String(geometry.xStart), geometry.left, geometry.top + geometry.plotHeight + 22);
    context.textAlign = "right";
    context.fillText(String(geometry.xEnd + matrix.binSize - 1), geometry.left + geometry.plotWidth, geometry.top + geometry.plotHeight + 22);
    context.save();
    context.translate(14, geometry.top + geometry.plotHeight);
    context.rotate(-Math.PI / 2);
    const yLabels = contactYAxisLabels(geometry, matrix.binSize);
    context.textAlign = "left";
    context.fillText(String(yLabels.bottom), 0, 0);
    context.textAlign = "right";
    context.fillText(String(yLabels.top), geometry.plotHeight, 0);
    context.restore();
    context.strokeStyle = line;
    context.lineWidth = 1;
    context.strokeRect(geometry.left - .5, geometry.top - .5, geometry.plotWidth + 1, geometry.plotHeight + 1);
  }

  function contactBaseView(matrix) {
    return {
      xStart: Math.floor(matrix.xMin / matrix.binSize) * matrix.binSize,
      xEnd: Math.floor(matrix.xMax / matrix.binSize) * matrix.binSize,
      yStart: Math.floor(matrix.yMin / matrix.binSize) * matrix.binSize,
      yEnd: Math.floor(matrix.yMax / matrix.binSize) * matrix.binSize
    };
  }

  function getContactStageView(state, stage) {
    const contact = state.contact;
    const matrix = contact.matrix;
    const base = contactBaseView(matrix);
    const key = stage === "overview" ? "overviewView" : stage === "zoom" ? "zoomView" : "detailView";
    if (contact[key]) {
      return contact[key];
    }
    if (stage === "zoom" && contact.overviewSelection) {
      return contactFocusedView(base, matrix, contact.overviewSelection, 24);
    }
    if (stage === "detail" && contact.zoomSelection) {
      return contactFocusedView(base, matrix, contact.zoomSelection, 8);
    }
    return base;
  }

  function getContactStageGeometry(state, stage, width, height) {
    const matrix = state.contact.matrix;
    const view = getContactStageView(state, stage);
    const xCount = Math.max(1, Math.round((view.xEnd - view.xStart) / matrix.binSize) + 1);
    const yCount = Math.max(1, Math.round((view.yEnd - view.yStart) / matrix.binSize) + 1);
    const left = 46;
    const top = 20;
    const plotWidth = Math.max(96, width - left - 15);
    const plotHeight = Math.max(96, height - top - 42);
    return {
      left: left,
      top: top,
      plotWidth: plotWidth,
      plotHeight: plotHeight,
      xStart: view.xStart,
      xEnd: view.xEnd,
      yStart: view.yStart,
      yEnd: view.yEnd,
      xCount: xCount,
      yCount: yCount,
      cellWidth: plotWidth / xCount,
      cellHeight: plotHeight / yCount,
      cap: matrix.cap || matrix.max || 1
    };
  }

  function contactStagePointFromEvent(state, canvas, stage, event) {
    const geometry = state.contact.stageGeometry && state.contact.stageGeometry[stage];
    const matrix = state.contact.matrix;
    if (!geometry || !matrix) {
      return null;
    }
    const rect = canvas.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    if (x < geometry.left || x > geometry.left + geometry.plotWidth || y < geometry.top || y > geometry.top + geometry.plotHeight) {
      return null;
    }
    const xIndex = Math.min(geometry.xCount - 1, Math.max(0, Math.floor((x - geometry.left) / geometry.cellWidth)));
    const yIndex = Math.min(geometry.yCount - 1, Math.max(0, Math.floor((y - geometry.top) / geometry.cellHeight)));
    return { x: geometry.xStart + xIndex * matrix.binSize, y: geometry.yStart + yIndex * matrix.binSize };
  }

  function getContactStageSelection(state, stage) {
    if (stage === "overview") return state.contact.overviewSelection;
    if (stage === "zoom") return state.contact.zoomSelection;
    return state.contact.selection;
  }

  function selectContactStagePoint(state, stage, point) {
    const contact = state.contact;
    if (stage === "overview") {
      contact.overviewSelection = point;
      contact.zoomSelection = null;
      contact.selection = null;
      contact.zoomView = null;
      contact.detailView = null;
      return;
    }
    if (stage === "zoom") {
      contact.zoomSelection = point;
      contact.selection = null;
      contact.detailView = null;
      return;
    }
    contact.selection = point;
  }

  function updateContactStageTooltip(state, stage, point) {
    const tooltip = document.querySelector('[data-contact-tooltip="' + stage + '"]');
    if (!tooltip) {
      return;
    }
    const matrix = state.contact.matrix;
    const cell = matrix.cellMap[point.x + ":" + point.y];
    const prefix = state.contact.rnaX + ":" + range(point.x, point.x + matrix.binSize - 1) + " × " + state.contact.rnaY + ":" + range(point.y, point.y + matrix.binSize - 1);
    tooltip.textContent = cell
      ? prefix + " · " + format(cell.records) + " records · " + format(cell.support) + " cluster support"
      : prefix + " · no records";
  }

  function contactFocusedView(base, matrix, point, span) {
    const maxXCount = Math.round((base.xEnd - base.xStart) / matrix.binSize) + 1;
    const maxYCount = Math.round((base.yEnd - base.yStart) / matrix.binSize) + 1;
    const xCount = Math.min(maxXCount, Math.max(2, span));
    const yCount = Math.min(maxYCount, Math.max(2, span));
    const xStart = clampBin(point.x - Math.floor(xCount / 2) * matrix.binSize, base.xStart, base.xEnd - (xCount - 1) * matrix.binSize, matrix.binSize);
    const yStart = clampBin(point.y - Math.floor(yCount / 2) * matrix.binSize, base.yStart, base.yEnd - (yCount - 1) * matrix.binSize, matrix.binSize);
    return {
      xStart: xStart,
      xEnd: xStart + (xCount - 1) * matrix.binSize,
      yStart: yStart,
      yEnd: yStart + (yCount - 1) * matrix.binSize
    };
  }

  function stageViewKey(stage) {
    return stage === "overview" ? "overviewView" : stage === "zoom" ? "zoomView" : "detailView";
  }

  function setContactStageView(state, stage, view) {
    state.contact[stageViewKey(stage)] = view;
  }

  function zoomContactStageView(state, stage, point, factor) {
    const matrix = state.contact.matrix;
    const base = contactBaseView(matrix);
    const view = getContactStageView(state, stage);
    const maxXCount = Math.round((base.xEnd - base.xStart) / matrix.binSize) + 1;
    const maxYCount = Math.round((base.yEnd - base.yStart) / matrix.binSize) + 1;
    const xCount = Math.min(maxXCount, Math.max(2, Math.round(((view.xEnd - view.xStart) / matrix.binSize + 1) * factor)));
    const yCount = Math.min(maxYCount, Math.max(2, Math.round(((view.yEnd - view.yStart) / matrix.binSize + 1) * factor)));
    setContactStageView(state, stage, contactFocusedView(base, matrix, point, Math.max(xCount, yCount)));
  }

  function panContactStageView(state, stage, point) {
    const drag = state.contact.stageDrag;
    if (!drag) {
      return;
    }
    const matrix = state.contact.matrix;
    const base = contactBaseView(matrix);
    const deltaX = drag.x - point.x;
    const deltaY = drag.y - point.y;
    if (Math.abs(deltaX) >= matrix.binSize || Math.abs(deltaY) >= matrix.binSize) {
      state.contact.stageDragMoved = true;
    }
    const xSpan = drag.view.xEnd - drag.view.xStart;
    const ySpan = drag.view.yEnd - drag.view.yStart;
    const xStart = clampBin(drag.view.xStart + deltaX, base.xStart, base.xEnd - xSpan, matrix.binSize);
    const yStart = clampBin(drag.view.yStart + deltaY, base.yStart, base.yEnd - ySpan, matrix.binSize);
    setContactStageView(state, stage, { xStart: xStart, xEnd: xStart + xSpan, yStart: yStart, yEnd: yStart + ySpan });
  }

  function moveContactStageSelection(state, stage, key) {
    const matrix = state.contact.matrix;
    const view = getContactStageView(state, stage);
    const current = getContactStageSelection(state, stage) || { x: view.xStart, y: view.yStart };
    let x = current.x;
    let y = current.y;
    if (key === "ArrowLeft") x -= matrix.binSize;
    if (key === "ArrowRight") x += matrix.binSize;
    if (key === "ArrowUp") y -= matrix.binSize;
    if (key === "ArrowDown") y += matrix.binSize;
    selectContactStagePoint(state, stage, {
      x: clampBin(x, view.xStart, view.xEnd, matrix.binSize),
      y: clampBin(y, view.yStart, view.yEnd, matrix.binSize)
    });
  }

  function drawContactCanvas(state) {
    const canvas = document.getElementById("contact-canvas");
    if (!canvas) {
      return;
    }

    const matrix = state.contact.matrix;
    const dimensions = prepareCanvas(canvas);
    const context = dimensions.context;
    const style = getComputedStyle(document.documentElement);
    const ink = style.getPropertyValue("--ink").trim();
    const muted = style.getPropertyValue("--muted").trim();
    const line = style.getPropertyValue("--line").trim();
    const surface = style.getPropertyValue("--surface").trim();
    const geometry = getContactGeometry(state, dimensions.width, dimensions.height);
    state.contact.geometry = geometry;

    context.fillStyle = surface;
    context.fillRect(0, 0, dimensions.width, dimensions.height);
    context.fillStyle = line;
    context.fillRect(geometry.left, geometry.top, geometry.plotWidth, geometry.plotHeight);

    forEachVisibleContactCell(matrix, geometry, function (cell) {
      const visualValue = state.contact.scale === "log" ? Math.log1p(cell.value) : cell.value;
      const ratio = geometry.cap ? Math.max(0, Math.min(1, visualValue / geometry.cap)) : 0;
      context.fillStyle = viridis(ratio);
      context.fillRect(
        geometry.left + ((cell.x - geometry.xStart) / matrix.binSize) * geometry.cellWidth,
        geometry.top + ((cell.y - geometry.yStart) / matrix.binSize) * geometry.cellHeight,
        Math.ceil(geometry.cellWidth) + .4,
        Math.ceil(geometry.cellHeight) + .4
      );
    });

    if (state.contact.selection) {
      const selected = state.contact.selection;
      if (selected.x >= geometry.xStart && selected.x <= geometry.xEnd && selected.y >= geometry.yStart && selected.y <= geometry.yEnd) {
        context.strokeStyle = ink;
        context.lineWidth = 2;
        context.strokeRect(
          geometry.left + ((selected.x - geometry.xStart) / matrix.binSize) * geometry.cellWidth + 1,
          geometry.top + ((selected.y - geometry.yStart) / matrix.binSize) * geometry.cellHeight + 1,
          Math.max(2, geometry.cellWidth - 2),
          Math.max(2, geometry.cellHeight - 2)
        );
      }
    }

    context.font = "12px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
    context.fillStyle = muted;
    context.textAlign = "left";
    context.fillText(String(geometry.xStart), geometry.left, geometry.top + geometry.plotHeight + 25);
    context.textAlign = "right";
    context.fillText(String(geometry.xEnd + matrix.binSize - 1), geometry.left + geometry.plotWidth, geometry.top + geometry.plotHeight + 25);
    context.save();
    context.translate(16, geometry.top + geometry.plotHeight);
    context.rotate(-Math.PI / 2);
    const yLabels = contactYAxisLabels(geometry, matrix.binSize);
    context.textAlign = "left";
    context.fillText(String(yLabels.bottom), 0, 0);
    context.textAlign = "right";
    context.fillText(String(yLabels.top), geometry.plotHeight, 0);
    context.restore();
    context.strokeStyle = line;
    context.lineWidth = 1;
    context.strokeRect(geometry.left - .5, geometry.top - .5, geometry.plotWidth + 1, geometry.plotHeight + 1);
  }

  function scheduleLegacyContactDraw(state, canvas) {
    if (state.contact.pendingLegacyDraw) {
      return;
    }
    if (!window.requestAnimationFrame) {
      drawContactCanvas(state);
      return;
    }
    state.contact.pendingLegacyDraw = true;
    window.requestAnimationFrame(function () {
      state.contact.pendingLegacyDraw = false;
      if (canvas.isConnected) {
        drawContactCanvas(state);
      }
    });
  }

  function prepareCanvas(canvas) {
    const rect = canvas.getBoundingClientRect();
    const dpr = Math.max(1, Math.min(2, window.devicePixelRatio || 1));
    const width = Math.max(260, Math.round(rect.width || 760));
    const height = Math.max(250, Math.round(rect.height || 600));
    const targetWidth = Math.round(width * dpr);
    const targetHeight = Math.round(height * dpr);

    if (canvas.width !== targetWidth || canvas.height !== targetHeight) {
      canvas.width = targetWidth;
      canvas.height = targetHeight;
    }

    const context = canvas.getContext("2d");
    context.setTransform(dpr, 0, 0, dpr, 0, 0);
    return { context: context, width: width, height: height };
  }

  function getContactView(state) {
    return getContactStageView(state, getContactExportStage(state));
  }

  function getContactExportStage(state) {
    return state.contact && state.contact.zoomSelection ? "detail" : "overview";
  }

  function getContactGeometry(state, width, height) {
    const matrix = state.contact.matrix;
    const view = getContactView(state);
    const xCount = Math.max(1, Math.round((view.xEnd - view.xStart) / matrix.binSize) + 1);
    const yCount = Math.max(1, Math.round((view.yEnd - view.yStart) / matrix.binSize) + 1);
    const left = 58;
    const top = 24;
    const plotWidth = Math.max(120, width - left - 20);
    const plotHeight = Math.max(120, height - top - 50);
    return {
      left: left,
      top: top,
      plotWidth: plotWidth,
      plotHeight: plotHeight,
      xStart: view.xStart,
      xEnd: view.xEnd,
      yStart: view.yStart,
      yEnd: view.yEnd,
      xCount: xCount,
      yCount: yCount,
      cellWidth: plotWidth / xCount,
      cellHeight: plotHeight / yCount,
      cap: matrix.cap || matrix.max || 1
    };
  }

  function contactPointFromEvent(state, canvas, event) {
    const geometry = state.contact.geometry;
    const matrix = state.contact.matrix;
    if (!geometry || !matrix) {
      return null;
    }
    const rect = canvas.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    if (x < geometry.left || x > geometry.left + geometry.plotWidth || y < geometry.top || y > geometry.top + geometry.plotHeight) {
      return null;
    }

    const xIndex = Math.min(geometry.xCount - 1, Math.max(0, Math.floor((x - geometry.left) / geometry.cellWidth)));
    const yIndex = Math.min(geometry.yCount - 1, Math.max(0, Math.floor((y - geometry.top) / geometry.cellHeight)));
    return {
      x: geometry.xStart + xIndex * matrix.binSize,
      y: geometry.yStart + yIndex * matrix.binSize
    };
  }

  function updateContactTooltip(state, point) {
    const tooltip = document.querySelector("[data-contact-tooltip]");
    if (!tooltip) {
      return;
    }
    const matrix = state.contact.matrix;
    const cell = matrix.cellMap[point.x + ":" + point.y];
    if (!cell) {
      tooltip.textContent = state.contact.rnaX + ":" + range(point.x, point.x + matrix.binSize - 1) + " × " + state.contact.rnaY + ":" + range(point.y, point.y + matrix.binSize - 1) + " · no records";
      return;
    }
    tooltip.textContent = state.contact.rnaX + ":" + range(point.x, point.x + matrix.binSize - 1) + " × " + state.contact.rnaY + ":" + range(point.y, point.y + matrix.binSize - 1) + " · " + format(cell.records) + " records · " + format(cell.support) + " cluster support";
  }

  function zoomContactView(state, point, factor) {
    const matrix = state.contact.matrix;
    const base = {
      xStart: Math.floor(matrix.xMin / matrix.binSize) * matrix.binSize,
      xEnd: Math.floor(matrix.xMax / matrix.binSize) * matrix.binSize,
      yStart: Math.floor(matrix.yMin / matrix.binSize) * matrix.binSize,
      yEnd: Math.floor(matrix.yMax / matrix.binSize) * matrix.binSize
    };
    const view = getContactView(state);
    const minCount = 2;
    const xSpan = Math.max(minCount, Math.round(((view.xEnd - view.xStart) / matrix.binSize + 1) * factor));
    const ySpan = Math.max(minCount, Math.round(((view.yEnd - view.yStart) / matrix.binSize + 1) * factor));
    const maxXCount = Math.round((base.xEnd - base.xStart) / matrix.binSize) + 1;
    const maxYCount = Math.round((base.yEnd - base.yStart) / matrix.binSize) + 1;
    const nextXCount = Math.min(maxXCount, xSpan);
    const nextYCount = Math.min(maxYCount, ySpan);
    const xStart = clampBin(point.x - Math.floor(nextXCount / 2) * matrix.binSize, base.xStart, base.xEnd - (nextXCount - 1) * matrix.binSize, matrix.binSize);
    const yStart = clampBin(point.y - Math.floor(nextYCount / 2) * matrix.binSize, base.yStart, base.yEnd - (nextYCount - 1) * matrix.binSize, matrix.binSize);

    state.contact.view = {
      xStart: xStart,
      xEnd: xStart + (nextXCount - 1) * matrix.binSize,
      yStart: yStart,
      yEnd: yStart + (nextYCount - 1) * matrix.binSize
    };
  }

  function panContactView(state, point) {
    const start = state.contact.dragStart;
    if (!start) {
      return;
    }
    const matrix = state.contact.matrix;
    const base = {
      xStart: Math.floor(matrix.xMin / matrix.binSize) * matrix.binSize,
      xEnd: Math.floor(matrix.xMax / matrix.binSize) * matrix.binSize,
      yStart: Math.floor(matrix.yMin / matrix.binSize) * matrix.binSize,
      yEnd: Math.floor(matrix.yMax / matrix.binSize) * matrix.binSize
    };
    const deltaX = start.x - point.x;
    const deltaY = start.y - point.y;
    if (Math.abs(deltaX) >= matrix.binSize || Math.abs(deltaY) >= matrix.binSize) {
      state.contact.dragMoved = true;
    }
    const xSpan = start.view.xEnd - start.view.xStart;
    const ySpan = start.view.yEnd - start.view.yStart;
    const xStart = clampBin(start.view.xStart + deltaX, base.xStart, base.xEnd - xSpan, matrix.binSize);
    const yStart = clampBin(start.view.yStart + deltaY, base.yStart, base.yEnd - ySpan, matrix.binSize);
    state.contact.view = {
      xStart: xStart,
      xEnd: xStart + xSpan,
      yStart: yStart,
      yEnd: yStart + ySpan
    };
  }

  function moveContactSelection(state, key) {
    const matrix = state.contact.matrix;
    const view = getContactView(state);
    const current = state.contact.selection || { x: view.xStart, y: view.yStart };
    let x = current.x;
    let y = current.y;

    if (key === "ArrowLeft") {
      x -= matrix.binSize;
    }
    if (key === "ArrowRight") {
      x += matrix.binSize;
    }
    if (key === "ArrowUp") {
      y -= matrix.binSize;
    }
    if (key === "ArrowDown") {
      y += matrix.binSize;
    }

    state.contact.selection = {
      x: clampBin(x, view.xStart, view.xEnd, matrix.binSize),
      y: clampBin(y, view.yStart, view.yEnd, matrix.binSize)
    };
  }

  function drawRegionProfile(state) {
    const canvas = document.getElementById("region-profile-canvas");
    const results = state.region && state.region.results;
    if (!canvas || !results || !results.profile) {
      return;
    }

    const dimensions = prepareCanvas(canvas);
    const context = dimensions.context;
    const style = getComputedStyle(document.documentElement);
    const line = style.getPropertyValue("--line").trim();
    const primary = style.getPropertyValue("--primary").trim();
    const surface = style.getPropertyValue("--surface").trim();
    const muted = style.getPropertyValue("--muted").trim();
    const profile = results.profile;
    const max = Math.max(1, profile.reduce(function (value, bin) { return Math.max(value, bin.value); }, 0));
    const left = 8;
    const top = 14;
    const width = dimensions.width - 16;
    const height = dimensions.height - 35;

    context.fillStyle = surface;
    context.fillRect(0, 0, dimensions.width, dimensions.height);
    context.strokeStyle = line;
    context.beginPath();
    context.moveTo(left, top + height + .5);
    context.lineTo(left + width, top + height + .5);
    context.stroke();
    profile.forEach(function (bin, index) {
      const barWidth = Math.max(1, width / profile.length);
      const barHeight = (bin.value / max) * height;
      context.fillStyle = primary;
      context.fillRect(left + index * barWidth, top + height - barHeight, Math.max(1, barWidth - 1), barHeight);
    });
    context.fillStyle = muted;
    context.font = "11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
    context.fillText("arm support", left, 11);
  }

  function bindViewpointCanvas(state, api) {
    const canvas = document.getElementById("viewpoint-canvas");
    const results = state.viewpoint && state.viewpoint.results;
    if (!canvas || !results || !results.ready || !results.bins.length) {
      return;
    }
    drawViewpointCanvas(state, canvas);

    canvas.addEventListener("pointermove", function (event) {
      const bin = viewpointBinFromEvent(state, canvas, event);
      canvas.title = bin
        ? bin.start + "–" + bin.end + " · mean coverage " + format(bin.value)
        : "";
    });
    canvas.addEventListener("pointerup", function (event) {
      const bin = viewpointBinFromEvent(state, canvas, event);
      if (!bin) {
        return;
      }
      state.viewpoint.selection = { start: bin.start };
      renderAndFocus(api, "#viewpoint-canvas");
    });
    canvas.addEventListener("keydown", function (event) {
      if (["ArrowLeft", "ArrowRight"].indexOf(event.key) === -1) {
        return;
      }
      event.preventDefault();
      const bins = results.bins;
      const currentIndex = state.viewpoint.selection
        ? Math.max(0, bins.findIndex(function (bin) { return bin.start === state.viewpoint.selection.start; }))
        : 0;
      const nextIndex = event.key === "ArrowLeft"
        ? Math.max(0, currentIndex - 1)
        : Math.min(bins.length - 1, currentIndex + 1);
      state.viewpoint.selection = { start: bins[nextIndex].start };
      renderAndFocus(api, "#viewpoint-canvas");
    });
  }

  function drawViewpointCanvas(state, canvas) {
    const results = state.viewpoint.results;
    const dimensions = prepareCanvas(canvas);
    const context = dimensions.context;
    const style = getComputedStyle(document.documentElement);
    const surface = style.getPropertyValue("--surface").trim();
    const primary = style.getPropertyValue("--primary").trim();
    const accent = style.getPropertyValue("--accent").trim();
    const line = style.getPropertyValue("--line").trim();
    const muted = style.getPropertyValue("--muted").trim();
    const ink = style.getPropertyValue("--ink").trim();
    const geometry = getViewpointGeometry(results, dimensions.width, dimensions.height);
    state.viewpoint.geometry = geometry;

    context.fillStyle = surface;
    context.fillRect(0, 0, dimensions.width, dimensions.height);
    context.strokeStyle = line;
    context.beginPath();
    context.moveTo(geometry.left, geometry.top + geometry.plotHeight + .5);
    context.lineTo(geometry.left + geometry.plotWidth, geometry.top + geometry.plotHeight + .5);
    context.stroke();

    results.bins.forEach(function (bin, index) {
      const x = geometry.left + index * geometry.barWidth;
      const height = geometry.max ? (bin.value / geometry.scaleMax) * geometry.plotHeight : 0;
      const selected = state.viewpoint.selection && state.viewpoint.selection.start === bin.start;
      context.fillStyle = selected ? accent : primary;
      context.fillRect(x, geometry.top + geometry.plotHeight - height, Math.max(1, geometry.barWidth - .35), height);
    });

    context.fillStyle = muted;
    context.font = "11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
    context.textAlign = "left";
    context.fillText(String(results.start), geometry.left, geometry.top + geometry.plotHeight + 22);
    context.textAlign = "right";
    context.fillText(String(results.end), geometry.left + geometry.plotWidth, geometry.top + geometry.plotHeight + 22);
    context.textAlign = "left";
    context.fillStyle = ink;
    context.fillText("mean interaction coverage", geometry.left, 12);
    context.fillStyle = muted;
    context.fillText("max " + format(geometry.max), geometry.left + geometry.plotWidth, 12);
  }

  function getViewpointGeometry(results, width, height) {
    const left = 42;
    const top = 22;
    const plotWidth = Math.max(120, width - left - 18);
    const plotHeight = Math.max(120, height - top - 43);
    return {
      left: left,
      top: top,
      plotWidth: plotWidth,
      plotHeight: plotHeight,
      barWidth: plotWidth / Math.max(1, results.bins.length),
      max: Math.max(0, Number(results.max) || 0),
      scaleMax: Math.max(1, Number(results.max) || 0)
    };
  }

  function viewpointBinFromEvent(state, canvas, event) {
    const geometry = state.viewpoint && state.viewpoint.geometry;
    const results = state.viewpoint && state.viewpoint.results;
    if (!geometry || !results) {
      return null;
    }
    const rect = canvas.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    if (x < geometry.left || x > geometry.left + geometry.plotWidth || y < geometry.top || y > geometry.top + geometry.plotHeight) {
      return null;
    }
    const index = Math.min(results.bins.length - 1, Math.max(0, Math.floor((x - geometry.left) / geometry.barWidth)));
    return results.bins[index];
  }

  function bindComparisonCanvases(state, api) {
    const result = state.comparison && state.comparison.result;
    if (!result || !result.ready) {
      return;
    }
    Array.from(document.querySelectorAll("[data-comparison-map]")).forEach(function (canvas) {
      const kind = canvas.dataset.comparisonMap;
      drawComparisonCanvas(state, canvas, kind);
      canvas.addEventListener("pointermove", function (event) {
        const cell = comparisonCellFromEvent(state, canvas, kind, event);
        canvas.title = cell
          ? "log2 effect " + format(cell.effect) + " · " + format(cell.presentDatasets) + " datasets"
          : "";
      });
      canvas.addEventListener("pointerup", function (event) {
        const cell = comparisonCellFromEvent(state, canvas, kind, event);
        if (!cell) {
          return;
        }
        state.comparison.selection = { x: cell.x, y: cell.y };
        state.comparison.activeMap = kind;
        renderAndFocus(api, "#comparison-" + kind + "-canvas");
      });
      canvas.addEventListener("keydown", function (event) {
        if (["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"].indexOf(event.key) === -1) {
          return;
        }
        event.preventDefault();
        if (moveComparisonSelection(state, kind, event.key)) {
          renderAndFocus(api, "#comparison-" + kind + "-canvas");
        }
      });
    });
  }

  function drawComparisonCanvas(state, canvas, kind) {
    const result = state.comparison.result;
    const dimensions = prepareCanvas(canvas);
    const context = dimensions.context;
    const style = getComputedStyle(document.documentElement);
    const surface = style.getPropertyValue("--surface").trim();
    const line = style.getPropertyValue("--line").trim();
    const ink = style.getPropertyValue("--ink").trim();
    const muted = style.getPropertyValue("--muted").trim();
    const geometry = getComparisonGeometry(result, dimensions.width, dimensions.height);
    state.comparison.geometry = state.comparison.geometry || {};
    state.comparison.geometry[kind] = geometry;
    const cells = kind === "conserved" ? result.conservedCells : result.cells;

    context.fillStyle = surface;
    context.fillRect(0, 0, dimensions.width, dimensions.height);
    context.fillStyle = line;
    context.fillRect(geometry.left, geometry.top, geometry.plotWidth, geometry.plotHeight);
    cells.forEach(function (cell) {
      const x = geometry.left + ((cell.x - geometry.xStart) / result.binSize) * geometry.cellWidth;
      const y = geometry.top + ((cell.y - geometry.yStart) / result.binSize) * geometry.cellHeight;
      context.fillStyle = kind === "conserved"
        ? conservedColor(cell.presentDatasets / Math.max(1, result.conservedMax))
        : effectColor(cell.effect / Math.max(.01, result.effectCap));
      context.fillRect(x, y, Math.ceil(geometry.cellWidth) + .4, Math.ceil(geometry.cellHeight) + .4);
    });
    if (state.comparison.selection) {
      const selected = state.comparison.selection;
      if (selected.x >= geometry.xStart && selected.x <= geometry.xEnd && selected.y >= geometry.yStart && selected.y <= geometry.yEnd) {
        context.strokeStyle = ink;
        context.lineWidth = 2;
        context.strokeRect(
          geometry.left + ((selected.x - geometry.xStart) / result.binSize) * geometry.cellWidth + 1,
          geometry.top + ((selected.y - geometry.yStart) / result.binSize) * geometry.cellHeight + 1,
          Math.max(2, geometry.cellWidth - 2),
          Math.max(2, geometry.cellHeight - 2)
        );
      }
    }
    context.font = "11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
    context.fillStyle = muted;
    context.textAlign = "left";
    context.fillText(String(geometry.xStart), geometry.left, geometry.top + geometry.plotHeight + 22);
    context.textAlign = "right";
    context.fillText(String(geometry.xEnd + result.binSize - 1), geometry.left + geometry.plotWidth, geometry.top + geometry.plotHeight + 22);
    context.textAlign = "right";
    context.fillText(String(geometry.yStart), geometry.left - 7, geometry.top + 4);
    context.fillText(String(geometry.yEnd + result.binSize - 1), geometry.left - 7, geometry.top + geometry.plotHeight);
    context.font = "11px system-ui, sans-serif";
    context.fillStyle = ink;
    context.textAlign = "left";
    context.fillText(state.comparison.rnaX + " (X) × " + state.comparison.rnaY + " (Y)", geometry.left, 15);
    if (kind === "effect") {
      context.fillText(state.comparison.conditionALabel + " enriched: violet · " + state.comparison.conditionBLabel + " enriched: teal", geometry.left, 29);
    }
    context.strokeStyle = line;
    context.strokeRect(geometry.left - .5, geometry.top - .5, geometry.plotWidth + 1, geometry.plotHeight + 1);
  }

  function getComparisonGeometry(result, width, height) {
    const xStart = Math.floor(result.xMin / result.binSize) * result.binSize;
    const xEnd = Math.floor(result.xMax / result.binSize) * result.binSize;
    const yStart = Math.floor(result.yMin / result.binSize) * result.binSize;
    const yEnd = Math.floor(result.yMax / result.binSize) * result.binSize;
    const left = 78;
    const top = 38;
    const plotWidth = Math.max(120, width - left - 18);
    const plotHeight = Math.max(120, height - top - 42);
    const xCount = Math.max(1, Math.round((xEnd - xStart) / result.binSize) + 1);
    const yCount = Math.max(1, Math.round((yEnd - yStart) / result.binSize) + 1);
    return {
      left: left,
      top: top,
      plotWidth: plotWidth,
      plotHeight: plotHeight,
      xStart: xStart,
      xEnd: xEnd,
      yStart: yStart,
      yEnd: yEnd,
      xCount: xCount,
      yCount: yCount,
      cellWidth: plotWidth / xCount,
      cellHeight: plotHeight / yCount
    };
  }

  function comparisonCellFromEvent(state, canvas, kind, event) {
    const geometry = state.comparison && state.comparison.geometry && state.comparison.geometry[kind];
    const result = state.comparison && state.comparison.result;
    if (!geometry || !result) {
      return null;
    }
    const rect = canvas.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    if (x < geometry.left || x > geometry.left + geometry.plotWidth || y < geometry.top || y > geometry.top + geometry.plotHeight) {
      return null;
    }
    const xIndex = Math.min(geometry.xCount - 1, Math.max(0, Math.floor((x - geometry.left) / geometry.cellWidth)));
    const yIndex = Math.min(geometry.yCount - 1, Math.max(0, Math.floor((y - geometry.top) / geometry.cellHeight)));
    const cell = result.cellMap[(geometry.xStart + xIndex * result.binSize) + ":" + (geometry.yStart + yIndex * result.binSize)] || null;
    return kind === "conserved" && cell && cell.presentDatasets < 2 ? null : cell;
  }

  function moveComparisonSelection(state, kind, key) {
    const result = state.comparison && state.comparison.result;
    if (!result || !result.ready) {
      return false;
    }
    const cells = kind === "conserved" ? result.conservedCells : result.cells;
    if (!cells || !cells.length) {
      return false;
    }

    const ordered = cells.slice().sort(function (left, right) { return left.y - right.y || left.x - right.x; });
    const selected = state.comparison.selection;
    const selectedCell = selected
      ? cells.find(function (cell) { return cell.x === selected.x && cell.y === selected.y; })
      : null;
    const current = selectedCell || ordered[0];
    let next;

    if (current) {
      const candidates = cells.filter(function (cell) {
        if (key === "ArrowLeft") {
          return cell.x < current.x;
        }
        if (key === "ArrowRight") {
          return cell.x > current.x;
        }
        if (key === "ArrowUp") {
          return cell.y < current.y;
        }
        return cell.y > current.y;
      });
      const horizontal = key === "ArrowLeft" || key === "ArrowRight";
      candidates.sort(function (left, right) {
        const leftPrimary = horizontal ? Math.abs(left.x - current.x) : Math.abs(left.y - current.y);
        const rightPrimary = horizontal ? Math.abs(right.x - current.x) : Math.abs(right.y - current.y);
        const leftSecondary = horizontal ? Math.abs(left.y - current.y) : Math.abs(left.x - current.x);
        const rightSecondary = horizontal ? Math.abs(right.y - current.y) : Math.abs(right.x - current.x);
        return leftSecondary - rightSecondary || leftPrimary - rightPrimary || left.y - right.y || left.x - right.x;
      });
      next = candidates[0] || (!selectedCell ? current : null);
    }

    if (!next) {
      return false;
    }
    state.comparison.selection = { x: next.x, y: next.y };
    state.comparison.activeMap = kind;
    return true;
  }

  function structureControlChangesSequence(key) {
    return ["engine", "source", "rna", "start", "end", "pastedSequence", "selectedRecordIndex", "evidenceLayout", "secondRna", "secondStart", "secondEnd", "homodimerOnly", "cplfoldEvidence"].indexOf(key) !== -1;
  }

  function resetStructureForControl(state, key) {
    if (window.Hyb2Structure) {
      window.Hyb2Structure.reset(state, { clearConstraints: structureControlChangesSequence(key) });
    }
  }

  function invalidateFastaConsumers(state) {
    if (state.viewpoint) {
      state.viewpoint.results = null;
      state.viewpoint.selection = null;
      if (state.viewpoint.rangeMode === "reference" &&
          !window.Hyb2Data.findFastaEntry(state.fasta, state.viewpoint.rna)) {
        state.viewpoint.rangeMode = "coordinates";
      }
    }
    if (state.structure && state.structure.source === "reference" && window.Hyb2Structure) {
      window.Hyb2Structure.reset(state, { clearConstraints: true });
    }
  }

  function handleChange(state, element, api) {
    const feature = element.dataset.feature;
    const key = element.dataset.key;

    if (feature === "interaction-filter") {
      state.filters[key] = element.value;
      state.selectedRecordIndex = null;
      state.interactionScrollTop = 0;
      api.updateInteractionHash();
      api.render();
      return true;
    }

    if (feature === "contact-control") {
      state.contact[key] = element.value;
      state.contact.matrix = null;
      state.contact.selection = null;
      state.contact.overviewSelection = null;
      state.contact.zoomSelection = null;
      state.contact.overviewView = null;
      state.contact.zoomView = null;
      state.contact.detailView = null;
      api.render();
      return true;
    }

    if (feature === "viewpoint-control") {
      state.viewpoint[key] = element.value;
      if (key === "rna" && state.viewpoint.rangeMode === "reference" &&
          !window.Hyb2Data.findFastaEntry(state.fasta, state.viewpoint.rna)) {
        state.viewpoint.rangeMode = "coordinates";
      }
      state.viewpoint.results = null;
      state.viewpoint.selection = null;
      api.render();
      return true;
    }

    if (feature === "comparison-control") {
      state.comparison[key] = element.value;
      state.comparison.result = null;
      state.comparison.selection = null;
      api.render();
      return true;
    }

    if (feature === "comparison-dataset") {
      const datasetId = element.dataset.datasetId;
      const dataset = (state.comparison.datasets || []).find(function (item) { return item.id === datasetId; });
      if (!dataset) {
        return true;
      }
      dataset[key] = element.value;
      state.comparison.result = null;
      state.comparison.selection = null;
      api.render();
      return true;
    }

    if (feature === "region-control") {
      state.region[key] = element.value;
      if (key === "partner") {
        state.region.partnerStart = "";
        state.region.partnerEnd = "";
      }
      state.region.results = null;
      api.render();
      return true;
    }

    if (feature === "structure-control") {
      resetStructureForControl(state, key);
      const value = key === "selectedRecordIndex" ? (element.value === "" ? null : Number(element.value)) : (element.type === "checkbox" ? element.checked : element.value);
      state.structure[key] = value;
      if (key === "constraintMode" && value === "hyb-guided") {
        state.structure.source = "reference";
      }
      if ((key === "engine" && value === "cplfold" && state.structure.cplfoldEvidence === "hyb-blocks") ||
          (key === "cplfoldEvidence" && value === "hyb-blocks")) {
        state.structure.source = "reference";
      }
      if (key === "engine" && value === "viennarna" && state.structure.constraintMode === "hyb-guided") {
        state.structure.source = "reference";
      }
      if (key === "source" && value !== "reference" && state.structure.constraintMode === "hyb-guided") {
        state.structure.constraintMode = "none";
      }
      if (key === "source" && value !== "reference" && state.structure.engine === "cplfold") {
        state.structure.cplfoldEvidence = "none";
      }
      api.render();
      return true;
    }

    if (feature === "fasta-mapping-editor" && state.fasta) {
      state.fasta[key] = element.value;
      if (key === "mappingEditorRna") {
        state.fasta.mappingEditorReference = state.fasta.mapping[element.value] || "";
        const mappingPanel = element.closest && element.closest(".fasta-mapping");
        const referenceInput = mappingPanel && mappingPanel.querySelector(
          '[data-feature="fasta-mapping-editor"][data-key="mappingEditorReference"]'
        );
        if (referenceInput) {
          referenceInput.value = state.fasta.mappingEditorReference;
        }
      }
      // Keep both search controls mounted while focus moves between them. A full
      // render here can replace the newly focused field during the blur/change
      // sequence and redirect the user's next keystrokes into the wrong input.
      return true;
    }

    if (feature === "fasta-mapping") {
      const rna = element.dataset.rna;
      if (element.value) {
        state.fasta.mapping[rna] = element.value;
      } else {
        delete state.fasta.mapping[rna];
      }
      invalidateFastaConsumers(state);
      api.render();
      return true;
    }

    return false;
  }

  function handleInput(state, element) {
    const feature = element.dataset.feature;
    const key = element.dataset.key;
    if (!key) {
      return false;
    }
    if (feature === "interaction-filter" && state.filters) {
      state.filters[key] = element.value;
      state.selectedRecordIndex = null;
      state.interactionScrollTop = 0;
      return true;
    }
    if (feature === "contact-control" && state.contact) {
      state.contact[key] = element.value;
      state.contact.matrix = null;
      state.contact.selection = null;
      state.contact.overviewSelection = null;
      state.contact.zoomSelection = null;
      state.contact.overviewView = null;
      state.contact.zoomView = null;
      state.contact.detailView = null;
      return true;
    }
    if (feature === "viewpoint-control" && state.viewpoint) {
      state.viewpoint[key] = element.value;
      state.viewpoint.results = null;
      state.viewpoint.selection = null;
      return true;
    }
    if (feature === "comparison-control" && state.comparison) {
      state.comparison[key] = element.value;
      state.comparison.result = null;
      state.comparison.selection = null;
      return true;
    }
    if (feature === "comparison-dataset" && state.comparison) {
      const dataset = (state.comparison.datasets || []).find(function (item) {
        return item.id === element.dataset.datasetId;
      });
      if (dataset) {
        dataset[key] = element.value;
        state.comparison.result = null;
        state.comparison.selection = null;
      }
      return true;
    }
    if (feature === "region-control" && state.region) {
      state.region[key] = element.value;
      state.region.results = null;
      return true;
    }
    if (feature === "structure-control" && state.structure) {
      resetStructureForControl(state, key);
      state.structure[key] = element.value;
      if (key === "constraintMode" && element.value === "hyb-guided") {
        state.structure.source = "reference";
      }
      if ((key === "engine" && element.value === "cplfold" && state.structure.cplfoldEvidence === "hyb-blocks") ||
          (key === "cplfoldEvidence" && element.value === "hyb-blocks")) {
        state.structure.source = "reference";
      }
      if (key === "engine" && element.value === "viennarna" && state.structure.constraintMode === "hyb-guided") {
        state.structure.source = "reference";
      }
      if (key === "source" && element.value !== "reference" && state.structure.constraintMode === "hyb-guided") {
        state.structure.constraintMode = "none";
      }
      if (key === "source" && element.value !== "reference" && state.structure.engine === "cplfold") {
        state.structure.cplfoldEvidence = "none";
      }
      return true;
    }
    if (feature === "fasta-mapping-editor" && state.fasta) {
      state.fasta[key] = element.value;
      return true;
    }
    return false;
  }

  function handleAction(state, element, api) {
    const action = element.dataset.featureAction;
    if (!action) {
      return false;
    }

    if (window.Hyb2StructureUI && window.Hyb2StructureUI.handleAction(action, state, api, element)) {
      return true;
    }

    if (action === "select-partner") {
      state.filters.partner = element.dataset.partner || "";
      state.selectedRecordIndex = null;
      state.interactionScrollTop = 0;
      api.updateInteractionHash();
      api.render();
      return true;
    }

    if (action === "open-record") {
      state.selectedRecordIndex = Number(element.dataset.recordIndex);
      api.render();
      return true;
    }

    if (action === "close-record") {
      state.selectedRecordIndex = null;
      api.render();
      return true;
    }

    if (action === "record-contact") {
      const record = window.Hyb2Pages.findRecord(state, Number(element.dataset.recordIndex));
      if (!record) {
        return true;
      }
      state.contact.rnaX = record.rnaOne;
      state.contact.rnaY = record.rnaTwo;
      state.contact.chimeraType = "all";
      state.contact.homodimer = "all";
      state.contact.matrix = null;
      const selection = {
        x: Math.floor(record.rnaOneStart / state.contact.binSize) * state.contact.binSize,
        y: Math.floor(record.rnaTwoStart / state.contact.binSize) * state.contact.binSize
      };
      state.contact.overviewSelection = selection;
      state.contact.zoomSelection = selection;
      state.contact.selection = selection;
      state.contact.overviewView = null;
      state.contact.zoomView = null;
      state.contact.detailView = null;
      api.navigate("contact-map");
      return true;
    }

    if (action === "record-region") {
      const record = window.Hyb2Pages.findRecord(state, Number(element.dataset.recordIndex));
      if (!record) {
        return true;
      }
      const armOne = element.dataset.arm === "1";
      state.region.rna = armOne ? record.rnaOne : record.rnaTwo;
      state.region.start = armOne ? record.rnaOneStart : record.rnaTwoStart;
      state.region.end = armOne ? record.rnaOneEnd : record.rnaTwoEnd;
      state.region.partner = armOne ? record.rnaTwo : record.rnaOne;
      state.region.partnerStart = "";
      state.region.partnerEnd = "";
      state.region.results = null;
      api.navigate("region");
      return true;
    }

    if (action === "record-fold") {
      if (window.Hyb2Structure) {
        window.Hyb2Structure.reset(state, { clearConstraints: true });
      }
      state.structure.source = "record";
      state.structure.selectedRecordIndex = Number(element.dataset.recordIndex);
      // A raw HYB record supplies its read sequence, not a mapped reference
      // interval. Reference-coordinate evidence must therefore be disabled.
      if (state.structure.constraintMode === "hyb-guided") {
        state.structure.constraintMode = "none";
      }
      if (state.structure.engine === "cplfold" && state.structure.cplfoldEvidence === "hyb-blocks") {
        state.structure.cplfoldEvidence = "none";
      }
      api.navigate("structure");
      return true;
    }

    if (action === "copy-record") {
      const record = window.Hyb2Pages.findRecord(state, Number(element.dataset.recordIndex));
      if (record) {
        api.copyText(record.raw, "Record copied to the clipboard.");
      }
      return true;
    }

    if (action === "export-filtered-csv") {
      api.download("filtered-interactions.csv", window.Hyb2Data.recordsToCsv(state.interactionResults || []), "text/csv");
      api.showToast("Filtered interaction CSV downloaded locally.");
      return true;
    }

    if (action === "export-filtered-hyb") {
      api.download("filtered.hyb", window.Hyb2Data.recordsToHyb(state.interactionResults || []), "text/plain");
      api.showToast("Filtered HYB file downloaded locally.");
      return true;
    }

    if (action === "interactions-export-params") {
      api.download("interactions-parameters.json", JSON.stringify(interactionParameters(state), null, 2), "application/json");
      api.showToast("Interaction parameters downloaded locally.");
      return true;
    }

    if (action === "generate-contact") {
      state.contact.matrix = null;
      state.contact.selection = null;
      state.contact.overviewSelection = null;
      state.contact.zoomSelection = null;
      state.contact.overviewView = null;
      state.contact.zoomView = null;
      state.contact.detailView = null;
      api.render();
      return true;
    }

    if (action === "reset-contact") {
      state.contact.selection = null;
      state.contact.overviewSelection = null;
      state.contact.zoomSelection = null;
      state.contact.overviewView = null;
      state.contact.zoomView = null;
      state.contact.detailView = null;
      api.render();
      return true;
    }

    if (action === "toggle-contact-data") {
      state.contact.showDataTable = !state.contact.showDataTable;
      api.render();
      return true;
    }

    if (action === "contact-explore-region") {
      if (!state.contact.selection || !state.contact.matrix) {
        return true;
      }
      const matrix = state.contact.matrix;
      state.region.rna = state.contact.rnaX;
      state.region.start = Math.max(1, state.contact.selection.x);
      state.region.end = state.contact.selection.x + matrix.binSize - 1;
      state.region.partner = state.contact.rnaY;
      state.region.partnerStart = Math.max(1, state.contact.selection.y);
      state.region.partnerEnd = state.contact.selection.y + matrix.binSize - 1;
      state.region.results = null;
      api.navigate("region");
      return true;
    }

    if (action === "contact-export-selected") {
      const selection = state.contact.selection;
      if (!selection) {
        return true;
      }
      const records = window.Hyb2Data.getCellRecords(state.records || [], state.contact, selection.x, selection.y);
      api.download("contact-cell-records.csv", window.Hyb2Data.recordsToCsv(records), "text/csv");
      api.showToast("Selected cell records downloaded locally.");
      return true;
    }

    if (action === "contact-export-tsv") {
      api.download("contact-map.tsv", window.Hyb2Data.contactToTsv(state.contact.matrix), "text/tab-separated-values");
      api.showToast("Contact TSV downloaded locally.");
      return true;
    }

    if (action === "contact-export-params") {
      api.download("contact-map-parameters.json", JSON.stringify(contactParameters(state), null, 2), "application/json");
      api.showToast("Contact-map parameters downloaded locally.");
      return true;
    }

    if (action === "contact-download-png") {
      if (!ensureVisualExport(state, "contact", api)) {
        return true;
      }
      downloadCanvasPng("contact-map.png", api, "contact-detail-canvas", "Contact PNG downloaded locally.", "contact-overview-canvas");
      return true;
    }

    if (action === "contact-download-svg") {
      if (!ensureVisualExport(state, "contact", api)) {
        return true;
      }
      api.download("contact-map.svg", buildContactSvg(state), "image/svg+xml");
      api.showToast("Contact SVG downloaded locally.");
      return true;
    }

    if (action === "generate-viewpoint") {
      state.viewpoint.results = null;
      state.viewpoint.selection = null;
      api.render();
      return true;
    }

    if (action === "reset-viewpoint-selection") {
      state.viewpoint.selection = null;
      api.render();
      return true;
    }

    if (action === "toggle-viewpoint-data") {
      state.viewpoint.showDataTable = !state.viewpoint.showDataTable;
      api.render();
      return true;
    }

    if (action === "viewpoint-explore-region") {
      const results = state.viewpoint.results;
      const selected = state.viewpoint.selection && results && results.bins
        ? results.bins.find(function (bin) { return bin.start === state.viewpoint.selection.start; })
        : null;
      if (!selected) {
        return true;
      }
      state.region.rna = state.viewpoint.rna;
      state.region.start = selected.start;
      state.region.end = selected.end;
      state.region.partner = state.viewpoint.partner || "";
      state.region.partnerStart = "";
      state.region.partnerEnd = "";
      state.region.results = null;
      api.navigate("region");
      return true;
    }

    if (action === "viewpoint-export-tsv") {
      api.download("viewpoint.tsv", window.Hyb2Data.viewpointToTsv(state.viewpoint.results), "text/tab-separated-values");
      api.showToast("Viewpoint TSV downloaded locally.");
      return true;
    }

    if (action === "viewpoint-export-params") {
      api.download("viewpoint-parameters.json", JSON.stringify(viewpointParameters(state), null, 2), "application/json");
      api.showToast("Viewpoint parameters downloaded locally.");
      return true;
    }

    if (action === "viewpoint-download-png") {
      if (!ensureVisualExport(state, "viewpoint", api)) {
        return true;
      }
      downloadCanvasPng("viewpoint.png", api, "viewpoint-canvas", "Viewpoint PNG downloaded locally.");
      return true;
    }

    if (action === "viewpoint-download-svg") {
      if (!ensureVisualExport(state, "viewpoint", api)) {
        return true;
      }
      api.download("viewpoint.svg", buildViewpointSvg(state), "image/svg+xml");
      api.showToast("Viewpoint SVG downloaded locally.");
      return true;
    }

    if (action === "choose-comparison-files") {
      api.chooseComparisonFiles();
      return true;
    }

    if (action === "remove-comparison-dataset") {
      const datasetId = element.dataset.datasetId;
      state.comparison.datasets = (state.comparison.datasets || []).filter(function (dataset) { return dataset.id !== datasetId || dataset.id === "primary"; });
      const remainingRnas = Array.from(new Set(state.comparison.datasets.flatMap(function (dataset) {
        return dataset.summary && dataset.summary.rnaNames ? dataset.summary.rnaNames : [];
      }))).sort(function (left, right) { return left.localeCompare(right); });
      if (remainingRnas.indexOf(state.comparison.rnaX) === -1) {
        state.comparison.rnaX = remainingRnas[0] || "";
      }
      if (remainingRnas.indexOf(state.comparison.rnaY) === -1) {
        state.comparison.rnaY = remainingRnas[0] || "";
      }
      state.comparison.result = null;
      state.comparison.selection = null;
      api.render();
      return true;
    }

    if (action === "generate-comparison") {
      state.comparison.result = null;
      state.comparison.selection = null;
      api.render();
      return true;
    }

    if (action === "toggle-comparison-data") {
      state.comparison.showDataTable = !state.comparison.showDataTable;
      api.render();
      return true;
    }

    if (action === "comparison-export-tsv") {
      if (!ensureVisualExport(state, "comparison", api)) {
        return true;
      }
      api.download("comparison.tsv", window.Hyb2Data.comparisonToTsv(state.comparison.result), "text/tab-separated-values");
      api.showToast("Comparison TSV downloaded locally.");
      return true;
    }

    if (action === "comparison-export-params") {
      api.download("comparison-parameters.json", JSON.stringify(comparisonParameters(state), null, 2), "application/json");
      api.showToast("Comparison parameters downloaded locally.");
      return true;
    }

    if (action === "comparison-export-deseq-counts" ||
        action === "comparison-export-hyb2-names" ||
        action === "comparison-export-deseq-metadata") {
      if (!ensureVisualExport(state, "comparison", api)) {
        return true;
      }
      if (action === "comparison-export-deseq-counts") {
        try {
          api.download(
            "hyb2-web.table.txt",
            window.Hyb2Data.comparisonCountMatrixToTsv(state.comparison, state.comparison.result),
            "text/tab-separated-values"
          );
          api.showToast("HYB2-compatible raw count table downloaded locally.");
        } catch (error) {
          api.showToast(error && error.message ? error.message : "The raw count table could not be exported.");
        }
      } else if (action === "comparison-export-hyb2-names") {
        api.download(
          "hyb2-web_names.table",
          window.Hyb2Data.comparisonLegacyNamesToTsv(state.comparison),
          "text/tab-separated-values"
        );
        api.showToast("HYB2-compatible two-column names table downloaded locally.");
      } else {
        api.download(
          "deseq2-sample-metadata.tsv",
          window.Hyb2Data.comparisonSampleMetadataToTsv(state.comparison),
          "text/tab-separated-values"
        );
        api.showToast("Extended headered sample metadata downloaded for audit or custom R workflows.");
      }
      return true;
    }

    if (action === "comparison-export-selected") {
      const selected = state.comparison.selection && state.comparison.result && state.comparison.result.cellMap
        ? state.comparison.result.cellMap[state.comparison.selection.x + ":" + state.comparison.selection.y]
        : null;
      if (!selected) {
        api.showToast("Select a comparison bin before exporting dataset values.");
        return true;
      }
      api.download(
        "comparison-selected-datasets.tsv",
        window.Hyb2Data.comparisonSelectedToTsv(state.comparison, selected, state.comparison.result.binSize),
        "text/tab-separated-values"
      );
      api.showToast("Selected-bin dataset values downloaded locally.");
      return true;
    }

    if (action === "comparison-explore-region") {
      const selected = state.comparison.selection;
      const result = state.comparison.result;
      if (!selected || !result) {
        return true;
      }
      state.region.rna = state.comparison.rnaX;
      state.region.start = Math.max(1, selected.x);
      state.region.end = selected.x + result.binSize - 1;
      state.region.partner = state.comparison.rnaY;
      state.region.partnerStart = Math.max(1, selected.y);
      state.region.partnerEnd = selected.y + result.binSize - 1;
      state.region.results = null;
      api.navigate("region");
      return true;
    }

    if (action === "comparison-download-png") {
      if (!ensureVisualExport(state, "comparison", api)) {
        return true;
      }
      downloadCanvasPng("comparison-effect-map.png", api, "comparison-effect-canvas", "Comparison effect PNG downloaded locally.");
      return true;
    }

    if (action === "comparison-download-svg") {
      if (!ensureVisualExport(state, "comparison", api)) {
        return true;
      }
      api.download("comparison-effect-map.svg", buildComparisonSvg(state), "image/svg+xml");
      api.showToast("Comparison effect SVG downloaded locally.");
      return true;
    }

    if (action === "choose-fasta") {
      api.chooseFasta();
      return true;
    }

    if (action === "edit-fasta-mapping") {
      if (!state.fasta) {
        return true;
      }
      const rna = element.dataset.rna || "";
      state.fasta.mappingEditorRna = rna;
      state.fasta.mappingEditorReference = state.fasta.mapping[rna] || "";
      api.render();
      return true;
    }

    if (action === "apply-fasta-mapping" || action === "remove-current-fasta-mapping") {
      if (!state.fasta || !state.summary) {
        return true;
      }
      const rna = String(state.fasta.mappingEditorRna || "").trim();
      const knownRna = (state.summary.rnaNames || []).indexOf(rna) !== -1;
      if (!knownRna) {
        api.showToast("Enter an exact RNA name from the loaded HYB file.");
        return true;
      }
      const reference = action === "remove-current-fasta-mapping"
        ? ""
        : String(state.fasta.mappingEditorReference || "").trim();
      if (reference && !(state.fasta.sequences || []).some(function (entry) { return entry.id === reference; })) {
        api.showToast("Enter an exact reference ID from the loaded FASTA file.");
        return true;
      }
      if (reference) {
        state.fasta.mapping[rna] = reference;
      } else {
        delete state.fasta.mapping[rna];
      }
      state.fasta.mappingEditorRna = rna;
      state.fasta.mappingEditorReference = reference;
      invalidateFastaConsumers(state);
      api.render();
      api.showToast(reference ? "Reference mapping updated locally." : "RNA marked as not matched.");
      return true;
    }

    if (action === "replace-hyb") {
      state.dialog = null;
      api.render();
      api.chooseHyb();
      return true;
    }

    if (action === "clear-hyb") {
      state.dialog = "clear";
      api.render();
      return true;
    }

    if (action === "remove-fasta") {
      api.removeFasta();
      return true;
    }

    if (action === "export-fasta-mapping") {
      api.download("fasta-mapping.json", JSON.stringify({
        application: "HYB2 Web Lite",
        version: "0.5.0",
        buildCommit: buildCommit(),
        generatedLocally: true,
        sourceHyb: sourceHyb(state),
        sourceFasta: sourceFasta(state, null),
        mapping: state.fasta.mapping
      }, null, 2), "application/json");
      api.showToast("FASTA mapping downloaded locally.");
      return true;
    }

    if (action === "explore-region") {
      state.region.results = null;
      api.render();
      return true;
    }

    if (action === "copy-region-sequence" || action === "region-send-structure" || action === "region-download-fasta") {
      const reference = window.Hyb2Data.extractReference(state.fasta, state.region.rna, state.region.start, state.region.end);
      if (!reference) {
        api.showToast("Reference sequence is unavailable for this region.");
        return true;
      }
      if (action === "copy-region-sequence") {
        api.copyText(reference.sequence, "Reference sequence copied to the clipboard.");
      }
      if (action === "region-send-structure") {
        if (window.Hyb2Structure) {
          window.Hyb2Structure.reset(state, { clearConstraints: true });
        }
        state.structure.source = "reference";
        state.structure.rna = state.region.rna;
        state.structure.start = reference.start;
        state.structure.end = reference.end;
        api.navigate("structure");
      }
      if (action === "region-download-fasta") {
        api.download(reference.id + "_" + reference.start + "-" + reference.end + ".fasta", ">" + reference.id + ":" + reference.start + "-" + reference.end + "\n" + reference.sequence + "\n", "text/plain");
        api.showToast("Region FASTA downloaded locally.");
      }
      return true;
    }

    if (action === "region-export-csv" || action === "region-export-hyb") {
      const records = state.region.results ? state.region.results.records : [];
      const content = action === "region-export-csv" ? window.Hyb2Data.recordsToCsv(records) : window.Hyb2Data.recordsToHyb(records);
      const fileName = action === "region-export-csv" ? "region-interactions.csv" : "region-interactions.hyb";
      api.download(fileName, content, action === "region-export-csv" ? "text/csv" : "text/plain");
      api.showToast("Region results downloaded locally.");
      return true;
    }

    if (action === "region-export-params") {
      api.download("region-parameters.json", JSON.stringify(regionParameters(state), null, 2), "application/json");
      api.showToast("Region parameters downloaded locally.");
      return true;
    }

    if (action === "copy-structure-sequence" || action === "structure-download-fasta") {
      const info = window.Hyb2Pages.getStructureSequence(state);
      if (!info.sequence) {
        api.showToast("Choose a valid sequence first.");
        return true;
      }
      if (action === "copy-structure-sequence") {
        api.copyText(info.sequence, "Prepared RNA sequence copied to the clipboard.");
      } else {
        api.download("structure-input.fasta", ">HYB2_Web_prepared_sequence\n" + info.sequence + "\n", "text/plain");
        api.showToast("Structure input FASTA downloaded locally.");
      }
      return true;
    }

    return false;
  }

  function buildContactSvg(state) {
    if (!visualExportReady(state, "contact")) {
      return "";
    }
    const colors = exportColors();
    const matrix = state.contact.matrix;
    const width = 900;
    const height = 720;
    const geometry = getContactGeometry(state, width, height);
    const rects = matrix.cells.filter(function (cell) {
      return cell.x >= geometry.xStart && cell.x <= geometry.xEnd && cell.y >= geometry.yStart && cell.y <= geometry.yEnd;
    }).map(function (cell) {
      const visualValue = state.contact.scale === "log" ? Math.log1p(cell.value) : cell.value;
      const ratio = geometry.cap ? Math.max(0, Math.min(1, visualValue / geometry.cap)) : 0;
      const x = geometry.left + ((cell.x - geometry.xStart) / matrix.binSize) * geometry.cellWidth;
      const y = geometry.top + ((cell.y - geometry.yStart) / matrix.binSize) * geometry.cellHeight;
      return '<rect x="' + x.toFixed(2) + '" y="' + y.toFixed(2) + '" width="' + (geometry.cellWidth + .4).toFixed(2) + '" height="' + (geometry.cellHeight + .4).toFixed(2) + '" fill="' + viridis(ratio) + '"/>';
    }).join("");

    return [
      '<?xml version="1.0" encoding="UTF-8"?>',
      '<svg xmlns="http://www.w3.org/2000/svg" width="' + width + '" height="' + height + '" viewBox="0 0 ' + width + " " + height + '">',
      '<rect width="100%" height="100%" fill="' + colors.surface + '"/>',
      '<rect x="' + geometry.left + '" y="' + geometry.top + '" width="' + geometry.plotWidth + '" height="' + geometry.plotHeight + '" fill="' + colors.line + '"/>',
      rects,
      '<rect x="' + geometry.left + '" y="' + geometry.top + '" width="' + geometry.plotWidth + '" height="' + geometry.plotHeight + '" fill="none" stroke="' + colors.ink + '" stroke-width="1"/>',
      '<text x="' + geometry.left + '" y="' + (geometry.top + geometry.plotHeight + 28) + '" font-family="monospace" font-size="12" fill="' + colors.muted + '">' + geometry.xStart + "</text>",
      '<text x="' + (geometry.left + geometry.plotWidth) + '" y="' + (geometry.top + geometry.plotHeight + 28) + '" text-anchor="end" font-family="monospace" font-size="12" fill="' + colors.muted + '">' + (geometry.xEnd + matrix.binSize - 1) + "</text>",
      '<text x="' + (geometry.left - 8) + '" y="' + (geometry.top + 4) + '" text-anchor="end" font-family="monospace" font-size="12" fill="' + colors.muted + '">' + geometry.yStart + "</text>",
      '<text x="' + (geometry.left - 8) + '" y="' + (geometry.top + geometry.plotHeight) + '" text-anchor="end" font-family="monospace" font-size="12" fill="' + colors.muted + '">' + (geometry.yEnd + matrix.binSize - 1) + "</text>",
      '<text x="' + geometry.left + '" y="16" font-family="system-ui, sans-serif" font-size="15" fill="' + colors.ink + '">' + escapeXml(state.contact.rnaX) + " × " + escapeXml(state.contact.rnaY) + "</text>",
      "</svg>"
    ].join("");
  }

  function buildViewpointSvg(state) {
    const results = state.viewpoint && state.viewpoint.results;
    if (!visualExportReady(state, "viewpoint")) {
      return "";
    }
    const colors = exportColors();
    const width = 1040;
    const height = 300;
    const geometry = getViewpointGeometry(results, width, height);
    const bars = results.bins.map(function (bin, index) {
      const barHeight = geometry.max ? (bin.value / geometry.scaleMax) * geometry.plotHeight : 0;
      const selected = state.viewpoint.selection && state.viewpoint.selection.start === bin.start;
      return '<rect x="' + (geometry.left + index * geometry.barWidth).toFixed(2) + '" y="' + (geometry.top + geometry.plotHeight - barHeight).toFixed(2) + '" width="' + Math.max(1, geometry.barWidth - .35).toFixed(2) + '" height="' + barHeight.toFixed(2) + '" fill="' + (selected ? colors.accent : colors.primary) + '"/>';
    }).join("");
    return [
      '<?xml version="1.0" encoding="UTF-8"?>',
      '<svg xmlns="http://www.w3.org/2000/svg" width="' + width + '" height="' + height + '" viewBox="0 0 ' + width + ' ' + height + '">',
      '<rect width="100%" height="100%" fill="' + colors.surface + '"/>',
      '<line x1="' + geometry.left + '" y1="' + (geometry.top + geometry.plotHeight) + '" x2="' + (geometry.left + geometry.plotWidth) + '" y2="' + (geometry.top + geometry.plotHeight) + '" stroke="' + colors.line + '"/>',
      bars,
      '<text x="' + geometry.left + '" y="12" font-family="system-ui, sans-serif" font-size="14" fill="' + colors.ink + '">' + escapeXml(results.rna) + ' viewpoint</text>',
      '<text x="' + geometry.left + '" y="' + (geometry.top + geometry.plotHeight + 24) + '" font-family="monospace" font-size="11" fill="' + colors.muted + '">' + results.start + '</text>',
      '<text x="' + (geometry.left + geometry.plotWidth) + '" y="' + (geometry.top + geometry.plotHeight + 24) + '" text-anchor="end" font-family="monospace" font-size="11" fill="' + colors.muted + '">' + results.end + '</text>',
      '</svg>'
    ].join("");
  }

  function buildComparisonSvg(state) {
    const result = state.comparison && state.comparison.result;
    if (!visualExportReady(state, "comparison")) {
      return "";
    }
    const colors = exportColors();
    const width = 720;
    const height = 520;
    const geometry = getComparisonGeometry(result, width, height);
    const cells = result.cells.map(function (cell) {
      const x = geometry.left + ((cell.x - geometry.xStart) / result.binSize) * geometry.cellWidth;
      const y = geometry.top + ((cell.y - geometry.yStart) / result.binSize) * geometry.cellHeight;
      return '<rect x="' + x.toFixed(2) + '" y="' + y.toFixed(2) + '" width="' + (geometry.cellWidth + .4).toFixed(2) + '" height="' + (geometry.cellHeight + .4).toFixed(2) + '" fill="' + effectColor(cell.effect / Math.max(.01, result.effectCap)) + '"/>';
    }).join("");
    return [
      '<?xml version="1.0" encoding="UTF-8"?>',
      '<svg xmlns="http://www.w3.org/2000/svg" width="' + width + '" height="' + height + '" viewBox="0 0 ' + width + ' ' + height + '">',
      '<rect width="100%" height="100%" fill="' + colors.surface + '"/>',
      '<rect x="' + geometry.left + '" y="' + geometry.top + '" width="' + geometry.plotWidth + '" height="' + geometry.plotHeight + '" fill="' + colors.line + '"/>',
      cells,
      '<rect x="' + geometry.left + '" y="' + geometry.top + '" width="' + geometry.plotWidth + '" height="' + geometry.plotHeight + '" fill="none" stroke="' + colors.ink + '"/>',
      '<text x="' + geometry.left + '" y="15" font-family="system-ui, sans-serif" font-size="13" fill="' + colors.ink + '">' + escapeXml(state.comparison.rnaX) + ' (X) × ' + escapeXml(state.comparison.rnaY) + ' (Y) · Mean log2 effect</text>',
      '<text x="' + geometry.left + '" y="29" font-family="system-ui, sans-serif" font-size="10" fill="' + colors.muted + '">' + escapeXml(state.comparison.conditionALabel) + ' enriched: violet · ' + escapeXml(state.comparison.conditionBLabel) + ' enriched: teal</text>',
      '<text x="' + geometry.left + '" y="' + (geometry.top + geometry.plotHeight + 22) + '" font-family="monospace" font-size="11" fill="' + colors.muted + '">' + geometry.xStart + '</text>',
      '<text x="' + (geometry.left + geometry.plotWidth) + '" y="' + (geometry.top + geometry.plotHeight + 22) + '" text-anchor="end" font-family="monospace" font-size="11" fill="' + colors.muted + '">' + (geometry.xEnd + result.binSize - 1) + '</text>',
      '<text x="' + (geometry.left - 8) + '" y="' + (geometry.top + 4) + '" text-anchor="end" font-family="monospace" font-size="11" fill="' + colors.muted + '">' + geometry.yStart + '</text>',
      '<text x="' + (geometry.left - 8) + '" y="' + (geometry.top + geometry.plotHeight) + '" text-anchor="end" font-family="monospace" font-size="11" fill="' + colors.muted + '">' + (geometry.yEnd + result.binSize - 1) + '</text>',
      '</svg>'
    ].join("");
  }

  function downloadCanvasPng(fileName, api, canvasId, confirmation, fallbackCanvasId) {
    const canvas = document.getElementById(canvasId) || (fallbackCanvasId ? document.getElementById(fallbackCanvasId) : null);
    if (!canvas) {
      api.showToast("The visual canvas is unavailable. Render the analysis and try again.");
      return;
    }
    try {
      canvas.toBlob(function (blob) {
        if (!blob) {
          api.showToast("This browser could not create the PNG. Try the SVG export instead.");
          return;
        }
        api.downloadBlob(fileName, blob);
        api.showToast(confirmation || "PNG downloaded locally.");
      }, "image/png");
    } catch (error) {
      api.showToast("This browser could not create the PNG. Try the SVG export instead.");
    }
  }

  function visualExportReady(state, kind) {
    if (kind === "contact") {
      const matrix = state.contact && state.contact.matrix;
      return Boolean(matrix && Array.isArray(matrix.cells) && matrix.cells.length);
    }
    if (kind === "viewpoint") {
      const results = state.viewpoint && state.viewpoint.results;
      return Boolean(results && results.ready && Array.isArray(results.bins) && results.bins.length);
    }
    if (kind === "comparison") {
      const result = state.comparison && state.comparison.result;
      return Boolean(result && result.ready && Array.isArray(result.cells) && result.cells.length);
    }
    return false;
  }

  function ensureVisualExport(state, kind, api) {
    if (visualExportReady(state, kind)) {
      return true;
    }
    const labels = { contact: "contact map", viewpoint: "viewpoint", comparison: "comparison" };
    api.showToast("Generate a " + (labels[kind] || "visual") + " with data before downloading.");
    return false;
  }

  function contactParameters(state) {
    const rawCustomCap = String(state.contact.customCap == null ? "" : state.contact.customCap).trim();
    const parsedCustomCap = rawCustomCap ? Number(rawCustomCap) : null;
    const customCap = Number.isFinite(parsedCustomCap) && parsedCustomCap > 0 ? parsedCustomCap : null;
    const matrix = state.contact.matrix;
    const binSize = Number(state.contact.binSize);
    const hasExtent = matrix && [matrix.xMin, matrix.xMax, matrix.yMin, matrix.yMax].every(Number.isFinite);
    const view = hasExtent ? getContactView(state) : null;
    const exportStage = hasExtent ? getContactExportStage(state) : null;
    const selected = state.contact.selection;
    return {
      application: "HYB2 Web Lite",
      version: "0.5.0",
      buildCommit: buildCommit(),
      analysis: "contact-map",
      generatedLocally: true,
      sourceHyb: sourceHyb(state),
      parameters: {
        rnaX: state.contact.rnaX,
        rnaY: state.contact.rnaY,
        binSize: binSize,
        measure: state.contact.measure === "support" ? "cluster-support" : "record-count",
        colourCap: state.contact.colourCap,
        customCap: state.contact.colourCap === "custom" ? customCap : null,
        effectiveColourCap: state.contact.matrix && Number.isFinite(state.contact.matrix.cap) ? state.contact.matrix.cap : null,
        scale: state.contact.scale,
        orientation: state.contact.orientation,
        chimeraType: state.contact.chimeraType,
        homodimerSubset: state.contact.homodimer || "all",
        exportedView: view ? {
          stage: exportStage,
          xStart: view.xStart,
          xEnd: view.xEnd + binSize - 1,
          yStart: view.yStart,
          yEnd: view.yEnd + binSize - 1
        } : null,
        selectedCell: selected ? {
          xStart: selected.x,
          xEnd: selected.x + binSize - 1,
          yStart: selected.y,
          yEnd: selected.y + binSize - 1
        } : null
      }
    };
  }

  function interactionParameters(state) {
    return {
      application: "HYB2 Web Lite",
      version: "0.5.0",
      buildCommit: buildCommit(),
      analysis: "interactions",
      generatedLocally: true,
      sourceHyb: sourceHyb(state),
      parameters: Object.assign({}, state.filters || {}),
      result: {
        matchingRecords: (state.interactionResults || []).length
      }
    };
  }

  function viewpointParameters(state) {
    const viewpoint = state.viewpoint || {};
    const result = viewpoint.results || {};
    return {
      application: "HYB2 Web Lite",
      version: "0.5.0",
      buildCommit: buildCommit(),
      analysis: "viewpoint",
      generatedLocally: true,
      sourceHyb: sourceHyb(state),
      sourceFasta: sourceFasta(state, viewpoint.rna),
      parameters: {
        rna: viewpoint.rna || "",
        partner: viewpoint.partner || null,
        chimeraType: viewpoint.chimeraType || "all",
        homodimerSubset: viewpoint.homodimer || "all",
        rangeMode: viewpoint.rangeMode || "coordinates",
        requestedStart: nullableNumber(viewpoint.start),
        requestedEnd: nullableNumber(viewpoint.end),
        effectiveStart: nullableNumber(result.start),
        effectiveEnd: nullableNumber(result.end),
        requestedBinSize: nullableNumber(viewpoint.binSize),
        effectiveBinSize: nullableNumber(result.binSize),
        measure: viewpoint.measure === "support" ? "cluster-support" : "record-count"
      },
      result: {
        matchingRecords: Array.isArray(result.records) ? result.records.length : 0,
        armContributions: Number(result.armContributions) || 0,
        totalCoverage: Number(result.totalCoverage) || 0
      }
    };
  }

  function regionParameters(state) {
    const region = state.region || {};
    const result = region.results || {};
    return {
      application: "HYB2 Web Lite",
      version: "0.5.0",
      buildCommit: buildCommit(),
      analysis: "region",
      generatedLocally: true,
      sourceHyb: sourceHyb(state),
      sourceFasta: sourceFasta(state, region.rna),
      parameters: {
        rna: region.rna || "",
        start: nullableNumber(region.start),
        end: nullableNumber(region.end),
        partner: region.partner || null,
        partnerStart: nullableNumber(region.partnerStart),
        partnerEnd: nullableNumber(region.partnerEnd),
        overlapRule: region.overlap || "any",
        effectiveProfileBinSize: nullableNumber(result.profileBinSize)
      },
      result: {
        matchingRecords: Array.isArray(result.records) ? result.records.length : 0,
        clusterSupport: Number(result.support) || 0,
        partnerRnas: Array.isArray(result.partners) ? result.partners.length : 0
      }
    };
  }

  function comparisonParameters(state) {
    const comparison = state.comparison || {};
    const result = comparison.result || {};
    const normalise = comparison.normalise === "none" ? "none" : "per-million-library-size";
    const selectedBin = comparison.selection && result.ready ? {
      xStart: comparison.selection.x,
      xEnd: comparison.selection.x + result.binSize - 1,
      yStart: comparison.selection.y,
      yEnd: comparison.selection.y + result.binSize - 1
    } : null;
    return {
      application: "HYB2 Web Lite",
      version: "0.5.0",
      buildCommit: buildCommit(),
      analysis: "comparison",
      generatedLocally: true,
      sourceHyb: sourceHyb(state),
      datasets: (comparison.datasets || []).map(function (dataset) {
        const summary = dataset.summary || {};
        return {
          id: dataset.id,
          label: dataset.label || "",
          condition: dataset.condition,
          fileName: dataset.fileName || summary.fileName || "",
          fileSize: dataset.fileSize == null ? (summary.fileSize == null ? null : summary.fileSize) : dataset.fileSize,
          sha256: dataset.sha256 || summary.sha256 || null,
          sha256Status: dataset.sha256 || summary.sha256
            ? "available"
            : (dataset.sha256Unavailable || summary.sha256Unavailable ? "unavailable" : "pending"),
          validRecords: Number(summary.validRecords) || 0,
          clusterSupport: Number(summary.supportInteractions) || 0
        };
      }),
      parameters: {
        conditionALabel: comparison.conditionALabel || "Condition A",
        conditionBLabel: comparison.conditionBLabel || "Condition B",
        rnaX: comparison.rnaX || "",
        rnaY: comparison.rnaY || "",
        binSize: nullableNumber(comparison.binSize),
        measure: comparison.measure === "support" ? "cluster-support" : "record-count",
        normalisation: normalise,
        pseudocount: normalise === "per-million-library-size" ? 1 : 0.5
      },
      result: {
        ready: result.ready === true,
        comparableBins: Array.isArray(result.cells) ? result.cells.length : 0,
        conservedBins: Array.isArray(result.conservedCells) ? result.conservedCells.length : 0,
        selectedBin: selectedBin,
        failureReason: result.ready === false ? result.reason || null : null
      },
      methodScope: {
        descriptiveEffectOnly: true,
        replicateAwareStatisticalTesting: false,
        adjustedPValues: false,
        webRBaseRuntimeSmoke: "recorded-manual-pass",
        webRRuntimeValidated: false,
        webRRuntimeVendored: false,
        webRDeploymentCiSmoke: false,
        pinnedDeSeq2ClosureValidated: false,
        deSeq2WasmReady: false,
        releaseBlocker: "Pinned DESeq2 1.52.0 WebAssembly closure, static library load, and native-R numerical parity",
        localCliExportAvailable: true
      }
    };
  }

  function sourceHyb(state) {
    const summary = state.summary || {};
    return {
      fileName: summary.fileName || "",
      fileSize: summary.fileSize == null ? null : summary.fileSize,
      sha256: summary.sha256 || null,
      sha256Status: summary.sha256
        ? "available"
        : (summary.sha256Unavailable ? "unavailable" : "pending")
    };
  }

  function sourceFasta(state, rna) {
    if (!state.fasta) {
      return null;
    }
    return {
      fileName: state.fasta.fileName || "",
      fileSize: state.fasta.fileSize == null ? null : state.fasta.fileSize,
      sha256: state.fasta.sha256 || null,
      sha256Status: state.fasta.sha256
        ? "available"
        : (state.fasta.sha256Unavailable ? "unavailable" : "pending"),
      mappedReferenceId: state.fasta.mapping && rna ? state.fasta.mapping[rna] || null : null
    };
  }

  function nullableNumber(value) {
    const text = String(value == null ? "" : value).trim();
    if (!text) {
      return null;
    }
    const number = Number(text);
    return Number.isFinite(number) ? number : null;
  }

  function buildCommit() {
    return window.HYB2_BUILD && window.HYB2_BUILD.commit || null;
  }

  function contactYAxisLabels(geometry, binSize) {
    return {
      top: geometry.yStart,
      bottom: geometry.yEnd + binSize - 1
    };
  }

  function viridis(ratio) {
    const bounded = Math.max(0, Math.min(1, ratio));
    const scaled = bounded * (BRAND_SCALE.length - 1);
    const index = Math.min(BRAND_SCALE.length - 2, Math.floor(scaled));
    const t = scaled - index;
    const from = BRAND_SCALE[index];
    const to = BRAND_SCALE[index + 1];
    const red = Math.round(from[0] + (to[0] - from[0]) * t);
    const green = Math.round(from[1] + (to[1] - from[1]) * t);
    const blue = Math.round(from[2] + (to[2] - from[2]) * t);
    return "rgb(" + red + ", " + green + ", " + blue + ")";
  }

  function effectColor(value) {
    const bounded = Math.max(-1, Math.min(1, value));
    const dark = isDarkTheme();
    const center = dark ? [19, 44, 71] : [245, 248, 250];
    const target = bounded >= 0
      ? (dark ? [154, 136, 255] : [106, 79, 242])
      : [0, 160, 157];
    const ratio = Math.abs(bounded);
    const red = Math.round(center[0] + (target[0] - center[0]) * ratio);
    const green = Math.round(center[1] + (target[1] - center[1]) * ratio);
    const blue = Math.round(center[2] + (target[2] - center[2]) * ratio);
    return "rgb(" + red + ", " + green + ", " + blue + ")";
  }

  function conservedColor(ratio) {
    const bounded = Math.max(0, Math.min(1, ratio));
    const dark = isDarkTheme();
    const from = dark ? [24, 54, 80] : [237, 249, 248];
    const to = [0, 160, 157];
    const red = Math.round(from[0] + (to[0] - from[0]) * bounded);
    const green = Math.round(from[1] + (to[1] - from[1]) * bounded);
    const blue = Math.round(from[2] + (to[2] - from[2]) * bounded);
    return "rgb(" + red + ", " + green + ", " + blue + ")";
  }

  function exportColors() {
    const dark = isDarkTheme();
    const fallback = dark
      ? {
        surface: "#132c47",
        line: "#2e4b66",
        ink: "#ecf1f8",
        muted: "#9eb1c3",
        primary: "#00a09d",
        accent: "#9a88ff"
      }
      : {
        surface: "#ffffff",
        line: "#dce7ec",
        ink: "#132c47",
        muted: "#607286",
        primary: "#00a09d",
        accent: "#6a4ff2"
      };
    if (typeof document === "undefined" || typeof getComputedStyle !== "function" || !document.documentElement) {
      return fallback;
    }
    const style = getComputedStyle(document.documentElement);
    return {
      surface: style.getPropertyValue("--surface").trim() || fallback.surface,
      line: style.getPropertyValue("--line").trim() || fallback.line,
      ink: style.getPropertyValue("--ink").trim() || fallback.ink,
      muted: style.getPropertyValue("--muted").trim() || fallback.muted,
      primary: style.getPropertyValue("--primary").trim() || fallback.primary,
      accent: style.getPropertyValue("--accent").trim() || fallback.accent
    };
  }

  function isDarkTheme() {
    return typeof document !== "undefined" && document.documentElement && document.documentElement.dataset.theme === "dark";
  }

  function clampBin(value, minimum, maximum, binSize) {
    const rounded = Math.round(value / binSize) * binSize;
    return Math.max(minimum, Math.min(maximum, rounded));
  }

  function range(start, end) {
    return format(start) + "–" + format(end);
  }

  function format(value) {
    return new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 }).format(value || 0);
  }

  function escapeXml(value) {
    return String(value == null ? "" : value).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  }

  window.Hyb2UI = {
    afterRender: afterRender,
    handleInput: handleInput,
    handleChange: handleChange,
    handleAction: handleAction,
    updateVirtualRows: updateVirtualRows,
    buildContactSvg: buildContactSvg,
    contactParameters: contactParameters,
    interactionParameters: interactionParameters,
    viewpointParameters: viewpointParameters,
    regionParameters: regionParameters,
    comparisonParameters: comparisonParameters,
    contactYAxisLabels: contactYAxisLabels,
    visualExportReady: visualExportReady,
    moveComparisonSelection: moveComparisonSelection
  };
}());
