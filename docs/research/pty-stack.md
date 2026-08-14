# The pty stack behind the tool overlay (researched 2026-08)

Why keepkit runs a tracked tool on an embedded pseudo-terminal the way it does,
which libraries were considered, and what each pinned version is buying. This
outlives the implementation plan on purpose: the pins below are load-bearing and
a future reader upgrading them needs the reasons, not just the numbers.

## What the feature needs

Running `vim`, `yazi` or `fzf` *inside* keepkit's own screen means three things
at once, and no single library gives all three:

1. a **pty** — the tool must believe it owns a terminal, or it will not draw at
   all (`isatty` fails, ncurses/crossterm refuse to start);
2. a **terminal emulator** — something has to interpret the escape sequences the
   tool writes and hand keepkit a rectangle of styled cells it can paint into a
   Bubble Tea `View()`;
3. **Windows support**, because keepkit ships a Windows binary and its release
   workflow cross-compiles for it.

## Chosen: `charmbracelet/x/xpty` + `charmbracelet/x/vt`

| Module | Version | Why |
|---|---|---|
| `github.com/charmbracelet/x/xpty` | **v0.1.4** (tagged) | one `Pty` interface over unix ptys (via `creack/pty`) and Windows **ConPTY**; `WaitProcess` papers over the Go runtime's inability to `cmd.Wait()` a ConPTY child |
| `github.com/charmbracelet/x/vt` | **pseudo-version `v0.0.0-20260813141921-f091cedeaf78`** — untagged upstream | VT220 + truecolor emulator in pure Go: `NewEmulator(w, h)`, `Write` pty bytes in, `Render() string` out |

`vt` has **no tagged release**, so the pseudo-version is deliberate rather than
sloppy. Re-pin it only together with a run of the full suite — see *Dependency
blast radius* below.

### Rejected alternatives

- **`creack/pty` directly** — no Windows. ConPTY support has been proposed
  upstream for years and never merged, so choosing it would mean shipping a
  feature that silently does not exist on one of keepkit's three platforms.
- **`taigrr/bubbleterm`** — the closest thing to a drop-in, but it requires
  Bubble Tea **v2** and keepkit is on v1.3.10. Upgrading the whole TUI to v2 to
  get one feature is the tail wagging the dog. Its emulator↔`Model` wiring was
  read for ideas; nothing was vendored.

`vt` works fine under Bubble Tea v1 because `Render()` returns a plain ANSI
string — it needs no v2 rendering core to hand keepkit its screen.

## What the API actually looks like (verified, not assumed)

The implementation plan sketched this API from research; every item below was
re-checked against the pinned modules before a line of `internal/term` was
written, because the plan carried a stop condition for material drift.

Matches the sketch:

- `vt.NewEmulator(w, h) *Emulator`, `Write([]byte)`, `Render() string`,
  `Resize(w, h)` (no error), `CursorPosition() uv.Position`, `IsAltScreen()`.
- `xpty.NewPty(w, h)`, `Pty.Start(*exec.Cmd)`, `Resize`, `Read`/`Write`/`Close`.
- `xpty.WaitProcess(ctx, cmd)` — falls back to `cmd.Wait()` off Windows and
  synthesises the `*exec.ExitError` that `os.Process.Wait` fails to produce on
  ConPTY, so the exit code has the same shape on every platform.
- `ConPty.Start` populates `cmd.Process` (via `os.FindProcess`), so the kill
  path needs no Windows-specific detour.

### Drift #1 — there is no key encoder, and the modes it would need are unexported

The plan preferred translating a `tea.KeyMsg` into bytes and calling
`Session.Write` directly, because that needs no second goroutine and makes
"only `Update` touches the emulator" literally true. **That function does not
exist** in the pinned stack: `ultraviolet` exports only output-side encoders
(`EncodeCursorStyle`, `EncodeMouseMode`, …), and `x/ansi` has none either.

Writing our own was rejected on correctness, not effort. `vt`'s `SendKey`
encodes **against emulator state**:

```go
ack := e.isModeSet(ansi.ModeCursorKeys)    // DECCKM - arrows are \x1bOA, not \x1b[A
akk := e.isModeSet(ansi.ModeNumericKeypad) // DECNKM
```

