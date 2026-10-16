// Package privilege is how archmaid gets root for a step without ever
// prompting on the raw tty: the sudo ticket (probe, two-stage authentication,
// keep-alive refresh), the argv and environment a step's child runs with, and
// the SUDO_ASKPASS bridge that routes a mid-run sudo prompt back to the UI
// (Listen, RunClient, Request.Verify). It knows nothing about the UI.
//
// Passwords pass straight through: they are never logged, never stored, and
// never kept in a struct that outlives the call that was handed them. The
// one exception is the GUI's KeptPassword (kept.go): one mlocked page,
// for one run, wiped when it ends (docs/HANDOVER.md, "The GUI keeps the
// run's password").
//
// Read docs/HANDOVER.md ("Auth is two-stage", "Nothing may prompt on the raw
// tty", "SUDO_ASKPASS goes only to steps that use sudo", "An askpass request
// is answered only from the running sudo step") before changing any of it:
// each rule there was learned against Arch's pam_faillock deny=3.
package privilege

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/sysbin"
)

// Privileged is a way to run steps as root.
type Privileged interface {
	// Probe reports whether root is available right now without asking
	// for a password (a cached ticket).
	Probe(ctx context.Context) bool
	// Authenticate checks pw and, only if it is right, obtains the ticket.
	// On failure detail is a message worth showing verbatim (an account
	// lockout notice), or "".
	Authenticate(ctx context.Context, pw string) (detail string, err error)
	// Refresh extends a live ticket without prompting; a no-op once it has
	// lapsed.
	Refresh(ctx context.Context)
	// Command is how argv runs as a step: needsPriv marks a step that uses
	// root (it runs sudo itself, or is a sudo command).
	Command(argv []string, needsPriv bool) proc.Spec
}

// Sudo is Privileged through sudo's tty ticket. Wrapper is the SUDO_ASKPASS
// wrapper of a running askpass Server ((*Server).Wrapper), or "" when there
// is none. SudoDir is that Server's sudo wrapper directory
// ((*Server).SudoDir, the TUI's only), or "": with Wrapper, Env puts it
// first on the PATH of every step that gets the askpass, so a sudo that
// topgrade, yay or makepkg start by name asks through archmaid
// (sudowrap.go).
type Sudo struct {
	Wrapper string
	SudoDir string
}

var _ Privileged = Sudo{}

// Probe runs `sudo -n -v`: it succeeds only with a valid cached ticket and
// never prompts.
func (Sudo) Probe(ctx context.Context) bool {
	return exec.CommandContext(ctx, sysbin.Sudo, "-n", "-v").Run() == nil
}

// Authenticate validates pw and mints the sudo ticket — in two stages,
// because a wrong password fed to `sudo -S` burns TWO pam_faillock failures
// (sudo's passwd_tries retry re-enters PAM on the EOF'd stdin before giving
// up; measured live). With Arch's default `deny=3`, one mistyped password
// would leave the account one failure from lockout.
//
//  1. Validate with su (see CheckPassword) — single-shot PAM, burns nothing.
//  2. Only a known-correct password reaches `sudo -S -v`, which then always
//     succeeds: mints the ticket, burns nothing, and clears stale failures.
//
// Stderr is captured so a pam_faillock lockout notice ("account locked…")
// can be surfaced verbatim instead of a generic wrong-password error. A
// mid-run askpass request is answered the same way (see Request.Answer).
//
// The ticket is then checked for reuse (`sudo -n -v`). Without one, every
// sudo asks again: with the askpass and the sudo wrapper wired (Wrapper and
// SudoDir) each such sudo asks through archmaid, so the run goes on and
// detail is NoTicketNote, with a nil error; without the sudo wrapper,
// topgrade's and yay's sudo would ask on the terminal, so it is
// ErrNoTicket and the run must not start.
func (s Sudo) Authenticate(ctx context.Context, pw string) (detail string, err error) {
	if detail, err := CheckPassword(ctx, pw); err != nil {
		return detail, err
	}
	cmd := exec.CommandContext(ctx, sysbin.Sudo, "-S", "-v")
	cmd.Stdin = strings.NewReader(pw + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return lockoutNotice(stderr.String()), err
	}
	// Is the ticket just minted reusable? topgrade's and yay's own sudo
	// reads it. `sudo -n -v` never prompts and never counts toward
	// faillock; it fails with timestamp_timeout=0, timestamp_type=ppid (the
	// ticket is bound to the sudo that minted it), or anything else that
	// keeps the ticket from being reused.
	if err := exec.CommandContext(ctx, sysbin.Sudo, "-n", "-v").Run(); err != nil {
		if s.Wrapper != "" && s.SudoDir != "" {
			// Every sudo the steps start by name runs through the sudo
			// wrapper: it asks archmaid each time, never the terminal.
			return NoTicketNote, nil
		}
		return NoTicketMessage, fmt.Errorf("%w: %v", ErrNoTicket, err)
	}
	return "", nil
}

