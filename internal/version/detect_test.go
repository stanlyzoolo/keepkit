package version

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name      string
		installed string
		latest    string
		want      bool
	}{
		{"newer major", "1.2.3", "2.0.0", true},
		{"newer minor", "1.2.3", "1.3.0", true},
		{"newer patch", "1.2.3", "1.2.4", true},
		{"older major", "2.0.0", "1.9.9", false},
		{"older minor", "1.3.0", "1.2.9", false},
		{"older patch", "1.2.4", "1.2.3", false},
		{"equal", "1.2.3", "1.2.3", false},
		{"multi-digit segments", "0.9.9", "0.10.0", true},
		{"empty installed", "", "1.0.0", false},
		{"empty latest", "1.0.0", "", false},
		{"both empty", "", "", false},
		{"v-prefix installed", "v1.2.3", "1.2.4", true},
		{"v-prefix latest", "1.2.3", "v1.2.4", true},
		{"v-prefix both equal", "v1.2.3", "v1.2.3", false},
		{"release newer than its rc", "1.2.3-rc1", "1.2.3", true},
		{"rc not newer than release", "1.2.3", "1.2.3-rc1", false},
		{"rc ordering", "1.2.3-rc1", "1.2.3-rc2", true},
		{"build metadata ignored", "1.2.3+build7", "1.2.3+build9", false},
		{"CalVer zero-padded segments", "2024.01.15", "2024.02.01", true},
		{"CalVer equal after zero-strip", "2024.01.15", "2024.1.15", false},
		{"4th segment truncated: equal", "1.2.3.4", "1.2.3.5", false},
		{"4th segment truncated: patch decides", "1.2.3.9", "1.2.4.0", true},
		{"invalid installed", "abc", "1.2.3", false},
		{"invalid latest", "1.2.3", "abc", false},
		{"two segments", "1.2", "1.3", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNewer(tt.installed, tt.latest); got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.installed, tt.latest, got, tt.want)
			}
		})
	}
}

// TestDisplayVersion pins the one edit the card's version spelling is allowed
// to make: a "v" in front of a bare version number, and nothing else. The
// value itself must survive verbatim — canonSemver, which decides whether the
// string is a version at all, rewrites zero-padding, a 4th segment and build
// metadata, and displaying that would show a version no tool ever reported.
func TestDisplayVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare semver gains the v", "1.10.2", "v1.10.2"},
		{"v-prefixed left alone", "v1.10.2", "v1.10.2"},
		{"uppercase V left alone", "V1.10.2", "V1.10.2"},
		{"two segments", "1.2", "v1.2"},
		{"pre-release", "1.2.3-rc1", "v1.2.3-rc1"},
		{"build metadata kept, not dropped", "1.2.3+build7", "v1.2.3+build7"},
		{"CalVer keeps its zero-padding", "2024.01.15", "v2024.01.15"},
		{"4th segment kept, not truncated", "1.2.3.4", "v1.2.3.4"},
		{"surrounding space trimmed", "  1.2.3  ", "v1.2.3"},
		{"not a version number", "nightly", "nightly"},
		{"prefixed tag left alone", "cli-2.0.0", "cli-2.0.0"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DisplayVersion(tt.in); got != tt.want {
				t.Errorf("DisplayVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// writeFakeTool creates an executable shell script named `name` in dir.
func writeFakeTool(t *testing.T, dir, name, script string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledVersion(t *testing.T) {
	dir := t.TempDir()
	writeFakeTool(t, dir, "goodtool", `echo "goodtool version 1.2.3"`)
	writeFakeTool(t, dir, "flagvtool", `if [ "$1" = "-V" ]; then echo "2.0.1"; else exit 1; fi`)
	writeFakeTool(t, dir, "brokentool", `exit 1`)
	writeFakeTool(t, dir, "customtool", `if [ "$1" = "version" ]; then echo "v3.4.5"; else exit 1; fi`)
	t.Setenv("PATH", dir)

	tests := []struct {
		name        string
		tool        loader.Tool
		want        string
		wantPresent bool
	}{
		{"--version output", loader.Tool{Name: "goodtool"}, "1.2.3", true},
		{"-V fallback when --version fails", loader.Tool{Name: "flagvtool"}, "2.0.1", true},
		{"tool not on PATH", loader.Tool{Name: "missingtool"}, "", false},
		// Installed yet unresponsive: no version, but present — the card must
		// not call this "not installed".
		{"tool exits non-zero on all candidates", loader.Tool{Name: "brokentool"}, "", true},
		// VersionCmd is never populated from ToolMeta today; this pins the
		// unit contract of the override path, not a production flow.
		{"VersionCmd override", loader.Tool{Name: "customtool", VersionCmd: "customtool version"}, "v3.4.5", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, present := InstalledVersion(tt.tool)
			if got != tt.want {
				t.Errorf("InstalledVersion(%+v) = %q, want %q", tt.tool, got, tt.want)
			}
			if present != tt.wantPresent {
				t.Errorf("InstalledVersion(%+v) present = %v, want %v", tt.tool, present, tt.wantPresent)
			}
		})
	}
}

func TestCargoVersionFromList(t *testing.T) {
	// The real shape of `cargo install --list`: crate header at column 0,
	// its binaries indented below it.
	const list = `ripgrep v14.1.0:
    rg
inertia-tui v0.1.0:
    inertia
multi-bin v2.3.4:
    alpha
    beta
pathinstall v0.9.0 (/Users/me/src/pathinstall):
    pathy
noversion:
    orphan
`

	tests := []struct {
		name    string
		list    string
		binName string
		want    string
	}{
		{"first crate", list, "rg", "14.1.0"},
		// The motivating case: the binary name differs from the crate name and
		// sits in a later block.
		{"binary in a later block, name differs from crate", list, "inertia", "0.1.0"},
		{"second binary of a multi-binary crate", list, "beta", "2.3.4"},
		// A path/git install carries a source suffix after the version.
		{"header with a source suffix", list, "pathy", "0.9.0"},
		{"header with no parseable version", list, "orphan", ""},
		{"binary not in the list", list, "missing", ""},
		{"crate name is not a binary name", list, "inertia-tui", ""},
		{"empty list", "", "rg", ""},
		{"empty binary name", list, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cargoVersionFromList(tt.list, tt.binName); got != tt.want {
				t.Errorf("cargoVersionFromList(_, %q) = %q, want %q", tt.binName, got, tt.want)
			}
		})
	}
}

func TestIsTUITakeover(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want bool
	}{
		{"alt-screen enter", "\x1b[?1049h frames", true},
		{"alt-screen leave", "boom\x1b[?1049l", true},
		{"plain help text", "usage: tool [--version]", false},
		{"plain SGR color is not a takeover", "\x1b[31mred\x1b[0m", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTUITakeover([]byte(tt.out)); got != tt.want {
				t.Errorf("isTUITakeover(%q) = %v, want %v", tt.out, got, tt.want)
			}
		})
	}
}

