package helper

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
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
func ChildEnv() []string {
	return []string{"PATH=/usr/bin", "LC_ALL=C", "TERM=dumb"}
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
// exactly ChildEnv, the working directory is /, stdin is /dev/null.
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
	cmd.Env = ChildEnv()
	cmd.Dir = "/"
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
