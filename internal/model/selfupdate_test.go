package model

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
	"github.com/stanlyzoolo/keepkit/internal/updater"
	"github.com/stanlyzoolo/keepkit/internal/version"
)

// ---- fixtures ----

// selfTestVersion is the running build every self-update test claims: a real
// release tag, so selfCheckEnabled() is on and the feature exists at all.
// selfTestLatest is the newer release the banner offers.
const (
	selfTestVersion = "v0.4.2"
	selfTestLatest  = "v0.5.0"
)

// selfModel is the one fixture of this file: a ready model on a release build
// with the given tracker, terminal width and banner state. The three axes used to
// be frozen in two separate helpers that differed only in which ones they let a
// test pick.
//
// HOME is redirected as well. TestMain's package-wide seams already keep
// meta.yaml, cache.json, the token and the logs off the real config, so this is
// only insurance for anything else that resolves a home directory (updater's go
// bin path, version's brew fallback) — but it is applied in exactly one place
// now, instead of at three of six former fixture sites.
func selfModel(t *testing.T, meta []loader.ToolMeta, width int, state selfState) Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	m := New(meta).WithAppVersion(selfTestVersion)
	m = mustModel(m.Update(tea.WindowSizeMsg{Width: width, Height: 24}))
	m.selfState = state
	m.selfLatest = selfTestLatest
	return m
}

// selfBarTools is the tracker the status-bar tests run against: exactly one
// tool, deliberately not named keepkit, so every "keepkit" in a rendered bar can
// only have come from the self-update banner.
func selfBarTools() []loader.ToolMeta {
	return []loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}}
}

// startedSelfUpdate is selfModel with a self-update already streaming: the
// pipeline state the confirm dialog's enter leaves behind. The banner state is a
// parameter rather than an inherited selfOffered — a completion test that asserts
// the state is untouched must be able to name the state it started from.
func startedSelfUpdate(t *testing.T, meta []loader.ToolMeta, state selfState) Model {
	t.Helper()
	m := selfModel(t, meta, 100, state)
	m.updatingFor = selfToolName
	m.updateLogFor = selfToolName
	m.selfUpdateLog = true
	return m
}

// seedSelfReleaseCache adds a fresh release-only cache entry for keepkit's own
// repo. It is what lets selfCheckCmd actually be executed in this package:
// version.SelfLatest answers from cache.json without a request, and version's
// httptest seam (testAPIBase) is unexported there.
//
// The whole cache is snapshotted and restored, so the entry does not leak into
// the rest of the package (cache.json is shared package-wide by TestMain's
// version.SetConfigDirForTesting) and a co-existing README seed survives.
func seedSelfReleaseCache(t *testing.T, tag string) {
	t.Helper()
	before := version.LoadCache()
	restore := make(version.Cache, len(before))
	for k, v := range before {
		restore[k] = v
	}
	t.Cleanup(func() { version.SaveCache(restore) })

	next := make(version.Cache, len(before)+1)
	for k, v := range before {
		next[k] = v
	}
	entry := next[version.SelfRepo]
	entry.Latest = tag
	entry.ReleaseCheckedAt = time.Now()
	next[version.SelfRepo] = entry
	version.SaveCache(next)
}

// ---- the version gate and the startup check ----

// TestSelfCheckEnabled: only a real release version turns the self-check on. The
// pseudo-version rows are the load-bearing ones: `go build .` / `go install .`
// stamp the module version from VCS since Go 1.24, so a working copy reports a
// pseudo-version (and +dirty with uncommitted changes) rather than "dev" — and
// those canonicalize as pre-releases sorting below every real tag, so a missing
// check makes a developer's own build offer to overwrite itself.
func TestSelfCheckEnabled(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{"unset", "", false},
		{"dev build", "dev", false},
		{"release tag", "v0.4.2", true},
		{"bare numeric", "0.4.2", true},
		{"pseudo-version from a tagless checkout", "v0.0.0-20260725115912-1be4bafa79c8", false},
		{"pseudo-version ahead of a tag", "v0.4.3-0.20260725115912-1be4bafa79c8", false},
		{"pseudo-version above a pre-release", "v0.4.3-rc.1.0.20260725115912-1be4bafa79c8", false},
		{"dirty working copy", "v0.0.0-20260725115912-1be4bafa79c8+dirty", false},
		{"dirty release tag", "v0.4.2+dirty", false},
		{"release tag with a pre-release", "v0.5.0-rc.1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(nil).WithAppVersion(tt.version)
			if got := m.selfCheckEnabled(); got != tt.want {
				t.Errorf("selfCheckEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestInitSelfCheckGatedOnVersion: Init queues the self-check only on a build
// with a real version — and the extra element really is the self-check, which is
// verified by executing it against a warm cache (a live request would be both
// flaky and rude; version's httptest seam is unexported from that package).
func TestInitSelfCheckGatedOnVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedSelfReleaseCache(t, "v9.9.9")

	batch := func(ver string) tea.BatchMsg {
		m := New([]loader.ToolMeta{{Name: "localtool"}}).WithAppVersion(ver)
		m.width, m.height = 80, 24
		msgs, ok := m.Init()().(tea.BatchMsg)
		if !ok {
			t.Fatal("Init must batch several commands")
		}
		return msgs
	}

	base := len(batch(""))
	for _, ver := range []string{"dev", "v0.0.0-20260725115912-1be4bafa79c8"} {
		if got := len(batch(ver)); got != base {
			t.Errorf("Init queued %d cmds on version %q and %d with no version, want no self-check", got, ver, base)
		}
	}

	release := batch("v0.4.2")
	if len(release) != base+1 {
		t.Fatalf("Init queued %d cmds on a release build and %d with no version, want exactly one more", len(release), base)
	}
	// The self-check rides right after the rate seed, i.e. at index 1.
	msg, ok := release[1]().(selfCheckMsg)
	if !ok {
		t.Fatalf("Init batch element 1 produced %T, want the selfCheckMsg", release[1]())
	}
	if msg.err != nil || msg.latest != "v9.9.9" {
		t.Errorf("selfCheckMsg = %+v, want the cached tag with no error", msg)
	}
}

// TestInitSelfCheckIsNotLast: the README seed has to stay the last element of
// the batch (TestInitFetchesReadmeForSelected executes it), so the self-check
// rides right after the rate seed at the front.
func TestInitSelfCheckIsNotLast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seedReadmeCache(t, "BurntSushi/ripgrep", "# ripgrep")

	m := New([]loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}}).WithAppVersion("v0.4.2")
	m.width, m.height = 80, 24

	batch, ok := m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatal("Init must batch several commands")
	}
	if _, ok := batch[len(batch)-1]().(readmeMsg); !ok {
		t.Error("last Init command is no longer the README seed — the self-check must not be appended last")
	}
}

