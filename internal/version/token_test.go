package version

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stanlyzoolo/keepkit/internal/logx"
)

// resetTokenState clears the in-memory token and the sync.Once so each test
// starts from a clean slate and re-reads the (test-overridden) token file.
//
// rejectedToken is cleared on both ends, and that is not symmetry for its own
// sake: Go runs a package's tests in one process, so a test that left a
// rejection standing would strip Authorization from every later test in the
// binary — deterministic contamination, not a flake.
func resetTokenState(t *testing.T, dir string) {
	t.Helper()
	origDir := testTokenDir
	testTokenDir = dir
	tokenMu.Lock()
	tokenMem = ""
	rejectedToken = ""
	tokenMu.Unlock()
	loadTokenOnce = sync.Once{}
	t.Cleanup(func() {
		testTokenDir = origDir
		tokenMu.Lock()
		tokenMem = ""
		rejectedToken = ""
		tokenMu.Unlock()
		loadTokenOnce = sync.Once{}
	})
}

func TestResolveTokenEnvOverConfig(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("config-token"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "env-token")

	if got := resolveToken(); got != "env-token" {
		t.Errorf("resolveToken() = %q, want env-token", got)
	}
	if got := TokenSource(); got != "env" {
		t.Errorf("TokenSource() = %q, want env", got)
	}
}

func TestResolveTokenFromConfig(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("  config-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "")

	if got := resolveToken(); got != "config-token" {
		t.Errorf("resolveToken() = %q, want config-token (trimmed)", got)
	}
	if got := TokenSource(); got != "config" {
		t.Errorf("TokenSource() = %q, want config", got)
	}
}

func TestResolveTokenEmpty(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	if got := resolveToken(); got != "" {
		t.Errorf("resolveToken() = %q, want empty", got)
	}
	if got := TokenSource(); got != "none" {
		t.Errorf("TokenSource() = %q, want none", got)
	}
}

func TestSetTokenWritesFile0600(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	if err := SetToken("my-token"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("token file mode = %v, want 0600", perm)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "my-token" {
		t.Errorf("token file content = %q, want my-token", string(data))
	}
	if got := resolveToken(); got != "my-token" {
		t.Errorf("resolveToken() after SetToken = %q, want my-token", got)
	}
	if got := TokenSource(); got != "config" {
		t.Errorf("TokenSource() after SetToken = %q, want config", got)
	}
}

func TestSetTokenCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	if err := SetToken("tok"); err != nil {
		t.Fatalf("SetToken should MkdirAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "token")); err != nil {
		t.Errorf("token file not created: %v", err)
	}
}

func TestClearToken(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	if err := SetToken("tok"); err != nil {
		t.Fatal(err)
	}
	if err := ClearToken(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "token")); !os.IsNotExist(err) {
		t.Errorf("token file still present after ClearToken: %v", err)
	}
	if got := resolveToken(); got != "" {
		t.Errorf("resolveToken() after ClearToken = %q, want empty", got)
	}
	if got := TokenSource(); got != "none" {
		t.Errorf("TokenSource() after ClearToken = %q, want none", got)
	}
}

func TestClearTokenNoFile(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	if err := ClearToken(); err != nil {
		t.Errorf("ClearToken on missing file should be nil, got %v", err)
	}
}

// TestRejectTokenSuppressesOnlyTheRequestPath pins the split the [a] overlay
// depends on: the suppression reaches what goes on the wire and nothing else. A
// rejected token must keep its source and its value, or the overlay loses the
// mask the user identifies the dead credential by — in exactly the state the
// overlay exists to describe.
func TestRejectTokenSuppressesOnlyTheRequestPath(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	if err := SetToken("ghp_deadbeef"); err != nil {
		t.Fatal(err)
	}
	rejectToken("ghp_deadbeef")

	if got := resolveToken(); got != "" {
		t.Errorf("resolveToken() = %q, want empty — a rejected token must not go on the wire", got)
	}
	if got := Token(); got != "ghp_deadbeef" {
		t.Errorf("Token() = %q, want the value kept for the overlay's mask", got)
	}
	if got := TokenSource(); got != "config" {
		t.Errorf("TokenSource() = %q, want config", got)
	}
	if !TokenRejected() {
		t.Error("TokenRejected() = false after rejecting the token in effect")
	}
}

// TestRejectTokenNewTokenResolvesAgain verifies the value-not-bool choice pays
// off: entering a different token needs no clearing code anywhere, it simply
// stops matching the rejected value.
func TestRejectTokenNewTokenResolvesAgain(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	if err := SetToken("ghp_dead"); err != nil {
		t.Fatal(err)
	}
	rejectToken("ghp_dead")
	if err := SetToken("ghp_fresh"); err != nil {
		t.Fatal(err)
	}

	if got := resolveToken(); got != "ghp_fresh" {
		t.Errorf("resolveToken() = %q, want ghp_fresh — a new token is not the rejected one", got)
	}
	if TokenRejected() {
		t.Error("TokenRejected() = true after a different token was stored")
	}
}

// TestRejectTokenClearLeavesNothingResolvable covers the other exit: removing
// the token leaves nothing to send and nothing to call degraded, since an absent
// token is not a refused one.
func TestRejectTokenClearLeavesNothingResolvable(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	if err := SetToken("ghp_dead"); err != nil {
		t.Fatal(err)
	}
	rejectToken("ghp_dead")
	if err := ClearToken(); err != nil {
		t.Fatal(err)
	}

	if got := resolveToken(); got != "" {
		t.Errorf("resolveToken() = %q, want empty after ClearToken", got)
	}
	if TokenRejected() {
		t.Error("TokenRejected() = true with no token configured")
	}
}

// TestTokenRejectedWithNoTokenConfigured is the degenerate case the
// rejectedToken != "" guard exists for: without it "" == "" and a user who
// never set a token would be shown the whole degraded UI for a state that is
// not degraded at all.
func TestTokenRejectedWithNoTokenConfigured(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	if TokenRejected() {
		t.Error("TokenRejected() = true with no token ever configured")
	}
	// rejectToken("") is a no-op, so an empty rejection cannot arm the state
	// either — the guard is not the only thing holding this line.
	rejectToken("")
	if TokenRejected() {
		t.Error("TokenRejected() = true after rejectToken(\"\")")
	}
}

// TestTokenRejectedEnvToken covers the case keepkit cannot fix by writing a
// file: a bad GITHUB_TOKEN cannot be unset from the environment, and the same
// value comparison is what suppresses it.
func TestTokenRejectedEnvToken(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "ghp_env_dead")

	rejectToken("ghp_env_dead")

	if got := resolveToken(); got != "" {
		t.Errorf("resolveToken() = %q, want empty for a rejected env token", got)
	}
	if got := TokenSource(); got != "env" {
		t.Errorf("TokenSource() = %q, want env", got)
	}
	if !TokenRejected() {
		t.Error("TokenRejected() = false for a rejected env token")
	}
}