`isModeSet` is unexported and no accessor surfaces it, so a hand-rolled encoder
could not know whether the child had switched to application cursor keys — the
mode `vim` and `less` set on entry. Arrows in vim are not a corner case for the
project's flagship feature. So keepkit uses `vt`'s own `SendKey`, which means
accepting the copy goroutine the plan listed as the fallback.

### Drift #2 — `xpty` does **not** make the child a session leader

The plan assumed "the pty's own `Setsid`+`Setctty`". It has neither:
`UnixPty.Start` wires `Stdin`/`Stdout`/`Stderr` to the slave and calls
`cmd.Start()`, and that is all. Without `Setsid`+`Setctty` the child inherits
keepkit's controlling terminal, job control never engages, and a
`SIGKILL`-to-the-process-group teardown would signal the wrong group.

`internal/term` therefore sets `SysProcAttr{Setsid: true, Setctty: true}`
itself, on the unix build only. `Ctty` defaults to 0, which is the child's
stdin — the slave that `xpty` just attached, so the default is the correct fd.

The invariant the plan derived from its wrong premise survives intact, and now
has a sharper reason: **never call `proc.DetachTTY` on the pty command.** It
would overwrite `SysProcAttr` wholesale, dropping `Setctty` and handing the
child a pty with no controlling terminal — the exact opposite of the point.

## Upstream issues we are living with

### charmbracelet/x#879 — `Emulator.Read`/`Close` data race

Real, reproduced here under `-race`, and it dictates the teardown:

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

A copy goroutine parked in `Read` plus a `Close()` from `Update` is a textbook
race, and `go test -race` reports it immediately. keepkit's suite runs `-race`,
so this is not a theoretical concern.

**The workaround: never call `Emulator.Close()`.** `InputPipe()` returns the
emulator's `*io.PipeWriter`; closing *that* unblocks the parked `Read` with
`io.EOF` through `io.Pipe`'s own synchronisation and never touches the
`e.closed` bool. Verified race-clean, and `TestSessionInputPipeCloseIsRaceFree`
in `internal/term` is what keeps the knowledge from being lost the next time
somebody reaches for the obvious `Close()`.

If a future `vt` fixes #879, the type assertion can go and `Close()` can come
back — the assertion falls back to `Close()` already if `InputPipe()` ever stops
returning an `io.Closer`.

### charmbracelet/x#935 — grapheme splitting

Wide graphemes split across two `Write` calls can render wrong. Not worked
around; the pty reader delivers large chunks and the failure is cosmetic and
transient. Tracked, not fixed here.

## Dependency blast radius

Pulling `vt` drags in `charmbracelet/ultraviolet` (the Bubble Tea v2 rendering
core) and bumps several modules that lipgloss and glamour already sat on — i.e.
**the dependency bump alone can move keepkit's render tests**, which is why the
gate for the dependency step was the full suite rather than the new package.

Measured on the bump (`go test ./...` before any feature code): **everything
stayed green.**

| Module | Before | After |
|---|---|---|
| `x/ansi` | v0.11.6 | v0.11.7 |
| `mattn/go-runewidth` | v0.0.19 | v0.0.23 |
| `clipperhouse/displaywidth` | v0.9.0 | v0.11.0 |
| `clipperhouse/uax29/v2` | v2.5.0 | v2.7.0 |
| `lucasb-eyer/go-colorful` | v1.3.0 | v1.4.0 |
| `charmbracelet/colorprofile` | v0.4.1 | v0.4.2 |
| `golang.org/x/sys` | v0.38.0 | v0.47.0 |
| `golang.org/x/sync` | v0.17.0 | v0.19.0 |
| `charmbracelet/ultraviolet` | — | v0.0.0-20260303162955-0b88c25f3fff (new) |

`go-runewidth` and `displaywidth` are the two to watch on any future re-pin:
keepkit measures glyph widths in half a dozen places (the gauge, the language
band, the list markers, `insetPanelTitle`'s border arithmetic) and pins several
of them with dedicated tests precisely because a width change is invisible until
a border tears.
