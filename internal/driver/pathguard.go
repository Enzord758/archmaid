package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// The kept password's PATH guard (privilege.PathShadows; docs/HANDOVER.md,
// "archmaid doesn't trust PATH for privilege"). Topgrade, and the paru, yay and
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
// (which could as well change any directory a pin would put first). It is
// deliberately not run again before each kept answer (review #134): by the
// time a planted sudo's askpass request arrives it has exec'd the real
// sudo, and it can have removed itself first, so a second scan sees a
// clean PATH and the answer goes out all the same.

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
	if d.keep.Spent() || !e.Step.Asks() || e.Env.DryRun {
		return
	}
	shadows, done := scanPath(privilege.ChildPATH(spec.Env, spec.ExtraEnv), spec.Env, spec.ExtraEnv, pathScanMax)
	if done && len(shadows) == 0 {
		return
	}
	d.keep.Fail()
	d.keepOffWhy = keepOffReason(done, shadows)
	what := "the check of this step's PATH didn't finish in " + pathScanMax.String()
	if done {
		what = "this step's PATH or sudo config has something that could stand in for sudo: " + namesOf(shadows)
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

// keepOffReason is the one-line why a failed guard gives the password
// dialog (NeedsPassword.KeepOff) and the gate's note (PreviewKeepOff).
func keepOffReason(done bool, shadows []privilege.Shadow) string {
	if !done {
		return "the PATH check didn't finish in time"
	}
	// A shadow with a Note is a config file (a topgrade, paru or yay config
	// that names a sudo or pacman of its own), else a file on PATH.
	var cfgs []privilege.Shadow
	onPath := false
	for _, s := range distinctShadows(shadows) {
		if s.Note != "" {
			cfgs = append(cfgs, s)
		} else {
			onPath = true
		}
	}
	var parts []string
	if onPath || len(cfgs) == 0 {
		parts = append(parts, "the PATH check found something that could stand in for sudo")
	}
	if len(cfgs) > 0 {
		c := cfgs[0]
		what := "your " + configTool(c.Path) + " config (" + tildePath(c.Path) + ") " + sanitize.Clip(sanitize.OneLine(c.Note), 60)
		if n := len(cfgs) - 1; n > 0 {
			what += fmt.Sprintf(" (and %d more)", n)
		}
		parts = append(parts, what)
	}
	return strings.Join(parts, ", and ")
}

// configTool is the program a config file of the guard belongs to.
func configTool(path string) string {
	base, dir := filepath.Base(path), filepath.Base(filepath.Dir(path))
	switch {
	case base == "topgrade.toml":
		return "topgrade"
	case base == "paru.conf" || dir == "paru":
		return "paru"
	case dir == "yay":
		return "yay"
	}
	return "tool"
}

// tildePath is path for a sentence: the home folder as ~, one line, short.
func tildePath(path string) string {
	if home := os.Getenv("HOME"); filepath.IsAbs(home) && strings.HasPrefix(path, home+"/") {
		path = "~" + strings.TrimPrefix(path, home)
	}
	return sanitize.Clip(sanitize.OneLine(path), 80)
}

// PreviewKeepOff is the PATH guard's verdict as of now, without its side
// effects (no Fail, no log line, no step output), for the GUI's gate sheet
// before the run starts: "" when the guard would leave the kept password
// on, else why it would turn it off (the reason the password dialog gives,
// NeedsPassword.KeepOff). It scans archmaid's own PATH, which is what a
// GUI step's child gets (the GUI has no sudo wrapper dir in front).
//
// It is a preview and only that. The step-start check (guardPath) stays
// the authority: PATH or the files on it can change between the sheet
// and the step, and the guard turns the password off at that moment
// whatever the sheet said.
func PreviewKeepOff() string {
	shadows, done := scanPath(privilege.ChildPATH(nil, nil), nil, nil, pathScanMax)
	if done && len(shadows) == 0 {
		return ""
	}
	return keepOffReason(done, shadows)
}

// pathShadows is the scan (a seam: a test makes it hang).
var pathShadows = privilege.PathShadows

// configShadows is the config scan (privilege.ConfigShadows: a sudo or
// pacman the user's topgrade, paru or yay config names), a seam like
// pathShadows.
var configShadows = privilege.ConfigShadows

// scanPath is pathShadows(path) and configShadows(env, extra) within max; done
// is false when they took longer.
func scanPath(path string, env, extra []string, max time.Duration) (shadows []privilege.Shadow, done bool) {
	scan, cfg := pathShadows, configShadows // read here, on the loop, not on the scan's goroutine
	ch := make(chan []privilege.Shadow, 1)  // the scan never blocks on it, finished or abandoned
	go func() { ch <- append(scan(path), cfg(env, extra)...) }()
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
	shadows = distinctShadows(shadows)
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

// distinctShadows drops what names a shadow already listed: the same path
// (a PATH with a directory twice finds its file twice) or the same file
// by device and inode (a symlinked directory, a hard link). Order kept.
func distinctShadows(shadows []privilege.Shadow) []privilege.Shadow {
	var out []privilege.Shadow
	var infos []os.FileInfo // out's files by position (nil: relative, or gone)
	seen := map[string]bool{}
next:
	for _, s := range shadows {
		key := fmt.Sprintf("%t|%s", s.Relative, s.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		var fi os.FileInfo
		if !s.Relative {
			fi, _ = os.Lstat(s.Path)
		}
		if fi != nil {
			for _, o := range infos {
				if o != nil && os.SameFile(o, fi) {
					continue next
				}
			}
		}
		out = append(out, s)
		infos = append(infos, fi)
	}
	return out
}