// TestSelfCheckCmdServesCache: the command itself (not just Init's batch shape)
// round-trips a cached tag into a selfCheckMsg with no error — the nil-error path
// is also the one that must not write a session log.
func TestSelfCheckCmdServesCache(t *testing.T) {
	seedSelfReleaseCache(t, "v9.9.9")
	stamp := version.LoadCache()[version.SelfRepo].ReleaseCheckedAt

	msg, ok := selfCheckCmd()().(selfCheckMsg)
	if !ok {
		t.Fatalf("selfCheckCmd produced %T, want selfCheckMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("selfCheckMsg.err = %v, want nil from a warm cache", msg.err)
	}
	if msg.latest != "v9.9.9" {
		t.Errorf("selfCheckMsg.latest = %q, want v9.9.9", msg.latest)
	}
	// Hermeticity: any request would have re-stamped the entry (a conclusive
	// answer) — the seed must have answered on its own, with no network access.
	if got := version.LoadCache()[version.SelfRepo].ReleaseCheckedAt; !got.Equal(stamp) {
		t.Errorf("ReleaseCheckedAt = %v, want the seed's %v (the cache must answer without a request)", got, stamp)
	}

	// End to end through the handler: a newer tag than the running build raises
	// the offer with that tag.
	m := New([]loader.ToolMeta{{Name: "rg"}}).WithAppVersion("v0.4.2")
	m.width, m.height = 80, 24
	m = mustModel(m.Update(msg))
	if m.selfState != selfOffered || m.selfLatest != "v9.9.9" {
		t.Errorf("selfState = %v, selfLatest = %q, want the offer for v9.9.9", m.selfState, m.selfLatest)
	}
}

// TestSelfCheckMsgStates: the handler is the only place the running binary's
// version is compared against the release tag, and only a strictly newer tag
// raises the banner.
func TestSelfCheckMsgStates(t *testing.T) {
	tests := []struct {
		name       string
		appVersion string
		msg        selfCheckMsg
		wantState  selfState
		wantLatest string
	}{
		{
			name:       "newer release offers the update",
			appVersion: "v0.4.2",
			msg:        selfCheckMsg{latest: "v0.5.0"},
			wantState:  selfOffered,
			wantLatest: "v0.5.0",
		},
		{
			name:       "same version stays quiet",
			appVersion: "v0.5.0",
			msg:        selfCheckMsg{latest: "v0.5.0"},
			wantState:  selfNone,
		},
		{
			name:       "older release stays quiet",
			appVersion: "v0.6.0",
			msg:        selfCheckMsg{latest: "v0.5.0"},
			wantState:  selfNone,
		},
		{
			name:       "unparsable tag stays quiet",
			appVersion: "v0.4.2",
			msg:        selfCheckMsg{latest: "nightly"},
			wantState:  selfNone,
		},
		{
			name:       "unparsable running version stays quiet",
			appVersion: "some-build",
			msg:        selfCheckMsg{latest: "v0.5.0"},
			wantState:  selfNone,
		},
		{
			name:       "no release published stays quiet",
			appVersion: "v0.4.2",
			msg:        selfCheckMsg{},
			wantState:  selfNone,
		},
		{
			name:       "rate limit stays quiet",
			appVersion: "v0.4.2",
			msg:        selfCheckMsg{err: version.ErrRateLimited},
			wantState:  selfNone,
		},
		{
			// The error alone must disqualify the message: a tag arriving
			// alongside one is not an answer this build can act on.
			name:       "a tag with an error stays quiet",
			appVersion: "v0.4.2",
			msg:        selfCheckMsg{latest: "v9.9.9", err: version.ErrRateLimited},
			wantState:  selfNone,
		},
		{
			name:       "transient failure stays quiet",
			appVersion: "v0.4.2",
			msg:        selfCheckMsg{err: errors.New("dial tcp: no route to host")},
			wantState:  selfNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New([]loader.ToolMeta{{Name: "localtool"}}).WithAppVersion(tt.appVersion)
			m.width, m.height = 80, 24

			m = mustModel(m.Update(tt.msg))
			if m.selfState != tt.wantState {
				t.Errorf("selfState = %v, want %v", m.selfState, tt.wantState)
			}
			if m.selfLatest != tt.wantLatest {
				t.Errorf("selfLatest = %q, want %q", m.selfLatest, tt.wantLatest)
			}
		})
	}
}

// TestSelfCheckMsgKeepsActedOnState: the message arrives once, but the handler
// must not be a way for it to walk back a state the user already acted on — and
// that holds for a *newer* tag too, which is the answer that actually writes.
func TestSelfCheckMsgKeepsActedOnState(t *testing.T) {
	for _, state := range []selfState{selfDismissed, selfUpdated, selfUpdatedLater} {
		for _, msg := range []selfCheckMsg{{latest: "v0.4.2"}, {latest: "v9.9.9"}} {
			m := New([]loader.ToolMeta{{Name: "localtool"}}).WithAppVersion("v0.4.2")
			m.width, m.height = 80, 24
			m.selfState = state
			m.selfLatest = "v0.5.0"

			m = mustModel(m.Update(msg))
			if m.selfState != state {
				t.Errorf("latest %q: selfState = %v, want %v to survive", msg.latest, m.selfState, state)
			}
			if m.selfLatest != "v0.5.0" {
				t.Errorf("latest %q: selfLatest = %q, want the acted-on tag kept", msg.latest, m.selfLatest)
			}
		}
	}
}

// ---- the selfState machine and what it renders ----

// TestSelfStateSitesAreExhaustive walks every selfState through the five sites
// that switch on it. .golangci.yml carries no exhaustiveness linter, so a sixth
// member would otherwise compile with some of those sites silently missing it —
// the row-count check against selfStateCount is what makes adding one fail here
// first, and the per-site columns say which sites it has to be decided at.
func TestSelfStateSitesAreExhaustive(t *testing.T) {
	tests := []struct {
		state       selfState
		wantBanner  bool // selfBannerCells replaces the hints with a full banner
		wantCompact bool // selfCompactCell renders the folded right-group cell
		wantHotkeys bool // the [?] overlay carries a Self group
		wantU       bool // [U] is bound (a command, or the restart flag)
	}{
		{state: selfNone},
		{state: selfOffered, wantBanner: true, wantHotkeys: true, wantU: true},
		{state: selfDismissed, wantCompact: true, wantHotkeys: true, wantU: true},
		{state: selfUpdated, wantBanner: true, wantHotkeys: true, wantU: true},
		{state: selfUpdatedLater, wantCompact: true, wantHotkeys: true, wantU: true},
	}
	if len(tests) != int(selfStateCount) {
		t.Fatalf("table has %d rows, want one per selfState (%d): a new state must be decided at all five sites",
			len(tests), selfStateCount)
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(int(tt.state)), func(t *testing.T) {
			m := selfModel(t, selfBarTools(), 100, tt.state)
			if _, full := m.selfBannerCells(); full != tt.wantBanner {
				t.Errorf("selfBannerCells full = %v, want %v", full, tt.wantBanner)
			}
			if got := m.selfCompactCell() != ""; got != tt.wantCompact {
				t.Errorf("selfCompactCell present = %v, want %v", got, tt.wantCompact)
			}
			hk := mustModel(m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}))
			// The group header is lowercase like every other one, so match on a
			// row only it can produce rather than on the word "self", which the
			// version line in the title also carries.
			overlay := hk.renderHotkeys()
			hasGroup := strings.Contains(overlay, "self-update") || strings.Contains(overlay, "restart")
			if hasGroup != tt.wantHotkeys {
				t.Errorf("[?] self group present = %v, want %v", hasGroup, tt.wantHotkeys)
			}
			u := m
			cmd := u.selfUpdateKey()
			if got := cmd != nil || u.restartRequested; got != tt.wantU {
				t.Errorf("[U] bound = %v, want %v", got, tt.wantU)
			}
			// [X] has no per-state requirement — it is a legitimate no-op at three
			// of the five — but it must never panic or invent a state.
			x := mustModel(m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}}))
			if x.selfState >= selfStateCount {
				t.Errorf("[X] left selfState = %d, outside the enum", x.selfState)
			}
		})
	}
}

