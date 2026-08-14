package model

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/stanlyzoolo/keepkit/internal/loader"
)

// newMouseTestModel builds a ready Model (viewports sized via WindowSizeMsg)
// with the given tools tracked and the first one selected.
func newMouseTestModel(t *testing.T, width, height int, names ...string) Model {
	t.Helper()
	metas := make([]loader.ToolMeta, len(names))
	for i, n := range names {
		metas[i] = loader.ToolMeta{Name: n, Note: "note of " + n}
	}
	m := New(metas)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	nm := updated.(Model)
	// Click-selection parity is asserted against the --help panel plumbing,
	// so these models opt out of the readme mode New() starts in.
	nm.helpMode = helpModeHelp
	return nm
}

func leftClick(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

func wheelDown(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress}
}

// toolRowY maps a list index to the screen row handleMouse expects
// (row 0 = top margin, row 1 = border, row 2 = first list row).
func toolRowY(m Model, idx int) int {
	// +1 for the blank row the list opens with: a tool's screen line is its
	// index one row down (buildToolRows' line map says so, and this mirrors it
	// for the flat lists these tests build).
	return 2 + 1 + idx - m.toolsViewport.YOffset
}

// forceColor pins the color profile for one test: renderLeftContent's
// focus-dependent parts are colors only (peach vs dim selection bar), and the
// default test profile strips them, which would make the assertions below pass
// against any staleness bug.
func forceColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// TestMouseFocusRefreshesToolsList is the staleness repro: a click that moves
// focus off the tools list must leave the list rendered as unfocused (dim
// selection bar). The bug it guards is a click path that writes m.focus
// directly and never re-renders the list, so it keeps the peach focused bar.
func TestMouseFocusRefreshesToolsList(t *testing.T) {
	tests := []struct {
		name  string
		click func(m Model) tea.MouseMsg
		want  int
	}{
		{"click brief", func(m Model) tea.MouseMsg { return leftClick(m.toolsW+3, 5) }, focusBrief},
		{"click help", func(m Model) tea.MouseMsg { return leftClick(m.toolsW+m.briefW+5, 5) }, focusHelp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forceColor(t)
			m := newMouseTestModel(t, 120, 24, "alpha", "beta")
			m.setFocus(focusTools)
			m.setToolsContent()

			updated, _ := m.Update(tt.click(m))
			nm := updated.(Model)
			if nm.focus != tt.want {
				t.Fatalf("focus = %d, want %d", nm.focus, tt.want)
			}

			want := firstRow(nm.renderLeftContent())
			got := strings.TrimRight(firstRow(nm.toolsViewport.View()), " ")
			if got != want {
				t.Errorf("tools row 0 = %q,\n                want %q (a fresh unfocused render)", got, want)
			}
		})
	}
}

// TestMouseFocusBackToToolsRefreshesList covers the mirror case: clicking the
// already-selected row must re-render the list as focused (peach bar), even
// though the selection does not move and selectMeta never runs.
func TestMouseFocusBackToToolsRefreshesList(t *testing.T) {
	forceColor(t)
	m := newMouseTestModel(t, 120, 24, "alpha", "beta")
	m.setFocus(focusBrief)

	updated, _ := m.Update(leftClick(1, toolRowY(m, 0)))
	nm := updated.(Model)
	if nm.focus != focusTools {
		t.Fatalf("focus = %d, want focusTools", nm.focus)
	}

	want := firstRow(nm.renderLeftContent())
	got := strings.TrimRight(firstRow(nm.toolsViewport.View()), " ")
	if got != want {
		t.Errorf("tools row 0 = %q,\n                want %q (a fresh focused render)", got, want)
	}
}

func firstRow(s string) string {
	row, _, _ := strings.Cut(s, "\n")
	return row
}

// TestMouseClickIgnoredWhileEditingNote is the wrong-target repro: with the
// note editor open on tool A, a click on tool B must not move the selection,
// and enter must still save into A.
func TestMouseClickIgnoredWhileEditingNote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newMouseTestModel(t, 80, 24, "alpha", "beta")
	m.mode = modeEditNote
	m.noteInput.SetValue("edited during click")

	updated, _ := m.Update(leftClick(1, toolRowY(m, 1)))
	nm := updated.(Model)
	if nm.metaSelected != 0 {
		t.Fatalf("click during note edit moved selection: metaSelected = %d, want 0", nm.metaSelected)
	}

	committed, _ := nm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	cm := committed.(Model)
	if got := cm.meta[0].Note; got != "edited during click" {
		t.Errorf("alpha note = %q, want the edited note", got)
	}
	if got := cm.meta[1].Note; got != "note of beta" {
		t.Errorf("beta note = %q, the edit leaked into the clicked tool", got)
	}
}

