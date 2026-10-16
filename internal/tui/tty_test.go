package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/ttyguard"
)

// A step's child that opens /dev/tty must not read the TUI's keys (the live
// VM run: topgrade's `git pull` over HTTPS asked for a username on
// /dev/tty, keystrokes alternated between it and Bubble Tea, and the run
// couldn't be aborted). The real TUI (Run) runs in a pty of its own, as in a
// terminal: this test binary again, as a session leader on the pty (test
// code only; product code never sets Setsid). Its one action runs a shell
// that tries to open /dev/tty and read from it.

const envTTYChild = "ARCHMAID_TTY_CHILD" // the marker file the probe writes

// envTTYShims is a dir the child puts first on PATH (its sudo shim).
const envTTYShims = "ARCHMAID_TTY_SHIMS"

// ttyProbe is the step's child: it records whether it could open the
// terminal, and what it read there, then waits to be aborted. (A failed
// redirection on exec would end the shell, so the open is a subshell's.)
const ttyProbe = `if ( exec 3</dev/tty ) 2>/dev/null; then
	echo opened >"$1"
	IFS= read -r line </dev/tty
	echo "read:$line" >>"$1"
else
	echo refused >"$1"
fi
echo probe waiting
exec sleep 30`

// TestTTYChild is the TUI the pty test runs; on its own it does nothing.
func TestTTYChild(t *testing.T) {
	marker := os.Getenv(envTTYChild)
	if marker == "" {
		t.Skip("run by TestStepCannotReadTheTerminal")
	}
	if shims := os.Getenv(envTTYShims); shims != "" {
		t.Setenv("PATH", shims+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	old := actions.Menu
	defer func() { actions.Menu = old }()
	actions.Menu = []actions.Action{{ID: "tty", Label: "TTY probe", Steps: func(actions.Env) []steps.Step {
		return []steps.Step{{ID: "probe", Title: "Probing the terminal…", LogLabel: "probe",
			Argv: []string{"sh", "-c", ttyProbe, "sh", marker}}}
	}}}
	// As main: not dumpable from here on, so the terminal hold and the run
	// are tested the way the binary runs them.
	if err := DisableCoreDumps(); err != nil {
		t.Fatal(err)
	}
	Configure(false, "")
	drv := NewDriver(engine.New(engine.Options{Env: ActionEnv()}), nil)
	err := Run(drv)
	// main's exit path, whatever ended the program.
	drv.Close()
	Finish(os.Stderr)
	DropSudoTicket(os.Stderr)
	if err != nil && !errors.Is(err, tea.ErrInterrupted) {
		t.Fatal(err)
	}
}

// ptyPair opens a pty: its leader, and its follower (not our terminal).
func ptyPair(t *testing.T) (leader, follower *os.File) {
	t.Helper()
	leader, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { leader.Close() })
	ioctl := func(req uintptr, arg unsafe.Pointer) {
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, leader.Fd(), req, uintptr(arg)); e != 0 {
			t.Fatal(e)
		}
	}
	var unlock int32
	ioctl(syscall.TIOCSPTLCK, unsafe.Pointer(&unlock))
	var n uint32
	ioctl(syscall.TIOCGPTN, unsafe.Pointer(&n))
	ws := struct{ row, col, x, y uint16 }{30, 100, 0, 0}
	ioctl(syscall.TIOCSWINSZ, unsafe.Pointer(&ws))
	follower, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { follower.Close() })
	return leader, follower
}

// ptyScreen collects what the TUI drew.
type ptyScreen struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *ptyScreen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *ptyScreen) has(sub string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Contains(s.buf.String(), sub)
}

func (s *ptyScreen) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// waitFor polls cond for up to 10 s; on a timeout it shows what was drawn.
func waitFor(t *testing.T, scr *ptyScreen, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s; screen:\n%q", what, scr.String())
}

// tuiChild is the real TUI (TestTTYChild) running as the session leader of
// a pty, with a sudo shim first on PATH that logs its argv to sudoLog.
type tuiChild struct {
	t                *testing.T
	leader, follower *os.File
	cmd              *exec.Cmd
	scr              *ptyScreen
	exited           chan struct{}
	waitErr          error
	marker, sudoLog  string
}

