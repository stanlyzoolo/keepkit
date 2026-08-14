package model

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/term"
	"github.com/stanlyzoolo/keepkit/internal/ui"
)

// fakeTermSession drives the overlay handlers without a pty: the test feeds
// events in and reads back what the model wrote, killed or resized. Every
// method is safe to call from the input relay goroutine, so the fake is usable
// under -race.
type fakeTermSession struct {
	events chan term.Event

	mu      sync.Mutex
	written []byte
	killed  int
	closed  int
	resized [][2]int
	writeIn error
}

func newFakeSession(buffer int) *fakeTermSession {
	return &fakeTermSession{events: make(chan term.Event, buffer)}
}

func (f *fakeTermSession) Events() <-chan term.Event { return f.events }

func (f *fakeTermSession) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeIn != nil {
		return 0, f.writeIn
	}
	f.written = append(f.written, p...)
	return len(p), nil
}

func (f *fakeTermSession) Resize(w, h int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resized = append(f.resized, [2]int{w, h})
	return nil
}

func (f *fakeTermSession) Kill() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed++
}

func (f *fakeTermSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

// input returns everything the model has relayed to the tool so far. It polls,
// because the relay is a goroutine and a bare read would race the handoff.
func (f *fakeTermSession) input(t *testing.T, want string) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		got := string(f.written)
		f.mu.Unlock()
		if want == "" || strings.Contains(got, want) || time.Now().After(deadline) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fakeTermSession) resizes() [][2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]int(nil), f.resized...)
}

func (f *fakeTermSession) counts() (killed, closed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killed, f.closed
}

// overlayModel is a model sitting in modeToolOverlay with the session already
// adopted, i.e. the state everything after termStartedMsg runs in.
func overlayModel(t *testing.T, f *fakeTermSession) Model {
	t.Helper()

	m := newTestModel(focusTools)
	m.mode = modeToolOverlay
	m.termToolName = "git"
	m.termW, m.termH = 40, 10

	nm := mustModel(m.Update(termStartedMsg{session: f}))
	if nm.termSession == nil {
		t.Fatal("termStartedMsg did not adopt the session")
	}
	if nm.termEmu == nil {
		t.Fatal("termStartedMsg did not create the emulator")
	}
	t.Cleanup(func() { nm.closeToolOverlay() })
	return nm
}

// The started message is what creates the emulator - on the Update goroutine,
// which is the whole reason the emulator lives on the model and not inside
// internal/term.
func TestTermStartedCreatesEmulatorAtGeometrySize(t *testing.T) {
	f := newFakeSession(4)
	m := overlayModel(t, f)

	if got := m.termEmu.Width(); got != 40 {
		t.Errorf("emulator width = %d, want 40 (termW)", got)
	}
	if got := m.termEmu.Height(); got != 10 {
		t.Errorf("emulator height = %d, want 10 (termH)", got)
	}
	if m.termExit != nil {
		t.Error("termExit is set on a session that just started")
	}
	if !m.termRunning() {
		t.Error("termRunning() is false right after the session started")
	}
}

// A session that arrives after the overlay is gone must not be adopted and must
// not be left running: nothing could reach it afterwards.
func TestTermStartedAfterCloseKillsTheSession(t *testing.T) {
	f := newFakeSession(4)
	m := newTestModel(focusTools) // still modeNormal - the overlay never opened

	nm := mustModel(m.Update(termStartedMsg{session: f}))

	if nm.termSession != nil {
		t.Error("a session was adopted outside modeToolOverlay")
	}
	killed, closed := f.counts()
	if killed == 0 || closed == 0 {
		t.Errorf("stale session left running: killed=%d closed=%d, want both > 0", killed, closed)
	}
}

// A chunk reaches a real vt emulator and shows up in Render() - pure Go, no
// pty involved, which is what makes the whole overlay assertable in model
// tests.
func TestTermChunkReachesTheEmulator(t *testing.T) {
	f := newFakeSession(4)
	m := overlayModel(t, f)

	nm := mustModel(m.Update(termChunkMsg{session: f, data: []byte("hello overlay")}))

	if got := nm.termEmu.Render(); !strings.Contains(got, "hello overlay") {
		t.Errorf("emulator rendered %q, want it to contain %q", got, "hello overlay")
	}
}

