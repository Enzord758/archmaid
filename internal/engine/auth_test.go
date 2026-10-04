package engine_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// password answers the password prompt the last call opened.
func (h *harness) password(pw Secret) Output {
	h.t.Helper()
	return h.do(h.s.SubmitPassword(event[NeedsPassword](h.t, h.out).ID, pw))
}

// toPrompt takes a sudo queue from Start to its password prompt: probe
// uncached, faillock read.
func (h *harness) toPrompt(id string) NeedsPassword {
	h.t.Helper()
	h.start(id)
	effect[ReadFaillock](h.t, h.handle(ProbeDone{Cached: false}))
	return event[NeedsPassword](h.t, h.handle(FaillockRead{Failures: 1, Deny: 3}))
}

func hasLog(o Output, header string) bool {
	return slices.ContainsFunc(all[LogWritten](o.Events), func(l LogWritten) bool { return l.Header == header })
}

func sudoQueue() actions.Action {
	wipe := sudo(child("wipe", "sudo", "w"))
	wipe.Confirm, wipe.Optional = "Wipe?", true
	a := queue("q", inproc("audit"), sudo(child("p", "sudo", "x")), wipe, child("b", "true"))
	a.Gate = "Proceed?"
	return a
}

func TestSessionPasswordRight(t *testing.T) {
	h := newHarness(t, Options{}, sudoQueue())
	h.start("q")
	o := h.handle(ProbeDone{Cached: false})
	if len(o.Events) != 0 || !reflect.DeepEqual(o.Effects, []Effect{ReadFaillock{}}) {
		t.Fatalf("uncached probe: %#v", o)
	}
	noOutput(t, "a second probe answer", h.s.Handle(ProbeDone{Cached: false}))
	o = h.handle(FaillockRead{Failures: 1, Deny: 3})
	if got := event[FaillockState](t, o); got != (FaillockState{Failures: 1, Deny: 3}) {
		t.Errorf("FaillockState = %+v", got)
	}
	noOutput(t, "a second faillock answer", h.s.Handle(FaillockRead{}))
	np := event[NeedsPassword](t, o)
	if np.Kind != PasswordUpFront || np.AttemptsLeft != 3 || np.Error != "" || np.Gate != "Proceed?" {
		t.Errorf("NeedsPassword = %+v", np)
	}
	noOutput(t, "a stale password", h.s.SubmitPassword(np.ID+1, "pw"))

	o = h.password("hunter2")
	if got := event[AuthWaiting](t, o); got != (AuthWaiting{Kind: WaitPasswordCheck}) {
		t.Errorf("AuthWaiting = %+v", got)
	}
	if got := effect[Authenticate](t, o); got.Password != "hunter2" {
		t.Error("Authenticate doesn't carry the password")
	}
	// The prompt is closed while the check runs: nothing answers it.
	noOutput(t, "a second submit", h.s.SubmitPassword(np.ID, "again"))
	noOutput(t, "a cancel mid-check", h.s.CancelPrompt(np.ID))

	o = h.handle(AuthDone{OK: true})
	if got := event[AuthResolved](t, o); got != (AuthResolved{Granted: true}) {
		t.Errorf("AuthResolved = %+v", got)
	}
	if _, ok := find[NeedsConfirm](o.Events); ok {
		t.Error("the gate was asked after the password (the prompt is the gate)")
	}
	event[QueueStarted](t, o)
	if !slices.Contains(o.Effects, Effect(KeepAlive{On: true})) {
		t.Errorf("no keep-alive: %#v", o.Effects)
	}
	noOutput(t, "a late AuthDone", h.s.Handle(AuthDone{OK: true}))
	h.result(steps.Result{})
	h.ok()
	h.confirm(false)
	event[Finished](t, h.ok())
	enginetest.CheckContract(t, h.evs)
}

func TestSessionPasswordWrongThenRight(t *testing.T) {
	h := newHarness(t, Options{}, sudoQueue())
	np := h.toPrompt("q")
	h.password("nope")
	o := h.handle(AuthDone{Err: errors.New("wrong")})
	if got := event[AuthResolved](t, o); got != (AuthResolved{AttemptsLeft: 2}) {
		t.Errorf("AuthResolved = %+v", got)
	}
	np2 := event[NeedsPassword](t, o)
	if np2.ID == np.ID || np2.AttemptsLeft != 2 || np2.Error != "Incorrect password — 2 attempts left" || np2.Gate != "Proceed?" {
		t.Errorf("re-ask = %+v", np2)
	}
	noOutput(t, "the first prompt's ID", h.s.SubmitPassword(np.ID, "pw"))

	h.password("nope")
	o = h.handle(AuthDone{Detail: "Account locked\x1b[2J until 14:32", Err: errors.New("locked")})
	if got := event[AuthResolved](t, o); got.Detail != "Account locked until 14:32" || got.AttemptsLeft != 1 {
		t.Errorf("AuthResolved = %+v, want the detail sanitized", got)
	}
	if got := event[NeedsPassword](t, o); got.Error != "Account locked until 14:32" || got.AttemptsLeft != 1 {
		t.Errorf("re-ask = %+v, want the detail verbatim", got)
	}
	h.password("hunter2")
	event[QueueStarted](t, h.handle(AuthDone{OK: true}))
}