// ErrNoTicket is Authenticate's error when the password was right but sudo
// did not keep a ticket another sudo can reuse, and there is no sudo
// wrapper to send the steps' own sudo through archmaid (see Authenticate).
// The run must not start: topgrade's and yay's sudo would prompt on the
// terminal.
var ErrNoTicket = errors.New("privilege: sudo's ticket is not reusable")

// NoTicketNote is Authenticate's detail, with a nil error, when the ticket
// isn't reusable but the sudo wrapper is wired: the run goes on, and the
// user is told once why archmaid will ask again.
const NoTicketNote = "sudo won't keep your password between prompts here, so archmaid asks for it in its own " +
	"window each time sudo needs it."

// NoTicketMessage is what the user is told when ErrNoTicket refuses a run.
const NoTicketMessage = "sudo didn't keep your password for this terminal (sudo's ticket isn't reusable here, " +
	"e.g. timestamp_timeout=0). topgrade and yay would then ask for it directly on the terminal, outside " +
	"archmaid. Run this from archmaid-gui, or allow sudo to keep the ticket."

// Refresh runs `sudo -n -v`, extending the cached ticket non-interactively —
// the keep-alive that stops a long step (topgrade) from letting it lapse. A
// no-op once it has lapsed: a step's `sudo -A` then asks through askpass.
func (Sudo) Refresh(ctx context.Context) {
	_ = exec.CommandContext(ctx, sysbin.Sudo, "-n", "-v").Run()
}

// dropTimeout bounds DropTicket: the TUI calls it on its way out, which it
// must never hold up.
const dropTimeout = 3 * time.Second

// DropTicket runs `sudo -k`, invalidating the sudo ticket the TUI primed
// and kept alive, so the next sudo in that terminal asks again (the owner's
// decision M1). It can never prompt: -k alone only resets the timestamp and
// needs no password; SUDO_ASKPASS is dropped from its environment, stdin is
// /dev/null, and it is bounded by dropTimeout. It must NOT carry -n: sudo
// 1.9.17 treats a bare -k as its own mode (parse_args.c:540-545) and then
// allows no other flag (valid_flags = 0), so `sudo -n -k` fails the check at
// parse_args.c:567 with the usage text and exit 1, and the ticket survives.
// The ticket is the record for the caller's tty and session
// (timestamp_type=tty), which is the TUI's own: DropTicket must run from the
// TUI process. sudo is sysbin.Sudo, like every other sudo and su here: an
// absolute path, never looked up on PATH (internal/sysbin; under testenv the
// bare name, which its traps catch). Its error is the caller's
// to ignore: a failed drop must never block an exit.
func DropTicket(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, dropTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sysbin.Sudo, "-k")
	cmd.Env = Sudo{}.Env(false)
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

// Command is how argv runs as a step's child. A sudo command gets -A — read
// any password via SUDO_ASKPASS (a UI popup), never the tty — when it
// needsPriv and askpass is wired; otherwise -n. With SUDO_ASKPASS unset, -A
// would fall back to sudo.conf's `Path askpass` (a GUI helper, if one is
// configured) or fail, while -n relies on the primed ticket and fails
// cleanly without it. Any other command keeps its argv. The environment is
// Env's. Display is argv as the step names it, without the flag.
//
// A step's sudo, and its topgrade (which runs sudo itself and is the only
// program the kept password trusts to start one), run from internal/sysbin
// by absolute path, never from PATH; the step keeps the names it gave for
// its Display.
func (s Sudo) Command(argv []string, needsPriv bool) proc.Spec {
	if len(argv) == 0 {
		return proc.Spec{}
	}
	name, args := argv[0], argv[1:]
	display := name + " " + strings.Join(args, " ")
	if name == "sudo" {
		flag := "-n"
		if needsPriv && s.Wrapper != "" {
			flag = "-A"
		}
		args = append([]string{flag}, args...)
	}
	return proc.Spec{Name: program(name), Args: args, Env: s.Env(needsPriv), Display: display}
}

