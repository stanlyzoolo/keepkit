package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stanlyzoolo/keepkit/internal/loader"
)

func TestStatusStyle(t *testing.T) {
	// Without a forced profile a non-TTY run renders every style to the bare
	// text, and all comparisons pass vacuously.
	forceColorProfile(t)
	s := DefaultStyles()
	tests := []struct {
		name   string
		status loader.Status
		want   lipgloss.Style
	}{
		{"active", loader.StatusActive, s.Ok},
		{"trying", loader.StatusTrying, s.Signal},
		{"inactive", loader.StatusInactive, s.Dim},
		{"unknown falls back to trying", loader.Status("bogus"), s.Signal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Status(tt.status)
			if got.Render("x") != tt.want.Render("x") {
				t.Errorf("Status(%q) renders %q, want %q", tt.status, got.Render("x"), tt.want.Render("x"))
			}
		})
	}
	// The three styles must be pairwise distinct, or the comparisons above
	// prove nothing even with a real color profile.
	if s.Ok.Render("x") == s.Signal.Render("x") ||
		s.Signal.Render("x") == s.Dim.Render("x") ||
		s.Ok.Render("x") == s.Dim.Render("x") {
		t.Error("status styles are not pairwise distinct; the mapping assertions are vacuous")
	}
}

// TestStylesBuildFromTheme is the guard on the one rule NewStyles has: every
// style comes from the Theme it was handed. A second theme therefore recolors
// the app entirely, with no style left behind on the default palette — which is
// what the whole Theme/Styles split exists to guarantee.
func TestStylesBuildFromTheme(t *testing.T) {
	forceColorProfile(t)
	other := Theme{
		Accent:    lipgloss.Color("#111111"),
		Signal:    lipgloss.Color("#222222"),
		Ok:        lipgloss.Color("#333333"),
		Danger:    lipgloss.Color("#444444"),
		Link:      lipgloss.Color("#555555"),
		Emphasis:  lipgloss.Color("#666666"),
		Text:      lipgloss.Color("#777777"),
		Dim:       lipgloss.Color("#888888"),
		Border:    lipgloss.Color("#999999"),
		Surface:   lipgloss.Color("#AAAAAA"),
		SignalDim: lipgloss.Color("#BBBBBB"),
	}
	def, alt := DefaultStyles(), NewStyles(other)

	// Every style must render differently under the two themes; one that comes
	// out equal is built from something other than its Theme field. Border
	// styles carry their color on the frame rather than the content, which the
	// rendered box below covers just as well.
	pairs := map[string][2]lipgloss.Style{
		"Accent":             {def.Accent, alt.Accent},
		"AccentBold":         {def.AccentBold, alt.AccentBold},
		"Signal":             {def.Signal, alt.Signal},
		"SignalBold":         {def.SignalBold, alt.SignalBold},
		"Ok":                 {def.Ok, alt.Ok},
		"Danger":             {def.Danger, alt.Danger},
		"Link":               {def.Link, alt.Link},
		"Emphasis":           {def.Emphasis, alt.Emphasis},
		"EmphasisBold":       {def.EmphasisBold, alt.EmphasisBold},
		"Text":               {def.Text, alt.Text},
		"Dim":                {def.Dim, alt.Dim},
		"Note":               {def.Note, alt.Note},
		"Rule":               {def.Rule, alt.Rule},
		"Surface":            {def.Surface, alt.Surface},
		"StatusBar":          {def.StatusBar, alt.StatusBar},
		"OverlayDim":         {def.OverlayDim, alt.OverlayDim},
		"GaugeFill":          {def.GaugeFill, alt.GaugeFill},
		"GaugeTrack":         {def.GaugeTrack, alt.GaugeTrack},
		"PanelBorder":        {def.PanelBorder, alt.PanelBorder},
		"PanelBorderFocused": {def.PanelBorderFocused, alt.PanelBorderFocused},
		"OverlayBorder":      {def.OverlayBorder, alt.OverlayBorder},
	}
	for name, pair := range pairs {
		if pair[0].Render("x") == pair[1].Render("x") {
			t.Errorf("%s renders identically under two themes — it is not built from Theme", name)
		}
	}

	if alt.Theme != other {
		t.Error("Styles.Theme does not carry the theme it was built from")
	}
}
