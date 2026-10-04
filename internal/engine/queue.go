package engine

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// run is one action in progress: its discovery, its plan and where the
// queue stands. A Session holds at most one.
type run struct {
	action  string
	started time.Time

	discoverSeq uint64 // the Discover in flight, or 0
	probing     bool   // a Probe is in flight
	planned     bool   // the action reached its queue (auth, gate or run): it gets a last-run record
	gate        string
	pending     []steps.Step // the plan, until QueueStarted
	prefetch    []string     // prefetches started and not yet cancelled

	queued    bool
	plan      []slot
	cur       int       // the step being asked about or run; -1 before the first
	stepSeq   uint64    // the running step's StartStep, or 0
	stepAt    time.Time // when it started
	out       []string  // its output so far, for its log entry
	keepAlive bool

	prompt    PromptID
	promptFor promptKind

	// Auth (auth.go).
	attempts       int                // wrong passwords so far this run
	checking       bool               // an Authenticate is in flight
	sudoGone       bool               // the password was given up on: NeedsPriv steps are refused
	root           *actions.NeedsRoot // a discovery held until root is primed
	authed         bool               // the rescan ran primed: its queue needs no probe
	faillockFor    faillockWait       // what a ReadFaillock in flight goes on to
	acceptedLaunch bool               // the helper step waiting on it was confirmed
	polkitWait     bool               // AuthWaiting{WaitPolkit} is open for the running step
	ask            *askpass           // the open askpass request (askpass.go), or nil

	errs, skipped, refused int
}

// slot is one step of the plan and how it stands.
type slot struct {
	step   steps.Step
	status steps.Status
	gated  bool // its confirm came from the step before's ConfirmNext
}

type promptKind int

const (
	promptGate promptKind = iota
	promptStep
	promptPassword // the up-front one
	promptAskpass
)

// running is the step running now, or nil.
func (r *run) running() *slot {
	if r.cur < 0 || r.cur >= len(r.plan) || r.plan[r.cur].status != steps.Running {
		return nil
	}
	return &r.plan[r.cur]
}

// maxAttempts is how many passwords the up-front prompt takes.
const maxAttempts = 3

// plan takes the queue an action (or its discovery) built and gets it going:
// the prefetches it names start now, so they run behind the prompts; a queue
// with a NeedsPriv step is probed for a cached ticket first (AuthSudo,
// outside a dry run), and the proceed-gate is asked only when no password
// prompt is — the prompt is the gate otherwise.
func (s *Session) plan(o *Output, queue []steps.Step, gate string) {
	r := s.run
	r.planned, r.gate, r.pending = true, gate, queue
	for _, st := range queue {
		if st.Prefetch != "" && !slices.Contains(r.prefetch, st.Prefetch) {
			r.prefetch = append(r.prefetch, st.Prefetch)
			o.effect(StartPrefetch{Name: st.Prefetch})
		}
	}
	if !needsPriv(queue) {
		s.startQueue(o)
		return
	}
	if s.opts.DryRun || s.opts.Auth == AuthPolkit || r.authed {
		// A dry run never asks; polkit asks per step; a rescan's queue
		// runs on the ticket its NeedsRoot primed.
		s.authorized(o)
		return
	}
	r.probing = true
	s.setPhase(o, PhaseProbing, "")
	o.effect(Probe{})
}

func needsPriv(queue []steps.Step) bool {
	return slices.ContainsFunc(queue, func(s steps.Step) bool { return s.NeedsPriv })
}

// probed takes the probe's answer: a cached ticket goes on (to the gate,
// or to a held NeedsRoot's rescan), otherwise the faillock count is read and
// the password asked for.
func (s *Session) probed(o *Output, in ProbeDone) {
	r := s.run
	if r == nil || !r.probing {
		return
	}
	r.probing = false
	switch {
	case in.Cached && r.root != nil:
		s.rescan(o)
	case in.Cached:
		s.authorized(o)
	default:
		s.readFaillock(o, faillockUpFront)
	}
}

