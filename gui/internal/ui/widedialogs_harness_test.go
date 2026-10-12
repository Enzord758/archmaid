package ui

// The decision dialogs in a wide window (the owner: "wider and less tall
// when there's space", task #95). At 1460x930 the real Full Cleanup gate
// is a two-column dialog: its 10 steps beside its notes, nothing to
// scroll, at least 800 px wide and shorter than the AdwAlertDialog it
// replaced; every other confirm is wider than before, and the password
// dialog grows past the 372 px libadwaita gave it. At 380 px everything
// still fits, stacked, with its buttons in view. Screenshots of each
// with ARCHMAID_GUI_SHOTS (real-wide-<width>-<moment>.png).

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// envWideW is real-wide's window width (the parent sets it); its height
// is wideH, or narrowH under 640 px.
const envWideW = "ARCHMAID_GUI_WIDE_W"

// wideH is real-wide's window height at a wide width: the owner's.
const wideH = 930

// wideSize is real-wide's window size.
func wideSize() (int, int) {
	wd, _ := strconv.Atoi(os.Getenv(envWideW))
	if wd < 640 {
		return wd, narrowH
	}
	return wd, wideH
}

// Before (d3b2b94, every prompt an AdwAlertDialog), measured by this
// test at 1460x930: the dialogs' widths and the gate's height (its sheet:
// 372x567, the plan's rail scrolling after 180 px). The new dialogs must
// be wider (the gate shorter too); the password dialog, 328 px then,
// wider than the 372 px the others had.
const (
	gateWBefore      = 372
	gateHBefore      = 567
	pwWBefore        = 328
	orphansWBefore   = 372
	downloadsWBefore = 372
	stepWBefore      = 372
	auditGateWBefore = 362
	selectionWBefore = 372
	reportWBefore    = 372
)

func init() { covScenarios["real-wide"] = realWide }

// dialogGeometry is d as shown in w: its size, whether a gate's steps and
// notes are side by side ("side=-" without a plan), how many of its
// scrolled windows have something to scroll, whether its yes button is in
// the window, and whether anything in it is wider than the window.
func dialogGeometry(w *Window, d gtk.Widgetter, yes string) string {
	// The dialog widget fills the window's dialog host; its sheet (the
	// floating or the bottom sheet's "sheet" node) is what is drawn.
	var sheet gtk.Widgetter
	eachWidget(d, func(wd gtk.Widgetter) {
		if sheet == nil && gtk.BaseWidget(wd).CSSName() == "sheet" {
			sheet = wd
		}
	})
	if sheet == nil {
		return "no sheet"
	}
	b, ok := boundsIn(sheet, w.win)
	if !ok {
		return "dialog not placed"
	}
	var rail, note gtk.Widgetter
	scrolls := 0
	eachWidget(d, func(wd gtk.Widgetter) {
		base := gtk.BaseWidget(wd)
		if !base.IsDrawable() {
			return
		}
		if rail == nil && base.HasCSSClass("am-rail") {
			rail = wd
		}
		if note == nil && base.HasCSSClass("am-note") {
			note = wd
		}
		if sw, ok := wd.(*gtk.ScrolledWindow); ok {
			if adj := sw.VAdjustment(); adj.Upper()-adj.PageSize() > 1 {
				scrolls++
			}
		}
	})
	side := "-"
	if rail != nil && note != nil {
		r, _ := boundsIn(rail, w.win)
		n, _ := boundsIn(note, w.win)
		side = strconv.FormatBool(n.x >= r.x+r.w && n.y < r.y+r.h)
	}
	buttons := false
	if btn := findButton(gtk.BaseWidget(d), yes); btn != nil {
		if bb, ok := boundsIn(btn, w.win); ok {
			buttons = bb.x >= 0 && bb.y >= 0 && bb.x+bb.w <= float64(w.win.Width()) && bb.y+bb.h <= float64(w.win.Height()) && btn.IsDrawable()
		}
	}
	return fmt.Sprintf("w=%.0f h=%.0f side=%s scrolls=%d buttons=%v fits=%v", b.w, b.h, side, scrolls, buttons, len(tooWide(d, w.win.Width())) == 0)
}

// gateRootCounts are the number the gate's note says run as root ("6
// steps run as root"; 0 without one) and the rail's tags reading "root".
func gateRootCounts(d *amDialog) (note, tags int) {
	re := regexp.MustCompile(`^(\d+) steps? runs? as root`)
	eachWidget(d, func(x gtk.Widgetter) {
		l, ok := x.(*gtk.Label)
		if !ok || !l.IsDrawable() {
			return
		}
		if l.HasCSSClass("am-tag") && l.Text() == "root" {
			tags++
		}
		if m := re.FindStringSubmatch(l.Text()); m != nil && l.HasCSSClass("am-note") {
			note, _ = strconv.Atoi(m[1])
		}
	})
	return note, tags
}

