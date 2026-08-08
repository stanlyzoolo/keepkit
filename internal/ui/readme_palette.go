package ui

import "github.com/charmbracelet/lipgloss"

// This file is keepkit's SECOND sanctioned non-role palette, and it is an
// exception rather than an application of the first one.
//
// languageColors (lang.go) is not a role because the colors are somebody
// else's: linguist publishes them, and a cyan dot beside "go" is only worth
// anything if it is GitHub's cyan. Nothing here has that excuse. These are
// keepkit-chosen shades, invented for one panel, and the rule they break —
// "color is a role, not a shade" (theme.go) — is broken knowingly: panel [3]
// renders somebody's *document*, and a document needs a heading ladder and
// syntax accents, which are typographic distinctions with no meaning in
// Theme's vocabulary. Six heading levels cannot be six meanings. Rather than
// grow Theme by five roles that mean "one step further down a heading ladder",
// the ladder lives here, contained to the README pipeline, and Theme keeps
// naming meanings only.
//
// The consequence, stated plainly because it is the price: a future theme
// switch repaints panel [3]'s body, links, quotes and inline code and does NOT
// repaint H1-H5 or the code-fence syntax accents. Those follow this file.
//
// "Easy to change" therefore means: edit the values below and `go build`.
// There is deliberately no config file and no live reload — the chroma style is
// registered once per process under a global name (see keepkitStyle), so a
// mid-session palette change could not repaint a fence anyway.
//
// The two blocks share one temperature (teal running into olive) so a code
// fence sitting under a heading reads as the same document rather than as two
// palettes meeting.

// HeadingColors is panel [3]'s heading ladder, H1 through H5, index 0 = H1.
// It exists because the stock config renders H3 through H6 identically, so a
// README's own structure was invisible below the second level. H6 is not here —
// it takes the Theme.Dim role, since by the sixth level the honest statement is
// "this is a heading and it is the quietest one".
//
// THE ORDER IS BRIGHT TO DARK, and that direction is the decision, not the
// palette. keepkit is a dark-terminal app, and measured against a typical dark
// background these five run 7.2 : 5.4 : 4.1 : 3.6 : 2.4 in contrast — so the
// dark-to-bright reading puts the LEAST contrast on H1, below the 3:1 floor
// for bold text and below the Dim role a caption uses. A document's top
// heading being the hardest line on the page to read is not a ladder, it is
// an inverted one. Loud at the top, quiet at the bottom.
//
// Accepted consequences of choosing for the dark terminal: on white the ends
// swap, so H1 measures ~2.4 there and the lowest steps read best — and on
// dark, H5 (2.4) sits below H6's Dim (4.2), which makes the ladder's last two
// steps read as one. Both are the price of the ordering, and both were
// weighed against a top-level heading nobody can find.
//
// Both background variants share the ladder: it is content typography, not a
// tint, and the light variant of keepkitStyle takes these colors while keeping
// glamour's stock light palette for everything else.
var HeadingColors = [5]lipgloss.Color{
	lipgloss.Color("#a2ad59"), // H1 — the loudest step
	lipgloss.Color("#8e936d"), // H2
	lipgloss.Color("#598381"), // H3
	lipgloss.Color("#177e89"), // H4
	lipgloss.Color("#08605f"), // H5 — the quietest
}

// ChromaPalette names the code-fence syntax accents keepkit owns. Every other
// chroma token stays whatever the standard config chose — this is a bounded
// repaint, not a full syntax theme (see keepkitStyle's comment for what the
// boundary is and why).
//
// The five are the tokens a README's fences actually spend: a shell block is
// mostly Keyword/LiteralString, a Go block adds NameFunction and Comment.
type ChromaPalette struct {
	// Keyword is `func`, `if`, `export` — the structure of the sample.
	Keyword lipgloss.Color
	// String is a quoted literal, the loudest thing in a shell fence.
	String lipgloss.Color
	// Comment sits below body brightness on purpose: it is the one part of a
	// sample the reader may skip.
	Comment lipgloss.Color
	// Number is a literal count or version.
	Number lipgloss.Color
	// Function is a called or declared name.
	Function lipgloss.Color
}

// ChromaColors is the code-fence palette, in the heading ladder's temperature
// so a fence reads as part of the same document. The values are picked against
// Theme.Surface (the plate a fence renders on), not against the terminal
// background — Comment is the quietest of the five and still clears the plate.
//
// Dark backgrounds only. On white, glamour's stock light chroma stays: these
// are chosen for a dark plate and three of them would be unreadable on paper.
var ChromaColors = ChromaPalette{
	Keyword:  lipgloss.Color("#4FA8A5"),
	String:   lipgloss.Color("#A2AD59"),
	Comment:  lipgloss.Color("#7C8577"),
	Number:   lipgloss.Color("#C4B77A"),
	Function: lipgloss.Color("#7FC4BE"),
}
