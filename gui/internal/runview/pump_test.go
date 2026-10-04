package runview

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/stub"
)

// loop stands in for the GTK main loop: one goroutine runs posted funcs in
// order, and post never blocks (like g_idle_add).
type loop struct {
	mu    sync.Mutex
	q     []func()
	wake  chan struct{}
	stop  chan struct{}
	done  chan struct{}
	owner atomic.Int64 // the running func's turn, to catch work off the loop
}

func newLoop() *loop {
	l := &loop{wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go l.run()
	return l
}

func (l *loop) post(f func()) {
	l.mu.Lock()
	l.q = append(l.q, f)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// do runs f on the loop and waits for it.
func (l *loop) do(f func()) {
	ran := make(chan struct{})
	l.post(func() { f(); close(ran) })
	<-ran
}

func (l *loop) run() {
	defer close(l.done)
	for {
		l.mu.Lock()
		q := l.q
		l.q = nil
		l.mu.Unlock()
		for _, f := range q {
			l.owner.Add(1)
			f()
			l.owner.Add(1)
		}
		if len(q) > 0 {
			continue
		}
		select {
		case <-l.wake:
		case <-l.stop:
			return
		}
	}
}

func (l *loop) close() { close(l.stop); <-l.done }

// onLoop reports whether a func is running on the loop right now.
func (l *loop) onLoop() bool { return l.owner.Load()%2 == 1 }

// collector applies batches on the loop, answering every confirm yes, and
// signals when Finished arrives.
type collector struct {
	t        *testing.T
	l        *loop
	d        engine.Driver
	m        *Model
	evs      []engine.Event
	batches  int
	maxLines int
	finished chan struct{}
}

func newCollector(t *testing.T, l *loop, d engine.Driver) *collector {
	return &collector{t: t, l: l, d: d, m: NewModel(), finished: make(chan struct{})}
}

func (c *collector) apply(evs []engine.Event) {
	if !c.l.onLoop() {
		c.t.Error("apply ran off the main loop")
	}
	c.batches++
	lines := 0
	for _, ev := range evs {
		if ch, ok := ev.(engine.OutputChunk); ok {
			lines += len(ch.Lines)
		}
	}
	c.maxLines = max(c.maxLines, lines)
	c.evs = append(c.evs, evs...)
	ch := c.m.Apply(evs)
	if ch.Opened != nil {
		c.m.Answered(ch.Opened.ID)
		if !c.d.Send(engine.Confirm{Prompt: ch.Opened.ID, Yes: true}) {
			c.t.Error("Send(Confirm) = false")
		}
	}
	if ch.Finished {
		close(c.finished)
	}
}

func (c *collector) wait(t *testing.T) {
	t.Helper()
	select {
	case <-c.finished:
	case <-time.After(10 * time.Second):
		t.Fatal("no Finished within 10s")
	}
}

// unmerge splits merged OutputChunks back into one line per chunk, so a
// pumped stream compares with the script line by line.
func unmerge(evs []engine.Event) []engine.Event {
	var out []engine.Event
	for _, ev := range evs {
		ch, ok := ev.(engine.OutputChunk)
		if !ok {
			out = append(out, ev)
			continue
		}
		for _, l := range ch.Lines {
			out = append(out, engine.OutputChunk{ID: ch.ID, Index: ch.Index, Lines: []string{l}})
		}
	}
	return out
}

func TestPumpDeliversFullCleanupInOrder(t *testing.T) {
	defer goleak.VerifyNone(t)
	l := newLoop()
	defer l.close()
	script := stub.FullCleanup(t0)
	d := stub.New(script, stub.Options{Clock: stub.NewFakeClock(t0)})
	c := newCollector(t, l, d)
	p := NewPump(d, l.post, c.apply, PumpOptions{})
	d.Send(engine.Start{Action: "fullcleanup"})
	c.wait(t)
	d.Close()
	<-p.Done()
	l.do(func() {}) // let the last flush finish

	if got, want := unmerge(c.evs), unmerge(script.Events()); !reflect.DeepEqual(got, want) {
		t.Fatalf("pumped events differ from the script:\n got %v\nwant %v", got, want)
	}
	if s := p.Stats(); s.Dropped != 0 || s.Flushes != c.batches || s.Flushes == 0 {
		t.Errorf("stats %+v (batches %d)", s, c.batches)
	}
}

// TestPumpFloodNoDrop: with dropping off, every line of a 50k flood reaches
// the model, in slices of at most FlushLines.
func TestPumpFloodNoDrop(t *testing.T) {
	defer goleak.VerifyNone(t)
	const n = 50000
	l := newLoop()
	defer l.close()
	d := stub.New(Flood(t0, n, 1, 0), stub.Options{Clock: stub.NewFakeClock(t0)})
	c := newCollector(t, l, d)
	p := NewPump(d, l.post, c.apply, PumpOptions{MaxPending: -1, FlushLines: 1000})
	d.Send(engine.Start{Action: "flood"})
	c.wait(t)
	d.Close()
	<-p.Done()
	l.do(func() {})

	s := p.Stats()
	if s.Lines != n || s.Dropped != 0 || c.m.Lines != n || c.m.Dropped != 0 {
		t.Fatalf("applied %d (model saw %d), dropped %d/%d; want %d and none", s.Lines, c.m.Lines, s.Dropped, c.m.Dropped, n)
	}
	if c.maxLines > 1000 {
		t.Errorf("a flush applied %d lines, over FlushLines 1000", c.maxLines)
	}
	out := c.m.Output()
	if len(out) != OutputCap || out[len(out)-1] != FloodLine(n, n) || out[0] != FloodLine(n-OutputCap+1, n) {
		t.Errorf("view holds %d lines, %q … %q", len(out), out[0], out[len(out)-1])
	}
	if s.P95() <= 0 || s.Max() < s.P95() || s.FirstLine.After(s.LastLine) {
		t.Errorf("stats %v %v %v %v", s.P95(), s.Max(), s.FirstLine, s.LastLine)
	}
	t.Logf("50k lines: %d flushes, p95 %v, max %v (apply only, no widgets)", s.Flushes, s.P95(), s.Max())
}

// TestPumpDropsOldestWhenBehind: a main loop that is stalled while a flood
// arrives sees at most MaxPending lines, the newest, with the drop counted.
func TestPumpDropsOldestWhenBehind(t *testing.T) {
	defer goleak.VerifyNone(t)
	const n = 5000
	l := newLoop()
	defer l.close()
	d := stub.New(Flood(t0, n, 7, 0), stub.Options{Clock: stub.NewFakeClock(t0)})
	c := newCollector(t, l, d)
	p := NewPump(d, l.post, c.apply, PumpOptions{MaxPending: 100})

	stall, stalled := make(chan struct{}), make(chan struct{})
	l.post(func() { close(stalled); <-stall })
	<-stalled
	d.Send(engine.Start{Action: "flood"})
	// The control events after the flood (StepDone, Finished) prove the
	// whole script was queued behind the stall.
	waitFor(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		n := len(p.queue)
		return n > 0 && reflect.DeepEqual(p.queue[n-1], engine.PhaseChanged{Phase: engine.PhaseIdle})
	})
	close(stall)
	c.wait(t)
	d.Close()
	<-p.Done()
	l.do(func() {})

	s := p.Stats()
	if s.Lines != 100 || s.Dropped != n-100 || c.m.Dropped != n-100 || c.m.Lines != n {
		t.Fatalf("applied %d, pump dropped %d, model dropped %d of %d seen; want 100, %d", s.Lines, s.Dropped, c.m.Dropped, c.m.Lines, n-100)
	}
	out := c.m.Output()
	if out[len(out)-1] != FloodLine(n, n) || out[1] != fmt.Sprintf("… %d lines not shown (they are in the log)", n-100) {
		t.Errorf("view %q … %q", out[1], out[len(out)-1])
	}
	for _, ev := range c.evs {
		if _, ok := ev.(engine.Finished); ok {
			return
		}
	}
	t.Error("Finished was dropped")
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestStopLeavesNothingRunning: Abort mid-run, then Close: the run ends with
// Aborted and Finished, the pump's goroutine returns and nothing leaks.
func TestStopLeavesNothingRunning(t *testing.T) {
	defer goleak.VerifyNone(t)
	l := newLoop()
	defer l.close()
	// The wall clock, so the run is still in topgrade when Abort lands.
	d := stub.New(stub.FullCleanup(time.Now()), stub.Options{})
	c := newCollector(t, l, d)
	started := make(chan struct{})
	var once sync.Once
	apply := func(evs []engine.Event) {
		c.apply(evs)
		for _, ev := range evs {
			if s, ok := ev.(engine.StepStarted); ok && s.Index == 1 {
				once.Do(func() { close(started) })
			}
		}
	}
	p := NewPump(d, l.post, apply, PumpOptions{})
	d.Send(engine.Start{Action: "fullcleanup"})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("topgrade never started")
	}
	if !d.Send(engine.Abort{}) {
		t.Fatal("Send(Abort) = false")
	}
	c.wait(t)
	d.Close()
	<-p.Done()
	l.do(func() {
		if f := c.m.Finished; f == nil || f.Outcome != engine.OutcomeAborted {
			t.Errorf("finished %+v, want aborted", f)
		}
		if c.m.Rows[1].State != RowAborted {
			t.Errorf("topgrade row %v, want aborted", c.m.Rows[1].State)
		}
	})
}

// TestCloseWithoutAbort: closing the Driver mid-run ends the stream and
// the pump without a Finished and without leaks.
func TestCloseWithoutAbort(t *testing.T) {
	defer goleak.VerifyNone(t)
	l := newLoop()
	defer l.close()
	d := stub.New(stub.FullCleanup(time.Now()), stub.Options{})
	p := NewPump(d, l.post, func([]engine.Event) {}, PumpOptions{})
	d.Send(engine.Start{Action: "fullcleanup"})
	d.Close()
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("pump still running after Close")
	}
	l.do(func() {})
}

