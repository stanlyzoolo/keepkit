package model

import (
	"strings"
	"testing"
	"time"
)

// segJoin reassembles what rcSegments split, which must reproduce the input
// byte for byte — segments are line runs, nothing else.
func segJoin(segs []rcSegment) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = s.text
	}
	return strings.Join(parts, "\n")
}

func TestRcSegments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []rcSegment
	}{
		{
			name: "no fence",
			in:   "# Title\n\nbody\n",
			want: []rcSegment{{text: "# Title\n\nbody\n"}},
		},
		{
			name: "fence with info string",
			in:   "intro\n```go\ncode\n```\nafter\n",
			want: []rcSegment{
				{text: "intro"},
				{text: "```go\ncode\n```", protected: true},
				{text: "after\n"},
			},
		},
		{
			name: "tilde fence with info string",
			in:   "intro\n~~~sh\ncode\n~~~\nafter",
			want: []rcSegment{
				{text: "intro"},
				{text: "~~~sh\ncode\n~~~", protected: true},
				{text: "after"},
			},
		},
		{
			// How a README documents markdown itself: the outer fence must
			// swallow the inner ``` blocks rather than closing on the first one.
			name: "long fence wraps short fences",
			in:   "intro\n````md\n```go\ncode\n```\n````\nafter",
			want: []rcSegment{
				{text: "intro"},
				{text: "````md\n```go\ncode\n```\n````", protected: true},
				{text: "after"},
			},
		},
		{
			// A tilde run never closes a backtick fence.
			name: "closer must match the opener character",
			in:   "```\ncode\n~~~\nstill code\n```\nafter",
			want: []rcSegment{
				{text: "```\ncode\n~~~\nstill code\n```", protected: true},
				{text: "after"},
			},
		},
		{
			// version.getReadme truncates at readmeMaxBytes and the cut can land
			// mid-fence; half a code sample is still code.
			name: "unterminated fence protects to EOF",
			in:   "intro\n```go\ncode\n![badge](a.png)",
			want: []rcSegment{
				{text: "intro"},
				{text: "```go\ncode\n![badge](a.png)", protected: true},
			},
		},
		{
			name: "fence nested under a list item",
			in:   "1. step\n\n    ```sh\n    run\n    ```\n\ndone",
			want: []rcSegment{
				{text: "1. step\n"},
				{text: "    ```sh\n    run\n    ```", protected: true},
				{text: "\ndone"},
			},
		},
		{
			name: "two fences",
			in:   "a\n```\none\n```\nb\n```\ntwo\n```\nc",
			want: []rcSegment{
				{text: "a"},
				{text: "```\none\n```", protected: true},
				{text: "b"},
				{text: "```\ntwo\n```", protected: true},
				{text: "c"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rcSegments(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d segments, want %d\ngot:  %#v\nwant: %#v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("segment %d = %#v, want %#v", i, got[i], tt.want[i])
				}
			}
			if joined := segJoin(got); joined != tt.in {
				t.Errorf("reassembled = %q, want the input %q", joined, tt.in)
			}
		})
	}
}

// Junk shaped exactly like what the removal rules delete must survive inside a
// fence byte for byte — a README showing markdown is the whole reason the
// segmentation exists.
func TestCleanReadmeMarkdownProtectsFences(t *testing.T) {
	const fence = "```md\n![Build](https://img.shields.io/badge.svg)\n<div align=\"center\">🚀 :rocket:</div>\n[docs](https://example.com/docs)\n<!-- a comment -->\n```"
	got := cleanReadmeMarkdown("intro\n\n" + fence + "\n\nafter\n")
	if !strings.Contains(got, fence) {
		t.Errorf("fenced block was rewritten\n--- got ---\n%s\n--- want to contain ---\n%s", got, fence)
	}
}

func TestCleanReadmeMarkdownProtectsInlineSpans(t *testing.T) {
	tests := []struct {
		name string
		span string
	}{
		{"image", "`![Build](badge.svg)`"},
		{"link", "`[docs](https://example.com)`"},
		{"html tag", "`<div align=\"center\">`"},
		{"html comment", "`<!-- hidden -->`"},
		{"emoji", "`🚀`"},
		{"shortcode", "`:rocket:`"},
		{"double backtick span holding a backtick", "``a ` b``"},
		{"reference image", "`![Build][badge]`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanReadmeMarkdown("text " + tt.span + " tail\n")
			if !strings.Contains(got, tt.span) {
				t.Errorf("inline span was rewritten\ngot:  %q\nwant to contain: %q", got, tt.span)
			}
		})
	}
}

func TestRcMaskSpansRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantSpans []string
	}{
		{"no backticks", "plain text", nil},
		{"one span", "use `keepkit --version` now", []string{"`keepkit --version`"}},
		{"two spans", "`a` and `b`", []string{"`a`", "`b`"}},
		{"double delimiter", "``a ` b``", []string{"``a ` b``"}},
		// A run with no closer of the same length is literal text, not a span.
		{"unclosed", "a ` b", nil},
		{"closer of a different length", "a ``b` c", nil},
		// A code span lives inside one paragraph: a stray backtick must not
		// pair with one in the next section and exempt everything between.
		{"blank line stops the search", "stray ` here\n\nnext ` section", nil},
		{"span across one line break", "a `b\nc` d", []string{"`b\nc`"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			masked, spans := rcMaskSpans(tt.in)
			if len(spans) != len(tt.wantSpans) {
				t.Fatalf("masked %d spans %q, want %d %q", len(spans), spans, len(tt.wantSpans), tt.wantSpans)
			}
			for i := range spans {
				if spans[i] != tt.wantSpans[i] {
					t.Errorf("span %d = %q, want %q", i, spans[i], tt.wantSpans[i])
				}
			}
			if len(spans) > 0 && strings.Contains(masked, "`") {
				t.Errorf("masked text still carries a backtick: %q", masked)
			}
			if got := rcUnmaskSpans(masked, spans); got != tt.in {
				t.Errorf("round trip = %q, want %q", got, tt.in)
			}
		})
	}
}

