package stub

import (
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	runreport "github.com/Enzord758/archmaid/internal/report"
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
	if req.AUR && req.Mode == steps.PkgInstall {
		if len(req.Names) > 0 && strings.HasPrefix(req.Names[0], "risky-") {
			return "aur-high"
		}
		return "aur"
	}
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
//	packages:aur              an AUR install: the sheet with the review of one base ("paru-bin",
//	                          a repo dependency first), then the three steps and the run
//	packages:aur-high         the same for a request whose first name starts with "risky-": the
//	                          audit has a high finding, which the sheet makes the user accept
//	packages:stale            the sheet, then the run's step fails and PackagesStale follows
//	packages:plain-error      a DiscoveryResult{Failed}, Notice, Finished{OutcomeNotice}
func PackagesCatalog(start time.Time) map[string]Script {
	m := map[string]Script{
		"packages:install":  packagesRun(start, steps.PkgInstall, false),
		"packages:remove":   packagesRun(start, steps.PkgRemove, false),
		"packages:stale":    packagesRun(start, steps.PkgInstall, true),
		"packages:aur":      packagesAURRun(start, false),
		"packages:aur-high": packagesAURRun(start, true),
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
	id, verb, title := steps.IDPkgInstall, steps.VerbPkgInstall, "Installing packages…"
	yes, label := "Install", "Packages installed"
	if mode == steps.PkgRemove {
		id, verb, title = steps.IDPkgRemove, steps.VerbPkgRemove, "Removing packages…"
		yes, label = "Remove", "Packages removed"
	}
	b.plan = []engine.PlanStep{{ID: id, Verb: verb, Title: title, LogLabel: label,
		NeedsPriv: true, AlwaysLog: true, Category: logstore.Packages}}
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
		b.log(logstore.Packages, "FAIL: "+label)
		b.emit(engine.PackagesStale{Plan: &p})
		b.emit(engine.Finished{Action: actions.IDPackages, Outcome: engine.OutcomeCompleted, Errs: 1,
			Elapsed: b.now.Sub(b.started)}, engine.PhaseChanged{Phase: engine.PhaseIdle})
		return b.script
	}
	b.wait(1500 * time.Millisecond)
	b.out(id, "resolving dependencies...", "checking for conflicts...", "(3/3) installing foo")
	b.done(id, steps.OK)
	b.log(logstore.Packages, "OK: "+label)
	rep := runreport.Report{Action: actions.IDPackages, Started: start, Finished: b.now, Elapsed: b.now.Sub(b.started), Steps: runreport.StepTally{Done: 1}}
	if mode == steps.PkgRemove {
		rep.Packages = &runreport.Packages{Removed: []runreport.Pkg{{Name: "foo", Old: "1.2-1"}, {Name: "libfoo", Old: "1.2-1"}}}
	} else {
		rep.Packages = &runreport.Packages{Installed: []runreport.Pkg{{Name: "foo", New: "1.2-1"}, {Name: "libfoo", New: "4.0-2"}, {Name: "libbar", New: "0.9-1"}}}
	}
	b.emit(engine.RunReport{Report: rep})
	b.finish(actions.IDPackages, engine.OutcomeCompleted)
	return b.script
}

// aurPlan is the AUR install the sheet reviews: one base with a PKGBUILD,
// an .install script, a patch, sources (one without a checksum) and a hint,
// a diff from the last reviewed commit, and a repository dependency in Rows.
// high adds a high audit finding next to the low one.
func aurPlan(high bool) steps.PkgPlan {
	name := "paru-bin"
	if high {
		name = "risky-bin"
	}
	audit := []steps.AURFinding{{Name: name, Reason: "no maintainer change in 90 days"}}
	if high {
		audit = []steps.AURFinding{{Name: name, High: true, Reason: "the maintainer changed since the last audit"}, audit[0]}
	}
	return steps.PkgPlan{Mode: steps.PkgInstall, AsDeps: true, Targets: []string{name}, Hash: stubHash,
		Rows: []steps.PkgRow{{Name: "libgit2", Version: "1.8.4-1", Repo: "extra", Target: true, Download: 600 << 10, Installed: 3 << 20}},
		AUR: []steps.AURPlan{{
			Base: name, Names: []string{name}, Version: "2.0.4-1", Commit: "0123456789abcdef0123456789abcdef01234567", Prev: "fedcba9876543210fedcba9876543210fedcba98",
			Files: []steps.AURFile{
				{Path: "PKGBUILD", Kind: steps.AURFilePKGBUILD, Size: 310, Text: "pkgname=" + name + "\npkgver=2.0.4\npkgrel=1\narch=('x86_64')\ndepends=('libgit2')\nsource=(\"https://example.org/" + name + "-2.0.4.tar.gz\" 'fix.patch')\nsha256sums=('0000000000000000000000000000000000000000000000000000000000000000' 'SKIP')\ninstall=" + name + ".install\n"},
				{Path: name + ".install", Kind: steps.AURFileInstall, Size: 60, Text: "post_install() {\n  echo 'installed'\n}\n"},
				{Path: "fix.patch", Kind: steps.AURFilePatch, Size: 120, Text: "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n"},
				{Path: "icon.png", Kind: steps.AURFileOther, Size: 9000, Note: "binary"}},
			Sources: []steps.AURSource{{URL: "https://example.org/" + name + "-2.0.4.tar.gz", Checksum: true}, {URL: "fix.patch"}},
			Diff:    "@@ -1,3 +1,3 @@\n-pkgver=2.0.3\n+pkgver=2.0.4\n pkgrel=1\n",
			Flags:   []string{"has an .install script: it runs as root at install", "a source has no checksum (SKIP)"},
			Audit:   audit, RepoDeps: []string{"libgit2"},
		}}}
}

// packagesAURRun is an AUR install: the sheet with the review, then (on a
// yes) the dependencies step, the user's build and the root install. With
// high the audit has a high finding (the sheet then makes the user accept
// it; the stub has nothing to commit).
func packagesAURRun(start time.Time, high bool) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}, branches: true}
	p := aurPlan(high)
	base := p.AUR[0].Base
	build := steps.IDAURBuildPrefix + base
	b.plan = []engine.PlanStep{
		{ID: steps.IDPkgInstall, Verb: steps.VerbPkgInstall, Title: "Installing dependencies…", LogLabel: "Dependencies installed", NeedsPriv: true, AlwaysLog: true, Category: logstore.Packages},
		{ID: build, Title: "Building " + base + "…", LogLabel: "Built " + base, Category: logstore.Packages},
		{ID: steps.IDPkgInstallLocal, Verb: steps.VerbPkgInstallLocal, Title: "Installing " + base + "…", LogLabel: "Installed " + base, NeedsPriv: true, AlwaysLog: true, Category: logstore.Packages},
	}
	b.emit(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Checking the packages…"})
	b.wait(600 * time.Millisecond)
	pid := b.id()
	title := "Build and install 1 package from the AUR?"
	b.emit(engine.PackagesPreview{Prompt: pid, Plan: p})
	b.emit(engine.NeedsConfirm{ID: pid, Kind: engine.ConfirmPackages, Title: title, Msg: title,
		Items: p.Targets, YesLabel: "Build and install"})
	sb := b.branch()
	sb.finish(actions.IDPackages, engine.OutcomeCancelled)
	b.script[len(b.script)-1].OnNo = sb.script

	b.emit(engine.PhaseChanged{Phase: engine.PhaseRunning})
	b.emit(engine.QueueStarted{Action: actions.IDPackages, Steps: append([]engine.PlanStep(nil), b.plan...), Started: b.now})
	b.start(steps.IDPkgInstall)
	b.emit(engine.AuthWaiting{Kind: engine.WaitPolkit, Step: steps.IDPkgInstall})
	b.wait(800 * time.Millisecond)
	b.emit(engine.AuthResolved{Granted: true})
	b.wait(time.Second)
	b.out(steps.IDPkgInstall, "resolving dependencies...", "(1/1) installing libgit2")
	b.done(steps.IDPkgInstall, steps.OK)
	b.log(logstore.Packages, "OK: Dependencies installed")
	b.start(build)
	b.wait(1500 * time.Millisecond)
	b.out(build, "==> Making package: "+base+" 2.0.4-1", "==> Finished making: "+base+" 2.0.4-1")
	b.done(build, steps.OK)
	b.log(logstore.Packages, "OK: Built "+base)
	b.start(steps.IDPkgInstallLocal)
	b.emit(engine.AuthWaiting{Kind: engine.WaitPolkit, Step: steps.IDPkgInstallLocal})
	b.wait(800 * time.Millisecond)
	b.emit(engine.AuthResolved{Granted: true})
	b.wait(time.Second)
	b.out(steps.IDPkgInstallLocal, "loading packages...", "(1/1) installing "+base)
	b.done(steps.IDPkgInstallLocal, steps.OK)
	b.log(logstore.Packages, "OK: Installed "+base)
	b.emit(engine.RunReport{Report: runreport.Report{Action: actions.IDPackages, Started: start, Finished: b.now, Elapsed: b.now.Sub(b.started), Steps: runreport.StepTally{Done: 3},
		Packages: &runreport.Packages{Installed: []runreport.Pkg{{Name: "libgit2", New: "1.8.4-1"}, {Name: base, New: "2.0.4-1"}}}}})
	b.finish(actions.IDPackages, engine.OutcomeCompleted)
	return b.script
}