// startTUIChild starts the TUI child and waits for its menu.
func startTUIChild(t *testing.T) *tuiChild {
	t.Helper()
	c := &tuiChild{t: t, scr: &ptyScreen{}, exited: make(chan struct{})}
	c.leader, c.follower = ptyPair(t)
	dir := t.TempDir()
	c.marker, c.sudoLog = filepath.Join(dir, "probe"), filepath.Join(dir, "sudo.log")
	shims := filepath.Join(dir, "bin")
	if err := os.Mkdir(shims, 0o755); err != nil {
		t.Fatal(err)
	}
	sudo := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + c.sudoLog + "'\n"
	if err := os.WriteFile(filepath.Join(shims, "sudo"), []byte(sudo), 0o755); err != nil {
		t.Fatal(err)
	}
	// The child puts the shims first on PATH itself: its own testenv puts
	// the traps first when it starts.
	c.start("TestTTYChild", "TTY probe", envTTYChild+"="+c.marker, envTTYShims+"="+shims)
	return c
}

// start runs the test named child (a TUI under Run) as the session leader
// of c's pty, with env added to desktopEnv, and waits for its menu, which
// shows menu.
func (c *tuiChild) start(child, menu string, env ...string) {
	t := c.t
	t.Helper()
	c.cmd = exec.Command(os.Args[0], "-test.run=^"+child+"$", "-test.count=1")
	c.cmd.Env = append(append(desktopEnv(), env...), "TERM=xterm-256color")
	c.cmd.Stdin, c.cmd.Stdout, c.cmd.Stderr = c.follower, c.follower, c.follower
	c.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { c.waitErr = c.cmd.Wait(); close(c.exited) }()
	t.Cleanup(func() {
		// Whatever happens, nothing of the TUI's session outlives the test.
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-c.exited:
		case <-time.After(5 * time.Second):
		}
	})
	go func() { _, _ = copyScreen(c.scr, c.leader) }()
	// termenv asks the terminal for its background colour and the cursor
	// (OSC 11, DSR) and waits up to 5 s for answers: answer as a terminal.
	waitFor(t, c.scr, "the terminal queries", func() bool { return c.scr.has("\x1b[6n") })
	c.key("\x1b]11;rgb:0000/0000/0000\x1b\\\x1b[1;1R")
	waitFor(t, c.scr, "the menu", func() bool { return c.scr.has(menu) })
}

func (c *tuiChild) key(k string) {
	if _, err := c.leader.Write([]byte(k)); err != nil {
		c.t.Fatal(err)
	}
}

