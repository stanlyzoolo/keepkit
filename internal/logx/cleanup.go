package logx

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// keepLogs is the number of newest session logs Cleanup retains.
const keepLogs = 20

// Cleanup removes all but the newest keepLogs session logs. It filters to the
// keepkit-*.log naming, sorts by name (lexicographic order equals chronological
// order because the timestamp is zero-padded and colon-free), and deletes the
// tail. Foreign files in the directory are never touched. A missing directory
// (the common case — no errors yet) and any removal error are ignored.
func Cleanup() {
	mu.Lock()
	dir := logDir()
	mu.Unlock()
	if dir == "" {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var logs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "keepkit-") && strings.HasSuffix(name, ".log") {
			logs = append(logs, name)
		}
	}
	if len(logs) <= keepLogs {
		return
	}

	sort.Strings(logs)
	for _, name := range logs[:len(logs)-keepLogs] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