// TestSelfUpdatingPredicate: "a self-update is in flight" is derived from the
// update pipeline's own state, not a selfState member, and every update of
// keepkit counts whichever key started it — so the target name decides which kind
// of update it is, and the version gate decides whether the kind exists at all: on
// a build with the feature off an update of keepkit is a plain tool update.
func TestSelfUpdatingPredicate(t *testing.T) {
	tests := []struct {
		name        string
		appVersion  string
		updatingFor string
		want        bool
	}{
		{"idle", "v0.4.2", "", false},
		{"self update running", "v0.4.2", selfToolName, true},
		{"another tool updating", "v0.4.2", "rg", false},
		{"keepkit updating on a dev build", "dev", selfToolName, false},
		{"keepkit updating on a pseudo-version build", "v0.0.0-20260725115912-1be4bafa79c8", selfToolName, false},
		{"keepkit updating with no version injected", "", selfToolName, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(nil).WithAppVersion(tt.appVersion)
			m.updatingFor = tt.updatingFor
			if got := m.selfUpdating(); got != tt.want {
				t.Errorf("selfUpdating() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSelfBannerInStatusBar walks the state machine's rendered surfaces: the two
// full banners replace the hint cells, the two collapsed states keep the hints
// and add a compact cell, and a running self-update shows neither banner (the
// hints stay usable while the log streams into [3]).
func TestSelfBannerInStatusBar(t *testing.T) {
	tests := []struct {
		name     string
		state    selfState
		updating bool
		want     []string
		absent   []string
		// wantCell is the expected selfCompactCell(): empty while a full banner
		// is up, so the bar can never advertise [U] twice.
		wantCell string
	}{
		{
			name:   "no banner",
			state:  selfNone,
			want:   []string{"t track"},
			absent: []string{"keepkit"},
		},
		{
			name:   "offer replaces the hints",
			state:  selfOffered,
			want:   []string{"keepkit v0.5.0 available", "U update", "X dismiss"},
			absent: []string{"t track"},
		},
		{
			name:     "dismissed keeps the hints and folds to a cell",
			state:    selfDismissed,
			want:     []string{"t track", "keepkit ↑ U"},
			absent:   []string{"available", "dismiss"},
			wantCell: "keepkit ↑ U",
		},
		{
			name:   "updated offers the restart",
			state:  selfUpdated,
			want:   []string{"keepkit updated", "U restart", "X later"},
			absent: []string{"t track", "available"},
		},
		{
			name:     "restart later folds to a cell",
			state:    selfUpdatedLater,
			want:     []string{"t track", "keepkit U restart"},
			absent:   []string{"later", "available"},
			wantCell: "keepkit U restart",
		},
		{
			name:     "in flight shows neither banner",
			state:    selfOffered,
			updating: true,
			want:     []string{"t track", "keepkit updating…"},
			absent:   []string{"available", "X dismiss"},
			wantCell: "keepkit updating…",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := selfModel(t, selfBarTools(), 120, tt.state)
			if tt.updating {
				m.updatingFor = selfToolName
				m.selfUpdateLog = true
			}
			if got := stripANSI(m.selfCompactCell()); got != tt.wantCell {
				t.Errorf("selfCompactCell() = %q, want %q", got, tt.wantCell)
			}
			bar := stripANSI(m.renderStatusBar())
			for _, want := range tt.want {
				if !strings.Contains(bar, want) {
					t.Errorf("status bar = %q, missing %q", bar, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(bar, absent) {
					t.Errorf("status bar = %q, should not contain %q", bar, absent)
				}
			}
		})
	}
}

// TestSelfBannerInEveryNormalFocus: the banner is a global announcement, not a
// panel action, so it replaces the hints in all three normal focus states.
func TestSelfBannerInEveryNormalFocus(t *testing.T) {
	for _, focus := range []int{focusTools, focusBrief, focusHelp} {
		m := selfModel(t, selfBarTools(), 120, selfOffered)
		m.focus = focus
		bar := stripANSI(m.renderStatusBar())
		if !strings.Contains(bar, "keepkit v0.5.0 available") || !strings.Contains(bar, "U update") {
			t.Errorf("focus %d bar = %q, want the self-update banner", focus, bar)
		}
	}
}

// TestSelfBannerYieldsToStatusMsg: the branch order is statusMsg > banner >
// hints. A transient status expires on its own (statusMsgTTL), so the banner
// comes back without any bookkeeping.
func TestSelfBannerYieldsToStatusMsg(t *testing.T) {
	m := selfModel(t, selfBarTools(), 120, selfOffered)
	m.statusMsg = "no repo for rg"
	bar := stripANSI(m.renderStatusBar())
	if !strings.Contains(bar, "no repo for rg") {
		t.Errorf("status bar = %q, want the transient message", bar)
	}
	if strings.Contains(bar, "keepkit") {
		t.Errorf("status bar = %q, want the banner suppressed under a status message", bar)
	}
}

// TestSelfCompactCellRightGroupDegradation: the compact cell and the gauge share
// the right corner. Wide enough for both, they both render; when they no longer
// fit together the actionable cell is the one that stays.
func TestSelfCompactCellRightGroupDegradation(t *testing.T) {
	rate := version.RateLimit{Known: true, Limit: 60, Remaining: 42}

	wide := selfModel(t, selfBarTools(), 160, selfDismissed)
	wide.rate = rate
	bar := stripANSI(wide.renderStatusBar())
	if !strings.Contains(bar, gaugeFillGlyph) || !strings.Contains(bar, "keepkit ↑ U") {
		t.Errorf("wide status bar = %q, want the full gauge and the self cell", bar)
	}

	// Wide enough for the hints plus the cell, too narrow for even the compact
	// gauge beside it. The exact number tracks the hint bar's width, which
	// shrank when the key hints dropped their brackets.
	tight := selfModel(t, selfBarTools(), 88, selfDismissed)
	tight.rate = rate
	bar = stripANSI(tight.renderStatusBar())
	if !strings.Contains(bar, "keepkit ↑ U") {
		t.Errorf("tight status bar = %q, want the self cell to outrank the gauge", bar)
	}
	if strings.Contains(bar, "18/60") {
		t.Errorf("tight status bar = %q, want no gauge next to the self cell", bar)
	}
}

// TestSelfCompactCellFitsBaselineTerminal: at the 80x24 baseline the hints alone
// already fill the bar, so the collapsed cell only appears if it outranks the
// trailing reminders — and it has to: after [X] it is the feature's one visible
// surface for the rest of the session, and the gauge it displaces is read-only.
func TestSelfCompactCellFitsBaselineTerminal(t *testing.T) {
	for _, focus := range []int{focusTools, focusBrief, focusHelp} {
		m := selfModel(t, selfBarTools(), 80, selfDismissed)
		m.focus = focus
		m.rate = version.RateLimit{Known: true, Limit: 60, Remaining: 42}

		bar := stripANSI(m.renderStatusBar())
		if !strings.Contains(bar, "keepkit ↑ U") {
			t.Errorf("focus %d: 80-col bar = %q, want the collapsed self cell", focus, bar)
		}
		// A dropped hint cell is the price; the leading one must survive.
		if lines := strings.Count(bar, "\n"); lines > 2 {
			t.Errorf("focus %d: bar wrapped to %d lines: %q", focus, lines+1, bar)
		}
	}
}

// TestRateGaugeUnaffectedWithoutSelfCell: with no banner the right group is the
// gauge alone and its full → compact → hidden degradation is unchanged.
func TestRateGaugeUnaffectedWithoutSelfCell(t *testing.T) {
	m := selfModel(t, selfBarTools(), 160, selfNone)
	m.rate = version.RateLimit{Known: true, Limit: 60, Remaining: 42}
	if bar := stripANSI(m.renderStatusBar()); !strings.Contains(bar, gaugeFillGlyph) {
		t.Errorf("status bar = %q, want the full gauge", bar)
	}

	// Narrow enough that the bar no longer fits beside the hints, wide enough
	// for the numbers.
	m = selfModel(t, selfBarTools(), 92, selfNone)
	m.rate = version.RateLimit{Known: true, Limit: 60, Remaining: 42}
	bar := stripANSI(m.renderStatusBar())
	if !strings.Contains(bar, "api 18/60") {
		t.Errorf("status bar = %q, want the compact gauge", bar)
	}
	if strings.Contains(bar, gaugeFillGlyph) {
		t.Errorf("status bar = %q, want the bar dropped at this width", bar)
	}
}

// ---- the [U] / [X] keys ----

// TestSelfKeys is the single (state, key) → (next state, command, restart flag)
// table for the banner's two action keys. [X] folds a banner into its compact
// cell and is a no-op with nothing to fold; [U] never moves the state itself —
// the update pipeline and the restart do, so pressing it again is a retry — and
// it is the only key that may raise the restart flag main re-execs on, which is
// why the plain quit key is a row here too.
func TestSelfKeys(t *testing.T) {
	// The command [U] returns, by kind. A detect command is deliberately not
	// executed: driving updater.Detect would spawn subprocesses and resolve the
	// developer's own keepkit — TestSelfUpdateKeyDetectsSelf executes it under an
	// update_cmd override instead.
	const (
		noCmd  = ""
		detect = "detect"
		quit   = "quit"
	)
	tests := []struct {
		name        string
		key         string
		state       selfState
		wantState   selfState
		wantCmd     string
		wantRestart bool
	}{
		{name: "X folds the offer", key: "X", state: selfOffered, wantState: selfDismissed},
		{name: "X defers the restart", key: "X", state: selfUpdated, wantState: selfUpdatedLater},
		{name: "X with no banner", key: "X", state: selfNone, wantState: selfNone},
		{name: "X on the folded offer", key: "X", state: selfDismissed, wantState: selfDismissed},
		{name: "X on the folded restart", key: "X", state: selfUpdatedLater, wantState: selfUpdatedLater},
		{name: "U with no banner", key: "U", state: selfNone, wantState: selfNone},
		{name: "U acts on the offer", key: "U", state: selfOffered, wantState: selfOffered, wantCmd: detect},
		{name: "U acts on the folded offer", key: "U", state: selfDismissed, wantState: selfDismissed, wantCmd: detect},
		{name: "U restarts", key: "U", state: selfUpdated, wantState: selfUpdated, wantCmd: quit, wantRestart: true},
		{
			name: "U restarts from the folded cell", key: "U", state: selfUpdatedLater,
			wantState: selfUpdatedLater, wantCmd: quit, wantRestart: true,
		},
		// The ordinary quit key quits without ever making main re-exec.
		{name: "q after a restart offer", key: "q", state: selfUpdated, wantState: selfUpdated, wantCmd: quit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := selfModel(t, selfBarTools(), 120, tt.state)
			if m.RestartRequested() {
				t.Fatal("RestartRequested() = true before the key")
			}
			updated, cmd := m.Update(keyRunes(tt.key))
			m = updated.(Model)
			if m.selfState != tt.wantState {
				t.Errorf("selfState = %v, want %v", m.selfState, tt.wantState)
			}
			if got := m.RestartRequested(); got != tt.wantRestart {
				t.Errorf("RestartRequested() = %v, want %v", got, tt.wantRestart)
			}
			switch tt.wantCmd {
			case noCmd:
				if cmd != nil {
					t.Errorf("cmd produced %T, want no command", cmd())
				}
			case quit:
				if cmd == nil {
					t.Fatal("cmd = nil, want tea.Quit")
				}
				if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
					t.Errorf("cmd produced %T, want tea.QuitMsg", cmd())
				}
			case detect:
				if cmd == nil {
					t.Fatal("cmd = nil, want the self detection")
				}
			}
		})
	}
}

// TestSelfUpdateKeyDetectsSelf executes the command [U] returns: it must be a
// *self*-tagged detection of keepkit. Without this the two mutations that kill
// the feature's main case both survive — a detection of the selected tool, or one
// missing the self flag, is dropped by acceptsUpdateDetect's selection check
// whenever keepkit is untracked, leaving [U] silently dead forever.
//
// Both halves keep updater.Detect subprocess-free: an update_cmd short-circuits
// detection entirely, and a name that cannot be on PATH answers from the LookPath
// miss with ErrUnknownManager.
func TestSelfUpdateKeyDetectsSelf(t *testing.T) {
	m := selfModel(t, selfBarTools(), 120, selfOffered)
	m.meta = []loader.ToolMeta{{Name: selfToolName, UpdateCmd: "true"}}
	m.tools = loader.ToolsFromMeta(m.meta)

	_, cmd := m.Update(keyRunes("U"))
	if cmd == nil {
		t.Fatal("[U] returned no command, want the self detection")
	}
	msg, ok := cmd().(updateDetectedMsg)
	if !ok {
		t.Fatalf("[U] command produced %T, want updateDetectedMsg", cmd())
	}
	if !msg.self {
		t.Error("updateDetectedMsg.self = false — an untracked keepkit's result would be dropped")
	}
	if msg.tool != selfToolName {
		t.Errorf("updateDetectedMsg.tool = %q, want %q", msg.tool, selfToolName)
	}

	// The command itself, driven directly with a name no PATH can resolve: no
	// subprocess, and the self tag still rides along.
	direct, ok := detectUpdateCmd(loader.Tool{Name: "keepkit-no-such-binary"}, true)().(updateDetectedMsg)
	if !ok {
		t.Fatalf("detectUpdateCmd produced %T, want updateDetectedMsg", direct)
	}
	if !direct.self || direct.tool != "keepkit-no-such-binary" {
		t.Errorf("detectUpdateCmd msg = %+v, want it tagged self for the given tool", direct)
	}
	if !errors.Is(direct.err, updater.ErrUnknownManager) {
		t.Errorf("detectUpdateCmd err = %v, want ErrUnknownManager for a name off PATH", direct.err)
	}
}

// TestSelfUpdateKeyInertWithoutBanner: at selfNone the key is unbound, and the
// busy refusal must never leak out ahead of that. Order matters because selfNone
// is the *only* state a dev build ever has: a `U` pressed during an ordinary tool
// update there used to answer "another update is running", which is a self-update
// key announcing itself on a build where the whole feature — request, banner,
// restart offer — is documented as absent.
//
// With a banner up the refusal is the point: one update at a time in both
// directions, reported rather than silent, because the banner on screen still
// advertises [U] and a tool update's only other indicator is a card spinner
// invisible unless that tool is selected.
func TestSelfUpdateKeyInertWithoutBanner(t *testing.T) {
	tests := []struct {
		name string
		// appVersion overrides selfModel's release version; empty keeps it.
		appVersion string
		state      selfState
		updating   string
		wantBusy   bool
	}{
		{name: "no banner, idle", state: selfNone},
		{name: "no banner, tool update running", state: selfNone, updating: "rg"},
		{name: "dev build, tool update running", appVersion: "dev", state: selfNone, updating: "rg"},
		{
			name:       "pseudo-version build, tool update running",
			appVersion: "v0.0.0-20260725115912-1be4bafa79c8",
			state:      selfNone,
			updating:   "rg",
		},
		{name: "offer blocked", state: selfOffered, updating: "rg", wantBusy: true},
		// The guard is one field in both directions: a self-update already
		// running blocks a second [U] exactly as a tool update does.
		{name: "offer blocked by a running self-update", state: selfOffered, updating: selfToolName, wantBusy: true},
		{name: "folded offer blocked", state: selfDismissed, updating: "rg", wantBusy: true},
		{name: "restart offer blocked", state: selfUpdated, updating: "rg", wantBusy: true},
		{name: "folded restart offer blocked", state: selfUpdatedLater, updating: "rg", wantBusy: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shrinkStatusTTL(t)
			m := selfModel(t, selfBarTools(), 120, tt.state)
			if tt.appVersion != "" {
				m = m.WithAppVersion(tt.appVersion)
			}
			m.updatingFor = tt.updating

			updated, cmd := m.Update(keyRunes("U"))
			got := updated.(Model)
			if got.selfState != tt.state {
				t.Errorf("selfState = %v, want %v (a blocked or unbound U moves nothing)", got.selfState, tt.state)
			}
			if got.RestartRequested() {
				t.Error("RestartRequested() = true, want the key to have done nothing")
			}
			if !tt.wantBusy {
				if got.statusMsg != "" {
					t.Errorf("statusMsg = %q, want silence — there is no banner to act on", got.statusMsg)
				}
				if cmd != nil {
					t.Errorf("cmd = %T, want none", cmd())
				}
				return
			}
			if got.statusMsg != updateBusyStatus {
				t.Errorf("statusMsg = %q, want %q", got.statusMsg, updateBusyStatus)
			}
			// Only the status expiry rides along — no detection was started.
			assertOnlyExpiryTick(t, cmd)
		})
	}
}

// TestToolUpdateKeyBlockedBySelfUpdate: one update at a time in the other
// direction too — [u] on a tool with a pending release must not start a second
// one while keepkit updates itself. Same updatingFor guard, mirrored by
// TestSelfUpdateKeyInertWithoutBanner's blocked rows for [U] under one.
func TestToolUpdateKeyBlockedBySelfUpdate(t *testing.T) {
	shrinkStatusTTL(t)
	m := startedSelfUpdate(t, []loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}}, selfOffered)
	m.focus = focusBrief
	m.versions["rg"] = VersionInfo{
		Installed:        "0.1.0",
		Latest:           "1.0.0",
		InstalledKnown:   true,
		InstalledPresent: true,
	}
	if !m.hasUpdate("rg") {
		t.Fatal("fixture: rg must have a pending update for [u] to be live")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.statusMsg != updateBusyStatus {
		t.Errorf("statusMsg = %q, want %q", got.statusMsg, updateBusyStatus)
	}
	// No detection started — only the status expiry rides along.
	assertOnlyExpiryTick(t, cmd)
	if got.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal (no confirm dialog)", got.mode)
	}
	if got.updatingFor != selfToolName || got.updateLogFor != selfToolName || !got.selfUpdating() {
		t.Errorf("updatingFor = %q, updateLogFor = %q, selfUpdating() = %v, want the self-update untouched",
			got.updatingFor, got.updateLogFor, got.selfUpdating())
	}
}

// TestSelfKeysAreNormalModeOnly: U and X are bound in the normal-mode switch, so
// every input mode's handler owns them first — a capital letter typed into the
// tool-list search is query text, and an open overlay swallows it.
func TestSelfKeysAreNormalModeOnly(t *testing.T) {
	m := selfModel(t, selfBarTools(), 120, selfOffered)
	m = mustModel(m.Update(keyRunes("/")))
	m = mustModel(m.Update(keyRunes("X")))
	if m.selfState != selfOffered {
		t.Errorf("selfState = %v, want the offer untouched by a searched X", m.selfState)
	}
	if got := m.search.Value(); got != "X" {
		t.Errorf("search query = %q, want the literal X", got)
	}

	m = selfModel(t, selfBarTools(), 120, selfUpdated)
	m = mustModel(m.Update(keyRunes("?")))
	m = mustModel(m.Update(keyRunes("X")))
	if m.selfState != selfUpdated || m.mode != modeHotkeys {
		t.Errorf("selfState = %v, mode = %v, want X swallowed by the hotkeys overlay", m.selfState, m.mode)
	}
}

// ---- detection, the confirm dialog and its bar ----

// TestSelfToolInheritsTrackedEntry: the detection target is the tracked entry
// when there is one, so an update_cmd override governs [U] exactly as it governs
// [u] on that row; otherwise it is synthesized from the constants.
func TestSelfToolInheritsTrackedEntry(t *testing.T) {
	tracked := New([]loader.ToolMeta{
		{Name: "rg"},
		{Name: selfToolName, GitHub: version.SelfRepo, UpdateCmd: "brew upgrade keepkit"},
	}).selfTool()
	if tracked.Name != selfToolName || tracked.UpdateCmd != "brew upgrade keepkit" {
		t.Errorf("selfTool() = %#v, want the tracked entry with its update_cmd", tracked)
	}

	// The synthesized entry's GitHub field carries the host-prefixed form every
	// tracked tool has, not the bare owner/repo of version.SelfRepo.
	synthetic := New([]loader.ToolMeta{{Name: "rg"}}).selfTool()
	if synthetic.Name != selfToolName || synthetic.GitHub != "github.com/"+version.SelfRepo || synthetic.UpdateCmd != "" {
		t.Errorf("selfTool() = %#v, want a synthesized keepkit entry", synthetic)
	}
	if loader.NormalizeRepo(synthetic.GitHub) != version.SelfRepo {
		t.Errorf("NormalizeRepo(%q) = %q, want %q",
			synthetic.GitHub, loader.NormalizeRepo(synthetic.GitHub), version.SelfRepo)
	}
}

// TestSelfDetectedAcceptance: a self detection result has no selection to match
// (keepkit is typically untracked, the tracker may be empty), so it is gated on
// the input mode and the update guard instead. A dropped result must leave the
// offer intact — [U] is the retry.
func TestSelfDetectedAcceptance(t *testing.T) {
	plan := updater.Plan{Manager: "brew", Argv: []string{"brew", "upgrade", "keepkit"}, Display: "brew upgrade keepkit"}
	tests := []struct {
		name        string
		meta        []loader.ToolMeta
		mode        inputMode
		updatingFor string
		wantConfirm bool
	}{
		{
			name:        "another tool selected",
			meta:        []loader.ToolMeta{{Name: "rg"}},
			wantConfirm: true,
		},
		{
			name:        "empty tracker",
			wantConfirm: true,
		},
		{
			name: "note editor owns the input",
			meta: []loader.ToolMeta{{Name: "rg"}},
			mode: modeEditNote,
		},
		{
			name: "hotkeys overlay owns the input",
			meta: []loader.ToolMeta{{Name: "rg"}},
			mode: modeHotkeys,
		},
		{
			name:        "a tool update is already running",
			meta:        []loader.ToolMeta{{Name: "rg"}},
			updatingFor: "rg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := selfModel(t, tt.meta, 100, selfOffered)
			m.mode = tt.mode
			m.updatingFor = tt.updatingFor

			m = mustModel(m.Update(updateDetectedMsg{tool: selfToolName, plan: plan, self: true}))
			if tt.wantConfirm {
				if m.mode != modeConfirmUpdate {
					t.Fatalf("mode = %v, want modeConfirmUpdate", m.mode)
				}
				if m.updateTarget != selfToolName {
					t.Errorf("updateTarget = %q, want %q", m.updateTarget, selfToolName)
				}
				if m.updatePlan.Display != plan.Display {
					t.Errorf("updatePlan.Display = %q, want %q", m.updatePlan.Display, plan.Display)
				}
				return
			}
			if m.mode != tt.mode {
				t.Errorf("mode = %v, want the dropped result to leave %v", m.mode, tt.mode)
			}
			if m.updateTarget != "" || m.updatePlan.Display != "" {
				t.Errorf("updateTarget = %q, plan = %q, want no plan stored", m.updateTarget, m.updatePlan.Display)
			}
			if m.selfState != selfOffered {
				t.Errorf("selfState = %v, want the offer left as a retry", m.selfState)
			}
		})
	}
}

