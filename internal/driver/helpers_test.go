package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// fakePriv is the sudo backend: a probe that answers cached, a password
// that is right, refreshes counted. Command runs argv as it is.
type fakePriv struct {
	cached    bool
	password  string
	refreshes atomic.Int32
}

func (f *fakePriv) Probe(context.Context) bool { return f.cached }

func (f *fakePriv) Authenticate(_ context.Context, pw string) (string, error) {
	if pw == f.password {
		return "", nil
	}
	return "", errors.New("wrong password")
}

func (f *fakePriv) Refresh(context.Context) { f.refreshes.Add(1) }

func (f *fakePriv) Command(argv []string, _ bool) proc.Spec {
	return proc.Spec{Name: argv[0], Args: argv[1:]}
}

// reports collects what the Driver reported.
type reports struct {
	mu   sync.Mutex
	errs []error
}

func (r *reports) add(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

func (r *reports) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, err := range r.errs {
		b.WriteString(err.Error() + "\n")
	}
	return b.String()
}

// rig is one Driver under test with what it was given.
type rig struct {
	t       *testing.T
	d       *Driver
	priv    *fakePriv
	log     *logstore.Store
	reports *reports
}

// newRig starts a Driver on a Session whose menu is menu, with deps'
// zero fields filled for a test (a fake Priv, a scratch log, no system
// reads, a short kill grace). The test fails if the Driver leaves a
// goroutine behind once closed (checked last, after every other cleanup).
func newRig(t *testing.T, opts engine.Options, deps Deps, menu ...actions.Action) *rig {
	t.Helper()
	noLeak(t)
	old := actions.Menu
	actions.Menu = menu
	t.Cleanup(func() { actions.Menu = old })
	r := &rig{t: t, reports: &reports{}}
	if deps.Priv == nil {
		r.priv = &fakePriv{cached: true, password: "good"}
		deps.Priv = r.priv
	}
	if deps.Log == nil {
		deps.Log = logstore.New(logstore.Options{Dir: t.TempDir(), DryRun: opts.DryRun})
	}
	r.log = deps.Log
	if deps.Status == nil {
		deps.Status = func() sysstat.Status { return sysstat.Status{} }
	}
	if deps.Faillock == nil {
		deps.Faillock = func() engine.FaillockRead { return engine.FaillockRead{Deny: 3} }
	}
	if deps.TickEvery == 0 {
		deps.TickEvery = time.Hour // no ticks to mask a stall from next's timeout
	}
	if deps.KillGrace == 0 {
		deps.KillGrace = 300 * time.Millisecond
	}
	if deps.WaitDelay == 0 {
		deps.WaitDelay = 200 * time.Millisecond
	}
	deps.Report = r.reports.add
	r.d = New(engine.New(opts), deps)
	t.Cleanup(r.d.Close)
	return r
}

// noLeak fails t if, once its cleanups have run, more goroutines run than
// before it started (polled for a while: a goroutine's exit is not
// instant).
func noLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for runtime.NumGoroutine() > before {
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<16)
				n := runtime.Stack(buf, true)
				t.Errorf("%d goroutines left over (%d before):\n%s", runtime.NumGoroutine(), before, buf[:n])
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}

// next is the Driver's next event; ok false once Events is closed.
func (r *rig) next() (engine.Event, bool) {
	r.t.Helper()
	select {
	case ev, ok := <-r.d.Events():
		return ev, ok
	case <-time.After(10 * time.Second):
		r.t.Fatal("no event for 10 s")
		return nil, false
	}
}

// run starts action and reads events until its Finished, answering each
// with answer (nil: no answer). It returns every event but the status
// ticks, held to the engine's contract.
func (r *rig) run(action string, answer func(engine.Event) engine.Command) []engine.Event {
	r.t.Helper()
	if !r.d.Send(engine.Start{Action: action}) {
		r.t.Fatal("Send(Start) = false on an open Driver")
	}
	var evs []engine.Event
	for {
		ev, ok := r.next()
		if !ok {
			r.t.Fatalf("Events closed before Finished; reports:\n%s", r.reports.text())
		}
		if _, tick := ev.(engine.StatusUpdated); !tick {
			evs = append(evs, ev)
		}
		if answer != nil {
			if c := answer(ev); c != nil && !r.d.Send(c) {
				r.t.Fatal("Send = false on an open Driver")
			}
		}
		if _, ok := ev.(engine.Finished); ok {
			enginetest.CheckContract(r.t, evs)
			return evs
		}
	}
}

