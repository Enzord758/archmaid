// Package runview is the Run page's state, in plain Go: the adapter from
// engine Events to what the page shows (Model), and the pump that carries a
// Driver's events to the GTK main thread in batches (Pump). Nothing here
// imports gotk4, so all of it is tested with plain `go test`, no display.
package runview

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/plural"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// OutputCap is how many output lines the view holds. Older lines leave the
// view (the engine's log keeps every line).
const OutputCap = 10000

// RowState is a checklist row's state.
type RowState int

const (
	RowPending RowState = iota
	RowRunning
	RowOK
	RowFailed
	RowSkipped
	RowAuthRefused
	RowAborted
	// RowNotRun: the action ended (an Abort) with the step still pending;
	// the engine sends such steps no event (Finished.NotRun counts them).
	RowNotRun
)

var rowStateNames = [...]string{"pending", "running", "ok", "failed", "skipped", "auth refused", "aborted", "not run"}

func (s RowState) String() string {
	if s < 0 || int(s) >= len(rowStateNames) {
		return "?"
	}
	return rowStateNames[s]
}

// Icon is the symbolic icon name for the state (from the Adwaita icon
// theme), "" for the running state, which shows a spinner instead.
func (s RowState) Icon() string {
	switch s {
	case RowRunning:
		return ""
	case RowOK:
		return "emblem-ok-symbolic"
	case RowFailed:
		return "dialog-error-symbolic"
	case RowSkipped:
		return "action-unavailable-symbolic"
	case RowAuthRefused:
		return "system-lock-screen-symbolic"
	case RowAborted:
		return "process-stop-symbolic"
	case RowNotRun:
		return "content-loading-symbolic"
	}
	return "radio-symbolic"
}

// Row is one checklist row: a step of the plan.
type Row struct {
	ID, Title string
	// LogLabel is what the step's log entry calls it ("Old package cache
	// cleaned"): a finished step's summary when its output had nothing.
	LogLabel string
	// Category is the log the step writes to (its failure goes there):
	// the failed step's Open log.
	Category logstore.Category
	// NeedsPriv: the step needs root; Optional: "no" at its confirm skips
	// it rather than ending the queue. Verb is the root helper's verb for
	// it, "" for a step that gets root through its own sudo (PrivTag).
	NeedsPriv, Optional bool
	Verb                string
	State               RowState
	Elapsed             time.Duration
	Err                 string
	// Waiting: polkit's agent has the user's attention for this step (the
	// root helper started through pkexec and nothing came back yet).
	Waiting bool
	// Faillock is the faillock count read before this helper step, if it
	// was readable: a warning on the row when one more failure locks the
	// account, the unlock time when it is locked.
	Faillock *engine.FaillockState
	// Last is the step's last non-blank output line, as plain text (kept
	// even after the line has left the view): its summary once it is done.
	Last string
}

// Summary is the one line a finished step shows while its output is
// collapsed (P8's timeline): a failed step's error, else its last output
// line; a step that printed nothing reads as its log label. Steps that
// didn't run say why, as their Subtitle does; a pending or running step
// has no summary.
func (r Row) Summary() string {
	switch r.State {
	case RowFailed:
		if r.Err != "" {
			return r.Err
		}
		return r.Last
	case RowOK:
		if r.Last != "" {
			return r.Last
		}
		return r.LogLabel
	case RowPending, RowRunning:
		return ""
	}
	return r.Subtitle()
}

// LastAttempt reports whether one more failed authorisation locks the
// account.
func LastAttempt(f engine.FaillockState) bool {
	return !f.Locked && f.Deny > 0 && f.Failures == f.Deny-1
}

// lockText is the note for a locked account: when it unlocks, if
// pam_faillock unlocks it on its own.
func lockText(f engine.FaillockState) string {
	if f.Unlock.IsZero() {
		return "account locked"
	}
	return "account locked until " + f.Unlock.Local().Format("15:04")
}

