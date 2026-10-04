package engine

import (
	"context"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Effect is IO the Session needs done. The set is closed. A Driver starts
// an Output's Effects in order, before it publishes that Output's Events
// (so a front end that sees StepStarted knows the child was started).
// WriteLog, WriteLastRun and RunHook are carried out to completion before
// the next Effect starts; the others may run on in the background, and
// report back through the Input named on each.
//
// Effects are for the Driver only: they may hold a Secret or a func, so
// they are never shown or logged.
type Effect interface{ isEffect() }

// Probe asks whether root is available without a password (a cached sudo
// ticket). → ProbeDone.
type Probe struct{}

// Authenticate checks Password and, only if it is right, obtains the
// ticket (privilege.Privileged.Authenticate). → AuthDone.
type Authenticate struct{ Password Secret }

// KeepAlive turns the Driver's ticket refresh on (every 60 s,
// privilege.Privileged.Refresh) or off. No Input.
type KeepAlive struct{ On bool }

// ReadFaillock asks for pam_faillock's count for the user. → FaillockRead.
type ReadFaillock struct{}

// Discover runs an action's discovery (actions.Action.Discover, or a
// NeedsRoot's Rescan) with a cancellable ctx. → Discovered with Seq.
type Discover struct {
	Seq    uint64
	Action string
	Run    func(ctx context.Context, env actions.Env) actions.Discovery
	Env    actions.Env
}

// StartStep starts Step: its Argv as a child, through the Driver's
// privilege backend when NeedsPriv or Verb is set, or its Run in-process
// (an in-process step whose Prefetch was started awaits that).
// → StepOutput (children), then exactly one StepExited (child) or
// StepResult (in-process), all with Seq; PrivilegeGranted with Seq when the
// backend reports privilege obtained.
type StartStep struct {
	Seq  uint64
	Step steps.Step
	Env  steps.Env
}

// Stop ends whatever the Effect with Seq started: a child gets SIGINT, then
// SIGKILL after the grace; an in-process step's or discovery's ctx is
// cancelled. Its late Inputs are ignored. No Input.
type Stop struct{ Seq uint64 }

// RunHook calls Fn (a step's OnAccept, a result's Commit: the audit's
// snapshot save) before the next Effect. → HookDone with Seq.
type RunHook struct {
	Seq  uint64
	Step string // the step it belongs to
	Fn   func() error
}

// WriteLog appends an entry to Category's log (logstore.Store.Append with
// Kind and Label; Body is the step's whole output). No Input: a write that
// fails is the Driver's to report.
type WriteLog struct {
	Category    logstore.Category
	Kind, Label string // "OK", "Topgrade completed"
	Body        string
}

// WriteLastRun records the finished action for the DMS widget
// (logstore.Store.WriteLastRun). No Input.
type WriteLastRun struct{ Record logstore.RunRecord }

// StartPrefetch starts the named prefetch (steps.Prefetch*: the AUR
// lookup) so it runs while a prompt is up; CancelPrefetch drops it. No
// Input: the step that needs it awaits it.
type StartPrefetch struct{ Name string }

// CancelPrefetch drops the named prefetch, if one runs.
type CancelPrefetch struct{ Name string }

// AnswerAskpass answers askpass request Req with Password
// (privilege.Request.Answer: su-checked before anything reaches sudo).
// → AskpassAnswered.
type AnswerAskpass struct {
	Req      uint64
	Password Secret
}

// HangUpAskpass closes askpass request Req unanswered (Request.Cancel). No
// Input.
type HangUpAskpass struct{ Req uint64 }

func (Probe) isEffect()          {}
func (Authenticate) isEffect()   {}
func (KeepAlive) isEffect()      {}
func (ReadFaillock) isEffect()   {}
func (Discover) isEffect()       {}
func (StartStep) isEffect()      {}
func (Stop) isEffect()           {}
func (RunHook) isEffect()        {}
func (WriteLog) isEffect()       {}
func (WriteLastRun) isEffect()   {}
func (StartPrefetch) isEffect()  {}
func (CancelPrefetch) isEffect() {}
func (AnswerAskpass) isEffect()  {}
func (HangUpAskpass) isEffect()  {}
