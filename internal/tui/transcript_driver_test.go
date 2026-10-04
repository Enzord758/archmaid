package tui

// driverTarget: the transcript Target that runs a Session through the
// TUI's syncDriver in pull mode, as the TUI will (E7), reading only what an
// engine.Driver gives a front end: the Events, batch by batch. The same
// goldens hold it to the TUI's behaviour, so the driver's Effects are
// carried out as sessionTarget's are.

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
	"github.com/Enzord758/archmaid/internal/steps"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// TestTranscriptDriver replays every scenario through the syncDriver.
func TestTranscriptDriver(t *testing.T) {
	for _, sc := range transcriptScenarios {
		t.Run(sc.name, func(t *testing.T) {
			if *updateTranscripts {
				t.Skip("-update-transcripts records through the TUI (TestTranscript) only")
			}
			got := sc.run(t, func(t *testing.T, shimLog string) tt.Target {
				return newDriverTarget(t, shimLog)
			}).Transcript()
			checkGolden(t, sc.name, got)
		})
	}
}

type driverTarget struct {
	d  *syncDriver
	fx *effects

	pending  []tt.Event
	err      error
	flags    []tt.Step
	contract *enginetest.Checker

	prompt   tt.Event
	promptID engine.PromptID
	report   engine.PromptID
	fin      *engine.Finished
}

func newDriverTarget(t *testing.T, shimLog string) *driverTarget {
	t.Helper()
	oldWrapper := askpassWrapper
	askpassWrapper = "/run/user/test/archmaid-askpass/wrapper"
	t.Cleanup(func() { askpassWrapper = oldWrapper })
	opts := engine.Options{DryRun: dryRun, Env: actionEnv()}
	if err := opts.Validate(); err != nil {
		t.Fatal(err)
	}
	tg := &driverTarget{fx: newEffects(shimLog), contract: enginetest.NewChecker(t)}
	tg.d = newSyncDriver(engine.New(opts), syncDeps{Priv: privileged(), Log: logs()})
	tg.d.afterWork = func() { tg.push(tg.fx.execs()...) }
	t.Cleanup(func() {
		tg.d.Close()
		if !t.Failed() && !t.Skipped() {
			tg.contract.Done()
		}
	})
	return tg
}

func (tg *driverTarget) push(evs ...tt.Event) { tg.pending = append(tg.pending, evs...) }

func (tg *driverTarget) fail(format string, a ...any) {
	if tg.err == nil {
		tg.err = fmt.Errorf(format, a...)
	}
}

// send hands the driver c and takes what it published at once.
func (tg *driverTarget) send(c engine.Command) {
	n := tg.d.launches
	if !tg.d.Send(c) {
		tg.fail("the driver refused %T", c)
	}
	tg.settle(n)
	tg.take(tg.d.published())
}

// settle waits for a prefetch the last step started (its pacman -Qqm lands
// with the action's own queries) and takes the exec lines, unless a child
// was launched: its line comes with its first output or its exit.
func (tg *driverTarget) settle(launches int) {
	for _, f := range tg.d.fetches {
		select {
		case <-f.Done():
		case <-time.After(5 * time.Second):
			tg.fail("the prefetched AUR audit never finished")
		}
	}
	if tg.d.launches == launches {
		tg.push(tg.fx.execs()...)
	}
}

func (tg *driverTarget) Start(sub string) (string, error) {
	i := slices.IndexFunc(actions.Menu, func(a actions.Action) bool { return strings.Contains(a.Label, sub) })
	if i < 0 {
		return "", fmt.Errorf("no menu item %q", sub)
	}
	tg.send(engine.Start{Action: actions.Menu[i].ID})
	return actions.Menu[i].Label, nil
}

func (tg *driverTarget) Next() (tt.Event, error) {
	for len(tg.pending) == 0 {
		if tg.err != nil {
			return nil, tg.err
		}
		n := tg.d.launches
		evs := tg.d.Pull()
		if len(evs) == 0 {
			switch {
			case tg.prompt != nil:
				return tg.prompt, nil
			case tg.fin != nil:
				return nil, nil
			case tg.report != 0:
				id := tg.report
				tg.report = 0
				tg.send(engine.Confirm{Prompt: id, Yes: false})
				continue
			}
			return nil, fmt.Errorf("stalled: no work, no prompt and no Finished")
		}
		tg.settle(n)
		tg.take(evs)
	}
	ev := tg.pending[0]
	tg.pending = tg.pending[1:]
	return ev, nil
}

func (tg *driverTarget) Reply(a tt.Answer) error {
	id := tg.promptID
	switch a.Kind {
	case "confirm":
		tg.prompt, tg.promptID = nil, 0
		tg.send(engine.Confirm{Prompt: id, Yes: a.Reply == "yes"})
	case "password":
		pw := engine.Secret("not-the-password")
		if a.Reply == "correct" {
			pw = shimPassword
		}
		tg.prompt, tg.promptID = nil, 0
		tg.send(engine.SubmitPassword{Prompt: id, Password: pw})
	case "select":
		tg.prompt, tg.promptID = nil, 0
		tg.send(engine.Select{Prompt: id, Picked: strings.Fields(a.Reply)})
	case "abort":
		if len(tg.d.child) > 0 {
			if !tg.fx.awaitExec(shimWait) {
				return fmt.Errorf("the running step's child never reached its shim")
			}
			tg.push(tg.fx.execs()...)
		}
		tg.send(engine.Abort{})
	default:
		return fmt.Errorf("the driver target can't reply %q", a.Kind)
	}
	return nil
}

