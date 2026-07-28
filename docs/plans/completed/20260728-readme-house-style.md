# README house style for panel [3]: markdown preprocessor + custom glamour theme

## Overview

Panel `[3]` renders each tool's README through glamour's standard `dark`/`light` style.
The result is noisy and inconsistent: badge/logo/HTML junk at the top of a typical
README, full link URLs bloating paragraphs in a narrow panel, bright standard-theme
colors that clash with keepkit's palette, and emoji scattered through headings. This
plan gives every README one calm "house style": a markdown cleanup pass before glamour
plus a custom glamour theme derived from keepkit's palette.

Key insight driving the cleanup rules: **no image can ever render in a TTY**, so all
images (badges, logos, screenshots) are removable without any badge-vs-logo heuristics.
Same for `[text](url)` links: panel `[3]` links are not clickable, so the href half is
pure noise — the preprocessor unwraps them to their text. Autolinks and bare URLs are
different: the URL *is* their content, so they stay visible.

All design decisions were approved in a brainstorm session (2026-07-28) and hardened by
a plan review the same day; do not re-litigate them during implementation.

**Acceptance criteria** (checked in Task 5):
- badge/logo/screenshot images gone, including reference-style (`![Build][badge]`)
- HTML junk gone; inline-tag text (`<kbd>Ctrl</kbd>` → `Ctrl`) preserved
- `[text](url)` renders as `text` with no URL and no double space; autolinks and bare
  URLs still visible; no table-link footnote stubs
- emoji and known `:shortcodes:` gone, no double-space/blank-line artifacts; `✅/❌/☑`
  translated to `✓/✗/✓`
- headings/rules/blockquotes in keepkit palette, no background plates
- code blocks and inline spans byte-identical through the preprocessor
- badge-only README → existing `No README for <name>. Press [h] for --help.` placeholder
- `styles.DarkStyleConfig`/`LightStyleConfig` globals unmutated

### Decision log

- **URL hiding mechanism — resolved 2026-07-28 by plan review, approved by user.** The
  style-level route (`Styles.Link.Format` rendering empty) was spiked against glamour
  v1.0.0 and rejected: glamour routes autolinks and linkified bare URLs through the same
  `Link` primitive with no separate link text, so blanking it silently deletes them
  (`See <https://example.com/docs> here` → `See  here`); it also leaves a double space
  where an inline link's href was, and table links leave footnote stubs. Chosen instead:
  the preprocessor unwraps `[text](url)` / `[text][ref]` → `text`. `Conceal`/`Faint`
  exist on `StylePrimitive` but the v1.0.0 renderer never applies them; `SkipHref` is an
  element field unreachable from styles.
- **4-space indented code blocks are NOT protected** by the segmentation pass (explicit
  decision): they are ambiguous with nested-list continuation lines, and modern READMEs
  overwhelmingly use fences. Accepted limitation, documented here.

## Context (from discovery)

- `internal/model/readme.go` — `renderReadme`: `cleanTerminalOutput` → glamour
  (`WithStandardStyle(readmeStyleName(dark))`, `WithWordWrap`, `WithColorProfile`);
  memoized by `readmeRenderCache` keyed `(name, raw, width, dark)`; test seam
  `testReadmeStyle` (unknown style name → constructor error → plain-text fallback path).
- `internal/model/readme_test.go` — fallback assertions at lines ~84 and ~147 compare
  against `cleanTerminalOutput(raw)`; `TestRenderReadmeStyleFollowsBackground` (~line 90)
  tests `readmeStyleName`'s dark/light branches.
- `internal/ui/styles.go` — palette vars `ColorPrimary #DA7756`, `ColorCategory
  #E8A87C`, `ColorMeta #5588AA`, `ColorMuted #AAAAAA`, `ColorDim #888888`, `ColorBorder
  #555555`, `ColorText #E8E8E8` — the theme derives from these (`string(ui.ColorPrimary)`),
  never from hex literals.
- `go.mod` — `glamour v1.0.0` (direct), `goldmark-emoji v1.0.6` (indirect; carries the
  GitHub shortcode dictionary via `definition.Github()` — note the exported name is
  `Github`, and it is `sync.Once`-guarded internally, so no caching wrapper is needed).
- `version.getReadme` truncates at `readmeMaxBytes` (512 KiB) and appends a marker — the
  cut can land **mid-fence**, so the segmentation pass must handle an unterminated fence.
- Empty-render guard reused, not extended: a README that cleans down to nothing renders
  `""` and the `data.content != "" && m.helpBase != ""` placeholder logic already shows
  `No README for <name>. Press [h] for --help.`.
