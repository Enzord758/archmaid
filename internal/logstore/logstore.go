// Package logstore is archmaid's on-disk log: one directory per run under the
// state dir, one file per category, private to the user. It is UI-free; the TUI
// renders what Read returns.
package logstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// ErrEmpty is returned by Read when a category has no log yet (or an empty one).
var ErrEmpty = errors.New("log is empty")

// Category buckets a step's log entries so one run's output doesn't land in a
// single undifferentiated file. Every step falls into exactly one bucket.
type Category string

const (
	Topgrade Category = "topgrade"
	Orphans  Category = "orphans"
	Cleanup  Category = "cleanup" // default: everything else
	Audit    Category = "audit"   // AUR audit results
)

// CategoryInfo is a category with its display label.
type CategoryInfo struct {
	ID    Category
	Label string
}

// Categories is every category in display order (the "View log" picker).
var Categories = []CategoryInfo{
	{Topgrade, "Topgrade"},
	{Orphans, "Orphan removal"},
	{Cleanup, "Cleanup"},
	{Audit, "AUR audit"},
}

// Label is c's display name, falling back to the raw value for any category
// that isn't in Categories (shouldn't happen in practice).
func (c Category) Label() string {
	for _, ci := range Categories {
		if ci.ID == c {
			return ci.Label
		}
	}
	return string(c)
}

// Valid reports whether c is one of the four categories. The set is fixed
// here, not read from Categories (an exported var a caller could append to):
// a category names a file, so "../x" must never become one.
func (c Category) Valid() bool {
	switch c {
	case Topgrade, Orphans, Cleanup, Audit:
		return true
	}
	return false
}

// ErrUnknownCategory refuses a category that isn't Valid.
var ErrUnknownCategory = errors.New("logstore: unknown log category")

// ErrNoStateDir means the store has nowhere safe to write: Options.Dir was
// relative, or it was empty and the home folder is unset or relative. Such a
// store refuses every read and write rather than use a path relative to
// whatever the working directory happens to be.
var ErrNoStateDir = errors.New("logstore: no absolute state directory (home folder unset or relative)")

// KeepRuns is how many run directories the startup prune keeps; older ones are
// removed entirely. The dryrun directory is exempt: it is a single reused
// slot, not a growing history.
const KeepRuns = 5

// runDirPattern matches a run directory name (the run stamp's own format), so
// Prune only ever touches directories the store created itself.
var runDirPattern = regexp.MustCompile(`^\d{8}-\d{4}$`)

// Logs hold full command output (package lists, paths, AUR maintainers), so
// they're private to the user. Directories and files created before this was
// tightened are fixed up on the next write.
const (
	dirMode  = 0o700
	fileMode = 0o600
)

// Options configures a Store.
type Options struct {
	// Dir is the state directory, an absolute path. Empty means
	// ~/.local/state/archmaid, a user-writable location that needs no root.
	// A relative Dir, or an empty one with no absolute home folder, gives a
	// store that refuses everything (ErrNoStateDir).
	Dir string
	// DryRun sends every entry to the single reused "dryrun" run directory.
	DryRun bool
	// Now is the clock; nil means time.Now. It names the run directory
	// (YYYYMMDD-HHMM, read once by New) and stamps each entry.
	Now func() time.Time
}

// Store writes and reads one run's logs.
type Store struct {
	root string // absolute; "" when err is set
	run  string // run directory name
	now  func() time.Time
	err  error // ErrNoStateDir: the store refuses everything
}

// New returns a Store for Options. It creates nothing on disk. Without an
// absolute state directory (see Options.Dir) the store is unusable: every
// write and read returns ErrNoStateDir, Prune does nothing, and Root, RunDir
// and Path are "" — never a path relative to the working directory.
func New(o Options) *Store {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	root := o.Dir
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
			root = filepath.Join(home, ".local", "state", "archmaid")
		}
	}
	var err error
	if !filepath.IsAbs(root) {
		root, err = "", ErrNoStateDir
	}
	run := now().Format("20060102-1504")
	if o.DryRun {
		run = "dryrun"
	}
	return &Store{root: root, run: run, now: now, err: err}
}

