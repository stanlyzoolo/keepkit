# Brief-panel tweaks, clickable links, and tag grouping

## Context

Five small-to-medium UX corrections requested for keepkit, agreed during a brainstorm:

1. **`installed` tag glyph** — the `latest:` line already prints a Nerd Font tag glyph U+F412 (`nf-oct-tag`) before the version; `installed:` should get the same glyph before its version for visual parity.
2. **Select/copy — dropped** (YAGNI, out of scope).
3. **Clickable links** — a mouse click on the `repo:` line or the changelog URL line in `[2] Brief` should open that URL in the browser (like the existing `o`/`c` keys). No visual styling of links (no underline/bold/italic). README (`[3]`) links are out of scope.
4. **README Table of Contents** — add a TOC generated from existing headings.
5. **Tags** — change the model from multi-tag (comma-separated `[]string`) to **one tag per tool**, and add a **toggleable "group tools by tag" list view** (default off = current behaviour).

The single-tag model was chosen specifically to remove the multi-tag grouping ambiguity (no duplicate rows, no first-tag-wins heuristic).

Convention for this plan: the U+F412 glyph is written as `\uf412` everywhere (prose and snippets), never as a raw character — the raw glyph is invisible in most editors/diffs and gets silently lost or misread (it already happened to this plan's own first draft during review).

---

## 1. `installed` tag glyph (trivial)

**File:** `internal/model/render.go`, the `installed != ""` case (~line 891).

Change:
```go
sb.WriteString(ui.InfoStyle.Render("installed: "+installed) + "\n")
```
to insert the same glyph the `latest:` line uses. `render.go:914`'s real format is `"latest: \uf412 "+card.Latest` — `latest:` + space + U+F412 + space + version (the glyph lives in the *source* string as a raw char there; write ours as the escape). Mirror it exactly:
```go
sb.WriteString(ui.InfoStyle.Render("installed: \uf412 "+installed) + "\n")
```
Apply **only** to the resolved-version case. Leave `✕ not found` (line 893-895) and `detecting…` (line 897) untouched — no version to tag. Whole string stays in `InfoStyle`, matching the no-update `latest:` branch.

**Semantic note (deliberate change):** today U+F412 appears only on `latest:` — it is the *release-tag* icon, and the existing test `render_test.go:2595-2608` ("card without a release: no latest line, no tag icon") pins that the glyph does **not** appear on the `installed:` line. This plan deliberately widens the glyph's meaning from "release tag" to "version marker"; record that in CLAUDE.md's card-versions bullet during the docs pass.

**Tests:** update `render_test.go:2600` (the `installed: v1.0.0` literal gains the glyph) and `render_test.go:2606` (the "no tag icon anywhere in the card" assertion must be rescoped to the `latest:` line or dropped — with the new semantics it would fail by design); additionally assert the glyph precedes the installed version in a resolved-version card.

---

## 2. Clickable `repo:` / changelog URL in `[2] Brief`

**Crux (from exploration):** `renderCard()` returns a plain string with no line-index metadata; the repo line (`render.go:879`, `repo: <t.GitHub>`) is written directly in the card builder, while the changelog URL line is written **inside `renderChangelogBlock`** (`render.go:1008-1009`, first line of the block when `msg.htmlUrl != ""`) — the card builder only appends that block's rendered result. Line heights vary (dividers = 3 lines, `wrapText` on about/note/tags/changelog body). `bubbles/viewport` is line-based (no soft-wrap), so a content-line index equals a viewport line.

**Approach — single source of truth, zero churn at the ~30 `SetContent(renderCard())` sites:**

- Refactor the current `renderCard()` body into `buildCard() (string, map[int]string)`. Record 0-based content-line indices via `lineIdx := strings.Count(sb.String(), "\n")`:
  - **repo line** — computed immediately before writing the repo line; maps to `"https://" + t.GitHub` (same resolution the `o` handler uses, `model.go:1190-1199`).
  - **changelog URL line** — the URL is written inside `renderChangelogBlock`, so the index is computed in `buildCard` *after the divider, immediately before appending the block's content*, and recorded **only when the block actually starts with the URL line** (`msg.htmlUrl != ""`; the loading/empty states emit no URL line — recording unconditionally would be an off-by-one onto body text). Maps to `msg.htmlUrl` **verbatim** (already has a scheme; note this differs from the `c` key, which opens `t.GitHub + "/releases"`).
- Keep `renderCard() string` as a thin wrapper: `s, _ := m.buildCard(); return s` — existing callers unchanged.
- **`handleMouse`** (`render.go`, brief branch ~1058-1067): on `MouseButtonLeft` press in `modeNormal`, after `setFocus(focusBrief)`, compute `line := msg.Y - 2 + m.briefViewport.YOffset` (same `-2` offset as the tools branch: 1 top margin + 1 border), then `if url, ok := links[line]; ok { return m, openURLCmd(url) }` where `_, links := m.buildCard()`. Recomputing on click is negligible and always in sync.

Reuse `openURLCmd`/`browserCommand` (`browser.go`) as-is.

**Test:** table test on `buildCard()` asserting the repo/changelog line indices resolve to the expected URLs across card variants (with/without changelog, changelog loading vs loaded, with/without update); a `handleMouse` test that a click on the repo line's Y returns an `openURLCmd` for the repo URL and a click elsewhere does not.

---

## 3. Tags → single tag per tool

Keep the `Tags []string` field (**no `meta.yaml` schema break**) but hold the invariant **len ≤ 1**.

- **Migration** — `internal/loader/meta.go`, `LoadMeta`, inside the existing `for i := range meta` migration loop (meta.go:73-81, next to the status migration): `if len(meta[i].Tags) > 1 { meta[i].Tags = meta[i].Tags[:1] }`. In-memory only; disk keeps the old list until the next `SaveMeta` (same property as the status migration). Mirror `TestLoadMetaMigratesRetiredStatuses` / `TestLoadMetaMigrationRoundTrip` (meta_test.go:204-284) with a multi-tag fixture.
- **Editor** — `internal/model/mode.go` `updateTagsEdit` (mode.go:99-107): stop splitting on `,`; store the whole trimmed input as one tag: `raw := strings.TrimSpace(...); mt.Tags = nil; if raw != "" { mt.Tags = []string{raw} }`.
  - **Semantics gap to resolve at impl time (see open detail below):** the migration takes the *first list element* (`Tags[:1]`), while the editor stores the *entire trimmed string* — typing `cli, foo` yields the single tag `"cli, foo"`, which then renders as `#cli, foo` in the search suffix and becomes a grouping key in section 4. Whichever way the open question is decided, the decision must be recorded (CLAUDE.md) and covered by a test — the two paths must not silently produce different tag shapes for the same user intent.
- **Status-bar hint** — `render.go:78-79`: `"comma-separated"` → `"single tag"`.
- **Placeholder** — `model.go:285`: `"tag1, tag2..."` → `"tag"`.
- Seed (`model.go:1136`, `strings.Join(mt.Tags, ", ")`) and card render (`render.go:949`) keep working unchanged for a ≤1-element slice.
- Search-by-tag (`matchingTag`, `model.go:1347-1355`) and the `#tag` suffix (`render.go:627-631`) are unaffected.

**Tests to update:** `TestSaveMetaLoadMetaRoundTrip` (meta_test.go:60, multi-tag literal), `TestTagsEditCommit` (mode_test.go:122-139, comma-parse expectation → single tag), **and `TestSearchMatchesByTag` (mode_test.go:788-813)** — its fixtures are two-element (`{"fuzzy","finder"}`, `{"git","TUI"}`) and it deliberately asserts a match via the *second* tag ("matches only via its TUI tag"). It would keep passing (the constructor doesn't enforce the invariant), but it models a state impossible after migration — convert the fixtures to single-tag while keeping a by-tag-match assertion.

---

## 4. Tag-grouping toggle (largest piece)

**Key insight (keeps the change bounded):** do **not** turn `metaSelected` into a render-row. Keep it a **tool index into `filteredMeta()`**; grouping is just a *reordering* of `filteredMeta()` (exactly what update-grouping already does). Non-selectable header rows are handled only in the three **tool-index ↔ screen-line** translation points, via a map that is the identity when grouping is off — so navigation (`j/k/g/G/PgUp/Dn/ctrl+d/u`), `selectedMeta()`, `indexOfMeta`, and every index-writing site stay untouched.

- **State:** add `m.groupByTag bool`. Toggle bound to **`space` in `focusTools`** — currently the reserved no-op (model.go:999-1005). The toggle **reorders `filteredMeta()`**, so `m.metaSelected` (an index into that projection) would silently point at a different tool; use the project's standard "cursor follows the tool, not the row" remap (same pattern as the `installedMsg`/`remoteMsg` handlers, model.go:402/463):
  ```go
  name := ""
  if mt, ok := m.selectedMeta(); ok { name = mt.Name }
  m.groupByTag = !m.groupByTag
  if name != "" { m.metaSelected = m.indexOfMeta(name) }
  m.setToolsContent()
  ```
  (empty list: skip the remap, just toggle + repaint). Deliberately **not** via `selectMeta` — no auto-fetch on a pure view toggle.
- **Ordering:** in `searchMatches()` (`model.go:1311-1345`), when `m.groupByTag && query == ""`, order matches grouped by the tool's single tag (stable, `meta.yaml` order within a group), untagged tools last. In this mode the update-partition is **not** applied (grouping wins; the `↑` marker still renders per row). When off, behaviour is exactly as today. Gating on empty query keeps `/` search behaviour identical.
- **Header rows + line maps:** walk the grouped matches; emit a non-selectable header line (`#<tag>` / `#untagged`, styled like `SectionLabelStyle` or `MetaNoteStyle`) whenever the group changes, then the tool row. In the same walk build:
  - `toolLine []int` — screen line per tool index.
  - `lineTool []int` — tool index per screen line (`-1` for a header).
  When `!groupByTag`, no headers → identity maps.
  **Where the maps are built:** `renderLeftContent` (render.go:596-661) has a **value receiver** `(m Model)` and returns only a string — it physically cannot assign model fields. Build the maps in a **pointer-receiver** method: either directly in `setToolsContent` (render.go:699-702) or in a new builder it calls that returns `(content string, toolLine, lineTool []int)` and stores the maps.
  **Every site that repaints the list content must go through `setToolsContent()`** so the maps can never go stale. Known offender: the `WindowSizeMsg` handler (`model.go:743-744`) currently calls `m.toolsViewport.SetContent(m.renderLeftContent())` directly and then `syncToolsViewport()` — after a resize the maps would be stale (wrong scroll or out-of-range index). Route it through `setToolsContent()` as part of this section.
- **Translation points using the maps:**
  - `syncToolsViewport` (render.go:686-696): replace `m.metaSelected` with `toolLine[m.metaSelected]` in **both** branches — the top-clamp comparison *and* the bottom formula `m.metaSelected - vpH + 1` (which becomes `toolLine[m.metaSelected] - vpH + 1`); mixing tool-index and screen-line units in either branch breaks scrolling as soon as headers exist.
  - `handleMouse` tools branch (render.go:1047-1054): `line := msg.Y-2+YOffset; if line in range { toolIdx := lineTool[line]; if toolIdx >= 0 { selectMeta(toolIdx) } }` (clicks on headers ignored).
- **Hotkeys/hints:** add `space — group by tag` to the `[?]` overlay tools group (render.go:501-509) and, if it fits the one-line budget, a hint on the `focusTools` bar (render.go:183-191). Respect `TestStatusBarNeverWraps` **and `TestRenderHotkeysSizeBudget` (render_test.go:3252-3274, frame height ≤ 20 rows)** — the `[1] Tools` group is currently 7 rows; the 8th row puts the tallest column's frame right at the 20-row edge (CLAUDE.md already warns a 7-row group pairs past the budget in some column partitions). After adding the row, run the test; if it overflows, shorten the description (`space — group by tag` is already near-minimal) or rebalance which groups share a column.

**Tests:** grouped ordering from `searchMatches` (tags + untagged-last, update-partition suppressed); `toolLine`/`lineTool` maps identity when off and header-aware when on; **`space` toggle keeps the same tool selected (remap by name) in both directions, including when the selected tool's group position changes**; `syncToolsViewport` scrolls to the selected tool's true screen line under grouping; a mouse click on a header row is a no-op while a click on a tool row selects it; `j/k` still visits every tool (never lands on a header); the resize path (`WindowSizeMsg`) rebuilds the maps (stale-map regression test); `TestRenderHotkeysSizeBudget` still passes with the new overlay row.

---

## 5. README Table of Contents (trivial)

**File:** `README.md`. No TOC or anchors exist today. Insert a `## Contents` list between the hero GIF and the `## Features` heading (verify exact line numbers at impl time — they are unverified and README may drift), linking to the existing H2 sections (GitHub-style lowercase-hyphen anchors): Features, Installation, Usage, Updating tools, GitHub API and token, Data storage, Architecture, Stack, Contributing, License. (Optionally nest the three `### Panel` subsections under Usage.)

---

## Docs to update (after implementation)

Run the `docs-sync` skill — these touch documented surfaces:
- **CLAUDE.md** — tag model is now single-tag (record the editor-vs-migration semantics decision); new `space` grouping toggle + the tool-index↔screen-line map + the "all list repaints go through `setToolsContent`" invariant; clickable brief links in `handleMouse`; `installed` glyph (record the widened U+F412 semantics in the card-versions bullet).
- **ARCHITECTURE.md** — same surfaces if described there.
- Regenerate demo GIFs only if the grouped view is worth showing (`demo-gifs` skill) — optional.

## Verification

1. `go build .` and run `go run .` against a real `~/.config/keepkit/meta.yaml`.
   - Brief shows the U+F412 glyph before the installed version.
   - Click the `repo:` line → browser opens the repo; click the changelog URL line → opens that release page; clicks elsewhere do nothing.
   - Edit a tag with `t`: typing `cli, foo` stores the whole string as one tag (or first token — per the open-detail decision); the card shows a single tag.
   - Press `space` in the tools panel → list groups under `#tag` / `#untagged` headers; the **same tool stays selected** across the toggle; `j/k` skips headers; clicking a header does nothing; clicking a tool selects it; the selection scrolls correctly after the toggle **and after a resize**; `space` again returns to the flat update-grouped view.
   - Pre-seed a `meta.yaml` with a multi-tag entry → after launch it shows one tag; after any `SaveMeta` (e.g. edit note) the file is rewritten to a single tag.
2. `go test -race ./...` — all green, including the new tests above.
3. `go vet ./...` and `golangci-lint run`.
4. Run the `preflight` skill before commit.

## Open detail to confirm during implementation
- Single-tag editor: treat the entire trimmed input as one tag (may contain spaces), or take only the first whitespace/comma-delimited token? Plan assumes **whole trimmed input = one tag**; adjust if a token is preferred. Whatever is chosen: align the migration (`Tags[:1]`) and the editor to produce the same tag shape, record it in CLAUDE.md, and pin it with a test (see section 3).
