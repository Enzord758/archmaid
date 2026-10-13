package driver

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/privilege/privtest"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// The kept password's PATH guard (pathguard.go) on a real step child under
// a Driver that keeps the password, as the GUI's: sysbin.Sudo is a "real"
// sudo (a stand-in at an absolute path, never run) in a dir of its own on
// the step's PATH, sysbin.Su takes "good" only, and the dirs a case puts
// before or after the real sudo's decide.

// guardRig is one run of a NeedsPriv step (the fake process table calls it
// /usr/bin/topgrade) whose PATH is before + the real sudo's dir + after +
// the test's own PATH.
type guardRig struct {
	*rig
	reqs  chan *privilege.Request
	pid   int
	start uint64
	lines []string // the step's output so far
}

func newGuardRig(t *testing.T, before, after []string) *guardRig {
	t.Helper()
	testenv.UseSysbin(t, &sysbin.Su, `read pw; [ "$pw" = good ]`)
	real := filepath.Dir(testenv.UseSysbin(t, &sysbin.Sudo, "exit 0"))
	path := append(append(append([]string{}, before...), real), after...)
	t.Setenv("PATH", strings.Join(append(path, os.Getenv("PATH")), string(os.PathListSeparator)))
	return startGuardRig(t, Deps{})
}

// startGuardRig starts the run on the PATH the test has set.
func startGuardRig(t *testing.T, deps Deps) *guardRig {
	t.Helper()
	end := filepath.Join(t.TempDir(), "end")
	g := &guardRig{reqs: make(chan *privilege.Request)}
	deps.Askpass, deps.KeepPassword, deps.Detach = g.reqs, true, true
	st := shStep(steps.IDTopgrade, "echo pid=$$; p=$PPID; while [ ! -f "+end+" ] && kill -0 $p 2>/dev/null; do sleep 0.02; done")
	st.NeedsPriv, st.Category = true, logstore.Topgrade
	g.rig = newRig(t, engine.Options{}, deps, action("a", st))
	g.d.Send(engine.Start{Action: "a"})
	for g.pid == 0 {
		ev, ok := g.next()
		if !ok {
			t.Fatal("events ended before the step printed its pid")
		}
		if c, isOut := ev.(engine.OutputChunk); isOut {
			g.lines = append(g.lines, c.Lines...)
			for _, l := range c.Lines {
				if v, ok := strings.CutPrefix(l, "pid="); ok {
					g.pid, _ = strconv.Atoi(v)
				}
			}
		}
	}
	child, err := proc.Read(g.pid)
	if err != nil {
		t.Fatal(err)
	}
	g.start = child.Start
	t.Cleanup(func() {
		_ = os.WriteFile(end, nil, 0o600)
		if c, err := proc.Read(g.pid); err == nil && c.Start == g.start {
			_ = syscall.Kill(-g.pid, syscall.SIGKILL)
		}
	})
	pid, s := g.pid, g.start
	privtest.FakeSystem(t, func() int { return ourUID }, "/fake/archmaid", func(p int) (proc.Info, error) {
		switch p {
		case helperPid:
			return proc.Info{PPid: sudoPid, Start: s + 20, Exe: "/fake/archmaid", Session: pid}, nil
		case helperPid2:
			return proc.Info{PPid: sudoPid2, Start: s + 20, Exe: "/fake/archmaid", Session: pid}, nil
		case sudoPid:
			return proc.Info{PPid: pid, Start: s + 10, Exe: "/usr/bin/" + "sudo"}, nil
		case sudoPid2:
			return proc.Info{PPid: pid, Start: s + 11, Exe: "/usr/bin/" + "sudo"}, nil
		case pid:
			return proc.Info{PPid: 1, Start: s, Exe: "/usr/bin/topgrade", Session: pid}, nil
		}
		return proc.Info{}, errors.New("no such process")
	})
	return g
}

// ask sends a request from helper pid and returns its client.
func (g *guardRig) ask(pid int) *askpassClient {
	g.t.Helper()
	c := newAskpassClient(g.t, "[sudo] password for me:", privilege.Peer{Pid: pid, UID: ourUID, Start: g.start + 20})
	sendReq(g.t, g.reqs, c.req)
	return c
}

