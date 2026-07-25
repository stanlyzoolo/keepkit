package version

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/stanlyzoolo/keeptui/internal/configdir"
	"github.com/stanlyzoolo/keeptui/internal/loader"
	"github.com/stanlyzoolo/keeptui/internal/logx"
)

const cacheTTL = 24 * time.Hour

// ghClient is the shared HTTP client for all GitHub API calls.
var ghClient = &http.Client{Timeout: 5 * time.Second}

// RateLimit is a snapshot of the GitHub REST API rate-limit state. Limit is 60
// for unauthenticated requests and 5000 with a token. Known reports whether any
// successful observation has been made yet.
type RateLimit struct {
	Limit     int
	Remaining int
	Reset     time.Time
	Known     bool
}

// rate state, guarded by rlMu.
var (
	rlMu sync.RWMutex
	rl   RateLimit
)

// Rate returns the current rate-limit snapshot.
func Rate() RateLimit {
	rlMu.RLock()
	defer rlMu.RUnlock()
	return rl
}

// updateRateFromHeaders parses the X-RateLimit-* response headers and folds
// them into the shared rl snapshot via mergeRateObservation. Missing or
// malformed headers are ignored (the previous snapshot is left untouched).
func updateRateFromHeaders(h http.Header) {
	limitStr := h.Get("X-RateLimit-Limit")
	remainingStr := h.Get("X-RateLimit-Remaining")
	resetStr := h.Get("X-RateLimit-Reset")
	if limitStr == "" && remainingStr == "" && resetStr == "" {
		return
	}
	limit, errL := strconv.Atoi(limitStr)
	remaining, errR := strconv.Atoi(remainingStr)
	resetUnix, errT := strconv.ParseInt(resetStr, 10, 64)
	if errL != nil || errR != nil || errT != nil {
		return
	}
	mergeRateObservation(RateLimit{
		Limit:     limit,
		Remaining: remaining,
		Reset:     time.Unix(resetUnix, 0),
		Known:     true,
	})
}

// mergeRateObservation folds a new rate snapshot into the shared rl under the
// shouldReplaceRate precedence rule and returns the resulting snapshot. Every
// write to rl must go through here so no observation source can clobber a more
// informed one.
func mergeRateObservation(snap RateLimit) RateLimit {
	rlMu.Lock()
	defer rlMu.Unlock()
	if shouldReplaceRate(rl, snap, time.Now()) {
		rl = snap
	}
	return rl
}

// shouldReplaceRate decides whether an incoming rate observation may replace
// the current one. GET /rate_limit is unreliable: with a token it can report a
// pristine counter (used=0, sliding reset) while the per-request X-RateLimit-*
// headers count real usage — observed live against api.github.com. So the rule
// is "more informed wins": accept a snapshot that reports the same or more
// usage (lower/equal Remaining), a different Limit (the auth context changed,
// e.g. a token was added or removed), or anything once the current window has
// expired (a reset counter is then legitimate). A same-window snapshot claiming
// FEWER used requests than already observed is the /rate_limit staleness lie
// and is dropped. Out-of-order concurrent header updates get the same
// treatment for free: the most-used observation sticks.
func shouldReplaceRate(cur, snap RateLimit, now time.Time) bool {
	if !snap.Known {
		return false
	}
	if !cur.Known {
		return true
	}
	if snap.Limit != cur.Limit {
		return true
	}
	if snap.Remaining <= cur.Remaining {
		return true
	}
	return !now.Before(cur.Reset)
}

// doGH performs a GitHub API request with the shared client. It sets the Accept
// header and, when a token resolves, an Authorization header, then accounts for
// the rate-limit headers on the response. This is the single auth + accounting
// point for all GitHub calls; callers keep their own status-code handling.
//
// Accept is only defaulted when the caller set none, so a fetcher that needs a
// different media type (fetchReadme asks for raw markdown) can pre-set it and
// still ride the shared auth + rate-accounting path instead of a second HTTP
// code path.
func doGH(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/vnd.github.v3+json")
	}
	if token := resolveToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ghClient.Do(req)
	if err != nil {
		logx.Errorf("version.doGH: %s %s: %v", req.Method, req.URL.Path, err)
		return nil, err
	}
	updateRateFromHeaders(resp.Header)
	return resp, nil
}

