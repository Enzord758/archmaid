package engine

import (
	"errors"

	"github.com/Enzord758/archmaid/internal/steps"
)

// A combined run: under AuthPolkit, consecutive helper steps that name the
// same steps.Step.Combine verb run as ONE root-helper call, so one polkit
// dialog covers them all (Full Cleanup's root steps: full-cleanup). The
// owner's rule is one dialog per run, as the TUI asks one sudo password.
//
//   - When the queue reaches the first of them, every confirm among them is
//     asked first, in plan order (a declined optional one is Skipped at
//     once, a declined required one ends the queue as ever), so no question
//     is left once the dialog is up.
//   - Then the faillock count is read once: a locked account refuses every
//     accepted step (AuthRefused, no dialog).
//   - Then the first accepted step starts (StepStarted, AuthWaiting{
//     WaitPolkit}) and one StartStep runs the call: its Step has Verb =
//     the Combine verb and Parts = the accepted steps, in order.
//   - Each later part starts on PartStarted and every part ends on PartDone,
//     so each step keeps its own row: StepStarted, OutputChunk, StepDone,
//     one step at a time, in plan order.
//   - StepExited ends the call: NotGranted refuses every part that hasn't
//     ended (AuthRefused: a refused dialog refuses exactly the steps it
//     covered); otherwise the part still running ends with Err, and a part
//     that never started is started and failed with Err (the helper never
//     reached it).
//
// Then the queue goes on behind the combined steps. Steps with an OnAccept
// never combine (it would have to run before a dialog that covers others),
// and a lone step runs on its own verb, as before.

// combined is the combined run in progress.
type combined struct {
	verb       string
	start, end int    // its plan slots, [start, end)
	running    bool   // the call was started (the confirms are behind it)
	seq        uint64 // the call's StartStep
}

// errPartNotRun fails a part the call ended before, when it said nothing
// else (a Driver always says why; this is the fallback).
var errPartNotRun = errors.New("the root helper ended before this step ran")

// combineAt starts a combined run at the current step if it begins one,
// and reports whether it did.
func (s *Session) combineAt(o *Output) bool {
	r := s.run
	verb := r.plan[r.cur].step.Combine
	if verb == "" || !s.combinable(r.plan[r.cur].step, verb) {
		return false
	}
	end := r.cur + 1
	for end < len(r.plan) && s.combinable(r.plan[end].step, verb) {
		end++
	}
	if end-r.cur < 2 {
		return false
	}
	r.comb = &combined{verb: verb, start: r.cur, end: end}
	s.combineNext(o)
	return true
}

// combinable reports whether st can be a part of a combined call of verb.
func (s *Session) combinable(st steps.Step, verb string) bool {
	return st.Combine == verb && s.polkitStep(st) && st.OnAccept == nil && len(st.Parts) == 0
}

// combineNext asks the next confirm of the combined steps, from the
// current one on; with none left it goes on to the call.
func (s *Session) combineNext(o *Output) {
	r, c := s.run, s.run.comb
	for ; r.cur < c.end; r.cur++ {
		sl := &r.plan[r.cur]
		if sl.status != steps.Pending || sl.accepted {
			continue
		}
		if sl.step.Confirm != "" {
			s.askStep(o)
			return
		}
		sl.accepted = true
	}
	if parts := s.parts(); len(parts) > 0 {
		r.cur = parts[0]
		s.readFaillock(o, faillockCombined)
		return
	}
	s.combineEnd(o) // every one of them was declined
}

// parts are the combined steps the call runs: accepted, not yet ended.
func (s *Session) parts() []int {
	r, c := s.run, s.run.comb
	var idx []int
	for i := c.start; i < c.end; i++ {
		if sl := r.plan[i]; sl.accepted && (sl.status == steps.Pending || sl.status == steps.Running) {
			idx = append(idx, i)
		}
	}
	return idx
}