- Glamour-side helpers verified present in v1.0.0: `glamour.WithStyles`,
  `glamour.WithInlineTableLinks`.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - table-driven tests for the pure preprocessor; struct-level tests for the theme
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `go test -race ./...` after each change (version package has real mutex-guarded
  state — keep `-race`)
- maintain backward compatibility: the `testReadmeStyle` seam behavior and the
  glamour-failure fallback path survive (the fallback now returns *preprocessed* text)
- the preprocessor runs synchronously inside `Update()` on up to 512 KiB of markdown:
  all regexes are compiled at package level (the `helpTokenRe` idiom in
  `internal/model/textutil.go`), never per call

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above)
- no e2e framework in this project; the TUI-level check is manual (see Post-Completion)
- color assertions on rendered output are deliberately avoided (no TTY in tests →
  `lipgloss.ColorProfile()` is Ascii → glamour strips colors). The theme is covered at
  the **struct level** instead: assert `keepkitStyle(...)` field values against
  `ui.Color*` — the only real coverage the theme can get, and it needs no TTY.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

Two independent layers, composed in `renderReadme`:

1. **Preprocessor** — pure `cleanReadmeMarkdown(raw string) string` in a new
   `internal/model/readme_clean.go`, called after `cleanTerminalOutput`, before glamour.
   Segmentation first (fences and inline spans are inviolable), then removal rules on
   the cleanable segments only.
2. **Theme** — `keepkitStyle(dark bool) ansi.StyleConfig` in a new
   `internal/model/readme_style.go`, built by **cloning** `styles.DarkStyleConfig` /
   `styles.LightStyleConfig` and overriding accents. Clone-and-override, not
   from-scratch: StyleConfig has dozens of fields and inheriting defaults protects
   against "forgotten field = broken render" on glamour upgrades. **The globals hold
   pointers** (`Color`, `BackgroundColor`, `Margin`, `Chroma`), and `styles.DefaultStyles`
   aliases them — every override assigns a **fresh pointer**; writing through a cloned
   pointer would mutate the shared style for the whole process.

`renderReadme` switches to `WithStyles(keepkitStyle(dark))` + `WithInlineTableLinks(true)`;
a non-empty `testReadmeStyle` keeps routing through `WithStandardStyle(name)` so the
existing constructor-failure fallback test still works. With production no longer
choosing a style *name*, `readmeStyleName` degenerates to the test branch — it is
deleted, `renderReadme` branches on `testReadmeStyle != ""` directly, and
`TestRenderReadmeStyleFollowsBackground` goes with it (the project removes dead branches
*and their test rows* — see CLAUDE.md on `restart_unix.go`).

The `readmeRenderCache` key stays `(name, raw, width, dark)` — `raw` is the
pre-cleanup input and the pipeline is deterministic, so memoization stays correct.

## Technical Details

### Segmentation (the inviolable-code rule)

- Fenced blocks: opener is 3+ backticks or 3+ tildes (info string allowed); the closer
  is the **same character with length ≥ the opener's** — a ````` ```` ````` fence
  wrapping ``` examples (how READMEs document markdown itself) stays one block.
- An **unterminated fence protects to EOF** (real case: the 512 KiB truncation can cut
  mid-fence).
- Inline `` `spans` `` are protected within cleanable segments.
- 4-space indented code blocks are *not* protected (see Decision log).

### Removal rules (cleanable segments only)

- **Images**: inline `![alt](url)`, linked `[![alt](img)](target)`, and
  reference-style `![alt][ref]` / `![alt][]` removed whole; lines left empty collapse
  so a 10-badge header vanishes.
- **Link-reference definitions**: standalone `[label]: url "title"` lines removed —
  they are pure metadata, and after image removal and link unwrapping nothing uses them.
- **Links**: `[text](url)` and `[text][ref]` unwrapped to `text`. Autolinks
  `<https://…>`, `<user@host>` and bare URLs are **left untouched** — their URL is the
  content (see Decision log).
- **HTML**: comments `<!-- … -->` removed whole; `<img>`/`<picture>`/`<video>`/
  `<script>`/`<style>` elements removed whole **including bodies**; other tags stripped
  keeping inner text (`<kbd>Ctrl</kbd>` → `Ctrl`, `<details><summary>` content
  survives). The tag pattern is constrained to a plausible HTML tag-name shape so
  autolinks, `<user@host>`, and prose/type angle brackets (`Vec<String>`, `a < b`)
  survive. No general sanitizer (bluemonday) — a targeted strip suffices.