// program is what runs for a step's argv[0]: the privileged programs a
// step names (sudo, topgrade) from internal/sysbin, read at the call;
// anything else by its name.
func program(name string) string {
	switch name {
	case "sudo":
		return sysbin.Sudo
	case "topgrade":
		return sysbin.Topgrade
	}
	return name
}

// Env is the environment a step's child runs with: archmaid's own, without
// any SUDO_ASKPASS, plus the askpass wrapper for steps that needsPriv (sudo
// steps, and topgrade, which runs sudo itself). Everything a step starts
// inherits it, down to AUR build scripts, so the fewer steps that carry the
// socket's location the better; one the user's shell exported is dropped too.
// SUDO_ASKPASS is never set in archmaid's own environment.
//
// A step that gets the askpass also gets SudoDir, when set, first on its
// PATH (on each PATH= entry; without one, before execvp's default,
// /bin:/usr/bin): a sudo its programs start by name is then archmaid's
// wrapper (sudowrap.go). archmaid's own sudo runs by absolute path
// (sysbin.Sudo) and never looks at it.
func (s Sudo) Env(needsPriv bool) []string {
	environ := os.Environ()
	env := make([]string, 0, len(environ)+2)
	wrap := needsPriv && s.Wrapper != "" && s.SudoDir != ""
	path := false
	for _, kv := range environ {
		switch {
		case strings.HasPrefix(kv, "SUDO_ASKPASS="):
		case wrap && strings.HasPrefix(kv, "PATH="):
			env = append(env, "PATH="+s.SudoDir+string(os.PathListSeparator)+strings.TrimPrefix(kv, "PATH="))
			path = true
		default:
			env = append(env, kv)
		}
	}
	if wrap && !path {
		env = append(env, "PATH="+s.SudoDir+string(os.PathListSeparator)+"/bin:/usr/bin")
	}
	if needsPriv && s.Wrapper != "" {
		env = append(env, "SUDO_ASKPASS="+s.Wrapper)
	}
	return env
}

// CheckPassword validates pw as the current user's with
// `su -s /usr/bin/true <user>` (sysbin.Su, never a su on PATH, which would
// be handed the password): single-shot PAM with no retry loop, and on
// Arch su's PAM stack doesn't include pam_faillock at all, so a wrong entry
// burns nothing. -s runs true in place of the login shell, so none of the
// user's shell startup files run (`su -c true` still had zsh read
// ~/.zshenv). On failure, detail is a lockout notice from su's stderr, if
// there was one.
//
// A password with a line break is refused (ErrLineBreak) before su runs: su
// and sudo each read one line, so either would check something other than
// what was typed. This guards Sudo.Authenticate and Request.Answer alike,
// whether or not the frontend checked first.
func CheckPassword(ctx context.Context, pw string) (detail string, err error) {
	if strings.ContainsAny(pw, "\r\n") {
		return "", ErrLineBreak
	}
	self, err := user.Current()
	if err != nil {
		return "", err
	}
	check := exec.CommandContext(ctx, sysbin.Su, "-s", "/usr/bin/true", self.Username)
	check.Stdin = strings.NewReader(pw + "\n")
	var stderr bytes.Buffer
	check.Stderr = &stderr
	if err := check.Run(); err != nil {
		return lockoutNotice(stderr.String()), err
	}
	return "", nil
}

// lockoutMax caps a lockout notice, in runes.
const lockoutMax = 160

// lockoutNotice extracts a pam_faillock lockout message from PAM stderr, if
// present, so the UI can say "account locked" rather than "wrong password".
// It is shown verbatim, so it is made safe to: sanitized onto one line
// (escapes and controls dropped) and clipped to lockoutMax runes.
func lockoutNotice(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(strings.ToLower(line), "locked") {
			return sanitize.Clip(sanitize.OneLine(line), lockoutMax)
		}
	}
	return ""
}
