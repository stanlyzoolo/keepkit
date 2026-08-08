package ui

import (
	"fmt"
	"math"
	"regexp"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// hexColorRe is the shape every value in this palette must have. lipgloss
// swallows anything else silently — a typo'd digit count or a stray space
// renders as the terminal default, which on a dark panel reads as "no style
// applied" rather than as a bug.
var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// TestReadmePaletteHexShape is a smoke guard, not a mutation-killable
// assertion: it cannot fail for any value a careful edit would produce, and
// that is the point — it catches the careless one. lipgloss.Color("#08605")
// compiles, runs, and paints nothing.
func TestReadmePaletteHexShape(t *testing.T) {
	named := map[string]lipgloss.Color{
		"ChromaColors.Keyword":  ChromaColors.Keyword,
		"ChromaColors.String":   ChromaColors.String,
		"ChromaColors.Comment":  ChromaColors.Comment,
		"ChromaColors.Number":   ChromaColors.Number,
		"ChromaColors.Function": ChromaColors.Function,
	}
	for i, c := range HeadingColors {
		named[headingName(i)] = c
	}
	for name, c := range named {
		if !hexColorRe.MatchString(string(c)) {
			t.Errorf("%s = %q, want a #RRGGBB literal", name, string(c))
		}
	}
}

// headingName spells H1..H5 for a failure message.
func headingName(i int) string {
	return string(rune('H')) + string(rune('1'+i))
}

// TestHeadingColorsLength pins the ladder at five entries. keepkitStyle indexes
// it by heading level, so a sixth entry (or a lost one) is either dead color or
// an out-of-range panic on the first README with that heading in it. H6 is
// deliberately absent — it takes the Theme.Dim role.
func TestHeadingColorsLength(t *testing.T) {
	if got := len(HeadingColors); got != 5 {
		t.Errorf("len(HeadingColors) = %d, want 5 (H1..H5; H6 is the Dim role)", got)
	}
}

// TestHeadingColorsAreDistinct pins what the ladder is FOR: the stock config
// renders H3 and H4 identically, and a ladder with a duplicated step is that
// bug again under a new name.
func TestHeadingColorsAreDistinct(t *testing.T) {
	seen := map[lipgloss.Color]int{}
	for i, c := range HeadingColors {
		if prev, dup := seen[c]; dup {
			t.Errorf("%s and %s are both %q — the ladder has no step there",
				headingName(prev), headingName(i), string(c))
		}
		seen[c] = i
	}
}

// TestHeadingColorsDescend pins the ladder's DIRECTION, which is the decision
// the values encode rather than an accident of which five shades were picked.
// keepkit is a dark-terminal app, so brightness reads as prominence: the
// reverse order puts the least contrast on H1, below the floor for bold text
// and below the Dim role a caption uses, and a top-level heading nobody can
// find is an inverted ladder rather than a ladder.
//
// A reordering is a one-line edit that changes nothing else and breaks no other
// test — this is what stands in its way.
func TestHeadingColorsDescend(t *testing.T) {
	for i := 1; i < len(HeadingColors); i++ {
		prev, cur := relLuminance(t, HeadingColors[i-1]), relLuminance(t, HeadingColors[i])
		if cur >= prev {
			t.Errorf("%s (%.4f) is not quieter than %s (%.4f) — the ladder must descend, loud at the top",
				headingName(i), cur, headingName(i-1), prev)
		}
	}
}

// relLuminance is WCAG relative luminance, the same measure the contrast ratios
// in HeadingColors' doc comment were computed with.
func relLuminance(t *testing.T, c lipgloss.Color) float64 {
	t.Helper()
	hex := string(c)
	if !hexColorRe.MatchString(hex) {
		t.Fatalf("cannot measure %q — not a #RRGGBB literal", hex)
	}
	var ch [3]float64
	for i := range ch {
		var v int
		if _, err := fmt.Sscanf(hex[1+i*2:3+i*2], "%02x", &v); err != nil {
			t.Fatalf("parse %q: %v", hex, err)
		}
		s := float64(v) / 255
		if s <= 0.03928 {
			ch[i] = s / 12.92
		} else {
			ch[i] = math.Pow((s+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*ch[0] + 0.7152*ch[1] + 0.0722*ch[2]
}
