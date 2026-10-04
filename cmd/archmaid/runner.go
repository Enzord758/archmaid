package main

import (
	"context"
	"fmt"
	"os/user"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ── the TUI's engine.Driver until internal/driver lands ──────────────────────
//
// The TUI talks to the engine only through engine.Driver: it Sends a
// Command for what the user did and applies the Events it reads (E7). The
// Driver is built the way internal/driver's is, so swapping it in is one
// line in main:
//
//	s := engine.New(opts)
//	d := newSyncDriver(s, syncDeps{...})   // → driver.New(s, driver.Deps{...})
//
// syncDriver carries out the Session's Effects like a Driver, with one
// extra: in pull mode (live false, the transcript harness) nothing runs in
// the background. Effects that take time become work, run one at a time, in
// FIFO order, by Pull on the caller's goroutine — exactly sessionTarget's
// order — so a transcript is deterministic. Live, each such Effect runs on
// its own goroutine and its Inputs go back to the Session under a mutex.

// syncDeps is what a syncDriver needs; the fields are internal/driver's
// Deps of the same names (a subset).
type syncDeps struct {
	Priv privilege.Privileged // required
	Log  *logstore.Store      // required
	// Askpass is the session's askpass server's requests, or nil. The
	// driver verifies each one against the running step's child.
	Askpass <-chan *privilege.Request
	// Faillock reads the user's pam_faillock count (never the faillock
	// binary); nil reads the current user's file.
	Faillock func() (privilege.Status, error)
	// KeepAliveEvery is the sudo ticket refresh (default 60 s). Live only.
	KeepAliveEvery time.Duration
	// Live runs Effects in the background; false is pull mode (Pull).
	Live bool
}

// currentFaillock is the current user's pam_faillock state.
func currentFaillock() (privilege.Status, error) {
	u, err := user.Current()
	if err != nil {
		return privilege.Status{}, err
	}
	return privilege.FaillockStatus(u.Username, time.Now())
}

// syncDriver runs one Session for the TUI. See the comment above.
type syncDriver struct {
	deps syncDeps

	mu      sync.Mutex // guards everything below (live mode)
	s       *engine.Session
	out     []engine.Event          // published, not yet delivered
	work    []func() []engine.Input // pull mode's queued Effects
	child   map[uint64]*runnerChild // running children, by Seq
	release map[uint64]context.CancelCauseFunc
	fetches map[string]*auditFetch
	asks    map[uint64]*privilege.Request
	lastAsk uint64
	gen     int  // the keep-alive loop's generation
	closed  bool // Close was called

	// launches counts children started, and afterWork (pull mode, nil
	// outside tests) runs after each piece of work, before its Inputs reach
	// the Session: the transcript harness takes the exec lines there.
	launches  int
	afterWork func()

	events chan engine.Event
	wake   chan struct{} // an Event was published (live)
	done   chan struct{} // closed by Close
	wg     sync.WaitGroup
	once   sync.Once
}

type runnerChild struct {
	c    *proc.Child
	ch   chan proc.Msg
	quit chan struct{}
}

var _ engine.Driver = (*syncDriver)(nil)

// newSyncDriver starts a driver for s. Live, it starts the goroutine that
// delivers Events and the one that takes askpass requests.
func newSyncDriver(s *engine.Session, deps syncDeps) *syncDriver {
	if s == nil || deps.Priv == nil || deps.Log == nil {
		panic("newSyncDriver: nil Session, Priv or Log")
	}
	if deps.Faillock == nil {
		deps.Faillock = currentFaillock
	}
	if deps.KeepAliveEvery <= 0 {
		deps.KeepAliveEvery = 60 * time.Second
	}
	d := &syncDriver{
		deps: deps, s: s,
		child:   map[uint64]*runnerChild{},
		release: map[uint64]context.CancelCauseFunc{},
		fetches: map[string]*auditFetch{},
		asks:    map[uint64]*privilege.Request{},
		events:  make(chan engine.Event),
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	if deps.Live {
		d.wg.Add(1)
		go d.deliver()
		if deps.Askpass != nil {
			d.wg.Add(1)
			go d.takeAskpass()
		}
	}
	return d
}

// Events delivers every Event in order. In pull mode it carries nothing:
// Pull hands the Events out instead.
func (d *syncDriver) Events() <-chan engine.Event { return d.events }

// Send applies c to the Session at once. It returns false once the driver
// is closed.
func (d *syncDriver) Send(c engine.Command) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false
	}
	var o engine.Output
	switch c := c.(type) {
	case engine.Start:
		o = d.s.Start(c.Action)
	case engine.Confirm:
		o = d.s.Confirm(c.Prompt, c.Yes)
	case engine.SubmitPassword:
		o = d.s.SubmitPassword(c.Prompt, c.Password)
	case engine.CancelPrompt:
		o = d.s.CancelPrompt(c.Prompt)
	case engine.Select:
		o = d.s.Select(c.Prompt, c.Picked)
	case engine.Abort:
		o = d.s.Abort()
	}
	d.apply(o)
	return true
}