// authorized goes on once privilege is settled: the gate if the action has
// one, else the queue.
func (s *Session) authorized(o *Output) {
	r := s.run
	if r.gate == "" {
		s.startQueue(o)
		return
	}
	r.prompt, r.promptFor = s.promptID(), promptGate
	title, body := splitQuestion(r.gate)
	o.event(NeedsConfirm{ID: r.prompt, Kind: ConfirmGate, Title: title, Body: body, Msg: r.gate})
}

// startQueue publishes the plan and runs its first step. hyprpm is kept
// last: it drops the sudo ticket on exit, so no step may need root after it.
func (s *Session) startQueue(o *Output) {
	r := s.run
	queue := hyprpmLast(r.pending)
	r.pending = nil
	r.plan = make([]slot, len(queue))
	ps := make([]PlanStep, len(queue))
	for i, st := range queue {
		r.plan[i] = slot{step: st}
		ps[i] = planStep(st)
	}
	r.queued = true
	s.setPhase(o, PhaseRunning, "")
	o.event(QueueStarted{Action: r.action, Steps: ps, DryRun: s.opts.DryRun, Started: s.now()})
	if !s.opts.DryRun && s.opts.Auth == AuthSudo && !r.sudoGone && needsPriv(queue) {
		r.keepAlive = true
		o.effect(KeepAlive{On: true})
	}
	s.advance(o)
}

// hyprpmLast moves the hyprpm step, if there is one, to the end of queue.
// The actions already put it there (TestNoPrivilegedStepAfterHyprpm); this
// keeps a queue built any other way from running root work after it.
func hyprpmLast(queue []steps.Step) []steps.Step {
	i := slices.IndexFunc(queue, func(s steps.Step) bool { return s.ID == steps.IDHyprpm })
	if i < 0 || i == len(queue)-1 {
		return queue
	}
	h := queue[i]
	return append(slices.Delete(slices.Clone(queue), i, i+1), h)
}

// planStep is s as a front end sees it.
func planStep(s steps.Step) PlanStep {
	p := PlanStep{
		ID: s.ID, Verb: s.Verb, Title: s.Title, InProc: s.Run != nil,
		NeedsPriv: s.NeedsPriv, Confirm: s.Confirm != "", Optional: s.Optional,
		AlwaysLog: s.AlwaysLog, Category: s.LogCategory(),
	}
	if !p.InProc {
		p.Argv = slices.Clone(s.Argv)
	}
	return p
}

// advance moves to the next step: its confirm if it has one, else it runs.
// Once the password was given up on, a NeedsPriv step is refused without
// its confirm. Past the last step the action is over.
func (s *Session) advance(o *Output) {
	r := s.run
	r.cur++
	if r.cur >= len(r.plan) {
		s.finish(o, OutcomeCompleted)
		return
	}
	if st := r.plan[r.cur].step; st.NeedsPriv && r.sudoGone {
		s.settle(o, steps.AuthRefused, "")
		s.log(o, st.LogCategory(), "SKIP", st.LogLabel+" (sudo authentication failed)", "")
		s.advance(o)
		return
	}
	if r.plan[r.cur].step.Confirm != "" {
		s.askStep(o)
		return
	}
	s.launch(o, false)
}

// askStep opens the current step's confirm.
func (s *Session) askStep(o *Output) {
	r := s.run
	st := r.plan[r.cur].step
	r.prompt, r.promptFor = s.promptID(), promptStep
	msg := sanitize.String(st.Confirm, sanitize.Plain)
	title, body := splitQuestion(msg)
	nc := NeedsConfirm{
		ID: r.prompt, Kind: ConfirmStep, Step: st.ID,
		Title: title, Body: body, Msg: msg,
		Destructive: destructive[st.ID], Commits: st.OnAccept != nil,
	}
	switch {
	case r.plan[r.cur].gated:
		nc.Kind = ConfirmAuditGate
	case st.ID == steps.IDOrphansRemove:
		nc.Kind = ConfirmOrphans
	case st.ID == steps.IDDownloads:
		nc.Kind = ConfirmDownloads
	}
	if nc.Kind == ConfirmOrphans {
		nc.Direct, nc.Cascade = shown(st.Items), shown(st.Cascade)
	} else {
		nc.Items = shown(st.Items)
	}
	o.event(nc)
}

