package logx

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// logFiles returns the keepkit-*.log entries in dir.
func logFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "keepkit-") && strings.HasSuffix(e.Name(), ".log") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestPathLazyNoFile(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	if p := Path(); p != "" {
		t.Fatalf("Path() = %q, want empty before any Errorf", p)
	}
	if files := logFiles(t, dir); len(files) != 0 {
		t.Fatalf("expected no log file, got %v", files)
	}
}

func TestFirstErrorfCreatesFileWithHeader(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	SetHeader("keepkit v1.4.0 darwin/arm64 tools=12 token=config")
	Errorf("something %s", "broke")

	files := logFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("expected exactly one log file, got %v", files)
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (header + record), got %d: %q", len(lines), string(data))
	}
	if lines[0] != "keepkit v1.4.0 darwin/arm64 tools=12 token=config" {
		t.Errorf("header line = %q", lines[0])
	}
	if !strings.Contains(lines[1], "ERROR something broke") {
		t.Errorf("record line = %q", lines[1])
	}
	if Path() == "" {
		t.Error("Path() empty after Errorf")
	}
}

func TestSecondErrorfAppendsNoDuplicateHeader(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	SetHeader("HEADER")
	Errorf("first")
	Errorf("second")

	files := logFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("expected one file, got %v", files)
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Count(got, "HEADER") != 1 {
		t.Errorf("header should appear once, got:\n%s", got)
	}
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("both records should be present, got:\n%s", got)
	}
}

func TestErrorfNoHeaderNoLeadingBlank(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	Errorf("only record")

	files := logFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("expected one file, got %v", files)
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 line without a header, got %d: %q", len(lines), string(data))
	}
	if !strings.Contains(lines[0], "ERROR only record") {
		t.Errorf("record line = %q", lines[0])
	}
}

func TestErrorfUnwritableDirLatchesFailed(t *testing.T) {
	dir := t.TempDir()
	// Make the log dir's parent a regular file so MkdirAll fails on every OS.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	restore := SetDirForTesting(filepath.Join(blocker, "logs"))
	defer restore()

	Errorf("boom") // must not panic

	if p := Path(); p != "" {
		t.Errorf("Path() = %q, want empty after failed open", p)
	}
	mu.Lock()
	latched := failed
	mu.Unlock()
	if !latched {
		t.Error("failed flag should be latched after a failed open")
	}
}

// panickingFunc is a named function so its name shows up in debug.Stack().
func panickingFunc() {
	panic("kaboom")
}

func TestRecoverLogsTraceWithRealSite(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	func() {
		defer func() {
			// Swallow the re-panic so the test continues.
			_ = recover()
		}()
		defer Recover("test.ctx")
		panickingFunc()
	}()

	files := logFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("expected one log file, got %v", files)
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "panic in test.ctx") {
		t.Errorf("log missing context, got:\n%s", got)
	}
	if !strings.Contains(got, "kaboom") {
		t.Errorf("log missing panic value, got:\n%s", got)
	}
	if !strings.Contains(got, "panickingFunc") {
		t.Errorf("trace should name the real panic site, got:\n%s", got)
	}
}

func TestRecoverRePanics(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	var caught any
	func() {
		defer func() {
			caught = recover()
		}()
		defer Recover("ctx")
		panic("original")
	}()

	if caught != "original" {
		t.Errorf("expected re-panic with %q, caught %v", "original", caught)
	}
}

func TestRecoverNoPanicNoWrite(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	func() {
		defer Recover("ctx")
		// normal return, no panic
	}()

	if files := logFiles(t, dir); len(files) != 0 {
		t.Fatalf("expected no log file on normal return, got %v", files)
	}
}

func TestErrorfConcurrent(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	SetHeader("H")
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			Errorf("record-%d", i)
		}(i)
	}
	wg.Wait()

	files := logFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("expected one file, got %v", files)
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	// Header + n records, every line intact (no interleaving/torn writes).
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != n+1 {
		t.Fatalf("expected %d lines, got %d", n+1, len(lines))
	}
	records := 0
	for _, ln := range lines {
		if strings.Contains(ln, "ERROR record-") {
			records++
		}
	}
	if records != n {
		t.Errorf("expected %d record lines, got %d", n, records)
	}
}
