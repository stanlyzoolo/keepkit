# Panel [3] README typography: heading ladder, own chroma palette, dividers

*Revised after plan-review (2026-08-08): list-marker coloring and table-separator coloring
were verified to be inexpressible in glamour v1.0.0's StyleConfig — both are now recorded
deliberate absences; the review's renderer-confirmation rule, the named breaking tests and
the two Task-2 traps are folded in below.*

## Overview

Panel `[3]`'s glamour theme (`keepkitStyle`) currently keeps the heading ladder almost
flat (H1 Accent, H2 Emphasis, H3–H6 Text bold — H3 and H4 are indistinguishable), leaves
code-block syntax accents on glamour's stock pink/violet palette, and renders `---`
dividers as a short stock dash run. This plan gives the panel a designed typography:

- a per-level heading color ladder from a user-chosen teal→olive palette, plus `›` depth
  markers for H3–H6;
- a keepkit-owned chroma token palette in the same temperature, replacing the stock
  accents that clash with it;
- `✓`/`☐` task checkboxes, italic quotes, full-panel-width dividers.

The palettes live in `internal/ui` as the **second sanctioned non-role palette** (the
`ui.LanguageColor` precedent — with one honest difference stated in the doc comment:
linguist's colors are externally-defined brand marks, these are keepkit-chosen shades, so
this file is a deliberate, contained exception to "color is a role, not a shade", not an
application of the existing one). They are exported var blocks — "easy to change" means
edit one place + `go build` (decided against a config file and against live reload;
chroma registration is one-shot per process anyway).

**Contract change, stated up front:** panel `[3]`'s headings and code-fence accents move
off `Theme` roles onto fixed hexes, so a future theme switch repaints the panel's body,
links, quotes and inline code but **not** H1–H5 or the fence accents. Three places state
the old contract ("switching every color in keepkit is switching one Theme value" —
`internal/ui/theme.go`, `readme_style.go`'s doc comment, `docs/design/readme-pipeline.md`)
and each is updated **in the task that breaks it**, not at the end.

## Context (from discovery)

- `internal/model/readme_style.go` — `keepkitStyle(t ui.Theme, dark)` clones
  `styles.DarkStyleConfig`/`LightStyleConfig`, overrides via fresh pointers (`ptrTo`);
  every new override MUST assign a fresh pointer or it restyles glamour process-wide
  (`TestKeepkitStyleLeavesGlobalsUntouched`, JSON snapshot).
- `internal/ui/lang.go` — the existing non-role palette; the new file sits beside it.
- glamour v1.0.0 `ansi.StyleConfig` fields verified against the module cache **and by
  rendering** (plan-review):
  - `H1..H6`/`Heading` StyleBlocks with `Prefix`; `cascadeStylePrimitive` lets a non-nil
    child field win over the parent, so per-level colors are safe under the `Heading`
    base — but it only overrides `Prefix` when the child's is **non-empty**, so the stock
    `"# "`/`"## "` come back if the explicit `H1.Prefix = ""`/`H2.Prefix = ""` lines are
    lost (no existing test pins them);
  - `styles.DarkStyleConfig.H6` carries an explicit `Bold: false` that beats inheritance —
    H6 must keep its own `Bold = ptrTo(true)`;
  - `Item.Color`/`Enumeration.Color` are **no-ops**: `BaseElement.doRender` paints both
    the `• ` BlockPrefix and the enumerator with the *enclosing block's* style
    (confirmed by render — a red `Item.Color` still produced document-gray bullets).
    List-marker coloring is inexpressible in StyleConfig; recorded deliberate absence;
  - table separators are drawn by lipgloss/table from glyph strings with **no color
    call**; `Table`'s StylePrimitive styles **cell content** (header and body alike), so
    separator coloring and header-only bolding are inexpressible; recorded deliberate
    absence;
  - `Task` StyleTask{`Ticked`,`Unticked`} and `HorizontalRule.Format` work as expected
    (confirmed by render).
- `renderReadme` (`internal/model/readme.go`) builds the renderer per render; the width
  it wraps to is its own local **after the `readmeMinWrap` clamp** (readme.go:42-44) —
  that clamped value, not `helpWrapWidth()` directly, is what the divider must use.
  `readmeRenderCache` is already keyed by (theme, width), so a width-dependent style
  re-renders on resize for free. The palettes are package vars — constant per process —
  so they do not need to join the cache key.
- Chroma caveat (documented in readme_style.go): glamour registers the built chroma style
  under the process-global one-shot name `"charm"`; first render wins. Assertions must be
  struct-level; a mid-session palette change cannot repaint fences — consistent with
  `m.darkBG` resolved once at construction.
- `Prefix` renders with the heading's own style — a depth marker cannot be `Dim`
  separately; it carries the level color, which encodes the same depth twice (accepted).

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task), plus the
  tui-render mutation check: for every new assertion, revert the production edit, confirm
  red, restore.
