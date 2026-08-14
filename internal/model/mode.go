package model

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/stanlyzoolo/keepkit/internal/launcher"
	"github.com/stanlyzoolo/keepkit/internal/loader"
	"github.com/stanlyzoolo/keepkit/internal/updater"
	"github.com/stanlyzoolo/keepkit/internal/version"
)

// inputMode is the single input/modal state of the TUI. Exactly one mode is
// active at a time; modeNormal is the base state where the per-focus key map
// applies. modeTokenInput is a sub-state of the API-status overlay: it is
// entered from modeAPIStatus via [e] and exits back to modeAPIStatus, and the
// overlay stays visible in both (apiOverlayVisible).
type inputMode int

const (
	modeNormal         inputMode = iota
	modeSearch                   // "/" in focusTools: filter the tool list
	modeEditNote                 // "e" in focusBrief
	modeEditTags                 // "#" in focusBrief
	modeTrack                    // "t", global
	modeConfirmUntrack           // "u", global
	modeRename                   // "m", global
	modeRunInput                 // enter in focusTools: run the tool in a new terminal tab
	modeConfirmUpdate            // enter in focusBrief (or "U"): confirm the detected update command
	modeAPIStatus                // "a": rate-limit / token overlay
	modeTokenInput               // "e" inside the overlay: masked token entry
	modeHotkeys                  // "?": static hotkeys-help overlay
	modeToolOverlay              // enter in focusTools: the tool runs in an embedded terminal
)

// apiOverlayVisible reports whether the API-status overlay is on screen —
// true both while browsing it and while entering a token.
func (m Model) apiOverlayVisible() bool {
	return m.mode == modeAPIStatus || m.mode == modeTokenInput
}

// overlayVisible reports whether any modal overlay is composited over the
// layout — the [a] API-status overlay (incl. token entry), the [?] hotkeys
// overlay, or the embedded tool terminal. It is the single "modal on screen"
// predicate for View() and the mouse gate, so a new overlay only has to extend
// this one helper.
func (m Model) overlayVisible() bool {
	return m.apiOverlayVisible() || m.mode == modeHotkeys || m.mode == modeToolOverlay
}

// updateHotkeys handles keys while the [?] hotkeys overlay is open: esc, q, or
// a second ? closes it back to modeNormal; every other key is a no-op. The
// overlay is static (no scrolling), mirroring the updateAPIStatus close
// pattern. ctrl+c still quits — this handler runs before the global quit case,
// so it must honor the overlay's own "ctrl+c anywhere" hint explicitly.
func (m Model) updateHotkeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q", "?":
		m.mode = modeNormal
		return m, nil
	}
	return m, nil
}

func (m Model) updateNoteEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.mode = modeNormal
		m.noteInput.Blur()
		if mt, ok := m.selectedMeta(); ok {
			mt.Note = strings.TrimSpace(m.noteInput.Value())
			m.meta = loader.UpsertMeta(m.meta, mt)
			loader.SaveMeta(m.meta) //nolint:errcheck
		}
		m.briefViewport.SetContent(m.renderCard())
		return m, nil
	case "esc":
		m.mode = modeNormal
		m.noteInput.Blur()
		m.briefViewport.SetContent(m.renderCard())
		return m, nil
	default:
		var cmd tea.Cmd
		m.noteInput, cmd = m.noteInput.Update(msg)
		m.briefViewport.SetContent(m.renderCard())
		return m, cmd
	}
}

// parseTag turns the tags editor's raw input into the tool's single tag,
// holding the len<=1 invariant loader.LoadMeta migrates legacy entries to. A
// tool has one tag: everything after the first comma is dropped rather than
// stored as a second tag or as one comma-carrying tag, so typing "cli, foo"
// and loading a legacy ["cli","foo"] land on the same value ("cli") — the two
// paths must not produce different tag shapes for the same intent. Spaces
// inside a tag are fine ("dev tools"); empty input clears the tag (nil, so
// meta.yaml's omitempty drops the key).
func parseTag(raw string) []string {
	first, _, _ := strings.Cut(raw, ",")
	if first = strings.TrimSpace(first); first == "" {
		return nil
	}
	return []string{first}
}

