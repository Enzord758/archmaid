package driver

import (
	"slices"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
)

// outbox is the Events still to deliver, in order. Control events are kept
// without bound and delivered exactly once. Consecutive OutputChunks of one
// step are merged into the last one while it is open: for flush after it
// was queued, until another event follows it, or until it holds sealAt
// lines. Past max undelivered lines of one step, the oldest of them are
// dropped and counted in the Dropped of the chunk that now leads.
type outbox struct {
	flush time.Duration
	max   int

	mu    sync.Mutex
	items []*item
	lines map[string]int // undelivered output lines, by step ID
	wake  chan struct{}  // cap 1: something was pushed
}

type item struct {
	ev     engine.Event        // a control event, or nil
	chunk  *engine.OutputChunk // an output chunk, or nil
	at     time.Time           // when it was queued
	sealed bool                // no more lines are merged into it
}

func newOutbox(flush time.Duration, max int) *outbox {
	return &outbox{flush: flush, max: max, lines: map[string]int{}, wake: make(chan struct{}, 1)}
}

// sealAt is how many lines close a chunk at once, so a consumer that keeps
// up gets a flood in steady pieces rather than all of a flush at once.
func (o *outbox) sealAt() int { return max(o.max/8, 1) }

// push queues one Output's Events.
func (o *outbox) push(evs []engine.Event) {
	if len(evs) == 0 {
		return
	}
	now := time.Now()
	o.mu.Lock()
	for _, ev := range evs {
		var tail *item
		if n := len(o.items); n > 0 {
			tail = o.items[n-1]
		}
		c, ok := ev.(engine.OutputChunk)
		if !ok {
			if tail != nil {
				tail.sealed = true
			}
			o.items = append(o.items, &item{ev: ev, at: now, sealed: true})
			continue
		}
		if len(c.Lines) == 0 && c.Dropped == 0 {
			continue
		}
		if tail != nil && !tail.sealed && tail.chunk != nil && tail.chunk.ID == c.ID {
			tail.chunk.Lines = append(tail.chunk.Lines, c.Lines...)
			tail.chunk.Dropped += c.Dropped
		} else {
			if tail != nil {
				tail.sealed = true
			}
			cp := c
			cp.Lines = slices.Clone(c.Lines)
			tail = &item{chunk: &cp, at: now}
			o.items = append(o.items, tail)
		}
		if len(tail.chunk.Lines) >= o.sealAt() {
			tail.sealed = true
		}
		o.lines[c.ID] += len(c.Lines)
		o.trim(c.ID)
	}
	o.mu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// trim drops step id's oldest undelivered lines past max.
func (o *outbox) trim(id string) {
	carry := 0 // Dropped of a chunk trimmed away, for the next one
	for i := 0; i < len(o.items) && (o.lines[id] > o.max || carry > 0); {
		it := o.items[i]
		if it.chunk == nil || it.chunk.ID != id {
			i++
			continue
		}
		it.chunk.Dropped += carry
		carry = 0
		n := min(o.lines[id]-o.max, len(it.chunk.Lines))
		if n <= 0 {
			break
		}
		it.chunk.Lines = it.chunk.Lines[n:]
		it.chunk.Dropped += n
		o.lines[id] -= n
		if len(it.chunk.Lines) > 0 {
			break
		}
		carry = it.chunk.Dropped
		o.items = slices.Delete(o.items, i, i+1)
	}
}

// next takes the first deliverable event, or says how long until the
// first one is (0: nothing queued).
func (o *outbox) next(now time.Time) (ev engine.Event, wait time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.items) == 0 {
		return nil, 0
	}
	it := o.items[0]
	if !it.sealed {
		if d := it.at.Add(o.flush).Sub(now); d > 0 {
			return nil, d
		}
	}
	o.items[0] = nil
	o.items = o.items[1:]
	if it.chunk == nil {
		return it.ev, 0
	}
	o.lines[it.chunk.ID] -= len(it.chunk.Lines)
	if o.lines[it.chunk.ID] == 0 {
		delete(o.lines, it.chunk.ID)
	}
	return *it.chunk, 0
}

// run delivers events to out until stop closes.
func (o *outbox) run(out chan<- engine.Event, stop <-chan struct{}) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		ev, wait := o.next(time.Now())
		if ev != nil {
			select {
			case out <- ev:
			case <-stop:
				return
			}
			continue
		}
		var tick <-chan time.Time
		if wait > 0 {
			timer.Reset(wait)
			tick = timer.C
		}
		select {
		case <-o.wake:
		case <-tick:
		case <-stop:
			return
		}
	}
}
