package model

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/muesli/termenv"

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

// testStyleWidth is the width the style tests build against. keepkitStyle only
// spends it on the divider, so any positive value does; it is named so a
// failure about a 60-cell rule is not read as a fixture coincidence.
const testStyleWidth = 60

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
			cfg := keepkitStyle(ui.Default, dark, testStyleWidth)

			// The bright plate is the main thing this theme exists to remove.
			if cfg.H1.BackgroundColor != nil {
				t.Errorf("H1.BackgroundColor = %q, want none", *cfg.H1.BackgroundColor)
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

// TestKeepkitStyleHeadingLadder pins panel [3]'s typography: one color per
// heading level from ui.HeadingColors, bold at every level, and a › depth
// marker from H3 down. The ladder is what makes a README's own structure
// visible below the second level — the stock config renders H3 through H6
// identically, which is the bug this replaced.
//
// Both variants share it: these are content typography, not a Theme tint, so
// unlike Document/Code/LinkText they are not gated on a dark background.
func TestKeepkitStyleHeadingLadder(t *testing.T) {
	for _, dark := range []bool{true, false} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			cfg := keepkitStyle(ui.Default, dark, testStyleWidth)
			levels := []*ansi.StyleBlock{&cfg.H1, &cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5}
			for i, h := range levels {
				lvl := i + 1
				want := string(ui.HeadingColors[i])
				if got := deref(t, "H"+string(rune('0'+lvl))+".Color", h.Color); got != want {
					t.Errorf("H%d.Color = %q, want ui.HeadingColors[%d] = %q", lvl, got, i, want)
				}
				if h.Bold == nil || !*h.Bold {
					t.Errorf("H%d is not bold — every level of the ladder carries weight as well as color", lvl)
				}
			}

			// H1/H2 carry NO prefix, and the empty string is an assignment, not
			// an omission: glamour's cascade overrides a child's Prefix only
			// when the child's is non-empty, so losing these two lines brings
			// the stock "# "/"## " markdown markers back rather than falling
			// through to the Heading base.
			if cfg.H1.Prefix != "" {
				t.Errorf("H1.Prefix = %q, want none — the stock marker is source, not text", cfg.H1.Prefix)
			}
			if cfg.H2.Prefix != "" {
				t.Errorf("H2.Prefix = %q, want none — the stock marker is source, not text", cfg.H2.Prefix)
			}
			// From H3 down the ladder's steps sit close together, so depth is
			// also spelled: one › per level below H2.
			for i, h := range []*ansi.StyleBlock{&cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
				want := strings.Repeat("›", i+1) + " "
				if h.Prefix != want {
					t.Errorf("H%d.Prefix = %q, want %q", i+3, h.Prefix, want)
				}
			}
			// H6 is bold in BOTH variants' intent only on dark; what is shared
			// is the marker. The color split is asserted below.
		})
	}
}

// TestKeepkitStyleTaskMarkers pins the checklist glyphs. The stock markers
// reprint the source's [ ]/[x] brackets — the same "source, not text" problem
// the heading prefixes had — and ☐/✓ say each state in one glyph. Shared:
// nothing about a checkbox depends on the background.
func TestKeepkitStyleTaskMarkers(t *testing.T) {
	for _, dark := range []bool{true, false} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			cfg := keepkitStyle(ui.Default, dark, testStyleWidth)
			if cfg.Task.Ticked != "✓ " {
				t.Errorf("Task.Ticked = %q, want %q", cfg.Task.Ticked, "✓ ")
			}
			if cfg.Task.Unticked != "☐ " {
				t.Errorf("Task.Unticked = %q, want %q", cfg.Task.Unticked, "☐ ")
			}
		})
	}
}

