package model

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
	"github.com/stanlyzoolo/keepkit/internal/updater"
	"github.com/stanlyzoolo/keepkit/internal/version"
)

func TestFetchHelpTakeoverLogs(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "faketui")
	script := "#!/bin/sh\nprintf '\\033[?1049lpanic: no tty\\n'\nexit 101\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	msg, ok := fetchHelpCmd("faketui", helpModeHelp)().(helpOutputMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T", msg)
	}
	if msg.output != "" {
		t.Errorf("takeover output leaked into help: %q", msg.output)
	}
	out := logx.ReadAllForTesting(logDir)
	if !strings.Contains(out, "faketui") || !strings.Contains(out, "TUI takeover") {
		t.Errorf("log should record the takeover and tool name, got:\n%s", out)
	}
}

func TestFetchHelpSuccessNoLog(t *testing.T) {
	dir := t.TempDir()
	sane := filepath.Join(dir, "fakecli")
	if err := os.WriteFile(sane, []byte("#!/bin/sh\necho 'Usage: fakecli'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	msg := fetchHelpCmd("fakecli", helpModeHelp)().(helpOutputMsg)
	if msg.output != "Usage: fakecli\n" {
		t.Fatalf("plain help lost: %q, err %v", msg.output, msg.err)
	}
	if out := logx.ReadAllForTesting(logDir); out != "" {
		t.Errorf("a successful help capture must not log, got:\n%s", out)
	}
}

type safeCmdMsg struct{ v int }

func TestSafeCmdPassesMsgThrough(t *testing.T) {
	cmd := safeCmd("ctx", func() tea.Msg { return safeCmdMsg{v: 7} })
	msg, ok := cmd().(safeCmdMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T", msg)
	}
	if msg.v != 7 {
		t.Errorf("msg not passed through untouched: %+v", msg)
	}
}

func safeCmdPanicSite() tea.Msg {
	panic("cmd boom")
}

func TestSafeCmdLogsAndRePanics(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	cmd := safeCmd("panic.ctx", safeCmdPanicSite)

	var caught any
	func() {
		defer func() { caught = recover() }()
		cmd()
	}()

	if caught != "cmd boom" {
		t.Errorf("expected re-panic with %q, caught %v", "cmd boom", caught)
	}
	out := logx.ReadAllForTesting(logDir)
	if !strings.Contains(out, "panic in panic.ctx") {
		t.Errorf("log missing context, got:\n%s", out)
	}
	if !strings.Contains(out, "safeCmdPanicSite") {
		t.Errorf("trace should name the real panic site, got:\n%s", out)
	}
}

func TestIsTUITakeover(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"plain help text", "Usage: tool [OPTIONS]", false},
		{"SGR-colored help", "\x1b[1mUsage:\x1b[0m tool", false},
		{"leave alt screen (inertia panic prefix)", "\x1b[?1049l\nthread 'main' panicked at …", true},
		{"enter alt screen (TUI ran until the timeout)", "\x1b[?1049h\x1b[2J\x1b[?25l frames…", true},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTUITakeover([]byte(tt.in)); got != tt.want {
				t.Errorf("isTUITakeover(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestFetchHelpCmdRejectsTUITakeover drives fetchHelpCmd against a fake tool
// that answers every help flag the way inertia does — alt-screen escape plus
// a crash trace — and expects the empty-output error path (which the
// helpOutputMsg handler turns into "No --help output for <name>…"), not the
// captured garbage.
func TestFetchHelpCmdRejectsTUITakeover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake tool")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "faketui")
	script := "#!/bin/sh\nprintf '\\033[?1049lpanic: no tty\\n'\nexit 101\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	msg, ok := fetchHelpCmd("faketui", helpModeHelp)().(helpOutputMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T", msg)
	}
	if msg.output != "" {
		t.Errorf("takeover output leaked into help: %q", msg.output)
	}

	// Control: a fake tool with normal help output still comes through.
	sane := filepath.Join(dir, "fakecli")
	if err := os.WriteFile(sane, []byte("#!/bin/sh\necho 'Usage: fakecli'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	msg = fetchHelpCmd("fakecli", helpModeHelp)().(helpOutputMsg)
	if msg.output != "Usage: fakecli\n" {
		t.Errorf("plain help lost: %q, err %v", msg.output, msg.err)
	}
}

// updateDoneModel builds a fully-initialized two-tool model with rg's installed
// version older than latest (so hasUpdate(rg) is true and rg sorts to the top of
// the update-grouped list). rg is mid-update: updatingFor/updateLogFor set.
func updateDoneModel(t *testing.T) Model {
	t.Helper()
	m := New([]loader.ToolMeta{
		{Name: "rg", GitHub: "github.com/BurntSushi/ripgrep"},
		{Name: "fzf"},
	})
	m = mustModel(m.Update(tea.WindowSizeMsg{Width: 120, Height: 24}))
	m.versions["rg"] = VersionInfo{Installed: "1.0.0", Latest: "2.0.0", InstalledKnown: true}
	m.updatePlan = updater.Plan{Manager: "brew", Argv: []string{"brew", "upgrade", "ripgrep"}, Display: "brew upgrade ripgrep"}
	m.updatingFor = "rg"
	m.updateLogFor = "rg"
	m.updateLog = []string{"==> Upgrading ripgrep", "==> Pouring ripgrep"}
	m.metaSelected = m.indexOfMeta("rg")
	return m
}

// TestUpdateDoneSuccess: a successful updateDoneMsg clears updatingFor, sets the
// "updated <name>" status and returns a command that re-detects the installed
// version (installedMsg for the same tool).
func TestUpdateDoneSuccess(t *testing.T) {
	shrinkStatusTTL(t)
	m := updateDoneModel(t)
	nm, cmd := m.Update(updateDoneMsg{tool: "rg", err: nil})
	m2 := nm.(Model)
	if m2.updatingFor != "" {
		t.Errorf("updatingFor = %q, want empty after done", m2.updatingFor)
	}
	if m2.statusMsg != "updated rg" {
		t.Errorf("statusMsg = %q, want %q", m2.statusMsg, "updated rg")
	}
	if cmd == nil {
		t.Fatal("want a re-fetch command (batched with the status tick), got nil")
	}
	// The success cmd batches the status-expiry tick with the installed re-detect.
	if !batchProducesInstalled(cmd, "rg") {
		t.Error("batched cmd does not re-detect the installed version for rg")
	}
}

// TestUpdateDoneFailure: a failed updateDoneMsg clears updatingFor, sets the
// "see [3]" status, writes a log line (manager + tool, never a token) and does
// not re-fetch.
func TestUpdateDoneFailure(t *testing.T) {
	shrinkStatusTTL(t)
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	m := updateDoneModel(t)
	nm, cmd := m.Update(updateDoneMsg{tool: "rg", err: errUpdateTest})
	m2 := nm.(Model)
	if m2.updatingFor != "" {
		t.Errorf("updatingFor = %q, want empty after failure", m2.updatingFor)
	}
	if m2.statusMsg != "update failed — see [3]" {
		t.Errorf("statusMsg = %q, want the see-[3] hint", m2.statusMsg)
	}
	// Failure must not re-fetch the installed version — only the status-expiry
	// tick rides along.
	assertOnlyExpiryTick(t, cmd)
	out := logx.ReadAllForTesting(logDir)
	if !strings.Contains(out, "rg") || !strings.Contains(out, "brew") {
		t.Errorf("log = %q, want tool and manager recorded", out)
	}
	if strings.Contains(out, "token") {
		t.Errorf("log leaked a token-ish word: %q", out)
	}
}

// TestUpdateDoneFailureEmptyLogSurfacesError: when the update fails before
// emitting any output (missing binary, bad argv, immediate exit), the empty [3]
// log would still read "starting update…". The done handler seeds the log with
// the error so the panel the status bar points to actually shows the reason.
func TestUpdateDoneFailureEmptyLogSurfacesError(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	m := updateDoneModel(t)
	m.updateLog = nil // failed before any chunk arrived

	nm, _ := m.Update(updateDoneMsg{tool: "rg", err: errUpdateTest})
	m2 := nm.(Model)
	if len(m2.updateLog) == 0 {
		t.Fatal("updateLog empty, want the error text seeded for [3]")
	}
	if !strings.Contains(m2.updateLog[0], "exit status 1") {
		t.Errorf("updateLog[0] = %q, want the error text", m2.updateLog[0])
	}
	// [3] must now render the seeded error, not the "starting update…" placeholder.
	if content := m2.renderHelpContent(); !strings.Contains(content, "exit status 1") {
		t.Errorf("[3] content = %q, want the update error surfaced", content)
	}
}

// TestUpdateDoneUntracked: a done for a tool no longer tracked (untracked
// mid-update) just clears the guard — no re-fetch, no crash, no status.
func TestUpdateDoneUntracked(t *testing.T) {
	m := updateDoneModel(t)
	m.updatingFor = "gone"
	nm, cmd := m.Update(updateDoneMsg{tool: "gone", err: nil})
	m2 := nm.(Model)
	if m2.updatingFor != "" {
		t.Errorf("updatingFor = %q, want cleared", m2.updatingFor)
	}
	if cmd != nil {
		t.Error("untracked done must not re-fetch")
	}
	if m2.statusMsg != "" {
		t.Errorf("statusMsg = %q, want empty for an untracked tool", m2.statusMsg)
	}
}

// TestUpdateDoneCursorFollowsTool: after the success re-fetch merges an
// up-to-date installed version, hasUpdate flips off and rg leaves the update
// group — but the selection still points at rg by name, not at a stale index.
func TestUpdateDoneCursorFollowsTool(t *testing.T) {
	m := updateDoneModel(t)
	// rg has an update → sorts to the top; selection is on rg (index 0).
	if got, _ := m.selectedMeta(); got.Name != "rg" {
		t.Fatalf("precondition: selected = %q, want rg", got.Name)
	}
	nm, cmd := m.Update(updateDoneMsg{tool: "rg", err: nil})
	m2 := nm.(Model)
	// Run the re-fetch to get the installedMsg, then feed it the now-current
	// installed version — this flips hasUpdate off and reorders the list.
	if cmd == nil {
		t.Fatal("want a re-fetch command")
	}
	m3 := mustModel(m2.Update(installedMsg{toolName: "rg", installed: "2.0.0"}))
	if m3.hasUpdate("rg") {
		t.Fatal("hasUpdate(rg) still true after installing latest")
	}
	if got, _ := m3.selectedMeta(); got.Name != "rg" {
		t.Errorf("selected = %q after regroup, want the cursor to follow rg", got.Name)
	}
}

var errUpdateTest = updateTestError("exit status 1")

type updateTestError string

func (e updateTestError) Error() string { return string(e) }

// seedReadmeCache writes a warm cache.json entry so fetchReadmeCmd resolves
// from disk instead of the network. HOME must already point at a temp dir
// (version resolves the cache under os.UserConfigDir()).
func seedReadmeCache(t *testing.T, repo, readme string) {
	t.Helper()
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(base, "keepkit"), 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	version.SaveCache(version.Cache{repo: {
		Readme:          readme,
		ReadmeCheckedAt: time.Now(),
	}})
}

// TestFetchReadmeCmdServesCache: a warm cache entry round-trips through the
// command into a readmeMsg without any network access.
func TestFetchReadmeCmdServesCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedReadmeCache(t, "BurntSushi/ripgrep", "# ripgrep\n\nfast search")

	msg, ok := fetchReadmeCmd("BurntSushi/ripgrep", "rg")().(readmeMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T", msg)
	}
	if msg.toolName != "rg" {
		t.Errorf("toolName = %q, want rg", msg.toolName)
	}
	if msg.err != nil {
		t.Errorf("err = %v, want nil", msg.err)
	}
	if !strings.Contains(msg.content, "fast search") {
		t.Errorf("content = %q, want the cached README", msg.content)
	}
}

// TestNeedsReadme pins the fetch predicate: a GitHub ref is required, and any
// stored answer — including a cached negative — counts as answered so cursor
// movement never re-fires the request.
func TestNeedsReadme(t *testing.T) {
	m := newTestModel(focusTools)
	withRepo := loader.Tool{Name: "rg", GitHub: "BurntSushi/ripgrep"}
	noRepo := loader.Tool{Name: "local"}

	if m.needsReadme(noRepo) {
		t.Error("a tool with no GitHub ref must never fetch a README")
	}
	if !m.needsReadme(withRepo) {
		t.Error("an unfetched tool with a repo must fetch")
	}
	m.readmeData["rg"] = readmeMsg{toolName: "rg", err: version.ErrNoReadme}
	if m.needsReadme(withRepo) {
		t.Error("a cached negative must not be retried on every selection move")
	}
}

// TestAutoFetchReadmeModeSkipsHelp: in README mode the auto-fetch must not set
// helpLoadingFor or spawn a --help subprocess (the helpCache branch would also
// index its [2]string out of range with mode 2). It fires the README fetch
// exactly once — a second pass with the answer cached adds no command.
func TestAutoFetchReadmeModeSkipsHelp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New([]loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}})
	m.width, m.height = 80, 24
	m.helpMode = helpModeReadme
	// Pre-fill the other sources so only the README decision varies.
	m.changelogData["rg"] = changelogMsg{toolName: "rg"}
	m.versions["rg"] = VersionInfo{Installed: "14.0.0", InstalledKnown: true, Latest: "14.0.0"}
	m.repoCards["rg"] = version.RepoCard{About: "search"}

	cold := countBatchedCmds(m.autoFetchCmdsForSelected())
	if m.helpLoadingFor != "" {
		t.Errorf("helpLoadingFor = %q, want empty — README mode must not fetch --help", m.helpLoadingFor)
	}
	if cold != 1 {
		t.Fatalf("cold auto-fetch batched %d cmds, want 1 (the README)", cold)
	}

	m.readmeData["rg"] = readmeMsg{toolName: "rg", content: "# rg"}
	if warm := countBatchedCmds(m.autoFetchCmdsForSelected()); warm != 0 {
		t.Errorf("warm auto-fetch batched %d cmds, want 0", warm)
	}
	if m.helpLoadingFor != "" {
		t.Errorf("helpLoadingFor = %q, want empty", m.helpLoadingFor)
	}
}

