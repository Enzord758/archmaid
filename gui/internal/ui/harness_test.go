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
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"go.uber.org/goleak"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/realdriver"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/gui/internal/settings"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/stub"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// GTK must own the process's main thread: TestMain runs on the main
// goroutine, and this pins that goroutine to the thread it started on.
func init() { runtime.LockOSThread() }

// A child inherits the parent's testenv (scratch HOME and XDG dirs, the
// privileged-binary traps), so only the parent sets one up.
func TestMain(m *testing.M) {
	// The askpass relay, as archmaid-gui's main: the real-Driver scenarios'
	// sudo shim runs the wrapper, which execs this binary.
	if code, relayed := realdriver.Relay(os.Args[1:]); relayed {
		os.Exit(code)
	}
	if mode := os.Getenv(envInstance); mode != "" {
		os.Exit(runInstanceChild(mode))
	}
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
	Buttons          []string // each confirm's "id yes/no" button labels
	PwBodies         []string // each password dialog's body
	// DriverOpts is each Driver's engine.Options, as made: dry run and
	// the downloads Flag.
	DriverOpts []string
	// The Settings page: the folder row's error after each apply ("" when
	// kept), and the problem line at the end.
	DlErrors []string
	Problem  string
	Toasts   []string
	Menu     []string // the Overview's rows
	MenuSub  string   // the started row's subtitle right after the click
	Page     string   // the page up after starting an action
	LastRun  []string // the Overview's last run: title, then issues
	LogRows  []string // the Logs page's "run/category" rows
	LogShown string   // the log opened, and its lines
	LogLines []string
	Notice   string
	// The window's title, the stub's banner if revealed, and the status
	// line, at the first frame.
	Title, Banner, FirstStatus string
	// Dumpable (prctl PR_GET_DUMPABLE) and CoreLimit (RLIMIT_CORE's soft
	// limit) in a real-Driver child, set up as main does.
	Dumpable  int
	CoreLimit uint64
	// EndStatus is the status line once the run finished; Statuses every
	// status line shown, once.
	EndStatus string
	Statuses  []string
	// Geometry is real-small's orphan dialog as shown (headingGeometry).
	Geometry string
	// Outcomes is every run's outcome, in order (real-runs runs several).
	Outcomes []string
	// Withdrawn is the askpass entry typed into, after the engine
	// withdrew its dialog (engine-stops-askpass): its length, and whether
	// the window still holds it.
	Withdrawn string
	// Controls is the window's state right after the engine stopped
	// (engine-stops): busy, Stop, the menu row, open dialogs, status.
	Controls string
	// Subtitles is every "title: subtitle" the run's events gave a
	// checklist row, once, in order (real- scenarios; see rowLines).
	Subtitles []string
	Leaked    string // goleak's verdict after Run returned, "" if clean
	Errors    []string
}

// countingDriver counts Close calls and records the prompt answers sent;
// with lines set, it also records the row lines (rowLines).
type countingDriver struct {
	engine.Driver
	closes *atomic.Int32
	mu     *sync.Mutex
	sent   *[]string
	lines  *rowLines
}

// rowLines tees a Driver's events to record every row line they give,
// each event folded on its own into a model of its own (the window's
// code: runview.Model and Row.Subtitle). The window's pump folds whatever
// queued between two flushes in one batch, so a line that comes and goes
// within a batch is never drawn although the run gave it: the polkit wait
// with the faillock warning, which the shim's pkexec refuses at once, on a
// loaded runner (CI) left no trace in what the rows showed.
type rowLines struct {
	once sync.Once
	out  <-chan engine.Event
	mu   *sync.Mutex
	got  *[]string
}

// Events is the Driver's, through the tee.
func (d countingDriver) Events() <-chan engine.Event {
	if d.lines == nil {
		return d.Driver.Events()
	}
	l := d.lines
	l.once.Do(func() {
		in := d.Driver.Events()
		out := make(chan engine.Event, cap(in))
		l.out = out
		go func() {
			defer close(out)
			m := runview.NewModel()
			for ev := range in {
				if _, ok := ev.(engine.OutputChunk); !ok { // no row line in it
					c := m.Apply([]engine.Event{ev})
					rows := append(c.Inserted, c.Rows...)
					if c.Reset {
						rows = rows[:0]
						for i := range m.Rows {
							rows = append(rows, i)
						}
					}
					l.mu.Lock()
					for _, i := range rows {
						if s := m.Rows[i].Title + ": " + m.Rows[i].Subtitle(); !slices.Contains(*l.got, s) {
							*l.got = append(*l.got, s)
						}
					}
					l.mu.Unlock()
				}
				out <- ev
			}
		}()
	})
	return l.out
}

