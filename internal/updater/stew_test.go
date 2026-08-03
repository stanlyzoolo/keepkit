package updater

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// env returns a getenv stub that answers from vars (a nil map answers nothing),
// the shape the pure stew cores expect.
func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

var noEnv = env(nil)

// TestStewDirsFrom pins the pure resolution of stew's two directories: config
// values win, stew's own defaults fill the gaps, and a malformed config is
// treated as no config at all.
func TestStewDirsFrom(t *testing.T) {
	const home = "/home/tester"
	xdgData := env(map[string]string{"XDG_DATA_HOME": "/xdg/data"})

	tests := []struct {
		name     string
		config   string
		home     string
		goos     string
		getenv   func(string) string
		wantBin  string
		wantData string
	}{
		{name: "linux bare home", home: home, goos: "linux", getenv: noEnv,
			wantBin:  filepath.Join(home, ".local", "bin"),
			wantData: filepath.Join(home, ".local", "share", "stew")},
		{name: "linux honors XDG_DATA_HOME", home: home, goos: "linux", getenv: xdgData,
			wantBin:  filepath.Join(home, ".local", "bin"),
			wantData: filepath.Join("/xdg/data", "stew")},
		{name: "darwin same defaults as linux", home: home, goos: "darwin", getenv: noEnv,
			wantBin:  filepath.Join(home, ".local", "bin"),
			wantData: filepath.Join(home, ".local", "share", "stew")},
		{name: "windows defaults", home: home, goos: "windows", getenv: noEnv,
			wantBin:  filepath.Join(home, "AppData", "Local", "stew", "bin"),
			wantData: filepath.Join(home, "AppData", "Local", "stew")},
		{name: "config overrides both", config: `{"stewPath":"/cfg/data","stewBinPath":"/cfg/bin"}`, home: home, goos: "linux", getenv: noEnv,
			wantBin: "/cfg/bin", wantData: "/cfg/data"},
		{name: "config overrides one field", config: `{"stewBinPath":"/cfg/bin"}`, home: home, goos: "windows", getenv: noEnv,
			wantBin:  "/cfg/bin",
			wantData: filepath.Join(home, "AppData", "Local", "stew")},
		{name: "malformed config falls back to defaults", config: `{"stewBinPath":`, home: home, goos: "linux", getenv: noEnv,
			wantBin:  filepath.Join(home, ".local", "bin"),
			wantData: filepath.Join(home, ".local", "share", "stew")},
		{name: "empty home disables defaults", home: "", goos: "linux", getenv: noEnv,
			wantBin: "", wantData: ""},
		{name: "config values survive an empty home", config: `{"stewPath":"/cfg/data","stewBinPath":"/cfg/bin"}`, home: "", goos: "linux", getenv: noEnv,
			wantBin: "/cfg/bin", wantData: "/cfg/data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin, data := stewDirsFrom(tt.config, tt.getenv, tt.home, tt.goos)
			if bin != tt.wantBin || data != tt.wantData {
				t.Errorf("stewDirsFrom() = (%q, %q), want (%q, %q)", bin, data, tt.wantBin, tt.wantData)
			}
		})
	}
}

