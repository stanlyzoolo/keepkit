package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

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
		layout = ui.PlaceOverlay(layout, fg, m.sty().OverlayDim)
	}
	// Vertical margin only; no horizontal margin so panels/status bar reach the
	// terminal edges.
	return lipgloss.NewStyle().Margin(1, 0).Render(layout)
}

func (m Model) renderStatusBar() string {
	s := m.sty()
	style := s.StatusBar.Width(m.width - 2)
	if m.mode == modeHelpSearch {
		matchInfo := ""
		if len(m.helpMatches) > 0 {
			matchInfo = fmt.Sprintf("  %d/%d matches", m.helpMatchIdx+1, len(m.helpMatches))
		} else if m.helpSearch.Value() != "" {
			matchInfo = "  no matches"
		}
		return style.Render(fmt.Sprintf(
			"%s %s  %s  %s  %s%s",
			s.AccentBold.Render("/"),
			m.helpSearch.View(),
			m.hint("n", "next"),
			m.hint("N", "prev"),
			m.hint("esc", "exit"),
			matchInfo,
		))
	}
	if m.mode == modeSearch {
		return style.Render(fmt.Sprintf(
			"%s %s  %d/%d  %s  %s  %s",
			s.AccentBold.Render("/"),
			m.search.View(),
			len(m.searchMatches()),
			len(m.meta),
			m.hint("enter", "open"),
			m.hint("↑/↓", "move"),
			m.hint("esc", "cancel"),
		))
	}
	if m.mode == modeEditNote {
		return style.Render(m.hint("enter", "save") + "  " + m.hint("esc", "cancel"))
	}
	if m.mode == modeEditTags {
		return style.Render(m.hint("enter", "save") + "  " + m.hint("esc", "cancel") + "  " + s.Note.Render("single tag"))
	}
	if m.mode == modeTrack {
		return style.Render(fmt.Sprintf(
			"%s %s  %s",
			s.AccentBold.Render("track (github url or tool name):"),
			m.trackInput.View(),
			m.hint("esc", "cancel"),
		))
	}
	if m.mode == modeConfirmUntrack {
		return style.Render(fmt.Sprintf(
			"%s  %s  %s",
			s.AccentBold.Render("untrack "+m.untrackTarget+"?"),
			m.hint("enter", "yes"),
			m.hint("esc", "no"),
		))
	}
	if m.mode == modeRename {
		return style.Render(fmt.Sprintf(
			"%s %s  %s",
			s.AccentBold.Render("rename to:"),
			m.nameInput.View(),
			m.hint("esc", "cancel"),
		))
	}
	if m.mode == modeRunInput {
		name := ""
		if mt, ok := m.selectedMeta(); ok {
			name = mt.Name
		}
		return style.Render(fmt.Sprintf(
			"%s %s  %s  %s",
			s.AccentBold.Render("run "+name+":"),
			m.runInput.View(),
			m.hint("enter", "run"),
			m.hint("esc", "cancel"),
		))
	}
	if m.mode == modeConfirmUpdate {
		// The name comes from the plan's own target, not the selection: for
		// keepkit's own update the selection is a foreign tool — or nothing at
		// all, with an empty tracker — and for a tool's it may have moved while
		// detection ran.
		return style.Render(fmt.Sprintf(
			"%s  %s  %s",
			s.AccentBold.Render("update "+m.updateTarget+": "+m.updatePlan.Display),
			m.hint("enter", "run"),
			m.hint("esc", "cancel"),
		))
	}
	if m.mode == modeTokenInput {
		return style.Render(m.hint("enter", "validate & save") + "  " + m.hint("esc", "cancel"))
	}
	if m.mode == modeAPIStatus {
		return style.Render(m.hint("r", "refresh") + "  " + m.hint("esc", "close"))
	}
	if m.mode == modeHotkeys {
		return style.Render(m.hint("esc", "close"))
	}
	if m.statusMsg != "" {
		return style.Render(s.AccentBold.Render(m.statusMsg))
	}
	// The self-update banner replaces the hint cells in all three normal focus
	// states — one check here rather than one per focus branch, since those are
	// the only paths left below. A transient statusMsg outranks it (branch
	// above) and expires on its own after statusMsgTTL.
	if cells, ok := m.selfBannerCells(); ok {
		return m.renderHintsBar(style, cells)
	}
	return m.renderHintsBar(style, m.globalHints())
}

// globalHints is the status bar's left half, and it is deliberately the SAME in
// every focus. It used to be three per-focus lists, which meant the one line
// always on screen kept rewriting itself as the user moved between panels while
// the keys it advertised were mostly reachable from anywhere anyway. What is
// genuinely panel-local now lives in that panel's own footer, next to the thing
// it acts on; what is left here is the tracker's global verb list.
//
// No space/group cell: it is [1]-local and its footer carries it. Cells are
// ordered most-important-first — renderHintsBar drops from the right.
func (m Model) globalHints() []string {
	return []string{
		m.hint("enter", "run"),
		m.hint("/", "search"),
		m.hint("t", "track"),
		m.hint("u", "untrack"),
		m.hint("R", "rename"),
		m.hint("L", "api"),
		m.hint("?", "keys"),
		m.hint("q", "quit"),
	}
}

// updateCountCell is the status bar's right-hand answer to "is there anything
// to do?": "3 updates ↑" in the signal color, or "" when everything is current.
// It counts the whole tracker, not the filtered view — a search narrowing the
// list must not make pending updates disappear from the one place that totals
// them.
func (m Model) updateCountCell() string {
	n := m.updateCount()
	if n == 0 {
		return ""
	}
	word := "updates"
	if n == 1 {
		word = "update"
	}
	return m.sty().Signal.Render(fmt.Sprintf("%d %s ↑", n, word))
}

