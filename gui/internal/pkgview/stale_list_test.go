package pkgview

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// #314: a package installed outside the page after it loaded is still in the
// list; Review re-reads the databases first and unstages it with a note
// instead of asking for a plan that can only be empty.
func TestReviewDropsATargetInstalledMeanwhile(t *testing.T) {
	r := newRig(repoPkg("plymouth", "26.134.222-5"), repoPkg("htop", "3"))
	be := &fakeBackend{}
	var notes []string
	reloads := 0
	c := NewController(Config{Search: r.s, Catalog: r.cat, Backend: be, Clock: r.clock,
		DBAge: func() time.Duration { return time.Hour },
		Reload: func() bool {
			reloads++
			for i := range r.cat.pkgs { // pacman -S plymouth ran in a terminal
				if r.cat.pkgs[i].Name == "plymouth" {
					r.cat.pkgs[i].Installed, r.cat.pkgs[i].InstalledVersion = true, "26.134.222-5"
				}
			}
			return true
		},
		Notice: func(s string) { notes = append(notes, s) }})
	pl, _ := r.cat.Lookup("plymouth")
	ht, _ := r.cat.Lookup("htop")
	if err := c.Add(pl, ModeInstall); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(ht, ModeInstall); err != nil {
		t.Fatal(err)
	}
	if err := c.Review(); err != nil {
		t.Fatal(err)
	}
	if reloads != 1 || len(notes) != 1 || !strings.Contains(notes[0], "plymouth is already installed (26.134.222-5)") {
		t.Fatalf("reloads %d, notes %q", reloads, notes)
	}
	if len(be.started) != 1 || !slices.Equal(be.started[0].Names, []string{"htop"}) {
		t.Fatalf("started %+v: plymouth must not be planned", be.started)
	}

	// Only stale targets left: nothing is planned at all, the sheet stays shut.
	c2 := NewController(Config{Search: r.s, Catalog: r.cat, Backend: &fakeBackend{}, Clock: r.clock,
		Notice: func(s string) { notes = append(notes, s) }})
	be2 := &fakeBackend{}
	c2.cfg.Backend = be2
	ht2, _ := r.cat.Lookup("htop")
	ht2.Installed, ht2.InstalledVersion = false, ""
	if err := c2.Add(ht2, ModeInstall); err != nil {
		t.Fatal(err)
	}
	for i := range r.cat.pkgs {
		if r.cat.pkgs[i].Name == "htop" {
			r.cat.pkgs[i].Installed, r.cat.pkgs[i].InstalledVersion = true, "3"
		}
	}
	notes = nil
	if err := c2.Review(); err != nil {
		t.Fatal(err)
	}
	if len(be2.started) != 0 || c2.Sheet.Open() || c2.List.Len() != 0 || len(notes) != 1 {
		t.Fatalf("started %v, open %v, list %d, notes %q", be2.started, c2.Sheet.Open(), c2.List.Len(), notes)
	}
}
