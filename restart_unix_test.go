//go:build !windows

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stanlyzoolo/keepkit/internal/logx"
)

// TestResolveSelfPath pins the argv0 semantics of the restart path resolution.
// The load-bearing case is the first one: a bare argv0 must go through PATH even
// when os.Executable() named an existing file, because on Linux that file is the
// symlink-resolved /proc/self/exe and can still be the pre-upgrade binary — a
// restart into it would loop the banner forever.
func TestResolveSelfPath(t *testing.T) {
	notFound := errors.New("not found in $PATH")
	tests := []struct {
		name         string
		executable   string
		argv0        string
		existing     []string
		pathHit      string // lookPath result; empty means a miss
		pathHitNoErr bool   // return ("", nil) instead of ("", notFound)
		want         string
		wantLookedUp string // the name lookPath must have been asked for
		wantErr      bool
	}{
		{
			name:         "bare argv0 prefers PATH over a live executable",
			executable:   "/old/keg/bin/keepkit",
			argv0:        "keepkit",
			existing:     []string{"/old/keg/bin/keepkit", "/usr/local/bin/keepkit"},
			pathHit:      "/usr/local/bin/keepkit",
			want:         "/usr/local/bin/keepkit",
			wantLookedUp: "keepkit",
		},
		{
			name:         "lookPath miss falls back to the executable",
			executable:   "/usr/local/bin/keepkit",
			argv0:        "keepkit",
			existing:     []string{"/usr/local/bin/keepkit"},
			want:         "/usr/local/bin/keepkit",
			wantLookedUp: "keepkit",
		},
		{
			name:         "lookPath returning an empty path with no error is a miss",
			executable:   "/usr/local/bin/keepkit",
			argv0:        "keepkit",
			existing:     []string{"/usr/local/bin/keepkit"},
			pathHitNoErr: true,
			want:         "/usr/local/bin/keepkit",
			wantLookedUp: "keepkit",
		},
		{
			// The empty-path check on its own: with no executable to compare
			// against, sameProgram accepts anything, so only `p != ""` stops the
			// empty string from being returned as a path to syscall.Exec.
			name:         "an empty lookPath hit is never a path",
			argv0:        "keepkit",
			pathHitNoErr: true,
			wantLookedUp: "keepkit",
			wantErr:      true,
		},
		{
			name:         "a PATH hit for a rewritten argv0 is rejected",
			executable:   "/usr/local/bin/keepkit",
			argv0:        "kt-wrapper",
			existing:     []string{"/usr/local/bin/keepkit", "/usr/bin/kt-wrapper"},
			pathHit:      "/usr/bin/kt-wrapper",
			want:         "/usr/local/bin/keepkit",
			wantLookedUp: "kt-wrapper",
		},
		{
			name:       "argv0 with a separator wins when it exists",
			executable: "/proc/self/exe/resolved/keepkit",
			argv0:      "./bin/keepkit",
			existing:   []string{"./bin/keepkit", "/proc/self/exe/resolved/keepkit"},
			pathHit:    "/usr/local/bin/keepkit",
			want:       "./bin/keepkit",
		},
		{
			name:       "vanished argv0 path falls back to the executable",
			executable: "/usr/local/bin/keepkit",
			argv0:      "/old/build/keepkit",
			existing:   []string{"/usr/local/bin/keepkit"},
			pathHit:    "/usr/local/bin/keepkit", // never consulted for a path argv0
			want:       "/usr/local/bin/keepkit",
		},
		{
			name:         "everything missed is an error",
			executable:   "/gone/keepkit",
			argv0:        "/also/gone/keepkit",
			wantErr:      true,
			wantLookedUp: "",
		},
		{
			name:    "no executable and no argv0",
			wantErr: true,
		},
		{
			name:       "empty argv0 still uses the executable",
			executable: "/usr/local/bin/keepkit",
			existing:   []string{"/usr/local/bin/keepkit"},
			want:       "/usr/local/bin/keepkit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists := func(p string) bool {
				for _, e := range tt.existing {
					if e == p {
						return true
					}
				}
				return false
			}
			lookedUp := ""
			lookPath := func(name string) (string, error) {
				lookedUp = name
				if tt.pathHit == "" {
					if tt.pathHitNoErr {
						return "", nil
					}
					return "", notFound
				}
				return tt.pathHit, nil
			}
			got, err := resolveSelfPath(tt.executable, exists, lookPath, tt.argv0)
			// PATH must be searched for argv0 itself, never for a hardcoded name.
			if lookedUp != tt.wantLookedUp {
				t.Errorf("lookPath called with %q, want %q", lookedUp, tt.wantLookedUp)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveSelfPath = %q, want an error", got)
				}
				if got != "" {
					t.Errorf("resolveSelfPath returned %q alongside an error, want empty", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSelfPath: unexpected error %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveSelfPath = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveSelfPathNeverLooksUpAPath: an argv0 carrying a separator is already
// a path, so PATH is not consulted for it — a same-named binary earlier in PATH
// must not hijack a restart the user started with ./keepkit.
func TestResolveSelfPathNeverLooksUpAPath(t *testing.T) {
	called := false
	lookPath := func(string) (string, error) {
		called = true
		return "/usr/local/bin/keepkit", nil
	}
	got, err := resolveSelfPath("/fallback/keepkit", func(string) bool { return true }, lookPath, "./keepkit")
	if err != nil {
		t.Fatalf("resolveSelfPath: %v", err)
	}
	if got != "./keepkit" {
		t.Errorf("resolveSelfPath = %q, want ./keepkit", got)
	}
	if called {
		t.Error("lookPath was consulted for an argv0 that is already a path")
	}
}

// TestFileExists covers the real-filesystem probe handed to resolveSelfPath.
func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "keepkit")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !fileExists(file) {
		t.Errorf("fileExists(%q) = false, want true", file)
	}
	if fileExists(filepath.Join(dir, "absent")) {
		t.Error("fileExists on a missing path = true, want false")
	}
}

// TestSelfPathResolves: the wrapper finds something to exec in the test binary's
// own environment (argv0 is the test binary, which exists as a path), so the
// os.Executable/LookPath/os.Args plumbing is not miswired.
func TestSelfPathResolves(t *testing.T) {
	got, err := selfPath()
	if err != nil {
		t.Fatalf("selfPath: %v", err)
	}
	if got == "" {
		t.Error("selfPath returned an empty path with no error")
	}
}

// TestRestartSelfWith covers both outcomes of the restart: a successful exec
// never comes back, and either failure (resolve or exec) prints the hint that
// says the update did land and logs the anomaly.
func TestRestartSelfWith(t *testing.T) {
	tests := []struct {
		name     string
		resolve  func() (string, error)
		execErr  error
		wantExec bool
		wantLog  string
	}{
		{
			name:     "exec fails",
			resolve:  func() (string, error) { return "/usr/local/bin/keepkit", nil },
			execErr:  errors.New("exec format error"),
			wantExec: true,
			wantLog:  "exec format error",
		},
		{
			name:    "path resolution fails",
			resolve: func() (string, error) { return "", errors.New("no keepkit binary to restart") },
			wantLog: "no keepkit binary to restart",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logDir := t.TempDir()
			restore := logx.SetDirForTesting(logDir)
			defer restore()

			execed := ""
			var out bytes.Buffer
			restartSelfWith(tt.resolve, func(path string, _, _ []string) error {
				execed = path
				return tt.execErr
			}, &out)

			if tt.wantExec && execed != "/usr/local/bin/keepkit" {
				t.Errorf("exec'd %q, want the resolved path", execed)
			}
			if !tt.wantExec && execed != "" {
				t.Errorf("exec'd %q after a failed resolve, want no exec at all", execed)
			}
			// The wording is pinned here rather than against the const: this is
			// where the user actually reads it, and it must say the update landed.
			if got := strings.TrimSpace(out.String()); got != "keepkit updated — run keepkit again" {
				t.Errorf("printed %q, want the restart hint", got)
			}
			if log := logx.ReadAllForTesting(logDir); !strings.Contains(log, tt.wantLog) {
				t.Errorf("log = %q, want it to record %q", log, tt.wantLog)
			}
		})
	}
}

// TestRestartSelfWithSuccessIsSilent: a successful exec replaces the image, so
// nothing after it runs — no hint, no log line. The stub returns nil to stand in
// for "did not come back".
func TestRestartSelfWithSuccessIsSilent(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	var out bytes.Buffer
	restartSelfWith(
		func() (string, error) { return "/usr/local/bin/keepkit", nil },
		func(string, []string, []string) error { return nil },
		&out,
	)
	if log := logx.ReadAllForTesting(logDir); log != "" {
		t.Errorf("log = %q, want nothing logged on a successful exec", log)
	}
	if out.Len() != 0 {
		t.Errorf("printed %q, want nothing on a successful exec", out.String())
	}
}
