package model

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
	"github.com/stanlyzoolo/keepkit/internal/ui"
	"github.com/stanlyzoolo/keepkit/internal/updater"
	"github.com/stanlyzoolo/keepkit/internal/version"
)

const (
	focusTools = 0
	focusBrief = 1
	focusHelp  = 2
)

type VersionInfo struct {
	Installed string
	Latest    string
	// InstalledKnown separates "local detection ran and found nothing"
	// (Installed == "", InstalledKnown == true) from "detection still in
	// flight" — only the installedMsg handler sets it, so the card can show
	// a pending state instead of a premature "not found".
	InstalledKnown bool
	// InstalledPresent separates the two states InstalledKnown collapses when
	// Installed == "": the tool is installed but won't name its version (a TUI
	// app that ignores --version) versus not installed at all. Only the
	// installedMsg handler sets it, from version.InstalledVersion's second
	// result; the card renders the two differently.
	InstalledPresent bool
}

// installedMsg carries the locally detected installed version for a tool, plus
// whether the tool is present at all (a version-less install is still an
// install). It is emitted by fetchInstalledCmd independently of any network
// activity so the installed version renders immediately, without waiting on
// GitHub.
type installedMsg struct {
	toolName  string
	installed string
	present   bool
}

// remoteMsg carries the result of a single network pass (release + repo info +
// languages) for a tool with a GitHub ref. It merges the latest tag, repo
// status and repo card in one message.
type remoteMsg struct {
	toolName   string
	latest     string
	repoStatus string
	card       version.RepoCard
	rate       version.RateLimit
	err        error
	// conclusive mirrors version.RepoData.Conclusive: the pass settled this tool
	// for the cache window. It is what remoteAnswered is written from, because
	// err cannot answer that even now that every failure class arrives named:
	// a repo with no releases settles the tool with a nil err, and a pass that
	// served a stale card under a rate limit settles nothing while carrying one.
	conclusive bool
}

// rateMsg carries a rate-limit snapshot fetched from GET /rate_limit, which
// does not spend core quota. It seeds/refreshes m.rate independently of any
// per-tool remote fetch (e.g. on startup and on the API-status overlay refresh).
type rateMsg struct {
	rate version.RateLimit
	err  error
}

type changelogMsg struct {
	toolName    string
	tag         string
	body        string
	htmlUrl     string
	publishedAt string
	err         error
}

type helpOutputMsg struct {
	toolName string
	mode     int
	output   string
	err      error
}

// readmeMsg carries the repository README for a tool, fetched once per tool per
// cache TTL. It mirrors changelogMsg: the whole message is stored in
// m.readmeData, so a 404 (ErrNoReadme) or a rate-limit error is a session-cached
// negative result and selection moves never re-fire the fetch.
type readmeMsg struct {
	toolName string
	content  string
	err      error
}

// statusExpiredMsg is the one-shot tick that retires a transient status message.
// setStatus stamps the current statusSeq into it; the Update handler clears
// m.statusMsg only when the seq still matches, so a timer for a message that has
// since been superseded is a harmless no-op instead of clearing the newer one.
type statusExpiredMsg struct{ seq int }

// statusMsgTTL is how long a transient status message holds the bar before it
// yields back to the hints. A var, not a const, mirroring launchTimeout: tea.Tick
// blocks for the full duration, so tests shrink it to inspect the produced
// statusExpiredMsg without a real sleep.
var statusMsgTTL = 1 * time.Second

const (
	helpModeHelp = 0
	helpModeMan  = 1
	// helpModeReadme is the third source of panel [3]: the repository README,
	// fetched from the GitHub API rather than probed locally. Note helpCache is
	// a [2]string indexed by mode — every index site must branch on this mode
	// before reaching the array.
	helpModeReadme = 2
)

// updateLogMaxLines caps the live update log buffer. Only the tail matters (the
// final "installed"/error lines); older output can be dropped without loss.
const updateLogMaxLines = 500

// updateBusyStatus is what enter in [2] and [U] report while another update
// runs (one at a time, no queue). Shared so the two refusals cannot word it
// differently.
const updateBusyStatus = "another update is running"

// refreshFailedStatus names why an [r] failed. The taxonomy is
// deliberately two-tier: a rate limit has an answer the user can act on — wait,
// or raise the ceiling from the [a] overlay — while a 401, a 5xx, a timeout and
// a dropped connection all mean "try later" and the UI has nothing to tell them
// apart with. In particular there is no token wording here: by the time a fetch
// fails, doGH has already retried a rejected token anonymously, so a refresh
// that failed did not fail *because of* the token — and the gauge and the [a]
// overlay are where that state is reported anyway.
func refreshFailedStatus(err error) string {
	if errors.Is(err, version.ErrRateLimited) {
		return "refresh failed: rate limited — press [a]"
	}
	return "refresh failed: network error"
}

// selfToolName is the name keepkit uses for itself inside the update pipeline:
// the updater's detection target, the updatingFor/updateLogFor guard value and
// the label the confirm bar and status messages show. A constant rather than a
// meta.yaml lookup — the feature's main case is a keepkit that is not tracked.
const selfToolName = "keepkit"

// selfState is the self-update banner's state machine. There is deliberately no
// "updating" member: whether a self-update is in flight is derived from the
// update pipeline itself (selfUpdating), so the two can never disagree.
//
//	selfNone         no newer release known — no banner at all
//	selfOffered      full banner: "keepkit <v> available — [U] update  [X] dismiss"
//	selfDismissed    folded to a compact "[U] update" cell beside the version
//	selfUpdated      full banner: "keepkit updated — [U] restart  [X] later"
//	selfUpdatedLater folded to a compact "[U] restart" cell
//
// Six sites switch on it — [U] (selfUpdateKey), [X], selfBannerCells,
// selfCompactCell, selfUpdateAvailable (the ↑ on the status bar's version cell)
// and the [?] Self group — and .golangci.yml carries no exhaustiveness linter,
// so a seventh member would compile while silently missing some of them. Every
// one of those switches therefore enumerates the whole enum and ends in a
// default (the two key handlers log there), and TestSelfStateSitesAreExhaustive
// walks selfNone..selfStateCount through all six. selfStateCount must stay last.
type selfState int

const (
	selfNone selfState = iota
	selfOffered
	selfDismissed
	selfUpdated
	selfUpdatedLater
	selfStateCount
)

// selfCheckMsg carries the result of the startup self-check — keepkit's own
// latest release tag. An empty latest with a nil error is the conclusive answer
// "no release published", not a failure. The comparison deliberately lives in
// the handler rather than the command, so it runs against the model's
// appVersion (the version of the running binary).
type selfCheckMsg struct {
	latest string
	err    error
}

// updateDetectedMsg carries the result of updater.Detect for a tool, run in a
// tea.Cmd because detection spawns subprocesses (go version -m, cargo install
// --list) and must never run on the Update thread. The handler enters the
// confirm mode on success and shows a hint on ErrUnknownManager.
//
// self marks a detection fired by the self-update banner ([U]) rather than by
// enter in [2] on a tracked tool row. The two differ in what makes a landed
// result still relevant: a tool result must match the selection, while
// keepkit's own has no selection to match (the main case is a keepkit that is
// not tracked) — see acceptsUpdateDetect.
type updateDetectedMsg struct {
	tool string
	plan updater.Plan
	err  error
	self bool
}

// updateChunkMsg carries one segment of the running update's merged
// stdout+stderr. replace is set for a segment terminated by '\r' (progress
// bars), so it overwrites the last buffered line instead of appending. ch is
// the same channel the reader goroutine writes to, threaded through so the
// handler can re-subscribe with waitForChunkCmd. (The channel is typed
// updateLine rather than string — see the note there — so it can also carry the
// completion+error item without a second channel.)
type updateChunkMsg struct {
	tool    string
	line    string
	replace bool
	ch      chan updateLine
}

// updateDoneMsg signals the update subprocess finished (err is the exit error,
// nil on success). The handler clears updatingFor and, on success, re-detects
// the installed version so the ↑ marker clears. elapsed is how long the process
// ran, measured by startUpdateCmd; it is zero when the run never started (empty
// argv, a StdoutPipe or Start error), and the block in [3] then omits the cell.
type updateDoneMsg struct {
	tool    string
	err     error
	elapsed time.Duration
}

// updateOutcome is the terminal state of the last update session: what the
// command did, and — once the post-update re-detect lands — what actually got
// installed. A zero tool means no session has finished yet.
//
// It is model state rather than lines appended to updateLog because the buffer
// is wrapped at render time (wrapText rebuilds lines from strings.Fields and
// counts runes), which would shred any styling written into it. So the buffer
// stays exactly what the command printed and the renderer draws the outcome —
// the same split changelogRender uses, where styling lands on whole finished
// lines.
//
// The two phases exist because "the command finished" is not "the tool was
// updated": brew can exit zero having done nothing. Phase 1 states only what the
// exit status proves; verified/now/nowPresent arrive from the installedMsg the
// success path fires, and are what let the block say the version actually moved.
type updateOutcome struct {
	tool, manager string
	elapsed       time.Duration
	err           error
	verified      bool   // the post-update re-detect landed
	was, now      string // installed version before / after
	nowPresent    bool
}

