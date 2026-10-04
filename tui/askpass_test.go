package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The SUDO_ASKPASS round trip, over a real unix socket: the helper side
// (runAskpass, what sudo execs), the TUI side (startAskpassServer + close), the
// two joined through the real wrapper script, and the model writing the typed
// password to the connection (or closing it unanswered on cancel). The wrapper
// execs this test binary, which TestMain dispatches into runAskpass exactly
// like main() does.

// askpassTimeout bounds every wait here, so a broken round trip fails instead
// of hanging the suite.
const askpassTimeout = 3 * time.Second

// fakeAskpassTUI listens on a fresh socket and serves one connection the way
// the TUI does: read the prompt line, then answer "pw\n" — or, if answer is
// false, close unanswered. The prompt it saw arrives on the returned channel.
func fakeAskpassTUI(t *testing.T, answer bool) (sock string, prompt <-chan string) {
	t.Helper()
	sock = filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(got)
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(askpassTimeout))
		line, _ := bufio.NewReader(conn).ReadString('\n')
		got <- line
		if answer {
			fmt.Fprint(conn, "pw\n")
		}
	}()
	return sock, got
}

// captureStdout runs fn with os.Stdout swapped for a pipe and returns what it
// printed. runAskpass prints the password with fmt.Print, i.e. to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	w.Close()
	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

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

// TestRunAskpass: the helper sends the prompt, prints the answer with its
// newline (sudo expects one) and exits 0; an unanswered close or an
// unreachable socket exits 1 and prints nothing.
func TestRunAskpass(t *testing.T) {
	t.Run("answered", func(t *testing.T) {
		sock, prompt := fakeAskpassTUI(t, true)
		var code int
		out := captureStdout(t, func() { code = runAskpass(sock, "[sudo] password for u: ") })
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if out != "pw\n" {
			t.Fatalf("stdout = %q, want %q", out, "pw\n")
		}
		if got := recv(t, prompt, "the prompt"); got != "[sudo] password for u: \n" {
			t.Fatalf("server saw %q, want the prompt line", got)
		}
	})

	t.Run("closed unanswered", func(t *testing.T) {
		sock, prompt := fakeAskpassTUI(t, false)
		var code int
		out := captureStdout(t, func() { code = runAskpass(sock, "Password:") })
		if code != 1 {
			t.Fatalf("exit = %d, want 1 (cancel)", code)
		}
		if out != "" {
			t.Fatalf("stdout = %q, want nothing on cancel", out)
		}
		recv(t, prompt, "the prompt")
	})

	t.Run("no socket", func(t *testing.T) {
		sock := filepath.Join(t.TempDir(), "missing.sock")
		var code int
		out := captureStdout(t, func() { code = runAskpass(sock, "Password:") })
		if code != 1 || out != "" {
			t.Fatalf("exit = %d, stdout = %q; want 1 and nothing", code, out)
		}
	})
}

// askpassEnv gives startAskpassServer a private runtime dir. SUDO_ASKPASS is
// set to a sentinel, registered with t.Setenv, so a test can prove the server
// leaves archmaid's own environment alone.
func askpassEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	return dir
}