// shown makes a step's Items safe to list: one line each, quoted when they
// hold anything a terminal would act on.
func shown(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = sanitize.QuoteName(it, 200)
	}
	return out
}

// destructive marks the steps whose confirm guards something archmaid
// can't bring back.
var destructive = map[string]bool{
	steps.IDPaccacheWipe:  true,
	steps.IDOrphansRemove: true,
	steps.IDUserCache:     true,
	steps.IDDownloads:     true,
}

// splitQuestion is a confirm's first line as its title, the rest (blank
// lines between trimmed) as its body.
func splitQuestion(msg string) (title, body string) {
	title, body, _ = strings.Cut(msg, "\n")
	return title, strings.TrimLeft(body, "\n")
}

// launch runs the current step: accepted says it was confirmed. A polkit
// helper step reads the faillock count first (faillockRead goes on).
func (s *Session) launch(o *Output, accepted bool) {
	r := s.run
	if s.polkitStep(r.plan[r.cur].step) {
		r.acceptedLaunch = accepted
		s.readFaillock(o, faillockPolkit)
		return
	}
	s.startStep(o, accepted)
}

// startStep starts the current step; a confirmed one's OnAccept runs first
// (a failure there is the hook's to report and doesn't stop the step).
func (s *Session) startStep(o *Output, accepted bool) {
	r := s.run
	st := &r.plan[r.cur]
	r.stepSeq, r.stepAt, r.out = s.seq(), s.now(), nil
	if accepted && st.step.OnAccept != nil {
		o.effect(RunHook{Seq: s.seq(), Step: st.step.ID, Fn: st.step.OnAccept})
	}
	st.status = steps.Running
	o.event(StepStarted{ID: st.step.ID, Index: r.cur, At: r.stepAt})
	o.effect(StartStep{Seq: r.stepSeq, Step: st.step, Env: steps.Env{DryRun: s.opts.DryRun}})
}

// decline answers "no" to the current step's confirm: an optional step is
// skipped (an AlwaysLog one logs that), a required one ends the queue with
// it and every step after it skipped.
func (s *Session) decline(o *Output) {
	r := s.run
	st := r.plan[r.cur].step
	if st.Optional {
		s.settle(o, steps.Skipped, "")
		if st.AlwaysLog {
			s.log(o, st.LogCategory(), "SKIP", st.LogLabel+" (declined)", "")
		}
		s.advance(o)
		return
	}
	for ; r.cur < len(r.plan); r.cur++ {
		s.settle(o, steps.Skipped, "")
	}
	s.finish(o, OutcomeAborted)
}

// current is the running step an Input with seq belongs to, or nil when
// seq is stale.
func (s *Session) current(seq uint64) *slot {
	r := s.run
	if r == nil || seq == 0 || seq != r.stepSeq {
		return nil
	}
	return r.running()
}

func (s *Session) stepOutput(o *Output, in StepOutput) {
	st := s.current(in.Seq)
	if st == nil || len(in.Lines) == 0 {
		return
	}
	s.output(o, in.Lines)
}

// output adds lines to the running step's output and publishes them. They
// are sanitized here whatever the Driver did: an Event never carries more
// than SGR colour.
func (s *Session) output(o *Output, lines []string) {
	r := s.run
	clean := make([]string, len(lines))
	for i, l := range lines {
		clean[i] = sanitize.String(l, sanitize.KeepSGR)
	}
	r.out = append(r.out, clean...)
	o.event(OutputChunk{ID: r.plan[r.cur].step.ID, Index: r.cur, Lines: clean})
}

func (s *Session) stepExited(o *Output, in StepExited) {
	if s.current(in.Seq) == nil {
		return
	}
	s.run.stepSeq = 0
	s.dropAskpass(o)
	s.endPolkitWait(o, in.NotGranted)
	if in.NotGranted {
		st := s.run.plan[s.run.cur].step
		s.settle(o, steps.AuthRefused, "")
		s.log(o, st.LogCategory(), "SKIP", st.LogLabel+" (privilege not granted)", "")
		s.advance(o)
		return
	}
	s.settle(o, outcome(in.Err), errText(in.Err))
	s.logDone(o, in.Err)
	s.advance(o)
}

