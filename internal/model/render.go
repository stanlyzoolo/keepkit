package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
	"github.com/stanlyzoolo/keepkit/internal/ui"
	"github.com/stanlyzoolo/keepkit/internal/version"
)

func (m Model) View() string {
	defer logx.Recover("model.View")

	if !m.ready {
		return "Loading..."
	}

	left := m.renderTools()
	middle := m.renderBrief()
	right := m.renderHelp()
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, middle, right)
	layout := lipgloss.JoinVertical(lipgloss.Left, body, m.renderStatusBar())
	if m.overlayVisible() {
		fg := m.renderAPIStatus()
		if m.mode == modeHotkeys {
			fg = m.renderHotkeys()
		}
		layout = ui.PlaceOverlay(layout, fg)
	}
	// Vertical margin only; no horizontal margin so panels/status bar reach the
	// terminal edges.
	return lipgloss.NewStyle().Margin(1, 0).Render(layout)
}

func (m Model) renderStatusBar() string {
	style := ui.HelpStyle.Width(m.width - 2)
	if m.mode == modeHelpSearch {
		matchInfo := ""
		if len(m.helpMatches) > 0 {
			matchInfo = fmt.Sprintf("  %d/%d matches", m.helpMatchIdx+1, len(m.helpMatches))
		} else if m.helpSearch.Value() != "" {
			matchInfo = "  no matches"
		}
		return style.Render(fmt.Sprintf(
			"%s %s  %s next  %s prev  %s exit%s",
			ui.SearchPromptStyle.Render("/"),
			m.helpSearch.View(),
			keyHint("n"),
			keyHint("N"),
			keyHint("esc"),
			matchInfo,
		))
	}
	if m.mode == modeSearch {
		return style.Render(fmt.Sprintf(
			"%s %s  %d/%d  %s open  %s move  %s cancel",
			ui.SearchPromptStyle.Render("/"),
			m.search.View(),
			len(m.searchMatches()),
			len(m.meta),
			keyHint("enter"),
			keyHint("↑/↓"),
			keyHint("esc"),
		))
	}
	if m.mode == modeEditNote {
		return style.Render(keyHint("enter") + " save  " + keyHint("esc") + " cancel")
	}
	if m.mode == modeEditTags {
		return style.Render(keyHint("enter") + " save  " + keyHint("esc") + " cancel  " + ui.MetaNoteStyle.Render("single tag"))
	}
	if m.mode == modeTrack {
		return style.Render(fmt.Sprintf(
			"%s %s  %s cancel",
			ui.SearchPromptStyle.Render("track (github url or tool name):"),
			m.trackInput.View(),
			keyHint("esc"),
		))
	}
	if m.mode == modeConfirmUntrack {
		return style.Render(fmt.Sprintf(
			"%s  %s yes  %s no",
			ui.SearchPromptStyle.Render("Untrack "+m.untrackTarget+"?"),
			keyHint("enter"),
			keyHint("esc"),
		))
	}
	if m.mode == modeRename {
		return style.Render(fmt.Sprintf(
			"%s %s  %s cancel",
			ui.SearchPromptStyle.Render("rename to:"),
			m.nameInput.View(),
			keyHint("esc"),
		))
	}
	if m.mode == modeRunInput {
		name := ""
		if mt, ok := m.selectedMeta(); ok {
			name = mt.Name
		}
		return style.Render(fmt.Sprintf(
			"%s %s  %s run  %s cancel",
			ui.SearchPromptStyle.Render("run "+name+":"),
			m.runInput.View(),
			keyHint("enter"),
			keyHint("esc"),
		))
	}
	if m.mode == modeConfirmUpdate {
		// The name comes from the plan's own target, not the selection: for
		// keepkit's own update the selection is a foreign tool — or nothing at
		// all, with an empty tracker — and for a tool's it may have moved while
		// detection ran.
		return style.Render(fmt.Sprintf(
			"%s  %s run  %s cancel",
			ui.SearchPromptStyle.Render("update "+m.updateTarget+": "+m.updatePlan.Display),
			keyHint("enter"),
			keyHint("esc"),
		))
	}
	if m.mode == modeTokenInput {
		return style.Render(keyHint("enter") + " validate & save  " + keyHint("esc") + " cancel")
	}
	if m.mode == modeAPIStatus {
		return style.Render(keyHint("r") + " refresh  " + keyHint("esc") + " close")
	}
	if m.mode == modeHotkeys {
		return style.Render(keyHint("esc") + " close")
	}
	if m.statusMsg != "" {
		return style.Render(ui.SearchPromptStyle.Render(m.statusMsg))
	}
	// The self-update banner replaces the hint cells in all three normal focus
	// states — one check here rather than one per focus branch, since those are
	// the only paths left below. A transient statusMsg outranks it (branch
	// above) and expires on its own after statusMsgTTL.
	if cells, ok := m.selfBannerCells(); ok {
		return m.renderHintsBar(style, cells)
	}
	if m.focus == focusBrief {
		hints := []string{
			keyHint("o") + " open repo",
			keyHint("c") + " changelog",
			keyHint("r") + " refresh",
			keyHint("s") + " status",
			keyHint("e") + " note",
			keyHint("t") + " tags",
			keyHint("q") + " quit",
			keyHint("?") + " keys",
		}
		if mt, ok := m.selectedMeta(); ok && m.hasUpdate(mt.Name) {
			hints = append([]string{keyHint("u") + " update"}, hints...)
		}
		return m.renderHintsBar(style, hints)
	}
	if m.focus == focusHelp {
		var hints []string
		// With a navigable entry index j/k drive the spotlight cursor while
		// the arrows keep their line scroll — advertise both; without
		// entries (update log, placeholders, prose) j/k scroll too.
		if m.helpNavIdx >= 0 {
			hints = append(hints, keyHint("esc")+" exit nav")
		}
		if len(m.helpEntries) > 0 {
			hints = append(hints, keyHint("j/k")+" navigate")
		}
		hints = append(hints,
			keyHint("↑↓")+" scroll",
			keyHint("h")+" --help",
			keyHint("m")+" man",
			keyHint("r")+" readme",
		)
		// The help search tears glamour's ANSI apart, so [/] is a no-op in
		// readme mode — don't advertise a key that does nothing there.
		if m.helpMode != helpModeReadme {
			hints = append(hints, keyHint("/")+" search")
		}
		hints = append(hints, keyHint("←")+" back", keyHint("q")+" quit", keyHint("?")+" keys")
		return m.renderHintsBar(style, hints)
	}
	return m.renderHintsBar(style, []string{
		keyHint("enter") + " run",
		keyHint("/") + " search",
		keyHint("t") + " track",
		keyHint("u") + " untrack",
		keyHint("r") + " rename",
		keyHint("q") + " quit",
		keyHint("?") + " keys",
	})
	// No [space] group cell here on purpose: this list already overflows 80
	// columns (the trailing cells drop), so the hint would be invisible on the
	// baseline terminal anyway, while on wider ones its 15 columns push the
	// API-usage gauge from its full form down to the compact one. The [?]
	// overlay documents the binding instead.
}

// rateGaugeMinGap is the minimum blank columns between the hint bar and the
// right-aligned API-usage gauge; below it the gauge is downgraded or dropped.
const rateGaugeMinGap = 2

// hintSep separates two hint cells in the status bar.
const hintSep = "  "

