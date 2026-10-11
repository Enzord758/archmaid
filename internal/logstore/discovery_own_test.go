package logstore

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiscoveryNeverDeletesOwnRunDir: a clock set back leaves a newer
// discovery-only run than this store's own directory, holding the same
// action's log. The store's own record counts as superseded by that rule,
// yet its directory is never deleted under it.
func TestDiscoveryNeverDeletesOwnRunDir(t *testing.T) {
	root := t.TempDir()
	seedDir(t, root, stampOf(day(5)), "services.log")
	s := storeAt(root, day(1))
	if err := s.Append(Services, "CHECK", "nothing", ""); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(root, stampOf(day(1)), "services.log")
	if _, err := os.Stat(own); err != nil {
		t.Fatalf("the store's own run directory was deleted: %v (dirs %v)", err, runDirs(t, root))
	}
	if err := s.Append(Services, "CHECK", "again", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(own); err != nil {
		t.Fatal(err)
	}
}
