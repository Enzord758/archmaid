package logstore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T, dir string) *Store {
	t.Helper()
	return New(Options{Dir: dir, Now: func() time.Time { return time.Date(2026, 9, 28, 0, 58, 0, 0, time.UTC) }})
}

func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestPaths(t *testing.T) {
	root := t.TempDir()
	s := newStore(t, root)
	if s.Root() != root || s.RunDir() != filepath.Join(root, "20260928-0058") {
		t.Fatalf("Root/RunDir = %q, %q", s.Root(), s.RunDir())
	}
	if got := s.Path(Audit); got != filepath.Join(root, "20260928-0058", "audit.log") {
		t.Fatalf("Path = %q", got)
	}
	d := New(Options{Dir: root, DryRun: true})
	if d.RunDir() != filepath.Join(root, "dryrun") {
		t.Fatalf("dry run dir = %q", d.RunDir())
	}
}

func TestLabels(t *testing.T) {
	if Audit.Label() != "AUR audit" || Category("odd").Label() != "odd" {
		t.Fatalf("labels: %q %q", Audit.Label(), Category("odd").Label())
	}
}

// TestReadCreatesNothing: reading is read-only. A run directory made by a mere
// read would take one of Prune's KeepRuns slots from a real run.
func TestReadCreatesNothing(t *testing.T) {
	s := newStore(t, t.TempDir())
	if _, err := s.Read(Cleanup); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Read of a missing log = %v, want ErrEmpty", err)
	}
	if _, err := os.Stat(s.RunDir()); !os.IsNotExist(err) {
		t.Fatalf("Read created the run directory (stat err = %v)", err)
	}
	if err := os.MkdirAll(s.RunDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path(Cleanup), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(Cleanup); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Read of an empty log = %v, want ErrEmpty", err)
	}
}

