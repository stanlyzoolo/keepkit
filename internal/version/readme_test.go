package version

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGetReadmeSuccessAndCache verifies the raw markdown round-trip, that the
// raw media type reaches the API, and that a second call within TTL is served
// from cache.json without a second request.
func TestGetReadmeSuccessAndCache(t *testing.T) {
	const body = "# tool\n\nsome *markdown* docs\n"
	var mu sync.Mutex
	requests := 0
	accepts := []string{}
	apiServer(t, apiHandlers{readme: func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		accepts = append(accepts, r.Header.Get("Accept"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}})

	got, err := GetReadme("github.com/owner/repo")
	if err != nil {
		t.Fatalf("GetReadme: unexpected error: %v", err)
	}
	if got != body {
		t.Errorf("GetReadme = %q, want %q", got, body)
	}

	mu.Lock()
	firstAccept := accepts[0]
	mu.Unlock()
	if firstAccept != "application/vnd.github.raw+json" {
		t.Errorf("Accept = %q, want application/vnd.github.raw+json", firstAccept)
	}

	// Read-after-write: the entry landed in the cache with its own timestamp.
	entry, ok := LoadCache()["owner/repo"]
	if !ok {
		t.Fatalf("cache has no entry for owner/repo")
	}
	if entry.Readme != body {
		t.Errorf("cached Readme = %q, want %q", entry.Readme, body)
	}
	if entry.ReadmeCheckedAt.IsZero() {
		t.Errorf("ReadmeCheckedAt is zero, want stamped")
	}
	if !entry.CheckedAt.IsZero() {
		t.Errorf("CheckedAt = %v, want untouched by a readme fetch", entry.CheckedAt)
	}

	// Second call inside TTL must not hit the network.
	if _, err := GetReadme("github.com/owner/repo"); err != nil {
		t.Fatalf("second GetReadme: %v", err)
	}
	mu.Lock()
	n := requests
	mu.Unlock()
	if n != 1 {
		t.Errorf("requests = %d, want 1 (second read served from cache)", n)
	}
}

// TestGetReadmeNotFound verifies a 404 maps to the typed ErrNoReadme.
func TestGetReadmeNotFound(t *testing.T) {
	apiServer(t, apiHandlers{readme: func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}})

	_, err := GetReadme("github.com/owner/repo")
	if !errors.Is(err, ErrNoReadme) {
		t.Errorf("err = %v, want ErrNoReadme", err)
	}
}

// TestGetReadmeRateLimited verifies a 403 with an exhausted quota maps to
// ErrRateLimited, the same classification the other fetchers produce.
func TestGetReadmeRateLimited(t *testing.T) {
	apiServer(t, apiHandlers{readme: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}})

	_, err := GetReadme("github.com/owner/repo")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("err = %v, want ErrRateLimited", err)
	}
}

// TestRefreshReadmeBypassesTTL verifies force re-fetches a fresh entry.
func TestRefreshReadmeBypassesTTL(t *testing.T) {
	var mu sync.Mutex
	body := "first"
	requests := 0
	apiServer(t, apiHandlers{readme: func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		b := body
		mu.Unlock()
		_, _ = w.Write([]byte(b))
	}})

	if _, err := GetReadme("github.com/owner/repo"); err != nil {
		t.Fatalf("setup GetReadme: %v", err)
	}
	mu.Lock()
	body = "second"
	mu.Unlock()

	got, err := RefreshReadme("github.com/owner/repo")
	if err != nil {
		t.Fatalf("RefreshReadme: %v", err)
	}
	if got != "second" {
		t.Errorf("RefreshReadme = %q, want %q", got, "second")
	}
	mu.Lock()
	n := requests
	mu.Unlock()
	if n != 2 {
		t.Errorf("requests = %d, want 2 (force bypassed the TTL)", n)
	}
}

// TestGetReadmeFailureKeepsCached verifies a transient failure on an expired
// entry serves the known README and leaves ReadmeCheckedAt stale, so the next
// call retries instead of being pinned to a blank panel for the whole TTL.
func TestGetReadmeFailureKeepsCached(t *testing.T) {
	var mu sync.Mutex
	fail := false
	apiServer(t, apiHandlers{readme: func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		f := fail
		mu.Unlock()
		if f {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("# cached docs"))
	}})

	if _, err := GetReadme("github.com/owner/repo"); err != nil {
		t.Fatalf("setup GetReadme: %v", err)
	}
	stamped := LoadCache()["owner/repo"].ReadmeCheckedAt

	// Expire the readme timestamp and break the endpoint.
	updateCacheEntry("owner/repo", func(e CacheEntry) CacheEntry {
		e.ReadmeCheckedAt = time.Now().Add(-2 * cacheTTL)
		return e
	})
	mu.Lock()
	fail = true
	mu.Unlock()

	got, err := GetReadme("github.com/owner/repo")
	if err != nil {
		t.Fatalf("GetReadme after failure: %v", err)
	}
	if got != "# cached docs" {
		t.Errorf("got %q, want the cached README preserved", got)
	}
	entry := LoadCache()["owner/repo"]
	if entry.Readme == "" {
		t.Errorf("cached Readme was wiped by a failed fetch")
	}
	if !entry.ReadmeCheckedAt.Before(stamped) {
		t.Errorf("ReadmeCheckedAt advanced on a failed fetch (%v)", entry.ReadmeCheckedAt)
	}
}

