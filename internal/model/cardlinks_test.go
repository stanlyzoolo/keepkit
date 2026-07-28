package model

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/ui"
	"github.com/stanlyzoolo/keepkit/internal/version"
)

const (
	linkRepo   = "github.com/cli/cli"
	linkShort  = "cli/cli"
	linkRelURL = "https://github.com/cli/cli/releases/tag/v2.0.0"
)

// linkedCardModel builds a ready model with one GitHub-tracked tool — the
// shape the brief panel's clickable lines are asserted against.
func linkedCardModel(t *testing.T, github string) Model {
	t.Helper()
	m := New([]loader.ToolMeta{{Name: "gh", GitHub: github}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	return updated.(Model)
}

// cardLine returns content line idx, ANSI stripped ("" when out of range).
func cardLine(content string, idx int) string {
	lines := strings.Split(content, "\n")
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return stripANSI(lines[idx])
}

// TestBuildCardLinks pins the clickable-line index across card variants: the
// repo line and (only when the changelog block actually leads with it) the
// release URL, each recorded at the line it is written on. Line heights vary
// with wrapping, which is exactly what the index has to survive.
func TestBuildCardLinks(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m *Model)
		// want maps a URL to a substring the line it was recorded at must contain.
		want map[string]string
	}{
		{
			name:  "repo line only while the changelog loads",
			setup: func(m *Model) { m.changelogLoadingFor = "gh" },
			want:  map[string]string{"https://" + linkRepo: linkShort},
		},
		{
			name: "changelog url linked alongside the repo",
			setup: func(m *Model) {
				m.changelogData["gh"] = changelogMsg{toolName: "gh", htmlUrl: linkRelURL, body: "release notes"}
			},
			want: map[string]string{
				"https://" + linkRepo: linkShort,
				linkRelURL:            "release notes",
			},
		},
		{
			name: "failed changelog carries no url",
			setup: func(m *Model) {
				m.changelogData["gh"] = changelogMsg{toolName: "gh", htmlUrl: linkRelURL, err: errors.New("boom")}
			},
			want: map[string]string{"https://" + linkRepo: linkShort},
		},
		{
			name: "release without an html url",
			setup: func(m *Model) {
				m.changelogData["gh"] = changelogMsg{toolName: "gh", body: "release notes"}
			},
			want: map[string]string{"https://" + linkRepo: linkShort},
		},
		{
			name: "update marker and wrapped about shift both lines",
			setup: func(m *Model) {
				m.versions["gh"] = VersionInfo{Installed: "v1.0.0", Latest: "v2.0.0", InstalledKnown: true}
				m.repoCards["gh"] = version.RepoCard{
					Latest: "v2.0.0",
					About:  strings.Repeat("a wordy description ", 12),
				}
				m.changelogData["gh"] = changelogMsg{toolName: "gh", htmlUrl: linkRelURL, body: "release notes"}
			},
			want: map[string]string{
				"https://" + linkRepo: linkShort,
				linkRelURL:            "release notes",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := linkedCardModel(t, linkRepo)
			tt.setup(&m)
			content, links := m.buildCard()

			if len(links) != len(tt.want) {
				t.Fatalf("links = %v, want %d entries (%v)", links, len(tt.want), tt.want)
			}
			for line, url := range links {
				marker, ok := tt.want[url]
				if !ok {
					t.Errorf("unexpected link %q at line %d", url, line)
					continue
				}
				if got := cardLine(content, line); !strings.Contains(got, marker) {
					t.Errorf("link %q recorded at line %d = %q, want it to contain %q",
						url, line, got, marker)
				}
			}
		})
	}

	t.Run("wrapping really moved the changelog link", func(t *testing.T) {
		// Guards the table's last case against passing trivially: the changelog
		// heading's index must shift when the tagline above it wraps, which is
		// what "record while writing" buys over hardcoded offsets. (The repo
		// link rides the title line and is pinned at 0 by construction.)
		plain := linkedCardModel(t, linkRepo)
		plain.changelogData["gh"] = changelogMsg{toolName: "gh", htmlUrl: linkRelURL, body: "release notes"}
		_, plainLinks := plain.buildCard()

		wrapped := linkedCardModel(t, linkRepo)
		wrapped.repoCards["gh"] = version.RepoCard{About: strings.Repeat("a wordy description ", 12)}
		wrapped.changelogData["gh"] = changelogMsg{toolName: "gh", htmlUrl: linkRelURL, body: "release notes"}
		_, wrappedLinks := wrapped.buildCard()

		lineOf := func(links map[int]string, url string) int {
			for line, u := range links {
				if u == url {
					return line
				}
			}
			return -1
		}
		before, after := lineOf(plainLinks, linkRelURL), lineOf(wrappedLinks, linkRelURL)
		if before < 0 || after <= before {
			t.Errorf("changelog link at line %d unwrapped and %d wrapped, want it pushed down", before, after)
		}
	})

	// The repo line shows the bare owner/repo but links the full ref, so the
	// two must not be assumed equal — and a ref NormalizeRepo rejects (an
	// unsupported or spoofed host) must render in full, or the display would
	// hide the host that makes the link not what it looks like.
	t.Run("shortened display, full link", func(t *testing.T) {
		m := linkedCardModel(t, linkRepo)
		content, links := m.buildCard()
		for line, url := range links {
			if url != "https://"+linkRepo {
				continue
			}
			got := cardLine(content, line)
			if !strings.Contains(got, linkShort) {
				t.Errorf("repo line = %q, want the bare %q", got, linkShort)
			}
			if strings.Contains(got, "github.com") {
				t.Errorf("repo line = %q, want the host dropped from the display", got)
			}
		}

		spoofed := "github.com.evil.com/cli/cli"
		m = linkedCardModel(t, spoofed)
		content, links = m.buildCard()
		if len(links) != 1 {
			t.Fatalf("links = %v, want the repo line linked", links)
		}
		for line, url := range links {
			if url != "https://"+spoofed {
				t.Errorf("link = %q, want the raw ref %q", url, "https://"+spoofed)
			}
			if got := cardLine(content, line); !strings.Contains(got, spoofed) {
				t.Errorf("repo line = %q, want the unshortenable ref shown in full", got)
			}
		}
	})

	t.Run("no repo, no links", func(t *testing.T) {
		m := linkedCardModel(t, "")
		if _, links := m.buildCard(); len(links) != 0 {
			t.Errorf("links = %v, want none for a tool with no GitHub ref", links)
		}
	})

	t.Run("renderCard is buildCard's text", func(t *testing.T) {
		m := linkedCardModel(t, linkRepo)
		content, _ := m.buildCard()
		if got := m.renderCard(); got != content {
			t.Errorf("renderCard diverged from buildCard:\n%q\nvs\n%q", got, content)
		}
	})
}