func (d countingDriver) Close() { d.closes.Add(1); d.Driver.Close() }

// Aborted passes the real Driver's through (signals.go waits for it).
func (d countingDriver) Aborted() <-chan struct{} { return abortedOf(d.Driver) }

// KillNow passes the real Driver's through (signals.go's second signal).
func (d countingDriver) KillNow() {
	if k, ok := d.Driver.(killer); ok {
		k.KillNow()
	}
}

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

	// Most scenarios start a menu action from the Overview against the
	// stub's Catalog; the flood and password ones play a single script.
	var script stub.Script
	opts := Options{Flags: gio.ApplicationNonUnique, StateDir: os.Getenv("ARCHMAID_GUI_STATE")}
	action, answerNo, password := actions.IDFullCleanup, false, "hunter2"
	cancelPassword := false
	switch scenario {
	case "flood": // unpaced, nothing dropped: the view's real capacity
		script, action, opts.Pump = runview.Flood(time.Now(), 50000, 1, 0), "flood", runview.PumpOptions{MaxPending: -1}
	case "flood-paced": // 10k lines/s, twice the budget, default pump
		script, action = runview.Flood(time.Now(), 50000, 100, 10*time.Millisecond), "flood"
	case "flood-default": // unpaced, default pump (drops what the view can't keep)
		script, action = runview.Flood(time.Now(), 50000, 1, 0), "flood"
	case "flood-colour": // as flood, every line in colour (seven spans)
		script, action, opts.Pump = runview.FloodColour(time.Now(), 50000, 1, 0), "flood", runview.PumpOptions{MaxPending: -1}
	case "services": // the services pick, two ticked
		action = actions.IDServices
	case "uncached": // the up-front password as the gate, right first time
		action = stub.IDFullCleanupUncached
	case "wrong-password": // the up-front password, wrong three times
		action, password = stub.IDFullCleanupUncached, string(stub.WrongPassword)
	case "askpass": // sudo's askpass mid-topgrade
		action = stub.IDFullCleanupAskpass
	case "audit": // the AUR audit report, accepted
		action = actions.IDAURAudit
	case "usercache-no": // a declined required step
		action, answerNo = actions.IDUserCache, true
	case "repair": // a discovery that ends in a notice
		action = actions.IDRepair
	case "settings": // home refused, a folder and dry run kept, then a run
		action = actions.IDRepair
	case "settings-bad": // gui.json holds the home folder: refused on load
		action = actions.IDRepair
	case "idle", "logs", "fullcleanup", "stop", "close-midrun":
	case "engine-stops", "engine-stops-rerun": // the Driver dies at the second step
	case "engine-stops-askpass": // ... with sudo's askpass dialog open, typed in
		action = stub.IDFullCleanupAskpass
	case "real-dry", "real-refused", "real-locked", "real-close", "real-tty", "real-small", // realdriver_harness_test.go
		"real-one-password", "real-agent-missing", "real-foreign-first":
	case "real-runs": // realRuns real topgrade runs in one window, then a dry one
		action = actions.IDTopgrade
	case "real-askpass": // topgrade's sudo asks through the relay
		action = actions.IDTopgrade
	case "real-askpass-cancel":
		action, cancelPassword = actions.IDTopgrade, true
	case "real-close-topgrade": // Run topgrade, then Stop and Quit mid-topgrade
		action = actions.IDTopgrade
	case "real-close-finished": // the question asked mid-topgrade, answered after its end
		action = actions.IDTopgrade
	case "real-close-polkit": // Full Cleanup; Stop and Quit while pkexec waits
	case "real-sigint", "real-sighup", "real-sigint-twice": // Run topgrade, then the signal mid-topgrade
		action = actions.IDTopgrade
	default:
		fail("unknown scenario %q", scenario)
	}
	var wiring *realdriver.Wiring
	if strings.HasPrefix(scenario, "real-") {
		// As archmaid-gui's main: no core files, and still dumpable.
		if err := NoCoreFiles(); err != nil {
			fail("NoCoreFiles: %v", err)
		}
		rep.Dumpable, rep.CoreLimit = dumpable(), coreRlimit()
		wiring = startRealWiring(fail)
		opts.Engine = wiring.Options()
		opts.KeepsPassword, opts.Notices = true, wiring.SetNotice // as mainReal
	} else {
		opts.Stub = true // as Main runs it for --stub
	}
	var sentMu sync.Mutex
	var died atomic.Bool // engine-stops: the one Driver that dies has
	kill := make(chan struct{})
	killDriver := sync.OnceFunc(func() { close(kill) })
	var typed *adw.PasswordEntryRow // engine-stops-askpass: the entry typed into
	opts.NewDriver = func(o engine.Options) engine.Driver {
		flag := "<none>"
		if o.Env.Downloads != nil {
			flag = o.Env.Downloads().Flag
		}
		rep.DriverOpts = append(rep.DriverOpts, fmt.Sprintf("dry=%v downloads=%q", o.DryRun, flag))
		var d engine.Driver
		if wiring != nil {
			d = wiring.New(o)
		} else {
			so := stub.Options{}
			if script == nil {
				so.Scripts = stub.Catalog(time.Now())
			}
			d = stub.New(script, so)
			if strings.HasPrefix(scenario, "engine-stops") {
				d = newDyingDriver(d, &died, scenario == "engine-stops-askpass", kill)
			}
		}
		cd := countingDriver{Driver: d, closes: &closes, mu: &sentMu, sent: &rep.Sent}
		if wiring != nil {
			cd.lines = &rowLines{mu: &sentMu, got: &rep.Subtitles}
		}
		return cd
	}

	var started time.Time
	var painting time.Time
	measuring := false
	opts.hook = func(w *Window) {
		closeSoon := func() { glib.TimeoutAdd(50, func() { w.win.Close() }) }
		if scenario == "real-small" {
			w.win.SetDefaultSize(640, 370) // half a 1280x800 screen, split
		}
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
						for _, e := range overview.Entries() {
							rep.Menu = append(rep.Menu, e.ID)
						}
						rep.LastRun = append([]string{w.lastRun.Title}, w.lastRun.Issues...)
						rep.Title, rep.FirstStatus = w.win.Title(), w.status.Text()
						if w.banner != nil && w.banner.Revealed() {
							rep.Banner = w.banner.Title()
						}
						if scenario == "logs" {
							browseLogs(w, rep, fail)
							return
						}
						if scenario == "settings-bad" && w.set.dlError.Visible() {
							rep.DlErrors = append(rep.DlErrors, w.set.dlError.Text())
						}
						if scenario == "settings" {
							changeSettings(w, rep)
						}
						started, measuring = time.Now(), true
						// Start it the way a user does: its Overview row.
						if r, ok := w.menu[action]; ok {
							r.row.Emit("activated")
							if rep.Page = w.stack.VisibleChildName(); rep.Page != "run" {
								fail("starting %s left the %q page up", action, rep.Page)
							}
							rep.MenuSub = r.row.Subtitle()
						} else {
							w.startAction(action)
						}
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
				delay := uint(30)
				small := scenario == "real-small" && p.Confirm != nil && len(p.Confirm.Direct) > 0
				if small {
					delay = 800 // shown, animated in and focused
				}
				glib.TimeoutAdd(delay, func() {
					d, ok := w.dialogs[id]
					if !ok {
						fail("prompt %d has no dialog", id)
						return
					}
					if small {
						rep.Geometry = headingGeometry(w, d)
					}
					// Answer the way a user would: type, tick, then press.
					resp := "yes"
					if answerNo {
						resp = "no"
					}
					if p.Confirm != nil {
						rep.Buttons = append(rep.Buttons, fmt.Sprintf("%d %s/%s", id, d.ResponseLabel("yes"), d.ResponseLabel("no")))
					}
					switch {
					case p.Password != nil:
						// The heading, the caller line, then the body.
						caller := ""
						if w.pwCaller != nil {
							caller = w.pwCaller.Label()
						}
						rep.PwBodies = append(rep.PwBodies, d.Heading()+"\n"+caller+"\n"+d.Body())
						w.pwEntry.SetText(password)
						if scenario == "engine-stops-askpass" {
							// Typed, never answered: the engine withdraws it.
							typed = w.pwEntry
							killDriver()
							return
						}
						resp = "ok"
						if cancelPassword {
							resp = "cancel"
						}
					case p.Selection != nil:
						w.checks[0].SetActive(true)
						w.checks[1].SetActive(true)
						resp = "ok"
					case p.Report != nil:
						resp = "accept"
					}
					entry := w.pwEntry
					// Click the response's button, as a user does:
					// libadwaita closes the dialog, then emits the
					// response.
					if b := findButton(&d.Widget, d.ResponseLabel(resp)); b != nil {
						b.Emit("clicked")
					} else {
						fail("prompt %d has no %q button", id, d.ResponseLabel(resp))
						d.Emit("response", resp)
						d.ForceClose()
					}
					if p.Password != nil && (entry.TextLength() != 0 || w.pwEntry != nil) {
						fail("the password entry wasn't wiped and forgotten after the answer")
					}
				})
			}
			if s := w.status.Text(); !slices.Contains(rep.Statuses, s) {
				rep.Statuses = append(rep.Statuses, s)
			}
			if !stopped && len(w.model.Rows) > 1 && w.model.Rows[1].State == runview.RowRunning && childStarted(scenario) {
				switch scenario {
				case "stop":
					stopped = true
					w.stopBtn.Emit("clicked")
				case "real-sigint", "real-sighup", "real-sigint-twice":
					// As from the terminal or a kill: to this process,
					// the GUI's, by the kernel.
					stopped = true
					sig := syscall.SIGINT
					if scenario == "real-sighup" {
						sig = syscall.SIGHUP
					}
					if err := syscall.Kill(os.Getpid(), sig); err != nil {
						fail("kill: %v", err)
					}
					if scenario == "real-sigint-twice" {
						// ctrl+c again while the first one's teardown
						// waits out the leftover's grace: the process
						// exits as soon as the record is written, with
						// no report. (Later, not back to back: two
						// SIGINTs pending at once are one.)
						go func() {
							time.Sleep(20 * time.Millisecond)
							_ = syscall.Kill(os.Getpid(), sig)
						}()
					}
				case "close-midrun", "real-close", "real-close-topgrade", "real-close-finished":
					stopped = true
					w.win.Close()
					if w.quitDialog == nil || w.closed {
						fail("closing mid-run didn't ask first")
						return
					}
					if scenario == "real-close-finished" {
						return // answered once the run has finished
					}
					glib.TimeoutAdd(30, func() { stopAndQuit(w, fail) })
				}
			}
			// real-close-polkit: the window's X while the helper step's
			// pkexec waits for the polkit dialog. Polkit's agent covers
			// our window then, so there is no question: it quits at once.
			if !stopped && scenario == "real-close-polkit" && w.model.Auth != nil && w.model.Auth.Kind == engine.WaitPolkit && pkexecWaiting() {
				stopped = true
				w.win.Close()
				if w.quitDialog != nil {
					fail("closing during the polkit wait asked, under polkit's dialog")
					glib.TimeoutAdd(30, func() { stopAndQuit(w, fail) })
					return
				}
				if !w.closed {
					fail("closing during the polkit wait left the window open")
					w.quitOnSignal()
				}
			}
			if c.Finished {
				measuring = false
				rep.RunNs = int64(time.Since(started))
				rep.EndMem = readMem()
				m := w.model
				rep.Outcome = "stopped" // the engine stopped: no Finished
				if m.Finished != nil {
					rep.Outcome = m.Finished.Outcome.String()
				}
				rep.Notice = m.Notice
				for _, r := range m.Rows {
					rep.Rows = append(rep.Rows, r.ID+"="+r.State.String())
				}
				rep.Lines, rep.Dropped, rep.ViewLines = m.Lines, m.Dropped, w.outLen
				if out := m.Output(); len(out) > 0 {
					rep.LastLine = out[len(out)-1]
				}
				rep.Pump = w.pump.Stats()
				rep.Toasts = w.toasted
				rep.EndStatus = w.status.Text()
				// Count the coloured rows once the last batch is drawn.
				glib.TimeoutAdd(20, func() {
					rep.Labels, rep.Coloured = countLabels(&w.outView.Widget)
				})
				switch scenario {
				case "close-midrun", "real-close", "real-close-topgrade", "real-sigint", "real-sighup", "real-sigint-twice":
					fail("the run finished before the window closed")
				}
				rep.Outcomes = append(rep.Outcomes, rep.Outcome)
				if scenario == "real-runs" && len(rep.Outcomes) <= realRuns {
					// The next run, from its Overview row again; the last
					// one with dry run switched on in Settings.
					glib.TimeoutAdd(50, func() {
						if len(rep.Outcomes) == realRuns {
							w.set.dryRun.SetActive(true)
						}
						rep.Rows = nil
						w.menu[action].row.Emit("activated")
					})
					return
				}
				if strings.HasPrefix(scenario, "engine-stops") && len(rep.Outcomes) == 1 {
					rep.Controls = fmt.Sprintf("busy=%v stop=%v menu=%v dialogs=%d status=%q",
						w.busy, w.stopBtn.Sensitive(), w.menu[actions.IDFullCleanup].row.Sensitive(), len(w.dialogs), w.status.Text())
					if typed != nil {
						rep.Withdrawn = fmt.Sprintf("len=%d kept=%v", typed.TextLength(), w.pwEntry != nil)
					}
					if scenario == "engine-stops-rerun" {
						glib.TimeoutAdd(50, func() {
							rep.Rows = nil
							w.menu[action].row.Emit("activated")
						})
						return
					}
					// Closing must not ask "Stop the run and quit?".
					glib.TimeoutAdd(50, func() {
						w.win.Close()
						if w.quitDialog != nil || !w.closed {
							fail("closing after the engine stopped didn't close the window")
						}
					})
					return
				}
				if scenario == "real-close-finished" {
					// The run ended under the open question: Stop and
					// Quit must still quit.
					if w.quitDialog == nil {
						fail("the quit dialog went with the run's end")
						closeSoon()
						return
					}
					glib.TimeoutAdd(50, func() { stopAndQuit(w, fail) })
					return
				}
				closeSoon()
			}
		}
	}

	code := Run([]string{"archmaid-gui-test"}, opts)
	rep.DriverCloses = closes.Load()
	if wiring != nil {
		wiring.Close()
		closeFakeSystem()
	}
	if err := goleak.Find(ignore); err != nil {
		rep.Leaked = err.Error()
	}
	want := 0
	switch scenario {
	case "real-sigint":
		want = 128 + int(syscall.SIGINT)
	case "real-sighup":
		want = 128 + int(syscall.SIGHUP)
	}
	if code != want {
		fail("exit status %d, want %d", code, want)
	}
	sentMu.Lock()
	b, _ := json.Marshal(rep)
	sentMu.Unlock()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return 3
	}
	return 0
}