// TestKeepkitStyleDividerSpansWidth pins the panel's one full-width rule. The
// stock format is a short dash run floating mid-panel, which reads as a typo
// rather than as the author dividing their document.
//
// The width comes from the caller — renderReadme passes the CLAMPED wrap width,
// the same number glamour wraps to — so the rule is asserted at two of them:
// one number could be a coincidence, two say the parameter is actually used.
// The Format is shared (colorless); the Border tint is dark-only and asserted
// in TestKeepkitStyleDarkOnlyOverrides.
func TestKeepkitStyleDividerSpansWidth(t *testing.T) {
	for _, dark := range []bool{true, false} {
		for _, w := range []int{40, 120} {
			cfg := keepkitStyle(ui.Default, dark, w)
			want := "\n" + strings.Repeat("─", w) + "\n"
			if cfg.HorizontalRule.Format != want {
				t.Errorf("dark=%v width=%d: HorizontalRule.Format = %q, want %d ─ cells",
					dark, w, cfg.HorizontalRule.Format, w)
			}
		}

		// A caller with no layout yet — a hand-built model, a test — keeps the
		// stock format. A zero-width rule would be an empty string, which is a
		// divider the author wrote and the panel silently dropped.
		stock := styles.LightStyleConfig.HorizontalRule.Format
		if dark {
			stock = styles.DarkStyleConfig.HorizontalRule.Format
		}
		if got := keepkitStyle(ui.Default, dark, 0).HorizontalRule.Format; got != stock {
			t.Errorf("dark=%v width=0: HorizontalRule.Format = %q, want the stock %q", dark, got, stock)
		}
	}
}

// The palette's text and border tints are chosen against a dark panel; on white
// the standard light colors stay.
func TestKeepkitStyleDarkOnlyOverrides(t *testing.T) {
	dark, light := keepkitStyle(ui.Default, true, testStyleWidth), keepkitStyle(ui.Default, false, testStyleWidth)

	// H6 is the ladder's floor and the one level that stays a Theme role: by
	// the sixth heading "this is a heading and it is the quietest one" is the
	// honest statement, which is what Dim means. Bold is explicit because the
	// stock dark H6 is a green Bold:false override that beats the Heading base.
	if got := deref(t, "dark H6.Color", dark.H6.Color); got != string(ui.Default.Dim) {
		t.Errorf("dark H6.Color = %q, want the quietest role %q", got, string(ui.Default.Dim))
	}
	if dark.H6.Bold == nil {
		t.Error("dark H6.Bold is nil — the stock dark H6 is an explicit Bold:false that beats inheritance")
	} else if !*dark.H6.Bold {
		t.Error("dark H6.Bold = false — the stock override was inherited instead of replaced")
	}
	// On white the stock H6 stays: Dim is picked against a dark panel, and the
	// depth marker already places the level there.
	if light.H6.Color != styles.LightStyleConfig.H6.Color {
		t.Error("light H6.Color was overridden — the stock light H6 is deliberate")
	}

	if got := deref(t, "dark LinkText.Color", dark.LinkText.Color); got != string(ui.Default.Link) {
		t.Errorf("dark LinkText.Color = %q, want %q", got, string(ui.Default.Link))
	}
	if got := deref(t, "dark HorizontalRule.Color", dark.HorizontalRule.Color); got != string(ui.Default.Border) {
		t.Errorf("dark HorizontalRule.Color = %q, want %q", got, string(ui.Default.Border))
	}
	if got := deref(t, "dark BlockQuote.Color", dark.BlockQuote.Color); got != string(ui.Default.Dim) {
		t.Errorf("dark BlockQuote.Color = %q, want %q", got, string(ui.Default.Dim))
	}
	// Dim says "less important"; italic is what says "somebody else's voice".
	// A quote needs both, and italic is the one emphasis axis the panel has
	// left unspent.
	if dark.BlockQuote.Italic == nil || !*dark.BlockQuote.Italic {
		t.Error("dark BlockQuote is not italic — Dim alone reads as demoted, not as quoted")
	}

	// Document is the base every block cascades onto, so this is the panel's
	// body-text rule: the same Text role the card's changelog notes render in.
	// The two sitting side by side at slightly different grays was the last
	// thing that read as "this panel came from another app".
	if got := deref(t, "dark Document.Color", dark.Document.Color); got != string(ui.Default.Text) {
		t.Errorf("dark Document.Color = %q, want the card's body text %q", got, string(ui.Default.Text))
	}

	// Inline code reads exactly like a code span in the card's changelog:
	// Text on the Surface plate — the plate does the raising, the text stays at
	// body brightness. Not Danger (red is the card's one alarm color) and not
	// Emphasis (a README spends dozens of spans per screen, and near-white made
	// every one of them the loudest thing in the panel).
	if got := deref(t, "dark Code.Color", dark.Code.Color); got != string(ui.Default.Text) {
		t.Errorf("dark Code.Color = %q, want %q", got, string(ui.Default.Text))
	}
	if got := deref(t, "dark Code.BackgroundColor", dark.Code.BackgroundColor); got != string(ui.Default.Surface) {
		t.Errorf("dark Code.BackgroundColor = %q, want the %q plate", got, string(ui.Default.Surface))
	}
	// The CodeBlock StyleBlock is the Ascii/NO_COLOR fallback path (a color
	// terminal renders a fence through Chroma instead) and must carry the same
	// pair, or the two paths drift.
	if got := deref(t, "dark CodeBlock.Color", dark.CodeBlock.Color); got != string(ui.Default.Text) {
		t.Errorf("dark CodeBlock.Color = %q, want %q", got, string(ui.Default.Text))
	}
	if got := deref(t, "dark CodeBlock.BackgroundColor", dark.CodeBlock.BackgroundColor); got != string(ui.Default.Surface) {
		t.Errorf("dark CodeBlock.BackgroundColor = %q, want the %q plate", got, string(ui.Default.Surface))
	}
	if got := deref(t, "dark Code.Color", dark.Code.Color); got == string(ui.Default.Danger) {
		t.Errorf("dark Code.Color is still the alarm role %q", got)
	}

	if got := deref(t, "light Heading.Color", light.Heading.Color); got == string(ui.Default.Text) {
		t.Errorf("light Heading.Color = %q — the dark body tint is unreadable on white", got)
	}
	if got := deref(t, "light Document.Color", light.Document.Color); got == string(ui.Default.Text) {
		t.Errorf("light Document.Color = %q — the dark body tint is unreadable on white", got)
	}
}

