package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// askpassEnv gives startAskpassServer a private runtime dir and registers
// SUDO_ASKPASS with t.Setenv, so the server's own os.Setenv is undone on
// cleanup.
func askpassEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("SUDO_ASKPASS", "")
	return dir
}

// TestAskpassServerWiresWrapperAndSocket: the server writes a private wrapper
// that execs us in --askpass mode on its socket, points SUDO_ASKPASS at it,
// delivers each prompt (newline trimmed) on reqs, and close() removes both.
func TestAskpassServerWiresWrapperAndSocket(t *testing.T) {
	dir := askpassEnv(t)
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
	if filepath.Dir(s.sock) != dir || filepath.Dir(s.wrapper) != dir {
		t.Fatalf("sock %q / wrapper %q not under XDG_RUNTIME_DIR %q", s.sock, s.wrapper, dir)
	}
	fi, err := os.Stat(s.wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("wrapper mode = %v, want 0700", fi.Mode().Perm())
	}
	script, _ := os.ReadFile(s.wrapper)
	if !strings.Contains(string(script), "--askpass") || !strings.Contains(string(script), s.sock) {
		t.Fatalf("wrapper doesn't exec --askpass on the socket:\n%s", script)
	}
	if got := os.Getenv("SUDO_ASKPASS"); got != s.wrapper {
		t.Fatalf("SUDO_ASKPASS = %q, want %q", got, s.wrapper)
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
	for _, p := range []string{s.sock, s.wrapper} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after close (err %v)", p, err)
		}
	}
}

// TestAskpassServerFallsBackToTempDir: with no XDG_RUNTIME_DIR the socket and
// wrapper go in os.TempDir().
func TestAskpassServerFallsBackToTempDir(t *testing.T) {
	askpassEnv(t)
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", tmp)

	s := startAskpassServer()
	defer s.close()
	if s.ln == nil || filepath.Dir(s.sock) != tmp || filepath.Dir(s.wrapper) != tmp {
		t.Fatalf("listening=%v sock=%q wrapper=%q, want both under %q", s.ln != nil, s.sock, s.wrapper, tmp)
	}
}

// TestAskpassServerBestEffort: when the socket or the wrapper can't be created
// the server is left unwired (no listener, SUDO_ASKPASS untouched), and close()
// is still safe — as it is on a zero server.
func TestAskpassServerBestEffort(t *testing.T) {
	t.Run("zero server", func(t *testing.T) {
		(&askpassServer{}).close()
	})

	t.Run("socket path too long", func(t *testing.T) {
		// sun_path is 108 bytes on Linux; Listen must fail.
		long := filepath.Join(askpassEnv(t), strings.Repeat("d", 120))
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
		if got := os.Getenv("SUDO_ASKPASS"); got != "" {
			t.Fatalf("SUDO_ASKPASS = %q, want it left unset", got)
		}
		s.close()
	})

	t.Run("wrapper unwritable", func(t *testing.T) {
		dir := askpassEnv(t)
		// A directory squatting on the wrapper's path makes WriteFile fail.
		wrapper := filepath.Join(dir, fmt.Sprintf("archmaid-askpass-%d.sh", os.Getpid()))
		if err := os.Mkdir(wrapper, 0o700); err != nil {
			t.Fatal(err)
		}

		s := startAskpassServer()
		if s.ln != nil {
			t.Fatal("listener left open after the wrapper failed")
		}
		if got := os.Getenv("SUDO_ASKPASS"); got != "" {
			t.Fatalf("SUDO_ASKPASS = %q, want it left unset", got)
		}
		s.close()
		if _, err := os.Stat(s.sock); !os.IsNotExist(err) {
			t.Fatalf("socket still exists after close (err %v)", err)
		}
	})
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
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	server.SetDeadline(time.Now().Add(askpassTimeout))

	m := fixedModel()
	m.screen = screenRunning
	m = update(m, askpassMsg{prompt: "[sudo] password for u: ", conn: client})
	if m.screen != screenPassword || m.bg != screenRunning || m.askpassConn != client {
		t.Fatalf("screen=%d bg=%d conn set=%v; want the prompt over screenRunning",
			m.screen, m.bg, m.askpassConn != nil)
	}
	return m, server
}

// TestAskpassSubmitWritesPasswordToConn: enter writes the typed password plus
// newline straight to the connection, closes it, clears the input and returns
// to the screen underneath.
func TestAskpassSubmitWritesPasswordToConn(t *testing.T) {
	m, server := askpassOverRunning(t)
	m = update(m, key("secret"))

	// net.Pipe is synchronous: the write in Update blocks until it's read, so
	// the reader has to be running before enter is pressed.
	got := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(server) // up to the model's Close
		got <- string(b)
	}()
	m = update(m, key("enter"))

	if pw := recv(t, got, "the password"); pw != "secret\n" {
		t.Fatalf("helper received %q, want %q", pw, "secret\n")
	}
	if m.askpassConn != nil {
		t.Fatal("askpassConn not cleared after submit")
	}
	if m.screen != screenRunning {
		t.Fatalf("screen = %d, want screenRunning", m.screen)
	}
	if m.password.Value() != "" {
		t.Fatal("password still held in the input after submit")
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