// A chunk belonging to a session the model no longer holds is dropped, and -
// the part that matters - it does not re-subscribe: a dead session's channel
// must not keep a command chain alive for the rest of the process.
func TestTermChunkFromStaleSessionIsDropped(t *testing.T) {
	f := newFakeSession(4)
	m := overlayModel(t, f)

	stale := newFakeSession(4)
	nm, cmd := m.Update(termChunkMsg{session: stale, data: []byte("ghost")})

	if cmd != nil {
		t.Error("a stale chunk re-subscribed to its session")
	}
	if got := nm.(Model).termEmu.Render(); strings.Contains(got, "ghost") {
		t.Error("a stale chunk was written to the live emulator")
	}
}

// The drain folds queued output into one message and, when it runs into the
// exit, delivers the data first and the exit as the next message. Losing that
// order loses a short-lived CLI's final screen, which is the whole point of
// staying open until esc.
func TestWaitForTermChunkDeliversDataBeforeExit(t *testing.T) {
	f := newFakeSession(8)
	f.events <- term.Data{Bytes: []byte("first ")}
	f.events <- term.Data{Bytes: []byte("second")}
	f.events <- term.Exit{Err: nil, Elapsed: 3 * time.Second}
	close(f.events)

	msg := waitForTermChunkCmd(f)()

	chunk, ok := msg.(termChunkMsg)
	if !ok {
		t.Fatalf("first message is %T, want termChunkMsg", msg)
	}
	if got := string(chunk.data); got != "first second" {
		t.Errorf("drained data = %q, want %q - the drain did not fold both chunks", got, "first second")
	}
	if chunk.exit == nil {
		t.Fatal("the drain reached the exit and dropped it")
	}
	if chunk.exit.elapsed != 3*time.Second {
		t.Errorf("elapsed = %v, want 3s (stamped by the session, not by Update)", chunk.exit.elapsed)
	}

	// and the model plays them out in that order: emulator first, exit next
	m := overlayModel(t, f)
	nm, cmd := m.Update(chunk)
	if got := nm.(Model).termEmu.Render(); !strings.Contains(got, "first second") {
		t.Errorf("emulator rendered %q, want the drained data", got)
	}
	if nm.(Model).termExit != nil {
		t.Error("the exit was recorded in the same update as the data it must follow")
	}
	if cmd == nil {
		t.Fatal("no follow-up command carried the exit")
	}
	if _, ok := cmd().(termExitMsg); !ok {
		t.Errorf("follow-up message is %T, want termExitMsg", cmd())
	}
}

// A drain that starts on the exit reports it directly.
func TestWaitForTermChunkExitOnly(t *testing.T) {
	f := newFakeSession(2)
	f.events <- term.Exit{Err: errors.New("boom"), Elapsed: time.Second, Killed: true}

	msg := waitForTermChunkCmd(f)()

	exit, ok := msg.(termExitMsg)
	if !ok {
		t.Fatalf("message is %T, want termExitMsg", msg)
	}
	if exit.err == nil || !exit.killed {
		t.Errorf("exit = %+v, want the error and killed flag carried through", exit)
	}
}

// The exit message stores the verdict and stops the chain. It writes no status
// message: the screen is the answer.
func TestTermExitStoresVerdictAndStopsTheChain(t *testing.T) {
	f := newFakeSession(4)
	m := overlayModel(t, f)

	nm, cmd := m.Update(termExitMsg{err: errors.New("exit status 1"), elapsed: 2 * time.Second})
	got := nm.(Model)

	if got.termExit == nil {
		t.Fatal("termExit is nil after termExitMsg")
	}
	if got.termExit.elapsed != 2*time.Second {
		t.Errorf("elapsed = %v, want 2s", got.termExit.elapsed)
	}
	if cmd != nil {
		t.Error("termExitMsg returned a command - the chain must stop at the exit")
	}
	if got.termRunning() {
		t.Error("termRunning() is still true after the exit")
	}
	if got.statusMsg != "" {
		t.Errorf("statusMsg = %q, want empty - the overlay itself reports the outcome", got.statusMsg)
	}
	if got.mode != modeToolOverlay {
		t.Errorf("mode = %v, want modeToolOverlay - the overlay stays until esc", got.mode)
	}
}

// An exit arriving after the overlay closed changes nothing.
func TestTermExitOutsideOverlayIsIgnored(t *testing.T) {
	m := newTestModel(focusTools)

	nm := mustModel(m.Update(termExitMsg{err: errors.New("late")}))

	if nm.termExit != nil {
		t.Error("a late exit was recorded outside the overlay")
	}
}

