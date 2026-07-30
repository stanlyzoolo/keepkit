package model

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
	"github.com/stanlyzoolo/keepkit/internal/ui"
	"github.com/stanlyzoolo/keepkit/internal/updater"
)

// feedChunk drives one updateChunkMsg through Update and returns the new model.
func feedChunk(m Model, msg updateChunkMsg) Model {
	updated, _ := m.Update(msg)
	return updated.(Model)
}

// TestUpdateChunkAppendAndReplace: a '\n' segment (replace=false) appends a new
// line; a '\r' segment (replace=true) overwrites the last line — so a progress
// bar renders as one updating line, not a stack of copies.
func TestUpdateChunkAppendAndReplace(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "git"}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.updatingFor = "git"
	m.updateLogFor = "git"

	// The first progress segment appends onto a fresh line; subsequent replace
	// segments overwrite it, so the bar collapses to one line.
	ch := make(chan updateLine, 1)
	m = feedChunk(m, updateChunkMsg{tool: "git", line: "downloading", ch: ch})
	m = feedChunk(m, updateChunkMsg{tool: "git", line: " 10%", ch: ch})
	m = feedChunk(m, updateChunkMsg{tool: "git", line: " 90%", replace: true, ch: ch})
	m = feedChunk(m, updateChunkMsg{tool: "git", line: "done", ch: ch})

	want := []string{"downloading", " 90%", "done"}
	if len(m.updateLog) != len(want) {
		t.Fatalf("updateLog = %#v, want %#v", m.updateLog, want)
	}
	for i, w := range want {
		if m.updateLog[i] != w {
			t.Errorf("updateLog[%d] = %q, want %q", i, m.updateLog[i], w)
		}
	}
}

// TestUpdateChunkReplaceOnEmptyBufferAppends: a leading '\r' segment (no prior
// line to replace) must not panic — it appends instead.
func TestUpdateChunkReplaceOnEmptyBufferAppends(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "git"}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.updatingFor = "git"
	m.updateLogFor = "git"

	ch := make(chan updateLine, 1)
	m = feedChunk(m, updateChunkMsg{tool: "git", line: "first", replace: true, ch: ch})
	if len(m.updateLog) != 1 || m.updateLog[0] != "first" {
		t.Fatalf("replace on empty buffer should append, got %#v", m.updateLog)
	}
}

// TestUpdateChunkCapKeepsTail: the buffer is capped to updateLogMaxLines and it
// is the *tail* that survives — the final install/error lines matter, not the
// head.
func TestUpdateChunkCapKeepsTail(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "git"}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.updatingFor = "git"
	m.updateLogFor = "git"

	ch := make(chan updateLine, 1)
	total := updateLogMaxLines + 50
	for i := range total {
		m = feedChunk(m, updateChunkMsg{tool: "git", line: fmt.Sprintf("line-%d", i), ch: ch})
	}

	if len(m.updateLog) != updateLogMaxLines {
		t.Fatalf("buffer len = %d, want cap %d", len(m.updateLog), updateLogMaxLines)
	}
	// The last line pushed must be the last line kept.
	wantLast := fmt.Sprintf("line-%d", total-1)
	if got := m.updateLog[len(m.updateLog)-1]; got != wantLast {
		t.Errorf("last kept line = %q, want %q", got, wantLast)
	}
	// The first kept line must be the one at offset total-cap.
	wantFirst := fmt.Sprintf("line-%d", total-updateLogMaxLines)
	if got := m.updateLog[0]; got != wantFirst {
		t.Errorf("first kept line = %q, want %q (head should be dropped)", got, wantFirst)
	}
}

// TestUpdateChunkNonSelectedToolViewportUntouched: while an update for tool X
// runs in the background and the user is looking at tool Y, a chunk for X folds
// into X's buffer but must not repaint Y's help viewport.
func TestUpdateChunkNonSelectedToolViewportUntouched(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "alpha"}, {Name: "beta"}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// Update runs for beta; the user is on alpha (index 0).
	m.metaSelected = 0
	m.updatingFor = "beta"
	m.updateLogFor = "beta"

	sentinel := "ALPHA-HELP-SENTINEL"
	m.helpViewport.SetContent(sentinel)
	before := m.helpViewport.View()

	ch := make(chan updateLine, 1)
	m = feedChunk(m, updateChunkMsg{tool: "beta", line: "compiling", ch: ch})

	// The chunk still lands in beta's buffer...
	if len(m.updateLog) != 1 || m.updateLog[0] != "compiling" {
		t.Errorf("background chunk should still buffer, got %#v", m.updateLog)
	}
	// ...but alpha's visible viewport must be byte-for-byte unchanged.
	if after := m.helpViewport.View(); after != before {
		t.Errorf("non-selected tool's viewport was repainted:\nbefore=%q\nafter=%q", before, after)
	}
}

