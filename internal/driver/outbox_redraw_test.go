package driver

import (
	"reflect"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
)

func redraw(id string, replace int, lines ...string) engine.OutputChunk {
	return engine.OutputChunk{ID: id, Lines: lines, Replace: replace}
}

// TestOutboxMergesRedraws (#129): a redraw (OutputChunk.Replace) merges
// into the open chunk before it as engine.Rewrite says, its lines counted
// once; one that can't be one chunk, or follows a sealed one, is
// delivered as it is; dropping a redraw's oldest lines keeps its Replace.
func TestOutboxMergesRedraws(t *testing.T) {
	o := newOutbox(time.Hour, 100)
	o.push([]engine.Event{chunk("a", "1", "2", "3"), redraw("a", 1, "3'", "4"), redraw("a", 5, "0'", "1'", "2'", "3''", "4'", "5")})
	o.push([]engine.Event{engine.Notice{Text: "n"}, redraw("a", 2, "4''", "5'")})
	got := drain(o, time.Now().Add(2*time.Hour))
	want := []engine.Event{
		redraw("a", 1, "0'", "1'", "2'", "3''", "4'", "5"),
		engine.Notice{Text: "n"},
		redraw("a", 2, "4''", "5'"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delivered %+v,\nwant %+v", got, want)
	}
	if len(o.lines) != 0 {
		t.Fatalf("lines left counted: %v", o.lines)
	}

	o = newOutbox(time.Hour, 3)
	o.push([]engine.Event{chunk("a", "1", "2"), engine.Notice{Text: "n"}, redraw("a", 1, "2'", "3", "4", "5")})
	got = drain(o, time.Now().Add(2*time.Hour))
	// The emptied chunk stays with its Dropped: carried into the redraw,
	// the redraw's removal would come before that gap and take a line the
	// consumer has in place of the dropped "2".
	want = []engine.Event{
		engine.OutputChunk{ID: "a", Lines: []string{}, Dropped: 2},
		engine.Notice{Text: "n"},
		engine.OutputChunk{ID: "a", Lines: []string{"3", "4", "5"}, Replace: 1, Dropped: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after a drop: %+v,\nwant %+v", got, want)
	}
}
