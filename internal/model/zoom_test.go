package model

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/loader"
)

// zoomHelpFixture is one paragraph long enough to wrap differently at every
// panel width the tests below use, so a missing re-wrap is visible as a line
// wider than the new budget rather than as an identical string.
const zoomHelpFixture = "OPTIONS:\n" +
	"  -v, --verbose\n" +
	"          print every step of the resolution, including the manager chain " +
	"and the paths it probed before giving up on the binary\n" +
	"  -q, --quiet\n" +
	"          say nothing at all unless something actually went wrong"

// zoomModel is a laid-out model with a tool selected, panel [3] on --help and a
// wrapping fixture cached — the state every zoom assertion needs.
func zoomModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := New([]loader.ToolMeta{{Name: "git", Tags: []string{"vcs"}}})
	m.helpMode = helpModeHelp
	m.focus = focusHelp
	nm := mustModel(m.Update(tea.WindowSizeMsg{Width: width, Height: height}))
	nm.helpCache = map[string][2]string{"git": {helpModeHelp: zoomHelpFixture}}
	nm.setHelpContent()
	return nm
}

// TestApplyLayoutRewrapsHelpOnWidthChange is the guard on applyLayout's one
// sharp edge: prevWrapW is captured from the STORED m.helpW, so a capture
// placed below the calcPanelWidths assignment compares the new width against
// itself and the re-wrap guard is dead — [3] then keeps its pre-resize wrapping
// forever. It is not the only test that fails on that mutation
// (TestHelpNavIdxResetTriggers/resize, TestResizeHeightOnlyKeepsCursor and
// TestReadmeResizeRerenders do too); it is the one that names the wrap width
// itself, so the failure points at the line that moved rather than at a lost
// spotlight three files away.
func TestApplyLayoutRewrapsHelpOnWidthChange(t *testing.T) {
	m := zoomModel(t, 160, 40)
	if m.helpBase == "" {
		t.Fatalf("helpBase empty — fixture never rendered")
	}
	wide := m.helpBase

	nm := mustModel(m.Update(tea.WindowSizeMsg{Width: 100, Height: 40}))
	if maxLineWidth(wide) <= nm.helpWrapWidth() {
		t.Fatalf("fixture fits the narrow budget already (%d ≤ %d) — it cannot show a re-wrap",
			maxLineWidth(wide), nm.helpWrapWidth())
	}
	if nm.helpBase == wide {
		t.Errorf("helpBase unchanged after a width-changing relayout — the re-wrap guard is dead")
	}
	if got, budget := maxLineWidth(nm.helpBase), nm.helpWrapWidth(); got > budget {
		t.Errorf("widest help line = %d cells, over the new %d budget (stale wrapping)", got, budget)
	}
}

// TestApplyLayoutIdempotent: applying the same layout twice must leave the
// model in the same visible state. Its load-bearing half is the zero-then-
// rebuild assertion at the bottom, which fails when the extraction drops the
// setToolsContent/card tail. Several mouse and line-map tests fail on that
// mutation too; this one states the tail as applyLayout's own contract rather
// than as a click landing on the wrong row.
func TestApplyLayoutIdempotent(t *testing.T) {
	m := zoomModel(t, 120, 30)
	m.toolLine, m.lineTool = nil, nil
	m.toolsViewport.SetContent("")
	m.briefViewport.SetContent("")

	m.applyLayout()
	first := m
	m.applyLayout()

	if m.toolsW != first.toolsW || m.briefW != first.briefW || m.helpW != first.helpW {
		t.Errorf("widths drifted on the second apply: (%d,%d,%d) vs (%d,%d,%d)",
			m.toolsW, m.briefW, m.helpW, first.toolsW, first.briefW, first.helpW)
	}
	if m.toolsViewport.Width != first.toolsViewport.Width ||
		m.toolsViewport.Height != first.toolsViewport.Height ||
		m.helpViewport.Width != first.helpViewport.Width {
		t.Errorf("viewport dimensions drifted on the second apply")
	}
	if !sameInts(m.toolLine, first.toolLine) || !sameInts(m.lineTool, first.lineTool) {
		t.Errorf("line maps drifted: %v/%v vs %v/%v",
			m.toolLine, m.lineTool, first.toolLine, first.lineTool)
	}
	// The tail is what rebuilds these at all: a relayout that skipped it would
	// leave the list maps and the card exactly as they were zeroed above.
	if len(first.toolLine) == 0 || len(first.lineTool) == 0 {
		t.Errorf("applyLayout did not rebuild the line maps")
	}
	if first.toolsViewport.View() == "" || first.briefViewport.View() == "" {
		t.Errorf("applyLayout did not repaint the list and the card")
	}
}

