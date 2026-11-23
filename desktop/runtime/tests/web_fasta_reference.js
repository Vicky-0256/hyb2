// Browser-side half of test_name_matching.py's cross-host FASTA fixtures.
"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
globalThis.window = globalThis;
require("../../../web/support-kernels.js");
require("../../../web/analysis-data.js");
require("../../../web/data-provider.js");
require("../../../web/analysis-pages.js");

const { text, records, names, manual } = JSON.parse(fs.readFileSync(0, "utf8"));
async function read(imported) {
  try {
    const sequences = imported
      ? records.map(([header, sequence]) => ({ id: header.split(/\s+/)[0], header, sequence }))
      : Hyb2Data.parseFasta(text);
    const provider = new Hyb2Provider.BrowserProvider();
    provider.addDataset({ id: "sample", summary: { rnaNames: names }, records: [] });
    const fasta = { sequences };
    // Embedded hosts may supply just records, without a prebuilt mapping.
    if (!imported || manual) fasta.mapping = Hyb2Data.buildFastaMapping(names, sequences, manual);
    provider.setReference(fasta);
    const [{ reference }] = await provider.listDatasets();
    const result = {};
    for (const rna of names) {
      const preview = await provider.getSequence("sample", { rna });
      assert.equal(reference.mapped[rna]?.referenceId, preview?.referenceId);
      assert.equal(reference.mapping[rna] || null, preview?.referenceId || null);
      if (!preview) { result[rna] = null; continue; }
      // Compare whole-record lookup/normalization with the native preview and
      // batch arms. Explicit 1..length regions would request a task window.
      const fold = Hyb2Pages.getStructureSequence({
        reference,
        structure: { source: "reference", engine: "cplfold", cplfoldMode: "single",
          cplfoldEvidence: "none", rna, start: "", end: "" },
        view: { structureEvidence: { guided: false, rnas: names, fasta } }
      });
      assert.equal(fold.error, "");
      assert.deepEqual([fold.sourceRna, fold.sourceStart, fold.sourceEnd], [rna, 1, preview.length]);
      result[rna] = [preview.header, preview.sequence, fold.sequence];
    }
    return result;
  } catch (error) { return { error: error.message }; }
}
Promise.all([read(false), read(true)]).then(results => process.stdout.write(JSON.stringify(results)));
