package privilege

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Enzord758/archmaid/internal/privilege/internal/seam"
	"github.com/Enzord758/archmaid/internal/proc"
)

// askpass routes sudo password prompts to the UI instead of the tty. The
// archmaid binary doubles as sudo's SUDO_ASKPASS helper: when sudo needs a
// password it runs a tiny wrapper that execs `archmaid --askpass <sock>`
// (RunClient), which asks the running UI over a unix socket and prints back
// whatever the user typed. The password is passed straight through — never
// stored.
//
// The wrapper's path reaches only the steps that use sudo (Sudo.Env). Those
// still include topgrade, and with it every AUR build script, so a request is
// answered only from archmaid's own helper, run by sudo, inside the running
// step's process tree (Request.Verify), and only once su has accepted the
// password (Request.Answer). Answer enforces the first itself: it refuses a
// Request that Verify hasn't passed (ErrUnverified), so a frontend that
// forgets the check gets a refusal, not a leaked password.

// DefaultReadTimeout bounds how long a client may take to send its prompt
// line, when ServerOptions leaves it unset.
const DefaultReadTimeout = 5 * time.Second

// MaxPrompt caps the prompt line a client may send. sudo's prompts are a few
// dozen bytes; a client that sends more than this without a newline is
// dropped.
const MaxPrompt = 4096

// ErrNoRuntimeDir is Listen's error without XDG_RUNTIME_DIR. There is
// deliberately no fallback to /tmp: XDG_RUNTIME_DIR is the user's own 0700
// directory, while /tmp is shared with every user on the machine.
var ErrNoRuntimeDir = errors.New("askpass: no XDG_RUNTIME_DIR")

// ErrLineBreak is Answer's error for a password with a line break: sudo reads
// one line, so it would get a different password than su checked.
var ErrLineBreak = errors.New("a password can't contain a line break")

// ErrUnverified is Answer's error for a Request that Request.Verify hasn't
// passed: nothing is written and su doesn't run.
var ErrUnverified = errors.New("askpass: the request was not verified")

// ServerOptions are the knobs a Server is started with.
type ServerOptions struct {
	// ReadTimeout bounds how long a client may take to send its prompt
	// line; zero means DefaultReadTimeout.
	ReadTimeout time.Duration
}

// Server owns the private directory, socket and wrapper script for one UI
// session.
type Server struct {
	reqs        chan *Request
	done        chan struct{} // closed by Close: pending deliveries give up
	closeOnce   sync.Once
	ln          net.Listener
	dir         string // private 0700 directory holding sock and wrapper
	sock        string
	wrapper     string
	readTimeout time.Duration
	readProc    func(pid int) (proc.Info, error)
}

// Listen creates a private 0700 directory under runtimeDir (the user's
// XDG_RUNTIME_DIR), opens the socket in it and writes the SUDO_ASKPASS
// wrapper next to it, which execs exe in --askpass mode on that socket. On
// any error nothing is left behind, and the caller runs without askpass:
// sudo steps then run `sudo -n` (Sudo with an empty Wrapper), relying on the
// primed ticket and failing cleanly without it, never prompting on the tty.
func Listen(runtimeDir, exe string, o ServerOptions) (*Server, error) {
	if runtimeDir == "" {
		return nil, ErrNoRuntimeDir
	}
	s := &Server{
		reqs:        make(chan *Request),
		done:        make(chan struct{}),
		readTimeout: o.ReadTimeout,
		readProc:    sys.readProc, // the same /proc that Verify reads
	}
	if s.readTimeout <= 0 {
		s.readTimeout = DefaultReadTimeout
	}
	if s.readProc == nil {
		s.readProc = proc.Read
	}

	dir, err := os.MkdirTemp(runtimeDir, "archmaid-askpass-") // mode 0700
	if err != nil {
		return nil, err
	}
	s.dir = dir
	s.sock = filepath.Join(dir, "askpass.sock")
	s.wrapper = filepath.Join(dir, "askpass.sh")

	ln, err := net.Listen("unix", s.sock)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if err := os.WriteFile(s.wrapper, []byte(wrapperScript(exe, s.sock)), 0o700); err != nil {
		ln.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	s.ln = ln

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// One goroutine per client: a client that never sends its
			// prompt holds up only itself, never the next sudo.
			go s.serve(conn)
		}
	}()
	return s, nil
}

