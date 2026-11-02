// Package driver is the real engine.Driver (E6): it owns one
// engine.Session, carries out its Effects against the system (children
// through internal/proc, privilege through internal/privilege, logs through
// internal/logstore), feeds their results back as Inputs, runs the one
// status ticker and streams the Session's Events, coalescing output.
//
// # Goroutines
//
// One goroutine, the loop, owns the Session: every Command (from Send) and
// every Input (from a worker, a child's pump, the ticker, the askpass
// reader) is serialized into it, and it alone calls the Session and starts
// Effects. An Effect that answers with an Input runs on a worker goroutine
// of its own (the probe, a password check, a discovery, an in-process step,
// a child's pump), which posts its Input back to the loop. WriteLog,
// WriteLastRun and RunHook run on the loop, to completion, before the next
// Effect starts; so do Stop, KeepAlive, the prefetches and the askpass
// hang-ups, which only signal.
//
// The loop never blocks on the front end: it hands each Output's Events to
// the outbox, which a publisher goroutine drains into Events. Control
// events are buffered there without bound; output is coalesced and, past
// Config.MaxLines undelivered lines of one step, its oldest lines are
// dropped and counted (see the engine's package doc, Dropping and
// coalescing).
//
// # Closing
//
// Close stops the run (the Session's Abort, once E5 has it), stops every
// child (SIGINT, SIGKILL after the grace; with its process group under
// Deps.Detach) and waits for it to be reaped,
// cancels every in-process step, discovery and prefetch and waits for
// them, hangs up open askpass requests, stops the ticker, the keep-alive
// and the publisher, and then closes Events. Nothing the Driver started
// outlives Close. A Session that panics (until E5 some calls do) closes the
// Driver the same way, from the loop: the panic is reported to
// Config.Report and Events is closed without a Finished.
package driver

