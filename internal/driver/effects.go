package driver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// effect starts one Effect, on the loop (see the package doc). WriteLog is
// not here: apply carries it out itself (writeLog), to know what was written.
func (d *Driver) effect(e engine.Effect) {
	switch e := e.(type) {
	case engine.Probe:
		d.work(func() engine.Input { return engine.ProbeDone{Cached: d.cfg.Priv.Probe(d.root)} })
	case engine.Authenticate:
		pw := string(e.Password) // only in this closure, for privilege
		d.work(func() engine.Input {
			detail, err := d.cfg.Priv.Authenticate(d.root, pw)
			return engine.AuthDone{OK: err == nil, Detail: detail, Err: err,
				NoTicket: errors.Is(err, privilege.ErrNoTicket)}
		})
	case engine.KeepAlive:
		d.setKeepAlive(e.On)
	case engine.ReadFaillock:
		d.work(func() engine.Input {
			return d.cfg.Faillock()
		})
	case engine.Discover:
		ctx, cancel := context.WithCancelCause(d.root)
		d.cancels[e.Seq] = cancel
		d.work(func() engine.Input { return engine.Discovered{Seq: e.Seq, Discovery: e.Run(ctx, e.Env)} })
	case engine.StartStep:
		d.startStep(e)
	case engine.Stop:
		if c := d.children[e.Seq]; c != nil {
			d.stopChild(c) // SIGINT, SIGKILL after KillGrace; its pump reaps it
		}
		d.release(e.Seq, nil) // plain context.Canceled: an abort
	case engine.RunHook:
		err := e.Fn()
		if err != nil {
			d.report(fmt.Errorf("driver: hook of step %q: %w", e.Step, err))
		}
		d.local = append(d.local, msg{in: engine.HookDone{Seq: e.Seq, Err: err}})
	case engine.WriteLastRun:
		if err := d.cfg.Log.WriteLastRun(e.Record); err != nil {
			d.report(fmt.Errorf("driver: writing the last run: %w", err))
		}
	case engine.RefreshUpdates:
		if f := d.cfg.RefreshUpdates; f != nil {
			d.goRun(func() {
				if err := f(d.root); err != nil {
					d.refreshFailed(err)
				}
			})
		}
	case engine.StartPrefetch:
		if d.cfg.Prefetch == nil || d.prefetch[e.Name] != nil {
			return
		}
		if p := d.cfg.Prefetch(e.Name); p != nil {
			d.prefetch[e.Name] = p
		}
	case engine.CancelPrefetch:
		d.cancelPrefetch(e.Name)
	case engine.AnswerAskpass:
		a := d.asks[e.Req]
		if a == nil {
			return // hung up already; the Session asks only for an open one
		}
		pw := string(e.Password)
		d.work(func() engine.Input {
			detail, err := d.answerUser(a, pw)
			return engine.AskpassAnswered{Req: e.Req, OK: err == nil, Detail: detail}
		})
	case engine.HangUpAskpass:
		if a := d.asks[e.Req]; a != nil {
			a.cancel()
			delete(d.asks, e.Req)
		}
	}
}

// writeLog carries out a WriteLog and reports whether the entry is written.
func (d *Driver) writeLog(e engine.WriteLog) bool {
	if err := d.cfg.Log.Append(e.Category, e.Kind, e.Label, e.Body); err != nil {
		d.report(fmt.Errorf("driver: writing the %s log: %w", e.Category, err))
		return false
	}
	return true
}

// refreshFailed records a RefreshUpdates that failed (no user manager or
// unit and no installed check, as on a machine running the TUI over ssh;
// a hung systemctl): one WARN line in the run's topgrade log, never a
// Report, which the TUI prints on the terminal after it quits. The refresh
// is best effort and the run is over. Only a failed log write is reported.
// It runs on a worker: a Store is safe for that (Append opens its file
// each time, O_APPEND).
func (d *Driver) refreshFailed(err error) {
	label := "Widget update count not refreshed: " + sanitize.OneLine(err.Error())
	if lerr := d.cfg.Log.Append(logstore.Topgrade, "WARN", label, ""); lerr != nil {
		d.report(fmt.Errorf("driver: writing the %s log: %w (the update count refresh failed: %v)", logstore.Topgrade, lerr, err))
	}
}

// cancelPrefetch drops a running prefetch and reaps it.
func (d *Driver) cancelPrefetch(name string) {
	p := d.prefetch[name]
	if p == nil {
		return
	}
	delete(d.prefetch, name)
	p.Cancel()
	d.goRun(func() { <-p.Done() })
}

