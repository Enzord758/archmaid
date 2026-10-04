package main

// The transcript harness: drives a whole menu action through Update against
// shimmed programs and records what happened — which programs ran, what was
// asked, the plan, every step transition, its output and its log entries — as
// a frontend-agnostic text transcript, golden-compared under
// testdata/transcripts/. Nothing is rendered, so a future engine can be held to
// the same goldens. See TESTING.md → Transcripts for the format.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	tea "github.com/charmbracelet/bubbletea"
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

// Answer is one scripted reply, consumed in order:
//
//   - confirm: Match is a substring of the confirm text; Reply is yes or no.
//   - password: Reply is correct (types shimPassword) or wrong.
//   - abort: fires ctrl+c then y as soon as the step whose title contains
//     Match turns running.
//   - select: answers the services multi-select; Reply is the space-separated
//     services to tick ("" ticks none).
type Answer struct {
	Kind, Match, Reply string
}

// shimPassword is the password the su shim accepts (see suRules).
const shimPassword = "hunter2"

// ── driver ───────────────────────────────────────────────────────────────────

// tickMsg replaces every tea.Tick (tickFn); the driver drops it unseen.
type tickMsg struct{}

// Driver pumps one menu action through Update and records its transcript.
//
// Cmds run synchronously in FIFO order and each resulting msg is fed back
// through Update; tea.BatchMsg is expanded in order; ticks are dropped; Init
// is never called, so the spinner never ticks. When nothing is left to run,
// an open prompt is answered from the script, and the action is over when
// there's neither work nor a prompt.
type Driver struct {
	t      *testing.T
	m      model
	script []Answer
	out    []string
	norm   *strings.Replacer

	shimLog string
	shimOff int
	logOff  map[string]int
	snap    string // last seen AUR snapshot content

	queue0  *stepState // &queue[0] of the planned queue, to spot a new one
	seen    []stepStatus
	flags   []string
	aborted bool // an abort answer fired
}

// NewDriver starts a driver on a fresh model at 100×40 with the status line
// pinned. Call it after installShims and after seeding HOME/logDir.
func NewDriver(t *testing.T, shimLog string, script ...Answer) *Driver {
	t.Helper()
	old := tickFn
	tickFn = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return tickMsg{} }
	}
	t.Cleanup(func() { tickFn = old })

	m := initialModel()
	m.status = testStatus
	// A blinking cursor's Focus cmd sleeps and re-arms forever; static is inert.
	m.password.Cursor.SetMode(cursor.CursorStatic)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})

	// longest first, so a path that prefixes another can't win
	pairs := [][2]string{{os.Getenv("HOME"), "$HOME"}, {logDir, "$LOGDIR"}}
	sort.Slice(pairs, func(i, j int) bool { return len(pairs[i][0]) > len(pairs[j][0]) })
	var rep []string
	for _, p := range pairs {
		if p[0] != "" {
			rep = append(rep, p[0], p[1])
		}
	}
	d := &Driver{t: t, m: m, script: script, shimLog: shimLog,
		logOff: map[string]int{}, norm: strings.NewReplacer(rep...)}
	d.snap, _ = readFileString(baselinePath())
	for _, f := range d.logFiles() { // only what this action writes
		b, _ := os.ReadFile(f)
		d.logOff[f] = len(b)
	}
	return d
}

func (d *Driver) emit(kind, format string, a ...any) {
	line := fmt.Sprintf("%-5s ", kind) + fmt.Sprintf(format, a...)
	d.out = append(d.out, strings.TrimRight(d.norm.Replace(line), " "))
}

// Select runs the menu item whose label contains sub to completion.
func (d *Driver) Select(sub string) {
	d.t.Helper()
	idx := -1
	for i, it := range menu {
		if strings.Contains(it.label, sub) {
			idx = i
			break
		}
	}
	if idx < 0 {
		d.t.Fatalf("no menu item %q", sub)
	}
	for d.m.cursor < idx {
		d.m = update(d.m, key("down"))
	}
	_, label, _ := strings.Cut(menu[idx].label, "  ") // drop the icon
	d.emit("action", "%q", label)
	d.pump(d.update(key("enter")))
}

