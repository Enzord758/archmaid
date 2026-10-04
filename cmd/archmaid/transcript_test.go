package main

// The transcript harness: runs a whole menu action against shimmed programs
// and records what happened — which programs ran, what was asked, the plan,
// every step transition, its output and its log entries — as a
// frontend-agnostic text transcript, golden-compared under
// testdata/transcripts/. Nothing is rendered.
//
// It's two halves. internal/testtranscript's Recorder owns the format and the
// answer script; a Target runs the action and reports events. tuiTarget, here,
// pumps the model through Update; from P4 on a sessionTarget drives
// engine.Session against the same shims, and the same goldens hold it to the
// same behaviour. See TESTING.md → Transcripts.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/charmbracelet/bubbles/cursor"
	tea "github.com/charmbracelet/bubbletea"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

var updateTranscripts = flag.Bool("update-transcripts", false, "rewrite testdata/transcripts/*.golden")

// ── shims ────────────────────────────────────────────────────────────────────

// Rule answers the invocations of a shimmed command whose argv ("$*") starts
// with Prefix ("" matches anything). Rules are tried in order; the first match
// runs Script (raw sh), prints Out, then exits with Exit — or, with Block,
// execs `sleep 30` instead so the step can be aborted.
type Rule struct {
	Prefix string
	Out    string
	Exit   int
	Script string
	Block  bool
	// TrapINT makes the whole shim ignore SIGINT from its first line (before
	// it logs, so an abort can't race the trap); with Block, only SIGKILL
	// ends it.
	TrapINT bool
}

// Shims maps a command name to its rules. A command with no matching rule
// prints nothing and exits 0.
type Shims map[string][]Rule

