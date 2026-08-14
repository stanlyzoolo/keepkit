package model

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/loader"
)

// enterRun drives updateRunInput's enter with value as the typed command and
// returns the resulting model and dispatched cmd.
//
// The cmd is never *executed* by these tests: it starts a real pty, which
// belongs in internal/term's own tests and nowhere near a model test.
func enterRun(m Model, value string) (Model, tea.Cmd) {
	m.mode = modeRunInput
	m.runInput.SetValue(value)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(Model), cmd
}

// runModel is a laid-out model with two tools, sized so the overlay fits.
func runModel(t *testing.T, w, h int) Model {
	t.Helper()

	m := New([]loader.ToolMeta{{Name: "git"}, {Name: "fd"}})
	return mustModel(m.Update(tea.WindowSizeMsg{Width: w, Height: h}))
}

// TestShellCommand pins both branches of the goos-parameterized shell seam:
// unix runs the command through sh -c, Windows through cmd /c. It survives the
// tab launcher because the overlay builds its argv with it — which is what
// keeps internal/term goos-agnostic.
func TestShellCommand(t *testing.T) {
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"linux", "sh", []string{"-c", "git status"}},
		{"darwin", "sh", []string{"-c", "git status"}},
		{"windows", "cmd", []string{"/c", "git status"}},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			name, args := shellCommand(tt.goos, "git status")
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("args = %v, want %v", args, tt.wantArgs)
			}
			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

// Enter on the prompt opens the overlay on the selected tool with the geometry
// the frame will render at, and remembers the command for the next prompt.
func TestRunInputEnterOpensOverlay(t *testing.T) {
	m := runModel(t, 120, 34)

	nm, cmd := enterRun(m, "git log --oneline")

	if nm.mode != modeToolOverlay {
		t.Fatalf("mode = %v after enter, want modeToolOverlay", nm.mode)
	}
	if nm.termToolName != "git" {
		t.Errorf("termToolName = %q, want %q", nm.termToolName, "git")
	}
	wantW, wantH, _ := nm.termGeometry()
	if nm.termW != wantW || nm.termH != wantH {
		t.Errorf("geometry = %dx%d, want %dx%d", nm.termW, nm.termH, wantW, wantH)
	}
	if got := nm.lastRun["git"]; got != "git log --oneline" {
		t.Errorf("lastRun[git] = %q, want the dispatched command", got)
	}
	if cmd == nil {
		t.Error("enter dispatched no command")
	}
	// nothing has started yet: the session arrives with termStartedMsg
	if nm.termSession != nil || nm.termEmu != nil {
		t.Error("the session or emulator existed before termStartedMsg")
	}
}

// The prompt prefills with the last command for that tool, falling back to the
// tool's own name.
func TestRunInputPrefill(t *testing.T) {
	m := runModel(t, 120, 34)

	opened := mustModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if opened.mode != modeRunInput {
		t.Fatalf("mode = %v, want modeRunInput", opened.mode)
	}
	if got := opened.runInput.Value(); got != "git" {
		t.Errorf("prefill = %q, want the tool name", got)
	}

	ran, _ := enterRun(opened, "git status -sb")
	ran.mode = modeNormal
	reopened := mustModel(ran.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if got := reopened.runInput.Value(); got != "git status -sb" {
		t.Errorf("prefill = %q, want the remembered command", got)
	}
}

// A screen with no room for an overlay refuses, and — the part that matters —
// writes no lastRun: a launch that never started is not something to remember.
func TestRunInputRefusesOnTinyTerminal(t *testing.T) {
	shrinkStatusTTL(t)
	m := runModel(t, 60, 20)

	nm, _ := enterRun(m, "git log")

	if nm.mode == modeToolOverlay {
		t.Error("the overlay opened on a terminal too small for it")
	}
	if nm.statusMsg != "terminal too small to run a tool" {
		t.Errorf("statusMsg = %q, want the refusal", nm.statusMsg)
	}
	if _, ok := nm.lastRun["git"]; ok {
		t.Error("a refused launch was recorded in lastRun")
	}
}

// esc and empty input both cancel without opening anything.
func TestRunInputCancels(t *testing.T) {
	t.Run("esc", func(t *testing.T) {
		m := runModel(t, 120, 34)
		m.mode = modeRunInput
		m.runInput.SetValue("git log")

		nm := mustModel(m.Update(tea.KeyMsg{Type: tea.KeyEsc}))
		if nm.mode != modeNormal {
			t.Errorf("mode = %v after esc, want modeNormal", nm.mode)
		}
		if _, ok := nm.lastRun["git"]; ok {
			t.Error("a cancelled prompt was recorded in lastRun")
		}
	})

	for _, blank := range []string{"", "   "} {
		t.Run("blank "+blank, func(t *testing.T) {
			m := runModel(t, 120, 34)
			nm, cmd := enterRun(m, blank)
			if nm.mode != modeNormal {
				t.Errorf("mode = %v, want modeNormal", nm.mode)
			}
			if cmd != nil {
				t.Error("a blank command dispatched something")
			}
		})
	}
}

// Enter on an empty tracker opens nothing: there is no tool to run.
func TestRunInputEmptyList(t *testing.T) {
	m := New(nil)
	mm := mustModel(m.Update(tea.WindowSizeMsg{Width: 120, Height: 34}))

	nm, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if nm.(Model).mode != modeNormal {
		t.Errorf("mode = %v on an empty list, want modeNormal", nm.(Model).mode)
	}
	if cmd != nil {
		t.Error("enter on an empty list dispatched a command")
	}
}

// An update running in [3] does not block a launch: they are independent, and
// the log keeps streaming under the overlay's dim.
func TestRunDuringUpdate(t *testing.T) {
	m := runModel(t, 120, 34)
	m.updatingFor = "fd"

	nm, cmd := enterRun(m, "git status")

	if nm.mode != modeToolOverlay {
		t.Errorf("mode = %v during a running update, want the overlay to open anyway", nm.mode)
	}
	if cmd == nil {
		t.Error("no command dispatched during a running update")
	}
}
