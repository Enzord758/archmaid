package ui

// The headless harness. The GUI tests run the real window in a child
// process (this test binary, re-executed with ARCHMAID_GUI_CHILD set), so
// GTK owns that process's main thread, and the parent only reads the
// child's JSON report. They need a display and are opt-in:
//
//	gui/scripts/headless.sh go test -v ./internal/ui     # weston --backend=headless
//
// headless.sh starts a private headless weston and sets WAYLAND_DISPLAY to
// it plus ARCHMAID_GUI_DISPLAY_TESTS=1; without that variable the tests
// skip, so a plain `go test ./...` never opens a window on the desktop.
// ARCHMAID_GUI_PERF=1 also enforces the performance budgets (they are
// always measured and logged).

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"go.uber.org/goleak"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/stub"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// GTK must own the process's main thread: TestMain runs on the main
// goroutine, and this pins that goroutine to the thread it started on.
func init() { runtime.LockOSThread() }

// A child inherits the parent's testenv (scratch HOME and XDG dirs, the
// privileged-binary traps), so only the parent sets one up.
func TestMain(m *testing.M) {
	if sc := os.Getenv("ARCHMAID_GUI_CHILD"); sc != "" {
		os.Exit(runChild(sc, os.Getenv("ARCHMAID_GUI_REPORT")))
	}
	os.Exit(testenv.Run(m))
}

// report is what a child scenario measured.
type report struct {
	Scenario   string
	FirstFrame int64 // unix ns of the first painted frame
	IdleMem    mem   // 1.5 s after the first frame, before any run
	EndMem     mem   // when the run finished
	Outcome    string
	Rows       []string // "id=state"
	Prompts    []engine.PromptID
	Lines      int // the model's count of output lines
	Dropped    int
	ViewLines  uint // lines in the output list at the end
	LastLine   string
	// Labels are the output rows on screen at the end; Coloured those
	// with Pango attributes.
	Labels, Coloured int
	Pump             runview.Stats
	RunNs            int64 // Start click → Finished applied
	Frames           []time.Duration
	DriverCloses     int32
	Sent             []string // the prompt answers the Driver got
	Leaked           string   // goleak's verdict after Run returned, "" if clean
	Errors           []string
}

// countingDriver counts Close calls and records the prompt answers sent.
type countingDriver struct {
	engine.Driver
	closes *atomic.Int32
	mu     *sync.Mutex
	sent   *[]string
}

func (d countingDriver) Close() { d.closes.Add(1); d.Driver.Close() }

func (d countingDriver) Send(c engine.Command) bool {
	var s string
	switch c := c.(type) {
	case engine.Confirm:
		s = fmt.Sprintf("confirm %d %v", c.Prompt, c.Yes)
	case engine.SubmitPassword:
		// The length only: the harness typed it, and Secret prints redacted.
		s = fmt.Sprintf("password %d len %d", c.Prompt, len(string(c.Password)))
	case engine.Select:
		s = fmt.Sprintf("select %d %v", c.Prompt, c.Picked)
	case engine.CancelPrompt:
		s = fmt.Sprintf("cancel %d", c.Prompt)
	}
	if s != "" {
		d.mu.Lock()
		*d.sent = append(*d.sent, s)
		d.mu.Unlock()
	}
	return d.Driver.Send(c)
}

