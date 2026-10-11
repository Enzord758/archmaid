package ui

// Plain-Go tests (no display) of the pieces of the GUI that need no
// widgets: they build a Window by hand, with only the fields the code under
// test touches. Display-only behaviour is in cover_harness_test.go.

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"

	"github.com/Enzord758/archmaid/gui/internal/logview"
	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// run builds a logview.Run the way Runs does, from a state dir.
func runsIn(t *testing.T, dir string) []logview.Run {
	t.Helper()
	runs, err := logview.Runs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

// TestRunFolder: which run directory a run's Open folder shows.
func TestRunFolder(t *testing.T) {
	dir := t.TempDir()
	at := func(h, m int) time.Time { return time.Date(2026, 10, 4, h, m, 0, 0, time.Local) }
	put := func(when time.Time, dry bool) {
		st := logstore.New(logstore.Options{Dir: dir, DryRun: dry, Now: func() time.Time { return when }})
		if err := st.Append(logstore.Cleanup, "OK", "x", "out\n"); err != nil {
			t.Fatal(err)
		}
	}
	put(at(9, 0), false)
	put(at(10, 30), false)
	runs := runsIn(t, dir)
	if got, ok := runFolder(runs, time.Time{}, true); ok {
		t.Errorf("a dry run with no dry-run slot got %q", got.Name)
	}
	put(at(11, 0), true)
	runs = runsIn(t, dir)
	if got, ok := runFolder(runs, at(10, 30), true); !ok || !got.DryRun {
		t.Errorf("dry run: %q %v, want the dry-run slot", got.Name, ok)
	}
	// Stamped with its minute, or the one before; else the newest real run.
	if got, ok := runFolder(runs, at(9, 0), false); !ok || got.Name != "20261004-0900" {
		t.Errorf("exact minute: %q %v", got.Name, ok)
	}
	if got, ok := runFolder(runs, at(9, 1), false); !ok || got.Name != "20261004-0900" {
		t.Errorf("a minute later: %q %v", got.Name, ok)
	}
	if got, ok := runFolder(runs, at(8, 0), false); !ok || got.Name != "20261004-1030" {
		t.Errorf("no match: %q %v, want the newest real run", got.Name, ok)
	}
	// Only a dry-run slot: a real run has no folder.
	only := []logview.Run{{Name: logview.DryRunName, DryRun: true}}
	if got, ok := runFolder(only, at(8, 0), false); ok {
		t.Errorf("a real run got the dry-run slot %q", got.Name)
	}
	if got, ok := runFolder(nil, time.Time{}, false); ok {
		t.Errorf("no runs: %q", got.Name)
	}
}

// TestEntryClass: the state colour of a log entry by its kind, none for an
// index outside the entries.
func TestEntryClass(t *testing.T) {
	es := []logview.Entry{{Kind: "OK"}, {Kind: "FAIL"}, {Kind: "ABORT"}, {Kind: "SKIP"}, {Kind: "WARN"}, {Kind: ""}, {Kind: "DRY"}}
	want := []string{"am-state-ok", "am-state-failed", "am-state-failed", "am-state-auth-refused", "am-state-auth-refused", "", "am-state-running"}
	for i, w := range want {
		if got := entryClass(es, i); got != w {
			t.Errorf("kind %q: %q, want %q", es[i].Kind, got, w)
		}
	}
	if entryClass(es, -1) != "" || entryClass(es, len(es)) != "" || entryClass(nil, 0) != "" {
		t.Error("an index outside the entries has a class")
	}
}

// TestCardByIDUnknown: an action without an Overview card gets its menu
// label.
func TestCardByIDUnknown(t *testing.T) {
	c := cardByID("not-a-card")
	if c.ID != "not-a-card" || c.Label != overview.Label("not-a-card") {
		t.Errorf("card %+v", c)
	}
	if c := cardByID(actions.IDFullCleanup); c.Label == "" || c.ID != actions.IDFullCleanup {
		t.Errorf("a real card: %+v", c)
	}
}

// TestUserCacheDirNoHome: without $HOME there is no folder to measure, and
// a measurement is not started.
func TestUserCacheDirNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got := userCacheDir(); got != "" {
		t.Errorf("dir %q without HOME", got)
	}
	w := &Window{}
	w.measureUserCache()
	if w.cache.busy {
		t.Error("a measurement started with no folder")
	}
	t.Setenv("HOME", "/h")
	if got := userCacheDir(); got != "/h/.cache" {
		t.Errorf("dir %q", got)
	}
	w.closed = true
	w.measureUserCache()
	if w.cache.busy {
		t.Error("a measurement started in a closed window")
	}
}

