// Package stub is an engine.Driver that replays a scripted Event sequence
// instead of running anything: the GUI builds against it until the real
// Driver lands (E6). It keeps the Driver contract — Send never blocks,
// events arrive in order, prompts wait for their answer and ignore stale
// IDs, Abort ends the run with Aborted and Finished — but it doesn't model
// what an answer changes: a script is one path, and "no" plays it on like
// "yes". Passwords sent to it are dropped unread.
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
type Cue struct {
	After time.Duration
	Event engine.Event
}

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
}

// Driver replays a Script. It implements engine.Driver.
type Driver struct {
	script Script
	clock  Clock
	events chan engine.Event
	cmds   chan engine.Command
	done   chan struct{}
	exited chan struct{}
	once   sync.Once
}

var _ engine.Driver = (*Driver)(nil)

// New starts a Driver that plays script on every engine.Start it is sent
// (one at a time: a Start while it plays is ignored), and idles otherwise.
func New(script Script, opts Options) *Driver {
	if opts.Clock == nil {
		opts.Clock = wallClock{}
	}
	if opts.Queue <= 0 {
		opts.Queue = 16
	}
	d := &Driver{
		script: script,
		clock:  opts.Clock,
		events: make(chan engine.Event),
		cmds:   make(chan engine.Command, opts.Queue),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
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
				if s, ok := c.(engine.Start); ok && s.Action != "" {
					action = s.Action
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

// play replays the script once; false when the Driver was closed.
func (d *Driver) play(action string) bool {
	r := &run{action: action, started: d.clock.Now()}
	for _, cue := range d.script {
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
			switch d.await(id) {
			case closed:
				return false
			case aborted:
				return d.abort(r, id)
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

// await holds the script until prompt id is answered, ignoring commands
// for any other prompt.
func (d *Driver) await(id engine.PromptID) answer {
	for {
		select {
		case <-d.done:
			return closed
		case c := <-d.cmds:
			switch c := c.(type) {
			case engine.Abort:
				return aborted
			case engine.Confirm:
				if c.Prompt == id {
					return answered
				}
			case engine.SubmitPassword:
				if c.Prompt == id {
					return answered
				}
			case engine.Select:
				if c.Prompt == id {
					return answered
				}
			case engine.CancelPrompt:
				if c.Prompt == id {
					return answered
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
