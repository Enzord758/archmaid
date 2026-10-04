package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/proc"
)

// The TUI's side of askpass: the session's server wired up for runStep and
// unwired on stop, the env reaching a real step child, and the model answering
// a request from the popup — su first, the password written only once it's
// right — or cancelling it. The server, the client, the peer check and the
// Request itself are internal/privilege's, and tested there.

// askpassTimeout bounds every wait here, so a broken round trip fails instead
// of hanging the suite.
const askpassTimeout = 3 * time.Second

// recv waits for one value on ch, failing the test after askpassTimeout.
func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(askpassTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

// TestAskpassServerWiring: startAskpassServer starts a server under
// XDG_RUNTIME_DIR and wires its wrapper up for runStep (sudo steps get -A),
// without ever setting SUDO_ASKPASS in archmaid's own environment;
// stopAskpassServer removes it and unwires the wrapper. Without
// XDG_RUNTIME_DIR askpass stays unwired, with no /tmp fallback, and stopping
// a nil server is safe.
func TestAskpassServerWiring(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	withAskpassWrapper(t, "")

	s := startAskpassServer()
	if s == nil {
		t.Fatal("no server with a usable XDG_RUNTIME_DIR")
	}
	if askpassWrapper == "" || askpassWrapper != s.Wrapper() || filepath.Dir(filepath.Dir(askpassWrapper)) != runtimeDir {
		t.Fatalf("askpassWrapper = %q, want the server's wrapper under %s", askpassWrapper, runtimeDir)
	}
	if got := os.Getenv("SUDO_ASKPASS"); got != "/from/the/users/shell" {
		t.Fatalf("SUDO_ASKPASS = %q: the server must not touch archmaid's own environment", got)
	}
	if spec := stepSpec(step{command: "sudo", args: []string{"true"}, needsSudo: true}); spec.Args[0] != "-A" {
		t.Fatalf("sudo step args = %v with askpass wired, want -A first", spec.Args)
	}
	stopAskpassServer(s)
	if askpassWrapper != "" {
		t.Fatalf("askpassWrapper = %q after stop, want it unwired", askpassWrapper)
	}
	if _, err := os.Stat(filepath.Dir(s.Wrapper())); !os.IsNotExist(err) {
		t.Fatalf("askpass dir still exists after stop (err %v)", err)
	}

	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", tmp)
	if s := startAskpassServer(); s != nil || askpassWrapper != "" {
		t.Fatalf("server %v, askpassWrapper %q without XDG_RUNTIME_DIR; want askpass unwired", s, askpassWrapper)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("left %d entries in TMPDIR", len(entries))
	}
	stopAskpassServer(nil)
}

// TestStepChildSeesAskpassEnv: the env reaches the real child — a shimmed
// topgrade (a needsSudo step) sees the wrapper, a plain step sees nothing.
func TestStepChildSeesAskpassEnv(t *testing.T) {
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	old := askpassWrapper
	askpassWrapper = "/wrapper"
	t.Cleanup(func() { askpassWrapper = old })

	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '[%s]\\n' \"$SUDO_ASKPASS\"\n"
	for _, name := range []string{"topgrade", "yay"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, c := range []struct {
		s    step
		want string
	}{
		{step{command: "topgrade", needsSudo: true}, "[/wrapper]"},
		{step{command: "yay"}, "[]"},
	} {
		ch := make(chan proc.Msg, 8)
		runStep(c.s, ch, nil)
		lines, err := drainStep(ch)
		if err != nil || len(lines) != 1 || lines[0] != c.want {
			t.Fatalf("%s saw SUDO_ASKPASS %v (err %v), want %s", c.s.command, lines, err, c.want)
		}
	}
}

// askpassOverRunning opens an askpass popup over a running queue, with the
// server end of an in-memory connection standing in for the helper.
func askpassOverRunning(t *testing.T) (model, net.Conn) {
	t.Helper()
	client, server := pipeConn(t)

	m := sudoStepRunning(t, fixedModel())
	m.screen = screenRunning
	req := helperAsks(client)
	m = update(m, req)
	if m.screen != screenPassword || m.bg != screenRunning || m.askpassReq != req.req {
		t.Fatalf("screen=%d bg=%d conn set=%v; want the prompt over screenRunning",
			m.screen, m.bg, m.askpassReq != nil)
	}
	return m, server
}

// submitAskpassKey types pw into the open askpass popup and presses enter. The
// input locks while su checks the password; the returned cmd is that check.
func submitAskpassKey(t *testing.T, m model, pw string) (model, tea.Cmd) {
	t.Helper()
	m = update(m, key(pw))
	next, cmd := m.Update(key("enter"))
	m = next.(model)
	if !m.authInFlight || cmd == nil {
		t.Fatalf("enter: authInFlight=%v cmd=%v, want the su check in flight", m.authInFlight, cmd != nil)
	}
	if m.password.Value() != "" {
		t.Fatal("password still held in the input after submit")
	}
	if got := update(m, key("x")); got.password.Value() != "" || got.screen != screenPassword {
		t.Fatal("keys reached the popup while su was checking")
	}
	return m, cmd
}

// requireSuOnly checks the su shim ran as `su -s /usr/bin/true <user>` (no -c:
// that runs the user's shell startup files) and that sudo never ran.
func requireSuOnly(t *testing.T, dir string) {
	t.Helper()
	argv, err := os.ReadFile(filepath.Join(dir, "su.called"))
	if err != nil {
		t.Fatal("su was not invoked")
	}
	self, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(argv)), "-s /usr/bin/true "+self.Username; got != want {
		t.Fatalf("su argv = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "sudo.called")); err == nil {
		t.Fatal("sudo ran: a password must pass su before sudo sees it")
	}
}

// TestAskpassRightPasswordReachesConn: su accepts → the password plus newline
// goes to the connection, which is closed; the popup closes back to the
// running screen and the attempt count resets.
func TestAskpassRightPasswordReachesConn(t *testing.T) {
	dir := shimAuthPair(t, 0)
	m, server := askpassOverRunning(t)
	m.pwAttempts = 1
	m, check := submitAskpassKey(t, m, "secret")

	// net.Pipe is synchronous: the write blocks until it's read, so the
	// reader has to be running before the check does.
	got := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(server) // up to the close after the write
		got <- string(b)
	}()
	m = update(m, check())

	if pw := recv(t, got, "the password"); pw != "secret\n" {
		t.Fatalf("helper received %q, want %q", pw, "secret\n")
	}
	requireSuOnly(t, dir)
	if m.authInFlight || m.askpassReq != nil || m.screen != screenRunning || m.pwAttempts != 0 {
		t.Fatalf("authInFlight=%v conn set=%v screen=%d attempts=%d; want the popup closed over screenRunning, count reset",
			m.authInFlight, m.askpassReq != nil, m.screen, m.pwAttempts)
	}
}

// TestAskpassWrongPasswordNeverReachesConn: su rejects → nothing is written to
// sudo's connection, which stays open; the popup stays up, unlocked, with the
// attempts left.
func TestAskpassWrongPasswordNeverReachesConn(t *testing.T) {
	dir := shimAuthPair(t, 1)
	m, server := askpassOverRunning(t)
	m, check := submitAskpassKey(t, m, "wrong")
	m = update(m, check())

	requireSuOnly(t, dir)
	server.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, err := server.Read(make([]byte, 64)); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("helper read n=%d err=%v; want nothing written and the connection still open", n, err)
	}
	if m.authInFlight || m.screen != screenPassword || m.askpassReq == nil {
		t.Fatalf("authInFlight=%v screen=%d conn set=%v; want the popup still up, unlocked",
			m.authInFlight, m.screen, m.askpassReq != nil)
	}
	if m.pwAttempts != 1 || !strings.Contains(m.pwErr, "2 attempts left") {
		t.Fatalf("attempts=%d pwErr=%q, want 1 and 2 attempts left", m.pwAttempts, m.pwErr)
	}
}

