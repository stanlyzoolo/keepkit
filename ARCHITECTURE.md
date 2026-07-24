# Architecture

`keeptui` is a terminal TUI tracker for CLI tools built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).
It is a pure TUI: `main.go` is a thin launcher that reads the tracker (`loader.LoadMeta()`),
sets up the error journal (`logx`) and starts the Bubble Tea model (`model.New(meta)`).
The only CLI surface is `--version`/`-V` and `--help`/`-h` (handled in `main.go`
before the TUI starts, so `keeptui` can be probed by version detectors — including
itself); any other argument errors out instead of booting the TUI.

## Package map

```mermaid
graph TD
    main[main.go] --> model
    main --> loader
    main --> logx
    main --> version
    main --> configdir
    model[internal/model] --> loader[internal/loader]
    model --> version[internal/version]
    model --> updater[internal/updater]
    model --> launcher[internal/launcher]
    model --> ui[internal/ui]
    model --> proc[internal/proc]
    model --> logx
    version --> loader
    version --> logx[internal/logx]
    version --> proc
    version --> configdir
    updater --> loader
    updater --> proc
    ui --> loader
    loader --> logx
    loader --> configdir
    logx --> configdir[internal/configdir]
```

| Package | Responsibility |
|---|---|
| `internal/configdir` | Resolve the base user-config dir: `~/.config/keeptui` on macOS/Linux (`$XDG_CONFIG_HOME` else `~/.config`), `%AppData%\keeptui` on Windows. Pure `baseFor(goos, …)` core + `Base()` wrapper; stdlib-only bottom leaf shared by `loader`/`version`/`logx`/`main` |
| `internal/launcher` | Decide how to run a tracked tool in a new terminal tab: pure `planFor(env, command, toolName)` → `Plan{Argv, Fallback, Terminal}`, detection chain tmux → iTerm2 → Terminal.app → kitty → WezTerm → fallback; env-only, no subprocesses |
| `internal/loader` | Tracker persistence (`meta.yaml`), status lifecycle (`active → trying → inactive`, legacy values migrated on read), the one-tag-per-tool invariant (a legacy multi-tag list is truncated to its first entry on read), GitHub ref parsing (`NormalizeRepo`, `ParseToolRef`) |
| `internal/logx` | Session error journal: errors only, one lazily created file per session, imports only the stdlib-only `configdir` leaf. Package-level state — any package can log without threading a logger through |
| `internal/model` | The entire Bubble Tea model: TUI state, key handling, rendering |
| `internal/proc` | `DetachTTY` — run probes without a controlling terminal; `KillGroup` — process-group SIGKILL (plain `Kill` on Windows) |
| `internal/ui` | Lip Gloss styles, `PlaceOverlay`, `StripANSI` |
| `internal/updater` | Detect the package manager that owns an installed binary and produce an update `Plan{Manager, Argv, Display}` |
| `internal/version` | Detect the installed version locally — `InstalledVersion(t) (ver, present)`; GitHub API with a 24-hour cache; semver comparison (`IsNewer`) |

`configdir`, `launcher`, `logx`, `proc`, `ui`, `updater` and `version` sit at the bottom of the import graph:
they know nothing about the TUI (`ui`, `updater` and `version` reach only into
`loader`/`proc`/`logx`). `configdir` is the lowest leaf (stdlib only), shared by
`loader`/`version`/`logx`/`main` for one config-dir resolution. GitHub ref parsing
is owned by `loader` (otherwise a `version ↔ loader` cycle would appear).

The `model` package is split across files within a single package:

| File | Contents |
|---|---|
| `model.go` | The `Model` struct, message types, `New`/`Init`/`Update`, selection and filtering helpers (`selectMeta`, `setFocus`, `searchMatches`, `filteredMeta`, `indexOfMeta`, `setHelpContent`) |
| `mode.go` | The `inputMode` enum and a handler per input mode |
| `commands.go` | All `tea.Cmd` constructors (fetch commands, update streaming) and re-fetch predicates |
| `render.go` | `View`, panel/card/status-bar/gauge/overlay renderers, mouse handling. The two list/card builders return their line index alongside the text: `buildCard` → clickable lines, `buildToolRows` → the tool-index ↔ screen-line maps |
| `readme.go` | `renderReadme` — markdown → ANSI via glamour, with a single-entry render cache |
| `textutil.go` | Pure text helpers (`wrapText`, `stripANSI`, `colorizeHelp`, `parseHelpEntries`, …) |
| `browser.go` | Opening URLs per `GOOS` |

## Data flow

1. `loader.LoadMeta()` reads `~/.config/keeptui/meta.yaml` — the single source of
   tracked tools.
