package ui

import (
	"strings"
	"testing"
)

// TestLanguageColor: the lookup is case-folded because the GitHub API answers
// with display names ("Go", "Vim Script") while the card lowercases everything
// it prints, and an unknown language must answer "no" rather than a color — the
// caller falls back to a theme role, so a language linguist has since added
// reads as unrecognized instead of as whichever known one hashed nearby.
func TestLanguageColor(t *testing.T) {
	tests := []struct {
		name string
		want string
		ok   bool
	}{
		{"Go", "#00ADD8", true},
		{"go", "#00ADD8", true},
		{"GO", "#00ADD8", true},
		{"Vim Script", "#199F4B", true},
		{"C++", "#F34B7D", true},
		{"Nonexistent Language", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := LanguageColor(tt.name)
		if ok != tt.ok {
			t.Errorf("LanguageColor(%q) known = %v, want %v", tt.name, ok, tt.ok)
		}
		if string(got) != tt.want {
			t.Errorf("LanguageColor(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestLanguageColorsAreLowercaseHex guards the table's two invariants at once: a
// key with an uppercase letter is unreachable through the case-folding lookup,
// and a value that is not a full hex triplet is a typo lipgloss would silently
// render as a terminal color index.
func TestLanguageColorsAreLowercaseHex(t *testing.T) {
	for name, color := range languageColors {
		if _, ok := LanguageColor(name); !ok {
			t.Errorf("key %q is unreachable through the case-folded lookup", name)
		}
		c := string(color)
		if len(c) != 7 || c[0] != '#' {
			t.Errorf("%q = %q, want a #RRGGBB literal", name, c)
			continue
		}
		for _, r := range c[1:] {
			if !strings.ContainsRune("0123456789ABCDEF", r) {
				t.Errorf("%q = %q, want uppercase hex digits", name, c)
				break
			}
		}
	}
}
