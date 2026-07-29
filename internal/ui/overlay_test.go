package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestStripANSI verifies escape sequences are removed while visible text and
// non-SGR content survive.
func TestStripANSI(t *testing.T) {
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("hello")
	if got := StripANSI(styled); got != "hello" {
		t.Errorf("StripANSI(styled) = %q, want %q", got, "hello")
	}
	if got := StripANSI("plain"); got != "plain" {
		t.Errorf("StripANSI(plain) = %q, want %q", got, "plain")
	}
	// Full escape grammar, not just SGR: private-mode CSI and OSC show up in
	// captured tool output and must not survive into rendered content.
	for in, want := range map[string]string{
		"\x1b[?1049lx":               "x", // leave alternate screen
		"\x1b[?25hx":                 "x", // show cursor
		"\x1b]0;title\x07x":          "x", // OSC window title, BEL-terminated
		"\x1b]8;;http://e\x1b\\link": "link",
		"\x1b[38;2;1;2;3mrgb\x1b[0m": "rgb",
	} {
		if got := StripANSI(in); got != want {
			t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPlaceOverlayCenters verifies the foreground box is spliced into the middle
// of the background, preserving background cells to the left and right of the
// overlaid span.
func TestPlaceOverlayCenters(t *testing.T) {
	bg := strings.Join([]string{
		"..........",
		"..........",
		"..........",
	}, "\n")
	fg := "XX"

	out := PlaceOverlay(bg, fg, DefaultStyles().OverlayDim)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("line count = %d, want 3", len(lines))
	}
	// fg height 1 over bg height 3 => centered on row 1.
	if lines[0] != ".........." || lines[2] != ".........." {
		t.Errorf("uncovered rows changed: %q / %q", lines[0], lines[2])
	}
	// fg width 2 over bg width 10 => x = 4.
	if lines[1] != "....XX...." {
		t.Errorf("overlaid row = %q, want %q", lines[1], "....XX....")
	}
}

// TestPlaceOverlayPreservesRowWidth verifies the overlaid row keeps the same
// visible width as the background row it replaced.
func TestPlaceOverlayPreservesRowWidth(t *testing.T) {
	bg := strings.Repeat("-", 20)
	fg := "MODAL"
	out := PlaceOverlay(bg, fg, DefaultStyles().OverlayDim)
	if w := lipgloss.Width(out); w != 20 {
		t.Errorf("overlaid width = %d, want 20", w)
	}
	if !strings.Contains(out, "MODAL") {
		t.Errorf("overlay content missing from %q", out)
	}
}

// TestPlaceOverlayWiderThanBackground verifies an overlay wider or taller than
// the background does not panic and renders the foreground.
func TestPlaceOverlayWiderThanBackground(t *testing.T) {
	bg := "ab"
	fg := strings.Join([]string{"WIDE-OVERLAY", "SECOND-LINE"}, "\n")
	out := PlaceOverlay(bg, fg, DefaultStyles().OverlayDim)
	if !strings.Contains(out, "WIDE-OVERLAY") {
		t.Errorf("wide overlay content missing from %q", out)
	}
}

// forceColorProfile makes lipgloss emit real ANSI attributes for the duration
// of a test — a non-TTY run strips them, which would hide dimming regressions.
func forceColorProfile(t *testing.T) {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

// TestPlaceOverlayDimsBackground verifies the visible background is repainted
// with the dim style and loses its original colors, while the fg is preserved
// verbatim. The covered row's side margins are the critical case: overlayLine
// strips bg styling itself, so a pre-applied dim would be erased there.
func TestPlaceOverlayDimsBackground(t *testing.T) {
	forceColorProfile(t)

	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	bgLine := red.Render(strings.Repeat(".", 10))
	bg := strings.Join([]string{bgLine, bgLine, bgLine}, "\n")
	fg := red.Render("XX") // styled fg must survive byte-for-byte

	out := PlaceOverlay(bg, fg, DefaultStyles().OverlayDim)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("line count = %d, want 3", len(lines))
	}

	// Derived from the theme rather than spelled as a hex literal, so a palette
	// change cannot turn this into a test of a color nothing uses any more.
	dimSeq := termenv.TrueColor.Color(string(Default.Dim)).Sequence(false)
	redSeq := "38;2;255;0;0"

	// Uncovered rows: dimmed, original color gone.
	for _, row := range []int{0, 2} {
		if !strings.Contains(lines[row], dimSeq) {
			t.Errorf("row %d not dimmed: %q", row, lines[row])
		}
		if strings.Contains(lines[row], redSeq) {
			t.Errorf("row %d kept its original color: %q", row, lines[row])
		}
	}

	// Covered row: fg verbatim, margins dimmed and stripped of the original
	// color (the fg itself is red, so check the margins around it).
	if !strings.Contains(lines[1], fg) {
		t.Fatalf("fg not preserved verbatim in %q", lines[1])
	}
	pre, post, _ := strings.Cut(lines[1], fg)
	for side, margin := range map[string]string{"left": pre, "right": post} {
		if !strings.Contains(margin, dimSeq) {
			t.Errorf("%s margin of the covered row not dimmed: %q", side, margin)
		}
		if strings.Contains(margin, redSeq) {
			t.Errorf("%s margin of the covered row kept its original color: %q", side, margin)
		}
	}
}

// TestPlaceOverlayDimKeepsAlignment verifies dimming does not change any
// row's visible width (the splice columns must stay put).
func TestPlaceOverlayDimKeepsAlignment(t *testing.T) {
	forceColorProfile(t)

	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	bgLine := styled.Render(strings.Repeat("-", 30))
	bg := strings.Join([]string{bgLine, bgLine, bgLine, bgLine}, "\n")

	out := PlaceOverlay(bg, "MODAL", DefaultStyles().OverlayDim)
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w != 30 {
			t.Errorf("row %d visible width = %d, want 30", i, w)
		}
	}
}

// TestDropVisible verifies visible columns are dropped from the left and the
// remaining tail is returned intact.
func TestDropVisible(t *testing.T) {
	if got := dropVisible("abcdef", 2); got != "cdef" {
		t.Errorf("dropVisible(abcdef, 2) = %q, want %q", got, "cdef")
	}
	if got := dropVisible("abc", 5); got != "" {
		t.Errorf("dropVisible(abc, 5) = %q, want empty", got)
	}
}

// TestTruncateVisible verifies truncation to a visible-column budget.
func TestTruncateVisible(t *testing.T) {
	if got := truncateVisible("abcdef", 3); got != "abc" {
		t.Errorf("truncateVisible(abcdef, 3) = %q, want %q", got, "abc")
	}
}
