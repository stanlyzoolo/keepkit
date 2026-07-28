package model

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

const sampleReadme = "# Title\n\nSome intro text.\n\n## Usage\n\n- first item\n- second item\n\n```sh\nkeepkit --version\n```\n"

// visibleLines strips ANSI from rendered output so assertions look at the text
// a user sees, not at glamour's escape sequences (which vary by color profile).
func visibleLines(s string) []string {
	return strings.Split(stripANSI(s), "\n")
}

func TestRenderReadmeContent(t *testing.T) {
	out := renderReadme(sampleReadme, 60, true)
	if out == "" {
		t.Fatal("renderReadme returned empty output")
	}
	plain := stripANSI(out)
	for _, want := range []string{"Title", "Some intro text.", "Usage", "first item", "second item", "keepkit --version"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered README missing %q\n---\n%s", want, plain)
		}
	}
}

func TestRenderReadmeEmpty(t *testing.T) {
	for _, in := range []string{"", "   \n\t\n"} {
		if got := renderReadme(in, 60, true); got != "" {
			t.Errorf("renderReadme(%q) = %q, want empty", in, got)
		}
	}
}

func TestRenderReadmeWrapRespectsWidth(t *testing.T) {
	long := "# T\n\n" + strings.Repeat("word ", 200)
	const width = 40
	out := renderReadme(long, width, true)
	for _, line := range visibleLines(out) {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("line wider than %d (%d): %q", width, w, line)
		}
	}
}

// A width below the floor must still wrap (and never divide by zero or produce
// a one-character column).
func TestRenderReadmeNarrowWidthUsesFloor(t *testing.T) {
	long := strings.Repeat("word ", 100)
	out := renderReadme(long, 1, true)
	if strings.TrimSpace(stripANSI(out)) == "" {
		t.Fatal("narrow render produced no text")
	}
	for _, line := range visibleLines(out) {
		if w := lipgloss.Width(line); w > readmeMinWrap {
			t.Fatalf("line wider than the %d floor (%d): %q", readmeMinWrap, w, line)
		}
	}
}

func TestRenderReadmeStripsControlChars(t *testing.T) {
	raw := "# Ti\x07tle\n\nbo\x1b[31mdy\x1b[0m text\rmore\n"
	plain := stripANSI(renderReadme(raw, 60, true))
	if strings.ContainsAny(plain, "\x07\x1b\r") {
		t.Errorf("control characters survived rendering: %q", plain)
	}
	if !strings.Contains(plain, "Title") {
		t.Errorf("expected sanitized heading %q in %q", "Title", plain)
	}
}

// An unrenderable style makes glamour.NewTermRenderer fail; the panel must get
// the preprocessed plain text rather than nothing.
func TestRenderReadmeFallsBackToPlainText(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"sanitizer only", "# Title\n\nbody\x07 text\n"},
		// The fallback must carry the preprocessing too, or a failed render
		// would be the one path that still shows badge and href noise.
		{"preprocessed", "# Title\n\n![Build](https://b.svg)\n\nSee the [docs](https://example.com) 🚀\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testReadmeStyle = "no-such-style"
			t.Cleanup(func() { testReadmeStyle = "" })

			got := renderReadme(tt.raw, 60, true)
			want := cleanReadmeMarkdown(cleanTerminalOutput(tt.raw))
			if got != want {
				t.Errorf("fallback = %q, want the preprocessed input %q", got, want)
			}
			if got == tt.raw {
				t.Errorf("fallback returned the raw input unchanged: %q", got)
			}
		})
	}
}

// The background is probed once at construction, never per render — glamour's
// own auto-style would query the terminal over stdin mid-session.
func TestNewResolvesBackgroundOnce(t *testing.T) {
	m := New(nil)
	if m.darkBG != lipgloss.HasDarkBackground() {
		t.Errorf("New().darkBG = %v, want %v", m.darkBG, lipgloss.HasDarkBackground())
	}
}

// A repeated call with the same key must not re-render — proven by breaking the
// renderer between the two calls: a cache miss would fall back to plain text.
func TestReadmeRenderCacheHit(t *testing.T) {
	var c readmeRenderCache
	first := c.render("tool", sampleReadme, 60, true)

	testReadmeStyle = "no-such-style"
	t.Cleanup(func() { testReadmeStyle = "" })

	if second := c.render("tool", sampleReadme, 60, true); second != first {
		t.Errorf("cached render = %q, want the first result %q", second, first)
	}
}

func TestReadmeRenderCacheInvalidation(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		raw   string
		width int
		dark  bool
	}{
		{"different tool", "other", sampleReadme, 60, true},
		{"different width", "tool", sampleReadme, 40, true},
		{"different background", "tool", sampleReadme, 60, false},
		{"refetched content", "tool", "# Other\n\nnew body\n", 60, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c readmeRenderCache
			first := c.render("tool", sampleReadme, 60, true)

			testReadmeStyle = "no-such-style"
			t.Cleanup(func() { testReadmeStyle = "" })

			got := c.render(tt.tool, tt.raw, tt.width, tt.dark)
			if got == first {
				t.Fatalf("%s: cache was reused, want a re-render", tt.name)
			}
			if want := cleanReadmeMarkdown(cleanTerminalOutput(tt.raw)); got != want {
				t.Errorf("%s: re-render = %q, want the fallback %q", tt.name, got, want)
			}
		})
	}
}