// TestAbortedOfPlainDriver: a Driver without Aborted (the stub) has "already
// aborted": its channel is closed, so a second signal doesn't wait on it.
func TestAbortedOfPlainDriver(t *testing.T) {
	select {
	case <-abortedOf(struct{}{}):
	default:
		t.Error("the channel of a Driver without Aborted is open")
	}
	open := make(chan struct{})
	d := abortDriver{open}
	select {
	case <-abortedOf(d):
		t.Error("a Driver's own Aborted channel was replaced")
	default:
	}
}

type abortDriver struct{ ch chan struct{} }

func (a abortDriver) Aborted() <-chan struct{} { return a.ch }

// TestWindowClosedGuards: a closed window ignores a second close, a Start,
// and a signal's quit; a busy one ignores a Start. None of them reaches
// for a widget.
func TestWindowClosedGuards(t *testing.T) {
	w := &Window{closed: true}
	if !w.closeRequest() {
		t.Error("closeRequest on a closed window didn't claim the event")
	}
	w.quitOnSignal()
	if w.quitting {
		t.Error("quitOnSignal on a closed window began quitting")
	}
	w.start("x", false)
	if w.current != "" || w.gen != 0 {
		t.Errorf("a closed window started: current %q gen %d", w.current, w.gen)
	}
	busy := &Window{busy: true}
	busy.start("x", false)
	if busy.current != "" || busy.gen != 0 {
		t.Errorf("a busy window started: current %q gen %d", busy.current, busy.gen)
	}
}

// TestNoWidgetsYetGuards: updates that arrive before the pages exist do
// nothing.
func TestNoWidgetsYetGuards(t *testing.T) {
	w := &Window{}
	w.updateRunBadge()
	w.redrawHeaders()
	if _, ok := w.failedRow(&stepHeader{}); ok {
		t.Error("a header without a section has a failed row")
	}
	w.drawHeader(&stepHeader{})
}

// TestLastRunFolderUnreadableState: a state dir that can't be listed gives
// no folder, not a guess.
func TestLastRunFolderUnreadableState(t *testing.T) {
	f := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &Window{opts: Options{StateDir: f}}
	w.lastRun.Record = &logstore.RunRecord{Action: actions.IDFullCleanup, Outcome: logstore.OutcomeOK, Started: time.Now()}
	if r, ok := w.lastRunFolder(); ok {
		t.Errorf("folder %q from an unreadable state dir", r.Name)
	}
}

// TestStatusTextNotRunAndDropped: the status line counts the steps not run
// and the lines not shown.
func TestStatusTextNotRunAndDropped(t *testing.T) {
	w := &Window{model: runview.NewModel()}
	w.model.Apply([]engine.Event{
		engine.QueueStarted{Action: "a", Steps: []engine.PlanStep{{ID: "s1", Title: "S1"}, {ID: "s2", Title: "S2"}}},
		engine.StepStarted{ID: "s1"},
		engine.OutputChunk{ID: "s1", Lines: []string{"x"}, Dropped: 7},
		engine.StepDone{ID: "s1", Status: steps.OK},
		engine.Finished{Action: "a", Outcome: engine.OutcomeAborted, NotRun: 3},
	})
	got := w.statusText()
	if !strings.Contains(got, "3 not run") || !strings.Contains(got, "7 not shown (in the log)") {
		t.Errorf("status %q", got)
	}
}

// TestPumpDone: a Driver's pump is done once its events ended.
func TestPumpDone(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	d := newRecDriver(0)
	w := sendWindow(d)
	defer closeSendWindow(w)
	if pumpDone(w.pump) {
		t.Fatal("a live pump reads as done")
	}
	w.closed = true
	d.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !pumpDone(w.pump) && time.Now().Before(deadline) {
		runMainLoop(10 * time.Millisecond)
	}
	if !pumpDone(w.pump) {
		t.Fatal("the pump never finished after its Driver closed")
	}
	// send to a done pump drops the command at once.
	w.closed = false
	w.send(engine.Abort{})
	if got := d.sent(); len(got) != 0 {
		t.Errorf("a done Driver got %q", got)
	}
}

// bareTimeline is a Window with the Run page's model and sections but no
// widgets: the list store and string lists are plain GObjects. follow is
// off (no list view to scroll) and the fold idle is marked queued.
func bareTimeline() *Window {
	tl := &timeline{byStep: map[string]*section{}, headers: map[uintptr]*stepHeader{}, foldQueued: true}
	tl.store = gio.NewListStore(gio.GTypeListModel)
	return &Window{model: runview.NewModel(), tl: tl}
}

// feed applies evs to w's model and its timeline the way redraw does
// (rebuild, insert, output).
func feed(w *Window, evs ...engine.Event) runview.Change {
	c := w.model.Apply(evs)
	if c.Reset {
		w.rebuild()
	}
	for _, i := range c.Inserted {
		w.insert(i)
	}
	w.output(c)
	return c
}