// A start failure never reaches the user as a closed overlay: it arrives as an
// exit that the outcome line can explain.
func TestStartTermCmdFailureBecomesExit(t *testing.T) {
	msg := startTermCmd("keepkit-no-such-shell-xyz", nil, 40, 10)()

	exit, ok := msg.(termExitMsg)
	if !ok {
		t.Fatalf("message is %T, want termExitMsg", msg)
	}
	if exit.err == nil {
		t.Error("a failed start carried no error")
	}
	if !exit.startFailed {
		t.Error("startFailed is false on a start that failed")
	}
}

// The input relay carries what Update sends to the tool, and the emulator is
// what encodes it - which is why the relay exists at all (x/vt has no exported
// key encoder). This is the -race lifecycle test: the relay must relay while
// the screen is being written from Update, and must be gone after teardown.
func TestTermInputRelayAndTeardown(t *testing.T) {
	f := newFakeSession(4)
	m := overlayModel(t, f)

	m.termEmu.SendText("ls -la\r")
	// drive the screen from this goroutine at the same time: the relay must
	// not touch screen state
	if _, err := m.termEmu.Write([]byte("banner")); err != nil {
		t.Fatalf("emulator write: %v", err)
	}

	if got := f.input(t, "ls -la"); !strings.Contains(got, "ls -la\r") {
		t.Errorf("tool received %q, want it to contain %q", got, "ls -la\r")
	}

	done := m.termInput.done
	m.closeToolOverlay()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the input relay outlived the overlay")
	}
	if m.termSession != nil || m.termEmu != nil || m.termInput != nil || m.termExit != nil {
		t.Error("closeToolOverlay left overlay state behind")
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v after teardown, want modeNormal", m.mode)
	}
	killed, closed := f.counts()
	if killed == 0 || closed == 0 {
		t.Errorf("teardown left the session alive: killed=%d closed=%d", killed, closed)
	}
}

// Teardown is safe on an overlay that never got a session - the window between
// the keypress and termStartedMsg.
func TestCloseToolOverlayBeforeStart(t *testing.T) {
	m := newTestModel(focusTools)
	m.mode = modeToolOverlay
	m.termToolName = "git"

	m.closeToolOverlay() // must not panic on the nil session/emulator/relay

	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal", m.mode)
	}
}

// The third overlay rides the shared modal predicate, which is what gates the
// mouse and View().
func TestToolOverlayIsAnOverlay(t *testing.T) {
	m := newTestModel(focusTools)
	m.mode = modeToolOverlay

	if !m.overlayVisible() {
		t.Error("overlayVisible() = false in modeToolOverlay")
	}
	if m.apiOverlayVisible() {
		t.Error("apiOverlayVisible() = true in modeToolOverlay - it is a different overlay")
	}
}

// View picks the tool overlay rather than falling through to the API-status
// panel, which is what the old two-way if would have done.
func TestViewPicksTheToolOverlay(t *testing.T) {
	f := newFakeSession(4)
	m := overlayModel(t, f)
	m.ready = true
	m.applyLayout()

	nm := mustModel(m.Update(termChunkMsg{session: f, data: []byte("in-overlay-marker")}))

	view := nm.View()
	if !strings.Contains(view, "in-overlay-marker") {
		t.Error("View() does not show the tool overlay's content")
	}
	if strings.Contains(view, "github token") {
		t.Error("View() fell through to the API-status overlay")
	}
}

