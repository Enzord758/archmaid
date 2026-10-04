package proc

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lines
}

// TestNextBatchesBufferedLines: Next takes everything already buffered in
// one batch, at most max lines, in order, and ends the batch at the done
// message (which nothing follows).
func TestNextBatchesBufferedLines(t *testing.T) {
	const n = 2*BatchMax + 10
	ch := make(chan Msg, n+1)
	for _, l := range numbered(n) {
		ch <- Msg{Line: l}
	}
	ch <- Msg{Done: true, Err: os.ErrClosed}

	var got []string
	var sizes []int
	for {
		b, ok := Next(ch, nil, BatchMax)
		if !ok {
			t.Fatal("Next gave up with no quit")
		}
		got = append(got, b.Lines...)
		sizes = append(sizes, len(b.Lines))
		if b.Done {
			if b.Err != os.ErrClosed {
				t.Fatalf("done err = %v, want the child's error", b.Err)
			}
			break
		}
	}
	if fmt.Sprint(sizes) != fmt.Sprint([]int{BatchMax, BatchMax, 10}) {
		t.Fatalf("batch sizes = %v, want %d, %d, then 10 with the done", sizes, BatchMax, BatchMax)
	}
	if strings.Join(got, ",") != strings.Join(numbered(n), ",") {
		t.Fatal("lines out of order or lost across batches")
	}
	if len(ch) != 0 {
		t.Fatalf("%d messages left after the done", len(ch))
	}
}

// TestNextDoneAlone: a done with nothing before it is a batch of its own, and
// a Next that finds nothing buffered after a line returns just that.
func TestNextDoneAlone(t *testing.T) {
	ch := make(chan Msg, 4)
	ch <- Msg{Line: "only"}
	if b, ok := Next(ch, nil, BatchMax); !ok || b.Done || len(b.Lines) != 1 || b.Lines[0] != "only" {
		t.Fatalf("got %#v (ok %v), want just the one line", b, ok)
	}
	ch <- Msg{Done: true}
	if b, ok := Next(ch, nil, BatchMax); !ok || !b.Done || len(b.Lines) != 0 {
		t.Fatalf("got %#v (ok %v), want a bare done", b, ok)
	}
}

// TestNextReleasedByQuit: a Next waiting on an empty channel returns with ok
// false once quit closes, so an abort leaves no goroutine behind.
func TestNextReleasedByQuit(t *testing.T) {
	quit := make(chan struct{})
	close(quit)
	if b, ok := Next(make(chan Msg), quit, BatchMax); ok {
		t.Fatalf("released Next returned %#v, want ok false", b)
	}
}
