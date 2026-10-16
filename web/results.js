/*
 * HYB2 result record: one description of the current session that the
 * Report page, the Markdown report, the JSON files and the TSV tables all
 * read, so they cannot disagree on a number, a scope or a status.
 *
 *   Hyb2Results.build(state, options) -> record (synchronous)
 *   Hyb2Results.files(record, audience) -> [{ path, mediaType, text, description }]
 *     audience "ai"       README, report.md, manifest, summary, data_dictionary,
 *                         provenance, evidence, bounded tables
 *     audience "analysis" manifest, data_dictionary, provenance, full tables,
 *                         structure files, figure SVGs
 *   Hyb2Results.withManifest(files, record) -> Promise(files + manifest.json)
 *   Hyb2Results.zip(files) -> Blob (stored, no compression; built in this tab)
 *
 * Every step and result has a fixed id (S03, R17) that does not depend on
 * which analyses ran, so an AI or a script can cite "R26" across exports.
 * A step that did not run has status "not_run" and its results value null;
 * nothing is written as 0 unless it was counted as 0.
 */
(function (root) {
  "use strict";

  const SCHEMA = "hyb2-result/1.0";
  const APP_VERSION = "0.5.0";
  // rows of the record-level interaction table in the AI package; the
  // analysis package has every filtered row up to MAX_ANALYSIS_ROWS
  const AI_INTERACTION_ROWS = 2000;
  const MAX_ANALYSIS_ROWS = 1000000;

  const STATUS = {
    ok: "Completed with results",
    no_result: "Completed; no result met the criteria",
    not_run: "Not run in this session",
    running: "Running when the snapshot was taken",
    failed: "Failed or stopped",
    stale: "Result is from earlier inputs; the inputs have changed since",
    pending: "Configured; result still being prepared"
  };

  const STEPS = [
    ["S01", "parse", "HYB parsing and validation"],
    ["S02", "reference", "Reference FASTA mapping"],
    ["S03", "interactions", "Interaction filtering"],
    ["S04", "contact_map", "Contact map"],
    ["S05", "viewpoint", "Viewpoint coverage"],
    ["S06", "region", "Region Explorer"],
    ["S07", "comparison", "Compare: effect map and similarity heatmap"],
    ["S08", "differential_test", "Compare: differential test (DESeq2 Wald test, JavaScript port)"],
    ["S09", "arc_map", "Compare: arc map of structures and differential interactions"],
    ["S10", "region_structures", "Compare: region structures (panels D, E)"],
    ["S11", "folding_energy", "Compare: folding energy (panels F, G)"],
    ["S12", "structure", "RNA structure prediction"],
    ["S13", "literature_search", "Related research (public database and literature lookups)"]
  ];

  // [id, key, step, label, unit, definition]
  const RESULTS = [
    ["R01", "valid_records", "parse", "Valid HYB records", "records", "Rows of the primary HYB file that passed validation."],
    ["R02", "invalid_records", "parse", "Invalid HYB rows", "rows", "Rows skipped by validation (blank and comment lines not included)."],
    ["R03", "unique_rnas", "parse", "Unique RNAs", "RNAs", "Distinct RNA names across both arms of the valid records."],
    ["R04", "rna_pairs", "parse", "RNA pairs", "pairs", "Distinct RNA pairs, arm order normalised."],
    ["R05", "cluster_support", "parse", "Cluster support", "support units", "Sum of the cluster support of the valid records (1 per record when the file has none)."],
    ["R06", "inferred_chimera_types", "parse", "Records with an inferred chimera type", "records", "Records whose chimera type HYB2 inferred because the file has no type column."],
    ["R07", "fasta_sequences", "reference", "Reference sequences", "sequences", "Sequences in the loaded reference FASTA."],
    ["R08", "mapped_rna_names", "reference", "HYB RNA names mapped to the FASTA", "RNA names", "HYB RNA names matched to a reference sequence."],
    ["R09", "filtered_records", "interactions", "Records matching the current filters", "records", "Valid records that pass the Interactions page filters (see S03 parameters)."],
    ["R10", "contact_cells", "contact_map", "Contact map cells", "cells", "Non-empty bins of the contact map of the S04 RNA pair."],
    ["R11", "contact_records_used", "contact_map", "Records in the contact map", "records", "Records that contributed to at least one contact map cell."],
    ["R12", "contact_bin_size", "contact_map", "Contact map bin size", "nt", "Bin width used (it can be larger than requested; see S04 parameters)."],
    ["R13", "viewpoint_records", "viewpoint", "Records covering the viewpoint", "records", "Records with an arm on the viewpoint RNA within the range."],
    ["R14", "viewpoint_max_coverage", "viewpoint", "Maximum viewpoint coverage", "records per bin", "Largest bin value of the viewpoint profile."],
    ["R15", "region_records", "region", "Records overlapping the region", "records", "Records with an arm overlapping the Region Explorer interval (inclusive)."],
    ["R16", "region_support", "region", "Cluster support in the region", "support units", "Sum of cluster support of the R15 records."],
    ["R17", "region_partners", "region", "Partner RNAs of the region", "RNAs", "Distinct partner RNAs of the R15 records."],
    ["R18", "compare_datasets_a", "comparison", "Datasets in condition A", "datasets", "HYB datasets assigned to condition A."],
    ["R19", "compare_datasets_b", "comparison", "Datasets in condition B", "datasets", "HYB datasets assigned to condition B."],
    ["R20", "comparable_bins", "comparison", "Comparable bins", "cells", "Bins of the effect map (present in at least one dataset)."],
    ["R21", "similarity_bins", "comparison", "Similarity heatmap bins", "cells", "Bins plotted in the similarity heatmap (smaller of the A and B counts at or above the minimum)."],
    ["R22", "cells_tested", "differential_test", "Cells tested", "cells", "Cells that passed the minimum count and were tested."],
    ["R23", "significant_cells", "differential_test", "Significant cells", "cells", "Tested cells with padj below the S08 cutoff."],
    ["R24", "significant_cells_a", "differential_test", "Significant cells higher in A", "cells", "R23 cells with log2 fold change A/B > 0."],
    ["R25", "significant_cells_b", "differential_test", "Significant cells higher in B", "cells", "R23 cells with log2 fold change A/B < 0."],
    ["R26", "interactions_a", "differential_test", "Merged interactions higher in A", "interactions", "Significant cells higher in A merged within the S08 merge range (hyb2_compare)."],
    ["R27", "interactions_b", "differential_test", "Merged interactions higher in B", "interactions", "Significant cells higher in B merged within the S08 merge range (hyb2_compare)."],
    ["R28", "arc_pairs_a", "arc_map", "Base pairs of structure A", "pairs", "Base pairs of the arc map's structure A."],
    ["R29", "arc_pairs_b", "arc_map", "Base pairs of structure B", "pairs", "Base pairs of the arc map's structure B, on A's coordinates."],
    ["R30", "arc_interactions", "arc_map", "Interactions drawn on the arc map", "interactions", "Differential interactions drawn under the arc map's padj cutoff."],
    ["R31", "region_a", "region_structures", "Region A (higher in condition one)", "interaction", "Chosen interaction for panel D as arm1↔arm2 (nt, A coordinates)."],
    ["R32", "region_b", "region_structures", "Region B (higher in condition two)", "interaction", "Chosen interaction for panel E as arm1↔arm2 (nt, A coordinates)."],
    ["R33", "energy_n_one", "folding_energy", "Interactions higher in condition one, folded", "interactions", "Paired RNAcofold energies in panel F."],
    ["R34", "energy_p_one", "folding_energy", "Paired t-test p, interactions higher in condition one", "p-value", "Two-sided paired t-test of dG(one) vs dG(two) in panel F; null when fewer than 3 pairs."],
    ["R35", "energy_n_two", "folding_energy", "Interactions higher in condition two, folded", "interactions", "Paired RNAcofold energies in panel G."],
    ["R36", "energy_p_two", "folding_energy", "Paired t-test p, interactions higher in condition two", "p-value", "Two-sided paired t-test of dG(one) vs dG(two) in panel G; null when fewer than 3 pairs."],
    ["R37", "structure_length", "structure", "Folded sequence length", "nt", "Length of the sequence of the RNA structure prediction."],
    ["R38", "structure_energy", "structure", "Predicted free energy", "kcal/mol", "Free energy of the selected structure."],
    ["R39", "structure_pairs", "structure", "Base pairs", "pairs", "Base pairs of the selected structure."],
    ["R40", "structure_crossing_pairs", "structure", "Crossing (pseudoknotted) pairs", "pairs", "Base pairs that cross another pair."],
    ["R41", "evidence_cards", "literature_search", "External sources added to the report", "cards", "Related research cards the researcher added to the report."]
  ];

  const COLUMN_TEXT = {
    sequence_id: ["string", "", "HYB sequence (read) identifier, column 1."],
    rna_1: ["string", "", "RNA of arm 1."],
    rna_1_start: ["integer", "nt", "Arm 1 start on rna_1, 1-based inclusive."],
    rna_1_end: ["integer", "nt", "Arm 1 end on rna_1, 1-based inclusive."],
    rna_2: ["string", "", "RNA of arm 2."],
    rna_2_start: ["integer", "nt", "Arm 2 start on rna_2, 1-based inclusive."],
    rna_2_end: ["integer", "nt", "Arm 2 end on rna_2, 1-based inclusive."],
    support_count: ["number", "support units", "Cluster support of the record."],
    support_source: ["string", "", "Where support_count came from (column or default)."],
    raw_read_count: ["integer", "reads", "Collapsed source reads, when the read ID carries them."],
    overlap_score: ["number", "", "Column 16 overlap score of standard HYB2 output."],
    homodimer_overlap_ge_5: ["boolean", "", "Overlap score at least 5 (homodimer proxy)."],
    chimera_type: ["string", "", "Chimera type (column 17 or inferred)."],
    chimera_type_source: ["string", "", "column or inferred."],
    dG: ["number", "kcal/mol", "Hybrid free energy from column 3."],
    line: ["integer", "", "Line of the record in the source file."],
    name: ["string", "", "RNA name."],
    rna: ["string", "", "RNA name."],
    records: ["integer", "records", "Number of records."],
    support: ["number", "support units", "Sum of cluster support."],
    x: ["integer", "nt", "Bin start on the x RNA (arm 1), 1-based."],
    y: ["integer", "nt", "Bin start on the y RNA (arm 2), 1-based."],
    value: ["number", "", "Cell value in the S04 measure."],
    bin_start: ["integer", "nt", "Bin start, 1-based inclusive."],
    bin_end: ["integer", "nt", "Bin end, 1-based inclusive."],
    coverage: ["number", "records", "Arm coverage of the bin."],
    file: ["string", "", "Dataset file name (no path)."],
    label: ["string", "", "Dataset label."],
    condition: ["string", "", "A or B."],
    condition_label: ["string", "", "Name of the condition."],
    valid_records: ["integer", "records", "Valid records of the dataset."],
    condition_a: ["number", "normalised count", "Mean library-normalised count of condition A in the bin."],
    condition_b: ["number", "normalised count", "Mean library-normalised count of condition B in the bin."],
    log2_effect: ["number", "log2", "Descriptive log2 effect A/B (not a test)."],
    similarity: ["number", "count", "Smaller raw count of A and B (HYB2 plot_similarity_map)."],
    datasets_present: ["integer", "datasets", "Datasets with a count in the bin."],
    direction: ["string", "", "up = higher in A; down = higher in B."],
    log2FoldChange: ["number", "log2", "DESeq2 log2 fold change A/B."],
    padj: ["number", "", "Benjamini-Hochberg adjusted p-value; NA when not tested."],
    baseMean: ["number", "normalised count", "Mean normalised count over all samples."],
    lineage: ["string", "", "Condition label of the region's enriched lineage."],
    rank: ["integer", "", "Rank of the interaction among the candidates (by padj)."],
    a: ["integer", "nt", "Arm 1 position of the interaction."],
    b: ["integer", "nt", "Arm 2 position of the interaction."],
    pairs_own: ["integer", "pairs", "Base pairs of the enriched lineage's structure that form the interaction."],
    pairs_other: ["integer", "pairs", "Base pairs of the other lineage's structure that form it."],
    X0: ["integer", "nt", "Window X start."],
    X1: ["integer", "nt", "Window X end."],
    Y0: ["integer", "nt", "Window Y start; - when merged with X."],
    Y1: ["integer", "nt", "Window Y end; - when merged with X."],
    length: ["integer", "nt", "Fragment length per arm."],
    side: ["string", "", "enriched_in_one or enriched_in_two."],
    dG_one: ["number", "kcal/mol", "RNAcofold energy with condition one's sequence."],
    dG_two: ["number", "kcal/mol", "RNAcofold energy with condition two's sequence."],
    comparison: ["string", "", "Conditions compared."],
    enriched_in: ["string", "", "Condition the interactions are higher in."],
    n: ["integer", "interactions", "Paired energies."],
    mean_dG_one: ["number", "kcal/mol", "Mean dG with condition one's sequence."],
    mean_dG_two: ["number", "kcal/mol", "Mean dG with condition two's sequence."],
    mean_diff_one_minus_two: ["number", "kcal/mol", "Mean of dG(one) − dG(two)."],
    t: ["number", "", "Paired t statistic."],
    p: ["number", "", "Two-sided p-value."],
    id: ["string", "", "Stable id in this package."],
    key: ["string", "", "Machine name."],
    step: ["string", "", "Step key."],
    status: ["string", "", "Status of the step (see data_dictionary status vocabulary)."],
    unit: ["string", "", "Unit of value."],
    position: ["integer", "nt", "Position, 1-based."],
    base: ["string", "", "Nucleotide."],
    partner: ["integer", "nt", "Paired position; 0 when unpaired."]
  };

  // ---- small helpers

  function number(value) {
    const n = Number(value);
    return Number.isFinite(n) ? n : null;
  }

  function count(list) {
    return Array.isArray(list) ? list.length : null;
  }

  function tsvCell(value) {
    if (value === null || value === undefined) {
      return "NA";
    }
    if (typeof value === "number" && !Number.isFinite(value)) {
      return "NA";
    }
    return String(value).replace(/[\t\r\n]+/g, " ");
  }

  function tsv(columns, rows) {
    return [columns.join("\t")].concat(rows.map(function (row) { return row.map(tsvCell).join("\t"); })).join("\n") + "\n";
  }

  function tsvRowCount(text) {
    const lines = String(text || "").split("\n").filter(function (line) { return line && line[0] !== "#"; });
    return Math.max(0, lines.length - 1);
  }

  function columnsOf(text) {
    const header = String(text || "").split("\n").filter(function (line) { return line && line[0] !== "#"; })[0] || "";
    return header ? header.split("\t") : [];
  }

  function describeColumns(names, overrides) {
    return names.map(function (name) {
      const text = (overrides && overrides[name]) || COLUMN_TEXT[name] || ["string", "", ""];
      return { name: name, type: text[0], unit: text[1] || null, description: text[2] || "" };
    });
  }

  function safe(fn, fallback) {
    try {
      return fn();
    } catch (error) {
      return fallback;
    }
  }

  function range(start, end) {
    return start === undefined || start === null || start === "" ? null : start + "-" + end;
  }

  // ---- record

  // options: { generatedAt, interactionRows (all filtered rows, from the
  // DataProvider), interactionTotal }
  function build(state, options) {
    const s = state || {};
    const o = options || {};
    const summary = s.summary || {};
    const record = {
      schema: SCHEMA,
      application: { name: "HYB2 Web Lite", version: APP_VERSION, buildCommit: root.HYB2_BUILD && root.HYB2_BUILD.commit || null },
      generatedAt: o.generatedAt || new Date().toISOString(),
      scope: {
        kind: "session_snapshot",
        label: "Session report",
        description: "Every analysis of this browser tab as currently configured, from one primary HYB file" +
          ((s.comparison && s.comparison.datasets || []).length > 1 ? " and the Compare datasets" : "") +
          ". Not a selection of results: steps that were not run are listed as not_run.",
        primaryDataset: summary.fileName || null
      },
      inputs: [],
      steps: [],
      results: [],
      tables: [],
      figures: [],
      structures: [],
      evidence: [],
      warnings: [],
      limitations: [
        "Results describe the loaded files only. Counts are not normalised against other experiments.",
        "The effect map and similarity heatmap are descriptive; only S08 is a statistical test.",
        "S08 is a JavaScript port of DESeq2 1.46, not R itself; check key results against R before publication.",
        "Structures (S09-S12) are computational predictions, not experimental structures.",
        "External sources (S13) are background material; citations are not verified and a mention is not evidence for a structure."
      ]
    };
    const steps = {};
    STEPS.forEach(function (spec) {
      const step = { id: spec[0], key: spec[1], title: spec[2], status: "not_run", statusText: STATUS.not_run, detail: null, parameters: {}, inputs: [], results: [], tables: [], figures: [] };
      steps[spec[1]] = step;
      record.steps.push(step);
    });
    const values = {};
    const setStatus = function (key, status, detail) {
      steps[key].status = status;
      steps[key].statusText = STATUS[status];
      steps[key].detail = detail || null;
    };
    const warn = function (key, text) {
      record.warnings.push({ id: "W" + String(record.warnings.length + 1).padStart(2, "0"), step: steps[key].id, text: text });
    };
    const addInput = function (input) {
      input.id = "I" + String(record.inputs.length + 1).padStart(2, "0");
      record.inputs.push(input);
      return input.id;
    };
    // table text, row count and columns are built when an export reads
    // them, so rendering the Report page does not write every table
    const addTable = function (key, table) {
      table.id = "T" + String(record.tables.length + 1).padStart(2, "0");
      table.step = steps[key].id;
      const make = table.make;
      const columnText = table.columnText;
      delete table.make;
      delete table.columnText;
      let text = null;
      const read = function () {
        if (text === null) {
          text = make();
        }
        return text;
      };
      Object.defineProperty(table, "text", { get: read, enumerable: false });
      Object.defineProperty(table, "rowCount", { get: function () { return tsvRowCount(read()); }, enumerable: true });
      Object.defineProperty(table, "columns", { get: function () { return describeColumns(columnsOf(read()), columnText); }, enumerable: true });
      record.tables.push(table);
      steps[key].tables.push(table.id);
      return table;
    };
    const addFigure = function (key, figure) {
      figure.id = "F" + String(record.figures.length + 1).padStart(2, "0");
      figure.step = steps[key].id;
      record.figures.push(figure);
      steps[key].figures.push(figure.id);
    };

    // S01 parse
    if (s.summary) {
      const primary = addInput({ role: "primary_hyb", fileName: summary.fileName || null, bytes: number(summary.fileSize), sha256: summary.sha256 || null,
        sha256Status: summary.sha256 ? "computed" : (summary.sha256Unavailable ? "unavailable" : "pending"), records: number(summary.validRecords) });
      steps.parse.inputs.push(primary);
      setStatus("parse", "ok");
      values.valid_records = number(summary.validRecords);
      values.invalid_records = number(summary.invalidRecords);
      values.unique_rnas = number(summary.uniqueRNAs);
      values.rna_pairs = number(summary.uniquePairs);
      values.cluster_support = number(summary.supportInteractions);
      values.inferred_chimera_types = number(summary.inferredChimeraTypeRecords || 0);
      if (values.invalid_records) {
        warn("parse", values.invalid_records + " invalid rows were skipped.");
      }
      if (values.inferred_chimera_types) {
        warn("parse", "Chimera types of " + values.inferred_chimera_types + " records were inferred; check them before reporting type proportions.");
      }
      if (summary.ambiguousColumn16Records) {
        warn("parse", summary.ambiguousColumn16Records + " rows have a legacy column 16 that may hold a raw-read count or cluster support.");
      }
      const rnaCounts = Array.isArray(summary.rnaCounts) ? summary.rnaCounts : [];
      addTable("parse", {
        file: "tables/top_rnas.tsv", title: "Top RNAs by cluster support", audience: ["ai", "analysis"],
        scope: "top " + rnaCounts.length + " of " + (values.unique_rnas === null ? "?" : values.unique_rnas) + " RNAs by support (the parser keeps the top 10)",
        make: () => tsv(["name", "records", "support"], rnaCounts.map(function (r) { return [r.name, r.records, r.support]; }))
      });
      const pairCounts = Array.isArray(summary.pairCounts) ? summary.pairCounts : [];
      addTable("parse", {
        file: "tables/top_rna_pairs.tsv", title: "Top RNA pairs by cluster support", audience: ["ai", "analysis"],
        scope: "top " + pairCounts.length + " of " + (values.rna_pairs === null ? "?" : values.rna_pairs) + " pairs by support",
        make: () => tsv(["rna_1", "rna_2", "records", "support"], pairCounts.map(function (r) { return [r.rnaOne, r.rnaTwo, r.records, r.support]; }))
      });
    }

    // S02 reference
    const reference = s.reference || null;
    if (reference) {
      steps.reference.inputs.push(addInput({ role: "reference_fasta", fileName: reference.fileName || null, bytes: number(reference.fileSize), sha256: reference.sha256 || null,
        sha256Status: reference.sha256 ? "computed" : "pending" }));
      values.fasta_sequences = number(reference.sequenceCount !== undefined ? reference.sequenceCount : count(s.fasta && s.fasta.sequences));
      values.mapped_rna_names = Object.keys(reference.mapping || {}).filter(function (k) { return reference.mapping[k]; }).length;
      setStatus("reference", values.mapped_rna_names ? "ok" : "no_result", values.mapped_rna_names ? null : "No HYB RNA name matched a FASTA sequence.");
      const names = (summary.rnaNames || []).length;
      if (values.mapped_rna_names < names) {
        warn("reference", "Only " + values.mapped_rna_names + " of " + names + " HYB RNA names matched the reference FASTA.");
      }
    } else if (s.summary) {
      warn("reference", "No reference FASTA was loaded: RNA coordinates are not checked against a sequence.");
    }

    // S03 interactions
    const filters = s.filters || {};
    steps.interactions.parameters = {
      rna: filters.rna || null, partner: filters.partner || null, class: filters.type || "all", chimeraType: filters.chimeraType || "all",
      minimumSupport: filters.minSupport || "1", search: filters.search || null, countMode: filters.countMode || "support"
    };
    const prepared = s.view && s.view.report;
    const total = o.interactionTotal !== undefined ? o.interactionTotal : (prepared ? number(prepared.filteredTotal) : null);
    if (s.summary && total !== null) {
      values.filtered_records = total;
      setStatus("interactions", total ? "ok" : "no_result");
      const rows = Array.isArray(o.interactionRows) ? o.interactionRows : null;
      if (rows && root.Hyb2Data && typeof root.Hyb2Data.recordsToCsv === "function") {
        addTable("interactions", {
          file: "tables/interactions.tsv", title: "Records matching the current filters", audience: ["analysis"],
          scope: rows.length < total ? "first " + rows.length + " of " + total + " filtered records" : "all " + total + " filtered records",
          make: () => csvToTsv(root.Hyb2Data.recordsToCsv(rows.slice(0, MAX_ANALYSIS_ROWS)))
        });
        const topCount = Math.min(rows.length, AI_INTERACTION_ROWS);
        addTable("interactions", {
          file: "tables/interactions_top.tsv", title: "Filtered records with the highest support", audience: ["ai"],
          scope: "top " + topCount + " of " + total + " filtered records by support (the analysis package has all)",
          make: () => csvToTsv(root.Hyb2Data.recordsToCsv(rows.slice().sort(function (left, right) { return (right.supportCount || 0) - (left.supportCount || 0); }).slice(0, AI_INTERACTION_ROWS)))
        });
      }
    } else if (s.summary) {
      // the filtered count arrives from the DataProvider after the page opens
      setStatus("interactions", "pending", "The filtered record count is still being prepared.");
    }

    // S04 contact map
    const contact = s.contact || {};
    const matrix = contact.matrix;
    steps.contact_map.parameters = { rnaX: contact.rnaX || null, rnaY: contact.rnaY || null, requestedBinSize: number(contact.binSize), measure: contact.measure || "records",
      scale: contact.scale || "linear", orientation: contact.orientation || "normalised", chimeraType: contact.chimeraType || "all", homodimer: contact.homodimer || "all" };
    if (matrix && matrix.limitExceeded) {
      setStatus("contact_map", "failed", matrix.reason || "The contact map exceeded the browser limits.");
    } else if (matrix && matrix.ready !== false && Array.isArray(matrix.cells)) {
      values.contact_cells = matrix.cells.length;
      values.contact_records_used = number(matrix.recordsUsed);
      values.contact_bin_size = number(matrix.binSize);
      setStatus("contact_map", matrix.cells.length ? "ok" : "no_result");
      if (matrix.binSizeAdjusted) {
        warn("contact_map", "The bin size was raised from " + matrix.requestedBinSize + " to " + matrix.binSize + " nt to stay within the browser limits.");
      }
      addTable("contact_map", {
        file: "tables/contact_map_cells.tsv", title: "Contact map cells (" + (contact.rnaX || "") + " × " + (contact.rnaY || "") + ")",
        audience: matrix.cells.length <= 20000 ? ["ai", "analysis"] : ["analysis"], scope: "all " + matrix.cells.length + " non-empty cells",
        make: () => tsv(["x", "y", "value", "records", "support"], matrix.cells.map(function (c) { return [c.x, c.y, c.value, c.records, c.support]; }))
      });
    } else if (contact.rnaX && contact.rnaY) {
      setStatus("contact_map", "pending", matrix && matrix.reason || null);
    }

    // S05 viewpoint
    const viewpoint = s.viewpoint || {};
    const vr = viewpoint.results;
    steps.viewpoint.parameters = { rna: viewpoint.rna || null, partner: viewpoint.partner || null, start: number(viewpoint.start), end: number(viewpoint.end),
      binSize: number(viewpoint.binSize), measure: viewpoint.measure || "records" };
    if (vr && vr.ready && Array.isArray(vr.bins)) {
      values.viewpoint_records = count(vr.records);
      values.viewpoint_max_coverage = number(vr.max);
      setStatus("viewpoint", vr.bins.length && vr.max ? "ok" : "no_result");
      addTable("viewpoint", {
        file: "tables/viewpoint_bins.tsv", title: "Viewpoint coverage of " + (vr.rna || viewpoint.rna || ""), audience: ["ai", "analysis"], scope: "all " + vr.bins.length + " bins",
        make: () => tsv(["bin_start", "bin_end", "coverage"], vr.bins.map(function (b) { return [b.start, b.end, b.value]; }))
      });
    } else if (vr && vr.reason) {
      setStatus("viewpoint", "not_run", vr.reason);
    } else if (viewpoint.rna && !vr) {
      setStatus("viewpoint", "pending", "The viewpoint profile is still being prepared.");
    }

    // S06 region
    const region = s.region || {};
    const rr = region.results;
    steps.region.parameters = { rna: region.rna || null, start: number(region.start), end: number(region.end), partner: region.partner || null, overlap: region.overlap || "any" };
    if (region.rna && rr && Array.isArray(rr.records) && !rr.reason) {
      values.region_records = rr.records.length;
      values.region_support = number(rr.support);
      values.region_partners = count(rr.partners);
      setStatus("region", rr.records.length ? "ok" : "no_result");
      addTable("region", {
        file: "tables/region_partners.tsv", title: "Partner RNAs of the region", audience: ["ai", "analysis"], scope: "all " + (rr.partners || []).length + " partners",
        make: () => tsv(["name", "records", "support"], (rr.partners || []).map(function (p) { return [p.name, p.records, p.support]; }))
      });
    } else if (region.rna && (!rr || rr.reason)) {
      setStatus("region", "pending", rr && rr.reason || "The region result is still being prepared.");
    }

    // S07 comparison
    const comparison = s.comparison || {};
    const datasets = Array.isArray(comparison.datasets) ? comparison.datasets : [];
    const labelA = comparison.conditionALabel || "Condition A";
    const labelB = comparison.conditionBLabel || "Condition B";
    steps.comparison.parameters = { conditionA: labelA, conditionB: labelB, rnaX: comparison.rnaX || null, rnaY: comparison.rnaY || null, binSize: number(comparison.binSize),
      measure: comparison.measure || "records", normalise: comparison.normalise || "library", similarityMinimum: number(comparison.similarityMinimum || 1) };
    datasets.forEach(function (dataset) {
      if (dataset.id === "primary") {
        return;
      }
      const ds = dataset.summary || {};
      steps.comparison.inputs.push(addInput({ role: "compare_dataset", fileName: dataset.fileName || null, bytes: number(dataset.fileSize || ds.fileSize), sha256: ds.sha256 || null,
        sha256Status: ds.sha256 ? "computed" : "not_computed", records: number(ds.validRecords), label: dataset.label || null, condition: dataset.condition === "B" ? "B" : "A" }));
    });
    if (datasets.length) {
      addTable("comparison", {
        file: "tables/compare_datasets.tsv", title: "Compare datasets and conditions", audience: ["ai", "analysis"], scope: "all " + datasets.length + " datasets",
        make: () => tsv(["file", "label", "condition", "condition_label", "valid_records"], datasets.map(function (d) {
          return [d.fileName, d.label || "", d.condition === "B" ? "B" : "A", d.condition === "B" ? labelB : labelA, d.summary ? d.summary.validRecords : null];
        }))
      });
    }
    const cr = comparison.result;
    if (cr && cr.ready && Array.isArray(cr.cells)) {
      values.compare_datasets_a = count(cr.groupA);
      values.compare_datasets_b = count(cr.groupB);
      values.comparable_bins = cr.cells.length;
      values.similarity_bins = count(cr.conservedCells);
      setStatus("comparison", cr.cells.length ? "ok" : "no_result");
      addTable("comparison", {
        file: "tables/compare_cells.tsv", title: "Effect map cells (descriptive)", audience: ["analysis"], scope: "all " + cr.cells.length + " comparable bins",
        make: () => tsv(["x", "y", "condition_a", "condition_b", "log2_effect", "similarity", "datasets_present"], cr.cells.map(function (c) {
          return [c.x, c.y, c.conditionA, c.conditionB, c.effect, c.similarity, c.presentDatasets];
        }))
      });
    } else if (datasets.length > 1) {
      setStatus("comparison", "pending", cr && cr.reason || null);
    } else {
      steps.comparison.detail = "Needs at least one dataset in each condition.";
    }

    // S08 differential test
    const deseq = comparison.deseq;
    if (deseq) {
      steps.differential_test.parameters = { minCount: number(deseq.minCount), mergeRange: number(deseq.radius), padjCutoff: number(deseq.padjCutoff),
        independentFilteringAlpha: 0.01, binSize: 10, method: "DESeq2 1.46 Wald test (JavaScript port of HYB2 Desktop's numpy port)" };
      if (deseq.status === "running") {
        setStatus("differential_test", "running", deseq.progress || null);
      } else if (deseq.status === "error") {
        setStatus("differential_test", "failed", deseq.error || null);
      }
      const dr = deseq.result;
      if (dr && deseq.status !== "running" && deseq.status !== "error") {
        const sm = dr.summary || {};
        values.cells_tested = number(sm.cellsTested);
        values.significant_cells = number(sm.significant);
        values.significant_cells_a = number(sm.significantUp);
        values.significant_cells_b = number(sm.significantDown);
        values.interactions_a = number(sm.interactionsUp);
        values.interactions_b = number(sm.interactionsDown);
        const current = root.Hyb2DeseqCard && typeof root.Hyb2DeseqCard.parameters === "function"
          ? safe(function () { return root.Hyb2DeseqCard.parameters(comparison).result.current; }, true) : true;
        setStatus("differential_test", current ? (sm.significant ? "ok" : "no_result") : "stale", current ? null : "The Compare inputs changed after this run.");
        if (!current) {
          warn("differential_test", "The differential test result is from earlier Compare inputs; run it again before reporting.");
        }
        if (root.Hyb2Deseq) {
          addTable("differential_test", {
            file: "tables/differential_interactions.tsv", title: "Merged differential interactions", audience: ["ai", "analysis"],
            scope: "all " + (dr.interactions || []).length + " merged interactions with padj < " + steps.differential_test.parameters.padjCutoff,
            make: () => root.Hyb2Deseq.interactionsToTsv(dr.interactions)
          });
          addTable("differential_test", {
            file: "tables/differential_significant_cells.tsv", title: "Significant cells", audience: ["analysis"],
            scope: "all " + (sm.significant || 0) + " significant cells, in padj order", make: () => root.Hyb2Deseq.significantToTsv(dr.significant)
          });
        }
        if (root.Hyb2DeseqCard && typeof root.Hyb2DeseqCard.panelSvg === "function") {
          addFigure("differential_test", { file: "figures/A_differential_map.svg", title: "A · Differential coverage map", svg: function () { return root.Hyb2DeseqCard.panelSvg(comparison).svg; } });
        }
      }
    }

    // S09-S11 arc map, regions, folding energy
    const arcMap = s.arcMap;
    const arcApi = root.Hyb2ArcMap;
    let arcModel = null;
    if (arcMap && arcApi && (arcMap.a || arcMap.interactions)) {
      const cached = typeof arcApi.cachedModel === "function" ? safe(function () { return arcApi.cachedModel(arcMap); }, null) : null;
      arcModel = cached ? cached.model : safe(function () { return arcApi.build(arcMap); }, null);
      steps.arc_map.parameters = { padj: number(arcMap.padj), conditionOne: arcMap.labelOne || null, conditionTwo: arcMap.labelTwo || null,
        oneOn: arcMap.oneOn || null, twoOn: arcMap.twoOn || null, start: number(arcMap.start), end: number(arcMap.end) };
      [["a", "arc_structure_a"], ["b", "arc_structure_b"], ["interactions", "arc_interactions"], ["annotation", "arc_annotation"]].forEach(function (pair) {
        const item = arcMap[pair[0]];
        if (item && item.name) {
          steps.arc_map.inputs.push(addInput({ role: pair[1], fileName: item.name }));
        }
      });
      if (arcModel && arcModel.ready) {
        values.arc_pairs_a = arcModel.stats ? arcModel.stats.pairsA : null;
        values.arc_pairs_b = arcModel.stats ? arcModel.stats.pairsB : null;
        values.arc_interactions = count(arcModel.overlay);
        setStatus("arc_map", "ok");
        (arcModel.notes || []).filter(function (note) { return /^Warning/.test(note); }).forEach(function (note) { warn("arc_map", note); });
        const svgText = cached && cached.svg;
        addFigure("arc_map", { file: "figures/C_arc_map.svg", title: "C · Arc map", svg: function () { return svgText || arcApi.svg(arcMap, arcModel); } });
      } else {
        setStatus("arc_map", "not_run", arcModel && arcModel.reason || null);
      }
      if (arcMap.a) {
        record.structures.push(structureFile("results/structure_A.dbn", "Arc map structure A", arcMap.a));
      }
      if (arcMap.b) {
        record.structures.push(structureFile("results/structure_B.dbn", "Arc map structure B", arcMap.b));
      }
    }
    const panels = root.Hyb2RegionPanels;
    if (arcModel && arcModel.ready && panels && arcMap.interactions) {
      const model = regionModel(arcMap, s.regionPanels, panels);
      if (model && model.ready) {
        const chosen = model.regions.filter(function (r) { return r.chosen; });
        model.regions.forEach(function (r) {
          values[r.label === "A" ? "region_a" : "region_b"] = r.chosen ? r.chosen.a + "↔" + r.chosen.b : null;
          if (!r.chosen && r.reason) {
            warn("region_structures", "Region " + r.label + ": " + r.reason);
          }
        });
        steps.region_structures.parameters = model.options ? JSON.parse(JSON.stringify(model.options)) : {};
        setStatus("region_structures", chosen.length ? "ok" : "no_result");
        addTable("region_structures", { file: "tables/regions.tsv", title: "Chosen regions D, E", audience: ["ai", "analysis"], scope: chosen.length + " regions", make: () => panels.regionsTsv(model) });
        if (chosen.length) {
          addFigure("region_structures", { file: "figures/DE_region_structures.svg", title: "D, E · Region structures", svg: function () { return panels.svg(model); } });
        }
      } else {
        setStatus("region_structures", "not_run", model && model.reason || null);
      }
    }
    const energy = s.foldingEnergy;
    const fe = root.Hyb2FoldingEnergy;
    if (energy && fe) {
      steps.folding_energy.parameters = { padj: number(energy.padj), fragmentLength: number(energy.length), temperature: 37, engine: "ViennaRNA RNAcofold (WebAssembly)" };
      if (energy.running) {
        setStatus("folding_energy", "running");
      } else if (energy.error) {
        setStatus("folding_energy", "failed", energy.error);
      } else if (energy.results) {
        const stats = fe.summarise(energy.results.rows);
        const one = stats.find(function (x) { return x.side === "enriched_in_one"; });
        const two = stats.find(function (x) { return x.side === "enriched_in_two"; });
        values.energy_n_one = one ? one.n : null;
        values.energy_p_one = one && Number.isFinite(one.p) ? one.p : null;
        values.energy_n_two = two ? two.n : null;
        values.energy_p_two = two && Number.isFinite(two.p) ? two.p : null;
        setStatus("folding_energy", energy.results.rows.length ? "ok" : "no_result");
        const labels = { one: arcMap && arcMap.labelOne || "condition one", two: arcMap && arcMap.labelTwo || "condition two" };
        addTable("folding_energy", { file: "tables/folding_energy.tsv", title: "RNAcofold energies per interaction", audience: ["ai", "analysis"],
          scope: "all " + energy.results.rows.length + " folded interactions", make: () => fe.toTsv(energy.results) });
        addTable("folding_energy", { file: "tables/folding_energy_tests.tsv", title: "Paired t-tests", audience: ["ai", "analysis"], scope: "both sides", make: () => fe.statsTsv(energy.results, labels) });
        if (stats.some(function (x) { return x.n > 0; })) {
          addFigure("folding_energy", { file: "figures/FG_folding_energy.svg", title: "F, G · Folding energy", svg: function () { return fe.svg(energy.results, labels); } });
        }
      }
    }

    // S12 structure
    const structure = s.structure || {};
    const sres = structure.result;
    steps.structure.parameters = { engine: structure.engine || null, source: structure.source || null, rna: structure.rna || null, start: number(structure.start), end: number(structure.end),
      constraintMode: structure.constraintMode || "none", pseudoknots: structure.engine === "cplfold" ? structure.cplfoldAllowPseudoknot !== false : null };
    if (sres && sres.sequence) {
      values.structure_length = sres.sequence.length;
      values.structure_energy = number(sres.energy);
      values.structure_pairs = count(sres.pairs);
      values.structure_crossing_pairs = number(sres.crossingPairs || 0);
      setStatus("structure", "ok");
      record.structures.push(structureFile("results/structure_prediction.dbn", "RNA structure prediction (" + (sres.engine || structure.engine || "") + ")",
        { name: (structure.rna || "structure") + (structure.start ? ":" + structure.start + "-" + structure.end : ""), sequence: sres.sequence, dotBracket: sres.dotBracket }));
    }

    // S13 literature search
    const evidence = s.evidence;
    const cards = evidence && Array.isArray(evidence.cards) ? evidence.cards : [];
    const included = root.Hyb2Evidence && typeof root.Hyb2Evidence.reportCards === "function" ? safe(function () { return root.Hyb2Evidence.reportCards(s); }, []) : [];
    record.literatureSearch = {
      status: cards.length ? (cards.some(function (c) { return c.status === "success"; }) ? "ok" : cards.some(function (c) { return c.status === "no_match"; }) ? "no_result" : "failed") : "not_run",
      lookups: cards.length,
      includedInReport: included.length,
      hit_count: included.length ? included.reduce(function (sum, c) { return sum + (typeof c.hit_count === "number" ? c.hit_count : 0); }, 0) : null
    };
    setStatus("literature_search", record.literatureSearch.status);
    if (cards.length) {
      values.evidence_cards = included.length;
    }
    included.forEach(function (card, index) {
      record.evidence.push({
        id: "E" + String(index + 1).padStart(2, "0"),
        source: card.source_provider, tool: card.source_tool, contentKind: card.content_kind, query: card.query_identifier, taxon: card.taxon || null,
        identityConfirmation: card.identity_confirmation, retrievedAt: card.retrieved_at, status: card.status, hitCount: card.hit_count,
        review: { state: card.review && card.review.state || "unreviewed", note: card.review && card.review.note || null },
        evidenceNature: card.content_kind === "database_metadata" ? "database annotation (background, not validation)" : "literature index (a mention, not evidence)",
        citationsVerified: false, sourceUrl: card.source_url,
        entries: (card.entries || []).map(function (e) { return { id: e.id || null, description: e.description || null, title: e.title || null, year: e.year || null, pmid: e.pmid || null }; })
      });
    });

    // results, in catalog order, every one present
    RESULTS.forEach(function (spec) {
      const step = steps[spec[2]];
      const known = Object.prototype.hasOwnProperty.call(values, spec[1]);
      const value = known ? values[spec[1]] : null;
      record.results.push({ id: spec[0], key: spec[1], step: step.id, label: spec[3], value: value, unit: spec[4], definition: spec[5],
        status: known && value !== null ? step.status : (step.status === "ok" || step.status === "no_result" ? "not_available" : step.status) });
      step.results.push(spec[0]);
    });
    return record;
  }

  function structureFile(path, title, structure) {
    const pairs = structure.pairs || [];
    let dotBracket = structure.dotBracket || "";
    const length = structure.sequence ? structure.sequence.length : (structure.length || 0);
    if (!dotBracket && length) {
      const chars = new Array(length).fill(".");
      pairs.forEach(function (p) {
        if (p[0] >= 1 && p[1] <= length) {
          chars[p[0] - 1] = "(";
          chars[p[1] - 1] = ")";
        }
      });
      dotBracket = chars.join("");
    }
    return { file: path, title: title, name: structure.name || null, length: length, text: ">" + (structure.name || title) + "\n" + (structure.sequence ? structure.sequence + "\n" : "") + dotBracket + "\n" };
  }

  let lastRegion = null;
  function regionModel(arcMap, rp, panels) {
    const key = [arcMap, rp, arcMap.interactions, arcMap.a, arcMap.b, arcMap.padj, arcMap.oneOn, arcMap.twoOn, arcMap.labelOne, arcMap.labelTwo,
      rp && rp.pickOne, rp && rp.pickTwo, rp && rp.fastaA, rp && rp.fastaB, rp && JSON.stringify(Object.keys(rp).filter(function (k) { return typeof rp[k] !== "object"; }).map(function (k) { return rp[k]; }))];
    if (lastRegion && lastRegion.key.length === key.length && lastRegion.key.every(function (v, i) { return v === key[i]; })) {
      return lastRegion.model;
    }
    const model = safe(function () { return panels.select(arcMap, rp); }, null);
    lastRegion = { key: key, model: model };
    return model;
  }

  function csvToTsv(csv) {
    // recordsToCsv quotes fields with commas or quotes; convert to TSV
    const out = [];
    String(csv || "").split(/\r?\n/).forEach(function (line) {
      if (!line) {
        return;
      }
      const cells = [];
      let cell = "";
      let quoted = false;
      for (let i = 0; i < line.length; i += 1) {
        const ch = line[i];
        if (quoted) {
          if (ch === '"' && line[i + 1] === '"') {
            cell += '"';
            i += 1;
          } else if (ch === '"') {
            quoted = false;
          } else {
            cell += ch;
          }
        } else if (ch === '"') {
          quoted = true;
        } else if (ch === ",") {
          cells.push(cell);
          cell = "";
        } else {
          cell += ch;
        }
      }
      cells.push(cell);
      out.push(cells.map(function (c) { return c === "" ? "NA" : c.replace(/[\t\r\n]+/g, " "); }).join("\t"));
    });
    return out.join("\n") + "\n";
  }

  // ---- text outputs

  function formatValue(result) {
    if (result.value === null || result.value === undefined) {
      return "—";
    }
    if (typeof result.value === "number") {
      if (result.unit === "p-value") {
        return result.value.toExponential(2);
      }
      return Number.isInteger(result.value) ? result.value.toLocaleString("en-US") : String(Number(result.value.toPrecision(6)));
    }
    return String(result.value);
  }

  function markdownCell(text) {
    return String(text === null || text === undefined ? "" : text).replace(/\|/g, "\\|").replace(/\r?\n/g, " ");
  }

  function reportMarkdown(record) {
    const lines = [];
    lines.push("# HYB2 analysis report");
    lines.push("");
    lines.push("- Scope: **" + record.scope.label + "**. " + record.scope.description);
    lines.push("- Primary dataset: `" + (record.scope.primaryDataset || "none") + "`");
    lines.push("- Generated: " + record.generatedAt + " by " + record.application.name + " " + record.application.version +
      (record.application.buildCommit ? " (build " + record.application.buildCommit.slice(0, 12) + ")" : "") + ", locally in a browser tab.");
    lines.push("- Schema: `" + record.schema + "`. Ids: S = step, R = result, T = table, F = figure, E = external source, W = warning, I = input.");
    lines.push("");
    lines.push("This text was generated from a fixed template, not by a language model. Every number below is also in `summary.json` under the same id.");
    lines.push("");
    lines.push("## Steps");
    lines.push("");
    lines.push("| Id | Step | Status | Detail |");
    lines.push("|---|---|---|---|");
    record.steps.forEach(function (step) {
      lines.push("| " + step.id + " | " + markdownCell(step.title) + " | `" + step.status + "` | " + markdownCell(step.detail || step.statusText) + " |");
    });
    lines.push("");
    lines.push("## Results");
    lines.push("");
    lines.push("Results of steps that did not run are listed with no value (—), never as 0.");
    lines.push("");
    lines.push("| Id | Step | Result | Value | Unit | Status |");
    lines.push("|---|---|---|---|---|---|");
    record.results.forEach(function (result) {
      lines.push("| " + result.id + " | " + result.step + " | " + markdownCell(result.label) + " | " + markdownCell(formatValue(result)) + " | " + markdownCell(result.unit) + " | `" + result.status + "` |");
    });
    lines.push("");
    lines.push("## Warnings");
    lines.push("");
    if (record.warnings.length) {
      record.warnings.forEach(function (w) { lines.push("- **" + w.id + "** (" + w.step + "): " + w.text); });
    } else {
      lines.push("- None.");
    }
    lines.push("");
    lines.push("## Limitations");
    lines.push("");
    record.limitations.forEach(function (text) { lines.push("- " + text); });
    lines.push("");
    lines.push("## Inputs");
    lines.push("");
    record.inputs.forEach(function (input) {
      lines.push("- **" + input.id + "** " + input.role + ": `" + (input.fileName || "unnamed") + "`" + (input.records !== undefined && input.records !== null ? ", " + input.records.toLocaleString("en-US") + " valid records" : "") +
        (input.sha256 ? ", SHA-256 `" + input.sha256 + "`" : input.sha256Status ? ", SHA-256 " + input.sha256Status : "") + (input.condition ? ", condition " + input.condition + (input.label ? " (" + input.label + ")" : "") : ""));
    });
    lines.push("");
    lines.push("## External sources");
    lines.push("");
    lines.push("Literature search status: `" + record.literatureSearch.status + "`" + (record.literatureSearch.hit_count === null ? " (no hit count: " + (record.literatureSearch.status === "not_run" ? "not run" : "none added to the report") + ")" : ", " + record.literatureSearch.hit_count + " hits in the added cards") + ".");
    lines.push("");
    record.evidence.forEach(function (e) {
      lines.push("- **" + e.id + "** " + e.source + ", query `" + e.query + "`" + (e.taxon ? " (taxon " + e.taxon + ")" : "") + ": " + e.evidenceNature + "; review `" + e.review.state + "`; citations not verified.");
    });
    lines.push("");
    lines.push("## Tables and figures");
    lines.push("");
    record.tables.forEach(function (t) { lines.push("- **" + t.id + "** (" + t.step + ") `" + t.file + "`: " + t.title + "; " + t.scope + "."); });
    record.figures.forEach(function (f) { lines.push("- **" + f.id + "** (" + f.step + ") `" + f.file + "`: " + f.title + " (analysis package)."); });
    lines.push("");
    lines.push("## Parameters");
    lines.push("");
    record.steps.filter(function (step) { return step.status !== "not_run" && Object.keys(step.parameters).length; }).forEach(function (step) {
      lines.push("- **" + step.id + "** " + Object.keys(step.parameters).filter(function (k) { return step.parameters[k] !== null && step.parameters[k] !== undefined; })
        .map(function (k) { return k + " = " + (typeof step.parameters[k] === "object" ? JSON.stringify(step.parameters[k]) : step.parameters[k]); }).join("; "));
    });
    lines.push("");
    return lines.join("\n");
  }

  function readmeMarkdown(record, audience, files) {
    const lines = [
      "# HYB2 result package",
      "",
      (audience === "ai" ? "Context package for an AI assistant" : "Data package for further analysis") + " from " + record.application.name + " " + record.application.version + ", generated " + record.generatedAt + ".",
      "",
      "Scope: **" + record.scope.label + "**: " + record.scope.description,
      "",
      "## Reading order",
      "",
      audience === "ai" ? "1. `report.md`: what was analysed, which steps ran, the results with ids, warnings and limits." : "1. `data_dictionary.json`: columns, units, coordinates and missing values.",
      audience === "ai" ? "2. `summary.json`: the same results as typed values with status and unit." : "2. `manifest.json`: every file with its size and SHA-256.",
      "3. `data_dictionary.json` before reading a table; `provenance.json` for methods and parameters" + (audience === "ai" ? "; `evidence.json` for external sources." : "."),
      "4. Tables under `tables/` only when a question needs rows" + (audience === "analysis" ? "; structures under `results/`, figures under `figures/`." : "."),
      "",
      "## Conventions",
      "",
      "- Coordinates are 1-based and inclusive, on each RNA's own sequence unless a column says otherwise.",
      "- Missing values are `NA` in TSV and `null` in JSON. A step that did not run has status `not_run` and its results are `null`, never 0.",
      "- Status vocabulary: " + Object.keys(STATUS).map(function (k) { return "`" + k + "` " + STATUS[k].toLowerCase(); }).join("; ") + ".",
      "- Ids are fixed: R26 always means \"" + RESULTS[25][3] + "\". Cite them (\"R26\", \"T05\", \"E01\") instead of \"the report\".",
      "",
      "## Not included",
      "",
      "- The input HYB, FASTA, structure and annotation files themselves (only their names and SHA-256 when computed).",
      "- Local file paths, browser data, keys or account details: the browser never sees paths, and the app has none of the others.",
      audience === "ai" ? "- Large tables (all filtered records, every comparison cell, significant cells): use the analysis package." : "- Interpretation text: this package has data and definitions only.",
      "",
      "## Files",
      ""
    ];
    files.forEach(function (f) { lines.push("- `" + f.path + "`: " + f.description); });
    lines.push("- `manifest.json`: file list with sizes and SHA-256.");
    lines.push("");
    return lines.join("\n");
  }

  function dataDictionary(record, tables) {
    return {
      schema: SCHEMA,
      conventions: {
        coordinates: "1-based, inclusive, on each RNA's own sequence; contact-map and comparison x/y are bin starts",
        missing: { tsv: "NA", json: null },
        notRun: "status not_run; values null, never 0",
        statusVocabulary: STATUS,
        units: "given per column; counts are record counts unless the column says support units"
      },
      tables: tables.map(function (t) {
        return { id: t.id, file: t.file, step: t.step, title: t.title, scope: t.scope, rowCount: t.rowCount, columns: t.columns };
      }),
      results: RESULTS.map(function (spec) { return { id: spec[0], key: spec[1], label: spec[3], unit: spec[4], definition: spec[5] }; })
    };
  }

  function provenance(record) {
    return {
      schema: SCHEMA,
      application: record.application,
      generatedAt: record.generatedAt,
      environment: "Browser tab; all computation local; no server upload",
      scope: record.scope,
      inputs: record.inputs,
      steps: record.steps.map(function (step) {
        return { id: step.id, key: step.key, title: step.title, status: step.status, detail: step.detail, parameters: step.parameters, inputs: step.inputs, results: step.results, tables: step.tables, figures: step.figures };
      }),
      methods: {
        parsing: "HYB2 Web Lite parser (HYB 15-17 columns, sam2hyb 18 columns)",
        differentialTest: "DESeq2 1.46 Wald test, JavaScript port of HYB2 Desktop's numpy port; hyb2_compare interaction merge",
        arcMap: "Banded global alignment of B onto A (HYB2 Desktop seqmap)",
        foldingEnergy: "ViennaRNA RNAcofold, WebAssembly, 37 °C, default parameters",
        structure: "ViennaRNA (WebAssembly) or CPLfold (Pyodide)"
      },
      reproducibility: "Versions, parameters and input digests are recorded so results can be traced. Re-running needs the same input files; the package does not contain them."
    };
  }

  function summaryJson(record) {
    return {
      schema: SCHEMA,
      generatedAt: record.generatedAt,
      application: record.application,
      scope: record.scope,
      steps: record.steps.map(function (step) { return { id: step.id, key: step.key, title: step.title, status: step.status, detail: step.detail }; }),
      results: record.results,
      warnings: record.warnings,
      literature_search: record.literatureSearch,
      limitations: record.limitations
    };
  }

  function evidenceJson(record) {
    return { schema: SCHEMA, literature_search: record.literatureSearch, sources: record.evidence,
      note: "External records are background material chosen by the researcher. Citations are not verified; a database entry or a literature mention does not validate a structure or an interaction." };
  }

  function json(value) {
    return JSON.stringify(value, null, 2) + "\n";
  }

  // files(record, audience) -> [{ path, mediaType, text, description }]
  function files(record, audience) {
    const kind = audience === "analysis" ? "analysis" : "ai";
    const tables = record.tables.filter(function (t) { return t.audience.indexOf(kind) >= 0; });
    const out = [];
    if (kind === "ai") {
      out.push({ path: "report.md", mediaType: "text/markdown", text: reportMarkdown(record), description: "Template report: scope, steps, results with ids, warnings, limits, sources." });
      out.push({ path: "summary.json", mediaType: "application/json", text: json(summaryJson(record)), description: "Steps and results as typed values with status and unit." });
    }
    out.push({ path: "data_dictionary.json", mediaType: "application/json", text: json(dataDictionary(record, tables)), description: "Conventions, status vocabulary, and every column of every table." });
    out.push({ path: "provenance.json", mediaType: "application/json", text: json(provenance(record)), description: "Application build, inputs with digests, methods and parameters per step." });
    if (kind === "ai") {
      out.push({ path: "evidence.json", mediaType: "application/json", text: json(evidenceJson(record)), description: "External sources added to the report, with query, nature and review state." });
    } else {
      out.push({ path: "summary.json", mediaType: "application/json", text: json(summaryJson(record)), description: "Steps and results as typed values with status and unit." });
    }
    tables.forEach(function (t) {
      out.push({ path: t.file, mediaType: "text/tab-separated-values", text: t.text, description: t.id + " " + t.title + " (" + t.scope + ")." });
    });
    if (kind === "analysis") {
      record.structures.forEach(function (st) {
        out.push({ path: st.file, mediaType: "text/plain", text: st.text, description: st.title + (st.length ? ", " + st.length + " nt" : "") + " (dot-bracket)." });
      });
      record.figures.forEach(function (f) {
        const text = safe(f.svg, "");
        if (text) {
          out.push({ path: f.file, mediaType: "image/svg+xml", text: text, description: f.id + " " + f.title + "." });
        }
      });
    }
    out.unshift({ path: "README.md", mediaType: "text/markdown", text: readmeMarkdown(record, kind, out), description: "What the package contains, reading order, conventions, what is excluded." });
    return out;
  }

  // ---- digests, manifest, zip

  function utf8(text) {
    return new TextEncoder().encode(text);
  }

  function sha256(bytes) {
    const subtle = root.crypto && root.crypto.subtle;
    if (!subtle) {
      return Promise.resolve(null);
    }
    return subtle.digest("SHA-256", bytes).then(function (buffer) {
      return Array.from(new Uint8Array(buffer)).map(function (b) { return b.toString(16).padStart(2, "0"); }).join("");
    }).catch(function () { return null; });
  }

  function withManifest(list, record) {
    return Promise.all(list.map(function (f) {
      const bytes = utf8(f.text);
      return sha256(bytes).then(function (digest) { return { path: f.path, mediaType: f.mediaType, bytes: bytes.length, sha256: digest, description: f.description }; });
    })).then(function (entries) {
      const manifest = { schema: SCHEMA, generatedAt: record.generatedAt, application: record.application, scope: record.scope, files: entries,
        note: "sha256 is null where this browser offers no digest API." };
      return list.concat([{ path: "manifest.json", mediaType: "application/json", text: json(manifest), description: "File list with sizes and SHA-256." }]);
    });
  }

  let crcTable = null;
  function crc32(bytes) {
    if (!crcTable) {
      crcTable = new Uint32Array(256);
      for (let n = 0; n < 256; n += 1) {
        let c = n;
        for (let k = 0; k < 8; k += 1) {
          c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
        }
        crcTable[n] = c >>> 0;
      }
    }
    let crc = 0xffffffff;
    for (let i = 0; i < bytes.length; i += 1) {
      crc = crcTable[(crc ^ bytes[i]) & 0xff] ^ (crc >>> 8);
    }
    return (crc ^ 0xffffffff) >>> 0;
  }

  // zip(files, folder) -> Uint8Array of a stored (uncompressed) ZIP
  function zipBytes(list, folder) {
    const prefix = folder ? folder.replace(/\/?$/, "/") : "";
    const now = new Date();
    const time = (now.getHours() << 11) | (now.getMinutes() << 5) | (now.getSeconds() >> 1);
    const date = ((now.getFullYear() - 1980) << 9) | ((now.getMonth() + 1) << 5) | now.getDate();
    const locals = [];
    const centrals = [];
    let offset = 0;
    list.forEach(function (f) {
      const name = utf8(prefix + f.path);
      const data = typeof f.text === "string" ? utf8(f.text) : f.bytes;
      const crc = crc32(data);
      const local = new Uint8Array(30 + name.length);
      const lv = new DataView(local.buffer);
      lv.setUint32(0, 0x04034b50, true);
      lv.setUint16(4, 20, true);
      lv.setUint16(6, 0x0800, true); // UTF-8 names
      lv.setUint16(8, 0, true);
      lv.setUint16(10, time, true);
      lv.setUint16(12, date, true);
      lv.setUint32(14, crc, true);
      lv.setUint32(18, data.length, true);
      lv.setUint32(22, data.length, true);
      lv.setUint16(26, name.length, true);
      local.set(name, 30);
      const central = new Uint8Array(46 + name.length);
      const cv = new DataView(central.buffer);
      cv.setUint32(0, 0x02014b50, true);
      cv.setUint16(4, 20, true);
      cv.setUint16(6, 20, true);
      cv.setUint16(8, 0x0800, true);
      cv.setUint16(10, 0, true);
      cv.setUint16(12, time, true);
      cv.setUint16(14, date, true);
      cv.setUint32(16, crc, true);
      cv.setUint32(20, data.length, true);
      cv.setUint32(24, data.length, true);
      cv.setUint16(28, name.length, true);
      cv.setUint32(42, offset, true);
      central.set(name, 46);
      locals.push(local, data);
      centrals.push(central);
      offset += local.length + data.length;
    });
    const centralSize = centrals.reduce(function (sum, c) { return sum + c.length; }, 0);
    const end = new Uint8Array(22);
    const ev = new DataView(end.buffer);
    ev.setUint32(0, 0x06054b50, true);
    ev.setUint16(8, list.length, true);
    ev.setUint16(10, list.length, true);
    ev.setUint32(12, centralSize, true);
    ev.setUint32(16, offset, true);
    const out = new Uint8Array(offset + centralSize + 22);
    let at = 0;
    locals.concat(centrals, [end]).forEach(function (part) {
      out.set(part, at);
      at += part.length;
    });
    return out;
  }

  function zip(list, folder) {
    return new Blob([zipBytes(list, folder)], { type: "application/zip" });
  }

  root.Hyb2Results = {
    SCHEMA: SCHEMA,
    STATUS: STATUS,
    STEPS: STEPS,
    RESULTS: RESULTS,
    AI_INTERACTION_ROWS: AI_INTERACTION_ROWS,
    MAX_ANALYSIS_ROWS: MAX_ANALYSIS_ROWS,
    build: build,
    files: files,
    reportMarkdown: reportMarkdown,
    formatValue: formatValue,
    withManifest: withManifest,
    crc32: crc32,
    zipBytes: zipBytes,
    zip: zip,
    csvToTsv: csvToTsv
  };
}(typeof window !== "undefined" ? window : globalThis));
