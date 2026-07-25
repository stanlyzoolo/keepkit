package loader

import (
	"os"
	"testing"

	"github.com/stanlyzoolo/keepkit/internal/logx"
)

// TestMain redirects logx to a throwaway directory for the whole package test
// binary, so tests that exercise SaveMeta's error paths never write
// keepkit-*.log into the real user config dir. Individual tests that assert
// logger output still call logx.SetDirForTesting with their own temp dir; its
// restore reverts to this fallback (not the real dir).
//
// It does the same for the tracker itself: SaveMeta rewrites meta.yaml
// wholesale, so a test that reaches it without an override would destroy a real
// user's tool list. Per-test helpers (useTempConfigDir) nest inside this one and
// restore back to it, never to the real directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "keepkit-loader-logs")
	if err != nil {
		panic(err)
	}
	restore := logx.SetDirForTesting(dir)
	cfgDir, err := os.MkdirTemp("", "keepkit-loader-config")
	if err != nil {
		panic(err)
	}
	restoreCfg := SetConfigDirForTesting(cfgDir)
	code := m.Run()
	restoreCfg()
	restore()
	_ = os.RemoveAll(dir)
	_ = os.RemoveAll(cfgDir)
	os.Exit(code)
}

// TestConfigDirIsolated fails if the package-wide isolation above is ever
// removed. It is the guard for a real incident: an ad-hoc probe test called
// SaveMeta with no override and overwrote a developer's actual meta.yaml.
func TestConfigDirIsolated(t *testing.T) {
	if ConfigDirOverride() == "" {
		t.Fatal("config dir override is empty — tests can write the real meta.yaml")
	}
}
