package version

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

// releaseJSON answers /releases/latest with a full release payload.
func releaseJSON(tag string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tag_name":     tag,
			"body":         "release notes",
			"html_url":     "https://github.com/" + SelfRepo + "/releases/tag/" + tag,
			"published_at": "2026-07-01T00:00:00Z",
		})
	}
}

// TestSelfLatestColdFetch verifies the release-only pass: it returns the tag,
// stamps ReleaseCheckedAt, leaves CheckedAt zero (so the repo card is not marked
// fresh-but-blank), and never touches the repo-info endpoint.
func TestSelfLatestColdFetch(t *testing.T) {
	hits := apiServer(t, apiHandlers{release: releaseJSON("v0.5.0")})

	got, err := SelfLatest()
	if err != nil {
		t.Fatalf("SelfLatest: %v", err)
	}
	if got != "v0.5.0" {
		t.Errorf("SelfLatest = %q, want v0.5.0", got)
	}

	entry, ok := LoadCache()[SelfRepo]
	if !ok {
		t.Fatalf("cache has no entry for %s", SelfRepo)
	}
	if entry.Latest != "v0.5.0" {
		t.Errorf("cached Latest = %q, want v0.5.0", entry.Latest)
	}
	if entry.ReleaseCheckedAt.IsZero() {
		t.Error("ReleaseCheckedAt is zero, want stamped")
	}
	if !entry.CheckedAt.IsZero() {
		t.Errorf("CheckedAt = %v, want untouched by a release-only pass", entry.CheckedAt)
	}
	if entry.HtmlUrl == "" || entry.PublishedAt == "" || entry.Body == "" {
		t.Errorf("HtmlUrl/PublishedAt/Body = %q/%q/%q, want the whole release tuple merged",
			entry.HtmlUrl, entry.PublishedAt, entry.Body)
	}
	if rel, info := hits.counts(); rel != 1 || info != 0 {
		t.Errorf("requests = release %d / repo-info %d, want 1 / 0", rel, info)
	}
}

// TestSelfLatestServedFromReleaseStamp verifies a second call inside the TTL
// makes no request at all.
func TestSelfLatestServedFromReleaseStamp(t *testing.T) {
	hits := apiServer(t, apiHandlers{release: releaseJSON("v0.5.0")})

	if _, err := SelfLatest(); err != nil {
		t.Fatalf("setup SelfLatest: %v", err)
	}
	got, err := SelfLatest()
	if err != nil {
		t.Fatalf("second SelfLatest: %v", err)
	}
	if got != "v0.5.0" {
		t.Errorf("SelfLatest = %q, want v0.5.0 from cache", got)
	}
	if rel, _ := hits.counts(); rel != 1 {
		t.Errorf("release requests = %d, want 1 (second read served from cache)", rel)
	}
}

// TestSelfLatestServedFromFullPass verifies the other freshness source: a
// tracked keepkit whose full repo pass already ran makes the self-check free.
func TestSelfLatestServedFromFullPass(t *testing.T) {
	hits := apiServer(t, apiHandlers{release: releaseJSON("v0.6.0")})

	if d := GetRepoData("https://github.com/" + SelfRepo); d.Latest != "v0.6.0" {
		t.Fatalf("setup GetRepoData: Latest = %q, want v0.6.0", d.Latest)
	}
	if e := LoadCache()[SelfRepo]; !e.ReleaseCheckedAt.IsZero() {
		t.Fatalf("full pass stamped ReleaseCheckedAt (%v), want it untouched", e.ReleaseCheckedAt)
	}

	relBefore, _ := hits.counts()
	got, err := SelfLatest()
	if err != nil {
		t.Fatalf("SelfLatest: %v", err)
	}
	if got != "v0.6.0" {
		t.Errorf("SelfLatest = %q, want v0.6.0", got)
	}
	if rel, _ := hits.counts(); rel != relBefore {
		t.Errorf("release requests = %d, want %d (fresh CheckedAt answers the self-check)", rel, relBefore)
	}
}