func TestCleanReadmeMarkdownImagesAndLinks(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "inline badge header vanishes",
			in:   "# Title\n\n![Build](https://img.shields.io/b.svg) ![License](https://img.shields.io/l.svg)\n\nIntro\n",
			want: "# Title\n\nIntro\n",
		},
		{
			name: "linked badge vanishes whole",
			in:   "[![Build](https://b.svg)](https://ci.example.com)\n\nIntro\n",
			want: "\nIntro\n",
		},
		{
			name: "reference badge leaves no orphan definition",
			in:   "![Build][badge]\n\nIntro\n\n[badge]: https://img.shields.io/b.svg\n",
			want: "\nIntro\n",
		},
		{
			name: "collapsed reference image",
			in:   "![Build][]\n\nIntro\n\n[Build]: https://img.shields.io/b.svg\n",
			want: "\nIntro\n",
		},
		{
			name: "shortcut reference image",
			in:   "![Build]\n\nIntro\n\n[Build]: https://img.shields.io/b.svg\n",
			want: "\nIntro\n",
		},
		{
			// The href is the noise; the sentence must close up around it with
			// exactly one space, not two.
			name: "inline link keeps its text",
			in:   "See the [docs](https://example.com/docs) for more.\n",
			want: "See the docs for more.\n",
		},
		{
			name: "mid-sentence image leaves no double space",
			in:   "Built ![badge](https://b.svg) with care.\n",
			want: "Built with care.\n",
		},
		{
			name: "reference link keeps its text",
			in:   "See the [docs][d] for more.\n\n[d]: https://example.com\n",
			want: "See the docs for more.\n",
		},
		{
			name: "shortcut link keeps its text",
			in:   "See [docs] for more.\n\n[docs]: https://example.com\n",
			want: "See docs for more.\n",
		},
		{
			// A table cell is where the style-level route left a footnote stub;
			// unwrapping in the source leaves the cell reading as plain text.
			name: "table link leaves no footnote stub",
			in:   "| doc | link |\n|---|---|\n| manual | [manual](https://example.com/m) |\n",
			want: "| doc | link |\n|---|---|\n| manual | manual |\n",
		},
		{
			name: "heading link keeps its text",
			in:   "## [Installation](#installation)\n",
			want: "## Installation\n",
		},
		{
			// A flat "[^)]*" destination stopped at the inner ")" and left the
			// rest of the URL on screen — the exact noise this pass removes.
			name: "badge url carrying parentheses goes whole",
			in:   "![alt](https://img.shields.io/badge/a-(b)-blue.svg)\n",
			want: "",
		},
		{
			name: "link url carrying parentheses",
			in:   "See [Foo](https://en.wikipedia.org/wiki/Foo_(bar)) here.\n",
			want: "See Foo here.\n",
		},
		{
			name: "ten-badge header folds to one blank line",
			in:   "# T\n\n![a](x)\n![b](y)\n![c](z)\n\nbody\n",
			want: "# T\n\nbody\n",
		},
		{
			// Two trailing spaces are markup; a removal inside the line has no
			// business retiring them.
			name: "hard line break survives a removal",
			in:   "see [docs](https://example.com)  \nsecond\n",
			want: "see docs  \nsecond\n",
		},
		{
			// Removal empties the title, and the underline must go with it
			// rather than staying behind as a row of equals signs.
			name: "orphaned setext underline goes with its title",
			in:   "\U0001F680\n=====\n\nbody\n",
			want: "\nbody\n",
		},
		{
			// A stray backtick must not pair with one further down and exempt
			// everything between from cleaning.
			name: "stray backtick does not protect the rest of the document",
			in:   "stray ` here\n\n![b](https://b.svg)\n\ntail\n",
			want: "stray ` here\n\ntail\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanReadmeMarkdown(tt.in); got != tt.want {
				t.Errorf("cleanReadmeMarkdown(%q)\n = %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCleanReadmeMarkdownHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "comment removed whole",
			in:   "a\n\n<!-- hidden\nmultiline -->\n\nb\n",
			want: "a\n\nb\n",
		},
		{
			name: "script body removed",
			in:   "a\n<script>\nvar x = 1;\n</script>\nb\n",
			want: "a\n\nb\n",
		},
		{
			name: "style body removed",
			in:   "a\n<style>\n.x { color: red }\n</style>\nb\n",
			want: "a\n\nb\n",
		},
		{
			name: "picture element removed with its body",
			in:   "<picture>\n  <source srcset=\"d.png\">\n  <img src=\"l.png\" alt=\"logo\">\n</picture>\n\n# Title\n",
			want: "\n# Title\n",
		},
		{
			name: "img tag removed",
			in:   "<img src=\"logo.png\" alt=\"logo\">\n\n# Title\n",
			want: "\n# Title\n",
		},
		{
			name: "inline tag keeps its text",
			in:   "Press <kbd>Ctrl</kbd>+<kbd>C</kbd> to quit.\n",
			want: "Press Ctrl+C to quit.\n",
		},
		{
			name: "details block keeps its content",
			in:   "<details>\n<summary>More</summary>\n\nbody text\n\n</details>\n",
			want: "\nMore\n\nbody text\n",
		},
		{
			name: "centered html header collapses",
			in:   "<h1 align=\"center\">keepkit</h1>\n<p align=\"center\">\n  <img src=\"a.svg\">\n  <img src=\"b.svg\">\n</p>\n\nIntro\n",
			want: "keepkit\n\nIntro\n",
		},
		{
			name: "br keeps the line",
			in:   "first<br>\nsecond\n",
			want: "first\nsecond\n",
		},
		{
			// A flat [^<>]* ends the tag at the ">" inside the quoted value and
			// leaves the tail of the tag on screen.
			name: "attribute value carrying a greater-than",
			in:   "<img alt=\"a > b\" src=\"x.png\">\n\n# Title\n",
			want: "\n# Title\n",
		},
		{
			// The label set is collected AFTER the HTML rules run: gathering it
			// first let a commented-out definition gate the unwrapping and eat
			// the task-list bracket the gate exists to protect.
			name: "label declared inside a comment gates nothing",
			in:   "<!--\n[x]: https://example.com\n-->\n\n- [x] done\n",
			want: "\n- [x] done\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanReadmeMarkdown(tt.in); got != tt.want {
				t.Errorf("cleanReadmeMarkdown(%q)\n = %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCleanReadmeMarkdownEmojiAndShortcodes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "heading keeps its words",
			in:   "## 🚀 Getting Started\n",
			want: "## Getting Started\n",
		},
		{
			// A heading whose only content was a pictograph would render as a
			// styled blank row.
			name: "emoji-only heading is dropped",
			in:   "# Title\n\n## 🚀\n\nbody\n",
			want: "# Title\n\nbody\n",
		},
		{
			name: "leading emoji leaves no indent",
			in:   "🎉 Released today.\n",
			want: "Released today.\n",
		},
		{
			name: "list item keeps its marker",
			in:   "- 🚀 fast\n- 🔒 safe\n",
			want: "- fast\n- safe\n",
		},
		{
			name: "keycap leaves no combining mark",
			in:   "1️⃣ first step\n",
			want: "1 first step\n",
		},
		{
			name: "zwj sequence goes whole",
			in:   "by 👩‍💻 the team\n",
			want: "by the team\n",
		},
		{
			name: "variation selector goes with its symbol untouched",
			in:   "power ⚡️ mode\n",
			want: "power ⚡ mode\n",
		},
		{
			// A feature table's meaning lives in the ✅/❌ column.
			name: "feature table marks are translated",
			in:   "| feature | linux | windows |\n|---|---|---|\n| tabs | ✅ | ❌ |\n| tags | ☑ | ❌ |\n",
			want: "| feature | linux | windows |\n|---|---|---|\n| tabs | ✓ | ✗ |\n| tags | ✓ | ✗ |\n",
		},
		{
			name: "known shortcode removed",
			in:   ":rocket: Fast by default.\n",
			want: "Fast by default.\n",
		},
		{
			name: "shortcode mid-sentence leaves no double space",
			in:   "Fast :rocket: by default.\n",
			want: "Fast by default.\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanReadmeMarkdown(tt.in); got != tt.want {
				t.Errorf("cleanReadmeMarkdown(%q)\n = %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

// The dictionary lookup, not the ":name:" shape, is what makes a shortcode.
func TestCleanReadmeMarkdownShortcodeNeedsAKnownName(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		removed bool
	}{
		{"known name", ":rocket: go\n", true},
		{"known name with plus", ":+1: agreed\n", true},
		{"unknown name", ":foo: go\n", false},
		{"clock reading", "at 12:30:45 today\n", false},
		{"ratio", "a 3:4:5 triangle\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanReadmeMarkdown(tt.in)
			if changed := got != tt.in; changed != tt.removed {
				t.Errorf("cleanReadmeMarkdown(%q) = %q; removed=%v, want removed=%v", tt.in, got, changed, tt.removed)
			}
		})
	}
}

// What the removal rules must NOT touch. An autolink's URL is its content, and
// prose angle brackets are not markup — the tag allowlist is what separates
// them from <kbd>.
func TestCleanReadmeMarkdownLeavesContentAlone(t *testing.T) {
	unchanged := []struct {
		name string
		in   string
	}{
		{"autolink", "See <https://example.com/docs> here.\n"},
		{"email autolink", "Mail <user@host.com> now.\n"},
		{"bare url", "See https://example.com/docs here.\n"},
		{"generic type", "Use Vec<String> for that.\n"},
		{"less-than in prose", "true when a < b holds\n"},
		{"unknown tag left as written", "the <Widget> component\n"},
		{"time is not a shortcode", "at 12:30:45 today\n"},
		{"unknown shortcode", "the :foo: marker\n"},
		{"bmp symbols a terminal font carries", "✓ done ★ starred → next\n"},
		{"task list brackets", "- [x] done\n- [ ] todo\n"},
		{"undeclared shortcut label", "marked [experimental] for now\n"},
		{"undeclared reference link", "see [text][nope] here\n"},
		// Two bracket pairs in a row are an array index far more often than a
		// reference link, which is why the reference form is label-gated too.
		{"array index in prose", "the value arr[i][j] is used\n"},
		{"definition-shaped line inside a paragraph", "Some prose here\n[note]: this matters\nmore prose\n"},
		{"numbered prose line", "[1]: first item explained\n"},
		{"thematic break", "above\n\n- - -\n\nbelow\n"},
		{"issue reference", "#123 fixed the crash\n"},
		{"hard line break", "first  \nsecond\n"},
		{"list indentation", "- one\n  - nested\n    - deeper\n"},
	}
	for _, tt := range unchanged {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanReadmeMarkdown(tt.in); got != tt.in {
				t.Errorf("cleanReadmeMarkdown(%q) = %q, want it unchanged", tt.in, got)
			}
		})
	}
}

