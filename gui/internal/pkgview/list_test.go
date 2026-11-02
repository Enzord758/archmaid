package pkgview

import (
	"slices"
	"testing"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

func TestListInstallsAndRemovalsDoNotMix(t *testing.T) {
	l := NewList(NewAdvisoryProtected())
	if err := l.Add(repoPkg("gvim", "9"), ModeInstall); err != nil {
		t.Fatal(err)
	}
	vim := installed(repoPkg("vim", "9"))
	err := l.Add(vim, ModeRemove)
	if ErrKind(err) != AddOtherMode {
		t.Fatalf("a removal next to an install: %v", err)
	}
	if l.Mode() != ModeInstall || !slices.Equal(l.Names(), []string{"gvim"}) {
		t.Fatalf("the refused add changed the list: %v %v", l.Mode(), l.Names())
	}
	l.Clear()
	if err := l.Add(vim, ModeRemove); err != nil {
		t.Fatal(err)
	}
	if ErrKind(l.Add(repoPkg("gvim", "9"), ModeInstall)) != AddOtherMode {
		t.Fatal("an install next to a removal was accepted")
	}
	// Emptying the list frees the mode.
	l.Remove("vim")
	if l.Mode() != ModeNone {
		t.Fatalf("mode %v after the last entry went", l.Mode())
	}
	if err := l.Add(repoPkg("gvim", "9"), ModeInstall); err != nil {
		t.Fatal(err)
	}
}

func TestListRules(t *testing.T) {
	l := NewList(NewAdvisoryProtected())
	inst := installed(repoPkg("vim", "9"))
	if ErrKind(l.Add(inst, ModeInstall)) != AddAlreadyInstalled {
		t.Error("an installed package was added for install")
	}
	if ErrKind(l.Add(repoPkg("nano", "1"), ModeRemove)) != AddNotInstalled {
		t.Error("a package that is not installed was added for removal")
	}
	orphan := installed(repoPkg("libfoo", "1"))
	orphan.Orphan, orphan.Reason = true, pkgdb.ReasonDep
	if ErrKind(l.Add(orphan, ModeRemove)) != AddOrphan {
		t.Error("an orphan was added for removal: Full Cleanup removes those")
	}
	if ErrKind(l.Add(installed(repoPkg("glibc", "1")), ModeRemove)) != AddProtected {
		t.Error("a protected package was added for removal")
	}
	if l.Len() != 0 {
		t.Fatalf("refused adds left %d entries", l.Len())
	}
	// The same name twice is one entry, not an error.
	_ = l.Add(inst, ModeRemove)
	if err := l.Add(inst, ModeRemove); err != nil || l.Len() != 1 {
		t.Fatalf("twice: %v, %d entries", err, l.Len())
	}
}

func TestListLimit(t *testing.T) {
	l := NewList(nil)
	for i := 0; i < MaxNames; i++ {
		if err := l.Add(repoPkg("p"+string(rune('a'+i%26))+string(rune('a'+i/26)), "1"), ModeInstall); err != nil {
			t.Fatal(i, err)
		}
	}
	if ErrKind(l.Add(repoPkg("one-more", "1"), ModeInstall)) != AddFull {
		t.Fatal("33rd name accepted: the helper takes 32")
	}
}

func TestListRequestIsRsOrRns(t *testing.T) {
	l := NewList(nil)
	_ = l.Add(installed(repoPkg("vim", "9")), ModeRemove)
	req, ok := l.Request()
	if !ok || req.Mode != steps.PkgRemove || req.WithConfig || req.AsDeps {
		t.Fatalf("request %+v: want -Rs", req)
	}
	l.WithConfig = true
	req, _ = l.Request()
	if !req.WithConfig {
		t.Fatalf("request %+v: want -Rns", req)
	}
	// The box means nothing for an install, and clearing resets it.
	l.Clear()
	_ = l.Add(repoPkg("gvim", "9"), ModeInstall)
	l.WithConfig = true
	req, _ = l.Request()
	if req.Mode != steps.PkgInstall || req.WithConfig {
		t.Fatalf("an install carried the config box: %+v", req)
	}
	l.Clear()
	if l.WithConfig {
		t.Fatal("Clear kept the config box")
	}
	if _, ok := l.Request(); ok {
		t.Fatal("an empty list made a request")
	}
}

func TestListTotals(t *testing.T) {
	l := NewList(nil)
	a, b := repoPkg("a", "1"), repoPkg("b", "1")
	a.DownloadSize, b.DownloadSize = 100, 50
	_ = l.Add(a, ModeInstall)
	_ = l.Add(b, ModeInstall)
	if l.Total() != 150 {
		t.Fatalf("total %d", l.Total())
	}
	if l.Items()[0].Name != "a" {
		t.Fatal("order is not the order added")
	}
}

func TestProtectedLearnsFromRefusals(t *testing.T) {
	p := NewAdvisoryProtected()
	if _, ok := p.Protected("nano"); ok {
		t.Fatal("nano protected")
	}
	p.Learn([]string{"nano"}, "")
	if why, ok := p.Protected("nano"); !ok || why == "" {
		t.Fatal("a refused name was not remembered with a reason")
	}
}

// An AUR package is a request of its own (PkgRequest.AUR): the engine builds
// it, it does not ask the helper for it. A list never mixes the two kinds.
func TestListAURIsItsOwnRun(t *testing.T) {
	l := NewList(NewAdvisoryProtected())
	if err := l.Add(aurPkg("paru-bin", "2-1"), ModeInstall); err != nil {
		t.Fatal(err)
	}
	if err := l.Add(aurPkg("yay-bin", "12-1"), ModeInstall); err != nil {
		t.Fatalf("two AUR packages: %v", err)
	}
	req, ok := l.Request()
	if !ok || !req.AUR || req.Mode != steps.PkgInstall || !slices.Equal(req.Names, []string{"paru-bin", "yay-bin"}) {
		t.Fatalf("request %+v", req)
	}
	if ErrKind(l.Add(repoPkg("gvim", "9"), ModeInstall)) != AddMixedSource {
		t.Fatal("a repo package joined an AUR list")
	}
	l.Clear()
	if err := l.Add(repoPkg("gvim", "9"), ModeInstall); err != nil {
		t.Fatal(err)
	}
	if ErrKind(l.Add(aurPkg("paru-bin", "2-1"), ModeInstall)) != AddMixedSource {
		t.Fatal("an AUR package joined a repo list")
	}
	if req, _ := l.Request(); req.AUR {
		t.Fatalf("a repo request marked AUR: %+v", req)
	}
}