// Every key reaches the tool while it runs — including the ones keepkit would
// otherwise act on. esc must not close the overlay (vim needs it) and ctrl+c
// must not quit keepkit (it is the tool's interrupt), which is the whole claim
// "the tool has the keyboard" makes.
func TestToolOverlayForwardsEveryKeyWhileRunning(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyMsg
		want string
	}{
		{"letters", keyRunes("ls"), "ls"},
		{"space", tea.KeyMsg{Type: tea.KeySpace}, " "},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, "\r"},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}, "\t"},
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f"},
		{"esc goes to the tool", tea.KeyMsg{Type: tea.KeyEsc}, "\x1b"},
		{"ctrl+c goes to the tool", tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03"},
		{"ctrl+d", tea.KeyMsg{Type: tea.KeyCtrlD}, "\x04"},
		{"up arrow", tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A"},
		{"down arrow", tea.KeyMsg{Type: tea.KeyDown}, "\x1b[B"},
		{"home", tea.KeyMsg{Type: tea.KeyHome}, "\x1b[H"},
		{"page up", tea.KeyMsg{Type: tea.KeyPgUp}, "\x1b[5~"},
		{"delete", tea.KeyMsg{Type: tea.KeyDelete}, "\x1b[3~"},
		{"shift+tab", tea.KeyMsg{Type: tea.KeyShiftTab}, "\x1b[Z"},
		{"f1", tea.KeyMsg{Type: tea.KeyF1}, "\x1bOP"},
		{"f5", tea.KeyMsg{Type: tea.KeyF5}, "\x1b[15~"},
		{"ctrl+left", tea.KeyMsg{Type: tea.KeyCtrlLeft}, "\x1b[1;5D"},
		{"shift+right", tea.KeyMsg{Type: tea.KeyShiftRight}, "\x1b[1;2C"},
		{"alt+rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true}, "\x1bb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeSession(4)
			m := overlayModel(t, f)

			nm := mustModel(m.Update(tt.key))

			if got := f.input(t, tt.want); !strings.Contains(got, tt.want) {
				t.Errorf("tool received %q, want it to contain %q", got, tt.want)
			}
			if nm.mode != modeToolOverlay {
				t.Errorf("mode = %v, want the overlay to keep the keyboard", nm.mode)
			}
		})
	}
}

// esc while the tool runs is the tool's, not the overlay's: closing here would
// make vim unusable, which is the case the whole design is built around.
func TestToolOverlayEscDoesNotCloseWhileRunning(t *testing.T) {
	f := newFakeSession(4)
	m := overlayModel(t, f)

	nm := mustModel(m.Update(tea.KeyMsg{Type: tea.KeyEsc}))

	if nm.mode != modeToolOverlay {
		t.Errorf("mode = %v after esc, want the overlay still open", nm.mode)
	}
	if nm.termSession == nil {
		t.Error("esc tore down a running session")
	}
}

// ctrl+c must not reach keepkit's global quit. That case sits after the mode
// dispatch, so this is structural - asserted because the failure is keepkit
// exiting out from under a running tool.
func TestToolOverlayCtrlCDoesNotQuit(t *testing.T) {
	f := newFakeSession(4)
	m := overlayModel(t, f)

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if cmd != nil {
		t.Error("ctrl+c in the overlay returned a command (tea.Quit reached)")
	}
	if nm.(Model).mode != modeToolOverlay {
		t.Error("ctrl+c left the overlay")
	}
}

// ctrl+\ is the reserved chord: it kills but stays in the mode, because the
// outcome line the user killed something to read arrives with termExitMsg.
func TestToolOverlayKillChord(t *testing.T) {
	f := newFakeSession(4)
	m := overlayModel(t, f)

	nm := mustModel(m.Update(tea.KeyMsg{Type: tea.KeyCtrlBackslash}))

	killed, _ := f.counts()
	if killed != 1 {
		t.Errorf("Kill called %d times, want 1", killed)
	}
	if nm.mode != modeToolOverlay {
		t.Errorf("mode = %v after ctrl+\\, want to stay until the exit arrives", nm.mode)
	}
	if nm.termExit != nil {
		t.Error("ctrl+\\ synthesised an exit instead of waiting for the session's")
	}
	if got := f.input(t, ""); strings.Contains(got, "\x1c") {
		t.Error("the kill chord was also forwarded to the tool")
	}
}

// After the exit the overlay is a still image: esc closes, everything else does
// nothing, and nothing is forwarded because there is nothing to forward to.
func TestToolOverlayAfterExit(t *testing.T) {
	f := newFakeSession(4)
	base := overlayModel(t, f)
	base = mustModel(base.Update(termExitMsg{elapsed: time.Second}))

	t.Run("esc closes", func(t *testing.T) {
		m := base
		nm := mustModel(m.Update(tea.KeyMsg{Type: tea.KeyEsc}))
		if nm.mode != modeNormal {
			t.Errorf("mode = %v after esc, want modeNormal", nm.mode)
		}
		if nm.termSession != nil || nm.termEmu != nil || nm.termExit != nil {
			t.Error("esc left overlay state behind")
		}
	})

	t.Run("other keys are no-ops", func(t *testing.T) {
		for _, key := range []tea.KeyMsg{
			keyRunes("q"), tea.KeyMsg{Type: tea.KeyEnter}, tea.KeyMsg{Type: tea.KeyCtrlC},
			tea.KeyMsg{Type: tea.KeyCtrlBackslash},
		} {
			m := base
			nm, cmd := m.Update(key)
			if nm.(Model).mode != modeToolOverlay {
				t.Errorf("key %v left the exited overlay", key)
			}
			if cmd != nil {
				t.Errorf("key %v returned a command from an exited overlay", key)
			}
		}
		// nothing was written to a tool that is no longer there
		if got := f.input(t, ""); got != "" {
			t.Errorf("an exited overlay forwarded %q to the dead session", got)
		}
	})
}

