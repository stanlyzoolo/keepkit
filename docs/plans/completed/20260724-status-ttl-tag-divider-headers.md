# Status message auto-expiry + tag-group divider headers

## Overview

Two independent polish items for the `[1]` Tools panel, one branch:

1. **Transient status messages auto-expire.** Today `m.statusMsg` (e.g. `grouped by tag` after a `space` toggle) is cleared only by the blanket reset on the next `tea.KeyMsg` (model.go:767) — until then it hides the hotkey hints bar indefinitely. After this change every *transient* status lives `statusMsgTTL = 2s` and then yields the bar back to the hints automatically; a keypress still clears it immediately. In-flight statuses (`launching <name> in <terminal>…`, `still launching <name>`) keep the current lifetime — they are extinguished by `launchDoneMsg`, not a timer.
2. **Tag-group headers become divider lines.** `tagHeaderLine` currently renders `#<tag>` in `SectionLabelStyle` (`ColorCategory #E8A87C`), which competes with the peach tool-name accent (`ColorPrimary #DA7756`). New look: `─ dev ────────…` across the full panel width — tag name in muted `ColorDim`, rule lines in `ColorBorder` (the panel-frame gray), no hashtag. Group boundaries read geometrically, not by color; the tool names stay the only color accent in the list.

Design approved in brainstorm (variant "divider line" chosen over "quiet label + count" and "minimal recolor"; timer scope "all transient statuses" chosen over "grouping message only").

## Context (from discovery)

- **statusMsg lifecycle**: single field `m.statusMsg` (model.go:153), rendered by `renderStatusBar` (render.go:140) which outranks the hints bar whenever non-empty; blanket reset at model.go:767 on every `tea.KeyMsg`.
- **Transient assignment sites** (all become `setStatus`): model.go:553 (openURL error), :581 (`launchFallbackStatus` auto-fallback), :585 (`launched <name>`), :596 (`not found — is it installed?`), :598 (`exited: …`), :640 (`no known updater…`), :642 (`update detect failed…`), :691 (`update failed — see [3]`), :716 (`updated <name>`), :1187 (`no update available…`), :1216/:1227 (`no repo for…`), :1388/:1401/:1403 (`toggleGroupByTag`); mode.go:187 (track result), :271 (rename error), :398 (`flushPendingLaunch` fallback); commands.go:355 (`no repo to refresh` in `refreshSelectedCmd`).
- **In-flight sites** (stay direct assignments): mode.go:354 (`launching <name> in <terminal>…`), mode.go:339 (`still launching` — reads like a transient rejection but is kept non-expiring deliberately: it reports a launch that is *still in progress* and is overwritten when `launchDoneMsg` lands; letting it expire mid-flight would hide the only indication that the adapter is busy for up to `launchTimeout`).
- **`toggleGroupByTag`** (model.go:1382) is `func (m *Model) toggleGroupByTag()` with no return; sole caller at model.go:1025 (the `space` key branch) — signature grows a `tea.Cmd` return.
- **Tag header**: `tagHeaderLine` (render.go:616) + `flattenLine` + `truncateToWidth` (cell-measured via `lipgloss.Width`); single write site in `buildToolRows` (render.go:686). One header = exactly one screen line — every line-map consumer assumes it.
- **Styles**: `internal/ui/styles.go`; `SectionLabelStyle` has other consumers ([L] overlay, [?] overlay, card section headers) and must stay untouched. The dim `#<tag>` suffix on tag-only search matches is a different element — untouched.
- **Existing tests that break (from plan review)**:
  - `cmd == nil` assertions flip to non-nil once their site returns the expiry tick: `render_test.go:1595` (no-repo `[o]`/`[c]` → converts model.go:1216/:1227), `launch_test.go:432` (`launched` success → :585), `launch_test.go:452` (non-zero exit → :598), `launch_test.go:487` (not-found → :596), `commands_test.go:224` (update failure "must not re-fetch" → :691). Reword intent to "want only the expiry tick", don't just delete.
  - `group_test.go:195-196` asserts the **opposite** of the new behavior: `space` toggle "returned a command, want none (a view toggle fetches nothing)" — the toggle now returns the timer (still no fetch; the comment needs rewording, not just the assertion).
  - Header-text assertions: `group_test.go:136–149` (`wantHeaders` `#cli`/`#scm`/`#untagged`), `:329` (`strings.Count(content, "#CLI") == 1` in `TestGroupingIsCaseInsensitive` — becomes 0, hard fail), `:555–570` (direct `tagHeaderLine` tests).
  - **Vacuous after reformat** (pass silently, stop guarding): `group_test.go:98` (`!Contains("#cli")` — search suppresses headers), `:248-249` (`!Contains("#untagged")` — lone-header refusal; this is a header-absence check, not a statusMsg check), `:332-333` (`!Contains("#cli")` — group not split). Must be rewritten against the new divider format, not left as-is.
  - Toggle statusMsg assertion: `group_test.go:233` (`no tags` explanation).

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - cover both success and error/edge scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `go test -race ./...` after each change (version package has real mutex-guarded state)
- maintain backward compatibility (no meta.yaml / cache format changes here)