// ErrRateLimited signals that a GitHub request was rejected because the API
// rate limit is exhausted (403/429 with X-RateLimit-Remaining == 0). Callers
// use errors.Is to degrade gracefully instead of showing a raw HTTP error.
var ErrRateLimited = errors.New("github api rate limit exceeded")

// ErrTokenInvalid signals that a candidate token failed validation against
// GET /rate_limit (HTTP 401). Used by FetchRateWithToken before persistence.
var ErrTokenInvalid = errors.New("github token invalid")

// errNoReleases signals that a repo's /releases/latest returned 404 — the repo
// simply has no releases. This is a conclusive answer (not a transient outage),
// so it must not block a cache entry from being marked fresh; otherwise a repo
// without releases would re-fetch all three endpoints on every start.
var errNoReleases = errors.New("no releases found")

// ErrNoReadme signals that a repo's /readme returned 404 — the repository has
// no README file at all. Like errNoReleases this is a conclusive answer, so the
// UI shows a tool-named placeholder instead of an error, and the model caches
// the negative for the session rather than refetching on every selection move.
var ErrNoReadme = errors.New("no readme found")

// readmeMaxBytes caps a fetched README. Well past any real tool's docs, but it
// keeps a pathological repository from bloating cache.json and from stalling the
// update loop in glamour (rendering is synchronous, and cost grows with size).
const readmeMaxBytes = 512 << 10

// classifyStatus maps a non-2xx GitHub response to an error. A 403 or 429 whose
// own X-RateLimit-Remaining header reads 0 is rate-limit exhaustion and returns
// ErrRateLimited; a 403 with remaining>0 is a genuine access denial and returns a
// generic HTTP error. Remaining is read from this response's own headers, never
// from the global rl snapshot, because a concurrent request may overwrite rl
// between this request's accounting and its classification.
func classifyStatus(resp *http.Response) error {
	remaining := resp.Header.Get("X-RateLimit-Remaining")
	path := ""
	if resp.Request != nil && resp.Request.URL != nil {
		path = resp.Request.URL.Path
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		if remaining == "0" {
			logx.Errorf("version.classifyStatus: %s http=%d remaining=%s: rate limited",
				path, resp.StatusCode, remaining)
			return ErrRateLimited
		}
	}
	// A 404 is a conclusive "not found" (a stale/private/renamed repo ref), not
	// a transient failure. It would recur on every startup and re-create the
	// session log each launch, defeating the "a log file means something went
	// wrong" signal — so classify it without logging. fetchRelease already
	// handles its own 404 as errNoReleases before reaching here.
	if resp.StatusCode != http.StatusNotFound {
		logx.Errorf("version.classifyStatus: %s http=%d remaining=%s", path, resp.StatusCode, remaining)
	}
	return fmt.Errorf("GitHub API: HTTP %d", resp.StatusCode)
}

// rateLimitResponse decodes the subset of GET /rate_limit we consume.
type rateLimitResponse struct {
	Resources struct {
		Core struct {
			Limit     int   `json:"limit"`
			Remaining int   `json:"remaining"`
			Reset     int64 `json:"reset"`
		} `json:"core"`
	} `json:"resources"`
}

// decodeCoreRate reads a /rate_limit response body into a RateLimit snapshot.
func decodeCoreRate(body io.Reader) (RateLimit, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return RateLimit{}, err
	}
	var rr rateLimitResponse
	if err := json.Unmarshal(data, &rr); err != nil {
		return RateLimit{}, err
	}
	return RateLimit{
		Limit:     rr.Resources.Core.Limit,
		Remaining: rr.Resources.Core.Remaining,
		Reset:     time.Unix(rr.Resources.Core.Reset, 0),
		Known:     true,
	}, nil
}

