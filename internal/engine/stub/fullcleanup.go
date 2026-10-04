package stub

import (
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
// about 9 s of scripted time from start.
func FullCleanup(start time.Time) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}}
	b.plan = []engine.PlanStep{
		{ID: steps.IDAURAudit, Title: "Auditing AUR packages…", InProc: true, AlwaysLog: true, Category: logstore.Audit},
		{ID: steps.IDTopgrade, Title: "Updating system with topgrade…", Argv: []string{"topgrade", "-y", "--disable", "hyprpm"}, NeedsPriv: true, AlwaysLog: true, Category: logstore.Topgrade},
		{ID: steps.IDPaccache, Verb: steps.VerbPaccache, Title: "Cleaning package cache…", Argv: []string{"sudo", "paccache", "-r"}, NeedsPriv: true, Category: logstore.Cleanup},
		{ID: steps.IDPaccacheWipe, Verb: steps.VerbPaccacheWipe, Title: "Deleting entire package cache…", Argv: []string{"sudo", "paccache", "-rk0"}, NeedsPriv: true, Confirm: true, Optional: true, AlwaysLog: true, Category: logstore.Cleanup},
		{ID: steps.IDOrphansProbe, Verb: steps.VerbPlanOrphans, Title: "Finding orphaned packages…", InProc: true, Category: logstore.Orphans},
		{ID: steps.IDYayCache, Title: "Cleaning AUR cache…", Argv: []string{"yay", "-Sc", "--noconfirm"}, Category: logstore.Cleanup},
		{ID: steps.IDTmpfiles, Verb: steps.VerbCleanTmp, Title: "Cleaning old temp files…", Argv: []string{"sudo", "systemd-tmpfiles", "--clean"}, NeedsPriv: true, AlwaysLog: true, Category: logstore.Cleanup},
		{ID: steps.IDJournal, Verb: steps.VerbVacuumJournal, Title: "Vacuuming logs…", Argv: []string{"sudo", "journalctl", "--vacuum-time=2weeks"}, NeedsPriv: true, AlwaysLog: true, Category: logstore.Cleanup},
		{ID: steps.IDIntegrity, Verb: steps.VerbCheckFiles, Title: "Checking package integrity…", Argv: []string{"sudo", "pacman", "-Qkq"}, NeedsPriv: true, Category: logstore.Cleanup},
		{ID: steps.IDHyprpm, Title: "Updating Hyprland plugins…", Argv: []string{"hyprpm", "update"}, Category: logstore.Cleanup},
	}
	removal := engine.PlanStep{
		ID: steps.IDOrphansRemove, Verb: steps.VerbRemoveOrphans, Title: "Removing orphaned packages…",
		Argv:      []string{"sudo", "pacman", "-Rns", "--noconfirm", "--", "libfoo", "libbar"},
		NeedsPriv: true, Confirm: true, Optional: true, AlwaysLog: true, Category: logstore.Orphans,
	}

	b.emit(engine.PhaseChanged{Phase: engine.PhaseProbing})
	b.wait(60 * time.Millisecond)
	const gate = "Proceed with full cleanup?"
	b.emit(engine.NeedsConfirm{ID: 1, Kind: engine.ConfirmGate, Title: gate, Msg: gate})

	b.emit(engine.PhaseChanged{Phase: engine.PhaseRunning})
	b.emit(engine.QueueStarted{Action: "fullcleanup", Steps: append([]engine.PlanStep(nil), b.plan...), Started: b.now})

	b.run(steps.IDAURAudit, 400*time.Millisecond,
		"1 foreign package(s) checked against the AUR: 0 high, 0 glance, 1 clean", "", "Nothing flagged.")
	b.log(logstore.Audit, "OK: AUR audit")

	b.start(steps.IDTopgrade)
	b.wait(800 * time.Millisecond)
	b.out(steps.IDTopgrade, ":: Synchronizing package databases...")
	b.wait(1200 * time.Millisecond)
	b.tick(steps.IDTopgrade)
	b.out(steps.IDTopgrade, " core is up to date", " extra is up to date", "\x1b[1;32m==>\x1b[0m Nothing to do.")
	b.wait(2 * time.Second)
	b.tick(steps.IDTopgrade)
	b.out(steps.IDTopgrade, "── Summary ──", "System update: OK")
	b.done(steps.IDTopgrade, steps.OK)
	b.log(logstore.Topgrade, "OK: Topgrade completed")

	b.run(steps.IDPaccache, 300*time.Millisecond, "==> no candidate packages found for pruning")

	const wipe = "Delete ENTIRE package cache? (can't reinstall without internet)"
	b.emit(engine.NeedsConfirm{
		ID: 2, Kind: engine.ConfirmStep, Step: steps.IDPaccacheWipe,
		Title: "Delete the entire package cache?", Body: "Without it, packages can't be reinstalled offline.",
		Destructive: true, Msg: wipe,
	})
	b.run(steps.IDPaccacheWipe, 200*time.Millisecond)
	b.log(logstore.Cleanup, "OK: Entire package cache removed")

	b.run(steps.IDOrphansProbe, 250*time.Millisecond,
		"2 orphaned package(s): libfoo libbar", "pacman -Rns would remove 3 package(s):", "  libfoo", "  libbar", "  libbaz")
	b.plan = append(b.plan[:5], append([]engine.PlanStep{removal}, b.plan[5:]...)...)
	b.emit(engine.StepInserted{Index: 5, Step: removal})
	b.emit(engine.NeedsConfirm{
		ID: 3, Kind: engine.ConfirmOrphans, Step: steps.IDOrphansRemove,
		Title: "Remove 2 orphaned packages?", Body: "Their now-unneeded dependencies go with them: 3 packages in total.",
		Direct: []string{"libfoo", "libbar"}, Cascade: []string{"libfoo", "libbar", "libbaz"},
		Destructive: true, Msg: "Remove 2 orphaned package(s) — 3 total once dependencies cascade?",
	})
	b.run(steps.IDOrphansRemove, 700*time.Millisecond,
		"checking dependencies...", "removing libbaz...", "removing libbar...", "removing libfoo...")
	b.log(logstore.Orphans, "OK: Orphaned packages removed")

	b.run(steps.IDYayCache, 300*time.Millisecond)
	b.run(steps.IDTmpfiles, 150*time.Millisecond)
	b.log(logstore.Cleanup, "OK: Temp files cleaned per tmpfiles.d rules")
	b.run(steps.IDJournal, 200*time.Millisecond, "Vacuuming done, freed 0B of archived journals from /var/log/journal.")
	b.log(logstore.Cleanup, "OK: Old logs removed")
	b.run(steps.IDIntegrity, 900*time.Millisecond)
	b.run(steps.IDHyprpm, 1200*time.Millisecond, "✔ all plugins are up to date")

	b.emit(engine.Finished{Action: "fullcleanup", Outcome: engine.OutcomeCompleted, Elapsed: b.now.Sub(b.started)})
	b.emit(engine.PhaseChanged{Phase: engine.PhaseIdle})
	return b.script
}