// TestBriefContentLineIsScreenRow pins the invariant the whole click mapping
// rests on: a content line wider than the panel (the release URL is regularly
// one) is *truncated* by the viewport, never wrapped onto a second screen row.
// If bubbles ever soft-wrapped instead, a content-line index would stop being a
// screen row and every link below an overlong line would silently shift.
func TestBriefContentLineIsScreenRow(t *testing.T) {
	m := linkedCardModel(t, linkRepo)
	// A synthetic overlong line: the card's own renderers all fit the panel by
	// construction now, and what is under test is the viewport's behaviour, not
	// theirs — a bubbles that soft-wrapped would break the mapping no matter
	// which line was too wide.
	long := "https://github.com/cli/cli/releases/tag/" + strings.Repeat("v", 120)
	m.briefViewport.SetContent("first\n" + long + "\nthird")

	rows := strings.Split(withScrollbar(ui.DefaultStyles(), m.briefViewport, m.briefW, false), "\n")
	if len(rows) < 3 {
		t.Fatalf("rendered %d rows, want at least 3", len(rows))
	}
	if got := strings.TrimSpace(stripANSI(rows[1])); !strings.HasPrefix(got, "https://github.com/cli/cli") {
		t.Errorf("screen row 1 = %q, want the (truncated) long line", got)
	}
	// The row below must be the next content line, not the long line's tail.
	if got := strings.TrimSpace(stripANSI(rows[2])); got != "third" {
		t.Errorf("screen row 2 = %q, want the next content line — the long line spilled over", got)
	}
}

