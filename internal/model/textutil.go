package model

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/stanlyzoolo/keepkit/internal/ui"
)

// formatStars formats a star count with K suffix for thousands.
func formatStars(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// languagePercent holds a language name and its percentage share.
type languagePercent struct {
	Name string
	Pct  float64
}

// languagePercents converts raw byte counts to sorted percentage slice (top 5).
func languagePercents(langs map[string]int) []languagePercent {
	if len(langs) == 0 {
		return nil
	}
	total := 0
	for _, v := range langs {
		total += v
	}
	if total == 0 {
		return nil
	}
	out := make([]languagePercent, 0, len(langs))
	for name, bytes := range langs {
		out = append(out, languagePercent{Name: name, Pct: float64(bytes) / float64(total) * 100})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pct > out[j].Pct })
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// renderLangBar renders a horizontal language bar with percentages, wrapping by
// words at width. firstLineUsed is the column budget already consumed on the
// first line (e.g. by an inline "languages: " label) so wrapping lines up.
func renderLangBar(langs map[string]int, width, firstLineUsed int) string {
	percents := languagePercents(langs)
	if len(percents) == 0 {
		return ""
	}
	// Language names lowercase in the normal note color; percentages dimmed.
	pctStyle := lipgloss.NewStyle().Foreground(ui.ColorDim)

	var lines []string
	var cur strings.Builder
	curW := firstLineUsed
	for _, lp := range percents {
		name := strings.ToLower(lp.Name)
		pct := fmt.Sprintf("%.0f%%", lp.Pct)
		tokenW := utf8.RuneCountInString(name) + 1 + utf8.RuneCountInString(pct)

		sep := 0
		if cur.Len() > 0 {
			sep = 2
		}
		// Wrap to a new line only when the token would overflow the width.
		if curW+sep+tokenW > width && cur.Len() > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
			curW = 0
			sep = 0
		}
		if sep > 0 {
			cur.WriteString("  ")
		}
		cur.WriteString(ui.InfoStyle.Render(name) + " " + pctStyle.Render(pct))
		curW += sep + tokenW
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return strings.Join(lines, "\n")
}

func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	var result strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			result.WriteByte('\n')
		}
		for k, wrapped := range wrapLine(line, width) {
			if k > 0 {
				result.WriteByte('\n')
			}
			result.WriteString(wrapped)
		}
	}
	return result.String()
}

// wrapLine wraps a single line by words at width. Extracted from wrapText so
// parseHelpEntries can count how many display lines a source line produces
// without re-implementing the wrap rules — the entry index and the rendered
// content must agree on line positions, so they must share this code.
// Note a wrapped line is rebuilt from strings.Fields: leading indentation is
// lost and continuation pieces start at column 0.
func wrapLine(line string, width int) []string {
	if utf8.RuneCountInString(line) <= width {
		return []string{line}
	}
	var lines []string
	var cur strings.Builder
	col := 0
	for j, word := range strings.Fields(line) {
		wl := utf8.RuneCountInString(word)
		switch {
		case j == 0:
			cur.WriteString(word)
			col = wl
		case col+1+wl > width:
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(word)
			col = wl
		default:
			cur.WriteByte(' ')
			cur.WriteString(word)
			col += 1 + wl
		}
	}
	return append(lines, cur.String())
}

