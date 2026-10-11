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
	"slices"
	"sort"
	"strconv"
	"strings"
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

// The discovery categories: one per menu action that looks before it acts,
// holding the record of what its discovery checked (the commands run, what
// they listed, what was skipped and why, errors). A run directory that holds
// nothing but these is a discovery-only run: the action ended at its
// discovery (nothing found, or the look failed). Such runs never count
// towards KeepRuns; only the newest record of each action is kept (see
// Append).
const (
	Services  Category = "services"  // Disable unused services
	Repair    Category = "repair"    // Repair broken packages: the scan
	Downloads Category = "downloads" // Delete old downloads
	AURAudit  Category = "auraudit"  // Audit AUR packages, from the menu
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
	{Services, "Services check"},
	{Repair, "Package check"},
	{Downloads, "Downloads check"},
	{AURAudit, "AUR audit check"},
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

// Valid reports whether c is one of the eight categories. The set is fixed
// here, not read from Categories (an exported var a caller could append to):
// a category names a file, so "../x" must never become one.
func (c Category) Valid() bool {
	switch c {
	case Topgrade, Orphans, Cleanup, Audit:
		return true
	}
	return c.Discovery()
}

// Discovery reports whether c is a discovery category (Services, Repair,
// Downloads, AURAudit).
func (c Category) Discovery() bool {
	switch c {
	case Services, Repair, Downloads, AURAudit:
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

// KeepRuns is how many run directories a real run keeps (Options.KeepRuns);
// older ones are removed entirely. The dryrun directory is exempt: it is a single reused
// slot, not a growing history. So are discovery-only run directories (only
// discovery logs in them): they don't count towards KeepRuns, and each is
// kept only while it holds the newest record of one of its actions.
const KeepRuns = 5

// runDirPattern matches a run directory name (the run stamp's own format,
// with the two-digit "-02".."-99" a run gets when its minute's directory is
// taken, see Options.Fresh; zero-padded so the names still sort in order),
// so Prune only ever touches directories the store created itself.
var runDirPattern = regexp.MustCompile(`^\d{8}-\d{4}(-\d{2})?$`)

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
	// Fresh makes this store a new run even when another run started in
	// the same minute: if the minute's directory exists, the run is
	// <stamp>-02 (then -03, ...), so two actions started within one minute
	// don't share a folder. Without it, the name is the stamp alone, which
	// is what a reader of an existing run (and a store re-made for one
	// run) wants. A dry-run store ignores it. New only looks; it creates
	// nothing.
	Fresh bool
	// Run names an existing run directory (a reader's handle for one that
	// has a "-02" suffix); it overrides the stamp when it is a run
	// directory's name.
	Run string
	// KeepRuns, when positive, rotates the run history: the Append that
	// makes this run's directory a real run (its first entry in a
	// non-discovery category, whether it creates the directory or finds it
	// holding only discovery records) then prunes to the KeepRuns newest
	// real runs (this run included); an Append to a discovery category
	// prunes the discovery-only directories a newer record supersedes.
	// Zero never prunes. A dry-run store never prunes: its entries go to the
	// dryrun slot, and a dry run must leave the real history exactly as it
	// found it.
	KeepRuns int
}

// Store writes and reads one run's logs.
type Store struct {
	root string // absolute; "" when err is set
	run  string // run directory name
	now  func() time.Time
	keep int   // prune to this many runs when Append creates the run dir; 0: never
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
	switch {
	case runDirPattern.MatchString(o.Run):
		run = o.Run
	case o.Fresh && !o.DryRun && err == nil:
		run = freeRun(root, run)
	}
	keep := max(o.KeepRuns, 0)
	if o.DryRun {
		run, keep = "dryrun", 0
	}
	return &Store{root: root, run: run, now: now, keep: keep, err: err}
}

// freeRun is stamp when no directory of that name exists under root, else
// <stamp>-NN, one past the highest NN taken (02 for the first repeat; names
// pruned from the bottom are not reused, so the names still sort as the runs
// came). Past 99 it is the stamp: a hundred runs in a minute share.
func freeRun(root, stamp string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return stamp
	}
	next := 0 // the highest NN taken; 1 stands for the bare stamp
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), stamp) || !runDirPattern.MatchString(e.Name()) {
			continue
		}
		n := 1
		if rest := strings.TrimPrefix(e.Name(), stamp); rest != "" {
			n, _ = strconv.Atoi(strings.TrimPrefix(rest, "-"))
		}
		next = max(next, n)
	}
	if next == 0 {
		return stamp
	}
	if next >= 99 {
		return stamp
	}
	return fmt.Sprintf("%s-%02d", stamp, next+1)
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