// browseLogs opens the Logs page from the Overview's View log row, opens
// the log ARCHMAID_GUI_LOG names ("run/category"), records what it shows,
// then closes the window.
func browseLogs(w *Window, rep *report, fail func(string, ...any)) {
	w.menu[actions.IDViewLog].row.Emit("activated")
	if rep.Page = w.stack.VisibleChildName(); rep.Page != "logs" {
		fail("View log left the %q page up", rep.Page)
	}
	for k := range w.logs.rows {
		rep.LogRows = append(rep.LogRows, k)
	}
	slices.Sort(rep.LogRows)
	if r, ok := w.logs.rows[os.Getenv("ARCHMAID_GUI_LOG")]; ok {
		r.Emit("activated")
	}
	rep.LogShown = w.logs.shown
	for i := range w.logs.textLen {
		rep.LogLines = append(rep.LogLines, w.logs.text.String(i))
	}
	glib.TimeoutAdd(50, func() { w.win.Close() })
}

// countLabels walks the output list's widgets: the labels showing text,
// and how many of them carry attributes (colour).
// changeSettings works the Settings page the way a user does: the home
// folder typed and applied (refused), then ARCHMAID_GUI_DL (kept), then dry
// run switched on. The run that follows must get a fresh Driver.
func changeSettings(w *Window, rep *report) {
	w.stack.SetVisibleChildName("settings")
	p := w.set
	shown := func() string {
		if p.dlError.Visible() {
			return p.dlError.Text()
		}
		return ""
	}
	for _, folder := range []string{os.Getenv("HOME"), os.Getenv("ARCHMAID_GUI_DL")} {
		p.downloads.SetText(folder)
		p.downloads.Emit("apply")
		rep.DlErrors = append(rep.DlErrors, shown())
	}
	p.dryRun.SetActive(true)
	if p.problem.Visible() {
		rep.Problem = p.problem.Text()
	}
}

