package version

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestConcurrentFetch verifies that parallel forced fetches for multiple
// repos all end up in the cache (no write is lost due to a race condition).
func TestConcurrentFetch(t *testing.T) {
	repos := []string{"owner/toolA", "owner/toolB", "owner/toolC"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case len(r.URL.Path) > 0 && r.URL.Path[len(r.URL.Path)-1] == 's' &&
			len(r.URL.Path) > 10 && r.URL.Path[len(r.URL.Path)-10:] == "/languages":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]int{"Go": 1000})
		case len(r.URL.Path) > 7 && r.URL.Path[len(r.URL.Path)-7:] == "/latest":
			w.Header().Set("Content-Type", "application/json")
			// Extract repo name from path for unique tag
			_ = json.NewEncoder(w).Encode(map[string]string{
				"tag_name":     "v1.0.0",
				"body":         "release notes",
				"html_url":     "https://github.com" + r.URL.Path,
				"published_at": "2025-01-01T00:00:00Z",
			})
		default:
			// Repo info endpoint
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"archived":         false,
				"description":      "test tool",
				"stargazers_count": 42,
			})
		}
	}))
	defer srv.Close()

	dir := t.TempDir()

	// Override package-level vars for this test (restored via defer).
	origAPIBase := testAPIBase
	origCacheDir := testCacheDir
	defer func() {
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	}()
	testAPIBase = srv.URL
	testCacheDir = dir

	var wg sync.WaitGroup
	for _, repo := range repos {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			RefreshRepoData("github.com/" + r)
		}(repo)
	}
	wg.Wait()

	cache := LoadCache()
	for _, repo := range repos {
		if _, ok := cache[repo]; !ok {
			t.Errorf("cache missing entry for %q after concurrent RefreshRepoData", repo)
		}
	}
}

// TestUpdateCacheEntryConcurrentRepos verifies that parallel updateCacheEntry
// calls for distinct repos all persist — no write is lost to a lost-update race.
func TestUpdateCacheEntryConcurrentRepos(t *testing.T) {
	dir := t.TempDir()
	origCacheDir := testCacheDir
	defer func() { testCacheDir = origCacheDir }()
	testCacheDir = dir

	const m = 20
	repos := make([]string, m)
	for i := 0; i < m; i++ {
		repos[i] = "owner/tool" + string(rune('A'+i))
	}

	var wg sync.WaitGroup
	for _, repo := range repos {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			updateCacheEntry(r, func(existing CacheEntry) CacheEntry {
				existing.Latest = r
				return existing
			})
		}(repo)
	}
	wg.Wait()

	cache := LoadCache()
	if len(cache) != m {
		t.Fatalf("cache has %d entries, want %d", len(cache), m)
	}
	for _, repo := range repos {
		entry, ok := cache[repo]
		if !ok {
			t.Errorf("cache missing entry for %q", repo)
			continue
		}
		if entry.Latest != repo {
			t.Errorf("entry %q Latest = %q, want %q", repo, entry.Latest, repo)
		}
	}
}

// TestUpdateCacheEntryConcurrentSameRepo verifies that concurrent updates to a
// single repo are serialized: the final count reflects every increment, so no
// read-modify-write is lost.
func TestUpdateCacheEntryConcurrentSameRepo(t *testing.T) {
	dir := t.TempDir()
	origCacheDir := testCacheDir
	defer func() { testCacheDir = origCacheDir }()
	testCacheDir = dir

	const n = 50
	const repo = "owner/tool"

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			updateCacheEntry(repo, func(existing CacheEntry) CacheEntry {
				existing.Stars++
				return existing
			})
		}()
	}
	wg.Wait()

	cache := LoadCache()
	if got := cache[repo].Stars; got != n {
		t.Errorf("Stars = %d after %d concurrent increments, want %d", got, n, n)
	}
}

// githubTestServer returns an httptest server that answers the release, repo
// info and languages endpoints, plus a cleanup that restores the package vars.
func githubTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case len(r.URL.Path) >= 10 && r.URL.Path[len(r.URL.Path)-10:] == "/languages":
			_ = json.NewEncoder(w).Encode(map[string]int{"Go": 1000})
		case len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"tag_name":     "v2.3.4",
				"body":         "release notes",
				"html_url":     "https://github.com" + r.URL.Path,
				"published_at": "2025-01-01T00:00:00Z",
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"archived":         false,
				"description":      "test tool",
				"stargazers_count": 42,
			})
		}
	}))
	origAPIBase := testAPIBase
	origCacheDir := testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	t.Cleanup(func() {
		srv.Close()
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	})
	return srv
}

// TestGetRepoDataReadAfterWrite verifies GetRepoData persists its result
// through updateCacheEntry so a subsequent read within TTL is served from
// cache, with release, card and languages fields all intact.
func TestGetRepoDataReadAfterWrite(t *testing.T) {
	githubTestServer(t)

	d := GetRepoData("github.com/owner/repo")
	if d.Latest != "v2.3.4" {
		t.Fatalf("Latest = %q, want v2.3.4", d.Latest)
	}
	if d.Languages["Go"] != 1000 {
		t.Errorf("Languages = %v, want Go:1000", d.Languages)
	}
	entry, ok := LoadCache()["owner/repo"]
	if !ok {
		t.Fatal("cache missing entry after GetRepoData")
	}
	if entry.Latest != "v2.3.4" {
		t.Errorf("cached Latest = %q, want v2.3.4", entry.Latest)
	}
	if entry.RepoStatus != "active" {
		t.Errorf("cached RepoStatus = %q, want active", entry.RepoStatus)
	}
	if entry.Languages == nil {
		t.Error("cached Languages nil after GetRepoData")
	}

	// A second read within TTL must preserve every field (served from cache).
	d2 := GetRepoData("github.com/owner/repo")
	if d2.Latest != "v2.3.4" || d2.Languages["Go"] != 1000 {
		t.Errorf("second read = %+v, want same data from cache", d2)
	}
}

// TestGetChangelogReadAfterWrite verifies GetChangelog persists body and
// preserves languages already populated by GetRepoData.
func TestGetChangelogReadAfterWrite(t *testing.T) {
	githubTestServer(t)

	GetRepoData("github.com/owner/repo")
	info, err := GetChangelog("github.com/owner/repo")
	if err != nil {
		t.Fatalf("GetChangelog: %v", err)
	}
	if info.Body != "release notes" {
		t.Errorf("changelog Body = %q, want release notes", info.Body)
	}
	entry := LoadCache()["owner/repo"]
	if entry.Body != "release notes" {
		t.Errorf("cached Body = %q, want release notes", entry.Body)
	}
	if entry.Languages["Go"] != 1000 {
		t.Errorf("cached Languages = %v, want Go:1000 preserved from GetRepoData", entry.Languages)
	}
}

