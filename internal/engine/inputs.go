package engine

import (
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
// verbatim (a lockout notice), or "".
type AuthDone struct {
	OK     bool
	Detail string
	Err    error
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
// lines, already sanitized (sanitize.KeepSGR), in order. A Driver may batch
// lines into one StepOutput; it never drops any here.
type StepOutput struct {
	Seq   uint64
	Lines []string
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

// StepResult is the Result of in-process step Seq.
type StepResult struct {
	Seq    uint64
	Result steps.Result
}

// HookDone answers RunHook with Seq.
type HookDone struct {
	Seq uint64
	Err error
}

// AskpassRequest is sudo asking for the password mid-run, through the
// askpass socket. The Driver has already run the peer check
// (privilege.Verify against the running step's child): Requester names the
// verified caller, or Refused says why the peer failed the check — the
// Session then hangs up and notes it in the step's output. Prompt is sudo's
// prompt text, raw (the Session sanitizes it).
type AskpassRequest struct {
	Req                        uint64
	Prompt, Requester, Refused string
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
func (AskpassRequest) isInput()   {}
func (AskpassAnswered) isInput()  {}