// TestAskpassServerWiresWrapperAndSocket: the server makes a private 0700
// directory under XDG_RUNTIME_DIR holding its socket and a 0700 wrapper that
// execs us in --askpass mode on that socket; it records the wrapper for runStep
// in askpassWrapper but never sets SUDO_ASKPASS in our own environment; it
// delivers each prompt (newline trimmed) on reqs, and close() removes the
// directory and unwires askpassWrapper.
func TestAskpassServerWiresWrapperAndSocket(t *testing.T) {
	runtimeDir := askpassEnv(t)
	s := startAskpassServer()
	closed := false
	defer func() {
		if !closed {
			s.close()
		}
	}()

	if s.ln == nil {
		t.Fatal("server not listening")
	}
	if filepath.Dir(s.dir) != runtimeDir || filepath.Dir(s.sock) != s.dir || filepath.Dir(s.wrapper) != s.dir {
		t.Fatalf("dir %q / sock %q / wrapper %q: want sock and wrapper in a private dir under %q",
			s.dir, s.sock, s.wrapper, runtimeDir)
	}
	for path, want := range map[string]os.FileMode{s.dir: 0o700, s.wrapper: 0o700} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Fatalf("%s mode = %v, want %v", path, fi.Mode().Perm(), want)
		}
	}
	script, _ := os.ReadFile(s.wrapper)
	if !strings.Contains(string(script), "--askpass") || !strings.Contains(string(script), shellQuote(s.sock)) {
		t.Fatalf("wrapper doesn't exec --askpass on the socket:\n%s", script)
	}
	if askpassWrapper != s.wrapper {
		t.Fatalf("askpassWrapper = %q, want %q", askpassWrapper, s.wrapper)
	}
	if got := os.Getenv("SUDO_ASKPASS"); got != "/from/the/users/shell" {
		t.Fatalf("SUDO_ASKPASS = %q: the server must not touch archmaid's own environment", got)
	}

	conn, err := net.Dial("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "Password: \r\n")
	req := recv(t, s.reqs, "the askpass request")
	defer req.conn.Close()
	// Only the line ending is trimmed — sudo's prompt keeps its trailing space.
	if req.prompt != "Password: " {
		t.Fatalf("prompt = %q, want %q", req.prompt, "Password: ")
	}

	s.close()
	closed = true
	if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
		t.Fatalf("%s still exists after close (err %v)", s.dir, err)
	}
	if askpassWrapper != "" {
		t.Fatalf("askpassWrapper = %q after close, want it unwired", askpassWrapper)
	}
}

// TestAskpassServerNeedsRuntimeDir: with no XDG_RUNTIME_DIR askpass stays
// unwired — no fallback to the shared /tmp.
func TestAskpassServerNeedsRuntimeDir(t *testing.T) {
	askpassEnv(t)
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", tmp)

	s := startAskpassServer()
	defer s.close()
	if s.ln != nil || s.dir != "" || askpassWrapper != "" {
		t.Fatalf("listening=%v dir=%q askpassWrapper=%q, want askpass unwired", s.ln != nil, s.dir, askpassWrapper)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("server left %d entries in TMPDIR", len(entries))
	}
}

// TestAskpassServerBestEffort: when the directory or the socket can't be
// created the server is left unwired (no listener, askpassWrapper empty), and
// close() is still safe — as it is on a zero server.
func TestAskpassServerBestEffort(t *testing.T) {
	t.Run("zero server", func(t *testing.T) {
		(&askpassServer{}).close()
	})

	t.Run("runtime dir missing", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", filepath.Join(askpassEnv(t), "gone"))
		s := startAskpassServer()
		if s.ln != nil || s.dir != "" || askpassWrapper != "" {
			t.Fatalf("listening=%v dir=%q askpassWrapper=%q, want askpass unwired", s.ln != nil, s.dir, askpassWrapper)
		}
		s.close()
	})

	t.Run("socket path too long", func(t *testing.T) {
		// sun_path is 108 bytes on Linux; Listen must fail.
		long := filepath.Join(askpassEnv(t), strings.Repeat("d", 100))
		if err := os.Mkdir(long, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_RUNTIME_DIR", long)

		s := startAskpassServer()
		if len(s.sock) <= 108 {
			t.Fatalf("socket path only %d bytes; the test needs > 108", len(s.sock))
		}
		if s.ln != nil {
			t.Fatal("listener set despite an over-long socket path")
		}
		if askpassWrapper != "" {
			t.Fatalf("askpassWrapper = %q, want it left unwired", askpassWrapper)
		}
		s.close()
		if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
			t.Fatalf("private dir still exists after close (err %v)", err)
		}
	})
}

// TestAskpassWrapperQuoting: the wrapper survives paths a shell would
// otherwise expand or split — a quote, $(…), backquotes, a backslash, spaces.
// Go's %q would have left "$(…)" live inside double quotes. The script is run
// by the real /bin/sh against a stand-in "archmaid" that prints its argv.
func TestAskpassWrapperQuoting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "it's $(touch pwned) `x` \\ dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "arch maid")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "a'b\"c.sock")
	wrapper := filepath.Join(t.TempDir(), "askpass.sh")
	if err := os.WriteFile(wrapper, []byte(askpassScript(exe, sock)), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(wrapper, "[sudo] password for $USER: ")
	cmd.Dir = t.TempDir()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("wrapper failed: %v", err)
	}
	want := "--askpass\n" + sock + "\n[sudo] password for $USER: \n"
	if string(out) != want {
		t.Fatalf("argv =\n%q\nwant\n%q", out, want)
	}
	if _, err := os.Stat(filepath.Join(cmd.Dir, "pwned")); err == nil {
		t.Fatal("the wrapper ran a command substitution from the path")
	}
}