// setKeepAlive turns the sudo ticket refresh on or off.
func (d *Driver) setKeepAlive(on bool) {
	switch {
	case on && d.keepAlive == nil:
		off := make(chan struct{})
		d.keepAlive = off
		d.goRun(func() { d.refresh(off) })
	case !on && d.keepAlive != nil:
		close(d.keepAlive)
		d.keepAlive = nil
	}
}

// refresh extends the ticket every KeepAliveEvery until off closes.
func (d *Driver) refresh(off <-chan struct{}) {
	t := time.NewTicker(d.cfg.KeepAliveEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			d.cfg.Priv.Refresh(d.root)
		case <-off:
			return
		case <-d.stop:
			return
		}
	}
}

// startStep starts a step: in-process on a worker, a helper step through
// the Helper (Polkit), anything else as a child through Priv's command.
func (d *Driver) startStep(e engine.StartStep) {
	st := e.Step
	if run := st.Run; run != nil {
		ctx, cancel := context.WithCancelCause(d.root)
		d.cancels[e.Seq] = cancel
		if p := d.prefetch[st.Prefetch]; st.Prefetch != "" && p != nil {
			run = func(ctx context.Context, _ steps.Env) steps.Result { return p.Run(ctx) }
		}
		d.goRun(func() {
			res, failed := runStep(run, ctx, e.Env)
			if failed != nil {
				d.report(failed)
			}
			d.post(msg{in: engine.StepResult{Seq: e.Seq, Result: res}})
		})
		return
	}
	if d.cfg.Polkit && !e.Env.DryRun && st.Verb != "" {
		d.startHelper(e)
		return
	}
	if d.cfg.Polkit && e.Env.DryRun && st.Verb != "" {
		// A dry run previews the polkit route (the pkexec line, a combined
		// call's parts) and starts nothing: no pkexec, no agent, no dialog.
		d.work(func() engine.Input {
			helperclient.DryRun(e.Seq, st, func(in engine.Input) { d.post(msg{in: in}) })
			return nil
		})
		return
	}
	ch := make(chan proc.Msg, proc.BatchMax)
	opts := proc.Options{DryRun: e.Env.DryRun, WaitDelay: d.cfg.WaitDelay, KillGrace: d.cfg.KillGrace, Detach: d.cfg.Detach}
	if d.cfg.Detach && d.cfg.Pty {
		opts.Pty = true
		if d.cfg.TermCols != nil {
			opts.Cols = d.cfg.TermCols()
		}
	}
	spec := d.cfg.Priv.Command(st.Argv, st.Asks())
	spec.ExtraEnv = st.Env // the step's own variables (topgrade's colour), for its child only
	d.guardPath(e, spec)   // the kept password's PATH guard (pathguard.go)
	c := proc.New(opts).Start(spec, ch, nil)
	if c != nil {
		d.children[e.Seq] = c
		d.childPriv[e.Seq] = st.Asks()
		d.lastChild = e.Seq
		if d.cfg.Detach {
			d.detachedMu.Lock()
			d.detached = append(d.detached, c)
			d.detachedMu.Unlock()
		}
	}
	d.goRun(func() { d.pump(e.Seq, ch) })
}

// stopChild stops c (proc.Child.Stop); a detached one is kept so teardown
// waits for its whole group (Deps.Detach).
func (d *Driver) stopChild(c *proc.Child) {
	c.Stop()
	if d.cfg.Detach && !slices.Contains(d.stopped, c) {
		d.stopped = append(d.stopped, c)
	}
}

// runStep runs an in-process step, turning a panic into a failed result.
func runStep(run func(context.Context, steps.Env) steps.Result, ctx context.Context, env steps.Env) (res steps.Result, failed error) {
	defer func() {
		if r := recover(); r != nil {
			failed = fmt.Errorf("driver: an in-process step panicked: %v", r)
			res = steps.Result{Err: failed}
		}
	}()
	return run(ctx, env), nil
}

// pump streams a child's output to the loop, sanitized, up to its done
// (StepExited). It always runs to the done, after a Stop too: that is how
// the child is known to be reaped.
func (d *Driver) pump(seq uint64, ch <-chan proc.Msg) {
	for {
		b, _ := proc.Next(ch, nil, proc.BatchMax) // no quit channel: Next waits for the done
		if len(b.Lines) > 0 {
			d.post(msg{in: engine.StepOutput{Seq: seq, Lines: clean(b.Lines), Replace: b.Replace}})
		}
		if b.Done {
			d.post(msg{in: engine.StepExited{Seq: seq, Err: b.Err}})
			return
		}
	}
}

// clean sanitizes raw output lines, keeping SGR colour (StepOutput's
// contract).
func clean(raw []string) []string {
	lines := make([]string, len(raw))
	for i, l := range raw {
		lines[i] = sanitize.String(l, sanitize.KeepSGR)
	}
	return lines
}
