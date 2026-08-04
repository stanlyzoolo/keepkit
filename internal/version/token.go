package version

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stanlyzoolo/keepkit/internal/configdir"
	"github.com/stanlyzoolo/keepkit/internal/logx"
)

// testTokenDir overrides the token file directory in tests.
var testTokenDir string

// token state: value from the config file or TUI entry (never the env token).
//
// rejectedToken holds the credential GitHub answered 401 to, stored as the
// value rather than as a bool. That is what makes the state need no lifecycle:
// a newly entered token simply differs from it and resolves again, a cleared
// token is empty and compares against the "" guard, and a bad GITHUB_TOKEN —
// which keepkit cannot unset — is suppressed by the same comparison for as long
// as it is the effective token. A bool would have to be cleared from SetToken,
// ClearToken and the env path, and one missed site is a session that keeps
// sending a token it knows is dead or stops sending a good one.
var (
	tokenMu       sync.RWMutex
	tokenMem      string
	rejectedToken string
	loadTokenOnce sync.Once
)

// tokenFilePath returns the path to the persisted token file.
func tokenFilePath() (string, error) {
	if testTokenDir != "" {
		return filepath.Join(testTokenDir, "token"), nil
	}
	base, err := configdir.Base()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "keepkit", "token"), nil
}

// loadTokenFromFile reads the token file into tokenMem exactly once. Any error
// (missing file, unreadable) leaves tokenMem empty. All tokenMem access goes
// through tokenMu so concurrent resolveToken() calls stay -race clean.
func loadTokenFromFile() {
	path, err := tokenFilePath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	tokenMu.Lock()
	tokenMem = strings.TrimSpace(string(data))
	tokenMu.Unlock()
}

// effectiveToken returns the configured GitHub token: the GITHUB_TOKEN env var
// takes precedence, otherwise the config-file token (lazily loaded once). This
// is the raw core — it answers what the user configured, regardless of whether
// GitHub has since refused it, which is exactly what the [a] overlay has to
// print. Only resolveToken applies the suppression.
func effectiveToken() string {
	if env := os.Getenv("GITHUB_TOKEN"); env != "" {
		return env
	}
	loadTokenOnce.Do(loadTokenFromFile)
	tokenMu.RLock()
	defer tokenMu.RUnlock()
	return tokenMem
}

// resolveToken returns the token to send on a request, or "" when the effective
// one has been rejected. doGH is its only caller, and deliberately so: the
// suppression is about what we put on the wire, not about what the user
// configured. An accessor that hid a rejected token from the UI would blank the
// source and the mask in precisely the state the overlay exists to explain.
func resolveToken() string {
	tok := effectiveToken()
	if tok == "" {
		return ""
	}
	tokenMu.RLock()
	defer tokenMu.RUnlock()
	if tok == rejectedToken {
		return ""
	}
	return tok
}

// rejectToken records the credential GitHub answered 401 to, so every later
// request goes out unauthenticated instead of repeating a call that cannot
// succeed. The token file is deliberately left alone: a rejection means the
// credential was refused, not that keepkit may destroy the user's data.
//
// The log line is written on the transition only. A cold start fires one pass
// per tracked tool in parallel, so a line per rejection would put a few dozen
// identical entries in a session log whose whole value is that a line means
// something happened.
func rejectToken(tok string) {
	if tok == "" {
		return
	}
	tokenMu.Lock()
	first := rejectedToken != tok
	rejectedToken = tok
	tokenMu.Unlock()
	if first {
		logx.Errorf("version.rejectToken: github rejected the %s token (HTTP 401); "+
			"requests continue unauthenticated for this session", TokenSource())
	}
}

// TokenRejected reports that the token currently in effect is the one GitHub
// refused. The rejectedToken != "" guard is load-bearing: without it a user with
// no token at all compares equal to the empty rejected value and gets the whole
// degraded UI for a state that is not degraded — unauthenticated by choice is
// not the same as unauthenticated by failure.
func TokenRejected() bool {
	tokenMu.RLock()
	rejected := rejectedToken
	tokenMu.RUnlock()
	return rejected != "" && effectiveToken() == rejected
}

// SetToken stores the token in memory and persists it to a 0600 file.
func SetToken(t string) error {
	path, err := tokenFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(t), 0600); err != nil {
		return err
	}
	// Mark the lazy load as done so a later resolveToken() does not overwrite
	// the value we just set with a stale file read.
	loadTokenOnce.Do(func() {})
	tokenMu.Lock()
	tokenMem = t
	tokenMu.Unlock()
	return nil
}

// ClearToken removes the persisted token file and the in-memory value.
func ClearToken() error {
	path, err := tokenFilePath()
	if err != nil {
		return err
	}
	loadTokenOnce.Do(func() {})
	tokenMu.Lock()
	tokenMem = ""
	tokenMu.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Token returns the configured GitHub token (env precedence, else config file),
// or "" when none is set. Used by the UI to render a masked preview, so it reads
// the raw core: a rejected token still has to show its source and its mask —
// that line is how the user recognises which credential to replace.
func Token() string {
	return effectiveToken()
}

// TokenSource reports where the configured token comes from: "env", "config",
// or "none". Like Token it reads the raw state and never the suppression — a
// rejected token still came from somewhere, and naming that place is half of
// what the overlay tells the user to go fix.
func TokenSource() string {
	if env := os.Getenv("GITHUB_TOKEN"); env != "" {
		return "env"
	}
	loadTokenOnce.Do(loadTokenFromFile)
	tokenMu.RLock()
	defer tokenMu.RUnlock()
	if tokenMem != "" {
		return "config"
	}
	return "none"
}