// TestSelfDetectedUnknownManager: a hand-installed binary has no manager to
// drive, which is a hint and not a dead-end dialog — the offer stays up. The
// wording is chosen by what the target *is* (isSelfUpdate), not by which key
// asked: keepkit's manual route is whatever installed it, a tool's is update_cmd,
// and the identical failure of the identical binary must not read two ways
// depending on whether [U] or [u] started it.
func TestSelfDetectedUnknownManager(t *testing.T) {
	tests := []struct {
		name       string
		appVersion string
		self       bool
		want       string
	}{
		{name: "U", appVersion: "v0.4.2", self: true, want: "manually"},
		// [u] on a tracked keepkit row is the same self-update, so it gets the
		// same wording even though msg.self is false.
		{name: "u on a tracked keepkit row", appVersion: "v0.4.2", want: "manually"},
		// ...but only where the feature is live at all: on a dev build that press
		// is a plain tool update and gets the tool wording.
		{name: "u on a dev build", appVersion: "v0.0.0-20260725115912-1be4bafa79c8", want: "update_cmd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := selfModel(t, []loader.ToolMeta{{Name: selfToolName}}, 100, selfOffered)
			m.appVersion = tt.appVersion

			m = mustModel(m.Update(updateDetectedMsg{tool: selfToolName, err: updater.ErrUnknownManager, self: tt.self}))
			if m.mode != modeNormal {
				t.Fatalf("mode = %v, want modeNormal (no dialog)", m.mode)
			}
			if !strings.Contains(m.statusMsg, "no known updater for "+selfToolName) {
				t.Errorf("statusMsg = %q, want it to name %s", m.statusMsg, selfToolName)
			}
			if !strings.Contains(m.statusMsg, tt.want) {
				t.Errorf("statusMsg = %q, want the %q route", m.statusMsg, tt.want)
			}
			if m.selfState != selfOffered {
				t.Errorf("selfState = %v, want the offer left as a retry", m.selfState)
			}
		})
	}
}