// installShims writes one #!/bin/sh shim per command into a fresh bin dir and
// makes that dir the whole PATH, so anything not shimmed is "not installed".
// passthrough symlinks real tools (resolved on the original PATH) into it, for
// steps that need e.g. bash/find under a scratch HOME. Every shim appends
// "<cmd> <argv>" to the returned log before doing anything else.
func installShims(t *testing.T, sh Shims, passthrough ...string) (shimLog string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shimLog = filepath.Join(root, "exec.log")
	if err := os.WriteFile(shimLog, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := func(name string) {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return
		}
		real, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("passthrough %s: %v", name, err)
		}
		if err := os.Symlink(real, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	for name, rules := range sh {
		var b strings.Builder
		b.WriteString("#!/bin/sh\n")
		for _, r := range rules {
			if r.TrapINT {
				b.WriteString("trap '' INT\n")
				break
			}
		}
		fmt.Fprintf(&b, "printf '%%s\\n' %s >> \"$ARCHMAID_SHIM_LOG\"\n", shQuote(name)+`" $*"`)
		b.WriteString("case \"$*\" in\n")
		for _, r := range rules {
			fmt.Fprintf(&b, "%s*)\n", shQuote(r.Prefix))
			if r.Script != "" {
				b.WriteString(r.Script + "\n")
			}
			if r.Out != "" {
				for _, l := range strings.Split(strings.TrimSuffix(r.Out, "\n"), "\n") {
					fmt.Fprintf(&b, "printf '%%s\\n' %s\n", shQuote(l))
				}
			}
			if r.Block {
				link("sleep")
				b.WriteString("exec sleep 30\n")
			}
			fmt.Fprintf(&b, "exit %d;;\n", r.Exit)
		}
		b.WriteString("esac\nexit 0\n")
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range passthrough {
		link(name)
	}
	t.Setenv("PATH", dir)
	t.Setenv("ARCHMAID_SHIM_LOG", shimLog)
	return shimLog
}

// shQuote single-quotes s for sh.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ── script ───────────────────────────────────────────────────────────────────

// Answer is one scripted reply; see tt.Answer for the kinds. It's tt.Answer
// declared here (not aliased) so scenarios can write Answer{"confirm", …, "yes"}
// without go vet's composites check objecting to another package's struct.
type Answer struct{ Kind, Match, Reply string }

// shimPassword is the password the su shim accepts (see suRules).
const shimPassword = "hunter2"

// newTarget makes the Target a scenario runs on, once the scenario has set up
// its environment (scratch HOME and log dir, shims, fakes). tuiTarget is the
// only one until engine.Session exists (P4); sessionTarget plugs in here.
type newTarget func(t *testing.T, shimLog string) tt.Target

// record runs the menu item matching item on a fresh target and records it,
// answering prompts from script. $HOME and the log dir are normalised.
func record(t *testing.T, mk newTarget, shimLog, item string, script ...Answer) *tt.Recorder {
	t.Helper()
	tg := mk(t, shimLog)
	answers := make([]tt.Answer, len(script))
	for i, a := range script {
		answers[i] = tt.Answer(a)
	}
	r := tt.NewRecorder(t, answers, os.Getenv("HOME"), "$HOME", logDir, "$LOGDIR")
	r.Run(tg, item)
	return r
}

// Driver is an action recorded through the TUI, for a test that also checks
// the model it left behind (d.m).
type Driver struct {
	*tt.Recorder
	*tuiTarget
}

// run records item through a tuiTarget and keeps the model.
func run(t *testing.T, shimLog, item string, script ...Answer) *Driver {
	t.Helper()
	var tg *tuiTarget
	r := record(t, func(t *testing.T, shimLog string) tt.Target {
		tg = newTUITarget(t, shimLog)
		return tg
	}, shimLog, item, script...)
	return &Driver{r, tg}
}

// ── side effects ─────────────────────────────────────────────────────────────

// effects watches what an action leaves outside the frontend: the programs
// the shims logged, log entries and the AUR snapshot. It's target-agnostic: a
// sessionTarget running the engine against the same shims and log store reads
// them the same way.
type effects struct {
	shimLog string
	shimOff int
	logOff  map[string]int
	snap    string // last seen AUR snapshot content
}

// newEffects starts watching from now: only what the action writes counts.
func newEffects(shimLog string) *effects {
	fx := &effects{shimLog: shimLog, logOff: map[string]int{}}
	fx.snap, _ = readFileString(baselinePath())
	for _, f := range fx.logFiles() {
		b, _ := os.ReadFile(f)
		fx.logOff[f] = len(b)
	}
	return fx
}

// execs returns the shim invocations logged since the last call. Runs of one
// command with the same first argument are sorted: a pipeline's processes, or
// Repair's parallel `pacman -Qkq -- <chunk>` scans, start concurrently, so
// their relative order isn't behaviour (an order between different commands
// is, as is `pacman -Qq` before the scans it feeds).
func (fx *effects) execs() []tt.Event {
	b, _ := os.ReadFile(fx.shimLog)
	if len(b) <= fx.shimOff {
		return nil
	}
	chunk := string(b[fx.shimOff:])
	fx.shimOff = len(b)
	lines := strings.Split(strings.TrimSuffix(chunk, "\n"), "\n")
	runKey := func(l string) string {
		if f := strings.Fields(l); len(f) > 1 {
			return f[0] + " " + f[1]
		}
		return l
	}
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && runKey(lines[j]) == runKey(lines[i]) {
			j++
		}
		sort.Strings(lines[i:j])
		i = j
	}
	evs := make([]tt.Event, len(lines))
	for i, l := range lines {
		evs[i] = tt.Exec{Cmdline: l}
	}
	return evs
}

// awaitExec waits until a child has logged itself to its shim, so the shim's
// SIGINT trap (if any) is installed and its exec line is deterministic.
func (fx *effects) awaitExec(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		b, _ := os.ReadFile(fx.shimLog)
		if len(b) > fx.shimOff && b[len(b)-1] == '\n' {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

var logStamp = regexp.MustCompile(`^\[\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\] `)

func (fx *effects) logFiles() []string {
	files, _ := filepath.Glob(filepath.Join(resolveLogDir(), "*", "*.log"))
	sort.Strings(files)
	return files
}

// logs returns the new log entries (header lines only; bodies are the out
// lines already recorded), without timestamp or run directory, grouped by
// category file.
func (fx *effects) logs() []tt.Event {
	var evs []tt.Event
	for _, f := range fx.logFiles() {
		b, _ := os.ReadFile(f)
		if len(b) <= fx.logOff[f] {
			continue
		}
		cat := strings.TrimSuffix(filepath.Base(f), ".log")
		for _, l := range strings.Split(string(b[fx.logOff[f]:]), "\n") {
			if logStamp.MatchString(l) {
				evs = append(evs, tt.LogWritten{Category: cat, Header: logStamp.ReplaceAllString(l, "")})
			}
		}
		fx.logOff[f] = len(b)
	}
	return evs
}

// snapshot returns the AUR maintainer snapshot when its content changed.
func (fx *effects) snapshot() []tt.Event {
	s, _ := readFileString(baselinePath())
	if s == fx.snap {
		return nil
	}
	fx.snap = s
	var m map[string]snapEntry
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return []tt.Event{tt.SnapshotChanged{Unreadable: true}}
	}
	ev := tt.SnapshotChanged{Entries: []tt.SnapEntry{}}
	for n, e := range m {
		ev.Entries = append(ev.Entries, tt.SnapEntry{Name: n, Maintainer: e.Maintainer,
			HasCo: e.HasCo, CoMaintainers: e.CoMaintainers})
	}
	return []tt.Event{ev}
}

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// ── tuiTarget ────────────────────────────────────────────────────────────────

// tickMsg replaces every tea.Tick (tickFn); the target drops it unseen.
type tickMsg struct{}

// tuiTarget is the Target that drives the TUI's model through Update.
//
// Cmds run synchronously in FIFO order and each resulting msg is fed back
// through Update; tea.BatchMsg is expanded in order; ticks are dropped; Init
// is never called, so the spinner never ticks. Each Update's events are
// buffered and handed out before the next cmd runs. When nothing is left to
// run, an open prompt is reported, and the action is over when there's
// neither work nor a prompt.
type tuiTarget struct {
	m       model
	fx      *effects
	q       []tea.Cmd  // cmds still to run
	pending []tt.Event // events not yet handed out
	err     error      // the first fault; reported once pending is empty

	queue0  *stepState // &queue[0] of the planned queue, to spot a new one
	seen    []steps.Status
	flags   []tt.Step // the plan as last reported
	aborted bool      // an abort answer fired

	confirms []confirmInfo   // each confirm as it was answered (sessionTarget compares)
	gated    map[string]bool // steps a result's ConfirmNext put a confirm on, by ID

	// frames, when not nil, collects the view at each prompt answered and
	// at the end (TestTranscriptEngine compares engine mode's).
	frames []string
}

// confirmInfo is what the open confirm says past its text, from the model:
// the gate, or the current step's confirm (its kind as the engine names
// it: the audit gate where a result's ConfirmNext put it there).
func (tg *tuiTarget) confirmInfo() confirmInfo {
	m := tg.m
	if m.gateActive {
		return confirmInfo{Kind: engine.ConfirmGate, Default: m.confirmYes}
	}
	if m.current < 0 || m.current >= len(m.queue) {
		tg.fail("a confirm with no gate and no current step — teach the recorder")
		return confirmInfo{}
	}
	s := m.queue[m.current].step
	c := confirmInfo{Kind: engine.ConfirmStep, Step: s.ID, Default: m.confirmYes,
		Destructive: destructiveSteps[s.ID], Commits: s.OnAccept != nil}
	switch {
	case tg.gated[s.ID]:
		c.Kind = engine.ConfirmAuditGate
	case s.ID == steps.IDOrphansRemove:
		c.Kind = engine.ConfirmOrphans
	case s.ID == steps.IDDownloads:
		c.Kind = engine.ConfirmDownloads
	}
	if c.Kind == engine.ConfirmOrphans {
		c.Direct, c.Cascade = quoted(s.Items), quoted(s.Cascade)
	} else {
		c.Items = quoted(s.Items)
	}
	return c
}

// newTUITarget starts a target on a fresh model at 100×40 with the status
// line pinned. Call it after installShims and after seeding HOME/logDir.
func newTUITarget(t *testing.T, shimLog string) *tuiTarget {
	t.Helper()
	old := tickFn
	tickFn = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return tickMsg{} }
	}
	t.Cleanup(func() { tickFn = old })
	// The real binary always wires askpass, so sudo steps run `sudo -A`; the
	// wrapper is never invoked here (sudo is a shim), only named.
	oldWrapper := askpassWrapper
	askpassWrapper = "/run/user/test/archmaid-askpass/wrapper"
	t.Cleanup(func() { askpassWrapper = oldWrapper })

	m := initialModel()
	m.status = testStatus
	// A blinking cursor's Focus cmd sleeps and re-arms forever; static is inert.
	m.password.Cursor.SetMode(cursor.CursorStatic)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	return &tuiTarget{m: m, fx: newEffects(shimLog)}
}