// pump runs cmds (and everything they lead to) until nothing is left, then
// answers the open prompt, if any, and carries on.
func (d *Driver) pump(q []tea.Cmd) {
	for {
		for len(q) > 0 {
			c := q[0]
			q = q[1:]
			if c == nil {
				continue
			}
			switch msg := c().(type) {
			case nil, tickMsg, statusNowMsg: // the status line is pinned
			case tea.BatchMsg:
				q = append(q, msg...)
			default:
				// whatever the cmd ran synchronously (a probe, a discovery,
				// the audit) — or the async step child, whose line is known
				// to be logged by the time its first stream msg arrives
				d.drainExec()
				q = append(q, d.update(msg)...)
			}
		}
		keys := d.answer()
		if keys == nil {
			return
		}
		for _, k := range keys {
			q = append(q, d.update(k)...)
		}
	}
}

// update feeds one msg through Update and records what changed.
func (d *Driver) update(msg tea.Msg) []tea.Cmd {
	d.t.Helper()
	switch msg.(type) {
	case tea.KeyMsg, tea.WindowSizeMsg, sudoCachedMsg, sudoResultMsg, discoveryMsg,
		streamMsg, stepResultMsg, editorDoneMsg:
	default:
		d.t.Fatalf("unexpected msg %T (%v) — teach the driver about it\n%s", msg, msg, d.dump())
	}
	cur, seq, bufLen, wasScreen := d.m.current, d.m.stepSeq, d.m.buf.Len(), d.m.screen
	var curStatus stepStatus
	if cur >= 0 && cur < len(d.m.queue) {
		curStatus = d.m.queue[cur].status
	}

	next, cmd := d.m.Update(msg)
	d.m = next.(model)

	// A child launched by this Update logs itself asynchronously; its line is
	// drained with its first stream msg (or by the abort) so it never races in.
	if !(d.m.stepSeq != seq && d.m.proc != nil) {
		d.drainExec()
	}
	switch msg := msg.(type) {
	case streamMsg:
		if !msg.done && d.m.buf.Len() > bufLen { // dropped late output doesn't count
			d.emit("out", "%d %s", cur+1, msg.line)
		}
	case stepResultMsg:
		if cur >= 0 && cur < len(d.m.queue) && d.m.queue[cur].status != curStatus && msg.output != "" {
			for _, l := range strings.Split(strings.TrimRight(msg.output, "\n"), "\n") {
				d.emit("out", "%d %s", cur+1, l)
			}
		}
	}
	started := d.observe()
	if d.m.screen != wasScreen {
		switch d.m.screen {
		case screenNotice:
			d.emit("note", "%q", d.m.notice)
		case screenAudit:
			if dm, ok := msg.(discoveryMsg); ok {
				for _, l := range strings.Split(dm.report, "\n") {
					d.emit("audit", "%s", l)
				}
			}
		}
	}
	cmds := []tea.Cmd{cmd}
	if i := d.abortTarget(started); i >= 0 {
		d.abort(i)
	}
	return cmds
}

// observe emits the plan once a queue exists, then — in a fixed order — the
// steps that finished, changed plan flags, log and snapshot writes, and
// returns the steps that started.
func (d *Driver) observe() (started []int) {
	if len(d.m.queue) > 0 && &d.m.queue[0] != d.queue0 {
		d.queue0 = &d.m.queue[0]
		d.emit("plan", "%d steps", len(d.m.queue))
		d.seen = make([]stepStatus, len(d.m.queue))
		d.flags = make([]string, len(d.m.queue))
		for i, s := range d.m.queue {
			d.flags[i] = planFlags(s.step)
			d.emit("plan", "%d %q %s", i+1, s.step.title, d.flags[i])
		}
	}
	for i := range d.seen {
		if st := d.m.queue[i].status; st != d.seen[i] && st >= statusOK {
			d.seen[i] = st
			d.emit("step", "%d %s", i+1, statusName(st))
		}
	}
	for i := range d.flags {
		if f := planFlags(d.m.queue[i].step); f != d.flags[i] {
			d.flags[i] = f
			d.emit("plan", "%d %q %s", i+1, d.m.queue[i].step.title, f)
		}
	}
	d.drainLogs()
	d.drainSnapshot()
	for i := range d.seen {
		if st := d.m.queue[i].status; st != d.seen[i] {
			d.seen[i] = st
			d.emit("step", "%d %s", i+1, statusName(st))
			if st == statusRunning {
				started = append(started, i)
			}
		}
	}
	return started
}

