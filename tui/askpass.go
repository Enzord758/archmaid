package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// askpass routes sudo password prompts to a TUI popup instead of the tty. The
// archmaid binary doubles as sudo's SUDO_ASKPASS helper: when sudo needs a
// password it runs a tiny wrapper that execs `archmaid --askpass <sock>`, which
// asks the running TUI over a unix socket and prints back whatever the user
// typed. The password is passed straight through — never stored.
//
// SUDO_ASKPASS is not set in archmaid's own environment: runStep hands it only
// to the steps that use sudo (see stepEnv), so nothing else archmaid starts
// learns where the socket is. Those steps still include topgrade, and with it
// every AUR build script, so the model also answers a request only while a sudo
// step runs and only from archmaid's own helper, run by sudo, inside that
// step's process tree (see verifyAskpassPeer).

// askpassMsg is delivered to the model when a child's sudo asks for a password.
// The model vets peer, shows a popup and writes the password back over conn (or
// closes it unanswered to cancel, which sudo treats as a failed attempt).
type askpassMsg struct {
	prompt string
	conn   net.Conn
	peer   askpassPeer
}

// askpassResultMsg is the outcome of checking a password typed into the
// askpass popup (see answerAskpass). conn ties it to the request it answers.
type askpassResultMsg struct {
	conn   net.Conn
	err    error  // su rejected the password; nothing was written to conn
	detail string // a lockout notice to show verbatim, if su printed one
}

// answerAskpass validates password with su before sudo sees it, like
// authSudo's first stage, and writes it to conn only once it's known to be
// right. A wrong answer to `sudo -A` burns two pam_faillock failures (deny=3
// on Arch: one typo from lockout); a wrong su burns none. The password is
// written straight through — never stored.
func answerAskpass(conn net.Conn, password string) tea.Cmd {
	return func() tea.Msg {
		if detail, err := suCheck(password); err != nil {
			return askpassResultMsg{conn: conn, err: err, detail: detail}
		}
		fmt.Fprintf(conn, "%s\n", password)
		conn.Close()
		return askpassResultMsg{conn: conn}
	}
}

// askpassPeer is the process on the other end of an askpass connection: its
// pid and uid as the kernel recorded them at connect time (SO_PEERCRED), and
// its start time read when the connection was accepted, so a later check can
// tell whether that pid still names the same process.
type askpassPeer struct {
	pid, uid int
	start    uint64
}

// runAskpass is the SUDO_ASKPASS helper path (archmaid --askpass <sock> [prompt]).
// Returns the process exit code: 0 on success, 1 if it can't reach the TUI or
// the prompt is cancelled.
func runAskpass(sock, prompt string) int {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return 1
	}
	defer conn.Close()
	fmt.Fprintln(conn, prompt) // tell the TUI what sudo is asking
	pw, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return 1 // TUI closed the connection unanswered → cancel
	}
	fmt.Print(pw) // pw keeps its trailing newline, which sudo expects
	return 0
}

// askpassWrapper is the SUDO_ASKPASS wrapper that runStep gives to sudo steps,
// or "" while askpass isn't wired up (no server, or it couldn't start). Set by
// startAskpassServer, cleared by close.
var askpassWrapper string

// askpassReadTimeout bounds how long a client may take to send its prompt
// line. A var so tests can shrink it.
var askpassReadTimeout = 5 * time.Second

// maxAskpassPrompt caps the prompt line a client may send. sudo's prompts are
// a few dozen bytes; anything longer than this is dropped.
const maxAskpassPrompt = 4096

// askpassServer owns the private directory, socket and wrapper script for a
// TUI session.
type askpassServer struct {
	reqs        chan askpassMsg
	done        chan struct{} // closed by close(): pending deliveries give up
	ln          net.Listener
	dir         string // private 0700 directory holding sock and wrapper
	sock        string
	wrapper     string
	readTimeout time.Duration
	readProc    func(pid int) (procInfo, error) // readProc, fixed at start
}

// startAskpassServer creates a private directory under XDG_RUNTIME_DIR, opens
// the socket and writes the SUDO_ASKPASS wrapper in it. Best-effort: on any
// error askpass simply isn't wired up, and sudo steps run `sudo -n` — they rely
// on the primed ticket and fail cleanly without it, never prompting on the tty.
// There is deliberately no fallback to /tmp: XDG_RUNTIME_DIR is the user's own
// 0700 directory, while /tmp is shared with every user on the machine. reqs is
// never closed; close() stops delivering to it.
func startAskpassServer() *askpassServer {
	s := &askpassServer{
		reqs:        make(chan askpassMsg),
		done:        make(chan struct{}),
		readTimeout: askpassReadTimeout,
		readProc:    readProc,
	}

	exe, err := os.Executable()
	if err != nil {
		return s
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return s
	}
	dir, err := os.MkdirTemp(runtimeDir, "archmaid-askpass-") // mode 0700
	if err != nil {
		return s
	}
	s.dir = dir
	s.sock = filepath.Join(dir, "askpass.sock")
	s.wrapper = filepath.Join(dir, "askpass.sh")

	ln, err := net.Listen("unix", s.sock)
	if err != nil {
		return s
	}
	s.ln = ln

	if err := os.WriteFile(s.wrapper, []byte(askpassScript(exe, s.sock)), 0o700); err != nil {
		ln.Close()
		s.ln = nil
		return s
	}
	askpassWrapper = s.wrapper

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
	return s
}

