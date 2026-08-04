# Make GitHub API failures visible and survivable

## Overview

A GitHub token that goes bad after it was stored turns keepkit into a silent
liar. The reported symptom: `claude` showed `installed v2.1.221` against
`latest v2.1.220` on the card, and a manual `[r]` refresh changed nothing.

The cause is not release/tag resolution. `~/.config/keepkit/token` had expired,
so every request answered `401 Bad credentials` — the same URLs answer `200`
with no `Authorization` header at all. An expired token is therefore **strictly
worse than no token**: unauthenticated the app would have worked at 60 req/h.
One session log holds 33 × `/repos/*/releases/latest http=401` and 32 ×
`/repos/* http=401` — every request of the session, all 34 tracked tools — and
none of it reached the screen.

The failure is swallowed one layer at a time:

1. `classifyStatus` (`internal/version/github.go:169`) — 401 is neither 403 nor
   429, so it is not `ErrRateLimited`; it becomes an anonymous
   `fmt.Errorf("GitHub API: HTTP %d")`.
2. `getRepoData` — with both core fetches failing, the total-failure early
   return serves the stale entry and leaves `CheckedAt` untouched. That is why
   `cache.json` still held the previous day's timestamp: the forced refresh
   physically wrote nothing.
3. **`d.Err = rlErr`** (`internal/version/github.go:547`) — the core defect. Only
   rate-limiting is propagated; every other error is dropped on the floor, so
   the model never learns a 401 happened.
4. `remoteCmd` (`internal/model/commands.go:195`) — with no error, the
   `repoStatus = "rate-limited"` branch cannot fire and the status stays
   whatever the cached entry said (`active`).
5. The `remoteMsg` handler (`internal/model/model.go:1018`) — `hasData` is true
   (cached `latest`), so the stale card renders as perfectly healthy.

Four consequences, in the order they hurt:

1. **A dead token is indistinguishable from "no updates".** Nothing on screen
   says the data is not being refreshed, and the only trace is a log file the
   user has to know to open.
2. **`[r]` answers nothing, ever.** `refreshingFor` starts the card-title
   spinner and `remoteMsg` clears it. Success, 401, 403, a timeout and a dropped
   connection all look identical: the spinner turns, the card does not change.
3. **The one error class we do handle is nearly unreachable.** The
   `rate limited — press [a]` hint is gated on
   `errors.Is(d.Err, ErrRateLimited) && d.Latest == "" && d.About == ""`, so it
   only ever reaches a tool with no cache at all — a freshly tracked one. For
   every tool that has been fetched once, a rate-limited pass is as invisible as
   a 401.
4. **Nothing ever re-checks the token.** It is validated exactly once, at entry
   time, by `FetchRateWithToken`. That it later expired is unobservable.

This plan makes every failure class carry a name, degrades a rejected token to
unauthenticated requests instead of a blackout, and gives each of the three
states a surface: a persistent indicator, an explanation, and an answer to
`[r]`.

**What "degraded" honestly buys, and what it does not.** With a warm cache the
degraded session is a working session: the retried requests succeed, cards
update, and the only difference is the gauge. On a **cold** cache it is not —
34 tools × 3 requests is 102 against an anonymous 60/h, and `Init` fires them in
one batch. There the outcome is a partial fill plus a visible rate-limited
surface. That is still strictly better than today's blackout, but it is a
different claim and the manual-verification steps below are written for it.

## Context (from discovery)

- files/components involved:
  - `internal/version/github.go` — `classifyStatus`, `doGH`, `getRepoData`,
    `RepoData`, `FetchRate`, `FetchRateWithToken`
  - `internal/version/token.go` — `resolveToken`, `Token`, `TokenSource`,
    `SetToken`, `ClearToken`
  - `internal/model/model.go` — `remoteMsg` / `rateMsg` structs, the `remoteMsg`
    and `tokenValidatedMsg` handlers, `Init`
  - `internal/model/commands.go` — `remoteCmd`, `fetchRateCmd`, `needsRemote`
  - `internal/model/render.go` — `renderRateGauge`, `renderAPIStatus`,
    `renderHintsBar`
