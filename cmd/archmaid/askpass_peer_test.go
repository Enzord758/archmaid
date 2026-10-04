package main

import (
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Who may have the password. Anything that inherited SUDO_ASKPASS can connect
// to the socket — under topgrade that includes every AUR build script — so a
// request is answered only while a sudo step runs, and only when it comes from
// archmaid's own helper, run by sudo, inside that step's process tree. The
// /proc lookups go through the readProc seam, so these tests stand in a fake
// process table: wrong uid, wrong binary, a parent that isn't sudo, a chain
// that never reaches the step, reused pids, an idle socket.

// Fake pids sit above the kernel's PID_MAX_LIMIT (2^22), so none can name a
// real process (os.FindProcess on one gives a handle that signals nothing).
const (
	fakeStepPid   = 5_000_100 // the running step: topgrade
	fakeCallerPid = 5_000_150 // yay, which runs sudo
	fakeSudoPid   = 5_000_200
	fakeHelperPid = 5_000_300 // archmaid --askpass, exec'd by the wrapper
)

// fakeUID is our uid in the fake process table. It is fixed, never the real
// uid: the suite must give the same answers as root (CI's container) and as a
// user, and "a user process" must never coincide with root's uid 0.
const fakeUID = 1000

// fakeProcs replaces readProc with a lookup in procs for the test, and pins
// ourUID to fakeUID.

func fakeProcs(t *testing.T, procs map[int]procInfo) {
	t.Helper()
	old, oldUID := readProc, ourUID
	ourUID = func() int { return fakeUID }
	readProc = func(pid int) (procInfo, error) {
		if p, ok := procs[pid]; ok {
			return p, nil
		}
		return procInfo{}, os.ErrNotExist
	}
	t.Cleanup(func() { readProc, ourUID = old, oldUID })
}

// askpassTree is the happy path, topgrade → yay → sudo → archmaid --askpass,
// as a user sees it: sudo's exe link is unreadable (setuid root), but it runs
// with euid 0 under the name "sudo".
func askpassTree(t *testing.T) map[int]procInfo {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	uid := fakeUID
	return map[int]procInfo{
		fakeStepPid:   {ppid: os.Getpid(), start: 100, comm: "topgrade", euid: uid, exe: "/usr/bin/topgrade"},
		fakeCallerPid: {ppid: fakeStepPid, start: 200, comm: "yay", euid: uid, exe: "/usr/bin/yay"},
		fakeSudoPid:   {ppid: fakeCallerPid, start: 300, comm: "sudo", euid: 0},
		fakeHelperPid: {ppid: fakeSudoPid, start: 400, comm: "askpass.sh", euid: uid, exe: self},
	}
}

// helperPeer is the helper as the server records it at accept.
func helperPeer() askpassPeer {
	return askpassPeer{pid: fakeHelperPid, uid: fakeUID, start: 400}
}

// TestVerifyAskpassPeer: the happy path names sudo's caller; every way a
// request can fail to be ours is refused.
func TestVerifyAskpassPeer(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		fakeProcs(t, askpassTree(t))
		from, err := verifyAskpassPeer(helperPeer(), fakeStepPid, 100)
		if err != nil {
			t.Fatalf("refused the step's own sudo: %v", err)
		}
		if from.callerExe != "/usr/bin/yay" || from.callerPid != fakeCallerPid {
			t.Fatalf("caller = %q (pid %d), want /usr/bin/yay (pid %d)", from.callerExe, from.callerPid, fakeCallerPid)
		}
	})

	t.Run("sudo is the step itself", func(t *testing.T) {
		// A plain `sudo -A …` step: the step's child is sudo.
		fakeProcs(t, askpassTree(t))
		if _, err := verifyAskpassPeer(helperPeer(), fakeSudoPid, 300); err != nil {
			t.Fatalf("refused a direct sudo step: %v", err)
		}
	})

	t.Run("readable sudo exe", func(t *testing.T) {
		// As root the exe link is readable; then it must be sudo's binary.
		tree := askpassTree(t)
		sudo := tree[fakeSudoPid]
		sudo.exe = sudoPath
		tree[fakeSudoPid] = sudo
		fakeProcs(t, tree)
		if _, err := verifyAskpassPeer(helperPeer(), fakeStepPid, 100); err != nil {
			t.Fatalf("refused a sudo whose exe reads %s: %v", sudoPath, err)
		}
	})

	refusals := []struct {
		name      string
		edit      func(tree map[int]procInfo, peer *askpassPeer)
		stepPid   int
		stepStart uint64
		want      string // in the error
	}{
		{name: "wrong uid", want: "not ours",
			edit: func(_ map[int]procInfo, peer *askpassPeer) { peer.uid = fakeUID + 1 }},
		{name: "helper gone", want: "gone",
			edit: func(tree map[int]procInfo, _ *askpassPeer) { delete(tree, fakeHelperPid) }},
		{name: "helper pid reused since accept", want: "reused",
			edit: func(_ map[int]procInfo, peer *askpassPeer) { peer.start = 399 }},
		{name: "not archmaid", want: "/usr/bin/python3",
			edit: setProc(fakeHelperPid, func(p *procInfo) { p.exe = "/usr/bin/python3" })},
		{name: "helper exe unreadable", want: "unknown program",
			edit: setProc(fakeHelperPid, func(p *procInfo) { p.exe = "" })},
		{name: "user process named sudo", want: "not started by sudo",
			edit: setProc(fakeSudoPid, func(p *procInfo) { p.euid = fakeUID })},
		{name: "root process not named sudo", want: "not started by sudo",
			edit: setProc(fakeSudoPid, func(p *procInfo) { p.comm = "pkexec" })},
		{name: "readable exe that isn't sudo", want: "not started by sudo",
			edit: setProc(fakeSudoPid, func(p *procInfo) { p.exe = "/home/u/bin/sudo" })},
		{name: "parent gone", want: "not started by sudo",
			edit: func(tree map[int]procInfo, _ *askpassPeer) { delete(tree, fakeSudoPid) }},
		{name: "chain misses the step", stepPid: 4242, want: "not started by the running step"},
		{name: "chain broken", want: "not started by the running step",
			edit: func(tree map[int]procInfo, _ *askpassPeer) { delete(tree, fakeCallerPid) }},
		{name: "step pid reused", stepStart: 99, want: "step's pid"},
		{name: "ancestor pid reused", want: "reused",
			// a parent can't have started after its child
			edit: setProc(fakeCallerPid, func(p *procInfo) { p.start = 350 })},
		{name: "cycle", want: "not started by the running step",
			edit: setProc(fakeCallerPid, func(p *procInfo) { p.ppid = fakeSudoPid; p.start = 300 })},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			tree, peer := askpassTree(t), helperPeer()
			if c.edit != nil {
				c.edit(tree, &peer)
			}
			fakeProcs(t, tree)
			stepPid, stepStart := fakeStepPid, uint64(100)
			if c.stepPid != 0 {
				stepPid = c.stepPid
			}
			if c.stepStart != 0 {
				stepStart = c.stepStart
			}
			_, err := verifyAskpassPeer(peer, stepPid, stepStart)
			if err == nil {
				t.Fatal("request accepted, want it refused")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %q, want it to mention %q", err, c.want)
			}
		})
	}
}