// TestRejectTokenLogsOnceForTheSameValue pins the transition-only log line. A
// cold start rejects in parallel once per tracked tool, so a line per call would
// bury the session log under identical entries and destroy the "a log file means
// something went wrong" signal.
func TestRejectTokenLogsOnceForTheSameValue(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	rejectToken("ghp_dead")
	rejectToken("ghp_dead")
	rejectToken("ghp_dead")

	out := logx.ReadAllForTesting(logDir)
	if n := strings.Count(out, "version.rejectToken"); n != 1 {
		t.Errorf("rejectToken logged %d times for one value, want 1:\n%s", n, out)
	}
	if strings.Contains(out, "ghp_dead") {
		t.Errorf("the rejected token leaked into the log:\n%s", out)
	}
}

// TestRejectedTokenReenteredStaysSuppressed covers the loop a frustrated user
// closes: paste the same expired token again. Validation goes through
// FetchRateWithToken, which bypasses doGH precisely so it observes the 401
// instead of surviving it, so nothing is persisted — and the value comparison
// keeps the session unauthenticated either way.
func TestRejectedTokenReenteredStaysSuppressed(t *testing.T) {
	dir := t.TempDir()
	resetTokenState(t, dir)
	t.Setenv("GITHUB_TOKEN", "")

	const dead = "ghp_expired_value"
	if err := SetToken(dead); err != nil {
		t.Fatal(err)
	}
	rejectToken(dead)

	// Re-entering the same value: SetToken is what the accept path would call,
	// and even reached directly it cannot un-reject the credential.
	if err := SetToken(dead); err != nil {
		t.Fatal(err)
	}
	if got := resolveToken(); got != "" {
		t.Errorf("resolveToken() = %q, want empty — the same dead value is still dead", got)
	}
	if !TokenRejected() {
		t.Error("TokenRejected() = false after re-entering the rejected value")
	}
}
