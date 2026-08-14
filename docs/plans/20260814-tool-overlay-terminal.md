# Tool overlay terminal: run tracked tools in an embedded PTY overlay

## Overview

- `enter` on the selected tool in `[1] tools` keeps opening the `modeRunInput` prompt (prefill: tool name / `lastRun`, editable), but the dispatched command now runs in an **embedded terminal overlay inside keepkit** (~70% of the screen, centered, `PlaceOverlay`-dimmed background) instead of a new terminal tab.
- One path for everything — **no TUI-vs-CLI distinction**: a TUI (vim/yazi/fzf) draws and lives in the overlay with the full keyboard proxied to it; a plain CLI prints and exits, and the overlay stays with the final screen + exit status until dismissed with `esc`.
- While the process is alive **every key (esc included) goes to the tool**; `ctrl+\` is the one reserved chord (kill). After exit, `esc` closes. This is the biggest feature of the project and **replaces the tab launcher entirely** — `internal/launcher`, the auto-fallback and `tea.ExecProcess` are deleted.

## Context (from discovery)

- Stack (researched 2026-08, decided): `github.com/charmbracelet/x/xpty` **v0.1.4** (tagged; unix pty via creack/pty + Windows **ConPTY**) + `github.com/charmbracelet/x/vt` (**untagged — pin the pseudo-version**; VT220+truecolor emulator: `NewEmulator(w, h)`, `Write` pty bytes in, `Render()` ANSI string out, `Resize`, `SendKey`/`SendText`, `InputPipe()`). Rejected: creack/pty direct (no Windows — ConPTY never merged), `taigrr/bubbleterm` (needs Bubble Tea v2; crib its emulator↔Model wiring only).
- x/vt has an open race issue (charmbracelet/x#879, Read/Close) and grapheme-split issue (#935) → **architecture rule: only `Update` touches the emulator's screen state**; the pty is read by one goroutine posting chunks as `tea.Msg`s — the `waitForChunkCmd` pattern from the update streamer. Note the rule sidesteps the *screen-buffer* races; the input path gets its own treatment (Task 1 decides between a key→bytes encoder — preferred, zero extra goroutines — and an `InputPipe` copy goroutine with an explicit lifecycle).
- x/vt master pulls `charmbracelet/ultraviolet` (Bubble Tea v2 rendering core) into the module graph and will likely bump `x/ansi` (pinned v0.11.6 — `ui.StripANSI` delegates to it), `x/cellbuf`, `colorprofile` — all transitive deps of lipgloss/glamour, i.e. **the dependency bump alone can move every render test**. Task 1's gate is therefore the full suite, not the new package.
- Existing anchors (verified by review): `shellCommand` (`internal/model/commands.go:117`) stays — the model builds argv with it and hands it to `term.Start`. `updateRunInput` (`internal/model/mode.go:330`) is the dispatch point to rewire; its refusal path deliberately skips the `lastRun` write (`mode.go:346`). `setStickyStatus`'s only callers are the two launch statuses (`mode.go:353`, `mode.go:371`) — it dies with them, plus `TestInFlightStatusSurvivesStaleExpiry` in `status_test.go`. `mode_test.go`'s `TestRunInputEnterStoresLastRun` asserts the post-enter mode and **will break at the switch** — it is in Task 5's file list.
- keepkit is on bubbletea v1.3.10; x/vt works in a v1 app — `Render()` returns a plain ANSI string.
- Patterns to follow: update streamer (`startUpdateCmd`/`waitForChunkCmd`, elapsed stamped in the cmd, never in `Update`), `modeAPIStatus` modality, `updateOutcomeBlock`'s helpers (`formatElapsed`, `fitCells`, `footerSep` — `render.go:2370`) for the exit line, the `restarter` narrow-interface idiom from main.go for the session seam, `toggleZoom`'s `!m.ready`-first refusal idiom.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes; the old launch path stays intact and green until the single switch-over task (Task 5)
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - unit tests for new and modified functions, covering success and error scenarios
- **CRITICAL: all tests must pass before starting next task** (`go test -race ./...`) - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation** (➕/⚠️ prefixes)
- no meta.yaml schema changes; no config files written by this feature

## Testing Strategy

- **unit tests**: required for every task; the per-task gate is `go build ./...` + `go vet ./...` + `go test -race ./...` + `golangci-lint run` (full tree, not just the touched package — see the dependency-bump note above), plus `GOOS=windows go build ./...` wherever a build tag or dependency changes. Note: every symbol added in Tasks 2–4 is unreferenced from non-test code until Task 5 — **its own task's tests are what keep `unused` quiet**.
- **e2e tests**: none in this project (TUI verified by model-level tests, per existing convention); x/vt being pure Go means even alt-screen/truecolor rendering is assertable in model tests
- **real pty**: only in `internal/term`'s own tests (unix `sh -c echo …`, no network), under `-race`. `internal/term` writes no config and does no `logx` logging (failures ride `Exit.Err`), so it needs no `TestMain` seam — stated in the package doc, not left as an omission.
- **model tests never execute the cmd returned by the run-prompt enter** — it would spawn a real pty; assert on the returned cmd/state only (the `assertOnlyExpiryTick` hazard).

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep plan in sync with actual work done

## Solution Overview

- New package **`internal/term`** — bottom of the import graph, no TUI knowledge (the architectural slot `internal/launcher` vacates): `Session` owns the pty lifecycle. The **model builds argv with its existing `shellCommand`** and passes it to `term.Start` — `internal/term` stays goos-agnostic and needs no duplicate. `Start` creates the `xpty.Pty`, starts **one reader goroutine** pty → chunk channel; the exit (err + elapsed, stamped in the goroutine) lands as the final event on the same channel, then the channel closes.
- **`vt.Emulator` lives on `Model`, not in `internal/term`**: only `Update` writes to its screen state (`termChunkMsg` → `m.termEmu.Write`). Input: `tea.KeyMsg` → key-translation → **preferably an encoder + `Session.Write` (no extra goroutine)**; the `InputPipe()` copy goroutine is the fallback, with an explicit close-order lifecycle (Task 1 decides, Task 3 implements).
- New **`inputMode: modeToolOverlay`** — modal like `modeAPIStatus`: while the process is alive its handler forwards every key to the tool (esc included) and reserves only `ctrl+\` (kill via `Session.Kill`); after `termExitMsg` only `esc` acts (cleanup: close session, `termEmu = nil`, back to `modeNormal`). `overlayVisible()` gains this third member — **mouse gating rides along; `View()`'s fg picker does not** (it is a two-way `if` that must become a `switch m.mode`, done in Task 2 with a placeholder body). One overlay at a time is structural (the mode) — no `launchingFor`-style guard.
- Geometry (owned by `termGeometry`, which must agree with the frame arithmetic, so it lives in Task 4): the **outer block** is 70% of width/height, centered; `Styles.OverlayBorder` is a rounded border **plus `Padding(0, 1)`**, so the emulator body is `emuW = outerW - 4`, `emuH = outerH - 2 - 1 (title row) - 1 (exit row)`. The **exit row is reserved from the start** (blank while running) so the block height never changes and `PlaceOverlay` never re-centers mid-session. Min clamps ≈40×10 on the emulator body; below that — and before the first `WindowSizeMsg` (`!m.ready`, checked first per the `toggleZoom` precedent) — the keypress refuses honestly with a statusMsg. `View()` wraps the composite in `Margin(1, 0)` (the background PlaceOverlay measures is the layout, not the terminal) and PlaceOverlay clips silently past the bottom — the clamps must account for both.

## Technical Details

- `internal/term` API sketch (verified against the real packages as Task 1's **first** item, with a stop condition):
  - `Start(shell string, args []string, w, h int, env []string) (*Session, error)` — env gets `TERM=xterm-256color`, `COLORTERM=truecolor` appended; cwd inherited
  - `Events() <-chan Event` where `Event` is either `Data []byte` or the final `Exit{Err error, Elapsed time.Duration}`
  - `Resize(w, h int) error`, `Write(p []byte) (int, error)`, `Kill()`
  - reader posts bounded chunks (≈32 KiB buffer). **pty EOF semantics**: reading the master after child exit returns `EIO` on Linux, EOF on macOS — both are normal termination, never `Exit.Err`; the verdict comes from `cmd.Wait()`/xpty's wait alone
  - `waitForTermChunkCmd` on the model side drains pending chunks non-blockingly into one msg; **a drain that reaches `Exit` stops there and delivers the accumulated data first** — the exit goes out as the next message (otherwise a short-lived CLI's final screen is lost, defeating the esc-after-exit design)
- Messages: `termStartedMsg{session}` (a start error surfaces as an immediate `termExitMsg`), `termChunkMsg{data []byte}`, `termExitMsg{err, elapsed}`. Elapsed is stamped in the session goroutine, never in `Update`.
- Model state: `m.termSession` (narrow interface, fake-able), `m.termEmu`, `m.termExit *termExitMsg` (nil while running), `m.termW/termH`, `m.termToolName`. **Pre-`termStartedMsg` window**: session and emulator are nil between dispatch and the started msg — keys are dropped, `ctrl+\` is a no-op, the overlay renders a `starting <tool>…` body (the `starting update…` idiom); a panic here would re-panic through `logx.Recover` and crash keepkit, so the guard is structural, not cosmetic.
- Rendering (`render.go`): frame `Styles.OverlayBorder`, title = tool name with a dim `ctrl+\ kill` hint on the right while running; body = `m.termEmu.Render()` (exactly w×h); the reserved bottom row carries the outcome line after exit, built from `updateOutcomeBlock`'s helpers: `✓ exited · <elapsed> · esc close` / `✕ exit <code> · <elapsed> · esc close` / `✕ killed · <elapsed> · esc close` / `✕ failed to start · <reason> · esc close`. No statusMsg on exit — the screen is the answer. **The child's cursor is rendered**: reverse-video the cell at the emulator's cursor position while the process is alive (hidden after exit; honour the emulator's cursor-visibility state if exposed).
- Status bar: `renderStatusBar` gets a `modeToolOverlay` branch (its siblings all have one; without it the bar advertises six dead global keys incl. a `q quit` that cannot fire): while running `<tool> running  ctrl+\ kill`, after exit `<tool> exited  esc close` — within the no-truncate budget (`TestStatusBarNeverWraps`).
- Key translation: rune keys → text input, named keys (arrows, enter, backspace, tab, home/end, pgup/pgdn, F-keys, ctrl+letter) → key events; exact mechanism (encoder vs `SendKey`+pipe) fixed in Task 1. Mouse is **not** proxied in v1 (`SendMouse` exists — YAGNI). Child OSC title/bell events ignored in v1.
- Not-installed tool: `sh` prints `command not found` into the overlay and exits 127 — visible with the ✕ line, no special handling (replaces the old `notFoundExit` mapping).
- Kill path: **never call `proc.DetachTTY` on the pty command** — the pty's own `Setsid`+`Setctty` make the child a session leader, and `DetachTTY` would clobber `Setctty` and break the feature. Unix kill goes to the process group (negative pid, the `KillGroup` idea) off the pty-created session; on the ConPTY path verify `cmd.Process` is populated — if xpty spawns the process itself, kill through xpty's own API and record the Windows degradation beside `restart_windows`'s precedent.
- Quit-while-open is unreachable by design (all keys go to the tool): the way out is quitting the tool (or `ctrl+\`), then `esc`. If keepkit itself dies (SIGTERM/kill), the closing pty master HUPs the child — accepted.
- Windows: works via ConPTY (`xpty`); runtime untested by CI — accepted, the same level as `restart_windows`; the existing `GOOS=windows go build` cross-compile step covers the build.
- Launch during a running update stays allowed (independent concerns; the update log keeps streaming into `[3]` under the dim).
- go.mod: `x/xpty v0.1.4` (tagged) + `x/vt` pinned pseudo-version; upstream issues #879/#935 are tracked limitations.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs — all inside this repo.
- **Post-Completion** (no checkboxes): manual verification in real terminals (real vim/yazi/fzf sessions, Windows smoke test), demo GIF decision — external to unit-testable code.

## Implementation Steps

### Task 1: dependencies + `internal/term` package — pty session with reader goroutine

**Files:**
- Create: `internal/term/session.go`
- Create: `internal/term/session_test.go`
- Create: `docs/research/pty-stack.md`
- Modify: `go.mod`, `go.sum`

- [ ] **first, before writing `Session`**: `go get github.com/charmbracelet/x/xpty@v0.1.4` + `github.com/charmbracelet/x/vt@latest`, pin the vt pseudo-version, and verify the researched API against the real packages (`NewEmulator`/`Render`/`SendKey`/`InputPipe`, cursor accessors, `xpty.NewPty`/`Start`/`Resize`); **check whether x/vt exposes a key→bytes encoder** (`vt.EncodeKey`-style) that would let `Update` translate keys and call `Session.Write` directly — no second goroutine, the Update-only rule becomes literally true. **Stop condition: if `Render() string`, the input mechanism or the cursor API differ materially from the sketch, stop and re-plan Tasks 2–4 before writing code**; record drift with ➕
- [ ] record the transitive bumps the vt/xpty pull causes (`x/ansi`, `x/cellbuf`, `colorprofile`, new `ultraviolet`) with ➕; if any existing render test in `internal/model`/`internal/ui` moves under the bumped deps, fix or pin **before** Task 2 so breakage is attributed to the bump, not to later feature code
- [ ] drop the 2026-08 stack research (candidates, rejection reasons, pinned versions, upstream issues #879/#935) into `docs/research/pty-stack.md` so the pin has a rationale that outlives this plan
- [ ] implement `Session`: `Start(shell, args, w, h, env)` → xpty + command (env: `TERM=xterm-256color`, `COLORTERM=truecolor`; cwd inherited), one reader goroutine → events channel; final `Exit{Err, Elapsed}` (elapsed stamped in the goroutine) then close. **Treat `EIO`/`os.ErrClosed` on the master read as normal EOF** (Linux vs macOS differ) — the verdict comes from the wait, never from the read error
- [ ] implement `Resize`, `Write`, `Kill` — **no `proc.DetachTTY` on the pty command** (it would clobber the pty's `Setctty`); unix kill signals the pty-led process group; verify `cmd.Process` is populated on the ConPTY path, else kill via xpty's own API (record the Windows shape with ➕)
- [ ] state in the package doc: no config paths, no `logx` — failures ride `Exit.Err`; hence no `TestMain` seam
- [ ] write tests (unix): `sh -c 'printf hi'` → data chunk then `Exit{Err: nil}` with elapsed > 0 (this is also the Linux `EIO`-is-not-an-error test); non-zero exit surfaces in `Exit.Err`; `Kill` terminates a `sleep` and the channel closes (no goroutine leak); `Resize` returns no error; all under `-race`
- [ ] write error-case tests: `Start` with a bogus shell → error, no goroutine leak (channel closes)
- [ ] run the full gate: `go build ./...` + `go vet ./...` + `go test -race ./...` + `golangci-lint run` + `GOOS=windows go build ./...` - must pass before task 2

### Task 2: model plumbing — mode, msgs, cmds, emulator state (no dispatch change yet)

**Files:**
- Create: `internal/model/overlay_term.go`
- Modify: `internal/model/model.go`, `internal/model/commands.go`, `internal/model/render.go`
- Modify: `internal/model/mouse_test.go`, `internal/model/zoom_test.go`
- Create: `internal/model/overlay_term_test.go`

- [ ] add `modeToolOverlay` to the `inputMode` enum; extend `overlayVisible()` with it — mouse gating rides along, **`View()`'s fg picker does not**: turn the `if m.mode == modeHotkeys` two-way pick into a `switch m.mode` with a placeholder `modeToolOverlay` body here, so Task 4 only fills the renderer in
- [ ] add model state: `termSession` behind a narrow local interface (the `restarter` idiom — `var _ termSession = (*term.Session)(nil)`), `termEmu`, `termExit`, `termW/termH`, `termToolName`
- [ ] add msgs `termStartedMsg`/`termChunkMsg`/`termExitMsg` and cmds `startTermCmd(shell, args, w, h)` (safeCmd-wrapped) + `waitForTermChunkCmd(session)` with the non-blocking drain that **stops at `Exit` and delivers accumulated data first**; handlers in `Update`: started → create emulator (Update-only rule) + wire the input path per Task 1's decision, chain the wait cmd; chunk → `termEmu.Write` + chain; exit → store `termExit`, stop chaining
- [ ] if Task 1 landed on the `InputPipe` copy goroutine: implement its explicit lifecycle — started exactly once per session when both ends exist; close order on cleanup is `Kill`/wait → close pty → close the emulator's input pipe → goroutine returns; it must never outlive `esc`
- [ ] add `modeToolOverlay` to the modal tables in `mouse_test.go` (mouse no-op set, ~line 201) and `zoom_test.go` (modal `z` guard, ~line 287) — these tables are what "rides along" means
- [ ] write tests: handlers drive a fake session (chunk channel + recorded `Write`/`Kill`/`Resize`); chunk msg reaches a real `vt` emulator and `Render()` shows the bytes (pure Go — no pty); a fake whose channel holds data+data+Exit yields both data chunks before `termExitMsg` and `Render()` shows all of it; exit msg stores status and stops the chain; if the pipe goroutine exists — a `-race` leak test for its close order
- [ ] run the full gate (build + vet + `go test -race ./...` + lint) - must pass before task 3

### Task 3: input routing — `updateToolOverlay` handler and key translation

**Files:**
- Modify: `internal/model/mode.go`, `internal/model/overlay_term.go`
- Modify: `internal/model/overlay_term_test.go`

- [ ] implement key translation `tea.KeyMsg` → tool input (runes → text; named keys/ctrl-chords → key events; mechanism per Task 1's decision), confirmed against the pinned vt version; esc translates and is **sent**, not consumed, while the process runs
- [ ] add `case modeToolOverlay: return m.updateToolOverlay(msg)` to the mode dispatch **unwrapped** — deliberately *not* through `flushPendingLaunch` like its siblings: a deferred exec fallback must not fire `tea.ExecProcess` on the very keystroke that closes the overlay (the wrapper disappears entirely in Task 5)
- [ ] `updateToolOverlay`: process alive → translate & send everything except `ctrl+\` (→ `Session.Kill`, stays in mode until `termExitMsg`); process exited → `esc` cleans up (session close, `termEmu = nil`, `modeNormal`), everything else no-op
- [ ] **pre-`termStartedMsg` nil guard**: keys arriving before the session exists are dropped, `ctrl+\` is a no-op there (a nil deref would re-panic through `logx.Recover` and crash keepkit)
- [ ] write tests: keys (incl. esc, ctrl+c) reach the fake's input while alive; `ctrl+\` triggers `Kill` and mode holds; esc before exit does NOT close; esc after `termExitMsg` cleans and returns to `modeNormal`; non-esc after exit is a no-op; a key in the pre-started state neither panics nor reaches anything
- [ ] run the full gate (build + vet + `go test -race ./...` + lint) - must pass before task 4

### Task 4: rendering — geometry, overlay frame, cursor, exit line, status bar, resize

**Files:**
- Modify: `internal/model/render.go`, `internal/model/overlay_term.go`, `internal/model/model.go` (`applyLayout`/`WindowSizeMsg`)
- Modify: `internal/model/overlay_term_test.go` (and/or `render_test.go`)

- [ ] implement `termGeometry(width, height)` beside the frame arithmetic it must agree with: outer block = 70%×70% centered; body `emuW = outerW - 4` (border + `Padding(0,1)`), `emuH = outerH - 2 - 1 (title) - 1 (reserved exit row)`; account for `View()`'s `Margin(1, 0)` and PlaceOverlay's silent bottom clip; min clamps ≈40×10 body; ok=false below them **and when `!m.ready` (checked first, the `toggleZoom` idiom)**
- [ ] render the overlay in the `View()` switch: `OverlayBorder` frame, title = tool name + dim `ctrl+\ kill` right-hint while running; body = `termEmu.Render()` (pre-start: `starting <tool>…`); the **exit row is reserved from the start** (blank while running) so the block height is constant and the overlay never re-centers at exit
- [ ] render the child's cursor: reverse-video the cell at the emulator's cursor position while the process is alive, hidden after exit; honour the emulator's cursor-visibility state if the API exposes it
- [ ] fill the reserved row after exit via `updateOutcomeBlock`'s helpers (`formatElapsed`/`fitCells`/`footerSep`): `✓ exited · <elapsed> · esc close` / `✕ exit <code> · <elapsed> · esc close` / `✕ killed · <elapsed> · esc close` / `✕ failed to start · <reason> · esc close` (`✓`/`✕` in `Ok`/`Danger`)
- [ ] add the `renderStatusBar` branch for `modeToolOverlay` (its siblings all have one — without it the bar advertises six dead global keys): running → `<tool> running  ctrl+\ kill`, exited → `<tool> exited  esc close`, within the no-truncate budget
- [ ] handle `WindowSizeMsg` while open: recompute `termGeometry`, `termEmu.Resize` + `Session.Resize`; shrinking below the minimum keeps the overlay at the clamped floor (no mid-session kill)
- [ ] write tests: rendered View contains frame, title, hint, `starting…` body pre-start, cursor cell reverse-video while alive, all four outcome lines after their exits; **block height identical before and after `termExitMsg`**; dimmed background; a body line carrying SGR renders at the right visible width inside the frame and no escape leaks past the frame's right edge (assert on `stripANSI(View())` width and the dim margins); status-bar branch in both states; resize propagates to fake session and emulator; refusal on a tiny terminal and on `!m.ready`
- [ ] run the full gate (build + vet + `go test -race ./...` + lint) - must pass before task 5

### Task 5: the switch — dispatch to overlay, delete the tab launcher wholesale

**Files:**
- Modify: `internal/model/mode.go` (`updateRunInput`), `internal/model/model.go`, `internal/model/commands.go`
- Delete: `internal/launcher/` (whole package)
- Modify: `internal/model/mode_test.go`, `internal/model/status_test.go`
- Modify/Delete: `internal/model/launch_test.go`

- [ ] rewire `updateRunInput`'s enter: **refusal first** (`termGeometry` incl. `!m.ready`) → statusMsg (`terminal too small to run a tool`-class wording) with **no `lastRun` write** — a launch that never started is not remembered (today's refusal comment, `mode.go:346`, keeps its meaning); on success: `lastRun` write, `startTermCmd`, `modeToolOverlay`; empty-input-cancels unchanged
- [ ] delete `internal/launcher`; in model: `startLaunchCmd`, `execToolCmd`, `launchDoneMsg`/`execDoneMsg`, `m.launchingFor`, `pendingLaunchName`/`Command` + `flushPendingLaunch` and all its modal-return call sites, `launchTimeout`, `launchFallbackStatus`, `notFoundExit`, the `launching…`/`tab open failed…` wordings
- [ ] delete `setStickyStatus` (its only callers were the two launch statuses) and `TestInFlightStatusSurvivesStaleExpiry`; keep `setStatus`/TTL machinery untouched
- [ ] keep `shellCommand` (now feeds the overlay dispatch; refresh its comment and the cross-reference on `updater.customPlan`)
- [ ] update `mode_test.go`: `TestRunInputEnterStoresLastRun` (post-enter mode → `modeToolOverlay`, `lastRun` still written); audit the other `modeRunInput` tests (`TestRunInputOpensPrefilled`, `TestRunInputEscCancels`, `TestRunInputBlankInputCancels`, `TestRunDuringUpdate`, `TestRunInputKeyGuard`) — prompt-opening ones stay, only dispatch-shape assertions change
- [ ] rewrite `launch_test.go` into overlay-dispatch tests: enter→prompt→overlay opens with prefill variants; empty list no-op; rename still clears `lastRun`; refusal writes no `lastRun`; **no test executes the returned cmd** (it would spawn a real pty)
- [ ] run the full gate + `GOOS=windows go build ./...` - must pass before task 6

### Task 6: surfaces — hotkeys overlay sweep

**Files:**
- Modify: `internal/model/render.go`
- Modify: `internal/model/render_test.go`

- [ ] `[?]` overlay tools group: the row is `{"enter", "run in a tab"}` (`render.go:797`) → `run in overlay`; that is +2 visible cells against the hard ≤76-col framed budget — measure after the change, and if `TestRenderHotkeysSizeBudget` breaks, shorten to `run overlay` rather than dropping a row
- [ ] verify no status-bar or footer branch references the deleted launch statuses; `[1]` footer `enter run` and the `modeRunInput` bar branch stay as-is
- [ ] sweep rendered-surface tests: hotkeys budget across all five self states, footer cells, status bar at the 80×24 baseline stay green with the new wording
- [ ] write/adjust tests for the changed `[?]` row
- [ ] run the full gate (build + vet + `go test -race ./...` + lint) - must pass before task 7

### Task 7: Verify acceptance criteria

- [ ] verify all requirements from Overview: prompt kept, overlay launch, full keyboard proxy while alive, `ctrl+\` kill, esc-after-exit close, reserved exit line, cursor rendered, CLI-and-TUI single path, tab path fully gone
- [ ] verify edge cases: empty list, empty input cancel, launch during a running update, resize during session, tiny-terminal and `!m.ready` refusals, not-installed tool shows 127 in overlay, keys before `termStartedMsg` are safe
- [ ] run full test suite: `go test -race ./...`
- [ ] run `go vet ./...`, `golangci-lint run`, `GOOS=windows go build ./...`, `GOOS=darwin go build ./...`

### Task 8: [Final] Update documentation

**Files:**
- Create: `docs/design/tool-overlay.md`
- Modify: `CLAUDE.md`, `ARCHITECTURE.md`, `README.md`, `docs/design/updating.md`, `internal/updater/updater.go`, `internal/model/model.go`

- [ ] create `docs/design/tool-overlay.md` (fourth deep-design doc: the Update-only emulator rule and why, the input-path decision, esc semantics, the kill chord and the no-`DetachTTY` invariant, geometry incl. the reserved exit row, the pty EOF rule, what was deleted and why) and link it from CLAUDE.md's design-docs table
- [ ] CLAUDE.md: replace the `internal/launcher` package row with `internal/term`; rewrite the **Run (`enter` in `focusTools`)** bullet to the overlay invariant summary; input-modes list (`modeToolOverlay`); `overlayVisible()` description; commands.go/mode.go file-table rows; drop `setStickyStatus` from the status-message lifecycle section; **"Three features" → four** in the design-docs preamble (line ~22) and the "never re-inline these three sections" sentence; fix the misquoted hotkeys row (`run in a tab`, not `run in tab`) while touching it
- [ ] re-anchor the **`planFor` idiom** onto a surviving example (`baseFor`/`shellCommand`): CLAUDE.md lines ~36/42/109, ARCHITECTURE.md ~348, `docs/design/updating.md` (two sites), `internal/updater/updater.go:93`; rewrite the "`execToolCmd` is the one unwrapped cmd" sentence in CLAUDE.md's logx section and ARCHITECTURE.md ~581 (every cmd is safeCmd-wrapped again); drop `launcher` from ARCHITECTURE.md ~65's bottom-leaf list; replace the "`launchDoneMsg`'s mode gate" mirror in `docs/design/updating.md` and the comment at `internal/model/model.go:839` with the surviving reason
- [ ] ARCHITECTURE.md: mermaid edge `model --> term` (drop `--> launcher`), package table row, mode count, rewrite the "Running a tool (`enter`)" section, "Three areas" → four (~20-22)
- [ ] README.md: rewrite the Features bullet at ~64-65 (the adapter list is the whole sentence and it all goes), Usage `enter` line at ~129-131, and the `[1]` hint enumeration at ~153
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification** (real terminals, real tools — unit tests must not touch them):
- vim in the overlay: esc reaches vim (mode switch works), cursor visible and tracking, `:q` exits, ✓ line shows, esc closes, keepkit screen intact
- yazi/fzf session end-to-end; a plain `rg --version` run: output + ✓ line stays until esc (verify the final screen survived the drain)
- `ctrl+\` on a hung `sleep 1000` (✕ killed line); terminal resize mid-vim; launch while an update streams in `[3]`
- Windows smoke test via ConPTY when a Windows machine is available (CI only cross-compiles)
- truecolor/alt-screen fidelity spot-check (btop or similar) — known upstream limits (#935 graphemes) noted, not fixed here

**External follow-ups**:
- demo GIFs (`demo/hero.gif`, `demo/update.gif`) show the old launch flow — decide on regeneration via the demo-gifs skill after merge
- track upstream x/vt issues #879/#935; a future tagged x/vt release replaces the pinned pseudo-version