func TestInstalledVersionTUITakeover(t *testing.T) {
	dir := t.TempDir()
	// A ratatui app probed with --version: it ignores the flag, boots its TUI
	// (alt-screen), then panics because DetachTTY left it no terminal. The
	// capture carries a dependency version (ratatui-0.30.2) that must never be
	// mistaken for the tool's own.
	writeFakeTool(t, dir, "tuitool", `printf '\033[?1049h\033[2J'; echo "panicked at ratatui-0.30.2/src/lib.rs"; exit 101`)
	t.Setenv("PATH", dir)

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	got, present := InstalledVersion(loader.Tool{Name: "tuitool"})
	if got != "" {
		t.Errorf("a TUI takeover must not yield a version, got %q", got)
	}
	if !present {
		t.Error("a TUI app that boots is installed: present should be true")
	}
	// A TUI app answering this way is classified behavior, not an anomaly —
	// logging it would re-create a session log on every startup.
	if out := logx.ReadAllForTesting(logDir); out != "" {
		t.Errorf("a TUI takeover must not log, got:\n%s", out)
	}
}

// TestInstalledVersionCargoFallback covers the wiring, not the parser
// (TestCargoVersionFromList owns that): a binary that won't name its version
// still resolves through `cargo install --list`, under the binary name rather
// than the crate name. TestMain's brew prefix is an empty directory, so the
// brew fallback misses and the cargo one is what answers.
func TestInstalledVersionCargoFallback(t *testing.T) {
	dir := t.TempDir()
	// The motivating tool: a ratatui app that ignores --version, boots its TUI
	// and dies without a terminal.
	writeFakeTool(t, dir, "inertia", `printf '\033[?1049h'; echo "panicked at ratatui-0.30.2"; exit 101`)
	// cargo knows the version without running the binary at all. Note the crate
	// name differs from the binary name — the whole point of the list lookup.
	writeFakeTool(t, dir, "cargo", `echo "inertia-tui v0.1.0:"; echo "    inertia"`)
	t.Setenv("PATH", dir)

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	got, present := InstalledVersion(loader.Tool{Name: "inertia"})
	if got != "0.1.0" {
		t.Errorf("InstalledVersion via cargo = %q, want %q", got, "0.1.0")
	}
	if !present {
		t.Error("a cargo-installed binary on PATH must report present")
	}
	// A fallback hit is this path's normal state, like the brew one.
	if out := logx.ReadAllForTesting(logDir); out != "" {
		t.Errorf("a successful cargo fallback must not log, got:\n%s", out)
	}
}

