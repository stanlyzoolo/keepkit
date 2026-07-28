package model

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stanlyzoolo/keepkit/internal/loader"
)

// groupTestModel builds a ready model over five tools in three tag groups
// (cli: rg, fd — scm: git, lazygit — untagged: jq), deliberately interleaved in
// meta.yaml order so grouping has to actually reorder them.
func groupTestModel(t *testing.T) Model {
	t.Helper()
	m := New([]loader.ToolMeta{
		{Name: "rg", Tags: []string{"cli"}},
		{Name: "git", Tags: []string{"scm"}},
		{Name: "jq"},
		{Name: "fd", Tags: []string{"cli"}},
		{Name: "lazygit", Tags: []string{"scm"}},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	nm := updated.(Model)
	nm.focus = focusTools
	return nm
}

func displayedNames(m Model) []string {
	out := make([]string, 0, len(m.meta))
	for _, mt := range m.filteredMeta() {
		out = append(out, mt.Name)
	}
	return out
}

func sameNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestGroupedOrderingByTag: with the tag view on, tools sharing a tag sit
// together in first-appearance group order, meta.yaml order inside a group, and
// untagged tools trail the list.
func TestGroupedOrderingByTag(t *testing.T) {
	m := groupTestModel(t)
	if got, want := displayedNames(m), []string{"rg", "git", "jq", "fd", "lazygit"}; !sameNames(got, want) {
		t.Fatalf("flat order = %v, want meta.yaml order %v", got, want)
	}

	m.groupByTag = true
	if got, want := displayedNames(m), []string{"rg", "fd", "git", "lazygit", "jq"}; !sameNames(got, want) {
		t.Errorf("grouped order = %v, want %v", got, want)
	}
}

// TestGroupedOrderingLeadsWithUpdates: in the grouped view an outdated tool
// leaves its tag section for the leading updates section — a tag says what a
// tool is, an update says what it needs, and the second is the reason the app
// is open. The section leads regardless of where its tools sit in meta.yaml.
func TestGroupedOrderingLeadsWithUpdates(t *testing.T) {
	m := groupTestModel(t)
	m.versions["jq"] = VersionInfo{Installed: "v1.0.0", Latest: "v2.0.0", InstalledKnown: true}

	if got := displayedNames(m); got[0] != "jq" {
		t.Fatalf("flat order = %v, want the updatable jq first", got)
	}

	m.groupByTag = true
	if got, want := displayedNames(m), []string{"jq", "rg", "fd", "git", "lazygit"}; !sameNames(got, want) {
		t.Errorf("grouped order = %v, want %v (updates section first)", got, want)
	}
	if content := stripANSI(m.renderLeftContent()); !strings.HasPrefix(content, "updates ") {
		t.Errorf("grouped content does not lead with the updates header:\n%s", content)
	}
}

// TestGroupingSuppressedWhileSearching: the `/` filter behaves exactly as
// before with the toggle on — no headers, update partition back on.
func TestGroupingSuppressedWhileSearching(t *testing.T) {
	m := groupTestModel(t)
	m.groupByTag = true
	updated, _ := m.Update(keyRunes("/"))
	m = updated.(Model)
	m = typeRunes(t, m, "g") // rg, git, lazygit

	if m.grouped() {
		t.Errorf("grouped() = true during a search, want the flat search behaviour")
	}
	content, toolLine, lineTool := m.buildToolRows()
	if strings.Contains(stripANSI(content), "─ cli") {
		t.Errorf("search results carry a group divider header:\n%s", stripANSI(content))
	}
	for i := range toolLine {
		if toolLine[i] != i || lineTool[i] != i {
			t.Fatalf("maps are not the identity while searching: toolLine=%v lineTool=%v", toolLine, lineTool)
		}
	}
}

// TestBuildToolRowsMapsFlat: with grouping off there are no header rows, so
// both maps are the identity — every downstream translation is a no-op and the
// pre-grouping behaviour is bit-for-bit unchanged.
func TestBuildToolRowsMapsFlat(t *testing.T) {
	m := groupTestModel(t)
	_, toolLine, lineTool := m.buildToolRows()
	if len(toolLine) != 5 || len(lineTool) != 5 {
		t.Fatalf("map sizes = %d/%d, want 5/5", len(toolLine), len(lineTool))
	}
	for i := range toolLine {
		if toolLine[i] != i {
			t.Errorf("toolLine[%d] = %d, want %d", i, toolLine[i], i)
		}
		if lineTool[i] != i {
			t.Errorf("lineTool[%d] = %d, want %d", i, lineTool[i], i)
		}
	}
}

// TestBuildToolRowsMapsGrouped: header rows shift every tool below them, the
// two maps stay each other's inverse, and each header line is where lineTool
// says a header is.
func TestBuildToolRowsMapsGrouped(t *testing.T) {
	m := groupTestModel(t)
	m.groupByTag = true
	content, toolLine, lineTool := m.buildToolRows()
	lines := strings.Split(strings.TrimRight(stripANSI(content), "\n"), "\n")

	// cli header, rg, fd, scm header, git, lazygit, untagged header, jq
	wantTool := []int{1, 2, 4, 5, 7}
	if !sameInts(toolLine, wantTool) {
		t.Fatalf("toolLine = %v, want %v", toolLine, wantTool)
	}
	wantLine := []int{-1, 0, 1, -1, 2, 3, -1, 4}
	if !sameInts(lineTool, wantLine) {
		t.Fatalf("lineTool = %v, want %v", lineTool, wantLine)
	}
	if len(lines) != len(lineTool) {
		t.Fatalf("rendered %d lines, lineTool covers %d", len(lines), len(lineTool))
	}

	// Divider headers: "<label> ────…". Match by the label and its rule rather
	// than the full line, whose dash count depends on the panel width.
	wantHeaders := map[int]string{0: "cli", 3: "scm", 6: "untagged"}
	for i, line := range lines {
		if label, isHeader := wantHeaders[i]; isHeader {
			if lineTool[i] != -1 {
				t.Errorf("line %d (%q) maps to tool %d, want -1", i, line, lineTool[i])
			}
			if !strings.HasPrefix(line, label+" ─") {
				t.Errorf("line %d = %q, want the %q divider header", i, line, label)
			}
			continue
		}
		name := m.filteredMeta()[lineTool[i]].Name
		if !strings.Contains(line, name) {
			t.Errorf("line %d = %q, want the row of %q (lineTool says %d)", i, line, name, lineTool[i])
		}
	}
}

func sameInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestSpaceTogglesGroupingKeepsSelection: space in focusTools flips the view
// and the cursor follows the *tool*, not the row — in both directions, and for
// a tool whose position actually changes.
func TestSpaceTogglesGroupingKeepsSelection(t *testing.T) {
	shrinkStatusTTL(t)
	m := groupTestModel(t)
	// jq: index 2 flat, index 4 grouped (untagged, last).
	m.metaSelected = 2
	if sel, _ := m.selectedMeta(); sel.Name != "jq" {
		t.Fatalf("setup: selected %q, want jq", sel.Name)
	}

	updated, cmd := m.Update(keyRunes(" "))
	nm := updated.(Model)
	if !nm.groupByTag {
		t.Fatalf("space did not turn grouping on")
	}
	// The toggle returns only the "grouped by tag" status-expiry tick — a view
	// toggle still fetches nothing.
	assertOnlyExpiryTick(t, cmd)
	if sel, _ := nm.selectedMeta(); sel.Name != "jq" {
		t.Errorf("selected %q after grouping on, want jq", sel.Name)
	}
	if nm.metaSelected != 4 {
		t.Errorf("metaSelected = %d, want 4 (jq's grouped index)", nm.metaSelected)
	}

	updated, _ = nm.Update(keyRunes(" "))
	nm = updated.(Model)
	if nm.groupByTag {
		t.Fatalf("second space did not turn grouping off")
	}
	if sel, _ := nm.selectedMeta(); sel.Name != "jq" {
		t.Errorf("selected %q after grouping off, want jq", sel.Name)
	}
	if nm.metaSelected != 2 {
		t.Errorf("metaSelected = %d, want 2 (jq's flat index)", nm.metaSelected)
	}
}

// TestSpaceGroupingNeedsATag: turning the view on with nothing tagged would
// draw a lone "untagged" divider over the unchanged list, so the toggle refuses
// and says why. Turning it *off* is never refused — otherwise removing the last
// tag would strand the user in the tag view.
func TestSpaceGroupingNeedsATag(t *testing.T) {
	t.Run("no tools at all", func(t *testing.T) {
		m := New(nil)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
		m = updated.(Model)
		m.focus = focusTools

		nm := mustModel(m.Update(keyRunes(" ")))
		if nm.groupByTag {
			t.Errorf("space turned grouping on with no tools")
		}
		if !strings.Contains(nm.statusMsg, "no tags") {
			t.Errorf("statusMsg = %q, want the no-tags explanation", nm.statusMsg)
		}
	})

	t.Run("tools but no tags", func(t *testing.T) {
		m := New([]loader.ToolMeta{{Name: "rg"}, {Name: "fd"}})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
		m = updated.(Model)
		m.focus = focusTools

		nm := mustModel(m.Update(keyRunes(" ")))
		if nm.groupByTag {
			t.Errorf("space turned grouping on with no tagged tool")
		}
		if strings.Contains(stripANSI(nm.renderLeftContent()), "─ untagged") {
			t.Errorf("list drew a lone untagged divider header:\n%s", stripANSI(nm.renderLeftContent()))
		}
	})

	t.Run("last tag removed while grouped", func(t *testing.T) {
		m := groupTestModel(t)
		m.groupByTag = true
		// Every tag gone (untagged by hand, or the tags editor emptied them):
		// the view degrades to flat instead of showing one untagged section...
		for i := range m.meta {
			m.meta[i].Tags = nil
		}
		if m.grouped() {
			t.Errorf("grouped() stayed true with no tagged tool")
		}
		if strings.Contains(stripANSI(m.renderLeftContent()), "─ ") {
			t.Errorf("list still drew a divider header:\n%s", stripANSI(m.renderLeftContent()))
		}
		// ...and space still turns the flag back off.
		nm := mustModel(m.Update(keyRunes(" ")))
		if nm.groupByTag {
			t.Errorf("space could not leave the tag view after the last tag was removed")
		}
	})
}

// TestTagEditRemapsCursorUnderGrouping: the tag is the grouping key, so
// committing one reorders the list under the cursor. The selection must follow
// the edited tool and the list must be repainted, or the line maps keep
// describing the pre-edit content for the next click.
func TestTagEditRemapsCursorUnderGrouping(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New([]loader.ToolMeta{
		{Name: "aaa", Tags: []string{"cli"}},
		{Name: "bbb", Tags: []string{"xxx"}},
		{Name: "ccc", Tags: []string{"cli"}},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(Model)
	m.focus = focusBrief
	m.groupByTag = true
	m.setToolsContent()

	m.metaSelected = 2 // grouped order is [aaa, ccc, bbb] → bbb
	if sel, _ := m.selectedMeta(); sel.Name != "bbb" {
		t.Fatalf("setup: selected %q, want bbb", sel.Name)
	}

	m.mode = modeEditTags
	m.tagsInput.SetValue("cli")
	nm := mustModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))

	if sel, _ := nm.selectedMeta(); sel.Name != "bbb" {
		t.Errorf("selected %q after the tag edit, want the edited tool bbb", sel.Name)
	}
	// The maps must match the repainted content, or a click resolves elsewhere.
	_, wantTool, wantLine := nm.buildToolRows()
	if !sameInts(nm.toolLine, wantTool) || !sameInts(nm.lineTool, wantLine) {
		t.Errorf("line maps = %v/%v, want %v/%v (list not repainted)",
			nm.toolLine, nm.lineTool, wantTool, wantLine)
	}
}

// TestGroupingIsCaseInsensitive: the search predicate compares tags case
// insensitively, so grouping must not split `CLI` and `cli` into two sections.
// The header shows the group's first spelling.
func TestGroupingIsCaseInsensitive(t *testing.T) {
	m := New([]loader.ToolMeta{
		{Name: "aaa", Tags: []string{"CLI"}},
		{Name: "bbb", Tags: []string{"zzz"}},
		{Name: "ccc", Tags: []string{"cli"}},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(Model)
	m.groupByTag = true

	if got, want := displayedNames(m), []string{"aaa", "ccc", "bbb"}; !sameNames(got, want) {
		t.Errorf("grouped order = %v, want %v (CLI and cli are one group)", got, want)
	}
	content := stripANSI(m.renderLeftContent())
	// The header shows the group's first spelling ("CLI"); the case-folded key
	// keeps "cli" in the same section, so no second lowercase divider appears.
	if n := strings.Count(content, "CLI ─"); n != 1 {
		t.Errorf("content has %d CLI divider headers, want 1:\n%s", n, content)
	}
	if strings.Contains(content, "cli ─") {
		t.Errorf("content split the group into a second cli section:\n%s", content)
	}
}

// TestGroupedNavigationSkipsHeaders: j walks tools, never headers — the cursor
// is a tool index, so every step lands on a real tool and one lap visits them
// all in display order.
func TestGroupedNavigationSkipsHeaders(t *testing.T) {
	m := groupTestModel(t)
	m.groupByTag = true
	m.setToolsContent()

	want := []string{"fd", "git", "lazygit", "jq", "rg"} // starting from rg
	for i, wantName := range want {
		updated, _ := m.Update(keyRunes("j"))
		m = updated.(Model)
		sel, ok := m.selectedMeta()
		if !ok {
			t.Fatalf("step %d: nothing selected", i)
		}
		if sel.Name != wantName {
			t.Fatalf("step %d: selected %q, want %q", i, sel.Name, wantName)
		}
		if m.lineTool[m.toolLine[m.metaSelected]] != m.metaSelected {
			t.Fatalf("step %d: selection landed on a header line", i)
		}
	}
}

// TestSyncToolsViewportUsesScreenLine: scrolling works in screen lines, so the
// headers above the selection count. With metaSelected used directly the last
// tool would sit below the bottom edge.
func TestSyncToolsViewportUsesScreenLine(t *testing.T) {
	m := groupTestModel(t)
	m.groupByTag = true
	m.toolsViewport.Height = 4 // 8 rendered lines, only 4 visible

	m.metaSelected = 4 // jq: tool index 4, screen line 7
	m.setToolsContent()

	if got, want := m.selectedLine(), 7; got != want {
		t.Fatalf("selectedLine = %d, want %d", got, want)
	}
	if got, want := m.toolsViewport.YOffset, 4; got != want {
		t.Errorf("YOffset = %d, want %d (screen line 7 bottom-aligned in 4 rows)", got, want)
	}
}

// TestWindowSizeRebuildsLineMaps is the stale-map regression: a resize repaints
// the list, so it must go through setToolsContent. A bare SetContent would
// leave the maps describing the pre-resize content — here the maps simply
// would not exist yet.
func TestWindowSizeRebuildsLineMaps(t *testing.T) {
	m := groupTestModel(t)
	m.groupByTag = true
	m.toolLine, m.lineTool = nil, nil

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	nm := updated.(Model)

	_, wantTool, wantLine := nm.buildToolRows()
	if !sameInts(nm.toolLine, wantTool) || !sameInts(nm.lineTool, wantLine) {
		t.Errorf("after resize maps = %v/%v, want %v/%v (rebuilt from the new content)",
			nm.toolLine, nm.lineTool, wantTool, wantLine)
	}
}

// pagingModel builds a grouped list taller than its viewport: 12 tools in 4
// tag groups (16 screen lines) with a 5-row viewport.
func pagingModel(t *testing.T) Model {
	t.Helper()
	var metas []loader.ToolMeta
	for g := range 4 {
		for i := range 3 {
			metas = append(metas, loader.ToolMeta{
				Name: fmt.Sprintf("t%d%d", g, i),
				Tags: []string{fmt.Sprintf("g%d", g)},
			})
		}
	}
	m := New(metas)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	nm := updated.(Model)
	nm.focus = focusTools
	nm.groupByTag = true
	nm.toolsViewport.Height = 5
	nm.setToolsContent()
	return nm
}

// TestGroupedPagingStepsScreenLines: a page key advances one screen page. The
// step is a count of viewport rows, so under grouping it must not be spent as a
// count of tools — that skips the rows the headers occupied, which were never
// displayed.
func TestGroupedPagingStepsScreenLines(t *testing.T) {
	// The cursor starts on t00, screen line 1 (line 0 is the #g0 header).
	// A page moves it a viewport height of *lines*, and syncToolsViewport
	// scrolls by the same amount, so the user reads consecutive pages: line
	// 1+5 = 6 is t11. Counting the step in tools instead would land on line
	// 1+5 tools = t12, skipping a row that was never displayed.
	tests := []struct {
		name string
		key  tea.KeyMsg
		want string
	}{
		{"pgdown", tea.KeyMsg{Type: tea.KeyPgDown}, "t11"},
		{"ctrl+f", ctrlKey('f'), "t11"},
		// Half a page = 2 lines: line 1 → line 3 = t02.
		{"ctrl+d", ctrlKey('d'), "t02"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := pagingModel(t)
			nm := mustModel(m.Update(tt.key))
			sel, ok := nm.selectedMeta()
			if !ok {
				t.Fatalf("nothing selected")
			}
			if sel.Name != tt.want {
				t.Errorf("selected %q, want %q (a page of screen lines, not of tools)", sel.Name, tt.want)
			}
		})
	}

	t.Run("pgup mirrors pgdown", func(t *testing.T) {
		m := pagingModel(t)
		down := mustModel(m.Update(tea.KeyMsg{Type: tea.KeyPgDown}))
		up := mustModel(down.Update(tea.KeyMsg{Type: tea.KeyPgUp}))
		if sel, _ := up.selectedMeta(); sel.Name != "t00" {
			t.Errorf("selected %q after down+up, want back at t00", sel.Name)
		}
	})

	t.Run("flat list keeps the old behaviour", func(t *testing.T) {
		m := pagingModel(t)
		m.groupByTag = false
		m.setToolsContent()
		nm := mustModel(m.Update(tea.KeyMsg{Type: tea.KeyPgDown}))
		if nm.metaSelected != 5 { // viewport height 5, no headers
			t.Errorf("metaSelected = %d, want 5 (height-sized step in a flat list)", nm.metaSelected)
		}
	})
}

// TestSyncToolsViewportRevealsHeader: scrolling up to a tool brings its group
// header along, so the first group never renders as if it had none.
func TestSyncToolsViewportRevealsHeader(t *testing.T) {
	m := pagingModel(t)
	// Jump to the last tool, then back to the first.
	end := mustModel(m.Update(keyRunes("G")))
	if end.toolsViewport.YOffset == 0 {
		t.Fatalf("setup: list did not scroll to the end")
	}
	top := mustModel(end.Update(keyRunes("g")))
	if got := top.toolsViewport.YOffset; got != 0 {
		t.Errorf("YOffset = %d, want 0 — the #g0 header above the first tool was left off screen", got)
	}
}

// TestMouseClickGroupedList: a click on a tool row selects that tool through
// the line map, and a click on a group header selects nothing.
func TestMouseClickGroupedList(t *testing.T) {
	m := groupTestModel(t)
	m.groupByTag = true
	m.setToolsContent()

	// Screen line 4 is git's row (see TestBuildToolRowsMapsGrouped), +2 for the
	// top margin and the panel border.
	updated, _ := m.Update(leftClick(1, 4+2))
	nm := updated.(Model)
	if sel, _ := nm.selectedMeta(); sel.Name != "git" {
		t.Errorf("click on git's row selected %q, want git", sel.Name)
	}

	// Screen line 3 is the scm divider header.
	updated, _ = nm.Update(leftClick(1, 3+2))
	after := updated.(Model)
	if sel, _ := after.selectedMeta(); sel.Name != "git" {
		t.Errorf("click on a header moved the selection to %q, want it left on git", sel.Name)
	}
	if after.focus != focusTools {
		t.Errorf("focus = %d, want focusTools (a header click still focuses the panel)", after.focus)
	}
}

// TestMouseClickOutsideToolsViewport: the same chrome guard as the brief panel
// — a click on the top margin or the border must not select a row through a
// scrolled viewport.
func TestMouseClickOutsideToolsViewport(t *testing.T) {
	m := pagingModel(t)
	end := mustModel(m.Update(keyRunes("G"))) // scroll to the bottom
	if end.toolsViewport.YOffset == 0 {
		t.Fatalf("setup: list did not scroll")
	}
	before, _ := end.selectedMeta()

	for _, y := range []int{0, 1, 2 + end.toolsViewport.Height} {
		after := mustModel(end.Update(leftClick(1, y)))
		if sel, _ := after.selectedMeta(); sel.Name != before.Name {
			t.Errorf("click at y=%d moved the selection %q → %q", y, before.Name, sel.Name)
		}
	}
}

// TestTagHeaderLineFitsOneLine: a header must occupy exactly one screen line of
// the panel's width — every consumer of the line maps assumes it. Measured in
// display cells (a CJK glyph is two) and flattened, since a hand-edited
// meta.yaml can put a newline in a tag.
func TestTagHeaderLineFitsOneLine(t *testing.T) {
	m := groupTestModel(t)
	m.toolsW = 8
	budget := m.toolsW - 1

	tags := []string{
		strings.Repeat("x", 40), // plain overlong
		"日本語ツールバンド",             // wide glyphs: 2 cells each
		"a日本語日本語",               // mixed widths
		"x\ny",                  // embedded newline
		"tab\there",             // embedded tab
		strings.Repeat("日", 3),  // exactly at the edge in runes, over in cells
	}
	for _, tag := range tags {
		got := stripANSI(m.tagHeaderLine(tag))
		if strings.ContainsAny(got, "\n\r") {
			t.Errorf("header for %q = %q, want a single line", tag, got)
		}
		// The framed divider is built to exactly the panel width (w >= 4 here),
		// so an overlong or CJK tag is cut, never allowed to overflow.
		if w := lipgloss.Width(got); w != budget {
			t.Errorf("header for %q = %q, width %d, want exactly %d", tag, got, w, budget)
		}
	}

	// A tag renders as the label followed by a rule out to the panel edge:
	// "ab ────" at toolsW-1 = 7 cells.
	if got, want := stripANSI(m.tagHeaderLine("ab")), "ab ────"; got != want {
		t.Errorf("short header = %q, want %q", got, want)
	}
}

// TestTagHeaderDividerFormat pins the divider look: "<label> ───…" to exactly
// the panel width with the label leading, the empty-tag label is "untagged" (no
// hashtag), and a panel too narrow to frame degrades to the bare label without
// panicking.
func TestTagHeaderDividerFormat(t *testing.T) {
	m := groupTestModel(t)

	t.Run("framed divider is exactly the panel width", func(t *testing.T) {
		m.toolsW = 20
		w := m.toolsW - 1
		got := stripANSI(m.tagHeaderLine("dev"))
		// Label first, then the rule out to the edge.
		if !strings.HasPrefix(got, "dev ─") || !strings.HasSuffix(got, "─") {
			t.Errorf("header = %q, want the \"dev ───…\" divider (no hashtag)", got)
		}
		if strings.Contains(got, "#") {
			t.Errorf("header = %q, want no hashtag", got)
		}
		if lw := lipgloss.Width(got); lw != w {
			t.Errorf("header width = %d, want exactly %d", lw, w)
		}
	})

	t.Run("empty tag renders untagged divider", func(t *testing.T) {
		m.toolsW = 20
		got := stripANSI(m.tagHeaderLine(""))
		if !strings.HasPrefix(got, "untagged ─") || !strings.HasSuffix(got, "─") {
			t.Errorf("empty-tag header = %q, want the \"untagged ───…\" divider", got)
		}
		if lw := lipgloss.Width(got); lw != m.toolsW-1 {
			t.Errorf("untagged header width = %d, want %d", lw, m.toolsW-1)
		}
	})

	t.Run("panel too narrow to frame degrades to bare label", func(t *testing.T) {
		// w = max(toolsW-1, 1) < 4 takes the degraded path.
		for _, tw := range []int{1, 2, 3, 4} {
			m.toolsW = tw
			w := max(m.toolsW-1, 1)
			got := stripANSI(m.tagHeaderLine("dev"))
			if strings.ContainsAny(got, "\n\r") {
				t.Errorf("toolsW=%d: header %q spans multiple lines", tw, got)
			}
			if lw := lipgloss.Width(got); lw > w {
				t.Errorf("toolsW=%d: header %q width %d, want <= %d", tw, got, lw, w)
			}
		}
	})
}

// TestGroupedRowsAreOneLineEach is the map-integrity guard behind the header
// rules: whatever the tags contain, the rendered list must have exactly as many
// lines as lineTool has entries, or clicks and scrolling resolve to the wrong
// rows.
func TestGroupedRowsAreOneLineEach(t *testing.T) {
	m := New([]loader.ToolMeta{
		{Name: "aa", Tags: []string{"x\ny"}},
		{Name: "bb", Tags: []string{"日本語ツールバンド説明"}},
		{Name: "cc"},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(Model)
	m.groupByTag = true

	content, toolLine, lineTool := m.buildToolRows()
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) != len(lineTool) {
		t.Fatalf("rendered %d lines, lineTool covers %d:\n%s", len(lines), len(lineTool), stripANSI(content))
	}
	for i, idx := range toolLine {
		if lineTool[idx] != i {
			t.Errorf("toolLine[%d] = %d but lineTool[%d] = %d", i, idx, idx, lineTool[idx])
		}
	}
}
