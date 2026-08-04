package version

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stanlyzoolo/keepkit/internal/logx"
)

// respWith builds a minimal *http.Response for classifyStatus tests, carrying a
// request so resp.Request.URL.Path is populated.
func respWith(t *testing.T, code int, remaining, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", "https://api.github.test"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	if remaining != "" {
		h.Set("X-RateLimit-Remaining", remaining)
	}
	return &http.Response{StatusCode: code, Header: h, Request: req}
}

func TestLoadCacheCorruptLogs(t *testing.T) {
	cacheDir := t.TempDir()
	origCacheDir := testCacheDir
	defer func() { testCacheDir = origCacheDir }()
	testCacheDir = cacheDir

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	// Write a corrupt cache.json.
	if err := os.WriteFile(filepath.Join(cacheDir, "cache.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := LoadCache()
	if len(c) != 0 {
		t.Errorf("expected empty cache on corrupt file, got %d entries", len(c))
	}
	out := logx.ReadAllForTesting(logDir)
	if !strings.Contains(out, "version.LoadCache") || !strings.Contains(out, "parse") {
		t.Errorf("expected a parse-error log line, got:\n%s", out)
	}
}

func TestClassifyStatusRateLimitLogs(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	err := classifyStatus(respWith(t, http.StatusForbidden, "0", "/repos/cli/cli"))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
	out := logx.ReadAllForTesting(logDir)
	if !strings.Contains(out, "http=403") || !strings.Contains(out, "remaining=0") {
		t.Errorf("log missing code/remaining, got:\n%s", out)
	}
	if !strings.Contains(out, "/repos/cli/cli") {
		t.Errorf("log missing request path, got:\n%s", out)
	}
	if !strings.Contains(out, "rate limited") {
		t.Errorf("log should mark rate limit, got:\n%s", out)
	}
}

func TestClassifyStatusGenericForbiddenLogs(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	err := classifyStatus(respWith(t, http.StatusForbidden, "42", "/repos/cli/cli"))
	if errors.Is(err, ErrRateLimited) {
		t.Fatalf("403 with remaining>0 must not be ErrRateLimited, got %v", err)
	}
	out := logx.ReadAllForTesting(logDir)
	if !strings.Contains(out, "http=403") || !strings.Contains(out, "remaining=42") {
		t.Errorf("log missing code/remaining, got:\n%s", out)
	}
	if strings.Contains(out, "rate limited") {
		t.Errorf("generic 403 should not be labelled rate limited, got:\n%s", out)
	}
}

// TestClassifyStatusUnauthorizedLogs verifies a 401 writes a line naming the
// code and the path — it is neither conclusive like a 404 nor normal, and it is
// the one status the user can fix — while a 404 stays silent, so a stale repo
// ref does not re-create a session log on every launch.
func TestClassifyStatusUnauthorizedLogs(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	err := classifyStatus(respWith(t, http.StatusUnauthorized, "4999", "/repos/cli/cli"))
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid, got %v", err)
	}
	out := logx.ReadAllForTesting(logDir)
	if !strings.Contains(out, "http=401") || !strings.Contains(out, "/repos/cli/cli") {
		t.Errorf("log missing code/path, got:\n%s", out)
	}
	if strings.Contains(out, "rate limited") {
		t.Errorf("a 401 must not be labelled rate limited, got:\n%s", out)
	}
}

func TestClassifyStatusNotFoundStaysSilent(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	err := classifyStatus(respWith(t, http.StatusNotFound, "4999", "/repos/cli/cli"))
	if err == nil || errors.Is(err, ErrTokenInvalid) || errors.Is(err, ErrRateLimited) {
		t.Fatalf("classifyStatus(404) = %v, want a generic error", err)
	}
	if out := logx.ReadAllForTesting(logDir); out != "" {
		t.Errorf("a 404 must leave no log line, got:\n%s", out)
	}
}

func TestDoGHNeverLogsToken(t *testing.T) {
	// Clear env precedence and route the config token through the seam.
	t.Setenv("GITHUB_TOKEN", "")
	tokenDir := t.TempDir()
	origTokenDir := testTokenDir
	defer func() { testTokenDir = origTokenDir }()
	testTokenDir = tokenDir

	const secret = "ghp_supersecrettoken123"
	if err := SetToken(secret); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ClearToken() }()

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/repos/cli/cli/releases/latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := doGH(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_ = classifyStatus(resp)

	out := logx.ReadAllForTesting(logDir)
	if strings.Contains(out, secret) {
		t.Errorf("log must never contain the token, got:\n%s", out)
	}
}

func TestLoadCacheMissingNoLog(t *testing.T) {
	cacheDir := t.TempDir()
	origCacheDir := testCacheDir
	defer func() { testCacheDir = origCacheDir }()
	testCacheDir = cacheDir

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	// No cache.json exists.
	c := LoadCache()
	if len(c) != 0 {
		t.Errorf("expected empty cache, got %d entries", len(c))
	}
	if out := logx.ReadAllForTesting(logDir); out != "" {
		t.Errorf("expected no log for a missing cache file, got:\n%s", out)
	}
}