// TestAutoFetchUpdateLogKeepsPriority: a live update log still owns [3] even in
// README mode — no README fetch is queued while the log is on screen.
func TestAutoFetchUpdateLogKeepsPriority(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New([]loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}})
	m.width, m.height = 80, 24
	m.helpMode = helpModeReadme
	m.changelogData["rg"] = changelogMsg{toolName: "rg"}
	m.versions["rg"] = VersionInfo{Installed: "14.0.0", InstalledKnown: true, Latest: "14.0.0"}
	m.repoCards["rg"] = version.RepoCard{About: "search"}
	m.updateLogFor = "rg"
	m.updateLog = []string{"==> brew upgrade"}

	if n := countBatchedCmds(m.autoFetchCmdsForSelected()); n != 0 {
		t.Errorf("auto-fetch batched %d cmds, want 0 while the update log is live", n)
	}
	if !strings.Contains(m.renderHelpContent(), "brew upgrade") {
		t.Error("the live update log must keep panel [3]")
	}
}

// TestRefreshSelectedForcesReadme: [r] drops the session entry (so a cached 404
// or rate-limit can recover) and adds the forced README fetch to the batch.
func TestRefreshSelectedForcesReadme(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New([]loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}})
	m.width, m.height = 80, 24
	m.readmeData["rg"] = readmeMsg{toolName: "rg", err: version.ErrNoReadme}

	cmd := m.refreshSelectedCmd(loader.Tool{Name: "rg", GitHub: "BurntSushi/ripgrep"})
	if _, still := m.readmeData["rg"]; still {
		t.Error("a force refresh must clear the cached README answer")
	}
	// spinner + installed + remote + changelog + readme
	if n := countBatchedCmds(cmd); n != 5 {
		t.Errorf("refresh batched %d cmds, want 5 (incl. the README)", n)
	}
}

