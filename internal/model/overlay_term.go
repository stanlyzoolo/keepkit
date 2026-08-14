package model

import (
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"

	"github.com/stanlyzoolo/keepkit/internal/term"
)

// termSession is the model's view of a running tool: everything the overlay
// needs and nothing else, so a test can drive the handlers with a fake instead
// of spawning a real pty. The narrow-interface idiom main.go uses for
// restarter, and the package-level assertion below is what keeps the real
// session bound to it.
type termSession interface {
	Events() <-chan term.Event
	Write(p []byte) (int, error)
	Resize(w, h int) error
	Kill()
	Close() error
}

var _ termSession = (*term.Session)(nil)

// termStartedMsg carries the live session back to Update, which is where the
// emulator is created — the emulator's screen state is touched by Update and
// nothing else, so it cannot be built in the command goroutine.
type termStartedMsg struct {
	session termSession
}

// termChunkMsg is one drained batch of the tool's output.
//
// exit is set when the drain ran into the session's final event. It rides along
// rather than being dropped because the drain has already taken it off the
// channel and there is nowhere to put it back: the handler writes data to the
// emulator first and re-emits exit as the *next* message, which is what keeps a
// short-lived CLI's final screen on display. Losing it the other way round —
// exit first, data discarded — would defeat the whole esc-after-exit design.
type termChunkMsg struct {
	session termSession
	data    []byte
	exit    *termExitMsg
}

// termExitMsg is the tool's verdict. elapsed and killed are stamped by the
// session goroutine, never here: time.Now() inside Update would make completion
// non-deterministic in tests, and only the session knows whether the exit
// followed a kill.
//
// startFailed marks the case where there is no session at all — term.Start
// itself failed, so the overlay opens straight into its outcome line. It is an
// explicit field rather than an inference from a nil m.termSession because the
// renderer reads it long after the distinction would have to be reconstructed.
type termExitMsg struct {
	err         error
	elapsed     time.Duration
	killed      bool
	startFailed bool
}

// exitMsgFrom converts the session's final event into the model's message.
func exitMsgFrom(e term.Exit) termExitMsg {
	return termExitMsg{err: e.Err, elapsed: e.Elapsed, killed: e.Killed}
}

// termInput relays the emulator's encoded input to the tool.
//
// This goroutine exists because the pinned x/vt has no exported key encoder and
// its SendKey encodes against emulator modes (DECCKM — whether arrows are
// \x1bOA or \x1b[A) that no accessor exposes, so keys must go through the
// emulator and come back out of its input pipe. Update calls SendKey/SendText;
// this goroutine is the reader that unblocks the internal io.Pipe those write
// into, and it touches no screen state — the Update-only rule holds.
type termInput struct {
	done chan struct{}
}

