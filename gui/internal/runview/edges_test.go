package runview

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Edge forms of SGR the parser tolerates: each is dropped or defaulted, and
// the text and the neighbouring styles are untouched.
func TestParseSGREdges(t *testing.T) {
	red := Style{FG: idx(1)}
	bold := Style{Bold: true}
	for _, c := range []struct {
		name, in string
		want     Line
	}{
		// A style set after the last character has no text: no span.
		{"style at the very end", "a\x1b[31m", Line{Text: "a"}},
		{"empty span at the end after a styled one", "\x1b[1ma\x1b[31m", Line{"a", []Span{sp(0, 1, bold)}}},
		// An empty parameter between ; is a 0 (reset).
		{"empty parameter resets", "\x1b[31ma\x1b[;1mb", Line{"ab", []Span{sp(0, 1, red), sp(1, 2, bold)}}},
		// A parameter too long for an int is ignored, the next one applies.
		{"overflowing parameter ignored", "\x1b[99999999999999999999;1ma", Line{"a", []Span{sp(0, 1, bold)}}},
		{"empty colon sub-parameter", "\x1b[:1ma", Line{Text: "a"}},
		// 38 with nothing after it.
		{"38 without parameters", "\x1b[38ma", Line{Text: "a"}},
		{"48 without parameters after a bold", "\x1b[1;48ma", Line{"a", []Span{sp(0, 1, bold)}}},
		// 24-bit with too few components uses all it has and sets nothing.
		{"truncated 24-bit", "\x1b[38;2;1;2ma", Line{Text: "a"}},
		// 38;7 is no colour form: only the 7 is consumed, the next applies.
		{"unknown extended form", "\x1b[38;7;1ma", Line{"a", []Span{sp(0, 1, bold)}}},
		{"unknown colon form", "\x1b[38:7:1ma", Line{Text: "a"}},
		// An empty component in the colon form is 0.
		{"colon 24-bit with an empty component", "\x1b[38:2:1::3ma", Line{"a", []Span{sp(0, 1, Style{FG: rgb(1, 0, 3)})}}},
		{"colon 256 with an empty index", "\x1b[38:5:ma", Line{"a", []Span{sp(0, 1, Style{FG: idx(0)})}}},
		{"colon colour out of range", "\x1b[38:2:1:2:999ma", Line{Text: "a"}},
	} {
		if got := ParseSGR(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ParseSGR(%q) = %+v, want %+v", c.name, c.in, got, c.want)
		}
	}
}

// Dank16Path falls back to ~/.cache without XDG_CACHE_HOME, and is empty
// when neither is known (and then the palette is the fallback).
func TestDank16PathFallbacks(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/home/someone")
	if got, want := Dank16Path(), "/home/someone/.cache/DankMaterialShell/dms-colors.json"; got != want {
		t.Errorf("path %q, want %q", got, want)
	}
	t.Setenv("HOME", "")
	if got := Dank16Path(); got != "" {
		t.Errorf("no cache dir, no home: path %q, want empty", got)
	}
	if LoadPalette() != FallbackPalette {
		t.Error("no path: not the fallback palette")
	}
}

// A colour that is hex-shaped but not hex fails the whole palette.
func TestLoadDank16BadHexDigits(t *testing.T) {
	body := strings.Replace(dank16JSON("dark", -1), "#000003", "#00zz03", 1)
	_, err := LoadDank16(writeColours(t, body))
	if err == nil || !strings.Contains(err.Error(), "color3") || !strings.Contains(err.Error(), "#00zz03") {
		t.Errorf("err %v, want it to name color3 and the value", err)
	}
}

// A plan with a root step that runs sudo itself (no helper verb), not in a
// dry run and without a kept password: the sudo dialog note.
func TestGateNotesSudoOwnDialog(t *testing.T) {
	plan := []engine.PlanStep{{ID: "x", Title: "X", NeedsPriv: true}}
	got := GateNotes(plan, false, false, false)
	want := []string{"If sudo needs your password, archmaid asks for it in its own dialog."}
	if !slices.Equal(got, want) {
		t.Errorf("notes %q, want %q", got, want)
	}
}

// A locked account that pam_faillock does not unlock on its own has no
// time to name.
func TestLockTextWithoutUnlockTime(t *testing.T) {
	if got := lockText(engine.FaillockState{Locked: true}); got != "account locked" {
		t.Errorf("lockText %q", got)
	}
	u := time.Date(2026, 10, 4, 12, 30, 0, 0, time.Local)
	if got := lockText(engine.FaillockState{Locked: true, Unlock: u}); got != "account locked until 12:30" {
		t.Errorf("lockText %q", got)
	}
}

// PlanChanged for a row the model has renames it in place and marks only
// that row; for an unknown step it changes nothing.
func TestPlanChangedUpdatesTheRow(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{plan("a", "b")})
	c := m.Apply([]engine.Event{engine.PlanChanged{Index: 1, Step: engine.PlanStep{ID: "b", Title: "New title", NeedsPriv: true, Optional: true, LogLabel: "New label"}}})
	r := m.Rows[1]
	if r.Title != "New title" || !r.NeedsPriv || !r.Optional || r.LogLabel != "New label" {
		t.Errorf("row %+v", r)
	}
	if !slices.Equal(c.Rows, []int{1}) {
		t.Errorf("rows touched %v, want [1]", c.Rows)
	}
	c = m.Apply([]engine.Event{engine.PlanChanged{Step: engine.PlanStep{ID: "zzz", Title: "Nope"}}})
	if len(c.Rows) != 0 || m.Rows[0].Title != "Step a" || m.Rows[1].Title != "New title" {
		t.Errorf("an unknown step changed rows %v: %+v", c.Rows, m.Rows)
	}
}