// Inline markdown patterns, compiled once — mdInline runs per source line of
// every release body the card renders.
//
// The link patterns keep their text class free of the closing delimiter
// ([^\]]* / [^)]*), which is what makes them non-greedy without the RE2 cost
// of an explicit lazy quantifier: two links on one line stay two matches.
// The emphasis patterns require a non-space rune on both inner edges (the
// CommonMark flanking rule in miniature), so "2 * 3 * 4" keeps its asterisks;
// the underscore pair additionally requires a non-word rune outside both
// delimiters, or an identifier like update_cmd would lose its underscore.
var (
	mdImageRe    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLinkRe     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdAutolinkRe = regexp.MustCompile(`<((?:https?|ftp|mailto):[^>\s]+)>`)
	mdHTMLTagRe  = regexp.MustCompile(`<[^<>]*>`)

	mdStrongStarRe  = regexp.MustCompile(`\*\*([^\s*](?:[^*]*[^\s*])?)\*\*`)
	mdStrongUnderRe = regexp.MustCompile(`__([^\s_](?:[^_]*[^\s_])?)__`)
	mdEmStarRe      = regexp.MustCompile(`\*([^\s*](?:[^*]*[^\s*])?)\*`)
	mdEmUnderRe     = regexp.MustCompile(`(^|[^\p{L}\p{N}_])_([^\s_](?:[^_]*[^\s_])?)_([^\p{L}\p{N}_]|$)`)
)

// mdInline reduces one line of markdown to plain text. Order is load-bearing:
// images before links (otherwise the link pattern eats "[alt](url)" out of an
// image and leaves a stray '!'), autolinks before the HTML-tag strip (which
// would otherwise swallow "<https://…>" whole — the bug this replaces), and
// emphasis last, after every construct whose delimiters could contain '*'/'_'.
// A malformed construct (unclosed '[', a lone '<') simply matches nothing and
// is left as written.
func mdInline(s string) string {
	s = mdImageRe.ReplaceAllString(s, "$1")
	s = mdLinkRe.ReplaceAllString(s, "$1")
	s = mdAutolinkRe.ReplaceAllString(s, "$1")
	s = mdHTMLTagRe.ReplaceAllString(s, "")
	s = mdStrongStarRe.ReplaceAllString(s, "$1")
	s = mdStrongUnderRe.ReplaceAllString(s, "$1")
	s = mdEmStarRe.ReplaceAllString(s, "$1")
	// The underscore pattern is the one that matches its own boundaries, and a
	// match consumes them: in "_a_ _b_" the separating space goes with the
	// first span, leaving the second with no boundary to match against (RE2 has
	// no lookaround). A second pass reaches it. Each pass either drops two
	// delimiters or changes nothing, so this terminates.
	for {
		next := mdEmUnderRe.ReplaceAllString(s, "${1}${2}${3}")
		if next == s {
			break
		}
		s = next
	}
	return strings.ReplaceAll(s, "`", "")
}

// mdLineKind tags a converted line for the styling the consumer applies to it.
// Two kinds only: the card's changelog block paints headings one step brighter
// and everything else muted, so a third tag (code, list, quote) would have no
// reader — what makes those blocks special lives inside the converter.
type mdLineKind int

const (
	mdBody    mdLineKind = iota // paragraphs, list items, code, quotes
	mdHeading                   // a #-heading line
)

// mdLine is one finished, already-wrapped output line. Wrapping happens inside
// the converter because hanging indents need the block structure, and because
// styling must land on whole finished lines — wrapLine counts runes and knows
// nothing about ANSI.
type mdLine struct {
	text string
	kind mdLineKind
}

// Block-level markdown patterns. Each allows CommonMark's up-to-3-space
// indent; the list patterns require whitespace after the marker, so a leading
// "#123" stays an issue reference and "**Breaking**" stays a paragraph.
var (
	mdFenceRe    = regexp.MustCompile("^ {0,3}(?:```|~~~)")
	mdHeadingRe  = regexp.MustCompile(`^ {0,3}#{1,6}[ \t]+(.*)$`)
	mdThematicRe = regexp.MustCompile(`^ {0,3}(?:-[ \t]*){3,}$|^ {0,3}(?:\*[ \t]*){3,}$|^ {0,3}(?:_[ \t]*){3,}$`)
	mdBulletRe   = regexp.MustCompile(`^([ \t]*)[-*+](?:[ \t]+(.*))?$`)
	mdOrderedRe  = regexp.MustCompile(`^([ \t]*)(\d{1,9})[.)](?:[ \t]+(.*))?$`)
	mdQuoteRe    = regexp.MustCompile(`^ {0,3}>[ \t]?`)
)

