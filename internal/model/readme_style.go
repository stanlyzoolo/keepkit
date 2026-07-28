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
// terminal's background, with the accents moved onto keepkit's palette so a
// README reads as part of the app instead of pasted in from another one.
//
// It CLONES rather than builds from scratch. StyleConfig has dozens of fields —
// chroma tokens, table separators, list indents — and inheriting them means a
// glamour upgrade that adds one cannot leave the panel with a hole in it.
//
// The light variant takes only the peach heading accents and the structural
// fixes: the rest of the palette (#E8E8E8 text, #555555 rules) is chosen
// against a dark panel and would be unreadable on white, so there the standard
// light colors stay.
func keepkitStyle(dark bool) ansi.StyleConfig {
	cfg := styles.LightStyleConfig
	if dark {
		cfg = styles.DarkStyleConfig
	}

	// glamour's own margin eats 2-4 columns of an already narrow panel, and the
	// panel frame is the breathing room.
	cfg.Document.Margin = ptrTo[uint](0)

	// The standard H1 is bright text on a colored plate — the loudest thing on
	// the screen and the main reason a rendered README looked tasteless here.
	// Peach and bold carries the same weight; the padding spaces go with the
	// plate, since without a background they only read as a stray indent.
	cfg.H1.Color = ptrTo(string(ui.ColorPrimary))
	cfg.H1.BackgroundColor = nil
	cfg.H1.Bold = ptrTo(true)
	cfg.H1.Prefix = ""
	cfg.H1.Suffix = ""

	// H2 takes the same color the brief card heads its sections with.
	cfg.H2.Color = ptrTo(string(ui.ColorCategory))
	cfg.H2.Bold = ptrTo(true)

	if !dark {
		return cfg
	}

	// Heading is the base every Hn cascades onto, so this is the H3-H5 rule;
	// H6 needs its own, because the standard dark config singles it out with a
	// green non-bold override.
	cfg.Heading.Color = ptrTo(string(ui.ColorText))
	cfg.Heading.Bold = ptrTo(true)
	cfg.H6.Color = ptrTo(string(ui.ColorText))
	cfg.H6.Bold = ptrTo(true)

	// What is still a link after the preprocessor is an autolink or a bare URL,
	// whose text IS the URL — the same blue the help panel gives its meta
	// tokens.
	cfg.LinkText.Color = ptrTo(string(ui.ColorMeta))

	// Rules and quote bars in the panel-frame grays, so they read as structure
	// rather than as content.
	cfg.HorizontalRule.Color = ptrTo(string(ui.ColorBorder))
	cfg.BlockQuote.Color = ptrTo(string(ui.ColorDim))

	return cfg
}