func sectionSteps(w *Window) []string {
	var ids []string
	for _, s := range w.tl.sections {
		ids = append(ids, s.step)
	}
	return ids
}

func queue(ids ...string) engine.QueueStarted {
	q := engine.QueueStarted{Action: "a"}
	for _, id := range ids {
		q.Steps = append(q.Steps, engine.PlanStep{ID: id, Title: "Step " + id, LogLabel: "Did " + id})
	}
	return q
}

// consistent checks the timeline's counts against its store.
func consistent(t *testing.T, w *Window) {
	t.Helper()
	if got := int(w.tl.store.NItems()); got != len(w.tl.sections) {
		t.Errorf("store holds %d lists, %d sections", got, len(w.tl.sections))
	}
	for _, s := range w.tl.sections {
		if int(s.list.NItems()) != s.items {
			t.Errorf("section %q: list %d items, counted %d", s.step, s.list.NItems(), s.items)
		}
	}
}

// TestTimelineLocate: a flat position finds its section and offset; past
// the end, or inside a section, there is no section starting there.
func TestTimelineLocate(t *testing.T) {
	w := bareTimeline()
	feed(w, queue("a", "b"), engine.StepStarted{ID: "a"}, engine.OutputChunk{ID: "a", Lines: []string{"1", "2", "3"}})
	tl := w.tl
	// a: 3 lines, b: its summary item.
	if s, off := tl.locate(2); s == nil || s.step != "a" || off != 2 {
		t.Errorf("locate(2) = %v %d", s, off)
	}
	if s, off := tl.locate(3); s == nil || s.step != "b" || off != 0 {
		t.Errorf("locate(3) = %v %d", s, off)
	}
	if s, off := tl.locate(4); s != nil || off != 0 {
		t.Errorf("locate(4) = %v %d, want none past the end", s, off)
	}
	if s := tl.sectionAt(3); s == nil || s.step != "b" {
		t.Errorf("sectionAt(3) = %v", s)
	}
	if s := tl.sectionAt(1); s != nil {
		t.Errorf("sectionAt(1) = %q inside a's lines, want none", s.step)
	}
	if tl.total() != 4 {
		t.Errorf("total %d, want 4", tl.total())
	}
}

// TestRowOfAndWantOpen: a section whose step has no row is open; the user's
// choice wins over the state.
func TestRowOfAndWantOpen(t *testing.T) {
	w := bareTimeline()
	feed(w, queue("a", "b"), engine.StepStarted{ID: "a"})
	if r, i := w.rowOf("nope"); i != -1 || r.ID != "" {
		t.Errorf("rowOf(nope) = %+v %d", r, i)
	}
	orphan := newSection("nope")
	if !w.wantOpen(orphan, "a") {
		t.Error("a section without a row wants to be folded")
	}
	b := w.tl.byStep["b"]
	if w.wantOpen(b, "a") {
		t.Error("a pending step wants to be open")
	}
	b.userSet, b.userOpen = true, true
	if !w.wantOpen(b, "a") {
		t.Error("the user's open was overridden by the pending state")
	}
	a := w.tl.byStep["a"]
	a.userSet, a.userOpen = true, false
	if w.wantOpen(a, "a") {
		t.Error("the user's fold was overridden by the running state")
	}
}

// TestSectionForLinesOutsideAnyStep: output of no step gets a section of
// its own, first, that the user's choice keeps open; rows inserted later
// land after it.
func TestSectionForLinesOutsideAnyStep(t *testing.T) {
	w := bareTimeline()
	feed(w, queue("a", "b"))
	feed(w, engine.OutputChunk{ID: "", Lines: []string{"stray 1", "stray 2"}})
	if got := sectionSteps(w); !slices.Equal(got, []string{"", "a", "b"}) {
		t.Fatalf("sections %q, want the stray one first", got)
	}
	s := w.tl.byStep[""]
	if !s.userSet || !s.userOpen || !s.lines || s.n != 2 || s.items != 2 {
		t.Errorf("stray section %+v", s)
	}
	if got := w.model.StepText(""); !slices.Equal(got, []string{"stray 1", "stray 2"}) {
		t.Errorf("model text %q", got)
	}
	// A step inserted at the head of the plan goes after the stray section.
	feed(w, engine.StepInserted{Index: 0, Step: engine.PlanStep{ID: "new", Title: "New"}})
	if got := sectionSteps(w); !slices.Equal(got, []string{"", "new", "a", "b"}) {
		t.Errorf("after an insert: %q", got)
	}
	consistent(t, w)
	// Rows outside the plan are not inserted.
	n := len(w.tl.sections)
	w.insert(-1)
	w.insert(len(w.model.Rows))
	if len(w.tl.sections) != n {
		t.Errorf("an out-of-range insert added a section: %q", sectionSteps(w))
	}
}

