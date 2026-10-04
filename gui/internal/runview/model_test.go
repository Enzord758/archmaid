package runview

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/stub"
	"github.com/Enzord758/archmaid/internal/steps"
)

var t0 = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// view mirrors what the widgets hold: it applies each Change's output diff
// and row inserts, the way the GTK page does.
type view struct {
	out  []string
	rows []string // row IDs
}

func (v *view) apply(m *Model, c Change) {
	v.out = append(v.out[c.OutRemove:], c.OutAppend...)
	if c.Reset {
		v.rows = v.rows[:0]
		for _, r := range m.Rows {
			v.rows = append(v.rows, r.ID)
		}
		return
	}
	for _, i := range c.Inserted {
		v.rows = slices.Insert(v.rows, i, m.Rows[i].ID)
	}
}

func check(t *testing.T, m *Model, v *view) {
	t.Helper()
	if !slices.Equal(v.out, m.Output()) {
		t.Fatalf("view output (%d lines) != model output (%d lines)", len(v.out), len(m.Output()))
	}
	var ids []string
	for _, r := range m.Rows {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(v.rows, ids) {
		t.Fatalf("view rows %v != model rows %v", v.rows, ids)
	}
}

func TestFullCleanupOneEventAtATime(t *testing.T) {
	m, v := NewModel(), &view{}
	var opened []engine.PromptID
	for _, ev := range stub.FullCleanup(t0).Events() {
		c := m.Apply([]engine.Event{ev})
		v.apply(m, c)
		check(t, m, v)
		if c.Opened != nil {
			opened = append(opened, c.Opened.ID)
			m.Answered(c.Opened.ID)
		}
	}
	if !slices.Equal(opened, []engine.PromptID{1, 2, 3}) {
		t.Errorf("prompts opened %v, want [1 2 3]", opened)
	}
	if len(m.Rows) != 11 {
		t.Fatalf("%d rows, want 11 (10 planned + the inserted orphan removal)", len(m.Rows))
	}
	if m.Rows[5].ID != steps.IDOrphansRemove {
		t.Errorf("row 5 is %s, want the inserted %s", m.Rows[5].ID, steps.IDOrphansRemove)
	}
	for _, r := range m.Rows {
		if r.State != RowOK {
			t.Errorf("row %s: %v, want ok", r.ID, r.State)
		}
	}
	if m.Running || m.Finished == nil || m.Finished.Outcome != engine.OutcomeCompleted {
		t.Errorf("running %v, finished %+v; want a completed run", m.Running, m.Finished)
	}
	if m.Phase != engine.PhaseIdle || m.Prompt != nil {
		t.Errorf("phase %v, prompt %v; want idle and none", m.Phase, m.Prompt)
	}
	if len(m.Logs) != 6 {
		t.Errorf("%d log entries, want 6", len(m.Logs))
	}
	out := strings.Join(m.Output(), "\n")
	for _, want := range []string{"── Updating system with topgrade…", "==> Nothing to do.", "removing libbaz..."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Error("output kept an escape sequence")
	}
}

func TestFullCleanupAsOneBatch(t *testing.T) {
	m, v := NewModel(), &view{}
	c := m.Apply(stub.FullCleanup(t0).Events())
	v.apply(m, c)
	check(t, m, v)
	if !c.Reset || !c.Finished || c.Opened != nil {
		t.Errorf("reset %v, finished %v, opened %v; want a reset, the finish, and no confirm left open", c.Reset, c.Finished, c.Opened)
	}
}

func TestInsertShiftsTouchedRows(t *testing.T) {
	m, v := NewModel(), &view{}
	plan := []engine.PlanStep{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	v.apply(m, m.Apply([]engine.Event{engine.QueueStarted{Steps: plan}}))
	c := m.Apply([]engine.Event{
		engine.StepStarted{ID: "b", Index: 1},
		engine.StepInserted{Index: 1, Step: engine.PlanStep{ID: "x"}},
		engine.StepDone{ID: "c", Index: 3, Status: steps.Failed, Err: "boom"},
	})
	v.apply(m, c)
	check(t, m, v)
	if !slices.Equal(c.Inserted, []int{1}) || !slices.Equal(c.Rows, []int{2, 3}) {
		t.Errorf("inserted %v rows %v, want [1] and [2 3]", c.Inserted, c.Rows)
	}
	if m.Rows[2].State != RowRunning || m.Rows[3].State != RowFailed || m.Rows[3].Subtitle() != "Failed · 0 ms · boom" {
		t.Errorf("rows %+v", m.Rows)
	}
}

func TestRowsKeyOnIDNotIndex(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{engine.QueueStarted{Steps: []engine.PlanStep{{ID: "a"}, {ID: "b"}}}})
	c := m.Apply([]engine.Event{engine.StepDone{ID: "b", Index: 0, Status: steps.Skipped}})
	if m.Rows[1].State != RowSkipped || m.Rows[0].State != RowPending || !slices.Equal(c.Rows, []int{1}) {
		t.Errorf("a stale index moved the wrong row: %+v", m.Rows)
	}
	if c := m.Apply([]engine.Event{engine.StepDone{ID: "nope", Index: 0}}); len(c.Rows) != 0 {
		t.Errorf("an unknown step touched rows %v", c.Rows)
	}
}

func TestOutputCapAgainstRandomBatches(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	m, v := NewModel(), &view{}
	n := 0
	for n < 3*OutputCap {
		var lines []string
		for range r.IntN(3 * OutputCap / 2) {
			n++
			lines = append(lines, fmt.Sprint(n))
		}
		v.apply(m, m.Apply([]engine.Event{engine.OutputChunk{ID: "s", Lines: lines}}))
		check(t, m, v)
	}
	out := m.Output()
	if len(out) != OutputCap || out[len(out)-1] != fmt.Sprint(n) || m.Lines != n {
		t.Fatalf("%d lines ending %q (seen %d), want %d ending %d", len(out), out[len(out)-1], m.Lines, OutputCap, n)
	}
}

func TestBigBatchInsertsOnlyWhatStays(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{engine.OutputChunk{Lines: []string{"old"}}})
	lines := make([]string, OutputCap+500)
	for i := range lines {
		lines[i] = fmt.Sprint(i)
	}
	c := m.Apply([]engine.Event{engine.OutputChunk{Lines: lines}})
	if c.OutRemove != 1 || len(c.OutAppend) != OutputCap || c.OutAppend[0] != "500" {
		t.Errorf("remove %d, append %d from %q; want 1, %d from \"500\"", c.OutRemove, len(c.OutAppend), c.OutAppend[0], OutputCap)
	}
}