// combineLocked refuses every part without a dialog: the account is
// locked, so even the right password would fail.
func (s *Session) combineLocked(o *Output) {
	r := s.run
	for _, i := range s.parts() {
		r.cur = i
		st := r.plan[i].step
		s.settle(o, steps.AuthRefused, "")
		s.log(o, st.LogCategory(), "SKIP", st.LogLabel+" (account locked)", "")
	}
	s.combineEnd(o)
}

// combineStart starts the call: the first part's row, the one StartStep,
// and the polkit wait.
func (s *Session) combineStart(o *Output) {
	r, c := s.run, s.run.comb
	idx := s.parts()
	call := steps.Step{ID: c.verb, Verb: c.verb, Title: "Running the root steps…", NeedsPriv: true}
	for _, i := range idx {
		call.Parts = append(call.Parts, r.plan[i].step)
	}
	c.running, c.seq = true, s.seq()
	r.stepSeq = c.seq
	s.startPart(o, idx[0])
	o.effect(StartStep{Seq: c.seq, Step: call, Env: steps.Env{DryRun: s.opts.DryRun}})
	o.event(AuthWaiting{Kind: WaitPolkit, Step: r.plan[idx[0]].step.ID})
	r.polkitWait = true
}

// startPart makes slot i the running step.
func (s *Session) startPart(o *Output, i int) {
	r := s.run
	r.cur, r.stepAt, r.out, r.check = i, s.now(), nil, nil
	r.plan[i].status = steps.Running
	o.event(StepStarted{ID: r.plan[i].step.ID, Index: i, At: r.stepAt})
}

// callOf is the running combined call in.Seq belongs to, or nil.
func (s *Session) callOf(seq uint64) *combined {
	r := s.run
	if r == nil || r.comb == nil || !r.comb.running || seq == 0 || seq != r.comb.seq {
		return nil
	}
	return r.comb
}

// partStarted starts the next part, if in names it and the one before has
// ended. Anything else is ignored: the call's StepExited settles what is
// left.
func (s *Session) partStarted(o *Output, in PartStarted) {
	if s.callOf(in.Seq) == nil || s.run.running() != nil {
		return
	}
	for _, i := range s.parts() {
		if i > s.run.cur {
			if s.run.plan[i].step.ID == in.Step {
				s.startPart(o, i)
			}
			return
		}
	}
}

// partDone ends the running part, if in names it.
func (s *Session) partDone(o *Output, in PartDone) {
	if s.callOf(in.Seq) == nil {
		return
	}
	st := s.run.running()
	if st == nil || st.step.ID != in.Step {
		return
	}
	s.settle(o, outcome(in.Err), errText(in.Err))
	s.logDone(o, in.Err)
}

// combinedExited ends the call (see the top of this file) and goes on
// behind the combined steps.
func (s *Session) combinedExited(o *Output, in StepExited) {
	r := s.run
	r.stepSeq = 0
	s.endPolkitWait(o, in.NotGranted)
	for _, i := range s.parts() {
		st := r.plan[i].step
		switch {
		case in.NotGranted:
			r.cur = i
			s.settle(o, steps.AuthRefused, "")
			s.log(o, st.LogCategory(), "SKIP", st.LogLabel+" (privilege not granted)", "")
			continue
		case r.plan[i].status == steps.Pending:
			s.startPart(o, i)
			err := in.Err
			if err == nil {
				err = errPartNotRun
			}
			s.settle(o, steps.Failed, errText(err))
			s.logDone(o, err)
			continue
		}
		r.cur = i
		s.settle(o, outcome(in.Err), errText(in.Err))
		s.logDone(o, in.Err)
	}
	s.combineEnd(o)
}

// combineEnd leaves the combined run and goes on with the step after it.
func (s *Session) combineEnd(o *Output) {
	r := s.run
	r.cur = r.comb.end - 1
	r.comb = nil
	s.advance(o)
}
