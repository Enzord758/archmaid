package tui

// Askpass end to end, through the TUI's real socket and the real Driver:
// startAskpassServer's Server, driver.New with tuiDriverDeps, and a
// NeedsPriv step whose shimmed sudo runs the SUDO_ASKPASS wrapper it was
// given, exactly as `sudo -A` would. The wrapper execs this test binary
// with --askpass (TestMain dispatches it to privilege.RunClient), so the
// request crosses a real unix socket, gets its peer from SO_PEERCRED and is
// checked by privilege.Request.Verify against the running step's child.
//
// The one fake is privtest.FakeSystem: /proc as it is, except that a
// process named sudo (the shim) looks like the real one (euid 0, its exe
// link unreadable), and, for the refusal cases, that our uid or the helper's start time is made to
// disagree. No transcript scenario sends a request (sudo is a shim there),
// so this is the only test of the whole path above internal/driver's.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/privilege/privtest"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// sockPassword is the only password the su shim accepts. It must never
// show up in an Event, a Driver report or a log file.
const sockPassword = "hunter2-askpass-socket"

// sockPrompt is the prompt the step's sudo passes with -p.
const sockPrompt = "[sudo] password for test:"

// sockWait bounds every wait for the Driver or a shim.
const sockWait = 10 * time.Second

// sockRig is one Driver on the TUI's askpass Server, with its shims.
type sockRig struct {
	t      *testing.T
	d      *driver.Driver
	srv    *privilege.Server
	dir    string // the shims, their answer.<tag> and status.<tag> files
	logDir string
	seen   []engine.Event

	mu      sync.Mutex
	reports []error

	// The refusal cases' knobs on the fake system.
	foreignUID atomic.Bool // our uid is taken to be one the helper doesn't have
	reused     atomic.Bool // a helper's start time changes after the accept
}