// TestAskpassStuckClientDoesNotBlock: a client that connects and never sends
// its prompt neither delays the next client nor stays connected past the read
// deadline.
func TestAskpassStuckClientDoesNotBlock(t *testing.T) {
	askpassEnv(t)
	old := askpassReadTimeout
	askpassReadTimeout = 300 * time.Millisecond
	s := startAskpassServer()
	askpassReadTimeout = old
	defer s.close()
	if s.ln == nil {
		t.Fatal("server not listening")
	}

	stuck, err := net.Dial("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer stuck.Close()

	next, err := net.Dial("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	fmt.Fprintln(next, "Password:")
	req := recv(t, s.reqs, "the second client's request")
	req.conn.Close()
	if req.prompt != "Password:" {
		t.Fatalf("prompt = %q, want %q", req.prompt, "Password:")
	}

	requireHangup(t, stuck)
}

// requireHangup fails unless the server closes conn within askpassTimeout
// without sending anything. A close with the client's bytes still unread
// arrives as a reset rather than EOF; either counts, a timeout doesn't.
func requireHangup(t *testing.T, conn net.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(askpassTimeout))
	n, err := conn.Read(make([]byte, 8))
	if ne, ok := err.(net.Error); n != 0 || err == nil || (ok && ne.Timeout()) {
		t.Fatalf("read n=%d err=%v, want the server to hang up", n, err)
	}
}

// TestAskpassOversizedPromptDropped: a client that sends more than
// maxAskpassPrompt without a newline is dropped, not delivered.
func TestAskpassOversizedPromptDropped(t *testing.T) {
	askpassEnv(t)
	s := startAskpassServer()
	defer s.close()

	conn, err := net.Dial("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(strings.Repeat("x", maxAskpassPrompt+10))); err != nil {
		t.Fatal(err)
	}
	requireHangup(t, conn)
	select {
	case req := <-s.reqs:
		req.conn.Close()
		t.Fatalf("oversized prompt was delivered (%d bytes)", len(req.prompt))
	default:
	}
}

// TestAskpassCloseDropsUndelivered: a request still waiting for the model when
// the server closes is hung up on rather than left blocked.
func TestAskpassCloseDropsUndelivered(t *testing.T) {
	askpassEnv(t)
	s := startAskpassServer()
	conn, err := net.Dial("unix", s.sock)
	if err != nil {
		s.close()
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, "Password:")
	s.close() // nobody ever reads s.reqs
	requireHangup(t, conn)
}

// TestStepEnvCarriesAskpassOnlyForSudoSteps: only needsSudo steps get
// SUDO_ASKPASS, pointing at our wrapper; every other step has it stripped, even
// when archmaid inherited one from the user's shell. Without a wired askpass no
// step gets one.
func TestStepEnvCarriesAskpassOnlyForSudoSteps(t *testing.T) {
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	old := askpassWrapper
	t.Cleanup(func() { askpassWrapper = old })

	askpassVals := func(env []string) []string {
		var vals []string
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "SUDO_ASKPASS="); ok {
				vals = append(vals, v)
			}
		}
		return vals
	}

	askpassWrapper = "/run/user/1000/archmaid-askpass-x/askpass.sh"
	if got := askpassVals(stepEnv(step{command: "topgrade", needsSudo: true})); len(got) != 1 || got[0] != askpassWrapper {
		t.Fatalf("sudo step SUDO_ASKPASS = %v, want [%s]", got, askpassWrapper)
	}
	if got := askpassVals(stepEnv(step{command: "yay"})); len(got) != 0 {
		t.Fatalf("non-sudo step SUDO_ASKPASS = %v, want none", got)
	}
	askpassWrapper = ""
	if got := askpassVals(stepEnv(step{command: "sudo", needsSudo: true})); len(got) != 0 {
		t.Fatalf("unwired askpass: sudo step SUDO_ASKPASS = %v, want none", got)
	}
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
		ch := make(chan streamMsg, 8)
		runStep(c.s, ch)
		lines, err := drainStep(ch)
		if err != nil || len(lines) != 1 || lines[0] != c.want {
			t.Fatalf("%s saw SUDO_ASKPASS %v (err %v), want %s", c.s.command, lines, err, c.want)
		}
	}
}

