package model

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark-emoji/definition"
)

// README preprocessing — the cleanup pass that runs between cleanTerminalOutput
// and glamour so panel [3] shows one calm house style instead of whatever
// badge/HTML/emoji noise the upstream repo happens to ship.
//
// The pass rests on one inviolable rule: code is never rewritten. A fenced
// block and an inline span are exactly how a README *shows* the markup these
// rules delete, so segmentation runs first and the removal rules only ever see
// the text between the protected parts.

// rcSegment is one run of consecutive lines, tagged with whether the removal
// rules may rewrite it. Segments are line runs, so joining every segment's text
// with "\n" reproduces the input exactly.
type rcSegment struct {
	text      string
	protected bool
}

var (
	// A fenced-code opener: 3+ backticks or 3+ tildes, an info string allowed
	// after them. The indent is deliberately unconstrained, like mdFenceRe's —
	// 4+ spaces would mean an indented code block, which this pass does not
	// implement, so CommonMark's 3-space limit would only mis-read a fence
	// nested under a list item.
	rcFenceOpenRe = regexp.MustCompile("^[ \t]*(`{3,}|~{3,})")
	// A closer carries the same marker character, a run at least as long as the
	// opener's, and nothing but whitespace after it — which is what keeps a
	// ```` fence wrapping ``` examples (how a README documents markdown itself)
	// one single block.
	rcFenceCloseRe = regexp.MustCompile("^[ \t]*(`{3,}|~{3,})[ \t]*$")
)

// rcFenceCloses reports whether line closes a fence opened with marker.
func rcFenceCloses(line, marker string) bool {
	m := rcFenceCloseRe.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	run := m[1]
	return run[0] == marker[0] && len(run) >= len(marker)
}

// rcSegments splits raw README markdown into protected fenced blocks and the
// cleanable runs between them. An unterminated fence protects everything to
// EOF: version.getReadme truncates at readmeMaxBytes and the cut can land
// mid-fence, where half a code sample is still code.
func rcSegments(s string) []rcSegment {
	lines := strings.Split(s, "\n")
	var (
		segs      []rcSegment
		cleanable []string
	)
	flush := func() {
		if len(cleanable) > 0 {
			segs = append(segs, rcSegment{text: strings.Join(cleanable, "\n")})
			cleanable = nil
		}
	}
	for i := 0; i < len(lines); {
		open := rcFenceOpenRe.FindStringSubmatch(lines[i])
		if open == nil {
			cleanable = append(cleanable, lines[i])
			i++
			continue
		}
		flush()
		block := []string{lines[i]}
		i++
		for i < len(lines) {
			line := lines[i]
			block = append(block, line)
			i++
			if rcFenceCloses(line, open[1]) {
				break
			}
		}
		segs = append(segs, rcSegment{text: strings.Join(block, "\n"), protected: true})
	}
	flush()
	return segs
}

// rcSpanMark brackets a masked inline code span. NUL is the sentinel because
// cleanTerminalOutput — which every production caller runs first — drops every
// control character, so no README text can collide with it.
const rcSpanMark = "\x00"

var rcSpanMarkRe = regexp.MustCompile("\x00(\\d+)\x00")

// rcMaskSpans replaces every inline code span with an opaque placeholder so the
// removal rules cannot rewrite what a README meant to show verbatim. The second
// result is the restore table for rcUnmaskSpans.
//
// Known limit: two spans separated by nothing but a removed construct come back
// adjacent (`x`![i](u)`y` → `x“y`), which CommonMark then reads as one span.
// It needs zero whitespace on either side of the construct, so no real prose
// hits it; fixing it means inventing a separator the author did not write.
func rcMaskSpans(s string) (string, []string) {
	if !strings.Contains(s, "`") {
		return s, nil
	}
	var (
		b     strings.Builder
		spans []string
	)
	for i := 0; i < len(s); {
		if s[i] != '`' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		if end := rcSpanEnd(s, j, j-i); end > 0 {
			spans = append(spans, s[i:end])
			b.WriteString(rcSpanMark + strconv.Itoa(len(spans)-1) + rcSpanMark)
			i = end
			continue
		}
		// No closing run of the same length: literal backticks, not a span.
		b.WriteString(s[i:j])
		i = j
	}
	return b.String(), spans
}

