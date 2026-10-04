package testtranscript

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Target runs one action and reports what happens, one Event at a time. It
// is driven from the test goroutine only.
//
// Start chooses the menu action whose label contains action and returns the
// label as shown (the TUI's icon dropped). Next does the next piece of work
// and returns what it led to, in order; nil when the action is over — nothing
// left to run and no prompt open. A Needs* event is returned only once all
// other work is done, and the Recorder answers it with Reply before calling
// Next again. Reply also carries out an abort answer, on the step that just
// started; the Exec lines of that step's child, if any, come before the
// Aborted event. Outcome says where the action ended.
//
// An error is a fault in the harness or the target (a stall, a msg nobody
// taught it), not behaviour to record: the Recorder fails the test with the
// transcript so far.
type Target interface {
	Start(action string) (label string, err error)
	Next() (Event, error)
	Reply(Answer) error
	Outcome() (Outcome, error)
}

// Answer is one scripted reply, consumed in order:
//
//   - confirm: Match is a substring of the confirm text; Reply is yes or no.
//   - password: Reply is correct (the password the fake su accepts) or wrong.
//   - abort: fires as soon as the step whose title contains Match starts.
//   - select: answers the multi-select; Reply is the space-separated options
//     to tick ("" ticks none).
type Answer struct {
	Kind, Match, Reply string
}

// Outcome is where an action ended.
type Outcome struct {
	// Kind is completed, aborted, cancelled (back on the menu), notice or
	// report.
	Kind          string
	Errs, Skipped int
}

// Recorder turns a Target's events into transcript lines and answers its
// prompts from the script.
type Recorder struct {
	tb     testing.TB
	script []Answer
	norm   *strings.Replacer
	lines  []string
	plan   []Step
}

// NewRecorder records against script. replace is old, new pairs applied to
// every line (the scratch $HOME and log dir as "$HOME", "$LOGDIR"); the
// longest old string wins, so a path that prefixes another can't, and an
// empty one is ignored.
func NewRecorder(tb testing.TB, script []Answer, replace ...string) *Recorder {
	tb.Helper()
	if len(replace)%2 != 0 {
		tb.Fatalf("NewRecorder: odd replace list %q", replace)
	}
	var pairs [][2]string
	for i := 0; i < len(replace); i += 2 {
		if replace[i] != "" {
			pairs = append(pairs, [2]string{replace[i], replace[i+1]})
		}
	}
	slices.SortStableFunc(pairs, func(a, b [2]string) int { return len(b[0]) - len(a[0]) })
	var rep []string
	for _, p := range pairs {
		rep = append(rep, p[0], p[1])
	}
	return &Recorder{tb: tb, script: slices.Clone(script), norm: strings.NewReplacer(rep...)}
}

// Run records action on tg to its end: the action line, every event, the
// replies, then the outcome. Every scripted answer must be used.
func (r *Recorder) Run(tg Target, action string) {
	r.tb.Helper()
	label, err := tg.Start(action)
	if err != nil {
		r.fatal(err)
	}
	r.emit("action", "%q", label)
	for {
		ev, err := tg.Next()
		if err != nil {
			r.fatal(err)
		}
		if ev == nil {
			break
		}
		r.event(tg, ev)
	}
	out, err := tg.Outcome()
	if err != nil {
		r.fatal(err)
	}
	if len(r.script) > 0 {
		r.tb.Errorf("unused script answers: %+v\n%s", r.script, r.Dump())
	}
	r.emit("end", "outcome=%s errs=%d skipped=%d", out.Kind, out.Errs, out.Skipped)
}

// Transcript is the recording, one line per event, newline-terminated.
func (r *Recorder) Transcript() string { return r.Dump() + "\n" }

// Dump is the recording so far, for failure messages.
func (r *Recorder) Dump() string { return strings.Join(r.lines, "\n") }

func (r *Recorder) fatal(err error) {
	r.tb.Helper()
	r.tb.Fatalf("%v\n%s", err, r.Dump())
}

func (r *Recorder) emit(kind, format string, a ...any) {
	line := fmt.Sprintf("%-5s ", kind) + fmt.Sprintf(format, a...)
	r.lines = append(r.lines, strings.TrimRight(r.norm.Replace(line), " "))
}

// suUser matches su's user argument, with or without a `-s <shell>` before it.
var suUser = regexp.MustCompile(`^su ((?:-s \S+ )?)\S+`)