// renderHintsBar lays out the left-aligned hint cells with the right group (the
// API-usage gauge plus, when there is one, the collapsed self-update cell)
// pinned to the right corner. inner is HelpStyle's content width (m.width-2, the
// border sits outside it).
//
// The bar must stay exactly one line: HelpStyle is width-constrained, so a hint
// list wider than inner wraps to a second row and View() returns m.height+1
// lines — one row past the terminal, which scrolls the top border off the alt
// screen. Cells are therefore dropped from the right (they are ordered
// most-important first) until the joined hints fit; the least useful reminders
// ([?] keys, [q] quit) are the first to go on a narrow terminal. A collapsed
// self cell keeps dropping them: it is the only remaining surface of a pending
// update, so it outranks the trailing reminders as well as the gauge, and at 80
// columns it would otherwise never fit at all. The leading cell is never
// dropped, and when even it is too wide (the fused banner cell on a very narrow
// terminal) it is truncated rather than allowed to wrap.
//
// Only then is the right group placed, degrading in a fixed order: full gauge +
// self cell → compact gauge + self cell → self cell alone → compact gauge. (The
// full gauge is not a step after the self cell: place() fails monotonically in
// width and the full gauge is always wider than the cell, so it could never fit
// where the cell did not.)
func (m Model) renderHintsBar(style lipgloss.Style, cells []string) string {
	inner := m.width - 2
	joined := func(cs []string) int { return lipgloss.Width(strings.Join(cs, hintSep)) }
	// The self cell is reserved for before any dropping starts: it lives in the
	// right group and is never dropped itself, so the hints have to fit around
	// it. One loop, one rule — a plain "fit the hints" pass ahead of it would be
	// dead work whose predicate has to be kept in sync with this one by hand.
	self := m.selfCompactCell()
	reserve := 0
	if self != "" {
		reserve = rateGaugeMinGap + lipgloss.Width(self)
	}
	for len(cells) > 1 && joined(cells)+reserve > inner {
		cells = cells[:len(cells)-1]
	}
	hints := strings.Join(cells, hintSep)
	if lipgloss.Width(hints) > inner {
		// One cell wider than the whole bar — the fused banner cell on a very
		// narrow terminal. Styling is stripped before the cut: a cut landing
		// inside an escape sequence would be re-emitted to the terminal verbatim.
		hints = truncateToWidth(stripANSI(hints), max(inner, 1))
	}
	place := func(right string) (string, bool) {
		if right == "" {
			return "", false
		}
		gap := inner - lipgloss.Width(hints) - lipgloss.Width(right)
		if gap < rateGaugeMinGap {
			return "", false
		}
		return hints + strings.Repeat(" ", gap) + right, true
	}
	// withGauge pairs a gauge with the self cell, or yields "" when that gauge
	// form is unavailable (no known rate snapshot) so place() skips the
	// candidate instead of rendering a stray separator.
	withGauge := func(gauge string) string {
		if gauge == "" {
			return ""
		}
		return gauge + hintSep + self
	}
	full, compact := m.renderRateGauge(false), m.renderRateGauge(true)
	candidates := []string{full, compact}
	if self != "" {
		candidates = []string{withGauge(full), withGauge(compact), self, compact}
	}
	for _, c := range candidates {
		if line, ok := place(c); ok {
			return style.Render(line)
		}
	}
	return style.Render(hints)
}

// selfBannerCells returns the self-update banner as most-important-first hint
// cells (the shape renderHintsBar drops from the right) and whether it replaces
// the normal hints. The announcement and its main action are deliberately fused
// into one cell: a separate "[U] update" cell would be the first thing dropped
// on a narrow terminal, leaving an announcement with no way to act on it.
//
// While the self-update itself runs there is no banner — the compact
// "keepkit updating…" cell in the right group is the only in-flight indicator
// besides the live log in [3] (an untracked keepkit has no card, so the card
// spinner never fires), and the normal hints stay usable meanwhile.
func (m Model) selfBannerCells() ([]string, bool) {
	if m.selfUpdating() {
		return nil, false
	}
	switch m.selfState {
	case selfOffered:
		return []string{
			selfToolName + " " + ui.UpdateAvailableStyle.Render(m.selfLatest) + " available — " + keyHint("U") + " update",
			keyHint("X") + " dismiss",
		}, true
	case selfUpdated:
		return []string{
			selfToolName + " updated — " + keyHint("U") + " restart",
			keyHint("X") + " later",
		}, true
	case selfNone, selfDismissed, selfUpdatedLater:
		// No full banner: nothing was offered, or [X] folded it into the compact
		// cell selfCompactCell renders instead.
	default:
		// Enumerated rather than left to fall through: a new selfState must be
		// decided here, and TestSelfStateSitesAreExhaustive is what says so.
	}
	return nil, false
}

// selfCompactCell returns the collapsed self-update cell for the status bar's
// right group, or "" when the banner is either absent or shown in full. It is
// what keeps [U] reachable for the rest of the session after [X], so the dismiss
// is a fold, not a cancel.
//
// The ↑ glyph is East-Asian Ambiguous (two cells under RUNEWIDTH_EASTASIAN=1),
// the same accepted class as the tool list's ⏺/↑ markers. renderHintsBar
// measures the cell with lipgloss.Width, so an over-wide measurement can only
// drop the cell earlier, never overflow the line.
func (m Model) selfCompactCell() string {
	if m.selfUpdating() {
		return selfToolName + " updating…"
	}
	switch m.selfState {
	case selfDismissed:
		return selfToolName + " " + ui.UpdateAvailableStyle.Render("↑") + " " + keyHint("U")
	case selfUpdatedLater:
		return selfToolName + " " + keyHint("U") + " restart"
	case selfNone, selfOffered, selfUpdated:
		// Nothing to collapse: no banner at all, or the full one is on screen.
	default:
		// See selfBannerCells: a new state has to be decided at every site.
	}
	return ""
}

func keyHint(k string) string {
	return ui.SearchPromptStyle.Render("[" + k + "]")
}

// gaugeCells is the fixed width of the API-usage bar, independent of whether the
// limit is 60 (no token) or 5000 (with token) — the fill tracks the used ratio,
// not an absolute count.
const gaugeCells = 12

// gaugeFillGlyph / gaugeTrackGlyph draw the bar. Both must stay width-stable:
// neither is East-Asian-Ambiguous, so lipgloss.Width counts them as one cell
// even under RUNEWIDTH_EASTASIAN=1. A full block (█ U+2588) is Ambiguous and
// would measure as two cells there, inflating the gap math in renderHintsBar
// and wrongly downgrading or mis-padding the bar.
const (
	gaugeFillGlyph  = "▮" // U+25AE black vertical rectangle
	gaugeTrackGlyph = "░" // U+2591 light shade
)

// renderRateGauge builds the right-corner "GitHub API Usage" indicator for the
// current rate snapshot, or "" when there is no known snapshot. It shows
// used/limit (used = Limit-Remaining), matching the API-status overlay. The bar
// is ▮ fill / ░ track glyphs (visible even if ANSI is stripped; any usage shows
// at least one filled cell via gaugeFilled) and constant yellow at every
// pressure level — exhaustion (used==limit) simply renders a full bar; the ⚠/✕
// alarm lives only in the [L] overlay. compact drops the label and bar, keeping
// "GH used/limit [L]" for narrow terminals.
func (m Model) renderRateGauge(compact bool) string {
	r := m.rate
	if !r.Known || r.Limit <= 0 {
		return ""
	}
	used := usedOf(r)
	nums := ui.RateUsageNumStyle.Render(fmt.Sprintf("%d/%d", used, r.Limit))
	if compact {
		return ui.GithubStyle.Render("GH ") + nums + " " + keyHint("L")
	}
	filled := gaugeFilled(used, r.Limit)
	bar := ui.RateGaugeFillStyle.Render(strings.Repeat(gaugeFillGlyph, filled)) +
		ui.RateGaugeTrackStyle.Render(strings.Repeat(gaugeTrackGlyph, gaugeCells-filled))
	return ui.GithubStyle.Render("GitHub API Usage ") +
		ui.RateBracketStyle.Render("[") + bar + ui.RateBracketStyle.Render("]") +
		" " + nums + "  " + keyHint("L") + ui.GithubStyle.Render(" details")
}

// usedOf returns consumed requests (Limit-Remaining) clamped to [0,Limit], the
// single source of used/limit for both the status-bar gauge and the [L] overlay.
// GitHub always reports Remaining in [0,Limit]; the clamp is defensive against a
// malformed snapshot.
func usedOf(r version.RateLimit) int {
	used := r.Limit - r.Remaining
	if used < 0 {
		return 0
	}
	if used > r.Limit {
		return r.Limit
	}
	return used
}

