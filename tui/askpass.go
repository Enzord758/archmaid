package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// askpass routes sudo password prompts to a TUI popup instead of the tty. The
// archmaid binary doubles as sudo's SUDO_ASKPASS helper: when sudo needs a
// password it runs a tiny wrapper that execs `archmaid --askpass <sock>`, which
// asks the running TUI over a unix socket and prints back whatever the user
// typed. The password is passed straight through — never stored.
//
// SUDO_ASKPASS is not set in archmaid's own environment: runStep hands it only
// to the steps that use sudo (see stepEnv), so nothing else archmaid starts
// learns where the socket is.

// askpassMsg is delivered to the model when a child's sudo asks for a password.
// The model shows a popup and writes the password back over conn (or closes it
// unanswered to cancel, which sudo treats as a failed attempt).
type askpassMsg struct {
	prompt string
	conn   net.Conn
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

// serve reads one client's prompt line, within readTimeout, and hands the
// request to the model. A client that is too slow, sends too much or goes
// away is dropped.
func (s *askpassServer) serve(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(s.readTimeout))
	prompt, err := bufio.NewReader(io.LimitReader(conn, maxAskpassPrompt)).ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{}) // the user takes as long as they need
	select {
	case s.reqs <- askpassMsg{prompt: strings.TrimRight(prompt, "\r\n"), conn: conn}:
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
