package privilege

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/privilege/internal/seam"
	"github.com/Enzord758/archmaid/internal/proc"
)

// The SUDO_ASKPASS round trip, over a real unix socket: the helper side
// (RunClient, what sudo execs), the server (Listen, Requests, Close), the two
// joined through the real wrapper script, and a Request answered (only after
// su accepts the password) or cancelled. The wrapper execs this test binary,
// which TestMain dispatches into RunClient exactly like archmaid's main().

// askpassTimeout bounds every wait here, so a broken round trip fails instead
// of hanging the suite.
const askpassTimeout = 3 * time.Second

// fakeUI listens on a fresh socket and serves one connection the way the UI
// does: read the prompt line, then answer "pw\n" — or, if answer is false,
// close unanswered. The prompt it saw arrives on the returned channel.
func fakeUI(t *testing.T, answer bool) (sock string, prompt <-chan string) {
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
		br := bufio.NewReader(conn)
		line, _ := br.ReadString('\n')
		got <- line
		if answer { // the handshake, then the password
			fmt.Fprint(conn, verifiedLine)
			if ack, _ := br.ReadString('\n'); ack == ackLine {
				fmt.Fprint(conn, "pw\n")
			}
		}
	}()
	return sock, got
}

// captureStdout runs fn with os.Stdout swapped for a pipe and returns what it
// printed. RunClient prints the password with fmt.Print, i.e. to os.Stdout.
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

// TestAskpassRunClient: the helper sends the prompt, prints the answer with its
// newline (sudo expects one) and exits 0; an unanswered close or an
// unreachable socket exits 1 and prints nothing.
func TestAskpassRunClient(t *testing.T) {
	// In process: harden would make this test binary non-dumpable.
	hardened := 0
	old := hardenRelay
	hardenRelay = func() error { hardened++; return nil }
	t.Cleanup(func() { hardenRelay = old })

	t.Run("answered", func(t *testing.T) {
		sock, prompt := fakeUI(t, true)
		var code int
		out := captureStdout(t, func() { code = RunClient(sock, "[sudo] password for u: ") })
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if out != "pw\n" {
			t.Fatalf("stdout = %q, want %q", out, "pw\n")
		}
		if hardened != 1 {
			t.Fatalf("hardened %d times before the password, want once", hardened)
		}
		if got := recv(t, prompt, "the prompt"); got != "[sudo] password for u: \n" {
			t.Fatalf("server saw %q, want the prompt line", got)
		}
	})

	t.Run("closed unanswered", func(t *testing.T) {
		sock, prompt := fakeUI(t, false)
		var code int
		out := captureStdout(t, func() { code = RunClient(sock, "Password:") })
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
		out := captureStdout(t, func() { code = RunClient(sock, "Password:") })
		if code != 1 || out != "" {
			t.Fatalf("exit = %d, stdout = %q; want 1 and nothing", code, out)
		}
	})
}

