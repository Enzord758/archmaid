package proc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Query runs a short read-only command under ctx (cancelling it kills the
// command) and returns its stdout — also on failure, for callers whose
// command reports through its exit status (pacman -Qdtq exits 1 for "none",
// pacman -Qkq exits 1 with a report on stdout). On a non-zero exit the error
// still wraps the *exec.ExitError, whose Stderr holds the command's stderr,
// and its text leads with that stderr when there is any.
func Query(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if msg := strings.TrimSpace(string(exit.Stderr)); msg != "" {
			return out, fmt.Errorf("%s: %w", msg, err)
		}
	}
	return out, err
}