// stopAndQuit answers the open "Stop the run and quit?" the way a user
// does: it clicks its Stop and Quit button (libadwaita then closes the
// dialog first and emits its response after, which Emit("response") and
// ForceClose in that order never showed). The window must then go: if it
// is still up a second later, that fails and the harness closes it so
// the child still ends.
func stopAndQuit(w *Window, fail func(string, ...any)) {
	d := w.quitDialog
	if d == nil {
		fail("no Stop the run and quit? dialog to answer")
		return
	}
	b := findButton(&d.Widget, "Stop and Quit")
	if b == nil {
		fail("the quit dialog has no Stop and Quit button")
		return
	}
	b.Emit("clicked")
	glib.TimeoutAdd(1000, func() {
		if !w.closed {
			fail("Stop and Quit left the window open")
			w.quitOnSignal()
		}
	})
}

// findButton is the button under root labelled label, or nil.
func findButton(root *gtk.Widget, label string) *gtk.Button {
	for c := root.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		if b, ok := c.(*gtk.Button); ok && b.Label() == label {
			return b
		}
		if b := findButton(gtk.BaseWidget(c), label); b != nil {
			return b
		}
	}
	return nil
}

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
func child(t *testing.T, scenario string, env ...string) (*report, time.Duration) {
	t.Helper()
	return childCmd(t, scenario, nil, env...)
}