// CRLF is normalized on entry. Without it a "```\r" closing fence never
// matches, the first fence protects everything to EOF, and nothing is cleaned
// at all — invisible to a suite that only ever feeds LF.
func TestCleanReadmeMarkdownNormalizesCRLF(t *testing.T) {
	const lf = "# T\n\n```go\ncode\n```\n\n![b](https://b.svg)\n\ntail\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")

	want := cleanReadmeMarkdown(lf)
	if got := cleanReadmeMarkdown(crlf); got != want {
		t.Errorf("CRLF input\n = %q\nwant the LF result %q", got, want)
	}
	if strings.Contains(want, "b.svg") {
		t.Fatalf("fixture is not exercising the badge removal: %q", want)
	}
}

// The pass runs synchronously inside Update() on up to readmeMaxBytes of
// untrusted remote markdown, so a pathological input must not freeze the TUI.
// The shape that did: one line of leading block markers, which made
// rcLineContent copy the remaining line once per marker — 8.4 s at the cap.
func TestCleanReadmeMarkdownPathologicalInputIsFast(t *testing.T) {
	const budget = 2 * time.Second
	inputs := map[string]string{
		"leading bullets":  "\U0001F680 " + strings.Repeat("- ", 262144),
		"leading quotes":   "\U0001F680 " + strings.Repeat("> ", 262144),
		"leading headings": "\U0001F680 " + strings.Repeat("# ", 262144),
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			cleanReadmeMarkdown(in)
			if elapsed := time.Since(start); elapsed > budget {
				t.Errorf("%d bytes took %v, want under %v", len(in), elapsed, budget)
			}
		})
	}
}

// The NUL sentinel cannot reach the pass in production (cleanTerminalOutput
// drops every control character), and a direct caller must not be able to
// smuggle a placeholder into the restore table either.
func TestCleanReadmeMarkdownDropsSentinel(t *testing.T) {
	got := cleanReadmeMarkdown("a\x000\x00b `code` c\n")
	if strings.Contains(got, "\x00") {
		t.Errorf("sentinel survived: %q", got)
	}
	if !strings.Contains(got, "`code`") {
		t.Errorf("code span lost: %q", got)
	}
}
