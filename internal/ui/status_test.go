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
	tests := []struct {
		name   string
		status loader.Status
		want   lipgloss.Style
	}{
		{"active", loader.StatusActive, StatusStyleActive},
		{"trying", loader.StatusTrying, StatusStyleTrying},
		{"inactive", loader.StatusInactive, StatusStyleInactive},
		{"unknown falls back to trying", loader.Status("bogus"), StatusStyleTrying},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StatusStyle(tt.status)
			if got.Render("x") != tt.want.Render("x") {
				t.Errorf("StatusStyle(%q) renders %q, want %q", tt.status, got.Render("x"), tt.want.Render("x"))
			}
		})
	}
	// The three styles must be pairwise distinct, or the comparisons above
	// prove nothing even with a real color profile.
	if StatusStyleActive.Render("x") == StatusStyleTrying.Render("x") ||
		StatusStyleTrying.Render("x") == StatusStyleInactive.Render("x") ||
		StatusStyleActive.Render("x") == StatusStyleInactive.Render("x") {
		t.Error("status styles are not pairwise distinct; the mapping assertions are vacuous")
	}
}
