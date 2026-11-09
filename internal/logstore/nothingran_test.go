package logstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A run in which nothing ran (cancelled) is no "last run": the record of the
// last run that did something stays byte for byte, and with none, none appears
// (not even the state folder). Every other outcome is written as always.
func TestLastRunNothingRanKeepsThePreviousRecord(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 20, 0, 0, 0, time.UTC)
	for _, dry := range []bool{false, true} {
		name := "real"
		if dry {
			name = "dry"
		}
		t.Run(name+"/previous record", func(t *testing.T) {
			dir := t.TempDir()
			s := New(Options{Dir: dir, DryRun: dry})
			if err := s.WriteLastRun(RunRecord{Action: "fullcleanup", Started: t0, Finished: t0.Add(time.Minute), Outcome: OutcomeOK, DryRun: dry}); err != nil {
				t.Fatal(err)
			}
			path := s.lastRunPath()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range []RunRecord{
				{Action: "topgrade", Started: t0.Add(time.Hour), Finished: t0.Add(time.Hour), Outcome: OutcomeCancelled},
				{Action: "services", Started: t0.Add(2 * time.Hour), Finished: t0.Add(2 * time.Hour), Outcome: OutcomeCancelled, StepsSkipped: 3, StepsRefused: 3},
			} {
				if !r.NothingRan() {
					t.Fatalf("%+v should be NothingRan", r)
				}
				if err := s.WriteLastRun(r); err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) {
					t.Fatalf("last-run.json changed by a nothing-ran record: %s (%v), was %s", after, err, before)
				}
			}
			ents, _ := os.ReadDir(filepath.Dir(path))
			for _, e := range ents {
				if strings.Contains(e.Name(), ".tmp") {
					t.Errorf("left a temp file: %s", e.Name())
				}
			}
		})
		t.Run(name+"/no previous record", func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "state")
			s := New(Options{Dir: dir, DryRun: dry})
			if err := s.WriteLastRun(RunRecord{Action: "topgrade", Outcome: OutcomeCancelled}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ReadLastRun(); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("ReadLastRun err = %v, want ErrNotExist", err)
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the state folder was created: %v", err)
			}
		})
	}
	// Something ran, or looked: written.
	for _, o := range []string{OutcomeOK, OutcomeFailed, OutcomeIncomplete, OutcomeAborted, OutcomeNothingFound} {
		s := New(Options{Dir: t.TempDir()})
		s.WriteLastRun(RunRecord{Action: "old", Outcome: OutcomeOK})
		if err := s.WriteLastRun(RunRecord{Action: "new", Outcome: o}); err != nil {
			t.Fatal(err)
		}
		if r, err := s.ReadLastRun(); err != nil || r.Action != "new" || r.Outcome != o {
			t.Errorf("%s: record = %+v, %v", o, r, err)
		}
	}
	if (RunRecord{Outcome: OutcomeAborted}).NothingRan() {
		t.Error("an aborted run (something ran) is NothingRan")
	}
}
