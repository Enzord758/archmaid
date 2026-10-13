package driver

import (
	"fmt"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/sysbin"
)

// The kept password's PATH guard (privilege.PathShadows; docs/HANDOVER.md,
// "archmaid doesn't trust PATH for privilege"). Topgrade, and the yay and
// makepkg it runs, start sudo by name on the step's PATH; something that
// stands in for sudo there would be answered from the kept password
// without a dialog. So when a step that gets the askpass starts its child,
// under a Driver that keeps the password, its PATH is scanned first, and
// if anything could stand in for sudo the kept password is off for the
// rest of the run (privilege.KeptPassword.Fail): every prompt, sudo's and
// polkit's, asks the user. The step's output and the step's log get a line
// each, naming what was found.
//
// The scan reads PATH as it is when the child starts. A file put there
// later, while the step runs, isn't seen: the guard catches a PATH that is
// already wrong, not a process of the same user that plants one mid-run
// (which could as well change any directory a pin would put first).

// pathScanMax bounds the scan, which runs on the loop before the child
// starts: a PATH entry on a hung network or FUSE mount would otherwise
// hold the loop. A scan that takes longer counts as a shadow (the kept
// password goes off); it is left to finish on its own goroutine.
var pathScanMax = 2 * time.Second

// pathShadowsMax caps how many shadows a line names.
const pathShadowsMax = 3

// guardPath runs the PATH guard for e's child, about to start with spec.
// It does nothing unless the Driver keeps the run's password (and can
// still), the step is a NeedsPriv child (the one that gets the askpass),
// and it runs for real.
func (d *Driver) guardPath(e engine.StartStep, spec proc.Spec) {
	if d.keep.Spent() || !e.Step.NeedsPriv || e.Env.DryRun {
		return
	}
	shadows, done := scanPath(privilege.ChildPATH(spec.Env, spec.ExtraEnv), pathScanMax)
	if done && len(shadows) == 0 {
		return
	}
	d.keep.Fail()
	what := "the check of this step's PATH didn't finish in " + pathScanMax.String()
	if done {
		what = "this step's PATH has something before " + sysbin.Sudo + " that could stand in for sudo: " + namesOf(shadows)
	}
	cat := e.Step.Category
	if !cat.Valid() {
		cat = logstore.Topgrade
	}
	if err := d.cfg.Log.Append(cat, "WARN", "Kept password off for this run: "+what, ""); err != nil {
		d.report(fmt.Errorf("driver: writing the %s log: %w (the kept password is off: %s)", cat, err, what))
	}
	line := "archmaid: the kept password is off for this run, so every password prompt asks you (" + what + ")"
	d.local = append(d.local, msg{in: engine.StepOutput{Seq: e.Seq, Lines: []string{line}}})
}

// pathShadows is the scan (a seam: a test makes it hang).
var pathShadows = privilege.PathShadows

// scanPath is pathShadows(path) within max; done is false when it took
// longer.
func scanPath(path string, max time.Duration) (shadows []privilege.Shadow, done bool) {
	scan := pathShadows                    // read here, on the loop, not on the scan's goroutine
	ch := make(chan []privilege.Shadow, 1) // the scan never blocks on it, finished or abandoned
	go func() { ch <- scan(path) }()
	t := time.NewTimer(max)
	defer t.Stop()
	select {
	case s := <-ch:
		return s, true
	case <-t.C:
		return nil, false
	}
}

// namesOf names shadows for one line: the first pathShadowsMax, then how
// many more, each sanitized (a file name can hold anything).
func namesOf(shadows []privilege.Shadow) string {
	var names []string
	for i, s := range shadows {
		if i == pathShadowsMax {
			names = append(names, fmt.Sprintf("%d more", len(shadows)-i))
			break
		}
		names = append(names, sanitize.Clip(sanitize.OneLine(s.String()), 200))
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
