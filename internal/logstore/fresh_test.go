package logstore

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func freshAt(root string, at time.Time) *Store {
	return New(Options{Dir: root, KeepRuns: KeepRuns, Fresh: true, Now: func() time.Time { return at }})
}

// TestFreshRunsInOneMinuteAreDistinct: two actions started in the same
// minute each get their own run folder (the VM round: LAST RUN listed
// both logs of one folder). The first is the stamp, the next <stamp>-02,
// <stamp>-03; a later minute is a plain stamp again; a store without Fresh
// (a reader's, or a store re-made for one run) still shares the stamp.
func TestFreshRunsInOneMinuteAreDistinct(t *testing.T) {
	root := t.TempDir()
	at := day(11)
	var dirs []string
	for range 3 {
		s := freshAt(root, at)
		if err := s.Append(Cleanup, "OK", "x", ""); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, filepath.Base(s.RunDir()))
	}
	st := stampOf(at)
	if want := []string{st, st + "-02", st + "-03"}; !slices.Equal(dirs, want) {
		t.Fatalf("run dirs %q, want %q", dirs, want)
	}
	if got := filepath.Base(storeAt(root, at).RunDir()); got != st {
		t.Errorf("a store without Fresh is %q, want the stamp %q", got, st)
	}
	if got := filepath.Base(freshAt(root, at.Add(time.Minute)).RunDir()); got != stampOf(at.Add(time.Minute)) {
		t.Errorf("the next minute's run is %q, want its own stamp", got)
	}
	if got := filepath.Base(New(Options{Dir: root, Fresh: true, DryRun: true, Now: func() time.Time { return at }}).RunDir()); got != "dryrun" {
		t.Errorf("a dry run is %q, want dryrun", got)
	}
	// A reader's handle for a suffixed run reads that run's directory.
	if got := New(Options{Dir: root, Run: st + "-02"}).RunDir(); got != filepath.Join(root, st+"-02") {
		t.Errorf("Run option: %q", got)
	}
	if got := New(Options{Dir: root, Run: "../x", Now: func() time.Time { return at }}).RunDir(); got != filepath.Join(root, st) {
		t.Errorf("a Run that isn't a run directory's name changed the dir: %q", got)
	}
}

// TestFreshRunsKeepTheHistoryRule: same-minute runs are ordinary run
// directories to the rotation: seven real runs in one minute keep the
// KeepRuns newest, and a discovery-only run in that minute is its own
// directory, kept as its action's newest record, without taking a slot.
func TestFreshRunsKeepTheHistoryRule(t *testing.T) {
	root := t.TempDir()
	at := day(11)
	d := freshAt(root, at)
	if err := d.Append(Services, "OK", "checked", ""); err != nil {
		t.Fatal(err)
	}
	for range 7 {
		if err := freshAt(root, at).Append(Cleanup, "OK", "x", ""); err != nil {
			t.Fatal(err)
		}
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	st := stampOf(at)
	// The discovery-only run (the stamp) stays; real runs -02..-08 keep
	// the five newest: -04..-08.
	if want := []string{st, st + "-04", st + "-05", st + "-06", st + "-07", st + "-08"}; !slices.Equal(got, want) {
		t.Errorf("run dirs %q, want %q", got, want)
	}
}