// rcSpanEnd returns the index just past the backtick run that closes a span
// opened with run backticks at from, or 0 when the span is never closed. The
// search stops at a blank line, mirroring CommonMark's paragraph boundary:
// letting one stray backtick pair with another three sections later would
// exempt half a README from cleaning.
func rcSpanEnd(s string, from, run int) int {
	for i := from; i < len(s); {
		switch s[i] {
		case '\n':
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			if j >= len(s) || s[j] == '\n' {
				return 0
			}
			i = j
		case '`':
			j := i
			for j < len(s) && s[j] == '`' {
				j++
			}
			if j-i == run {
				return j
			}
			i = j
		default:
			i++
		}
	}
	return 0
}

// rcUnmaskSpans puts every masked code span back where its placeholder sits.
func rcUnmaskSpans(s string, spans []string) string {
	if len(spans) == 0 {
		return s
	}
	return rcSpanMarkRe.ReplaceAllStringFunc(s, func(m string) string {
		// The index was written by rcMaskSpans and the sentinel is stripped from
		// the input, so neither branch should ever fire; they are here because a
		// broken invariant must degrade to a lost span, not panic mid-render.
		idx, err := strconv.Atoi(strings.Trim(m, rcSpanMark))
		if err != nil || idx >= len(spans) {
			return ""
		}
		return spans[idx]
	})
}

// HTML rules that need the whole segment: a comment and a <picture> body
// routinely span several lines, which a per-line pass could never see.
var (
	rcHTMLCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

	// Elements dropped with their bodies — a <picture> is a badge/logo wrapper
	// and an <svg> is the logo itself, neither of which a TTY can show. RE2 has
	// no backreference, so each name gets its own pattern instead of one
	// \1-closed alternation. <img> needs no entry here: it is void, so the tag
	// strip below already removes the whole element.
	rcHTMLBodyElemRes = rcCompileBodyElems("picture", "video", "audio", "svg", "script", "style")

	// Every other tag is stripped keeping its inner text (<kbd>Ctrl</kbd> →
	// Ctrl, a <details>/<summary> block keeps its content). The name is matched
	// against a fixed list rather than a generic identifier shape, which is
	// what lets autolinks (<https://…>, <user@host>) and prose angle brackets
	// (Vec<String>, a < b) through untouched — an unknown name is left as
	// written, the honest degradation. The trailing \b on the name is what makes
	// the alternation order-independent: Go's regexp picks the leftmost-FIRST
	// branch, so without it "a" would claim "<abbr>" and leave "br>" behind.
	rcHTMLTagRe = regexp.MustCompile(`(?is)</?(?:` + strings.Join(rcHTMLNames, "|") + `)\b` + rcTagAttrs + `/?>`)
)

// rcTagAttrs matches the attribute part of an HTML tag. Quoted values are
// matched whole, because a flat [^<>]* ends the tag at the first ">" it sees —
// including one inside a value, which left `<img alt="a > b" src="x.png">`
// rendering as ` b" src="x.png">`.
const rcTagAttrs = `(?:\s(?:"[^"]*"|'[^']*'|[^<>"'])*)?`

// rcHTMLNames is the tag allowlist for rcHTMLTagRe: the elements READMEs
// actually use. The bodied names appear here too, so a stray closing tag left
// by an unbalanced <picture> is still cleaned up.
var rcHTMLNames = []string{
	"a", "abbr", "address", "article", "aside", "audio", "b", "blockquote", "br",
	"caption", "center", "cite", "code", "col", "colgroup", "dd", "del", "details",
	"dfn", "div", "dl", "dt", "em", "embed", "figcaption", "figure", "font",
	"footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hgroup", "hr", "i",
	"iframe", "img", "ins", "kbd", "li", "main", "mark", "nav", "nobr", "noscript",
	"object", "ol", "p", "param", "picture", "pre", "q", "s", "samp", "script",
	"section", "small", "source", "span", "strike", "strong", "style", "sub",
	"summary", "sup", "svg", "table", "tbody", "td", "tfoot", "th", "thead",
	"time", "tr", "track", "tt", "u", "ul", "var", "video", "wbr",
}

