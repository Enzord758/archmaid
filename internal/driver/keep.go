package driver

// The GUI's one password per run (Deps.KeepPassword; docs/HANDOVER.md, "The
// GUI keeps the run's password"). The first password the user gives the
// run, at an askpass prompt or at polkit's (Deps.Agent), is kept in a
// privilege.KeptPassword once su has accepted it. The run's later prompts
// are then answered from it, by the Driver alone, without the Session:
//
//   - sudo's askpass, only for a request privilege.Request.Verify passed
//     against the running step's child (our uid, our helper, run by sudo,
//     inside that child's process tree), whose step is a NeedsPriv child,
//     whose helper is in that child's session (Detach makes every step a
//     session leader), and whose sudo was started by topgrade or the AUR helper with
//     nothing else between it and the step (Requester.KeptMayAnswer: a
//     makepkg or a PKGBUILD's shell gets the user's dialog). A sudo process
//     that asks a second time was refused what it got: the kept password
//     is dropped for good and the request goes to the Session (the user);
//   - polkit's text agent, only while a helper step runs (the agent itself
//     only passes on prompts for archmaid's helper actions, for the current
//     user).
//
// Every kept answer is su-checked first, as the user's are (su costs no
// pam_faillock attempt); one that su refuses drops the kept password and
// goes to the Session, and so does a polkit failure. So a kept password is
// never used after it failed once, and never sent where it was refused.
// It is wiped when the run ends in any way: Finished, Close (Stop and
// Quit, a signal), KillNow, and at Deps.KeepFor.