type Model struct {
	tools               []loader.Tool
	versions            map[string]VersionInfo
	repoStatus          map[string]string
	repoCards           map[string]version.RepoCard
	changelogData       map[string]changelogMsg
	changelogLoadingFor string
	focus               int
	toolsViewport       viewport.Model
	briefViewport       viewport.Model
	helpViewport        viewport.Model
	search              textinput.Model
	// searchPrevName is the commit/rollback anchor for modeSearch: captured
	// (from the current selection) when "/" opens the search and cleared on any
	// exit. enter commits to the highlighted match; esc rolls the cursor back
	// to this tool in the full list.
	searchPrevName string
	statusMsg      string
	// statusSeq is a generation counter for transient status messages: setStatus
	// bumps it on every transient message and stamps the value into the
	// statusExpiredMsg tick it returns, so a stale timer (from a message already
	// superseded by a newer one) is dropped instead of clearing the current
	// message early. In-flight statuses ("launching …") bypass setStatus and so
	// carry no timer — they are extinguished by launchDoneMsg, not the clock.
	statusSeq int
	width     int
	height    int
	ready     bool

	// mode is the single input/modal state (see inputMode in mode.go). The
	// zero value modeNormal is the base state; per-mode key handlers own the
	// input while any other mode is active.
	mode inputMode

	noteInput textinput.Model
	tagsInput textinput.Model

	trackInput textinput.Model

	untrackTarget string

	nameInput textinput.Model

	// runInput is the modeRunInput command prompt (enter in focusTools) and
	// lastRun remembers, per tool name, the last command dispatched this
	// session so the next prompt prefills it instead of the bare tool name.
	// Session-only: rename deletes the old-name entry alongside helpCache et
	// al.; untrack leaves it (harmless, session-scoped).
	runInput textinput.Model
	lastRun  map[string]string

	// launchingFor is the one-adapter-launch-at-a-time guard (the launch twin
	// of updatingFor): the name of the tool whose tab-open adapter run is in
	// flight, empty when idle. Set on startLaunchCmd dispatch, cleared by
	// launchDoneMsg. Without it a second enter+enter while osascript sits on
	// the macOS Automation dialog (up to launchTimeout) would dispatch a
	// concurrent adapter run. The ExecProcess fallback needs no guard — it
	// suspends the TUI synchronously.
	launchingFor string

	// pendingLaunchName/Command hold the exec fallback deferred by the
	// launchDoneMsg mode gate: the adapter failed while another mode owned the
	// screen (tea.ExecProcess would seize the terminal under an open
	// editor/overlay and route keystrokes to the spawned shell), so the
	// fallback waits here until the keystroke that returns the model to
	// modeNormal — flushPendingLaunch (mode.go) then dispatches it with a
	// visible status message. A statusMsg set at gate time would be dead UI:
	// every non-normal mode's renderStatusBar branch outranks the statusMsg
	// branch, and the blanket statusMsg reset on tea.KeyMsg wipes it on the
	// very keystroke that closes the mode.
	pendingLaunchName    string
	pendingLaunchCommand string

	// spinner animates while a force refresh ([r]) is in flight; refreshingFor
	// holds the name of the tool being refreshed (empty = idle). refreshingFor
	// doubles as the double-press guard and as the tick-loop / render gate.
	spinner       spinner.Model
	refreshingFor string

	// updatingFor twins refreshingFor for the in-TUI update flow: it holds the
	// name of the tool currently being updated (empty = idle), drives the card
	// spinner and doubles as the single-update-at-a-time guard. updatePlan is
	// the plan awaiting confirmation in modeConfirmUpdate and updateTarget the
	// name it belongs to — resolved when the plan was detected, not when enter
	// is pressed, so the dialog cannot be retargeted by a selection move and
	// keepkit's own update (which has no row, and no selection when the tracker
	// is empty) needs no separate identity. updateLog is the live merged
	// stdout+stderr buffer for panel [3]; updateLogFor is the tool it belongs to,
	// so navigating away shows normal help and navigating back shows the log
	// again (the buffer survives until the next update starts). updateOutcome is
	// that session's terminal state, rendered as a block under the buffer; it is
	// reset with the buffer, so the two can never describe different sessions.
	updatingFor   string
	updatePlan    updater.Plan
	updateTarget  string
	updateLog     []string
	updateLogFor  string
	updateOutcome updateOutcome

	// appVersion is the version of the running binary (ldflag or buildinfo,
	// injected by WithAppVersion) and the gate for the whole self-update
	// feature: empty or "dev" means no self-check request and no banner. It is
	// also what the latest release is compared against — deliberately not
	// m.versions[selfToolName].Installed, whose three detection fallbacks
	// (--version, brew dir, cargo install --list) can report a version that is
	// not the one this process is running.
	appVersion string
	// selfLatest is keepkit's own latest release tag and selfState the banner
	// state machine (see selfState). selfUpdateLog marks the live [3] log as
	// keepkit's own, so it stays visible regardless of the selection — an
	// untracked keepkit has no row to select. restartRequested is the exit flag
	// main reads off the model p.Run() returns.
	selfLatest       string
	selfState        selfState
	selfUpdateLog    bool
	restartRequested bool

	meta []loader.ToolMeta
	// metaSelected is an index into filteredMeta() — a tool index, never a
	// screen row. Grouping only reorders that projection and inserts
	// non-selectable header lines, so navigation and every index-writing site
	// stay unit-consistent; the two maps below are the only tool-index ↔
	// screen-line translation, and they are the identity when grouping is off.
	metaSelected int

	// groupByTag is the [1] list view toggle (space in focusTools): off (the
	// default) is the flat update-grouped list, on partitions the tools under
	// tag divider headers. Display-only — meta.yaml is never reordered.
	groupByTag bool
	// toolLine maps a tool index to its screen line, lineTool a screen line
	// back to its tool index (-1 on a header row). Both are rebuilt by
	// setToolsContent, the single point that repaints the list — a repaint
	// that bypasses it would leave them describing the previous content.
	toolLine []int
	lineTool []int

	helpMode       int
	helpLoadingFor string
	helpCache      map[string][2]string
	// readmeData is the session cache for helpModeReadme, keyed by tool name.
	// The whole message is stored (content or error), so a negative result is
	// remembered for the session instead of refetched on every selection move;
	// a force refresh ([r] in the brief panel) clears the entry.
	readmeData map[string]readmeMsg
	// readmeLoading holds the tools whose README request is in flight — the
	// readme twin of helpLoadingFor, but a set rather than a single name: the
	// entry lands in readmeData only when the response arrives, so without it
	// two selection moves onto the same tool inside that window (j then k, a
	// click back) would each spend a GitHub request. Cleared by readmeMsg.
	readmeLoading map[string]bool
	// remoteAnswered holds the tools whose network pass the version layer called
	// CONCLUSIVE — it settled the tool for the cache window, even when it
	// carried no Latest (a repo with neither releases nor tags). needsRemote's
	// empty-Latest clause would otherwise re-dispatch the whole pass on every
	// cursor visit. The honest cost of that is a goroutine and a cache.json
	// read, not API quota: errNoReleases is already conclusive on the version
	// side, so the repeat was served from cache.
	//
	// Session-scoped, and written from msg.conclusive rather than from the error
	// or from m.repoStatus. Neither of those can answer it: a repo with no
	// releases settles the tool with a nil error, a rate-limited pass that
	// served a stale card settles nothing while carrying one, and that same pass
	// writes "active"/"archived" into repoStatus. Cleared wholesale when a new
	// token validates — nothing settled under the old credential is settled
	// under the new one.
	remoteAnswered map[string]bool
	// readmeRender memoizes the last glamour render (see readme.go).
	readmeRender readmeRenderCache
	// changelogRender memoizes the last release-notes markdown conversion
	// (see render.go). A pointer, so the value-receiver card renderers can
	// fill it; nil is a working cache-less mode for Model{} literals.
	changelogRender *changelogRenderCache

	// helpEntries indexes the navigable entries (flag/subcommand line plus its
	// description block) of the current help text, in wrapped display-line
	// coordinates. helpNavIdx is the spotlight cursor over them: -1 = off
	// (plain reading; the panel renders full-color). Empty helpEntries (update
	// log, placeholders, prose-only help) leaves j/k as plain scroll.
	// helpBase caches the wrapped+colorized full-color content, recomputed
	// only in setHelpContent — cursor moves repaint per keystroke and must not
	// re-run the colorizeHelp regex over a whole man page each time.
	helpEntries []entryRange
	helpNavIdx  int
	helpBase    string

	// darkBG is the terminal background resolved once at construction
	// (lipgloss caches the probe) and handed to renderReadme, so glamour never
	// runs its own OSC background query against Bubble Tea's input reader.
	darkBG bool

	toolsW int
	briefW int
	helpW  int

	// rate is the latest GitHub rate-limit snapshot, seeded on startup by
	// fetchRateCmd and refreshed by remote fetches. A Known==false snapshot
	// never overwrites a previously-known value.
	rate version.RateLimit

	// tokenInput is the overlay's masked token field (modeTokenInput) and
	// tokenError holds the inline "token invalid" message.
	tokenInput textinput.Model
	tokenError string

	// styles is the whole app's color vocabulary, built from one ui.Theme in
	// New(). Every renderer reads it through sty() rather than reaching for a
	// package-level style, which is what makes a theme a *value*: handing the
	// model a different ui.Styles re-colors the entire UI, and nothing else has
	// to know. A pointer because renderers are value receivers and a frame
	// reads it dozens of times.
	styles *ui.Styles
}

// sty returns the model's styles, falling back to the default theme when it has
// none. The fallback exists for the Model{} literals most tests are built from
// — the same nil-tolerant shape changelogRender uses — and is the *only* place
// below internal/ui allowed to read a package-level style: a renderer that
// called ui.DefaultStyles() itself would keep painting the default palette
// after the model switched themes.
func (m Model) sty() *ui.Styles {
	if m.styles == nil {
		return ui.DefaultStyles()
	}
	return m.styles
}

func New(meta []loader.ToolMeta) Model {
	ti := textinput.New()
	ti.Placeholder = "search..."
	ti.CharLimit = 64

	ni := textinput.New()
	ni.Placeholder = "note..."
	ni.CharLimit = 256

	tgi := textinput.New()
	tgi.Placeholder = "tag..."
	tgi.CharLimit = 256

	tri := textinput.New()
	tri.Placeholder = "github url or tool name..."
	tri.CharLimit = 256

	nmi := textinput.New()
	nmi.Placeholder = "new name..."
	nmi.CharLimit = 256

	rni := textinput.New()
	rni.Placeholder = "command..."
	rni.CharLimit = 512

	tki := textinput.New()
	tki.Placeholder = "ghp_..."
	tki.CharLimit = 256
	tki.EchoMode = textinput.EchoPassword
	tki.EchoCharacter = '•'

	styles := ui.NewStyles(ui.Default)

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = styles.Accent

	m := Model{
		styles:          styles,
		tools:           loader.ToolsFromMeta(meta),
		versions:        make(map[string]VersionInfo),
		repoStatus:      make(map[string]string),
		repoCards:       make(map[string]version.RepoCard),
		changelogData:   make(map[string]changelogMsg),
		helpCache:       make(map[string][2]string),
		readmeData:      make(map[string]readmeMsg),
		readmeLoading:   make(map[string]bool),
		remoteAnswered:  make(map[string]bool),
		changelogRender: &changelogRenderCache{},
		search:          ti,
		noteInput:       ni,
		tagsInput:       tgi,
		trackInput:      tri,
		nameInput:       nmi,
		runInput:        rni,
		lastRun:         make(map[string]string),
		tokenInput:      tki,
		spinner:         sp,
		meta:            meta,
		helpNavIdx:      -1,
		// The README is the default source of [3]: it is the only one that
		// exists for a tracked-but-not-installed tool, which is exactly the
		// state where docs matter most.
		helpMode: helpModeReadme,
		darkBG:   lipgloss.HasDarkBackground(),
	}

	return m
}

// WithAppVersion injects the running binary's version (main.buildVersion) and is
// what switches the self-update feature on. A builder rather than a New
// parameter because New( is called from a hundred-odd tests: the zero value
// leaves the feature off, so none of them had to change.
func (m Model) WithAppVersion(v string) Model {
	m.appVersion = v
	return m
}

// RestartRequested reports whether the user accepted [U] restart after a
// successful self-update. main reads it off the model p.Run() returns — after
// Bubble Tea has restored the terminal — and re-execs the new binary.
func (m Model) RestartRequested() bool { return m.restartRequested }

// selfCheckEnabled gates the self-check: a build with no release behind it has
// no version worth comparing against a release tag, so it makes no request and
// shows no banner.
//
// "dev" is the ldflag default, but it is not the only shape a working copy
// reports. Since Go 1.24 a plain `go build .` / `go install .` stamps the module
// version from VCS, so this project's own documented dev commands yield a
// pseudo-version (v0.0.0-20260725115912-1be4bafa79c8) or, with uncommitted
// changes, a +dirty suffix. Those canonicalize as valid semver pre-releases that
// sort below every real tag, so without this check a developer's own build would
// announce an update and offer to overwrite itself with the latest release.
func (m Model) selfCheckEnabled() bool {
	return m.appVersion != "" && m.appVersion != "dev" && !isDevVersion(m.appVersion)
}

// pseudoVersionRe matches the Go pseudo-version suffix — a 14-digit UTC
// timestamp plus a 12-char commit prefix — which is common to all three of its
// forms (vX.0.0-<ts>-<hash>, vX.Y.Z-0.<ts>-<hash>, vX.Y.Z-pre.0.<ts>-<hash>).
// The separator before the timestamp is a dash in the first form and a dot in
// the other two, where the timestamp extends an existing pre-release.
var pseudoVersionRe = regexp.MustCompile(`[-.][0-9]{14}-[0-9a-f]{12}$`)

// isDevVersion reports whether v describes a working copy rather than a release.
// Build metadata is enough on its own: buildinfo appends +dirty for uncommitted
// changes and no release tag of this project carries a + at all.
func isDevVersion(v string) bool {
	if strings.Contains(v, "+") {
		return true
	}
	return pseudoVersionRe.MatchString(v)
}