const (
	mdMaxListLevel = 2 // deeper nesting keeps folding into this indent
	mdIndentPerLvl = 2 // output spaces per nesting level
	mdCodeIndent   = "  "
)

// markdownToLines converts a markdown body (a GitHub release note) into plain
// pre-wrapped lines that keep the structure the old strip-everything pass
// destroyed: list markers, heading emphasis, code verbatim. It is a single
// line pass over the input carrying two state flags — inside a fenced code
// block, inside an HTML comment — and is pure, so the tables test it directly.
// width <= 0 disables wrapping (the call site floors it, but a caller must not
// have to).
func markdownToLines(s string, width int) []mdLine {
	if s == "" {
		return nil
	}
	// GitHub release bodies regularly arrive CRLF (the web form submits it).
	// The old code was accidentally saved by its per-line TrimSpace; here a
	// stray \r would ride the verbatim code path straight into the viewport,
	// and a \r-suffixed closing fence would never match, swallowing the rest
	// of the body as code.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var out []mdLine
	emit := func(text string, kind mdLineKind) {
		out = append(out, mdLine{text: text, kind: kind})
	}
	// emitBlank collapses runs of blank lines to one and drops leading ones;
	// trailing ones are cut after the pass.
	emitBlank := func() {
		if len(out) > 0 && out[len(out)-1].text != "" {
			out = append(out, mdLine{})
		}
	}

	inCode, inComment := false, false
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimRight(raw, " \t")

		if inCode {
			if mdFenceRe.MatchString(line) {
				inCode = false
				continue
			}
			switch {
			case line != "":
				emit(mdCodeIndent+line, mdBody)
			case len(out) > 0:
				// A blank line inside the block is verbatim too, so a sample
				// keeps its own spacing instead of going through the collapse
				// — but not as the very first output line, which would be a
				// leading blank row no one trims.
				emit("", mdBody)
			}
			continue
		}

		// Comments are cut before any classification (a release-note template
		// is mostly comments), but never inside code, where they are literal.
		line, inComment = mdCutComments(line, inComment)
		if strings.TrimSpace(line) == "" {
			emitBlank()
			continue
		}

		if mdFenceRe.MatchString(line) {
			inCode = true // the fence line, language tag and all, is dropped
			continue
		}

		// A blockquote marker is stripped and the remainder re-classified, so
		// a list inside a quote stays a list.
		for mdQuoteRe.MatchString(line) {
			line = mdQuoteRe.ReplaceAllString(line, "")
		}

		if m := mdHeadingRe.FindStringSubmatch(line); m != nil {
			mdEmitWrapped(emit, mdInline(m[1]), "", "", width, mdHeading)
			continue
		}
		// Checked before the list patterns: "---" is the stock separator above
		// "**Full Changelog**: …" and must never read as a bullet.
		if mdThematicRe.MatchString(line) {
			emitBlank()
			continue
		}
		if m := mdBulletRe.FindStringSubmatch(line); m != nil {
			mdEmitListItem(emit, emitBlank, m[1], "• ", m[2], width)
			continue
		}
		if m := mdOrderedRe.FindStringSubmatch(line); m != nil {
			mdEmitListItem(emit, emitBlank, m[1], m[2]+". ", m[3], width)
			continue
		}

		text := mdInline(strings.TrimSpace(line))
		if strings.TrimSpace(text) == "" {
			emitBlank()
			continue
		}
		mdEmitWrapped(emit, text, "", "", width, mdBody)
	}

	for len(out) > 0 && out[len(out)-1].text == "" {
		out = out[:len(out)-1]
	}
	return out
}

// mdEmitListItem writes one list item: the marker normalized (bullets to •,
// numbered items keeping their number), the nesting taken from the source
// indent, and the text wrapped with a hanging indent so continuations align
// under the first text column instead of under the marker.
func mdEmitListItem(emit func(string, mdLineKind), emitBlank func(), indent, marker, text string, width int) {
	body := mdInline(strings.TrimSpace(text))
	if strings.TrimSpace(body) == "" {
		emitBlank() // a marker with no content is not an item
		return
	}
	pad := strings.Repeat(" ", mdIndentPerLvl*mdListLevel(indent))
	mdEmitWrapped(emit, body, pad+marker, pad+strings.Repeat(" ", utf8.RuneCountInString(marker)), width, mdBody)
}