- test files that actually hold the affected coverage (there is **no**
  `internal/model/model_test.go` — the handler tests live elsewhere):
  - `internal/model/render_test.go` — `remoteMsg`/`installedMsg` handler tests
    (`TestUpdateInstalledAndRemoteMsgPopulateCaches`, :2460), the rate/gauge
    tests, the `[a]` overlay tests
  - `internal/model/status_test.go` — `assertOnlyExpiryTick` (:27) and the
    `statusMsgTTL` helpers
  - `internal/model/commands_test.go` — fetch predicates and command batches
  - `internal/version/github_test.go`, `internal/version/token_test.go`
- related patterns found:
  - `doGH` is already documented as the single auth + rate-accounting point, so
    a retry there needs no second HTTP path
  - `RepoData.Conclusive` already exists and is already honest: it means "this
    pass settled the tool", which is exactly the predicate `[r]` needs
  - `remoteMsg` already carries a `version.RateLimit` snapshot taken inside the
    command goroutine — the precedent for carrying `tokenRejected` the same way
  - `shouldReplaceRate` (`github.go:96-110`) already admits an observation whose
    `Limit` changed, so the 5000 → 60 transition needs no new support
- dependencies identified: none new; no new package, no new import

## Development Approach

- **testing approach**: Regular (code first, then tests within the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in
  that task
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - **update existing test cases whose stated premise the change falsifies** —
    tasks 2 and 3 each carry one, named explicitly in their checklists
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `go test -race ./...` after each change
- maintain backward compatibility

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above)
- **e2e tests**: none — this project has no UI-based e2e suite. The TUI is
  covered by rendering tests over constructed models and messages
- `internal/version` has `testAPIBase`, so its tests drive a real `httptest`
  server and can assert on the headers a request carried
- `internal/model` cannot reach the network (`testAPIBase` is unexported there),
  so every model test constructs messages by hand and feeds them to `Update`
- **process-global state needs a reset seam.** Go runs a package's tests in one
  process, sequentially, so a test that leaves `rejectedToken` set would strip
  `Authorization` from every later test in the same binary — deterministic
  contamination, not a flake. `resetTokenState`
  (`internal/version/token_test.go:12-27`) is the existing seam and must learn
  the new field, in both its setup and its `t.Cleanup`
- test isolation is per test binary via the `TestMain` seams
  (`logx.SetDirForTesting`, `loader.SetConfigDirForTesting`,
  `version.SetConfigDirForTesting`) — already in place, nothing new needed. **No
  test may reach a real config path.**

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

Three layers, in dependency order.

**Taxonomy.** A closed set of failure reasons: `ErrRateLimited` (403/429 with
`X-RateLimit-Remaining: 0`), `errNoReleases` (a conclusive negative that is never
surfaced as an error), `ErrTokenInvalid` (401), and everything else = transient.
"Transient" is deliberately the *absence* of a name rather than a name: 5xx,
timeout, DNS and a broken transport all get the same treatment (retry later) and
the UI has nothing to tell them apart with. No new sentinels are introduced —
`ErrTokenInvalid` already exists and is currently produced only by
`FetchRateWithToken`.

> **Why the priority pick is two-tier, not three.** Once the retry lands (below),
> a 401 on a request that carried a token never reaches the caller as an error:
> `doGH` consumes it and returns the anonymous retry's response. GitHub answers
> 401 only for bad credentials, so an anonymous request cannot produce one
> either. `ErrTokenInvalid` therefore survives as a **classification**, not as a
> hot path — it names the code in a log line and in the defensive case of a
> proxy or enterprise host that 401s an anonymous request. The `getRepoData`
> pick is `ErrRateLimited > transient`, and `[r]` has no token wording. Wiring a
> tier that nothing can reach would read as live logic to the next person.

**Degradation.** On a 401 to a request that carried `Authorization`, `doGH`
marks the token rejected and retries the same request once without the header.
A blackout becomes a working 60 req/h session. The rejection is stored as a
*value* (`rejectedToken string`), not a bool, so re-entering a token, clearing
it, or an env token that cannot be unset are all handled by one comparison
rather than by a lifecycle that has to be maintained in four places.

