package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func seed(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupKeepsNewest20(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	// 25 files with lexicographically ordered names (chronological).
	for i := 0; i < 25; i++ {
		seed(t, dir, fmt.Sprintf("keepkit-2026-07-17_%02d-00-00.log", i))
	}

	Cleanup()

	remaining := logFiles(t, dir)
	if len(remaining) != 20 {
		t.Fatalf("expected 20 files kept, got %d", len(remaining))
	}
	// The 5 oldest (00..04) must be gone; 05..24 kept.
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("keepkit-2026-07-17_%02d-00-00.log", i)
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s removed", name)
		}
	}
	for i := 5; i < 25; i++ {
		name := fmt.Sprintf("keepkit-2026-07-17_%02d-00-00.log", i)
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s kept: %v", name, err)
		}
	}
}

func TestCleanupLeavesForeignFiles(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	for i := 0; i < 25; i++ {
		seed(t, dir, fmt.Sprintf("keepkit-2026-07-17_%02d-00-00.log", i))
	}
	seed(t, dir, "notes.txt")
	seed(t, dir, "keepkit-x.txt") // wrong suffix

	Cleanup()

	for _, name := range []string{"notes.txt", "keepkit-x.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("foreign file %s should be untouched: %v", name, err)
		}
	}
}

func TestCleanupMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	restore := SetDirForTesting(dir)
	defer restore()

	Cleanup() // must not panic or error
}

func TestCleanupFewerThan20(t *testing.T) {
	dir := t.TempDir()
	restore := SetDirForTesting(dir)
	defer restore()

	for i := 0; i < 5; i++ {
		seed(t, dir, fmt.Sprintf("keepkit-2026-07-17_%02d-00-00.log", i))
	}

	Cleanup()

	if got := len(logFiles(t, dir)); got != 5 {
		t.Fatalf("expected all 5 files kept, got %d", got)
	}
}