// TestConcurrentInitLikeFetch mimics startup: for several repos two GetRepoData
// calls run in parallel; every repo must end up with both release and card
// fields intact — no lost write between concurrent passes.
func TestConcurrentInitLikeFetch(t *testing.T) {
	githubTestServer(t)

	repos := []string{"owner/toolA", "owner/toolB", "owner/toolC", "owner/toolD"}
	var wg sync.WaitGroup
	for _, repo := range repos {
		field := "github.com/" + repo
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				GetRepoData(field)
			}()
		}
	}
	wg.Wait()

	cache := LoadCache()
	for _, repo := range repos {
		entry, ok := cache[repo]
		if !ok {
			t.Errorf("cache missing entry for %q", repo)
			continue
		}
		if entry.Latest != "v2.3.4" {
			t.Errorf("%q Latest = %q, want v2.3.4", repo, entry.Latest)
		}
		if entry.Languages["Go"] != 1000 {
			t.Errorf("%q Languages = %v, want Go:1000", repo, entry.Languages)
		}
	}
}

// TestGetRepoDataSinglePass verifies GetRepoData returns latest+status+about+
// stars+languages from one pass and that a second call within TTL is served
// entirely from cache (no further network requests).
func TestGetRepoDataSinglePass(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case len(r.URL.Path) >= 10 && r.URL.Path[len(r.URL.Path)-10:] == "/languages":
			_ = json.NewEncoder(w).Encode(map[string]int{"Go": 1000})
		case len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"tag_name":     "v3.1.4",
				"body":         "release notes",
				"html_url":     "https://github.com" + r.URL.Path,
				"published_at": "2025-01-01T00:00:00Z",
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"archived":         false,
				"description":      "test tool",
				"stargazers_count": 42,
			})
		}
	}))
	origAPIBase := testAPIBase
	origCacheDir := testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	}()

	d := GetRepoData("github.com/owner/repo")
	if d.Latest != "v3.1.4" {
		t.Errorf("Latest = %q, want v3.1.4", d.Latest)
	}
	if d.RepoStatus != "active" {
		t.Errorf("RepoStatus = %q, want active", d.RepoStatus)
	}
	if d.About != "test tool" {
		t.Errorf("About = %q, want test tool", d.About)
	}
	if d.Stars != 42 {
		t.Errorf("Stars = %d, want 42", d.Stars)
	}
	if d.Languages["Go"] != 1000 {
		t.Errorf("Languages = %v, want Go:1000", d.Languages)
	}

	mu.Lock()
	afterFirst := requests
	mu.Unlock()
	if afterFirst == 0 {
		t.Fatal("expected network requests on first GetRepoData")
	}

	d2 := GetRepoData("github.com/owner/repo")
	mu.Lock()
	afterSecond := requests
	mu.Unlock()
	if afterSecond != afterFirst {
		t.Errorf("second GetRepoData made %d extra requests, want 0 (cache hit)", afterSecond-afterFirst)
	}
	if d2.Latest != "v3.1.4" || d2.Languages["Go"] != 1000 {
		t.Errorf("cache-hit RepoData = %+v, want same as first call", d2)
	}
}

// TestGetRepoDataLanguagesFailureStillCaches verifies that when the languages
// endpoint fails (leaving Languages nil) but the other endpoints succeed, a
// second call within TTL is still served from cache and does not trigger a full
// three-endpoint re-fetch. This guards the regression where the cache-hit gate
// required Languages != nil, forcing a network pass on every start for any repo
// whose languages endpoint was flaky or rate-limited.
func TestGetRepoDataLanguagesFailureStillCaches(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		switch {
		case len(r.URL.Path) >= 10 && r.URL.Path[len(r.URL.Path)-10:] == "/languages":
			w.WriteHeader(http.StatusInternalServerError)
		case len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/latest":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"tag_name":     "v1.0.0",
				"body":         "release notes",
				"html_url":     "https://github.com" + r.URL.Path,
				"published_at": "2025-01-01T00:00:00Z",
			})
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"archived":         false,
				"description":      "test tool",
				"stargazers_count": 7,
			})
		}
	}))
	origAPIBase := testAPIBase
	origCacheDir := testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	}()

	d := GetRepoData("github.com/owner/repo")
	if d.Latest != "v1.0.0" {
		t.Errorf("Latest = %q, want v1.0.0", d.Latest)
	}
	if d.Languages != nil {
		t.Errorf("Languages = %v, want nil after languages fetch failure", d.Languages)
	}

	mu.Lock()
	afterFirst := requests
	mu.Unlock()
	if afterFirst == 0 {
		t.Fatal("expected network requests on first GetRepoData")
	}

	d2 := GetRepoData("github.com/owner/repo")
	mu.Lock()
	afterSecond := requests
	mu.Unlock()
	if afterSecond != afterFirst {
		t.Errorf("second GetRepoData made %d extra requests, want 0 (cache hit despite nil Languages)", afterSecond-afterFirst)
	}
	if d2.Latest != "v1.0.0" {
		t.Errorf("cache-hit Latest = %q, want v1.0.0", d2.Latest)
	}
}

// TestGetRepoDataTotalFailureDoesNotPoisonCache verifies that when both the
// release and repo-info endpoints fail on a cold cache, GetRepoData does NOT
// write a fresh-but-empty entry. A subsequent call must retry over the network
// (not be served an empty cache hit) once the endpoints recover.
func TestGetRepoDataTotalFailureDoesNotPoisonCache(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		failing := fail
		mu.Unlock()
		if failing {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case len(r.URL.Path) >= 10 && r.URL.Path[len(r.URL.Path)-10:] == "/languages":
			_ = json.NewEncoder(w).Encode(map[string]int{"Go": 1000})
		case len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"archived": false, "description": "test tool", "stargazers_count": 9,
			})
		}
	}))
	origAPIBase := testAPIBase
	origCacheDir := testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	}()

	d := GetRepoData("github.com/owner/repo")
	if d.Latest != "" || d.RepoStatus != "" {
		t.Errorf("first (failing) GetRepoData = %+v, want zero RepoData", d)
	}

	// Endpoints recover; a second call must retry (no poisoned cache hit).
	mu.Lock()
	fail = false
	afterFirst := requests
	mu.Unlock()

	d2 := GetRepoData("github.com/owner/repo")
	mu.Lock()
	afterSecond := requests
	mu.Unlock()
	if afterSecond == afterFirst {
		t.Fatal("second GetRepoData made no requests, cache was poisoned by total failure")
	}
	if d2.Latest != "v2.0.0" {
		t.Errorf("recovered Latest = %q, want v2.0.0", d2.Latest)
	}
}

