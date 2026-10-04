package main

// sessionTarget: the transcript Target that drives internal/engine's
// Session instead of the TUI's model, playing the Session's Effects against
// the same shims, log store and fake AUR as tuiTarget, so the same goldens
// hold both front ends to the same behaviour (TESTING.md → Transcripts).

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// sessionSkips are the scenarios the Session can't run yet, each with the
// reason; TestTranscriptSession skips them by name, never silently.
var sessionSkips = map[string]string{
	"fullcleanup_orphans_cascade_acceptall.dryrun": needsInsert,
	"fullcleanup_orphans_cascade_acceptall.shim":   needsInsert,
	"fullcleanup_decline_optionals.shim":           needsInsert,
	"fullcleanup_orphans_from_update.shim":         needsInsert,
	"fullcleanup_yay_fails.shim":                   needsInsert,
	"fullcleanup_audit_takeover_decline.shim":      needsGate,
	"fullcleanup_audit_takeover_accept.shim":       needsGate,

	"fullcleanup_uncached_password_ok.shim":  needsPassword,
	"fullcleanup_three_wrong_passwords.shim": needsPassword,
	"repair_auth_giveup.shim":                needsPassword,
	"repair_cached_found.shim":               needsRoot,
	"repair_false_positive.shim":             needsRoot,
	"fullcleanup_abort_topgrade.shim":        needsAbort,
	"fullcleanup_abort_sigkill.shim":         needsAbort,
	"services_select2.shim":                  needsSelection,
	"services_none_selected.shim":            needsSelection,
	"auraudit_first_baseline.shim":           needsReport,
	"auraudit_takeover.shim":                 needsReport,
}

const (
	needsInsert    = "needs E2: a result that inserts a step (InsertNext)"
	needsGate      = "needs E2: a result that gates the next step (ConfirmNext)"
	needsPassword  = "needs E3 auth: the password prompt (SubmitPassword)"
	needsRoot      = "needs E3 auth: Repair's NeedsRoot recheck"
	needsAbort     = "needs E5: Abort"
	needsSelection = "needs E5: the services selection (Select)"
	needsReport    = "needs E5: the audit report (OutputReport)"
)

// TestTranscriptSession replays every scenario through the engine's
// Session, against the goldens TestTranscript records through the TUI.
func TestTranscriptSession(t *testing.T) {
	for name := range sessionSkips {
		if !slices.ContainsFunc(transcriptScenarios, func(sc transcriptScenario) bool { return sc.name == name }) {
			t.Errorf("sessionSkips names %q, which is no scenario", name)
		}
	}
	replay(t, func(t *testing.T, shimLog string) tt.Target { return newSessionTarget(t, shimLog) }, sessionSkips)
}

// sessionTarget runs an action on an engine.Session and carries out its
// Effects the way a Driver would, one at a time on the test goroutine.
//
// Effects that answer at once (WriteLog, WriteLastRun, RunHook, the
// prefetch) run as the Output is applied; those whose answer is an Input —
// the probe, a discovery, a step — become work, run in FIFO order by Next,
// each answer fed back through Handle. That is tuiTarget's cmd queue with
// Inputs for msgs, so the exec lines land where they do there: a work
// item's after it ran, a step child's with its first output or exit.
//
// Each Output's events are buffered in the order the TUI's transcripts
// record one Update's (see TESTING.md → Format): plan, output, finished
// and inserted steps, changed plan flags, log entries and the snapshot (read
// from disk, by effects), started steps, then a notice. A prompt is handed
// out once no work is left.
type sessionTarget struct {
	t  *testing.T
	s  *engine.Session
	fx *effects

	work    []func() []engine.Input
	pending []tt.Event
	err     error
	evs     []engine.Event // every engine event, for the contract check

	prompt   tt.Event // the open prompt as the recorder sees it, or nil
	promptID engine.PromptID
	fin      *engine.Finished

	fetches  map[string]*auditFetch
	children map[uint64]*sessionChild
	release  map[uint64]context.CancelFunc // in-process steps' ctx, by Seq
}

