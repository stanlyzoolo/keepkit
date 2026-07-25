package version

import (
	"os"
	"path/filepath"
	"strings"
)

// testBrewPrefix overrides the Homebrew prefix in tests.
var testBrewPrefix string

// brewPrefix resolves the Homebrew installation prefix without spawning brew
// (a ruby process that costs ~1.5s per invocation): the HOMEBREW_PREFIX env
// var brew's shellenv exports, else the first standard location that exists.
// Empty when brew is not installed (including Windows, where none exist).
func brewPrefix() string {
	if testBrewPrefix != "" {
		return testBrewPrefix
	}
	if p := os.Getenv("HOMEBREW_PREFIX"); p != "" {
		return p
	}
	for _, p := range []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

// brewDirVersion reads the installed version of a brew-managed tool from
// Homebrew's own directory layout, where the version is the name of the
// per-tool subdirectory (Caskroom/<name>/0.15.1, Cellar/<name>/14.1.0).
// This serves apps with no --version CLI (casks like GUI/terminal apps) at
// the cost of two ReadDir calls — no brew subprocess. Returns "" when the
// tool is not brew-managed or its keepkit name differs from the formula/cask
// name.
func brewDirVersion(name string) string {
	prefix := brewPrefix()
	// A brew formula/cask name is a bare identifier; a name with a path
	// separator can't be one and must not turn the Join into a traversal.
	if prefix == "" || name == "" || strings.ContainsAny(name, `/\`) {
		return ""
	}
	for _, room := range []string{"Caskroom", "Cellar"} {
		if v := versionFromDir(filepath.Join(prefix, room, name)); v != "" {
			return v
		}
	}
	return ""
}

// versionFromDir extracts the newest version among a keg/cask directory's
// version-named subdirectories. Dot-entries are skipped (Caskroom keeps a
// service .metadata dir); a cask's ",<build>" suffix is cut; entries without
// a numeric version (e.g. a cask pinned to "latest") are ignored — better no
// version than an incomparable string. Cellar may hold several versions
// until `brew cleanup`, so entries compete via IsNewer.
func versionFromDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		raw, _, _ := strings.Cut(e.Name(), ",")
		// versionRe's [\d.]* tail can capture a trailing dot ("1.2.3." from a
		// malformed dir name); IsNewer could never compare it, so trim now.
		v := strings.TrimRight(versionRe.FindString(raw), ".")
		if v == "" {
			continue
		}
		if best == "" || IsNewer(best, v) {
			best = v
		}
	}
	return best
}
