package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege/privtest"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// A sudo that a step's program starts by name (topgrade's, yay's,
// makepkg's) must ask through the TUI's askpass popup, never on the TUI's
// terminal (task #153, docs/HANDOVER.md "Nothing may prompt on the raw
// tty"). The real TUI runs in a pty as the session leader (as
// TestStepCannotReadTheTerminal), with one NeedsPriv step: a script that
// runs `sudo pacman -Syu <tag>` by name, the way yay does. On PATH, behind
// the TUI's own sudo wrapper, is a decoy `sudo` that stands for the real
// one reached by name: it logs that it ran and tries the terminal, as sudo
// would to prompt. sysbin.Sudo is a shim outside PATH (as /usr/bin/sudo is
// what the wrapper execs) that asks $SUDO_ASKPASS when given -A and logs
// anything else as a prompt on the terminal. su is a shim that takes
// ttySudoPassword only: every popup answer goes through Request.Answer's su
// check first.

const (
	// envTTYSudo is the scratch dir the sudo child's shims and logs live in.
	envTTYSudo = "ARCHMAID_TTY_SUDO"
	// ttySudoPassword is the one password the su shim accepts.
	ttySudoPassword = "hunter2-tty-sudo"
	// ttySudoMenu is the child's one action.
	ttySudoMenu = "Sudo by name"
)

// ttySudoTool is the step: two sudo calls by name, as yay makes them, each
// followed by its exit status.
const ttySudoTool = `#!/bin/sh
sudo pacman -Syu one
echo "sudo one: $?"
sudo pacman -Syu two
echo "sudo two: $?"
`

// TestTTYSudoChild is the TUI the sudo-by-name tests run; on its own it
// does nothing.
func TestTTYSudoChild(t *testing.T) {
	dir := os.Getenv(envTTYSudo)
	if dir == "" {
		t.Skip("run by TestTUISudoByNameAsksInThePopup")
	}
	// The real sudo: outside PATH, the absolute path the wrapper execs.
	testenv.UseSysbin(t, &sysbin.Sudo, `printf '%s\n' "$*" >> `+shQuote(dir)+`/sudo.log
case "$1" in
-n) [ "$2" = -v ] && [ -e `+shQuote(dir)+`/noticket ] && exit 1; exit 0 ;;
-S) IFS= read -r pw; exit 0 ;;
-A) shift; for tag; do :; done
	"$SUDO_ASKPASS" "[sudo] password for $tag: " > `+shQuote(dir)+`/answer.$tag
	st=$?
	echo $st > `+shQuote(dir)+`/status.$tag
	exit $st ;;
esac
printf 'prompted on the terminal: %s\n' "$*" >> `+shQuote(dir)+`/tty.log
exit 1`)
	t.Setenv("PATH", filepath.Join(dir, "shims")+string(os.PathListSeparator)+os.Getenv("PATH"))

	// The relay's parent is the sudo shim: it looks like the real sudo
	// to a user (setuid root, its exe unreadable). Before the Server
	// starts: Listen takes its /proc reader from here.
	privtest.FakeSystem(t, os.Getuid, "", func(pid int) (proc.Info, error) {
		p, err := proc.Read(pid)
		if err == nil && p.Comm == "sudo" {
			p.Exe, p.EUID = "", 0
		}
		return p, err
	})
	rt, err := os.MkdirTemp("", "apty") // short: a socket path has ~108 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(rt) })
	t.Setenv("XDG_RUNTIME_DIR", rt)

	old := actions.Menu
	defer func() { actions.Menu = old }()
	actions.Menu = []actions.Action{{ID: "sudo-by-name", Label: ttySudoMenu, Steps: func(actions.Env) []steps.Step {
		return []steps.Step{{ID: "tool", Title: "Running a tool that calls sudo…", LogLabel: "tool",
			Argv: []string{filepath.Join(dir, "tool")}, NeedsPriv: true}}
	}}}
	if err := DisableCoreDumps(); err != nil {
		t.Fatal(err)
	}
	Configure(false, "")
	srv := StartAskpass()
	if srv == nil {
		t.Fatal("the askpass Server didn't start")
	}
	deps := driverDeps(srv.Requests())
	deps.Faillock = func() engine.FaillockRead { return engine.FaillockRead{Deny: 3} } // never the real tally
	drv := driver.New(engine.New(engine.Options{Env: ActionEnv()}), deps)
	err = Run(drv)
	// main's exit path.
	drv.Close()
	Finish(os.Stderr)
	DropSudoTicket(os.Stderr)
	StopAskpass(srv)
	if err != nil && !errors.Is(err, tea.ErrInterrupted) {
		t.Fatal(err)
	}
}

// ttySudoRig is the sudo child's scratch dir with its shims.
type ttySudoRig struct {
	t   *testing.T
	dir string
}