// TestZoomToggleWidensHelp: z from [2]/[3] moves columns from the card to the
// readme and back, reporting each direction. The widths are the assertion —
// the flag alone would be satisfied by a toggle nothing follows.
func TestZoomToggleWidensHelp(t *testing.T) {
	shrinkStatusTTL(t)
	m := zoomModel(t, 160, 40)
	flatBrief, flatHelp := m.briefW, m.helpW

	nm := mustModel(m.Update(keyRunes("z")))
	if !nm.helpZoom {
		t.Fatalf("helpZoom = false after z")
	}
	if nm.helpW <= flatHelp || nm.briefW >= flatBrief {
		t.Errorf("z gave [3] %d (was %d) and left [2] %d (was %d) — want [3] wider at [2]'s expense",
			nm.helpW, flatHelp, nm.briefW, flatBrief)
	}
	if nm.helpViewport.Width != nm.helpW-1 {
		t.Errorf("help viewport = %d, want %d — the relayout did not reach the viewports",
			nm.helpViewport.Width, nm.helpW-1)
	}
	// Silent in both directions: the widths ARE the feedback, and a bar
	// restating them spends a line on what the user just watched happen.
	if nm.statusMsg != "" {
		t.Errorf("statusMsg = %q after a successful zoom, want the bar left alone", nm.statusMsg)
	}

	back := mustModel(nm.Update(keyRunes("z")))
	if back.helpZoom {
		t.Errorf("helpZoom = true after the second z")
	}
	if back.briefW != flatBrief || back.helpW != flatHelp {
		t.Errorf("second z left (%d,%d), want the original (%d,%d)",
			back.briefW, back.helpW, flatBrief, flatHelp)
	}
	if back.statusMsg != "" {
		t.Errorf("statusMsg = %q after restoring the layout, want the bar left alone", back.statusMsg)
	}
}

// TestZoomBindingScope: z belongs to the two focuses looking at [3], the same
// gate R/H/M use. In [1] it is unbound — and unlike the trio it never moves
// focus, because a width change is not a change of what you are reading.
func TestZoomBindingScope(t *testing.T) {
	shrinkStatusTTL(t)
	for _, focus := range []int{focusBrief, focusHelp} {
		m := zoomModel(t, 160, 40)
		m.focus = focus
		nm := mustModel(m.Update(keyRunes("z")))
		if !nm.helpZoom {
			t.Errorf("z in focus %d did not zoom", focus)
		}
		if nm.focus != focus {
			t.Errorf("z moved focus %d → %d, want it left alone", focus, nm.focus)
		}
	}

	m := zoomModel(t, 160, 40)
	m.focus = focusTools
	nm, cmd := m.Update(keyRunes("z"))
	tm := nm.(Model)
	if tm.helpZoom || tm.helpW != m.helpW {
		t.Errorf("z in focusTools zoomed (helpZoom=%v, helpW %d→%d), want a no-op",
			tm.helpZoom, m.helpW, tm.helpW)
	}
	if cmd != nil {
		t.Errorf("z in focusTools returned a command, want nil")
	}
}

