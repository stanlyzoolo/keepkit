# Close the update chain: a terminal state for the live log in `[3]`

## Overview

A tool update (`enter` in `[2]`) has no terminal state. After `updateDoneMsg`
(`internal/model/model.go:1157`) the only completion signals are a `statusMsg`
that lives for **one second** (`statusMsgTTL`) and is wiped by any keypress, the
spinner leaving the card title, and the `↑` marker disappearing from the row a
moment later. Nothing is written into the log buffer at all: `updateChunkMsg`
appends only what the command printed, and the final `updateLine{done: true}`
becomes an `updateDoneMsg` whose text goes nowhere.

Four consequences, in the order they hurt:

1. The log ends on the manager's last line (`go: downloading …`) under a frame
   still reading `[3] update`, so the screen is **indistinguishable from "still
   downloading"**. This is the reported bug.
2. With a manager that succeeds silently the buffer stays empty and
   `helpContent()` (`render.go:2234`) keeps printing `starting update…` *after*
   the install finished.
3. Asymmetry inside one pipeline: the self path closes its chain (`selfState =
   selfUpdated` → the persistent `keepkit updated — U restart / X later` banner),
   the tool path has no terminal state whatsoever.
4. "The command finished" ≠ "the tool was updated". `brew upgrade` can exit zero
   having done nothing, and today nobody says so.

The log stays on screen — it is the install record and is wanted on both
outcomes — and gains an explicit terminal block in **two phases**: the fate of
the command immediately, then the **verified** version once the post-update
re-detect lands.

## Context

- `internal/model/model.go` — `updateDoneMsg` handler (self branch at `:1173`,
  tool branch below), `installedMsg` handler (`:857`), `recordUpdateFailure`
  (`:787`), `showsUpdateLog` (`:676`).
- `internal/model/commands.go` — `updateLine` (`:460`), `startUpdateCmd` (`:494`),
  `waitForChunkCmd` (`:542`).
- `internal/model/mode.go` — `updateConfirmUpdate` (`:415`), where a session's
  buffer is reset.
- `internal/model/render.go` — `helpContent` (`:2223`), `renderHelp` (`:1371`),
  `panelFooter` (`:840`), `insetPanelTitle` (`:1457`).
- Design docs that carry the current rationale and will go stale:
  `docs/design/updating.md:13-14`, `docs/design/self-update.md:19`, `CLAUDE.md`
  (the **Update** summary at `:111`, **Panel titles**, and the `logx` site list at
  `:155`).

## Development Approach

- Testing approach: **regular** (code first, then tests) per task, but the two
  pure helpers land with their tables in the same task.
- Every task ends with tests written and the suite green before the next starts:
  `go test -race ./...`.
- `tui-render`'s procedure governs the render tasks: single definition point,
  read the output at 80 columns, mutation-check every new assertion.

## Solution Overview

The outcome lives in **model state, not in log lines**. `helpContent()` renders
the buffer through `wrapText(strings.Join(m.updateLog, "\n"))`, and `wrapText`
rebuilds lines from `strings.Fields` counting runes — it would shred styled text.
So the buffer stays exactly what the command printed, and the renderer draws the
outcome, the same split `changelogRender` already uses (styling lands on whole
finished lines).

Elapsed time is measured **in the command, not in the model**: `time.Now()` inside
`Update()` would make completion non-deterministic in tests, while
`startUpdateCmd` already owns the process. No new time field on `Model`.