// gaugeFilled maps used/limit onto the fixed gaugeCells-wide bar, rounding to the
// nearest cell with integer math (no math import) and clamping to [0,gaugeCells].
// The rounded ratio is then clamped into a truthful range: any usage shows at
// least one cell (with limit 5000 the first cell would otherwise need ~209
// requests), and only exhaustion renders a full bar (used < limit caps at
// gaugeCells-1). Outside those edge bands the fill tracks the used ratio only,
// so limit 60 and limit 5000 fill identically at the same percentage.
func gaugeFilled(used, limit int) int {
	if limit <= 0 || used <= 0 {
		return 0
	}
	filled := (used*gaugeCells + limit/2) / limit
	if filled < 1 {
		filled = 1
	}
	maxFill := gaugeCells
	if used < limit {
		maxFill = gaugeCells - 1
	}
	if filled > maxFill {
		filled = maxFill
	}
	return filled
}

// rateLowThreshold is the number of remaining GitHub API requests at or below
// which the status bar (and API-status overlay) flags rate-limit pressure.
const rateLowThreshold = 10

// rateLevel classifies a rate snapshot's pressure so the status-bar signal and
// the overlay icon share one decision. It is the single source of truth for the
// none/warn/exhausted thresholds.
type rateLevel int

func classifyRate(r version.RateLimit) rateLevel {
	if !r.Known {
		return rateUnknown
	}
	switch {
	case r.Remaining == 0:
		return rateExhausted
	case r.Remaining <= rateLowThreshold:
		return rateWarn
	default:
		return rateOK
	}
}

// rateIcon returns the styled indicator (none / ⚠ / ✕) for a rate snapshot,
// sharing classifyRate with the status-bar signal so the overlay and the bar
// never disagree. Returns "" when nothing should flag.
func rateIcon(rate version.RateLimit) string {
	switch classifyRate(rate) {
	case rateExhausted:
		return ui.DangerStyle.Render("✕")
	case rateWarn:
		return ui.WarnStyle.Render("⚠")
	default:
		return ""
	}
}

// maskToken renders a token as its first 4 and last 4 characters joined by
// bullets (e.g. "ghp_••••••••3f2a"), never exposing the middle. Short tokens
// are fully masked.
func maskToken(t string) string {
	if len(t) <= 8 {
		return strings.Repeat("•", len(t))
	}
	return t[:4] + strings.Repeat("•", 8) + t[len(t)-4:]
}

// renderAPIStatus builds the API-status overlay body: an optional add-token
// nudge (when none is configured), the token source (masked), used/limit with
// the shared icon, and the reset time.
func (m Model) renderAPIStatus() string {
	var b strings.Builder
	b.WriteString(ui.SectionLabelStyle.Render("GitHub API status") + "\n\n")

	source := version.TokenSource()
	// Nudge the user to add a token when none is configured — it lifts the hourly
	// limit from 60 to 5000. Hidden once a token exists or while entering one.
	if source == "none" && m.mode != modeTokenInput {
		b.WriteString(ui.WarnStyle.Render("Add a GitHub token to raise the limit (60 → 5000/h)  ") + keyHint("e") + "\n\n")
	}

	tokenLine := "Token: " + source
	if tok := version.Token(); tok != "" {
		tokenLine += " (" + maskToken(tok) + ")"
	}
	b.WriteString(ui.InfoStyle.Render(tokenLine) + "\n")

	if m.rate.Known {
		icon := rateIcon(m.rate)
		// Used/limit matches the status-bar gauge so the two surfaces agree.
		usedLine := fmt.Sprintf("Used: %d / %d", usedOf(m.rate), m.rate.Limit)
		if icon != "" {
			usedLine = icon + " " + usedLine
		}
		b.WriteString(ui.InfoStyle.Render(usedLine) + "\n")
		if !m.rate.Reset.IsZero() {
			mins := int(time.Until(m.rate.Reset).Minutes())
			if mins < 0 {
				mins = 0
			}
			b.WriteString(ui.InfoStyle.Render(fmt.Sprintf(
				"Reset: in %d min (%s)", mins, m.rate.Reset.Format("15:04"))) + "\n")
		}
	} else {
		b.WriteString(ui.InfoStyle.Render("Limit: unknown") + "\n")
	}

	if m.mode == modeTokenInput {
		b.WriteString("\n" + ui.SearchPromptStyle.Render("token: ") + m.tokenInput.View() + "\n")
	}
	if m.tokenError != "" {
		b.WriteString(ui.DangerStyle.Render(m.tokenError) + "\n")
	}

	b.WriteString("\n")
	var hints string
	if m.mode == modeTokenInput {
		hints = keyHint("enter") + " validate & save  " + keyHint("esc") + " cancel"
	} else {
		hints = keyHint("e") + " set token  "
		if source == "config" {
			hints += keyHint("d") + " remove token  "
		}
		hints += keyHint("r") + " refresh  " + keyHint("esc") + " close"
	}
	b.WriteString(ui.InfoStyle.Render(hints))

	return ui.OverlayBorder.Render(b.String())
}

// hotkeyRow is one binding line in the [?] overlay: a key cell (bracketed by
// keyHint) and a 1-4 word description.
type hotkeyRow struct{ key, desc string }

// hotkeyGroup is a titled block of binding rows in one overlay column.
type hotkeyGroup struct {
	title string
	rows  []hotkeyRow
}