// TestReadmeMsgKeepsKnownContent: a later failure (rate limit, network) must
// not blank a README that already arrived — the same known-content-wins merge
// the repo cards use.
func TestReadmeMsgKeepsKnownContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New([]loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}})
	m.width, m.height = 80, 24

	m = mustModel(m.Update(readmeMsg{toolName: "rg", content: "# ripgrep"}))
	if got := m.readmeData["rg"].content; got != "# ripgrep" {
		t.Fatalf("content = %q, want the fetched README", got)
	}

	m = mustModel(m.Update(readmeMsg{toolName: "rg", err: version.ErrRateLimited}))
	entry := m.readmeData["rg"]
	if entry.content != "# ripgrep" {
		t.Errorf("content = %q, want the known README to survive the failure", entry.content)
	}
	if !errors.Is(entry.err, version.ErrRateLimited) {
		t.Errorf("err = %v, want the failure recorded alongside the content", entry.err)
	}
}

// TestReadmeMsgStoresNegative: with nothing known yet, the error is stored as
// the session answer so the panel can render a specific placeholder.
func TestReadmeMsgStoresNegative(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New([]loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}})
	m.width, m.height = 80, 24

	m = mustModel(m.Update(readmeMsg{toolName: "rg", err: version.ErrNoReadme}))
	if !errors.Is(m.readmeData["rg"].err, version.ErrNoReadme) {
		t.Errorf("readmeData = %+v, want the 404 remembered", m.readmeData["rg"])
	}
}