// The window between the keypress and termStartedMsg has no session and no
// emulator. A key there must be dropped, not dereferenced: a panic would
// re-panic through logx.Recover and take keepkit down.
func TestToolOverlayKeysBeforeStart(t *testing.T) {
	m := newTestModel(focusTools)
	m.mode = modeToolOverlay
	m.termToolName = "git"

	for _, key := range []tea.KeyMsg{
		keyRunes("x"), tea.KeyMsg{Type: tea.KeyEsc}, tea.KeyMsg{Type: tea.KeyCtrlBackslash},
		tea.KeyMsg{Type: tea.KeyUp},
	} {
		nm, cmd := m.Update(key) // must not panic
		if nm.(Model).mode != modeToolOverlay {
			t.Errorf("key %v left the pre-start overlay", key)
		}
		if cmd != nil {
			t.Errorf("key %v returned a command before the session existed", key)
		}
	}
}

// The overlay's dispatch is deliberately not wrapped in flushPendingLaunch,
// unlike every sibling mode: a deferred exec fallback firing tea.ExecProcess on
// the keystroke that closes the overlay would seize the terminal.
func TestToolOverlayDoesNotFlushPendingLaunch(t *testing.T) {
	f := newFakeSession(4)
	m := overlayModel(t, f)
	m.pendingLaunchName = "fd"
	m.pendingLaunchCommand = "fd --version"
	m = mustModel(m.Update(termExitMsg{elapsed: time.Second}))

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if cmd != nil {
		t.Error("closing the overlay dispatched the deferred exec fallback")
	}
	if nm.(Model).pendingLaunchName != "fd" {
		t.Error("the deferred fallback was consumed by the overlay's dispatch")
	}
}

// laidOutOverlay is a ready model of the given size sitting in the overlay with
// the session adopted and the emulator sized by termGeometry — the state every
// rendering assertion runs against.
func laidOutOverlay(t *testing.T, w, h int, f *fakeTermSession) Model {
	t.Helper()

	m := New([]loader.ToolMeta{{Name: "yazi"}}).WithAppVersion("v0.1.0")
	mm := mustModel(m.Update(tea.WindowSizeMsg{Width: w, Height: h}))
	mm.mode = modeToolOverlay
	mm.termToolName = "yazi"
	mm.termW, mm.termH, _ = mm.termGeometry()

	nm := mustModel(mm.Update(termStartedMsg{session: f}))
	t.Cleanup(func() { nm.closeToolOverlay() })
	return nm
}

// blockSize reports the overlay frame's size in visible cells.
//
// Measured on the frame itself rather than on the composited View: the layout
// behind it is full of rounded corners of its own (three panels and the status
// bar), so scanning the finished screen for a frame finds the panels' borders
// and reports a size that never changes no matter what the overlay does.
func blockSize(t *testing.T, frame string) (rows, cols int) {
	t.Helper()

	lines := strings.Split(stripANSI(frame), "\n")
	for _, line := range lines {
		if w := len([]rune(line)); w > cols {
			cols = w
		}
	}
	if cols == 0 {
		t.Fatal("the overlay frame rendered nothing")
	}
	return len(lines), cols
}