// childCmd is child, with prep (when set) applied to the child's command
// before it starts.
func childCmd(t *testing.T, scenario string, prep func(*exec.Cmd), env ...string) (*report, time.Duration) {
	t.Helper()
	path := t.TempDir() + "/report.json"
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), "ARCHMAID_GUI_CHILD="+scenario, "ARCHMAID_GUI_REPORT="+path), env...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if prep != nil {
		prep(cmd)
	}
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
	if want := []string{"1 Proceed/Cancel", "2 Delete/Skip", "3 Remove/Skip"}; !slices.Equal(rep.Buttons, want) {
		t.Errorf("confirm buttons %q, want %q", rep.Buttons, want)
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
	// A stub window says so: the banner, the title, the status line.
	if rep.Banner != StubBanner || rep.Title != "archmaid (stub)" || !strings.HasPrefix(rep.FirstStatus, "STUB · ") {
		t.Errorf("banner %q title %q status %q: want the stub marked as a demo", rep.Banner, rep.Title, rep.FirstStatus)
	}
}

// TestGUIPrompts: the services pick, the up-front password (right, and
// wrong three times) and sudo's askpass, from the stub's Catalog, answer
// through Select, SubmitPassword and Confirm with their prompt IDs.
func TestGUIPrompts(t *testing.T) {
	needDisplay(t)
	for _, c := range []struct {
		scenario, outcome string
		sent              []string
	}{
		{"services", "completed", []string{"select 1 [bluetooth.service cups.service]"}},
		{"uncached", "completed", []string{"password 1 len 7", "confirm 2 true", "confirm 3 true"}},
		// Three wrong ones: the steps that need root are refused, the rest run.
		{"wrong-password", "completed", []string{"password 1 len 5", "password 2 len 5", "password 3 len 5"}},
		// The askpass comes mid-topgrade, between the gate and the confirms.
		{"askpass", "completed", []string{"confirm 1 true", "password 2 len 7", "confirm 3 true", "confirm 4 true"}},
	} {
		rep, _ := child(t, c.scenario)
		if rep.Outcome != c.outcome || !slices.Equal(rep.Sent, c.sent) {
			t.Errorf("%s: outcome %q sent %q; want %q after %q", c.scenario, rep.Outcome, rep.Sent, c.outcome, c.sent)
		}
		switch c.scenario {
		case "wrong-password":
			if len(rep.Rows) < 2 || rep.Rows[1] != steps.IDTopgrade+"=auth refused" {
				t.Errorf("%s: rows %q, want topgrade refused", c.scenario, rep.Rows)
			}
			if len(rep.PwBodies) != 3 || !strings.Contains(rep.PwBodies[1], "Incorrect password") || !strings.Contains(rep.PwBodies[2], "1 attempt") {
				t.Errorf("%s: password dialogs said %q; want the error on the retries", c.scenario, rep.PwBodies)
			}
		case "askpass":
			if len(rep.PwBodies) != 1 || !strings.Contains(rep.PwBodies[0], "yay (pid 4242)") {
				t.Errorf("%s: askpass dialog said %q; want who asked", c.scenario, rep.PwBodies)
			}
		}
	}
}

