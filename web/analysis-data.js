(function () {
  "use strict";

  const MAX_CONTACT_CELLS = 100000;
  const MAX_CONTACT_BIN_CONTRIBUTIONS = 2000000;
  const MAX_COMPARISON_CELLS = 100000;
  const MAX_COMPARISON_DATASET_CELLS = 500000;
  const MAX_R_INTEGER_COUNT = 2147483647;

  function defaultInteractionFilters() {
    return {
      rna: "",
      partner: "",
      type: "all",
      chimeraType: "all",
      search: "",
      armOneStart: "",
      armOneEnd: "",
      armTwoStart: "",
      armTwoEnd: "",
      maxEvalue: "",
      minDg: "",
      maxDg: "",
      minSupport: "1",
      countMode: "support"
    };
  }

  function defaultContactState(summary) {
    const pair = summary && summary.pairCounts && summary.pairCounts[0];
    return {
      rnaX: pair ? pair.rnaOne : "",
      rnaY: pair ? pair.rnaTwo : "",
      binSize: 10,
      measure: "records",
      colourCap: "95",
      customCap: "",
      scale: "linear",
      orientation: "normalised",
      chimeraType: "all",
      homodimer: "all",
      matrix: null,
      selection: null,
      showDataTable: false,
      overviewSelection: null,
      zoomSelection: null,
      overviewView: null,
      zoomView: null,
      detailView: null
    };
  }

  function defaultRegionState(summary, records) {
    const rna = summary && summary.rnaCounts && summary.rnaCounts[0] ? summary.rnaCounts[0].name : "";
    const extent = getRnaExtent(records || [], rna);
    return {
      rna: rna,
      start: extent ? extent.min : "",
      end: extent ? Math.min(extent.max, (extent.min || 0) + 200) : "",
      partner: "",
      partnerStart: "",
      partnerEnd: "",
      overlap: "any",
      results: null
    };
  }

  function defaultViewpointState(summary, records) {
    const rna = summary && summary.rnaCounts && summary.rnaCounts[0] ? summary.rnaCounts[0].name : "";
    const extent = getRnaExtent(records || [], rna);
    return {
      rna: rna,
      partner: "",
      chimeraType: "all",
      homodimer: "all",
      rangeMode: "coordinates",
      start: extent ? extent.min : "",
      end: extent ? extent.max : "",
      binSize: "1",
      measure: "records",
      results: null,
      selection: null,
      showDataTable: false
    };
  }

  function defaultComparisonState(summary, records) {
    const pair = summary && summary.pairCounts && summary.pairCounts[0];
    return {
      datasets: summary ? [{
        id: "primary",
        fileName: summary.fileName,
        fileSize: summary.fileSize,
        summary: summary,
        records: records || [],
        condition: "A",
        label: "Primary"
      }] : [],
      loading: null,
      error: "",
      rnaX: pair ? pair.rnaOne : "",
      rnaY: pair ? pair.rnaTwo : "",
      binSize: "10",
      measure: "records",
      normalise: "library",
      conditionALabel: "Condition A",
      conditionBLabel: "Condition B",
      result: null,
      selection: null,
      activeMap: "effect",
      showDataTable: false
    };
  }

  function defaultStructureState() {
    return {
      source: "reference",
      rna: "",
      start: "",
      end: "",
      pastedSequence: "",
      selectedRecordIndex: null,
      minimumLoop: "3",
      temperature: "37",
      constraintMode: "none",
      constraintText: "",
      evidenceLayout: "single",
      secondRna: "",
      secondStart: "",
      secondEnd: "",
      homodimerOnly: false,
      constraintLimit: "75",
      randomFoldCount: "0",
      randomSeed: "HYB2-Web",
      allowLargeEnsemble: false,
      allowLarge: false,
      selectedNucleotide: null,
      status: "idle",
      progress: 0,
      message: "",
      result: null,
      runningSequenceInfo: null,
      runId: 0
    };
  }

  function getRnaExtent(records, rna) {
    let min = Infinity;
    let max = -Infinity;

    records.forEach(function (record) {
      if (record.rnaOne === rna) {
        min = Math.min(min, record.rnaOneStart);
        max = Math.max(max, record.rnaOneEnd);
      }
      if (record.rnaTwo === rna) {
        min = Math.min(min, record.rnaTwoStart);
        max = Math.max(max, record.rnaTwoEnd);
      }
    });

    return Number.isFinite(min) ? { min: min, max: max } : null;
  }

  function getFilteredRecords(state) {
    const filters = state.filters || defaultInteractionFilters();
    const query = String(filters.search || "").trim().toLowerCase();
    const armOneStart = coordinateOrNull(filters.armOneStart);
    const armOneEnd = coordinateOrNull(filters.armOneEnd);
    const armTwoStart = coordinateOrNull(filters.armTwoStart);
    const armTwoEnd = coordinateOrNull(filters.armTwoEnd);
    const maxEvalue = numberOrNull(filters.maxEvalue);
    const minDg = numberOrNull(filters.minDg);
    const maxDg = numberOrNull(filters.maxDg);
    const minSupport = numberOrNull(filters.minSupport);

    const coordinateFilters = [
      [filters.armOneStart, armOneStart],
      [filters.armOneEnd, armOneEnd],
      [filters.armTwoStart, armTwoStart],
      [filters.armTwoEnd, armTwoEnd]
    ];
    const hasInvalidCoordinate = coordinateFilters.some(function (entry) {
      return String(entry[0] == null ? "" : entry[0]).trim() && entry[1] === null;
    });

    if (hasInvalidCoordinate || (armOneStart !== null && armOneEnd !== null && armOneStart > armOneEnd) ||
        (armTwoStart !== null && armTwoEnd !== null && armTwoStart > armTwoEnd)) {
      return [];
    }

    return (state.records || []).filter(function (record) {
      if (filters.rna && record.rnaOne !== filters.rna && record.rnaTwo !== filters.rna) {
        return false;
      }

      if (filters.partner) {
        if (!filters.rna) {
          if (record.rnaOne !== filters.partner && record.rnaTwo !== filters.partner) {
            return false;
          }
        } else if (partnerForRecord(record, filters.rna) !== filters.partner) {
          return false;
        }
      }

      if (filters.type === "intra" && record.rnaOne !== record.rnaTwo) {
        return false;
      }
      if (filters.type === "inter" && record.rnaOne === record.rnaTwo) {
        return false;
      }
      if (filters.type === "homodimer" && !record.isHomodimer) {
        return false;
      }

      if (filters.chimeraType && filters.chimeraType !== "all" && record.chimeraType !== filters.chimeraType) {
        return false;
      }

      if (query) {
        const searchable = [
          record.id,
          record.sequence,
          record.rnaOne,
          record.rnaTwo,
          record.chimeraType,
          record.overlapScore === null ? "" : record.overlapScore
        ].join(" ").toLowerCase();
        if (searchable.indexOf(query) === -1) {
          return false;
        }
      }

      if (!overlapsOptional(record.rnaOneStart, record.rnaOneEnd, armOneStart, armOneEnd)) {
        return false;
      }
      if (!overlapsOptional(record.rnaTwoStart, record.rnaTwoEnd, armTwoStart, armTwoEnd)) {
        return false;
      }

      if (maxEvalue !== null) {
        const values = [record.rnaOneEvalue, record.rnaTwoEvalue].filter(function (value) { return value !== null; });
        if (values.length && values.some(function (value) { return value > maxEvalue; })) {
          return false;
        }
      }

      if (minDg !== null && (record.dg === null || record.dg < minDg)) {
        return false;
      }
      if (maxDg !== null && (record.dg === null || record.dg > maxDg)) {
        return false;
      }
      if (minSupport !== null && record.supportCount < minSupport) {
        return false;
      }

      return true;
    });
  }

  function partnerForRecord(record, rna) {
    if (record.rnaOne === rna && record.rnaTwo === rna) {
      return rna;
    }
    if (record.rnaOne === rna) {
      return record.rnaTwo;
    }
    if (record.rnaTwo === rna) {
      return record.rnaOne;
    }
    return "";
  }

  function getPartnerCounts(records, rna, countMode) {
    const counts = Object.create(null);

    records.forEach(function (record) {
      const partner = rna ? partnerForRecord(record, rna) : "";
      if (!partner) {
        return;
      }

      const existing = counts[partner] || { name: partner, records: 0, support: 0 };
      existing.records += 1;
      existing.support += record.supportCount;
      counts[partner] = existing;
    });

    return Object.keys(counts)
      .map(function (name) { return counts[name]; })
      .sort(function (left, right) {
        const leftValue = countMode === "records" ? left.records : left.support;
        const rightValue = countMode === "records" ? right.records : right.support;
        return rightValue - leftValue || left.name.localeCompare(right.name);
      });
  }

  function orientRecordForPair(record, rnaX, rnaY, orientation) {
    if (record.rnaOne === rnaX && record.rnaTwo === rnaY) {
      return {
        record: record,
        xStart: record.rnaOneStart,
        xEnd: record.rnaOneEnd,
        yStart: record.rnaTwoStart,
        yEnd: record.rnaTwoEnd,
        swapped: false
      };
    }

    if (orientation === "original") {
      return null;
    }

    if (rnaX !== rnaY && record.rnaOne === rnaY && record.rnaTwo === rnaX) {
      return {
        record: record,
        xStart: record.rnaTwoStart,
        xEnd: record.rnaTwoEnd,
        yStart: record.rnaOneStart,
        yEnd: record.rnaOneEnd,
        swapped: true
      };
    }

    return null;
  }

  function buildContactMatrix(records, contact) {
    const requestedBinSize = Number(contact.binSize);
    const binSize = Number.isSafeInteger(requestedBinSize) && requestedBinSize > 0 ? requestedBinSize : 10;
    const cells = Object.create(null);
    let xMin = Infinity;
    let xMax = -Infinity;
    let yMin = Infinity;
    let yMax = -Infinity;
    let recordsUsed = 0;
    let totalSupport = 0;
    let cellCount = 0;
    let binContributions = 0;
    let limitReason = "";

    recordLoop:
    for (let recordIndex = 0; recordIndex < records.length; recordIndex += 1) {
      const record = records[recordIndex];
      if (contact.chimeraType && contact.chimeraType !== "all" && record.chimeraType !== contact.chimeraType) {
        continue;
      }
      if (!matchesHomodimerSubset(record, contact.homodimer)) {
        continue;
      }
      const oriented = orientRecordForPair(record, contact.rnaX, contact.rnaY, contact.orientation);
      if (!oriented) {
        continue;
      }

      const xStartBin = binSize * Math.floor(oriented.xStart / binSize);
      const xEndBin = binSize * Math.floor(oriented.xEnd / binSize);
      const yStartBin = binSize * Math.floor(oriented.yStart / binSize);
      const yEndBin = binSize * Math.floor(oriented.yEnd / binSize);
      const xBinCount = Math.floor((xEndBin - xStartBin) / binSize) + 1;
      const yBinCount = Math.floor((yEndBin - yStartBin) / binSize) + 1;
      const recordContributions = xBinCount * yBinCount;
      const value = contact.measure === "records" ? 1 : record.supportCount;

      if (!Number.isSafeInteger(xBinCount) || !Number.isSafeInteger(yBinCount) ||
          !Number.isSafeInteger(recordContributions) || recordContributions > MAX_CONTACT_CELLS) {
        limitReason = "Contact map not generated: one interaction spans more than " + MAX_CONTACT_CELLS.toLocaleString("en-US") + " bin pairs. Increase the bin size or correct unusually large arm coordinates.";
        break;
      }
      if (recordContributions > MAX_CONTACT_BIN_CONTRIBUTIONS - binContributions) {
        limitReason = "Contact map not generated: the selected records require more than " + MAX_CONTACT_BIN_CONTRIBUTIONS.toLocaleString("en-US") + " bin contributions. Increase the bin size or narrow the selected data.";
        break;
      }

      for (let x = xStartBin; x <= xEndBin; x += binSize) {
        for (let y = yStartBin; y <= yEndBin; y += binSize) {
          const key = x + ":" + y;
          let cell = cells[key];
          if (!cell) {
            if (cellCount >= MAX_CONTACT_CELLS) {
              limitReason = "Contact map not generated: the selected records cover more than " + MAX_CONTACT_CELLS.toLocaleString("en-US") + " distinct cells. Increase the bin size or narrow the selected data.";
              break recordLoop;
            }
            cell = { x: x, y: y, records: 0, support: 0 };
            cells[key] = cell;
            cellCount += 1;
          }
          cell.records += 1;
          cell.support += record.supportCount;
          cell.value = (cell.value || 0) + value;
        }
      }

      binContributions += recordContributions;
      recordsUsed += 1;
      totalSupport += record.supportCount;
      xMin = Math.min(xMin, oriented.xStart);
      xMax = Math.max(xMax, oriented.xEnd);
      yMin = Math.min(yMin, oriented.yStart);
      yMax = Math.max(yMax, oriented.yEnd);
    }

    if (limitReason) {
      return {
        ready: false,
        reason: limitReason,
        limitExceeded: true,
        cells: [],
        cellMap: Object.create(null),
        binSize: binSize,
        xMin: 0,
        xMax: 0,
        yMin: 0,
        yMax: 0,
        recordsUsed: 0,
        totalSupport: 0,
        cellContributions: binContributions,
        maximumCells: MAX_CONTACT_CELLS,
        maximumBinContributions: MAX_CONTACT_BIN_CONTRIBUTIONS,
        max: 0,
        cap: 0
      };
    }

    const entries = Object.keys(cells).map(function (key) { return cells[key]; });
    const values = entries.map(function (cell) { return cell.value; }).sort(function (left, right) { return left - right; });
    const displayValues = values.map(function (value) { return contact.scale === "log" ? Math.log1p(value) : value; });
    const cap = resolveColourCap(displayValues, contact);

    return {
      ready: true,
      reason: "",
      limitExceeded: false,
      cells: entries,
      cellMap: cells,
      binSize: binSize,
      xMin: Number.isFinite(xMin) ? xMin : 0,
      xMax: Number.isFinite(xMax) ? xMax : 0,
      yMin: Number.isFinite(yMin) ? yMin : 0,
      yMax: Number.isFinite(yMax) ? yMax : 0,
      recordsUsed: recordsUsed,
      totalSupport: totalSupport,
      cellContributions: binContributions,
      maximumCells: MAX_CONTACT_CELLS,
      maximumBinContributions: MAX_CONTACT_BIN_CONTRIBUTIONS,
      max: displayValues.length ? displayValues[displayValues.length - 1] : 0,
      cap: cap
    };
  }

  function resolveColourCap(values, contact) {
    if (!values.length) {
      return 0;
    }

    if (contact.colourCap === "max") {
      return values[values.length - 1];
    }

    if (contact.colourCap === "custom") {
      const custom = numberOrNull(contact.customCap);
      return custom !== null && custom > 0 ? custom : values[values.length - 1];
    }

    return quantile(values, (Number(contact.colourCap) || 95) / 100);
  }

  function quantile(sortedValues, fraction) {
    if (!sortedValues.length) {
      return 0;
    }

    const position = Math.max(0, Math.min(sortedValues.length - 1, (sortedValues.length - 1) * fraction));
    const lower = Math.floor(position);
    const upper = Math.ceil(position);
    return sortedValues[lower] + (sortedValues[upper] - sortedValues[lower]) * (position - lower);
  }

  function getCellRecords(records, contact, xBin, yBin) {
    const binSize = Number(contact.binSize) || 10;
    return records.filter(function (record) {
      if (contact.chimeraType && contact.chimeraType !== "all" && record.chimeraType !== contact.chimeraType) {
        return false;
      }
      if (!matchesHomodimerSubset(record, contact.homodimer)) {
        return false;
      }
      const oriented = orientRecordForPair(record, contact.rnaX, contact.rnaY, contact.orientation);
      if (!oriented) {
        return false;
      }

      return (
        oriented.xStart <= xBin + binSize - 1 &&
        oriented.xEnd >= xBin &&
        oriented.yStart <= yBin + binSize - 1 &&
        oriented.yEnd >= yBin
      );
    });
  }

  function getRegionResults(state) {
    const region = state.region;
    const start = coordinateOrNull(region.start);
    const end = coordinateOrNull(region.end);
    const partnerStartText = String(region.partnerStart == null ? "" : region.partnerStart).trim();
    const partnerEndText = String(region.partnerEnd == null ? "" : region.partnerEnd).trim();
    const partnerRangeSpecified = !!partnerStartText || !!partnerEndText;
    const partnerStart = coordinateOrNull(region.partnerStart);
    const partnerEnd = coordinateOrNull(region.partnerEnd);

    if (!region.rna || start === null || end === null || start > end ||
        (partnerRangeSpecified && (!region.partner || partnerStart === null || partnerEnd === null || partnerStart > partnerEnd))) {
      return emptyRegionResults();
    }

    const matches = (state.records || []).filter(function (record) {
      return matchingRegionOrientations(record, region, start, end, partnerRangeSpecified, partnerStart, partnerEnd).length > 0;
    });

    const partnerMap = Object.create(null);
    matches.forEach(function (record) {
      const orientation = matchingRegionOrientations(record, region, start, end, partnerRangeSpecified, partnerStart, partnerEnd)[0];
      const partner = orientation.partner;
      const current = partnerMap[partner] || { name: partner, records: 0, support: 0 };
      current.records += 1;
      current.support += record.supportCount;
      partnerMap[partner] = current;
    });

    const profile = buildRegionProfile(matches, region.rna, start, end);
    return {
      records: matches,
      support: matches.reduce(function (total, record) { return total + record.supportCount; }, 0),
      partners: Object.keys(partnerMap).map(function (name) { return partnerMap[name]; })
        .sort(function (left, right) { return right.support - left.support || left.name.localeCompare(right.name); }),
      profile: profile.bins,
      profileBinSize: profile.binSize,
      profileBinSizeAdjusted: profile.adjusted,
      profileBinUpdates: profile.binUpdates,
      profileRangeUpdates: profile.rangeUpdates
    };
  }

  function emptyRegionResults() {
    return {
      records: [],
      support: 0,
      partners: [],
      profile: [],
      profileBinSize: 1,
      profileBinSizeAdjusted: false,
      profileBinUpdates: 0,
      profileRangeUpdates: 0
    };
  }

  function matchingRegionOrientations(record, region, start, end, partnerRangeSpecified, partnerStart, partnerEnd) {
    const orientations = [];
    if (record.rnaOne === region.rna) {
      orientations.push({
        anchorStart: record.rnaOneStart,
        anchorEnd: record.rnaOneEnd,
        partner: record.rnaTwo,
        partnerStart: record.rnaTwoStart,
        partnerEnd: record.rnaTwoEnd
      });
    }
    if (record.rnaTwo === region.rna) {
      orientations.push({
        anchorStart: record.rnaTwoStart,
        anchorEnd: record.rnaTwoEnd,
        partner: record.rnaOne,
        partnerStart: record.rnaOneStart,
        partnerEnd: record.rnaOneEnd
      });
    }
    return orientations.filter(function (orientation) {
      if (!matchesOverlapRule(orientation.anchorStart, orientation.anchorEnd, start, end, region.overlap)) {
        return false;
      }
      if (region.partner && orientation.partner !== region.partner) {
        return false;
      }
      return !partnerRangeSpecified || matchesOverlapRule(
        orientation.partnerStart,
        orientation.partnerEnd,
        partnerStart,
        partnerEnd,
        region.overlap
      );
    });
  }

  function matchesOverlapRule(recordStart, recordEnd, regionStart, regionEnd, rule) {
    if (rule === "contained") {
      return recordStart >= regionStart && recordEnd <= regionEnd;
    }
    return recordStart <= regionEnd && recordEnd >= regionStart;
  }

  function buildRegionProfile(records, rna, start, end) {
    const span = Math.max(1, end - start + 1);
    const requestedBinSize = span > 1000 ? 50 : span > 500 ? 25 : 10;
    const maximumBins = 30000;
    const binSize = Math.max(requestedBinSize, Math.ceil(span / maximumBins));
    const binCount = Math.ceil(span / binSize);
    const profile = [];
    const difference = new Float64Array(binCount + 1);
    let binUpdates = 0;
    let rangeUpdates = 0;

    for (let index = 0; index < binCount; index += 1) {
      const binStart = start + index * binSize;
      profile.push({ start: binStart, end: Math.min(end, binStart + binSize - 1), value: 0 });
    }

    records.forEach(function (record) {
      const arms = [];
      if (record.rnaOne === rna) {
        arms.push({ start: record.rnaOneStart, end: record.rnaOneEnd });
      }
      if (record.rnaTwo === rna) {
        arms.push({ start: record.rnaTwoStart, end: record.rnaTwoEnd });
      }
      arms.forEach(function (arm) {
        const armStart = Math.max(start, arm.start);
        const armEnd = Math.min(end, arm.end);
        if (armStart > armEnd) {
          return;
        }
        const firstBin = Math.max(0, Math.floor((armStart - start) / binSize));
        const lastBin = Math.min(binCount - 1, Math.floor((armEnd - start) / binSize));
        const value = Number(record.supportCount) || 0;

        // Every overlapped Region bin receives the complete arm support. Use a
        // difference array so a long arm costs one range update instead of one
        // main-thread mutation per covered bin. `binUpdates` intentionally
        // retains its previous meaning: the number of logical arm/bin
        // contributions represented by the profile.
        difference[firstBin] += value;
        difference[lastBin + 1] -= value;
        binUpdates += lastBin - firstBin + 1;
        rangeUpdates += 1;
      });
    });

    let runningValue = 0;
    for (let index = 0; index < binCount; index += 1) {
      runningValue += difference[index];
      profile[index].value = runningValue;
    }

    return {
      bins: profile,
      binSize: binSize,
      adjusted: binSize !== requestedBinSize,
      binUpdates: binUpdates,
      rangeUpdates: rangeUpdates
    };
  }

  function getViewpointResults(state) {
    const viewpoint = state.viewpoint || defaultViewpointState(state.summary, state.records);
    const extent = getRnaExtent(state.records || [], viewpoint.rna);
    const reference = findFastaEntry(state.fasta, viewpoint.rna);
    const requestedStart = coordinateOrNull(viewpoint.start);
    const requestedEnd = coordinateOrNull(viewpoint.end);
    const useReferenceRange = viewpoint.rangeMode === "reference" && reference;
    if (viewpoint.rangeMode === "reference" && !reference) {
      return emptyViewpointResults();
    }
    const invalidRequestedRange = !useReferenceRange && (
      (String(viewpoint.start == null ? "" : viewpoint.start).trim() && requestedStart === null) ||
      (String(viewpoint.end == null ? "" : viewpoint.end).trim() && requestedEnd === null)
    );
    if (invalidRequestedRange) {
      return emptyViewpointResults();
    }
    const start = useReferenceRange ? 1 : (requestedStart === null ? (extent ? extent.min : null) : requestedStart);
    const end = useReferenceRange ? reference.sequence.length : (requestedEnd === null ? (extent ? extent.max : null) : requestedEnd);

    if (!viewpoint.rna || start === null || end === null || start > end) {
      return emptyViewpointResults();
    }

    const requestedBinSize = Math.max(1, Math.floor(Number(viewpoint.binSize) || 1));
    const span = end - start + 1;
    const maximumBins = 30000;
    const binSize = Math.max(requestedBinSize, Math.ceil(span / maximumBins));
    const binCount = Math.ceil(span / binSize);
    const bins = Array.from({ length: binCount }, function (_, index) {
      const binStart = start + index * binSize;
      return {
        start: binStart,
        end: Math.min(end, binStart + binSize - 1),
        coverageTotal: 0,
        value: 0
      };
    });
    const boundaryCoverage = new Float64Array(binCount);
    const fullBinDifference = new Float64Array(binCount + 1);
    const matches = [];
    let armContributions = 0;
    let coverageBoundaryUpdates = 0;
    let coverageRangeUpdates = 0;

    (state.records || []).forEach(function (record) {
      const arms = getViewpointArms(record, viewpoint.rna, viewpoint.partner, viewpoint.chimeraType, viewpoint.homodimer);
      if (!arms.length) {
        return;
      }

      let contributed = false;
      arms.forEach(function (arm) {
        const armStart = Math.max(start, arm.start);
        const armEnd = Math.min(end, arm.end);
        if (armStart > armEnd) {
          return;
        }
        contributed = true;
        armContributions += 1;
        const value = viewpoint.measure === "support" ? record.supportCount : 1;
        const firstBin = Math.max(0, Math.floor((armStart - start) / binSize));
        const lastBin = Math.min(binCount - 1, Math.floor((armEnd - start) / binSize));

        if (firstBin === lastBin) {
          boundaryCoverage[firstBin] += (armEnd - armStart + 1) * value;
          coverageBoundaryUpdates += 1;
          return;
        }

        // Only the two boundary bins can be partially covered. Every bin
        // strictly between them is covered in full and therefore receives the
        // same per-nucleotide value; represent that run with one difference
        // update. Endpoints remain inclusive.
        boundaryCoverage[firstBin] += (bins[firstBin].end - armStart + 1) * value;
        boundaryCoverage[lastBin] += (armEnd - bins[lastBin].start + 1) * value;
        coverageBoundaryUpdates += 2;

        const firstFullBin = firstBin + 1;
        const lastFullBin = lastBin - 1;
        if (firstFullBin <= lastFullBin) {
          fullBinDifference[firstFullBin] += value;
          fullBinDifference[lastFullBin + 1] -= value;
          coverageRangeUpdates += 1;
        }
      });
      if (contributed) {
        matches.push(record);
      }
    });

    let fullBinValue = 0;
    bins.forEach(function (bin, index) {
      fullBinValue += fullBinDifference[index];
      const binWidth = Math.max(1, bin.end - bin.start + 1);
      bin.coverageTotal = boundaryCoverage[index] + fullBinValue * binWidth;
      bin.value = bin.coverageTotal / binWidth;
    });

    return {
      ready: true,
      rna: viewpoint.rna,
      start: start,
      end: end,
      requestedBinSize: requestedBinSize,
      binSize: binSize,
      binSizeAdjusted: binSize !== requestedBinSize,
      records: matches,
      armContributions: armContributions,
      coverageBoundaryUpdates: coverageBoundaryUpdates,
      coverageRangeUpdates: coverageRangeUpdates,
      bins: bins,
      max: bins.reduce(function (maximum, bin) { return Math.max(maximum, bin.value); }, 0),
      totalCoverage: bins.reduce(function (total, bin) { return total + bin.coverageTotal; }, 0)
    };
  }

  function emptyViewpointResults() {
    return {
      ready: false,
      rna: "",
      start: null,
      end: null,
      requestedBinSize: 1,
      binSize: 1,
      binSizeAdjusted: false,
      records: [],
      armContributions: 0,
      coverageBoundaryUpdates: 0,
      coverageRangeUpdates: 0,
      bins: [],
      max: 0,
      totalCoverage: 0
    };
  }

  function getViewpointArms(record, rna, partner, chimeraType, homodimer) {
    if (chimeraType && chimeraType !== "all" && record.chimeraType !== chimeraType) {
      return [];
    }
    if (!matchesHomodimerSubset(record, homodimer)) {
      return [];
    }

    const arms = [];
    if (record.rnaOne === rna && (!partner || record.rnaTwo === partner)) {
      arms.push({ start: record.rnaOneStart, end: record.rnaOneEnd });
    }
    if (record.rnaTwo === rna && (!partner || record.rnaOne === partner)) {
      arms.push({ start: record.rnaTwoStart, end: record.rnaTwoEnd });
    }
    return arms;
  }

  function viewpointToTsv(results) {
    const rows = [["start", "end", "mean_interaction_coverage"]];
    (results && results.bins ? results.bins : []).forEach(function (bin) {
      rows.push([bin.start, bin.end, bin.value]);
    });
    return rows.map(function (row) { return row.join("\t"); }).join("\n");
  }

  function buildComparisonResults(comparison) {
    const config = comparison || {};
    const datasets = (config.datasets || []).filter(function (dataset) {
      return dataset && dataset.summary && Array.isArray(dataset.records);
    });
    const groupA = datasets.filter(function (dataset) { return dataset.condition === "A"; });
    const groupB = datasets.filter(function (dataset) { return dataset.condition === "B"; });

    if (!config.rnaX || !config.rnaY || !groupA.length || !groupB.length) {
      return {
        ready: false,
        reason: !groupA.length || !groupB.length
          ? "Assign at least one local dataset to each condition."
          : "Choose RNA X and RNA Y to compare.",
        groupA: groupA,
        groupB: groupB,
        cells: [],
        conservedCells: []
      };
    }

    const template = {
      rnaX: config.rnaX,
      rnaY: config.rnaY,
      binSize: Number(config.binSize) || 10,
      measure: config.measure === "support" ? "support" : "records",
      colourCap: "95",
      scale: "linear",
      orientation: "normalised"
    };
    const byKey = Object.create(null);
    const allDatasets = groupA.concat(groupB);
    let matrixFailure = "";
    let distinctCellCount = 0;
    let datasetCellContributions = 0;

    allDatasets.forEach(function (dataset) {
      if (matrixFailure) {
        return;
      }
      const matrix = buildContactMatrix(dataset.records, template);
      if (matrix.ready === false) {
        matrixFailure = matrix.reason || "A dataset exceeded the browser contact-map limits.";
        return;
      }
      if (datasetCellContributions + matrix.cells.length > MAX_COMPARISON_DATASET_CELLS) {
        matrixFailure = "The combined maps contain more than " + MAX_COMPARISON_DATASET_CELLS.toLocaleString("en-US") + " dataset-cell contributions. Increase the bin size or compare fewer datasets.";
        return;
      }
      datasetCellContributions += matrix.cells.length;
      const librarySize = template.measure === "support"
        ? Number(dataset.summary.supportInteractions || 0)
        : Number(dataset.summary.validRecords || 0);
      const scale = config.normalise === "library" ? 1000000 / Math.max(1, librarySize) : 1;

      matrix.cells.forEach(function (cell) {
        if (matrixFailure) {
          return;
        }
        const key = cell.x + ":" + cell.y;
        let entry = byKey[key];
        if (!entry) {
          if (distinctCellCount >= MAX_COMPARISON_CELLS) {
            matrixFailure = "The combined maps cover more than " + MAX_COMPARISON_CELLS.toLocaleString("en-US") + " distinct cells. Increase the bin size or narrow the RNA pair.";
            return;
          }
          entry = {
            x: cell.x,
            y: cell.y,
            aTotal: 0,
            bTotal: 0,
            presentDatasets: 0,
            datasets: []
          };
          byKey[key] = entry;
          distinctCellCount += 1;
        }
        const scaledValue = cell.value * scale;
        if (dataset.condition === "A") {
          entry.aTotal += scaledValue;
        } else {
          entry.bTotal += scaledValue;
        }
        entry.presentDatasets += 1;
        entry.datasets.push({
          id: dataset.id,
          label: dataset.label || dataset.fileName,
          condition: dataset.condition,
          rawValue: cell.value,
          value: scaledValue
        });
      });
    });

    if (matrixFailure) {
      return {
        ready: false,
        reason: "Comparison not generated. " + matrixFailure,
        groupA: groupA,
        groupB: groupB,
        cells: [],
        conservedCells: [],
        limitExceeded: true,
        maximumCells: MAX_COMPARISON_CELLS,
        maximumDatasetCells: MAX_COMPARISON_DATASET_CELLS,
        datasetCellContributions: datasetCellContributions
      };
    }

    const pseudocount = config.normalise === "library" ? 1 : 0.5;
    const cells = Object.keys(byKey).map(function (key) {
      const entry = byKey[key];
      const conditionA = entry.aTotal / groupA.length;
      const conditionB = entry.bTotal / groupB.length;
      return {
        x: entry.x,
        y: entry.y,
        conditionA: conditionA,
        conditionB: conditionB,
        effect: Math.log2((conditionA + pseudocount) / (conditionB + pseudocount)),
        presentDatasets: entry.presentDatasets,
        datasets: entry.datasets
      };
    }).sort(function (left, right) { return left.x - right.x || left.y - right.y; });
    const absoluteEffects = cells.map(function (cell) { return Math.abs(cell.effect); }).sort(function (left, right) { return left - right; });
    const conservedCells = cells.filter(function (cell) { return cell.presentDatasets >= 2; });
    const binSize = template.binSize;

    if (!cells.length) {
      return {
        ready: false,
        reason: "No interactions were found for the selected RNA pair across the assigned datasets.",
        groupA: groupA,
        groupB: groupB,
        cells: [],
        conservedCells: []
      };
    }

    return {
      ready: true,
      limitExceeded: false,
      groupA: groupA,
      groupB: groupB,
      cells: cells,
      cellMap: Object.fromEntries(cells.map(function (cell) { return [cell.x + ":" + cell.y, cell]; })),
      conservedCells: conservedCells,
      binSize: binSize,
      xMin: cells.length ? cells.reduce(function (minimum, cell) { return Math.min(minimum, cell.x); }, Infinity) : 0,
      xMax: cells.length ? cells.reduce(function (maximum, cell) { return Math.max(maximum, cell.x); }, -Infinity) : 0,
      yMin: cells.length ? cells.reduce(function (minimum, cell) { return Math.min(minimum, cell.y); }, Infinity) : 0,
      yMax: cells.length ? cells.reduce(function (maximum, cell) { return Math.max(maximum, cell.y); }, -Infinity) : 0,
      effectCap: absoluteEffects.length ? Math.max(.25, quantile(absoluteEffects, .95)) : 1,
      conservedMax: conservedCells.reduce(function (maximum, cell) { return Math.max(maximum, cell.presentDatasets); }, 1),
      distinctCells: distinctCellCount,
      datasetCellContributions: datasetCellContributions,
      maximumCells: MAX_COMPARISON_CELLS,
      maximumDatasetCells: MAX_COMPARISON_DATASET_CELLS,
      normalise: config.normalise || "library",
      measure: template.measure
    };
  }

  function comparisonToTsv(result) {
    const rows = [["x_start", "x_end", "y_start", "y_end", "condition_a_mean", "condition_b_mean", "log2_effect", "datasets_present"]];
    if (!result || !result.cells) {
      return rows[0].join("\t");
    }
    result.cells.forEach(function (cell) {
      rows.push([
        cell.x,
        cell.x + result.binSize - 1,
        cell.y,
        cell.y + result.binSize - 1,
        cell.conditionA,
        cell.conditionB,
        cell.effect,
        cell.presentDatasets
      ]);
    });
    return rows.map(function (row) { return row.join("\t"); }).join("\n");
  }

  function comparisonSelectedToTsv(comparison, selected, binSizeValue) {
    const rows = [["x_start", "x_end", "y_start", "y_end", "dataset_id", "label", "condition", "file_name", "raw_value", "analysis_value"]];
    if (!comparison || !selected) {
      return rows[0].join("\t");
    }
    const binSize = Number(binSizeValue) > 0 ? Number(binSizeValue) : Number(comparison.binSize) || 1;
    const present = Object.create(null);
    (selected.datasets || []).forEach(function (datasetValue) {
      present[datasetValue.id] = datasetValue;
    });
    (comparison.datasets || []).forEach(function (dataset) {
      const value = present[dataset.id];
      rows.push([
        selected.x,
        selected.x + binSize - 1,
        selected.y,
        selected.y + binSize - 1,
        dataset.id,
        dataset.label || dataset.fileName || "",
        dataset.condition || "",
        dataset.fileName || "",
        value ? value.rawValue : 0,
        value ? value.value : 0
      ]);
    });
    return rows.map(function (row) { return row.map(tsvCell).join("\t"); }).join("\n");
  }

  function comparisonCountMatrixToTsv(comparison, result) {
    const datasets = comparison && comparison.datasets || [];
    const rows = [["feature_id"].concat(datasets.map(function (dataset) { return dataset.id; }))];
    if (!result || !result.ready || !Array.isArray(result.cells)) {
      return rows[0].map(tsvCell).join("\t");
    }
    result.cells.slice().sort(function (left, right) {
      return left.x - right.x || left.y - right.y;
    }).forEach(function (cell) {
      const featureId = cell.x + "_" + cell.y;
      const present = Object.create(null);
      (cell.datasets || []).forEach(function (entry) { present[entry.id] = entry.rawValue; });
      rows.push([featureId].concat(datasets.map(function (dataset) {
        const rawCount = Object.prototype.hasOwnProperty.call(present, dataset.id) ? present[dataset.id] : 0;
        return validateLegacyRCount(rawCount, featureId, dataset.id);
      })));
    });
    return rows.map(function (row) { return row.map(tsvCell).join("\t"); }).join("\n");
  }

  function comparisonLegacyNamesToTsv(comparison) {
    return (comparison && comparison.datasets || []).map(function (dataset) {
      return [
        dataset.id,
        dataset.condition === "A" ? "condition_one" : "condition_two"
      ].map(tsvCell).join("\t");
    }).join("\n");
  }

  function comparisonSampleMetadataToTsv(comparison) {
    const rows = [["sample_id", "condition", "condition_label", "file_name"]];
    (comparison && comparison.datasets || []).forEach(function (dataset) {
      rows.push([
        dataset.id,
        dataset.condition === "A" ? "condition_one" : "condition_two",
        dataset.condition === "A" ? comparison.conditionALabel : comparison.conditionBLabel,
        dataset.fileName || dataset.summary && dataset.summary.fileName || ""
      ]);
    });
    return rows.map(function (row) { return row.map(tsvCell).join("\t"); }).join("\n");
  }

  function tsvCell(value) {
    return String(value == null ? "" : value).replace(/[\t\r\n]+/g, " ");
  }

  function validateLegacyRCount(value, featureId, sampleId) {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new Error(
        "Cannot export HYB2/DESeq2 counts: feature \"" + featureId + "\", sample \"" + sampleId +
        "\" is not a non-negative safe integer. Re-parse the HYB file or choose Record count instead of Cluster support."
      );
    }
    if (value > MAX_R_INTEGER_COUNT) {
      throw new Error(
        "Cannot export HYB2/DESeq2 counts: feature \"" + featureId + "\", sample \"" + sampleId +
        "\" exceeds the legacy R integer maximum of 2,147,483,647. Choose Record count, correct unusually large count_total values, or use a validated 64-bit workflow."
      );
    }
    return value;
  }

  function parseFasta(text) {
    const sequences = [];
    let current = null;

    String(text || "").replace(/\r/g, "").split("\n").forEach(function (line) {
      const trimmed = line.trim();
      if (!trimmed) {
        return;
      }

      if (trimmed.charAt(0) === ">") {
        if (current) {
          sequences.push(current);
        }
        const header = trimmed.slice(1).trim();
        current = {
          header: header,
          id: header.split(/\s+/)[0] || "",
          sequence: ""
        };
        return;
      }

      if (current) {
        // Only whitespace is layout. Preserve every other symbol so a gap,
        // digit, or unsupported base can never silently shift coordinates.
        current.sequence += trimmed.replace(/\s+/g, "").toUpperCase();
      }
    });

    if (current) {
      sequences.push(current);
    }

    return sequences.filter(function (item) { return item.id && item.sequence; }).map(function (item) {
      item.invalidCharacters = Array.from(new Set(item.sequence.match(/[^ACGTU]/g) || []));
      return item;
    });
  }

  function buildFastaMapping(rnaNames, sequences) {
    const mapping = Object.create(null);
    const byHeader = Object.create(null);
    const byId = Object.create(null);

    (sequences || []).forEach(function (entry) {
      if (entry.header && byHeader[entry.header] === undefined) {
        byHeader[entry.header] = entry.id;
      }
      if (entry.id && byId[entry.id] === undefined) {
        byId[entry.id] = entry.id;
      }
    });

    (rnaNames || []).forEach(function (rna) {
      if (byHeader[rna] !== undefined || byId[rna] !== undefined) {
        mapping[rna] = byHeader[rna] !== undefined ? byHeader[rna] : byId[rna];
      }
    });

    return mapping;
  }

  function findFastaEntry(fasta, rna) {
    if (!fasta || !fasta.mapping || !fasta.sequences) {
      return null;
    }
    const id = fasta.mapping[rna];
    return fasta.sequences.find(function (entry) { return entry.id === id; }) || null;
  }

  function extractReference(fasta, rna, start, end) {
    const entry = findFastaEntry(fasta, rna);
    if (!entry) {
      return null;
    }
    const rawStart = String(start == null ? "" : start).trim();
    const rawEnd = String(end == null ? "" : end).trim();
    const parsedStart = coordinateOrNull(start);
    const parsedEnd = coordinateOrNull(end);
    if ((rawStart && parsedStart === null) || (rawEnd && parsedEnd === null)) {
      return null;
    }
    const left = parsedStart === null ? 1 : parsedStart;
    const right = Math.min(entry.sequence.length, parsedEnd === null ? entry.sequence.length : parsedEnd);
    if (left > right) {
      return null;
    }
    return {
      id: entry.id,
      header: entry.header,
      start: left,
      end: right,
      sequence: entry.sequence.slice(left - 1, right)
    };
  }

  function normaliseFoldSequence(input) {
    const lines = String(input || "").replace(/\r/g, "").split("\n");
    const raw = lines.filter(function (line) { return line.trim().charAt(0) !== ">"; }).join("");
    const sequence = raw.replace(/\s+/g, "").toUpperCase().replace(/T/g, "U");
    const invalid = sequence.match(/[^ACGU]/g);
    return {
      sequence: sequence,
      invalid: invalid ? Array.from(new Set(invalid)) : []
    };
  }

  function recordsToCsv(records) {
    const rows = [[
      "sequence_id",
      "rna_1",
      "rna_1_start",
      "rna_1_end",
      "rna_2",
      "rna_2_start",
      "rna_2_end",
      "support_count",
      "support_source",
      "raw_read_count",
      "overlap_score",
      "homodimer_overlap_ge_5",
      "chimera_type",
      "chimera_type_source",
      "dG",
      "line"
    ]];

    records.forEach(function (record) {
      rows.push([
        record.id,
        record.rnaOne,
        record.rnaOneStart,
        record.rnaOneEnd,
        record.rnaTwo,
        record.rnaTwoStart,
        record.rnaTwoEnd,
        record.supportCount,
        record.supportSource,
        record.rawReadCount === null ? "" : record.rawReadCount,
        record.overlapScore === null ? "" : record.overlapScore,
        record.isHomodimer ? "true" : "false",
        record.chimeraType,
        record.chimeraTypeSource,
        record.dg === null ? "" : record.dg,
        record.lineNumber
      ]);
    });

    return rows.map(function (row) {
      return row.map(csvCell).join(",");
    }).join("\n");
  }

  function recordsToHyb(records) {
    return records.map(function (record) { return record.raw; }).join("\n") + (records.length ? "\n" : "");
  }

  function rnaCountsToCsv(records) {
    const counts = new Map();

    (records || []).forEach(function (record) {
      addRnaCount(counts, record.rnaOne, record);
      addRnaCount(counts, record.rnaTwo, record);
    });

    const rows = [[
      "rna",
      "arm_occurrences",
      "source_reads",
      "source_read_count_arms",
      "cluster_support",
      "overlap_score_total",
      "overlap_score_arms"
    ]];
    Array.from(counts.values()).sort(function (left, right) {
      return right.support - left.support || left.name.localeCompare(right.name);
    }).forEach(function (item) {
      rows.push([
        item.name,
        item.records,
        item.rawReadCountRecords ? item.rawReads : "",
        item.rawReadCountRecords,
        item.support,
        item.overlapRecords ? item.overlapTotal : "",
        item.overlapRecords
      ]);
    });
    return csvRows(rows);
  }

  function rnaPairsToCsv(records) {
    const counts = new Map();

    (records || []).forEach(function (record) {
      const left = String(record.rnaOne || "");
      const right = String(record.rnaTwo || "");
      const rnaOne = left.localeCompare(right) <= 0 ? left : right;
      const rnaTwo = left.localeCompare(right) <= 0 ? right : left;
      const key = rnaOne + "\u0000" + rnaTwo;
      const item = counts.get(key) || countEntry({ rnaOne: rnaOne, rnaTwo: rnaTwo });
      addRecordCount(item, record);
      counts.set(key, item);
    });

    const rows = [[
      "rna_1",
      "rna_2",
      "hyb_records",
      "source_reads",
      "source_read_count_records",
      "cluster_support",
      "overlap_score_total",
      "overlap_score_records"
    ]];
    Array.from(counts.values()).sort(function (left, right) {
      return right.support - left.support ||
        left.rnaOne.localeCompare(right.rnaOne) ||
        left.rnaTwo.localeCompare(right.rnaTwo);
    }).forEach(function (item) {
      rows.push([
        item.rnaOne,
        item.rnaTwo,
        item.records,
        item.rawReadCountRecords ? item.rawReads : "",
        item.rawReadCountRecords,
        item.support,
        item.overlapRecords ? item.overlapTotal : "",
        item.overlapRecords
      ]);
    });
    return csvRows(rows);
  }

  function addRnaCount(counts, name, record) {
    const key = String(name || "");
    const item = counts.get(key) || countEntry({ name: key });
    addRecordCount(item, record);
    counts.set(key, item);
  }

  function countEntry(identity) {
    return Object.assign({
      records: 0,
      support: 0,
      rawReads: 0,
      rawReadCountRecords: 0,
      overlapTotal: 0,
      overlapRecords: 0
    }, identity);
  }

  function addRecordCount(item, record) {
    item.records += 1;
    item.support += Number(record.supportCount) || 0;
    if (record.rawReadCount !== null && record.rawReadCount !== undefined) {
      item.rawReads += Number(record.rawReadCount) || 0;
      item.rawReadCountRecords += 1;
    }
    if (record.overlapScore !== null && record.overlapScore !== undefined) {
      item.overlapTotal += Number(record.overlapScore) || 0;
      item.overlapRecords += 1;
    }
  }

  function csvRows(rows) {
    return rows.map(function (row) {
      return row.map(csvCell).join(",");
    }).join("\n");
  }

  function contactToTsv(matrix) {
    const rows = [["x_start", "x_end", "y_start", "y_end", "records", "support_count"]];
    matrix.cells.sort(function (left, right) { return left.x - right.x || left.y - right.y; }).forEach(function (cell) {
      rows.push([
        cell.x,
        cell.x + matrix.binSize - 1,
        cell.y,
        cell.y + matrix.binSize - 1,
        cell.records,
        cell.support
      ]);
    });
    return rows.map(function (row) { return row.join("\t"); }).join("\n");
  }

  function csvCell(value) {
    return '"' + String(value == null ? "" : value).replace(/"/g, '""') + '"';
  }

  function numberOrNull(value) {
    const string = String(value == null ? "" : value).trim();
    if (!string) {
      return null;
    }
    const parsed = Number(string);
    return Number.isFinite(parsed) ? parsed : null;
  }

  function coordinateOrNull(value) {
    const parsed = numberOrNull(value);
    return Number.isSafeInteger(parsed) && parsed >= 1 ? parsed : null;
  }

  function matchesHomodimerSubset(record, subset) {
    if (subset === "only") {
      return !!record.isHomodimer;
    }
    if (subset === "exclude") {
      return !record.isHomodimer;
    }
    return true;
  }

  function overlapsOptional(recordStart, recordEnd, filterStart, filterEnd) {
    if (filterStart === null && filterEnd === null) {
      return true;
    }
    const start = filterStart === null ? -Infinity : filterStart;
    const end = filterEnd === null ? Infinity : filterEnd;
    return recordStart <= end && recordEnd >= start;
  }

  window.Hyb2Data = {
    defaultInteractionFilters: defaultInteractionFilters,
    defaultContactState: defaultContactState,
    defaultRegionState: defaultRegionState,
    defaultViewpointState: defaultViewpointState,
    defaultComparisonState: defaultComparisonState,
    defaultStructureState: defaultStructureState,
    getRnaExtent: getRnaExtent,
    getFilteredRecords: getFilteredRecords,
    getPartnerCounts: getPartnerCounts,
    orientRecordForPair: orientRecordForPair,
    buildContactMatrix: buildContactMatrix,
    getCellRecords: getCellRecords,
    getRegionResults: getRegionResults,
    getViewpointResults: getViewpointResults,
    viewpointToTsv: viewpointToTsv,
    buildComparisonResults: buildComparisonResults,
    comparisonToTsv: comparisonToTsv,
    comparisonSelectedToTsv: comparisonSelectedToTsv,
    comparisonCountMatrixToTsv: comparisonCountMatrixToTsv,
    comparisonLegacyNamesToTsv: comparisonLegacyNamesToTsv,
    comparisonSampleMetadataToTsv: comparisonSampleMetadataToTsv,
    parseFasta: parseFasta,
    buildFastaMapping: buildFastaMapping,
    findFastaEntry: findFastaEntry,
    extractReference: extractReference,
    normaliseFoldSequence: normaliseFoldSequence,
    recordsToCsv: recordsToCsv,
    recordsToHyb: recordsToHyb,
    rnaCountsToCsv: rnaCountsToCsv,
    rnaPairsToCsv: rnaPairsToCsv,
    contactToTsv: contactToTsv,
    numberOrNull: numberOrNull
  };
}());
