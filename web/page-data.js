/*
 * Page data layer: the only place where analysis pages meet a DataProvider.
 *
 * Page renderers stay synchronous and read prepared view data from `state`.
 * ensure(pageId) compares each page task's query key with what is already
 * loaded and asynchronously fetches what is missing through the provider:
 *
 *   state.summary / state.datasets / state.reference   listDatasets + getSummary
 *   state.view.interactions {total, offset, rows, partners}   queryHybrids + getTopInteractions
 *   state.view.selectedRecord                          queryHybrids({ indices })
 *   state.contact.matrix                               getContactBins
 *   state.viewpoint.results                            getArms (+ reference length)
 *   state.region.results, state.view.regionReference   queryHybrids({ region }) + getSequence
 *   state.comparison.datasets / .result                listDatasets + getContactBins per dataset
 *   state.view.structureEvidence / structureRecords    getSequence + getArms / queryHybrids
 *   state.view.report                                  queryHybrids(top rows) + getTopInteractions
 *
 * Handlers keep invalidating results the way they always did (for example
 * `state.contact.matrix = null`); a missing slot or a changed key triggers a
 * fetch on the next render.
 */
(function (root) {
  "use strict";

  const INTERACTION_WINDOW = 400;
  const INTERACTION_MARGIN = 100;
  const STRUCTURE_RECORD_CHOICES = 500;
  const REPORT_TABLE_ROWS = 12;
  const CONTACT_MAX_CELLS = 250000;
  const PAGE_IDS = ["overview", "interactions", "contact-map", "viewpoint", "region", "compare", "structure", "report"];

  function providerApi() {
    return root.Hyb2Provider;
  }

  function data() {
    return root.Hyb2Data;
  }

  function stableKey(value) {
    return JSON.stringify(value === undefined ? null : value);
  }

  function pick(source, keys) {
    const result = {};
    keys.forEach(function (key) {
      result[key] = source && source[key] !== undefined ? source[key] : null;
    });
    return result;
  }

  function errorMessage(error, fallback) {
    return error && error.message ? error.message : fallback;
  }

  function freshView() {
    return {
      keys: Object.create(null),
      errors: Object.create(null),
      loading: Object.create(null),
      interactions: null,
      selectedRecord: null,
      regionReference: undefined,
      structureEvidence: null,
      structureRecords: null,
      structureSelectedRecord: null,
      report: null,
      contactBins: null
    };
  }

  // Default page state for a freshly selected dataset (embedded mode). The
  // website shell performs the equivalent initialisation after parsing.
  function initialisePageState(state, summary) {
    const hyb2Data = data();
    state.filters = hyb2Data.defaultInteractionFilters();
    state.interactionScrollTop = 0;
    state.selectedRecordIndex = null;
    state.contact = hyb2Data.defaultContactState(summary);
    state.region = hyb2Data.defaultRegionState(summary, null);
    state.viewpoint = hyb2Data.defaultViewpointState(summary, null);
    state.comparison = hyb2Data.defaultComparisonState(null, null);
    const pair = summary && summary.pairCounts && summary.pairCounts[0];
    state.comparison.rnaX = pair ? pair.rnaOne : "";
    state.comparison.rnaY = pair ? pair.rnaTwo : "";
    state.structure = hyb2Data.defaultStructureState();
    state.structure.rna = state.region.rna;
    state.structure.start = state.region.start;
    state.structure.end = state.region.end;
    const secondRna = pair ? pair.rnaTwo : state.region.rna;
    const secondExtent = hyb2Data.getRnaExtent(null, secondRna, summary);
    state.structure.secondRna = secondRna;
    state.structure.secondStart = secondExtent ? secondExtent.min : state.region.start;
    state.structure.secondEnd = secondExtent ? Math.min(secondExtent.max, secondExtent.min + 200) : state.region.end;
  }

  function referenceFor(datasets, selection) {
    const ids = selection && selection.datasetIds ? selection.datasetIds : [];
    for (let index = 0; index < ids.length; index += 1) {
      const dataset = (datasets || []).find(function (item) { return item.id === ids[index]; });
      if (dataset && dataset.reference) {
        return dataset.reference;
      }
    }
    return null;
  }

  // Mapped reference length for an RNA in the current selection, or null.
  function referenceLength(state, rna) {
    const reference = state.reference;
    const mapped = reference && reference.mapped && reference.mapped[rna];
    return mapped && Number.isSafeInteger(mapped.length) ? mapped.length : null;
  }

  function hasReference(state, rna) {
    return referenceLength(state, rna) !== null;
  }

  function syncComparisonDatasets(state) {
    const comparison = state.comparison;
    if (!comparison) {
      return;
    }
    const excluded = comparison.excludedDatasetIds || {};
    const previous = Object.create(null);
    (comparison.datasets || []).forEach(function (dataset) { previous[dataset.id] = dataset; });
    const next = (state.datasets || []).filter(function (dataset) {
      return !excluded[dataset.id];
    }).map(function (dataset) {
      const prior = previous[dataset.id];
      return {
        id: dataset.id,
        fileName: dataset.fileName,
        fileSize: dataset.fileSize,
        sha256: dataset.sha256 || null,
        sha256Unavailable: !!dataset.sha256Unavailable,
        summary: {
          fileName: dataset.fileName,
          fileSize: dataset.fileSize,
          validRecords: dataset.validRecords,
          supportInteractions: dataset.supportInteractions,
          rnaNames: dataset.rnaNames || []
        },
        condition: prior ? prior.condition : (dataset.condition || (dataset.primary ? "A" : "B")),
        label: prior ? prior.label : (dataset.primary ? "Primary" : dataset.label)
      };
    });
    const changed = stableKey(next.map(function (dataset) { return [dataset.id, dataset.condition, dataset.label, dataset.summary.validRecords]; })) !==
      stableKey((comparison.datasets || []).map(function (dataset) { return [dataset.id, dataset.condition, dataset.label, dataset.summary && dataset.summary.validRecords]; }));
    comparison.datasets = next;
    if (changed) {
      comparison.result = null;
      comparison.selection = null;
    }
  }

  function windowRows(interactions, start, count) {
    if (!interactions || !Array.isArray(interactions.rows)) {
      return null;
    }
    const end = Math.min(interactions.total, start + count);
    if (start < interactions.offset || end > interactions.offset + interactions.rows.length) {
      return null;
    }
    return interactions.rows.slice(start - interactions.offset, end - interactions.offset);
  }

  function createController(options) {
    const settings = options || {};
    const state = settings.state;
    const onUpdate = typeof settings.onUpdate === "function" ? settings.onUpdate : function () {};
    let provider = null;
    let unsubscribe = null;
    let epoch = 0;
    let metaEpoch = 0;
    let destroyed = false;
    let notifyScheduled = false;
    const pending = Object.create(null);

    if (!state.view) {
      state.view = freshView();
    }

    function view() {
      if (!state.view) {
        state.view = freshView();
      }
      return state.view;
    }

    function selection() {
      return state.selection || { datasetIds: [] };
    }

    function hasSelection() {
      return !!(provider && state.selection && state.selection.datasetIds && state.selection.datasetIds.length);
    }

    function scheduleNotify(reason) {
      if (notifyScheduled || destroyed) {
        return;
      }
      notifyScheduled = true;
      Promise.resolve().then(function () {
        notifyScheduled = false;
        if (!destroyed) {
          onUpdate(reason);
        }
      });
    }

    function setProvider(next) {
      if (unsubscribe) {
        unsubscribe();
        unsubscribe = null;
      }
      provider = next || null;
      if (provider && typeof provider.subscribe === "function") {
        unsubscribe = provider.subscribe(function (detail) {
          if (detail && detail.metadataOnly) {
            metaEpoch += 1;
          } else {
            epoch += 1;
          }
          scheduleNotify("provider");
        });
      }
      invalidate();
    }

    function invalidate() {
      epoch += 1;
      Object.keys(pending).forEach(function (name) { delete pending[name]; });
    }

    function baseKey() {
      return epoch + "|" + providerApi().selectionKey(selection());
    }

    // ---- Task definitions ----

    function baseTask() {
      const sel = selection();
      return {
        name: "base",
        key: baseKey() + "|" + metaEpoch,
        present: function () { return !!state.summary || !!view().errors.base; },
        fetch: function () {
          return Promise.all([provider.listDatasets(), provider.getSummary(sel)]).then(function (results) {
            return { datasets: results[0], summary: results[1] };
          });
        },
        apply: function (value) {
          const firstLoad = !state.summary;
          state.datasets = value.datasets || [];
          state.reference = referenceFor(state.datasets, sel);
          if (state.summary !== value.summary) {
            state.summary = value.summary;
          }
          if ((firstLoad || !state.filters) && settings.initialiseOnSelection) {
            initialisePageState(state, state.summary);
          }
          syncComparisonDatasets(state);
        },
        fallback: function (error) {
          view().errors.base = errorMessage(error, "The analysis data could not be loaded.");
        }
      };
    }

    function interactionTasks() {
      const sel = selection();
      const filters = Object.assign({}, state.filters || data().defaultInteractionFilters());
      const tasks = [{
        name: "interactions",
        key: baseKey() + "|" + stableKey(filters),
        present: function () { return !!view().interactions; },
        fetch: function () {
          const rowHeight = state.density === "compact" ? 38 : 46;
          const start = Math.max(0, Math.floor((state.interactionScrollTop || 0) / rowHeight) - INTERACTION_MARGIN);
          const page = provider.queryHybrids(sel, { filters: filters, offset: start, limit: INTERACTION_WINDOW });
          const partners = filters.rna
            ? provider.getTopInteractions(sel, { by: "partner", rna: filters.rna, filters: filters, countMode: filters.countMode })
            : Promise.resolve({ total: 0, rows: [] });
          return Promise.all([page, partners]).then(function (results) {
            return {
              total: results[0].total,
              offset: results[0].offset || 0,
              rows: results[0].rows || [],
              partners: results[1].rows || [],
              filtersKey: stableKey(filters)
            };
          });
        },
        apply: function (value) { view().interactions = value; },
        fallback: function (error) {
          view().interactions = { total: 0, offset: 0, rows: [], partners: [], error: errorMessage(error, "Interactions could not be loaded.") };
        }
      }];
      const selectedIndex = state.selectedRecordIndex;
      if (selectedIndex !== null && selectedIndex !== undefined && !Number.isNaN(Number(selectedIndex))) {
        tasks.push(recordByIndexTask("selectedRecord", Number(selectedIndex)));
      }
      return tasks;
    }

    function recordByIndexTask(name, index) {
      const sel = selection();
      return {
        name: name,
        key: baseKey() + "|" + index,
        present: function () { return !!view()[name] && view()[name].index === index; },
        fetch: function () {
          return provider.queryHybrids(sel, { indices: [index] }).then(function (result) {
            return { index: index, record: result.rows && result.rows[0] || null };
          });
        },
        apply: function (value) { view()[name] = value; },
        fallback: function () { view()[name] = { index: index, record: null }; }
      };
    }

    function contactTasks() {
      const contact = state.contact;
      if (!contact || !contact.rnaX || !contact.rnaY) {
        return [];
      }
      const sel = selection();
      const query = pick(contact, ["rnaX", "rnaY", "binSize", "measure", "orientation", "chimeraType", "homodimer"]);
      // Providers double the bin size until the map fits (a 10.8 kb genome
      // at 10 nt would otherwise be ~1.2 M cells).
      query.maxCells = CONTACT_MAX_CELLS;
      const queryKey = baseKey() + "|" + stableKey(query);
      const display = pick(contact, ["colourCap", "customCap", "scale"]);
      return [{
        name: "contact",
        key: queryKey + "|" + stableKey(display),
        present: function () { return !!state.contact && !!state.contact.matrix; },
        fetch: function () {
          const cached = view().contactBins;
          const bins = cached && cached.key === queryKey
            ? Promise.resolve(cached.bins)
            : provider.getContactBins(sel, query).then(function (result) {
              view().contactBins = { key: queryKey, bins: result };
              return result;
            });
          return bins.then(function (result) {
            return data().contactMatrixFromBins(result, contact);
          });
        },
        apply: function (matrix) { contact.matrix = matrix; },
        fallback: function (error) {
          contact.matrix = data().contactMatrixFromBins({ ready: false, limitExceeded: true, reason: errorMessage(error, "The contact map could not be loaded."), binSize: contact.binSize }, contact);
        }
      }];
    }

    function viewpointTasks() {
      const viewpoint = state.viewpoint;
      if (!viewpoint) {
        return [];
      }
      const sel = selection();
      const params = pick(viewpoint, ["rna", "partner", "chimeraType", "homodimer", "rangeMode", "start", "end", "binSize", "measure"]);
      const length = referenceLength(state, viewpoint.rna);
      return [{
        name: "viewpoint",
        key: baseKey() + "|" + stableKey(params) + "|" + length,
        present: function () { return !!state.viewpoint && !!state.viewpoint.results; },
        fetch: function () {
          const extents = state.summary && state.summary.rnaExtents || {};
          const context = { extent: extents[viewpoint.rna] || null, referenceLength: length };
          const range = data().viewpointRange(viewpoint, context);
          if (!range) {
            return Promise.resolve(data().viewpointResultsFromRecords(viewpoint, [], context));
          }
          return provider.getArms(sel, {
            rna: viewpoint.rna,
            partner: viewpoint.partner || "",
            chimeraType: viewpoint.chimeraType || "all",
            homodimer: viewpoint.homodimer || "all",
            start: range.start,
            end: range.end
          }).then(function (arms) {
            return data().viewpointResultsFromRecords(viewpoint, providerApi().armsToRecords(arms), context);
          });
        },
        apply: function (results) { viewpoint.results = results; },
        fallback: function (error) {
          viewpoint.results = { ready: false, bins: [], records: [], reason: errorMessage(error, "Viewpoint could not be loaded.") };
        }
      }];
    }

    function regionTasks() {
      const region = state.region;
      if (!region) {
        return [];
      }
      const sel = selection();
      const params = pick(region, ["rna", "start", "end", "partner", "partnerStart", "partnerEnd", "overlap"]);
      const tasks = [{
        name: "region",
        key: baseKey() + "|" + stableKey(params),
        present: function () { return !!state.region && !!state.region.results; },
        fetch: function () {
          if (!data().regionQuery(params)) {
            return Promise.resolve(data().regionResultsFromRecords([], params));
          }
          return provider.queryHybrids(sel, { region: params, limit: null }).then(function (result) {
            return data().regionResultsFromRecords(result.rows || [], params);
          });
        },
        apply: function (results) { region.results = results; },
        fallback: function (error) {
          region.results = Object.assign(data().regionResultsFromRecords([], params), { reason: errorMessage(error, "Region results could not be loaded.") });
        }
      }];
      if (region.rna) {
        const referenceParams = pick(region, ["rna", "start", "end"]);
        tasks.push({
          name: "regionReference",
          key: baseKey() + "|" + metaEpoch + "|" + stableKey(referenceParams) + "|" + referenceLength(state, region.rna),
          present: function () { return view().regionReference !== undefined; },
          fetch: function () {
            if (!hasReference(state, region.rna)) {
              return Promise.resolve(null);
            }
            return provider.getSequence(sel, referenceParams);
          },
          apply: function (sequence) {
            view().regionReference = sequence ? {
              id: sequence.referenceId,
              header: sequence.header,
              rna: sequence.rna,
              start: sequence.start,
              end: sequence.end,
              sequence: sequence.sequence,
              params: referenceParams
            } : null;
          },
          fallback: function () { view().regionReference = null; }
        });
      }
      return tasks;
    }

    function comparisonTasks() {
      const comparison = state.comparison;
      if (!comparison || comparison.loading) {
        return [];
      }
      const params = pick(comparison, ["rnaX", "rnaY", "binSize", "measure", "normalise"]);
      const datasets = (comparison.datasets || []).map(function (dataset) {
        return [dataset.id, dataset.condition, dataset.label];
      });
      return [{
        name: "comparison",
        key: baseKey() + "|" + stableKey(params) + "|" + stableKey(datasets),
        present: function () { return !!state.comparison && !!state.comparison.result; },
        fetch: function () {
          const assigned = (comparison.datasets || []).filter(function (dataset) {
            return dataset.condition === "A" || dataset.condition === "B";
          });
          const hasA = assigned.some(function (dataset) { return dataset.condition === "A"; });
          const hasB = assigned.some(function (dataset) { return dataset.condition === "B"; });
          if (!comparison.rnaX || !comparison.rnaY || !hasA || !hasB) {
            return Promise.resolve(data().buildComparisonResults(comparison));
          }
          // No maxCells here: every dataset must share one bin size.
          const query = data().comparisonContactQuery(comparison);
          return Promise.all(assigned.map(function (dataset) {
            return provider.getContactBins({ datasetIds: [dataset.id] }, query);
          })).then(function (binsList) {
            const byId = Object.create(null);
            assigned.forEach(function (dataset, index) { byId[dataset.id] = binsList[index]; });
            return data().buildComparisonResults(Object.assign({}, comparison, {
              datasets: (comparison.datasets || []).map(function (dataset) {
                return byId[dataset.id] ? Object.assign({}, dataset, { contactBins: byId[dataset.id] }) : dataset;
              })
            }));
          });
        },
        apply: function (result) { comparison.result = result; },
        fallback: function (error) {
          comparison.result = { ready: false, reason: errorMessage(error, "The comparison could not be loaded."), groupA: [], groupB: [], cells: [], conservedCells: [] };
        }
      }];
    }

    function structureTasks() {
      const structure = state.structure;
      if (!structure) {
        return [];
      }
      const sel = selection();
      const tasks = [];
      if (structure.source === "reference") {
        const cplfold = structure.engine === "cplfold";
        const guided = cplfold ? structure.cplfoldEvidence === "hyb-blocks" : structure.constraintMode === "hyb-guided";
        const rnas = [structure.rna, structure.secondRna].filter(function (rna, index, list) {
          return rna && list.indexOf(rna) === index;
        });
        const lengths = rnas.map(function (rna) { return referenceLength(state, rna); });
        tasks.push({
          name: "structureEvidence",
          key: baseKey() + "|" + stableKey(rnas) + "|" + stableKey(lengths) + "|" + stableKey(state.reference && state.reference.mapping) + "|" + guided,
          present: function () { return !!view().structureEvidence; },
          fetch: function () {
            const sequences = Promise.all(rnas.map(function (rna) {
              return hasReference(state, rna) ? provider.getSequence(sel, { rna: rna }) : Promise.resolve(null);
            }));
            const arms = guided ? provider.getArms(sel, {}) : Promise.resolve(null);
            return Promise.all([sequences, arms]).then(function (results) {
              const fasta = { sequences: [], mapping: {} };
              results[0].forEach(function (sequence, index) {
                if (!sequence) {
                  return;
                }
                if (!fasta.sequences.some(function (entry) { return entry.id === sequence.referenceId; })) {
                  fasta.sequences.push({ id: sequence.referenceId, header: sequence.header, sequence: sequence.sequence });
                }
                fasta.mapping[rnas[index]] = sequence.referenceId;
              });
              return {
                fasta: state.reference ? fasta : null,
                records: results[1] ? providerApi().armsToRecords(results[1]) : null,
                guided: guided,
                rnas: rnas
              };
            });
          },
          apply: function (value) { view().structureEvidence = value; },
          fallback: function (error) {
            view().structureEvidence = { fasta: null, records: null, guided: guided, rnas: rnas, error: errorMessage(error, "Reference evidence could not be loaded.") };
          }
        });
      }
      if (structure.source === "record") {
        tasks.push({
          name: "structureRecords",
          key: baseKey(),
          present: function () { return !!view().structureRecords; },
          fetch: function () {
            return provider.queryHybrids(sel, { limit: STRUCTURE_RECORD_CHOICES }).then(function (result) {
              return result.rows || [];
            });
          },
          apply: function (rows) { view().structureRecords = rows; },
          fallback: function () { view().structureRecords = []; }
        });
        if (structure.selectedRecordIndex !== null && structure.selectedRecordIndex !== undefined) {
          tasks.push(recordByIndexTask("structureSelectedRecord", Number(structure.selectedRecordIndex)));
        }
      }
      return tasks;
    }

    function reportTasks() {
      const sel = selection();
      const filters = Object.assign({}, state.filters || data().defaultInteractionFilters());
      return [{
        name: "report",
        key: baseKey() + "|" + stableKey(filters),
        present: function () { return !!view().report; },
        fetch: function () {
          const rows = provider.queryHybrids(sel, { filters: filters, sort: "support", limit: REPORT_TABLE_ROWS });
          const partners = filters.rna
            ? provider.getTopInteractions(sel, { by: "partner", rna: filters.rna, filters: filters, countMode: filters.countMode || "support" })
            : Promise.resolve({ rows: [] });
          return Promise.all([rows, partners]).then(function (results) {
            return {
              filteredTotal: results[0].total,
              filteredSample: results[0].rows || [],
              partnerCounts: results[1].rows || []
            };
          });
        },
        apply: function (value) { view().report = value; },
        fallback: function () { view().report = { filteredTotal: 0, filteredSample: [], partnerCounts: [] }; }
      }].concat(contactTasks(), viewpointTasks(), regionTasks(), comparisonTasks(), structureTasks());
    }

    function pageTasks(pageId) {
      if (pageId === "interactions") return interactionTasks();
      if (pageId === "contact-map") return contactTasks();
      if (pageId === "viewpoint") return viewpointTasks();
      if (pageId === "region") return regionTasks();
      if (pageId === "compare") return comparisonTasks();
      if (pageId === "structure") return structureTasks();
      if (pageId === "report") return reportTasks();
      return [];
    }

    // ---- Execution ----

    function run(task) {
      const current = pending[task.name];
      if (current && current.key === task.key) {
        return current.promise;
      }
      const record = { key: task.key, promise: null };
      pending[task.name] = record;
      view().loading[task.name] = true;
      record.promise = Promise.resolve().then(task.fetch).then(function (value) {
        if (destroyed || pending[task.name] !== record) {
          return false;
        }
        delete pending[task.name];
        delete view().loading[task.name];
        delete view().errors[task.name];
        task.apply(value);
        view().keys[task.name] = task.key;
        return true;
      }, function (error) {
        if (destroyed || pending[task.name] !== record) {
          return false;
        }
        delete pending[task.name];
        delete view().loading[task.name];
        view().errors[task.name] = errorMessage(error, "Data could not be loaded.");
        task.fallback(error);
        view().keys[task.name] = task.key;
        if (typeof settings.onError === "function") {
          settings.onError(error, task.name);
        }
        return true;
      });
      return record.promise;
    }

    // Returns null when the page's data is current, otherwise a Promise that
    // resolves (true when something was applied) once the fetches settle.
    function ensure(pageId) {
      if (destroyed || !hasSelection()) {
        return null;
      }
      const base = baseTask();
      const tasks = [base];
      if (state.summary) {
        pageTasks(pageId).forEach(function (task) { if (task) tasks.push(task); });
      }
      const stale = tasks.filter(function (task) {
        return !(view().keys[task.name] === task.key && task.present());
      });
      if (!stale.length) {
        return null;
      }
      return Promise.all(stale.map(run)).then(function (results) {
        return results.some(Boolean);
      });
    }

    function isLoading(name) {
      return !!view().loading[name];
    }

    // options.keepState: the website shell has already initialised summary
    // and page state for this selection; only (re)bind the data layer.
    function setSelection(next, options) {
      const normalised = providerApi().normaliseSelection(next);
      const changed = !providerApi().sameSelection(normalised, selection());
      state.selection = normalised;
      if (options && options.keepState) {
        state.view = freshView();
        invalidate();
        return Promise.resolve(false);
      }
      if (changed) {
        state.summary = null;
        state.datasets = [];
        state.reference = null;
        state.filters = null;
        state.view = freshView();
        invalidate();
      }
      return ensure(null) || Promise.resolve(false);
    }

    function loadInteractionWindow(start) {
      const interactions = view().interactions;
      if (!hasSelection() || !interactions || interactions.error) {
        return Promise.resolve(false);
      }
      const filters = Object.assign({}, state.filters || data().defaultInteractionFilters());
      const key = view().keys.interactions;
      const offset = Math.max(0, Math.floor(start) - INTERACTION_MARGIN);
      const token = {};
      view().interactionWindowToken = token;
      return provider.queryHybrids(selection(), { filters: filters, offset: offset, limit: INTERACTION_WINDOW }).then(function (page) {
        if (destroyed || view().interactionWindowToken !== token || view().keys.interactions !== key || view().interactions !== interactions) {
          return false;
        }
        interactions.offset = page.offset || offset;
        interactions.rows = page.rows || [];
        interactions.total = page.total;
        return true;
      });
    }

    function queryHybrids(query) {
      if (!hasSelection()) {
        return Promise.resolve({ total: 0, offset: 0, rows: [] });
      }
      return provider.queryHybrids(selection(), query || {});
    }

    function findRecord(index) {
      const target = String(index);
      const current = view();
      const candidates = [];
      if (current.selectedRecord && current.selectedRecord.record) candidates.push(current.selectedRecord.record);
      if (current.structureSelectedRecord && current.structureSelectedRecord.record) candidates.push(current.structureSelectedRecord.record);
      const lists = [current.interactions && current.interactions.rows, current.structureRecords, current.report && current.report.filteredSample];
      lists.forEach(function (list) { if (Array.isArray(list)) Array.prototype.push.apply(candidates, list); });
      return candidates.find(function (record) { return record && String(record.index) === target; }) || null;
    }

    function destroy() {
      destroyed = true;
      if (unsubscribe) {
        unsubscribe();
        unsubscribe = null;
      }
      Object.keys(pending).forEach(function (name) { delete pending[name]; });
    }

    return {
      setProvider: setProvider,
      getProvider: function () { return provider; },
      setSelection: setSelection,
      ensure: ensure,
      invalidate: function () { invalidate(); scheduleNotify("invalidate"); },
      isLoading: isLoading,
      loadInteractionWindow: loadInteractionWindow,
      queryHybrids: queryHybrids,
      findRecord: findRecord,
      destroy: destroy
    };
  }

  root.Hyb2PageData = {
    PAGE_IDS: PAGE_IDS,
    INTERACTION_WINDOW: INTERACTION_WINDOW,
    createController: createController,
    initialisePageState: initialisePageState,
    freshView: freshView,
    referenceLength: referenceLength,
    hasReference: hasReference,
    windowRows: windowRows,
    syncComparisonDatasets: syncComparisonDatasets
  };
}(typeof window !== "undefined" ? window : globalThis));
