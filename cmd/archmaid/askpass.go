package main

import (
	"os"

	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// The TUI's side of askpass: the socket server, the client (RunClient), the
// peer check (Verify) and answering a request (Request.Answer) live in
// internal/privilege. Here the server is started for the session and its
// requests handed to the Driver (newDriver), which verifies each against the
// running step's child before the Session sees it; the model shows the
// popup the Session asks for (NeedsPassword, PasswordAskpass).

// askpassWrapper is the SUDO_ASKPASS wrapper sudo steps get, or "" while
// askpass isn't wired up (no server, or it couldn't start). Set by
// startAskpassServer, cleared by stopAskpassServer.
var askpassWrapper string

// privileged is how steps get root: sudo, with the askpass wrapper while it's
// wired up. Call it on main's goroutine (it reads askpassWrapper).
func privileged() privilege.Privileged {
	return privilege.Sudo{Wrapper: askpassWrapper}
}

// startAskpassServer starts the session's askpass server under
// XDG_RUNTIME_DIR and wires its wrapper up for privileged. Best-effort: on
// any error it returns nil, askpass simply isn't wired up, and sudo steps
// run `sudo -n` — they rely on the primed ticket and fail cleanly without
// it, never prompting on the tty.
func startAskpassServer() *privilege.Server {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	s, err := privilege.Listen(os.Getenv("XDG_RUNTIME_DIR"), exe, privilege.ServerOptions{})
	if err != nil {
		return nil
	}
	askpassWrapper = s.Wrapper()
	return s
}

// stopAskpassServer closes s and unwires its wrapper. A nil s is a no-op.
func stopAskpassServer(s *privilege.Server) {
	if s == nil {
		return
	}
	s.Close()
	if askpassWrapper == s.Wrapper() {
		askpassWrapper = ""
	}
}

// askpassRequester is who the open askpass request came from, for the popup.
type askpassRequester struct {
	step   string // title of the running step it belongs to
	caller string // the engine's Requester: "<exe> (pid N)", verified by the Driver
	prompt string // sudo's prompt, sanitized; untrusted (sudo -p sets it)
}

// askpassPromptText is sudo's prompt as the popup shows it: untrusted (sudo -p
// lets whoever runs sudo set it), so stripped of escapes and control
// characters, kept to one line and clipped.
func askpassPromptText(prompt string) string {
	return sanitize.Clip(sanitize.OneLine(prompt), 48)
}
