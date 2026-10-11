package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No config folder is an error from Load, not the defaults: the page says
// the settings can't be kept.
func TestLoadNoPath(t *testing.T) {
	s, err := Load("")
	if err == nil || !strings.Contains(err.Error(), "no config folder") || s != (Settings{}) {
		t.Errorf("Load(\"\") = %+v, %v", s, err)
	}
}

// A path that exists but can't be read as a file (a directory) is the
// defaults and the read error, not "missing".
func TestLoadUnreadable(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err == nil || s != (Settings{}) {
		t.Fatalf("Load(dir) = %+v, %v; want the defaults and an error", s, err)
	}
	if os.IsNotExist(err) {
		t.Errorf("a directory reported as missing: %v", err)
	}
}

// Save with no path says why and writes nothing.
func TestSaveNoPath(t *testing.T) {
	if err := Save("", Settings{DryRun: true}); err == nil || !strings.Contains(err.Error(), "no config folder") {
		t.Errorf("err %v", err)
	}
}

// The folder can't be created because a file is in the way: an error, and
// the file is left alone.
func TestSaveFolderBlockedByFile(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "archmaid")
	if err := os.WriteFile(blocker, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(filepath.Join(blocker, "gui.json"), Settings{DryRun: true}); err == nil {
		t.Fatal("Save under a file: want an error")
	}
	if b, _ := os.ReadFile(blocker); string(b) != "mine" {
		t.Errorf("the blocking file now holds %q", b)
	}
}

// A folder that can't be written to: an error, and no temp file is left.
func TestSaveReadOnlyFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := Save(filepath.Join(dir, "gui.json"), Settings{DryRun: true}); err == nil {
		t.Fatal("Save into a read-only folder: want an error")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("folder holds %v, want nothing", ents)
	}
}
