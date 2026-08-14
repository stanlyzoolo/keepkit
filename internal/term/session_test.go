//go:build !windows

package term

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// collect drains a session to its Exit event, returning everything the child
// printed plus the verdict. It fails rather than hangs if the session never
// finishes, so a regression shows up as one named test instead of a timeout on
// the whole package.
func collect(t *testing.T, s *Session, within time.Duration) ([]byte, Exit) {
	t.Helper()

	var out []byte
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				t.Fatal("channel closed before an Exit event")
			}
			switch e := ev.(type) {
			case Data:
				out = append(out, e.Bytes...)
			case Exit:
				// the channel must close right after the Exit
				select {
				case _, open := <-s.Events():
					if open {
						t.Fatal("an event followed Exit")
					}
				case <-time.After(time.Second):
					t.Fatal("channel did not close after Exit")
				}
				return out, e
			}
		case <-deadline:
			t.Fatalf("session did not finish within %s", within)
		}
	}
}

func startTest(t *testing.T, script string) *Session {
	t.Helper()

	s, err := Start("sh", []string{"-c", script}, 80, 24, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// A clean run streams its output and reports a nil error. This doubles as the
// EOF test: reading a pty master whose child has exited answers EIO on Linux
// and EOF on macOS, and neither may become the verdict.
func TestSessionRunsAndExitsClean(t *testing.T) {
	s := startTest(t, "printf hi")

	out, exit := collect(t, s, 10*time.Second)

	if !bytes.Contains(out, []byte("hi")) {
		t.Errorf("output %q does not contain %q", out, "hi")
	}
	if exit.Err != nil {
		t.Errorf("Exit.Err = %v, want nil (a pty read error is not the verdict)", exit.Err)
	}
	if exit.Killed {
		t.Error("Exit.Killed is true for a session nobody killed")
	}
	if exit.Elapsed <= 0 {
		t.Errorf("Exit.Elapsed = %v, want > 0 (stamped in the reader, not by the consumer)", exit.Elapsed)
	}
}

// A non-zero exit rides Exit.Err as an *exec.ExitError, so the caller can read
// the code off it for the overlay's outcome line.
func TestSessionNonZeroExit(t *testing.T) {
	s := startTest(t, "exit 3")

	_, exit := collect(t, s, 10*time.Second)

	var ee *exec.ExitError
	if !errors.As(exit.Err, &ee) {
		t.Fatalf("Exit.Err = %v (%T), want an *exec.ExitError", exit.Err, exit.Err)
	}
	if got := ee.ExitCode(); got != 3 {
		t.Errorf("exit code %d, want 3", got)
	}
}

// A tool that is not installed is not a special case: sh reports it and exits
// 127, which is what the overlay shows. This replaces the old notFoundExit
// mapping the tab launcher needed.
func TestSessionNotInstalledToolExits127(t *testing.T) {
	s := startTest(t, "keepkit-no-such-tool-xyz")

	out, exit := collect(t, s, 10*time.Second)

	var ee *exec.ExitError
	if !errors.As(exit.Err, &ee) {
		t.Fatalf("Exit.Err = %v, want an *exec.ExitError", exit.Err)
	}
	if got := ee.ExitCode(); got != 127 {
		t.Errorf("exit code %d, want 127", got)
	}
	if !strings.Contains(strings.ToLower(string(out)), "not found") {
		t.Errorf("output %q does not carry the shell's not-found message", out)
	}
}

// Kill terminates a child that would otherwise outlive the test, the channel
// closes, and the Exit says it was killed rather than leaving the caller to
// infer it from "signal: killed".
func TestSessionKill(t *testing.T) {
	before := runtime.NumGoroutine()
	s := startTest(t, "sleep 60")

	// let the child actually reach sleep before killing it
	time.Sleep(150 * time.Millisecond)
	s.Kill()

	_, exit := collect(t, s, 10*time.Second)

	if exit.Err == nil {
		t.Error("Exit.Err is nil for a killed child")
	}
	if !exit.Killed {
		t.Error("Exit.Killed is false after Kill()")
	}

	// the reader goroutine must be gone once the channel closed
	waitGoroutines(t, before)
}

// Kill reaches the tools an `sh -c` line spawned, not just the shell: the child
// is a session leader, so the negative-pid signal covers the whole group. A
// grandchild that survived would keep the pty's write end open and the reader
// would never see EOF - which is exactly what this asserts by finishing.
func TestSessionKillReachesGrandchildren(t *testing.T) {
	s := startTest(t, "sleep 60 & sleep 60")

	time.Sleep(150 * time.Millisecond)
	s.Kill()

	_, exit := collect(t, s, 10*time.Second)
	if !exit.Killed {
		t.Error("Exit.Killed is false after Kill()")
	}
}

func TestSessionResize(t *testing.T) {
	s := startTest(t, "sleep 1")

	if err := s.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	s.Kill()
	collect(t, s, 10*time.Second)
}

// Write reaches the child's stdin.
func TestSessionWrite(t *testing.T) {
	s := startTest(t, "read line; printf 'got:%s' \"$line\"")

	time.Sleep(150 * time.Millisecond)
	if _, err := s.Write([]byte("ping\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	out, exit := collect(t, s, 10*time.Second)
	if exit.Err != nil {
		t.Errorf("Exit.Err = %v, want nil", exit.Err)
	}
	if !bytes.Contains(out, []byte("got:ping")) {
		t.Errorf("output %q does not echo the written line", out)
	}
}

// A bogus command fails at Start and leaks nothing: no session is returned, so
// there is no channel to close and no goroutine to leak.
func TestStartBogusShell(t *testing.T) {
	before := runtime.NumGoroutine()

	s, err := Start("keepkit-no-such-shell-xyz", nil, 80, 24, nil)
	if err == nil {
		_ = s.Close()
		t.Fatal("Start with a bogus shell returned no error")
	}
	if s != nil {
		t.Errorf("Start returned a session (%v) alongside an error", s)
	}

	waitGoroutines(t, before)
}

// Start's env is what the child sees, and TERM/COLORTERM are appended so the
// tool renders in colour. Appending is enough because os/exec keeps the last
// value for a duplicate key - this pins that, since an inherited TERM would
// otherwise win and a tmux/screen TERM would cost the overlay its colours.
func TestSessionEnvOverridesTerm(t *testing.T) {
	s, err := Start("sh", []string{"-c", "printf '%s|%s' \"$TERM\" \"$COLORTERM\""},
		80, 24, []string{"TERM=dumb"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	out, _ := collect(t, s, 10*time.Second)

	if !bytes.Contains(out, []byte("xterm-256color|truecolor")) {
		t.Errorf("child saw %q, want TERM=xterm-256color and COLORTERM=truecolor", out)
	}
}

// The bytes a session streams are what a vt emulator turns back into a screen -
// this is the whole contract between internal/term and the model, checked once
// here so the model's own tests can use a fake session with a clear conscience.
func TestSessionOutputRendersInEmulator(t *testing.T) {
	s := startTest(t, "printf 'hello pty'")

	out, _ := collect(t, s, 10*time.Second)

	emu := vt.NewEmulator(40, 5)
	if _, err := emu.Write(out); err != nil {
		t.Fatalf("emulator write: %v", err)
	}
	if !strings.Contains(emu.Render(), "hello pty") {
		t.Errorf("emulator rendered %q, want it to contain %q", emu.Render(), "hello pty")
	}
}

// Closing the emulator's input pipe is how a copy goroutine parked in Read is
// stopped, and it must stay race-free: Emulator.Close() writes an
// unsynchronised bool that Read checks on every call (charmbracelet/x#879), so
// the obvious teardown is the one that trips -race. Closing InputPipe()'s
// writer goes through io.Pipe's own synchronisation instead and never touches
// that bool. The model's overlay teardown depends on this; see
// docs/research/pty-stack.md.
func TestSessionInputPipeCloseIsRaceFree(t *testing.T) {
	emu := vt.NewEmulator(20, 5)

	var relayed []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				relayed = append(relayed, buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()

	// drive the screen state from this goroutine while the copy goroutine
	// reads the input side: the two halves must not race either.
	for range 50 {
		if _, err := emu.Write([]byte("x")); err != nil {
			t.Fatalf("emulator write: %v", err)
		}
		emu.SendKey(uv.KeyPressEvent{Code: 'a'})
	}

	closer, ok := emu.InputPipe().(io.Closer)
	if !ok {
		t.Fatalf("InputPipe() is %T, not an io.Closer - the overlay teardown assumes it is", emu.InputPipe())
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("close input pipe: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the copy goroutine did not exit after the input pipe was closed")
	}
	if len(relayed) != 50 {
		t.Errorf("relayed %d bytes, want 50", len(relayed))
	}
}

// waitGoroutines gives the runtime a moment to retire finished goroutines
// before comparing counts - an immediate read races the scheduler.
func waitGoroutines(t *testing.T, before int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		got := runtime.NumGoroutine()
		if got <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("goroutines: %d before, %d after - the reader leaked", before, got)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
