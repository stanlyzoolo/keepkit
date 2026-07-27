# uv / bun / pnpm update detection

## Overview

Add three package managers — **uv**, **bun**, **pnpm** — to the update-detection
chain in `internal/updater`, so `[u]` can update tools installed through them
(requested by users after the reddit announcement). Along the way this fixes a
real misdetection, empirically verified during plan review:

- **bun** globals resolve through symlinks into
  `$BUN_INSTALL/install/global/node_modules/<pkg>/…`, so today the npm step
  claims them and offers `npm install -g <pkg>` — the wrong manager, which
  would install a duplicate copy under npm's prefix (verified with bun 1.3.14).
- **pnpm** globals are **not** symlinks at all: `$PNPM_HOME/bin/<name>` is a
  cmd-shim **shell script** `EvalSymlinks` cannot resolve (verified with pnpm
  11.17.0), so today they land in `ErrUnknownManager`. The shim's last line is
  machine-readable — `# cmd-shim-target=<abs path to …/node_modules/<pkg>/cli.js>`
  — which is what the pnpm branch parses.

Scope is `internal/updater` only. Installed-version fallbacks in
`internal/version` (the brew-dir / cargo-list idea) are deliberately **not**
added for these managers: their tools are ordinary CLIs that answer
`--version`, and pipx lives without such a fallback too (YAGNI).

## Context (from discovery)

- Files involved: `internal/updater/updater.go`, `internal/updater/updater_test.go`;
  docs: `CLAUDE.md` (chain at lines ~39 and ~95), `README.md` (manager list at
  line ~43, detection bullets at ~140–144, "Updating tools" caveats at
  ~146–161), `ARCHITECTURE.md` (chain verbatim at line ~199).
- Patterns to follow: pure detection core `detectFromPath` (table-tested, no
  subprocesses) + OS-facing `Detect` wrapper that gathers signals
  (`goBuildinfo`, and now the pnpm shim target); `segmentUnder` / `underDir`
  path helpers; `autoPlan` constructor; the `cargoCrateFromList`/`cargoCrate`
  split (pure parser + bounded OS read) for the shim parser; the
  `launcher.planFor` idiom (injected `getenv`) for env-dependent pure cores.
- One production `detectFromPath` call site (updater.go:143) and one test call
  site (the table runner at `updater_test.go:84`).
- Design agreed in a brainstorm session, then revised after a plan-review agent
  empirically validated all three manager layouts (uv 0.11.32, bun 1.3.14,
  pnpm 11.17.0) — the pnpm branch was redesigned around the cmd-shim target
  and the misdetection premise reattributed from pnpm to bun.

## Development Approach

- **testing approach**: Regular (code + table tests in the same task, package convention)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `go test -race ./internal/updater/` after each change; full `go test -race ./...` at the end
- backward compatibility: zero-value `managerDirs{}` + empty shim target
  disable the three new checks; the only intentional behavior changes reachable
  with zero-value inputs are the `npmPackage` service-segment fix and the npm
  step's `.pnpm` gate (both Task 2, both deliberate)

## Testing Strategy

- **unit tests**: table-driven, in `internal/updater/updater_test.go`, no real
  package managers required. Env resolution is tested through the injected
  `getenv`, never by mutating the process environment. Exception: **one**
  unix-gated `Detect`-level fixture test (the `TestDetectResolvesSymlink`
  style, `updater_test.go:277`) pins the `Detect` → `resolveManagerDirs()` →
  `detectFromPath` wiring via `t.Setenv` — that is wiring verification, not
  env-resolution testing, and the package already does this for `PATH`.
- **e2e tests**: none in this project; a live smoke check with the locally
  installed uv is part of acceptance (Task 6).

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep plan in sync with actual work done

## Solution Overview

Extend the pure chain with three checks. New order:

**brew → go buildinfo → cargo → pipx → uv → pnpm → bun → npm**

pnpm/bun sit **before** npm because their paths can contain `node_modules` —
same load-bearing-order principle as the documented "brew before go". uv sits
next to pipx (identical technique). Soft degradation everywhere: an
unresolvable manager dir or unreadable shim just disables its check, and an
exhausted chain still ends in `ErrUnknownManager` with the existing
`update_cmd` hint.

Key design decisions:

- **`managerDirs` struct, not new package seams**: `detectFromPath` gains
  parameters for the new signals. The OS-facing `Detect` fills them via
  `resolveManagerDirs()` = thin wrapper over the pure
  `managerDirsFrom(getenv, home, goos)` (the `launcher.planFor` idiom).
  Empty field = check off, **by explicit `!= ""` guards** — `underDir(p, "")`
  is treacherously true for relative paths, so emptiness must be checked
  before the path helpers, mirroring the existing `if home != ""` guards
  (updater.go:268/277).