func (m Model) updateTagsEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.mode = modeNormal
		m.tagsInput.Blur()
		if mt, ok := m.selectedMeta(); ok {
			mt.Tags = parseTag(m.tagsInput.Value())
			m.meta = loader.UpsertMeta(m.meta, mt)
			loader.SaveMeta(m.meta) //nolint:errcheck
			// The tag is the tag view's grouping key, so committing one can
			// move this tool to another section — under the cursor, which
			// indexes the reordered projection. Remap by name and repaint, the
			// same cursor-follows-the-tool rule the async merges use; the
			// repaint also rebuilds the line maps, which would otherwise still
			// describe the pre-edit list for the next click.
			m.metaSelected = m.indexOfMeta(mt.Name)
			m.setToolsContent()
		}
		m.briefViewport.SetContent(m.renderCard())
		return m, nil
	case "esc":
		m.mode = modeNormal
		m.tagsInput.Blur()
		m.briefViewport.SetContent(m.renderCard())
		return m, nil
	default:
		var cmd tea.Cmd
		m.tagsInput, cmd = m.tagsInput.Update(msg)
		m.briefViewport.SetContent(m.renderCard())
		return m, cmd
	}
}

// trackTool adds (or updates) a tracked tool from a GitHub URL or plain name.
// It returns the updated meta slice and a status message ("" on a fresh add,
// "already tracked" when the name was already present). Empty input is a no-op.
func trackTool(meta []loader.ToolMeta, input string) ([]loader.ToolMeta, string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return meta, ""
	}
	name, github, _ := loader.ParseToolRef(input)
	entry := loader.ToolMeta{
		Name:   name,
		GitHub: github,
		Status: loader.StatusTrying,
		Added:  loader.TodayDate(),
	}
	status := ""
	// UpsertMeta replaces the record wholesale, so a re-track must carry the
	// existing user-authored fields forward or SaveMeta makes their loss
	// permanent behind an "already tracked" that reads as a no-op. The status
	// reset to trying is the one deliberate change: re-tracking is "I'm trying
	// this again". A ref-less input keeps the known GitHub ref — typing the
	// bare name is not a request to forget the repo.
	if prev := loader.FindMeta(meta, name); prev != nil {
		status = "already tracked"
		entry.Note = prev.Note
		entry.Tags = prev.Tags
		entry.UpdateCmd = prev.UpdateCmd
		if github == "" {
			entry.GitHub = prev.GitHub
		}
		if prev.Added != "" {
			entry.Added = prev.Added
		}
	}
	return loader.UpsertMeta(meta, entry), status
}

func (m Model) updateTrackInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.mode = modeNormal
		m.trackInput.Blur()
		input := strings.TrimSpace(m.trackInput.Value())
		if input == "" {
			return m, nil
		}
		name, _, _ := loader.ParseToolRef(input)
		var status string
		m.meta, status = trackTool(m.meta, input)
		loader.SaveMeta(m.meta) //nolint:errcheck
		m.tools = loader.ToolsFromMeta(m.meta)
		// metaSelected is an index into filteredMeta(), whose update partition
		// reorders rows relative to m.meta — a raw m.meta index lands the
		// cursor on a foreign tool (updateTagsEdit's idiom).
		m.metaSelected = m.indexOfMeta(name)
		m.setToolsContent()
		m.briefViewport.GotoTop()
		m.briefViewport.SetContent(m.renderCard())
		return m, tea.Batch(m.setStatus(status), m.autoFetchCmdsForSelected())
	case "esc":
		m.mode = modeNormal
		m.trackInput.Blur()
		return m, nil
	default:
		var cmd tea.Cmd
		m.trackInput, cmd = m.trackInput.Update(msg)
		return m, cmd
	}
}