// TestZoomRefusesWhenNarrow: at the 80x24 baseline the minimum clamps produce
// the same triple for both variants, so the keypress says so instead of
// flipping a flag that changes nothing on screen.
func TestZoomRefusesWhenNarrow(t *testing.T) {
	shrinkStatusTTL(t)
	m := zoomModel(t, 80, 24)
	nm := mustModel(m.Update(keyRunes("z")))
	if nm.helpZoom {
		t.Errorf("helpZoom = true at 80 cols, want the refusal to leave it off")
	}
	if nm.toolsW != m.toolsW || nm.briefW != m.briefW || nm.helpW != m.helpW {
		t.Errorf("widths changed on a refused zoom: (%d,%d,%d) → (%d,%d,%d)",
			m.toolsW, m.briefW, m.helpW, nm.toolsW, nm.briefW, nm.helpW)
	}
	if nm.statusMsg != "too narrow to zoom" {
		t.Errorf("statusMsg = %q, want %q", nm.statusMsg, "too narrow to zoom")
	}
}

// TestZoomNarrowStillUnzooms: the narrow refusal is one-directional, exactly
// like toggleGroupByTag's. A session zoomed wide and then resized down to a
// width where both variants clamp alike must still be able to turn zoom off —
// a symmetric refusal strands the flag on, and the layout comes back zoomed the
// moment the terminal grows again.
func TestZoomNarrowStillUnzooms(t *testing.T) {
	shrinkStatusTTL(t)
	m := zoomModel(t, 160, 40)
	m = mustModel(m.Update(keyRunes("z")))
	if !m.helpZoom {
		t.Fatalf("zoom did not engage at 160 cols — the rest of the test is vacuous")
	}

	narrow := mustModel(m.Update(tea.WindowSizeMsg{Width: 80, Height: 24}))
	nm := mustModel(narrow.Update(keyRunes("z")))
	if nm.helpZoom {
		t.Errorf("helpZoom still true after z at 80 cols — the flag is stranded on")
	}
	if nm.statusMsg == "too narrow to zoom" {
		t.Errorf("turning zoom off was refused; the refusal must gate activation only")
	}

	// The proof the flag was not merely cosmetic: grown back, the layout is the
	// unzoomed triple rather than the one the stranded flag would have kept.
	wide := mustModel(nm.Update(tea.WindowSizeMsg{Width: 160, Height: 40}))
	tw, bw, hw := wide.panelWidthsFor(false)
	if wide.toolsW != tw || wide.briefW != bw || wide.helpW != hw {
		t.Errorf("layout after regrowing = (%d,%d,%d), want the unzoomed (%d,%d,%d)",
			wide.toolsW, wide.briefW, wide.helpW, tw, bw, hw)
	}
}

// TestZoomBeforeReady: z before the first WindowSizeMsg has no layout to
// toggle — the guard is explicit, not the clamps agreeing by coincidence.
func TestZoomBeforeReady(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "git"}})
	m.focus = focusBrief
	nm, cmd := m.Update(keyRunes("z"))
	tm := nm.(Model)
	if tm.helpZoom || tm.ready {
		t.Errorf("z before the first resize flipped state (helpZoom=%v, ready=%v)",
			tm.helpZoom, tm.ready)
	}
	if cmd != nil {
		t.Errorf("z before the first resize returned a command, want nil")
	}
}

// TestZoomFetchesNothing: a view toggle spends no request, in either direction
// and on the refusal — the status tick is the whole command.
func TestZoomFetchesNothing(t *testing.T) {
	shrinkStatusTTL(t)
	// A successful toggle sets no status, so it has nothing to return at all —
	// a stricter assertion than the tick check: any command here is a fetch.
	m := zoomModel(t, 160, 40)
	if _, cmd := m.Update(keyRunes("z")); cmd != nil {
		t.Errorf("z returned %T, want nil — a silent view toggle has nothing to run", cmd())
	}

	nm := mustModel(m.Update(keyRunes("z")))
	if _, cmd := nm.Update(keyRunes("z")); cmd != nil {
		t.Errorf("the restoring z returned %T, want nil", cmd())
	}

	// The refusal is the one exit that speaks, so it carries the expiry tick
	// and must carry nothing besides.
	narrow := zoomModel(t, 80, 24)
	_, cmd := narrow.Update(keyRunes("z"))
	assertOnlyExpiryTick(t, cmd)
}