type sessionChild struct {
	c    *proc.Child
	ch   chan proc.Msg
	quit chan struct{}
}

func newSessionTarget(t *testing.T, shimLog string) *sessionTarget {
	t.Helper()
	// As in tuiTarget: the real binary always wires askpass, so sudo steps
	// run `sudo -A`; the wrapper is only named, never run.
	oldWrapper := askpassWrapper
	askpassWrapper = "/run/user/test/archmaid-askpass/wrapper"
	t.Cleanup(func() { askpassWrapper = oldWrapper })

	tg := &sessionTarget{
		t:        t,
		s:        engine.New(engine.Options{DryRun: dryRun, Env: actionEnv()}),
		fx:       newEffects(shimLog),
		fetches:  map[string]*auditFetch{},
		children: map[uint64]*sessionChild{},
		release:  map[uint64]context.CancelFunc{},
	}
	t.Cleanup(func() {
		for _, f := range tg.fetches {
			f.Cancel()
			<-f.Done()
		}
		for _, c := range tg.children {
			close(c.quit)
			c.c.Stop()
		}
		for _, cancel := range tg.release {
			cancel()
		}
		if !t.Failed() && !t.Skipped() {
			enginetest.CheckContract(t, tg.evs)
		}
	})
	return tg
}

func (tg *sessionTarget) push(evs ...tt.Event) { tg.pending = append(tg.pending, evs...) }

func (tg *sessionTarget) fail(format string, a ...any) {
	if tg.err == nil {
		tg.err = fmt.Errorf(format, a...)
	}
}

// Start starts the menu action whose label contains sub.
func (tg *sessionTarget) Start(sub string) (string, error) {
	i := slices.IndexFunc(actions.Menu, func(a actions.Action) bool { return strings.Contains(a.Label, sub) })
	if i < 0 {
		return "", fmt.Errorf("no menu item %q", sub)
	}
	a := actions.Menu[i]
	o := tg.s.Start(a.ID)
	if len(o.Events) == 0 && len(o.Effects) == 0 {
		return "", fmt.Errorf("the session ignored Start(%q)", a.ID)
	}
	tg.apply(o)
	return a.Label, nil
}

// Next hands out the buffered events, running work until there are some;
// with no work left it reports the open prompt, or nil once the action is
// over.
func (tg *sessionTarget) Next() (tt.Event, error) {
	for len(tg.pending) == 0 {
		if tg.err != nil {
			return nil, tg.err
		}
		if len(tg.work) == 0 {
			switch {
			case tg.prompt != nil:
				return tg.prompt, nil
			case tg.fin != nil:
				return nil, nil
			}
			return nil, fmt.Errorf("stalled: no work, no prompt and no Finished")
		}
		w := tg.work[0]
		tg.work = tg.work[1:]
		ins := w()
		tg.push(tg.fx.execs()...)
		for _, in := range ins {
			tg.apply(tg.s.Handle(in))
			if r, ok := in.(engine.StepResult); ok {
				// The result was taken: release the step's ctx.
				if cancel := tg.release[r.Seq]; cancel != nil {
					cancel()
					delete(tg.release, r.Seq)
				}
			}
		}
	}
	ev := tg.pending[0]
	tg.pending = tg.pending[1:]
	return ev, nil
}

// Reply answers the open prompt.
func (tg *sessionTarget) Reply(a tt.Answer) error {
	id := tg.promptID
	switch a.Kind {
	case "confirm":
		if _, ok := tg.prompt.(tt.NeedsConfirm); !ok {
			return fmt.Errorf("confirm reply with %T open", tg.prompt)
		}
		tg.prompt, tg.promptID = nil, 0
		tg.apply(tg.s.Confirm(id, a.Reply == "yes"))
		return nil
	}
	return fmt.Errorf("the session target can't reply %q yet", a.Kind)
}

// Outcome is the Finished event's.
func (tg *sessionTarget) Outcome() (tt.Outcome, error) {
	if tg.fin == nil {
		return tt.Outcome{}, fmt.Errorf("the action never finished")
	}
	return tt.Outcome{Kind: tg.fin.Outcome.String(), Errs: tg.fin.Errs, Skipped: tg.fin.Skipped}, nil
}

