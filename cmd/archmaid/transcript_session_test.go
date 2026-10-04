package main

// sessionTarget: the transcript Target that drives internal/engine's
// Session instead of the TUI's model, playing the Session's Effects against
// the same shims, log store and fake AUR as tuiTarget, so the same goldens
// hold both front ends to the same behaviour (TESTING.md → Transcripts).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// sessionSkips are the scenarios the Session can't run yet, each with the
// reason; TestTranscriptSession skips them by name, never silently.
var sessionSkips = map[string]string{
	"fullcleanup_abort_topgrade.shim": needsAbort,
	"fullcleanup_abort_sigkill.shim":  needsAbort,
	"services_select2.shim":           needsSelection,
	"services_none_selected.shim":     needsSelection,
	"auraudit_first_baseline.shim":    needsReport,
	"auraudit_takeover.shim":          needsReport,
}

const (
	needsAbort     = "needs E5: Abort"
	needsSelection = "needs E5: the services selection (Select)"
	needsReport    = "needs E5: the audit report (OutputReport)"
)

// TestTranscriptSession replays every scenario through the engine's
// Session, against the goldens TestTranscript records through the TUI.
func TestTranscriptSession(t *testing.T) {
	for name := range sessionSkips {
		if !slices.ContainsFunc(transcriptScenarios, func(sc transcriptScenario) bool { return sc.name == name }) {
			t.Errorf("sessionSkips names %q, which is no scenario", name)
		}
	}
	for _, sc := range transcriptScenarios {
		t.Run(sc.name, func(t *testing.T) {
			if why, ok := sessionSkips[sc.name]; ok {
				t.Skip(why)
			}
			if *updateTranscripts {
				t.Skip("-update-transcripts records through the TUI (TestTranscript) only")
			}
			// What the golden doesn't hold, the TUI's own run of the
			// scenario says: its confirms past their text, its last-run.json.
			var ref tuiReference
			if !t.Run("tui", func(t *testing.T) { ref = recordTUIReference(t, sc) }) {
				t.Fatal("the TUI's reference run failed")
			}
			var tg *sessionTarget
			got := sc.run(t, func(t *testing.T, shimLog string) tt.Target {
				tg = newSessionTarget(t, shimLog)
				return tg
			}).Transcript()
			checkGolden(t, sc.name, got)
			tg.compare(t, ref)
		})
	}
}

// tuiReference is what the TUI's run of a scenario showed that its golden
// doesn't record.
type tuiReference struct {
	confirms []confirmInfo
	lastRun  *logstore.RunRecord // last-run.json as the TUI left it, nil if none
}

// recordTUIReference runs sc through the TUI and keeps what it asked and
// recorded.
func recordTUIReference(t *testing.T, sc transcriptScenario) tuiReference {
	var tg *tuiTarget
	sc.run(t, func(t *testing.T, shimLog string) tt.Target {
		tg = newTUITarget(t, shimLog)
		return tg
	})
	ref := tuiReference{confirms: tg.confirms}
	r, err := logs().ReadLastRun()
	switch {
	case err == nil:
		ref.lastRun = &r
	case !errors.Is(err, os.ErrNotExist):
		t.Fatal(err)
	}
	return ref
}

// confirmInfo is what a confirm prompt says past its text (which the
// goldens hold): an engine.NeedsConfirm's fields, or what the TUI's model
// had up when it asked.
type confirmInfo struct {
	Kind                          engine.ConfirmKind
	Step                          string
	Items, Direct, Cascade        []string
	Default, Destructive, Commits bool
}

// destructiveSteps are the steps whose confirm guards something archmaid
// can't bring back (NeedsConfirm.Destructive). The TUI has no such flag;
// this is the harness's own list.
var destructiveSteps = map[string]bool{
	steps.IDPaccacheWipe:  true,
	steps.IDOrphansRemove: true,
	steps.IDUserCache:     true,
	steps.IDDownloads:     true,
}

// quoted is a confirm's list as NeedsConfirm carries it: one safe line
// each, nil for none.
func quoted(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = sanitize.QuoteName(it, 200)
	}
	return out
}

