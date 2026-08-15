# The tool overlay: running a tracked tool inside keepkit

`enter` in `[1] tools` opens a one-line prompt, and the command you type runs on
a **pseudo-terminal inside keepkit** — a bordered block over the dimmed layout,
with the whole keyboard handed to the tool. `vim` draws in it and edits; `fzf`
filters in it; `rg --version` prints two lines and stops, and the block stays
put until `esc`. There is one path for all of them.

This replaced a tab launcher that scripted *someone else's* terminal into
opening a tab (`internal/launcher`: tmux, iTerm2, Terminal.app, kitty, WezTerm,
plus a `tea.ExecProcess` fallback and an auto-fallback for when an adapter
failed). That whole package and its recovery machinery are gone; what follows is
why the current shape is the way it is.

Read this before changing anything in `internal/term`, `internal/model/overlay_term.go`,
or the `enter` dispatch in `updateRunInput`.

## The one rule everything else follows

**Only `Update` touches the emulator's screen state.**

`vt.Emulator` lives on `Model`, not inside `internal/term`. The pty is read by a
single goroutine that posts bytes as `tea.Msg`s — the same `waitForChunkCmd`
pattern the update streamer uses — and the `termChunkMsg` handler is the only
place that calls `Write`. `Render`, `Resize` and `CursorPosition` are likewise
reached from `Update`/`View` and nowhere else.

