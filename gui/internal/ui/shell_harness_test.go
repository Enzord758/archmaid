package ui

import (
	"fmt"
	"slices"
	"testing"

	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
)

// navState is the shell as narrow-nav records it: whether the split view
// is collapsed, the overlay shown, and the page in view.
func navState(w *Window, what string) string {
	return fmt.Sprintf("%s: collapsed=%v sidebar=%v page=%s", what, w.shell.split.Collapsed(), w.shell.split.ShowSidebar(), w.stack.VisibleChildName())
}

// then runs the steps one after another, each once the frames before it
// have been drawn.
func then(steps ...func()) {
	if len(steps) == 0 {
		return
	}
	glib.TimeoutAdd(300, func() {
		steps[0]()
		then(steps[1:]...)
	})
}

// narrowNav works the narrow shell the way a user does at 630x372: the
// header's toggle opens the overlay, and a page picked in it closes it,
// the page already shown included; the toggle again, then Escape (the
// overlay's key handler); the toggle again, then a press on the dimmed
// page (its press handler, at the window's right edge); then the window
// widens past the breakpoint and narrows again, and the page picked must
// still be up.
func narrowNav(w *Window, rep *report) {
	sh := w.shell
	note := func(what string) func() {
		return func() { rep.Nav = append(rep.Nav, navState(w, what)) }
	}
	activate := func(name string) func() { return func() { sh.navRows[name].Emit("activate") } }
	open := func() { sh.toggle.SetActive(true) }
	then(note("start"), open, note("toggled"),
		activate("overview"), note("picked overview"),
		open, activate("settings"), note("picked settings"),
		open, func() { rep.Nav = append(rep.Nav, fmt.Sprintf("escape handled=%v", w.navKey(gdk.KEY_Escape))) }, note("escape"),
		open, func() {
			rep.Nav = append(rep.Nav, fmt.Sprintf("press handled=%v", w.navPress(float64(w.win.Width()-10), float64(w.win.Height()/2))))
		}, note("pressed outside"),
		open, func() { rep.Nav = append(rep.Nav, fmt.Sprintf("press in sidebar handled=%v", w.navPress(20, 20))) }, note("pressed in sidebar"),
		activate("settings"),
		func() { w.win.SetDefaultSize(900, 372) }, note("wide"),
		func() { w.win.SetDefaultSize(630, 372) }, note("narrow again"),
		func() { w.win.Close() })
}

// TestGUINarrowNav: at 630x372 the sidebar is an overlay that the toggle
// opens and a picked page (also the one shown), Escape or a press outside
// it closes, and the page picked stays up across the breakpoint (Dev 5's
// VM round r8).
func TestGUINarrowNav(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "narrow-nav")
	want := []string{
		"start: collapsed=true sidebar=false page=overview",
		"toggled: collapsed=true sidebar=true page=overview",
		"picked overview: collapsed=true sidebar=false page=overview",
		"picked settings: collapsed=true sidebar=false page=settings",
		"escape handled=true",
		"escape: collapsed=true sidebar=false page=settings",
		"press handled=true",
		"pressed outside: collapsed=true sidebar=false page=settings",
		"press in sidebar handled=false",
		"pressed in sidebar: collapsed=true sidebar=true page=settings",
		"wide: collapsed=false sidebar=true page=settings",
		"narrow again: collapsed=true sidebar=false page=settings",
	}
	if !slices.Equal(rep.Nav, want) {
		t.Errorf("nav\n%q\nwant\n%q", rep.Nav, want)
	}
}
