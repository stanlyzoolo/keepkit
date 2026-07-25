//go:build unix

// Package proc hardens tool subprocesses (help/version probes) against
// misbehaving CLI tools. A tracked tool that ignores its arguments and boots
// a TUI (ratatui/crossterm, tcell, bubbletea …) opens /dev/tty directly and
// toggles raw mode / the alternate screen on the terminal keepkit itself is
// drawing on, tearing the UI apart. Detaching the child from the controlling
// terminal makes that open fail (ENXIO), so such a tool exits immediately and
// harmlessly instead.
package proc

import (
	"os/exec"
	"syscall"
)

// DetachTTY runs cmd in its own session so it has no controlling terminal:
// any attempt to open /dev/tty fails instead of reaching keepkit's screen.
func DetachTTY(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// KillGroup terminates cmd and every process in its group. DetachTTY makes the
// child a session/group leader (Setsid), so a plain cmd.Process.Kill would
// orphan grandchildren such as the tools spawned by `sh -c`. Signalling the
// negated pid delivers SIGKILL to the whole group. Best-effort: a nil or
// already-exited process is a no-op.
func KillGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
