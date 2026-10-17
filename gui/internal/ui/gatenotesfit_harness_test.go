package ui

// The gate's "Before you start" notes fit without scrolling (task #176,
// the VM round #173: the notes #172 lengthened ran below the dialog at the
// VM's window). The Full Cleanup gate as the VM shows it (10 steps), for
// polkit and sudo, the kept wording and the longer keep-off one, at the
// owner's window (the wide dialog, columns side by side) and at the VM's
// (636x754, the right half of its screen). At each, every note is drawn
// whole inside the middle's visible part, and nothing in the middle needs
// scrolling. With ARCHMAID_GUI_SHOTS: gate-notes-<size>-<case>.png.

import (
	"fmt"
	"testing"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

func init() {
	covScenarios["gate-notes-fit"] = gateNotesFit
	covOptions["gate-notes-fit"] = func(o *Options, _ *report) { o.KeepsPassword = true }
}

func TestGUIGateNotesFit(t *testing.T) { covChild(t, "gate-notes-fit") }

// vmGatePlan is the Full Cleanup plan the VM's gate listed.
func vmGatePlan() []engine.PlanStep {
	root := func(id, title string, confirm bool) engine.PlanStep {
		return engine.PlanStep{ID: id, Title: title, NeedsPriv: true, Verb: "full-cleanup", Confirm: confirm}
	}
	return []engine.PlanStep{
		{ID: steps.IDTopgrade, Title: "Updating everything with topgrade...", NeedsPriv: true},
		{ID: "refresh", Title: "Refreshing mirrors..."},
		{ID: steps.IDOrphansProbe, Title: "Finding orphaned packages..."},
		root("cache", "Cleaning package cache...", false),
		root("cache-all", "Deleting entire package cache...", true),
		root("cache-un", "Removing uninstalled packages from cache...", false),
		root("tmp", "Cleaning old temp files...", false),
		root("logs", "Vacuuming logs...", false),
		root("check", "Checking package integrity...", false),
		{ID: steps.IDAURAudit, Title: "Auditing AUR packages..."},
		{ID: "aur", Title: "Cleaning AUR cache..."},
	}
}

func gateNotesFit(c *covEnv) {
	w := c.w
	c.quiet()
	sizes := [][2]int{{1460, 930}, {636, 754}}
	type variant struct {
		name   string
		polkit bool
		keep   string
	}
	why := "the PATH check found something that could stand in for sudo"
	variants := []variant{{"polkit-kept", true, ""}, {"polkit-off", true, why}, {"sudo-kept", false, ""}, {"sudo-off", false, why}}
	size, next := 0, 0
	id := engine.PromptID(700)
	var step func()
	step = func() {
		if next == len(variants) {
			next = 0
			size++
			if size == len(sizes) {
				c.finish()
				return
			}
			w.win.SetDefaultSize(sizes[size][0], sizes[size][1])
			coreglib.TimeoutAdd(400, step)
			return
		}
		v := variants[next]
		w.opts.Stub = false
		w.opts.Engine.Auth = engine.AuthSudo
		if v.polkit {
			w.opts.Engine.Auth = engine.AuthPolkit
		}
		w.opts.KeepPreview = func() string { return v.keep }
		id++
		pid := id
		c.feed(engine.NeedsConfirm{ID: pid, Kind: engine.ConfirmGate, Title: "Proceed with full cleanup?", Msg: "Proceed with full cleanup?",
			Plan: vmGatePlan(), Default: true, YesLabel: "Proceed"})
		coreglib.TimeoutAdd(700, func() {
			d := w.dialogs[pid]
			if d == nil {
				c.fail("%s: no dialog", v.name)
				return
			}
			wd, ht := w.win.Width(), w.win.Height()
			sc, _ := d.parts[1].(*gtk.ScrolledWindow)
			if sc == nil {
				c.fail("%s: no middle", v.name)
				return
			}
			c.ok(!overflows(sc), "%s at %dx%d: the middle scrolls (%.0f of %.0f px shown): the notes don't fit", v.name, wd, ht, sc.VAdjustment().PageSize(), sc.VAdjustment().Upper())
			sb, sok := boundsIn(sc, w.win)
			n := 0
			eachWidget(d, func(x gtk.Widgetter) {
				if !gtk.BaseWidget(x).HasCSSClass("am-note") || !gtk.BaseWidget(x).IsDrawable() {
					return
				}
				n++
				if nb, ok := boundsIn(x, w.win); !ok || !sok || nb.y < sb.y-0.5 || nb.y+nb.h > sb.y+sb.h+0.5 {
					c.fail("%s at %dx%d: note %d (%+v) is not inside the middle (%+v)", v.name, wd, ht, n, nb, sb)
				}
			})
			c.ok(n >= 3, "%s at %dx%d: %d notes drawn", v.name, wd, ht, n)
			shoot(w, fmt.Sprintf("gate-notes-%dx%d", wd, ht), v.name)
			c.press(d, "no")
			next++
			coreglib.TimeoutAdd(150, step)
		})
	}
	w.win.SetDefaultSize(sizes[0][0], sizes[0][1])
	coreglib.TimeoutAdd(400, step)
}
