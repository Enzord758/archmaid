package logstore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// seedDir makes the run directory name under root holding the given files.
func seedDir(t *testing.T, root, name string, files ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(root, name, f), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// storeAt is a real store (KeepRuns) whose run directory is at.
func storeAt(root string, at time.Time) *Store {
	return New(Options{Dir: root, KeepRuns: KeepRuns, Now: func() time.Time { return at }})
}

func day(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }

func stampOf(t time.Time) string { return t.Format("20060102-1504") }

func TestDiscoveryCategories(t *testing.T) {
	for _, c := range []Category{Services, Repair, Downloads, AURAudit} {
		if !c.Valid() || !c.Discovery() {
			t.Errorf("%q: Valid %v, Discovery %v; want both", c, c.Valid(), c.Discovery())
		}
		if c.Label() == string(c) {
			t.Errorf("%q has no label in Categories", c)
		}
	}
	for _, c := range []Category{Topgrade, Orphans, Cleanup, Audit} {
		if c.Discovery() {
			t.Errorf("%q is not a discovery category", c)
		}
	}
	if Category("../x").Discovery() || Category("services.log").Valid() {
		t.Fatal("a made-up category passed")
	}
}

// TestDiscoveryRunsNeverEvictRealRuns: five real runs and a pile of
// discovery-only ones (several per action). Discovery records never take a
// real run's slot, and only the newest discovery-only run per action stays.
func TestDiscoveryRunsNeverEvictRealRuns(t *testing.T) {
	root := t.TempDir()
	var real []string
	for d := 1; d <= KeepRuns; d++ {
		seedDir(t, root, stampOf(day(d)), "cleanup.log", "topgrade.log")
		real = append(real, stampOf(day(d)))
	}
	cats := []Category{Services, Repair, Downloads, AURAudit, Services, Repair, Services}
	for i, c := range cats {
		if err := storeAt(root, day(10+i)).Append(c, "CHECK", "nothing", "body\n"); err != nil {
			t.Fatal(err)
		}
	}
	got := runDirs(t, root)
	for _, r := range real {
		if !slices.Contains(got, r) {
			t.Fatalf("real run %s evicted by discovery records: %v", r, got)
		}
	}
	// Newest per action: services day 16, repair day 15, auraudit day 13,
	// downloads day 12.
	want := append(slices.Clone(real), stampOf(day(12)), stampOf(day(13)), stampOf(day(15)), stampOf(day(16)))
	slices.Sort(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("run dirs = %v\nwant       %v", got, want)
	}
	// A sixth real run evicts the oldest real one, never a discovery record.
	if err := storeAt(root, day(20)).Append(Cleanup, "OK", "x", ""); err != nil {
		t.Fatal(err)
	}
	got = runDirs(t, root)
	if slices.Contains(got, real[0]) || !slices.Contains(got, real[1]) {
		t.Fatalf("a real run's rotation: %v", got)
	}
	for _, d := range []int{12, 13, 15, 16} {
		if !slices.Contains(got, stampOf(day(d))) {
			t.Fatalf("the real rotation took discovery record %s: %v", stampOf(day(d)), got)
		}
	}
}

// TestDiscoveryDirBecomingRealRotates: the TUI keeps one run directory per
// process. A discovery record first, then a cleanup entry in the same
// directory: that entry makes it a real run, and the history rotates then,
// as if it had created the directory.
func TestDiscoveryDirBecomingRealRotates(t *testing.T) {
	root := t.TempDir()
	old := seedRuns(t, root, KeepRuns)
	s := storeAt(root, day(1))
	if err := s.Append(Services, "CHECK", "nothing", ""); err != nil {
		t.Fatal(err)
	}
	if got := runDirs(t, root); len(got) != KeepRuns+1 {
		t.Fatalf("a discovery record rotated the real history: %v", got)
	}
	if err := s.Append(Cleanup, "OK", "Topgrade completed", ""); err != nil {
		t.Fatal(err)
	}
	got := runDirs(t, root)
	if len(got) != KeepRuns || slices.Contains(got, old[0]) || !slices.Contains(got, s.run) {
		t.Fatalf("after the run became real: %v (oldest %s should be gone)", got, old[0])
	}
	// A later entry in a real directory rotates nothing.
	seedDir(t, root, "20000101-0000", "cleanup.log")
	if err := s.Append(Services, "CHECK", "again", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(Cleanup, "OK", "again", ""); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(runDirs(t, root), "20000101-0000") {
		t.Fatal("a later Append pruned")
	}
}

// TestDiscoveryClassificationFailsSafe: a directory is discovery-only only
// when it holds discovery logs and nothing else. A real log, a stray file
// or a subdirectory makes it real, so it is never taken by the discovery
// pruning; an empty one is real too.
func TestDiscoveryClassificationFailsSafe(t *testing.T) {
	root := t.TempDir()
	seedDir(t, root, "20260101-0000", "services.log", "cleanup.log") // a found run: details + the disable
	seedDir(t, root, "20260102-0000", "services.log", "notes.txt")
	seedDir(t, root, "20260103-0000", "services.log")
	if err := os.MkdirAll(filepath.Join(root, "20260103-0000", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	seedDir(t, root, "20260104-0000")
	seedDir(t, root, "20260105-0000", "services.log") // discovery-only, superseded below
	if err := storeAt(root, day(1)).Append(Services, "CHECK", "nothing", ""); err != nil {
		t.Fatal(err)
	}
	got := runDirs(t, root)
	want := []string{"20260101-0000", "20260102-0000", "20260103-0000", "20260104-0000", stampOf(day(1))}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("run dirs = %v, want %v", got, want)
	}
	for _, c := range []struct {
		files []string
		only  bool
	}{
		{[]string{"services.log"}, true},
		{[]string{"services.log", "repair.log"}, true},
		{[]string{"services.log", "audit.log"}, false},
		{[]string{"services.log.tmp"}, false},
		{nil, false},
	} {
		dir := t.TempDir()
		for _, f := range c.files {
			os.WriteFile(filepath.Join(dir, f), nil, 0o600)
		}
		if _, only := discoveryLogs(dir); only != c.only {
			t.Errorf("%v: discovery-only %v, want %v", c.files, only, c.only)
		}
	}
	if _, only := discoveryLogs(filepath.Join(root, "missing")); only {
		t.Error("an unreadable directory counted as discovery-only")
	}
}

// TestDiscoveryNewestRecordMayBeInARealRun: a real run directory holding the
// newest services record (a TUI session that checked after a cleanup)
// supersedes an older discovery-only one; a discovery-only directory that
// still holds the newest record of another action stays.
func TestDiscoveryNewestRecordMayBeInARealRun(t *testing.T) {
	root := t.TempDir()
	seedDir(t, root, "20260101-0000", "services.log")
	seedDir(t, root, "20260102-0000", "services.log", "downloads.log")
	s := storeAt(root, day(1))
	if err := s.Append(Cleanup, "OK", "x", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(Services, "CHECK", "nothing", ""); err != nil {
		t.Fatal(err)
	}
	got := runDirs(t, root)
	want := []string{"20260102-0000", stampOf(day(1))}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("run dirs = %v, want %v", got, want)
	}
}

// TestDiscoveryDryRunNeverPrunes: a dry run's discovery record goes to the
// dryrun slot and deletes nothing.
func TestDiscoveryDryRunNeverPrunes(t *testing.T) {
	root := t.TempDir()
	seedDir(t, root, "20260101-0000", "services.log")
	seedRuns(t, root, KeepRuns+1)
	s := New(Options{Dir: root, DryRun: true, KeepRuns: KeepRuns, Now: func() time.Time { return day(1) }})
	if err := s.Append(Services, "CHECK", "nothing", ""); err != nil {
		t.Fatal(err)
	}
	if got := runDirs(t, root); len(got) != KeepRuns+2 {
		t.Fatalf("a dry run pruned: %v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "dryrun", "services.log")); err != nil {
		t.Fatalf("dry-run record: %v", err)
	}
}