// Subtitle is the row's second line: its state and time.
func (r Row) Subtitle() string {
	switch r.State {
	case RowPending:
		return ""
	case RowRunning:
		if r.Waiting {
			if r.Faillock != nil && LastAttempt(*r.Faillock) {
				return "Waiting for authorisation… · one more failure locks your account"
			}
			return "Waiting for authorisation…"
		}
		if r.Elapsed > 0 {
			return "Running · " + fmtElapsed(r.Elapsed)
		}
		return "Running"
	case RowFailed:
		if r.Err != "" {
			return "Failed · " + fmtElapsed(r.Elapsed) + " · " + r.Err
		}
		return "Failed · " + fmtElapsed(r.Elapsed)
	case RowSkipped:
		return "Skipped"
	case RowAuthRefused:
		// A refused or cancelled polkit dialog, a locked account, three
		// wrong passwords: the user's call or the account's state, not an
		// error.
		// Err is set only when no polkit agent could ask
		// (engine.ErrNoAuthAgent): then no dialog was shown at all.
		if r.Faillock != nil && r.Faillock.Locked {
			return "Not authorised · " + lockText(*r.Faillock)
		}
		if r.Err != "" {
			return "Not authorised · " + r.Err
		}
		return "Not authorised"
	case RowAborted:
		return "Stopped · " + fmtElapsed(r.Elapsed)
	case RowNotRun:
		return "Not run"
	}
	return "Done · " + fmtElapsed(r.Elapsed)
}

func fmtElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}

// Change is what one Apply changed, for the widgets to redraw exactly that.
type Change struct {
	// Reset: the plan was replaced (QueueStarted); rebuild every row.
	Reset bool
	// Inserted are rows inserted, as indexes at the time of each insert,
	// in order; Rows are rows whose content changed (indexes after all
	// inserts), each once.
	Inserted []int
	Rows     []int
	// OutTrim lines leave the tail of the output view (a step redrew
	// them: OutputChunk.Replace), then OutRemove lines leave its head,
	// then OutAppend lines join its tail; after that the view holds
	// Model.Output().
	OutTrim   int
	OutRemove int
	OutAppend []string
	// Steps is the same output diff grouped by step, for a view that
	// shows each step's output under its own header (P8's timeline): in
	// order, each StepOut removes lines from the head of its step's group,
	// then appends to its tail. The "── title" line a step starts with is
	// not in it (the timeline draws its own header); every other line is.
	Steps []StepOut
	// Opened is the prompt opened in this batch and still open at its end;
	// Closed are prompts closed by the engine (PromptClosed).
	Opened *Prompt
	Closed []engine.PromptID
	// Status: Phase, Status, Notice or the counters changed. Finished: the
	// action ended in this batch.
	Status, Finished bool
	// Toasts are one-off messages to pop up (ReportAccepted's text).
	Toasts []string
}

// Model is the Run page's state. It is not safe for concurrent use: the
// GTK main thread owns it.
type Model struct {
	Action   string
	DryRun   bool
	Rows     []Row
	Phase    engine.Phase
	Busy     string
	Running  bool
	Finished *engine.Finished
	// Stopped is EngineStopped once the Driver's events ended without a
	// Finished (Stop); "" otherwise.
	Stopped string
	Notice  string
	// Discovery is the action's own discovery result when it ended there
	// (nothing found, or the look failed): the page shows it as the run of
	// one "Discovery" step. nil for any other run.
	Discovery *engine.DiscoveryResult
	// StartDry: the action being started is a dry run (the window knows;
	// a DiscoveryResult doesn't say).
	StartDry bool
	Prompt   *Prompt // the open prompt, or nil
	Stat     engine.StatusUpdated
	Logs     []engine.LogWritten
	// Auth is the open AuthWaiting, or nil. A WaitPolkit one also marks
	// its step's row Waiting until AuthResolved.
	Auth *engine.AuthWaiting
	// Faillock is the latest faillock count read (before a polkit helper
	// step), or nil. pendingLock is it until the step it was read for
	// starts or is refused, which then carries it.
	Faillock    *engine.FaillockState
	pendingLock *engine.FaillockState
	// warned is the count last warned about in a toast, so each helper
	// step doesn't repeat the same warning.
	warned engine.FaillockState

	// Lines counts the output lines of the current run (a new queue or a
	// discovery result starts from zero); Dropped those the Driver or
	// the pump dropped before they reached the view.
	Lines, Dropped int
	// Started is when the current queue was planned (QueueStarted.Started).
	Started time.Time

	out ring
	// view is the window [viewLo, viewHi) of the output sequence the
	// widgets hold, as of the last Apply.
	viewLo, viewHi int
	// redrawn: lines from sequence number redrawFrom on were taken back
	// off the ring (a redraw) since the last sync.
	redrawn    bool
	redrawFrom int
	// base is the first sequence number the view may show: a new run
	// moves it to the end (Clear, QueueStarted), so the page starts empty.
	base int
	// runs is the view's lines grouped as they came: consecutive lines of
	// one step (or one step's header), oldest first from runHead. It is
	// what lets a line that has left the ring still be removed from its
	// step's group.
	runs    []run
	runHead int
}