2. `model.New(meta)` builds the model; `Init()` fires the async fetches — results
   arrive as messages and are merged into the state.

Each tool has five data sources, split so local detection never waits on the network:

- **installed** — `fetchInstalledCmd`: a local subprocess (`--version`/`-V`, with brew-directory
  and `cargo install --list` fallbacks that need no subprocess of the tool itself), always
  fired. It reports a version *and* whether the tool is present at all, so the card can tell
  "installed, version unknown" from "not installed";
- **remote** — `fetchRemoteCmd`: a single network pass via `version.GetRepoData`
  (release + repo card + languages), only when `github` is set;
- **changelog** — `fetchChangelogCmd`;
- **help** — `fetchHelpCmd`: `--help`/`-h`/`help` or `man <name>` depending on the panel mode.
  The `--help` probe spawns a subprocess, so it only runs when panel `[3]` actually
  shows help — `Init()` and `autoFetchCmdsForSelected` both skip it in README mode;
- **readme** — `fetchReadmeCmd`: `version.GetReadme`, only when `github` is set. Unlike
  the other four it is **lazy**: `Init()` seeds only the selected tool and
  `autoFetchCmdsForSelected` fires it on a selection move only while
  `helpMode == helpModeReadme`, so the request is spent per *visited* tool rather than
  per tracked tool. The whole `readmeMsg` (content or error) lands in `m.readmeData`, so
  a 404 or a rate limit is a session-cached negative; `[r]` in `[2]` clears the entry,
  and a token added in the `[L]` overlay drops the rate-limited ones so they can retry.
  The entry appears only when the response lands, so an in-flight request is tracked
  separately in `m.readmeLoading` — without it a `j`/`k` bounce back onto the same tool
  would spend a second request inside that window.

Message handlers merge without clobbering (installed never resets latest and vice
versa). On selection change `autoFetchCmdsForSelected()` fills in what's missing —
the pure predicates `needsInstalled`/`needsRemote`/`needsReadme` skip what is already
cached.

### Probe sandbox

A tracked tool may respond to `--help` by booting its own TUI — that must not shred
the `keeptui` screen. The protection has two layers:

1. every probe goes through `proc.DetachTTY` — its own session, no controlling
   terminal: the child's attempt to open `/dev/tty` gets `ENXIO` instead of toggling
   our terminal;
2. output is sanitized before it can reach a viewport: `ui.StripANSI` (the full
   escape-sequence grammar via `x/ansi.Strip`) + `cleanTerminalOutput` (drops stray
   control characters). A capture carrying the alt-screen signature (`ESC[?1049`,
   `isTUITakeover`) is discarded entirely.

A library that probes the terminal counts as the same hazard: `glamour.WithAutoStyle()`
is never used, because its termenv OSC background query reads stdin and races Bubble
Tea's input reader. Dark/light is resolved once at construction via lipgloss's cached
`HasDarkBackground()` (`m.darkBG`) and passed to glamour as a fixed `WithStandardStyle`.
The README body itself is bounded (`readmeMaxBytes`) and sanitized before rendering.

## TUI state machine

Three panels with cycling focus: `[1] Tools` (the list), `[2] Brief` (the card),
`[3] Readme` (the README/`--help`/`man`/update-log view, switched by `r`/`h`/`m` —
`m.helpMode` is global, not per tool, and defaults to the README). Focus moves with `→`/`←`, the digits
`1`/`2`/`3`, or a mouse click; everything goes through `setFocus(f)`, which repaints
the tools list — the only viewport whose content depends on focus.

All modal state is a single field `m.mode inputMode` (13 values: `modeNormal`, `modeSearch`,
`modeHelpSearch`, `modeEditNote`, `modeEditTags`, `modeTrack`, `modeConfirmUntrack`, `modeRename`,
`modeRunInput`, `modeConfirmUpdate`, `modeAPIStatus`, `modeTokenInput`, `modeHotkeys`). Exactly one mode is active at
a time; `Update()` dispatches via `switch m.mode`, so keys that open other modes
structurally cannot fire inside another mode's input.

Key invariants:

- **A single list projection.** Row order (the "has update" group on top, then
  `meta.yaml` order; during search — the name/tag filter; with `space` pressed —
  grouped by tag instead, untagged last, which replaces the update partition rather
  than nesting inside it) lives only in `searchMatches()`. The renderer, the
  selection index and the mouse row mapping all look through it — desync is
  impossible. `meta.yaml` on disk is never reordered. Tag groups are keyed
  case-insensitively, matching the search predicate.