import (
	"context"
	"fmt"
	"os/user"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// Deps is what a Driver carries out the Session's Effects with. Priv and
// Log are required; every other zero field has a default.
type Deps struct {
	// Priv is the sudo backend: probe, the two-stage password check, the
	// keep-alive and the command line a step's child runs with. Give it the
	// askpass Server's wrapper (privilege.Sudo{Wrapper: srv.Wrapper()}) when
	// Askpass is set.
	Priv privilege.Privileged
	// Log takes WriteLog and WriteLastRun. Only the Store rotates history,
	// and only on a real run's first write: the Driver writes nothing else.
	Log *logstore.Store

	// KillGrace is how long a stopped child gets between SIGINT and
	// SIGKILL; WaitDelay bounds reading its pipe after it exits
	// (proc.Options). Zero means 5 s and 2 s, the TUI's. A dry run is each
	// step's own (StartStep's Env), not set here.
	KillGrace, WaitDelay time.Duration
	// Detach starts every step's child in a session of its own, without a
	// controlling terminal, and stops it with its process group
	// (proc.Options.Detach): sudo under it can then only ask through
	// Askpass. The GUI sets it; the TUI must not (its steps share its
	// session for the sudo ticket, see internal/proc).
	Detach bool
	// Pty (with Detach) gives every step's child a pseudo-terminal as its
	// output, never as its controlling terminal (proc.Options.Pty): tools
	// colour and draw progress as in a terminal, and sudo still finds no
	// terminal to prompt on. TermCols, read as each child starts, is its
	// width in columns (nil or < 1: 80). Only the GUI sets them; the TUI's
	// children keep their pipes.
	Pty      bool
	TermCols func() int

	// Askpass delivers the askpass Server's requests ((*privilege.Server).
	// Requests); nil means no askpass. The Driver verifies each against the
	// running step's child before the Session sees it, and never closes
	// the Server: its owner does, after Close.
	Askpass <-chan *privilege.Request

	// KeepPassword is the GUI's one password per run (keep.go): the first
	// password the user gives the run is kept (privilege.KeptPassword)
	// and answers the run's later askpass and Agent prompts itself. It is
	// wiped when the run ends, however it ends, and at KeepFor (zero: 60
	// min). The TUI leaves it off.
	KeepPassword bool
	KeepFor      time.Duration
	// Agent delivers polkit's text agent's prompts during helper steps
	// (the GUI's; see AgentPrompt); nil means none. Each is answered from
	// the kept password, or by the user through the Session.
	Agent <-chan AgentPrompt

	// Prefetch starts the named prefetch (steps.Prefetch*) and returns it,
	// or nil for a name it doesn't know. Nil means no prefetching: a step
	// with a Prefetch then runs its own Run.
	Prefetch func(name string) Prefetch

	// Polkit must be set exactly when the Session runs engine.AuthPolkit:
	// a child step with a Verb, outside a dry run, then runs through Helper
	// (pkexec and the root helper) instead of Priv.
	Polkit bool
	// Helper runs those steps (see Helper); nil means helperclient.Run.
	Helper Helper

	// Faillock answers ReadFaillock; nil means helperclient.Faillock for
	// the current user, now.
	Faillock func() engine.FaillockRead
	// Status reads the status line for each Tick; nil means
	// sysstat.Read(sysstat.DefaultPaths()).
	Status func() sysstat.Status

	// TickEvery is the status ticker's period (default 1 s), KeepAliveEvery
	// the sudo ticket refresh's while KeepAlive is on (default 60 s).
	TickEvery, KeepAliveEvery time.Duration
	// FlushEvery is how long output lines are held to coalesce them into
	// one OutputChunk (default 20 ms). MaxLines is how many undelivered
	// lines of one step are kept before the oldest are dropped (default
	// 20000).
	FlushEvery time.Duration
	MaxLines   int

	// RefreshUpdates is called (on a worker, never on the loop) once at
	// the end of a real run whose topgrade step started
	// (engine.RefreshUpdates); its context ends at Close. Both front ends
	// set the package's RefreshUpdates; nil does nothing. An error is one
	// WARN line in the run's topgrade log, never a Report (the TUI prints
	// those on the terminal after it quits); it never fails the run.
	RefreshUpdates func(ctx context.Context) error

	// RunReport, when set, has the Driver make a run report (internal/report)
	// for every real run that reaches its queue: it measures around the
	// cleaning steps, and at the end adds engine.RunReport to the events and
	// the summary object to the last-run record. Nil (the default) means no
	// report: a Driver built for a test must not read the machine's pacman
	// log. See runreport.go.
	RunReport *report.Config

	// Report is told what no Event carries: a log write that failed, a
	// hook's error, a recovered panic. Nil drops them. It may run on the
	// loop and must not call Close.
	Report func(error)
}

// Prefetch is a prefetch started by Deps.Prefetch (the audit's lookup).
type Prefetch interface {
	// Run is the step's result, awaiting the prefetch; ctx is the step's.
	Run(ctx context.Context) steps.Result
	// Cancel abandons it; it then finishes promptly. Safe to call twice.
	Cancel()
	// Done is closed once it has finished.
	Done() <-chan struct{}
}

// Defaults, as the TUI has them.
const (
	defaultKillGrace = 5 * time.Second
	defaultWaitDelay = 2 * time.Second
	defaultTick      = time.Second
	defaultKeepAlive = 60 * time.Second
	defaultFlush     = 20 * time.Millisecond
	defaultMaxLines  = 20000
)

// Driver is the real engine.Driver.
type Driver struct {
	cfg Deps
	s   *engine.Session

	events chan engine.Event
	out    *outbox
	inputs chan msg // from workers, pumps, the ticker, the askpass reader

	mu       sync.Mutex
	cmds     []engine.Command
	closed   bool
	cmdReady chan struct{} // cap 1: Send's wake-up

	quit     chan struct{} // closed by Close: the loop tears down
	quitOnce sync.Once
	stop     chan struct{} // closed by the loop's teardown: everything else ends
	done     chan struct{} // closed once Events is closed
	aborted  chan struct{} // closed once teardown has aborted the run (Aborted)

	// Deps.Detach: every child started, for KillNow, which may run on any
	// goroutine while the loop owns children.
	detachedMu sync.Mutex
	detached   []*proc.Child

	root   context.Context // every worker's; cancelled at teardown
	cancel context.CancelFunc
	// live is root until Close: the run report's measuring gives up at once
	// when the owner is closing the Driver.
	live       context.Context
	cancelLive context.CancelFunc
	wg         sync.WaitGroup // every goroutine but the loop and the publisher
	pubDone    chan struct{}

	// Owned by the loop.
	rep        *report.Collector // the run in progress's report, or nil (runreport.go)
	local      []msg             // Inputs the loop made itself (HookDone)
	children   map[uint64]*proc.Child
	stopped    []*proc.Child                      // Deps.Detach: children stopped, their groups to await at teardown
	lastChild  uint64                             // the newest child still running
	cancels    map[uint64]context.CancelCauseFunc // in-process steps, discoveries, helper steps
	prefetch   map[string]Prefetch                // running prefetches
	asks       map[uint64]asker                   // open askpass and agent requests
	lastAsk    uint64
	childPriv  map[uint64]bool         // running children whose step is NeedsPriv
	helpers    map[uint64]bool         // running helper steps (polkit)
	keep       *privilege.KeptPassword // Deps.KeepPassword; nil otherwise
	keepOffWhy string                  // why the kept password was turned off, for the prompt (NeedsPassword.KeepOff)
	askedBy    map[sudoKey]bool        // sudo processes that asked this run
	keepAlive  chan struct{}           // closed to stop the refresh; nil when off
}

// msg is what reaches the loop besides Commands: an Input, or an askpass
// request still to verify.
type msg struct {
	in    engine.Input
	ask   *privilege.Request
	agent AgentPrompt
	// noKeep: the kept password was tried for this request and failed:
	// it goes to the Session.
	noKeep bool
}

// hangUp ends what m carries unanswered, for a Driver that has stopped.
func (m msg) hangUp() {
	if m.ask != nil {
		m.ask.Cancel()
	}
	if m.agent != nil {
		m.agent.Cancel()
	}
}

var _ engine.Driver = (*Driver)(nil)

// New starts a Driver around s, which it owns from now on: nothing else
// may call s. Its ticker starts at once. It panics without s, cfg.Priv or
// cfg.Log.
func New(s *engine.Session, cfg Deps) *Driver {
	if s == nil || cfg.Priv == nil || cfg.Log == nil {
		panic("driver: New needs a Session, Deps.Priv and Deps.Log")
	}
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&cfg.KillGrace, defaultKillGrace)
	def(&cfg.WaitDelay, defaultWaitDelay)
	def(&cfg.TickEvery, defaultTick)
	def(&cfg.KeepAliveEvery, defaultKeepAlive)
	def(&cfg.FlushEvery, defaultFlush)
	if cfg.MaxLines <= 0 {
		cfg.MaxLines = defaultMaxLines
	}
	if cfg.Faillock == nil {
		cfg.Faillock = currentFaillock
	}
	if cfg.Helper == nil {
		cfg.Helper = helperclient.Run
	}
	if cfg.Status == nil {
		cfg.Status = func() sysstat.Status { return sysstat.Read(sysstat.DefaultPaths()) }
	}
	root, cancel := context.WithCancel(context.Background())
	live, cancelLive := context.WithCancel(root)
	d := &Driver{
		cfg:        cfg,
		s:          s,
		events:     make(chan engine.Event),
		out:        newOutbox(cfg.FlushEvery, cfg.MaxLines),
		inputs:     make(chan msg, 64),
		cmdReady:   make(chan struct{}, 1),
		quit:       make(chan struct{}),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		aborted:    make(chan struct{}),
		root:       root,
		cancel:     cancel,
		live:       live,
		cancelLive: cancelLive,
		pubDone:    make(chan struct{}),
		children:   map[uint64]*proc.Child{},
		cancels:    map[uint64]context.CancelCauseFunc{},
		prefetch:   map[string]Prefetch{},
		asks:       map[uint64]asker{},
		childPriv:  map[uint64]bool{},
		helpers:    map[uint64]bool{},
		askedBy:    map[sudoKey]bool{},
	}
	if cfg.KeepPassword {
		keepFor := cfg.KeepFor
		if keepFor <= 0 {
			keepFor = defaultKeepFor
		}
		d.keep = privilege.NewKeptPassword(keepFor)
	}
	go func() {
		defer close(d.pubDone)
		d.out.run(d.events, d.stop)
	}()
	d.goRun(d.ticker)
	if cfg.Askpass != nil {
		d.goRun(d.readAskpass)
	}
	if cfg.Agent != nil {
		d.goRun(d.readAgent)
	}
	go d.loop()
	return d
}

