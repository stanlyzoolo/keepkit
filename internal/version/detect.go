package version

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
	"github.com/stanlyzoolo/keepkit/internal/proc"
)

var versionRe = regexp.MustCompile(`v?(\d+\.\d+[\d.]*)`)

// InstalledVersion detects the locally installed version of t and reports
// whether the tool is present at all. The two results are independent: a tool
// can be installed yet refuse to name its version (a ratatui app that ignores
// --version and boots its own TUI), which the card must not render the same
// way as "not installed". Sources in order: the binary's own --version/-V,
// then Homebrew's directory layout, then `cargo install --list` — the last two
// answer without running the tool.
func InstalledVersion(t loader.Tool) (string, bool) {
	var candidates [][]string

	if t.VersionCmd != "" {
		parts := strings.Fields(t.VersionCmd)
		candidates = [][]string{parts}
	} else {
		candidates = [][]string{
			{t.Name, "--version"},
			{t.Name, "-V"},
		}
	}

	// Accumulate reasons only for anomalous failures — a binary that exists but
	// won't answer --version/-V (non-zero exit, timeout, or no parseable
	// version). A plain "not on PATH" (LookPath miss) is the normal
	// "not installed" state the card renders as "installed: not found", not a
	// malfunction, so it is left out: otherwise every tracked-but-uninstalled
	// tool would create a session log on every startup, defeating logx's
	// "a log file means something went wrong" signal. We also do not log inside
	// the loop — a --version miss followed by a -V success must stay silent.
	var reasons []string
	// binaryExists is the presence signal: set on a LookPath hit, i.e. the tool
	// is installed regardless of whether any candidate names its version.
	var binaryExists bool
	for _, args := range candidates {
		if _, err := exec.LookPath(args[0]); err != nil {
			continue // not installed — benign, never logged
		}
		binaryExists = true
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		// Detached from the controlling terminal: a tool that ignores
		// --version/-V and starts a TUI must not reach keepkit's screen.
		proc.DetachTTY(cmd)
		out, err := cmd.CombinedOutput()
		cancel()
		// The tool ignored the flag and booted its own TUI (the same signature
		// fetchHelpCmd discards): the capture is app frames plus the crash
		// trace DetachTTY provokes, not a version string. Parsing it would
		// mis-read a dependency's version out of the panic ("ratatui-0.30.2"),
		// so the capture is dropped whole and the loop stops — the remaining
		// candidate would only boot the app again. Accumulated reasons are
		// dropped with it: this is now a classified kind of tool, not an
		// anomaly, and logging it would re-create a session log on every
		// startup, defeating logx's "a log file means something went wrong".
		if isTUITakeover(out) {
			reasons = nil
			break
		}
		if err != nil {
			reasons = append(reasons, strings.Join(args, " ")+": "+err.Error())
			continue
		}
		if m := versionRe.FindString(string(out)); m != "" {
			return m, true
		}
		reasons = append(reasons, strings.Join(args, " ")+": no version string in output")
	}
	// Brew fallback: apps with no --version CLI (casks like GUI/terminal
	// apps) still record their version in Homebrew's directory layout. A hit
	// here also suppresses the reasons log — a cask failing --version every
	// startup is this path's normal state, not a malfunction, and logx's
	// signal is "a log file means something went wrong".
	if v := brewDirVersion(t.Name); v != "" {
		return v, true
	}
	// Cargo fallback, the same idea one ecosystem over: a Rust TUI records its
	// version in the crate registry even when the binary won't say it. Gated on
	// binaryExists — the list is keyed by the binary name, so for a tool that
	// isn't installed it can only miss, and this spends a subprocess.
	if binaryExists {
		if v := cargoListVersion(t.Name); v != "" {
			return v, true
		}
	}
	// Only an installed-but-unresponsive binary reaches here with reasons; a
	// tool simply absent from PATH leaves reasons empty and logs nothing.
	if len(reasons) > 0 {
		logx.Errorf("version.InstalledVersion: %s: %s", t.Name, strings.Join(reasons, "; "))
	}
	return "", binaryExists
}