func TestSessionThreeWrongPasswords(t *testing.T) {
	h := newHarness(t, Options{}, sudoQueue())
	h.toPrompt("q")
	var o Output
	for i := range 3 {
		h.password("nope")
		o = h.handle(AuthDone{Err: errors.New("wrong")})
		if i < 2 {
			event[NeedsPassword](t, o)
		}
	}
	if got := event[AuthResolved](t, o); got != (AuthResolved{}) {
		t.Errorf("AuthResolved = %+v", got)
	}
	if _, ok := find[NeedsPassword](o.Events); ok {
		t.Error("asked a fourth time")
	}
	event[QueueStarted](t, o) // the queue runs anyway, without the gate
	if _, ok := find[KeepAlive](o.Effects); ok {
		t.Error("keep-alive armed with no ticket")
	}
	o = h.result(steps.Result{Output: "audited"})
	// p and the confirm step wipe are refused without running or asking.
	done := all[StepDone](o.Events)
	if len(done) != 3 || done[1].ID != "p" || done[1].Status != steps.AuthRefused || done[2].ID != "wipe" || done[2].Status != steps.AuthRefused {
		t.Errorf("StepDone = %+v", done)
	}
	if _, ok := find[NeedsConfirm](o.Events); ok {
		t.Error("a refused step's confirm was asked")
	}
	if !hasLog(o, "SKIP: p done (sudo authentication failed)") || !hasLog(o, "SKIP: wipe done (sudo authentication failed)") {
		t.Errorf("logs = %+v", all[LogWritten](o.Events))
	}
	o = h.ok()
	if fin := event[Finished](t, o); fin.Outcome != OutcomeCompleted || fin.Refused != 2 || fin.Skipped != 2 {
		t.Errorf("Finished = %+v", fin)
	}
	if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeIncomplete || rec.StepsRefused != 2 {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

func TestSessionPasswordFaillock(t *testing.T) {
	unlock := t0.Add(10 * time.Minute)
	for _, c := range []struct {
		name string
		in   FaillockRead
		want *FaillockState
	}{
		{"unreadable", FaillockRead{Err: errors.New("EACCES")}, nil},
		{"locked", FaillockRead{Failures: 3, Deny: 3, Unlock: unlock}, &FaillockState{Failures: 3, Deny: 3, Locked: true, Unlock: unlock}},
		{"never locks", FaillockRead{Failures: 5, Unlock: unlock}, &FaillockState{Failures: 5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, Options{}, sudoQueue())
			h.start("q")
			h.handle(ProbeDone{})
			o := h.handle(c.in)
			got, ok := find[FaillockState](o.Events)
			if c.want == nil && ok || c.want != nil && got != *c.want {
				t.Errorf("FaillockState = %+v (%v), want %+v", got, ok, c.want)
			}
			// The sudo flow asks anyway: the front end shows the state.
			event[NeedsPassword](t, o)
		})
	}
}

func TestSessionPasswordCancelled(t *testing.T) {
	h := newHarness(t, Options{}, sudoQueue())
	np := h.toPrompt("q")
	o := h.cancel(np.ID)
	if fin := event[Finished](t, o); fin.Outcome != OutcomeCancelled {
		t.Errorf("Finished = %+v", fin)
	}
	if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeCancelled {
		t.Errorf("record = %+v", rec)
	}
}

// ── Repair's NeedsRoot ───────────────────────────────────────────────────────

func needsRootAction() (actions.Action, actions.NeedsRoot) {
	nr := actions.NeedsRoot{
		Items: []string{"foo"}, Busy: "Rechecking…", AuthFailed: "Sudo authentication failed — can't scan.",
		Rescan: func(context.Context, actions.Env) actions.Discovery { panic("the Session never discovers") },
	}
	return actions.Action{ID: "r", Busy: "Scanning…",
		Discover: func(context.Context, actions.Env) actions.Discovery { panic("the Session never discovers") }}, nr
}

// discovered hands the Session nr for the action the last call started.
func (h *harness) discovered(d actions.Discovery) Output {
	h.t.Helper()
	return h.handle(Discovered{Seq: effect[Discover](h.t, h.out).Seq, Discovery: d})
}

func TestSessionNeedsRootCached(t *testing.T) {
	a, nr := needsRootAction()
	h := newHarness(t, Options{}, a)
	h.start("r")
	o := h.discovered(nr)
	if !reflect.DeepEqual(o.Effects, []Effect{Probe{}}) {
		t.Fatalf("effects = %#v", o.Effects)
	}
	o = h.handle(ProbeDone{Cached: true})
	if got := event[PhaseChanged](t, o); got != (PhaseChanged{Phase: PhaseDiscovering, Busy: "Rechecking…"}) {
		t.Errorf("phase = %+v", got)
	}
	d := effect[Discover](t, o)
	if d.Action != "r" || d.Run == nil {
		t.Errorf("rescan = %+v", d)
	}
	repair := sudo(child("fix", "sudo", "pacman", "-S"))
	o = h.discovered(actions.Pending{Steps: []steps.Step{repair}})
	if _, ok := find[Probe](o.Effects); ok {
		t.Error("the rescan's queue was probed again")
	}
	event[QueueStarted](t, o)
	o = h.ok()
	if rec := effect[WriteLastRun](t, o).Record; rec.Action != "r" || rec.Outcome != logstore.OutcomeOK {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

func TestSessionNeedsRootFalsePositive(t *testing.T) {
	a, nr := needsRootAction()
	h := newHarness(t, Options{}, a)
	h.start("r")
	h.discovered(nr)
	h.handle(ProbeDone{Cached: true})
	o := h.discovered(actions.Notice{Text: "No broken packages found."})
	if fin := event[Finished](t, o); fin.Outcome != OutcomeNotice {
		t.Errorf("Finished = %+v", fin)
	}
	if _, ok := find[WriteLastRun](o.Effects); ok {
		t.Error("a notice wrote a last-run record")
	}
}

func TestSessionNeedsRootPassword(t *testing.T) {
	a, nr := needsRootAction()

	t.Run("right", func(t *testing.T) {
		h := newHarness(t, Options{}, a)
		h.start("r")
		h.discovered(nr)
		effect[ReadFaillock](t, h.handle(ProbeDone{}))
		h.handle(FaillockRead{Deny: 3})
		h.password("hunter2")
		o := h.handle(AuthDone{OK: true})
		event[AuthResolved](t, o)
		effect[Discover](t, o)
	})

	t.Run("three wrong", func(t *testing.T) {
		h := newHarness(t, Options{}, a)
		h.start("r")
		h.discovered(nr)
		h.handle(ProbeDone{})
		h.handle(FaillockRead{Deny: 3})
		var o Output
		for range 3 {
			h.password("nope")
			o = h.handle(AuthDone{Err: errors.New("wrong")})
		}
		if got := event[Notice](t, o); got.Text != nr.AuthFailed {
			t.Errorf("Notice = %+v", got)
		}
		if fin := event[Finished](t, o); fin.Outcome != OutcomeNotice {
			t.Errorf("Finished = %+v", fin)
		}
		if _, ok := find[WriteLastRun](o.Effects); ok {
			t.Error("the give-up wrote a last-run record (the TUI's doesn't)")
		}
		enginetest.CheckContract(t, h.evs)
	})

	t.Run("cancelled", func(t *testing.T) {
		h := newHarness(t, Options{}, a)
		h.start("r")
		h.discovered(nr)
		h.handle(ProbeDone{})
		np := event[NeedsPassword](t, h.handle(FaillockRead{Deny: 3}))
		if np.Gate != "" {
			t.Errorf("Gate = %q for an action without one", np.Gate)
		}
		o := h.cancel(np.ID)
		if rec := effect[WriteLastRun](t, o).Record; rec.Action != "r" || rec.Outcome != logstore.OutcomeCancelled {
			t.Errorf("record = %+v (the TUI records a cancelled recheck)", rec)
		}
	})

	for _, c := range []struct {
		name string
		opts Options
	}{{"dry run", Options{DryRun: true}}, {"polkit", Options{Auth: AuthPolkit}}} {
		t.Run(c.name+" rescans at once", func(t *testing.T) {
			h := newHarness(t, c.opts, a)
			h.start("r")
			o := h.discovered(nr)
			if _, ok := find[Probe](o.Effects); ok {
				t.Error("probed")
			}
			effect[Discover](t, o)
		})
	}
}

// ── polkit ───────────────────────────────────────────────────────────────────

func helper(id, verb string) steps.Step {
	s := sudo(child(id, "sudo", id))
	s.Verb = verb
	return s
}

func TestSessionPolkitGranted(t *testing.T) {
	accepted := false
	conf := helper("c", "verb-c")
	conf.Confirm, conf.OnAccept = "Sure?", func() error { accepted = true; return nil }
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "verb-h"), conf, sudo(child("t", "topgrade"))))
	o := h.start("q")
	if _, ok := find[StepStarted](o.Events); ok {
		t.Fatal("a helper step started before the faillock read")
	}
	if !reflect.DeepEqual(o.Effects, []Effect{ReadFaillock{}}) {
		t.Fatalf("effects = %#v", o.Effects)
	}
	o = h.handle(FaillockRead{Failures: 2, Deny: 3})
	event[FaillockState](t, o)
	if got := event[AuthWaiting](t, o); got != (AuthWaiting{Kind: WaitPolkit, Step: "h"}) {
		t.Errorf("AuthWaiting = %+v", got)
	}
	if st := effect[StartStep](t, o); st.Step.Verb != "verb-h" {
		t.Errorf("StartStep = %+v", st)
	}
	seq := h.seq()
	noOutput(t, "a stale grant", h.s.Handle(PrivilegeGranted{Seq: seq + 99}))
	o = h.handle(PrivilegeGranted{Seq: seq})
	if got := event[AuthResolved](t, o); got != (AuthResolved{Granted: true}) {
		t.Errorf("AuthResolved = %+v", got)
	}
	noOutput(t, "a second grant", h.s.Handle(PrivilegeGranted{Seq: seq}))
	h.handle(StepOutput{Seq: seq, Lines: []string{"done"}})
	o = h.handle(StepExited{Seq: seq})
	if _, ok := find[AuthResolved](o.Events); ok {
		t.Error("resolved twice")
	}
	// The confirmed helper step: confirm, then the faillock, then OnAccept.
	o = h.confirm(true)
	if accepted || !reflect.DeepEqual(o.Effects, []Effect{ReadFaillock{}}) {
		t.Fatalf("effects = %#v (OnAccept ran: %v)", o.Effects, accepted)
	}
	o = h.handle(FaillockRead{Deny: 3})
	effect[RunHook](t, o).Fn()
	if !accepted {
		t.Error("OnAccept didn't run")
	}
	// Exits cleanly without a start line seen: still granted.
	o = h.handle(StepExited{Seq: h.seq()})
	if got := event[AuthResolved](t, o); got != (AuthResolved{Granted: true}) {
		t.Errorf("AuthResolved = %+v", got)
	}
	// A NeedsPriv step without a verb isn't the helper: it starts at once.
	effect[StartStep](t, o)
	if _, ok := find[AuthWaiting](o.Events); ok {
		t.Error("waited on polkit for a step without a verb")
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

func TestSessionPolkitNotGranted(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v"), child("b", "true")))
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	o := h.handle(StepExited{Seq: h.seq(), Err: errors.New("exit status 127"), NotGranted: true})
	if got := event[AuthResolved](t, o); got != (AuthResolved{}) {
		t.Errorf("AuthResolved = %+v", got)
	}
	ar := slices.IndexFunc(o.Events, func(e Event) bool { _, ok := e.(AuthResolved); return ok })
	sd := slices.IndexFunc(o.Events, func(e Event) bool { _, ok := e.(StepDone); return ok })
	if ar > sd {
		t.Error("AuthResolved after the StepDone it resolves")
	}
	if got := event[StepDone](t, o); got.Status != steps.AuthRefused {
		t.Errorf("StepDone = %+v", got)
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

func TestSessionPolkitLocked(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v"), child("b", "true")))
	h.start("q")
	o := h.handle(FaillockRead{Failures: 3, Deny: 3, Unlock: t0.Add(time.Minute)})
	if got := event[FaillockState](t, o); !got.Locked {
		t.Errorf("FaillockState = %+v", got)
	}
	if got := event[StepDone](t, o); got.ID != "h" || got.Status != steps.AuthRefused {
		t.Errorf("StepDone = %+v", got)
	}
	if _, ok := find[AuthWaiting](o.Events); ok {
		t.Error("waited on polkit for a locked account")
	}
	if !hasLog(o, "SKIP: h done (account locked)") {
		t.Errorf("logs = %+v", all[LogWritten](o.Events))
	}
	if st := effect[StartStep](t, o); st.Step.ID != "b" {
		t.Errorf("started %q, want only the next step", st.Step.ID)
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

func TestSessionPolkitDryRunAsksNothing(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit, DryRun: true}, queue("q", helper("h", "v")))
	o := h.start("q")
	if _, ok := find[ReadFaillock](o.Effects); ok {
		t.Error("a dry run read the faillock count")
	}
	effect[StartStep](t, o)
}
