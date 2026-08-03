// stew detection lives here: stew (marwanhawari/stew) is a source-compiler
// package manager whose binaries land in a plain, shared bin directory — the
// default is ~/.local/bin on unix — so no path convention alone can prove
// ownership. The two things that can are stew's own files: stew.config.json
// (which resolves where stew installs to and where its data lives) and
// Stewfile.lock.json (which lists exactly which binaries stew manages). Both
// are read by the OS-facing wrapper; the pure core only parses.

package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// stewConfigFilePath resolves stew.config.json's location, mirroring stew's
// own rules (GetStewConfigFilePath in lib/config.go): $XDG_CONFIG_HOME/stew or
// ~/.config/stew on unix, %LOCALAPPDATA%.../stew/Config on Windows.
func stewConfigFilePath(getenv func(string) string, home, goos string) string {
	switch goos {
	case "windows":
		if home == "" {
			return ""
		}
		return filepath.Join(home, "AppData", "Local", "stew", "Config", "stew.config.json")
	default:
		if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "stew", "stew.config.json")
		}
		if home == "" {
			return ""
		}
		return filepath.Join(home, ".config", "stew", "stew.config.json")
	}
}

// readStewConfig is the OS-facing half of the stew signal: the config file's
// contents, or "" when absent or unreadable — a missing file just means stew
// is not configured (or not installed), which is the normal non-match state.
func readStewConfig() string {
	p := stewConfigFilePath(os.Getenv, homeDir(), runtime.GOOS)
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// stewDirsFrom is the pure resolution core: stew's two configurable
// directories from its config file, with stew's platform defaults for the
// fields the file (or one of its two values) omits. A malformed config is
// treated as an empty one — defaults apply. Either result being "" means the
// stew check is off (no home to default against), exactly like an empty
// managerDirs field.
func stewDirsFrom(configJSON string, getenv func(string) string, home, goos string) (bin, data string) {
	if configJSON != "" {
		var cfg struct {
			StewPath    string `json:"stewPath"`
			StewBinPath string `json:"stewBinPath"`
		}
		if err := json.Unmarshal([]byte(configJSON), &cfg); err == nil {
			bin, data = cfg.StewBinPath, cfg.StewPath
		}
	}
	if bin == "" {
		bin = stewDefaultBin(home, goos)
	}
	if data == "" {
		data = stewDefaultData(getenv, home, goos)
	}
	return bin, data
}

// stewDefaultBin is stew's default install location (GetDefaultStewBinPath).
func stewDefaultBin(home, goos string) string {
	if home == "" {
		return ""
	}
	switch goos {
	case "windows":
		return filepath.Join(home, "AppData", "Local", "stew", "bin")
	default:
		return filepath.Join(home, ".local", "bin")
	}
}

// stewDefaultData is stew's default data root, the parent of pkg/ and
// Stewfile.lock.json (GetDefaultStewPath).
func stewDefaultData(getenv func(string) string, home, goos string) string {
	if home == "" {
		return ""
	}
	switch goos {
	case "windows":
		return filepath.Join(home, "AppData", "Local", "stew")
	default:
		if xdg := getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "stew")
		}
		return filepath.Join(home, ".local", "share", "stew")
	}
}

// readStewLock reads stew's lock file at <dataDir>/Stewfile.lock.json, or ""
// when absent/unreadable. Stew rewrites it on every install/upgrade, so a
// stale-but-present file is the normal state; content arriving here is trusted
// to the same degree pnpm shim content is — see the bare-scope hole in
// npmPackage for why that still gets validated rather than interpolated.
func readStewLock(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dataDir, "Stewfile.lock.json"))
	if err != nil {
		return ""
	}
	return string(b)
}

// stewManaged reports whether stew owns the binary on PATH at realPath. Thin
// OS-facing wrapper over stewOwned: it reads stew.config.json and
// Stewfile.lock.json and feeds their contents to the pure core, exactly the
// way readPnpmShim hands shimTarget to detectFromPath.
func stewManaged(realPath string) bool {
	bin, data := stewDirsFrom(readStewConfig(), os.Getenv, homeDir(), runtime.GOOS)
	if data == "" {
		return false
	}
	// stewBin is a root like the managerDirs ones, so it gets the same
	// symlink expansion before it is compared against the resolved binary path.
	return stewOwned(realPath, resolveDir(bin), readStewLock(data))
}

// stewOwned is the pure decision, and both halves are load-bearing. The lock
// file alone would claim any binary that shares a stew-managed name — the
// classic shadowing case where a cargo `rg` on PATH ahead of stew's `~/.local/
// bin/rg` must not be offered `stew upgrade rg`. The path alone would claim
// every plain binary the user dropped into ~/.local/bin by hand, offering
// `stew upgrade <name>` for something stew does not manage (stew refuses with
// "binary not installed"). Together they say "this exact binary is stew's".
func stewOwned(realPath, stewBin, lockJSON string) bool {
	if stewBin == "" || !underDir(realPath, stewBin) {
		return false
	}
	return stewLockHas(lockJSON, binaryName(realPath))
}

// stewLockHas reports whether the lock file names a stew-managed package that
// installs the given binary. Only github-sourced packages are upgradable:
// source == "other" means the binary was installed from a URL, which `stew
// upgrade` refuses by design, so offering it here would be a command we know
// will fail — the honest degradation is to stay quiet.
func stewLockHas(lockJSON, binName string) bool {
	if lockJSON == "" || binName == "" {
		return false
	}
	var lock struct {
		Packages []struct {
			Source string `json:"source"`
			Binary string `json:"binary"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(lockJSON), &lock); err != nil {
		return false
	}
	for _, pkg := range lock.Packages {
		if pkg.Binary == binName && pkg.Source == "github" {
			return true
		}
	}
	return false
}
