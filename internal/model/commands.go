package model

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/launcher"
	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/logx"
	"github.com/stanlyzoolo/keepkit/internal/proc"
	"github.com/stanlyzoolo/keepkit/internal/updater"
	"github.com/stanlyzoolo/keepkit/internal/version"
)

// safeCmd wraps a command so a panic in its goroutine is recorded to the session
// log (with the real stack) and then re-raised. Bubble Tea catches the re-raised
// panic to restore the terminal, but prints the trace into the alt-screen buffer
// where it is lost on exit; logx.Recover writes it to a durable file first. One
// grep-able, uniform call site is worth the extra closure allocation per command.
func safeCmd(ctx string, fn tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer logx.Recover(ctx)
		return fn()
	}
}

// tokenValidatedMsg carries the result of validating a candidate token against
// GET /rate_limit. token is the candidate to persist on success.
type tokenValidatedMsg struct {
	token string
	rate  version.RateLimit
	err   error
}

// validateTokenCmd checks a candidate token against /rate_limit without touching
// package token state; the handler persists it only on success.
func validateTokenCmd(token string) tea.Cmd {
	return safeCmd("validateTokenCmd", func() tea.Msg {
		rate, err := version.FetchRateWithToken(token)
		return tokenValidatedMsg{token: token, rate: rate, err: err}
	})
}

// launchTimeout bounds a tab-open adapter subprocess. Adapters normally return
// near-instantly; the ceiling exists for the paths that can block on a human —
// most notably osascript waiting on the macOS Automation permission dialog.
// When it fires, the launchDoneMsg error handler auto-falls back to running the
// tool in the current window, so the launch still happens. A var, not a const:
// the timeout/KillGroup wiring is load-bearing and tests shrink it instead of
// waiting 10 real seconds (the same seam idiom as updater's testHomeDir).
var launchTimeout = 10 * time.Second

// launchDoneMsg carries the result of a tab-open adapter run (startLaunchCmd).
// command is the user's shell command, carried so the error handler can build
// the ExecProcess fallback without re-reading input state.
type launchDoneMsg struct {
	toolName string
	command  string
	err      error
}

// execDoneMsg is the tea.ExecProcess callback result for the fallback path:
// the tool ran in the current window and keepkit resumed. err is the tool's
// own exit status — a tool exiting non-zero is not a keepkit anomaly, so the
// handler surfaces it as a statusMsg and never logs it.
type execDoneMsg struct {
	toolName string
	err      error
}