// rcCompileBodyElems builds the open-tag-to-close-tag pattern for each name.
func rcCompileBodyElems(names ...string) []*regexp.Regexp {
	res := make([]*regexp.Regexp, 0, len(names))
	for _, n := range names {
		res = append(res, regexp.MustCompile(`(?is)<`+n+`\b`+rcTagAttrs+`>.*?</`+n+`\s*>`))
	}
	return res
}

// Line-level patterns. Inline images and links reuse textutil.go's mdImageRe /
// mdLinkRe — the same grammar fact, a different consumer.
var (
	// A link-reference definition line: pure metadata, and after image removal
	// and link unwrapping nothing points at it any more. Deleting a line of
	// someone's README is the most destructive thing this pass does, so the
	// shape is strict — a single-token destination (or an <angle> one) and at
	// most a quoted title, nothing else. A looser "[label]: <non-space>" ate
	// "[1]: first item explained", an ordinary line of prose.
	rcLinkDefRe = regexp.MustCompile(`^[ \t]{0,3}\[([^\]]+)\]:[ \t]*(?:<[^<>]*>|\S+)(?:[ \t]+(?:"[^"]*"|'[^']*'|\([^()]*\)))?[ \t]*$`)

	// The reference forms, [text][label] and ![alt][label], plus their collapsed
	// [text][] variants whose label is the text. Gated on a declared label for
	// the same reason the shortcut form below is: ungated, prose carrying two
	// bracket pairs in a row — "the value arr[i][j] is used" — came out as
	// "the value arri is used".
	rcRefRe = regexp.MustCompile(`(!?)\[([^\]]*)\]\[([^\]]*)\]`)

	// The shortcut form, [label] with the definition supplying the URL. It runs
	// last, when every bracketed construct with a destination is already gone,
	// and only for labels a definition actually declared — otherwise a task-list
	// "[x]" or a prose "[experimental]" would lose its brackets.
	rcShortcutRe = regexp.MustCompile(`(!?)\[([^\]]*)\]`)

	// A setext underline, kept for one job only: dropping the one left orphaned
	// when removal emptied the title above it. Only the "=" form — a "-" run is
	// equally a thematic break, and leaving it renders a rule either way.
	rcSetextRe = regexp.MustCompile(`^[ \t]{0,3}=+[ \t]*$`)

	// A leading block marker, stripped only to ask "did removal leave this line
	// with nothing to say?". The heading and list forms require whitespace (or
	// end of line) after the marker, so a "#hashtag" keeps its text and a "---"
	// rule is never mistaken for a bullet.
	rcBlockMarkerRe = regexp.MustCompile(`^[ \t]*(?:#{1,6}(?:[ \t]+|$)|>[ \t]?|[-*+](?:[ \t]+|$)|\d{1,9}[.)](?:[ \t]+|$))`)

	rcInnerSpacesRe = regexp.MustCompile(`  +`)

	// A GitHub emoji shortcode. The name class is deliberately wide enough for
	// ":+1:" and ":e-mail:"; what decides a match is the dictionary lookup, not
	// the shape — ":30:" inside "12:30:45" matches this pattern and survives
	// because no emoji is named "30".
	rcShortcodeRe = regexp.MustCompile(`:([a-zA-Z0-9_+-]+):`)
)

// rcShortcodes is GitHub's shortcode dictionary — the same one goldmark-emoji
// gives glamour, so keepkit and the renderer agree on what a shortcode is. The
// package builds it under a sync.Once and only reads it afterwards.
var rcShortcodes = definition.Github()

