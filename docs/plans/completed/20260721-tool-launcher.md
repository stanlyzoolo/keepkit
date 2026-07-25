# Tool launcher: run a tracked tool in a new terminal tab

## Overview

- `enter` on the selected tool in `[1] Tools` opens a one-line command prompt (prefilled with the tool name, or the last command run for it this session) and launches the command in a **new terminal tab named after the tool** — the keepkit session keeps running.
- Tab opening is terminal-specific, so a detection chain picks the adapter: tmux → iTerm2 → Terminal.app → kitty → wezterm. Terminals with no scripting API (agterm, ghostty, warp; Windows lands here too — via the auto-fallback, see Technical Details) fall back to `tea.ExecProcess`: the tool runs in the current window, keepkit suspends and resumes when it exits.
- Solves: launching `yazi`/`fzf`/`dive`-style tools straight from the tracker without leaving keepkit or manually opening tabs.

## Context (from discovery)

- New package: `internal/launcher` — bottom of the import graph, architectural mirror of `internal/updater` (pure core + env-facing wrapper, no TUI knowledge).
- Model files touched: `internal/model/model.go` (key dispatch, msg handlers, `lastRun`), `internal/model/mode.go` (`modeRunInput` + handler), `internal/model/commands.go` (launch `tea.Cmd`s), `internal/model/render.go` (status bar branch, hotkeys overlay row).
- Patterns to follow: `modeTrack`/`modeRename` input-mode idiom; `proc.DetachTTY` for probe subprocesses; `updater.Detect`'s pure-core/seam split; rename's stale-state cleanup (`m.helpCache` et al.).
- Base: fresh `origin/main` **including the README help-mode merge (PR #35)** — panel `[3]` defaults to `helpModeReadme`; nothing in this feature touches help modes, but mode.go/model.go line numbers reflect the merged code.
- `enter` is unbound in `modeNormal` (verified: it only appears in `modeSearch` and input-mode handlers).

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - unit tests for new and modified functions, covering success and error scenarios
- **CRITICAL: all tests must pass before starting next task** (`go test -race ./...`) - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- maintain backward compatibility (no meta.yaml schema changes in this feature)

## Testing Strategy

- **unit tests**: required for every task; `go test -race ./...` + `go vet ./...` + `golangci-lint run` green before each task boundary
- **e2e tests**: none in this project (TUI verified by model-level tests, per existing convention)

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep plan in sync with actual work done

## Solution Overview

- `internal/launcher` owns "how do I open a tab here": `planFor(env, command, toolName)` is a pure function over an injected `env func(string) string`, returning `Plan{Argv []string, Fallback bool, Terminal string}`. `Detect(command, toolName)` is the thin `os.Getenv` wrapper.
- Detection priority (first hit wins): `$TMUX` set → tmux (deliberately first: inside tmux `TERM_PROGRAM` names the *outer* terminal, and a tmux window is the correct "tab" there) → `$TERM_PROGRAM == "iTerm.app"` → `$TERM_PROGRAM == "Apple_Terminal"` → `$KITTY_WINDOW_ID` set → `$TERM_PROGRAM == "WezTerm"` → `Plan{Fallback: true}`.
- The user command always executes as `sh -c <cmd>` (`cmd /c` on Windows, fallback path only). For tmux/kitty/wezterm the command travels as an argv element — no escaping. For the two AppleScript paths it is interpolated into the script source — `appleScriptQuote` (backslashes then double quotes) is the single escaping point.
- Model side: `modeRunInput` (new input mode) → on enter, `launcher.Detect` (env-only, safe inside `Update()`); tab path runs the adapter argv as a `tea.Cmd` under `proc.DetachTTY` → `launchDoneMsg{toolName, cmd, err}`; adapter failure (kitty remote control off, Automation permission denied) **auto-falls back** to `tea.ExecProcess` so the tool always launches, with the status bar explaining. Non-zero exit of the tool itself → `statusMsg` only, no `logx` (a tool exiting non-zero is not a keepkit anomaly).

## Technical Details

- `Plan` per terminal (`<cmd>` = raw user command, `<tool>` = tool name):
  - tmux: `["tmux", "new-window", "-n", <tool>, "--", <cmd>]` (tmux runs the string via the user's shell; `--` so a `-`-leading command isn't parsed as a flag — added in review)
  - iTerm2: `["osascript", "-e", <script>]` — script creates a tab in the current window with the default profile, `write text "<escaped cmd>"`, sets the session name to `<escaped tool>`
  - Terminal.app: `["osascript", "-e", "tell application \"Terminal\" to do script \"<escaped cmd>\""]` — opens a **window**, not a tab (tabs are not scriptable without System Events; honest degradation, documented)
  - kitty: `["kitten", "@", "launch", "--type=tab", "--tab-title", <tool>, "sh", "-c", <cmd>]`
  - wezterm: `["wezterm", "cli", "spawn", "--", "sh", "-c", <cmd>]` (tab title left to wezterm defaults; naming needs a second pane-id round-trip — deliberately skipped, YAGNI)
- Model state: `m.runInput textinput.Model` (own field, like `m.search` — not shared with note/tags inputs), `m.lastRun map[string]string` (session-only; rename deletes the old-name entry alongside `helpCache` et al.; untrack leaves it — harmless, session-scoped).
- Messages: `launchDoneMsg{toolName, command string, err error}` (tab path; `command` carried so the error handler can build the fallback without re-reading input state), `execDoneMsg{toolName string, err error}` (ExecProcess callback).
- Fallback exec: `tea.ExecProcess(exec.Command("sh", "-c", cmd), func(err error) tea.Msg { return execDoneMsg{name, err} })` — Bubble Tea handles terminal release/restore. On Windows: `exec.Command("cmd", "/c", cmd)` via a small per-GOOS helper (mirrors `browserCommand`).
- Empty input on enter = cancel (no-op, back to `modeNormal`). Empty tool list: `enter` in `focusTools` is a no-op.
- Launch while `updatingFor != ""` is deliberately NOT blocked — independent concerns (ExecProcess pauses rendering of the live update log; the buffer catches up on resume).
- Windows note: `planFor` is env-only, so WezTerm on native Windows still yields the wezterm tab plan (whose `sh -c` will fail there) — Windows is served by the **auto-fallback** (`launchDoneMsg{err}` → `cmd /c` exec), not by detection short-circuiting. One noisy failed attempt is accepted; a `GOOS` guard in `planFor` is deliberate YAGNI until a Windows user reports it.
- A not-installed tool launches anyway — `sh` reports `command not found` in the tab; keepkit does not pre-check PATH.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, CLAUDE.md — all inside this repo.
- **Post-Completion** (no checkboxes): manual verification in real terminals (agterm fallback, iTerm2 tab, tmux window), since adapters shell out to real terminal APIs that unit tests must not touch.

## Implementation Steps

### Task 1: `internal/launcher` package — detection core and plans

**Files:**
- Create: `internal/launcher/launcher.go`
- Create: `internal/launcher/launcher_test.go`

- [x] create `Plan{Argv []string, Fallback bool, Terminal string}` and pure `planFor(env func(string) string, command, toolName string) Plan` with the priority chain tmux → iTerm.app → Apple_Terminal → kitty → WezTerm → fallback
- [x] implement `appleScriptQuote` (escape `\` then `"`) and build the two osascript plans through it; tmux/kitty/wezterm plans pass command and tool name as argv elements
- [x] add thin `Detect(command, toolName) Plan` wrapper over `os.Getenv`
- [x] write table tests for `planFor`: each terminal env → expected argv; `$TMUX` wins over a simultaneously-set `$TERM_PROGRAM`; empty env → `Fallback: true`; tool names with spaces/unicode stay intact as argv elements
- [x] write table tests for `appleScriptQuote` (quotes, backslashes, combinations) and assert the osascript plan embeds the escaped command
- [x] run `go test -race ./internal/launcher/` - must pass before task 2

### Task 2: `modeRunInput` — key, input mode, lastRun

**Files:**
- Modify: `internal/model/mode.go`
- Modify: `internal/model/model.go`
- Create or modify: `internal/model/mode_test.go`

- [x] add `modeRunInput` to the `inputMode` enum (comment: `enter in focusTools`) and `updateRunInput` handler in mode.go: esc → cancel to `modeNormal`; enter with empty/whitespace input → cancel; enter with text → dispatch launch (Task 3 wires the actual cmd; this task can return a placeholder `nil` cmd)
- [x] add `case modeRunInput: return m.updateRunInput(msg)` to the `switch m.mode` dispatch in model.go (~line 669-686) — without it keystrokes fall through to the normal-mode key map (`t` would open track, `u` untrack)
- [x] add `m.runInput textinput.Model` (init alongside the other inputs in `New`) and `m.lastRun map[string]string`; `enter` in `modeNormal`+`focusTools` (no-op on empty list) opens `modeRunInput` prefilled with `m.lastRun[name]` else the tool name, cursor at end
- [x] on successful dispatch store `m.lastRun[name] = command`; extend rename's stale-state cleanup to delete the old-name `lastRun` entry
- [x] write tests: enter opens prefilled mode (fresh name and lastRun variants) only in focusTools; empty list no-op; esc cancels clean; empty-input enter cancels; rename clears `lastRun`
- [x] run `go test -race ./internal/model/` - must pass before task 3

### Task 3: launch commands — tab path, ExecProcess fallback, auto-fallback on error

**Files:**
- Modify: `internal/model/commands.go`
- Modify: `internal/model/model.go`
- Modify: `internal/model/mode.go`
- Create: `internal/model/launch_test.go`

- [x] add `launchDoneMsg{toolName, command string, err error}` and `execDoneMsg{toolName string, err error}`; `startLaunchCmd(plan, toolName, command)` in commands.go runs `plan.Argv` via `exec.Command` + `proc.DetachTTY` (short timeout, e.g. 10s), emits `launchDoneMsg`, and is wrapped in `safeCmd("startLaunchCmd", …)` like every other cmd constructor in the file
- [x] add pure `shellCommand(goos, cmd string) (string, []string)` helper (`sh -c` / `cmd /c`; truly mirroring `browserCommand`'s goos-parameterized, spawn-free signature so both branches are table-testable) and `execToolCmd(toolName, command)` returning `tea.ExecProcess(exec.Command(shellCommand(runtime.GOOS, command)), …execDoneMsg)`
- [x] wire `updateRunInput`'s enter: `launcher.Detect(command, name)` → fallback plan ⇒ `execToolCmd`; else `startLaunchCmd`; set `statusMsg` (`launched <name>` on `launchDoneMsg` success — mode-neutral wording, since Terminal.app and tmux open a *window*, not a tab)
- [x] handle `launchDoneMsg` with err: `statusMsg` explaining the tab failure + **auto-fallback** by returning `execToolCmd(msg.toolName, msg.command)`; handle `execDoneMsg`: non-zero exit → `statusMsg "<name> exited: <err>"`, no logx
- [x] write tests: fallback-plan vs tab-plan dispatch driven through `t.Setenv` on the real `launcher.Detect` (clear `TMUX`/`TERM_PROGRAM`/`KITTY_WINDOW_ID` → exec path; set `TMUX` → adapter path — `planFor` is unexported and cross-package, so env is the seam); table-test both `shellCommand` branches (`linux`/`darwin` → `sh -c`, `windows` → `cmd /c`); `launchDoneMsg{err}` handler fires fallback and sets statusMsg; `execDoneMsg{err}` sets statusMsg without logging (reuse `logx.SetDirForTesting` no-log assertion idiom)
- [x] run `go test -race ./internal/model/` - must pass before task 4
- ➕ [x] review fixes: auto-fallback gated on `modeNormal` (a delayed `tea.ExecProcess` must not seize the terminal under an open editor/overlay — other modes get a retry hint); `m.launchingFor` one-launch-at-a-time guard + `launching <name> in <terminal>…` dispatch feedback (gives `Plan.Terminal` its production consumer); `launchTimeout` const → var (test seam) with a timeout/KillGroup test; `execDoneCallback` extracted from `execToolCmd` and tested; tmux plan gained `--`; `appleScriptQuote` escapes `\n`/`\r`/`\t`; GOOS skips on unix-binary tests
- ➕ [x] re-check fixes (iteration 2): the gated "retry hint" was dead UI (shadowed by every mode's status-bar branch and wiped by the blanket `statusMsg` reset on the mode-closing keystroke) — replaced with a **deferred fallback**: the gate stores `pendingLaunchName`/`Cmd` and `flushPendingLaunch` (mode.go) dispatches `execToolCmd` directly (no `launcher.Detect` re-run) with a rendered statusMsg on the keystroke that returns the mode to `modeNormal`; every modal return funnels through the flush (mode-dispatch switch + inline `modeSearch`/`modeHelpSearch` exits; `modeTokenInput` waits for the overlay to close); a fresh `modeRunInput` dispatch drops a pending fallback; tests assert *rendered* status-bar visibility, not just the field

### Task 4: status bar, hints, hotkeys overlay

**Files:**
- Modify: `internal/model/render.go`
- Modify: `internal/model/render_test.go`

- [x] add `renderStatusBar` branch for `modeRunInput`: `run <name>: <input>  [enter] run  [esc] cancel` (echo the live input like the other input modes)
- [x] add `[enter] run` to the `focusTools` normal-mode hints bar
- [x] add the `enter — run in tab` row to the `[?]` hotkeys overlay's tools group (desc shortened from "run tool in a new tab": the overlay sat at 75/76 cols and the longer text would break the hard width budget)
- [x] write tests: status bar branch renders name+input+hints; focusTools bar carries `[enter] run`; hotkeys overlay still fits the ≤20-row × ≤76-col budget (existing budget test must stay green; gauge-tier test widths shifted by the 13 cols the new hint cell added)
- [x] run `go test -race ./internal/model/` - must pass before task 5

### Task 5: Verify acceptance criteria

- [x] verify all requirements from Overview are implemented (prompt prefill + lastRun, tab plans per terminal, named tabs where the API allows, auto-fallback, ExecProcess resume)
- [x] verify edge cases: empty list, empty input, launch during update streaming, Windows build (`GOOS=windows go build ./...`)
- [x] run full test suite: `go test -race ./...`
- [x] run `go vet ./...` and `golangci-lint run`

### Task 6: [Final] Update documentation

- [x] update CLAUDE.md: `internal/launcher` row in the package table; `modeRunInput` in the input-modes list; launch flow bullet in the TUI state-machine section; `enter` in the hotkeys/status-bar descriptions (also: `updateRunInput` in the mode.go file-table row, `startLaunchCmd`/`execToolCmd`/`shellCommand` in the commands.go row)
- [x] update README.md hotkeys section if it lists keys (added the `enter` row to the Panel `[1] Tools` table)
- ➕ [x] update ARCHITECTURE.md — missed from this task's original file list, caught in review: mermaid edge `model --> launcher`, `internal/launcher` package-table row, bottom-of-import-graph list, mode enum "13 values" incl. `modeRunInput`, new "Running a tool (`enter`)" section, `execToolCmd` safeCmd-exception note in the logx section
- ➕ [x] README.md Features bullet ("Run tools") + intro capability mention (review)
- [x] move this plan to `docs/plans/completed/` (harness moves it)

## Post-Completion

**Manual verification** (adapters shell out to real terminal APIs that unit tests must not touch):
- in agterm (no adapter): `enter` on `yazi` → runs in the current window, keepkit resumes on exit with no screen corruption
- in iTerm2: new tab opens, named after the tool, command runs; denying the Automation permission triggers the auto-fallback path
- inside tmux: `tmux new-window` named after the tool, even when the outer terminal is iTerm2
- `dive` flow: enter → append an image arg → next launch prefills the previous command

**External system updates**:
- agterm (Swift app, separate project): once it grows a CLI/IPC for "open tab with command", add a matching adapter to `internal/launcher` — the chain is built to take one more entry