// sessionConfirm is what the Session's confirm said.
func sessionConfirm(nc engine.NeedsConfirm) confirmInfo {
	return confirmInfo{Kind: nc.Kind, Step: nc.Step, Items: nc.Items, Direct: nc.Direct, Cascade: nc.Cascade,
		Default: nc.Default, Destructive: nc.Destructive, Commits: nc.Commits}
}

// compare holds what the session target saw, past the transcript, to the
// TUI's run of the same scenario: every confirm (in the order answered)
// and the last-run record, which the Session's WriteLastRun carries and
// the TUI writes itself.
func (tg *sessionTarget) compare(t *testing.T, ref tuiReference) {
	t.Helper()
	if len(tg.confirms) != len(ref.confirms) {
		t.Errorf("the session asked %d confirms, the TUI %d", len(tg.confirms), len(ref.confirms))
	}
	for i := range min(len(tg.confirms), len(ref.confirms)) {
		if got, want := tg.confirms[i], ref.confirms[i]; !reflect.DeepEqual(got, want) {
			t.Errorf("confirm %d:\nsession %+v\nTUI     %+v", i+1, got, want)
		}
	}
	norm := func(r logstore.RunRecord) logstore.RunRecord {
		r.V, r.Started, r.Finished = 0, time.Time{}, time.Time{}
		return r
	}
	switch {
	case len(tg.lastRuns) > 1:
		t.Errorf("the session wrote last-run.json %d times: %+v", len(tg.lastRuns), tg.lastRuns)
	case (len(tg.lastRuns) == 1) != (ref.lastRun != nil):
		t.Errorf("last-run.json: the session wrote %+v, the TUI left %+v", tg.lastRuns, ref.lastRun)
	case ref.lastRun != nil:
		got := tg.lastRuns[0]
		if norm(got) != norm(*ref.lastRun) {
			t.Errorf("last-run.json:\nsession %+v\nTUI     %+v", norm(got), norm(*ref.lastRun))
		}
		if got.Started.IsZero() || got.Finished.Before(got.Started) {
			t.Errorf("the session's last run went from %v to %v", got.Started, got.Finished)
		}
		// and what the effect carried is what the file got
		if r, err := logs().ReadLastRun(); err != nil || norm(r) != norm(got) {
			t.Errorf("last-run.json reads back %+v (%v), the session wrote %+v", r, err, got)
		}
	}
}

// sessionTarget runs an action on an engine.Session and carries out its
// Effects the way a Driver would, one at a time on the test goroutine.
//
// Effects that answer at once (WriteLog, WriteLastRun, RunHook, the
// prefetch) run as the Output is applied; those whose answer is an Input —
// the probe, a discovery, a step — become work, run in FIFO order by Next,
// each answer fed back through Handle. That is tuiTarget's cmd queue with
// Inputs for msgs, so the exec lines land where they do there: a work
// item's after it ran, a step child's with its first output or exit.
//
// Each Output's events are buffered in the order the TUI's transcripts
// record one Update's (see TESTING.md → Format): plan, output, finished
// and inserted steps, changed plan flags, log entries and the snapshot (read
// from disk, by effects), started steps, then a notice. A prompt is handed
// out once no work is left.
type sessionTarget struct {
	t  *testing.T
	s  *engine.Session
	fx *effects

	work    []func() []engine.Input
	pending []tt.Event
	err     error
	flags   []tt.Step // the plan as last recorded

	contract *enginetest.Checker // every Output's events, as they come
	outputs  int                 // Outputs applied so far
	confirms []confirmInfo       // each confirm as it was answered
	lastRuns []logstore.RunRecord
	ask      engine.NeedsConfirm // the open confirm, when prompt is one

	prompt   tt.Event // the open prompt as the recorder sees it, or nil
	promptID engine.PromptID
	fin      *engine.Finished

	fetches  map[string]*auditFetch
	children map[uint64]*sessionChild
	release  map[uint64]context.CancelCauseFunc // in-process steps' ctx, by Seq
}

type sessionChild struct {
	c    *proc.Child
	ch   chan proc.Msg
	quit chan struct{}
}