// Root is the state directory ("" for a store without one).
func (s *Store) Root() string { return s.root }

// RunDir is the directory this run's entries land under: <root>/dryrun (reused
// and appended across dry runs) or <root>/<run timestamp>. "" for a store
// without a state directory.
func (s *Store) RunDir() string {
	if s.err != nil {
		return ""
	}
	return filepath.Join(s.root, s.run)
}

// Path is the file cat logs to for this run: <RunDir>/<category>.log. It
// creates nothing: reading a log must not leave an empty run directory behind,
// which would take one of Prune's KeepRuns slots from a real run. Append
// creates the directory when there is something to write. It is "" for a
// category that isn't Valid, and for a store without a state directory.
func (s *Store) Path(cat Category) string {
	if s.check(cat) != nil {
		return ""
	}
	return filepath.Join(s.RunDir(), string(cat)+".log")
}

// check is the error reading or writing cat's log gets, or nil.
func (s *Store) check(cat Category) error {
	if s.err != nil {
		return s.err
	}
	if !cat.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownCategory, string(cat))
	}
	return nil
}

// Prune deletes all but the keep most recent run directories. A no-op if the
// root doesn't exist yet. The dryrun directory and anything that isn't a run
// directory (stray files, a user's own subdirectories) are left alone.
func (s *Store) Prune(keep int) {
	if s.err != nil {
		return
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	var runs []string
	for _, e := range entries {
		if e.IsDir() && runDirPattern.MatchString(e.Name()) {
			runs = append(runs, e.Name())
		}
	}
	if len(runs) <= keep {
		return
	}
	sort.Strings(runs) // the stamp format sorts lexicographically = chronologically
	for _, name := range runs[:len(runs)-keep] {
		os.RemoveAll(filepath.Join(s.root, name))
	}
}

// Append writes a timestamped entry to cat's log. kind is a short tag (FAIL,
// DRYRUN, SKIP, OK). If body is non-empty it's wrapped in an output block.
// label and body are written as plain text: a log must not replay escapes in
// whatever terminal views it later (topgrade's colours, or anything an AUR
// build printed).
func (s *Store) Append(cat Category, kind, label, body string) error {
	if err := s.check(cat); err != nil {
		return err
	}
	path := s.Path(cat)
	run := filepath.Dir(path)
	if err := os.MkdirAll(run, dirMode); err != nil {
		return err
	}
	os.Chmod(s.root, dirMode) // MkdirAll leaves existing dirs as they were
	os.Chmod(run, dirMode)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	defer f.Close()
	f.Chmod(fileMode) // a file from an older version may still be 0644
	label, body = sanitize.String(label, sanitize.Plain), sanitize.String(body, sanitize.Plain)
	ts := s.now().Format("2006-01-02 15:04:05")
	if body == "" {
		_, err = fmt.Fprintf(f, "[%s] %s: %s\n", ts, kind, label)
		return err
	}
	_, err = fmt.Fprintf(f, "[%s] %s: %s\n--- %s output ---\n%s--- end output ---\n",
		ts, kind, label, label, body)
	return err
}

// Read returns cat's log for this run as plain text (logs from older versions
// hold raw escapes). ErrEmpty means there is nothing to show: no file, or an
// empty one; any other error is a failed read.
func (s *Store) Read(cat Category) (string, error) {
	if err := s.check(cat); err != nil {
		return "", err
	}
	b, err := os.ReadFile(s.Path(cat))
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(b) == 0) {
		return "", ErrEmpty
	}
	if err != nil {
		return "", err
	}
	return sanitize.String(string(b), sanitize.Plain), nil
}
