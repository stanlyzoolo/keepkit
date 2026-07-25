package model

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"github.com/stanlyzoolo/keeptui/internal/loader"
	"github.com/stanlyzoolo/keeptui/internal/logx"
	"github.com/stanlyzoolo/keeptui/internal/ui"
	"github.com/stanlyzoolo/keeptui/internal/version"
)

// TestUpdateViewNoPanicNoLog confirms a normal Update/View cycle writes no log
// file — logx.Recover is a no-op without a panic in flight, so View being hot
// does not create log churn.
func TestUpdateViewNoPanicNoLog(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	m := New([]loader.ToolMeta{{Name: "git", Tags: []string{"vcs"}}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	_ = m.View()

	if entries, err := os.ReadDir(logDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "keeptui-") {
				t.Errorf("normal Update/View cycle created a log file: %s", e.Name())
			}
		}
	}
}

func TestWrapText(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"fits", "hello world", 100, "hello world"},
		{"wraps on word boundary", "aaa bbb ccc", 7, "aaa bbb\nccc"},
		{"zero width returns input", "x y z", 0, "x y z"},
		{"keeps existing newlines", "ab\ncd", 100, "ab\ncd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wrapText(tt.in, tt.width); got != tt.want {
				t.Errorf("wrapText(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
		})
	}
}