// currentFaillock is the current user's pam_faillock count, now.
func currentFaillock() engine.FaillockRead {
	u, err := user.Current()
	if err != nil {
		return engine.FaillockRead{Err: err}
	}
	return helperclient.Faillock(u.Username, time.Now())
}

// Events delivers the Session's Events in order (output coalesced, see the
// package doc); it is closed once Close has returned.
func (d *Driver) Events() <-chan engine.Event { return d.events }

// Send queues c for the Session and never blocks. Commands have a queue of
// their own, apart from Inputs and Events, with no bound: it returns false
// only once the Driver is closed (Close, or a Session panic), as the
// engine's package doc (Drivers) asks.
func (d *Driver) Send(c engine.Command) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || c == nil {
		return false
	}
	d.cmds = append(d.cmds, c)
	select {
	case d.cmdReady <- struct{}{}:
	default:
	}
	return true
}

// Aborted is closed once Close (or a Session panic) has begun the
// teardown and, for a Close, aborted the run: its last-run record is
// written (WriteLastRun runs on the loop, to completion), while stopping
// the children may still take up to KillGrace. A front end that must exit
// at once can wait for it so the record isn't lost.
func (d *Driver) Aborted() <-chan struct{} { return d.aborted }

// KillNow SIGKILLs, at once and with no grace, the process group of every
// child the Driver started under Deps.Detach: what Close is still
// stopping, and whatever a step that already ended left running. A group
// already gone is never signalled (proc.Child.KillGroup). It is for a
// front end about to exit without waiting for Close (archmaid-gui's
// second signal, after Aborted): nothing a run started may outlive it.
// Safe from any goroutine; without Detach it does nothing.
func (d *Driver) KillNow() {
	d.keep.Wipe() // the GUI exits now: nothing of the run's password stays
	d.detachedMu.Lock()
	kids := slices.Clone(d.detached)
	d.detachedMu.Unlock()
	for _, c := range kids {
		c.KillGroup()
	}
}

