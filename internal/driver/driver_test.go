package driver

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A queue runs end to end: children and an in-process step, output, a
// failure, the log entries and last-run.json; the in-process step's ctx is
// released with steps.ErrResultTaken once its result was taken.
func TestRunsAQueue(t *testing.T) {
	var stepCtx atomic.Pointer[context.Context]
	inproc := steps.Step{ID: "inproc", Title: "In process…", LogLabel: "In process", AlwaysLog: true,
		Run: func(ctx context.Context, _ steps.Env) steps.Result {
			stepCtx.Store(&ctx)
			return steps.Result{Output: "from inside"}
		}}
	r := newRig(t, engine.Options{}, Deps{}, action("q",
		shStep("echo", "echo one; echo two"),
		inproc,
		shStep("fail", "echo bad; exit 3"),
	))
	evs := r.run("q", yes)

	fin := find[engine.Finished](t, evs)
	if fin.Outcome != engine.OutcomeCompleted || fin.Errs != 1 {
		t.Fatalf("Finished = %+v, want completed with 1 error\n%s", fin, kinds(evs))
	}
	if got, dropped := lines(evs); !slices.Equal(got, []string{"one", "two", "from inside", "bad"}) || dropped != 0 {
		t.Fatalf("output %q (dropped %d)", got, dropped)
	}
	log, err := r.log.Read(logstore.Cleanup)
	if err != nil || !strings.Contains(log, "OK: echo") || !strings.Contains(log, "OK: In process") || !strings.Contains(log, "FAIL: fail") {
		t.Fatalf("cleanup log = %q (%v)", log, err)
	}
	rec, err := r.log.ReadLastRun()
	if err != nil || rec.Action != "q" || rec.Outcome != logstore.OutcomeFailed {
		t.Fatalf("last run = %+v (%v)", rec, err)
	}
	ctx := *stepCtx.Load()
	if cause := context.Cause(ctx); !errors.Is(cause, steps.ErrResultTaken) {
		t.Fatalf("the in-process step's ctx: cause %v, want steps.ErrResultTaken", cause)
	}
}

// The sudo flow: no cached ticket, so the faillock count and the password
// prompt; a wrong password asks again, the right one runs the queue with
// the keep-alive on, and off again after it.
func TestSudoPasswordFlow(t *testing.T) {
	st := shStep("priv", "sleep 0.2; echo done")
	st.NeedsPriv = true
	r := newRig(t, engine.Options{}, Deps{KeepAliveEvery: 10 * time.Millisecond}, action("p", st))
	r.priv.cached = false
	tries := 0
	evs := r.run("p", func(ev engine.Event) engine.Command {
		if p, ok := ev.(engine.NeedsPassword); ok {
			tries++
			pw := engine.Secret("bad")
			if tries > 1 {
				pw = "good"
			}
			return engine.SubmitPassword{Prompt: p.ID, Password: pw}
		}
		return nil
	})
	if tries != 2 {
		t.Fatalf("asked %d times, want 2\n%s", tries, kinds(evs))
	}
	if fs := find[engine.FaillockState](t, evs); fs.Deny != 3 {
		t.Fatalf("FaillockState = %+v", fs)
	}
	if fin := find[engine.Finished](t, evs); fin.Outcome != engine.OutcomeCompleted || fin.Errs != 0 {
		t.Fatalf("Finished = %+v", fin)
	}
	n := r.priv.refreshes.Load()
	if n == 0 {
		t.Fatal("the keep-alive never refreshed the ticket while the step ran")
	}
	time.Sleep(50 * time.Millisecond)
	if after := r.priv.refreshes.Load(); after > n+1 {
		t.Fatalf("the keep-alive went on after the queue: %d refreshes, then %d", n, after)
	}
}

// A discovery runs on a worker; its Notice ends the action.
func TestDiscoveryNotice(t *testing.T) {
	a := actions.Action{ID: "d", Label: "d", Busy: "Looking…",
		Discover: func(context.Context, actions.Env) actions.Discovery { return actions.Notice{Text: "Nothing found."} }}
	r := newRig(t, engine.Options{}, Deps{}, a)
	evs := r.run("d", nil)
	if n := find[engine.Notice](t, evs); n.Text != "Nothing found." {
		t.Fatalf("Notice = %+v", n)
	}
	if fin := find[engine.Finished](t, evs); fin.Outcome != engine.OutcomeNotice {
		t.Fatalf("Finished = %+v", fin)
	}
}