// The block must not move when the tool exits. Height is held by the reserved
// exit row and width by padding every row to the body width; without either,
// PlaceOverlay re-centres the whole frame at the exact moment the user is
// reading the outcome.
func TestToolOverlayBlockDoesNotMoveAtExit(t *testing.T) {
	for _, dim := range [][2]int{{80, 24}, {120, 34}} {
		t.Run(fmt.Sprintf("%dx%d", dim[0], dim[1]), func(t *testing.T) {
			f := newFakeSession(4)
			m := laidOutOverlay(t, dim[0], dim[1], f)
			m = mustModel(m.Update(termChunkMsg{session: f, data: []byte("a wide enough line of tool output\r\n")}))

			runRows, runCols := blockSize(t, m.renderToolOverlay())

			exited := mustModel(m.Update(termExitMsg{elapsed: 12 * time.Second}))
			exitRows, exitCols := blockSize(t, exited.renderToolOverlay())

			if runRows != exitRows {
				t.Errorf("block height %d running, %d exited — the reserved row is not doing its job", runRows, exitRows)
			}
			if runCols != exitCols {
				t.Errorf("block width %d running, %d exited — a row is not padded to the body width", runCols, exitCols)
			}
		})
	}
}

// Every row inside the frame is exactly the body width, whatever the tool
// printed: a short row leaves a ragged edge and a long one widens the block.
func TestToolOverlayRowsAreExactlyBodyWidth(t *testing.T) {
	f := newFakeSession(4)
	m := laidOutOverlay(t, 100, 30, f)
	// a line carrying SGR, and one longer than the body can hold
	m = mustModel(m.Update(termChunkMsg{session: f,
		data: []byte("\x1b[1;31mred\x1b[0m short\r\n" + strings.Repeat("x", 200))}))

	rows := m.termBodyRows(m.termW, m.termH)
	for i, row := range rows {
		if got := lipgloss.Width(row); got != m.termW {
			t.Errorf("body row %d is %d cells, want %d", i, got, m.termW)
		}
	}

	frame := m.renderToolOverlay()
	widths := map[int]bool{}
	for _, line := range strings.Split(stripANSI(frame), "\n") {
		widths[len([]rune(line))] = true
	}
	if len(widths) != 1 {
		t.Errorf("frame lines have %d distinct widths %v, want all equal", len(widths), widths)
	}
}

// A tool whose name is wider than the body must not widen the block. lipgloss
// sizes a border to its widest line, so an unclamped title row is the one thing
// that can push the frame past the geometry everything else agreed on — and it
// would push it past the screen edge, where PlaceOverlay clips silently.
func TestToolOverlayLongToolNameDoesNotWidenTheBlock(t *testing.T) {
	f := newFakeSession(4)
	m := laidOutOverlay(t, 100, 30, f)
	want := frameWidth(t, m.renderToolOverlay())

	m.termToolName = strings.Repeat("very-long-tool-name-", 8)
	if got := frameWidth(t, m.renderToolOverlay()); got != want {
		t.Errorf("frame is %d cells wide with a long tool name, want %d", got, want)
	}

	// and the same once it has exited, where the title row loses its right cell
	m.termExit = &termExitMsg{elapsed: time.Second}
	if got := frameWidth(t, m.renderToolOverlay()); got != want {
		t.Errorf("exited frame is %d cells wide with a long tool name, want %d", got, want)
	}
}

// frameWidth is the visible width of a rendered frame's widest line.
func frameWidth(t *testing.T, frame string) int {
	t.Helper()

	widest := 0
	for _, line := range strings.Split(stripANSI(frame), "\n") {
		if w := len([]rune(line)); w > widest {
			widest = w
		}
	}
	return widest
}

// The title names the tool and, while it runs, carries the one key that is
// still keepkit's. Both go once the tool has exited: by then esc is the only
// key that does anything, and the exit row is what says so.
func TestToolOverlayTitleAndKillHint(t *testing.T) {
	f := newFakeSession(4)
	m := laidOutOverlay(t, 100, 30, f)

	running := stripANSI(m.renderToolOverlay())
	if !strings.Contains(running, "yazi") {
		t.Error("the frame does not name the tool")
	}
	if !strings.Contains(running, `ctrl+\ kill`) {
		t.Error("the frame does not advertise the kill chord while the tool runs")
	}

	exited := stripANSI(mustModel(m.Update(termExitMsg{elapsed: time.Second})).renderToolOverlay())
	if strings.Contains(exited, `ctrl+\ kill`) {
		t.Error("the kill chord is still advertised after the tool exited")
	}
}

// Before termStartedMsg the overlay is already on screen and has to say
// something rather than showing a blank rectangle.
func TestToolOverlayStartingBody(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "yazi"}})
	mm := mustModel(m.Update(tea.WindowSizeMsg{Width: 100, Height: 30}))
	mm.mode = modeToolOverlay
	mm.termToolName = "yazi"

	if got := stripANSI(mm.renderToolOverlay()); !strings.Contains(got, "starting yazi…") {
		t.Errorf("pre-start frame does not say what it is waiting for:\n%s", got)
	}
}

