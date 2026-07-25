// Package logx is a dependency-free, errors-only session logger. It writes one
// plain-text file per session under ~/.config/keepkit/logs, created lazily on the
// first write. A session with no errors leaves no file at all — the presence of
// a file is itself the signal that something went wrong.
//
// The logger never breaks the app: its own failures (file won't open, disk
// full) are swallowed silently, because crashing or spraying the TUI because
// logging failed is worse than losing a log line.
package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/stanlyzoolo/keepkit/internal/configdir"
)

var (
	mu          sync.Mutex
	file        *os.File
	path        string
	header      string
	failed      bool   // latched failed-open flag: once OpenFile errors we never retry
	dirOverride string // test seam; empty in production
)

// logDir resolves <configdir.Base>/keepkit/logs, honoring the test override.
// Mirrors loader.MetaPath's resolution. Caller holds mu.
func logDir() string {
	if dirOverride != "" {
		return dirOverride
	}
	base, err := configdir.Base()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "keepkit", "logs")
}

// SetDirForTesting redirects the log directory and resets all logger state, so
// other packages' tests can capture logx output and stay order-independent
// (sticky globals otherwise make "no file created" assertions depend on test
// order within the package binary). The returned restore reverts to the
// *previous* override (not necessarily the real directory) and re-zeros the
// state, so a per-test redirect nests correctly inside a package TestMain that
// already redirected to a shared temp dir.
func SetDirForTesting(dir string) (restore func()) {
	mu.Lock()
	defer mu.Unlock()
	prev := dirOverride
	reset()
	dirOverride = dir
	return func() {
		mu.Lock()
		defer mu.Unlock()
		reset()
		dirOverride = prev
	}
}

// reset zeroes the mutable logger state. Caller holds mu.
func reset() {
	if file != nil {
		_ = file.Close()
	}
	file = nil
	path = ""
	header = ""
	failed = false
}

// SetHeader stores the header line written as the first line of the log file
// when (and if) it is eventually created. It does not create a file.
func SetHeader(s string) {
	mu.Lock()
	defer mu.Unlock()
	header = s
}

// Errorf appends one error record to the session log, creating the file lazily
// on the first call. All internal failures are swallowed silently.
func Errorf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()

	if file == nil {
		if failed {
			return
		}
		dir := logDir()
		if dir == "" {
			failed = true
			return
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			failed = true
			return
		}
		name := "keepkit-" + time.Now().Format("2006-01-02_15-04-05") + ".log"
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			failed = true
			return
		}
		file = f
		path = f.Name()
		if header != "" {
			_, _ = fmt.Fprintln(file, header)
		}
	}

	ts := time.Now().Format("2006-01-02T15:04:05.000Z07:00")
	_, _ = fmt.Fprintf(file, "%s ERROR %s\n", ts, fmt.Sprintf(format, args...))
}

// Recover is a defer helper that records a panic and re-raises it. It must be
// deferred directly inside the function whose panic it should catch, because
// recover() consumes the panic wherever it fires first — a defer above Bubble
// Tea's own recover would never see it.
//
// On a live panic it writes the panic value plus debug.Stack() (which, from
// inside this defer, still contains the gopanic frame and thus the real panic
// site) via Errorf, then re-panics so terminal restoration and error reporting
// proceed exactly as before. On a normal return it is a no-op.
func Recover(context string) {
	if r := recover(); r != nil {
		Errorf("panic in %s: %v\n%s", context, r, debug.Stack())
		panic(r)
	}
}

// Path returns the current session log file path, or "" if no file has been
// created yet.
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

// ReadAllForTesting returns the concatenated contents of every keepkit-*.log in
// dir, or "" when the directory is absent. It is the shared reader for tests
// that assert what the logger wrote (paired with SetDirForTesting), so each
// package's test file does not re-implement the scan.
func ReadAllForTesting(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var sb strings.Builder
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "keepkit-") && strings.HasSuffix(e.Name(), ".log") {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			sb.Write(data)
		}
	}
	return sb.String()
}
