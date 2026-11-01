#!/usr/bin/env node
// HYB2 Desktop writes the web app's hyb2-result/1.0 packages
// (desktop/app/views/resultpkg.go) from copies of web/results.js's JSON
// Schemas and id catalog in desktop/app/views/resultschema/. This fails when
// the copies and the web differ; regenerate them with --write.
"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

global.window = undefined;
require(path.resolve(__dirname, "../web/results.js"));
const R = globalThis.Hyb2Results;
const dir = path.resolve(__dirname, "../desktop/app/views/resultschema");
const expected = {};
for (const [file, schema] of Object.entries(R.SCHEMAS)) {
  expected[path.basename(file)] = JSON.stringify(schema, null, 2) + "\n";
}
expected["catalog.json"] = JSON.stringify({ schema: R.SCHEMA, status: R.STATUS, steps: R.STEPS, results: R.RESULTS, nature: R.NATURE_TEXT }, null, 2) + "\n";

if (process.argv.includes("--write")) {
  for (const [file, text] of Object.entries(expected)) fs.writeFileSync(path.join(dir, file), text);
  console.log("wrote", Object.keys(expected).length, "files to", dir);
  process.exit(0);
}
assert.deepEqual(fs.readdirSync(dir).sort(), Object.keys(expected).sort(), "resultschema/ holds exactly the web's schemas and catalog");
for (const [file, text] of Object.entries(expected)) {
  assert.equal(fs.readFileSync(path.join(dir, file), "utf8"), text, file + " differs from web/results.js; run with --write");
}
console.log("desktop result schemas match web/results.js (" + Object.keys(expected).length + " files)");
