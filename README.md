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

![keepkit — the tool list grouped by tag, a card refreshed past its 24h cache while the GitHub API gauge counts the requests, and panel [3] cycling through the README, the man page and the tool's own --help](demo/hero.gif)

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
  (`active` / `trying` / `inactive`), keep a note and one tag per tool; `m` renames a
  tool when the binary name differs from the repo name (e.g. `claude-code` → `claude`),
  which is what makes its installed version resolve
- **Versions at a glance** — every row carries the installed version in its own
  right-hand column (detected locally, with Homebrew and cargo fallbacks for tools
  that won't answer `--version`); the latest release comes from GitHub. Outdated
  tools are marked `↑`, gathered at the top of the list, and counted in the panel
  title and the status bar
- **Update from inside the TUI** — `enter` on the card detects the package manager
  (brew / go / cargo / pipx / uv / pnpm / bun / npm, or `update_cmd` from
  `meta.yaml`), asks for
  confirmation and streams the command output into panel `[3]` in real time
- **Self-update** — when a newer keepkit release exists, the status bar offers it;
  after the update one more key restarts keepkit in place, in the same terminal tab
- **Docs panel** — panel `[3]` switches between the rendered repository README,
  `--help` output and the `man` page by hotkeys; in `--help` / `man` mode `j` / `k`
  walk flags and subcommands with the current entry spotlighted. Every README is
  rendered in one house style: badges, logos, HTML wrappers, unclickable link URLs,
  the pictographic emoji a terminal font cannot draw and the title page the card
  already shows are stripped, and the headings follow keepkit's own palette — code
  samples are left exactly as written
- **Clickable card** — the repository and release links on the tool card open in the
  browser by mouse click, or by hotkeys for the repo and changelog pages
- **Language stack** — the card names a repository's languages with their shares and
  draws them as a proportional band in GitHub's own per-language colors
- **Tags and grouping** — one tag per tool; `space` regroups the flat list under
  section headers, led by everything with a pending update, and back
- **Run tools** — launch any tracked tool in a new terminal tab (tmux / iTerm2 /
  kitty / WezTerm / Terminal.app) or in the current window, without leaving keepkit
- **Search** — `/` filters by name and tag with match highlighting and an `N/M` counter
- **GitHub API token** — a quota gauge in the status bar, plus token management in
  the `a` overlay, lifts the anonymous 60 requests/hour to 5000
- **Session error log** — errors (and only errors) are journaled to a per-session
  file, so a misbehaving session can be researched after the fact; no errors — no file
- **Mouse support** — scrolling, panel focus, selection and card links all respond
  to the mouse

The status bar carries the keys that mean the same thing in every panel, each
panel's own actions sit in its footer, and `?` opens the full hotkeys overlay —
every keybinding, grouped by panel. That is all you need to learn. Its left corner
names the build you are running (`keepkit v0.1.0`), marked with the same `↑` an
outdated tool row carries when a newer keepkit release is waiting; the quota gauge
sits in the right corner.

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

- **`[1] Tools`** — the tracker list, each row carrying its installed version:
  search, tag grouping, track / untrack / rename, and `enter` to run the selected
  tool.
- **`[2] Brief`** — the tool card: the tool's name and repo, its tagline, then a
  metrics strip (installed / latest / maintenance / stars) and a line carrying
  languages, status, tag and note. From here `enter` installs a pending release, the
  repo or changelog opens in the browser, and the card data is force-refreshed.
- **`[3] Readme / Help / Man`** — the docs panel: the rendered repository README (the
  default, `R`), the tool's `--help` output (`H`) or its `man` page (`M`) — the three
  sources are capitals, so none collides with `r` refresh or `m` rename. A README is cleaned up
  before rendering — badges, logos, HTML and pictographic emoji go, link text stays
  without its URL, the title (and a slogan under it that only repeats the card's own)
  is dropped so the panel opens on the first sentence that says something new, and
  code blocks are untouched. While an update runs, this panel shows its live log
  instead, and keeps it afterwards under a line saying how the update ended.

Focus moves with `←` / `→` or the digits `1` / `2` / `3` (each panel's number is in
its title, and the focused panel is marked with `▸` as well as by color). The status
bar carries the six keys that do the same thing in every focus — `t` track, `u`
untrack, `m` rename, `a` api, `?` keys, `q` quit — centered between the running
version on the left and the quota gauge on the right. Everything panel-local sits in
that panel's own footer: `/` filter, `enter` run and `space` group in `[1]`, the
card's actions in `[2]`. Press `?` any time for the full hotkeys overlay, grouped by
panel.

When you enter a GitHub URL (`https://github.com/owner/repo`, with `.git`, without a
scheme, or in SSH form `git@github.com:owner/repo.git`), keepkit puts the short tool
name into `name` and the normalized `github.com/owner/repo` into the `github` field.
A new tool gets the `trying` status.

The `name` field is also what keepkit probes locally: the installed version comes from
running `<name> --version`, then `-V`, then the `Caskroom/<name>` / `Cellar/<name>`
directory, then `cargo install --list`. So when a repo is not named after the binary it
installs — `anthropics/claude-code` ships `claude` — every probe misses and the row
carries no version. Press `m` and rename the tool to the binary's own name: `github`
keeps pointing at the repo, so nothing about the release data changes, while the
installed version, the `↑` marker and the comparison behind it start working.

## Updating tools

![in-TUI update — the card puts the installed version against the latest release, enter detects the package manager and asks to confirm its command, and the manager's output streams into panel [3] until it reports the outcome and the version keepkit verified afterwards](demo/update.gif)

When the installed version lags behind the latest release (the `↑` marker), press
`enter` on the tool card. keepkit detects the package manager the binary was
installed with:

- `brew` — a `/Cellar/<formula>/…` path → `brew upgrade <formula>`;
- `go` — buildinfo (`go version -m`) with a `path` field → `go install <module>@latest`;
- `cargo` — a binary in `~/.cargo/bin` → `cargo install <crate>`;
- `pipx` — a venv in `~/.local/pipx/venvs/<pkg>/` → `pipx upgrade <pkg>`;
- `uv` — a tool under `$UV_TOOL_DIR` (default `~/.local/share/uv/tools/<pkg>/`) →
  `uv tool upgrade <pkg>`;
- `pnpm` — a global under `$PNPM_HOME` (default `~/Library/pnpm` on macOS,
  `~/.local/share/pnpm` on Linux) → `pnpm add -g <pkg>`;
- `bun` — a global under `$BUN_INSTALL` (default `~/.bun`) → `bun add -g <pkg>`;
- `npm` — a global `node_modules/<pkg>` → `npm install -g <pkg>`.

pnpm and bun are checked before npm on purpose: both keep their globals behind
`node_modules` paths, so npm would otherwise claim them and offer
`npm install -g <pkg>` — a duplicate copy under npm's prefix while the original keeps
shadowing it on `PATH`. `add -g` rather than `update -g` for both, because `update`
respects the range recorded at install time and can quietly refuse the major bump the
card is offering.

If the binary cannot be attributed to any of those — or the tool has no binary of
its own on `PATH` at all — one more check runs before giving up: a Homebrew keg or
cask named after the tool → `brew upgrade <name>`. That covers formulae whose
binaries are named differently (`rust` installs `rustc` and `cargo`, so there is no
`rust` binary to detect from) and cask apps whose executable lives inside the `.app`
bundle, where the path itself names no manager.

Two limits are worth knowing. Detection for uv, pnpm, bun and npm reads path
conventions, so on **Windows** their bins are `.cmd` shims keepkit does not parse —
a pre-existing npm limitation the three new managers inherit; on macOS and Linux
pnpm's shims are `#!/bin/sh` scripts, which keepkit does read. And a manager's **own**
binary is out of scope: `bun upgrade`, `pnpm self-update` and friends are not offered,
those tools update themselves. A layout no check recognises normally degrades to the
same `update_cmd` hint as an unknown manager.

One case does not, and it is worth knowing: pnpm and bun globals sit behind
`node_modules` paths, so if keepkit cannot work out where their root is, the path
still looks like an ordinary npm install and the offer becomes
`npm install -g <package>`. Running it would install a second copy under npm's
prefix while the original keeps shadowing it on `PATH`. keepkit reads `$PNPM_HOME`
and `$BUN_INSTALL` (and the platform defaults) and expands symlinks in them, so this
only bites when the root is somewhere the environment does not name — a store moved
by a config file, or a session started without your shell profile. Export the
variable, or set `update_cmd` on the tool, and the right manager is picked.

The command is shown in the status bar for confirmation; its output streams into
panel `[3] update` in real time and the TUI stays responsive. One update runs at a
time; a command gets 10 minutes (a sudo password prompt inside it fails fast instead
of hanging).

When it ends, the log stays where it is — it is the record of what happened — and
closes with what the panel frame now also says, `[3] update finished` or
`[3] update failed`:

```
✓ finished · brew · 14s
✓ fd  v10.2.0 → v10.3.0
R readme · H help · M man
```

The log itself is dimmed — it is the manager talking, while these last lines are
keepkit's own verdict on what the manager did.

The second line is written a moment later, once the version has been re-detected —
which is also what makes the `↑` marker disappear. It is a separate line because a
manager exiting successfully is not the same thing as the tool having moved: when
nothing changed it reads `⚠ fd  still v10.2.0` instead, and if the update left no
working binary behind, `✕ fd  not on PATH`. A failure prints the reason under the
first line and normally stops there, since nothing re-detects after it.

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
keepkit v0.5.0 available — U update  X dismiss
```

`U` updates through the same pipeline as any tool — manager detection, confirmation,
live log in panel `[3]` (visible whichever tool is selected, even with an empty
tracker). `X` folds the notice into a compact `U update` cell beside the version in the
bar's right corner, where `U` keeps working; nothing is written to disk, so a dismissed notice returns on the next
launch. After a successful update the bar reads `keepkit updated — U restart`:
`U` replaces the running process with the new binary — same terminal tab, same tmux
pane, same arguments. On Windows there is no in-place restart: keepkit prints
`keepkit updated — run keepkit again` and exits.

keepkit does not need to be in your own tracker for any of this — the check is built
in. If it *is* tracked, its `update_cmd` governs the self-update exactly as it
governs `enter` on that row, and the release data is shared with its card. Good to know:

- One update at a time: while any update runs, both `U` and `enter` answer
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

Quota usage sits in the right corner of the status bar (`api ▮▮░░░░░░░░░░ 12/60`) for
the whole session once the numbers are known, and the fill turns red as the window
runs out. It is also the only visible sign that keepkit has an API surface at all, so
hiding it at rest hid the overlay with it; on a narrow terminal it is the first thing
the corner drops. The `a` key opens the API status overlay: token source, quota usage with a warning icon
and the reset time; right in the overlay a token can be entered (validated before it
is saved), removed or the numbers refreshed.

The token source follows environment precedence: the `GITHUB_TOKEN` variable always
wins over the file. A token entered in the TUI is stored in `~/.config/keepkit/token`
with `0600` permissions; an environment token is never written to disk. When the
quota is exhausted, already-loaded cards are not erased, and a card with no data
shows the `rate limited — press L` hint.

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
- [goldmark-emoji](https://github.com/yuin/goldmark-emoji) — GitHub's `:shortcode:` dictionary, used to strip them from a README
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