**Visibility.** Three surfaces, each for a different lifetime of fact:

| fact | lifetime | surface |
|---|---|---|
| the token was rejected, we are anonymous | the session | the status-bar gauge: `api✕` |
| why, and how to fix it | on demand | the `[a]` overlay |
| this refresh settled nothing | one press | a `statusMsg` from `[r]` |

The gauge is the *primary* place the state is announced, not a guaranteed one:
it renders nothing while `!gaugeVisible(m.rate)` and it is droppable under width
pressure. The `[a]` overlay is what always has the answer, and the `[?]` overlay
already documents the `a` key.

Key design decisions and rationale:

- **The retry lives in `doGH` and nowhere else.** It is already the single auth
  point, so there is exactly one place that can produce a 401 for a token we
  chose to send. A startup pre-check was rejected: `Init` fires the rate seed and
  34 repo passes in one batch, so a check cannot land before the passes it would
  warn.
- **`FetchRateWithToken` must keep bypassing `doGH`** (`ghClient.Do` directly,
  `github.go:262`). If it rode the shared path, the retry would answer an
  anonymous `200` and we would persist a known-dead token. Today that separation
  reads as an incidental detail; this plan turns it into a pinned invariant.
- **Suppression is a wrapper, not a rewrite of the token accessors.** Only the
  request path may see a rejected token as absent. The overlay still has to
  print its source and its mask, which is exactly what the rejected state is
  about — so a raw core stays underneath (see Technical Details).
- **The token file is never deleted.** A 401 means the credential was rejected,
  not that we may destroy the user's data. It stays on disk and is merely unused
  for the session.
- **The rejected flag rides on messages, not read from `View`.** A package
  global cannot be set in a model test without the network. `remoteMsg`/`rateMsg`
  carry the snapshot taken inside the command goroutine — the same shape `rate`
  already uses — and the model keeps `m.tokenRejected`.
- **`✕` on the gauge, not a relabel to `anon`.** A user with no token is also
  anonymous, and that is not degradation. The suffix costs one column, sheds
  with the gauge, and does not collide with the `⚠`/`✕` usage-threshold icons,
  which live only in the `[a]` overlay.

Explicitly **out of scope** (taken to personal notes as a separate option):
a full `apiHealth` state machine, data age on the card, and an "installed newer
than latest" anomaly marker.

## Technical Details

**`classifyStatus`** gains one branch before the generic return:
`resp.StatusCode == http.StatusUnauthorized` → `ErrTokenInvalid`. Logging stays
as it is (401 is not a 404 and is worth a line).

**`getRepoData` error pick.** `var rlErr error` and the terminal
`d.Err = rlErr` (`github.go:547`, plus the early-return site at `:480`) are
replaced by a helper that picks the more actionable of `relErr` / `infoErr`:
`ErrRateLimited` (fixed by waiting) over transient (fixed by nothing).
`errNoReleases` never participates: it is a conclusive negative, not a failure.
`RepoData.Conclusive` is untouched.

**Token accessors** (`token.go`, all under the existing `tokenMu`). The
suppression must not reach the accessors the overlay reads, or the mask would
vanish in exactly the state the overlay exists to describe (`Token()` is a
one-line delegate today, `token.go:102-104`, and `render.go:577` renders its
mask). So the file splits into a raw core and one suppressing wrapper:

| function | reads | used by |
|---|---|---|
| `effectiveToken()` | env, else `tokenMem` | the three accessors below |
| `Token()` | `effectiveToken()` | the overlay's masked preview |
| `TokenSource()` | env / `tokenMem` (unchanged) | the overlay |
| `TokenRejected()` | `rejectedToken != "" && effectiveToken() == rejectedToken` | the commands |
| `resolveToken()` | `effectiveToken()`, `""` when rejected | **`doGH` only** |

