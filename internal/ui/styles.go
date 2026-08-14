package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/stanlyzoolo/keepkit/internal/loader"
)

// Styles is every lipgloss.Style keepkit renders with, built from one Theme.
// It is a value, not a package of vars: the model holds one (see model.New), so
// re-theming the whole app is handing the model a different Styles rather than
// mutating globals under a running render.
//
// The set is deliberately small and named after Theme's roles rather than after
// the widgets that use it. A per-widget name (the old TagHeaderStyle,
// HelpSectionStyle, SectionLabelStyle, …) reads well exactly once — at the call
// site it was invented for — and then quietly multiplies into a palette of
// near-duplicates that a theme has to keep consistent by hand. Emphasis steps
// that used to justify a new color are covered here by weight (Bold), italics
// and the Surface background, so what a call site picks is a *meaning*, and the
// comment above it says which.
type Styles struct {
	// Theme is the palette these styles were built from. Renderers that need a
	// raw color rather than a style — a border repaint, glamour's StyleConfig,
	// a background derived per segment — read it from here, so they still go
	// through the theme instead of reaching for a package-level color.
	Theme Theme

	// Accent: focus and interaction. AccentBold additionally marks the one
	// element that *has* focus — the selected tool's name, a search hit.
	Accent     lipgloss.Style
	AccentBold lipgloss.Style

	// Signal: requires action. Bold is the card's headline version, plain the
	// repeated markers in the list.
	Signal     lipgloss.Style
	SignalBold lipgloss.Style

	// Ok: healthy state. Danger: broken or exhausted — always bold, it is an
	// alarm and there is never more than one on screen.
	Ok     lipgloss.Style
	Danger lipgloss.Style

	// Link: URLs only.
	Link lipgloss.Style

	// Emphasis: the brightest text. EmphasisBold heads a section or names the
	// selected tool in the card — the screen's few typographic peaks.
	Emphasis     lipgloss.Style
	EmphasisBold lipgloss.Style

	// Text: ordinary reading text. Dim: labels, counters, SHAs, secondary
	// hints. Note: the same step down, italic — placeholders and prose that is
	// not the app's own voice ("no tools tracked", a repo's tagline).
	Text lipgloss.Style
	Dim  lipgloss.Style
	Note lipgloss.Style

	// Rule draws dividers, group headers and the vertical lines between metric
	// columns. Surface is a background *only* — a call site pairs it with a
	// foreground style rather than using it alone, because a lipgloss style
	// applied around already-styled text cannot repaint the segments inside it.
	Rule    lipgloss.Style
	Surface lipgloss.Style

	// Panel frames and the status bar. The focused panel differs by color and
	// by the ▸ its title carries — two signals, so focus survives a colorless
	// terminal.
	PanelBorder        lipgloss.Style
	PanelBorderFocused lipgloss.Style
	StatusBar          lipgloss.Style

	// Overlays: the modal frame, and the repaint applied to everything behind
	// it so the modal is the only full-color element on screen.
	OverlayBorder lipgloss.Style
	OverlayDim    lipgloss.Style

	// TermCursor marks where the tool running in the [enter] overlay has its
	// cursor. It is the one style here that names no theme color, and that is
	// the point: reverse video swaps whatever the tool already painted into
	// that cell, so the block stays visible against the tool's own palette
	// instead of keepkit's — which is the only way one style can mark a cursor
	// on a screen keepkit does not control.
	TermCursor lipgloss.Style

	// The API-usage gauge. GaugeTrack is Theme.SignalDim's only consumer: fill
	// and track have to read as one bar, which no other pair of roles can do.
	GaugeFill  lipgloss.Style
	GaugeTrack lipgloss.Style
}

// NewStyles builds the style set for a theme. It is the only function in the
// codebase allowed to turn a color into a style — every field below is derived
// from t, never from a literal.
//
// It returns a pointer: Styles is ~20 lipgloss.Style values (a few kilobytes),
// the renderers are value receivers, and a frame reads it dozens of times.
func NewStyles(t Theme) *Styles {
	fg := func(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	bold := func(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c).Bold(true) }

	return &Styles{
		Theme: t,

		Accent:     fg(t.Accent),
		AccentBold: bold(t.Accent),

		Signal:     fg(t.Signal),
		SignalBold: bold(t.Signal),

		Ok:     fg(t.Ok),
		Danger: bold(t.Danger),

		Link: fg(t.Link),

		Emphasis:     fg(t.Emphasis),
		EmphasisBold: bold(t.Emphasis),

		Text: fg(t.Text),
		Dim:  fg(t.Dim),
		Note: fg(t.Dim).Italic(true),

		Rule:    fg(t.Border),
		Surface: lipgloss.NewStyle().Background(t.Surface),

		PanelBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Border),
		PanelBorderFocused: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Accent),
		StatusBar: fg(t.Text).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Border),

		OverlayBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Accent).
			Padding(0, 1),
		OverlayDim: fg(t.Dim),
		TermCursor: lipgloss.NewStyle().Reverse(true),

		GaugeFill:  fg(t.Signal),
		GaugeTrack: fg(t.SignalDim),
	}
}

// Status colors a tool's tracked status: active is a resting healthy state,
// trying asks for a decision, inactive steps back. An unknown value reads as
// trying — the state a hand-edited meta.yaml is closest to.
func (s *Styles) Status(st loader.Status) lipgloss.Style {
	switch st {
	case loader.StatusActive:
		return s.Ok
	case loader.StatusInactive:
		return s.Dim
	case loader.StatusTrying:
		return s.Signal
	default:
		return s.Signal
	}
}

// defaultStyles is the Default theme built once. DefaultStyles is what a
// renderer falls back to when it was handed no styles at all — a Model built as
// a bare struct literal, which most tests are. Nothing outside that fallback
// (and package ui's own overlay compositor) may reach for it: a call site that
// does would keep rendering the default theme after the model switched themes.
var defaultStyles = NewStyles(Default)

func DefaultStyles() *Styles { return defaultStyles }
