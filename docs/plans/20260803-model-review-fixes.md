# internal/model review fixes

## Overview

Fix the confirmed findings of the 2026-08-03 four-reviewer audit of `internal/model/`, per the owner's per-item directives:

- **taken**: №1 (re-track data loss), №2 (cursor index space), №4 (mdInline rewrites inline code), №5 (generic HTML strip eats prose), №6 (changelog block width), №7 (CodeBlock theme override unreachable on color terminals — fix by repainting Chroma under the theme), №8 (in-flight status killed by a stale timer — investigate first, then fix), №9 (browser zombie processes), №10 (release-less repos — solve via a GitHub *tags* fallback plus a model-side settle), №11 (fence close marker mismatch), №14 (`46.0k` → `46k`, `46.2k` must survive), №15 (doc drift).
- **feature removal instead of a fix**: №3 — the help/man search (`/` from `[2]`/`[3]`, `modeHelpSearch`, `n`/`N`, match highlight/counter) is removed entirely; the owner considers the feature unnecessary.
- **ignored**: №12 (inline-span false fence open in the README cleaner), №13 (byte-vs-rune separator width in the language list).

Revised 2026-08-04 after plan-review: Task 10 split into version-side and model-side halves; №7's premise corrected (the StyleBlock override is the Ascii/no-color path, not dead code — the fix narrows to a bounded Chroma repaint); №9 switched to `Start()` + `go Wait()`; Task 2's removal inventory extended to `ARCHITECTURE.md` and the shared `/` case; doc sweep extended to `README.md`.