// wideConfirms are the prompts real-wide feeds after the real gate, each
// as the engine builds it, with the button it is answered with.
func wideConfirms() []struct {
	name string
	ev   engine.Event
	yes  string // the button checked to be in view
	resp string
} {
	dl := downloads.Plan{Shown: "~/Downloads", Names: realDownloads}
	dlTitle, dlBody, _ := strings.Cut(dl.Confirm(), "\n")
	var dlItems []string
	for _, n := range dl.Names {
		dlItems = append(dlItems, sanitize.QuoteName(n, 200))
	}
	type c = struct {
		name string
		ev   engine.Event
		yes  string
		resp string
	}
	return []c{
		{"orphans", engine.NeedsConfirm{ID: 201, Kind: engine.ConfirmOrphans, Step: "orphans-remove", Title: vmOrphansTitle, Msg: vmOrphansTitle,
			Direct: vmOrphans, Cascade: vmCascade, Destructive: true, YesLabel: "Remove"}, "Remove", "no"},
		{"downloads", engine.NeedsConfirm{ID: 202, Kind: engine.ConfirmDownloads, Step: "downloads", Title: dlTitle,
			Body: strings.TrimLeft(dlBody, "\n"), Msg: dl.Confirm(), Items: dlItems, Destructive: true, YesLabel: "Delete"}, "Delete", "no"},
		{"step", engine.NeedsConfirm{ID: 203, Kind: engine.ConfirmStep, Step: "paccache-wipe", Title: "Delete ENTIRE package cache? (can't reinstall without internet)",
			Msg: "Delete ENTIRE package cache? (can't reinstall without internet)", Destructive: true, YesLabel: "Delete"}, "Delete", "no"},
		{"audit-gate", engine.NeedsConfirm{ID: 204, Kind: engine.ConfirmAuditGate, Step: "topgrade",
			Title: "valent changed maintainer since the last audit. Update anyway?", Msg: "valent changed maintainer since the last audit. Update anyway?",
			YesLabel: "Update anyway"}, "Update anyway", "no"},
		{"password", engine.NeedsPassword{ID: 205, Kind: engine.PasswordAskpass, Requester: "/usr/bin/yay (pid 1597816)",
			Prompt: "[sudo] password for enzord: "}, "Authenticate", "cancel"},
		{"polkit", engine.NeedsPassword{ID: 206, Kind: engine.PasswordAskpass, Polkit: true, Action: "org.archmaid.helper.full-cleanup"}, "Authenticate", "cancel"},
		{"upfront", engine.NeedsPassword{ID: 207, Kind: engine.PasswordUpFront, Gate: "Proceed with full cleanup?"}, "Authenticate", "cancel"},
		{"selection", engine.NeedsSelection{ID: 208, Title: "Enabled but not running — pick the services to disable",
			Items: []string{"bluetooth.service", "cups.service", "avahi-daemon.service", "ModemManager.service", "sshd.service"}}, "Continue", "cancel"},
		{"report", engine.OutputReport{ID: 209, Text: "x", Audit: &engine.AuditReport{Total: 31, CanAccept: true, Findings: []engine.AuditFinding{
			{Name: "valent", High: true, Flags: []string{"new maintainer", "0 votes"}, Reason: "maintainer andyholmes → someone-else"},
			{Name: "walbottle-git", Flags: []string{"orphaned", "out of date"}},
		}}}, "Accept", "close"},
	}
}