// setProc returns an edit that changes pid's entry in the fake tree.
func setProc(pid int, change func(*procInfo)) func(map[int]procInfo, *askpassPeer) {
	return func(tree map[int]procInfo, _ *askpassPeer) {
		p := tree[pid]
		change(&p)
		tree[pid] = p
	}
}

// TestReadProcFS reads the real /proc: our own entry matches what the runtime
// says, a child started after us has us as parent and a later start time, a
// root process's exe link is unreadable to a user (which is why isSudo can't
// rely on it), and a missing pid is an error.
func TestReadProcFS(t *testing.T) {
	self, err := readProcFS(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	if self.ppid != os.Getppid() || self.euid != os.Geteuid() || self.exe != exe || self.start == 0 || self.comm == "" {
		t.Fatalf("self = %+v; want ppid %d, euid %d, exe %s and a start time", self, os.Getppid(), os.Geteuid(), exe)
	}

	child := exec.Command("sleep", "10")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Wait()
	defer child.Process.Kill()
	kid, err := readProcFS(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if kid.ppid != os.Getpid() || kid.start < self.start {
		t.Fatalf("child = %+v; want ppid %d and a start time ≥ %d", kid, os.Getpid(), self.start)
	}

	if os.Geteuid() != 0 {
		init, err := readProcFS(1)
		if err != nil {
			t.Fatal(err)
		}
		if init.euid != 0 || init.exe != "" {
			t.Fatalf("pid 1 = %+v; want euid 0 and an unreadable exe", init)
		}
	}

	if _, err := readProcFS(fakeStepPid); err == nil {
		t.Fatal("no error for a pid that can't exist")
	}
}

// ── the model's side ─────────────────────────────────────────────────────────

// sudoStepRunning makes m's current step a running sudo step whose child is
// fakeStepPid (as launch records it), with the happy-path tree behind
// readProc. A model without a queue gets a one-step one.
func sudoStepRunning(t *testing.T, m model) model {
	t.Helper()
	fakeProcs(t, askpassTree(t))
	if len(m.queue) == 0 {
		m.queue = []stepState{{step: step{title: "Updating system with topgrade…", command: "topgrade"}}}
		m.current = 0
	}
	m.queue[m.current].step.needsSudo = true
	m.queue[m.current].status = statusRunning
	p, err := os.FindProcess(fakeStepPid)
	if err != nil {
		t.Fatal(err)
	}
	m.proc, m.procAt = p, 100
	return m
}

// helperAsks is the helper's request, as the server delivers it.
func helperAsks(conn net.Conn) askpassMsg {
	return askpassMsg{prompt: "[sudo] password for u: ", conn: conn, peer: helperPeer()}
}

// pipeConn returns the model's end of an in-memory connection and the
// helper's end, both closed on cleanup.
func pipeConn(t *testing.T) (model, helper net.Conn) {
	t.Helper()
	model, helper = net.Pipe()
	t.Cleanup(func() { model.Close(); helper.Close() })
	helper.SetDeadline(time.Now().Add(askpassTimeout))
	return model, helper
}

// requireClosedUnanswered fails unless the helper's end reads EOF: the model
// hung up without writing anything.
func requireClosedUnanswered(t *testing.T, helper net.Conn) {
	t.Helper()
	if n, err := helper.Read(make([]byte, 64)); n != 0 || err != io.EOF {
		t.Fatalf("helper read n=%d err=%v, want the model to hang up unanswered", n, err)
	}
}

// TestAskpassIdleSocketHungUp: with no sudo step running — on the menu, during
// a step that doesn't use sudo, after the step finished — a request is hung up
// on at once, with no popup.
func TestAskpassIdleSocketHungUp(t *testing.T) {
	running := func(t *testing.T) model {
		m := sudoStepRunning(t, fixedModel())
		m.screen = screenRunning
		return m
	}
	cases := map[string]func(t *testing.T) model{
		"menu": func(t *testing.T) model {
			fakeProcs(t, askpassTree(t))
			return fixedModel()
		},
		"step without sudo": func(t *testing.T) model {
			m := running(t)
			m.queue[0].step.needsSudo = false
			return m
		},
		"step finished": func(t *testing.T) model {
			m := running(t)
			m.queue[0].status = statusOK
			return m
		},
		"aborted": func(t *testing.T) model {
			m := running(t)
			m.proc = nil
			return m
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			m := build(t)
			before := m.screen
			conn, helper := pipeConn(t)
			m = update(m, helperAsks(conn))
			requireClosedUnanswered(t, helper)
			if m.screen != before || m.askpassConn != nil {
				t.Fatalf("screen %d → %d, conn set=%v; want no popup", before, m.screen, m.askpassConn != nil)
			}
		})
	}
}

// TestAskpassRefusedRequestIsNoted: a request from outside the step's tree
// during a sudo step is hung up on, gets no popup, and leaves a sanitized line
// in the output pane (and so in the step's log) saying what was refused.
func TestAskpassRefusedRequestIsNoted(t *testing.T) {
	m := sudoStepRunning(t, fixedModel())
	m.screen = screenRunning
	tree := askpassTree(t)
	helper := tree[fakeHelperPid]
	helper.exe = "/tmp/evil\x1b]52;c;Zm9v\x07"
	tree[fakeHelperPid] = helper
	fakeProcs(t, tree)

	conn, peer := pipeConn(t)
	m = update(m, helperAsks(conn))
	requireClosedUnanswered(t, peer)
	if m.screen != screenRunning || m.askpassConn != nil {
		t.Fatalf("screen = %d, conn set=%v; want no popup", m.screen, m.askpassConn != nil)
	}
	out := m.buf.String()
	if !strings.Contains(out, "refused a sudo password request") || !strings.Contains(out, "/tmp/evil") {
		t.Fatalf("output pane doesn't note the refusal:\n%q", out)
	}
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x07') {
		t.Fatalf("refusal note carries raw escapes: %q", out)
	}
}

