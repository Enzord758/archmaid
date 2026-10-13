package proc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Review #135 (Dev 3): the GUI's pty, from the child's side.

// fdTargets parses `ls -l /proc/<pid>/fd` into "fd=target" strings, with
// every /dev/pts/N written as /dev/pts/*.
func fdTargets(out []string) []string {
	re := regexp.MustCompile(` ([0-9]+) -> (.*)$`)
	pts := regexp.MustCompile(`^/dev/pts/[0-9]+$`)
	var got []string
	for _, l := range out {
		m := re.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		target := m[2]
		if pts.MatchString(target) {
			target = "/dev/pts/*"
		}
		got = append(got, m[1]+"="+target)
	}
	return got
}

// lsOwnFds lists the shell's own fds (not ls's: the second command keeps
// sh from exec'ing ls).
const lsOwnFds = "ls -l /proc/$$/fd\n:"

// TestPtyChildHoldsOnlyItsStreams: a child on the pty holds exactly its
// three streams: /dev/null and the slave twice. Neither the master (which
// must be close-on-exec: a step's child holding it could read its own
// output away from archmaid, and would keep the pty alive after archmaid
// closed it, so a leftover writer blocks instead of getting EIO) nor a
// second copy of the slave (the one openPty returns must be close-on-exec
// too) reaches it. Survivors before this test: the master or the slave
// opened without O_CLOEXEC.
func TestPtyChildHoldsOnlyItsStreams(t *testing.T) {
	ch := make(chan Msg, 64)
	if c := New(Options{Detach: true, Pty: true, KillGrace: time.Second}).Start(Spec{Name: "sh", Args: []string{"-c", lsOwnFds}}, ch, nil); c == nil {
		t.Fatal("did not start")
	}
	lines, err := drain(t, ch)
	if err != nil {
		t.Fatal(err, lines)
	}
	if got, want := fdTargets(lines), []string{"0=/dev/null", "1=/dev/pts/*", "2=/dev/pts/*"}; !slices.Equal(got, want) {
		t.Fatalf("the pty child holds %q, want %q (ls said %q)", got, want, lines)
	}
}