// TestUpdateChunkForeignToolDropped: a chunk whose tool is not the active
// update session (updateLogFor) is ignored entirely — it neither appends nor
// panics — while the handler still re-subscribes to keep the channel draining.
func TestUpdateChunkForeignToolDropped(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "git"}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.updatingFor = "git"
	m.updateLogFor = "git"

	ch := make(chan updateLine, 1)
	m2, cmd := m.Update(updateChunkMsg{tool: "stale", line: "ignored", ch: ch})
	m = m2.(Model)
	if len(m.updateLog) != 0 {
		t.Errorf("foreign chunk should not append, got %#v", m.updateLog)
	}
	if cmd == nil {
		t.Error("handler must re-subscribe even for a foreign chunk")
	}
}

// TestUpdateChunkSanitizes: raw ANSI/control bytes in a segment are cleaned
// before entering the buffer (this text is re-emitted verbatim by the renderer).
func TestUpdateChunkSanitizes(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "git"}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.updatingFor = "git"
	m.updateLogFor = "git"

	ch := make(chan updateLine, 1)
	m = feedChunk(m, updateChunkMsg{tool: "git", line: "\x1b[32mok\x1b[0m\x07", ch: ch})
	if len(m.updateLog) != 1 || m.updateLog[0] != "ok" {
		t.Fatalf("segment not sanitized, got %#v", m.updateLog)
	}
}

