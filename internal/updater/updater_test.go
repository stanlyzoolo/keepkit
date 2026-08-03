package updater

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stanlyzoolo/keepkit/internal/loader"
)

func TestDetectFromPath(t *testing.T) {
	// Just a prefix for building fixture paths — no testHomeDir override, and
	// that is the point: every root now arrives in dirs, so the core reads no
	// environment at all. An override here would suggest otherwise.
	home := "/home/tester"

	goBuildinfo := "\tpath\tgithub.com/junegunn/fzf\n\tmod\tgithub.com/junegunn/fzf\tv0.55.0\n"
	pnpmHome := filepath.Join(home, "Library", "pnpm")
	bunInstall := filepath.Join(home, ".bun")
	bunGlobal := filepath.Join(bunInstall, "install", "global", "node_modules")

	tests := []struct {
		name        string
		realPath    string
		buildinfo   string
		shimTarget  string
		dirs        managerDirs
		wantManager string
		wantArgv    []string
		wantErr     error
	}{
		{
			name:        "brew cellar formula from path",
			realPath:    "/opt/homebrew/Cellar/ripgrep/14.1.0/bin/rg",
			wantManager: "brew",
			wantArgv:    []string{"brew", "upgrade", "ripgrep"},
		},
		{
			name:        "go buildinfo module",
			realPath:    "/home/tester/go/bin/fzf",
			buildinfo:   goBuildinfo,
			wantManager: "go",
			wantArgv:    []string{"go", "install", "github.com/junegunn/fzf@latest"},
		},
		{
			name:        "cargo bin dir",
			realPath:    filepath.Join(home, ".cargo", "bin", "exa"),
			dirs:        managerDirs{cargoBin: filepath.Join(home, ".cargo", "bin")},
			wantManager: "cargo",
			wantArgv:    []string{"cargo", "install", "exa"},
		},
		{
			name:        "pipx venv package",
			realPath:    filepath.Join(home, ".local", "pipx", "venvs", "black", "bin", "black"),
			dirs:        managerDirs{pipxVenvs: filepath.Join(home, ".local", "pipx", "venvs")},
			wantManager: "pipx",
			wantArgv:    []string{"pipx", "upgrade", "black"},
		},
		{
			// The same `!= ""` guard the three newer steps carry: with an empty
			// cargoBin, underDir(p, "") would claim any relative path.
			name:     "empty cargoBin leaves a relative path undetected",
			realPath: "bin/exa",
			wantErr:  ErrUnknownManager,
		},
		{
			name:        "npm global node_modules",
			realPath:    "/usr/local/lib/node_modules/typescript/bin/tsc",
			wantManager: "npm",
			wantArgv:    []string{"npm", "install", "-g", "typescript"},
		},
		{
			name:        "npm scoped package",
			realPath:    "/usr/local/lib/node_modules/@angular/cli/bin/ng",
			wantManager: "npm",
			wantArgv:    []string{"npm", "install", "-g", "@angular/cli"},
		},
		{
			name:      "unmatched path",
			realPath:  "/usr/local/bin/mytool",
			buildinfo: "",
			wantErr:   ErrUnknownManager,
		},
		{
			name:        "uv tool",
			realPath:    filepath.Join(home, ".local", "share", "uv", "tools", "cowsay", "bin", "cowsay"),
			dirs:        managerDirs{uvTools: filepath.Join(home, ".local", "share", "uv", "tools")},
			wantManager: "uv",
			wantArgv:    []string{"uv", "tool", "upgrade", "cowsay"},
		},
		{
			// The explicit `dirs.uvTools != ""` guard: segmentUnder(p, "") matches
			// any relative path, so without it an empty dir would claim this one.
			name:     "empty uvTools leaves a relative path undetected",
			realPath: "somewhere/cowsay/bin/cowsay",
			wantErr:  ErrUnknownManager,
		},
		{
			// pnpm >= 9: the bin entry is a shell script, so the package name can
			// only come from the shim target Detect read off it.
			name:        "pnpm shim target",
			realPath:    filepath.Join(pnpmHome, "bin", "tsc"),
			shimTarget:  filepath.Join(pnpmHome, "global", "v11", "8f3a2c1", "node_modules", "typescript", "cli.js"),
			dirs:        managerDirs{pnpmHome: pnpmHome},
			wantManager: "pnpm",
			wantArgv:    []string{"pnpm", "add", "-g", "typescript"},
		},
		{
			name:        "pnpm shim target with a scoped package",
			realPath:    filepath.Join(pnpmHome, "bin", "ng"),
			shimTarget:  filepath.Join(pnpmHome, "global", "v11", "1d0e9b4", "node_modules", "@angular", "cli", "bin", "ng.js"),
			dirs:        managerDirs{pnpmHome: pnpmHome},
			wantManager: "pnpm",
			wantArgv:    []string{"pnpm", "add", "-g", "@angular/cli"},
		},
		{
			// The reachable half of the bare-scope hole: a shim target is file
			// content nothing validated, and a target ending at the scope used to
			// come back as `pnpm add -g @angular`.
			name:       "pnpm shim target naming a bare scope",
			realPath:   filepath.Join(pnpmHome, "bin", "ng"),
			shimTarget: filepath.Join(pnpmHome, "global", "v11", "1d0e9b4", "node_modules", "@angular"),
			dirs:       managerDirs{pnpmHome: pnpmHome},
			wantErr:    ErrUnknownManager,
		},
		{
			// Legacy layouts symlink into the store, so the resolved path itself
			// names the package and no shim target is involved.
			name:        "pnpm legacy symlink layout",
			realPath:    filepath.Join(pnpmHome, "global", "5", "node_modules", "typescript", "bin", "tsc"),
			dirs:        managerDirs{pnpmHome: pnpmHome},
			wantManager: "pnpm",
			wantArgv:    []string{"pnpm", "add", "-g", "typescript"},
		},
		{
			// The virtual store under PNPM_HOME: pnpm claims it, and the ".pnpm"
			// service segment must not become the package name.
			name:        "pnpm virtual store layout",
			realPath:    filepath.Join(pnpmHome, "global", "5", "node_modules", ".pnpm", "typescript@5.6.2", "node_modules", "typescript", "bin", "tsc"),
			dirs:        managerDirs{pnpmHome: pnpmHome},
			wantManager: "pnpm",
			wantArgv:    []string{"pnpm", "add", "-g", "typescript"},
		},
		{
			// pnpm's own binary: no shim target, no node_modules segment. Manager
			// self-updates are out of scope, so it falls through the whole chain.
			name:     "pnpm own binary falls through",
			realPath: filepath.Join(pnpmHome, "bin", "pnpm"),
			dirs:     managerDirs{pnpmHome: pnpmHome},
			wantErr:  ErrUnknownManager,
		},
		{
			// The explicit `dirs.pnpmHome != ""` guard: underDir(p, "") is true for
			// any relative path, so without it this shim target would claim pnpm.
			name:       "empty pnpmHome leaves a relative path undetected",
			realPath:   "bin/tsc",
			shimTarget: "/somewhere/node_modules/typescript/cli.js",
			wantErr:    ErrUnknownManager,
		},
		{
			name:        "bun global package",
			realPath:    filepath.Join(bunGlobal, "prettier", "bin", "prettier.cjs"),
			dirs:        managerDirs{bunInstall: bunInstall},
			wantManager: "bun",
			wantArgv:    []string{"bun", "add", "-g", "prettier"},
		},
		{
			name:        "bun scoped package",
			realPath:    filepath.Join(bunGlobal, "@angular", "cli", "bin", "ng"),
			dirs:        managerDirs{bunInstall: bunInstall},
			wantManager: "bun",
			wantArgv:    []string{"bun", "add", "-g", "@angular/cli"},
		},
		{
			// Ordering: the path carries node_modules, so with bun checked after
			// npm this resolved to `npm install -g prettier` — the wrong manager,
			// installing a duplicate under npm's prefix.
			name:        "order: bun path with node_modules yields bun not npm",
			realPath:    filepath.Join(bunGlobal, "prettier", "bin", "prettier.cjs"),
			dirs:        managerDirs{bunInstall: bunInstall, uvTools: filepath.Join(home, "uv")},
			wantManager: "bun",
			wantArgv:    []string{"bun", "add", "-g", "prettier"},
		},
		{
			// bun's own binary: under bunInstall but with no node_modules segment.
			// `bun upgrade` is out of scope, so it falls through the chain.
			name:     "bun own binary falls through",
			realPath: filepath.Join(bunInstall, "bin", "bun"),
			dirs:     managerDirs{bunInstall: bunInstall},
			wantErr:  ErrUnknownManager,
		},
		{
			// A pnpm store path no manager dir covers must NOT be claimed by npm:
			// `npm install -g typescript` would install a duplicate under npm's
			// prefix while the pnpm copy keeps shadowing it on PATH.
			name:     "pnpm store path outside every manager dir is not npm",
			realPath: "/elsewhere/node_modules/.pnpm/typescript@5.6.2/node_modules/typescript/bin/tsc",
			wantErr:  ErrUnknownManager,
		},
		{
			name:        "order: cellar with buildinfo yields brew not go",
			realPath:    "/opt/homebrew/Cellar/gopls/0.16.1/bin/gopls",
			buildinfo:   "\tpath\tgolang.org/x/tools/gopls\n",
			wantManager: "brew",
			wantArgv:    []string{"brew", "upgrade", "gopls"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := detectFromPath(tt.realPath, tt.buildinfo, tt.shimTarget, tt.dirs)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.Manager != tt.wantManager {
				t.Errorf("Manager = %q, want %q", plan.Manager, tt.wantManager)
			}
			if !equalStrings(plan.Argv, tt.wantArgv) {
				t.Errorf("Argv = %v, want %v", plan.Argv, tt.wantArgv)
			}
			wantDisplay := join(tt.wantArgv)
			if plan.Display != wantDisplay {
				t.Errorf("Display = %q, want %q", plan.Display, wantDisplay)
			}
		})
	}
}

