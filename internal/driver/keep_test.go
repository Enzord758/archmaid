package driver

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/privilege/privtest"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The kept password (Deps.KeepPassword, keep.go) in the Driver alone:
// fake sudo processes (privtest.FakeSystem) around a real step child, and
// fake polkit agent prompts around a fake helper.

// More fake pids: a second and a third askpass helper, the sudo processes
// they run under, and a helper in a session that isn't the step's.
const (
	helperPid2 = 990011
	helperPid3 = 990012
	sudoPid2   = 990021
	foreignPid = 990031
	// The chains through yay: helperYay under sudoYay under yayPid under
	// the step; helperDeep under sudoDeep under a PKGBUILD's bash under
	// makepkg (a bash script) under yayPid.
	helperYay  = 990041
	sudoYay    = 990042
	yayPid     = 990043
	helperDeep = 990051
	sudoDeep   = 990052
	bashPid    = 990053
	makepkgPid = 990054
)

// keepRig is a run of one long NeedsPriv step (a real child, which the
// fake process table calls /usr/bin/topgrade) under a Driver that keeps
// the run's password, with the fake process table around it: helperPid
// under sudoPid and helperPid2 under sudoPid2 in the child's session, both
// sudos run by the step itself; helperPid3 under sudoPid again; foreignPid
// outside the session; and the yay chains (helperYay, helperDeep).
type keepRig struct {
	*rig
	reqs  chan *privilege.Request
	pid   int
	start uint64
	file  string // the step exits once it exists: 0 if it holds "ok"
	// capped: Deps.KeepFor is short, so the kept password may be gone
	// (wiped at the cap) by the time capture looks.
	capped bool
}

func newKeepRig(t *testing.T, deps Deps) *keepRig {
	t.Helper()
	suShim(t)
	k := &keepRig{reqs: make(chan *privilege.Request), file: filepath.Join(t.TempDir(), "end"), capped: deps.KeepFor > 0}
	deps.Askpass, deps.KeepPassword, deps.Detach = k.reqs, true, true
	// The loop also ends when this test binary does ($PPID gone): the
	// step is in a session of its own (Detach), so nothing else would end
	// it if the binary died before its cleanups.
	st := shStep("sudo-step", "echo $$; p=$PPID; while [ ! -f "+k.file+" ] && kill -0 $p 2>/dev/null; do sleep 0.02; done; [ \"$(cat "+k.file+")\" = ok ]")
	st.NeedsPriv = true
	k.rig = newRig(t, engine.Options{}, deps, action("a", st))
	k.d.Send(engine.Start{Action: "a"})
	for k.pid == 0 {
		ev, _ := k.next()
		if _, ok := ev.(engine.OutputChunk); ok {
			k.pid = pidOf(t, ev)
		}
	}
	child, err := proc.Read(k.pid)
	if err != nil {
		t.Fatal(err)
	}
	k.start = child.Start
	// However the test ends, the step ends (before the Driver's Close, as
	// cleanups run last-in first-out): its end file, then its process
	// group, while it is still this step's (same start time).
	t.Cleanup(func() {
		_ = os.WriteFile(k.file, []byte("ok"), 0o600)
		if c, err := proc.Read(k.pid); err == nil && c.Start == k.start {
			_ = syscall.Kill(-k.pid, syscall.SIGKILL)
		}
	})
	pid, s := k.pid, k.start
	privtest.FakeSystem(t, func() int { return ourUID }, "/fake/archmaid", func(p int) (proc.Info, error) {
		switch p {
		case helperPid, helperPid3:
			return proc.Info{PPid: sudoPid, Start: s + 20, Exe: "/fake/archmaid", Session: pid}, nil
		case helperYay:
			return proc.Info{PPid: sudoYay, Start: s + 20, Exe: "/fake/archmaid", Session: pid}, nil
		case helperDeep:
			return proc.Info{PPid: sudoDeep, Start: s + 20, Exe: "/fake/archmaid", Session: pid}, nil
		case sudoYay:
			return proc.Info{PPid: yayPid, Start: s + 12, Exe: "/usr/bin/sudo"}, nil
		case sudoDeep:
			return proc.Info{PPid: bashPid, Start: s + 13, Exe: "/usr/bin/sudo"}, nil
		case bashPid:
			return proc.Info{PPid: makepkgPid, Start: s + 3, Exe: "/usr/bin/bash", Session: pid}, nil
		case makepkgPid:
			return proc.Info{PPid: yayPid, Start: s + 2, Exe: "/usr/bin/bash", Comm: "makepkg", Session: pid}, nil
		case yayPid:
			return proc.Info{PPid: pid, Start: s + 1, Exe: "/usr/bin/yay", Session: pid}, nil
		case helperPid2:
			return proc.Info{PPid: sudoPid2, Start: s + 20, Exe: "/fake/archmaid", Session: pid}, nil
		case foreignPid:
			return proc.Info{PPid: sudoPid2, Start: s + 20, Exe: "/fake/archmaid", Session: pid + 1}, nil
		case sudoPid:
			return proc.Info{PPid: pid, Start: s + 10, Exe: "/usr/bin/sudo"}, nil
		case sudoPid2:
			return proc.Info{PPid: pid, Start: s + 11, Exe: "/usr/bin/sudo"}, nil
		case pid:
			return proc.Info{PPid: 1, Start: s, Exe: "/usr/bin/topgrade", Session: pid}, nil
		}
		return proc.Info{}, errors.New("no such process")
	})
	return k
}

