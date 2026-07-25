<p align="center">
  <img src="assets/keepkit-wordmark.svg" alt="keepkit" width="420">
</p>

<p align="center">
  <a href="https://github.com/stanlyzoolo/keepkit/actions/workflows/ci.yml"><img src="https://github.com/stanlyzoolo/keepkit/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/stanlyzoolo/keepkit/releases/latest"><img src="https://img.shields.io/github/v/release/stanlyzoolo/keepkit" alt="Latest release"></a>
  <a href="https://goreportcard.com/report/github.com/stanlyzoolo/keepkit"><img src="https://goreportcard.com/badge/github.com/stanlyzoolo/keepkit" alt="Go Report Card"></a>
  <img src="https://img.shields.io/github/go-mod/go-version/stanlyzoolo/keepkit" alt="Go version">
  <a href="LICENSE"><img src="https://img.shields.io/github/license/stanlyzoolo/keepkit" alt="License"></a>
</p>

**keepkit** is a lightweight TUI for tracking versions of your favorite tools.
It keeps your kit in one list: installed and latest versions side by side, repository
cards, notes and tags — and updates an outdated tool right from the interface.
Pure TUI, no subcommands; the only flags are `--version` and `--help`.

![keepkit — track a repo by URL, tag it, group the list by tag, run the tool right from the tracker, then refresh the card while the GitHub API gauge counts requests](demo/hero.gif)

## Contents