func runChild(scenario, path string) int {
	DefaultRenderer() // as archmaid-gui's main does
	ignore := goleak.IgnoreCurrent()
	rep := &report{Scenario: scenario}
	fail := func(format string, a ...any) { rep.Errors = append(rep.Errors, fmt.Sprintf(format, a...)) }
	var closes atomic.Int32

	script := stub.FullCleanup(time.Now())
	opts := Options{Flags: gio.ApplicationNonUnique, Action: "fullcleanup"}
	switch scenario {
	case "flood": // unpaced, nothing dropped: the view's real capacity
		script, opts.Action, opts.Pump = runview.Flood(time.Now(), 50000, 1, 0), "flood", runview.PumpOptions{MaxPending: -1}
	case "flood-paced": // 10k lines/s, twice the budget, default pump
		script, opts.Action = runview.Flood(time.Now(), 50000, 100, 10*time.Millisecond), "flood"
	case "flood-default": // unpaced, default pump (drops what the view can't keep)
		script, opts.Action = runview.Flood(time.Now(), 50000, 1, 0), "flood"
	case "flood-colour": // as flood, every line in colour (seven spans)
		script, opts.Action, opts.Pump = runview.FloodColour(time.Now(), 50000, 1, 0), "flood", runview.PumpOptions{MaxPending: -1}
	case "prompts": // selection, up-front password, askpass
		script, opts.Action = runview.Prompts(time.Now()), "services"
	case "audit": // the AUR audit report, accepted
		script, opts.Action = runview.Audit(), "auraudit"
	case "idle", "fullcleanup", "stop", "close-midrun":
	default:
		fail("unknown scenario %q", scenario)
	}
	var sentMu sync.Mutex
	opts.NewDriver = func() engine.Driver {
		return countingDriver{Driver: stub.New(script, stub.Options{}), closes: &closes, mu: &sentMu, sent: &rep.Sent}
	}

	var started time.Time
	var painting time.Time
	measuring := false
	opts.hook = func(w *Window) {
		closeSoon := func() { glib.TimeoutAdd(50, func() { w.win.Close() }) }
		w.win.ConnectRealize(func() {
			fc := gdk.BaseFrameClock(w.win.FrameClock())
			fc.ConnectBeforePaint(func() { painting = time.Now() })
			fc.ConnectAfterPaint(func() {
				if rep.FirstFrame == 0 {
					rep.FirstFrame = time.Now().UnixNano()
					// Idle: let the first frames settle, read RSS, then go.
					glib.TimeoutAdd(1500, func() {
						rep.IdleMem = readMem()
						if scenario == "idle" {
							w.win.Close()
							return
						}
						started, measuring = time.Now(), true
						w.runBtn.Emit("clicked")
					})
				}
				if measuring && !painting.IsZero() {
					rep.Frames = append(rep.Frames, time.Since(painting))
				}
			})
		})
		stopped := false
		w.onApply = func(c runview.Change) {
			if c.Opened != nil {
				id := c.Opened.ID
				rep.Prompts = append(rep.Prompts, id)
				p := *c.Opened
				glib.TimeoutAdd(30, func() {
					d, ok := w.dialogs[id]
					if !ok {
						fail("prompt %d has no dialog", id)
						return
					}
					// Answer the way a user would: type, tick, then press.
					resp := "yes"
					switch {
					case p.Password != nil:
						w.pwEntry.SetText("hunter2")
						resp = "ok"
					case p.Selection != nil:
						w.checks[0].SetActive(true)
						w.checks[1].SetActive(true)
						resp = "ok"
					case p.Report != nil:
						resp = "accept"
					}
					d.Emit("response", resp)
					d.ForceClose()
					if p.Password != nil && w.pwEntry.TextLength() != 0 {
						fail("the password entry wasn't wiped after the answer")
					}
				})
			}
			if !stopped && len(w.model.Rows) > 1 && w.model.Rows[1].State == runview.RowRunning {
				switch scenario {
				case "stop":
					stopped = true
					w.stopBtn.Emit("clicked")
				case "close-midrun":
					stopped = true
					w.win.Close()
					if w.quitDialog == nil || w.closed {
						fail("closing mid-run didn't ask first")
						return
					}
					glib.TimeoutAdd(30, func() { w.quitDialog.Emit("response", "quit"); w.quitDialog.ForceClose() })
				}
			}
			if c.Finished {
				measuring = false
				rep.RunNs = int64(time.Since(started))
				rep.EndMem = readMem()
				m := w.model
				rep.Outcome = m.Finished.Outcome.String()
				for _, r := range m.Rows {
					rep.Rows = append(rep.Rows, r.ID+"="+r.State.String())
				}
				rep.Lines, rep.Dropped, rep.ViewLines = m.Lines, m.Dropped, w.outLen
				if out := m.Output(); len(out) > 0 {
					rep.LastLine = out[len(out)-1]
				}
				rep.Pump = w.pump.Stats()
				// Count the coloured rows once the last batch is drawn.
				glib.TimeoutAdd(20, func() {
					rep.Labels, rep.Coloured = countLabels(&w.outView.Widget)
				})
				if scenario == "close-midrun" {
					fail("the run finished before the window closed")
				}
				closeSoon()
			}
		}
	}

	code := Run([]string{"archmaid-gui-test"}, opts)
	rep.DriverCloses = closes.Load()
	if err := goleak.Find(ignore); err != nil {
		rep.Leaked = err.Error()
	}
	if code != 0 {
		fail("exit status %d", code)
	}
	b, _ := json.Marshal(rep)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return 3
	}
	return 0
}