// TestStreamLines pins the reader's line-splitting: '\n' and lone '\r' each end
// a segment (replace flag distinguishes them), "\r\n" counts as one '\n', and a
// trailing unterminated fragment is emitted as an appended line.
func TestStreamLines(t *testing.T) {
	in := "a\nb\rc\r\nd"
	type seg struct {
		text    string
		replace bool
	}
	var got []seg
	streamLines(strings.NewReader(in), func(text string, replace bool) {
		got = append(got, seg{text, replace})
	})

	// replace reflects the *previous* segment's terminator: only "c" follows a
	// lone '\r' (after "b"), so only "c" overwrites.
	want := []seg{
		{"a", false}, // first segment, nothing before it
		{"b", false}, // "a" ended in \n → append
		{"c", true},  // "b" ended in lone \r → overwrite
		{"d", false}, // "c" ended in \r\n (treated as \n) → append
	}
	if len(got) != len(want) {
		t.Fatalf("streamLines segments = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// TestWaitForChunkCmd: a normal item becomes updateChunkMsg (carrying the same
// channel for re-subscribe); the done item and a closed channel both become
// updateDoneMsg with the exit error and the measured duration.
func TestWaitForChunkCmd(t *testing.T) {
	ch := make(chan updateLine, 3)
	ch <- updateLine{text: "hello", replace: false}
	ch <- updateLine{done: true, err: nil, elapsed: 3 * time.Second}

	msg := waitForChunkCmd("git", ch)()
	chunk, ok := msg.(updateChunkMsg)
	if !ok {
		t.Fatalf("first item: got %T, want updateChunkMsg", msg)
	}
	if chunk.tool != "git" || chunk.line != "hello" || chunk.replace {
		t.Errorf("unexpected chunk: %#v", chunk)
	}
	if chunk.ch != ch {
		t.Error("chunk must carry the same channel for re-subscribe")
	}

	msg = waitForChunkCmd("git", ch)()
	done, ok := msg.(updateDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("done item: got %#v, want updateDoneMsg{err:nil}", msg)
	}
	// The duration rides the same item as the exit error — dropping it here
	// would leave the block in [3] unable to say how long the update took.
	if done.elapsed != 3*time.Second {
		t.Errorf("elapsed = %v, want it carried through from the done item", done.elapsed)
	}

	// A closed channel with nothing left also yields a done message.
	closed := make(chan updateLine)
	close(closed)
	if _, ok := waitForChunkCmd("git", closed)().(updateDoneMsg); !ok {
		t.Fatalf("closed channel should yield updateDoneMsg")
	}
}

// TestDetectUpdateCmdCustom: a Tool with UpdateCmd set resolves to a custom
// plan without any detection subprocess, and detectUpdateCmd surfaces it as an
// updateDetectedMsg.
func TestDetectUpdateCmdCustom(t *testing.T) {
	msg := detectUpdateCmd(loader.Tool{Name: "git", UpdateCmd: "brew upgrade git"}, false)()
	det, ok := msg.(updateDetectedMsg)
	if !ok {
		t.Fatalf("got %T, want updateDetectedMsg", msg)
	}
	if det.err != nil {
		t.Fatalf("custom plan should not error: %v", det.err)
	}
	if det.tool != "git" || det.plan.Manager != "custom" || det.plan.Display != "brew upgrade git" {
		t.Errorf("unexpected plan: %#v", det.plan)
	}
	if det.self {
		t.Error("self = true for a tool detection, want false")
	}
}

// TestStartUpdateCmdStreamsToCompletion drives a real trivial subprocess end to
// end: startUpdateCmd + waitForChunkCmd pump every line and finish with a
// success updateDoneMsg. This exercises the load-bearing reader-before-Wait
// ordering and the merged stdout+stderr pipe.
func TestStartUpdateCmdStreamsToCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c")
	}
	cmd := "printf 'one\\ntwo\\n'; printf 'err\\n' 1>&2"
	p := updater.Plan{Manager: "custom", Argv: []string{"sh", "-c", cmd}, Display: cmd}

	msg := startUpdateCmd(p, "x")()
	var lines []string
	for {
		switch v := msg.(type) {
		case updateChunkMsg:
			if !v.replace {
				lines = append(lines, v.line)
			} else if len(lines) > 0 {
				lines[len(lines)-1] = v.line
			}
			msg = waitForChunkCmd(v.tool, v.ch)()
		case updateDoneMsg:
			if v.err != nil {
				t.Fatalf("update should succeed, err %v", v.err)
			}
			joined := strings.Join(lines, "|")
			// stderr is merged into the same stream; order between the two is
			// not guaranteed, so assert membership.
			for _, want := range []string{"one", "two", "err"} {
				if !strings.Contains(joined, want) {
					t.Errorf("missing %q in streamed output %q", want, joined)
				}
			}
			return
		default:
			t.Fatalf("unexpected msg %T", msg)
		}
	}
}

// TestStartUpdateCmdReportsElapsed: the done message carries how long the
// process ran. The measurement lives here rather than in the updateDoneMsg
// handler because time.Now() inside Update() would make completion
// non-deterministic in every test that drives it.
func TestStartUpdateCmdReportsElapsed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c")
	}
	p := updater.Plan{Manager: "custom", Argv: []string{"sh", "-c", "printf 'x\\n'"}, Display: "x"}

	msg := startUpdateCmd(p, "x")()
	for {
		switch v := msg.(type) {
		case updateChunkMsg:
			msg = waitForChunkCmd(v.tool, v.ch)()
		case updateDoneMsg:
			if v.elapsed <= 0 {
				t.Errorf("elapsed = %v, want a positive duration for a run that happened", v.elapsed)
			}
			return
		default:
			t.Fatalf("unexpected msg %T", msg)
		}
	}
}

// TestStartUpdateCmdUnstartedReportsZeroElapsed: the early returns — empty argv,
// a StdoutPipe or Start failure — never reach the stamp, so they report zero.
// That is the "it never ran" signal the block in [3] reads to omit the duration
// cell instead of claiming an update took 0s.
func TestStartUpdateCmdUnstartedReportsZeroElapsed(t *testing.T) {
	msg := startUpdateCmd(updater.Plan{Manager: "custom"}, "x")()
	done, ok := msg.(updateDoneMsg)
	if !ok {
		t.Fatalf("got %T, want updateDoneMsg", msg)
	}
	if done.err == nil {
		t.Error("empty argv must report an error")
	}
	if done.elapsed != 0 {
		t.Errorf("elapsed = %v, want 0 for a process that never started", done.elapsed)
	}
}

