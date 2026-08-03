# Run `update_cmd` through the platform shell (`cmd /c` on Windows)

Fixes [#55](https://github.com/stanlyzoolo/keepkit/issues/55).

## Overview

An explicit `update_cmd` is hardcoded to run through `sh -c`
(`internal/updater/updater.go:241`), so on Windows every custom update command
demands Git Bash even when the command itself is pure Windows
(`winget upgrade --id BurntSushi.ripgrep`). The pain is sharper than it looks:
the detection chain has **no Windows-native manager** (no winget/scoop/choco
step), so anything not installed via go/cargo/npm/pnpm/bun falls through to the
`set update_cmd` hint — on Windows `update_cmd` is the *primary* update path,
and it is the one path that cannot run there out of the box.

keepkit already answers this exact question one layer up: `shellCommand(goos,
cmd)` (`internal/model/commands.go:114`) runs a tracked tool through `cmd /c`
on Windows and `sh -c` elsewhere. Running a tool works without Git Bash;
updating it does not. This plan makes the update path give the same answer.

Issue #55 asks for a configurable `shell:` block instead. Deliberately not
taken (YAGNI): the stated pain — the Git Bash requirement — is fully removed
by the per-GOOS default; `meta.yaml` is a top-level YAML *list* (`[]ToolMeta`,
`internal/loader/meta.go:96`), so a global key is a schema break or a second
config file keepkit doesn't have today; and a PowerShell user can write
`powershell -NoProfile -Command "…"` directly in `update_cmd` — `cmd /c` will
run it. The issue reply (Post-Completion) explains this and invites a follow-up
if a concrete shell-semantics need remains.

## Context

- `internal/updater/updater.go` — the custom-plan branch in `Detect`
  (`:238-244`, the code change); two doc comments that go stale with it: the
  `Plan` doc (`:30`, `["sh", "-c", <cmd>]`) and `Detect`'s resolution-order
  comment (`:227-228`, "run through sh -c").
- `internal/updater/updater_test.go` — `TestDetectUpdateCmdOverride` (`:719`)
  asserts the literal `sh -c` argv; after the change it must expect the host
  GOOS's shell or it fails on a Windows test host.
- `internal/model/commands.go` — `shellCommand` (`:114`), the sibling this
  change deliberately duplicates; gains a cross-reference comment.
- `docs/design/updating.md:11` — "A `update_cmd` in `meta.yaml` always wins
  and runs via `sh -c`" becomes false and needs the per-GOOS wording.
- `README.md:244-245` — "takes precedence over auto-detection and runs via
  `sh -c` (pipes and `&&` are fine)" — the one user-facing doc a Windows user
  reads about this feature; asserts the contract being changed.
- `ARCHITECTURE.md:289-290` — "`update_cmd` from `meta.yaml` always wins and
  runs via `sh -c`" — same drift in the contributor-facing digest.
  (`ARCHITECTURE.md:421` is the *run* path, already worded `sh -c` / `cmd /c`
  — no change.)
- `CLAUDE.md` — deliberately untouched: its updater row never names the shell,
  so the invariant summary does not go stale (the repo rule is to touch it only
  then). The "`sh -c` grandchildren" phrase in `updating.md:12` stays — it
  describes the unix Setsid/KillGroup path and remains accurate.

## Development Approach

- Testing approach: **regular** (code first, then tests) — the pure core lands
  with its table in the same task.
- Every task ends with the suite green before the next starts:
  `go test -race ./...`.
- The duplication question is settled: `customPlan` is a **deliberate sibling**
  of `model`'s `shellCommand`, not a shared abstraction. `model` sits above
  `updater` in the import graph, and the repo's idiom for bottom leaves that
  may not import each other is a documented duplicate (`testBrewPrefix`,
  `isTUITakeover`). Cross-reference comments on both copies guard the drift.
- The `cmd /c` quoting caveat (a command embedding double quotes can misparse —
  Go's generic argv quoting is not cmd.exe-aware) is accepted with
  `shellCommand`'s exact reasoning: the fix, `SysProcAttr.CmdLine`, needs a
  per-GOOS file and a real Windows report to justify it.

## Solution Overview

The custom-plan branch of `Detect` moves into a pure, goos-parameterized core —
the `shellCommand`/`browserCommand`/`planFor` idiom — and `Detect` passes
`runtime.GOOS`:

```go
// customPlan builds the Plan for an explicit update_cmd override: the command
// runs through the platform shell (the user may write pipes or &&) — sh -c
// everywhere except Windows, where cmd /c spares the Git Bash requirement.
// Goos-parameterized so both branches are table-testable — the updater-side
// sibling of model's shellCommand, which may not be imported from here (model
// sits above updater in the import graph); the two must not drift. The cmd /c
// branch shares shellCommand's accepted quoting caveat: Go's generic argv
// quoting is not cmd.exe-aware, so a command embedding double quotes can
// misparse there.
func customPlan(goos, cmd string) Plan {
	shell := []string{"sh", "-c"}
	if goos == "windows" {
		shell = []string{"cmd", "/c"}
	}
	return Plan{
		Manager: "custom",
		Argv:    append(shell, cmd),
		Display: cmd,
	}
}
```

Nothing downstream changes: `startUpdateCmd` runs `plan.Argv` as-is and the
confirm dialog shows `plan.Display` (still the raw command).

Two accepted Windows degradations, stated rather than hidden:

- **Timeout kill orphans the child**: the change makes a shell-wrapped update
  *reachable* on Windows for the first time, and `proc.KillGroup`'s Windows
  branch (`internal/proc/detach_windows.go:22-27`) kills only the direct
  process — a 10-min deadline kill terminates `cmd.exe` and can orphan the
  real updater underneath. Accepted next to the quoting caveat: fixing it
  (Job Objects) needs a real Windows report to justify.
- **Git-Bash migration edge**: a Windows user who *has* Git Bash and wrote an
  sh-specific `update_cmd` (`$(…)`, single quotes, `$VAR`) today runs it via
  `sh`; after the change it runs under `cmd.exe`. `&&` and `|` survive, so the
  breakage is narrow, and the escape hatch is one edit — prefix the command
  with `sh -c "…"` explicitly (`cmd /c` still finds Git Bash's `sh` on PATH).
  The README edit and the #55 reply both carry this note.

## Implementation Steps

### Task 1: Extract `customPlan` and retire the `sh -c` hardcode

**Files:**
- Modify: `internal/updater/updater.go`
- Modify: `internal/updater/updater_test.go`

- [ ] add the pure `customPlan(goos, cmd string) Plan` (comment as drafted in
      Solution Overview) and replace the inline `Plan{…"sh", "-c"…}` literal in
      `Detect` (`updater.go:238-244`) with `customPlan(runtime.GOOS,
      t.UpdateCmd)`
- [ ] update the two stale doc comments: the `Plan` doc (`updater.go:30`) and
      `Detect`'s resolution-order note (`updater.go:227-228`) to the per-GOOS
      wording
- [ ] write `TestCustomPlan` — table over goos: `windows` → `{"cmd", "/c",
      cmd}`, `linux`/`darwin` → `{"sh", "-c", cmd}`, plus an unrecognized goos
      row (`"plan9"`) pinning that `sh -c` is the *default* branch, not two
      named platforms; every row also pins `Manager == "custom"` and
      `Display == cmd` (raw, unquoted)
- [ ] update `TestDetectUpdateCmdOverride` (`updater_test.go:719`): derive the
      expected shell prefix from `runtime.GOOS` (a two-line literal switch in
      the test, not a call to `customPlan` — the expectation must stay
      independent) so the test is green on any host
- [ ] run tests — `go test -race ./internal/updater/` must pass before task 2

### Task 2: Cross-reference the sibling and refresh all four doc surfaces

**Files:**
- Modify: `internal/model/commands.go`
- Modify: `docs/design/updating.md`
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`

- [ ] extend `shellCommand`'s comment (`commands.go:107-113`) with one line
      naming `updater.customPlan` as the deliberate duplicate that must not
      drift (mirror of the reference `customPlan` carries back)
- [ ] update `docs/design/updating.md:11`: "always wins and runs via `sh -c`
      (skips detection entirely)" → per-GOOS wording (`sh -c`; `cmd /c` on
      Windows, so a winget/PowerShell `update_cmd` needs no Git Bash)
- [ ] update `README.md:244-245`: per-GOOS wording plus the two user-facing
      notes — a winget/PowerShell command now works with no Git Bash, and an
      sh-specific command stays reachable by writing `sh -c "…"` explicitly
- [ ] update `ARCHITECTURE.md:289-290` to the same per-GOOS wording
      (`:421`, the run path, is already correct — leave it)
- [ ] verify no other surface asserts the `sh -c` contract for `update_cmd`:
      `grep -rn 'sh -c' --include='*.md' . | grep -v docs/plans/` plus
      `grep -rn '"sh"' internal/` — the four-document contract is
      CLAUDE.md + README.md + ARCHITECTURE.md + docs/design/ (`docs-sync`)
- [ ] run tests — `go test -race ./...` must pass before task 3

### Task 3: Verify acceptance criteria

**Files:** none — verification only (the matrix is the repo's `preflight`
skill; run it by name).

- [ ] `update_cmd` produces `cmd /c` argv on windows and `sh -c` elsewhere,
      Display unchanged — pinned by `TestCustomPlan` +
      `TestDetectUpdateCmdOverride`
- [ ] full suite: `go build ./... && go vet ./... && go test -race ./...`
- [ ] lint: `golangci-lint run`
- [ ] cross-compile check (the CI step that actually compiles the Windows
      branch): `GOOS=windows go build ./... && GOOS=windows go vet ./...`

### Task 4: [Final] Update documentation

**Files:**
- Move: `docs/plans/20260803-update-cmd-windows-shell.md` →
  `docs/plans/completed/`

- [ ] run the repo's `docs-sync` skill — it owns the four-document contract
      (CLAUDE.md, README.md, ARCHITECTURE.md, docs/design/) and is the final
      check that Task 2 left nothing stale; expected outcome: CLAUDE.md needs
      no edit (its updater row names no shell)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Shipping** (external actions, in order):
- Branch off `main`; commit subject is user-facing release copy:
  `fix: run update_cmd through cmd /c on Windows` (lands under *Bug fixes* in
  the release changelog).
- PR in English, no Claude Code footer, body ends with `Closes #55`.
- Comment on #55 to @loderunner84 (in English) before/at PR time: why the
  per-GOOS default instead of a configurable `shell:` block (pain removed
  without new config surface; `meta.yaml` is a top-level list, so a global key
  is a schema break; PowerShell reachable via `update_cmd` itself), the
  Git-Bash migration note (an sh-specific command keeps working by writing
  `sh -c "…"` explicitly — the reply must not read as strictly-better when the
  edge exists), plus a note that winget/scoop detection steps are a possible
  separate direction if demand shows. **Draft is shown to the user before
  posting** — outward-facing action.
- Post-merge cleanup: worktree, local branch, remote branch.

**Manual verification:**
- No Windows runner executes tests (CI cross-compiles only), so the actual
  `winget upgrade …` run stays unverified until a Windows user confirms —
  accepted; the pure table test pins the argv contract both ways.
