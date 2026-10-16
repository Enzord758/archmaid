package privilege

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dropShim installs a sudo stand-in on an exclusive PATH that records its
// argv, whether it saw SUDO_ASKPASS and what stdin was, then runs tail.
func dropShim(t *testing.T, tail string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"" + dir + "/argv\"\n" +
		"printf '%s\\n' \"${SUDO_ASKPASS-unset}\" > \"" + dir + "/askpass\"\n" +
		"if [ -t 0 ]; then echo tty; else echo notty; fi > \"" + dir + "/stdin\"\n" + tail + "\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return dir
}

func readTrim(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// TestDropTicketNeverPrompts: DropTicket runs exactly `sudo -k`, with no
// SUDO_ASKPASS (even one the user's shell exported) and no terminal on
// stdin, so it can't ask anywhere.
func TestDropTicketNeverPrompts(t *testing.T) {
	dir := dropShim(t, "exit 0")
	t.Setenv("SUDO_ASKPASS", "/somewhere/askpass")
	if err := DropTicket(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := readTrim(t, filepath.Join(dir, "argv")); got != "-k" {
		t.Errorf("sudo ran with %q, want \"-k\"", got)
	}
	if got := readTrim(t, filepath.Join(dir, "askpass")); got != "unset" {
		t.Errorf("sudo saw SUDO_ASKPASS=%q", got)
	}
	if got := readTrim(t, filepath.Join(dir, "stdin")); got != "notty" {
		t.Errorf("sudo's stdin: %s", got)
	}
}

// TestDropTicketBounded: a sudo that fails reports it, and one that hangs
// is cut off at the timeout: an exit is never held up.
func TestDropTicketBounded(t *testing.T) {
	dropShim(t, "exit 1")
	if err := DropTicket(context.Background()); err == nil {
		t.Error("a failing sudo -k reported success")
	}
	dropShim(t, "exec /usr/bin/sleep 30")
	start := time.Now()
	if err := DropTicket(context.Background()); err == nil {
		t.Error("a hanging sudo -k reported success")
	}
	if d := time.Since(start); d > dropTimeout+2*time.Second {
		t.Errorf("DropTicket took %v", d)
	}
}