// Requests delivers each client's request, once its prompt line has arrived.
// It is never closed; after Close nothing more is delivered.
func (s *Server) Requests() <-chan *Request { return s.reqs }

// Wrapper is the SUDO_ASKPASS wrapper's path, for Sudo.Wrapper.
func (s *Server) Wrapper() string { return s.wrapper }

// Close stops accepting, hangs up on requests not yet delivered and removes
// the private directory with the socket and wrapper. Safe to call twice, and
// on a nil Server.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		close(s.done)
		s.ln.Close()
		os.RemoveAll(s.dir)
	})
}

// wrapperScript is the wrapper sudo runs. It bakes in the socket path (via
// argv) so we never depend on an env var reaching the helper — and never risk
// sudo running archmaid as a full TUI. "$@" forwards sudo's prompt text.
func wrapperScript(exe, sock string) string {
	return "#!/bin/sh\nexec " + shellQuote(exe) + " --askpass " + shellQuote(sock) + " \"$@\"\n"
}

// shellQuote quotes s as one POSIX shell word. Inside single quotes nothing is
// special except the quote itself, which is closed, escaped and reopened.
// (Go's %q is not shell quoting: "$", "`" and "\" stay live inside it.)
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// serve identifies one client, reads its prompt line within readTimeout, and
// hands the request on; the UI decides whether to answer it. A client that
// can't be identified, is too slow, sends too much or goes away is dropped.
func (s *Server) serve(conn net.Conn) {
	peer, err := s.peerOf(conn)
	if err != nil {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Now().Add(s.readTimeout))
	prompt, err := bufio.NewReader(io.LimitReader(conn, MaxPrompt)).ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{}) // the user takes as long as they need
	select {
	case s.reqs <- newRequest(conn, strings.TrimRight(prompt, "\r\n"), peer):
	case <-s.done:
		conn.Close()
	}
}

// peerOf asks the kernel who is on the other end of conn (SO_PEERCRED) and
// records that process's start time.
func (s *Server) peerOf(conn net.Conn) (Peer, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return Peer{}, errors.New("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return Peer{}, err
	}
	if credErr != nil {
		return Peer{}, credErr
	}
	p, err := s.readProc(int(cred.Pid))
	if err != nil {
		return Peer{}, err
	}
	return Peer{Pid: int(cred.Pid), UID: int(cred.Uid), Start: p.Start}, nil
}

// ── one request ──────────────────────────────────────────────────────────────

// Peer is the process on the other end of an askpass connection: its pid and
// uid as the kernel recorded them at connect time (SO_PEERCRED), and its
// start time read when the connection was accepted, so a later check can tell
// whether that pid still names the same process.
type Peer struct {
	Pid, UID int
	Start    uint64
}

// Request is one client's request for the password. Exactly one of Answer
// (once it succeeds) or Cancel ends it. It never holds the password. Only a
// Server makes one (outside tests): its Peer is what the kernel said about
// the connection, never a caller's claim.
type Request struct {
	// Prompt is sudo's prompt as the client sent it, line ending trimmed.
	// Untrusted: whoever runs sudo sets it with -p.
	Prompt string
	// Peer is who connected, for display; Verify checks it.
	Peer Peer
	conn net.Conn
	// verified is set by a Verify that passed; Answer requires it.
	verified atomic.Bool
	// beforeSend, for tests, runs after the relay acknowledged the
	// handshake and before the password is written.
	beforeSend func()
}

// newRequest wraps a connection that carries one request, as a Server
// delivers it. Unexported: pairing a connection with a peer is the Server's
// job (SO_PEERCRED), not a caller's.
func newRequest(conn net.Conn, prompt string, peer Peer) *Request {
	return &Request{Prompt: prompt, Peer: peer, conn: conn}
}

