package pkgview

import (
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

// RecentMax is how many packages the empty state's "Recently installed"
// row shows.
const RecentMax = 5

// RecentRow is one entry of "Recently installed": the package and when it
// came, in a few plain words.
type RecentRow struct {
	Pkg pkgdb.Package
	Ago string // "just now", "3 h ago", "2 days ago"
}

// RecentRows is the n most recently installed packages (the catalog's
// Recent, newest first), each with its age at now. A clock that is behind
// the install date reads "just now", never a negative age.
func RecentRows(c Catalog, now time.Time, n int) []RecentRow {
	if c == nil || n <= 0 {
		return nil
	}
	var out []RecentRow
	for _, p := range c.Recent(n) {
		ago := "just now"
		if d := now.Sub(p.InstallDate); d >= time.Minute {
			ago = Age(d) + " ago"
		}
		out = append(out, RecentRow{Pkg: p, Ago: ago})
	}
	return out
}
