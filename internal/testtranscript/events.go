// Package testtranscript records a menu action as a frontend-agnostic text
// transcript, for golden comparison. It's test-only: nothing outside a _test.go
// file imports it.
//
// The Recorder owns the transcript format (docs/TESTING.md → Transcripts). It
// consumes Events from a Target, the thing that actually runs the action:
// today the TUI's model pumped through Update (tuiTarget, in cmd/archmaid's
// tests), from P4 on also engine.Session (sessionTarget). Both feed the same
// Recorder, so a golden recorded through one holds the other to the same
// behaviour. The package can't see a model or a session, so the format can't
// depend on either.
//
// The vocabulary follows the engine API planned for P4 (QueueStarted,
// PlanChanged, StepStarted, StepDone, OutputLine, NeedsConfirm, NeedsPassword,
// NeedsSelection, OutputReport): it's what the engine reports, projected down
// to what the v1 goldens record. Steps are queue positions, 0-based; the
// engine's stable step IDs map onto them through the QueueStarted order.
package testtranscript

import "slices"

// Event is one observation of a running action. The set is closed: a Target
// can only report what the Recorder knows how to write.
type Event interface{ isEvent() }

// Status is a step's state, as far as transcripts tell them apart.
type Status int

const (
	Pending Status = iota
	Running
	OK
	Failed
	Skipped
)

func (s Status) String() string {
	if s < Pending || s > Skipped {
		return "status?"
	}
	return [...]string{"pending", "running", "ok", "failed", "skipped"}[s]
}

// Step is one step of the plan: its title and the flags a transcript records.
type Step struct {
	Title string
	// InProc is a step run in-process; otherwise Argv is the program run.
	InProc bool
	Argv   []string
	// Sudo needs root; Confirm asks first; Optional is skipped, not aborted,
	// on "no"; AlwaysLog logs on success too.
	Sudo, Confirm, Optional, AlwaysLog bool
	// Category is the log file the step writes to (audit, cleanup, …).
	Category string
}

// SameFlags reports whether s and o differ in nothing a plan line records
// besides the title: a Target emits PlanChanged when this turns false.
func (s Step) SameFlags(o Step) bool {
	return s.InProc == o.InProc && slices.Equal(s.Argv, o.Argv) &&
		s.Sudo == o.Sudo && s.Confirm == o.Confirm && s.Optional == o.Optional &&
		s.AlwaysLog == o.AlwaysLog && s.Category == o.Category
}

// QueueStarted: a new queue was planned. Writes `plan N steps`, then one plan
// line per step.
type QueueStarted struct{ Steps []Step }

// StepInserted: a running queue grew a step at Index (the orphan probe's
// removal); the steps behind it move up one. Writes `plan N steps` (the new
// length) and `plan +<i> …`.
type StepInserted struct {
	Index int
	Step  Step
}

// PlanChanged: the flags of the step at Index changed mid-run (the audit
// gating topgrade). Writes its plan line again.
type PlanChanged struct {
	Index int
	Step  Step
}

// StepStarted: the step at Index is running. An abort answer waiting for it
// fires here.
type StepStarted struct{ Index int }

// StepDone: the step at Index finished: OK, Failed or Skipped.
type StepDone struct {
	Index  int
	Status Status
}

// OutputLine is one line of a step's output, as the frontend accepted it —
// one event per line, however the output was batched (the engine's
// OutputChunk is split into lines).
type OutputLine struct {
	Index int
	Text  string
}

// Exec: a program ran, as the fake executor (a shim) logged it: the command
// name and its argv, space-joined.
type Exec struct{ Cmdline string }

// LogWritten: a log entry was written to Category's file. Header is the entry
// header without its timestamp ("OK: Topgrade completed"); the body is the
// output already recorded.
type LogWritten struct{ Category, Header string }

// SnapshotChanged: the AUR maintainer snapshot's content changed.
type SnapshotChanged struct {
	Entries []SnapEntry
	// Unreadable is a snapshot that couldn't be parsed (or was removed).
	Unreadable bool
}

// SnapEntry is one package in the AUR maintainer snapshot.
type SnapEntry struct {
	Name, Maintainer string
	// HasCo is false for an entry without a co-maintainer record (old
	// format); CoMaintainers lists them when there is one.
	HasCo         bool
	CoMaintainers []string
}

// NeedsConfirm: a yes/no question is open. The Recorder answers it with Reply.
type NeedsConfirm struct{ Text string }

// NeedsPassword: the sudo password is asked for.
type NeedsPassword struct{}

// NeedsSelection: a multi-select (the services list) is open.
type NeedsSelection struct{ Options []string }

// Aborted: the abort answer was carried out on the running step at Index.
type Aborted struct{ Index int }

// Notice: the action ended in a notice (a popup in the TUI).
type Notice struct{ Text string }

// OutputReport: the action ended in a report screen (the AUR audit).
type OutputReport struct{ Text string }

func (QueueStarted) isEvent()    {}
func (StepInserted) isEvent()    {}
func (PlanChanged) isEvent()     {}
func (StepStarted) isEvent()     {}
func (StepDone) isEvent()        {}
func (OutputLine) isEvent()      {}
func (Exec) isEvent()            {}
func (LogWritten) isEvent()      {}
func (SnapshotChanged) isEvent() {}
func (NeedsConfirm) isEvent()    {}
func (NeedsPassword) isEvent()   {}
func (NeedsSelection) isEvent()  {}
func (Aborted) isEvent()         {}
func (Notice) isEvent()          {}
func (OutputReport) isEvent()    {}
