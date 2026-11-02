package aurbuild

import (
	"os"
	"syscall"
	"testing"
)

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o600) }

// skipIfRoot: permission tests mean nothing to root.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
}