// A dry run starts no child: the step reports what it would run.
func TestDryRunStartsNothing(t *testing.T) {
	r := newRig(t, engine.Options{DryRun: true}, Deps{}, action("dry", shStep("x", "exit 1")))
	evs := r.run("dry", nil)
	got, _ := lines(evs)
	if len(got) != 1 || !strings.HasPrefix(got[0], proc.DryRunPrefix) {
		t.Fatalf("output %q, want one dry-run line", got)
	}
}

// 50k lines from a child reach a consumer that keeps up, all of them, in
// order, coalesced into far fewer chunks, none dropped.
func TestFloodNothingDroppedWhenKeptUp(t *testing.T) {
	const n = 50000
	r := newRig(t, engine.Options{}, Deps{}, action("flood", shStep("seq", "seq 1 "+strconv.Itoa(n))))
	start := time.Now()
	evs := r.run("flood", nil)
	got, dropped := lines(evs)
	chunks := 0
	for _, ev := range evs {
		if _, ok := ev.(engine.OutputChunk); ok {
			chunks++
		}
	}
	t.Logf("%d lines in %d chunks, %v", len(got), chunks, time.Since(start))
	if dropped != 0 || len(got) != n {
		t.Fatalf("%d lines delivered, %d dropped; want %d, 0", len(got), dropped, n)
	}
	for i, l := range got {
		if l != strconv.Itoa(i+1) {
			t.Fatalf("line %d is %q", i, l)
		}
	}
	if chunks > n/proc.BatchMax {
		t.Fatalf("%d chunks for %d lines: not coalesced", chunks, n)
	}
}

// A consumer that reads nothing until the action is over gets every
// control event, once and in order, and of the output the newest MaxLines
// with the rest counted as Dropped, at the right place.
func TestBackpressureDropsOnlyOutput(t *testing.T) {
	const n, max = 50000, 1000
	r := newRig(t, engine.Options{}, Deps{MaxLines: max, TickEvery: 2 * time.Millisecond},
		action("flood", shStep("seq", "seq 1 "+strconv.Itoa(n)), shStep("after", "echo after")))
	if !r.d.Send(engine.Start{Action: "flood"}) {
		t.Fatal("Send = false")
	}
	// last-run.json is written before Finished is published: once it is
	// there, every event is queued and none was read.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := r.log.ReadLastRun(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var control []string
	var out []string
	ticks, dropped := 0, 0
	next := 1 // the seq value the next delivered line should have, drops counted
	for {
		ev, ok := r.next()
		if !ok {
			t.Fatal("Events closed")
		}
		switch ev := ev.(type) {
		case engine.StatusUpdated:
			ticks++
			continue
		case engine.OutputChunk:
			if ev.ID == "seq" {
				next += ev.Dropped
				for _, l := range ev.Lines {
					if l != strconv.Itoa(next) {
						t.Fatalf("line %q where %d was due", l, next)
					}
					next++
				}
			}
			out = append(out, ev.Lines...)
			dropped += ev.Dropped
			continue
		case engine.StepStarted:
			control = append(control, "started "+ev.ID)
		case engine.StepDone:
			control = append(control, "done "+ev.ID)
		default:
			control = append(control, strings.TrimPrefix(kinds([]engine.Event{ev}), ""))
		}
		if _, ok := ev.(engine.Finished); ok {
			break
		}
	}
	want := []string{"PhaseChanged", "QueueStarted", "started seq", "done seq", "LogWritten",
		"started after", "done after", "LogWritten", "Finished", "PhaseChanged"}
	if strings.Join(control, ",") != strings.Join(want[:len(want)-1], ",") {
		t.Fatalf("control events %q, want %q", control, want[:len(want)-1])
	}
	if next != n+1 || len(out)+dropped != n+1 || dropped == 0 || len(out) > max+1 {
		t.Fatalf("%d lines delivered, %d dropped (next %d); want %d in all, at most %d delivered", len(out), dropped, next, n+1, max+1)
	}
	t.Logf("%d lines delivered, %d dropped, %d ticks kept", len(out), dropped, ticks)
	if ticks == 0 {
		t.Fatal("no StatusUpdated: the ticker never ran")
	}
}

