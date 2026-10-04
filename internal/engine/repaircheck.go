package engine

import (
	"errors"
	"slices"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Repair's recheck under polkit (actions.RepairCheckStep): the root helper
// runs check-files over the user scan's candidates and reports the broken
// ones in its check event (HelperCheck). Once the step exits, that decides
// what follows, as the sudo rescan's discovery would have:
//
//   - some broken: the reinstall (actions.RepairStep) goes in behind the
//     check (StepInserted) and runs with its own confirm;
//   - none broken: the action ends in "No broken packages found.";
//   - a failed check, one with no check event, or one naming a package it
//     wasn't asked about (fail closed: nothing it names is reinstalled):
//     the action ends in "Couldn't scan for broken packages: …".
//
// The notices end the action as a discovery's would (OutcomeNotice, no
// last-run record). A dry run runs nothing, so its recheck decides
// nothing; a recheck that never got privilege is AuthRefused as any helper
// step.

var (
	errNoCheck     = errors.New("the root helper reported no result")
	errUnaskedName = errors.New("the root helper reported packages it wasn't asked about")
)

// helperCheck keeps the running repair-check's check event.
func (s *Session) helperCheck(o *Output, in HelperCheck) {
	st := s.current(in.Seq)
	if st == nil || st.step.ID != steps.IDRepairCheck {
		return
	}
	s.run.check = &HelperCheck{Seq: in.Seq, Broken: slices.Clone(in.Broken)}
}

// repairChecked goes on from a repair-check that just settled after exiting
// with err, and reports whether it did (else the queue advances as usual).
func (s *Session) repairChecked(o *Output, err error) bool {
	r := s.run
	st := r.plan[r.cur].step
	if st.ID != steps.IDRepairCheck || s.opts.DryRun {
		return false
	}
	check := r.check
	r.check = nil
	switch {
	case err == nil && check == nil:
		err = errNoCheck
	case err == nil && slices.ContainsFunc(check.Broken, func(n string) bool { return !slices.Contains(st.Items, n) }):
		err = errUnaskedName
	}
	switch {
	case err != nil:
		o.event(Notice{Text: "Couldn't scan for broken packages: " + sanitize.Clip(sanitize.OneLine(err.Error()), 120)})
	case len(check.Broken) == 0:
		o.event(Notice{Text: "No broken packages found."})
	default:
		next := actions.RepairStep(check.Broken)
		r.plan = slices.Insert(r.plan, r.cur+1, slot{step: next})
		o.event(StepInserted{Index: r.cur + 1, Step: planStep(next)})
		s.advance(o)
		return true
	}
	s.finish(o, OutcomeNotice)
	return true
}
