package audit

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// StateFile is audit-state.json's name, in the state dir's root beside the
// snapshot. It is the contract the DMS widget reads (docs/dms-widget.md,
// section 3): the high AUR findings that are not yet acknowledged, i.e. still
// held back from the snapshot.
const StateFile = "audit-state.json"

// StateVersion is State.V. Adding fields keeps it; removing or renaming one
// bumps it.
const StateVersion = 1

// maxStateFile caps ReadState; a real file is a few hundred bytes.
const maxStateFile = 1 << 20

// reasonMax caps a StateFinding's reason.
const reasonMax = 120

// State is audit-state.json.
//
// High lists every unacknowledged high finding (the widget's "N AUR
// warnings"); a corrupt snapshot is one of them ("snapshot unreadable").
// Checked is when the last completed audit ran. SnapshotPresent and
// SnapshotReadable describe the maintainer snapshot as that audit (or the
// commit that followed it) left it.
type State struct {
	V                int            `json:"v"`
	Checked          time.Time      `json:"checked"`
	Packages         int            `json:"packages"`
	High             []StateFinding `json:"high"`
	SnapshotPresent  bool           `json:"snapshot_present"`
	SnapshotReadable bool           `json:"snapshot_readable"`
}

// StateFinding is one unacknowledged high finding: the package (sanitized)
// and a short sanitized reason such as "maintainer alice → mallory",
// "co-maintainer bob added" or "snapshot unreadable".
type StateFinding struct {
	Pkg    string `json:"pkg"`
	Reason string `json:"reason"`
}

// WriteState replaces the file at path atomically: a 0600 temp file in the
// same directory is written, fsynced and renamed over it, then the directory
// is fsynced, so a crash leaves the old state or the new one, never a torn
// file. High is written as [] rather than null.
func WriteState(path string, s State) error {
	if s.High == nil {
		s.High = []StateFinding{}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*") // mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails harmlessly once renamed
	if _, err := tmp.Write(append(b, '\n')); err != nil {
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
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// ReadState reads the file at path (size-capped). A missing file is an error
// satisfying errors.Is(err, fs.ErrNotExist).
func ReadState(path string) (State, error) {
	f, err := os.Open(path)
	if err != nil {
		return State{}, err
	}
	defer f.Close()
	var s State
	if err := json.NewDecoder(io.LimitReader(f, maxStateFile)).Decode(&s); err != nil {
		return State{}, err
	}
	return s, nil
}

// state is what the audit says right now: every high finding is unacknowledged
// until a commit that accepts them runs (see stateAfterCommit).
func (r Result) state() State {
	s := State{
		V: StateVersion, Checked: r.checked, Packages: r.Total, High: []StateFinding{},
		SnapshotPresent:  r.Baseline != BaselineMissing,
		SnapshotReadable: r.Baseline != BaselineCorrupt,
	}
	for _, f := range r.Findings {
		if f.High {
			s.High = append(s.High, StateFinding{
				Pkg:    sanitize.Field(f.Name, FieldMax),
				Reason: sanitize.Field(f.Reason, reasonMax),
			})
		}
	}
	return s
}

// stateAfterCommit is the state once save(holdHigh) succeeded. Held highs stay
// listed (and a corrupt snapshot, left alone, stays unreadable); otherwise
// they are acknowledged: cleared, and the snapshot now exists and reads.
func (r Result) stateAfterCommit(holdHigh bool) State {
	s := r.state()
	if holdHigh && r.Baseline == BaselineCorrupt {
		return s
	}
	s.SnapshotPresent, s.SnapshotReadable = true, true
	if !holdHigh {
		s.High = []StateFinding{}
	}
	return s
}

// writeState records s unless there is no StatePath or this is a dry run (a
// dry run never touches the real state, like it never advances the snapshot).
// A failed write is logged (WARN) and returned: the state is advisory and
// never fails an audit.
func (r Result) writeState(s State) error {
	if r.cfg.StatePath == "" || r.cfg.DryRun {
		return nil
	}
	if err := WriteState(r.cfg.StatePath, s); err != nil {
		r.cfg.log("WARN", "AUR audit state not saved: "+err.Error(), "")
		return err
	}
	return nil
}
