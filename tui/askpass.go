package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// askpass routes sudo password prompts to a TUI popup instead of the tty. The
// archmaid binary doubles as sudo's SUDO_ASKPASS helper: when sudo needs a
// password it runs a tiny wrapper that execs `archmaid --askpass <sock>`, which
// asks the running TUI over a unix socket and prints back whatever the user
// typed. The password is passed straight through — never stored.

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

// askpassServer owns the socket + wrapper script for a TUI session.
type askpassServer struct {
	reqs    chan askpassMsg
	ln      net.Listener
	sock    string
	wrapper string
}

// startAskpassServer opens the per-run socket, writes the SUDO_ASKPASS wrapper,
// and points sudo at it. Best-effort: on any error askpass simply isn't wired
// up and `sudo -A` steps fail cleanly. reqs is closed only when close() runs.
func startAskpassServer() *askpassServer {
	s := &askpassServer{reqs: make(chan askpassMsg)}

	exe, err := os.Executable()
	if err != nil {
		return s
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	s.sock = filepath.Join(dir, fmt.Sprintf("archmaid-askpass-%d.sock", os.Getpid()))
	s.wrapper = filepath.Join(dir, fmt.Sprintf("archmaid-askpass-%d.sh", os.Getpid()))
	_ = os.Remove(s.sock)

	ln, err := net.Listen("unix", s.sock)
	if err != nil {
		return s
	}
	s.ln = ln

	// The wrapper bakes in the socket path (via argv) so we never depend on an
	// env var reaching the helper — and never risk sudo running archmaid as a
	// full TUI. "$@" forwards sudo's prompt text.
	script := fmt.Sprintf("#!/bin/sh\nexec %q --askpass %q \"$@\"\n", exe, s.sock)
	if err := os.WriteFile(s.wrapper, []byte(script), 0o700); err != nil {
		ln.Close()
		s.ln = nil
		return s
	}
	os.Setenv("SUDO_ASKPASS", s.wrapper)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			prompt, _ := bufio.NewReader(conn).ReadString('\n')
			s.reqs <- askpassMsg{prompt: strings.TrimRight(prompt, "\r\n"), conn: conn}
		}
	}()
	return s
}

func (s *askpassServer) close() {
	if s.ln != nil {
		s.ln.Close()
	}
	if s.sock != "" {
		os.Remove(s.sock)
	}
	if s.wrapper != "" {
		os.Remove(s.wrapper)
	}
}