// TestGUIAudit: the audit report's Accept is Confirm(yes) on its ID.
func TestGUIAudit(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "audit")
	if rep.Outcome != "report" || !slices.Equal(rep.Sent, []string{"confirm 1 true"}) {
		t.Errorf("outcome %q sent %q; want report after confirm 1 true", rep.Outcome, rep.Sent)
	}
	if !slices.Contains(rep.Toasts, "Maintainer changes accepted: they won't be flagged again.") {
		t.Errorf("toasts %q: ReportAccepted's text wasn't shown", rep.Toasts)
	}
}

// TestGUIOverviewStarts: an action started from its Overview row (the
// menu is actions.Menu without Exit and Edit config) shows the Run page.
func TestGUIOverviewStarts(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "repair")
	want := []string{"fullcleanup", "usercache", "repair", "downloads", "topgrade", "services", "auraudit", "viewlog"}
	if !slices.Equal(rep.Menu, want) {
		t.Errorf("menu %q, want %q", rep.Menu, want)
	}
	if rep.Page != "run" || rep.Outcome != "notice" || rep.Notice != "No broken packages found." {
		t.Errorf("page %q outcome %q notice %q", rep.Page, rep.Outcome, rep.Notice)
	}
	if rep.MenuSub == "" {
		t.Errorf("the started row says nothing while it runs")
	}
	if len(rep.LastRun) != 1 || rep.LastRun[0] != "No run recorded yet" {
		t.Errorf("last run %q with an empty state dir", rep.LastRun)
	}
}

