package model

import (
	"os"
	"testing"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
	"github.com/stanlyzoolo/keepkit/internal/version"
)

// TestMain redirects logx to a throwaway directory for the whole package test
// binary, so tests that exercise the logging paths (fetchHelpCmd, safeCmd
// panics) never write keepkit-*.log into the real user config dir. Individual
// tests that assert logger output still call logx.SetDirForTesting with their
// own temp dir; its restore reverts to this fallback (not the real dir).
//
// The same blanket treatment covers every file the TUI can write, because this
// package reaches all of them: the tags/track/rename/untrack/status handlers
// call loader.SaveMeta (which rewrites meta.yaml wholesale), the [L] overlay
// reaches version.SetToken, and any fetch path writes cache.json. Relying on
// each test to remember its own temp HOME is what let an ad-hoc probe overwrite
// a real tracker; TestConfigDirIsolated pins that this never regresses.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "keepkit-model-logs")
	if err != nil {
		panic(err)
	}
	restore := logx.SetDirForTesting(dir)
	cfgDir, err := os.MkdirTemp("", "keepkit-model-config")
	if err != nil {
		panic(err)
	}
	restoreMeta := loader.SetConfigDirForTesting(cfgDir)
	restoreVersion := version.SetConfigDirForTesting(cfgDir)
	code := m.Run()
	restoreVersion()
	restoreMeta()
	restore()
	_ = os.RemoveAll(dir)
	_ = os.RemoveAll(cfgDir)
	os.Exit(code)
}

// TestConfigDirIsolated fails if the package-wide isolation above is ever
// removed. It is the guard for a real incident: an ad-hoc probe test drove the
// model into loader.SaveMeta with no override and overwrote a developer's
// actual meta.yaml.
func TestConfigDirIsolated(t *testing.T) {
	if loader.ConfigDirOverride() == "" {
		t.Error("loader config override is empty — tests can write the real meta.yaml")
	}
	cacheDir, tokenDir := version.ConfigDirOverrides()
	if cacheDir == "" || tokenDir == "" {
		t.Errorf("version overrides = %q/%q — tests can write the real cache/token", cacheDir, tokenDir)
	}
}