func newTTYSudoRig(t *testing.T, noTicket bool) ttySudoRig {
	t.Helper()
	r := ttySudoRig{t: t, dir: t.TempDir()}
	shims := filepath.Join(r.dir, "shims")
	if err := os.Mkdir(shims, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"tool": ttySudoTool,
		// The sudo a step finds by name without archmaid's wrapper: it
		// would prompt on the terminal (real sudo is setuid root, so the
		// TUI's exclusive hold can't stop it; this one only tries).
		"shims/sudo": "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shQuote(r.dir) + "/decoy.log\n" +
			"if ( exec 3<>/dev/tty ) 2>/dev/null; then echo opened >> " + shQuote(r.dir) + "/tty.log; " +
			"else echo refused >> " + shQuote(r.dir) + "/decoy.log; fi\nexit 1\n",
		"shims/su": "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shQuote(r.dir) + "/su.log\n" +
			"IFS= read -r pw\n[ \"$pw\" = " + shQuote(ttySudoPassword) + " ]\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(r.dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if noTicket {
		if err := os.WriteFile(filepath.Join(r.dir, "noticket"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r ttySudoRig) read(name string) string {
	b, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		r.t.Fatal(err)
	}
	return string(b)
}

func (r ttySudoRig) exists(name string) bool {
	_, err := os.Stat(filepath.Join(r.dir, name))
	return err == nil
}

// TestTUISudoByNameAsksInThePopup: with a terminal present, a step's
// program that runs `sudo` by name reaches the TUI's askpass popup (each
// call its own popup, naming the program that ran sudo), the answer goes
// through su first and then to sudo, and nothing reaches the terminal: the
// decoy sudo on PATH never runs and nothing prompts on, writes to or reads
// from the tty. With a ticket sudo can't reuse (timestamp_timeout=0) the
// run goes on after the up-front password (it used to be refused), shows
// its one note, and asks once per sudo call.
func TestTUISudoByNameAsksInThePopup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		noTicket bool
	}{{"ticket", false}, {"timestamp_timeout=0", true}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTTYSudoRig(t, tc.noTicket)
			c := &tuiChild{t: t, scr: &ptyScreen{}, exited: make(chan struct{})}
			c.leader, c.follower = ptyPair(t)
			c.start("TestTTYSudoChild", ttySudoMenu, envTTYSudo+"="+r.dir)
			scr := c.scr
			su := 0 // su checks so far

			c.key("\r")
			if tc.noTicket {
				// The up-front password: su, sudo -S -v, then the reuse check
				// fails; the run goes on with its note.
				waitFor(t, scr, "the up-front password prompt", func() bool { return scr.has("sudo access required") })
				c.key(ttySudoPassword + "\r")
				su++
				waitFor(t, scr, "the run's note", func() bool { return scr.has("keep your password between prompts") })
			}
			for _, tag := range []string{"one", "two"} {
				// Each sudo passes a prompt of its own (sudo -p), which
				// the popup shows: the popup for this call is open.
				waitFor(t, scr, "the askpass popup for sudo "+tag, func() bool {
					return scr.has("password for "+tag) || r.exists("decoy.log") || r.exists("tty.log")
				})
				if r.exists("decoy.log") || r.exists("tty.log") {
					t.Fatalf("sudo %s went to the terminal: decoy %q, tty %q, sudo %q; screen:\n%q",
						tag, r.read("decoy.log"), r.read("tty.log"), r.read("sudo.log"), scr.String())
				}
				c.key(ttySudoPassword + "\r")
				su++
				waitFor(t, scr, "sudo "+tag+"'s answer", func() bool { return r.exists("status." + tag) })
				if got := r.read("answer." + tag); got != ttySudoPassword+"\n" {
					t.Fatalf("sudo %s got %q from its askpass, want the password", tag, got)
				}
				if got := strings.Count(r.read("su.log"), "\n"); got != su {
					t.Fatalf("su ran %d times by sudo %s's answer, want %d (every answer su-checked first)", got, tag, su)
				}
			}
			waitFor(t, scr, "the summary", func() bool { return scr.has("Cleanup completed successfully") })
			c.quit()
			if c.waitErr != nil {
				t.Errorf("the TUI exited with %v; screen:\n%q", c.waitErr, scr.String())
			}

			want := "-A pacman -Syu one\n-A pacman -Syu two\n"
			if tc.noTicket {
				want = "-n -v\n-S -v\n-n -v\n" + want // the probe, priming, the reuse check
			} else {
				want = "-n -v\n" + want // the probe: cached
			}
			if got := r.read("sudo.log"); !strings.HasPrefix(got, want) || !strings.HasSuffix(got, "-n -k\n") {
				t.Errorf("the real sudo ran\n%s\nwant it to start with\n%sand end with the ticket drop (-n -k)", got, want)
			}
			if got := r.read("decoy.log") + r.read("tty.log"); got != "" {
				t.Errorf("a sudo reached the terminal: %q", got)
			}
		})
	}
}
