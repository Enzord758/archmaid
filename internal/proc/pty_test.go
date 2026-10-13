package proc

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// ptyFacts is what a child under Options.Pty sees, one fact per line. The
// pty is its output, never its controlling terminal: stdin is not a tty,
// /dev/tty can't be opened, tty_nr (the 5th field after the command name
// in /proc/<pid>/stat, what sudo reads for its tty) is 0. Its own
// /dev/pts/N opens again by name for writing (reopen=ok), as every
// `>/dev/stderr` does: the slave is write-only and mode 0200, and a
// write-only open never makes a terminal the controlling one
// (TestPtyReopenByNameNeverControls).
const ptyFacts = `
[ -t 0 ] && echo fd0=tty || echo fd0=notty
[ -t 1 ] && echo fd1=tty || echo fd1=notty
[ -t 2 ] && echo fd2=tty >&2 || echo fd2=notty >&2
( : </dev/tty ) 2>/dev/null && echo devtty=open || echo devtty=fails
echo "tty_nr=$(sed 's/.*) //' /proc/$$/stat | cut -d' ' -f5)"
echo "session_leader=$([ "$(sed 's/.*) //' /proc/$$/stat | cut -d' ' -f4)" = $$ ] && echo yes || echo no)"
pts=$(readlink /proc/$$/fd/1)
case $pts in /dev/pts/*) echo stdout=pts ;; *) echo "stdout=$pts" ;; esac
case $pts in /dev/pts/*) ( : >"$pts" ) 2>/dev/null && echo reopen=ok || echo reopen=refused ;; *) echo reopen=n/a ;; esac
echo "size=$(stty size <&2)"
echo "TERM=$TERM"
`

// TestPtyIsOutputNotControllingTerminal (#129): the GUI's pty facts.
func TestPtyIsOutputNotControllingTerminal(t *testing.T) {
	t.Setenv("TERM", "dumb") // the child's TERM is the pty's, whatever archmaid's
	ch := make(chan Msg, 64)
	c := New(Options{Detach: true, Pty: true, Cols: 132, KillGrace: time.Second}).Start(Spec{Name: "sh", Args: []string{"-c", ptyFacts}}, ch, nil)
	if c == nil {
		t.Fatal("did not start")
	}
	lines, err := drain(t, ch)
	if err != nil {
		t.Fatal(err, lines)
	}
	want := []string{"fd0=notty", "fd1=tty", "fd2=tty", "devtty=fails", "tty_nr=0", "session_leader=yes",
		"stdout=pts", "reopen=ok", "size=50 132", "TERM=xterm-256color"}
	if !slices.Equal(lines, want) {
		t.Fatalf("the child saw\n%q\nwant\n%q", lines, want)
	}
}

// TestPtyPagerReadFailsAtOnce (review #135): a tool that waits for a key
// on fd 2 when /dev/tty fails (less, or git and journalctl paging into
// it) gets EBADF at once: the slave is write-only. Readable, the read
// waited forever (archmaid never writes into the pty) and so did the step.
func TestPtyPagerReadFailsAtOnce(t *testing.T) {
	ch := make(chan Msg, 64)
	c := New(Options{Detach: true, Pty: true, KillGrace: time.Second}).Start(probeSpec("pager"), ch, nil)
	if c == nil {
		t.Fatal("did not start")
	}
	var lines []string
	timeout := time.After(3 * time.Second)
	for {
		select {
		case m := <-ch:
			if !m.Done {
				lines = append(lines, m.Line)
				continue
			}
			if !slices.Contains(lines, "read2=bad file descriptor") {
				t.Fatalf("the child's read of fd 2: %q (err %v), want EBADF", lines, m.Err)
			}
			return
		case <-timeout:
			c.KillGroup()
			_, _ = drain(t, ch)
			t.Fatalf("the child's read of fd 2 still waits after 3 s (it said %q)", lines)
		}
	}
}