// countLabels walks the output list's widgets: the labels showing text,
// and how many of them carry attributes (colour).
func countLabels(root *gtk.Widget) (labels, coloured int) {
	for c := root.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		w := gtk.BaseWidget(c)
		if l, ok := c.(*gtk.Label); ok && w.Mapped() && l.Text() != "" {
			labels++
			if l.Attributes() != nil {
				coloured++
			}
		}
		n, k := countLabels(w)
		labels, coloured = labels+n, coloured+k
	}
	return labels, coloured
}

// mem is the process's memory, in KiB: RSS (VmRSS; shared libraries count
// in full), PSS (shared pages split among their users) and Anon (RssAnon:
// heaps, the Go one and GTK's).
type mem struct{ RSS, PSS, Anon int64 }

func (m mem) String() string {
	return fmt.Sprintf("RSS %.0f MB (PSS %.0f, anon %.0f)", float64(m.RSS)/1024, float64(m.PSS)/1024, float64(m.Anon)/1024)
}

func readMem() mem {
	return mem{
		RSS:  procField("/proc/self/status", "VmRSS:"),
		PSS:  procField("/proc/self/smaps_rollup", "Pss:"),
		Anon: procField("/proc/self/status", "RssAnon:"),
	}
}

// procField reads a "Name: value kB" field, or -1.
func procField(path, name string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == name {
			n, _ := strconv.ParseInt(f[1], 10, 64)
			return n
		}
	}
	return -1
}

func needDisplay(t *testing.T) {
	t.Helper()
	if os.Getenv("ARCHMAID_GUI_DISPLAY_TESTS") != "1" {
		t.Skip("needs a display: run through gui/scripts/headless.sh (sets ARCHMAID_GUI_DISPLAY_TESTS=1)")
	}
}

func perf() bool { return os.Getenv("ARCHMAID_GUI_PERF") == "1" }

// child runs one scenario and returns its report and how long after the
// exec its first frame was painted.
func child(t *testing.T, scenario string) (*report, time.Duration) {
	t.Helper()
	path := t.TempDir() + "/report.json"
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "ARCHMAID_GUI_CHILD="+scenario, "ARCHMAID_GUI_REPORT="+path)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: child: %v", scenario, err)
		}
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("%s: child still running after 60s", scenario)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rep := &report{}
	if err := json.Unmarshal(b, rep); err != nil {
		t.Fatal(err)
	}
	for _, e := range rep.Errors {
		t.Errorf("%s: %s", scenario, e)
	}
	if rep.DriverCloses < 1 {
		t.Errorf("%s: Close was never called on the Driver", scenario)
	}
	if rep.Leaked != "" {
		t.Errorf("%s: goroutines left after the window closed:\n%s", scenario, rep.Leaked)
	}
	var first time.Duration
	if rep.FirstFrame != 0 {
		first = time.Unix(0, rep.FirstFrame).Sub(start)
	}
	return rep, first
}

func TestGUIFullCleanup(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "fullcleanup")
	if want := []string{"confirm 1 true", "confirm 2 true", "confirm 3 true"}; !slices.Equal(rep.Sent, want) {
		t.Errorf("sent %q, want %q", rep.Sent, want)
	}
	if rep.Outcome != "completed" || !slices.Equal(rep.Prompts, []engine.PromptID{1, 2, 3}) {
		t.Errorf("outcome %q prompts %v; want completed after prompts 1, 2, 3", rep.Outcome, rep.Prompts)
	}
	if len(rep.Rows) != 11 || rep.Rows[5] != steps.IDOrphansRemove+"=ok" {
		t.Errorf("rows %v", rep.Rows)
	}
	for _, r := range rep.Rows {
		if !strings.HasSuffix(r, "=ok") {
			t.Errorf("row %s", r)
		}
	}
}

// TestGUIPrompts: the selection, password and askpass dialogs answer
// through Select and SubmitPassword with their prompt IDs.
func TestGUIPrompts(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "prompts")
	want := []string{"select 1 [cups.service avahi-daemon.service]", "password 2 len 7", "password 3 len 7"}
	if rep.Outcome != "completed" || !slices.Equal(rep.Sent, want) {
		t.Errorf("outcome %q sent %q; want completed after %q", rep.Outcome, rep.Sent, want)
	}
}