// TestUpdateOutcomeRecordedOnBothResults: every finished session leaves a
// terminal state behind, success as well as failure — the success case is the
// whole point, since before it the only sign an update ended was a status
// message that expires in a second.
func TestUpdateOutcomeRecordedOnBothResults(t *testing.T) {
	shrinkStatusTTL(t)
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "failure", err: errUpdateTest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := updateDoneModel(t)
			m2 := mustModel(m.Update(updateDoneMsg{tool: "rg", err: tt.err, elapsed: 12 * time.Second}))

			got := m2.updateOutcome
			if got.tool != "rg" {
				t.Errorf("outcome.tool = %q, want rg", got.tool)
			}
			if got.manager != "brew" {
				t.Errorf("outcome.manager = %q, want the plan's manager", got.manager)
			}
			if got.elapsed != 12*time.Second {
				t.Errorf("outcome.elapsed = %v, want the duration off the message", got.elapsed)
			}
			if got.err != tt.err {
				t.Errorf("outcome.err = %v, want %v", got.err, tt.err)
			}
			// Read before the version merge: the success path fires
			// fetchInstalledCmd, whose installedMsg overwrites this very field.
			if got.was != "1.0.0" {
				t.Errorf("outcome.was = %q, want the pre-update installed version", got.was)
			}
			if got.verified {
				t.Error("outcome.verified set before any installedMsg landed")
			}
		})
	}
}

// TestUpdateOutcomeSharedWithSelfPath: keepkit's own update ends through the same
// recorder as a tool's. The two paths are documented as sharing one definition so
// they cannot drift, and this is the assertion that says so — the self branch
// returns before the tool branch is ever reached.
func TestUpdateOutcomeSharedWithSelfPath(t *testing.T) {
	shrinkStatusTTL(t)
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	m := startedSelfUpdate(t, []loader.ToolMeta{{Name: "rg"}}, selfOffered)
	m.updatePlan = updater.Plan{Manager: "go", Display: "go install …@latest"}

	m2 := mustModel(m.Update(updateDoneMsg{tool: selfToolName, err: nil, elapsed: 8 * time.Second}))
	if m2.updateOutcome.tool != selfToolName || m2.updateOutcome.manager != "go" {
		t.Errorf("outcome = %+v, want the self update recorded like any other", m2.updateOutcome)
	}
	if m2.updateOutcome.elapsed != 8*time.Second {
		t.Errorf("outcome.elapsed = %v, want 8s", m2.updateOutcome.elapsed)
	}
}

// TestUpdateOutcomeClearedByNextUpdate: the block belongs to the buffer under it,
// so starting a session drops the previous one's terminal state. A survivor would
// render "✓ finished" over a log that has just started.
func TestUpdateOutcomeClearedByNextUpdate(t *testing.T) {
	shrinkStatusTTL(t)
	m := updateDoneModel(t)
	m = mustModel(m.Update(updateDoneMsg{tool: "rg", err: nil, elapsed: time.Second}))
	if m.updateOutcome.tool == "" {
		t.Fatal("precondition: the first update must have left an outcome")
	}

	m.mode = modeConfirmUpdate
	m.updateTarget = "rg"
	m.updatePlan = updater.Plan{Manager: "brew", Argv: []string{"true"}, Display: "true"}
	m2 := mustModel(m.updateConfirmUpdate(tea.KeyMsg{Type: tea.KeyEnter}))

	if m2.updateOutcome != (updateOutcome{}) {
		t.Errorf("outcome = %+v, want it cleared with the buffer", m2.updateOutcome)
	}
}

// TestUpdateOutcomeVerifiedByInstalledMsg: the re-detect the success path fires
// is what turns "the command exited zero" into "the tool is at this version".
// The unrelated-tool and already-verified rows guard the two ways this handler —
// which fires for every tool at startup and on every [r] — could corrupt an
// outcome it does not own.
func TestUpdateOutcomeVerifiedByInstalledMsg(t *testing.T) {
	shrinkStatusTTL(t)

	for _, tt := range []struct {
		name         string
		msg          installedMsg
		pre          func(*Model)
		wantVerified bool
		wantNow      string
	}{
		{
			name:         "the update's own re-detect",
			msg:          installedMsg{toolName: "rg", installed: "2.0.0", present: true},
			wantVerified: true,
			wantNow:      "2.0.0",
		},
		{
			name:         "another tool's re-detect is ignored",
			msg:          installedMsg{toolName: "fzf", installed: "0.5", present: true},
			wantVerified: false,
		},
		{
			name: "an answered outcome is not overwritten",
			msg:  installedMsg{toolName: "rg", installed: "9.9.9", present: true},
			pre: func(m *Model) {
				m.updateOutcome.verified = true
				m.updateOutcome.now = "2.0.0"
			},
			wantVerified: true,
			wantNow:      "2.0.0",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := updateDoneModel(t)
			m = mustModel(m.Update(updateDoneMsg{tool: "rg", elapsed: time.Second}))
			if tt.pre != nil {
				tt.pre(&m)
			}

			m2 := mustModel(m.Update(tt.msg))
			if m2.updateOutcome.verified != tt.wantVerified {
				t.Errorf("verified = %v, want %v", m2.updateOutcome.verified, tt.wantVerified)
			}
			if m2.updateOutcome.now != tt.wantNow {
				t.Errorf("now = %q, want %q", m2.updateOutcome.now, tt.wantNow)
			}
		})
	}
}