// TestReadmeMsgRepaintsSelected: an arrival for the selected tool in README
// mode recomputes the panel; one for a hidden mode leaves it alone.
func TestReadmeMsgRepaintsSelected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New([]loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}})
	m.width, m.height = 80, 24
	m.helpMode = helpModeReadme

	// The leading H1 is dropped by the preprocessor (the card prints the tool
	// name), so the body is what proves the render ran.
	m = mustModel(m.Update(readmeMsg{toolName: "rg", content: "# ripgrep\n\nfast search"}))
	if !strings.Contains(stripANSI(m.helpBase), "fast search") {
		t.Errorf("helpBase = %q, want the rendered README", m.helpBase)
	}

	m.helpMode = helpModeHelp
	m.helpBase = ""
	m = mustModel(m.Update(readmeMsg{toolName: "rg", content: "# other"}))
	if m.helpBase != "" {
		t.Errorf("helpBase = %q, want no repaint while README mode is hidden", m.helpBase)
	}
}

// TestInitFetchesReadmeForSelected: startup fires the panel-[3] sources
// directly (not via autoFetchCmdsForSelected), so the README needs its own
// seed — otherwise the default README panel would sit on a placeholder until
// the selection moves. The readme fetch is appended last.
func TestInitFetchesReadmeForSelected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedReadmeCache(t, "BurntSushi/ripgrep", "# ripgrep")

	m := New([]loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}})
	m.width, m.height = 80, 24

	batch, ok := m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatal("Init must batch several commands")
	}
	msg, ok := batch[len(batch)-1]().(readmeMsg)
	if !ok {
		t.Fatalf("last Init command yielded %T, want readmeMsg", msg)
	}
	if msg.toolName != "rg" || !strings.Contains(msg.content, "ripgrep") {
		t.Errorf("readmeMsg = %+v, want the selected tool's cached README", msg)
	}
}

