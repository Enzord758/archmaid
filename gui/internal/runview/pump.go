package runview

import (
	"slices"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
)

// PumpOptions configure a Pump.
type PumpOptions struct {
	// MaxPending is how many output lines may wait for the main thread
	// before the oldest are dropped (counted in OutputChunk.Dropped, as a
	// Driver would). The view only ever holds OutputCap lines, so dropping
	// beyond that loses nothing it would have kept. 0 means OutputCap; a
	// negative value never drops (for measuring the view's real capacity).
	MaxPending int
	// FlushLines is the most output lines one main-thread callback applies;
	// the rest waits for the next callback, so a flood is cut into slices
	// that each fit the main-thread budget (measured with the GTK list: a
	// 512-line flush — append, trim the head, scroll — takes 1–2 ms). 0
	// means 512.
	FlushLines int
	// Ended, when set, runs once on the main thread after the last batch
	// once the Driver's Events has closed: after its Close, or when the
	// Driver stopped on its own (a Session panic), possibly without a
	// Finished. The front end tells the two apart.
	Ended func()
}

// Pump carries a Driver's events to the main thread. One goroutine reads
// Events() and queues them; whenever the queue goes from empty to not, it
// posts one flush to the main thread (post is glib's idle-add in the GUI),
// and the flush hands everything queued — up to FlushLines output lines —
// to apply, in order. Control events are never dropped or merged; adjacent
// OutputChunks of one step are merged, and the oldest queued lines dropped
// past MaxPending.
//
// post must not block and must run its func on the thread that owns
// apply's state; apply runs only from there.
type Pump struct {
	post  func(func())
	apply func([]engine.Event)
	opts  PumpOptions

	mu        sync.Mutex
	queue     []engine.Event
	lines     int // output lines in queue
	scheduled bool
	stats     Stats
	// ended: Events has closed; endSent: Ended has been run.
	ended, endSent bool

	done chan struct{}
}

// Stats are the pump's counters, for the perf proof.
type Stats struct {
	Flushes int             // main-thread callbacks that applied something
	Lines   int             // output lines applied
	Dropped int             // output lines the pump dropped
	Work    []time.Duration // time spent in apply, per flush
	// FirstLine and LastLine are when the first and the last output line
	// were applied.
	FirstLine, LastLine time.Time
}

// P95 is the 95th-percentile flush time (0 with no flushes).
func (s Stats) P95() time.Duration { return quantile(s.Work, 0.95) }

// Max is the slowest flush.
func (s Stats) Max() time.Duration { return quantile(s.Work, 1) }

func quantile(d []time.Duration, q float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := slices.Clone(d)
	slices.Sort(s)
	i := int(q*float64(len(s))+0.5) - 1
	return s[min(max(i, 0), len(s)-1)]
}

// NewPump starts pumping d's events.
func NewPump(d engine.Driver, post func(func()), apply func([]engine.Event), opts PumpOptions) *Pump {
	if opts.MaxPending == 0 {
		opts.MaxPending = OutputCap
	}
	if opts.FlushLines <= 0 {
		opts.FlushLines = 512
	}
	p := &Pump{post: post, apply: apply, opts: opts, done: make(chan struct{})}
	go p.run(d.Events())
	return p
}

// Done is closed once the Driver's event stream has ended and the pump's
// goroutine has returned (after the Driver's Close).
func (p *Pump) Done() <-chan struct{} { return p.done }

// Stats is a copy of the counters.
func (p *Pump) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.stats
	s.Work = slices.Clone(s.Work)
	return s
}

func (p *Pump) run(events <-chan engine.Event) {
	defer close(p.done)
	for ev := range events {
		p.mu.Lock()
		p.add(ev)
		schedule := !p.scheduled
		p.scheduled = true
		p.mu.Unlock()
		if schedule {
			p.post(p.flush)
		}
	}
	if p.opts.Ended == nil {
		return
	}
	// One more flush runs Ended once everything queued is applied.
	p.mu.Lock()
	p.ended = true
	schedule := !p.scheduled
	p.scheduled = true
	p.mu.Unlock()
	if schedule {
		p.post(p.flush)
	}
}

