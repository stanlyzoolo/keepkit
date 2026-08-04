package model

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/version"
)

// shrinkStatusTTL shortens statusMsgTTL so a test can invoke the tick Cmd that
// setStatus returns without waiting the real TTL (tea.Tick blocks for the full
// duration). Same var-seam idiom as launchTimeout in launch_test.go. Must be
// called before the Update that produces the tick — tea.Tick captures the TTL
// value at construction time.
func shrinkStatusTTL(t *testing.T) {
	t.Helper()
	orig := statusMsgTTL
	statusMsgTTL = time.Millisecond
	t.Cleanup(func() { statusMsgTTL = orig })
}

// assertOnlyExpiryTick verifies cmd is exactly the transient-status expiry tick
// and nothing else — no fetch/exec batched alongside. The caller must have
// shrunk statusMsgTTL (shrinkStatusTTL) before the Update that produced cmd.
func assertOnlyExpiryTick(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("cmd = nil, want the status-expiry tick")
	}
	switch msg := cmd().(type) {
	case statusExpiredMsg:
		// A lone tick — nothing else rides along.
	case tea.BatchMsg:
		t.Fatalf("cmd batched %d commands, want only the expiry tick", len(msg))
	default:
		t.Fatalf("cmd produced %T, want statusExpiredMsg (only the expiry tick)", msg)
	}
}

// assertExpiryTickSeq verifies cmd is the transient-status expiry tick stamped
// with wantSeq. The caller must have shrunk statusMsgTTL.
func assertExpiryTickSeq(t *testing.T, cmd tea.Cmd, wantSeq int) {
	t.Helper()
	if cmd == nil {
		t.Fatal("cmd = nil, want the status-expiry tick")
	}
	msg, ok := cmd().(statusExpiredMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want statusExpiredMsg", cmd())
	}
	if msg.seq != wantSeq {
		t.Errorf("tick seq = %d, want %d", msg.seq, wantSeq)
	}
}

// batchProducesInstalled reports whether cmd (a batch or single cmd) has a leaf
// producing an installedMsg for name. Flattens tea batches; the caller must have
// shrunk statusMsgTTL so a batched expiry tick returns immediately.
func batchProducesInstalled(cmd tea.Cmd, name string) bool {
	return cmd != nil && msgHasInstalled(cmd(), name)
}

func msgHasInstalled(msg tea.Msg, name string) bool {
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			if c != nil && msgHasInstalled(c(), name) {
				return true
			}
		}
		return false
	case installedMsg:
		return m.toolName == name
	default:
		return false
	}
}

// TestSetStatusReturnsExpiryTick: setStatus sets the message, bumps the
// generation counter, and returns a Cmd producing a statusExpiredMsg stamped
// with the new seq.
func TestSetStatusReturnsExpiryTick(t *testing.T) {
	shrinkStatusTTL(t)
	m := newTestModel(focusTools)
	before := m.statusSeq

	cmd := m.setStatus("grouped by tag")
	if m.statusMsg != "grouped by tag" {
		t.Fatalf("statusMsg = %q, want %q", m.statusMsg, "grouped by tag")
	}
	if m.statusSeq != before+1 {
		t.Fatalf("statusSeq = %d, want %d", m.statusSeq, before+1)
	}
	if cmd == nil {
		t.Fatal("setStatus returned nil cmd, want the expiry tick")
	}
	msg, ok := cmd().(statusExpiredMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want statusExpiredMsg", cmd())
	}
	if msg.seq != m.statusSeq {
		t.Errorf("tick seq = %d, want %d (must match the message it retires)", msg.seq, m.statusSeq)
	}
}

// TestStatusExpiredMatchingSeqClears: a timer whose seq is still current clears
// the message.
func TestStatusExpiredMatchingSeqClears(t *testing.T) {
	shrinkStatusTTL(t)
	m := newTestModel(focusTools)
	m.setStatus("flat list")
	seq := m.statusSeq

	updated, _ := m.Update(statusExpiredMsg{seq: seq})
	if got := updated.(Model).statusMsg; got != "" {
		t.Errorf("statusMsg = %q after matching expiry, want cleared", got)
	}
}