// TestInitSkipsReadmeWithoutRepo: a tool with no GitHub ref has nothing to
// fetch, so Init queues no README command for it.
func TestInitSkipsReadmeWithoutRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New([]loader.ToolMeta{{Name: "localtool"}})
	m.width, m.height = 80, 24

	batch, ok := m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatal("Init must batch several commands")
	}
	// rate + installed; no changelog, no readme — and no --help probe either,
	// because the default panel [3] is the README (see the test below).
	if len(batch) != 2 {
		t.Errorf("Init batched %d cmds, want 2 for a tool with no repo", len(batch))
	}
}

// TestInitHelpProbeFollowsHelpMode: the --help probe spawns a subprocess, so
// startup only pays for it when panel [3] actually shows help. In the default
// README mode the capture would never be rendered.
func TestInitHelpProbeFollowsHelpMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	batchLen := func(mode int) int {
		m := New([]loader.ToolMeta{{Name: "localtool"}})
		m.width, m.height = 80, 24
		m.helpMode = mode
		batch, ok := m.Init()().(tea.BatchMsg)
		if !ok {
			t.Fatal("Init must batch several commands")
		}
		return len(batch)
	}

	readme, help := batchLen(helpModeReadme), batchLen(helpModeHelp)
	if help != readme+1 {
		t.Errorf("Init queued %d cmds in help mode and %d in readme mode, want exactly one more (the --help probe)", help, readme)
	}
}