// Other packages' tests reach newRequest and setSystem through privtest, by
// way of seam, which only internal/privilege's own packages can import.
func init() {
	seam.NewRequest = func(conn net.Conn, prompt string, pid, uid int, start uint64) any {
		return newRequest(conn, prompt, Peer{Pid: pid, UID: uid, Start: start})
	}
	seam.SetSystem = func(uid func() int, exe string, readProc func(int) (proc.Info, error)) func() {
		return setSystem(system{uid: uid, exe: exe, readProc: readProc})
	}
}

// Verify decides whether this request may have the password (see verifyPeer
// for the checks) and, if so, marks it verified: the one way to make Answer
// accept it. On success it returns sudo's caller, for the UI to name. A
// request that fails stays unverified. stepPid and stepStart are the running
// step's child as proc.Child records it; everything else (our uid, our
// binary, /proc) Verify reads from the system itself, never from the caller.
func (r *Request) Verify(stepPid int, stepStart uint64) (Requester, error) {
	from, err := verifyPeer(r.Peer, stepPid, stepStart, sys)
	if err != nil {
		return Requester{}, err
	}
	r.verified.Store(true)
	return from, nil
}

// Answer validates pw with su before sudo sees it (CheckPassword, like
// Authenticate's first stage) and writes it to the client only once it's
// known to be right, then hangs up. A wrong answer to `sudo -A` burns two
// pam_faillock failures (deny=3 on Arch: one typo from lockout); a wrong su
// burns none. On a wrong password nothing is written, the request stays open
// for another try, and detail is a lockout notice to show verbatim, if su
// printed one. A password with a line break is refused (ErrLineBreak) before
// su runs. The password is written straight through — never stored.
//
// A request that Verify hasn't passed is refused first (ErrUnverified):
// nothing is written, su doesn't run, and the request stays open (Cancel it).
func (r *Request) Answer(pw string) (detail string, err error) {
	if !r.verified.Load() {
		return "", ErrUnverified
	}
	if strings.ContainsAny(pw, "\r\n") {
		return "", ErrLineBreak
	}
	if detail, err := CheckPassword(context.Background(), pw); err != nil {
		return detail, err
	}
	// A write that fails means the helper (and so its sudo) is gone; that
	// sudo's step reports the failure, so the answer itself still counts.
	line := make([]byte, len(pw)+1)
	copy(line, pw)
	line[len(pw)] = '\n'
	r.deliver(line)
	clear(line)
	return "", nil
}

// The handshake before a password goes to the askpass relay (RunClient):
// the server says the request is verified; the relay, which had to stay
// dumpable until then (Verify reads its /proc exe), makes itself
// non-dumpable and acknowledges; only then is the password sent. So the
// relay never holds a password while a core dump or a same-uid ptrace
// could read it.
const (
	verifiedLine = "verified\n"
	ackLine      = "ok\n"
)

// handshakeTimeout bounds the wait for the relay's acknowledgement.
const handshakeTimeout = 5 * time.Second

// deliver runs the handshake and writes line (the password and its
// newline) to the relay, then hangs up. Without the acknowledgement
// nothing is written.
func (r *Request) deliver(line []byte) {
	defer r.conn.Close()
	if _, err := io.WriteString(r.conn, verifiedLine); err != nil {
		return
	}
	_ = r.conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	ack := make([]byte, len(ackLine))
	if _, err := io.ReadFull(r.conn, ack); err != nil || string(ack) != ackLine {
		return
	}
	_ = r.conn.SetReadDeadline(time.Time{})
	if r.beforeSend != nil {
		r.beforeSend()
	}
	_, _ = r.conn.Write(line)
}

// errHandshake: the server didn't open with the verified line.
var errHandshake = errors.New("askpass: no handshake from the UI")

