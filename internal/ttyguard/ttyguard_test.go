package ttyguard

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// openPTY opens a fresh pty: its leader and its follower (not our
// controlling terminal), both closed when t ends.
func openPTY(t *testing.T) (leader, follower *os.File) {
	t.Helper()
	leader, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { leader.Close() })
	var unlock int32
	if err := ioctl(leader, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		t.Fatal(err)
	}
	var n uint32
	if err := ioctl(leader, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		t.Fatal(err)
	}
	follower, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { follower.Close() })
	return leader, follower
}

// opens runs a shell whose controlling terminal is follower (a session of
// its own on it, as a user's shell has: test code only) and reports what a
// child of it gets opening /dev/tty and the follower's own path, as a step's
// child would.
func opens(t *testing.T, follower *os.File) string {
	t.Helper()
	script := `( exec 3<>/dev/tty ) 2>/dev/null && printf 'devtty=opened ' || printf 'devtty=refused '
( exec 3<>"$1" ) 2>/dev/null && echo name=opened || echo name=refused`
	cmd := exec.Command("sh", "-c", script, "sh", follower.Name())
	cmd.Stdin = follower
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	return strings.TrimSpace(string(out))
}

const (
	refused = "devtty=refused name=refused"
	opened  = "devtty=opened name=opened"
)

// TestHoldRefusesNewOpens: while held, no new open of the terminal works,
// /dev/tty or by name; an fd opened before keeps reading; Pause lets opens
// through until resumed; Release undoes it for good.
func TestHoldRefusesNewOpens(t *testing.T) {
	leader, follower := openPTY(t)
	if got := opens(t, follower); got != opened {
		t.Fatalf("before Hold: %s", got)
	}
	g, err := Hold(follower)
	if err != nil {
		t.Fatal(err)
	}
	if on, err := Exclusive(follower); err != nil || !on {
		t.Fatalf("Exclusive = %v, %v", on, err)
	}
	if got := opens(t, follower); got != refused {
		t.Fatalf("held: %s", got)
	}
	// The fd the TUI reads from still works.
	if _, err := leader.Write([]byte("k\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if n, err := follower.Read(buf); err != nil || !strings.HasPrefix(string(buf[:n]), "k") {
		t.Fatalf("read %q, %v", buf[:n], err)
	}

	resume := g.Pause()
	inner := g.Pause()
	if got := opens(t, follower); got != opened {
		t.Fatalf("paused: %s", got)
	}
	inner()
	inner() // a second call is a no-op
	if got := opens(t, follower); got != opened {
		t.Fatalf("still paused once: %s", got)
	}
	resume()
	if got := opens(t, follower); got != refused {
		t.Fatalf("resumed: %s", got)
	}

	if err := g.Release(); err != nil {
		t.Fatal(err)
	}
	if on, _ := Exclusive(follower); on {
		t.Fatal("still exclusive after Release")
	}
	if got := opens(t, follower); got != opened {
		t.Fatalf("released: %s", got)
	}
	if err := g.Release(); err != nil {
		t.Fatalf("a second Release: %v", err)
	}
}

// TestReleaseWhilePaused: Release during a Pause leaves it off, and the
// late resume doesn't turn it back on.
func TestReleaseWhilePaused(t *testing.T) {
	_, follower := openPTY(t)
	g, _ := Hold(follower)
	resume := g.Pause()
	if err := g.Release(); err != nil {
		t.Fatal(err)
	}
	resume()
	if on, _ := Exclusive(follower); on {
		t.Fatal("exclusive again after a resume that came after Release")
	}
}

// TestHoldLeavesOthersExclusive: a terminal already exclusive is someone
// else's to undo: Release leaves it so.
func TestHoldLeavesOthersExclusive(t *testing.T) {
	_, follower := openPTY(t)
	if err := ioctl(follower, syscall.TIOCEXCL, nil); err != nil {
		t.Fatal(err)
	}
	g, err := Hold(follower)
	if err != nil {
		t.Fatal(err)
	}
	g.Pause()()
	if err := g.Release(); err != nil {
		t.Fatal(err)
	}
	if on, _ := Exclusive(follower); !on {
		t.Fatal("Release cleared an exclusivity Hold didn't set")
	}
	_ = ioctl(follower, syscall.TIOCNXCL, nil)
}

// TestHoldNotATerminal: a pipe, a file or nil is a Guard that does nothing.
func TestHoldNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	for _, f := range []*os.File{r, nil} {
		g, err := Hold(f)
		if err != nil {
			t.Fatal(err)
		}
		g.Pause()()
		if err := g.Release(); err != nil {
			t.Fatal(err)
		}
	}
	var g *Guard
	g.Pause()()
	if err := g.Release(); err != nil {
		t.Fatal(err)
	}
	if IsTerminal(r) {
		t.Fatal("a pipe is a terminal")
	}
}
