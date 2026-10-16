#!/usr/bin/env node
// Checks web/results.js, the result record behind the Report page and its
// exports:
//   - a step that did not run is not_run and its results are null, never 0;
//   - the Report page's status chips and "Results at a glance" values, the
//     Markdown report, summary.json and the TSV tables agree with the record;
//   - the AI package leaves out the analysis-only tables; the manifest's
//     sizes and SHA-256 match the files; the ZIP reads back byte for byte.
// The same checks on the real ZAFR vs ZASI example run in verify-example.cjs.
"use strict";

const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const repository = path.resolve(__dirname, "..");
const web = function (file) { return path.join(repository, "web", file); };

function load(context, files) {
  files.forEach(function (file) {
    vm.runInContext(fs.readFileSync(web(file), "utf8"), context, { filename: file });
  });
}

const context = vm.createContext({
  Intl, Number, String, Boolean, Object, Array, Math, Date, RegExp, Set, Map, WeakMap, JSON, Promise, Uint8Array, Uint32Array, DataView, TextEncoder, Blob,
  crypto: globalThis.crypto, console,
  window: {}
});
context.window.window = context.window;
context.window.crypto = globalThis.crypto;
context.window.HYB2_BUILD = { commit: "0123456789abcdef0123" };
context.window.Hyb2Data = {
  recordsToCsv: function (records) {
    return ["sequence_id,rna_1,rna_1_start,rna_1_end,rna_2,rna_2_start,rna_2_end,support_count,dG"].concat(records.map(function (r) {
      return [r.id, r.rnaOne, r.rnaOneStart, r.rnaOneEnd, r.rnaTwo, r.rnaTwoStart, r.rnaTwoEnd, r.supportCount, r.dg === null ? "" : r.dg].map(function (v) {
        const text = String(v);
        return /[",\n]/.test(text) ? '"' + text.replace(/"/g, '""') + '"' : text;
      }).join(",");
    })).join("\n");
  }
};
// results.js reads window.*; report-page.js needs the helpers below
load(context, ["results.js"]);
context.window.Hyb2UI = {};
load(context, ["report-page.js"]);
const R = context.window.Hyb2Results;
const report = context.window.Hyb2Report;

// ---- an empty session: nothing beyond parsing ran
const summary = {
  fileName: "sample.hyb", fileSize: 4096, validRecords: 120, invalidRecords: 0, uniqueRNAs: 3, uniquePairs: 2, supportInteractions: 150,
  inferredChimeraTypeRecords: 0, rnaNames: ["RNA_A", "RNA_B", "RNA_C"],
  rnaCounts: [{ name: "RNA_A", records: 100, support: 120 }], pairCounts: [{ rnaOne: "RNA_A", rnaTwo: "RNA_B", records: 90, support: 110 }]
};
const empty = { summary: summary, filters: {}, view: { report: { filteredTotal: 120, filteredSample: [], partnerCounts: [] } } };
const bare = R.build(empty, { generatedAt: "2026-10-16T00:00:00.000Z" });
const status = function (record, key) { return record.steps.find(function (s) { return s.key === key; }).status; };
const result = function (record, id) { return record.results.find(function (r) { return r.id === id; }); };
assert.equal(bare.schema, "hyb2-result/1.0");
assert.equal(bare.steps.length, R.STEPS.length);
assert.equal(bare.results.length, R.RESULTS.length, "every catalog result is listed, run or not");
assert.equal(status(bare, "parse"), "ok");
["contact_map", "viewpoint", "region", "comparison", "differential_test", "arc_map", "region_structures", "folding_energy", "structure", "literature_search"].forEach(function (key) {
  assert.equal(status(bare, key), "not_run", key + " did not run");
});
bare.results.forEach(function (r) {
  const step = bare.steps.find(function (s) { return s.id === r.step; });
  if (step.status === "not_run") {
    assert.equal(r.value, null, r.id + " of a step that did not run is null, not 0");
    assert.equal(r.status, "not_run");
  }
});
assert.equal(result(bare, "R01").value, 120);
assert.equal(result(bare, "R06").value, 0, "a counted zero stays 0");
assert.deepEqual(JSON.parse(JSON.stringify(bare.literatureSearch)), { status: "not_run", lookups: 0, includedInReport: 0, hit_count: null },
  "no literature search: status not_run and hit_count null, not 0");
// fixed ids: the same key has the same id in every record
assert.equal(result(bare, "R23").key, "significant_cells");
assert.equal(bare.steps.find(function (s) { return s.key === "differential_test"; }).id, "S08");
assert.ok(bare.warnings.some(function (w) { return /No reference FASTA/.test(w.text); }));

// ---- a session with analyses
const records = [
  { id: "r1", rnaOne: "RNA_A", rnaOneStart: 1, rnaOneEnd: 20, rnaTwo: "RNA_B", rnaTwoStart: 50, rnaTwoEnd: 70, supportCount: 3, dg: -10.5 },
  { id: "r2", rnaOne: "RNA_A", rnaOneStart: 5, rnaOneEnd: 25, rnaTwo: "RNA_B", rnaTwoStart: 55, rnaTwoEnd: 75, supportCount: 1, dg: null },
  { id: "r3,odd", rnaOne: "RNA_A", rnaOneStart: 9, rnaOneEnd: 30, rnaTwo: "RNA_C", rnaTwoStart: 2, rnaTwoEnd: 22, supportCount: 2, dg: -3 }
];
const full = {
  summary: summary,
  filters: { rna: "RNA_A", minSupport: "1" },
  view: { report: { filteredTotal: 3, filteredSample: records, partnerCounts: [] } },
  reference: { fileName: "ref.fa", fileSize: 99, sha256: "ab".repeat(32), sequenceCount: 2, mapping: { RNA_A: "RNA_A", RNA_B: "RNA_B" } },
  contact: { rnaX: "RNA_A", rnaY: "RNA_B", binSize: 10, matrix: { ready: true, cells: [{ x: 1, y: 51, value: 2, records: 2, support: 4 }, { x: 11, y: 61, value: 1, records: 1, support: 1 }], recordsUsed: 2, binSize: 10, cap: 2 } },
  viewpoint: { rna: "RNA_A", results: { ready: true, rna: "RNA_A", start: 1, end: 30, binSize: 10, max: 0, records: [], bins: [{ start: 1, end: 10, value: 0 }] } },
  comparison: {
    conditionALabel: "Control", conditionBLabel: "Treated", binSize: "10",
    datasets: [{ id: "primary", fileName: "sample.hyb", condition: "A", summary: { validRecords: 120 } }, { id: "d2", fileName: "other.hyb", label: "T1", condition: "B", summary: { validRecords: 80 } }],
    result: { ready: true, groupA: [{}], groupB: [{}], cells: [{ x: 1, y: 51, conditionA: 2, conditionB: 1, effect: 1, similarity: 1, presentDatasets: 2 }], conservedCells: [] },
    deseq: { status: "error", error: "Each condition needs at least two replicates.", minCount: "2", radius: "100", padjCutoff: "0.05" }
  },
  structure: { engine: "vienna", rna: "RNA_A", start: 1, end: 8, result: { engine: "ViennaRNA", sequence: "GGGAAACC", dotBracket: "(((..)))", energy: -1.2, pairs: [[1, 8], [2, 7], [3, 6]], crossingPairs: 0 } },
  evidence: { cards: [{ status: "no_match", include_in_report: false, hit_count: 0, entries: [] }] }
};
const record = R.build(full, { generatedAt: "2026-10-16T00:00:00.000Z", interactionRows: records, interactionTotal: 3 });
assert.equal(status(record, "contact_map"), "ok");
assert.equal(status(record, "viewpoint"), "no_result", "a profile of zeros is a result with nothing in it");
assert.equal(status(record, "differential_test"), "failed");
assert.equal(record.steps.find(function (s) { return s.key === "differential_test"; }).detail, "Each condition needs at least two replicates.");
["R22", "R23", "R26"].forEach(function (id) { assert.equal(result(record, id).value, null, id + " of a failed test is null"); });
assert.equal(status(record, "structure"), "ok");
assert.equal(result(record, "R39").value, 3);
assert.equal(status(record, "literature_search"), "no_result", "a lookup with no match ran and found nothing");
assert.equal(record.literatureSearch.hit_count, null, "no card added: hit_count unknown, not 0");
assert.equal(result(record, "R41").value, 0, "the search ran: zero cards added is a count");

// the Report page shows the record's statuses and values
const html = report.render(full);
const chips = {};
html.replace(/data-step="(S\d\d)" data-status="([a-z_]+)"/g, function (all, id, value) { chips[id] = chips[id] || new Set(); chips[id].add(value); return all; });
record.steps.forEach(function (step) {
  assert.ok(chips[step.id], step.id + " has a status chip on the Report page");
  assert.deepEqual(Array.from(chips[step.id]), [step.status], step.id + " chip = record status");
});
const shown = {};
html.replace(/data-result="(R\d\d)">([^<]*)</g, function (all, id, value) { shown[id] = value; return all; });
record.results.forEach(function (r) {
  assert.equal(shown[r.id], R.formatValue(r), r.id + " on the Report page = record");
});
assert.match(html, /Results at a glance/);
assert.match(html, /data-audience="ai"/);
assert.match(html, /Version<\/strong> <code>HYB2 Web Lite 0\.5\.0 · 0123456789ab/);
assert.match(html, /<strong>Scope<\/strong> Session report/);

// Markdown, summary.json and tables from the same record
const ai = R.files(record, "ai");
const analysis = R.files(record, "analysis");
const byPath = function (list, p) { return list.find(function (f) { return f.path === p; }); };
const md = byPath(ai, "report.md").text;
record.results.forEach(function (r) {
  const row = "| " + r.id + " | " + r.step + " | " + r.label.replace(/\|/g, "\\|") + " | " + R.formatValue(r) + " | ";
  assert.ok(md.indexOf(row) >= 0, r.id + " in report.md: " + row);
});
record.steps.forEach(function (s) { assert.ok(md.indexOf("| " + s.id + " | ") >= 0 && md.indexOf("`" + s.status + "`") >= 0); });
assert.match(md, /not generated by a language model|not by a language model/);
const summaryJson = JSON.parse(byPath(ai, "summary.json").text);
assert.deepEqual(summaryJson.results, JSON.parse(JSON.stringify(record.results)), "summary.json results = record");
assert.deepEqual(summaryJson.steps.map(function (s) { return s.status; }), JSON.parse(JSON.stringify(record.steps.map(function (s) { return s.status; }))));
assert.equal(summaryJson.literature_search.hit_count, null);
record.tables.forEach(function (t) {
  const inAi = !!byPath(ai, t.file);
  const inAnalysis = !!byPath(analysis, t.file);
  assert.equal(inAi, t.audience.indexOf("ai") >= 0, t.file + " in the AI package only when meant for it");
  assert.equal(inAnalysis, t.audience.indexOf("analysis") >= 0, t.file + " in the analysis package only when meant for it");
  const text = (byPath(ai, t.file) || byPath(analysis, t.file)).text;
  const lines = text.trim().split("\n");
  assert.equal(lines.length - 1, t.rowCount, t.file + " rows = rowCount");
  lines.forEach(function (line) { assert.equal(line.split("\t").length, t.columns.length, t.file + " columns per row"); });
});
assert.ok(!byPath(ai, "tables/interactions.tsv") && byPath(analysis, "tables/interactions.tsv"), "all filtered records only in the analysis package");
const interactions = byPath(analysis, "tables/interactions.tsv").text.split("\n");
assert.equal(interactions[3].split("\t")[0], "r3,odd", "a quoted CSV field becomes one TSV cell");
assert.equal(interactions[2].split("\t")[8], "NA", "a missing dG is NA");
const dictionary = JSON.parse(byPath(ai, "data_dictionary.json").text);
assert.equal(dictionary.conventions.missing.tsv, "NA");
dictionary.tables.forEach(function (t) { t.columns.forEach(function (c) { assert.ok(c.description, t.file + " column " + c.name + " is described"); }); });
const provenance = JSON.parse(byPath(ai, "provenance.json").text);
assert.equal(provenance.inputs[1].sha256, "ab".repeat(32));
assert.equal(provenance.inputs[0].sha256Status, "pending", "a digest not yet computed is pending, not empty");
assert.ok(byPath(analysis, "results/structure_prediction.dbn").text.indexOf("(((..)))") > 0);
assert.ok(byPath(ai, "README.md").text.indexOf("never 0") > 0);

// app.js wires the export buttons: every action it names exists in the
// report markup, and the handlers read the clicked node (actionNode)
const appSource = fs.readFileSync(web("app.js"), "utf8");
["report-export", "report-export-close", "report-export-zip", "report-export-file", "report-export-copy", "report-export-html"].forEach(function (action) {
  assert.ok(appSource.indexOf('action === "' + action + '"') > 0, action + " is handled in app.js");
});
const handlers = appSource.slice(appSource.indexOf('if (action === "report-export")'), appSource.indexOf('if (action === "report-export-html")'));
assert.doesNotMatch(handlers, /\belement\./, "the click handler's node is actionNode");
assert.match(fs.readFileSync(web("index.html"), "utf8"), /src="\.\/results\.js"[^]*src="\.\/report-page\.js"/, "index.html loads results.js before report-page.js");

async function main() {
  // manifest digests and the ZIP
  const withManifest = await R.withManifest(ai, record);
  const manifest = JSON.parse(withManifest.find(function (f) { return f.path === "manifest.json"; }).text);
  assert.equal(manifest.files.length, ai.length);
  manifest.files.forEach(function (entry) {
    const file = byPath(ai, entry.path);
    const bytes = Buffer.from(file.text, "utf8");
    assert.equal(entry.bytes, bytes.length, entry.path + " size");
    assert.equal(entry.sha256, crypto.createHash("sha256").update(bytes).digest("hex"), entry.path + " SHA-256");
  });
  const zip = Buffer.from(R.zipBytes(withManifest, "hyb2-result"));
  // read the central directory back
  const endAt = zip.lastIndexOf(Buffer.from([0x50, 0x4b, 0x05, 0x06]));
  const entries = zip.readUInt16LE(endAt + 10);
  let at = zip.readUInt32LE(endAt + 16);
  assert.equal(entries, withManifest.length);
  for (let i = 0; i < entries; i += 1) {
    assert.equal(zip.readUInt32LE(at), 0x02014b50);
    const crc = zip.readUInt32LE(at + 16);
    const size = zip.readUInt32LE(at + 20);
    const nameLength = zip.readUInt16LE(at + 28);
    const local = zip.readUInt32LE(at + 42);
    const name = zip.slice(at + 46, at + 46 + nameLength).toString("utf8");
    const localName = zip.readUInt16LE(local + 26);
    const data = zip.slice(local + 30 + localName, local + 30 + localName + size);
    const file = withManifest.find(function (f) { return "hyb2-result/" + f.path === name; });
    assert.ok(file, name + " is a package file");
    assert.equal(data.toString("utf8"), file.text, name + " content");
    assert.equal(crc, R.crc32(new Uint8Array(data)), name + " CRC-32");
    assert.equal(crc, zlibCrc(data), name + " CRC-32 = zlib");
    at += 46 + nameLength;
  }
  process.stdout.write("verify-results: ok (" + record.steps.length + " steps, " + record.results.length + " results, " + record.tables.length + " tables; ZIP " + zip.length + " bytes)\n");
}

function zlibCrc(buffer) {
  return require("node:zlib").crc32 ? require("node:zlib").crc32(buffer) : R.crc32(new Uint8Array(buffer));
}

main().catch(function (error) {
  process.stderr.write((error && error.stack) || String(error));
  process.stderr.write("\n");
  process.exit(1);
});