func (m Model) updateUntrackConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.mode = modeNormal
		// A deferred launch fallback for this same tool dies with the
		// untrack: this dialog-closing enter funnels through
		// flushPendingLaunch, which would otherwise exec the now-untracked
		// tool's command. A pending fallback for a *different* tool
		// deliberately survives and flushes on this keystroke — that launch
		// was requested before the dialog opened and its tool is still
		// tracked.
		if m.pendingLaunchName == m.untrackTarget {
			m.pendingLaunchName, m.pendingLaunchCommand = "", ""
		}
		m.meta = loader.RemoveMeta(m.meta, m.untrackTarget)
		loader.SaveMeta(m.meta) //nolint:errcheck
		m.tools = loader.ToolsFromMeta(m.meta)
		m.untrackTarget = ""
		// Keep metaSelected at the same index so selection lands on the next
		// item; clamp to the new last index (or 0 when the list is empty).
		if m.metaSelected > len(m.meta)-1 {
			m.metaSelected = max(len(m.meta)-1, 0)
		}
		m.setToolsContent()
		m.briefViewport.GotoTop()
		m.briefViewport.SetContent(m.renderCard())
		return m, m.autoFetchCmdsForSelected()
	default:
		// esc or any other key cancels.
		m.mode = modeNormal
		m.untrackTarget = ""
		return m, nil
	}
}

// renameTool changes a tracked tool's Name from old to newName, preserving its
// GitHub/Status/Tags/Note/Added fields. An empty newName (after trimming) or a
// newName equal to old is a no-op. A collision with another tracked tool's name
// is rejected with an error and leaves meta unchanged.
func renameTool(meta []loader.ToolMeta, old, newName string) ([]loader.ToolMeta, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" || newName == old {
		return meta, nil
	}
	if loader.FindMeta(meta, newName) != nil {
		return meta, fmt.Errorf("name already exists")
	}
	for i := range meta {
		if meta[i].Name == old {
			meta[i].Name = newName
			return meta, nil
		}
	}
	return meta, nil
}

func (m Model) updateRenameInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		mt, ok := m.selectedMeta()
		if !ok {
			m.mode = modeNormal
			m.nameInput.Blur()
			return m, nil
		}
		old := mt.Name
		newName := strings.TrimSpace(m.nameInput.Value())
		updated, err := renameTool(m.meta, old, newName)
		if err != nil {
			m.mode = modeNormal
			m.nameInput.Blur()
			return m, m.setStatus(err.Error())
		}
		m.mode = modeNormal
		m.nameInput.Blur()
		if newName == "" || newName == old {
			return m, nil
		}
		m.meta = updated
		loader.SaveMeta(m.meta) //nolint:errcheck
		m.tools = loader.ToolsFromMeta(m.meta)
		delete(m.helpCache, old)
		delete(m.repoCards, old)
		delete(m.versions, old)
		delete(m.repoStatus, old)
		delete(m.changelogData, old)
		delete(m.readmeData, old)
		delete(m.readmeLoading, old)
		delete(m.remoteAnswered, old)
		delete(m.lastRun, old)
		// Displayed index, not a raw m.meta one — the update partition can
		// reorder rows between the two (same rule as updateTrackInput).
		m.metaSelected = m.indexOfMeta(newName)
		m.setToolsContent()
		m.briefViewport.GotoTop()
		m.briefViewport.SetContent(m.renderCard())
		return m, m.autoFetchCmdsForSelected()
	case "esc":
		m.mode = modeNormal
		m.nameInput.Blur()
		return m, nil
	default:
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
}

