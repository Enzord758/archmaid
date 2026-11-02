package proc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Query runs a short read-only command under ctx (cancelling it kills the
// command) and returns its stdout — also on failure, for callers whose
// command reports through its exit status (pacman -Qdtq exits 1 for "none",
// pacman -Qkq exits 1 with a report on stdout). On a non-zero exit the error
// still wraps the *exec.ExitError, whose Stderr holds the command's stderr,
// and its text leads with that stderr when there is any. A sudo that could
// prompt on the terminal is refused like Start refuses it (CheckSudo), and
// the command runs without any inherited SUDO_ASKPASS.
func Query(ctx context.Context, name string, args ...string) ([]byte, error) {
	return QueryIn(ctx, "", nil, name, args...)
}

// QueryIn is Query in directory dir ("" = archmaid's) with exactly env as
// the command's environment (nil = archmaid's own, as Query). The AUR build
// runs git this way: a fixed, clean environment and nothing inherited.
func QueryIn(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	if err := CheckSudo(name, args); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// A grandchild that keeps the output pipe open must not outlive a kill.
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = Spec{Name: name, Env: env}.env()
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if msg := strings.TrimSpace(string(exit.Stderr)); msg != "" {
			return out, fmt.Errorf("%s: %w", msg, err)
		}
	}
	return out, err
}