// yes answers every confirm yes.
func yes(ev engine.Event) engine.Command {
	if c, ok := ev.(engine.NeedsConfirm); ok {
		return engine.Confirm{Prompt: c.ID, Yes: true}
	}
	return nil
}

// lines are the output lines of evs, and the lines dropped.
func lines(evs []engine.Event) (out []string, dropped int) {
	for _, ev := range evs {
		if c, ok := ev.(engine.OutputChunk); ok {
			out = append(out, c.Lines...)
			dropped += c.Dropped
		}
	}
	return out, dropped
}

// find is the first event of type T in evs.
func find[T engine.Event](t *testing.T, evs []engine.Event) T {
	t.Helper()
	for _, ev := range evs {
		if e, ok := ev.(T); ok {
			return e
		}
	}
	var zero T
	t.Fatalf("no %T among %s", zero, kinds(evs))
	return zero
}

func kinds(evs []engine.Event) string {
	k := make([]string, len(evs))
	for i, ev := range evs {
		k[i] = strings.TrimPrefix(fmt.Sprintf("%T", ev), "engine.")
	}
	return strings.Join(k, " ")
}

// action is a menu action running queue.
func action(id string, queue ...steps.Step) actions.Action {
	return actions.Action{ID: id, Label: id, Steps: func(actions.Env) []steps.Step { return queue }}
}

// shStep is a child step running script under sh.
func shStep(id, script string) steps.Step {
	return steps.Step{ID: id, Title: id + "…", LogLabel: id, Argv: []string{"sh", "-c", script}, AlwaysLog: true}
}

// pidOf reads the pid a step printed first (`echo $$`).
func pidOf(t *testing.T, ev engine.Event) int {
	t.Helper()
	c, ok := ev.(engine.OutputChunk)
	if !ok || len(c.Lines) == 0 {
		t.Fatalf("want the pid line, got %#v", ev)
	}
	pid, err := strconv.Atoi(c.Lines[0])
	if err != nil {
		t.Fatalf("pid line %q: %v", c.Lines[0], err)
	}
	return pid
}

// gone waits until pid has been reaped (no /proc entry, not even a
// zombie's), up to within.
func gone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); errors.Is(err, os.ErrNotExist) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// fakePrefetch is a prefetch whose result is out.
type fakePrefetch struct {
	out       string
	done      chan struct{}
	once      sync.Once
	cancelled atomic.Bool
}

func newFakePrefetch(out string) *fakePrefetch {
	return &fakePrefetch{out: out, done: make(chan struct{})}
}

func (p *fakePrefetch) Run(context.Context) steps.Result { return steps.Result{Output: p.out} }
func (p *fakePrefetch) Cancel() {
	p.cancelled.Store(true)
	p.once.Do(func() { close(p.done) })
}
func (p *fakePrefetch) Done() <-chan struct{} { return p.done }

// fakeHelper is a Helper that emits what helperclient.Run would for a
// helper that printed lines: granted or not, or blocking until Stop.
func fakeHelper(lines []string, granted, notGranted, block bool) Helper {
	return func(ctx context.Context, seq uint64, _ steps.Step, emit func(engine.Input)) helperclient.Outcome {
		if granted {
			emit(engine.PrivilegeGranted{Seq: seq})
		}
		if len(lines) > 0 {
			emit(engine.StepOutput{Seq: seq, Lines: lines})
		}
		if block {
			<-ctx.Done()
			emit(engine.StepExited{Seq: seq, Err: context.Cause(ctx)})
			return helperclient.Outcome{}
		}
		emit(engine.StepExited{Seq: seq, NotGranted: notGranted})
		return helperclient.Outcome{Granted: granted, NotGranted: notGranted}
	}
}