// startLaunchCmd runs a tab-open adapter plan (tmux new-window, osascript,
// kitten @ launch, …) and emits launchDoneMsg. The adapter is detached from
// the controlling terminal like every other probe (proc.DetachTTY), and on the
// timeout the whole process group is killed — DetachTTY's Setsid makes the
// child a session leader, so a plain kill would orphan grandchildren. The
// error is not logged here: the launchDoneMsg handler auto-falls back, so an
// adapter failure is a degraded path, not a dead end.
//
// Accepted race: if the timeout kills an adapter that had already executed its
// tab command (osascript stuck after `write text`, a stalled tmux server), the
// error still triggers the fallback and the command runs twice. The window is
// narrow and undetectable from here; the alternative — not falling back — would
// strand every genuine failure.
func startLaunchCmd(plan launcher.Plan, toolName, command string) tea.Cmd {
	return safeCmd("startLaunchCmd", func() tea.Msg {
		if len(plan.Argv) == 0 {
			return launchDoneMsg{toolName: toolName, command: command, err: errors.New("empty launch command")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, plan.Argv[0], plan.Argv[1:]...)
		proc.DetachTTY(cmd)
		cmd.Cancel = func() error { return proc.KillGroup(cmd) }
		err := cmd.Run()
		return launchDoneMsg{toolName: toolName, command: command, err: err}
	})
}

// shellCommand resolves the shell invocation that runs the user's command in
// the current window. Pure and goos-parameterized so both branches are
// table-testable without spawning anything — the exact mirror of
// browserCommand's seam. The cmd /c branch rides Go's generic argv quoting,
// which is not cmd.exe-aware — a command embedding double quotes can misparse
// there; accepted on the degraded Windows path (the fix, SysProcAttr.CmdLine,
// needs a per-GOOS file and a real Windows report to justify it).
// updater.customPlan is the deliberate duplicate of this decision on the update
// path (updater sits below model in the import graph and may not import it);
// the two must not drift.
func shellCommand(goos, cmd string) (string, []string) {
	if goos == "windows" {
		return "cmd", []string{"/c", cmd}
	}
	return "sh", []string{"-c", cmd}
}

// execToolCmd runs command in the current window via tea.ExecProcess: Bubble
// Tea releases the terminal, the tool takes it over, and keepkit resumes when
// it exits. This is the fallback for terminals with no tab-scripting API and
// the safety net when an adapter fails. Deliberately not wrapped in safeCmd:
// tea.ExecProcess only builds the exec message — nothing here can panic.
func execToolCmd(toolName, command string) tea.Cmd {
	name, args := shellCommand(runtime.GOOS, command)
	return tea.ExecProcess(exec.Command(name, args...), execDoneCallback(toolName))
}

// execDoneCallback maps the tea.ExecProcess completion error into execDoneMsg
// for toolName. Extracted from execToolCmd so the mapping is testable — the
// exec message Bubble Tea builds keeps its callback field unexported.
func execDoneCallback(toolName string) func(error) tea.Msg {
	return func(err error) tea.Msg {
		return execDoneMsg{toolName: toolName, err: err}
	}
}

// notFoundExit reports whether err is the shell's "command not found" exit
// status: 127 from sh (POSIX), 9009 from cmd.exe. Exit 127 means the tool
// never ran at all, so the execDoneMsg handler can say "not installed"
// instead of a cryptic exit status. Only the ExecProcess fallback can see
// this — a tab adapter's shell reports the miss inside its own tab.
func notFoundExit(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	code := ee.ExitCode()
	return code == 127 || code == 9009
}

// fetchInstalledCmd returns a Cmd that detects the installed version of t
// locally (subprocess) and emits an installedMsg. It never touches the network,
// so the installed version can render before any GitHub fetch completes.
func fetchInstalledCmd(t loader.Tool) tea.Cmd {
	return safeCmd("fetchInstalledCmd", func() tea.Msg {
		installed, present := version.InstalledVersion(t)
		return installedMsg{
			toolName:  t.Name,
			installed: installed,
			present:   present,
		}
	})
}

// fetchRemoteCmd returns a Cmd that makes a single network pass over t's
// repository via version.GetRepoData (release + repo info + languages) and emits
// a remoteMsg carrying the latest tag, repo status and repo card together.
func fetchRemoteCmd(t loader.Tool) tea.Cmd { return remoteCmd(t, false) }

// refreshRemoteCmd is the force variant of fetchRemoteCmd: it bypasses the cache
// TTL via version.RefreshRepoData. Emits the same remoteMsg.
func refreshRemoteCmd(t loader.Tool) tea.Cmd { return remoteCmd(t, true) }

func remoteCmd(t loader.Tool, force bool) tea.Cmd {
	ctx := "fetchRemoteCmd"
	if force {
		ctx = "refreshRemoteCmd"
	}
	return safeCmd(ctx, func() tea.Msg {
		var d version.RepoData
		if force {
			d = version.RefreshRepoData(t.GitHub)
		} else {
			d = version.GetRepoData(t.GitHub)
		}
		repoStatus := d.RepoStatus
		// Rate-limited and no data came back: signal the card to render a
		// "rate limited" hint. This gives ErrRateLimited a real runtime consumer.
		if errors.Is(d.Err, version.ErrRateLimited) && d.Latest == "" && d.About == "" {
			repoStatus = "rate-limited"
		}
		return remoteMsg{
			toolName:   t.Name,
			latest:     d.Latest,
			repoStatus: repoStatus,
			card: version.RepoCard{
				About:       d.About,
				Stars:       d.Stars,
				Languages:   d.Languages,
				Latest:      d.Latest,
				PublishedAt: d.PublishedAt,
				HtmlUrl:     d.HtmlUrl,
				Body:        d.Body,
				RepoStatus:  d.RepoStatus,
			},
			rate:          version.Rate(),
			err:           d.Err,
			conclusive:    d.Conclusive,
			tokenRejected: version.TokenRejected(),
		}
	})
}

// fetchRateCmd queries GET /rate_limit, which reports the current quota without
// spending it, and emits a rateMsg. Fired on startup to seed the status-bar
// signal even on warm-cache starts (where remote fetches make no request) and
// on demand from the API-status overlay.
func fetchRateCmd() tea.Cmd {
	return safeCmd("fetchRateCmd", func() tea.Msg {
		rate, err := version.FetchRate()
		return rateMsg{rate: rate, err: err, tokenRejected: version.TokenRejected()}
	})
}

// selfCheckCmd fetches keepkit's own latest release tag — one release-only
// request per cache TTL window (see version.SelfLatest) — and emits a
// selfCheckMsg. It takes no parameters: the repo is a constant and the
// is-it-newer comparison belongs to the handler, which knows m.appVersion.
func selfCheckCmd() tea.Cmd {
	return safeCmd("selfCheckCmd", func() tea.Msg {
		// No logging here, the same as remoteCmd: version owns it end to end
		// (doGH logs a transport failure, classifyStatus a bad status, both with
		// more detail than this layer has), and a second line per failure would
		// mean every offline launch writes two — inflating the "a log file means
		// something went wrong" signal instead of sharpening it. "No release
		// published" is not a failure at all: it arrives as an empty tag with a
		// nil error.
		latest, err := version.SelfLatest()
		return selfCheckMsg{latest: latest, err: err}
	})
}

func fetchChangelogCmd(githubField, toolName string) tea.Cmd {
	return changelogCmd(githubField, toolName, false)
}

// refreshChangelogCmd is the force variant of fetchChangelogCmd: it bypasses the
// cache TTL via version.RefreshChangelog. Emits the same changelogMsg.
func refreshChangelogCmd(githubField, toolName string) tea.Cmd {
	return changelogCmd(githubField, toolName, true)
}

func changelogCmd(githubField, toolName string, force bool) tea.Cmd {
	ctx := "fetchChangelogCmd"
	if force {
		ctx = "refreshChangelogCmd"
	}
	return safeCmd(ctx, func() tea.Msg {
		var (
			info version.ReleaseInfo
			err  error
		)
		if force {
			info, err = version.RefreshChangelog(githubField)
		} else {
			info, err = version.GetChangelog(githubField)
		}
		return changelogMsg{
			toolName:    toolName,
			tag:         info.Tag,
			body:        info.Body,
			htmlUrl:     info.HtmlUrl,
			publishedAt: info.PublishedAt,
			err:         err,
		}
	})
}

// fetchReadmeCmd fetches the repository README for a tool (cached 24h in
// cache.json, so a warm start makes no request). Mirrors fetchChangelogCmd.
func fetchReadmeCmd(githubField, toolName string) tea.Cmd {
	return readmeCmd(githubField, toolName, false)
}

// refreshReadmeCmd is the force variant of fetchReadmeCmd: it bypasses the
// cache TTL via version.RefreshReadme. Emits the same readmeMsg.
func refreshReadmeCmd(githubField, toolName string) tea.Cmd {
	return readmeCmd(githubField, toolName, true)
}

func readmeCmd(githubField, toolName string, force bool) tea.Cmd {
	ctx := "fetchReadmeCmd"
	if force {
		ctx = "refreshReadmeCmd"
	}
	return safeCmd(ctx, func() tea.Msg {
		var (
			content string
			err     error
		)
		if force {
			content, err = version.RefreshReadme(githubField)
		} else {
			content, err = version.GetReadme(githubField)
		}
		// ErrNoReadme is a conclusive 404, not a malfunction: logging it would
		// re-create the session log on every launch for a repo that simply has
		// no README, defeating "a log file means something went wrong" (the
		// same rule classifyStatus applies to 404s and InstalledVersion to a
		// tool that is not on PATH). Rate limits are already logged by doGH.
		if err != nil && !errors.Is(err, version.ErrNoReadme) && !errors.Is(err, version.ErrRateLimited) {
			logx.Errorf("model.%s: %s: %v", ctx, toolName, err)
		}
		return readmeMsg{toolName: toolName, content: content, err: err}
	})
}

// needsInstalled reports whether local detection for t has not yet run.
// Detected locally, so it fires regardless of GitHub, matching Init(). It keys
// off InstalledKnown, not Installed=="", so a tool that was probed and found
// missing (Installed=="", InstalledKnown==true) is not re-probed — and thus not
// re-logged — on every cursor movement; a force refresh ([r]) re-detects
// explicitly by bypassing this guard.
func (m *Model) needsInstalled(t loader.Tool) bool {
	info, ok := m.versions[t.Name]
	return !ok || !info.InstalledKnown
}

// needsRemote reports whether the network pass for t must still run.
// Requires a GitHub ref (matching the Init() guard) and either an unknown
// latest tag or a missing repo card — unless the pass already came back clean,
// which is an answer even when it carried no tag at all (a repo with neither
// releases nor tags). Without that marker the empty-Latest clause re-dispatched
// the pass on every cursor visit; see the remoteAnswered field doc for why the
// marker is the error and not m.repoStatus, and for the honest cost of the
// repeat (a goroutine and a cache read, not API quota).
func (m *Model) needsRemote(t loader.Tool) bool {
	if t.GitHub == "" {
		return false
	}
	if m.remoteAnswered[t.Name] {
		return false
	}
	if _, ok := m.repoCards[t.Name]; !ok {
		return true
	}
	info := m.versions[t.Name]
	return info.Latest == ""
}

// needsReadme reports whether the README fetch for t must still run. Requires a
// GitHub ref, no request already in flight, and no session entry at all — an
// entry holding an error counts as answered, so a 404 or a rate limit is not
// retried on every cursor movement (a force refresh clears the entry
// explicitly). The in-flight check is what keeps the "one request per visited
// tool" budget honest: the entry appears only when the response lands, so
// without it a j/k bounce back onto the same tool would fire a second request.
func (m *Model) needsReadme(t loader.Tool) bool {
	if t.GitHub == "" {
		return false
	}
	if m.readmeLoading[t.Name] {
		return false
	}
	_, ok := m.readmeData[t.Name]
	return !ok
}

// refreshSelectedCmd force-refreshes t's data, bypassing the cache TTL: the repo
// pass (release + repo info + languages) and the changelog are re-fetched and the
// installed version is re-detected locally. While the repo pass is in flight
// refreshingFor drives the card spinner; the remoteMsg handler clears it on
// completion, which halts the tick loop. A second press while the same tool is
// already refreshing is ignored. A tool with no GitHub ref only re-detects the
// installed version (no spinner, nothing to clear).
func (m *Model) refreshSelectedCmd(t loader.Tool) tea.Cmd {
	if m.refreshingFor == t.Name {
		return nil
	}
	if t.GitHub == "" {
		return tea.Batch(m.setStatus("no repo to refresh"), fetchInstalledCmd(t))
	}
	m.refreshingFor = t.Name
	m.briefViewport.SetContent(m.renderCard())
	// Drop the session entry so a cached negative (404, rate limit) can recover:
	// the forced fetch is the only thing that ever revisits it. The deletion
	// makes needsReadme true again, so the forced request must be marked in
	// flight or a selection move during the window would fire a second one
	// (refreshingFor does not cover it — remoteMsg clears that flag as soon as
	// the repo pass lands, which can be well before the README does).
	delete(m.readmeData, t.Name)
	m.markReadmeLoading(t.Name)
	return tea.Batch(
		m.spinner.Tick,
		fetchInstalledCmd(t),
		refreshRemoteCmd(t),
		refreshChangelogCmd(t.GitHub, t.Name),
		refreshReadmeCmd(t.GitHub, t.Name),
	)
}

// autoFetchCmdsForSelected returns a batched Cmd that auto-fetches changelog
// and --help for the currently selected tool if not yet cached.
// Uses a pointer receiver so it can update loading state fields on m.
func (m *Model) autoFetchCmdsForSelected() tea.Cmd {
	var cmds []tea.Cmd
	if t, ok := m.selectedTool(); ok {
		if t.GitHub != "" {
			if _, already := m.changelogData[t.Name]; !already && m.changelogLoadingFor != t.Name {
				m.changelogLoadingFor = t.Name
				m.briefViewport.SetContent(m.renderCard())
				cmds = append(cmds, fetchChangelogCmd(t.GitHub, t.Name))
			}
		}
		if m.needsInstalled(t) {
			cmds = append(cmds, fetchInstalledCmd(t))
		}
		if m.needsRemote(t) {
			cmds = append(cmds, fetchRemoteCmd(t))
		}
	}
	if mt, ok := m.selectedMeta(); ok {
		switch {
		case m.showsUpdateLog():
			// A live update log owns [3] — this tool's, or keepkit's own, which is
			// selection-independent: don't fetch help (and don't set
			// helpLoadingFor) — a late helpOutputMsg or the "Loading..." state
			// would clobber the log. Just render the log branch, scrolled to the
			// tail so the newest output is visible on re-selection.
			m.setHelpContent()
			m.helpViewport.GotoBottom()
		case m.helpMode == helpModeReadme:
			// README mode owns [3]: repaint from the session cache and fetch it
			// once per tool. Deliberately ahead of the helpCache case — that one
			// would index the [2]string out of range with mode 2 and spawn a
			// --help subprocess the panel would never show.
			m.setHelpContent()
			m.helpViewport.GotoTop()
			// needsReadme covers the [r] refresh window too: that path deletes
			// the session entry (so a cached negative can recover) and marks its
			// own forced fetch in flight, so leaving and re-entering the tool
			// meanwhile does not spend a second request.
			if t, ok := m.selectedTool(); ok && m.needsReadme(t) {
				m.markReadmeLoading(t.Name)
				cmds = append(cmds, fetchReadmeCmd(t.GitHub, t.Name))
			}
		case m.helpCache[mt.Name][m.helpMode] == "":
			m.helpLoadingFor = mt.Name
			m.setHelpContent()
			cmds = append(cmds, fetchHelpCmd(mt.Name, m.helpMode))
		default:
			m.setHelpContent()
			m.helpViewport.GotoTop()
		}
	}
	return tea.Batch(cmds...)
}

// updateLine is one item crossing from the update reader goroutine to the tea
// runtime. A normal item carries text plus the replace flag (a '\r' progress
// segment overwrites the last line; a '\n' line appends). The final item has
// done set and carries the process exit error (nil on success). Using one
// channel for both — instead of a separate error channel threaded through every
// waitForChunkCmd re-subscribe — keeps the happens-before ordering trivial: the
// reader sends the done item before closing the channel, so it is received in
// order, race-free. The done item also carries how long the process ran: the
// duration is measured here rather than in the model because time.Now() inside
// Update() would make completion non-deterministic in tests, and this is where
// the process actually lives.
type updateLine struct {
	text    string
	replace bool
	done    bool
	err     error
	elapsed time.Duration
}

// updateTimeout bounds a running update. `cargo install` compiles from source
// and can legitimately run for many minutes, so the ceiling is generous.
const updateTimeout = 10 * time.Minute

// detectUpdateCmd resolves the update Plan for t off the Update thread —
// updater.Detect spawns subprocesses (go version -m, cargo install --list) and
// must never run inside Update(), like every other probe. Emits an
// updateDetectedMsg; the handler enters the confirm mode or shows a hint.
//
// self marks a [U] press on the self-update banner rather than an enter in [2]
// on a tool row: the same detection, but the result belongs to a banner with no
// row behind it, which is what the handler's relevance gate needs to know (see
// acceptsUpdateDetect).
func detectUpdateCmd(t loader.Tool, self bool) tea.Cmd {
	return safeCmd("detectUpdateCmd", func() tea.Msg {
		plan, err := updater.Detect(t)
		return updateDetectedMsg{tool: t.Name, plan: plan, err: err, self: self}
	})
}

// startUpdateCmd runs plan's command, streaming its merged stdout+stderr into a
// channel one segment at a time, and returns waitForChunkCmd to pump the first
// segment into the tea runtime. The subprocess is detached from the controlling
// terminal (proc.DetachTTY) so a program that expects a TTY — e.g. a sudo
// password prompt — fails fast instead of hanging invisibly. On the 10-minute
// deadline the whole process group is killed (the child is a session leader,
// so a plain kill would orphan `sh -c` grandchildren).
//
// That last sentence is unix-only, and the difference is load-bearing: on
// Windows proc.KillGroup degrades to Process.Kill on the direct child, so a
// deadline kill ends `cmd /c` while the real updater (winget, powershell)
// survives holding the inherited pipe write handle. Without EOF the drain
// below never returns, cmd.Wait() is never reached and no done item is ever
// sent — m.updatingFor stays set for the rest of the session. WaitDelay does
// not rescue this: StdoutPipe creates no copier goroutine, so os/exec's
// force-close of parentIOPipes only ever runs from inside Wait(). The real fix
// is a Windows process tree kill (Job Object) in internal/proc, deferred with
// the cmd /c quoting caveat until a real Windows report justifies it.
func startUpdateCmd(plan updater.Plan, tool string) tea.Cmd {
	return safeCmd("startUpdateCmd", func() tea.Msg {
		if len(plan.Argv) == 0 {
			return updateDoneMsg{tool: tool, err: errors.New("empty update command")}
		}

		ch := make(chan updateLine, 64)
		ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
		cmd := exec.CommandContext(ctx, plan.Argv[0], plan.Argv[1:]...)
		proc.DetachTTY(cmd)
		// Kill the process group, not just the direct child, when ctx fires.
		cmd.Cancel = func() error { return proc.KillGroup(cmd) }

		pipe, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			return updateDoneMsg{tool: tool, err: err}
		}
		cmd.Stderr = cmd.Stdout // merge stderr into the same pipe (one fd, no copier)
		if err := cmd.Start(); err != nil {
			cancel()
			return updateDoneMsg{tool: tool, err: err}
		}

		// Stamped after Start returns, so the duration measures the process and
		// not the fork: the two early returns above never reach this and report a
		// zero elapsed, which is exactly the "it never ran" case.
		start := time.Now()

		go func() {
			defer cancel()
			// Read the pipe to EOF *first*, then Wait: os/exec forbids calling
			// Wait before all pipe reads finish (it closes the pipe under the
			// reader). Only after the drain + Wait is the exit error known.
			streamLines(pipe, func(text string, replace bool) {
				ch <- updateLine{text: text, replace: replace}
			})
			waitErr := cmd.Wait()
			ch <- updateLine{done: true, err: waitErr, elapsed: time.Since(start)}
			close(ch)
		}()

		// Invoke the wait once here so this command yields the first real chunk
		// message: returning the waitForChunkCmd *value* would hand Update a
		// tea.Cmd as a Msg (Bubble Tea does not auto-run a returned command).
		return waitForChunkCmd(tool, ch)()
	})
}

