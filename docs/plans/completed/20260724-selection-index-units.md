# Follow-up: pre-existing selection-index and upsert defects

Split out of the review of `feat/brief-glyph-links-tag-grouping` (2026-07-24). All five
predate that branch — the tag view only made them easier to hit — so they were left out
of it deliberately. Each was reproduced with a probe during that review.

## 1. `track` and `rename` write file-order indices into `metaSelected`

`internal/model/mode.go:172` (`updateTrackInput`) and `:284` (`updateRenameInput`) both
scan `m.meta` and assign the resulting index to `m.metaSelected`. But `metaSelected`
indexes `filteredMeta()` — the *displayed* projection, which the update partition has
reordered since long before tag grouping existed. Whenever a tool with a pending update
sits above the target in display order, the cursor lands on a different tool: the card,
panel `[3]` and `autoFetchCmdsForSelected` all follow the wrong one.

Fix: use `m.indexOfMeta(name)` (already the rule for the async merges and the `space`
toggle) instead of the manual scan, in both handlers.

## 2. Re-tracking an existing tool wipes its metadata

`trackTool` (`internal/model/mode.go:147`) builds a fresh `ToolMeta` and hands it to
`loader.UpsertMeta`, which replaces the stored entry wholesale — `Tags`, `Note`, `Status`
and `Added` are reset even though the handler reports `already tracked` and the user
expects a no-op (a common way to attach a GitHub ref to a name-only entry).

Fix: when `FindMeta` reports the name is present, merge into the existing entry —
overwrite `GitHub` only when the input carried one, and keep status/tag/note/added.

## 3. A wrapped tool name desyncs the list's line maps

`buildToolRows` (`internal/model/render.go:702`) records one screen line per tool, but the
row it writes is `wrapText(mt.Name, m.toolsW-5)`, which spans two lines for a name longer
than the name column (9 cells at the 80x24 baseline). Clicks below such a row resolve to
the neighbouring tool or to nothing, and `syncToolsViewport` scrolls to the wrong line.
The same class of bug for group headers is already prevented by `tagHeaderLine`.

Fix: truncate the name to one line (`truncateToWidth`, as the headers do) — or teach the
maps that a row can be several lines tall. The first is far simpler and matches how the
`#tag` suffix already gets dropped when the row budget is tight.

## 4. A tools-panel click reads `YOffset` after `setFocus` rewrote it

`handleMouse` (`internal/model/render.go:1172`) calls `m.setFocus(focusTools)` — which
repaints and re-clamps the scroll position — and only then maps the click row against
`m.toolsViewport.YOffset`. After a wheel scroll from another panel the offset snaps back
to the selection before the row is resolved, so the click selects the tool that happened
to move under the cursor.

Fix: capture `YOffset` (or resolve the row) before the `setFocus` call.

## Verification

`go test -race ./...`, plus a regression test per item — the click and paging cases are
cheap to pin with the `pagingModel` helper added in `internal/model/group_test.go`.

## Outcome (2026-08-10)

Items 1-3 landed and are in `main`:

- **1** — both handlers go through `m.indexOfMeta(name)` (`internal/model/mode.go:196`,
  `:304`), with the reason stated in a comment at the first site.
- **2** — `trackTool` carries `Note`, `Tags`, `UpdateCmd`, `Added` and a ref-less
  input's `GitHub` forward from the existing entry; the status reset to `trying` is
  the one deliberate change.
- **3** — `buildToolRows` truncates instead of wrapping
  (`truncateToWidth(flattenLine(mt.Name), nameBudget)`, `internal/model/render.go:1225`).

**Item 4 is closed unfixed, by the owner's call.** `handleMouse` still reads
`m.toolsViewport.YOffset` after `setFocus(focusTools)` has re-clamped it
(`internal/model/render.go:2230-2235`), so a click that follows a wheel scroll from
another panel can select a neighbouring tool. It is not tracked anywhere else — if it
starts to bite, this section is the record of what it is and where the fix goes
(capture the offset, or resolve the row, before the `setFocus` call).