// realWide: at envWideW, Full Cleanup's real gate (the real Driver's
// plan, the kept password's note), declined; then each other dialog fed
// as the engine sends it and answered no. Each measured 600 ms after it
// opened (shown and settled), and shot.
func realWide(c *covEnv) {
	w := c.w
	width := w.win.Width()
	shot := fmt.Sprintf("real-wide-%d", width)
	measure := func(name string, id engine.PromptID, yes string) {
		d, ok := w.dialogs[id]
		if !ok {
			c.fail("%s: no dialog for prompt %d", name, id)
			return
		}
		shoot(w, shot, name)
		c.rep.Narrow = append(c.rep.Narrow, fmt.Sprintf("%s width=%d %s", name, width, dialogGeometry(w, d, yes)))
	}
	confirms := wideConfirms()
	next := 0
	var feedNext func()
	feedNext = func() {
		if next == len(confirms) {
			c.finish()
			return
		}
		c.feed(confirms[next].ev)
	}
	w.onApply = func(ch runview.Change) {
		if ch.Opened == nil {
			return
		}
		id := ch.Opened.ID
		coreglib.TimeoutAdd(600, func() {
			d, ok := w.dialogs[id]
			if !ok {
				c.fail("prompt %d has no dialog", id)
				c.finish()
				return
			}
			if id < 200 { // the real gate
				measure("gate", id, "Proceed")
				// Its rail's tags: under the titles in the 380 px bottom
				// sheet (Dev 5's VM round b6 had them at the line's end),
				// at the line's end at 1460.
				c.ok(checkRailTags(c, d, fmt.Sprintf("gate at %d", width)) >= 7, "gate at %d: fewer rail steps with tags drawn than the plan's 7", width)
				// The note's count is the rail's root tags (Dev 5's VM
				// round b6: "6 steps run as root" beside 7 root tags).
				note, roots := gateRootCounts(d)
				c.ok(note > 0 && note == roots, "gate at %d: the note says %d steps run as root, the rail tags %d root", width, note, roots)
				c.press(d, "no")
				return
			}
			cf := confirms[next]
			measure(cf.name, id, cf.yes)
			if cf.name == "orphans" && width >= 1460 {
				// The VM's cascade, one "name version" per line, unbroken.
				c.ok(len(wrappedLists(d)) == 0, "orphans at %d: lists wrap their lines: %q", width, wrappedLists(d))
			}
			c.press(d, cf.resp)
			next++
			coreglib.TimeoutAdd(150, func() { feedNext() })
		})
	}
	finished := false
	prev := w.onApply
	w.onApply = func(ch runview.Change) {
		prev(ch)
		if ch.Finished && !finished {
			finished = true
			// The declined gate's run is over: the other dialogs.
			coreglib.TimeoutAdd(150, func() { feedNext() })
		}
	}
	w.menu[actions.IDFullCleanup].start()
}

// TestGUIWideDialogs: at 1460x930 the gate is two columns, at least 800 px
// wide, shorter than before, nothing in it scrolls; every other dialog is
// wider than before. At 380 px every dialog fits with its buttons in view
// and the gate's columns stacked.
func TestGUIWideDialogs(t *testing.T) {
	needDisplay(t)
	for _, wd := range narrowWidths(t, 1460, 380) {
		t.Run(strconv.Itoa(wd), func(t *testing.T) {
			_, env := installShims(t, shimOpts{})
			var buf bytes.Buffer
			rep, _ := childCmd(t, "real-wide", func(c *exec.Cmd) { c.Stderr = io.MultiWriter(os.Stderr, &buf) },
				append(env, fmt.Sprintf("%s=%d", envWideW, wd))...)
			for _, l := range strings.Split(buf.String(), "\n") {
				if glibWarned(l) {
					t.Errorf("GTK logged: %s", l)
				}
			}
			got := map[string]struct {
				w, h, scrolls int
				side          string
				buttons, fits bool
			}{}
			for _, m := range rep.Narrow {
				t.Log(m)
				var name string
				var width int
				var g struct {
					w, h, scrolls int
					side          string
					buttons, fits bool
				}
				if _, err := fmt.Sscanf(m, "%s width=%d w=%d h=%d side=%s scrolls=%d buttons=%t fits=%t", &name, &width, &g.w, &g.h, &g.side, &g.scrolls, &g.buttons, &g.fits); err != nil {
					t.Errorf("measure %q: %v", m, err)
					continue
				}
				got[name] = g
				if width != wd {
					t.Errorf("%s: the window is %d px wide, want %d", name, width, wd)
				}
				if !g.fits || !g.buttons {
					t.Errorf("%s: fits %v, its buttons in view %v: %s", name, g.fits, g.buttons, m)
				}
			}
			names := []string{"gate"}
			for _, cf := range wideConfirms() {
				names = append(names, cf.name)
			}
			for _, n := range names {
				if _, ok := got[n]; !ok {
					t.Errorf("never measured %s (%q)", n, rep.Narrow)
				}
			}
			gate := got["gate"]
			if wd < 640 {
				if gate.side != "false" {
					t.Errorf("gate at %d px: steps and notes side by side %s, want them stacked", wd, gate.side)
				}
				return
			}
			if gate.w < 800 || gate.h >= gateHBefore || gate.side != "true" || gate.scrolls != 0 {
				t.Errorf("gate at %d px: %dx%d, side by side %s, %d scrolling; want at least 800 px wide, under %d px high (before: %dx%d), steps beside the notes, nothing scrolling",
					wd, gate.w, gate.h, gate.side, gate.scrolls, gateHBefore, gateWBefore, gateHBefore)
			}
			for n, before := range map[string]int{"orphans": orphansWBefore, "downloads": downloadsWBefore, "step": stepWBefore, "audit-gate": auditGateWBefore,
				"password": 372, "polkit": 372, "upfront": 372, "selection": selectionWBefore, "report": reportWBefore} {
				if g := got[n]; g.w <= before {
					t.Errorf("%s at %d px: %d px wide, want wider than before (%d)", n, wd, g.w, before)
				}
			}
			if slices.ContainsFunc(names, func(n string) bool { return got[n].scrolls > 0 }) {
				t.Errorf("a dialog scrolls at %d px: %v", wd, got)
			}
		})
	}
}