// waitForChunkCmd blocks on the next item from the update channel and turns it
// into a message: a normal item becomes updateChunkMsg (which re-subscribes via
// this same command), the done item — or a closed channel — becomes
// updateDoneMsg. It is the re-subscribe half of Bubble Tea's channel idiom.
func waitForChunkCmd(tool string, ch chan updateLine) tea.Cmd {
	return safeCmd("waitForChunkCmd", func() tea.Msg {
		ul, ok := <-ch
		if !ok || ul.done {
			return updateDoneMsg{tool: tool, err: ul.err, elapsed: ul.elapsed}
		}
		return updateChunkMsg{tool: tool, line: ul.text, replace: ul.replace, ch: ch}
	})
}

// streamLines reads r to EOF, calling emit once per line with a replace flag.
// The flag models a real terminal cursor: a lone '\r' leaves the cursor at the
// start of the current line, so the *next* segment overwrites it — replace is
// therefore true when the *previous* segment ended in a lone '\r', not the
// current one. '\n' (and "\r\n", collapsed to one '\n') moves to a fresh line,
// so the next segment appends. This makes a progress bar ("10%\r90%\r100%\n")
// collapse to a single updating line while ordinary output still appends. A
// trailing unterminated fragment is emitted last.
func streamLines(r io.Reader, emit func(text string, replace bool)) {
	br := bufio.NewReader(r)
	var buf []byte
	prevCR := false
	flush := func(text string, isCR bool) {
		emit(text, prevCR)
		prevCR = isCR
	}
	for {
		b, err := br.ReadByte()
		if err != nil {
			if len(buf) > 0 {
				flush(string(buf), false)
			}
			return
		}
		switch b {
		case '\n':
			flush(string(buf), false)
			buf = buf[:0]
		case '\r':
			// Peek: "\r\n" is a normal line terminator (append next), a lone
			// "\r" leaves the cursor at line start (overwrite next).
			if next, _ := br.Peek(1); len(next) == 1 && next[0] == '\n' {
				_, _ = br.ReadByte() // consume the '\n'
				flush(string(buf), false)
			} else {
				flush(string(buf), true)
			}
			buf = buf[:0]
		default:
			buf = append(buf, b)
		}
	}
}