func newSessionTarget(t *testing.T, shimLog string) *sessionTarget {
	t.Helper()
	// As in tuiTarget: the real binary always wires askpass, so sudo steps
	// run `sudo -A`; the wrapper is only named, never run.
	oldWrapper := askpassWrapper
	askpassWrapper = "/run/user/test/archmaid-askpass/wrapper"
	t.Cleanup(func() { askpassWrapper = oldWrapper })

	// The run's flags and the TUI's actionEnv, as a front end would hand
	// them over, checked first.
	opts := engine.Options{DryRun: dryRun, Env: actionEnv()}
	if err := opts.Validate(); err != nil {
		t.Fatal(err)
	}
	tg := &sessionTarget{
		t:        t,
		s:        engine.New(opts),
		fx:       newEffects(shimLog),
		fetches:  map[string]*auditFetch{},
		children: map[uint64]*sessionChild{},
		release:  map[uint64]context.CancelCauseFunc{},
		contract: enginetest.NewChecker(t),
	}
	t.Cleanup(func() {
		for _, f := range tg.fetches {
			f.Cancel()
			<-f.Done()
		}
		for _, c := range tg.children {
			close(c.quit)
			c.c.Stop()
		}
		for _, cancel := range tg.release {
			cancel(nil)
		}
		if !t.Failed() && !t.Skipped() {
			tg.contract.Done()
		}
	})
	return tg
}

func (tg *sessionTarget) push(evs ...tt.Event) { tg.pending = append(tg.pending, evs...) }

func (tg *sessionTarget) fail(format string, a ...any) {
	if tg.err == nil {
		tg.err = fmt.Errorf(format, a...)
	}
}

// Start starts the menu action whose label contains sub.
func (tg *sessionTarget) Start(sub string) (string, error) {
	i := slices.IndexFunc(actions.Menu, func(a actions.Action) bool { return strings.Contains(a.Label, sub) })
	if i < 0 {
		return "", fmt.Errorf("no menu item %q", sub)
	}
	a := actions.Menu[i]
	o := tg.s.Start(a.ID)
	if len(o.Events) == 0 && len(o.Effects) == 0 {
		return "", fmt.Errorf("the session ignored Start(%q)", a.ID)
	}
	tg.apply(o)
	return a.Label, nil
}

// Next hands out the buffered events, running work until there are some;
// with no work left it reports the open prompt, or nil once the action is
// over.
func (tg *sessionTarget) Next() (tt.Event, error) {
	for len(tg.pending) == 0 {
		if tg.err != nil {
			return nil, tg.err
		}
		if len(tg.work) == 0 {
			switch {
			case tg.prompt != nil:
				return tg.prompt, nil
			case tg.fin != nil:
				return nil, nil
			}
			return nil, fmt.Errorf("stalled: no work, no prompt and no Finished")
		}
		w := tg.work[0]
		tg.work = tg.work[1:]
		ins := w()
		tg.push(tg.fx.execs()...)
		for _, in := range ins {
			tg.apply(tg.s.Handle(in))
			if r, ok := in.(engine.StepResult); ok {
				// The result was taken: release the step's ctx, saying so
				// (StartStep's contract).
				if cancel := tg.release[r.Seq]; cancel != nil {
					cancel(steps.ErrResultTaken)
					delete(tg.release, r.Seq)
				}
			}
		}
	}
	ev := tg.pending[0]
	tg.pending = tg.pending[1:]
	return ev, nil
}

// Reply answers the open prompt.
func (tg *sessionTarget) Reply(a tt.Answer) error {
	id := tg.promptID
	switch a.Kind {
	case "confirm":
		if _, ok := tg.prompt.(tt.NeedsConfirm); !ok {
			return fmt.Errorf("confirm reply with %T open", tg.prompt)
		}
		tg.confirms = append(tg.confirms, sessionConfirm(tg.ask))
		tg.prompt, tg.promptID = nil, 0
		tg.apply(tg.s.Confirm(id, a.Reply == "yes"))
		return nil
	case "password":
		if _, ok := tg.prompt.(tt.NeedsPassword); !ok {
			return fmt.Errorf("password reply with %T open", tg.prompt)
		}
		pw := engine.Secret("not-the-password")
		if a.Reply == "correct" {
			pw = shimPassword
		}
		tg.prompt, tg.promptID = nil, 0
		tg.apply(tg.s.SubmitPassword(id, pw))
		return nil
	}
	return fmt.Errorf("the session target can't reply %q yet", a.Kind)
}

