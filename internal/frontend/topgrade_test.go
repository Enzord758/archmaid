package frontend

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTopgradeConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if p, err := TopgradeConfigPath(); err != nil || p != filepath.Join(home, ".config", "topgrade.toml") {
		t.Errorf("path %q %v", p, err)
	}
	for _, h := range []string{"", "relative/home"} {
		t.Setenv("HOME", h)
		if p, err := TopgradeConfigPath(); !errors.Is(err, ErrNoHome) || p != "" {
			t.Errorf("HOME=%q: path %q %v, want ErrNoHome", h, p, err)
		}
	}
}

func TestEnsureTopgradeConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf", "topgrade.toml")
	created, err := EnsureTopgradeConfig(path)
	if err != nil || !created {
		t.Fatalf("first: created %v %v", created, err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != DefaultTopgradeConfig || !strings.Contains(string(b), `aur_command = "yay"`) {
		t.Fatalf("defaults %q %v", b, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}

	// An existing file, even a dangling symlink, is never written.
	must(t, os.WriteFile(path, []byte("# mine\n"), 0o600))
	if created, err := EnsureTopgradeConfig(path); err != nil || created {
		t.Errorf("existing: created %v %v", created, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "# mine\n" {
		t.Errorf("existing config changed: %q", b)
	}
	link := filepath.Join(t.TempDir(), "topgrade.toml")
	target := filepath.Join(t.TempDir(), "elsewhere.toml")
	must(t, os.Symlink(target, link))
	if created, err := EnsureTopgradeConfig(link); err != nil || created {
		t.Errorf("dangling symlink: created %v %v", created, err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Error("the symlink's target was created")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
