// Package configdir resolves keeptui's base user-config directory — the parent
// of the keeptui/ subdir that holds meta.yaml, cache.json, the token and logs.
// It is the bottom leaf of the import graph (stdlib only), so every package that
// owns one of those files can share the resolution without an import cycle.
//
// The resolution deliberately differs from os.UserConfigDir(): on macOS that
// returns ~/Library/Application Support, but keeptui keeps macOS and Linux on
// the same ~/.config/keeptui path (honoring $XDG_CONFIG_HOME). Windows still
// follows os.UserConfigDir() (%AppData%), where there is no ~/.config convention.
package configdir

import (
	"os"
	"path/filepath"
	"runtime"
)

// Base returns keeptui's base user-config directory. Callers append the
// "keeptui" subdir (and the file) themselves, matching what os.UserConfigDir()
// callers did before. See baseFor for the per-GOOS rules.
func Base() (string, error) {
	return baseFor(runtime.GOOS, os.Getenv, os.UserConfigDir, os.UserHomeDir)
}

// baseFor is the pure core behind Base, parameterized on GOOS and the env/dir
// lookups so both branches are table-testable (the shellCommand/planFor idiom).
// Windows → os.UserConfigDir() (%AppData%); macOS and Linux → $XDG_CONFIG_HOME
// if set, else ~/.config.
func baseFor(goos string, getenv func(string) string, userConfigDir, userHomeDir func() (string, error)) (string, error) {
	if goos == "windows" {
		return userConfigDir()
	}
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}
