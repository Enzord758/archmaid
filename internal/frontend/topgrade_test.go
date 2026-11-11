package frontend

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/aurhelper"
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
	// The test environment's AUR helper is yay (ARCHMAID_AUR_HELPER, testenv).
	if err != nil || string(b) != TopgradeConfigFor(aurhelper.Helper{Name: "yay"}) || !strings.Contains(string(b), "\narch_package_manager = \"yay\"\n") {
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

// The default config names the helper that is installed with topgrade's own
// key, arch_package_manager; the old "aur_command" is not a topgrade key and its
// [linux] table rejects unknown keys (config.rs, deny_unknown_fields), so it
// must never be written. With no helper the key is only a comment.
func TestTopgradeConfigFor(t *testing.T) {
	for _, c := range []struct {
		h    aurhelper.Helper
		want string // "" = no active key
	}{{aurhelper.Helper{Name: "paru"}, "paru"}, {aurhelper.Helper{Name: "yay"}, "yay"}, {aurhelper.Helper{}, ""}} {
		got := TopgradeConfigFor(c.h)
		if strings.Contains(got, "aur_command") {
			t.Errorf("%+v: writes aur_command, which topgrade rejects:\n%s", c.h, got)
		}
		if v := aurhelper.ParseArchPackageManager([]byte(got)); v != c.want {
			t.Errorf("%+v: arch_package_manager %q, want %q:\n%s", c.h, v, c.want, got)
		}
		for _, must := range []string{"[misc]", "assume_yes = true", "cleanup = true", "[linux]", "# [skip_steps]"} {
			if !strings.Contains(got, must) {
				t.Errorf("%+v: lost %q", c.h, must)
			}
		}
	}
	if TopgradeConfigFor(aurhelper.Helper{}) != DefaultTopgradeConfig {
		t.Error("no helper: not the default")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
