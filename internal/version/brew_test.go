package version

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
)

// makeBrewLayout builds <prefix>/<room>/<tool>/<version-dir> entries in a
// temp brew prefix and points testBrewPrefix at it, restoring the previous
// seam value (TestMain's package-wide empty prefix) on cleanup.
func makeBrewLayout(t *testing.T, dirs map[string][]string) string {
	t.Helper()
	prefix := t.TempDir()
	for tool, versions := range dirs {
		for _, v := range versions {
			if err := os.MkdirAll(filepath.Join(prefix, tool, v), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	prev := testBrewPrefix
	testBrewPrefix = prefix
	t.Cleanup(func() { testBrewPrefix = prev })
	return prefix
}

func TestBrewDirVersion(t *testing.T) {
	prefix := makeBrewLayout(t, map[string][]string{
		"Caskroom/agterm":     {"0.15.1"},
		"Caskroom/buildcask":  {"1.2.3,45678"},
		"Cellar/multitool":    {"1.0.0", "1.2.0"},
		"Cellar/revtool":      {"9.0.1_1"},
		"Cellar/revrace":      {"9.0.1_2", "9.0.2"},
		"Caskroom/latesttool": {"latest"},
		"Caskroom/dottool":    {".metadata", "2.0.0"},
		"Caskroom/bothtool":   {"3.0.0"},
		"Cellar/bothtool":     {"1.0.0"},
		"Caskroom/calver":     {"2024.01.15"},
		"Caskroom/traildot":   {"1.2.3."},
	})
	// A version-named plain file must be skipped (only directories count).
	if err := os.MkdirAll(filepath.Join(prefix, "Caskroom", "filetool"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prefix, "Caskroom", "filetool", "0.1.0"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		tool string
		want string
	}{
		{"cask simple version", "agterm", "0.15.1"},
		{"cask build suffix cut", "buildcask", "1.2.3"},
		{"cellar picks newest of several", "multitool", "1.2.0"},
		{"brew revision suffix cut", "revtool", "9.0.1"},
		{"revision competes against release", "revrace", "9.0.2"},
		{"latest-pinned cask yields nothing", "latesttool", ""},
		{"dot-entries skipped", "dottool", "2.0.0"},
		{"Caskroom checked before Cellar", "bothtool", "3.0.0"},
		{"CalVer dir name kept as-is", "calver", "2024.01.15"},
		{"trailing dot trimmed", "traildot", "1.2.3"},
		{"version-named file skipped", "filetool", ""},
		{"tool not brew-managed", "missingtool", ""},
		{"empty name", "", ""},
		{"path separator rejected", "../Caskroom/agterm", ""},
		{"backslash rejected", `evil\name`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := brewDirVersion(tt.tool); got != tt.want {
				t.Errorf("brewDirVersion(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestBrewDirVersionNoBrew(t *testing.T) {
	prev := testBrewPrefix
	testBrewPrefix = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { testBrewPrefix = prev })

	if got := brewDirVersion("agterm"); got != "" {
		t.Errorf("nonexistent prefix must yield no version, got %q", got)
	}
}

func TestBrewPrefixFromEnv(t *testing.T) {
	prefix := t.TempDir()
	if err := os.MkdirAll(filepath.Join(prefix, "Caskroom", "envtool", "3.1.0"), 0755); err != nil {
		t.Fatal(err)
	}
	prev := testBrewPrefix
	testBrewPrefix = ""
	t.Cleanup(func() { testBrewPrefix = prev })
	t.Setenv("HOMEBREW_PREFIX", prefix)

	if got := brewDirVersion("envtool"); got != "3.1.0" {
		t.Errorf("brewDirVersion via HOMEBREW_PREFIX = %q, want %q", got, "3.1.0")
	}

	// An env prefix pointing nowhere degrades to "not brew-managed", not an
	// error.
	t.Setenv("HOMEBREW_PREFIX", filepath.Join(prefix, "gone"))
	if got := brewDirVersion("envtool"); got != "" {
		t.Errorf("dangling HOMEBREW_PREFIX must yield no version, got %q", got)
	}
}

func TestBrewPrefixStandardPaths(t *testing.T) {
	prev := testBrewPrefix
	testBrewPrefix = ""
	t.Cleanup(func() { testBrewPrefix = prev })
	t.Setenv("HOMEBREW_PREFIX", "")

	// Machine-independent contract of the standard-locations scan: either no
	// prefix is found, or the found one is an existing directory.
	if p := brewPrefix(); p != "" {
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			t.Errorf("brewPrefix() = %q, which is not an existing directory", p)
		}
	}
}

func TestInstalledVersionBrewFallback(t *testing.T) {
	binDir := t.TempDir()
	// agterm exists on PATH but boots an app instead of answering --version —
	// the motivating case for the brew fallback.
	writeFakeTool(t, binDir, "agterm", `exit 1`)
	// A responsive CLI must win over the brew directory: the binary's own
	// answer is the ground truth, the layout is only a fallback.
	writeFakeTool(t, binDir, "clitool", `echo "clitool 5.5.5"`)
	t.Setenv("PATH", binDir)
	makeBrewLayout(t, map[string][]string{
		"Caskroom/agterm":  {"0.15.1"},
		"Cellar/noclitool": {"14.1.0"},
		"Cellar/clitool":   {"5.0.0"},
	})

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	if got, present := InstalledVersion(loader.Tool{Name: "agterm"}); got != "0.15.1" || !present {
		t.Fatalf("expected brew fallback to find 0.15.1 (present), got %q (present=%v)", got, present)
	}
	// A cask with no PATH binary at all resolves purely from the brew layout —
	// and a version found there is itself proof the tool is installed.
	if got, present := InstalledVersion(loader.Tool{Name: "noclitool"}); got != "14.1.0" || !present {
		t.Fatalf("expected brew fallback to find 14.1.0 (present), got %q (present=%v)", got, present)
	}
	if got, present := InstalledVersion(loader.Tool{Name: "clitool"}); got != "5.5.5" || !present {
		t.Fatalf("expected CLI answer to win over brew dir, got %q (present=%v)", got, present)
	}
	// A fallback hit is this path's normal state, not a malfunction — the
	// failed --version/-V attempts must not create a session log.
	if out := logx.ReadAllForTesting(logDir); out != "" {
		t.Errorf("a successful brew fallback must not log, got:\n%s", out)
	}
}