// rcEmojiTranslations keep a feature table readable after the pictographs are
// gone: a ✅/❌ column carries the meaning of the row, so those two (and the
// ballot-box tick) become the panel's own plain marks instead of vanishing.
var rcEmojiTranslations = strings.NewReplacer(
	"✅", "✓",
	"❌", "✗",
	"☑", "✓",
)

// rcStripEmoji drops the pictographic runes a TTY renders as tofu or as a
// double-width smear, plus the joiners that would otherwise be left stranded:
// the variation selector (U+FE0F), the zero-width joiner and the combining
// enclosing keycap that turns "1" into 1️⃣. BMP symbols a terminal font does
// carry — ✓ ★ → — are deliberately kept.
func rcStripEmoji(s string) string {
	s = rcEmojiTranslations.Replace(s)
	if !strings.ContainsFunc(s, rcIsEmojiRune) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if rcIsEmojiRune(r) {
			return -1
		}
		return r
	}, s)
}

func rcIsEmojiRune(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF:
		return true
	case r == 0xFE0F, r == 0x200D, r == 0x20E3:
		return true
	}
	return false
}

// rcStripShortcodes removes ":name:" for names GitHub actually defines, so an
// unknown ":foo:" and a clock reading stay as written.
func rcStripShortcodes(s string) string {
	if !strings.Contains(s, ":") {
		return s
	}
	return rcShortcodeRe.ReplaceAllStringFunc(s, func(m string) string {
		if _, ok := rcShortcodes.Get(strings.Trim(m, ":")); ok {
			return ""
		}
		return m
	})
}

// cleanReadmeMarkdown is the whole preprocessing pass: segment, then rewrite
// only the cleanable segments. It is pure — every rule is a string rewrite — so
// the tables test it directly.
func cleanReadmeMarkdown(s, about string) string {
	if s == "" {
		return ""
	}
	// The mask sentinel is NUL and production input has none, but dropping any
	// here keeps a direct caller from smuggling a placeholder into the restore
	// table.
	s = strings.ReplaceAll(s, rcSpanMark, "")
	// CRLF is normalized for markdownToLines' reason, and this pass needs it
	// just as badly: a "```\r" closing fence never matches rcFenceCloseRe, so
	// the first fence protects everything to EOF and nothing is cleaned at all.
	// Production is safe (cleanTerminalOutput drops \r first) but every test
	// calls this function directly, which is exactly the path that broke.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	segs := rcSegments(s)

	// Two phases, because the label set has to be right before a single line is
	// rewritten. Masking and the segment-wide HTML rules run first — collecting
	// labels ahead of them let a "[x]: url" commented out with <!-- --> gate the
	// unwrapping, and eat the task-list "[x]" the gate exists to protect.
	spans := make([][]string, len(segs))
	for i, seg := range segs {
		if seg.protected {
			continue
		}
		masked, sp := rcMaskSpans(seg.text)
		segs[i].text = rcStripHTML(masked)
		spans[i] = sp
	}

	labels := rcDefinedLabels(segs)
	out := make([]string, len(segs))
	for i, seg := range segs {
		if seg.protected {
			out[i] = seg.text
			continue
		}
		out[i] = rcUnmaskSpans(rcCollapseBlankRuns(rcCleanLines(seg.text, labels)), spans[i])
	}
	return rcDropTitleBlock(strings.Join(out, "\n"), about)
}

