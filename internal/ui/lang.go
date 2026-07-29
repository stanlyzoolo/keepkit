package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// languageColors are GitHub's own per-language identity colors — linguist's
// languages.yml, keyed by the lowercased name the API returns in a repo's
// /languages response.
//
// They are deliberately NOT roles on Theme, and a theme switch must not repaint
// them. Theme is keepkit's vocabulary of *meanings*; these are somebody else's
// brand marks, and the whole value of a cyan dot beside "go" is that it is the
// same cyan the reader has already seen on every repo page. A themed language
// dot would be a dot that agrees with the app and disagrees with GitHub, which
// is the one thing it may not do. They live in this package only so the "no hex
// literal below internal/ui" rule still holds literally.
//
// Accepted caveat: linguist picks these against a white page, so the handful
// that are near-black there — lua, powershell, json, crystal — read as very dark
// dots on a dark terminal. Lightening them would make the dot no longer the
// color it is claiming to be, so they stay as published.
var languageColors = map[string]lipgloss.Color{
	"go":                lipgloss.Color("#00ADD8"),
	"rust":              lipgloss.Color("#DEA584"),
	"python":            lipgloss.Color("#3572A5"),
	"javascript":        lipgloss.Color("#F1E05A"),
	"typescript":        lipgloss.Color("#3178C6"),
	"shell":             lipgloss.Color("#89E051"),
	"c":                 lipgloss.Color("#555555"),
	"c++":               lipgloss.Color("#F34B7D"),
	"c#":                lipgloss.Color("#178600"),
	"java":              lipgloss.Color("#B07219"),
	"ruby":              lipgloss.Color("#701516"),
	"php":               lipgloss.Color("#4F5D95"),
	"swift":             lipgloss.Color("#F05138"),
	"kotlin":            lipgloss.Color("#A97BFF"),
	"lua":               lipgloss.Color("#000080"),
	"perl":              lipgloss.Color("#0298C3"),
	"haskell":           lipgloss.Color("#5E5086"),
	"elixir":            lipgloss.Color("#6E4A7E"),
	"erlang":            lipgloss.Color("#B83998"),
	"scala":             lipgloss.Color("#C22D40"),
	"clojure":           lipgloss.Color("#DB5855"),
	"zig":               lipgloss.Color("#EC915C"),
	"nim":               lipgloss.Color("#FFC200"),
	"dart":              lipgloss.Color("#00B4AB"),
	"html":              lipgloss.Color("#E34C26"),
	"css":               lipgloss.Color("#563D7C"),
	"scss":              lipgloss.Color("#C6538C"),
	"sass":              lipgloss.Color("#A53B70"),
	"less":              lipgloss.Color("#1D365D"),
	"vue":               lipgloss.Color("#41B883"),
	"svelte":            lipgloss.Color("#FF3E00"),
	"astro":             lipgloss.Color("#FF5A03"),
	"makefile":          lipgloss.Color("#427819"),
	"cmake":             lipgloss.Color("#DA3434"),
	"meson":             lipgloss.Color("#007800"),
	"dockerfile":        lipgloss.Color("#384D54"),
	"nix":               lipgloss.Color("#7E7EFF"),
	"vim script":        lipgloss.Color("#199F4B"),
	"vim snippet":       lipgloss.Color("#199F4B"),
	"emacs lisp":        lipgloss.Color("#C065DB"),
	"common lisp":       lipgloss.Color("#3FB68B"),
	"scheme":            lipgloss.Color("#1E4AEC"),
	"racket":            lipgloss.Color("#3C5CAA"),
	"powershell":        lipgloss.Color("#012456"),
	"batchfile":         lipgloss.Color("#C1F12E"),
	"assembly":          lipgloss.Color("#6E4C13"),
	"objective-c":       lipgloss.Color("#438EFF"),
	"r":                 lipgloss.Color("#198CE7"),
	"julia":             lipgloss.Color("#A270BA"),
	"ocaml":             lipgloss.Color("#EF7A08"),
	"f#":                lipgloss.Color("#B845FC"),
	"groovy":            lipgloss.Color("#4298B8"),
	"tex":               lipgloss.Color("#3D6117"),
	"roff":              lipgloss.Color("#ECDEBE"),
	"m4":                lipgloss.Color("#63B5E5"),
	"awk":               lipgloss.Color("#C30E9B"),
	"hcl":               lipgloss.Color("#844FBA"),
	"starlark":          lipgloss.Color("#76D275"),
	"jsonnet":           lipgloss.Color("#0064BD"),
	"solidity":          lipgloss.Color("#AA6746"),
	"webassembly":       lipgloss.Color("#04133B"),
	"coffeescript":      lipgloss.Color("#244776"),
	"crystal":           lipgloss.Color("#000100"),
	"d":                 lipgloss.Color("#BA595E"),
	"fortran":           lipgloss.Color("#4D41B1"),
	"pascal":            lipgloss.Color("#E3F171"),
	"prolog":            lipgloss.Color("#74283C"),
	"tcl":               lipgloss.Color("#E4CC98"),
	"vhdl":              lipgloss.Color("#ADB2CB"),
	"verilog":           lipgloss.Color("#B2B7F8"),
	"ada":               lipgloss.Color("#02F88C"),
	"elm":               lipgloss.Color("#60B5CC"),
	"purescript":        lipgloss.Color("#1D222D"),
	"rescript":          lipgloss.Color("#ED5051"),
	"reason":            lipgloss.Color("#FF5847"),
	"v":                 lipgloss.Color("#4F87C4"),
	"odin":              lipgloss.Color("#60AFFE"),
	"gleam":             lipgloss.Color("#FFAFF3"),
	"mojo":              lipgloss.Color("#FF4C1F"),
	"bicep":             lipgloss.Color("#519ABA"),
	"json":              lipgloss.Color("#292929"),
	"yaml":              lipgloss.Color("#CB171E"),
	"toml":              lipgloss.Color("#9C4221"),
	"xml":               lipgloss.Color("#0060AC"),
	"markdown":          lipgloss.Color("#083FA1"),
	"mdx":               lipgloss.Color("#FCB32C"),
	"jupyter notebook":  lipgloss.Color("#DA5B0B"),
	"handlebars":        lipgloss.Color("#F7931E"),
	"twig":              lipgloss.Color("#C1D026"),
	"blade":             lipgloss.Color("#F7523F"),
	"pug":               lipgloss.Color("#A86454"),
	"ejs":               lipgloss.Color("#A91E50"),
	"mustache":          lipgloss.Color("#724B3B"),
	"jinja":             lipgloss.Color("#A52A22"),
	"gherkin":           lipgloss.Color("#5B2063"),
	"plpgsql":           lipgloss.Color("#336790"),
	"smalltalk":         lipgloss.Color("#596706"),
	"raku":              lipgloss.Color("#0000FB"),
	"nushell":           lipgloss.Color("#3B8B8C"),
	"fish":              lipgloss.Color("#4AAE47"),
	"just":              lipgloss.Color("#384D54"),
	"sql":               lipgloss.Color("#E38C00"),
	"applescript":       lipgloss.Color("#101F1F"),
	"rich text format":  lipgloss.Color("#D4B94D"),
	"typst":             lipgloss.Color("#239DAD"),
	"cuda":              lipgloss.Color("#3A4E3A"),
	"objective-c++":     lipgloss.Color("#6866FB"),
	"protocol buffer":   lipgloss.Color("#E8C55A"),
	"terraform":         lipgloss.Color("#844FBA"),
	"kickstart":         lipgloss.Color("#A52A22"),
	"editorconfig":      lipgloss.Color("#FFF1F2"),
	"procfile":          lipgloss.Color("#3B2F63"),
	"shellsession":      lipgloss.Color("#89E051"),
	"powerbuilder":      lipgloss.Color("#8F0F8D"),
	"visual basic .net": lipgloss.Color("#945DB7"),
}

// LanguageColor returns the color GitHub paints a language with, and whether it
// knows the language at all. An unknown language gets no color from here — the
// caller falls back to a theme role, so a language linguist has since added
// reads as "unrecognized" rather than as the wrong one.
func LanguageColor(name string) (lipgloss.Color, bool) {
	c, ok := languageColors[strings.ToLower(name)]
	return c, ok
}
