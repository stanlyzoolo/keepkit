package main

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
	"github.com/stanlyzoolo/keepkit/internal/model"
)

func TestHandleCLI(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantDone bool
		wantOut  string // substring expected on stdout
		wantErr  string // substring expected on stderr
	}{
		{name: "no args launches TUI", args: nil, wantCode: 0, wantDone: false},
		{name: "--version", args: []string{"--version"}, wantCode: 0, wantDone: true, wantOut: "keepkit "},
		{name: "-V", args: []string{"-V"}, wantCode: 0, wantDone: true, wantOut: "keepkit "},
		{name: "-v", args: []string{"-v"}, wantCode: 0, wantDone: true, wantOut: "keepkit "},
		{name: "version word", args: []string{"version"}, wantCode: 0, wantDone: true, wantOut: "keepkit "},
		{name: "--help", args: []string{"--help"}, wantCode: 0, wantDone: true, wantOut: "Usage:"},
		{name: "-h", args: []string{"-h"}, wantCode: 0, wantDone: true, wantOut: "Usage:"},
		{name: "help word", args: []string{"help"}, wantCode: 0, wantDone: true, wantOut: "Usage:"},
		{name: "unknown flag", args: []string{"--bogus"}, wantCode: 2, wantDone: true, wantErr: `unknown argument "--bogus"`},
		{name: "unknown word", args: []string{"frobnicate"}, wantCode: 2, wantDone: true, wantErr: `unknown argument "frobnicate"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut strings.Builder
			code, done := handleCLI(tt.args, &out, &errOut)
			if code != tt.wantCode || done != tt.wantDone {
				t.Fatalf("handleCLI(%v) = (%d, %v), want (%d, %v)", tt.args, code, done, tt.wantCode, tt.wantDone)
			}
			if tt.wantOut != "" && !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want substring %q", out.String(), tt.wantOut)
			}
			if tt.wantOut == "" && out.String() != "" {
				t.Errorf("stdout = %q, want empty", out.String())
			}
			if tt.wantErr != "" && !strings.Contains(errOut.String(), tt.wantErr) {
				t.Errorf("stderr = %q, want substring %q", errOut.String(), tt.wantErr)
			}
		})
	}
}

// The --version line must be parseable by the tool's own installed-version
// detection (version.versionRe wants a dotted numeric), so a release build of
// keepkit tracked inside keepkit shows up as installed.
func TestVersionOutputParseable(t *testing.T) {
	var out strings.Builder
	oldVersion := version
	version = "v0.5.1"
	defer func() { version = oldVersion }()

	if code, done := handleCLI([]string{"--version"}, &out, &strings.Builder{}); code != 0 || !done {
		t.Fatalf("handleCLI --version = (%d, %v), want (0, true)", code, done)
	}
	if got, want := out.String(), "keepkit v0.5.1\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		ldflag, mod, want string
	}{
		{"v1.2.3", "v0.0.9", "v1.2.3"}, // release ldflag always wins
		{"dev", "v0.5.0", "v0.5.0"},    // go install stamps the module version
		{"dev", "(devel)", "dev"},      // plain go build from a checkout
		{"dev", "", "dev"},
		{"", "", "dev"},
	}
	for _, tt := range tests {
		if got := resolveVersion(tt.ldflag, tt.mod); got != tt.want {
			t.Errorf("resolveVersion(%q, %q) = %q, want %q", tt.ldflag, tt.mod, got, tt.want)
		}
	}
}

// TestNewRootModelInjectsAppVersion: the model the shipped binary runs must
// carry this build's version — that injection is the only thing that turns the
// self-update feature on, and without it every release would ship with no
// self-check, no banner and no restart offer while the model package's own tests
// stayed green. Observed through Init's batch, which is where the version has its
// first user-visible effect: a release build queues the self-check command, a dev
// build queues nothing extra.
//
// The batch is only counted, never executed: the elements are live network
// fetches. The tool carries no GitHub ref for the same reason (it keeps Init's
// remote seeds out of the batch) and exists at all so the batch always holds two
// or more commands — tea.Batch hands back a single command unwrapped, and this
// asserts on the tea.BatchMsg.
func TestNewRootModelInjectsAppVersion(t *testing.T) {
	meta := []loader.ToolMeta{{Name: "localtool"}}
	batchLen := func(ver string) int {
		msg := newRootModel(meta, ver).Init()()
		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			t.Fatalf("Init() with version %q produced %T, want a tea.BatchMsg", ver, msg)
		}
		return len(batch)
	}
	dev := batchLen("dev")
	if got := batchLen("v0.4.2"); got != dev+1 {
		t.Errorf("Init queued %d cmds on a release build and %d on a dev build, want exactly one more (the self-check) — is WithAppVersion still wired?", got, dev)
	}
}

// TestRestartIfRequested pins the other half of the production wiring: the model
// p.Run() returns decides whether keepkit re-execs itself. A stand-in supplies
// the flag because model.Model can only reach it through unexported state.
func TestRestartIfRequested(t *testing.T) {
	tests := []struct {
		name  string
		final tea.Model
		want  bool
	}{
		{name: "restart requested", final: fakeFinal{restart: true}, want: true},
		{name: "quit without the restart flag", final: fakeFinal{}},
		{name: "a real model carries no flag by default", final: model.New(nil)},
		{name: "a model that cannot answer at all", final: plainFinal{}},
		{name: "no final model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			restartIfRequested(tt.final, func() { called = true })
			if called != tt.want {
				t.Errorf("restart called = %v, want %v", called, tt.want)
			}
		})
	}
}

// fakeFinal stands in for the model p.Run() returns after [U] restart.
type fakeFinal struct{ restart bool }

func (f fakeFinal) Init() tea.Cmd                       { return nil }
func (f fakeFinal) Update(tea.Msg) (tea.Model, tea.Cmd) { return f, nil }
func (f fakeFinal) View() string                        { return "" }
func (f fakeFinal) RestartRequested() bool              { return f.restart }

// plainFinal is a tea.Model with no restart flag at all — the `ok` half of the
// type assertion, which must not restart.
type plainFinal struct{}

func (p plainFinal) Init() tea.Cmd                       { return nil }
func (p plainFinal) Update(tea.Msg) (tea.Model, tea.Cmd) { return p, nil }
func (p plainFinal) View() string                        { return "" }

// TestMain isolates the files this package can reach for the whole test binary:
// main calls loader.LoadMeta, and a test here must never touch the developer's
// real tracker. Mirrors the TestMain in internal/loader, internal/model and
// internal/version. (The version package's cache/token are not redirected here:
// nothing in this package reaches them, because the two tests that build a model
// only construct it and count Init's batch — no model command is ever executed.
// A test that does execute one has to redirect them itself, aliasing the import
// the way main.go does: a plain `version` name would collide with main.go's
// ldflag variable.)
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "keepkit-main-logs")
	if err != nil {
		panic(err)
	}
	restoreLog := logx.SetDirForTesting(dir)
	cfgDir, err := os.MkdirTemp("", "keepkit-main-config")
	if err != nil {
		panic(err)
	}
	restoreMeta := loader.SetConfigDirForTesting(cfgDir)
	code := m.Run()
	restoreMeta()
	restoreLog()
	_ = os.RemoveAll(dir)
	_ = os.RemoveAll(cfgDir)
	os.Exit(code)
}

// TestConfigDirIsolated fails if the isolation above is ever removed.
func TestConfigDirIsolated(t *testing.T) {
	if loader.ConfigDirOverride() == "" {
		t.Error("loader config override is empty — tests can write the real meta.yaml")
	}
}