// TestSelfConfirmEnterStartsWithEmptyTracker: the target comes from the plan, not
// from the selection, so enter starts the update even with nothing to select —
// reading it off selectedMeta would silently cancel exactly where the feature is
// needed most.
func TestSelfConfirmEnterStartsWithEmptyTracker(t *testing.T) {
	m := selfModel(t, nil, 100, selfOffered)
	m.mode = modeConfirmUpdate
	m.updatePlan = updater.Plan{Manager: "brew", Argv: []string{"true"}, Display: "brew upgrade keepkit"}
	m.updateTarget = selfToolName
	m.updateLog = []string{"stale output from a previous run"}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal after enter", m.mode)
	}
	if m.updatingFor != selfToolName || m.updateLogFor != selfToolName {
		t.Errorf("updatingFor = %q, updateLogFor = %q, want both %q", m.updatingFor, m.updateLogFor, selfToolName)
	}
	if !m.selfUpdateLog || !m.selfUpdating() {
		t.Errorf("selfUpdateLog = %v, selfUpdating() = %v, want the self log marked live", m.selfUpdateLog, m.selfUpdating())
	}
	if len(m.updateLog) != 0 {
		t.Errorf("updateLog = %v, want reset to empty", m.updateLog)
	}
	if cmd == nil {
		t.Error("cmd = nil, want the start+spinner batch")
	}
}