`rejectToken(tok string)` records the refused value and emits one `logx` line on
the transition only — 34 goroutines must not write 34 lines. No clearing code is
needed anywhere: a newly entered token differs from the rejected value and works
immediately; a cleared token is empty anyway; a bad `GITHUB_TOKEN` cannot be
unset from the environment but is suppressed by the same comparison. The
`rejectedToken != ""` guard in `TokenRejected` is load-bearing: without it a user
with **no** token compares equal to the empty rejected value and gets the whole
degraded UI for a state that is not degraded at all.

**`doGH` retry.** After `ghClient.Do`, when the request carried `Authorization`
and the response is 401: drain and close the body, `rejectToken(tok)`,
`req.Clone(req.Context())` minus the `Authorization` header, one retry. All
requests are bodiless GETs, so cloning is safe. The retry's response goes
through the same `updateRateFromHeaders`, so the gauge moves honestly from 5000
to 60. Cost: with 34 parallel goroutines a few pay their own retry before the
mark lands — bounded by the tool count, and those requests would have failed
anyway. Every `doGH` consumer inherits this: `fetchRelease` (`:825`, shared by
`getChangelog` and `SelfLatest`), `fetchRepoInfo` (`:715`), `fetchLanguages`
(`:747`), `fetchLatestTag` (`:781`), `fetchReadme` (`:676`) and `FetchRate`
(`:233`).

**Model state and messages.** `remoteMsg` and `rateMsg` gain
`tokenRejected bool`, snapshotted in the command goroutine after the fetch. The
model keeps `m.tokenRejected`; `renderRateGauge` and `renderAPIStatus` read the
field. The `tokenValidatedMsg` success path clears it explicitly: that message
is the one proof a new token works, it returns the user straight to
`modeAPIStatus` (`model.go:1097-1099`), and without the clear the overlay would
keep reading `rejected (HTTP 401)` against the token that just validated.

**Recovery.** `tokenValidatedMsg` currently backfills only the selected tool via
`autoFetchCmdsForSelected()`. On an accepted token it will additionally clear
`m.remoteAnswered` and fan out `fetchRemoteCmd` for **every tool with
`t.GitHub != ""`** — `Init`'s predicate (`model.go:882-887`), deliberately *not*
`needsRemote`. `needsRemote` (`commands.go:342-354`) returns false as soon as a
card exists with a non-empty `Latest`, which is true for precisely the tools that
rendered stale-but-present data during the degraded window — the ones this
recovery exists for. The cost is the documented one: a goroutine plus a
`cache.json` read per tool, not API quota.

**Processing flow after the change**, with a dead token and a warm cache:

```
doGH → 401 → rejectToken → retry anonymous → 200
     → data fetched, cache written, card updated
     → remoteMsg{tokenRejected: true, conclusive: true}
     → gauge renders api✕, [a] explains, [r] stays silent (it worked)
```

and with an exhausted anonymous quota (the cold-cache case):

```
doGH → 403 remaining=0 → ErrRateLimited → getRepoData total-failure return
     → remoteMsg{err: ErrRateLimited, conclusive: false, hasData: true}
     → stale card still renders, [r] answers "refresh failed: rate limited — press [a]"
```

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code changes, tests and
  documentation updates inside this repository
- **Post-Completion** (no checkboxes): manual verification against the live
  GitHub API, which no automated test can perform

## Implementation Steps

### Task 1: Classify 401 as ErrTokenInvalid

**Files:**
- Modify: `internal/version/github.go`
- Modify: `internal/version/github_test.go`

- [ ] add a `http.StatusUnauthorized` branch to `classifyStatus` returning
      `ErrTokenInvalid`, placed before the generic `HTTP %d` return
- [ ] extend the doc comment on `ErrTokenInvalid`: it is no longer produced only
      by `FetchRateWithToken`, and after task 5 it is a defensive classification
      rather than a hot path
- [ ] write a test: a 401 response classifies as `ErrTokenInvalid`
- [ ] write tests for the neighbours that must not change: 403 with
      `remaining=0` → `ErrRateLimited`, 403 with `remaining>0` → generic, 404 →
      generic and unlogged
- [ ] run `go test -race ./internal/version/` — must pass before task 2