// TestUpdateOutcomeVerifiedKeepsAbsence: a re-detect that finds nothing still
// answers the outcome. present is the second, independent result of the probe —
// an update that left no binary on PATH is exactly the case worth reporting, and
// treating "no version string" as "no answer" would hide it behind a block that
// never completes.
func TestUpdateOutcomeVerifiedKeepsAbsence(t *testing.T) {
	shrinkStatusTTL(t)
	m := updateDoneModel(t)
	m = mustModel(m.Update(updateDoneMsg{tool: "rg", elapsed: time.Second}))

	m2 := mustModel(m.Update(installedMsg{toolName: "rg", installed: "", present: false}))
	if !m2.updateOutcome.verified {
		t.Error("verified = false, want the absence recorded as an answer")
	}
	if m2.updateOutcome.nowPresent {
		t.Error("nowPresent = true, want the probe's own result")
	}
}

// TestHelpKeyDismissesCompletedUpdateLog: the update log is sticky in [3] after
// completion, but an explicit [H]/[M] is intent to leave it — the key must clear
// updateLogFor so --help / man is reachable for that tool again.
func TestHelpKeyDismissesCompletedUpdateLog(t *testing.T) {
	for _, key := range []string{"H", "M"} {
		m := New([]loader.ToolMeta{{Name: "git"}})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m = updated.(Model)
		m.focus = focusBrief
		m.updateLog = []string{"updated git"}
		m.updateLogFor = "git"
		m.updatingFor = "" // update finished

		updated, _ = m.Update(keyRunes(key))
		m = updated.(Model)
		if m.updateLogFor != "" {
			t.Errorf("[%s] after completion should clear updateLogFor, got %q", key, m.updateLogFor)
		}
	}
}

// TestHelpKeyKeepsLiveUpdateLog: while an update is still in flight, [H]/[M]
// must NOT drop the live log — updatingFor still names the tool.
func TestHelpKeyKeepsLiveUpdateLog(t *testing.T) {
	for _, key := range []string{"H", "M"} {
		m := New([]loader.ToolMeta{{Name: "git"}})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m = updated.(Model)
		m.focus = focusBrief
		m.updateLog = []string{"downloading"}
		m.updateLogFor = "git"
		m.updatingFor = "git" // in flight

		updated, _ = m.Update(keyRunes(key))
		m = updated.(Model)
		if m.updateLogFor != "git" {
			t.Errorf("[%s] during in-flight update should keep live log, got %q", key, m.updateLogFor)
		}
	}
}

// outcomeModel builds a model whose panel [3] shows fd's update log, sized to
// the 80x24 baseline where the block's width budget is tightest (27 cells).
func outcomeModel(t *testing.T, width int, o updateOutcome) Model {
	t.Helper()
	m := New([]loader.ToolMeta{{Name: "fd", GitHub: "github.com/sharkdp/fd"}})
	m = mustModel(m.Update(tea.WindowSizeMsg{Width: width, Height: 24}))
	m.updateLogFor = "fd"
	m.updateLog = []string{"go: downloading golang.org/x/mod"}
	m.updateOutcome = o
	m.setHelpContent()
	return m
}