// TestZoomInSearchStaysQueryText: modeSearch is handled inline, outside the
// mode-dispatch switch, so it is the one mode whose key safety is not
// structurally guaranteed — a z typed into the filter must stay text.
func TestZoomInSearchStaysQueryText(t *testing.T) {
	m := zoomModel(t, 160, 40)
	m.focus = focusTools
	m = mustModel(m.Update(keyRunes("/")))
	if m.mode != modeSearch {
		t.Fatalf("mode = %v, want modeSearch", m.mode)
	}
	nm := mustModel(m.Update(keyRunes("z")))
	if nm.helpZoom {
		t.Errorf("z in modeSearch zoomed the layout")
	}
	if got := nm.search.Value(); got != "z" {
		t.Errorf("search query = %q, want %q", got, "z")
	}
}

// TestZoomUnderOverlay: the two overlays are modes, so the mode-dispatch switch
// consumes z before the normal-mode case sees it — structurally, the way every
// other normal-mode key is protected. Asserted anyway because relayouting the
// screen underneath an open modal is the one failure this would produce, and
// modeSearch next door proves the dispatch is not uniform.
func TestZoomUnderOverlay(t *testing.T) {
	for _, mode := range []inputMode{modeHotkeys, modeAPIStatus, modeToolOverlay} {
		m := zoomModel(t, 160, 40)
		m.mode = mode
		nm := mustModel(m.Update(keyRunes("z")))
		if nm.helpZoom {
			t.Errorf("z in mode %v zoomed the layout under an open overlay", mode)
		}
		if nm.briefW != m.briefW || nm.helpW != m.helpW {
			t.Errorf("mode %v: widths moved under the overlay: (%d,%d) → (%d,%d)",
				mode, m.briefW, m.helpW, nm.briefW, nm.helpW)
		}
	}
}

// TestZoomResetsSpotlight: any width-changing zoom re-wraps [3], so the entry
// ranges are recomputed and the j/k cursor is lost — the same consequence a
// width resize already has, asserted rather than assumed.
func TestZoomResetsSpotlight(t *testing.T) {
	shrinkStatusTTL(t)
	m := zoomModel(t, 160, 40)
	if len(m.helpEntries) == 0 {
		t.Fatalf("fixture produced no navigable entries")
	}
	m.helpNavIdx = 1

	nm := mustModel(m.Update(keyRunes("z")))
	if nm.helpNavIdx != -1 {
		t.Errorf("helpNavIdx = %d after z, want -1 (the re-wrap resets the cursor)", nm.helpNavIdx)
	}
}

// TestZoomUnderUpdateLog: zoom owns width, showsUpdateLog owns content. A z
// while a log holds [3] must relayout and leave the log exactly where it was.
func TestZoomUnderUpdateLog(t *testing.T) {
	shrinkStatusTTL(t)
	m := zoomModel(t, 160, 40)
	m.updateLogFor = "git"
	m.updateLog = []string{"==> Upgrading git", "==> Pouring git"}
	if !m.showsUpdateLog() {
		t.Fatalf("test setup does not own [3] with an update log")
	}

	nm := mustModel(m.Update(keyRunes("z")))
	if !nm.helpZoom || nm.helpW <= m.helpW {
		t.Errorf("z under an update log did not relayout (%d → %d)", m.helpW, nm.helpW)
	}
	if !nm.showsUpdateLog() {
		t.Errorf("the update log lost panel [3] to a width change")
	}
	if !strings.Contains(stripANSI(nm.renderHelpContent()), "Pouring git") {
		t.Errorf("log content missing after zoom:\n%s", stripANSI(nm.renderHelpContent()))
	}
}