// TestPtyReopenByNameNeverControls (review #135): a session leader with
// no controlling terminal that opens its pty again by name can't take it:
// for reading, the slave's mode (0200) refuses the open, and a write-only
// open, even without O_NOCTTY, never makes it the controlling terminal
// (the kernel wants read access for that). /dev/tty still fails.
func TestPtyReopenByNameNeverControls(t *testing.T) {
	if unix.Geteuid() == 0 {
		t.Skip("root opens a mode 0200 slave for reading: rdwr=permission denied holds for others only")
	}
	lines := probe(t, "reopen")
	// (The test binary adds its own lines: PASS, and a coverage figure
	// under -cover.)
	for _, want := range []string{"rdwr=permission denied", "wronly=ok", "tty_nr=0", "devtty=no such device or address"} {
		if !slices.Contains(lines, want) {
			t.Errorf("the child saw %q, want %q among them", lines, want)
		}
	}
}

// TestPtyOutputLines: a child on the pty gets its colour and its redraws
// through as final lines: CRLF ends a line, a progress bar redraws its own
// line, a cursor-up rewrite replaces what was sent, and every line written
// before a quick exit arrives (the pty's EIO comes after the data).
func TestPtyOutputLines(t *testing.T) {
	script := `printf 'a\nb\n'; sleep 0.2; printf '\033[1Ax\033[K\n\033[31mred\033[0m\n'
for i in 1 2 3; do printf '\r[%s] %d%%' "$i" "$((i*33))"; sleep 0.06; done; printf '\r[done] 100%%\033[K\n'
seq 1 3000`
	ch := make(chan Msg, 64)
	New(Options{Detach: true, Pty: true, KillGrace: time.Second}).Start(Spec{Name: "sh", Args: []string{"-c", script}}, ch, nil)
	var msgs []Msg
	var lines []string
	for msg := range msgsUntilDone(t, ch) {
		msgs = append(msgs, msg)
		if !msg.Done {
			lines = append(lines[:len(lines)-msg.Replace], msg.Line)
		}
	}
	if done := msgs[len(msgs)-1]; done.Err != nil {
		t.Fatal(done.Err)
	}
	want := []string{"a", "x", "\x1b[31mred\x1b[0m", "[done] 100%"}
	for i := 1; i <= 3000; i++ {
		want = append(want, strconv.Itoa(i))
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("final lines %q…, want %q…", head(lines), head(want))
	}
	if !slices.ContainsFunc(msgs, func(m Msg) bool { return m.Replace > 0 }) {
		t.Fatal("no Msg replaced a line: the redraws went out as new lines")
	}
}

func head(s []string) []string { return s[:min(len(s), 8)] }

// msgsUntilDone yields ch's messages through the done, failing t after 10 s.
func msgsUntilDone(t *testing.T, ch <-chan Msg) func(func(Msg) bool) {
	return func(yield func(Msg) bool) {
		timeout := time.After(10 * time.Second)
		for {
			select {
			case m := <-ch:
				if !yield(m) || m.Done {
					return
				}
			case <-timeout:
				t.Fatal("no done after 10 s")
				return
			}
		}
	}
}