// mdListLevel maps a source indent to a nesting level: two spaces per level
// (floor), a tab counting as four, clamped so a deeply nested list cannot walk
// off a 30-column card.
func mdListLevel(indent string) int {
	cols := 0
	for _, r := range indent {
		if r == '\t' {
			cols += 4
		} else {
			cols++
		}
	}
	return min(cols/2, mdMaxListLevel)
}

// mdEmitWrapped wraps text at the remaining width and emits it with first as
// the first line's prefix and hang as every continuation's. Wrapping is
// skipped when nothing is left to wrap into — wrapLine would then degrade to
// one word per line, which is worse than letting the viewport truncate.
func mdEmitWrapped(emit func(string, mdLineKind), text, first, hang string, width int, kind mdLineKind) {
	avail := width - utf8.RuneCountInString(first)
	if width <= 0 || avail <= 0 {
		emit(first+text, kind)
		return
	}
	for i, w := range wrapLine(text, avail) {
		if i == 0 {
			emit(first+w, kind)
		} else {
			emit(hang+w, kind)
		}
	}
}

// mdCutComments removes HTML comments from one line, carrying the open state
// across lines (release-note templates comment out whole instruction blocks).
// It returns the surviving text and whether a comment is still open — an
// unclosed "<!--" runs to the end of the input.
func mdCutComments(line string, inComment bool) (string, bool) {
	var b strings.Builder
	for {
		if inComment {
			i := strings.Index(line, "-->")
			if i < 0 {
				return b.String(), true
			}
			line = line[i+len("-->"):]
			inComment = false
			continue
		}
		i := strings.Index(line, "<!--")
		if i < 0 {
			b.WriteString(line)
			return b.String(), false
		}
		b.WriteString(line[:i])
		line = line[i+len("<!--"):]
		inComment = true
	}
}

func stripANSI(s string) string {
	return ui.StripANSI(s)
}

// isTUITakeover reports whether captured probe output shows the tool started
// a full-screen TUI instead of printing help: entering or leaving the
// alternate screen (ESC[?1049h/l) is the one sequence real help text never
// contains. The check runs on the RAW capture — cleanTerminalOutput strips
// exactly this evidence.
func isTUITakeover(out []byte) bool {
	return bytes.Contains(out, []byte("\x1b[?1049"))
}

