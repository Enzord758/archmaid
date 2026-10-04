package engine

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
)

// Options configure a Session. The zero value is a real run with the sudo
// flow and the wall clock.
type Options struct {
	// DryRun reports what would run and changes nothing; it is copied into
	// Env and into every step's steps.Env.
	DryRun bool
	// Now is the Session's only clock: step and run timings (Elapsed) and
	// the last-run record are read from it. nil means time.Now; tests pass a
	// fake.
	Now func() time.Time
	// Env is what the actions need from outside the engine (internal/actions).
	Env actions.Env
	// Auth is how the run gets root (see AuthFlow).
	Auth AuthFlow
}

// Validate reports what o leaves out that a Session needs: the required
// actions.Env fields (actions.Env.Validate) and a known AuthFlow. A front
// end checks it once, before New; a Session built from an invalid Options
// fails when it reaches the missing piece.
func (o Options) Validate() error {
	var errs []error
	if err := o.Env.Validate(); err != nil {
		errs = append(errs, err)
	}
	if o.Auth != AuthSudo && o.Auth != AuthPolkit {
		errs = append(errs, fmt.Errorf("engine: unknown AuthFlow %d", o.Auth))
	}
	return errors.Join(errs...)
}

// AuthFlow is how a run obtains privilege. Either way a refusal is one
// outcome, steps.AuthRefused; the flow only decides when the user is asked.
type AuthFlow int

const (
	// AuthSudo asks for the password once, before a queue with a NeedsPriv
	// step runs (probe first: a cached ticket asks nothing), checks it in two
	// stages (su, then sudo -S -v) and keeps the ticket alive; a sudo
	// re-prompt mid-run comes back as an askpass prompt.
	AuthSudo AuthFlow = iota
	// AuthPolkit asks nothing up front: each privileged step runs the root
	// helper through pkexec, and polkit's agent asks for the password.
	AuthPolkit
)

// PromptID names one open prompt (see the package doc, Prompts). Zero is
// never an ID.
type PromptID uint64

// Secret is a password in transit. It prints as "<redacted>" — with every
// fmt verb, nested in any struct, and through encoding/json or any other
// encoding.TextMarshaler user — so string(s) is the only way to read it.
// Only Commands, Session arguments and Effects hold one, never an Event.
type Secret string

const redacted = "<redacted>"

// String is "<redacted>".
func (Secret) String() string { return redacted }

// Format writes "<redacted>" whatever the verb and flags.
func (Secret) Format(f fmt.State, _ rune) { io.WriteString(f, redacted) }

// MarshalText is "<redacted>".
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Output is what one Session call produced: Events for the front end, in
// order, and Effects for the Driver, in the order to start them. Either may
// be empty; an ignored call returns both empty.
type Output struct {
	Events  []Event
	Effects []Effect
}

func (o *Output) event(evs ...Event)  { o.Events = append(o.Events, evs...) }
func (o *Output) effect(fx ...Effect) { o.Effects = append(o.Effects, fx...) }

// Session is one front end's run state: which action runs, its plan, the
// open prompt, auth, timings. See the package doc for the contract.
//
// Built so far (P4 E1–E4): an action's discovery (a Notice or a queue), the
// queue itself — the sudo probe, the proceed-gate, each step's confirm,
// running, output, results and their logging, steps a result inserts or
// gates — the last-run record, and auth (auth.go): the up-front password
// with its attempts and give-up, the faillock count, Repair's NeedsRoot
// recheck, and the polkit helper steps; askpass (E4, askpass.go). Not yet:
// Abort, Select and the discoveries that need them (NeedsSelection,
// Report; E5). Those calls panic with errNotImplemented
// rather than pretend to run something.
type Session struct {
	opts   Options
	env    actions.Env
	lookup func(string) (actions.Action, bool) // actions.ByID; tests swap it

	lastPrompt PromptID
	lastSeq    uint64
	phase      Phase
	run        *run // the action in progress; nil when idle
}

// errNotImplemented is what a Session method panics with for a part of the
// engine that isn't built yet (see Session).
var errNotImplemented = errors.New("engine: not implemented yet (P4 E3–E5); use internal/engine/stub")

// New returns an idle Session.
func New(opts Options) *Session {
	env := opts.Env
	env.DryRun = opts.DryRun
	return &Session{opts: opts, env: env, lookup: actions.ByID}
}

// Start runs the menu action with id (actions.ByID): a queue is planned
// (QueueStarted, after the probe, password or gate it needs), a discovery is
// started (PhaseChanged with its Busy text). An unknown id, or a Start
// while an action runs, is ignored.
func (s *Session) Start(action string) Output {
	if s.run != nil {
		return Output{}
	}
	a, ok := s.lookup(action)
	if !ok || (a.Steps == nil && a.Discover == nil) {
		return Output{} // the front end's own entries (view log, …)
	}
	s.run = &run{action: a.ID, started: s.now(), cur: -1}
	var o Output
	if a.Discover != nil {
		s.run.discoverSeq = s.seq()
		s.setPhase(&o, PhaseDiscovering, a.Busy)
		o.effect(Discover{Seq: s.run.discoverSeq, Action: a.ID, Run: a.Discover, Env: s.env})
		return o
	}
	s.plan(&o, a.Steps(s.env), a.Gate)
	return o
}

