package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stanlyzoolo/keeptui/internal/logx"
)

// TestMain redirects logx to a throwaway directory for the whole package test
// binary, so tests that exercise the logging error paths (classifyStatus, doGH,
// LoadCache/SaveCache, InstalledVersion) never write keeptui-*.log into the real
// user config dir. Individual tests that assert logger output still call
// logx.SetDirForTesting with their own temp dir; its restore reverts to this
// fallback (not the real dir), keeping every test off the real config.
//
// It also pins testBrewPrefix to an empty throwaway prefix, so no test ever
// consults the developer machine's real Homebrew tree (a real formula named
// like a test fixture would otherwise leak a version into InstalledVersion
// results). Brew-specific tests install their own layout via makeBrewLayout.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "keeptui-version-logs")
	if err != nil {
		panic(err)
	}
	restore := logx.SetDirForTesting(dir)
	brewDir, err := os.MkdirTemp("", "keeptui-version-brew")
	if err != nil {
		panic(err)
	}
	testBrewPrefix = brewDir
	// Same blanket protection for the two files this package writes: cache.json
	// and the token. Per-test overrides nest inside it and restore back to it.
	cfgDir, err := os.MkdirTemp("", "keeptui-version-config")
	if err != nil {
		panic(err)
	}
	restoreCfg := SetConfigDirForTesting(cfgDir)
	code := m.Run()
	restoreCfg()
	restore()
	_ = os.RemoveAll(dir)
	_ = os.RemoveAll(brewDir)
	_ = os.RemoveAll(cfgDir)
	os.Exit(code)
}

// apiHandlers lets a test drive the endpoints it is about and leave the rest to
// apiServer's canned answers. A nil field keeps the canned handler, so a test
// names only the endpoint it is testing.
type apiHandlers struct {
	release  http.HandlerFunc // /releases/latest
	readme   http.HandlerFunc // /readme
	repoInfo http.HandlerFunc // /repos/{owner}/{repo}
}

// apiHits counts requests per endpoint class, so a test can assert a call was
// served from cache.json rather than from the network. Tests that only need the
// server discard it.
type apiHits struct {
	mu       sync.Mutex
	release  int
	repoInfo int
}

func (h *apiHits) hit(counter *int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	*counter++
}

func (h *apiHits) counts() (release, repoInfo int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.release, h.repoInfo
}

// apiServer wires testAPIBase/testCacheDir to an httptest server that answers
// every GitHub endpoint this package fetches — caller-driven where the test says
// so, with plausible canned data everywhere else — for the duration of the test.
// It is the single fixture for both the README and the self-check suites: they
// differ only in which endpoint they drive, and duplicating the wiring is how the
// two copies drifted apart on the token reset.
func apiServer(t *testing.T, h apiHandlers) *apiHits {
	t.Helper()
	hits := &apiHits{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			hits.hit(&hits.release)
			if h.release != nil {
				h.release(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0", "body": "notes"})
		case strings.HasSuffix(r.URL.Path, "/readme"):
			if h.readme != nil {
				h.readme(w, r)
				return
			}
			_, _ = w.Write([]byte("# docs"))
		case strings.HasSuffix(r.URL.Path, "/languages"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]int{"Go": 100})
		default:
			hits.hit(&hits.repoInfo)
			if h.repoInfo != nil {
				h.repoInfo(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"archived": false, "description": "tracker", "stargazers_count": 5,
			})
		}
	}))
	origAPIBase, origCacheDir := testAPIBase, testCacheDir
	testAPIBase = srv.URL
	testCacheDir = t.TempDir()
	// Redirect the token too: without this the fetchers read the developer's real
	// ~/.config/keeptui/token and doGH sends it to this local server.
	t.Setenv("GITHUB_TOKEN", "")
	resetTokenState(t, t.TempDir())
	t.Cleanup(func() {
		srv.Close()
		testAPIBase, testCacheDir = origAPIBase, origCacheDir
	})
	return hits
}

// TestConfigDirIsolated fails if the package-wide isolation above is ever
// removed: without it a test that writes the cache or saves a token rewrites the
// real user config.
func TestConfigDirIsolated(t *testing.T) {
	cacheDir, tokenDir := ConfigDirOverrides()
	if cacheDir == "" || tokenDir == "" {
		t.Fatalf("cache/token dir overrides = %q/%q, want a temp dir — tests can reach the real config", cacheDir, tokenDir)
	}
}
