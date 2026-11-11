package pkgview

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/steps"
)

// A held update's "Review and update" (#280) is the AUR install request of
// exactly that package: the page's list is neither used nor cleared, the
// gate for repository updates still holds, and a name that is not a package's
// never reaches the engine.
func TestReviewAURIsOneAURRequest(t *testing.T) {
	cr := newCtl()
	cr.setUpdates(0, 1, time.Hour)
	if err := cr.c.Add(aurPkg("somethingelse", "1"), ModeInstall); err != nil {
		t.Fatal(err)
	}
	if err := cr.c.ReviewAUR("spotify"); err != nil {
		t.Fatal(err)
	}
	want := steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"spotify"}, AUR: true}
	if len(cr.be.started) != 1 || cr.be.started[0].Mode != want.Mode || !cr.be.started[0].AUR ||
		len(cr.be.started[0].Names) != 1 || cr.be.started[0].Names[0] != "spotify" {
		t.Fatalf("the engine got %+v, want %+v", cr.be.started, want)
	}
	if !cr.c.Sheet.Open() || cr.c.Sheet.State() != SheetPlanning {
		t.Fatalf("the sheet is %v", cr.c.Sheet.State())
	}
	// A second review while this one is up is refused.
	if err := cr.c.ReviewAUR("other"); err != ErrSheetOpen || len(cr.be.started) != 1 {
		t.Fatalf("second review: %v (%d requests)", err, len(cr.be.started))
	}
	// The run ends well: the page's own list is still there.
	cr.c.Handle(RunStarted{})
	cr.c.Handle(Done{OK: true})
	if cr.c.List.Len() != 1 || !cr.c.List.Has("somethingelse") {
		t.Fatalf("the held update's run cleared the list: %v", cr.c.List.Names())
	}
}

func TestReviewAURWaitsForRepoUpdatesAndChecksTheName(t *testing.T) {
	cr := newCtl()
	cr.setUpdates(2, 0, time.Hour)
	if err := cr.c.ReviewAUR("spotify"); err != ErrBlocked || len(cr.be.started) != 0 || cr.c.Sheet.Open() {
		t.Fatalf("with repository updates waiting: %v, %d requests", err, len(cr.be.started))
	}
	cr.setUpdates(0, 0, time.Hour)
	for _, bad := range []string{"", "-rf", "a b", "x;y", "../x", "a\nb"} {
		if err := cr.c.ReviewAUR(bad); err == nil || len(cr.be.started) != 0 {
			t.Errorf("ReviewAUR(%q): %v, %d requests", bad, err, len(cr.be.started))
		}
	}
	// A list's own review after a direct one clears the list again.
	if err := cr.c.ReviewAUR("spotify"); err != nil {
		t.Fatal(err)
	}
	cr.c.Dismiss()
	cr.c.Handle(Done{})
	if err := cr.c.Add(aurPkg("zed", "1"), ModeInstall); err != nil {
		t.Fatal(err)
	}
	if err := cr.c.Review(); err != nil {
		t.Fatal(err)
	}
	cr.c.Handle(RunStarted{})
	cr.c.Handle(Done{OK: true})
	if cr.c.List.Len() != 0 {
		t.Fatal("a list's own run no longer clears the list")
	}
}
