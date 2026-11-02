package overview

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// The Packages page's view of updates-cache.json (the timer's output,
// packaging/systemd/archmaid-updates-check; v3). It only reads.

// UpdatesFresh is how old the cache may be to gate an install on it
// (contract section 4: a stale or missing cache never blocks by itself).
const UpdatesFresh = 24 * time.Hour

// maxUpdatesFile caps what is read: the file holds at most 200 entries.
const maxUpdatesFile = 1 << 20

// Updates is the cache's counts.
type Updates struct {
	Known   bool // the file was read and parsed
	Count   int
	Repo    int
	AUR     int // 0 when unknown (null)
	AURHeld int
	Checked time.Time
}

// ParseUpdates parses updates-cache.json's bytes.
func ParseUpdates(b []byte) (Updates, bool) {
	var raw struct {
		V       int    `json:"v"`
		Count   *int   `json:"count"`
		Repo    *int   `json:"repo"`
		AUR     *int   `json:"aur"`
		AURHeld *int   `json:"aur_held"`
		Checked string `json:"checked"`
	}
	if err := json.Unmarshal(b, &raw); err != nil || raw.Repo == nil || raw.Count == nil {
		return Updates{}, false
	}
	t, err := time.Parse(time.RFC3339, raw.Checked)
	if err != nil || *raw.Repo < 0 || *raw.Count < 0 {
		return Updates{}, false
	}
	u := Updates{Known: true, Count: *raw.Count, Repo: *raw.Repo, Checked: t}
	if raw.AUR != nil && *raw.AUR > 0 {
		u.AUR = *raw.AUR
	}
	if raw.AURHeld != nil && *raw.AURHeld > 0 {
		u.AURHeld = *raw.AURHeld
	}
	return u, true
}

// ReadUpdates reads updates-cache.json from stateDir ("" is logstore's
// default). A missing, unreadable or malformed file is the zero Updates
// (Known false).
func ReadUpdates(stateDir string) Updates {
	root := logstore.New(logstore.Options{Dir: stateDir}).Root()
	if root == "" {
		return Updates{}
	}
	f, err := os.Open(filepath.Join(root, "updates-cache.json"))
	if err != nil {
		return Updates{}
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxUpdatesFile))
	if err != nil {
		return Updates{}
	}
	u, _ := ParseUpdates(b)
	return u
}

// Fresh: the cache was read and is at most UpdatesFresh old at now (a
// check dated in the future counts as not fresh).
func (u Updates) Fresh(now time.Time) bool {
	if !u.Known {
		return false
	}
	age := now.Sub(u.Checked)
	return age >= -time.Minute && age <= UpdatesFresh
}

// BlocksInstall: a fresh cache with repo updates pending. AUR-only
// updates do not block (the helper decides the rest).
func (u Updates) BlocksInstall(now time.Time) bool { return u.Fresh(now) && u.Repo > 0 }