func (tg *tuiTarget) push(evs ...tt.Event) { tg.pending = append(tg.pending, evs...) }

func (tg *tuiTarget) fail(format string, a ...any) {
	if tg.err == nil {
		tg.err = fmt.Errorf(format, a...)
	}
}

// Start moves the menu cursor to the item whose label contains sub and
// presses enter.
func (tg *tuiTarget) Start(sub string) (string, error) {
	idx := -1
	for i, it := range actions.Menu {
		if strings.Contains(it.Label, sub) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("no menu item %q", sub)
	}
	for tg.m.cursor < idx {
		tg.m = update(tg.m, key("down"))
	}
	label := actions.Menu[idx].Label
	tg.q = tg.update(key("enter"))
	return label, nil
}

// Next hands out the buffered events, running cmds until there are some;
// with no cmd left it reports the open prompt, or nil when there's none.
func (tg *tuiTarget) Next() (tt.Event, error) {
	for len(tg.pending) == 0 {
		if tg.err != nil {
			return nil, tg.err
		}
		if len(tg.q) == 0 {
			return tg.prompt()
		}
		c := tg.q[0]
		tg.q = tg.q[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil, tickMsg, statusNowMsg: // the status line is pinned
		case tea.BatchMsg:
			tg.q = append(tg.q, msg...)
		default:
			// whatever the cmd ran synchronously (a probe, a discovery, the
			// audit) — or the async step child, whose line is known to be
			// logged by the time its first stream msg arrives
			tg.push(tg.fx.execs()...)
			tg.q = append(tg.q, tg.update(msg)...)
		}
	}
	ev := tg.pending[0]
	tg.pending = tg.pending[1:]
	return ev, nil
}