- **Emoji**: pictographic SMP blocks U+1F000–U+1FAFF, VS16 (U+FE0F), ZWJ (U+200D) and
  the combining enclosing keycap (U+20E3) removed; translation map `✅→✓`, `❌→✗`,
  `☑→✓` (feature tables keep meaning, in panel style); BMP symbols `✓ ★ →` kept.
  After removal, doubled spaces collapse and lines emptied by it are dropped —
  `## 🚀 Getting Started` → `## Getting Started`, an emoji-only heading disappears
  rather than rendering as a styled blank.
- **Shortcodes**: `:name:` removed **only when `name` is in `definition.Github()`** —
  `:30:` inside `12:30:45` and unknown `:foo:` survive.

### Theme overrides

Dark variant (values from `internal/ui/styles.go`, never hex literals); light inherits
`LightStyleConfig` text colors and takes only the peach accents (`#E8E8E8` text is
unreadable on white):

- **H1** bold `ui.ColorPrimary`, **no background plate** (`BackgroundColor = nil`) —
  the standard style's bright plate is the main "tastelessness" source; **H2**
  `ui.ColorCategory` bold; **H3+** bold text color.
- **Links**: `LinkText` tinted `ui.ColorMeta`; no `Format` tricks (see Decision log —
  the preprocessor already removed hrefs; what remains linkified is autolinks/bare
  URLs, whose text is the URL).
- **Rules/blockquotes**: `ui.ColorBorder` / `ui.ColorDim`, matching panel frames.
- **Document margin 0** (fresh `*uint`): glamour's own margin eats 2–4 columns inside
  the already narrow `helpWrapWidth`; the panel frame provides the breathing room.
- **Code blocks**: chroma theme untouched (YAGNI — adjustable later as a one-liner).

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs achievable in this repo
- **Post-Completion** (no checkboxes): manual TUI verification, demo-gif re-recording

## Implementation Steps

### Task 1: Segmentation core — fences and inline spans

**Files:**
- Create: `internal/model/readme_clean.go`
- Create: `internal/model/readme_clean_test.go`

- [ ] implement segmentation of raw markdown into protected segments (fenced blocks per
      the rules above, inline `` ` `` spans) and cleanable segments, plus the
      `cleanReadmeMarkdown` shell that reassembles them (rules arrive in Tasks 2–3)
- [ ] handle the edge shapes: fence with info string; `~~~` fence; closer same-char and
      length ≥ opener (````` ```` ````` wrapping ``` blocks); unterminated fence
      protects to EOF
- [ ] write protection tests: junk-shaped content (`![…](…)`, `<div>`, emoji) inside a
      fence and inside an inline span survives byte-for-byte through the shell
- [ ] write segmentation edge tests: long fence wrapping short fences; unterminated
      fence (simulated 512 KiB truncation cut); `~~~` with info string
- [ ] run tests — must pass before task 2

### Task 2: Removal rules — images, links, HTML

**Files:**
- Modify: `internal/model/readme_clean.go`
- Modify: `internal/model/readme_clean_test.go`

- [ ] implement image removal: `![alt](url)`, `[![alt](img)](target)`,
      reference-style `![alt][ref]` / `![alt][]`; collapse lines left empty
- [ ] implement link unwrapping `[text](url)` / `[text][ref]` → `text`; remove
      standalone link-reference definition lines; leave autolinks
      (`<https://…>`, `<user@host>`) and bare URLs untouched
- [ ] implement HTML removal: comments whole; `<img>`/`<picture>`/`<video>`/`<script>`/
      `<style>` whole including bodies; other tags stripped keeping inner text, tag
      pattern constrained to plausible HTML tag names; package-level regexes only
- [ ] write table-driven tests: inline/linked/reference badge headers vanish with no
      orphan definitions; `[text](url)` → `text`; `<kbd>`/`<details>` keep text;
      `<script>` body gone
- [ ] write negative tests: autolink, email autolink, `Vec<String>`, `a < b` prose, and
      bare URLs all survive; `:30:`-style text untouched by these rules
- [ ] run tests — must pass before task 3

### Task 3: Removal rules — emoji and shortcodes

**Files:**
- Modify: `internal/model/readme_clean.go`
- Modify: `internal/model/readme_clean_test.go`
- Modify: `go.mod` (goldmark-emoji indirect → direct)

- [ ] implement emoji stripping (U+1F000–U+1FAFF, VS16, ZWJ, U+20E3) with the
      translation map `✅→✓`, `❌→✗`, `☑→✓`; keep BMP symbols (`✓ ★ →`)
