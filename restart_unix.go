//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/stanlyzoolo/keepkit/internal/logx"
)

// restartSelf replaces this process with the freshly updated binary, so the
// terminal tab or tmux pane keepkit was launched in survives the restart: same
// pid, same argv, same environment. Called from runTUI strictly after p.Run()
// returned — Bubble Tea has restored the terminal by then, and exec'ing from
// inside Update would hand the new process an alt screen it never opened.
// On success it does not return.
func restartSelf() {
	restartSelfWith(selfPath, syscall.Exec, os.Stdout)
}

// restartSelfWith is restartSelf's testable core: the path resolver, the exec
// call and the hint's destination are injected (the same seam idiom as
// resolveSelfPath's exists/lookPath).
//
// A failure here is not a failed update: the new binary is on disk either way,
// so the honest outcome is a plain exit plus restartHint on stdout, with a zero
// status — it is an instruction, not a diagnostic. The failure is still an
// anomaly worth researching after the fact, hence the log line.
func restartSelfWith(resolve func() (string, error), execve func(string, []string, []string) error, out io.Writer) {
	path, err := resolve()
	if err == nil {
		// Returns only on failure: a successful exec replaces this image, so
		// nothing below runs.
		err = execve(path, os.Args, os.Environ())
	}
	if err == nil {
		return
	}
	logx.Errorf("restart self: %v", err)
	_, _ = fmt.Fprintln(out, restartHint)
}

// resolveSelfPath picks which binary to exec on restart. It mirrors how a shell
// resolves the command the user typed instead of trusting os.Executable, and the
// order is load-bearing on Linux: there os.Executable() reads /proc/self/exe and
// comes back symlink-*resolved*, so after a keg-style upgrade it can still name
// the live *old* binary — exec'ing that would loop the feature ("restart" → the
// same banner → "restart"). A bare argv0 therefore goes through lookPath first,
// which finds whatever the upgrade put on PATH. An argv0 carrying a path
// separator is already a path the user pointed at and is used as-is when it
// exists. executable is the fallback for both cases, and only when it exists —
// otherwise there is nothing safe to exec and the caller prints restartHint.
//
// A PATH hit is only trusted when it names the same program as executable:
// argv0 is set by the parent process, so a wrapper (exec -a) or a shell that
// rewrote argv[0] would otherwise make the restart exec an unrelated binary with
// keepkit's argv and environment.
func resolveSelfPath(executable string, exists func(string) bool, lookPath func(string) (string, error), argv0 string) (string, error) {
	if argv0 != "" {
		if strings.Contains(argv0, "/") {
			if exists(argv0) {
				return argv0, nil
			}
		} else if p, err := lookPath(argv0); err == nil && p != "" && sameProgram(p, executable) {
			return p, nil
		}
	}
	if executable != "" && exists(executable) {
		return executable, nil
	}
	return "", fmt.Errorf("no keepkit binary to restart (argv0 %q, executable %q)", argv0, executable)
}

// sameProgram reports whether two paths name the same program by base name. An
// unknown executable — os.Executable() failed — cannot contradict anything, so
// it accepts.
//
// filepath.Base is the whole comparison because this file is unix-only: the
// windows-style separator and .exe handling it used to carry dated from when the
// core lived in the untagged restart.go, and under //go:build !windows neither
// can fire — exec.LookPath("keepkit") on unix never answers "keepkit.exe", and a
// backslash in argv0 is an ordinary filename character, not a separator.
func sameProgram(a, b string) bool {
	if b == "" {
		return true
	}
	return filepath.Base(a) == filepath.Base(b)
}

// selfPath is the OS-facing wrapper over resolveSelfPath (the shellCommand /
// planFor idiom: pure core, thin wrapper).
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		// Nothing to fall back on, but argv0 may still resolve.
		exe = ""
	}
	argv0 := ""
	if len(os.Args) > 0 {
		argv0 = os.Args[0]
	}
	return resolveSelfPath(exe, fileExists, exec.LookPath, argv0)
}

// fileExists is resolveSelfPath's exists probe. A path that stats is good
// enough — anything finer (not executable, wrong architecture) is reported by
// the exec itself, and both outcomes land on the same restartHint.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