func TestDroppedLinesShowAMarker(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{engine.OutputChunk{Lines: []string{"x"}, Dropped: 7}})
	if !slices.Equal(m.Output(), []string{"… 7 lines not shown (they are in the log)", "x"}) || m.Dropped != 7 || m.Lines != 8 {
		t.Errorf("output %q dropped %d lines %d", m.Output(), m.Dropped, m.Lines)
	}
}

func TestPrompts(t *testing.T) {
	m := NewModel()
	c := m.Apply([]engine.Event{engine.NeedsConfirm{ID: 4, Msg: "?"}})
	if c.Opened == nil || m.Prompt == nil {
		t.Fatal("confirm not opened")
	}
	m.Answered(3) // stale: ignored
	if m.Prompt == nil {
		t.Fatal("a stale answer closed the prompt")
	}
	c = m.Apply([]engine.Event{engine.PromptClosed{ID: 4}})
	if m.Prompt != nil || !slices.Equal(c.Closed, []engine.PromptID{4}) {
		t.Errorf("prompt %v closed %v", m.Prompt, c.Closed)
	}
	// Opened and withdrawn in one batch: nothing to show.
	c = m.Apply([]engine.Event{engine.NeedsConfirm{ID: 5}, engine.PromptClosed{ID: 5}})
	if c.Opened != nil || m.Prompt != nil {
		t.Errorf("opened %v prompt %v; want neither", c.Opened, m.Prompt)
	}
	c = m.Apply([]engine.Event{
		engine.NeedsPassword{ID: 6}, engine.NeedsSelection{ID: 7}, engine.OutputReport{ID: 8}, engine.OutputReport{},
	})
	if !slices.Equal(c.Unsupported, []engine.PromptID{6, 7, 8}) {
		t.Errorf("unsupported %v, want [6 7 8]", c.Unsupported)
	}
}

