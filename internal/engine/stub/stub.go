// Package stub is an engine.Driver that replays a scripted Event sequence
// instead of running anything: the GUI builds against it until the real
// Driver lands (E6). It keeps the Driver contract — Send never blocks,
// events arrive in order, prompts wait for their answer and ignore stale
// IDs, Abort ends the run with Aborted and Finished. It models what an
// answer changes only where a script says so: a Cue's OnNo, OnCancel and
// OnWrong branches play instead of the rest of the script, and without them
// every answer plays on like "yes". A password is compared with
// WrongPassword and dropped, never kept.
//
// Pacing comes from a Clock: the wall clock for a GUI to watch, or a
// FakeClock that jumps ahead instead of sleeping, for tests.
package stub

import (
	"sync"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
)

// Cue is one scripted event: wait After on the driver's clock, then
// publish Event. An event that opens a prompt (NeedsConfirm, NeedsPassword,
// NeedsSelection, OutputReport with a non-zero ID) holds the script until
// that prompt is answered or cancelled.
//
// A prompt's cue may carry branches. When the prompt is answered no (a
// Confirm with Yes false, or a Select that picked nothing), OnNo plays
// instead of the rest of the script; when it is cancelled (CancelPrompt),
// OnCancel does, or OnNo when OnCancel is nil (the engine reads a cancel as
// a no). A nil branch means "continue", so a script without branches plays
// one path. A branch is a whole ending: it must end the run (Finished, then
// PhaseChanged idle), carrying the tail of the script itself when the run
// goes on, and its own cues may carry branches. Branches on a cue that
// opens no prompt are never played.
type Cue struct {
	After    time.Duration
	Event    engine.Event
	OnNo     Script
	OnCancel Script
	// OnWrong plays instead of the rest of the script when a password
	// prompt is answered with the Secret WrongPassword; without it every
	// password plays on as right.
	OnWrong Script
}

// WrongPassword is the one Secret the stub calls wrong: it can't check a
// real password, so a SubmitPassword carrying exactly this plays a Cue's
// OnWrong, and any other plays on as right. The Driver compares the Secret
// and drops it: it is never stored, logged or put in an Event.
const WrongPassword = "wrong"

// Script is a run, played from the top on every Start.
type Script []Cue

// Events is the script's events alone, in order.
func (s Script) Events() []engine.Event {
	out := make([]engine.Event, len(s))
	for i, c := range s {
		out[i] = c.Event
	}
	return out
}

// Duration is the script's total scripted wait.
func (s Script) Duration() time.Duration {
	var d time.Duration
	for _, c := range s {
		d += c.After
	}
	return d
}

// Clock paces the replay.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time                         { return time.Now() }
func (wallClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// FakeClock is a Clock that never sleeps: After moves it forward by d and
// fires at once, so a script plays instantly while Now still reads its
// scripted time.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock is a FakeClock reading start.
func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

// Now is the fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After advances the clock by d and returns a channel that has already
// fired.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- now
	return ch
}

// Options configure a Driver.
type Options struct {
	Clock Clock // nil means the wall clock
	// Queue is how many Commands Send can hold before it returns false;
	// 0 means 16.
	Queue int
	// Scripts, when non-nil, replaces the single script: Start picks the
	// script named by its Action, and an action with none ends in a Notice
	// and Finished{OutcomeNotice}.
	Scripts map[string]Script
}

// Driver replays a Script. It implements engine.Driver.
type Driver struct {
	script  Script
	scripts map[string]Script
	clock   Clock
	events  chan engine.Event
	cmds    chan engine.Command
	done    chan struct{}
	exited  chan struct{}
	once    sync.Once
}

var _ engine.Driver = (*Driver)(nil)

// New starts a Driver that plays script on every engine.Start it is sent
// (or, with opts.Scripts, the script the Start names), one at a time: a
// Start while it plays is ignored. It idles otherwise.
func New(script Script, opts Options) *Driver {
	if opts.Clock == nil {
		opts.Clock = wallClock{}
	}
	if opts.Queue <= 0 {
		opts.Queue = 16
	}
	d := &Driver{
		script:  script,
		scripts: opts.Scripts,
		clock:   opts.Clock,
		events:  make(chan engine.Event),
		cmds:    make(chan engine.Command, opts.Queue),
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
	}
	go d.loop()
	return d
}

// Events is the event stream, closed once Close has returned.
func (d *Driver) Events() <-chan engine.Event { return d.events }

// Send queues c without blocking: false when the Driver is closed or its
// queue is full.
func (d *Driver) Send(c engine.Command) bool {
	select {
	case <-d.done:
		return false
	default:
	}
	select {
	case d.cmds <- c:
		return true
	default:
		return false
	}
}

// Close stops the replay and waits for it; safe to call more than once.
func (d *Driver) Close() {
	d.once.Do(func() { close(d.done) })
	<-d.exited
}

func (d *Driver) loop() {
	defer close(d.exited)
	defer close(d.events)
	for {
		var action string
		for action == "" {
			select {
			case <-d.done:
				return
			case c := <-d.cmds:
				switch c := c.(type) {
				case engine.Start:
					action = c.Action
				case engine.StartPackages:
					action = PackagesKey(c.Req)
				}
			}
		}
		if !d.play(action) {
			return
		}
	}
}

