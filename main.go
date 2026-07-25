package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stanlyzoolo/keeptui/internal/configdir"
	"github.com/stanlyzoolo/keeptui/internal/loader"
	"github.com/stanlyzoolo/keeptui/internal/logx"
	"github.com/stanlyzoolo/keeptui/internal/model"
	verpkg "github.com/stanlyzoolo/keeptui/internal/version"
)

// version is overridden at release time via -ldflags "-X main.version=<tag>"
// (see .github/workflows/release.yml). It defaults to "dev" for local builds.
var version = "dev"

const usage = `keeptui — terminal TUI tracker for CLI tools

Usage:
  keeptui            launch the TUI
  keeptui --version  print version and exit
  keeptui --help     print this help and exit

There are no other flags or subcommands; all interaction happens inside
the TUI. Data lives in the "keeptui" directory under your user config
directory (meta.yaml, cache.json, token, logs/).
`

func main() {
	if code, done := handleCLI(os.Args[1:], os.Stdout, os.Stderr); done {
		os.Exit(code)
	}
	runTUI()
}

// handleCLI is the only non-TUI surface. done=false means "no args — launch
// the TUI". Anything unrecognized is an error, not a fall-through to the TUI:
// a keeptui probed by another tool (including keeptui itself) with an unknown
// flag must fail fast instead of booting a TUI on a detached terminal.
func handleCLI(args []string, out, errOut io.Writer) (code int, done bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case "--version", "-V", "-v", "version":
		_, _ = fmt.Fprintf(out, "keeptui %s\n", buildVersion())
		return 0, true
	case "--help", "-h", "help":
		_, _ = fmt.Fprint(out, usage)
		return 0, true
	default:
		_, _ = fmt.Fprintf(errOut, "keeptui: unknown argument %q\n\n%s", args[0], usage)
		return 2, true
	}
}

// buildVersion resolves what --version prints and what seeds the log header.
func buildVersion() string {
	mod := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		mod = bi.Main.Version
	}
	return resolveVersion(version, mod)
}

// resolveVersion picks the release ldflag when set, else the module version
// stamped by `go install module@version`, else "dev". `(devel)` is what a
// plain `go build` from a checkout stamps — as unhelpful as "dev", skipped.
func resolveVersion(ldflag, modVersion string) string {
	if ldflag != "dev" && ldflag != "" {
		return ldflag
	}
	if modVersion != "" && modVersion != "(devel)" {
		return modVersion
	}
	return "dev"
}

// migrateConfigDir renames the pre-rename config directory (<UserConfigDir>/keys,
// from when the app was called "keys") to the current keeptui config dir. The
// old "keys" data was written under os.UserConfigDir(); the new dir resolves via
// configdir.Base() (~/.config/keeptui on macOS/Linux, %AppData%\keeptui on
// Windows), i.e. wherever the app now reads. One-shot and conservative: if the
// new directory already exists — even empty — nothing is touched, so a
// half-adopted new install is never overwritten by old data.
func migrateConfigDir() {
	base, err := os.UserConfigDir()
	if err != nil {
		return
	}
	newBase, err := configdir.Base()
	if err != nil {
		return
	}
	oldDir := filepath.Join(base, "keys")
	newDir := filepath.Join(newBase, "keeptui")
	if _, err := os.Stat(newDir); err == nil {
		return
	}
	if _, err := os.Stat(oldDir); err != nil {
		return
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		logx.Errorf("config migration %s -> %s: %v", oldDir, newDir, err)
	}
}

func runTUI() {
	// Before logx.Cleanup: the log directory itself lives inside the config
	// directory being migrated.
	migrateConfigDir()
	logx.Cleanup()
	// Partial header first, so even a LoadMeta failure gets a non-blank header.
	ver := buildVersion()
	logx.SetHeader(fmt.Sprintf("keeptui %s %s/%s", ver, runtime.GOOS, runtime.GOARCH))

	meta, err := loader.LoadMeta()
	if err != nil {
		logx.Errorf("loader.LoadMeta: %v", err)
		fmt.Fprintf(os.Stderr, "error loading tools: %v\n", err)
		os.Exit(1)
	}
	// Enrich the header with tool count and token source now that meta loaded.
	logx.SetHeader(fmt.Sprintf("keeptui %s %s/%s tools=%d token=%s",
		ver, runtime.GOOS, runtime.GOARCH, len(meta), verpkg.TokenSource()))

	p := tea.NewProgram(
		newRootModel(meta, ver),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	final, err := p.Run()
	if err != nil {
		if errors.Is(err, tea.ErrProgramPanic) {
			logx.Errorf("tea.Run ended in panic: %v", err)
		} else {
			logx.Errorf("tea.Run: %v", err)
		}
		fmt.Fprintf(os.Stderr, "error running: %v\n", err)
		os.Exit(1)
	}
	restartIfRequested(final, restartSelf)
}

// newRootModel builds the model tea.NewProgram runs. It is a named function
// rather than an expression inline in runTUI for the reason resolveSelfPath and
// shellCommand are: runTUI itself needs a terminal and cannot be tested, so
// everything it decides without one lives outside it.
//
// WithAppVersion hands the model the version of this very binary: it is what the
// self-check compares against keeptui's latest release, and a dev build
// (ver == "dev") switches the whole feature off — no request, no banner. Dropping
// it would leave the self-update feature silently dead in every shipped binary,
// which is exactly what TestNewRootModelInjectsAppVersion is there to catch.
func newRootModel(meta []loader.ToolMeta, ver string) model.Model {
	return model.New(meta).WithAppVersion(ver)
}

// restarter is the one thing runTUI reads off the model p.Run() returns. It is
// an interface rather than a model.Model type assertion so the decision below
// can be exercised with a stand-in: the flag sits in the model's unexported
// state behind a key press that needs unexported messages to become reachable,
// so from here no real model can ever answer true. The compile-time assertion
// below is what pins the real model to it — a renamed method breaks the build
// instead of the feature.
type restarter interface{ RestartRequested() bool }

var _ restarter = model.Model{}

// restartIfRequested runs the [U] restart the user accepted after a self-update.
// The key quits with a flag instead of exec'ing from inside Update: only here,
// after p.Run() returned, is the terminal restored and the alt screen gone, so
// the new process starts on a clean one. restart is injected — the same seam
// idiom as restartSelfWith — because the decision is testable and syscall.Exec
// is not.
func restartIfRequested(final tea.Model, restart func()) {
	if r, ok := final.(restarter); ok && r.RestartRequested() {
		restart()
	}
}
