package helperclient

import (
	"os"
	"path/filepath"
	"testing"
)

// The only place tests move helperFile, Run's install check.

// fakeHelperInstalled points the check at a stand-in file in dir, for the
// rest of t. Nothing runs it: the shim pkexec only prints.
func fakeHelperInstalled(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, "archmaid-helper")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setHelperFile(t, p)
}

// helperNotInstalled points the check at a path that doesn't exist.
func helperNotInstalled(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "missing", "archmaid-helper")
	setHelperFile(t, p)
	return p
}

func setHelperFile(t *testing.T, p string) {
	old := helperFile
	helperFile = p
	t.Cleanup(func() { helperFile = old })
}