// TestSelfLatestStaleFullPassWithoutTagRefetches verifies the content check on
// the CheckedAt branch: a fresh full pass that left no tag is not an answer, so
// the self-check still makes its own request.
func TestSelfLatestFreshFullPassWithoutTagRefetches(t *testing.T) {
	hits := apiServer(t, apiHandlers{release: releaseJSON("v0.7.0")})

	// A full pass may stamp CheckedAt with an empty Latest; that alone must not
	// short-circuit the release-only pass.
	updateCacheEntry(SelfRepo, func(e CacheEntry) CacheEntry {
		e.CheckedAt = time.Now()
		e.About = "tracker"
		return e
	})

	got, err := SelfLatest()
	if err != nil {
		t.Fatalf("SelfLatest: %v", err)
	}
	if got != "v0.7.0" {
		t.Errorf("SelfLatest = %q, want v0.7.0", got)
	}
	if rel, _ := hits.counts(); rel != 1 {
		t.Errorf("release requests = %d, want 1 (a tag-less fresh entry is not an answer)", rel)
	}
}

// TestSelfLatestNoReleases verifies a 404 is conclusive: empty tag, nil error,
// ReleaseCheckedAt stamped, and no re-probe on the next call.
func TestSelfLatestNoReleases(t *testing.T) {
	hits := apiServer(t, apiHandlers{release: func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}})

	got, err := SelfLatest()
	if err != nil {
		t.Fatalf("SelfLatest: %v, want a conclusive no-release answer", err)
	}
	if got != "" {
		t.Errorf("SelfLatest = %q, want empty", got)
	}
	entry := LoadCache()[SelfRepo]
	if entry.ReleaseCheckedAt.IsZero() {
		t.Error("ReleaseCheckedAt is zero, want stamped (a repo without releases must not be re-probed)")
	}
	if !entry.CheckedAt.IsZero() {
		t.Errorf("CheckedAt = %v, want untouched", entry.CheckedAt)
	}

	if _, err := SelfLatest(); err != nil {
		t.Fatalf("second SelfLatest: %v", err)
	}
	if rel, _ := hits.counts(); rel != 1 {
		t.Errorf("release requests = %d, want 1 (the negative is cached)", rel)
	}
}

// TestSelfLatestDroppedReleaseKeepsSharedTuple pins the 404 contract: the answer
// is conclusively "no release" and is remembered as ReleaseMissing, but the
// release tuple a tracked keepkit's card shows (latest:, its date, the ↑ marker,
// the changelog body, the clickable release URL) survives untouched — the same
// thing getRepoData and getChangelog do with the identical 404. The banner is
// silenced by the flag, not by destroying another feature's content.
func TestSelfLatestDroppedReleaseKeepsSharedTuple(t *testing.T) {
	apiServer(t, apiHandlers{release: func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}})
	url := "https://github.com/" + SelfRepo + "/releases/tag/v0.5.0"
	updateCacheEntry(SelfRepo, func(e CacheEntry) CacheEntry {
		e.Latest = "v0.5.0"
		e.Body = "release notes"
		e.HtmlUrl = url
		e.PublishedAt = "2026-07-01T00:00:00Z"
		e.About = "tracker"
		return e
	})

	got, err := SelfLatest()
	if err != nil {
		t.Fatalf("SelfLatest: %v", err)
	}
	if got != "" {
		t.Errorf("SelfLatest = %q, want empty once the release is gone", got)
	}
	entry := LoadCache()[SelfRepo]
	if entry.Latest != "v0.5.0" || entry.Body != "release notes" || entry.HtmlUrl != url || entry.PublishedAt == "" {
		t.Errorf("release tuple = %q/%q/%q/%q, want it preserved for the card",
			entry.Latest, entry.Body, entry.HtmlUrl, entry.PublishedAt)
	}
	if !entry.ReleaseMissing {
		t.Error("ReleaseMissing = false, want the negative recorded outside the tuple")
	}
	if entry.About != "tracker" {
		t.Errorf("About = %q, want the card fields preserved", entry.About)
	}

	// The next call inside the TTL is served from that stamp and must agree — the
	// preserved tag must not come back as an update offer.
	if again, err := SelfLatest(); err != nil || again != "" {
		t.Errorf("second SelfLatest = %q, %v; want the cached negative", again, err)
	}
}

