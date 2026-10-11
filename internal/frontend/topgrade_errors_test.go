package frontend

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestEnsureTopgradeConfigErrors: a config that can't be created is an error
// (created reports whether anything was written), and nothing is left that
// pretends otherwise.
func TestEnsureTopgradeConfigErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions don't bind root")
	}
	t.Run("directory can't be made", func(t *testing.T) {
		parent := t.TempDir()
		must(t, os.Chmod(parent, 0o500))
		t.Cleanup(func() { os.Chmod(parent, 0o700) })
		created, err := EnsureTopgradeConfig(filepath.Join(parent, "conf", "topgrade.toml"))
		if err == nil || created {
			t.Errorf("created %v, err %v; want an error and nothing created", created, err)
		}
	})
	t.Run("file can't be opened", func(t *testing.T) {
		dir := t.TempDir()
		must(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { os.Chmod(dir, 0o700) })
		created, err := EnsureTopgradeConfig(filepath.Join(dir, "topgrade.toml"))
		if err == nil || created {
			t.Errorf("created %v, err %v; want an error and nothing created", created, err)
		}
	})
	t.Run("write fails", func(t *testing.T) {
		// A zero file-size limit makes the write fail with EFBIG (Go ignores
		// SIGXFSZ unless asked); the file was created, so created says true.
		var old syscall.Rlimit
		must(t, syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old))
		must(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 0, Max: old.Max}))
		restore := func() { syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old) }
		t.Cleanup(restore)
		path := filepath.Join(t.TempDir(), "topgrade.toml")
		created, err := EnsureTopgradeConfig(path)
		restore()
		if err == nil || !created {
			t.Errorf("created %v, err %v; want the error and created=true (the empty file exists)", created, err)
		}
		if fi, serr := os.Stat(path); serr != nil || fi.Size() != 0 {
			t.Errorf("the file after a failed write: %v, %v", fi, serr)
		}
	})
}
