package engine

import (
	"slices"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
	runreport "github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// Event is something that happened, for a front end to show. The set is
// closed (only this package's types implement it) and every Event is plain
// data: no password, func, channel or interface in any field (see the
// package doc, Passwords).
type Event interface{ isEvent() }

// PlanStep is one step of the plan as a front end sees it: steps.Step
// without the funcs.
type PlanStep struct {
	// ID is the step's stable ID (steps.ID*), unique within the plan: key
	// rows on it, not on Title or position.
	ID string
	// Verb is the root helper's verb for the step, or "".
	Verb  string
	Title string
	// LogLabel is what the step's log entry calls it ("Old package cache
	// cleaned"); the TUI's checklist shows it on a step that succeeded.
	LogLabel string
	// InProc is a step run in-process; otherwise Argv is its command line
	// as planned (before the privilege backend wraps it).
	InProc bool
	Argv   []string
	// NeedsPriv needs root; Confirm is asked before it runs; Optional is
	// skipped rather than ending the queue on "no"; AlwaysLog logs success
	// too.
	NeedsPriv, Confirm, Optional, AlwaysLog bool
	Category                                logstore.Category
}

// QueueStarted: a queue was planned and is about to run. Steps is the whole
// plan, in order; indexes in later events are positions in it (0-based),
// as moved by StepInserted.
type QueueStarted struct {
	Action  string // the menu action's ID (actions.ID*)
	Steps   []PlanStep
	DryRun  bool
	Started time.Time // Options.Now when it was planned
}

// StepInserted: the plan grew a step at Index (the orphan probe's removal);
// the steps from Index on move up one.
type StepInserted struct {
	Index int
	Step  PlanStep
}

// PlanChanged: the step at Index changed mid-run (the audit gating
// topgrade: now Confirm and Optional). Step is its new description; its ID
// is unchanged.
type PlanChanged struct {
	Index int
	Step  PlanStep
}

// StepStarted: the step is running (its child started, or its in-process
// run began).
type StepStarted struct {
	ID    string
	Index int
	At    time.Time
}

// StepDone: the step finished, or was settled without running. Status is
// steps.OK, Failed, Skipped or AuthRefused (never Aborted: an abort ends a
// step with the Aborted event instead). Err is the failure, sanitized and
// one line, or "".
type StepDone struct {
	ID      string
	Index   int
	Status  steps.Status
	Elapsed time.Duration // since its StepStarted; 0 if it never ran
	Err     string
}

// OutputChunk is output of the running step, whole lines in order, SGR
// colour kept and every other escape stripped. The engine emits each line
// once and never caps; a Driver may merge chunks and drop lines (Dropped,
// see the package doc, Dropping and coalescing).
//
// A chunk may redraw lines it sent before (Replace): the child rewrote
// them, a progress bar or a spinner. A front end then removes the step's
// last Replace lines, shows Dropped, and appends Lines. It never removes
// more of the step's lines than it got since its last gap (a Dropped, the
// step's start, lines its own cap let go): what it can't remove is gone
// already, so Lines still all go in. The engine only replaces lines of the
// step's child (never a line it added itself, such as an askpass refusal,
// whose older lines it keeps as they were), so Replace stays within what
// the child printed since the last such line.
type OutputChunk struct {
	ID    string
	Index int
	Lines []string
	// Dropped is how many of the step's lines a Driver dropped right
	// before these, because its consumer fell behind. Always 0 from a
	// Session. The log still has them.
	Dropped int
	// Replace is how many of the step's last lines, as sent in earlier
	// chunks, Lines replace (len(Lines) >= Replace from a Session). 0
	// appends, as every chunk did before #129.
	Replace int
}

// Rewrite returns a followed by b, two chunks of the same step, as one
// chunk, when b redraws lines (b.Replace > 0) and that can be said in one
// chunk: b's replaced lines are all among a's, or a dropped nothing in
// front of its lines (a.Dropped == 0), and b dropped nothing (b.Dropped ==
// 0). ok is false otherwise; a and b are then delivered as they are. The
// result never shares a's or b's backing arrays.
func Rewrite(a, b OutputChunk) (merged OutputChunk, ok bool) {
	if b.Dropped > 0 || (b.Replace > len(a.Lines) && a.Dropped > 0) {
		return a, false
	}
	if b.Replace <= len(a.Lines) {
		a.Lines = append(slices.Clip(a.Lines[:len(a.Lines)-b.Replace]), b.Lines...)
		return a, true
	}
	a.Replace += b.Replace - len(a.Lines)
	a.Lines = slices.Clone(b.Lines)
	return a, true
}

// Aborted: the user's Abort stopped the running step at Index. It ends that
// step instead of a StepDone (steps.Aborted).
type Aborted struct {
	ID      string
	Index   int
	Elapsed time.Duration
}

// LogWritten: an entry went to Category's log file. Header is the entry's
// header without its timestamp ("OK: Topgrade completed"). The engine emits
// it with the WriteLog effect that writes it.
type LogWritten struct {
	Category logstore.Category
	Header   string
}

// ConfirmKind is what a NeedsConfirm asks about.
type ConfirmKind int

const (
	ConfirmGate      ConfirmKind = iota // "Proceed with full cleanup?" before a queue
	ConfirmStep                         // a step's own Confirm
	ConfirmAuditGate                    // the AUR audit holding back the update (Step is the gated step)
	ConfirmOrphans                      // removing orphans: Direct and Cascade are set
	ConfirmDownloads                    // deleting old downloads: Items are the entries
)

// NeedsConfirm opens a yes/no prompt; answer with Confirm(ID, yes) or
// CancelPrompt(ID) (= no).
type NeedsConfirm struct {
	ID   PromptID
	Kind ConfirmKind
	// Step is the ID of the step this confirm gates, or "" for a gate in
	// front of the whole queue.
	Step string
	// Title and Body are the question split for a dialog: a short heading
	// and the detail under it ("" when there is none).
	Title, Body string
	// Items lists what the step acts on, when it acts on a list (downloads,
	// units); Direct and Cascade are the orphan removal's two lists: the
	// orphans found, and everything `pacman -Rns` removes with them.
	Items, Direct, Cascade []string
	// Default is the answer Enter gives; Destructive marks a step that
	// removes something that can't be brought back by archmaid.
	Default, Destructive bool
	// Msg is the whole question as one string, exactly as the TUI shows it
	// and transcripts record it.
	Msg string
	// Commits: "yes" also runs the result's Commit (the audit's snapshot
	// save, as the step's OnAccept). False on a gate that stands for a
	// refused result (steps.Result.Check): that step runs on "yes", but
	// nothing is committed.
	Commits bool
	// Plan previews the queue a ConfirmGate stands in front of: the steps
	// QueueStarted will carry on "yes", equal to its Steps (same order,
	// same sanitized arguments). nil for every other kind.
	Plan []PlanStep
	// YesLabel is the yes button's label, saying what "yes" does
	// ("Proceed", "Delete", "Remove", "Update anyway"); "" when there is no
	// better word than "Yes", which the front end then shows. The no button
	// is the front end's own ("Skip" for an Optional step, else "Cancel").
	YesLabel string
}

// PasswordKind is which password prompt a NeedsPassword is.
type PasswordKind int

const (
	// PasswordUpFront is the sudo password before a queue runs (AuthSudo).
	PasswordUpFront PasswordKind = iota
	// PasswordAskpass is sudo asking again mid-step, through SUDO_ASKPASS.
	PasswordAskpass
)

// NeedsPassword opens a password prompt; answer with SubmitPassword(ID, pw)
// or CancelPrompt(ID). A wrong password re-opens it with a new ID, Error
// set and AttemptsLeft one lower.
type NeedsPassword struct {
	ID   PromptID
	Kind PasswordKind
	// Step is the running step an askpass prompt belongs to; "" up front.
	Step string
	// Requester names who asked (askpass only): sudo's caller, as binary and
	// pid. Prompt is sudo's own prompt text, sanitized ("" up front).
	Requester, Prompt string
	AttemptsLeft      int
	// Error is why the last attempt failed ("wrong password", a faillock
	// notice verbatim), or "" on the first ask.
	Error string
	// Gate is the proceed-gate this up-front prompt stands in for
	// ("Proceed with full cleanup?"), so a dialog can ask it with the
	// password; "" when the action has none, and for askpass.
	Gate string
	// Polkit marks an askpass prompt from polkit's text agent for a helper
	// step (AskpassRequest.Polkit), not sudo's; Action is the polkit action
	// id it authenticates, as the agent read it (an org.archmaid.helper.*
	// one). A front end picks the dialog from these, never from Requester.
	Polkit bool
	Action string
	// Keeps is set by a Driver that keeps the run's password (the GUI's)
	// when the password given to this prompt will be kept and answer the
	// run's later prompts. False is the safe default: a front end claims
	// nothing is kept unless this says so. KeepOff is then, for a Driver
	// that keeps passwords, why not (one short line, sanitized; "" when it
	// has no better reason than that it was refused or has run out).
	Keeps   bool
	KeepOff string
}

// NeedsSelection opens a multi-select (the services list); answer with
// Select(ID, picked) or CancelPrompt(ID).
type NeedsSelection struct {
	ID    PromptID
	Title string
	Items []string
}

// PromptClosed: the engine withdrew the open prompt with ID without an
// answer (the askpass client hung up, its step ended, an Abort). The front
// end closes its dialog; answers to ID are now ignored.
type PromptClosed struct{ ID PromptID }

// WaitKind is what an AuthWaiting waits on.
type WaitKind int

const (
	// WaitPasswordCheck: a submitted password is being checked (su, then
	// sudo); lock the input.
	WaitPasswordCheck WaitKind = iota
	// WaitPolkit: polkit's agent has the user's attention for Step (the root
	// helper started through pkexec and nothing came back yet).
	WaitPolkit
)

// AuthWaiting: privilege is being decided outside the front end's prompt.
// AuthResolved always follows before the waiting step or queue goes on.
type AuthWaiting struct {
	Kind WaitKind
	Step string // the step waiting, for WaitPolkit; "" otherwise
}

// AuthResolved ends an AuthWaiting. Granted false with AttemptsLeft > 0
// means another NeedsPassword follows; otherwise the privileged steps end
// as steps.AuthRefused. Detail is a message to show verbatim (a lockout
// notice), or "". Granted with a Detail: the run goes on, and Detail is a
// note about how it will ask (the TUI's sudo keeps no ticket, so archmaid
// asks each time sudo needs the password: privilege.NoTicketNote); a front
// end shows it once, for the run.
type AuthResolved struct {
	Granted      bool
	AttemptsLeft int
	Detail       string
}

// FaillockState is pam_faillock's count for the user, read before a polkit
// prompt (/run/faillock/$USER): Failures of Deny allowed, Locked until
// Unlock. A front end warns when one more failure locks the account.
type FaillockState struct {
	Failures, Deny int
	Locked         bool
	Unlock         time.Time // zero unless Locked
}

// Phase is what the Session is busy with. An open prompt is not a phase: its
// Needs* event (or OutputReport) says so, on top of the phase it opened in;
// a password check is AuthWaiting.
type Phase int

const (
	PhaseIdle        Phase = iota // no action; also right after Finished
	PhaseDiscovering              // an action's Discover runs (Busy says what)
	PhaseProbing                  // checking for a cached sudo ticket
	PhaseRunning                  // a queue runs
)

// PhaseChanged: the Session moved to Phase. Busy is the spinner text while
// discovering ("Scanning for broken packages…"), else "".
type PhaseChanged struct {
	Phase Phase
	Busy  string
}

// StatusUpdated is the status line, once per Driver tick (one ticker per
// Driver, never one per action). Elapsed is the running action's time so
// far and StepElapsed the running step's, both on Options.Now; zero when
// there is none.
type StatusUpdated struct {
	Now                  time.Time
	Status               sysstat.Status
	Elapsed, StepElapsed time.Duration
	Step                 string // the running step's ID, or ""
}

// Notice: the action ended in a one-line message ("No broken packages
// found."); Finished{OutcomeNotice} follows at once. Since the discovery
// record, a DiscoveryResult (Nothing or Failed) with the same text as its
// Summary comes before it, with the record's LogWritten between them.
type Notice struct{ Text string }

// DiscoveryOutcome is how an action's discovery (actions.Action.Discover,
// and Repair's root recheck) came out.
type DiscoveryOutcome int

const (
	// DiscoveryFound: it found something to act on; a pick list, a
	// password prompt, a confirm or the queue follows.
	DiscoveryFound DiscoveryOutcome = iota
	// DiscoveryNothing: it looked and found nothing to do. The action ends
	// at it: LogWritten (its record), Notice, Finished{OutcomeNotice}, and
	// last-run.json says "nothing-found".
	DiscoveryNothing
	// DiscoveryFailed: the look itself failed (a listing that couldn't
	// run, sudo given up on for Repair's recheck). The action ends as for
	// DiscoveryNothing; last-run.json says "failed" with one failed step.
	DiscoveryFailed
)

// String is the outcome as a word: "found", "nothing-found", "failed".
func (o DiscoveryOutcome) String() string {
	if o < DiscoveryFound || o > DiscoveryFailed {
		return "discovery?"
	}
	return [...]string{"found", "nothing-found", "failed"}[o]
}

// DiscoveryResult: an action's discovery ended, and this is what it
// checked — the action's own result when it ends there (Nothing, Failed),
// the run's first stage when it goes on (Found). A front end shows it as
// the run of one "Discovery" step: Summary as its line, Details under it.
//
// Order: a DiscoveryResult comes as the discovery's result arrives (or,
// for Repair under polkit, as its recheck step ends), before anything the
// discovery leads to. An
// action may have more than one (Repair: the user scan's Found, then the
// root recheck's Found, Nothing or Failed); each later one stands for the
// whole discovery so far (its Details include the earlier ones'), so a
// front end shows the latest. Nothing and Failed are followed by the
// record's LogWritten, then Notice and Finished{OutcomeNotice}, and
// nothing else. A Found record is written when the queue it leads to
// starts, after QueueStarted (a LogWritten in the first step's log), so
// the run's log holds what was found; a discovery that leads to nothing
// run (no pick, an Abort) writes no record, as before.
type DiscoveryResult struct {
	Action  string // the menu action's ID (actions.ID*)
	Outcome DiscoveryOutcome
	// Summary is one line on what was checked and what came of it
	// ("Checked 23 stopped services: none are enabled. Skipped 6 (…)."),
	// or of what failed ("Couldn't list services: …"). Plain, sanitized.
	Summary string
	// Details are the full lists, one entry per line, plain and sanitized:
	// each unit with its is-enabled state and each skipped one with the
	// rule that skipped it, the packages pacman flagged, each download with
	// its age. May be empty.
	Details []string
	// Err is the failure alone, one line ("" unless Failed).
	Err string
	// Category is the log the record went to in the same Output (a
	// discovery category, logstore.Category.Discovery, for Nothing and
	// Failed: a discovery-only run never takes a real run's place in the
	// history); "" for Found.
	Category logstore.Category
	// LogPath is that log file's path. The Session leaves it "" (it does
	// no IO); internal/driver fills it in from its store once the record
	// is written, and leaves it "" if the write failed. The stub never sets
	// it.
	LogPath string
	// Started is when the action started (Options.Now); Elapsed is the
	// discovery's time so far.
	Started time.Time
	Elapsed time.Duration
}

// OutputReport opens a report (the AUR audit). Text is the report as plain
// lines; Audit is the same as data. Answer with Confirm(ID, true) to accept
// the flagged findings (only when Audit.CanAccept), Confirm(ID, false) or
// CancelPrompt(ID) to dismiss; Finished{OutcomeReport} follows the answer.
type OutputReport struct {
	ID    PromptID
	Text  string
	Audit *AuditReport // nil for a report that isn't an audit
}

// AuditReport is an AUR audit's result as data.
type AuditReport struct {
	Total    int // foreign packages checked
	Findings []AuditFinding
	// CanAccept: there are high findings a yes on the report records as
	// checked; it is true exactly when the discovery's Report.Accept is set.
	CanAccept bool
}

// AuditFinding is one flagged package (audit.Finding, sanitized).
type AuditFinding struct {
	Name   string
	High   bool
	Flags  []string // what was seen, one short phrase each
	Reason string   // why it's high ("maintainer alice → mallory"), or ""
}

// Outcome is how an action ended.
type Outcome int

const (
	OutcomeCompleted Outcome = iota // the queue ran to its end (Errs may be > 0)
	OutcomeAborted                  // Abort, or a declined required step, even the first
	OutcomeCancelled                // ended before any step ran (a cancelled password prompt, a declined gate)
	OutcomeNotice                   // ended in a Notice
	OutcomeReport                   // ended in a dismissed or accepted OutputReport
)

// String is the outcome as transcripts write it.
func (o Outcome) String() string {
	if o < OutcomeCompleted || o > OutcomeReport {
		return "outcome?"
	}
	return [...]string{"completed", "aborted", "cancelled", "notice", "report"}[o]
}

// Finished is the last event of every action. Errs counts failed steps,
// Skipped skipped and AuthRefused ones.
type Finished struct {
	Action        string
	Outcome       Outcome
	Errs, Skipped int
	Elapsed       time.Duration
	// Refused counts the AuthRefused steps, a part of Skipped: a queue that
	// completed with Refused > 0 is an incomplete run (last-run.json says
	// so), though its Outcome stays OutcomeCompleted.
	Refused int
	// NotRun counts the steps an Abort left pending: they got no event and
	// are not in Skipped. A front end shows them as not run.
	NotRun int
}

// RunReport: what the run changed on the machine (internal/report), made by
// a Driver that was given a report configuration (driver.Deps.RunReport)
// for a real run that reached its queue. The Session never emits it: the
// Driver adds it to the Output that carries Finished, right before
// Finished, and it comes after every step's StepDone. A front end that
// doesn't know it ignores it. Plain data, sanitized: package names and
// files come from pacman.log.
type RunReport struct{ Report runreport.Report }

// ReportAccepted: the user accepted an OutputReport's findings (Confirm(ID,
// true) on a report whose Audit.CanAccept). It follows the RunHook that ran
// the report's Accept (and its HookDone), and comes before
// Finished{OutcomeReport}. Saved says whether the save worked; Text is the
// message to show ("Maintainer changes accepted: …", or why not).
type ReportAccepted struct {
	ID    PromptID
	Saved bool
	Text  string
}

func (QueueStarted) isEvent()    {}
func (StepInserted) isEvent()    {}
func (PlanChanged) isEvent()     {}
func (StepStarted) isEvent()     {}
func (StepDone) isEvent()        {}
func (OutputChunk) isEvent()     {}
func (Aborted) isEvent()         {}
func (LogWritten) isEvent()      {}
func (NeedsConfirm) isEvent()    {}
func (NeedsPassword) isEvent()   {}
func (NeedsSelection) isEvent()  {}
func (PromptClosed) isEvent()    {}
func (AuthWaiting) isEvent()     {}
func (AuthResolved) isEvent()    {}
func (FaillockState) isEvent()   {}
func (PhaseChanged) isEvent()    {}
func (StatusUpdated) isEvent()   {}
func (Notice) isEvent()          {}
func (OutputReport) isEvent()    {}
func (Finished) isEvent()        {}
func (RunReport) isEvent()       {}
func (ReportAccepted) isEvent()  {}
func (DiscoveryResult) isEvent() {}
