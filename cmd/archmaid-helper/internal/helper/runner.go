package helper

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// Absolute paths of everything the helper ever executes. Never resolved via
// PATH, never through a shell.
const (
	Pacman         = "/usr/bin/pacman"
	Paccache       = "/usr/bin/paccache"
	Journalctl     = "/usr/bin/journalctl"
	SystemdTmpfile = "/usr/bin/systemd-tmpfiles"
	Systemctl      = "/usr/bin/systemctl"
)

// ChildEnv is the complete environment every child runs with. The helper's
// own environment is also reset to this in main, so it is cleared twice.
//
// TERM stays dumb although the run output shows colour (task #111): no
// child's colour depends on it (pacman's comes from --color, helper.go,
// and pacman reads no TERM; paccache colours only on a terminal), so a
// real TERM would buy nothing, while systemd's tools take TERM=dumb as
// "no colour" on top of the pipe, and disable-unit parses `systemctl cat`,
// which with colour on wraps [Install] and every directive in SGR: the
// section would go unseen, and with it the Alias=, WantedBy= and Also=
// its denylist vetting checks. Hooks and install scriptlets that probe
// the terminal (tput, ncurses) stay quiet under it too. For the same
// reason SYSTEMD_COLORS is never set here: see CallEnv for the calls that
// get it on their own.
func ChildEnv() []string {
	return []string{"PATH=/usr/bin", "LC_ALL=C", "TERM=dumb"}
}

// SystemdColours is the one variable CallEnv adds (systemd documents 1 and
// true; "true" would do the same).
const SystemdColours = "SYSTEMD_COLORS=1"

// CallEnv is the environment of one call: ChildEnv, plus SYSTEMD_COLORS=1
// for the systemd tools whose output the helper only relays, as-is, never
// parses (task #133): systemd-tmpfiles --clean, journalctl --vacuum-time=...
// and systemctl disable --now. They have no --color option and write to a
// pipe, so this is their only switch. It is an allowlist of exact shapes
// (the argv builders in helper.go): every other call, systemctl show and
// cat above all (disable-unit's denylist vetting parses them; coloured,
// `systemctl cat` wraps [Install] in SGR and the check would fail open),
// keeps plain ChildEnv. TERM stays dumb either way.
func CallEnv(argv []string) []string {
	env := ChildEnv()
	if relayedSystemd(argv) {
		env = append(env, SystemdColours)
	}
	return env
}

func relayedSystemd(argv []string) bool {
	switch {
	case len(argv) == 2 && argv[0] == SystemdTmpfile:
		return argv[1] == "--clean"
	case len(argv) == 2 && argv[0] == Journalctl:
		return strings.HasPrefix(argv[1], "--vacuum-")
	case len(argv) >= 4 && argv[0] == Systemctl:
		return argv[1] == "disable" && argv[2] == "--now" && argv[3] == "--"
	}
	return false
}

// Runner executes commands. It is the seam tests replace: the real one is
// ExecRunner, the tests use a fake that records argv and never executes.
type Runner interface {
	// Query runs argv to completion and returns its stdout and stderr.
	// err is non-nil only when the command could not be run at all; a
	// non-zero exit is reported through exit (-1 if killed by a signal).
	Query(argv []string) (stdout, stderr string, exit int, err error)
	// Stream runs argv, calling line for each line of its output as it
	// arrives (stream is "out" or "err"), and returns its exit status.
	Stream(argv []string, line func(stream, text string)) (exit int, err error)
}

// ExecRunner is the real Runner: argv[0] must be absolute, the environment is
// exactly ChildEnv, the working directory is /, stdin is /dev/null, and the
// child runs in a new session (see command).
type ExecRunner struct{}

// maxCapture caps what Query buffers per stream.
const maxCapture = 32 << 20

// maxLine is the longest line Stream relays; longer ones are split.
const maxLine = 8192

func command(argv []string) (*exec.Cmd, error) {
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
		return nil, fmt.Errorf("refusing to run a non-absolute command: %q", argv)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = CallEnv(argv)
	cmd.Dir = "/"
	// A session of its own, so nothing aimed at the terminal that started
	// the helper reaches pacman mid-transaction: not a Ctrl-C, Ctrl-\ or
	// Ctrl-Z sent to the foreground process group, not the SIGHUP of a
	// closed terminal, not a kill of the helper's process group. The child
	// has no controlling terminal and needs none: stdin is /dev/null and
	// its output goes through pipes.
	//
	// This is the opposite of the TUI, whose steps must NOT get Setsid:
	// they run sudo, and a sudo ticket is keyed to the tty and session
	// (docs/HANDOVER.md). The helper is root already, through pkexec, and
	// runs no sudo, so it has no ticket to lose. Don't copy either choice
	// into the other program.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, nil
}

func exitStatus(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return -1, err
}

// Query implements Runner.
func (ExecRunner) Query(argv []string) (string, string, int, error) {
	cmd, err := command(argv)
	if err != nil {
		return "", "", -1, err
	}
	var out, errb capped
	cmd.Stdout, cmd.Stderr = &out, &errb
	exit, err := exitStatus(cmd.Run())
	return out.String(), errb.String(), exit, err
}

// Stream implements Runner.
func (ExecRunner) Stream(argv []string, line func(stream, text string)) (int, error) {
	cmd, err := command(argv)
	if err != nil {
		return -1, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scanLines(stdout, "out", line) }()
	go func() { defer wg.Done(); scanLines(stderr, "err", line) }()
	wg.Wait() // drain both pipes fully before Wait closes them
	return exitStatus(cmd.Wait())
}

// scanLines reads r to EOF, calling line per line. It always drains r, even
// past a scan error, so the child never blocks or dies on a full pipe.
func scanLines(r io.Reader, stream string, line func(stream, text string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 2*maxLine)
	sc.Split(SplitLines)
	for sc.Scan() {
		line(stream, sc.Text())
	}
	_, _ = io.Copy(io.Discard, r)
}

// SplitLines is a bufio.SplitFunc that ends a line at \n, \r\n or a lone \r
// (a progress-bar redraw), and cuts lines longer than maxLine bytes.
func SplitLines(data []byte, atEOF bool) (int, []byte, error) {
	limit := min(len(data), maxLine)
	if i := bytes.IndexAny(data[:limit], "\r\n"); i >= 0 {
		if data[i] == '\n' {
			return i + 1, data[:i], nil
		}
		if i+1 < len(data) {
			if data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
			return i + 1, data[:i], nil
		}
		if atEOF {
			return i + 1, data[:i], nil
		}
		return 0, nil, nil // a \r at the end: wait to see if \n follows
	}
	if len(data) >= maxLine {
		return maxLine, data[:maxLine], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// capped is a bytes.Buffer that silently stops growing at maxCapture.
type capped struct{ bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxCapture - c.Len(); room < len(p) {
		if room > 0 {
			c.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return c.Buffer.Write(p)
}
