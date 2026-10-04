package engine_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/steps"
)

var errFailed = errors.New("exit status 1")

// askpassRun is a cached sudo queue whose sudo step p is running.
func askpassRun(t *testing.T, opts Options, ss ...steps.Step) *harness {
	t.Helper()
	if len(ss) == 0 {
		ss = []steps.Step{sudo(child("p", "sudo", "x")), sudo(child("p2", "sudo", "y")), child("b", "true")}
	}
	h := newHarness(t, opts, queue("q", ss...))
	h.start("q")
	if opts.Auth == AuthSudo && !opts.DryRun {
		h.handle(ProbeDone{Cached: true})
	}
	return h
}

func ask(req uint64) AskpassRequest {
	return AskpassRequest{Req: req, Prompt: "[sudo] password for \x1b]0;pwn\x07me: ", Requester: "/usr/bin/topgrade\x1b[2J (pid 42)"}
}

func TestSessionAskpassRight(t *testing.T) {
	h := askpassRun(t, Options{})
	seq := h.seq()
	o := h.handle(ask(7))
	if len(o.Effects) != 0 {
		t.Errorf("effects = %#v", o.Effects)
	}
	np := event[NeedsPassword](t, o)
	want := NeedsPassword{ID: np.ID, Kind: PasswordAskpass, Step: "p", Requester: "/usr/bin/topgrade (pid 42)",
		Prompt: "[sudo] password for me:", AttemptsLeft: 3}
	if np != want {
		t.Errorf("NeedsPassword = %+v,\nwant %+v", np, want)
	}
	noOutput(t, "Confirm on an askpass prompt", h.s.Confirm(np.ID, true))

	o = h.password("hunter2")
	if got := event[AuthWaiting](t, o); got != (AuthWaiting{Kind: WaitPasswordCheck}) {
		t.Errorf("AuthWaiting = %+v", got)
	}
	if got := effect[AnswerAskpass](t, o); got.Req != 7 || got.Password != "hunter2" {
		t.Errorf("AnswerAskpass req %d", got.Req)
	}
	noOutput(t, "another request's answer", h.s.Handle(AskpassAnswered{Req: 8, OK: true}))
	o = h.handle(AskpassAnswered{Req: 7, OK: true})
	if !reflect.DeepEqual(o.Events, []Event{AuthResolved{Granted: true}}) || len(o.Effects) != 0 {
		t.Errorf("answered: %#v", o)
	}
	noOutput(t, "a late answer", h.s.Handle(AskpassAnswered{Req: 7, OK: true}))
	// sudo asks again later in the same step ("try again" is its re-prompt).
	o = h.handle(AskpassRequest{Req: 9, Prompt: "Sorry, try again."})
	if np := event[NeedsPassword](t, o); np.Error != "Incorrect password — try again" || np.AttemptsLeft != 3 {
		t.Errorf("re-prompt = %+v", np)
	}
	h.password("hunter2")
	h.handle(AskpassAnswered{Req: 9, OK: true})
	h.handle(StepExited{Seq: seq})
	h.ok()
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

func TestSessionAskpassWrongThenGiveUp(t *testing.T) {
	h := askpassRun(t, Options{})
	seq := h.seq()
	h.handle(ask(7))
	for left := 2; left >= 1; left-- {
		h.password("nope")
		o := h.handle(AskpassAnswered{Req: 7})
		if got := event[AuthResolved](t, o); got != (AuthResolved{AttemptsLeft: left}) {
			t.Errorf("AuthResolved = %+v", got)
		}
		if np := event[NeedsPassword](t, o); np.AttemptsLeft != left || np.Kind != PasswordAskpass {
			t.Errorf("re-ask = %+v", np)
		}
		if len(o.Effects) != 0 {
			t.Errorf("effects = %#v (nothing reaches sudo)", o.Effects)
		}
	}
	h.password("nope")
	o := h.handle(AskpassAnswered{Req: 7, Detail: "locked"})
	if got := event[AuthResolved](t, o); got != (AuthResolved{Detail: "locked"}) {
		t.Errorf("AuthResolved = %+v", got)
	}
	if !reflect.DeepEqual(o.Effects, []Effect{HangUpAskpass{Req: 7}}) {
		t.Errorf("effects = %#v", o.Effects)
	}
	// The step fails on its own; the next sudo step is refused.
	o = h.handle(StepExited{Seq: seq, Err: errFailed})
	done := all[StepDone](o.Events)
	if len(done) != 2 || done[1].ID != "p2" || done[1].Status != steps.AuthRefused {
		t.Errorf("StepDone = %+v", done)
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

func TestSessionAskpassLineBreak(t *testing.T) {
	h := askpassRun(t, Options{})
	np := event[NeedsPassword](t, h.handle(ask(7)))
	o := h.password("a\nb")
	if len(o.Effects) != 0 {
		t.Errorf("effects = %#v", o.Effects)
	}
	np2 := event[NeedsPassword](t, o)
	if np2.ID == np.ID || np2.Error != "A password can't contain a line break" || np2.AttemptsLeft != 3 {
		t.Errorf("re-ask = %+v", np2)
	}
}

func TestSessionAskpassCancelled(t *testing.T) {
	h := askpassRun(t, Options{})
	seq := h.seq()
	np := event[NeedsPassword](t, h.handle(ask(7)))
	o := h.cancel(np.ID)
	if !reflect.DeepEqual(o, Output{Effects: []Effect{HangUpAskpass{Req: 7}}}) {
		t.Errorf("cancel: %#v (the step goes on)", o)
	}
	h.handle(StepExited{Seq: seq}) // p
	h.ok()                         // p2: not refused
	event[Finished](t, h.ok())
	enginetest.CheckContract(t, h.evs)
}

func TestSessionAskpassStepEnds(t *testing.T) {
	t.Run("prompt open", func(t *testing.T) {
		h := askpassRun(t, Options{})
		seq := h.seq()
		np := event[NeedsPassword](t, h.handle(ask(7)))
		o := h.handle(StepExited{Seq: seq, Err: errFailed})
		if got := event[PromptClosed](t, o); got.ID != np.ID {
			t.Errorf("PromptClosed = %+v", got)
		}
		if !slices.Contains(o.Effects, Effect(HangUpAskpass{Req: 7})) {
			t.Errorf("effects = %#v", o.Effects)
		}
		pc := slices.IndexFunc(o.Events, func(e Event) bool { _, ok := e.(PromptClosed); return ok })
		sd := slices.IndexFunc(o.Events, func(e Event) bool { _, ok := e.(StepDone); return ok })
		if pc > sd {
			t.Error("the prompt closed after its step's StepDone")
		}
		noOutput(t, "the withdrawn prompt", h.s.SubmitPassword(np.ID, "pw"))
	})
	t.Run("check in flight", func(t *testing.T) {
		h := askpassRun(t, Options{})
		seq := h.seq()
		h.handle(ask(7))
		h.password("pw")
		o := h.handle(StepExited{Seq: seq})
		if got := event[AuthResolved](t, o); got.Granted {
			t.Errorf("AuthResolved = %+v", got)
		}
		if _, ok := find[PromptClosed](o.Events); ok {
			t.Error("closed a prompt that wasn't open")
		}
		noOutput(t, "the late answer", h.s.Handle(AskpassAnswered{Req: 7, OK: true}))
	})
}

func TestSessionAskpassRefused(t *testing.T) {
	notes := func(o Output) []string {
		var ls []string
		for _, c := range all[OutputChunk](o.Events) {
			ls = append(ls, c.Lines...)
		}
		return ls
	}
	t.Run("peer refused", func(t *testing.T) {
		h := askpassRun(t, Options{})
		o := h.handle(AskpassRequest{Req: 7, Refused: "caller is not sudo\x1b[2J\nreally"})
		if !reflect.DeepEqual(o.Effects, []Effect{HangUpAskpass{Req: 7}}) {
			t.Errorf("effects = %#v", o.Effects)
		}
		if got := notes(o); len(got) != 1 || got[0] != "archmaid: refused a sudo password request: caller is not sudo really" {
			t.Errorf("note = %q", got)
		}
		if _, ok := find[NeedsPassword](o.Events); ok {
			t.Error("asked for a refused peer")
		}
	})
	t.Run("second request", func(t *testing.T) {
		h := askpassRun(t, Options{})
		h.handle(ask(7))
		o := h.handle(ask(8))
		if !reflect.DeepEqual(o.Effects, []Effect{HangUpAskpass{Req: 8}}) {
			t.Errorf("effects = %#v", o.Effects)
		}
		if got := notes(o); len(got) != 1 || got[0] != "archmaid: refused a second sudo password request while one is open" {
			t.Errorf("note = %q", got)
		}
	})
	hangsUp := func(t *testing.T, h *harness) {
		t.Helper()
		o := h.handle(ask(7))
		if !reflect.DeepEqual(o, Output{Effects: []Effect{HangUpAskpass{Req: 7}}}) {
			t.Errorf("request: %#v, want a silent hang-up", o)
		}
	}
	t.Run("no sudo step runs", func(t *testing.T) {
		hangsUp(t, askpassRun(t, Options{}, child("a", "true"), sudo(child("p", "sudo", "x"))))
	})
	t.Run("an in-process step runs", func(t *testing.T) {
		hangsUp(t, askpassRun(t, Options{}, sudo(inproc("a"))))
	})
	t.Run("a polkit helper step runs", func(t *testing.T) {
		h := askpassRun(t, Options{Auth: AuthPolkit}, helper("h", "v"))
		h.handle(FaillockRead{Deny: 3})
		hangsUp(t, h)
	})
	t.Run("between steps", func(t *testing.T) {
		wipe := sudo(child("w", "sudo", "w"))
		wipe.Confirm = "Wipe?"
		h := askpassRun(t, Options{}, sudo(child("p", "sudo", "x")), wipe)
		h.ok()
		hangsUp(t, h) // the confirm is up, nothing runs
	})
}