// TestGetRepoDataStaleFallbackOnReleaseFailure verifies that when only the
// release endpoint fails on a re-fetch (repo info still succeeds), the failed
// field keeps its stale cached value while the successful fields refresh. This
// exercises the field-by-field stale fallback inside the updateCacheEntry mutate.
func TestGetRepoDataStaleFallbackOnReleaseFailure(t *testing.T) {
	var mu sync.Mutex
	failRelease := false
	stars := 11
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fr := failRelease
		curStars := stars
		mu.Unlock()
		switch {
		case len(r.URL.Path) >= 10 && r.URL.Path[len(r.URL.Path)-10:] == "/languages":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]int{"Go": 500})
		case len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/latest":
			if fr {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.2.3"})
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"archived": false, "description": "good tool", "stargazers_count": curStars,
			})
		}
	}))
	origAPIBase := testAPIBase
	origCacheDir := testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	}()

	// Populate a good entry.
	if d := GetRepoData("github.com/owner/repo"); d.Latest != "v1.2.3" {
		t.Fatalf("setup: Latest = %q, want v1.2.3", d.Latest)
	}

	// Expire the cached entry so the next call re-fetches, fail only the release
	// endpoint, and change stars so we can confirm the repo-info fields refreshed.
	updateCacheEntry("owner/repo", func(e CacheEntry) CacheEntry {
		e.CheckedAt = time.Now().Add(-2 * cacheTTL)
		return e
	})
	mu.Lock()
	failRelease = true
	stars = 22
	mu.Unlock()

	d := GetRepoData("github.com/owner/repo")
	if d.Latest != "v1.2.3" {
		t.Errorf("stale fallback Latest = %q, want v1.2.3 preserved", d.Latest)
	}
	if d.Stars != 22 {
		t.Errorf("refreshed Stars = %d, want 22 (repo info succeeded)", d.Stars)
	}
}

// TestGetRepoDataInfoFailureDoesNotPoisonCache verifies that when repo-info is
// rate-limited on a cold cache while release and languages succeed, the entry is
// NOT marked fresh: the About/stars gap must be retried on the next start rather
// than served blank for the whole TTL. This is the regression behind "About
// stopped loading and the info section is missing fields" for a tool tracked
// during a rate-limited cold start.
func TestGetRepoDataInfoFailureDoesNotPoisonCache(t *testing.T) {
	var mu sync.Mutex
	failInfo := true
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		fi := failInfo
		mu.Unlock()
		switch {
		case len(r.URL.Path) >= 10 && r.URL.Path[len(r.URL.Path)-10:] == "/languages":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]int{"Rust": 500})
		case len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/latest":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.1.3"})
		default:
			if fi {
				// Rate-limited repo-info: 403 with the quota exhausted.
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"archived": false, "description": "hacker news tui", "stargazers_count": 9,
			})
		}
	}))
	origAPIBase := testAPIBase
	origCacheDir := testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	}()

	// First pass: release + languages succeed, repo-info rate-limited. The
	// successful fields must persist, but About stays blank.
	d := GetRepoData("github.com/owner/repo")
	if d.Latest != "v0.1.3" {
		t.Errorf("Latest = %q, want v0.1.3 (release succeeded)", d.Latest)
	}
	if d.About != "" {
		t.Errorf("About = %q, want empty (repo-info failed)", d.About)
	}

	mu.Lock()
	afterFirst := requests
	mu.Unlock()

	// Repo-info recovers. Because the partial failure must NOT have marked the
	// entry fresh, the next call re-fetches and fills About/stars.
	mu.Lock()
	failInfo = false
	mu.Unlock()

	d2 := GetRepoData("github.com/owner/repo")
	mu.Lock()
	afterSecond := requests
	mu.Unlock()
	if afterSecond == afterFirst {
		t.Fatal("second GetRepoData made no requests: cache was poisoned fresh-but-blank by the repo-info failure")
	}
	if d2.About != "hacker news tui" {
		t.Errorf("About = %q, want \"hacker news tui\" after repo-info recovered", d2.About)
	}
	if d2.Stars != 9 {
		t.Errorf("Stars = %d, want 9 after repo-info recovered", d2.Stars)
	}
	if d2.Latest != "v0.1.3" {
		t.Errorf("Latest = %q, want v0.1.3 preserved", d2.Latest)
	}
}

// TestGetRepoDataInvalidField verifies empty and unparseable github fields
// return a zero RepoData without panicking or hitting the network.
func TestGetRepoDataInvalidField(t *testing.T) {
	for _, field := range []string{"", "onlyowner"} {
		got := GetRepoData(field)
		if got.Latest != "" || got.RepoStatus != "" || got.About != "" ||
			got.Stars != 0 || got.Languages != nil || got.Body != "" ||
			got.HtmlUrl != "" || got.PublishedAt != "" {
			t.Errorf("GetRepoData(%q) = %+v, want zero RepoData", field, got)
		}
	}
}

// TestClassifyStatusRateLimited verifies a 403 whose own header reads
// X-RateLimit-Remaining==0 classifies as ErrRateLimited, while a 403 with
// remaining>0 (genuine access denial) returns a generic error. The check reads
// the response header, not the global rl snapshot.
func TestClassifyStatusRateLimited(t *testing.T) {
	// Restore the shared snapshot afterwards so this test's dirty write doesn't
	// leak into order-dependent tests reading Rate().
	resetRate(t)
	// Seed global rl with a "healthy" remaining to prove classifyStatus ignores it.
	rlMu.Lock()
	rl = RateLimit{Limit: 5000, Remaining: 5000, Known: true}
	rlMu.Unlock()

	exhausted := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
	exhausted.Header.Set("X-RateLimit-Remaining", "0")
	if err := classifyStatus(exhausted); !errors.Is(err, ErrRateLimited) {
		t.Errorf("classifyStatus(403, remaining=0) = %v, want ErrRateLimited", err)
	}

	tooMany := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
	tooMany.Header.Set("X-RateLimit-Remaining", "0")
	if err := classifyStatus(tooMany); !errors.Is(err, ErrRateLimited) {
		t.Errorf("classifyStatus(429, remaining=0) = %v, want ErrRateLimited", err)
	}

	denied := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
	denied.Header.Set("X-RateLimit-Remaining", "37")
	if err := classifyStatus(denied); err == nil || errors.Is(err, ErrRateLimited) {
		t.Errorf("classifyStatus(403, remaining=37) = %v, want generic HTTP error", err)
	}

	// No rate-limit header at all → generic error even though status is 403.
	bare := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
	if err := classifyStatus(bare); err == nil || errors.Is(err, ErrRateLimited) {
		t.Errorf("classifyStatus(403, no header) = %v, want generic HTTP error", err)
	}
}

