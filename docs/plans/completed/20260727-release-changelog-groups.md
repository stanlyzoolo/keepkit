# Grouped release changelog via GoReleaser

## Overview

Release notes for every `v*` tag should assemble themselves from the commits
accumulated since the previous tag — grouped and filtered, not the current flat
commit list. The delivery channel is the GitHub release body: it is what users
read on GitHub **and** what keepkit itself renders (the changelog block in
panel `[2]`, the self-check's cached `Body`). No CHANGELOG.md in the tree —
deliberately out of scope.

The accumulation mechanism already exists: conventional commit subjects
(`feat:` / `fix:` / `docs:` / `chore:`). This plan only teaches GoReleaser to
group them and to append a static Install footer, plus one line of commit
discipline in CLAUDE.md.

## Context (from discovery)

- Release pipeline: push `v*` tag → `.github/workflows/release.yml` →
  GoReleaser (`.goreleaser.yaml`), which cuts the GitHub release and pushes the
  Homebrew formula.
- Current changelog config is minimal: `use: github, sort: asc` — a flat
  ungrouped commit list. v0.1.0's notes were written by hand.
- **`.goreleaser.yaml` already has a `release:` block** (lines 61–64:
  `github:` owner/name, sitting *after* `changelog:`). The footer goes into
  that block — adding a second `release:` key is a YAML unmarshal error
  (verified: `goreleaser check` fails with `mapping key "release" already
  defined`).
- Commit history is already conventional (`feat:`, `fix:`, `docs(demo):`,
  `chore:`); an occasional off-nomenclature subject exists (`rebrand:`), and
  history has a handful of `refactor:` commits — neither excluded nor grouped,
  so they will land under the catch-all *Other changes*.
- `goreleaser` 2.14.1 is installed locally (`/opt/homebrew/bin/goreleaser`).
  Note: `goreleaser check` on the **current** config already prints a
  `brews is being phased out in favor of homebrew_casks` deprecation warning
  and exits 0 — pre-existing, not this plan's breakage; migrating it is out
  of scope.
- Existing preview tooling: the `release-tools:last-tag` skill shows commits
  since the last tag — the raw material of the next changelog.
- ⚠️ **Stale when implemented, corrected here.** The plan was written against
  `stripMarkdown`, which no longer exists: main replaced it with
  `markdownToLines` (`internal/model/textutil.go`) in `e81bfb6`, *Keep the
  markdown structure in the card's changelog*. So a fenced block no longer
  degrades to a stray `bash` line — it renders properly on the card's
  `Surface` code plate. The footer still uses **inline code, no fenced
  block**, but now as a *preference* rather than a workaround: `mdInline`
  ends in `strings.ReplaceAll(s, "`", "")`, so inline backticks are dropped
  and the three install paths read as one prose sentence, whereas a fence
  would split that sentence across three plated blocks. Verified by probing
  `markdownToLines` with the real footer text — `## Install` → `mdHeading`
  (`"Install"`, prefix stripped), the command lines → `mdBody` with the
  backticks gone.

## Development Approach

- **testing approach**: N/A — config + docs only, no Go code is touched. The
  verification gates: `goreleaser check` (syntax; does **not** validate
  templates or render the changelog) for every YAML task, plus one real local
  changelog render for Task 1 (see Testing Strategy). Task 3 is docs-only and
  has no gate beyond a read-through.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: update this plan file when scope changes during implementation**
- `go test -race ./...` must stay green (it should be untouched by this plan;
  running it confirms nothing leaked into the Go build)

## Testing Strategy

- no unit tests: no Go code changes. If any task turns out to require touching
  Go code, stop and revisit the plan — that is a scope change.
- per-task gate: `goreleaser check` passes (expect the pre-existing `brews`
  deprecation warning; exit code 0 is the criterion).
- **real changelog render is possible locally** (despite `--snapshot`
  skipping it): a non-snapshot run with publish skipped writes the grouped
  output to `dist/CHANGELOG.md`. ⚠️ **The recipe below is the corrected one —
  the plan's original `use: github` version cannot work.** A local temp tag
  does not exist on the remote, so the compare API the `github` source calls
  answers `404 Not Found`:
  `GET /repos/stanlyzoolo/keepkit/compare/v0.1.0...v0.1.1-preview: 404`.
  Pushing the preview tag to satisfy it is not acceptable. The fix is to
  render from **local git** through a throwaway copy of the config, leaving
  the committed one untouched:

  ```bash
  sed 's/^  use: github$/  use: git/' .goreleaser.yaml > /tmp/preview.yaml
  git tag v0.1.1-preview
  goreleaser release --clean --config /tmp/preview.yaml \
    --skip=archive,publish,validate,sbom,sign,docker,announce,homebrew,before
  cat dist/CHANGELOG.md
  git tag -d v0.1.1-preview && rm -rf dist
  ```

  This still exercises the real filter/group code path (GoReleaser applies
  `filters`/`groups` identically for both sources — only entry origin and
  line format differ), and needs **no** `GITHUB_TOKEN`. `dist/` is gitignored.
  **Limit**: the footer is rendered only at publish time, so this validates
  grouping/filtering but not the footer template — see Task 2 for how that
  gap was closed instead.
- ⚠️ **Expected output changed with the rebase.** The plan predicted an empty
  `## Changelog` because at its original base every commit since `v0.1.0` was
  docs/chore. This branch was rebased onto `9ca6573`, which carries 31
  commits including real `feat(ui):`/`fix(model):`/`perf(model):` work, so the
  correct expectation is a *populated* three-group changelog — a stronger
  proof than the empty one, since it exercises all three groups at once.
- final proof: the first tag cut after real feat/fix work produces grouped
  notes with the Install footer.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

Two edits to `.goreleaser.yaml`, one line in CLAUDE.md:

1. **Changelog groups + filters** — keep `use: github` (each line carries a
   commit link and author) and `sort: asc` (note: sorts **alphabetically by
   message**, not chronologically — deliberate: scoped subjects like
   `feat(model):` cluster together within a group); exclude noise
   (docs/test/chore/ci/build, merge commits, `go mod tidy`); group the rest
   into *New features* (`feat`), *Bug fixes* (`fix`) and a catch-all *Other
   changes* so an off-nomenclature commit stays visible instead of silently
   vanishing.
2. **`release.footer`** — the static Install block (brew, `go install
   @{{ .Tag }}`, archives) that v0.1.0 carried by hand, now stamped onto every
   release automatically. Inline code, not a fenced block — see Context (the
   keepkit card consumer).
3. **Commit discipline note in CLAUDE.md** — `feat:`/`fix:` subjects land in
   release notes verbatim, so they are written for the user; the changelog
   filters in `.goreleaser.yaml` drop exactly `docs:`/`test:`/`chore:`/`ci:`/
   `build:` (plus merge commits); anything else — `refactor:`, `perf:`, an
   off-nomenclature subject — stays visible under *Other changes*.

Key regexp detail: with `use: github` the changelog line is prefixed with the
SHA, so group regexps must be of the `^.*?feat(\(.+\))??!?:.+$` shape (scope +
breaking marker; the same convention GoReleaser's own config uses), while
`filters.exclude` anchors on the raw message start (`^docs`) — GoReleaser
strips the SHA token before applying filters.

✅ **Both halves of that asymmetry are now confirmed on a real render**, not
just in review: the rendered lines carry a SHA prefix (`* e1ad93c… feat(model):
…`), which is what the leading `^.*?` is for, and the `^docs`-anchored
excludes nonetheless dropped every `docs:` commit in the range — so filters
genuinely see the bare subject while groups see the formatted line. The two
patterns are **not** interchangeable: anchoring a group at `^feat` matches
nothing, and prefixing an exclude with `^.*?` would make it match far more
than intended.

Deliberately out of scope (YAGNI, confirmed in brainstorm): `release.header`,
semver automation from commit types, draft releases, git-cliff / CHANGELOG.md,
an in-TUI "what's new" view, a `goreleaser check` step in ci.yml (flagged as
an option by review; not part of the approved scope).

## Technical Details

Target `changelog:` block in `.goreleaser.yaml` (replaces the current
two-line one):

```yaml
changelog:
  use: github
  sort: asc
  filters:
    exclude:
      - "^docs"
      - "^test"
      - "^chore"
      - "^ci"
      - "^build"
      - "Merge pull request"
      - "Merge branch"
      - "go mod tidy"
  groups:
    - title: New features
      regexp: '^.*?feat(\(.+\))??!?:.+$'
      order: 100
    - title: Bug fixes
      regexp: '^.*?fix(\(.+\))??!?:.+$'
      order: 200
    - title: Other changes
      order: 999
```

Target `release:` block — the **existing** block at the bottom of the file
grows a `footer:` key (do not create a second `release:` key):

```yaml
release:
  github:
    owner: stanlyzoolo
    name: keepkit
  footer: |
    ## Install

    `brew install stanlyzoolo/apps/keepkit`, or
    `go install github.com/stanlyzoolo/keepkit@{{ .Tag }}`, or grab an archive above.
```

Template surface stays `{{ .Tag }}` only (`.Tag` keeps the leading `v`, so
the `go install` line names the real released tag). **`goreleaser check` does
not validate this template** — a typo there surfaces as a failed release job
after the tag is pushed (recovery in Post-Completion).

## Implementation Steps

### Task 1: Group and filter the GoReleaser changelog

**Files:**
- Modify: `.goreleaser.yaml`

- [x] replace the two-line `changelog:` block with the grouped/filtered block
      from Technical Details (keep `use: github`, `sort: asc`)
- [x] keep the catch-all `Other changes` group **without** a `regexp` key —
      that is what makes it the catch-all
- [x] run the local changelog render from Testing Strategy (temp tag +
      `--skip=…publish…`, via the corrected `use: git` recipe — the original
      `use: github` one 404s on the compare API); confirm `dist/CHANGELOG.md`
      matches expectations for the tag range; delete the temp tag.
      **Result**: all three groups populated correctly — 7 `feat` under *New
      features*, 11 `fix` under *Bug fixes*, `perf(model):` alone under *Other
      changes*; every `docs:` commit and every merge commit filtered out.
      Temp tag and `dist/` removed afterwards
- [x] run `goreleaser check` — exit 0 (the pre-existing `brews` deprecation
      warning is expected) — must pass before task 2

### Task 2: Add the static Install footer to releases

**Files:**
- Modify: `.goreleaser.yaml`

- [x] add `footer:` to the **existing** `release:` block as shown in Technical
      Details — do not introduce a second `release:` key. Verified: the parsed
      YAML has exactly one `release:` key, holding `['github', 'footer']`
- [x] use inline code for the install commands, no fenced ` ```bash ` block —
      note the *reason* changed (see the corrected Context bullet); the
      decision stands, now because a fence would split one sentence across
      three plated blocks on the card
- [x] verify `{{ .Tag }}` is used (not a hardcoded version) and is the
      **only** template expression in the footer — `goreleaser check` cannot
      catch a template typo, it would fail the release job at tag time
- [x] ➕ **closed that gap instead of just noting it**: rather than leave the
      one un-checkable failure mode to the next real tag, the footer was
      extracted from the parsed YAML and run through Go's `text/template`
      with `Option("missingkey=error")` and a `struct{ Tag string }`. Parses
      and executes clean, rendering
      `go install github.com/stanlyzoolo/keepkit@v0.1.1` — so `.Tag` keeps
      its leading `v` as designed. `missingkey=error` is what makes a
      mistyped field (`{{ .tag }}`) a hard failure rather than the silent
      `<no value>` GoReleaser would otherwise publish
- [x] run `goreleaser check` — exit 0 — must pass before task 3

### Task 3: Commit-discipline note in CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [x] add 1–2 sentences to the Commands/CI paragraph (next to the existing
      release description; name `.goreleaser.yaml` explicitly, since the
      paragraph currently doesn't): `feat:`/`fix:` commit subjects become
      release notes verbatim (grouped by `.goreleaser.yaml`'s changelog
      config) — write them user-facing; `docs:`/`test:`/`chore:`/`ci:`/
      `build:` are dropped by the filters, and anything else (`refactor:`,
      `perf:`, off-nomenclature) lands under *Other changes*
- [x] keep it tight — this is discipline guidance, not a spec. Landed as three
      sentences in the existing CI/release paragraph, also naming panel `[2]`
      as the second consumer of the body and recording the group-vs-exclude
      regexp asymmetry, since that is the part a future editor would get
      wrong

### Task 4: Verify acceptance criteria

- [x] `goreleaser check` passes on the final config (exit 0; only the known
      `brews` deprecation warning)
- [x] re-read `.goreleaser.yaml` top to bottom: changelog block matches the
      approved design exactly; single `release:` block; footer renders the
      three install paths inline
- [x] `go build .` and `go test -race ./...` still green (nothing in the Go
      tree should have changed). Ran the **full** `preflight` matrix rather
      than just those two — `go build` ✅, `go vet ./...` ✅, `go test -race
      ./...` ✅ (all 10 packages), `golangci-lint run` ✅ 0 issues
      (v2.12.2). ⚠️ Both must be run **inside the worktree**: cwd resets to
      the primary checkout between shell calls, and a check that silently runs
      against `main` reports a false green
- [x] confirm nothing else in the release pipeline references changelog
      behavior (grep `.github/workflows/` for changelog assumptions;
      `release.yml`'s `fetch-depth: 0` stays — GoReleaser still needs full
      history for the tag range). **Result**: the only hit is
      `release.yml:18-19`, the `fetch-depth: 0` line and its comment already
      naming the changelog. No workflow change needed

### Task 5: [Final] Update documentation

- [x] check README.md — no changes expected (verified in review: its
      "changelog" mentions are the TUI's `[c]` key and card block only);
      update only if something slipped in. **Confirmed**: the only two hits
      (lines 58, 134) are both the `[c]`-key/card-refresh feature text, and
      the word `goreleaser` does not appear in README.md at all
- [x] CLAUDE.md already updated in task 3 — verify the note reads naturally
      in context
- [x] move this plan to `docs/plans/completed/`

### ➕ Task 6: Rebase the worktree onto current `main`

*Discovered at the start of implementation — not in the original plan.*

- [x] ⚠️ The worktree branch was based on `f8ad9d2`, while `main` had advanced
      to `9ca6573` (31 commits, including the whole theming/redesign series).
      Task 3 edits `CLAUDE.md`, which `main` had rewritten by 89 lines — so
      editing the stale copy would have produced a conflict-prone merge in
      exactly the paragraph being touched. Rebased the single docs commit onto
      `main` **before** any edit; it applied cleanly
- [x] confirmed `.goreleaser.yaml` itself was **untouched** by all 31 commits,
      so Tasks 1–2 were unaffected by the rebase and the plan's line
      references for the `release:` block stayed valid
- [x] ⚠️ side effect worth recording: this is also what changed Task 1's
      expected render output from empty to populated (see Testing Strategy)

## Post-Completion

*No checkboxes — external actions.*

**Manual verification:**
- The real proof is the next tag. ⚠️ **This no longer carries the plan's
  original caveat**: it warned the section would be empty because this plan's
  own commits are all docs/chore. After the rebase onto `9ca6573` the
  `v0.1.0..HEAD` range holds 31 commits with 7 `feat` and 11 `fix` among them,
  so the next tag produces a fully populated three-group changelog — already
  rendered and inspected locally (see Task 1). Nothing needs to be waited for
  or accepted as empty. Before tagging, preview the raw material with
  `release-tools:last-tag`.
- The one thing still unproven end-to-end is the footer *at publish time*
  (GoReleaser only renders it when actually cutting the release). Its template
  was validated independently — see Task 2 — so the residual risk is
  GoReleaser's own footer plumbing, not the template.
- **Recovery if the release job fails on a footer template error** (the one
  class of typo `goreleaser check` cannot catch): fix the config on main,
  then delete and re-push the tag (`git tag -d vX.Y.Z && git push --delete
  origin vX.Y.Z`, re-tag, push). The Homebrew formula push rides the same
  job, so it self-heals on the re-run.
- If the notes need a hand-written Highlights block for a big release,
  `gh release edit` on top of the generated body remains the workflow.

**External systems:**
- None — the Homebrew tap pipeline is untouched.