// TestMouseBriefLinkClick: a left click on a linked line dispatches the browser
// command, a click on any other line does not, and the row→line arithmetic
// follows the viewport's scroll offset.
func TestMouseBriefLinkClick(t *testing.T) {
	newModel := func(t *testing.T) Model {
		t.Helper()
		m := linkedCardModel(t, linkRepo)
		m.changelogData["gh"] = changelogMsg{
			toolName: "gh",
			htmlUrl:  linkRelURL,
			body:     strings.Repeat("release notes paragraph. ", 60),
		}
		m.briefViewport.SetContent(m.renderCard())
		return m
	}
	// X anywhere inside the brief panel: it starts right after the tools panel
	// (toolsW + 2 border columns).
	briefX := func(m Model) int { return m.toolsW + 3 }

	t.Run("linked lines open the browser", func(t *testing.T) {
		m := newModel(t)
		_, links := m.buildCard()
		if len(links) != 2 {
			t.Fatalf("setup: links = %v, want the repo and changelog lines", links)
		}
		for line, url := range links {
			_, cmd := m.Update(leftClick(briefX(m), line+2))
			if cmd == nil {
				t.Errorf("click on line %d (%s) dispatched no command", line, url)
			}
		}
	})

	t.Run("other lines do nothing", func(t *testing.T) {
		m := newModel(t)
		_, links := m.buildCard()
		// Line 0 is the card's blank top row and line 1 the title, which carries
		// the repo link; line 2 is the tagline, never clickable.
		if _, ok := links[2]; ok {
			t.Fatalf("setup: line 2 unexpectedly linked")
		}
		updated, cmd := m.Update(leftClick(briefX(m), 2+2))
		if cmd != nil {
			t.Errorf("click on an unlinked line dispatched a command")
		}
		if updated.(Model).focus != focusBrief {
			t.Errorf("click did not focus the brief panel")
		}
	})

	t.Run("scrolled viewport shifts the rows", func(t *testing.T) {
		m := newModel(t)
		m.briefViewport.SetYOffset(3)
		if m.briefViewport.YOffset != 3 {
			t.Fatalf("setup: YOffset = %d, want 3 (card not tall enough?)", m.briefViewport.YOffset)
		}
		_, links := m.buildCard()
		var logLine int
		for line, url := range links {
			if url == linkRelURL {
				logLine = line
			}
		}
		if logLine < 4 {
			t.Fatalf("setup: changelog link at line %d, too close to the top to scroll past", logLine)
		}
		// The same screen row now shows a different content line: clicking where
		// the changelog heading was before the scroll must no longer open it.
		if _, cmd := m.Update(leftClick(briefX(m), logLine+2)); cmd != nil {
			t.Errorf("click ignored the scroll offset and still opened a link")
		}
		if _, cmd := m.Update(leftClick(briefX(m), logLine-3+2)); cmd == nil {
			t.Errorf("click at the scrolled changelog row dispatched no command")
		}
	})

	t.Run("clicks outside the viewport open nothing", func(t *testing.T) {
		// X inside the brief column is not enough: the outer Margin(1,0) row,
		// the panel borders and the status/hints bars share those columns. With
		// a scrolled viewport an unbounded row would map onto a real link and
		// launch the browser from a click on empty chrome.
		m := newModel(t)
		m.briefViewport.SetYOffset(6)
		if m.briefViewport.YOffset != 6 {
			t.Fatalf("setup: YOffset = %d, want 6 (card not tall enough?)", m.briefViewport.YOffset)
		}
		outside := []struct {
			name string
			y    int
		}{
			{"top margin", 0},
			{"panel top border", 1},
			{"panel bottom border", 2 + m.briefViewport.Height},
			{"status bar", 2 + m.briefViewport.Height + 1},
			{"below the terminal", m.height + 5},
		}
		for _, tt := range outside {
			if _, cmd := m.Update(leftClick(briefX(m), tt.y)); cmd != nil {
				t.Errorf("click on the %s (y=%d) dispatched a command", tt.name, tt.y)
			}
		}
	})

	t.Run("no clicks while an input mode owns the keyboard", func(t *testing.T) {
		m := newModel(t)
		m.mode = modeEditNote
		_, links := m.buildCard()
		for line := range links {
			if _, cmd := m.Update(leftClick(briefX(m), line+2)); cmd != nil {
				t.Errorf("click on line %d opened a link while the note editor was open", line)
			}
		}
	})
}
