//go:build windows

package term

import (
	"syscall"

	"github.com/charmbracelet/x/xpty"
)

// ptyProcAttr has nothing to add on Windows: ConPTY gives the child its own
// console, and the session-leader/controlling-terminal model the unix build
// needs Setsid/Setctty for does not exist here. ConPty.Start passes this
// straight through to the spawn as ProcAttr.Sys, where nil is fine.
func ptyProcAttr() *syscall.SysProcAttr { return nil }

// closeSlave has nothing to close on Windows: a ConPTY is one pseudo-console
// handle rather than a master/slave fd pair, and ConPty.Close owns all of it.
func closeSlave(xpty.Pty) {}