Both paths — a tool's update and keepkit's own — record through **one function**,
which is what CLAUDE.md already demands of this spot ("One definition, so the two
paths cannot drift").

## Technical Details

### State

```go
// updateOutcome is the terminal state of the last update session: what the
// command did, and — once the post-update re-detect lands — what actually got
// installed. tool == "" means no session has finished yet. It is model state
// rather than appended log lines because the buffer is wrapped at render time
// (wrapText counts runes) and would shred styled text.
type updateOutcome struct {
	tool, manager string
	elapsed       time.Duration
	err           error
	verified      bool   // phase 2 landed
	was, now      string // installed before / after the update
	nowPresent    bool
}
```

One field on `Model`, next to `updateLog`/`updateLogFor`. The render guard is
`m.updateOutcome.tool == m.updateLogFor`, which covers the self path for free:
`updateConfirmUpdate` sets `updateLogFor = target` unconditionally
(`mode.go:426`).

`was` is captured in the `updateDoneMsg` handler from
`m.versions[msg.tool].Installed` — the merge has not happened yet there, so the
value is guaranteed pre-update.

### Phase 2

In the `installedMsg` handler, guarded by
`m.updateOutcome.tool == msg.toolName && !m.updateOutcome.verified` — one string
compare in a handler that fires for every tool at startup. A failed update never
fires the re-detect, so there is correctly no phase 2 there; an untracked keepkit
has none either (no re-detect), and its restart offer is carried by the banner.

### Text and color

Budget is `helpWrapWidth()`, which is **27** cells at the 80×24 baseline
(`helpW` = 30). **The color carries the verdict, not the whole line**: the manager
name and the seconds are exactly what `theme.go:46` calls `Dim` ("labels,
counters, … never competing"), and green has no business on a timing.

| line | text | cells | color |
|---|---|---|---|
| 1 success | `✓ finished` + ` · go · 12s` | 21 | `Ok` + `Dim` |
| 1 failure | `✕ failed` + ` · brew · 4s` | 20 | `Danger` + `Dim` |
| 1a reason | `  exit status 1` (hanging indent) | 15 | `Text` |
| 2 version moved | `✓ fd  v10.2.0 → v10.3.0` | 23 | `Ok` |
| 2 version did not | `⚠ fd  still v10.2.0` | 19 | `Signal` |
| 2 install broken | `✕ fd  not on PATH` | 17 | `Danger` |
| 2 present, no version | `✓ fd  installed` | 15 | `Ok` |
| 3 way out | `R readme · H help · M man` | 25 | `Accent` + `Dim` |

No new theme role. `Signal` on "the version did not move" is that role's own
definition (`theme.go:27`, "requires action and nothing else"): the command ran,
there is no result, the user decides. `Text` on the reason — the verdict above it
is already red, and the reason is prose to read; a red paragraph out of a
multi-line exec error would be an alarm on top of an alarm.

**The wrap rule splits by the number of runs in a line.** Lines 1 and 3 carry two
colors and are therefore **never** wrapped: they are built from cells and the
cells are dropped from the right — `✓ finished · go · 12s` → `✓ finished · go` →
`✓ finished`. Lines 1a and 2 are single-role, so they take the ordinary path:
build plain → `wrapText` → style the finished lines.

### Title

`insetPanelTitle` measures `[]rune`, not cells, so an East-Asian-Ambiguous `✓`
under `RUNEWIDTH_EASTASIAN=1` would push the top border one cell wide — the
invariant CLAUDE.md's **Panel titles** section protects. Hence words: a live log
stays `[3] update`, a finished one reads `[3] update finished` /
`[3] update failed` (`▸ [3] update finished` = 21 runes of 27). The frame thereby
carries the subject line 1 gave up for brevity — each surface says the part the
other does not. The footer's source cell follows the same `name` variable for
free (`update finished · 2/2`). Inside the viewport `✓`/`⚠`/`✕`/`→` are fine:
decorative class, with `✓ present` / `✕ missing` in the metrics strip and
`v0.3.2 → v1.0.2` in the changelog heading as precedent.

### Deliberate non-goals

- `statusMsg` and its 1s TTL are left alone: the block now carries the outcome
  persistently, and the one-second line stays an extra nudge.
- A **backgrounded** update (selection moved to another tool) still completes
  almost silently — the log does not own `[3]`, leaving the 1s status, the `↑`
  vanishing and the `[1]` counter decrementing. Returning to the tool shows the
  log with its block. Documented, not fixed.

## Implementation Steps

### Task 1: Carry the elapsed time out of the update process

**Files:**
- Modify: `internal/model/commands.go`
- Modify: `internal/model/model.go`
- Modify: `internal/model/commands_test.go`

- [x] add `elapsed time.Duration` to `updateLine` (`commands.go:460`) and to
      `updateDoneMsg` (`model.go:209`)
- [x] in `startUpdateCmd`, stamp `start := time.Now()` before `cmd.Start()` and
      send `updateLine{done: true, err: waitErr, elapsed: time.Since(start)}`
- [x] pass `ul.elapsed` through `waitForChunkCmd` into `updateDoneMsg`; leave the
      early-return error paths (empty argv, `StdoutPipe`, `Start`) at zero
- [x] write a test that a streamed run reports a non-zero elapsed and that an
      empty-argv failure reports zero
- [x] run `go test -race ./internal/model/` — must pass before task 2

### Task 2: Record the outcome from one function on both paths

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/mode.go`
- Modify: `internal/model/update_test.go`
- Modify: `internal/model/commands_test.go`

- [x] add the `updateOutcome` type and the `Model` field beside
      `updateLog`/`updateLogFor`
- [x] turn `recordUpdateFailure` into `recordUpdateOutcome(msg)`: write the
      outcome (including `was` from `m.versions[msg.tool].Installed`), keep the
      same `logx.Errorf` on `msg.err != nil`, repaint `[3]` via
      `setHelpContent()` + `GotoBottom()` when `showsUpdateLog()`
- [x] call it from **both** `updateDoneMsg` branches on **success and failure**
      (today only the failure branch calls it)
- [x] reset the outcome in `updateConfirmUpdate` next to `m.updateLog = nil`
- [x] write tests: outcome recorded on success and on failure, shared with the
      self branch (`startedSelfUpdate` from `selfupdate_test.go`), cleared when a
      second update starts
- [x] run `go test -race ./internal/model/` — must pass before task 3

### Task 3: Fill in the verified version from the re-detect

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/update_test.go`

- [x] in the `installedMsg` handler, under
      `m.updateOutcome.tool == msg.toolName && !m.updateOutcome.verified`, set
      `verified`/`now`/`nowPresent` and repaint `[3]` the way phase 1 does
- [x] write tests: `updateDoneMsg` → `installedMsg` marks the outcome verified;
      an unrelated tool's `installedMsg` does not; a second `installedMsg` does
      not overwrite a verified outcome
- [x] run `go test -race ./internal/model/` — must pass before task 4

### Task 4: Pure helpers for the block's text

**Files:**
- Modify: `internal/model/textutil.go`
- Modify: `internal/model/textutil_test.go`

- [x] add `formatElapsed(d time.Duration) string` → `12s`, `1m3s`, `<1s`, and
      `""` at zero
- [x] add `updateVerifyLine(tool, was, now string, present bool)` returning the
      text and its role for the four phase-2 branches in the table above, with
      versions printed through `version.DisplayVersion`
- [x] write table tests for both, covering all four branches and `was == ""`
- [x] run `go test -race ./internal/model/` — must pass before task 5

### Task 5: Extract the cell-dropping helper from `panelFooter`

**Files:**
- Modify: `internal/model/render.go`
- Modify: `internal/model/render_test.go`

- [x] lift the `fit` closure out of `panelFooter` (`render.go:851`) into a
      package-level `fitCells(cells []string, sep string, inner, reserve int) string`
- [x] call it from `panelFooter` unchanged (`reserve` keeps its meaning)
- [x] write a table test for `fitCells` (drops from the right, respects
      `reserve`, returns `""` when even one cell cannot fit)
- [x] confirm the existing footer tests still pass untouched
- [x] run `go test -race ./internal/model/` — must pass before task 6

### Task 6: Render the terminal block in `[3]`

**Files:**
- Modify: `internal/model/render.go`
- Modify: `internal/model/update_test.go`

- [ ] add `updateOutcomeBlock()`: line 1 from cells via `fitCells` (verdict in
      `Ok`/`Danger`, metadata in `Dim`, dim `footerSep`), the reason line wrapped
      then styled `Text`, line 2 from `updateVerifyLine`, line 3 from
      `m.hint()` cells via `fitCells`
- [ ] append it in `helpContent()` after one blank line, and narrow the
      `starting update…` branch to "buffer empty **and** no outcome"
- [ ] ➕ drop the buffer seeding from `recordUpdateOutcome` (moved here from
      task 2: the seeding and the block are one substitution, and removing it
      earlier would leave `TestUpdateDoneFailureEmptyLogSurfacesError` and
      `TestSelfUpdateDoneFailure` red across four tasks)
- [ ] rewrite those two tests onto the block: the reason is surfaced in `[3]`,
      the buffer itself stays empty
- [ ] write tests: the block renders on success and failure; with an **empty**
      buffer `starting update…` is gone; lines 1 and 3 drop cells on a narrow
      panel and never add a line; the color roles are the ones in the table
      (`forceColorProfile` + `themeSeq`)
- [ ] run `go test -race ./internal/model/` — must pass before task 7

### Task 7: Mark completion in the frame and the footer

**Files:**
- Modify: `internal/model/render.go`
- Modify: `internal/model/update_test.go`

- [ ] in `renderHelp`, resolve `name` to `update` while live and to
      `update finished` / `update failed` once the outcome belongs to this log,
      so the inset title and the footer's source cell follow one variable
- [ ] write a test asserting all three spellings on `renderHelp()` output (with
      the frame, so the title arithmetic is exercised)
- [ ] confirm `render_test.go:3272` and `selfupdate_test.go:1472` — both pin
      `[3] update` for a **live** log — stay green untouched
- [ ] run `go test -race ./internal/model/` — must pass before task 8

### Task 8: Verify acceptance criteria

- [ ] look at it: temporary `internal/model/zz_vis_test.go` dumping
      `renderHelp()` at widths **80** and 120 for all five states (live /
      finished / finished with a moved version / finished with `⚠ still` /
      failed with a reason), then **delete the file**
- [ ] mutation-check every assertion added in tasks 1-7: revert the production
      edit, confirm red, restore — colors and title words especially, that class
      of mutant survived the PR #48 review
- [ ] `go run .` against a tracked tool with a pending update: the block appears,
      the second line lands a moment after the first, and `R`/`H`/`M` from the
      hint line really leave the log
- [ ] run the full matrix via the `preflight` skill

### Task 9: [Final] Update documentation

- [ ] `docs/design/updating.md` — "Live log in `[3]`" and "Spinner + completion"
      (the latter describes both the buffer seeding and the `statusMsg`)
- [ ] `docs/design/self-update.md:19` — "Completion", where the branch is
      described as writing nothing but `recordUpdateFailure`
- [ ] `CLAUDE.md` — the **Update** summary (`:111`, names
      `recordUpdateFailure`), **Panel titles** (`[3] update` is called the only
      override), and the `logx` site list (`:155`)
- [ ] run the `docs-sync` skill to catch what the manual pass missed
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification**
- The failure path is worth one real run (`chmod -w` on the brew prefix, or an
  `update_cmd` pointing at a missing binary) to see the reason line wrap inside
  27 cells.
- A backgrounded update: start one, move the selection away, come back — the
  block must be there, which is what the known limitation above rests on.

**Demos**
- `demo/update.gif` records exactly this flow and its ending changes. Ask about
  the `demo-gifs` skill after the change lands.
