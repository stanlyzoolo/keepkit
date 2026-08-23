package model

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestMouseXToColumn pins the screen-X → content-column geometry that drag
// selection rests on: the brief content starts one column right of the tools
// panel's right border, is briefW-1 columns wide (the last column is the
// scrollbar), and YOffset shifts the mapped line.
func TestMouseXToColumn(t *testing.T) {
	m := newMouseTestModel(t, 120, 24, "alpha", "beta")

	start, width, ok := m.panelContentStartX(focusBrief)
	if !ok {
		t.Fatal("panelContentStartX(focusBrief) = not ok")
	}
	if start != m.toolsW+3 {
		t.Errorf("brief content start = %d, want toolsW+3 = %d", start, m.toolsW+3)
	}
	if width != m.briefW-1 {
		t.Errorf("brief content width = %d, want briefW-1 = %d", width, m.briefW-1)
	}

	// Y=2 is the first content row (margin=0, border=1).
	if pos, ok := m.mouseSelPos(focusBrief, start, 2); !ok || pos != (selPos{0, 0}) {
		t.Errorf("mouseSelPos(briefStart, 2) = %+v/%v, want line 0 col 0", pos, ok)
	}
	if pos, ok := m.mouseSelPos(focusBrief, start+3, 2); !ok || pos.col != 3 {
		t.Errorf("mouseSelPos(briefStart+3, 2) col = %d, want 3", pos.col)
	}
	// A click on the scrollbar column clamps to the content width.
	if pos, _ := m.mouseSelPos(focusBrief, start+width+1, 2); pos.col != width {
		t.Errorf("mouseSelPos(scrollbar) col = %d, want clamped to %d", pos.col, width)
	}

	m.briefViewport.SetContent(strings.Repeat("line\n", 40))
	m.briefViewport.SetYOffset(2)
	if pos, _ := m.mouseSelPos(focusBrief, start, 2); pos.line != 2 {
		t.Errorf("mouseSelPos with YOffset=2 line = %d, want 2", pos.line)
	}
}