// ReadAnswer is the relay's side of an answer: it reads the server's
// verified line, runs harden (RunClient: no core dumps from here on; nil
// for none), acknowledges, and returns the password line, newline
// included. An error means no password came: cancelled, or the UI went
// away.
func ReadAnswer(conn io.ReadWriter, harden func() error) (string, error) {
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	if line != verifiedLine {
		return "", errHandshake
	}
	if harden != nil {
		if err := harden(); err != nil {
			return "", err
		}
	}
	if _, err := io.WriteString(conn, ackLine); err != nil {
		return "", err
	}
	return br.ReadString('\n')
}

// hardenRelay makes the relay non-dumpable before it takes the password
// (prctl PR_SET_DUMPABLE, 0). A variable so this package's in-process
// tests of RunClient don't turn their own test binary non-dumpable.
var hardenRelay = func() error { return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) }

// Cancel hangs up without answering; sudo counts that as a failed attempt
// (the helper exits 1), but no PAM failure is burnt.
func (r *Request) Cancel() { r.conn.Close() }

// RunClient is the SUDO_ASKPASS helper (archmaid --askpass <sock> [prompt]):
// it sends prompt to the UI over sock and prints the answer, newline
// included, for sudo. It returns the process exit code: 0 on success, 1 if
// it can't reach the UI or the request is cancelled.
func RunClient(sock, prompt string) int {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return 1
	}
	defer conn.Close()
	fmt.Fprintln(conn, prompt) // tell the UI what sudo is asking
	pw, err := ReadAnswer(conn, hardenRelay)
	if err != nil {
		return 1 // the UI closed the connection unanswered → cancel
	}
	fmt.Print(pw) // pw keeps its trailing newline, which sudo expects
	return 0
}

// ── vetting a request ────────────────────────────────────────────────────────

// sudoPath is sudo's binary; the askpass helper's parent must be it.
const sudoPath = "/usr/bin/sudo"

// maxChain bounds the walk from sudo up to the running step.
const maxChain = 64

// system is the view of the machine that Verify (and Listen, for a peer's
// start time) checks against. Zero fields mean the real thing. Only tests
// change it: this package's own, and other packages' through privtest. No
// production caller can hand Verify a process table that always agrees.
type system struct {
	// uid returns the uid a peer must run as; nil means os.Getuid.
	uid func() int
	// exe is archmaid's own binary, which the helper must be; "" means
	// os.Executable.
	exe string
	// readProc looks a process up; nil means proc.Read.
	readProc func(pid int) (proc.Info, error)
}

// sys is the system Verify and Listen use: the real one, outside tests.
var sys system

// setSystem replaces sys until the returned restore runs.
func setSystem(s system) (restore func()) {
	old := sys
	sys = s
	return func() { sys = old }
}

// Requester is who a verified request came from: the process that ran sudo.
// Sudo names that sudo (its pid and start time, so a second request from
// the same sudo process can be told apart from another sudo's), and Session
// is the askpass helper's session id. Chain is the real binary of every
// process from sudo's caller up to the step's child, in that order ("" for
// one that couldn't be read).
type Requester struct {
	CallerExe string // its real binary; "" if unreadable
	CallerPid int
	Sudo      Peer
	Session   int
	Chain     []string
}

// keptCallers are the programs the GUI's kept password (KeptPassword) may
// answer a sudo for: topgrade, the step, and the yay it runs. pacman never
// runs sudo (it runs as root under it), and nothing else belongs between
// the step and its sudo: a makepkg, or a shell from a PKGBUILD's build(),
// gets the user's dialog naming it, never the kept password.
var keptCallers = map[string]bool{"/usr/bin/topgrade": true, "/usr/bin/yay": true}

// KeptMayAnswer reports whether the kept password may answer this request:
// every process from sudo's caller up to the step's child is one of the
// keptCallers (docs/HANDOVER.md, "The GUI keeps the run's password").
func (r Requester) KeptMayAnswer() bool {
	if len(r.Chain) == 0 {
		return false
	}
	for _, exe := range r.Chain {
		if !keptCallers[exe] {
			return false
		}
	}
	return true
}

