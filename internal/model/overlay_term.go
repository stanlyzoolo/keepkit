package model

import (
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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
// A session arriving when the overlay is no longer waiting for one is killed
// rather than adopted. As wired that cannot happen — the start-failure exit
// and a started session are alternative results of the same command, and no
// message handler writes m.mode without a gate that refuses modeToolOverlay —
// but a fake can produce it and a future mode writer could, and a guard kept
// for that future must actually be safe when it fires: the drain below is
// what lets the reader goroutine end (and the killed child be reaped) when a
// chatty tool has already filled the event buffer, where a bare Kill+Close
// would park the reader on a channel send forever.
func (m *Model) handleTermStarted(msg termStartedMsg) tea.Cmd {
	if m.mode != modeToolOverlay || m.termSession != nil {
		msg.session.Kill()
		_ = msg.session.Close()
		// Bounded: the closed master fails the reader's next read, the wait
		// reaps the killed child, Exit lands and the channel closes.
		return safeCmd("drainStaleTermSession", func() tea.Msg {
			for range msg.session.Events() {
			}
			return nil
		})
	}

	m.termSession = msg.session
	m.termEmu = vt.NewEmulator(m.termW, m.termH)
	m.termInput = startTermInput(m.termEmu, msg.session)
	// The pty was created at the geometry the run-prompt enter captured. A
	// WindowSizeMsg processed in the start window has already moved termW/termH
	// with no session to follow it, and resizeToolOverlay's nothing-changed
	// early return then sees the new size stored — so the pty would keep the
	// stale winsize until the next real resize. Unconditional on purpose: in
	// the common no-resize case this is a same-size TIOCSWINSZ, which the
	// kernel drops without a SIGWINCH, so the child never notices.
	_ = msg.session.Resize(m.termW, m.termH)
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
		if msg.Paste {
			// A paste is one block of text, not typed keys: Emulator.Paste
			// brackets it with the ?2004 markers exactly when the tool asked
			// for them, which is what keeps vim from auto-indenting every
			// pasted line. No alt prefix — a paste has no modifier.
			emu.Paste(string(msg.Runes))
			return
		}
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

// Overlay geometry. The block covers termOverlayPercent of the screen in both
// directions; the minimums are what a tool actually needs to be usable rather
// than merely drawn — below them the keypress refuses instead of opening a
// window nothing fits in.
const (
	termOverlayPercent = 70
	termMinBodyW       = 40
	termMinBodyH       = 10

	// termChromeRows is what the frame spends on itself inside the border: the
	// title row and the exit row. The exit row is reserved from the very start
	// and left blank while the tool runs, so the block's height never changes
	// and PlaceOverlay cannot re-centre it the moment the tool exits.
	termChromeRows = 2
	// termChromeCols is the border (2) plus OverlayBorder's Padding(0, 1) (2).
	termChromeCols = 4
)

// termGeometry is the emulator's body size for the current terminal, plus
// whether the screen is big enough for one at all.
//
// The size is always clamped to the floor and ok is a separate answer, because
// the two callers want different things: the keypress refuses on !ok, while a
// terminal shrunk *mid-session* keeps rendering at the minimum — resizing the
// user's editor into nothing is bad, killing it outright is worse.
//
// !m.ready is checked first and explicitly, the toggleZoom idiom: before the
// first WindowSizeMsg every dimension is zero and the percentages below would
// agree on a floor-sized overlay by coincidence rather than by measurement.
func (m Model) termGeometry() (w, h int, ok bool) {
	if !m.ready {
		return termMinBodyW, termMinBodyH, false
	}
	return termGeometryFor(m.width, m.height)
}

// termGeometryFor is the pure core (the planFor/baseFor idiom), so the clamps
// are testable without a laid-out model.
func termGeometryFor(width, height int) (w, h int, ok bool) {
	// The overlay composites over the layout, not over the terminal: View wraps
	// the composite in Margin(1, 0), so two rows are already spoken for and an
	// overlay sized against the raw height would be clipped at the bottom by
	// PlaceOverlay — silently, which is how a missing exit line would look.
	bgH := height - 2

	outerW := min(width*termOverlayPercent/100, width)
	outerH := min(bgH*termOverlayPercent/100, bgH)

	w, h = outerW-termChromeCols, outerH-2-termChromeRows
	ok = w >= termMinBodyW && h >= termMinBodyH
	return max(w, termMinBodyW), max(h, termMinBodyH), ok
}

// resizeToolOverlay follows a terminal resize through to the tool: the emulator
// is re-laid-out first and the pty is told second, so the child's redraw lands
// on a screen that is already the right shape.
func (m *Model) resizeToolOverlay() {
	if m.mode != modeToolOverlay {
		return
	}
	w, h, _ := m.termGeometry()
	if w == m.termW && h == m.termH {
		return
	}
	m.termW, m.termH = w, h
	if m.termEmu != nil {
		m.termEmu.Resize(w, h)
	}
	if m.termSession != nil {
		_ = m.termSession.Resize(w, h)
	}
}

// renderToolOverlay draws the embedded terminal: a title row naming the tool,
// the tool's own screen, and a bottom row that is blank while it runs and
// carries the verdict once it has exited.
func (m Model) renderToolOverlay() string {
	s := m.sty()
	w, h, _ := m.termGeometry()

	// Title. The kill chord is advertised here rather than only in the status
	// bar because this frame is what the user is looking at, and it is the one
	// key that still belongs to keepkit while the tool has the keyboard. It
	// goes once the tool has exited — by then the only key that does anything
	// is esc, and the exit row says so.
	title := s.EmphasisBold.Render(m.termToolName)
	titleRow := title
	if m.termExit == nil {
		hint := m.hint(`ctrl+\`, "kill")
		if pad := w - lipgloss.Width(title) - lipgloss.Width(hint); pad > 0 {
			titleRow = title + strings.Repeat(" ", pad) + hint
		}
	}

	rows := make([]string, 0, h+termChromeRows)
	rows = append(rows, titleRow)
	rows = append(rows, m.termBodyRows(w, h)...)
	rows = append(rows, m.termExitRow(w))

	// Every row is padded to exactly w before the frame goes round it. lipgloss
	// sizes a border to its widest line, so without this the block would be as
	// wide as whatever the tool happened to print — and, worse, it would *change
	// width at exit*, when the title loses its kill hint and the exit row
	// replaces a blank. PlaceOverlay centres what it is given, so a one-cell
	// change is the whole block jumping sideways at the moment the user is
	// reading the outcome. The reserved row keeps the height constant; this
	// keeps the width constant.
	for i, row := range rows {
		rows[i] = termRow(row, w)
	}
	return s.OverlayBorder.Render(strings.Join(rows, "\n"))
}

// termRow fits one row to exactly w visible cells.
//
// Both directions go through x/ansi: padding is plain spaces appended after the
// styling (never inside it), and a row too wide is cut by visible column rather
// than by byte or rune index, which would land inside an escape sequence that
// the terminal then executes.
func termRow(row string, w int) string {
	switch width := lipgloss.Width(row); {
	case width < w:
		return row + strings.Repeat(" ", w-width)
	case width > w:
		return ansi.Truncate(row, w, "")
	default:
		return row
	}
}

// termBodyRows is the tool's screen, exactly h rows tall.
//
// Before termStartedMsg there is no emulator: the overlay is already on screen
// (the keypress opened it) and has to say something, which is the same
// "starting …" idiom the update log uses for its own pre-output window.
func (m Model) termBodyRows(w, h int) []string {
	rows := make([]string, h)
	if m.termEmu == nil {
		rows[0] = m.sty().Dim.Render("starting " + m.termToolName + "…")
		return rows
	}

	lines := strings.Split(m.termEmu.Render(), "\n")
	for i := range rows {
		if i < len(lines) {
			// Padded here rather than by the final pass, because the cursor
			// splice below addresses a *column*: on a short line, a cursor
			// past its end would otherwise be spliced in right after the last
			// glyph instead of where the tool actually put it.
			rows[i] = termRow(lines[i], w)
		} else {
			rows[i] = strings.Repeat(" ", w)
		}
	}
	return m.withTermCursor(rows, w, h)
}

// withTermCursor reverse-videos the cell the tool has its cursor on, so a shell
// prompt or an editor's caret is visible instead of the screen looking frozen.
// Hidden once the tool has exited: a cursor on a dead screen invites typing.
//
// The splice goes through x/ansi rather than a byte offset: an emulator line
// carries SGR runs, and cutting one at a rune index lands inside an escape
// sequence, which the terminal then executes. ansi.Truncate/TruncateLeft cut by
// *visible* column and keep the styling on both sides intact.
func (m Model) withTermCursor(rows []string, w, h int) []string {
	if m.termExit != nil || m.termEmu == nil {
		return rows
	}

	pos := m.termEmu.CursorPosition()
	if pos.X < 0 || pos.X >= w || pos.Y < 0 || pos.Y >= h || pos.Y >= len(rows) {
		return rows
	}

	content := " "
	if cell := m.termEmu.CellAt(pos.X, pos.Y); cell != nil && cell.Content != "" {
		content = cell.Content
	}

	line := rows[pos.Y]
	rows[pos.Y] = ansi.Truncate(line, pos.X, "") +
		m.sty().TermCursor.Render(content) +
		ansi.TruncateLeft(line, pos.X+1, "")
	return rows
}

// termExitRow is the reserved bottom row: blank while the tool runs, the
// verdict plus the way out once it has finished.
//
// No status message accompanies it — the screen the tool left behind is the
// answer, and this row is the caption on it.
func (m Model) termExitRow(w int) string {
	if m.termExit == nil {
		return ""
	}
	s := m.sty()
	sep := s.Dim.Render(footerSep)

	verdict := s.Ok.Render("✓ exited")
	switch {
	case m.termExit.startFailed:
		verdict = s.Danger.Render("✕ failed to start")
	case m.termExit.killed:
		verdict = s.Danger.Render("✕ killed")
	case m.termExit.err != nil:
		verdict = s.Danger.Render("✕ exit " + termExitCode(m.termExit.err))
	}

	cells := []string{verdict}
	// A start failure has no elapsed worth printing and a reason that is the
	// whole point, so it takes the middle cell the others spend on duration.
	if m.termExit.startFailed {
		if reason := m.termExit.err; reason != nil {
			cells = append(cells, s.Dim.Render(reason.Error()))
		}
	} else if el := formatElapsed(m.termExit.elapsed); el != "" {
		cells = append(cells, s.Dim.Render(el))
	}
	cells = append(cells, m.hint("esc", "close"))

	return fitCells(cells, sep, w, 0)
}

// termExitCode names the code an exited tool reported, falling back to "?" for
// an error that is not an exit status at all (a wait that failed on its own).
func termExitCode(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return strconv.Itoa(ee.ExitCode())
	}
	return "?"
}

// termRunning reports that the overlay has a tool that has not exited yet —
// the state where every key belongs to the tool and only ctrl+\ is reserved.
// A session still starting counts as running: its keys are dropped, not
// treated as an exited overlay's.
func (m Model) termRunning() bool {
	return m.mode == modeToolOverlay && m.termExit == nil
}
