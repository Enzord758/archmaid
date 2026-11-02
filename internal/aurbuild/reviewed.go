package aurbuild

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/pkgdb"
)

// ReviewedFile is the file's name in the state directory.
const ReviewedFile = "aur-reviewed.json"

// DefaultReviewed is the Reviewed of the real state directory
// (logstore.DefaultDir, ~/.local/state/archmaid; XDG_STATE_HOME is not
// honoured, as for every other state file). Without a usable home folder its
// Path is "": Get finds nothing and Mark returns an error.
func DefaultReviewed() *Reviewed {
	dir := logstore.DefaultDir()
	if dir == "" {
		return &Reviewed{}
	}
	return &Reviewed{Path: filepath.Join(dir, ReviewedFile)}
}

// Reviewed remembers, per package base, the last commit the user reviewed
// and installed: the next review shows the diff from it. The file is user
// state (~/.local/state/archmaid/aur-reviewed.json, docs/design/
// packages-contract.md section 5), written only after an install succeeded
// (the queue's last step), never by a review alone.
//
// A missing or unreadable file reads as "nothing reviewed": the next review
// is a first one, which is the safe direction.
type Reviewed struct {
	// Path is the file.
	Path string

	mu sync.Mutex
}

type reviewedFile struct {
	Version int               `json:"version"`
	Bases   map[string]string `json:"bases"`
}

func (r *Reviewed) load() map[string]string {
	b, err := os.ReadFile(r.Path)
	if err != nil {
		return map[string]string{}
	}
	var f reviewedFile
	if json.Unmarshal(b, &f) != nil || f.Version != 1 || f.Bases == nil {
		return map[string]string{}
	}
	for base, c := range f.Bases { // drop anything that is not a base and a commit
		if !pkgdb.ValidName(base) || !validCommit(c) {
			delete(f.Bases, base)
		}
	}
	return f.Bases
}

// Get is the last reviewed commit of base, or "".
func (r *Reviewed) Get(base string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.load()[base]
}

// Mark records commit as reviewed and installed for each base in marks
// (base -> commit), atomically.
func (r *Reviewed) Mark(marks map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !filepath.IsAbs(r.Path) {
		return errors.New("no state directory for the reviewed commits")
	}
	cur := r.load()
	for base, c := range marks {
		if !pkgdb.ValidName(base) || !validCommit(c) {
			return errors.New("not a package base and commit")
		}
		cur[base] = c
	}
	out := reviewedFile{Version: 1, Bases: cur} // json sorts map keys
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.Path), ".aur-reviewed-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.Path)
}
