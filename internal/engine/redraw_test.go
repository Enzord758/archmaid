package engine_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// TestStepOutputRedraws (#129): a child's StepOutput with Replace redraws
// its last lines: the OutputChunk says so, and the log entry gets only the
// final text of each line. Fails on ecb0903, where Replace didn't exist and
// every frame was a new line in the chunk and the log.
func TestStepOutputRedraws(t *testing.T) {
	step := child("a", "x")
	step.Category = logstore.Topgrade
	h := newHarness(t, Options{}, queue("q", step))
	h.start("q")
	seq := h.seq()
	feed := func(replace int, lines ...string) OutputChunk {
		t.Helper()
		o := h.handle(StepOutput{Seq: seq, Lines: lines, Replace: replace})
		return event[OutputChunk](t, o)
	}
	if c := feed(0, "one", "progress 10%"); c.Replace != 0 || !slices.Equal(c.Lines, []string{"one", "progress 10%"}) {
		t.Fatalf("first chunk %+v", c)
	}
	if c := feed(1, "progress 50%"); c.Replace != 1 || !slices.Equal(c.Lines, []string{"progress 50%"}) {
		t.Fatalf("redraw %+v", c)
	}
	if c := feed(2, "ONE", "\x1b[1mprogress 100%\x1b[0m", "two"); c.Replace != 2 || len(c.Lines) != 3 {
		t.Fatalf("cursor-up redraw %+v", c)
	}
	o := h.handle(StepExited{Seq: seq, Err: errors.New("exit status 1")})
	log := effect[WriteLog](t, o)
	if want := "ONE\n\x1b[1mprogress 100%\x1b[0m\ntwo\n"; log.Body != want {
		t.Fatalf("log body %q, want the final lines %q", log.Body, want)
	}
}

// TestStepOutputRedrawStopsAtOwnLines: a line the engine adds itself (an
// askpass refusal) ends what the child may redraw; a redraw reaching past
// it replaces only the child's lines after it and drops the new versions
// of the ones before (they stay as they were, in the chunk and the log).
func TestStepOutputRedrawStopsAtOwnLines(t *testing.T) {
	h := askpassRun(t, Options{})
	seq := h.seq()
	h.handle(StepOutput{Seq: seq, Lines: []string{"a 10%"}})
	h.handle(AskpassRequest{Req: 7, Refused: "caller is not sudo"})
	h.handle(StepOutput{Seq: seq, Lines: []string{"b"}})
	o := h.handle(StepOutput{Seq: seq, Lines: []string{"a 100%", "B", "c"}, Replace: 2})
	c := event[OutputChunk](t, o)
	if c.Replace != 1 || !slices.Equal(c.Lines, []string{"B", "c"}) {
		t.Fatalf("chunk %+v, want Replace 1 of the child's own line after the refusal", c)
	}
	// a redraw of nothing the child may reach adds nothing
	h.handle(AskpassRequest{Req: 8, Refused: "again"})
	if o := h.handle(StepOutput{Seq: seq, Lines: []string{"c2"}, Replace: 1}); len(all[OutputChunk](o.Events)) != 0 {
		t.Fatalf("a redraw of an unreachable line published %+v", o.Events)
	}
	o = h.handle(StepExited{Seq: seq, Err: errors.New("exit status 1")})
	body := effect[WriteLog](t, o).Body
	want := "a 10%\narchmaid: refused a sudo password request: caller is not sudo\nB\nc\narchmaid: refused a sudo password request: again\n"
	if body != want {
		t.Fatalf("log body\n%q\nwant\n%q", body, want)
	}
}

// TestRewrite: two chunks of one step merge as a front end would apply
// them one after the other, without sharing their arrays.
func TestRewrite(t *testing.T) {
	apply := func(view []string, c OutputChunk) []string {
		k := min(c.Replace, len(view))
		return append(slices.Clone(view[:len(view)-k]), c.Lines...)
	}
	base := []string{"p", "q", "r"}
	for _, tc := range []struct {
		name string
		a, b OutputChunk
		ok   bool
	}{
		{"within a", OutputChunk{Lines: []string{"x", "y"}}, OutputChunk{Lines: []string{"Y", "z"}, Replace: 1}, true},
		{"all of a", OutputChunk{Lines: []string{"x"}}, OutputChunk{Lines: []string{"X"}, Replace: 1}, true},
		{"past a", OutputChunk{Lines: []string{"x"}, Replace: 1}, OutputChunk{Lines: []string{"Q", "R", "X"}, Replace: 3}, true},
		{"b dropped lines", OutputChunk{Lines: []string{"x"}}, OutputChunk{Lines: []string{"X"}, Replace: 1, Dropped: 2}, false},
		{"past a, which dropped lines", OutputChunk{Lines: []string{"x"}, Dropped: 4}, OutputChunk{Lines: []string{"R", "X"}, Replace: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aLines := slices.Clone(tc.a.Lines)
			m, ok := Rewrite(tc.a, tc.b)
			if ok != tc.ok {
				t.Fatalf("ok %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			want := apply(apply(base, tc.a), tc.b)
			if got := apply(base, m); !slices.Equal(got, want) {
				t.Fatalf("merged %+v gives %q, one after the other %q", m, got, want)
			}
			m.Lines = append(m.Lines[:0], strings.Repeat("!", 1))
			if !slices.Equal(tc.a.Lines, aLines) {
				t.Fatal("the merged chunk shares a's array")
			}
		})
	}
}