### Task 2: Render cached data on any error, not only on a nil one

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/render_test.go`

- [ ] rewrite the first `remoteMsg` case as `case hasData || msg.err == nil:`
- [ ] update the branch comment to say the predicate is about data, not about
      the error class
- [ ] **amend the existing assertion this falsifies**: the "remoteMsg with err
      set must not touch the caches" block in
      `TestUpdateInstalledAndRemoteMsgPopulateCaches`
      (`internal/model/render_test.go:2503-2512`) feeds `latest: "2.0"`, which
      makes `hasData` true. Split it: an error with **no** data still populates
      nothing; an error **with** data now populates
- [ ] write a test: `remoteMsg` carrying a non-rate-limit error plus a populated
      card merges `versions`, `repoCards` and `repoStatus`
- [ ] write a test: `remoteMsg` with no data and `repoStatus == "rate-limited"`
      still reaches the second case
- [ ] run `go test -race ./internal/model/` — must pass before task 3

### Task 3: Propagate the classified error out of getRepoData

**Files:**
- Modify: `internal/version/github.go`
- Modify: `internal/version/github_test.go`

- [ ] add a helper that picks the more actionable of two errors —
      `ErrRateLimited` over transient — ignoring `errNoReleases`
- [ ] replace `var rlErr error` and both `d.Err = rlErr` sites (`github.go:480`
      and `:547`) with the helper
- [ ] update the `RepoData.Err` doc comment: it no longer carries only
      `ErrRateLimited`
- [ ] **amend the existing assertion this falsifies**:
      `TestRepoDataConclusive/total failure is not conclusive`
      (`internal/version/github_test.go:1281-1284`) fatals on a non-nil `Err`
      with the premise *"a 5xx reaches the caller as a nil error"*. Invert it and
      rewrite the function's rationale comment (`:1252-1257`), which states the
      same now-false claim — the test's real subject (`Conclusive`, not `Err`, is
      the retry marker) survives
- [ ] write tests for the pick order, driving an `httptest` server that answers
      403+remaining=0 / 500 per endpoint
- [ ] write a test: a repo with no releases still reports `Err == nil` and
      `Conclusive == true`
- [ ] run `go test -race ./...` — must pass before task 4

### Task 4: Split the token accessors and record a rejected value

**Files:**
- Modify: `internal/version/token.go`
- Modify: `internal/version/token_test.go`

- [ ] extract `effectiveToken()` (env, else `tokenMem`) as the raw core and
      point `Token()` and `TokenSource()` at it — they must keep answering for a
      rejected token, or the overlay loses the mask it is meant to show
- [ ] add `rejectedToken string` under `tokenMu` and `rejectToken(tok string)`
      writing it, with exactly one `logx` line on the transition
- [ ] make `resolveToken()` the suppressing wrapper — `""` while the effective
      token equals `rejectedToken` — and document that `doGH` is its only caller
- [ ] add `TokenRejected() bool` as
      `rejectedToken != "" && effectiveToken() == rejectedToken`
- [ ] extend `resetTokenState` (`token_test.go:12-27`) to clear `rejectedToken`
      in **both** its setup and its `t.Cleanup`
- [ ] write tests: rejecting suppresses `resolveToken` but not `Token`,
      `TokenSource` or the mask; a different token via `SetToken` resolves again;
      `ClearToken` leaves nothing resolvable
- [ ] write a test: with **no** token configured, `TokenRejected()` is false —
      the empty-equals-empty degenerate case
- [ ] write a test: `rejectToken` called twice for the same value logs once
- [ ] run `go test -race ./internal/version/` — must pass before task 5

### Task 5: Retry once without the token on a 401 in doGH

**Files:**
- Modify: `internal/version/github.go`
- Modify: `internal/version/github_test.go`

- [ ] in `doGH`, remember whether an `Authorization` header was set; on a 401
      response drain and close the body, call `rejectToken`, clone the request
      without the header and retry exactly once
- [ ] account the retry's response through `updateRateFromHeaders` like any
      other response
- [ ] document on `FetchRateWithToken` that bypassing `doGH` is load-bearing,
      not incidental — the retry would otherwise answer an anonymous 200 and a
      dead token would be persisted
- [ ] route every new 401 test through `resetTokenState` so the rejection cannot
      leak into later tests in the same binary
- [ ] write a test: server answers 401 with `Authorization` and 200 without →
      two requests, the second header-less, the call succeeds,
      `TokenRejected()` is true
- [ ] write a test: after the rejection, later requests go out header-less on
      the first attempt with no second retry
- [ ] write a test: `FetchRateWithToken` against an always-401 server returns
      `ErrTokenInvalid` and never an anonymous 200
- [ ] write a test: a 401 for a request that carried no token is classified,
      not retried
- [ ] run `go test -race ./...` — must pass before task 6

### Task 6: Carry the rejected state to the model on messages

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/commands.go`
- Modify: `internal/model/render_test.go`

