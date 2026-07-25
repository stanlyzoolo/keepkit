package model

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/launcher"
	"github.com/stanlyzoolo/keepkit/internal/logx"
)

// assertExecMsg pins the ExecProcess path: the cmd's message must be Bubble
// Tea's internal exec message. The matching rule (type-name substring, not
// the exact "tea.execMsg" — the type is unexported and a bubbletea rename
// would otherwise break these tests confusingly; launchDoneMsg excluded)
// lives in execMsgIn, the single definition for both helpers.
func assertExecMsg(t *testing.T, msg tea.Msg) {
	t.Helper()
	if !execMsgIn(msg) {
		t.Errorf("msg = %+v (%T), want Bubble Tea's exec message", msg, msg)
	}
}

// clearTerminalEnv blanks every variable launcher.Detect inspects, so the
// detection chain lands on the fallback plan regardless of the terminal the
// tests actually run in. planFor is unexported and cross-package — env is the
// seam.
func clearTerminalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TMUX", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "")
}

// enterRun drives updateRunInput's enter with value as the typed command and
// returns the resulting model and dispatched cmd.
func enterRun(m Model, value string) (Model, tea.Cmd) {
	m.mode = modeRunInput
	m.runInput.SetValue(value)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(Model), cmd
}

// TestShellCommand pins both branches of the goos-parameterized shell seam:
// unix runs the command through sh -c, Windows through cmd /c.
func TestShellCommand(t *testing.T) {
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"linux", "sh", []string{"-c", "git status"}},
		{"darwin", "sh", []string{"-c", "git status"}},
		{"windows", "cmd", []string{"/c", "git status"}},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			name, args := shellCommand(tt.goos, "git status")
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("args = %v, want %v", args, tt.wantArgs)
			}
			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

// TestStartLaunchCmd runs the adapter command constructor against harmless
// argvs: a succeeding binary yields a nil-err launchDoneMsg carrying the tool
// and command, a missing binary a non-nil err, and an empty argv (which a
// fallback plan would have — dispatch must never route one here) errors
// instead of panicking on Argv[0].
func TestStartLaunchCmd(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("needs the unix true binary")
		}
		msg, ok := startLaunchCmd(launcher.Plan{Argv: []string{"true"}}, "git", "git status")().(launchDoneMsg)
		if !ok {
			t.Fatalf("unexpected msg type %T", msg)
		}
		if msg.err != nil {
			t.Errorf("err = %v, want nil", msg.err)
		}
		if msg.toolName != "git" || msg.command != "git status" {
			t.Errorf("msg = %+v, want toolName git and command carried", msg)
		}
	})
	t.Run("missing adapter binary", func(t *testing.T) {
		msg, ok := startLaunchCmd(launcher.Plan{Argv: []string{"keepkit-no-such-adapter-xyz"}}, "git", "git status")().(launchDoneMsg)
		if !ok {
			t.Fatalf("unexpected msg type %T", msg)
		}
		if msg.err == nil {
			t.Error("err = nil, want the exec failure")
		}
	})
	t.Run("empty argv", func(t *testing.T) {
		msg, ok := startLaunchCmd(launcher.Plan{}, "git", "git status")().(launchDoneMsg)
		if !ok {
			t.Fatalf("unexpected msg type %T", msg)
		}
		if msg.err == nil {
			t.Error("err = nil, want an empty-command error")
		}
	})
}

// TestStartLaunchCmdTimeout pins the load-bearing timeout wiring: when the
// launchTimeout deadline fires, cmd.Cancel (proc.KillGroup on the process
// group) kills the adapter and the launchDoneMsg carries the failure — this is
// what routes a stuck osascript into the auto-fallback. launchTimeout is a var
// precisely so this test does not wait 10 real seconds.
func TestStartLaunchCmdTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs the unix sleep binary")
	}
	orig := launchTimeout
	launchTimeout = 50 * time.Millisecond
	defer func() { launchTimeout = orig }()

	start := time.Now()
	msg, ok := startLaunchCmd(launcher.Plan{Argv: []string{"sleep", "30"}}, "git", "git status")().(launchDoneMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T", msg)
	}
	if msg.err == nil {
		t.Fatal("err = nil, want the deadline kill")
	}
	if msg.toolName != "git" || msg.command != "git status" {
		t.Errorf("msg = %+v, want toolName and command carried for the fallback", msg)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("adapter outlived the deadline by %v — Cancel/KillGroup wiring is broken", elapsed)
	}
}