// TestKeepkitStyleRepaintsChroma pins the one override that decides what a code
// fence actually looks like in a live session. glamour renders a fence through
// rules.Chroma whenever ColorProfile != Ascii, and through the CodeBlock
// StyleBlock fields otherwise — so on every real terminal the StyleBlock plate
// is never reached and this is what the user sees.
//
// The repaint is bounded to a NAMED SEVEN: Background and Text carry the plate
// (Text is the load-bearing one — chroma resolves an unset token background up
// to Text, its root, not to Background), and five token entries carry the
// syntax accents keepkit owns. Everything else is inherited from the standard
// config, which is what the DeepEqual below is for: it is the guard against
// the repaint quietly growing into a full syntax theme. Dark only — the light
// palette deliberately keeps the stock colors, so there the pointer must still
// ALIAS the global.
func TestKeepkitStyleRepaintsChroma(t *testing.T) {
	dark := keepkitStyle(ui.Default, true, testStyleWidth)
	if dark.CodeBlock.Chroma == nil {
		t.Fatal("dark CodeBlock.Chroma is nil")
	}
	// A fresh pointer, or the assignments below would have restyled glamour for
	// the whole process (TestKeepkitStyleLeavesGlobalsUntouched's rule).
	if dark.CodeBlock.Chroma == styles.DarkStyleConfig.CodeBlock.Chroma {
		t.Error("dark CodeBlock.Chroma still aliases the global — the repaint writes through it")
	}
	if got := deref(t, "dark Chroma.Background.BackgroundColor", dark.CodeBlock.Chroma.Background.BackgroundColor); got != string(ui.Default.Surface) {
		t.Errorf("dark Chroma.Background.BackgroundColor = %q, want the %q plate", got, string(ui.Default.Surface))
	}
	if got := deref(t, "dark Chroma.Text.Color", dark.CodeBlock.Chroma.Text.Color); got != string(ui.Default.Text) {
		t.Errorf("dark Chroma.Text.Color = %q, want %q", got, string(ui.Default.Text))
	}
	if got := deref(t, "dark Chroma.Text.BackgroundColor", dark.CodeBlock.Chroma.Text.BackgroundColor); got != string(ui.Default.Surface) {
		t.Errorf("dark Chroma.Text.BackgroundColor = %q, want the %q plate — without it the fence has NO plate, since chroma resolves an unset token background up to Text", got, string(ui.Default.Surface))
	}

	// The five syntax accents keepkit owns. Color only: the background is
	// inherited from the Text entry above, and repeating it here would be five
	// more places for the plate to drift.
	for _, tt := range []struct {
		name string
		got  ansi.StylePrimitive
		want string
	}{
		{"Keyword", dark.CodeBlock.Chroma.Keyword, string(ui.ChromaColors.Keyword)},
		{"LiteralString", dark.CodeBlock.Chroma.LiteralString, string(ui.ChromaColors.String)},
		{"Comment", dark.CodeBlock.Chroma.Comment, string(ui.ChromaColors.Comment)},
		{"LiteralNumber", dark.CodeBlock.Chroma.LiteralNumber, string(ui.ChromaColors.Number)},
		{"NameFunction", dark.CodeBlock.Chroma.NameFunction, string(ui.ChromaColors.Function)},
	} {
		if got := deref(t, "dark Chroma."+tt.name+".Color", tt.got.Color); got != tt.want {
			t.Errorf("dark Chroma.%s.Color = %q, want the palette's %q", tt.name, got, tt.want)
		}
		if tt.got.BackgroundColor != nil {
			t.Errorf("dark Chroma.%s.BackgroundColor = %q — the plate is inherited from Text, not repeated per token",
				tt.name, *tt.got.BackgroundColor)
		}
	}

	// A token the palette does NOT own still aliases the standard value: this
	// is a bounded repaint, not a syntax theme.
	want := deref(t, "stock Chroma.Operator.Color", styles.DarkStyleConfig.CodeBlock.Chroma.Operator.Color)
	if got := deref(t, "dark Chroma.Operator.Color", dark.CodeBlock.Chroma.Operator.Color); got != want {
		t.Errorf("dark Chroma.Operator.Color = %q, want the inherited %q — the repaint is bounded", got, want)
	}

	// Everything outside the named seven is inherited verbatim. Zero the seven
	// and the rest must match the standard config exactly — this is the guard
	// against the repaint growing one token at a time.
	stock := *styles.DarkStyleConfig.CodeBlock.Chroma
	got := *dark.CodeBlock.Chroma
	for _, p := range []*ansi.Chroma{&stock, &got} {
		p.Background, p.Text = ansi.StylePrimitive{}, ansi.StylePrimitive{}
		p.Keyword, p.LiteralString, p.Comment = ansi.StylePrimitive{}, ansi.StylePrimitive{}, ansi.StylePrimitive{}
		p.LiteralNumber, p.NameFunction = ansi.StylePrimitive{}, ansi.StylePrimitive{}
	}
	if !reflect.DeepEqual(got, stock) {
		t.Error("a chroma token outside the named seven was repainted")
	}

	light := keepkitStyle(ui.Default, false, testStyleWidth)
	if light.CodeBlock.Chroma != styles.LightStyleConfig.CodeBlock.Chroma {
		t.Error("light CodeBlock.Chroma was cloned — the light palette deliberately stays stock")
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
	_, _ = keepkitStyle(ui.Default, true, testStyleWidth), keepkitStyle(ui.Default, false, testStyleWidth)
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
		name   string
		raw    string
		want   []string
		absent []string
		// sameLine[0] must render on the same line as sameLine[1] — the only way
		// to pin a table cell's content against glamour's footnote layout.
		sameLine [2]string
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
			// What WithInlineTableLinks is actually for: the preprocessor
			// unwrapped every inline link already, so only an autolink can still
			// reach a cell — and without the option glamour moves it to a
			// numbered footnote under the table instead of leaving it in place.
			name:     "table autolink stays in its cell",
			raw:      "| topic | where |\n|---|---|\n| usage | <https://example.com/m> |\n",
			want:     []string{"https://example.com/m"},
			sameLine: [2]string{"usage", "https://example.com/m"},
		},
		{
			name: "bare url survives",
			raw:  "Read https://example.com/docs first.\n",
			want: []string{"https://example.com/docs"},
		},
		{
			// The title goes with the badges: the card beside this panel already
			// prints the tool's name as the largest thing on the screen.
			name:   "badge header and title vanish, prose survives",
			raw:    "# keepkit\n\n[![Build](https://b.svg)](https://ci.example.com) ![License](https://l.svg)\n\nA tracker for CLI tools.\n",
			want:   []string{"A tracker for CLI tools."},
			absent: []string{"b.svg", "l.svg", "ci.example.com", "Image:", "keepkit"},
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
		// The double-space check belongs on the preprocessed source, where a
		// removal artifact would live — glamour pads table cells with runs of
		// spaces of its own, which is layout, not residue. It is also
		// background-independent, so it runs once rather than per variant.
		t.Run(tt.name+"/no removal residue", func(t *testing.T) {
			for _, line := range strings.Split(cleanReadmeMarkdown(cleanTerminalOutput(tt.raw), ""), "\n") {
				if strings.Contains(strings.TrimSpace(line), "  ") {
					t.Errorf("removal left a double space: %q", line)
				}
			}
		})
		for _, dark := range []bool{true, false} {
			name := tt.name + "/light"
			if dark {
				name = tt.name + "/dark"
			}
			t.Run(name, func(t *testing.T) {
				plain := stripANSI(renderReadme(tt.raw, 100, dark, ui.Default, ""))
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
				if tt.sameLine[0] != "" && !sharesLine(plain, tt.sameLine[0], tt.sameLine[1]) {
					t.Errorf("%q and %q are not on one line\n--- rendered ---\n%s", tt.sameLine[0], tt.sameLine[1], plain)
				}
			})
		}
	}
}

