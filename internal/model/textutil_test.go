package model

import (
	"slices"
	"testing"
	"time"
)

func TestMdInline(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text untouched", "just a sentence", "just a sentence"},
		{"link keeps text only", "see [the docs](https://example.com) now", "see the docs now"},
		{"two links on one line", "[a](https://a) and [b](https://b)", "a and b"},
		{"link inside a sentence", "fixed in [#42](https://x/pull/42).", "fixed in #42."},
		{"image renders its alt", "![build badge](https://img/b.svg)", "build badge"},
		{"image without alt disappears", "![](https://img/b.svg)", ""},
		{"image before link so no stray bang", "x ![a](u) y", "x a y"},
		// The HTML-tag strip used to eat autolinks whole — this is the regression.
		{"autolink survives the html strip", "notes: <https://example.com/rel/v1>", "notes: https://example.com/rel/v1"},
		{"mailto autolink", "write <mailto:a@b.c>", "write mailto:a@b.c"},
		{"html tag is cut", "a <br/> b <span class=\"x\">c</span>", "a  b c"},
		{"bare url untouched", "see https://example.com/a_b for details", "see https://example.com/a_b for details"},
		{"strong stars", "**Breaking**: the flag is gone", "Breaking: the flag is gone"},
		{"strong underscores", "__Breaking__ change", "Breaking change"},
		{"emphasis stars", "a *very* small fix", "a very small fix"},
		{"emphasis underscores", "a _very_ small fix", "a very small fix"},
		// A match consumes the boundaries it asserts, so the span after a
		// single separating space needs a second pass to be reached.
		{"two underscore spans in a row", "x _a_ _b_ y", "x a b y"},
		{"three underscore spans in a row", "_a_ _b_ _c_", "a b c"},
		{"code span", "pass `--verbose` to it", "pass --verbose to it"},
		// Inline code is masked before the rules run: nothing may rewrite what
		// the author meant verbatim. The first two are the regressions — the
		// HTML strip ate a flag's argument, emphasis fired across two spans.
		{"code span keeps flag argument", "use `--output <path>` here", "use --output <path> here"},
		{"emphasis cannot cross two spans", "`a*b` and `c*d`", "a*b and c*d"},
		{"backtick as span content survives", "run ``a`b`` now", "run a`b now"},
		{"underscores in span untouched", "see `_private_field` docs", "see _private_field docs"},
		// The HTML strip is rcHTMLTagRe's allowlist, not a generic <…> eater:
		// angle-bracket prose in release notes is not markup.
		{"generic type parameter survives", "changed Vec<String> to Vec<Bytes>", "changed Vec<String> to Vec<Bytes>"},
		{"bracketed email survives", "contact <support@example.com>", "contact <support@example.com>"},
		// Identifiers must survive: an unguarded '_' strip turns update_cmd into
		// updatecmd, and multiplication into emphasis.
		{"snake_case identifier kept", "set update_cmd in meta.yaml", "set update_cmd in meta.yaml"},
		{"multiple underscores kept", "foo_bar_baz stays", "foo_bar_baz stays"},
		{"spaced asterisks kept", "2 * 3 * 4", "2 * 3 * 4"},
		// Pathological input: no panic, no mangling beyond the obvious.
		{"unclosed bracket", "a [text without a paren", "a [text without a paren"},
		{"unclosed angle", "a < b and c", "a < b and c"},
		{"unclosed emphasis", "a *dangling marker", "a *dangling marker"},
		{"empty input", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mdInline(tt.in); got != tt.want {
				t.Errorf("mdInline(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// mdDump renders converted lines as "H|text" / "B|text" so a table row can
// assert kind and exact column layout in one readable literal.
func mdDump(lines []mdLine) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		tag := "B"
		if l.kind == mdHeading {
			tag = "H"
		}
		out = append(out, tag+"|"+l.text)
	}
	return out
}

func TestMarkdownToLines(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  []string
	}{
		{"empty input", "", 80, nil},
		{
			"headings h1 to h6",
			"# One\n## Two\n### Three\n#### Four\n##### Five\n###### Six",
			80,
			[]string{"H|One", "H|Two", "H|Three", "H|Four", "H|Five", "H|Six"},
		},
		{
			// The space after the hashes is required, so a leading issue ref
			// stays text — the old TrimLeft(line, "#") mangled it to "123 …".
			"issue ref is not a heading",
			"#123 fixed the crash",
			80,
			[]string{"B|#123 fixed the crash"},
		},
		{
			"heading keeps link text only",
			"## [v1.2.3](https://x/releases/v1.2.3) — hotfix",
			80,
			[]string{"H|v1.2.3 — hotfix"},
		},
		{
			"nested bullets normalize to dots",
			"- top level\n  - nested one\n    - deep\n      - deeper still",
			80,
			[]string{"B|• top level", "B|  • nested one", "B|    • deep", "B|    • deeper still"},
		},
		{
			"numbered items keep their number",
			"1. first\n2. second\n10) tenth",
			80,
			[]string{"B|1. first", "B|2. second", "B|10. tenth"},
		},
		{
			"three-space indent is one level, tab is two",
			"   - three spaces\n\t- one tab",
			80,
			[]string{"B|  • three spaces", "B|    • one tab"},
		},
		{
			// Continuations align under the first text column, not the marker.
			"top-level item wraps with a hanging indent",
			"- alpha beta gamma delta",
			12,
			[]string{"B|• alpha beta", "B|  gamma", "B|  delta"},
		},
		{
			"nested item wraps against the narrowest allowed width",
			"  - alpha beta",
			10,
			[]string{"B|  • alpha", "B|    beta"},
		},
		{
			"emphasis at line start stays body text",
			"*emphasis* leads\n**Breaking**: flag removed",
			80,
			[]string{"B|emphasis leads", "B|Breaking: flag removed"},
		},
		{
			"marker with no content is not an item",
			"- one\n-\n- two",
			80,
			[]string{"B|• one", "B|", "B|• two"},
		},
		{
			"thematic breaks drop to a blank line",
			"before\n---\n***\n___\nafter",
			80,
			[]string{"B|before", "B|", "B|after"},
		},
		{
			"fenced code is verbatim and indented",
			"text\n```go\nx := 1\n    deep\n```\nmore",
			80,
			[]string{"B|text", "B|  x := 1", "B|      deep", "B|more"},
		},
		{
			"code is never wrapped",
			"```\nkeepkit --flag one two three four five\n```",
			12,
			[]string{"B|  keepkit --flag one two three four five"},
		},
		{
			"code keeps its markup and blank lines",
			"```sh\n**not bold**\n\n# not a heading\n```",
			80,
			[]string{"B|  **not bold**", "B|", "B|  # not a heading"},
		},
		{
			// The blank-line collapse never sees the code branch's verbatim
			// lines, so a block opening on a blank one must not leave a
			// leading blank row nothing trims.
			"code opening on a blank line has no leading blank",
			"```\n\ncode\n```",
			80,
			[]string{"B|  code"},
		},
		{
			// A line with nothing but a badge is empty after the inline pass;
			// it must read as a blank, not as an empty body line.
			"badge-only line collapses to a blank",
			"text\n![](https://img.shields.io/badge.svg)\nmore",
			80,
			[]string{"B|text", "B|", "B|more"},
		},
		{
			// An alt-less badge wrapped in a link: the image leaves nothing for
			// the link to carry, so the whole body converts to nothing and the
			// card falls through to "no release notes available.".
			"a body that is only a badge converts to nothing",
			"[![](https://img/b.svg)](https://ci/x)",
			80,
			nil,
		},
		{
			"an html-only line collapses to a blank",
			"text\n<div align=\"center\">\nmore",
			80,
			[]string{"B|text", "B|", "B|more"},
		},
		{
			// A fence nested under a list item sits past CommonMark's 3-space
			// limit ("1. " content aligns at column 4). Enforcing the limit
			// leaked the language tag as a body line reading "go" and dropped
			// the sample's verbatim treatment.
			"fence indented under a list item still opens",
			"1. run this:\n\n    ```go\n    x := 1\n    ```\n2. done",
			80,
			[]string{"B|1. run this:", "B|", "B|      x := 1", "B|2. done"},
		},
		{
			"setext underline does not survive as a row of equals",
			"Highlights\n==========\nbody",
			80,
			[]string{"B|Highlights", "B|", "B|body"},
		},
		{
			// A heading whose only content is an image converts to nothing; the
			// blank must come from the collapse, never as an empty line still
			// tagged mdHeading.
			"heading that collapses to nothing yields a body blank",
			"a\n## ![](https://img/x.svg)\n\nb",
			80,
			[]string{"B|a", "B|", "B|b"},
		},
		{
			"a body that is only an empty heading converts to nothing",
			"## ![](https://img/x.svg)",
			80,
			nil,
		},
		{
			"unclosed fence runs to the end",
			"text\n~~~\ncode line",
			80,
			[]string{"B|text", "B|  code line"},
		},
		{
			// A fence closes on its own marker only — the way a README
			// documents markdown itself is a ~~~ block wrapping ``` samples.
			// Closing on either marker ended the block at the inner fence and
			// then re-opened one on the next, so the sample's own text came out
			// as body and the tail after the real closer was swallowed as code.
			"fence closes only on its own marker",
			"~~~\n```\nsample\n```\n~~~\ntail",
			80,
			[]string{"B|  ```", "B|  sample", "B|  ```", "B|tail"},
		},
		{
			// The same rule the README pass follows: a longer run closes a
			// shorter opener, a shorter one does not.
			"longer closing run closes a shorter opener",
			"```\ncode\n````\nafter",
			80,
			[]string{"B|  code", "B|after"},
		},
		{
			"shorter run does not close a longer opener",
			"````\ncode\n```\nstill code",
			80,
			[]string{"B|  code", "B|  ```", "B|  still code"},
		},
		{
			// Without the CR normalization the closing fence never matches and
			// the rest of the body is swallowed as code.
			"CRLF closing fence still closes",
			"```\r\ncode\r\n```\r\nafter",
			80,
			[]string{"B|  code", "B|after"},
		},
		{
			"CRLF body end to end",
			"# Title\r\n\r\n- item\r\n",
			80,
			[]string{"H|Title", "B|", "B|• item"},
		},
		{
			"blockquote marker is stripped",
			"> quoted text\n>> deep quote",
			80,
			[]string{"B|quoted text", "B|deep quote"},
		},
		{
			"list inside a quote stays a list",
			"> - quoted item",
			80,
			[]string{"B|• quoted item"},
		},
		{
			"table rows pass through",
			"| flag | meaning |\n| --- | --- |\n| -v | verbose |",
			80,
			[]string{"B|| flag | meaning |", "B|| --- | --- |", "B|| -v | verbose |"},
		},
		{
			"multi-line comment is cut whole",
			"<!--\ntemplate instructions\n-->\nreal text",
			80,
			[]string{"B|real text"},
		},
		{
			"unclosed comment runs to the end",
			"kept\n<!-- swallowed\nalso swallowed",
			80,
			[]string{"B|kept"},
		},
		{
			"inline comment leaves the surrounding text",
			"before <!-- note --> after",
			80,
			[]string{"B|before  after"},
		},
		{
			"a body that is only a comment converts to nothing",
			"<!-- nothing to see -->",
			80,
			nil,
		},
		{
			"blank runs collapse and the edges are trimmed",
			"\n\na\n\n\n\nb\n\n",
			80,
			[]string{"B|a", "B|", "B|b"},
		},
		{
			// The call site floors the width, but the converter is pure and
			// tables call it directly: width 0 must not wrap and must not loop.
			"width zero disables wrapping",
			"- alpha beta gamma delta epsilon zeta",
			0,
			[]string{"B|• alpha beta gamma delta epsilon zeta"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mdDump(markdownToLines(tt.in, tt.width))
			if !slices.Equal(got, tt.want) {
				t.Errorf("markdownToLines(%q, %d):\n got %q\nwant %q", tt.in, tt.width, got, tt.want)
			}
		})
	}
}

// TestMarkdownToLinesGitHubBody drives the shape GitHub's own release notes
// take: a "What's Changed" heading, "* … by @user in <url>" bullets and the
// "**Full Changelog**" line under a --- separator.
func TestMarkdownToLinesGitHubBody(t *testing.T) {
	body := "## What's Changed\r\n" +
		"* fix: detect uv tools by [@someone](https://github.com/someone) in https://github.com/o/r/pull/44\r\n" +
		"* docs: update README\r\n" +
		"\r\n" +
		"**Full Changelog**: https://github.com/o/r/compare/v1.0.0...v1.1.0\r\n"

	got := mdDump(markdownToLines(body, 200))
	want := []string{
		"H|What's Changed",
		"B|• fix: detect uv tools by @someone in https://github.com/o/r/pull/44",
		"B|• docs: update README",
		"B|",
		"B|Full Changelog: https://github.com/o/r/compare/v1.0.0...v1.1.0",
	}
	if !slices.Equal(got, want) {
		t.Errorf("github release body:\n got %q\nwant %q", got, want)
	}
}

// TestFormatElapsed: the three shapes the block can print, and the empty string
// for a process that never ran — a duration cell reading "0s" would claim the
// update took no time rather than that it never started.
func TestFormatElapsed(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"never started", 0, ""},
		{"negative is not a duration", -time.Second, ""},
		{"sub-second says so", 400 * time.Millisecond, "<1s"},
		{"seconds", 12 * time.Second, "12s"},
		{"rounds to the second", 12*time.Second + 400*time.Millisecond, "12s"},
		{"minutes", 90 * time.Second, "1m30s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatElapsed(tt.d); got != tt.want {
				t.Errorf("formatElapsed(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// TestUpdateVerifyLine: the four results of the post-update re-detect. The
// "still" row is the one the whole second phase exists for — a manager that
// exits zero having changed nothing is invisible in every other signal.
func TestUpdateVerifyLine(t *testing.T) {
	tests := []struct {
		name     string
		was, now string
		present  bool
		want     string
		wantKind outcomeKind
	}{
		{
			name: "version moved", was: "10.2.0", now: "10.3.0", present: true,
			want: "✓ fd  v10.2.0 → v10.3.0", wantKind: outcomeOk,
		},
		{
			name: "version did not move", was: "10.2.0", now: "10.2.0", present: true,
			want: "⚠ fd  still v10.2.0", wantKind: outcomeWarn,
		},
		{
			name: "nothing on PATH afterwards", was: "10.2.0", now: "", present: false,
			want: "✕ fd  not on PATH", wantKind: outcomeBad,
		},
		{
			name: "installed but will not name a version", was: "10.2.0", now: "", present: true,
			want: "✓ fd  installed", wantKind: outcomeOk,
		},
		{
			name: "nothing was installed before", was: "", now: "10.3.0", present: true,
			want: "✓ fd  v10.3.0", wantKind: outcomeOk,
		},
		{
			name: "a v-prefixed probe is not double-prefixed", was: "v10.2.0", now: "v10.3.0", present: true,
			want: "✓ fd  v10.2.0 → v10.3.0", wantKind: outcomeOk,
		},
		{
			name: "a non-semver version passes through", was: "nightly", now: "cli-2.0", present: true,
			want: "✓ fd  nightly → cli-2.0", wantKind: outcomeOk,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, kind := updateVerifyLine("fd", tt.was, tt.now, tt.present)
			if got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if kind != tt.wantKind {
				t.Errorf("kind = %v, want %v", kind, tt.wantKind)
			}
		})
	}
}

// TestMarkdownToLinesFenceKind asserts the kind, which mdDump deliberately
// collapses (it tags every non-heading "B"). The kind is what paints the card's
// Surface plate, so a regression emitting fenced lines as mdBody with the indent
// still on them would read identically in the table above and go green.
func TestMarkdownToLinesFenceKind(t *testing.T) {
	// A ~~~ block wrapping ``` samples: everything between the outer pair is
	// code, the tail after the real closer is body.
	got := markdownToLines("~~~\n```\nsample\n```\n~~~\ntail", 80)
	want := []mdLineKind{mdCode, mdCode, mdCode, mdBody}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(got), len(want), mdDump(got))
	}
	for i, k := range want {
		if got[i].kind != k {
			t.Errorf("line %d (%q) kind = %d, want %d", i, got[i].text, got[i].kind, k)
		}
	}
}