// isTUITakeover reports whether captured output carries the alt-screen
// signature, i.e. the probed tool ignored its arguments and booted its own TUI.
// Deliberately a duplicate of model.isTUITakeover: version sits at the bottom
// of the import graph and importing model would invert it.
func isTUITakeover(out []byte) bool {
	return bytes.Contains(out, []byte("\x1b[?1049"))
}

// cargoListVersion reads binName's version out of `cargo install --list`,
// which knows every cargo-installed crate's version without running its binary.
// Returns "" when cargo is absent (the common case, short-circuited by
// LookPath before any subprocess) or the binary isn't cargo-installed. The
// timeout mirrors InstalledVersion's own: a large crate list must not stall
// fetchInstalledCmd's goroutine.
func cargoListVersion(binName string) string {
	if binName == "" {
		return ""
	}
	if _, err := exec.LookPath("cargo"); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cargo", "install", "--list")
	proc.DetachTTY(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return cargoVersionFromList(string(out), binName)
}

// cargoVersionFromList extracts binName's version from `cargo install --list`
// output, whose shape is a crate header at column 0 followed by its indented
// binaries:
//
//	inertia-tui v0.1.0:
//	    inertia
//	ripgrep v14.1.0:
//	    rg
//
// The version comes from the regex's capture group, i.e. without the leading
// "v": every other source of installed: is bare (brew dirs "1.96.1", --version
// "26.5.6") and the card must not show one shape for cargo tools and another
// for the rest. A binary under a header with no parseable version yields "".
func cargoVersionFromList(list, binName string) string {
	if binName == "" {
		return ""
	}
	var current string
	for line := range strings.SplitSeq(list, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			current = ""
			if m := versionRe.FindStringSubmatch(line); m != nil {
				current = m[1]
			}
			continue
		}
		if strings.TrimSpace(line) == binName {
			return current
		}
	}
	return ""
}

// IsNewer reports whether latest is a newer version than installed.
// Comparison is semver (pre-releases order below their release); either side
// failing to canonicalize — after CalVer/4-segment normalization — means "not
// newer", matching the old empty-string behavior.
func IsNewer(installed, latest string) bool {
	a := canonSemver(installed)
	b := canonSemver(latest)
	if a == "" || b == "" {
		return false
	}
	return semver.Compare(b, a) > 0
}

// DisplayVersion is the card's spelling of a version string: a bare version
// number gains the "v" its GitHub tag almost certainly carries, so the
// installed: and latest: lines read as a pair instead of sitting one letter
// apart — a tool's --version prints "1.10.2" where its release is tagged
// "v1.10.2", and the same binary looked like two different things.
//
// canonSemver decides *whether* the string is a version number at all, so
// anything it rejects ("nightly", "cli-2.0", a hash) is passed through
// untouched. Its output is deliberately not what gets displayed: it drops
// zero-padding (2024.01.15 → v2024.1.15), a 4th segment and build metadata,
// and the card must show the version the tool actually reported — the "v" is
// the only edit this function is allowed to make.
func DisplayVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || canonSemver(v) == "" {
		return v
	}
	if v[0] == 'v' || v[0] == 'V' {
		return v
	}
	return "v" + v
}

// canonSemver normalizes a detected version or GitHub tag into a form
// semver.Compare accepts, or "" when it cannot. Beyond the "v" prefix it
// handles two shapes strict semver rejects but real tools emit: zero-padded
// numeric segments (CalVer, "2024.01.15") and a 4th segment ("1.2.3.4",
// truncated — the first three decide newer-ness). Build metadata is dropped;
// a pre-release suffix is kept so rc/beta order below the release.
func canonSemver(v string) string {
	v = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "v"), "V")
	v, _, _ = strings.Cut(v, "+")
	if v == "" {
		return ""
	}
	base, pre, hasPre := strings.Cut(v, "-")

	segs := strings.Split(base, ".")
	if len(segs) > 3 {
		segs = segs[:3]
	}
	for i, s := range segs {
		s = strings.TrimLeft(s, "0")
		if s == "" {
			s = "0"
		}
		segs[i] = s
	}

	out := "v" + strings.Join(segs, ".")
	if hasPre {
		out += "-" + pre
	}
	if !semver.IsValid(out) {
		return ""
	}
	return out
}
