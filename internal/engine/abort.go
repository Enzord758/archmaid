package engine

import "github.com/Enzord758/archmaid/internal/steps"

// abort ends the action in progress at once, as the TUI's abortRunning
// does, from wherever it stands:
//
//   - a discovery in flight is stopped (Stop with its Seq);
//   - an open prompt is withdrawn (PromptClosed), a password check in
//     flight resolves as not granted (AuthResolved{}), and a probe or
//     faillock read in flight is forgotten: their late answers are ignored;
//   - a running step is stopped (Stop: the Driver sends SIGINT and, after
//     its grace, SIGKILL, or cancels an in-process step's ctx with plain
//     context.Canceled, which the downloads delete logs as its own ABORT),
//     its askpass request hung up on, its polkit wait resolved, and it ends
//     with Aborted. The engine writes no log entry for it, as the TUI.
//
// Steps still pending get no event: Finished counts them in NotRun. The
// action is OutcomeAborted once any step started, else OutcomeCancelled;
// last-run.json says the same, counting the aborted step and the pending
// ones as skipped (record).
func (s *Session) abort(o *Output) {
	r := s.run
	if r.discoverSeq != 0 {
		o.effect(Stop{Seq: r.discoverSeq})
		r.discoverSeq = 0
	}
	if r.prompt != 0 && r.promptFor != promptAskpass { // an askpass prompt goes with its step
		o.event(PromptClosed{ID: r.prompt})
		r.prompt = 0
	}
	if r.checking {
		o.event(AuthResolved{})
		r.checking = false
	}
	r.probing, r.faillockFor, r.root = false, faillockNone, nil
	if st := r.running(); st != nil {
		o.effect(Stop{Seq: r.stepSeq})
		r.stepSeq = 0
		s.dropAskpass(o)
		if r.polkitWait {
			r.polkitWait = false
			o.event(AuthResolved{})
		}
		st.status = steps.Aborted
		o.event(Aborted{ID: st.step.ID, Index: r.cur, Elapsed: s.now().Sub(r.stepAt)})
	}
	outcome := OutcomeCancelled
	if r.anyStarted() {
		outcome = OutcomeAborted
	}
	s.finish(o, outcome)
}

// anyStarted reports whether any step of the plan started.
func (r *run) anyStarted() bool {
	for _, sl := range r.plan {
		switch sl.status {
		case steps.OK, steps.Failed, steps.Aborted, steps.Running:
			return true
		}
	}
	return false
}
