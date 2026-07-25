package model

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/loader"
)

// ctrlKey builds a ctrl+<r> key message whose String() matches the switch cases
// in Update ("ctrl+d" etc.).
func ctrlKey(r rune) tea.KeyMsg {
	switch r {
	case 'd':
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case 'u':
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case 'f':
		return tea.KeyMsg{Type: tea.KeyCtrlF}
	case 'b':
		return tea.KeyMsg{Type: tea.KeyCtrlB}
	}
	panic("unsupported ctrl rune")
}

// scrollModel builds a ready 80x24 model with nTools tracked and tall content
// loaded into the brief/help viewports so paging/scrolling has room to move.
// helpEntries stays empty, so j/k scroll (not spotlight-navigate) in [3].
func scrollModel(t *testing.T, nTools int) Model {
	t.Helper()
	metas := make([]loader.ToolMeta, nTools)
	for i := range metas {
		metas[i] = loader.ToolMeta{Name: fmt.Sprintf("tool%02d", i)}
	}
	m := New(metas)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.briefViewport.SetContent(strings.Repeat("line\n", 100))
	m.helpViewport.SetContent(strings.Repeat("line\n", 100))
	m.helpEntries = nil
	return m
}

func yOffsetOf(m Model, focus int) int {
	if focus == focusBrief {
		return m.briefViewport.YOffset
	}
	return m.helpViewport.YOffset
}

func setYOffset(m *Model, focus, off int) {
	if focus == focusBrief {
		m.briefViewport.SetYOffset(off)
	} else {
		m.helpViewport.SetYOffset(off)
	}
}

// TestScrollLineStepUnified: j and ↓ (k and ↑) move a content viewport by the
// same 3-line step in both focusBrief and focusHelp — the unification this
// plan introduces (previously j was 1 line, arrows 3).
func TestScrollLineStepUnified(t *testing.T) {
	for _, focus := range []int{focusBrief, focusHelp} {
		t.Run(fmt.Sprintf("focus_%d", focus), func(t *testing.T) {
			for _, down := range []tea.KeyMsg{keyRunes("j"), {Type: tea.KeyDown}} {
				m := scrollModel(t, 3)
				m.focus = focus
				nm := mustModel(m.Update(down))
				if got := yOffsetOf(nm, focus); got != 3 {
					t.Errorf("%q: YOffset = %d, want 3", down.String(), got)
				}
			}
			for _, up := range []tea.KeyMsg{keyRunes("k"), {Type: tea.KeyUp}} {
				m := scrollModel(t, 3)
				m.focus = focus
				setYOffset(&m, focus, 10)
				nm := mustModel(m.Update(up))
				if got := yOffsetOf(nm, focus); got != 7 {
					t.Errorf("%q: YOffset = %d, want 7 (10 - 3)", up.String(), got)
				}
			}
		})
	}
}

// TestScrollHalfAndFullPage: ctrl+d/ctrl+u half-page and ctrl+f/ctrl+b/pgdn/
// pgup/space full-page the brief/help viewports. Each half-page moves further
// than the 3-line step, and each full-page moves further than a half-page.
func TestScrollHalfAndFullPage(t *testing.T) {
	for _, focus := range []int{focusBrief, focusHelp} {
		t.Run(fmt.Sprintf("focus_%d", focus), func(t *testing.T) {
			half := yOffsetOf(mustModel(scrollModel(t, 3).setFocusFor(focus).Update(ctrlKey('d'))), focus)
			if half <= 3 {
				t.Errorf("ctrl+d half-page YOffset = %d, want > 3 (line step)", half)
			}
			for _, full := range []tea.KeyMsg{{Type: tea.KeyPgDown}, ctrlKey('f'), keyRunes(" ")} {
				got := yOffsetOf(mustModel(scrollModel(t, 3).setFocusFor(focus).Update(full)), focus)
				if got <= half {
					t.Errorf("%q full-page YOffset = %d, want > half-page %d", full.String(), got, half)
				}
			}
			// ctrl+u / ctrl+b / pgup move back up from an offset.
			for _, up := range []tea.KeyMsg{ctrlKey('u'), ctrlKey('b'), {Type: tea.KeyPgUp}} {
				m := scrollModel(t, 3)
				m.focus = focus
				setYOffset(&m, focus, 40)
				got := yOffsetOf(mustModel(m.Update(up)), focus)
				if got >= 40 {
					t.Errorf("%q: YOffset = %d, want < 40 (scrolled up)", up.String(), got)
				}
			}
		})
	}
}

// setFocusFor is a tiny test helper so the table above can chain .Update off a
// focused model without a local var.
func (m Model) setFocusFor(focus int) Model {
	m.focus = focus
	return m
}

