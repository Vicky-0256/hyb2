# Desktop verification — 2026-10-14 (MyGo native shell)

Scope: the native desktop app in `desktop/app` (MyGo v0.4.8, Go 1.27.2,
no cgo) over the bundled runtime, replacing the Tauri shell. The earlier
Tauri verification (2026-10-08) is in git history.

## Environment

- macOS 26.6.2, Apple Silicon arm64, 10 cores, 32 GB
- Go 1.27.2 (Homebrew), MyGo v0.4.8 (`app/go.mod`)
- Bundled runtime: CPython 3.11.17 (python-build-standalone), numpy 1.24.4,
  numba 0.58.1, ViennaRNA 2.7.2, Bowtie2 2.5.5

## Checks

| Check | Result |
|---|---|
| `CGO_ENABLED=0 go test ./...` (app, core, views, widgets) | Passed |
| `go vet ./...`, `go tool mygo vet .`, `gofmt -l .` | Clean |
| `GOOS=windows` / `GOOS=linux` `go vet` and `go test -c` | Passed (compile only) |
| core: ported tasks.rs / prefs.rs / main.rs tests, real-bridge task end to end, cancel kills the whole process tree | Passed |
| `integration_test.go`: views on the real core and bridge (create project, add sample, index task, sample becomes selectable) | Passed, 1.2 s |
| `e2e_test.go` (tag `e2e`): ZIKV data through the views — new project, sample sheet import (preview), pipelines for 2 FASTQ subsets + 2 linked .hyb from the Data page after the disk estimate, QC counts, DESeq2 test, contact map, window fold shown in the arc viewer | Passed, 102 s |
| Python runtime and pipeline tests (bundled interpreter, `PYTHONDONTWRITEBYTECODE=1`) | Passed |
| `go tool mygo build -skip-dmg` | `build/darwin-arm64/HYB2 Desktop.app`, 211 MB, no `.pyc` |
| Launch the built app: sidecars start from the bundle, stop on quit | Passed |
| Page snapshots (headless tester PNGs, light and dark) reviewed for every page | Done |

## Not verified here

- Running the app on Windows and Linux (CI workflow `desktop-native.yml`
  covers build, vet and tests there).
- Clicking through the real window on macOS: this host's terminal has no
  screen-recording or accessibility permission, so interaction was verified
  with the headless tester instead.
- Signing and notarization (builds are ad-hoc signed).