// cleanTerminalOutput strips ANSI escapes, carriage returns, and backspace
// overstrike (man pages render bold/underline as "x\bx"/"_\bx"). Leaving the
// backspaces in makes lipgloss miscount widths and overflow the panel.
// Remaining control characters (BEL, form feed, a lone ESC from a sequence
// cut mid-stream by the probe timeout, …) are dropped too — this text goes
// into a viewport verbatim, so anything non-printable reaches the terminal.
func cleanTerminalOutput(s string) string {
	s = stripANSI(s)
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r == '\r':
			// drop
		case r == '\b':
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		case r == '\n' || r == '\t':
			out = append(out, r)
		case r < 0x20 || r == 0x7f:
			// drop
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

// helpTokenRe matches every highlightable token (flag, <meta>, [meta]) in one
// alternation so colorizeHelp can style them in a single pass over the original
// line. Scanning once is essential: styled tokens embed ANSI escapes that
// contain '[', so a second regex pass (e.g. the bracket pattern) would match
// inside those escapes and corrupt them into visible "[38;2;…m" garbage.
//   - group 1: word boundary before a flag (line start, whitespace, or '(')
//   - group 2: the flag itself; a boundary is required so a dash inside a word
//     like "golangci-lint" is not mistaken for a short flag
//   - group 3: <angle> meta token
//   - group 4: [bracket] meta token
var helpTokenRe = regexp.MustCompile(`(^|[\s(])(--?[a-zA-Z][a-zA-Z0-9\-_]*)|(<[^>]+>)|(\[[^\]]+\])`)

// stylePrefix returns the raw ANSI prefix a lipgloss style emits, so base text
// color can be re-asserted after nested styled tokens reset it.
func stylePrefix(s lipgloss.Style) string {
	r := s.Render("\x00")
	if pre, _, ok := strings.Cut(r, "\x00"); ok {
		return pre
	}
	return ""
}

func colorizeHelp(s string) string {
	base := stylePrefix(ui.InfoStyle)
	const reset = "\x1b[0m"
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if isHelpSectionHeader(line) {
			lines[i] = ui.HelpSectionStyle.Render(line)
			continue
		}
		if line == "" {
			continue
		}
		// Single pass over the ORIGINAL line. Each styled token re-asserts the
		// base color after it so the rest of the line stays the unified content
		// color (matching the changelog body). We never re-scan styled output,
		// so the '[' inside injected escapes can't be mis-matched as meta.
		var b strings.Builder
		last := 0
		for _, m := range helpTokenRe.FindAllStringSubmatchIndex(line, -1) {
			b.WriteString(line[last:m[0]])
			switch {
			case m[4] >= 0: // flag: group 1 = boundary, group 2 = flag text
				b.WriteString(line[m[2]:m[3]])
				b.WriteString(ui.HelpFlagStyle.Render(line[m[4]:m[5]]))
			case m[6] >= 0: // <angle> meta
				b.WriteString(ui.HelpMetaStyle.Render(line[m[6]:m[7]]))
			case m[8] >= 0: // [bracket] meta
				b.WriteString(ui.HelpMetaStyle.Render(line[m[8]:m[9]]))
			}
			b.WriteString(base)
			last = m[1]
		}
		b.WriteString(line[last:])
		lines[i] = base + b.String() + reset
	}
	return strings.Join(lines, "\n")
}

// entryRange is a half-open [start, end) range of display (wrapped) lines
// holding one navigable help entry: a flag or subcommand line plus its
// indented description lines.
type entryRange struct {
	start, end int
}

// helpEntryFlagRe: the line's first non-space token is a flag — the flag core
// of helpTokenRe anchored at the trimmed start of the line.
var helpEntryFlagRe = regexp.MustCompile(`^\s*--?[a-zA-Z]`)

// helpEntrySubcmdRe: an indented subcommand row — a word not starting with a
// dash followed by 2+ spaces and description text (the shape of cobra/clap
// command blocks). Indentation is required so unindented prose ("Usage: keepkit
// [flags]", section text) never starts an entry; the word class has no ':' so
// inline labels like "Note:" don't match, and no '.' so justified man-page
// prose ("tree.  See also git-log(1)" — two spaces after a sentence period)
// doesn't read as a subcommand row.
var helpEntrySubcmdRe = regexp.MustCompile(`^\s+[A-Za-z][A-Za-z0-9_-]*\s{2,}\S`)

func isHelpEntryStart(line string) bool {
	return helpEntryFlagRe.MatchString(line) || helpEntrySubcmdRe.MatchString(line)
}

// isHelpSectionHeader is the single definition of a help section header — an
// unindented line ending in ':'. Both colorizeHelp (styling) and
// parseHelpEntries (entry boundaries) use it, so the two can't drift.
func isHelpSectionHeader(line string) bool {
	trimmed := strings.TrimRight(line, " ")
	return trimmed != "" && trimmed[0] != ' ' && trimmed[0] != '\t' && strings.HasSuffix(trimmed, ":")
}

