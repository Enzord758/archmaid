package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// Review #134 (Dev 2) of the PATH guard (#131).

// The owner's PATH (2026-10-13) has nvm's node bin, ~/.local/bin and
// pnpm's dir before /usr/local/sbin, /usr/local/bin and /usr/bin, and
// cuda, the perl dirs and rustup's after it; none of the user dirs holds a
// sudo-like program, and /usr/bin holds su, pkexec and run0 (a symlink)
// besides sudo. On that PATH the guard finds nothing: the run's second
// sudo gets the kept password, with no guard line anywhere.
func TestPathGuardOwnerPATH(t *testing.T) {
	testenv.UseSysbin(t, &sysbin.Su, `read pw; [ "$pw" = good ]`)
	usrBin := filepath.Dir(testenv.UseSysbin(t, &sysbin.Sudo, "exit 0"))
	home, root := t.TempDir(), t.TempDir()
	mk := func(dir string, files ...string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	link := func(dir, name, target string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	nvm := mk(filepath.Join(home, ".nvm", "versions", "node", "v20.20.2", "bin"), "node")
	link(nvm, "npm", "../lib/node_modules/npm/bin/npm-cli.js")
	link(nvm, "npx", "../lib/node_modules/npm/bin/npx-cli.js")
	local := mk(filepath.Join(home, ".local", "bin"), "uv", "uvx")
	link(local, "claude", filepath.Join(home, ".local", "share", "claude", "versions", "2.1.0"))
	pnpm := mk(filepath.Join(home, ".local", "share", "pnpm"), "pnpm", "pnpx")
	mk(usrBin, "su", "pkexec", "systemd-run")
	link(usrBin, "run0", "systemd-run")
	before := []string{nvm, local, pnpm, mk(filepath.Join(root, "usr", "local", "sbin")), mk(filepath.Join(root, "usr", "local", "bin"))}
	after := []string{
		mk(filepath.Join(root, "opt", "cuda", "bin"), "nvcc"),
		mk(filepath.Join(root, "usr", "bin", "core_perl"), "perldoc"),
		mk(filepath.Join(root, "usr", "lib", "rustup", "bin"), "rustc", "cargo"),
	}
	path := append(append(append(before, usrBin), after...), os.Getenv("PATH"))
	t.Setenv("PATH", strings.Join(path, string(os.PathListSeparator)))
	g := startGuardRig(t, Deps{})
	requireOn(t, g)
	if g.d.keep.Spent() || !g.d.keep.Has() {
		t.Error("the kept password is off or empty on the owner's PATH")
	}
}

// The guard scans the PATH the step's child gets, which a step's own
// variables (steps.Step.Env, proc.Spec.ExtraEnv) can set, not archmaid's
// own: a shadow only on the child's PATH turns keeping off, one only on
// archmaid's doesn't.
func TestPathGuardStepEnvPATH(t *testing.T) {
	real := realSudoDir(t)
	shadow := dirWith(t, "sudo", 0o755)
	sep, orig := string(os.PathListSeparator), os.Getenv("PATH")
	clean, dirty := real+sep+orig, shadow+sep+real+sep+orig
	off := func(own, child string) bool {
		t.Helper()
		t.Setenv("PATH", own)
		st := shStep(steps.IDTopgrade, "true")
		st.NeedsPriv, st.Category = true, logstore.Topgrade
		st.Env = []string{"PATH=" + child}
		r := newRig(t, engine.Options{}, Deps{KeepPassword: true}, action("q", st))
		r.run("q", yes)
		r.d.Close()
		s, _ := r.log.Read(logstore.Topgrade)
		return strings.Contains(s, "Kept password off")
	}
	if !off(clean, dirty) {
		t.Error("a shadow on the step's own PATH (Step.Env): the guard didn't turn keeping off")
	}
	if off(dirty, clean) {
		t.Error("a shadow only on archmaid's PATH, the step's own PATH clean: the guard turned keeping off")
	}
}