// TestGUIDeclined: "no" at a required step's confirm (the Catalog's
// declined path) ends the run aborted with the step skipped.
func TestGUIDeclined(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "usercache-no")
	if !slices.Equal(rep.Sent, []string{"confirm 1 false"}) || !slices.Equal(rep.Buttons, []string{"1 Clear/Cancel"}) {
		t.Errorf("sent %q buttons %q", rep.Sent, rep.Buttons)
	}
	if rep.Outcome != "aborted" || !slices.Equal(rep.Rows, []string{steps.IDUserCache + "=skipped"}) {
		t.Errorf("outcome %q rows %q", rep.Outcome, rep.Rows)
	}
}

// TestGUILogsAndLastRun: the Overview reads last-run.json (20 days old: the
// stale warning) and View log lists the runs and opens one, all from a
// scratch state dir that the window leaves exactly as it was.
func TestGUILogsAndLastRun(t *testing.T) {
	needDisplay(t)
	dir := filepath.Join(t.TempDir(), "state")
	old := time.Now().Add(-20 * 24 * time.Hour).Truncate(time.Minute)
	st := logstore.New(logstore.Options{Dir: dir, Now: func() time.Time { return old }})
	if err := st.Append(logstore.Topgrade, "OK", "Topgrade completed", "\x1b[1m==>\x1b[0m done\n"); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteLastRun(logstore.RunRecord{Action: actions.IDFullCleanup, Outcome: logstore.OutcomeFailed, StepsFailed: 1, Started: old, Finished: old}); err != nil {
		t.Fatal(err)
	}
	dry := logstore.New(logstore.Options{Dir: dir, DryRun: true})
	if err := dry.Append(logstore.Cleanup, "DRYRUN", "Clearing cache", ""); err != nil {
		t.Fatal(err)
	}
	before := stateTree(t, dir)
	run := old.Format("20060102-1504")
	rep, _ := child(t, "logs", "ARCHMAID_GUI_STATE="+dir, "ARCHMAID_GUI_LOG="+run+"/topgrade")
	if want := []string{"Full Cleanup (Recommended) · 20 days ago", "Last run failed (1 step)", "No cleanup for 20 days"}; !slices.Equal(rep.LastRun, want) {
		t.Errorf("last run %q, want %q", rep.LastRun, want)
	}
	if want := []string{run + "/topgrade", "dryrun/cleanup"}; rep.Page != "logs" || !slices.Equal(rep.LogRows, want) {
		t.Errorf("page %q log rows %q, want logs and %q", rep.Page, rep.LogRows, want)
	}
	if rep.LogShown != run+"/topgrade" || len(rep.LogLines) != 4 || rep.LogLines[2] != "==> done" {
		t.Errorf("shown %q lines %q", rep.LogShown, rep.LogLines)
	}
	if after := stateTree(t, dir); !slices.Equal(before, after) {
		t.Errorf("the GUI changed the state dir:\n%q\n%q", before, after)
	}
}