// TestAskpassEndToEnd runs the wrapper the way sudo would: it execs this test
// binary in --askpass mode, which reaches the server; answering the request
// on reqs makes the helper print the password.
func TestAskpassEndToEnd(t *testing.T) {
	askpassEnv(t)
	s := startAskpassServer()
	defer s.close()
	if s.ln == nil {
		t.Fatal("server not listening")
	}

	prompt := make(chan string, 1)
	go func() {
		select {
		case req := <-s.reqs:
			prompt <- req.prompt
			fmt.Fprint(req.conn, "pw\n")
			req.conn.Close()
		case <-time.After(askpassTimeout):
			close(prompt)
		}
	}()

	cmd := exec.Command(s.wrapper, "Password:")
	cmd.Stderr = os.Stderr
	// Under -race the helper would otherwise sleep 1s on exit (the race
	// runtime's default atexit_sleep_ms).
	cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("wrapper failed: %v (stdout %q)", err, out)
	}
	if string(out) != "pw\n" {
		t.Fatalf("wrapper printed %q, want %q", out, "pw\n")
	}
	if got := recv(t, prompt, "the request"); got != "Password:" {
		t.Fatalf("server saw prompt %q, want %q", got, "Password:")
	}
}

// askpassOverRunning opens an askpass popup over a running queue, with the
// server end of an in-memory connection standing in for the helper.
func askpassOverRunning(t *testing.T) (model, net.Conn) {
	t.Helper()
	client, server := pipeConn(t)

	m := sudoStepRunning(t, fixedModel())
	m.screen = screenRunning
	m = update(m, helperAsks(client))
	if m.screen != screenPassword || m.bg != screenRunning || m.askpassConn != client {
		t.Fatalf("screen=%d bg=%d conn set=%v; want the prompt over screenRunning",
			m.screen, m.bg, m.askpassConn != nil)
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
	if m.authInFlight || m.askpassConn != nil || m.screen != screenRunning || m.pwAttempts != 0 {
		t.Fatalf("authInFlight=%v conn set=%v screen=%d attempts=%d; want the popup closed over screenRunning, count reset",
			m.authInFlight, m.askpassConn != nil, m.screen, m.pwAttempts)
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
	if m.authInFlight || m.screen != screenPassword || m.askpassConn == nil {
		t.Fatalf("authInFlight=%v screen=%d conn set=%v; want the popup still up, unlocked",
			m.authInFlight, m.screen, m.askpassConn != nil)
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
	m = update(m, askpassResultMsg{conn: m.askpassConn, err: errors.New("denied"),
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
			if m.pwAttempts != i || m.askpassConn == nil {
				t.Fatalf("after %d wrong: attempts=%d conn set=%v", i, m.pwAttempts, m.askpassConn != nil)
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
	if m.askpassConn != nil || m.screen != screenRunning || !m.sudoUnavailable {
		t.Fatalf("conn set=%v screen=%d sudoUnavailable=%v; want the request cancelled, back on screenRunning, sudo steps skipped",
			m.askpassConn != nil, m.screen, m.sudoUnavailable)
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
		if !strings.Contains(m.pwErr, "line break") || m.pwAttempts != 0 || m.askpassConn == nil {
			t.Fatalf("%q: pwErr=%q attempts=%d conn set=%v; want it refused, uncounted, popup up",
				pw, m.pwErr, m.pwAttempts, m.askpassConn != nil)
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
	other, _ := pipeConn(t)
	got := update(m, askpassResultMsg{conn: other, err: errors.New("nope")})
	if got.pwAttempts != 0 || got.askpassConn != m.askpassConn || got.screen != screenPassword {
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
	if m.askpassConn != nil {
		t.Fatal("askpassConn not cleared after cancel")
	}
	if m.screen != screenRunning {
		t.Fatalf("screen = %d, want screenRunning", m.screen)
	}
	if m.password.Value() != "" {
		t.Fatal("typed password left in the input after cancel")
	}
}
