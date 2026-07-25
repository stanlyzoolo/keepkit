//go:build windows

package proc

import (
	"os/exec"
	"syscall"
)

// DetachTTY starts cmd without a console (DETACHED_PROCESS), the Windows
// analog of dropping the controlling terminal: a child that tries to draw a
// TUI cannot reach keepkit's console via CONOUT$. Captured stdout/stderr pipes
// keep working.
func DetachTTY(cmd *exec.Cmd) {
	const detachedProcess = 0x00000008
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess, HideWindow: true}
}

// KillGroup terminates cmd. Windows has no session-leader/process-group model
// like unix, so this falls back to killing the direct process. Best-effort: a
// nil or already-exited process is a no-op.
func KillGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
