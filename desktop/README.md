# HYB2 Desktop shell

This directory contains the independent Tauri 2 desktop product. It has its
own `ui/` entry and navigation, project lifecycle, capability detection and
native task bridge. The desktop release is not a remote web wrapper: it remains
usable offline and can add native-only workflows without changing the web
deployment. Analysis views can consume the shared data contracts separately.

## Prerequisites

- Rust stable with the Tauri platform prerequisites installed
- Node.js 18 or newer and npm
- Python 3 for the development static server

On Windows, install the Microsoft C++ Build Tools and WebView2 runtime. On
Linux, install the GTK/WebKitGTK development packages required by Tauri 2. See
the [Tauri prerequisites](https://v2.tauri.app/start/prerequisites/) page for
platform-specific packages.

## Run the desktop shell

From this directory:

```bash
npm install
npm run dev
```

`npm run dev` stages the frontend and starts a static server bound to
`127.0.0.1:4173`, then opens the independent desktop entry. The static server
is used only for development; production embeds `desktop/dist` through
`frontendDist`. Run `npm run stage` after changing frontend files during development.

## Build the desktop app

```bash
npm run build
```

This builds the native application bundle under
`src-tauri/target/release/bundle/` without requiring a disk-image tool. To
build platform installers as well, run `npm run build:installers` on the target
operating system. The first shell milestone does not package
Bowtie2, R, ViennaRNA, VARNA, or other native analysis runtimes; those will be
connected through the desktop adapter and task runner in later milestones.

## Configuration notes

- `src-tauri/tauri.conf.json` resolves `frontendDist: "../dist"` relative to
  its directory. Build and dev hooks stage the desktop entry and shared web assets.
- `beforeDevCommand` runs `npm run dev:frontend` from `desktop/`.
- `csp` is a strict policy; Pyodide/WASM require `'wasm-unsafe-eval'` and
  `'unsafe-eval'` in `script-src`.
- `runtime_detect`, `project_create`, and `project_validate` are exposed as
  structured Tauri commands and call the local JSON bridge without a shell.

## Development checks

Run the dependency-free runtime and task-runner checks from the repository
root:

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s desktop/runtime/tests -p 'test_*.py'
python3 -m unittest discover -s desktop/task-runner -p 'test_*.py'
python3 -m desktop.runtime.detector --repo-root . --pretty
```

The runtime detector reports a capability matrix for the first-launch screen.
The task-runner prototype defines the structured process contract that will be
exposed through Tauri IPC once native commands are added.

## Independent desktop bridge

`runtime/bridge.py` is the process boundary for the desktop product. It is
independent from the web deployment and accepts one JSON request per
invocation:

```bash
python3 -m desktop.runtime.bridge <<'JSON'
{"operation":"detect","repo_root":"."}
JSON
python3 -m desktop.runtime.bridge <<'JSON'
{"operation":"create_project","path":"/tmp/demo.hyb2proj","name":"Demo"}
JSON
```

Supported operations are `detect`, `create_project`, `validate_project`, and
the allowlisted `run_task` validation job. A task writes its structured event
log under the project's `runs/<task-id>/events.jsonl`; the desktop task drawer
renders queued, running, succeeded, failed and interrupted states. The current safe
job validates project references; analysis engine jobs will be added only when
their native assets and execution bridges are available.
Project creation makes portable `inputs/`, `runs/`, and `exports/` directories;
it does not claim that Python, NumPy, Numba, or CPLfold are bundled. Those
capabilities are advertised only after the detector finds them.

## Local analysis integration

`scripts/stage-frontend.cjs` copies the independent desktop navigation to
`dist/` and the web product to `dist/analysis/`, preserving relative Worker,
stylesheet and example-file paths. It never modifies the web deployment.
Analysis is loaded directly into the desktop document as an integrated
workspace. It is not an iframe and does not load a remote website. The shared
asset base keeps workers, ViennaRNA, Pyodide and example files inside the
bundled desktop resources.

Staging writes `asset-status.json` and `asset-status.js`. The Analysis section
reports missing ViennaRNA or Pyodide/NumPy/CPLfold runtime files. Presence is
not proof of successful engine startup; native folding flags remain disabled.
HYB import and data visualizations work without those optional folding assets.

### Desktop shell (RAWDATA_APP_PLAN.md section 3.7)

One project, one navigation. Without an open project the app shows a start
screen (open / create / recent). With a project open:

- **Top bar**: project switcher, reference and gene, the global *selection*
  (one sample, or a condition = its replicates merged), a task indicator that
  opens the task drawer (progress from `progress.json` bytes with an ETA,
  cancel, resume), Help (workflow, methods, file format, validation of the
  selection) and Preferences (CPU / memory budget, theme, runtime diagnostics).
- **1 Data** references and samples (`project_op`): import raw files
  (`dialog_pick_files` then `add_sample`, linked in place by default), import a
  sample sheet (dry-run preview, then commit), run / resume samples
  (`disk_estimate` first, then `task_start sample_pipeline`), check inputs
  and relink.
- **2 QC** per-sample counts (`analysis_summary`), replicate correlation,
  and the Overview page of the selection.
- **3 Explore / 6 Report** the web analysis pages mounted with
  `Hyb2Web.mount(host, {shell:false, provider: DesktopProvider})`
  (`ui/desktop-provider.js` maps the DataProvider contract onto
  `analysis_query`).
- **4 Compare** condition A vs B through `analysis_query compare_bins`, with
  Locate / Region / Fold A & B per differential bin.
- **5 Structure** genome and window folds (`task_start structure`), the
  result list (`list_structures`), and an arc viewer that compares two
  structures (`read_structure`).

`ui_state.desktop` keeps the page, sub-tabs, selection, reference/gene and
the compare groups; `ui_state.analysis` keeps the embedded pages' snapshot.
Shell code lives in `ui/` (`boot.js`, `core.js`, `app.js`, one file per page,
`arc-view.js`, `desktop-provider.js`); `scripts/stage-frontend.cjs` checks
that every file is staged.

## Architecture after the 2026-10 fixes

See `../DESKTOP_FIX_PLAN.md` for the full contracts.

- Rust keeps two persistent Python sidecars (`bridge --serve`): `control` for
  project/runtime operations and `compute` for `cplfold_predict`, so the Numba
  JIT stays warm (`NUMBA_CACHE_DIR` is the app cache dir).
- Long jobs use one task API: `task_start` (`kind`: `validate_project` or
  `cplfold`), `task_status`, `task_cancel`, `task_list`. Rust is the only writer
  of `runs/<id>/status.json`; children write `result.json`; finished tasks are
  registered in `project.json`. Tasks left running at exit are `interrupted`.
- `project.json` writes are locked (`project.json.lock`) and atomic.
- `ui_state` is `{desktop: {route}, analysis: {...}}`.
- A strict CSP is set in `tauri.conf.json`; `HYB2_REPOSITORY_ROOT` /
  `HYB2_PYTHON` are honoured in debug builds only.
- Native CPLfold limit: `HYB2_CPLFOLD_MAX_NT` (default 1000); the browser path
  keeps its 500 nt ceiling.