import (
	"context"
	"fmt"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// defaultKeepFor is how long a kept password lasts at most.
const defaultKeepFor = time.Hour

// AgentPrompt is polkit's text agent asking for the password of the user
// for a helper step (gui/internal/polkitagent through Deps.Agent). The
// agent has checked that it authenticates one of archmaid's helper actions
// (org.archmaid.helper.*) as the current user; it passes on nothing else.
type AgentPrompt interface {
	// Action is the polkit action id the agent authenticates.
	Action() string
	// Prompt is PAM's prompt ("Password: "), raw.
	Prompt() string
	// Answer writes pw to the agent; Done then says how polkit took it.
	// The caller still owns pw.
	Answer(pw []byte) error
	// Done is closed once polkit's verdict on an Answer is known, or the
	// agent went away; Err is then nil when polkit authenticated, else
	// why not.
	Done() <-chan struct{}
	Err() error
	// Cancel ends the prompt unanswered: polkit refuses the request.
	Cancel()
}

// asker is an open request the Session may answer: sudo's (askpass) or
// polkit's agent's.
type asker interface {
	// answer su-checks pw and hands it on. accepted runs once su has
	// accepted it and before it goes out, so whatever the answer sets off
	// (sudo's command, polkit's verdict and the next helper step's prompt)
	// comes after it.
	answer(ctx context.Context, pw string, accepted func()) (detail string, err error)
	cancel()
}

type sudoAsk struct{ r *privilege.Request }

func (a sudoAsk) answer(_ context.Context, pw string, accepted func()) (string, error) {
	return a.r.AnswerThen(pw, accepted)
}
func (a sudoAsk) cancel() { a.r.Cancel() }

type agentAsk struct{ p AgentPrompt }

// answer su-checks pw before it reaches polkit (whose PAM stack counts a
// wrong password against pam_faillock; su's doesn't), then writes it.
func (a agentAsk) answer(ctx context.Context, pw string, accepted func()) (string, error) {
	b := []byte(pw)
	defer clear(b)
	if detail, err := privilege.CheckPasswordBytes(ctx, b); err != nil {
		return detail, err
	}
	accepted()
	return "", a.p.Answer(b)
}
func (a agentAsk) cancel() { a.p.Cancel() }

// sudoKey names one sudo process: its pid and start time.
type sudoKey struct {
	pid   int
	start uint64
}

// readAgent passes the agent's prompts to the loop.
func (d *Driver) readAgent() {
	for {
		select {
		case p, ok := <-d.cfg.Agent:
			if !ok {
				return
			}
			if p != nil {
				d.post(msg{agent: p})
			}
		case <-d.stop:
			return
		}
	}
}

// askpassRequest vets sudo's request: answered from the kept password when
// it may be (true: nothing for the Session), else the Session's Input.
func (d *Driver) askpassRequest(m msg) (engine.Input, bool) {
	r := m.ask
	in, from, ok := d.verify(r)
	if !ok || m.noKeep {
		return in, false
	}
	// A sudo process that asks again was refused what it got last time
	// (sudo re-asks after a wrong password; su let none through, so it
	// refused a password su accepted): never the kept password again.
	key := sudoKey{from.Sudo.Pid, from.Sudo.Start}
	if d.askedBy[key] {
		d.keep.Fail()
		return in, false
	}
	d.askedBy[key] = true
	if !d.keep.Has() || !d.childPriv[d.lastChild] {
		return in, false
	}
	if !from.KeptMayAnswer() {
		// Something else stands between the step and this sudo (makepkg,
		// a PKGBUILD's shell): the user's dialog, naming the caller.
		return in, false
	}
	c := d.children[d.lastChild]
	if d.cfg.Detach && from.Session != c.Pid {
		return in, false // not in the step's own session
	}
	delete(d.asks, in.(engine.AskpassRequest).Req) // the Session never hears of it
	d.work(func() engine.Input {
		pw, err := d.keep.Copy()
		if err == nil {
			_, err = r.AnswerBytes(d.root, pw)
			clear(pw)
		}
		if err != nil {
			d.keep.Fail()
			d.post(msg{ask: r, noKeep: true}) // the user, then
		}
		return nil
	})
	return nil, true
}

// agentRequest takes polkit's agent's prompt: hung up unless a helper step
// runs; answered from the kept password (true); else the Session's Input.
func (d *Driver) agentRequest(m msg) (engine.Input, bool) {
	p := m.agent
	if len(d.helpers) == 0 {
		p.Cancel() // no helper step of ours waits for polkit
		return nil, true
	}
	if !m.noKeep && d.keep.Has() {
		d.work(func() engine.Input {
			d.answerAgentKept(p)
			return nil
		})
		return nil, true
	}
	d.lastAsk++
	d.asks[d.lastAsk] = agentAsk{p}
	return engine.AskpassRequest{
		Req: d.lastAsk, Prompt: p.Prompt(), Polkit: true, Action: p.Action(),
		Requester: "polkit's agent (pkttyagent)",
	}, false
}

// answerAgentKept answers polkit's agent's prompt p from the kept password,
// on a worker: su-checked first; a password that is gone by now (wiped
// since agentRequest saw it) or that su refuses sends p to the Session
// (the user), and one su refuses, or the agent can't take, is dropped.
func (d *Driver) answerAgentKept(p AgentPrompt) {
	pw, err := d.keep.Copy()
	if err != nil {
		d.post(msg{agent: p, noKeep: true})
		return
	}
	defer clear(pw)
	if _, err := privilege.CheckPasswordBytes(d.root, pw); err != nil {
		d.keep.Fail()
		d.post(msg{agent: p, noKeep: true})
		return
	}
	if p.Answer(pw) != nil {
		d.keep.Fail()
		return
	}
	d.watchAgent(p)
}

// watchAgent drops the kept password if polkit refuses an answer.
func (d *Driver) watchAgent(p AgentPrompt) {
	d.goRun(func() {
		select {
		case <-p.Done():
			if p.Err() != nil {
				d.keep.Fail()
			}
		case <-d.stop:
		}
	})
}

// answerUser answers the Session's open request a with the user's pw (the
// AnswerAskpass effect, on a worker). A password su accepts is kept for
// the run's later prompts (the first one only) before it goes out, not
// after: what the answer sets off may ask again at once (polkit grants
// the helper step, the next one's agent prompt comes in), and that prompt
// must find it kept, or it goes to the user, who has already answered.
// One kept that then can't be handed on (the agent went away) is dropped.
func (d *Driver) answerUser(a asker, pw string) (detail string, err error) {
	kept := false
	detail, err = a.answer(d.root, pw, func() { kept = d.keepAnswer(pw) })
	switch {
	case err != nil && kept:
		d.keep.Fail()
	case err == nil && d.cfg.KeepPassword:
		if ag, ok := a.(agentAsk); ok {
			d.watchAgent(ag.p) // polkit's verdict on it
		}
	}
	return detail, err
}

// keepAnswer keeps a password the user gave that su accepted; true when
// it is now the kept one.
func (d *Driver) keepAnswer(pw string) bool {
	if !d.cfg.KeepPassword {
		return false
	}
	b := []byte(pw)
	defer clear(b)
	return d.keep.Keep(b)
}

// requesterOf names a verified caller as the TUI's askpass popup does:
// its binary (the tail of the path, at most 40 runes), then its pid.
func requesterOf(from privilege.Requester) string {
	exe := from.CallerExe
	if exe == "" {
		exe = "an unknown program"
	}
	return fmt.Sprintf("%s (pid %d)", sanitize.ClipTail(sanitize.OneLine(exe), 40), from.CallerPid)
}
