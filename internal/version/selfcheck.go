package version

import (
	"errors"
	"time"
)

// SelfRepo is keepkit's own GitHub repository. The self-check does not depend on
// meta.yaml — an untracked keepkit is the feature's main case — so the ref is a
// constant rather than a tool field.
const SelfRepo = "stanlyzoolo/keepkit"

// SelfLatest returns keepkit's own latest release tag. It is a release-only pass:
// unlike GetRepoData it never touches /repos/{repo} or /languages, so a startup
// self-check costs one request per TTL window instead of three.
//
// The tag is served from cache.json when the entry is fresh, and freshness has
// its own timestamp (ReleaseCheckedAt) so this pass can never mark a repo card as
// fresh-but-blank — the same independence ReadmeCheckedAt buys the README. A
// tracked keepkit shares the cache entry with the full repo pass in both
// directions: a fresh full pass makes the self-check free, and a self-check never
// refreshes the card.
//
// An empty tag with a nil error means the answer is conclusively "no release
// published" (a 404 on /releases/latest). That is not a failure, and it is
// remembered for the TTL like any other answer (in CacheEntry.ReleaseMissing, not
// by wiping the tuple a tracked keepkit's card shows), so a repo without releases
// is not re-probed on every launch. A transient failure (rate limit, network,
// 5xx) returns the error and stamps nothing, so the next launch retries.
func SelfLatest() (string, error) {
	// SelfRepo is a constant already in the normalized "owner/repo" form, so this
	// cannot fail — the call names the shape of the cache key rather than parsing.
	repo := extractRepo(SelfRepo)

	if tag, ok := selfCachedTag(LoadCache()[repo]); ok {
		return tag, nil
	}

	info, err := fetchRelease(repo)
	if err != nil && !errors.Is(err, errNoReleases) {
		return "", err
	}
	missing := err != nil // only errNoReleases gets past the check above

	// Both remaining outcomes are conclusive and stamp ReleaseCheckedAt — and only
	// ReleaseCheckedAt, which is the whole point of the separate timestamp: a
	// release-only side pass must never mark the shared repo card as fresh. What
	// the fetch does to the entry itself (the tuple on success, ReleaseMissing
	// either way) is applyReleaseOutcome's single definition, shared with the two
	// passes that fetch a release alongside the card, so a 404 records the negative
	// without destroying content a tracked keepkit's card shows.
	updateCacheEntry(repo, func(existing CacheEntry) CacheEntry {
		e := applyReleaseOutcome(existing, info, err)
		e.ReleaseCheckedAt = time.Now()
		return e
	})
	// The flag, not the tuple, is the answer — exactly as on the cache-hit branch
	// above: a preserved tag belongs to the card, never to the update offer.
	if missing {
		return "", nil
	}
	return info.Tag, nil
}

// selfCachedTag answers the self-check from a cache entry, reporting whether the
// entry answers at all. Either timestamp counts, since both are stamped only by a
// conclusive pass, but they carry different guarantees: ReleaseCheckedAt is
// written by SelfLatest alone and is an answer on its own (the tag when there is
// one, the empty string when ReleaseMissing says there is not). CheckedAt is
// stamped by the two passes that fetch a release alongside the repo card
// (getRepoData and getChangelog), whose gates have no release-content check at
// all, so it counts only when one of them actually left an answer behind — a tag
// or the same recorded negative.
//
// All three release fetches maintain ReleaseMissing through applyReleaseOutcome,
// which is what lets the answer be read off the flag instead of the tuple: a tag
// preserved from a release that has since been deleted stays available to the
// card and is *not* offered as an update.
func selfCachedTag(e CacheEntry) (string, bool) {
	if time.Since(e.ReleaseCheckedAt) < cacheTTL {
		return selfTagOf(e), true
	}
	if time.Since(e.CheckedAt) < cacheTTL && (e.Latest != "" || e.ReleaseMissing) {
		return selfTagOf(e), true
	}
	return "", false
}

// selfTagOf is the entry's answer to "which release should the banner offer":
// nothing when the last conclusive fetch found no latest release, the cached tag
// otherwise.
func selfTagOf(e CacheEntry) string {
	if e.ReleaseMissing {
		return ""
	}
	return e.Latest
}
