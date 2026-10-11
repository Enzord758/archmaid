// Package logview is the Logs page's state, in plain Go: the run history
// under archmaid's state directory and each run's category logs, read
// through internal/logstore. It only ever reads: it never creates, deletes
// or rotates anything (rotation is a real run's business, logstore's
// KeepRuns).
package logview

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// DryRunName is the dry runs' single reused run directory.
const DryRunName = "dryrun"

// runName is a run directory's name: logstore's stamp, YYYYMMDD-HHMM.
var runName = regexp.MustCompile(`^\d{8}-\d{4}(-\d{2})?$`)

const stamp = "20060102-1504"

// Run is one run directory.
type Run struct {
	Name   string    // "20261004-1200", or "dryrun"
	Time   time.Time // the stamp, local time; zero for the dry-run slot
	DryRun bool
	// Categories are the logs this run has, in logstore.Categories' order.
	Categories []logstore.Category
	// Modified is the newest modification time of its logs (zero: none).
	Modified time.Time
	store    *logstore.Store
}

// Title is the run's row title: its time, or "Dry runs".
func (r Run) Title() string {
	if r.DryRun {
		return "Dry runs"
	}
	return r.Time.Format("Mon 2 Jan 2006, 15:04")
}

// Subtitle names the logs it has.
func (r Run) Subtitle() string {
	var l []string
	for _, c := range r.Categories {
		l = append(l, c.Label())
	}
	if len(l) == 0 {
		return "No logs"
	}
	return strings.Join(l, " · ")
}

// Runs lists the run directories under stateDir ("" is logstore's
// default), newest first, with the dry-run slot last. No state directory
// yet is no runs, not an error.
func Runs(stateDir string) ([]Run, error) {
	root := logstore.New(logstore.Options{Dir: stateDir}).Root()
	if root == "" {
		return nil, logstore.ErrNoStateDir
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []Run
	var dry *Run
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var r Run
		switch name := e.Name(); {
		case name == DryRunName:
			r = Run{Name: name, DryRun: true, store: logstore.New(logstore.Options{Dir: root, DryRun: true})}
		case runName.MatchString(name):
			// <stamp>, or <stamp>-NN for the NNth run of that minute.
			t, err := time.ParseInLocation(stamp, name[:len(stamp)], time.Local)
			if err != nil || t.Format(stamp) != name[:len(stamp)] {
				continue // a stamp that doesn't round-trip (a DST gap)
			}
			// A store whose clock reads the stamp is that run's store:
			// its RunDir is this directory, and Read reads its logs.
			r = Run{Name: name, Time: t, store: logstore.New(logstore.Options{Dir: root, Run: name, Now: func() time.Time { return t }})}
		default:
			continue
		}
		for _, c := range logstore.Categories {
			if fi, err := os.Stat(r.store.Path(c.ID)); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
				r.Categories = append(r.Categories, c.ID)
				if fi.ModTime().After(r.Modified) {
					r.Modified = fi.ModTime()
				}
			}
		}
		if r.DryRun {
			dry = &r
		} else {
			runs = append(runs, r)
		}
	}
	slices.SortFunc(runs, func(a, b Run) int { return strings.Compare(b.Name, a.Name) })
	if dry != nil {
		runs = append(runs, *dry)
	}
	return runs, nil
}

// Read is a run's log for cat as plain lines (escapes stripped by
// logstore). logstore.ErrEmpty: there is nothing to show.
func (r Run) Read(cat logstore.Category) ([]string, error) {
	if r.store == nil {
		return nil, logstore.ErrEmpty
	}
	s, err := r.store.Read(cat)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n"), nil
}