// newSockRig starts the askpass Server as the TUI does, under a short
// scratch XDG_RUNTIME_DIR (a socket path has ~108 bytes), and a Driver
// wired with tuiDriverDeps whose menu is one action, "sock", running
// sockScript under sh as a NeedsPriv step. sudo and su are shims first on
// PATH: sudo -n succeeds (the probe and the keep-alive: a cached ticket),
// sudo -A asks $SUDO_ASKPASS and writes what it got to answer.<tag> and
// its exit status to status.<tag>, where tag is its last argument; su
// accepts sockPassword only.
func newSockRig(t *testing.T, withSudo bool) *sockRig {
	t.Helper()
	r := &sockRig{t: t, dir: t.TempDir()}
	sudo := "#!/bin/sh\n" +
		"case \"$1\" in -n) exit 0 ;; -A) ;; *) exit 90 ;; esac\n" +
		"shift\nprompt=\nif [ \"$1\" = -p ]; then prompt=$2; shift 2; fi\n" +
		"for tag; do :; done\n" +
		"\"$SUDO_ASKPASS\" \"$prompt\" > " + shQuote(r.dir) + "/answer.$tag\n" +
		"st=$?\n" +
		"echo $st > " + shQuote(r.dir) + "/status.$tag.tmp\n" +
		"mv " + shQuote(r.dir) + "/status.$tag.tmp " + shQuote(r.dir) + "/status.$tag\n" +
		"exit $st\n"
	su := "#!/bin/sh\nread pw\n[ \"$pw\" = " + shQuote(sockPassword) + " ]\n"
	for name, body := range map[string]string{"sudo": sudo, "su": su} {
		if err := os.WriteFile(filepath.Join(r.dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", r.dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldLog := logDir
	logDir = t.TempDir()
	r.logDir = logDir
	t.Cleanup(func() { logDir = oldLog })

	script := sockScript(r.dir, withSudo)
	oldMenu := actions.Menu
	actions.Menu = []actions.Action{{ID: "sock", Label: "sock", Steps: func(actions.Env) []steps.Step {
		return []steps.Step{{ID: "sock-step", Title: "Socket step…", LogLabel: "Socket step",
			Argv: []string{"sh", "-c", script}, NeedsPriv: true, AlwaysLog: true}}
	}}}
	t.Cleanup(func() { actions.Menu = oldMenu })

	// The fake system goes in before the Server starts: Listen takes its
	// /proc reader (for each peer's start time at accept) from it.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	helperReads := map[int]int{}
	privtest.FakeSystem(t, func() int {
		if r.foreignUID.Load() {
			return os.Getuid() + 1
		}
		return os.Getuid()
	}, "", func(pid int) (proc.Info, error) {
		p, err := proc.Read(pid)
		if err != nil {
			return p, err
		}
		if p.Comm == "sudo" {
			// The shim stands in for sudo, as a user sees the real one:
			// setuid root, its exe link unreadable.
			p.Exe, p.EUID = "", 0
		}
		if p.Exe == self && r.reused.Load() {
			mu.Lock()
			helperReads[pid]++
			if helperReads[pid] > 1 { // read again after the accept
				p.Start++
			}
			mu.Unlock()
		}
		return p, nil
	})

	rt, err := os.MkdirTemp("", "apsk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(rt) })
	t.Setenv("XDG_RUNTIME_DIR", rt)
	oldWrapper := askpassWrapper
	r.srv = startAskpassServer()
	if r.srv == nil {
		t.Fatal("the askpass Server didn't start")
	}
	if !strings.HasPrefix(r.srv.Wrapper(), rt+string(filepath.Separator)) {
		t.Fatalf("the askpass wrapper %q is outside the scratch runtime dir", r.srv.Wrapper())
	}

	opts := engine.Options{Env: actionEnv()}
	if err := opts.Validate(); err != nil {
		t.Fatal(err)
	}
	deps := tuiDriverDeps(r.srv)
	if deps.Askpass == nil {
		t.Fatal("tuiDriverDeps didn't wire the Server's requests")
	}
	deps.Faillock = func() engine.FaillockRead { return engine.FaillockRead{Deny: 3} }
	deps.Status = func() sysstat.Status { return sysstat.Status{} }
	deps.TickEvery = time.Hour
	deps.Report = func(err error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.reports = append(r.reports, err)
	}
	r.d = driver.New(engine.New(opts), deps)
	t.Cleanup(func() {
		r.d.Close()
		stopAskpassServer(r.srv)
		askpassWrapper = oldWrapper
	})
	return r
}

// next is the Driver's next Event (status ticks skipped), recorded in
// seen; a confirm is answered yes. It fails the test after sockWait.
func (r *sockRig) next() engine.Event {
	r.t.Helper()
	timeout := time.After(sockWait)
	for {
		select {
		case ev, ok := <-r.d.Events():
			if !ok {
				r.t.Fatal("the Driver's Events closed")
			}
			if _, tick := ev.(engine.StatusUpdated); tick {
				continue
			}
			r.seen = append(r.seen, ev)
			if c, ok := ev.(engine.NeedsConfirm); ok {
				r.d.Send(engine.Confirm{Prompt: c.ID, Yes: true})
			}
			return ev
		case <-timeout:
			r.t.Fatalf("no Event for %v; seen so far: %s", sockWait, r.kinds())
			return nil
		}
	}
}

// await reads Events until one matches; an askpass prompt before it fails
// the test unless it is what's awaited (a refused peer must never reach
// the user).
func (r *sockRig) await(what string, match func(engine.Event) bool) engine.Event {
	r.t.Helper()
	for {
		ev := r.next()
		if match(ev) {
			return ev
		}
		if p, ok := ev.(engine.NeedsPassword); ok {
			r.t.Fatalf("waiting for %s, got a password prompt: %+v", what, p)
		}
		if _, ok := ev.(engine.Finished); ok {
			r.t.Fatalf("the run finished while waiting for %s: %s; %#v", what, r.kinds(), r.seen)
		}
	}
}

// awaitOutput is the OutputChunk with a line containing s, among the
// Events seen so far (the Driver coalesces a step's lines, so it may have
// come with an earlier one) or the ones to come.
func (r *sockRig) awaitOutput(what, s string) engine.OutputChunk {
	r.t.Helper()
	for _, ev := range r.seen {
		if outputHas(s)(ev) {
			return ev.(engine.OutputChunk)
		}
	}
	return r.await(what, outputHas(s)).(engine.OutputChunk)
}

// outputHas matches an OutputChunk with a line containing s.
func outputHas(s string) func(engine.Event) bool {
	return func(ev engine.Event) bool {
		c, ok := ev.(engine.OutputChunk)
		return ok && strings.Contains(strings.Join(c.Lines, "\n"), s)
	}
}

// start runs the action and returns the step's child pid, the first line
// its script prints (`echo $$`).
func (r *sockRig) start() int {
	r.t.Helper()
	if !r.d.Send(engine.Start{Action: "sock"}) {
		r.t.Fatal("Send(Start) = false on an open Driver")
	}
	c := r.await("the step's pid", func(ev engine.Event) bool {
		_, ok := ev.(engine.OutputChunk)
		return ok
	}).(engine.OutputChunk)
	pid, err := strconv.Atoi(c.Lines[0])
	if err != nil {
		r.t.Fatalf("the step's first line %q: %v", c.Lines[0], err)
	}
	return pid
}

// sudoResult waits for the sudo shim tagged tag to finish and returns what
// its askpass printed and its exit status.
func (r *sockRig) sudoResult(tag string) (answer string, status int) {
	r.t.Helper()
	deadline := time.Now().Add(sockWait)
	for {
		b, err := os.ReadFile(filepath.Join(r.dir, "status."+tag))
		if err == nil {
			status, err = strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				r.t.Fatalf("status.%s = %q", tag, b)
			}
			a, err := os.ReadFile(filepath.Join(r.dir, "answer."+tag))
			if err != nil {
				r.t.Fatal(err)
			}
			return string(a), status
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("sudo %s never finished", tag)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// outsider starts the sudo shim with the step's wrapper from this test
// process, outside the step's process tree, as anything else that learnt
// the wrapper's path could. wait waits for it (bounded by sockWait) and
// returns what its askpass printed and its exit status; the test's
// cleanup kills it if it is still running.
func (r *sockRig) outsider(tag string) (wait func() (answer string, status int)) {
	r.t.Helper()
	cmd := exec.Command(filepath.Join(r.dir, "sudo"), "-A", "-p", sockPrompt, "true", tag)
	cmd.Env = append(os.Environ(), "SUDO_ASKPASS="+r.srv.Wrapper())
	if err := cmd.Start(); err != nil {
		r.t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	r.t.Cleanup(func() { cmd.Process.Kill(); <-done })
	return func() (string, int) {
		r.t.Helper()
		select {
		case <-done:
		case <-time.After(sockWait):
			r.t.Fatalf("the outsider's sudo %s didn't finish", tag)
		}
		return r.sudoResult(tag)
	}
}

// release lets the step's script run to its end.
func (r *sockRig) release() {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, "release"), nil, 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// noLeaks fails if the password is in any Event seen, any Driver report or
// any file under the log dir.
func (r *sockRig) noLeaks() {
	r.t.Helper()
	for _, ev := range r.seen {
		if s := fmt.Sprintf("%#v %v", ev, ev); strings.Contains(s, sockPassword) {
			r.t.Errorf("the password is in an Event: %s", s)
		}
	}
	r.mu.Lock()
	for _, err := range r.reports {
		if strings.Contains(err.Error(), sockPassword) {
			r.t.Errorf("the password is in a Driver report: %v", err)
		}
	}
	r.mu.Unlock()
	err := filepath.WalkDir(r.logDir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), sockPassword) {
			r.t.Errorf("the password is in the log file %s", path)
		}
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *sockRig) kinds() string {
	k := make([]string, len(r.seen))
	for i, ev := range r.seen {
		k[i] = strings.TrimPrefix(fmt.Sprintf("%T", ev), "engine.")
	}
	return strings.Join(k, " ")
}

// sockScript is a step that prints its pid (exiting on the SIGINT a Stop
// sends once the command in hand returns), runs `sudo -A` (tagged
// "step") when withSudo, says how sudo exited, then waits up to ~30 s for
// release.
func sockScript(dir string, withSudo bool) string {
	s := "trap 'exit 130' INT\necho $$\n"
	if withSudo {
		s += "sudo -A -p " + shQuote(sockPrompt) + " true step\necho \"asked, exit $?\"\n"
	}
	return s + "n=0\nwhile [ ! -e " + shQuote(dir) + "/release ] && [ $n -lt 600 ]; do sleep 0.05; n=$((n+1)); done\necho released\n"
}

// The honest path: the step's own sudo asks through its wrapper; the
// prompt names the step's shell as sudo's caller; a wrong password is
// asked again with nothing written; the right one reaches sudo exactly,
// and the run goes on to its end.
func TestAskpassSocketHonest(t *testing.T) {
	r := newSockRig(t, true)
	pid := r.start()

	p := r.await("the askpass prompt", func(ev engine.Event) bool {
		_, ok := ev.(engine.NeedsPassword)
		return ok
	}).(engine.NeedsPassword)
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		t.Fatal(err)
	}
	wantFrom := fmt.Sprintf("%s (pid %d)", sanitize.ClipTail(sanitize.OneLine(exe), 40), pid)
	if p.Kind != engine.PasswordAskpass || p.Step != "sock-step" || p.Requester != wantFrom || p.Prompt != sockPrompt || p.Error != "" {
		t.Fatalf("NeedsPassword = %+v, want an askpass prompt for sock-step from %q", p, wantFrom)
	}

	r.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: "wrong-one"})
	if res := r.await("the wrong password's verdict", func(ev engine.Event) bool {
		_, ok := ev.(engine.AuthResolved)
		return ok
	}).(engine.AuthResolved); res.Granted || res.AttemptsLeft != 2 {
		t.Fatalf("after a wrong password: %+v", res)
	}
	p = r.await("the prompt again", func(ev engine.Event) bool {
		_, ok := ev.(engine.NeedsPassword)
		return ok
	}).(engine.NeedsPassword)
	if p.Kind != engine.PasswordAskpass || p.Error == "" || p.AttemptsLeft != 2 {
		t.Fatalf("asked again: %+v", p)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "status.step")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("sudo finished after a wrong password (err %v): it must keep waiting", err)
	}

	r.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: sockPassword})
	if res := r.await("AuthResolved", func(ev engine.Event) bool {
		_, ok := ev.(engine.AuthResolved)
		return ok
	}).(engine.AuthResolved); !res.Granted {
		t.Fatalf("after the right password: %+v", res)
	}
	if answer, status := r.sudoResult("step"); answer != sockPassword+"\n" || status != 0 {
		t.Fatalf("sudo got %q (askpass exit %d), want exactly the password and its newline", answer, status)
	}
	r.awaitOutput("sudo's exit", "asked, exit 0")

	r.release()
	fin := r.await("Finished", func(ev engine.Event) bool {
		_, ok := ev.(engine.Finished)
		return ok
	}).(engine.Finished)
	if fin.Outcome != engine.OutcomeCompleted || fin.Errs != 0 {
		t.Fatalf("Finished = %+v", fin)
	}
	r.d.Close()
	logged := false
	filepath.WalkDir(r.logDir, func(path string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			b, _ := os.ReadFile(path)
			logged = logged || strings.Contains(string(b), "asked, exit 0")
		}
		return nil
	})
	if !logged {
		t.Fatal("the step's output never reached a log file: the leak check below would prove nothing")
	}
	r.noLeaks()
}