// The child's cursor is a reverse-video cell while it runs, so a shell prompt
// does not look frozen; it goes once the tool has exited, because a cursor on a
// dead screen invites typing.
func TestToolOverlayCursor(t *testing.T) {
	// Reverse video is styling, and the default test profile strips it — the
	// assertion would pass against a cursor that was never drawn.
	forceColor(t)
	f := newFakeSession(4)
	m := laidOutOverlay(t, 100, 30, f)
	m = mustModel(m.Update(termChunkMsg{session: f, data: []byte("$ ")}))

	pos := m.termEmu.CursorPosition()
	if pos.X != 2 || pos.Y != 0 {
		t.Fatalf("emulator cursor at (%d,%d), want (2,0) after %q", pos.X, pos.Y, "$ ")
	}

	rows := m.termBodyRows(m.termW, m.termH)
	if !strings.Contains(rows[0], "\x1b[7m") {
		t.Errorf("cursor row carries no reverse-video sequence: %q", rows[0])
	}
	if got := lipgloss.Width(rows[0]); got != m.termW {
		t.Errorf("the cursor splice changed the row width to %d, want %d", got, m.termW)
	}

	exited := mustModel(m.Update(termExitMsg{elapsed: time.Second}))
	if strings.Contains(exited.termBodyRows(exited.termW, exited.termH)[0], "\x1b[7m") {
		t.Error("the cursor is still drawn after the tool exited")
	}
}

// The four outcomes each get their own line, and each names what happened plus
// the way out. No status message accompanies them — the screen is the answer.
func TestToolOverlayExitRow(t *testing.T) {
	tests := []struct {
		name string
		exit termExitMsg
		want []string
	}{
		{"clean", termExitMsg{elapsed: 12 * time.Second}, []string{"✓ exited", "12s", "esc close"}},
		{"non-zero", termExitMsg{err: exitErrorWithCode(t, 3), elapsed: 4 * time.Second},
			[]string{"✕ exit 3", "4s", "esc close"}},
		{"killed", termExitMsg{err: errors.New("signal: killed"), elapsed: 90 * time.Second, killed: true},
			[]string{"✕ killed", "1m30s", "esc close"}},
		{"failed to start", termExitMsg{err: errors.New("no such file"), startFailed: true},
			[]string{"✕ failed to start", "no such file", "esc close"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeSession(4)
			m := laidOutOverlay(t, 120, 34, f)
			m = mustModel(m.Update(tt.exit))

			row := stripANSI(m.termExitRow(m.termW))
			for _, want := range tt.want {
				if !strings.Contains(row, want) {
					t.Errorf("exit row %q does not contain %q", row, want)
				}
			}
			if m.statusMsg != "" {
				t.Errorf("statusMsg = %q, want the exit row to be the only report", m.statusMsg)
			}
		})
	}
}

// While the tool runs the row is reserved and blank: it exists so the block's
// height never changes, not to say anything yet.
func TestToolOverlayExitRowBlankWhileRunning(t *testing.T) {
	f := newFakeSession(4)
	m := laidOutOverlay(t, 100, 30, f)

	if got := m.termExitRow(m.termW); got != "" {
		t.Errorf("exit row = %q while the tool runs, want it reserved and blank", got)
	}
}

// The status bar has a branch of its own, because the global one advertises six
// keys the running tool has taken over — including a q quit that cannot fire.
func TestToolOverlayStatusBar(t *testing.T) {
	f := newFakeSession(4)
	m := laidOutOverlay(t, 100, 30, f)

	running := stripANSI(m.renderStatusBar())
	if !strings.Contains(running, "yazi running") || !strings.Contains(running, `ctrl+\ kill`) {
		t.Errorf("running status bar = %q", running)
	}
	for _, dead := range []string{"q quit", "t track", "a api"} {
		if strings.Contains(running, dead) {
			t.Errorf("status bar still advertises %q, which the tool has taken over", dead)
		}
	}

	exited := stripANSI(mustModel(m.Update(termExitMsg{elapsed: time.Second})).renderStatusBar())
	if !strings.Contains(exited, "yazi exited") || !strings.Contains(exited, "esc close") {
		t.Errorf("exited status bar = %q", exited)
	}
	if strings.Count(exited, "\n") != 2 {
		t.Errorf("status bar wrapped: %q", exited)
	}
}

