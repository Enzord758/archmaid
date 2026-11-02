package ui

// The Packages page's live-check fixes (task #244, findings B3..B10) on the
// real widgets: greyed protected packages, the page following the databases,
// the sheet's password note, Stop, the dry-run status, the polkit dialog's
// message and the one-click "Clear the list and add this".

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

// liveChanged is what the fake Reload answers: the databases changed.
var liveChanged atomic.Bool

// liveWiring gives the live scenario what the real app has: the helper's
// protected list (htop is on it, which the built-in list does not know),
// a Reload that follows liveChanged, and a Driver that keeps the password.
func (e *pkgEnv) liveWiring(o *Options) {
	o.KeepsPassword = true
	o.Packages.Protected = func(context.Context) ([]string, error) { return []string{"htop", "glibc"}, nil }
	o.Packages.Reload = func() bool { return liveChanged.Swap(false) }
}

func aurPlanForLive() steps.PkgPlan {
	return steps.PkgPlan{Targets: []string{"paru-bin"}, Hash: "sha256:" + strings.Repeat("d", 64),
		Rows: []steps.PkgRow{{Name: "libgit2", Version: "1.8.4-1", Repo: "extra", Target: true, Download: 600 << 10, Installed: 3 << 20}},
		AUR: []steps.AURPlan{{Base: "paru-bin", Names: []string{"paru-bin"}, Version: "2.0.4-1", Commit: "0123456789abcdef",
			Files: []steps.AURFile{{Path: "PKGBUILD", Kind: steps.AURFilePKGBUILD, Size: 30, Text: "pkgname=paru-bin\n"}}}}}
}