- **pnpm via the cmd-shim target**: the pure parser `pnpmShimTarget(contents)`
  extracts the `# cmd-shim-target=` line; `Detect` does a bounded best-effort
  read of the found binary **only when it sits under `pnpmHome`** and passes
  the target into the core as a signal (exactly how `goBuildinfo` rides in).
  The target path contains `node_modules/<pkg>`, so the existing `npmPackage`
  extracts the name.
- **`pnpm add -g` / `bun add -g`, not `update -g`**: `update` respects the
  saved semver range and can silently refuse a major bump, while keepkit
  promises to install the version shown as `latest:`; `add -g` mirrors the
  npm branch's `npm install -g` semantics.
- **`uv tool upgrade <pkg>`**: the canonical uv command.
- **The npm step refuses `.pnpm` store paths**: a path through
  `/node_modules/.pnpm/` outside the detected pnpm dir means "pnpm layout we
  failed to attribute" — offering `npm install -g <pkg>` there would silently
  install a working duplicate under npm's prefix, so the chain falls through
  to `ErrUnknownManager` + the `update_cmd` hint instead (honest degradation).
- **Manager self-updates are out of scope** (`bun upgrade`,
  `pnpm self-update`): the managers' own binaries (no `node_modules` segment,
  no shim target) fall through the chain.
- **Non-goal**: cargo/pipx keep resolving home inside the core via `homeDir()`
  as today; `managerDirs` carries only the three new dirs. No refactor of the
  existing steps.

## Technical Details

Directory resolution (`managerDirsFrom(getenv, home, goos)`):