// TestUpdateOutcomeBlockRendered: what a finished session actually says. The
// live row is the control — while the update runs the panel must show no
// verdict at all, or the block would be announcing an ending that has not
// happened.
func TestUpdateOutcomeBlockRendered(t *testing.T) {
	tests := []struct {
		name    string
		outcome updateOutcome
		want    []string
		absent  []string
	}{
		{
			name:   "still running",
			want:   []string{"go: downloading"},
			absent: []string{"✓ finished", "✕ failed", "R readme"},
		},
		{
			name:    "finished, not yet verified",
			outcome: updateOutcome{tool: "fd", manager: "go", elapsed: 12 * time.Second},
			want:    []string{"✓ finished · go · 12s", "R readme · H help · M man"},
			absent:  []string{"✕ failed"},
		},
		{
			name: "finished and verified",
			outcome: updateOutcome{tool: "fd", manager: "go", elapsed: 12 * time.Second,
				verified: true, was: "10.2.0", now: "10.3.0", nowPresent: true},
			want: []string{"✓ finished · go · 12s", "✓ fd  v10.2.0 → v10.3.0"},
		},
		{
			name: "the manager did nothing",
			outcome: updateOutcome{tool: "fd", manager: "brew", elapsed: 3 * time.Second,
				verified: true, was: "10.2.0", now: "10.2.0", nowPresent: true},
			want: []string{"✓ finished · brew · 3s", "⚠ fd  still v10.2.0"},
		},
		{
			name: "failed",
			outcome: updateOutcome{tool: "fd", manager: "brew", elapsed: 4 * time.Second,
				err: errUpdateTest},
			want:   []string{"✕ failed · brew · 4s", "exit status 1"},
			absent: []string{"✓ finished"},
		},
		{
			name:    "a run that never started omits the duration",
			outcome: updateOutcome{tool: "fd", manager: "brew", err: errUpdateTest},
			want:    []string{"✕ failed · brew"},
			absent:  []string{"· 0s", "<1s"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := outcomeModel(t, 80, tt.outcome)
			got := stripANSI(m.renderHelpContent())
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("[3] = %q, want it to contain %q", got, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("[3] = %q, want it free of %q", got, absent)
				}
			}
		})
	}
}

// TestUpdateOutcomeBlockReplacesPlaceholder: a manager that succeeds without
// printing anything used to leave [3] reading "starting update…" forever, since
// the placeholder was gated on the buffer alone.
func TestUpdateOutcomeBlockReplacesPlaceholder(t *testing.T) {
	m := outcomeModel(t, 80, updateOutcome{tool: "fd", manager: "go", elapsed: time.Second})
	m.updateLog = nil
	m.setHelpContent()

	got := stripANSI(m.renderHelpContent())
	if strings.Contains(got, "starting update…") {
		t.Errorf("[3] = %q, want the placeholder gone once a session has ended", got)
	}
	if !strings.Contains(got, "✓ finished") {
		t.Errorf("[3] = %q, want the block on its own", got)
	}
}

// TestUpdateOutcomeBlockBelongsToItsLog: the block is rendered under the buffer
// it describes. A tool whose log is not the one on screen must not have its
// verdict painted over another tool's output.
func TestUpdateOutcomeBlockBelongsToItsLog(t *testing.T) {
	m := outcomeModel(t, 80, updateOutcome{tool: "rg", manager: "brew", elapsed: time.Second})
	if got := stripANSI(m.renderHelpContent()); strings.Contains(got, "✓ finished") {
		t.Errorf("[3] = %q, want no block for another tool's outcome", got)
	}
}

// TestUpdateOutcomeBlockLinesNeverWrap: the verdict row and the way out are
// built from styled cells, so they can only be shortened by dropping cells —
// wrapText counts runes and would cut inside an escape sequence. Squeeze the
// panel and the block must lose cells, never gain lines.
func TestUpdateOutcomeBlockLinesNeverWrap(t *testing.T) {
	o := updateOutcome{tool: "fd", manager: "brew", elapsed: 12 * time.Second}
	wide := outcomeModel(t, 200, o)
	narrow := outcomeModel(t, 80, o)
	narrow.helpW = 24 // below the panel minimum, reachable only by hand
	narrow.setHelpContent()

	count := func(m Model) int {
		return len(strings.Split(strings.TrimRight(stripANSI(m.updateOutcomeBlock()), "\n"), "\n"))
	}
	if got, want := count(narrow), count(wide); got != want {
		t.Errorf("narrow block = %d lines, wide = %d; cells must drop, not wrap", got, want)
	}
	got := stripANSI(narrow.updateOutcomeBlock())
	for _, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > narrow.helpWrapWidth() {
			t.Errorf("line %q is %d cells, over the %d budget", line, w, narrow.helpWrapWidth())
		}
	}
	if !strings.Contains(got, "✓ finished") {
		t.Errorf("block = %q, want the leading cell kept", got)
	}
}

