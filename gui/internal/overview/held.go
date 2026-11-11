package overview

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

// Held updates (#280): the AUR updates the timer's check holds back for the
// cooldown (updates-cache.json v3, "packages" entries with src "aur" and
// held true), and the packages an AUR audit holds (audit-state.json, the
// high findings not accepted yet). The Overview's Updates card links to the
// sheet that lists them. Read only.

// Cooldown is how long a new AUR version is held back (the timer's check:
// packaging/systemd/archmaid-updates-check, yay's cooldown).
const Cooldown = 72 * time.Hour

// maxHeld caps the lists: the cache holds at most 200 entries.
const maxHeld = 200

// auditReasonMax caps an audit finding's text.
const auditReasonMax = 120

// ageRE is the cache's age tag ("2d4h", "6h54m", "3d").
var ageRE = regexp.MustCompile(`^[0-9]+[dhms]([0-9]+[dhms])*$`)

// versionRE is what the timer lets through for from and to.
var versionRE = regexp.MustCompile(`^[A-Za-z0-9._+:~-]{1,48}$`)

// HeldPkg is an AUR update in its cooldown.
type HeldPkg struct {
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

// Held is everything the sheet lists.
type Held struct {
	Cooldown []HeldPkg
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
func ParseHeld(b []byte) []HeldPkg {
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
	var out []HeldPkg
	for i, p := range raw.Packages {
		if i >= maxHeld {
			break
		}
		if p.Src != "aur" || p.Held == nil || !*p.Held || !pkgdb.ValidName(p.Name) ||
			!versionRE.MatchString(p.From) || !versionRE.MatchString(p.To) {
			continue
		}
		h := HeldPkg{Name: p.Name, From: p.From, To: p.To}
		if d, ok := ParseAge(p.Age); ok {
			h.Age, h.AgeKnown, h.Since = p.Age, true, d
		}
		out = append(out, h)
	}
	return out
}

// ReadHeld reads both files from stateDir ("" is logstore's default). A
// missing or unreadable file is an empty list.
func ReadHeld(stateDir string) Held {
	root := logstore.New(logstore.Options{Dir: stateDir}).Root()
	if root == "" {
		return Held{}
	}
	var h Held
	if f, err := os.Open(filepath.Join(root, "updates-cache.json")); err == nil {
		if b, err := io.ReadAll(io.LimitReader(f, maxUpdatesFile)); err == nil {
			h.Cooldown = ParseHeld(b)
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
func (h HeldPkg) FreeIn() string {
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
func (h HeldPkg) Tag() string {
	if h.Age == "" {
		return "held"
	}
	return "held " + h.Age
}

// Detail is the row's mono line: "1.2.74-1 → 1.2.76-1 · free in 20 h".
func (h HeldPkg) Detail() string {
	return h.From + " → " + h.To + " · free " + freeText(h)
}

func freeText(h HeldPkg) string {
	if f := h.FreeIn(); f != "soon" {
		return "in " + f
	}
	return "soon"
}

// Count is what the card's link says: the cooldown count the check made
// (shown only while the cache is fresh, like the rest of the card) and the
// packages an audit holds.
func (h Held) Count(u Updates, now time.Time) int {
	n := len(h.Audit)
	if u.Fresh(now) {
		n += u.AURHeld
	}
	return n
}

// LinkText is the card's link, "" when nothing is held.
func (h Held) LinkText(u Updates, now time.Time) string {
	if n := h.Count(u, now); n > 0 {
		return strconv.Itoa(n) + " held ›"
	}
	return ""
}
