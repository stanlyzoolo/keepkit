package version

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stanlyzoolo/keeptui/internal/configdir"
)

// testTokenDir overrides the token file directory in tests.
var testTokenDir string

// token state: value from the config file or TUI entry (never the env token).
var (
	tokenMu       sync.RWMutex
	tokenMem      string
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
	return filepath.Join(base, "keeptui", "token"), nil
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

// resolveToken returns the effective GitHub token: the GITHUB_TOKEN env var
// takes precedence, otherwise the config-file token (lazily loaded once).
func resolveToken() string {
	if env := os.Getenv("GITHUB_TOKEN"); env != "" {
		return env
	}
	loadTokenOnce.Do(loadTokenFromFile)
	tokenMu.RLock()
	defer tokenMu.RUnlock()
	return tokenMem
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

// Token returns the effective GitHub token (env precedence, else config file),
// or "" when none is set. Used by the UI to render a masked preview.
func Token() string {
	return resolveToken()
}

// TokenSource reports where the effective token comes from: "env", "config",
// or "none".
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