// NewModel is an empty Model.
func NewModel() *Model { return &Model{out: ring{buf: make([]entry, OutputCap)}} }

// Output is the lines the view holds, oldest first, as plain text.
func (m *Model) Output() []string { return m.out.since(m.lowest()) }

// lowest is the oldest sequence number the view can hold.
func (m *Model) lowest() int { return max(m.out.high-len(m.out.buf), m.base) }

// Clear empties the page (the owner's decision Q12): no rows and no
// output; the last run's output is in Logs. QueueStarted does the same for
// a new run. The window no longer calls it when an action starts (P8 row
// 13): an action that ends before its queue keeps the last run's page.
func (m *Model) Clear() Change {
	m.Rows = m.Rows[:0]
	m.base = m.out.total
	c := Change{Reset: true}
	c.OutTrim, c.OutRemove, c.OutAppend, c.Steps = m.sync()
	return c
}

// ViewLine is line i (0 = the oldest) of the view as of the last Apply,
// with its colour spans; false when i is out of range.
func (m *Model) ViewLine(i int) (Line, bool) {
	seq := m.viewLo + i
	if i < 0 || seq >= m.viewHi || seq < m.out.high-len(m.out.buf) {
		return Line{}, false
	}
	e := m.out.buf[seq%len(m.out.buf)]
	if e.raw == e.text {
		return Line{Text: e.text}, true
	}
	return ParseSGR(e.raw), true
}

