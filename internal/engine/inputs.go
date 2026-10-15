package engine

import (
	"errors"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// Input is what a Driver feeds Session.Handle: an Effect's result, or
// something it observed. The set is closed. Inputs carrying a Seq that is
// no longer current are ignored.
type Input interface{ isInput() }

// Tick is the Driver's one ticker (every few seconds): Status is freshly
// read (sysstat.Read). → StatusUpdated.
type Tick struct{ Status sysstat.Status }

// ProbeDone answers Probe: Cached is a usable ticket without a password.
type ProbeDone struct{ Cached bool }

// AuthDone answers Authenticate. Detail is a message worth showing
// verbatim (a lockout notice), or "". With OK it is a note for the run
// (privilege.NoTicketNote: sudo keeps no ticket, so archmaid asks each
// time), carried on AuthResolved{Granted: true}.
//
// NoTicket: the password was right but sudo kept no ticket another sudo can
// reuse and nothing sends the steps' own sudo through archmaid
// (privilege.ErrNoTicket); OK is false, Detail says why, and the run
// ends before any step starts, with no attempt counted: AuthResolved, a
// Notice with Detail, then Finished{OutcomeCancelled}.
type AuthDone struct {
	OK       bool
	Detail   string
	Err      error
	NoTicket bool
}

// FaillockRead answers ReadFaillock (see FaillockState); Err if the count
// couldn't be read.
type FaillockRead struct {
	Failures, Deny int
	Unlock         time.Time
	Err            error
}

// Discovered answers Discover with Seq.
type Discovered struct {
	Seq       uint64
	Discovery actions.Discovery
}

// StepOutput is output of the child StartStep started with Seq: whole
// lines, already sanitized (sanitize.KeepSGR; the Session sanitizes again,
// per sanitize.ColourMode), in order. A Driver may batch
// lines into one StepOutput; it never drops any here.
type StepOutput struct {
	Seq   uint64
	Lines []string
	// Replace is how many of the child's last lines, sent in earlier
	// StepOutputs, Lines replace (the child redrew them: proc.Msg.Replace).
	// The Session replaces only what the child printed since the last line
	// it added itself, and drops as many leading Lines as it could not
	// replace (OutputChunk). 0 appends.
	Replace int
}

// PrivilegeGranted: the privilege backend got root for step Seq (the root
// helper printed its JSON start line). Ends an AuthWaiting{WaitPolkit}.
type PrivilegeGranted struct{ Seq uint64 }

// StepExited: the child of step Seq is done and all its output was
// delivered. Err is its failure, nil on success. NotGranted is the backend's
// verdict that it never got privilege (pkexec's 127 before the helper's
// start line, whatever the reason) → steps.AuthRefused.
type StepExited struct {
	Seq        uint64
	Err        error
	NotGranted bool
}

// ErrNoAuthAgent is a NotGranted StepExited's Err (wrapped) when no polkit
// agent was there to ask for the password: pkexec runs with
// --disable-internal-agent and said "No authentication agent found". The
// step still ends steps.AuthRefused, but its StepDone carries this text as
// Err, so a front end doesn't present it as a refused password.
var ErrNoAuthAgent = errors.New("no polkit agent is running, so the password can't be asked")

// StepResult is the Result of in-process step Seq.
type StepResult struct {
	Seq    uint64
	Result steps.Result
}

// HelperCheck is the root helper's check event for step Seq (its
// check-files verb, Repair's recheck under polkit): Broken lists the
// packages it found broken, empty for none. A Driver sends it, parsed from
// the helper's output (internal/helperclient), before that step's
// StepExited; the Session decides from it what follows the recheck.
type HelperCheck struct {
	Seq    uint64
	Broken []string
}

// PartStarted: the combined root-helper call Seq (a StartStep whose Step
// has Parts) began its part Step (that part's step ID). A Driver sends it,
// parsed from the helper's output (internal/helperclient), for every part
// after the first, which runs from the call's start. The Session takes
// only the next part in order, once the one before is done.
type PartStarted struct {
	Seq  uint64
	Step string
}

// PartDone: part Step of the combined call Seq finished with Err (nil on
// success). The call's StepExited settles the part still running and every
// part that never started.
type PartDone struct {
	Seq  uint64
	Step string
	Err  error
}

// HookDone answers RunHook with Seq.
type HookDone struct {
	Seq uint64
	Err error
}

// AskpassRequest is sudo asking for the password mid-run, through the
// askpass socket. The Driver has already run the peer check
// (Request.Verify against the running step's child): Requester names the
// verified caller, or Refused says why the peer failed the check — the
// Session then hangs up and notes it in the step's output. Prompt is sudo's
// prompt text, raw (the Session sanitizes it).
//
// Polkit marks a request from polkit's text agent instead (the GUI's
// helper steps, see the Driver's Deps.Agent): the agent has checked it is
// for one of archmaid's helper actions and for the current user. It is
// answered only while a polkit helper step waits for authorisation, and
// the Driver su-checks the password before it reaches the agent.
type AskpassRequest struct {
	Req                        uint64
	Prompt, Requester, Refused string
	Polkit                     bool
	// Action is the polkit action id, for a Polkit request.
	Action string
}

// AskpassAnswered answers AnswerAskpass: OK when su accepted the password
// and it was written to sudo; Detail as for AuthDone.
type AskpassAnswered struct {
	Req    uint64
	OK     bool
	Detail string
}

func (Tick) isInput()             {}
func (ProbeDone) isInput()        {}
func (AuthDone) isInput()         {}
func (FaillockRead) isInput()     {}
func (Discovered) isInput()       {}
func (StepOutput) isInput()       {}
func (PrivilegeGranted) isInput() {}
func (StepExited) isInput()       {}
func (StepResult) isInput()       {}
func (HookDone) isInput()         {}
func (HelperCheck) isInput()      {}
func (AskpassRequest) isInput()   {}
func (AskpassAnswered) isInput()  {}
func (PartStarted) isInput()      {}
func (PartDone) isInput()         {}
