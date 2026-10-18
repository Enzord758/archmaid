package stub

import (
	"fmt"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// builder writes a script on a virtual timeline, so the times inside the
// events (Started, At, Elapsed, Now) agree with the cue delays.
type builder struct {
	now     time.Time
	started time.Time
	pending time.Duration
	script  Script
	plan    []engine.PlanStep
	at      map[string]time.Time // step ID → when it started
	skipped int                  // steps declined so far

	branches bool // give the prompts their OnNo branches
	lastID   engine.PromptID
}

// id is the next prompt ID of this path of the run.
func (b *builder) id() engine.PromptID {
	b.lastID++
	return b.lastID
}

// branch is a copy of b to write an answer's branch on: same moment, same
// plan, and an empty script.
func (b *builder) branch() *builder {
	c := *b
	c.script = nil
	c.pending = 0
	c.plan = append([]engine.PlanStep(nil), b.plan...)
	c.at = map[string]time.Time{}
	for k, v := range b.at {
		c.at[k] = v
	}
	return &c
}

// decline gives the prompt just emitted an OnNo branch (when b writes
// branches): the step it gates is Skipped and logged, and rest writes
// what follows. The Optional steps are the ones a "no" lets the run go on
// without.
func (b *builder) decline(step string, cat logstore.Category, header string, rest func(*builder)) {
	if !b.branches {
		return
	}
	sb := b.branch()
	sb.skip(step, cat, header)
	rest(sb)
	b.script[len(b.script)-1].OnNo = sb.script
}

// skip is a step that never runs: declined.
func (b *builder) skip(id string, cat logstore.Category, header string) {
	b.skipped++
	b.emit(engine.StepDone{ID: id, Index: b.index(id), Status: steps.Skipped})
	b.log(cat, header)
}

// finish ends the run.
func (b *builder) finish(action string, outcome engine.Outcome) {
	b.emit(engine.Finished{Action: action, Outcome: outcome, Skipped: b.skipped, Elapsed: b.now.Sub(b.started)},
		engine.PhaseChanged{Phase: engine.PhaseIdle})
}

func (b *builder) wait(d time.Duration) {
	b.pending += d
	b.now = b.now.Add(d)
}

func (b *builder) emit(evs ...engine.Event) {
	for _, ev := range evs {
		b.script = append(b.script, Cue{After: b.pending, Event: ev})
		b.pending = 0
	}
}

func (b *builder) index(id string) int {
	for i, s := range b.plan {
		if s.ID == id {
			return i
		}
	}
	panic("stub: no step " + id)
}

func (b *builder) start(id string) {
	b.at[id] = b.now
	b.emit(engine.StepStarted{ID: id, Index: b.index(id), At: b.now})
}

func (b *builder) out(id string, lines ...string) {
	b.emit(engine.OutputChunk{ID: id, Index: b.index(id), Lines: lines})
}

func (b *builder) done(id string, st steps.Status) {
	b.emit(engine.StepDone{ID: id, Index: b.index(id), Status: st, Elapsed: b.now.Sub(b.at[id])})
}

func (b *builder) tick(step string) {
	b.emit(engine.StatusUpdated{
		Now:         b.now,
		Status:      sysstat.Status{Free: "212G", RAM: "41G", Cache: "3.1G"},
		Elapsed:     b.now.Sub(b.started),
		StepElapsed: b.now.Sub(b.at[step]),
		Step:        step,
	})
}

// run is a whole step that succeeds after d, printing lines.
func (b *builder) run(id string, d time.Duration, lines ...string) {
	b.start(id)
	b.wait(d)
	if len(lines) > 0 {
		b.out(id, lines...)
	}
	b.done(id, steps.OK)
}

func (b *builder) log(cat logstore.Category, header string) {
	b.emit(engine.LogWritten{Category: cat, Header: header})
}

// FullCleanup is a Full Cleanup with a cached sudo ticket, shaped like the
// fullcleanup_orphans_cascade_acceptall transcript: the gate, ten planned
// steps, the "delete entire cache" confirm, the orphan probe inserting the
// removal behind itself with its cascade confirm, and a clean finish. Its
// prompts are IDs 1 (gate), 2 (paccache -rk0) and 3 (orphans); it runs
// about 9 s of scripted time from start. Every prompt is answered yes, and
// a "no" plays on like one; FullCleanupBranches is the script that follows
// the answers.
func FullCleanup(start time.Time) Script { return fullCleanup(start, false) }

// FullCleanupBranches is FullCleanup with its prompts' branches, shaped like
// the fullcleanup_gate_declined and fullcleanup_decline_optionals
// transcripts. A no (or a cancel) at the gate ends the run cancelled, before
// any step. A no at the "delete entire cache" confirm skips that step, and a
// no at the orphans confirm skips the removal (Finished{Skipped: 1} or 2):
// either way the run goes on to the end.
func FullCleanupBranches(start time.Time) Script { return fullCleanup(start, true) }

// fcOpts are the variations of a Full Cleanup script.
type fcOpts struct {
	branches bool // the prompts' OnNo branches
	uncached bool // the sudo ticket isn't cached: the password is asked first
	askpass  bool // topgrade's sudo asks for the password mid-step
}

// FullCleanupUncached is Full Cleanup where the sudo probe found no ticket:
// it opens with NeedsPassword{UpFront} carrying the gate text and
// AttemptsLeft 3 (prompt 1), which stands in for the gate, then runs like
// FullCleanupBranches with its confirms (IDs from 2).
//
// The stub can't check a password: the Secret "wrong" (WrongPassword) is
// wrong, anything else is right.
//   - Right: AuthWaiting{PasswordCheck}, AuthResolved{Granted}, and the run.
//   - Wrong: AuthWaiting, AuthResolved{AttemptsLeft: 2}, and a new
//     NeedsPassword (a new ID) whose Error is "Incorrect password — 2
//     attempts left"; the next wrong one says 1 attempt left. The third
//     wrong one gives up as the fullcleanup_three_wrong_passwords
//     transcript: AuthResolved{AttemptsLeft: 0}, then the queue runs with
//     every sudo step AuthRefused (no confirms asked, "sudo authentication
//     failed" in their logs) and Finished{Completed, Skipped: 7}.
//   - Cancel (or no): Finished{Cancelled}, before any step.
func FullCleanupUncached(start time.Time) Script {
	return fullCleanupWith(start, fcOpts{branches: true, uncached: true})
}

// FullCleanupAskpass is FullCleanupBranches where topgrade's sudo asks for
// the password mid-step: NeedsPassword{Kind: Askpass, Step: topgrade,
// Requester: "yay (pid 4242)", Prompt: "[sudo] password for user:"} as
// prompt 2 (the gate is 1). Any password is right, as the stub keeps no
// count of askpass attempts: AuthWaiting, AuthResolved{Granted} and the step
// goes on. A cancel hangs up and the step goes on too, with no event of
// its own, as the engine has it. Abort still withdraws the prompt.
func FullCleanupAskpass(start time.Time) Script {
	return fullCleanupWith(start, fcOpts{branches: true, askpass: true})
}

func fullCleanup(start time.Time, branches bool) Script {
	return fullCleanupWith(start, fcOpts{branches: branches})
}

func fullCleanupWith(start time.Time, o fcOpts) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}, branches: o.branches}
	b.plan = []engine.PlanStep{
		{ID: steps.IDAURAudit, Title: "Auditing AUR packages…", InProc: true, AlwaysLog: true, Category: logstore.Audit},
		{ID: steps.IDTopgrade, Title: "Updating system with topgrade…", Argv: []string{"topgrade", "-y", "--no-retry", "--disable", "hyprpm"}, NeedsPriv: true, AlwaysLog: true, Category: logstore.Topgrade},
		{ID: steps.IDPaccache, Verb: steps.VerbPaccache, Title: "Cleaning package cache…", Argv: []string{"sudo", "paccache", "-r"}, NeedsPriv: true, Category: logstore.Cleanup},
		{ID: steps.IDOrphansProbe, Verb: steps.VerbPlanOrphans, Title: "Finding orphaned packages…", InProc: true, Category: logstore.Orphans},
		{ID: steps.IDYayCache, Title: "Cleaning AUR cache…", Argv: []string{"yay", "-Sc", "--noconfirm"}, NeedsPriv: true, Category: logstore.Cleanup},
		{ID: steps.IDTmpfiles, Verb: steps.VerbCleanTmp, Title: "Cleaning old temp files…", Argv: []string{"sudo", "systemd-tmpfiles", "--clean"}, NeedsPriv: true, AlwaysLog: true, Category: logstore.Cleanup},
		{ID: steps.IDJournal, Verb: steps.VerbVacuumJournal, Title: "Vacuuming logs…", Argv: []string{"sudo", "journalctl", "--vacuum-time=2weeks"}, NeedsPriv: true, AlwaysLog: true, Category: logstore.Cleanup},
		{ID: steps.IDIntegrity, Verb: steps.VerbCheckFiles, Title: "Checking package integrity…", Argv: []string{"sudo", "pacman", "-Qkq"}, NeedsPriv: true, Category: logstore.Cleanup},
		{ID: steps.IDHyprpm, Title: "Updating Hyprland plugins…", Argv: []string{"hyprpm", "update"}, Category: logstore.Cleanup},
	}

	b.emit(engine.PhaseChanged{Phase: engine.PhaseProbing})
	b.wait(60 * time.Millisecond)
	const gate = "Proceed with full cleanup?"
	cancelled := func(c *builder) { c.finish("fullcleanup", engine.OutcomeCancelled) }
	if o.uncached {
		// The probe found no ticket: the password prompt is the gate.
		b.password(engine.NeedsPassword{Kind: engine.PasswordUpFront, Gate: gate, AttemptsLeft: 3},
			func(c *builder) { fcQueue(c, o) }, fcGone, cancelled)
		return b.script
	}
	b.emit(engine.NeedsConfirm{ID: b.id(), Kind: engine.ConfirmGate, Title: gate, Msg: gate,
		Plan: append([]engine.PlanStep(nil), b.plan...), YesLabel: "Proceed"})
	if o.branches {
		sb := b.branch()
		cancelled(sb)
		b.script[len(b.script)-1].OnNo = sb.script
	}
	fcQueue(b, o)
	return b.script
}