// TestSelfLatestNegativeSharedWithFullPass verifies the two passes agree on what a
// 404 means: whichever one observes it records ReleaseMissing, and whichever one
// later fetches a release clears it. Otherwise the self-check would either offer a
// tag the full pass only preserved for the card, or stay silent for a whole TTL
// after the release came back.
func TestSelfLatestNegativeSharedWithFullPass(t *testing.T) {
	var missing bool
	hits := apiServer(t, apiHandlers{release: func(w http.ResponseWriter, r *http.Request) {
		if missing {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		releaseJSON("v0.9.0")(w, r)
	}})

	// A full pass with a tag, then the release disappears and the full pass runs
	// again: it keeps the tuple (that is its contract) and records the negative.
	if d := GetRepoData("github.com/" + SelfRepo); d.Latest != "v0.9.0" {
		t.Fatalf("setup GetRepoData: Latest = %q, want v0.9.0", d.Latest)
	}
	missing = true
	if d := RefreshRepoData("github.com/" + SelfRepo); d.Latest != "v0.9.0" {
		t.Fatalf("RefreshRepoData: Latest = %q, want the tuple preserved on a 404", d.Latest)
	}
	if e := LoadCache()[SelfRepo]; !e.ReleaseMissing {
		t.Fatal("full pass 404 left ReleaseMissing false, want the negative recorded")
	}

	relBefore, _ := hits.counts()
	got, err := SelfLatest()
	if err != nil || got != "" {
		t.Errorf("SelfLatest = %q, %v; want the shared negative, not the preserved tag", got, err)
	}
	if rel, _ := hits.counts(); rel != relBefore {
		t.Errorf("release requests = %d, want %d (the fresh full pass answers)", rel, relBefore)
	}

	// The release comes back: the full pass clears the negative, so the self-check
	// offers the tag again without waiting out the TTL.
	missing = false
	if d := RefreshRepoData("github.com/" + SelfRepo); d.Latest != "v0.9.0" {
		t.Fatalf("RefreshRepoData: Latest = %q, want v0.9.0", d.Latest)
	}
	if got, err := SelfLatest(); err != nil || got != "v0.9.0" {
		t.Errorf("SelfLatest = %q, %v; want v0.9.0 once the release is back", got, err)
	}
}

// TestSelfLatestNegativeClearedByChangelogFetch covers the third writer of the
// release tuple: a changelog fetch that lands a release also retires a remembered
// "no release" negative, so the self-check reads the entry the same way whichever
// pass filled it.
func TestSelfLatestNegativeClearedByChangelogFetch(t *testing.T) {
	hits := apiServer(t, apiHandlers{release: releaseJSON("v1.2.3")})

	// A negative from an expired window: its own stamp no longer answers, so the
	// CheckedAt branch — the one the changelog fetch stamps — decides.
	updateCacheEntry(SelfRepo, func(e CacheEntry) CacheEntry {
		e.ReleaseMissing = true
		e.ReleaseCheckedAt = time.Now().Add(-2 * cacheTTL)
		return e
	})

	if _, err := GetChangelog("github.com/" + SelfRepo); err != nil {
		t.Fatalf("setup GetChangelog: %v", err)
	}
	relBefore, _ := hits.counts()

	got, err := SelfLatest()
	if err != nil || got != "v1.2.3" {
		t.Errorf("SelfLatest = %q, %v; want v1.2.3 — the fetched release retires the negative", got, err)
	}
	if rel, _ := hits.counts(); rel != relBefore {
		t.Errorf("release requests = %d, want %d (the fresh entry answers)", rel, relBefore)
	}
}

// TestSelfLatestNegativeRecordedByChangelog404 closes the third writer's other
// direction: a changelog fetch that answers 404 must *record* the negative, not
// only clear it on success. This is the one window where no other pass can:
// CheckedAt is fresh (so getRepoData short-circuits and never asks) while Body is
// empty (a release published without notes), which makes the changelog the only
// pass still reaching /releases/latest. Without the write, a release deleted or
// converted to a draft would go unobserved and the self-check's CheckedAt branch
// would keep offering the preserved tag for the rest of the TTL.
func TestSelfLatestNegativeRecordedByChangelog404(t *testing.T) {
	hits := apiServer(t, apiHandlers{release: func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}})

	// A fresh full pass that left a tag but no body — the changelog's own gate
	// (fresh CheckedAt *and* a non-empty Body) is what sends it to the network.
	checkedAt := time.Now()
	updateCacheEntry(SelfRepo, func(e CacheEntry) CacheEntry {
		e.Latest = "v0.5.0"
		e.HtmlUrl = "https://github.com/" + SelfRepo + "/releases/tag/v0.5.0"
		e.PublishedAt = "2026-07-01T00:00:00Z"
		e.CheckedAt = checkedAt
		return e
	})

	info, err := GetChangelog("github.com/" + SelfRepo)
	if err != nil {
		t.Fatalf("GetChangelog: %v", err)
	}
	if info.Tag != "v0.5.0" {
		t.Errorf("GetChangelog Tag = %q, want the cached tuple served on a 404", info.Tag)
	}
	if rel, _ := hits.counts(); rel != 1 {
		t.Fatalf("release requests = %d, want 1 (the empty Body must force the fetch)", rel)
	}

	entry := LoadCache()[SelfRepo]
	if !entry.ReleaseMissing {
		t.Error("ReleaseMissing = false, want the changelog's own 404 recorded")
	}
	if entry.Latest != "v0.5.0" || entry.HtmlUrl == "" || entry.PublishedAt == "" {
		t.Errorf("release tuple = %q/%q/%q, want it preserved for the card",
			entry.Latest, entry.HtmlUrl, entry.PublishedAt)
	}
	if !entry.CheckedAt.Equal(checkedAt) {
		t.Errorf("CheckedAt = %v, want %v (the flag write must not restamp freshness)", entry.CheckedAt, checkedAt)
	}

	// The self-check reads that entry through the still-fresh CheckedAt branch:
	// the flag, not the preserved tag, is the answer.
	got, err := SelfLatest()
	if err != nil || got != "" {
		t.Errorf("SelfLatest = %q, %v; want the recorded negative, not the preserved tag", got, err)
	}
	if rel, _ := hits.counts(); rel != 1 {
		t.Errorf("release requests = %d, want 1 (the fresh entry answers)", rel)
	}
}