// TestAskpassLockoutNoticeShown: su's lockout notice replaces the generic
// error.
func TestAskpassLockoutNoticeShown(t *testing.T) {
	m, _ := askpassOverRunning(t)
	m.authInFlight = true
	m = update(m, askpassResultMsg{req: m.askpassReq, err: errors.New("denied"),
		detail: "The account is locked due to 3 failed logins."})
	if !strings.Contains(m.pwErr, "locked") {
		t.Fatalf("pwErr = %q, want the lockout notice", m.pwErr)
	}
}

// TestAskpassAttemptsCounted: wrong passwords in the popup count toward
// maxSudoAttempts. At the limit the request is cancelled (closed unanswered,
// so this sudo fails) and the queue's later sudo steps are skipped, as after
// a failed prime.
func TestAskpassAttemptsCounted(t *testing.T) {
	shimAuthPair(t, 1)
	m, server := askpassOverRunning(t)
	for i := 1; i <= maxSudoAttempts; i++ {
		var check tea.Cmd
		m, check = submitAskpassKey(t, m, "wrong")
		if i < maxSudoAttempts {
			m = update(m, check())
			if m.pwAttempts != i || m.askpassReq == nil {
				t.Fatalf("after %d wrong: attempts=%d conn set=%v", i, m.pwAttempts, m.askpassReq != nil)
			}
			continue
		}
		got := make(chan error, 1)
		go func() {
			n, err := server.Read(make([]byte, 64))
			if n != 0 {
				err = fmt.Errorf("read %d bytes", n)
			}
			got <- err
		}()
		m = update(m, check())
		if err := recv(t, got, "the cancel"); err != io.EOF {
			t.Fatalf("helper read %v, want EOF (closed unanswered)", err)
		}
	}
	if m.askpassReq != nil || m.screen != screenRunning || !m.sudoUnavailable {
		t.Fatalf("conn set=%v screen=%d sudoUnavailable=%v; want the request cancelled, back on screenRunning, sudo steps skipped",
			m.askpassReq != nil, m.screen, m.sudoUnavailable)
	}
}

