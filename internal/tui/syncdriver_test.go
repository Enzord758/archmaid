package tui

import (
	"context"
	"sync"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// syncDriver is the tests' engine.Driver: it owns a Session and carries out
// its Effects as sessionTarget does, with nothing in the background. Send
// applies a Command at once; Effects that take time (a probe, a discovery,
// a step) become work, run one at a time in FIFO order by Pull on the
// test's goroutine, so a transcript is deterministic. Its Events are handed
// out by Pull and published, never on the Events channel.
//
// The binary will run on internal/driver (E6), built the same way:
// driver.New(s, driver.Deps{Priv, Log, ...}).
type syncDriver struct {
	deps syncDeps

	mu      sync.Mutex // the Session and the maps; work runs outside it
	s       *engine.Session
	out     []engine.Event          // published, not yet handed out
	work    []func() []engine.Input // queued Effects
	child   map[uint64]*runnerChild // running children, by Seq
	release map[uint64]context.CancelCauseFunc
	fetches map[string]*auditFetch
	closed  bool

	// launches counts children started, and afterWork runs after each piece
	// of work, before its Inputs reach the Session: the transcript harness
	// takes the exec lines there.
	launches  int
	afterWork func()

	events chan engine.Event
	once   sync.Once
}

// syncDeps is what a syncDriver needs: internal/driver's Deps of the same
// names (a subset).
type syncDeps struct {
	Priv privilege.Privileged
	Log  *logstore.Store
	// Faillock answers ReadFaillock; tests never read the real tally.
	Faillock func() (privilege.Status, error)
}

type runnerChild struct {
	c    *proc.Child
	ch   chan proc.Msg
	quit chan struct{}
}

var _ engine.Driver = (*syncDriver)(nil)

func newSyncDriver(s *engine.Session, deps syncDeps) *syncDriver {
	if s == nil || deps.Priv == nil || deps.Log == nil {
		panic("newSyncDriver: nil Session, Priv or Log")
	}
	if deps.Faillock == nil {
		deps.Faillock = func() (privilege.Status, error) { return privilege.Status{Deny: 3}, nil }
	}
	return &syncDriver{
		deps: deps, s: s,
		child:   map[uint64]*runnerChild{},
		release: map[uint64]context.CancelCauseFunc{},
		fetches: map[string]*auditFetch{},
		events:  make(chan engine.Event),
	}
}

// Events carries nothing (Pull hands the Events out); closed by Close.
func (d *syncDriver) Events() <-chan engine.Event { return d.events }

// Send applies c to the Session at once; false once closed.
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

// Close aborts what runs, stops every child and closes Events.
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
		close(d.events)
	})
}

// Pull hands out the published Events; with none, it runs the next queued
// work and hands out what that produced. Empty means the driver is idle:
// no Event and no work left. It may block as long as the work does.
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

// published hands out the published Events without running any work.
func (d *syncDriver) published() []engine.Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	evs := d.out
	d.out = nil
	return evs
}

// idle reports whether no work is queued.
func (d *syncDriver) idle() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.work) == 0
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
	d.out = append(d.out, o.Events...)
	d.handle(hooks)
}

// effect carries out one Effect other than RunHook. Called with mu held.
func (d *syncDriver) effect(e engine.Effect) {
	switch e := e.(type) {
	case engine.Probe:
		priv := d.deps.Priv
		d.work = append(d.work, func() []engine.Input {
			return []engine.Input{engine.ProbeDone{Cached: priv.Probe(context.Background())}}
		})
	case engine.Authenticate:
		priv, pw := d.deps.Priv, string(e.Password)
		d.work = append(d.work, func() []engine.Input {
			detail, err := priv.Authenticate(context.Background(), pw)
			return []engine.Input{engine.AuthDone{OK: err == nil, Detail: detail, Err: err}}
		})
	case engine.ReadFaillock:
		read := d.deps.Faillock
		d.work = append(d.work, func() []engine.Input {
			st, err := read()
			return []engine.Input{engine.FaillockRead{Failures: st.Failures, Deny: st.Deny, Unlock: st.Unlock, Err: err}}
		})
	case engine.Discover:
		ctx, cancel := context.WithCancelCause(context.Background())
		d.release[e.Seq] = cancel
		d.work = append(d.work, func() []engine.Input {
			return []engine.Input{engine.Discovered{Seq: e.Seq, Discovery: e.Run(ctx, e.Env)}}
		})
	case engine.StartStep:
		d.startStep(e)
	case engine.Stop:
		d.stop(e.Seq)
	case engine.WriteLog:
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
	case engine.KeepAlive, engine.AnswerAskpass, engine.HangUpAskpass:
		// The refresh is a tick (the transcripts drop ticks); no scenario
		// has an askpass request.
	}
}

// stop ends what Seq started: a child gets SIGINT, then SIGKILL after the
// grace (runStep); an in-process step's or discovery's ctx is
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

// startStep starts a step: an in-process one as work (awaiting its
// prefetch, if one runs), a child now, pumped as work. Called with mu held.
func (d *syncDriver) startStep(e engine.StartStep) {
	if run := e.Step.Run; run != nil {
		ctx, cancel := context.WithCancelCause(context.Background())
		d.release[e.Seq] = cancel
		if f := d.fetches[e.Step.Prefetch]; f != nil {
			run = func(ctx context.Context, _ steps.Env) steps.Result { return f.Run(ctx) }
		}
		d.work = append(d.work, func() []engine.Input {
			return []engine.Input{engine.StepResult{Seq: e.Seq, Result: run(ctx, e.Env)}}
		})
		return
	}
	c := &runnerChild{ch: make(chan proc.Msg, proc.BatchMax), quit: make(chan struct{})}
	c.c = runStep(e.Step, c.ch, c.quit)
	d.child[e.Seq] = c
	d.launches++
	d.work = append(d.work, d.pump(e.Seq, c))
}

// pump is one proc.Next on a child's stream: StepOutput (sanitized, as a
// Driver must) and, at its end, StepExited; until then it queues itself
// again.
func (d *syncDriver) pump(seq uint64, c *runnerChild) func() []engine.Input {
	var next func() []engine.Input
	next = func() []engine.Input {
		b, ok := proc.Next(c.ch, c.quit, proc.BatchMax)
		if !ok {
			return nil // stopped
		}
		var ins []engine.Input
		if len(b.Lines) > 0 {
			lines := make([]string, len(b.Lines))
			for i, l := range b.Lines {
				lines[i] = sanitize.String(l, sanitize.KeepSGR)
			}
			ins = append(ins, engine.StepOutput{Seq: seq, Lines: lines})
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if b.Done {
			delete(d.child, seq)
			return append(ins, engine.StepExited{Seq: seq, Err: b.Err})
		}
		d.work = append(d.work, next)
		return ins
	}
	return next
}