// TestToggleStep: a click on a header folds an open step to its summary
// and opens it again; the choice sticks.
func TestToggleStep(t *testing.T) {
	w := bareTimeline()
	feed(w, queue("a"), engine.StepStarted{ID: "a"}, engine.OutputChunk{ID: "a", Lines: []string{"l1", "l2"}})
	s := w.tl.byStep["a"]
	if !s.lines || s.items != 2 || w.outLen != 2 {
		t.Fatalf("running step: lines %v items %d outLen %d", s.lines, s.items, w.outLen)
	}
	w.toggleStep(s)
	if s.lines || s.items != 1 || w.outLen != 0 || !s.userSet || s.userOpen {
		t.Errorf("folded: lines %v items %d outLen %d userSet %v userOpen %v", s.lines, s.items, w.outLen, s.userSet, s.userOpen)
	}
	if got := s.list.String(0); got != summaryItem {
		t.Errorf("folded list holds %q", got)
	}
	// More output while folded by the user stays folded, and counts.
	feed(w, engine.OutputChunk{ID: "a", Lines: []string{"l3"}})
	if s.lines || s.n != 3 {
		t.Errorf("output into a user-folded step: lines %v n %d", s.lines, s.n)
	}
	w.toggleStep(s)
	if !s.lines || s.items != 3 || w.outLen != 3 || !s.userOpen {
		t.Errorf("reopened: lines %v items %d outLen %d userOpen %v", s.lines, s.items, w.outLen, s.userOpen)
	}
	consistent(t, w)
}

// TestOutputTrimmingAllOfAnOpenStep: when the ring's cap trims every line of
// an open step, it shows its summary item, never an empty list (an empty
// section draws no header).
func TestOutputTrimmingAllOfAnOpenStep(t *testing.T) {
	w := bareTimeline()
	feed(w, queue("a", "b"), engine.StepStarted{ID: "a"}, engine.OutputChunk{ID: "a", Lines: []string{"l1", "l2"}})
	s := w.tl.byStep["a"]
	w.output(runview.Change{Steps: []runview.StepOut{{ID: "a", Remove: 2}}})
	if s.lines || s.n != 0 || s.items != 1 || w.outLen != 0 {
		t.Fatalf("lines %v n %d items %d outLen %d, want the summary item only", s.lines, s.n, s.items, w.outLen)
	}
	if got := s.list.String(0); got != summaryItem || s.list.NItems() != 1 {
		t.Errorf("list holds %d items, first %q", s.list.NItems(), got)
	}
	consistent(t, w)
}

// TestReplaceListOfUnknownSection: a section the timeline doesn't hold is
// left alone.
func TestReplaceListOfUnknownSection(t *testing.T) {
	w := bareTimeline()
	feed(w, queue("a"))
	stray := newSection("zz")
	before := stray.list
	w.replaceList(stray, []string{"x", "y"})
	if stray.list != before || stray.items != 1 || w.tl.store.NItems() != 1 {
		t.Errorf("a foreign section was replaced: items %d, store %d", stray.items, w.tl.store.NItems())
	}
}

// TestFoldNow: the queued fold folds the open steps that no longer want to
// be, and does nothing once the window is closed.
func TestFoldNow(t *testing.T) {
	w := bareTimeline()
	feed(w, queue("a", "b"), engine.StepStarted{ID: "a"}, engine.OutputChunk{ID: "a", Lines: []string{"x"}})
	feed(w, engine.StepDone{ID: "a", Status: steps.OK}, engine.StepStarted{ID: "b"})
	a := w.tl.byStep["a"]
	if !a.lines {
		t.Fatal("a is not open")
	}
	w.closed = true
	w.tl.foldQueued = true
	w.foldNow()
	if !a.lines || w.tl.foldQueued {
		t.Errorf("closed window: a.lines %v foldQueued %v, want it left open and the flag cleared", a.lines, w.tl.foldQueued)
	}
	w.closed = false
	w.foldNow()
	if a.lines || a.items != 1 {
		t.Errorf("a finished step stayed open after a later one started (lines %v)", a.lines)
	}
	consistent(t, w)
}

// TestFmtClock: mm:ss, and h:mm:ss past an hour.
func TestFmtClock(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                              "00:00",
		59 * time.Second:               "00:59",
		61*time.Minute + 5*time.Second: "1:01:05",
		3600 * time.Second:             "1:00:00",
		26*time.Hour + 3*time.Minute + 9*time.Second: "26:03:09",
	} {
		if got := fmtClock(d); got != want {
			t.Errorf("fmtClock(%v) = %q, want %q", d, got, want)
		}
	}
}
