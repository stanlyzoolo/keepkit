package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour/styles"

	"github.com/stanlyzoolo/keepkit/internal/ui"
)

// deref reports a *string style field's value, naming the field when it is nil
// so a failure says which override went missing.
func deref(t *testing.T, field string, p *string) string {
	t.Helper()
	if p == nil {
		t.Fatalf("%s is nil, want a color", field)
	}
	return *p
}

// The theme is asserted at the struct level, not on rendered output: tests have
// no TTY, so lipgloss.ColorProfile() is Ascii and glamour strips every color it
// would emit. These fields are the only real coverage the theme can get.
func TestKeepkitStyleOverrides(t *testing.T) {
	for _, dark := range []bool{true, false} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			cfg := keepkitStyle(dark)

			if got := deref(t, "H1.Color", cfg.H1.Color); got != string(ui.ColorPrimary) {
				t.Errorf("H1.Color = %q, want %q", got, string(ui.ColorPrimary))
			}
			// The bright plate is the main thing this theme exists to remove.
			if cfg.H1.BackgroundColor != nil {
				t.Errorf("H1.BackgroundColor = %q, want none", *cfg.H1.BackgroundColor)
			}
			if got := deref(t, "H2.Color", cfg.H2.Color); got != string(ui.ColorCategory) {
				t.Errorf("H2.Color = %q, want %q", got, string(ui.ColorCategory))
			}
			if cfg.Document.Margin == nil || *cfg.Document.Margin != 0 {
				t.Errorf("Document.Margin = %v, want 0 — the panel frame is the margin", cfg.Document.Margin)
			}
			// Cloned, not built from scratch: a field the theme never mentions
			// must still carry the standard config's value.
			if cfg.List.LevelIndent == 0 {
				t.Error("List.LevelIndent = 0 — the standard config was not inherited")
			}
			if cfg.CodeBlock.Chroma == nil {
				t.Error("CodeBlock.Chroma is nil — the standard config was not inherited")
			}
		})
	}
}

// The palette's text and border tints are chosen against a dark panel; on white
// the standard light colors stay.
func TestKeepkitStyleDarkOnlyOverrides(t *testing.T) {
	dark, light := keepkitStyle(true), keepkitStyle(false)

	if got := deref(t, "dark Heading.Color", dark.Heading.Color); got != string(ui.ColorText) {
		t.Errorf("dark Heading.Color = %q, want %q", got, string(ui.ColorText))
	}
	if got := deref(t, "dark H6.Color", dark.H6.Color); got != string(ui.ColorText) {
		t.Errorf("dark H6.Color = %q, want %q", got, string(ui.ColorText))
	}
	if got := deref(t, "dark LinkText.Color", dark.LinkText.Color); got != string(ui.ColorMeta) {
		t.Errorf("dark LinkText.Color = %q, want %q", got, string(ui.ColorMeta))
	}
	if got := deref(t, "dark HorizontalRule.Color", dark.HorizontalRule.Color); got != string(ui.ColorBorder) {
		t.Errorf("dark HorizontalRule.Color = %q, want %q", got, string(ui.ColorBorder))
	}
	if got := deref(t, "dark BlockQuote.Color", dark.BlockQuote.Color); got != string(ui.ColorDim) {
		t.Errorf("dark BlockQuote.Color = %q, want %q", got, string(ui.ColorDim))
	}

	if got := deref(t, "light Heading.Color", light.Heading.Color); got == string(ui.ColorText) {
		t.Errorf("light Heading.Color = %q — the dark body tint is unreadable on white", got)
	}
	if got := deref(t, "light Document.Color", light.Document.Color); got == string(ui.ColorText) {
		t.Errorf("light Document.Color = %q — the dark body tint is unreadable on white", got)
	}
}