// ask sends a request from helper pid and returns its client.
func (k *keepRig) ask(pid int) *askpassClient {
	k.t.Helper()
	c := newAskpassClient(k.t, "[sudo] password for me:", privilege.Peer{Pid: pid, UID: ourUID, Start: k.start + 20})
	sendReq(k.t, k.reqs, c.req)
	return c
}

// prompt waits for the Session's next password prompt.
func (k *keepRig) prompt() engine.NeedsPassword {
	k.t.Helper()
	for {
		ev, ok := k.next()
		if !ok {
			k.t.Fatal("events ended with no password prompt")
		}
		if p, ok := ev.(engine.NeedsPassword); ok {
			return p
		}
	}
}

// got is what the client was sent, or fails after 5 s.
func got(t *testing.T, c *askpassClient) string {
	t.Helper()
	select {
	case line := <-c.got:
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("the askpass client got no answer")
		return ""
	}
}

// noPrompt fails if a password prompt comes within a short while.
func (k *keepRig) noPrompt(what string) {
	k.t.Helper()
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case ev, ok := <-k.d.Events():
			if !ok {
				return
			}
			if p, isPrompt := ev.(engine.NeedsPassword); isPrompt {
				k.t.Fatalf("%s: the user was asked: %+v", what, p)
			}
		case <-deadline:
			return
		}
	}
}

// capture answers the first request as the user, which keeps it, and
// returns once the Session has closed that request (AuthResolved). Not
// before: the answer reaches sudo, and is kept, on a worker that only then
// posts AskpassAnswered, and until the Session has taken that, its request
// is still open and it hangs up any other one ("a second sudo password
// request while one is open"), so a test's next request to the Session
// would get no prompt.
func (k *keepRig) capture() {
	k.t.Helper()
	first := k.ask(helperPid)
	p := k.prompt()
	k.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: "good"})
	if line := got(k.t, first); line != "good\n" {
		k.t.Fatalf("the first sudo got %q", line)
	}
	for {
		ev, ok := k.next()
		if !ok {
			k.t.Fatal("events ended before the first request was resolved")
		}
		if r, isRes := ev.(engine.AuthResolved); isRes {
			if !r.Granted {
				k.t.Fatalf("the first request resolved as %+v", r)
			}
			break
		}
		if p, isPrompt := ev.(engine.NeedsPassword); isPrompt {
			k.t.Fatalf("asked again after the right password: %+v", p)
		}
	}
	// The worker kept it before it posted AskpassAnswered.
	if !k.d.keep.Has() && !(k.capped && k.d.keep.Wiped()) {
		k.t.Fatal("the user's password wasn't kept")
	}
}

// A second sudo of the run is answered from the kept password without
// asking; the first sudo asking again is not: it refused what it got, so
// the kept password is dropped and the user is asked.
func TestKeepAnswersTheRunsSudo(t *testing.T) {
	k := newKeepRig(t, Deps{})
	k.capture()
	second := k.ask(helperPid2)
	if line := got(t, second); line != "good\n" {
		t.Fatalf("the second sudo got %q", line)
	}
	k.noPrompt("a second sudo of the run")
	again := k.ask(helperPid3) // under sudoPid, which asked first
	p := k.prompt()
	if !k.d.keep.Wiped() {
		t.Fatal("the kept password outlived a sudo asking again")
	}
	if p.AttemptsLeft != 3 {
		t.Errorf("prompt %+v", p)
	}
	k.d.Send(engine.CancelPrompt{Prompt: p.ID})
	if line := got(t, again); line != "" {
		t.Fatalf("a sudo asking again got %q", line)
	}
}

