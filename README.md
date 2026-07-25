# keeptui

A terminal TUI tracker for CLI tools: a list of tracked tools, a card with repository
data, versions and notes, the rendered repository README plus built-in `--help` / `man`
viewing, and updating outdated tools or launching any tracked tool right from the
interface. Pure TUI — no
subcommands; the only flags are `--version` and `--help`.

![keeptui — three-panel overview: tracker list, tool card, docs viewer (README / --help / man), live search and the hotkeys overlay](demo/hero.gif)

## Contents

- [Features](#features)
- [Installation](#installation)
- [Usage](#usage)
- [Updating tools](#updating-tools)
- [Updating keeptui itself](#updating-keeptui-itself)
- [GitHub API and token](#github-api-and-token)
- [Data storage](#data-storage)
- [Architecture](#architecture)
- [Stack](#stack)
- [Contributing](#contributing)
- [License](#license)

## Features

- **Three panels**: tools (the tracker list), brief (the tool card), docs (README / `--help` / `man`)
- **README first** — panel `[3]` opens on the repository README, rendered right in the terminal; `h` / `m` / `r` switch between `--help`, `man` and the README. A tool that is tracked but not installed still has a full panel — exactly the case where docs matter most
- **Tool card** — repository, stars, languages, installed and latest version with release date, status, note and tags
- **Versions** — the installed version is detected locally, the latest is fetched from GitHub; an outdated install is marked with `↑` in the list and on the card, and tools with an available update are grouped at the top of the list
- **In-TUI updates** — `u` on the card detects the package manager (brew / go / cargo / pipx / npm) or uses `update_cmd` from `meta.yaml`, shows the command for confirmation and streams its output into panel `[3]` in real time
- **Self-update** — when a newer `keeptui` release exists, the status bar offers it: `U` updates through the same pipeline, `X` folds the notice into a compact cell that keeps `U` working, and after the update `U` restarts `keeptui` in place, in the same terminal tab. Works whether or not `keeptui` is in your own tracker
- **Run tools** — `enter` on a tool opens a one-line command prompt (it remembers the last command per tool for the session) and launches it in a new terminal tab (tmux / iTerm2 / kitty / WezTerm / Terminal.app); anywhere else the tool runs in the current window and `keeptui` resumes when it exits
- **Help navigation** — in `--help` / `man` mode `j` / `k` walk through flags and subcommands with the current entry highlighted; `/` searches the text
- **List search** — `/` filters by name and tag with match highlighting and an `N/M` counter
- **Tracker** — add by GitHub URL, statuses, one tag per tool and notes, all inside the TUI; `space` regroups the list under tag divider headers
- **GitHub API gauge** — an API quota usage indicator in the status bar, token management via `L`
- **Mouse** — scrolling, clicking on panels, and clicking the repository / release links on the card

## Installation

Homebrew (macOS / Linux):

```bash
brew install stanlyzoolo/apps/keeptui
```

Or tap once and install by name:

```bash
brew tap stanlyzoolo/apps
brew install keeptui
```

Upgrade later with `brew upgrade keeptui`.

From source (requires Go 1.25+):

```bash
git clone https://github.com/stanlyzoolo/keeptui
cd keeptui
go install .
```

The binary lands in `~/go/bin/keeptui`. Make sure `~/go/bin` is on your `PATH`.

## Usage

Run `keeptui` — the three-panel interface opens. Focus moves with `←` / `→` or the
digits `1` / `2` / `3` (each panel's number is written in its title). Press `?` at any
time for the hotkeys overlay — every keybinding, grouped by panel.

### Panel `[1] Tools`

| Key | Action |
|-----|--------|
| `j / k`, `↑ / ↓` | navigate the list (wraps around the edges) |
| `PgUp / PgDn`, `ctrl+f / ctrl+b` | page the selection up / down |
| `ctrl+d / ctrl+u` | move the selection half a page down / up |
| `g / G` | jump to the first / last tool |
| `space` | group the list by tag on / off — tools are gathered under tag divider headers with the label centered (`────… tag ────…`, untagged ones last); the selected tool stays selected across the toggle. With nothing tagged yet there is nothing to group, and the status bar says so |
| `t` | track — add a tool by GitHub URL or short name |
| `u` | untrack — remove (with confirmation) |
| `r` | rename — fix the binary name when it differs from the repo name (e.g. `claude-code` → `claude`) |
| `enter` | run the tool: a one-line prompt opens, prefilled with the tool name (or the last command run for it this session — handy for appending arguments); the command opens in a new tab named after the tool where the terminal is scriptable (tmux, iTerm2, kitty; Terminal.app opens a window, WezTerm an unnamed tab), anywhere else it runs in the current window — `keeptui` suspends and resumes when the tool exits. If opening the tab fails, the command automatically runs in the current window instead |
| `/` | search by name and tag: the matched substring is highlighted, tag-only matches show the tag dimmed, the status bar shows an `N/M` counter; `↑` / `↓` move through matches, `enter` opens the card, `esc` cancels and restores the previous selection |
| `L` | GitHub API status — limits and token (see below) |
| `U` / `X` | update `keeptui` itself / fold the notice away — active from any panel while a new `keeptui` release is offered (see [Updating keeptui itself](#updating-keeptui-itself)) |
| `?` | hotkeys overlay — every keybinding, grouped by panel |
| `esc`, `q`, `ctrl+c` | quit (`q` / `ctrl+c` quit from any panel; `esc` quits only here — in `[2]` / `[3]` it moves focus back instead) |

When you enter a GitHub URL (`https://github.com/owner/repo`, with `.git`, without a
scheme, or in SSH form `git@github.com:owner/repo.git`), `keeptui` puts the short tool
name into `name` and the normalized `github.com/owner/repo` into the `github` field.
A new tool gets the `trying` status.

The selected row carries the `⏺` marker, which stays visible (dimmed) while another
panel is focused. Tools with an available update are marked `↑` and gathered at the
top of the list; the order in `meta.yaml` is never changed.

`space` switches the list to the tag view: tools are gathered under muted tag divider
headers with the label centered (`────… tag ────…`, untagged ones under `────… untagged ────…`, last), in the order the tags first appear in
`meta.yaml`; tags differing only in letter case are one group, exactly as the search
treats them. In this view tools are grouped by tag rather than by pending update — the
`↑` marker still shows per row. Header rows are not selectable: `j` / `k` step over
them and clicking one does nothing. `space` again returns to the flat view; the
selected tool stays selected either way. Both views are display-only.

### Panel `[2] Brief`

| Key | Action |
|-----|--------|
| `o` | open the repository in the browser |
| `c` | open the changelog / releases page in the browser |
| `u` | update the tool (available when marked `↑`); `enter` runs the shown command, `esc` cancels |
| `r` | force-refresh the tool's data (card, changelog, README, installed version), bypassing the cache |
| `s` | cycle the status (`active → trying → inactive → active`) |
| `e` | edit the note |
| `t` | edit the tag — one tag per tool; text after the first comma is dropped, an empty value clears it |
| `j / k`, `↑ / ↓` | scroll the card (3 lines) |
| `ctrl+d / ctrl+u`, `ctrl+f / ctrl+b`, `PgUp / PgDn`, `space`, `g / G` | half-page / full-page scroll, top / bottom |
| `U` / `X` | update `keeptui` itself / fold the notice away — see [Updating keeptui itself](#updating-keeptui-itself) |
| `?` | hotkeys overlay |

Statuses: `active` (●) · `trying` (○) · `inactive` (✕) — shown on the card.
Legacy `forgotten` / `archived` values from `meta.yaml` are automatically read as
`inactive`; a legacy list of several tags is read as its first tag.

The `installed:` line has four states: the detected version, `detecting…` while the
local probe runs, `✓ no version` for a tool that is installed but won't report one
(a TUI app that ignores `--version`), and `✕ not installed`. The version is read from
`--version` / `-V`, and failing that from Homebrew's directory layout or
`cargo install --list` — so tools with no version CLI still resolve.

The card's links are clickable: a click on the `repo:` line opens the repository in the
browser, a click on the release URL under `[changelog]` opens that release page — the
same thing `o` and `c` do from the keyboard.

### Panel `[3] Readme / Help / Man`

The panel has three sources; the current one is shown in its title. On startup it
opens on the **README**: the repository README is fetched from the GitHub API and
rendered in the terminal (headings, lists, code blocks, tables). The mode is global,
not per tool — pick `--help` once and moving through the list keeps showing `--help`.

| Key | Action |
|-----|--------|
| `r` | README mode — the rendered repository README (the default); works only while `[3]` is focused, in `[1]` `r` is rename and in `[2]` refresh |
| `h` / `m` | `--help` / `man` mode (these two also work from `[2]`) |
| `j / k` | navigate by entries — flags and subcommands; the current entry is highlighted, the rest is dimmed (when there are no entries — in README mode, for example — `j / k` scroll 3 lines like the arrows) |
| `↑ / ↓` | scroll the text (3 lines) |
| `ctrl+d / ctrl+u`, `ctrl+f / ctrl+b`, `PgUp / PgDn`, `space`, `g / G` | half-page / full-page scroll, top / bottom |
| `/` | search the text (`n` / `N` — next / previous match); not available in README mode |
| `U` / `X` | update `keeptui` itself / fold the notice away — see [Updating keeptui itself](#updating-keeptui-itself) |
| `?` | hotkeys overlay |
| `esc` | first turns off entry navigation, then moves focus away |

The README is loaded lazily — one request per tool, cached for 24 hours — and only
for the tool whose README you actually look at: while you stay in `--help` or `man`
mode nothing is fetched at all. In README mode, though, moving to a tool for the first
time does spend that one request, so walking a long list on a cold cache costs one
request per tool visited. A tool without a `github` field, a repository without a
README, an exhausted quota or a failed fetch show a message with the way out
(`No repo for <name>`, `No README in <owner/repo>`, `rate limited — press [L]`,
`No README for <name>`); `r` in the brief panel re-fetches, bypassing the cache, and
adding a token in the `L` overlay retries the ones that hit the limit.

While a tool is being updated, this panel (`[3] Update`) shows the live command log;
the log stays available after completion — until the next update. For a tool the log
belongs to its row: move away and you see that tool's docs again, come back and the log
is there. A `keeptui` self-update log is shown whichever tool is selected (and with an
empty tracker); once it has finished, moving the selection or switching the mode with
`h` / `m` / `r` returns the panel to the docs.

## Updating tools

![in-TUI update — detect the manager, confirm the command, stream the log into panel [3]](demo/update.gif)

When the installed version lags behind the latest release (the `↑` marker), press `u`
in the brief panel. `keeptui` detects the package manager the binary was installed with:

- `brew` — a `/Cellar/<formula>/…` path → `brew upgrade <formula>`;
- `go` — buildinfo (`go version -m`) with a `path` field → `go install <module>@latest`;
- `cargo` — a binary in `~/.cargo/bin` → `cargo install <crate>`;
- `pipx` — a venv in `~/.local/pipx/venvs/<pkg>/` → `pipx upgrade <pkg>`;
- `npm` — a global `node_modules/<pkg>` → `npm install -g <pkg>`.

If the tool has no binary of its own on `PATH`, one more check runs before giving up:
a Homebrew keg or cask named after the tool → `brew upgrade <name>`. That covers
formulae whose binaries are named differently — `rust` installs `rustc` and `cargo`,
so there is no `rust` binary to detect from.

The command is shown in the status bar for confirmation (`enter` runs it, any other
key cancels); its output streams into panel `[3] Update` in real time and the TUI
stays responsive. After a successful update the version is re-detected, the `↑`
marker disappears, and the tool leaves the update group. One update runs at a time;
a command gets 10 minutes (a sudo password prompt inside it fails fast instead of
hanging).

If the manager cannot be detected (manual install), `keeptui` suggests setting the
`update_cmd` field or updating manually (`o` opens the releases page). `update_cmd`
in `meta.yaml` always takes precedence over auto-detection and runs via `sh -c`
(pipes and `&&` are fine):

```yaml
- name: mytool
  github: github.com/owner/mytool
  update_cmd: mytool self-update
```

## Updating keeptui itself

`keeptui` watches its own releases too. On startup it checks the latest one — a single
request, cached for 24 hours; on a build from a working copy the feature is off entirely
— no request, no notice, no restart offer (that covers both `dev` and the pseudo-version
a plain `go build` stamps, `v0.0.0-<date>-<commit>`, with or without `+dirty`). When a
newer version exists, the status bar replaces the usual hints with a notice:

```
keeptui v0.5.0 available — [U] update  [X] dismiss
```

- `U` — detects how this `keeptui` was installed (the same brew / go / cargo / pipx /
  npm detection tools get put through), shows the command for confirmation and streams
  its output into panel `[3] Update`. That log is visible whichever tool is selected —
  even with an empty tracker. While the update runs, a compact `keeptui updating…` cell
  sits in the corner of the status bar.
- `X` — folds the notice into a compact `keeptui ↑ [U]` cell next to the API gauge. `U`
  keeps working from there for the rest of the session; nothing is written to disk, so a
  dismissed notice comes back on the next launch.

After a successful update the bar reads `keeptui updated — [U] restart  [X] later`.
`U` replaces the running process with the new binary — same terminal tab, same tmux
pane, same arguments — so there is nothing to reopen; `X` folds that offer into
`keeptui [U] restart` for later in the session. On Windows there is no in-place restart:
`keeptui` prints `keeptui updated — run keeptui again` and exits.

`keeptui` does not need to be in your own tracker for any of this — the check is built
in. If it *is* tracked, `update_cmd` from its `meta.yaml` entry governs the self-update
exactly as it governs `u` on that row, and the release data is shared with its card, so
once that card has been fetched the check costs nothing extra. Updating a tracked
`keeptui` with `u` on its row is the same thing as `U` — it offers the restart too. On a
working-copy build, where the whole feature is off, that row behaves like any other
tool's: the update runs and its log stays with the row, but there is no notice and no
restart offer — restarting a working copy would just bring back the binary you built.

- One update at a time: while any update is running both `U` and `u` refuse out loud —
  the bar says `another update is running` instead of starting a second one. (Without a
  notice on screen, `U` is not a key at all: on a working-copy build it does nothing
  whatsoever.)
- If the manager cannot be detected (a hand-downloaded binary), the bar says
  `no known updater for keeptui — update manually` and nothing else happens.
- A Homebrew formula can lag behind the GitHub release: after updating, the installed
  version may still be older than the latest tag, and the notice comes back. That is an
  honest reflection of the state, not a bug.
- If the release check fails (no network, exhausted quota), there is simply no notice —
  everything else works as before. Updating a tracked `keeptui` with `u` still works and
  still offers the restart afterwards; there was just no notice to start from.
- If the update itself fails, the bar says `update failed — see [3]` and the reason stays
  in panel `[3]`. The notice goes back to whatever it was before — `keeptui v0.5.0
  available` (where `U` retries), the folded cell if you had folded it, a pending
  `[U] restart` from an earlier successful update, or nothing at all if there was no
  notice to begin with.
- Quitting `keeptui` in the middle of a self-update is safe: the updater runs detached and
  finishes on its own, exactly as for a tool update.
- The update targets the `keeptui` on your `PATH`. If you launched a copy by path
  (`./keeptui`) while a different one is installed, that installed one is what gets
  updated — and the restart brings back the copy you launched, so the notice reappears.

## GitHub API and token

`keeptui` fetches releases and repository cards through the GitHub REST API. Without a
token the limit is **60 requests per hour** per IP, with a token — **5000**. Each
tool with a `github` field costs 3 requests on startup, plus one more when you open
its README in panel `[3]`; so a cold start with a large list and no token can hit the
limit — cards stay empty until the window resets. The `keeptui` release check adds one
more request per 24 hours (none on a `dev` build).

Quota usage is visible in the right corner of the status bar (`▮▮░░░░░░░░░░ 12/60`). It
shares that corner with the folded self-update cell and yields to it: while the full
`keeptui … available` notice is up the gauge stays put, but after `X` the compact
`keeptui ↑ [U]` cell takes priority and can push the gauge off a narrow bar. The
`L` key works from any panel (as long as no other input mode is active) and opens the
API status overlay: token source, quota usage with an icon (`⚠` — low, `✕` —
exhausted) and the reset time. Right in the overlay:

- `e` — enter a token (echo hidden); the token is validated with a `/rate_limit` request and saved only on success;
- `d` — remove the saved token (available only for the file-based token);
- `r` — refresh the numbers; `esc` / `q` — close.

The token source follows environment precedence: the `GITHUB_TOKEN` variable always
wins over the file. A token entered in the TUI is stored in `~/.config/keeptui/token`
with `0600` permissions; an environment token is never written to disk. When the
quota is exhausted, already-loaded cards are not erased, and a card with no data
shows the `rate limited — press [L]` hint.

## Data storage

The tool list lives in `~/.config/keeptui/meta.yaml` — one entry per tool (`name`,
`status`, `added`, optionally `tags`, `note`, `github`, `update_cmd`). `tags` stays a
list in the file format, but a tool has **one** tag: a longer list is read as its first
entry and rewritten as a single-item list on the next save — the file as it was before
that migration is kept as `meta.yaml.bak`, so the dropped tags stay recoverable. The file is
fully managed from the TUI; editing it by hand is not required but safe — writes are
atomic.

| What | Where |
|------|-------|
| Tracker metadata | `~/.config/keeptui/meta.yaml` |
| Version, README and self-check cache (24h TTL each) | `~/.config/keeptui/cache.json` |
| GitHub token (`0600`) | `~/.config/keeptui/token` |
| Session error log | `~/.config/keeptui/logs/keeptui-<timestamp>.log` |
| Copy of the tracker before the one-tag migration | `~/.config/keeptui/meta.yaml.bak` |

The paths above are the macOS and Linux locations (`$XDG_CONFIG_HOME` if set, otherwise
`~/.config`). On Windows the base directory is `%AppData%\keeptui\` instead.

The log is created lazily — only on the first error. A session with no errors leaves
no file at all, so the presence of a file is itself the signal. The 20 most recent
logs are kept.

## Architecture

How the code is organized — the package graph, data flow, TUI state machine,
subprocess sandbox — is described in [ARCHITECTURE.md](ARCHITECTURE.md).

## Stack

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [Bubbles](https://github.com/charmbracelet/bubbles) — text input, viewport, spinner
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) — styling
- [Glamour](https://github.com/charmbracelet/glamour) — markdown rendering for the README panel
- [x/ansi](https://github.com/charmbracelet/x) — stripping escape sequences from captured tool output
- [termenv](https://github.com/muesli/termenv) — terminal color-profile detection
- [go-runewidth](https://github.com/mattn/go-runewidth) — glyph width measurement
- [golang.org/x/mod/semver](https://pkg.go.dev/golang.org/x/mod/semver) — version comparison
- [gopkg.in/yaml.v3](https://pkg.go.dev/gopkg.in/yaml.v3) — reading/writing `meta.yaml`

## Contributing

Bug reports and pull requests are welcome. Before submitting, run
`go test -race ./...` and `go vet ./...` — CI checks the same.

## License

MIT