// TestSelfLatestErrors verifies transient failures surface classified and stamp
// nothing, so the next launch retries instead of going quiet for a whole TTL.
func TestSelfLatestErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr error
	}{
		{
			name: "rate limited",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusForbidden)
			},
			wantErr: ErrRateLimited,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := apiServer(t, apiHandlers{release: tt.handler})

			got, err := SelfLatest()
			if err == nil {
				t.Fatalf("SelfLatest = %q, nil; want an error", got)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if got != "" {
				t.Errorf("SelfLatest = %q, want empty on failure", got)
			}
			if e, ok := LoadCache()[SelfRepo]; ok && !e.ReleaseCheckedAt.IsZero() {
				t.Errorf("ReleaseCheckedAt = %v, want unstamped after a failed fetch", e.ReleaseCheckedAt)
			}

			// The failure left nothing behind, so the next call retries.
			_, _ = SelfLatest()
			if rel, _ := hits.counts(); rel != 2 {
				t.Errorf("release requests = %d, want 2 (a failure must not suppress the retry)", rel)
			}
		})
	}
}

// TestSelfLatestPreservesEntry verifies the merge-on-write: the release-only
// pass must not wipe the card/README fields a tracked keepkit already has.
func TestSelfLatestPreservesEntry(t *testing.T) {
	apiServer(t, apiHandlers{release: releaseJSON("v0.8.0")})

	if d := GetRepoData("github.com/" + SelfRepo); d.About != "tracker" {
		t.Fatalf("setup GetRepoData: About = %q, want tracker", d.About)
	}
	if _, err := GetReadme("github.com/" + SelfRepo); err != nil {
		t.Fatalf("setup GetReadme: %v", err)
	}
	before := LoadCache()[SelfRepo]

	// Expire only the card timestamps so the self-check actually fetches.
	updateCacheEntry(SelfRepo, func(e CacheEntry) CacheEntry {
		e.CheckedAt = time.Now().Add(-2 * cacheTTL)
		return e
	})

	if _, err := SelfLatest(); err != nil {
		t.Fatalf("SelfLatest: %v", err)
	}

	entry := LoadCache()[SelfRepo]
	if entry.Readme != before.Readme || entry.Readme == "" {
		t.Errorf("Readme = %q, want it preserved (%q)", entry.Readme, before.Readme)
	}
	if !entry.ReadmeCheckedAt.Equal(before.ReadmeCheckedAt) {
		t.Errorf("ReadmeCheckedAt = %v, want unchanged %v", entry.ReadmeCheckedAt, before.ReadmeCheckedAt)
	}
	if entry.About != "tracker" || entry.Stars != 5 || len(entry.Languages) == 0 {
		t.Errorf("card fields wiped: About=%q Stars=%d Languages=%v", entry.About, entry.Stars, entry.Languages)
	}
	if !entry.CheckedAt.Before(time.Now().Add(-cacheTTL)) {
		t.Errorf("CheckedAt = %v, want left stale by a release-only pass", entry.CheckedAt)
	}
}