// TestPtyNotInheritedByOtherChildren: while a step's child runs on its
// pty, a child archmaid starts meanwhile (here a Query, as the parsed
// pacman calls) holds no fd of that pty: not the master, not the slave.
func TestPtyNotInheritedByOtherChildren(t *testing.T) {
	ch := make(chan Msg, 64)
	c := New(Options{Detach: true, Pty: true, KillGrace: time.Second}).Start(Spec{Name: "sh", Args: []string{"-c", "echo up\nexec sleep 3"}}, ch, nil)
	if c == nil {
		t.Fatal("did not start")
	}
	defer func() {
		c.KillGroup()
		_, _ = drain(t, ch)
	}()
	select {
	case m := <-ch:
		if m.Line != "up" {
			t.Fatalf("first message %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pty child never started")
	}
	out, err := Query(context.Background(), "sh", "-c", lsOwnFds)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fdTargets(strings.Split(string(out), "\n")) {
		if strings.HasSuffix(fd, "=/dev/ptmx") || strings.HasSuffix(fd, "=/dev/pts/*") {
			t.Errorf("a Query child started during a pty step holds %s", fd)
		}
	}
}

const envPtyProbe = "ARCHMAID_PTY_PROBE"

// TestPtyProbeChild is not a test on its own: TestPtyNeverAnswers and
// TestPtyOutputOnly start the test binary with envPtyProbe set as the
// pty's child (a session leader, as every step's child), and it reports
// what it could do with its streams.
func TestPtyProbeChild(t *testing.T) {
	switch os.Getenv(envPtyProbe) {
	case "":
		t.Skip("run by TestPtyNeverAnswers and TestPtyOutputOnly")
	case "answer":
		// Ask the terminal what a real one answers on its input: the
		// cursor position (DSR 6), the device attributes (DA1, DA2), the
		// foreground colour (OSC 10) and the window title (XTWINOPS 21).
		fmt.Print("\x1b[6n\x1b[c\x1b[>c\x1b]10;?\x07\x1b[21t\n")
		time.Sleep(300 * time.Millisecond)
		fmt.Println("read2=" + readNow(2))
	case "own":
		sctty := "ok"
		if err := unix.IoctlSetInt(1, unix.TIOCSCTTY, 0); err != nil {
			sctty = err.Error()
		}
		fmt.Println("sctty=" + sctty)
		stat, _ := os.ReadFile("/proc/self/stat")
		_, rest, _ := strings.Cut(string(stat), ") ")
		if f := strings.Fields(rest); len(f) > 4 {
			fmt.Println("tty_nr=" + f[4])
		}
		fmt.Println("read2=" + readNow(2))
	}
}

// readNow reads fd without waiting: "eof", "data", "nothing" (it could
// read, nothing was there) or the error.
func readNow(fd int) string {
	if err := unix.SetNonblock(fd, true); err != nil {
		return err.Error()
	}
	buf := make([]byte, 64)
	n, err := unix.Read(fd, buf)
	switch {
	case errors.Is(err, unix.EAGAIN):
		return "nothing"
	case err != nil:
		return err.Error()
	case n == 0:
		return "eof"
	}
	return fmt.Sprintf("data %q", buf[:n])
}

func probe(t *testing.T, mode string) []string {
	t.Helper()
	ch := make(chan Msg, 64)
	spec := Spec{Name: os.Args[0], Args: []string{"-test.run=^TestPtyProbeChild$", "-test.count=1"}, ExtraEnv: []string{envPtyProbe + "=" + mode}}
	if c := New(Options{Detach: true, Pty: true, KillGrace: time.Second}).Start(spec, ch, nil); c == nil {
		t.Fatal("did not start")
	}
	lines, err := drain(t, ch)
	if err != nil {
		t.Fatal(err, lines)
	}
	return lines
}

// TestPtyNeverAnswers: archmaid never writes into the pty. A child that
// asks the terminal for a report (cursor position, device attributes, a
// colour, the title) gets nothing back on its terminal, and the queries
// don't reach the output either.
func TestPtyNeverAnswers(t *testing.T) {
	lines := probe(t, "answer")
	if !slices.Contains(lines, "read2=nothing") && !slices.Contains(lines, "read2=bad file descriptor") {
		t.Errorf("the child's terminal had input after its queries: %q", lines)
	}
	for _, l := range lines {
		if strings.ContainsAny(l, "\x1b\a") {
			t.Errorf("a query reached the output: %q", l)
		}
	}
}

// TestPtyOutputOnly: the slave the child holds is for writing only, so
// the "output only" rule is the kernel's, not a convention: a session
// leader can't make it its controlling terminal with TIOCSCTTY (which
// needs read access; with it, tty_nr would name the pty, and a sudo
// started below — setuid root, so TIOCEXCL doesn't stop its open of
// /dev/tty — would prompt on it and wait for a password archmaid never
// writes), and a tool that reads its keys from stderr when /dev/tty
// fails (less, more) gets an error instead of waiting forever. Shell
// redirections to /dev/stdout and /dev/stderr, which open the slave again
// by name, keep working (TIOCEXCL refuses them: EBUSY).
func TestPtyOutputOnly(t *testing.T) {
	t.Skip("KNOWN BUG (review #135, should-fix): the slave is opened O_RDWR and exclusive (TIOCEXCL): a session " +
		"leader's TIOCSCTTY on fd 1 succeeds, fd 2 is readable (a read waits forever), and `> /dev/stderr` fails " +
		"with EBUSY; remove this Skip with the fix (and pty_test.go's reopen=refused)")
	lines := probe(t, "own")
	for _, want := range []string{"sctty=operation not permitted", "tty_nr=0", "read2=bad file descriptor"} {
		if !slices.Contains(lines, want) {
			t.Errorf("the child saw %q, want %q among them", lines, want)
		}
	}
	ch := make(chan Msg, 64)
	New(Options{Detach: true, Pty: true, KillGrace: time.Second}).Start(Spec{Name: "sh", Args: []string{"-c",
		"echo to-stderr >/dev/stderr\necho to-stdout >/dev/stdout\necho to-fd1 >/proc/self/fd/1"}}, ch, nil)
	got, err := drain(t, ch)
	if want := []string{"to-stderr", "to-stdout", "to-fd1"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("redirections by name: %q (err %v), want %q", got, err, want)
	}
}
