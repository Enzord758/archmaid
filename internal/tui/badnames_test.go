package tui

import (
	"strings"
	"testing"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// refusedNames are lists with a bad name in the middle: the whole list is
// refused, not just the one name.
var refusedNames = map[string]string{
	"space-mid":  "libok\nevil --noconfirm\nlibtwo",
	"escape-mid": "libok\n\x1b[31mevil\x1b[0m\nlibtwo",
	"dash-mid":   "libok\n--overwrite\nlibtwo",
}

// refusalTargets are the two frontends' paths: the TUI's model and the
// engine Session (the GUI's).
var refusalTargets = map[string]newTarget{
	"tui":     func(t *testing.T, shimLog string) tt.Target { return newTUIEngineTarget(t, shimLog) },
	"session": func(t *testing.T, shimLog string) tt.Target { return newSessionTarget(t, shimLog) },
}

// noRemovalQueued asserts a transcript holds no step added to the queue, no
// question about removing or reinstalling, no sudo pacman that installs or
// removes, and no raw escape.
func noRemovalQueued(t *testing.T, name, tr string) {
	t.Helper()
	for _, l := range strings.Split(tr, "\n") {
		switch {
		case strings.HasPrefix(l, "plan  +"):
			t.Errorf("%s: a step was added after the refusal: %q", name, l)
		case strings.HasPrefix(l, "ask") && (strings.Contains(l, "orphaned") || strings.Contains(l, "Reinstall")):
			t.Errorf("%s: a removal question was asked: %q", name, l)
		case strings.HasPrefix(l, "exec") && strings.Contains(l, "sudo") && (strings.Contains(l, "-Rns") || strings.Contains(l, "pacman -S ")):
			t.Errorf("%s: a privileged pacman ran: %q", name, l)
		case strings.HasPrefix(l, "exec") && strings.Contains(l, "-Rs --print"):
			t.Errorf("%s: the removal preview ran with a refused list: %q", name, l)
		}
	}
	if strings.ContainsRune(tr, '\x1b') {
		t.Errorf("%s: a raw escape reached the transcript", name)
	}
}

// TestRefusedOrphanNameLeavesNoStep (QA b8 tail): Full Cleanup's orphan
// probe refuses the whole list when one name in the middle is not a package
// name, on the TUI's model and on the engine Session (the GUI's path): the
// probe fails, no removal step is inserted, nothing is asked, and nothing
// reaches `sudo pacman -Rns`.
func TestRefusedOrphanNameLeavesNoStep(t *testing.T) {
	for tname, mk := range refusalTargets {
		for name, orphans := range refusedNames {
			t.Run(tname+"/"+name, func(t *testing.T) {
				transcriptEnv(t, false)
				shimLog := installShims(t, fullCleanupShims(true, orphans, orphans))
				aurClean(t)
				tr := record(t, mk, shimLog, "Full Cleanup", acceptAll[:1]...).Transcript()
				noRemovalQueued(t, name, tr)
				if !strings.Contains(tr, "step  4 failed") {
					t.Errorf("the orphan probe did not fail:\n%s", tr)
				}
			})
		}
	}
}

// TestRefusedBrokenNameLeavesNoStep: Repair refuses a flagged name that is
// not a package name, from the user scan and from the root recheck, with the
// good flagged names beside it dropped too: no reinstall is planned.
func TestRefusedBrokenNameLeavesNoStep(t *testing.T) {
	for tname, mk := range refusalTargets {
		t.Run(tname+"/user-scan", func(t *testing.T) {
			transcriptEnv(t, false)
			pinScanWorkers(t, 1)
			shimLog := installShims(t, Shims{
				"pacman": repairPacmanRules("baz -evil foo", "baz -evil foo"),
				"sudo":   sudoRules(true),
			})
			tr := record(t, mk, shimLog, "Repair broken packages").Transcript()
			noRemovalQueued(t, "user-scan", tr)
			if strings.Contains(tr, "sudo -n pacman -Qkq") {
				t.Errorf("a refused scan still reached sudo:\n%s", tr)
			}
		})
		t.Run(tname+"/root-recheck", func(t *testing.T) {
			transcriptEnv(t, false)
			pinScanWorkers(t, 1)
			shimLog := installShims(t, Shims{
				"pacman": repairPacmanRules("baz foo", "baz foo"),
				"sudo": sudoRules(true, Rule{Prefix: "-n pacman -Qkq --color never -- ", Exit: 1,
					Out: "baz /usr/share/baz/a\nev;il /usr/x\nfoo /usr/share/foo/a"}),
			})
			tr := record(t, mk, shimLog, "Repair broken packages").Transcript()
			noRemovalQueued(t, "root-recheck", tr)
		})
	}
}