// TestGUISettings: the Settings page refuses the home folder inline (the
// TUI's --downloads check), keeps a folder and dry run in a scratch
// XDG_CONFIG_HOME's archmaid/gui.json (0600), and the next Start runs on a
// fresh Driver made with them; a second start of the app begins with them.
func TestGUISettings(t *testing.T) {
	needDisplay(t)
	cfg, dl := t.TempDir(), t.TempDir()
	env := []string{"XDG_CONFIG_HOME=" + cfg, "ARCHMAID_GUI_DL=" + dl}
	rep, _ := child(t, "settings", env...)
	if len(rep.DlErrors) != 2 || !strings.Contains(rep.DlErrors[0], "home") || rep.DlErrors[1] != "" {
		t.Errorf("folder errors %q: want the home folder refused, then %s kept", rep.DlErrors, dl)
	}
	want := []string{`dry=false downloads=""`, fmt.Sprintf("dry=true downloads=%q", dl)}
	if !slices.Equal(rep.DriverOpts, want) || rep.DriverCloses != 2 {
		t.Errorf("drivers %q (%d closed), want %q, both closed", rep.DriverOpts, rep.DriverCloses, want)
	}
	if rep.Outcome != "notice" || rep.Problem != "" {
		t.Errorf("outcome %q problem %q", rep.Outcome, rep.Problem)
	}
	path := filepath.Join(cfg, "archmaid", "gui.json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("gui.json mode %v, want 0600", fi.Mode().Perm())
	}
	if s, err := settings.Load(path); err != nil || s != (settings.Settings{Downloads: dl, DryRun: true}) {
		t.Errorf("gui.json holds %+v, %v", s, err)
	}
	if ents, _ := os.ReadDir(filepath.Join(cfg, "archmaid")); len(ents) != 1 {
		t.Errorf("config folder holds %v, want only gui.json", ents)
	}

	rep, _ = child(t, "repair", env...)
	// The window's first Driver and the Start's fresh one, both with them.
	if want := []string{want[1], want[1]}; !slices.Equal(rep.DriverOpts, want) || rep.DriverCloses != 2 {
		t.Errorf("restarted: drivers %q (%d closed), want %q", rep.DriverOpts, rep.DriverCloses, want)
	}

	// A gui.json naming the home folder (edited by hand, or saved before a
	// check): vetted on load, shown under the row, not used.
	bad := t.TempDir()
	if err := settings.Save(filepath.Join(bad, "archmaid", "gui.json"), settings.Settings{Downloads: os.Getenv("HOME"), DryRun: true}); err != nil {
		t.Fatal(err)
	}
	rep, _ = child(t, "settings-bad", "XDG_CONFIG_HOME="+bad)
	if len(rep.DlErrors) != 1 || !strings.Contains(rep.DlErrors[0], "home") {
		t.Errorf("loaded folder errors %q: want the home folder refused", rep.DlErrors)
	}
	if want := []string{`dry=true downloads=""`, `dry=true downloads=""`}; !slices.Equal(rep.DriverOpts, want) {
		t.Errorf("loaded a refused folder: drivers %q, want %q", rep.DriverOpts, want)
	}
}

func stateTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprint(p, fi.ModTime(), fi.Mode(), fi.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
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