- **The selection is a tool index, never a screen row.** The tag view inserts
  non-selectable divider header rows (`────… <tag> ────…`), so the two units diverge — but only inside the
  maps `buildToolRows()` returns beside the content (`toolLine`, `lineTool`; the
  identity when grouping is off) and their consumers: `syncToolsViewport`, the mouse
  row mapping, and the page/half-page keys, whose step is a count of viewport rows.
  Navigation and every index-writing site keep working in tool indices. The maps are written by `setToolsContent()`, which is therefore the only
  place allowed to repaint the list content.
- **The cursor follows the tool, not the row.** An async version merge — or the
  `space` view toggle — can regroup the list; those paths capture the selected tool's
  name before the change and remap the index afterwards (`indexOfMeta`).
- **Search is a transaction.** `/` remembers `searchPrevName`; `enter` commits the
  selection (focus moves to the card), `esc` rolls the cursor back to the previous tool.
- **Card links are indexed, not parsed.** `buildCard()` returns the card text plus a
  `line → URL` map recorded while writing (line heights vary with wrapping), so a
  click on the `repo:` line or the changelog release URL opens the browser. `handleMouse`
  rebuilds the map per click, which is why it can never describe stale content.
- **A click's X picks the panel, `panelRow` decides whether it is on one at all.**
  The outer margin, the borders and the status bars share the panels' columns; with a
  scrolled viewport an unbounded row would map that chrome onto a list row or a card
  link. Both panels translate through the same helper.
- **`setHelpContent()` is the single recompute point for the help panel.** Entry
  navigation (`j`/`k`, `parseHelpEntries`, the `applySpotlight` spotlight) is
  recomputed only where the visible text actually changed; style-only repaints never
  reset the cursor. In README mode there are no entries — the glamour output is
  already styled, so `j`/`k` scroll and `/` is a no-op.
- **`m.helpCache` is a `map[string][2]string` whose values are indexed by `m.helpMode`.** README content lives in
  a separate map (`m.readmeData`), so every index site is guarded by a README branch
  first — mode `2` would otherwise run off the end of the array.

## Updating a tool (`u`)

`updater.Detect` identifies the manager from the installed binary — the chain is
brew → go → cargo → pipx → npm (order matters: brew before go, so a brew-installed
Go binary is not misrouted to `go install`). `update_cmd` from `meta.yaml` always
wins and runs via `sh -c`. Detection spawns subprocesses, so it runs as a `tea.Cmd`,
never inside `Update()`.

When the tool has no binary of its own on PATH, one fallback runs before
`ErrUnknownManager`: a `Cellar/<name>`/`Caskroom/<name>` directory means brew owns
the name, so the plan is `brew upgrade <name>`. That is the `rust` case — the
formula ships `rustc` and `cargo`, so `LookPath("rust")` can only miss.

Output streaming uses the "channel + re-subscribe" idiom, with no `*tea.Program`: a
goroutine reads the merged stdout+stderr to EOF (`streamLines`, splitting on `\n`
**and** `\r` — brew/npm progress bars collapse into one updating line), then
`cmd.Wait()`, then a final `updateLine{done, err}` item and `close(ch)` — the order
is mandatory, `Wait` before the pipe is drained is forbidden by `os/exec`.
`waitForChunkCmd` does one receive from the channel and re-creates itself. The log
lives in `[3] Update` (a ~500-line buffer); the 10-minute deadline ends with
`proc.KillGroup` on the process group.

## Running a tool (`enter`)

`enter` in `[1] Tools` opens a one-line prompt (prefilled with the last command
dispatched for the tool this session, else the tool name) and launches the
command. `launcher.Detect` picks the path from the environment alone — no
subprocesses, so unlike every probe it is safe inside `Update()`. A tab plan
runs its argv as a `tea.Cmd` through `proc.DetachTTY` with a 10-second ceiling
(`proc.KillGroup` on the process group when it fires — mostly for osascript
blocked on the macOS Automation dialog); a `Fallback` plan — terminals with no
scripting API, and native Windows — runs the command in the current window via
`tea.ExecProcess` (`sh -c` / `cmd /c`): keeptui suspends and resumes when the
tool exits.

An adapter failure **auto-falls back** to `tea.ExecProcess`, so the tool still
launches — but only from `modeNormal`: the result can arrive seconds after
enter, and seizing the terminal under an open editor or overlay would send the
user's keystrokes to the spawned shell. Under any other mode the fallback is
deferred, not dropped: it fires — with a visible status message — on the
keystroke that closes the mode, going straight to the exec fallback (the
failing adapter plan is never re-run). One adapter launch runs at a time (`m.launchingFor`, the
launch twin of `updatingFor`). Working directories differ by path: a tab opens
in the new shell's default cwd, the fallback inherits keeptui's. A non-zero
exit of the tool itself is a status message only — never logged.