// Close aborts what runs, stops every child and goroutine the driver
// started, and closes Events.
func (d *syncDriver) Close() {
	d.once.Do(func() {
		d.mu.Lock()
		d.apply(d.s.Abort())
		d.closed = true
		for seq := range d.child {
			d.stop(seq)
		}
		for _, f := range d.fetches {
			f.Cancel()
		}
		d.work, d.out = nil, nil
		d.mu.Unlock()
		close(d.done)
		d.wg.Wait()
		close(d.events)
	})
}

// Pull (pull mode) hands out the published Events; with none, it runs the
// next queued work and hands out what that produced. Empty means the
// driver is idle: no Event and no work left. It runs on the caller's
// goroutine and may block as long as the work does (a probe, a step's next
// output).
func (d *syncDriver) Pull() []engine.Event {
	for {
		d.mu.Lock()
		if len(d.out) > 0 || len(d.work) == 0 || d.closed {
			evs := d.out
			d.out = nil
			d.mu.Unlock()
			return evs
		}
		w := d.work[0]
		d.work = d.work[1:]
		d.mu.Unlock()
		ins := w()
		if d.afterWork != nil {
			d.afterWork()
		}
		d.mu.Lock()
		d.handle(ins)
		d.mu.Unlock()
	}
}

// published (pull mode) hands out the published Events without running
// any work.
func (d *syncDriver) published() []engine.Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	evs := d.out
	d.out = nil
	return evs
}

// deliver (live) moves published Events to the channel, without bound on
// the backlog: the Session is never blocked by a slow reader.
func (d *syncDriver) deliver() {
	defer d.wg.Done()
	for {
		d.mu.Lock()
		if len(d.out) == 0 {
			d.mu.Unlock()
			select {
			case <-d.wake:
				continue
			case <-d.done:
				return
			}
		}
		ev := d.out[0]
		d.out = d.out[1:]
		d.mu.Unlock()
		select {
		case d.events <- ev:
		case <-d.done:
			return
		}
	}
}

func (d *syncDriver) takeAskpass() {
	defer d.wg.Done()
	for {
		select {
		case req, ok := <-d.deps.Askpass:
			if !ok {
				return
			}
			d.mu.Lock()
			if d.closed {
				req.Cancel()
			} else {
				d.askpass(req)
			}
			d.mu.Unlock()
		case <-d.done:
			return
		}
	}
}

