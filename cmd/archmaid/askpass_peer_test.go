package main

import (
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
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

func fakeProcs(t *testing.T, procs map[int]proc.Info) {
	t.Helper()
	old, oldUID := readProc, ourUID
	ourUID = func() int { return fakeUID }
	readProc = func(pid int) (proc.Info, error) {
		if p, ok := procs[pid]; ok {
			return p, nil
		}
		return proc.Info{}, os.ErrNotExist
	}
	t.Cleanup(func() { readProc, ourUID = old, oldUID })
}

// askpassTree is the happy path, topgrade → yay → sudo → archmaid --askpass,
// as a user sees it: sudo's exe link is unreadable (setuid root), but it runs
// with euid 0 under the name "sudo".
func askpassTree(t *testing.T) map[int]proc.Info {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	uid := fakeUID
	return map[int]proc.Info{
		fakeStepPid:   {PPid: os.Getpid(), Start: 100, Comm: "topgrade", EUID: uid, Exe: "/usr/bin/topgrade"},
		fakeCallerPid: {PPid: fakeStepPid, Start: 200, Comm: "yay", EUID: uid, Exe: "/usr/bin/yay"},
		fakeSudoPid:   {PPid: fakeCallerPid, Start: 300, Comm: "sudo", EUID: 0},
		fakeHelperPid: {PPid: fakeSudoPid, Start: 400, Comm: "askpass.sh", EUID: uid, Exe: self},
	}
}

// helperPeer is the helper as the server records it at accept.
func helperPeer() privilege.Peer {
	return privilege.Peer{Pid: fakeHelperPid, UID: fakeUID, Start: 400}
}

// ── the model's side ─────────────────────────────────────────────────────────

// sudoStepRunning makes m's current step a running sudo step whose child is
// fakeStepPid (as launch records it), with the happy-path tree behind
// readProc. A model without a queue gets a one-step one.
func sudoStepRunning(t *testing.T, m model) model {
	t.Helper()
	fakeProcs(t, askpassTree(t))
	if len(m.queue) == 0 {
		m.queue = []stepState{{step: steps.Step{Title: "Updating system with topgrade…", Argv: []string{"topgrade"}}}}
		m.current = 0
	}
	m.queue[m.current].step.NeedsPriv = true
	m.queue[m.current].status = steps.Running
	m.proc = &proc.Child{Pid: fakeStepPid, Start: 100}
	return m
}

// helperAsks is the helper's request, as the server delivers it.
func helperAsks(conn net.Conn) askpassMsg {
	return askpassMsg{req: privilege.NewTestRequest(conn, "[sudo] password for u: ", helperPeer())}
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
			m.queue[0].step.NeedsPriv = false
			return m
		},
		"step finished": func(t *testing.T) model {
			m := running(t)
			m.queue[0].status = steps.OK
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
			if m.screen != before || m.askpassReq != nil {
				t.Fatalf("screen %d → %d, conn set=%v; want no popup", before, m.screen, m.askpassReq != nil)
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
	helper.Exe = "/tmp/evil\x1b]52;c;Zm9v\x07"
	tree[fakeHelperPid] = helper
	fakeProcs(t, tree)

	conn, peer := pipeConn(t)
	m = update(m, helperAsks(conn))
	requireClosedUnanswered(t, peer)
	if m.screen != screenRunning || m.askpassReq != nil {
		t.Fatalf("screen = %d, conn set=%v; want no popup", m.screen, m.askpassReq != nil)
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
	firstReq := helperAsks(first)
	m = update(m, firstReq)
	if m.askpassReq != firstReq.req {
		t.Fatal("first request didn't open the popup")
	}

	second, helper := pipeConn(t)
	m = update(m, helperAsks(second))
	requireClosedUnanswered(t, helper)
	if m.askpassReq != firstReq.req || m.screen != screenPassword {
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
	req.req.Prompt = "\x1b]0;pwned\x07[sudo] password\nfor u: "
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