// TestSelfConfirmCancelClearsTarget: the target names a plan awaiting
// confirmation, so a cancelled dialog must not leave it set for the next one.
func TestSelfConfirmCancelClearsTarget(t *testing.T) {
	m := selfModel(t, []loader.ToolMeta{{Name: "rg"}}, 100, selfOffered)
	m.mode = modeConfirmUpdate
	m.updatePlan = updater.Plan{Argv: []string{"true"}, Display: "brew upgrade keepkit"}
	m.updateTarget = selfToolName

	m = mustModel(m.Update(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal", m.mode)
	}
	if m.updateTarget != "" {
		t.Errorf("updateTarget = %q after cancel, want it cleared", m.updateTarget)
	}
	if m.updatingFor != "" || m.selfUpdateLog {
		t.Errorf("updatingFor = %q, selfUpdateLog = %v, want nothing started", m.updatingFor, m.selfUpdateLog)
	}
}

// TestToolConfirmEnterReleasesSelfLog: a tool update takes the log buffer over,
// so a completed self-update's selection-independent claim on [3] goes with it —
// otherwise the tool's log would render under every other row too.
func TestToolConfirmEnterReleasesSelfLog(t *testing.T) {
	m := selfModel(t, []loader.ToolMeta{{Name: "rg"}}, 100, selfOffered)
	m.selfState = selfUpdated
	m.selfUpdateLog = true
	m.updateLogFor = selfToolName
	m.mode = modeConfirmUpdate
	m.updateTarget = "rg"
	m.updatePlan = updater.Plan{Argv: []string{"true"}, Display: "brew upgrade ripgrep"}

	m = mustModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if m.updatingFor != "rg" || m.updateLogFor != "rg" {
		t.Errorf("updatingFor = %q, updateLogFor = %q, want both rg", m.updatingFor, m.updateLogFor)
	}
	if m.selfUpdateLog {
		t.Error("selfUpdateLog = true after a tool update started, want it released")
	}
}

// TestSelfConfirmBarNamesKeepkit: the confirm bar names the plan's own target,
// which for keepkit's update is neither the selected tool nor — with an empty
// tracker — anything at all.
func TestSelfConfirmBarNamesKeepkit(t *testing.T) {
	for _, meta := range [][]loader.ToolMeta{{{Name: "rg"}}, nil} {
		m := selfModel(t, meta, 100, selfOffered)
		m.mode = modeConfirmUpdate
		m.updatePlan = updater.Plan{Display: "brew upgrade keepkit"}
		m.updateTarget = selfToolName

		bar := stripANSI(m.renderStatusBar())
		if !strings.Contains(bar, "update keepkit: brew upgrade keepkit") {
			t.Errorf("tracker %v: confirm bar = %q, want it to name keepkit", meta, bar)
		}
		if strings.Contains(bar, "update rg") {
			t.Errorf("tracker %v: confirm bar = %q, want no selected-tool name", meta, bar)
		}
	}

	// Regression: a tool plan still names its tool.
	m := selfModel(t, []loader.ToolMeta{{Name: "rg"}}, 100, selfOffered)
	m.mode = modeConfirmUpdate
	m.updatePlan = updater.Plan{Display: "brew upgrade ripgrep"}
	m.updateTarget = "rg"
	if bar := stripANSI(m.renderStatusBar()); !strings.Contains(bar, "update rg: brew upgrade ripgrep") {
		t.Errorf("tool confirm bar = %q, want it to name the plan's tool", bar)
	}
}

// ---- completion: success, failure and the restart offer ----

// TestSelfUpdateDoneSuccessUntracked: the self branch sits ahead of the
// toolByName early return, so an untracked keepkit's update actually completes —
// with the tool path it would finish silently and [U] restart would never appear.
func TestSelfUpdateDoneSuccessUntracked(t *testing.T) {
	shrinkStatusTTL(t)
	m := startedSelfUpdate(t, []loader.ToolMeta{{Name: "rg"}}, selfOffered)
	m.updateLog = []string{"==> Upgrading keepkit", "installed"}

	updated, cmd := m.Update(updateDoneMsg{tool: selfToolName})
	m = updated.(Model)
	if m.selfState != selfUpdated {
		t.Errorf("selfState = %v, want selfUpdated (the restart offer)", m.selfState)
	}
	if m.statusMsg != "updated keepkit" {
		t.Errorf("statusMsg = %q, want %q", m.statusMsg, "updated keepkit")
	}
	if m.updatingFor != "" || m.selfUpdating() {
		t.Errorf("updatingFor = %q, selfUpdating() = %v, want the guard cleared", m.updatingFor, m.selfUpdating())
	}
	// Nothing to re-detect: an untracked keepkit has no card and no ↑ marker, and
	// this process is still the old binary either way.
	assertOnlyExpiryTick(t, cmd)
}

// TestSelfUpdateDoneSuccessTracked: a tracked keepkit does have a card and a ↑
// marker, so the installed re-detect rides along. The batch is deliberately not
// executed — fetchInstalledCmd for this tool name would run the developer's own
// keepkit binary, making the test depend on the machine it runs on.
func TestSelfUpdateDoneSuccessTracked(t *testing.T) {
	shrinkStatusTTL(t)
	m := startedSelfUpdate(t, []loader.ToolMeta{{Name: selfToolName, GitHub: "github.com/" + version.SelfRepo}}, selfOffered)
	m.updateLog = []string{"installed"}

	updated, cmd := m.Update(updateDoneMsg{tool: selfToolName})
	m = updated.(Model)
	if m.selfState != selfUpdated {
		t.Errorf("selfState = %v, want selfUpdated", m.selfState)
	}
	if m.statusMsg != "updated keepkit" {
		t.Errorf("statusMsg = %q, want %q", m.statusMsg, "updated keepkit")
	}
	if m.selfUpdating() {
		t.Error("selfUpdating() = true after completion, want the guard cleared")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want the expiry tick batched with the re-detect")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("cmd produced %T (%d cmds), want a 2-command batch (tick + re-detect)", cmd(), len(batch))
	}
}

// TestSelfUpdateDoneFailure: a failed self-update leaves the banner it started
// from — here the offer, so [U] is a retry — and seeds the log with the reason
// when the command produced none: the status bar points at [3] for it, so the
// panel itself must have been repainted (asserted on the viewport, not on a fresh
// renderHelpContent call, which would pass with no repaint at all). The state
// check here is a spot check on this one fixture and is deliberately shallow: the
// handler writes no selfState at all, which only a table over every prior state
// can show — TestSelfUpdateDoneFailureKeepsPriorState is that table.
func TestSelfUpdateDoneFailure(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	m := startedSelfUpdate(t, nil, selfOffered)

	m = mustModel(m.Update(updateDoneMsg{tool: selfToolName, err: errors.New("exit status 1")}))
	// What the failure does to selfState is TestSelfUpdateDoneFailureKeepsPriorState's
	// subject, over all five prior states; asserting it against this fixture's own
	// seed would only restate the seed.
	if !strings.Contains(m.statusMsg, "update failed") {
		t.Errorf("statusMsg = %q, want an update-failed message", m.statusMsg)
	}
	if m.updatingFor != "" {
		t.Errorf("updatingFor = %q, want the guard cleared", m.updatingFor)
	}
	if len(m.updateLog) != 1 || !strings.Contains(m.updateLog[0], "exit status 1") {
		t.Errorf("updateLog = %#v, want the failure seeded for [3]", m.updateLog)
	}
	if view := stripANSI(m.helpViewport.View()); !strings.Contains(view, "exit status 1") {
		t.Errorf("help viewport = %q, want the seeded failure painted into [3]", view)
	}
	// The same post-hoc record the tool path writes, and just as token-free.
	log := logx.ReadAllForTesting(logDir)
	if !strings.Contains(log, selfToolName) || !strings.Contains(log, "exit status 1") {
		t.Errorf("log = %q, want the failed self-update recorded", log)
	}
	if strings.Contains(log, "token") {
		t.Errorf("log leaked a token-ish word: %q", log)
	}
}

// TestUpdateFailureSeedsOnlyItsOwnLog: the seed guard is the buffer's owner, not
// just its emptiness — a failure for a tool that no longer owns the log (another
// update started meanwhile) must not append its reason to that other log.
func TestUpdateFailureSeedsOnlyItsOwnLog(t *testing.T) {
	shrinkStatusTTL(t)
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	m := startedSelfUpdate(t, []loader.ToolMeta{{Name: "rg"}}, selfOffered)
	// The buffer has moved on to a tool update; the self log is empty history.
	m.updateLogFor = "rg"
	m.updateLog = nil

	m = mustModel(m.Update(updateDoneMsg{tool: selfToolName, err: errors.New("exit status 1")}))
	if len(m.updateLog) != 0 {
		t.Errorf("updateLog = %#v, want rg's buffer untouched by keepkit's failure", m.updateLog)
	}
	// The record still happens — only the on-screen seed is owner-gated.
	if log := logx.ReadAllForTesting(logDir); !strings.Contains(log, "exit status 1") {
		t.Errorf("log = %q, want the failure recorded anyway", log)
	}
}

// TestSelfUpdateDoneFailureKeepsPriorState: a failed self-update must not move
// selfState at all — whatever the banner said before the update it says again the
// moment updatingFor clears, so any write here can only walk a state back. All
// four non-trivial prior states are ways to get that wrong: an [X] folded
// mid-update is a deliberate "not now"; a pending [U] restart from an earlier
// successful update is still valid (that binary is on disk either way); and
// selfNone — reachable with no [U] press at all, since [u] on a tracked keepkit row
// is a self-update too and hasUpdate comes from the locally detected version,
// independent of a startup check that may have been rate-limited, offline, or
// simply said "not newer" — has no version behind it, so forcing "offered" there
// rendered a banner with a hole in it ("keepkit  available") that no later
// selfCheckMsg could fill, the handler writing only from selfNone.
func TestSelfUpdateDoneFailureKeepsPriorState(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	tests := []struct {
		name         string
		state        selfState
		latest       string
		wantContains string
		wantAbsent   string
	}{
		{name: "never offered", state: selfNone, wantAbsent: "keepkit"},
		{name: "offered", state: selfOffered, latest: "v0.5.0", wantContains: "v0.5.0 available"},
		{name: "folded offer", state: selfDismissed, latest: "v0.5.0", wantContains: "keepkit ↑"},
		{name: "restart pending", state: selfUpdated, wantContains: "keepkit updated"},
		{name: "restart folded", state: selfUpdatedLater, wantContains: "keepkit U restart"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := startedSelfUpdate(t, nil, tt.state)
			m.selfLatest = tt.latest

			m = mustModel(m.Update(updateDoneMsg{tool: selfToolName, err: errors.New("exit status 1")}))
			if m.selfState != tt.state {
				t.Errorf("selfState = %v, want the prior %v untouched", m.selfState, tt.state)
			}
			// The status bar as it reads once the transient "update failed" message
			// has expired — that message outranks the banner while it is up.
			m.statusMsg = ""
			bar := stripANSI(m.renderStatusBar())
			if tt.wantContains != "" && !strings.Contains(bar, tt.wantContains) {
				t.Errorf("status bar = %q, want %q", bar, tt.wantContains)
			}
			if tt.wantAbsent != "" && strings.Contains(bar, tt.wantAbsent) {
				t.Errorf("status bar = %q, want no %q surface at all", bar, tt.wantAbsent)
			}
		})
	}
}

