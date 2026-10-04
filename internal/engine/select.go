package engine

import (
	"slices"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A pick list, as the TUI's services screen: the discovery's Items are
// shown one line each (shown), the user picks some, and the discovery's
// Steps builds the queue from the picked items, in the list's order. A
// pick is matched by what was shown, so a name an escape made harmless
// still picks the real one. Nothing picked (or nothing that was offered),
// or an empty queue, runs nothing: the action ends cancelled, with no
// last-run record, as in the TUI.

// selectTitle heads an action's pick list.
var selectTitle = map[string]string{
	actions.IDServices: "Enabled but not running — select services to disable",
}

// askSelection opens the pick list d.
func (s *Session) askSelection(o *Output, d actions.NeedsSelection) {
	r := s.run
	r.sel, r.selShown = &d, shown(d.Items)
	r.prompt, r.promptFor = s.promptID(), promptSelect
	o.event(NeedsSelection{ID: r.prompt, Title: selectTitle[r.action], Items: slices.Clone(r.selShown)})
}

// selected takes the user's pick and plans its queue.
func (s *Session) selected(o *Output, picked []string) {
	r := s.run
	d := r.sel
	r.prompt, r.sel = 0, nil
	var sel []string
	for i, sh := range r.selShown {
		if slices.Contains(picked, sh) {
			sel = append(sel, d.Items[i])
		}
	}
	r.selShown = nil
	var queue []steps.Step
	if len(sel) > 0 && d.Steps != nil {
		queue = d.Steps(sel)
	}
	if len(queue) == 0 {
		s.finish(o, OutcomeCancelled)
		return
	}
	s.plan(o, queue, "")
}
