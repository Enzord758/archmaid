package logstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// LastRunFile is the name of the file WriteLastRun maintains.
const LastRunFile = "last-run.json"

// Outcomes a RunRecord can carry.
const (
	OutcomeOK     = "ok"     // every step ran, or was declined at its own confirm, and succeeded
	OutcomeFailed = "failed" // at least one step failed
	// OutcomeIncomplete: nothing failed, but some steps never ran because
	// privilege was refused (sudo gave up after wrong passwords, the root
	// helper was refused); StepsRefused says how many. Not a clean run.
	OutcomeIncomplete = "incomplete"
	OutcomeAborted    = "aborted"   // stopped part-way (abort, or a declined required step)
	OutcomeCancelled  = "cancelled" // nothing ran: declined gate, cancelled password, auth gave up
)

// RunRecord summarises one finished queue, for readers that only want "how did
// the last run go" (the status line, the DMS widget).
type RunRecord struct {
	V            int       `json:"v"`      // schema version, 1
	Action       string    `json:"action"` // the menu action's id
	Started      time.Time `json:"started"`
	Finished     time.Time `json:"finished"`
	Outcome      string    `json:"outcome"` // ok | failed | incomplete | aborted | cancelled
	StepsFailed  int       `json:"steps_failed"`
	StepsSkipped int       `json:"steps_skipped"` // every step that didn't run, refused ones included
	// StepsRefused is the part of StepsSkipped that didn't run because
	// privilege was refused. Added within v1: a reader of an older record
	// sees 0.
	StepsRefused int  `json:"steps_refused"`
	DryRun       bool `json:"dry_run"`
}

// lastRunPath is where the record lives: <root>/last-run.json, except that a
// dry run uses <root>/dryrun/last-run.json so it can never overwrite the record
// of the last real run.
func (s *Store) lastRunPath() string {
	if s.run == "dryrun" {
		return filepath.Join(s.RunDir(), LastRunFile)
	}
	return filepath.Join(s.root, LastRunFile)
}

// WriteLastRun stores r atomically (temp file, fsync, rename: a reader sees the
// old or the new record, never a torn one), 0600 in a 0700 directory. V is set
// to 1. Times are encoded as RFC3339.
func (s *Store) WriteLastRun(r RunRecord) error {
	if s.err != nil {
		return s.err
	}
	r.V = 1
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := s.lastRunPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	os.Chmod(s.root, dirMode)
	os.Chmod(dir, dirMode)
	tmp, err := os.CreateTemp(dir, LastRunFile+".tmp*") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadLastRun returns the stored record. The error wraps os.ErrNotExist when
// none has been written yet.
func (s *Store) ReadLastRun() (RunRecord, error) {
	var r RunRecord
	if s.err != nil {
		return r, s.err
	}
	b, err := os.ReadFile(s.lastRunPath())
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	return r, err
}