// TestStewConfigFilePath pins the config file location, stew's own resolution
// (GetStewConfigFilePath) rather than any convention of ours.
func TestStewConfigFilePath(t *testing.T) {
	const home = "/home/tester"

	tests := []struct {
		name   string
		home   string
		goos   string
		getenv func(string) string
		want   string
	}{
		{name: "linux bare home", home: home, goos: "linux", getenv: noEnv,
			want: filepath.Join(home, ".config", "stew", "stew.config.json")},
		{name: "linux honors XDG_CONFIG_HOME", home: home, goos: "darwin", getenv: env(map[string]string{"XDG_CONFIG_HOME": "/xdg/config"}),
			want: filepath.Join("/xdg/config", "stew", "stew.config.json")},
		{name: "windows", home: home, goos: "windows", getenv: noEnv,
			want: filepath.Join(home, "AppData", "Local", "stew", "Config", "stew.config.json")},
		{name: "empty home unix", home: "", goos: "linux", getenv: noEnv, want: ""},
		{name: "empty home windows", home: "", goos: "windows", getenv: noEnv, want: ""},
		{
			// XDG_CONFIG_HOME is honored without a home too — stew reads it
			// before touching the home dir at all.
			name: "XDG without home", home: "", goos: "linux", getenv: env(map[string]string{"XDG_CONFIG_HOME": "/xdg/config"}),
			want: filepath.Join("/xdg/config", "stew", "stew.config.json"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stewConfigFilePath(tt.getenv, tt.home, tt.goos); got != tt.want {
				t.Errorf("stewConfigFilePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStewLockHas pins the pure lock parser, including the two gates: the
// binary name must match and the source must be "github" — a URL-installed
// binary (source "other") is not upgradable by `stew upgrade`, so offering it
// would be a command we know will fail.
func TestStewLockHas(t *testing.T) {
	lock := `{
  "os": "linux",
  "arch": "amd64",
  "packages": [
    {"source": "github", "owner": "sharkdp", "repo": "fd", "tag": "v10.2.0", "binary": "fd"},
    {"source": "github", "binary": "rg"},
    {"source": "other", "binary": "custom-bin"}
  ]
}`
	tests := []struct {
		name string
		lock string
		bin  string
		want bool
	}{
		{name: "github package", lock: lock, bin: "fd", want: true},
		{name: "second github package", lock: lock, bin: "rg", want: true},
		{name: "url-installed package is not upgradable", lock: lock, bin: "custom-bin", want: false},
		{name: "binary not in lock", lock: lock, bin: "nonexistent", want: false},
		{name: "empty lock", lock: "", bin: "fd", want: false},
		{name: "empty binary name", lock: lock, bin: "", want: false},
		{name: "malformed lock", lock: `{"packages":`, bin: "fd", want: false},
		{
			// Keepkit never interpolates lock content into a command, so a
			// hostile binary field is inert — but it must not match either.
			name: "lookalike binary field", lock: `{"packages":[{"source":"github","binary":"fd & rm -rf /"}]}`, bin: "fd", want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stewLockHas(tt.lock, tt.bin); got != tt.want {
				t.Errorf("stewLockHas(_, %q) = %v, want %v", tt.bin, got, tt.want)
			}
		})
	}
}

// TestStewOwned pins the pure decision: both halves are required. The lock
// alone would claim a same-named binary shadowing stew's on PATH; the path
// alone would claim every hand-installed file in stew's (shared) default bin
// dir.
func TestStewOwned(t *testing.T) {
	const home = "/home/tester"
	bin := filepath.Join(home, ".local", "bin")
	lock := `{"packages":[{"source":"github","binary":"fd"}]}`
	shadowed := "/home/other/.cargo/bin/fd"

	tests := []struct {
		name     string
		realPath string
		stewBin  string
		lock     string
		want     bool
	}{
		{name: "under bin and in lock", realPath: filepath.Join(bin, "fd"), stewBin: bin, lock: lock, want: true},
		{name: "not under stew's bin", realPath: shadowed, stewBin: bin, lock: lock, want: false},
		{name: "under bin but not in lock", realPath: filepath.Join(bin, "rg"), stewBin: bin, lock: lock, want: false},
		{name: "empty stewBin disables the check", realPath: filepath.Join(bin, "fd"), stewBin: "", lock: lock, want: false},
		{name: "empty lock", realPath: filepath.Join(bin, "fd"), stewBin: bin, lock: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stewOwned(tt.realPath, tt.stewBin, tt.lock); got != tt.want {
				t.Errorf("stewOwned() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestStewManaged pins the OS-facing wiring end to end: config + lock file are
// read, defaults resolve against a test home, and the two directories can be
// relocated via the config exactly as stew allows.
func TestStewManaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture relies on unix config/data env vars")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	mustMkdirAll(t, home)
	setTestHomeDir(t, home)

	// Stew's defaults are what they would resolve to with no config file.
	// The binary sits in the default bin dir and the lock in the default data
	// dir, so only the file contents matter — no config to write.
	defaultBin := filepath.Join(home, ".local", "bin")
	defaultData := filepath.Join(home, ".local", "share", "stew")
	mustMkdirAll(t, defaultBin)
	mustMkdirAll(t, defaultData)
	if err := os.WriteFile(filepath.Join(defaultData, "Stewfile.lock.json"),
		[]byte(`{"packages":[{"source":"github","binary":"fd"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(defaultBin, "fd")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !stewManaged(binPath) {
		t.Error("stewManaged() = false for a binary in the default stew bin dir and lock")
	}
	if stewManaged(filepath.Join(defaultBin, "rg")) {
		t.Error("stewManaged() = true for a binary not in the lock file")
	}
	if stewManaged(filepath.Join(base, "somewhere", "fd")) {
		t.Error("stewManaged() = true for a binary outside stew's bin dir")
	}

	// A relocated install: the config moves both directories and keepkit must
	// follow them rather than its (or stew's) defaults.
	customBin := filepath.Join(base, "custom", "bin")
	customData := filepath.Join(base, "custom", "data")
	mustMkdirAll(t, customBin)
	mustMkdirAll(t, customData)
	configDir := filepath.Join(home, ".config", "stew")
	mustMkdirAll(t, configDir)
	if err := os.WriteFile(filepath.Join(configDir, "stew.config.json"),
		[]byte(`{"stewPath":"`+customData+`","stewBinPath":"`+customBin+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customData, "Stewfile.lock.json"),
		[]byte(`{"packages":[{"source":"github","binary":"rg"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	customPath := filepath.Join(customBin, "rg")
	if err := os.WriteFile(customPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !stewManaged(customPath) {
		t.Error("stewManaged() = false for a binary under the configured stew directories")
	}
	if stewManaged(binPath) {
		t.Error("stewManaged() = true for a default-dir binary after the config moved the roots")
	}
}