// startTermInput wires emu's input side to sess. One goroutine per session,
// started exactly once, when both ends exist.
func startTermInput(emu *vt.Emulator, sess termSession) *termInput {
	ti := &termInput{done: make(chan struct{})}
	go func() {
		defer close(ti.done)
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				if _, werr := sess.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return ti
}

// stop ends the relay and waits for the goroutine, so it can never outlive the
// overlay. Callers close the session first: that way a relay parked in
// Session.Write fails fast instead of holding the teardown.
//
// It closes the emulator's *input pipe* rather than the emulator. Emulator.Close
// writes an unsynchronised bool that Emulator.Read checks on every call
// (charmbracelet/x#879) — reproduced under -race in internal/term — while
// closing the pipe writer unblocks the parked Read through io.Pipe's own
// synchronisation and touches nothing shared. The Close fallback is for a future
// x/vt that stops handing out an io.Closer; see docs/research/pty-stack.md.
func (ti *termInput) stop(emu *vt.Emulator) {
	if ti == nil {
		return
	}
	if emu != nil {
		if closer, ok := emu.InputPipe().(io.Closer); ok {
			_ = closer.Close()
		} else {
			_ = emu.Close()
		}
	}
	<-ti.done
}

// startTermCmd runs shell+args on a fresh w×h pty off the Update thread.
//
// A start failure never reaches the caller as an error: it becomes an immediate
// termExitMsg, so the overlay that just opened shows why instead of closing
// again under the user.
func startTermCmd(shell string, args []string, w, h int) tea.Cmd {
	return safeCmd("startTermCmd", func() tea.Msg {
		s, err := term.Start(shell, args, w, h, nil)
		if err != nil {
			return termExitMsg{err: err, startFailed: true}
		}
		return termStartedMsg{session: s}
	})
}

// waitForTermChunkCmd blocks for the session's next event and folds everything
// already queued behind it into one message — a full-screen redraw arrives as
// several reads and repainting once per read would spend a Bubble Tea frame on
// each. It is the re-subscribe half of the channel idiom waitForChunkCmd uses
// for the update streamer.
//
// The drain stops at the final event and hands it back through termChunkMsg.exit
// rather than swallowing it; see that field's doc for why the order matters.
func waitForTermChunkCmd(s termSession) tea.Cmd {
	return safeCmd("waitForTermChunkCmd", func() tea.Msg {
		ev, ok := <-s.Events()
		if !ok {
			// A closed channel with no Exit behind it is not something the
			// session can produce, but a fake can: end the overlay rather
			// than re-subscribing to a channel that will never answer.
			return termExitMsg{}
		}

		switch e := ev.(type) {
		case term.Exit:
			return exitMsgFrom(e)
		case term.Data:
			data := e.Bytes
			for {
				select {
				case next, open := <-s.Events():
					if !open {
						return termChunkMsg{session: s, data: data, exit: &termExitMsg{}}
					}
					switch n := next.(type) {
					case term.Data:
						data = append(data, n.Bytes...)
					case term.Exit:
						exit := exitMsgFrom(n)
						return termChunkMsg{session: s, data: data, exit: &exit}
					}
				default:
					return termChunkMsg{session: s, data: data}
				}
			}
		}
		return nil
	})
}

// handleTermStarted adopts the live session: the emulator is created here, on
// the Update goroutine, and the input relay is started now that both ends
// exist.
//
// A session arriving after the overlay is gone — the user's tool failed to
// start and esc closed the outcome line while term.Start was still in flight,
// or a stale start lost its race — is killed rather than adopted. Leaving it
// running would strand a pty nothing can reach.
func (m *Model) handleTermStarted(msg termStartedMsg) tea.Cmd {
	if m.mode != modeToolOverlay || m.termSession != nil {
		msg.session.Kill()
		_ = msg.session.Close()
		return nil
	}

	m.termSession = msg.session
	m.termEmu = vt.NewEmulator(m.termW, m.termH)
	m.termInput = startTermInput(m.termEmu, msg.session)
	return waitForTermChunkCmd(msg.session)
}

// handleTermChunk folds one batch of output into the emulator.
//
// A chunk whose session is not the current one is stale — its overlay is
// already closed — and is dropped without re-subscribing, so a dead session's
// channel cannot keep a command chain alive.
func (m *Model) handleTermChunk(msg termChunkMsg) tea.Cmd {
	if msg.session == nil || msg.session != m.termSession {
		return nil
	}

	if m.termEmu != nil && len(msg.data) > 0 {
		_, _ = m.termEmu.Write(msg.data)
	}

	if msg.exit != nil {
		// Deliver the exit as the next message, after this batch has been
		// painted: the final screen is the point of staying open.
		exit := *msg.exit
		return func() tea.Msg { return exit }
	}
	return waitForTermChunkCmd(msg.session)
}

// handleTermExit records the verdict and stops the chain. It writes nothing
// else: the overlay stays exactly as the tool left it until esc, and the
// screen is the answer — no status message.
func (m *Model) handleTermExit(msg termExitMsg) {
	if m.mode != modeToolOverlay {
		return
	}
	exit := msg
	m.termExit = &exit
}

// closeToolOverlay tears the overlay down and returns to modeNormal.
//
// The order is load-bearing: the session goes first so the relay's pending
// Session.Write fails fast, then the relay is stopped and waited for, and only
// then are the emulator and the rest of the state dropped. Reversing the first
// two can park the relay in a write nobody will drain.
func (m *Model) closeToolOverlay() {
	if m.termSession != nil {
		m.termSession.Kill()
		_ = m.termSession.Close()
	}
	m.termInput.stop(m.termEmu)

	m.termSession = nil
	m.termInput = nil
	m.termEmu = nil
	m.termExit = nil
	m.termToolName = ""
	m.termW, m.termH = 0, 0
	m.mode = modeNormal
}

// updateToolOverlay owns every keystroke while the overlay is open — that is
// what "the tool has the keyboard" means, and it is why the mode consumes keys
// it has no use for rather than letting them fall through to the normal-mode
// map underneath. Key translation, the ctrl+\ kill chord and esc-after-exit
// land in Task 3 of the tool-overlay plan.
func (m Model) updateToolOverlay(_ tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m, nil
}

// renderToolOverlay draws the embedded terminal. Placeholder: the frame,
// geometry, cursor and outcome line land in Task 4 of the tool-overlay plan.
func (m Model) renderToolOverlay() string {
	body := "starting " + m.termToolName + "…"
	if m.termEmu != nil {
		body = m.termEmu.Render()
	}
	return m.sty().OverlayBorder.Render(body)
}

// termRunning reports that the overlay has a tool that has not exited yet —
// the state where every key belongs to the tool and only ctrl+\ is reserved.
// A session still starting counts as running: its keys are dropped, not
// treated as an exited overlay's.
func (m Model) termRunning() bool {
	return m.mode == modeToolOverlay && m.termExit == nil
}