// displayVersion is the running build's version as a human reads it, for the
// [?] overlay's title. A release build already reads well and passes through
// untouched; a working copy does not.
//
// Go stamps an untagged commit with a pseudo-version — v0.1.1-0.20260728221642-
// 0b86a47f4f05 — which is 44 characters of timestamp in a keys sheet, and its
// leading number is a release that does not exist yet. The two facts a reader
// actually wants are behind it: the last release, and which commit this is. Go's
// own rule maps one onto the other, so the base is recovered rather than shown
// as stamped:
//
//	vX.Y.(Z+1)-0.<ts>-<hash>     an untagged commit after the release vX.Y.Z
//	vX.Y.Z-pre.0.<ts>-<hash>     … after the pre-release vX.Y.Z-pre
//	vX.0.0-<ts>-<hash>           … in a repository with no tag at all
//
// The result carries a + and the commit, so it can never be mistaken for the
// release itself — which is also why the *raw* value stays in m.appVersion: the
// self-update gate reads it, and it has to keep seeing a working copy.
func displayVersion(v string) string {
	// Build metadata rides on the end (+dirty for uncommitted changes) and the
	// pseudo-version pattern is anchored, so it is split off first — otherwise
	// the one build most likely to be read, a developer's own working copy,
	// is the one that keeps all 44 characters.
	v, meta, dirty := strings.Cut(v, "+")
	loc := pseudoVersionRe.FindStringIndex(v)
	if loc == nil {
		if dirty {
			return v + "+" + meta
		}
		return v
	}
	base, hash := v[:loc[0]], v[len(v)-12:]
	if len(hash) > 7 {
		hash = hash[:7]
	}
	// The separator the regex matched is what tells the tagless form apart: Go
	// writes vX.0.0-<ts> with a dash and extends an existing pre-release with a
	// dot. Reading the base's own tail instead would take the "0" of v0.0.0 for
	// the pre-release marker and answer "v0.0".
	switch sep := v[loc[0]]; {
	case sep == '-':
		base = "dev" // vX.0.0-<ts>-<hash>: no tag to be after
	case strings.HasSuffix(base, "-0"):
		// A release base, bumped a patch by Go: undo the bump to name it.
		base = previousPatch(strings.TrimSuffix(base, "-0"))
	case strings.HasSuffix(base, ".0"):
		// A pre-release base, extended by Go: the tag is what is left.
		base = strings.TrimSuffix(base, ".0")
	default:
		base = ""
	}
	if base == "" {
		base = "dev"
	}
	out := base + "+" + hash
	if dirty {
		out += "-" + meta
	}
	return out
}

// previousPatch turns vX.Y.Z into vX.Y.(Z-1) — the inverse of the patch bump Go
// applies when it builds a pseudo-version for a commit after a release. It
// answers "" for anything it cannot invert (a non-numeric or zero patch), which
// the caller reads as "no tag behind this build".
func previousPatch(v string) string {
	dot := strings.LastIndex(v, ".")
	if dot < 0 {
		return ""
	}
	patch, err := strconv.Atoi(v[dot+1:])
	if err != nil || patch <= 0 {
		return ""
	}
	return v[:dot+1] + strconv.Itoa(patch-1)
}

// isSelfUpdate reports whether an update of the named tool is keepkit's own
// self-update — the one that owns panel [3] under every selection and ends in the
// [U] restart offer — rather than a plain tool update. Every update of keepkit is
// a self-update regardless of which key started it, so the target name decides
// which *kind* of update it is; but the feature as a whole is gated on the running
// build having a release behind it, exactly like the startup check
// (selfCheckEnabled), and the name alone would smuggle it past that gate.
//
// The gate is not cosmetic. On a dev build there is no check, no banner and
// nothing to compare, so enter in [2] on a tracked keepkit row must stay what
// it is for every other row: a plain tool update. Announcing a restart there
// would also be a lie — a working copy's argv0 carries a path separator, so
// resolveSelfPath re-execs that very binary and would silently return the user
// to the pre-update version.
func (m Model) isSelfUpdate(name string) bool {
	return name == selfToolName && m.selfCheckEnabled()
}

// selfUpdating reports whether the update currently in flight is keepkit's own.
// Derived from the update pipeline's own state instead of a selfState member, so
// "a self-update is running" can never drift out of sync with updatingFor.
func (m Model) selfUpdating() bool {
	return m.isSelfUpdate(m.updatingFor)
}

// selfTool is the updater's detection target for keepkit itself. A tracked
// keepkit is used as tracked, so a update_cmd override in meta.yaml governs the
// self-update exactly as it governs an enter in [2] on that row; otherwise the
// entry is synthesized — the feature's main case is a keepkit that is not in
// the tracker at all, and updater.Detect only needs the name (plus the
// override).
func (m Model) selfTool() loader.Tool {
	if t, ok := m.toolByName(selfToolName); ok {
		return t
	}
	// The GitHub field carries the same host-prefixed form every tracked tool
	// has ("github.com/owner/repo"), the shape openURLCmd and NormalizeRepo
	// expect — updater.Detect ignores it, but a wrong-shaped value here would be
	// a trap for the next reader of the synthesized entry.
	return loader.Tool{Name: selfToolName, GitHub: "github.com/" + version.SelfRepo}
}

// showsUpdateLog reports whether panel [3] currently belongs to an update log
// instead of help. Two ways to own the panel: the selected tool is the one whose
// log is buffered (the tool path — the buffer is sticky per tool, so navigating
// away shows normal help and navigating back shows the log again), or the log is
// keepkit's own, which stays visible regardless of the selection because an
// untracked keepkit has no row to select and the tracker can even be empty.
//
// The single predicate every updateLogFor-bound site shares — the inset title,
// the renderHelpContent branch, the per-chunk repaint, the setHelpContent entry
// gate and the help fetch — so they can never disagree about who owns [3].
func (m Model) showsUpdateLog() bool {
	if m.selfUpdateLog {
		return true
	}
	if m.updateLogFor == "" {
		return false
	}
	mt, ok := m.selectedMeta()
	return ok && mt.Name == m.updateLogFor
}

// startToolUpdate is enter in [2]: install the release the card is showing. It
// fires only when the tool actually has a pending one (else a hint) and no
// update is already running — one at a time, no queue, and the refusal reports
// itself like every other blocked action here, because a running update's only
// other sign is a card spinner invisible unless that tool is selected.
// Detection spawns subprocesses, so it runs in a tea.Cmd; the updateDetectedMsg
// handler is what opens the confirm dialog.
func (m *Model) startToolUpdate() tea.Cmd {
	if m.updatingFor != "" {
		return m.setStatus(updateBusyStatus)
	}
	t, ok := m.selectedTool()
	if !ok {
		return nil
	}
	if !m.hasUpdate(t.Name) {
		return m.setStatus("no update available for " + t.Name)
	}
	return detectUpdateCmd(t, false)
}

// dismissSelfLog releases a *completed* self-update log's claim on panel [3],
// reporting whether it did. A live self-update keeps the panel — the log is the
// only place its output is visible. Called from the paths that mean "show me
// something else": an explicit [H]/[M]/[R] and a selection move.
//
// Only the override is dropped, not updateLogFor: a tracked keepkit keeps the
// same per-tool stickiness every other tool has.
func (m *Model) dismissSelfLog() bool {
	if !m.selfUpdateLog || m.selfUpdating() {
		return false
	}
	m.selfUpdateLog = false
	return true
}

// selfUpdateKey is what [U] does: start the offered self-update, or quit into the
// restart once one has finished. A named helper like every other multi-step key
// (toggleGroupByTag, switchHelpMode), so the normal-mode switch keeps one line
// per key instead of the two guards plus a nested switch this needs.
//
// With no banner on screen (selfNone) the key is unbound — and on a dev build
// that is the only reachable state, where the feature is documented as absent end
// to end. So the "one update at a time" refusal is checked *after* the state,
// never before it: answering `another update is running` there would make U the
// one audible piece of a self-update surface that is supposed to be off.
func (m *Model) selfUpdateKey() tea.Cmd {
	if m.selfState == selfNone {
		return nil
	}
	// One update at a time, self or tool: while updatingFor is set the key
	// reports itself instead of doing nothing — the banner on screen still
	// advertises [U], and during a *tool* update the only other indicator is a
	// card spinner that is invisible unless that tool is selected.
	if m.updatingFor != "" {
		return m.setStatus(updateBusyStatus)
	}
	switch m.selfState {
	case selfOffered, selfDismissed:
		// Detection spawns subprocesses (go version -m, cargo install --list),
		// so it can only run as a command. The banner state is deliberately left
		// alone — until the confirm dialog takes over, U stays a retry.
		return detectUpdateCmd(m.selfTool(), true)
	case selfUpdated, selfUpdatedLater:
		// The new binary is on disk; only this process is still the old one.
		// Quitting is the whole restart from the model's side: the flag rides the
		// final model out of p.Run(), and main re-execs after Bubble Tea has
		// restored the terminal — doing it from inside Update would exec on top
		// of the alt screen.
		m.restartRequested = true
		return tea.Quit
	case selfNone:
		// Unreachable — the early return above owns it — but named so that the
		// switch enumerates the whole enum like every other selfState site.
	default:
		logx.Errorf("self-update: [U] on unhandled selfState %d", m.selfState)
	}
	return nil
}

// acceptsUpdateDetect reports whether a landed detection result may still open
// the confirm dialog. Both paths refuse while an update runs (one at a time, no
// queue) and while any input mode owns the keyboard: detection spawns
// subprocesses and the answer can arrive seconds later, so a confirm dialog
// opening under an editor, a search or an overlay would steal the keystroke
// aimed at it (the same reasoning as launchDoneMsg's mode gate). Beyond that a
// tool result must still match the selection — the user may have moved on —
// while keepkit's own has no selection to match: it is typically untracked and
// the tracker may be empty.
func (m Model) acceptsUpdateDetect(msg updateDetectedMsg) bool {
	if m.updatingFor != "" || m.mode != modeNormal {
		return false
	}
	if msg.self {
		return true
	}
	mt, ok := m.selectedMeta()
	return ok && mt.Name == msg.tool
}

// recordUpdateOutcome closes an update session: it stores the terminal state the
// block under the live log renders, and — on a failure — records it for post-hoc
// research. Both paths call it, a tool's update and keepkit's own, on success as
// well as failure, so the two can never drift in what a finished update looks
// like or in how a failed one is logged.
//
// was is read here rather than later because this runs before the version merge:
// the success path returns fetchInstalledCmd, whose installedMsg is what fills in
// the other half of the outcome (see the phase 2 note on updateOutcome), so
// m.versions still holds the pre-update version at this point.
//
// The repaint goes through setHelpContent, not a bare SetContent: the *text* of
// [3] changes here, and that is the single recompute point for it. The entry
// index stays empty for a log either way, so j/k remain plain scroll.
//
// Nothing is written into the buffer. A failure with no output at all (empty
// argv, missing manager binary, an immediate non-zero exit) used to seed its
// reason there so [3] would not read "starting update…" while the status bar
// pointed at it for the reason; updateOutcomeBlock now states it, so the buffer
// stays exactly what the command printed and the tail below is the manager's own
// output rather than a line this function wrote a moment ago.
//
// The update argv never carries the token and msg.err is an exec/exit error, so
// the log line stays token-free.
func (m *Model) recordUpdateOutcome(msg updateDoneMsg) {
	m.updateOutcome = updateOutcome{
		tool:    msg.tool,
		manager: m.updatePlan.Manager,
		elapsed: msg.elapsed,
		err:     msg.err,
		was:     m.versions[msg.tool].Installed,
	}
	if m.showsUpdateLog() {
		m.setHelpContent()
		m.helpViewport.GotoBottom()
	}
	if msg.err == nil {
		return
	}
	// The tail is the manager's own output and nothing else: the failure reason
	// rides msg.err, which is already the next field over.
	logx.Errorf("update failed: tool=%s manager=%s err=%v tail=%q",
		msg.tool, m.updatePlan.Manager, msg.err, tailLines(m.updateLog, 5))
}