// askpassScript is the wrapper sudo runs. It bakes in the socket path (via
// argv) so we never depend on an env var reaching the helper — and never risk
// sudo running archmaid as a full TUI. "$@" forwards sudo's prompt text.
func askpassScript(exe, sock string) string {
	return "#!/bin/sh\nexec " + shellQuote(exe) + " --askpass " + shellQuote(sock) + " \"$@\"\n"
}

// shellQuote quotes s as one POSIX shell word. Inside single quotes nothing is
// special except the quote itself, which is closed, escaped and reopened.
// (Go's %q is not shell quoting: "$", "`" and "\" stay live inside it.)
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// serve identifies one client, reads its prompt line within readTimeout, and
// hands the request to the model, which decides whether to answer it. A client
// that can't be identified, is too slow, sends too much or goes away is
// dropped.
func (s *askpassServer) serve(conn net.Conn) {
	peer, err := s.peerOf(conn)
	if err != nil {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Now().Add(s.readTimeout))
	prompt, err := bufio.NewReader(io.LimitReader(conn, maxAskpassPrompt)).ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{}) // the user takes as long as they need
	select {
	case s.reqs <- askpassMsg{prompt: strings.TrimRight(prompt, "\r\n"), conn: conn, peer: peer}:
	case <-s.done:
		conn.Close()
	}
}

func (s *askpassServer) close() {
	if s.done != nil {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}
	if s.ln != nil {
		s.ln.Close()
	}
	if s.dir != "" {
		os.RemoveAll(s.dir)
	}
	if askpassWrapper == s.wrapper {
		askpassWrapper = ""
	}
}

// peerOf asks the kernel who is on the other end of conn (SO_PEERCRED) and
// records that process's start time.
func (s *askpassServer) peerOf(conn net.Conn) (askpassPeer, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return askpassPeer{}, errors.New("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return askpassPeer{}, err
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return askpassPeer{}, err
	}
	if credErr != nil {
		return askpassPeer{}, credErr
	}
	p, err := s.readProc(int(cred.Pid))
	if err != nil {
		return askpassPeer{}, err
	}
	return askpassPeer{pid: int(cred.Pid), uid: int(cred.Uid), start: p.start}, nil
}

// ── vetting a request ────────────────────────────────────────────────────────

// procInfo is what the askpass check reads about one process.
type procInfo struct {
	ppid  int
	start uint64 // start time: field 22 of /proc/<pid>/stat, in ticks since boot
	comm  string // field 2 of /proc/<pid>/stat: the executed file's name
	euid  int    // effective uid, from /proc/<pid>/status
	exe   string // /proc/<pid>/exe, or "" when we may not read it
}

// ourUID is the uid a peer must run as. A var so tests can pin it instead of
// depending on who runs the suite.
var ourUID = os.Getuid

// readProc looks a process up in /proc. A var so tests can stand in a fake
// process table.
var readProc = readProcFS

// readProcFS reads pid's procInfo from /proc. The exe link of a process we
// may not ptrace — another user's, or a setuid one like sudo — is unreadable;
// exe is then left empty rather than failing the lookup.
func readProcFS(pid int) (procInfo, error) {
	dir := "/proc/" + strconv.Itoa(pid)
	stat, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return procInfo{}, err
	}
	// "pid (comm) state ppid …": comm may hold spaces and parentheses itself,
	// so it runs from the first "(" to the last ")".
	open, end := bytes.IndexByte(stat, '('), bytes.LastIndexByte(stat, ')')
	if open < 0 || end < open {
		return procInfo{}, fmt.Errorf("pid %d: malformed stat", pid)
	}
	f := strings.Fields(string(stat[end+1:])) // f[0] is field 3
	if len(f) < 20 {
		return procInfo{}, fmt.Errorf("pid %d: short stat", pid)
	}
	ppid, err1 := strconv.Atoi(f[1])
	start, err2 := strconv.ParseUint(f[19], 10, 64)
	if err1 != nil || err2 != nil {
		return procInfo{}, fmt.Errorf("pid %d: malformed stat", pid)
	}
	info := procInfo{ppid: ppid, start: start, comm: string(stat[open+1 : end]), euid: -1}

	status, err := os.ReadFile(dir + "/status")
	if err != nil {
		return procInfo{}, err
	}
	for _, line := range strings.Split(string(status), "\n") {
		if rest, ok := strings.CutPrefix(line, "Uid:"); ok {
			if ids := strings.Fields(rest); len(ids) >= 2 { // real, effective, …
				info.euid, _ = strconv.Atoi(ids[1])
			}
		}
	}
	if exe, err := os.Readlink(dir + "/exe"); err == nil {
		info.exe = strings.TrimSuffix(exe, " (deleted)")
	}
	return info, nil
}