The rule exists because x/vt's screen buffer is not synchronised and the library
carries an open race issue (charmbracelet/x#879). Keeping every screen-touching
call on Bubble Tea's own goroutine sidesteps the whole class instead of guarding
against it call by call.

The **input** path is the one exception, and it is a deliberate, bounded one —
see below.

## Why input needs a goroutine after all

The plan preferred translating a `tea.KeyMsg` into bytes and calling
`Session.Write` directly: no second goroutine, and the rule above becomes
literally true. That turned out to be impossible to do *correctly*.

`vt.SendKey` encodes against emulator state:

```go
ack := e.isModeSet(ansi.ModeCursorKeys)    // DECCKM: arrows are \x1bOA, not \x1b[A
akk := e.isModeSet(ansi.ModeNumericKeypad) // DECNKM
```

`isModeSet` is unexported and nothing surfaces it, and no key encoder is
exported anywhere in the pinned stack (`ultraviolet` has only output-side
`Encode*` helpers). A hand-rolled encoder could not know whether the child had
switched to application cursor keys — the mode `vim` and `less` set on entry.
Arrows in vim are not a corner case for this feature.

So keys go through the emulator, and `termInput` is the goroutine that reads
them back out of its input pipe and writes them to the pty. It touches no screen
state.

Translation splits three ways, by what actually depends on emulator state:

| Keys | How | Why |
|---|---|---|
| runes, space | `SendText` | printable text, no encoding decision |
| arrows, Home/End, PgUp/PgDn, Insert/Delete, shift+tab, F1–F12 | `SendKey` | DECCKM/DECNKM apply |
| the control range (`KeyEnter`, `KeyEsc`, `KeyCtrlA` …) | the byte itself | Bubble Tea's control key *types are* the control bytes, and none of them is mode-dependent — writing the byte is exactly what vt's encoder produces |
| ctrl/shift-modified cursor keys | hand-encoded `CSI 1;<mod><final>` | **x/vt encodes none of them** — its `SendKey` default emits nothing when `Mod != 0`, which would silently swallow `ctrl+left` in every editor. Safe to write out because, unlike the bare arrows, the modified forms do not depend on DECCKM |
| a paste (`KeyRunes` with `Paste: true`) | `Emulator.Paste` | one block of text, not typed keys: `Paste` brackets it with the `?2004` markers exactly when the tool set that mode — without them vim auto-indents every pasted line — and passes it bare to a tool that never asked |

`Alt` is a Bubble Tea flag rather than a key: it becomes an ESC prefix (a paste takes no prefix — it has no modifier).

### Teardown: never call `Emulator.Close()`

This is the sharp edge, and it is load-bearing:

```go
func (e *Emulator) Read(p []byte) (int, error) {
	if e.closed { return 0, io.EOF }   // unsynchronised read
	return e.pr.Read(p)
}

func (e *Emulator) Close() error {
	e.closed = true                    // unsynchronised write
	return e.pw.CloseWithError(io.EOF)
}
```

A relay parked in `Read` plus a `Close()` from `Update` is charmbracelet/x#879,
and `go test -race` reports it immediately — reproduced here, which is why the
workaround is not speculative.

**`InputPipe()` returns the emulator's `*io.PipeWriter`; closing *that* unblocks
the parked `Read` through `io.Pipe`'s own synchronisation and never touches the
bool.** `termInput.stop` does exactly that, falling back to `Close()` only if a
future x/vt stops handing out an `io.Closer`.
`TestSessionInputPipeCloseIsRaceFree` is what keeps the next reader from
reaching for the obvious teardown.

`closeToolOverlay` is the single teardown and its order matters: **session first**
(so a relay parked in `Session.Write` fails fast), then stop the relay and
*wait* for it, then drop the emulator. Reversing the first two can park the
relay in a write nobody will drain.

## esc belongs to the tool

While the tool runs, **every** key is forwarded — `esc`, `ctrl+c`, `q`,
everything. `esc` is what makes vim usable; `ctrl+c` is the tool's interrupt and
must not be keepkit's quit. That is the whole meaning of "the tool has the
keyboard", and it is why `updateToolOverlay` consumes keys it has no use for
instead of letting them fall through to the normal-mode map.

**`ctrl+\` is the one reserved chord.** It kills the process group and *stays in
the mode*: the verdict arrives as `termExitMsg`, and that is what turns the
overlay into its outcome line. Leaving on the keypress would throw away the log
the user killed something in order to read.

Once the tool has exited the overlay is a still image: `esc` closes it, every
other key does nothing, and nothing is forwarded because there is nothing left
to forward to.

Quitting keepkit while the overlay is open is unreachable by design. The way out
is quitting the tool (or `ctrl+\`), then `esc`. If keepkit itself is killed, the
closing pty master HUPs the child — accepted.

## Geometry, and the two things that must not move

The block is 70% of the screen in both directions, centred by `ui.PlaceOverlay`.
`Styles.OverlayBorder` is a rounded border plus `Padding(0, 1)`, so the emulator
body is `outerW - 4` by `outerH - 2 - 2` — the two rows being the title and the
exit row. At the 80×24 baseline that is **52×11**, and the baseline succeeding
is the number that mattered.

Two invariants keep the block still, and they fail differently:

- **The exit row is reserved from the start** — blank while the tool runs. Without
  it the block would grow by a row at the moment the tool finishes and
  `PlaceOverlay` would re-centre it vertically.
- **Every row is padded to the body width.** lipgloss sizes a border to its
  widest line, so before this the block measured 54 cells running and 53 exited
  (the title loses its `ctrl+\ kill` hint) and jumped sideways at the exact
  moment the user starts reading the outcome. `termRow` clamps both directions,
  ANSI-safely; the case it really exists for is a tool name longer than the body,
  which would otherwise push the frame past the screen edge where `PlaceOverlay`
  clips silently.

`termGeometry` returns the size **and** a separate `ok`, because the two callers
want different things: the keypress refuses on `!ok` (`terminal too small to run
a tool`, no `lastRun` write — a launch that never started is not remembered),
while a terminal shrunk *mid-session* clamps to the floor and keeps the tool
running. Resizing somebody's editor into nothing is bad; killing it outright is
worse. `!m.ready` is checked first and explicitly, the `toggleZoom` idiom: before
the first `WindowSizeMsg` every dimension is zero and the percentages would agree
on a floor-sized overlay by coincidence rather than by measurement.

Resize propagation hangs off **`applyLayout`**, the single relayout point, so it
cannot drift from it; the emulator is re-laid-out first and the pty told second,
so the child's redraw lands on a screen that is already the right shape.

## The cursor

The child's cursor is a reverse-video cell while the tool runs, hidden once it
has exited — a cursor on a dead screen invites typing. The pinned x/vt exposes
`CursorPosition()` but no visibility accessor, so alive/exited is the whole rule.

The splice goes through `ansi.Truncate`/`ansi.TruncateLeft`, which cut by
**visible column** and keep the SGR runs on both sides intact. A rune-index cut
would land inside an escape sequence, which the terminal then executes. Padding
happens *before* the splice for a related reason: on a short line, a cursor past
its end would otherwise be spliced in right after the last glyph instead of
where the tool actually put it.

`ui.Styles.TermCursor` is the only style in the set naming no theme colour, and
that is the point — reverse video swaps whatever the *tool* painted into that
cell, which is the only way one style can mark a cursor on a screen keepkit does
not control. A terminal with no SGR support shows no cursor; a terminal with no
SGR support cannot show a cursor block anyway.

## The pty session

`internal/term` is the architectural slot `internal/launcher` vacated: bottom of
the import graph, no TUI knowledge, no config paths, no `logx` — a failure rides
`Exit.Err` to the caller, which is the surface that can actually show it. That is
why it needs no `TestMain` seam.

`Session` owns one pty and one reader goroutine. Events leave through
`Events() <-chan Event`: zero or more `Data`, then exactly one `Exit`, then the
channel closes. The channel is buffered (64) so the reader stays ahead of a
consumer that drains once per Bubble Tea message; past it the reader blocks and
the child is throttled, which is the correct back-pressure.

Three rules inside it:

- **A pty read error is never the verdict.** A master whose child has exited
  answers `EIO` on Linux and EOF on macOS, and one closed by `Kill`/`Close`
  answers `os.ErrClosed`. All three are normal termination; what happened comes
  from `xpty.WaitProcess`, which also synthesises the `*exec.ExitError` that
  `os.Process.Wait` fails to produce on ConPTY.
- **`Elapsed` and `Killed` are stamped in the goroutine.** `time.Now()` inside
  `Update` would make completion non-deterministic in tests, and only the
  session knows whether an exit followed a kill — making the model correlate its
  own keypress against `signal: killed` would be guesswork.
- **Never call `proc.DetachTTY` on the pty command.** xpty sets neither `Setsid`
  nor `Setctty` (its `Start` only wires stdio), so `internal/term` sets them
  itself; `DetachTTY` assigns `SysProcAttr` wholesale and would drop them,
  leaving the child with no controlling terminal — the exact opposite of the
  point. `Setsid` is also what makes the unix child a process-group leader, so
  `proc.KillGroup`'s negative-pid signal reaches the tools an `sh -c` line
  started.

The model builds argv with its existing `shellCommand`, which is what keeps
`internal/term` goos-agnostic.

## Message flow, and the ordering that is easy to break

```
enter (prompt) → startTermCmd ──► termStartedMsg{session}
                                       │  Update creates the emulator here
                                       ▼
                            waitForTermChunkCmd ──► termChunkMsg{data, exit?}
                                       ▲                    │
                                       └────────────────────┘
                                                            │ exit != nil
                                                            ▼
                                                      termExitMsg
```

`waitForTermChunkCmd` folds everything already queued into one message — a
full-screen redraw arrives as several reads and repainting once per read would
spend a frame on each.

**The drain carries the exit rather than swallowing it.** A Go channel cannot be
un-read, so when the drain runs into the final event it has nowhere to put it
back; it rides on `termChunkMsg.exit`, the handler writes the data first and
re-emits the exit as the *next* message. Delivering the exit before the data it
followed would lose a short-lived tool's final screen, which is precisely what
esc-after-exit exists to show.

Two guards that are structural, not cosmetic:

- **A chunk whose session is not the current one is dropped without
  re-subscribing.** A dead session's channel must not keep a command chain alive.
- **A `termStartedMsg` arriving outside the mode is killed and closed, not
  adopted.** A start that lost its race with `esc` would otherwise strand a live
  pty nothing can reach.
- **Keys before `termStartedMsg` are dropped.** The window is milliseconds, but a
  nil deref there would re-panic through `logx.Recover` and take keepkit down.

## The outcome line

The reserved row fills in with one of four verdicts, built from the update
block's own helpers (`formatElapsed`, `fitCells`, `footerSep`):

```
✓ exited · 12s · esc close
✕ exit 3 · 4s · esc close
✕ killed · 1m30s · esc close
✕ failed to start · no such file · esc close
```

A start failure spends the middle cell on its reason rather than on an elapsed
it does not have. There is **no status message** on exit: the screen the tool
left behind is the answer and this row is the caption on it. The status bar gets
a branch of its own for the same reason the siblings have one — without it the
bar would go on advertising six global keys the running tool has taken over,
including a `q quit` that cannot fire.

A tool that is not installed needs no special handling: `sh` reports it and exits
127, which the `✕ exit 127` line shows. That replaced the old `notFoundExit`
mapping the tab launcher needed.

## What was deleted, and what that bought

`internal/launcher` (the five-adapter detection chain, `Plan`, `appleScriptQuote`)
and, in the model: `startLaunchCmd`, `execToolCmd`, `launchDoneMsg`/`execDoneMsg`,
`m.launchingFor`, `pendingLaunchName`/`Command`, `flushPendingLaunch` and every
modal-return call site it wrapped, `launchTimeout`, `launchFallbackStatus`,
`notFoundExit`, and `setStickyStatus` (whose only callers were the two launch
statuses).

Most of that existed to survive an adapter failing — a fallback, a deferred
fallback for when the fallback could not seize the terminal safely, a guard so
two of them could not run at once, and a sticky status because the in-flight
message had to outlive its own timer. None of it has anything to answer now: the
overlay is keepkit's own screen, there is no adapter to fail and no terminal to
seize. One mode replaced the lot.

`shellCommand` survives and now builds argv for the pty. Its deliberate duplicate
in `updater.customPlan` still must not drift.

## Known limits

- **Windows works via ConPTY but is untested at runtime** — CI only
  cross-compiles, the same level of assurance as `restart_windows`. `ConPty.Start`
  populates `cmd.Process`, so `proc.KillGroup`'s Windows branch needs no change.
- **Mouse is not proxied.** `SendMouse` exists; nothing wanted it yet.
- **Child OSC title and bell events are ignored.**
- **Wide graphemes split across two reads can render wrong** (charmbracelet/x#935).
  The reader delivers large chunks and the failure is cosmetic and transient.
- **Working directory is inherited** from keepkit, not from anything the tool
  chose.

`docs/research/pty-stack.md` records why this stack was chosen, what was
rejected, and every pinned version's reason.
