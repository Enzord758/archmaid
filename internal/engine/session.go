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

// Session is one front end's run state: which action runs, its plan, the
// open prompt, auth, timings. See the package doc for the contract.
type Session struct {
	opts Options
}

// errNotImplemented is what every Session method panics with until E1.
var errNotImplemented = errors.New("engine: Session is not implemented yet (E1); use internal/engine/stub")

// New returns an idle Session.
func New(opts Options) *Session { return &Session{opts: opts} }

// Start runs the menu action with id (actions.ByID): a queue is planned
// (QueueStarted, after the probe, password or gate it needs), a discovery is
// started (PhaseChanged with its Busy text). An unknown id, or a Start
// while an action runs, is ignored.
func (s *Session) Start(action string) Output { panic(errNotImplemented) }

// Confirm answers the open NeedsConfirm (or OutputReport: yes accepts its
// audit findings, no dismisses it) with id.
func (s *Session) Confirm(id PromptID, yes bool) Output { panic(errNotImplemented) }

// SubmitPassword answers the open NeedsPassword with id. The Session hands
// pw on in one Effect (Authenticate for the up-front prompt, AnswerAskpass
// for a mid-run one) and keeps no copy.
func (s *Session) SubmitPassword(id PromptID, pw Secret) Output { panic(errNotImplemented) }

// CancelPrompt closes the open prompt with id unanswered: a password prompt
// gives up on privilege (the up-front one cancels the queue; an askpass one
// hangs up on sudo), a confirm counts as "no", a selection picks nothing, a
// report is dismissed.
func (s *Session) CancelPrompt(id PromptID) Output { panic(errNotImplemented) }

// Select answers the open NeedsSelection with id; picked is a subset of its
// Items (anything else is ignored). Picking nothing runs nothing.
func (s *Session) Select(id PromptID, picked []string) Output { panic(errNotImplemented) }

// Abort ends the running action at once: a running step is stopped (a Stop
// effect) and ends with Aborted, an open prompt is withdrawn (PromptClosed),
// and the action ends with Finished — OutcomeAborted, or OutcomeCancelled if
// no step had run. A no-op when idle. Asking "are you sure?" first is the
// front end's business.
func (s *Session) Abort() Output { panic(errNotImplemented) }

// Handle feeds the Session the result of an Effect, or a Driver
// observation (Tick, AskpassRequest). An Input for a Seq that is no longer
// current (a stopped step's late output) is ignored.
func (s *Session) Handle(in Input) Output { panic(errNotImplemented) }