func init() { covScenarios["cov-dialog-close"] = covDialogClose }

// TestGUICovDialogClose: a wide dialog closed without a button (Escape,
// the window's close) sends its close response exactly once; a button's
// answer is the only one sent.
func TestGUICovDialogClose(t *testing.T) {
	rep := covChild(t, "cov-dialog-close")
	if rep != nil {
		t.Logf("%q", rep.Narrow)
	}
}

// covDialogClose: a confirm, a pick list and a report each closed as
// Escape closes them (adw_dialog_close), a confirm answered by its
// button, and a confirm under the window's close; each must reach the
// Driver once.
func covDialogClose(c *covEnv) {
	w := c.w
	c.quiet()
	// answers are the answers prompt id got.
	answers := func(id engine.PromptID) []string {
		var out []string
		for _, s := range c.sent() {
			if f := strings.Fields(s); len(f) > 1 && f[1] == strconv.Itoa(int(id)) {
				out = append(out, s)
			}
		}
		return out
	}
	escape := func(id engine.PromptID) func() {
		return func() {
			d := w.dialogs[id]
			if d == nil {
				c.fail("prompt %d has no dialog", id)
				return
			}
			d.Close() // what Escape does
		}
	}
	once := func(id engine.PromptID, want string) func() {
		return func() {
			got := answers(id)
			c.ok(len(got) == 1 && got[0] == want, "prompt %d: sent %q, want exactly %q", id, got, want)
			c.ok(w.dialogs[id] == nil, "prompt %d: the closed dialog is still held", id)
		}
	}
	var responses []string
	c.seq(150,
		func() { c.feed(engine.NeedsConfirm{ID: 301, Title: "Escape closes me?", YesLabel: "Go"}) },
		escape(301),
		once(301, "confirm 301 false"),
		func() {
			c.feed(engine.NeedsSelection{ID: 302, Title: "Pick", Items: []string{"a.service", "b.service"}})
		},
		escape(302),
		once(302, "cancel 302"),
		func() {
			c.feed(engine.OutputReport{ID: 303, Text: "x", Audit: &engine.AuditReport{Total: 1, CanAccept: true,
				Findings: []engine.AuditFinding{{Name: "valent", High: true, Flags: []string{"new maintainer"}}}}})
		},
		escape(303),
		once(303, "confirm 303 false"),
		func() { c.feed(engine.NeedsConfirm{ID: 305, Title: "Click me?", YesLabel: "Go"}) },
		func() {
			d := w.dialogs[305]
			if d == nil {
				c.fail("prompt 305 has no dialog")
				return
			}
			// The dialog's own responses, not just what the window
			// passed on (answer drops a second one for a known ID).
			d.ConnectResponse(func(r string) { responses = append(responses, r) })
			c.press(d, "yes")
		},
		once(305, "confirm 305 true"),
		func() {
			c.ok(slices.Equal(responses, []string{"yes"}), "prompt 305's dialog responded %q, want just yes", responses)
		},
		func() { c.feed(engine.NeedsConfirm{ID: 304, Title: "The window closes under me?"}) },
		func() {
			c.ok(w.dialogs[304] != nil, "prompt 304 has no dialog")
			// The window's close: libadwaita closes the open dialog
			// first (its close response), or the window goes and takes
			// it along (withdrawn, the Driver closed: nothing sent).
			// Never twice.
			w.win.Close()
		},
		func() {
			got := answers(304)
			if w.closed {
				c.ok(len(got) == 0, "the window closed, yet prompt 304 sent %q", got)
				c.rep.Narrow = append(c.rep.Narrow, "window-close: the window closed with the dialog")
				return
			}
			c.ok(len(got) == 1 && got[0] == "confirm 304 false", "the window's close sent %q for prompt 304, want exactly the close response", got)
			c.ok(w.dialogs[304] == nil, "prompt 304's dialog is still held")
			c.rep.Narrow = append(c.rep.Narrow, "window-close: the dialog closed, the window stayed")
		},
	)
}