// TestAskpassLineBreakRejected: a password with a line break never reaches su
// or the connection (sudo reads one line, so it would get a different password
// than su checked), and doesn't count as an attempt.
func TestAskpassLineBreakRejected(t *testing.T) {
	dir := shimAuthPair(t, 0)
	m, server := askpassOverRunning(t)
	for _, pw := range []string{"good\nbad", "good\rbad"} {
		next, cmd := m.submitAskpass(pw)
		m = next.(model)
		if cmd != nil || m.authInFlight {
			t.Fatalf("%q: a check was started", pw)
		}
		if !strings.Contains(m.pwErr, "line break") || m.pwAttempts != 0 || m.askpassReq == nil {
			t.Fatalf("%q: pwErr=%q attempts=%d conn set=%v; want it refused, uncounted, popup up",
				pw, m.pwErr, m.pwAttempts, m.askpassReq != nil)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "su.called")); err == nil {
		t.Fatal("su ran for a password with a line break")
	}
	server.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, err := server.Read(make([]byte, 64)); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("helper read n=%d err=%v; want nothing written", n, err)
	}
}

// TestAskpassStaleResultIgnored: a check result for a request that is no
// longer open changes nothing.
func TestAskpassStaleResultIgnored(t *testing.T) {
	m, _ := askpassOverRunning(t)
	otherConn, _ := pipeConn(t)
	got := update(m, askpassResultMsg{req: helperAsks(otherConn).req, err: errors.New("nope")})
	if got.pwAttempts != 0 || got.askpassReq != m.askpassReq || got.screen != screenPassword {
		t.Fatal("a stale result changed the open popup")
	}
}

// TestAskpassCancelClosesConnUnanswered: esc closes the connection without
// writing anything (sudo counts it as a failed attempt) and returns to the
// screen underneath.
func TestAskpassCancelClosesConnUnanswered(t *testing.T) {
	m, server := askpassOverRunning(t)
	m = update(m, key("secret"))

	type readResult struct {
		n   int
		err error
	}
	got := make(chan readResult, 1)
	go func() {
		n, err := server.Read(make([]byte, 64))
		got <- readResult{n, err}
	}()
	m = update(m, key("esc"))

	if r := recv(t, got, "the close"); r.n != 0 || r.err != io.EOF {
		t.Fatalf("helper read n=%d err=%v, want 0 bytes and io.EOF", r.n, r.err)
	}
	if m.askpassReq != nil {
		t.Fatal("askpassReq not cleared after cancel")
	}
	if m.screen != screenRunning {
		t.Fatalf("screen = %d, want screenRunning", m.screen)
	}
	if m.password.Value() != "" {
		t.Fatal("typed password left in the input after cancel")
	}
}
