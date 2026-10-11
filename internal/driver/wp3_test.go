package driver

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/privilege/privtest"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestHangUpCancelsAnAgentPrompt: a polkit agent prompt that is queued but
// not yet taken by the loop is cancelled when the Driver hangs up on it, so
// polkit's dialog is not left waiting for a Driver that has stopped.
func TestHangUpCancelsAnAgentPrompt(t *testing.T) {
	p := newFakePrompt("org.archmaid.helper.full-cleanup", "good")
	msg{agent: p}.hangUp()
	select {
	case <-p.cancelled:
	default:
		t.Fatal("the agent prompt was not cancelled")
	}
}

// TestReadAskpassEndsWhenTheServerCloses: the askpass reader returns when the
// request channel is closed, instead of spinning on it; a nil request is
// skipped and a request is posted to the loop.
func TestReadAskpassEndsWhenTheServerCloses(t *testing.T) {
	reqs := make(chan *privilege.Request)
	d := &Driver{cfg: Deps{Askpass: reqs}, stop: make(chan struct{}), inputs: make(chan msg, 1), quit: make(chan struct{})}
	done := make(chan struct{})
	go func() { d.readAskpass(); close(done) }()
	reqs <- nil
	r := privtest.NewRequest(nil, "[sudo] password:", privilege.Peer{})
	reqs <- r
	if m := <-d.inputs; m.ask != r {
		t.Fatalf("the loop got %+v, want the request", m)
	}
	close(reqs)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("readAskpass did not return after the request channel closed")
	}
}

// TestAnswerForAHungUpRequestIsDropped: the Session's answer to an askpass
// request the Driver already hung up on is ignored: no su is run (there is
// no Priv to run it with: that would panic), nothing is posted.
func TestAnswerForAHungUpRequestIsDropped(t *testing.T) {
	d := &Driver{asks: map[uint64]asker{}, inputs: make(chan msg, 1)}
	d.effect(engine.AnswerAskpass{Req: 7, Password: engine.Secret("good")})
	if len(d.inputs) != 0 || len(d.asks) != 0 {
		t.Errorf("inputs %d, asks %d, want nothing", len(d.inputs), len(d.asks))
	}
}

// panicClock is an engine clock that panics once armed: the Session then
// panics in whatever Handle it is in next.
type panicClock struct{ armed atomic.Bool }

func (c *panicClock) now() time.Time {
	if c.armed.Load() {
		panic("clock armed")
	}
	return time.Now()
}

// drainEvents reads Events to its close, failing after 10 s; it reports
// whether a Finished came.
func drainEvents(t *testing.T, r *rig) (finished bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-r.d.Events():
			if !ok {
				return finished
			}
			if _, f := ev.(engine.Finished); f {
				finished = true
			}
		case <-deadline:
			t.Fatal("Events did not close after the Session panicked")
		}
	}
}

// panicked checks the Driver closed after a Session panic: reported, Events
// closed without a Finished, Send refused.
func panicked(t *testing.T, r *rig) {
	t.Helper()
	if drainEvents(t, r) {
		t.Error("Finished after the Session panicked")
	}
	if r.d.Send(engine.Abort{}) {
		t.Error("Send after the panic = true")
	}
	if !strings.Contains(r.reports.text(), "the engine panicked: clock armed") {
		t.Errorf("reports %q", r.reports.text())
	}
}

// TestSessionPanicOnAnInputClosesDriver: a Session that panics while it
// handles an Input from a worker (not a Command) closes the Driver the same
// way as for a Command.
func TestSessionPanicOnAnInputClosesDriver(t *testing.T) {
	clock := &panicClock{}
	st := steps.Step{ID: "arm", Title: "Arm…", LogLabel: "Arm",
		Run: func(context.Context, steps.Env) steps.Result { clock.armed.Store(true); return steps.Result{} }}
	r := newRig(t, engine.Options{Now: clock.now}, Deps{}, action("a", st))
	r.d.Send(engine.Start{Action: "a"})
	panicked(t, r)
}

