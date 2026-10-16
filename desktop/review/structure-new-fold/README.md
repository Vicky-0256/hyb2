# Structure › New fold: review for biologists

One page, one task: predict the structure of one RNA with CPLfold, using the
chimeric reads of chosen samples as evidence. The arc viewer on the right of
the page is unchanged.

## What changed

| Part | Before | After |
|---|---|---|
| Input | RNA name cut in the select; modes "Whole RNA, pseudoknot-free" / "Windows" without a word on when to use which | Numbered fields (1 RNA to fold, 2 Evidence, 3 What to fold); the full RNA name and length under the select; the samples behind the evidence; the windows parsed as "2 windows: 401 + 601 nt" |
| Advanced settings | Alpha, Beam, Parallel windows next to the required fields, meaning only on hover | Folded under "Advanced settings", each with a sentence and its default, Reset to defaults; the values in use stay visible below ("Evidence weight 0.50 · beam 100 · defaults", amber when changed) |
| Necessary confirmation | One grey line "Estimated: …"; a red evidence warning while Run fold stayed enabled | "Check before running", pinned below the form so it is always in view: what is folded (nt), evidence, time, memory against the task budget, the limits of the result, and "Cannot run yet" with every reason and what to do; Run fold disabled until it can run; Cmd/Ctrl+Enter |
| Run status | "Started. Progress is shown in the list and the task drawer." | In place: waiting to start, reading reads / folding n of N windows / saving, elapsed and time left, Cancel; a second run is refused while one runs |
| Failure | Raw error text in a toast (and sometimes in the list) | What happened in plain words, "Next:" with what to do, the raw error under "Technical details" with the log path |
| Cancel / interrupted | Nothing / "interrupted" chip with Resume | "Nothing was saved, including windows already folded" / "HYB2 Desktop closed while it ran … starts from the beginning" with Run it again |
| Results | "GENE · 1 window" titles running out of the card; evidence and date only | Full RNA name on up to two lines; "1 window · 3,060 nt (3,170–6,229)"; "1,055 base pairs · −452.0 kcal/mol"; count in the title; missing files explained; empty, loading and error states with a next step |

## States checked with synthetic content

`views/structure_states_test.go` renders each state (`TestStructureStateSnapshots`,
also run against the code before the change for the images here) and asserts
what each says and offers (`TestStructureFoldStates`):

- `long-many.png`: a 110-character RNA name, 30 results, one with its files missing
- `empty.png`, `loading.png`, `no-samples.png`
- `queued.png`, `partial.png` (3 of 8 windows, time left), `succeeded.png`
- `failed-memory.png`, `failed-evidence.png`, `cancelled.png`, `interrupted.png`
- `after-partial-dark.png`, `after-failed-memory-dark.png`: dark theme
- memory over the budget, changed advanced settings and a second run while one
  runs: asserted in `TestStructureFoldStates`

Each comparison image shows the page's left column, before | after.

## Verification (2026-10-16, macOS arm64)

- `CGO_ENABLED=0 go test ./...` in `desktop/app`: all packages pass
  (views 92 s), `go vet` for darwin, windows and linux.
- `TestStructureFormPrefillAndRun`: the whole-RNA request, the prefill from
  Explore, evidence mapped to another reference refused with its reason,
  window errors, and the second fold started with Cmd+Enter from the window
  list.
- `TestEndToEnd` (e2e tag, real runtime and CPLfold, 4 small ZIKV samples):
  passes in 103 s; the fold of 3,170–3,700 with AFR evidence runs from the
  pinned Run fold and its status reports "173 base pairs · -65.2 kcal/mol",
  as the viewer (`real-fold-finished.png`).
- Found while verifying: in a 1280 × 860 window the prefilled form put Run
  fold below the visible part of the column (the e2e click missed it), which
  is why the check and Run fold are now pinned; and the first fold after an
  install took 69 s where "about 2 s" was estimated (engine preparation), now
  said on the Time line.

## Not verified

- Keyboard: Cmd+Enter is tested; Tab order through the form and screen-reader
  output were not checked by hand.
- Windows and Linux: compiled and vetted only; the page was not opened there.
- A real fold that fails, is cancelled or is interrupted: those states are
  synthetic (task snapshots); the failure explanations match the error texts
  of `desktop/runtime/structure.py`, except "ran out of memory / killed",
  whose wording from the operating system is assumed.
- Toasts (app-wide) still show the raw error and can cover the pinned panel
  for a few seconds; they are not part of this page.
