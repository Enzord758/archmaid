package main

// tuiEngineTarget: the transcript Target that drives the TUI's model in
// engine mode — keys through Update, the model's actions on a syncDriver in
// pull mode, its Events back through Update — and records what the model
// then shows: the checklist's statuses, its plan flags, the screen. It is
// the target TestTranscript records the goldens through; until the old
// path is gone, TestTranscriptEngine also holds each prompt and end screen
// to the old path's frame.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/charmbracelet/bubbles/cursor"
	tea "github.com/charmbracelet/bubbletea"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// TestTranscriptEngine replays every scenario through the old path and
// through the TUI in engine mode, and compares their frames.
func TestTranscriptEngine(t *testing.T) {
	for _, sc := range transcriptScenarios {
		t.Run(sc.name, func(t *testing.T) {
			if *updateTranscripts {
				t.Skip("-update-transcripts records through TestTranscript only")
			}
			var old *tuiTarget
			if !t.Run("old", func(t *testing.T) {
				sc.run(t, func(t *testing.T, shimLog string) tt.Target {
					old = newTUITarget(t, shimLog)
					old.frames = []string{}
					return old
				})
			}) {
				t.Fatal("the old path's reference run failed")
			}
			var tg *tuiEngineTarget
			got := sc.run(t, func(t *testing.T, shimLog string) tt.Target {
				tg = newTUIEngineTarget(t, shimLog)
				return tg
			}).Transcript()
			checkGolden(t, sc.name, got)
			if len(tg.frames) != len(old.frames) {
				t.Fatalf("engine mode showed %d prompt/end frames, the old path %d", len(tg.frames), len(old.frames))
			}
			for i := range tg.frames {
				if tg.frames[i] != old.frames[i] {
					t.Errorf("frame %d differs:\nengine:\n%s\nold:\n%s", i+1, tg.frames[i], old.frames[i])
				}
			}
		})
	}
}

type tuiEngineTarget struct {
	m  model
	d  *syncDriver
	fx *effects

	pending []tt.Event
	err     error

	seen    []steps.Status
	flags   []tt.Step
	aborted bool     // the Aborted line was recorded
	frames  []string // the view at each prompt answered and at the end
}

func newTUIEngineTarget(t *testing.T, shimLog string) *tuiEngineTarget {
	t.Helper()
	old := tickFn
	tickFn = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return tickMsg{} }
	}
	t.Cleanup(func() { tickFn = old })
	oldWrapper := askpassWrapper
	askpassWrapper = "/run/user/test/archmaid-askpass/wrapper"
	t.Cleanup(func() { askpassWrapper = oldWrapper })

	opts := engine.Options{DryRun: dryRun, Env: actionEnv()}
	if err := opts.Validate(); err != nil {
		t.Fatal(err)
	}
	tg := &tuiEngineTarget{fx: newEffects(shimLog)}
	tg.d = newSyncDriver(engine.New(opts), syncDeps{Priv: privileged(), Log: logs()})
	tg.d.afterWork = func() { tg.push(tg.fx.execs()...) }
	t.Cleanup(tg.d.Close)

	m := initialModel()
	m.status = testStatus
	m.password.Cursor.SetMode(cursor.CursorStatic)
	m.eng = newEngineState(tg.d)
	tg.m = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	return tg
}

func (tg *tuiEngineTarget) push(evs ...tt.Event) { tg.pending = append(tg.pending, evs...) }

func (tg *tuiEngineTarget) fail(format string, a ...any) {
	if tg.err == nil {
		tg.err = fmt.Errorf(format, a...)
	}
}

// keys feeds keys through Update, then the Events the Driver published
// meanwhile (a synchronous Send's whole Output).
func (tg *tuiEngineTarget) keys(keys ...tea.Msg) {
	n := tg.d.launches
	for _, k := range keys {
		tg.m = update(tg.m, k)
	}
	tg.deliver(tg.d.published(), n)
}

// deliver hands the model one batch of Events and records what changed. A
// prefetch the batch started is awaited (its pacman -Qqm lands with the
// action's own queries); a child it launched logs itself later, with its
// first output or exit.
func (tg *tuiEngineTarget) deliver(evs []engine.Event, launches int) {
	for _, f := range tg.d.fetches {
		select {
		case <-f.Done():
		case <-time.After(5 * time.Second):
			tg.fail("the prefetched AUR audit never finished")
		}
	}
	if tg.d.launches == launches {
		tg.push(tg.fx.execs()...)
	}
	if len(evs) == 0 {
		return
	}
	wasScreen := tg.m.screen
	next, _ := tg.m.Update(engineEventsMsg{evs: evs})
	tg.m = next.(model)

	// What the batch put in the output pane, line by line, and where a
	// step was inserted (the model's checklist says that it grew, not
	// where).
	var out []tt.Event
	inserted := -1
	var report string
	for _, ev := range evs {
		switch ev := ev.(type) {
		case engine.OutputChunk:
			for _, l := range ev.Lines {
				out = append(out, tt.OutputLine{Index: ev.Index, Text: l})
			}
		case engine.StepInserted:
			inserted = ev.Index
		case engine.OutputReport:
			report = ev.Text
		}
	}
	tg.observe(out, inserted)
	if tg.m.screen != wasScreen {
		switch tg.m.screen {
		case screenNotice:
			tg.push(tt.Notice{Text: tg.m.notice})
		case screenAudit:
			tg.push(tt.OutputReport{Text: report})
		}
	}
}

