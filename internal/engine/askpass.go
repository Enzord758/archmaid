package engine

import (
	"strings"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// Askpass, as the TUI's handleAskpass does it. A request reaches the
// Session already peer-checked by the Driver (privilege's Request.Verify
// against the running step's child): Refused says why it failed. It is
// answered only while a sudo step runs — a NeedsPriv child that isn't a
// polkit helper step — and only one at a time; anything else is hung up on
// (a refused peer or a second request with a line in the step's output).
// The password is su-checked by the Driver before it reaches sudo
// (AnswerAskpass), on the run's wrong-password count: the third wrong one
// hangs up and gives up on sudo for the rest of the queue. Cancelling the
// prompt hangs up (sudo counts that attempt as failed); the step's end
// withdraws an open prompt (PromptClosed).

// askpass is the open askpass request.
type askpass struct {
	req                     uint64
	step, requester, prompt string
	checking                bool // an AnswerAskpass is in flight
}

// askpassRequest vets a request and, if it may be ours, asks for the
// password.
func (s *Session) askpassRequest(o *Output, in AskpassRequest) {
	r := s.run
	var st *slot
	if r != nil && r.stepSeq != 0 {
		st = r.running()
	}
	if st == nil || !st.step.NeedsPriv || st.step.Run != nil || s.polkitStep(st.step) {
		o.effect(HangUpAskpass{Req: in.Req}) // nothing of ours is asking
		return
	}
	switch {
	case r.ask != nil:
		o.effect(HangUpAskpass{Req: in.Req})
		s.output(o, []string{"archmaid: refused a second sudo password request while one is open"})
		return
	case in.Refused != "":
		o.effect(HangUpAskpass{Req: in.Req})
		s.output(o, []string{sanitize.OneLine("archmaid: refused a sudo password request: " + in.Refused)})
		return
	}
	r.ask = &askpass{
		req: in.Req, step: st.step.ID,
		requester: sanitize.OneLine(in.Requester),
		prompt:    sanitize.Clip(sanitize.OneLine(in.Prompt), maxPrompt),
	}
	why := ""
	if strings.Contains(strings.ToLower(in.Prompt), "try again") {
		why = "Incorrect password — try again" // sudo's own re-prompt
	}
	s.askAskpass(o, why)
}

// maxPrompt clips sudo's prompt (untrusted: sudo -p sets it).
const maxPrompt = 200

// askAskpass opens the prompt for the open request.
func (s *Session) askAskpass(o *Output, why string) {
	r := s.run
	a := r.ask
	r.prompt, r.promptFor = s.promptID(), promptAskpass
	o.event(NeedsPassword{
		ID: r.prompt, Kind: PasswordAskpass, Step: a.step,
		Requester: a.requester, Prompt: a.prompt,
		AttemptsLeft: maxAttempts - r.attempts, Error: why,
	})
}

// submitAskpass hands the password to the Driver's su check and sudo. A
// line break is refused here and asked again (nothing sent, no attempt
// counted): sudo reads one line, so it would get a different password than
// su checked.
func (s *Session) submitAskpass(o *Output, pw Secret) {
	r := s.run
	if strings.ContainsAny(string(pw), "\r\n") {
		s.askAskpass(o, "A password can't contain a line break")
		return
	}
	r.prompt, r.ask.checking = 0, true
	o.event(AuthWaiting{Kind: WaitPasswordCheck})
	o.effect(AnswerAskpass{Req: r.ask.req, Password: pw})
}

// cancelAskpass hangs up on the open request: sudo fails that attempt, and
// the step goes on.
func (s *Session) cancelAskpass(o *Output) {
	r := s.run
	r.prompt = 0
	o.effect(HangUpAskpass{Req: r.ask.req})
	r.ask = nil
}

// askpassAnswered takes the check's verdict for the open request.
func (s *Session) askpassAnswered(o *Output, in AskpassAnswered) {
	r := s.run
	if r == nil || r.ask == nil || !r.ask.checking || in.Req != r.ask.req {
		return
	}
	r.ask.checking = false
	if in.OK {
		r.attempts, r.ask = 0, nil
		o.event(AuthResolved{Granted: true})
		return
	}
	r.attempts++
	left, detail := maxAttempts-r.attempts, sanitize.OneLine(in.Detail)
	o.event(AuthResolved{AttemptsLeft: left, Detail: detail})
	if left > 0 {
		s.askAskpass(o, wrongPassword(left, detail))
		return
	}
	// Unanswered: this sudo fails, and the queue's later sudo steps are
	// refused, as after a failed prime.
	o.effect(HangUpAskpass{Req: r.ask.req})
	r.ask, r.sudoGone = nil, true
}

// dropAskpass withdraws the open request when its step ends: the prompt
// closes (PromptClosed), a check in flight resolves as not granted, and
// sudo is hung up on.
func (s *Session) dropAskpass(o *Output) {
	r := s.run
	a := r.ask
	if a == nil {
		return
	}
	if r.prompt != 0 && r.promptFor == promptAskpass {
		o.event(PromptClosed{ID: r.prompt})
		r.prompt = 0
	}
	if a.checking {
		o.event(AuthResolved{}) // the step it was for is over
	}
	o.effect(HangUpAskpass{Req: a.req})
	r.ask = nil
}