// Close stops the run and everything the Driver started, then closes
// Events (see the package doc). Safe to call more than once and from any
// goroutine; every call returns once it is all done.
func (d *Driver) Close() {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	d.quitOnce.Do(func() {
		close(d.quit)
		d.cancelLive()
	})
	<-d.done
}

// goRun runs f on a goroutine Close waits for.
func (d *Driver) goRun(f func()) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		f()
	}()
}

// post hands m to the loop; once the Driver stops it is dropped (and an
// askpass request in it hung up).
func (d *Driver) post(m msg) {
	select {
	case d.inputs <- m:
	case <-d.stop:
		m.hangUp()
	}
}

// work runs f on a worker and posts its Input, if any. A panic in f is
// reported and turned into nothing: the Session then waits, as for an
// Input that never came, until the run is aborted or closed.
func (d *Driver) work(f func() engine.Input) {
	d.goRun(func() {
		var in engine.Input
		func() {
			defer d.recoverTo("a worker", nil)
			in = f()
		}()
		if in != nil {
			d.post(msg{in: in})
		}
	})
}

// recoverTo reports a panic in what; with failed set, it is also set true.
func (d *Driver) recoverTo(what string, failed *bool) {
	if r := recover(); r != nil {
		d.report(fmt.Errorf("driver: %s panicked: %v\n%s", what, r, debug.Stack()))
		if failed != nil {
			*failed = true
		}
	}
}

func (d *Driver) report(err error) {
	if err != nil && d.cfg.Report != nil {
		d.cfg.Report(err)
	}
}

// ticker is the one status ticker: a Tick, freshly read, every TickEvery.
func (d *Driver) ticker() {
	t := time.NewTicker(d.cfg.TickEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			d.post(msg{in: engine.Tick{Status: d.cfg.Status()}})
		case <-d.stop:
			return
		}
	}
}

// readAskpass passes the Server's requests to the loop, which verifies them.
func (d *Driver) readAskpass() {
	for {
		select {
		case r, ok := <-d.cfg.Askpass:
			if !ok {
				return
			}
			if r != nil {
				d.post(msg{ask: r})
			}
		case <-d.stop:
			return
		}
	}
}

