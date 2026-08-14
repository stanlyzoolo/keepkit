package model

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/term"
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

var _ tea.Model = Model{}