// TestGUIAudit: the audit report's Accept is Confirm(yes) on its ID.
func TestGUIAudit(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "audit")
	if rep.Outcome != "report" || !slices.Equal(rep.Sent, []string{"confirm 1 true"}) {
		t.Errorf("outcome %q sent %q; want report after confirm 1 true", rep.Outcome, rep.Sent)
	}
}

func TestGUIStop(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "stop")
	if rep.Outcome != "aborted" || len(rep.Rows) < 2 || rep.Rows[1] != steps.IDTopgrade+"=aborted" {
		t.Errorf("outcome %q rows %v; want aborted in topgrade", rep.Outcome, rep.Rows)
	}
}

func TestGUICloseMidRun(t *testing.T) {
	needDisplay(t)
	child(t, "close-midrun")
}

// TestGUIFirstFrameAndIdleRAM: budgets < 400 ms to the first frame (warm:
// the best of the runs after the first) and < 120 MB idle.
func TestGUIFirstFrameAndIdleRAM(t *testing.T) {
	needDisplay(t)
	var firsts []time.Duration
	var idle mem
	for range 4 {
		rep, first := child(t, "idle")
		firsts = append(firsts, first)
		if rep.IdleMem.RSS > idle.RSS {
			idle = rep.IdleMem
		}
	}
	warm := slices.Min(firsts[1:])
	t.Logf("first frame: %v (warm best %v); idle %v", firsts, warm, idle)
	rss := idle.RSS
	if perf() {
		if warm >= 400*time.Millisecond {
			t.Errorf("warm first frame %v, budget < 400 ms", warm)
		}
		if rss >= 120*1024 {
			t.Errorf("idle RSS %.1f MB, budget < 120 MB", float64(rss)/1024)
		}
	}
}

// TestGUIThroughput: 50k lines through the real window. Budgets: ≥ 5k
// lines/s, main-thread work per batch p95 ≤ 4 ms, RSS after < 200 MB.
func TestGUIThroughput(t *testing.T) {
	needDisplay(t)
	for _, sc := range []string{"flood", "flood-paced", "flood-default", "flood-colour"} {
		rep, _ := child(t, sc)
		s := rep.Pump
		span := s.LastLine.Sub(s.FirstLine)
		run := time.Duration(rep.RunNs)
		rate := float64(rep.Lines) / run.Seconds()
		slices.Sort(rep.Frames)
		var fp95, fmax time.Duration
		if n := len(rep.Frames); n > 0 {
			fp95, fmax = rep.Frames[min(n*95/100, n-1)], rep.Frames[n-1]
		}
		t.Logf("%s: %d lines (%d dropped, %d in view) in %v (first→last line %v) = %.0f lines/s; %d flushes, apply p95 %v max %v; %d frames, frame work p95 %v max %v; idle %v → after %v",
			sc, rep.Lines, rep.Dropped, rep.ViewLines, run.Round(time.Millisecond), span.Round(time.Millisecond), rate,
			s.Flushes, s.P95(), s.Max(), len(rep.Frames), fp95, fmax, rep.IdleMem, rep.EndMem)
		last := runview.FloodLine(50000, 50000)
		if sc == "flood-colour" {
			last = runview.FloodColourLine(50000, 50000)
			t.Logf("%s: %d of %d visible output rows coloured", sc, rep.Coloured, rep.Labels)
			if rep.Labels == 0 || rep.Coloured != rep.Labels {
				t.Errorf("%s: %d of %d visible output rows coloured, want all", sc, rep.Coloured, rep.Labels)
			}
		}
		if rep.Outcome != "completed" || rep.Lines != 50000 || rep.ViewLines != runview.OutputCap || rep.LastLine != last {
			t.Errorf("%s: outcome %q, %d lines, %d in view ending %q", sc, rep.Outcome, rep.Lines, rep.ViewLines, rep.LastLine)
		}
		if sc != "flood-default" && rep.Dropped != 0 {
			t.Errorf("%s: %d lines dropped, want none", sc, rep.Dropped)
		}
		if !perf() {
			continue
		}
		if sc == "flood" && rate < 5000 {
			t.Errorf("%s: %.0f lines/s, budget ≥ 5000", sc, rate)
		}
		if s.P95() > 4*time.Millisecond {
			t.Errorf("%s: main-thread work per batch p95 %v, budget ≤ 4 ms", sc, s.P95())
		}
		if rep.EndMem.RSS >= 200*1024 {
			t.Errorf("%s: after 50k lines %v, budget RSS < 200 MB", sc, rep.EndMem)
		}
	}
}
