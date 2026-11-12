package runview

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/report"
)

// An AUR package that is installed already and is updated through the Packages
// page (the held sheet's "Review and update") is logged by pacman as upgraded,
// not installed: the summary said "0 installed" (#285). It counts like an
// install does.
func TestSummaryPackagesRunUpgrade(t *testing.T) {
	up := report.Report{Action: actions.IDPackages, Packages: &report.Packages{
		Upgraded: []report.Pkg{{Name: "termix-bin", Old: "1.0-1", New: "1.1-1", AUR: true}}, AURUpgraded: 1}}
	s := BuildSummary(up, true)
	if s.Kicker != "DONE" || s.Headline != "Packages updated" || s.Sub != "1 package upgraded" {
		t.Fatalf("upgrade: %q %q %q", s.Kicker, s.Headline, s.Sub)
	}
	if s.Packages == nil || s.Packages.Head != "1 upgraded" || len(s.Packages.Rows) != 1 ||
		s.Packages.Rows[0].Name != "termix-bin" || s.Packages.Rows[0].Change != "1.0-1 → 1.1-1" {
		t.Fatalf("upgrade block: %+v", s.Packages)
	}
	// An install and an upgrade in one run (a new package pulling a dependency
	// forward): both are counted, the headline stays "installed".
	both := report.Report{Action: actions.IDPackages, Packages: &report.Packages{
		Installed: []report.Pkg{{Name: "foo", New: "1-1"}}, Upgraded: []report.Pkg{{Name: "libfoo", Old: "1-1", New: "2-1"}}}}
	s = BuildSummary(both, true)
	if s.Headline != "Packages installed" || s.Sub != "1 package installed · 1 package upgraded" ||
		s.Packages.Head != "1 installed, 1 upgraded" || len(s.Packages.Rows) != 2 {
		t.Fatalf("both: %q %q %+v", s.Headline, s.Sub, s.Packages)
	}
	// Upgrades and removals together.
	mix := report.Report{Action: actions.IDPackages, Packages: &report.Packages{
		Upgraded: []report.Pkg{{Name: "a", Old: "1", New: "2"}}, Removed: []report.Pkg{{Name: "b", Old: "1"}}}}
	if s := BuildSummary(mix, true); s.Packages.Head != "1 upgraded, 1 removed" || s.Headline != "Packages installed" {
		t.Fatalf("mix: %q %+v", s.Headline, s.Packages)
	}
	// More rows than fit: the rest are counted as hidden.
	many := &report.Packages{}
	for i := 0; i < MaxPackageRows+3; i++ {
		many.Upgraded = append(many.Upgraded, report.Pkg{Name: "p", Old: "1", New: "2"})
	}
	if s := BuildSummary(report.Report{Action: actions.IDPackages, Packages: many}, true); len(s.Packages.Rows) != MaxPackageRows || s.Packages.Hidden != 3 {
		t.Fatalf("rows %d hidden %d", len(s.Packages.Rows), s.Packages.Hidden)
	}
}