// Only topgrade and the yay it runs may stand between the step and a sudo
// the kept password answers: step (topgrade) → yay → sudo is answered
// without asking; step → yay → makepkg → a PKGBUILD's bash → sudo gets the
// user's dialog naming that bash, never the kept password, and keeps it
// for topgrade's own sudo.
func TestKeepOnlyTopgradeAndYay(t *testing.T) {
	k := newKeepRig(t, Deps{})
	k.capture()
	viaYay := k.ask(helperYay)
	if line := got(t, viaYay); line != "good\n" {
		t.Fatalf("topgrade → yay → sudo got %q, want the kept password", line)
	}
	k.noPrompt("topgrade → yay → sudo")
	deep := k.ask(helperDeep)
	p := k.prompt()
	if p.Requester != "/usr/bin/bash (pid "+strconv.Itoa(bashPid)+")" {
		t.Errorf("the dialog names %q, want the PKGBUILD's bash", p.Requester)
	}
	k.d.Send(engine.CancelPrompt{Prompt: p.ID})
	if line := got(t, deep); line != "" {
		t.Fatalf("a sudo under makepkg got %q", line)
	}
	if !k.d.keep.Has() {
		t.Fatal("a refused-to-answer chain dropped the kept password")
	}
	own := k.ask(helperPid2) // topgrade's own sudo
	if line := got(t, own); line != "good\n" {
		t.Fatalf("topgrade's own sudo got %q", line)
	}
}

// Not from this run: a peer that fails Verify is hung up on, and a
// verified one outside the step's session gets the user's dialog, never
// the kept password; neither drops it.
func TestKeepNotForeignRequests(t *testing.T) {
	k := newKeepRig(t, Deps{})
	k.capture()
	bad := newAskpassClient(t, "[sudo] password:", privilege.Peer{Pid: helperPid2, UID: ourUID + 1, Start: k.start + 20})
	sendReq(t, k.reqs, bad.req)
	if line := got(t, bad); line != "" {
		t.Fatalf("a foreign uid got %q", line)
	}
	foreign := k.ask(foreignPid)
	p := k.prompt()
	k.d.Send(engine.CancelPrompt{Prompt: p.ID})
	if line := got(t, foreign); line != "" {
		t.Fatalf("a request outside the step's session got %q", line)
	}
	if !k.d.keep.Has() {
		t.Fatal("a foreign request dropped the kept password")
	}
}

// The user's password is kept once su has accepted it, before sudo has it:
// a sudo of the run that asks while the first answer is still going out
// (here sudo has read the password but not its line end yet) is answered
// from it, not hung up as a second request to the Session.
func TestKeepBeforeSudoHasIt(t *testing.T) {
	k := newKeepRig(t, Deps{})
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })
	sendReq(t, k.reqs, privtest.NewRequest(server, "[sudo] password for me:", privilege.Peer{Pid: helperPid, UID: ourUID, Start: k.start + 20}))
	p := k.prompt()
	k.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: "good"})
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	read := func(n int) string {
		t.Helper()
		b := make([]byte, n)
		if _, err := io.ReadFull(client, b); err != nil {
			t.Fatalf("the first sudo's side: %v", err)
		}
		return string(b)
	}
	if line := read(len("verified\n")); line != "verified\n" {
		t.Fatalf("handshake %q", line)
	}
	if _, err := io.WriteString(client, "ok\n"); err != nil {
		t.Fatal(err)
	}
	if pw := read(len("good")); pw != "good" {
		t.Fatalf("the first sudo got %q", pw)
	}
	second := k.ask(helperPid2) // the newline is still to come
	if line := got(t, second); line != "good\n" {
		t.Fatalf("a sudo asking while the first answer went out got %q, want the kept password", line)
	}
	if rest := read(1); rest != "\n" {
		t.Fatalf("the first sudo's line ends in %q", rest)
	}
	k.noPrompt("a sudo asking while the first answer went out")
}

// A kept password su now refuses is never written: it is dropped and the
// user is asked for that request.
func TestKeepRefusedBySu(t *testing.T) {
	k := newKeepRig(t, Deps{})
	k.capture()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "su"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	second := k.ask(helperPid2)
	p := k.prompt()
	if !k.d.keep.Wiped() {
		t.Fatal("a password su refused is still kept")
	}
	k.d.Send(engine.CancelPrompt{Prompt: p.ID})
	if line := got(t, second); line != "" {
		t.Fatalf("sudo got %q", line)
	}
}