- [ ] add `tokenRejected bool` to `remoteMsg` and `rateMsg`, documenting that it
      is snapshotted in the command goroutine like `rate`
- [ ] set it in `remoteCmd` and `fetchRateCmd` from `version.TokenRejected()`
      after the fetch returns
- [ ] add `m.tokenRejected` to `Model` and write it from both handlers
- [ ] clear `m.tokenRejected` in the `tokenValidatedMsg` success path, before it
      returns to `modeAPIStatus`
- [ ] write tests: a `remoteMsg` and a `rateMsg` with `tokenRejected: true` each
      set the field
- [ ] write a test: an accepted `tokenValidatedMsg` clears it, and a failed one
      leaves it alone
- [ ] run `go test -race ./internal/model/` — must pass before task 7

### Task 7: Mark the rejected token on the status-bar gauge

**Files:**
- Modify: `internal/model/render.go`
- Modify: `internal/model/render_test.go`

- [ ] render the gauge label as `api✕ ` with the `✕` in `Danger` when
      `m.tokenRejected`, in both the full and the `compact` form
- [ ] extend the `renderRateGauge` doc comment with the new state and with why
      the suffix is not a relabel to `anon`
- [ ] write a test: the gauge carries `✕` when rejected and does not when not
- [ ] write a test: `TestStatusBarNeverWraps` stays green at the 80×24 baseline
      with the rejected state on
- [ ] write a test: the compact form keeps the marker (it is the form a narrow
      bar keeps) — note the shed order is *version cell first, gauge after*
      (`render.go:284-303`), so no test may claim the opposite
- [ ] run `go test -race ./internal/model/` — must pass before task 8

### Task 8: Explain the rejection in the [a] overlay

**Files:**
- Modify: `internal/model/render.go`
- Modify: `internal/model/render_test.go`

- [ ] render the token line as `token  <source> (<masked>) — rejected (HTTP
      401)` in `Danger` when `m.tokenRejected`, with
      `requests run unauthenticated` under it
- [ ] widen the `add a github token…` nudge condition from
      `source == "none"` to also cover the rejected case, with wording about
      replacing it
- [ ] write a test: the rejected token line renders **with its mask intact**
      (the task 4 split is what makes this possible) plus the unauthenticated
      note
- [ ] write a test: the nudge appears for a rejected token and stays hidden
      while `modeTokenInput` is open
- [ ] write a test: a healthy token renders neither
- [ ] write a size-budget test for the overlay at 80×24 — this task adds its
      longest line so far and nothing pins its width today
- [ ] run `go test -race ./internal/model/` — must pass before task 9

### Task 9: Make [r] answer every press

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/status_test.go`

- [ ] in the `remoteMsg` handler, where `refreshingFor` is cleared, return
      `setStatus` with a reason when `!msg.conclusive`
- [ ] map the reason from `msg.err`: `ErrRateLimited` →
      `refresh failed: rate limited — press [a]`, anything else →
      `refresh failed: network error` (no token wording — see the two-tier note
      in Solution Overview)
- [ ] keep success silent — the card repaint is the answer
- [ ] write a table test over the reasons asserting the status text, shrinking
      `statusMsgTTL` first as the sibling helpers require
      (`status_test.go:44-45`) so the tick does not add a real second per case
- [ ] write a test using `assertOnlyExpiryTick` (`status_test.go:27`) that no
      fetch command rode along
- [ ] write a test: a conclusive pass sets no status
- [ ] run `go test -race ./internal/model/` — must pass before task 10

### Task 10: Refetch everything when a good token is accepted

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/commands_test.go`