// errAfterHyprpm refuses a result that would put a step needing root behind
// hyprpm, which drops the sudo ticket on exit.
var errAfterHyprpm = errors.New("a step that needs root may not run after hyprpm")

// stepResult takes an in-process step's result, the way the TUI does, and
// fails closed where the result is malformed:
//
//   - A result that sets both ConfirmNext and InsertNext (steps.Result.Check)
//     is refused: the step fails with the refusal in its output, nothing is
//     inserted and Commit never runs — but the gate stands, on the step it
//     was meant for (with nothing inserted, the next one), so that step runs
//     only on "yes". So is an InsertNext of a step needing root once hyprpm
//     has run.
//   - InsertNext (only from a result without Err) goes right behind the
//     step: StepInserted.
//   - ConfirmNext puts that confirm on the next step and makes it optional
//     (PlanChanged), with Commit as its OnAccept: it runs only on "yes". With
//     no next step both are dropped.
//   - Otherwise Commit runs now (a RunHook), before the step's log entry,
//     whether the step failed or not, as in the TUI: for the downloads
//     delete it is what says the result was taken, so a partial failure
//     isn't logged as an abort too. (A failed audit has no Commit.)
func (s *Session) stepResult(o *Output, in StepResult) {
	st := s.current(in.Seq)
	if st == nil {
		return
	}
	r := s.run
	r.stepSeq = 0
	res := in.Result
	refuse := func(err error) {
		res = steps.Result{Output: res.Output + "\narchmaid: " + err.Error(), Err: err, ConfirmNext: res.ConfirmNext}
	}
	if err := res.Check(); err != nil {
		refuse(err)
	}
	if res.Err == nil && res.InsertNext != nil && res.InsertNext.NeedsPriv &&
		slices.ContainsFunc(r.plan[:r.cur+1], func(sl slot) bool { return sl.step.ID == steps.IDHyprpm }) {
		refuse(errAfterHyprpm)
	}
	if p := st.step.Prefetch; p != "" && slices.Contains(r.prefetch, p) {
		// consumed: the step awaited it
		r.prefetch = slices.DeleteFunc(r.prefetch, func(n string) bool { return n == p })
		o.effect(CancelPrefetch{Name: p})
	}
	if res.Output != "" {
		s.output(o, strings.Split(strings.TrimRight(res.Output, "\n"), "\n"))
	}
	s.settle(o, outcome(res.Err), errText(res.Err))
	if res.Err == nil && res.InsertNext != nil {
		next := *res.InsertNext
		r.plan = slices.Insert(r.plan, r.cur+1, slot{step: next})
		o.event(StepInserted{Index: r.cur + 1, Step: planStep(next)})
	}
	switch {
	case res.ConfirmNext != "" && r.cur+1 < len(r.plan):
		next := &r.plan[r.cur+1]
		next.step.Confirm, next.step.Optional, next.step.OnAccept = res.ConfirmNext, true, res.Commit
		next.gated = true
		o.event(PlanChanged{Index: r.cur + 1, Step: planStep(next.step)})
	case res.ConfirmNext == "" && res.Commit != nil:
		o.effect(RunHook{Seq: s.seq(), Step: st.step.ID, Fn: res.Commit})
	}
	s.logDone(o, res.Err)
	s.advance(o)
}

func outcome(err error) steps.Status {
	if err != nil {
		return steps.Failed
	}
	return steps.OK
}

// errText is a step's failure as StepDone carries it: sanitized, one line.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return sanitize.OneLine(err.Error())
}

// logDone writes the log entry of the step that just finished with err:
// failures always, successes only for AlwaysLog steps, every outcome in a
// dry run; the body is its whole output.
func (s *Session) logDone(o *Output, err error) {
	r := s.run
	st := r.plan[r.cur].step
	var body strings.Builder
	for _, l := range r.out {
		body.WriteString(l)
		body.WriteByte('\n')
	}
	r.out = nil
	switch {
	case err != nil:
		s.log(o, st.LogCategory(), "FAIL", st.LogLabel, body.String())
	case s.opts.DryRun:
		s.log(o, st.LogCategory(), "DRYRUN", st.LogLabel, body.String())
	case st.AlwaysLog:
		s.log(o, st.LogCategory(), "OK", st.LogLabel, body.String())
	}
}