// password asks for a password (ev's ID is set here) and writes what
// follows each answer. A right one (any Secret but WrongPassword) goes on
// as the check's AuthWaiting and AuthResolved{Granted}, then rest writes
// the run on. WrongPassword is wrong, when giveUp is not nil: AuthResolved
// without a grant, and the prompt again with a new ID and its Error, until
// the attempts are gone and giveUp writes the end. A cancel (or a no) is
// onCancel's branch, when not nil.
func (b *builder) password(ev engine.NeedsPassword, rest, giveUp, onCancel func(*builder)) {
	ev.ID = b.id()
	b.emit(ev)
	at := len(b.script) - 1
	if onCancel != nil {
		cb := b.branch()
		onCancel(cb)
		b.script[at].OnNo = cb.script
	}
	if giveUp != nil {
		wb := b.branch()
		wb.wait(100 * time.Millisecond)
		wb.emit(engine.AuthWaiting{Kind: engine.WaitPasswordCheck})
		wb.wait(400 * time.Millisecond)
		left := ev.AttemptsLeft - 1
		wb.emit(engine.AuthResolved{AttemptsLeft: left})
		if left > 0 {
			s := "s"
			if left == 1 {
				s = ""
			}
			again := ev
			again.AttemptsLeft = left
			again.Error = fmt.Sprintf("Incorrect password — %d attempt%s left", left, s)
			wb.password(again, rest, giveUp, onCancel)
		} else {
			giveUp(wb)
		}
		b.script[at].OnWrong = wb.script
	}
	b.wait(100 * time.Millisecond)
	b.emit(engine.AuthWaiting{Kind: engine.WaitPasswordCheck})
	b.wait(400 * time.Millisecond)
	b.emit(engine.AuthResolved{Granted: true})
	rest(b)
}