// TestRunInputDispatchFallback: with no detectable terminal (env cleared) the
// enter dispatch takes the ExecProcess path — the returned cmd yields Bubble
// Tea's internal exec message, not a launchDoneMsg — and records lastRun.
func TestRunInputDispatchFallback(t *testing.T) {
	clearTerminalEnv(t)
	m := newTestModel(focusTools)
	nm, cmd := enterRun(m, "git status")
	if nm.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal", nm.mode)
	}
	if got := nm.lastRun["git"]; got != "git status" {
		t.Errorf("lastRun[git] = %q, want %q", got, "git status")
	}
	if nm.launchingFor != "" {
		t.Errorf("launchingFor = %q, want empty — the ExecProcess path needs no guard", nm.launchingFor)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want the ExecProcess dispatch")
	}
	// Invoking the cmd only builds the message — the process runs later, when
	// the runtime handles it — so nothing is spawned here.
	assertExecMsg(t, cmd())
}

// TestRunInputDispatchAdapter: with $TMUX set (pointing at a bogus socket, so
// invoking the cmd cannot touch a real tmux server) the enter dispatch takes
// the adapter path — the returned cmd emits launchDoneMsg carrying the tool
// name and command for the auto-fallback.
func TestRunInputDispatchAdapter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("invoking the adapter cmd needs a unix tmux/exec environment")
	}
	clearTerminalEnv(t)
	t.Setenv("TMUX", "/nonexistent/keepkit-test-socket,99999,0")
	m := newTestModel(focusTools)
	nm, cmd := enterRun(m, "git status")
	if got := nm.lastRun["git"]; got != "git status" {
		t.Errorf("lastRun[git] = %q, want %q", got, "git status")
	}
	// Dispatch feedback + one-launch-at-a-time guard: the adapter can block
	// for seconds, so the user must see progress and a second enter must not
	// dispatch concurrently.
	if nm.launchingFor != "git" {
		t.Errorf("launchingFor = %q, want %q", nm.launchingFor, "git")
	}
	if !strings.Contains(nm.statusMsg, "launching git") || !strings.Contains(nm.statusMsg, "tmux") {
		t.Errorf("statusMsg = %q, want launching feedback naming tool and terminal", nm.statusMsg)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want the adapter dispatch")
	}
	msg, ok := cmd().(launchDoneMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T, want launchDoneMsg", msg)
	}
	if msg.toolName != "git" || msg.command != "git status" {
		t.Errorf("msg = %+v, want toolName and command carried for the fallback", msg)
	}
	// err is not asserted: tmux may be missing entirely or fail to connect to
	// the bogus socket — either way the adapter path emitted its message.
}

// TestLaunchDoneMsgFallback: an adapter failure sets an explanatory statusMsg
// and auto-falls back to the ExecProcess path, so the tool still launches.
// Nothing is logged — the fallback makes this degraded, not broken.
func TestLaunchDoneMsgFallback(t *testing.T) {
	logDir := t.TempDir()
	restore := logx.SetDirForTesting(logDir)
	defer restore()

	m := newTestModel(focusTools)
	m.launchingFor = "git"
	updated, cmd := m.Update(launchDoneMsg{toolName: "git", command: "git status", err: errors.New("osascript: not authorized")})
	nm := updated.(Model)
	if nm.launchingFor != "" {
		t.Errorf("launchingFor = %q, want the guard cleared", nm.launchingFor)
	}
	if nm.statusMsg == "" || !strings.Contains(nm.statusMsg, "git") {
		t.Errorf("statusMsg = %q, want a tab-failure explanation naming the tool", nm.statusMsg)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want the ExecProcess auto-fallback")
	}
	assertExecMsg(t, cmd())
	if out := logx.ReadAllForTesting(logDir); out != "" {
		t.Errorf("adapter failure must not log (auto-fallback handles it), got:\n%s", out)
	}
}

// execMsgIn walks a cmd's produced message, flattening tea batches, and
// reports whether some leaf is Bubble Tea's exec message (matched like
// assertExecMsg — type-name substring, launchDoneMsg excluded).
func execMsgIn(msg tea.Msg) bool {
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil && execMsgIn(c()) {
				return true
			}
		}
		return false
	}
	if _, isLaunch := msg.(launchDoneMsg); isLaunch {
		return false
	}
	return strings.Contains(fmt.Sprintf("%T", msg), "exec")
}

