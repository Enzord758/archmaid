package engine_test

import (
	"os"
	"slices"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

const sgrLine = ":: \x1b[1mSearching\x1b[0m"

// outputLines runs a one-step queue and returns the lines the step's output
// became: through an in-process result (output) and through a child's
// StepOutput.
func outputLines(t *testing.T, opts Options) (result, streamed []string) {
	t.Helper()
	h := newHarness(t, opts, queue("q", inproc("a")))
	h.start("q")
	o := h.result(steps.Result{Output: sgrLine + "\n"})
	result = event[OutputChunk](t, o).Lines
	h = newHarness(t, opts, queue("q", child("a", "x")))
	h.start("q")
	o = h.handle(StepOutput{Seq: h.seq(), Lines: []string{sgrLine}})
	streamed = event[OutputChunk](t, o).Lines
	return result, streamed
}

// TestNoColourValues (QA b8 tail): NO_COLOR empty keeps the SGR of a user
// step's output; any non-empty value, "0" included (no-color.org), strips
// it; the unset variable keeps it. A child's StepOutput and an in-process
// result agree.
func TestNoColourValues(t *testing.T) {
	for _, tc := range []struct {
		val   string
		unset bool
		plain bool
	}{{"", false, false}, {"", true, false}, {"1", false, true}, {"0", false, true}, {"false", false, true}, {" ", false, true}} {
		t.Setenv("NO_COLOR", tc.val)
		if tc.unset {
			os.Unsetenv("NO_COLOR")
		}
		want := sgrLine
		if tc.plain {
			want = ":: Searching"
		}
		res, str := outputLines(t, Options{})
		if !slices.Equal(res, []string{want}) || !slices.Equal(str, []string{want}) {
			t.Errorf("NO_COLOR=%q (unset %v): result %q, streamed %q, want %q", tc.val, tc.unset, res, str, want)
		}
		if want != sanitize.String(sgrLine, sanitize.ColourMode()) {
			t.Errorf("NO_COLOR=%q: the engine and sanitize.ColourMode disagree", tc.val)
		}
	}
}

// TestNoColourReadOnce: the Session reads NO_COLOR in New. Changing it
// mid-run changes nothing, either way.
func TestNoColourReadOnce(t *testing.T) {
	for _, tc := range []struct{ before, after, want string }{
		{"", "1", sgrLine},
		{"1", "", ":: Searching"},
		{"1", "0", ":: Searching"},
	} {
		t.Setenv("NO_COLOR", tc.before)
		h := newHarness(t, Options{}, queue("q", child("a", "x"), child("b", "y")))
		t.Setenv("NO_COLOR", tc.after)
		h.start("q")
		seq := h.seq()
		o := h.handle(StepOutput{Seq: seq, Lines: []string{sgrLine}})
		if got := event[OutputChunk](t, o).Lines; !slices.Equal(got, []string{tc.want}) {
			t.Errorf("NO_COLOR %q then %q: first step's line %q, want %q", tc.before, tc.after, got, tc.want)
		}
		h.handle(StepExited{Seq: seq})
		t.Setenv("NO_COLOR", tc.before)
		o = h.handle(StepOutput{Seq: h.seq(), Lines: []string{sgrLine}})
		if got := event[OutputChunk](t, o).Lines; !slices.Equal(got, []string{tc.want}) {
			t.Errorf("NO_COLOR %q then %q, back again: second step's line %q, want %q", tc.before, tc.after, got, tc.want)
		}
	}
}