// Prune deletes all but the keep most recent real run directories. A no-op
// if the root doesn't exist yet. Discovery-only run directories (see
// KeepRuns) neither count nor go; the dryrun directory, this store's own run
// directory (even if a clock set back makes it sort oldest) and anything
// that isn't a run directory (stray files, a user's own subdirectories) are
// left alone.
//
// Nothing in archmaid calls Prune at startup: deleting history is a real
// run's business, so it happens from Append (see Options.KeepRuns).
func (s *Store) Prune(keep int) {
	if s.err != nil {
		return
	}
	var runs []string
	for _, name := range s.runDirs() {
		if _, only := discoveryLogs(filepath.Join(s.root, name)); !only {
			runs = append(runs, name)
		}
	}
	if len(runs) <= keep {
		return
	}
	for _, name := range runs[:len(runs)-keep] {
		if name == s.run {
			continue
		}
		os.RemoveAll(filepath.Join(s.root, name))
	}
}

// pruneDiscovery deletes every discovery-only run directory whose records
// are all superseded: for each discovery log it holds, a newer run directory
// (real or discovery-only) holds that action's log too. So the newest record
// of each action survives, and at most one discovery-only directory per
// action. This store's own run directory is never deleted.
func (s *Store) pruneDiscovery() {
	if s.err != nil {
		return
	}
	runs := s.runDirs()
	seen := map[Category]bool{}
	for i := len(runs) - 1; i >= 0; i-- { // newest first
		dir := filepath.Join(s.root, runs[i])
		cats, only := discoveryLogs(dir)
		if only && runs[i] != s.run && !slices.ContainsFunc(cats, func(c Category) bool { return !seen[c] }) {
			os.RemoveAll(dir)
			continue
		}
		for _, c := range cats {
			seen[c] = true
		}
	}
}

// runDirs lists the run directories under the root, oldest first (the stamp
// format sorts lexicographically = chronologically).
func (s *Store) runDirs() []string {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	var runs []string
	for _, e := range entries {
		if e.IsDir() && runDirPattern.MatchString(e.Name()) {
			runs = append(runs, e.Name())
		}
	}
	sort.Strings(runs)
	return runs
}

// discoveryLogs reads the run directory dir: the discovery categories whose
// log it holds, and whether it is discovery-only — at least one discovery
// log and nothing else at all. Anything it can't read or doesn't know (a
// real category's log, a stray file, a subdirectory) makes it a real run,
// so a doubt never exposes a real run to the discovery pruning; an empty
// directory is real too.
func discoveryLogs(dir string) (cats []Category, only bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	only = len(entries) > 0
	for _, e := range entries {
		c := Category(strings.TrimSuffix(e.Name(), ".log"))
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".log") && c.Discovery() {
			cats = append(cats, c)
			continue
		}
		only = false
	}
	return cats, only
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
	_, statErr := os.Stat(run)
	// The history rotates once the entry is in (see Options.KeepRuns): a
	// real entry in a new directory, or in one that held only discovery
	// records, makes this a real run; a discovery entry may supersede an
	// older discovery-only run.
	becomesReal := false
	if s.keep > 0 && !cat.Discovery() {
		_, only := discoveryLogs(run)
		becomesReal = errors.Is(statErr, os.ErrNotExist) || only
	}
	defer func() {
		switch {
		case s.keep > 0 && cat.Discovery():
			s.pruneDiscovery()
		case becomesReal:
			s.Prune(s.keep)
		}
	}()
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
