package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"

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
//     check (StepInserted), headed by a Found DiscoveryResult whose
//     record goes into the reinstall's log, and runs with its own confirm;
//   - none broken: the action ends in "No broken packages: …";
//   - a failed check, one with no check event, or one naming a package it
//     wasn't asked about (fail closed: nothing it names is reinstalled):
//     the action ends in "Couldn't scan for broken packages: …".
//
// The notices end the action as a discovery's would (endDiscovery: a
// DiscoveryResult, its record in the repair log, OutcomeNotice and a
// "nothing-found" or "failed" last-run record). A dry run runs nothing,
// so its recheck decides nothing; a recheck that never got privilege is
// AuthRefused as any helper step.

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
	tried := "Rechecked as root through the root helper (check-files): " + nameList(shown(st.Items))
	switch {
	case err != nil:
		why := sanitize.Clip(sanitize.OneLine(err.Error()), 120)
		s.endDiscovery(o, actions.Notice{Text: "Couldn't scan for broken packages: " + why, Err: why,
			Details: []string{tried + ": failed"}})
	case len(check.Broken) == 0:
		s.endDiscovery(o, actions.Notice{
			Text: fmt.Sprintf("No broken packages: the %d flagged as the user (%s) are complete as root.",
				len(st.Items), nameList(shown(st.Items))),
			Details: []string{tried + ": nothing missing"}})
	default:
		next := actions.RepairStep(check.Broken)
		r.plan = slices.Insert(r.plan, r.cur+1, slot{step: next})
		o.event(StepInserted{Index: r.cur + 1, Step: planStep(next)})
		// What the recheck found heads the reinstall's log (a real run from
		// here on, whatever happens to the reinstall).
		s.found(o, actions.Checked{
			Summary: fmt.Sprintf("%d broken (%s), confirmed as root by the root helper.",
				len(check.Broken), nameList(shown(check.Broken))),
			Details: []string{tried + ": " + strings.Join(shown(check.Broken), " ") + " broken"},
		})
		s.writeFound(o, next.LogCategory())
		s.advance(o)
		return true
	}
	return true
}

// nameList is names for a summary: the first three, then "…".
func nameList(names []string) string {
	if len(names) > 3 {
		return strings.Join(names[:3], ", ") + ", …"
	}
	return strings.Join(names, ", ")
}