func (m Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.tools)*2+1)
	// Seed the rate-limit signal up front; on warm-cache starts remote fetches
	// make no request, so this is the only observation that populates m.rate.
	cmds = append(cmds, fetchRateCmd())
	// Right after the rate seed and deliberately not last: the README seed at the
	// end of this function has to stay the final element of the batch
	// (TestInitFetchesReadmeForSelected executes it).
	if m.selfCheckEnabled() {
		cmds = append(cmds, selfCheckCmd())
	}
	for _, t := range m.tools {
		cmds = append(cmds, fetchInstalledCmd(t))
		if t.GitHub != "" {
			cmds = append(cmds, fetchRemoteCmd(t))
		}
	}
	if m.mode == modeSearch {
		cmds = append(cmds, textinput.Blink)
	}
	// Auto-fetch --help and changelog for initial selected tool. The --help probe
	// spawns a subprocess, so it only runs when [3] actually shows it: in the
	// default readme mode the capture would never be rendered (same reasoning as
	// the readme case in autoFetchCmdsForSelected).
	if mt, ok := m.selectedMeta(); ok && m.helpMode == helpModeHelp {
		cached := m.helpCache[mt.Name]
		if cached[helpModeHelp] == "" {
			cmds = append(cmds, fetchHelpCmd(mt.Name, helpModeHelp))
		}
	}
	if t, ok := m.selectedTool(); ok && t.GitHub != "" {
		cmds = append(cmds, fetchChangelogCmd(t.GitHub, t.Name))
		// Startup fires the panel-[3] sources directly rather than through
		// autoFetchCmdsForSelected, so the README needs its own seed — otherwise
		// the default readme panel shows a placeholder until the selection moves.
		// The mark reaches the model Bubble Tea keeps even though Init's receiver
		// is a copy: a map header points at the same table.
		m.markReadmeLoading(t.Name)
		cmds = append(cmds, fetchReadmeCmd(t.GitHub, t.Name))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer logx.Recover("model.Update")

	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.MouseMsg:
		return m.handleMouse(msg)

	case installedMsg:
		// A version merge can flip hasUpdate and reorder the grouped list, so
		// capture the selected tool by name before the merge and remap the
		// cursor onto its new row after — the selection follows the tool, not
		// the index. No auto-fetch (don't route through selectMeta).
		prev, hasSel := m.selectedMeta()
		info := m.versions[msg.toolName]
		info.Installed = msg.installed
		info.InstalledKnown = true
		info.InstalledPresent = msg.present
		m.versions[msg.toolName] = info
		if hasSel {
			m.metaSelected = m.indexOfMeta(prev.Name)
		}
		// Phase 2 of a finished update: this is the re-detect its success path
		// fired, and the only thing that knows whether the version actually moved
		// — a manager can exit zero having done nothing. Guarded on the outcome's
		// own name and on not being answered yet, since this handler fires for
		// every tracked tool at startup and again on any [r] refresh.
		//
		// Strictly *after* the cursor remap above: showsUpdateLog() asks
		// selectedMeta(), which reads metaSelected against filteredMeta() — and the
		// merge two lines up is exactly what flips hasUpdate and re-partitions that
		// list. Evaluated before the remap, the stale index names a different tool,
		// so the panel repaint is skipped for the tool that just updated (it leaves
		// the top partition) or fires for one merely sitting at its old row.
		if m.updateOutcome.tool == msg.toolName && !m.updateOutcome.verified {
			m.updateOutcome.verified = true
			m.updateOutcome.now = msg.installed
			m.updateOutcome.nowPresent = msg.present
			if m.showsUpdateLog() {
				m.setHelpContent()
				m.helpViewport.GotoBottom()
			}
		}
		m.setToolsContent()
		m.briefViewport.SetContent(m.renderCard())
		return m, nil

	case changelogMsg:
		if msg.toolName == m.changelogLoadingFor {
			m.changelogLoadingFor = ""
		}
		m.changelogData[msg.toolName] = msg
		m.briefViewport.SetContent(m.renderCard())
		return m, nil

	case readmeMsg:
		delete(m.readmeLoading, msg.toolName)
		// Known-content-wins merge, like the repo cards: a later failure (rate
		// limit, network) must not blank a README that was already fetched.
		if msg.err != nil && msg.content == "" {
			if prev, ok := m.readmeData[msg.toolName]; ok && prev.content != "" {
				prev.err = msg.err
				m.readmeData[msg.toolName] = prev
				return m, nil
			}
		}
		m.readmeData[msg.toolName] = msg
		// Repaint only when the arrival changes what is on screen: the selected
		// tool AND the displayed mode.
		if mt, ok := m.selectedMeta(); ok && mt.Name == msg.toolName && m.helpMode == helpModeReadme {
			m.setHelpContent()
		}
		return m, nil

	case remoteMsg:
		// Capture the selected tool by name before any merge: a fresh Latest can
		// flip hasUpdate and reorder the grouped list, so the cursor must follow
		// the tool, not the row index (remapped below, no auto-fetch).
		prev, hasSel := m.selectedMeta()
		// Merge the rate snapshot without clobbering a known value with an
		// unknown one (cache-hit remote fetches make no request, so carry
		// Known==false).
		if msg.rate.Known {
			m.rate = msg.rate
		}
		// Data is displayable when the fetch succeeded, or when it failed but still
		// carried usable cache values: a fresh tag from a partial fetch, or the
		// stale card kept on a total failure. In those cases render the data so
		// known tags/cards survive the outage.
		hasData := msg.latest != "" || msg.card.About != ""
		// Only a pass the version layer calls conclusive is an answer: it stamped
		// (or found) a fresh cache entry. A rate-limited pass may still have
		// rendered a stale card below, and a total or partial failure arrives
		// with a nil error — both must stay retryable.
		if msg.conclusive {
			if m.remoteAnswered == nil {
				m.remoteAnswered = make(map[string]bool)
			}
			m.remoteAnswered[msg.toolName] = true
		}
		switch {
		// The predicate is about data, not about the error class: what decides
		// whether the caches are written is whether the pass came back with
		// something to write. Gating the stale-data path on ErrRateLimited alone
		// meant a 401 or a 5xx dropped a card the pass had actually carried up
		// from the cache — the tool went blank for a failure it survived.
		case hasData || msg.err == nil:
			info := m.versions[msg.toolName]
			info.Latest = msg.latest
			m.versions[msg.toolName] = info
			if msg.repoStatus != "" {
				m.repoStatus[msg.toolName] = msg.repoStatus
			}
			m.repoCards[msg.toolName] = msg.card
			if hasSel {
				m.metaSelected = m.indexOfMeta(prev.Name)
			}
			m.setToolsContent()
			m.briefViewport.SetContent(m.renderCard())
		case msg.repoStatus == "rate-limited":
			// Rate-limited with no card to show: mark the tool so the card can
			// render "rate limited — press a" instead of a bare failure.
			m.repoStatus[msg.toolName] = "rate-limited"
			m.briefViewport.SetContent(m.renderCard())
		}
		// A refresh's repo pass has landed (success or error): clear the flag so
		// the card title drops the "refreshing … data" status back to name+about.
		// This also halts the tick loop.
		if msg.toolName == m.refreshingFor {
			m.refreshingFor = ""
			m.briefViewport.SetContent(m.renderCard())
			// [r] used to answer nothing at all: success, a rate limit, a 401, a
			// timeout and a dropped connection were one gesture — the spinner
			// turns, the card does not change. A failed pass says why; a pass
			// that fetched something stays silent, because the repainted card IS
			// the answer.
			//
			// The predicate is the error and deliberately not !conclusive, which
			// is a broader thing: it is also false when the version layer refused
			// the ref outright (an unsupported or spoofed host — a bare RepoData
			// with a nil Err), which would report a network failure that never
			// happened, and on a partial pass that fetched a new tag and lost
			// only the repo card — where the bar would contradict a card the user
			// just watched update. Every path that actually failed to fetch now
			// carries a named error (see version.pickFetchErr).
			if msg.err != nil {
				return m, m.setStatus(refreshFailedStatus(msg.err))
			}
		}
		return m, nil

	case rateMsg:
		// Non-clobber merge: only a successful, Known snapshot updates m.rate.
		if msg.err == nil && msg.rate.Known {
			m.rate = msg.rate
		}
		return m, nil

	case selfCheckMsg:
		// The startup self-check answered. A failure (rate limit, network, 5xx)
		// leaves the state at selfNone: no banner, the rest of the app is
		// unaffected, and the record was already written inside version (doGH /
		// classifyStatus) — selfCheckCmd itself logs nothing, see its comment.
		// An empty tag is the conclusive "no release published" answer and means
		// no banner either. Only a state that has not been acted on is written —
		// in either direction: neither the "not newer" path nor a newer tag may
		// walk back a dismissal, a finished update or a pending restart, and the
		// check answers once per session anyway.
		if msg.err != nil || msg.latest == "" || m.selfState != selfNone {
			return m, nil
		}
		if version.IsNewer(m.appVersion, msg.latest) {
			m.selfLatest = msg.latest
			m.selfState = selfOffered
		}
		return m, nil

	case spinner.TickMsg:
		// Animate while a refresh ([r]) or an update (enter in [2]) is in flight;
		// once both refreshingFor and updatingFor are cleared (by the remoteMsg /
		// updateDoneMsg handlers) the loop stops rescheduling itself.
		if m.refreshingFor == "" && m.updatingFor == "" {
			return m, nil
		}
		m.spinner, cmd = m.spinner.Update(msg)
		m.briefViewport.SetContent(m.renderCard())
		return m, cmd

	case tokenValidatedMsg:
		// Validation result for a candidate token entered in the overlay. On
		// failure nothing is written to disk; the inline message stays visible
		// and the input remains open for a retry.
		if msg.err != nil {
			m.tokenError = "token invalid"
			return m, nil
		}
		if err := version.SetToken(msg.token); err != nil {
			m.tokenError = "could not save token"
			return m, nil
		}
		// The result may land after the user already left the token input via
		// esc; only a still-active modeTokenInput falls back to the overlay.
		if m.mode == modeTokenInput {
			m.mode = modeAPIStatus
		}
		m.tokenInput.Blur()
		m.tokenInput.SetValue("")
		// The token validated and is on disk, but SetToken writes the config file
		// and effectiveToken reads that only when GITHUB_TOKEN is empty — so under
		// env precedence what was just accepted is not what goes on the wire. Say
		// so, and stop here.
		//
		// Everything below assumes the opposite. msg.rate is the candidate's
		// snapshot, so the gauge would claim a 5000 limit the session does not
		// have; and the fan-out would spend a three-request pass per tool at the
		// anonymous 60/h ceiling — with a few dozen tools that is the whole hour,
		// burnt by the one gesture the overlay offers as the way out of a degraded
		// session. Failing to recover is survivable; making it worse is not.
		if version.TokenSource() == "env" {
			m.tokenError = "saved — GITHUB_TOKEN still takes precedence"
			return m, nil
		}
		m.tokenError = ""
		if msg.rate.Known {
			m.rate = msg.rate
		}
		// A rate-limited README is a session-cached negative that needsReadme
		// treats as answered — drop those entries so the backfill can actually
		// retry them, which is what the "rate limited — press a" placeholder
		// promises. Fetched content and 404s stay.
		for name, data := range m.readmeData {
			if data.content == "" && errors.Is(data.err, version.ErrRateLimited) {
				delete(m.readmeData, name)
			}
		}
		// Refetch every tool with a repo, not just the selected one. The predicate
		// is Init's — `t.GitHub != ""` — and deliberately NOT needsRemote, which
		// returns false as soon as a card exists with a non-empty Latest: that is
		// true for precisely the tools that rendered stale-but-present data
		// through the degraded window, the ones this recovery exists for. Clearing
		// remoteAnswered first is the other half; a tool the layer settled under
		// the old credential is not settled under the new one.
		//
		// This does spend quota — a real three-request pass per tool, since the
		// degraded window left every entry stale (an inconclusive pass never
		// stamps CheckedAt) and that is exactly what makes the refetch worth
		// doing. The new token is what pays for it: the limit is 5000/h from here
		// on, not the 60 the session was running at.
		clear(m.remoteAnswered)
		// autoFetchCmdsForSelected refreshes the selected tool's other sources
		// (changelog, installed, README) and dispatches its repo pass too, but
		// only when needsRemote says so — which after the clear above is true for
		// a cold-cache tool, the harsher of the two degraded shapes. So decide
		// against the same marker state it will read, and let the loop skip
		// whatever it already queued: two passes for one repo spend twice the
		// quota on it and race two updateCacheEntry writes for the same entry.
		queued := ""
		if t, ok := m.selectedTool(); ok && m.needsRemote(t) {
			queued = t.Name
		}
		cmds := []tea.Cmd{m.autoFetchCmdsForSelected()}
		for _, t := range m.tools {
			if t.GitHub != "" && t.Name != queued {
				cmds = append(cmds, fetchRemoteCmd(t))
			}
		}
		return m, tea.Batch(cmds...)

	case openURLMsg:
		if msg.err != nil {
			return m, m.setStatus(msg.err.Error())
		}
		return m, nil

	case launchDoneMsg:
		// Tab-open adapter finished. Clear the one-launch-at-a-time guard
		// first, so the fallback (or the user's retry) can dispatch again.
		if m.launchingFor == msg.toolName {
			m.launchingFor = ""
		}
		// On failure, auto-fall back to running the tool in the current
		// window — the tool always launches, the status bar explains the
		// degradation. Not logged: the fallback makes this a degraded path,
		// not a keepkit malfunction.
		if msg.err != nil {
			// The result can arrive up to launchTimeout after enter (osascript
			// blocked on the macOS Automation dialog). tea.ExecProcess seizes
			// the terminal, so the auto-fallback fires only from the base
			// state: under an open editor/overlay/search it would hijack the
			// screen and send the user's keystrokes to the spawned shell —
			// there the fallback is deferred instead: flushPendingLaunch
			// dispatches it (same statusMsg, no adapter re-run) on the
			// keystroke that returns the mode to modeNormal.
			if m.mode != modeNormal {
				m.pendingLaunchName = msg.toolName
				m.pendingLaunchCommand = msg.command
				return m, nil
			}
			return m, tea.Batch(
				m.setStatus(launchFallbackStatus(msg.toolName)),
				execToolCmd(msg.toolName, msg.command),
			)
		}
		// Mode-neutral wording: Terminal.app and tmux open a window, not a tab.
		return m, m.setStatus("launched " + msg.toolName)

	case execDoneMsg:
		// The tool ran in the current window and keepkit resumed. A non-zero
		// exit belongs to the tool, not keepkit — statusMsg only, no logx.
		// Exception in wording only: the shell's own "command not found"
		// exit (notFoundExit) means the tool never ran, so the message says
		// "not installed" instead of surfacing a cryptic exit status.
		if msg.err != nil {
			if notFoundExit(msg.err) {
				return m, m.setStatus(msg.toolName + " not found — is it installed?")
			}
			return m, m.setStatus(msg.toolName + " exited: " + msg.err.Error())
		}
		return m, nil

	case helpOutputMsg:
		// Only the named tool's result retires the loading marker: a stale
		// fetch for a previously highlighted tool must not clear the flag
		// while the currently selected tool's fetch is still in flight.
		if m.helpLoadingFor == msg.toolName {
			m.helpLoadingFor = ""
		}
		cached := m.helpCache[msg.toolName]
		if msg.err == nil && msg.output != "" {
			cached[msg.mode] = msg.output
		} else if msg.mode == helpModeHelp {
			cached[msg.mode] = "No --help output for " + msg.toolName + ".\nPress [M] for the man page."
		} else {
			cached[msg.mode] = "No man page for " + msg.toolName + ".\nPress [H] for --help."
		}
		m.helpCache[msg.toolName] = cached
		// Recompute only when the arrival changes the text on screen: the
		// selected tool AND the displayed mode. A late fetch for the hidden
		// mode (slow --help landing after the user switched to [m]) must not
		// reset an active spotlight cursor over unchanged visible text.
		if mt, ok := m.selectedMeta(); ok && mt.Name == msg.toolName && msg.mode == m.helpMode {
			m.setHelpContent()
		}
		return m, nil

	case updateDetectedMsg:
		// Detection result for an enter in [2] (tool) or a [U] (keepkit itself)
		// press; the relevance gate differs per path, see acceptsUpdateDetect. A
		// dropped result changes nothing — for the self path the banner stays at
		// selfOffered, where [U] is a retry. ErrUnknownManager is not a dead-end
		// dialog either, just a hint: the tool path can point at update_cmd, while
		// keepkit's own manual route is whatever installed it. On success, stash
		// the plan together with the name it was detected for (which the confirm
		// dialog and its status bar read) and open the confirm dialog.
		if !m.acceptsUpdateDetect(msg) {
			return m, nil
		}
		if msg.err != nil {
			if errors.Is(msg.err, updater.ErrUnknownManager) {
				// The wording branches on what the target *is*, not on which key
				// asked: the same failure of the same binary must read the same
				// way from [U] and from enter in [2] on a tracked keepkit row (see
				// isSelfUpdate). msg.self keeps the one meaning only it has — "no
				// selection to match" — in acceptsUpdateDetect.
				if m.isSelfUpdate(msg.tool) {
					return m, m.setStatus("no known updater for " + selfToolName + " — update manually")
				}
				return m, m.setStatus("no known updater for " + msg.tool + " — set update_cmd or [o] releases")
			}
			return m, m.setStatus("update detect failed: " + msg.err.Error())
		}
		m.updatePlan = msg.plan
		m.updateTarget = msg.tool
		m.mode = modeConfirmUpdate
		return m, nil

	case updateChunkMsg:
		// Fold one segment of the running update's output into the live buffer.
		// A '\r'-terminated segment (progress bar) replaces the last line rather
		// than appending, so a progress bar renders as one updating line. Only
		// the active session's buffer is touched, but we always re-subscribe so
		// the channel keeps draining to EOF (which yields updateDoneMsg).
		if msg.tool == m.updateLogFor {
			line := cleanTerminalOutput(msg.line)
			if msg.replace && len(m.updateLog) > 0 {
				m.updateLog[len(m.updateLog)-1] = line
			} else {
				m.updateLog = append(m.updateLog, line)
			}
			// Cap to the tail: the final lines (install result, errors) matter.
			if len(m.updateLog) > updateLogMaxLines {
				m.updateLog = m.updateLog[len(m.updateLog)-updateLogMaxLines:]
			}
			// Repaint [3] and autoscroll only while the log is the panel's
			// content; a chunk for a backgrounded tool update leaves the visible
			// panel (another tool's help) untouched, while a self-update's log
			// owns the panel regardless of the selection.
			if m.showsUpdateLog() {
				m.helpViewport.SetContent(m.renderHelpContent())
				m.helpViewport.GotoBottom()
			}
		}
		return m, waitForChunkCmd(msg.tool, msg.ch)

	case updateDoneMsg:
		// The update subprocess finished. Clear the guard regardless of outcome
		// so a new update can start and the spinner.TickMsg loop stops
		// rescheduling itself; the live log in [3] survives until the next
		// update begins. Re-render the card so its title drops the spinner.
		m.updatingFor = ""
		// One record for every outcome, ahead of every branch below: the block in
		// [3] is what tells the user the update ended at all, and four call sites
		// for it would be four chances for the self path and the tool path to say
		// different things about the same event. It reaches no card and fires no
		// fetch, so even the untracked-mid-update branch can pass through it — a
		// failed update is worth logging whether or not its tool is still tracked.
		m.recordUpdateOutcome(msg)
		// keepkit's own update is settled first, ahead of the toolByName lookup:
		// that early return drops the message whenever the tool is not tracked,
		// which for keepkit is the normal case — the update would finish silently
		// and the [U] restart offer would never appear. The discriminator is
		// isSelfUpdate, not the bare name: an update of keepkit is a self-update
		// whichever key started it (so enter in [2] on a tracked keepkit row ends
		// here too and offers the same restart instead of leaving a banner that
		// contradicts the card), but only on a build where the feature is live at
		// all — on a dev build the same keypress falls through to the plain tool
		// path below.
		if m.isSelfUpdate(msg.tool) {
			m.briefViewport.SetContent(m.renderCard())
			if msg.err != nil {
				// selfState is deliberately left exactly as it was found. Whatever
				// the banner showed before the update it shows again the moment
				// updatingFor clears: the offer, where [U] is the retry, or the
				// compact cell when [X] folded it mid-update, or nothing at all when
				// the check never offered anything. Forcing selfOffered here instead
				// announced "keepkit  available" with no version behind it whenever
				// the update started from selfNone (a rate-limited or offline startup
				// check, a check that said "not newer", or enter in [2] on a tracked
				// row), and it replaced a pending [U] restart from an earlier
				// successful update with that same empty banner.
				return m, m.setStatus("update failed — see [3]")
			}
			m.selfState = selfUpdated
			statusCmd := m.setStatus("updated " + selfToolName)
			// The new binary is on disk, but this process is still the old one —
			// only a tracked keepkit has a card and a ↑ marker for the re-detect
			// to update.
			if t, tracked := m.toolByName(msg.tool); tracked {
				return m, tea.Batch(statusCmd, fetchInstalledCmd(t))
			}
			return m, statusCmd
		}
		// A tool untracked mid-update is no longer in m.tools: just drop the
		// guard — no re-fetch, no statusMsg reaching into a card that is gone.
		t, ok := m.toolByName(msg.tool)
		if !ok {
			m.briefViewport.SetContent(m.renderCard())
			return m, nil
		}
		if msg.err != nil {
			statusCmd := m.setStatus("update failed — see [3]")
			m.briefViewport.SetContent(m.renderCard())
			return m, statusCmd
		}
		// Success: re-detect the installed version. The installedMsg merge flips
		// hasUpdate off, moves the tool out of the update group and remaps the
		// cursor by name, so no extra bookkeeping is needed here.
		statusCmd := m.setStatus("updated " + msg.tool)
		m.briefViewport.SetContent(m.renderCard())
		return m, tea.Batch(statusCmd, fetchInstalledCmd(t))

	case statusExpiredMsg:
		// Retire a transient status only if it is still the current one: a stale
		// timer from a superseded message must not clear the newer message. The
		// blanket tea.KeyMsg reset stays; a timer arriving after it is a no-op.
		if msg.seq == m.statusSeq {
			m.statusMsg = ""
		}
		return m, nil

	case tea.WindowSizeMsg:
		prevWrapW := m.helpWrapWidth()
		m.width = msg.Width
		m.height = msg.Height
		m.toolsW, m.briefW, m.helpW = m.calcPanelWidths()
		vpH := m.calcListHeight()

		if !m.ready {
			// Viewports are 1 col narrower than their panel to leave a gutter
			// for the scrollbar rendered by withScrollbar.
			m.toolsViewport = viewport.New(m.toolsW-1, vpH)
			m.briefViewport = viewport.New(m.briefW-1, vpH)
			m.helpViewport = viewport.New(m.helpW-1, vpH)
			// Zero the default pager keymap on all three viewports: every
			// keyboard scroll binding lives explicitly in Update()'s switch, so
			// the hidden defaults (d/u/f/b/space/h/l scrolling uncaught keys
			// inconsistently) must never come back. Wheel scrolling rides
			// MouseWheelEnabled — a separate field, still true — so it stays on.
			m.toolsViewport.KeyMap = viewport.KeyMap{}
			m.briefViewport.KeyMap = viewport.KeyMap{}
			m.helpViewport.KeyMap = viewport.KeyMap{}
			m.setHelpContent()
			m.ready = true
		} else {
			m.toolsViewport.Width = m.toolsW - 1
			m.toolsViewport.Height = vpH
			m.briefViewport.Width = m.briefW - 1
			m.briefViewport.Height = vpH
			m.helpViewport.Width = m.helpW - 1
			m.helpViewport.Height = vpH
			// Re-wrap and recompute the entry index (resetting the cursor —
			// stale wrapped-line ranges would dim the wrong lines) only when
			// the wrap width actually changed: a height-only resize (tmux bar
			// toggle, vertical resize) leaves every entry range valid, so an
			// active spotlight survives it.
			if m.helpWrapWidth() != prevWrapW {
				m.setHelpContent()
			}
		}
		// Through setToolsContent, not a bare SetContent: a resize repaints the
		// list, so the line maps must be rebuilt with it or they would describe
		// the pre-resize content (wrong scroll, out-of-range click mapping).
		m.setToolsContent()
		m.briefViewport.SetContent(m.renderCard())
		return m, nil

	case tea.KeyMsg:
		m.statusMsg = ""

		// Every modal return funnels through flushPendingLaunch: the keystroke
		// that brings the mode back to modeNormal is the single point where a
		// deferred exec fallback (launchDoneMsg mode gate) can safely fire —
		// see the pendingLaunchName field doc. While the mode stays open the
		// wrapper is a no-op.
		switch m.mode {
		case modeEditNote:
			return flushPendingLaunch(m.updateNoteEdit(msg))
		case modeEditTags:
			return flushPendingLaunch(m.updateTagsEdit(msg))
		case modeTrack:
			return flushPendingLaunch(m.updateTrackInput(msg))
		case modeConfirmUntrack:
			return flushPendingLaunch(m.updateUntrackConfirm(msg))
		case modeRename:
			return flushPendingLaunch(m.updateRenameInput(msg))
		case modeRunInput:
			return flushPendingLaunch(m.updateRunInput(msg))
		case modeConfirmUpdate:
			return flushPendingLaunch(m.updateConfirmUpdate(msg))
		case modeAPIStatus, modeTokenInput:
			return flushPendingLaunch(m.updateAPIStatus(msg))
		case modeHotkeys:
			return flushPendingLaunch(m.updateHotkeys(msg))
		}

		if m.mode == modeSearch {
			switch msg.String() {
			case "esc":
				// Rollback: restore the cursor to the tool selected before the
				// search started (fallback 0 when it was untracked mid-search).
				// selectMeta refreshes the help viewport too — arrow moves may
				// have loaded another tool's help mid-search.
				m.mode = modeNormal
				m.search.SetValue("")
				m.search.Blur()
				prev := m.searchPrevName
				m.searchPrevName = ""
				// selectMeta before the flush: it mutates m, and Go copies m
				// into the call before evaluating a second argument.
				cmd = m.selectMeta(m.indexOfMeta(prev))
				return flushPendingLaunch(m, cmd)
			case "enter":
				// Commit: accept the highlighted match. With no matches the key
				// is a no-op and search stays open.
				mt, ok := m.selectedMeta()
				if !ok {
					return m, nil
				}
				m.mode = modeNormal
				m.search.SetValue("")
				m.search.Blur()
				m.searchPrevName = ""
				m.focus = focusBrief
				// The filter is gone once the mode is normal, so remap the
				// cursor onto the full list by name. selectMeta before the
				// flush: it mutates m, and Go copies m into the call before
				// evaluating a second argument.
				cmd = m.selectMeta(m.indexOfMeta(mt.Name))
				return flushPendingLaunch(m, cmd)
			case "up", "down":
				// Move the highlight through the filtered list (wrap-around,
				// j/k parity). Never forwarded to the textinput, so the query
				// text is untouched; with zero matches the key is consumed.
				if n := len(m.filteredMeta()); n > 0 {
					if msg.String() == "down" {
						return m, m.selectMeta((m.metaSelected + 1) % n)
					}
					return m, m.selectMeta((m.metaSelected - 1 + n) % n)
				}
				return m, nil
			default:
				prevQuery := m.search.Value()
				m.search, cmd = m.search.Update(msg)
				if m.search.Value() != prevQuery {
					// The filter changed: reset the highlight to the first
					// match (a stale index could fall out of a narrower
					// filter's range). Pure cursor movement (left/right/
					// home/end) keeps a user-moved highlight.
					m.metaSelected = 0
					m.setToolsContent()
					m.briefViewport.GotoTop()
					m.briefViewport.SetContent(m.renderCard())
					// Re-sync the help panel like selectMeta does: a prior
					// arrow move may have left it on "Loading..." for a tool
					// this reset just unselected, and the stale fetch landing
					// later won't repaint an unselected tool's panel. It must go
					// through setHelpContent, not a bare SetContent: the readme
					// branch of renderHelpContent serves helpBase, which only
					// setHelpContent re-renders for the new selection —
					// otherwise [3] shows the previous tool's README under the
					// new tool's name. No fetch is fired here (one per keystroke
					// would spend the quota on rows merely typed past); the
					// commit/rollback exits go through selectMeta and pick it up.
					m.setHelpContent()
					m.helpViewport.GotoTop()
				}
				return m, cmd
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		// esc walks left panel by panel and quits off the left edge; left/right
		// walk without wrapping. The targets are named rather than computed
		// from m.focus so the constants stay reorderable.
		case "esc":
			switch m.focus {
			case focusHelp:
				// First esc only exits the navigation cursor (spotlight off,
				// scroll position kept); the next esc walks focus left as
				// usual.
				if m.clearHelpNav() {
					return m, nil
				}
				m.setFocus(focusBrief)
			case focusBrief:
				m.setFocus(focusTools)
			default:
				return m, tea.Quit
			}

		case "right", "l":
			switch m.focus {
			case focusTools:
				m.setFocus(focusBrief)
			case focusBrief:
				m.setFocus(focusHelp)
			}

		case "left":
			switch m.focus {
			case focusHelp:
				m.setFocus(focusBrief)
			case focusBrief:
				m.setFocus(focusTools)
			}

		// The panel titles ([1] Tools / [2] Brief / [3] Help) document these
		// hotkeys; unlike the arrows they jump directly, e.g. tools → help.
		case "1":
			m.setFocus(focusTools)

		case "2":
			m.setFocus(focusBrief)

		case "3":
			m.setFocus(focusHelp)

		case "j", "down":
			if m.focus == focusTools {
				// Wrap around to the top when moving past the last tool.
				if n := len(m.filteredMeta()); n > 0 {
					return m, m.selectMeta((m.metaSelected + 1) % n)
				}
			} else {
				// In [3] with a navigable entry index, j drives the spotlight
				// cursor; the arrows deliberately keep their line scroll so
				// prose between and after entries stays reachable without a
				// mouse (PgUp/PgDn/g/G/wheel stay pure scroll too). With no
				// entries (update log, placeholders, prose) j scrolls as
				// before.
				if msg.String() == "j" && m.focus == focusHelp && len(m.helpEntries) > 0 {
					m.helpNavStep(1)
					return m, nil
				}
				// Unified scroll: j and ↓ both step 3 lines.
				if m.focus == focusBrief {
					m.briefViewport.ScrollDown(3)
				} else if m.focus == focusHelp {
					m.helpViewport.ScrollDown(3)
				}
				return m, nil
			}

		case "k", "up":
			if m.focus == focusTools {
				// Wrap around to the bottom when moving above the first tool.
				if n := len(m.filteredMeta()); n > 0 {
					return m, m.selectMeta((m.metaSelected - 1 + n) % n)
				}
			} else {
				// See "j": only the letter key navigates, arrows keep line
				// scroll.
				if msg.String() == "k" && m.focus == focusHelp && len(m.helpEntries) > 0 {
					m.helpNavStep(-1)
					return m, nil
				}
				// Unified scroll: k and ↑ both step 3 lines.
				if m.focus == focusBrief {
					m.briefViewport.ScrollUp(3)
				} else if m.focus == focusHelp {
					m.helpViewport.ScrollUp(3)
				}
				return m, nil
			}

		case "pgup", "ctrl+b":
			if m.focus == focusTools {
				// A page is a page of *screen lines*: under grouping the
				// headers between rows make a line step and a tool step
				// different sizes (see toolNearLine).
				step := max(m.toolsViewport.Height, 1)
				return m, m.selectMeta(m.toolNearLine(m.selectedLine()-step, -1))
			}
			if m.focus == focusBrief {
				m.briefViewport.PageUp()
			} else if m.focus == focusHelp {
				m.helpViewport.PageUp()
			}
			return m, nil

		case "pgdown", "ctrl+f", " ":
			if m.focus == focusTools {
				// space is the list-view toggle here, not a page-down —
				// ctrl+f/PgDn already page the list.
				if msg.String() == " " {
					return m, m.toggleGroupByTag()
				}
				step := max(m.toolsViewport.Height, 1)
				return m, m.selectMeta(m.toolNearLine(m.selectedLine()+step, 1))
			}
			if m.focus == focusBrief {
				m.briefViewport.PageDown()
			} else if m.focus == focusHelp {
				m.helpViewport.PageDown()
			}
			return m, nil

		case "ctrl+d":
			if m.focus == focusTools {
				step := max(m.toolsViewport.Height/2, 1)
				return m, m.selectMeta(m.toolNearLine(m.selectedLine()+step, 1))
			}
			if m.focus == focusBrief {
				m.briefViewport.HalfPageDown()
			} else if m.focus == focusHelp {
				m.helpViewport.HalfPageDown()
			}
			return m, nil

		case "ctrl+u":
			if m.focus == focusTools {
				step := max(m.toolsViewport.Height/2, 1)
				return m, m.selectMeta(m.toolNearLine(m.selectedLine()-step, -1))
			}
			if m.focus == focusBrief {
				m.briefViewport.HalfPageUp()
			} else if m.focus == focusHelp {
				m.helpViewport.HalfPageUp()
			}
			return m, nil

		case "g":
			if m.focus == focusTools {
				if len(m.filteredMeta()) > 0 {
					return m, m.selectMeta(0)
				}
				return m, nil
			}
			if m.focus == focusBrief {
				m.briefViewport.GotoTop()
			} else if m.focus == focusHelp {
				m.helpViewport.GotoTop()
			}

		case "G":
			if m.focus == focusTools {
				if n := len(m.filteredMeta()); n > 0 {
					return m, m.selectMeta(n - 1)
				}
				return m, nil
			}
			if m.focus == focusBrief {
				m.briefViewport.GotoBottom()
			} else if m.focus == focusHelp {
				m.helpViewport.GotoBottom()
			}

		case "enter":
			// enter is the panel's primary action, and each panel has exactly
			// one: in [1] run the selected tool, in [2] install the release the
			// card is showing. Same key, because in both panels it is the thing
			// the user came to that panel to do.
			if m.focus == focusBrief {
				// Assigned first: startToolUpdate mutates m (a status message),
				// and Go copies m into the return before evaluating the call.
				cmd = m.startToolUpdate()
				return m, cmd
			}
			// Run the selected tool: open the one-line command prompt,
			// prefilled with the last command dispatched for it this session
			// (else the tool name), cursor at the end. An empty list is a no-op.
			if m.focus == focusTools {
				if mt, ok := m.selectedMeta(); ok {
					m.mode = modeRunInput
					prefill := m.lastRun[mt.Name]
					if prefill == "" {
						prefill = mt.Name
					}
					m.runInput.SetValue(prefill)
					m.runInput.CursorEnd()
					m.runInput.Focus()
					return m, textinput.Blink
				}
			}

		case "/":
			// [1]-only: `/` is the tool-list filter and nothing else. The guard
			// is what keeps a press in [2]/[3] from falling through into the
			// entry below — it would open modeSearch over an unfocused list.
			// (It used to open the help/man search, a feature since removed.)
			if m.focus != focusTools {
				return m, nil
			}
			// Remember the current selection so esc can roll back to it.
			m.searchPrevName = ""
			if mt, ok := m.selectedMeta(); ok {
				m.searchPrevName = mt.Name
			}
			m.mode = modeSearch
			m.search.Focus()
			return m, textinput.Blink

		// The three sources of panel [3] are capitals as a set, so none of them
		// can collide with a lowercase verb — m is the global rename now and r
		// stays [2]'s refresh. They also fire uniformly from [2] and from [3],
		// unlike the old h/m/r trio where readme was reachable only from [3]
		// while the [3] title advertised it from both, so pressing it in [2]
		// silently ran a network refresh instead.
		case "H":
			if m.focus == focusBrief || m.focus == focusHelp {
				m.focus = focusHelp
				return m, m.switchHelpMode(helpModeHelp)
			}

		case "M":
			if m.focus == focusBrief || m.focus == focusHelp {
				m.focus = focusHelp
				return m, m.switchHelpMode(helpModeMan)
			}

		case "R":
			if m.focus == focusBrief || m.focus == focusHelp {
				m.focus = focusHelp
				return m, m.switchHelpMode(helpModeReadme)
			}

		case "e":
			if m.focus == focusBrief {
				if mt, ok := m.selectedMeta(); ok {
					m.mode = modeEditNote
					m.noteInput.SetValue(mt.Note)
					m.noteInput.Focus()
					m.briefViewport.SetContent(m.renderCard())
					return m, textinput.Blink
				}
			}

		// t / u / m are the tracker's own verbs and fire in every focus — three
		// of the six keys the status bar now advertises as global (with a, ?
		// and q). They used to be [1]-only because each collided with a [2]
		// action (t tags, u update, r refresh); the redesign moved those onto
		// keys of their own (# tags, enter update) and freed all three to mean
		// one thing everywhere, which is what lets the bar carry a single
		// focus-independent list instead of three per-focus ones.
		case "t":
			m.mode = modeTrack
			m.trackInput.SetValue("")
			m.trackInput.Focus()
			return m, textinput.Blink

		case "u":
			if mt, ok := m.selectedMeta(); ok {
				m.mode = modeConfirmUntrack
				m.untrackTarget = mt.Name
				return m, nil
			}

		// Rename is m, not R: R is panel [3]'s readme source now, and the
		// tracker's verbs are the lowercase set.
		case "m":
			if mt, ok := m.selectedMeta(); ok {
				m.mode = modeRename
				m.nameInput.SetValue(mt.Name)
				m.nameInput.Focus()
				return m, textinput.Blink
			}

		case "#":
			// Edit the tag. Its own key now: t is the global track verb, and a
			// tag editor reachable only from the card is where the tag is shown.
			if m.focus == focusBrief {
				if mt, ok := m.selectedMeta(); ok {
					m.mode = modeEditTags
					m.tagsInput.SetValue(tagOf(mt))
					m.tagsInput.Focus()
					m.briefViewport.SetContent(m.renderCard())
					return m, textinput.Blink
				}
			}

		// r is [2]'s refresh and nothing else. It used to double as [3]'s readme
		// switch, which made the same key mean "spend three requests" or "swap
		// the panel's source" depending on a focus the [3] title did not
		// mention; that source moved to R with the rest of the trio.
		case "r":
			if m.focus == focusBrief {
				if t, ok := m.selectedTool(); ok {
					return m, m.refreshSelectedCmd(t)
				}
			}

		case "o":
			if m.focus == focusBrief {
				if t, ok := m.selectedTool(); ok {
					if t.GitHub == "" {
						return m, m.setStatus("no repo for " + t.Name)
					}
					return m, openURLCmd("https://" + t.GitHub)
				}
			}

		case "c":
			if m.focus == focusBrief {
				if t, ok := m.selectedTool(); ok {
					if t.GitHub == "" {
						return m, m.setStatus("no repo for " + t.Name)
					}
					return m, openURLCmd("https://" + t.GitHub + "/releases")
				}
			}

		case "s":
			if m.focus == focusBrief {
				if mt, ok := m.selectedMeta(); ok {
					mt.Status = loader.NextStatus(mt.Status)
					m.meta = loader.UpsertMeta(m.meta, mt)
					loader.SaveMeta(m.meta) //nolint:errcheck
					m.briefViewport.SetContent(m.renderCard())
					return m, nil
				}
			}

		case "a":
			// Open the API-status overlay and refresh the rate numbers on demand
			// (GET /rate_limit does not spend quota). Reached only in modeNormal —
			// every other mode's handler returns earlier.
			m.mode = modeAPIStatus
			m.tokenError = ""
			return m, fetchRateCmd()

		case "?":
			// Open the static hotkeys-help overlay. Reached only in modeNormal —
			// every input mode's handler returns earlier, so ? typed into a
			// search/note/token field stays literal text.
			m.mode = modeHotkeys
			return m, nil

		case "U":
			// The self-update banner's action key, live in every focus and in
			// both the full banner and its collapsed cell. Reached only in
			// modeNormal — like a, every input mode's handler returns earlier,
			// so a capital U typed into a search/note/token field stays text.
			return m, m.selfUpdateKey()

		case "X":
			// Fold the banner into its compact right-group cell. Session-only —
			// nothing is written to disk, and [U] stays reachable there.
			switch m.selfState {
			case selfOffered:
				m.selfState = selfDismissed
			case selfUpdated:
				m.selfState = selfUpdatedLater
			case selfNone, selfDismissed, selfUpdatedLater:
				// Nothing to fold: no banner, or it is already the compact cell.
			default:
				logx.Errorf("self-update: [X] on unhandled selfState %d", m.selfState)
			}
			return m, nil
		}
		// No keyboard fall-through into viewport.Update: every scroll key is bound
		// explicitly above, and the default pager keymap was zeroed (see
		// WindowSizeMsg). An uncaught key is a deliberate no-op.
	}

	return m, cmd
}