// rcDropTitleBlock removes a README's leading H1 and, when it merely repeats
// the repo description the card already prints, the slogan under it. Both are
// on screen a panel away: the card shows the tool's name as the largest thing
// in the app with the description right below it, so in the readme they are a
// title page the reader scrolls past to reach the first sentence that says
// anything new.
//
// Only the *leading* heading goes — a later "# Install" is content.
//
// The slogan is matched against `about` rather than guessed at, and that is the
// whole point: dropping a paragraph is the most destructive thing this pass can
// do, and every shape-based rule for "this looks like a tagline" also matches a
// short opening sentence. Equality with a string the app already displays is
// something the reader can verify at a glance; a heuristic is not. With no
// description to compare against, nothing below the heading is touched.
func rcDropTitleBlock(s, about string) string {
	lines := strings.Split(s, "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || !strings.HasPrefix(lines[i], "# ") {
		return s
	}
	i++
	if key := rcTaglineKey(about); key != "" {
		// A slogan sits directly under the title, separated by blank lines only,
		// and is a paragraph of its own — a second line makes it prose no matter
		// how the first one reads.
		blank := i
		for blank < len(lines) && strings.TrimSpace(lines[blank]) == "" {
			blank++
		}
		if blank < len(lines) && rcTaglineKey(lines[blank]) == key &&
			(blank+1 >= len(lines) || strings.TrimSpace(lines[blank+1]) == "") {
			i = blank + 1
		}
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	return strings.Join(lines[i:], "\n")
}

// rcTaglineKey normalizes a line for the slogan comparison: a README writes the
// repo's own description with its own emphasis and punctuation, so the match
// looks past markdown markers, case, spacing and a trailing full stop. It
// answers "" for anything that cannot be a slogan at all.
func rcTaglineKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "*_`>#")
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ".!")
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	return s
}

// rcDefinedLabels collects every declared link-reference label, document-wide:
// definitions collect at the bottom of a README while their shortcut uses sit
// at the top, and a fence between the two puts them in different segments.
func rcDefinedLabels(segs []rcSegment) map[string]bool {
	labels := make(map[string]bool)
	for _, seg := range segs {
		if seg.protected {
			continue
		}
		lines := strings.Split(seg.text, "\n")
		for i, isDef := range rcDefinitionLines(lines) {
			if !isDef {
				continue
			}
			if m := rcLinkDefRe.FindStringSubmatch(lines[i]); m != nil {
				labels[rcNormalizeLabel(m[1])] = true
			}
		}
	}
	return labels
}

// rcDefinitionLines marks which lines are link-reference definitions. Matching
// the shape is not enough: CommonMark forbids a definition from interrupting a
// paragraph, so a line only counts when what sits above it is the start of the
// segment, a blank line, or another definition. Without that rule a paragraph
// reading "Some prose here / [note]: this matters / more prose" silently lost
// its middle line.
func rcDefinitionLines(lines []string) []bool {
	defs := make([]bool, len(lines))
	inParagraph := false
	for i, line := range lines {
		switch {
		case strings.TrimSpace(line) == "":
			inParagraph = false
		case !inParagraph && rcLinkDefRe.MatchString(line):
			defs[i] = true
		default:
			inParagraph = true
		}
	}
	return defs
}