// TestClassifyStatusTaxonomy pins the closed set of named failure classes and,
// just as importantly, their neighbours: a 401 is bad credentials and must carry
// ErrTokenInvalid, while the two 403 forms and a 404 must keep answering exactly
// what they answered before. The 401 branch sits between them, so a misplaced
// return would show up here as a neighbour changing class.
func TestClassifyStatusTaxonomy(t *testing.T) {
	tests := []struct {
		name      string
		code      int
		remaining string
		want      error // nil means "generic, named neither sentinel"
	}{
		{"401 is a rejected token", http.StatusUnauthorized, "", ErrTokenInvalid},
		{"401 with a remaining header is still a rejected token", http.StatusUnauthorized, "4999", ErrTokenInvalid},
		{"403 exhausted is rate limited", http.StatusForbidden, "0", ErrRateLimited},
		{"429 exhausted is rate limited", http.StatusTooManyRequests, "0", ErrRateLimited},
		{"403 with quota left is a plain denial", http.StatusForbidden, "37", nil},
		{"404 is generic", http.StatusNotFound, "4999", nil},
		{"500 is generic", http.StatusInternalServerError, "4999", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyStatus(respWith(t, tt.code, tt.remaining, "/repos/cli/cli"))
			if err == nil {
				t.Fatalf("classifyStatus(%d) = nil, want an error", tt.code)
			}
			if tt.want != nil {
				if !errors.Is(err, tt.want) {
					t.Fatalf("classifyStatus(%d) = %v, want %v", tt.code, err, tt.want)
				}
				return
			}
			if errors.Is(err, ErrTokenInvalid) || errors.Is(err, ErrRateLimited) {
				t.Fatalf("classifyStatus(%d) = %v, want an unnamed generic error", tt.code, err)
			}
		})
	}
}

// authProbeServer answers 401 to any request carrying an Authorization header
// and 200 to any request without one, recording every request's auth state in
// order. It is the shape of a GitHub that has expired the stored credential: the
// same URLs work perfectly well anonymously.
type authProbeServer struct {
	*httptest.Server
	mu   sync.Mutex
	auth []bool // one entry per request: did it carry Authorization?
}

func newAuthProbeServer(t *testing.T, body string) *authProbeServer {
	t.Helper()
	p := &authProbeServer{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authed := r.Header.Get("Authorization") != ""
		p.mu.Lock()
		p.auth = append(p.auth, authed)
		p.mu.Unlock()
		if authed {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// All three headers, since updateRateFromHeaders drops an observation
		// missing any one of them.
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", "59")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *authProbeServer) requests() []bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bool(nil), p.auth...)
}

// TestDoGHRetriesUnauthenticatedOn401 is the degradation this whole change
// rests on: an expired token is strictly worse than no token, because the very
// same URLs answer 200 without one. A 401 must therefore cost one wasted request
// and then work, not blank the session.
func TestDoGHRetriesUnauthenticatedOn401(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")
	resetRate(t)
	if err := SetToken("ghp_expired"); err != nil {
		t.Fatal(err)
	}

	srv := newAuthProbeServer(t, `{"tag_name":"v1.0.0"}`)

	req, err := http.NewRequest("GET", srv.URL+"/repos/cli/cli/releases/latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := doGH(req)
	if err != nil {
		t.Fatalf("doGH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 from the unauthenticated retry", resp.StatusCode)
	}
	if got := srv.requests(); len(got) != 2 || !got[0] || got[1] {
		t.Errorf("requests (authed?) = %v, want [true false] — one rejected, one retried header-less", got)
	}
	if !TokenRejected() {
		t.Error("TokenRejected() = false after a 401 to an authorized request")
	}
	// The retry's headers are the honest window and must be what the gauge reads.
	if r := Rate(); r.Limit != 60 {
		t.Errorf("Rate().Limit = %d, want 60 accounted from the anonymous retry", r.Limit)
	}
}

// TestDoGHSkipsTokenAfterRejection pins that the rejection is remembered: later
// requests go out header-less on the FIRST attempt. Without it every request in
// the session would pay a doomed round trip.
func TestDoGHSkipsTokenAfterRejection(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")
	resetRate(t)
	if err := SetToken("ghp_expired"); err != nil {
		t.Fatal(err)
	}

	srv := newAuthProbeServer(t, `{"tag_name":"v1.0.0"}`)

	for i := range 3 {
		req, err := http.NewRequest("GET", srv.URL+"/repos/cli/cli/releases/latest", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := doGH(req)
		if err != nil {
			t.Fatalf("doGH #%d: %v", i, err)
		}
		_ = resp.Body.Close()
	}

	// First call: rejected + retried. The two after it: one request each.
	want := []bool{true, false, false, false}
	got := srv.requests()
	if len(got) != len(want) {
		t.Fatalf("made %d requests %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requests (authed?) = %v, want %v", got, want)
		}
	}
}

// TestDoGHDoesNotRetryATokenlessRequest verifies the retry is gated on the
// request having carried credentials. A 401 to an anonymous request is a
// defensive case (a proxy, an enterprise host) and repeating it verbatim would
// double every request for nothing.
func TestDoGHDoesNotRetryATokenlessRequest(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")
	resetRate(t)

	var requests int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/repos/cli/cli/releases/latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := doGH(req)
	if err != nil {
		t.Fatalf("doGH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	mu.Lock()
	n := requests
	mu.Unlock()
	if n != 1 {
		t.Errorf("made %d requests, want 1 — nothing to retry without", n)
	}
	if err := classifyStatus(resp); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("classifyStatus = %v, want ErrTokenInvalid", err)
	}
	if TokenRejected() {
		t.Error("TokenRejected() = true after a 401 to a request that carried no token")
	}
}

// TestDoGHKeepsTheTokenWhenAnonymousAlso401s pins what the 401 is evidence
// ABOUT. Dropping the header has to change the answer for the credential to be
// the thing at fault; a host that refuses an anonymous request too — a proxy, an
// enterprise instance — is refusing the resource. Marking the token there would
// strip Authorization for the rest of the session and put "rejected (HTTP 401)"
// in the [a] overlay beside a credential that is perfectly good.
func TestDoGHKeepsTheTokenWhenAnonymousAlso401s(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")
	resetRate(t)
	if err := SetToken("ghp_perfectly_fine"); err != nil {
		t.Fatal(err)
	}

	var requests int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/repos/cli/cli/releases/latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := doGH(req)
	if err != nil {
		t.Fatalf("doGH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	mu.Lock()
	n := requests
	mu.Unlock()
	if n != 2 {
		t.Errorf("made %d requests, want 2 — the retry still has to happen to learn this", n)
	}
	if TokenRejected() {
		t.Error("TokenRejected() = true, but dropping the header changed nothing — " +
			"the host refuses the resource, not the credential")
	}
	if got := resolveToken(); got != "ghp_perfectly_fine" {
		t.Errorf("resolveToken() = %q, want the token still sent on later requests", got)
	}
}

// TestFetchRateWithTokenNeverSeesTheRetry pins the doGH bypass as an invariant.
// Validation is the one caller that must observe a 401 rather than survive it:
// riding the shared path would answer an anonymous 200 and persist a token
// already known to be dead.
func TestFetchRateWithTokenNeverSeesTheRetry(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")
	resetRate(t)

	srv := newAuthProbeServer(t, `{"resources":{"core":{"limit":60,"remaining":59}}}`)
	origAPIBase := testAPIBase
	testAPIBase = srv.URL
	t.Cleanup(func() { testAPIBase = origAPIBase })

	if _, err := FetchRateWithToken("ghp_candidate"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("FetchRateWithToken = %v, want ErrTokenInvalid", err)
	}
	if got := srv.requests(); len(got) != 1 || !got[0] {
		t.Errorf("requests (authed?) = %v, want exactly one authorized attempt", got)
	}
	// Validation must not arm the session-wide suppression either: the candidate
	// was never the token in effect.
	if TokenRejected() {
		t.Error("TokenRejected() = true after validating an unrelated candidate")
	}
}

// TestFetchRateParsesCore verifies FetchRate decodes resources.core from the
// /rate_limit endpoint and updates the shared snapshot.
func TestFetchRateParsesCore(t *testing.T) {
	reset := time.Now().Add(30 * time.Minute).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rate_limit" {
			t.Errorf("unexpected path %q, want /rate_limit", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"resources": map[string]interface{}{
				"core": map[string]interface{}{
					"limit":     5000,
					"remaining": 4321,
					"reset":     reset,
				},
			},
		})
	}))
	origAPIBase := testAPIBase
	testAPIBase = srv.URL
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
	}()

	snap, err := FetchRate()
	if err != nil {
		t.Fatalf("FetchRate: %v", err)
	}
	if snap.Limit != 5000 || snap.Remaining != 4321 || !snap.Known {
		t.Errorf("FetchRate snapshot = %+v, want Limit=5000 Remaining=4321 Known=true", snap)
	}
	if snap.Reset.Unix() != reset {
		t.Errorf("FetchRate Reset = %d, want %d", snap.Reset.Unix(), reset)
	}
	if got := Rate(); got.Remaining != 4321 {
		t.Errorf("global Rate() Remaining = %d, want 4321", got.Remaining)
	}
}

// TestFetchRateWithTokenValid verifies FetchRateWithToken sends the candidate
// token and does not mutate global tokenMem.
func TestFetchRateWithTokenValid(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"resources": map[string]interface{}{
				"core": map[string]interface{}{"limit": 5000, "remaining": 5000, "reset": 0},
			},
		})
	}))
	origAPIBase := testAPIBase
	testAPIBase = srv.URL
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
	}()

	tokenMu.RLock()
	before := tokenMem
	tokenMu.RUnlock()

	snap, err := FetchRateWithToken("ghp_candidate123")
	if err != nil {
		t.Fatalf("FetchRateWithToken: %v", err)
	}
	if snap.Limit != 5000 {
		t.Errorf("snapshot Limit = %d, want 5000", snap.Limit)
	}
	if gotAuth != "Bearer ghp_candidate123" {
		t.Errorf("Authorization = %q, want Bearer ghp_candidate123", gotAuth)
	}
	tokenMu.RLock()
	after := tokenMem
	tokenMu.RUnlock()
	if after != before {
		t.Errorf("tokenMem mutated by FetchRateWithToken: %q -> %q", before, after)
	}
}