Revised again 2026-08-04 after the second plan-review pass: the tag write is **gated off a preserved release tuple** (a tag over a deleted release's cached notes would render a hybrid card); the card-consequence assertion moved to the model-side task; the rate-limit trap-case shape pinned exactly; the Chroma repaint stated **dark-only**; the `[?]` overlay `/`-row move made definite; `CLAUDE.md`'s `mdInline` order sentence added to Task 3.

## Context (from discovery)

- Findings were produced and *verified against code* during the review session; the full report lives in the session transcript. File anchors below are from `main` @ `5e725b2`.
- Work happens in the existing worktree `.claude/worktrees/fix+model-review-findings` (branch `worktree-fix+model-review-findings`).
- A draft patch with already-written-and-tested versions of №1, №2, №4, №5, №8, №14 (plus №13, which is now ignored, and a `Run()` variant of №9 superseded by this plan — do not reapply those hunks) is saved at the session scratchpad as `review-fixes-draft.patch`. Reuse hunks selectively; every reused hunk still goes through this plan's TDD order (failing test first, then apply).
- Key invariants that constrain the fixes are in `CLAUDE.md` (single projection point `searchMatches()`, `metaSelected` is an index into `filteredMeta()`, cache-mutation rules in `internal/version`, the `e := existing` cache-entry style, `applyReleaseOutcome` as the one definition of what a release fetch does to a cache entry, the `CheckedAt` poison-guard).
- Test seams: `loader.SetConfigDirForTesting`/`version.SetConfigDirForTesting`/`logx.SetDirForTesting` are already installed in `TestMain`; `version` tests use `testAPIBase` + `httptest` for API fixtures (`internal/version/github_test.go`); `model` tests must never execute network commands (warm-cache pattern instead) and must never invoke `openURLCmd`'s command (`browser_test.go` states this explicitly).
- Docs to keep in sync are a **trio**: `CLAUDE.md`, `docs/design/*`, **and `ARCHITECTURE.md`** — the third has been forgotten by three completed plans in a row and caught only in review each time.

## Development Approach

- **testing approach**: TDD — every fix starts with a failing test that reproduces the defect, then the fix, then green. The one documented exception is Task 8 (№9): `browser_test.go` forbids invoking the command, so the task ships with existing tests kept green and no new executing test.
- complete each task fully before moving to the next
- make small, focused changes; one logical unit per task
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task (Task 8's exception is stated above and in the task)
- **CRITICAL: all tests must pass before starting next task** (`go test -race ./...`)
- **CRITICAL: update this plan file when scope changes during implementation**
- maintain backward compatibility of `meta.yaml` / `cache.json` formats (additive changes only)

## Testing Strategy

- **unit tests**: required for every task; `go test -race ./...` is the gate (the repo's CI matrix).
- no e2e framework in this repo; the closest equivalent is rendering tests over `View()`/`renderX()` output — use them for №6 and the №3 removal.
- before the final PR: run the `preflight` skill (build / vet / `test -race` / golangci-lint — the exact CI matrix). Remember: preflight can red-flake from antivirus; re-run before diagnosing.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep plan in sync with actual work done

## Solution Overview

Four groups, ordered so the removal (№3) lands before fixes that would otherwise touch the code being deleted:

1. **Tracker correctness** (№1, №2): merge semantics in `trackTool` so a re-track preserves user-authored fields; cursor remap through `indexOfMeta` (the displayed projection) in the track/rename commit handlers.
2. **Feature removal** (№3): delete the help search wholesale — mode, keys, state, renderer branches, helpers — and update the doc trio accordingly.
3. **Converter/render/plumbing fixes** (№4–№11, №14): mask inline code and reuse the README allowlist in the card converter; single width definition (`cardWidth()`) for the changelog block; a bounded theme repaint of glamour's Chroma for README code fences; seq-bumping sticky statuses; reaped browser openers; marker-matched fence closing; `TrimSuffix(".0")` stars. №10 extends `internal/version` with a tags fallback so release-less-but-tagged repos get a real `latest` (version-side task), and `needsRemote` stops re-dispatching for conclusively answered tools (model-side task).
4. **Doc sync** (№15): stale comments, `CLAUDE.md`/`ARCHITECTURE.md`/`README.md` lines, folded into each task where the code changes, plus a final sweep.

## Technical Details

- **№1**: `trackTool` builds the fresh entry, then, when `loader.FindMeta` hits, carries `Note`/`Tags`/`UpdateCmd` forward, keeps `GitHub` when the input had none, keeps a non-empty `Added`. `Status → trying` stays (pinned by `TestTrackTool` and defensible as "trying again").
- **№2**: both commit handlers replace their raw `m.meta` index loops (track: `for i, mt := range m.meta` at mode.go:179; rename: `for i, e := range m.meta` at mode.go:288 — note the different loop variables, a literal grep for one finds one of two) with `m.metaSelected = m.indexOfMeta(name)`; `updateTagsEdit` (mode.go:110-127) is the in-repo reference idiom.
- **№3**: remove `modeHelpSearch` from the `inputMode` enum and its dispatch; remove `m.helpSearch`, `m.helpMatches`, `m.helpMatchIdx`; remove the search branch in `helpContent()` (render.go:2414-2432) and the `renderStatusBar` branch (render.go:47-63 — note: this *is* the `N/M matches` surface; the `[3]` panel footer never carried one). The `case "/"` at model.go:1706 is **shared** — today the brief/help branch returns in both sub-paths; *deleting* it would let `/` from `[2]`/`[3]` fall into the tool-list-search entry below, so the change is *adding a `focusTools` guard*, not deleting the case. Remove `findMatches`/`highlightMatch` (textutil.go) after a caller inventory. Drop `modeHelpSearch` from `flushPendingLaunch`'s funnel list and from the `[?]` overlay if listed. `/` stays bound only in `focusTools`.
- **№4/№5**: `mdInline` masks spans via `rcMaskSpans` before any rule, strips unpaired backticks while spans are still masked, restores spans with delimiter runs cut (`mdSpanBody`); `mdHTMLTagRe` is deleted and the strip goes through `rcHTMLTagRe` (the `rcHTMLNames` allowlist). Two known gaps to document side by side in the doc comment: `mdCutComments` runs upstream and still cuts a comment inside a span; `mdInline` is called per source line, so a span split across source lines is not masked (same as today).
- **№6**: `renderChangelogBlock` uses `inner := max(m.cardWidth(), 10)` instead of `max(m.briefW-2, 10)`; feed `TestPanelsKeepTheirGutter` (render_test.go:4862) a changelog body with a code fence so the gutter test actually exercises the block; fix the contradicting `CLAUDE.md` "Card changelog body" sentence.
- **№7** (premise corrected by plan-review): glamour v1.0.0 renders a code fence through `rules.Chroma` when `ColorProfile != termenv.Ascii`, and through the `StyleBlock` fields otherwise — **both paths are live**: the StyleBlock override is what `NO_COLOR`/dumb terminals and this package's own tests exercise, so it stays. The fix is a **bounded** Chroma repaint: clone `cfg.CodeBlock.Chroma` into a fresh struct (the glamour globals alias), set `Chroma.Background`'s background → `t.Surface`, `Chroma.Text`'s color → `t.Emphasis` and background → `t.Surface` (chroma's formatter emits background per token — without it the plate is patchy), inherit every other token entry unchanged. No "where a role fits" judgement calls. Caveat that must go into the comment and docs: glamour registers the built chroma style under the **process-global, one-shot name `"charm"`** (guarded by a registry-presence check), so the first render in a process wins the slot — which is why the only reliable assertion is struct-level, and why a mid-session theme switch would not repaint fences (acceptable: `m.darkBG` is resolved once at construction anyway). The repaint is **dark-only**: every `CodeBlock` override sits after the `if !dark { return cfg }` early return (readme_style.go:70), and the light variant deliberately keeps the stock palette — `t.Surface`/`t.Emphasis` are chosen against a dark panel, and readme_style_test.go:105-110 already guards the light side.
- **№8**: investigation first: a failing test proving whose timer clears the in-flight status. Hypothesis from the review (to confirm): `setStatus` (model.go:2010) bumps `statusSeq` and arms a TTL tick; the two in-flight sites (mode.go:341, 359) assign `m.statusMsg` directly *without* bumping, so the earlier tick's `seq` still equals `m.statusSeq` and the expiry handler wipes the sticky message. Fix: a `setStickyStatus` helper (bump seq, no timer), used by both sites. Test drives the real sequence: transient status → launch dispatch (use the `t.Setenv("TMUX", …)` pattern from launch_test.go:183 to get a non-fallback plan) → deliver the stale `statusExpiredMsg` → sticky message must survive.
- **№9** (decision): `Start()` + `go cmd.Wait()` — reaps the child without holding the `safeCmd` goroutine for the opener's lifetime (a shell-wrapper `xdg-open` can outlive the click), and keeps `openURLMsg.err` meaning "could not launch": `Run()` would turn a non-zero opener exit into a new visible `setStatus` path (model.go:1099-1103), a behavior change nobody asked for.
- **№10 version-side**: new `fetchLatestTag(field)` — `GET /repos/{owner}/{repo}/tags` (first page), pick the max tag via `canonSemver`/`IsNewer` (fall back to the first entry when nothing canonicalizes; empty list → no answer). `getRepoData` calls it only when `fetchRelease` returned `errNoReleases`, and **after** the total-failure early return (github.go:462-467) — a rate-limited pass must not spend a tags request whose result is then discarded. Cache semantics, stated precisely:
  - the tag writes `Latest` only (no `Body`/`HtmlUrl`/`PublishedAt`), inside the **same** `updateCacheEntry` mutate, *after* `applyReleaseOutcome`, in `e := existing` style;
  - the tag write is **gated on the entry carrying no release tuple** (`Body`/`HtmlUrl`/`PublishedAt` all empty): `applyReleaseOutcome` deliberately *preserves* a deleted release's tuple on 404 ("known content wins"), and a tag written over it would render the new tag with the *old* release's notes and clickable URL — the exact hybrid `CLAUDE.md`'s self-check section warns about, which `getChangelog`'s cached-body gate would then serve for the rest of the window;
  - `ReleaseMissing` **stays true** (there is still no release — the self-check must stay quiet; `selfTagOf` keeps answering nothing);
  - the tags outcome does **not** participate in the `conclusive` decision (github.go:468-478): `relConclusive := relErr == nil || errors.Is(relErr, errNoReleases)` is unchanged, so a transient tags failure degrades to exactly today's behavior (conclusive-but-blank `Latest` for the window) instead of vetoing the stamp and re-spending the repo-info requests every launch;
  - `applyReleaseOutcome`'s doc comment gains a sentence: `Latest` now has a second provenance (a git tag), written beside it, never by it;
  - `github.go:468-478`'s comment and `CLAUDE.md`'s poison-guard/"Force refresh" wording updated in the same task. The `Refresh` path reuses the same `force` core.
  - Card consequence (pinned in the **model-side** task, whose gate actually runs it): a tag-derived `Latest` yields `↑` + an `enter` offer + no release date + "no release notes available." + **no clickable release heading** (render.go:1656-1668 already gates on `changelogURL != ""`).
- **№10 model-side**: today `errNoReleases` is already conclusive, so the per-visit cost is a goroutine + a `cache.json` read, **not** API quota — the settle is a hygiene fix, kept minimal. `m.repoStatus`-presence is **not** a usable marker (plan-review verified: a rate-limited pass carrying a stale card writes `"active"`/`"archived"` into `m.repoStatus` via model.go:998-1005, so presence would permanently suppress the retry). Therefore: a session-scoped `m.remoteAnswered map[string]bool`, initialized in `New()`, written by the `remoteMsg` handler **only when `msg.err == nil`**, checked by `needsRemote`; rename's stale-state cleanup deletes the old name (untrack deliberately does not — no sibling per-name map is cleaned there). The acceptance test must include the trap case in **exactly** this shape: `card.About != ""`, `latest == ""`, `repoStatus == "active"`, `err = ErrRateLimited` — the only shape where the rejected `repoStatus` marker would have suppressed a retry `needsRemote` still wants (a complete stale card also carries `Latest`, which makes `needsRemote` false today for an unrelated reason and would mask the assertion).
- **№11**: `markdownToLines` records the opening fence marker (char + run length, new `mdFenceOpenRe` with a capture) and closes only via `rcFenceCloses(line, marker)` — same semantics as the README pass; `mdFenceRe` goes away.
- **№14**: `formatStars` → `strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "k"`. Only a literal `.0` tail is cut: 46000→`46k`, 46200→`46.2k` (owner's explicit requirement), 1500→`1.5k`, 999→`999`.
- **№15**: model.go:995/1014/1089 comments say «press **L**» where the UI renders «press **a**» (render.go:1632/2240) — the same stale key is in user-facing `README.md:311` and in `render_test.go:4081`; CLAUDE.md "Known exceptions… `h`/`m` move only between brief/help" names retired keys (now `H`/`M`/`R`); `ARCHITECTURE.md:174` (mode enum) and `:235` (status-bar `/` rationale) are invalidated by the №3 removal. Every task updates the lines it invalidates, the final sweep catches the rest.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, and doc updates in this repo.
- **Post-Completion** (no checkboxes): PR/merge flow, manual TUI smoke test, demo GIF check.

## Implementation Steps

### Task 1: trackTool merge semantics + displayed-index cursor remap (№1, №2)

**Files:**
- Modify: `internal/model/mode.go`
- Modify: `internal/model/render_test.go`
- Modify: `internal/model/mode_test.go`

- [ ] write failing test `TestTrackToolRetrackPreservesFields` (render_test.go): re-track by bare name keeps Note/Tags/UpdateCmd/Added/GitHub, resets Status to trying; a ref-carrying re-track updates GitHub and keeps the rest
- [ ] write failing tests `TestTrackCommitCursorUsesDisplayedIndex` / `TestRenameCommitCursorUsesDisplayedIndex` (mode_test.go): with the update partition floating another tool to the top, the cursor lands on the tracked/renamed tool (the track case must use a *re-track* — a fresh add is last in both orders and cannot catch the bug)
- [ ] implement the merge in `trackTool` (carry user fields; keep GitHub on ref-less input; keep non-empty Added)
- [ ] replace both `for i, mt := range m.meta` remap loops with `m.metaSelected = m.indexOfMeta(name)`
- [ ] run tests — `go test -race ./internal/model/` must pass before task 2

### Task 2: remove the help/man search feature (№3)

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/mode.go`
- Modify: `internal/model/render.go`
- Modify: `internal/model/textutil.go`
- Modify: `internal/model/mode_test.go`, `internal/model/launch_test.go`, `internal/model/textutil_test.go`, `internal/model/render_test.go`, `internal/model/scroll_test.go` (wherever `modeHelpSearch`/`findMatches`/`highlightMatch` appear)
- Modify: `CLAUDE.md`, `ARCHITECTURE.md`

- [ ] grep-inventory every reference to `modeHelpSearch`, `helpSearch`, `helpMatches`, `helpMatchIdx`, `findMatches`, `highlightMatch` — the removal list is the inventory, not this plan's guess
- [ ] write/adjust tests first: `/` in `focusBrief`/`focusHelp` leaves the mode `modeNormal` **and** the status bar showing the global hints (`renderStatusBar` assertion — this is the surface the `N/M` counter lived on); `/` still opens `modeSearch` in `focusTools`; `n`/`N` in `[3]` are plain no-ops
- [ ] **add a `focusTools` guard to the shared `case "/"`** (model.go:1706) — bare deletion of the brief/help branch would let `/` from `[2]`/`[3]` fall into the tool-list-search entry below
- [ ] remove the mode member, its `Update` dispatch, the model fields, the `helpContent()` search branch, the `renderStatusBar` branch, and the now-unreferenced `findMatches`/`highlightMatch`
- [ ] update `flushPendingLaunch`'s funnel comment/list; **move the `/` row in the `[?]` overlay from the global group (render.go:723) to the `[1] tools` group** and reword its description to name the list filter — after the removal `/` is `[1]`-only, and the global group is the on-screen statement of the "same in every focus" rule (same column, so the size budget and `TestRenderHotkeysSizeBudget` are unaffected)
- [ ] update `CLAUDE.md` (input-modes list, status-bar rationale for `/`, help-navigation `/`-entry path, `clearHelpNav` wording) and `ARCHITECTURE.md` (:174 mode enum, :235 status-bar `/` sentence)
- [ ] run tests — full `go test -race ./...` (removal can break any panel test) before task 3

### Task 3: mask inline code and allowlist the HTML strip in the card converter (№4, №5)

**Files:**
- Modify: `internal/model/textutil.go`
- Modify: `internal/model/textutil_test.go`
- Modify: `CLAUDE.md`

- [ ] write failing `TestMdInline` cases: `` use `--output <path>` here `` keeps the argument; `` `a*b` and `c*d` `` does not fuse; `` ``a`b`` `` keeps the content backtick; `` `_private_field` `` untouched; `changed Vec<String> to Vec<Bytes>` and `contact <support@example.com>` survive in prose
- [ ] rewrite `mdInline`: `rcMaskSpans` first, rules on the masked text, strip unpaired backticks while masked, restore via `rcUnmaskSpans` with `mdSpanBody`-trimmed spans; replace `mdHTMLTagRe` with `rcHTMLTagRe`; delete `mdHTMLTagRe`
- [ ] add the NUL-drop at `markdownToLines` entry (the card body, unlike the README, never passes `cleanTerminalOutput` — a hostile body could forge mask placeholders)
- [ ] document the two known gaps side by side in the `mdInline` doc comment: `mdCutComments` upstream still cuts a comment inside a span; per-line invocation leaves a span split across source lines unmasked (same as today)
- [ ] update `CLAUDE.md`'s "Card changelog body" inline-order sentence ("images → links → **autolinks before the HTML-tag strip** → emphasis", generic strip) to the new reality: span masking first, then the rules, the strip is the `rcHTMLNames` allowlist
- [ ] run tests — must pass before task 4

### Task 4: fence close must match the opening marker (№11)

**Files:**
- Modify: `internal/model/textutil.go`
- Modify: `internal/model/textutil_test.go`
- Modify: `internal/model/readme_clean.go` (comment only — :30 references `mdFenceRe` by name)

- [ ] write failing test: `~~~` / code / ``` ``` `` / code / ``` ``` `` / `~~~` / tail — everything between the `~~~` pair is code, `tail` is body
- [ ] add `mdFenceOpenRe` (captures the run), track the open marker, close via `rcFenceCloses(line, marker)`; remove `mdFenceRe` and fix the `readme_clean.go:30` comment that names it
- [ ] run tests — must pass before task 5

### Task 5: changelog block wraps to cardWidth (№6)

**Files:**
- Modify: `internal/model/render.go`
- Modify: `internal/model/render_test.go`
- Modify: `CLAUDE.md`

- [ ] extend `TestPanelsKeepTheirGutter` (render_test.go:4862, failing first): seed `changelogData` for the selected tool with a body containing a code fence — the padded plate must respect the right gutter (on main it renders `briefW-1` cells against a `briefW-2` budget)
- [ ] change `renderChangelogBlock` to `inner := max(m.cardWidth(), 10)`
- [ ] fix the `CLAUDE.md` "Card changelog body" sentence that documents `max(m.briefW-2, 10)` (the panelGutter section's "no branch can render flush against the frame" becomes true again)
- [ ] run tests — must pass before task 6

### Task 6: bounded theme repaint of glamour's Chroma (№7)

**Files:**
- Modify: `internal/model/readme_style.go`
- Modify: `internal/model/readme_style_test.go`
- Modify: `CLAUDE.md`, `docs/design/readme-pipeline.md`

- [ ] write failing struct-level test (**dark variant only** — every CodeBlock override sits after the `if !dark` early return): `keepkitStyle(t, true).CodeBlock.Chroma` is a fresh pointer (plain pointer comparison against `styles.DarkStyleConfig.CodeBlock.Chroma`), its `Background` background and `Text` color/background carry `t.Surface`/`t.Emphasis`, every other token entry equals the stock one; the **light** config's `CodeBlock.Chroma` still **aliases** `styles.LightStyleConfig`'s (the light palette deliberately stays stock); `TestKeepkitStyleLeavesGlobalsUntouched` (readme_style_test.go:114) stays green
- [ ] clone `cfg.CodeBlock.Chroma` by value into a fresh pointer; repaint exactly `Chroma.Background` (background → `t.Surface`) and `Chroma.Text` (color → `t.Emphasis`, background → `t.Surface`); inherit the rest unchanged — no per-token judgement calls
- [ ] keep the `StyleBlock` `Color`/`BackgroundColor` overrides and rewrite the comment to name both render paths: Chroma when `ColorProfile != Ascii` (live sessions), StyleBlock for Ascii/`NO_COLOR` and this package's tests
- [ ] document the process-global one-shot `"charm"` registration (first render wins; struct-level assertion is the only reliable one; a mid-session theme switch cannot repaint fences — consistent with `m.darkBG` resolved once at construction)
- [ ] update `CLAUDE.md` (readme_style.go row) and `docs/design/readme-pipeline.md` ("CodeBlock on the same Surface background", "Chroma … deliberately untouched" — both need the two-path truth)
- [ ] run tests — must pass before task 7

### Task 7: sticky statuses must invalidate pending expiry timers (№8)

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/mode.go`
- Modify: `internal/model/status_test.go` (or launch_test.go, wherever the launch statuses are pinned)

- [ ] **investigate first** (owner's directive): write the reproducing test before any fix — shrink `statusMsgTTL`, drive `setStatus` (e.g. the group toggle), then the launch dispatch that assigns `launching <name> in <terminal>…` (use the `t.Setenv("TMUX", …)` pattern from launch_test.go:183 for a non-fallback plan), then deliver the *first* status's `statusExpiredMsg{seq}`; assert the in-flight message survives. The test failing on `main`'s code is the proof of whose timer kills it (expected: the transient's timer, because the direct assignment leaves `statusSeq` unbumped and the seq guard passes)
- [ ] if the investigation contradicts the hypothesis, ⚠️ stop and re-plan this task before writing a fix
- [ ] add `setStickyStatus(s)` (bump `statusSeq`, set message, no timer) next to `setStatus` with a doc comment stating why the bump is the point
- [ ] switch both in-flight sites (`still launching …`, `launching … in …`) to it; keep their "sticky, not setStatus" comments accurate
- [ ] run tests — `TestStatusExpired*`/`TestSetStatus*` and the new one must pass before task 8

### Task 8: reap browser opener processes (№9)

**Files:**
- Modify: `internal/model/browser.go`

- [ ] switch `exec.Command(...).Start()` to `Start()` + `go cmd.Wait()` on success, with the rationale in a comment: the goroutine reaps the child (no zombie per `o`/`c`/link click) without blocking on a shell-wrapper opener, and `openURLMsg.err` keeps meaning "could not launch" (a `Run()` would surface opener exit codes into a new `setStatus` path — a behavior change)
- [ ] **no new executing test — documented exception**: `browser_test.go` explicitly forbids invoking the command (it would launch a real browser); `TestBrowserCommand`/`TestUpdateOpenURLMsg` remain the coverage and must stay green
- [ ] run tests — must pass before task 9

### Task 9: formatStars trims only the `.0` tail (№14)

**Files:**
- Modify: `internal/model/textutil.go`
- Modify: `internal/model/textutil_test.go`

- [ ] write failing table test: 46000→`46k`, 46200→`46.2k` (owner: fractional values must survive), 1500→`1.5k`, 1000→`1k`, 999→`999`
- [ ] apply the `TrimSuffix(".0")` mirror of `formatShare`
- [ ] run tests — must pass before task 10

### Task 10: tags fallback for release-less repos — version side (№10a)

**Files:**
- Modify: `internal/version/github.go`
- Modify: `internal/version/github_test.go`
- Modify: `CLAUDE.md`

- [ ] write failing tests against `testAPIBase` fixtures, asserting on the resulting `CacheEntry` (the `updateCacheEntry`/`e := existing` shape is a review-time rule, not a test assertion): releases 404 + tags `[v1.2.0, v1.10.0]` → `Latest == "v1.10.0"` (semver max, not list order), `ReleaseMissing` true, `Body`/`HtmlUrl`/`PublishedAt` empty; releases 404 + empty tags → today's conclusive-but-blank outcome; tags fetch error (500/rate limit) → `conclusive` decision unchanged, entry identical to today's errNoReleases outcome (no veto of the `CheckedAt` stamp, no re-spend of the repo-info requests); **tuple-collision case**: pre-seeded entry with a full release tuple + 404 + tags present → the tag is *not* written, the preserved tuple stays intact (no hybrid of new tag over old notes)
- [ ] implement `fetchLatestTag` + the `errNoReleases`-gated call in `getRepoData`, placed **after** the total-failure early return (github.go:462-467); the tag write gated on an empty release tuple; shared `force` core covers `RefreshRepoData`
- [ ] add the provenance sentence to `applyReleaseOutcome`'s doc comment (`Latest` can now also be written beside it by the tags fallback, never by it) and update github.go:468-478's `conclusive` comment
- [ ] update `CLAUDE.md`: GitHub API request accounting (one extra request per tag-only repo per window, 404 path only), the poison-guard/"Force refresh" wording, the self-check invariant (a tag is not a release — `ReleaseMissing` semantics unchanged)
- [ ] run tests — `go test -race ./internal/version/` must pass before task 11

### Task 11: needsRemote settles on a conclusive answer — model side (№10b)

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/commands.go`
- Modify: `internal/model/render_test.go` (`TestNeedsRemote` lives at :2173-2214; the hand-built `Model{…}` literal at :2800-2808 needs the new map seeded or the handler write panics on nil)

- [ ] write failing tests: a tool whose `remoteMsg` arrived with `err == nil` and empty `Latest` (repo with neither releases nor tags) → `needsRemote` false (no re-dispatch per cursor visit); the trap case in **exactly** the pinned shape — `card.About != ""`, `latest == ""`, `repoStatus == "active"`, `err = ErrRateLimited` → still retryable (a *complete* stale card also carries `Latest` and masks the assertion for an unrelated reason); rename cleans the marker with the other per-name maps (untrack deliberately does not — it cleans no sibling map either)
- [ ] pin the card consequence of a tag-derived `Latest` (moved from Task 10 — this task's gate actually runs it): `↑` + `enter` offer + no release date + "no release notes available." + no clickable release heading (render.go:1656-1668 gates on `changelogURL != ""`)
- [ ] add session-scoped `m.remoteAnswered map[string]bool`, **initialized in `New()`**, written by the `remoteMsg` handler **only when `msg.err == nil`**; `needsRemote` consults it (nil-safe read); rename's stale-state cleanup deletes the old name; seed the hand-built literal at render_test.go:2800-2808
- [ ] note the honest cost in the comment: this saves a goroutine + `cache.json` read per cursor visit, not API quota (`errNoReleases` is already conclusive on the version side)
- [ ] update `CLAUDE.md`'s async-fetch-split wording for `needsRemote`
- [ ] run tests — must pass before task 12

### Task 12: doc drift sweep (№15)

**Files:**
- Modify: `internal/model/model.go` (comments only)
- Modify: `internal/model/render_test.go`
- Modify: `CLAUDE.md`, `ARCHITECTURE.md`, `README.md`

- [ ] fix the three «press L» comments (model.go:995, 1014, 1089) to name `[a]`, plus the same stale key in `README.md:311` (user-facing) and `render_test.go:4081`
- [ ] fix the CLAUDE.md "Known exceptions… `h`/`m`" sentence to the current `H`/`M`/`R` keys
- [ ] re-check `ARCHITECTURE.md` against tasks 2/10/11's changes (the enum listing and status-bar rationale were already updated in task 2 — verify nothing else drifted)
- [ ] run the `docs-sync` skill check over CLAUDE.md / docs/design/* / ARCHITECTURE.md against the branch's final state; fix what it flags
- [ ] run tests — full suite still green

### Task 13: verify acceptance criteria

- [ ] every taken finding (№1, 2, 4, 5, 6, 7, 8, 9, 10, 11, 14, 15) has a regression test that fails on `main` and passes here — except №9, whose documented exception is stated in Task 8; №3's removal leaves no dead references
- [ ] run the `preflight` skill: build / vet / `go test -race ./...` / golangci-lint — the exact CI matrix, all green
- [ ] grep for leftovers scoped to `internal/ ARCHITECTURE.md CLAUDE.md README.md docs/design/` (NOT `docs/plans/completed/`, which legitimately archives the old names): `modeHelpSearch|findMatches|highlightMatch|mdHTMLTagRe|mdFenceRe` return nothing

### Task 14: [Final] update documentation

- [ ] re-read the CLAUDE.md sections touched by tasks 2, 3, 5, 6, 10, 11, 12 as a whole — no contradictions left between them
- [ ] update README.md if the help-search removal is user-visible there (beyond the :311 line already fixed)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- TUI smoke test in a real terminal: re-track an annotated tool (fields survive), rename with an update pending (cursor follows), a README with code fences (Surface plate + stock token accents), a release-note body with `Vec<String>` and inline-code flags, a tag-only repo (latest + `↑`, no changelog), `o`/`c` links (no zombie accumulation in `ps`), `/` outside `[1]` does nothing.
- Demo GIFs: check whether `demo/hero.gif`/`demo/update.gif` show the help search or its hints; if yes, run the `demo-gifs` skill (it asks before recording).

**External:**
- PR to `main` per repo convention (English body, no AI footer — see memory `pr-description-style`); merge cleanup per memory `post-merge-cleanup`.