// Abort stops a child with SIGINT; one that ignores SIGINT is SIGKILLed
// after the grace. Either is reaped. Children run in archmaid's own
// session and process group (TestNoSetsid's rule).
func TestStopEscalates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		atLeast,
		under time.Duration
	}{
		{"sigint", "echo $$; exec sleep 30", 0, 250 * time.Millisecond},
		{"sigkill", "trap '' INT; echo $$; exec sleep 30", 300 * time.Millisecond, 3 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, engine.Options{}, Deps{KillGrace: 300 * time.Millisecond}, action("s", shStep("sleep", tc.script)))
			r.d.Send(engine.Start{Action: "s"})
			pid := 0
			for pid == 0 {
				ev, _ := r.next()
				if _, ok := ev.(engine.OutputChunk); ok {
					pid = pidOf(t, ev)
				}
			}
			checkNoSetsid(t, pid)
			at := time.Now()
			r.d.Send(engine.Abort{})
			var evs []engine.Event
			for {
				ev, _ := r.next()
				evs = append(evs, ev)
				if _, ok := ev.(engine.Finished); ok {
					break
				}
			}
			if fin := find[engine.Finished](t, evs); fin.Outcome != engine.OutcomeAborted {
				t.Fatalf("Finished = %+v", fin)
			}
			find[engine.Aborted](t, evs)
			if !gone(pid, 5*time.Second) {
				t.Fatalf("pid %d still there 5 s after Abort", pid)
			}
			if took := time.Since(at); took < tc.atLeast || took > tc.under {
				t.Fatalf("stopped after %v, want %v–%v", took, tc.atLeast, tc.under)
			}
		})
	}
}

