package model

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// readmeMinWrap is the floor for the glamour word-wrap width; it mirrors the
// floor helpWrapWidth() applies, so a very narrow panel produces wrapped text
// instead of a per-character column.
const readmeMinWrap = 20

// testReadmeStyle routes the renderer through a named standard style instead of
// keepkitStyle (the testConfigDir/testCacheDir seam idiom). An unknown name
// makes glamour.NewTermRenderer fail, which is how the fallback path is
// exercised.
var testReadmeStyle string

// renderReadme turns raw README markdown into the ANSI text panel [3] shows,
// in two layers. First the text is sanitized with cleanTerminalOutput — a
// README is remote content that lands in a viewport verbatim, so it gets the
// same treatment as a probe capture — and then preprocessed by
// cleanReadmeMarkdown, which strips the badges, hrefs, HTML and emoji a TTY
// cannot show or does not need. What survives is rendered with keepkitStyle,
// the palette-matched theme.
//
// The style is resolved from the dark/light answer the caller already has, and
// deliberately NOT by glamour.WithAutoStyle(): auto-style probes the live
// terminal with a termenv OSC background query that reads stdin, which races
// Bubble Tea's input reader and breaks the project's terminal-sandboxing
// policy. Any glamour failure falls back to the preprocessed plain text: an
// unstyled README beats an empty panel.
func renderReadme(raw string, width int, dark bool) string {
	clean := cleanReadmeMarkdown(cleanTerminalOutput(raw))
	if strings.TrimSpace(clean) == "" {
		return ""
	}
	if width < readmeMinWrap {
		width = readmeMinWrap
	}
	style := glamour.WithStyles(keepkitStyle(dark))
	if testReadmeStyle != "" {
		style = glamour.WithStandardStyle(testReadmeStyle)
	}
	r, err := glamour.NewTermRenderer(
		style,
		glamour.WithWordWrap(width),
		// glamour defaults to TrueColor regardless of the environment; follow
		// lipgloss instead so a degraded profile (NO_COLOR, dumb term) yields
		// plain text like every other panel.
		glamour.WithColorProfile(lipgloss.ColorProfile()),
		// A table link renders in its cell rather than as a numbered footnote
		// under the table. The preprocessor already unwrapped the inline form;
		// this covers an autolink that survived into a cell.
		glamour.WithInlineTableLinks(true),
	)
	if err != nil {
		return clean
	}
	out, err := r.Render(clean)
	if err != nil {
		return clean
	}
	return strings.TrimRight(out, "\n")
}

// readmeRenderCache memoizes the last rendered README. setHelpContent() runs on
// every selection move and every resize, and re-parsing a large README through
// glamour is far heavier than colorizeHelp, so one entry keyed by
// (name, width, dark) is enough — moving away and back re-renders once. The raw
// text is compared too so a refetch (force refresh, late readmeMsg) for the
// same tool is never served from a stale entry.
type readmeRenderCache struct {
	name  string
	raw   string
	width int
	dark  bool
	out   string
	ok    bool
}

// render returns the rendered README for name, reusing the cached result when
// every key component matches.
func (c *readmeRenderCache) render(name, raw string, width int, dark bool) string {
	if c.ok && c.name == name && c.width == width && c.dark == dark && c.raw == raw {
		return c.out
	}
	out := renderReadme(raw, width, dark)
	*c = readmeRenderCache{name: name, raw: raw, width: width, dark: dark, out: out, ok: true}
	return out
}