- **renderer-confirmation rule (from plan-review):** a struct-level mutation check only
  proves the struct field changed, never that glamour *reads* that field —
  `Item.Color` passes both and does nothing. So **any override new to this file must be
  confirmed against glamour's renderer once, in its own task** (read the element's
  `Render` in the module cache, or do a one-off `forceColorProfile` render in the
  throwaway vis file) **before** its struct test is written. Do not defer this to the
  final vis task.
- complete each task fully before moving to the next; run `go test -race ./internal/model
  ./internal/ui` after each change, full `go test -race ./...` at the end.
- every task's struct-level tests must keep `TestKeepkitStyleLeavesGlobalsUntouched`
  green — that test is the fresh-pointer guard.
- existing assertions that pin the old look are **updated, never deleted**; the ones this
  plan breaks are named in their tasks.

## Testing Strategy

- **unit tests**: struct-level assertions on the returned `ansi.StyleConfig` (the
  `TestKeepkitStyleRepaintsChroma` pattern) — tests have no TTY, rendered-output color
  assertions are impossible (Ascii profile strips them) — each backed by the
  renderer-confirmation rule above.
- **visual check**: throwaway `internal/model/zz_vis_test.go` rendering a fixture README
  through `renderReadme` at 80 and 120 columns, **in both `dark` and light variants**
  (`forceColorProfile(t)` + `themeSeq(...)` when checking which color a run got); read
  the output, then delete the file. This is where the `#08605f` (dark) and
  `#a2ad59`/`#8e936d` (light, ~2.2:1/~2.9:1 on white) contrast risks are judged.
- no e2e infrastructure in this project.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix, blockers with ⚠️ prefix
- update this file if implementation deviates from the design

## Solution Overview

Design decisions (brainstorm + review revisions, all user-approved):