func fetchHelpCmd(name string, mode int) tea.Cmd {
	return safeCmd("fetchHelpCmd", func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var output []byte
		var err error
		sawTakeover := false

		modeLabel := "--help"
		if mode == helpModeMan {
			modeLabel = "man"
		}

		// Each mode has exactly one source — no silent cross-fallback — so [m]
		// and [h] are distinct and a missing page/flag surfaces its own message
		// instead of masquerading as the other.
		// Every probe runs detached from the controlling terminal: a tool
		// that answers --help/-h/help by booting its own TUI would otherwise
		// grab /dev/tty and shred keepkit's screen (see internal/proc).
		if mode == helpModeMan {
			cmd := exec.CommandContext(ctx, "man", name)
			cmd.Env = append(os.Environ(), "MANPAGER=cat", "MANWIDTH=80", "TERM=dumb")
			proc.DetachTTY(cmd)
			output, err = cmd.Output()
		} else {
			for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
				if ctx.Err() != nil {
					break
				}
				cmd := exec.CommandContext(ctx, name, args...)
				proc.DetachTTY(cmd)
				out, e := cmd.CombinedOutput()
				err = e
				// A tool that answered the flag by booting its own TUI leaves
				// the alt-screen signature in the capture (plus a crash trace,
				// since DetachTTY cut it off from /dev/tty). That is not help
				// text — fall through to the "No --help output" message.
				if isTUITakeover(out) {
					sawTakeover = true
					continue
				}
				if len(out) > 0 {
					output = out
					break
				}
			}
		}

		if len(output) == 0 {
			// One line per capture, not one per candidate: a TUI takeover trips
			// all of --help/-h/help, and logging inside the loop would write the
			// same line three times.
			if sawTakeover {
				logx.Errorf("model.fetchHelpCmd: %s [%s]: discarded TUI takeover capture", name, modeLabel)
			} else {
				logx.Errorf("model.fetchHelpCmd: %s [%s]: no output: %v", name, modeLabel, err)
			}
			return helpOutputMsg{toolName: name, mode: mode, err: err}
		}
		return helpOutputMsg{toolName: name, mode: mode, output: cleanTerminalOutput(string(output))}
	})
}
