/*
 * DesktopProvider: the web DataProvider contract (web/data-provider.js) over
 * the desktop analysis backend (Tauri `analysis_query` -> Python
 * hyb2analysis indexes). Selection = { datasetIds: [sample ids] }; several
 * ids are merged replicates (the backend sums counts across indexes).
 *
 * Backend -> page field names:
 *   hybrid rows  gene1/start1/end1/gene2/start2/end2/reads/type/overlap
 *                -> rnaOne/rnaOneStart/rnaOneEnd/rnaTwo/.../supportCount/chimeraType/overlapScore
 *   summary      merged.records/reads/with_dg/genes/pairs/types
 *                -> validRecords/supportInteractions/withDg/rnaCounts+rnaNames/pairCounts/chimeraTypes
 *
 * Row `index` is stable within a selection: position of the sample in
 * datasetIds * 1e9 + the hybrid id in that sample's index. queryHybrids
 * ({ indices }) answers from rows this provider has already returned.
 *
 * Queries the backend cannot express exactly (free-text search, homodimer
 * subset, either-orientation regions) fetch at most MAX_ROWS rows per
 * backend query and finish the filtering here; results then carry
 * `truncated: true` when the backend had more rows.
 */
(function (root) {
  "use strict";

  var MAX_ROWS = 10000;
  var INDEX_STRIDE = 1e9;
  var CACHE_SIZE = 32;

  function api() { return root.Hyb2Provider; }
  function data() { return root.Hyb2Data; }

  function basename(path) {
    var parts = String(path || "").split(/[\\/]+/);
    return parts[parts.length - 1] || "";
  }
  function numberOrNull(value) {
    var text = String(value == null ? "" : value).trim();
    if (!text) return null;
    var number = Number(text);
    return Number.isFinite(number) ? number : null;
  }
  function sortKeys(value) {
    if (Array.isArray(value)) return value.map(sortKeys);
    if (value && typeof value === "object") {
      var out = {};
      Object.keys(value).sort().forEach(function (key) { out[key] = sortKeys(value[key]); });
      return out;
    }
    return value;
  }

  /*
   * options.query(operation, payload) -> Promise<result>   analysis_query result.result
   * options.readSequence(referenceId, name, start, end) -> Promise<{sequence, length, start, end} | null>
   * setProject({ samples, references }) after the project model changes.
   */
  function DesktopProvider(options) {
    var settings = options || {};
    this.query = settings.query;
    this.readSequence = settings.readSequence || null;
    this.samples = [];
    this.references = [];
    this.groups = null;
    this.listeners = new Set();
    this.cache = new Map();
    this.rows = new Map();
    this.sampleStats = Object.create(null);
    this.version = 0;
  }

  DesktopProvider.prototype.kind = "desktop";

  DesktopProvider.prototype.setProject = function (project) {
    var info = project || {};
    this.samples = (info.samples || []).slice();
    this.references = (info.references || []).slice();
    // Only analysis-relevant changes (ready samples, their indexes, names,
    // references) invalidate cached queries.
    var signature = JSON.stringify([
      this.samples.filter(function (sample) { return sample.ready; }).map(function (sample) {
        return [sample.id, sample.name, (sample.outputs || {}).index, ((sample.stages || {}).index || {}).updated_at, sample.reference_id];
      }),
      this.references.map(function (reference) { return [reference.id, reference.quick_hash, (reference.sequences || []).length]; })
    ]);
    if (signature === this.signature) return;
    this.signature = signature;
    this.changed();
  };

  // Web Compare fallback: { A: [ids], B: [ids] } marks dataset conditions and
  // limits listDatasets to the grouped samples (plus the current selection).
  DesktopProvider.prototype.setCompareGroups = function (groups) {
    this.groups = groups && groups.A && groups.B ? { A: groups.A.slice(), B: groups.B.slice() } : null;
    this.changed({ metadataOnly: true });
  };

  DesktopProvider.prototype.subscribe = function (listener) {
    var listeners = this.listeners;
    listeners.add(listener);
    return function () { listeners.delete(listener); };
  };

  DesktopProvider.prototype.changed = function (options) {
    var metadataOnly = !!(options && options.metadataOnly);
    this.version += 1;
    if (!metadataOnly) {
      this.cache.clear();
      this.rows.clear();
    }
    var detail = { version: this.version, metadataOnly: metadataOnly };
    Array.from(this.listeners).forEach(function (listener) {
      try { listener(detail); } catch (error) { /* keep notifying */ }
    });
  };

  DesktopProvider.prototype.ids = function (selection) {
    var ids = api().normaliseSelection(selection).datasetIds;
    if (!ids.length) throw new Error("Select at least one analysed sample.");
    return ids;
  };

  DesktopProvider.prototype.call = function (operation, payload) {
    var key = operation + "\u0001" + JSON.stringify(sortKeys(payload));
    var cache = this.cache;
    if (cache.has(key)) {
      var hit = cache.get(key);
      cache.delete(key);
      cache.set(key, hit);
      return hit;
    }
    var promise = Promise.resolve(this.query(operation, payload));
    cache.set(key, promise);
    promise.catch(function () { cache.delete(key); });
    while (cache.size > CACHE_SIZE) cache.delete(cache.keys().next().value);
    return promise;
  };

  DesktopProvider.prototype.sample = function (id) {
    return this.samples.find(function (sample) { return sample.id === id; }) || null;
  };

  DesktopProvider.prototype.reference = function (id) {
    return this.references.find(function (reference) { return reference.id === id; }) || null;
  };

  DesktopProvider.prototype.referenceInfo = function (sample) {
    var reference = sample ? this.reference(sample.reference_id) : null;
    if (!reference) return null;
    var mapping = {};
    var mapped = {};
    (reference.sequences || []).forEach(function (entry) {
      mapping[entry.name] = entry.name;
      mapped[entry.name] = { referenceId: entry.name, length: entry.length };
    });
    return {
      fileName: reference.label || basename(reference.path),
      fileSize: reference.size_bytes == null ? null : reference.size_bytes,
      sha256: reference.sha256 || null,
      sha256Unavailable: !reference.sha256,
      sequenceCount: (reference.sequences || []).length,
      referenceId: reference.id,
      mapping: mapping,
      mapped: mapped
    };
  };

  // ---- Mapping ---------------------------------------------------------

  function mapRow(row, ids) {
    var index = (Number(row.index) || 0) * INDEX_STRIDE + Number(row.id);
    var overlap = row.overlap == null || row.overlap === "" ? null : Number(row.overlap);
    return {
      index: index,
      id: row.name || String(row.id),
      sequence: row.sequence || "",
      rnaOne: row.gene1,
      rnaOneStart: row.start1,
      rnaOneEnd: row.end1,
      rnaOneReadStart: null,
      rnaOneReadEnd: null,
      rnaOneEvalue: null,
      rnaTwo: row.gene2,
      rnaTwoStart: row.start2,
      rnaTwoEnd: row.end2,
      rnaTwoReadStart: null,
      rnaTwoReadEnd: null,
      rnaTwoEvalue: null,
      dg: row.dg == null ? null : Number(row.dg),
      supportCount: Number(row.reads) || 1,
      rawReadCount: row.reads == null ? null : Number(row.reads),
      overlapScore: overlap,
      isHomodimer: row.gene1 === row.gene2 && overlap !== null && overlap >= 5,
      chimeraType: row.type || "",
      chimeraTypeSource: row.type ? "column17" : "none",
      lineNumber: null,
      raw: row.name ? [row.name, row.sequence || "", row.dg == null ? "." : row.dg, row.gene1, "", "", row.start1, row.end1, "", row.gene2, "", "", row.start2, row.end2, ""].join("\t") : "",
      datasetId: ids[Number(row.index) || 0] || null,
      sourceIndex: row.id
    };
  }

  function mapSummary(result, ids, samples) {
    var merged = result.merged || {};
    var genes = merged.genes || [];
    var extents = {};
    var observed = merged.extents || result.extents || {};
    genes.forEach(function (gene) {
      var extent = observed[gene.name];
      if (extent && extent.min != null) extents[gene.name] = { min: extent.min, max: extent.max };
      else if (gene.min_start != null) extents[gene.name] = { min: gene.min_start, max: gene.max_end };
      else extents[gene.name] = { min: 1, max: gene.length || gene.max_end || 1 };
    });
    var types = (merged.types || []).map(function (type) {
      return { name: type.name, records: type.records, support: type.reads };
    });
    var summary = {
      fileName: ids.map(function (id) {
        var sample = samples[id];
        return sample ? sample.name : id;
      }).join(" + "),
      fileSize: null,
      datasetIds: ids.slice(),
      validRecords: merged.records || 0,
      invalidRecords: merged.invalid || 0,
      totalLines: (merged.records || 0) + (merged.invalid || 0) + (merged.skipped || 0),
      skippedBlankLines: 0,
      skippedCommentLines: merged.skipped || 0,
      supportInteractions: merged.reads || 0,
      rawReadInteractions: merged.reads || 0,
      rawReadCountRecords: merged.records_with_reads || 0,
      supportMetadataRecords: 0,
      withDg: merged.with_dg || 0,
      overlapScoreRecords: 0,
      canonicalRecords: merged.records || 0,
      legacyClusterRecords: 0,
      ambiguousColumn16Records: 0,
      intraRecords: merged.intra_records || 0,
      intraSupport: 0,
      interRecords: merged.inter_records || 0,
      interSupport: 0,
      homodimerRecords: 0,
      homodimerRawReads: 0,
      uniqueRNAs: genes.length,
      uniquePairs: (merged.pairs || []).length,
      rnaNames: genes.map(function (gene) { return gene.name; }).sort(function (a, b) { return a.localeCompare(b); }),
      rnaCounts: genes.slice(0, 10).map(function (gene) {
        return { name: gene.name, records: gene.records, support: gene.reads, rawReads: gene.reads, rawReadCountRecords: gene.records, overlapTotal: 0, overlapRecords: 0 };
      }),
      pairCounts: (merged.pairs || []).slice(0, 10).map(function (pair) {
        return { rnaOne: pair.gene1, rnaTwo: pair.gene2, records: pair.records, support: pair.reads, rawReads: pair.reads, rawReadCountRecords: pair.records, overlapTotal: 0, overlapRecords: 0 };
      }),
      chimeraTypes: types,
      errors: [],
      errorSampleTruncated: false,
      errorSampleCount: 0,
      omittedErrorCount: 0,
      backend: "desktop-index",
      perSample: (result.indexes || []).map(function (part, position) {
        return { id: ids[position], records: part.records, reads: part.reads, withDg: part.with_dg, intra: part.intra_records, inter: part.inter_records };
      })
    };
    (result.indexes || []).forEach(function (part) {
      (part.errors || []).slice(0, 100).forEach(function (error) {
        if (summary.errors.length < 100) summary.errors.push(typeof error === "object" ? error : { line: null, error: String(error), content: "" });
      });
    });
    summary.errorSampleCount = summary.errors.length;
    summary.rnaExtents = extents;
    summary.allPairs = (merged.pairs || []).map(function (pair) {
      return { rnaOne: pair.gene1, rnaTwo: pair.gene2, records: pair.records, support: pair.reads };
    });
    return summary;
  }

  // ---- Contract --------------------------------------------------------

  DesktopProvider.prototype.listDatasets = async function () {
    var self = this;
    var groups = this.groups;
    var grouped = groups ? groups.A.concat(groups.B) : null;
    return this.samples.filter(function (sample) {
      return sample.ready && (!grouped || grouped.indexOf(sample.id) >= 0);
    }).map(function (sample, position) {
      var stats = self.sampleStats[sample.id] || {};
      var reference = self.referenceInfo(sample);
      var reads = sample.reads || {};
      return {
        id: sample.id,
        label: sample.name || sample.id,
        fileName: basename(reads.path) || sample.name,
        fileSize: reads.size_bytes == null ? null : reads.size_bytes,
        sha256: reads.sha256 || null,
        sha256Unavailable: !reads.sha256,
        condition: groups ? (groups.A.indexOf(sample.id) >= 0 ? "A" : "B") : null,
        primary: position === 0,
        validRecords: stats.records || 0,
        supportInteractions: stats.reads || 0,
        rnaNames: reference ? Object.keys(reference.mapping) : (sample.gene_id ? [sample.gene_id] : []),
        reference: reference
      };
    });
  };

  DesktopProvider.prototype.getSummary = async function (selection) {
    var ids = this.ids(selection);
    var self = this;
    var result = await this.call("analysis_summary", { sample_ids: ids });
    (result.indexes || []).forEach(function (part, position) {
      self.sampleStats[ids[position]] = { records: part.records, reads: part.reads };
    });
    var byId = {};
    this.samples.forEach(function (sample) { byId[sample.id] = sample; });
    return mapSummary(result, ids, byId);
  };

  DesktopProvider.prototype.getContactBins = async function (selection, query) {
    var ids = this.ids(selection);
    var options = query || {};
    var rnaX = options.rnaX || options.gene;
    var rnaY = options.rnaY || options.gene2 || rnaX;
    var measure = options.measure === "support" || options.measure === "reads" ? "reads" : "records";
    var types = Array.isArray(options.types) ? options.types : (options.chimeraType && options.chimeraType !== "all" ? [options.chimeraType] : null);
    var requested = Number(options.binSize) > 0 ? Math.floor(Number(options.binSize)) : 10;
    var payload = { sample_ids: ids, gene: rnaX, gene2: rnaY, bin_size: requested, measure: measure };
    if (types) payload.types = types;
    if (Number(options.maxCells) > 0) payload.max_cells = Math.floor(Number(options.maxCells));
    if (options.region) payload.region = options.region;
    // Page "original" = arm 1 on X only; "normalised" = both arm orders.
    if (options.orientation) payload.orientation = options.orientation === "original" ? "oriented" : "both";
    var result = await this.call("analysis_contact_bins", payload);
    var binSize = result.bin_size || requested;
    var xs = result.x || [];
    var ys = result.y || [];
    var extent = { xMin: 0, xMax: 0, yMin: 0, yMax: 0 };
    if (xs.length) {
      // Loops, not Math.min.apply: maps can hold hundreds of thousands of cells.
      var xMin = Infinity, xMax = -Infinity, yMin = Infinity, yMax = -Infinity;
      for (var cell = 0; cell < xs.length; cell += 1) {
        if (xs[cell] < xMin) xMin = xs[cell];
        if (xs[cell] > xMax) xMax = xs[cell];
        if (ys[cell] < yMin) yMin = ys[cell];
        if (ys[cell] > yMax) yMax = ys[cell];
      }
      extent.xMin = Math.max(1, xMin);
      extent.xMax = xMax + binSize - 1;
      extent.yMin = Math.max(1, yMin);
      extent.yMax = yMax + binSize - 1;
    }
    var counts = result.count || [];
    return {
      ready: true,
      reason: "",
      limitExceeded: false,
      gene: rnaX,
      gene2: rnaY,
      binSize: binSize,
      requestedBinSize: requested,
      binSizeAdjusted: binSize !== requested,
      length: result.length,
      length2: result.length2,
      lengthX: result.length,
      lengthY: result.length2,
      region: options.region || null,
      measure: measure === "reads" ? "support" : "records",
      total: result.total || 0,
      max: result.max || 0,
      x: xs,
      y: ys,
      count: counts,
      records: counts,
      support: counts,
      recordsUsed: result.total || 0,
      totalSupport: result.total || 0,
      extent: extent,
      orientation: result.orientation || options.orientation || null
    };
  };

  function serverOrder(options) {
    var order = String(options.order || options.sort || "file");
    if (order === "support" || order === "reads") return "reads";
    return ["position", "dg"].indexOf(order) >= 0 ? order : "file";
  }

  // InteractionFilters -> backend filter sets (OR-ed) + whether the page
  // filter still has to run here.
  function translateFilters(filters) {
    var f = filters || {};
    var base = {};
    var client = false;
    if (f.type === "intra") base.intra = true;
    else if (f.type === "inter") base.intra = false;
    else if (f.type === "homodimer") { base.intra = true; client = true; }
    if (f.chimeraType && f.chimeraType !== "all") base.types = [f.chimeraType];
    var minSupport = numberOrNull(f.minSupport);
    if (minSupport !== null && minSupport > 1) base.min_reads = minSupport;
    var minDg = numberOrNull(f.minDg);
    var maxDg = numberOrNull(f.maxDg);
    if (minDg !== null) base.min_dg = minDg;
    if (maxDg !== null) base.max_dg = maxDg;
    var region = {};
    var a1 = numberOrNull(f.armOneStart);
    var a2 = numberOrNull(f.armOneEnd);
    var b1 = numberOrNull(f.armTwoStart);
    var b2 = numberOrNull(f.armTwoEnd);
    if (a1 !== null || a2 !== null) region.x = [a1 === null ? 0 : a1, a2 === null ? 1e12 : a2];
    if (b1 !== null || b2 !== null) region.y = [b1 === null ? 0 : b1, b2 === null ? 1e12 : b2];
    if (region.x || region.y) base.region = region;
    if (String(f.search || "").trim()) client = true;
    var sets;
    if (f.rna && f.partner) {
      if (f.rna === f.partner) {
        sets = [Object.assign({ gene1: f.rna, gene2: f.rna }, base)];
      } else {
        sets = [Object.assign({ gene1: f.rna, gene2: f.partner }, base), Object.assign({ gene1: f.partner, gene2: f.rna }, base)];
      }
    } else if (f.rna || f.partner) {
      sets = [Object.assign({ gene: f.rna || f.partner }, base)];
    } else {
      sets = [base];
    }
    return { sets: sets, client: client || sets.length > 1 };
  }

  function regionSets(region) {
    var start = numberOrNull(region.start);
    var end = numberOrNull(region.end);
    if (!region.rna || start === null || end === null) return [];
    var ps = numberOrNull(region.partnerStart);
    var pe = numberOrNull(region.partnerEnd);
    var partnerSpan = ps !== null && pe !== null ? [ps, pe] : null;
    var forward = { gene1: region.rna, region: { x: [start, end] } };
    var reverse = { gene2: region.rna, region: { y: [start, end] } };
    if (region.partner) {
      forward.gene2 = region.partner;
      reverse.gene1 = region.partner;
      if (partnerSpan) {
        forward.region.y = partnerSpan;
        reverse.region.x = partnerSpan;
      }
    }
    return [forward, reverse];
  }

  function cellSets(cell) {
    var size = Number(cell.binSize) || 10;
    var x = [Number(cell.x), Number(cell.x) + size - 1];
    var y = [Number(cell.y), Number(cell.y) + size - 1];
    var types = cell.chimeraType && cell.chimeraType !== "all" ? [cell.chimeraType] : null;
    var sets = [{ gene1: cell.rnaX, gene2: cell.rnaY, region: { x: x, y: y } }];
    if (cell.orientation !== "original") {
      sets.push({ gene1: cell.rnaY, gene2: cell.rnaX, region: { x: y, y: x } });
    }
    return sets.map(function (set) { if (types) set.types = types; return set; });
  }

  function andFilters(left, right) {
    var out = Object.assign({}, left, right);
    if (left.region && right.region) out.region = Object.assign({}, left.region, right.region);
    return out;
  }

  DesktopProvider.prototype.fetchRows = async function (ids, filters, options) {
    var payload = { sample_ids: ids, filters: filters, limit: options.limit, offset: options.offset || 0, order: options.order || "file" };
    if (options.withSequence === false) payload.with_sequence = false;
    var result = await this.call("analysis_hybrids", payload);
    return result;
  };

  DesktopProvider.prototype.remember = function (selection, rows) {
    var key = api().selectionKey(selection);
    var store = this.rows.get(key);
    if (!store) {
      store = new Map();
      this.rows.set(key, store);
    }
    rows.forEach(function (row) { store.set(row.index, row); });
    if (store.size > 50000) {
      var drop = store.size - 40000;
      var iterator = store.keys();
      while (drop-- > 0) store.delete(iterator.next().value);
    }
  };

  // Union of several backend filter sets, finished here.
  DesktopProvider.prototype.unionRows = async function (ids, sets, order, withSequence) {
    var self = this;
    var results = await Promise.all(sets.map(function (set) {
      return self.fetchRows(ids, set, { limit: MAX_ROWS, offset: 0, order: order, withSequence: withSequence });
    }));
    var seen = new Set();
    var rows = [];
    var truncated = false;
    results.forEach(function (result) {
      if (result.total > (result.rows || []).length) truncated = true;
      (result.rows || []).forEach(function (row) {
        var mapped = mapRow(row, ids);
        if (seen.has(mapped.index)) return;
        seen.add(mapped.index);
        rows.push(mapped);
      });
    });
    return { rows: rows, truncated: truncated };
  };

  function sortRows(rows, order) {
    if (order === "reads") {
      rows.sort(function (a, b) { return b.supportCount - a.supportCount || a.index - b.index; });
    } else if (order === "position") {
      rows.sort(function (a, b) {
        return String(a.rnaOne).localeCompare(String(b.rnaOne)) || a.rnaOneStart - b.rnaOneStart || a.index - b.index;
      });
    } else if (order === "dg") {
      rows.sort(function (a, b) {
        var left = a.dg == null ? Infinity : a.dg;
        var right = b.dg == null ? Infinity : b.dg;
        return left - right || a.index - b.index;
      });
    } else {
      rows.sort(function (a, b) { return a.index - b.index; });
    }
    return rows;
  }

  DesktopProvider.prototype.queryHybrids = async function (selection, query) {
    var ids = this.ids(selection);
    var options = query || {};
    var order = serverOrder(options);
    var offset = Math.max(0, Math.floor(Number(options.offset) || 0));
    var limit = options.limit === null ? MAX_ROWS : (options.limit === undefined ? 100 : Math.max(0, Math.min(MAX_ROWS, Math.floor(Number(options.limit)) || 0)));
    if (Array.isArray(options.indices)) {
      var store = this.rows.get(api().selectionKey(selection)) || new Map();
      var found = options.indices.map(function (index) { return store.get(Number(index)); }).filter(Boolean);
      return { total: found.length, offset: 0, limit: found.length, order: order, rows: found };
    }
    var translated = translateFilters(options.filters);
    var sets = translated.sets;
    var client = translated.client;
    if (options.region) {
      var regionParts = regionSets(options.region);
      if (!regionParts.length) return { total: 0, offset: offset, limit: limit, order: order, rows: [] };
      sets = [].concat.apply([], sets.map(function (set) { return regionParts.map(function (part) { return andFilters(set, part); }); }));
      client = true;
    }
    if (options.cell) {
      var cellParts = cellSets(options.cell);
      sets = [].concat.apply([], sets.map(function (set) { return cellParts.map(function (part) { return andFilters(set, part); }); }));
      client = client || cellParts.length > 1;
    }
    if (!client) {
      var page = await this.fetchRows(ids, sets[0], { limit: limit, offset: offset, order: order });
      var mapped = (page.rows || []).map(function (row) { return mapRow(row, ids); });
      this.remember(selection, mapped);
      return { total: page.total, offset: offset, limit: limit, order: order, rows: mapped };
    }
    var union = await this.unionRows(ids, sets, order, true);
    var rows = union.rows;
    var hyb2Data = data();
    if (options.filters && hyb2Data) rows = hyb2Data.filterRecords(rows, options.filters);
    if (options.region && hyb2Data) rows = hyb2Data.filterRegionRecords(rows, options.region);
    sortRows(rows, order);
    this.remember(selection, rows);
    return {
      total: rows.length,
      offset: offset,
      limit: limit,
      order: order,
      truncated: union.truncated,
      rows: rows.slice(offset, offset + limit)
    };
  };

  DesktopProvider.prototype.getTopInteractions = async function (selection, query) {
    var options = query || {};
    var by = options.by || "partner";
    var limit = Number(options.limit) > 0 ? Math.floor(Number(options.limit)) : null;
    var rows;
    if (by === "bin") {
      var ids = this.ids(selection);
      var rnaX = options.rnaX || options.gene;
      var payload = {
        sample_ids: ids, gene: rnaX, gene2: options.rnaY || options.gene2 || rnaX,
        bin_size: Number(options.binSize) > 0 ? Math.floor(Number(options.binSize)) : 10,
        n: Math.min(MAX_ROWS, limit || 50),
        measure: options.measure === "support" || options.measure === "reads" ? "reads" : "records"
      };
      if (options.chimeraType && options.chimeraType !== "all") payload.types = [options.chimeraType];
      if (options.region) payload.region = options.region;
      if (options.orientation) payload.orientation = options.orientation === "original" ? "oriented" : "both";
      var result = await this.call("analysis_top", payload);
      rows = (result.rows || []).map(function (row) {
        return { x: row.x, y: row.y, xEnd: row.x_end, yEnd: row.y_end, count: row.count, records: row.count, support: row.count, perSample: row.per_index };
      });
    } else {
      // Pair and partner totals come from the summary (filters other than
      // the RNA are not applied by the index aggregate).
      var summary = await this.getSummary(selection);
      var countMode = options.countMode === "records" ? "records" : "support";
      var pairs = summary.allPairs || [];
      if (by === "pair") {
        rows = pairs.slice();
      } else {
        var rna = options.rna || "";
        var partners = {};
        pairs.forEach(function (pair) {
          var partner = pair.rnaOne === rna ? pair.rnaTwo : (pair.rnaTwo === rna ? pair.rnaOne : null);
          if (partner === null) return;
          var entry = partners[partner] || (partners[partner] = { name: partner, records: 0, support: 0 });
          entry.records += pair.records;
          entry.support += pair.support;
        });
        rows = Object.keys(partners).map(function (key) { return partners[key]; });
      }
      rows.sort(function (a, b) { return b[countMode] - a[countMode]; });
    }
    return { total: rows.length, rows: limit ? rows.slice(0, limit) : rows };
  };

  DesktopProvider.prototype.getSequence = async function (selection, query) {
    var ids = this.ids(selection);
    var options = query || {};
    if (!options.rna || !this.readSequence) return null;
    var sample = this.sample(ids[0]);
    var info = this.referenceInfo(sample);
    if (!info || !info.mapped[options.rna]) return null;
    var length = info.mapped[options.rna].length;
    var start = Math.max(1, Math.floor(Number(options.start) || 1));
    var end = Math.min(length, Math.floor(Number(options.end) || length));
    if (start > end) return null;
    var key = "sequence\u0001" + info.referenceId + "\u0001" + options.rna;
    var cache = this.cache;
    if (!cache.has(key)) {
      var loading = Promise.resolve(this.readSequence(info.referenceId, options.rna));
      cache.set(key, loading);
      loading.catch(function () { cache.delete(key); });
    }
    var full = await cache.get(key);
    if (!full || typeof full.sequence !== "string") return null;
    var sequence = full.sequence.slice(start - 1, end);
    return {
      rna: options.rna,
      referenceId: options.rna,
      header: options.rna,
      start: start,
      end: end,
      length: full.sequence.length,
      sequence: sequence,
      invalidCharacters: Object.keys(sequence.toUpperCase().replace(/[ACGUT]/g, "").split("").reduce(function (seen, c) { seen[c] = true; return seen; }, {}))
    };
  };

  // Arms of rows touching a region (Viewpoint, region evidence). Without
  // `rna` this returns the MAX_ROWS strongest rows only; whole-genome
  // HYB-guided folding runs as a desktop structure task instead.
  DesktopProvider.prototype.getArms = async function (selection, query) {
    var ids = this.ids(selection);
    var options = query || {};
    var types = options.chimeraType && options.chimeraType !== "all" ? [options.chimeraType] : null;
    var sets;
    if (options.rna) {
      var start = numberOrNull(options.start);
      var end = numberOrNull(options.end);
      var span = [start === null ? 0 : start, end === null ? 1e12 : end];
      var forward = { gene1: options.rna, region: { x: span } };
      var reverse = { gene2: options.rna, region: { y: span } };
      if (options.partner) {
        forward.gene2 = options.partner;
        reverse.gene1 = options.partner;
      }
      sets = [forward, reverse];
    } else {
      sets = [{}];
    }
    if (types) sets.forEach(function (set) { set.types = types; });
    var union = await this.unionRows(ids, sets, "reads", false);
    var hyb2Data = data();
    var rows = union.rows.filter(function (row) {
      return !hyb2Data || hyb2Data.matchesHomodimerSubset(row, options.homodimer);
    });
    sortRows(rows, "file");
    var arms = api().recordsToArms(rows);
    arms.truncated = union.truncated;
    return arms;
  };

  root.Hyb2DesktopProvider = { DesktopProvider: DesktopProvider, MAX_ROWS: MAX_ROWS, mapRow: mapRow, mapSummary: mapSummary };
}(typeof window !== "undefined" ? window : globalThis));
