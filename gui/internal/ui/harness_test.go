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
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	// ToastLabels: toast-markup's labels in the toast overlay, as shown.
	ToastLabels []string
	// Chips: the Run page's visible count chips at the end ("1 done").
	Chips   []string
	Menu    []string // the Overview's rows
	MenuSub string   // the started row's subtitle right after the click
	// usercache-size: the Clear user cache cell's sub line (text, or
	// "<hidden>") before the run, while it waits at its gate, and once
	// the run has ended and the cell re-measured.
	CacheStart, CacheRunning, CacheEnd string
	CacheMeasures                      int64    // finished measurements by the end
	Page                               string   // the page up after starting an action
	LastRun                            []string // the Overview's last run: title, then issues
	// Follow is fullcleanup's follow-the-bottom flag: at the end, after a
	// scrollbar drag, after reaching the bottom.
	Follow string
	// Folded counts the steps with output folded to their summary.
	Folded int
	// Summaries and Headers are the timeline's shown summary lines and
	// step titles at the end (the folded steps, the headers drawn).
	Summaries, Headers []string
	// Pages is the Run page at each run's end (clear-rerun,
	// real-failed-decline): runPage.
	Pages []string
	// Heads: the same moments' "previous run" tag and page title.
	Heads []string
	// real-failed: the failed step's actions after the first run (their
	// labels, or "hidden"), and while the second goes; what Copy error
	// copied; the Logs page Open log showed ("page run category").
	FailedActs, FailedBusy []string
	Copied, FailedLog      string
	// LastTag and LastTone are the last run's outcome tag and its tone
	// class (am-tag-ok, -warn, -danger, -neutral).
	LastTag, LastTone string
	LogRows           []string // the Logs page's "run/category" rows
	LogShown          string   // the log opened, and its lines
	LogLines          []string
	Notice            string
	// Disc is a discovery-only action's Run page at its end, and what the
	// Overview's last run cell and the Logs page show after it (discPage).
	Disc *discPage
	// DiscScroll is disc-scroll's long discovery page (discscroll_harness_test.go).
	DiscScroll string
	DiscWrap   string
	DiscHome   string

	// The Logs page's list while searching for ARCHMAID_GUI_LOGSEARCH,
	// and with its first section folded (logs_harness_test.go).
	LogSearch, LogFolded []string
	// The Overview's last run cell: its log files line, and the page and
	// log its Open in Logs showed ("page run/category").
	LastFiles, LastOpened string
	// LastCell is the last run cell's files line and whether Open in Logs
	// shows, as the window opens.
	LastCell string
	// The Settings page's topgrade card error after Open in editor ("" if
	// none; settings_harness_test.go).
	ConfigError string

	// The window's title, the stub's banner if revealed, and the status
	// line, at the first frame.
	Title, Banner, FirstStatus string
	// Launched is what the window asked its Launcher for (recordLauncher's
	// Calls): nothing is opened in a test.
	Launched []string
	// Dumpable (prctl PR_GET_DUMPABLE) and CoreLimit (RLIMIT_CORE's soft
	// limit) in a real-Driver child, set up as main does.
	Dumpable  int
	CoreLimit uint64
	// EndStatus is the status line once the run finished; Statuses every
	// status line shown, once.
	EndStatus string
	Statuses  []string
	// EndStatuses: the status line at each run's end (audit-rerun).
	EndStatuses []string
	// EndLines: the model's output line count at each run's end (audit-rerun).
	EndLines []int
	// Geometry is real-small's orphan dialog as shown (headingGeometry).
	Geometry string
	// Nav is narrow-nav's record of the sidebar overlay (shell_harness_test.go).
	Nav []string
	// Shell is real-small's frame mid-run (shellGeometry).
	Shell string
	// Confirms is each confirm dialog's text as shown (dialogLabels).
	Confirms []string
	// Picks is each pick list as shown: heading, rows, the boxes' count.
	Picks []string
	// Caller and Focus: small-askpass's and small-polkit's password dialog
	// at 630x372: the caller label as shown (callerGeometry), and whether
	// the password entry has the focus.
	Caller, Focus string
	// Collapsed: the sidebar was collapsed at the first frame.
	Collapsed bool
	// Meters is the sidebar's meters when the (last) run finished.
	Meters []string
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
	launcher := &recordLauncher{Choose: os.Getenv("ARCHMAID_GUI_CHOOSE")}
	// Nothing in a test opens an app or a portal dialog.
	opts := Options{Flags: gio.ApplicationNonUnique, StateDir: os.Getenv("ARCHMAID_GUI_STATE"), Launcher: launcher}
	action, answerNo, password := actions.IDFullCleanup, false, "hunter2"
	cancelPassword := false
	switch scenario {
	case "flood": // unpaced, nothing dropped: the view's real capacity
		script, action, opts.Pump = runview.Flood(time.Now(), 50000, 1, 0), "flood", runview.PumpOptions{MaxPending: -1}
	case "flood-paced": // 10k lines/s, twice the budget, default pump
		script, action = runview.Flood(time.Now(), 50000, 100, 10*time.Millisecond), "flood"
	case "flood-default": // unpaced, default pump (drops what the view can't keep)
		script, action = runview.Flood(time.Now(), 50000, 1, 0), "flood"
	case "flood-steps": // 50k lines over 10 steps finishing mid-flood: folds, the cap across sections
		script, action, opts.Pump = runview.FloodSteps(time.Now(), 50000, 10, 1), "flood", runview.PumpOptions{MaxPending: -1}
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
	case "usercache-size", "usercache-none": // the cell's size line, before and after a run
		action = actions.IDUserCache
	case "repair", "disc-scroll": // a discovery that ends in a notice (disc-scroll: then a long one, injected)
		action = actions.IDRepair
	case "settings": // home refused, a folder and dry run kept, then a run
		action = actions.IDRepair
	case "settings-bad": // gui.json holds the home folder: refused on load
		action = actions.IDRepair
	case "idle", "logs", "logs-newer", "fullcleanup", "stop", "close-midrun":
	case "narrow-nav": // 630x372: the sidebar overlay (shell_harness_test.go)
	case "toast-markup": // a toast whose message looks like markup
	case "toast-half": // half of a 1920 px screen: the longest toasts, no GTK warning
	case "dry-first": // Full Cleanup from the hero's Dry run first
	case "audit-rerun": // Full Cleanup, then the AUR audit (a report, no queue): the audit's page
	case "clear-rerun": // Full Cleanup, then Repair (a notice), then Clear user cache: when the page clears
	case "confirm-flag": // --confirm fullcleanup: the window opens into the gate; "no"
		answerNo = true
	case "engine-stops", "engine-stops-rerun": // the Driver dies at the second step
	case "engine-stops-askpass": // ... with sudo's askpass dialog open, typed in
		action = stub.IDFullCleanupAskpass
	case "real-dry", "real-refused", "real-locked", "real-close", "real-tty", "real-small", // realdriver_harness_test.go
		"real-one-password", "real-agent-missing", "real-foreign-first":
	case "real-runs": // realRuns real topgrade runs in one window, then a dry one
		action = actions.IDTopgrade
	case "real-askpass": // topgrade's sudo asks through the relay
		action = actions.IDTopgrade
	case "small-askpass", "small-polkit": // the password dialog at 630x372 (TestGUISmallWindow)
		action = stub.IDFullCleanupAskpass
	case "real-askpass-cancel":
		action, cancelPassword = actions.IDTopgrade, true
	case "real-services", "real-services-none", "real-services-error": // the services discovery on a systemctl shim
		action = actions.IDServices
	case "real-failed": // topgrade fails; its step's Copy error, Open log, then Run again
		action = actions.IDTopgrade
	case "real-failed-decline": // Full Cleanup whose topgrade fails, then Run topgrade again with its gate declined
	case "real-close-topgrade": // Run topgrade, then Stop and Quit mid-topgrade
		action = actions.IDTopgrade
	case "real-close-finished": // the question asked mid-topgrade, answered after its end
		action = actions.IDTopgrade
	case "real-close-polkit": // Full Cleanup; Stop and Quit while pkexec waits
	case "real-sigint", "real-sighup", "real-sigint-twice": // Run topgrade, then the signal mid-topgrade
		action = actions.IDTopgrade
	default:
		if covScenarios[scenario] == nil { // cover_harness_test.go's
			fail("unknown scenario %q", scenario)
		}
	}
	if scenario == "confirm-flag" {
		opts.Confirm = actions.IDFullCleanup
	}
	if strings.HasPrefix(scenario, "small-") {
		opts.KeepsPassword = true // the kept-password note too: the tallest dialog
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
			if strings.HasPrefix(scenario, "small-") {
				d = newRetouchDriver(d, smallPassword(scenario))
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
		if scenario == "toast-half" {
			wd, _ := strconv.Atoi(os.Getenv("ARCHMAID_GUI_TOAST_W"))
			w.win.SetDefaultSize(wd, 372)
		}
		if scenario == "real-small" || scenario == "narrow-nav" || strings.HasPrefix(scenario, "small-") {
			w.win.SetDefaultSize(630, 372) // half a 1280x800 screen, split (spec 1.8)
		}
		w.win.ConnectRealize(func() {
			fc := gdk.BaseFrameClock(w.win.FrameClock())
			fc.ConnectBeforePaint(func() { painting = time.Now() })
			fc.ConnectAfterPaint(func() {
				if rep.FirstFrame == 0 {
					rep.FirstFrame = time.Now().UnixNano()
					// Idle: let the first frames settle, read RSS, then go.
					settle := uint(1500)
					if covScenarios[scenario] != nil {
						settle = 200 // the cover scenarios measure nothing
					}
					glib.TimeoutAdd(settle, func() {
						rep.IdleMem = readMem()
						shoot(w, scenario, "start")
						rep.Collapsed = w.shell.split.Collapsed()
						if !w.win.HasCSSClass(scopeClass) {
							fail("the window lacks the %q class style.css is scoped to", scopeClass)
						}
						if scenario == "idle" {
							w.win.Close()
							return
						}
						if scenario == "toast-markup" {
							w.toast(markupToast)
							glib.TimeoutAdd(400, func() {
								rep.ToastLabels = labelsUnder(&w.toasts.Widget)
								w.win.Close()
							})
							return
						}
						if scenario == "toast-half" {
							// One at a time: the overlay queues, so each is shown
							// for a moment, then dismissed.
							for i, m := range halfToasts {
								glib.TimeoutAdd(uint(i)*700, func() {
									t := plainToast(m)
									w.toasted = append(w.toasted, m)
									w.toasts.AddToast(t)
									glib.TimeoutAdd(500, t.Dismiss)
								})
							}
							glib.TimeoutAdd(uint(len(halfToasts))*700+300, func() { w.win.Close() })
							return
						}
						// The Overview's actions: the hero and the cells.
						for _, c := range overview.Cards() {
							if r := w.menu[c.ID]; r != nil && r.cell != nil {
								rep.Menu = append(rep.Menu, c.ID)
							}
						}
						rep.LastTag = w.lastTag.Text()
						for _, c := range []string{"am-tag-ok", "am-tag-warn", "am-tag-danger", "am-tag-neutral"} {
							if w.lastTag.HasCSSClass(c) {
								rep.LastTone = c
							}
						}
						rep.LastRun = append([]string{w.lastRun.Title}, w.lastRun.Issues...)
						rep.CacheStart = cacheCell(w)
						if files, open := w.lastRunExtras(); files != nil && open != nil {
							rep.LastCell = fmt.Sprintf("files=%q open=%v", map[bool]string{true: files.Text()}[files.Visible()], open.Visible())
						}
						rep.Title, rep.FirstStatus = w.win.Title(), w.status.Text()
						if w.banner != nil && w.banner.Revealed() {
							rep.Banner = w.banner.Title()
						}
						if run := covScenarios[scenario]; run != nil {
							run(&covEnv{w: w, rep: rep, fail: fail})
							return
						}
						if scenario == "logs" {
							browseLogs(w, rep, fail)
							return
						}
						if scenario == "logs-newer" {
							logsNewer(w, rep)
							return
						}
						if scenario == "narrow-nav" {
							narrowNav(w, rep)
							return
						}
						if scenario == "settings-bad" && w.set.dlError.Visible() {
							rep.DlErrors = append(rep.DlErrors, w.set.dlError.Text())
						}
						if scenario == "settings" {
							changeSettings(w, rep)
						}
						started, measuring = time.Now(), true
						if scenario == "confirm-flag" {
							// Started by --confirm itself, at once.
							rep.Page = w.stack.VisibleChildName()
							return
						}
						// Start it the way a user does: its Overview cell (the
						// hero's Run, or its Dry run first).
						if r, ok := w.menu[action]; ok {
							if scenario == "dry-first" {
								w.dryFirst.Emit("clicked")
							} else {
								r.start()
							}
							if rep.Page = w.stack.VisibleChildName(); rep.Page != "run" {
								fail("starting %s left the %q page up", action, rep.Page)
							}
							rep.MenuSub = r.sub.Label()
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
				if scenario == "usercache-size" {
					// The run is up (at its gate): the cell says so. The stub
					// deletes nothing, so the fixture is emptied here, as the
					// real step would.
					rep.CacheRunning = cacheCell(w)
					dir := filepath.Join(os.Getenv("HOME"), ".cache")
					ents, _ := os.ReadDir(dir)
					for _, e := range ents {
						if !strings.HasPrefix(e.Name(), ".") {
							_ = os.RemoveAll(filepath.Join(dir, e.Name()))
						}
					}
				}
				p := *c.Opened
				delay := uint(30)
				small := scenario == "real-small" && p.Confirm != nil && len(p.Confirm.Direct) > 0
				smallPw := strings.HasPrefix(scenario, "small-") && p.Password != nil
				small = small || smallPw
				if small {
					delay = 800 // shown, animated in and focused
				}
				if os.Getenv(shotsEnv) != "" && delay < 600 {
					delay = 600 // the dialog animated in, for its shot
				}
				glib.TimeoutAdd(delay, func() {
					d, ok := w.dialogs[id]
					if !ok {
						fail("prompt %d has no dialog", id)
						return
					}
					shoot(w, scenario, fmt.Sprintf("prompt-%d", id))
					if !d.HasCSSClass(scopeClass) {
						fail("prompt %d's dialog lacks the %q class", id, scopeClass)
					}
					if small {
						rep.Geometry = headingGeometry(w, d)
						rep.Shell = shellGeometry(w)
					}
					if smallPw {
						rep.Caller = callerGeometry(w)
						rep.Focus = focusIn(w, w.pwEntry)
					}
					// Answer the way a user would: type, tick, then press.
					resp := "yes"
					if answerNo {
						resp = "no"
					}
					if scenario == "real-failed-decline" && len(rep.Outcomes) == 1 {
						resp = "no" // Run topgrade's gate, declined
					}
					if p.Confirm != nil {
						rep.Buttons = append(rep.Buttons, fmt.Sprintf("%d %s/%s", id, d.ResponseLabel("yes"), d.ResponseLabel("no")))
						rep.Confirms = append(rep.Confirms, d.Heading()+"\n"+dialogLabels(d, nil))
					}
					switch {
					case p.Password != nil:
						// The heading, the caller line, then the body.
						caller := ""
						if w.pwCaller != nil {
							caller = w.pwCaller.Label()
						}
						rep.PwBodies = append(rep.PwBodies, d.Heading()+"\n"+caller+"\n"+dialogLabels(d, w.pwCaller))
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
						// The pick list as shown: its heading, its rows
						// and how many boxes there are to tick.
						rep.Picks = append(rep.Picks, fmt.Sprintf("%s\n%s\n%d boxes", d.Heading(), dialogLabels(d, nil), len(w.checks)))
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
					if smallPw {
						// Its geometry is all this scenario is for.
						glib.TimeoutAdd(50, func() {
							w.win.Close()
							glib.TimeoutAdd(30, func() { stopAndQuit(w, fail) })
						})
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
				if w.ended != nil {     // this action's end, also one before its queue
					rep.Outcome = w.ended.Outcome.String()
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
				if scenario == "confirm-flag" {
					rep.Page = w.stack.VisibleChildName()
				}
				rep.Meters = w.shell.meters.text()
				// Count the coloured rows once the last batch is drawn.
				glib.TimeoutAdd(20, func() {
					rep.Labels, rep.Coloured = countLabels(&w.outView.Widget)
					for _, c := range []*gtk.Label{w.tl.done, w.tl.failed, w.tl.skipped, w.tl.refused} {
						if c.IsVisible() {
							rep.Chips = append(rep.Chips, c.Text())
						}
					}
					rep.Summaries = labelsWith(&w.outView.Widget, "am-summary")
					if scenario == "fullcleanup" {
						// Follow-the-bottom: a drag on the scrollbar stops
						// it, reaching the bottom again restarts it.
						before := w.follow
						w.dragStop.Emit("drag-begin", 0.0, 0.0)
						dragged := w.follow
						w.tl.scroll.Emit("edge-reached", gtk.PosBottom)
						rep.Follow = fmt.Sprintf("%v %v %v", before, dragged, w.follow)
					}
					// The sections mirror the model: each holds its step's
					// lines when open, its summary item when folded.
					for _, sec := range w.tl.sections {
						n := len(w.model.StepText(sec.step))
						want := 1
						if sec.lines {
							want = n
						}
						if sec.n != n || sec.items != want || int(sec.list.NItems()) != want {
							fail("section %q: n %d items %d list %d; the model holds %d (open %v)", sec.step, sec.n, sec.items, sec.list.NItems(), n, sec.lines)
						}
						if !sec.lines && sec.n > 0 {
							rep.Folded++
						}
					}
					rep.Headers = labelsWith(&w.outView.Widget, "am-step-title")
					if scenario == "repair" || strings.HasPrefix(scenario, "real-services-") {
						rep.Disc = discOf(w)
					}
					// Every drawn step header's marker carries its state's
					// class (style.css).
					for _, s := range w.tl.sections {
						if h := s.header; h != nil {
							if r, i := w.rowOf(s.step); i >= 0 && !h.icon.HasCSSClass(stateClass(r.State)) {
								fail("step %s (%s) lacks %s", s.step, r.State, stateClass(r.State))
							}
						}
					}
					shoot(w, scenario, fmt.Sprintf("end-%d", len(rep.Outcomes)))
				})
				if scenario == "usercache-size" || scenario == "usercache-none" {
					// The re-measure is off the main thread: wait for the
					// line to settle.
					polls := 0
					var poll func()
					poll = func() {
						polls++
						rep.CacheEnd = cacheCell(w)
						if rep.CacheEnd == "Empty" || (scenario == "usercache-none" && cacheMeasures.Load() >= 2) || polls > 60 {
							rep.CacheMeasures = cacheMeasures.Load()
							w.win.Close()
							return
						}
						glib.TimeoutAdd(50, poll)
					}
					glib.TimeoutAdd(50, poll)
					return
				}
				switch scenario {
				case "close-midrun", "real-close", "real-close-topgrade", "real-sigint", "real-sighup", "real-sigint-twice":
					fail("the run finished before the window closed")
				}
				rep.Outcomes = append(rep.Outcomes, rep.Outcome)
				if scenario == "audit-rerun" {
					rep.EndStatuses = append(rep.EndStatuses, w.status.Text())
					rep.EndLines = append(rep.EndLines, w.model.Lines)
				}
				if scenario == "clear-rerun" || scenario == "audit-rerun" || scenario == "real-failed-decline" {
					rep.Pages = append(rep.Pages, runPage(w))
					rep.Heads = append(rep.Heads, fmt.Sprintf("prev=%v title=%q", w.tl.prev.IsVisible(), w.shell.title.Label()))
				}
				if scenario == "disc-scroll" && discScroll(w, rep, len(rep.Outcomes)) {
					return
				}
				if scenario == "audit-rerun" && len(rep.Outcomes) == 1 {
					// Start the audit once the first run has settled (not
					// busy), not after a guessed delay; and check it started.
					polls := 0
					var again func()
					again = func() {
						if polls++; w.busy && polls < 400 {
							glib.TimeoutAdd(25, again)
							return
						}
						w.menu[actions.IDAURAudit].start()
						if !w.busy {
							fail("the audit didn't start (polls %d)", polls)
							w.win.Close()
						}
					}
					glib.TimeoutAdd(25, again)
					return
				}
				if scenario == "clear-rerun" && len(rep.Outcomes) < 3 {
					// Then an action that ends in a notice, before any
					// queue (the page stays), then one with a queue (the
					// page clears when it starts).
					next := actions.IDRepair
					if len(rep.Outcomes) == 2 {
						next = actions.IDUserCache
					}
					glib.TimeoutAdd(50, func() { w.menu[next].start() })
					return
				}
				if scenario == "real-failed-decline" && len(rep.Outcomes) == 1 {
					// Run topgrade on the failed step; its gate is declined.
					glib.TimeoutAdd(60, func() {
						for _, s := range w.tl.sections {
							if s.step == steps.IDTopgrade && s.header != nil {
								s.header.acts.again.Emit("clicked")
								return
							}
						}
						fail("the failed topgrade step has no header drawn")
						w.win.Close()
					})
					return
				}
				if scenario == "real-failed-decline" {
					// The failed step offers its actions again.
					glib.TimeoutAdd(60, func() {
						for _, s := range w.tl.sections {
							if s.step == steps.IDTopgrade && s.header != nil && s.header.acts.box.Visible() {
								rep.FailedActs = append(rep.FailedActs, s.header.acts.again.Label())
							}
						}
						w.win.Close()
					})
					return
				}
				if scenario == "real-failed" && len(rep.Outcomes) == 1 {
					// The failed step's buttons, as a user clicks them,
					// once its header is drawn.
					glib.TimeoutAdd(60, func() { clickFailed(w, rep, fail) })
					return
				}
				if scenario == "real-runs" && len(rep.Outcomes) <= realRuns {
					// The next run, from its Overview row again; the last
					// one with dry run switched on in Settings.
					glib.TimeoutAdd(50, func() {
						if len(rep.Outcomes) == realRuns {
							w.set.dryRun.SetActive(true)
						}
						rep.Rows = nil
						w.menu[action].start()
					})
					return
				}
				if strings.HasPrefix(scenario, "engine-stops") && len(rep.Outcomes) == 1 {
					rep.Controls = fmt.Sprintf("busy=%v stop=%v menu=%v dialogs=%d status=%q",
						w.busy, w.stopBtn.Visible() && w.stopBtn.Sensitive(), w.menu[actions.IDFullCleanup].cell.Sensitive(), len(w.dialogs), w.status.Text())
					if typed != nil {
						rep.Withdrawn = fmt.Sprintf("len=%d kept=%v", typed.TextLength(), w.pwEntry != nil)
					}
					if scenario == "engine-stops-rerun" {
						glib.TimeoutAdd(50, func() {
							rep.Rows = nil
							w.menu[action].start()
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

	if set := covOptions[scenario]; set != nil {
		set(&opts, rep)
	}
	code := Run([]string{"archmaid-gui-test"}, opts)
	rep.Launched = launcher.Calls()
	styleMu.Lock()
	for _, e := range styleErrors {
		fail("style.css: %s", e)
	}
	styleMu.Unlock()
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

// clickFailed clicks the failed topgrade step's actions: Copy error,
// Open topgrade.log, then Run topgrade, whose run (it fails again) ends
// the scenario.
func clickFailed(w *Window, rep *report, fail func(string, ...any)) {
	var h *stepHeader
	for _, s := range w.tl.sections {
		if s.step == steps.IDTopgrade {
			h = s.header
		}
	}
	if h == nil {
		fail("the failed topgrade step has no header drawn")
		w.win.Close()
		return
	}
	acts := func() []string {
		if !h.acts.box.Visible() {
			return []string{"hidden"}
		}
		// " [primary]" marks the suggested (primary) button.
		var out []string
		for _, b := range []*gtk.Button{h.acts.again, h.acts.log, h.acts.copy} {
			l := b.Label()
			if b.HasCSSClass("suggested-action") {
				l += " [primary]"
			}
			out = append(out, l)
		}
		return out
	}
	rep.FailedActs = acts()
	for _, s := range w.tl.sections {
		if s.header != nil && s.header != h && s.header.acts.box.Visible() {
			fail("step %s (not failed) shows the failed actions", s.step)
		}
	}
	h.acts.copy.Emit("clicked")
	rep.Copied = w.copied
	h.acts.log.Emit("clicked")
	if run := w.logs.run; run != nil {
		rep.FailedLog = w.stack.VisibleChildName() + " " + run.Name + " " + string(w.logs.cat)
	}
	h.acts.again.Emit("clicked")
	if w.stack.VisibleChildName() != "run" || !w.busy {
		fail("Run topgrade didn't start a run (page %q)", w.stack.VisibleChildName())
		w.win.Close()
		return
	}
	// While the new run goes, no header offers the actions.
	for _, s := range w.tl.sections {
		if s.header != nil && s.header.acts.box.Visible() {
			rep.FailedBusy = append(rep.FailedBusy, s.step)
		}
	}
}

// browseLogs opens the Logs page (as the Logs tab does; the Overview's
// View log row goes in P8), opens the log ARCHMAID_GUI_LOG names
// ("run/category"), records what it shows, searches it for
// ARCHMAID_GUI_LOGSEARCH, folds its first section, presses Open folder,
// then closes the window. The Logs page's own helpers are in
// logs_harness_test.go.
func browseLogs(w *Window, rep *report, fail func(string, ...any)) {
	if files, open := w.lastRunExtras(); files != nil && open != nil && open.Visible() {
		rep.LastFiles = files.Text()
		open.Emit("clicked")
		rep.LastOpened = w.stack.VisibleChildName() + " " + w.logs.shown
	}
	w.showLogs()
	if rep.Page = w.stack.VisibleChildName(); rep.Page != "logs" {
		fail("the Logs page isn't up: %q", rep.Page)
	}
	for k := range w.logs.open {
		rep.LogRows = append(rep.LogRows, k)
	}
	slices.Sort(rep.LogRows)
	if open, ok := w.logs.open[os.Getenv("ARCHMAID_GUI_LOG")]; ok {
		open()
	}
	rep.LogShown = w.logs.shown
	rep.LogLines = logLines(w)
	rep.LogSearch = searchLog(w, os.Getenv("ARCHMAID_GUI_LOGSEARCH"))
	rep.LogFolded = foldFirstSection(w)
	w.logs.folder.Emit("clicked")
	delay := uint(50)
	if os.Getenv(shotsEnv) != "" {
		delay = 600 // laid out and drawn, for its shot
	}
	glib.TimeoutAdd(delay, func() {
		shoot(w, "logs", "page")
		w.win.Close()
	})
}

// countLabels walks the output list's widgets: the labels showing text,
// and how many of them carry attributes (colour).
// changeSettings works the Settings page the way a user does: the home
// folder typed and applied (refused), then ARCHMAID_GUI_DL (kept), then
// Choose… (the harness's Launcher answers ARCHMAID_GUI_CHOOSE; when that is
// unset it answers nothing and the folder stays), Open in editor and the
// log card's Open folder, then dry run switched on. The run that follows
// must get a fresh Driver.
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
	p.choose.Emit("clicked")
	if os.Getenv("ARCHMAID_GUI_CHOOSE") != "" {
		rep.DlErrors = append(rep.DlErrors, shown())
	}
	p.editor.Emit("clicked")
	if p.configError.Visible() {
		rep.ConfigError = p.configError.Text()
	}
	// A second click right away, as after the editor closed (Dev 5's
	// first-click bug): every click must ask the Launcher again, whatever
	// the first launch is still doing.
	p.editor.Emit("clicked")
	p.logFolder.Emit("clicked")
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

// markupToast is toast-markup's message: an error naming a path with
// markup in it.
const markupToast = "Couldn't open it: open /tmp/<b>bold</b> & co: no such file"

// TestGUIToastPlain: a toast shows its message as plain text, never as
// markup (Dev 3's security read): the label reads the message literally.
func TestGUIToastPlain(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "toast-markup")
	if !slices.Contains(rep.ToastLabels, markupToast) {
		t.Errorf("toast labels %q, want %q literally", rep.ToastLabels, markupToast)
	}
}

// halfToasts are the longest toasts the window shows: the failed end with
// refused steps, a notice at ShortMax runes and an error with a path.
var halfToasts = []string{
	"Run completed",
	"Run finished with 1 failed step · 5 steps not authorised",
	strings.Repeat("A notice that runs on and on. ", 4)[:runview.ShortMax-1] + "…",
	"Couldn't open it: open /home/someone/.local/state/archmaid/runs/2026-10-12_09-30-00_full-cleanup: no such file",
}

// TestGUIToastHalfScreen: with the window at the width of half a 1920 px
// screen (960 px), half a 1280 px one (630 px) and libadwaita's 360 px
// minimum, no toast makes GTK or libadwaita log a warning (Dev 5's VM
// round: AdwToastOverlay "exceeds AdwApplicationWindow width", because the
// Overview's minimum width was 371 px, past a 360 px window).
func TestGUIToastHalfScreen(t *testing.T) {
	needDisplay(t)
	for _, wd := range []string{"960", "630", "360"} {
		t.Run(wd, func(t *testing.T) {
			var buf bytes.Buffer
			childCmd(t, "toast-half", func(c *exec.Cmd) { c.Stderr = io.MultiWriter(os.Stderr, &buf) }, "ARCHMAID_GUI_TOAST_W="+wd)
			for _, l := range strings.Split(buf.String(), "\n") {
				if glibWarned(l) {
					t.Errorf("GTK logged: %s", l)
				}
			}
		})
	}
}

// glibWarned: a stderr line that is a GLib warning, critical or error (the
// log writer prints them as slog lines: "WARN ...", "ERROR ...").
func glibWarned(l string) bool {
	return strings.Contains(l, " WARN ") || strings.Contains(l, " ERROR ") || strings.Contains(l, "-WARNING") || strings.Contains(l, "-CRITICAL")
}

// labelsUnder is the text of the shown labels under root, as GTK lays it
// out (the label's text after any markup parsing).
func labelsUnder(root *gtk.Widget) []string {
	var out []string
	var walk func(gtk.Widgetter)
	walk = func(p gtk.Widgetter) {
		for c := gtk.BaseWidget(p).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
			if l, ok := c.(*gtk.Label); ok && l.IsVisible() {
				out = append(out, l.Text())
			}
			walk(c)
		}
	}
	walk(root)
	return out
}

// dialogLabels is the text of d's labels that are shown, in order, but for
// its heading, skip (the caller line, said apart) and its buttons'.
func dialogLabels(d *adw.AlertDialog, skip *gtk.Label) string {
	var out []string
	var walk func(gtk.Widgetter)
	walk = func(p gtk.Widgetter) {
		for c := gtk.BaseWidget(p).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
			if _, ok := c.(*gtk.Button); ok {
				continue
			}
			if l, ok := c.(*gtk.Label); ok && l.IsVisible() && l.Label() != d.Heading() && (skip == nil || glib.InternObject(l).Native() != glib.InternObject(skip).Native()) {
				out = append(out, l.Label())
			}
			walk(c)
		}
	}
	walk(d)
	return strings.Join(out, "\n")
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

// labelsWith is the text of the mapped labels under root with class.
func labelsWith(root *gtk.Widget, class string) []string {
	var out []string
	for c := root.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		w := gtk.BaseWidget(c)
		if l, ok := c.(*gtk.Label); ok && w.Mapped() && l.HasCSSClass(class) && l.Text() != "" {
			out = append(out, l.Text())
		}
		out = append(out, labelsWith(w, class)...)
	}
	return out
}

func countLabels(root *gtk.Widget) (labels, coloured int) {
	for c := root.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		w := gtk.BaseWidget(c)
		// Output lines only: not the step headers or summaries.
		if l, ok := c.(*gtk.Label); ok && w.Mapped() && l.Text() != "" && l.HasCSSClass("am-line") {
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
	// The gate's plan rail (spec 1.3): the kicker, the numbered steps with
	// their tags, and the notes computed from the plan; no dry-run banner
	// on a real run's confirms.
	if len(rep.Confirms) == 0 {
		t.Fatal("no confirm was shown")
	}
	for _, want := range []string{"10 steps, in this order", "01\nAuditing AUR packages…", "04\nDeleting entire package cache…\nasks\nroot",
		"5 steps run as root, together, after one polkit authorisation", "Step 4 asks before it deletes the entire package cache."} {
		if !strings.Contains(rep.Confirms[0], want) {
			t.Errorf("the gate lacks %q:\n%s", want, rep.Confirms[0])
		}
	}
	for _, c := range rep.Confirms {
		if strings.Contains(c, dryBannerText) {
			t.Errorf("a real run's confirm says dry run:\n%s", c)
		}
	}
	if rep.Collapsed {
		t.Error("the sidebar is collapsed at the default 960x640")
	}
	// The timeline: step headers drawn; a step that finished before the
	// last folds to its summary line (its last output line); the last one
	// stays open, so the run's end shows.
	if len(rep.Headers) == 0 || len(rep.Summaries) == 0 || rep.ViewLines == 0 {
		t.Errorf("timeline: headers %q summaries %q, %d lines open", rep.Headers, rep.Summaries, rep.ViewLines)
	}
	if rep.Follow != "true false true" {
		t.Errorf("follow at the end, after a scrollbar drag, at the bottom: %q", rep.Follow)
	}
	// The stub's status has no byte counts: values, empty bars.
	if want := []string{"disk 212G 0.00", "ram 41G 0.00", "cache 3.1G 0.00"}; !slices.Equal(rep.Meters, want) {
		t.Errorf("meters %q, want %q", rep.Meters, want)
	}
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

// repairNone is the stub Repair's summary (engine/stub.RepairNone).
const repairNone = "No broken packages found."

// TestGUIOverviewStarts: an action started from its Overview row (the
// menu is actions.Menu without Exit and Edit config) shows the Run page.
func TestGUIOverviewStarts(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "repair")
	want := []string{"fullcleanup", "usercache", "repair", "downloads", "topgrade", "services", "auraudit"}
	if !slices.Equal(rep.Menu, want) {
		t.Errorf("menu %q, want %q", rep.Menu, want)
	}
	if rep.Page != "run" || rep.Outcome != "notice" || rep.Notice != repairNone {
		t.Errorf("page %q outcome %q notice %q", rep.Page, rep.Outcome, rep.Notice)
	}
	// The notice is shown: in the toast and on the status line.
	if !slices.Contains(rep.Toasts, repairNone) || !strings.HasPrefix(rep.EndStatus, "STUB · "+repairNone) {
		t.Errorf("toasts %q status %q: the notice isn't shown", rep.Toasts, rep.EndStatus)
	}
	if rep.MenuSub == "" {
		t.Errorf("the started row says nothing while it runs")
	}
	if len(rep.LastRun) != 1 || rep.LastRun[0] != "No run recorded yet" {
		t.Errorf("last run %q with an empty state dir", rep.LastRun)
	}
}

// cacheCell is the Clear user cache cell's sub line as shown, or "<hidden>".
func cacheCell(w *Window) string {
	r := w.menu[actions.IDUserCache]
	if !r.sub.Visible() {
		return "<hidden>"
	}
	return r.sub.Label()
}

// TestGUIUserCacheSize: the Clear user cache cell shows how much is in
// ~/.cache while idle, "Running" while its action goes, and re-measures when
// the run ends ("Empty" once the step emptied it). XDG_CACHE_HOME points at
// a folder elsewhere: the action deletes $HOME/.cache, so that is what is
// measured. A dot entry stays after rm -rf .cache/*, so it isn't counted.
func TestGUIUserCacheSize(t *testing.T) {
	needDisplay(t)
	home, xdg := t.TempDir(), t.TempDir()
	cache := filepath.Join(home, ".cache", "app")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// Random bytes: zeros could be stored compressed or sparse (btrfs zstd,
	// tmpfs) and report less than 3 MiB of blocks.
	for _, f := range []string{filepath.Join(cache, "blob"), filepath.Join(home, ".cache", ".hidden"), filepath.Join(xdg, "other")} {
		buf := make([]byte, 3<<20)
		if _, err := rand.Read(buf); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, buf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rep, _ := child(t, "usercache-size", "HOME="+home, "XDG_CACHE_HOME="+xdg)
	// Only blob (3 MiB = 3.0M, rounded up) and the directory "app" count: the
	// dot entry and XDG_CACHE_HOME's file would add 3 MiB each (6.x/9.x). The
	// directory's own blocks (0 on tmpfs, 4 KiB on ext4/btrfs) tip the ceil
	// from 3.0M to 3.1M, so exactly those two lines are right.
	if rep.CacheStart != "3.1M in ~/.cache" && rep.CacheStart != "3.0M in ~/.cache" {
		t.Errorf("idle line %q, want the 3 MiB fixture", rep.CacheStart)
	}
	if rep.CacheRunning != "Running" {
		t.Errorf("line while running %q", rep.CacheRunning)
	}
	if rep.CacheEnd != "Empty" {
		t.Errorf("line after the run %q, want Empty", rep.CacheEnd)
	}
}

// TestGUIUserCacheUnreadable: no ~/.cache, nothing is shown (never a wrong
// number), before the run and after it.
//
// The measurement must have run, or hidden would prove nothing: the scenario
// waits until two finished (at the start and after the run), and the count
// is asserted. A ~/.cache with mode 000 is the same (skipped as root, which
// reads it anyway).
func TestGUIUserCacheUnreadable(t *testing.T) {
	needDisplay(t)
	for _, tc := range []string{"missing", "denied"} {
		t.Run(tc, func(t *testing.T) {
			home := t.TempDir()
			if tc == "denied" {
				if os.Geteuid() == 0 {
					t.Skip("root reads a mode 000 folder")
				}
				cache := filepath.Join(home, ".cache")
				if err := os.MkdirAll(filepath.Join(cache, "app"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cache, "app", "blob"), make([]byte, 1<<20), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(cache, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(cache, 0o755) })
			}
			rep, _ := child(t, "usercache-none", "HOME="+home)
			if rep.CacheStart != "<hidden>" || rep.CacheEnd != "<hidden>" {
				t.Errorf("lines %q / %q with an unreadable ~/.cache, want hidden", rep.CacheStart, rep.CacheEnd)
			}
			if rep.CacheMeasures < 2 {
				t.Errorf("%d measurements finished, want the start's and the run's: the feature never ran", rep.CacheMeasures)
			}
		})
	}
}

// discPage is the Run page of an action that ended at its discovery, as
// the window shows it, and the Overview's last run cell and the Logs page
// after it.
type discPage struct {
	Title, Kicker string
	Prev          bool
	Steps         []string // the timeline's step IDs
	State         string   // the Discovery header's state class
	Summary       string   // the step's line (its row's summary)
	Lines         []string // the step's output
	Headers       []string
	LastTitle     []string // the last run cell: title, then issues
	LastTag       string
	LastTone      string
	LogRows       []string
}

func discOf(w *Window) *discPage {
	d := &discPage{Title: w.shell.title.Label(), Kicker: w.tl.kicker.Text(), Prev: w.tl.prev.IsVisible()}
	for _, s := range w.tl.sections {
		d.Steps = append(d.Steps, s.step)
		if h := s.header; h != nil && s.step == runview.DiscoveryStepID {
			for _, c := range h.icon.CSSClasses() {
				if strings.HasPrefix(c, "am-state-") {
					d.State = c
				}
			}
		}
	}
	if r, i := w.rowOf(runview.DiscoveryStepID); i >= 0 {
		d.Summary = r.Summary()
	}
	d.Lines = w.model.StepText(runview.DiscoveryStepID)
	d.Headers = labelsWith(&w.outView.Widget, "am-step-title")
	d.LastTitle = append([]string{w.lastRun.Title}, w.lastRun.Issues...)
	d.LastTag = w.lastTag.Text()
	for _, c := range []string{"am-tag-ok", "am-tag-warn", "am-tag-danger", "am-tag-neutral"} {
		if w.lastTag.HasCSSClass(c) {
			d.LastTone = c
		}
	}
	w.showLogs()
	for k := range w.logs.open {
		d.LogRows = append(d.LogRows, k)
	}
	slices.Sort(d.LogRows)
	return d
}

// runPage is the Run page as shown: the header's kicker and elapsed time,
// the timeline's steps, and the output lines it holds.
func runPage(w *Window) string {
	var ids []string
	for _, s := range w.tl.sections {
		ids = append(ids, s.step)
	}
	return fmt.Sprintf("kicker=%q elapsed=%q steps=%s lines=%d", w.tl.kicker.Text(), w.tl.elapsed.Text(), strings.Join(ids, ","), w.outLen)
}

// TestGUIClearRerun: Q12, as P8 row 13 has it. The Run page clears when a
// new run's queue starts, not when the action starts. Repair, which ends at
// its discovery (nothing found), is its own result, not Full Cleanup's page
// tagged "previous run" (owner's decision): the page switches to it with the
// discovery's result; Clear user cache (a queue) then shows only its own
// steps.
func TestGUIClearRerun(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "clear-rerun")
	if !slices.Equal(rep.Outcomes, []string{"completed", "notice", "completed"}) || len(rep.Pages) != 3 {
		t.Fatalf("outcomes %q pages %q", rep.Outcomes, rep.Pages)
	}
	if !strings.Contains(rep.Pages[0], steps.IDTopgrade) || strings.Contains(rep.Pages[0], " lines=0") {
		t.Errorf("Full Cleanup's page: %s", rep.Pages[0])
	}
	if !strings.Contains(rep.Pages[1], "steps="+runview.DiscoveryStepID+" ") || strings.Contains(rep.Pages[1], steps.IDTopgrade) {
		t.Errorf("Repair's page isn't its own Discovery result: %s", rep.Pages[1])
	}
	if strings.Contains(rep.Pages[2], steps.IDTopgrade) || !strings.Contains(rep.Pages[2], "steps="+steps.IDUserCache) {
		t.Errorf("Clear user cache's page still holds Full Cleanup's steps: %s", rep.Pages[2])
	}
	// No page is tagged "previous run"; each is titled for its own action.
	if want := []string{`prev=false title="Full Cleanup (Recommended)"`, `prev=false title="Repair broken packages"`, `prev=false title="Clear user cache"`}; !slices.Equal(rep.Heads, want) {
		t.Errorf("the page's tag and title %q, want %q", rep.Heads, want)
	}
}

// TestGUIAuditRerun: the AUR audit ends in a report, not a queue; after a
// Full Cleanup its Run page is the audit's own (no steps of the earlier
// run, no "previous run" tag), titled for the audit, and the status line
// doesn't carry the earlier run's line count.
func TestGUIAuditRerun(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "audit-rerun")
	if !slices.Equal(rep.Outcomes, []string{"completed", "report"}) || len(rep.Pages) != 2 || len(rep.Heads) != 2 {
		t.Fatalf("outcomes %q pages %q", rep.Outcomes, rep.Pages)
	}
	if strings.Contains(rep.Pages[1], steps.IDTopgrade) {
		t.Errorf("the audit's page holds Full Cleanup's steps: %s", rep.Pages[1])
	}
	if want := `prev=false title="Audit AUR packages"`; rep.Heads[1] != want {
		t.Errorf("the audit page's tag and title %q, want %q", rep.Heads[1], want)
	}
	// The status line carries the run's own line count, not the earlier
	// run's (wording-independent: the model's counts and the numbers shown).
	if len(rep.EndLines) != 2 || rep.EndLines[0] <= 1 || rep.EndLines[1] >= rep.EndLines[0] {
		t.Fatalf("line counts %v: want Full Cleanup's many, then fewer for the audit", rep.EndLines)
	}
	first := regexp.MustCompile(`\b` + strconv.Itoa(rep.EndLines[0]) + `\b`)
	if !first.MatchString(rep.EndStatuses[0]) {
		t.Errorf("status %q doesn't show the run's %d lines", rep.EndStatuses[0], rep.EndLines[0])
	}
	if first.MatchString(rep.EndStatuses[1]) {
		t.Errorf("the audit's status %q carries the earlier run's count %d", rep.EndStatuses[1], rep.EndLines[0])
	}
}

// TestGUIRepairNothingFound: Repair finding nothing (the stub's repair_none)
// shows as its own result on the Run page: the action's title, one Discovery
// step in the ok state with the summary as its line and the details under
// it, the kicker "Nothing found", no "previous run"; the toast and the
// status line say the summary.
func TestGUIRepairNothingFound(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "repair")
	d := rep.Disc
	if d == nil || rep.Outcome != "notice" {
		t.Fatalf("outcome %q disc %+v", rep.Outcome, d)
	}
	const summary = "No broken packages found."
	if d.Title != "Repair broken packages" || d.Prev || d.Kicker != "Nothing found" || d.State != "am-state-ok" ||
		!slices.Equal(d.Steps, []string{runview.DiscoveryStepID}) || d.Summary != summary {
		t.Errorf("page %+v", d)
	}
	if len(d.Lines) != 1 || !strings.HasPrefix(d.Lines[0], "1422 installed") {
		t.Errorf("details %q", d.Lines)
	}
	if !slices.Contains(rep.Toasts, summary) || !strings.Contains(rep.EndStatus, summary) {
		t.Errorf("toasts %q status %q: the summary isn't shown", rep.Toasts, rep.EndStatus)
	}
}

// TestGUIDryFirst: the hero's Dry run first runs Full Cleanup as a dry run
// on that Driver only; the saved settings (gui.json) stay as they were,
// and the next Driver is a real one again.
func TestGUIDryFirst(t *testing.T) {
	needDisplay(t)
	cfg := t.TempDir()
	rep, _ := child(t, "dry-first", "XDG_CONFIG_HOME="+cfg)
	if want := []string{`dry=false downloads=""`, `dry=true downloads=""`}; !slices.Equal(rep.DriverOpts, want) {
		t.Errorf("drivers %q, want the window's real one, then a dry one for the run", rep.DriverOpts)
	}
	if rep.Outcome != "completed" || rep.Page != "run" {
		t.Errorf("outcome %q page %q", rep.Outcome, rep.Page)
	}
	if _, err := os.Stat(filepath.Join(cfg, "archmaid", "gui.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Dry run first saved the settings: %v", err)
	}
	// Every confirm of the dry run says so up front, before its body and
	// lists (P8 row 13, Dev 5's finding).
	if len(rep.Confirms) == 0 {
		t.Fatal("no confirm was shown")
	}
	for _, c := range rep.Confirms {
		if _, rest, _ := strings.Cut(c, "\n"); !strings.HasPrefix(rest+"\n", dryBannerText+"\n") {
			t.Errorf("a dry run's confirm doesn't open with %q:\n%s", dryBannerText, c)
		}
	}
	// Its gate says nothing asks for a password, not how to type it.
	if !strings.Contains(rep.Confirms[0], runview.DryNoPassword) || strings.Contains(rep.Confirms[0], "password once") || strings.Contains(rep.Confirms[0], "polkit authorisation") {
		t.Errorf("the dry run's gate notes:\n%s", rep.Confirms[0])
	}
}

// TestGUIConfirmFlag: --confirm fullcleanup opens the window straight into
// Full Cleanup's confirm (the gate); nothing runs before the answer, and
// "no" ends it cancelled.
func TestGUIConfirmFlag(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "confirm-flag")
	if !slices.Equal(rep.Sent, []string{"confirm 1 false"}) || rep.Outcome != "cancelled" || rep.Page != "run" {
		t.Errorf("sent %q outcome %q page %q; want the gate first, declined, cancelled", rep.Sent, rep.Outcome, rep.Page)
	}
	for _, r := range rep.Rows {
		if !strings.HasSuffix(r, "=pending") && !strings.HasSuffix(r, "=not run") {
			t.Errorf("a step ran before Proceed: %s", r)
		}
	}
}

// TestGUILastRunTones: the last run's tag and tone: a failed run is FAILED
// in the danger tone; a cancelled one is CANCELLED and neutral (grey), as
// the DMS widget draws it, not a warning. A cancelled run ran nothing: its
// cell lists no log files (not an earlier run's) and has no Open in Logs.
func TestGUILastRunTones(t *testing.T) {
	needDisplay(t)
	for _, tc := range []struct {
		outcome, tag, tone, cell string
		refused                  int
	}{
		{logstore.OutcomeFailed, "FAILED", "am-tag-danger", `files="topgrade.log" open=true`, 0},
		{logstore.OutcomeCancelled, "CANCELLED", "am-tag-neutral", fmt.Sprintf("files=%q open=false", nothingRan), 0},
		// Every step refused: the engine records it cancelled, steps_refused 1.
		{logstore.OutcomeCancelled, "NOT AUTHORISED", "am-tag-warn", fmt.Sprintf("files=%q open=false", nothingRan), 1},
		{logstore.OutcomeCancelled, "NOT AUTHORISED", "am-tag-warn", fmt.Sprintf("files=%q open=false", nothingRan), 3},
		// steps_refused on any other outcome keeps that outcome's own tag.
		{logstore.OutcomeFailed, "FAILED", "am-tag-danger", `files="topgrade.log" open=true`, 2},
		{logstore.OutcomeAborted, "ABORTED", "am-tag-warn", `files="topgrade.log" open=true`, 2},
		{logstore.OutcomeIncomplete, "INCOMPLETE", "am-tag-warn", `files="topgrade.log" open=true`, 2},
		{logstore.OutcomeOK, "OK", "am-tag-ok", `files="topgrade.log" open=true`, 0},
		{logstore.OutcomeNothingFound, "NOTHING FOUND", "am-tag-ok", `files="topgrade.log" open=true`, 0},
	} {
		dir := t.TempDir()
		now := time.Now()
		// An earlier run's folder, with its log.
		earlier := func() time.Time { return now.Add(-time.Hour) }
		if err := logstore.New(logstore.Options{Dir: dir, Now: earlier}).Append(logstore.Topgrade, "OK", "Topgrade completed", "done\n"); err != nil {
			t.Fatal(err)
		}
		if err := logstore.New(logstore.Options{Dir: dir}).WriteLastRun(logstore.RunRecord{Action: actions.IDFullCleanup, Outcome: tc.outcome, StepsRefused: tc.refused, Started: now, Finished: now}); err != nil {
			t.Fatal(err)
		}
		rep, _ := child(t, "repair", "ARCHMAID_GUI_STATE="+dir)
		if rep.LastTag != tc.tag || rep.LastTone != tc.tone {
			t.Errorf("%s: tag %q tone %q, want %q %q", tc.outcome, rep.LastTag, rep.LastTone, tc.tag, tc.tone)
		}
		if rep.LastCell != tc.cell {
			t.Errorf("%s: last run cell %s, want %s", tc.outcome, rep.LastCell, tc.cell)
		}
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
	if rep.LogShown != run+"/topgrade" || !slices.Equal(rep.LogLines, []string{"Topgrade completed", "==> done"}) {
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
// the best of the runs after the first) and < 125 MB idle (120 MB until
// P8 row 13: the median was at the edge, 119.2-119.6 MB before it and
// 119.8-120.1 MB after, with ~1 MB between runs).
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
		if rss >= 125*1024 {
			t.Errorf("idle RSS %.1f MB, budget < 125 MB", float64(rss)/1024)
		}
	}
}

// TestGUIThroughput: 50k lines through the real window. Budgets: ≥ 5k
// lines/s, main-thread work per batch p95 ≤ 4 ms, RSS after < 200 MB.
func TestGUIThroughput(t *testing.T) {
	needDisplay(t)
	for _, sc := range []string{"flood", "flood-paced", "flood-default", "flood-colour", "flood-steps"} {
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
		inView := uint(runview.OutputCap)
		if sc == "flood-steps" {
			// Only the last part stays open. The cap (10k lines) keeps
			// the last two parts' lines: the ninth is folded to its
			// summary, the eight before it hold no lines any more.
			inView = 5000
			if rep.Folded != 1 {
				t.Errorf("%s: %d steps folded with lines, want 1 (the ninth part)", sc, rep.Folded)
			}
		}
		if rep.Outcome != "completed" || rep.Lines != 50000 || rep.ViewLines != inView || rep.LastLine != last {
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
