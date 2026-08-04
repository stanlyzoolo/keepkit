package model

import (
	"os/exec"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
)

// openURLMsg is returned after attempting to launch the system browser.
// err is nil on success and carries the launch error otherwise.
type openURLMsg struct {
	err error
}

// browserCommand resolves the binary and arguments used to open url in the
// default browser for the given GOOS. It is pure so it can be tested without
// launching a process.
func browserCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}

// openURLCmd builds a tea.Cmd that launches the system browser for url and
// reports the outcome via openURLMsg.
//
// Start plus a goroutine that Waits, rather than either alone: nobody reaping
// the child leaves a zombie for the rest of the session — one per o/c/link
// click — while Wait on this goroutine would hold it for the opener's whole
// lifetime, and a shell-wrapper xdg-open can outlive the click by minutes.
// Run() would do both at once and change what the message means: openURLMsg.err
// is "could not launch", and Run would start reporting the opener's own exit
// code through the visible setStatus path in Update.
func openURLCmd(url string) tea.Cmd {
	return safeCmd("openURLCmd", func() tea.Msg {
		name, args := browserCommand(runtime.GOOS, url)
		cmd := exec.Command(name, args...)
		if err := cmd.Start(); err != nil {
			return openURLMsg{err: err}
		}
		// The error is deliberately dropped: the launch already succeeded, and
		// what the opener does afterwards is not keepkit's business.
		go cmd.Wait() //nolint:errcheck
		return openURLMsg{}
	})
}