// add queues ev; p.mu is held.
func (p *Pump) add(ev engine.Event) {
	if ch, ok := ev.(engine.OutputChunk); ok {
		if n := len(p.queue); n > 0 {
			if last, ok := p.queue[n-1].(engine.OutputChunk); ok && last.ID == ch.ID && last.Index == ch.Index {
				merged, ok := last, false
				switch {
				case ch.Replace > 0: // a redraw merges as one chunk when it can
					merged, ok = engine.Rewrite(last, ch)
				case ch.Dropped == 0:
					merged.Lines, ok = append(slices.Clip(last.Lines), ch.Lines...), true
				}
				if ok {
					p.lines += len(merged.Lines) - len(last.Lines)
					p.queue[n-1] = merged
					p.trim()
					return
				}
			}
		}
		p.lines += len(ch.Lines)
		p.queue = append(p.queue, ch)
		p.trim()
		return
	}
	p.queue = append(p.queue, ev)
}

// trim drops the oldest queued lines past MaxPending; p.mu is held.
func (p *Pump) trim() {
	if p.opts.MaxPending < 0 {
		return
	}
	for i := 0; p.lines > p.opts.MaxPending && i < len(p.queue); i++ {
		ch, ok := p.queue[i].(engine.OutputChunk)
		if !ok || len(ch.Lines) == 0 {
			continue
		}
		n := min(len(ch.Lines), p.lines-p.opts.MaxPending)
		ch.Lines, ch.Dropped = ch.Lines[n:], ch.Dropped+n
		p.queue[i] = ch
		p.lines -= n
		p.stats.Dropped += n
	}
}

// take removes the next batch from the queue: everything up to FlushLines
// output lines, splitting a chunk at the limit. p.mu is held.
func (p *Pump) take() (batch []engine.Event, lines int) {
	budget := p.opts.FlushLines
	i := 0
	for ; i < len(p.queue); i++ {
		ch, ok := p.queue[i].(engine.OutputChunk)
		if !ok {
			continue
		}
		if budget == 0 && len(ch.Lines) > 0 {
			batch = slices.Clone(p.queue[:i])
			p.queue = p.queue[i:]
			p.lines -= lines
			return batch, lines
		}
		if len(ch.Lines) > budget {
			head := ch
			head.Lines = ch.Lines[:budget:budget]
			rest := ch // the head carries the chunk's redraw and drop
			rest.Lines, rest.Dropped, rest.Replace = ch.Lines[budget:], 0, 0
			batch = append(slices.Clone(p.queue[:i]), head)
			p.queue[i] = rest
			p.queue = p.queue[i:]
			lines += budget
			p.lines -= lines
			return batch, lines
		}
		budget -= len(ch.Lines)
		lines += len(ch.Lines)
	}
	batch, p.queue = p.queue, nil
	p.lines -= lines
	return batch, lines
}

// flush runs on the main thread: it applies one batch and, if more is
// queued, posts itself again.
func (p *Pump) flush() {
	p.mu.Lock()
	batch, lines := p.take()
	more := len(p.queue) > 0
	p.scheduled = more
	end := !more && p.ended && !p.endSent
	if end {
		p.endSent = true
	}
	p.mu.Unlock()
	if len(batch) > 0 {
		start := time.Now()
		p.apply(batch)
		end := time.Now()
		p.mu.Lock()
		p.stats.Flushes++
		p.stats.Work = append(p.stats.Work, end.Sub(start))
		if lines > 0 {
			if p.stats.Lines == 0 {
				p.stats.FirstLine = end
			}
			p.stats.Lines += lines
			p.stats.LastLine = end
		}
		p.mu.Unlock()
	}
	if end {
		p.opts.Ended()
	}
	if more {
		p.post(p.flush)
	}
}