// TestScrollToolsCtrlHalfPage: ctrl+d/ctrl+u move the tools selection by half a
// page, clamped at both ends.
func TestScrollToolsCtrlHalfPage(t *testing.T) {
	m := scrollModel(t, 40)
	m.focus = focusTools
	half := max(m.toolsViewport.Height/2, 1)

	nm := mustModel(m.Update(ctrlKey('d')))
	if nm.metaSelected != half {
		t.Errorf("ctrl+d selection = %d, want %d (half page)", nm.metaSelected, half)
	}
	// Clamp at the bottom.
	m.metaSelected = 39
	m.setToolsContent()
	nm = mustModel(m.Update(ctrlKey('d')))
	if nm.metaSelected != 39 {
		t.Errorf("ctrl+d at end: selection = %d, want 39 (clamped)", nm.metaSelected)
	}
	// ctrl+u from the top clamps at 0.
	m.metaSelected = 0
	m.setToolsContent()
	nm = mustModel(m.Update(ctrlKey('u')))
	if nm.metaSelected != 0 {
		t.Errorf("ctrl+u at top: selection = %d, want 0 (clamped)", nm.metaSelected)
	}
}

// TestScrollToolsGotoFirstLast: g/G in focusTools jump to the first/last tool
// (card follows via selectMeta) and are no-ops on an empty list.
func TestScrollToolsGotoFirstLast(t *testing.T) {
	m := scrollModel(t, 40)
	m.focus = focusTools
	m.metaSelected = 20
	m.setToolsContent()

	if nm := mustModel(m.Update(keyRunes("G"))); nm.metaSelected != 39 {
		t.Errorf("G: selection = %d, want 39 (last)", nm.metaSelected)
	}
	if nm := mustModel(m.Update(keyRunes("g"))); nm.metaSelected != 0 {
		t.Errorf("g: selection = %d, want 0 (first)", nm.metaSelected)
	}

	// Empty list: g/G are no-ops, no panic.
	e := New([]loader.ToolMeta{})
	updated, _ := e.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	e = updated.(Model)
	e.focus = focusTools
	for _, k := range []tea.KeyMsg{keyRunes("g"), keyRunes("G"), ctrlKey('d'), ctrlKey('u')} {
		if nm := mustModel(e.Update(k)); nm.metaSelected != 0 {
			t.Errorf("%q on empty list: selection = %d, want 0", k.String(), nm.metaSelected)
		}
	}
}

// TestHiddenKeymapGone: with the default viewport keymap zeroed, the single
// letters d/u/f/b no longer scroll [3], and h/l no longer shift the viewport
// horizontally after a focus change.
func TestHiddenKeymapGone(t *testing.T) {
	t.Run("single letters do not scroll help", func(t *testing.T) {
		for _, k := range []string{"d", "u", "f", "b"} {
			m := scrollModel(t, 3)
			m.focus = focusHelp
			nm := mustModel(m.Update(keyRunes(k)))
			if nm.helpViewport.YOffset != 0 {
				t.Errorf("%q scrolled help: YOffset = %d, want 0 (no hidden keymap)", k, nm.helpViewport.YOffset)
			}
		}
	})
	t.Run("l does not shift horizontally", func(t *testing.T) {
		// xOffset is unexported, so prove no horizontal scroll via the rendered
		// view: with wide content and a narrow viewport the old hidden keymap
		// would have scrolled 'l' rightward, changing viewport.View().
		wide := strings.Repeat("this is a very long line that overflows the narrow viewport width considerably\n", 50)

		// l in focusHelp has no other effect (no focus sub-case, no fetch), so a
		// byte-identical help view is exactly "the horizontal keymap is gone".
		m := scrollModel(t, 3)
		m.focus = focusHelp
		m.helpViewport.SetContent(wide)
		before := m.helpViewport.View()
		if nm := mustModel(m.Update(keyRunes("l"))); nm.helpViewport.View() != before {
			t.Errorf("l in focusHelp shifted the help viewport")
		}

		// l from focusBrief moves focus to help but must not scroll it sideways.
		m = scrollModel(t, 3)
		m.focus = focusBrief
		m.helpViewport.SetContent(wide)
		beforeHelp := m.helpViewport.View()
		if nm := mustModel(m.Update(keyRunes("l"))); nm.helpViewport.View() != beforeHelp {
			t.Errorf("l from focusBrief shifted the help viewport after the focus change")
		}
	})
}

// TestWheelSurvivesKeymapZeroing: wheel-down over each of tools/brief/help still
// advances that viewport's YOffset — wheel rides MouseWheelEnabled, a field
// separate from the keymap that was zeroed.
func TestWheelSurvivesKeymapZeroing(t *testing.T) {
	names := make([]string, 40)
	for i := range names {
		names[i] = fmt.Sprintf("tool%02d", i)
	}
	base := newMouseTestModel(t, 80, 24, names...)
	base.briefViewport.SetContent(strings.Repeat("line\n", 100))
	base.helpViewport.SetContent(strings.Repeat("line\n", 100))

	tools := mustModel(base.Update(wheelDown(1, 5)))
	if tools.toolsViewport.YOffset == 0 {
		t.Error("wheel over tools did not scroll")
	}
	brief := mustModel(base.Update(wheelDown(base.toolsW+3, 5)))
	if brief.briefViewport.YOffset == 0 {
		t.Error("wheel over brief did not scroll")
	}
	help := mustModel(base.Update(wheelDown(base.toolsW+base.briefW+5, 5)))
	if help.helpViewport.YOffset == 0 {
		t.Error("wheel over help did not scroll")
	}
}
