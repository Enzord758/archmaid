package engine

import (
	"fmt"
	"strconv"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// How a run gets root, as the TUI does it:
//
//   - AuthSudo: a queue with a NeedsPriv step is probed for a cached ticket;
//     uncached, the faillock count is read (FaillockState) and the password
//     asked for (NeedsPassword, the prompt doubling as the proceed-gate).
//     A submitted password is checked in two stages by the Driver
//     (Authenticate: su, then sudo -S -v); three wrong ones give up, and the
//     queue then runs with every NeedsPriv step AuthRefused ("sudo
//     authentication failed"). Wrong passwords are counted per run, with
//     the askpass prompts (E4) on the same count.
//     Exception (lateAuth, queue.go): a queue of ONE confirmed root step
//     with no gate (Delete entire package cache) asks its confirm first;
//     the probe and password come only after a yes, so a no costs no password.
//   - Repair's NeedsRoot authenticates the same way, then runs its Rescan;
//     its queue needs no second probe. Three wrong passwords end it in its
//     AuthFailed notice.
//   - AuthPolkit: nothing is asked up front. Each helper step (a child with
//     a Verb) reads the faillock count first: a locked account is refused
//     without a polkit dialog; otherwise the step starts under
//     AuthWaiting{WaitPolkit} until PrivilegeGranted, or until it exits
//     NotGranted (AuthRefused).

// faillockWait is what a ReadFaillock in flight goes on to.
type faillockWait int

const (
	faillockNone     faillockWait = iota
	faillockUpFront               // the up-front password prompt
	faillockPolkit                // the current helper step
	faillockCombined              // a combined run's one call (combine.go)
)

// readFaillock asks for the faillock count; w is what follows its answer.
func (s *Session) readFaillock(o *Output, w faillockWait) {
	s.run.faillockFor = w
	o.effect(ReadFaillock{})
}

// faillockRead reports the count (an unreadable one is not reported) and
// goes on with what it was read for.
func (s *Session) faillockRead(o *Output, in FaillockRead) {
	r := s.run
	if r == nil || r.faillockFor == faillockNone {
		return
	}
	w := r.faillockFor
	r.faillockFor = faillockNone
	var fs FaillockState
	if in.Err == nil {
		fs = faillockState(in)
		o.event(fs)
	}
	if w == faillockUpFront {
		s.askPassword(o, "")
		return
	}
	if w == faillockCombined {
		if fs.Locked {
			s.combineLocked(o)
		} else {
			s.combineStart(o)
		}
		return
	}
	if fs.Locked {
		// Even the right password fails now, and pkexec would only say 127:
		// refuse without a dialog.
		st := r.plan[r.cur].step
		s.settle(o, steps.AuthRefused, "")
		s.log(o, st.LogCategory(), "SKIP", st.LogLabel+" (account locked)", "")
		s.advance(o)
		return
	}
	s.startStep(o, r.acceptedLaunch)
	o.event(AuthWaiting{Kind: WaitPolkit, Step: r.plan[r.cur].step.ID})
	r.polkitWait = true
}

// faillockState is in as an Event. pam_faillock counts a locked account's
// failures as the ones that locked it (privilege.Status), so it is locked
// exactly when they reach a non-zero deny.
func faillockState(in FaillockRead) FaillockState {
	fs := FaillockState{Failures: in.Failures, Deny: in.Deny}
	if in.Deny > 0 && in.Failures >= in.Deny {
		fs.Locked, fs.Unlock = true, in.Unlock
	}
	return fs
}

// askPassword opens the up-front password prompt, with why the last
// attempt failed ("" on the first ask).
func (s *Session) askPassword(o *Output, why string) {
	r := s.run
	r.prompt, r.promptFor = s.promptID(), promptPassword
	o.event(NeedsPassword{ID: r.prompt, Kind: PasswordUpFront, AttemptsLeft: maxAttempts - r.attempts, Error: why,
		Gate: sanitize.String(r.gate, sanitize.Plain)})
}

// submitUpFront hands the password to the Driver's two-stage check, with
// the prompt closed until it answers (the TUI locks its input).
func (s *Session) submitUpFront(o *Output, pw Secret) {
	r := s.run
	r.prompt, r.checking = 0, true
	o.event(AuthWaiting{Kind: WaitPasswordCheck})
	o.effect(Authenticate{Password: pw})
}

// authDone takes the check's verdict: right goes on (the rescan, or the
// queue: the prompt was its gate), wrong asks again, the third wrong one
// gives up.
func (s *Session) authDone(o *Output, in AuthDone) {
	r := s.run
	if r == nil || !r.checking {
		return
	}
	r.checking = false
	if in.OK {
		// Detail on a right password is a note for the run (the TUI's
		// sudo keeps no ticket, so each sudo asks through archmaid:
		// privilege.NoTicketNote).
		r.attempts = 0
		o.event(AuthResolved{Granted: true, Detail: sanitize.OneLine(in.Detail)})
		if r.root != nil {
			s.rescan(o)
			return
		}
		if r.lateGoing {
			s.lateGo(o)
			return
		}
		s.startQueue(o)
		return
	}
	if in.NoTicket {
		// Right password, unusable ticket, and no sudo wrapper to send
		// topgrade's and yay's own sudo through archmaid: they would
		// prompt on the raw tty. Nothing has run.
		r.attempts = 0
		o.event(AuthResolved{})
		o.event(Notice{Text: sanitize.String(in.Detail, sanitize.Plain)})
		s.finish(o, OutcomeCancelled)
		return
	}
	r.attempts++
	left, detail := maxAttempts-r.attempts, sanitize.OneLine(in.Detail)
	o.event(AuthResolved{AttemptsLeft: left, Detail: detail})
	if left > 0 {
		s.askPassword(o, wrongPassword(left, detail))
		return
	}
	if nr := r.root; nr != nil {
		// No queue to skip through: the scan itself needs root.
		r.root = nil
		s.endDiscovery(o, actions.Notice{Text: nr.AuthFailed, Err: "sudo authentication failed after " +
			strconv.Itoa(maxAttempts) + " attempts",
			Details: []string{"Tried: the root recheck of " + strconv.Itoa(len(nr.Items)) + " flagged item(s), which needs sudo"}})
		return
	}
	// The queue runs anyway; its NeedsPriv steps are refused (advance).
	r.sudoGone = true
	if r.lateGoing {
		r.lateGoing = false
		r.cur-- // the confirmed step, refused by advance
		s.advance(o)
		return
	}
	s.startQueue(o)
}

// lateGo starts the confirmed root step once root is settled (lateAuth).
func (s *Session) lateGo(o *Output) {
	r := s.run
	r.lateGoing = false
	r.keepAlive = true
	o.effect(KeepAlive{On: true})
	s.setPhase(o, PhaseRunning, "")
	s.startStep(o, true)
}

// wrongPassword is a failed attempt's Error: detail (a lockout notice)
// verbatim, else the attempts left.
func wrongPassword(left int, detail string) string {
	if detail != "" {
		return detail
	}
	s := "s"
	if left == 1 {
		s = ""
	}
	return fmt.Sprintf("Incorrect password — %d attempt%s left", left, s)
}

// needsRoot holds a discovery that can only finish as root (Repair's
// recheck) and authenticates for it like a sudo queue. A dry run never
// asks, and polkit can't prime a ticket: both rescan at once (the rescan's
// `sudo -n` then fails cleanly without one).
func (s *Session) needsRoot(o *Output, d actions.NeedsRoot) {
	r := s.run
	r.root, r.planned = &d, true
	if s.opts.DryRun || s.opts.Auth == AuthPolkit {
		s.rescan(o)
		return
	}
	r.probing = true
	s.setPhase(o, PhaseProbing, "")
	o.effect(Probe{})
}

// rescan runs the held discovery's Rescan; its queue goes straight to the
// run, the ticket being primed.
func (s *Session) rescan(o *Output) {
	r := s.run
	nr := r.root
	r.root, r.authed = nil, true
	r.discoverSeq = s.seq()
	s.setPhase(o, PhaseDiscovering, nr.Busy)
	o.effect(Discover{Seq: r.discoverSeq, Action: r.action, Run: nr.Rescan, Env: s.env})
}

// polkitStep reports whether st runs the root helper through pkexec: under
// AuthPolkit, outside a dry run, a child with a Verb.
func (s *Session) polkitStep(st steps.Step) bool {
	return s.opts.Auth == AuthPolkit && !s.opts.DryRun && st.Verb != "" && st.Run == nil
}

// privilegeGranted ends the running step's polkit wait.
func (s *Session) privilegeGranted(o *Output, in PrivilegeGranted) {
	if s.current(in.Seq) == nil || !s.run.polkitWait {
		return
	}
	s.resolvePolkit(o, AuthResolved{Granted: true})
}

// endPolkitWait resolves a polkit wait the step's exit ends: granted unless
// the backend says it never got privilege.
func (s *Session) endPolkitWait(o *Output, notGranted bool) {
	if !s.run.polkitWait {
		return
	}
	s.resolvePolkit(o, AuthResolved{Granted: !notGranted})
}
