package overview

import (
	"strconv"
	"time"

	"github.com/Enzord758/archmaid/internal/held"
)

// Held updates (#280): the AUR updates the timer's check holds back for the
// cooldown, and the packages an AUR audit holds. What they are and how they
// are read lives in internal/held (shared with the TUI); this file is the
// Overview's side: the card's count and link. Read only.

// Cooldown is how long a new AUR version is held back.
const Cooldown = held.Cooldown

// HeldPkg is an AUR update in its cooldown.
type HeldPkg = held.Pkg

// AuditHold is a package an AUR audit holds.
type AuditHold = held.AuditHold

// Held is everything the sheet lists.
type Held struct{ held.Set }

// ParseAge reads the cache's age tag: "2d4h" is 52 hours.
func ParseAge(s string) (time.Duration, bool) { return held.ParseAge(s) }

// ParseHeld reads the held AUR entries of updates-cache.json's bytes.
func ParseHeld(b []byte) []HeldPkg { return held.ParseHeld(b) }

// ReadHeld reads both files from stateDir ("" is logstore's default). A
// missing or unreadable file is an empty list.
func ReadHeld(stateDir string) Held { return Held{held.Read(stateDir)} }

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