// TestKeepkitUpdateSelfHandlingGatedOnBuild: [u] on a tracked keepkit row reaches
// the completion handler on every build, so the version gate — not the target name
// — is what decides whether it is treated as a self-update. On a release build it
// is one (the restart offer appears even though the startup check never got to
// selfOffered); on a dev build, where the feature is documented as fully off, it
// stays a plain tool update: no panel-owning log, no banner, no Self group in [?],
// and [U] raises no restart request — which on such a build would re-exec the
// working copy and silently hand back the pre-update binary.
func TestKeepkitUpdateSelfHandlingGatedOnBuild(t *testing.T) {
	tests := []struct {
		name       string
		appVersion string
		wantSelf   bool
	}{
		{name: "release build", appVersion: "v0.4.2", wantSelf: true},
		{name: "dev build", appVersion: "dev"},
		{name: "pseudo-version build", appVersion: "v0.0.0-20260725115912-1be4bafa79c8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shrinkStatusTTL(t)
			t.Setenv("HOME", t.TempDir())
			meta := []loader.ToolMeta{{Name: selfToolName, GitHub: "github.com/" + version.SelfRepo}, {Name: "rg"}}
			m := New(meta).WithAppVersion(tt.appVersion)
			m = mustModel(m.Update(tea.WindowSizeMsg{Width: 120, Height: 24}))
			if m.selfState != selfNone {
				t.Fatalf("selfState = %v, want selfNone before anything ran", m.selfState)
			}

			// The tool route, exactly as [u] on the keepkit row leaves it.
			m.mode = modeConfirmUpdate
			m.updateTarget = selfToolName
			m.updatePlan = updater.Plan{Manager: "brew", Argv: []string{"true"}, Display: "brew upgrade keepkit"}
			m = mustModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
			if m.selfUpdateLog != tt.wantSelf || m.selfUpdating() != tt.wantSelf {
				t.Fatalf("selfUpdateLog = %v, selfUpdating() = %v, want both %v",
					m.selfUpdateLog, m.selfUpdating(), tt.wantSelf)
			}

			// The batch is deliberately not executed: fetchInstalledCmd for this name
			// would run the developer's own keepkit binary.
			m = mustModel(m.Update(updateDoneMsg{tool: selfToolName}))
			if m.statusMsg != "updated keepkit" {
				t.Errorf("statusMsg = %q, want %q on both paths", m.statusMsg, "updated keepkit")
			}
			wantState := selfNone
			if tt.wantSelf {
				wantState = selfUpdated
			}
			if m.selfState != wantState {
				t.Fatalf("selfState = %v, want %v", m.selfState, wantState)
			}

			m.statusMsg = ""
			bar := stripANSI(m.renderStatusBar())
			overlay := stripANSI(m.renderHotkeys())
			m = mustModel(m.Update(keyRunes("U")))
			if tt.wantSelf {
				if !strings.Contains(bar, "keepkit updated") {
					t.Errorf("status bar = %q, want the restart offer", bar)
				}
				if !strings.Contains(overlay, "restart") {
					t.Errorf("[?] overlay = %q, want the Self group's restart row", overlay)
				}
				if !m.RestartRequested() {
					t.Error("RestartRequested() = false, want [U] to request the restart")
				}
				return
			}
			if strings.Contains(bar, "keepkit updated") || strings.Contains(bar, "U restart") {
				t.Errorf("status bar = %q, want no self-update surface on a dev build", bar)
			}
			if strings.Contains(overlay, "self-update") || strings.Contains(overlay, "U  restart") {
				t.Errorf("[?] overlay = %q, want no Self group on a dev build", overlay)
			}
			if m.RestartRequested() {
				t.Error("RestartRequested() = true on a dev build, want [U] unbound (it would re-exec the working copy)")
			}
			// A plain tool update also keeps the log per-tool sticky, so moving off
			// the keepkit row hands [3] back.
			m.focus = focusTools
			if moved := mustModel(m.Update(keyRunes("j"))); moved.showsUpdateLog() {
				t.Error("showsUpdateLog() = true under another row, want the plain per-tool log")
			}
		})
	}
}

// ---- who owns panel [3] while an update streams ----

