package model

import (
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"

	"github.com/stanlyzoolo/keepkit/internal/ui"
)

// ptrTo returns a pointer to v. glamour's StyleConfig is pointer-heavy, the
// package globals keepkitStyle clones share those pointers, and
// styles.DefaultStyles aliases the same structs — so every override must assign
// a FRESH pointer. Writing through a cloned one (*cfg.H1.Color = …) would
// restyle glamour for the whole process.
func ptrTo[T any](v T) *T { return &v }

// keepkitStyle is panel [3]'s glamour theme: the standard config for the
// terminal's background, with the accents moved onto the theme's roles so a
// README reads as part of the app instead of pasted in from another one. It
// takes the Theme rather than reading colors from a package — that is what puts
// the readme panel under the same one-value theme switch as everything else
// (the panel is re-rendered on the existing switchHelpMode path).
//
// It CLONES rather than builds from scratch. StyleConfig has dozens of fields —
// chroma tokens, table separators, list indents — and inheriting them means a
// glamour upgrade that adds one cannot leave the panel with a hole in it.
//
// The light variant takes only the heading accents and the structural fixes:
// the rest of the palette is chosen against a dark panel and would be
// unreadable on white, so there the standard light colors stay.
//
// One part of the redesign is deliberately missing: a section heading is meant
// to carry a border-colored rule out to the panel edge. StyleConfig cannot
// express that — Prefix/Suffix are inline and HorizontalRule is a fixed string,
// so the only way to fake it is to inject a `---` the author never wrote into
// their README. Weight and Emphasis carry the heading on their own instead.
func keepkitStyle(t ui.Theme, dark bool) ansi.StyleConfig {
	cfg := styles.LightStyleConfig
	if dark {
		cfg = styles.DarkStyleConfig
	}

	// glamour's own margin eats 2-4 columns of an already narrow panel, and the
	// panel frame is the breathing room.
	cfg.Document.Margin = ptrTo[uint](0)

	// The standard H1 is bright text on a colored plate — the loudest thing on
	// the screen and the main reason a rendered README looked tasteless here.
	// Accent and bold carries the same weight; the padding spaces go with the
	// plate, since without a background they only read as a stray indent. (The
	// preprocessor drops a README's leading H1 outright — it repeats the card's
	// tool name — so this styles the ones further down.)
	cfg.H1.Color = ptrTo(string(t.Accent))
	cfg.H1.BackgroundColor = nil
	cfg.H1.Bold = ptrTo(true)
	cfg.H1.Prefix = ""
	cfg.H1.Suffix = ""

	// H2 is the panel's section heading — the brightest text role, matching the
	// card's own. The "## " prefix goes with it: the standard style reprints the
	// markdown marker in front of every heading, which is source, not text, and
	// weight already says the line is a heading.
	cfg.H2.Color = ptrTo(string(t.Emphasis))
	cfg.H2.Bold = ptrTo(true)
	cfg.H2.Prefix = ""
	for _, h := range []*ansi.StyleBlock{&cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
		h.Prefix = ""
	}

	if !dark {
		return cfg
	}

	// Document is the base every block cascades onto, so this is the panel's
	// body-text rule: the same Text role the card's changelog notes render in.
	// The standard dark config picks its own gray, and the two sitting side by
	// side at slightly different brightnesses was the last thing that still read
	// as "this panel came from another app".
	cfg.Document.Color = ptrTo(string(t.Text))

	// Heading is the base every Hn cascades onto, so this is the H3-H5 rule;
	// H6 needs its own, because the standard dark config singles it out with a
	// green non-bold override.
	cfg.Heading.Color = ptrTo(string(t.Text))
	cfg.Heading.Bold = ptrTo(true)
	cfg.H6.Color = ptrTo(string(t.Text))
	cfg.H6.Bold = ptrTo(true)

	// The bold lead of a feature line ("Check agent visibility — …") is the
	// readme's own emphasis peak, matching the card's.
	cfg.Strong.Color = ptrTo(string(t.Emphasis))

	// Inline code is where a README names a file, a flag or a command. It used to
	// be Danger with no plate, and that was wrong twice: red is the card's one
	// alarm color and a README spends it a dozen times a screen, and the dark red
	// was the *least* legible thing in the panel rather than the most.
	//
	// It now reads exactly like a code span in the card's changelog — Text on
	// the Surface plate — which is the same rule stated once in two panels: a
	// literal the reader could type is raised off the prose, not colored against
	// it. The plate alone does the raising; the text stays at body brightness,
	// because a spell in Emphasis made every code span the loudest thing in the
	// panel (a README spends dozens per screen, and Emphasis is meant to peak a
	// few times per frame). The prefix/suffix spaces the standard style already
	// puts around inline code become the plate's padding for free.
	cfg.Code.Color = ptrTo(string(t.Text))
	cfg.Code.BackgroundColor = ptrTo(string(t.Surface))

	// A code block is an install command the user is about to run: the one
	// place the readme gets the Surface background, so it reads as a plate.
	//
	// Two render paths, and BOTH are live. glamour sends a fence through
	// rules.Chroma whenever ColorProfile != termenv.Ascii — every real session —
	// and falls back to these StyleBlock fields for Ascii/NO_COLOR terminals,
	// which is also what this package's TTY-less tests exercise. So the
	// StyleBlock override below stays, and the Chroma repaint above it is what
	// the user actually sees.
	cfg.CodeBlock.Color = ptrTo(string(t.Text))
	cfg.CodeBlock.BackgroundColor = ptrTo(string(t.Surface))

	// The repaint is deliberately bounded to two entries: Background carries the
	// plate, and Text needs the same background because chroma's formatter emits
	// one per token — without it the plate is punched through wherever a token
	// falls back to Text. Every other token entry is inherited unchanged, so the
	// syntax accents stay the ones the standard config chose and no per-token
	// judgement call is made here.
	//
	// The clone is a fresh pointer for the reason ptrTo exists: the globals are
	// aliased by styles.DefaultStyles, and writing through cfg.CodeBlock.Chroma
	// would restyle glamour process-wide.
	//
	// Caveat worth knowing: glamour registers the built chroma style under the
	// process-global, one-shot name "charm" and skips the registration when that
	// name is already taken, so the FIRST render in a process wins the slot. That
	// is why the only reliable assertion is struct-level, and why a mid-session
	// theme switch could not repaint a fence anyway — consistent with m.darkBG,
	// which is resolved once at construction.
	if cfg.CodeBlock.Chroma != nil {
		chromaCfg := *cfg.CodeBlock.Chroma
		chromaCfg.Background.BackgroundColor = ptrTo(string(t.Surface))
		chromaCfg.Text.Color = ptrTo(string(t.Text))
		chromaCfg.Text.BackgroundColor = ptrTo(string(t.Surface))
		cfg.CodeBlock.Chroma = &chromaCfg
	}

	// What is still a link after the preprocessor is an autolink or a bare URL,
	// whose text IS the URL — the Link role's only consumer here.
	cfg.LinkText.Color = ptrTo(string(t.Link))

	// Rules and quote bars in the panel-frame grays, so they read as structure
	// rather than as content.
	cfg.HorizontalRule.Color = ptrTo(string(t.Border))
	cfg.BlockQuote.Color = ptrTo(string(t.Dim))

	return cfg
}
