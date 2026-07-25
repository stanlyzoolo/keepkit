package model

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keeptui/internal/loader"
	"github.com/stanlyzoolo/keeptui/internal/updater"
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
// updateDoneMsg with the exit error.
func TestWaitForChunkCmd(t *testing.T) {
	ch := make(chan updateLine, 3)
	ch <- updateLine{text: "hello", replace: false}
	ch <- updateLine{done: true, err: nil}

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
	if done, ok := msg.(updateDoneMsg); !ok || done.err != nil {
		t.Fatalf("done item: got %#v, want updateDoneMsg{err:nil}", msg)
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

// TestHelpKeyDismissesCompletedUpdateLog: the update log is sticky in [3] after
// completion, but an explicit [h]/[m] is intent to leave it — the key must clear
// updateLogFor so --help / man is reachable for that tool again.
func TestHelpKeyDismissesCompletedUpdateLog(t *testing.T) {
	for _, key := range []string{"h", "m"} {
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

// TestHelpKeyKeepsLiveUpdateLog: while an update is still in flight, [h]/[m]
// must NOT drop the live log — updatingFor still names the tool.
func TestHelpKeyKeepsLiveUpdateLog(t *testing.T) {
	for _, key := range []string{"h", "m"} {
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