// rcNormalizeLabel folds a reference label the way CommonMark matches them:
// case-insensitively, with internal whitespace collapsed.
func rcNormalizeLabel(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// rcStripHTML runs the segment-wide HTML rules. Comments go first: a script or
// style body may well contain "-->" and would otherwise cut a comment short.
func rcStripHTML(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	s = rcHTMLCommentRe.ReplaceAllString(s, "")
	for _, re := range rcHTMLBodyElemRes {
		s = re.ReplaceAllString(s, "")
	}
	return rcHTMLTagRe.ReplaceAllString(s, "")
}

// rcCleanLines applies the per-line rules and drops what removal emptied. Both
// the tidy-up and the drop are gated on the line having actually changed, so a
// "- - -" thematic break is never mistaken for an emptied bullet.
func rcCleanLines(s string, labels map[string]bool) string {
	lines := strings.Split(s, "\n")
	defs := rcDefinitionLines(lines)
	out := make([]string, 0, len(lines))
	// A setext title is two lines, and removal only ever empties the first;
	// without this the underline outlives it as a visible row of equals signs.
	dropUnderline := false
	for i, line := range lines {
		if dropUnderline {
			dropUnderline = false
			if rcSetextRe.MatchString(line) {
				continue
			}
		}
		if defs[i] {
			continue
		}
		cleaned := rcCleanLine(line, labels)
		if cleaned == line {
			out = append(out, line)
			continue
		}
		cleaned = rcTidyLine(line, cleaned)
		if rcLineContent(cleaned) == "" {
			dropUnderline = true
			continue
		}
		out = append(out, cleaned)
	}
	return strings.Join(out, "\n")
}

// rcCleanLine removes images, unwraps links and strips emoji. Order is
// load-bearing twice: an image goes before the link that may wrap it, so a
// linked badge "[![alt](img)](target)" collapses to nothing instead of leaving
// a stray "!"; and the emoji pass runs after the unwrapping, so a "[🚀 Go](url)"
// link is reduced to its text first and the pictograph is then found there.
func rcCleanLine(line string, labels map[string]bool) string {
	line = mdImageRe.ReplaceAllString(line, "")
	line = mdLinkRe.ReplaceAllString(line, "$1")
	line = rcUnwrapRefs(line, labels)
	line = rcUnwrapShortcuts(line, labels)
	line = rcStripEmoji(line)
	return rcStripShortcodes(line)
}

// rcUnwrapRefs resolves [text][label] and ![alt][label] — and the collapsed
// [text][] form, whose label is its own text — for declared labels only. An
// undeclared one is left as written, which is also what a browser shows.
func rcUnwrapRefs(s string, labels map[string]bool) string {
	if len(labels) == 0 || !strings.Contains(s, "][") {
		return s
	}
	return rcRefRe.ReplaceAllStringFunc(s, func(m string) string {
		g := rcRefRe.FindStringSubmatch(m)
		label := g[3]
		if strings.TrimSpace(label) == "" {
			label = g[2]
		}
		if !labels[rcNormalizeLabel(label)] {
			return m
		}
		if g[1] == "!" {
			return ""
		}
		return g[2]
	})
}

// rcUnwrapShortcuts resolves the [label] form for declared labels only.
func rcUnwrapShortcuts(s string, labels map[string]bool) string {
	if len(labels) == 0 || !strings.Contains(s, "[") {
		return s
	}
	return rcShortcutRe.ReplaceAllStringFunc(s, func(m string) string {
		g := rcShortcutRe.FindStringSubmatch(m)
		if !labels[rcNormalizeLabel(g[2])] {
			return m
		}
		if g[1] == "!" {
			return ""
		}
		return g[2]
	})
}

// rcTidyLine repairs the spacing a removal left behind. The indent comes from
// the ORIGINAL line, not the cleaned one: list nesting depends on it, while a
// leading "🚀 " that removal turned into a space would otherwise become an
// indent the author never wrote — four of them and markdown reads the line as
// an indented code block. A hard line break is carried over for the same
// reason: two trailing spaces are markup, and removal inside the line has no
// business retiring them.
func rcTidyLine(orig, cleaned string) string {
	indent := orig[:len(orig)-len(strings.TrimLeft(orig, " \t"))]
	body := strings.TrimSpace(cleaned)
	if body == "" {
		return indent
	}
	body = rcInnerSpacesRe.ReplaceAllString(body, " ")
	if strings.HasSuffix(orig, "  ") {
		body += "  "
	}
	return indent + body
}

// rcLineContent strips leading block markers so the caller can ask whether a
// line still says anything. "## 🚀" cleans to "##", which is a heading with no
// content — glamour would render it as a styled blank row.
//
// The loop slices rather than calling ReplaceAllString, and that is not a
// micro-optimization: the pattern is ^-anchored, so each replace could only
// ever rewrite the head, yet it copied the whole remaining line every pass.
// One line of "🚀 " + 262144 "- " markers — a 512 KiB README, exactly the cap
// version.getReadme enforces — took 8.4 s of a synchronous Update(), i.e. the
// whole TUI frozen on untrusted remote input. Slicing makes it linear.
func rcLineContent(line string) string {
	for {
		loc := rcBlockMarkerRe.FindStringIndex(line)
		if loc == nil {
			break
		}
		line = line[loc[1]:]
	}
	return strings.TrimSpace(line)
}

// rcCollapseBlankRuns folds consecutive blank lines into one. A badge header
// wrapped in <p align="center"> leaves a blank line per removed element; in
// markdown one blank line and five mean the same break, so the fold is free.
func rcCollapseBlankRuns(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