// TestMouseClickIgnoredInModalModes verifies clicks change neither selection
// nor focus while a modal input mode owns the screen.
func TestMouseClickIgnoredInModalModes(t *testing.T) {
	for _, mode := range []inputMode{modeConfirmUntrack, modeTokenInput} {
		t.Run(fmt.Sprintf("mode_%d", mode), func(t *testing.T) {
			m := newMouseTestModel(t, 80, 24, "alpha", "beta")
			m.mode = mode
			wantFocus := m.focus

			// Click another tools row, then the brief and help panels.
			for _, msg := range []tea.MouseMsg{
				leftClick(1, toolRowY(m, 1)),
				leftClick(m.toolsW+3, 5),
				leftClick(m.toolsW+m.briefW+5, 5),
			} {
				updated, _ := m.Update(msg)
				m = updated.(Model)
			}
			if m.metaSelected != 0 {
				t.Errorf("metaSelected = %d, want 0", m.metaSelected)
			}
			if m.focus != wantFocus {
				t.Errorf("focus = %d, want unchanged %d", m.focus, wantFocus)
			}
		})
	}
}

// TestMouseWheelScrollsInSearch verifies the wheel stays live in modeSearch:
// scrolling the tools panel moves the viewport without touching selection.
func TestMouseWheelScrollsInSearch(t *testing.T) {
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("tool%02d", i)
	}
	m := newMouseTestModel(t, 80, 10, names...)
	m.mode = modeSearch

	updated, _ := m.Update(wheelDown(1, 3))
	nm := updated.(Model)
	if nm.toolsViewport.YOffset == 0 {
		t.Error("wheel in modeSearch did not scroll the tools viewport")
	}
	if nm.metaSelected != 0 {
		t.Errorf("wheel moved selection: metaSelected = %d, want 0", nm.metaSelected)
	}
}

// TestMouseNoOpUnderOverlay verifies that while the [L] overlay is visible
// neither clicks nor the wheel move anything underneath.
func TestMouseNoOpUnderOverlay(t *testing.T) {
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("tool%02d", i)
	}
	for _, mode := range []inputMode{modeAPIStatus, modeTokenInput, modeHotkeys, modeToolOverlay} {
		t.Run(fmt.Sprintf("mode_%d", mode), func(t *testing.T) {
			m := newMouseTestModel(t, 80, 10, names...)
			m.mode = mode
			wantFocus := m.focus

			for _, msg := range []tea.MouseMsg{
				wheelDown(1, 3),
				leftClick(1, toolRowY(m, 1)),
				leftClick(m.toolsW+3, 5),
			} {
				updated, _ := m.Update(msg)
				m = updated.(Model)
			}
			if m.toolsViewport.YOffset != 0 {
				t.Errorf("tools viewport scrolled under the overlay: YOffset = %d", m.toolsViewport.YOffset)
			}
			if m.metaSelected != 0 || m.focus != wantFocus {
				t.Errorf("state moved under the overlay: metaSelected = %d, focus = %d", m.metaSelected, m.focus)
			}
		})
	}
}

// TestMouseClickSelectsAndFetches verifies click-selection parity with the
// keyboard path: selecting another tool returns the auto-fetch cmd and
// re-renders the help viewport from the new tool's cached help.
func TestMouseClickSelectsAndFetches(t *testing.T) {
	m := newMouseTestModel(t, 80, 24, "alpha", "beta")
	m.helpCache["alpha"] = [2]string{"ALPHA HELP", ""}
	m.helpCache["beta"] = [2]string{"BETA HELP", ""}
	m.helpViewport.SetContent(m.renderHelpContent())

	updated, cmd := m.Update(leftClick(1, toolRowY(m, 1)))
	nm := updated.(Model)
	if nm.metaSelected != 1 {
		t.Fatalf("metaSelected = %d, want 1", nm.metaSelected)
	}
	if cmd == nil {
		t.Error("click selection returned nil cmd, want the auto-fetch batch")
	}
	if !strings.Contains(nm.helpViewport.View(), "BETA HELP") {
		t.Error("help viewport still shows the previous tool's help")
	}
	if nm.helpViewport.YOffset != 0 {
		t.Errorf("help viewport YOffset = %d, want 0 after GotoTop", nm.helpViewport.YOffset)
	}
}