// TestManagerDirsFrom pins the pure env resolution of the three
// path-convention manager roots. Expectations are built with filepath.Join, not
// literal separators: the core runs on the host OS (the configdir.baseFor
// table-test precedent), so a hardcoded "\" would fail everywhere but Windows.
func TestManagerDirsFrom(t *testing.T) {
	const home = "/home/tester"
	joinPath := filepath.Join

	tests := []struct {
		name string
		env  map[string]string
		home string
		goos string
		want managerDirs
	}{
		{
			name: "linux bare home",
			home: home,
			goos: "linux",
			want: managerDirs{
				uvTools:    joinPath(home, ".local", "share", "uv", "tools"),
				pnpmHome:   joinPath(home, ".local", "share", "pnpm"),
				bunInstall: joinPath(home, ".bun"),
			},
		},
		{
			name: "linux XDG_DATA_HOME",
			env:  map[string]string{"XDG_DATA_HOME": "/xdg/data"},
			home: home,
			goos: "linux",
			want: managerDirs{
				uvTools:    joinPath("/xdg/data", "uv", "tools"),
				pnpmHome:   joinPath("/xdg/data", "pnpm"),
				bunInstall: joinPath(home, ".bun"),
			},
		},
		{
			// uv uses the XDG layout on macOS too, so only pnpm differs here.
			name: "darwin bare home",
			home: home,
			goos: "darwin",
			want: managerDirs{
				uvTools:    joinPath(home, ".local", "share", "uv", "tools"),
				pnpmHome:   joinPath(home, "Library", "pnpm"),
				bunInstall: joinPath(home, ".bun"),
			},
		},
		{
			name: "darwin ignores XDG for pnpm",
			env:  map[string]string{"XDG_DATA_HOME": "/xdg/data"},
			home: home,
			goos: "darwin",
			want: managerDirs{
				uvTools:    joinPath("/xdg/data", "uv", "tools"),
				pnpmHome:   joinPath(home, "Library", "pnpm"),
				bunInstall: joinPath(home, ".bun"),
			},
		},
		{
			// Windows: uv has no default at all, pnpm rides LOCALAPPDATA.
			name: "windows LOCALAPPDATA",
			env:  map[string]string{"LOCALAPPDATA": `C:\Users\t\AppData\Local`},
			home: home,
			goos: "windows",
			want: managerDirs{
				uvTools:    "",
				pnpmHome:   joinPath(`C:\Users\t\AppData\Local`, "pnpm"),
				bunInstall: joinPath(home, ".bun"),
			},
		},
		{
			name: "windows without LOCALAPPDATA",
			home: home,
			goos: "windows",
			want: managerDirs{
				uvTools:    "",
				pnpmHome:   "",
				bunInstall: joinPath(home, ".bun"),
			},
		},
		{
			name: "windows uv is env-only",
			env:  map[string]string{"UV_TOOL_DIR": "/uv/tools", "XDG_DATA_HOME": "/xdg/data"},
			home: home,
			goos: "windows",
			want: managerDirs{
				uvTools:    "/uv/tools",
				pnpmHome:   "",
				bunInstall: joinPath(home, ".bun"),
			},
		},
		{
			name: "env overrides win over every default",
			env: map[string]string{
				"UV_TOOL_DIR":   "/custom/uv",
				"PNPM_HOME":     "/custom/pnpm",
				"BUN_INSTALL":   "/custom/bun",
				"XDG_DATA_HOME": "/xdg/data",
			},
			home: home,
			goos: "linux",
			want: managerDirs{uvTools: "/custom/uv", pnpmHome: "/custom/pnpm", bunInstall: "/custom/bun"},
		},
		{
			// No home, no env: every check stays off.
			name: "empty home disables defaults",
			goos: "linux",
			want: managerDirs{},
		},
		{
			// An env override still works without a home.
			name: "empty home keeps env overrides",
			env:  map[string]string{"PNPM_HOME": "/custom/pnpm"},
			goos: "darwin",
			want: managerDirs{pnpmHome: "/custom/pnpm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// cargo and pipx depend on home alone — no env var, no per-platform
			// branch — so they are asserted once here instead of being repeated
			// identically in every row, where they would bury the differences
			// the table exists to show.
			want := tt.want
			if tt.home != "" {
				want.cargoBin = joinPath(tt.home, ".cargo", "bin")
				want.pipxVenvs = joinPath(tt.home, ".local", "pipx", "venvs")
			}
			getenv := func(k string) string { return tt.env[k] }
			if got := managerDirsFrom(getenv, tt.home, tt.goos); got != want {
				t.Errorf("managerDirsFrom() = %+v, want %+v", got, want)
			}
		})
	}
}