func TestTakeSplitsAtTheBudget(t *testing.T) {
	p := &Pump{opts: PumpOptions{FlushLines: 3, MaxPending: -1}}
	p.add(engine.OutputChunk{ID: "a", Lines: []string{"1", "2"}})
	p.add(engine.OutputChunk{ID: "a", Lines: []string{"3", "4"}}) // merged
	p.add(engine.StepDone{ID: "a"})
	p.add(engine.OutputChunk{ID: "b", Lines: []string{"5"}})
	if len(p.queue) != 3 {
		t.Fatalf("queue %v, want the two a-chunks merged", p.queue)
	}
	b, n := p.take()
	if n != 3 || !reflect.DeepEqual(b, []engine.Event{engine.OutputChunk{ID: "a", Lines: []string{"1", "2", "3"}}}) {
		t.Fatalf("first take %v (%d lines)", b, n)
	}
	b, n = p.take()
	want := []engine.Event{engine.OutputChunk{ID: "a", Lines: []string{"4"}}, engine.StepDone{ID: "a"}, engine.OutputChunk{ID: "b", Lines: []string{"5"}}}
	if n != 2 || !reflect.DeepEqual(b, want) || p.lines != 0 || len(p.queue) != 0 {
		t.Fatalf("second take %v (%d lines), left %v/%d", b, n, p.queue, p.lines)
	}
	// Exactly on the budget: the next chunk waits whole.
	p.add(engine.OutputChunk{ID: "a", Lines: []string{"1", "2", "3"}})
	p.add(engine.StepDone{ID: "a"})
	p.add(engine.OutputChunk{ID: "b", Lines: []string{"4"}})
	if b, n = p.take(); n != 3 || len(b) != 2 || len(p.queue) != 1 {
		t.Fatalf("take on the budget: %v (%d), left %v", b, n, p.queue)
	}
}

func TestQuantile(t *testing.T) {
	var s Stats
	if s.P95() != 0 {
		t.Error("p95 of nothing")
	}
	for i := 1; i <= 100; i++ {
		s.Work = append(s.Work, time.Duration(i))
	}
	if s.P95() != 95 || s.Max() != 100 {
		t.Errorf("p95 %v max %v", s.P95(), s.Max())
	}
}