// fcQueue writes the run from the queue's start to its end.
func fcQueue(b *builder, o fcOpts) {
	b.emit(engine.PhaseChanged{Phase: engine.PhaseRunning})
	b.emit(engine.QueueStarted{Action: "fullcleanup", Steps: append([]engine.PlanStep(nil), b.plan...), Started: b.now})
	fcAudit(b)

	b.start(steps.IDTopgrade)
	b.wait(800 * time.Millisecond)
	b.out(steps.IDTopgrade, ":: Synchronizing package databases...")
	if !o.askpass {
		fcTopgradeEnd(b)
		return
	}
	// sudo, run by yay, asks through the askpass helper. Cancelling hangs
	// up on it and the step goes on (the engine emits no PromptClosed for
	// a cancel: the answer closed the prompt). The stub takes any password
	// here, WrongPassword included.
	b.password(engine.NeedsPassword{
		Kind: engine.PasswordAskpass, Step: steps.IDTopgrade,
		Requester: "yay (pid 4242)", Prompt: "[sudo] password for user:", AttemptsLeft: 3,
	}, fcTopgradeEnd, nil, fcTopgradeEnd)
}

func fcAudit(b *builder) {
	b.run(steps.IDAURAudit, 400*time.Millisecond,
		"1 foreign package(s) checked against the AUR: 0 high, 0 glance, 1 clean", "", "Nothing flagged.")
	b.log(logstore.Audit, "OK: AUR audit")
}

// fcTopgradeEnd writes the run from the middle of topgrade's output on.
func fcTopgradeEnd(b *builder) {
	b.wait(1200 * time.Millisecond)
	b.tick(steps.IDTopgrade)
	b.out(steps.IDTopgrade, " core is up to date", " extra is up to date", "\x1b[1;32m==>\x1b[0m Nothing to do.")
	b.wait(2 * time.Second)
	b.tick(steps.IDTopgrade)
	b.out(steps.IDTopgrade, "── Summary ──", "System update: OK")
	b.done(steps.IDTopgrade, steps.OK)
	b.log(logstore.Topgrade, "OK: Topgrade completed")

	b.run(steps.IDPaccache, 300*time.Millisecond, "==> no candidate packages found for pruning")

	fcOrphans(b)
}