// The kept password is wiped (read back as zero) at every end of the run.
func TestKeepWipedAtEveryEnd(t *testing.T) {
	for _, end := range []string{"ok", "failed", "abort", "close", "killnow", "cap"} {
		t.Run(end, func(t *testing.T) {
			deps := Deps{}
			if end == "cap" {
				deps.KeepFor = 300 * time.Millisecond
			}
			k := newKeepRig(t, deps)
			k.capture()
			switch end {
			case "ok", "failed":
				body := "ok"
				if end == "failed" {
					body = "no"
				}
				if err := os.WriteFile(k.file, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				for {
					ev, ok := k.next()
					if !ok {
						t.Fatal("no Finished")
					}
					if f, isFin := ev.(engine.Finished); isFin {
						if (f.Errs > 0) != (end == "failed") {
							t.Fatalf("Finished = %+v", f)
						}
						break
					}
				}
			case "abort":
				k.d.Send(engine.Abort{})
				for {
					ev, _ := k.next()
					if _, isFin := ev.(engine.Finished); isFin {
						break
					}
				}
			case "close":
				k.d.Close()
			case "killnow":
				k.d.KillNow()
			case "cap":
				deadline := time.Now().Add(5 * time.Second)
				for k.d.keep.Has() && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if !k.d.keep.Wiped() {
				t.Fatalf("%s: the kept password wasn't wiped", end)
			}
			if k.d.keep.Keep([]byte("good")) {
				t.Fatalf("%s: a password could be kept after the run's end", end)
			}
			_ = os.WriteFile(k.file, []byte("ok"), 0o600)
		})
	}
}

// fakePrompt is polkit's agent asking: Answer records the password and
// polkit's verdict is right exactly for want.
type fakePrompt struct {
	action, want string
	mu           sync.Mutex
	answers      []string
	err          error
	done         chan struct{}
	once         sync.Once
	cancelled    chan struct{}
	cancelOnce   sync.Once
	// then, if set, runs at the end of Answer, after polkit's verdict is
	// out (done closed): what that verdict sets off can happen before
	// Answer returns, as with the real agent.
	then func()
	// answerErr, if set, is Answer's error: the agent went away before it
	// took the password, so polkit gives no verdict on it.
	answerErr error
}

// setThen sets p.then.
func (p *fakePrompt) setThen(f func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.then = f
}

func newFakePrompt(action, want string) *fakePrompt {
	return &fakePrompt{action: action, want: want, done: make(chan struct{}), cancelled: make(chan struct{})}
}

func (p *fakePrompt) Action() string { return p.action }
func (p *fakePrompt) Prompt() string { return "Password: " }
func (p *fakePrompt) Answer(pw []byte) error {
	p.mu.Lock()
	p.answers = append(p.answers, string(pw))
	if p.answerErr != nil {
		defer p.mu.Unlock()
		return p.answerErr
	}
	if string(pw) != p.want {
		p.err = errors.New("polkit: authentication failed")
	}
	then := p.then
	p.mu.Unlock()
	p.once.Do(func() { close(p.done) })
	if then != nil {
		then()
	}
	return nil
}
func (p *fakePrompt) Done() <-chan struct{} { return p.done }
func (p *fakePrompt) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}
func (p *fakePrompt) Cancel() { p.cancelOnce.Do(func() { close(p.cancelled) }) }

func (p *fakePrompt) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.answers...)
}

// agentHelper is a Helper whose pkexec asks polkit, which asks the agent
// (a fresh fakePrompt, sent on prompts): granted when polkit's verdict is
// right, refused when it isn't or the prompt is cancelled.
func agentHelper(agent chan<- AgentPrompt, prompts chan<- *fakePrompt, want string) Helper {
	return func(ctx context.Context, seq uint64, _ steps.Step, emit func(engine.Input)) helperclient.Outcome {
		p := newFakePrompt("org.archmaid.helper.full-cleanup", want)
		prompts <- p // buffered: the tests make room for every helper step
		select {
		case agent <- p:
		case <-ctx.Done():
			emit(engine.StepExited{Seq: seq, NotGranted: true, Err: ctx.Err()})
			return helperclient.Outcome{NotGranted: true}
		}
		select {
		case <-p.done:
			if p.Err() == nil {
				emit(engine.PrivilegeGranted{Seq: seq})
				emit(engine.StepExited{Seq: seq})
				return helperclient.Outcome{Granted: true}
			}
		case <-p.cancelled:
		case <-ctx.Done():
		}
		emit(engine.StepExited{Seq: seq, NotGranted: true, Err: errors.New("exit status 127")})
		return helperclient.Outcome{NotGranted: true}
	}
}

