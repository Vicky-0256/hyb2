"use strict";

// Stage an independent desktop release. Never rewrite the web deployment.
const fs = require("node:fs");
const path = require("node:path");
const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "dist");
fs.rmSync(output, { recursive: true, force: true });
fs.mkdirSync(output, { recursive: true });
fs.cpSync(path.join(desktop, "ui"), output, { recursive: true });
fs.cpSync(path.join(desktop, "..", "web"), path.join(output, "analysis"), {
  recursive: true,
  filter: (source) => ![".git", "__pycache__", ".DS_Store"].includes(path.basename(source))
});
for (const entry of ["index.html", "app.js", "style.css", "boot.js", "core.js", "desktop-provider.js", "arc-view.js", "data-page.js", "qc-page.js", "workspace.js", "compare-page.js", "structure-page.js", "analysis/data-provider.js", "analysis/page-data.js", "analysis/index.html", "analysis/app.js", "analysis/desktop-adapter.js", "analysis/structure-controller.js", "analysis/analysis-pages.js", "analysis/parser.worker.js", "analysis/assets/examples/ZIKV_1-10807_example.hyb"]) {
  if (!fs.statSync(path.join(output, entry)).isFile()) throw new Error("Missing desktop asset: " + entry);
}
function availability(files) {
  const missing = files.filter((file) => !fs.existsSync(path.join(output, "analysis", file)));
  return { assetsPresent: missing.length === 0, missing };
}
const pyodide = "vendor/pyodide-cplfold/";
const pythonFiles = ["pyodide.mjs", "pyodide.asm.js", "pyodide.asm.wasm", "python_stdlib.zip", "pyodide-lock.json", "build-manifest.json"].map((file) => pyodide + file);
const pythonErrors = [];
try {
  const build = JSON.parse(fs.readFileSync(path.join(output, "analysis", pyodide, "build-manifest.json"), "utf8"));
  const archive = build.cplfoldArchiveFile;
  if (typeof archive !== "string" || !/^cplfold-python-[a-f0-9]{64}\.zip$/.test(archive)) throw new Error("Invalid CPLfold archive manifest");
  pythonFiles.push(pyodide + archive);
  const lock = JSON.parse(fs.readFileSync(path.join(output, "analysis", pyodide, "pyodide-lock.json"), "utf8"));
  if (!lock.packages.numpy.file_name) throw new Error("NumPy wheel missing from runtime manifest");
  pythonFiles.push(pyodide + lock.packages.numpy.file_name);
} catch (error) { pythonErrors.push(error.message); }
const assets = {
  vienna: availability(["vendor/viennarna/vienna-rna.js", "vendor/viennarna/vienna-rna.wasm"]),
  cplfold: availability(pythonFiles)
};
assets.cplfold.manifestErrors = pythonErrors;
assets.cplfold.assetsPresent = assets.cplfold.assetsPresent && pythonErrors.length === 0;
fs.writeFileSync(path.join(output, "asset-status.js"), "window.HYB2_ASSET_STATUS = " + JSON.stringify(assets) + ";\n");
fs.writeFileSync(path.join(output, "asset-status.json"), JSON.stringify(assets, null, 2) + "\n");
console.log("Staged desktop navigation and local analysis assets in desktop/dist");
console.log(JSON.stringify(assets, null, 2));