// prompt reports the open prompt; nil when the action is over.
func (tg *tuiTarget) prompt() (tt.Event, error) {
	switch tg.m.screen {
	case screenConfirm:
		return tt.NeedsConfirm{Text: tg.m.confirmMsg}, nil
	case screenPassword:
		return tt.NeedsPassword{}, nil
	case screenServices:
		return tt.NeedsSelection{Options: slices.Clone(tg.m.services)}, nil
	case screenRunning, screenDiscovering, screenAbortConfirm:
		return nil, fmt.Errorf("stalled on screen %d with nothing left to run", tg.m.screen)
	}
	if tg.frames != nil {
		tg.frames = append(tg.frames, tg.m.View())
	}
	return nil, nil
}

// Reply answers the open prompt with keys, or aborts the running step.
func (tg *tuiTarget) Reply(a tt.Answer) error {
	if tg.frames != nil {
		tg.frames = append(tg.frames, tg.m.View())
	}
	var keys []tea.Msg
	switch a.Kind {
	case "confirm":
		tg.confirms = append(tg.confirms, tg.confirmInfo())
		keys = []tea.Msg{key(a.Reply[:1])}
	case "password":
		pw := "not-the-password"
		if a.Reply == "correct" {
			pw = shimPassword
		}
		tg.m.password.SetValue(pw)
		keys = []tea.Msg{key("enter")}
	case "select":
		want := strings.Fields(a.Reply)
		for _, svc := range tg.m.services {
			if slices.Contains(want, svc) {
				keys = append(keys, key(" "))
			}
			keys = append(keys, key("down"))
		}
		keys = append(keys, key("enter"))
	case "abort":
		return tg.abort()
	default:
		return fmt.Errorf("no reply of kind %q", a.Kind)
	}
	for _, k := range keys {
		tg.q = append(tg.q, tg.update(k)...)
	}
	return nil
}

