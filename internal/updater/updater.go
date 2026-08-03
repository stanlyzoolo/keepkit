// Package updater detects the package manager that owns an installed tool
// binary and produces an update Plan. It sits at the bottom of the import
// graph like internal/version: it has no TUI knowledge and depends only on
// internal/loader for the Tool type.
package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
// shell) except for the "custom" manager, where it runs through the platform
// shell — ["sh", "-c", <cmd>], or ["cmd", "/c", <cmd>] on Windows (customPlan).
type Plan struct {
	// Manager names the detected package manager, in chain order:
	// "brew" | "go" | "cargo" | "pipx" | "uv" | "pnpm" | "bun" | "npm" | "custom".
	Manager string
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

// managerDirs carries the per-user root directories of every manager whose
// detection is path-convention based (cargo, pipx, uv, pnpm, bun). Detect fills
// it from the environment; the pure core only reads it — which is what lets
// detectFromPath make good on "no I/O", since it no longer calls homeDir()
// itself.
//
// An empty field switches that manager's check off, which is what makes the
// zero value backwards compatible. That rule is enforced inside underDir and
// segmentUnder, which answer "no match" for an empty dir — deliberately not by
// a `!= ""` guard at each step, which is what it was until six copies of one
// convention proved the point this codebase already made with
// version.applyReleaseOutcome.
type managerDirs struct {
	cargoBin   string // cargo bin dir: <cargoBin>/<binary>
	pipxVenvs  string // pipx venv root: <pipxVenvs>/<pkg>/bin/<binary>
	uvTools    string // uv tool root: <uvTools>/<pkg>/bin/<binary>
	pnpmHome   string // pnpm global home: the bin shims plus the global store
	bunInstall string // bun install root: <bunInstall>/bin/<binary>
}

// resolveManagerDirs is the thin OS-facing wrapper over managerDirsFrom
// (the launcher.planFor idiom: pure core over an injected env lookup), plus the
// one thing the pure core cannot do — expanding symlinks in the resolved roots.
//
// That expansion is load-bearing, not hygiene. Detect compares these roots
// against an EvalSymlinks-*resolved* binary path, so a root carrying any
// symlink component (a relocated `~/.bun` → `/mnt/big/bun`, a home on a
// secondary volume, `/home` under autofs) simply fails to match, and the miss
// is silent. For uv and pnpm's shim/store layouts that only costs the update
// offer, but a bun global — and a legacy pnpm one — still carries a plain
// `node_modules/<pkg>` segment, so the npm step claims it and offers
// `npm install -g <pkg>`: the exact duplicate-install misdetection this chain
// exists to prevent (`TestDetectSymlinkedManagerRoot`).
func resolveManagerDirs() managerDirs {
	d := managerDirsFrom(os.Getenv, homeDir(), runtime.GOOS)
	// Every root, not just home: the symlink can sit anywhere on the path, and
	// an env override brings its own. A new field must be added here too — the
	// all-five assertion in TestResolveManagerDirsExpandsSymlinks is the guard.
	for _, dir := range []*string{&d.cargoBin, &d.pipxVenvs, &d.uvTools, &d.pnpmHome, &d.bunInstall} {
		*dir = resolveDir(*dir)
	}
	return d
}

// resolveDir expands every symlink component of dir, falling back to dir itself
// when that fails — a root that does not exist yet is the normal state for a
// manager the user has not installed, and it must stay a harmless non-match
// rather than becoming an empty (disabled) field.
func resolveDir(dir string) string {
	if dir == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// managerDirsFrom is the pure resolution core. Each manager's own env var wins;
// otherwise the platform default is used, and an empty home disables every
// home-based default (the field stays empty, i.e. the check stays off).
//
// cargo and pipx deliberately read no env var of their own here — $CARGO_HOME
// and $PIPX_HOME are not consulted, exactly as before this struct existed.
// Honouring them is a behaviour change for anyone who sets them and belongs in
// its own commit, not smuggled in with a path-resolution fix.
func managerDirsFrom(getenv func(string) string, home, goos string) managerDirs {
	var d managerDirs

	// cargo and pipx: home-derived on every platform, no override.
	if home != "" {
		d.cargoBin = filepath.Join(home, ".cargo", "bin")
		d.pipxVenvs = filepath.Join(home, ".local", "pipx", "venvs")
	}

	// uv: $UV_TOOL_DIR wins. uv follows the XDG layout on macOS too (it does not
	// use ~/Library/Application Support), so darwin and linux share one default.
	// Windows has no default at all — uv's layout there is unverified and a
	// wrong guess would be worse than the check being off (honest degradation).
	d.uvTools = getenv("UV_TOOL_DIR")
	if d.uvTools == "" && goos != "windows" {
		if xdg := getenv("XDG_DATA_HOME"); xdg != "" {
			d.uvTools = filepath.Join(xdg, "uv", "tools")
		} else if home != "" {
			d.uvTools = filepath.Join(home, ".local", "share", "uv", "tools")
		}
	}

	// pnpm: $PNPM_HOME wins; the default differs per platform.
	d.pnpmHome = getenv("PNPM_HOME")
	if d.pnpmHome == "" {
		switch goos {
		case "darwin":
			if home != "" {
				d.pnpmHome = filepath.Join(home, "Library", "pnpm")
			}
		case "windows":
			if local := getenv("LOCALAPPDATA"); local != "" {
				d.pnpmHome = filepath.Join(local, "pnpm")
			}
		default:
			if xdg := getenv("XDG_DATA_HOME"); xdg != "" {
				d.pnpmHome = filepath.Join(xdg, "pnpm")
			} else if home != "" {
				d.pnpmHome = filepath.Join(home, ".local", "share", "pnpm")
			}
		}
	}

	// bun: $BUN_INSTALL wins, else ~/.bun on every platform.
	d.bunInstall = getenv("BUN_INSTALL")
	if d.bunInstall == "" && home != "" {
		d.bunInstall = filepath.Join(home, ".bun")
	}

	return d
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
//     the platform shell (the user may write pipes or &&) — sh -c everywhere
//     except Windows, where cmd /c spares the Git Bash requirement — and
//     detection is skipped entirely.
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
		return customPlan(runtime.GOOS, t.UpdateCmd), nil
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
	dirs := resolveManagerDirs()

	// The pnpm signal, gathered exactly like buildinfo and only where it can
	// possibly exist: a bin shim under PNPM_HOME. Reading every binary on PATH
	// to look for a comment would be a needless open per detection.
	shimTarget := ""
	if underDir(realPath, dirs.pnpmHome) {
		shimTarget = readPnpmShim(realPath)
	}

	plan, err := detectFromPath(realPath, buildinfo, shimTarget, dirs)
	if err != nil {
		// The chain can exhaust with the binary found: a Homebrew cask app keeps
		// its launcher on PATH while the executable lives inside the .app bundle
		// (agterm), which matches neither the Cellar regex nor any other manager.
		// The same self-validating brew-by-name check as the LookPath miss above:
		// it fires only when a keg/cask directory of that name actually exists.
		if errors.Is(err, ErrUnknownManager) {
			if plan, ok := brewNamePlan(t.Name); ok {
				return plan, nil
			}
		}
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

// pnpmShimMarker is the trailing comment pnpm's cmd-shim writes into every
// global bin script, naming the file the shim actually executes.
const pnpmShimMarker = "# cmd-shim-target="

// pnpmShimMaxBytes caps the shim read. Real shims are ~1.5 KiB; the cap is what
// keeps the read bounded when the path turns out to be an ordinary binary that
// merely happens to sit under PNPM_HOME.
//
// Anything larger is rejected outright rather than parsed truncated, and that
// is a correctness guard, not tidiness: a cut landing inside the marker line
// hands the parser a *shortened* path, and shortening
// ".../node_modules/typescript/..." to ".../node_modules/types" yields "types"
// — a real package on npm. The chain would then offer `pnpm add -g types`, a
// confidently wrong command where the whole design promises honest degradation.
const pnpmShimMaxBytes = 8 << 10

// pnpmShimTarget is the pure parser for a pnpm global bin shim: the path the
// shim executes, or "" when the contents carry no marker.
//
// pnpm's globals are not symlinks — $PNPM_HOME/bin/<name> is a /bin/sh script
// EvalSymlinks resolves to itself — so this comment is the only machine
// readable link from the binary on PATH to the package that owns it. The last
// marker wins: cmd-shim writes it as the final line, so anything earlier came
// from the package's own text.
func pnpmShimTarget(contents string) string {
	target := ""
	for line := range strings.SplitSeq(contents, "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), pnpmShimMarker); ok {
			target = strings.TrimSpace(after)
		}
	}
	return target
}

// readPnpmShim is the OS-facing half of the pnpm signal: a bounded best-effort
// read of a pnpm global bin shim. Every failure yields "" rather than an error
// — an unreadable file just leaves the pnpm check without its signal, exactly
// like a missing `go` leaves goBuildinfo empty.
func readPnpmShim(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	// Read one byte past the cap (the version.getReadme idiom) purely to tell
	// "fits" from "does not fit": a file that overruns is not a cmd-shim, and
	// the parser must never see a truncated tail. Rejecting is the whole point
	// — see pnpmShimMaxBytes.
	buf, err := io.ReadAll(io.LimitReader(f, pnpmShimMaxBytes+1))
	if err != nil || len(buf) > pnpmShimMaxBytes {
		return ""
	}
	return pnpmShimTarget(string(buf))
}

// cellarRe extracts the formula name from a Homebrew Cellar path segment:
// .../Cellar/<formula>/<version>/bin/<binary>.
var cellarRe = regexp.MustCompile(`/Cellar/([^/]+)/`)

// goPathRe matches the `path <module>` line emitted by `go version -m`.
var goPathRe = regexp.MustCompile(`(?m)^\s*path\s+(\S+)\s*$`)

// detectFromPath is the pure detection core: given a binary's real (symlink
// resolved) path, the output of `go version -m <path>` (may be empty), the pnpm
// cmd-shim target Detect read off that binary (empty when there is none) and
// the resolved manager directories, it returns the update Plan. It performs no
// I/O, reads no environment and spawns no subprocesses — every root arrives in
// dirs — so table tests need no real package managers installed.
//
// Order matters twice over: brew is checked before go because a brew-installed
// Go binary carries buildinfo and would otherwise be misrouted to `go install`,
// and pnpm/bun are checked before npm because their layouts contain
// node_modules segments the npm step would otherwise claim.
func detectFromPath(realPath, buildinfo, shimTarget string, dirs managerDirs) (Plan, error) {
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

	// 3. Cargo (~/.cargo/bin). Crate name defaults to the binary name; the OS
	// wrapper refines it via `cargo install --list`.
	if underDir(realPath, dirs.cargoBin) {
		crate := binaryName(realPath)
		return autoPlan("cargo", []string{"cargo", "install", crate}), nil
	}

	// 4. pipx (~/.local/pipx/venvs/<pkg>/...).
	if pkg := segmentUnder(realPath, dirs.pipxVenvs); pkg != "" {
		return autoPlan("pipx", []string{"pipx", "upgrade", pkg}), nil
	}

	// 5. uv (<uvTools>/<pkg>/bin/<binary>, reached through a symlink in uv's own
	// bin dir) — the same technique as pipx, one ecosystem tool over.
	if pkg := segmentUnder(realPath, dirs.uvTools); pkg != "" {
		return autoPlan("uv", []string{"uv", "tool", "upgrade", pkg}), nil
	}

	// 6. pnpm globals, before npm because the store path carries node_modules
	// segments the npm step would otherwise claim. The package name comes from
	// the shim target Detect read for us (pnpm >= 9) or, for the legacy symlink
	// layouts, from the resolved path itself. Neither yielding a name means this
	// is not a package binary at all — pnpm's own executable — so it falls
	// through without an error (`pnpm self-update` is out of scope).
	if underDir(realPath, dirs.pnpmHome) {
		if pkg := npmPackage(shimTarget); pkg != "" {
			return autoPlan("pnpm", []string{"pnpm", "add", "-g", pkg}), nil
		}
		if pkg := npmPackage(realPath); pkg != "" {
			return autoPlan("pnpm", []string{"pnpm", "add", "-g", pkg}), nil
		}
	}

	// 7. bun globals ($BUN_INSTALL/bin/<binary> symlinked into
	// install/global/node_modules/<pkg>/...). Before npm for the same reason as
	// pnpm, and not merely for tidiness: the npm step used to claim these and
	// offer `npm install -g <pkg>`, installing a duplicate under npm's prefix.
	// bun's own binary has no node_modules segment and falls through — `bun
	// upgrade` is out of scope.
	if underDir(realPath, dirs.bunInstall) {
		if pkg := npmPackage(realPath); pkg != "" {
			return autoPlan("bun", []string{"bun", "add", "-g", pkg}), nil
		}
	}

	// 8. npm global (.../node_modules/<pkg>/...). A path through pnpm's virtual
	// store that got this far is a pnpm layout the pnpm step failed to
	// attribute — claiming it for npm would offer `npm install -g <pkg>`, which
	// silently installs a second working copy under npm's prefix while the pnpm
	// one keeps shadowing it on PATH. Falling through to ErrUnknownManager plus
	// the update_cmd hint is the honest degradation.
	if !strings.Contains(filepath.ToSlash(realPath), pnpmStoreSegment) {
		if pkg := npmPackage(realPath); pkg != "" {
			return autoPlan("npm", []string{"npm", "install", "-g", pkg}), nil
		}
	}

	return Plan{}, ErrUnknownManager
}

// autoPlan builds a Plan whose Display is the joined Argv.
func autoPlan(manager string, argv []string) Plan {
	return Plan{Manager: manager, Argv: argv, Display: strings.Join(argv, " ")}
}

// customPlan builds the Plan for an explicit update_cmd override: the command
// runs through the platform shell (the user may write pipes or &&) — sh -c
// everywhere except Windows, where cmd /c spares the Git Bash requirement.
// Goos-parameterized so both branches are table-testable — the updater-side
// sibling of model's shellCommand, which may not be imported from here (model
// sits above updater in the import graph); the two must not drift. The cmd /c
// branch shares shellCommand's accepted quoting caveat: Go's generic argv
// quoting is not cmd.exe-aware, so a command embedding double quotes can
// misparse there.
func customPlan(goos, cmd string) Plan {
	shell := []string{"sh", "-c"}
	if goos == "windows" {
		shell = []string{"cmd", "/c"}
	}
	return Plan{
		Manager: "custom",
		Argv:    append(shell, cmd),
		Display: cmd,
	}
}

// binaryName returns the file name of a path without directory.
func binaryName(p string) string {
	return filepath.Base(p)
}

// underDir reports whether path p lives under directory dir.
//
// An empty dir is **no match**, and that guard belongs here rather than at the
// call sites: filepath.Rel("", "bin/exa") succeeds and yields "bin/exa", so a
// *relative* p would otherwise read as living under every root. Every caller
// passes a managerDirs field, where empty means "this check is off", and that
// used to be a `!= ""` convention hand-copied into six call sites — the shape
// this codebase already learned to replace with one definition.
func underDir(p, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// segmentUnder returns the first path segment of p immediately below dir, or ""
// if p is not under dir. E.g. segmentUnder(".../venvs/black/bin/black",
// ".../venvs") == "black". An empty dir yields "" for underDir's reason.
func segmentUnder(p, dir string) string {
	if dir == "" {
		return ""
	}
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
// segment carrying a package name.
//
// A segment starting with "." right after node_modules is a service directory,
// never a package: ".pnpm" is pnpm's virtual store and ".bin" holds the shims.
// Such a segment is skipped and the scan continues — pnpm's store nests the
// real package one level deeper
// (.../node_modules/.pnpm/<pkg>@<ver>/node_modules/<pkg>/...).
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
		if strings.HasPrefix(pkg, ".") {
			continue // service dir (.pnpm, .bin) — keep scanning
		}
		if strings.HasPrefix(pkg, "@") {
			// A scope alone names no package: "@angular" is a directory holding
			// them. Returning it produced `pnpm add -g @scope`, a wrong command
			// where the chain owes an honest miss — and it is reachable, because
			// one caller passes a shim target, i.e. unvalidated file content.
			if i+2 >= len(parts) || parts[i+2] == "" {
				return ""
			}
			return pkg + "/" + parts[i+2]
		}
		return pkg
	}
	return ""
}

// pnpmStoreSegment is pnpm's virtual store directory inside node_modules. A
// path through it that no earlier chain step claimed is a pnpm layout we failed
// to attribute — see the npm step's gate in detectFromPath.
const pnpmStoreSegment = "/node_modules/.pnpm/"
