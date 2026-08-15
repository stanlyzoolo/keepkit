// Package term runs a command on a pseudo-terminal and streams what it prints.
//
// It is the architectural slot the old tab launcher vacated: bottom of the
// import graph, no TUI knowledge. A Session owns the pty and one reader
// goroutine; everything it observes leaves through Events() as a value, so the
// model can consume it with the same waitForChunkCmd pattern the update
// streamer uses. The terminal *emulator* deliberately lives on the model, not
// here — only Bubble Tea's Update goroutine may touch its screen state.
//
// The caller builds argv (keepkit's model has shellCommand for that) and hands
// it to Start, which keeps this package goos-agnostic.
//
// This package writes no config and does no logx logging: a failure rides
// Exit.Err to the caller, which is the surface that can actually show it. That
// is why it needs no TestMain seam, unlike loader/version/logx.
package term

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/xpty"
	"github.com/stanlyzoolo/keepkit/internal/proc"
)

// readChunk bounds one read off the pty master. A full-screen redraw of a
// large terminal is tens of kilobytes, so this sizes one repaint into one
// event rather than a dozen.
const readChunk = 32 * 1024

// Event is what a Session reports: Data for output, Exit exactly once, last.
type Event interface{ termEvent() }

// Data is a chunk of bytes the child wrote. Bytes is owned by the receiver —
// the reader copies out of its buffer before sending.
type Data struct{ Bytes []byte }

func (Data) termEvent() {}

// Exit is the final event on the channel; the channel closes right after it.
// Err is nil for a clean exit and an *exec.ExitError for a non-zero one, so a
// caller can read the code off it. Elapsed is stamped in the reader goroutine,
// never by the consumer — a consumer that stamped it would make completion
// depend on when it got round to reading, which is not a testable quantity.
type Exit struct {
	Err     error
	Elapsed time.Duration
	// Killed reports that the exit followed a Kill() call rather than the
	// child deciding to stop. The session is the only place that knows both
	// halves of that, so it answers rather than making the caller correlate
	// its own keypress against an exit status that says "signal: killed".
	Killed bool
}

func (Exit) termEvent() {}

// Session is one command running on one pty.
type Session struct {
	pty    xpty.Pty
	cmd    *exec.Cmd
	events chan Event

	killed    atomic.Bool
	closeOnce sync.Once
}

// Start runs shell with args on a fresh w×h pty and begins streaming.
//
// env is the child's environment; nil inherits keepkit's. TERM and COLORTERM
// are appended either way — os/exec keeps the last value for a duplicate key,
// so appending overrides an inherited TERM without having to scan for it. The
// working directory is inherited.
//
// On unix the child is put in its own session with the pty slave as its
// controlling terminal. xpty does not do this itself (it only wires stdio), and
// without it job control never engages and the kill path would signal the wrong
// process group. It is also why proc.DetachTTY must never be applied here: it
// assigns SysProcAttr wholesale and would drop Setctty.
func Start(shell string, args []string, w, h int, env []string) (*Session, error) {
	pty, err := xpty.NewPty(w, h)
	if err != nil {
		return nil, err
	}

	if env == nil {
		env = os.Environ()
	}
	cmd := exec.Command(shell, args...)
	cmd.Env = append(append([]string{}, env...), "TERM=xterm-256color", "COLORTERM=truecolor")
	cmd.SysProcAttr = ptyProcAttr()

	if err := pty.Start(cmd); err != nil {
		_ = pty.Close()
		return nil, err
	}
	// The child owns its dup of the slave now; the parent's copy must go, or
	// on Linux the master never answers EIO after the child exits and the
	// Exit event never fires. See closeSlave.
	closeSlave(pty)

	s := &Session{
		pty:    pty,
		cmd:    cmd,
		events: make(chan Event, eventBuffer),
	}
	go s.stream()
	return s, nil
}

// eventBuffer lets the reader stay ahead of a consumer that only drains once
// per Bubble Tea message, without letting output queue without bound: past it
// the reader blocks, the pty buffer fills, and the child is throttled — which
// is the correct back-pressure for a tool printing faster than we can paint.
const eventBuffer = 64

// Events returns the stream. It yields zero or more Data events, then exactly
// one Exit, then closes.
func (s *Session) Events() <-chan Event { return s.events }

// stream is the one goroutine per session: pty master → Data events, then the
// process verdict → Exit, then close.
func (s *Session) stream() {
	started := time.Now()
	buf := make([]byte, readChunk)

	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.events <- Data{Bytes: chunk}
		}
		if err != nil {
			// Any read error ends the stream and none of them is the
			// verdict. A master whose child has exited answers EIO on
			// Linux and EOF on macOS, and a master closed by Kill/Close
			// answers os.ErrClosed — all three are normal termination.
			// What actually happened comes from the wait below.
			break
		}
	}

	err := xpty.WaitProcess(context.Background(), s.cmd)
	s.events <- Exit{Err: err, Elapsed: time.Since(started), Killed: s.killed.Load()}
	close(s.events)
}

// Write sends input to the child.
func (s *Session) Write(p []byte) (int, error) { return s.pty.Write(p) }

// Resize tells the child its terminal changed size.
func (s *Session) Resize(w, h int) error { return s.pty.Resize(w, h) }

// Kill terminates the child and everything it spawned. The unix child is a
// session leader (see Start), so its pid doubles as its process-group id and
// proc.KillGroup's negative-pid signal reaches the tools an `sh -c` line
// started. Best-effort: an already-exited process is a no-op, and the verdict
// still arrives as the Exit event once the reader drains.
func (s *Session) Kill() {
	s.killed.Store(true)
	_ = proc.KillGroup(s.cmd)
}

// Close releases the pty. Calling it while the child lives sends it SIGHUP;
// the normal path is to Close after the Exit event has already arrived. Safe
// to call more than once.
//
// os.ErrClosed is filtered out: xpty's Close closes master then slave, and the
// unix slave was already dropped at Start (see closeSlave) — its second close
// is the expected shape of a clean teardown, not a failure to report.
func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.pty.Close()
		if errors.Is(err, os.ErrClosed) {
			err = nil
		}
	})
	return err
}
