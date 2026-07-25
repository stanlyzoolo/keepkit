// Package updater detects the package manager that owns an installed tool
// binary and produces an update Plan. It sits at the bottom of the import
// graph like internal/version: it has no TUI knowledge and depends only on
// internal/loader for the Tool type.
package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/proc"
)

// ErrUnknownManager is returned when no package manager can be attributed to a
// tool's installed binary. Callers treat it as "no automatic update available"
// rather than a hard failure.
var ErrUnknownManager = errors.New("no known package manager for tool")

// Plan describes how to update a tool. Argv is executed directly (not through a
// shell) except for the "custom" manager, where it is ["sh", "-c", <cmd>].
type Plan struct {
	Manager string   // "brew" | "go" | "cargo" | "npm" | "pipx" | "custom"
	Argv    []string // e.g. ["brew", "upgrade", "ripgrep"]
	Display string   // human-facing command shown in the confirm dialog
}

// testHomeDir overrides the home directory in tests (mirrors
// loader.testConfigDir / version.testCacheDir).
var testHomeDir string

func homeDir() string {
	if testHomeDir != "" {
		return testHomeDir
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// testBrewPrefix overrides the Homebrew prefix in tests.
var testBrewPrefix string

// brewPrefix resolves the Homebrew installation prefix without spawning brew
// (a ruby process that costs ~1.5s per invocation): the HOMEBREW_PREFIX env
// var brew's shellenv exports, else the first standard location that exists.
// Empty when brew is not installed (including Windows, where none exist).
// A deliberate duplicate of version.brewPrefix — both packages are bottom
// leaves of the import graph and neither may depend on the other; twelve
// duplicated lines cost less than a shared package they would both import.
func brewPrefix() string {
	if testBrewPrefix != "" {
		return testBrewPrefix
	}
	if p := os.Getenv("HOMEBREW_PREFIX"); p != "" {
		return p
	}
	for _, p := range []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

// brewNamePlan reports the brew upgrade plan for a tracked name that is itself
// a formula/cask, for binaries LookPath cannot find. Thin OS-facing wrapper
// over brewNamePlanAt.
func brewNamePlan(name string) (Plan, bool) {
	return brewNamePlanAt(name, brewPrefix())
}

// brewNamePlanAt is the pure core: a keg or cask directory named after the tool
// means brew owns it, and `brew upgrade <name>` is the update command. Mirrors
// version.brewDirVersion's traversal guard — a brew formula/cask name is a bare
// identifier, so a name carrying a path separator can't be one and must not
// turn the Join into a traversal.
func brewNamePlanAt(name, prefix string) (Plan, bool) {
	if prefix == "" || name == "" || strings.ContainsAny(name, `/\`) {
		return Plan{}, false
	}
	for _, room := range []string{"Cellar", "Caskroom"} {
		if fi, err := os.Stat(filepath.Join(prefix, room, name)); err == nil && fi.IsDir() {
			return autoPlan("brew", []string{"brew", "upgrade", name}), true
		}
	}
	return Plan{}, false
}

// Detect resolves the update Plan for a tool. It is the OS-facing wrapper over
// the pure detectFromPath core: it spawns subprocesses (go version -m, cargo
// install --list) and must therefore never run on a latency-sensitive path such
// as Bubble Tea's Update — callers run it inside a tea.Cmd.
//
// Resolution order:
//  1. An explicit UpdateCmd always wins: it becomes a "custom" plan run through
//     sh -c (the user may write pipes or &&) and detection is skipped entirely.
//  2. Otherwise the binary is located via LookPath + EvalSymlinks, Go buildinfo
//     is collected via `go version -m`, and the pair is fed to detectFromPath.
//     A cargo hit is refined with the real crate name from `cargo install
//     --list`. Helper failures degrade softly — a missing `go`/`cargo` just
//     leaves the corresponding signal empty, never aborting detection.
//
// A binary that is not on PATH yields ErrUnknownManager wrapped with a
// "not installed" hint.
func Detect(t loader.Tool) (Plan, error) {
	if strings.TrimSpace(t.UpdateCmd) != "" {
		return Plan{
			Manager: "custom",
			Argv:    []string{"sh", "-c", t.UpdateCmd},
			Display: t.UpdateCmd,
		}, nil
	}

	found, err := exec.LookPath(t.Name)
	if err != nil {
		// No binary by that name, but the tracker name can still be a brew
		// formula whose binaries are named differently — "rust" ships rustc and
		// cargo, so LookPath("rust") misses while `brew upgrade rust` is exactly
		// the right command. The branch is self-validating: it fires only when a
		// keg/cask directory of that name actually exists.
		if plan, ok := brewNamePlan(t.Name); ok {
			return plan, nil
		}
		return Plan{}, fmt.Errorf("%s not installed: %w", t.Name, ErrUnknownManager)
	}
	realPath, err := filepath.EvalSymlinks(found)
	if err != nil {
		realPath = found // fall back to the unresolved path rather than aborting
	}

	buildinfo := goBuildinfo(realPath)

	plan, err := detectFromPath(realPath, buildinfo)
	if err != nil {
		return Plan{}, err
	}

	// Refine the cargo crate name: detectFromPath defaults it to the binary
	// name, but the crate can differ (crate "exa" ships binary "exa"; crate
	// "ripgrep" ships "rg"). `cargo install --list` is the source of truth.
	if plan.Manager == "cargo" {
		if crate := cargoCrate(binaryName(realPath)); crate != "" {
			plan = autoPlan("cargo", []string{"cargo", "install", crate})
		}
	}

	return plan, nil
}

// probeTimeout bounds each detection helper subprocess.
const probeTimeout = 3 * time.Second

// goBuildinfo returns the output of `go version -m <path>`, or "" when go is
// absent or the binary carries no Go buildinfo. It never errors: an empty
// result simply means "no Go module signal" to detectFromPath.
func goBuildinfo(path string) string {
	out, err := runProbe("go", "version", "-m", path)
	if err != nil {
		return ""
	}
	return out
}

// cargoCrate returns the crate name that owns the given binary by parsing
// `cargo install --list`, or "" when cargo is absent or the binary is not
// listed (caller keeps the binary-name fallback).
func cargoCrate(binName string) string {
	out, err := runProbe("cargo", "install", "--list")
	if err != nil {
		return ""
	}
	return cargoCrateFromList(out, binName)
}

// cargoCrateFromList is the pure parser for `cargo install --list` output. Each
// crate is a header line "<crate> v<ver>:" followed by indented binary names;
// it returns the crate whose binary set contains binName, else "".
func cargoCrateFromList(list, binName string) string {
	var crate string
	for line := range strings.SplitSeq(list, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			// Header line: "<crate> vX.Y.Z:" (path/source may follow in parens).
			name := strings.TrimSpace(line)
			if i := strings.IndexByte(name, ' '); i >= 0 {
				name = name[:i]
			}
			crate = name
			continue
		}
		if strings.TrimSpace(line) == binName {
			return crate
		}
	}
	return ""
}

// runProbe executes a detection helper subprocess detached from the controlling
// terminal (proc.DetachTTY) with a short timeout, returning combined output.
func runProbe(name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	proc.DetachTTY(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// cellarRe extracts the formula name from a Homebrew Cellar path segment:
// .../Cellar/<formula>/<version>/bin/<binary>.
var cellarRe = regexp.MustCompile(`/Cellar/([^/]+)/`)

// goPathRe matches the `path <module>` line emitted by `go version -m`.
var goPathRe = regexp.MustCompile(`(?m)^\s*path\s+(\S+)\s*$`)

// detectFromPath is the pure detection core: given a binary's real (symlink
// resolved) path and the output of `go version -m <path>` (may be empty), it
// returns the update Plan. It performs no I/O and spawns no subprocesses, so
// table tests need no real package managers installed.
//
// Order matters: brew is checked before go because a brew-installed Go binary
// carries buildinfo and would otherwise be misrouted to `go install`.
func detectFromPath(realPath, buildinfo string) (Plan, error) {
	// 1. Homebrew Cellar.
	if m := cellarRe.FindStringSubmatch(realPath); m != nil {
		formula := m[1]
		return autoPlan("brew", []string{"brew", "upgrade", formula}), nil
	}

	// 2. Go buildinfo module path.
	if m := goPathRe.FindStringSubmatch(buildinfo); m != nil {
		module := m[1]
		return autoPlan("go", []string{"go", "install", module + "@latest"}), nil
	}

	home := homeDir()

	// 3. Cargo (~/.cargo/bin). Crate name defaults to the binary name; the OS
	// wrapper refines it via `cargo install --list`.
	if home != "" {
		cargoBin := filepath.Join(home, ".cargo", "bin")
		if underDir(realPath, cargoBin) {
			crate := binaryName(realPath)
			return autoPlan("cargo", []string{"cargo", "install", crate}), nil
		}
	}

	// 4. pipx (~/.local/pipx/venvs/<pkg>/...).
	if home != "" {
		pipxVenvs := filepath.Join(home, ".local", "pipx", "venvs")
		if pkg := segmentUnder(realPath, pipxVenvs); pkg != "" {
			return autoPlan("pipx", []string{"pipx", "upgrade", pkg}), nil
		}
	}

	// 5. npm global (.../node_modules/<pkg>/...).
	if pkg := npmPackage(realPath); pkg != "" {
		return autoPlan("npm", []string{"npm", "install", "-g", pkg}), nil
	}

	return Plan{}, ErrUnknownManager
}

// autoPlan builds a Plan whose Display is the joined Argv.
func autoPlan(manager string, argv []string) Plan {
	return Plan{Manager: manager, Argv: argv, Display: strings.Join(argv, " ")}
}

// binaryName returns the file name of a path without directory.
func binaryName(p string) string {
	return filepath.Base(p)
}

// underDir reports whether path p lives under directory dir.
func underDir(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// segmentUnder returns the first path segment of p immediately below dir, or ""
// if p is not under dir. E.g. segmentUnder(".../venvs/black/bin/black",
// ".../venvs") == "black".
func segmentUnder(p, dir string) string {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 || parts[0] == "." || parts[0] == "" {
		return ""
	}
	return parts[0]
}

// npmPackage returns the npm package name for a binary whose realpath goes
// through a node_modules directory (.../node_modules/<pkg>/...), handling
// scoped packages (@scope/name). Returns "" when there is no node_modules
// segment.
func npmPackage(p string) string {
	parts := strings.Split(filepath.ToSlash(p), "/")
	for i, seg := range parts {
		if seg != "node_modules" {
			continue
		}
		if i+1 >= len(parts) {
			return ""
		}
		pkg := parts[i+1]
		if strings.HasPrefix(pkg, "@") && i+2 < len(parts) {
			return pkg + "/" + parts[i+2]
		}
		return pkg
	}
	return ""
}