// quit presses q until the TUI exits.
func (c *tuiChild) quit() {
	c.t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
		c.key("q")
		select {
		case <-c.exited:
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
	c.t.Fatalf("the TUI never quit; screen:\n%q", c.scr.String())
}

// awaitExit waits for the TUI to exit by itself.
func (c *tuiChild) awaitExit(why string) {
	c.t.Helper()
	select {
	case <-c.exited:
	case <-time.After(15 * time.Second):
		c.t.Fatalf("the TUI never exited after %s; screen:\n%q", why, c.scr.String())
	}
}

// TestStepCannotReadTheTerminal: under the real TUI in a terminal, a step's
// child can't open the terminal (it is refused, as with no terminal at all),
// every key still reaches the TUI (ctrl+c opens the abort question, y
// aborts, q quits), and the terminal is released once the TUI exits.
func TestStepCannotReadTheTerminal(t *testing.T) {
	c := startTUIChild(t)
	scr, marker := c.scr, c.marker
	c.key("\r")
	waitFor(t, scr, "the probe", func() bool { _, err := os.Stat(marker); return err == nil })
	waitFor(t, scr, "the probe's verdict", func() bool { b, _ := os.ReadFile(marker); return len(b) > 0 })
	c.key("hello\r") // what a user might type: it must not reach the probe
	b, _ := os.ReadFile(marker)
	if got := strings.TrimSpace(string(b)); got != "refused" {
		t.Errorf("the step's child opened the TUI's terminal: %q", got)
	}

	c.key("\x03")
	waitFor(t, scr, "the abort question", func() bool { return scr.has("Abort the running step") })
	c.key("y")
	waitFor(t, scr, "the end of the run", func() bool {
		scr.mu.Lock()
		defer scr.mu.Unlock()
		s := scr.buf.String()
		return strings.Count(s, "TTY probe") > 1 && strings.LastIndex(s, "TTY probe") > strings.LastIndex(s, "Abort the running step")
	})
	c.quit()
	if c.waitErr != nil {
		t.Errorf("the TUI exited with %v; screen:\n%q", c.waitErr, scr.String())
	}
	if b, _ := os.ReadFile(marker); strings.Contains(string(b), "read:") {
		t.Errorf("the step's child read keys: %q", b)
	}
	if on, err := ttyguard.Exclusive(c.follower); err != nil || on {
		t.Errorf("the terminal is still exclusive after the TUI exited: %v, %v", on, err)
	}
}

// TestTUIDropsTicketOnExit: however the TUI ends (q, SIGTERM, SIGINT,
// SIGHUP), main's exit path runs and drops the sudo ticket with exactly
// `sudo -k`, once (the owner's decision M1).
func TestTUIDropsTicketOnExit(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  syscall.Signal
	}{{"q", 0}, {"SIGTERM", syscall.SIGTERM}, {"SIGINT", syscall.SIGINT}, {"SIGHUP", syscall.SIGHUP}} {
		t.Run(tc.name, func(t *testing.T) {
			c := startTUIChild(t)
			if tc.sig == 0 {
				c.quit()
			} else {
				if err := c.cmd.Process.Signal(tc.sig); err != nil {
					t.Fatal(err)
				}
				c.awaitExit(tc.name)
			}
			b, _ := os.ReadFile(c.sudoLog)
			if got := strings.TrimSpace(string(b)); got != "-k" {
				t.Errorf("sudo ran %q on exit, want exactly \"-k\" once; screen:\n%q", got, c.scr.String())
			}
		})
	}
}

// desktopEnv is this process's environment without what makes termenv treat
// a terminal as a pipe: with CI set (GitHub Actions sets CI=true) it skips
// its background-colour and cursor queries, which the test answers as a
// terminal would. The child runs in a real pty, as on the owner's desktop.
func desktopEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); k != "CI" && k != "NO_COLOR" {
			env = append(env, kv)
		}
	}
	return env
}

// copyScreen reads r into w until r fails (the pty closes).
func copyScreen(w *ptyScreen, r *os.File) (int64, error) {
	buf := make([]byte, 4096)
	var n int64
	for {
		m, err := r.Read(buf)
		if m > 0 {
			w.Write(buf[:m])
			n += int64(m)
		}
		if err != nil {
			return n, err
		}
	}
}

// TestEditorGetsTheTerminal: the editor is handed the terminal on purpose,
// so while it runs the hold is paused (a tcell editor opens /dev/tty), and
// it is back once the editor exits. A stream the command already names is
// kept.
func TestEditorGetsTheTerminal(t *testing.T) {
	_, follower := ptyPair(t)
	g, err := ttyguard.Hold(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Release()
	termGuard.Store(g)
	defer termGuard.Store(nil)

	out := filepath.Join(t.TempDir(), "out")
	cmd := exec.Command("sh", "-c", `( exec 3</dev/tty ) 2>/dev/null && echo opened >"$1" || echo refused >"$1"`, "sh", out)
	cmd.Stdin = follower
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0} // the terminal as its own
	h := handedTerminal{cmd}
	h.SetStdin(strings.NewReader("not this"))
	h.SetStdout(io.Discard)
	h.SetStderr(io.Discard)
	if cmd.Stdin != follower || cmd.Stdout != io.Discard || cmd.Stderr != io.Discard {
		t.Fatalf("stdin %v stdout %v stderr %v", cmd.Stdin, cmd.Stdout, cmd.Stderr)
	}
	if err := h.Run(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); strings.TrimSpace(string(b)) != "opened" {
		t.Errorf("the editor couldn't open the terminal: %q", b)
	}
	if on, _ := ttyguard.Exclusive(follower); !on {
		t.Error("the terminal wasn't held again after the editor")
	}
}