// TestModesPrivate: logs carry full command output, so a write leaves the
// state dir and run dir 0700 and the file 0600 — whether it creates them or
// finds them left 0755/0644 by an older version.
func TestModesPrivate(t *testing.T) {
	check := func(t *testing.T, s *Store) {
		t.Helper()
		for path, want := range map[string]fs.FileMode{
			s.Root():        0o700,
			s.RunDir():      0o700,
			s.Path(Cleanup): 0o600,
		} {
			if got := modeOf(t, path); got != want {
				t.Errorf("%s: mode %o, want %o", path, got, want)
			}
		}
	}
	t.Run("fresh", func(t *testing.T) {
		s := newStore(t, filepath.Join(t.TempDir(), "archmaid"))
		if err := s.Append(Cleanup, "OK", "x", ""); err != nil {
			t.Fatal(err)
		}
		check(t, s)
	})
	t.Run("existing", func(t *testing.T) {
		s := newStore(t, filepath.Join(t.TempDir(), "archmaid"))
		if err := os.MkdirAll(s.RunDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		os.Chmod(s.Root(), 0o755)
		if err := os.WriteFile(s.Path(Cleanup), []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(Cleanup, "OK", "x", ""); err != nil {
			t.Fatal(err)
		}
		check(t, s)
	})
}

// TestAppendFormat: an entry is a timestamped line, plus an output block when
// there is a body.
func TestAppendFormat(t *testing.T) {
	s := newStore(t, t.TempDir())
	s.Append(Cleanup, "OK", "plain", "")
	s.Append(Cleanup, "FAIL", "with body", "line\n")
	got, err := s.Read(Cleanup)
	if err != nil {
		t.Fatal(err)
	}
	want := "[2026-09-28 00:58:00] OK: plain\n" +
		"[2026-09-28 00:58:00] FAIL: with body\n--- with body output ---\nline\n--- end output ---\n"
	if got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}

// TestLogsAreSanitized: what a step streamed (colours, a progress bar, an OSC
// 52 clipboard write) lands in the log as plain text, and an old log that
// still holds raw escapes is read back plain.
func TestLogsAreSanitized(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := s.Append(Topgrade, "OK", "\x1b]0;t\x07Topgrade", "\x1b[1m:: \x1b[0mupgrading\n10%\r100%\n\x1b]52;c;ZXZpbA==\x07done\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path(Topgrade))
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if strings.ContainsAny(log, "\x1b\r\x07") {
		t.Fatalf("log still holds escapes or CRs: %q", log)
	}
	if !strings.Contains(log, ":: upgrading\n100%\ndone\n") || !strings.Contains(log, "OK: Topgrade\n") {
		t.Fatalf("log lost the text: %q", log)
	}

	old := "[2026-09-28 00:58:00] OK: Topgrade completed\n\x1b[32mok\x1b[0m\x1b]0;title\x07\n"
	if err := os.WriteFile(s.Path(Cleanup), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(Cleanup)
	if err != nil || strings.ContainsAny(got, "\x1b\x07") || !strings.Contains(got, "ok\n") {
		t.Fatalf("Read = %q, %v; want the old log as plain text", got, err)
	}
}

// TestPruneKeepsLastRuns: with more than keep run directories, only the oldest
// excess ones are removed; the dryrun slot and anything that isn't a run
// directory (stray files, a user's own subdirectories) survive untouched
// regardless of age or count.
func TestPruneKeepsLastRuns(t *testing.T) {
	root := t.TempDir()
	s := newStore(t, root)

	// 7 run directories, oldest to newest; only the newest 5 should survive.
	runs := []string{
		"20260101-0900", "20260102-0900", "20260103-0900", "20260104-0900",
		"20260105-0900", "20260106-0900", "20260107-0900",
	}
	for _, r := range runs {
		if err := os.MkdirAll(filepath.Join(root, r), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, r, "cleanup.log"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Not a run directory: never pruned, no matter how it's named or how old.
	if err := os.MkdirAll(filepath.Join(root, "dryrun"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.Prune(KeepRuns)

	for _, r := range runs[:2] {
		if _, err := os.Stat(filepath.Join(root, r)); !os.IsNotExist(err) {
			t.Fatalf("run dir %s should have been pruned (oldest beyond KeepRuns)", r)
		}
	}
	for _, r := range runs[2:] {
		if _, err := os.Stat(filepath.Join(root, r)); err != nil {
			t.Fatalf("run dir %s should have survived pruning: %v", r, err)
		}
	}
	for _, p := range []string{filepath.Join(root, "dryrun"), other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s should have survived pruning: %v", p, err)
		}
	}
}

// TestPruneNoopUnderLimit: fewer run directories than keep prunes nothing, and
// a missing root is not an error.
func TestPruneNoopUnderLimit(t *testing.T) {
	root := t.TempDir()
	run := filepath.Join(root, "20260101-0900")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	newStore(t, root).Prune(KeepRuns)
	if _, err := os.Stat(run); err != nil {
		t.Fatalf("sole run dir should survive when under KeepRuns: %v", err)
	}
	newStore(t, filepath.Join(root, "missing")).Prune(KeepRuns)
}

// seedRuns creates n run directories under root, oldest first, dated well
// before newStore's clock, each holding a log, and returns their names.
func seedRuns(t *testing.T, root string, n int) []string {
	t.Helper()
	var runs []string
	for i := range n {
		r := time.Date(2020, 1, 1+i, 9, 0, 0, 0, time.UTC).Format("20060102-1504")
		if err := os.MkdirAll(filepath.Join(root, r), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, r, "cleanup.log"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
	}
	return runs
}

// runDirs lists root's run directories, sorted.
func runDirs(t *testing.T, root string) []string {
	t.Helper()
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && runDirPattern.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestAppendRotatesWhenTheRunStarts: a store with KeepRuns prunes when its
// first Append creates the run directory, keeping KeepRuns runs including the
// new one, and does nothing before that or on later Appends.
func TestAppendRotatesWhenTheRunStarts(t *testing.T) {
	root := t.TempDir()
	old := seedRuns(t, root, KeepRuns+1)
	s := New(Options{Dir: root, KeepRuns: KeepRuns,
		Now: func() time.Time { return time.Date(2026, 9, 28, 0, 58, 0, 0, time.UTC) }})
	if _, err := s.Read(Cleanup); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Read: %v", err)
	}
	if got := runDirs(t, root); len(got) != KeepRuns+1 {
		t.Fatalf("New or Read pruned: %v", got)
	}
	if err := s.Append(Cleanup, "OK", "first", ""); err != nil {
		t.Fatal(err)
	}
	want := append(append([]string{}, old[2:]...), "20260928-0058")
	if got := runDirs(t, root); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("after the first Append: %v, want %v", got, want)
	}
	// A run dir appearing later (another archmaid) is not this run's to rotate.
	extra := seedRuns(t, root, 1) // re-creates 20200101-0900
	if err := s.Append(Cleanup, "OK", "second", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, extra[0])); err != nil {
		t.Fatalf("a later Append pruned: %v", err)
	}
}

// TestDryRunStoreNeverPrunes: a dry run leaves the real history alone, even
// with KeepRuns set and more runs than that on disk.
func TestDryRunStoreNeverPrunes(t *testing.T) {
	root := t.TempDir()
	seedRuns(t, root, KeepRuns+1)
	s := New(Options{Dir: root, DryRun: true, KeepRuns: KeepRuns})
	if err := s.Append(Cleanup, "DRYRUN", "would", ""); err != nil {
		t.Fatal(err)
	}
	if got := runDirs(t, root); len(got) != KeepRuns+1 {
		t.Fatalf("a dry run pruned the history: %v", got)
	}
}

// TestPruneSparesOwnRun: a clock set back makes this run sort oldest; Prune
// still never deletes the directory it is writing to.
func TestPruneSparesOwnRun(t *testing.T) {
	root := t.TempDir()
	seedRuns(t, root, KeepRuns+1)
	s := New(Options{Dir: root, KeepRuns: KeepRuns,
		Now: func() time.Time { return time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC) }})
	if err := s.Append(Cleanup, "OK", "x", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.RunDir()); err != nil {
		t.Fatalf("own run dir pruned: %v", err)
	}
}