- [ ] implement post-removal cleanup: collapse doubled spaces, drop lines emptied by
      removal (emoji-only headings disappear)
- [ ] implement shortcode removal via `definition.Github()` (direct import; no new
      module in go.sum; no caching wrapper — it is `sync.Once`-guarded)
- [ ] write tests: emoji stripped from headings/prose without double-space artifacts;
      emoji-only heading dropped; keycap `1️⃣` leaves no stray U+20E3; translation map
      applied; BMP symbols survive
- [ ] write negative tests: `:30:` inside `12:30:45` survives; unknown `:foo:` survives;
      known `:rocket:` removed; shortcode and emoji inside a fence survive
- [ ] run tests — must pass before task 4

### Task 4: Theme — keepkitStyle and renderReadme integration

**Files:**
- Create: `internal/model/readme_style.go`
- Create: `internal/model/readme_style_test.go`
- Modify: `internal/model/readme.go`
- Modify: `internal/model/readme_test.go`

- [ ] implement `keepkitStyle(dark bool) ansi.StyleConfig` per Technical Details:
      clone the standard config, override headings/links/rules/margin with values
      derived from `ui.Color*`; every override assigns a **fresh pointer** — never
      write through a cloned pointer
- [ ] wire the pipeline in `renderReadme`: `cleanTerminalOutput` →
      `cleanReadmeMarkdown` (re-check emptiness after it — early return `""`) → glamour
      with `WithStyles(keepkitStyle(dark))` + `WithInlineTableLinks(true)`; the
      plain-text fallback returns the *preprocessed* text
- [ ] collapse the dead seam: branch on `testReadmeStyle != ""` directly in
      `renderReadme` (→ `WithStandardStyle(testReadmeStyle)`), delete
      `readmeStyleName`, delete `TestRenderReadmeStyleFollowsBackground`
- [ ] update the two fallback assertions (`readme_test.go` ~84, ~147) to compare
      against `cleanReadmeMarkdown(cleanTerminalOutput(raw))`; add one fallback case
      whose input the preprocessor actually changes
- [ ] write struct-level theme tests: `H1.Color == string(ui.ColorPrimary)` (deref),
      `H1.BackgroundColor == nil`, `*Document.Margin == 0`, dark/light differ; plus a
      regression test that `styles.DarkStyleConfig`/`LightStyleConfig` are deep-equal
      to their pre-call values after both `keepkitStyle(true)` and `keepkitStyle(false)`
- [ ] write behavioral tests: `[text](url)` renders as `text` — URL absent, **no double
      space**; a table with a `[manual](url)` link renders the cell with no footnote
      stub; autolink and bare URL still visible; both variants render a sample doc
      without error; badge-only README renders to `""`
- [ ] run tests — must pass before task 5

### Task 5: Verify acceptance criteria

- [ ] verify every bullet of the **Acceptance criteria** list in the Overview
- [ ] verify edge cases: README of only badges/HTML → friendly placeholder; README with
      HTML/markdown examples inside fences → examples intact; truncated-mid-fence input
- [ ] run full CI matrix locally: `go build .`, `go vet ./...`,
      `go test -race ./...`, `golangci-lint run` (preflight)
- [ ] verify go.mod/go.sum changed only by the goldmark-emoji promotion (no new modules)

### Task 6: [Final] Update documentation

- [ ] update CLAUDE.md: `model` package file table (~line 50: add `readme_clean.go`,
      `readme_style.go`) and the panel `[3]` modes paragraph (~line 111: the "runs
      glamour with a **fixed** `WithStandardStyle("dark"|"light")`" clause → the new
      pipeline)
- [ ] update ARCHITECTURE.md in its three spots: the `model` file table (~line 74,
      `readme.go` row), the "passed to glamour as a fixed `WithStandardStyle`" clause
      (~line 134), and the `testReadmeStyle` seam description (~line 399)
- [ ] check README.md: only the Glamour credit line (~262) touches this area — update
      only if the rendering description changed
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- open keepkit in a dark and a light terminal; walk several real READMEs (badge-heavy,
  HTML-heavy, emoji-heavy, one with code examples containing HTML, one with a
  links-in-table feature matrix) and confirm the house style reads calmly and nothing
  legitimate was eaten
- taste pass on the theme: heading/link tints may need 1–2 iterations by eye — the
  first values are educated guesses

**External follow-ups:**
- re-record demo GIFs (`demo-gifs` skill): `hero.gif` shows panel `[3]` with a rendered
  README and will visibly change
- this branch lives in worktree `readme-house-style`
  (branch `worktree-readme-house-style`); merge via PR to `main` as usual