func TestParseHelpEntries(t *testing.T) {
	// Wide width: no wrapping, so entry ranges equal source-line ranges.
	const wide = 200

	clapHelp := strings.Join([]string{
		"ripgrep 13.0.0",
		"",
		"USAGE:",
		"    rg [OPTIONS] PATTERN",
		"",
		"OPTIONS:",
		"    -e, --regexp <PATTERN>",
		"            A pattern to search for.",
		"    -i, --ignore-case",
		"            Case insensitive search.",
		"            Second description line.",
		"    -v, --invert-match",
	}, "\n")

	cobraHelp := strings.Join([]string{
		"Work seamlessly with GitHub.",
		"",
		"Available Commands:",
		"  completion  Generate the autocompletion script",
		"  help        Help about any command",
		"",
		"Flags:",
		"  -h, --help   help for gh",
	}, "\n")

	gnuHelp := strings.Join([]string{
		"Usage: ls [OPTION]... [FILE]...",
		"",
		"  -v, --verbose",
		"          explain what is being done",
		"  -q, --quiet",
		"          suppress output",
	}, "\n")

	manHelp := strings.Join([]string{
		"OPTIONS",
		"     -i      Ignore case.",
		"             More description.",
		"",
		"     -V      Print version.",
	}, "\n")

	tests := []struct {
		name string
		in   string
		want []entryRange
	}{
		{"empty", "", nil},
		{"prose only", "just some text\nwith no flags at all", nil},
		{"clap flags with descriptions", clapHelp, []entryRange{
			{start: 6, end: 8},   // -e + description
			{start: 8, end: 11},  // -i + two description lines
			{start: 11, end: 12}, // -v, no description
		}},
		{"cobra subcommands and flags", cobraHelp, []entryRange{
			{start: 3, end: 4}, // completion
			{start: 4, end: 5}, // help
			{start: 7, end: 8}, // -h
		}},
		{"gnu flags", gnuHelp, []entryRange{
			{start: 2, end: 4},
			{start: 4, end: 6},
		}},
		{"man page options", manHelp, []entryRange{
			{start: 1, end: 3}, // -i + continuation, blank line terminates
			{start: 4, end: 5},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseHelpEntries(tt.in, wide)
			if len(got) != len(tt.want) {
				t.Fatalf("parseHelpEntries returned %d entries %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("entry %d = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}

	t.Run("headers and usage belong to no entry", func(t *testing.T) {
		lines := strings.Split(clapHelp, "\n")
		for _, e := range parseHelpEntries(clapHelp, wide) {
			for i := e.start; i < e.end; i++ {
				if isHelpSectionHeader(lines[i]) || strings.Contains(lines[i], "USAGE") || strings.Contains(lines[i], "rg [OPTIONS]") {
					t.Errorf("entry %v covers non-entry line %d: %q", e, i, lines[i])
				}
			}
		}
	})
}

// TestParseHelpEntriesWrapMapping pins the invariant that entry ranges
// address wrapped display lines: a long description wraps into extra lines
// and the entry must cover all of them, in the exact positions wrapText
// produces.
func TestParseHelpEntriesWrapMapping(t *testing.T) {
	raw := strings.Join([]string{
		"OPTIONS:",
		"  -i, --ignore-case",
		"          a very long description line that will certainly wrap at a narrow width",
		"  -q, --quiet",
	}, "\n")
	const width = 30
	display := strings.Split(wrapText(raw, width), "\n")

	entries := parseHelpEntries(raw, width)
	if len(entries) != 2 {
		t.Fatalf("got %d entries %v, want 2", len(entries), entries)
	}
	first, second := entries[0], entries[1]
	if !strings.Contains(display[first.start], "--ignore-case") {
		t.Errorf("first entry start line = %q, want the -i flag line", display[first.start])
	}
	if first.end-first.start < 3 {
		t.Errorf("first entry %v spans %d display lines, want >= 3 (wrapped description)", first, first.end-first.start)
	}
	if !strings.Contains(display[second.start], "--quiet") {
		t.Errorf("second entry start line = %q, want the -q flag line", display[second.start])
	}
	if second.end != len(display) {
		t.Errorf("second entry end = %d, want %d (last display line)", second.end, len(display))
	}
	for i := first.end; i < second.start; i++ {
		t.Errorf("gap line %d between entries: %q", i, display[i])
	}
}

func TestFormatStars(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{59400, "59.4k"},
	}
	for _, tt := range tests {
		if got := formatStars(tt.in); got != tt.want {
			t.Errorf("formatStars(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLanguagePercents(t *testing.T) {
	t.Run("empty returns nil", func(t *testing.T) {
		if got := languagePercents(nil); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
		if got := languagePercents(map[string]int{}); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("sorted descending with correct percent", func(t *testing.T) {
		got := languagePercents(map[string]int{"Go": 3, "Rust": 1})
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0].Name != "Go" || got[0].Pct != 75 {
			t.Errorf("got[0] = %+v, want {Go 75}", got[0])
		}
		if got[1].Name != "Rust" || got[1].Pct != 25 {
			t.Errorf("got[1] = %+v, want {Rust 25}", got[1])
		}
	})

	t.Run("caps at top 5", func(t *testing.T) {
		langs := map[string]int{"a": 6, "b": 5, "c": 4, "d": 3, "e": 2, "f": 1}
		if got := languagePercents(langs); len(got) != 5 {
			t.Errorf("len = %d, want 5", len(got))
		}
	})
}

func TestCleanTerminalOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "no change", "no change"},
		{"backspace overstrike removes prev rune", "a\bb", "b"},
		{"bold overstrike (man bold)", "N\bNA\bA", "NA"},
		{"carriage return dropped", "x\ry", "xy"},
		{"strips ANSI escapes", "\x1b[1mhi\x1b[0m", "hi"},
		// A TUI tool probed with --help can leave terminal-state escapes in
		// its captured output (the inertia incident): re-emitting them from
		// the help viewport flips the real terminal out of the alt screen.
		{"strips private-mode CSI (alt screen)", "\x1b[?1049lpanic: boom", "panic: boom"},
		{"strips OSC title", "\x1b]0;title\x07text", "text"},
		{"drops stray control chars, keeps \\n and \\t", "a\x07b\fc\nd\te", "abc\nd\te"},
		{"drops lone ESC from a truncated sequence", "cut\x1b", "cut"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanTerminalOutput(tt.in); got != tt.want {
				t.Errorf("cleanTerminalOutput(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// forceColorProfile forces truecolor so lipgloss actually emits ANSI escapes
// (a non-TTY test run strips them and hides regressions), restoring the
// previous profile on cleanup so the global doesn't leak into later tests.
func forceColorProfile(t *testing.T) {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

func TestColorizeHelp(t *testing.T) {
	forceColorProfile(t)

	// A dash inside a word (e.g. "golangci-lint") must not be styled as a
	// short flag, which would inject an ANSI escape mid-word.
	got := colorizeHelp("golangci-lint runs linters")
	if strings.Contains(got, "golangci\x1b") {
		t.Errorf("colorizeHelp injected escape inside word: %q", got)
	}

	// A real flag preceded by whitespace should still be styled.
	got = colorizeHelp("use --verbose for details")
	if !strings.Contains(got, "\x1b") {
		t.Errorf("colorizeHelp did not style a real flag: %q", got)
	}

	// A flag followed by a [bracket]/<angle> meta token must not be corrupted:
	// the meta regex must never match the '[' inside the flag's ANSI escape,
	// which would leave a visible "[38;2;…m" and doubled ESC bytes.
	for _, in := range []string{
		"--foo [bar] enable it",
		"usage: tool [options] --verbose",
		"see <arg> and --flag here",
	} {
		got := colorizeHelp(in)
		if strings.Contains(got, "\x1b\x1b") {
			t.Errorf("colorizeHelp produced doubled ESC for %q: %q", in, got)
		}
		// After stripping every valid CSI escape, no raw "[…m" may remain.
		if leftover := ansiCSI.ReplaceAllString(got, ""); strings.Contains(leftover, "[38;") {
			t.Errorf("colorizeHelp leaked a stripped-ESC escape for %q: %q", in, got)
		}
	}
}

var ansiCSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestStripMarkdown(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"## Hello", "Hello"},
		{"**bold**", "bold"},
		{"`code`", "code"},
	}
	for _, tt := range tests {
		if got := stripMarkdown(tt.in); got != tt.want {
			t.Errorf("stripMarkdown(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRenderRepoStatus(t *testing.T) {
	tests := []struct {
		status string
		want   []string // substrings that must be present
	}{
		{"active", []string{"●", "active"}},
		{"archived", []string{"⚠", "archived"}},
		{"weird", []string{"weird"}},
	}
	for _, tt := range tests {
		got := renderRepoStatus(tt.status)
		for _, sub := range tt.want {
			if !strings.Contains(got, sub) {
				t.Errorf("renderRepoStatus(%q) = %q, missing %q", tt.status, got, sub)
			}
		}
	}
}

func TestRenderLangBar(t *testing.T) {
	t.Run("lowercases names and shows percent", func(t *testing.T) {
		got := renderLangBar(map[string]int{"Go": 1}, 40, 0)
		if !strings.Contains(got, "go") {
			t.Errorf("expected lowercase 'go' in %q", got)
		}
		if strings.Contains(got, "Go") {
			t.Errorf("expected no uppercase 'Go' in %q", got)
		}
		if !strings.Contains(got, "100%") {
			t.Errorf("expected '100%%' in %q", got)
		}
	})

	t.Run("wraps when over width", func(t *testing.T) {
		langs := map[string]int{"alpha": 30, "bravo": 25, "charlie": 25, "delta": 20}
		got := renderLangBar(langs, 12, 0)
		if !strings.Contains(got, "\n") {
			t.Errorf("expected wrapping (newline) in %q", got)
		}
	})

	t.Run("empty returns empty", func(t *testing.T) {
		if got := renderLangBar(nil, 40, 0); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}

func TestFindMatches(t *testing.T) {
	if got := findMatches("a\nb\na", "a"); len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("findMatches = %v, want [0 2]", got)
	}
	if got := findMatches("x", "y"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	if got := findMatches("anything", ""); got != nil {
		t.Errorf("empty query should match nothing, got %v", got)
	}
}

func TestRenderStatusBarFocusTools(t *testing.T) {
	m := Model{width: 80, focus: focusTools}
	got := m.renderStatusBar()

	for _, want := range []string{"search", "track", "quit"} {
		if !strings.Contains(got, want) {
			t.Errorf("focusTools status bar = %q, missing %q", got, want)
		}
	}
	for _, absent := range []string{"filter", "github", "check", "navigate"} {
		if strings.Contains(got, absent) {
			t.Errorf("focusTools status bar = %q, should not contain %q", got, absent)
		}
	}
}

func TestRenderStatusBarFocusBrief(t *testing.T) {
	// Wide enough for the whole hint list: at 80 columns the bar degrades by
	// dropping trailing cells (see TestStatusBarNeverWraps).
	m := Model{width: 120, focus: focusBrief}
	got := m.renderStatusBar()

	for _, want := range []string{"[o]", "[c]", "[s]", "[e]", "[t]", "[q]"} {
		if !strings.Contains(got, want) {
			t.Errorf("focusBrief status bar = %q, missing %q", got, want)
		}
	}
	for _, absent := range []string{"scroll", "help", "back"} {
		if strings.Contains(got, absent) {
			t.Errorf("focusBrief status bar = %q, should not contain %q", got, absent)
		}
	}
}

// TestRenderStatusBarSearch verifies the modeSearch bar still echoes the live
// query and shows the commit/rollback hints: enter open, arrows move, esc
// cancel.
func TestRenderStatusBarSearch(t *testing.T) {
	m := newSearchTestModel()
	updated, _ := m.Update(keyRunes("/"))
	m = updated.(Model)
	m = typeRunes(t, m, "rip")

	got := m.renderStatusBar()
	for _, want := range []string{"rip", "[enter]", "open", "[↑/↓]", "move", "[esc]", "cancel"} {
		if !strings.Contains(got, want) {
			t.Errorf("search status bar = %q, missing %q", got, want)
		}
	}
}

// TestRenderStatusBarSearchCounter verifies the N/M match counter in the
// search bar: matches over the full list size, including 0/M when the query
// filters everything out.
func TestRenderStatusBarSearchCounter(t *testing.T) {
	m := newSearchTestModel() // fzf, git, ripgrep
	updated, _ := m.Update(keyRunes("/"))
	m = updated.(Model)

	m = typeRunes(t, m, "rip")
	if got := m.renderStatusBar(); !strings.Contains(got, "1/3") {
		t.Errorf("search status bar = %q, want counter 1/3", got)
	}

	m = typeRunes(t, m, "zzz")
	if got := m.renderStatusBar(); !strings.Contains(got, "0/3") {
		t.Errorf("search status bar = %q, want counter 0/3", got)
	}
}

// TestRenderLeftContentSearchMarker verifies the selection marker stays
// visible while searching and follows the arrow-moved highlight through the
// filtered list.
func TestRenderLeftContentSearchMarker(t *testing.T) {
	m := newSearchTestModel()
	updated, _ := m.Update(keyRunes("/"))
	m = updated.(Model)
	m = typeRunes(t, m, "g") // matches git and ripgrep

	lines := strings.Split(m.renderLeftContent(), "\n")
	if len(lines) < 2 {
		t.Fatalf("renderLeftContent = %q, want at least 2 rows", lines)
	}
	if !strings.Contains(lines[0], "⏺") || !strings.Contains(lines[0], "git") {
		t.Errorf("first match row = %q, want marker on git", lines[0])
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	lines = strings.Split(m.renderLeftContent(), "\n")
	if strings.Contains(lines[0], "⏺") {
		t.Errorf("first row = %q, marker should move away after down", lines[0])
	}
	if !strings.Contains(lines[1], "⏺") || !strings.Contains(lines[1], "ripgrep") {
		t.Errorf("second match row = %q, want marker on ripgrep", lines[1])
	}
}

// TestRenderLeftContentTagMatchSuffix verifies rows that matched only by tag
// show the dim #tag suffix, name-matched rows do not, and the suffix is
// dropped (without panicking) when the name column is too narrow for it.
func TestRenderLeftContentTagMatchSuffix(t *testing.T) {
	m := New([]loader.ToolMeta{
		{Name: "gitui", Tags: []string{"git"}},
		{Name: "lazygit", Tags: []string{"tui"}},
	})
	// 100 cols → toolsW 18: wide enough for "lazygit #tui" (at 80 the layout
	// minimums squeeze toolsW to 14 and the suffix is legitimately dropped).
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(Model)
	m.focus = focusTools
	updated, _ = m.Update(keyRunes("/"))
	m = updated.(Model)
	m = typeRunes(t, m, "tui") // gitui matches by name, lazygit only by tag

	lines := strings.Split(stripANSI(m.renderLeftContent()), "\n")
	if !strings.Contains(lines[1], "lazygit") || !strings.Contains(lines[1], "#tui") {
		t.Errorf("tag-only row = %q, want lazygit with #tui suffix", lines[1])
	}
	if strings.Contains(lines[0], "#") {
		t.Errorf("name-match row = %q, want no tag suffix", lines[0])
	}

	// A name column too narrow for the suffix drops it instead of wrapping
	// the row.
	m.toolsW = 8 // maxName = 3
	lines = strings.Split(stripANSI(m.renderLeftContent()), "\n")
	for i, line := range lines {
		if strings.Contains(line, "#") {
			t.Errorf("narrow row %d = %q, want tag suffix dropped", i, line)
		}
	}
}

// TestRenderLeftContentSearchHighlight verifies the matched substring of a
// non-selected row's name is wrapped in the peach-bold highlight while
// searching.
func TestRenderLeftContentSearchHighlight(t *testing.T) {
	forceColorProfile(t)
	m := newSearchTestModel() // fzf, git, ripgrep
	updated, _ := m.Update(keyRunes("/"))
	m = updated.(Model)
	m = typeRunes(t, m, "i") // matches git (selected) and ripgrep

	lines := strings.Split(m.renderLeftContent(), "\n")
	if want := ui.SelectedNameStyle.Render("i"); !strings.Contains(lines[1], want) {
		t.Errorf("non-selected match row = %q, want highlighted substring %q", lines[1], want)
	}
	if !strings.Contains(stripANSI(lines[1]), "ripgrep") {
		t.Errorf("non-selected match row = %q, highlight corrupted the name", stripANSI(lines[1]))
	}
}

// TestHighlightNameMatch pins the helper: case-insensitive match, untouched
// non-match, and per-line behavior (a match split across a wrap boundary is
// left unhighlighted).
func TestHighlightNameMatch(t *testing.T) {
	forceColorProfile(t)
	styled := ui.SelectedNameStyle.Render("ip")
	if got := highlightNameMatch("ripgrep", "ip"); got != "r"+styled+"grep" {
		t.Errorf("highlightNameMatch(ripgrep, ip) = %q, want %q", got, "r"+styled+"grep")
	}
	if got := highlightNameMatch("RipGrep", "ipg"); got != "R"+ui.SelectedNameStyle.Render("ipG")+"rep" {
		t.Errorf("highlightNameMatch(RipGrep, ipg) = %q, case-insensitive match expected", got)
	}
	if got := highlightNameMatch("ripgrep", "zz"); got != "ripgrep" {
		t.Errorf("highlightNameMatch(ripgrep, zz) = %q, want untouched", got)
	}
	if got := highlightNameMatch("ab\ncd", "bc"); got != "ab\ncd" {
		t.Errorf("highlightNameMatch(ab\\ncd, bc) = %q, want untouched across the wrap boundary", got)
	}
	// Only the first occurrence per line is highlighted.
	if got := highlightNameMatch("gogo", "go"); got != ui.SelectedNameStyle.Render("go")+"go" {
		t.Errorf("highlightNameMatch(gogo, go) = %q, want only the first occurrence styled", got)
	}
	// Rune safety: Ⱥ (2 bytes) lowercases to ⱥ (3 bytes), so byte offsets
	// found in strings.ToLower(line) would slice the original out of range.
	if got := highlightNameMatch("Ⱥx", "x"); stripANSI(got) != "Ⱥx" || !utf8.ValidString(got) {
		t.Errorf("highlightNameMatch(Ⱥx, x) = %q, want the name intact and valid UTF-8", got)
	}
	if got := highlightNameMatch("Ⱥx", "ⱥ"); stripANSI(got) != "Ⱥx" || !strings.Contains(got, "Ⱥ") {
		t.Errorf("highlightNameMatch(Ⱥx, ⱥ) = %q, want case-insensitive match on the original rune", got)
	}
}

// TestRenderLeftContentMarkerSurvivesFocus verifies the ⏺ marker on the
// selected row does not disappear when focus moves to the brief/help panels
// (it renders dim there, but stays in the output).
func TestRenderLeftContentMarkerSurvivesFocus(t *testing.T) {
	m := New([]loader.ToolMeta{
		{Name: "fzf", Status: loader.StatusActive},
		{Name: "git", Status: loader.StatusActive},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.metaSelected = 1

	for _, f := range []int{focusTools, focusBrief, focusHelp} {
		m.focus = f
		lines := strings.Split(stripANSI(m.renderLeftContent()), "\n")
		if !strings.HasPrefix(lines[1], "⏺ git") {
			t.Errorf("focus %v: selected row = %q, want ⏺ marker on git", f, lines[1])
		}
		if strings.Contains(lines[0], "⏺") {
			t.Errorf("focus %v: unselected row = %q, should carry no marker", f, lines[0])
		}
	}
}

// TestRenderLeftContentMarkerColumn verifies the marker column carries only the
// ⏺ cursor: the selected row gets it regardless of status, every other row
// (active, trying, inactive, unknown) gets a plain space — the status edge is
// gone (tool status lives in the brief card only).
func TestRenderLeftContentMarkerColumn(t *testing.T) {
	m := New([]loader.ToolMeta{
		{Name: "fzf", Status: loader.StatusActive},
		{Name: "git", Status: loader.StatusTrying},
		{Name: "ripgrep", Status: loader.StatusInactive},
		{Name: "yq", Status: loader.Status("bogus")},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.focus = focusTools
	m.metaSelected = 0

	lines := strings.Split(stripANSI(m.renderLeftContent()), "\n")
	if !strings.HasPrefix(lines[0], "⏺ fzf") {
		t.Errorf("selected active row = %q, want ⏺ marker", lines[0])
	}
	for i, name := range []string{"git", "ripgrep", "yq"} {
		if !strings.HasPrefix(lines[i+1], "  "+name) {
			t.Errorf("non-selected row = %q, want plain space in the marker column", lines[i+1])
		}
		if strings.Contains(lines[i+1], "⏺") {
			t.Errorf("non-selected row = %q, should carry no cursor", lines[i+1])
		}
	}

	// The ⏺ cursor takes priority on the selected row regardless of status, and
	// the row it left behind falls back to a plain-space marker column.
	m.metaSelected = 1
	lines = strings.Split(stripANSI(m.renderLeftContent()), "\n")
	if !strings.HasPrefix(lines[1], "⏺ git") {
		t.Errorf("selected trying row = %q, want ⏺ cursor", lines[1])
	}
	if !strings.HasPrefix(lines[0], "  fzf") {
		t.Errorf("active row = %q, want plain space in the marker column", lines[0])
	}
}

// TestRenderLeftContentRowWidth verifies the marker column glyphs are all
// single-cell, so every row keeps the same visible width prefix (1 marker
// cell + 1 space) regardless of selection or status.
func TestRenderLeftContentRowWidth(t *testing.T) {
	m := New([]loader.ToolMeta{
		{Name: "aa", Status: loader.StatusActive},
		{Name: "bb", Status: loader.StatusTrying},
		{Name: "cc", Status: loader.StatusInactive},
		{Name: "dd", Status: loader.Status("bogus")},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.focus = focusTools
	m.metaSelected = 0

	for i, line := range strings.Split(strings.TrimRight(stripANSI(m.renderLeftContent()), "\n"), "\n") {
		if w := lipgloss.Width(line); w != 4 { // marker + space + 2-rune name
			t.Errorf("row %d = %q, visible width = %d, want 4", i, line, w)
		}
	}
}

// updatableModel builds a model with tools where the named ones have an
// available update (Installed older than Latest), so the grouping partition
// lifts them to the top of the list. Sizing goes through a real WindowSizeMsg.
func updatableModel(t *testing.T, metas []loader.ToolMeta, updatable ...string) Model {
	t.Helper()
	m := New(metas)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.focus = focusTools
	for _, name := range updatable {
		m.versions[name] = VersionInfo{Installed: "1.0", Latest: "2.0", InstalledKnown: true}
	}
	return m
}

// filteredNames projects the displayed (grouped/filtered) order to a name slice.
func filteredNames(m Model) []string {
	var out []string
	for _, mt := range m.filteredMeta() {
		out = append(out, mt.Name)
	}
	return out
}

// TestToolsListGrouping verifies updatable tools are stable-partitioned to the
// top of the displayed list (meta.yaml order preserved inside each group) while
// the underlying m.meta slice is left untouched.
func TestToolsListGrouping(t *testing.T) {
	metas := []loader.ToolMeta{
		{Name: "aa"}, {Name: "bb"}, {Name: "cc"}, {Name: "dd"},
	}
	// bb and dd have updates → they float up, keeping their relative order.
	m := updatableModel(t, metas, "bb", "dd")

	if got, want := filteredNames(m), []string{"bb", "dd", "aa", "cc"}; !slices.Equal(got, want) {
		t.Errorf("displayed order = %v, want %v", got, want)
	}
	// m.meta on disk order is never reordered by the display projection.
	if got, want := (func() []string {
		var s []string
		for _, mt := range m.meta {
			s = append(s, mt.Name)
		}
		return s
	})(), []string{"aa", "bb", "cc", "dd"}; !slices.Equal(got, want) {
		t.Errorf("m.meta order = %v, want %v (untouched)", got, want)
	}
	// The rendered rows follow the same order.
	rows := strings.Split(strings.TrimRight(stripANSI(m.renderLeftContent()), "\n"), "\n")
	for i, want := range []string{"bb", "dd", "aa", "cc"} {
		if !strings.Contains(rows[i], want) {
			t.Errorf("row %d = %q, want %s", i, rows[i], want)
		}
	}
}

// TestToolsListGroupingWithinSearch verifies the grouping partition applies
// inside an active search filter too: matched updatable tools sort above matched
// non-updatable ones.
func TestToolsListGroupingWithinSearch(t *testing.T) {
	metas := []loader.ToolMeta{
		{Name: "git"}, {Name: "gitui"}, {Name: "lazygit"},
	}
	m := updatableModel(t, metas, "lazygit") // lazygit has an update
	updated, _ := m.Update(keyRunes("/"))
	m = updated.(Model)
	m = typeRunes(t, m, "git") // all three match by name

	if got, want := filteredNames(m), []string{"lazygit", "git", "gitui"}; !slices.Equal(got, want) {
		t.Errorf("filtered+grouped order = %v, want %v", got, want)
	}
}

// TestRemoteMsgKeepsSelectionOnReorder verifies a remoteMsg that flips another
// tool into the updatable group (lifting it above the selected one) keeps the
// cursor on the *selected tool*, not its old row index.
func TestRemoteMsgKeepsSelectionOnReorder(t *testing.T) {
	m := updatableModel(t, []loader.ToolMeta{{Name: "aa"}, {Name: "bb"}, {Name: "cc"}})
	m.versions["cc"] = VersionInfo{Installed: "1.0", InstalledKnown: true} // no Latest yet
	m.metaSelected = 1                                                     // bb (displayed idx 1)

	// cc gets a newer release → hasUpdate(cc) true → cc floats to the top,
	// pushing bb to displayed idx 2.
	updated, _ := m.Update(remoteMsg{toolName: "cc", latest: "2.0", card: version.RepoCard{About: "x"}})
	nm := updated.(Model)

	if got, want := filteredNames(nm), []string{"cc", "aa", "bb"}; !slices.Equal(got, want) {
		t.Fatalf("displayed order = %v, want %v", got, want)
	}
	if sel, ok := nm.selectedMeta(); !ok || sel.Name != "bb" || nm.metaSelected != 2 {
		t.Errorf("selection = %v (idx %d), want bb at idx 2 (followed the tool)", sel, nm.metaSelected)
	}
}

// TestRemoteMsgSelectedToolLiftedToTop verifies that when the *selected* tool is
// the one gaining an update, it stays selected at its new top-of-list index.
func TestRemoteMsgSelectedToolLiftedToTop(t *testing.T) {
	m := updatableModel(t, []loader.ToolMeta{{Name: "aa"}, {Name: "bb"}, {Name: "cc"}})
	m.versions["cc"] = VersionInfo{Installed: "1.0", InstalledKnown: true}
	m.metaSelected = 2 // cc

	updated, _ := m.Update(remoteMsg{toolName: "cc", latest: "2.0", card: version.RepoCard{About: "x"}})
	nm := updated.(Model)

	if sel, ok := nm.selectedMeta(); !ok || sel.Name != "cc" || nm.metaSelected != 0 {
		t.Errorf("selection = %v (idx %d), want cc at idx 0 (lifted, still selected)", sel, nm.metaSelected)
	}
}

// TestInstalledMsgKeepsSelectionOnReorder verifies the installedMsg handler
// remaps the cursor by name too: a fresh installed version that makes another
// tool updatable reorders the list without dragging the selection off its tool.
func TestInstalledMsgKeepsSelectionOnReorder(t *testing.T) {
	m := updatableModel(t, []loader.ToolMeta{{Name: "aa"}, {Name: "bb"}, {Name: "cc"}})
	m.versions["cc"] = VersionInfo{Latest: "2.0"} // Latest known, Installed pending
	m.metaSelected = 1                            // bb

	// cc's installed version arrives, older than Latest → cc becomes updatable
	// and floats to the top, pushing bb to idx 2.
	updated, _ := m.Update(installedMsg{toolName: "cc", installed: "1.0"})
	nm := updated.(Model)

	if sel, ok := nm.selectedMeta(); !ok || sel.Name != "bb" || nm.metaSelected != 2 {
		t.Errorf("selection = %v (idx %d), want bb at idx 2", sel, nm.metaSelected)
	}
}

// TestVersionMsgsEmptyListNoPanic verifies installedMsg/remoteMsg on an empty
// tool list are safe: the remap is skipped when there is no selection.
func TestVersionMsgsEmptyListNoPanic(t *testing.T) {
	m := New(nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(installedMsg{toolName: "ghost", installed: "1.0"})
	m = updated.(Model)
	updated, _ = m.Update(remoteMsg{toolName: "ghost", latest: "2.0", card: version.RepoCard{About: "x"}})
	m = updated.(Model)
	if m.metaSelected != 0 {
		t.Errorf("metaSelected = %d, want 0 on empty list", m.metaSelected)
	}
}

// TestMarkerGlyphWidth pins the marker/suffix glyphs at width 1 in
// go-runewidth's default condition (the one lipgloss measures with), keeping the
// list's row-width math stable. Note: both ⏺ (U+23FA, the selection cursor) and
// ↑ (U+2191, the update-available suffix) are East-Asian **Ambiguous** — they
// measure 2 cells under RUNEWIDTH_EASTASIAN=1. This is consciously accepted:
// the removed ▎ status edge was in the same class, so the change is not a
// regression. A bare lipgloss.Width==1 check cannot detect the ambiguity, so the
// test measures both conditions explicitly.
func TestMarkerGlyphWidth(t *testing.T) {
	for _, r := range []rune{'⏺', '↑'} {
		def := runewidth.Condition{EastAsianWidth: false}
		if w := def.RuneWidth(r); w != 1 {
			t.Errorf("RuneWidth(%q) default = %d, want 1", r, w)
		}
		// Document (not enforce) the Ambiguous classification: width 2 under the
		// East-Asian condition. If a future Unicode table narrowed this to 1 the
		// comment above would be stale — surface it as a heads-up, not a failure.
		ea := runewidth.Condition{EastAsianWidth: true}
		if w := ea.RuneWidth(r); w != 2 {
			t.Logf("RuneWidth(%q) east-asian = %d, expected 2 (Ambiguous) — table may have changed", r, w)
		}
	}
}

// TestRenderPanelTitles verifies every panel's top border carries its title
// with the focus hotkey, that the help title follows m.helpMode, and that the
// splice leaves the border's visible width alone.
func TestRenderPanelTitles(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "fzf", Status: loader.StatusActive}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(Model)

	m.helpMode = helpModeHelp
	top := stripANSI(strings.SplitN(m.renderHelp(), "\n", 2)[0])
	if !strings.Contains(top, " [3] Help ") {
		t.Errorf("help-mode top border = %q, want [3] Help title", top)
	}

	m.helpMode = helpModeReadme
	topReadme := stripANSI(strings.SplitN(m.renderHelp(), "\n", 2)[0])
	if !strings.Contains(topReadme, " [3] Readme ") {
		t.Errorf("readme-mode top border = %q, want [3] Readme title", topReadme)
	}

	m.helpMode = helpModeMan
	lines := strings.Split(m.renderHelp(), "\n")
	topMan := stripANSI(lines[0])
	if !strings.Contains(topMan, " [3] Man ") || strings.Contains(topMan, "Help") {
		t.Errorf("man-mode top border = %q, want [3] Man title", topMan)
	}
	if bottom := stripANSI(lines[len(lines)-1]); lipgloss.Width(topMan) != lipgloss.Width(bottom) {
		t.Errorf("top border width = %d, want %d (unchanged)", lipgloss.Width(topMan), lipgloss.Width(bottom))
	}

	for _, tt := range []struct{ name, panel, want string }{
		{"tools", m.renderTools(), " [1] Tools "},
		{"brief", m.renderBrief(), " [2] Brief "},
	} {
		panelLines := strings.Split(tt.panel, "\n")
		got := stripANSI(panelLines[0])
		if !strings.Contains(got, tt.want) {
			t.Errorf("%s top border = %q, want %q title", tt.name, got, tt.want)
		}
		if bottom := stripANSI(panelLines[len(panelLines)-1]); lipgloss.Width(got) != lipgloss.Width(bottom) {
			t.Errorf("%s top border width = %d, want %d (unchanged)", tt.name, lipgloss.Width(got), lipgloss.Width(bottom))
		}
	}
}

// TestPanelTitleFollowsFocus verifies each panel's title is painted with the
// focus-aware border color: same visible text, different styling, so the
// focused branch of every panel renderer is exercised.
func TestPanelTitleFollowsFocus(t *testing.T) {
	forceColor(t)

	tests := []struct {
		name   string
		focus  int
		render func(Model) string
	}{
		{"tools", focusTools, Model.renderTools},
		{"brief", focusBrief, Model.renderBrief},
		{"help", focusHelp, Model.renderHelp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New([]loader.ToolMeta{{Name: "fzf", Status: loader.StatusActive}})
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
			m = updated.(Model)

			m.focus = focusTools
			if tt.focus == focusTools {
				m.focus = focusHelp // ensure the panel under test starts unfocused
			}
			blurred, _, _ := strings.Cut(tt.render(m), "\n")

			m.focus = tt.focus
			focused, _, _ := strings.Cut(tt.render(m), "\n")

			if focused == blurred {
				t.Errorf("%s top border identical focused and unfocused (%q), want a focus-aware color", tt.name, blurred)
			}
			if stripANSI(focused) != stripANSI(blurred) {
				t.Errorf("%s visible top border changed with focus:\n focused = %q\n blurred = %q",
					tt.name, stripANSI(focused), stripANSI(blurred))
			}
		})
	}
}

// TestInsetPanelTitle exercises the splice helper directly: a too-narrow
// panel is returned unchanged (no panic), a title that does not fit whole is
// dropped rather than truncated, and a fitting title is inset without
// changing the top line's visible width.
func TestInsetPanelTitle(t *testing.T) {
	narrow := "╭─╮\n│ │\n╰─╯"
	if got := insetPanelTitle(narrow, "--help", false); got != narrow {
		t.Errorf("narrow panel = %q, want unchanged", got)
	}
	if got := insetPanelTitle("", "--help", false); got != "" {
		t.Errorf("empty panel = %q, want unchanged", got)
	}

	// " --help " needs 8 cells; this top offers 3 — dropped whole, not chopped.
	partial := "╭────╮\n│    │\n╰────╯"
	if got := insetPanelTitle(partial, "--help", true); got != partial {
		t.Errorf("partial-fit panel = %q, want unchanged (title dropped whole)", got)
	}

	fits := "╭──────────╮\n│          │\n╰──────────╯"
	top := stripANSI(strings.SplitN(insetPanelTitle(fits, "--help", true), "\n", 2)[0])
	if len([]rune(top)) != 12 {
		t.Errorf("titled top = %q, visible width = %d, want 12", top, len([]rune(top)))
	}
	if !strings.HasPrefix(top, "╭─ --help ") || !strings.HasSuffix(top, "╮") {
		t.Errorf("titled top = %q, want inset title with corners intact", top)
	}
}

func TestRenderStatusBarGauge(t *testing.T) {
	known := version.RateLimit{Known: true, Remaining: 15, Limit: 60} // used 45/60

	t.Run("unknown renders no gauge", func(t *testing.T) {
		m := Model{width: 120, focus: focusTools}
		m.rate = version.RateLimit{Known: false, Remaining: 0, Limit: 60}
		got := m.renderStatusBar()
		for _, absent := range []string{"GitHub API Usage", "GH ", "45/60"} {
			if strings.Contains(got, absent) {
				t.Errorf("unknown rate status bar = %q, should not contain %q", got, absent)
			}
		}
	})

	t.Run("wide width shows full gauge (pinned to focusTools)", func(t *testing.T) {
		// The [enter] run cell widened the focusTools hints to 79 cols; the full
		// gauge (50 cols) needs width >= hints+gauge+gap+border = 133.
		m := Model{width: 135, focus: focusTools, rate: known}
		got := m.renderStatusBar()
		for _, want := range []string{"GitHub API Usage", "45/60", "[L]", "search"} {
			if !strings.Contains(got, want) {
				t.Errorf("wide status bar = %q, missing %q", got, want)
			}
		}
	})

	t.Run("medium width collapses to compact", func(t *testing.T) {
		m := Model{width: 103, focus: focusTools, rate: known}
		got := m.renderStatusBar()
		if strings.Contains(got, "GitHub API Usage") {
			t.Errorf("medium status bar unexpectedly full: %q", got)
		}
		for _, want := range []string{"GH ", "45/60", "[L]"} {
			if !strings.Contains(got, want) {
				t.Errorf("medium status bar = %q, missing %q", got, want)
			}
		}
	})

	t.Run("narrow width hides gauge but keeps hints", func(t *testing.T) {
		m := Model{width: 75, focus: focusTools, rate: known}
		got := m.renderStatusBar()
		for _, absent := range []string{"GitHub API Usage", "GH ", "45/60"} {
			if strings.Contains(got, absent) {
				t.Errorf("narrow status bar = %q, should not contain %q", got, absent)
			}
		}
		for _, want := range []string{"search", "track", "quit"} {
			if !strings.Contains(got, want) {
				t.Errorf("narrow status bar = %q, missing hint %q", got, want)
			}
		}
	})

	t.Run("focusBrief also right-aligns the gauge", func(t *testing.T) {
		// focusBrief hints are longer than focusTools, so a wider terminal is
		// needed for the full form — exercises the per-focus downgrade threshold.
		m := Model{width: 160, focus: focusBrief, rate: known}
		got := m.renderStatusBar()
		for _, want := range []string{"GitHub API Usage", "45/60", "[L]", "open repo"} {
			if !strings.Contains(got, want) {
				t.Errorf("focusBrief status bar = %q, missing %q", got, want)
			}
		}
	})

	t.Run("input and modal modes suppress the gauge", func(t *testing.T) {
		for _, m := range []Model{
			{width: 120, mode: modeTrack, trackInput: textinput.New(), rate: known},
			{width: 120, mode: modeRename, nameInput: textinput.New(), rate: known},
			{width: 120, mode: modeSearch, search: textinput.New(), rate: known},
			{width: 120, mode: modeEditNote, rate: known},
			{width: 120, mode: modeEditTags, rate: known},
			{width: 120, mode: modeAPIStatus, rate: known},
		} {
			got := m.renderStatusBar()
			if strings.Contains(got, "GitHub API Usage") || strings.Contains(got, "GH ") {
				t.Errorf("input/modal status bar leaked gauge: %q", got)
			}
		}
	})
}

func TestRenderHintsBarAlignment(t *testing.T) {
	known := version.RateLimit{Known: true, Remaining: 15, Limit: 60} // used 45/60
	m := Model{width: 120, rate: known}
	hints := []string{"abc"}

	// A plain (border-less) style isolates the laid-out content; the gap logic
	// inside renderHintsBar uses m.width-2 regardless of the style passed.
	out := m.renderHintsBar(lipgloss.NewStyle(), hints)
	plain := ansiCSI.ReplaceAllString(out, "")
	inner := m.width - 2

	// Fills exactly to the right edge — the gauge is pinned to the corner.
	if w := lipgloss.Width(out); w != inner {
		t.Errorf("laid-out width = %d, want inner %d (gauge not right-aligned)", w, inner)
	}
	// Gauge sits at the far right (full form ends with " details").
	if !strings.HasSuffix(plain, "details") {
		t.Errorf("hints bar = %q, gauge not at the right end", plain)
	}
	// Hints stay on the left with a spacer before the gauge.
	if !strings.HasPrefix(plain, "abc  ") {
		t.Errorf("hints bar = %q, want hints then a spacer on the left", plain)
	}
}

func TestRenderStatusBarTracking(t *testing.T) {
	m := Model{width: 80, mode: modeTrack, trackInput: textinput.New()}
	got := m.renderStatusBar()
	if !strings.Contains(got, "track") {
		t.Errorf("tracking status bar = %q, missing prompt", got)
	}
}

func TestTrackTool(t *testing.T) {
	tests := []struct {
		name       string
		meta       []loader.ToolMeta
		input      string
		wantName   string
		wantGitHub string
		wantLen    int
		wantStatus string
	}{
		{
			name:       "github url derives name and github",
			input:      "https://github.com/anthropics/claude-code",
			wantName:   "claude-code",
			wantGitHub: "github.com/anthropics/claude-code",
			wantLen:    1,
		},
		{
			name:     "plain name has no github",
			input:    "git",
			wantName: "git",
			wantLen:  1,
		},
		{
			name:    "empty input is a no-op",
			input:   "   ",
			wantLen: 0,
		},
		{
			name:       "duplicate updates not duplicates",
			meta:       []loader.ToolMeta{{Name: "git", Status: loader.StatusActive}},
			input:      "git",
			wantName:   "git",
			wantLen:    1,
			wantStatus: "already tracked",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, status := trackTool(tt.meta, tt.input)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if tt.wantName == "" {
				return
			}
			e := loader.FindMeta(got, tt.wantName)
			if e == nil {
				t.Fatalf("expected entry %q in result", tt.wantName)
			}
			if e.GitHub != tt.wantGitHub {
				t.Errorf("github = %q, want %q", e.GitHub, tt.wantGitHub)
			}
			if e.Status != loader.StatusTrying {
				t.Errorf("status field = %q, want %q", e.Status, loader.StatusTrying)
			}
			if e.Added == "" {
				t.Errorf("Added should be set")
			}
		})
	}
}

func TestTrackToolSavePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	meta, _ := trackTool(nil, "git")
	if err := loader.SaveMeta(meta); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	loaded, err := loader.LoadMeta()
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if loader.FindMeta(loaded, "git") == nil {
		t.Errorf("expected git in saved meta")
	}
}

func TestRenderStatusBarConfirmUntrack(t *testing.T) {
	m := Model{width: 80, mode: modeConfirmUntrack, untrackTarget: "git"}
	got := m.renderStatusBar()
	for _, want := range []string{"Untrack", "git", "yes", "no"} {
		if !strings.Contains(got, want) {
			t.Errorf("confirm untrack status bar = %q, missing %q", got, want)
		}
	}
}

func TestRenderStatusBarFocusToolsUntrackHint(t *testing.T) {
	m := Model{width: 80, focus: focusTools}
	if !strings.Contains(m.renderStatusBar(), "untrack") {
		t.Errorf("focusTools status bar missing untrack hint: %q", m.renderStatusBar())
	}
}

func TestUpdateUntrackConfirm(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	enter := tea.KeyMsg{Type: tea.KeyEnter}
	esc := tea.KeyMsg{Type: tea.KeyEsc}

	t.Run("enter removes and clamps selection to next item", func(t *testing.T) {
		m := Model{
			meta: []loader.ToolMeta{
				{Name: "a"}, {Name: "b"}, {Name: "c"},
			},
			metaSelected:  1,
			mode:          modeConfirmUntrack,
			untrackTarget: "b",
		}
		m.tools = loader.ToolsFromMeta(m.meta)

		updated, _ := m.updateUntrackConfirm(enter)
		nm := updated.(Model)

		if nm.mode == modeConfirmUntrack {
			t.Errorf("confirmingUntrack should be false after enter")
		}
		if loader.FindMeta(nm.meta, "b") != nil {
			t.Errorf("b should be removed")
		}
		if len(nm.meta) != 2 {
			t.Fatalf("len = %d, want 2", len(nm.meta))
		}
		// selection stays at index 1, now pointing at "c".
		if nm.metaSelected != 1 {
			t.Errorf("metaSelected = %d, want 1", nm.metaSelected)
		}
	})

	t.Run("enter on last item clamps to new last index", func(t *testing.T) {
		m := Model{
			meta:          []loader.ToolMeta{{Name: "a"}, {Name: "b"}},
			metaSelected:  1,
			mode:          modeConfirmUntrack,
			untrackTarget: "b",
		}
		m.tools = loader.ToolsFromMeta(m.meta)

		updated, _ := m.updateUntrackConfirm(enter)
		nm := updated.(Model)

		if nm.metaSelected != 0 {
			t.Errorf("metaSelected = %d, want 0", nm.metaSelected)
		}
	})

	t.Run("esc cancels and leaves list unchanged", func(t *testing.T) {
		m := Model{
			meta:          []loader.ToolMeta{{Name: "a"}, {Name: "b"}},
			metaSelected:  0,
			mode:          modeConfirmUntrack,
			untrackTarget: "a",
		}
		m.tools = loader.ToolsFromMeta(m.meta)

		updated, _ := m.updateUntrackConfirm(esc)
		nm := updated.(Model)

		if nm.mode == modeConfirmUntrack {
			t.Errorf("confirmingUntrack should be false after esc")
		}
		if len(nm.meta) != 2 || loader.FindMeta(nm.meta, "a") == nil {
			t.Errorf("list should be unchanged after esc, got %v", nm.meta)
		}
	})
}

func TestGaugeFilled(t *testing.T) {
	// Fixed width independent of the limit: 25% used fills the same at 60 and 5000.
	if a, b := gaugeFilled(15, 60), gaugeFilled(1250, 5000); a != b {
		t.Errorf("25%% fill differs by limit: 60→%d, 5000→%d", a, b)
	}
	tests := []struct {
		used, limit, want int
	}{
		{0, 60, 0},
		{60, 60, gaugeCells}, // exhausted → full bar
		{30, 60, gaugeCells / 2},
		{-5, 60, 0},                  // never negative
		{99, 60, gaugeCells},         // over-limit stays full
		{5, 0, 0},                    // no divide-by-zero
		{1, 60, 1},                   // any usage shows at least one cell…
		{1, 5000, 1},                 // …even when the ratio rounds to zero
		{59, 60, gaugeCells - 1},     // full bar means exhaustion only…
		{4999, 5000, gaugeCells - 1}, // …however close the ratio rounds to it
	}
	for _, tt := range tests {
		if got := gaugeFilled(tt.used, tt.limit); got != tt.want {
			t.Errorf("gaugeFilled(%d,%d) = %d, want %d", tt.used, tt.limit, got, tt.want)
		}
	}
}

func TestRenderRateGauge(t *testing.T) {
	t.Run("full form shows label, used/limit and [L]", func(t *testing.T) {
		m := Model{rate: version.RateLimit{Known: true, Remaining: 15, Limit: 60}}
		got := m.renderRateGauge(false)
		for _, want := range []string{"GitHub API Usage", "45/60", "[L]"} {
			if !strings.Contains(got, want) {
				t.Errorf("full gauge = %q, missing %q", got, want)
			}
		}
	})

	t.Run("compact form is shorter and shows GH used/limit [L]", func(t *testing.T) {
		m := Model{rate: version.RateLimit{Known: true, Remaining: 15, Limit: 60}}
		full := m.renderRateGauge(false)
		compact := m.renderRateGauge(true)
		if !strings.Contains(compact, "GH ") || !strings.Contains(compact, "45/60") || !strings.Contains(compact, "[L]") {
			t.Errorf("compact gauge = %q, missing parts", compact)
		}
		if lipgloss.Width(compact) >= lipgloss.Width(full) {
			t.Errorf("compact (%d) not shorter than full (%d)", lipgloss.Width(compact), lipgloss.Width(full))
		}
	})

	t.Run("exhausted shows 60/60 with a full bar", func(t *testing.T) {
		// Constant-yellow-at-exhaustion is structural (renderRateGauge has no
		// pressure branch); gaugeFilled(60,60)==gaugeCells is covered separately.
		m := Model{rate: version.RateLimit{Known: true, Remaining: 0, Limit: 60}}
		got := m.renderRateGauge(false)
		if !strings.Contains(got, "60/60") {
			t.Errorf("exhausted gauge = %q, want 60/60", got)
		}
		// Strip ANSI before checking the bar is a full gaugeCells-wide fill
		// block between the brackets.
		plain := ansiCSI.ReplaceAllString(got, "")
		if !strings.Contains(plain, "["+strings.Repeat(gaugeFillGlyph, gaugeCells)+"]") {
			t.Errorf("exhausted gauge = %q, want a full %d-cell bar", plain, gaugeCells)
		}
	})

	t.Run("partial fill draws fill and track glyphs", func(t *testing.T) {
		// 30/60 → exactly half the bar; glyphs survive ANSI stripping, so the
		// bar stays visible on degraded color profiles.
		m := Model{rate: version.RateLimit{Known: true, Remaining: 30, Limit: 60}}
		plain := ansiCSI.ReplaceAllString(m.renderRateGauge(false), "")
		want := "[" + strings.Repeat(gaugeFillGlyph, gaugeCells/2) + strings.Repeat(gaugeTrackGlyph, gaugeCells/2) + "]"
		if !strings.Contains(plain, want) {
			t.Errorf("half-used gauge = %q, want bar %q", plain, want)
		}
	})

	t.Run("unknown snapshot renders nothing", func(t *testing.T) {
		m := Model{rate: version.RateLimit{Known: false, Remaining: 0, Limit: 60}}
		if got := m.renderRateGauge(false); got != "" {
			t.Errorf("unknown gauge = %q, want empty", got)
		}
	})
}

// TestRenderRateGaugeColors pins the gauge's fill/track color distinction. It
// asserts the isolated styles, not the full gauge string: the brackets and the
// used/limit number also emit foreground ColorOrange (RateBracketStyle /
// RateUsageNumStyle), so a fill regression — colorless, merged into the track,
// or back to a painted background — would be masked by them.
func TestRenderRateGaugeColors(t *testing.T) {
	forceColorProfile(t)

	// Expected sequences come from termenv (its hex→RGB conversion rounds, so
	// literal palette bytes would be brittle): "38;2;r;g;b" foreground form.
	fgSeq := func(c lipgloss.Color) string {
		return termenv.TrueColor.Color(string(c)).Sequence(false)
	}
	fillSeq, trackSeq := fgSeq(ui.ColorOrange), fgSeq(ui.ColorOrangeDim)

	fill := ui.RateGaugeFillStyle.Render(gaugeFillGlyph)
	if !strings.Contains(fill, fillSeq) {
		t.Errorf("fill = %q, missing foreground sequence %q", fill, fillSeq)
	}
	if strings.Contains(fill, "48;2;") {
		t.Errorf("fill = %q, must color the glyph, not paint a background", fill)
	}

	track := ui.RateGaugeTrackStyle.Render(gaugeTrackGlyph)
	if !strings.Contains(track, trackSeq) {
		t.Errorf("track = %q, missing foreground sequence %q", track, trackSeq)
	}
	if strings.Contains(track, "48;2;") {
		t.Errorf("track = %q, must color the glyph, not paint a background", track)
	}

	if fillSeq == trackSeq {
		t.Error("fill and track resolve to the same color — the empty track would be indistinguishable")
	}
}

// TestGaugeGlyphWidthsStable pins that the bar glyphs are not
// East-Asian-Ambiguous: lipgloss.Width must report one cell per glyph even
// under RUNEWIDTH_EASTASIAN=1, or renderHintsBar's gap math would inflate and
// wrongly downgrade or mis-pad the gauge (a full block █ measures as two cells
// there). The width tables read the env var once at package init, so the
// ambiguous-width variant re-runs this test in a child process.
func TestGaugeGlyphWidthsStable(t *testing.T) {
	for _, g := range []string{gaugeFillGlyph, gaugeTrackGlyph} {
		if w := lipgloss.Width(g); w != 1 {
			t.Errorf("glyph %q width = %d, want 1", g, w)
		}
	}
	if os.Getenv("KEYS_WIDTH_CHECK_CHILD") == "1" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run", "^TestGaugeGlyphWidthsStable$")
	cmd.Env = append(os.Environ(), "KEYS_WIDTH_CHECK_CHILD=1", "RUNEWIDTH_EASTASIAN=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("glyph widths change under RUNEWIDTH_EASTASIAN=1:\n%s", out)
	}
}

func TestListNavigationWraps(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	keyJ := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}
	keyK := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}

	newModel := func(meta []loader.ToolMeta, sel int) Model {
		m := Model{width: 80, height: 24, focus: focusTools, ready: true, meta: meta, metaSelected: sel}
		m.tools = loader.ToolsFromMeta(m.meta)
		return m
	}

	t.Run("down from last wraps to first", func(t *testing.T) {
		m := newModel([]loader.ToolMeta{{Name: "a"}, {Name: "b"}, {Name: "c"}}, 2)
		nm := mustModel(m.Update(keyJ))
		if nm.metaSelected != 0 {
			t.Errorf("metaSelected = %d, want 0 (wrap to first)", nm.metaSelected)
		}
	})

	t.Run("up from first wraps to last", func(t *testing.T) {
		m := newModel([]loader.ToolMeta{{Name: "a"}, {Name: "b"}, {Name: "c"}}, 0)
		nm := mustModel(m.Update(keyK))
		if nm.metaSelected != 2 {
			t.Errorf("metaSelected = %d, want 2 (wrap to last)", nm.metaSelected)
		}
	})

	t.Run("single item stays put both directions", func(t *testing.T) {
		m := newModel([]loader.ToolMeta{{Name: "a"}}, 0)
		if nm := mustModel(m.Update(keyJ)); nm.metaSelected != 0 {
			t.Errorf("down: metaSelected = %d, want 0", nm.metaSelected)
		}
		if nm := mustModel(m.Update(keyK)); nm.metaSelected != 0 {
			t.Errorf("up: metaSelected = %d, want 0", nm.metaSelected)
		}
	})

	t.Run("empty list does not panic", func(t *testing.T) {
		m := newModel(nil, 0)
		_ = mustModel(m.Update(keyJ))
		_ = mustModel(m.Update(keyK))
	})
}

func mustModel(tm tea.Model, _ tea.Cmd) Model {
	return tm.(Model)
}

// TestHelpMissingSourceMessages verifies that a mode whose source is absent
// (no man page, or no --help output) surfaces an explicit, tool-named message
// with a cross-hint to the other mode — instead of silently showing nothing or
// the other mode's content.
func TestHelpMissingSourceMessages(t *testing.T) {
	base := func(mode int) Model {
		m := Model{
			width: 120, height: 24, helpW: 60,
			meta:         []loader.ToolMeta{{Name: "agterm"}},
			metaSelected: 0,
			focus:        focusHelp,
			helpMode:     mode,
			helpCache:    map[string][2]string{},
		}
		m.tools = loader.ToolsFromMeta(m.meta)
		return m
	}

	t.Run("man mode with no page names the tool and points to [h]", func(t *testing.T) {
		m := base(helpModeMan)
		nm := mustModel(m.Update(helpOutputMsg{toolName: "agterm", mode: helpModeMan, err: errBoom}))
		plain := ansiCSI.ReplaceAllString(nm.renderHelpContent(), "")
		if !strings.Contains(plain, "No man page for agterm") {
			t.Errorf("man message = %q, want explicit no-man-page", plain)
		}
		if !strings.Contains(plain, "[h]") {
			t.Errorf("man message = %q, want cross-hint to --help", plain)
		}
	})

	t.Run("help mode with no output names the tool and points to [m]", func(t *testing.T) {
		m := base(helpModeHelp)
		nm := mustModel(m.Update(helpOutputMsg{toolName: "agterm", mode: helpModeHelp, err: errBoom}))
		plain := ansiCSI.ReplaceAllString(nm.renderHelpContent(), "")
		if !strings.Contains(plain, "No --help output for agterm") {
			t.Errorf("help message = %q, want explicit no-help", plain)
		}
		if !strings.Contains(plain, "[m]") {
			t.Errorf("help message = %q, want cross-hint to man", plain)
		}
	})
}

func TestRenderStatusBarRenaming(t *testing.T) {
	m := Model{width: 80, mode: modeRename, nameInput: textinput.New()}
	got := m.renderStatusBar()
	if !strings.Contains(got, "rename to") {
		t.Errorf("renaming status bar = %q, missing prompt", got)
	}
}

func TestRenderStatusBarFocusToolsRenameHint(t *testing.T) {
	m := Model{width: 80, focus: focusTools}
	if !strings.Contains(m.renderStatusBar(), "rename") {
		t.Errorf("focusTools status bar missing rename hint: %q", m.renderStatusBar())
	}
}

func TestRenameTool(t *testing.T) {
	t.Run("changes name and preserves other fields", func(t *testing.T) {
		meta := []loader.ToolMeta{
			{Name: "claude-code", GitHub: "github.com/anthropics/claude-code", Status: loader.StatusActive, Tags: []string{"ai"}, Note: "n", Added: "2026-01-01"},
		}
		got, err := renameTool(meta, "claude-code", "claude")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		e := loader.FindMeta(got, "claude")
		if e == nil {
			t.Fatalf("expected entry 'claude'")
		}
		if e.GitHub != "github.com/anthropics/claude-code" {
			t.Errorf("github = %q, want preserved", e.GitHub)
		}
		if e.Status != loader.StatusActive {
			t.Errorf("status = %q, want preserved", e.Status)
		}
		if len(e.Tags) != 1 || e.Tags[0] != "ai" || e.Note != "n" || e.Added != "2026-01-01" {
			t.Errorf("fields not preserved: %+v", e)
		}
		if loader.FindMeta(got, "claude-code") != nil {
			t.Errorf("old name should be gone")
		}
	})

	t.Run("empty is a no-op", func(t *testing.T) {
		meta := []loader.ToolMeta{{Name: "git"}}
		got, err := renameTool(meta, "git", "   ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if loader.FindMeta(got, "git") == nil {
			t.Errorf("git should be unchanged")
		}
	})

	t.Run("collision is rejected and leaves entry unchanged", func(t *testing.T) {
		meta := []loader.ToolMeta{{Name: "a", GitHub: "x"}, {Name: "b"}}
		got, err := renameTool(meta, "a", "b")
		if err == nil {
			t.Fatalf("expected collision error")
		}
		e := loader.FindMeta(got, "a")
		if e == nil || e.GitHub != "x" {
			t.Errorf("entry 'a' should be unchanged, got %+v", e)
		}
	})
}

func TestRenameToolSavePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	meta := []loader.ToolMeta{{Name: "git", Status: loader.StatusActive}}
	got, err := renameTool(meta, "git", "g")
	if err != nil {
		t.Fatalf("renameTool: %v", err)
	}
	if err := loader.SaveMeta(got); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	loaded, err := loader.LoadMeta()
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if loader.FindMeta(loaded, "g") == nil {
		t.Errorf("expected renamed 'g' in saved meta")
	}
	if loader.FindMeta(loaded, "git") != nil {
		t.Errorf("old 'git' should not be in saved meta")
	}
}

func TestUpdateBriefOpenActions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	keyO := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")}
	keyC := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}

	t.Run("no repo sets status message and no command", func(t *testing.T) {
		shrinkStatusTTL(t)
		for _, key := range []tea.KeyMsg{keyO, keyC} {
			m := Model{
				meta:         []loader.ToolMeta{{Name: "tool-x"}},
				metaSelected: 0,
				focus:        focusBrief,
			}
			m.tools = loader.ToolsFromMeta(m.meta)

			updated, cmd := m.Update(key)
			nm := updated.(Model)

			if nm.statusMsg != "no repo for tool-x" {
				t.Errorf("key %q: statusMsg = %q, want %q", key.String(), nm.statusMsg, "no repo for tool-x")
			}
			// No repo → no browser open, only the transient-status expiry tick.
			assertOnlyExpiryTick(t, cmd)
		}
	})

	t.Run("repo set returns a non-nil command", func(t *testing.T) {
		for _, key := range []tea.KeyMsg{keyO, keyC} {
			m := Model{
				meta:         []loader.ToolMeta{{Name: "tool-x", GitHub: "github.com/owner/tool-x"}},
				metaSelected: 0,
				focus:        focusBrief,
			}
			m.tools = loader.ToolsFromMeta(m.meta)

			updated, cmd := m.Update(key)
			nm := updated.(Model)

			if nm.statusMsg != "" {
				t.Errorf("key %q: statusMsg = %q, want empty", key.String(), nm.statusMsg)
			}
			if cmd == nil {
				t.Errorf("key %q: cmd = nil, want non-nil for tool with repo", key.String())
			}
		}
	})
}

func TestUpdateBriefStatusCycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	keyS := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}

	t.Run("cycles status through the full loop", func(t *testing.T) {
		m := Model{
			meta:         []loader.ToolMeta{{Name: "tool-x", Status: loader.StatusActive}},
			metaSelected: 0,
			focus:        focusBrief,
		}
		m.tools = loader.ToolsFromMeta(m.meta)

		want := []loader.Status{
			loader.StatusTrying,
			loader.StatusInactive,
			loader.StatusActive,
		}

		var cur tea.Model = m
		for i, w := range want {
			updated, _ := cur.(Model).Update(keyS)
			nm := updated.(Model)
			got := loader.FindMeta(nm.meta, "tool-x").Status
			if got != w {
				t.Errorf("step %d: status = %q, want %q", i, got, w)
			}
			cur = nm
		}
	})

	t.Run("inert outside focusBrief", func(t *testing.T) {
		m := Model{
			meta:         []loader.ToolMeta{{Name: "tool-x", Status: loader.StatusActive}},
			metaSelected: 0,
			focus:        focusTools,
		}
		m.tools = loader.ToolsFromMeta(m.meta)

		updated, _ := m.Update(keyS)
		nm := updated.(Model)
		if got := loader.FindMeta(nm.meta, "tool-x").Status; got != loader.StatusActive {
			t.Errorf("status = %q, want %q (unchanged outside focusBrief)", got, loader.StatusActive)
		}
	})
}

func TestScrollColumn(t *testing.T) {
	const thumb = "▐"

	t.Run("no thumb when content fits", func(t *testing.T) {
		vp := viewport.New(10, 5)
		vp.SetContent("one\ntwo")
		if got := scrollColumn(vp, true); strings.Contains(got, thumb) {
			t.Errorf("expected no thumb for non-scrollable content, got %q", got)
		}
	})

	t.Run("thumb when content overflows", func(t *testing.T) {
		vp := viewport.New(10, 3)
		vp.SetContent(strings.Repeat("line\n", 20))
		if got := scrollColumn(vp, true); !strings.Contains(got, thumb) {
			t.Errorf("expected thumb for scrollable content, got %q", got)
		}
	})
}

// countBatchedCmds executes cmd and reports how many commands it batches.
// A nil cmd counts as 0; a single non-batch cmd counts as 1. Only call this
// when the batched cmds are side-effect free to execute (or when a BatchMsg is
// expected), since tea.Batch collapses a lone cmd into that cmd directly.
func countBatchedCmds(cmd tea.Cmd) int {
	if cmd == nil {
		return 0
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		return len(msg)
	default:
		return 1
	}
}

func TestFetchInstalledCmd(t *testing.T) {
	// A nonexistent name makes InstalledVersion skip exec, so the closure runs
	// with no network I/O and never touches GitHub.
	cmd := fetchInstalledCmd(loader.Tool{Name: "nonexistent-tool-xyz", GitHub: ""})
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd from fetchInstalledCmd")
	}
	msg, ok := cmd().(installedMsg)
	if !ok {
		t.Fatalf("expected installedMsg, got %T", cmd())
	}
	if msg.toolName != "nonexistent-tool-xyz" {
		t.Errorf("toolName = %q, want %q", msg.toolName, "nonexistent-tool-xyz")
	}
	// The command must carry InstalledVersion's second result, not just the
	// version: a tool that is not on PATH is the "not installed" state the card
	// renders differently from a version-less install.
	if msg.present {
		t.Error("present = true for a tool that does not exist")
	}

	// false is the zero value, so the assertion above cannot catch a dropped
	// present on its own — pin the true case against a binary that really is on
	// PATH but answers no version, the state the two differ in.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "presenttool"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	hit, ok := fetchInstalledCmd(loader.Tool{Name: "presenttool"})().(installedMsg)
	if !ok {
		t.Fatalf("expected installedMsg, got %T", hit)
	}
	if hit.installed != "" {
		t.Errorf("installed = %q, want empty — the fake answers no version", hit.installed)
	}
	if !hit.present {
		t.Error("present = false for a binary that is on PATH")
	}
}

// TestInstalledMsgCarriesPresence pins the plumbing between the probe and the
// card: fetchInstalledCmd's present flag must survive the handler into
// VersionInfo and reach the rendered installed: line. Without this the
// assignment could be dropped and every other test would stay green — the
// render tests build VersionInfo directly.
func TestInstalledMsgCarriesPresence(t *testing.T) {
	newModel := func() Model {
		m := New([]loader.ToolMeta{{Name: "gh", GitHub: "cli/cli"}})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
		return updated.(Model)
	}

	tests := []struct {
		name    string
		present bool
		want    string
		notWant string
	}{
		{"installed but version-less", true, "installed: ✓ no version", "not installed"},
		{"absent", false, "installed: ✕ not installed", "no version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel()
			updated, _ := m.Update(installedMsg{toolName: "gh", installed: "", present: tt.present})
			nm := updated.(Model)

			info := nm.versions["gh"]
			if info.InstalledPresent != tt.present {
				t.Errorf("InstalledPresent = %v, want %v", info.InstalledPresent, tt.present)
			}
			if !info.InstalledKnown {
				t.Error("InstalledKnown = false, want true — the probe reported back")
			}
			card := stripANSI(nm.renderCard())
			if !strings.Contains(card, tt.want) {
				t.Errorf("card missing %q; got:\n%s", tt.want, card)
			}
			if strings.Contains(card, tt.notWant) {
				t.Errorf("card shows the other presence state (%q); got:\n%s", tt.notWant, card)
			}
		})
	}
}

func TestNeedsInstalled(t *testing.T) {
	tests := []struct {
		name     string
		tool     loader.Tool
		versions map[string]VersionInfo
		want     bool
	}{
		{
			name: "fresh tool needs installed",
			tool: loader.Tool{Name: "git", GitHub: "cli/cli"},
			want: true,
		},
		{
			name:     "known installed does not need refetch",
			tool:     loader.Tool{Name: "git", GitHub: "cli/cli"},
			versions: map[string]VersionInfo{"git": {Installed: "1.0", InstalledKnown: true}},
			want:     false,
		},
		{
			name:     "probed-but-missing does not need refetch",
			tool:     loader.Tool{Name: "git", GitHub: "cli/cli"},
			versions: map[string]VersionInfo{"git": {Installed: "", InstalledKnown: true}},
			want:     false,
		},
		{
			name:     "entry with only Latest still needs installed",
			tool:     loader.Tool{Name: "git", GitHub: "cli/cli"},
			versions: map[string]VersionInfo{"git": {Latest: "2.0"}},
			want:     true,
		},
		{
			name: "installed fires even without GitHub",
			tool: loader.Tool{Name: "git", GitHub: ""},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{versions: tt.versions}
			if got := m.needsInstalled(tt.tool); got != tt.want {
				t.Errorf("needsInstalled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNeedsRemote(t *testing.T) {
	tests := []struct {
		name      string
		tool      loader.Tool
		repoCards map[string]version.RepoCard
		versions  map[string]VersionInfo
		want      bool
	}{
		{
			name: "fresh tool with GitHub needs remote",
			tool: loader.Tool{Name: "git", GitHub: "cli/cli"},
			want: true,
		},
		{
			name:      "card and latest present: no remote",
			tool:      loader.Tool{Name: "git", GitHub: "cli/cli"},
			repoCards: map[string]version.RepoCard{"git": {}},
			versions:  map[string]VersionInfo{"git": {Latest: "2.0"}},
			want:      false,
		},
		{
			name:      "card present but latest empty: needs remote",
			tool:      loader.Tool{Name: "git", GitHub: "cli/cli"},
			repoCards: map[string]version.RepoCard{"git": {}},
			versions:  map[string]VersionInfo{"git": {Installed: "1.0"}},
			want:      true,
		},
		{
			name: "remote not needed without GitHub",
			tool: loader.Tool{Name: "git", GitHub: ""},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{repoCards: tt.repoCards, versions: tt.versions}
			if got := m.needsRemote(tt.tool); got != tt.want {
				t.Errorf("needsRemote() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutoFetchCmdsForSelected_QueuesFetches(t *testing.T) {
	name := "git"
	// Changelog and --help are already cached so those branches append nothing;
	// only version + repo card are missing. This isolates the new fetch block:
	// a non-nil batch alone would pass even without it (changelog/help fire too),
	// so assert the batch holds exactly the two expected commands.
	m := &Model{
		meta:          []loader.ToolMeta{{Name: name, GitHub: "cli/cli"}},
		tools:         []loader.Tool{{Name: name, GitHub: "cli/cli"}},
		metaSelected:  0,
		changelogData: map[string]changelogMsg{name: {}},
		helpCache:     map[string][2]string{name: {helpModeHelp: "cached"}},
	}
	cmd := m.autoFetchCmdsForSelected()
	if cmd == nil {
		t.Fatal("expected non-nil batched Cmd queuing version + repo card fetches")
	}
	if got := countBatchedCmds(cmd); got != 2 {
		t.Fatalf("expected exactly 2 queued cmds (version + repo card), got %d", got)
	}
}

func TestAutoFetchCmdsForSelected_NoFetchWhenCached(t *testing.T) {
	name := "git"
	m := &Model{
		meta:          []loader.ToolMeta{{Name: name, GitHub: "cli/cli"}},
		tools:         []loader.Tool{{Name: name, GitHub: "cli/cli"}},
		metaSelected:  0,
		changelogData: map[string]changelogMsg{name: {}},
		helpCache:     map[string][2]string{name: {helpModeHelp: "cached help"}},
		versions:      map[string]VersionInfo{name: {Installed: "1.0", Latest: "2.0", InstalledKnown: true}},
		repoCards:     map[string]version.RepoCard{name: {}},
	}
	if m.needsInstalled(m.tools[0]) {
		t.Error("needsInstalled should be false when installed version is cached")
	}
	if m.needsRemote(m.tools[0]) {
		t.Error("needsRemote should be false when repo card and latest are cached")
	}
	if cmd := m.autoFetchCmdsForSelected(); cmd != nil {
		t.Fatal("expected nil Cmd when all sources are already cached")
	}
}

func TestUpdateRenameInputClearsStaleCaches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	old := "cli"
	newName := "gh"
	m := Model{
		meta:          []loader.ToolMeta{{Name: old, GitHub: "cli/cli"}},
		metaSelected:  0,
		mode:          modeRename,
		nameInput:     textinput.New(),
		repoCards:     map[string]version.RepoCard{old: {}},
		versions:      map[string]VersionInfo{old: {}},
		repoStatus:    map[string]string{old: "ok"},
		changelogData: map[string]changelogMsg{old: {}},
		helpCache:     map[string][2]string{old: {helpModeHelp: "cached"}},
		readmeData:    map[string]readmeMsg{old: {content: "# cli"}},
	}
	m.tools = loader.ToolsFromMeta(m.meta)
	m.nameInput.SetValue(newName)

	updated, _ := m.updateRenameInput(tea.KeyMsg{Type: tea.KeyEnter})
	nm := updated.(Model)

	if _, ok := nm.repoCards[old]; ok {
		t.Errorf("repoCards still holds stale old-name key %q after rename", old)
	}
	if _, ok := nm.versions[old]; ok {
		t.Errorf("versions still holds stale old-name key %q after rename", old)
	}
	if _, ok := nm.repoStatus[old]; ok {
		t.Errorf("repoStatus still holds stale old-name key %q after rename", old)
	}
	if _, ok := nm.changelogData[old]; ok {
		t.Errorf("changelogData still holds stale old-name key %q after rename", old)
	}
	if _, ok := nm.helpCache[old]; ok {
		t.Errorf("helpCache still holds stale old-name key %q after rename", old)
	}
	if _, ok := nm.readmeData[old]; ok {
		t.Errorf("readmeData still holds stale old-name key %q after rename", old)
	}
}

// TestUpdateInstalledAndRemoteMsgPopulateCaches closes the loop the rename test
// opens: after stale keys are cleared, the async fetch results must repopulate
// the caches under the (new) tool name. It also proves installedMsg and
// remoteMsg merge into one VersionInfo without either clobbering the other's
// field, in both arrival orders.
func TestUpdateInstalledAndRemoteMsgPopulateCaches(t *testing.T) {
	newModel := func() Model {
		m := Model{
			meta:          []loader.ToolMeta{{Name: "gh", GitHub: "cli/cli"}},
			metaSelected:  0,
			versions:      map[string]VersionInfo{},
			repoStatus:    map[string]string{},
			repoCards:     map[string]version.RepoCard{},
			changelogData: map[string]changelogMsg{},
		}
		m.tools = loader.ToolsFromMeta(m.meta)
		return m
	}

	// installed first, then remote.
	m := newModel()
	updated, _ := m.Update(installedMsg{toolName: "gh", installed: "1.0"})
	nm := updated.(Model)
	if got := nm.versions["gh"]; got.Installed != "1.0" {
		t.Errorf("after installedMsg versions[gh].Installed = %q, want 1.0", got.Installed)
	}
	updated, _ = nm.Update(remoteMsg{toolName: "gh", latest: "2.0", repoStatus: "active", card: version.RepoCard{About: "x"}})
	nm = updated.(Model)
	if got := nm.versions["gh"]; got.Installed != "1.0" || got.Latest != "2.0" {
		t.Errorf("versions[gh] = %+v, want {Installed:1.0 Latest:2.0}", got)
	}
	if got := nm.repoStatus["gh"]; got != "active" {
		t.Errorf("repoStatus[gh] = %q, want active", got)
	}
	if got, ok := nm.repoCards["gh"]; !ok || got.About != "x" {
		t.Errorf("repoCards[gh] = %+v (ok=%v), want About:x", got, ok)
	}

	// remote first, then installed — installed must not wipe Latest.
	m = newModel()
	updated, _ = m.Update(remoteMsg{toolName: "gh", latest: "2.0", card: version.RepoCard{}})
	nm = updated.(Model)
	updated, _ = nm.Update(installedMsg{toolName: "gh", installed: "1.0"})
	nm = updated.(Model)
	if got := nm.versions["gh"]; got.Installed != "1.0" || got.Latest != "2.0" {
		t.Errorf("reversed order versions[gh] = %+v, want {Installed:1.0 Latest:2.0}", got)
	}

	// remoteMsg with err set must not touch the caches.
	m = newModel()
	updated, _ = m.Update(remoteMsg{toolName: "gh", latest: "2.0", err: errBoom})
	nm = updated.(Model)
	if _, ok := nm.repoCards["gh"]; ok {
		t.Errorf("repoCards populated despite remoteMsg error")
	}
	if got := nm.versions["gh"]; got.Latest != "" {
		t.Errorf("versions[gh].Latest = %q, want empty on remoteMsg error", got.Latest)
	}
}

var errBoom = errors.New("boom")

func newRateModel() Model {
	m := Model{
		meta:          []loader.ToolMeta{{Name: "gh", GitHub: "cli/cli"}},
		metaSelected:  0,
		versions:      map[string]VersionInfo{},
		repoStatus:    map[string]string{},
		repoCards:     map[string]version.RepoCard{},
		changelogData: map[string]changelogMsg{},
	}
	m.tools = loader.ToolsFromMeta(m.meta)
	return m
}

// TestRemoteMsgRateMerge verifies the non-clobber merge: a Known snapshot is
// stored, and a later Known==false snapshot (a cache-hit remote fetch) does not
// wipe it.
func TestRemoteMsgRateMerge(t *testing.T) {
	known := version.RateLimit{Limit: 5000, Remaining: 4999, Known: true}
	m := newRateModel()
	updated, _ := m.Update(remoteMsg{toolName: "gh", latest: "2.0", rate: known})
	nm := updated.(Model)
	if nm.rate != known {
		t.Fatalf("m.rate = %+v, want %+v", nm.rate, known)
	}

	// A Known==false snapshot must not overwrite the known value.
	updated, _ = nm.Update(remoteMsg{toolName: "gh", latest: "2.1", rate: version.RateLimit{}})
	nm = updated.(Model)
	if nm.rate != known {
		t.Errorf("Known==false remoteMsg clobbered m.rate: got %+v, want %+v", nm.rate, known)
	}
}

// TestRateMsgHandler verifies rateMsg stores a Known snapshot and that an error
// (or Known==false) leaves a previously known m.rate untouched.
func TestRateMsgHandler(t *testing.T) {
	known := version.RateLimit{Limit: 5000, Remaining: 100, Known: true}
	m := newRateModel()
	updated, _ := m.Update(rateMsg{rate: known})
	nm := updated.(Model)
	if nm.rate != known {
		t.Fatalf("after rateMsg m.rate = %+v, want %+v", nm.rate, known)
	}

	// An error snapshot must not clobber the known value.
	updated, _ = nm.Update(rateMsg{rate: version.RateLimit{Limit: 60, Known: true}, err: errBoom})
	nm = updated.(Model)
	if nm.rate != known {
		t.Errorf("errored rateMsg clobbered m.rate: got %+v, want %+v", nm.rate, known)
	}
}

// TestRemoteMsgRateLimitedHint verifies that a rate-limited remoteMsg with no
// card sets the "rate-limited" repoStatus so the card can render a hint.
func TestRemoteMsgRateLimitedHint(t *testing.T) {
	m := newRateModel()
	updated, _ := m.Update(remoteMsg{
		toolName:   "gh",
		repoStatus: "rate-limited",
		rate:       version.RateLimit{Limit: 60, Remaining: 0, Known: true},
		err:        version.ErrRateLimited,
	})
	nm := updated.(Model)
	if got := nm.repoStatus["gh"]; got != "rate-limited" {
		t.Errorf("repoStatus[gh] = %q, want rate-limited", got)
	}
	if _, ok := nm.repoCards["gh"]; ok {
		t.Errorf("repoCards populated despite rate-limited error")
	}
	if !nm.rate.Known || nm.rate.Remaining != 0 {
		t.Errorf("m.rate = %+v, want Known with Remaining 0", nm.rate)
	}
	// The card must actually render the hint, not just set the internal map.
	if card := stripANSI(nm.renderCard()); !strings.Contains(card, "rate limited — press [L]") {
		t.Errorf("renderCard() missing rate-limit hint; got:\n%s", card)
	}
}

// TestRemoteMsgRateLimitedKeepsStaleData verifies that a rate-limit error
// accompanied by usable stale/partial cache data still populates the caches
// (known tags and cards must survive a rate-limited outage), rather than being
// dropped in favour of the empty "rate limited" hint.
func TestRemoteMsgRateLimitedKeepsStaleData(t *testing.T) {
	m := newRateModel()
	updated, _ := m.Update(remoteMsg{
		toolName:   "gh",
		latest:     "2.0",
		repoStatus: "active",
		card:       version.RepoCard{About: "stale about", Latest: "2.0"},
		err:        version.ErrRateLimited,
	})
	nm := updated.(Model)
	if got := nm.versions["gh"]; got.Latest != "2.0" {
		t.Errorf("versions[gh].Latest = %q, want 2.0 (stale data dropped)", got.Latest)
	}
	if got := nm.repoStatus["gh"]; got != "active" {
		t.Errorf("repoStatus[gh] = %q, want active", got)
	}
	if got, ok := nm.repoCards["gh"]; !ok || got.About != "stale about" {
		t.Errorf("repoCards[gh] = %+v (ok=%v), want About:stale about", got, ok)
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"ghp_1234567890abcdef3f2a", "ghp_••••••••3f2a"},
		{"12345678", "••••••••"},
		{"abc", "•••"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := maskToken(tt.in); got != tt.want {
			t.Errorf("maskToken(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRenderAPIStatusOverlay(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_1234567890abcdef3f2a")

	m := Model{width: 80, height: 24, mode: modeAPIStatus}
	m.rate = version.RateLimit{Known: true, Remaining: 0, Limit: 60}
	got := m.renderAPIStatus()

	// Used/limit (not remaining): Remaining 0 of 60 → "60 / 60".
	for _, want := range []string{"GitHub API status", "env", "ghp_••••••••3f2a", "Used: 60 / 60", "✕", "[e]", "[r]", "[esc]"} {
		if !strings.Contains(got, want) {
			t.Errorf("overlay = %q, missing %q", got, want)
		}
	}
	// [d] remove token is hidden for the env source.
	if strings.Contains(got, "remove token") {
		t.Errorf("overlay = %q, should not offer remove token for env source", got)
	}
	// Token hint is hidden when a token is configured (env source here).
	if strings.Contains(got, "raise the limit") {
		t.Errorf("overlay = %q, should not show token hint when a token exists", got)
	}
}

func TestRenderAPIStatusUsedLimit(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_1234567890abcdef3f2a")
	m := Model{width: 80, height: 24, mode: modeAPIStatus}
	m.rate = version.RateLimit{Known: true, Remaining: 15, Limit: 60}
	got := m.renderAPIStatus()
	if !strings.Contains(got, "Used: 45 / 60") {
		t.Errorf("overlay = %q, want used/limit line 'Used: 45 / 60'", got)
	}
	if strings.Contains(got, "Limit: 15") {
		t.Errorf("overlay = %q, should not show remaining as the count", got)
	}
}

func TestRenderAPIStatusTokenHint(t *testing.T) {
	t.Run("shown when no token", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("GITHUB_TOKEN", "")
		if version.TokenSource() != "none" {
			t.Skipf("precondition: TokenSource() = %q, want none", version.TokenSource())
		}
		m := Model{width: 80, height: 24, mode: modeAPIStatus}
		m.rate = version.RateLimit{Known: true, Remaining: 30, Limit: 60}
		if got := m.renderAPIStatus(); !strings.Contains(got, "raise the limit") {
			t.Errorf("overlay = %q, missing token hint when no token", got)
		}
	})

	t.Run("hidden while entering a token", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("GITHUB_TOKEN", "")
		m := Model{width: 80, height: 24, mode: modeTokenInput, tokenInput: textinput.New()}
		if got := m.renderAPIStatus(); strings.Contains(got, "raise the limit") {
			t.Errorf("overlay = %q, should hide token hint while entering a token", got)
		}
	})
}

// sgrParamRe captures the parameter list of each SGR escape sequence.
var sgrParamRe = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// hasItalic reports whether s contains an SGR sequence enabling italics
// (parameter 3, possibly merged with colors, e.g. "\x1b[3;38;5;145m").
func hasItalic(s string) bool {
	for _, match := range sgrParamRe.FindAllStringSubmatch(s, -1) {
		if slices.Contains(strings.Split(match[1], ";"), "3") {
			return true
		}
	}
	return false
}

// TestRenderAPIStatusHintsNotItalic pins the overlay styling: no part of the
// overlay (in particular the hint line) may render in italics, in either the
// read-only view or the token-input sub-state.
func TestRenderAPIStatusHintsNotItalic(t *testing.T) {
	forceColorProfile(t)
	t.Setenv("GITHUB_TOKEN", "ghp_1234567890abcdef3f2a")

	for _, mode := range []inputMode{modeAPIStatus, modeTokenInput} {
		m := Model{width: 80, height: 24, mode: mode, tokenInput: textinput.New()}
		m.rate = version.RateLimit{Known: true, Remaining: 15, Limit: 60}
		if got := m.renderAPIStatus(); hasItalic(got) {
			t.Errorf("mode %d: overlay contains italic styling: %q", mode, got)
		}
	}
}

func TestRenderAPIStatusWarnIcon(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	m := Model{width: 80, height: 24, mode: modeAPIStatus}
	m.rate = version.RateLimit{Known: true, Remaining: rateLowThreshold, Limit: 60}
	got := m.renderAPIStatus()
	if !strings.Contains(got, "⚠") {
		t.Errorf("overlay = %q, missing warn icon", got)
	}
}

func TestAPIStatusOverlayToggle(t *testing.T) {
	m := Model{width: 80, height: 24, focus: focusTools, ready: true}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("L")})
	nm := updated.(Model)
	if nm.mode != modeAPIStatus {
		t.Fatalf("pressing L did not open the API-status overlay")
	}
	if cmd == nil {
		t.Errorf("pressing L should fire a rate fetch cmd")
	}
	// esc closes it.
	updated2, _ := nm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated2.(Model).apiOverlayVisible() {
		t.Errorf("esc did not close the API-status overlay")
	}
}

// TestUpdateAPIStatusOpensTokenEntry verifies [e] switches the overlay into the
// masked token-input sub-mode.
func TestUpdateAPIStatusOpensTokenEntry(t *testing.T) {
	m := Model{width: 80, height: 24, mode: modeAPIStatus, tokenInput: textinput.New()}
	updated, cmd := m.updateAPIStatus(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	nm := updated.(Model)
	if nm.mode != modeTokenInput {
		t.Fatal("pressing e did not enter token-input mode")
	}
	if cmd == nil {
		t.Error("expected a blink cmd when entering token mode")
	}
}

// TestTokenValidatedMsgInvalid verifies a 401 result shows the inline error,
// keeps the input open, and never persists a token.
func TestTokenValidatedMsgInvalid(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	version.ClearToken() //nolint:errcheck

	m := Model{width: 80, height: 24, mode: modeTokenInput, tokenInput: textinput.New()}
	updated, _ := m.Update(tokenValidatedMsg{token: "ghp_bad", err: version.ErrTokenInvalid})
	nm := updated.(Model)
	if nm.tokenError != "token invalid" {
		t.Errorf("tokenError = %q, want %q", nm.tokenError, "token invalid")
	}
	if nm.mode != modeTokenInput {
		t.Error("invalid token should keep the input open for a retry")
	}
	if src := version.TokenSource(); src != "none" {
		t.Errorf("invalid token must not be stored, TokenSource() = %q", src)
	}
}

// TestTokenValidatedMsgValid verifies a 200 result persists the token, exits the
// input, updates the rate snapshot, and fires the card-backfill cmd.
func TestTokenValidatedMsgValid(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	version.ClearToken()                       //nolint:errcheck
	t.Cleanup(func() { version.ClearToken() }) //nolint:errcheck

	name := "git"
	m := Model{
		width: 80, height: 24, mode: modeTokenInput,
		tokenInput:    textinput.New(),
		tokenError:    "token invalid",
		meta:          []loader.ToolMeta{{Name: name, GitHub: "cli/cli"}},
		tools:         []loader.Tool{{Name: name, GitHub: "cli/cli"}},
		changelogData: map[string]changelogMsg{name: {}},
		helpCache:     map[string][2]string{name: {helpModeHelp: "cached"}},
		versions:      map[string]VersionInfo{},
		repoCards:     map[string]version.RepoCard{},
	}
	rate := version.RateLimit{Known: true, Remaining: 4999, Limit: 5000}
	updated, cmd := m.Update(tokenValidatedMsg{token: "ghp_goodtoken1234", rate: rate})
	nm := updated.(Model)
	if version.TokenSource() != "config" {
		t.Fatalf("valid token was not stored, TokenSource() = %q", version.TokenSource())
	}
	if nm.mode == modeTokenInput {
		t.Error("valid token should exit the token-input mode")
	}
	if nm.tokenError != "" {
		t.Errorf("tokenError should be cleared, got %q", nm.tokenError)
	}
	if nm.rate.Limit != 5000 {
		t.Errorf("m.rate not updated from the validated snapshot, got %+v", nm.rate)
	}
	if cmd == nil {
		t.Error("valid token should fire the backfill cmd")
	}
}

// TestUpdateAPIStatusRemoveToken verifies [d] clears a config-sourced token.
func TestUpdateAPIStatusRemoveToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := version.SetToken("ghp_config1234567"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	t.Cleanup(func() { version.ClearToken() }) //nolint:errcheck
	if version.TokenSource() != "config" {
		t.Fatalf("precondition: TokenSource() = %q, want config", version.TokenSource())
	}

	m := Model{width: 80, height: 24, mode: modeAPIStatus, tokenInput: textinput.New()}
	m.updateAPIStatus(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if src := version.TokenSource(); src != "none" {
		t.Errorf("[d] did not clear the token, TokenSource() = %q", src)
	}
}

// TestRenderStatusBarTokenInput verifies the status bar reflects the token-input
// sub-mode.
func TestRenderStatusBarTokenInput(t *testing.T) {
	m := Model{width: 80, height: 24, mode: modeTokenInput, tokenInput: textinput.New()}
	got := m.renderStatusBar()
	if !strings.Contains(got, "validate & save") {
		t.Errorf("status bar = %q, missing token-input hint", got)
	}
}

// TestRenderAPIStatusTokenEntry verifies the overlay shows the masked input and
// the inline error while entering a token.
func TestRenderAPIStatusTokenEntry(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	m := Model{width: 80, height: 24, mode: modeTokenInput, tokenInput: textinput.New()}
	m.tokenError = "token invalid"
	got := m.renderAPIStatus()
	for _, want := range []string{"token:", "token invalid", "validate & save"} {
		if !strings.Contains(got, want) {
			t.Errorf("overlay = %q, missing %q", got, want)
		}
	}
}

// TestRefreshCmdsEmitTypedMsgs verifies the force-refresh commands emit the same
// message types as their non-force variants, carrying the tool name. Uses an
// empty GitHub field so the version layer returns without any network call.
func TestRefreshCmdsEmitTypedMsgs(t *testing.T) {
	t.Run("remote", func(t *testing.T) {
		msg := refreshRemoteCmd(loader.Tool{Name: "tool"})()
		rm, ok := msg.(remoteMsg)
		if !ok {
			t.Fatalf("refreshRemoteCmd emitted %T, want remoteMsg", msg)
		}
		if rm.toolName != "tool" {
			t.Errorf("remoteMsg.toolName = %q, want tool", rm.toolName)
		}
	})
	t.Run("changelog", func(t *testing.T) {
		msg := refreshChangelogCmd("", "tool")()
		cm, ok := msg.(changelogMsg)
		if !ok {
			t.Fatalf("refreshChangelogCmd emitted %T, want changelogMsg", msg)
		}
		if cm.toolName != "tool" {
			t.Errorf("changelogMsg.toolName = %q, want tool", cm.toolName)
		}
	})
}

// TestSpinnerTickGateWhenIdle verifies a spinner tick is a no-op (no rescheduled
// command) when no refresh is in flight, so the animation loop halts when idle.
func TestSpinnerTickGateWhenIdle(t *testing.T) {
	m := Model{width: 80, focus: focusBrief}
	_, cmd := m.Update(spinner.TickMsg{})
	if cmd != nil {
		t.Errorf("idle spinner tick returned a command %v, want nil (loop should halt)", cmd)
	}
}

// TestUpdateBriefRefresh covers the [r] refresh action in the brief panel: it
// starts a refresh (sets refreshingFor + status) for a repo-backed tool, the
// remoteMsg completion clears it, a no-repo tool only reports status, a repeat
// press is a no-op guard, and [r] in the tool list still starts a rename.
func TestUpdateBriefRefresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	keyR := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}

	t.Run("repo tool starts refresh", func(t *testing.T) {
		m := Model{
			meta:         []loader.ToolMeta{{Name: "tool-x", GitHub: "github.com/owner/tool-x"}},
			metaSelected: 0,
			focus:        focusBrief,
		}
		m.tools = loader.ToolsFromMeta(m.meta)

		updated, cmd := m.Update(keyR)
		nm := updated.(Model)

		if nm.refreshingFor != "tool-x" {
			t.Errorf("refreshingFor = %q, want tool-x", nm.refreshingFor)
		}
		// The status bar is not taken over — the "refreshing" hint lives in the
		// card title, not the status bar.
		if nm.statusMsg != "" {
			t.Errorf("statusMsg = %q, want empty (no status-bar takeover)", nm.statusMsg)
		}
		if cmd == nil {
			t.Error("cmd = nil, want a non-nil refresh batch")
		}
	})

	t.Run("remoteMsg completion clears refresh state", func(t *testing.T) {
		m := Model{
			meta:          []loader.ToolMeta{{Name: "tool-x", GitHub: "github.com/owner/tool-x"}},
			metaSelected:  0,
			focus:         focusBrief,
			refreshingFor: "tool-x",
			versions:      map[string]VersionInfo{},
			repoStatus:    map[string]string{},
			repoCards:     map[string]version.RepoCard{},
		}
		m.tools = loader.ToolsFromMeta(m.meta)

		updated, _ := m.Update(remoteMsg{toolName: "tool-x", latest: "v1.0.0"})
		nm := updated.(Model)

		if nm.refreshingFor != "" {
			t.Errorf("refreshingFor = %q, want cleared after remoteMsg", nm.refreshingFor)
		}
	})

	t.Run("no-repo tool reports status without refresh state", func(t *testing.T) {
		m := Model{
			meta:         []loader.ToolMeta{{Name: "tool-x"}},
			metaSelected: 0,
			focus:        focusBrief,
		}
		m.tools = loader.ToolsFromMeta(m.meta)

		updated, _ := m.Update(keyR)
		nm := updated.(Model)

		if nm.refreshingFor != "" {
			t.Errorf("refreshingFor = %q, want empty for no-repo tool", nm.refreshingFor)
		}
		if nm.statusMsg != "no repo to refresh" {
			t.Errorf("statusMsg = %q, want \"no repo to refresh\"", nm.statusMsg)
		}
	})

	t.Run("repeat press while refreshing is a no-op guard", func(t *testing.T) {
		m := Model{
			meta:          []loader.ToolMeta{{Name: "tool-x", GitHub: "github.com/owner/tool-x"}},
			metaSelected:  0,
			focus:         focusBrief,
			refreshingFor: "tool-x",
		}
		m.tools = loader.ToolsFromMeta(m.meta)

		updated, cmd := m.Update(keyR)
		nm := updated.(Model)

		if nm.refreshingFor != "tool-x" {
			t.Errorf("refreshingFor = %q, want tool-x unchanged", nm.refreshingFor)
		}
		if cmd != nil {
			t.Errorf("cmd = %v, want nil (guarded second press)", cmd)
		}
	})

	t.Run("r in tool list still starts rename", func(t *testing.T) {
		m := Model{
			meta:         []loader.ToolMeta{{Name: "tool-x"}},
			metaSelected: 0,
			focus:        focusTools,
			nameInput:    textinput.New(),
		}
		m.tools = loader.ToolsFromMeta(m.meta)

		updated, _ := m.Update(keyR)
		nm := updated.(Model)

		if nm.mode != modeRename {
			t.Error("renaming = false, want true (r in focusTools opens rename)")
		}
		if nm.refreshingFor != "" {
			t.Errorf("refreshingFor = %q, want empty in focusTools", nm.refreshingFor)
		}
	})
}

// TestBriefHelpBarHasRefresh verifies the focusBrief help bar advertises [r] refresh.
func TestBriefHelpBarHasRefresh(t *testing.T) {
	m := Model{width: 120, focus: focusBrief}
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "refresh") {
		t.Errorf("focusBrief help bar = %q, want it to mention \"refresh\"", bar)
	}
}

// TestRenderCardSpinner verifies that while a tool is refreshing the card title
// becomes "refreshing <name> data <spinner>" (hiding the about), and reverts to
// name + about when idle.
func TestRenderCardSpinner(t *testing.T) {
	m := Model{
		meta:         []loader.ToolMeta{{Name: "tool-x", GitHub: "github.com/owner/tool-x"}},
		metaSelected: 0,
		briefW:       80,
		repoCards:    map[string]version.RepoCard{"tool-x": {About: "a fine tool"}},
	}
	m.tools = loader.ToolsFromMeta(m.meta)
	m.spinner = spinner.New()
	m.spinner.Spinner = spinner.MiniDot
	frame := m.spinner.View()

	m.refreshingFor = "tool-x"
	withSpin := m.renderCard()
	for _, want := range []string{"refreshing ", "tool-x", " data ", frame} {
		if !strings.Contains(withSpin, want) {
			t.Errorf("refreshing card = %q, want it to contain %q", withSpin, want)
		}
	}
	if strings.Contains(withSpin, "a fine tool") {
		t.Errorf("refreshing card = %q, want the about hidden while refreshing", withSpin)
	}

	m.refreshingFor = ""
	noSpin := m.renderCard()
	if strings.Contains(noSpin, frame) {
		t.Errorf("idle card = %q, want no spinner frame %q", noSpin, frame)
	}
	if !strings.Contains(noSpin, "a fine tool") {
		t.Errorf("idle card = %q, want the about shown when not refreshing", noSpin)
	}
}

// TestRenderCardInstalledLatest covers the [info] version lines: installed:
// renders whenever the section is open (muted "detecting…" while the local
// probe is in flight, "✓ no version" / "✕ not installed" once it reported
// empty, split by presence), the section
// opens for a GitHub-less tool once an installed version is known, and
// latest: gains the update highlight + ↑ only when the installed version is
// older. The model goes through New + WindowSizeMsg so renderCard sees the
// same initialized state (spinner, widths) as the running app.
func TestRenderCardInstalledLatest(t *testing.T) {
	newCardModel := func(github string) Model {
		m := New([]loader.ToolMeta{{Name: "gh", GitHub: github}})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
		return updated.(Model)
	}

	t.Run("up to date: both lines, no arrow", func(t *testing.T) {
		m := newCardModel("cli/cli")
		m.versions["gh"] = VersionInfo{Installed: "v2.0.0", Latest: "v2.0.0", InstalledKnown: true}
		m.repoCards["gh"] = version.RepoCard{Latest: "v2.0.0"}
		card := stripANSI(m.renderCard())
		if !strings.Contains(card, "installed: \uf412 v2.0.0") {
			t.Errorf("card missing installed line; got:\n%s", card)
		}
		if !strings.Contains(card, "latest:  v2.0.0") {
			t.Errorf("card missing latest line; got:\n%s", card)
		}
		if strings.Contains(card, "↑") {
			t.Errorf("up-to-date card shows update arrow; got:\n%s", card)
		}
	})

	t.Run("update available: arrow and date suffix", func(t *testing.T) {
		m := newCardModel("cli/cli")
		m.versions["gh"] = VersionInfo{Installed: "v1.0.0", Latest: "v2.0.0", InstalledKnown: true}
		m.repoCards["gh"] = version.RepoCard{Latest: "v2.0.0", PublishedAt: "2026-01-02T15:04:05Z"}
		card := stripANSI(m.renderCard())
		if !strings.Contains(card, "latest:  v2.0.0 ↑ (2026-01-02)") {
			t.Errorf("card missing highlighted latest with arrow; got:\n%s", card)
		}
		if !strings.Contains(card, "installed: \uf412 v1.0.0") {
			t.Errorf("card missing installed line; got:\n%s", card)
		}
	})

	t.Run("detection reported empty and absent: not installed", func(t *testing.T) {
		m := newCardModel("cli/cli")
		m.versions["gh"] = VersionInfo{Latest: "v2.0.0", InstalledKnown: true}
		m.repoCards["gh"] = version.RepoCard{Latest: "v2.0.0"}
		card := stripANSI(m.renderCard())
		if !strings.Contains(card, "installed: ✕ not installed") {
			t.Errorf("card missing installed fallback with ✕ marker; got:\n%s", card)
		}
	})

	// A tool that is installed but won't name its version (a TUI app ignoring
	// --version) must not read as missing: same empty Installed, different line.
	t.Run("detection reported empty but present: no version", func(t *testing.T) {
		m := newCardModel("cli/cli")
		m.versions["gh"] = VersionInfo{Latest: "v2.0.0", InstalledKnown: true, InstalledPresent: true}
		m.repoCards["gh"] = version.RepoCard{Latest: "v2.0.0"}
		card := stripANSI(m.renderCard())
		if !strings.Contains(card, "installed: ✓ no version") {
			t.Errorf("card missing present-but-version-less line; got:\n%s", card)
		}
		if strings.Contains(card, "not installed") {
			t.Errorf("an installed tool must not read as missing; got:\n%s", card)
		}
	})

	t.Run("detection pending: detecting, not \"not found\"", func(t *testing.T) {
		m := newCardModel("cli/cli")
		m.versions["gh"] = VersionInfo{Latest: "v2.0.0"}
		m.repoCards["gh"] = version.RepoCard{Latest: "v2.0.0"}
		card := stripANSI(m.renderCard())
		if !strings.Contains(card, "installed: detecting…") {
			t.Errorf("card missing pending installed line; got:\n%s", card)
		}
		if strings.Contains(card, "not installed") || strings.Contains(card, "✕") {
			t.Errorf("card claims not installed before detection finished; got:\n%s", card)
		}
	})

	t.Run("no version data at all: detecting, no latest", func(t *testing.T) {
		m := newCardModel("cli/cli")
		card := stripANSI(m.renderCard())
		if !strings.Contains(card, "installed: detecting…") {
			t.Errorf("card missing pending installed line; got:\n%s", card)
		}
		if strings.Contains(card, "latest:") {
			t.Errorf("card shows latest with no card data; got:\n%s", card)
		}
	})

	// A repo card can exist with no release at all (repo info + stars fetched,
	// no tagged release): the latest: line is gated on card.Latest != "", so it
	// may not appear — and with it gone the U+F412 glyph may show up exactly
	// once, on the installed: line it now also marks.
	t.Run("card without a release: no latest line, one version glyph", func(t *testing.T) {
		m := newCardModel("cli/cli")
		m.versions["gh"] = VersionInfo{Installed: "v1.0.0", InstalledKnown: true}
		m.repoCards["gh"] = version.RepoCard{Stars: 42}
		card := stripANSI(m.renderCard())
		if !strings.Contains(card, "installed: \uf412 v1.0.0") {
			t.Errorf("card missing installed line; got:\n%s", card)
		}
		if strings.Contains(card, "latest:") {
			t.Errorf("card shows latest line with no release; got:\n%s", card)
		}
		if n := strings.Count(card, "\uf412"); n != 1 {
			t.Errorf("card shows the version glyph %d times, want 1 (installed only); got:\n%s", n, card)
		}
	})

	t.Run("no github with installed: info section opens", func(t *testing.T) {
		m := newCardModel("")
		m.versions["gh"] = VersionInfo{Installed: "v1.0.0", InstalledKnown: true}
		card := stripANSI(m.renderCard())
		if !strings.Contains(card, "[info]") || !strings.Contains(card, "installed: \uf412 v1.0.0") {
			t.Errorf("card missing [info]/installed for GitHub-less tool; got:\n%s", card)
		}
	})

	t.Run("no github no installed: no info section", func(t *testing.T) {
		m := newCardModel("")
		card := stripANSI(m.renderCard())
		if strings.Contains(card, "[info]") || strings.Contains(card, "installed:") {
			t.Errorf("card renders [info] with nothing to show; got:\n%s", card)
		}
	})
}

// updateLogModel builds a model with two tools and a live update log for the
// first (rg), for the Task-6 [3]-panel log rendering tests. Maps are non-nil so
// selectMeta/renderCard/autoFetch never nil-panic; viewports are sized so
// GotoBottom has something to scroll.
func updateLogModel() Model {
	m := Model{
		width: 120, height: 24, helpW: 60, briefW: 40, toolsW: 20,
		meta:          []loader.ToolMeta{{Name: "rg"}, {Name: "fzf"}},
		metaSelected:  0,
		focus:         focusHelp,
		helpMode:      helpModeHelp,
		helpCache:     map[string][2]string{"fzf": {helpModeHelp: "FZFHELP"}},
		versions:      map[string]VersionInfo{"rg": {InstalledKnown: true}, "fzf": {InstalledKnown: true}},
		repoCards:     map[string]version.RepoCard{},
		repoStatus:    map[string]string{},
		changelogData: map[string]changelogMsg{},
		updateLogFor:  "rg",
		updateLog:     []string{"upgrading rg", "done"},
	}
	m.tools = loader.ToolsFromMeta(m.meta)
	m.helpViewport = viewport.New(59, 17)
	m.briefViewport = viewport.New(39, 17)
	m.toolsViewport = viewport.New(19, 17)
	return m
}

// TestUpdateLogPanelTitle verifies the [3] panel title reads "[3] Update" while
// the updating tool is selected and reverts to "[3] Help" on another tool.
func TestUpdateLogPanelTitle(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "rg", Status: loader.StatusActive}, {Name: "fzf", Status: loader.StatusActive}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(Model)
	m.updateLogFor = "rg"
	m.updateLog = []string{"upgrading rg"}
	m.helpMode = helpModeHelp // the mode the log falls back to on another tool

	m.metaSelected = 0 // rg — the updating tool
	m.helpViewport.SetContent(m.renderHelpContent())
	top := stripANSI(strings.SplitN(m.renderHelp(), "\n", 2)[0])
	if !strings.Contains(top, " [3] Update ") {
		t.Errorf("updating-tool top border = %q, want [3] Update title", top)
	}

	m.metaSelected = 1 // fzf — not updating
	topOther := stripANSI(strings.SplitN(m.renderHelp(), "\n", 2)[0])
	if !strings.Contains(topOther, " [3] Help ") || strings.Contains(topOther, "Update") {
		t.Errorf("other-tool top border = %q, want [3] Help title", topOther)
	}
}

// TestUpdateLogRendersAheadOfLoading verifies the live-log branch wins over the
// helpLoadingFor "Loading..." state: re-selecting the updating tool mid-fetch
// still shows the log buffer, not a loading spinner.
func TestUpdateLogRendersAheadOfLoading(t *testing.T) {
	m := updateLogModel()
	m.helpLoadingFor = "rg" // even mid-fetch, the log branch takes precedence

	got := m.renderHelpContent()
	if strings.Contains(got, "Loading") {
		t.Errorf("renderHelpContent = %q, want the log, not Loading", got)
	}
	if !strings.Contains(got, "upgrading rg") || !strings.Contains(got, "done") {
		t.Errorf("renderHelpContent = %q, want the full log buffer", got)
	}
}

// TestUpdateLogSelectAwayAndBack verifies navigating away from the updating tool
// shows the other tool's normal help, and navigating back shows the live log
// again (the buffer survives selection moves).
func TestUpdateLogSelectAwayAndBack(t *testing.T) {
	m := updateLogModel()

	_ = m.selectMeta(0) // rg (log tool)
	if got := m.renderHelpContent(); !strings.Contains(got, "upgrading rg") {
		t.Errorf("on rg: renderHelpContent = %q, want update log", got)
	}
	_ = m.selectMeta(1) // fzf — normal help
	if got := m.renderHelpContent(); !strings.Contains(got, "FZFHELP") || strings.Contains(got, "upgrading rg") {
		t.Errorf("on fzf: renderHelpContent = %q, want fzf help, not the log", got)
	}
	_ = m.selectMeta(0) // back to rg — log again
	if got := m.renderHelpContent(); !strings.Contains(got, "upgrading rg") {
		t.Errorf("back on rg: renderHelpContent = %q, want update log again", got)
	}
}

// TestAutoFetchSkipsHelpForUpdateLog verifies autoFetchCmdsForSelected does not
// arm a help fetch (or set helpLoadingFor) for the tool whose live log is
// showing — otherwise a late helpOutputMsg would clobber the log in [3].
func TestAutoFetchSkipsHelpForUpdateLog(t *testing.T) {
	m := updateLogModel()
	m.helpCache = map[string][2]string{} // rg has no cached help: normally a fetch
	m.metaSelected = 0                   // rg — the updating tool

	_ = m.autoFetchCmdsForSelected()
	if m.helpLoadingFor != "" {
		t.Errorf("helpLoadingFor = %q, want empty (help fetch skipped for log tool)", m.helpLoadingFor)
	}
	if got := m.renderHelpContent(); strings.Contains(got, "Loading") {
		t.Errorf("renderHelpContent = %q, want the log, not Loading", got)
	}
}

// TestUpdateLogPersistsAfterDone verifies the log buffer survives an
// updateDoneMsg: the tool's log stays displayable until the next update starts.
func TestUpdateLogPersistsAfterDone(t *testing.T) {
	m := updateLogModel()
	m.updatingFor = "rg"

	nm := mustModel(m.Update(updateDoneMsg{tool: "rg", err: nil}))
	if nm.updateLogFor != "rg" || len(nm.updateLog) == 0 {
		t.Fatalf("after done: updateLogFor=%q log=%v, want log preserved", nm.updateLogFor, nm.updateLog)
	}
	if got := nm.renderHelpContent(); !strings.Contains(got, "upgrading rg") {
		t.Errorf("renderHelpContent after done = %q, want the persisted log", got)
	}
}

// TestApplySpotlight: with an active navigation cursor, lines outside the
// current entry are stripped of their own coloring and repainted dim, while
// the entry's lines keep the full colorizeHelp styling.
func TestApplySpotlight(t *testing.T) {
	forceColorProfile(t)
	const dimSeq = "38;2;136;136;136" // ui.ColorDim #888888 in truecolor

	m := helpNavModel()
	if len(m.helpEntries) != 2 {
		t.Fatalf("helpEntries = %v, want 2", m.helpEntries)
	}
	plain := m.renderHelpContent()
	if strings.Contains(plain, dimSeq) {
		t.Fatalf("cursor off: output already contains the dim sequence")
	}

	m.helpNavIdx = 0
	out := m.renderHelpContent()
	lines := strings.Split(out, "\n")
	e := m.helpEntries[0]
	for i, line := range lines {
		inside := i >= e.start && i < e.end
		hasDim := strings.Contains(line, dimSeq)
		if inside && hasDim {
			t.Errorf("line %d inside entry is dimmed: %q", i, line)
		}
		if !inside && !hasDim {
			t.Errorf("line %d outside entry is not dimmed: %q", i, line)
		}
	}
	// The spotlighted flag line keeps its HelpFlagStyle color (ColorPrimary).
	const flagSeq = "38;2;218;119;86" // ui.ColorPrimary #DA7756
	if !strings.Contains(lines[e.start], flagSeq) {
		t.Errorf("entry start line lost its flag styling: %q", lines[e.start])
	}
	// Outside lines carry no colorizeHelp styling anymore, only the dim.
	if strings.Contains(lines[0], flagSeq) {
		t.Errorf("dimmed header line still carries original styling: %q", lines[0])
	}
}

// TestApplySpotlightStaleIndex: an out-of-bounds cursor renders undimmed
// instead of panicking (a value-receiver render must not trust the index).
func TestApplySpotlightStaleIndex(t *testing.T) {
	forceColorProfile(t)
	m := helpNavModel()
	m.helpNavIdx = 99
	if got, want := m.renderHelpContent(), func() string { m.helpNavIdx = -1; return m.renderHelpContent() }(); got != want {
		t.Errorf("stale index output differs from cursor-off output")
	}
}

// TestSpotlightClearedOnFocusAway: moving focus off [3] with an active cursor
// repaints the viewport undimmed — stale dimming must not survive setFocus.
func TestSpotlightClearedOnFocusAway(t *testing.T) {
	forceColorProfile(t)
	const dimSeq = "38;2;136;136;136"

	m := helpNavModel()
	m.helpViewport = viewport.New(60, 10)
	m.helpNavIdx = 0
	m.helpViewport.SetContent(m.renderHelpContent())
	if !strings.Contains(m.helpViewport.View(), dimSeq) {
		t.Fatalf("precondition: viewport should show dimmed content")
	}

	updated, _ := m.Update(keyRunes("2"))
	nm := updated.(Model)
	if nm.focus != focusBrief {
		t.Fatalf("focus = %d, want focusBrief", nm.focus)
	}
	if strings.Contains(nm.helpViewport.View(), dimSeq) {
		t.Errorf("stale dim survived the focus move away from [3]")
	}
}

// helpNavScrollModel builds a model with six two-line entries (13 display
// lines) and a 4-line viewport, for exercising cursor movement + auto-scroll.
func helpNavScrollModel() Model {
	m := newTestModel(focusHelp)
	m.helpW = 62
	var b strings.Builder
	b.WriteString("OPTIONS:")
	for _, f := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"} {
		b.WriteString("\n  --" + f + "\n          about " + f)
	}
	m.helpCache["git"] = [2]string{helpModeHelp: b.String()}
	m.setHelpContent()
	m.helpViewport = viewport.New(60, 4)
	m.helpViewport.SetContent(m.renderHelpContent())
	return m
}

// TestHelpNavFirstPress: the first j lands on the first entry visible at the
// current scroll position, not the first entry of the document.
func TestHelpNavFirstPress(t *testing.T) {
	t.Run("from top", func(t *testing.T) {
		m := helpNavScrollModel()
		updated, _ := m.Update(keyRunes("j"))
		nm := updated.(Model)
		if nm.helpNavIdx != 0 {
			t.Errorf("helpNavIdx = %d, want 0 (first entry)", nm.helpNavIdx)
		}
	})
	t.Run("scrolled down", func(t *testing.T) {
		m := helpNavScrollModel()
		m.helpViewport.SetYOffset(6) // entries at {1,3} {3,5} {5,7}...: first with end > 6 is index 2
		updated, _ := m.Update(keyRunes("j"))
		nm := updated.(Model)
		if nm.helpNavIdx != 2 {
			t.Errorf("helpNavIdx = %d, want 2 (first visible entry)", nm.helpNavIdx)
		}
	})
	t.Run("scrolled past all entries", func(t *testing.T) {
		m := helpNavScrollModel()
		m.helpViewport.SetContent(m.renderHelpContent() + "\n" + strings.Repeat("trailing\n", 20))
		m.helpViewport.SetYOffset(15)
		updated, _ := m.Update(keyRunes("j"))
		nm := updated.(Model)
		if want := len(nm.helpEntries) - 1; nm.helpNavIdx != want {
			t.Errorf("helpNavIdx = %d, want %d (last entry)", nm.helpNavIdx, want)
		}
	})
}

// TestHelpNavEdges: no wrap-around — j at the last entry and k at the first
// are no-ops.
func TestHelpNavEdges(t *testing.T) {
	m := helpNavScrollModel()
	m.helpNavIdx = len(m.helpEntries) - 1
	updated, _ := m.Update(keyRunes("j"))
	nm := updated.(Model)
	if want := len(nm.helpEntries) - 1; nm.helpNavIdx != want {
		t.Errorf("j at last entry: helpNavIdx = %d, want %d", nm.helpNavIdx, want)
	}

	nm.helpNavIdx = 0
	nm.helpViewport.GotoTop()
	updated, _ = nm.Update(keyRunes("k"))
	nm = updated.(Model)
	if nm.helpNavIdx != 0 {
		t.Errorf("k at first entry: helpNavIdx = %d, want 0", nm.helpNavIdx)
	}
}

// TestHelpNavEscSemantics: the first esc only turns the cursor off (scroll
// kept, focus kept); the second walks focus to [2] as before.
func TestHelpNavEscSemantics(t *testing.T) {
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	m := helpNavScrollModel()
	m.helpNavIdx = 2
	m.helpViewport.SetYOffset(5)

	updated, _ := m.Update(esc)
	nm := updated.(Model)
	if nm.helpNavIdx != -1 {
		t.Fatalf("first esc: helpNavIdx = %d, want -1", nm.helpNavIdx)
	}
	if nm.focus != focusHelp {
		t.Fatalf("first esc: focus = %d, want focusHelp", nm.focus)
	}
	if nm.helpViewport.YOffset != 5 {
		t.Errorf("first esc: YOffset = %d, want 5 (scroll preserved)", nm.helpViewport.YOffset)
	}

	updated, _ = nm.Update(esc)
	nm = updated.(Model)
	if nm.focus != focusBrief {
		t.Errorf("second esc: focus = %d, want focusBrief", nm.focus)
	}
}

// TestHelpNavAutoScroll: moving the cursor keeps the entry in view; a
// taller-than-window entry pins its start to the top edge.
func TestHelpNavAutoScroll(t *testing.T) {
	t.Run("scrolls down to entry below window", func(t *testing.T) {
		m := helpNavScrollModel()
		m.helpNavIdx = 0
		updated, _ := m.Update(keyRunes("j")) // to entry {3,5}, window [0,4)
		nm := updated.(Model)
		if nm.helpViewport.YOffset != 1 {
			t.Errorf("YOffset = %d, want 1 (end-Height)", nm.helpViewport.YOffset)
		}
	})
	t.Run("scrolls up to entry above window", func(t *testing.T) {
		m := helpNavScrollModel()
		m.helpNavIdx = 3
		m.helpViewport.SetYOffset(8)
		updated, _ := m.Update(keyRunes("k")) // to entry {5,7}, window [8,12)
		nm := updated.(Model)
		if nm.helpViewport.YOffset != 5 {
			t.Errorf("YOffset = %d, want 5 (entry start)", nm.helpViewport.YOffset)
		}
	})
	t.Run("tall entry pins start to top", func(t *testing.T) {
		m := newTestModel(focusHelp)
		m.helpW = 62
		tall := "OPTIONS:\n  --big\n" + strings.TrimRight(strings.Repeat("          line\n", 6), "\n")
		m.helpCache["git"] = [2]string{helpModeHelp: tall}
		m.setHelpContent()
		m.helpViewport = viewport.New(60, 4)
		m.helpViewport.SetContent(m.renderHelpContent())
		if len(m.helpEntries) != 1 {
			t.Fatalf("helpEntries = %v, want 1 tall entry", m.helpEntries)
		}
		updated, _ := m.Update(keyRunes("j"))
		nm := updated.(Model)
		if want := nm.helpEntries[0].start; nm.helpViewport.YOffset != want {
			t.Errorf("YOffset = %d, want %d (entry start, not bottom-aligned)", nm.helpViewport.YOffset, want)
		}
	})
}

// TestHelpNavEmptyEntriesScrolls: with no entries (placeholder, update log)
// j/k keep their plain scroll behavior and never activate the cursor.
func TestHelpNavEmptyEntriesScrolls(t *testing.T) {
	m := newTestModel(focusHelp)
	m.helpW = 62
	m.helpViewport = viewport.New(60, 4)
	m.helpViewport.SetContent(strings.Repeat("x\n", 20))

	updated, _ := m.Update(keyRunes("j"))
	nm := updated.(Model)
	if nm.helpNavIdx != -1 {
		t.Errorf("helpNavIdx = %d, want -1 (no entries, no cursor)", nm.helpNavIdx)
	}
	if nm.helpViewport.YOffset != 3 {
		t.Errorf("YOffset = %d, want 3 (unified 3-line scroll)", nm.helpViewport.YOffset)
	}
}

// TestRenderStatusBarFocusHelp: the [3] bar advertises navigation when the
// entry index is non-empty, adds the exit hint while the cursor is active,
// and falls back to the scroll label when there is nothing to navigate.
func TestRenderStatusBarFocusHelp(t *testing.T) {
	stripBar := func(m Model) string {
		return stripANSI(m.renderStatusBar())
	}

	t.Run("no entries: scroll label", func(t *testing.T) {
		m := newTestModel(focusHelp)
		m.width = 120
		bar := stripBar(m)
		if !strings.Contains(bar, "scroll") || strings.Contains(bar, "navigate") {
			t.Errorf("bar = %q, want scroll hint and no navigate hint", bar)
		}
	})
	t.Run("entries present: navigate hint", func(t *testing.T) {
		m := helpNavModel()
		m.width = 120
		bar := stripBar(m)
		if !strings.Contains(bar, "[j/k] navigate") {
			t.Errorf("bar = %q, want j/k navigate hint", bar)
		}
		if strings.Contains(bar, "exit nav") {
			t.Errorf("bar = %q, exit-nav hint must be absent while the cursor is off", bar)
		}
	})
	t.Run("cursor active: exit-nav hint", func(t *testing.T) {
		m := helpNavModel()
		m.width = 120
		m.helpNavIdx = 0
		bar := stripBar(m)
		if !strings.Contains(bar, "[esc] exit nav") {
			t.Errorf("bar = %q, want esc exit-nav hint", bar)
		}
	})
}

// TestParseHelpEntriesReviewRegressions pins the parser fixes from the code
// review: flag-prefixed continuation lines, justified man prose, and
// multi-paragraph descriptions.
func TestParseHelpEntriesReviewRegressions(t *testing.T) {
	const wide = 200

	t.Run("flag token in continuation stays inside the entry", func(t *testing.T) {
		raw := strings.Join([]string{
			"OPTIONS:",
			"  --ignore",
			"          Respect ignore files.",
			"          This flag can be overridden with",
			"          --no-ignore.",
		}, "\n")
		got := parseHelpEntries(raw, wide)
		want := []entryRange{{start: 1, end: 5}}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("parseHelpEntries = %v, want %v (deeper --no-ignore. is description, not a new entry)", got, want)
		}
	})

	t.Run("justified man prose is not a subcommand row", func(t *testing.T) {
		raw := strings.Join([]string{
			"DESCRIPTION",
			"     tree.  See also git-log(1) for details.",
			"     branch.  Another justified sentence gap.",
		}, "\n")
		if got := parseHelpEntries(raw, wide); got != nil {
			t.Errorf("parseHelpEntries = %v, want nil (sentence-period + double space is prose)", got)
		}
	})

	t.Run("blank line inside a multi-paragraph description", func(t *testing.T) {
		raw := strings.Join([]string{
			"  -p, --paginate",
			"          First paragraph of the description.",
			"",
			"          Second paragraph, same option.",
			"  -q, --quiet",
		}, "\n")
		got := parseHelpEntries(raw, wide)
		want := []entryRange{{start: 0, end: 4}, {start: 4, end: 5}}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("parseHelpEntries = %v, want %v (blank line keeps the paragraphs together)", got, want)
		}
	})

	t.Run("blank line before the next entry still terminates", func(t *testing.T) {
		raw := strings.Join([]string{
			"  completion  Generate the autocompletion script",
			"",
			"  help        Help about any command",
		}, "\n")
		got := parseHelpEntries(raw, wide)
		if len(got) != 2 || got[0] != (entryRange{start: 0, end: 1}) || got[1] != (entryRange{start: 2, end: 3}) {
			t.Errorf("parseHelpEntries = %v, want two single-line entries", got)
		}
	})
}

// TestHelpNavArrowsKeepScrolling: with entries present, only j/k drive the
// cursor — the arrows keep their 3-line scroll so prose stays reachable.
func TestHelpNavArrowsKeepScrolling(t *testing.T) {
	m := helpNavScrollModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	nm := updated.(Model)
	if nm.helpNavIdx != -1 {
		t.Errorf("helpNavIdx = %d, want -1 (down arrow must not activate the cursor)", nm.helpNavIdx)
	}
	if nm.helpViewport.YOffset != 3 {
		t.Errorf("YOffset = %d, want 3 (arrow line scroll)", nm.helpViewport.YOffset)
	}

	nm.helpNavIdx = 1
	updated, _ = nm.Update(tea.KeyMsg{Type: tea.KeyUp})
	nm = updated.(Model)
	if nm.helpNavIdx != 1 {
		t.Errorf("helpNavIdx = %d, want 1 (up arrow must not move the cursor)", nm.helpNavIdx)
	}
	if nm.helpViewport.YOffset != 0 {
		t.Errorf("YOffset = %d, want 0 (scrolled back up)", nm.helpViewport.YOffset)
	}
}

// TestHelpNavStartDirection: activation with no entry on screen goes to the
// nearest entry in the movement direction, not blindly to the first
// below-window entry.
func TestHelpNavStartDirection(t *testing.T) {
	// 12 lines of prose, then two entries, then more prose.
	raw := "INTRO:\n" + strings.TrimRight(strings.Repeat("intro prose line\n", 11), "\n") +
		"\n  --alpha\n          about alpha\n  --beta\n          about beta"
	m := newTestModel(focusHelp)
	m.helpW = 62
	m.helpCache["git"] = [2]string{helpModeHelp: raw}
	m.setHelpContent()
	m.helpViewport = viewport.New(60, 4)
	m.helpViewport.SetContent(m.renderHelpContent())
	if len(m.helpEntries) != 2 {
		t.Fatalf("helpEntries = %v, want 2", m.helpEntries)
	}

	t.Run("j goes to next entry below the window", func(t *testing.T) {
		vm := m
		vm.helpViewport.SetYOffset(0) // window [0,4): prose only
		updated, _ := vm.Update(keyRunes("j"))
		nm := updated.(Model)
		if nm.helpNavIdx != 0 {
			t.Errorf("helpNavIdx = %d, want 0 (next entry below)", nm.helpNavIdx)
		}
	})
	t.Run("k goes to previous entry above the window", func(t *testing.T) {
		vm := m
		vm.helpViewport.SetContent(vm.renderHelpContent() + "\n" + strings.Repeat("tail\n", 20))
		vm.helpViewport.SetYOffset(18) // window past both entries
		updated, _ := vm.Update(keyRunes("k"))
		nm := updated.(Model)
		if want := len(nm.helpEntries) - 1; nm.helpNavIdx != want {
			t.Errorf("helpNavIdx = %d, want %d (previous entry above)", nm.helpNavIdx, want)
		}
	})
}

// TestHelpOutputMsgHiddenModeKeepsCursor: a late fetch result for the mode
// that is not on screen must not reset an active spotlight cursor.
func TestHelpOutputMsgHiddenModeKeepsCursor(t *testing.T) {
	m := helpNavModel() // displaying helpModeHelp
	m.helpNavIdx = 1

	updated, _ := m.Update(helpOutputMsg{toolName: "git", mode: helpModeMan, output: "MAN(1) page text"})
	nm := updated.(Model)
	if nm.helpNavIdx != 1 {
		t.Errorf("helpNavIdx = %d, want 1 (hidden-mode arrival must not reset)", nm.helpNavIdx)
	}
	if nm.helpCache["git"][helpModeMan] == "" {
		t.Errorf("man output was not cached")
	}

	// Same tool, displayed mode: text on screen changed — recompute + reset.
	updated, _ = nm.Update(helpOutputMsg{toolName: "git", mode: helpModeHelp, output: navHelpFixture})
	nm = updated.(Model)
	if nm.helpNavIdx != -1 {
		t.Errorf("helpNavIdx = %d, want -1 after displayed-mode arrival", nm.helpNavIdx)
	}
}

// TestResizeHeightOnlyKeepsCursor: a resize that does not change the wrap
// width leaves the entry ranges valid, so the cursor survives; a width
// change recomputes and resets.
func TestResizeHeightOnlyKeepsCursor(t *testing.T) {
	m := newTestModel(focusHelp)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	m.helpCache["git"] = [2]string{helpModeHelp: navHelpFixture}
	m.setHelpContent()
	if len(m.helpEntries) != 2 {
		t.Fatalf("helpEntries = %v, want 2", m.helpEntries)
	}
	m.helpNavIdx = 1

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	nm := updated.(Model)
	if nm.helpNavIdx != 1 {
		t.Errorf("helpNavIdx = %d, want 1 (height-only resize keeps the cursor)", nm.helpNavIdx)
	}

	updated, _ = nm.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	nm = updated.(Model)
	if nm.helpNavIdx != -1 {
		t.Errorf("helpNavIdx = %d, want -1 (width change recomputes and resets)", nm.helpNavIdx)
	}
}

// TestHelpBaseCache: setHelpContent caches the colorized base and the normal
// render path serves it; the spotlight is applied over the cache.
func TestHelpBaseCache(t *testing.T) {
	forceColorProfile(t)
	m := helpNavModel()
	if m.helpBase == "" {
		t.Fatalf("helpBase empty after setHelpContent")
	}
	if got := m.renderHelpContent(); got != m.helpBase {
		t.Errorf("cursor-off render differs from cached base")
	}
	m.helpNavIdx = 0
	if got := m.renderHelpContent(); got == m.helpBase || !strings.Contains(got, "38;2;136;136;136") {
		t.Errorf("spotlight render did not dim over the cached base")
	}
}

// hotkeysViewModel builds a ready 80x24 model in modeHotkeys for View-level
// overlay assertions. The self state governs the one state-dependent group, so
// it is a parameter: selfNone lists no Self group at all (both keys are unbound
// there), and the two live states word it differently.
func hotkeysViewModel(state selfState) Model {
	m := New([]loader.ToolMeta{{Name: "git"}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.mode = modeHotkeys
	m.selfState = state
	return m
}

// TestRenderHotkeysOverlayContent: View in modeHotkeys shows every group header
// and a per-group spot-check key/description.
func TestRenderHotkeysOverlayContent(t *testing.T) {
	m := hotkeysViewModel(selfOffered)
	view := m.View()
	for _, want := range []string{
		"Keyboard shortcuts",
		"Global", "[1] Tools", "[2] Brief", "Self", "[3] Help / Man / Readme", "Scrolling",
		"focus panel", // Global
		"select tool", // [1] Tools
		"open repo",   // [2] Brief
		"self-update", // Self
		"entry nav",   // [3] Help / Man / Readme
		"readme",      // [3] the third panel source
		"3 lines",     // Scrolling
		"run in tab",  // [1] Tools enter row
		"close",       // title close hint
	} {
		if !strings.Contains(view, want) {
			t.Errorf("hotkeys View missing %q", want)
		}
	}
}

// TestRenderHotkeysSelfGroupFollowsState: the Self group documents the bindings
// that are actually live. With no banner (every dev build) U and X do nothing and
// the group is absent; after a successful update they mean restart/later, not
// update/dismiss — one fixed wording would document half the state machine.
func TestRenderHotkeysSelfGroupFollowsState(t *testing.T) {
	tests := []struct {
		state  selfState
		want   []string
		absent []string
	}{
		{state: selfNone, absent: []string{"Self", "self-update", "restart"}},
		{state: selfOffered, want: []string{"Self", "self-update", "dismiss"}, absent: []string{"restart"}},
		{state: selfDismissed, want: []string{"Self", "self-update", "dismiss"}},
		{state: selfUpdated, want: []string{"Self", "restart", "later"}, absent: []string{"self-update"}},
		{state: selfUpdatedLater, want: []string{"Self", "restart", "later"}},
	}
	for _, tt := range tests {
		view := hotkeysViewModel(tt.state).renderHotkeys()
		for _, want := range tt.want {
			if !strings.Contains(view, want) {
				t.Errorf("state %v: overlay missing %q", tt.state, want)
			}
		}
		for _, absent := range tt.absent {
			if strings.Contains(view, absent) {
				t.Errorf("state %v: overlay should not contain %q", tt.state, absent)
			}
		}
	}
}

// TestRenderHotkeysDimsBackground: the overlay dims the composited background,
// mirroring the [L] overlay.
func TestRenderHotkeysDimsBackground(t *testing.T) {
	forceColorProfile(t)
	const dimSeq = "38;2;136;136;136" // ui.ColorDim #888888
	m := hotkeysViewModel(selfNone)
	if !strings.Contains(m.View(), dimSeq) {
		t.Errorf("hotkeys overlay did not dim the background")
	}
}

// TestRenderHotkeysSizeBudget: at 80x24 the overlay fits the 20-row background
// with no PlaceOverlay clipping — both the top title's close hint and the
// bottom Scrolling row survive.
func TestRenderHotkeysSizeBudget(t *testing.T) {
	// selfOffered is the widest variant of the one state-dependent group
	// ("self-update", 11 cells against the column's 12-cell "cycle status").
	m := hotkeysViewModel(selfOffered)
	view := m.View()
	// Title row (top of the overlay).
	if !strings.Contains(view, "close") {
		t.Errorf("size budget: title close hint clipped off the top")
	}
	// Bottom-most, right-most row: [g/G] top / bottom (last Scrolling row in the
	// last column) — its trailing word survives only if nothing was clipped off
	// the bottom or the right edge.
	if !strings.Contains(view, "top / bottom") {
		t.Errorf("size budget: last Scrolling row clipped (bottom/right overflow)")
	}
	// The readme row lives in the same (tallest) column, above Scrolling.
	if !strings.Contains(view, "readme") {
		t.Errorf("size budget: [3] readme row clipped")
	}
	// Last row of the Self group — the newest addition, in the shortest column.
	if !strings.Contains(view, "dismiss") {
		t.Errorf("size budget: Self group's last row clipped")
	}
	// The framed overlay must be <= the 20-row background height — in every self
	// state, since the Self group's wording (and presence) varies with it.
	for _, state := range []selfState{selfNone, selfOffered, selfDismissed, selfUpdated, selfUpdatedLater} {
		overlay := hotkeysViewModel(state).renderHotkeys()
		if h := lipgloss.Height(overlay); h > 20 {
			t.Errorf("state %v: overlay framed height = %d, want <= 20 (80x24 background)", state, h)
		}
		if w := lipgloss.Width(overlay); w > 76 {
			t.Errorf("state %v: overlay framed width = %d, want <= 76", state, w)
		}
	}
}

// TestHotkeysHintInFocusBars: the [?] keys hint appears in all three normal
// focus status bars.
func TestHotkeysHintInFocusBars(t *testing.T) {
	for _, focus := range []int{focusTools, focusBrief, focusHelp} {
		m := New([]loader.ToolMeta{{Name: "git"}})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
		m = updated.(Model)
		m.focus = focus
		bar := m.renderStatusBar()
		if !strings.Contains(bar, "[?]") || !strings.Contains(bar, "keys") {
			t.Errorf("focus %d bar missing [?] keys hint: %q", focus, bar)
		}
	}
}

// TestReadmeHintInHelpBar: the focusHelp bar advertises [r] readme next to the
// [h]/[m] source switches, in every help mode (the key is always live there).
func TestReadmeHintInHelpBar(t *testing.T) {
	for _, mode := range []int{helpModeHelp, helpModeMan, helpModeReadme} {
		m := New([]loader.ToolMeta{{Name: "git"}})
		m = mustModel(m.Update(tea.WindowSizeMsg{Width: 120, Height: 24}))
		m.focus = focusHelp
		m.helpMode = mode
		bar := m.renderStatusBar()
		if !strings.Contains(bar, "[r]") || !strings.Contains(bar, "readme") {
			t.Errorf("mode %d focusHelp bar missing [r] readme hint: %q", mode, bar)
		}
		for _, want := range []string{"--help", "man"} {
			if !strings.Contains(bar, want) {
				t.Errorf("mode %d focusHelp bar lost %q hint: %q", mode, want, bar)
			}
		}
		// [/] is a no-op in readme mode (the help search would tear glamour's
		// ANSI apart), so the bar must not advertise it there.
		if hasSearch := strings.Contains(bar, "search"); hasSearch != (mode != helpModeReadme) {
			t.Errorf("mode %d focusHelp bar search hint = %v, want %v: %q", mode, hasSearch, mode != helpModeReadme, bar)
		}
	}
	// The other focus bars keep their own r binding (rename / refresh).
	m := New([]loader.ToolMeta{{Name: "git"}})
	m = mustModel(m.Update(tea.WindowSizeMsg{Width: 120, Height: 24}))
	m.focus = focusTools
	if bar := m.renderStatusBar(); strings.Contains(bar, "readme") {
		t.Errorf("focusTools bar leaked readme hint: %q", bar)
	}
}

// readmePanelModel returns a sized model in README mode with the given tools,
// the first one selected and panel [3] focused.
func readmePanelModel(t *testing.T, metas ...loader.ToolMeta) Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	m := New(metas)
	m = mustModel(m.Update(tea.WindowSizeMsg{Width: 100, Height: 30}))
	m.helpMode = helpModeReadme
	m.focus = focusHelp
	return m
}

// TestReadmePlaceholders pins the tool-named placeholder for every state the
// README panel can be in without content.
func TestReadmePlaceholders(t *testing.T) {
	repo := loader.ToolMeta{Name: "rg", GitHub: "BurntSushi/ripgrep"}

	t.Run("no repo", func(t *testing.T) {
		m := readmePanelModel(t, loader.ToolMeta{Name: "local"})
		if got := stripANSI(m.renderHelpContent()); !strings.Contains(got, "No repo for local.") {
			t.Errorf("panel = %q, want the no-repo placeholder", got)
		}
	})
	t.Run("loading", func(t *testing.T) {
		m := readmePanelModel(t, repo)
		if got := stripANSI(m.renderHelpContent()); !strings.Contains(got, "Loading...") {
			t.Errorf("panel = %q, want the loading placeholder while the fetch is in flight", got)
		}
	})
	t.Run("no readme in repo", func(t *testing.T) {
		m := readmePanelModel(t, repo)
		m = mustModel(m.Update(readmeMsg{toolName: "rg", err: version.ErrNoReadme}))
		got := stripANSI(m.renderHelpContent())
		if !strings.Contains(got, "No README in BurntSushi/ripgrep.") || !strings.Contains(got, "[h]") {
			t.Errorf("panel = %q, want the 404 placeholder naming the repo", got)
		}
	})
	t.Run("rate limited", func(t *testing.T) {
		m := readmePanelModel(t, repo)
		m = mustModel(m.Update(readmeMsg{toolName: "rg", err: version.ErrRateLimited}))
		if got := stripANSI(m.renderHelpContent()); !strings.Contains(got, "rate limited — press [L]") {
			t.Errorf("panel = %q, want the rate-limit placeholder", got)
		}
	})
	t.Run("content that renders to nothing", func(t *testing.T) {
		// glamour renders a README carrying no visible markdown to an empty
		// string; returning that as real content would paint a blank panel with
		// no way out, so it must fall through to a placeholder.
		m := readmePanelModel(t, repo)
		m = mustModel(m.Update(readmeMsg{toolName: "rg", content: "<!-- badges only -->\n"}))
		if m.helpBase != "" {
			t.Skipf("glamour rendered %q to non-empty output; the empty-render path needs another fixture", "<!-- -->")
		}
		got := stripANSI(m.renderHelpContent())
		if strings.TrimSpace(got) == "" {
			t.Fatal("panel is blank, want a placeholder")
		}
		if !strings.Contains(got, "No README for rg.") || !strings.Contains(got, "[h]") {
			t.Errorf("panel = %q, want the generic placeholder with a way out", got)
		}
	})
	t.Run("generic failure", func(t *testing.T) {
		m := readmePanelModel(t, repo)
		m = mustModel(m.Update(readmeMsg{toolName: "rg", err: errors.New("dial tcp: no route to host")}))
		got := stripANSI(m.renderHelpContent())
		if !strings.Contains(got, "No README for rg.") || !strings.Contains(got, "[h]") {
			t.Errorf("panel = %q, want the generic placeholder", got)
		}
	})
	t.Run("cached content wins over a later failure", func(t *testing.T) {
		m := readmePanelModel(t, repo)
		m = mustModel(m.Update(readmeMsg{toolName: "rg", content: "# ripgrep\n\nfast search"}))
		m = mustModel(m.Update(readmeMsg{toolName: "rg", err: version.ErrRateLimited}))
		got := stripANSI(m.renderHelpContent())
		if !strings.Contains(got, "fast search") {
			t.Errorf("panel = %q, want the known README to survive the failure", got)
		}
	})
}

// TestReadmeModeHasNoEntryNav: glamour output is already ANSI, so
// parseHelpEntries/colorizeHelp never run in readme mode. The entry index stays
// empty, which is what keeps j/k a plain 3-line scroll instead of a spotlight
// cursor over lines that only look like flags.
func TestReadmeModeHasNoEntryNav(t *testing.T) {
	body := "# rg\n\n" + strings.Repeat("    --hidden     search hidden files\n    --glob PAT   filter paths\n", 40)
	m := readmePanelModel(t, loader.ToolMeta{Name: "rg", GitHub: "BurntSushi/ripgrep"})
	m = mustModel(m.Update(readmeMsg{toolName: "rg", content: body}))

	if m.helpBase == "" {
		t.Fatal("helpBase empty, want the rendered README")
	}
	if len(m.helpEntries) != 0 {
		t.Fatalf("helpEntries = %d, want none in readme mode", len(m.helpEntries))
	}

	nm := mustModel(m.Update(keyRunes("j")))
	if nm.helpNavIdx != -1 {
		t.Errorf("helpNavIdx = %d, want -1 (j must not start entry nav in readme mode)", nm.helpNavIdx)
	}
	if nm.helpViewport.YOffset == 0 {
		t.Error("j did not scroll the readme viewport")
	}
}

// TestTokenAddClearsRateLimitedReadmes: the rate-limit placeholder tells the
// user to press [L]; needsReadme treats any stored entry as answered, so the
// negative has to be dropped when a token lands or the panel would stay stuck
// on "rate limited" for the whole session.
func TestTokenAddClearsRateLimitedReadmes(t *testing.T) {
	m := readmePanelModel(t,
		loader.ToolMeta{Name: "rg", GitHub: "BurntSushi/ripgrep"},
		loader.ToolMeta{Name: "fd", GitHub: "sharkdp/fd"},
		loader.ToolMeta{Name: "bat", GitHub: "sharkdp/bat"},
	)
	m = mustModel(m.Update(readmeMsg{toolName: "rg", err: version.ErrRateLimited}))
	m = mustModel(m.Update(readmeMsg{toolName: "fd", err: version.ErrNoReadme}))
	m = mustModel(m.Update(readmeMsg{toolName: "bat", content: "# bat"}))

	// The handler persists the token in the version package's process-wide
	// state; drop it again so the order of the other tests cannot depend on it.
	t.Cleanup(func() { _ = version.ClearToken() })
	m = mustModel(m.Update(tokenValidatedMsg{token: "ghp_test"}))

	if _, still := m.readmeData["rg"]; still {
		t.Error("rate-limited README survived the token add; the [L] hint would be a dead end")
	}
	if _, gone := m.readmeData["fd"]; !gone {
		t.Error("a 404 is conclusive and must not be retried after a token add")
	}
	if got := m.readmeData["bat"].content; got != "# bat" {
		t.Errorf("bat content = %q, want the fetched README untouched", got)
	}
}

// TestReadmeResizeRerenders: a width change re-wraps the README through
// glamour (the render cache is keyed by width); a height-only resize keeps the
// scroll position.
func TestReadmeResizeRerenders(t *testing.T) {
	// Many paragraphs: the rendered README must stay taller than the viewport,
	// or the height-only resize would clamp the scroll offset legitimately.
	body := "# ripgrep\n\n" + strings.Repeat(
		"ripgrep recursively searches directories for a regex pattern. It respects gitignore rules by default.\n\n", 60)
	m := readmePanelModel(t, loader.ToolMeta{Name: "rg", GitHub: "BurntSushi/ripgrep"})
	m = mustModel(m.Update(readmeMsg{toolName: "rg", content: body}))
	narrow := maxLineWidth(stripANSI(m.helpBase))
	if narrow == 0 {
		t.Fatal("helpBase empty, want the rendered README")
	}

	m = mustModel(m.Update(tea.WindowSizeMsg{Width: 160, Height: 30}))
	wide := maxLineWidth(stripANSI(m.helpBase))
	if wide <= narrow {
		t.Errorf("widest line = %d at width 160, want more than %d at width 100", wide, narrow)
	}

	m.helpViewport.SetYOffset(2)
	m = mustModel(m.Update(tea.WindowSizeMsg{Width: 160, Height: 40}))
	if m.helpViewport.YOffset != 2 {
		t.Errorf("YOffset = %d, want 2 (a height-only resize keeps the scroll position)", m.helpViewport.YOffset)
	}
	if maxLineWidth(stripANSI(m.helpBase)) != wide {
		t.Error("a height-only resize must not re-wrap the README")
	}
}

// maxLineWidth returns the width of the widest line of s.
func maxLineWidth(s string) int {
	w := 0
	for _, line := range strings.Split(s, "\n") {
		w = max(w, lipgloss.Width(strings.TrimRight(line, " ")))
	}
	return w
}

// TestStatusBarNeverWraps pins the layout budget: the hints bar is one line at
// the 80×24 baseline in every normal focus and panel-[3] mode, so View() never
// returns more rows than the terminal has (an over-tall View scrolls the top
// border off the alt screen). renderHintsBar drops trailing hint cells to get
// there; only the presence of a wrap is a bug.
// The self-update cases additionally seed a known rate snapshot: without one the
// gauge is not drawn at all and the right group's geometry — where the banner's
// compact cell competes with it for the corner — would go unchecked.
func TestStatusBarNeverWraps(t *testing.T) {
	cases := []struct {
		name      string
		focus     int
		helpMode  int
		nav       bool
		self      selfState
		selfTag   string
		updating  bool
		knownRate bool
		width     int
	}{
		{name: "tools", focus: focusTools, helpMode: helpModeHelp},
		{name: "brief", focus: focusBrief, helpMode: helpModeHelp},
		{name: "help mode", focus: focusHelp, helpMode: helpModeHelp},
		{name: "man mode", focus: focusHelp, helpMode: helpModeMan},
		{name: "readme mode", focus: focusHelp, helpMode: helpModeReadme},
		{name: "help mode with entry nav", focus: focusHelp, helpMode: helpModeHelp, nav: true},
		{name: "self offered banner", focus: focusTools, helpMode: helpModeHelp, self: selfOffered, knownRate: true},
		{name: "self offered banner in brief", focus: focusBrief, helpMode: helpModeHelp, self: selfOffered, knownRate: true},
		{name: "self dismissed cell", focus: focusTools, helpMode: helpModeHelp, self: selfDismissed, knownRate: true},
		{name: "self dismissed cell in help", focus: focusHelp, helpMode: helpModeReadme, self: selfDismissed, knownRate: true},
		{name: "self updated banner", focus: focusTools, helpMode: helpModeHelp, self: selfUpdated, knownRate: true},
		{name: "self restart later cell", focus: focusBrief, helpMode: helpModeHelp, self: selfUpdatedLater, knownRate: true},
		{name: "self updating cell", focus: focusTools, helpMode: helpModeHelp, self: selfOffered, updating: true, knownRate: true},
		// Narrow terminals: the fused banner cell is undroppable (a dropped [U]
		// would leave an announcement with no way to act on it), so below its own
		// width it must be cut rather than allowed to wrap. 40 is around the
		// threshold for the default tag, 36 below it, and a long upstream tag
		// pushes the same cell over the edge at a comfortable width.
		{name: "self banner at 40 cols", focus: focusTools, helpMode: helpModeHelp, self: selfOffered, width: 40},
		{name: "self banner at 36 cols", focus: focusTools, helpMode: helpModeHelp, self: selfOffered, width: 36},
		{name: "self banner at 24 cols", focus: focusBrief, helpMode: helpModeHelp, self: selfUpdated, width: 24},
		{
			name: "long tag in the banner", focus: focusTools, helpMode: helpModeHelp,
			self: selfOffered, selfTag: "v2026.07.25-nightly.1+build.42", width: 44, knownRate: true,
		},
		// The collapsed cell has to fit the 80x24 baseline: after [X] it is the
		// feature's only visible surface for the rest of the session, so it drops
		// hint cells rather than itself.
		{name: "self dismissed cell at 80 cols", focus: focusTools, helpMode: helpModeHelp, self: selfDismissed, knownRate: true, width: 80},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			width := tc.width
			if width == 0 {
				width = 80
			}
			// WithAppVersion is what main injects and what selfCheckEnabled gates
			// the whole feature on. Without it selfUpdating() is false whatever
			// updatingFor says, so the updating case below would silently render the
			// offered banner instead of the compact "keeptui updating…" cell.
			m := New([]loader.ToolMeta{{Name: "rg", GitHub: "BurntSushi/ripgrep"}}).WithAppVersion("v0.4.2")
			m = mustModel(m.Update(tea.WindowSizeMsg{Width: width, Height: 24}))
			m.focus = tc.focus
			m.helpMode = tc.helpMode
			if tc.nav {
				m.helpEntries = []entryRange{{start: 0, end: 1}}
				m.helpNavIdx = 0
			}
			m.selfState = tc.self
			m.selfLatest = "v0.5.0"
			if tc.selfTag != "" {
				m.selfLatest = tc.selfTag
			}
			if tc.updating {
				m.updatingFor = selfToolName
				m.selfUpdateLog = true
				if !m.selfUpdating() {
					t.Fatal("fixture: the model must be mid-self-update, " +
						"else this case renders the offered banner and duplicates the case above")
				}
			}
			if tc.knownRate {
				m.rate = version.RateLimit{Known: true, Limit: 60, Remaining: 42}
			}

			if got := lipgloss.Height(m.renderStatusBar()); got != 3 {
				t.Errorf("status bar height = %d, want 3 (border + one hint line)", got)
			}
			// Only at the baseline width and above: the three panels have their
			// own minimum widths (15+30+30 plus borders), so below ~80 columns the
			// layout overflows on its own — with or without a banner — and that is
			// a separate, pre-existing limit, not the bar wrapping.
			if width < 80 {
				return
			}
			if got := lipgloss.Height(m.View()); got > m.height {
				t.Errorf("View() height = %d, want at most the terminal height %d", got, m.height)
			}
		})
	}
}

// TestSearchTypingRepaintsReadmeForSelection: typing in the tool-list search
// moves the selection without going through selectMeta, so the repaint has to
// run setHelpContent — renderHelpContent's readme branch serves helpBase, which
// nothing else re-renders. Otherwise [3] keeps showing the previously selected
// tool's README under the newly selected tool's name.
func TestSearchTypingRepaintsReadmeForSelection(t *testing.T) {
	m := readmePanelModel(t,
		loader.ToolMeta{Name: "rg", GitHub: "BurntSushi/ripgrep"},
		loader.ToolMeta{Name: "fd", GitHub: "sharkdp/fd"},
	)
	m = mustModel(m.Update(readmeMsg{toolName: "rg", content: "# rg\n\nrecursive grep"}))
	m = mustModel(m.Update(readmeMsg{toolName: "fd", content: "# fd\n\nfind entries"}))
	m.focus = focusTools
	m.setHelpContent()
	if !strings.Contains(stripANSI(m.helpBase), "recursive grep") {
		t.Fatalf("helpBase = %q, want rg's README before the search", stripANSI(m.helpBase))
	}

	m = mustModel(m.Update(keyRunes("/")))
	m = mustModel(m.Update(keyRunes("f")))

	if mt, ok := m.selectedMeta(); !ok || mt.Name != "fd" {
		t.Fatalf("selected = %v, want fd", mt)
	}
	panel := stripANSI(m.renderHelpContent())
	if !strings.Contains(panel, "find entries") {
		t.Errorf("panel [3] = %q, want fd's README", panel)
	}
	if strings.Contains(panel, "recursive grep") {
		t.Error("panel [3] still shows the previously selected tool's README")
	}
}

// TestReadmeFetchGuardedWhileInFlight: the session entry only lands when the
// response arrives, so needsReadme must also honour the in-flight marker —
// otherwise a j/k bounce back onto the same tool spends a second GitHub request
// against a 60/hour unauthenticated budget.
func TestReadmeFetchGuardedWhileInFlight(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New([]loader.ToolMeta{
		{Name: "rg", GitHub: "BurntSushi/ripgrep"},
		{Name: "fd", GitHub: "sharkdp/fd"},
	})
	m = mustModel(m.Update(tea.WindowSizeMsg{Width: 100, Height: 30}))
	// Pre-fill the other sources so only the README decision varies.
	for _, name := range []string{"rg", "fd"} {
		m.changelogData[name] = changelogMsg{toolName: name}
		m.versions[name] = VersionInfo{Installed: "1.0.0", InstalledKnown: true, Latest: "1.0.0"}
		m.repoCards[name] = version.RepoCard{About: "tool"}
	}

	if n := countBatchedCmds(m.autoFetchCmdsForSelected()); n != 1 {
		t.Fatalf("first visit batched %d cmds, want 1 (the README)", n)
	}
	if n := countBatchedCmds(m.autoFetchCmdsForSelected()); n != 0 {
		t.Errorf("re-visit while the fetch is in flight batched %d cmds, want 0", n)
	}

	// The response retires the marker; the stored entry then answers for the
	// rest of the session.
	m = mustModel(m.Update(readmeMsg{toolName: "rg", content: "# rg"}))
	if m.readmeLoading["rg"] {
		t.Error("readmeLoading[rg] survived the response")
	}
	if n := countBatchedCmds(m.autoFetchCmdsForSelected()); n != 0 {
		t.Errorf("after the response the auto-fetch batched %d cmds, want 0", n)
	}

	// A force refresh drops the entry and marks its own request, so a selection
	// bounce during that window does not fire a second one either.
	_ = m.refreshSelectedCmd(loader.Tool{Name: "rg", GitHub: "BurntSushi/ripgrep"})
	if !m.readmeLoading["rg"] {
		t.Fatal("the forced README refresh is not marked in flight")
	}
	if n := countBatchedCmds(m.autoFetchCmdsForSelected()); n != 0 {
		t.Errorf("re-visit during a forced refresh batched %d cmds, want 0", n)
	}
}

// TestRenderStatusBarRunInput: the modeRunInput bar echoes the selected tool's
// name, the live command input, and the run/cancel hints — mirroring the other
// input-mode branches.
func TestRenderStatusBarRunInput(t *testing.T) {
	m := New([]loader.ToolMeta{{Name: "yazi"}})
	m.width = 80
	m.mode = modeRunInput
	m.runInput.SetValue("yazi /tmp")

	got := m.renderStatusBar()
	for _, want := range []string{"run yazi:", "yazi /tmp", "[enter]", "run", "[esc]", "cancel"} {
		if !strings.Contains(got, want) {
			t.Errorf("run input status bar = %q, missing %q", got, want)
		}
	}
}

// TestRenderStatusBarFocusToolsRunHint: the focusTools normal-mode bar carries
// the [enter] run hint (first cell — it must survive narrow-terminal cell
// dropping, which trims from the right).
func TestRenderStatusBarFocusToolsRunHint(t *testing.T) {
	m := Model{width: 80, focus: focusTools}
	got := m.renderStatusBar()
	if !strings.Contains(got, "[enter] run") {
		t.Errorf("focusTools status bar missing the [enter] run cell: %q", got)
	}

	// Narrow terminal: renderHintsBar drops cells from the right, so the
	// first-placed [enter] run must outlive the right-side reminders. Width 24
	// (inner 22) fits exactly one cell.
	m.width = 24
	got = m.renderStatusBar()
	if !strings.Contains(got, "[enter] run") {
		t.Errorf("narrow focusTools bar dropped the [enter] run cell: %q", got)
	}
	if strings.Contains(got, "[q] quit") || strings.Contains(got, "[?] keys") {
		t.Errorf("narrow focusTools bar kept right-side cells that should drop first: %q", got)
	}
}