// abortTarget is the started step the next abort answer is waiting for, or -1.
func (d *Driver) abortTarget(started []int) int {
	if len(d.script) == 0 || d.script[0].Kind != "abort" {
		return -1
	}
	for _, i := range started {
		if strings.Contains(d.m.queue[i].step.title, d.script[0].Match) {
			return i
		}
	}
	return -1
}

// abort answers ctrl+c → y on running step i. It first waits for the child
// to log itself, so the shim's SIGINT trap (if any) is installed and the exec
// line is deterministic.
func (d *Driver) abort(i int) {
	d.t.Helper()
	d.script = d.script[1:]
	if d.m.proc != nil {
		deadline := time.Now().Add(5 * time.Second)
		for {
			b, _ := os.ReadFile(d.shimLog)
			if len(b) > d.shimOff && b[len(b)-1] == '\n' {
				break
			}
			if time.Now().After(deadline) {
				d.t.Fatalf("step %d's child never reached its shim\n%s", i+1, d.dump())
			}
			time.Sleep(time.Millisecond)
		}
		d.drainExec()
	}
	d.emit("abort", "%d %q", i+1, d.m.queue[i].step.title)
	d.aborted = true
	for _, k := range []tea.KeyMsg{{Type: tea.KeyCtrlC}, key("y")} {
		next, cmd := d.m.Update(k)
		d.m = next.(model)
		if cmd != nil {
			d.t.Fatalf("abort key %q returned a cmd", k)
		}
	}
	d.observe()
}

// answer replies to the open prompt from the script; nil when there's none.
func (d *Driver) answer() []tea.Msg {
	d.t.Helper()
	var kind, text string
	switch d.m.screen {
	case screenConfirm:
		kind, text = "confirm", d.m.confirmMsg
	case screenPassword:
		kind = "password"
	case screenServices:
		kind, text = "select", strings.Join(d.m.services, " ")
	case screenRunning, screenDiscovering, screenAbortConfirm:
		d.t.Fatalf("stalled on screen %d with nothing left to run\n%s", d.m.screen, d.dump())
	default:
		return nil
	}
	if len(d.script) == 0 {
		d.t.Fatalf("unscripted %s prompt %q\n%s", kind, text, d.dump())
	}
	a := d.script[0]
	d.script = d.script[1:]
	if a.Kind != kind || !strings.Contains(text, a.Match) {
		d.t.Fatalf("script wants %s %q, got %s prompt %q\n%s", a.Kind, a.Match, kind, text, d.dump())
	}
	switch kind {
	case "password":
		pw, shown := "not-the-password", "<wrong>"
		if a.Reply == "correct" {
			pw, shown = shimPassword, "<correct>"
		}
		d.emit("auth", "password -> %s", shown)
		d.m.password.SetValue(pw)
		return []tea.Msg{key("enter")}
	case "select":
		want := strings.Fields(a.Reply)
		var keys []tea.Msg
		for _, svc := range d.m.services {
			for _, w := range want {
				if w == svc {
					keys = append(keys, key(" "))
				}
			}
			keys = append(keys, key("down"))
		}
		d.emit("pick", "[%s] -> [%s]", text, strings.Join(want, " "))
		return append(keys, key("enter"))
	}
	if a.Reply != "yes" && a.Reply != "no" {
		d.t.Fatalf("confirm reply %q, want yes or no", a.Reply)
	}
	d.emit("ask", "%q -> %s", text, a.Reply)
	return []tea.Msg{key(a.Reply[:1])}
}

var suUser = regexp.MustCompile(`^su \S+ `)