// row finds the row for a step: by ID (the contract's key), else by index.
func (m *Model) row(id string, index int) int {
	if index >= 0 && index < len(m.Rows) && m.Rows[index].ID == id {
		return index
	}
	for i, r := range m.Rows {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// Prompt is an open prompt: exactly one of its pointers is set.
type Prompt struct {
	ID        engine.PromptID
	Confirm   *engine.NeedsConfirm
	Password  *engine.NeedsPassword
	Selection *engine.NeedsSelection
	Report    *engine.OutputReport
}

// Answer claims the open prompt id for the user's answer: true when id is
// the open prompt, which it then closes; false for a stale id (withdrawn,
// already answered, or replaced by a newer prompt). The page sends an
// answer only on true, so a stale ID never reaches the engine.
func (m *Model) Answer(id engine.PromptID) bool {
	if m.Prompt == nil || m.Prompt.ID != id || id == 0 {
		return false
	}
	m.Prompt = nil
	return true
}

func (m *Model) open(c *Change, p Prompt) {
	m.Prompt, c.Opened = &p, &p
}

// Apply folds a batch of events, in order, into the model and says what
// changed.
func (m *Model) Apply(evs []engine.Event) Change {
	var c Change
	touched := map[int]bool{}
	touch := func(i int) {
		if i >= 0 {
			touched[i] = true
		}
	}
	for _, ev := range evs {
		switch ev := ev.(type) {
		case engine.QueueStarted:
			m.Action, m.DryRun, m.Running, m.Finished, m.Notice, m.Stopped = ev.Action, ev.DryRun, true, nil, "", ""
			m.Discovery = nil
			m.Lines, m.Dropped = 0, 0
			m.Rows, m.Auth, m.pendingLock, m.Started = m.Rows[:0], nil, nil, ev.Started
			// A new run starts with an empty page (Q12); one action
			// plans one queue (Repair's reinstall is inserted).
			m.base = m.out.total
			for _, s := range ev.Steps {
				m.Rows = append(m.Rows, newRow(s))
			}
			c.Reset, c.Inserted = true, nil
			clear(touched)
			c.Status = true
		case engine.DiscoveryResult:
			if ev.Outcome == engine.DiscoveryFound {
				// The queue it leads to is the run.
				continue
			}
			m.discover(ev)
			c.Reset, c.Inserted, c.Status = true, nil, true
			clear(touched)
		case engine.StepInserted:
			i := min(max(ev.Index, 0), len(m.Rows))
			m.Rows = append(m.Rows[:i], append([]Row{newRow(ev.Step)}, m.Rows[i:]...)...)
			shifted := map[int]bool{}
			for j := range touched {
				if j >= i {
					j++
				}
				shifted[j] = true
			}
			touched = shifted
			if !c.Reset {
				c.Inserted = append(c.Inserted, i)
			}
		case engine.PlanChanged:
			if i := m.row(ev.Step.ID, ev.Index); i >= 0 {
				m.Rows[i].Title, m.Rows[i].NeedsPriv, m.Rows[i].Optional = ev.Step.Title, ev.Step.NeedsPriv, ev.Step.Optional
				m.Rows[i].Verb = ev.Step.Verb
				m.Rows[i].LogLabel = ev.Step.LogLabel
				touch(i)
			}
		case engine.StepStarted:
			if i := m.row(ev.ID, ev.Index); i >= 0 {
				m.Rows[i].State, m.Rows[i].Elapsed = RowRunning, 0
				m.Rows[i].Faillock, m.pendingLock = m.pendingLock, nil
				touch(i)
				head := "── " + m.Rows[i].Title
				m.out.push(entry{raw: head, text: head, step: ev.ID, head: true})
			}
		case engine.OutputChunk:
			// A redraw takes the step's last lines back first: no more
			// than it has of them since its last gap (redrawable).
			k := m.redrawable(ev.ID, ev.Replace)
			if k > 0 {
				m.out.total -= k
				if !m.redrawn || m.out.total < m.redrawFrom {
					m.redrawn, m.redrawFrom = true, m.out.total
				}
			}
			if ev.Dropped > 0 {
				m.Dropped += ev.Dropped
				note := "… " + plural.N(ev.Dropped, "line", "lines") + " not shown (they are in the log)"
				m.out.push(entry{raw: note, text: note, step: ev.ID, note: true})
			}
			m.Lines += len(ev.Lines) + ev.Dropped - k
			for _, l := range ev.Lines {
				m.out.push(entry{raw: l, text: StripSGR(l), step: ev.ID})
			}
			if last := m.lastText(len(ev.Lines)); last != "" {
				// Not touched: the summary shows only once the step is
				// done, and StepDone touches the row then.
				if i := m.row(ev.ID, ev.Index); i >= 0 {
					m.Rows[i].Last = last
				}
			}
			c.Status = true
		case engine.StepDone:
			if i := m.row(ev.ID, ev.Index); i >= 0 {
				m.Rows[i].State, m.Rows[i].Elapsed, m.Rows[i].Err = doneState(ev.Status), ev.Elapsed, ev.Err
				m.Rows[i].Waiting = false
				if m.pendingLock != nil {
					// Refused without starting: the account is locked.
					m.Rows[i].Faillock, m.pendingLock = m.pendingLock, nil
				}
				touch(i)
			}
		case engine.Aborted:
			if i := m.row(ev.ID, ev.Index); i >= 0 {
				m.Rows[i].State, m.Rows[i].Elapsed = RowAborted, ev.Elapsed
				touch(i)
			}
		case engine.NeedsConfirm:
			m.open(&c, Prompt{ID: ev.ID, Confirm: &ev})
		case engine.NeedsPassword:
			m.open(&c, Prompt{ID: ev.ID, Password: &ev})
		case engine.NeedsSelection:
			m.open(&c, Prompt{ID: ev.ID, Selection: &ev})
		case engine.OutputReport:
			if ev.ID != 0 {
				m.open(&c, Prompt{ID: ev.ID, Report: &ev})
			} else {
				m.Notice = ev.Text
				c.Status = true
			}
		case engine.PromptClosed:
			if m.Prompt != nil && m.Prompt.ID == ev.ID {
				m.Prompt = nil
				if c.Opened != nil && c.Opened.ID == ev.ID {
					c.Opened = nil
				}
			}
			c.Closed = append(c.Closed, ev.ID)
		case engine.AuthWaiting:
			a := ev
			m.Auth = &a
			if ev.Kind == engine.WaitPolkit {
				if i := m.row(ev.Step, -1); i >= 0 {
					m.Rows[i].Waiting = true
					touch(i)
				}
			}
			c.Status = true
		case engine.AuthResolved:
			if a := m.Auth; a != nil && a.Kind == engine.WaitPolkit {
				if i := m.row(a.Step, -1); i >= 0 && m.Rows[i].Waiting {
					m.Rows[i].Waiting = false
					touch(i)
				}
			}
			m.Auth = nil
			c.Status = true
		case engine.FaillockState:
			f := ev
			m.Faillock, m.pendingLock = &f, &f
			if (f.Locked || LastAttempt(f)) && f != m.warned {
				m.warned = f
				if f.Locked {
					c.Toasts = append(c.Toasts, "Your "+lockText(f)+": archmaid won't ask for authorisation")
				} else {
					c.Toasts = append(c.Toasts, "One more failed authorisation locks your account")
				}
			}
			c.Status = true
		case engine.PhaseChanged:
			m.Phase, m.Busy = ev.Phase, ev.Busy
			c.Status = true
		case engine.StatusUpdated:
			m.Stat = ev
			if i := m.row(ev.Step, -1); i >= 0 && m.Rows[i].State == RowRunning {
				m.Rows[i].Elapsed = ev.StepElapsed
				touch(i)
			}
			c.Status = true
		case engine.LogWritten:
			m.Logs = append(m.Logs, ev)
		case engine.Notice:
			m.Notice = ev.Text
			c.Status = true
		case engine.ReportAccepted:
			// A toast: the report's dialog is gone and Finished follows.
			if ev.Text != "" {
				c.Toasts = append(c.Toasts, ev.Text)
			}
		case engine.Finished:
			f := ev
			m.Finished, m.Running, m.Prompt = &f, false, nil
			c.Finished, c.Status, c.Opened = true, true, nil
			// The engine sends the steps an Abort left pending no event
			// (it counts them in NotRun); they show as not run.
			for i := range m.Rows {
				if m.Rows[i].State == RowPending {
					m.Rows[i].State = RowNotRun
					touch(i)
				}
			}
		}
	}
	for i := range touched {
		c.Rows = append(c.Rows, i)
	}
	slices.Sort(c.Rows)
	c.OutTrim, c.OutRemove, c.OutAppend, c.Steps = m.sync()
	return c
}

// redrawable is how many of the newest lines a redraw of n lines of step
// may take back: its output lines at the end of the ring, back to its
// header line, a dropped-lines note, another step's line or the oldest the
// view may hold.
func (m *Model) redrawable(step string, n int) int {
	k, low := 0, m.lowest()
	for k < n && m.out.total-k-1 >= low {
		e := m.out.buf[(m.out.total-k-1)%len(m.out.buf)]
		if e.step != step || e.head || e.note {
			break
		}
		k++
	}
	return k
}

// lastText is the last non-blank text among the newest n lines pushed (at
// most the ring's size), "" when all are blank; it looks from the newest
// back, so a flood costs one check.
func (m *Model) lastText(n int) string {
	for i := 1; i <= min(n, len(m.out.buf)); i++ {
		if t := m.out.buf[(m.out.total-i)%len(m.out.buf)].text; strings.TrimSpace(t) != "" {
			return t
		}
	}
	return ""
}

// DiscoveryStepID is the ID of the one step a discovery result shows as.
const DiscoveryStepID = "discovery"

// discover makes the page this action's own: an action that ended at its
// discovery is a run of one "Discovery" step, the Summary its line, the
// Details its output. The state is ok for nothing found, failed for a look
// that failed.
func (m *Model) discover(d engine.DiscoveryResult) {
	m.Action, m.DryRun, m.Running, m.Finished, m.Stopped = d.Action, m.StartDry, true, nil, ""
	m.Auth, m.pendingLock, m.Started = nil, nil, d.Started
	m.Lines, m.Dropped = 0, 0
	m.base = m.out.total
	r := Row{ID: DiscoveryStepID, Title: "Discovery", LogLabel: "Discovery", Category: d.Category,
		State: RowOK, Elapsed: d.Elapsed, Last: d.Summary}
	if d.Outcome == engine.DiscoveryFailed {
		r.State = RowFailed
	}
	m.Rows = append(m.Rows[:0], r)
	m.Discovery = &d
	head := "── " + r.Title
	m.out.push(entry{raw: head, text: head, step: r.ID, head: true})
	for _, l := range d.Details {
		m.out.push(entry{raw: l, text: l, step: r.ID})
		m.Lines++
	}
}

// ShowReport makes the page the action's own when its result is a report
// (the AUR audit: a dialog, no queue): an empty page for action, not the
// last run's steps and line count. Returns the Change to redraw.
func (m *Model) ShowReport(action string) Change {
	m.Action, m.DryRun, m.Running, m.Finished, m.Stopped = action, m.StartDry, true, nil, ""
	m.Auth, m.pendingLock, m.Started = nil, nil, time.Time{}
	m.Lines, m.Dropped = 0, 0
	m.Discovery = nil
	c := m.Clear()
	c.Status = true
	return c
}

func newRow(s engine.PlanStep) Row {
	return Row{ID: s.ID, Title: s.Title, LogLabel: s.LogLabel, NeedsPriv: s.NeedsPriv, Optional: s.Optional, Verb: s.Verb, Category: s.Category}
}

// EngineStopped is what the GUI says when the Driver's events end without
// a Finished: the Driver closed on its own (a Session panic).
const EngineStopped = "archmaid's engine stopped unexpectedly"

// Stop folds in that the Driver's events ended without a Finished: the
// action is over (Change.Finished, so the page leaves the running state;
// Model.Finished is nil), the open prompt is closed, a running row
// reads aborted and a pending one not run, and EngineStopped is the
// status and a toast. Call it only for a Driver the page didn't close
// itself.
func (m *Model) Stop() Change {
	c := Change{Finished: true, Status: true, Toasts: []string{EngineStopped}}
	if m.Prompt != nil {
		c.Closed = append(c.Closed, m.Prompt.ID)
	}
	m.Stopped, m.Finished, m.Running, m.Prompt, m.Auth, m.Busy = EngineStopped, nil, false, nil, nil, ""
	for i := range m.Rows {
		switch m.Rows[i].State {
		case RowRunning:
			m.Rows[i].State, m.Rows[i].Waiting = RowAborted, false
		case RowPending:
			m.Rows[i].State = RowNotRun
		default:
			continue
		}
		c.Rows = append(c.Rows, i)
	}
	return c
}

// ConfirmLabels are a confirm's two buttons: yes is the engine's YesLabel
// ("Delete", "Update anyway"), "Yes" when it has none; no is "Skip" for an
// Optional step (no skips it and the queue goes on), else "Cancel".
func (m *Model) ConfirmLabels(p engine.NeedsConfirm) (yes, no string) {
	yes, no = p.YesLabel, "Cancel"
	if yes == "" {
		yes = "Yes"
	}
	if p.Step != "" {
		if i := m.row(p.Step, -1); i >= 0 && m.Rows[i].Optional {
			no = "Skip"
		}
	}
	return yes, no
}

// PlanLines is a gate confirm's plan preview, one line per step: its
// title, marked with its PrivTag when it has one. nil for a confirm
// without a plan.
func PlanLines(plan []engine.PlanStep) []string {
	var out []string
	for _, s := range plan {
		l := s.Title
		if t := PrivTag(s.NeedsPriv, s.Verb); t != "" {
			l += "  (" + t + ")"
		}
		out = append(out, l)
	}
	return out
}

func doneState(s steps.Status) RowState {
	switch s {
	case steps.OK:
		return RowOK
	case steps.Failed:
		return RowFailed
	case steps.Skipped:
		return RowSkipped
	case steps.AuthRefused:
		return RowAuthRefused
	case steps.Aborted:
		return RowAborted
	}
	return RowPending
}

// StepOut is one step's part of an output diff (Change.Steps): Remove
// lines leave the head of the step's group, Trim lines leave its tail (a
// redraw), then Append lines (a sub-slice of Change.OutAppend) join its
// tail. ID is the step's ID ("" for a line outside any step).
type StepOut struct {
	ID     string
	Remove int
	Trim   int
	Append []string
}

// run is consecutive view lines of one step: its header line (head) or
// its output.
type run struct {
	step string
	head bool
	n    int
}

// sync is the output diff since the last Apply: the view drops the lines
// a redraw took back from its tail and the lines that left the ring (or
// the run, base), and gains the new ones that are still in it, so a batch
// bigger than OutputCap never inserts a line only to remove it. grouped is
// the same diff by step.
func (m *Model) sync() (trim, remove int, add []string, grouped []StepOut) {
	hi := m.out.total
	lo := min(m.lowest(), hi)
	keep := m.viewHi // the view's lines below keep are unchanged
	if m.redrawn {
		keep = min(max(m.redrawFrom, m.viewLo), m.viewHi)
		m.redrawn = false
	}
	trim = m.viewHi - keep
	remove = min(max(lo-m.viewLo, 0), keep-m.viewLo)
	from := max(keep, lo)
	if from < hi {
		add = m.out.since(from)
	}
	m.viewLo, m.viewHi = lo, hi
	grouped = m.popTail(trim, grouped)
	grouped = m.popRuns(remove, grouped)
	return trim, remove, add, m.pushRuns(from, add, grouped)
}

// popTail takes n lines off the tail of the view's runs and adds their
// removal, by step, to out (a step's header line isn't in the grouped
// diff; a redraw never takes one back).
func (m *Model) popTail(n int, out []StepOut) []StepOut {
	for n > 0 && len(m.runs) > m.runHead {
		r := &m.runs[len(m.runs)-1]
		k := min(n, r.n)
		if !r.head {
			out = append(out, StepOut{ID: r.step, Trim: k})
		}
		r.n -= k
		n -= k
		if r.n == 0 {
			m.runs = m.runs[:len(m.runs)-1]
		}
	}
	return out
}

// popRuns takes n lines off the head of the view's runs and adds their
// removal, by step, to out (a step's header line isn't in the grouped
// diff).
func (m *Model) popRuns(n int, out []StepOut) []StepOut {
	for n > 0 && m.runHead < len(m.runs) {
		r := &m.runs[m.runHead]
		k := min(n, r.n)
		if !r.head {
			if l := len(out) - 1; l >= 0 && out[l].ID == r.step && out[l].Append == nil {
				out[l].Remove += k
			} else {
				out = append(out, StepOut{ID: r.step, Remove: k})
			}
		}
		r.n -= k
		n -= k
		if r.n == 0 {
			m.runHead++
		}
	}
	if m.runHead > 64 && m.runHead*2 > len(m.runs) {
		m.runs = append(m.runs[:0], m.runs[m.runHead:]...)
		m.runHead = 0
	}
	return out
}

// pushRuns records add (the lines from sequence number from on, all still
// in the ring) as runs, and adds their appends, by step, to out.
func (m *Model) pushRuns(from int, add []string, out []StepOut) []StepOut {
	for i := 0; i < len(add); {
		e := m.out.buf[(from+i)%len(m.out.buf)]
		j := i + 1
		for j < len(add) {
			f := m.out.buf[(from+j)%len(m.out.buf)]
			if f.step != e.step || f.head != e.head {
				break
			}
			j++
		}
		if l := len(m.runs) - 1; l >= m.runHead && m.runs[l].step == e.step && m.runs[l].head == e.head {
			m.runs[l].n += j - i
		} else {
			m.runs = append(m.runs, run{step: e.step, head: e.head, n: j - i})
		}
		if l := len(out) - 1; !e.head && l >= 0 && out[l].ID == e.step && out[l].Trim > 0 && out[l].Append == nil {
			out[l].Append = add[i:j:j] // a redraw: its trim and its lines in one
		} else if !e.head {
			out = append(out, StepOut{ID: e.step, Append: add[i:j:j]})
		}
		i = j
	}
	return out
}

// ring holds the last len(buf) lines of a sequence of total lines. A
// redraw takes lines back (total goes down); high is the most total has
// been, so the lines the ring overwrote stay gone.
type ring struct {
	buf   []entry
	total int
	high  int
}

// entry is an output line: raw as it came (SGR kept) and its plain text.
// The spans are parsed from raw only when a row shows the line (ViewLine),
// so a flood pays for stripping, not for styling 10k lines nobody sees.
// step is the ID of the step it belongs to; head marks the "── title"
// line a step starts with.
type entry struct {
	raw, text, step string
	head            bool
	note            bool // the "lines not shown" note of a Dropped
}

func (r *ring) push(e entry) {
	r.buf[r.total%len(r.buf)] = e
	r.total++
	r.high = max(r.high, r.total)
}

// since is the text of the lines from sequence number from (still in the
// ring) on.
func (r *ring) since(from int) []string {
	out := make([]string, 0, r.total-from)
	for i := from; i < r.total; i++ {
		out = append(out, r.buf[i%len(r.buf)].text)
	}
	return out
}
