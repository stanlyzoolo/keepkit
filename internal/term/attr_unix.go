//go:build !windows

package term

import "syscall"

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
