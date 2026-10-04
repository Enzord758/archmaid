package driver

import (
	"reflect"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
)

// drain takes everything deliverable from o as of when.
func drain(o *outbox, when time.Time) []engine.Event {
	var evs []engine.Event
	for {
		ev, _ := o.next(when)
		if ev == nil {
			return evs
		}
		evs = append(evs, ev)
	}
}

func chunk(id string, lines ...string) engine.OutputChunk {
	return engine.OutputChunk{ID: id, Lines: lines}
}

// Consecutive chunks of one step merge while the flush window is open; a
// control event or another step's chunk closes it. Nothing is delivered
// from an open chunk before its window ends.
func TestOutboxCoalesces(t *testing.T) {
	o := newOutbox(time.Hour, 100)
	o.push([]engine.Event{chunk("a", "1"), chunk("a", "2")})
	o.push([]engine.Event{chunk("a", "3"), engine.Notice{Text: "n"}, chunk("b", "x"), chunk("a", "4")})
	o.push([]engine.Event{chunk("a"), engine.OutputChunk{ID: "a"}}) // empty: nothing
	got := drain(o, time.Now())
	want := []engine.Event{chunk("a", "1", "2", "3"), engine.Notice{Text: "n"}, chunk("b", "x")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delivered %+v, want %+v", got, want)
	}
	if _, wait := o.next(time.Now()); wait <= 0 {
		t.Fatal("the open chunk was not held for its window")
	}
	if got := drain(o, time.Now().Add(2*time.Hour)); !reflect.DeepEqual(got, []engine.Event{chunk("a", "4")}) {
		t.Fatalf("after the window: %+v", got)
	}
}

// Past max undelivered lines of a step, its oldest go, counted in the
// Dropped of the chunk that then leads — across a control event, and
// carried on when a whole chunk goes. Other steps' lines are untouched.
func TestOutboxDropsOldestOutputOnly(t *testing.T) {
	o := newOutbox(time.Hour, 3)
	o.push([]engine.Event{chunk("a", "1", "2"), engine.Notice{Text: "n"}, chunk("b", "x"), chunk("a", "3", "4", "5")})
	got := drain(o, time.Now().Add(2*time.Hour))
	want := []engine.Event{
		engine.Notice{Text: "n"},
		chunk("b", "x"),
		engine.OutputChunk{ID: "a", Lines: []string{"3", "4", "5"}, Dropped: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delivered %+v, want %+v", got, want)
	}
	o.push([]engine.Event{chunk("a", "6", "7", "8", "9", "10")})
	got = drain(o, time.Now().Add(2*time.Hour))
	want = []engine.Event{engine.OutputChunk{ID: "a", Lines: []string{"8", "9", "10"}, Dropped: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delivered %+v, want %+v", got, want)
	}
	if len(o.lines) != 0 {
		t.Fatalf("line counts left: %v", o.lines)
	}
}
