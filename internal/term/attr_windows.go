//go:build windows

package term

import "syscall"

// ptyProcAttr has nothing to add on Windows: ConPTY gives the child its own
// console, and the session-leader/controlling-terminal model the unix build
// needs Setsid/Setctty for does not exist here. ConPty.Start passes this
// straight through to the spawn as ProcAttr.Sys, where nil is fine.
func ptyProcAttr() *syscall.SysProcAttr { return nil }