// observe records, from the model, in the transcript's order: the plan once
// a queue exists, the output lines, the steps that finished (an inserted
// one in its place), changed plan flags, log and snapshot writes, and the
// steps that started.
func (tg *tuiEngineTarget) observe(out []tt.Event, inserted int) {
	q := tg.m.queue
	if tg.seen == nil && len(q) > 0 {
		tg.seen = make([]steps.Status, len(q))
		tg.flags = make([]tt.Step, len(q))
		for i, s := range q {
			tg.flags[i] = transcriptStep(s.plan)
		}
		tg.push(tt.QueueStarted{Steps: slices.Clone(tg.flags)})
	}
	if inserted >= 0 {
		tg.seen = slices.Insert(tg.seen, inserted, steps.Pending)
		tg.flags = slices.Insert(tg.flags, inserted, transcriptStep(q[inserted].plan))
	}
	tg.push(out...)
	for i := range tg.seen {
		if i == inserted {
			tg.push(tt.StepInserted{Index: i, Step: tg.flags[i]})
		}
		st := q[i].status
		switch {
		case st == tg.seen[i] || st < steps.OK:
		case st == steps.Aborted:
			tg.push(tt.Aborted{Index: i})
			tg.aborted = true
		case tg.aborted && st == steps.Skipped && tg.seen[i] == steps.Pending:
			// what the abort left: the Aborted line stands for it
		default:
			tg.push(tt.StepDone{Index: i, Status: planStatus(st)})
		}
		if st >= steps.OK {
			tg.seen[i] = st
		}
	}
	for i := range tg.flags {
		if s := transcriptStep(q[i].plan); !s.SameFlags(tg.flags[i]) {
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
				tg.fail("step %d went back to %s", i+1, planStatus(st))
				continue
			}
			tg.push(tt.StepStarted{Index: i})
		}
	}
}

func (tg *tuiEngineTarget) Start(sub string) (string, error) {
	idx := slices.IndexFunc(actions.Menu, func(a actions.Action) bool { return strings.Contains(a.Label, sub) })
	if idx < 0 {
		return "", fmt.Errorf("no menu item %q", sub)
	}
	for tg.m.cursor < idx {
		tg.m = update(tg.m, key("down"))
	}
	tg.keys(key("enter"))
	return actions.Menu[idx].Label, nil
}

func (tg *tuiEngineTarget) Next() (tt.Event, error) {
	for len(tg.pending) == 0 {
		if tg.err != nil {
			return nil, tg.err
		}
		n := tg.d.launches
		evs := tg.d.Pull()
		if len(evs) == 0 {
			return tg.prompt()
		}
		tg.deliver(evs, n)
	}
	ev := tg.pending[0]
	tg.pending = tg.pending[1:]
	return ev, nil
}

// prompt reports the open prompt from the screen; nil when the action is
// over (a report ends it, as on the old path).
func (tg *tuiEngineTarget) prompt() (tt.Event, error) {
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
	tg.frames = append(tg.frames, tg.m.View())
	return nil, nil
}

func (tg *tuiEngineTarget) Reply(a tt.Answer) error {
	tg.frames = append(tg.frames, tg.m.View())
	var keys []tea.Msg
	switch a.Kind {
	case "confirm":
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
		if tg.m.screen != screenRunning {
			return fmt.Errorf("abort on screen %d", tg.m.screen)
		}
		if len(tg.d.child) > 0 {
			if !tg.fx.awaitExec(5 * time.Second) {
				return fmt.Errorf("the running step's child never reached its shim")
			}
			tg.push(tg.fx.execs()...)
		}
		keys = []tea.Msg{tea.KeyMsg{Type: tea.KeyCtrlC}, key("y")}
	default:
		return fmt.Errorf("no reply of kind %q", a.Kind)
	}
	tg.keys(keys...)
	return nil
}

func (tg *tuiEngineTarget) Outcome() (tt.Outcome, error) {
	out := tt.Outcome{Errs: tg.m.errs, Skipped: tg.m.skipped}
	switch {
	case tg.m.eng.aborted, tg.m.screen == screenSummary && tg.m.aborted:
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