## Testing Strategy

- **unit tests**: required for every task; model-package tests run under the `TestMain` config-dir isolation seams (never touch real config)
- **e2e tests**: none in this project; the demo GIFs (`demo-gifs` skill) are regenerated post-merge instead

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

**Change 1 — generation counter + one-shot tick** (standard Bubble Tea idiom):
- `statusSeq int` on `Model`, `statusExpiredMsg{seq int}`, `var statusMsgTTL = 2 * time.Second` — a **var, not const**, mirroring the `launchTimeout` precedent (commands.go:59, shrunk to 50ms in `launch_test.go:131`): `tea.Tick` blocks for the full duration when the Cmd is invoked, so tests must shrink the TTL to inspect the produced `statusExpiredMsg` without a 2s sleep.
- `func (m *Model) setStatus(s string) tea.Cmd`: increments `statusSeq`, sets `m.statusMsg = s`, snapshots `seq := m.statusSeq` into a **local** and returns `tea.Tick(statusMsgTTL, func(time.Time) tea.Msg { return statusExpiredMsg{seq} })`. The closure must capture the local by value and read **no model state**: reading `m.statusSeq` at fire time would both defeat the generation guard and race with `Update`'s mutations under `-race` (the Cmd runs in a goroutine).
- `Update()` handler: `case statusExpiredMsg:` clears `m.statusMsg` only when `msg.seq == m.statusSeq` — a stale timer from a superseded message never kills the newer one. The blanket `tea.KeyMsg` reset stays; a timer arriving after it is a harmless no-op.
- The transient/in-flight split needs no flag: transient sites call `setStatus` (and batch the returned cmd into what they already return); in-flight sites keep plain assignment.

**Change 2 — divider header**: `tagHeaderLine` composes `─ ` + label + ` ` + `─…` to the full width budget `w = max(m.toolsW-1, 1)`. Label is the group's first spelling (or `untagged`), flattened as today, truncated to `max(w-4, 1)` cells; tail rule length is `w − 2 − lipgloss.Width(label) − 1`, clamped to 0. On a panel too narrow to frame (`w < 4`) degrade to the bare truncated label. Two new styles: `TagHeaderStyle` (label, `ColorDim`) and `TagRuleStyle` (rules, `ColorBorder`). Prior art: the brief card's `sectionDivider` (render.go:1188/1195) already draws rule dashes with an inline `Foreground(ui.ColorBorder)` — `TagRuleStyle` is the same color promoted to a named `ui` style; consider pointing `sectionDivider` at it too (optional, no behavior change). `─` (U+2500) is East-Asian Ambiguous — the same accepted class as `⏺`/`↑` and the panel borders; 1 cell in the default runewidth condition.

## Technical Details

