package engine_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// The Session's own tests play the Driver by hand: they read the Effects of
// each Output and answer with the Inputs a Driver would send. Nothing runs.
// Every run is also held to the ordering contract (enginetest.CheckContract).
// The tests are outside the package so they can use enginetest; the menu
// seam and the unexported bits they need are in export_test.go.

var t0 = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// harness is a Session under test with a fake clock and its own menu.
type harness struct {
	t   *testing.T
	s   *Session
	now time.Time
	evs []Event // every event so far, in order
	out Output  // the last call's
}

func newHarness(t *testing.T, opts Options, menu ...actions.Action) *harness {
	t.Helper()
	h := &harness{t: t, now: t0}
	opts.Now = func() time.Time { return h.now }
	h.s = New(opts)
	SetMenu(h.s, menu)
	return h
}

func (h *harness) wait(d time.Duration) { h.now = h.now.Add(d) }

func (h *harness) do(o Output) Output {
	h.out = o
	h.evs = append(h.evs, o.Events...)
	return o
}

func (h *harness) start(id string) Output    { return h.do(h.s.Start(id)) }
func (h *harness) handle(in Input) Output    { return h.do(h.s.Handle(in)) }
func (h *harness) cancel(id PromptID) Output { return h.do(h.s.CancelPrompt(id)) }

// confirm answers the confirm the last call opened.
func (h *harness) confirm(yes bool) Output {
	h.t.Helper()
	return h.do(h.s.Confirm(event[NeedsConfirm](h.t, h.out).ID, yes))
}

// seq is the Seq of the step the last call started.
func (h *harness) seq() uint64 {
	h.t.Helper()
	return effect[StartStep](h.t, h.out).Seq
}

// exit finishes the running child with err; ok finishes it cleanly.
func (h *harness) exit(err error) Output { return h.handle(StepExited{Seq: h.seq(), Err: err}) }
func (h *harness) ok() Output            { return h.exit(nil) }

// result finishes the running in-process step with r.
func (h *harness) result(r steps.Result) Output { return h.handle(StepResult{Seq: h.seq(), Result: r}) }

