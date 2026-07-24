package configdir

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestBaseFor(t *testing.T) {
	const (
		home    = "/home/user"
		appData = `C:\Users\user\AppData\Roaming`
		xdgDir  = "/custom/xdg"
	)
	userConfig := func() (string, error) { return appData, nil }
	homeDir := func() (string, error) { return home, nil }
	noEnv := func(string) string { return "" }
	xdgEnv := func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return xdgDir
		}
		return ""
	}

	tests := []struct {
		name   string
		goos   string
		getenv func(string) string
		want   string
	}{
		{"windows uses UserConfigDir (%AppData%)", "windows", noEnv, appData},
		{"windows ignores XDG", "windows", xdgEnv, appData},
		{"linux without XDG uses ~/.config", "linux", noEnv, filepath.Join(home, ".config")},
		{"linux honors XDG_CONFIG_HOME", "linux", xdgEnv, xdgDir},
		{"darwin without XDG uses ~/.config (not Application Support)", "darwin", noEnv, filepath.Join(home, ".config")},
		{"darwin honors XDG_CONFIG_HOME", "darwin", xdgEnv, xdgDir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := baseFor(tt.goos, tt.getenv, userConfig, homeDir)
			if err != nil {
				t.Fatalf("baseFor returned error: %v", err)
			}
			if got != tt.want {
				t.Errorf("baseFor(%q) = %q, want %q", tt.goos, got, tt.want)
			}
		})
	}
}

// TestBaseForHomeError: on the unix path with no XDG, a UserHomeDir failure
// propagates instead of returning a bogus ".config" relative path.
func TestBaseForHomeError(t *testing.T) {
	boom := errors.New("no home")
	failHome := func() (string, error) { return "", boom }
	okConfig := func() (string, error) { return "/whatever", nil }
	noEnv := func(string) string { return "" }

	if _, err := baseFor("linux", noEnv, okConfig, failHome); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the home-dir error propagated", err)
	}
}

// TestBaseForWindowsConfigError: the Windows branch surfaces a UserConfigDir
// error rather than falling back to ~/.config.
func TestBaseForWindowsConfigError(t *testing.T) {
	boom := errors.New("no appdata")
	failConfig := func() (string, error) { return "", boom }
	okHome := func() (string, error) { return `C:\Users\user`, nil }
	noEnv := func(string) string { return "" }

	if _, err := baseFor("windows", noEnv, failConfig, okHome); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the UserConfigDir error propagated", err)
	}
}