// helpIndent returns the number of leading whitespace runes (tabs count as
// one — only relative depth matters).
func helpIndent(line string) int {
	n := 0
	for _, r := range line {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}

// parseHelpEntries derives the navigable entry index for the help panel from
// the raw (pre-wrap) help text. Detection runs on the SOURCE lines: wrapText
// drops leading indentation when it wraps a line (see wrapLine), which would
// defeat the indent-based continuation heuristic on wrapped output. The
// resulting ranges are then mapped onto wrapped-line indices via the same
// wrapLine wrapText uses, so an entryRange always addresses the lines the
// viewport actually shows.
//
// An entry starts at a flag line or an indented subcommand row and continues
// through lines indented deeper than its start line — including deeper lines
// that merely begin with a flag token ("…can be overridden with\n
// --no-ignore.") and blank lines separating paragraphs of one description —
// ending at a section header or the next line at the entry's own indent or
// shallower. Headers, Usage and free prose belong to no entry.
func parseHelpEntries(raw string, width int) []entryRange {
	if raw == "" {
		return nil
	}
	src := strings.Split(raw, "\n")
	// wrappedAt[i] = display index of the first wrapped line produced by
	// src[i]; wrappedAt[len(src)] = total display line count.
	wrappedAt := make([]int, len(src)+1)
	n := 0
	for i, line := range src {
		wrappedAt[i] = n
		if width > 0 {
			n += len(wrapLine(line, width))
		} else {
			n++
		}
	}
	wrappedAt[len(src)] = n

	var entries []entryRange
	for i := 0; i < len(src); {
		if !isHelpEntryStart(src[i]) {
			i++
			continue
		}
		indent := helpIndent(src[i])
		j := i + 1
		for j < len(src) {
			l := src[j]
			if strings.TrimSpace(l) == "" {
				// A blank line inside a multi-paragraph description does not
				// end the entry (man pages and clap v4 separate paragraphs of
				// one option with blank lines at the same deep indent): look
				// past the blank run — if the next non-blank line still
				// continues this entry, the paragraphs belong together.
				k := j
				for k < len(src) && strings.TrimSpace(src[k]) == "" {
					k++
				}
				if k < len(src) && continuesEntry(src[k], indent) {
					j = k + 1
					continue
				}
				break
			}
			if !continuesEntry(l, indent) {
				break
			}
			j++
		}
		entries = append(entries, entryRange{start: wrappedAt[i], end: wrappedAt[j]})
		i = j
	}
	return entries
}

// continuesEntry reports whether line belongs to the description block of an
// entry whose start line has the given indent: deeper-indented and not a
// section header. A deeper line that happens to begin with a flag token is a
// description continuation, not a new entry — only a line at the entry's own
// indent or shallower can start the next one.
func continuesEntry(line string, indent int) bool {
	return !isHelpSectionHeader(line) && helpIndent(line) > indent
}

func findMatches(text, query string) []int {
	if query == "" {
		return nil
	}
	lq := strings.ToLower(query)
	var matches []int
	for i, line := range strings.Split(strings.ToLower(text), "\n") {
		if strings.Contains(line, lq) {
			matches = append(matches, i)
		}
	}
	return matches
}

func highlightMatch(line, query string) string {
	if query == "" {
		return line
	}
	lr := []rune(line)
	qr := []rune(strings.ToLower(query))
	idx := runeIndexFold(lr, qr)
	if idx < 0 {
		return line
	}
	end := idx + len(qr)
	return string(lr[:idx]) + ui.SearchMatchStyle.Render(string(lr[idx:end])) + string(lr[end:])
}

// runeIndexFold returns the rune index of the first occurrence of query
// (already lowercase) in line, comparing case-insensitively rune by rune.
// Working in runes keeps the offsets valid for slicing line itself:
// strings.Index over strings.ToLower(line) yields byte offsets into the
// lowered string, which drift — and can slice out of range — when
// lowercasing changes a rune's UTF-8 length (e.g. Ⱥ U+023A is 2 bytes, its
// lowercase ⱥ U+2C65 is 3).
func runeIndexFold(line, query []rune) int {
	if len(query) == 0 {
		return -1
	}
	for i := 0; i+len(query) <= len(line); i++ {
		hit := true
		for j, q := range query {
			if unicode.ToLower(line[i+j]) != q {
				hit = false
				break
			}
		}
		if hit {
			return i
		}
	}
	return -1
}