// checkNoSetsid: the child shares our session and process group.
func checkNoSetsid(t *testing.T, pid int) {
	t.Helper()
	me, err := proc.Read(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	child, err := proc.Read(pid)
	if err != nil {
		t.Fatal(err)
	}
	if child.Session != me.Session || child.Pgrp != me.Pgrp {
		t.Fatalf("child session %d pgrp %d, ours %d %d: no Setsid/Setpgid for steps", child.Session, child.Pgrp, me.Session, me.Pgrp)
	}
}

// Close mid-step stops and reaps the child, aborts the run (last-run.json
// says so), closes Events, and refuses Sends after; a second Close is a
// no-op.
func TestCloseMidStep(t *testing.T) {
	r := newRig(t, engine.Options{}, Deps{}, action("c", shStep("sleep", "trap '' INT; echo $$; exec sleep 30")))
	r.d.Send(engine.Start{Action: "c"})
	pid := 0
	for pid == 0 {
		ev, _ := r.next()
		if _, ok := ev.(engine.OutputChunk); ok {
			pid = pidOf(t, ev)
		}
	}
	at := time.Now()
	r.d.Close()
	t.Logf("Close took %v", time.Since(at))
	if !gone(pid, 0) {
		t.Fatalf("pid %d outlived Close", pid)
	}
	for range r.d.Events() { // drained, then closed
	}
	if r.d.Send(engine.Abort{}) {
		t.Fatal("Send after Close = true")
	}
	r.d.Close()
	if rec, err := r.log.ReadLastRun(); err != nil || rec.Outcome != logstore.OutcomeAborted {
		t.Fatalf("last run %+v (%v), want aborted", rec, err)
	}
}

// Close mid in-process step cancels its ctx plainly (an abort), and waits
// for it.
func TestCloseMidInProcStep(t *testing.T) {
	started := make(chan struct{})
	var cause atomic.Value
	st := steps.Step{ID: "wait", Title: "Waiting…", LogLabel: "Wait", Run: func(ctx context.Context, _ steps.Env) steps.Result {
		close(started)
		<-ctx.Done()
		cause.Store(context.Cause(ctx))
		return steps.Result{}
	}}
	r := newRig(t, engine.Options{}, Deps{}, action("w", st))
	r.d.Send(engine.Start{Action: "w"})
	<-started
	r.d.Close()
	if c, _ := cause.Load().(error); !errors.Is(c, context.Canceled) || errors.Is(c, steps.ErrResultTaken) {
		t.Fatalf("cause %v, want plain context.Canceled", c)
	}
}

// Send never fails while the Driver is open, however many Commands queue
// up; Close with none of them read is still clean.
func TestSendQueuesWithoutBound(t *testing.T) {
	r := newRig(t, engine.Options{}, Deps{})
	for i := range 10000 {
		if !r.d.Send(engine.Start{Action: "none-" + strconv.Itoa(i)}) {
			t.Fatalf("Send %d = false", i)
		}
	}
	if r.d.Send(nil) {
		t.Fatal("Send(nil) = true")
	}
}

// A Session that panics closes the Driver: the panic is reported, Events
// closes without a Finished, and Send refuses.
func TestSessionPanicClosesDriver(t *testing.T) {
	boom := actions.Action{ID: "boom", Label: "boom", Steps: func(actions.Env) []steps.Step { panic("boom") }}
	r := newRig(t, engine.Options{}, Deps{}, boom)
	r.d.Send(engine.Start{Action: "boom"})
	for range r.d.Events() {
	}
	if r.d.Send(engine.Abort{}) {
		t.Fatal("Send after the panic = true")
	}
	if !strings.Contains(r.reports.text(), "the engine panicked: boom") {
		t.Fatalf("reports %q", r.reports.text())
	}
}

// Panics in an in-process step or a discovery are reported; the step
// fails, the discovery is simply never answered (Abort ends it).
func TestWorkerPanics(t *testing.T) {
	st := steps.Step{ID: "p", Title: "P…", LogLabel: "P", Run: func(context.Context, steps.Env) steps.Result { panic("in step") }}
	disc := actions.Action{ID: "d", Label: "d", Busy: "…",
		Discover: func(context.Context, actions.Env) actions.Discovery { panic("in discovery") }}
	r := newRig(t, engine.Options{}, Deps{}, action("s", st), disc)
	evs := r.run("s", nil)
	if done := find[engine.StepDone](t, evs); done.Status != steps.Failed || !strings.Contains(done.Err, "panicked: in step") {
		t.Fatalf("StepDone = %+v", done)
	}
	r.d.Send(engine.Start{Action: "d"})
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.reports.text(), "in discovery") {
		if time.Now().After(deadline) {
			t.Fatalf("reports %q", r.reports.text())
		}
		time.Sleep(time.Millisecond)
	}
	r.d.Send(engine.Abort{})
	for {
		ev, _ := r.next()
		if _, ok := ev.(engine.Finished); ok {
			break
		}
	}
}

// A failed log write and a failing hook are reported, not lost.
func TestReportsWriteAndHookFailures(t *testing.T) {
	gated := shStep("gated", "true")
	gated.Confirm = "Run it?"
	gated.OnAccept = func() error { return errors.New("hook failed") }
	r := newRig(t, engine.Options{}, Deps{Log: logstore.New(logstore.Options{Dir: "relative"})}, action("h", gated))
	r.run("h", yes)
	for _, want := range []string{"hook failed", "writing the cleanup log", "writing the last run"} {
		if !strings.Contains(r.reports.text(), want) {
			t.Errorf("reports %q, want %q", r.reports.text(), want)
		}
	}
}

