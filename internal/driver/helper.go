package driver

import (
	"context"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Helper runs one root-helper step under polkit and emits what happens as
// engine Inputs, in order, the last being exactly one StepExited for seq:
// helperclient.Run's shape, which is the default (Deps.Helper).
//
// It is called on a worker; the Driver forwards each emitted Input to the
// Session, in order, on its loop. Cancelling ctx (Stop, Close) kills
// pkexec while the polkit dialog is up. Once privilege was granted the
// helper runs as root and can't be signalled by us, so Run waits for it:
// Stop never blocks the loop (it only cancels), but Close, which waits for
// every worker, then waits for the helper to finish its verb.
type Helper func(ctx context.Context, seq uint64, st steps.Step, emit func(engine.Input)) helperclient.Outcome

// startHelper runs a helper step on a worker, forwarding its Inputs.
func (d *Driver) startHelper(e engine.StartStep) {
	ctx, cancel := context.WithCancelCause(d.root)
	d.cancels[e.Seq] = cancel
	d.work(func() engine.Input {
		// Its Err is in the StepExited it emitted; the rest is for logs.
		d.cfg.Helper(ctx, e.Seq, e.Step, func(in engine.Input) { d.post(msg{in: in}) })
		return nil
	})
}