// until reads events, keeping the output lines, up to the first that
// matches; it fails if the events end first.
func (g *guardRig) until(what string, match func(engine.Event) bool) engine.Event {
	g.t.Helper()
	for {
		ev, ok := g.next()
		if !ok {
			g.t.Fatalf("events ended before %s", what)
		}
		if c, isOut := ev.(engine.OutputChunk); isOut {
			g.lines = append(g.lines, c.Lines...)
		}
		if match(ev) {
			return ev
		}
	}
}

// answer runs the run's first request as the user does: asked, answered
// "good" (which is kept, unless the guard turned keeping off), resolved.
func (g *guardRig) answer() {
	g.t.Helper()
	first := g.ask(helperPid)
	p := g.until("the first prompt", func(ev engine.Event) bool { _, ok := ev.(engine.NeedsPassword); return ok }).(engine.NeedsPassword)
	g.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: "good"})
	if line := got(g.t, first); line != "good\n" {
		g.t.Fatalf("the first sudo got %q", line)
	}
	g.until("the first request's end", func(ev engine.Event) bool { _, ok := ev.(engine.AuthResolved); return ok })
}

// second is the run's second sudo asking: answered from the kept password
// (true), or the user is asked, which the test cancels (false).
func (g *guardRig) second() (kept bool) {
	g.t.Helper()
	c := g.ask(helperPid2)
	timeout := time.After(5 * time.Second)
	for {
		select {
		case line := <-c.got:
			if line != "good\n" {
				g.t.Fatalf("the second sudo got %q", line)
			}
			return true
		case ev, ok := <-g.d.Events():
			if !ok {
				g.t.Fatal("events ended before the second sudo was answered")
			}
			if out, isOut := ev.(engine.OutputChunk); isOut {
				g.lines = append(g.lines, out.Lines...)
			}
			if p, isPrompt := ev.(engine.NeedsPassword); isPrompt {
				g.d.Send(engine.CancelPrompt{Prompt: p.ID})
				if line := got(g.t, c); line != "" {
					g.t.Fatalf("a cancelled prompt's sudo got %q", line)
				}
				return false
			}
		case <-timeout:
			g.t.Fatal("the second sudo got no answer and the user wasn't asked")
		}
	}
}

// topgradeLog is the run's topgrade log so far.
func (g *guardRig) topgradeLog() string {
	s, _ := g.log.Read(logstore.Topgrade)
	return s
}

const guardLine = "archmaid: the kept password is off for this run, so every password prompt asks you ("

// requireOff: the guard turned the kept password off, naming each of
// names in one WARN line of the topgrade log and one line of the step's
// output; the run's second sudo got the dialog.
func requireOff(t *testing.T, g *guardRig, names ...string) {
	t.Helper()
	g.answer()
	if g.second() {
		t.Fatal("the second sudo was answered from the kept password: the guard didn't turn it off")
	}
	if !g.d.keep.Spent() || g.d.keep.Has() {
		t.Error("the kept password isn't off")
	}
	var out []string
	for _, l := range g.lines {
		if strings.HasPrefix(l, "archmaid: ") {
			out = append(out, l)
		}
	}
	if len(out) != 1 || !strings.HasPrefix(out[0], guardLine) {
		t.Fatalf("step output's guard lines %q, want one starting %q", out, guardLine)
	}
	if !strings.HasPrefix(g.lines[0], guardLine) {
		t.Errorf("the guard's line is not the step's first: %q", g.lines)
	}
	log := g.topgradeLog()
	if n := strings.Count(log, "WARN: Kept password off for this run: "); n != 1 {
		t.Errorf("topgrade log has %d guard WARN lines, want 1:\n%s", n, log)
	}
	for _, n := range names {
		if !strings.Contains(out[0], n) || !strings.Contains(log, n) {
			t.Errorf("%q not named in the output line %q or the log:\n%s", n, out[0], log)
		}
	}
}

// requireOn: no guard line anywhere, and the second sudo got the kept
// password without a dialog.
func requireOn(t *testing.T, g *guardRig) {
	t.Helper()
	g.answer()
	if !g.second() {
		t.Fatal("the second sudo got the dialog: the guard turned the kept password off")
	}
	for _, l := range g.lines {
		if strings.HasPrefix(l, "archmaid: ") {
			t.Errorf("guard line in the output: %q", l)
		}
	}
	if log := g.topgradeLog(); strings.Contains(log, "Kept password off") {
		t.Errorf("guard line in the log:\n%s", log)
	}
}