// sharesLine reports whether some line of s carries both needles.
func sharesLine(s, a, b string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, a) && strings.Contains(line, b) {
			return true
		}
	}
	return false
}

// A README that is nothing but badges and HTML cleans down to nothing, and an
// empty render is what makes readmeContent fall through to the friendly
// "No README for <name>. Press [h] for --help." placeholder.
func TestRenderReadmeBadgeOnlyIsEmpty(t *testing.T) {
	raw := "<p align=\"center\">\n  <img src=\"logo.png\">\n</p>\n\n[![Build](https://b.svg)](https://ci.example.com)\n![License](https://l.svg)\n\n<!-- nothing to see -->\n\n[badge]: https://b.svg\n"
	for _, dark := range []bool{true, false} {
		if got := renderReadme(raw, 60, dark, ui.Default, ""); got != "" {
			t.Errorf("dark=%v: renderReadme = %q, want empty", dark, got)
		}
	}
}

// TestChromaFormatterFollowsProfile pins that a code fence is told the same
// thing about the terminal as everything else. glamour's own default is
// "terminal256" for EVERY profile, which quantizes the fence's plate into the
// 256-color cube — Theme.Surface #343945 lands on index 237 (#3a3a3a), a flat
// gray with the blue cast gone — so the one plate rendered in two colors: the
// card's changelog code line and panel [3]'s own inline code emit the exact
// value through lipgloss/termenv, and only the fence between them was off.
//
// The mapping is not a constant "terminal16m", because forcing 24-bit
// sequences onto an ANSI-only terminal is the same bug one layer down.
func TestChromaFormatterFollowsProfile(t *testing.T) {
	for _, tt := range []struct {
		profile termenv.Profile
		name    string
		want    string
	}{
		{termenv.TrueColor, "TrueColor", "terminal16m"},
		{termenv.ANSI256, "ANSI256", "terminal256"},
		{termenv.ANSI, "ANSI", "terminal16"},
		// Ascii never reaches chroma — glamour gates the whole path on
		// ColorProfile != Ascii and falls through to the CodeBlock StyleBlock —
		// so this arm only has to be harmless, and the stock value is that.
		{termenv.Ascii, "Ascii", "terminal256"},
	} {
		if got := chromaFormatterFor(tt.profile); got != tt.want {
			t.Errorf("chromaFormatterFor(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestFencePlateMatchesTheCard is the assertion the mapping exists for, and it
// is deliberately on RENDERED output rather than on the option: passing
// WithChromaFormatter proves nothing about what chroma then emits, and the
// whole defect was a value that survived every struct-level check.
//
// The comparison is against the card's own plate — ui.Styles.Surface, what
// renderChangelogBlock paints a code line with — because "the same plate in two
// panels" is the claim, not "some truecolor background".
func TestFencePlateMatchesTheCard(t *testing.T) {
	forceColorProfile(t) // TrueColor; without it the profile is Ascii and no color is emitted at all

	card := ui.NewStyles(ui.Default).Surface.Render("x")
	want := bgSequence(t, card)

	out := renderReadme("# t\n\n```sh\necho hi\n```\n", 40, true, ui.Default, "")
	var fence string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(stripANSI(line), "echo hi") {
			fence = line
			break
		}
	}
	if fence == "" {
		t.Fatalf("no fence line in:\n%s", out)
	}
	got := bgSequence(t, fence)

	// Both must be 24-bit. This is the half that actually regresses: drop the
	// formatter mapping and the fence comes back as "48;5;237", an INDEXED
	// color, which is the defect stated in its own terms.
	for _, s := range []string{got, want} {
		if !strings.HasPrefix(s, "48;2;") {
			t.Fatalf("background %q is not 24-bit — the plate was quantized", s)
		}
	}

	// Tolerance of one unit per channel, and it is termenv's, not ours:
	// TrueColor.Color("#343945") emits 52;56;69 where the hex says 52;57;69.
	// chroma writes the hex verbatim, so the card is the side that rounds — it
	// has done so for every Surface plate in the app since the role existed.
	// One unit out of 255 is not a disagreement about the color; a jump to a
	// palette index is.
	gr, wr := parseRGB(t, got), parseRGB(t, want)
	for i := range gr {
		if d := gr[i] - wr[i]; d > 1 || d < -1 {
			t.Errorf("fence plate %q vs card plate %q — channel %d differs by %d, the two panels disagree about Theme.Surface",
				got, want, i, d)
		}
	}
}

// bgSequence extracts the background half of the first SGR run that carries
// one. glamour fuses foreground and background into a single sequence
// (\x1b[38;2;…;48;2;…m), so a pattern anchored on \x1b[48; finds nothing — a
// false negative this test was written around once already.
func bgSequence(t *testing.T, s string) string {
	t.Helper()
	i := strings.Index(s, "48;")
	if i < 0 {
		t.Fatalf("no background in %q", s)
	}
	j := strings.Index(s[i:], "m")
	if j < 0 {
		t.Fatalf("unterminated escape in %q", s)
	}
	return s[i : i+j]
}

// parseRGB reads the three channels out of a "48;2;R;G;B" background.
func parseRGB(t *testing.T, seq string) [3]int {
	t.Helper()
	parts := strings.Split(seq, ";")
	if len(parts) != 5 {
		t.Fatalf("not a 24-bit background: %q", seq)
	}
	var out [3]int
	for i := range out {
		v, err := strconv.Atoi(parts[i+2])
		if err != nil {
			t.Fatalf("channel %d of %q: %v", i, seq, err)
		}
		out[i] = v
	}
	return out
}