- [Key features](#key-features)
- [Installation](#installation)
- [Usage](#usage)
- [Updating tools](#updating-tools)
- [Updating keepkit itself](#updating-keepkit-itself)
- [GitHub API and token](#github-api-and-token)
- [Data storage](#data-storage)
- [Architecture](#architecture)
- [Stack](#stack)
- [Contributing](#contributing)
- [License](#license)

## Key features

- **Track your tools** — add by GitHub URL or short name, cycle statuses
  (`active` / `trying` / `inactive`), keep a note and one tag per tool; rename a tool
  when the binary name differs from the repo name (e.g. `claude-code` → `claude`)
- **Versions at a glance** — the installed version is detected locally (with Homebrew
  and cargo fallbacks for tools that won't answer `--version`), the latest release
  comes from GitHub; outdated tools are marked `↑` and gathered at the top of the list
- **Update from inside the TUI** — one key detects the package manager
  (brew / go / cargo / pipx / npm, or `update_cmd` from `meta.yaml`), asks for
  confirmation and streams the command output into panel `[3]` in real time
- **Self-update** — when a newer keepkit release exists, the status bar offers it;
  after the update one more key restarts keepkit in place, in the same terminal tab
- **Docs panel** — panel `[3]` switches between the rendered repository README,
  `--help` output and the `man` page by hotkeys; in `--help` / `man` mode `j` / `k`
  walk flags and subcommands with the current entry spotlighted
- **Clickable card** — the repository and release links on the tool card open in the
  browser by mouse click, or by hotkeys for the repo and changelog pages
- **Tags and grouping** — one tag per tool; `space` regroups the flat list under tag
  divider headers and back
- **Run tools** — launch any tracked tool in a new terminal tab (tmux / iTerm2 /
  kitty / WezTerm / Terminal.app) or in the current window, without leaving keepkit
- **Search** — `/` filters by name and tag with match highlighting and an `N/M` counter
- **GitHub API token** — an on-screen quota gauge plus token management in the `L`
  overlay lifts the anonymous 60 requests/hour to 5000
- **Session error log** — errors (and only errors) are journaled to a per-session
  file, so a misbehaving session can be researched after the fact; no errors — no file
- **Mouse support** — scrolling, panel focus, selection and card links all respond
  to the mouse

Every panel shows its own keys in the help bar at the bottom, and `?` opens the full
hotkeys overlay — every keybinding, grouped by panel. That is all you need to learn.

## Installation

### Homebrew (macOS / Linux)

```bash
brew install stanlyzoolo/apps/keepkit
```

Or tap once and install by name:

```bash
brew tap stanlyzoolo/apps
brew install keepkit
```

Upgrade later with `brew upgrade keepkit`.

### go install

Requires Go 1.25+:

```bash
go install github.com/stanlyzoolo/keepkit@latest
```

The binary lands in `~/go/bin/keepkit` (make sure `~/go/bin` is on your `PATH`).

### Prebuilt binaries

Archives for macOS, Linux and Windows (amd64 / arm64) are attached to every
[GitHub release](https://github.com/stanlyzoolo/keepkit/releases/latest). Unpack and
put `keepkit` on your `PATH`.

### From source

```bash
git clone https://github.com/stanlyzoolo/keepkit
cd keepkit
go install .
```

Note: a build from a working copy is a *dev* build — the self-update check is off
by design.

## Usage

Run `keepkit` — a three-panel interface opens:

- **`[1] Tools`** — the tracker list: search, tag grouping, track / untrack / rename,
  and `enter` to run the selected tool.
- **`[2] Brief`** — the tool card: repository, stars, languages, installed and latest
  versions with the release date, status, note and tag. From here tools are updated,
  the repo or changelog opened in the browser, and the card data force-refreshed.
- **`[3] Readme / Help / Man`** — the docs panel: the rendered repository README (the
  default), the tool's `--help` output or its `man` page. While an update runs, this
  panel shows its live log instead.

Focus moves with `←` / `→` or the digits `1` / `2` / `3` (each panel's number is in
its title). Each panel lists its keys in the help bar; press `?` any time for the
full hotkeys overlay, grouped by panel.

When you enter a GitHub URL (`https://github.com/owner/repo`, with `.git`, without a
scheme, or in SSH form `git@github.com:owner/repo.git`), keepkit puts the short tool
name into `name` and the normalized `github.com/owner/repo` into the `github` field.
A new tool gets the `trying` status.

## Updating tools

![in-TUI update — the card shows installed vs latest, [u] detects the manager, the log streams into panel [3], refresh confirms the new version](demo/update.gif)

When the installed version lags behind the latest release (the `↑` marker), press `u`
on the tool card. keepkit detects the package manager the binary was installed with:

- `brew` — a `/Cellar/<formula>/…` path → `brew upgrade <formula>`;
- `go` — buildinfo (`go version -m`) with a `path` field → `go install <module>@latest`;
- `cargo` — a binary in `~/.cargo/bin` → `cargo install <crate>`;
- `pipx` — a venv in `~/.local/pipx/venvs/<pkg>/` → `pipx upgrade <pkg>`;
- `npm` — a global `node_modules/<pkg>` → `npm install -g <pkg>`.

If the binary cannot be attributed to any of those — or the tool has no binary of
its own on `PATH` at all — one more check runs before giving up: a Homebrew keg or
cask named after the tool → `brew upgrade <name>`. That covers formulae whose
binaries are named differently (`rust` installs `rustc` and `cargo`, so there is no
`rust` binary to detect from) and cask apps whose executable lives inside the `.app`
bundle, where the path itself names no manager.

The command is shown in the status bar for confirmation; its output streams into
panel `[3] Update` in real time and the TUI stays responsive. After a successful
update the version is re-detected and the `↑` marker disappears. One update runs at
a time; a command gets 10 minutes (a sudo password prompt inside it fails fast
instead of hanging).

If the manager cannot be detected (manual install), keepkit suggests setting the
`update_cmd` field or updating manually. `update_cmd` in `meta.yaml` always takes
precedence over auto-detection and runs via `sh -c` (pipes and `&&` are fine):

```yaml
- name: mytool
  github: github.com/owner/mytool
  update_cmd: mytool self-update
```

## Updating keepkit itself

keepkit watches its own releases too. On startup it checks the latest one — a single
request, cached for 24 hours; on a build from a working copy the feature is off
entirely. When a newer version exists, the status bar shows a notice:

```
keepkit v0.5.0 available — [U] update  [X] dismiss
```

`U` updates through the same pipeline as any tool — manager detection, confirmation,
live log in panel `[3]` (visible whichever tool is selected, even with an empty
tracker). `X` folds the notice into a compact cell next to the API gauge where `U`
keeps working; nothing is written to disk, so a dismissed notice returns on the next
launch. After a successful update the bar reads `keepkit updated — [U] restart`:
`U` replaces the running process with the new binary — same terminal tab, same tmux
pane, same arguments. On Windows there is no in-place restart: keepkit prints
`keepkit updated — run keepkit again` and exits.

keepkit does not need to be in your own tracker for any of this — the check is built
in. If it *is* tracked, its `update_cmd` governs the self-update exactly as it
governs `u` on that row, and the release data is shared with its card. Good to know:

- One update at a time: while any update runs, both `U` and `u` answer
  `another update is running` instead of starting a second one.
- A Homebrew formula can lag behind the GitHub release: after updating, the installed
  version may still be older than the latest tag and the notice comes back. That is
  an honest reflection of the state, not a bug.
- If the release check fails (no network, exhausted quota), there is simply no
  notice — everything else works as before.
- If the update fails, the bar says `update failed — see [3]` and the reason stays in
  panel `[3]`.
- The update targets the `keepkit` on your `PATH`. If you launched a copy by path
  (`./keepkit`) while a different one is installed, the installed one is what gets
  updated — and the restart brings back the copy you launched, so the notice
  reappears.

## GitHub API and token

keepkit fetches releases and repository cards through the GitHub REST API. Without a
token the limit is **60 requests per hour** per IP, with a token — **5000**. Each
tool with a `github` field costs 3 requests on startup, plus one more when you open
its README in panel `[3]`; a cold start with a large list and no token can hit the
limit — cards stay empty until the window resets. The keepkit release check adds one
more request per 24 hours (none on a dev build).

Quota usage is visible in the right corner of the status bar
(`▮▮░░░░░░░░░░ 12/60`). The `L` key opens the API status overlay: token source,
quota usage with a warning icon and the reset time; right in the overlay a token can
be entered (validated before it is saved), removed or the numbers refreshed.

The token source follows environment precedence: the `GITHUB_TOKEN` variable always
wins over the file. A token entered in the TUI is stored in `~/.config/keepkit/token`
with `0600` permissions; an environment token is never written to disk. When the
quota is exhausted, already-loaded cards are not erased, and a card with no data
shows the `rate limited — press [L]` hint.

## Data storage

The tool list lives in `~/.config/keepkit/meta.yaml` — one entry per tool (`name`,
`status`, `added`, optionally `tags`, `note`, `github`, `update_cmd`). The file is
fully managed from the TUI; editing it by hand is not required but safe — writes are
atomic. `tags` stays a list in the file format, but a tool has **one** tag: a longer
legacy list is read as its first entry, and the file as it was before that migration
is kept as `meta.yaml.bak`.

| What | Where |
|------|-------|
| Tracker metadata | `~/.config/keepkit/meta.yaml` |
| Version, README and self-check cache (24h TTL each) | `~/.config/keepkit/cache.json` |
| GitHub token (`0600`) | `~/.config/keepkit/token` |
| Session error log | `~/.config/keepkit/logs/keepkit-<timestamp>.log` |
| Copy of the tracker before the one-tag migration | `~/.config/keepkit/meta.yaml.bak` |

The paths above are the macOS and Linux locations (`$XDG_CONFIG_HOME` if set,
otherwise `~/.config`). On Windows the base directory is `%AppData%\keepkit\`
instead. A config directory left by an earlier version under the previous name
(`keeptui`) is picked up and renamed automatically on first launch.

The error log is created lazily — only on the first error. A session with no errors
leaves no file at all, so the presence of a file is itself the signal. The 20 most
recent logs are kept.

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

[MIT](LICENSE)
