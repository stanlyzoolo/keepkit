# Structured markdown rendering for the [changelog] card section

## Overview

The `[changelog]` section of panel `[2] Brief` destroys the markdown structure of GitHub
release notes instead of respecting it: `stripMarkdown` (`internal/model/textutil.go:148`)
eats list markers (`strings.Trim(line, "*_")` trims a leading `*`/`-` bullet), leaves
`[text](url)` links raw, ignores fenced code blocks, and its `<...>` HTML-strip loop
swallows autolinks (`<https://…>`); `wrapText` then rebuilds lines from `strings.Fields`,
losing every indent. The fix, agreed in a brainstorm session: replace `stripMarkdown` with
a block-based converter that preserves structure (bullets, headings, code) as plain text
in the card's muted aesthetic — headings styled bold-light, everything else `InfoStyle`.
Not glamour (the card must stay muted and the render is synchronous inside `Update()`),
not point fixes (bold headings and hanging indents are unreachable after `wrapText`).

## Context (from discovery)

- Files involved: `internal/model/textutil.go` (`stripMarkdown` — single caller is
  `renderChangelogBlock`), `internal/model/render.go:1361` (`renderChangelogBlock`),
  `internal/ui/styles.go` (new heading style), `internal/model/render_test.go`
  (`TestStripMarkdown` at :332 — deleted). Converter and inline-helper tests go into a
  **new `internal/model/textutil_test.go`** — `render_test.go` is ~3900 lines and the
  package already splits tests by file (`group_test.go`, `readme_test.go`,
  `cardlinks_test.go`); the `renderChangelogBlock` styling tests stay in
  `render_test.go` next to the other render tables.
- Invariants that must survive untouched: `buildCard()`'s clickable-line map (the release
  URL is the block's first line — `TestBuildCardLinks`, `TestMouseBriefLinkClick`) and
  "content line = screen row" (`TestBriefContentLineIsScreenRow` — the viewport truncates,
  never soft-wraps).
- Patterns to follow: one wrap algorithm per package (`wrapLine`), named styles in
  `internal/ui/styles.go` (no inline `lipgloss.NewStyle()` in renderers), styling applied
  to whole lines only after wrapping (`wrapLine` counts runes and knows nothing of ANSI).
- `•` (U+2022) is East-Asian Ambiguous — same accepted class as `⏺`/`↑`/`─` (see the
  CLAUDE.md marker-glyph note); it never enters wrap math, which is rune-based.

## Development Approach