| Field | Env override | Default |
|---|---|---|
| `uvTools` | `$UV_TOOL_DIR` | `$XDG_DATA_HOME/uv/tools`, else `<home>/.local/share/uv/tools` (macOS and Linux — uv uses XDG paths on macOS too; verified). **Windows: no default**, env-only (uv's Windows layout differs and is unverified — honest degradation) |
| `pnpmHome` | `$PNPM_HOME` | darwin: `<home>/Library/pnpm`; linux: `$XDG_DATA_HOME/pnpm`, else `<home>/.local/share/pnpm`; windows: `%LOCALAPPDATA%\pnpm` |
| `bunInstall` | `$BUN_INSTALL` | `<home>/.bun` (all platforms) |

Empty `home` disables home-based defaults (field stays empty).

Signature: `detectFromPath(realPath, buildinfo, shimTarget string, dirs managerDirs)`
— `shimTarget` is the pnpm cmd-shim target (empty when none), gathered by
`Detect` alongside `buildinfo`. One signature change, done in Task 1;
`shimTarget` is consumed from Task 4.

Detection steps (inside `detectFromPath`, after pipx, before npm; each behind
its explicit `dirs.<field> != ""` guard):

- **uv**: `segmentUnder(realPath, dirs.uvTools)` → `autoPlan("uv", ["uv", "tool", "upgrade", pkg])`.
  Verified layout: `<uvTools>/<pkg>/bin/<bin>` behind a symlink in uv's bin dir.
- **pnpm**: `underDir(realPath, dirs.pnpmHome)` and a package name from
  `npmPackage(shimTarget)` (shim layout, pnpm ≥ 9) **or** `npmPackage(realPath)`
  (legacy symlink layouts) → `autoPlan("pnpm", ["pnpm", "add", "-g", pkg])`;
  no name → fall through, no error (that's the manager's own binary).
  Verified target shape: `<pnpmHome>/global/v11/<hash>/node_modules/<pkg>/cli.js`.
- **bun**: `underDir(realPath, dirs.bunInstall)` and `npmPackage(realPath) != ""`
  → `autoPlan("bun", ["bun", "add", "-g", pkg])`; bun's own binary
  (`<bunInstall>/bin/bun`) has no `node_modules` segment and falls through.
  Verified layout: `$BUN_INSTALL/bin/<bin>` symlink → `../install/global/node_modules/<pkg>/cli.js`.

Shim reading (`Detect` side): only when the found path sits under a non-empty
`pnpmHome`; read capped (e.g. 8 KiB — real shims are ~1.5 KiB); any
read/parse failure yields an empty signal, never an error. Pure parser
`pnpmShimTarget(contents string) string` returns the path after
`# cmd-shim-target=` (last matching line wins), `""` otherwise.

`npmPackage` fix: when the segment after `node_modules` starts with `.`
(`.pnpm`, `.bin` — service dirs, never package names), skip it and keep
scanning for the next `node_modules` occurrence. Scoped packages
(`@scope/name`) keep working. Independently, the **npm step** refuses any
path containing `/node_modules/.pnpm/` (see Solution Overview).

Known limitations (accepted, documented in README by Task 7):

- Windows pnpm/bun/npm bins are `.cmd` shims — pre-existing npm limitation,
  inherited unchanged (the unix shim parser reads `#!/bin/sh` scripts, which
  is what pnpm writes on macOS/Linux).
- Detection is path-convention-based: a future manager layout change makes its
  check miss silently and the chain degrades to `ErrUnknownManager` + hint.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs — all in this repo
- **Post-Completion** (no checkboxes): reddit follow-up, out-of-scope notes

## Implementation Steps

### Task 1: managerDirs resolution and detectFromPath signature

**Files:**
- Modify: `internal/updater/updater.go`
- Modify: `internal/updater/updater_test.go`

- [ ] add `managerDirs` struct + pure `managerDirsFrom(getenv func(string) string, home, goos string) managerDirs` implementing the resolution table above
- [ ] add thin OS wrapper `resolveManagerDirs()` (`os.Getenv`, `homeDir()`, `runtime.GOOS`) and pass its result from `Detect` into `detectFromPath`
- [ ] change the signature to `detectFromPath(realPath, buildinfo, shimTarget string, dirs managerDirs)` (`shimTarget` documented, consumed from Task 4; `Detect` passes `""` until then); add `shimTarget string` + `dirs managerDirs` fields to the existing `TestDetectFromPath` table struct so Tasks 3–5 add **rows**, not new runners
- [ ] write table tests for `managerDirsFrom`: `UV_TOOL_DIR` set / only `XDG_DATA_HOME` / bare home; `PNPM_HOME` set/unset × darwin/linux/windows (incl. `LOCALAPPDATA`); `BUN_INSTALL` set/unset; empty home disables defaults; windows uv default absent — expectations built with `filepath.Join`, never literal `\` (the `configdir.baseFor` table-test precedent; the core runs on the host OS)
- [ ] run `go test -race ./internal/updater/` - must pass before task 2

### Task 2: npmPackage service segments and the npm-step .pnpm gate

**Files:**
- Modify: `internal/updater/updater.go`
- Modify: `internal/updater/updater_test.go`

- [ ] teach `npmPackage` to skip a post-`node_modules` segment starting with `.` and continue scanning for the next `node_modules`
- [ ] gate the npm step: a path containing `/node_modules/.pnpm/` is never claimed as npm — it falls through (ends in `ErrUnknownManager` + `update_cmd` hint unless an earlier step claimed it)
- [ ] write `npmPackage` tests: `.pnpm` virtual-store path yields the real package (not `.pnpm`); scoped package under the `.pnpm` store; `.bin`-only path yields `""`; plain and scoped npm paths unchanged
- [ ] write chain tests: `.pnpm` store path **outside** all manager dirs → `ErrUnknownManager` (not a silent `npm install -g` duplicate); plain `node_modules` path still yields npm
- [ ] run `go test -race ./internal/updater/` - must pass before task 3

### Task 3: uv detection step

**Files:**
- Modify: `internal/updater/updater.go`
- Modify: `internal/updater/updater_test.go`

- [ ] add the uv check after pipx, behind `dirs.uvTools != ""`: `segmentUnder(realPath, dirs.uvTools)` → `autoPlan("uv", []string{"uv", "tool", "upgrade", pkg})`
- [ ] extend the `Plan.Manager` doc comment with `"uv"`, listing managers in chain order: `"brew" | "go" | "cargo" | "pipx" | "uv" | "pnpm" | "bun" | "npm" | "custom"` (pnpm/bun land in Tasks 4–5)
- [ ] write test rows: uv tool path detected (argv `uv tool upgrade <pkg>`); empty `uvTools` with a **relative** path stays undetected (pins the explicit guard — `segmentUnder(p, "")` matches relative paths without it)
- [ ] run `go test -race ./internal/updater/` - must pass before task 4

### Task 4: pnpm detection via the cmd-shim target

**Files:**
- Modify: `internal/updater/updater.go`
- Modify: `internal/updater/updater_test.go`

- [ ] add pure parser `pnpmShimTarget(contents string) string` (`# cmd-shim-target=` line → target path, `""` otherwise)
- [ ] add the bounded best-effort shim read in `Detect`, fired only when the found path sits under a non-empty `pnpmHome`; result rides into `detectFromPath` as `shimTarget`
- [ ] add the pnpm check before bun/npm, behind `dirs.pnpmHome != ""`: `underDir(realPath, dirs.pnpmHome)` + name from `npmPackage(shimTarget)` or `npmPackage(realPath)` → `autoPlan("pnpm", []string{"pnpm", "add", "-g", pkg})`; no name → fall through without error
- [ ] write parser tests: real shim contents (the verified pnpm 11 shape); contents without the marker; empty string; marker with scoped-package target
- [ ] write chain rows: shim-target path (`…/global/v11/<hash>/node_modules/<pkg>/cli.js`) → `pnpm add -g <pkg>` (explicitly not npm); legacy symlink layout via `npmPackage(realPath)`; pnpm's own binary (no target, no `node_modules`) falls through to `ErrUnknownManager`; empty `pnpmHome` guard with a relative path
- [ ] run `go test -race ./internal/updater/` - must pass before task 5

### Task 5: bun detection step and the Detect wiring test

**Files:**
- Modify: `internal/updater/updater.go`
- Modify: `internal/updater/updater_test.go`

- [ ] add the bun check between pnpm and npm, behind `dirs.bunInstall != ""`: `underDir(realPath, dirs.bunInstall)` + `npmPackage(realPath) != ""` → `autoPlan("bun", []string{"bun", "add", "-g", pkg})`
- [ ] write chain rows: bun global under `<bunInstall>/install/global/node_modules/<pkg>` → `bun add -g <pkg>`; scoped package under bun; ordering row (path under `bunInstall` containing `node_modules` resolves to bun, not npm); bun's own binary (`<bunInstall>/bin/bun`) falls through to `ErrUnknownManager`
- [ ] write the unix-gated `Detect`-level wiring fixture test (`TestDetectResolvesSymlink` style): tmp `BUN_INSTALL` with `bin/<name>` → `../install/global/node_modules/<pkg>/cli.js` symlink, `t.Setenv("BUN_INSTALL", …)` + `t.Setenv("PATH", …)`, assert `bun add -g <pkg>` — pins `Detect` → `resolveManagerDirs()` → `detectFromPath(…, dirs)` threading that pure-core rows cannot catch
- [ ] run `go test -race ./internal/updater/` - must pass before task 6

### Task 6: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented (three managers detected, bun-as-npm misdetection fixed, pnpm shim path working, zero-value compatibility)
- [ ] run full test suite: `go test -race ./...`
- [ ] run the preflight skill (build / vet / race tests / golangci-lint — the CI matrix)
- [ ] live smoke check with the locally installed uv, no home pollution: `UV_TOOL_DIR=<tmp>/tools UV_TOOL_BIN_DIR=<tmp>/bin uv tool install cowsay`, then a **temporary env-gated test inside the package** (a scratchpad `go run` main cannot import an internal package) asserting `Detect` yields `uv tool upgrade cowsay`; remove the temporary test and the tmp dirs afterwards

### Task 7: [Final] Update documentation

**Files:**
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`

- [ ] update both CLAUDE.md chain mentions (package-table updater row ~line 39 and the `[u]` section ~line 95): full chain `brew → go → cargo → pipx → uv → pnpm → bun → npm`, the pnpm/bun-before-npm ordering rationale, the shim-target signal, the npm `.pnpm` gate
- [ ] update README.md: manager list (~line 43), detection bullets (~140–144) with uv/pnpm/bun entries and their commands, and the "Updating tools" caveats (~146–161) with the accepted limitations (Windows `.cmd` shims inherited from npm; manager self-updates out of scope)
- [ ] update ARCHITECTURE.md's verbatim chain (~line 199)
- [ ] run the docs-sync skill to catch remaining drift
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification:**
- optionally verify pnpm/bun detection on a machine where they are installed
  as daily drivers; the review agent validated the layouts empirically
  (scratchpad fixtures, pnpm 11.17.0 / bun 1.3.14 / uv 0.11.32), but a live
  confirmation from a pnpm/bun user (e.g. the reddit requesters) closes the loop
- pnpm versions older than the verified 11.x may use different store layouts;
  the legacy `npmPackage(realPath)` branch and the `.pnpm` skip cover the known
  ones, and anything else degrades to `ErrUnknownManager` + hint

**External follow-up:**
- reply in the reddit thread once the release ships