// loop owns the Session until Close or a Session panic, then tears down.
func (d *Driver) loop() {
	defer close(d.done)
	for {
		if len(d.local) > 0 {
			m := d.local[0]
			d.local = d.local[1:]
			if !d.handle(m) {
				d.teardown(false)
				return
			}
			continue
		}
		select {
		case <-d.quit:
			d.teardown(true)
			return
		case <-d.cmdReady:
			d.mu.Lock()
			cmds := d.cmds
			d.cmds = nil
			d.mu.Unlock()
			for _, c := range cmds {
				if !d.command(c) {
					d.teardown(false)
					return
				}
			}
		case m := <-d.inputs:
			if !d.handle(m) {
				d.teardown(false)
				return
			}
		}
	}
}

// call runs one Session call, recovering a panic (false: the Driver must
// close).
func (d *Driver) call(f func() engine.Output) (o engine.Output, ok bool) {
	failed := false
	func() {
		defer d.recoverTo("the engine", &failed)
		o = f()
	}()
	return o, !failed
}

// command applies c; false when the Session panicked.
func (d *Driver) command(c engine.Command) bool {
	s := d.s
	o, ok := d.call(func() engine.Output {
		switch c := c.(type) {
		case engine.Start:
			return s.Start(c.Action)
		case engine.StartPackages:
			return s.StartPackages(c.Req)
		case engine.Confirm:
			return s.Confirm(c.Prompt, c.Yes)
		case engine.SubmitPassword:
			return s.SubmitPassword(c.Prompt, c.Password)
		case engine.CancelPrompt:
			return s.CancelPrompt(c.Prompt)
		case engine.Select:
			return s.Select(c.Prompt, c.Picked)
		case engine.Abort:
			return s.Abort()
		}
		return engine.Output{}
	})
	if ok {
		d.apply(o)
	}
	return ok
}

// handle feeds the Session one Input (an askpass request verified first),
// carries out what it answers, and does the Driver's own bookkeeping for
// it; false when the Session panicked.
func (d *Driver) handle(m msg) bool {
	in := m.in
	var done bool
	switch {
	case m.ask != nil:
		in, done = d.askpassRequest(m)
	case m.agent != nil:
		in, done = d.agentRequest(m)
	}
	if done {
		return true // answered or hung up by the Driver: nothing for the Session
	}
	switch in := in.(type) {
	case engine.StepExited:
		delete(d.children, in.Seq) // reaped
		delete(d.childPriv, in.Seq)
		delete(d.helpers, in.Seq)
		d.release(in.Seq, steps.ErrResultTaken)
	case engine.Discovered:
		d.release(in.Seq, steps.ErrResultTaken)
	case engine.AskpassAnswered:
		if in.OK {
			delete(d.asks, in.Req) // answered: the request is over
		}
	}
	if r, isResult := in.(engine.StepResult); isResult {
		d.reportStepResult(r.Result)
	}
	o, ok := d.call(func() engine.Output { return d.s.Handle(in) })
	if !ok {
		return false
	}
	d.apply(o)
	if r, isResult := in.(engine.StepResult); isResult {
		// Taken, and that Output's Effects carried out: release the step's
		// ctx saying so (engine.StartStep).
		d.release(r.Seq, steps.ErrResultTaken)
	}
	return true
}

// release cancels the ctx of what Effect seq started, with cause.
func (d *Driver) release(seq uint64, cause error) {
	if cancel := d.cancels[seq]; cancel != nil {
		cancel(cause)
		delete(d.cancels, seq)
	}
}

// verify names an askpass request for the Session: its peer checked
// against the running step's child (privilege.Request.Verify), Refused
// saying why when it fails (ok false). from is who asked, when verified.
func (d *Driver) verify(r *privilege.Request) (in engine.Input, from privilege.Requester, ok bool) {
	d.lastAsk++
	d.asks[d.lastAsk] = sudoAsk{r}
	req := engine.AskpassRequest{Req: d.lastAsk, Prompt: r.Prompt}
	c := d.children[d.lastChild]
	if c == nil {
		req.Refused = "no step's child is running"
		return req, from, false
	}
	from, err := r.Verify(c.Pid, c.Start)
	if err != nil {
		req.Refused = err.Error()
		return req, from, false
	}
	req.Requester = requesterOf(from)
	return req, from, true
}