// keepkitStyle clones structs whose pointer fields alias the package globals
// (styles.DefaultStyles hands the very same ones out), so an override that
// wrote THROUGH a cloned pointer would restyle glamour for the whole process.
// The snapshot is JSON so the comparison follows every pointer.
func TestKeepkitStyleLeavesGlobalsUntouched(t *testing.T) {
	snapshot := func(t *testing.T) (string, string) {
		t.Helper()
		d, err := json.Marshal(styles.DarkStyleConfig)
		if err != nil {
			t.Fatalf("marshal dark: %v", err)
		}
		l, err := json.Marshal(styles.LightStyleConfig)
		if err != nil {
			t.Fatalf("marshal light: %v", err)
		}
		return string(d), string(l)
	}

	wantDark, wantLight := snapshot(t)
	_, _ = keepkitStyle(true), keepkitStyle(false)
	gotDark, gotLight := snapshot(t)

	if gotDark != wantDark {
		t.Errorf("styles.DarkStyleConfig was mutated\n got: %s\nwant: %s", gotDark, wantDark)
	}
	if gotLight != wantLight {
		t.Errorf("styles.LightStyleConfig was mutated\n got: %s\nwant: %s", gotLight, wantLight)
	}
}

// What the whole pipeline does to a README, read off the visible text. Colors
// are absent here (no TTY), which is exactly why these assertions are about
// content, not styling.
func TestRenderReadmeHouseStyle(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		absent  []string
		noEmpty bool
	}{
		{
			name:   "link keeps its text and drops the href",
			raw:    "See the [docs](https://example.com/docs) for more.\n",
			want:   []string{"See the docs for more."},
			absent: []string{"example.com"},
		},
		{
			name:   "table link leaves no footnote stub",
			raw:    "| topic | where |\n|---|---|\n| usage | [manual](https://example.com/m) |\n",
			want:   []string{"manual"},
			absent: []string{"example.com"},
		},
		{
			// The URL is the content of an autolink, so it stays visible.
			name: "autolink survives",
			raw:  "Read <https://example.com/docs> first.\n",
			want: []string{"https://example.com/docs"},
		},
		{
			name: "bare url survives",
			raw:  "Read https://example.com/docs first.\n",
			want: []string{"https://example.com/docs"},
		},
		{
			name:   "badge header vanishes, prose survives",
			raw:    "# keepkit\n\n[![Build](https://b.svg)](https://ci.example.com) ![License](https://l.svg)\n\nA tracker for CLI tools.\n",
			want:   []string{"keepkit", "A tracker for CLI tools."},
			absent: []string{"b.svg", "l.svg", "ci.example.com", "Image:"},
		},
		{
			name:   "emoji heading keeps its words",
			raw:    "## 🚀 Getting Started\n\nRun it.\n",
			want:   []string{"Getting Started", "Run it."},
			absent: []string{"🚀"},
		},
		{
			name: "fenced sample survives whole",
			raw:  "# T\n\n```md\n![Build](https://b.svg)\n```\n",
			want: []string{"![Build](https://b.svg)"},
		},
	}
	for _, tt := range tests {
		for _, dark := range []bool{true, false} {
			name := tt.name + "/light"
			if dark {
				name = tt.name + "/dark"
			}
			t.Run(name, func(t *testing.T) {
				plain := stripANSI(renderReadme(tt.raw, 100, dark))
				for _, w := range tt.want {
					if !strings.Contains(plain, w) {
						t.Errorf("missing %q\n--- rendered ---\n%s", w, plain)
					}
				}
				for _, a := range tt.absent {
					if strings.Contains(plain, a) {
						t.Errorf("unexpected %q\n--- rendered ---\n%s", a, plain)
					}
				}
				// The double-space check belongs on the preprocessed source,
				// where a removal artifact would live — glamour pads table
				// cells with runs of spaces of its own, which is layout, not
				// residue.
				for _, line := range strings.Split(cleanReadmeMarkdown(cleanTerminalOutput(tt.raw)), "\n") {
					if strings.Contains(strings.TrimSpace(line), "  ") {
						t.Errorf("removal left a double space: %q", line)
					}
				}
			})
		}
	}
}

// A README that is nothing but badges and HTML cleans down to nothing, and an
// empty render is what makes readmeContent fall through to the friendly
// "No README for <name>. Press [h] for --help." placeholder.
func TestRenderReadmeBadgeOnlyIsEmpty(t *testing.T) {
	raw := "<p align=\"center\">\n  <img src=\"logo.png\">\n</p>\n\n[![Build](https://b.svg)](https://ci.example.com)\n![License](https://l.svg)\n\n<!-- nothing to see -->\n\n[badge]: https://b.svg\n"
	for _, dark := range []bool{true, false} {
		if got := renderReadme(raw, 60, dark); got != "" {
			t.Errorf("dark=%v: renderReadme = %q, want empty", dark, got)
		}
	}
}
