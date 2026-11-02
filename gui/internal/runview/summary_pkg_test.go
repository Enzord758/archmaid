package runview

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A Packages page run is not an update: its summary says what it installed
// or removed, never "Everything is up to date" and "0 upgraded".
func TestSummaryPackagesRun(t *testing.T) {
	inst := report.Report{Action: steps.IDPkgInstall, Elapsed: 5 * time.Second, Steps: report.StepTally{Done: 1},
		Packages: &report.Packages{Installed: []report.Pkg{{Name: "firefox", New: "133.0-1"}, {Name: "libxt", New: "1.3.1-1"}}}}
	s := BuildSummary(inst, true)
	if s.Kicker != "DONE" || s.Headline != "Packages installed" || s.Sub != "2 packages installed · 5 s" {
		t.Fatalf("install: %q %q %q", s.Kicker, s.Headline, s.Sub)
	}
	if s.Packages == nil || s.Packages.Head != "2 installed" || len(s.Packages.Rows) != 2 || s.Packages.Rows[0].Change != "133.0-1" {
		t.Fatalf("install block: %+v", s.Packages)
	}
	rem := report.Report{Action: steps.IDPkgRemove, Packages: &report.Packages{Removed: []report.Pkg{{Name: "vim", Old: "9.1-1"}, {Name: "vim-runtime", Old: "9.1-1"}}}}
	s = BuildSummary(rem, true)
	if s.Headline != "Packages removed" || s.Sub != "2 packages removed" || s.Packages.Head != "2 removed" || len(s.Packages.Lines) != 1 {
		t.Fatalf("remove: %q %q %+v", s.Headline, s.Sub, s.Packages)
	}
	// An update run's summary is unchanged.
	up := report.Report{Action: "topgrade", Packages: &report.Packages{Upgraded: []report.Pkg{{Name: "a", Old: "1", New: "2"}}}}
	if s := BuildSummary(up, true); s.Sub != "1 package upgraded" || s.Packages.Head != "1 upgraded" {
		t.Fatalf("update: %q %+v", s.Sub, s.Packages)
	}
}

// The engine names a Packages run's queue, and so its report, actions.IDPackages
// ("packages"), not the step's ID: the summary must still say what the run
// did (it said "Everything is up to date").
func TestSummaryPackagesRunByEngineAction(t *testing.T) {
	inst := report.Report{Action: actions.IDPackages, Packages: &report.Packages{Installed: []report.Pkg{{Name: "firefox", New: "133.0-1"}}}}
	s := BuildSummary(inst, true)
	if s.Headline != "Packages installed" || s.Sub != "1 package installed" || s.Packages.Head != "1 installed" {
		t.Fatalf("install: %q %q %+v", s.Headline, s.Sub, s.Packages)
	}
	rem := report.Report{Action: actions.IDPackages, Packages: &report.Packages{Removed: []report.Pkg{{Name: "vim", Old: "9.1-1"}}}}
	s = BuildSummary(rem, true)
	if s.Headline != "Packages removed" || s.Sub != "1 package removed" || s.Packages.Head != "1 removed" {
		t.Fatalf("remove: %q %q %+v", s.Headline, s.Sub, s.Packages)
	}
}

// The planned size change of a Packages run is in the sub line when known.
func TestSummaryPackagesRunSizeChange(t *testing.T) {
	inst := report.Report{Action: actions.IDPackages, Packages: &report.Packages{
		Installed: []report.Pkg{{Name: "firefox", New: "133.0-1"}}, SizeChange: 14 << 20, SizeKnown: true}}
	if s := BuildSummary(inst, true); s.Sub != "1 package installed · uses 14 MiB more" {
		t.Errorf("install: %q", s.Sub)
	}
	rem := report.Report{Action: actions.IDPackages, Packages: &report.Packages{
		Removed: []report.Pkg{{Name: "vim", Old: "9.1-1"}}, SizeChange: -(48 << 20), SizeKnown: true}}
	if s := BuildSummary(rem, true); s.Sub != "1 package removed · frees 48 MiB" {
		t.Errorf("remove: %q", s.Sub)
	}
	none := report.Report{Action: actions.IDPackages, Packages: &report.Packages{Installed: []report.Pkg{{Name: "a", New: "1"}}}}
	if s := BuildSummary(none, true); s.Sub != "1 package installed" {
		t.Errorf("unknown size: %q", s.Sub)
	}
}
