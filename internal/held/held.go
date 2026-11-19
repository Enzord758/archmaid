package held

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/pkgdb"
)

// Package held reads the held updates (#280, shared by the GUI and the TUI): the AUR updates the timer's check holds back for the
// cooldown (updates-cache.json v3, "packages" entries with src "aur" and
// held true), and the packages an AUR audit holds (audit-state.json, the
// high findings not accepted yet). The Overview's Updates card links to the
// sheet that lists them. Read only.

// Cooldown is how long a new AUR version is held back (the timer's check:
// packaging/systemd/archmaid-updates-check, yay's cooldown).
const Cooldown = 72 * time.Hour

// maxHeld caps the lists: the cache holds at most 200 entries.
const maxHeld = 200

// maxUpdatesFile caps what is read of updates-cache.json: it holds at most
// 200 entries.
const maxUpdatesFile = 1 << 20

// auditReasonMax caps an audit finding's text.
const auditReasonMax = 120

// ageRE is the cache's age tag ("2d4h", "6h54m", "3d").
var ageRE = regexp.MustCompile(`^[0-9]+[dhms]([0-9]+[dhms])*$`)

// versionRE is what the timer lets through for from and to.
var versionRE = regexp.MustCompile(`^[A-Za-z0-9._+:~-]{1,48}$`)

// Pkg is an AUR update in its cooldown.
type Pkg struct {
	Name, From, To string
	// Age is the cache's age tag ("2d4h"), "" when it could not be read
	// (held without an age still counts as held); AgeKnown says it parsed
	// and Since is that age.
	Age      string
	AgeKnown bool
	Since    time.Duration
}

// AuditHold is a package an AUR audit holds: a high finding the user has
// not accepted yet.
type AuditHold struct {
	Pkg, Reason string
}

// Set is everything the sheet lists.
type Set struct {
	// Checked is when the cache's check ran (zero when it can't be read).
	Checked  time.Time
	Cooldown []Pkg
	Audit    []AuditHold
}

// ParseAge reads the cache's age tag: "2d4h" is 52 hours.
func ParseAge(s string) (time.Duration, bool) {
	if !ageRE.MatchString(s) {
		return 0, false
	}
	var total time.Duration
	n := ""
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n += string(r)
			continue
		}
		v, err := strconv.ParseInt(n, 10, 32)
		if err != nil {
			return 0, false
		}
		n = ""
		switch r {
		case 'd':
			total += time.Duration(v) * 24 * time.Hour
		case 'h':
			total += time.Duration(v) * time.Hour
		case 'm':
			total += time.Duration(v) * time.Minute
		case 's':
			total += time.Duration(v) * time.Second
		}
	}
	return total, true
}

// ParseHeld reads the held AUR entries of updates-cache.json's bytes (none
// for a file without a package list). An entry whose name or versions do not
// look like a package's is left out (it is still in the count): the file is
// text another program wrote.
func ParseHeld(b []byte) []Pkg {
	var raw struct {
		Packages []struct {
			Name string `json:"name"`
			From string `json:"from"`
			To   string `json:"to"`
			Src  string `json:"src"`
			Held *bool  `json:"held"`
			Age  string `json:"age"`
		} `json:"packages"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	var out []Pkg
	// The cap is on what is kept, not on what is scanned: the writer lists the
	// AUR entries first, but a list with a long repo part ahead of them must
	// not hide the held ones (the file is at most maxUpdatesFile bytes).
	for _, p := range raw.Packages {
		if len(out) >= maxHeld {
			break
		}
		if p.Src != "aur" || p.Held == nil || !*p.Held || !pkgdb.ValidName(p.Name) ||
			!versionRE.MatchString(p.From) || !versionRE.MatchString(p.To) {
			continue
		}
		h := Pkg{Name: p.Name, From: p.From, To: p.To}
		if d, ok := ParseAge(p.Age); ok {
			h.Age, h.AgeKnown, h.Since = p.Age, true, d
		}
		out = append(out, h)
	}
	return out
}

// CacheFresh is how old the cache may be for its cooldown list to count: a
// stale cache says nothing about what is held now (the Overview card's rule,
// overview.UpdatesFresh).
const CacheFresh = 24 * time.Hour

// CooldownAt is the cooldown rows that count at now: none unless the cache
// was checked within CacheFresh (a check dated in the future, by more than a
// minute, is not fresh either).
func (s Set) CooldownAt(now time.Time) []Pkg {
	if s.Checked.IsZero() {
		return nil
	}
	if age := now.Sub(s.Checked); age < -time.Minute || age > CacheFresh {
		return nil
	}
	return s.Cooldown
}

// parseChecked reads the cache's "checked" time (zero when it isn't one).
func parseChecked(b []byte) time.Time {
	var raw struct {
		Checked string `json:"checked"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw.Checked)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Read reads both files from stateDir ("" is logstore's default). A
// missing or unreadable file is an empty list.
func Read(stateDir string) Set {
	root := logstore.New(logstore.Options{Dir: stateDir}).Root()
	if root == "" {
		return Set{}
	}
	var h Set
	if f, err := os.Open(filepath.Join(root, "updates-cache.json")); err == nil {
		if b, err := io.ReadAll(io.LimitReader(f, maxUpdatesFile)); err == nil {
			h.Cooldown = ParseHeld(b)
			h.Checked = parseChecked(b)
		}
		f.Close()
	}
	if st, err := audit.ReadState(filepath.Join(root, audit.StateFile)); err == nil {
		for i, f := range st.High {
			if i >= maxHeld {
				break
			}
			if f.Pkg == "" {
				continue
			}
			h.Audit = append(h.Audit, AuditHold{Pkg: clip(f.Pkg, 64), Reason: clip(f.Reason, auditReasonMax)})
		}
	}
	return h
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// FreeIn is when the held update leaves its cooldown: "20 h", "1 d 15 h",
// "40 min"; "soon" when its age could not be read (or has run past the
// cooldown), since an update without a readable age counts as held.
func (h Pkg) FreeIn() string {
	if !h.AgeKnown {
		return "soon"
	}
	rem := Cooldown - h.Since
	if rem <= 0 {
		return "soon"
	}
	if rem < time.Hour {
		return strconv.Itoa(int((rem+time.Minute-1)/time.Minute)) + " min"
	}
	hours := int((rem + time.Hour - 1) / time.Hour) // rounded up: never early
	d, hh := hours/24, hours%24
	switch {
	case d == 0:
		return strconv.Itoa(hh) + " h"
	case hh == 0:
		return strconv.Itoa(d) + " d"
	}
	return strconv.Itoa(d) + " d " + strconv.Itoa(hh) + " h"
}

// Tag is the row's tag: "held 2d4h", "held" when the age is unknown.
func (h Pkg) Tag() string {
	if h.Age == "" {
		return "held"
	}
	return "held " + h.Age
}

// Detail is the row's mono line: "1.2.74-1 → 1.2.76-1 · free in 20 h".
func (h Pkg) Detail() string {
	return h.From + " → " + h.To + " · free " + freeText(h)
}

func freeText(h Pkg) string {
	if f := h.FreeIn(); f != "soon" {
		return "in " + f
	}
	return "soon"
}