- [ ] in the `tokenValidatedMsg` success path, clear `m.remoteAnswered` before
      building commands
- [ ] fan out `fetchRemoteCmd` for every tool with `t.GitHub != ""` — `Init`'s
      predicate, **not** `needsRemote`, which returns false for exactly the
      tools that rendered stale-but-present data and therefore need it most
- [ ] document the choice and its cost inline (a goroutine plus a `cache.json`
      read per tool, not API quota)
- [ ] write a test: an accepted token clears `remoteAnswered` and returns a
      batch sized for every tool with a GitHub ref
- [ ] write a test: a tool whose card is already populated is still in the batch
      (the regression `needsRemote` would cause)
- [ ] write a test: a rejected validation (`msg.err != nil`) changes neither
- [ ] run `go test -race ./...` — must pass before task 11

### Task 11: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented: 401 named, error
      propagated, retry degrades to anonymous, three surfaces present, `[r]`
      always answers
- [ ] verify edge cases: a 401 on a token-less request, a rejected env token, a
      re-entered identical bad token, and **no** token at all (must not read as
      degraded)
- [ ] verify the two `doGH` consumers whose negatives outlive the degraded
      window: a README negative cached under the new error class is **not**
      cleared by the `tokenValidatedMsg` cleanup, which drops only rate-limited
      entries (`model.go:1110-1114`); and `SelfLatest` has no force variant, so a
      self-check that failed while degraded stays failed until the next launch.
      Decide per case: fix here, or record as known and out of scope
- [ ] run the full suite: `go test -race ./...`
- [ ] run the `preflight` skill (build / vet / `go test -race` / golangci-lint)
- [ ] verify no test reaches a real config path (the `TestConfigDirIsolated`
      tests stay green)

### Task 12: [Final] Update documentation

- [ ] update the **GitHub API** section of `CLAUDE.md`: `doGH` now retries
      without the token on a 401, and `RepoData.Err` no longer carries only
      `ErrRateLimited`
- [ ] update the **Async fetch responsibility split** sentence in `CLAUDE.md`
      that reads *"`Err` carries `ErrRateLimited` or nil, so an offline start and
      a 5xx both arrive with a nil error"* — the second half stops being true
- [ ] update the **status bar** and **API-status overlay** paragraphs of
      `CLAUDE.md` with the `api✕` state and the rejected-token line
- [ ] update the two in-code comments that assert the same obsolete claim and
      that `docs-sync` cannot see: the `remoteMsg.conclusive` field doc
      (`internal/model/model.go:67-71`) and the `remoteAnswered` field doc
      (`:399-408`)
- [ ] run the `docs-sync` skill to catch anything else that drifted
- [ ] update `README.md` only if it documents the token flow
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes,
informational only*

**Manual verification** (cannot be covered by tests — no automated test may hold
a real credential):

- with a **warm** cache, put a deliberately invalid token in
  `~/.config/keepkit/token`, start keepkit, and confirm: cards still render, the
  gauge reads `api✕` against a 60-limit, `[a]` explains the rejection with the
  mask still visible, and `[r]` succeeds silently
- with a **cold** cache (`rm ~/.config/keepkit/cache.json`) and the same bad
  token, confirm the honest degraded outcome: a partial fill, then
  `refresh failed: rate limited — press [a]` — 34 tools × 3 requests exceeds the
  anonymous 60/h, and this is the case the plan does *not* claim to make whole
- replace the token with a valid one via `[a] → [e]` and confirm every card
  refetches without a restart and the `✕` clears immediately
- confirm `GITHUB_TOKEN=<invalid> keepkit` degrades the same way and that the
  overlay names `env` as the source
- confirm a run with **no** token configured shows no `✕` and no rejection text

**External system updates**: none. No consuming projects, no deployment config,
no third-party integration is affected.