func (m Model) selectedMeta() (loader.ToolMeta, bool) {
	filtered := m.filteredMeta()
	if m.metaSelected < 0 || m.metaSelected >= len(filtered) {
		return loader.ToolMeta{}, false
	}
	return filtered[m.metaSelected], true
}

func (m Model) selectedTool() (loader.Tool, bool) {
	mt, ok := m.selectedMeta()
	if !ok {
		return loader.Tool{}, false
	}
	return m.toolByName(mt.Name)
}

// toolByName returns the tracked tool with the given name (and false when it is
// no longer tracked — e.g. untracked mid-update). It backs updateDoneMsg's
// re-fetch, which fires for a tool that need not be the selected one.
func (m Model) toolByName(name string) (loader.Tool, bool) {
	for _, t := range m.tools {
		if t.Name == name {
			return t, true
		}
	}
	return loader.Tool{}, false
}

// tailLines joins the last n entries of the update log into a single
// space-separated string for the failure log line; fewer than n lines are all
// returned. Kept small on purpose — the log record wants a hint, not the buffer.
func tailLines(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " ")
}

// searchQuery returns the normalized (lowercase, trimmed) live query, or ""
// when the tool-list search is not active — the empty string doubles as the
// "no filter" signal for searchMatches and the list renderer.
func (m Model) searchQuery() string {
	if m.mode != modeSearch {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(m.search.Value()))
}

