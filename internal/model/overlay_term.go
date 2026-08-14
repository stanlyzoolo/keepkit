package model

import (
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
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

// termKillKey is the one chord the overlay keeps for itself. Everything else —
// esc, ctrl+c, q — belongs to the tool, so the way out of a hung program cannot
// be a key the program might want. ctrl+\ is the choice because it is already
// SIGQUIT by convention and no editor binds it.
const termKillKey = tea.KeyCtrlBackslash

// updateToolOverlay owns every keystroke while the overlay is open — that is
// what "the tool has the keyboard" means, and it is why the mode consumes keys
// it has no use for rather than letting them fall through to the normal-mode
// map underneath.
//
// While the tool runs, every key is translated and sent, ctrl+\ kills. Once it
// has exited the overlay is a still image: esc closes it and nothing else does
// anything — in particular the keys are no longer forwarded, because there is
// nothing left to forward them to.
func (m Model) updateToolOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.termExit != nil {
		if msg.Type == tea.KeyEsc {
			m.closeToolOverlay()
		}
		return m, nil
	}

	if msg.Type == termKillKey {
		// Kill, but stay in the mode: the verdict arrives as termExitMsg and
		// that is what turns the overlay into its outcome line. Leaving here
		// would drop the log the user just killed something to read.
		if m.termSession != nil {
			m.termSession.Kill()
		}
		return m, nil
	}

	// Between the keypress that opened the overlay and termStartedMsg there is
	// no emulator and no session. Keys are dropped rather than queued: the
	// window is milliseconds, and a nil deref here would re-panic through
	// logx.Recover and take keepkit down with it.
	if m.termEmu != nil {
		sendToolKey(m.termEmu, msg)
	}
	return m, nil
}

// termNamedKeys maps the Bubble Tea key types that need the emulator's own
// encoder — the ones whose bytes depend on emulator state (DECCKM turns the
// arrows and Home/End into their \x1bO… forms, DECNKM the keypad). Everything
// here goes through SendKey for that reason; see the file header on why the
// model cannot encode them itself.
var termNamedKeys = map[tea.KeyType]uv.KeyPressEvent{
	tea.KeyUp:       {Code: uv.KeyUp},
	tea.KeyDown:     {Code: uv.KeyDown},
	tea.KeyRight:    {Code: uv.KeyRight},
	tea.KeyLeft:     {Code: uv.KeyLeft},
	tea.KeyHome:     {Code: uv.KeyHome},
	tea.KeyEnd:      {Code: uv.KeyEnd},
	tea.KeyPgUp:     {Code: uv.KeyPgUp},
	tea.KeyPgDown:   {Code: uv.KeyPgDown},
	tea.KeyInsert:   {Code: uv.KeyInsert},
	tea.KeyDelete:   {Code: uv.KeyDelete},
	tea.KeyShiftTab: {Code: uv.KeyTab, Mod: uv.ModShift},
	tea.KeyF1:       {Code: uv.KeyF1},
	tea.KeyF2:       {Code: uv.KeyF2},
	tea.KeyF3:       {Code: uv.KeyF3},
	tea.KeyF4:       {Code: uv.KeyF4},
	tea.KeyF5:       {Code: uv.KeyF5},
	tea.KeyF6:       {Code: uv.KeyF6},
	tea.KeyF7:       {Code: uv.KeyF7},
	tea.KeyF8:       {Code: uv.KeyF8},
	tea.KeyF9:       {Code: uv.KeyF9},
	tea.KeyF10:      {Code: uv.KeyF10},
	tea.KeyF11:      {Code: uv.KeyF11},
	tea.KeyF12:      {Code: uv.KeyF12},
}

// termModifiedKeys covers the modified cursor keys, which the pinned x/vt does
// not encode at all — its SendKey falls through to a default that emits nothing
// for a modified special key, so routing these through the emulator would
// silently swallow ctrl+left in every editor.
//
// They are safe to write out here because, unlike the bare arrows, the modified
// forms do not depend on DECCKM: xterm's CSI 1;<mod><final> shape is what every
// terminal sends whatever the cursor-keys mode. The modifier digit is
// 1+shift(1)+alt(2)+ctrl(4), and the finals are A/B/C/D for the arrows and H/F
// for home/end.
var termModifiedKeys = map[tea.KeyType]string{
	tea.KeyShiftUp:        "\x1b[1;2A",
	tea.KeyShiftDown:      "\x1b[1;2B",
	tea.KeyShiftRight:     "\x1b[1;2C",
	tea.KeyShiftLeft:      "\x1b[1;2D",
	tea.KeyShiftHome:      "\x1b[1;2H",
	tea.KeyShiftEnd:       "\x1b[1;2F",
	tea.KeyCtrlUp:         "\x1b[1;5A",
	tea.KeyCtrlDown:       "\x1b[1;5B",
	tea.KeyCtrlRight:      "\x1b[1;5C",
	tea.KeyCtrlLeft:       "\x1b[1;5D",
	tea.KeyCtrlHome:       "\x1b[1;5H",
	tea.KeyCtrlEnd:        "\x1b[1;5F",
	tea.KeyCtrlShiftUp:    "\x1b[1;6A",
	tea.KeyCtrlShiftDown:  "\x1b[1;6B",
	tea.KeyCtrlShiftRight: "\x1b[1;6C",
	tea.KeyCtrlShiftLeft:  "\x1b[1;6D",
	tea.KeyCtrlShiftHome:  "\x1b[1;6H",
	tea.KeyCtrlShiftEnd:   "\x1b[1;6F",
	tea.KeyCtrlPgUp:       "\x1b[5;5~",
	tea.KeyCtrlPgDown:     "\x1b[6;5~",
}

// sendToolKey translates one Bubble Tea key into what the tool should read.
//
// Bubble Tea's control-key types are the control bytes themselves — KeyEnter is
// 0x0d, KeyEsc 0x1b, KeyCtrlA 0x01, KeyBackspace 0x7f — and none of those is
// mode-dependent, so writing the byte is exactly what the emulator's encoder
// would have produced for them. Alt arrives as a flag rather than a key, and an
// alt-modified key is its unmodified form behind an ESC.
func sendToolKey(emu *vt.Emulator, msg tea.KeyMsg) {
	alt := ""
	if msg.Alt {
		alt = "\x1b"
	}

	switch msg.Type {
	case tea.KeyRunes:
		emu.SendText(alt + string(msg.Runes))
	case tea.KeySpace:
		emu.SendText(alt + " ")
	default:
		if seq, ok := termModifiedKeys[msg.Type]; ok {
			emu.SendText(alt + seq)
			return
		}
		if key, ok := termNamedKeys[msg.Type]; ok {
			if msg.Alt {
				key.Mod |= uv.ModAlt
			}
			emu.SendKey(key)
			return
		}
		// What is left is the control range, where the key type *is* the byte.
		// A type outside it is one Bubble Tea knows and this build does not
		// (a newer F-key, say); dropping it beats sending a stray rune.
		if msg.Type >= 0 && msg.Type <= 0x7f {
			emu.SendText(alt + string(rune(msg.Type)))
		}
	}
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
