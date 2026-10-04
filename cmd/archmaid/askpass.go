package main

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// The TUI's side of askpass: the socket server, the client (RunClient), the
// peer check (Verify) and answering a request (Request.Answer) live in
// internal/privilege. Here the server is started for the session, its
// requests become askpassMsgs, and the model vets each one, shows a popup and
// hands the typed password to Request.Answer (see handleAskpass).

// askpassMsg is delivered to the model when a child's sudo asks for a password.
type askpassMsg struct{ req *privilege.Request }

// askpassResultMsg is the outcome of answering the open askpass request (see
// answerAskpass). req ties it to the request it answers.
type askpassResultMsg struct {
	req    *privilege.Request
	err    error  // su rejected the password; nothing was written to sudo
	detail string // a lockout notice to show verbatim, if su printed one
}

// answerAskpass checks password with su and, only if it's right, passes it
// to sudo (privilege.Request.Answer), off the UI thread.
func answerAskpass(req *privilege.Request, password string) tea.Cmd {
	return func() tea.Msg {
		detail, err := req.Answer(password)
		return askpassResultMsg{req: req, err: err, detail: detail}
	}
}

// askpassWrapper is the SUDO_ASKPASS wrapper that runStep gives to sudo steps,
// or "" while askpass isn't wired up (no server, or it couldn't start). Set by
// startAskpassServer, cleared by stopAskpassServer.
var askpassWrapper string

// privileged is how steps get root: sudo, with the askpass wrapper while it's
// wired up. Call it on the model's goroutine (it reads askpassWrapper).
func privileged() privilege.Privileged {
	return privilege.Sudo{Wrapper: askpassWrapper}
}

// startAskpassServer starts the session's askpass server under
// XDG_RUNTIME_DIR and wires its wrapper up for runStep. Best-effort: on any
// error it returns nil, askpass simply isn't wired up, and sudo steps run
// `sudo -n` — they rely on the primed ticket and fail cleanly without it,
// never prompting on the tty.
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
	step      string // title of the running step it belongs to
	callerExe string // real binary of the process that ran sudo; "" if unreadable
	callerPid int
	caller    string // the engine's Requester, as the popup shows it; "" in the old path
	prompt    string // sudo's prompt, sanitized; untrusted (sudo -p sets it)
}

// verifyAskpassPeer runs req.Verify for the running step. Only a request it
// passed can be answered (Request.Answer refuses any other).
func verifyAskpassPeer(req *privilege.Request, stepPid int, stepStart uint64) (askpassRequester, error) {
	from, err := req.Verify(stepPid, stepStart)
	if err != nil {
		return askpassRequester{}, err
	}
	return askpassRequester{callerExe: from.CallerExe, callerPid: from.CallerPid}, nil
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
	return sanitize.Clip(sanitize.OneLine(prompt), 48)
}