// searchMatch pairs a tool that passed the search filter with how the query
// matched it: byTagOnly flags rows whose name did not match (a tag did), and
// tag carries the first matching tag of such a row so the renderer can show
// what earned it a place in the list (empty on name matches).
type searchMatch struct {
	meta      loader.ToolMeta
	byTagOnly bool
	tag       string
}

// searchMatches is the search predicate: a tool matches when its name OR any
// of its tags contains the query (case-insensitive substring). With no active
// query every tool passes unmarked. The result is then stable-partitioned so
// tools with an available update float to the top of the list (meta.yaml order
// preserved inside each group) — this ordering is the single projection point
// every consumer (renderer, filteredMeta, selection/mouse row mapping) sees, so
// they can never desync. It is display-only: m.meta on disk is never reordered.
func (m Model) searchMatches() []searchMatch {
	query := m.searchQuery()
	out := make([]searchMatch, 0, len(m.meta))
	for _, mt := range m.meta {
		if query == "" {
			out = append(out, searchMatch{meta: mt})
			continue
		}
		nameHit := strings.Contains(strings.ToLower(mt.Name), query)
		var tag string
		if !nameHit {
			if tag = matchingTag(mt.Tags, query); tag == "" {
				continue
			}
		}
		out = append(out, searchMatch{meta: mt, byTagOnly: !nameHit, tag: tag})
	}

	// Tag view: the two orderings are exclusive — grouping wins, so the tag
	// sections stay contiguous (the ` ↑` marker still renders per row).
	if m.grouped() {
		return m.groupMatchesByTag(out)
	}

	// Stable two-pass partition: updatable rows first, the rest after, each
	// group keeping its relative (meta.yaml) order. The predicate is exactly
	// hasUpdate — the same one that renders the ` ↑` suffix, so the group and
	// the suffix can never disagree.
	grouped := make([]searchMatch, 0, len(out))
	for _, sm := range out {
		if m.hasUpdate(sm.meta.Name) {
			grouped = append(grouped, sm)
		}
	}
	for _, sm := range out {
		if !m.hasUpdate(sm.meta.Name) {
			grouped = append(grouped, sm)
		}
	}
	return grouped
}