// updateRunInput handles the one-line run prompt opened by enter in focusTools
// (modeRunInput): esc cancels back to modeNormal; enter with empty/whitespace
// input cancels too; enter with text records the command in m.lastRun and
// dispatches the launch. launcher.Detect is env-only (no subprocesses), so it
// is safe to call here on the Update thread: a fallback plan runs the tool in
// the current window via tea.ExecProcess, any other plan opens a terminal tab
// via startLaunchCmd (whose error handler auto-falls back to ExecProcess).
// Adapter dispatch sets m.launchingFor (one launch at a time) and a
// "launching <name> in <terminal>…" statusMsg — the in-flight feedback for the
// seconds an adapter can block.
func (m Model) updateRunInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.mode = modeNormal
		m.runInput.Blur()
		command := strings.TrimSpace(m.runInput.Value())
		if command == "" {
			return m, nil
		}
		mt, ok := m.selectedMeta()
		if !ok {
			return m, nil
		}
		// One launch at a time: an adapter run can stay pending up to
		// launchTimeout (osascript on the Automation dialog), and nothing else
		// stops a second enter+enter from dispatching concurrently. The
		// command is deliberately not recorded in lastRun — it never ran.
		if m.launchingFor != "" {
			// Sticky, not setStatus: this reports a launch that is *still in
			// progress* (overwritten by launchDoneMsg). Letting a timer expire it
			// mid-flight would hide the only sign the adapter is busy for up to
			// launchTimeout — including a timer armed by a transient status set
			// moments earlier, which is why the helper bumps statusSeq.
			m.setStickyStatus("still launching " + m.launchingFor)
			return m, nil
		}
		m.lastRun[mt.Name] = command
		// An explicit new dispatch supersedes a fallback deferred while this
		// prompt was open (a gated adapter failure landing mid-typing) —
		// flushing both would run two commands on this same keystroke.
		m.pendingLaunchName, m.pendingLaunchCommand = "", ""
		plan := launcher.Detect(command, mt.Name)
		if plan.Fallback {
			return m, execToolCmd(mt.Name, command)
		}
		// In-flight feedback: the adapter can block for seconds, and without a
		// statusMsg the prompt just closes and nothing visibly happens. Sticky,
		// not setStatus: this must not expire mid-flight — it is extinguished by
		// launchDoneMsg, not the clock, and a still-armed transient timer must
		// not retire it either (setStickyStatus bumps the seq for that).
		m.launchingFor = mt.Name
		m.setStickyStatus("launching " + mt.Name + " in " + plan.Terminal + "…")
		return m, startLaunchCmd(plan, mt.Name, command)
	case "esc":
		m.mode = modeNormal
		m.runInput.Blur()
		return m, nil
	default:
		var cmd tea.Cmd
		m.runInput, cmd = m.runInput.Update(msg)
		return m, cmd
	}
}

// launchFallbackStatus is the single definition of the tab-failure fallback
// message — shown by both the ungated launchDoneMsg auto-fallback (model.go)
// and the deferred flush below, which must stay byte-identical (tests pin the
// exact text).
func launchFallbackStatus(name string) string {
	return "tab open failed — running " + name + " here"
}

// flushPendingLaunch dispatches the exec fallback deferred by the
// launchDoneMsg mode gate (see the pendingLaunchName field doc). Every modal
// keystroke return in Update funnels through it: when the handler has just
// brought the model back to modeNormal and a gated tab-open failure is
// pending, the terminal is safe to seize again — the fallback fires now, with
// the same statusMsg the ungated auto-fallback shows. Set here, AFTER the
// blanket statusMsg reset and the mode handler, the message survives the
// mode-closing keystroke, and modeNormal's renderStatusBar actually paints it
// (every open mode's status-bar branch outranks statusMsg — a hint set at gate
// time was dead UI). Going straight to execToolCmd — never back through
// launcher.Detect — means the retry cannot re-run the known-failing adapter
// plan. While the mode stays open (or modeTokenInput fell back to
// modeAPIStatus) this is a no-op passthrough.
func flushPendingLaunch(mdl tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	// Plain assertion: every per-mode handler returns a Model value, and a
	// future handler returning a different concrete type should fail loudly
	// here, not silently skip the flush.
	m := mdl.(Model)
	if m.mode != modeNormal || m.pendingLaunchName == "" {
		return mdl, cmd
	}
	name, command := m.pendingLaunchName, m.pendingLaunchCommand
	m.pendingLaunchName, m.pendingLaunchCommand = "", ""
	return m, tea.Batch(cmd, m.setStatus(launchFallbackStatus(name)), execToolCmd(name, command))
}