// TestSessionPanicOnAHookResultClosesDriver: the same for an Input the loop
// made itself: the result of a report's Accept hook.
func TestSessionPanicOnAHookResultClosesDriver(t *testing.T) {
	clock := &panicClock{}
	rep := actions.Action{ID: "rep", Label: "rep", Busy: "…",
		Discover: func(context.Context, actions.Env) actions.Discovery {
			return actions.Report{Text: "findings", Accept: func() error { clock.armed.Store(true); return nil }}
		}}
	r := newRig(t, engine.Options{Now: clock.now}, Deps{}, rep)
	r.d.Send(engine.Start{Action: "rep"})
	for {
		ev, ok := r.next()
		if !ok {
			t.Fatal("Events closed before the report")
		}
		if rp, ok := ev.(engine.OutputReport); ok {
			r.d.Send(engine.Confirm{Prompt: rp.ID, Yes: true})
			break
		}
	}
	panicked(t, r)
}

// TestCloseCancelsARunningDiscovery: Close while a discovery is running
// cancels its context (a plain cancellation, not a result taken) and waits for
// it to return.
func TestCloseCancelsARunningDiscovery(t *testing.T) {
	started, finished := make(chan struct{}), make(chan error, 1)
	disc := actions.Action{ID: "d", Label: "d", Busy: "…",
		Discover: func(ctx context.Context, _ actions.Env) actions.Discovery {
			close(started)
			<-ctx.Done()
			finished <- context.Cause(ctx)
			return nil
		}}
	r := newRig(t, engine.Options{}, Deps{}, disc)
	r.d.Send(engine.Start{Action: "d"})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the discovery never started")
	}
	r.d.Close()
	select {
	case cause := <-finished:
		if !errors.Is(cause, context.Canceled) {
			t.Errorf("cancel cause %v, want context.Canceled", cause)
		}
	default:
		t.Fatal("Close returned before the discovery saw its context cancelled")
	}
}

// TestSessionPanicCancelsAPrefetch: a prefetch in flight when the Session
// panics (so no abort can cancel it) is cancelled and reaped by the teardown.
func TestSessionPanicCancelsAPrefetch(t *testing.T) {
	clock := &panicClock{}
	made := make(chan *fakePrefetch, 1)
	deps := Deps{Prefetch: func(string) Prefetch {
		p := newFakePrefetch("x")
		made <- p
		return p
	}}
	st := steps.Step{ID: "a", Title: "A…", LogLabel: "A", Prefetch: "p",
		Run: func(context.Context, steps.Env) steps.Result { return steps.Result{} }}
	gated := action("gated", st)
	gated.Gate = "Go?"
	r := newRig(t, engine.Options{Now: clock.now}, deps, gated)
	r.d.Send(engine.Start{Action: "gated"})
	var confirm engine.NeedsConfirm
	for {
		ev, _ := r.next()
		if c, ok := ev.(engine.NeedsConfirm); ok {
			confirm = c
			break
		}
	}
	p := <-made
	clock.armed.Store(true)
	r.d.Send(engine.Confirm{Prompt: confirm.ID, Yes: true})
	panicked(t, r)
	if !p.cancelled.Load() {
		t.Fatal("the prefetch was left running by the teardown")
	}
}

// TestPrefetchIsStartedOnce: two steps naming the same prefetch share one;
// and with no Prefetch dependency the steps simply run themselves.
func TestPrefetchIsStartedOnce(t *testing.T) {
	mk := func(id, name string) steps.Step {
		return steps.Step{ID: id, Title: id + "…", LogLabel: id, Prefetch: name, AlwaysLog: true,
			Run: func(context.Context, steps.Env) steps.Result { return steps.Result{Output: id + " ran"} }}
	}
	var made int
	deps := Deps{Prefetch: func(string) Prefetch { made++; return newFakePrefetch("shared") }}
	r := newRig(t, engine.Options{}, deps, action("two", mk("a", "p"), mk("b", "p")))
	if got, _ := lines(r.run("two", nil)); len(got) == 0 || got[0] != "shared" {
		t.Errorf("output %q, want the prefetched result first", got)
	}
	if made != 1 {
		t.Errorf("the prefetch was made %d times, want once", made)
	}

	none := newRig(t, engine.Options{}, Deps{}, action("none", mk("a", "p")))
	if got, _ := lines(none.run("none", nil)); len(got) != 1 || got[0] != "a ran" {
		t.Errorf("without a Prefetch dependency: output %q, want the step's own run", got)
	}
}