// drainExec emits the shim invocations logged since the last drain. Runs of
// one command are sorted: a pipeline's processes start concurrently, so their
// relative order isn't behaviour (an order between different commands is).
func (d *Driver) drainExec() {
	b, _ := os.ReadFile(d.shimLog)
	if len(b) <= d.shimOff {
		return
	}
	chunk := string(b[d.shimOff:])
	d.shimOff = len(b)
	lines := strings.Split(strings.TrimSuffix(chunk, "\n"), "\n")
	for i := 0; i < len(lines); {
		j := i + 1
		name, _, _ := strings.Cut(lines[i], " ")
		for j < len(lines) && strings.HasPrefix(lines[j], name+" ") {
			j++
		}
		sort.Strings(lines[i:j])
		i = j
	}
	for _, l := range lines {
		d.emit("exec", "%s", suUser.ReplaceAllLiteralString(l, "su $USER "))
	}
}

var logStamp = regexp.MustCompile(`^\[\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\] `)

func (d *Driver) logFiles() []string {
	files, _ := filepath.Glob(filepath.Join(resolveLogDir(), "*", "*.log"))
	sort.Strings(files)
	return files
}

// drainLogs emits new log entries (header lines only; bodies are the out
// lines already recorded), without timestamp or run directory. Entries
// written in one Update are grouped by category file.
func (d *Driver) drainLogs() {
	for _, f := range d.logFiles() {
		b, _ := os.ReadFile(f)
		if len(b) <= d.logOff[f] {
			continue
		}
		cat := strings.TrimSuffix(filepath.Base(f), ".log")
		for _, l := range strings.Split(string(b[d.logOff[f]:]), "\n") {
			if logStamp.MatchString(l) {
				d.emit("log", "%s %s", cat, logStamp.ReplaceAllString(l, ""))
			}
		}
		d.logOff[f] = len(b)
	}
}

// drainSnapshot emits the AUR maintainer snapshot whenever its content changes.
func (d *Driver) drainSnapshot() {
	s, _ := readFileString(baselinePath())
	if s == d.snap {
		return
	}
	d.snap = s
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		d.emit("snap", "unreadable")
		return
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var parts []string
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s=%q", n, m[n]))
	}
	d.emit("snap", "%s", strings.Join(parts, " "))
}

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// outcome classifies where the action ended up, independent of screens.
func (d *Driver) outcome() string {
	switch {
	case d.aborted, d.m.screen == screenSummary && d.m.aborted:
		return "aborted"
	case d.m.screen == screenSummary:
		return "completed"
	case d.m.screen == screenNotice:
		return "notice"
	case d.m.screen == screenAudit:
		return "report"
	case d.m.screen == screenMenu:
		return "cancelled"
	}
	d.t.Fatalf("action ended on screen %d\n%s", d.m.screen, d.dump())
	return ""
}

func (d *Driver) dump() string { return strings.Join(d.out, "\n") }

// Transcript ends the recording; every scripted answer must have been used.
func (d *Driver) Transcript() string {
	d.t.Helper()
	if len(d.script) > 0 {
		d.t.Errorf("unused script answers: %+v\n%s", d.script, d.dump())
	}
	d.emit("end", "outcome=%s errs=%d skipped=%d", d.outcome(), d.m.errs, d.m.skipped)
	return strings.Join(d.out, "\n") + "\n"
}

func planFlags(s step) string {
	var f []string
	if s.run != nil {
		f = append(f, "inproc")
	} else {
		f = append(f, "argv="+strings.Join(append([]string{s.command}, s.args...), " "))
	}
	for _, b := range []struct {
		on   bool
		name string
	}{{s.needsSudo, "sudo"}, {s.confirm != "", "confirm"}, {s.optional, "optional"}, {s.alwaysLog, "alwayslog"}} {
		if b.on {
			f = append(f, b.name)
		}
	}
	return strings.Join(append(f, "cat="+string(stepCategory(s))), " ")
}

func statusName(s stepStatus) string {
	return [...]string{"pending", "running", "ok", "failed", "skipped"}[s]
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
