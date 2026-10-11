package logstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteLastRunCreateTempFails: a record that can't be written is an
// error, not a silent loss. /proc takes no new files, whoever asks; root is
// skipped because WriteLastRun would try to chmod it.
func TestWriteLastRunCreateTempFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("WriteLastRun chmods its root, which must not be /proc as root")
	}
	s := New(Options{Dir: "/proc"})
	if err := s.WriteLastRun(RunRecord{Outcome: OutcomeOK}); err == nil {
		t.Fatal("WriteLastRun into /proc succeeded")
	}
}

// TestPruneDiscoveryOnABrokenStore: a store whose root was refused (a relative
// dir) neither prunes nor creates anything.
func TestPruneDiscoveryOnABrokenStore(t *testing.T) {
	s := New(Options{Dir: "relative-state-dir", KeepRuns: 1})
	if s.err == nil {
		t.Fatal("a relative dir must make a broken store")
	}
	s.pruneDiscovery()
	s.Prune(1)
	if _, err := os.Stat("relative-state-dir"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a broken store touched its relative dir: %v", err)
	}
}

// TestLogPathThatIsADirectory: when the log's path is a directory, Append and
// Read say so (the OS error) rather than ErrEmpty or success.
func TestLogPathThatIsADirectory(t *testing.T) {
	s := newStore(t, t.TempDir())
	if err := os.MkdirAll(s.Path(Audit), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(Audit, "step", "label", "body\n"); err == nil {
		t.Error("Append onto a directory succeeded")
	}
	if _, err := s.Read(Audit); err == nil || errors.Is(err, ErrEmpty) {
		t.Errorf("Read of a directory: err = %v, want a read error that is not ErrEmpty", err)
	}
	if _, err := os.Stat(filepath.Join(s.Path(Audit))); err != nil {
		t.Errorf("the directory is gone: %v", err)
	}
}

// TestStateDirBelowAFile: a state directory that can't be created (a path
// component is a file) fails Append and WriteLastRun with the OS error, and
// leaves the file as it was.
func TestStateDirBelowAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newStore(t, filepath.Join(blocker, "state"))
	if err := s.Append(Audit, "step", "label", ""); err == nil {
		t.Error("Append below a file succeeded")
	}
	if err := s.WriteLastRun(RunRecord{Outcome: OutcomeOK}); err == nil {
		t.Error("WriteLastRun below a file succeeded")
	}
	if b, _ := os.ReadFile(blocker); string(b) != "x" {
		t.Errorf("the blocking file changed: %q", b)
	}
}