// verifyPeer decides whether a request from peer may have the password
// (Request.Verify). Anything that inherited SUDO_ASKPASS can connect — under
// topgrade that includes every AUR build script — so the peer must be:
//
//   - running as our uid;
//   - still the process that connected (same start time as at accept: its pid
//     wasn't reused);
//   - archmaid itself (the wrapper execs us in --askpass mode);
//   - a child of sudo; and
//   - inside the running step's process tree: walking up from sudo reaches
//     stepPid, started at stepStart (proc.Child.Start, read before the step
//     could be reaped and its pid reused).
//
// A fork's parent always started before it, so a parent with a later start
// time than its child is a reused pid, and the walk refuses it. On success it
// returns sudo's caller, for the UI to name.
func verifyPeer(peer Peer, stepPid int, stepStart uint64, o system) (Requester, error) {
	uid, readProc := o.uid, o.readProc
	if uid == nil {
		uid = os.Getuid
	}
	if readProc == nil {
		readProc = proc.Read
	}
	self := o.exe
	if self == "" {
		exe, err := os.Executable()
		if err != nil {
			return Requester{}, fmt.Errorf("can't tell our own binary: %w", err)
		}
		self = exe
	}

	if peer.UID != uid() {
		return Requester{}, fmt.Errorf("pid %d runs as uid %d, not ours", peer.Pid, peer.UID)
	}
	helper, err := readProc(peer.Pid)
	if err != nil {
		return Requester{}, fmt.Errorf("pid %d is gone", peer.Pid)
	}
	if helper.Start != peer.Start {
		return Requester{}, fmt.Errorf("pid %d was reused since it connected", peer.Pid)
	}
	if helper.Exe != self {
		return Requester{}, fmt.Errorf("pid %d is %s, not archmaid's askpass helper", peer.Pid, orUnknown(helper.Exe))
	}
	sudo, err := readProc(helper.PPid)
	if err != nil || !isSudo(sudo) {
		return Requester{}, fmt.Errorf("pid %d was not started by sudo (parent pid %d)", peer.Pid, helper.PPid)
	}

	pid, p, child := helper.PPid, sudo, helper
	var chain []string
	for depth := 0; ; depth++ {
		if p.Start > child.Start {
			return Requester{}, fmt.Errorf("pid %d was reused", pid)
		}
		if pid != helper.PPid { // sudo's caller and up
			chain = append(chain, p.Exe)
		}
		if pid == stepPid {
			if p.Start != stepStart {
				return Requester{}, fmt.Errorf("the step's pid %d was reused", pid)
			}
			break
		}
		if p.PPid <= 1 || depth >= maxChain {
			return Requester{}, fmt.Errorf("pid %d's sudo was not started by the running step", peer.Pid)
		}
		child, pid = p, p.PPid
		if p, err = readProc(pid); err != nil {
			return Requester{}, fmt.Errorf("pid %d's sudo was not started by the running step", peer.Pid)
		}
	}

	from := Requester{CallerPid: sudo.PPid, Sudo: Peer{Pid: helper.PPid, UID: sudo.EUID, Start: sudo.Start}, Session: helper.Session, Chain: chain}
	if caller, err := readProc(sudo.PPid); err == nil {
		from.CallerExe = caller.Exe
	}
	return from, nil
}

// isSudo reports whether p is sudo. sudo is setuid root, and reading another
// process's /proc/<pid>/exe needs ptrace access, which a user doesn't have over
// a root process — so for a real sudo Exe is normally empty. Then fall back to
// running with euid 0 under the name "sudo": a user can rename only their own
// processes, and those never run as root. Comm comes from the executed file's
// name, though, so a symlink named sudo to another setuid-root binary passes
// too; none of Arch's starts a user program as its child without
// authenticating first. If Exe is readable it must be sudoPath.
func isSudo(p proc.Info) bool {
	if p.Exe != "" {
		return p.Exe == sudoPath
	}
	return p.EUID == 0 && p.Comm == "sudo"
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown program"
	}
	return s
}