// TestDragSelectionCopiesOnRelease drives a full drag over the brief card's
// title line and asserts the clipboard seam receives the plain text of the
// selection, the copy command reports a positive count, and the selection state
// is cleared.
func TestDragSelectionCopiesOnRelease(t *testing.T) {
	prev := writeClipboard
	var copied string
	writeClipboard = func(s string) error { copied = s; return nil }
	t.Cleanup(func() { writeClipboard = prev })

	m := newMouseTestModel(t, 120, 24, "gh")
	lines := strings.Split(m.renderCard(), "\n")
	if len(lines) < 2 {
		t.Fatalf("setup: card has %d lines", len(lines))
	}
	want := stripANSI(lines[1]) // the title line, line 1 (line 0 is the blank top row)

	start, _, _ := m.panelContentStartX(focusBrief)
	press := tea.MouseMsg{X: start, Y: 1 + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	motion := tea.MouseMsg{X: start + 10, Y: 1 + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion}
	release := tea.MouseMsg{X: start + 10, Y: 1 + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}

	updated, _ := m.Update(press)
	nm := updated.(Model)
	if !nm.selActive {
		t.Fatal("press did not begin a selection")
	}

	updated, _ = nm.Update(motion)
	nm = updated.(Model)
	updated, cmd := nm.Update(release)
	nm = updated.(Model)
	if cmd == nil {
		t.Fatal("release returned no copy command")
	}
	msg := cmd()
	if _, ok := msg.(copyDoneMsg); !ok {
		t.Fatalf("release command = %#v, want copyDoneMsg", msg)
	}
	if copied != want {
		t.Errorf("copied = %q, want %q", copied, want)
	}
	if nm.selActive {
		t.Errorf("selection still active after release")
	}
}

// TestDragStartingOnLinkDoesNotOpen: a drag that begins on a clickable link line
// must copy the selection, not open the browser — the link only opens on a pure
// click (press+release with no motion).
func TestDragStartingOnLinkDoesNotOpen(t *testing.T) {
	prev := writeClipboard
	writeClipboard = func(string) error { return nil }
	t.Cleanup(func() { writeClipboard = prev })

	m := linkedCardModel(t, linkRepo)
	m.changelogData["gh"] = changelogMsg{toolName: "gh", htmlUrl: linkRelURL, body: "release notes"}
	m.briefViewport.SetContent(m.renderCard())

	_, links := m.buildCard()
	var repoLine = -1
	for line, url := range links {
		if url == "https://"+linkRepo {
			repoLine = line
		}
	}
	if repoLine < 0 {
		t.Fatal("setup: no repo link line")
	}

	start, _, _ := m.panelContentStartX(focusBrief)
	press := tea.MouseMsg{X: start, Y: repoLine + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	motion := tea.MouseMsg{X: start + 6, Y: repoLine + 1 + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion}
	release := tea.MouseMsg{X: start + 6, Y: repoLine + 1 + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}

	updated, _ := m.Update(press)
	updated, _ = updated.(Model).Update(motion)
	_, cmd := updated.(Model).Update(release)
	if cmd == nil {
		t.Fatal("drag release returned no command")
	}
	if _, ok := cmd().(copyDoneMsg); !ok {
		t.Errorf("drag over a link dispatched a browser/copy-adjacent command, want copyDoneMsg")
	}
}

// TestEmptyClickNoCopy: a pure click on a non-link line starts and ends a
// selection without copying, and dispatches no command.
func TestEmptyClickNoCopy(t *testing.T) {
	prev := writeClipboard
	called := false
	writeClipboard = func(string) error { called = true; return nil }
	t.Cleanup(func() { writeClipboard = prev })

	m := newMouseTestModel(t, 120, 24, "gh")
	start, _, _ := m.panelContentStartX(focusBrief)

	updated, _ := m.Update(leftClick(start, 2+2))
	updated, cmd := updated.(Model).Update(leftRelease(start, 2+2))
	if cmd != nil {
		t.Errorf("empty click returned a command %#v", cmd)
	}
	if called {
		t.Errorf("empty click wrote to the clipboard")
	}
	if updated.(Model).selActive {
		t.Errorf("selection still active after empty click")
	}
}

// TestDragReleaseOutsidePanelFinalizes: a drag that begins in the brief panel
// but is released with the cursor over the tools panel (or elsewhere outside
// [2]/[3]) must still copy and clear the selection — the drag belongs to the
// panel where it was anchored, not where the cursor ended up.
func TestDragReleaseOutsidePanelFinalizes(t *testing.T) {
	prev := writeClipboard
	var copied string
	writeClipboard = func(s string) error { copied = s; return nil }
	t.Cleanup(func() { writeClipboard = prev })

	m := newMouseTestModel(t, 120, 24, "gh")
	start, _, _ := m.panelContentStartX(focusBrief)
	press := tea.MouseMsg{X: start, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	motion := tea.MouseMsg{X: start + 6, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion}
	// Released over the tools panel (X=1), well outside the brief content.
	release := tea.MouseMsg{X: 1, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}

	updated, _ := m.Update(press)
	updated, _ = updated.(Model).Update(motion)
	updated, cmd := updated.(Model).Update(release)
	if cmd == nil {
		t.Fatal("release outside the panel returned no copy command")
	}
	if _, ok := cmd().(copyDoneMsg); !ok {
		t.Errorf("release outside the panel dispatched %#v, want copyDoneMsg", cmd())
	}
	if copied == "" {
		t.Error("nothing was copied")
	}
	if updated.(Model).selActive {
		t.Error("selection still active after releasing outside the panel")
	}
}

func TestSelectionDisabledUnderOverlayAndNonNormal(t *testing.T) {
	prev := writeClipboard
	called := false
	writeClipboard = func(string) error { called = true; return nil }
	t.Cleanup(func() { writeClipboard = prev })

	for _, mode := range []inputMode{modeEditNote, modeAPIStatus} {
		m := newMouseTestModel(t, 120, 24, "gh")
		m.mode = mode
		start, _, _ := m.panelContentStartX(focusBrief)
		press := tea.MouseMsg{X: start, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
		motion := tea.MouseMsg{X: start + 5, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion}
		release := tea.MouseMsg{X: start + 5, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}

		updated, _ := m.Update(press)
		updated, _ = updated.(Model).Update(motion)
		updated, cmd := updated.(Model).Update(release)
		if updated.(Model).selActive {
			t.Errorf("mode %d: selection began while the keyboard was owned", mode)
		}
		if cmd != nil || called {
			t.Errorf("mode %d: selection copied while the keyboard was owned", mode)
		}
	}
}