// TestFetchRateWithTokenInvalid verifies a 401 returns ErrTokenInvalid and does
// not mutate global tokenMem.
func TestFetchRateWithTokenInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	origAPIBase := testAPIBase
	testAPIBase = srv.URL
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
	}()

	tokenMu.RLock()
	before := tokenMem
	tokenMu.RUnlock()

	_, err := FetchRateWithToken("bad-token")
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("FetchRateWithToken(bad) = %v, want ErrTokenInvalid", err)
	}
	tokenMu.RLock()
	after := tokenMem
	tokenMu.RUnlock()
	if after != before {
		t.Errorf("tokenMem mutated on invalid token: %q -> %q", before, after)
	}
}

// TestFetchReleaseRateLimited verifies fetchRelease surfaces ErrRateLimited when
// the endpoint returns 403 with X-RateLimit-Remaining==0.
func TestFetchReleaseRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	origAPIBase := testAPIBase
	testAPIBase = srv.URL
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
	}()

	_, err := fetchRelease("owner/repo")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("fetchRelease on 403/remaining=0 = %v, want ErrRateLimited", err)
	}
}

// TestExtractRepo guards the delegation to loader.NormalizeRepo so a stored
// github field is normalized to "owner/repo" before it reaches the GitHub API.
func TestExtractRepo(t *testing.T) {
	tests := []struct {
		field string
		want  string
	}{
		{"github.com/owner/repo", "owner/repo"},
		{"https://github.com/owner/repo/tree/main", "owner/repo"},
		{"github.com/owner/repo.git", "owner/repo"},
		{"onlyowner", ""},
	}
	for _, tt := range tests {
		if got := extractRepo(tt.field); got != tt.want {
			t.Errorf("extractRepo(%q) = %q, want %q", tt.field, got, tt.want)
		}
	}
}

// TestFetchRateMalformedBody verifies a 200 /rate_limit response with a body
// that is not valid JSON surfaces a decode error and leaves the shared snapshot
// untouched (never a bogus Known snapshot).
func TestFetchRateMalformedBody(t *testing.T) {
	resetRate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{not json"))
	}))
	origAPIBase := testAPIBase
	testAPIBase = srv.URL
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
	}()

	if _, err := FetchRate(); err == nil {
		t.Fatal("FetchRate on malformed body = nil error, want decode error")
	}
	if got := Rate(); got.Known {
		t.Errorf("shared snapshot became Known after malformed body: %+v", got)
	}
}

// TestFetchRateNon200 verifies a non-200 /rate_limit status is surfaced as an
// error via classifyStatus rather than decoded as a valid snapshot.
func TestFetchRateNon200(t *testing.T) {
	resetRate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	origAPIBase := testAPIBase
	testAPIBase = srv.URL
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
	}()

	if _, err := FetchRate(); err == nil {
		t.Fatal("FetchRate on 500 = nil error, want error")
	}
	if got := Rate(); got.Known {
		t.Errorf("shared snapshot became Known after 500: %+v", got)
	}
}

