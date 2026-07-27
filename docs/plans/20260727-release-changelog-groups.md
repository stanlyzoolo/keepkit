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
- keepkit's own card strips markdown when rendering a release body
  (`stripMarkdown`, `internal/model/textutil.go`): `#` and backticks are
  dropped, a fenced ` ```bash ` line degrades to a bare `bash` line. The
  footer therefore uses **inline code, no fenced block** (GitHub renders
  inline code fine; the card stays clean).

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
  output to `dist/CHANGELOG.md`:

  ```bash
  git tag v0.1.1-preview
  GITHUB_TOKEN=$(gh auth token) goreleaser release --clean \
    --skip=archive,publish,validate,sbom,sign,docker,announce,homebrew,before
  cat dist/CHANGELOG.md
  git tag -d v0.1.1-preview
  ```

  `GITHUB_TOKEN` is required because `use: github` pulls entries from the
  compare API; `dist/` is gitignored. **Limits**: the footer is rendered only
  at publish time — this preview validates grouping/filtering, not the footer
  template; and on this branch the range `v0.1.0..v0.1.1-preview` holds only
  docs/chore commits, so an *empty* `## Changelog` body is the **correct**
  expected output here (it proves the filters drop them).
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
strips the SHA token before applying filters (empirically confirmed in
review).

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

- [ ] replace the two-line `changelog:` block with the grouped/filtered block
      from Technical Details (keep `use: github`, `sort: asc`)
- [ ] keep the catch-all `Other changes` group **without** a `regexp` key —
      that is what makes it the catch-all
- [ ] run the local changelog render from Testing Strategy (temp tag +
      `--skip=…publish…`, needs `GITHUB_TOKEN`); confirm `dist/CHANGELOG.md`
      matches expectations for the tag range (on this branch: an empty
      `## Changelog` — every commit since v0.1.0 is docs/chore and must be
      filtered out); delete the temp tag
- [ ] run `goreleaser check` — exit 0 (the pre-existing `brews` deprecation
      warning is expected) — must pass before task 2

### Task 2: Add the static Install footer to releases

**Files:**
- Modify: `.goreleaser.yaml`

- [ ] add `footer:` to the **existing** `release:` block (lines 61–64) as
      shown in Technical Details — do not introduce a second `release:` key
- [ ] use inline code for the install commands, no fenced ` ```bash ` block
      (keepkit's card strips fences into stray `bash` lines)
- [ ] verify `{{ .Tag }}` is used (not a hardcoded version) and is the
      **only** template expression in the footer — `goreleaser check` cannot
      catch a template typo, it would fail the release job at tag time
- [ ] run `goreleaser check` — exit 0 — must pass before task 3

### Task 3: Commit-discipline note in CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] add 1–2 sentences to the Commands/CI paragraph (next to the existing
      release description; name `.goreleaser.yaml` explicitly, since the
      paragraph currently doesn't): `feat:`/`fix:` commit subjects become
      release notes verbatim (grouped by `.goreleaser.yaml`'s changelog
      config) — write them user-facing; `docs:`/`test:`/`chore:`/`ci:`/
      `build:` are dropped by the filters, and anything else (`refactor:`,
      `perf:`, off-nomenclature) lands under *Other changes*
- [ ] keep it tight — this is discipline guidance, not a spec

### Task 4: Verify acceptance criteria

- [ ] `goreleaser check` passes on the final config (exit 0; only the known
      `brews` deprecation warning)
- [ ] re-read `.goreleaser.yaml` top to bottom: changelog block matches the
      approved design exactly; single `release:` block; footer renders the
      three install paths inline
- [ ] `go build .` and `go test -race ./...` still green (nothing in the Go
      tree should have changed)
- [ ] confirm nothing else in the release pipeline references changelog
      behavior (grep `.github/workflows/` for changelog assumptions;
      `release.yml`'s `fetch-depth: 0` stays — GoReleaser still needs full
      history for the tag range)

### Task 5: [Final] Update documentation

- [ ] check README.md — no changes expected (verified in review: its
      "changelog" mentions are the TUI's `[c]` key and card block only);
      update only if something slipped in
- [ ] CLAUDE.md already updated in task 3 — verify the note reads naturally
      in context
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*No checkboxes — external actions.*

**Manual verification:**
- The real proof is the next tag — but note: if it is cut right after this
  merge, the changelog section will be **empty** (this plan's own commits are
  all docs/chore, correctly filtered; the body would be a bare `## Changelog`
  plus the footer). Cut the first tag after real feat/fix work, or accept an
  empty section. Before tagging, preview the raw material with
  `release-tools:last-tag`.
- **Recovery if the release job fails on a footer template error** (the one
  class of typo `goreleaser check` cannot catch): fix the config on main,
  then delete and re-push the tag (`git tag -d vX.Y.Z && git push --delete
  origin vX.Y.Z`, re-tag, push). The Homebrew formula push rides the same
  job, so it self-heals on the re-run.
- If the notes need a hand-written Highlights block for a big release,
  `gh release edit` on top of the generated body remains the workflow.

**External systems:**
- None — the Homebrew tap pipeline is untouched.