// Outcome is the Finished event's.
func (tg *sessionTarget) Outcome() (tt.Outcome, error) {
	if tg.fin == nil {
		return tt.Outcome{}, fmt.Errorf("the action never finished")
	}
	return tt.Outcome{Kind: tg.fin.Outcome.String(), Errs: tg.fin.Errs, Skipped: tg.fin.Skipped}, nil
}

// apply carries out an Output: its Effects in order, then its Events,
// buffered in the transcript's order. The events are held to the engine's
// contract as they come, and to the TUI's order within one Update
// (tuiOrder): the transcript records one Output's events in that order,
// whatever order they came in, so an Output that broke it would pass the
// golden unseen.
func (tg *sessionTarget) apply(o engine.Output) {
	tg.outputs++
	if !tg.contract.Check(o.Events) {
		tg.fail("output %d breaks the engine's contract (see above): %s", tg.outputs, eventKinds(o.Events))
	}
	if err := tuiOrder(o.Events); err != nil {
		tg.fail("output %d: %v: %s", tg.outputs, err, eventKinds(o.Events))
	}
	launched := false
	var hooks []engine.Input
	for _, e := range o.Effects {
		switch e := e.(type) {
		case engine.StartStep:
			if tg.startStep(e) {
				launched = true
			}
		case engine.RunHook:
			hooks = append(hooks, engine.HookDone{Seq: e.Seq, Err: e.Fn()})
		default:
			tg.effect(e)
		}
	}
	// A child started here logs itself asynchronously; its exec line is
	// taken with its first output or its exit, never here.
	if !launched {
		tg.push(tg.fx.execs()...)
	}

	var plan, out, done, changed, started, tail []tt.Event
	var engineLogs []tt.Event
	for _, ev := range o.Events {
		switch ev := ev.(type) {
		case engine.QueueStarted:
			ps := make([]tt.Step, len(ev.Steps))
			for i, s := range ev.Steps {
				ps[i] = transcriptStep(s)
			}
			plan = append(plan, tt.QueueStarted{Steps: ps})
			tg.flags = slices.Clone(ps)
		case engine.OutputChunk:
			for _, l := range ev.Lines {
				out = append(out, tt.OutputLine{Index: ev.Index, Text: l})
			}
		case engine.StepDone:
			done = append(done, tt.StepDone{Index: ev.Index, Status: transcriptStatus(ev.Status)})
		case engine.StepInserted:
			done = append(done, tt.StepInserted{Index: ev.Index, Step: transcriptStep(ev.Step)})
			tg.flags = slices.Insert(tg.flags, ev.Index, transcriptStep(ev.Step))
		case engine.PlanChanged:
			// A plan line is written again only when its flags changed, as
			// tuiTarget does (a new confirm text alone isn't recorded).
			if s := transcriptStep(ev.Step); !s.SameFlags(tg.flags[ev.Index]) {
				tg.flags[ev.Index] = s
				changed = append(changed, tt.PlanChanged{Index: ev.Index, Step: s})
			}
		case engine.StepStarted:
			started = append(started, tt.StepStarted{Index: ev.Index})
		case engine.LogWritten:
			engineLogs = append(engineLogs, tt.LogWritten{Category: string(ev.Category), Header: ev.Header})
		case engine.Notice:
			tail = append(tail, tt.Notice{Text: ev.Text})
		case engine.NeedsConfirm:
			tg.open(ev.ID, tt.NeedsConfirm{Text: ev.Msg})
			tg.ask = ev
		case engine.PromptClosed:
			// The engine closed it (an askpass request withdrawn): it is no
			// longer there to answer.
			if ev.ID == tg.promptID {
				tg.prompt, tg.promptID = nil, 0
			}
		case engine.NeedsPassword:
			tg.open(ev.ID, tt.NeedsPassword{})
		case engine.Finished:
			tg.fin = &ev
		case engine.PhaseChanged, engine.StatusUpdated, engine.AuthWaiting, engine.AuthResolved,
			engine.FaillockState: // no transcript line
		default:
			tg.fail("event %T (%+v): teach the session target", ev, ev)
		}
	}
	tg.push(plan...)
	tg.push(out...)
	tg.push(done...)
	tg.push(changed...)
	logs := tg.fx.logs()
	if !contains(logs, engineLogs) {
		tg.fail("the session reported log entries %v, the log files got %v", engineLogs, logs)
	}
	tg.push(logs...)
	tg.push(tg.fx.snapshot()...)
	tg.push(started...)
	tg.push(tail...)
	for _, h := range hooks {
		tg.apply(tg.s.Handle(h))
	}
}