// Geometry: 70% of the screen, minus the frame's own chrome, refusing below the
// minimums. The 80x24 baseline must succeed — it is the default terminal.
func TestTermGeometry(t *testing.T) {
	tests := []struct {
		w, h         int
		wantW, wantH int
		wantOK       bool
	}{
		// 80x24: background is 22 rows, 70% -> 56x15, body 52x11
		{80, 24, 52, 11, true},
		{120, 34, 80, 18, true},
		{60, 20, termMinBodyW, termMinBodyH, false}, // too short: body would be 8 rows
		{40, 40, termMinBodyW, 22, false},           // too narrow: body would be 24 cols, height is fine
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			w, h, ok := termGeometryFor(tt.w, tt.h)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("body = %dx%d, want %dx%d", w, h, tt.wantW, tt.wantH)
			}
			if w < termMinBodyW || h < termMinBodyH {
				t.Errorf("body %dx%d is under the floor — a shrunk terminal must clamp, not vanish", w, h)
			}
		})
	}
}

// Before the first WindowSizeMsg there is no layout: refused explicitly rather
// than by the percentages agreeing on the floor by coincidence.
func TestTermGeometryNotReady(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "yazi"}})
	m.width, m.height = 200, 60 // set, but no WindowSizeMsg has been applied

	if _, _, ok := m.termGeometry(); ok {
		t.Error("termGeometry() reported ok before the first relayout")
	}
}

// A resize follows through to both halves: the emulator is re-laid-out and the
// tool is told, so its redraw lands on a screen of the right shape.
func TestToolOverlayResize(t *testing.T) {
	f := newFakeSession(4)
	m := laidOutOverlay(t, 120, 34, f)
	beforeW, beforeH := m.termW, m.termH

	nm := mustModel(m.Update(tea.WindowSizeMsg{Width: 160, Height: 44}))

	if nm.termW == beforeW && nm.termH == beforeH {
		t.Fatalf("geometry unchanged after a resize: %dx%d", nm.termW, nm.termH)
	}
	if nm.termEmu.Width() != nm.termW || nm.termEmu.Height() != nm.termH {
		t.Errorf("emulator is %dx%d, want %dx%d", nm.termEmu.Width(), nm.termEmu.Height(), nm.termW, nm.termH)
	}
	got := f.resizes()
	if len(got) == 0 {
		t.Fatal("the tool was never told about the resize")
	}
	if last := got[len(got)-1]; last[0] != nm.termW || last[1] != nm.termH {
		t.Errorf("tool told %dx%d, want %dx%d", last[0], last[1], nm.termW, nm.termH)
	}
}

// Shrinking past the minimum clamps to the floor and keeps the tool running:
// resizing somebody's editor into nothing is bad, killing it outright is worse.
func TestToolOverlayResizeBelowMinimumClamps(t *testing.T) {
	f := newFakeSession(4)
	m := laidOutOverlay(t, 120, 34, f)

	nm := mustModel(m.Update(tea.WindowSizeMsg{Width: 50, Height: 16}))

	if nm.termW != termMinBodyW || nm.termH != termMinBodyH {
		t.Errorf("geometry = %dx%d, want the %dx%d floor", nm.termW, nm.termH, termMinBodyW, termMinBodyH)
	}
	if nm.mode != modeToolOverlay || nm.termSession == nil {
		t.Error("a shrink tore the session down")
	}
}

// The background is dimmed like every other overlay, so the tool's screen is
// the only full-colour thing on it.
func TestToolOverlayDimsTheBackground(t *testing.T) {
	forceColor(t)
	f := newFakeSession(4)
	m := laidOutOverlay(t, 120, 34, f)

	view := m.View()
	dim := lipgloss.NewStyle().Foreground(ui.Default.Dim).Render("")
	if seq, _, _ := strings.Cut(dim, "m"); !strings.Contains(view, seq) {
		t.Error("the layout behind the overlay is not dimmed")
	}
}

// exitErrorWithCode produces a real *exec.ExitError carrying code, so the exit
// row is exercised against the shape a session actually reports rather than a
// zero value whose ExitCode() is -1.
func exitErrorWithCode(t *testing.T, code int) error {
	t.Helper()

	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("sh -c 'exit %d' returned %v, want an *exec.ExitError", code, err)
	}
	return ee
}

var _ tea.Model = Model{}