// TestUpdateOutcomeBlockColorRoles: the verdict carries the color and the
// metadata beside it does not — the manager name and the duration are labels,
// and Dim is the role for those. Swapping any of these reads as a different
// claim about the update, which is exactly the mutation a plain-text assertion
// would miss.
func TestUpdateOutcomeBlockColorRoles(t *testing.T) {
	forceColorProfile(t)
	th := ui.Default

	tests := []struct {
		name    string
		outcome updateOutcome
		verdict string
		color   lipgloss.Color
	}{
		{
			name:    "success",
			outcome: updateOutcome{tool: "fd", manager: "go", elapsed: 12 * time.Second},
			verdict: "✓ finished", color: th.Ok,
		},
		{
			name:    "failure",
			outcome: updateOutcome{tool: "fd", manager: "go", elapsed: 12 * time.Second, err: errUpdateTest},
			verdict: "✕ failed", color: th.Danger,
		},
		{
			name: "nothing changed",
			outcome: updateOutcome{tool: "fd", manager: "brew", elapsed: time.Second,
				verified: true, was: "10.2.0", now: "10.2.0", nowPresent: true},
			verdict: "⚠ fd  still v10.2.0", color: th.Signal,
		},
		{
			name: "install broken",
			outcome: updateOutcome{tool: "fd", manager: "brew", elapsed: time.Second,
				verified: true, was: "10.2.0", nowPresent: false},
			verdict: "✕ fd  not on PATH", color: th.Danger,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := outcomeModel(t, 120, tt.outcome).updateOutcomeBlock()

			var line string
			for _, l := range strings.Split(block, "\n") {
				if strings.Contains(stripANSI(l), tt.verdict) {
					line = l
					break
				}
			}
			if line == "" {
				t.Fatalf("block = %q, want a line reading %q", block, tt.verdict)
			}
			if !strings.Contains(line, themeSeq(tt.color)) {
				t.Errorf("line %q does not carry %s, the role for this verdict", line, tt.color)
			}
			for _, other := range []lipgloss.Color{th.Ok, th.Signal, th.Danger} {
				if other != tt.color && strings.Contains(line, themeSeq(other)) {
					t.Errorf("line %q also carries %s — one verdict, one role", line, other)
				}
			}
		})
	}

	// The verdict row carries two roles, not one: the metadata beside it steps
	// back to Dim. A whole-line render would leave no Dim on that row at all.
	verdictRow := strings.SplitN(outcomeModel(t, 120, updateOutcome{
		tool: "fd", manager: "go", elapsed: 12 * time.Second}).updateOutcomeBlock(), "\n", 2)[0]
	if !strings.Contains(verdictRow, themeSeq(th.Dim)) {
		t.Errorf("verdict row = %q, want the manager and duration dim beside the verdict", verdictRow)
	}
	if !strings.Contains(verdictRow, themeSeq(th.Ok)) {
		t.Errorf("verdict row = %q, want the verdict itself in Ok", verdictRow)
	}
}

// TestUpdateLogTitleFollowsOutcome: the frame is where a reader who has scrolled
// away from the end of the log learns the update ended. It stays "update" while
// one runs — the two existing tests that pin that spelling describe a live log —
// and names the result once there is one.
func TestUpdateLogTitleFollowsOutcome(t *testing.T) {
	tests := []struct {
		name    string
		outcome updateOutcome
		want    string
	}{
		{name: "live", want: "[3] update "},
		{
			name:    "finished",
			outcome: updateOutcome{tool: "fd", manager: "go", elapsed: time.Second},
			want:    "[3] update finished ",
		},
		{
			name:    "failed",
			outcome: updateOutcome{tool: "fd", manager: "go", elapsed: time.Second, err: errUpdateTest},
			want:    "[3] update failed ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := outcomeModel(t, 120, tt.outcome)
			m.focus = focusHelp
			panel := stripANSI(m.renderHelp())
			top := strings.SplitN(panel, "\n", 2)[0]
			if !strings.Contains(top, tt.want) {
				t.Errorf("top border = %q, want the title %q", top, tt.want)
			}
			// The footer's source cell is the same string, so the two cannot
			// disagree about whether the update is still running.
			if !strings.Contains(panel, strings.TrimPrefix(tt.want, "[3] ")) {
				t.Errorf("panel = %q, want the footer naming the source too", panel)
			}
		})
	}
}