// TestShowsUpdateLog: the single predicate behind every [3] site. The tool path
// is per-tool sticky (log only under its own row), the self path is
// selection-independent — including an empty tracker, where nothing is selected.
func TestShowsUpdateLog(t *testing.T) {
	tests := []struct {
		name         string
		meta         []loader.ToolMeta
		selected     int
		updateLogFor string
		selfLog      bool
		want         bool
	}{
		{name: "idle", meta: []loader.ToolMeta{{Name: "rg"}}},
		{name: "tool log under its own row", meta: []loader.ToolMeta{{Name: "rg"}}, updateLogFor: "rg", want: true},
		{
			name:         "tool log under another row",
			meta:         []loader.ToolMeta{{Name: "rg"}, {Name: "fd"}},
			selected:     1,
			updateLogFor: "rg",
		},
		{
			name:         "self log under a foreign row",
			meta:         []loader.ToolMeta{{Name: "rg"}},
			updateLogFor: selfToolName,
			selfLog:      true,
			want:         true,
		},
		{name: "self log with an empty tracker", updateLogFor: selfToolName, selfLog: true, want: true},
		{name: "empty tracker, no log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(tt.meta)
			m.metaSelected = tt.selected
			m.updateLogFor = tt.updateLogFor
			m.selfUpdateLog = tt.selfLog
			if got := m.showsUpdateLog(); got != tt.want {
				t.Errorf("showsUpdateLog() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSelfUpdateLogOwnsHelpPanel: the log and the [3] Update title are reachable
// with keepkit untracked and even with an empty tracker — where selectedMeta,
// which used to gate both, has nothing to return.
func TestSelfUpdateLogOwnsHelpPanel(t *testing.T) {
	for _, meta := range [][]loader.ToolMeta{{{Name: "rg"}}, nil} {
		m := startedSelfUpdate(t, meta, selfOffered)
		m.updateLog = []string{"==> Upgrading keepkit"}

		if content := stripANSI(m.renderHelpContent()); !strings.Contains(content, "==> Upgrading keepkit") {
			t.Errorf("tracker %v: [3] = %q, want the self-update log", meta, content)
		}
		if panel := stripANSI(m.renderHelp()); !strings.Contains(panel, "[3] update") {
			t.Errorf("tracker %v: panel title missing [3] update", meta)
		}
	}
}

// TestSelfUpdateChunkRepaintsUnderForeignSelection: the streaming output has to
// reach the visible panel even though the selected row belongs to another tool —
// the repaint gate used to require the selection to be the updating tool.
func TestSelfUpdateChunkRepaintsUnderForeignSelection(t *testing.T) {
	m := startedSelfUpdate(t, []loader.ToolMeta{{Name: "rg"}}, selfOffered)

	ch := make(chan updateLine, 1)
	m = feedChunk(m, updateChunkMsg{tool: selfToolName, line: "==> Downloading", ch: ch})
	if len(m.updateLog) != 1 || m.updateLog[0] != "==> Downloading" {
		t.Fatalf("updateLog = %#v, want the chunk buffered", m.updateLog)
	}
	if view := stripANSI(m.helpViewport.View()); !strings.Contains(view, "==> Downloading") {
		t.Errorf("help viewport = %q, want the live self-update log", view)
	}
}

// TestSelfUpdateLogReleasedWhenDone: the self log is selection-independent, so
// something has to hand [3] back — a selection move or an explicit [h]/[m]/[r],
// but only once the update has finished.
func TestSelfUpdateLogReleasedWhenDone(t *testing.T) {
	// A selection move after completion returns the panel to the tool's help.
	m := startedSelfUpdate(t, []loader.ToolMeta{{Name: "rg"}, {Name: "fd"}}, selfOffered)
	m.updateLog = []string{"installed"}
	m.updatingFor = "" // finished
	m.focus = focusTools

	moved := mustModel(m.Update(keyRunes("j")))
	if moved.selfUpdateLog {
		t.Error("selfUpdateLog = true after a selection move, want the panel handed back")
	}
	if strings.Contains(stripANSI(moved.renderHelpContent()), "installed") {
		t.Error("[3] still shows the finished self-update log after a selection move")
	}

	// The same move while it is still streaming keeps the log — that output is
	// only visible there.
	m.updatingFor = selfToolName
	live := mustModel(m.Update(keyRunes("j")))
	if !live.selfUpdateLog {
		t.Error("selfUpdateLog = false during a live update, want the log kept")
	}

	// [h] releases it too, including with an empty tracker, where switchHelpMode
	// returns early on the missing selection.
	empty := startedSelfUpdate(t, nil, selfOffered)
	empty.updateLog = []string{"installed"}
	empty.updatingFor = ""
	empty.focus = focusBrief
	empty = mustModel(empty.Update(keyRunes("h")))
	if empty.selfUpdateLog {
		t.Error("selfUpdateLog = true after [h] with an empty tracker, want it released")
	}
}

// TestSelfUpdateLogSuppressesHelpNav covers the setHelpContent site of
// showsUpdateLog: keepkit's own log owns [3] regardless of the selection, so the
// entry index must stay empty even though the selected tool has cached --help —
// otherwise j/k would drive a spotlight computed for that help over log lines.
func TestSelfUpdateLogSuppressesHelpNav(t *testing.T) {
	base := func() Model {
		// WithAppVersion because the fixture below claims a *live* self-update, and
		// selfCheckEnabled gates that state: without it selfUpdating() is false
		// whatever updatingFor says, and the fixture would be a state the app can
		// never reach.
		m := newTestModel(focusHelp).WithAppVersion("v0.4.2")
		m.helpW = 62
		m.helpCache["git"] = [2]string{helpModeHelp: navHelpFixture}
		return m
	}

	// Control: the same fixture is navigable with no log in the way.
	control := base()
	control.setHelpContent()
	if len(control.helpEntries) != 2 {
		t.Fatalf("control helpEntries = %v, want the 2 fixture entries", control.helpEntries)
	}

	m := base()
	m.updatingFor = selfToolName
	m.updateLogFor = selfToolName
	m.selfUpdateLog = true
	m.updateLog = []string{"==> Upgrading keepkit"}
	m.setHelpContent()

	if len(m.helpEntries) != 0 {
		t.Errorf("helpEntries = %v, want empty while the self-update log owns [3]", m.helpEntries)
	}
	if m.helpBase != "" {
		t.Errorf("helpBase = %q, want empty (the log is not colorized help)", m.helpBase)
	}
	if content := stripANSI(m.renderHelpContent()); !strings.Contains(content, "==> Upgrading keepkit") {
		t.Errorf("[3] = %q, want the self-update log", content)
	}
	// With no entries j is plain scroll, so the spotlight cursor stays off.
	if moved := mustModel(m.Update(keyRunes("j"))); moved.helpNavIdx != -1 {
		t.Errorf("helpNavIdx = %d after j, want -1 (no spotlight over log lines)", moved.helpNavIdx)
	}
}

// TestSelfUpdateLogSkipsHelpFetch covers the autoFetchCmdsForSelected site of
// showsUpdateLog: a selection move under a live self-update must not start a
// --help probe or set helpLoadingFor, or a late helpOutputMsg (or the
// "Loading..." state) would clobber the log.
func TestSelfUpdateLogSkipsHelpFetch(t *testing.T) {
	live := startedSelfUpdate(t, []loader.ToolMeta{{Name: "rg"}}, selfOffered)
	live.helpMode = helpModeHelp
	live.updateLog = []string{"==> Upgrading keepkit"}

	live.autoFetchCmdsForSelected()
	if live.helpLoadingFor != "" {
		t.Errorf("helpLoadingFor = %q, want no --help probe while the self log owns [3]", live.helpLoadingFor)
	}
	if content := stripANSI(live.renderHelpContent()); !strings.Contains(content, "==> Upgrading keepkit") {
		t.Errorf("[3] = %q, want the self-update log", content)
	}

	// Control: once the log is released the same tool does get its probe.
	control := startedSelfUpdate(t, []loader.ToolMeta{{Name: "rg"}}, selfOffered)
	control.helpMode = helpModeHelp
	control.updatingFor, control.updateLogFor, control.selfUpdateLog = "", "", false
	control.autoFetchCmdsForSelected()
	if control.helpLoadingFor != "rg" {
		t.Errorf("helpLoadingFor = %q, want rg with no log in the way", control.helpLoadingFor)
	}
}