// TestStatusExpiredStaleSeqIgnored: a second setStatus supersedes the first, so
// the first message's timer (stale seq) must not clear the newer message.
func TestStatusExpiredStaleSeqIgnored(t *testing.T) {
	shrinkStatusTTL(t)
	m := newTestModel(focusTools)
	m.setStatus("grouped by tag")
	stale := m.statusSeq
	m.setStatus("flat list") // supersedes; seq bumps

	updated, _ := m.Update(statusExpiredMsg{seq: stale})
	if got := updated.(Model).statusMsg; got != "flat list" {
		t.Errorf("statusMsg = %q after stale expiry, want the newer message intact", got)
	}
}

// TestToggleGroupByTagSetsExpiringStatus: the toggle reports the view change via
// setStatus — "grouped by tag" on, "flat list" off — and the cmd it returns is
// the matching expiry tick (current seq), so the message clears itself.
func TestToggleGroupByTagSetsExpiringStatus(t *testing.T) {
	shrinkStatusTTL(t)
	m := groupTestModel(t)

	cmd := m.toggleGroupByTag()
	if !m.groupByTag {
		t.Fatal("toggle did not turn grouping on")
	}
	if m.statusMsg != "grouped by tag" {
		t.Errorf("statusMsg = %q, want %q", m.statusMsg, "grouped by tag")
	}
	assertExpiryTickSeq(t, cmd, m.statusSeq)

	cmd = m.toggleGroupByTag()
	if m.groupByTag {
		t.Fatal("toggle did not turn grouping off")
	}
	if m.statusMsg != "flat list" {
		t.Errorf("statusMsg = %q, want %q", m.statusMsg, "flat list")
	}
	assertExpiryTickSeq(t, cmd, m.statusSeq)
}

// TestInFlightStatusSurvivesStaleExpiry drives the real sequence a user can
// produce in under statusMsgTTL: a transient status (the group toggle), then a
// launch dispatch whose "launching …" message reports work still in progress.
// The transient's timer is still in flight when the launch message lands, and it
// must not take it down — the in-flight status is extinguished by launchDoneMsg,
// not by the clock, and losing it hides the only sign the adapter is busy for up
// to launchTimeout. A direct m.statusMsg assignment leaves statusSeq unbumped,
// so the stale timer's seq still matches and the expiry handler wipes it; the
// bump is what setStickyStatus exists for.
func TestInFlightStatusSurvivesStaleExpiry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the adapter path needs a unix tmux environment")
	}
	shrinkStatusTTL(t)
	clearTerminalEnv(t)
	t.Setenv("TMUX", "/nonexistent/keepkit-test-socket,99999,0")

	m := groupTestModel(t)
	m.focus = focusTools
	toggleCmd := m.toggleGroupByTag()
	staleSeq := m.statusSeq
	if toggleCmd == nil {
		t.Fatal("toggle returned no expiry tick")
	}

	// The launch dispatch: an adapter plan, so the status is the in-flight one.
	nm, _ := enterRun(m, "git status")
	if !strings.Contains(nm.statusMsg, "launching") {
		t.Fatalf("statusMsg = %q, want the in-flight launch feedback", nm.statusMsg)
	}
	inFlight := nm.statusMsg

	// The toggle's timer fires now — it belongs to a message already replaced.
	after := mustModel(nm.Update(statusExpiredMsg{seq: staleSeq}))
	if after.statusMsg != inFlight {
		t.Errorf("statusMsg = %q after the stale expiry, want the in-flight %q", after.statusMsg, inFlight)
	}
}

// TestStatusBarReturnsHintsAfterExpiry: while a transient status is live the bar
// shows it (outranking the hints); once the matching timer fires the hints bar
// returns on its own, with no keypress.
func TestStatusBarReturnsHintsAfterExpiry(t *testing.T) {
	shrinkStatusTTL(t)
	m := newTestModel(focusBrief)
	m.setStatus("grouped by tag")
	seq := m.statusSeq

	if bar := stripANSI(m.renderStatusBar()); !strings.Contains(bar, "grouped by tag") {
		t.Fatalf("status bar = %q, want the live status message", bar)
	}

	updated, _ := m.Update(statusExpiredMsg{seq: seq})
	bar := stripANSI(updated.(Model).renderStatusBar())
	if strings.Contains(bar, "grouped by tag") {
		t.Errorf("status bar still shows the expired message: %q", bar)
	}
	if !strings.Contains(bar, "t track") {
		t.Errorf("status bar = %q, want the global hints back", bar)
	}
}