// find returns the first T in xs and whether there is one.
func find[T any, X any](xs []X) (T, bool) {
	for _, x := range xs {
		if v, ok := any(x).(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

func event[T Event](t *testing.T, o Output) T {
	t.Helper()
	v, ok := find[T](o.Events)
	if !ok {
		var zero T
		t.Fatalf("no %T among the events %#v", zero, o.Events)
	}
	return v
}

func effect[T Effect](t *testing.T, o Output) T {
	t.Helper()
	v, ok := find[T](o.Effects)
	if !ok {
		var zero T
		t.Fatalf("no %T among the effects %#v", zero, o.Effects)
	}
	return v
}

func all[T any, X any](xs []X) []T {
	var out []T
	for _, x := range xs {
		if v, ok := any(x).(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func noOutput(t *testing.T, what string, o Output) {
	t.Helper()
	if len(o.Events) != 0 || len(o.Effects) != 0 {
		t.Errorf("%s: want an empty Output, got %#v", what, o)
	}
}

// ── steps and actions for the tests ──────────────────────────────────────────

func child(id string, argv ...string) steps.Step {
	return steps.Step{ID: id, Title: id + "…", LogLabel: id + " done", Argv: argv}
}

func inproc(id string) steps.Step {
	return steps.Step{ID: id, Title: id + "…", LogLabel: id + " done",
		Run: func(context.Context, steps.Env) steps.Result { panic("the Session never runs a step") }}
}

func sudo(s steps.Step) steps.Step { s.NeedsPriv = true; return s }

func queue(id string, ss ...steps.Step) actions.Action {
	return actions.Action{ID: id, Label: id, Steps: func(actions.Env) []steps.Step { return ss }}
}

func ids(ps []PlanStep) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestOptionsValidate(t *testing.T) {
	err := Options{}.Validate()
	if err == nil || !strings.Contains(err.Error(), "AuditStep") || !strings.Contains(err.Error(), "DeleteDownloads") {
		t.Errorf("empty Options: %v, want actions.Env's missing fields", err)
	}
	env := actions.Env{
		AuditStep:   func() steps.Step { return steps.Step{} },
		AuditReport: func(context.Context) actions.Discovery { return nil },
		Downloads:   func() downloads.Options { return downloads.Options{} },
		DeleteDownloads: func(downloads.Plan) func(context.Context, steps.Env) steps.Result {
			return nil
		},
	}
	if err := (Options{Env: env, Auth: AuthPolkit}).Validate(); err != nil {
		t.Errorf("complete Options: %v", err)
	}
	if err := (Options{Env: env, Auth: AuthPolkit + 1}).Validate(); err == nil || !strings.Contains(err.Error(), "AuthFlow") {
		t.Errorf("unknown AuthFlow: %v", err)
	}
}

func TestSessionIgnoresWhatItCantStart(t *testing.T) {
	h := newHarness(t, Options{}, queue("q", child("a", "true")), actions.Action{ID: "viewlog"})
	noOutput(t, "unknown action", h.start("nope"))
	noOutput(t, "a front end's own entry", h.start("viewlog"))
	noOutput(t, "Confirm while idle", h.do(h.s.Confirm(1, true)))
	noOutput(t, "CancelPrompt while idle", h.cancel(1))
	noOutput(t, "Abort while idle", h.do(h.s.Abort()))
	noOutput(t, "late output while idle", h.handle(StepOutput{Seq: 1, Lines: []string{"x"}}))
	h.start("q")
	noOutput(t, "Start while an action runs", h.do(h.s.Start("q")))
}

func TestSessionRunsAQueue(t *testing.T) {
	a := child("a", "echo", "hi")
	b := inproc("b")
	c := child("c", "false")
	c.Category, c.AlwaysLog = logstore.Topgrade, true
	h := newHarness(t, Options{}, queue("q", a, b, c))

	o := h.start("q")
	qs := event[QueueStarted](t, o)
	want := []PlanStep{
		{ID: "a", Title: "a…", Argv: []string{"echo", "hi"}, Category: logstore.Cleanup},
		{ID: "b", Title: "b…", InProc: true, Category: logstore.Cleanup},
		{ID: "c", Title: "c…", Argv: []string{"false"}, AlwaysLog: true, Category: logstore.Topgrade},
	}
	if qs.Action != "q" || !reflect.DeepEqual(qs.Steps, want) || !qs.Started.Equal(t0) || qs.DryRun {
		t.Fatalf("QueueStarted = %+v", qs)
	}
	if got := event[PhaseChanged](t, o); got.Phase != PhaseRunning {
		t.Errorf("phase = %v, want running", got.Phase)
	}
	if got := event[StepStarted](t, o); got != (StepStarted{ID: "a", Index: 0, At: t0}) {
		t.Errorf("StepStarted = %+v", got)
	}
	ss := effect[StartStep](t, o)
	if ss.Seq == 0 || ss.Step.ID != "a" || ss.Env.DryRun {
		t.Errorf("StartStep = %+v", ss)
	}

	h.wait(time.Second)
	o = h.handle(StepOutput{Seq: ss.Seq, Lines: []string{"hi", "\x1b]52;c;aGk=\a\x1b[1mbold\x1b[0m"}})
	if got := event[OutputChunk](t, o); !slices.Equal(got.Lines, []string{"hi", "\x1b[1mbold\x1b[0m"}) || got.ID != "a" || got.Dropped != 0 {
		t.Errorf("OutputChunk = %+v (want the OSC 52 write stripped, SGR kept)", got)
	}
	h.out = Output{Effects: []Effect{ss}}
	o = h.ok()
	if got := event[StepDone](t, o); got != (StepDone{ID: "a", Index: 0, Status: steps.OK, Elapsed: time.Second}) {
		t.Errorf("StepDone = %+v", got)
	}
	if _, ok := find[WriteLog](o.Effects); ok {
		t.Error("an ordinary step that succeeded was logged")
	}
	if got := event[StepStarted](t, o); got.ID != "b" || got.Index != 1 {
		t.Errorf("next StepStarted = %+v", got)
	}

	o = h.result(steps.Result{Output: "x\ny\n"})
	if got := event[OutputChunk](t, o); !slices.Equal(got.Lines, []string{"x", "y"}) || got.ID != "b" {
		t.Errorf("in-process output = %+v", got)
	}
	event[StepStarted](t, o)

	h.wait(2 * time.Second)
	h.handle(StepOutput{Seq: h.seq(), Lines: []string{"partial"}})
	h.out = Output{Effects: []Effect{StartStep{Seq: ss.Seq + 2}}} // c's seq, as above
	o = h.exit(errors.New("exit status 1\nand more"))
	if got := event[StepDone](t, o); got.Status != steps.Failed || got.Err != "exit status 1 and more" || got.Elapsed != 2*time.Second {
		t.Errorf("failed StepDone = %+v", got)
	}
	if got := effect[WriteLog](t, o); got != (WriteLog{Category: logstore.Topgrade, Kind: "FAIL", Label: "c done", Body: "partial\n"}) {
		t.Errorf("WriteLog = %+v", got)
	}
	if got := event[LogWritten](t, o); got != (LogWritten{Category: logstore.Topgrade, Header: "FAIL: c done"}) {
		t.Errorf("LogWritten = %+v", got)
	}
	fin := event[Finished](t, o)
	if fin != (Finished{Action: "q", Outcome: OutcomeCompleted, Errs: 1, Elapsed: 3 * time.Second}) {
		t.Errorf("Finished = %+v", fin)
	}
	rec := effect[WriteLastRun](t, o).Record
	if rec.Action != "q" || rec.Outcome != logstore.OutcomeFailed || rec.StepsFailed != 1 ||
		!rec.Started.Equal(t0) || !rec.Finished.Equal(t0.Add(3*time.Second)) {
		t.Errorf("last run = %+v", rec)
	}
	if last := h.evs[len(h.evs)-1]; last != (PhaseChanged{Phase: PhaseIdle}) {
		t.Errorf("last event %#v, want PhaseChanged idle", last)
	}
	enginetest.CheckContract(t, h.evs)

	// Idle again: the next action starts.
	if o := h.start("q"); len(o.Events) == 0 {
		t.Error("a second Start after Finished was ignored")
	}
}

func TestSessionIgnoresStaleInputs(t *testing.T) {
	h := newHarness(t, Options{}, queue("q", child("a", "true"), inproc("b")))
	h.start("q")
	first := h.seq()
	h.ok()
	for _, in := range []Input{
		StepOutput{Seq: first, Lines: []string{"late"}},
		StepExited{Seq: first},
		StepResult{Seq: first, Result: steps.Result{Output: "late"}},
		StepResult{Seq: 0},
		StepOutput{Seq: h.seq() + 100, Lines: []string{"x"}},
		Discovered{Seq: first, Discovery: actions.Notice{Text: "x"}},
		ProbeDone{Cached: true},
		HookDone{Seq: first},
		StepOutput{Seq: h.seq()}, // no lines
	} {
		noOutput(t, reflectName(in), h.s.Handle(in))
	}
}

func reflectName(v any) string { return reflect.TypeOf(v).Name() }

func TestSessionLogsByPolicy(t *testing.T) {
	for _, c := range []struct {
		name      string
		dry, al   bool
		err       error
		wantKind  string // "" for no entry
		wantOKRec string
	}{
		{"ordinary ok", false, false, nil, "", logstore.OutcomeOK},
		{"alwayslog ok", false, true, nil, "OK", logstore.OutcomeOK},
		{"failure", false, false, errors.New("x"), "FAIL", logstore.OutcomeFailed},
		{"dry run", true, false, nil, "DRYRUN", logstore.OutcomeOK},
		{"dry run failure", true, true, errors.New("x"), "FAIL", logstore.OutcomeFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := child("a", "x")
			s.AlwaysLog = c.al
			h := newHarness(t, Options{DryRun: c.dry}, queue("q", s))
			if event[QueueStarted](t, h.start("q")).DryRun != c.dry {
				t.Error("QueueStarted.DryRun is not the run's")
			}
			if effect[StartStep](t, h.out).Env.DryRun != c.dry {
				t.Error("StartStep.Env.DryRun is not the run's")
			}
			h.handle(StepOutput{Seq: h.seq(), Lines: []string{"one", "two"}})
			h.out = Output{Effects: []Effect{StartStep{Seq: 1}}}
			o := h.exit(c.err)
			logs := all[WriteLog](o.Effects)
			if c.wantKind == "" {
				if len(logs) != 0 {
					t.Errorf("logged %+v", logs)
				}
			} else if len(logs) != 1 || logs[0].Kind != c.wantKind || logs[0].Body != "one\ntwo\n" || logs[0].Label != "a done" {
				t.Errorf("logs = %+v, want one %s entry with the output", logs, c.wantKind)
			}
			if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != c.wantOKRec || rec.DryRun != c.dry {
				t.Errorf("record = %+v", rec)
			}
		})
	}
}

func TestSessionStepConfirms(t *testing.T) {
	x := child("x", "rm", "-rf", "cache")
	x.Confirm, x.Optional, x.AlwaysLog = "Clear it?\n\n  a\n  b", true, true
	y := child("y", "true")
	y.Confirm = "Go on?"
	z := child("z", "true")
	h := newHarness(t, Options{}, queue("q", x, y, z))

	o := h.start("q")
	nc := event[NeedsConfirm](t, o)
	if nc.ID == 0 || nc.Kind != ConfirmStep || nc.Step != "x" || nc.Msg != x.Confirm ||
		nc.Title != "Clear it?" || nc.Body != "  a\n  b" || nc.Default {
		t.Errorf("NeedsConfirm = %+v", nc)
	}
	if _, ok := find[StepStarted](o.Events); ok {
		t.Error("a step with a confirm started before it was answered")
	}
	noOutput(t, "a confirm for another ID", h.s.Confirm(nc.ID+1, true))
	noOutput(t, "a cancel for another ID", h.s.CancelPrompt(nc.ID+1))
	noOutput(t, "a selection", h.s.Select(nc.ID, nil))
	noOutput(t, "a password", h.s.SubmitPassword(nc.ID, "pw"))

	// "no" on the optional one skips just it, and logs that (AlwaysLog).
	o = h.confirm(false)
	if got := event[StepDone](t, o); got != (StepDone{ID: "x", Index: 0, Status: steps.Skipped}) {
		t.Errorf("declined optional = %+v", got)
	}
	if got := event[LogWritten](t, o); got.Header != "SKIP: x done (declined)" {
		t.Errorf("decline log = %+v", got)
	}
	noOutput(t, "the answered prompt again", h.s.Confirm(nc.ID, true))

	// Cancelling the required one is "no": it and everything after it are
	// skipped and the queue is aborted.
	o = h.cancel(event[NeedsConfirm](t, o).ID)
	done := all[StepDone](o.Events)
	if len(done) != 2 || done[0].ID != "y" || done[1].ID != "z" || done[1].Status != steps.Skipped {
		t.Errorf("declined required: StepDone %+v", done)
	}
	if fin := event[Finished](t, o); fin.Outcome != OutcomeAborted || fin.Skipped != 3 {
		t.Errorf("Finished = %+v", fin)
	}
	// Nothing ran: the record says cancelled, as the TUI's does.
	if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeCancelled || rec.StepsSkipped != 3 {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

func TestSessionDeclinedRequiredAfterAStepRan(t *testing.T) {
	y := child("y", "true")
	y.Confirm = "Go on?"
	h := newHarness(t, Options{}, queue("q", child("a", "true"), y, child("z", "true")))
	h.start("q")
	o := h.ok()
	o = h.confirm(false)
	if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeAborted || rec.StepsSkipped != 2 {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

func TestSessionConfirmedStepRunsOnAcceptFirst(t *testing.T) {
	ran := false
	x := child("x", "true")
	x.Confirm, x.OnAccept = "Sure?", func() error { ran = true; return nil }
	h := newHarness(t, Options{}, queue("q", x))
	h.start("q")
	o := h.confirm(true)
	if len(o.Effects) != 2 {
		t.Fatalf("effects = %#v, want RunHook then StartStep", o.Effects)
	}
	hook, ok := o.Effects[0].(RunHook)
	if !ok || hook.Step != "x" || hook.Seq == 0 {
		t.Fatalf("first effect = %#v", o.Effects[0])
	}
	if _, ok := o.Effects[1].(StartStep); !ok {
		t.Fatalf("second effect = %#v", o.Effects[1])
	}
	hook.Fn()
	if !ran {
		t.Error("RunHook's Fn isn't the step's OnAccept")
	}
	noOutput(t, "HookDone", h.s.Handle(HookDone{Seq: hook.Seq}))
}

func TestSessionProbeGateAndKeepAlive(t *testing.T) {
	pre := inproc("audit")
	pre.Prefetch = "aur"
	gated := func() actions.Action {
		a := queue("q", pre, sudo(child("p", "sudo", "x")))
		a.Gate = "Proceed?"
		return a
	}

	t.Run("cached, gate accepted", func(t *testing.T) {
		h := newHarness(t, Options{}, gated())
		o := h.start("q")
		if !reflect.DeepEqual(o.Effects, []Effect{StartPrefetch{Name: "aur"}, Probe{}}) {
			t.Fatalf("effects = %#v", o.Effects)
		}
		if got := event[PhaseChanged](t, o); got.Phase != PhaseProbing {
			t.Errorf("phase = %+v", got)
		}
		o = h.handle(ProbeDone{Cached: true})
		nc := event[NeedsConfirm](t, o)
		if nc.Kind != ConfirmGate || nc.Step != "" || nc.Msg != "Proceed?" || nc.Title != "Proceed?" {
			t.Errorf("gate = %+v", nc)
		}
		noOutput(t, "a second probe answer", h.s.Handle(ProbeDone{Cached: true}))
		o = h.confirm(true)
		event[QueueStarted](t, o)
		if !slices.Contains(o.Effects, Effect(KeepAlive{On: true})) {
			t.Errorf("no keep-alive for a sudo queue: %#v", o.Effects)
		}
		// The audit's result consumes its prefetch.
		o = h.result(steps.Result{Output: "ok"})
		if !slices.Contains(o.Effects, Effect(CancelPrefetch{Name: "aur"})) {
			t.Errorf("the consumed prefetch wasn't dropped: %#v", o.Effects)
		}
		o = h.ok()
		if !slices.Contains(o.Effects, Effect(KeepAlive{On: false})) {
			t.Errorf("keep-alive left on: %#v", o.Effects)
		}
		if n := len(all[CancelPrefetch](o.Effects)); n != 0 {
			t.Errorf("prefetch dropped twice")
		}
		enginetest.CheckContract(t, h.evs)
	})

	t.Run("gate declined", func(t *testing.T) {
		h := newHarness(t, Options{}, gated())
		h.start("q")
		h.handle(ProbeDone{Cached: true})
		o := h.confirm(false)
		if _, ok := find[QueueStarted](h.evs); ok {
			t.Error("a declined gate planned the queue")
		}
		if fin := event[Finished](t, o); fin.Outcome != OutcomeCancelled {
			t.Errorf("Finished = %+v", fin)
		}
		if !slices.Contains(o.Effects, Effect(CancelPrefetch{Name: "aur"})) {
			t.Errorf("prefetch not dropped: %#v", o.Effects)
		}
		if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeCancelled {
			t.Errorf("record = %+v", rec)
		}
		enginetest.CheckContract(t, h.evs)
	})

	t.Run("uncached", func(t *testing.T) {
		h := newHarness(t, Options{}, gated())
		h.start("q")
		h.handle(ProbeDone{Cached: false})
		o := h.handle(FaillockRead{Deny: 3})
		np := event[NeedsPassword](t, o)
		if np.ID == 0 || np.Kind != PasswordUpFront || np.AttemptsLeft != 3 || np.Error != "" {
			t.Errorf("NeedsPassword = %+v", np)
		}
		if _, ok := find[NeedsConfirm](o.Events); ok {
			t.Error("the gate was asked on top of the password prompt")
		}
		noOutput(t, "Confirm on a password prompt", h.s.Confirm(np.ID, true))
		o = h.cancel(np.ID)
		if fin := event[Finished](t, o); fin.Outcome != OutcomeCancelled {
			t.Errorf("Finished = %+v", fin)
		}
		if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeCancelled {
			t.Errorf("record = %+v", rec)
		}
		enginetest.CheckContract(t, h.evs)
	})

	for _, c := range []struct {
		name string
		opts Options
	}{{"dry run", Options{DryRun: true}}, {"polkit", Options{Auth: AuthPolkit}}} {
		t.Run(c.name+" asks no probe", func(t *testing.T) {
			h := newHarness(t, c.opts, gated())
			o := h.start("q")
			if _, ok := find[Probe](o.Effects); ok {
				t.Error("probed")
			}
			event[NeedsConfirm](t, o) // the gate still stands
			o = h.confirm(true)
			if _, ok := find[KeepAlive](o.Effects); ok {
				t.Error("keep-alive armed")
			}
		})
	}

	t.Run("no root: the gate still stands", func(t *testing.T) {
		a := queue("q", child("a", "true"))
		a.Gate = "Proceed?"
		h := newHarness(t, Options{}, a)
		o := h.start("q")
		if _, ok := find[Probe](o.Effects); ok {
			t.Error("probed a queue that needs no root")
		}
		if nc := event[NeedsConfirm](t, o); nc.Kind != ConfirmGate {
			t.Errorf("gate = %+v", nc)
		}
		if _, ok := find[QueueStarted](o.Events); ok {
			t.Error("planned before the gate was answered")
		}
		event[QueueStarted](t, h.confirm(true))
	})
}

func TestSessionNotGrantedIsAuthRefused(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", sudo(child("p", "x")), child("b", "true")))
	h.start("q")
	o := h.handle(StepExited{Seq: h.seq(), Err: errors.New("exit status 127"), NotGranted: true})
	if got := event[StepDone](t, o); got.Status != steps.AuthRefused || got.Err != "" {
		t.Errorf("StepDone = %+v", got)
	}
	if got := event[LogWritten](t, o); got.Header != "SKIP: p done (privilege not granted)" {
		t.Errorf("log = %+v", got)
	}
	o = h.ok()
	if fin := event[Finished](t, o); fin.Skipped != 1 || fin.Refused != 1 || fin.Errs != 0 || fin.Outcome != OutcomeCompleted {
		t.Errorf("Finished = %+v", fin)
	}
	// Completed, but not a clean run: the record says incomplete.
	if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeIncomplete || rec.StepsRefused != 1 || rec.StepsSkipped != 1 {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

func TestSessionDiscovery(t *testing.T) {
	disc := func(context.Context, actions.Env) actions.Discovery { panic("the Session never discovers") }
	a := actions.Action{ID: "d", Busy: "Looking…", Discover: disc}
	envSeen := actions.Env{RepairWorkers: 3}

	t.Run("notice", func(t *testing.T) {
		h := newHarness(t, Options{DryRun: true, Env: envSeen}, a)
		o := h.start("d")
		if got := event[PhaseChanged](t, o); got != (PhaseChanged{Phase: PhaseDiscovering, Busy: "Looking…"}) {
			t.Errorf("phase = %+v", got)
		}
		d := effect[Discover](t, o)
		if d.Seq == 0 || d.Action != "d" || d.Run == nil || !d.Env.DryRun || d.Env.RepairWorkers != 3 {
			t.Errorf("Discover = %+v (Env must be Options.Env with DryRun copied in)", d)
		}
		noOutput(t, "a stale discovery", h.s.Handle(Discovered{Seq: d.Seq + 1, Discovery: actions.Notice{Text: "x"}}))
		o = h.handle(Discovered{Seq: d.Seq, Discovery: actions.Notice{Text: "Nothing \x1b[31mhere\x1b[0m."}})
		if got := event[Notice](t, o); got.Text != "Nothing here." {
			t.Errorf("Notice = %q, want it plain", got.Text)
		}
		if fin := event[Finished](t, o); fin.Outcome != OutcomeNotice {
			t.Errorf("Finished = %+v", fin)
		}
		if _, ok := find[WriteLastRun](o.Effects); ok {
			t.Error("a notice wrote a last-run record (the TUI's doesn't)")
		}
		enginetest.CheckContract(t, h.evs)
	})

	t.Run("pending", func(t *testing.T) {
		h := newHarness(t, Options{}, a)
		d := effect[Discover](t, h.start("d"))
		o := h.handle(Discovered{Seq: d.Seq, Discovery: actions.Pending{Steps: []steps.Step{child("a", "x")}}})
		if got := event[QueueStarted](t, o); !slices.Equal(ids(got.Steps), []string{"a"}) {
			t.Errorf("QueueStarted = %+v", got)
		}
		o = h.ok()
		if rec := effect[WriteLastRun](t, o).Record; rec.Action != "d" || rec.Outcome != logstore.OutcomeOK {
			t.Errorf("record = %+v", rec)
		}
		enginetest.CheckContract(t, h.evs)
	})
}

func TestSessionTick(t *testing.T) {
	st := sysstat.Status{Free: "1G"}
	h := newHarness(t, Options{}, queue("q", child("a", "true")))
	if got := event[StatusUpdated](t, h.handle(Tick{Status: st})); got != (StatusUpdated{Now: t0, Status: st}) {
		t.Errorf("idle tick = %+v", got)
	}
	h.wait(time.Second)
	h.start("q")
	h.wait(2 * time.Second)
	want := StatusUpdated{Now: t0.Add(3 * time.Second), Status: st, Elapsed: 2 * time.Second, StepElapsed: 2 * time.Second, Step: "a"}
	if got := event[StatusUpdated](t, h.s.Handle(Tick{Status: st})); got != want {
		t.Errorf("running tick = %+v, want %+v", got, want)
	}
}

func TestSessionHangsUpOnAskpass(t *testing.T) {
	h := newHarness(t, Options{})
	o := h.handle(AskpassRequest{Req: 7, Prompt: "[sudo] password:"})
	if !reflect.DeepEqual(o.Effects, []Effect{HangUpAskpass{Req: 7}}) || len(o.Events) != 0 {
		t.Errorf("askpass request: %#v", o)
	}
}

func TestSessionKeepsHyprpmLast(t *testing.T) {
	hy := child(steps.IDHyprpm, "hyprpm", "update")
	h := newHarness(t, Options{DryRun: true}, queue("q", child("a", "x"), hy, sudo(child("p", "sudo", "y"))))
	qs := event[QueueStarted](t, h.start("q"))
	if got := ids(qs.Steps); !slices.Equal(got, []string{"a", "p", steps.IDHyprpm}) {
		t.Errorf("plan = %v, want hyprpm moved last", got)
	}
}

func TestSessionPanicsOnWhatIsNotBuilt(t *testing.T) {
	expectPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if r := recover(); r != ErrNotImplemented {
				t.Errorf("%s: recovered %v, want ErrNotImplemented", name, r)
			}
		}()
		f()
	}
	h := newHarness(t, Options{}, queue("q", child("a", "x")),
		actions.Action{ID: "d", Discover: func(context.Context, actions.Env) actions.Discovery { return nil }})
	h.start("q")
	expectPanic("Abort", func() { h.s.Abort() })

	for _, d := range []actions.Discovery{actions.Report{Text: "r"}, actions.NeedsSelection{}} {
		h := newHarness(t, Options{}, actions.Action{ID: "d", Discover: func(context.Context, actions.Env) actions.Discovery { return nil }})
		seq := effect[Discover](t, h.start("d")).Seq
		expectPanic(reflectName(d), func() { h.s.Handle(Discovered{Seq: seq, Discovery: d}) })
	}
}

func TestSessionCommitRunsWhenTheResultIsTaken(t *testing.T) {
	committed := false
	h := newHarness(t, Options{}, queue("q", inproc("a")))
	h.start("q")
	o := h.result(steps.Result{Output: "done", Commit: func() error { committed = true; return nil }})
	hook := effect[RunHook](t, o)
	if hook.Step != "a" {
		t.Errorf("RunHook = %+v", hook)
	}
	if i, j := slices.IndexFunc(o.Effects, func(e Effect) bool { _, ok := e.(RunHook); return ok }),
		slices.IndexFunc(o.Effects, func(e Effect) bool { _, ok := e.(WriteLastRun); return ok }); i > j {
		t.Errorf("effects out of order: %#v", o.Effects)
	}
	hook.Fn()
	if !committed {
		t.Error("RunHook's Fn isn't the result's Commit")
	}
}

func TestSplitQuestion(t *testing.T) {
	for msg, want := range map[string][2]string{
		"One line?":            {"One line?", ""},
		"Head?\n\n  a\n  b":    {"Head?", "  a\n  b"},
		"Head?\nbody":          {"Head?", "body"},
		"":                     {"", ""},
		"Head?\n\n\n  trailer": {"Head?", "  trailer"},
	} {
		if t1, b1 := SplitQuestion(msg); t1 != want[0] || b1 != want[1] {
			t.Errorf("SplitQuestion(%q) = %q, %q", msg, t1, b1)
		}
	}
}
