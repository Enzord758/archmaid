package engine

import (
	"slices"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/sanitize"
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
	actions.IDServices:    "Enabled but not running — select services to disable",
	actions.IDClearCaches: "Clear caches — tick what to delete",
}

// selectGo is the verb of the go-ahead key of an action's pick.
var selectGo = map[string]string{
	actions.IDServices:    "disable",
	actions.IDClearCaches: "clear",
}

// askSelection opens the pick list d.
func (s *Session) askSelection(o *Output, d actions.NeedsSelection) {
	r := s.run
	r.sel, r.selShown = &d, shown(d.Items)
	r.prompt, r.promptFor = s.promptID(), promptSelect
	o.event(NeedsSelection{ID: r.prompt, Title: selectTitle[r.action], Items: slices.Clone(r.selShown),
		Rows: shownRows(d.Rows, len(d.Items)), Go: selectGo[r.action]})
}

// selected takes the user's pick and plans its queue.
func (s *Session) selected(o *Output, picked []string) {
	r := s.run
	d := r.sel
	r.prompt, r.sel = 0, nil
	var sel []string
	for i, sh := range r.selShown {
		if slices.Contains(picked, sh) && !rowDisabled(d, i) {
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
	r.pickQueue = true
	s.plan(o, queue, "")
}

// rowDisabled reports whether item i of the pick can't be picked: its row is
// disabled, or the pick's Rows don't match its Items (a front end shows no
// rows then, so no row could have been meant to be enabled): every item is
// disabled. A pick with no Rows at all (Services) has no disabled item.
func rowDisabled(d *actions.NeedsSelection, i int) bool {
	if len(d.Rows) == 0 {
		return false
	}
	return len(d.Rows) != len(d.Items) || d.Rows[i].Disabled
}

// shownRows makes a pick's rows safe to list (one line each, as shown
// does for the items); nil unless there is one per item.
func shownRows(rows []actions.PickRow, n int) []actions.PickRow {
	if len(rows) != n || n == 0 {
		return nil
	}
	out := make([]actions.PickRow, n)
	for i, r := range rows {
		out[i] = actions.PickRow{Label: sanitize.QuoteName(r.Label, 200), Note: sanitize.QuoteName(r.Note, 300), Disabled: r.Disabled}
	}
	return out
}
