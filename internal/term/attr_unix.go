//go:build !windows

package term

import (
	"syscall"

	"github.com/charmbracelet/x/xpty"
)

// ptyProcAttr puts the child in its own session with the pty slave as its
// controlling terminal.
//
// xpty does neither — UnixPty.Start only wires stdin/stdout/stderr to the slave
// — so without this the child inherits keepkit's controlling terminal, job
// control never engages, and Kill's process-group signal would land on
// keepkit's own group.
//
// Ctty is left at its zero value on purpose: it names a descriptor in the
// *child's* table, and fd 0 there is the slave xpty just attached as stdin.
func ptyProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Setctty: true}
}

// closeSlave drops the parent's copy of the pty slave right after the child
// starts. xpty keeps both halves of the pair open in the parent and its Start
// never closes the slave (creack/pty's own StartWithAttrs does, with a defer
// right after Open — xpty rewired the same fds without that line), and on
// Linux a read on the master returns EIO only once *every* slave fd is closed
// — the parent's copy included. Keeping it meant the child's exit never ended
// stream's read, so the Exit event never fired and every consumer of Events()
// hung; macOS revokes the terminal when the session leader exits and answers
// EOF regardless, which is why the leak was invisible locally.
//
// Closing here is safe for Setctty: cmd.Start's fork/exec handshake completes
// before it returns, so the child has already made its own fd 0 — its dup of
// the slave — the controlling terminal. The child's copies are untouched;
// only the parent's leaks.
func closeSlave(p xpty.Pty) {
	if u, ok := p.(*xpty.UnixPty); ok {
		_ = u.Slave().Close()
	}
}