// refuse is a privileged step that never runs: sudo failed.
func (b *builder) refuse(id string, cat logstore.Category, label string) {
	b.skipped++
	b.emit(engine.StepDone{ID: id, Index: b.index(id), Status: steps.AuthRefused})
	b.log(cat, "SKIP: "+label+" (sudo authentication failed)")
}

// fcGone writes the run after three wrong passwords, as the
// fullcleanup_three_wrong_passwords transcript has it: the queue runs, but
// every sudo step is AuthRefused, with no confirm asked.
func fcGone(b *builder) {
	b.emit(engine.PhaseChanged{Phase: engine.PhaseRunning})
	b.emit(engine.QueueStarted{Action: "fullcleanup", Steps: append([]engine.PlanStep(nil), b.plan...), Started: b.now})
	fcAudit(b)
	b.refuse(steps.IDTopgrade, logstore.Topgrade, "Topgrade completed")
	b.refuse(steps.IDPaccache, logstore.Cleanup, "Old package cache cleaned")
	b.run(steps.IDOrphansProbe, 250*time.Millisecond, orphanLines...)
	b.insertRemoval()
	b.refuse(steps.IDOrphansRemove, logstore.Orphans, "Orphaned packages removed")
	b.run(steps.IDYayCache, 300*time.Millisecond)
	b.refuse(steps.IDTmpfiles, logstore.Cleanup, "Temp files cleaned per tmpfiles.d rules")
	b.refuse(steps.IDJournal, logstore.Cleanup, "Old logs removed")
	b.refuse(steps.IDIntegrity, logstore.Cleanup, "Package integrity check completed")
	b.run(steps.IDHyprpm, 1200*time.Millisecond, "✔ all plugins are up to date")
	b.finish("fullcleanup", engine.OutcomeCompleted)
}

// fcOrphans writes the run from the orphan probe on.
func fcOrphans(b *builder) {
	b.run(steps.IDOrphansProbe, 250*time.Millisecond, orphanLines...)
	b.insertRemoval()
	b.emit(engine.NeedsConfirm{
		ID: b.id(), Kind: engine.ConfirmOrphans, Step: steps.IDOrphansRemove,
		Title: "Remove 2 orphaned packages?", Body: "Their now-unneeded dependencies go with them: 3 packages in total.",
		Direct: []string{"libfoo", "libbar"}, Cascade: []string{"libfoo", "libbar", "libbaz"},
		Destructive: true, Msg: "Remove 2 orphaned package(s) — 3 total once dependencies cascade?", YesLabel: "Remove",
	})
	b.decline(steps.IDOrphansRemove, logstore.Orphans, "SKIP: Orphaned packages removed (declined)", fcRest)
	b.run(steps.IDOrphansRemove, 700*time.Millisecond,
		"checking dependencies...", "removing libbaz...", "removing libbar...", "removing libfoo...")
	b.log(logstore.Orphans, "OK: Orphaned packages removed")
	fcRest(b)
}

// fcRest writes the run from the AUR cache step to its end.
func fcRest(b *builder) {
	b.run(steps.IDYayCache, 300*time.Millisecond)
	b.run(steps.IDTmpfiles, 150*time.Millisecond)
	b.log(logstore.Cleanup, "OK: Temp files cleaned per tmpfiles.d rules")
	b.run(steps.IDJournal, 200*time.Millisecond, "Vacuuming done, freed 0B of archived journals from /var/log/journal.")
	b.log(logstore.Cleanup, "OK: Old logs removed")
	b.run(steps.IDIntegrity, 900*time.Millisecond)
	b.run(steps.IDHyprpm, 1200*time.Millisecond, "✔ all plugins are up to date")

	b.finish("fullcleanup", engine.OutcomeCompleted)
}

var orphanLines = []string{"2 orphaned package(s): libfoo libbar", "pacman -Rns would remove 3 package(s):", "  libfoo", "  libbar", "  libbaz"}

// insertRemoval is the orphan probe's result: the removal step, put in
// behind the probe.
func (b *builder) insertRemoval() {
	removal := engine.PlanStep{
		ID: steps.IDOrphansRemove, Verb: steps.VerbRemoveOrphans, Title: "Removing orphaned packages…",
		Argv:      []string{"sudo", "pacman", "-Rns", "--noconfirm", "--", "libfoo", "libbar"},
		NeedsPriv: true, Confirm: true, Optional: true, AlwaysLog: true, Category: logstore.Orphans,
	}
	b.plan = append(b.plan[:4], append([]engine.PlanStep{removal}, b.plan[4:]...)...)
	b.emit(engine.StepInserted{Index: 4, Step: removal})
}