// tuiRank is where an event of one Output lands in its transcript (apply's
// buckets: tuiTarget.observe's order within one Update), or -1 for an
// event with no transcript line.
func tuiRank(ev engine.Event) int {
	switch ev.(type) {
	case engine.QueueStarted:
		return 0
	case engine.OutputChunk:
		return 1
	case engine.StepDone, engine.StepInserted:
		return 2
	case engine.PlanChanged:
		return 3
	case engine.LogWritten:
		return 4
	case engine.StepStarted:
		return 5
	case engine.Notice:
		return 6
	case engine.NeedsConfirm, engine.NeedsPassword, engine.NeedsSelection, engine.OutputReport, engine.Finished:
		return 7 // what the action now waits on, or its end
	}
	return -1
}

// tuiOrder reports an event that comes after one its transcript records
// behind it. One exception: a StepDone after a LogWritten, a further step
// settled in the same Output (steps skipped behind a refused password).
// The TUI lists such steps' lines first and their log entries after, which
// says nothing about the order they happened in; the engine settles and
// logs them one at a time, which doc.go allows.
func tuiOrder(evs []engine.Event) error {
	top, at := -1, -1
	for i, ev := range evs {
		r := tuiRank(ev)
		if r < 0 {
			continue
		}
		_, done := ev.(engine.StepDone)
		_, logged := evs[max(at, 0)].(engine.LogWritten)
		if r < top && !(done && logged) {
			return fmt.Errorf("event %d (%T) comes after %T, out of the TUI's order", i, ev, evs[at])
		}
		top, at = r, i
	}
	return nil
}

// eventKinds lists evs' types, for a failure message.
func eventKinds(evs []engine.Event) string {
	kinds := make([]string, len(evs))
	for i, ev := range evs {
		kinds[i] = strings.TrimPrefix(fmt.Sprintf("%T", ev), "engine.")
	}
	return strings.Join(kinds, " ")
}

// contains reports whether, in each log category, sub's entries are a
// subsequence of evs' (the log files may also hold entries the Session
// didn't write itself, such as the audit commit's ACCEPT; and the harness
// diffs the files one after another, so entries written in one go come
// ordered by file, not as the Session wrote them).
func contains(evs, sub []tt.Event) bool {
	byCat := func(evs []tt.Event) map[string][]tt.Event {
		m := map[string][]tt.Event{}
		for _, ev := range evs {
			if l, ok := ev.(tt.LogWritten); ok {
				m[l.Category] = append(m[l.Category], ev)
			}
		}
		return m
	}
	have := byCat(evs)
	for cat, want := range byCat(sub) {
		i := 0
		for _, ev := range have[cat] {
			if i < len(want) && ev == want[i] {
				i++
			}
		}
		if i != len(want) {
			return false
		}
	}
	return true
}

func (tg *sessionTarget) open(id engine.PromptID, ev tt.Event) {
	if tg.prompt != nil {
		tg.fail("prompt %v opened while %v is open", ev, tg.prompt)
	}
	tg.prompt, tg.promptID = ev, id
}