// setStatus shows a transient status message and returns the tick that retires
// it after statusMsgTTL. Every transient assignment site routes through here (in
// flight statuses like "launching …" stay direct assignments — they must not
// vanish mid-flight). The returned Cmd must be batched into whatever the caller
// already returns. The closure captures the sequence number by value and reads
// no model state: reading m.statusSeq at fire time would both defeat the
// generation guard and race Update's mutations under -race (the Cmd runs in a
// goroutine).
func (m *Model) setStatus(s string) tea.Cmd {
	m.statusSeq++
	m.statusMsg = s
	seq := m.statusSeq
	return tea.Tick(statusMsgTTL, func(time.Time) tea.Msg {
		return statusExpiredMsg{seq: seq}
	})
}

// setStickyStatus shows an in-flight status with no expiry timer — it reports
// work still in progress and is extinguished by the completion message, not by
// the clock. The seq bump is the whole point of the helper: a transient status
// set within the last statusMsgTTL still has a tick in flight, and its seq would
// otherwise match this message too, wiping the only sign the adapter is busy for
// up to launchTimeout. It returns nothing, so a caller cannot batch a timer onto
// it by mistake.
func (m *Model) setStickyStatus(s string) {
	m.statusSeq++
	m.statusMsg = s
}

// toggleGroupByTag flips the [1] list between the flat update-grouped view and
// the tag-grouped one. The toggle reorders filteredMeta(), and metaSelected is
// an index into that projection — so it would silently point at a different
// tool. Capture the selected tool by name first, remap after: the same
// cursor-follows-the-tool rule the installedMsg/remoteMsg handlers use.
// Deliberately not routed through selectMeta — a pure view toggle changes no
// selection and must fire no auto-fetch.
// The three statusMsg exits all route through setStatus, so the returned tea.Cmd
// is the expiry tick that yields the bar back to the hints after statusMsgTTL;
// the caller (the space case) returns it. A view toggle still fetches nothing.
func (m *Model) toggleGroupByTag() tea.Cmd {
	// Turning it on with nothing tagged would produce a lone "untagged" divider
	// over the unchanged list — a keypress that reads as broken. Say why
	// instead. Turning it *off* is never refused, so a tracker whose last tag
	// was just removed cannot get stuck in the tag view.
	if !m.groupByTag && !m.hasTaggedTool() {
		return m.setStatus("no tags to group by — press [#] on a tool")
	}
	name := ""
	if mt, ok := m.selectedMeta(); ok {
		name = mt.Name
	}
	m.groupByTag = !m.groupByTag
	if name != "" {
		m.metaSelected = m.indexOfMeta(name)
	}
	m.setToolsContent()
	if m.groupByTag {
		return m.setStatus("grouped by tag")
	}
	return m.setStatus("flat list")
}

// hasTaggedTool reports whether any tracked tool carries a tag — the condition
// under which the tag view has anything to show.
func (m Model) hasTaggedTool() bool {
	for _, mt := range m.meta {
		if tagOf(mt) != "" {
			return true
		}
	}
	return false
}

// grouped is the single "the list is in tag view" predicate, shared by the
// ordering (searchMatches) and the header rows (buildToolRows) so the two can
// never disagree about whether headers belong in the content. An active search
// suppresses it: the `/` filter behaves identically to before, headers and all.
// It also requires at least one tagged tool: with none, the tag view is a
// single "untagged" divider over the unchanged list, so it degrades to the flat
// view rather than spending a row on a meaningless section (this covers a tag
// removed while the view is on, which the toggle guard cannot).
func (m Model) grouped() bool {
	return m.groupByTag && m.searchQuery() == "" && m.hasTaggedTool()
}

// groupKey is the section a tool belongs to in the grouped view: the updates
// group when it has a pending release, otherwise its case-folded tag ("" =
// untagged). Case-folded because matchingTag already compares that way and two
// spellings the search treats as one tag must not split into two sections; the
// header then shows the group's first spelling.
//
// The updates group cuts across the tags on purpose. A tag says what a tool IS,
// which is a stable fact the user chose; an update is what the tool NEEDS right
// now, which is the reason the app is open. Sorting the second under the first
// would scatter the answer across the whole list.
func (m Model) groupKey(mt loader.ToolMeta) string {
	if m.hasUpdate(mt.Name) {
		return updatesGroupLabel
	}
	return tagKey(mt)
}

// groupMatchesByTag reorders matches so tools in the same section sit together:
// the updates section first, then each tag in first-appearance (meta.yaml)
// order, untagged last. Order inside a section is meta.yaml's too — the same
// stability rule as the flat view's update partition, so the list only ever
// regroups rows the user's own file order already fixed. One section per tool
// (groupKey), so no row can land in two.
func (m Model) groupMatchesByTag(matches []searchMatch) []searchMatch {
	var order []string
	seen := make(map[string]bool, len(matches))
	for _, sm := range matches {
		if key := m.groupKey(sm.meta); key != "" && !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
	}
	// The updates section leads regardless of where its first tool sits in
	// meta.yaml — it is the one section whose position is a statement.
	if seen[updatesGroupLabel] {
		order = append([]string{updatesGroupLabel},
			slices.DeleteFunc(order, func(k string) bool { return k == updatesGroupLabel })...)
	}
	out := make([]searchMatch, 0, len(matches))
	for _, key := range order {
		for _, sm := range matches {
			if m.groupKey(sm.meta) == key {
				out = append(out, sm)
			}
		}
	}
	for _, sm := range matches {
		if m.groupKey(sm.meta) == "" {
			out = append(out, sm)
		}
	}
	return out
}