// listen starts a Server for this test binary in a fresh runtime dir, closed
// on cleanup. SUDO_ASKPASS is set to a sentinel, registered with t.Setenv, so
// a test can prove the server leaves the process's own environment alone.
func listen(t *testing.T, o ServerOptions) (*Server, string) {
	t.Helper()
	runtimeDir := t.TempDir()
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Listen(runtimeDir, exe, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, runtimeDir
}

// TestAskpassListenWiresWrapperAndSocket: the server makes a private 0700 directory
// under the runtime dir holding its socket and a 0700 wrapper that execs us
// in --askpass mode on that socket; it never sets SUDO_ASKPASS in our own
// environment; it delivers each prompt (line ending trimmed) on Requests,
// and Close removes the directory, twice over safely.
func TestAskpassListenWiresWrapperAndSocket(t *testing.T) {
	s, runtimeDir := listen(t, ServerOptions{})

	if filepath.Dir(s.dir) != runtimeDir || filepath.Dir(s.sock) != s.dir || s.Wrapper() != s.wrapper || filepath.Dir(s.wrapper) != s.dir {
		t.Fatalf("dir %q / sock %q / wrapper %q: want sock and wrapper in a private dir under %q",
			s.dir, s.sock, s.Wrapper(), runtimeDir)
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
	if got := os.Getenv("SUDO_ASKPASS"); got != "/from/the/users/shell" {
		t.Fatalf("SUDO_ASKPASS = %q: the server must not touch our own environment", got)
	}

	conn, err := net.Dial("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "Password: \r\n")
	req := recv(t, s.Requests(), "the askpass request")
	defer req.Cancel()
	// Only the line ending is trimmed — sudo's prompt keeps its trailing space.
	if req.Prompt != "Password: " {
		t.Fatalf("prompt = %q, want %q", req.Prompt, "Password: ")
	}

	s.Close()
	s.Close()
	if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
		t.Fatalf("%s still exists after Close (err %v)", s.dir, err)
	}
	(*Server)(nil).Close()
}

// TestAskpassListenNeedsRuntimeDir: with no runtime dir there is no server — no
// fallback to the shared /tmp.
func TestAskpassListenNeedsRuntimeDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	s, err := Listen("", "/usr/bin/archmaid", ServerOptions{})
	if s != nil || !errors.Is(err, ErrNoRuntimeDir) {
		t.Fatalf("Listen(\"\") = %v, %v; want no server and ErrNoRuntimeDir", s, err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("Listen left %d entries in TMPDIR", len(entries))
	}
}

// TestAskpassListenFailsClean: when the directory or the socket can't be created,
// Listen returns an error and leaves nothing behind.
func TestAskpassListenFailsClean(t *testing.T) {
	t.Run("runtime dir missing", func(t *testing.T) {
		if s, err := Listen(filepath.Join(t.TempDir(), "gone"), "/usr/bin/archmaid", ServerOptions{}); s != nil || err == nil {
			t.Fatalf("Listen = %v, %v; want an error", s, err)
		}
	})

	t.Run("socket path too long", func(t *testing.T) {
		// sun_path is 108 bytes on Linux; net.Listen must fail.
		long := filepath.Join(t.TempDir(), strings.Repeat("d", 100))
		if err := os.Mkdir(long, 0o700); err != nil {
			t.Fatal(err)
		}
		if len(long+"/archmaid-askpass-0123456789/askpass.sock") <= 108 {
			t.Fatal("the test needs a socket path over 108 bytes")
		}
		if s, err := Listen(long, "/usr/bin/archmaid", ServerOptions{}); s != nil || err == nil {
			t.Fatalf("Listen = %v, %v; want an error", s, err)
		}
		if entries, _ := os.ReadDir(long); len(entries) != 0 {
			t.Fatalf("Listen left %d entries behind", len(entries))
		}
	})
}

// TestAskpassWrapperQuoting: the wrapper survives paths a shell would otherwise
// expand or split — a quote, $(…), backquotes, a backslash, spaces. Go's %q
// would have left "$(…)" live inside double quotes. The script is run by the
// real /bin/sh against a stand-in "archmaid" that prints its argv.
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
	if err := os.WriteFile(wrapper, []byte(wrapperScript(exe, sock)), 0o700); err != nil {
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

// TestAskpassStuckClientDoesNotBlock: a client that connects and never sends its
// prompt neither delays the next client nor stays connected past the read
// deadline.
func TestAskpassStuckClientDoesNotBlock(t *testing.T) {
	s, _ := listen(t, ServerOptions{ReadTimeout: 300 * time.Millisecond})

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
	req := recv(t, s.Requests(), "the second client's request")
	req.Cancel()
	if req.Prompt != "Password:" {
		t.Fatalf("prompt = %q, want %q", req.Prompt, "Password:")
	}

	requireHangup(t, stuck)
}

// TestAskpassListenDefaults: unset ServerOptions mean DefaultReadTimeout and
// proc.Read.
func TestAskpassListenDefaults(t *testing.T) {
	s, _ := listen(t, ServerOptions{})
	if s.readTimeout != DefaultReadTimeout || s.readProc == nil {
		t.Fatalf("readTimeout = %v, readProc set = %v; want %v and proc.Read", s.readTimeout, s.readProc != nil, DefaultReadTimeout)
	}
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

// TestAskpassOversizedPromptDropped: a client that sends more than MaxPrompt
// without a newline is dropped, not delivered.
func TestAskpassOversizedPromptDropped(t *testing.T) {
	s, _ := listen(t, ServerOptions{})

	conn, err := net.Dial("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(strings.Repeat("x", MaxPrompt+10))); err != nil {
		t.Fatal(err)
	}
	requireHangup(t, conn)
	select {
	case req := <-s.Requests():
		req.Cancel()
		t.Fatalf("oversized prompt was delivered (%d bytes)", len(req.Prompt))
	default:
	}
}

// TestAskpassCloseDropsUndelivered: a request still waiting to be taken when the
// server closes is hung up on rather than left blocked.
func TestAskpassCloseDropsUndelivered(t *testing.T) {
	s, _ := listen(t, ServerOptions{})
	conn, err := net.Dial("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, "Password:")
	// Close only once the request waits to be taken: before that, Close
	// shuts the listener on a connection still in its backlog, and the
	// hang-up seen below would be the kernel's, not serve's.
	waitServeBlocked(t)
	s.Close() // nobody ever reads Requests
	requireHangup(t, conn)
}

// waitServeBlocked waits until a Server's serve goroutine has read its
// client's prompt and blocks handing the request on.
func waitServeBlocked(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(askpassTimeout)
	for time.Now().Before(deadline) {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		for _, g := range strings.Split(string(buf), "\n\n") {
			if strings.Contains(g, "[select") && strings.Contains(g, "privilege.(*Server).serve(") {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no serve goroutine is waiting to deliver a request")
}

// TestUnidentifiedPeerDropped: a client the server can't look up in /proc is
// hung up on before it can deliver anything.
func TestUnidentifiedPeerDropped(t *testing.T) {
	restore := setSystem(system{readProc: func(int) (proc.Info, error) { return proc.Info{}, os.ErrNotExist }})
	s, _ := listen(t, ServerOptions{}) // takes the empty /proc above
	restore()
	conn, err := net.Dial("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, "Password:")
	requireHangup(t, conn)
}

// TestServerReportsPeer: over a real socket the server records the
// connecting process — here the test itself — with its uid and start time.
func TestServerReportsPeer(t *testing.T) {
	s, _ := listen(t, ServerOptions{})
	conn, err := net.Dial("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("Password:\n"))
	req := recv(t, s.Requests(), "the request")
	defer req.Cancel()

	self, err := proc.Read(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if want := (Peer{Pid: os.Getpid(), UID: os.Getuid(), Start: self.Start}); req.Peer != want {
		t.Fatalf("peer = %+v, want %+v", req.Peer, want)
	}
}

// TestPeerOfNeedsUnixSocket: a connection that isn't a unix socket has no
// SO_PEERCRED and is refused.
func TestPeerOfNeedsUnixSocket(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err := (&Server{readProc: proc.Read}).peerOf(a); err == nil {
		t.Fatal("peerOf accepted a non-unix connection")
	}
}

// TestAskpassEndToEnd runs the wrapper the way sudo would: it execs this test
// binary in --askpass mode, which reaches the server; answering the request
// (su shimmed to accept) makes the helper print the password.
func TestAskpassEndToEnd(t *testing.T) {
	s, _ := listen(t, ServerOptions{})
	path := os.Getenv("PATH")
	shimAuth(t, 0, 0, "")

	type answered struct {
		prompt string
		err    error
	}
	done := make(chan answered, 1)
	relayOwner := make(chan int, 1)
	go func() {
		select {
		case req := <-s.Requests():
			// The transport is what's under test: the helper here is this
			// test binary, not run by sudo, so it is marked verified by hand
			// (Verify has tests of its own).
			req.verified.Store(true)
			req.beforeSend = func() { relayOwner <- procOwner(req.Peer.Pid) }
			_, err := req.Answer("pw")
			done <- answered{req.Prompt, err}
		case <-time.After(askpassTimeout):
			close(done)
		}
	}()

	cmd := exec.Command(s.Wrapper(), "Password:")
	cmd.Stderr = os.Stderr
	// The helper gets the real PATH back (only Answer's su is shimmed); under
	// -race it would otherwise sleep 1s on exit (the race runtime's default
	// atexit_sleep_ms).
	cmd.Env = append(os.Environ(), "PATH="+path, "GORACE=atexit_sleep_ms=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("wrapper failed: %v (stdout %q)", err, out)
	}
	if string(out) != "pw\n" {
		t.Fatalf("wrapper printed %q, want %q", out, "pw\n")
	}
	got := recv(t, done, "the request")
	if got.prompt != "Password:" || got.err != nil {
		t.Fatalf("server saw prompt %q, Answer err %v; want %q and nil", got.prompt, got.err, "Password:")
	}
	// A non-dumpable process's /proc files are root's: the relay was
	// non-dumpable before the password reached it.
	if uid := recv(t, relayOwner, "the relay's /proc owner"); uid != 0 {
		t.Fatalf("the relay's /proc/<pid>/status was owned by uid %d when the password went: it was still dumpable", uid)
	}
}

// procOwner is the uid owning /proc/<pid>/status: root's for a non-dumpable
// process, its own uid's otherwise; -1 if it can't be read.
func procOwner(pid int) int {
	var st syscall.Stat_t
	if err := syscall.Stat("/proc/"+strconv.Itoa(pid)+"/status", &st); err != nil {
		return -1
	}
	return int(st.Uid)
}

// unverifiedPipeRequest is a Request from the fake helper (helperPeer) over
// an in-memory connection, and the helper's end of it, both closed on
// cleanup. Nothing has verified it yet.
func unverifiedPipeRequest(t *testing.T) (*Request, net.Conn) {
	t.Helper()
	ours, helper := net.Pipe()
	t.Cleanup(func() { ours.Close(); helper.Close() })
	helper.SetDeadline(time.Now().Add(askpassTimeout))
	return newRequest(ours, "[sudo] password for u: ", helperPeer()), helper
}

// pipeRequest is unverifiedPipeRequest once Verify has passed it against the
// fake process table, so Answer will take it.
func pipeRequest(t *testing.T) (*Request, net.Conn) {
	t.Helper()
	req, helper := unverifiedPipeRequest(t)
	if _, err := verifyAgainst(t, req, fakeTree()); err != nil {
		t.Fatalf("Verify = %v, want the fake helper verified", err)
	}
	return req, helper
}

// requireNothingWritten fails unless the helper's end is still open with
// nothing to read.
func requireNothingWritten(t *testing.T, helper net.Conn) {
	t.Helper()
	helper.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, err := helper.Read(make([]byte, 64)); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("helper read n=%d err=%v; want nothing written and the connection still open", n, err)
	}
}

// TestAskpassAnswerRightPassword: su accepts → the password plus newline goes to
// the helper, which is then hung up on.
func TestAskpassAnswerRightPassword(t *testing.T) {
	dir := shimAuth(t, 0, 0, "")
	req, helper := pipeRequest(t)
	if req.Prompt != "[sudo] password for u: " || req.Peer != helperPeer() {
		t.Fatalf("newRequest = %+v", req)
	}

	// net.Pipe is synchronous: the write blocks until it's read, so the
	// reader has to be running before Answer is.
	got := make(chan string, 1)
	go func() {
		pw, _ := ReadAnswer(helper, nil) // the relay's side, handshake and all
		got <- pw
	}()
	if detail, err := req.Answer("secret"); err != nil || detail != "" {
		t.Fatalf("Answer = %q, %v; want success", detail, err)
	}
	if pw := recv(t, got, "the password"); pw != "secret\n" {
		t.Fatalf("helper received %q, want %q", pw, "secret\n")
	}
	requireSuOnly(t, dir, "secret")
}

// TestAskpassAnswerThen: accepted runs once su has accepted the password and
// before anything (the handshake included) reaches the helper; never for a
// password su refuses.
func TestAskpassAnswerThen(t *testing.T) {
	shimAuth(t, 0, 0, "")
	req, helper := pipeRequest(t)
	got := make(chan string, 1)
	go func() {
		pw, _ := ReadAnswer(helper, nil)
		got <- pw
	}()
	ran := false
	_, err := req.AnswerThen("secret", func() {
		ran = true
		select {
		case pw := <-got:
			t.Errorf("the helper had %q before accepted ran", pw)
		default:
		}
	})
	if err != nil || !ran {
		t.Fatalf("AnswerThen = %v, accepted ran: %v", err, ran)
	}
	if pw := recv(t, got, "the password"); pw != "secret\n" {
		t.Fatalf("helper received %q", pw)
	}

	shimAuth(t, 1, 0, "")
	req, helper = pipeRequest(t)
	if _, err := req.AnswerThen("wrong", func() { t.Error("accepted ran for a password su refused") }); err == nil {
		t.Fatal("AnswerThen took a password su refused")
	}
	requireNothingWritten(t, helper)
}

// TestAskpassAnswerWrongPasswordNeverWritten: su rejects → nothing reaches the
// helper, the connection stays open for another try, and su's lockout line
// comes back as the detail.
func TestAskpassAnswerWrongPasswordNeverWritten(t *testing.T) {
	dir := t.TempDir()
	su := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"" + dir + "/su.called\"\n" +
		"IFS= read -r line; printf '%s' \"$line\" > \"" + dir + "/su.stdin\"\n" +
		"printf 'The account is locked due to 3 failed logins.\\n' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "su"), []byte(su), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	req, helper := pipeRequest(t)

	detail, err := req.Answer("wrong")
	if err == nil || !strings.Contains(detail, "locked") {
		t.Fatalf("Answer = %q, %v; want an error with the lockout notice", detail, err)
	}
	requireSuOnly(t, dir, "wrong")
	requireNothingWritten(t, helper)
}

// TestAskpassAnswerLineBreakRefused: a password with a line break never reaches su
// or the helper: sudo reads one line, so it would get a different password
// than su checked.
func TestAskpassAnswerLineBreakRefused(t *testing.T) {
	dir := shimAuth(t, 0, 0, "")
	req, helper := pipeRequest(t)
	for _, pw := range []string{"good\nbad", "good\rbad"} {
		if _, err := req.Answer(pw); !errors.Is(err, ErrLineBreak) {
			t.Fatalf("Answer(%q) = %v, want ErrLineBreak", pw, err)
		}
	}
	if _, ok := called(t, dir, "su"); ok {
		t.Fatal("su ran for a password with a line break")
	}
	requireNothingWritten(t, helper)
}

// TestAskpassAnswerRequiresVerify: Answer itself enforces the peer check. A
// request nobody verified, and one whose Verify failed, are refused with
// ErrUnverified before su runs, nothing reaches the helper, and the request
// stays open. Only once Verify passes does the same password go through.
func TestAskpassAnswerRequiresVerify(t *testing.T) {
	dir := shimAuth(t, 0, 0, "")
	req, helper := unverifiedPipeRequest(t)
	// The helper reads from the start, so a wrongly answered request shows
	// up as bytes received (net.Pipe is synchronous: without a reader, a
	// write would block instead).
	got := make(chan string, 1)
	go func() {
		pw, _ := ReadAnswer(helper, nil) // the relay's side, handshake and all
		got <- pw
	}()
	if _, err := req.Answer("secret"); !errors.Is(err, ErrUnverified) {
		t.Fatalf("Answer on an unverified request = %v, want ErrUnverified (helper got %q)", err, recv(t, got, "the helper's read"))
	}

	// A helper sudo didn't start: Verify refuses it, and it stays refused.
	tree := fakeTree()
	h := tree[fakeHelperPid]
	h.PPid = fakeCallerPid
	tree[fakeHelperPid] = h
	if _, err := verifyAgainst(t, req, tree); err == nil {
		t.Fatal("Verify passed a helper that sudo didn't start")
	}
	if _, err := req.Answer("secret"); !errors.Is(err, ErrUnverified) {
		t.Fatalf("Answer after a failed Verify = %v, want ErrUnverified", err)
	}
	if _, ok := called(t, dir, "su"); ok {
		t.Fatal("su ran for an unverified request")
	}
	select {
	case b := <-got:
		t.Fatalf("the helper got %q from an unverified request", b)
	case <-time.After(50 * time.Millisecond): // still open, nothing written
	}

	if _, err := verifyAgainst(t, req, fakeTree()); err != nil {
		t.Fatalf("Verify = %v, want the fake helper verified", err)
	}
	if _, err := req.Answer("secret"); err != nil {
		t.Fatalf("Answer once verified = %v", err)
	}
	if pw := recv(t, got, "the password"); pw != "secret\n" {
		t.Fatalf("helper received %q, want %q", pw, "secret\n")
	}
}

// TestSeamRequestIsUnverified: the constructor privtest reaches through seam
// hands out a request that Answer still refuses until Verify passes it.
func TestSeamRequestIsUnverified(t *testing.T) {
	dir := shimAuth(t, 0, 0, "") // never the real su, should Answer get that far
	ours, helper := net.Pipe()
	defer ours.Close()
	defer helper.Close()
	helper.SetDeadline(time.Now().Add(askpassTimeout))
	go io.Copy(io.Discard, helper) // a wrong answer must not block on the pipe
	req := seam.NewRequest(ours, "p", fakeHelperPid, fakeUID, 400).(*Request)
	if req.Peer != helperPeer() {
		t.Fatalf("Peer = %+v, want %+v", req.Peer, helperPeer())
	}
	if _, err := req.Answer("secret"); !errors.Is(err, ErrUnverified) {
		t.Fatalf("Answer = %v, want ErrUnverified", err)
	}
	if _, ok := called(t, dir, "su"); ok {
		t.Fatal("su ran for an unverified request")
	}
}

// TestAskpassCancelHangsUpUnanswered: Cancel closes the connection without writing
// anything; the helper reads EOF.
func TestAskpassCancelHangsUpUnanswered(t *testing.T) {
	req, helper := pipeRequest(t)
	got := make(chan error, 1)
	go func() {
		n, err := helper.Read(make([]byte, 64))
		if n != 0 {
			err = fmt.Errorf("read %d bytes", n)
		}
		got <- err
	}()
	req.Cancel()
	if err := recv(t, got, "the hangup"); err != io.EOF {
		t.Fatalf("helper read %v, want io.EOF", err)
	}
}