func pkgLive(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	pkgSeq(c,
		func() { // B4: the helper's list greys htop, with the reason
			p.modeInstalled.SetActive(true)
		},
		func() {
			row := rowByName(p.instList, "htop")
			c.ok(row != nil, "htop is not in the installed view")
			if row != nil {
				b := findTooltip(gtk.BaseWidget(row), pkgview.ProtectedReason)
				c.ok(b != nil && !b.(*gtk.Button).Sensitive(), "htop should be greyed with %q", pkgview.ProtectedReason)
			}
			vim := rowByName(p.instList, "vim")
			c.ok(vim != nil && findTooltip(gtk.BaseWidget(vim), "Add to the removal list") != nil, "vim must stay removable")
			p.modeSearch.SetActive(true)
		},
		func() { // B5: pacman installs gvim behind the page's back; showing the page re-reads
			e.typeQuery(p, "gvim")
			e.advance(120)
		},
		func() {
			e.advance(400)
		},
		func() {
			hit, _ := e.cat.Lookup("gvim")
			p.selectPkg(hit)
		},
		func() {
			c.ok(findButton(gtk.BaseWidget(p.detail), "Install") != nil, "gvim should offer Install first:\n%s", pageText(p))
			for i := range e.cat.pkgs {
				if e.cat.pkgs[i].Name == "gvim" {
					e.cat.pkgs[i].Installed, e.cat.pkgs[i].InstalledVersion, e.cat.pkgs[i].Reason = true, e.cat.pkgs[i].Version, pkgdb.ReasonExplicit
				}
			}
			liveChanged.Store(true)
			w.stack.SetVisibleChildName("overview")
			w.stack.SetVisibleChildName("packages")
		},
		func() {
			c.ok(findButton(gtk.BaseWidget(p.detail), "Remove") != nil && findButton(gtk.BaseWidget(p.detail), "Install") == nil,
				"the open detail did not follow the database:\n%s", pageText(p))
			c.ok(p.selected == "gvim", "the selection moved to %q", p.selected)
			shoot(w, "cov-pkg-live", "reloaded-detail")
		},
		func() { // B10: an AUR entry blocks a repo package; one click starts over
			paru := e.aur.info["paru-bin"]
			c.ok(p.ctl.Add(paru, pkgview.ModeInstall) == nil, "paru-bin did not join the list")
			e.typeQuery(p, "firefox")
			e.advance(120)
		},
		func() { e.advance(400) },
		func() {
			why := "AUR packages are built and installed in their own run: review or clear the list first. Press to clear the list and add this."
			ff := rowByName(p.results, "firefox")
			var b gtk.Widgetter
			if ff != nil {
				b = findTooltip(gtk.BaseWidget(ff), why)
			}
			c.ok(b != nil && b.(*gtk.Button).Sensitive(), "firefox's add button should offer to clear the list")
			if b == nil {
				return
			}
			n := len(w.toasted)
			b.(*gtk.Button).Emit("clicked")
			c.ok(len(w.toasted) == n+1 && strings.HasPrefix(w.toasted[n], "AUR packages are built"), "the refusal was not said: %q", w.toasted[n:])
			// The detail's own button does the same.
			ffp, _ := e.cat.Lookup("firefox")
			p.selectPkg(ffp)
		},
		func() {
			clr := findButton(gtk.BaseWidget(p.detail), "Clear the list and add this")
			c.ok(clr != nil, "the detail has no Clear the list and add this:\n%s", pageText(p))
			if clr != nil {
				clr.Emit("clicked")
			}
		},
		func() {
			c.ok(strings.Join(p.ctl.List.Names(), ",") == "firefox", "the list is %v, want just firefox", p.ctl.List.Names())
			p.trayClear.Emit("clicked")
		},
		func() { // B7: the sheet says what happens with the kept password
			paru := e.aur.info["paru-bin"]
			c.ok(p.ctl.Now(paru, pkgview.ModeInstall) == nil, "Now")
			w.current = actions.IDPackages
			w.setBusy(true) // the window's own part of startPackages
		},
		func() {
			c.ok(p.sheet != nil, "no planning sheet")
			c.ok(!w.stopBtn.IsVisible(), "Stop shows while the sheet is up (planning)")
			p.ctl.Handle(pkgview.Preview{Prompt: 2, Plan: aurPlanForLive()})
		},
		func() {
			txt := dialogLabels(p.sheet, nil)
			c.ok(strings.Contains(txt, "Two root steps (dependencies, then the AUR). You're asked for your password once"), "sheet lacks the kept-password note:\n%s", txt)
			c.ok(!strings.Contains(txt, "2 password prompts"), "the old note is still there:\n%s", txt)
			c.ok(!w.stopBtn.IsVisible(), "Stop shows while the sheet is up (review)")
			shoot(w, "cov-pkg-live", "sheet-password-kept")
			p.ctl.Dismiss()
		},
		func() { // B8: a dry run asks for no password
			c.ok(p.sheet == nil, "the sheet stays up after Cancel")
			w.drvDry = true
			c.ok(p.ctl.Now(e.aur.info["paru-bin"], pkgview.ModeInstall) == nil, "Now")
		},
		func() {
			p.ctl.Handle(pkgview.Preview{Prompt: 3, Plan: aurPlanForLive()})
		},
		func() {
			txt := dialogLabels(p.sheet, nil)
			c.ok(strings.Contains(txt, pkgview.DryNoPassword) && !strings.Contains(txt, "asked for your password"), "the dry sheet still talks of passwords:\n%s", txt)
			shoot(w, "cov-pkg-live", "sheet-password-dry")
			w.drvDry = false
			p.ctl.Dismiss()
		},
		func() { // B9: the sheet is gone, Stop is back; the root step hides it again
			c.ok(w.stopBtn.IsVisible(), "Stop did not come back once the sheet was closed")
			w.model.Rows = []runview.Row{{ID: steps.IDPkgInstallLocal, State: runview.RowRunning, NeedsPriv: true}}
			w.updateStop()
			c.ok(!w.stopBtn.IsVisible(), "Stop shows while the root helper step runs")
			w.model.Rows[0].Waiting = true // polkit's dialog is up: pkexec can still be stopped
			w.updateStop()
			c.ok(w.stopBtn.IsVisible(), "Stop should work while polkit's dialog is up")
			w.model.Rows = nil
			w.setBusy(false)
			w.current = ""
			// A prompt of another action keeps Stop, as before.
			w.current = actions.IDTopgrade
			w.setBusy(true)
			w.model.Prompt = &runview.Prompt{}
			w.updateStop()
			c.ok(w.stopBtn.IsVisible(), "Stop must not change for other actions")
			w.model.Prompt = nil
			w.setBusy(false)
		},
		func() { // B9: DRY RUN leaves the status when dry run is switched off
			w.model.DryRun = true
			w.set.dryRun.SetActive(true)
			c.ok(strings.Contains(w.statusText(), "DRY RUN"), "status %q lacks DRY RUN", w.statusText())
			w.set.dryRun.SetActive(false)
			c.ok(!strings.Contains(w.statusText(), "DRY RUN") && !strings.Contains(w.status.Text(), "DRY RUN"),
				"status %q still says DRY RUN after the switch was turned off", w.status.Text())
		},
		func() { // #248: a real request after a dry run does not say DRY RUN while its sheet is prepared
			w.set.dryRun.SetActive(false)
			w.model.DryRun = true // the run before was a dry run
			w.startPackages(steps.PkgRequest{Names: []string{"vim"}})
			c.ok(!strings.Contains(w.statusText(), "DRY RUN"), "status %q says DRY RUN for a real request", w.statusText())
			w.setBusy(false)
			w.set.dryRun.SetActive(true)
			w.startPackages(steps.PkgRequest{Names: []string{"vim"}})
			c.ok(strings.Contains(w.statusText(), "DRY RUN"), "status %q lacks DRY RUN for a dry request", w.statusText())
			w.setBusy(false)
			w.set.dryRun.SetActive(false)
		},
		func() { // B9: the polkit dialog names what it authorises
			_, caller, _ := passwordText(engine.NeedsPassword{Kind: engine.PasswordAskpass, Polkit: true, Action: "org.archmaid.helper.pkg-install-local"})
			c.ok(strings.HasPrefix(caller, "Authentication is required to install packages built from the AUR"), "caller %q", caller)
		},
	)
}
