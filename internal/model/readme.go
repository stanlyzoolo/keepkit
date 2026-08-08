package model

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/stanlyzoolo/keepkit/internal/ui"
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

// chromaFormatterFor maps lipgloss's resolved color profile onto the chroma
// formatter glamour should hand a code fence, following the profile the same
// way WithColorProfile already does — one decision about the terminal's
// capability, applied to both render paths instead of one.
//
// glamour's own default is "terminal256" for every profile, which quantizes the
// fence's plate into the 256-color cube: Theme.Surface #343945 lands on index
// 237 (#3a3a3a), a flat gray that has lost the blue cast. That put the SAME
// plate on screen in two different colors — the card's changelog code line and
// panel [3]'s inline code both render through lipgloss/termenv at the exact
// value, while only the fence beside them was off. "terminal16m" emits the hex
// verbatim and the three agree.
//
// It is a mapping rather than a constant because forcing truecolor on a
// terminal that cannot take it is the same bug one layer down: an ANSI-only
// session would be sent 24-bit sequences it renders as garbage. Ascii never
// reaches chroma at all (glamour gates the whole path on
// ColorProfile != Ascii), so its arm only has to be harmless.
//
// Pure, so both branches are table-testable — the shellCommand/planFor idiom;
// the thin wrapper is the lipgloss.ColorProfile() call in renderReadme.
func chromaFormatterFor(p termenv.Profile) string {
	switch p {
	case termenv.TrueColor:
		return "terminal16m"
	case termenv.ANSI256:
		return "terminal256"
	case termenv.ANSI:
		return "terminal16"
	case termenv.Ascii:
		return "terminal256"
	default:
		return "terminal256"
	}
}

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
func renderReadme(raw string, width int, dark bool, t ui.Theme, about string) string {
	clean := cleanReadmeMarkdown(cleanTerminalOutput(raw), about)
	if strings.TrimSpace(clean) == "" {
		return ""
	}
	if width < readmeMinWrap {
		width = readmeMinWrap
	}
	// keepkitStyle takes the CLAMPED width, the same number WithWordWrap gets
	// below: it sizes the full-width divider, and a divider measured against
	// the caller's raw request would overrun the panel on a narrow terminal.
	style := glamour.WithStyles(keepkitStyle(t, dark, width))
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
		// A code fence renders through chroma, which has its own formatter and
		// glamour's own default for it ("terminal256") ignores the profile
		// above — so the fence quantized its plate while the inline code beside
		// it did not. One capability answer, both paths.
		glamour.WithChromaFormatter(chromaFormatterFor(lipgloss.ColorProfile())),
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
	// theme is part of the key because it is part of the render: a theme
	// switch has to invalidate the entry, or the panel would keep serving the
	// previous palette's ANSI until the tool changed. about likewise decides
	// what the preprocessor drops, and it arrives asynchronously — a card that
	// lands after the first render must re-run the pass.
	theme ui.Theme
	about string
	out   string
	ok    bool
}

// render returns the rendered README for name, reusing the cached result when
// every key component matches.
func (c *readmeRenderCache) render(name, raw string, width int, dark bool, t ui.Theme, about string) string {
	if c.ok && c.name == name && c.width == width && c.dark == dark && c.theme == t && c.about == about && c.raw == raw {
		return c.out
	}
	out := renderReadme(raw, width, dark, t, about)
	*c = readmeRenderCache{name: name, raw: raw, width: width, dark: dark, theme: t, about: about, out: out, ok: true}
	return out
}