// FetchRate queries GET /rate_limit and merges the result into the shared
// snapshot, returning the merged value. The rate_limit endpoint does not
// consume core quota, so it is safe to call on demand (overlay open, refresh,
// startup seed) — but its numbers are advisory only: it can claim zero usage
// while real requests have been counted (see shouldReplaceRate), so it seeds
// an unknown snapshot and must never override a more informed one.
func FetchRate() (RateLimit, error) {
	req, err := http.NewRequest("GET", apiBase()+"/rate_limit", nil)
	if err != nil {
		return RateLimit{}, err
	}
	resp, err := doGH(req)
	if err != nil {
		return RateLimit{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RateLimit{}, classifyStatus(resp)
	}
	snap, err := decodeCoreRate(resp.Body)
	if err != nil {
		return RateLimit{}, err
	}
	return mergeRateObservation(snap), nil
}

// FetchRateWithToken validates a candidate token by issuing GET /rate_limit with
// an explicit Authorization header. It does NOT touch tokenMem, the token file,
// or the global rl snapshot, so an unpersisted token never leaks into shared
// state. A 401 returns ErrTokenInvalid; callers persist via SetToken only after a
// successful (200) result.
func FetchRateWithToken(token string) (RateLimit, error) {
	req, err := http.NewRequest("GET", apiBase()+"/rate_limit", nil)
	if err != nil {
		return RateLimit{}, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ghClient.Do(req)
	if err != nil {
		return RateLimit{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return RateLimit{}, ErrTokenInvalid
	}
	if resp.StatusCode != http.StatusOK {
		return RateLimit{}, classifyStatus(resp)
	}
	return decodeCoreRate(resp.Body)
}

var cacheMu sync.Mutex

// testCacheDir overrides the cache directory in tests.
var testCacheDir string

// testAPIBase overrides the GitHub API base URL in tests.
var testAPIBase string

// SetConfigDirForTesting points both files this package writes — cache.json and
// the token (testTokenDir, token.go) — at dir, and returns a restore func.
//
// Exported like loader.SetConfigDirForTesting and for the same reason: a model
// test that drives the [L] overlay reaches SetToken, which rewrites the user's
// real GitHub token file, and any fetch path rewrites the real cache. Test
// binaries install this once in TestMain rather than relying on every test to
// remember. restore reverts to the previous overrides, so a nested per-test
// override cannot un-isolate the rest of the run.
func SetConfigDirForTesting(dir string) (restore func()) {
	prevCache, prevToken := testCacheDir, testTokenDir
	testCacheDir, testTokenDir = dir, dir
	return func() { testCacheDir, testTokenDir = prevCache, prevToken }
}

// ConfigDirOverrides reports the active cache/token overrides ("" in a normal
// run), so a test can assert its own isolation.
func ConfigDirOverrides() (cacheDir, tokenDir string) {
	return testCacheDir, testTokenDir
}

type CacheEntry struct {
	Latest      string         `json:"latest"`
	CheckedAt   time.Time      `json:"checked_at"`
	Body        string         `json:"body,omitempty"`
	HtmlUrl     string         `json:"html_url,omitempty"`
	PublishedAt string         `json:"published_at,omitempty"`
	RepoStatus  string         `json:"repo_status,omitempty"` // "active" or "archived"
	About       string         `json:"about,omitempty"`
	Stars       int            `json:"stars,omitempty"`
	Languages   map[string]int `json:"languages,omitempty"`
	Readme      string         `json:"readme,omitempty"`
	// ReadmeCheckedAt is deliberately separate from CheckedAt: the repo-card
	// freshness gate in getRepoData is CheckedAt-only with no content check, so
	// a successful README fetch stamping the shared timestamp would mark a
	// deliberately stale (rate-limited) repo pass as fresh and serve a blank
	// card for the whole TTL. Two timestamps keep the poison guards independent.
	ReadmeCheckedAt time.Time `json:"readme_checked_at,omitzero"`
	// ReleaseCheckedAt mirrors ReadmeCheckedAt for the release-only pass that
	// SelfLatest makes (selfcheck.go), and is separate from CheckedAt for the
	// same reason: getRepoData's freshness gate is CheckedAt-only with no content
	// check, so a release-only pass stamping the shared timestamp would mark a
	// never-fetched (or deliberately stale) repo card as fresh and serve a blank
	// card for the whole TTL. Each poison guard owns its own timestamp.
	ReleaseCheckedAt time.Time `json:"release_checked_at,omitzero"`
	// ReleaseMissing records that the last conclusive release fetch answered 404
	// ("no latest release"): a repo that never published one, a single release
	// converted to a draft, or a repo the token cannot see. It exists so that
	// negative can be remembered *without* clearing the release tuple
	// (Latest/Body/HtmlUrl/PublishedAt), which belongs to the shared repo card and
	// which every pass here preserves on a 404 — getRepoData writes the tuple only
	// when the fetch succeeded, getChangelog falls back to the cached body, and
	// SelfLatest would otherwise be the one writer that destroys another feature's
	// content. Maintained by every release fetch — all three of SelfLatest,
	// getRepoData and getChangelog set it on a 404 and clear it on a fetched
	// release, including getChangelog's error path, which is the *only* pass that
	// still reaches /releases/latest when CheckedAt is fresh and Body is empty.
	// Read only by the self-check, whose banner must not offer an update to a
	// release that is gone.
	ReleaseMissing bool `json:"release_missing,omitempty"`
}

// RepoCard holds full repository metadata for display in the TUI.
type RepoCard struct {
	About       string
	Stars       int
	Languages   map[string]int
	Latest      string
	PublishedAt string
	HtmlUrl     string
	Body        string
	RepoStatus  string
}

// ReleaseInfo holds full release metadata from GitHub.
type ReleaseInfo struct {
	Tag         string
	Body        string
	HtmlUrl     string
	PublishedAt string
}

// RepoData is the combined result of a single network pass over a repository:
// release + repo info + languages. It carries everything the TUI needs for the
// version line and the repo card, so version and card no longer each fetch the
// repo info separately.
type RepoData struct {
	Latest      string
	RepoStatus  string
	About       string
	Stars       int
	Languages   map[string]int
	Body        string
	HtmlUrl     string
	PublishedAt string
	// Err carries a classified fetch error (currently only ErrRateLimited) so
	// callers can degrade gracefully via errors.Is. It is set when a release or
	// repo-info fetch was rejected for rate limiting; the returned data may still
	// hold stale values from the cache.
	Err error
}

func repoDataFromEntry(e CacheEntry) RepoData {
	return RepoData{
		Latest:      e.Latest,
		RepoStatus:  e.RepoStatus,
		About:       e.About,
		Stars:       e.Stars,
		Languages:   e.Languages,
		Body:        e.Body,
		HtmlUrl:     e.HtmlUrl,
		PublishedAt: e.PublishedAt,
	}
}

type Cache map[string]CacheEntry

// GetRepoData fetches release, repo info and languages for a tool's github
// field in a single pass and returns the combined data. A cache entry within
// TTL is served without any network call. On a miss it makes one call to each
// endpoint and persists the result atomically via updateCacheEntry; fields
// whose fetch fails are kept from the existing entry (stale fallback), so a
// rate-limited release does not wipe a previously known tag or card. Freshness
// is decided on CheckedAt alone: a failed languages fetch leaves Languages nil
// but must not force a full three-endpoint re-fetch on every subsequent start.
// A total failure (both release and repo info fail) is not written at all, so a
// cold-cache outage does not poison the entry as fresh-but-empty for the TTL.
// A partial failure — one core endpoint succeeds, the other fails transiently
// (rate limit / network / 5xx) — still merges the successful field but does NOT
// advance CheckedAt, so the entry stays stale and the next start retries to
// fill the missing field instead of serving it blank for the whole TTL.
func GetRepoData(githubField string) RepoData {
	return getRepoData(githubField, false)
}

// RefreshRepoData force-refreshes a tool's repo data, bypassing the cache TTL.
// It makes the same single network pass as GetRepoData and writes through the
// same updateCacheEntry merge — including the conclusive-CheckedAt guard — so a
// forced refresh that hits a rate limit on repo-info does not poison the entry.
func RefreshRepoData(githubField string) RepoData {
	return getRepoData(githubField, true)
}

// getRepoData is the shared implementation. force skips only the freshness
// short-circuit; the fetch, merge and conclusive-CheckedAt logic are identical
// on both paths.
func getRepoData(githubField string, force bool) RepoData {
	if githubField == "" {
		return RepoData{}
	}
	repo := extractRepo(githubField)
	if repo == "" {
		return RepoData{}
	}

	cache := LoadCache()
	entry, cached := cache[repo]
	if !force && cached && time.Since(entry.CheckedAt) < cacheTTL {
		return repoDataFromEntry(entry)
	}

	info, relErr := fetchRelease(repo)
	repoStatus, about, stars, infoErr := fetchRepoInfo(repo)
	langs, _ := fetchLanguages(repo)

	// Surface a rate-limit classification so the UI can render "rate limited"
	// instead of a bare failure. The data itself may still carry stale cache
	// values; the error is advisory.
	var rlErr error
	if errors.Is(relErr, ErrRateLimited) || errors.Is(infoErr, ErrRateLimited) {
		rlErr = ErrRateLimited
	}

	// Total fetch failure (offline / rate-limited): both the release and the repo
	// info endpoints failed. Do not write a fresh-but-empty entry — since freshness
	// is decided on CheckedAt alone, that would read back as a valid cache hit and
	// suppress any retry for the full TTL. Return the stale entry (zero value if the
	// cache was cold) so the next start retries.
	if relErr != nil && infoErr != nil {
		d := repoDataFromEntry(entry)
		d.Err = rlErr
		return d
	}

	// Only a conclusive pass may advance CheckedAt (mark the entry fresh). A
	// core fetch is conclusive when it either succeeded or returned a definitive
	// negative (a repo with no releases). A transient failure — rate limit,
	// network, 5xx — on either core endpoint must leave CheckedAt stale so the
	// next start retries and fills the gap. Otherwise a partial write (e.g.
	// release + languages succeed but repo-info is rate-limited) would stamp the
	// entry fresh for the full TTL with About/stars/maintenance permanently
	// blank until the cache expires.
	relConclusive := relErr == nil || errors.Is(relErr, errNoReleases)
	infoConclusive := infoErr == nil
	conclusive := relConclusive && infoConclusive

	var stored CacheEntry
	updateCacheEntry(repo, func(existing CacheEntry) CacheEntry {
		e := existing
		if conclusive {
			e.CheckedAt = time.Now()
		}
		// The release half of the write — tuple on success, ReleaseMissing both
		// ways — is applyReleaseOutcome's single definition, shared with the two
		// other passes that fetch a release, so no writer can maintain one half of
		// the flag's contract and forget the other.
		e = applyReleaseOutcome(e, info, relErr)
		if infoErr == nil {
			e.RepoStatus = repoStatus
			e.About = about
			e.Stars = stars
		}
		if langs != nil {
			e.Languages = langs
		}
		stored = e
		return e
	})
	d := repoDataFromEntry(stored)
	d.Err = rlErr
	return d
}

// GetChangelog returns full release info for a tool's github field.
// Uses cache when fresh and body is present; fetches otherwise.
func GetChangelog(githubField string) (ReleaseInfo, error) {
	return getChangelog(githubField, false)
}

// RefreshChangelog force-refreshes a tool's release notes, bypassing the cache
// TTL. On fetch error it still falls back to the cached body when present.
func RefreshChangelog(githubField string) (ReleaseInfo, error) {
	return getChangelog(githubField, true)
}

// getChangelog is the shared implementation. force skips only the freshness
// short-circuit; the fetch and cached-fallback logic are identical on both paths.
func getChangelog(githubField string, force bool) (ReleaseInfo, error) {
	if githubField == "" {
		return ReleaseInfo{}, fmt.Errorf("no github field")
	}
	repo := extractRepo(githubField)
	if repo == "" {
		return ReleaseInfo{}, fmt.Errorf("cannot parse github field: %q", githubField)
	}

	cache := LoadCache()
	entry, cached := cache[repo]

	if !force && cached && time.Since(entry.CheckedAt) < cacheTTL && entry.Body != "" {
		return ReleaseInfo{Tag: entry.Latest, Body: entry.Body, HtmlUrl: entry.HtmlUrl, PublishedAt: entry.PublishedAt}, nil
	}

	info, err := fetchRelease(repo)
	if err != nil {
		// A 404 is conclusive, so it is recorded as a flag here too — the tuple
		// below is still served from cache, this pass destroys nothing (see
		// CacheEntry.ReleaseMissing). It matters because this is the only pass
		// that reaches /releases/latest when CheckedAt is fresh but Body is empty
		// (a release published without notes): getRepoData short-circuits, so
		// without this write nobody would observe a release that has since been
		// deleted or drafted, and the self-check's cached-tag branch would keep
		// offering it for the rest of the TTL.
		if errors.Is(err, errNoReleases) {
			markReleaseMissing(repo)
		}
		if cached {
			return ReleaseInfo{Tag: entry.Latest, Body: entry.Body, HtmlUrl: entry.HtmlUrl, PublishedAt: entry.PublishedAt}, nil
		}
		return ReleaseInfo{}, err
	}

	// Mutate a copy of the existing entry instead of building a literal: a
	// literal only carries the fields it knows about and silently wipes every
	// other one (Readme/ReadmeCheckedAt) on each changelog fetch. The release half
	// of the write is applyReleaseOutcome's, shared with the two other passes that
	// fetch a release; this one only adds its own freshness stamp.
	updateCacheEntry(repo, func(existing CacheEntry) CacheEntry {
		e := applyReleaseOutcome(existing, info, nil)
		e.CheckedAt = time.Now()
		return e
	})
	return info, nil
}

// GetReadme returns the raw README markdown for a tool's github field, served
// from cache.json while within TTL and fetched otherwise. It costs one request
// per tool per TTL window.
func GetReadme(githubField string) (string, error) {
	return getReadme(githubField, false)
}

// RefreshReadme force-refreshes a tool's README, bypassing the cache TTL. On a
// fetch error it still falls back to a cached README when one is present.
func RefreshReadme(githubField string) (string, error) {
	return getReadme(githubField, true)
}

// getReadme is the shared implementation. force skips only the freshness
// short-circuit; the fetch and cached-fallback logic are identical on both
// paths. A failed fetch never advances ReadmeCheckedAt, so a rate-limited or
// offline attempt does not poison the entry as fresh-but-blank for the TTL.
func getReadme(githubField string, force bool) (string, error) {
	if githubField == "" {
		return "", fmt.Errorf("no github field")
	}
	repo := extractRepo(githubField)
	if repo == "" {
		return "", fmt.Errorf("cannot parse github field: %q", githubField)
	}

	cache := LoadCache()
	entry, cached := cache[repo]

	if !force && cached && time.Since(entry.ReadmeCheckedAt) < cacheTTL && entry.Readme != "" {
		return entry.Readme, nil
	}

	md, err := fetchReadme(repo)
	if err != nil {
		// Known content wins over a transient failure; a 404 has no content to
		// fall back to and surfaces as ErrNoReadme.
		if entry.Readme != "" && !errors.Is(err, ErrNoReadme) {
			return entry.Readme, nil
		}
		return "", err
	}

	updateCacheEntry(repo, func(existing CacheEntry) CacheEntry {
		e := existing
		e.Readme = md
		e.ReadmeCheckedAt = time.Now()
		return e
	})
	return md, nil
}

// fetchReadme GETs /repos/{repo}/readme with the raw media type, so the response
// body is the README markdown itself rather than a base64-wrapped JSON envelope.
func fetchReadme(repo string) (string, error) {
	url := apiBase() + "/repos/" + repo + "/readme"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	// Pre-set Accept: doGH only defaults it when the caller left it empty.
	req.Header.Set("Accept", "application/vnd.github.raw+json")
	resp, err := doGH(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNoReadme
	}
	if resp.StatusCode != http.StatusOK {
		return "", classifyStatus(resp)
	}
	// Bounded read: the body is cached to disk and then glamour-parsed inside
	// the Bubble Tea update loop, so an outsized README would both bloat
	// cache.json and stall a keystroke. Truncation is marked so the panel does
	// not silently look complete.
	data, err := io.ReadAll(io.LimitReader(resp.Body, readmeMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > readmeMaxBytes {
		return string(data[:readmeMaxBytes]) + "\n\n*(README truncated)*\n", nil
	}
	return string(data), nil
}

func apiBase() string {
	if testAPIBase != "" {
		return testAPIBase
	}
	return "https://api.github.com"
}

func fetchRepoInfo(repo string) (status, about string, stars int, err error) {
	url := apiBase() + "/repos/" + repo
	req, reqErr := http.NewRequest("GET", url, nil)
	if reqErr != nil {
		return "", "", 0, reqErr
	}
	resp, doErr := doGH(req)
	if doErr != nil {
		return "", "", 0, doErr
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", 0, classifyStatus(resp)
	}
	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", "", 0, readErr
	}
	var info struct {
		Archived        bool   `json:"archived"`
		Description     string `json:"description"`
		StargazersCount int    `json:"stargazers_count"`
	}
	if unmarshalErr := json.Unmarshal(data, &info); unmarshalErr != nil {
		return "", "", 0, unmarshalErr
	}
	if info.Archived {
		return "archived", info.Description, info.StargazersCount, nil
	}
	return "active", info.Description, info.StargazersCount, nil
}

func fetchLanguages(repo string) (map[string]int, error) {
	url := apiBase() + "/repos/" + repo + "/languages"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := doGH(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, classifyStatus(resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var langs map[string]int
	if err := json.Unmarshal(data, &langs); err != nil {
		return nil, err
	}
	return langs, nil
}

func fetchRelease(repo string) (ReleaseInfo, error) {
	url := apiBase() + "/repos/" + repo + "/releases/latest"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return ReleaseInfo{}, err
	}
	resp, err := doGH(req)
	if err != nil {
		return ReleaseInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ReleaseInfo{}, errNoReleases
	}
	if resp.StatusCode != http.StatusOK {
		return ReleaseInfo{}, classifyStatus(resp)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ReleaseInfo{}, err
	}

	var release struct {
		TagName     string `json:"tag_name"`
		Body        string `json:"body"`
		HtmlUrl     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return ReleaseInfo{}, err
	}
	return ReleaseInfo{
		Tag:         release.TagName,
		Body:        release.Body,
		HtmlUrl:     release.HtmlUrl,
		PublishedAt: release.PublishedAt,
	}, nil
}

func extractRepo(githubField string) string {
	return loader.NormalizeRepo(githubField)
}

func cacheFilePath() (string, error) {
	if testCacheDir != "" {
		return filepath.Join(testCacheDir, "cache.json"), nil
	}
	base, err := configdir.Base()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "keeptui", "cache.json"), nil
}

func LoadCache() Cache {
	path, err := cacheFilePath()
	if err != nil {
		logx.Errorf("version.LoadCache: resolve path: %v", err)
		return Cache{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// A cold start with no cache.json yet is not an error.
		if !os.IsNotExist(err) {
			logx.Errorf("version.LoadCache: read %s: %v", path, err)
		}
		return Cache{}
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		logx.Errorf("version.LoadCache: parse %s: %v", path, err)
		return Cache{}
	}
	return c
}

// updateCacheEntry atomically applies mutate to a single repo's cache entry.
// It holds cacheMu across a fresh LoadCache → mutate → SaveCache cycle so that
// concurrent goroutines never overwrite each other's writes: mutate receives the
// current on-disk entry (or a zero value if absent) and must return the entry to
// store, pulling any fields it doesn't set from existing.
func updateCacheEntry(repo string, mutate func(existing CacheEntry) CacheEntry) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache := LoadCache()
	cache[repo] = mutate(cache[repo])
	SaveCache(cache)
}

// applyReleaseOutcome folds the result of one /releases/latest fetch into a cache
// entry. It is the single definition of what a release fetch does to the entry,
// used by all three passes that make one (getRepoData, getChangelog, SelfLatest)
// so the two halves of the ReleaseMissing contract — set on a 404, cleared on a
// fetched release — cannot be maintained by one writer and forgotten by the next.
//
// The release tuple is written only on success: it belongs to the shared repo
// card, and a 404 records the negative in the flag instead of destroying content
// another feature displays (see CacheEntry.ReleaseMissing). A transient failure
// (rate limit, network, 5xx) touches nothing at all — the entry stays as it was so
// the next pass retries. Freshness timestamps are the caller's business: each pass
// stamps its own (CheckedAt / ReleaseCheckedAt) under its own conclusiveness rule.
func applyReleaseOutcome(e CacheEntry, info ReleaseInfo, err error) CacheEntry {
	switch {
	case err == nil:
		e.Latest = info.Tag
		e.Body = info.Body
		e.HtmlUrl = info.HtmlUrl
		e.PublishedAt = info.PublishedAt
		e.ReleaseMissing = false
	case errors.Is(err, errNoReleases):
		e.ReleaseMissing = true
	}
	return e
}

// markReleaseMissing records a 404 on its own, for the one caller that observes
// the negative outside an entry write it was making anyway (getChangelog's error
// path, which serves the cached tuple and has nothing else to store).
func markReleaseMissing(repo string) {
	updateCacheEntry(repo, func(existing CacheEntry) CacheEntry {
		return applyReleaseOutcome(existing, ReleaseInfo{}, errNoReleases)
	})
}

func SaveCache(c Cache) {
	path, err := cacheFilePath()
	if err != nil {
		logx.Errorf("version.SaveCache: resolve path: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logx.Errorf("version.SaveCache: mkdir %s: %v", filepath.Dir(path), err)
		return
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		logx.Errorf("version.SaveCache: marshal: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		logx.Errorf("version.SaveCache: write %s: %v", path, err)
	}
}