// updateCount is how many tracked tools have a newer release than the version
// installed locally. It drives both the [1] panel title and the status bar, so
// the two can never disagree.
func (m Model) updateCount() int {
	n := 0
	for _, mt := range m.meta {
		if m.hasUpdate(mt.Name) {
			n++
		}
	}
	return n
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
// Only then is the right group placed. It has three members — the API-usage
// gauge, the pending-updates count and the collapsed self-update cell — and
// they are dropped in order of how *actionable* they are: the gauge first (a
// read-only reminder, and it only shows up under quota pressure at all), then
// the self cell, and the updates count last, since it is the one thing on the
// bar that says the tracker needs attention. The candidate list spells that
// order out rather than computing it, because "which combination is next"
// is a judgement about meaning, not about width.
func (m Model) renderHintsBar(style lipgloss.Style, cells []string) string {
	inner := m.width - 2
	joined := func(cs []string) int { return lipgloss.Width(strings.Join(cs, hintSep)) }
	// join drops empty members so a candidate never renders a stray separator;
	// an all-empty candidate collapses to "" and place() skips it.
	join := func(parts ...string) string {
		kept := parts[:0]
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		return strings.Join(kept, hintSep)
	}
	full, compact := m.renderRateGauge(false), m.renderRateGauge(true)
	upd, self := m.updateCountCell(), m.selfCompactCell()

	// The two undroppable members are reserved for before any dropping starts:
	// they live in the right group and the hints have to fit around them. One
	// loop, one rule — a plain "fit the hints" pass ahead of it would be dead
	// work whose predicate has to be kept in sync with this one by hand.
	reserve := 0
	if keep := join(upd, self); keep != "" {
		reserve = rateGaugeMinGap + lipgloss.Width(keep)
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
	for _, c := range []string{
		join(full, upd, self),
		join(compact, upd, self),
		join(upd, self),
		join(compact, upd),
		join(upd),
		join(compact, self),
		join(self),
		join(compact),
	} {
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
	s := m.sty()
	if m.selfUpdating() {
		return nil, false
	}
	switch m.selfState {
	case selfOffered:
		return []string{
			selfToolName + " " + s.SignalBold.Render(m.selfLatest) + " available — " + m.hint("U", "update"),
			m.hint("X", "dismiss"),
		}, true
	case selfUpdated:
		return []string{
			selfToolName + " updated — " + m.hint("U", "restart"),
			m.hint("X", "later"),
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
	s := m.sty()
	if m.selfUpdating() {
		return selfToolName + " updating…"
	}
	switch m.selfState {
	case selfDismissed:
		return selfToolName + " " + s.SignalBold.Render("↑") + " " + m.key("U")
	case selfUpdatedLater:
		return selfToolName + " " + m.hint("U", "restart")
	case selfNone, selfOffered, selfUpdated:
		// Nothing to collapse: no banner at all, or the full one is on screen.
	default:
		// See selfBannerCells: a new state has to be decided at every site.
	}
	return ""
}

// key renders a hotkey the single way keepkit renders one: the bare key in the
// accent color. The brackets it used to wear are gone — the accent already says
// "this is a key", and at the status bar's density a pair of brackets per hint
// cost more columns than the whole [?] overlay's third column. Literal brackets
// survive only where they name a *panel* ([3] readme), which is a different
// thing and now the only bracketed thing on screen.
func (m Model) key(k string) string {
	return m.sty().Accent.Render(k)
}

// hint is a key with its label: "enter run", accent then dim. Every status-bar,
// footer and overlay cell is built from it, so the contrast is defined once —
// and it is deliberately a *two-step* contrast: the key is the only thing on a
// hint the eye needs to find, so the word beside it steps back rather than
// competing at reading brightness.
func (m Model) hint(k, label string) string {
	s := m.sty()
	return s.Accent.Render(k) + " " + s.Dim.Render(label)
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

// gaugeDangerRemaining is where the gauge's fill turns red. It is read against
// the *limit* as well as absolutely, because the two limits differ by two orders
// of magnitude: 100 left is an alarm out of 5000 and most of a token-less window
// out of 60, so an absolute-only bound would paint that user's bar permanently
// red — the opposite of a signal. The bound is the tighter of the two: <100 with
// a token, <15 without one.
const gaugeDangerRemaining = 100

// gaugeVisible reports whether there is a quota to show. The gauge is on screen
// for the whole session once the rate is known: it is also the only place the
// API surface is visible at all, so hiding it at rest hid the [L] overlay with
// it — the numbers are the affordance.
func gaugeVisible(r version.RateLimit) bool {
	return r.Known && r.Limit > 0
}

// renderRateGauge builds the right-corner API-usage indicator for the current
// rate snapshot, or "" when there is nothing to show (see gaugeVisible). It
// shows used/limit (used = Limit-Remaining), matching the API-status overlay.
// The bar is ▮ fill / ░ track glyphs — foreground-colored, so it survives a
// stripped-ANSI profile, and any usage shows at least one filled cell via
// gaugeFilled. The fill turns danger-red as the window runs out; the ⚠/✕ alarm
// itself still lives only in the [L] overlay, which the [?] overlay documents —
// the bar spends no columns advertising a key. compact drops the bar, keeping
// "api used/limit" for narrow terminals.
func (m Model) renderRateGauge(compact bool) string {
	s := m.sty()
	r := m.rate
	if !gaugeVisible(r) {
		return ""
	}
	used := usedOf(r)
	label := s.Dim.Render("api ")
	nums := s.Dim.Render(fmt.Sprintf("%d/%d", used, r.Limit))
	if compact {
		return label + nums
	}
	fill := s.GaugeFill
	if r.Remaining < min(gaugeDangerRemaining, r.Limit/4) {
		fill = s.Danger
	}
	filled := gaugeFilled(used, r.Limit)
	bar := fill.Render(strings.Repeat(gaugeFillGlyph, filled)) +
		s.GaugeTrack.Render(strings.Repeat(gaugeTrackGlyph, gaugeCells-filled))
	return label + bar + " " + nums
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
func rateIcon(s *ui.Styles, rate version.RateLimit) string {
	switch classifyRate(rate) {
	case rateExhausted:
		return s.Danger.Render("✕")
	case rateWarn:
		return s.SignalBold.Render("⚠")
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
	s := m.sty()
	var b strings.Builder
	b.WriteString(s.EmphasisBold.Render("github api usage") + "\n\n")

	source := version.TokenSource()
	// Nudge the user to add a token when none is configured — it lifts the hourly
	// limit from 60 to 5000. Hidden once a token exists or while entering one.
	if source == "none" && m.mode != modeTokenInput {
		b.WriteString(s.Signal.Render("add a github token to raise the limit (60 → 5000/h)  ") + m.hint("e", "set") + "\n\n")
	}

	tokenLine := "token  " + source
	if tok := version.Token(); tok != "" {
		tokenLine += " (" + maskToken(tok) + ")"
	}
	b.WriteString(s.Text.Render(tokenLine) + "\n")

	if m.rate.Known {
		icon := rateIcon(s, m.rate)
		// Used/limit matches the status-bar gauge so the two surfaces agree.
		usedLine := fmt.Sprintf("used   %d / %d", usedOf(m.rate), m.rate.Limit)
		if icon != "" {
			usedLine = icon + " " + usedLine
		}
		b.WriteString(s.Text.Render(usedLine) + "\n")
		if !m.rate.Reset.IsZero() {
			mins := int(time.Until(m.rate.Reset).Minutes())
			if mins < 0 {
				mins = 0
			}
			b.WriteString(s.Text.Render(fmt.Sprintf(
				"reset  in %d min (%s)", mins, m.rate.Reset.Format("15:04"))) + "\n")
		}
	} else {
		b.WriteString(s.Text.Render("limit  unknown") + "\n")
	}

	if m.mode == modeTokenInput {
		b.WriteString("\n" + s.AccentBold.Render("token ") + m.tokenInput.View() + "\n")
	}
	if m.tokenError != "" {
		b.WriteString(s.Danger.Render(m.tokenError) + "\n")
	}

	b.WriteString("\n")
	var hints string
	if m.mode == modeTokenInput {
		hints = m.hint("enter", "validate & save") + "  " + m.hint("esc", "cancel")
	} else {
		hints = m.hint("e", "set token") + "  "
		if source == "config" {
			hints += m.hint("d", "remove token") + "  "
		}
		hints += m.hint("r", "refresh") + "  " + m.hint("esc", "close")
	}
	b.WriteString(s.Text.Render(hints))

	return s.OverlayBorder.Render(b.String())
}

// The [?] overlay's spacing. The grid is two columns of a dozen-odd short
// rows, and at the minimum gaps it read as a wall of text — the room to fix
// that is horizontal, since the row budget is fixed by the 80x24 background
// while the width budget has ~15 columns of slack.
const (
	hotkeyKeyMinW = 8       // key column floor, before padding to the widest key
	hotkeyKeyGap  = "   "   // between a key and its description
	hotkeyColGap  = "     " // between the two columns
)

// hotkeyRow is one binding line in the [?] overlay: a key cell (bracketed by
// keyHint) and a 1-4 word description.
type hotkeyRow struct{ key, desc string }

// hotkeyGroup is a titled block of binding rows in one overlay column.
type hotkeyGroup struct {
	title string
	rows  []hotkeyRow
}

// renderHotkeys builds the [?] overlay: a title row with the close hint
// right-aligned, then a two-column grid of every normal-mode binding, one per
// line, grouped by the panel or area it belongs to. Inside a column every key
// cell is padded to the column's widest key, so the descriptions line up and
// the keys never drift sideways.
//
// Hard size budget: <= 20 rows x <= 76 cols framed, so it fits the 80x24
// composited background PlaceOverlay clips off the bottom. That leaves 16 rows
// per column, and both columns are AT it — a new binding needs a row freed, not
// appended, and TestRenderHotkeysSizeBudget is what says so. Rows that were
// merged to buy that room (o/c, j/k ↑/↓) are the reason the descriptions read
// as pairs.
//
// The close hint lives in the title rather than on a footer row of its own,
// which is a whole row saved; esc, q and a second ? all close it.
func (m Model) renderHotkeys() string {
	s := m.sty()
	// padTo right-pads a (possibly ANSI-styled) cell to a visible width so the
	// descriptions line up; measured with lipgloss.Width, which strips ANSI.
	padTo := func(str string, w int) string {
		if d := w - lipgloss.Width(str); d > 0 {
			return str + strings.Repeat(" ", d)
		}
		return str
	}

	// renderColumn stacks groups vertically: a blank line separates groups
	// (none above the first), the header sits directly on top of its rows, and
	// every key cell is padded to the column's widest key so every description
	// in the column starts at the same offset.
	renderColumn := func(groups []hotkeyGroup) string {
		// The key column is padded to a floor, not just to the widest key: a
		// column of one- and two-character keys would otherwise put its
		// descriptions right against them, which is exactly what made the
		// overlay read as cramped.
		keyW := hotkeyKeyMinW
		for _, g := range groups {
			for _, r := range g.rows {
				if w := lipgloss.Width(r.key); w > keyW {
					keyW = w
				}
			}
		}
		var lines []string
		for gi, g := range groups {
			if gi > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, s.EmphasisBold.Render(g.title))
			for _, r := range g.rows {
				lines = append(lines, padTo(s.Accent.Render(r.key), keyW)+hotkeyKeyGap+s.Dim.Render(r.desc))
			}
		}
		return strings.Join(lines, "\n")
	}

	col1 := renderColumn([]hotkeyGroup{
		{"global", []hotkeyRow{
			{"1/2/3", "focus panel"},
			{"←/→", "move focus"},
			{"esc/q", "back / quit"},
			{"/", "search"},
			{"t", "track a repo"},
			{"u", "untrack selected"},
			{"R", "rename selected"},
			{"L", "api usage"},
			{"?", "this overlay"},
		}},
		{"[1] tools", []hotkeyRow{
			{"j/k", "move selection"},
			{"g/G", "first / last"},
			{"space", "group by tag"},
			{"enter", "run in a tab"},
		}},
	})

	// The self group is the one state-dependent part of the overlay: U and X are
	// bound only while a banner is on screen, and the two live states bind them
	// differently (update/dismiss vs restart/later). Listing one fixed wording
	// would document half the state machine and, on a build with no banner at
	// all (any dev build), advertise two dead keys.
	groups2 := []hotkeyGroup{
		{"[2] brief", []hotkeyRow{
			{"enter", "update to latest"},
			{"r", "refresh"},
			{"e", "edit note"},
			{"#", "edit tags"},
			{"s", "cycle status"},
			{"o/c", "repo / releases"},
		}},
		{"[3] readme", []hotkeyRow{
			{"r/h/m", "readme/help/man"},
			{"j/k ↑/↓", "navigate / scroll"},
			{"ctrl+d/u", "half page"},
		}},
	}
	switch m.selfState {
	case selfOffered, selfDismissed:
		groups2 = append(groups2, hotkeyGroup{"self", []hotkeyRow{
			{"U", "self-update"},
			{"X", "dismiss"},
		}})
	case selfUpdated, selfUpdatedLater:
		groups2 = append(groups2, hotkeyGroup{"self", []hotkeyRow{
			{"U", "restart"},
			{"X", "later"},
		}})
	case selfNone:
		// No banner, so U/X are unbound and the group would document dead keys.
	default:
		// See selfBannerCells: a new state has to be decided at every site.
	}
	col2 := renderColumn(groups2)

	grid := lipgloss.JoinHorizontal(lipgloss.Top, col1, hotkeyColGap, col2)

	// Title row: the overlay's name, the running version beside it (the one
	// question a keys sheet is also asked), and the close hint pinned right.
	title := s.EmphasisBold.Render("keys")
	if m.appVersion != "" {
		title += " " + s.Dim.Render(selfToolName+" "+m.appVersion)
	}
	closeHint := m.hint("esc", "close")
	titleRow := title + " " + closeHint
	if pad := lipgloss.Width(grid) - lipgloss.Width(title) - lipgloss.Width(closeHint); pad > 0 {
		titleRow = title + strings.Repeat(" ", pad) + closeHint
	}

	body := lipgloss.JoinVertical(lipgloss.Left, titleRow, "", grid)
	return s.OverlayBorder.Render(body)
}

func (m Model) calcVpHeight() int {
	// Match the panel's inner content height. lipgloss adds borders outside the
	// configured Height, so Height(m.height-7) gives exactly m.height-7 content
	// rows; the viewport plus its footer must fill them so the scrollbar reaches
	// the bottom.
	return max(m.height-7, 1)
}

// panelFooterRows is how many of a panel's content rows belong to its footer: a
// spacer (a rule in [1], a blank line elsewhere) plus the footer line itself.
// All three panels reserve the same number, which is what keeps their viewports
// the same height and their bottom edges aligned.
//
// Zero on a terminal too short to spare them — two of six content rows is a
// third of the panel, and a footer is a reminder while the content is the point.
const panelFooterRows = 2

func (m Model) footerRows() int {
	if m.calcVpHeight() < 6 {
		return 0
	}
	return panelFooterRows
}

// calcListHeight is the viewport height inside a panel: the content height its
// footer does not claim. Both the WindowSizeMsg handler (which sizes the
// viewports) and the renderers (which stack viewport + footer back to the full
// height) go through it, so the two cannot drift apart and leave a panel one row
// too tall — which lipgloss would answer by pushing the status bar off screen.
func (m Model) calcListHeight() int {
	return max(m.calcVpHeight()-m.footerRows(), 1)
}

// panelFooter renders a panel's footer block — a blank spacer row plus the
// hints — to exactly footerRows() lines and w cells wide, or "" when the panel
// is too short for one. left cells are joined by " · " and dropped from the
// right until they fit (same rule as the status bar, and for the same reason: a
// footer that wrapped would push the panel past its height); right is pinned to
// the far edge and dropped first, since it is the least important thing in the
// block. Both ends sit one cell in from the panel frame, the same gutter the
// list's group headers keep — content that touches its own border reads as
// having overflowed it.
//
// The spacer is blank in every panel: [1] carried a border-colored rule for a
// while, and it only made the footer look like a fourth section of the list
// rather than the frame's own caption.
func (m Model) panelFooter(w int, left []string, right string) string {
	if m.footerRows() == 0 {
		return ""
	}
	inner := max(w-2*panelGutter, 1)

	fit := func(cells []string, reserve int) string {
		for len(cells) > 0 {
			line := strings.Join(cells, footerSep)
			if lipgloss.Width(line)+reserve <= inner {
				return line
			}
			cells = cells[:len(cells)-1]
		}
		return ""
	}

	line := fit(left, rateGaugeMinGap+lipgloss.Width(right))
	if right != "" {
		if gap := inner - lipgloss.Width(line) - lipgloss.Width(right); gap >= rateGaugeMinGap {
			line += strings.Repeat(" ", gap) + right
		} else if line == "" {
			line = truncateToWidth(stripANSI(right), inner)
		}
	}
	if lipgloss.Width(line) > inner {
		line = truncateToWidth(stripANSI(line), inner)
	}
	gutter := strings.Repeat(" ", panelGutter)
	return "\n" + gutter + line
}

// panelGutter is the blank column a panel keeps between its frame and its own
// chrome — the list's group headers and every footer. Content that touches the
// border it lives in reads as having overflowed it. Tool rows keep their own,
// wider indent (toolRowIndent), and the selected row's fill deliberately breaks
// out of the gutter to span the panel edge to edge.
const panelGutter = 1

// footerSep separates two cells in a panel footer. The status bar uses two
// spaces; a footer has fewer cells and more room around them, so the middot
// reads as grouping rather than as clutter.
const footerSep = " · "

func (m Model) calcPanelWidths() (toolsW, briefW, helpW int) {
	// 20%-46%-34% layout: the card is the panel the redesign gives the room to,
	// because its metrics strip lays four measurements side by side and the
	// readme beside it is prose that reads fine in a narrower column.
	// lipgloss adds borders OUTSIDE the configured Width,
	// so Width(panelW) renders as panelW+2 on screen, and panel content fills
	// the full panelW (dividers/viewports use panelW, not panelW-2).
	// Horizontal overhead reserved here = 6: 2 border cols x 3 panels. There is
	// no outer horizontal margin and panels sit flush against each other.
	available := max(m.width-6, 1)
	toolsW = max((available*20)/100, 15)
	briefW = max((available*46)/100, 30)
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

// tagHeaderLine renders a group header as a label followed by a rule running
// out towards the panel edge: " dev ──────── ", one blank column in from the
// frame at both ends (panelGutter). The label is dim and the rule the
// panel-frame gray, so a group boundary reads as structure — the tool names stay
// the only color accent. The trailing group's label is "untagged", the leading
// one's is updatesGroupLabel.
//
// The label leads rather than sits centered because these headers are read down
// a column: a left edge the eye can run along beats a symmetric frame.
//
// One header must occupy exactly one screen line — every consumer of the line
// maps assumes it — so the label is flattened (a tag from a hand-edited
// meta.yaml can carry a newline, which would silently emit a second row) and the
// whole line is built to exactly w cells, so the viewport never wraps or
// truncates it.
func (m Model) tagHeaderLine(tag string) string {
	s := m.sty()
	label := "untagged"
	if tag != "" {
		label = flattenLine(tag)
	}
	w := max(m.toolsW-1, 1)
	// Too narrow for " label ─ " — degrade to the bare label, cut to the panel
	// so it still occupies a single line no wider than w. lipgloss.Width, not a
	// rune count, so a CJK tag is cut by display cells (the same reason
	// truncateToWidth exists).
	if w < 5 {
		return s.Dim.Render(truncateToWidth(label, w))
	}
	gutter := strings.Repeat(" ", panelGutter)
	inner := w - 2*panelGutter
	label = truncateToWidth(label, max(inner-2, 1))
	rule := max(inner-1-lipgloss.Width(label), 0)
	return gutter + s.Dim.Render(label) + s.Rule.Render(" "+strings.Repeat("─", rule)) + gutter
}

// toolRowIndent is how far into a row its name starts: the panel gutter, the
// marker column and one blank. The marker therefore sits in the same column as
// a section header's label, and the names one step in from it — so a group
// reads as a group rather than as a label that happens to sit above some rows.
const toolRowIndent = panelGutter + 2

// updatesGroupLabel heads the group of tools with a pending release. It is the
// tracker's headline — the answer to the question the app exists to answer —
// so in the grouped view it always comes first, ahead of every tag.
const updatesGroupLabel = "updates"

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
// the identity there; the grouped view is the only thing that makes them differ,
// which is what keeps metaSelected a tool index everywhere else.
//
// A row is three columns — marker, name, version — and the version is pinned to
// the right edge, which is what makes the list scannable down two edges at once:
// names on the left, what is installed on the right. The name column absorbs the
// slack between them and is TRUNCATED, never wrapped: a wrapped name would emit
// a second screen line for one tool and every entry in the line maps below it
// would be off by one.
func (m Model) buildToolRows() (string, []int, []int) {
	s := m.sty()
	var sb strings.Builder
	matches := m.searchMatches()
	query := m.searchQuery()
	grouped := m.grouped()
	w := max(m.toolsW-1, 1) // the scrollbar owns the last column

	toolLine := make([]int, len(matches))
	lineTool := make([]int, 0, len(matches))
	line := 0
	prevGroup := ""

	for i, sm := range matches {
		mt := sm.meta

		// Grouped view: open a section whenever the group changes. searchMatches
		// has already made each group's rows contiguous, so one pass emits every
		// header exactly once. The comparison uses the same key the grouping
		// does, so `CLI` and `cli` stay one section — the header then shows the
		// spelling of the group's first tool.
		if grouped {
			key := m.groupKey(mt)
			if i == 0 || key != prevGroup {
				// A blank row above every section but the first: without it the
				// last tool of one group and the header of the next sit on
				// adjacent lines and the list reads as one dense column. The
				// row is a real screen line, so it goes into lineTool as a
				// non-selectable one like the header itself.
				if i > 0 {
					sb.WriteString("\n")
					lineTool = append(lineTool, -1)
					line++
				}
				label := tagOf(mt)
				if key == updatesGroupLabel {
					label = updatesGroupLabel
				}
				sb.WriteString(m.tagHeaderLine(label) + "\n")
				lineTool = append(lineTool, -1)
				line++
			}
			prevGroup = key
		}

		selected := i == m.metaSelected
		// The selected row is a filled block spanning the whole panel, so every
		// segment of it carries the background itself: a lipgloss style applied
		// around already-styled text cannot repaint what is inside it — the
		// inner reset sequences would punch holes in the fill.
		paint := func(st lipgloss.Style) lipgloss.Style {
			if selected {
				return st.Background(s.Theme.Surface)
			}
			return st
		}

		// Version column: what is installed, right-aligned. A tool with a
		// pending release trades the dim for the signal color and gains the ↑
		// — the row's whole job is to say "this one is behind" at a glance.
		// keepkit's own row instead gets a • in the healthy color: it names the
		// binary the user is looking at, which no other row can claim. The name
		// alone decides it — unlike isSelfUpdate, which additionally asks
		// whether the self-update *feature* is live, because this marker is
		// identification, not an offer to act on anything.
		verText, verStyle := m.versions[mt.Name].Installed, paint(s.Dim)
		switch {
		case verText == "":
		case m.hasUpdate(mt.Name):
			verText += " ↑"
			verStyle = paint(s.Signal)
		case mt.Name == selfToolName:
			verText += " •"
			verStyle = paint(s.Ok)
		}
		verW := lipgloss.Width(verText)

		// Marker column (width 1): ⏺ on the selected row — accent while the
		// tools panel is focused, dim otherwise so the selection never
		// disappears when focus moves to brief/help. Non-selected rows get a
		// plain space (no status edge — tool status lives in the brief card).
		// The marker stays visible in modeSearch too: the cursor there is
		// user-controlled (arrows move the highlight through the matches).
		mark := paint(s.Dim).Render(" ")
		nameStyle := paint(s.Text)
		switch {
		case selected && m.focus == focusTools:
			mark = paint(s.Accent).Render("⏺")
			nameStyle = paint(s.AccentBold)
		case selected:
			mark = paint(s.Dim).Render("⏺")
		}

		// Name column: whatever the marker, the indents and the version leave,
		// with at least one blank cell between name and version so the two
		// never touch.
		nameBudget := max(w-toolRowIndent-panelGutter-verW-1, 1)
		name := truncateToWidth(flattenLine(mt.Name), nameBudget)

		// Rows that matched only by tag show the tag that earned them the spot,
		// dimmed, right of the name — skipped when the remaining budget cannot
		// absorb it.
		tagSuffix := ""
		if sm.byTagOnly {
			if suffix := " #" + sm.tag; lipgloss.Width(name)+lipgloss.Width(suffix) <= nameBudget {
				tagSuffix = suffix
			}
		}

		rendered := nameStyle.Render(name)
		// While searching, highlight the matched substring — except on the
		// focused selected row, whose whole name already carries the same
		// accent-bold style (nesting the ANSI would only corrupt it).
		if query != "" && (!selected || m.focus != focusTools) {
			rendered = highlightNameMatch(s, name, query, paint(s.AccentBold), nameStyle)
		}
		if tagSuffix != "" {
			rendered += paint(s.Dim).Render(tagSuffix)
		}

		// Pad out to the full panel width so a selected row's fill reaches both
		// edges instead of stopping at the last glyph.
		// The row's own gutters are *painted*: the selected row's fill breaks out
		// of them to span the panel edge to edge, which is what makes it read as
		// a filled row rather than as a highlighted word.
		gutter := paint(s.Dim).Render(strings.Repeat(" ", panelGutter))
		gap := max(w-toolRowIndent-panelGutter-lipgloss.Width(name)-lipgloss.Width(tagSuffix)-verW, 1)
		sb.WriteString(gutter + mark + paint(s.Dim).Render(strings.Repeat(" ", toolRowIndent-panelGutter-1)) +
			rendered + paint(s.Dim).Render(strings.Repeat(" ", gap)) + verStyle.Render(verText) + gutter + "\n")

		toolLine[i] = line
		lineTool = append(lineTool, i)
		line++
	}

	if len(matches) == 0 {
		if m.mode == modeSearch {
			sb.WriteString(s.Note.Render("  no matches.") + "\n")
		} else {
			sb.WriteString(s.Note.Render("  no tools tracked.\n  press t to add one.") + "\n")
		}
	}

	return sb.String(), toolLine, lineTool
}

// highlightNameMatch renders the first occurrence of the query inside the
// tool name in the accent — distinct from highlightMatch (textutil.go), the
// single-line highlighter of the help search. Matching is case-insensitive
// (rune-wise via runeIndexFold, so names whose lowercase form has a different
// byte length cannot desync the slice offsets); query must already be lowercase
// (searchQuery normalizes it).
//
// hit and rest are passed in rather than read off s because the selected row
// paints its background into every segment: a hit rendered from the bare style
// would punch a hole in the fill.
func highlightNameMatch(s *ui.Styles, name, query string, hit, rest lipgloss.Style) string {
	lr := []rune(name)
	qr := []rune(query)
	idx := runeIndexFold(lr, qr)
	if idx < 0 {
		return rest.Render(name)
	}
	end := idx + len(qr)
	return rest.Render(string(lr[:idx])) + hit.Render(string(lr[idx:end])) + rest.Render(string(lr[end:]))
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

// panelTitle is a panel's inset title in the two forms insetPanelTitle needs:
// plain for the width arithmetic against the border it is spliced into, styled
// for the screen. They must spell the same text — a styled form that says more
// than the plain one would overrun the border it was measured against.
type panelTitle struct{ plain, styled string }

// focusTitle builds the "▸ [1] tools" head every panel title starts with. Focus
// is marked twice on purpose: by the accent and by the ▸ — so the focused panel
// is still identifiable on a monochrome terminal, or to a reader who cannot
// separate the two panel colors.
func (m Model) focusTitle(n, name string, focused bool) panelTitle {
	s := m.sty()
	label := "[" + n + "] " + name
	if focused {
		return panelTitle{"▸ " + label, s.Accent.Render("▸ ") + s.AccentBold.Render(label)}
	}
	return panelTitle{label, s.Dim.Render(label)}
}

// append extends a title with another styled part, keeping both forms in step.
func (t panelTitle) append(plain, styled string) panelTitle {
	return panelTitle{t.plain + plain, t.styled + styled}
}

func (m Model) renderTools() string {
	s := m.sty()
	focused := m.focus == focusTools

	// The title carries the two numbers that answer "what is in here": how many
	// tools are tracked, and how many of them are behind. The second is the
	// tracker's whole point, so it is the one thing in a panel title that gets
	// the signal color.
	title := m.focusTitle("1", "tools", focused)
	if n := len(m.meta); n > 0 {
		title = title.append(fmt.Sprintf(" %d", n), " "+s.Dim.Render(fmt.Sprintf("%d", n)))
	}
	if n := m.updateCount(); n > 0 {
		title = title.append(fmt.Sprintf(" %d↑", n), " "+s.Signal.Render(fmt.Sprintf("%d↑", n)))
	}

	footer := m.panelFooter(m.toolsW, []string{
		m.hint("/", "filter"),
		m.hint("space", "group"),
	}, "")
	return m.framePanel(m.toolsW, focused,
		withScrollbar(s, m.toolsViewport, m.toolsW, focused), footer, title)
}

func (m Model) renderBrief() string {
	s := m.sty()
	focused := m.focus == focusBrief

	// The footer leads with the panel's primary action, and what that action is
	// depends on the card: with a release pending, installing it outranks
	// everything else the card can do.
	var cells []string
	if t, ok := m.selectedTool(); ok && m.hasUpdate(t.Name) {
		latest := version.DisplayVersion(m.versions[t.Name].Latest)
		cells = append(cells, m.hint("enter", "update to "+latest))
	}
	// No note/tags cells: both keys are already offered in the meta line above,
	// beside the values they edit, and a footer repeating them spends the row
	// on saying the same thing twice.
	cells = append(cells,
		m.hint("r", "refresh"),
		m.hint("s", "status"),
		m.hint("o", "repo"),
		m.hint("c", "changelog"),
	)
	footer := m.panelFooter(m.briefW, cells, "")
	return m.framePanel(m.briefW, focused,
		withScrollbar(s, m.briefViewport, m.briefW, focused), footer,
		m.focusTitle("2", "brief", focused))
}

func (m Model) renderHelp() string {
	s := m.sty()
	focused := m.focus == focusHelp

	name := "help"
	switch m.helpMode {
	case helpModeMan:
		name = "man"
	case helpModeReadme:
		name = "readme"
	}
	// While an update log is showing — the selected tool's, or keepkit's own,
	// which is selection-independent — the panel is the log, not help. Mirror
	// that in the inset title, and drop the mode hints with it: none of the
	// three modes is what the panel is currently showing.
	logShowing := m.showsUpdateLog()
	if logShowing {
		name = "update"
	}
	title := m.focusTitle("3", name, focused)
	if !logShowing {
		// The other two sources of this panel, named in the frame rather than
		// in the footer: they switch what the panel *is*, which is a property
		// of the panel, not an action on its content.
		for _, alt := range [][2]string{{"h", "help"}, {"m", "man"}, {"r", "readme"}} {
			if alt[1] == name {
				continue
			}
			hint := " · " + alt[0] + " " + alt[1]
			title = title.append(hint, s.Rule.Render(hint))
		}
	}

	// The footer says where in the text the reader is, and — while the entry
	// index has something to walk — that j/k walk it. Those two hints are
	// contextual in a way no other panel's are: they appear and disappear with
	// the content, which is exactly why they belong beside it rather than in
	// the global bar.
	cells := []string{s.Dim.Render(name), s.Dim.Render(m.helpPagePosition())}
	if m.helpNavIdx >= 0 {
		cells = append(cells, m.hint("esc", "exit nav"))
	}
	if len(m.helpEntries) > 0 {
		cells = append(cells, m.hint("j/k", "navigate"))
	}
	footer := m.panelFooter(m.helpW, cells, m.hint("ctrl+d/u", "page"))
	return m.framePanel(m.helpW, focused,
		withScrollbar(s, m.helpViewport, m.helpW, focused), footer, title)
}

// helpPagePosition reads the [3] viewport's scroll position as "page/pages" —
// the one bit of orientation a scrollbar thumb cannot give a reader who is
// counting how much is left.
func (m Model) helpPagePosition() string {
	h := max(m.helpViewport.Height, 1)
	total := max(m.helpViewport.TotalLineCount(), 1)
	pages := (total + h - 1) / h
	return fmt.Sprintf("%d/%d", min(m.helpViewport.YOffset/h+1, pages), pages)
}

// framePanel is the shared body of the three renderers: content and footer
// stacked into exactly the panel's content height, wrapped in the focus-aware
// border, with the title inset into its top edge.
func (m Model) framePanel(w int, focused bool, content, footer string, title panelTitle) string {
	s := m.sty()
	panelStyle := s.PanelBorder
	if focused {
		panelStyle = s.PanelBorderFocused
	}
	body := content
	if footer != "" {
		body = lipgloss.JoinVertical(lipgloss.Left, content, footer)
	}
	panel := panelStyle.Width(w).Height(m.calcVpHeight()).Render(body)
	return insetPanelTitle(s, panel, title, focused)
}

// insetPanelTitle splices " title " into the top border line of a rendered
// panel, starting at the 3rd visible cell. The border runs are repainted from
// their stripANSI text (a homogeneously colored run of single-width runes, so
// no ANSI-aware splicing is needed) and the title is dropped in already styled,
// which is what lets one title carry several colors — the [1] panel's tracked
// count and update count are the point of the whole mechanism. A title that
// does not fit is dropped whole and the panel returned unchanged (a chopped
// title reads worse than none), which is why the plain form is measured rather
// than the styled one: escape sequences are not cells.
func insetPanelTitle(s *ui.Styles, panel string, title panelTitle, focused bool) string {
	lines := strings.SplitN(panel, "\n", 2)
	top := []rune(stripANSI(lines[0]))
	label := []rune(" " + title.plain + " ")
	const start = 2               // keep the corner + one ─ cell
	avail := len(top) - start - 1 // keep the closing corner
	if len(label) > avail {
		return panel
	}
	color := s.Theme.Border
	if focused {
		color = s.Theme.Accent
	}
	border := lipgloss.NewStyle().Foreground(color)
	lines[0] = border.Render(string(top[:start])) +
		" " + title.styled + " " +
		border.Render(string(top[start+len(label):]))
	return strings.Join(lines, "\n")
}

// withScrollbar renders a viewport with a 1-col scrollbar gutter on its right
// edge. The gutter stays blank unless the content is taller than the viewport,
// in which case a thumb (no track) is drawn proportional to the scroll position.
// The thumb is peach when the panel is focused, dim otherwise.
func withScrollbar(s *ui.Styles, vp viewport.Model, panelWidth int, focused bool) string {
	left := lipgloss.NewStyle().Width(max(panelWidth-1, 1)).Render(vp.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, scrollColumn(s, vp, focused))
}

func scrollColumn(s *ui.Styles, vp viewport.Model, focused bool) string {
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
		thumbStyle := s.Dim
		if focused {
			thumbStyle = s.Accent
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
// clickable lines: 0-based content-line number → URL. Line heights vary (the
// metrics strip is several rows, the tagline and changelog body wrap), so the
// indices are recorded while writing — strings.Count(sb, "\n") immediately
// before the line is the line's own index — rather than reconstructed by
// parsing the result. bubbles/viewport is line-based (no soft wrap), so a
// content-line index is a viewport line index, which is what the mouse handler
// maps a click row to.
//
// The card reads top-down as one answer: what this tool IS (name, repo,
// tagline), what STATE it is in (the metrics strip), what the user has said
// about it (the meta line), and what changed (the changelog). The old
// "[section] ─────" headers are gone with the label column that went with
// them: four labelled sections over a dozen one-value lines spent most of the
// panel on scaffolding, and the strip says the same thing in three rows.
func (m Model) buildCard() (string, map[int]string) {
	s := m.sty()
	links := make(map[int]string)
	if len(m.meta) == 0 {
		return s.Note.Render("no tools tracked.\npress t to add one."), links
	}

	t, ok := m.selectedTool()
	if !ok {
		return s.Note.Render("select a tool from the left panel."), links
	}

	inner := max(m.briefW-2, 1)
	card, hasCard := m.repoCards[t.Name]

	var sb strings.Builder

	// Title: the tool's name is the single largest thing on the screen, so it
	// takes the brightest role and the only bold in the block. The repo ref
	// rides beside it in dim — it identifies the name rather than competing
	// with it, and it is what the whole [info] section used to spend a line on.
	name := truncateToWidth(flattenLine(t.Name), max(inner-2, 8))
	switch {
	case m.updatingFor == t.Name:
		// While an update runs the title becomes a status line (twin of the
		// refresh spinner; the two are mutually exclusive via their guards).
		sb.WriteString(s.Text.Render("updating ") + s.EmphasisBold.Render(name) +
			" " + m.spinner.View() + "\n")
	case m.refreshingFor == t.Name:
		sb.WriteString(s.Text.Render("refreshing ") + s.EmphasisBold.Render(name) +
			s.Text.Render(" data ") + m.spinner.View() + "\n")
	default:
		title := s.EmphasisBold.Render(name)
		if t.GitHub != "" {
			// The bare owner/repo: the host is implied, and its 11 cells are
			// worth more than they cost in a 30-column panel. NormalizeRepo
			// answering "" (an unsupported or spoofed host, e.g.
			// "github.com.evil.com/x/y") falls back to the raw value on
			// purpose — shortening exactly there would hide the very host that
			// makes the link not what it looks like.
			repoText := t.GitHub
			if short := loader.NormalizeRepo(t.GitHub); short != "" {
				repoText = short
			}
			if lipgloss.Width(name)+1+lipgloss.Width(repoText) <= inner {
				// Clickable, and it resolves exactly like [o] does — the full
				// t.GitHub, never the shortened display text.
				links[strings.Count(sb.String(), "\n")] = "https://" + t.GitHub
				title += " " + s.Dim.Render(repoText)
			}
		}
		sb.WriteString(title + "\n")
	}

	// Tagline: the repo's own one-liner, in reading text. Not italic and not
	// dimmed — it is the first real sentence about the tool.
	if hasCard && card.About != "" {
		sb.WriteString(s.Text.Render(wrapText(card.About, inner)) + "\n")
	}
	if !hasCard && m.repoStatus[t.Name] == "rate-limited" {
		sb.WriteString(s.SignalBold.Render("rate limited — press L") + "\n")
	}

	// One blank row between blocks. The strip needs no second one: its own
	// padding row is filled with the plate colour, which already reads as air —
	// two plain rows on top of it read as a hole.
	if strip := m.metricsStrip(t, inner); len(strip) > 0 {
		sb.WriteString("\n" + strings.Join(strip, "\n") + "\n")
	}

	if meta := m.metaLine(t, inner); meta != "" {
		sb.WriteString("\n" + meta + "\n")
	}

	// Changelog: a heading row carrying the version transition and a link to
	// the release page, then the notes themselves.
	var changelogContent, changelogURL string
	if m.changelogLoadingFor == t.Name {
		changelogContent = s.Note.Render("loading changelog…") + "\n"
	} else if data, hasLog := m.changelogData[t.Name]; hasLog {
		changelogContent = m.renderChangelogBlock(data)
		// Only a successful fetch with a URL leads with one — that is the line
		// to register, and it comes from the same data the block rendered from,
		// so the two cannot drift.
		if data.err == nil {
			changelogURL = data.htmlUrl
		}
	} else if t.GitHub != "" {
		changelogContent = s.Note.Render("loading changelog…") + "\n"
	}
	if changelogContent != "" {
		sb.WriteString("\n\n")
		heading, linked := m.changelogHeading(t, inner, changelogURL != "")
		// Registered only when the heading actually shows the "release notes ↗"
		// affordance: a row that looks like plain text must not open a browser.
		if linked {
			links[strings.Count(sb.String(), "\n")] = changelogURL
		}
		sb.WriteString(heading + "\n\n" + changelogContent)
	}

	return sb.String(), links
}

// metricLabels are the strip's captions, uppercased so they read as a caption
// row rather than as four more words: the values are what the eye should land
// on, and in a terminal there is no smaller type size to demote a label with.
//
// metricMinCol is the narrowest a column may get before the strip re-flows onto
// fewer columns — the widest label plus a cell of breathing room.
const metricMinCol = len("MAINTENANCE") + 1

// metricCell is one measurement in the strip: a caption, its value with the
// style that carries the value's meaning, and an optional second line under it.
type metricCell struct {
	label string
	value string
	style lipgloss.Style
	sub   string
}

// metricsStrip is the card's headline block: installed / latest / maintenance /
// stars as a filled panel, captions above values, columns separated by rules.
//
// The values read at Text, one step below the tool's name above them and one
// above their own captions. A terminal has a single font size — the grid is the
// terminal's, not the app's — so the three sizes the design draws here are
// three steps of weight and brightness instead, and spending the brightest one
// on four measurements would leave the name nothing to be the peak of. The two
// exceptions carry meaning rather than rank: a pending release is one of the
// screen's three "act on this" points, and a broken install is its one alarm.
// It replaces six "label: value" lines whose labels ran down the left edge and
// pushed every value into a column of its own — here the values sit side by side
// and the block reads as one instrument panel.
//
// It re-flows rather than truncates: at the 80-column baseline the panel is ~30
// cells and four columns would leave seven each, so the grid drops to two
// columns and then to one. A metric with nothing to report is left out entirely,
// so a tool with no GitHub ref shows a one-cell strip instead of three empty
// captions.
func (m Model) metricsStrip(t loader.Tool, inner int) []string {
	s := m.sty()
	vinfo := m.versions[t.Name]
	card, hasCard := m.repoCards[t.Name]

	var cells []metricCell

	// installed: four states, and the two version-less ones are deliberately
	// different — a tool that is installed but will not name its version (a TUI
	// app that ignores --version) is a working install and reads affirmative,
	// while a genuine absence is the one thing on the card that is wrong. Both
	// are single words: the caption above them already says INSTALLED, and the
	// sentences they used to be ("no version", "not installed") were the only
	// values in the strip too wide for a baseline-width column.
	switch {
	case vinfo.Installed != "":
		cells = append(cells, metricCell{"INSTALLED", version.DisplayVersion(vinfo.Installed), s.Text, ""})
	case vinfo.InstalledKnown && vinfo.InstalledPresent:
		cells = append(cells, metricCell{"INSTALLED", "✓ present", s.Ok, ""})
	case vinfo.InstalledKnown:
		cells = append(cells, metricCell{"INSTALLED", "✕ missing", s.Danger, ""})
	default:
		cells = append(cells, metricCell{"INSTALLED", "detecting…", s.Dim, ""})
	}

	if hasCard && card.Latest != "" {
		date := card.PublishedAt
		if len(date) > 10 {
			date = date[:10]
		}
		value, style := version.DisplayVersion(card.Latest), s.Text
		if m.hasUpdate(t.Name) {
			// The one place on the card that means "act on this", and the whole
			// reason the tool is in the tracker.
			value += " ↑"
			style = s.SignalBold
		}
		cells = append(cells, metricCell{"LATEST", value, style, date})
	}
	if hasCard && card.RepoStatus != "" {
		value, style := "● "+card.RepoStatus, s.Ok
		if card.RepoStatus == "archived" {
			value, style = "⚠ "+card.RepoStatus, s.Signal
		}
		cells = append(cells, metricCell{"MAINTENANCE", value, style, ""})
	}
	if hasCard && card.Stars > 0 {
		cells = append(cells, metricCell{"STARS", formatStars(card.Stars), s.Text, ""})
	}
	// Below one column's worth of room the grid cannot say anything a caption
	// would not eat whole, so the strip stands down rather than printing
	// single-letter columns. Only a hand-built model gets here: the brief panel
	// has a 30-cell minimum.
	if len(cells) == 0 || inner < metricMinCol {
		return nil
	}

	// The column has to fit its widest *value* as well as its widest caption:
	// sizing on captions alone cut "✕ not installed" to "✕ not insta" at the
	// 80x24 baseline, which is the default terminal. A value wider than any
	// caption therefore costs columns rather than legibility.
	need := metricMinCol
	for _, c := range cells {
		need = max(need, lipgloss.Width(c.value)+1, lipgloss.Width(c.sub)+1)
	}
	cols := min(max((inner-1)/need, 1), len(cells))
	sep := " │ "
	colW := max((inner-2-(cols-1)*lipgloss.Width(sep))/cols, 1)

	// pad renders one cell's text on the block background and pads it to the
	// column. Every segment carries the background itself: a style applied
	// around already-styled text cannot repaint what is inside it, and the
	// inner reset sequences would punch holes in the fill.
	pad := func(text string, style lipgloss.Style) string {
		text = truncateToWidth(text, colW)
		return style.Background(s.Theme.Surface).Render(text) +
			s.Surface.Render(strings.Repeat(" ", max(colW-lipgloss.Width(text), 0)))
	}
	blank := s.Surface.Render(" ")
	rule := s.Rule.Background(s.Theme.Surface).Render(sep)

	var out []string
	// A blank row above and below turns the strip into a block with air in it
	// rather than three crowded lines of text.
	pad0 := s.Surface.Render(strings.Repeat(" ", inner))
	out = append(out, pad0)
	for start := 0; start < len(cells); start += cols {
		row := cells[start:min(start+cols, len(cells))]
		var labels, values, subs []string
		hasSub := false
		for _, c := range row {
			labels = append(labels, pad(c.label, s.Dim))
			values = append(values, pad(c.value, c.style))
			subs = append(subs, pad(c.sub, s.Dim))
			hasSub = hasSub || c.sub != ""
		}
		line := func(parts []string) string {
			body := blank + strings.Join(parts, rule) + blank
			if fill := inner - lipgloss.Width(body); fill > 0 {
				body += s.Surface.Render(strings.Repeat(" ", fill))
			}
			return body
		}
		out = append(out, line(labels), line(values))
		if hasSub {
			out = append(out, line(subs))
		}
	}
	return append(out, pad0)
}

// metaLine is everything the user has told keepkit about the tool, plus what
// the repo says it is written in, on one wrapped line instead of four labelled
// ones. Empty values do not get a line of their own — they get the key that
// fills them ("tags — # add"), which is the only thing an empty field is for.
func (m Model) metaLine(t loader.Tool, inner int) string {
	s := m.sty()
	mt, ok := m.selectedMeta()
	if !ok {
		return ""
	}
	card := m.repoCards[t.Name]

	var cells []string
	if len(card.Languages) > 0 {
		// One line, always. renderLangBar wraps on its own budget, and a cell
		// that wrapped internally would break the whole line's arithmetic
		// below: lipgloss.Width measures a multi-line string by its widest
		// line, which is not the footprint it occupies, so the next cell could
		// be spliced onto the middle of the bar — and the continuation lines
		// arrive with no "lang" label to say what they are. So the bar is cut
		// to one line like every other over-wide value here, with a … saying a
		// language was left out.
		const langLabel = "lang "
		budget := max(inner-len(langLabel)-2, 10)
		bar, rest, wrapped := strings.Cut(renderLangBar(s, card.Languages, budget, 0), "\n")
		if wrapped && rest != "" {
			bar += s.Dim.Render(" …")
		}
		cells = append(cells, s.Dim.Render(langLabel)+bar)
	}
	cells = append(cells, s.Dim.Render("status ")+
		s.Status(mt.Status).Render(loader.StatusSymbol[mt.Status]+" "+string(mt.Status)))

	// The two editable fields. In edit mode the input replaces the value in
	// place, so the card never jumps while it is being typed into.
	// A value is cut to what a cell may occupy before it is styled: cutting
	// afterwards would land inside an escape sequence, which the viewport
	// re-emits to the terminal verbatim. A note is prose and the card is not
	// where it is read in full — the editor shows all of it.
	value := func(label, text string) string {
		budget := max(inner-lipgloss.Width(label)-1, 8)
		if lipgloss.Width(text) > budget {
			text = truncateToWidth(flattenLine(text), max(budget-1, 1)) + "…"
		}
		return s.Dim.Render(label+" ") + s.Text.Render(text)
	}
	switch {
	case m.mode == modeEditTags:
		cells = append(cells, s.Dim.Render("tags ")+m.tagsInput.View())
	case tagOf(mt) != "":
		cells = append(cells, value("tags", tagOf(mt)))
	default:
		cells = append(cells, s.Dim.Render("tags ")+s.Dim.Render("— ")+m.hint("#", "add"))
	}
	switch {
	case m.mode == modeEditNote:
		cells = append(cells, s.Dim.Render("note ")+m.noteInput.View())
	case mt.Note != "":
		cells = append(cells, value("note", mt.Note))
	default:
		cells = append(cells, s.Dim.Render("note ")+s.Dim.Render("— ")+m.hint("e", "write"))
	}

	// Wrapping is by whole cells: a cell carries ANSI, so it must never be cut
	// mid-escape, and half a "note …" reads as noise anyway.
	sep := s.Dim.Render(" · ")
	var lines []string
	cur, curW := "", 0
	for _, c := range cells {
		w := lipgloss.Width(c)
		switch {
		case cur == "":
			cur, curW = c, w
		case curW+lipgloss.Width(sep)+w <= inner:
			cur += sep + c
			curW += lipgloss.Width(sep) + w
		default:
			lines = append(lines, cur)
			cur, curW = c, w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

// changelogHeading is the changelog's own header row: the word, the version
// transition it covers, and — right-aligned — the link to the release page. The
// transition is what the block is actually about, so it is stated rather than
// left for the reader to infer from two version numbers in the strip above.
//
// The second result says whether the link actually fit, and buildCard registers
// the clickable line only then: a heading rendered without the affordance must
// not silently open a browser.
func (m Model) changelogHeading(t loader.Tool, inner int, hasLink bool) (string, bool) {
	s := m.sty()
	head := s.EmphasisBold.Render("changelog")
	// The transition is gated on hasUpdate, the same predicate the ↑ and the
	// update key use — NOT on the two strings differing. A tool whose --version
	// prints "1.10.2" against a "v1.10.2" tag is up to date, and comparing the
	// raw forms printed it "v1.10.2 → v1.10.2": a transition to nowhere on the
	// one card that has nothing to report.
	if vinfo := m.versions[t.Name]; m.hasUpdate(t.Name) && vinfo.Installed != "" {
		head += "  " + s.Dim.Render(version.DisplayVersion(vinfo.Installed)+" → "+version.DisplayVersion(vinfo.Latest))
	}
	if !hasLink {
		return head, false
	}
	// The gap is a rule, not blank space: it ties the two ends of the row into
	// one heading and separates the notes below from the meta line above, which
	// no other divider does now that the card has no section headers.
	link := s.Link.Render("release notes ↗")
	if gap := inner - lipgloss.Width(head) - lipgloss.Width(link) - 2; gap >= 1 {
		return head + " " + s.Rule.Render(strings.Repeat("─", gap)) + " " + link, true
	}
	return head, false
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
	s := m.sty()
	if msg.err != nil {
		return changelogIndent + s.Text.Render("changelog unavailable: "+msg.err.Error()) + "\n"
	}
	var sb strings.Builder
	// The release notes alone: the link to the release page and the version
	// transition are the changelog heading's job, and the versions themselves
	// are already in the metrics strip above.
	// markdownToLines wraps as it converts (hanging indents need the block
	// structure), so styling lands on whole finished lines — the only way to
	// keep ANSI out of the rune-based wrap math. An empty result covers both
	// an empty body and one the converter consumed whole (all comments, all
	// separators).
	inner := max(m.briefW-2, 10)
	lines := m.changelogRender.lines(msg.body, max(inner-len(changelogIndent), 10))
	if len(lines) == 0 {
		return changelogIndent + s.Text.Render("no release notes available.") + "\n"
	}
	// Code sits on the same plate the metrics strip uses, which is what makes a
	// command in a release note read as something to run rather than as more
	// prose. The plate is padded out to the panel so the block reads as a block:
	// a background that stopped at the last glyph would be a ragged highlight.
	plate := s.Emphasis.Background(s.Theme.Surface)
	width := max(inner-len(changelogIndent), 1)
	for _, line := range lines {
		switch {
		case line.kind == mdCode:
			text := truncateToWidth(line.text, width)
			sb.WriteString(changelogIndent + plate.Render(text) +
				s.Surface.Render(strings.Repeat(" ", max(width-lipgloss.Width(text), 0))) + "\n")
		case line.text == "":
			// A blank separator carries no text to color; rendering it would
			// only emit an empty escape pair.
			sb.WriteString("\n")
		case line.kind == mdHeading:
			sb.WriteString(changelogIndent + s.EmphasisBold.Render(line.text) + "\n")
		default:
			sb.WriteString(changelogIndent + s.Text.Render(line.text) + "\n")
		}
	}
	return sb.String()
}

// changelogIndent steps the release notes in under their heading, so the block
// reads as belonging to it rather than as the next section of the card. It is
// plain spaces written outside the styling, so no escape sequence is ever split
// by it.
const changelogIndent = "  "

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
// s.Dim — the same strip-then-repaint trick the [L] overlay uses,
// but per whole line, so no ANSI-safe splicing is needed. A stale
// out-of-bounds index renders undimmed rather than panicking (entries and
// cursor are reset together in setHelpContent, but a value-receiver render
// must not trust that).
func (m Model) applySpotlight(text string) string {
	s := m.sty()
	if m.helpNavIdx < 0 || m.helpNavIdx >= len(m.helpEntries) {
		return text
	}
	e := m.helpEntries[m.helpNavIdx]
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i < e.start || i >= e.end {
			lines[i] = s.Dim.Render(stripANSI(line))
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
		return "rate limited — press L", false
	}
	return "No README for " + name + ".\nPress [h] for --help.", false
}

func (m Model) renderHelpContent() string {
	s := m.sty()
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
			return s.Note.Render("starting update…")
		}
		return wrapText(strings.Join(m.updateLog, "\n"), m.helpWrapWidth())
	}

	mt, ok := m.selectedMeta()
	if !ok {
		return s.Note.Render("No tool selected")
	}

	// README mode: content comes from readmeData (glamour-rendered into helpBase
	// by setHelpContent), never from the [2]string helpCache. Placed after the
	// update-log branch, which keeps priority, and ahead of every helpCache
	// index — mode 2 is out of range for that array.
	if m.helpMode == helpModeReadme {
		text, real := m.readmeContent(mt.Name)
		if !real {
			return s.Note.Render(text)
		}
		return text
	}

	// Gate per tool, not on "any fetch in flight": another tool's fetch may
	// still be running (fast j/k, search arrows then esc/enter) while the
	// selected tool's help is already cached — that cache must render, or the
	// panel would stick on "Loading..." when the stale fetch lands unselected.
	if m.helpLoadingFor == mt.Name {
		return s.Note.Render("Loading...")
	}

	cached, has := m.helpCache[mt.Name]
	if !has || cached[m.helpMode] == "" {
		if m.helpMode == helpModeHelp {
			return s.Note.Render("Press [h] for --help\nPress [m] for man page")
		}
		return s.Note.Render("Press [m] for man page\nPress [h] for --help")
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
		return m.applySpotlight(colorizeHelp(s, wrapText(cached[m.helpMode], m.helpWrapWidth())))
	}
	text := wrapText(cached[m.helpMode], m.helpWrapWidth())
	query := m.helpSearch.Value()
	lines := strings.Split(text, "\n")
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = highlightMatch(s, line, query)
	}
	return strings.Join(result, "\n")
}
