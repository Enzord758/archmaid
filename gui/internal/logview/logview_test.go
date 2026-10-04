package logview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// write logs an entry the way a real (or dry) run at t does.
func write(t *testing.T, dir string, at time.Time, dry bool, cat logstore.Category, label string) {
	t.Helper()
	st := logstore.New(logstore.Options{Dir: dir, DryRun: dry, Now: func() time.Time { return at }})
	if err := st.Append(cat, "OK", label, "\x1b[1mbold\x1b[0m output\n"); err != nil {
		t.Fatal(err)
	}
}

func TestRunsNewestFirstDryRunLast(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if runs, err := Runs(dir); err != nil || runs != nil {
		t.Fatalf("no state dir: %v %v", runs, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("listing created the state dir")
	}
	d1 := time.Date(2026, 10, 1, 9, 30, 0, 0, time.Local)
	d2 := time.Date(2026, 10, 3, 18, 5, 0, 0, time.Local)
	write(t, dir, d1, false, logstore.Topgrade, "Topgrade completed")
	write(t, dir, d2, false, logstore.Cleanup, "Clearing cache")
	write(t, dir, d2, false, logstore.Audit, "AUR audit")
	write(t, dir, d2, true, logstore.Orphans, "Orphans")
	// Not runs: a stray file, a user's own folder, a stamp-named file.
	must(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))
	must(t, os.Mkdir(filepath.Join(dir, "mine"), 0o700))
	must(t, os.WriteFile(filepath.Join(dir, "20260101-0000"), []byte("x"), 0o600))

	runs, err := Runs(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range runs {
		got = append(got, fmt.Sprintf("%s %v %v", r.Name, r.Categories, r.DryRun))
	}
	want := []string{"20261003-1805 [cleanup audit] false", "20261001-0930 [topgrade] false", "dryrun [orphans] true"}
	if !slices.Equal(got, want) {
		t.Errorf("runs %q, want %q", got, want)
	}
	if s := runs[0].Subtitle(); s != "Cleanup · AUR audit" {
		t.Errorf("subtitle %q", s)
	}
	if s := runs[2].Title(); s != "Dry runs" {
		t.Errorf("title %q", s)
	}
	lines, err := runs[1].Read(logstore.Topgrade)
	if err != nil || len(lines) != 4 || lines[2] != "bold output" {
		t.Errorf("read %q %v", lines, err)
	}
	if _, err := runs[1].Read(logstore.Orphans); !errors.Is(err, logstore.ErrEmpty) {
		t.Errorf("a missing log: %v, want ErrEmpty", err)
	}
}

// TestReadOnly: listing and reading every log leaves the state dir as it
// was, even with more runs than logstore keeps (nothing is pruned).
func TestReadOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	for i := range logstore.KeepRuns + 2 {
		write(t, dir, time.Date(2026, 9, 1+i, 12, 0, 0, 0, time.Local), false, logstore.Cleanup, "x")
	}
	for i := range 3 {
		must(t, os.MkdirAll(filepath.Join(dir, fmt.Sprintf("2025010%d-1200", i+1)), 0o700))
	}
	before := tree(t, dir)
	runs, err := Runs(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		for _, c := range logstore.Categories {
			r.Read(c.ID)
		}
	}
	if after := tree(t, dir); !slices.Equal(before, after) {
		t.Errorf("state dir changed:\n%q\n%q", before, after)
	}
	if len(runs) != logstore.KeepRuns+5 {
		t.Errorf("%d runs listed, want all %d", len(runs), logstore.KeepRuns+5)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprint(p, fi.ModTime(), fi.Mode(), fi.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