// sudoPath is sudo's binary; the askpass helper's parent must be it.
const sudoPath = "/usr/bin/sudo"

// maxAskpassChain bounds the walk from sudo up to the running step.
const maxAskpassChain = 64

// askpassRequester is who a request the model answers came from, for the popup.
type askpassRequester struct {
	step      string // title of the running step it belongs to
	callerExe string // real binary of the process that ran sudo; "" if unreadable
	callerPid int
	prompt    string // sudo's prompt, sanitized; untrusted (sudo -p sets it)
}

// verifyAskpassPeer decides whether a request may have the password. Anything
// that inherited SUDO_ASKPASS can connect — under topgrade that includes every
// AUR build script — so the peer must be:
//
//   - running as our uid;
//   - still the process that connected (same start time as at accept: its pid
//     wasn't reused);
//   - archmaid itself (the wrapper execs us in --askpass mode);
//   - a child of sudo; and
//   - inside the running step's process tree: walking up from sudo reaches
//     stepPid, started at stepStart.
//
// A fork's parent always started before it, so a parent with a later start
// time than its child is a reused pid, and the walk refuses it. On success it
// returns sudo's caller for the popup; the caller fills in step and prompt.
func verifyAskpassPeer(peer askpassPeer, stepPid int, stepStart uint64) (askpassRequester, error) {
	if peer.uid != ourUID() {
		return askpassRequester{}, fmt.Errorf("pid %d runs as uid %d, not ours", peer.pid, peer.uid)
	}
	helper, err := readProc(peer.pid)
	if err != nil {
		return askpassRequester{}, fmt.Errorf("pid %d is gone", peer.pid)
	}
	if helper.start != peer.start {
		return askpassRequester{}, fmt.Errorf("pid %d was reused since it connected", peer.pid)
	}
	if self, err := os.Executable(); err != nil || helper.exe != self {
		return askpassRequester{}, fmt.Errorf("pid %d is %s, not archmaid's askpass helper", peer.pid, orUnknown(helper.exe))
	}
	sudo, err := readProc(helper.ppid)
	if err != nil || !isSudo(sudo) {
		return askpassRequester{}, fmt.Errorf("pid %d was not started by sudo (parent pid %d)", peer.pid, helper.ppid)
	}

	pid, p, child := helper.ppid, sudo, helper
	for depth := 0; ; depth++ {
		if p.start > child.start {
			return askpassRequester{}, fmt.Errorf("pid %d was reused", pid)
		}
		if pid == stepPid {
			if p.start != stepStart {
				return askpassRequester{}, fmt.Errorf("the step's pid %d was reused", pid)
			}
			break
		}
		if p.ppid <= 1 || depth >= maxAskpassChain {
			return askpassRequester{}, fmt.Errorf("pid %d's sudo was not started by the running step", peer.pid)
		}
		child, pid = p, p.ppid
		if p, err = readProc(pid); err != nil {
			return askpassRequester{}, fmt.Errorf("pid %d's sudo was not started by the running step", peer.pid)
		}
	}

	from := askpassRequester{callerPid: sudo.ppid}
	if caller, err := readProc(sudo.ppid); err == nil {
		from.callerExe = caller.exe
	}
	return from, nil
}

// isSudo reports whether p is sudo. sudo is setuid root, and reading another
// process's /proc/<pid>/exe needs ptrace access, which a user doesn't have over
// a root process — so for a real sudo exe is normally empty. Then fall back to
// running with euid 0 under the name "sudo": a user can rename only their own
// processes, and those never run as root. comm comes from the executed file's
// name, though, so a symlink named sudo to another setuid-root binary passes
// too; none of Arch's starts a user program as its child without
// authenticating first. If exe is readable it must be sudoPath.
func isSudo(p procInfo) bool {
	if p.exe != "" {
		return p.exe == sudoPath
	}
	return p.euid == 0 && p.comm == "sudo"
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown program"
	}
	return s
}

// askpassPromptText is sudo's prompt as the popup shows it: untrusted (sudo -p
// lets whoever runs sudo set it), so stripped of escapes and control
// characters, kept to one line and clipped.
func askpassPromptText(prompt string) string {
	return clipRunes(oneLine(prompt), 48)
}

// oneLine is untrusted text made safe to show on one line: sanitized, with
// every run of whitespace (newlines included) collapsed to one space.
func oneLine(s string) string {
	return strings.Join(strings.Fields(Sanitize(s, SanitizePlain)), " ")
}

// clipRunes shortens s to at most n runes, marking a cut with "…".
func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// clipPath shortens a path to at most n runes, keeping its tail (the binary's
// name) and marking the cut with "…".
func clipPath(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n+1:])
}