func (tg *driverTarget) Outcome() (tt.Outcome, error) {
	if tg.fin == nil {
		return tt.Outcome{}, fmt.Errorf("the action never finished")
	}
	return tt.Outcome{Kind: tg.fin.Outcome.String(), Errs: tg.fin.Errs, Skipped: tg.fin.Skipped}, nil
}

// take buffers one batch of Events in the transcript's order (as
// sessionTarget.apply does for an Output).
func (tg *driverTarget) take(evs []engine.Event) {
	if len(evs) == 0 {
		return
	}
	if !tg.contract.Check(evs) {
		tg.fail("a batch breaks the engine's contract: %s", eventKinds(evs))
	}
	var plan, out, done, changed, started, tail, engineLogs []tt.Event
	for _, ev := range evs {
		switch ev := ev.(type) {
		case engine.QueueStarted:
			ps := make([]tt.Step, len(ev.Steps))
			for i, s := range ev.Steps {
				ps[i] = transcriptStep(s)
			}
			plan = append(plan, tt.QueueStarted{Steps: ps})
			tg.flags = slices.Clone(ps)
		case engine.OutputChunk:
			for _, l := range ev.Lines {
				out = append(out, tt.OutputLine{Index: ev.Index, Text: l})
			}
		case engine.StepDone:
			done = append(done, tt.StepDone{Index: ev.Index, Status: transcriptStatus(ev.Status)})
		case engine.Aborted:
			done = append(done, tt.Aborted{Index: ev.Index})
		case engine.StepInserted:
			done = append(done, tt.StepInserted{Index: ev.Index, Step: transcriptStep(ev.Step)})
			tg.flags = slices.Insert(tg.flags, ev.Index, transcriptStep(ev.Step))
		case engine.PlanChanged:
			if s := transcriptStep(ev.Step); !s.SameFlags(tg.flags[ev.Index]) {
				tg.flags[ev.Index] = s
				changed = append(changed, tt.PlanChanged{Index: ev.Index, Step: s})
			}
		case engine.StepStarted:
			started = append(started, tt.StepStarted{Index: ev.Index})
		case engine.LogWritten:
			engineLogs = append(engineLogs, tt.LogWritten{Category: string(ev.Category), Header: ev.Header})
		case engine.Notice:
			tail = append(tail, tt.Notice{Text: ev.Text})
		case engine.OutputReport:
			tail = append(tail, tt.OutputReport{Text: ev.Text})
			tg.report = ev.ID
		case engine.NeedsConfirm:
			tg.open(ev.ID, tt.NeedsConfirm{Text: ev.Msg})
		case engine.NeedsPassword:
			tg.open(ev.ID, tt.NeedsPassword{})
		case engine.NeedsSelection:
			tg.open(ev.ID, tt.NeedsSelection{Options: slices.Clone(ev.Items)})
		case engine.PromptClosed:
			if ev.ID == tg.promptID {
				tg.prompt, tg.promptID = nil, 0
			}
		case engine.Finished:
			tg.fin = &ev
		}
	}
	tg.push(plan...)
	tg.push(out...)
	tg.push(done...)
	tg.push(changed...)
	logs := tg.fx.logs()
	if !contains(logs, engineLogs) {
		tg.fail("the driver reported log entries %v, the log files got %v", engineLogs, logs)
	}
	tg.push(logs...)
	tg.push(tg.fx.snapshot()...)
	tg.push(started...)
	tg.push(tail...)
}

func (tg *driverTarget) open(id engine.PromptID, ev tt.Event) {
	if tg.prompt != nil {
		tg.fail("prompt %v opened while %v is open", ev, tg.prompt)
	}
	tg.prompt, tg.promptID = ev, id
}

// Close aborts the running action, closes Events and refuses further
// Commands; a second Close is a no-op.
func TestSyncDriverClose(t *testing.T) {
	transcriptEnv(t, false)
	installShims(t, nil)
	probe := actions.Action{ID: "closeprobe", Label: "Close probe", Steps: func(actions.Env) []steps.Step {
		return []steps.Step{{ID: "closeprobe", Title: "Printing…", LogLabel: "Printed",
			Run: func(context.Context, steps.Env) steps.Result { return steps.Result{Output: "a"} }}}
	}}
	old := actions.Menu
	actions.Menu = append(slices.Clone(old), probe)
	t.Cleanup(func() { actions.Menu = old })

	d := newSyncDriver(engine.New(engine.Options{Env: actionEnv()}), syncDeps{Priv: privileged(), Log: logs()})
	if !d.Send(engine.Start{Action: probe.ID}) {
		t.Fatal("Send refused Start")
	}
	d.Close()
	if _, open := <-d.Events(); open {
		t.Error("Events is still open after Close")
	}
	if d.Send(engine.Abort{}) {
		t.Error("Send after Close was accepted")
	}
	if evs := d.Pull(); len(evs) != 0 {
		t.Errorf("Pull after Close = %v", evs)
	}
	d.Close()
}