// TestNpmPackage pins the package-name extraction, including the service
// segments (".pnpm", ".bin") that are never package names — before the skip,
// a pnpm store path resolved to the literal package ".pnpm".
func TestNpmPackage(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"plain global package", "/usr/local/lib/node_modules/typescript/bin/tsc", "typescript"},
		{"scoped global package", "/usr/local/lib/node_modules/@angular/cli/bin/ng", "@angular/cli"},
		{
			"pnpm virtual store skips .pnpm",
			"/home/t/.local/share/pnpm/global/5/node_modules/.pnpm/typescript@5.6.2/node_modules/typescript/bin/tsc",
			"typescript",
		},
		{
			"pnpm virtual store with a scoped package",
			"/home/t/.local/share/pnpm/global/5/node_modules/.pnpm/@angular+cli@18.0.0/node_modules/@angular/cli/bin/ng",
			"@angular/cli",
		},
		{"only a .bin shim dir", "/usr/local/lib/node_modules/.bin/tsc", ""},
		// A scope with nothing after it is a directory, not a package. It used to
		// come back as "@angular", i.e. `npm install -g @angular`.
		{"bare scope", "/usr/local/lib/node_modules/@angular", ""},
		{"bare scope with a trailing slash", "/usr/local/lib/node_modules/@angular/", ""},
		{
			"bare scope inside the pnpm store",
			"/home/t/.local/share/pnpm/global/5/node_modules/.pnpm/@scope+cli@1.0.0/node_modules/@scope",
			"",
		},
		{"no node_modules segment", "/usr/local/bin/tsc", ""},
		{"node_modules is the last segment", "/usr/local/lib/node_modules", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := npmPackage(tt.path); got != tt.want {
				t.Errorf("npmPackage(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestPnpmShimTarget pins the pure shim parser against the real pnpm cmd-shim
// shape: the marker is the last line, and the path it names is the only machine
// readable link from a pnpm global bin script to the package that owns it.
func TestPnpmShimTarget(t *testing.T) {
	const target = "/home/tester/Library/pnpm/global/v11/8f3a2c1/node_modules/typescript/bin/tsc"
	shim := "#!/bin/sh\n" +
		`basedir=$(dirname "$(echo "$0" | sed -e 's,\\,/,g')")` + "\n" +
		"\n" +
		"case `uname` in\n" +
		"    *CYGWIN*|*MINGW*|*MSYS*)\n" +
		"        if command -v cygpath > /dev/null 2>&1; then\n" +
		`            basedir=$(cygpath -w "$basedir")` + "\n" +
		"        fi\n" +
		"    ;;\n" +
		"esac\n" +
		"\n" +
		`if [ -x "$basedir/node" ]; then` + "\n" +
		`  exec "$basedir/node" "` + target + `" "$@"` + "\n" +
		"else\n" +
		`  exec node "` + target + `" "$@"` + "\n" +
		"fi\n" +
		"# cmd-shim-target=" + target + "\n"

	tests := []struct {
		name     string
		contents string
		want     string
	}{
		{"real shim", shim, target},
		{
			"scoped package target",
			"#!/bin/sh\n# cmd-shim-target=/home/t/Library/pnpm/global/v11/1d0/node_modules/@angular/cli/bin/ng.js\n",
			"/home/t/Library/pnpm/global/v11/1d0/node_modules/@angular/cli/bin/ng.js",
		},
		{"no marker", "#!/bin/sh\nexec node foo.js\n", ""},
		{"empty contents", "", ""},
		{
			// pnpm writes \r\n shims on some setups; the \r must not ride along
			// into the path and turn node_modules lookups into misses.
			"crlf line endings",
			"#!/bin/sh\r\n# cmd-shim-target=/home/t/pnpm/global/v11/1d0/node_modules/typescript/bin/tsc\r\n",
			"/home/t/pnpm/global/v11/1d0/node_modules/typescript/bin/tsc",
		},
		{
			// The marker is written last, so a copy of it inside the package's own
			// text (an npm bin shim the package itself ships) must not win.
			"last marker wins",
			"# cmd-shim-target=/old/node_modules/a/cli.js\n# cmd-shim-target=/new/node_modules/b/cli.js\n",
			"/new/node_modules/b/cli.js",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pnpmShimTarget(tt.contents); got != tt.want {
				t.Errorf("pnpmShimTarget() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestReadPnpmShim covers the OS-facing half of the pnpm signal, whose whole
// contract is "every failure is an empty signal, never an error" — plus the one
// case where an empty signal is a correctness guard rather than degradation:
// an oversized file, whose truncated tail would otherwise be parsed.
func TestReadPnpmShim(t *testing.T) {
	dir := t.TempDir()
	const target = "/home/t/Library/pnpm/global/v11/abc/node_modules/typescript/bin/tsc"
	markerLine := pnpmShimMarker + target + "\n"

	write := func(t *testing.T, name, contents string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Exactly at the cap: still read, still parsed.
	atCap := "#!/bin/sh\n" + strings.Repeat("#", pnpmShimMaxBytes-len(markerLine)-11) + "\n" + markerLine
	if len(atCap) != pnpmShimMaxBytes {
		t.Fatalf("fixture is %d bytes, want exactly the cap %d", len(atCap), pnpmShimMaxBytes)
	}

	// One byte over, with the cut landing inside the target's package name:
	// parsed truncated this yields "types", a real npm package, and the chain
	// would offer `pnpm add -g types`.
	straddling := "#!/bin/sh\n" + strings.Repeat("#", pnpmShimMaxBytes-len(markerLine)+3) + "\n" + markerLine

	tests := []struct {
		name string
		path string
		want string
	}{
		{"real shim", write(t, "tsc", "#!/bin/sh\nexec node x\n"+markerLine), target},
		{"no marker", write(t, "plain", "#!/bin/sh\nexec node x\n"), ""},
		{"empty file", write(t, "empty", ""), ""},
		{"exactly at the cap", write(t, "atcap", atCap), target},
		{"oversized shim is rejected whole", write(t, "big", straddling), ""},
		{"missing file", filepath.Join(dir, "nope"), ""},
		{"path is a directory", dir, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readPnpmShim(tt.path); got != tt.want {
				t.Errorf("readPnpmShim() = %q, want %q", got, tt.want)
			}
		})
	}

	// The guard is what makes the rejection meaningful: spell out what parsing
	// the truncated tail would have produced.
	if got := pnpmShimTarget(straddling[:pnpmShimMaxBytes]); npmPackage(got) != "types" {
		t.Fatalf("fixture no longer straddles the package name: target %q → pkg %q", got, npmPackage(got))
	}
}

// TestPathHelpersRejectEmptyDir pins the safe-by-construction half of
// managerDirs: an empty root means "this check is off", and both helpers answer
// that themselves. It used to be a `!= ""` guard hand-copied into six call
// sites, where one omission silently claimed any *relative* path — a real
// hazard, since filepath.Rel("", "bin/exa") succeeds and yields "bin/exa".
func TestPathHelpersRejectEmptyDir(t *testing.T) {
	// Relative paths are the dangerous ones; absolutes already failed Rel.
	for _, p := range []string{"bin/exa", "cowsay/bin/cowsay", "exa", "", "/abs/bin/exa"} {
		if underDir(p, "") {
			t.Errorf("underDir(%q, \"\") = true, want false", p)
		}
		if got := segmentUnder(p, ""); got != "" {
			t.Errorf("segmentUnder(%q, \"\") = %q, want \"\"", p, got)
		}
	}
	// A relative root against an absolute path (a user writing
	// `export BUN_INSTALL=./bun`): filepath.Rel cannot relate the two and
	// errors, which must read as "no match" rather than propagate.
	if underDir("/abs/bin/exa", filepath.Join("rel", "root")) {
		t.Error("underDir claimed an absolute path for a relative root")
	}
	if got := segmentUnder("/abs/bin/exa", filepath.Join("rel", "root")); got != "" {
		t.Errorf("segmentUnder = %q, want \"\" for a relative root", got)
	}

	// A non-empty dir keeps working, so the guard is not a blanket off switch.
	if !underDir(filepath.Join("root", "bin", "exa"), "root") {
		t.Error("underDir lost a genuine match")
	}
	if got := segmentUnder(filepath.Join("root", "black", "bin"), "root"); got != "black" {
		t.Errorf("segmentUnder = %q, want %q", got, "black")
	}
}

// TestResolveManagerDirsExpandsSymlinks asserts that **every** field comes back
// symlink-expanded. It is the guard on the loop in resolveManagerDirs: a field
// added to managerDirs but forgotten there would resolve to a root Detect can
// never match against a real (resolved) binary path, and the miss is silent.
//
// Both mechanisms are covered at once — cargo/pipx/uv inherit their symlink
// from a relocated home, pnpm/bun bring their own through an env override.
func TestResolveManagerDirsExpandsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture is unix-only, and uv has no Windows default")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// A home reached through a symlink: /home under autofs, a home on a
	// secondary volume, /var → /private/var on macOS.
	realHome := filepath.Join(base, "realhome")
	homeLink := filepath.Join(base, "home")
	mustMkdirAll(t, realHome)
	if err := os.Symlink(realHome, homeLink); err != nil {
		t.Fatal(err)
	}
	setTestHomeDir(t, homeLink)
	t.Setenv("UV_TOOL_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")

	// A relocated tool root: `ln -s /mnt/big/bun ~/.bun`, the disk-space case.
	realStore := filepath.Join(base, "store")
	storeLink := filepath.Join(base, "storelink")
	mustMkdirAll(t, realStore)
	if err := os.Symlink(realStore, storeLink); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PNPM_HOME", filepath.Join(storeLink, "pnpm"))
	t.Setenv("BUN_INSTALL", filepath.Join(storeLink, "bun"))

	// EvalSymlinks resolves only paths that exist, so every root must.
	uvDefault := filepath.Join(realHome, ".local", "share", "uv", "tools")
	for _, d := range []string{
		filepath.Join(realHome, ".cargo", "bin"),
		filepath.Join(realHome, ".local", "pipx", "venvs"),
		uvDefault,
		filepath.Join(realStore, "pnpm"),
		filepath.Join(realStore, "bun"),
	} {
		mustMkdirAll(t, d)
	}

	got := resolveManagerDirs()
	want := managerDirs{
		cargoBin:   filepath.Join(realHome, ".cargo", "bin"),
		pipxVenvs:  filepath.Join(realHome, ".local", "pipx", "venvs"),
		uvTools:    uvDefault,
		pnpmHome:   filepath.Join(realStore, "pnpm"),
		bunInstall: filepath.Join(realStore, "bun"),
	}
	if got != want {
		t.Errorf("resolveManagerDirs() = %+v,\nwant %+v", got, want)
	}
}

// TestResolveDirKeepsUnresolvable pins the fallback: a root that does not exist
// (the normal state for a manager the user has not installed) must stay a
// harmless non-match, never become an empty — i.e. disabled — field, which
// would be indistinguishable from "this platform has no default".
func TestResolveDirKeepsUnresolvable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "bin")
	if got := resolveDir(missing); got != missing {
		t.Errorf("resolveDir(missing) = %q, want it unchanged", got)
	}
	if got := resolveDir(""); got != "" {
		t.Errorf("resolveDir(\"\") = %q, want \"\"", got)
	}
}

// TestDetectSymlinkedManagerRoot is the end-to-end regression for the two
// layouts where an unexpanded root did not merely lose the update offer but
// produced a *wrong command*: bun globals and legacy pnpm globals both keep a
// plain node_modules/<pkg> segment, so the npm step claimed them and offered
// `npm install -g <pkg>` — a duplicate under npm's prefix, shadowed on PATH by
// the original. Both returned exactly that before resolveManagerDirs expanded
// the roots.
func TestDetectSymlinkedManagerRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink/PATH fixture is unix-only")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realRoot := filepath.Join(base, "real")
	rootLink := filepath.Join(base, "link")
	mustMkdirAll(t, realRoot)
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		env      string // env var naming the root, set to the symlinked path
		tool     string
		pkgPath  []string // package binary, relative to the real root
		wantArgv []string
	}{
		{
			name:     "bun global",
			env:      "BUN_INSTALL",
			tool:     "prettier",
			pkgPath:  []string{"install", "global", "node_modules", "prettier", "bin", "prettier.cjs"},
			wantArgv: []string{"bun", "add", "-g", "prettier"},
		},
		{
			name:     "pnpm legacy global",
			env:      "PNPM_HOME",
			tool:     "tsc",
			pkgPath:  []string{"global", "5", "node_modules", "typescript", "bin", "tsc"},
			wantArgv: []string{"pnpm", "add", "-g", "typescript"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(realRoot, tt.name)
			pkgBin := filepath.Join(append([]string{root}, tt.pkgPath...)...)
			mustMkdirAll(t, filepath.Dir(pkgBin))
			if err := os.WriteFile(pkgBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			binDir := filepath.Join(root, "bin")
			mustMkdirAll(t, binDir)
			if err := os.Symlink(pkgBin, filepath.Join(binDir, tt.tool)); err != nil {
				t.Fatal(err)
			}
			// The root as a shell rc would export it: through the symlink.
			t.Setenv(tt.env, filepath.Join(rootLink, tt.name))
			t.Setenv("PATH", binDir)

			plan, err := Detect(loader.Tool{Name: tt.tool})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !equalStrings(plan.Argv, tt.wantArgv) {
				t.Errorf("Argv = %v, want %v", plan.Argv, tt.wantArgv)
			}
		})
	}
}

func TestCustomPlan(t *testing.T) {
	// An update_cmd runs through the platform shell: cmd /c on Windows (so a
	// winget or PowerShell command needs no Git Bash), sh -c everywhere else —
	// and "everywhere else" is the *default* branch, not two named platforms,
	// which the plan9 row is what pins.
	const cmd = "winget upgrade --id BurntSushi.ripgrep && echo done"

	tests := []struct {
		name      string
		goos      string
		wantShell []string
	}{
		{"windows", "windows", []string{"cmd", "/c"}},
		{"linux", "linux", []string{"sh", "-c"}},
		{"darwin", "darwin", []string{"sh", "-c"}},
		{"unrecognized goos falls back to sh", "plan9", []string{"sh", "-c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := customPlan(tt.goos, cmd)

			if plan.Manager != "custom" {
				t.Errorf("Manager = %q, want %q", plan.Manager, "custom")
			}
			wantArgv := append(append([]string{}, tt.wantShell...), cmd)
			if !equalStrings(plan.Argv, wantArgv) {
				t.Errorf("Argv = %v, want %v", plan.Argv, wantArgv)
			}
			// Display stays the raw command — the confirm dialog shows what the
			// user wrote, never the shell wrapper.
			if plan.Display != cmd {
				t.Errorf("Display = %q, want %q", plan.Display, cmd)
			}
		})
	}
}

func TestDetectUpdateCmdOverride(t *testing.T) {
	// A tool with UpdateCmd set returns a custom plan even when the binary is
	// not on PATH — proving detection (LookPath) is skipped entirely.
	tool := loader.Tool{Name: "definitely-not-a-real-binary-xyz", UpdateCmd: "brew upgrade rg && echo done"}
	plan, err := Detect(tool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Manager != "custom" {
		t.Errorf("Manager = %q, want %q", plan.Manager, "custom")
	}
	// Spelled out here rather than taken from customPlan: the expectation has to
	// stay independent of the code under test, and Detect passes runtime.GOOS,
	// so the shell is the host's.
	wantShell := []string{"sh", "-c"}
	if runtime.GOOS == "windows" {
		wantShell = []string{"cmd", "/c"}
	}
	wantArgv := append(wantShell, "brew upgrade rg && echo done")
	if !equalStrings(plan.Argv, wantArgv) {
		t.Errorf("Argv = %v, want %v", plan.Argv, wantArgv)
	}
	if plan.Display != "brew upgrade rg && echo done" {
		t.Errorf("Display = %q, want %q", plan.Display, "brew upgrade rg && echo done")
	}
}

func TestDetectMissingBinary(t *testing.T) {
	// Empty brew prefix: no keg can be found by name either, so the miss is
	// conclusive regardless of what the host machine has installed.
	setTestBrewPrefix(t, t.TempDir())

	tool := loader.Tool{Name: "definitely-not-a-real-binary-xyz"}
	_, err := Detect(tool)
	if !errors.Is(err, ErrUnknownManager) {
		t.Fatalf("err = %v, want ErrUnknownManager", err)
	}
}

// setTestBrewPrefix points brewPrefix at dir for the duration of the test,
// restoring the previous override afterwards.
func setTestBrewPrefix(t *testing.T, dir string) {
	t.Helper()
	orig := testBrewPrefix
	testBrewPrefix = dir
	t.Cleanup(func() { testBrewPrefix = orig })
}

func TestBrewNamePlanAt(t *testing.T) {
	prefix := t.TempDir()
	// rust: the motivating case — a formula whose binaries (rustc, cargo) are
	// named differently, so LookPath("rust") can never find it.
	mustMkdirAll(t, filepath.Join(prefix, "Cellar", "rust", "1.96.1"))
	mustMkdirAll(t, filepath.Join(prefix, "Caskroom", "someapp", "2.0.0"))
	// A plain file, not a directory — must not be mistaken for a keg.
	mustMkdirAll(t, filepath.Join(prefix, "Cellar"))
	if err := os.WriteFile(filepath.Join(prefix, "Cellar", "notadir"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		tool     string
		prefix   string
		wantOK   bool
		wantArgv []string
	}{
		{"cellar formula", "rust", prefix, true, []string{"brew", "upgrade", "rust"}},
		{"caskroom cask", "someapp", prefix, true, []string{"brew", "upgrade", "someapp"}},
		{"no such keg", "nothere", prefix, false, nil},
		{"keg path is a file", "notadir", prefix, false, nil},
		{"no brew prefix", "rust", "", false, nil},
		{"empty name", "", prefix, false, nil},
		// Traversal guard: a brew formula name is a bare identifier.
		{"name with slash", "../../etc", prefix, false, nil},
		{"name with backslash", `..\..\etc`, prefix, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, ok := brewNamePlanAt(tt.tool, tt.prefix)
			if ok != tt.wantOK {
				t.Fatalf("brewNamePlanAt(%q, %q) ok = %v, want %v", tt.tool, tt.prefix, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if plan.Manager != "brew" {
				t.Errorf("Manager = %q, want %q", plan.Manager, "brew")
			}
			if !equalStrings(plan.Argv, tt.wantArgv) {
				t.Errorf("Argv = %v, want %v", plan.Argv, tt.wantArgv)
			}
			if want := strings.Join(tt.wantArgv, " "); plan.Display != want {
				t.Errorf("Display = %q, want %q", plan.Display, want)
			}
		})
	}
}

// TestDetectBrewByName pins the Detect branch: a tracked name with no binary of
// its own still resolves to brew when a keg of that name exists — the "rust is
// tracked, rustc is installed" case that used to dead-end in ErrUnknownManager.
func TestDetectBrewByName(t *testing.T) {
	prefix := t.TempDir()
	mustMkdirAll(t, filepath.Join(prefix, "Cellar", "rust", "1.96.1"))
	setTestBrewPrefix(t, prefix)
	// An empty PATH guarantees the LookPath miss this branch hangs off.
	t.Setenv("PATH", t.TempDir())

	plan, err := Detect(loader.Tool{Name: "rust"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Manager != "brew" {
		t.Errorf("Manager = %q, want %q", plan.Manager, "brew")
	}
	wantArgv := []string{"brew", "upgrade", "rust"}
	if !equalStrings(plan.Argv, wantArgv) {
		t.Errorf("Argv = %v, want %v", plan.Argv, wantArgv)
	}

	// An explicit UpdateCmd still wins over the brew branch.
	custom, err := Detect(loader.Tool{Name: "rust", UpdateCmd: "rustup update"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if custom.Manager != "custom" || custom.Display != "rustup update" {
		t.Errorf("UpdateCmd override lost to the brew branch: %+v", custom)
	}
}

// TestDetectBrewByNameChainExhausted pins the second brew-by-name site: the
// binary IS on PATH but belongs to no known manager — a Homebrew cask app whose
// executable lives inside the .app bundle (agterm), matching neither the Cellar
// regex nor any other chain step. The keg/cask directory makes
// `brew upgrade <name>` self-validating here exactly as on the LookPath miss.
func TestDetectBrewByNameChainExhausted(t *testing.T) {
	prefix := t.TempDir()
	mustMkdirAll(t, filepath.Join(prefix, "Caskroom", "someapp", "0.15.1"))
	setTestBrewPrefix(t, prefix)

	// Real executables on PATH whose paths match no manager pattern.
	binDir := t.TempDir()
	for _, name := range []string{"someapp", "orphan"} {
		script := filepath.Join(binDir, name)
		if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)

	plan, err := Detect(loader.Tool{Name: "someapp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Manager != "brew" {
		t.Errorf("Manager = %q, want %q", plan.Manager, "brew")
	}
	wantArgv := []string{"brew", "upgrade", "someapp"}
	if !equalStrings(plan.Argv, wantArgv) {
		t.Errorf("Argv = %v, want %v", plan.Argv, wantArgv)
	}

	// Without a keg of that name the exhausted chain still dead-ends.
	if _, err := Detect(loader.Tool{Name: "orphan"}); !errors.Is(err, ErrUnknownManager) {
		t.Fatalf("orphan err = %v, want ErrUnknownManager", err)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDetectResolvesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink/PATH fixture is unix-only")
	}
	tmp := t.TempDir()

	// Real binary under a Homebrew Cellar-shaped path.
	cellarBin := filepath.Join(tmp, "opt", "Cellar", "mytool", "1.0.0", "bin", "mytool")
	if err := os.MkdirAll(filepath.Dir(cellarBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cellarBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A bin dir on PATH holds a symlink to the real binary.
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "mytool")
	if err := os.Symlink(cellarBin, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	plan, err := Detect(loader.Tool{Name: "mytool"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// EvalSymlinks must have followed the link into the Cellar path → brew.
	if plan.Manager != "brew" {
		t.Errorf("Manager = %q, want %q", plan.Manager, "brew")
	}
	wantArgv := []string{"brew", "upgrade", "mytool"}
	if !equalStrings(plan.Argv, wantArgv) {
		t.Errorf("Argv = %v, want %v", plan.Argv, wantArgv)
	}
}

// TestDetectResolvesBunGlobal pins the Detect → resolveManagerDirs() →
// detectFromPath threading, which no pure-core row can reach: with the dirs
// argument dropped on the floor every table row above would still pass while
// the shipped binary fed a zero-value managerDirs into the core and never
// claimed a uv, pnpm or bun path again.
func TestDetectResolvesBunGlobal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink/PATH fixture is unix-only")
	}
	// EvalSymlinks up front: on macOS t.TempDir() hands back a /var path that is
	// itself a symlink to /private/var, and Detect compares the *resolved*
	// binary path against BUN_INSTALL.
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// The real file: $BUN_INSTALL/install/global/node_modules/<pkg>/<bin>.
	pkgBin := filepath.Join(tmp, "install", "global", "node_modules", "prettier", "bin", "prettier.cjs")
	mustMkdirAll(t, filepath.Dir(pkgBin))
	if err := os.WriteFile(pkgBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// $BUN_INSTALL/bin/<name> symlinks to it, and that bin dir is the PATH.
	binDir := filepath.Join(tmp, "bin")
	mustMkdirAll(t, binDir)
	if err := os.Symlink(pkgBin, filepath.Join(binDir, "prettier")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUN_INSTALL", tmp)
	t.Setenv("PATH", binDir)

	plan, err := Detect(loader.Tool{Name: "prettier"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Manager != "bun" {
		t.Errorf("Manager = %q, want %q", plan.Manager, "bun")
	}
	wantArgv := []string{"bun", "add", "-g", "prettier"}
	if !equalStrings(plan.Argv, wantArgv) {
		t.Errorf("Argv = %v, want %v", plan.Argv, wantArgv)
	}
}

// setTestHomeDir points homeDir at dir for the duration of the test, restoring
// the previous override afterwards (the setTestBrewPrefix shape).
func setTestHomeDir(t *testing.T, dir string) {
	t.Helper()
	orig := testHomeDir
	testHomeDir = dir
	t.Cleanup(func() { testHomeDir = orig })
}

// TestDetectResolvesUvTool is the only Detect-level test that reaches a manager
// root through its *default* rather than its env var, and that is the point:
// the bun and pnpm fixtures below set BUN_INSTALL/PNPM_HOME, so the `home` and
// `goos` arguments of resolveManagerDirs go unexercised there — swapping the two
// (both are strings, so it compiles) left the whole suite green until this test
// existed.
func TestDetectResolvesUvTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uv has no Windows default and the fixture needs symlinks")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Both env overrides off, so resolution has to go through home + goos.
	t.Setenv("UV_TOOL_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	setTestHomeDir(t, home)

	// The verified uv layout: <uvTools>/<pkg>/bin/<binary>, behind a symlink in
	// uv's own bin dir (measured against uv 0.11.32).
	pkgBin := filepath.Join(home, ".local", "share", "uv", "tools", "cowsay", "bin", "cowsay")
	mustMkdirAll(t, filepath.Dir(pkgBin))
	if err := os.WriteFile(pkgBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, ".local", "bin")
	mustMkdirAll(t, binDir)
	if err := os.Symlink(pkgBin, filepath.Join(binDir, "cowsay")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	plan, err := Detect(loader.Tool{Name: "cowsay"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Manager != "uv" {
		t.Errorf("Manager = %q, want %q", plan.Manager, "uv")
	}
	wantArgv := []string{"uv", "tool", "upgrade", "cowsay"}
	if !equalStrings(plan.Argv, wantArgv) {
		t.Errorf("Argv = %v, want %v", plan.Argv, wantArgv)
	}
}

// TestDetectReadsPnpmShim pins the other half of the Detect wiring: the shim
// read. pnpm's bin entries are scripts, not symlinks, so without this read the
// core gets an empty shimTarget and a real pnpm global is undetectable.
func TestDetectReadsPnpmShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh shim/PATH fixture is unix-only")
	}
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(tmp, "global", "v11", "8f3a2c1", "node_modules", "typescript", "bin", "tsc")
	binDir := filepath.Join(tmp, "bin")
	mustMkdirAll(t, binDir)
	shim := "#!/bin/sh\nexec node \"" + target + "\" \"$@\"\n# cmd-shim-target=" + target + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "tsc"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PNPM_HOME", tmp)
	t.Setenv("PATH", binDir)

	plan, err := Detect(loader.Tool{Name: "tsc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Manager != "pnpm" {
		t.Errorf("Manager = %q, want %q", plan.Manager, "pnpm")
	}
	wantArgv := []string{"pnpm", "add", "-g", "typescript"}
	if !equalStrings(plan.Argv, wantArgv) {
		t.Errorf("Argv = %v, want %v", plan.Argv, wantArgv)
	}
}

func TestCargoCrateFromList(t *testing.T) {
	list := "" +
		"exa v0.10.1:\n" +
		"    exa\n" +
		"ripgrep v14.1.0 (https://github.com/BurntSushi/ripgrep):\n" +
		"    rg\n" +
		"bat v0.24.0:\n" +
		"    bat\n"

	tests := []struct {
		binName string
		want    string
	}{
		{"rg", "ripgrep"},
		{"exa", "exa"},
		{"bat", "bat"},
		{"nonexistent", ""},
	}
	for _, tt := range tests {
		if got := cargoCrateFromList(list, tt.binName); got != tt.want {
			t.Errorf("cargoCrateFromList(_, %q) = %q, want %q", tt.binName, got, tt.want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func join(argv []string) string {
	return strings.Join(argv, " ")
}
