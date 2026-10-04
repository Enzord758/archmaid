package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// ── maintainer snapshot ──────────────────────────────────────────────────────

// SnapshotFile is the snapshot's file name. The TUI keeps it in the state
// dir's root, not a run directory, so log pruning never touches it.
const SnapshotFile = "aur-maintainers.json"

// lockFile, beside the snapshot, serialises saves between processes.
const lockFile = "aur-maintainers.lock"

// Baseline is what LoadSnapshot found on disk.
type Baseline int

const (
	BaselineMissing Baseline = iota // no snapshot yet: the first audit
	BaselineOK
	// BaselineCorrupt: a snapshot exists but can't be read or parsed. It is
	// never treated as missing: that would silently re-baseline and wave
	// through exactly the maintainer change the snapshot exists to catch.
	BaselineCorrupt
)

// Entry is one package in the snapshot: its maintainer ("" = orphaned) and
// its co-maintainers (sorted, raw).
//
// On disk it is {"maintainer": "alice", "comaintainers": ["bob"]}. Snapshots
// written before co-maintainers were tracked hold a bare "alice" per package
// instead; that still loads, as an entry with HasCo false, and so does an
// object without "comaintainers". HasCo false means "co-maintainers never
// recorded": classify then treats them as a baseline (no alarm, like a first
// audit), and the next save records them. Without that, upgrading archmaid
// would gate every package that has a co-maintainer. An entry saved with
// HasCo false (a package the AUR didn't report, so its old entry is kept)
// leaves "comaintainers" out, so it reads back the same way.
type Entry struct {
	Maintainer    string
	CoMaintainers []string
	HasCo         bool
}

type entryJSON struct {
	Maintainer    *string   `json:"maintainer"`
	CoMaintainers *[]string `json:"comaintainers,omitempty"`
}

// MarshalJSON writes the current object format.
func (e Entry) MarshalJSON() ([]byte, error) {
	j := entryJSON{Maintainer: &e.Maintainer}
	if e.HasCo {
		co := append([]string{}, e.CoMaintainers...) // [] rather than null
		j.CoMaintainers = &co
	}
	return json.Marshal(j)
}

// UnmarshalJSON accepts the current object and the legacy bare string. Any
// other shape — null, a number, an object without "maintainer" — is an error,
// which makes the whole snapshot BaselineCorrupt.
func (e *Entry) UnmarshalJSON(b []byte) error {
	var legacy string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &legacy); err != nil {
			return err
		}
		*e = Entry{Maintainer: legacy}
		return nil
	}
	if len(b) == 0 || b[0] != '{' {
		return fmt.Errorf("snapshot entry is neither a maintainer nor an object: %.20s", b)
	}
	var j entryJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	if j.Maintainer == nil {
		return errors.New("snapshot entry without a maintainer")
	}
	*e = Entry{Maintainer: *j.Maintainer}
	if j.CoMaintainers != nil {
		e.CoMaintainers, e.HasCo = sortedNames(*j.CoMaintainers), true
	}
	return nil
}

// EntryFor is the snapshot entry for what the AUR reports now.
func EntryFor(ai Info) Entry {
	e := Entry{CoMaintainers: sortedNames(ai.CoMaintainers), HasCo: true}
	if ai.Maintainer != nil {
		e.Maintainer = *ai.Maintainer
	}
	return e
}

// sortedNames is names sorted, without duplicates (nil for none).
func sortedNames(names []string) []string {
	var out []string
	for n := range nameSet(names) {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// LoadSnapshot reads a name → Entry snapshot (see Entry for the format, old
// and new). Only a file that doesn't exist reads as BaselineMissing; any other
// read error, invalid JSON, a JSON null or an entry of the wrong shape is
// BaselineCorrupt, with no data.
func LoadSnapshot(path string) (map[string]Entry, Baseline) {
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, BaselineMissing
	case err != nil:
		return nil, BaselineCorrupt
	}
	var m map[string]Entry
	if json.Unmarshal(b, &m) != nil || m == nil {
		return nil, BaselineCorrupt
	}
	return m, BaselineOK
}

// snapshotFinding is the finding a corrupt snapshot turns into. Without a
// readable baseline no maintainer change can be detected, so the audit fails
// closed: high severity, gating the update like a takeover would.
func snapshotFinding() Finding {
	return Finding{
		Name: "maintainer snapshot", High: true,
		Flags:  []string{"unreadable: maintainer changes can't be checked, re-verify maintainers by hand"},
		Reason: "snapshot unreadable",
	}
}

// nextSnapshot is the snapshot to save: each audited package's current
// maintainer and co-maintainers, keeping the old entry for any package the
// AUR didn't report this time (a transient removal must not erase its
// history). This is also where an entry from an old-format snapshot gets its
// co-maintainers recorded.
func nextSnapshot(pkgs []string, info map[string]Info, prev map[string]Entry) map[string]Entry {
	next := make(map[string]Entry, len(pkgs))
	for _, name := range pkgs {
		if ai, ok := info[name]; ok {
			next[name] = EntryFor(ai)
		} else if p, ok := prev[name]; ok {
			next[name] = p
		}
	}
	return next
}

// ErrSnapshotLocked: another archmaid holds the snapshot lock, so this save
// is skipped rather than racing it.
var ErrSnapshotLocked = errors.New("another archmaid is saving the AUR snapshot")

// WriteSnapshot replaces the snapshot at path atomically, under an exclusive
// lock: a 0600 temp file in the same directory is written, fsynced and
// renamed over the snapshot, then the directory is fsynced, so a crash leaves
// the old snapshot or the new one, never a torn file. A corrupt snapshot on
// disk is first renamed to <path>.corrupt-<time>, kept for inspection rather
// than overwritten. A held lock returns ErrSnapshotLocked unwritten.
func WriteSnapshot(path string, next map[string]Entry) error {
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := lockSnapshot(dir)
	if err != nil {
		return err
	}
	defer unlock()

	if _, state := LoadSnapshot(path); state == BaselineCorrupt {
		aside := path + ".corrupt-" + time.Now().Format("20060102-150405")
		if err := os.Rename(path, aside); err != nil {
			return err
		}
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*") // mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails harmlessly once renamed
	if _, err := tmp.Write(b); err != nil {
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
	return d.Sync() // make the rename itself durable
}

// lockSnapshot takes an exclusive, non-blocking flock on lockFile in dir
// (left on disk: deleting a lock file races its next user) and returns the
// unlock func, or ErrSnapshotLocked if another process holds it.
func lockSnapshot(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrSnapshotLocked
		}
		return nil, err
	}
	return func() { f.Close() }, nil // closing the fd releases the flock
}