// refreshingModel returns a model mid-[r] for the named tool: refreshingFor set,
// the maps the remoteMsg handler writes through initialised.
func refreshingModel(t *testing.T, name string) Model {
	t.Helper()
	m := New([]loader.ToolMeta{{Name: name, GitHub: "cli/cli"}})
	m.width, m.height = 80, 24
	m.focus = focusBrief
	m.refreshingFor = name
	return m
}

// TestRefreshAnswersEveryPress pins the promise [r] makes. Before this, success,
// a rate limit, a 401, a timeout and a dropped connection were one gesture: the
// spinner turns and the card does not change, so a user could not tell a tool
// that is up to date from a tool whose data has not been fetched in a day.
//
// The predicate is conclusive, not err: a pass can fail and still have settled
// nothing to say (a repo with no releases answers with a nil error), and a
// rate-limited pass that served a stale card carries an error while settling
// nothing.
func TestRefreshAnswersEveryPress(t *testing.T) {
	tests := []struct {
		name       string
		msg        remoteMsg
		wantStatus string
	}{
		{
			name:       "rate limited names the key that raises the ceiling",
			msg:        remoteMsg{toolName: "gh", err: version.ErrRateLimited},
			wantStatus: "refresh failed: rate limited — press [a]",
		},
		{
			name:       "a rejected token is not called out separately",
			msg:        remoteMsg{toolName: "gh", err: version.ErrTokenInvalid},
			wantStatus: "refresh failed: network error",
		},
		{
			name:       "a transient failure reads the same whatever it was",
			msg:        remoteMsg{toolName: "gh", err: errBoom},
			wantStatus: "refresh failed: network error",
		},
		{
			// The version layer refuses an unsupported or spoofed host before
			// making any request and answers a bare RepoData — no error, not
			// conclusive. Nothing failed to fetch, so claiming a network error
			// would name a cause that never existed.
			name:       "a ref the version layer refused says nothing",
			msg:        remoteMsg{toolName: "gh"},
			wantStatus: "",
		},
		{
			// A partial pass: the release fetch landed a new tag, only the repo
			// card was lost. Conclusive is false (CheckedAt stays stale for the
			// refill) but the card visibly updated, so the bar must not
			// contradict what the user just watched happen.
			name:       "a partial pass that fetched a tag says nothing",
			msg:        remoteMsg{toolName: "gh", latest: "v2.0.0"},
			wantStatus: "",
		},
		{
			name:       "a conclusive pass stays silent — the repainted card is the answer",
			msg:        remoteMsg{toolName: "gh", latest: "v2.0.0", conclusive: true},
			wantStatus: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The tick is constructed inside Update, so the TTL must shrink first.
			shrinkStatusTTL(t)
			m := refreshingModel(t, "gh")
			updated, cmd := m.Update(tt.msg)
			nm := updated.(Model)

			if nm.statusMsg != tt.wantStatus {
				t.Errorf("statusMsg = %q, want %q", nm.statusMsg, tt.wantStatus)
			}
			if nm.refreshingFor != "" {
				t.Errorf("refreshingFor = %q, want cleared whatever the outcome", nm.refreshingFor)
			}
			if tt.wantStatus == "" {
				if cmd != nil {
					t.Errorf("a silent success returned a cmd (%T), want none", cmd())
				}
				return
			}
			// Nothing may ride along: [r] answering is a message, not a retry.
			assertOnlyExpiryTick(t, cmd)
		})
	}
}

// TestRefreshStatusOnlyForTheRefreshedTool: the background passes Init fires are
// inconclusive all the time (offline start, rate limit) and must not put a
// "refresh failed" message on the bar for a gesture the user never made.
func TestRefreshStatusOnlyForTheRefreshedTool(t *testing.T) {
	shrinkStatusTTL(t)
	m := refreshingModel(t, "gh")
	m.meta = append(m.meta, loader.ToolMeta{Name: "rg", GitHub: "BurntSushi/ripgrep"})
	m.tools = loader.ToolsFromMeta(m.meta)

	updated, cmd := m.Update(remoteMsg{toolName: "rg", err: version.ErrRateLimited})
	nm := updated.(Model)

	if nm.statusMsg != "" {
		t.Errorf("statusMsg = %q, want silence for a tool nobody refreshed", nm.statusMsg)
	}
	if nm.refreshingFor != "gh" {
		t.Errorf("refreshingFor = %q, want gh still in flight", nm.refreshingFor)
	}
	if cmd != nil {
		t.Errorf("returned a cmd (%T), want none", cmd())
	}
}