// TestHelpFooterZoomCell: [3] advertises z in its own footer, beside the two
// other things that are true of the panel's content — and drops it while an
// update log owns the panel, exactly as the title drops the source hints. The
// cell is last, so it is also the first to go under width pressure: at the
// 80-column baseline the left budget is spent by "readme · 1/1" alone.
func TestHelpFooterZoomCell(t *testing.T) {
	// The widths are each comfortably clear of that mode's shed threshold, not
	// the thresholds themselves — this asserts the cell is emitted per mode, and
	// the boundary is asserted once at the bottom. What actually moves the
	// threshold is a navigable entry index, which adds a fourth cell: measured,
	// the cell survives from ~114 columns with no index and ~150 with one. So
	// --help is the binding case here (the fixture caches help output, so it has
	// an index), while man in this fixture has none and would in fact keep the
	// cell well below 160. Per-mode numbers would be asserting about the
	// fixture's cache rather than about the mode.
	modes := []struct {
		mode  int
		width int
	}{
		{helpModeReadme, 120},
		{helpModeHelp, 160},
		{helpModeMan, 160},
	}
	for _, tc := range modes {
		m := zoomModel(t, tc.width, 30)
		m.helpMode = tc.mode
		m.setHelpContent()
		if got := stripANSI(m.renderHelp()); !strings.Contains(got, "z zoom") {
			t.Errorf("mode %d footer at %d cols carries no z cell:\n%s", tc.mode, tc.width, got)
		}
	}

	// The log owns the panel: none of the three modes is on screen, so neither
	// the source hints in the title nor the zoom cell belong to what is.
	// Wide enough that the cell would fit if it were emitted — at a width where
	// the footer sheds it anyway this assertion would pass either way.
	log := zoomModel(t, 160, 30)
	log.updateLogFor = "git"
	log.updateLog = []string{"==> Upgrading git"}
	log.setHelpContent()
	if !log.showsUpdateLog() {
		t.Fatalf("fixture does not own [3] with an update log")
	}
	if got := stripANSI(log.renderHelp()); strings.Contains(got, "z zoom") {
		t.Errorf("the update log's footer carries the zoom cell:\n%s", got)
	}

	// At the baseline the footer sheds it — the position/source pair and the
	// pinned ctrl+d/u reserve leave nothing for a width preference.
	narrow := zoomModel(t, 80, 24)
	narrow.helpMode = helpModeReadme
	got := stripANSI(narrow.renderHelp())
	if strings.Contains(got, "z zoom") {
		t.Errorf("the 80-col footer kept the zoom cell:\n%s", got)
	}
	if !strings.Contains(got, "ctrl+d/u page") {
		t.Errorf("the 80-col footer lost its pinned right cell:\n%s", got)
	}
}

// TestHotkeysOverlayZoomRow: the [?] sheet documents z at every width, which is
// what makes the footer cell droppable. The merged note/tags row is what paid
// for it — the per-column row budget is hard (TestRenderHotkeysSizeBudget).
func TestHotkeysOverlayZoomRow(t *testing.T) {
	m := zoomModel(t, 120, 30)
	m.appVersion = "v0.4.2"
	got := stripANSI(m.renderHotkeys())
	if !strings.Contains(got, "z") || !strings.Contains(got, "zoom panel") {
		t.Errorf("overlay does not document z:\n%s", got)
	}
	if !strings.Contains(got, "note / tags") {
		t.Errorf("overlay lost the merged note/tags row:\n%s", got)
	}
	if strings.Contains(got, "edit note") || strings.Contains(got, "edit tags") {
		t.Errorf("overlay still carries the unmerged editor rows — the budget is over:\n%s", got)
	}
}