// TestRefreshRepoDataBypassesTTL verifies RefreshRepoData re-fetches and updates
// a cache entry that is still within TTL, while plain GetRepoData in the same
// state serves the cache without a request.
func TestRefreshRepoDataBypassesTTL(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	stars := 42
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		curStars := stars
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case len(r.URL.Path) >= 10 && r.URL.Path[len(r.URL.Path)-10:] == "/languages":
			_ = json.NewEncoder(w).Encode(map[string]int{"Go": 1000})
		case len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"archived": false, "description": "tool", "stargazers_count": curStars,
			})
		}
	}))
	origAPIBase := testAPIBase
	origCacheDir := testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	}()

	// Prime a fresh entry.
	if d := GetRepoData("github.com/owner/repo"); d.Stars != 42 {
		t.Fatalf("setup: Stars = %d, want 42", d.Stars)
	}
	mu.Lock()
	afterPrime := requests
	mu.Unlock()

	// A plain GetRepoData is a cache hit: no new requests.
	GetRepoData("github.com/owner/repo")
	mu.Lock()
	afterHit := requests
	mu.Unlock()
	if afterHit != afterPrime {
		t.Errorf("GetRepoData made %d extra requests on a fresh entry, want 0", afterHit-afterPrime)
	}

	// Change the server, then force a refresh: it must re-fetch and update.
	mu.Lock()
	stars = 99
	mu.Unlock()
	d := RefreshRepoData("github.com/owner/repo")
	mu.Lock()
	afterRefresh := requests
	mu.Unlock()
	if afterRefresh == afterHit {
		t.Fatal("RefreshRepoData made no requests, want a forced network pass")
	}
	if d.Stars != 99 {
		t.Errorf("RefreshRepoData Stars = %d, want 99 (fresh value)", d.Stars)
	}
}

// TestRefreshRepoDataKeepsConclusiveGuard verifies a forced refresh reuses the
// conclusive-CheckedAt guard: when repo-info is rate-limited while release and
// languages succeed, the entry is not marked fresh and the cached About is not
// wiped.
func TestRefreshRepoDataKeepsConclusiveGuard(t *testing.T) {
	var mu sync.Mutex
	failInfo := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fi := failInfo
		mu.Unlock()
		switch {
		case len(r.URL.Path) >= 10 && r.URL.Path[len(r.URL.Path)-10:] == "/languages":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]int{"Go": 1000})
		case len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/latest":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
		default:
			if fi {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"archived": false, "description": "good tool", "stargazers_count": 5,
			})
		}
	}))
	origAPIBase := testAPIBase
	origCacheDir := testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	}()

	// Prime a good entry with a known About.
	if d := GetRepoData("github.com/owner/repo"); d.About != "good tool" {
		t.Fatalf("setup: About = %q, want good tool", d.About)
	}

	// Force a refresh while repo-info is rate-limited.
	mu.Lock()
	failInfo = true
	mu.Unlock()
	d := RefreshRepoData("github.com/owner/repo")
	if d.About != "good tool" {
		t.Errorf("About = %q, want good tool preserved (repo-info failed)", d.About)
	}

	// The entry must not have been marked fresh: repo-info recovers, and the next
	// call re-fetches instead of serving a poisoned entry.
	mu.Lock()
	failInfo = false
	mu.Unlock()
	if d2 := GetRepoData("github.com/owner/repo"); d2.About != "good tool" {
		t.Errorf("post-recovery About = %q, want good tool (re-fetched, not poisoned)", d2.About)
	}
}

// TestRefreshChangelogBypassesTTL verifies RefreshChangelog re-fetches release
// notes even when a fresh cache entry already carries a body.
func TestRefreshChangelogBypassesTTL(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	body := "old notes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		curBody := body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/latest" {
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0", "body": curBody})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"archived": false, "description": "tool"})
	}))
	origAPIBase := testAPIBase
	origCacheDir := testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	defer func() {
		srv.Close()
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	}()

	// Prime a fresh entry with a body.
	if info, err := GetChangelog("github.com/owner/repo"); err != nil || info.Body != "old notes" {
		t.Fatalf("setup: info = %+v, err = %v", info, err)
	}
	mu.Lock()
	afterPrime := requests
	mu.Unlock()

	// Plain GetChangelog is a cache hit (fresh + body present): no new request.
	_, _ = GetChangelog("github.com/owner/repo")
	mu.Lock()
	afterHit := requests
	mu.Unlock()
	if afterHit != afterPrime {
		t.Errorf("GetChangelog made %d extra requests on a fresh body entry, want 0", afterHit-afterPrime)
	}

	// Force refresh: must re-fetch and pick up the new body.
	mu.Lock()
	body = "new notes"
	mu.Unlock()
	info, err := RefreshChangelog("github.com/owner/repo")
	if err != nil {
		t.Fatalf("RefreshChangelog: %v", err)
	}
	mu.Lock()
	afterRefresh := requests
	mu.Unlock()
	if afterRefresh == afterHit {
		t.Fatal("RefreshChangelog made no requests, want a forced network pass")
	}
	if info.Body != "new notes" {
		t.Errorf("RefreshChangelog Body = %q, want new notes", info.Body)
	}
}

// tagsFallbackServer answers /releases/latest with 404 and /tags with whatever
// the caller configured — the shape of a repo that publishes git tags but never
// cut a GitHub release. tagsStatus fails the tags endpoint; hits() counts the
// tags requests, which is how a test tells "the write was gated" apart from "no
// request was ever made".
type tagsFallbackServer struct {
	tags       []string
	tagsStatus int
	tagsHits   int
	mu         sync.Mutex
}

func (s *tagsFallbackServer) hits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tagsHits
}

func newTagsFallbackServer(t *testing.T, tags []string, tagsStatus int) *tagsFallbackServer {
	t.Helper()
	state := &tagsFallbackServer{tags: tags, tagsStatus: tagsStatus}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/languages"):
			_ = json.NewEncoder(w).Encode(map[string]int{"Go": 1000})
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.WriteHeader(http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/tags"):
			state.mu.Lock()
			state.tagsHits++
			tags, status := state.tags, state.tagsStatus
			state.mu.Unlock()
			if status != 0 && status != http.StatusOK {
				if status == http.StatusForbidden {
					w.Header().Set("X-RateLimit-Remaining", "0")
				}
				w.WriteHeader(status)
				return
			}
			out := make([]map[string]any, 0, len(tags))
			for _, tag := range tags {
				out = append(out, map[string]any{"name": tag})
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"archived":         false,
				"description":      "test tool",
				"stargazers_count": 42,
			})
		}
	}))
	origAPIBase, origCacheDir := testAPIBase, testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	t.Cleanup(func() {
		srv.Close()
		testAPIBase = origAPIBase
		testCacheDir = origCacheDir
	})
	return state
}