func TestAbortedRun(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{
		engine.QueueStarted{Steps: []engine.PlanStep{{ID: "a"}, {ID: "b"}}},
		engine.StepStarted{ID: "a"},
		engine.StatusUpdated{Step: "a", StepElapsed: 1500 * time.Millisecond},
	})
	if got := m.Rows[0].Subtitle(); got != "Running · 1.5 s" {
		t.Errorf("running subtitle %q", got)
	}
	m.Apply([]engine.Event{
		engine.NeedsConfirm{ID: 1},
		engine.PromptClosed{ID: 1},
		engine.Aborted{ID: "a", Elapsed: 2 * time.Second},
		engine.Finished{Outcome: engine.OutcomeAborted},
	})
	if m.Rows[0].State != RowAborted || m.Rows[0].Subtitle() != "Stopped · 2.0 s" || m.Rows[1].State != RowPending || m.Running {
		t.Errorf("rows %+v running %v", m.Rows, m.Running)
	}
}

func TestAuthAndStatusEvents(t *testing.T) {
	m := NewModel()
	c := m.Apply([]engine.Event{engine.AuthWaiting{Kind: engine.WaitPolkit, Step: "x"}})
	if m.Auth == nil || !c.Status {
		t.Error("AuthWaiting not recorded")
	}
	m.Apply([]engine.Event{engine.AuthResolved{Granted: true}, engine.Notice{Text: "hi"}, engine.PlanChanged{Step: engine.PlanStep{ID: "none"}}})
	if m.Auth != nil || m.Notice != "hi" {
		t.Errorf("auth %v notice %q", m.Auth, m.Notice)
	}
}

func TestRowStateText(t *testing.T) {
	for s := RowPending; s <= RowAborted; s++ {
		if s.String() == "?" {
			t.Errorf("state %d has no name", s)
		}
		r := Row{State: s, Elapsed: 20 * time.Millisecond}
		if s != RowPending && r.Subtitle() == "" {
			t.Errorf("state %v has no subtitle", s)
		}
		if (s.Icon() == "") != (s == RowRunning) {
			t.Errorf("state %v icon %q", s, s.Icon())
		}
	}
	if RowState(99).String() != "?" {
		t.Error("an unknown state has a name")
	}
	if got := (Row{State: RowFailed}).Subtitle(); got != "Failed · 0 ms" {
		t.Errorf("failed subtitle %q", got)
	}
	for st, want := range map[steps.Status]RowState{steps.OK: RowOK, steps.Failed: RowFailed, steps.Skipped: RowSkipped, steps.AuthRefused: RowAuthRefused, steps.Aborted: RowAborted, steps.Pending: RowPending} {
		if got := doneState(st); got != want {
			t.Errorf("doneState(%v) = %v, want %v", st, got, want)
		}
	}
}

func TestStripSGR(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                         "plain",
		"\x1b[1;32m==>\x1b[0m Nothing.": "==> Nothing.",
		"a\x1b[mb":                      "ab",
		"cut \x1b[1;3":                  "cut ",
		"lone \x1b":                     "lone ",
	} {
		if got := StripSGR(in); got != want {
			t.Errorf("StripSGR(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFloodScript(t *testing.T) {
	s := Flood(t0, 25, 10, time.Millisecond)
	m := NewModel()
	m.Apply(s.Events())
	out := m.Output()
	if len(out) != 26 || m.Lines != 25 {
		t.Fatalf("%d view lines (%d seen), want 26 (header + 25)", len(out), m.Lines)
	}
	for i := 1; i <= 25; i++ {
		if out[i] != FloodLine(i, 25) {
			t.Fatalf("line %d is %q, want %q", i, out[i], FloodLine(i, 25))
		}
	}
	if s.Duration() != 3*time.Millisecond || !reflect.DeepEqual(m.Rows[0].State, RowOK) {
		t.Errorf("duration %v, row %+v", s.Duration(), m.Rows[0])
	}
	if got := len(Flood(t0, 3, 0, 0)); got != 3+3+3 {
		t.Errorf("perChunk 0: %d cues, want 9 (one line per chunk)", got)
	}
}

func BenchmarkApplyFlush(b *testing.B) {
	lines := make([]string, 2048)
	for i := range lines {
		lines[i] = FloodLine(i+1, 2048)
	}
	m := NewModel()
	b.ReportAllocs()
	for b.Loop() {
		m.Apply([]engine.Event{engine.OutputChunk{ID: "s", Lines: lines}})
	}
}