// effect carries out one Effect other than StartStep and RunHook.
func (tg *sessionTarget) effect(e engine.Effect) {
	switch e := e.(type) {
	case engine.Probe:
		priv := privileged()
		tg.work = append(tg.work, func() []engine.Input {
			return []engine.Input{engine.ProbeDone{Cached: priv.Probe(context.Background())}}
		})
	case engine.Authenticate:
		priv := privileged()
		tg.work = append(tg.work, func() []engine.Input {
			detail, err := priv.Authenticate(context.Background(), string(e.Password))
			return []engine.Input{engine.AuthDone{OK: err == nil, Detail: detail, Err: err}}
		})
	case engine.ReadFaillock:
		// Never the real tally: the shimmed su keeps no count.
		tg.work = append(tg.work, func() []engine.Input {
			return []engine.Input{engine.FaillockRead{Deny: 3}}
		})
	case engine.Discover:
		tg.work = append(tg.work, func() []engine.Input {
			return []engine.Input{engine.Discovered{Seq: e.Seq, Discovery: e.Run(context.Background(), e.Env)}}
		})
	case engine.WriteLog:
		_ = logs().Append(e.Category, e.Kind, e.Label, e.Body)
	case engine.WriteLastRun:
		tg.lastRuns = append(tg.lastRuns, e.Record)
		_ = logs().WriteLastRun(e.Record)
	case engine.KeepAlive: // the refresh is a tick; the transcripts drop ticks
	case engine.StartPrefetch:
		if e.Name != steps.PrefetchAURAudit {
			tg.fail("unknown prefetch %q", e.Name)
			return
		}
		// Let it finish here, so its pacman -Qqm lands with the action's
		// own queries, as tuiTarget does.
		f := startAuditFetch()
		tg.fetches[e.Name] = f
		select {
		case <-f.Done():
		case <-time.After(5 * time.Second):
			tg.fail("the prefetched AUR audit never finished")
		}
	case engine.CancelPrefetch:
		if f := tg.fetches[e.Name]; f != nil {
			f.Cancel()
			delete(tg.fetches, e.Name)
		}
	default:
		tg.fail("effect %T: teach the session target", e)
	}
}

// startStep starts a step: an in-process one becomes work (awaiting its
// prefetch, if one runs), a child is started now and pumped by work. It
// reports whether it started a child.
func (tg *sessionTarget) startStep(e engine.StartStep) bool {
	if run := e.Step.Run; run != nil {
		ctx, cancel := context.WithCancelCause(context.Background())
		tg.release[e.Seq] = cancel
		if f := tg.fetches[e.Step.Prefetch]; f != nil {
			run = func(ctx context.Context, _ steps.Env) steps.Result { return f.run(ctx) }
		}
		tg.work = append(tg.work, func() []engine.Input {
			return []engine.Input{engine.StepResult{Seq: e.Seq, Result: run(ctx, e.Env)}}
		})
		return false
	}
	c := &sessionChild{ch: make(chan proc.Msg, proc.BatchMax), quit: make(chan struct{})}
	c.c = proc.New(procOptions()).Start(stepSpec(e.Step), c.ch, c.quit)
	tg.children[e.Seq] = c
	tg.work = append(tg.work, tg.pump(e.Seq, c))
	return true
}

// pump is one proc.Next on a child's stream, as StepOutput (sanitized, as a
// Driver must) and, at its end, StepExited; it queues itself again until
// the child is done.
func (tg *sessionTarget) pump(seq uint64, c *sessionChild) func() []engine.Input {
	return func() []engine.Input {
		b, ok := proc.Next(c.ch, c.quit, proc.BatchMax)
		if !ok {
			return nil
		}
		var ins []engine.Input
		if len(b.Lines) > 0 {
			lines := make([]string, len(b.Lines))
			for i, l := range b.Lines {
				lines[i] = sanitize.String(l, sanitize.KeepSGR)
			}
			ins = append(ins, engine.StepOutput{Seq: seq, Lines: lines})
		}
		if b.Done {
			delete(tg.children, seq)
			return append(ins, engine.StepExited{Seq: seq, Err: b.Err})
		}
		tg.work = append(tg.work, tg.pump(seq, c))
		return ins
	}
}

