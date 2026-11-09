package actions

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A Packages run logs to packages.log (#268): its install and removal step
// had no category of its own and fell into cleanup.log, so Last run said
// "Packages" with cleanup.log.
func TestPackagesStepsLogToPackages(t *testing.T) {
	for _, mode := range []steps.PkgMode{steps.PkgInstall, steps.PkgRemove} {
		st := Packages(steps.PkgPlan{Mode: mode, Targets: []string{"foo"}})
		if len(st) != 1 || st[0].LogCategory() != logstore.Packages {
			t.Errorf("mode %v: the step logs to %q, want %q", mode, st[0].LogCategory(), logstore.Packages)
		}
	}
}