// updateConfirmUpdate handles the modeConfirmUpdate dialog (modeled on
// modeConfirmUntrack): enter launches the update — set updatingFor, reset the
// live log to the target, and fire the streaming command plus the spinner tick;
// esc (or any other key) cancels back to modeNormal. The plan awaiting
// confirmation lives in m.updatePlan and the name it belongs to in
// m.updateTarget, so there is one path here for a tool and for keepkit itself:
// reading the name off the selection instead would silently cancel a
// self-update whenever the tracker is empty, and would retarget a tool's plan
// onto whatever row the user moved to while detection was running.
func (m Model) updateConfirmUpdate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.mode = modeNormal
		target := m.updateTarget
		m.updateTarget = ""
		if target == "" {
			return m, nil
		}
		m.updatingFor = target
		m.updateLog = nil
		m.updateLogFor = target
		// The previous session's terminal block goes with its buffer: the two are
		// rendered as one thing, so a stale "✓ finished" left under a log that has
		// just started would read as this update having already ended.
		m.updateOutcome = updateOutcome{}
		// keepkit's own log owns [3] under every selection — an untracked keepkit
		// has no row of its own (see showsUpdateLog) — while a tool's log is
		// per-tool sticky, so taking the single buffer over also releases a
		// finished self-update's claim on the panel. Read off selfUpdating(), which
		// is updatingFor (just set to target) through the feature's version gate:
		// with the self-update off, keepkit's row keeps the same per-tool stickiness
		// as any other row.
		m.selfUpdateLog = m.selfUpdating()
		m.briefViewport.SetContent(m.renderCard())
		// Text-change transition: [3] switches from help to the live log, so
		// the entry index empties and any spotlight cursor resets.
		m.setHelpContent()
		return m, tea.Batch(
			m.spinner.Tick,
			startUpdateCmd(m.updatePlan, target),
		)
	default:
		// esc or any other key cancels. The plan goes with the target: the two
		// are written together at the single detect site and read together by the
		// confirm bar, so "no pending plan" has to mean the same thing here as it
		// does on the path where the result was dropped before it ever landed.
		m.mode = modeNormal
		m.updateTarget = ""
		m.updatePlan = updater.Plan{}
		return m, nil
	}
}

// updateAPIStatus handles keys while the API-status overlay is open: [e] opens
// the masked token-input sub-mode, [d] removes a config-sourced token, [r]
// refreshes the numbers, [esc] closes. While entering a token, submit validates
// via version.FetchRateWithToken before anything is persisted.
func (m Model) updateAPIStatus(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeTokenInput {
		switch msg.String() {
		case "enter":
			candidate := strings.TrimSpace(m.tokenInput.Value())
			if candidate == "" {
				m.mode = modeAPIStatus
				m.tokenInput.Blur()
				m.tokenError = ""
				return m, nil
			}
			// Validate the candidate against /rate_limit; SetToken runs only
			// after a 200 in the tokenValidatedMsg handler.
			return m, validateTokenCmd(candidate)
		case "esc":
			m.mode = modeAPIStatus
			m.tokenInput.Blur()
			m.tokenInput.SetValue("")
			m.tokenError = ""
			return m, nil
		default:
			var cmd tea.Cmd
			m.tokenInput, cmd = m.tokenInput.Update(msg)
			return m, cmd
		}
	}
	switch msg.String() {
	case "esc", "q":
		m.mode = modeNormal
		m.tokenError = ""
		return m, nil
	case "r":
		return m, fetchRateCmd()
	case "e":
		m.mode = modeTokenInput
		m.tokenError = ""
		m.tokenInput.SetValue("")
		m.tokenInput.Focus()
		return m, textinput.Blink
	case "d":
		if version.TokenSource() == "config" {
			version.ClearToken() //nolint:errcheck
			return m, fetchRateCmd()
		}
	}
	return m, nil
}