// TestLaunchDoneMsgModeGateDefersFallback: a delayed adapter failure
// (osascript can block on the Automation dialog until launchTimeout) must NOT
// auto-fall back while another mode owns the screen — tea.ExecProcess would
// seize the terminal under an open editor/overlay and route keystrokes to the
// spawned shell. The failure must not be silent either: the fallback is
// deferred, and the keystroke that closes the mode flushes it — dispatching
// the exec fallback directly (no adapter re-run) with a status message the
// normal-mode status bar actually renders. The visibility assertion is the
// point: a statusMsg set at gate time is shadowed by the mode's own
// status-bar branch and wiped by the blanket KeyMsg reset.
func TestLaunchDoneMsgModeGateDefersFallback(t *testing.T) {
	for _, mode := range []inputMode{modeEditNote, modeSearch, modeHelpSearch, modeAPIStatus, modeTrack, modeRunInput, modeHotkeys} {
		m := newTestModel(focusTools)
		m.mode = mode
		m.launchingFor = "git"
		updated, cmd := m.Update(launchDoneMsg{toolName: "git", command: "git status", err: errors.New("osascript: not authorized")})
		nm := updated.(Model)
		if cmd != nil {
			t.Errorf("mode %d: cmd != nil — the auto-fallback fired under an open mode", mode)
		}
		if nm.mode != mode {
			t.Errorf("mode %d: mode changed to %d, want untouched", mode, nm.mode)
		}
		if nm.launchingFor != "" {
			t.Errorf("mode %d: launchingFor = %q, want the guard cleared", mode, nm.launchingFor)
		}
		if nm.pendingLaunchName != "git" || nm.pendingLaunchCommand != "git status" {
			t.Errorf("mode %d: pending = %q/%q, want the deferred fallback stored", mode, nm.pendingLaunchName, nm.pendingLaunchCommand)
		}

		// The mode-closing keystroke (esc exits every one of these modes)
		// flushes the deferred fallback.
		closed, flushCmd := nm.Update(tea.KeyMsg{Type: tea.KeyEsc})
		fm := closed.(Model)
		if fm.mode != modeNormal {
			t.Fatalf("mode %d: esc left mode %d, want modeNormal", mode, fm.mode)
		}
		if fm.pendingLaunchName != "" || fm.pendingLaunchCommand != "" {
			t.Errorf("mode %d: pending = %q/%q after flush, want cleared", mode, fm.pendingLaunchName, fm.pendingLaunchCommand)
		}
		if flushCmd == nil {
			t.Fatalf("mode %d: flush cmd = nil, want the exec fallback dispatched", mode)
		}
		if !execMsgIn(flushCmd()) {
			t.Errorf("mode %d: flush cmd produced no exec message — the fallback did not fire", mode)
		}
		// VISIBILITY: the message must survive the mode-closing keystroke and
		// render on the normal-mode status bar.
		if bar := stripANSI(fm.renderStatusBar()); !strings.Contains(bar, "tab open failed — running git here") {
			t.Errorf("mode %d: status bar = %q, want the fallback message rendered", mode, bar)
		}
	}
}

// TestLaunchDoneMsgModeGateTokenInputTwoStage: modeTokenInput's esc falls back
// to modeAPIStatus, not modeNormal — the deferred fallback must stay pending
// through that intermediate stop and flush only on the esc that actually
// closes the overlay.
func TestLaunchDoneMsgModeGateTokenInputTwoStage(t *testing.T) {
	m := newTestModel(focusTools)
	m.mode = modeTokenInput
	updated, _ := m.Update(launchDoneMsg{toolName: "git", command: "git status", err: errors.New("osascript: not authorized")})
	nm := updated.(Model)

	stage1, cmd1 := nm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	sm := stage1.(Model)
	if sm.mode != modeAPIStatus {
		t.Fatalf("mode = %d after first esc, want modeAPIStatus", sm.mode)
	}
	if cmd1 != nil && execMsgIn(cmd1()) {
		t.Error("exec fallback fired while the API overlay is still open")
	}
	if sm.pendingLaunchName != "git" {
		t.Errorf("pendingLaunchName = %q, want kept through the modeAPIStatus stop", sm.pendingLaunchName)
	}

	stage2, cmd2 := sm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	fm := stage2.(Model)
	if fm.mode != modeNormal {
		t.Fatalf("mode = %d after second esc, want modeNormal", fm.mode)
	}
	if fm.pendingLaunchName != "" {
		t.Errorf("pendingLaunchName = %q, want cleared by the flush", fm.pendingLaunchName)
	}
	if cmd2 == nil || !execMsgIn(cmd2()) {
		t.Error("second esc produced no exec message — the deferred fallback never ran")
	}
	if bar := stripANSI(fm.renderStatusBar()); !strings.Contains(bar, "tab open failed — running git here") {
		t.Errorf("status bar = %q, want the fallback message rendered", bar)
	}
}