// TestGetRepoDataTagsFallback: a repo with no release but with tags gets a real
// latest out of the tag list — the semver maximum, not the list's first entry
// (GitHub orders /tags by creation, so v1.9.0 routinely precedes v1.10.0). The
// tag is NOT a release: only Latest is written, the notes tuple stays empty, and
// ReleaseMissing stays true so the self-check keeps quiet about it.
func TestGetRepoDataTagsFallback(t *testing.T) {
	srv := newTagsFallbackServer(t, []string{"v1.2.0", "v1.10.0", "not-a-version"}, http.StatusOK)

	d := GetRepoData("github.com/owner/repo")
	if d.Latest != "v1.10.0" {
		t.Errorf("Latest = %q, want the semver max v1.10.0", d.Latest)
	}
	e := LoadCache()["owner/repo"]
	if e.Latest != "v1.10.0" {
		t.Errorf("cached Latest = %q, want v1.10.0", e.Latest)
	}
	if e.Body != "" || e.HtmlUrl != "" || e.PublishedAt != "" {
		t.Errorf("a tag is not a release: tuple = %q/%q/%q, want empty", e.Body, e.HtmlUrl, e.PublishedAt)
	}
	if !e.ReleaseMissing {
		t.Error("ReleaseMissing = false — there is still no release, and the self-check reads this")
	}
	if e.CheckedAt.IsZero() {
		t.Error("CheckedAt not stamped — a 404 on releases is still a conclusive pass")
	}
	// Served from cache on the second read: the fallback costs one request per
	// window, not per visit.
	before := srv.hits()
	if d2 := GetRepoData("github.com/owner/repo"); d2.Latest != "v1.10.0" {
		t.Errorf("second read Latest = %q, want the cached v1.10.0", d2.Latest)
	}
	if srv.hits() != before {
		t.Errorf("the cached read spent %d extra tags requests", srv.hits()-before)
	}
}

// TestGetRepoDataTagsFallbackEmpty: no releases and no tags is today's outcome
// unchanged — a conclusive but blank Latest, nothing invented.
func TestGetRepoDataTagsFallbackEmpty(t *testing.T) {
	newTagsFallbackServer(t, nil, http.StatusOK)

	if d := GetRepoData("github.com/owner/repo"); d.Latest != "" {
		t.Errorf("Latest = %q, want empty", d.Latest)
	}
	e := LoadCache()["owner/repo"]
	if e.Latest != "" || !e.ReleaseMissing || e.CheckedAt.IsZero() {
		t.Errorf("entry = %+v, want blank Latest, ReleaseMissing, stamped CheckedAt", e)
	}
	if e.About != "test tool" {
		t.Errorf("About = %q — the card half of the pass must be unaffected", e.About)
	}
}

// TestGetRepoDataTagsFallbackErrorKeepsConclusive: the tags request is a side
// pass. A failure there must degrade to exactly today's errNoReleases outcome —
// it may not veto the CheckedAt stamp, or every launch would re-spend the two
// repo-info requests for a repo whose release answer was already conclusive.
func TestGetRepoDataTagsFallbackErrorKeepsConclusive(t *testing.T) {
	newTagsFallbackServer(t, nil, http.StatusInternalServerError)

	d := GetRepoData("github.com/owner/repo")
	if d.Latest != "" {
		t.Errorf("Latest = %q, want empty on a failed tags pass", d.Latest)
	}
	e := LoadCache()["owner/repo"]
	if e.CheckedAt.IsZero() {
		t.Error("CheckedAt not stamped — a tags failure must not veto the release pass's conclusiveness")
	}
	if !e.ReleaseMissing {
		t.Error("ReleaseMissing = false, want the 404 recorded as before")
	}
	if e.About != "test tool" {
		t.Errorf("About = %q, want the card written as before", e.About)
	}
}

// TestGetRepoDataTagsFallbackKeepsPreservedTuple is the collision case:
// applyReleaseOutcome deliberately PRESERVES a deleted release's tuple on a 404
// ("known content wins"), and a tag written over it would render the new tag
// beside the old release's notes and clickable URL — a hybrid card, served for
// the rest of the window by getChangelog's cached-body gate. So the tag write is
// gated on the entry carrying no release tuple at all.
func TestGetRepoDataTagsFallbackKeepsPreservedTuple(t *testing.T) {
	srv := newTagsFallbackServer(t, []string{"v2.0.0"}, http.StatusOK)
	updateCacheEntry("owner/repo", func(existing CacheEntry) CacheEntry {
		e := existing
		e.Latest = "v1.0.0"
		e.Body = "the old notes"
		e.HtmlUrl = "https://github.com/owner/repo/releases/tag/v1.0.0"
		e.PublishedAt = "2025-01-01T00:00:00Z"
		return e
	})

	GetRepoData("github.com/owner/repo")
	// Exactness: without this the test cannot tell a gated write from a tags
	// request that never happened — both leave the preserved tuple intact.
	if got := srv.hits(); got != 1 {
		t.Errorf("tags requests = %d, want exactly 1 (the fallback ran and its write was gated)", got)
	}
	e := LoadCache()["owner/repo"]
	if e.Latest != "v1.0.0" {
		t.Errorf("Latest = %q, want the preserved v1.0.0 — a tag must not be written over a release tuple", e.Latest)
	}
	if e.Body != "the old notes" || e.HtmlUrl == "" || e.PublishedAt == "" {
		t.Errorf("preserved tuple damaged: %q/%q/%q", e.Body, e.HtmlUrl, e.PublishedAt)
	}
	if !e.ReleaseMissing {
		t.Error("ReleaseMissing = false, want the 404 recorded")
	}
}

