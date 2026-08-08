# Panel `[3]`'s preprocessor vs glow: what each side does to a README

This is a research snapshot, not a design doc: it records how keepkit's markdown
preprocessing (`internal/model/readme_clean.go`, `cleanReadmeMarkdown`) differs from what
glow does before glamour, and why the two tools made opposite choices. It answers one
question — "why does keepkit carry ~650 lines of cleanup that glow lives without?" — and
documents nothing the code must keep true. Verified against glow master, glamour v1.0.0
(the version in `go.mod`) and keepkit as of 2026-08-08.

## The architectural difference

**glow has no content preprocessor.** Its entire pre-glamour surface is
`utils.RemoveFrontmatter` (the CLI path strips a YAML frontmatter block) and
`utils.WrapCodeBlock` (a non-markdown file is wrapped in a fence so it renders as one code
block). Everything else is glamour *options* — `WithWordWrap`, `WithPreservedNewLines`,
`WithBaseURL`, a style — i.e. decisions about how to show constructs, not rewrites of the
document.

That works because glow's decisions run **after parsing, on the goldmark AST**: glamour's
ansi renderer maps each node kind to an element and styles it. keepkit's pass is the
opposite — a **pre-parse string/regex rewrite** whose output is then handed to glamour.
The trade is explicit:

- keepkit pays for parser guarantees by hand: `rcSegments` re-derives fenced-block
  boundaries, `rcMaskSpans` NUL-masks inline spans, and the "code is never rewritten" rule
  needs its own machinery. glow gets all of that free — a rule that only exists as node
  styling can never see the inside of a code node.
- In exchange, keepkit can do what node styling cannot: delete a construct *differently
  depending on content* (unwrap `[text](url)` but keep `<https://…>` autolinks, which
  glamour renders through the same Link primitive — the reason a style-level route was
  rejected in [`readme-pipeline.md`](../design/readme-pipeline.md)), drop whole lines
  (badge headers, link-reference definitions, the title block), and collapse the blank
  runs the removals leave behind.

## Construct by construct

"glow" below means bare glamour v1.0.0 behavior, since glow adds nothing of its own.

| Construct | glow (= glamour v1.0.0) | keepkit |
|---|---|---|
| `![badge](url)` | alt text **plus the full URL** (`ansi/image.go` renders the `ImageText` format, then the destination) — a ten-badge header becomes a stack of shields.io URLs | removed whole, every form incl. reference/shortcut (`mdImageRe`, `rcRefRe`, `rcShortcutRe`): no image can render in a TTY |
| `[text](url)` | text **plus the href** (`ansi/link.go`; `SkipHref` exists but only fires for table footnote links) | unwrapped to the text, href dropped — panel `[3]` links are not clickable; autolinks and bare URLs kept (there the URL *is* the content) |
| Raw HTML | `HTMLBlock`/`RawHTML` nodes pass through `bluemonday.StrictPolicy` (`ansi/context.go`) — tags stripped, inner text kept | own pass: comments whole, `<picture>`/`<video>`/`<audio>`/`<svg>`/`<script>`/`<style>` **with bodies**, every other tag stripped via the `rcHTMLNames` allowlist (unknown names left as written) |
| Emoji / `:shortcodes:` | real emoji pass through as text; glow does not enable `glamour.WithEmoji()`, so `:rocket:` stays literal | SMP pictographs stripped, `✅→✓` / `❌→✗` / `☑→✓` translated, shortcodes cut only when `definition.Github()` knows the name |
| Leading H1 + tagline | untouched | `rcDropTitleBlock`: the card already shows the name and description one panel over |
| Control characters | not cleaned — glow reads trusted local files | `cleanTerminalOutput` runs first: the input is an untrusted API blob headed for a viewport that re-emits it |
| Frontmatter | removed (`utils.RemoveFrontmatter`) | not handled — GitHub READMEs do not carry it |

## Observations

- **bluemonday is already in keepkit's dependency tree.** glamour v1.0.0 depends on it and
  sanitizes HTML nodes itself, so `microcosm-cc/bluemonday v1.0.27` sits in `go.mod` as an
  indirect dependency. The design docs' "no general sanitizer (bluemonday) is pulled in"
  predates glamour v1.0.0 and is now stale in its implication — still accurate in the
  narrow sense that keepkit's own pass does not *use* it.
- **The two HTML passes partially overlap.** For plain tags (`<kbd>`, `<div>`, `<p>`)
  glamour now does roughly what keepkit's tag strip does. keepkit's pass is still
  load-bearing for what glamour cannot do: cutting `<picture>`/`<svg>` **bodies** (the
  StrictPolicy keeps inner text, so an inlined SVG logo would leak its text nodes),
  removing comments *before* link-reference labels are collected (a commented-out
  definition must not gate the unwrapping), and running before the parser at all — the
  label set and the line drops depend on seeing the raw document.
- **Why glow can afford to print URLs**: it is a full-screen pager where a URL is content
  the user came to read — and on glamour master (not yet in v1.0.0; no `makeHyperlink`
  there) links additionally become OSC 8 hyperlinks, i.e. actually clickable. Panel `[3]`
  is a ~30-column strip beside the card where every href costs two wrapped lines of noise,
  and `[o]`/`[c]` plus the card's clickable lines already cover "open it".
- **Different performance budgets.** glow renders inside an async `tea.Cmd` and may take
  its time; keepkit's pass runs synchronously inside `Update()` — which is where the
  512 KiB cap, the `rcLineContent` slicing fix (8.4 s → milliseconds on adversarial
  input) and `readmeRenderCache` memoization come from.

## Why keepkit cannot simply adopt glow's shape

Dropping the preprocessor and leaning on glamour alone would put badge URL stacks, hrefs
after every link, literal shortcodes and tofu pictographs back on a 30-column panel fed
untrusted remote content on the UI thread. The pieces glamour v1.0.0 did catch up on (tag
stripping) are the cheapest part of the pass; the parts that make the panel readable —
image and href removal, title-block drop, emoji policy, blank-run collapse — are exactly
the ones that need content-dependent rewriting the AST-styling layer cannot express.