// TestPtyWaitDelay: something the child left running holds the pty's
// slave as it would hold a pipe; WaitDelay bounds the read after the child
// exits, and the step is done (a success).
func TestPtyWaitDelay(t *testing.T) {
	ch := make(chan Msg, 16)
	c := New(Options{Detach: true, Pty: true, WaitDelay: 200 * time.Millisecond, KillGrace: time.Second}).
		Start(Spec{Name: "sh", Args: []string{"-c", "sleep 30 & echo started"}}, ch, nil)
	if c == nil {
		t.Fatal("did not start")
	}
	defer func() {
		c.KillGroup()
		deadline := time.Now().Add(5 * time.Second)
		for groupAlive(c.Pid) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}()
	start := time.Now()
	lines, err := drain(t, ch)
	if err != nil || !slices.Equal(lines, []string{"started"}) {
		t.Fatalf("lines %q, err %v", lines, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("done after %v: the background holder kept the read open", d)
	}
}

// TestRedrawCoalesced (#129): a child that redraws one line as fast as it
// can costs at most one redraw per Options.Redraw, on a pty and on a pipe,
// and its final text arrives.
func TestRedrawCoalesced(t *testing.T) {
	const every = 100 * time.Millisecond
	script := `i=0; end=$(( $(date +%s) + 2 )); while [ "$(date +%s)" -lt "$end" ]; do i=$((i+1)); printf '\r%08d' "$i"; done; printf '\rfinal\033[K\n'`
	for _, opts := range []Options{{Redraw: every}, {Detach: true, Pty: true, Redraw: every}} {
		t.Run(fmt.Sprintf("pty=%v", opts.Pty), func(t *testing.T) {
			opts.KillGrace = time.Second
			ch := make(chan Msg, 64)
			start := time.Now()
			New(opts).Start(Spec{Name: "sh", Args: []string{"-c", script}}, ch, nil)
			var lines []string
			redraws, msgs := 0, 0
			for m := range msgsUntilDone(t, ch) {
				if m.Done {
					if m.Err != nil {
						t.Fatal(m.Err)
					}
					break
				}
				msgs++
				if m.Replace > 0 {
					redraws++
				}
				lines = append(lines[:len(lines)-m.Replace], m.Line)
			}
			elapsed := time.Since(start)
			if !slices.Equal(lines, []string{"final"}) {
				t.Fatalf("final lines %q", lines)
			}
			if limit := int(elapsed/every) + 2; redraws > limit || msgs > limit+1 {
				t.Fatalf("%d redraws, %d messages in %v: more than one per %v", redraws, msgs, elapsed, every)
			}
			if redraws < 3 {
				t.Fatalf("only %d redraws in %v: the line wasn't shown while it changed", redraws, elapsed)
			}
		})
	}
}

// TestNextMergesReplace: Next folds a Msg's Replace into its batch.
func TestNextMergesReplace(t *testing.T) {
	ch := make(chan Msg, 16)
	for _, m := range []Msg{{Line: "a"}, {Line: "b"}, {Line: "B", Replace: 1}, {Line: "x", Replace: 4}, {Line: "y"}} {
		ch <- m
	}
	b, ok := Next(ch, nil, 16)
	if !ok || b.Replace != 2 || !slices.Equal(b.Lines, []string{"x", "y"}) {
		t.Fatalf("batch %+v", b)
	}
	ch <- Msg{Line: "z", Replace: 1}
	ch <- Msg{Line: "w"}
	b, _ = Next(ch, nil, 16)
	if b.Replace != 1 || strings.Join(b.Lines, ",") != "z,w" {
		t.Fatalf("batch %+v", b)
	}
}

// TestPtyPeerFallbackOpensByName (review #141): where TIOCGPTPEER fails
// (a kernel before 4.13), peer opens /dev/pts/N by name, still write-only
// and O_NOCTTY (a read-write open by a session leader could make the pty
// its controlling terminal), and the slave is as with the ioctl.
func TestPtyPeerFallbackOpensByName(t *testing.T) {
	ioctl, open := ptyPeerIoctl, ptyOpenByName
	t.Cleanup(func() { ptyPeerIoctl, ptyOpenByName = ioctl, open })
	ptyPeerIoctl = func(int, uintptr) (int, error) { return -1, errors.New("no TIOCGPTPEER") }
	var name string
	var flags int
	ptyOpenByName = func(path string, mode int, perm uint32) (int, error) {
		name, flags = path, mode
		return open(path, mode, perm)
	}
	m, slave, err := openPty(80)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	defer slave.Close()
	if !strings.HasPrefix(name, "/dev/pts/") {
		t.Fatalf("the slave was opened as %q, want a /dev/pts/N name", name)
	}
	if want := unix.O_WRONLY | unix.O_NOCTTY | unix.O_CLOEXEC; flags != want {
		t.Errorf("open flags %#x, want O_WRONLY|O_NOCTTY|O_CLOEXEC %#x", flags, want)
	}
	if fl, err := unix.FcntlInt(slave.Fd(), unix.F_GETFL, 0); err != nil || fl&unix.O_ACCMODE != unix.O_WRONLY {
		t.Errorf("slave flags %#x (%v), want write-only", fl, err)
	}
	if _, err := unix.Read(int(slave.Fd()), make([]byte, 1)); err != unix.EBADF {
		t.Errorf("read of the slave: %v, want EBADF", err)
	}
	if fi, err := slave.Stat(); err != nil || fi.Mode().Perm() != slaveMode {
		t.Errorf("slave mode %v (%v), want %#o", fi, err, slaveMode)
	}
}
