package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// useLogDir points the log dir at dir for one test.
func useLogDir(t *testing.T, dir string) {
	t.Helper()
	old := logDir
	logDir = dir
	t.Cleanup(func() { logDir = old })
}

func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// TestViewLogCreatesNoRunDir: browsing the logs is read-only. logPath used to
// MkdirAll the run directory, so just opening View log left an empty
// <timestamp>/ behind that took one of pruneLogs' five slots from a real run.
func TestViewLogCreatesNoRunDir(t *testing.T) {
	useLogDir(t, t.TempDir())

	m := fixedModel()
	next, _ := m.openLogPicker()
	m = next.(model)
	for _, c := range logCategoryLabels {
		next, _ = m.openLogCategory(c.id)
		_ = next.(model).View()
	}

	if _, err := os.Stat(filepath.Join(logDir, runDirName())); !os.IsNotExist(err) {
		t.Fatalf("viewing logs created the run directory (stat err = %v)", err)
	}
}

// TestLogModesPrivate: logs carry full command output, so a write leaves the
// state dir and run dir 0700 and the file 0600 — whether it creates them or
// finds them left 0755/0644 by an older version.
func TestLogModesPrivate(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "archmaid")
		useLogDir(t, root)
		appendLog(logCategoryCleanup, "OK", "x", "")
		assertPrivate(t, root)
	})
	t.Run("existing", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "archmaid")
		useLogDir(t, root)
		run := filepath.Join(root, runDirName())
		if err := os.MkdirAll(run, 0o755); err != nil {
			t.Fatal(err)
		}
		os.Chmod(root, 0o755)
		if err := os.WriteFile(logPath(logCategoryCleanup), []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		appendLog(logCategoryCleanup, "OK", "x", "")
		assertPrivate(t, root)
	})
}

func assertPrivate(t *testing.T, root string) {
	t.Helper()
	run := filepath.Join(root, runDirName())
	for path, want := range map[string]fs.FileMode{
		root:                        0o700,
		run:                         0o700,
		logPath(logCategoryCleanup): 0o600,
	} {
		if got := modeOf(t, path); got != want {
			t.Errorf("%s: mode %o, want %o", path, got, want)
		}
	}
}