// A step with a Prefetch awaits it; a declined gate cancels it. Either way
// it is reaped.
func TestPrefetch(t *testing.T) {
	var made []*fakePrefetch
	deps := Deps{Prefetch: func(name string) Prefetch {
		if name != "p" {
			return nil
		}
		p := newFakePrefetch("prefetched")
		made = append(made, p)
		return p
	}}
	st := steps.Step{ID: "a", Title: "A…", LogLabel: "A", Prefetch: "p",
		Run: func(context.Context, steps.Env) steps.Result { return steps.Result{Output: "not prefetched"} }}
	other := steps.Step{ID: "b", Title: "B…", LogLabel: "B", Prefetch: "unknown",
		Run: func(context.Context, steps.Env) steps.Result { return steps.Result{Output: "own run"} }}
	gatedA := action("gated", st)
	gatedA.Gate = "Go?"
	r := newRig(t, engine.Options{}, deps, action("pre", st, other), gatedA)
	evs := r.run("pre", nil)
	if got, _ := lines(evs); !slices.Equal(got, []string{"prefetched", "own run"}) {
		t.Fatalf("output %q", got)
	}
	r.run("gated", func(ev engine.Event) engine.Command {
		if c, ok := ev.(engine.NeedsConfirm); ok {
			return engine.CancelPrompt{Prompt: c.ID}
		}
		return nil
	})
	if len(made) != 2 || !made[0].cancelled.Load() || !made[1].cancelled.Load() {
		t.Fatalf("prefetches %d, cancelled %v", len(made), made)
	}
}

// Under polkit a helper step goes through the Helper: its start line
// grants (AuthResolved), its output is shown, its notGranted refuses.
func TestHelperSteps(t *testing.T) {
	st := steps.Step{ID: "h", Title: "Helper…", LogLabel: "Helper", Verb: steps.VerbPaccache,
		Argv: []string{"false"}, NeedsPriv: true, AlwaysLog: true}
	for _, tc := range []struct {
		name    string
		lines   []string
		granted bool
		status  steps.Status
	}{
		{"granted", []string{"cleaned"}, true, steps.OK},
		{"refused", nil, false, steps.AuthRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeHelper(tc.lines, tc.granted, !tc.granted, false)
			r := newRig(t, engine.Options{Auth: engine.AuthPolkit}, Deps{Polkit: true, Helper: h}, action("pk", st))
			evs := r.run("pk", nil)
			if w := find[engine.AuthWaiting](t, evs); w.Kind != engine.WaitPolkit {
				t.Fatalf("AuthWaiting = %+v", w)
			}
			if res := find[engine.AuthResolved](t, evs); res.Granted != tc.granted {
				t.Fatalf("AuthResolved = %+v", res)
			}
			if done := find[engine.StepDone](t, evs); done.Status != tc.status {
				t.Fatalf("StepDone = %+v", done)
			}
			if got, _ := lines(evs); !slices.Equal(got, tc.lines) {
				t.Fatalf("output %q", got)
			}
		})
	}
}

// Close mid helper step cancels its ctx and waits for it.
func TestCloseMidHelperStep(t *testing.T) {
	st := steps.Step{ID: "h", Title: "Helper…", LogLabel: "Helper", Verb: steps.VerbPaccache, Argv: []string{"false"}, NeedsPriv: true}
	r := newRig(t, engine.Options{Auth: engine.AuthPolkit}, Deps{Polkit: true, Helper: fakeHelper(nil, true, false, true)}, action("pk", st))
	r.d.Send(engine.Start{Action: "pk"})
	for {
		ev, _ := r.next()
		if _, ok := ev.(engine.AuthResolved); ok {
			break
		}
	}
	r.d.Close()
}

// The defaults New fills in read the system read-only: the status line and
// the current user's faillock tally.
func TestDefaults(t *testing.T) {
	noLeak(t)
	d := New(engine.New(engine.Options{}), Deps{Priv: &fakePriv{}, Log: logstore.New(logstore.Options{Dir: t.TempDir()})})
	defer d.Close()
	if d.cfg.TickEvery != defaultTick || d.cfg.MaxLines != defaultMaxLines || d.cfg.KillGrace != defaultKillGrace {
		t.Fatalf("defaults %+v", d.cfg)
	}
	_ = d.cfg.Status()
	if in := d.cfg.Faillock(); in.Err != nil {
		t.Logf("faillock: %v", in.Err) // this only reads
	}
	if d.cfg.Helper == nil {
		t.Fatal("no default Helper")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("New without Priv did not panic")
		}
	}()
	New(engine.New(engine.Options{}), Deps{})
}