// publish queues evs for the reader. Called with mu held.
func (d *syncDriver) publish(evs []engine.Event) {
	if len(evs) == 0 {
		return
	}
	d.out = append(d.out, evs...)
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// handle feeds the Session Inputs, each Output applied in turn. A
// StepResult releases its step's ctx once taken (StartStep's contract).
// Called with mu held.
func (d *syncDriver) handle(ins []engine.Input) {
	for _, in := range ins {
		if d.closed {
			return
		}
		d.apply(d.s.Handle(in))
		if res, ok := in.(engine.StepResult); ok {
			if cancel := d.release[res.Seq]; cancel != nil {
				cancel(steps.ErrResultTaken)
				delete(d.release, res.Seq)
			}
		}
	}
}

// askpass vets req against the running step's child (Request.Verify) and
// hands it to the Session, which decides whether to ask. Called with mu
// held.
func (d *syncDriver) askpass(req *privilege.Request) {
	d.lastAsk++
	id := d.lastAsk
	d.asks[id] = req
	in := engine.AskpassRequest{Req: id, Prompt: req.Prompt, Refused: "no step is running"}
	for _, c := range d.child { // at most one step runs
		if c.c == nil {
			continue
		}
		in.Refused = ""
		from, err := req.Verify(c.c.Pid, c.c.Start)
		if err != nil {
			in.Refused = err.Error()
			break
		}
		in.Requester = fmt.Sprintf("%s (pid %d)", sanitize.ClipTail(sanitize.OneLine(orUnknown(from.CallerExe)), 40), from.CallerPid)
	}
	d.handle([]engine.Input{in})
}

// apply carries out an Output: its Effects in order (a RunHook to
// completion before the next), then publishes its Events, then hands the
// Session each HookDone. Called with mu held.
func (d *syncDriver) apply(o engine.Output) {
	var hooks []engine.Input
	for _, e := range o.Effects {
		if h, ok := e.(engine.RunHook); ok {
			hooks = append(hooks, engine.HookDone{Seq: h.Seq, Err: h.Fn()})
			continue
		}
		d.effect(e)
	}
	d.publish(o.Events)
	d.handle(hooks)
}

// later runs f as work: in pull mode queued for Pull, live on its own
// goroutine, its Inputs handed to the Session as they come back.
func (d *syncDriver) later(f func() []engine.Input) {
	if !d.deps.Live {
		d.work = append(d.work, f)
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ins := f()
		d.mu.Lock()
		d.handle(ins)
		d.mu.Unlock()
	}()
}

// effect carries out one Effect other than RunHook. Called with mu held.
func (d *syncDriver) effect(e engine.Effect) {
	switch e := e.(type) {
	case engine.Probe:
		priv := d.deps.Priv
		d.later(func() []engine.Input {
			return []engine.Input{engine.ProbeDone{Cached: priv.Probe(context.Background())}}
		})
	case engine.Authenticate:
		// su first, then sudo -S -v (privilege.Sudo.Authenticate): a wrong
		// password never reaches sudo.
		priv, pw := d.deps.Priv, string(e.Password)
		d.later(func() []engine.Input {
			detail, err := priv.Authenticate(context.Background(), pw)
			return []engine.Input{engine.AuthDone{OK: err == nil, Detail: detail, Err: err}}
		})
	case engine.ReadFaillock:
		read := d.deps.Faillock
		d.later(func() []engine.Input {
			st, err := read()
			return []engine.Input{engine.FaillockRead{Failures: st.Failures, Deny: st.Deny, Unlock: st.Unlock, Err: err}}
		})
	case engine.KeepAlive:
		d.gen++ // ends the loop running, if any
		if e.On && d.deps.Live {
			d.keepAlive(d.gen)
		}
	case engine.Discover:
		ctx, cancel := context.WithCancelCause(context.Background())
		d.release[e.Seq] = cancel
		d.later(func() []engine.Input {
			return []engine.Input{engine.Discovered{Seq: e.Seq, Discovery: e.Run(ctx, e.Env)}}
		})
	case engine.StartStep:
		d.startStep(e)
	case engine.Stop:
		d.stop(e.Seq)
	case engine.WriteLog:
		// The store rotates old runs only at a real run's first write.
		_ = d.deps.Log.Append(e.Category, e.Kind, e.Label, e.Body)
	case engine.WriteLastRun:
		_ = d.deps.Log.WriteLastRun(e.Record)
	case engine.StartPrefetch:
		if e.Name == steps.PrefetchAURAudit && d.fetches[e.Name] == nil {
			d.fetches[e.Name] = startAuditFetch()
		}
	case engine.CancelPrefetch:
		if f := d.fetches[e.Name]; f != nil {
			f.Cancel()
			delete(d.fetches, e.Name)
		}
	case engine.AnswerAskpass:
		req, pw := d.asks[e.Req], string(e.Password)
		if req == nil {
			return
		}
		d.later(func() []engine.Input {
			detail, err := req.Answer(pw) // su-checked before anything reaches sudo
			return []engine.Input{engine.AskpassAnswered{Req: e.Req, OK: err == nil, Detail: detail}}
		})
	case engine.HangUpAskpass:
		if req := d.asks[e.Req]; req != nil {
			req.Cancel()
			delete(d.asks, e.Req)
		}
	}
}

// stop ends what Seq started: a child gets SIGINT, then SIGKILL after the
// grace (procOptions); an in-process step's or discovery's ctx is
// cancelled with plain context.Canceled, an abort. Called with mu held.
func (d *syncDriver) stop(seq uint64) {
	if c := d.child[seq]; c != nil {
		close(c.quit)
		c.c.Stop() // nil-safe: a dry run starts no child
		delete(d.child, seq)
	}
	if cancel := d.release[seq]; cancel != nil {
		cancel(nil)
		delete(d.release, seq)
	}
}

// keepAlive (live) refreshes the sudo ticket every KeepAliveEvery until a
// KeepAlive or Close ends generation gen.
func (d *syncDriver) keepAlive(gen int) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		t := time.NewTicker(d.deps.KeepAliveEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				d.mu.Lock()
				on := d.gen == gen && !d.closed
				d.mu.Unlock()
				if !on {
					return
				}
				d.deps.Priv.Refresh(context.Background())
			case <-d.done:
				return
			}
		}
	}()
}