// apply starts o's Effects in order, then publishes its Events. A Finished
// ends the run, and with it the kept password (keep.go).
//
// With a run report configured (Deps.RunReport) it also watches the run
// (runreport.go): the steps starting in o's Effects and ending in its
// Events are measured around, and the Output that ends the run is split at
// Finished: the events before it go out first, then the report is made
// (bounded), the last-run record is written with its summary, and
// RunReport and Finished follow.
func (d *Driver) apply(o engine.Output) {
	d.reportBegin(o)
	finishing := d.rep != nil && slices.ContainsFunc(o.Events, func(ev engine.Event) bool {
		_, ok := ev.(engine.Finished)
		return ok
	})
	var lastRun *engine.WriteLastRun
	written := map[logstore.Category]bool{} // logs this Output's WriteLogs wrote
	for _, e := range o.Effects {
		switch e := e.(type) {
		case engine.WriteLog:
			written[e.Category] = d.writeLog(e)
			continue
		case engine.StartStep:
			d.reportStarting(e)
		case engine.WriteLastRun:
			if finishing {
				lastRun = &e // written once the report is made
				continue
			}
		}
		d.effect(e)
	}
	for i, ev := range o.Events {
		switch ev := ev.(type) {
		case engine.NeedsPassword:
			// Whether the kept password will answer is the Driver's to
			// say (keep.go), read now, when the prompt goes out.
			if d.cfg.KeepPassword {
				ev.Keeps = !d.keep.Spent()
				if !ev.Keeps {
					ev.KeepOff = d.keepOffWhy
				}
				o.Events[i] = ev
			}
		case engine.StepDone:
			d.reportStepDone(ev.ID, ev.Status)
		case engine.Aborted:
			d.reportStepDone(ev.ID, steps.Aborted)
		case engine.Finished:
			d.keep.Wipe()
			if finishing {
				d.out.push(o.Events[:i]) // the last steps show while the report is made
				rep := d.reportFinish(ev)
				if lastRun != nil {
					d.writeLastRun(*lastRun, rep)
				}
				rest := o.Events[i:]
				if rep != nil {
					rest = append([]engine.Event{engine.RunReport{Report: *rep}}, rest...)
				}
				d.out.push(rest)
				return
			}
		case engine.DiscoveryResult:
			// The record went out with this Output (engine.DiscoveryResult):
			// say where, once it is on disk.
			if ev.Category != "" && written[ev.Category] {
				ev.LogPath = d.cfg.Log.Path(ev.Category)
				o.Events[i] = ev
			}
		}
	}
	d.out.push(o.Events)
}

// teardown ends everything (see the package doc, Closing). asked is a
// Close: the run is aborted first, as Abort would.
func (d *Driver) teardown(asked bool) {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	if asked {
		if o, ok := d.call(d.s.Abort); ok {
			d.apply(o)
		}
	}
	close(d.aborted) // the abort's last-run record is written
	close(d.stop)
	d.cancel()
	for seq, c := range d.children {
		d.stopChild(c) // its pump runs on to the done: the child is reaped
		delete(d.children, seq)
	}
	for seq, cancel := range d.cancels {
		cancel(nil) // plain context.Canceled: an abort
		delete(d.cancels, seq)
	}
	for name := range d.prefetch {
		d.cancelPrefetch(name)
	}
	for id, a := range d.asks {
		a.cancel()
		delete(d.asks, id)
	}
	d.keep.Wipe() // the run is over
	d.setKeepAlive(false)
	d.wg.Wait()
	// A detached child's group outlives the child until its Stop has
	// SIGKILLed what is left of it, at most KillGrace after the Stop.
	for _, c := range d.stopped {
		<-c.GroupStopped()
	}
	// Every poster has returned; what they queued and the loop never took
	// is dropped, and an askpass request among it hung up on.
	for {
		select {
		case m := <-d.inputs:
			m.hangUp()
			continue
		default:
		}
		break
	}
	<-d.pubDone
	close(d.events)
}
