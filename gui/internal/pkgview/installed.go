package pkgview

import (
	"strings"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

// InstalledShown caps the rows the page draws at once: a system holds
// thousands of packages, and the filter is how the rest is reached.
const InstalledShown = 150

// Views are the installed view's tabs in order.
var Views = []pkgdb.View{pkgdb.ViewExplicit, pkgdb.ViewDependencies, pkgdb.ViewOrphans, pkgdb.ViewForeign}

// ViewTitle is a tab's title.
func ViewTitle(v pkgdb.View) string {
	switch v {
	case pkgdb.ViewExplicit:
		return "Explicit"
	case pkgdb.ViewDependencies:
		return "Dependencies"
	case pkgdb.ViewOrphans:
		return "Orphans"
	case pkgdb.ViewForeign:
		return "Foreign"
	}
	return ""
}

// InstalledRow is one row of the installed view.
type InstalledRow struct {
	Pkg  pkgdb.Package
	Size string
	Tags []Tag
	// Removable: it can go in the removal list. Why says why not.
	Removable bool
	Why       string
}

// InstalledView is the installed list: a tab, a filter and the rows.
type InstalledView struct {
	cat    Catalog
	prot   Protector
	view   pkgdb.View
	filter string
	cache  map[pkgdb.View][]pkgdb.Package
}

// NewInstalled makes the view on the Explicit tab.
func NewInstalled(cat Catalog, prot Protector) *InstalledView {
	return &InstalledView{cat: cat, prot: prot, view: pkgdb.ViewExplicit, cache: map[pkgdb.View][]pkgdb.Package{}}
}

// Reload drops what was read (the system changed: a run finished).
func (v *InstalledView) Reload() { v.cache = map[pkgdb.View][]pkgdb.Package{} }

// View is the tab shown.
func (v *InstalledView) View() pkgdb.View { return v.view }

// SetView picks a tab.
func (v *InstalledView) SetView(w pkgdb.View) { v.view = w }

// SetFilter sets the filter text (name or description contains it, case
// ignored).
func (v *InstalledView) SetFilter(s string) { v.filter = strings.ToLower(strings.TrimSpace(s)) }

// Filter is the filter text, lower-cased.
func (v *InstalledView) Filter() string { return v.filter }

func (v *InstalledView) all(w pkgdb.View) []pkgdb.Package {
	if v.cat == nil {
		return nil
	}
	if ps, ok := v.cache[w]; ok {
		return ps
	}
	ps := v.cat.Installed(w)
	v.cache[w] = ps
	return ps
}

// Count is the number of packages in a tab, unfiltered (the tab's badge).
func (v *InstalledView) Count(w pkgdb.View) int { return len(v.all(w)) }

// Rows are the current tab's rows after the filter, in the catalog's
// (name) order; total is how many matched before the cap InstalledShown.
func (v *InstalledView) Rows() (rows []InstalledRow, total int) {
	for _, p := range v.all(v.view) {
		if v.filter != "" && !strings.Contains(strings.ToLower(p.Name), v.filter) &&
			!strings.Contains(strings.ToLower(p.Description), v.filter) {
			continue
		}
		total++
		if len(rows) < InstalledShown {
			rows = append(rows, v.row(p))
		}
	}
	return rows, total
}

func (v *InstalledView) row(p pkgdb.Package) InstalledRow {
	r := InstalledRow{Pkg: p, Size: Size(p.InstalledSize), Tags: RowTags(p), Removable: true}
	switch {
	case p.Orphan:
		r.Removable, r.Why = false, "Full Cleanup removes orphans."
	case v.prot != nil:
		if why, ok := v.prot.Protected(p.Name); ok {
			r.Removable, r.Why = false, why
		}
	}
	return r
}

// OrphansNote is the banner over the Orphans tab, "" when there are none.
func OrphansNote(n int) string {
	if n == 0 {
		return ""
	}
	return N(n, "orphan", "orphans") + ". Full Cleanup removes them; they are listed here to look at."
}