## GitHub API

Without a token — 60 requests/hour per IP, with a token — 5000. A tool with `github`
costs 3 requests at startup, plus one lazy request for the README of the tool opened
in panel `[3]` (`GET /repos/{owner}/{repo}/readme` with
`Accept: application/vnd.github.raw+json` — `doGH` only defaults `Accept` when the
caller left it empty). Token: `GITHUB_TOKEN` from the environment always wins over the
`~/.config/keeptui/token` file (`0600`); a token entered in the TUI is validated with a
`/rate_limit` request before being written to disk.

- **`doGH(req)`** — the single auth point: headers, the 5-second client, reading the
  rate-limit headers of every response.
- **The rate-limit snapshot** is updated through `mergeRateObservation`: an
  "optimistic" observation from `/rate_limit` cannot roll back the per-request
  header readings within the same window.
- **`ErrRateLimited`** — a typed error for 403/429 with `X-RateLimit-Remaining: 0`
  from the response's own headers; the card shows "rate limited — press [L]",
  already-loaded data is not erased.
- **The cache** (`cache.json`, 24h TTL): every read-modify-write goes through
  `updateCacheEntry(repo, mutate)` — under a mutex, re-read from disk, merge, write
  back; parallel startup goroutines never clobber each other's repositories. `mutate`
  always mutates a copy of the existing entry instead of building a `CacheEntry{…}`
  literal — a literal silently drops the fields that writer does not know about. The
  README has its own freshness timestamp (`ReadmeCheckedAt`), separate from the
  card's `CheckedAt`, so the two poison-guards stay independent. Force refresh (`r`)
  skips only the freshness check, keeping the merge and the guard against poisoning
  the cache with an empty response.

## Storage

The base dir comes from `configdir.Base()`: `~/.config/keeptui` on macOS and Linux (`$XDG_CONFIG_HOME` else `~/.config`), `%AppData%\keeptui` on Windows. Paths below use the macOS/Linux form.

| Data | Path |
|---|---|
| Tracker metadata | `~/.config/keeptui/meta.yaml` |
| Version + README cache (24h TTL each, separate timestamps) | `~/.config/keeptui/cache.json` |
| GitHub token (`0600`) | `~/.config/keeptui/token` |
| Session error log | `~/.config/keeptui/logs/keeptui-<timestamp>.log` |
| Pre-migration tracker copy (written once, when a load dropped tags) | `~/.config/keeptui/meta.yaml.bak` |

`SaveMeta` writes atomically (temp file + `os.Rename` in the same directory) — a
crash mid-write can never truncate `meta.yaml`.

## Error journal (`logx`)

Errors only; the file is created lazily on the first write — a session with no errors
leaves no file, so the presence of a file is itself the signal. The filename carries a
colon-free zero-padded timestamp: lexicographic order equals chronological order,
which is what `Cleanup()` relies on (the 20 most recent are kept). `logx.Recover` is
hooked deeper than Bubble Tea's own recover (inside `Update`, `View` and every
command via `safeCmd`; `execToolCmd` is the one unwrapped cmd — `tea.ExecProcess`
only constructs the exec message, nothing there can panic): it records the panic
with a stack trace and **re-panics** so
Bubble Tea restores the terminal correctly. The logger's own failures are swallowed
silently.

## Testing

Tests never touch the real config, and the guarantee is per *test binary*: every
package whose tests can reach a writable path installs the redirect in its `TestMain`,
not in individual tests. Three exported seams share one shape —
`Set…ForTesting(dir) (restore func())`, where `restore` reverts to the previous
override, so a per-test override nests inside the package-wide one:
`logx.SetDirForTesting` (session logs), `loader.SetConfigDirForTesting` (`meta.yaml`)
and `version.SetConfigDirForTesting` (`cache.json`, `token`). They are exported
because the package that can reach a file is usually not the one that owns it: a
`model` test driving the tags or track handlers ends up in `loader.SaveMeta`, which
rewrites the tracker wholesale. A `TestConfigDirIsolated` test in each of those
packages fails if the isolation is ever dropped.

Per-test setup still uses the internal seams: `testConfigDir`, `testCacheDir`,
`testTokenDir`, `testAPIBase`, `testBrewPrefix` (one copy in `version`, a second in
`updater` — each private to its package), `updater`'s `testHomeDir`, and
`model`'s `testReadmeStyle` (forces the glamour construction failure so the
plain-text fallback is covered). The races are real (mutexes in `version`, `logx`),
so tests always run with `-race`:

```bash
go test -race ./...
```