// settle gives the current step its final status, counted in the run's
// tallies, with StepDone.
func (s *Session) settle(o *Output, status steps.Status, errText string) {
	r := s.run
	st := &r.plan[r.cur]
	var elapsed time.Duration
	if st.status == steps.Running {
		elapsed = s.now().Sub(r.stepAt)
	}
	st.status = status
	switch status {
	case steps.Failed:
		r.errs++
	case steps.Skipped:
		r.skipped++
	case steps.AuthRefused:
		r.skipped++
		r.refused++
	}
	o.event(StepDone{ID: st.step.ID, Index: r.cur, Status: status, Elapsed: elapsed, Err: errText})
}

// log writes an entry (the WriteLog effect) and says so (LogWritten).
func (s *Session) log(o *Output, cat logstore.Category, kind, label, body string) {
	o.effect(WriteLog{Category: cat, Kind: kind, Label: label, Body: body})
	o.event(LogWritten{Category: cat, Header: kind + ": " + sanitize.String(label, sanitize.Plain)})
}

func (s *Session) discovered(o *Output, in Discovered) {
	r := s.run
	if r == nil || in.Seq == 0 || in.Seq != r.discoverSeq {
		return
	}
	r.discoverSeq = 0
	switch d := in.Discovery.(type) {
	case actions.Notice:
		o.event(Notice{Text: sanitize.String(d.Text, sanitize.Plain)})
		s.finish(o, OutcomeNotice)
	case actions.Pending:
		s.plan(o, d.Steps, "")
	case actions.NeedsRoot:
		s.needsRoot(o, d)
	default:
		panic(errNotImplemented) // NeedsSelection, Report (E5)
	}
}

// finish ends the action: the keep-alive off, prefetches dropped, the
// last-run record for an action that reached its queue or its auth (never
// for a notice, as in the TUI), then Finished.
func (s *Session) finish(o *Output, outcome Outcome) {
	r := s.run
	s.dropAskpass(o)
	if r.keepAlive {
		o.effect(KeepAlive{On: false})
	}
	for _, p := range r.prefetch {
		o.effect(CancelPrefetch{Name: p})
	}
	now := s.now()
	if r.planned && outcome != OutcomeNotice {
		o.effect(WriteLastRun{Record: s.record(outcome, now)})
	}
	o.event(Finished{Action: r.action, Outcome: outcome, Errs: r.errs, Skipped: r.skipped, Refused: r.refused, Elapsed: now.Sub(r.started)})
	s.run = nil
	s.setPhase(o, PhaseIdle, "")
}

// record is the last-run record for an action that ended with outcome. Its
// outcome follows the TUI's: cancelled whenever no step ran (a declined
// gate or required first step, a cancelled password), aborted for a queue a
// declined required step stopped, failed on any failed step, incomplete
// when a step was refused privilege, else ok; StepsRefused counts those.
func (s *Session) record(outcome Outcome, now time.Time) logstore.RunRecord {
	r := s.run
	ran, skipped, refused := false, 0, 0
	for _, st := range r.plan {
		switch st.status {
		case steps.OK, steps.Failed:
			ran = true
		case steps.AuthRefused:
			refused++
			skipped++
		case steps.Skipped, steps.Aborted:
			skipped++
		}
	}
	rec := logstore.RunRecord{
		Action: r.action, Started: r.started, Finished: now,
		StepsFailed: r.errs, StepsSkipped: skipped, StepsRefused: refused,
		DryRun: s.opts.DryRun,
	}
	switch {
	case !ran:
		rec.Outcome = logstore.OutcomeCancelled
	case outcome == OutcomeAborted:
		rec.Outcome = logstore.OutcomeAborted
	case r.errs > 0:
		rec.Outcome = logstore.OutcomeFailed
	case refused > 0:
		rec.Outcome = logstore.OutcomeIncomplete
	default:
		rec.Outcome = logstore.OutcomeOK
	}
	return rec
}