// TestRunInputRedispatchSupersedesPendingLaunch: a gated adapter failure can
// land while the run prompt is open. An explicit new dispatch from that
// prompt supersedes the deferred fallback — flushing both on the same enter
// would run two commands.
func TestRunInputRedispatchSupersedesPendingLaunch(t *testing.T) {
	clearTerminalEnv(t)
	m := newTestModel(focusTools)
	m.pendingLaunchName = "git"
	m.pendingLaunchCommand = "git status"
	nm, cmd := enterRun(m, "git log")
	if nm.pendingLaunchName != "" || nm.pendingLaunchCommand != "" {
		t.Errorf("pending = %q/%q, want dropped by the new dispatch", nm.pendingLaunchName, nm.pendingLaunchCommand)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want the new command dispatched")
	}
	msg := cmd()
	if _, isBatch := msg.(tea.BatchMsg); isBatch {
		t.Fatalf("msg = %T, want a single exec dispatch — a batch means the stale fallback ran too", msg)
	}
	assertExecMsg(t, msg)
	if strings.Contains(nm.statusMsg, "tab open failed") {
		t.Errorf("statusMsg = %q, want no stale-fallback message on a fresh dispatch", nm.statusMsg)
	}
}

// TestUntrackConfirmDropsPendingLaunchForSameTool: a deferred fallback for
// the tool being untracked dies with the untrack — the dialog-closing enter
// funnels through flushPendingLaunch and would otherwise exec the
// now-untracked tool's command.
func TestUntrackConfirmDropsPendingLaunchForSameTool(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newTestModel(focusTools)
	m.mode = modeConfirmUntrack
	m.untrackTarget = "git"
	m.pendingLaunchName = "git"
	m.pendingLaunchCommand = "git status"
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := updated.(Model)
	if nm.pendingLaunchName != "" || nm.pendingLaunchCommand != "" {
		t.Errorf("pending = %q/%q, want dropped by the untrack", nm.pendingLaunchName, nm.pendingLaunchCommand)
	}
	if strings.Contains(nm.statusMsg, "tab open failed") {
		t.Errorf("statusMsg = %q, want no fallback message for the untracked tool", nm.statusMsg)
	}
	if cmd != nil && execMsgIn(cmd()) {
		t.Error("exec fallback fired for the tool that was just untracked")
	}
}

// TestRunInputInFlightGuard: while an adapter run is pending (launchingFor
// set), a second enter dispatches nothing and does not record lastRun — one
// launch at a time, mirroring updatingFor.
func TestRunInputInFlightGuard(t *testing.T) {
	clearTerminalEnv(t)
	m := newTestModel(focusTools)
	m.launchingFor = "yazi"
	nm, cmd := enterRun(m, "git status")
	if cmd != nil {
		t.Error("cmd != nil, want the dispatch blocked while a launch is in flight")
	}
	if len(nm.lastRun) != 0 {
		t.Errorf("lastRun = %v, want empty — the blocked command never ran", nm.lastRun)
	}
	if !strings.Contains(nm.statusMsg, "yazi") {
		t.Errorf("statusMsg = %q, want it naming the in-flight launch", nm.statusMsg)
	}
}

// TestLaunchDoneMsgSuccess: a clean adapter run reports "launched <name>"
// (mode-neutral — Terminal.app and tmux open a window, not a tab) and
// dispatches nothing further.
func TestLaunchDoneMsgSuccess(t *testing.T) {
	shrinkStatusTTL(t)
	m := newTestModel(focusTools)
	m.launchingFor = "git"
	updated, cmd := m.Update(launchDoneMsg{toolName: "git", command: "git status"})
	nm := updated.(Model)
	if nm.statusMsg != "launched git" {
		t.Errorf("statusMsg = %q, want %q", nm.statusMsg, "launched git")
	}
	if nm.launchingFor != "" {
		t.Errorf("launchingFor = %q, want the guard cleared", nm.launchingFor)
	}
	// Success dispatches nothing further — only the transient-status expiry tick.
	assertOnlyExpiryTick(t, cmd)
}

