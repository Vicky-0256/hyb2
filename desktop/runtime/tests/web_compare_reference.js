// The web Compare page's own code on .hyb files, for test_compare.py:
// parser.worker.js parseRecord, deseq-card.js collectArms, deseq.js binArms /
// countTable / compare, batch-tools.js topRegions / mapBatch / mapFiles and
// analysis-data.js's count table, names.table and metadata exports.
//
//   node web_compare_reference.js request.json   (prints JSON)
//
// request: {samples: [{id, label, fileName, condition: "A"|"B", path, rnaX, rnaY}],
//           labels: {A, B}, options: {minCount, radius, padjCutoff},
//           probability, topMaps: <desktop top_maps.json, optional>}
"use strict";
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const web = path.resolve(__dirname, "..", "..", "..", "web");
globalThis.window = globalThis;
globalThis.self = globalThis;
for (const name of ["deseq.js", "deseq-card.js", "batch-tools.js", "analysis-data.js", "parser.worker.js"]) {
  vm.runInThisContext(fs.readFileSync(path.join(web, name), "utf8"), { filename: name });
}
const D = globalThis.Hyb2Deseq;

// parser.worker.js consumeLine on a whole file
function readArms(file) {
  const arms = { index: [], rnaOne: [], rnaTwo: [], oneStart: [], oneEnd: [], twoStart: [], twoEnd: [] };
  fs.readFileSync(file, "utf8").split(/\r?\n/).forEach(function (raw, k) {
    const line = k === 0 ? raw.replace(/^﻿/, "") : raw;
    if (!line.trim() || /^\s*#/.test(line)) {
      return;
    }
    const parsed = parseRecord(line, k + 1); // eslint-disable-line no-undef
    if (!parsed.ok) {
      return;
    }
    const r = parsed.record;
    arms.index.push(arms.index.length);
    arms.rnaOne.push(r.rnaOne); arms.rnaTwo.push(r.rnaTwo);
    arms.oneStart.push(r.rnaOneStart); arms.oneEnd.push(r.rnaOneEnd);
    arms.twoStart.push(r.rnaTwoStart); arms.twoEnd.push(r.rnaTwoEnd);
  });
  return arms;
}

const request = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const out = {};
const samples = request.samples.map(function (s) {
  const arms = globalThis.Hyb2DeseqCard.collectArms(readArms(s.path), s);
  return Object.assign({}, s, { arms: arms, cells: D.binArms(arms, 10) });
});
out.records = samples.map(function (s) { return s.arms.length / 4; });
out.cells = samples.map(function (s) {
  return Array.from(s.cells.x, function (x, i) { return [x, s.cells.y[i], s.cells.count[i]]; });
});

// the exports, from the web's own writers over the DESeq2 count table
const table = D.countTable(samples.map(function (s) { return s.cells; }));
const comparison = {
  conditionALabel: request.labels.A, conditionBLabel: request.labels.B,
  datasets: samples.map(function (s) { return { id: s.id, condition: s.condition, fileName: s.fileName }; })
};
const cells = [];
// deseq.js keys: xBin * 2^25 + yBin
for (let i = 0; i < table.n; i += 1) {
  const xb = Math.floor(table.key[i] / 33554432);
  const datasets = [];
  samples.forEach(function (s, j) {
    if (table.table[i * table.m + j]) {
      datasets.push({ id: s.id, rawValue: table.table[i * table.m + j] });
    }
  });
  cells.push({ x: xb * 10, y: (table.key[i] - xb * 33554432) * 10, datasets: datasets });
}
out.counts = window.Hyb2Data.comparisonCountMatrixToTsv(comparison, { ready: true, cells: cells });
out.names = window.Hyb2Data.comparisonLegacyNamesToTsv(comparison);
out.metadata = window.Hyb2Data.comparisonSampleMetadataToTsv(comparison);

// the differential test, as the card runs it without a Worker
const result = D.compare(samples.map(function (s) { return { name: s.id, cells: s.cells }; }),
  samples.map(function (s) { return s.condition === "A"; }),
  Object.assign({ alpha: 0.01, binSize: 10 }, request.options));
out.summary = result.summary;
out.interactions = result.interactions;

// Top 10 local maps: the web's cells and cap from the .hyb files
const regions = globalThis.Hyb2Batch.topRegions(result.interactions);
const batch = globalThis.Hyb2Batch.mapBatch(samples.map(function (s) {
  return { id: s.id, label: s.label, condition: s.condition, rnaX: s.rnaX, rnaY: s.rnaY, arms: s.arms };
}), regions, null, request.probability);
out.maps = {
  regions: regions, quantile: batch.quantile, cap: batch.cap,
  panels: batch.panels.map(function (panel) { return panel.samples.map(function (s) { return s.cells; }); })
};

// the web's ZIP files from the desktop's top_maps.json (same cells, same order)
if (request.topMaps) {
  const t = request.topMaps;
  const histogram = new Map(t.histogram.map(function (entry) { return [entry[0], entry[1]]; }));
  const quantile = globalThis.Hyb2Batch.histogramQuantile(histogram, request.probability);
  const desktopBatch = {
    panels: t.regions.map(function (region, k) {
      return { region: region, samples: t.samples.map(function (s, j) {
        return { id: s.id, label: s.label, condition: s.condition, rnaX: s.rna_x, rnaY: s.rna_y, cells: t.panels[k][j] };
      }) };
    }),
    probability: request.probability, quantile: quantile, cap: Math.max(1, Math.ceil(quantile)), binSize: 10
  };
  out.files = globalThis.Hyb2Batch.mapFiles(desktopBatch, { labels: request.labels, datasets: [], summary: { parameters: {} } });
}
process.stdout.write(JSON.stringify(out));
