package stub

import (
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages page's scripts (docs/design/packages-contract.md, sections 3
// and 7), for Options.Scripts next to Catalog. A Driver sent
// engine.StartPackages plays the script PackagesKey(req) names: the
// request's first name when a script is keyed on it ("conflict",
// "protected", "stale", …), else the mode's own ("install" / "remove"), so a
// front end triggers each path by installing a package of that name.

// PackagesKey is the Scripts key a StartPackages request plays.
func PackagesKey(req steps.PkgRequest) string { return "packages:" + packagesName(req) }

func packagesName(req steps.PkgRequest) string {
	if len(req.Names) > 0 {
		switch req.Names[0] {
		case "conflict", "protected", "updates-pending", "stale", "not-found", "required-by", "busy", "plain-error":
			return req.Names[0]
		}
	}
	if req.Mode == steps.PkgRemove {
		return "remove"
	}
	return "install"
}

// PackagesCatalog is a script for every path of a Packages run:
//
//	packages:install          the sheet (prompt 1) for "foo" + two dependencies, then
//	                          the run; a no or cancel ends Finished{OutcomeCancelled}
//	packages:remove           the same for a removal, "-Rs" with one dependency
//	packages:conflict         PackagesRefused{RefuseConflict}, then Finished{OutcomeNotice}
//	packages:protected        PackagesRefused{RefuseProtected}
//	packages:not-found, required-by, busy   PackagesRefused with that reason
//	packages:updates-pending  PackagesUpdatesPending{Count: 7}
//	packages:stale            the sheet, then the run's step fails and PackagesStale follows
//	packages:plain-error      a DiscoveryResult{Failed}, Notice, Finished{OutcomeNotice}
func PackagesCatalog(start time.Time) map[string]Script {
	m := map[string]Script{
		"packages:install": packagesRun(start, steps.PkgInstall, false),
		"packages:remove":  packagesRun(start, steps.PkgRemove, false),
		"packages:stale":   packagesRun(start, steps.PkgInstall, true),
		"packages:updates-pending": packagesDiscoveryEnd(start, func(b *builder) {
			b.emit(engine.PackagesUpdatesPending{Count: 7})
		}),
		"packages:plain-error": packagesDiscoveryEnd(start, func(b *builder) {
			b.emit(engine.DiscoveryResult{Action: actions.IDPackages, Outcome: engine.DiscoveryFailed,
				Summary: "Couldn't plan the package change.", Err: "the root helper is not installed"},
				engine.Notice{Text: "Couldn't plan the package change. the root helper is not installed"})
		}),
	}
	for reason, detail := range map[steps.RefuseReason]string{
		steps.RefuseConflict:  "gvim replaces vim, which is installed; remove vim first",
		steps.RefuseProtected: "glibc is protected: removing it would break the system",
		steps.RefuseNotFound:  "no package named this in the repositories",
		steps.RefuseRequired:  "other packages still need it",
		steps.RefuseBusy:      "pacman is running",
	} {
		reason, detail := reason, detail
		m["packages:"+string(reason)] = packagesDiscoveryEnd(start, func(b *builder) {
			b.emit(engine.PackagesRefused{Refusal: steps.PkgRefusal{Reason: reason, Names: []string{"foo"}, Detail: detail}})
		})
	}
	return m
}

// packagesDiscoveryEnd is a Packages run that ends at its discovery: the
// helper's answer (emit), then Finished{OutcomeNotice}.
func packagesDiscoveryEnd(start time.Time, emit func(*builder)) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}, branches: true}
	b.emit(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Checking the packages…"})
	b.wait(300 * time.Millisecond)
	emit(b)
	b.finish(actions.IDPackages, engine.OutcomeNotice)
	return b.script
}

// packagesPlan is the plan the sheet shows.
func packagesPlan(mode steps.PkgMode) steps.PkgPlan {
	if mode == steps.PkgRemove {
		return steps.PkgPlan{Mode: steps.PkgRemove, Targets: []string{"foo"}, Hash: stubHash,
			Rows: []steps.PkgRow{
				{Name: "foo", Version: "1.2-1", Target: true, Installed: 48 << 20},
				{Name: "libfoo", Version: "1.2-1", Installed: 6 << 20},
			}}
	}
	return steps.PkgPlan{Mode: steps.PkgInstall, Targets: []string{"foo"}, Hash: stubHash,
		Rows: []steps.PkgRow{
			{Name: "foo", Version: "1.2-1", Repo: "extra", Target: true, Download: 3 << 20, Installed: 12 << 20},
			{Name: "libfoo", Version: "4.0-2", Repo: "core", Download: 1 << 20, Installed: 4 << 20},
			{Name: "libbar", Version: "0.9-1", Repo: "cachyos-v3", Download: 512 << 10, Installed: 2 << 20},
		}}
}

// stubHash is a well-formed plan hash the stub shows; it hashes nothing.
const stubHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// packagesRun is a Packages run that reaches the sheet (prompt 1) and, on a
// yes, runs the one root step. With stale the step ends Failed and
// PackagesStale follows (the run ends Completed with one failure).
func packagesRun(start time.Time, mode steps.PkgMode, stale bool) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}, branches: true}
	p := packagesPlan(mode)
	id, verb, title := steps.IDPkgInstall, steps.PkgVerbInstall, "Installing packages…"
	yes, label := "Install", "Packages installed"
	if mode == steps.PkgRemove {
		id, verb, title = steps.IDPkgRemove, steps.PkgVerbRemove, "Removing packages…"
		yes, label = "Remove", "Packages removed"
	}
	b.plan = []engine.PlanStep{{ID: id, Verb: verb, Title: title, LogLabel: label,
		NeedsPriv: true, AlwaysLog: true, Category: logstore.Cleanup}}
	b.emit(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Checking the packages…"})
	b.wait(400 * time.Millisecond)
	pid := b.id()
	b.emit(engine.PackagesPreview{Prompt: pid, Plan: p})
	b.emit(engine.NeedsConfirm{ID: pid, Kind: engine.ConfirmPackages, Title: title, Msg: title,
		Items: p.Targets, Destructive: mode == steps.PkgRemove, YesLabel: yes})
	sb := b.branch()
	sb.finish(actions.IDPackages, engine.OutcomeCancelled)
	b.script[len(b.script)-1].OnNo = sb.script

	b.emit(engine.PhaseChanged{Phase: engine.PhaseRunning})
	b.emit(engine.QueueStarted{Action: actions.IDPackages, Steps: append([]engine.PlanStep(nil), b.plan...), Started: b.now})
	b.start(id)
	b.emit(engine.AuthWaiting{Kind: engine.WaitPolkit, Step: id})
	b.wait(800 * time.Millisecond)
	b.emit(engine.AuthResolved{Granted: true})
	if stale {
		b.wait(300 * time.Millisecond)
		b.done(id, steps.Failed)
		b.log(logstore.Cleanup, "FAIL: "+label)
		b.emit(engine.PackagesStale{Plan: &p})
		b.emit(engine.Finished{Action: actions.IDPackages, Outcome: engine.OutcomeCompleted, Errs: 1,
			Elapsed: b.now.Sub(b.started)}, engine.PhaseChanged{Phase: engine.PhaseIdle})
		return b.script
	}
	b.wait(1500 * time.Millisecond)
	b.out(id, "resolving dependencies...", "checking for conflicts...", "(3/3) installing foo")
	b.done(id, steps.OK)
	b.log(logstore.Cleanup, "OK: "+label)
	b.finish(actions.IDPackages, engine.OutcomeCompleted)
	return b.script
}