// A request from a peer that fails Verify is hung up on, never shown, and
// leaves a line in the step's output saying why. Each case differs from
// the honest path in one thing only, so each fails if Verify is bypassed.
func TestAskpassSocketRefusedPeers(t *testing.T) {
	for _, c := range []struct {
		name string
		// fromStep: the step's own sudo asks; otherwise one this test runs.
		fromStep bool
		setup    func(r *sockRig)
		why      string // the refusal's reason, as Verify words it
	}{
		{"outside the step", false, nil, "sudo was not started by the running step"},
		{"another uid", true, func(r *sockRig) { r.foreignUID.Store(true) }, "not ours"},
		{"start time changed", true, func(r *sockRig) { r.reused.Store(true) }, "was reused since it connected"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newSockRig(t, c.fromStep)
			if c.setup != nil {
				c.setup(r)
			}
			r.start()
			result := func() (string, int) { return r.sudoResult("step") }
			if !c.fromStep {
				result = r.outsider("outsider")
			}
			// A prompt in place of this line fails at once (await).
			line := r.awaitOutput("the refusal line", "refused a sudo password request")
			answer, status := result()
			if got := strings.Join(line.Lines, "\n"); !strings.Contains(got, c.why) {
				t.Fatalf("refusal line %q, want the reason %q", got, c.why)
			}
			if answer != "" || status == 0 {
				t.Fatalf("a refused peer got %q (askpass exit %d), want a hang-up", answer, status)
			}
			r.release()
			r.await("Finished", func(ev engine.Event) bool {
				_, ok := ev.(engine.Finished)
				return ok
			})
			r.d.Close()
			for _, ev := range r.seen {
				if p, ok := ev.(engine.NeedsPassword); ok {
					t.Fatalf("a refused peer was shown as a prompt: %+v", p)
				}
			}
			r.noLeaks()
		})
	}
}

// With no step running a request is hung up on at once and nothing is
// said: no prompt, no output, no Event at all.
func TestAskpassSocketIdleHungUp(t *testing.T) {
	r := newSockRig(t, false)
	if answer, status := r.outsider("idle")(); answer != "" || status == 0 {
		t.Fatalf("an idle request got %q (askpass exit %d), want a hang-up", answer, status)
	}
	select {
	case ev := <-r.d.Events():
		if _, tick := ev.(engine.StatusUpdated); !tick {
			t.Fatalf("an idle request made an Event: %#v", ev)
		}
	case <-time.After(300 * time.Millisecond):
	}
	r.noLeaks()
}

// A request still open when the Driver closes is hung up on by Close.
func TestAskpassSocketOpenAtCloseHungUp(t *testing.T) {
	r := newSockRig(t, true)
	r.start()
	r.await("the askpass prompt", func(ev engine.Event) bool {
		_, ok := ev.(engine.NeedsPassword)
		return ok
	})
	r.d.Close()
	if answer, status := r.sudoResult("step"); answer != "" || status == 0 {
		t.Fatalf("a request open at Close got %q (askpass exit %d), want a hang-up", answer, status)
	}
	r.noLeaks()
}