- `setStatus` lives in model.go next to the other model helpers; pointer receiver (callable from `Update`'s addressable value receiver).
- `toggleGroupByTag` returns `tea.Cmd`; the `space` case at model.go:1025 returns it. All three of its exits (`no tags to group by`, `grouped by tag`, `flat list`) go through `setStatus`.
- `flushPendingLaunch` (mode.go:398) and `refreshSelectedCmd` (commands.go:355) already return `tea.Cmd` — batch the tick with the existing command via `tea.Batch`.
- The `statusExpiredMsg` case slots into `Update`'s message switch alongside the other internal msgs (before `tea.KeyMsg`); after clearing, plain return — the render pass repaints the bar with hints.
- `tagHeaderLine` renders three separately-styled segments concatenated; tests compare via `stripANSI`. The line is built to exactly `w` cells, so the viewport never truncates it.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, doc updates in this repo
- **Post-Completion** (no checkboxes): demo-GIF regeneration, release notes

## Implementation Steps

### Task 1: Status auto-expiry mechanism (setStatus + statusExpiredMsg)

**Files:**
- Modify: `internal/model/model.go`
- Create: `internal/model/status_test.go`

- [x] add `statusSeq int` field to `Model`, `statusExpiredMsg{seq int}` msg type, `var statusMsgTTL = 2 * time.Second` (var, not const — test seam like `launchTimeout`)
- [x] add `func (m *Model) setStatus(s string) tea.Cmd` (increment seq, set msg, snapshot `seq` into a local, return `tea.Tick` whose closure reads no model state)
- [x] add `case statusExpiredMsg:` to `Update()` — clear `m.statusMsg` only when `msg.seq == m.statusSeq`
- [x] write tests: matching seq clears statusMsg; stale seq (superseded message) leaves it intact
- [x] write test: after expiry `renderStatusBar` shows the hints bar again (statusMsg branch no longer taken)
- [x] run `go test -race ./internal/model/` — must pass before task 2

### Task 2: Convert transient statusMsg sites to setStatus

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/mode.go`
- Modify: `internal/model/commands.go`
- Modify: `internal/model/group_test.go` (toggle call sites; inverted `space` assertion)
- Modify: `internal/model/render_test.go` (`cmd == nil` flips)
- Modify: `internal/model/launch_test.go` (`cmd == nil` flips)
- Modify: `internal/model/commands_test.go` (`cmd == nil` flips)

- [x] change `toggleGroupByTag()` to return `tea.Cmd`; route its three statusMsg exits through `setStatus`; return the cmd from the `space` case (model.go:1025)
- [x] convert model.go transient sites (:553, :581, :585, :596, :598, :640, :642, :691, :716, :1187, :1216, :1227) to `setStatus`, batching the tick into each branch's returned cmd
- [x] convert mode.go:187 (track result), :271 (rename error), :398 (`flushPendingLaunch` — `tea.Batch` with existing cmd) and commands.go:355 (`refreshSelectedCmd` — batch with `fetchInstalledCmd`)
- [x] leave mode.go:354/:339 (`launching…`/`still launching…`) as direct assignments; add a code comment marking them deliberately non-expiring (overwritten by `launchDoneMsg`, must not vanish mid-flight)
- [x] fix the five `cmd == nil` assertions that now receive the expiry tick — reword intent to "want only the expiry tick", verify no fetch/exec rides along: `render_test.go:1595` (no-repo `[o]`/`[c]`), `launch_test.go:432` (launched success), `:452` (non-zero exit), `:487` (not-found), `commands_test.go:224` (update failure must not re-fetch)
- [x] rewrite the inverted toggle assertion `group_test.go:195-196` ("want none — a view toggle fetches nothing" → the toggle now returns the timer, still no fetch; reword the comment too)
- [x] add test: toggle sets `grouped by tag`/`flat list` and returns a cmd producing `statusExpiredMsg` with the current seq (shrink `statusMsgTTL` per the `launchTimeout` pattern, `launch_test.go:131`)
- [x] run `go test -race ./internal/model/` — must pass before task 3

### Task 3: Tag-group headers as divider lines

**Files:**
- Modify: `internal/ui/styles.go`
- Modify: `internal/model/render.go`
- Modify: `internal/model/group_test.go`

- [x] add `TagHeaderStyle` (`Foreground(ColorDim)`) and `TagRuleStyle` (`Foreground(ColorBorder)`) to `internal/ui/styles.go`; `SectionLabelStyle` untouched
- [x] rewrite `tagHeaderLine`: `─ <label> ───…` to exactly `w = max(m.toolsW-1, 1)` cells (label ≤ `max(w-4,1)` cells via `truncateToWidth`, tail clamped to 0, bare-label degradation at `w < 4`); `flattenLine` kept; empty tag → `untagged` (no `#`)
- [x] update existing header assertions in `group_test.go` to the new format via `stripANSI`: `wantHeaders` map (:136–149), `strings.Count(content, "#CLI")` (:329 — hard fail, count becomes 0), direct `tagHeaderLine` tests (:555–570)
- [x] rewrite the three absence-checks that go vacuous after the reformat (they'd pass while guarding nothing): `group_test.go:98` (search suppresses headers), `:248-249` (lone-header refusal), `:332-333` (group not split) — assert against the new divider format (e.g. count `─ cli`, check `untagged` divider)
- [x] write tests: rendered header is exactly `w` cells wide (`lipgloss.Width` on stripped line); long tag and CJK tag truncate by display cells, never exceed `w`
- [x] write tests: narrow panel (`w < 4`) degrades without panic; untagged group renders `─ untagged ───…`; header stays a single line for a tag containing `\n`
- [x] run `go test -race ./internal/model/ ./internal/ui/` — must pass before task 4

### Task 4: Verify acceptance criteria

- [x] verify: `space` toggle shows `grouped by tag`/`flat list`, hints bar returns by itself after ~2s (manual `go run .` check)
- [x] verify: `launching <name>…` does not expire mid-flight; keypress still clears any status instantly
- [x] verify: tag headers render as muted divider lines, tool names remain the only color accent
- [x] run full check matrix (preflight): `go build .`, `go vet ./...`, `go test -race ./...`, `golangci-lint run`

### Task 5: [Final] Update documentation

- [x] update `CLAUDE.md`: tag-header description (`#<tag>`/`SectionLabelStyle` → divider line/`TagHeaderStyle`+`TagRuleStyle`) and the statusMsg lifecycle (blanket reset + TTL expiry, in-flight exceptions)
- [x] run docs-sync check for `README.md` drift on the same points — ⚠️ **deviation**: `ARCHITECTURE.md` **does** exist (the plan wrongly said it did not) and also carried the `#tag` header drift; fixed there too (`ARCHITECTURE.md:137`). README `#tag`/`#untagged` mentions fixed at lines 34, 79, 98–99. Status TTL not surfaced in README/ARCHITECTURE (digest/user docs — no drift to fix)
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- eyeball the tag view in a real terminal at 80×24 and a narrow width (~50 cols): divider color vs. selection bar, no wrap in the tools panel

**External system updates:**
- regenerate README demo GIFs (`demo-gifs` skill) — the hero demo shows the tag-grouped list and the status bar