// abort answers ctrl+c → y on the running step. It first waits for the child
// to log itself, so the shim's SIGINT trap (if any) is installed and the exec
// line is deterministic.
func (tg *tuiTarget) abort() error {
	i := tg.m.current
	if i < 0 || i >= len(tg.m.queue) || tg.m.queue[i].status != steps.Running {
		return fmt.Errorf("abort with no running step (current %d)", i)
	}
	if tg.m.proc != nil {
		if !tg.fx.awaitExec(5 * time.Second) {
			return fmt.Errorf("step %d's child never reached its shim", i+1)
		}
		tg.push(tg.fx.execs()...)
	}
	tg.push(tt.Aborted{Index: i})
	tg.aborted = true
	for _, k := range []tea.KeyMsg{{Type: tea.KeyCtrlC}, key("y")} {
		next, cmd := tg.m.Update(k)
		tg.m = next.(model)
		if cmd != nil {
			return fmt.Errorf("abort key %q returned a cmd", k)
		}
	}
	// The abort settles the checklist: the running step is Aborted and every
	// pending one behind it Skipped. The abort line stands for those changes
	// (a step line each would say nothing it doesn't), so check them and
	// take them as seen.
	for j := i; j < len(tg.seen); j++ {
		want := steps.Skipped
		if j == i {
			want = steps.Aborted
		} else if tg.seen[j] != steps.Pending {
			continue
		}
		if st := tg.m.queue[j].status; st != want {
			return fmt.Errorf("after the abort step %d is %d, want %d", j+1, st, want)
		}
		tg.seen[j] = want
	}
	tg.observe(-1)
	return nil
}

// update feeds one msg through Update and buffers what changed.
func (tg *tuiTarget) update(msg tea.Msg) []tea.Cmd {
	switch msg.(type) {
	case tea.KeyMsg, tea.WindowSizeMsg, sudoCachedMsg, sudoResultMsg, discoveryMsg,
		streamBatchMsg, stepResultMsg, editorDoneMsg:
	default:
		tg.fail("unexpected msg %T (%v) — teach the target about it", msg, msg)
		return nil
	}
	cur, seq, wasScreen := tg.m.current, tg.m.stepSeq, tg.m.screen
	inserted := -1 // a step this Update put into the queue
	var curStatus steps.Status
	if cur >= 0 && cur < len(tg.m.queue) {
		curStatus = tg.m.queue[cur].status
	}

	next, cmd := tg.m.Update(msg)
	tg.m = next.(model)
	// An action that audits starts the lookup on its own goroutine when it's
	// chosen (auditFetch). Let it finish here, so its pacman -Qqm lands in
	// this Update's exec lines rather than at a racy point later.
	if f := tg.m.auditFetch; f != nil {
		select {
		case <-f.Done():
		case <-time.After(5 * time.Second):
			tg.fail("the prefetched AUR audit never finished")
			return nil
		}
	}

	// A child launched by this Update logs itself asynchronously; its line is
	// drained with its first stream msg (or by the abort) so it never races in.
	if !(tg.m.stepSeq != seq && tg.m.proc != nil) {
		tg.push(tg.fx.execs()...)
	}
	switch msg := msg.(type) {
	case streamBatchMsg:
		// One out line per streamed line, however the pump batched them.
		// Dropped late output doesn't count (a batch ending in done may have
		// launched the next step, which resets buf, so judge by seq).
		// The lines as the TUI shows them: sanitized one by one.
		if msg.seq == seq {
			for _, l := range msg.lines {
				tg.push(tt.OutputLine{Index: cur, Text: sanitize.String(l, sanitize.KeepSGR)})
			}
		}
	case stepResultMsg:
		accepted := cur >= 0 && cur < len(tg.m.queue) && tg.m.queue[cur].status != curStatus
		// The output as handleStepResult shows it: a refused result with
		// the refusal behind it, sanitized whole, then split.
		res := msg.Result
		if err := res.Check(); err != nil {
			res = steps.Result{Output: res.Output + "\narchmaid: " + err.Error(), Err: err, ConfirmNext: res.ConfirmNext}
		}
		if accepted && res.Output != "" {
			out := sanitize.String(strings.TrimRight(res.Output, "\n"), sanitize.KeepSGR)
			for _, l := range strings.Split(out, "\n") {
				tg.push(tt.OutputLine{Index: cur, Text: l})
			}
		}
		if accepted && res.Err == nil && res.InsertNext != nil {
			inserted = cur + 1
		}
		if accepted && res.ConfirmNext != "" && cur+1 < len(tg.m.queue) {
			if tg.gated == nil {
				tg.gated = map[string]bool{}
			}
			tg.gated[tg.m.queue[cur+1].step.ID] = true
		}
	}
	tg.observe(inserted)
	if tg.m.screen != wasScreen {
		switch tg.m.screen {
		case screenNotice:
			tg.push(tt.Notice{Text: tg.m.notice})
		case screenAudit:
			if dm, ok := msg.(discoveryMsg); ok {
				r, _ := dm.d.(actions.Report)
				tg.push(tt.OutputReport{Text: r.Text})
			}
		}
	}
	return []tea.Cmd{cmd}
}