// nextPrompt is the next helper step's polkit prompt, or fails after 10 s.
func nextPrompt(t *testing.T, prompts <-chan *fakePrompt) *fakePrompt {
	t.Helper()
	select {
	case p := <-prompts:
		return p
	case <-time.After(10 * time.Second):
		t.Fatal("no helper step asked polkit within 10 s")
		return nil
	}
}

// polkit's agent: the run's first prompt goes to the user (su-checked,
// then to the agent), and is kept; the next helper steps' prompts are
// answered from it without asking. The password su refuses never reaches
// polkit. polkit's verdict on the first answer sets off the next helper
// step, whose prompt may come in before that Answer has returned (here it
// always does): the password is kept by then.
func TestKeepAnswersPolkitsAgent(t *testing.T) {
	suShim(t)
	agent, prompts := make(chan AgentPrompt), make(chan *fakePrompt, 3)
	h := steps.Step{ID: "h1", Title: "Root…", LogLabel: "Root", Verb: steps.VerbPaccache, Argv: []string{"false"}, NeedsPriv: true}
	h2, h3 := h, h
	h2.ID, h3.ID = "h2", "h3"
	r := newRig(t, engine.Options{Auth: engine.AuthPolkit},
		Deps{Polkit: true, Agent: agent, KeepPassword: true, Helper: agentHelper(agent, prompts, "good")}, action("pk", h, h2, h3))
	r.d.Send(engine.Start{Action: "pk"})
	k := &keepRig{rig: r}
	p := k.prompt()
	if p.Kind != engine.PasswordAskpass || !p.Polkit || p.Action != "org.archmaid.helper.full-cleanup" {
		t.Fatalf("NeedsPassword = %+v", p)
	}
	first := nextPrompt(t, prompts)
	release := make(chan struct{})
	first.setThen(func() { // the first Answer returns once the next prompt is over
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	})
	r.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: "bad"}) // su refuses it
	p = k.prompt()
	if p.AttemptsLeft != 2 {
		t.Fatalf("after a wrong password: %+v", p)
	}
	r.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: "good"})
	second := nextPrompt(t, prompts)
	select {
	case <-second.done:
	case <-time.After(3 * time.Second):
		t.Error("the next helper step's prompt wasn't answered from the kept password while the first answer was going out")
	}
	close(release)
	third := nextPrompt(t, prompts)
	for { // nothing more is asked
		ev, _ := r.next()
		if p, ok := ev.(engine.NeedsPassword); ok {
			t.Fatalf("asked again: %+v", p)
		}
		if f, ok := ev.(engine.Finished); ok {
			if f.Errs != 0 || f.Refused != 0 {
				t.Fatalf("Finished = %+v", f)
			}
			break
		}
	}
	if got := first.got(); len(got) != 1 || got[0] != "good" {
		t.Errorf("the first prompt got %q: su must refuse the wrong one before polkit sees it", got)
	}
	for _, p := range []*fakePrompt{second, third} {
		if got := p.got(); len(got) != 1 || got[0] != "good" {
			t.Errorf("a later prompt got %q, want the kept password", got)
		}
	}
	if !r.d.keep.Wiped() {
		t.Error("not wiped at the run's end")
	}
}

// A kept password polkit refuses is dropped: never sent to polkit again.
// And an agent prompt while no helper step runs is cancelled.
func TestKeepPolkitRefusesIt(t *testing.T) {
	suShim(t)
	agent, prompts := make(chan AgentPrompt), make(chan *fakePrompt, 3)
	h := steps.Step{ID: "h1", Title: "Root…", LogLabel: "Root", Verb: steps.VerbPaccache, Argv: []string{"false"}, NeedsPriv: true}
	r := newRig(t, engine.Options{Auth: engine.AuthPolkit},
		Deps{Polkit: true, Agent: agent, KeepPassword: true, Helper: agentHelper(agent, prompts, "other")}, action("pk", h))
	idle := newFakePrompt("org.archmaid.helper.full-cleanup", "good")
	select {
	case agent <- idle:
	case <-time.After(5 * time.Second):
		t.Fatal("the Driver took no agent prompt")
	}
	select {
	case <-idle.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("a prompt with no helper step running wasn't cancelled")
	}
	r.d.keep.Keep([]byte("good")) // as if an earlier prompt kept it
	r.d.Send(engine.Start{Action: "pk"})
	p := nextPrompt(t, prompts)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the kept password wasn't tried")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !r.d.keep.Wiped() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !r.d.keep.Wiped() {
		t.Fatal("a password polkit refused is still kept")
	}
	if got := p.got(); len(got) != 1 {
		t.Fatalf("polkit got %d answers", len(got))
	}
}
