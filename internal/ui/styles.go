package ui

import "github.com/charmbracelet/lipgloss"

var (
	// Palette — every color used in the UI is named here; styles below build
	// only from these (no inline hex literals).
	ColorPrimary   = lipgloss.Color("#DA7756")
	ColorOrange    = lipgloss.Color("#E5A040")
	ColorOrangeDim = lipgloss.Color("#7A5A1E") // darker yellow — the API-usage gauge's empty track
	ColorGreen     = lipgloss.Color("#6AAF6A")
	ColorMeta      = lipgloss.Color("#5588AA")
	ColorMuted     = lipgloss.Color("#AAAAAA")
	ColorDim       = lipgloss.Color("#888888")
	ColorBorder    = lipgloss.Color("#555555")
	ColorText      = lipgloss.Color("#E8E8E8")
	ColorCategory  = lipgloss.Color("#E8A87C")
	ColorKey       = lipgloss.Color("#C8A97E")
	ColorDanger    = lipgloss.Color("#D06060")

	PanelBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder)

	PanelBorderFocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary)

	// SelectionBarStyle renders the ⏺ cursor on the selected row while the
	// tools panel is focused; SelectionBarDimStyle keeps the cursor visible
	// (but muted) when focus moves to the brief/help panels.
	SelectionBarStyle = lipgloss.NewStyle().
				Foreground(ColorPrimary)

	SelectionBarDimStyle = lipgloss.NewStyle().
				Foreground(ColorDim)

	// SelectedNameStyle highlights the selected tool's name when the tools
	// panel is focused.
	SelectedNameStyle = lipgloss.NewStyle().
				Foreground(ColorPrimary).
				Bold(true)

	DescStyle = lipgloss.NewStyle().
			Foreground(ColorText)

	HelpStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder)

	SearchPromptStyle = lipgloss.NewStyle().
				Foreground(ColorPrimary).
				Bold(true)

	SearchMatchStyle = lipgloss.NewStyle().
				Foreground(ColorKey).
				Bold(true)

	GithubStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Rate-usage gauge (status-bar right corner): yellow brackets + used-count,
	// yellow ▮ fill glyphs and darker-yellow ░ track glyphs. The bar is drawn
	// with foreground-colored glyphs, not painted backgrounds, so it stays
	// visible when a terminal's color profile degrades and ANSI is stripped.
	// Colors are constant — the bar never recolors on rate pressure.
	RateBracketStyle    = lipgloss.NewStyle().Foreground(ColorOrange)
	RateUsageNumStyle   = lipgloss.NewStyle().Foreground(ColorOrange)
	RateGaugeFillStyle  = lipgloss.NewStyle().Foreground(ColorOrange)
	RateGaugeTrackStyle = lipgloss.NewStyle().Foreground(ColorOrangeDim)

	// WarnStyle / DangerStyle flag GitHub API rate-limit pressure in the
	// status bar and API-status overlay.
	WarnStyle = lipgloss.NewStyle().
			Foreground(ColorOrange).
			Bold(true)

	DangerStyle = lipgloss.NewStyle().
			Foreground(ColorDanger).
			Bold(true)

	UpdateAvailableStyle = lipgloss.NewStyle().
				Foreground(ColorOrange).
				Bold(true)

	// OkStyle marks a benign affirmative state — the card's "installed but
	// version unknown". Deliberately not UpdateAvailableStyle: orange there
	// means "act on this", and a working install needs no action.
	OkStyle = lipgloss.NewStyle().
		Foreground(ColorGreen).
		Bold(true)

	// My Tools status colors
	StatusColorActive   = ColorGreen
	StatusColorTrying   = ColorOrange
	StatusColorInactive = ColorMuted

	StatusStyleActive = lipgloss.NewStyle().
				Foreground(StatusColorActive).
				Bold(true)

	StatusStyleTrying = lipgloss.NewStyle().
				Foreground(StatusColorTrying).
				Bold(true)

	StatusStyleInactive = lipgloss.NewStyle().
				Foreground(StatusColorInactive)

	HelpFlagStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary)

	HelpSectionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorCategory)

	HelpMetaStyle = lipgloss.NewStyle().
			Foreground(ColorMeta)

	// HelpDimStyle repaints help lines outside the spotlighted entry while the
	// [3] panel's navigation cursor is active. Same color as OverlayDimStyle
	// today, but a separate name: the reading tint can be tuned without
	// touching the overlay compositor.
	HelpDimStyle = lipgloss.NewStyle().
			Foreground(ColorDim)

	MetaTagStyle = lipgloss.NewStyle().
			Foreground(ColorMeta)

	MetaNoteStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true)

	MetaDetailLabelStyle = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Width(8)

	RepoStatusStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true)

	// SectionLabelStyle renders the bracketed section headers in the brief
	// panel, e.g. "[info]". Bold + category color to stand out from the line.
	SectionLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorCategory)

	// InfoStyle is the non-italic muted style for the [info] section lines.
	InfoStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// ChangelogHeadingStyle marks a markdown heading inside the card's
	// [changelog] body: one emphasis step above the muted InfoStyle body, and
	// no new color — ColorText keeps it from competing with the peach
	// SectionLabelStyle that heads the card's own sections.
	ChangelogHeadingStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorText)

	// TagHeaderStyle / TagRuleStyle draw the tag-group divider rows in the [1]
	// tools list: "─ dev ────…". The label is muted (ColorDim), the rule lines
	// the panel-frame gray (ColorBorder), so group boundaries read geometrically
	// rather than by color — the tool names stay the only color accent in the
	// list. Deliberately not SectionLabelStyle (its ColorCategory competes with
	// the peach tool-name accent).
	TagHeaderStyle = lipgloss.NewStyle().
			Foreground(ColorDim)

	TagRuleStyle = lipgloss.NewStyle().
			Foreground(ColorBorder)

	// Scrollbar thumb: peach when the panel is focused, dim otherwise.
	ScrollThumbStyle = lipgloss.NewStyle().
				Foreground(ColorPrimary)

	ScrollThumbDimStyle = lipgloss.NewStyle().
				Foreground(ColorDim)
)