func dirWith(t *testing.T, name string, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexec /usr/bin/false\n"), mode); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A sudo in a dir before the real one's turns the kept password off: one
// WARN line in the topgrade log and one line in the step's output name
// the file, and the run's next sudo gets the dialog.
func TestPathGuardShadowBefore(t *testing.T) {
	shadow := dirWith(t, "sudo", 0o755)
	requireOff(t, newGuardRig(t, []string{shadow}, nil), filepath.Join(shadow, "sudo"))
}

// A sudo after the real one's can't stand in for it: no effect.
func TestPathGuardShadowAfter(t *testing.T) {
	requireOn(t, newGuardRig(t, nil, []string{dirWith(t, "sudo", 0o755)}))
}

// A sudo without an execute bit can't run: no effect.
func TestPathGuardNotExecutable(t *testing.T) {
	requireOn(t, newGuardRig(t, []string{dirWith(t, "sudo", 0o644)}, nil))
}

// A symlink named sudo before the real one counts even when it points at
// the real sudo (what it points at can change mid-run).
func TestPathGuardSymlinkToRealSudo(t *testing.T) {
	link := t.TempDir()
	testenv.UseSysbin(t, &sysbin.Su, `read pw; [ "$pw" = good ]`)
	real := testenv.UseSysbin(t, &sysbin.Sudo, "exit 0")
	if err := os.Symlink(real, filepath.Join(link, "sudo")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", link+":"+filepath.Dir(real)+":"+os.Getenv("PATH"))
	requireOff(t, startGuardRig(t, Deps{}), filepath.Join(link, "sudo"))
}

// The other ways to root count too (here doas and pkexec), and so does a
// relative PATH entry before the real sudo.
func TestPathGuardOtherNamesAndRelative(t *testing.T) {
	doas, pk := dirWith(t, "doas", 0o700), dirWith(t, "pkexec", 0o755)
	requireOff(t, newGuardRig(t, []string{doas, "relbin", pk}, nil),
		filepath.Join(doas, "doas"), `the relative PATH entry "relbin"`, filepath.Join(pk, "pkexec"))
}

// A scan that doesn't finish in time counts as a shadow: off, saying so.
func TestPathGuardScanTimeout(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free)
	oldScan, oldMax := pathShadows, pathScanMax
	pathShadows = func(string) []privilege.Shadow { <-release; return nil }
	pathScanMax = 50 * time.Millisecond
	t.Cleanup(func() { pathShadows, pathScanMax = oldScan, oldMax })
	requireOff(t, newGuardRig(t, nil, nil), "didn't finish in 50ms")
	free() // the abandoned scan ends now, before the rig's leak check
}

// Under testenv (sysbin.Sudo bare: PATH decides every program) the guard
// finds nothing, so the other tests' shims first on PATH change nothing.
func TestPathGuardBareSudo(t *testing.T) {
	testenv.UseSysbin(t, &sysbin.Su, `read pw; [ "$pw" = good ]`)
	t.Setenv("PATH", dirWith(t, "sudo", 0o755)+":"+os.Getenv("PATH"))
	requireOn(t, startGuardRig(t, Deps{}))
}

// guardOff runs action a on a fresh Driver (KeepPassword per keep) with a
// shadowing sudo first on PATH and reports whether the guard went off:
// a WARN line in the topgrade log.
func guardOff(t *testing.T, keep bool, opts engine.Options, st steps.Step) bool {
	t.Helper()
	real := filepath.Dir(testenv.UseSysbin(t, &sysbin.Sudo, "exit 0"))
	t.Setenv("PATH", dirWith(t, "sudo", 0o755)+":"+real+":"+os.Getenv("PATH"))
	st.Category = logstore.Topgrade
	r := newRig(t, opts, Deps{KeepPassword: keep}, action("q", st))
	evs := r.run("q", yes)
	r.d.Close()
	out, _ := lines(evs)
	s, _ := r.log.Read(logstore.Topgrade)
	off := strings.Contains(s, "Kept password off")
	if on := strings.Contains(strings.Join(out, "\n"), guardLine); on != off {
		t.Errorf("log says off=%v, output says %v", off, on)
	}
	return off
}

// The guard only runs where a kept password could be handed out: a
// NeedsPriv child step, for real, under a Driver that keeps the password.
// Not for a step that doesn't get the askpass, a dry run, or the TUI.
func TestPathGuardOnlyWhereItMatters(t *testing.T) {
	priv := shStep(steps.IDTopgrade, "true")
	priv.NeedsPriv = true
	plain := shStep(steps.IDTopgrade, "true")
	if !guardOff(t, true, engine.Options{}, priv) {
		t.Error("a NeedsPriv step under KeepPassword: the guard didn't run")
	}
	if guardOff(t, true, engine.Options{}, plain) {
		t.Error("a step without NeedsPriv: the guard ran")
	}
	if guardOff(t, true, engine.Options{DryRun: true}, priv) {
		t.Error("a dry run: the guard ran")
	}
	if guardOff(t, false, engine.Options{}, priv) {
		t.Error("no KeepPassword (the TUI): the guard ran")
	}
}

// The guard's WARN line that can't be written is reported; the kept
// password is off all the same.
func TestPathGuardUnloggable(t *testing.T) {
	real := filepath.Dir(testenv.UseSysbin(t, &sysbin.Sudo, "exit 0"))
	t.Setenv("PATH", dirWith(t, "sudo", 0o755)+":"+real+":"+os.Getenv("PATH"))
	st := shStep(steps.IDTopgrade, "true")
	st.NeedsPriv = true
	r := newRig(t, engine.Options{}, Deps{KeepPassword: true, Log: logstore.New(logstore.Options{Dir: "relative"})}, action("q", st))
	evs := r.run("q", yes)
	if out, _ := lines(evs); len(out) == 0 || !strings.HasPrefix(out[0], guardLine) {
		t.Errorf("output %q, want the guard's line first", out)
	}
	if !r.d.keep.Spent() {
		t.Error("the kept password isn't off")
	}
	if s := r.reports.text(); !strings.Contains(s, "the kept password is off") {
		t.Errorf("reports %q, want the failed WARN line", s)
	}
}

// namesOf names up to three shadows, then how many more.
func TestPathGuardNamesOf(t *testing.T) {
	sh := func(ps ...string) []privilege.Shadow {
		var out []privilege.Shadow
		for _, p := range ps {
			out = append(out, privilege.Shadow{Path: p})
		}
		return out
	}
	for _, c := range []struct {
		in   []privilege.Shadow
		want string
	}{
		{sh("/a/sudo"), "/a/sudo"},
		{sh("/a/sudo", "/b/su"), "/a/sudo and /b/su"},
		{sh("/a/sudo", "/b/su", "/c/doas"), "/a/sudo, /b/su and /c/doas"},
		{sh("/a/sudo", "/b/su", "/c/doas", "/d/run0", "/e/please"), "/a/sudo, /b/su, /c/doas and 2 more"},
		{sh("/a/\x1b[31msudo\n"), "/a/sudo"},
	} {
		if got := namesOf(c.in); got != c.want {
			t.Errorf("namesOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The update step's topgrade runs from sysbin.Topgrade through the real
// privilege.Sudo, never a topgrade on PATH. (Not NeedsPriv here, so the
// run needs no sudo ticket: Sudo.Command maps the name either way.)
func TestTopgradeRunsSysbin(t *testing.T) {
	testenv.UseSysbin(t, &sysbin.Topgrade, `printf '%s\n' "sysbin topgrade $*"`)
	decoys := testenv.Decoys(t, "topgrade", "sudo", "su")
	st := steps.Step{ID: steps.IDTopgrade, Title: "t", LogLabel: "t", Argv: []string{"topgrade", "-y"}, AlwaysLog: true}
	r := newRig(t, engine.Options{}, Deps{Priv: privilege.Sudo{}}, action("q", st))
	evs := r.run("q", yes)
	if out, _ := lines(evs); strings.Join(out, "\n") != "sysbin topgrade -y" {
		t.Errorf("output %q, want the sysbin topgrade's line", out)
	}
	if b, _ := os.ReadFile(decoys); len(b) > 0 {
		t.Errorf("ran from PATH instead of internal/sysbin:\n%s", b)
	}
}