// TestGetChangelogPreservesReadme verifies the changelog writer carries the
// README fields over from the existing entry — a CacheEntry{...} literal there
// silently wiped them on every changelog fetch.
func TestGetChangelogPreservesReadme(t *testing.T) {
	apiServer(t, apiHandlers{readme: func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# keep me"))
	}})

	if _, err := GetReadme("github.com/owner/repo"); err != nil {
		t.Fatalf("setup GetReadme: %v", err)
	}
	before := LoadCache()["owner/repo"].ReadmeCheckedAt

	if _, err := GetChangelog("github.com/owner/repo"); err != nil {
		t.Fatalf("GetChangelog: %v", err)
	}

	entry := LoadCache()["owner/repo"]
	if entry.Readme != "# keep me" {
		t.Errorf("Readme = %q, want it preserved across a changelog fetch", entry.Readme)
	}
	if !entry.ReadmeCheckedAt.Equal(before) {
		t.Errorf("ReadmeCheckedAt = %v, want unchanged %v", entry.ReadmeCheckedAt, before)
	}
}

// TestGetReadmeDoesNotRefreshRepoCard verifies a successful README fetch leaves
// the repo-card CheckedAt alone: stamping the shared timestamp would mark a
// deliberately stale (rate-limited) repo pass as fresh and suppress its retry.
func TestGetReadmeDoesNotRefreshRepoCard(t *testing.T) {
	var mu sync.Mutex
	infoRequests := 0
	apiServer(t, apiHandlers{repoInfo: func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		infoRequests++
		n := infoRequests
		mu.Unlock()
		if n == 1 {
			// First repo-info pass is rate-limited: the entry must stay stale.
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"archived": false, "description": "recovered", "stargazers_count": 7,
		})
	}})

	// Partial failure: release succeeds, repo-info rate-limited → entry stale.
	if d := GetRepoData("github.com/owner/repo"); d.About != "" {
		t.Fatalf("setup: About = %q, want empty", d.About)
	}
	if !LoadCache()["owner/repo"].CheckedAt.IsZero() {
		t.Fatalf("setup: CheckedAt stamped despite a partial failure")
	}

	if _, err := GetReadme("github.com/owner/repo"); err != nil {
		t.Fatalf("GetReadme: %v", err)
	}
	if !LoadCache()["owner/repo"].CheckedAt.IsZero() {
		t.Fatalf("readme fetch stamped the repo-card CheckedAt")
	}

	// The repo pass must still retry and now fill About.
	if d := GetRepoData("github.com/owner/repo"); d.About != "recovered" {
		t.Errorf("About = %q, want %q (repo fetch retried)", d.About, "recovered")
	}
}

// TestGetRepoDataPreservesReadme is the repo-card twin of
// TestGetChangelogPreservesReadme: getRepoData's cache write runs at every
// startup and every refresh, so a CacheEntry{...} literal there would wipe a
// cached README on the very next version poll.
func TestGetRepoDataPreservesReadme(t *testing.T) {
	apiServer(t, apiHandlers{readme: func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# keep me"))
	}})

	if _, err := GetReadme("github.com/owner/repo"); err != nil {
		t.Fatalf("setup GetReadme: %v", err)
	}
	before := LoadCache()["owner/repo"].ReadmeCheckedAt

	if d := GetRepoData("github.com/owner/repo"); d.Latest != "v1.0.0" {
		t.Fatalf("setup GetRepoData: Latest = %q, want v1.0.0", d.Latest)
	}

	entry := LoadCache()["owner/repo"]
	if entry.Readme != "# keep me" {
		t.Errorf("Readme = %q, want it preserved across a repo-data fetch", entry.Readme)
	}
	if !entry.ReadmeCheckedAt.Equal(before) {
		t.Errorf("ReadmeCheckedAt = %v, want unchanged %v", entry.ReadmeCheckedAt, before)
	}
}

// TestFetchReadmeTruncatesOversized verifies the bounded read: the body is
// cached to disk and glamour-parsed on the update loop, so an outsized README
// is cut off and marked rather than carried whole.
func TestFetchReadmeTruncatesOversized(t *testing.T) {
	huge := strings.Repeat("x", readmeMaxBytes+4096)
	apiServer(t, apiHandlers{readme: func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(huge))
	}})

	md, err := GetReadme("github.com/owner/repo")
	if err != nil {
		t.Fatalf("GetReadme: %v", err)
	}
	if len(md) > readmeMaxBytes+64 {
		t.Errorf("README length = %d, want it capped near readmeMaxBytes (%d)", len(md), readmeMaxBytes)
	}
	if !strings.Contains(md, "README truncated") {
		t.Error("a truncated README must say so, or the panel looks complete")
	}
	if !strings.HasPrefix(md, "xxx") {
		t.Error("the retained prefix must be the real README content")
	}
}

// TestDoGHPreservesPresetAccept verifies doGH only defaults the Accept header
// when the caller left it empty — the raw README media type must survive.
func TestDoGHPreservesPresetAccept(t *testing.T) {
	var mu sync.Mutex
	seen := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Accept"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	preset, err := http.NewRequest("GET", srv.URL+"/preset", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	preset.Header.Set("Accept", "application/vnd.github.raw+json")
	resp, err := doGH(preset)
	if err != nil {
		t.Fatalf("doGH preset: %v", err)
	}
	resp.Body.Close()

	plain, err := http.NewRequest("GET", srv.URL+"/plain", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err = doGH(plain)
	if err != nil {
		t.Fatalf("doGH plain: %v", err)
	}
	resp.Body.Close()

	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	want := []string{"application/vnd.github.raw+json", "application/vnd.github.v3+json"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Accept headers = %v, want %v", got, want)
	}
}
