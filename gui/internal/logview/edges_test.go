package logview

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// A run directory with no non-empty log has no categories: its subtitle
// says so, and the empty log files are not counted.
func TestRunWithoutLogs(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "20261001-0930"), 0o700))
	must(t, os.WriteFile(filepath.Join(dir, "20261001-0930", "cleanup.log"), nil, 0o600))
	runs, err := Runs(dir)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %v %v", runs, err)
	}
	if len(runs[0].Categories) != 0 {
		t.Errorf("categories %v, an empty log is no log", runs[0].Categories)
	}
	if s := runs[0].Subtitle(); s != "No logs" {
		t.Errorf("subtitle %q, want No logs", s)
	}
}

// Without a usable state directory (no HOME, no dir given) Runs says so
// instead of listing the working directory.
func TestRunsNoStateDir(t *testing.T) {
	t.Setenv("HOME", "")
	runs, err := Runs("")
	if !errors.Is(err, logstore.ErrNoStateDir) || runs != nil {
		t.Errorf("runs %v, err %v, want ErrNoStateDir", runs, err)
	}
}

// A state "directory" that is a file is an error from Runs, not "no runs".
func TestRunsUnreadableStateDir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "state")
	must(t, os.WriteFile(f, []byte("x"), 0o600))
	runs, err := Runs(f)
	if err == nil || errors.Is(err, os.ErrNotExist) || runs != nil {
		t.Errorf("runs %v, err %v, want a read error", runs, err)
	}
}

// A directory shaped like a stamp that is no time (month 13) is skipped.
func TestRunsSkipsImpossibleStamp(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "20261399-9999"), 0o700))
	write(t, dir, time.Date(2026, 10, 1, 9, 30, 0, 0, time.Local), false, logstore.Cleanup, "x")
	runs, err := Runs(dir)
	if err != nil || len(runs) != 1 || runs[0].Name != "20261001-0930" {
		t.Errorf("runs %v %v, want only the real one", runs, err)
	}
}

// A stamp that falls in a DST gap parses to another minute, so it is not
// a run this store could read: skipped.
func TestRunsSkipsDSTGapStamp(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata: ", err)
	}
	old := time.Local
	time.Local = ny
	t.Cleanup(func() { time.Local = old })
	dir := t.TempDir()
	// 2026-03-08 02:30 does not exist in New York.
	must(t, os.Mkdir(filepath.Join(dir, "20260308-0230"), 0o700))
	must(t, os.Mkdir(filepath.Join(dir, "20260308-0430"), 0o700))
	runs, err := Runs(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range runs {
		names = append(names, r.Name)
	}
	if len(names) != 1 || names[0] != "20260308-0430" {
		t.Errorf("runs %v, want only 20260308-0430", names)
	}
}

// The zero Run has no store: reading it is ErrEmpty, never a panic.
func TestReadZeroRun(t *testing.T) {
	if _, err := (Run{}).Read(logstore.Cleanup); !errors.Is(err, logstore.ErrEmpty) {
		t.Errorf("err %v, want ErrEmpty", err)
	}
}