// sleepingSudoStep is a rig with action "a" (one NeedsPriv step that sleeps)
// whose clock can be armed; the caller starts it (after any change to the
// rig's fake Priv, which the Driver reads from its workers).
func sleepingSudoStep(t *testing.T, deps Deps) (*rig, *panicClock) {
	t.Helper()
	clock := &panicClock{}
	st := shStep("sudo-step", "echo $$; exec sleep 30")
	st.NeedsPriv = true
	r := newRig(t, engine.Options{Now: clock.now}, deps, action("a", st))
	return r, clock
}

// TestSessionPanicHangsUpAnOpenAskpassRequest: a sudo prompt waiting for the
// user's password when the Session panics (so no abort hangs up on it) is
// hung up on by the teardown: its client sees EOF, never a password.
func TestSessionPanicHangsUpAnOpenAskpassRequest(t *testing.T) {
	suShim(t)
	reqs := make(chan *privilege.Request)
	r, clock := sleepingSudoStep(t, Deps{Askpass: reqs})
	r.d.Send(engine.Start{Action: "a"})
	pid := 0
	var prompt engine.NeedsPassword
	var c *askpassClient
	for prompt.ID == 0 {
		ev, ok := r.next()
		if !ok {
			t.Fatal("Events closed")
		}
		switch ev := ev.(type) {
		case engine.OutputChunk:
			if pid == 0 {
				pid = pidOf(t, ev)
				child, err := proc.Read(pid)
				if err != nil {
					t.Fatal(err)
				}
				privtest.FakeSystem(t, func() int { return ourUID }, "/fake/archmaid", func(p int) (proc.Info, error) {
					switch p {
					case helperPid:
						return proc.Info{PPid: sudoPid, Start: child.Start + 20, Exe: "/fake/archmaid"}, nil
					case sudoPid:
						return proc.Info{PPid: pid, Start: child.Start + 10, Exe: "/usr/bin/" + "sudo"}, nil
					case pid:
						return proc.Info{PPid: 1, Start: child.Start, Exe: "/usr/bin/sh"}, nil
					}
					return proc.Info{}, errors.New("no such process")
				})
				c = newAskpassClient(t, "[sudo] password for me:", privilege.Peer{Pid: helperPid, UID: ourUID, Start: child.Start + 20})
				sendReq(t, reqs, c.req)
			}
		case engine.NeedsPassword:
			prompt = ev
		}
	}
	clock.armed.Store(true)
	r.d.Send(engine.Abort{})
	panicked(t, r)
	if line := got(t, c); line != "" {
		t.Fatalf("a request open at the panic got %q, want a hang-up", line)
	}
}

// TestSessionPanicStopsTheKeepAlive: a Session panic while the sudo ticket's
// keep-alive runs stops it with the rest.
func TestSessionPanicStopsTheKeepAlive(t *testing.T) {
	r, clock := sleepingSudoStep(t, Deps{KeepAliveEvery: 5 * time.Millisecond})
	r.priv.cached = false
	r.d.Send(engine.Start{Action: "a"})
	for r.priv.refreshes.Load() == 0 {
		ev, ok := r.next()
		if !ok {
			t.Fatal("Events closed")
		}
		if p, isPw := ev.(engine.NeedsPassword); isPw {
			r.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: "good"})
		}
	}
	clock.armed.Store(true)
	r.d.Send(engine.Abort{})
	panicked(t, r)
	n := r.priv.refreshes.Load()
	time.Sleep(40 * time.Millisecond)
	if after := r.priv.refreshes.Load(); after != n {
		t.Fatalf("the keep-alive refreshed %d more times after the panic", after-n)
	}
}
