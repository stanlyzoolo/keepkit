package ui

import "github.com/charmbracelet/lipgloss"

// Theme is the app's entire color vocabulary — ten semantic roles and nothing
// else. Styles are built from these roles only (NewStyles is the single
// consumer), so no file below internal/ui carries a hex literal and switching
// every color in keepkit is switching one Theme value.
//
// The roles are named for what a color *means*, not for what it looks like: a
// theme that paints "signal" green would still be internally consistent, while
// a role named "orange" would be a lie the moment it changed. Two rules keep
// the vocabulary from growing back into a palette:
//
//   - a new role must name a distinct *meaning*, not a shade — weight (Bold),
//     italics and the Surface background cover emphasis steps for free. This is
//     what retired the old ColorCategory/ColorKey (headers and search hits) and
//     ColorMuted (a second, indistinguishable text gray);
//   - Link is for URLs only. Everything that used to be "meta blue" — help
//     placeholders, hints, tag suffixes — is Dim, so a blue run of text in the
//     UI always means "this is an address".
type Theme struct {
	// Accent marks *focus and interaction*: the active panel's border and
	// title, the ⏺ cursor, the selected tool's name, and every key hint in the
	// app. If it is accented, pressing something is involved.
	Accent lipgloss.Color
	// Signal means "requires action" and nothing else: the ↑ on an outdated
	// tool, the latest version when an update exists, the updates counter,
	// status trying. Deliberately scarce — it is the color the eye should find
	// first on a screen full of tools.
	Signal lipgloss.Color
	// Ok is a healthy resting state: ● active maintenance, a version-less but
	// working install, keepkit's own up-to-date version.
	Ok lipgloss.Color
	// Danger is broken or exhausted: a failed detection, a spent rate limit.
	Danger lipgloss.Color
	// Link is URLs, and only URLs.
	Link lipgloss.Color
	// Emphasis is the brightest text on screen: the card's tool name, metric
	// values, section headings, the bold lead of a readme feature line. There
	// are meant to be very few of them per frame.
	Emphasis lipgloss.Color
	// Text is ordinary reading text — the default for anything with no reason
	// to stand out or step back.
	Text lipgloss.Color
	// Dim is labels, counters, SHAs, secondary hints: present, readable,
	// never competing.
	Dim lipgloss.Color
	// Border draws panel frames, dividers and the vertical rules between metric
	// columns — structure the eye should follow without reading.
	Border lipgloss.Color
	// Surface is the only background role: the card's metrics strip, the code
	// lines in its changelog, readme code plates. It must sit close enough to
	// the terminal background that a filled block reads as raised rather than
	// as a painted bar.
	Surface lipgloss.Color

	// SignalDim is the API gauge's empty track — the one place a *shade* of a
	// role is unavoidable, since fill and track must read as one bar. It is not
	// a semantic role and nothing else may use it.
	SignalDim lipgloss.Color
}

// Default is keepkit's own theme. A second theme is a second value of this
// struct — no other file changes.
var Default = Theme{
	Accent:    lipgloss.Color("#DA7756"),
	Signal:    lipgloss.Color("#E5A040"),
	Ok:        lipgloss.Color("#6AAF6A"),
	Danger:    lipgloss.Color("#D06060"),
	Link:      lipgloss.Color("#5F93B8"),
	Emphasis:  lipgloss.Color("#F2F3F5"),
	Text:      lipgloss.Color("#A8ADB8"),
	Dim:       lipgloss.Color("#767C88"),
	Border:    lipgloss.Color("#454B57"),
	Surface:   lipgloss.Color("#343945"),
	SignalDim: lipgloss.Color("#7A5A1E"),
}
