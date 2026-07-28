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
		idx, err := strconv.Atoi(strings.Trim(m, rcSpanMark))
		if err != nil || idx < 0 || idx >= len(spans) {
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
	// written, the honest degradation.
	rcHTMLTagRe = regexp.MustCompile(`(?is)</?(?:` + strings.Join(rcHTMLNames, "|") + `)\b(?:\s[^<>]*)?/?>`)
)

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
		res = append(res, regexp.MustCompile(`(?is)<`+n+`\b[^<>]*>.*?</`+n+`\s*>`))
	}
	return res
}

// Line-level patterns. Inline images and links reuse textutil.go's mdImageRe /
// mdLinkRe — the same grammar fact, a different consumer.
var (
	// A link-reference definition line: pure metadata, and after image removal
	// and link unwrapping nothing points at it any more.
	rcLinkDefRe = regexp.MustCompile(`^[ \t]{0,3}\[([^\]]+)\]:[ \t]*\S`)

	rcRefImageRe = regexp.MustCompile(`!\[([^\]]*)\]\[([^\]]*)\]`)
	rcRefLinkRe  = regexp.MustCompile(`\[([^\]]*)\]\[([^\]]*)\]`)

	// The shortcut form, [label] with the definition supplying the URL. It runs
	// last, when every bracketed construct with a destination is already gone,
	// and only for labels a definition actually declared — otherwise a task-list
	// "[x]" or a prose "[experimental]" would lose its brackets.
	rcShortcutRe = regexp.MustCompile(`(!?)\[([^\]]*)\]`)

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
func cleanReadmeMarkdown(s string) string {
	if s == "" {
		return ""
	}
	// The mask sentinel is NUL and production input has none, but dropping any
	// here keeps a direct caller from smuggling a placeholder into the restore
	// table.
	s = strings.ReplaceAll(s, rcSpanMark, "")

	segs := rcSegments(s)
	labels := rcDefinedLabels(segs)
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		if seg.protected {
			out = append(out, seg.text)
			continue
		}
		out = append(out, rcCleanSegment(seg.text, labels))
	}
	return strings.Join(out, "\n")
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
		for _, line := range strings.Split(seg.text, "\n") {
			if m := rcLinkDefRe.FindStringSubmatch(line); m != nil {
				labels[rcNormalizeLabel(m[1])] = true
			}
		}
	}
	return labels
}

// rcNormalizeLabel folds a reference label the way CommonMark matches them:
// case-insensitively, with internal whitespace collapsed.
func rcNormalizeLabel(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// rcCleanSegment applies every removal rule to one cleanable segment, with
// inline code spans masked out for the duration.
func rcCleanSegment(s string, labels map[string]bool) string {
	masked, spans := rcMaskSpans(s)
	masked = rcStripHTML(masked)
	masked = rcCleanLines(masked, labels)
	masked = rcCollapseBlankRuns(masked)
	return rcUnmaskSpans(masked, spans)
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
// the tidy-up and the drop are gated on the line having actually changed, so an
// untouched line keeps its trailing spaces (a hard line break) and a "- - -"
// thematic break is never mistaken for an emptied bullet.
func rcCleanLines(s string, labels map[string]bool) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if rcLinkDefRe.MatchString(line) {
			continue
		}
		cleaned := rcCleanLine(line, labels)
		if cleaned == line {
			out = append(out, line)
			continue
		}
		cleaned = rcTidyLine(line, cleaned)
		if rcLineContent(cleaned) == "" {
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
	line = rcRefImageRe.ReplaceAllString(line, "")
	line = mdLinkRe.ReplaceAllString(line, "$1")
	line = rcRefLinkRe.ReplaceAllString(line, "$1")
	line = rcUnwrapShortcuts(line, labels)
	line = rcStripEmoji(line)
	return rcStripShortcodes(line)
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
// an indented code block.
func rcTidyLine(orig, cleaned string) string {
	indent := orig[:len(orig)-len(strings.TrimLeft(orig, " \t"))]
	body := strings.TrimSpace(cleaned)
	return indent + rcInnerSpacesRe.ReplaceAllString(body, " ")
}

// rcLineContent strips leading block markers so the caller can ask whether a
// line still says anything. "## 🚀" cleans to "##", which is a heading with no
// content — glamour would render it as a styled blank row.
func rcLineContent(line string) string {
	for {
		next := rcBlockMarkerRe.ReplaceAllString(line, "")
		if next == line {
			break
		}
		line = next
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
