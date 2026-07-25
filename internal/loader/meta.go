package loader

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/stanlyzoolo/keepkit/internal/configdir"
	"github.com/stanlyzoolo/keepkit/internal/logx"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusTrying   Status = "trying"
	StatusInactive Status = "inactive"
)

var StatusSymbol = map[Status]string{
	StatusActive:   "●",
	StatusTrying:   "○",
	StatusInactive: "✕",
}

var StatusCycle = []Status{
	StatusActive,
	StatusTrying,
	StatusInactive,
}

type ToolMeta struct {
	Name   string   `yaml:"name"`
	Status Status   `yaml:"status"`
	Added  string   `yaml:"added"`
	Tags   []string `yaml:"tags,omitempty"`
	Note   string   `yaml:"note,omitempty"`
	GitHub string   `yaml:"github,omitempty"`
	// UpdateCmd is an explicit update command that overrides package-manager
	// detection. omitempty keeps meta.yaml written without the field backward
	// compatible.
	UpdateCmd string `yaml:"update_cmd,omitempty"`
}

// testConfigDir overrides the config directory in tests.
var testConfigDir string

// SetConfigDirForTesting points MetaPath at dir and returns a restore func, so
// a test binary can isolate the tracker for its whole run.
//
// Exported for the same reason logx.SetDirForTesting is: the packages that can
// reach meta.yaml are not only this one — a model test that drives the tags,
// track, rename or untrack handlers lands in SaveMeta, which rewrites the file
// wholesale. Leaving that to each test to remember (via its own temp HOME) is
// what let an ad-hoc probe overwrite a real tracker; every package whose tests
// can reach a mutation now installs this in TestMain instead, and
// TestConfigDirIsolated fails loudly if that ever gets removed.
//
// restore reverts to the previous override, not to the real directory, so a
// per-test override nested inside the package-wide one cannot un-isolate the
// rest of the binary.
func SetConfigDirForTesting(dir string) (restore func()) {
	prev := testConfigDir
	testConfigDir = dir
	return func() { testConfigDir = prev }
}

// ConfigDirOverride reports the active test override ("" in a normal run). It
// exists so a test can assert its own isolation.
func ConfigDirOverride() string {
	return testConfigDir
}

func MetaPath() string {
	if testConfigDir != "" {
		return filepath.Join(testConfigDir, "keepkit", "meta.yaml")
	}
	configDir, err := configdir.Base()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "keepkit", "meta.yaml")
}

func LoadMeta() ([]ToolMeta, error) {
	path := MetaPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []ToolMeta{}, nil
	}
	if err != nil {
		return nil, err
	}
	var meta []ToolMeta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	// In-memory migration of retired statuses; the file keeps the old value
	// until the next SaveMeta. Unknown statuses pass through untouched —
	// NextStatus already falls back to active for them.
	droppedTags := false
	for i := range meta {
		switch meta[i].Status {
		case "forgotten", "archived":
			meta[i].Status = StatusInactive
		}
		// One tag per tool: the field stays a []string (no meta.yaml schema
		// break) but holds the len<=1 invariant, so grouping by tag has no
		// duplicate rows and no first-tag-wins heuristic at render time. A
		// legacy multi-tag list keeps its first entry — the same "first tag
		// wins" rule the editor applies to comma-separated input. In-memory
		// only, like the status migration above.
		if len(meta[i].Tags) > 1 {
			meta[i].Tags = meta[i].Tags[:1]
			droppedTags = true
		}
	}
	// The status migration replaces a retired value with its successor; this one
	// discards user-authored data, and the next SaveMeta — which any keystroke
	// that edits a note or cycles a status triggers — makes it permanent. Stash
	// the pre-migration file once so the dropped tags stay recoverable.
	if droppedTags {
		backupMeta(path, data)
	}
	return meta, nil
}

// backupMeta writes the pre-migration meta.yaml next to the original as
// meta.yaml.bak. Best-effort by design: a tracker that cannot be backed up must
// still open, so a failure is logged and swallowed rather than surfaced. It runs
// only on a load that actually dropped tags, so the copy is not overwritten by
// later (already migrated) loads.
func backupMeta(path string, data []byte) {
	bak := path + ".bak"
	if err := os.WriteFile(bak, data, 0644); err != nil {
		logx.Errorf("loader.LoadMeta: tag migration backup %s: %v", bak, err)
	}
}

// SaveMeta writes meta.yaml atomically: the data lands in a temp file in the
// same directory first, then rename replaces the old file in one step, so a
// crash mid-write can never leave a truncated meta.yaml behind.
func SaveMeta(meta []ToolMeta) error {
	path := MetaPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		logx.Errorf("loader.SaveMeta: mkdir %s: %v", filepath.Dir(path), err)
		return err
	}
	data, err := yaml.Marshal(meta)
	if err != nil {
		logx.Errorf("loader.SaveMeta: marshal: %v", err)
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		logx.Errorf("loader.SaveMeta: write %s: %v", tmp, err)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		logx.Errorf("loader.SaveMeta: rename %s -> %s: %v", tmp, path, err)
		return err
	}
	return nil
}

func FindMeta(meta []ToolMeta, name string) *ToolMeta {
	for i := range meta {
		if meta[i].Name == name {
			return &meta[i]
		}
	}
	return nil
}

func UpsertMeta(meta []ToolMeta, entry ToolMeta) []ToolMeta {
	for i := range meta {
		if meta[i].Name == entry.Name {
			meta[i] = entry
			return meta
		}
	}
	return append(meta, entry)
}

func RemoveMeta(meta []ToolMeta, name string) []ToolMeta {
	out := meta[:0]
	for _, m := range meta {
		if m.Name != name {
			out = append(out, m)
		}
	}
	return out
}

func TodayDate() string {
	return time.Now().Format("2006-01-02")
}

func NextStatus(s Status) Status {
	for i, cs := range StatusCycle {
		if cs == s {
			return StatusCycle[(i+1)%len(StatusCycle)]
		}
	}
	return StatusActive
}