// TestRepoDataConclusive pins the flag the model settles on. It is the version
// layer's answer to "is this tool done for the window", and it must be false on
// exactly the passes that left the entry stale on purpose — otherwise the model
// stops re-dispatching for a tool whose card was never filled.
//
// Err cannot carry that, and still cannot now that every failure class reaches
// the caller named: the two answer different questions and disagree in both
// directions. A repo with no releases settles the tool with a nil Err; a
// rate-limited pass that served a stale card is a non-nil Err on a pass that
// settled nothing.
func TestRepoDataConclusive(t *testing.T) {
	t.Run("clean pass is conclusive", func(t *testing.T) {
		githubTestServer(t)
		if d := GetRepoData("github.com/owner/repo"); !d.Conclusive {
			t.Error("Conclusive = false after a successful pass")
		}
		// The cache hit that follows is the same answer, served without a request.
		if d := GetRepoData("github.com/owner/repo"); !d.Conclusive {
			t.Error("Conclusive = false on a cache hit — a fresh entry is a settled one")
		}
	})

	t.Run("total failure is not conclusive", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		origAPIBase, origCacheDir := testAPIBase, testCacheDir
		testAPIBase, testCacheDir = srv.URL, t.TempDir()
		t.Cleanup(func() {
			srv.Close()
			testAPIBase, testCacheDir = origAPIBase, origCacheDir
		})

		d := GetRepoData("github.com/owner/repo")
		if d.Err == nil {
			t.Fatal("Err = nil after a 5xx — a transient failure now reaches the caller named")
		}
		if errors.Is(d.Err, ErrRateLimited) {
			t.Fatalf("Err = %v, want the plain transient error, not a rate-limit claim", d.Err)
		}
		if d.Conclusive {
			t.Error("Conclusive = true after a total failure — the entry was deliberately left stale")
		}
	})

	t.Run("no releases is conclusive with no error", func(t *testing.T) {
		// The definitive negative: /releases/latest 404s, the repo itself answers.
		// The tool is settled for the window and nothing failed, so the card must
		// not carry an error for the most ordinary state there is.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/releases/latest"), strings.HasSuffix(r.URL.Path, "/tags"):
				w.WriteHeader(http.StatusNotFound)
			case strings.HasSuffix(r.URL.Path, "/languages"):
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]int{"Go": 1000})
			default:
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"description": "a repo that never cut a release", "stargazers_count": 7,
				})
			}
		}))
		origAPIBase, origCacheDir := testAPIBase, testCacheDir
		testAPIBase, testCacheDir = srv.URL, t.TempDir()
		t.Cleanup(func() {
			srv.Close()
			testAPIBase, testCacheDir = origAPIBase, origCacheDir
		})

		d := GetRepoData("github.com/owner/repo")
		if d.Err != nil {
			t.Errorf("Err = %v, want nil — errNoReleases is a negative, not a failure", d.Err)
		}
		if !d.Conclusive {
			t.Error("Conclusive = false — a repo with no releases is settled for the window")
		}
	})

	t.Run("partial failure is not conclusive", func(t *testing.T) {
		// The release endpoint answers, repo info does not: getRepoData leaves
		// CheckedAt stale so the next pass refills About/stars/maintenance.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasSuffix(r.URL.Path, "/languages"):
				_ = json.NewEncoder(w).Encode(map[string]int{"Go": 1000})
			case strings.HasSuffix(r.URL.Path, "/releases/latest"):
				_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		origAPIBase, origCacheDir := testAPIBase, testCacheDir
		testAPIBase, testCacheDir = srv.URL, t.TempDir()
		t.Cleanup(func() {
			srv.Close()
			testAPIBase, testCacheDir = origAPIBase, origCacheDir
		})

		d := GetRepoData("github.com/owner/repo")
		if d.Latest != "v1.0.0" {
			t.Fatalf("Latest = %q, want the half that succeeded", d.Latest)
		}
		if d.Conclusive {
			t.Error("Conclusive = true while CheckedAt was left stale for a refill")
		}
	})
}

// TestRepoDataErrPickOrder pins which of the two core fetches' errors reaches
// the caller. ErrRateLimited outranks a transient failure whichever endpoint it
// came from, because it is the only class with an answer the user can act on;
// the rest are indistinguishable to the UI and it does not matter which one is
// picked, only that one is. Driven end to end through getRepoData rather than
// against pickFetchErr directly — the wiring is what regressed before.
func TestRepoDataErrPickOrder(t *testing.T) {
	// status returns a server answering each endpoint with the given code, 200
	// meaning "answer normally".
	serve := func(t *testing.T, relCode, infoCode int) {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			write := func(code int) bool {
				if code == http.StatusOK {
					return false
				}
				if code == http.StatusForbidden {
					w.Header().Set("X-RateLimit-Remaining", "0")
				}
				w.WriteHeader(code)
				return true
			}
			switch {
			case strings.HasSuffix(r.URL.Path, "/languages"):
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]int{"Go": 1000})
			case strings.HasSuffix(r.URL.Path, "/releases/latest"):
				if write(relCode) {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
			default:
				if write(infoCode) {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"description": "x", "stargazers_count": 1})
			}
		}))
		origAPIBase, origCacheDir := testAPIBase, testCacheDir
		testAPIBase, testCacheDir = srv.URL, t.TempDir()
		t.Cleanup(func() {
			srv.Close()
			testAPIBase, testCacheDir = origAPIBase, origCacheDir
		})
	}

	tests := []struct {
		name              string
		relCode, infoCode int
		wantRateLimited   bool
		wantErr           bool
	}{
		{"release rate-limited outranks a repo-info 5xx", http.StatusForbidden, http.StatusInternalServerError, true, true},
		{"repo-info rate-limited outranks a release 5xx", http.StatusInternalServerError, http.StatusForbidden, true, true},
		{"both rate-limited", http.StatusForbidden, http.StatusForbidden, true, true},
		{"both transient", http.StatusInternalServerError, http.StatusInternalServerError, false, true},
		{"release fails alone", http.StatusInternalServerError, http.StatusOK, false, true},
		{"repo info fails alone", http.StatusOK, http.StatusInternalServerError, false, true},
		{"clean pass carries no error", http.StatusOK, http.StatusOK, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serve(t, tt.relCode, tt.infoCode)
			d := GetRepoData("github.com/owner/repo")
			switch {
			case !tt.wantErr:
				if d.Err != nil {
					t.Fatalf("Err = %v, want nil", d.Err)
				}
			case d.Err == nil:
				t.Fatal("Err = nil, want a classified failure")
			case tt.wantRateLimited && !errors.Is(d.Err, ErrRateLimited):
				t.Fatalf("Err = %v, want ErrRateLimited", d.Err)
			case !tt.wantRateLimited && errors.Is(d.Err, ErrRateLimited):
				t.Fatalf("Err = %v, want a transient error, not a rate-limit claim", d.Err)
			}
		})
	}
}

// TestGetRepoDataTagsFallbackSkippedWhenRateLimited pins the placement of the
// fallback call: it sits AFTER the total-failure early return, so a pass that
// fetched nothing spends no request on tags whose result it would discard. The
// assertion is the request count — the resulting entry looks the same either
// way, which is exactly why the count is what has to be checked.
func TestGetRepoDataTagsFallbackSkippedWhenRateLimited(t *testing.T) {
	var (
		mu       sync.Mutex
		tagsHits int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tags") {
			mu.Lock()
			tagsHits++
			mu.Unlock()
		}
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Repo info is rate-limited: with the release endpoint's 404 that is a
		// total failure, and getRepoData returns before any tags request.
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	origAPIBase, origCacheDir := testAPIBase, testCacheDir
	testAPIBase, testCacheDir = srv.URL, t.TempDir()
	t.Cleanup(func() {
		srv.Close()
		testAPIBase, testCacheDir = origAPIBase, origCacheDir
	})

	d := GetRepoData("github.com/owner/repo")
	if !errors.Is(d.Err, ErrRateLimited) {
		t.Fatalf("Err = %v, want ErrRateLimited — the premise of this test", d.Err)
	}
	mu.Lock()
	got := tagsHits
	mu.Unlock()
	if got != 0 {
		t.Errorf("tags requests = %d, want 0 — a rate-limited pass must spend nothing on the fallback", got)
	}
	if d.Conclusive {
		t.Error("Conclusive = true after a rate-limited total failure")
	}
}
