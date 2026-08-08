package model

import (
	"strings"

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

// headingPrefixes are the depth markers panel [3] puts in front of H3-H6. H1
// and H2 get none: a document's top two levels are found by their color and
// their weight, and a marker on them would only be noise on the lines that
// need it least.
//
// From H3 down, color has run out of room — the ladder's lower steps sit close
// together by construction — so depth is also spelled, one › per level below
// H2. The markers are NOT dim: Prefix renders with the heading's own style,
// which glamour gives no way around, so each marker carries its level's color
// and states the depth twice. Accepted: saying it twice is the failure mode
// worth having.
//
// › is U+203A, East-Asian Ambiguous like ⏺/↑/•/─ — the accepted class. It
// travels through glamour's own wrap, not wrapText, and an over-wide
// measurement there can only wrap a heading early.
var headingPrefixes = [4]string{"› ", "›› ", "››› ", "›››› "} // H3, H4, H5, H6

// keepkitStyle is panel [3]'s glamour theme: the standard config for the
// terminal's background, re-accented so a README reads as part of the app
// instead of pasted in from another one.
//
// It CLONES rather than builds from scratch. StyleConfig has dozens of fields —
// chroma tokens, table separators, list indents — and inheriting them means a
// glamour upgrade that adds one cannot leave the panel with a hole in it.
//
// Two palettes meet here, and the split is deliberate. The panel's *chrome* —
// body text, links, quotes, inline code, the rules — comes from the Theme it is
// handed, so it stays under the same one-value theme switch as the rest of the
// app. The panel's *typography* — the H1-H5 heading ladder and (in Task 3) the
// code-fence syntax accents — comes from ui.HeadingColors/ui.ChromaColors,
// which are fixed shades and do not follow a Theme change. A heading level is
// not a meaning Theme has a word for; see internal/ui/readme_palette.go for the
// whole argument and for what a theme switch will and will not repaint.
//
// The light variant takes the heading ladder and the structural fixes: the
// rest of the palette is chosen against a dark panel and would be unreadable on
// white, so there the standard light colors stay.
//
// One part of the redesign is deliberately missing: a section heading is meant
// to carry a border-colored rule out to the panel edge. HorizontalRule now IS
// a full-width rule (see the Format override below), but it is the wrong one —
// it fires on a `---` the author wrote, and the only way to put one under a
// heading is to inject a `---` they never wrote into their README. Editing
// somebody's document to decorate it is where this stops. Weight and the
// ladder carry the heading on their own instead.
func keepkitStyle(t ui.Theme, dark bool, width int) ansi.StyleConfig {
	cfg := styles.LightStyleConfig
	if dark {
		cfg = styles.DarkStyleConfig
	}

	// glamour's own margin eats 2-4 columns of an already narrow panel, and the
	// panel frame is the breathing room.
	cfg.Document.Margin = ptrTo[uint](0)

	// The heading ladder, H1 through H5, one color per level. The stock config
	// renders H3 through H6 identically, so a README's own structure was
	// invisible below the second level; this is the fix, and it is why the
	// colors are shades rather than roles.
	//
	// Bold is set per level rather than left to the Heading base: the stock H6
	// carries an explicit Bold:false that beats inheritance, and a rule that
	// only holds for five of six levels is not a rule.
	for i, h := range []*ansi.StyleBlock{&cfg.H1, &cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5} {
		h.Color = ptrTo(string(ui.HeadingColors[i]))
		h.Bold = ptrTo(true)
	}

	// The standard H1 is bright text on a colored plate — the loudest thing on
	// the screen and the main reason a rendered README looked tasteless here.
	// The color and the weight carry it; the padding spaces go with the plate,
	// since without a background they only read as a stray indent. (The
	// preprocessor drops a README's leading H1 outright — it repeats the card's
	// tool name — so this styles the ones further down.)
	cfg.H1.BackgroundColor = nil
	cfg.H1.Suffix = ""

	// The prefixes are assigned, never left alone: glamour's cascade overrides
	// a child's Prefix only when the child's is NON-EMPTY, so dropping these
	// two lines does not fall back to the Heading base — it brings the stock
	// "# "/"## " markers back, which are source rather than text.
	cfg.H1.Prefix = ""
	cfg.H2.Prefix = ""
	for i, h := range []*ansi.StyleBlock{&cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
		h.Prefix = headingPrefixes[i]
	}

	// A task list is a README's own checklist — a roadmap, a support matrix —
	// and the stock markers reprint the source's [ ]/[x] brackets, which is the
	// same "source, not text" problem the heading prefixes had. ☐/✓ say the two
	// states as one glyph each. Both are decorative (they carry no width
	// arithmetic), and ☐ is U+2610, in the accepted Ambiguous class.
	cfg.Task.Ticked = "✓ "
	cfg.Task.Unticked = "☐ "

	// Two things this file deliberately does NOT style, recorded so the next
	// reader does not spend the afternoon finding out why:
	//
	//   - LIST MARKERS. Item.Color and Enumeration.Color exist and are no-ops:
	//     ansi.BaseElement.doRender paints the "• " BlockPrefix and the
	//     enumerator with the ENCLOSING block's style, so a red Item.Color
	//     still produces document-gray bullets. Confirmed by render. There is
	//     no StyleConfig field that reaches them.
	//   - TABLES. The separators are drawn by lipgloss/table from glyph strings
	//     with no color call at all, and the Table StylePrimitive styles CELL
	//     CONTENT — header and body alike — so neither a border tint nor a
	//     header-only weight is expressible. Confirmed by reading the renderer.
	//
	// Both would need a glamour change, not a config one.

	// A `---` in a README is the author dividing their document, and the stock
	// format answers it with a short dash run floating mid-panel, which reads
	// as a typo rather than as a division. It becomes the panel's one full-width
	// rule instead — the same ─ the frame is drawn with, so a divider and a
	// border are visibly the same kind of thing.
	//
	// The width is keepkitStyle's only use of the parameter, and it is the
	// CLAMPED wrap width renderReadme passes, the same number glamour wraps to.
	// width <= 0 keeps the stock format: a caller that has no layout yet (a
	// hand-built model, a test) must get something rather than an empty string.
	//
	// Accepted caveat: a `---` nested under a blockquote or a list item is
	// indented by the enclosing block, so the run is then width−indent cells
	// wide and, having no breakpoints, OVERFLOWS rather than wraps. Rare enough
	// to accept — the stock short run could never hit it, so this is a new
	// failure mode, but it costs one visually long line in a document shape
	// almost nobody writes.
	if width > 0 {
		cfg.HorizontalRule.Format = "\n" + strings.Repeat("─", width) + "\n"
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

	// H6 is the ladder's floor and the one level that stays a Theme role: by
	// the sixth heading the honest statement is "this is a heading and it is
	// the quietest one", which is exactly what Dim means. It needs its own Bold
	// for the reason stated above — the stock dark H6 is an explicit
	// Bold:false, green, non-bold override that beats the Heading base.
	//
	// Dark only. On white the stock H6 stays: Dim is picked against a dark
	// panel, and the depth marker assigned in the shared section is enough to
	// place the level there.
	cfg.H6.Color = ptrTo(string(t.Dim))
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

	// The repaint is bounded to a NAMED SEVEN, and the boundary is the point:
	// two entries carry the plate, five carry the syntax accents keepkit owns,
	// and every other token is inherited from the standard config, so a glamour
	// upgrade that retunes the rest arrives for free.
	//
	// The plate is Background + Text, and Text is the load-bearing one —
	// verified by render, not by reading the struct. chroma's formatter emits a
	// background per token and resolves an unset one up the token hierarchy to
	// Text (its root), NOT to Background: with Background set and Text's
	// background unset, the fence comes out with no plate at all. The converse
	// is why the five accents below set Color only — their background is
	// inherited from Text, and repeating it would be five more places for the
	// plate to drift.
	//
	// The five are the tokens a README's fences actually spend (a shell block
	// is mostly Keyword/LiteralString, a Go block adds NameFunction and
	// Comment), and they come from ui.ChromaColors rather than from the Theme:
	// "the color of a string literal" is not a meaning Theme has a word for.
	// Dark only, like the plate — the stock light chroma is chosen for paper.
	//
	// Accepted caveat: chroma's terminal formatter downsamples to the 256-color
	// cube regardless of the profile glamour was given, so what lands is each
	// value's nearest slot, not the hex. The five were checked to land on five
	// distinct slots; two near-neighbours would silently collapse into one.
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
		chromaCfg.Keyword.Color = ptrTo(string(ui.ChromaColors.Keyword))
		chromaCfg.LiteralString.Color = ptrTo(string(ui.ChromaColors.String))
		chromaCfg.Comment.Color = ptrTo(string(ui.ChromaColors.Comment))
		chromaCfg.LiteralNumber.Color = ptrTo(string(ui.ChromaColors.Number))
		chromaCfg.NameFunction.Color = ptrTo(string(ui.ChromaColors.Function))
		cfg.CodeBlock.Chroma = &chromaCfg
	}

	// What is still a link after the preprocessor is an autolink or a bare URL,
	// whose text IS the URL — the Link role's only consumer here.
	cfg.LinkText.Color = ptrTo(string(t.Link))

	// Rules and quote bars in the panel-frame grays, so they read as structure
	// rather than as content.
	cfg.HorizontalRule.Color = ptrTo(string(t.Border))

	// A blockquote is somebody else's voice inside the document — a warning
	// box, a quoted issue, an upstream note. Dim already steps it back; italic
	// is what says it is quoted rather than merely less important, and it is
	// the one emphasis axis the panel has left unspent. Confirmed by render.
	cfg.BlockQuote.Color = ptrTo(string(t.Dim))
	cfg.BlockQuote.Italic = ptrTo(true)

	return cfg
}