| Element | Decision |
|---|---|
| H1 | `#08605f`, bold — shared section (both variants) |
| H2 | `#177e89`, bold — shared |
| H3 | `#598381`, bold, prefix `› ` — shared |
| H4 | `#8e936d`, bold, prefix `›› ` — shared |
| H5 | `#a2ad59`, bold, prefix `››› ` — shared |
| H6 | `Theme.Dim`, bold (explicit — stock `Bold:false` trap), prefix `›››› ` — **dark-only** (the file's rule: Theme tints beyond heading accents are dark-only; light keeps its stock H6, prefix still applied shared) |
| Chroma tokens | Keyword / LiteralString / Comment / LiteralNumber / NameFunction from an own teal/olive var block; all other tokens inherited — dark-only like the existing repaint |
| Inline code | unchanged (`Text` on `Surface`, shared rule with the card changelog) |
| List markers | **deliberate absence** — `Item.Color`/`Enumeration.Color` are no-ops in glamour v1.0.0; recorded as a code comment, no override shipped |
| Task lists | `Ticked: "✓ "`, `Unticked: "☐ "` — shared |
| Divider `---` | `─` × clamped render width via `HorizontalRule.Format` — Format shared (colorless), color stays `Border` dark-only |
| Blockquote | `Dim` (existing) + italic — dark-only |
| Tables | **deliberate absence** — separators are lipgloss-drawn and uncolorable; `Table`'s style colors cell text; no header-only style exists; recorded as a code comment |
| LinkText / Strong | unchanged (`Link` / `Emphasis`) |

Known risk, accepted: `#08605f` is dark against the dark panel — the ladder reads
"inverted" (H1 quietest). Judged at the vis step in **both variants**; reversing the
ladder is a 5-line var edit and the user decides then.

## Technical Details

- New file `internal/ui/readme_palette.go` (shape pinned, not implementer's choice):
  - `HeadingColors [5]lipgloss.Color` — H1..H5 (H6 is the `Dim` role, dark-only);
  - `ChromaColors struct { Keyword, String, Comment, Number, Function lipgloss.Color }`
    as one named var;
  - doc comment: second sanctioned non-role palette; how it *differs* from
    `LanguageColor` (invented shades vs external brand marks — a contained exception to
    "role, not shade", accepted for content typography); the "edit here + go build"
    contract; the theme-switch consequence (these do not follow a `Theme` change).
- `keepkitStyle` gains a `width int` parameter (**added in Task 2**, the first task that
  touches the signature, so later tasks and existing tests are rewritten once):
  `HorizontalRule.Format = "\n" + strings.Repeat("─", width) + "\n"` set in Task 5;
  width <= 0 keeps the stock format. `renderReadme` passes its **clamped** width local.
  The `testReadmeStyle` seam path (`WithStandardStyle`) is unaffected.
- Chroma overrides extend the existing bounded repaint: the clone stays
  (`chromaCfg := *cfg.CodeBlock.Chroma`), each overridden token entry sets `Color` and
  `BackgroundColor = Surface`. Note from review: chroma v2's `Style.Get` likely inherits
  `Background` from the style's `Background` entry, so the explicit background may be
  belt-and-braces rather than load-bearing — **verify before writing the rationale
  comment** (do not copy the existing `Text`-entry comment's reason unverified).
- Accepted caveat (record in a comment near the Format override): a `---` inside a
  blockquote or list wraps at `width − indent`, and a run with no breakpoints overflows
  rather than breaks. Rare (the stock short dash run never hit it), accepted.
- Depth marker `›` (U+203A) and `☐` (U+2610) are East-Asian-Ambiguous decorative glyphs
  in wrapped text — the accepted class (`⏺`/`↑`/`•` precedent). Pin in the glyph-width
  test as documentation of the assumption (they travel through glamour's own wrap, not
  `wrapText` — the pin documents, it does not cover that path).

## Implementation Steps

### Task 1: Heading + chroma palette var block in internal/ui

**Files:**
- Create: `internal/ui/readme_palette.go`
- Create: `internal/ui/readme_palette_test.go`

- [ ] create `readme_palette.go` with `HeadingColors [5]lipgloss.Color`
      (`#08605f #177e89 #598381 #8e936d #a2ad59`) and `ChromaColors` (five teal/olive
      token colors picked to sit against `Surface`)
- [ ] doc comment per Technical Details: non-role palette, the *difference* from
      `LanguageColor`, theme-switch consequence, edit contract
- [ ] write tests: every entry parses as `#RRGGBB` (guards a typo'd hex lipgloss would
      swallow), array length pinned at 5 — note: only the length pin is
      mutation-checkable; the hex-shape assertion is a smoke guard, not a mutant killer
- [ ] run tests — must pass before task 2

### Task 2: Per-level heading colors, depth markers, width parameter

**Files:**
- Modify: `internal/model/readme_style.go`
- Modify: `internal/model/readme.go`
- Modify: `internal/model/readme_style_test.go`

- [ ] add `width int` to `keepkitStyle`; thread through `renderReadme` (pass the
      **clamped** width local) and every existing call site in tests; no Format use yet
- [ ] renderer-confirmation: re-verify per-level heading color + prefix rendering once
      via a `forceColorProfile` render (review already confirmed child-wins cascade and
      prefix styling; this is the rule being followed, cheap here)
- [ ] shared section: H1–H5 colors from `ui.HeadingColors` + `Bold` via `ptrTo`;
      **keep the explicit `H1.Prefix = ""` and `H2.Prefix = ""`** (cascade only
      overrides a non-empty child prefix — losing these silently brings `# `/`## ` back);
      H3–H5 prefixes `"› "`/`"›› "`/`"››› "`; H6 prefix `"›››› "` shared
- [ ] dark branch: `H6.Color = Theme.Dim` **and `H6.Bold = ptrTo(true)`** (stock dark H6
      carries an explicit `Bold: false` that beats inheritance); drop the now-redundant
      dark-branch `Heading`/`H6` *color* overrides only where the per-level colors
      replace them
- [ ] update the named breaking tests rather than deleting them:
      `TestKeepkitStyleOverrides` (pins H1 Accent / H2 Emphasis → now palette),
      `TestKeepkitStyleDarkOnlyOverrides` (pins dark Heading/H6 Text → new shape)
- [ ] write struct tests: each level's Color/Bold/Prefix pinned, **including
      `H1.Prefix == ""` / `H2.Prefix == ""` and `H6.Bold == true`**; H6 color dark-only
- [ ] rewrite `keepkitStyle`'s doc comment: the theme-switch claim ("same one-value
      theme switch") now holds for body/links/quotes/inline code only — say so here, in
      the task that makes it true
- [ ] add `›` to the glyph-width test (both runewidth conditions, Ambiguous accepted)
- [ ] mutation-check each assertion (delete one level's override → red);
      `TestKeepkitStyleLeavesGlobalsUntouched` stays green
- [ ] run tests — must pass before task 3

### Task 3: Own chroma token palette for code blocks

**Files:**
- Modify: `internal/model/readme_style.go`
- Modify: `internal/model/readme_style_test.go`

- [ ] renderer-confirmation: verify chroma token entries are read for a Go + shell fence
      (one-off render), and whether the explicit per-token `BackgroundColor` is
      load-bearing or inherited from the `Background` entry — write the comment from
      what is observed, not from the plan
- [ ] extend the bounded repaint (dark branch): `Keyword`, `LiteralString`, `Comment`,
      `LiteralNumber`, `NameFunction` take `ui.ChromaColors` (+ `Surface` background per
      the verification above); the fresh-pointer clone stays
- [ ] **narrow, do not delete, the `reflect.DeepEqual` boundedness guard** in
      `TestKeepkitStyleRepaintsChroma`: zero out the now-owned seven entries
      (Background, Text + the five) and assert everything else is inherited verbatim;
      rewrite its comment — the repaint is no longer "bounded to two entries", it is
      bounded to a named seven
- [ ] write struct tests: the five entries carry the palette colors; an untouched token
      (e.g. `Operator`) still aliases the standard value
- [ ] mutation-check each new assertion; globals-untouched snapshot stays green
- [ ] run tests — must pass before task 4

### Task 4: Task checkboxes, blockquote italic, recorded absences

**Files:**
- Modify: `internal/model/readme_style.go`
- Modify: `internal/model/readme_style_test.go`

- [ ] shared: `Task.Ticked = "✓ "`, `Task.Unticked = "☐ "` (renderer-confirmed by the
      review; note the confirmation source in the comment)
- [ ] dark branch: `BlockQuote.Italic = ptrTo(true)` (color stays `Dim`);
      renderer-confirm italic is applied before writing the test
- [ ] record the two deliberate absences as one code comment block on `keepkitStyle`:
      list markers (`Item.Color`/`Enumeration.Color` are no-ops — BaseElement renders
      BlockPrefix with the enclosing block's style) and tables (separators are
      lipgloss-drawn with no color call; `Table`'s style colors cell content; no
      header-only style)
- [ ] add `☐` to the glyph-width test
- [ ] write struct tests for Ticked/Unticked and Italic; mutation-check
- [ ] run tests — must pass before task 5

### Task 5: Full-width divider

**Files:**
- Modify: `internal/model/readme_style.go`
- Modify: `internal/model/readme_style_test.go`

- [ ] shared section: `HorizontalRule.Format = "\n" + strings.Repeat("─", width) + "\n"`
      guarded on `width > 0` (width <= 0 keeps stock); color stays `Border` dark-only
- [ ] record the accepted caveat comment: a `---` nested under an indent overflows
      rather than breaks (no breakpoints in the run)
- [ ] rewrite the stale half-sentence in the "deliberately missing" paragraph of
      `keepkitStyle`'s doc comment — "`HorizontalRule` is a fixed string" stops being
      true here; the injection objection (faking a heading rule needs a `---` the author
      never wrote) still stands and remains the recorded reason
- [ ] write struct tests: Format length equals width at two widths; width=0 falls back
- [ ] mutation-check; run tests — must pass before task 6

### Task 6: Visual check at 80/120 columns, both variants, ladder verdict

**Files:**
- Create (throwaway): `internal/model/zz_vis_test.go` — deleted in this same task

- [ ] render a fixture README (headings H1–H6, go + shell fences, nested/ordered/task
      lists, a quote, `---`, a table) through `renderReadme` at 80 and 120 columns,
      **dark and light**; read the output
- [ ] judge the `#08605f` contrast on dark and the `#a2ad59`/`#8e936d` contrast on
      light; if the inverted ladder reads wrong, show the user both orders and let them
      pick (5-line var edit either way)
- [ ] confirm the divider spans the wrap width, the fence plate has no punch-throughs
      around recolored tokens, checkboxes and depth markers align
- [ ] delete `zz_vis_test.go`
- [ ] run the full suite: `go test -race ./...`

### Task 7: Verify acceptance criteria

- [ ] every element of the Solution Overview table is implemented or carries its
      recorded deliberate-absence comment (list markers, tables)
- [ ] every new assertion has survived its mutation check (Task 1's hex-shape smoke
      guard exempted, as recorded there)
- [ ] `go vet ./...` and `golangci-lint run` clean
- [ ] full suite green: `go test -race ./...`

### Task 8: [Final] Update documentation

- [ ] `docs/design/readme-pipeline.md`: rewrite the `keepkitStyle` section — the heading
      ladder and its palette, the chroma palette, the width parameter, the divider, the
      theme-switch contract change, the two deliberate absences; keep the existing
      rationale style (what broke / why this shape)
- [ ] `CLAUDE.md`: `internal/ui` package row mentions the second non-role palette beside
      `LanguageColor`; the readme_style summary line follows the new shape (run
      docs-sync)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- run the real TUI (`go run .`) on a dark terminal with a README-heavy tool selected;
  eyeball the ladder, fences and dividers at a real 80×24 window
- preflight before commit/PR (build / vet / race tests / lint — the CI matrix)

**External:**
- demo-gifs: panel `[3]` is visible in `demo/hero.gif` — ask the user whether to
  regenerate after merge (never regenerate without asking)
- light-background terminals keep glamour's standard light chroma and stock H6, and get
  the H1–H5 ladder as-is; the light render in Task 6 is where that is judged, and the
  palette file is the one place to touch if it ever needs to fork by background
