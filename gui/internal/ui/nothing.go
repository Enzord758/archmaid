package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// "No cache to clear" (#207, the owner): Clear caches that finds both
// caches empty is not a run, so it doesn't take the Run page. Start has
// switched to it for the measuring; when the discovery ends with nothing
// found (the engine's DiscoveryNothing, which for this action means both
// caches are just empty, a look that failed is DiscoveryFailed), the
// window drops that discovery from the model (the Run page keeps the last
// run, as for a declined gate), goes back to the page Start left and says
// so in a dialog. The engine's record is unchanged: last-run.json says
// "nothing found" and the discovery log has its CHECK record; there is no
// queue and no run record.

// takeNothing returns evs without the empty-cache discovery result and
// remembers that the dialog is due when the action's Finished arrives. The
// Notice event stays: the status line says it until the next action.
func (w *Window) takeNothing(evs []engine.Event) []engine.Event {
	out := make([]engine.Event, 0, len(evs))
	for _, ev := range evs {
		if d, ok := ev.(engine.DiscoveryResult); ok && d.Action == actions.IDClearCaches && d.Outcome == engine.DiscoveryNothing {
			w.nothingPending = true
			continue
		}
		out = append(out, ev)
	}
	return out
}

// showNothing goes back to the page Start left and opens the popup.
func (w *Window) showNothing() {
	w.nothingPending = false
	if w.pageBefore != "" {
		w.stack.SetVisibleChildName(w.pageBefore)
	}
	if w.nothingDialog != nil {
		return
	}
	a := adw.NewAlertDialog(actions.NoCacheHeading, actions.NoCacheReason)
	a.SetBodyUseMarkup(false)
	a.SetHeadingUseMarkup(false)
	a.AddResponse("ok", "OK")
	a.SetDefaultResponse("ok")
	a.SetCloseResponse("ok")
	d := alertDialog(a)
	d.ConnectResponse(func(string) {
		if w.nothingDialog == d {
			w.nothingDialog = nil
		}
	})
	w.nothingDialog = d
	d.show(w)
}