// A chunk of blank lines does not replace the summary line the step
// already has.
func TestBlankChunkKeepsLastText(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{plan("a"), engine.StepStarted{ID: "a"},
		engine.OutputChunk{ID: "a", Lines: []string{"real line"}}})
	m.Apply([]engine.Event{engine.OutputChunk{ID: "a", Lines: []string{"", "   ", "\x1b[0m"}}})
	if m.Rows[0].Last != "real line" {
		t.Errorf("Last %q, want the last non-blank line to stay", m.Rows[0].Last)
	}
}

// Output that lands in a step after another step's header splits that
// step into two runs; when the ring drops both, the removal is one entry
// for the step, not two.
func TestPopRunsMergesRemovalsOfOneStep(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{plan("a", "b", "z"), engine.StepStarted{ID: "a", Index: 0},
		engine.OutputChunk{ID: "a", Index: 0, Lines: []string{"a1", "a2"}},
		engine.StepStarted{ID: "b", Index: 1},
		engine.OutputChunk{ID: "a", Index: 0, Lines: []string{"a3", "a4"}}})
	// 1 header + 2 + 1 header + 2 = 6 lines in the view.
	flood := make([]string, OutputCap)
	for i := range flood {
		flood[i] = fmt.Sprint("z", i)
	}
	c := m.Apply([]engine.Event{engine.OutputChunk{ID: "z", Index: 2, Lines: flood}})
	if c.OutRemove != 6 {
		t.Fatalf("removed %d lines, want 6", c.OutRemove)
	}
	if len(c.Steps) != 2 || c.Steps[0].ID != "a" || c.Steps[0].Remove != 4 || c.Steps[0].Append != nil {
		var got []string
		for _, s := range c.Steps {
			got = append(got, fmt.Sprintf("%s -%d +%d", s.ID, s.Remove, len(s.Append)))
		}
		t.Errorf("steps %q, want a's 4 removals as one entry first", got)
	}
}

// Once more than 64 runs have left the head and they are over half of all,
// the run list is compacted; the grouped view stays right across it.
func TestRunListCompaction(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{plan("a", "b", "z")})
	var evs []engine.Event
	for i := range 100 { // alternating steps: one run per line
		id := "a"
		if i%2 == 1 {
			id = "b"
		}
		evs = append(evs, engine.OutputChunk{ID: id, Lines: []string{fmt.Sprint(i)}})
	}
	g := groups{}
	c := m.Apply(evs)
	g.apply(t, c.Steps)
	if len(m.runs) != 100 {
		t.Fatalf("%d runs, want 100", len(m.runs))
	}
	flood := make([]string, OutputCap)
	for i := range flood {
		flood[i] = fmt.Sprint("z", i)
	}
	c = m.Apply([]engine.Event{engine.OutputChunk{ID: "z", Lines: flood}})
	g.apply(t, c.Steps)
	if m.runHead != 0 || len(m.runs) != 1 {
		t.Errorf("runHead %d, %d runs; want the 100 popped runs dropped, 1 left", m.runHead, len(m.runs))
	}
	if !reflect.DeepEqual(g, m.want()) {
		t.Errorf("grouped view differs from the model's after compaction")
	}
	if l, ok := m.StepLine("z", 0); !ok || l.Text != "z0" {
		t.Errorf("StepLine(z, 0) = %q, %v", l.Text, ok)
	}
}

// StepLine refuses a negative index.
func TestStepLineNegative(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{plan("a"), engine.StepStarted{ID: "a"}, engine.OutputChunk{ID: "a", Lines: []string{"x"}},
		engine.StepDone{ID: "a", Status: steps.OK}})
	if l, ok := m.StepLine("a", -1); ok || l.Text != "" {
		t.Errorf("StepLine(-1) = %q, %v", l.Text, ok)
	}
	if l, ok := m.StepLine("a", 0); !ok || l.Text != "x" {
		t.Errorf("StepLine(0) = %q, %v", l.Text, ok)
	}
}

// FloodSteps with no chunk size sends one line per chunk, still spreading
// the n lines over the k steps.
func TestFloodStepsZeroChunk(t *testing.T) {
	s := FloodSteps(t0, 4, 2, 0)
	var chunks, lines int
	for _, ev := range s.Events() {
		if o, ok := ev.(engine.OutputChunk); ok {
			chunks++
			lines += len(o.Lines)
			if len(o.Lines) != 1 {
				t.Errorf("chunk of %d lines, want 1", len(o.Lines))
			}
		}
	}
	if chunks != 4 || lines != 4 {
		t.Errorf("%d chunks, %d lines; want 4 and 4", chunks, lines)
	}
	m := NewModel()
	m.Apply(s.Events())
	if got := m.StepText("flood-01"); len(got) != 2 {
		t.Errorf("step 1 text %q, want 2 lines", got)
	}
}
