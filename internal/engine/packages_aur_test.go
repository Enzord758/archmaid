package engine_test

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

var aurReq = steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"app"}, AUR: true}

func aurPlan(rows bool) steps.PkgPlan {
	p := steps.PkgPlan{Mode: steps.PkgInstall, Targets: []string{"app"}, Warnings: []string{"The maintainer audit could not run: offline"},
		AUR: []steps.AURPlan{
			{Base: "lib", Names: []string{"lib"}, Prev: strings.Repeat("1", 40)},
			{Base: "app", Names: []string{"app"}, Audit: []steps.AURFinding{{Name: "app", High: true, Reason: "maintainer alice → mallory"}, {Name: "app", Reason: "unmaintained"}}},
		}}
	if rows {
		p.Rows = []steps.PkgRow{{Name: "libfoo", Version: "1-1", Repo: "core", Target: true}}
	}
	return p
}

// TestAURSheet: the AUR sheet says what runs where, how many passwords it
// asks, that the author's code runs as root, and shows the audit's high
// findings and notes; Cancel is the default and Install is not destructive.
func TestAURSheet(t *testing.T) {
	for _, rows := range []bool{false, true} {
		h := pkgHarness(t)
		p := aurPlan(rows)
		h.startPackages(aurReq)
		o := h.discovered(actions.PkgPreview{Plan: p, Steps: []steps.Step{{ID: "aur-build:app"}}})
		nc := event[NeedsConfirm](t, o)
		if nc.Kind != ConfirmPackages || nc.Destructive || nc.YesLabel != "Build and install" || nc.Default {
			t.Errorf("confirm = %+v", nc)
		}
		if nc.Title != "Build and install 1 package from the AUR?" {
			t.Errorf("title = %q", nc.Title)
		}
		for _, want := range []string{"2 AUR package bases", "lib, app", "package author's code as root",
			"Packages you have installed before", "Audit, high: app: maintainer alice → mallory", "audit could not run"} {
			if !strings.Contains(nc.Body, want) {
				t.Errorf("rows=%v: body lacks %q:\n%s", rows, want, nc.Body)
			}
		}
		if strings.Contains(nc.Body, "unmaintained") {
			t.Error("only high findings go in the question; the page lists the rest")
		}
		if rows {
			if !strings.Contains(nc.Body, "2 times") || !strings.Contains(nc.Body, "1 package from the repositories") {
				t.Errorf("two root steps need two prompts:\n%s", nc.Body)
			}
		} else if !strings.Contains(nc.Body, "once") || strings.Contains(nc.Body, "from the repositories") {
			t.Errorf("one root step, one prompt:\n%s", nc.Body)
		}
	}
	// a first review for everything: no "changed since" line
	h := pkgHarness(t)
	p := aurPlan(false)
	p.AUR[0].Prev = ""
	h.startPackages(aurReq)
	nc := event[NeedsConfirm](t, h.discovered(actions.PkgPreview{Plan: p, Steps: []steps.Step{{ID: "x"}}}))
	if strings.Contains(nc.Body, "Packages you have installed before") {
		t.Errorf("body:\n%s", nc.Body)
	}
}