// renderHotkeys builds the static [?] hotkeys overlay: a title row with the
// close hint right-aligned, then a three-column grid of every normal-mode
// binding, one binding per line, grouped and annotated by the panel/mode it
// belongs to. Each group header is preceded by a blank line (except the first
// in its column) and sits directly above its own rows — the blank line that
// used to follow the header was reclaimed when the readme row pushed the
// tallest column past the row budget, and no column partition of the five
// groups fits 20 rows with it. Inside a column every
// key cell is padded to the column's widest key so descriptions line up and the
// keys never drift sideways. Styled like the [L] overlay (OverlayBorder frame,
// SectionLabelStyle headers, keyHint keys, InfoStyle text). Hard size budget:
// <= 20 rows x <= 76 cols framed, so it fits the 80x24 composited background
// that PlaceOverlay clips off the bottom.
func (m Model) renderHotkeys() string {
	hdr := ui.SectionLabelStyle.Render
	info := ui.InfoStyle.Render
	// padTo right-pads a (possibly ANSI-styled) cell to a visible width so the
	// descriptions line up; measured with lipgloss.Width, which strips ANSI.
	padTo := func(s string, w int) string {
		if d := w - lipgloss.Width(s); d > 0 {
			return s + strings.Repeat(" ", d)
		}
		return s
	}

	// renderColumn stacks groups vertically: a blank line separates groups
	// (none above the first), the header sits directly on top of its rows, and
	// every key cell is padded to the column's widest key so every description
	// in the column starts at the same offset.
	renderColumn := func(groups []hotkeyGroup) string {
		keyW := 0
		for _, g := range groups {
			for _, r := range g.rows {
				if w := lipgloss.Width(keyHint(r.key)); w > keyW {
					keyW = w
				}
			}
		}
		var lines []string
		for gi, g := range groups {
			if gi > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, hdr(g.title))
			for _, r := range g.rows {
				lines = append(lines, padTo(keyHint(r.key), keyW)+"  "+info(r.desc))
			}
		}
		return strings.Join(lines, "\n")
	}

	col1 := renderColumn([]hotkeyGroup{
		{"Global", []hotkeyRow{
			{"1/2/3", "focus panel"},
			{"←/→", "move focus"},
			{"esc / q", "back / quit"},
			{"L", "API status"},
			{"?", "this help"},
		}},
		{"[1] Tools", []hotkeyRow{
			{"j/k ↑/↓", "select tool"},
			{"g/G", "first / last"},
			{"space", "group by tag"},
			{"enter", "run in tab"},
			{"t", "track tool"},
			{"u", "untrack tool"},
			{"r", "rename tool"},
			{"/", "filter tools"},
		}},
	})

	// Self lives in column 2 because it is the shortest one: column 1 is exactly
	// at the 20-row budget, so a group there would clip. Descriptions must stay
	// <= 12 cells (the column's current widest, "cycle status") or the column —
	// and with it the overlay — grows past 76 columns.
	//
	// It is the one state-dependent group: U and X are bound only while a banner
	// is on screen, and the two live states bind them differently (update/dismiss
	// vs restart/later). Listing one fixed wording would document half the state
	// machine and, on a build with no banner at all (any dev build), advertise two
	// dead keys.
	groups2 := []hotkeyGroup{
		{"[2] Brief", []hotkeyRow{
			{"o", "open repo"},
			{"c", "changelog"},
			{"r", "refresh"},
			{"s", "cycle status"},
			{"e", "edit note"},
			{"t", "edit tags"},
			{"u", "run update"},
			{"h", "--help"},
			{"m", "man page"},
		}},
	}
	switch m.selfState {
	case selfOffered, selfDismissed:
		groups2 = append(groups2, hotkeyGroup{"Self", []hotkeyRow{
			{"U", "self-update"},
			{"X", "dismiss"},
		}})
	case selfUpdated, selfUpdatedLater:
		groups2 = append(groups2, hotkeyGroup{"Self", []hotkeyRow{
			{"U", "restart"},
			{"X", "later"},
		}})
	case selfNone:
		// No banner, so U/X are unbound and the group would document dead keys.
	default:
		// See selfBannerCells: a new state has to be decided at every site.
	}
	col2 := renderColumn(groups2)

	col3 := renderColumn([]hotkeyGroup{
		{"[3] Help / Man / Readme", []hotkeyRow{
			{"j/k", "entry nav"},
			{"↑/↓", "scroll"},
			{"esc", "exit nav"},
			{"h/m", "help / man"},
			{"r", "readme"},
			{"/", "search"},
			{"n/N", "next match"},
		}},
		{"Scrolling", []hotkeyRow{
			{"j/k ↑/↓", "3 lines"},
			{"ctrl+d/u", "half page"},
			{"ctrl+f/b", "full page"},
			{"PgUp/PgDn", "full page"},
			{"g/G", "top / bottom"},
		}},
	})

	grid := lipgloss.JoinHorizontal(lipgloss.Top, col1, "   ", col2, "   ", col3)

	// Title row: heading left, close hint right-aligned to the grid width so the
	// overlay reads as a single block, with a blank line below it separating the
	// title from the grid.
	title := hdr("Keyboard shortcuts")
	closeHint := keyHint("esc") + " " + info("close")
	titleRow := title + " " + closeHint
	if pad := lipgloss.Width(grid) - lipgloss.Width(title) - lipgloss.Width(closeHint); pad > 0 {
		titleRow = title + strings.Repeat(" ", pad) + closeHint
	}

	body := lipgloss.JoinVertical(lipgloss.Left, titleRow, "", grid)
	return ui.OverlayBorder.Render(body)
}

func (m Model) calcVpHeight() int {
	// Match the panel's inner content height. lipgloss adds borders outside the
	// configured Height, so Height(m.height-7) gives exactly m.height-7 content
	// rows; the viewport must fill them so the scrollbar reaches the bottom.
	return max(m.height-7, 1)
}

func (m Model) calcPanelWidths() (toolsW, briefW, helpW int) {
	// 20%-40%-40% layout. lipgloss adds borders OUTSIDE the configured Width,
	// so Width(panelW) renders as panelW+2 on screen, and panel content fills
	// the full panelW (dividers/viewports use panelW, not panelW-2).
	// Horizontal overhead reserved here = 6: 2 border cols x 3 panels. There is
	// no outer horizontal margin and panels sit flush against each other.
	available := max(m.width-6, 1)
	toolsW = max((available*20)/100, 15)
	briefW = max((available*40)/100, 30)
	helpW = available - toolsW - briefW
	if helpW < 30 {
		helpW = 30
		briefW = available - toolsW - helpW
		if briefW < 30 {
			briefW = 30
			toolsW = available - briefW - helpW
			// Ensure toolsW doesn't go negative on very small terminals
			if toolsW < 1 {
				toolsW = 1
				// Reduce other panels proportionally
				briefW = max((available-toolsW-5)/2, 1)
				helpW = available - toolsW - briefW
			}
		}
	}
	return
}

// renderLeftContent is the list text alone, for callers that only paint or
// inspect it; setToolsContent goes through buildToolRows to store the line maps
// that come with it.
func (m Model) renderLeftContent() string {
	content, _, _ := m.buildToolRows()
	return content
}

// tagHeaderLine renders a tag-group header as a divider row with the label
// centered: "──────── dev ────────" across the full list width, label in muted
// TagHeaderStyle and the rule lines in the panel-frame TagRuleStyle (no hashtag)
// — group boundaries read geometrically so the tool names stay the only color
// accent. The trailing group's label is "untagged". One header must occupy
// exactly one screen line — every consumer of the line maps assumes it — so the
// label is flattened (a tag from a hand-edited meta.yaml can carry a newline,
// which would silently emit a second row) and the whole line is built to exactly
// w cells, so the viewport never wraps or truncates it.
func (m Model) tagHeaderLine(tag string) string {
	label := "untagged"
	if tag != "" {
		label = flattenLine(tag)
	}
	w := max(m.toolsW-1, 1)
	// Too narrow to frame a "─ label ─" — degrade to the bare label, cut to the
	// panel so it still occupies a single line no wider than w.
	if w < 4 {
		return ui.TagHeaderStyle.Render(truncateToWidth(label, w))
	}
	// Reserve a rule on each side (≥1) + the two spaces around the label = 4
	// cells of frame, so the label gets w-4. The remaining rule is split evenly
	// (odd remainder → the extra dash goes right). lipgloss.Width, not a rune
	// count, so a CJK tag is cut by display cells (the same reason
	// truncateToWidth exists).
	label = truncateToWidth(label, max(w-4, 1))
	rule := max(w-2-lipgloss.Width(label), 0)
	head := rule / 2
	tail := rule - head
	return ui.TagRuleStyle.Render(strings.Repeat("─", head)+" ") +
		ui.TagHeaderStyle.Render(label) +
		ui.TagRuleStyle.Render(" "+strings.Repeat("─", tail))
}