// apply carries out an Output: its Effects in order, then its Events,
// buffered in the transcript's order.
func (tg *sessionTarget) apply(o engine.Output) {
	tg.evs = append(tg.evs, o.Events...)
	launched := false
	var hooks []engine.Input
	for _, e := range o.Effects {
		switch e := e.(type) {
		case engine.StartStep:
			if tg.startStep(e) {
				launched = true
			}
		case engine.RunHook:
			hooks = append(hooks, engine.HookDone{Seq: e.Seq, Err: e.Fn()})
		default:
			tg.effect(e)
		}
	}
	// A child started here logs itself asynchronously; its exec line is
	// taken with its first output or its exit, never here.
	if !launched {
		tg.push(tg.fx.execs()...)
	}

	var plan, out, done, changed, started, tail []tt.Event
	var engineLogs []tt.Event
	for _, ev := range o.Events {
		switch ev := ev.(type) {
		case engine.QueueStarted:
			ps := make([]tt.Step, len(ev.Steps))
			for i, s := range ev.Steps {
				ps[i] = transcriptStep(s)
			}
			plan = append(plan, tt.QueueStarted{Steps: ps})
		case engine.OutputChunk:
			for _, l := range ev.Lines {
				out = append(out, tt.OutputLine{Index: ev.Index, Text: l})
			}
		case engine.StepDone:
			done = append(done, tt.StepDone{Index: ev.Index, Status: transcriptStatus(ev.Status)})
		case engine.StepInserted:
			done = append(done, tt.StepInserted{Index: ev.Index, Step: transcriptStep(ev.Step)})
		case engine.PlanChanged:
			changed = append(changed, tt.PlanChanged{Index: ev.Index, Step: transcriptStep(ev.Step)})
		case engine.StepStarted:
			started = append(started, tt.StepStarted{Index: ev.Index})
		case engine.LogWritten:
			engineLogs = append(engineLogs, tt.LogWritten{Category: string(ev.Category), Header: ev.Header})
		case engine.Notice:
			tail = append(tail, tt.Notice{Text: ev.Text})
		case engine.NeedsConfirm:
			tg.open(ev.ID, tt.NeedsConfirm{Text: ev.Msg})
		case engine.NeedsPassword:
			tg.open(ev.ID, tt.NeedsPassword{})
		case engine.Finished:
			tg.fin = &ev
		case engine.PhaseChanged, engine.StatusUpdated:
		default:
			tg.fail("event %T (%+v): teach the session target", ev, ev)
		}
	}
	tg.push(plan...)
	tg.push(out...)
	tg.push(done...)
	tg.push(changed...)
	logs := tg.fx.logs()
	if !contains(logs, engineLogs) {
		tg.fail("the session reported log entries %v, the log files got %v", engineLogs, logs)
	}
	tg.push(logs...)
	tg.push(tg.fx.snapshot()...)
	tg.push(started...)
	tg.push(tail...)
	for _, h := range hooks {
		tg.apply(tg.s.Handle(h))
	}
}

// contains reports whether sub is a subsequence of evs (the log files may
// also hold entries the Session didn't write itself, such as the audit
// commit's ACCEPT).
func contains(evs, sub []tt.Event) bool {
	i := 0
	for _, ev := range evs {
		if i < len(sub) && ev == sub[i] {
			i++
		}
	}
	return i == len(sub)
}

func (tg *sessionTarget) open(id engine.PromptID, ev tt.Event) {
	if tg.prompt != nil {
		tg.fail("prompt %v opened while %v is open", ev, tg.prompt)
	}
	tg.prompt, tg.promptID = ev, id
}

