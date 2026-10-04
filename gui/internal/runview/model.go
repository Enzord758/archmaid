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
)

var rowStateNames = [...]string{"pending", "running", "ok", "failed", "skipped", "auth refused", "aborted"}

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
	}
	return "radio-symbolic"
}

// Row is one checklist row: a step of the plan.
type Row struct {
	ID, Title string
	NeedsPriv bool
	State     RowState
	Elapsed   time.Duration
	Err       string
}

// Subtitle is the row's second line: its state and time.
func (r Row) Subtitle() string {
	switch r.State {
	case RowPending:
		return ""
	case RowRunning:
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
		return "Not authorized"
	case RowAborted:
		return "Stopped · " + fmtElapsed(r.Elapsed)
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
	// Opened is the confirm opened in this batch and still open at its end;
	// Closed are prompts closed by the engine (PromptClosed).
	Opened *engine.NeedsConfirm
	Closed []engine.PromptID
	// Unsupported are prompts the skeleton can't show yet (password,
	// selection, report): the page cancels them.
	Unsupported []engine.PromptID
	// Status: Phase, Status, Notice or the counters changed. Finished: the
	// action ended in this batch.
	Status, Finished bool
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
	Notice   string
	Prompt   *engine.NeedsConfirm // the open confirm, or nil
	Stat     engine.StatusUpdated
	Logs     []engine.LogWritten
	// Auth is the open AuthWaiting, or nil.
	//
	// TODO(E6): polkit dialog → helper progress. AuthWaiting{WaitPolkit}
	// should mark its step's row "Waiting for authorization" and
	// FaillockState should warn before the attempt that locks the account;
	// both need the real Driver to be exercised.
	Auth *engine.AuthWaiting

	// Lines counts every output line received; Dropped those the Driver or
	// the pump dropped before they reached the view.
	Lines, Dropped int

	out ring
	// view is the window [viewLo, viewHi) of the output sequence the
	// widgets hold, as of the last Apply.
	viewLo, viewHi int
}

// NewModel is an empty Model.
func NewModel() *Model { return &Model{out: ring{buf: make([]string, OutputCap)}} }

// Output is the lines the view holds, oldest first.
func (m *Model) Output() []string { return m.out.lines() }

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

// Answered closes the open prompt id after the user answered it.
func (m *Model) Answered(id engine.PromptID) {
	if m.Prompt != nil && m.Prompt.ID == id {
		m.Prompt = nil
	}
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
			m.Action, m.DryRun, m.Running, m.Finished, m.Notice = ev.Action, ev.DryRun, true, nil, ""
			m.Rows = m.Rows[:0]
			for _, s := range ev.Steps {
				m.Rows = append(m.Rows, Row{ID: s.ID, Title: s.Title, NeedsPriv: s.NeedsPriv})
			}
			c.Reset, c.Inserted = true, nil
			clear(touched)
			c.Status = true
		case engine.StepInserted:
			i := min(max(ev.Index, 0), len(m.Rows))
			m.Rows = append(m.Rows[:i], append([]Row{{ID: ev.Step.ID, Title: ev.Step.Title, NeedsPriv: ev.Step.NeedsPriv}}, m.Rows[i:]...)...)
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
				m.Rows[i].Title, m.Rows[i].NeedsPriv = ev.Step.Title, ev.Step.NeedsPriv
				touch(i)
			}
		case engine.StepStarted:
			if i := m.row(ev.ID, ev.Index); i >= 0 {
				m.Rows[i].State, m.Rows[i].Elapsed = RowRunning, 0
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
				// TODO(P6b-2): ANSI → spans with the dank16 palette; the
				// skeleton shows plain text.
				m.push(StripSGR(l))
			}
			c.Status = true
		case engine.StepDone:
			if i := m.row(ev.ID, ev.Index); i >= 0 {
				m.Rows[i].State, m.Rows[i].Elapsed, m.Rows[i].Err = doneState(ev.Status), ev.Elapsed, ev.Err
				touch(i)
			}
		case engine.Aborted:
			if i := m.row(ev.ID, ev.Index); i >= 0 {
				m.Rows[i].State, m.Rows[i].Elapsed = RowAborted, ev.Elapsed
				touch(i)
			}
		case engine.NeedsConfirm:
			p := ev
			m.Prompt, c.Opened = &p, &p
		case engine.NeedsPassword:
			// TODO(E6): the askpass dialog for the AUR step (PasswordAskpass:
			// Requester, Prompt, AttemptsLeft, Error) and SubmitPassword. The
			// stub can't check a password, so it waits for the real Driver.
			c.Unsupported = append(c.Unsupported, ev.ID)
		case engine.NeedsSelection:
			c.Unsupported = append(c.Unsupported, ev.ID)
		case engine.OutputReport:
			if ev.ID != 0 {
				c.Unsupported = append(c.Unsupported, ev.ID)
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
			c.Status = true
		case engine.AuthResolved:
			m.Auth = nil
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
		case engine.Finished:
			f := ev
			m.Finished, m.Running, m.Prompt = &f, false, nil
			c.Finished, c.Status, c.Opened = true, true, nil
		}
	}
	for i := range touched {
		c.Rows = append(c.Rows, i)
	}
	slices.Sort(c.Rows)
	c.OutRemove, c.OutAppend = m.sync()
	return c
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

func (m *Model) push(line string) { m.out.push(line) }

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
	buf   []string
	total int
}

func (r *ring) push(s string) {
	r.buf[r.total%len(r.buf)] = s
	r.total++
}

// since is the lines from sequence number from (still in the ring) on.
func (r *ring) since(from int) []string {
	out := make([]string, 0, r.total-from)
	for i := from; i < r.total; i++ {
		out = append(out, r.buf[i%len(r.buf)])
	}
	return out
}

func (r *ring) lines() []string { return r.since(max(r.total-len(r.buf), 0)) }

// StripSGR removes ANSI escape sequences (the engine leaves only SGR
// colour in output lines) and returns plain text.
func StripSGR(s string) string {
	i := 0
	for i < len(s) && s[i] != 0x1b {
		i++
	}
	if i == len(s) {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b = append(b, s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
		}
	}
	return string(b)
}
