// Package runview is the Run page's state, in plain Go: the adapter from
// engine Events to what the page shows (Model), and the pump that carries a
// Driver's events to the GTK main thread in batches (Pump). Nothing here
// imports gotk4, so all of it is tested with plain `go test`, no display.
package runview

import (
	"fmt"
	"slices"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
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
	// NeedsPriv: the step needs root; Optional: "no" at its confirm skips
	// it rather than ending the queue.
	NeedsPriv, Optional bool
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
		if r.Faillock != nil && r.Faillock.Locked {
			return "Not authorised · " + lockText(*r.Faillock)
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
	// OutRemove lines leave the head of the output view, then OutAppend
	// lines join its tail; after that the view holds Model.Output().
	OutRemove int
	OutAppend []string
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
	Prompt  *Prompt // the open prompt, or nil
	Stat    engine.StatusUpdated
	Logs    []engine.LogWritten
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

	// Lines counts every output line received; Dropped those the Driver or
	// the pump dropped before they reached the view.
	Lines, Dropped int

	out ring
	// view is the window [viewLo, viewHi) of the output sequence the
	// widgets hold, as of the last Apply.
	viewLo, viewHi int
}

// NewModel is an empty Model.
func NewModel() *Model { return &Model{out: ring{buf: make([]entry, OutputCap)}} }

// Output is the lines the view holds, oldest first, as plain text.
func (m *Model) Output() []string { return m.out.lines() }

// ViewLine is line i (0 = the oldest) of the view as of the last Apply,
// with its colour spans; false when i is out of range.
func (m *Model) ViewLine(i int) (Line, bool) {
	seq := m.viewLo + i
	if i < 0 || seq >= m.viewHi || seq < m.out.total-len(m.out.buf) {
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
			m.Rows, m.Auth, m.pendingLock = m.Rows[:0], nil, nil
			for _, s := range ev.Steps {
				m.Rows = append(m.Rows, Row{ID: s.ID, Title: s.Title, NeedsPriv: s.NeedsPriv, Optional: s.Optional})
			}
			c.Reset, c.Inserted = true, nil
			clear(touched)
			c.Status = true
		case engine.StepInserted:
			i := min(max(ev.Index, 0), len(m.Rows))
			m.Rows = append(m.Rows[:i], append([]Row{{ID: ev.Step.ID, Title: ev.Step.Title, NeedsPriv: ev.Step.NeedsPriv, Optional: ev.Step.Optional}}, m.Rows[i:]...)...)
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
				touch(i)
			}
		case engine.StepStarted:
			if i := m.row(ev.ID, ev.Index); i >= 0 {
				m.Rows[i].State, m.Rows[i].Elapsed = RowRunning, 0
				m.Rows[i].Faillock, m.pendingLock = m.pendingLock, nil
				touch(i)
				m.push("── " + m.Rows[i].Title)
			}
		case engine.OutputChunk:
			if ev.Dropped > 0 {
				m.Dropped += ev.Dropped
				m.push(fmt.Sprintf("… %d lines not shown (they are in the log)", ev.Dropped))
			}
			m.Lines += len(ev.Lines) + ev.Dropped
			for _, l := range ev.Lines {
				m.out.push(entry{raw: l, text: StripSGR(l)})
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
	c.OutRemove, c.OutAppend = m.sync()
	return c
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
// title, marked when it needs root. nil for a confirm without a plan.
func PlanLines(plan []engine.PlanStep) []string {
	var out []string
	for _, s := range plan {
		l := s.Title
		if s.NeedsPriv {
			l += "  (root)"
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

// push adds a line archmaid wrote itself (a step header, a drop notice):
// plain text, no SGR parsing.
func (m *Model) push(line string) { m.out.push(entry{raw: line, text: line}) }

// sync is the output diff since the last Apply: the view drops the lines
// that left the ring and gains the new ones that are still in it, so a
// batch bigger than OutputCap never inserts a line only to remove it.
func (m *Model) sync() (remove int, add []string) {
	hi := m.out.total
	lo := max(hi-OutputCap, 0)
	remove = min(max(lo-m.viewLo, 0), m.viewHi-m.viewLo)
	from := max(m.viewHi, lo)
	if from < hi {
		add = m.out.since(from)
	}
	m.viewLo, m.viewHi = lo, hi
	return remove, add
}

// ring holds the last len(buf) lines of a sequence of total lines.
type ring struct {
	buf   []entry
	total int
}

// entry is an output line: raw as it came (SGR kept) and its plain text.
// The spans are parsed from raw only when a row shows the line (ViewLine),
// so a flood pays for stripping, not for styling 10k lines nobody sees.
type entry struct{ raw, text string }

func (r *ring) push(e entry) {
	r.buf[r.total%len(r.buf)] = e
	r.total++
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

func (r *ring) lines() []string { return r.since(max(r.total-len(r.buf), 0)) }
