package model

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