// TestExecDoneMsg: the ExecProcess callback result. A non-zero exit surfaces
// as a statusMsg and is never logged (the tool's exit status is not a keepkit
// anomaly); a clean exit is silent.
func TestExecDoneMsg(t *testing.T) {
	shrinkStatusTTL(t)
	t.Run("non-zero exit", func(t *testing.T) {
		logDir := t.TempDir()
		restore := logx.SetDirForTesting(logDir)
		defer restore()

		m := newTestModel(focusTools)
		updated, cmd := m.Update(execDoneMsg{toolName: "git", err: errors.New("exit status 1")})
		nm := updated.(Model)
		if want := "git exited: exit status 1"; nm.statusMsg != want {
			t.Errorf("statusMsg = %q, want %q", nm.statusMsg, want)
		}
		// The status shows, but nothing else is dispatched — only its expiry tick.
		assertOnlyExpiryTick(t, cmd)
		if out := logx.ReadAllForTesting(logDir); out != "" {
			t.Errorf("a tool's non-zero exit must not log, got:\n%s", out)
		}
	})
	t.Run("clean exit", func(t *testing.T) {
		m := newTestModel(focusTools)
		updated, cmd := m.Update(execDoneMsg{toolName: "git"})
		nm := updated.(Model)
		if nm.statusMsg != "" {
			t.Errorf("statusMsg = %q, want silence on a clean exit", nm.statusMsg)
		}
		if cmd != nil {
			t.Error("cmd != nil, want none")
		}
	})
	t.Run("command not found exit", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("needs the unix sh binary")
		}
		logDir := t.TempDir()
		restore := logx.SetDirForTesting(logDir)
		defer restore()

		// A real 127 from the same shell the fallback uses — the tool never
		// ran, so the message must say "not installed", not the exit status.
		err := exec.Command("sh", "-c", "exit 127").Run()
		m := newTestModel(focusTools)
		updated, cmd := m.Update(execDoneMsg{toolName: "git", err: err})
		nm := updated.(Model)
		if want := "git not found — is it installed?"; nm.statusMsg != want {
			t.Errorf("statusMsg = %q, want %q", nm.statusMsg, want)
		}
		// The status shows, but nothing else is dispatched — only its expiry tick.
		assertOnlyExpiryTick(t, cmd)
		if out := logx.ReadAllForTesting(logDir); out != "" {
			t.Errorf("a not-found exit must not log, got:\n%s", out)
		}
	})
}

// TestNotFoundExit pins the "command not found" classifier: only the shell's
// own not-found exit codes (127 sh, 9009 cmd.exe) qualify — an ordinary
// non-zero exit, a non-ExitError and nil all stay on the generic path.
func TestNotFoundExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs the unix sh binary")
	}
	exit127 := exec.Command("sh", "-c", "exit 127").Run()
	exit1 := exec.Command("sh", "-c", "exit 1").Run()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"shell not-found 127", exit127, true},
		{"ordinary failure 1", exit1, false},
		{"non-ExitError", errors.New("exit status 127"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notFoundExit(tt.err); got != tt.want {
				t.Errorf("notFoundExit(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestExecDoneCallback pins the ExecProcess completion mapping that
// execToolCmd hands to Bubble Tea: the callback must carry the tool name and
// the error into execDoneMsg (the exec message's callback field is unexported,
// so this is the only way to exercise it).
func TestExecDoneCallback(t *testing.T) {
	fail := errors.New("exit status 2")
	msg, ok := execDoneCallback("git")(fail).(execDoneMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T", msg)
	}
	if msg.toolName != "git" || !errors.Is(msg.err, fail) {
		t.Errorf("msg = %+v, want toolName git and the error carried", msg)
	}
	clean, ok := execDoneCallback("git")(nil).(execDoneMsg)
	if !ok || clean.err != nil {
		t.Errorf("clean exit mapping = %+v, want nil err", clean)
	}
}