// TestAskpassSecondRequestRefused: while one popup is open, another request is
// hung up on; the open one keeps its connection.
func TestAskpassSecondRequestRefused(t *testing.T) {
	m := sudoStepRunning(t, fixedModel())
	m.screen = screenRunning
	first, _ := pipeConn(t)
	m = update(m, helperAsks(first))
	if m.askpassConn != first {
		t.Fatal("first request didn't open the popup")
	}

	second, helper := pipeConn(t)
	m = update(m, helperAsks(second))
	requireClosedUnanswered(t, helper)
	if m.askpassConn != first || m.screen != screenPassword {
		t.Fatal("second request displaced the open one")
	}
}

// TestAskpassPopupShowsRequester: the popup names the running step and sudo's
// real caller with its pid, and shows sudo's prompt only quoted and stripped of
// escapes — whoever runs sudo picks it with -p.
func TestAskpassPopupShowsRequester(t *testing.T) {
	m := sudoStepRunning(t, sizedModel())
	m.screen = screenRunning
	conn, _ := pipeConn(t)
	req := helperAsks(conn)
	req.prompt = "\x1b]0;pwned\x07[sudo] password\nfor u: "
	m = update(m, req)
	if m.screen != screenPassword {
		t.Fatalf("screen = %d, want the password popup", m.screen)
	}

	view := m.viewPassword()
	for _, want := range []string{
		"Updating system with topgrade…",
		"/usr/bin/yay (pid 5000150)",
		`"[sudo] password for u:"`,
	} {
		if !strings.Contains(view, want) {
			t.Errorf("popup missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "pwned") || strings.ContainsRune(view, '\x07') {
		t.Fatalf("popup shows the prompt's escape sequence:\n%q", view)
	}
}

// TestAskpassPromptTextClipped: a long prompt is cut to one short line.
func TestAskpassPromptTextClipped(t *testing.T) {
	got := askpassPromptText(strings.Repeat("ab ", 40))
	if n := len([]rune(got)); n != 48 || !strings.HasSuffix(got, "…") {
		t.Fatalf("prompt = %q (%d runes), want 48 ending in …", got, n)
	}
}

// TestAskpassServerReportsPeer: over a real socket the server records the
// connecting process — here the test itself — with its uid and start time.
func TestAskpassServerReportsPeer(t *testing.T) {
	askpassEnv(t)
	s := startAskpassServer()
	defer s.close()
	conn, err := net.Dial("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("Password:\n"))
	req := recv(t, s.reqs, "the request")
	defer req.conn.Close()

	self, err := readProcFS(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if want := (askpassPeer{pid: os.Getpid(), uid: os.Getuid(), start: self.start}); req.peer != want {
		t.Fatalf("peer = %+v, want %+v", req.peer, want)
	}
}