- **testing approach**: Regular (code first, then table tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** (`go test -race ./...`)
- **CRITICAL: update this plan file when scope changes during implementation**
- maintain backward compatibility: the release-URL line, the clickable-line map and the
  empty-body fallback (`no release notes available.`) keep their exact current behavior

## Testing Strategy

- **unit tests**: required for every task; table-driven, matching the existing
  `TestWrapText`/`TestCleanTerminalOutput` style in `internal/model/render_test.go`
- **e2e tests**: none in this project — the closest equivalents are the invariant tests
  (`TestBriefContentLineIsScreenRow`, `TestBuildCardLinks`) which must stay green unmodified
- full check matrix before commit: `preflight` (build / vet / `test -race` / golangci-lint)

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep plan in sync with actual work done

## Solution Overview

`markdownToLines(s string, width int) []mdLine` replaces `stripMarkdown`: a line-by-line
pass with two state flags (inside fenced code / inside HTML comment) classifies blocks,
strips inline markup, and returns **pre-wrapped** lines each tagged with a kind. Wrapping
happens inside the converter (via the existing `wrapLine`) because hanging indents need
block structure, and styling must land on whole finished lines — the only way to keep
ANSI out of the width math. `renderChangelogBlock` becomes a trivial consumer: iterate,
style `mdHeading` with the new `ui.ChangelogHeadingStyle`, everything else with
`ui.InfoStyle`, join with `\n`.

## Technical Details

Types (in `internal/model/textutil.go`):

```go
type mdLineKind int

const (
    mdBody mdLineKind = iota // paragraphs, list items, code — everything non-heading
    mdHeading                // a #-heading line
)

type mdLine struct {
    text string
    kind mdLineKind
}

func markdownToLines(s string, width int) []mdLine
```

Two kinds only — no `mdCode`: the sole consumer styles every non-heading line
`InfoStyle`, so a code tag would have zero readers (YAGNI). Everything that makes code
lines special (verbatim, no inline processing, no wrap, 2-space indent) lives entirely
inside the converter, driven by the fence state flag. `markdownToLines` guards
`width <= 0` itself (no wrapping then) — it is a pure function called directly from test
tables, not only through the floored call site.

Block rules (line pass, two state flags):

- **Input normalization** — `\r\n`/`\r` → `\n` before the line pass. GitHub release
  bodies regularly arrive CRLF (the web form submits it); the old code was accidentally
  saved by its per-line `TrimSpace`, and the verbatim code path below would otherwise
  carry `\r` straight into the viewport — exactly the class `cleanTerminalOutput` exists
  to stop. A `\r`-suffixed closing fence would also fail the fence match and swallow the
  rest of the body as code. Precedent: `internal/version/detect.go:162` does
  `strings.TrimRight(line, "\r")`.
- **Fenced code** — ``` or `~~~` toggles code mode; fence lines (with their language tag)
  are dropped; inside, lines go out verbatim with a 2-space indent and are **never
  wrapped** (wrapped shell code is worse than truncated; the viewport truncates overlong
  lines already — the accepted behavior `TestBriefContentLineIsScreenRow` pins). An
  unclosed fence means code to the end of input — no panic.
- **Headings** — `#{1,6}` **followed by a space** at line start → strip the marker,
  `kind=mdHeading`. The space is required: a leading issue ref like `#123 fixed …` stays
  literal text — a deliberate improvement over the old `TrimLeft(line, "#")`, which
  mangled it to `123 fixed …`. Levels are not distinguished (one emphasis step is enough
  in a narrow card). Setext headings — YAGNI.
- **Thematic breaks** — a line of only 3+ `-`/`*`/`_` (`---`, `***`, `___`) → dropped as
  a blank line (participates in blank collapse), **never** a list item; `---` is the
  stock separator before "**Full Changelog**: …" in real release notes.
- **Lists** — `-`/`*`/`+` or `N.` **followed by a space** after optional indent. No
  space → not a list: `*emphasis*` or `**Breaking**` at line start stays body text (the
  old `strings.Trim(line, "*_")` bug class must not come back). Marker normalizes to `•`
  (numbered items keep their number). Nesting level = leading spaces / 2 (floor; a tab
  counts as 4 spaces), clamped to 2 levels, 2 output spaces per level. Wrap with
  **hanging indent**: text wraps at `width − indent`, continuations align under the
  first text column, not the marker.
- **Blockquotes** — strip the `>` marker, treat as body.
- **HTML comments** — `<!-- -->` cut out, including multi-line (second state flag);
  release-note templates are full of them. Unclosed `<!--` runs to end of input.
- **Tables** — passed through as plain text (rare in release notes; YAGNI).
- **Blank lines** — collapse to at most one (current behavior).

Inline processing (heading/body only, never code), order is load-bearing:

1. Images `![alt](url)` → alt (empty alt → dropped entirely).
2. Links `[text](url)` → text (non-greedy).
3. Autolinks `<https://…>` → bare URL — **before** the HTML-tag strip, which would
   otherwise eat them (a current bug, fixed as a side effect).
4. HTML tags `<…>` — cut (existing logic).
5. `**`/`__`/`*`/`_`/`` ` `` markers — stripped. Bare URLs stay as-is.

Integration (`renderChangelogBlock`, `internal/model/render.go:1361`):

- The release-URL line and `buildCard`'s clickable-line registration do not change — the
  block still starts with the URL, indices stay live.
- The block keeps its trailing `"\n"` exactly as today (`render.go:1373`/`:1375`) — a
  bare `strings.Join(lines, "\n")` would drop it.
- Empty conversion result → the existing `no release notes available.` branch. This now
  covers a *new* path: a non-empty body the converter consumed entirely (comment-only,
  `---`-only) must land on the fallback, not on a blank block.
- Width floor `max(m.briefW-2, 10)` stays at the call site (covers `briefW == 0` before
  the first `WindowSizeMsg`; at width 10 a nested bullet's indent still leaves ~4 columns
  and `wrapLine` degrades to word-per-line without looping).
- New style `ui.ChangelogHeadingStyle = Bold(true).Foreground(ColorText)` in
  `internal/ui/styles.go` — brighter than the muted body without a new color, so it does
  not compete with the card's peach `SectionLabelStyle` section headers.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs (CLAUDE.md /
  ARCHITECTURE.md updates are Task 5) — all in this repo.
- **Post-Completion** (no checkboxes): manual visual check in a live terminal, demo GIFs
  if the visible change warrants it.

## Implementation Steps

### Task 1: Inline markdown processing helper

**Files:**
- Modify: `internal/model/textutil.go`
- Create: `internal/model/textutil_test.go`

- [ ] add `mdInline(s string) string` (name free to improve) applying, in order: images →
      links → autolinks → HTML-tag strip → emphasis/backtick markers, per Technical Details
- [ ] package-level compiled regexes for images/links/autolinks (project style: no
      per-call `regexp.MustCompile`)
- [ ] write table tests in `textutil_test.go`: plain link, image with/without alt,
      autolink survives the HTML strip (regression), nested `[text](url)` inside a
      sentence, `**bold**`/`` `code` ``, bare URL untouched, HTML tag cut, pathological
      input (unclosed `[`, `<`) — no panic
- [ ] run `go test -race ./internal/model/` — must pass before task 2

### Task 2: Block converter `markdownToLines`

**Files:**
- Modify: `internal/model/textutil.go`
- Modify: `internal/model/textutil_test.go`

- [ ] add `mdLineKind`, `mdLine`, `markdownToLines(s, width)` with the line pass: CR
      normalization first, then fence state, comment state, heading/thematic-break/
      list/quote classification, blank collapse; `width <= 0` guard (no wrap)
- [ ] list handling: space-required marker, `•` normalization, numbered items, nesting
      clamp (2 levels, 2 spaces each, tab = 4 spaces, floor), hanging-indent wrap via
      `wrapLine` at `width − indent` with continuation prefix
- [ ] code lines: 2-space indent, verbatim, no wrap; unclosed fence runs to end
- [ ] multi-line HTML comments cut; unclosed `<!--` runs to end
- [ ] write table tests: headings h1–h6, `#123 fixed …` stays literal (heading needs the
      space), nested + numbered lists with exact hanging-indent column assertions,
      3-space and tab indents, `*emphasis*`/`**Breaking**` at line start stay body,
      thematic breaks `---`/`***`/`___` dropped as blank, fenced code (with language /
      without / unclosed / closing fence arriving as `` ```\r\n ``), CRLF body
      end-to-end, blockquote (plain, nested `>>`, quote containing a list), table row
      passed through, multi-line comment, blank collapse, marker-only line (`- `)
      dropped as blank, width floor (width 10 with nested list), width 0 (no panic, no
      loop), empty input → empty output
- [ ] run `go test -race ./internal/model/` — must pass before task 3

### Task 3: Wire `renderChangelogBlock`, add heading style, delete `stripMarkdown`

**Files:**
- Modify: `internal/ui/styles.go`
- Modify: `internal/model/render.go`
- Modify: `internal/model/render_test.go`

- [ ] add `ui.ChangelogHeadingStyle` (`Bold(true).Foreground(ColorText)`) with the usual
      doc comment in `internal/ui/styles.go`
- [ ] rewrite `renderChangelogBlock` to iterate `markdownToLines(msg.body, max(m.briefW-2, 10))`:
      `mdHeading` → `ChangelogHeadingStyle`, else `InfoStyle`; keep the block's trailing
      `"\n"`; empty result → existing `no release notes available.` branch; URL line
      untouched
- [ ] delete `stripMarkdown` and `TestStripMarkdown` (`render_test.go:332`) — the converter
      tables from tasks 1–2 are their replacement
- [ ] write tests for `renderChangelogBlock` (in `render_test.go`): heading line carries
      bold + `ColorText`, body carries muted; a typical GitHub release body ("What's
      Changed" + "* … by @user in https://…/pull/N" bullets) renders with `•` and no raw
      `[…](…)`; error branch (`msg.err != nil`) unchanged
- [ ] write fallback tests: a body that is only an HTML comment and a body that is only
      `---` both render `no release notes available.` (non-empty body fully consumed by
      the converter — a new path with no existing coverage)
- [ ] verify unmodified invariant tests stay green: `TestBriefContentLineIsScreenRow`,
      `TestBuildCardLinks`, `TestMouseBriefLinkClick`
- [ ] run `go test -race ./...` — must pass before task 4

### Task 4: Verify acceptance criteria

- [ ] verify all Overview requirements: bullets survive, headings bold-light, links show
      text only, autolinks show bare URL, code blocks verbatim, muted card aesthetic kept
- [ ] verify edge cases: empty body, comment-only body, unclosed fence/comment, `briefW == 0`
- [ ] run full check matrix via the `preflight` skill (build / vet / `test -race` / lint)
- [ ] eyeball a live card: run keepkit against a tool whose release notes carry lists,
      links and a code block (keepkit itself qualifies)

### Task 5: [Final] Update documentation

- [ ] update CLAUDE.md — `stripMarkdown` is **not** mentioned there today, so this is an
      addition, not a replacement: add `markdownToLines` to the `textutil.go` row of the
      model-files table (CLAUDE.md:51) and describe the changelog block's rendering in
      the card prose (CLAUDE.md:91, "Clickable card lines" area): whole-line styling
      after wrap, `•`'s East-Asian-Ambiguous class (same accepted family as `⏺`/`↑`/`─`)
- [ ] update ARCHITECTURE.md:75 (the matching `textutil.go` inventory line); README.md
      has no changelog-rendering prose — verify and leave untouched
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- visual pass in a real terminal at the 80×24 baseline and a narrow (~50 col) width —
  hanging indents and truncated code lines look right, heading bold reads clearly
  against the muted body (the palette is fixed hex constants with no light-theme
  variant, so the bar is parity with the existing UI, not theme-perfect contrast)

**External system updates:**
- none — the change is self-contained; demo GIFs (`demo-gifs` skill) only if the visible
  difference is worth re-recording