// event writes ev, answering it first when it's a prompt.
func (r *Recorder) event(tg Target, ev Event) {
	r.tb.Helper()
	switch ev := ev.(type) {
	case QueueStarted:
		r.plan = slices.Clone(ev.Steps)
		r.emit("plan", "%d steps", len(r.plan))
		for i, s := range r.plan {
			r.emit("plan", "%d %q %s", i+1, s.Title, flags(s))
		}
	case StepInserted:
		r.plan = slices.Insert(r.plan, r.index(ev.Index, 1), ev.Step)
		r.emit("plan", "%d steps", len(r.plan))
		r.emit("plan", "+%d %q %s", ev.Index+1, ev.Step.Title, flags(ev.Step))
	case PlanChanged:
		r.plan[r.index(ev.Index, 0)] = ev.Step
		r.emit("plan", "%d %q %s", ev.Index+1, ev.Step.Title, flags(ev.Step))
	case StepStarted:
		r.emit("step", "%d %s", r.index(ev.Index, 0)+1, Running)
		if len(r.script) > 0 && r.script[0].Kind == "abort" && strings.Contains(r.plan[ev.Index].Title, r.script[0].Match) {
			a := r.script[0]
			r.script = r.script[1:]
			r.reply(tg, a)
		}
	case StepDone:
		if ev.Status < OK || ev.Status > Skipped {
			r.fatal(fmt.Errorf("step %d done with status %s", ev.Index+1, ev.Status))
		}
		r.emit("step", "%d %s", r.index(ev.Index, 0)+1, ev.Status)
	case OutputLine:
		r.emit("out", "%d %s", ev.Index+1, ev.Text)
	case Exec:
		r.emit("exec", "%s", suUser.ReplaceAllString(ev.Cmdline, "su ${1}$$USER"))
	case LogWritten:
		r.emit("log", "%s %s", ev.Category, ev.Header)
	case SnapshotChanged:
		r.emit("snap", "%s", snapshot(ev))
	case Aborted:
		r.emit("abort", "%d %q", ev.Index+1, r.plan[r.index(ev.Index, 0)].Title)
	case Notice:
		r.emit("note", "%q", ev.Text)
	case OutputReport:
		for _, l := range strings.Split(ev.Text, "\n") {
			r.emit("audit", "%s", l)
		}
	case NeedsConfirm:
		a := r.next("confirm", ev.Text)
		if a.Reply != "yes" && a.Reply != "no" {
			r.fatal(fmt.Errorf("confirm reply %q, want yes or no", a.Reply))
		}
		r.emit("ask", "%q -> %s", ev.Text, a.Reply)
		r.reply(tg, a)
	case NeedsPassword:
		a := r.next("password", "")
		shown := "<wrong>"
		if a.Reply == "correct" {
			shown = "<correct>"
		}
		r.emit("auth", "password -> %s", shown)
		r.reply(tg, a)
	case NeedsSelection:
		a := r.next("select", strings.Join(ev.Options, " "))
		r.emit("pick", "[%s] -> [%s]", strings.Join(ev.Options, " "), strings.Join(strings.Fields(a.Reply), " "))
		r.reply(tg, a)
	default:
		r.fatal(fmt.Errorf("event %T (%+v) has no transcript line — teach the recorder", ev, ev))
	}
}

// index checks i against the plan (extra allows one past the end, for an
// insertion) and returns it.
func (r *Recorder) index(i, extra int) int {
	r.tb.Helper()
	if i < 0 || i >= len(r.plan)+extra {
		r.fatal(fmt.Errorf("step index %d outside the %d-step plan", i, len(r.plan)))
	}
	return i
}

// next takes the scripted answer for an open prompt of kind with text.
func (r *Recorder) next(kind, text string) Answer {
	r.tb.Helper()
	if len(r.script) == 0 {
		r.fatal(fmt.Errorf("unscripted %s prompt %q", kind, text))
	}
	a := r.script[0]
	r.script = r.script[1:]
	if a.Kind != kind || !strings.Contains(text, a.Match) {
		r.fatal(fmt.Errorf("script wants %s %q, got %s prompt %q", a.Kind, a.Match, kind, text))
	}
	return a
}

func (r *Recorder) reply(tg Target, a Answer) {
	r.tb.Helper()
	if err := tg.Reply(a); err != nil {
		r.fatal(err)
	}
}

// flags is a plan line's tail: inproc or argv=…, the set flags, the category.
func flags(s Step) string {
	var f []string
	if s.InProc {
		f = append(f, "inproc")
	} else {
		f = append(f, "argv="+strings.Join(s.Argv, " "))
	}
	for _, b := range []struct {
		on   bool
		name string
	}{{s.Sudo, "sudo"}, {s.Confirm, "confirm"}, {s.Optional, "optional"}, {s.AlwaysLog, "alwayslog"}} {
		if b.on {
			f = append(f, b.name)
		}
	}
	return strings.Join(append(f, "cat="+s.Category), " ")
}

// snapshot is a snap line: name="maintainer" per package, sorted, plus
// +co[a,b] when it has co-maintainers or +co? for an entry with no
// co-maintainer record (old format).
func snapshot(ev SnapshotChanged) string {
	if ev.Unreadable {
		return "unreadable"
	}
	es := slices.Clone(ev.Entries)
	slices.SortFunc(es, func(a, b SnapEntry) int { return strings.Compare(a.Name, b.Name) })
	parts := make([]string, 0, len(es))
	for _, e := range es {
		p := fmt.Sprintf("%s=%q", e.Name, e.Maintainer)
		switch {
		case !e.HasCo:
			p += "+co?"
		case len(e.CoMaintainers) > 0:
			p += "+co[" + strings.Join(e.CoMaintainers, ",") + "]"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}
