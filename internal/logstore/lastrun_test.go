package logstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLastRunRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s := New(Options{Dir: dir})
	if _, err := s.ReadLastRun(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent: err = %v, want ErrNotExist", err)
	}
	t0 := time.Date(2026, 10, 4, 17, 48, 40, 0, time.FixedZone("x", 7200))
	in := RunRecord{Action: "fullcleanup", Started: t0, Finished: t0.Add(time.Minute),
		Outcome: OutcomeFailed, StepsFailed: 1, StepsSkipped: 2, StepsRefused: 1}
	if err := s.WriteLastRun(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadLastRun()
	if err != nil {
		t.Fatal(err)
	}
	if got.V != 1 || got.Action != in.Action || !got.Started.Equal(in.Started) ||
		!got.Finished.Equal(in.Finished) || got.Outcome != in.Outcome ||
		got.StepsFailed != 1 || got.StepsSkipped != 2 || got.StepsRefused != 1 || got.DryRun {
		t.Fatalf("round trip = %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "last-run.json"))
	if !strings.Contains(string(raw), `"v":1`) || !strings.Contains(string(raw), `"steps_refused":1`) || !strings.Contains(string(raw), "2026-10-04T17:48:40+02:00") {
		t.Fatalf("raw = %s", raw)
	}
	for p, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "last-run.json"): 0o600} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != want {
			t.Fatalf("%s mode = %v (%v), want %o", p, fi.Mode(), err, want)
		}
	}
}

func TestLastRunAtomicNoLeftovers(t *testing.T) {
	dir := t.TempDir()
	s := New(Options{Dir: dir})
	for i := 0; i < 3; i++ {
		if err := s.WriteLastRun(RunRecord{Outcome: OutcomeOK, StepsSkipped: i}); err != nil {
			t.Fatal(err)
		}
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || ents[0].Name() != "last-run.json" {
		t.Fatalf("dir = %v, want only last-run.json (no temp files)", ents)
	}
	if r, _ := s.ReadLastRun(); r.StepsSkipped != 2 {
		t.Fatalf("not replaced: %+v", r)
	}
	// A too-open file from elsewhere is replaced by a 0600 one.
	os.Chmod(filepath.Join(dir, "last-run.json"), 0o644)
	s.WriteLastRun(RunRecord{Outcome: OutcomeOK})
	if fi, _ := os.Stat(filepath.Join(dir, "last-run.json")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode())
	}
}

func TestLastRunDryRunUsesItsOwnSlot(t *testing.T) {
	dir := t.TempDir()
	real := New(Options{Dir: dir})
	real.WriteLastRun(RunRecord{Outcome: OutcomeOK, Action: "real"})
	dry := New(Options{Dir: dir, DryRun: true})
	if err := dry.WriteLastRun(RunRecord{Outcome: OutcomeFailed, Action: "dry", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if r, _ := real.ReadLastRun(); r.Action != "real" {
		t.Fatalf("dry run clobbered the real record: %+v", r)
	}
	if r, err := dry.ReadLastRun(); err != nil || r.Action != "dry" {
		t.Fatalf("dry record = %+v, %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dryrun", "last-run.json")); err != nil {
		t.Fatal(err)
	}
}

// A record written before steps_refused existed still reads, as 0 refused:
// the field is additive within v1.
func TestLastRunOldRecordWithoutRefused(t *testing.T) {
	dir := t.TempDir()
	old := `{"v":1,"action":"fullcleanup","started":"2026-10-04T17:48:40+02:00",` +
		`"finished":"2026-10-04T17:49:40+02:00","outcome":"ok","steps_failed":0,"steps_skipped":1,"dry_run":false}`
	if err := os.WriteFile(filepath.Join(dir, LastRunFile), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := New(Options{Dir: dir}).ReadLastRun()
	if err != nil || r.V != 1 || r.Outcome != OutcomeOK || r.StepsSkipped != 1 || r.StepsRefused != 0 {
		t.Fatalf("old record = %+v, %v", r, err)
	}
}