// effect carries out one Effect other than StartStep and RunHook.
func (tg *sessionTarget) effect(e engine.Effect) {
	switch e := e.(type) {
	case engine.Probe:
		priv := privileged()
		tg.work = append(tg.work, func() []engine.Input {
			return []engine.Input{engine.ProbeDone{Cached: priv.Probe(context.Background())}}
		})
	case engine.Discover:
		tg.work = append(tg.work, func() []engine.Input {
			return []engine.Input{engine.Discovered{Seq: e.Seq, Discovery: e.Run(context.Background(), e.Env)}}
		})
	case engine.WriteLog:
		_ = logs().Append(e.Category, e.Kind, e.Label, e.Body)
	case engine.WriteLastRun:
		_ = logs().WriteLastRun(e.Record)
	case engine.KeepAlive: // the refresh is a tick; the transcripts drop ticks
	case engine.StartPrefetch:
		if e.Name != steps.PrefetchAURAudit {
			tg.fail("unknown prefetch %q", e.Name)
			return
		}
		// Let it finish here, so its pacman -Qqm lands with the action's
		// own queries, as tuiTarget does.
		f := startAuditFetch()
		tg.fetches[e.Name] = f
		select {
		case <-f.Done():
		case <-time.After(5 * time.Second):
			tg.fail("the prefetched AUR audit never finished")
		}
	case engine.CancelPrefetch:
		if f := tg.fetches[e.Name]; f != nil {
			f.Cancel()
			delete(tg.fetches, e.Name)
		}
	default:
		tg.fail("effect %T: teach the session target", e)
	}
}

// startStep starts a step: an in-process one becomes work (awaiting its
// prefetch, if one runs), a child is started now and pumped by work. It
// reports whether it started a child.
func (tg *sessionTarget) startStep(e engine.StartStep) bool {
	if run := e.Step.Run; run != nil {
		ctx, cancel := context.WithCancel(context.Background())
		tg.release[e.Seq] = cancel
		if f := tg.fetches[e.Step.Prefetch]; f != nil {
			run = func(ctx context.Context, _ steps.Env) steps.Result { return f.run(ctx) }
		}
		tg.work = append(tg.work, func() []engine.Input {
			return []engine.Input{engine.StepResult{Seq: e.Seq, Result: run(ctx, e.Env)}}
		})
		return false
	}
	c := &sessionChild{ch: make(chan proc.Msg, proc.BatchMax), quit: make(chan struct{})}
	c.c = proc.New(procOptions()).Start(stepSpec(e.Step), c.ch, c.quit)
	tg.children[e.Seq] = c
	tg.work = append(tg.work, tg.pump(e.Seq, c))
	return true
}

// pump is one proc.Next on a child's stream, as StepOutput (sanitized, as a
// Driver must) and, at its end, StepExited; it queues itself again until
// the child is done.
func (tg *sessionTarget) pump(seq uint64, c *sessionChild) func() []engine.Input {
	return func() []engine.Input {
		b, ok := proc.Next(c.ch, c.quit, proc.BatchMax)
		if !ok {
			return nil
		}
		var ins []engine.Input
		if len(b.Lines) > 0 {
			lines := make([]string, len(b.Lines))
			for i, l := range b.Lines {
				lines[i] = sanitize.String(l, sanitize.KeepSGR)
			}
			ins = append(ins, engine.StepOutput{Seq: seq, Lines: lines})
		}
		if b.Done {
			delete(tg.children, seq)
			return append(ins, engine.StepExited{Seq: seq, Err: b.Err})
		}
		tg.work = append(tg.work, tg.pump(seq, c))
		return ins
	}
}

// transcriptStep is what a transcript records of a planned step.
func transcriptStep(p engine.PlanStep) tt.Step {
	s := tt.Step{Title: p.Title, InProc: p.InProc, Sudo: p.NeedsPriv, Confirm: p.Confirm,
		Optional: p.Optional, AlwaysLog: p.AlwaysLog, Category: string(p.Category)}
	if !p.InProc {
		s.Argv = slices.Clone(p.Argv)
	}
	return s
}

// transcriptStatus is how a transcript draws a settled step: every "didn't
// run" is a skip, as in the TUI's checklist.
func transcriptStatus(s steps.Status) tt.Status {
	switch s {
	case steps.OK:
		return tt.OK
	case steps.Failed:
		return tt.Failed
	}
	return tt.Skipped
}