// Confirm answers the open NeedsConfirm (or OutputReport: yes accepts its
// audit findings, no dismisses it) with id.
func (s *Session) Confirm(id PromptID, yes bool) Output {
	r := s.run
	if r == nil || id == 0 || id != r.prompt || (r.promptFor != promptGate && r.promptFor != promptStep) {
		return Output{}
	}
	var o Output
	r.prompt = 0
	switch r.promptFor {
	case promptGate:
		if yes {
			s.startQueue(&o)
		} else {
			s.finish(&o, OutcomeCancelled)
		}
	case promptStep:
		if yes {
			s.launch(&o, true)
		} else {
			s.decline(&o)
		}
	}
	return o
}

// SubmitPassword answers the open NeedsPassword with id. The Session hands
// pw on in one Effect (Authenticate for the up-front prompt, AnswerAskpass
// for a mid-run one) and keeps no copy.
func (s *Session) SubmitPassword(id PromptID, pw Secret) Output {
	r := s.run
	if r == nil || id == 0 || id != r.prompt {
		return Output{}
	}
	var o Output
	switch r.promptFor {
	case promptPassword:
		s.submitUpFront(&o, pw)
	case promptAskpass:
		s.submitAskpass(&o, pw)
	}
	return o
}

// CancelPrompt closes the open prompt with id unanswered: a password prompt
// gives up on privilege (the up-front one cancels the queue; an askpass one
// hangs up on sudo), a confirm counts as "no", a selection picks nothing, a
// report is dismissed.
func (s *Session) CancelPrompt(id PromptID) Output {
	r := s.run
	if r == nil || id == 0 || id != r.prompt {
		return Output{}
	}
	var o Output
	switch r.promptFor {
	case promptGate, promptStep:
		return s.Confirm(id, false)
	case promptAskpass:
		s.cancelAskpass(&o)
		return o
	}
	r.prompt = 0
	s.finish(&o, OutcomeCancelled)
	return o
}

// Select answers the open NeedsSelection with id; picked is a subset of its
// Items (anything else is ignored). Picking nothing runs nothing.
func (s *Session) Select(id PromptID, picked []string) Output {
	// No NeedsSelection is opened until E5, so no id is one.
	return Output{}
}

// Abort ends the running action at once: a running step is stopped (a Stop
// effect) and ends with Aborted, a discovery in flight is stopped, an open
// prompt is withdrawn (PromptClosed), and the action ends with Finished —
// OutcomeAborted, or OutcomeCancelled if no step had started (abort.go). A
// no-op when idle. Asking "are you sure?" first is the front end's
// business.
func (s *Session) Abort() Output {
	if s.run == nil {
		return Output{}
	}
	var o Output
	s.abort(&o)
	return o
}

// Handle feeds the Session the result of an Effect, or a Driver
// observation (Tick, AskpassRequest). An Input for a Seq that is no longer
// current (a stopped step's late output) is ignored.
func (s *Session) Handle(in Input) Output {
	var o Output
	switch in := in.(type) {
	case Tick:
		s.tick(&o, in)
	case AskpassRequest:
		s.askpassRequest(&o, in)
	case AskpassAnswered:
		s.askpassAnswered(&o, in)
	case ProbeDone:
		s.probed(&o, in)
	case FaillockRead:
		s.faillockRead(&o, in)
	case AuthDone:
		s.authDone(&o, in)
	case PrivilegeGranted:
		s.privilegeGranted(&o, in)
	case Discovered:
		s.discovered(&o, in)
	case StepOutput:
		s.stepOutput(&o, in)
	case StepExited:
		s.stepExited(&o, in)
	case StepResult:
		s.stepResult(&o, in)
	}
	// HookDone needs nothing: a hook that fails is logged by the hook, and
	// never blocks its step.
	return o
}

func (s *Session) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// seq is a fresh Effect sequence number (never zero).
func (s *Session) seq() uint64 {
	s.lastSeq++
	return s.lastSeq
}

func (s *Session) promptID() PromptID {
	s.lastPrompt++
	return s.lastPrompt
}

func (s *Session) setPhase(o *Output, p Phase, busy string) {
	if p == s.phase && busy == "" {
		return
	}
	s.phase = p
	o.event(PhaseChanged{Phase: p, Busy: busy})
}

func (s *Session) tick(o *Output, in Tick) {
	now := s.now()
	ev := StatusUpdated{Now: now, Status: in.Status}
	if r := s.run; r != nil {
		ev.Elapsed = now.Sub(r.started)
		if st := r.running(); st != nil {
			ev.StepElapsed = now.Sub(r.stepAt)
			ev.Step = st.step.ID
		}
	}
	o.event(ev)
}