// TestMouseClickUncachedToolSetsLoading verifies a click on an uncached tool
// sets the same loading fields as the keyboard path.
func TestMouseClickUncachedToolSetsLoading(t *testing.T) {
	m := newMouseTestModel(t, 80, 24, "alpha", "beta")
	m.meta[1].GitHub = "github.com/example/beta"
	m.tools = loader.ToolsFromMeta(m.meta)

	updated, cmd := m.Update(leftClick(1, toolRowY(m, 1)))
	nm := updated.(Model)
	if cmd == nil {
		t.Fatal("click on uncached tool returned nil cmd")
	}
	if nm.helpLoadingFor != "beta" {
		t.Errorf("helpLoadingFor = %q, want %q", nm.helpLoadingFor, "beta")
	}
	if nm.changelogLoadingFor != "beta" {
		t.Errorf("changelogLoadingFor = %q, want %q", nm.changelogLoadingFor, "beta")
	}
}

// TestMouseClickSameRowAndEmptyArea verifies clicking the already-selected row
// returns no cmd, and a click on the empty area below the list focuses the
// tools panel without moving the selection.
func TestMouseClickSameRowAndEmptyArea(t *testing.T) {
	m := newMouseTestModel(t, 80, 24, "alpha", "beta")
	m.focus = focusBrief

	updated, cmd := m.Update(leftClick(1, toolRowY(m, 0)))
	nm := updated.(Model)
	if cmd != nil {
		t.Error("click on the already-selected row returned a cmd")
	}
	if nm.focus != focusTools {
		t.Errorf("focus = %d, want focusTools", nm.focus)
	}

	nm.focus = focusBrief
	updated, cmd = nm.Update(leftClick(1, toolRowY(nm, 10)))
	nm = updated.(Model)
	if cmd != nil {
		t.Error("click on the empty area returned a cmd")
	}
	if nm.focus != focusTools {
		t.Errorf("empty-area click: focus = %d, want focusTools", nm.focus)
	}
	if nm.metaSelected != 0 {
		t.Errorf("empty-area click moved selection: metaSelected = %d", nm.metaSelected)
	}
}

// TestMouseClickHonorsYOffset verifies the row mapping accounts for the tools
// viewport scroll offset.
func TestMouseClickHonorsYOffset(t *testing.T) {
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("tool%02d", i)
	}
	m := newMouseTestModel(t, 80, 10, names...)
	m.toolsViewport.YOffset = 5

	// Screen row 2 is the first visible list row = screen line YOffset, which
	// with the blank row the list opens with is the tool one index below it.
	updated, _ := m.Update(leftClick(1, 2))
	nm := updated.(Model)
	if nm.metaSelected != 4 {
		t.Errorf("metaSelected = %d, want 4 (YOffset honored)", nm.metaSelected)
	}
}

// TestMouseClickResolvesGroupedRow verifies a click resolves the clicked tool
// through the same grouped projection the renderer uses: with an updatable tool
// lifted to the top, clicking row 0 selects that tool, not the first meta.yaml
// entry. handleMouse needs no grouping-specific code — it maps through
// m.filteredMeta(), which is now grouped.
func TestMouseClickResolvesGroupedRow(t *testing.T) {
	m := newMouseTestModel(t, 80, 24, "alpha", "beta", "gamma")
	// gamma has an update → displayed order is [gamma, alpha, beta].
	m.versions["gamma"] = VersionInfo{Installed: "1.0", Latest: "2.0", InstalledKnown: true}
	m.setToolsContent()

	updated, _ := m.Update(leftClick(1, toolRowY(m, 0)))
	nm := updated.(Model)
	if sel, ok := nm.selectedMeta(); !ok || sel.Name != "gamma" || nm.metaSelected != 0 {
		t.Errorf("row-0 click selected %v (idx %d), want gamma (the updatable, top-grouped tool)", sel, nm.metaSelected)
	}
}

// TestMouseNoOpBeforeReady verifies mouse events before the first
// WindowSizeMsg (zero panel widths) are ignored.
func TestMouseNoOpBeforeReady(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "alpha"}, {Name: "beta"}})

	updated, cmd := m.Update(leftClick(1, 3))
	nm := updated.(Model)
	if cmd != nil {
		t.Error("mouse before ready returned a command")
	}
	if nm.metaSelected != 0 || nm.focus != m.focus {
		t.Errorf("mouse before ready changed state: metaSelected = %d, focus = %d", nm.metaSelected, nm.focus)
	}
}
