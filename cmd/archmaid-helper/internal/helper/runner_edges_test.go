package helper

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// ExecRunner's edges: a binary that isn't there, output past the capture
// cap, a progress-bar \r at a read's end, and pipes the kernel won't give.

// TestExecRunnerQueryMissingBinary: Query of a binary that isn't there is
// an error with exit -1, never a clean exit 0 with no output.
func TestExecRunnerQueryMissingBinary(t *testing.T) {
	out, errOut, exit, err := ExecRunner{}.Query([]string{"/nonexistent/archmaid-x"})
	if !errors.Is(err, fs.ErrNotExist) || exit != -1 || out != "" || errOut != "" {
		t.Fatalf("Query = %q, %q, %d, %v; want exit -1 and the error", out, errOut, exit, err)
	}
}

// TestSplitLinesTrailingCR: a \r at the end of what has been read may be
// the first half of \r\n, so the line waits for more; at EOF it ends the
// line.
func TestSplitLinesTrailingCR(t *testing.T) {
	if adv, tok, err := SplitLines([]byte("50%\r"), false); adv != 0 || tok != nil || err != nil {
		t.Errorf("mid-stream: %d, %q, %v; want to wait for more", adv, tok, err)
	}
	if adv, tok, err := SplitLines([]byte("50%\r"), true); adv != 4 || string(tok) != "50%" || err != nil {
		t.Errorf("at EOF: %d, %q, %v; want the line", adv, tok, err)
	}
}

// TestCappedStopsAtMaxCapture: a command's captured output stops growing
// at maxCapture (a flood from a root command can't run the helper out of
// memory), while every write still reports success so the command isn't
// cut off mid-write.
func TestCappedStopsAtMaxCapture(t *testing.T) {
	var c capped
	chunk := bytes.Repeat([]byte("x"), maxCapture-10)
	if n, err := c.Write(chunk); n != len(chunk) || err != nil {
		t.Fatalf("first write: %d, %v", n, err)
	}
	if n, err := c.Write([]byte("0123456789abcdef")); n != 16 || err != nil {
		t.Fatalf("straddling write: %d, %v", n, err)
	}
	if n, err := c.Write([]byte("more")); n != 4 || err != nil {
		t.Fatalf("write past the cap: %d, %v", n, err)
	}
	if c.Len() != maxCapture || !strings.HasSuffix(c.String(), "x0123456789") {
		t.Fatalf("kept %d bytes ending %q, want exactly %d ending in the first 10 of the straddling write",
			c.Len(), c.String()[c.Len()-11:], maxCapture)
	}
}

// envNoFDChild runs TestNoFDChild: this test binary again, as a process
// that can open only a few more files.
const envNoFDChild = "ARCHMAID_HELPER_NOFD_CHILD"

// TestNoFDChild runs Stream with too few file descriptors left for its
// stdout pipe ("stdout") or its stderr pipe ("stderr"): it must fail with
// exit -1 and start nothing.
func TestNoFDChild(t *testing.T) {
	which := os.Getenv(envNoFDChild)
	if which == "" {
		t.Skip("run by TestExecRunnerStreamNoPipes")
	}
	free := map[string]int{"stdout": 1, "stderr": 2}[which]
	marker := os.Getenv("ARCHMAID_NOFD_MARKER")
	var exit int
	var err error
	withFreeFDs(t, free, func() {
		exit, err = ExecRunner{}.Stream([]string{"/bin/sh", "-c", ": > '" + marker + "'"}, func(string, string) {})
	})
	if err == nil || exit != -1 {
		t.Fatalf("Stream with %d free fds = %d, %v; want exit -1 and an error", free, exit, err)
	}
	// The pipe's own failure, not a later one (Start's /dev/null).
	var se *os.SyscallError
	if !errors.Is(err, syscall.EMFILE) || !errors.As(err, &se) || se.Syscall != "pipe2" {
		t.Fatalf("err = %v, want pipe2's EMFILE", err)
	}
}

// withFreeFDs runs f with exactly free file descriptors left under a
// lowered RLIMIT_NOFILE, then gives them all back.
func withFreeFDs(t *testing.T, free int, f func()) {
	t.Helper()
	// The poller must exist before the table fills: making it then would
	// throw instead of failing a pipe.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	w.Close()
	var old unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &old); err != nil {
		t.Fatal(err)
	}
	low := old
	low.Cur = 256
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &low); err != nil {
		t.Fatal(err)
	}
	defer unix.Setrlimit(unix.RLIMIT_NOFILE, &old)
	var fds []int
	defer func() {
		for _, fd := range fds {
			unix.Close(fd)
		}
	}()
	for {
		fd, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.EMFILE) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		fds = append(fds, fd)
	}
	if len(fds) < free {
		t.Fatalf("only %d fds to free", len(fds))
	}
	for range free {
		unix.Close(fds[len(fds)-1])
		fds = fds[:len(fds)-1]
	}
	f()
}

// TestExecRunnerStreamNoPipes: when the kernel won't give Stream its
// stdout or stderr pipe, it fails (exit -1) and the command never starts.
func TestExecRunnerStreamNoPipes(t *testing.T) {
	for _, which := range []string{"stdout", "stderr"} {
		marker := t.TempDir() + "/ran"
		cmd := exec.Command(os.Args[0], "-test.run=^TestNoFDChild$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), envNoFDChild+"="+which, "ARCHMAID_NOFD_MARKER="+marker)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: TestNoFDChild") {
			t.Fatalf("%s: child: %v\n%s", which, err, out)
		}
		if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s: the command ran", which)
		}
	}
}