// run is one replay's state.
type run struct {
	action  string
	started time.Time
	ran     bool                // a step has started
	step    *engine.StepStarted // the running step, or nil
	since   time.Time           // when it started, on the clock
}

// scriptFor is the script a Start for action plays.
func (d *Driver) scriptFor(action string) Script {
	if d.scripts == nil {
		return d.script
	}
	if s, ok := d.scripts[action]; ok {
		return s
	}
	return Script{
		{Event: engine.DiscoveryResult{Action: action, Outcome: engine.DiscoveryFailed,
			Summary: "The stub has no script for " + action + ".", Err: "no script"}},
		{Event: engine.Notice{Text: "The stub has no script for " + action + "."}},
		{Event: engine.Finished{Action: action, Outcome: engine.OutcomeNotice}},
		{Event: engine.PhaseChanged{Phase: engine.PhaseIdle}},
	}
}

// play replays the script for action once; false when the Driver was
// closed.
func (d *Driver) play(action string) bool {
	return d.playScript(&run{action: action, started: d.clock.Now()}, d.scriptFor(action))
}

// playScript plays script, or the branch an answer picks in its place.
func (d *Driver) playScript(r *run, script Script) bool {
	for _, cue := range script {
		if cue.After > 0 {
			fired := d.clock.After(cue.After)
		wait:
			for {
				select {
				case <-d.done:
					return false
				case <-fired:
					break wait
				case c := <-d.cmds:
					if _, ok := c.(engine.Abort); ok {
						return d.abort(r, 0)
					}
				}
			}
		}
		if !d.publish(cue.Event) {
			return false
		}
		switch ev := cue.Event.(type) {
		case engine.StepStarted:
			r.ran, r.step, r.since = true, &ev, d.clock.Now()
		case engine.StepDone, engine.Aborted:
			r.step = nil
		}
		if id := promptID(cue.Event); id != 0 {
			ans, how := d.await(id)
			switch ans {
			case closed:
				return false
			case aborted:
				return d.abort(r, id)
			}
			branch := cue.OnNo
			switch how {
			case yes:
				branch = nil
			case cancelled:
				if cue.OnCancel != nil {
					branch = cue.OnCancel
				}
			case wrong:
				branch = cue.OnWrong
			}
			if branch != nil {
				return d.playScript(r, branch)
			}
		}
	}
	return true
}

type answer int

const (
	answered answer = iota
	aborted
	closed
)

// how an open prompt was answered.
type how int

const (
	yes       how = iota // a yes, a password or a pick
	no                   // a no, or nothing picked
	cancelled            // CancelPrompt
	wrong                // a SubmitPassword of WrongPassword
)

// await holds the script until prompt id is answered, ignoring commands
// for any other prompt.
func (d *Driver) await(id engine.PromptID) (answer, how) {
	for {
		select {
		case <-d.done:
			return closed, yes
		case c := <-d.cmds:
			switch c := c.(type) {
			case engine.Abort:
				return aborted, yes
			case engine.Confirm:
				if c.Prompt == id {
					if c.Yes {
						return answered, yes
					}
					return answered, no
				}
			case engine.SubmitPassword:
				if c.Prompt == id {
					if string(c.Password) == WrongPassword {
						return answered, wrong
					}
					return answered, yes
				}
			case engine.Select:
				if c.Prompt == id {
					if len(c.Picked) == 0 {
						return answered, no
					}
					return answered, yes
				}
			case engine.CancelPrompt:
				if c.Prompt == id {
					return answered, cancelled
				}
			}
		}
	}
}

// abort ends the replay as the engine would: the open prompt withdrawn,
// the running step Aborted, then Finished.
func (d *Driver) abort(r *run, open engine.PromptID) bool {
	var evs []engine.Event
	if open != 0 {
		evs = append(evs, engine.PromptClosed{ID: open})
	}
	if r.step != nil {
		evs = append(evs, engine.Aborted{ID: r.step.ID, Index: r.step.Index, Elapsed: d.clock.Now().Sub(r.since)})
	}
	outcome := engine.OutcomeCancelled
	if r.ran {
		outcome = engine.OutcomeAborted
	}
	evs = append(evs,
		engine.Finished{Action: r.action, Outcome: outcome, Elapsed: d.clock.Now().Sub(r.started)},
		engine.PhaseChanged{Phase: engine.PhaseIdle})
	for _, ev := range evs {
		if !d.publish(ev) {
			return false
		}
	}
	return true
}

func (d *Driver) publish(ev engine.Event) bool {
	select {
	case d.events <- ev:
		return true
	case <-d.done:
		return false
	}
}

// promptID is the prompt ev opens, or 0.
func promptID(ev engine.Event) engine.PromptID {
	switch ev := ev.(type) {
	case engine.NeedsConfirm:
		return ev.ID
	case engine.NeedsPassword:
		return ev.ID
	case engine.NeedsSelection:
		return ev.ID
	case engine.OutputReport:
		return ev.ID
	}
	return 0
}