// tagOf returns a tool's single tag, or "" when it has none. Tags stays a
// []string so meta.yaml keeps its schema, but it is held at len<=1 (parseTag on
// the way in, loader.LoadMeta migrating legacy lists), so every consumer reads
// the one tag through here rather than joining the slice.
func tagOf(mt loader.ToolMeta) string {
	if len(mt.Tags) == 0 {
		return ""
	}
	return mt.Tags[0]
}

// tagKey is the grouping identity of a tool's tag: case-folded, because the
// search predicate (matchingTag) already compares case-insensitively, and two
// spellings the search treats as one tag must not split the list into two
// sections. The header still shows the group's first spelling.
func tagKey(mt loader.ToolMeta) string {
	return strings.ToLower(tagOf(mt))
}

// matchingTag returns the first tag containing the query, or "".
func matchingTag(tags []string, query string) string {
	for _, tag := range tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return tag
		}
	}
	return ""
}

// filteredMeta projects searchMatches to the plain meta slice. It routes
// through searchMatches unconditionally (no empty-query fast path): the grouped
// order must reach the selection/mouse system too, or the renderer (grouped)
// and every click/cursor (ungrouped m.meta) would target different rows.
func (m Model) filteredMeta() []loader.ToolMeta {
	matches := m.searchMatches()
	out := make([]loader.ToolMeta, len(matches))
	for i, sm := range matches {
		out[i] = sm.meta
	}
	return out
}

// selectMeta moves the cursor to idx in the current (possibly filtered) list
// and refreshes every panel that tracks the selection: the tools list, the
// brief card (scrolled to top) and the help viewport via the auto-fetch path.
// Shared by keyboard navigation (j/k, arrows, pgup/pgdown), the search
// commit/rollback exits and mouse clicks so the post-move refresh ritual
// cannot drift between call sites.
func (m *Model) selectMeta(idx int) tea.Cmd {
	m.metaSelected = idx
	// Moving to a tool is intent to see that tool: a completed self-update log
	// would otherwise hold [3] for every row (it is selection-independent by
	// design), with no way back to help. A live one keeps the panel.
	m.dismissSelfLog()
	m.setToolsContent()
	m.briefViewport.GotoTop()
	m.briefViewport.SetContent(m.renderCard())
	return m.autoFetchCmdsForSelected()
}

// markReadmeLoading records that a README request for name is in flight, so
// needsReadme stops returning true until the readmeMsg lands. Every site that
// fires fetchReadmeCmd/refreshReadmeCmd must call it. The lazy map creation
// keeps hand-built Models (tests, which skip New) from panicking on a nil map;
// there the mark is simply a no-op, which is what a model with no session
// caches wants anyway.
func (m *Model) markReadmeLoading(name string) {
	if m.readmeLoading == nil {
		m.readmeLoading = make(map[string]bool)
	}
	m.readmeLoading[name] = true
}

// helpWrapWidth is the single source of the help panel's inner wrap width.
// renderHelpContent and setHelpContent must wrap identically — entry ranges
// are wrapped-line indices, so a width divergence would desync the spotlight
// from the lines the viewport actually shows.
//
// The viewport is one column narrower than the panel (withScrollbar keeps that
// one for the thumb) and a panelGutter is held at each end, so the text sits one
// column clear of the frame on the left and of the scrollbar on the right.
func (m Model) helpWrapWidth() int {
	return max(m.helpW-1-2*panelGutter, 20)
}

// setHelpContent is the single recompute point for the help panel: whenever
// the underlying text changes (selection move, [H]/[M], fetched help output,
// resize, update-log transitions) it re-derives the navigable entry index and
// resets the spotlight cursor before repainting the viewport. Style-only
// repaints (search-highlight keystrokes, cursor moves, per-chunk log appends)
// call SetContent(renderHelpContent()) directly — they must not reset the
// cursor. Never scrolls; callers keep their own GotoTop/GotoBottom.
func (m *Model) setHelpContent() {
	s := m.sty()
	m.helpEntries = nil
	m.helpNavIdx = -1
	m.helpBase = ""
	// The update log (a tool's or keepkit's own — showsUpdateLog covers both) and
	// the loading state render instead of help text; both leave the entry index
	// empty so j/k stay plain scroll instead of driving a spotlight over log
	// lines. A cache miss or stored "No --help output…" fallback yields no
	// entries via the parser.
	if mt, ok := m.selectedMeta(); ok && !m.showsUpdateLog() {
		switch {
		case m.helpMode == helpModeReadme:
			// README mode: helpBase holds the glamour render (memoized — this
			// runs on every selection move and resize). Entries stay empty, so
			// j/k are plain scroll and colorizeHelp/parseHelpEntries never touch
			// the ANSI output glamour produces.
			if data, has := m.readmeData[mt.Name]; has && data.content != "" {
				m.helpBase = m.readmeRender.render(mt.Name, data.content, m.helpWrapWidth(), m.darkBG, m.sty().Theme, m.repoCards[mt.Name].About)
			}
		case m.helpLoadingFor != mt.Name:
			if raw := m.rawHelpText(); raw != "" {
				m.helpEntries = parseHelpEntries(raw, m.helpWrapWidth())
				// Built here, not via renderHelpContent: that renderer serves
				// helpBase back through applySpotlight, and the cache must hold
				// the plain full-color base the spotlight dims against.
				m.helpBase = colorizeHelp(s, wrapText(raw, m.helpWrapWidth()))
			}
		}
	}
	m.helpViewport.SetContent(m.renderHelpContent())
}

// switchHelpMode points panel [3] at one of its three sources and returns the
// fetch command for that source when it is not cached yet. Shared by [H], [M]
// and [R] — the three keys differ only in which source they select, so the
// log dismissal, repaint and scroll-to-top live here once. Focus stays with the
// caller: all three fire from [2] as well as [3] and move focus with them.
func (m *Model) switchHelpMode(mode int) tea.Cmd {
	m.helpMode = mode
	// An explicit mode switch is intent to leave a completed update log
	// (otherwise sticky), keepkit's own included — and that one is not tied to a
	// selection, so it is released before the guard below, which returns early on
	// an empty tracker. There the repaint has to happen here: nothing else
	// re-renders [3] with no tool to select.
	dropped := m.dismissSelfLog()
	mt, ok := m.selectedMeta()
	if !ok {
		if dropped {
			m.setHelpContent()
		}
		return nil
	}
	// The same intent for a tool's log; a live update (updatingFor == name) keeps
	// the panel.
	if m.updateLogFor == mt.Name && m.updatingFor != mt.Name {
		m.updateLogFor = ""
	}
	// README first: helpCache is a [2]string and mode 2 would index it out of
	// range. Its source is readmeData, and a selection moved while [H]/[M] was
	// showing never fetched it (autoFetchCmdsForSelected only fetches in readme
	// mode), so the switch has to cover that gap.
	if mode == helpModeReadme {
		m.setHelpContent()
		m.helpViewport.GotoTop()
		// needsReadme is false while a request is in flight, including the one
		// a [r] refresh in [2] just fired (see autoFetchCmdsForSelected).
		if t, ok := m.selectedTool(); ok && m.needsReadme(t) {
			m.markReadmeLoading(t.Name)
			return fetchReadmeCmd(t.GitHub, t.Name)
		}
		return nil
	}
	if m.helpCache[mt.Name][mode] == "" {
		m.helpLoadingFor = mt.Name
		m.setHelpContent()
		return fetchHelpCmd(mt.Name, mode)
	}
	m.setHelpContent()
	m.helpViewport.GotoTop()
	return nil
}

// clearHelpNav turns the spotlight cursor off and repaints the help viewport
// when it was on, reporting whether it was. Every path that deactivates
// navigation (esc, any focus move) must repaint through
// here — clearing the index without the repaint leaves stale dimming on
// screen, because nothing else re-renders help on those paths.
func (m *Model) clearHelpNav() bool {
	if m.helpNavIdx < 0 {
		return false
	}
	m.helpNavIdx = -1
	m.helpViewport.SetContent(m.renderHelpContent())
	return true
}

// helpNavStep moves the [3] spotlight cursor. The first press lands on the
// entry at the user's reading position (helpNavStart) — not the document
// top — later presses step by delta, clamped at the ends (no wrap: cycling
// around a multi-screen man page is disorienting). The repaint is
// style-only — the entry index is unchanged, so setHelpContent (which would
// reset the cursor it just moved) must not run here.
func (m *Model) helpNavStep(delta int) {
	if len(m.helpEntries) == 0 {
		return
	}
	if m.helpNavIdx < 0 {
		m.helpNavIdx = m.helpNavStart(delta)
	} else {
		m.helpNavIdx = min(max(m.helpNavIdx+delta, 0), len(m.helpEntries)-1)
	}
	m.helpViewport.SetContent(m.renderHelpContent())
	m.scrollToNavEntry()
}

// helpNavStart picks the entry the first navigation press lands on: the
// first entry intersecting the visible window. When no entry is on screen
// (reading prose between option blocks), the nearest entry in the movement
// direction — the next one below for j, the previous one above for k — so
// activation continues from the reading position instead of jumping to an
// arbitrary off-screen entry; clamped to the ends.
func (m Model) helpNavStart(delta int) int {
	top := m.helpViewport.YOffset
	bottom := top + m.helpViewport.Height
	for i, e := range m.helpEntries {
		if e.end > top && e.start < bottom {
			return i
		}
	}
	if delta < 0 {
		for i := len(m.helpEntries) - 1; i >= 0; i-- {
			if m.helpEntries[i].end <= top {
				return i
			}
		}
		return 0
	}
	for i, e := range m.helpEntries {
		if e.start >= bottom {
			return i
		}
	}
	return len(m.helpEntries) - 1
}

// scrollToNavEntry keeps the current entry in view. The branches are
// mutually exclusive and the scroll-down case clamps to the entry start, so
// an entry taller than the window pins its start to the top edge instead of
// bottom-aligning (which would push the start off-screen).
func (m *Model) scrollToNavEntry() {
	e := m.helpEntries[m.helpNavIdx]
	switch {
	case e.start < m.helpViewport.YOffset:
		m.helpViewport.SetYOffset(e.start)
	case e.end > m.helpViewport.YOffset+m.helpViewport.Height:
		m.helpViewport.SetYOffset(min(e.end-m.helpViewport.Height, e.start))
	}
}

// setFocus moves focus to f and refreshes the tools list, the only viewport
// whose *content* depends on focus (renderLeftContent dims the selection bar
// and drops the search highlight when the list is unfocused). The brief card
// and the help text render identically either way — their focus-dependent
// parts are the border and the inset title, which renderBrief/renderHelp apply
// around the viewport at View time. Every focus move (digits, arrows, esc and
// the mouse) goes through here, so the refresh cannot drift between call sites.
func (m *Model) setFocus(f int) {
	if m.focus == f {
		return
	}
	m.focus = f
	// The spotlight cursor is an attribute of actively reading [3]: any
	// focus move clears it (with the repaint clearHelpNav guarantees).
	m.clearHelpNav()
	m.setToolsContent()
}

// indexOfMeta returns the index of the named tool in the *displayed* order
// (m.filteredMeta() — the grouped, possibly search-filtered projection), so
// callers that set m.metaSelected land on the row the renderer actually draws.
// Falls back to 0 when the name is empty or the tool is absent (e.g. untracked
// mid-search, or filtered out).
func (m Model) indexOfMeta(name string) int {
	for i, mt := range m.filteredMeta() {
		if mt.Name == name {
			return i
		}
	}
	return 0
}

const (
	rateUnknown rateLevel = iota
	rateOK
	rateWarn
	rateExhausted
)

// isDevBuild reports whether the running binary is a working copy rather than a
// release — the same question selfCheckEnabled asks, exported to the test that
// pins displayVersion's contract: shortening the label must not shorten what the
// self-update gate sees.
func (m Model) isDevBuild() bool { return isDevVersion(m.appVersion) }