// flattenLine collapses the line breaks (and the stray control characters that
// travel with them) of a single-line label into spaces.
func flattenLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// truncateToWidth cuts s to at most w display cells. It measures with
// lipgloss.Width — the same go-runewidth condition the renderer uses, where a
// CJK glyph is two cells — because a rune count would let a wide-character tag
// render past the panel edge and be hard-cut mid-glyph by the viewport. Cuts on
// rune boundaries; a multi-rune grapheme cluster (a flag emoji) can still split,
// which is cosmetic and needs a segmenter to do better.
func truncateToWidth(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > w {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String()
}

// buildToolRows renders the tool list and the tool-index ↔ screen-line maps
// that go with it: toolLine[i] is tool i's screen line, lineTool[l] the tool on
// screen line l (-1 on a group header). Flat view has no headers, so both are
// the identity there; the tag view is the only thing that makes them differ,
// which is what keeps metaSelected a tool index everywhere else.
func (m Model) buildToolRows() (string, []int, []int) {
	var sb strings.Builder
	matches := m.searchMatches()
	query := m.searchQuery()
	maxName := m.toolsW - 5
	grouped := m.grouped()

	toolLine := make([]int, len(matches))
	lineTool := make([]int, 0, len(matches))
	line := 0
	prevTag := ""

	for i, sm := range matches {
		mt := sm.meta

		// Tag view: open a section whenever the tag changes. searchMatches has
		// already made each tag's rows contiguous, so one pass emits every
		// header exactly once. The comparison uses the same case-folded key the
		// grouping does, so `CLI` and `cli` stay one section — the header then
		// shows the spelling of the group's first tool.
		if grouped {
			key := tagKey(mt)
			if i == 0 || key != prevTag {
				sb.WriteString(m.tagHeaderLine(tagOf(mt)) + "\n")
				lineTool = append(lineTool, -1)
				line++
			}
			prevTag = key
		}
		name := wrapText(mt.Name, maxName)
		name = strings.TrimRight(name, "\n")
		plainNameW := lipgloss.Width(name)

		updateMark, updateW := "", 0
		if m.hasUpdate(mt.Name) {
			updateMark = " " + ui.UpdateAvailableStyle.Render("↑")
			updateW = lipgloss.Width(updateMark)
		}

		// While searching, highlight the matched substring of the name —
		// except on the focused selected row, whose whole name is about to
		// get the same peach-bold style anyway (nesting the ANSI would only
		// corrupt it).
		selected := i == m.metaSelected
		if query != "" && (!selected || m.focus != focusTools) {
			name = highlightNameMatch(name, query)
		}

		// Rows that matched only by tag show the tag that earned them the
		// spot, dimmed, right of the name — skipped when the row's full
		// budget (marker column + name column + update mark, see maxName)
		// cannot absorb it without wrapping.
		if sm.byTagOnly {
			if tagW := lipgloss.Width("#" + sm.tag); plainNameW+tagW+updateW <= maxName+1 {
				name += " " + ui.MetaNoteStyle.Render("#"+sm.tag)
			}
		}

		// Marker column (width 1): ⏺ on the selected row — peach while the
		// tools panel is focused, dim otherwise so the selection never
		// disappears when focus moves to brief/help. Non-selected rows get a
		// plain space (no status edge — tool status lives in the brief card).
		// The marker stays visible in modeSearch too: the cursor there is
		// user-controlled (arrows move the highlight through the matches).
		var mark string
		switch {
		case selected && m.focus == focusTools:
			mark = ui.SelectionBarStyle.Render("⏺")
			name = ui.SelectedNameStyle.Render(name)
		case selected:
			mark = ui.SelectionBarDimStyle.Render("⏺")
		default:
			mark = " "
		}
		toolLine[i] = line
		lineTool = append(lineTool, i)
		line++
		sb.WriteString(mark + " " + name + updateMark + "\n")
	}

	if len(matches) == 0 {
		if m.mode == modeSearch {
			sb.WriteString(ui.DescStyle.Render("  No matches.") + "\n")
		} else {
			sb.WriteString(ui.DescStyle.Render("  No tools tracked.\n  Press t to add one.") + "\n")
		}
	}

	return sb.String(), toolLine, lineTool
}

// highlightNameMatch renders the first occurrence of the query inside the
// (possibly wrapped) tool name peach-bold — distinct from highlightMatch
// (textutil.go), the single-line ColorKey highlighter of the help search.
// Matching is case-insensitive (rune-wise via runeIndexFold, so names whose
// lowercase form has a different byte length cannot desync the slice offsets)
// and per-line: a match split across a wrap boundary stays unhighlighted.
// query must already be lowercase (searchQuery normalizes it).
func highlightNameMatch(name, query string) string {
	lines := strings.Split(name, "\n")
	qr := []rune(query)
	for i, line := range lines {
		lr := []rune(line)
		idx := runeIndexFold(lr, qr)
		if idx < 0 {
			continue
		}
		end := idx + len(qr)
		lines[i] = string(lr[:idx]) + ui.SelectedNameStyle.Render(string(lr[idx:end])) + string(lr[end:])
	}
	return strings.Join(lines, "\n")
}

// selectedLine is the selected tool's screen line — metaSelected translated
// through toolLine, which under grouping differs from it by the headers above.
// A model whose list was never painted (hand-built, pre-first-render) has no
// map and falls back to the identity the flat list has anyway.
func (m Model) selectedLine() int {
	if m.metaSelected >= 0 && m.metaSelected < len(m.toolLine) {
		return m.toolLine[m.metaSelected]
	}
	return m.metaSelected
}

// toolAtLine maps a screen line of the list back to a tool index, or -1 for a
// header row or a line past the end. Identity fallback as in selectedLine.
func (m Model) toolAtLine(line int) int {
	if len(m.lineTool) == 0 {
		return line
	}
	if line < 0 || line >= len(m.lineTool) {
		return -1
	}
	return m.lineTool[line]
}

// toolNearLine resolves a *screen line* to a selectable tool: the tool on that
// line, else the first one found stepping in dir (+1 down, -1 up), clamped to
// the ends. It is what lets the page and half-page keys move by a screen page:
// their step is a count of viewport rows, but metaSelected counts tools, and
// under grouping the headers in between make those two units differ — stepping
// the row count as a tool count skips rows that were never displayed.
func (m Model) toolNearLine(line, dir int) int {
	n := len(m.filteredMeta())
	if n == 0 {
		return 0
	}
	if len(m.lineTool) == 0 {
		// No map (hand-built model): lines are tool indices.
		return min(max(line, 0), n-1)
	}
	if line < 0 {
		return 0
	}
	if line >= len(m.lineTool) {
		return n - 1
	}
	for i := line; i >= 0 && i < len(m.lineTool); i += dir {
		if t := m.lineTool[i]; t >= 0 {
			return t
		}
	}
	// Only headers that way (the leading header when moving up): take the end.
	if dir < 0 {
		return 0
	}
	return n - 1
}

// syncToolsViewport adjusts YOffset so that the selected tool is visible. Both
// branches work in screen lines (selectedLine, not metaSelected): mixing the
// two units breaks scrolling as soon as a header sits above the selection.
func (m *Model) syncToolsViewport() {
	vpH := m.toolsViewport.Height
	if vpH <= 0 {
		return
	}
	line := m.selectedLine()
	// Scrolling up reveals the group header directly above the selection too:
	// clamping to the tool's own line alone would park the first group's header
	// one row above the window, with no keyboard way to bring it back.
	top := line
	if i := line - 1; i >= 0 && i < len(m.lineTool) && m.lineTool[i] == -1 {
		top = i
	}
	if top < m.toolsViewport.YOffset {
		m.toolsViewport.SetYOffset(top)
	} else if line >= m.toolsViewport.YOffset+vpH {
		m.toolsViewport.SetYOffset(line - vpH + 1)
	}
}

// setToolsContent refreshes viewport content, the line maps and the scroll
// position. It is the ONLY place the list content may be repainted: the maps
// describe the content it just wrote, so a SetContent(renderLeftContent())
// anywhere else would leave them describing the previous list.
func (m *Model) setToolsContent() {
	content, toolLine, lineTool := m.buildToolRows()
	m.toolLine, m.lineTool = toolLine, lineTool
	m.toolsViewport.SetContent(content)
	m.syncToolsViewport()
}

func (m Model) renderTools() string {
	focused := m.focus == focusTools
	panelStyle := ui.PanelBorder
	if focused {
		panelStyle = ui.PanelBorderFocused
	}

	panel := panelStyle.
		Width(m.toolsW).
		Height(max(m.height-7, 1)).
		Render(withScrollbar(m.toolsViewport, m.toolsW, focused))
	return insetPanelTitle(panel, "[1] Tools", focused)
}

func (m Model) renderBrief() string {
	focused := m.focus == focusBrief
	panelStyle := ui.PanelBorder
	if focused {
		panelStyle = ui.PanelBorderFocused
	}

	panel := panelStyle.
		Width(m.briefW).
		Height(max(m.height-7, 1)).
		Render(withScrollbar(m.briefViewport, m.briefW, focused))
	return insetPanelTitle(panel, "[2] Brief", focused)
}

func (m Model) renderHelp() string {
	focused := m.focus == focusHelp
	panelStyle := ui.PanelBorder
	if focused {
		panelStyle = ui.PanelBorderFocused
	}

	title := "[3] Help"
	switch m.helpMode {
	case helpModeMan:
		title = "[3] Man"
	case helpModeReadme:
		title = "[3] Readme"
	}
	// While an update log is showing — the selected tool's, or keepkit's own,
	// which is selection-independent — the panel is the log, not help. Mirror
	// that in the inset title.
	if m.showsUpdateLog() {
		title = "[3] Update"
	}
	panel := panelStyle.
		Width(m.helpW).
		Height(max(m.height-7, 1)).
		Render(withScrollbar(m.helpViewport, m.helpW, focused))
	return insetPanelTitle(panel, title, focused)
}

// insetPanelTitle splices " title " into the top border line of a rendered
// panel, starting at the 3rd visible cell. The top border is a homogeneously
// colored run of single-width runes, so instead of ANSI-aware splicing the
// line is rebuilt from its stripANSI text and repainted whole with the border
// color (peach when focused). A title that does not fit is dropped whole —
// the panel is returned unchanged (a chopped title reads worse than none).
func insetPanelTitle(panel, title string, focused bool) string {
	lines := strings.SplitN(panel, "\n", 2)
	top := []rune(stripANSI(lines[0]))
	label := []rune(" " + title + " ")
	const start = 2               // keep the corner + one ─ cell
	avail := len(top) - start - 1 // keep the closing corner
	if len(label) > avail {
		return panel
	}
	rebuilt := make([]rune, 0, len(top))
	rebuilt = append(rebuilt, top[:start]...)
	rebuilt = append(rebuilt, label...)
	rebuilt = append(rebuilt, top[start+len(label):]...)
	color := ui.ColorBorder
	if focused {
		color = ui.ColorPrimary
	}
	lines[0] = lipgloss.NewStyle().Foreground(color).Render(string(rebuilt))
	return strings.Join(lines, "\n")
}

// withScrollbar renders a viewport with a 1-col scrollbar gutter on its right
// edge. The gutter stays blank unless the content is taller than the viewport,
// in which case a thumb (no track) is drawn proportional to the scroll position.
// The thumb is peach when the panel is focused, dim otherwise.
func withScrollbar(vp viewport.Model, panelWidth int, focused bool) string {
	left := lipgloss.NewStyle().Width(max(panelWidth-1, 1)).Render(vp.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, scrollColumn(vp, focused))
}

func scrollColumn(vp viewport.Model, focused bool) string {
	height := vp.Height
	if height <= 0 {
		return ""
	}
	rows := make([]string, height)
	for i := range rows {
		rows[i] = " "
	}
	total := vp.TotalLineCount()
	if total > height {
		thumbStyle := ui.ScrollThumbDimStyle
		if focused {
			thumbStyle = ui.ScrollThumbStyle
		}
		thumb := max(height*height/total, 1)
		pos := 0
		if maxOff := total - height; maxOff > 0 {
			pos = vp.YOffset * (height - thumb) / maxOff
		}
		for i := pos; i < pos+thumb && i < height; i++ {
			// Right half block: a half-width thumb hugging the panel border.
			rows[i] = thumbStyle.Render("▐")
		}
	}
	return strings.Join(rows, "\n")
}

// renderCard is the card text alone — the shape ~30 SetContent call sites want.
// The clickable-link index is built by buildCard and only handleMouse needs it.
func (m Model) renderCard() string {
	s, _ := m.buildCard()
	return s
}

// buildCard renders the brief panel and, alongside it, the index of its
// clickable lines: 0-based content-line number → URL. Line heights vary
// (dividers are 3 lines, about/note/changelog bodies wrap), so the indices are
// recorded while writing — strings.Count(sb, "\n") immediately before the line
// is the line's own index — rather than reconstructed by parsing the result.
// bubbles/viewport is line-based (no soft wrap), so a content-line index is a
// viewport line index, which is what the mouse handler maps a click row to.
func (m Model) buildCard() (string, map[int]string) {
	links := make(map[int]string)
	if len(m.meta) == 0 {
		return ui.DescStyle.Render("no tools tracked.\npress t to add one."), links
	}

	t, ok := m.selectedTool()
	if !ok {
		return ui.DescStyle.Render("select a tool from the left panel."), links
	}

	inner := max(m.briefW-2, 1)

	var sb strings.Builder

	card, hasCard := m.repoCards[t.Name]

	// Title line: tool name (bold orange) + about (gray italic). Name is always
	// shown; about is appended when available.
	name := t.Name
	maxNameLen := 30
	if utf8.RuneCountInString(name) > maxNameLen {
		name = name[:maxNameLen-3] + "..."
	}
	nameRendered := lipgloss.NewStyle().Bold(true).Foreground(ui.ColorOrange).Render(name)
	var title string
	if m.updatingFor == t.Name {
		// While an update is running, the title becomes a status line:
		// "updating <name> <spinner>" (twin of the refresh spinner; the two are
		// mutually exclusive via the [u]/[r] guards). The about is hidden until
		// the update completes.
		title = ui.InfoStyle.Render("updating ") + nameRendered + ui.InfoStyle.Render(" ") + m.spinner.View()
	} else if m.refreshingFor == t.Name {
		// While a force refresh is in flight, the title line becomes a status
		// line: "refreshing <name> data <spinner>" (name keeps its bold style,
		// spinner frames advance on spinner.TickMsg). The about is hidden until
		// the refreshed card lands.
		title = ui.InfoStyle.Render("refreshing ") + nameRendered + ui.InfoStyle.Render(" data ") + m.spinner.View()
	} else {
		title = nameRendered
		if hasCard && card.About != "" {
			aboutWidth := max(inner-utf8.RuneCountInString(name)-3, 20)
			aboutWrapped := wrapText(card.About, aboutWidth)
			title += " — " + ui.MetaNoteStyle.Render(aboutWrapped)
		}
	}
	sb.WriteString(title + "\n")

	// [info] section: repo / stars / installed / latest / languages / repo
	// status. Local detection alone (installed version, no GitHub ref and no
	// card) is enough to open the section.
	vinfo := m.versions[t.Name]
	installed := vinfo.Installed
	hasInfo := t.GitHub != "" || installed != "" ||
		(hasCard && (card.Stars > 0 || card.Latest != "" || len(card.Languages) > 0 || card.RepoStatus != ""))
	if hasInfo {
		sb.WriteString(m.sectionDivider("info"))
		if t.GitHub != "" {
			// Clickable: resolves exactly like the [o] key does.
			links[strings.Count(sb.String(), "\n")] = "https://" + t.GitHub
			sb.WriteString(ui.GithubStyle.Render("repo: "+t.GitHub) + "\n")
			if !hasCard && m.repoStatus[t.Name] == "rate-limited" {
				sb.WriteString(ui.WarnStyle.Render("rate limited — press [L]") + "\n")
			}
		}
		if hasCard && card.Stars > 0 {
			sb.WriteString(ui.InfoStyle.Render(fmt.Sprintf("stars: %s", formatStars(card.Stars))) + "\n")
		}
		// The version-less states resolve only once the local probe reported
		// back (InstalledKnown) — before that an empty Installed just means the
		// detection is still in flight. Present-but-version-less is a working
		// install (a TUI app that ignores --version), so it reads green and
		// affirmative; only a genuine absence gets the red ✕.
		// A resolved version carries the same U+F412 (nf-oct-tag) glyph the
		// latest: line uses — here it marks a version, not specifically a
		// release tag; the two version lines read as a pair. The states with no
		// version to tag stay bare. Written as a \u escape on purpose — the raw
		// glyph is invisible in most editors and diffs and gets silently lost.
		switch {
		case installed != "":
			sb.WriteString(ui.InfoStyle.Render("installed: \uf412 "+installed) + "\n")
		case vinfo.InstalledKnown && vinfo.InstalledPresent:
			sb.WriteString(ui.InfoStyle.Render("installed: ") +
				ui.OkStyle.Render("✓") +
				ui.InfoStyle.Render(" no version") + "\n")
		case vinfo.InstalledKnown:
			sb.WriteString(ui.InfoStyle.Render("installed: ") +
				ui.DangerStyle.Render("✕") +
				ui.InfoStyle.Render(" not installed") + "\n")
		default:
			sb.WriteString(ui.InfoStyle.Render("installed: detecting…") + "\n")
		}
		if hasCard {
			if card.Latest != "" {
				var suffix string
				if card.PublishedAt != "" {
					date := card.PublishedAt
					if len(date) > 10 {
						date = date[:10]
					}
					suffix = " (" + date + ")"
				}
				if m.hasUpdate(t.Name) {
					sb.WriteString(ui.InfoStyle.Render("latest: ") +
						ui.UpdateAvailableStyle.Render(" "+card.Latest+" ↑") +
						ui.InfoStyle.Render(suffix) + "\n")
				} else {
					sb.WriteString(ui.InfoStyle.Render("latest:  "+card.Latest+suffix) + "\n")
				}
			}
			if len(card.Languages) > 0 {
				label := "languages: "
				bar := renderLangBar(card.Languages, inner, utf8.RuneCountInString(label))
				sb.WriteString(ui.InfoStyle.Render(label) + bar + "\n")
			}
			if card.RepoStatus != "" {
				sb.WriteString(ui.InfoStyle.Render("maintenance:") + " " + renderRepoStatus(card.RepoStatus) + "\n")
			}
		}
	}

	// [notes] section: status / note / tags (with inline editing via e/t).
	if mt, ok := m.selectedMeta(); ok {
		sb.WriteString(m.sectionDivider("notes"))
		sym := loader.StatusSymbol[mt.Status]
		symStyled := ui.StatusStyle(mt.Status).Render(sym + " " + string(mt.Status))
		sb.WriteString(ui.MetaDetailLabelStyle.Render("status:") + " " + symStyled + "\n")

		if m.mode == modeEditNote {
			sb.WriteString(ui.MetaDetailLabelStyle.Render("note:") + " " + m.noteInput.View() + "\n")
		} else {
			noteText := mt.Note
			if noteText == "" {
				noteText = "— (press e to edit)"
			}
			wrapped := wrapText(noteText, inner)
			sb.WriteString(ui.MetaDetailLabelStyle.Render("note:") + " " + ui.MetaNoteStyle.Render(wrapped) + "\n")
		}

		if m.mode == modeEditTags {
			sb.WriteString(ui.MetaDetailLabelStyle.Render("tags:") + " " + m.tagsInput.View() + "\n")
		} else {
			tagsText := tagOf(mt)
			if tagsText == "" {
				tagsText = "— (press t to edit)"
			}
			wrapped := wrapText(tagsText, inner)
			sb.WriteString(ui.MetaDetailLabelStyle.Render("tags:") + " " + ui.MetaTagStyle.Render(wrapped) + "\n")
		}
	}

	// [changelog] section (only when there is content to show).
	var changelogContent, changelogURL string
	if m.changelogLoadingFor == t.Name {
		changelogContent = ui.DescStyle.Render("loading changelog...") + "\n"
	} else if data, ok := m.changelogData[t.Name]; ok {
		changelogContent = m.renderChangelogBlock(data)
		// The block leads with the release URL only on a successful fetch with
		// one — that is the line to register, and it is derived from the same
		// data the block rendered from, so the two cannot drift.
		if data.err == nil {
			changelogURL = data.htmlUrl
		}
	} else if t.GitHub != "" {
		changelogContent = ui.DescStyle.Render("loading changelog...") + "\n"
	}
	if changelogContent != "" {
		sb.WriteString(m.sectionDivider("changelog"))
		if changelogURL != "" {
			// Registered after the divider, before the block: the URL is the
			// block's first line. Note this is the release's own page, not the
			// [c] key's <repo>/releases index.
			links[strings.Count(sb.String(), "\n")] = changelogURL
		}
		sb.WriteString(changelogContent)
	}

	return sb.String(), links
}

// renderRepoStatus highlights the maintenance state of the upstream repo: a
// green dot for an active repo, a yellow warning sign for an archived one.
func renderRepoStatus(status string) string {
	switch status {
	case "active":
		return lipgloss.NewStyle().Foreground(ui.StatusColorActive).Render("● active")
	case "archived":
		return lipgloss.NewStyle().Foreground(ui.StatusColorTrying).Render("⚠ archived")
	default:
		return ui.RepoStatusStyle.Render(status)
	}
}

// sectionDivider renders a labeled section header that spans the panel's content
// width, e.g. "[info] ───────────". The label is rendered only by callers when
// the section actually has content, so no empty dividers are produced.
func (m Model) sectionDivider(label string) string {
	tag := "[" + label + "] "
	// briefW-1 to leave room for the scrollbar gutter; leading blank line adds
	// breathing room between sections.
	dashes := max(m.briefW-1-utf8.RuneCountInString(tag), 0)
	// Blank line above and below the header for breathing room.
	return "\n" + ui.SectionLabelStyle.Render(tag) +
		lipgloss.NewStyle().Foreground(ui.ColorBorder).Render(strings.Repeat("─", dashes)) + "\n\n"
}

// changelogRenderCache memoizes the last markdown conversion. The whole card
// is rebuilt on every spinner frame while a refresh or an update runs (see the
// spinner.TickMsg handler), so the body of a large release note would go
// through the converter's regexes ~12 times a second to animate one glyph.
// Same shape as readmeRenderCache, one entry keyed by (body, width) — the two
// inputs markdownToLines has.
//
// It hangs off Model as a POINTER so a value receiver can still fill it, and
// its method tolerates a nil receiver: most tests build Model{} literals and
// must keep working without New().
type changelogRenderCache struct {
	body  string
	width int
	out   []mdLine
	ok    bool
}

func (c *changelogRenderCache) lines(body string, width int) []mdLine {
	if c == nil {
		return markdownToLines(body, width)
	}
	if c.ok && c.width == width && c.body == body {
		return c.out
	}
	out := markdownToLines(body, width)
	*c = changelogRenderCache{body: body, width: width, out: out, ok: true}
	return out
}

func (m Model) renderChangelogBlock(msg changelogMsg) string {
	if msg.err != nil {
		return ui.InfoStyle.Render("changelog unavailable: "+msg.err.Error()) + "\n"
	}
	var sb strings.Builder
	// Only the link to the tag + the changelog text; version/date are already
	// shown in [info]. Unified muted style (InfoStyle), same as the [info] text.
	if msg.htmlUrl != "" {
		sb.WriteString(ui.InfoStyle.Render(msg.htmlUrl) + "\n\n")
	}
	// markdownToLines wraps as it converts (hanging indents need the block
	// structure), so styling lands on whole finished lines — the only way to
	// keep ANSI out of the rune-based wrap math. An empty result covers both
	// an empty body and one the converter consumed whole (all comments, all
	// separators).
	lines := m.changelogRender.lines(msg.body, max(m.briefW-2, 10))
	if len(lines) == 0 {
		sb.WriteString(ui.InfoStyle.Render("no release notes available.") + "\n")
		return sb.String()
	}
	for _, line := range lines {
		switch {
		case line.text == "":
			// A blank separator carries no text to color; rendering it would
			// only emit an empty escape pair.
			sb.WriteString("\n")
		case line.kind == mdHeading:
			sb.WriteString(ui.ChangelogHeadingStyle.Render(line.text) + "\n")
		default:
			sb.WriteString(ui.InfoStyle.Render(line.text) + "\n")
		}
	}
	return sb.String()
}

// panelRow converts a click's screen Y into a 0-based row inside a panel's
// viewport, or -1 when the click is not on the viewport at all. Row 0 is the
// outer top margin, row 1 the panel's top border, row 2 the viewport's first
// row; anything from the viewport's bottom edge down (the panel's lower border,
// the status and hints bars) is outside too. Both panels share it so a stray Y
// can never be mapped onto content — with an offset viewport, a click on the
// chrome would otherwise resolve to a real list row or card link.
func panelRow(y, vpHeight int) int {
	row := y - 2
	if row < 0 || row >= vpHeight {
		return -1
	}
	return row
}

func (m Model) hasUpdate(toolName string) bool {
	vi, ok := m.versions[toolName]
	return ok && version.IsNewer(vi.Installed, vi.Latest)
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Mouse policy: wheel scrolls in every mode, clicks (selection/focus) only
	// in modeNormal — while an input mode owns the keyboard a click must not
	// move selectedMeta() under it (wrong-target note/tags/rename commits).
	// Under any overlay nothing may move: scrolling there is invisible.
	if !m.ready || m.overlayVisible() {
		return m, nil
	}
	clickable := m.mode == modeNormal

	// Panels sit flush (each is panelW+2 wide incl. borders) with no outer
	// horizontal margin, so screen X maps directly to panel spans.
	toolsPanelEnd := m.toolsW + 2
	briefPanelEnd := toolsPanelEnd + m.briefW + 2

	// X alone does not mean "inside a panel": the outer Margin(1,0) row, the
	// panel borders and the status/hints bars share the panels' columns. Rows
	// outside the viewport must not be mapped onto content — a stray Y there
	// once resolved to a card link and opened the browser from a click on empty
	// chrome.

	// Detect which panel the click is in
	var cmd tea.Cmd
	if msg.X < toolsPanelEnd {
		// Left panel (Tools)
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && clickable {
			// Any click in the panel focuses it, matching brief/help.
			m.setFocus(focusTools)
			// The row is a screen line: under grouping it maps back through
			// lineTool, and a click on a header row selects nothing.
			toolIdx := -1
			if row := panelRow(msg.Y, m.toolsViewport.Height); row >= 0 {
				toolIdx = m.toolAtLine(row + m.toolsViewport.YOffset)
			}
			filtered := m.filteredMeta()
			if toolIdx >= 0 && toolIdx < len(filtered) && m.metaSelected != toolIdx {
				// Mirror the keyboard j/k path (shared selectMeta helper,
				// including the auto-fetch).
				return m, m.selectMeta(toolIdx)
			}
		} else if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			m.toolsViewport, cmd = m.toolsViewport.Update(msg)
		}
	} else if msg.X < briefPanelEnd {
		// Middle panel (Brief)
		switch msg.Button {
		case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
			m.briefViewport, cmd = m.briefViewport.Update(msg)
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress && clickable {
				m.setFocus(focusBrief)
				// Clickable links: the repo line and the changelog release URL
				// open in the browser, like the [o] and [c] keys. The index is
				// recomputed here rather than cached in the model — it is one
				// card render per click and can never go stale against the
				// content actually on screen.
				if row := panelRow(msg.Y, m.briefViewport.Height); row >= 0 {
					line := row + m.briefViewport.YOffset
					if _, links := m.buildCard(); links[line] != "" {
						return m, openURLCmd(links[line])
					}
				}
			}
		}
	} else {
		// Right panel (Help)
		switch msg.Button {
		case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
			m.helpViewport, cmd = m.helpViewport.Update(msg)
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress && clickable {
				m.setFocus(focusHelp)
			}
		}
	}
	return m, cmd
}