// observe buffers the plan once a queue exists, then — in a fixed order — the
// steps that finished (with a step inserted at index inserted, -1 for none,
// in its place among them), changed plan flags, log and snapshot writes, and
// the steps that started.
func (tg *tuiTarget) observe(inserted int) {
	q := tg.m.queue
	if inserted >= 0 {
		// The same queue, one step longer (it may have moved in memory):
		// shift the bookkeeping behind the insertion point, not a new plan.
		tg.queue0 = &q[0]
		tg.seen = slices.Insert(tg.seen, inserted, steps.Pending)
		tg.flags = slices.Insert(tg.flags, inserted, planStep(q[inserted].step))
	}
	if len(q) > 0 && &q[0] != tg.queue0 {
		tg.queue0 = &q[0]
		tg.seen = make([]steps.Status, len(q))
		tg.flags = make([]tt.Step, len(q))
		for i, s := range q {
			tg.flags[i] = planStep(s.step)
		}
		tg.push(tt.QueueStarted{Steps: slices.Clone(tg.flags)})
	}
	for i := range tg.seen {
		if i == inserted { // in place: after the step that inserted it finished
			tg.push(tt.StepInserted{Index: i, Step: tg.flags[i]})
		}
		if st := q[i].status; st != tg.seen[i] && st >= steps.OK {
			tg.seen[i] = st
			tg.push(tt.StepDone{Index: i, Status: planStatus(st)})
		}
	}
	for i := range tg.flags {
		if s := planStep(q[i].step); !s.SameFlags(tg.flags[i]) {
			tg.flags[i] = s
			tg.push(tt.PlanChanged{Index: i, Step: s})
		}
	}
	tg.push(tg.fx.logs()...)
	tg.push(tg.fx.snapshot()...)
	for i := range tg.seen {
		if st := q[i].status; st != tg.seen[i] {
			tg.seen[i] = st
			if st != steps.Running {
				tg.fail("step %d went back to %s — teach the recorder", i+1, planStatus(st))
				continue
			}
			tg.push(tt.StepStarted{Index: i})
		}
	}
}

// Outcome classifies where the action ended, independent of screens.
func (tg *tuiTarget) Outcome() (tt.Outcome, error) {
	out := tt.Outcome{Errs: tg.m.errs, Skipped: tg.m.skipped}
	switch {
	case tg.aborted, tg.m.screen == screenSummary && tg.m.aborted:
		out.Kind = "aborted"
	case tg.m.screen == screenSummary:
		out.Kind = "completed"
	case tg.m.screen == screenNotice:
		out.Kind = "notice"
	case tg.m.screen == screenAudit:
		out.Kind = "report"
	case tg.m.screen == screenMenu:
		out.Kind = "cancelled"
	default:
		return out, fmt.Errorf("action ended on screen %d", tg.m.screen)
	}
	return out, nil
}

// planStep is what a transcript records of a step.
func planStep(s steps.Step) tt.Step {
	p := tt.Step{Title: s.Title, InProc: s.Run != nil, Sudo: s.NeedsPriv, Confirm: s.Confirm != "",
		Optional: s.Optional, AlwaysLog: s.AlwaysLog, Category: string(s.LogCategory())}
	if !p.InProc {
		p.Argv = append([]string(nil), s.Argv...)
	}
	return p
}

// planStatus is how the checklist draws s: every "didn't run" state is a skip.
func planStatus(s steps.Status) tt.Status {
	switch s {
	case steps.Pending:
		return tt.Pending
	case steps.Running:
		return tt.Running
	case steps.OK:
		return tt.OK
	case steps.Failed:
		return tt.Failed
	case steps.Skipped, steps.AuthRefused, steps.Aborted:
		return tt.Skipped
	}
	panic(fmt.Sprintf("unknown steps.Status %d", s))
}

// checkGolden compares got with testdata/transcripts/<name>.golden, or
// rewrites it under -update-transcripts.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "transcripts", name+".golden")
	if *updateTranscripts {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-transcripts to create it)\n%s", err, got)
	}
	if string(want) == got {
		return
	}
	w, g := strings.Split(string(want), "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			t.Fatalf("transcript %s differs at line %d:\n  want %q\n  got  %q\n(run with -update-transcripts if intended)\n\n%s",
				name, i+1, wl, gl, got)
		}
	}
}