// startStep starts a step: an in-process one as work (awaiting its
// prefetch, if one runs), a child now, pumped as work. Only NeedsPriv steps
// get sudo and SUDO_ASKPASS (stepSpec); the child stays in the TUI's
// session (no Setsid). Called with mu held.
func (d *syncDriver) startStep(e engine.StartStep) {
	if run := e.Step.Run; run != nil {
		ctx, cancel := context.WithCancelCause(context.Background())
		d.release[e.Seq] = cancel
		if f := d.fetches[e.Step.Prefetch]; f != nil {
			run = func(ctx context.Context, _ steps.Env) steps.Result { return f.run(ctx) }
		}
		d.later(func() []engine.Input {
			return []engine.Input{engine.StepResult{Seq: e.Seq, Result: run(ctx, e.Env)}}
		})
		return
	}
	c := &runnerChild{ch: make(chan proc.Msg, proc.BatchMax), quit: make(chan struct{})}
	c.c = proc.New(procOptions()).Start(stepSpec(e.Step), c.ch, c.quit)
	d.child[e.Seq] = c
	d.launches++
	d.later(d.pump(e.Seq, c))
}

// pump is one proc.Next on a child's stream: StepOutput and, at its end,
// StepExited. Until then it queues itself again (live: it loops).
func (d *syncDriver) pump(seq uint64, c *runnerChild) func() []engine.Input {
	var next func() []engine.Input
	next = func() []engine.Input {
		for {
			b, ok := proc.Next(c.ch, c.quit, proc.BatchMax)
			if !ok {
				return nil // stopped
			}
			var ins []engine.Input
			if len(b.Lines) > 0 {
				lines := make([]string, len(b.Lines)) // StepOutput is sanitized
				for i, l := range b.Lines {
					lines[i] = sanitize.String(l, sanitize.KeepSGR)
				}
				ins = append(ins, engine.StepOutput{Seq: seq, Lines: lines})
			}
			if b.Done {
				d.mu.Lock()
				delete(d.child, seq)
				d.mu.Unlock()
				return append(ins, engine.StepExited{Seq: seq, Err: b.Err})
			}
			if !d.deps.Live {
				d.mu.Lock()
				d.work = append(d.work, next)
				d.mu.Unlock()
				return ins
			}
			d.mu.Lock()
			d.handle(ins)
			d.mu.Unlock()
		}
	}
	return next
}