// applySpotlight dims every line outside the current navigation entry while
// the [3] cursor is active: the entry's lines keep their full colorizeHelp
// styling, the rest are stripped of their own coloring and repainted with
// ui.HelpDimStyle — the same strip-then-repaint trick the [L] overlay uses,
// but per whole line, so no ANSI-safe splicing is needed. A stale
// out-of-bounds index renders undimmed rather than panicking (entries and
// cursor are reset together in setHelpContent, but a value-receiver render
// must not trust that).
func (m Model) applySpotlight(text string) string {
	if m.helpNavIdx < 0 || m.helpNavIdx >= len(m.helpEntries) {
		return text
	}
	e := m.helpEntries[m.helpNavIdx]
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i < e.start || i >= e.end {
			lines[i] = ui.HelpDimStyle.Render(stripANSI(line))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) rawHelpText() string {
	mt, ok := m.selectedMeta()
	if !ok {
		return ""
	}
	// README content is not a probe capture and lives in readmeData, not the
	// [2]string helpCache — indexing it with helpModeReadme would panic.
	if m.helpMode == helpModeReadme {
		return ""
	}
	cached, has := m.helpCache[mt.Name]
	if !has {
		return ""
	}
	return cached[m.helpMode]
}

// readmeContent returns the rendered README for the selected tool plus a flag
// telling whether it is real content (false = the string is a placeholder).
func (m Model) readmeContent(name string) (string, bool) {
	data, ok := m.readmeData[name]
	// helpBase must be non-empty too: glamour renders a README that carries no
	// visible markdown (HTML comments, badges-only whitespace) to an empty
	// string, and returning that as real content paints a blank panel with no
	// way out. Such a render falls through to the generic placeholder below.
	if ok && data.content != "" && m.helpBase != "" {
		return m.helpBase, true
	}
	t, found := m.toolByName(name)
	if !found || t.GitHub == "" {
		return "No repo for " + name + ".\nPress [h] for --help.", false
	}
	if !ok {
		return "Loading...", false
	}
	switch {
	case errors.Is(data.err, version.ErrNoReadme):
		return "No README in " + t.GitHub + ".\nPress [h] for --help.", false
	case errors.Is(data.err, version.ErrRateLimited):
		return "rate limited — press [L]", false
	}
	return "No README for " + name + ".\nPress [h] for --help.", false
}

func (m Model) renderHelpContent() string {
	// Live update log: while a tool is (or was just) being updated, [3] shows the
	// merged stdout+stderr buffer instead of help. This branch sits ahead of the
	// helpLoadingFor/cache branches so re-selecting the updating tool never paints
	// "Loading..." (autoFetchCmdsForSelected also skips the help fetch, so no late
	// helpOutputMsg clobbers it), and ahead of the "no tool selected" guard: a
	// self-update's log is not tied to a selection at all — keepkit is typically
	// untracked and the tracker may even be empty. The buffer survives until the
	// next update starts.
	if m.showsUpdateLog() {
		if len(m.updateLog) == 0 {
			return ui.MetaNoteStyle.Render("starting update…")
		}
		return wrapText(strings.Join(m.updateLog, "\n"), m.helpWrapWidth())
	}

	mt, ok := m.selectedMeta()
	if !ok {
		return ui.MetaNoteStyle.Render("No tool selected")
	}

	// README mode: content comes from readmeData (glamour-rendered into helpBase
	// by setHelpContent), never from the [2]string helpCache. Placed after the
	// update-log branch, which keeps priority, and ahead of every helpCache
	// index — mode 2 is out of range for that array.
	if m.helpMode == helpModeReadme {
		text, real := m.readmeContent(mt.Name)
		if !real {
			return ui.MetaNoteStyle.Render(text)
		}
		return text
	}

	// Gate per tool, not on "any fetch in flight": another tool's fetch may
	// still be running (fast j/k, search arrows then esc/enter) while the
	// selected tool's help is already cached — that cache must render, or the
	// panel would stick on "Loading..." when the stale fetch lands unselected.
	if m.helpLoadingFor == mt.Name {
		return ui.MetaNoteStyle.Render("Loading...")
	}

	cached, has := m.helpCache[mt.Name]
	if !has || cached[m.helpMode] == "" {
		if m.helpMode == helpModeHelp {
			return ui.MetaNoteStyle.Render("Press [h] for --help\nPress [m] for man page")
		}
		return ui.MetaNoteStyle.Render("Press [m] for man page\nPress [h] for --help")
	}
	if m.mode != modeHelpSearch || m.helpSearch.Value() == "" {
		// Cursor moves and clear-cursor repaints hit this path once per
		// keystroke: reuse the base cached by setHelpContent instead of
		// re-running the colorize regex over a whole man page each time.
		// The fallback covers renders on models that haven't gone through
		// setHelpContent (direct test construction).
		if m.helpBase != "" {
			return m.applySpotlight(m.helpBase)
		}
		return m.applySpotlight(colorizeHelp(wrapText(cached[m.helpMode], m.helpWrapWidth())))
	}
	text := wrapText(cached[m.helpMode], m.helpWrapWidth())
	query := m.helpSearch.Value()
	lines := strings.Split(text, "\n")
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = highlightMatch(line, query)
	}
	return strings.Join(result, "\n")
}