// transcriptStep is what a transcript records of a planned step.
func transcriptStep(p engine.PlanStep) tt.Step {
	s := tt.Step{Title: p.Title, InProc: p.InProc, Sudo: p.NeedsPriv, Confirm: p.Confirm,
		Optional: p.Optional, AlwaysLog: p.AlwaysLog, Category: string(p.Category)}
	if !p.InProc {
		s.Argv = slices.Clone(p.Argv)
	}
	return s
}

// transcriptStatus is how a transcript draws a settled step: every "didn't
// run" is a skip, as in the TUI's checklist.
func transcriptStatus(s steps.Status) tt.Status {
	switch s {
	case steps.OK:
		return tt.OK
	case steps.Failed:
		return tt.Failed
	}
	return tt.Skipped
}

// A real downloads delete through the Session (in the scratch HOME) is
// logged once, as done, with no ABORT entry from its own abort bookkeeping:
// the Session runs the result's Commit (which disarms it), and the target
// then releases the step's ctx with steps.ErrResultTaken, as StartStep's
// contract asks (which keeps it quiet even where no Commit runs).
func TestSessionDownloadsRealRunLogsNoAbort(t *testing.T) {
	transcriptEnv(t, false)
	dl := downloadsDir(t)
	writeFile(t, filepath.Join(dl, "old.iso"), "x")
	cutoffAt(t, ctimeMark(t))
	shimLog := installShims(t, nil)
	mk := func(t *testing.T, shimLog string) tt.Target { return newSessionTarget(t, shimLog) }
	got := record(t, mk, shimLog, "Delete old downloads", Answer{"confirm", "older than 30 days", "yes"}).Transcript()
	waitForDownloads(io.Discard)
	if exists(filepath.Join(dl, "old.iso")) {
		t.Fatalf("old.iso survived:\n%s", got)
	}
	log, err := logs().Read(logCategoryCleanup)
	if err != nil || !strings.Contains(log, "OK: Old downloads deleted") || strings.Contains(log, "ABORT") {
		t.Fatalf("cleanup.log = %q (%v), want the OK entry and no ABORT\n%s", log, err, got)
	}
}

// L7: an in-process step's output with an escape sequence spanning a
// newline. The TUI sanitizes Result.Output whole and then splits it
// (handleStepResult), so the OSC below swallows the newline and the "y"
// in it; the Session splits first and sanitizes each line, so it shows
// three lines, the "y" among them. Both are safe; they differ.
func TestSessionInProcOutputLikeTUI(t *testing.T) {
	t.Skip("fails until Dev 1's L7 fix: the engine's stepResult (queue.go) must sanitize Result.Output whole, " +
		"then split it, as the TUI does (today the Session shows a, yb, c where the TUI shows ab, c)")
	const out = "a\x1b]0;x\ny\x07b\nc"
	probe := actions.Action{ID: "l7probe", Label: "Escape across a newline", Steps: func(actions.Env) []steps.Step {
		return []steps.Step{{ID: "l7probe", Title: "Printing…", LogLabel: "Printed", AlwaysLog: true,
			Run: func(context.Context, steps.Env) steps.Result { return steps.Result{Output: out} }}}
	}}
	old := actions.Menu
	actions.Menu = append(slices.Clone(old), probe)
	t.Cleanup(func() { actions.Menu = old })
	got := map[string]string{}
	for name, mk := range map[string]newTarget{
		"tui":     func(t *testing.T, shimLog string) tt.Target { return newTUITarget(t, shimLog) },
		"session": func(t *testing.T, shimLog string) tt.Target { return newSessionTarget(t, shimLog) },
	} {
		t.Run(name, func(t *testing.T) {
			transcriptEnv(t, false)
			got[name] = record(t, mk, installShims(t, nil), probe.Label).Transcript()
		})
	}
	if got["tui"] != got["session"] {
		t.Errorf("the Session shows the step's output unlike the TUI\nTUI:\n%s\nsession:\n%s", got["tui"], got["session"])
	}
}