// TestInstalledVersionBrewBeatsCargo pins the source order. Both fallbacks can
// answer for this tool and they disagree; brew is consulted first, so a tool
// installed by brew is never reported with a stale crate version left behind by
// an older cargo install.
func TestInstalledVersionBrewBeatsCargo(t *testing.T) {
	binDir := t.TempDir()
	writeFakeTool(t, binDir, "dualtool", `exit 1`)
	writeFakeTool(t, binDir, "cargo", `echo "dualtool v9.9.9:"; echo "    dualtool"`)
	t.Setenv("PATH", binDir)
	makeBrewLayout(t, map[string][]string{"Cellar/dualtool": {"1.2.3"}})

	if got, present := InstalledVersion(loader.Tool{Name: "dualtool"}); got != "1.2.3" || !present {
		t.Errorf("InstalledVersion = %q (present=%v), want %q from the brew layout", got, present, "1.2.3")
	}
}

// TestInstalledVersionCargoGatedOnPresence pins the binaryExists gate: the
// cargo list is keyed by binary name, so for a tool that isn't installed it can
// only miss — and spawning a subprocess per absent tool on every startup is the
// cost the gate exists to avoid. The fake cargo would happily answer, so a
// leaked gate fails twice: the marker appears and a version comes back.
func TestInstalledVersionCargoGatedOnPresence(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "cargo-ran")
	// The marker is written with a shell redirection, not `touch`: PATH is the
	// fixture directory alone, so any external command the fake calls would
	// silently fail to resolve and leave this assertion dead.
	writeFakeTool(t, dir, "cargo", `: > "`+marker+`"; echo "ghost-crate v1.0.0:"; echo "    ghosttool"`)
	t.Setenv("PATH", dir)

	got, present := InstalledVersion(loader.Tool{Name: "ghosttool"})
	if got != "" {
		t.Errorf("InstalledVersion = %q, want empty for a tool that is not installed", got)
	}
	if present {
		t.Error("a tool absent from PATH must not report present")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("cargo install --list ran for a tool that is not installed — the binaryExists gate leaked")
	}
}

func TestInstalledVersionMissingBinaryNoLog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	// A tool that is simply not on PATH is the normal "not installed" state,
	// not a malfunction — it must not create a session log.
	got, present := InstalledVersion(loader.Tool{Name: "missingtool"})
	if got != "" {
		t.Fatalf("expected empty version, got %q", got)
	}
	if present {
		t.Error("a not-on-PATH tool must not report present")
	}
	if out := logx.ReadAllForTesting(logDir); out != "" {
		t.Errorf("a not-on-PATH tool must not log, got:\n%s", out)
	}
}

func TestInstalledVersionPresentButBrokenLogs(t *testing.T) {
	dir := t.TempDir()
	// Binary exists on PATH but exits non-zero for every candidate — installed
	// yet unresponsive, a genuine anomaly worth one log line.
	writeFakeTool(t, dir, "brokentool", `exit 1`)
	t.Setenv("PATH", dir)

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	got, present := InstalledVersion(loader.Tool{Name: "brokentool"})
	if got != "" {
		t.Fatalf("expected empty version, got %q", got)
	}
	if !present {
		t.Error("a binary on PATH is installed even when it won't name its version")
	}
	out := logx.ReadAllForTesting(logDir)
	// Exactly one log line despite two candidates (--version and -V).
	if lines := strings.Count(out, "version.InstalledVersion"); lines != 1 {
		t.Fatalf("expected exactly one log line, got %d:\n%s", lines, out)
	}
	if !strings.Contains(out, "brokentool") {
		t.Errorf("log should name the tool, got:\n%s", out)
	}
}

func TestInstalledVersionLoggingFallbackNoLog(t *testing.T) {
	dir := t.TempDir()
	writeFakeTool(t, dir, "flagvtool", `if [ "$1" = "-V" ]; then echo "2.0.1"; else exit 1; fi`)
	t.Setenv("PATH", dir)

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	if got, _ := InstalledVersion(loader.Tool{Name: "flagvtool"}); got != "2.0.1" {
		t.Fatalf("expected 2.0.1 via -V fallback, got %q", got)
	}
	if out := logx.ReadAllForTesting(logDir); out != "" {
		t.Errorf("a successful -V fallback must not log, got:\n%s", out)
	}
}

func TestInstalledVersionLoggingSuccessNoLog(t *testing.T) {
	dir := t.TempDir()
	writeFakeTool(t, dir, "goodtool", `echo "goodtool version 1.2.3"`)
	t.Setenv("PATH", dir)

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	if got, _ := InstalledVersion(loader.Tool{Name: "goodtool"}); got != "1.2.3" {
		t.Fatalf("expected 1.2.3, got %q", got)
	}
	if out := logx.ReadAllForTesting(logDir); out != "" {
		t.Errorf("a first-candidate success must not log, got:\n%s", out)
	}
}
