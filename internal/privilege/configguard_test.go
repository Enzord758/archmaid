package privilege

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfigShadows (#282): which helper config settings turn the kept
// password off.
func TestConfigShadows(t *testing.T) {
	for _, c := range []struct {
		name, file, content string
		want                bool
	}{
		{"nothing", "paru/paru.conf", "[options]\nBottomUp\n", false},
		{"commented", "paru/paru.conf", "#Sudo = /x\n", false},
		{"system's own", "paru/paru.conf", "Sudo = sudo\nPacman = /usr/bin/pacman\n", false},
		{"paru own sudo", "paru/paru.conf", "Sudo=/x\n", true},
		{"paru include", "paru/paru.conf", "Include = /home/me/more.conf\n", true},
		{"paru sudoflags", "paru/paru.conf", "SudoFlags = --foo\n", true},
		{"yay minified json", "yay/config.json", `{"sudobin":"/x","sudoloop":true}`, true},
		{"yay json default", "yay/config.json", `{"sudobin":"sudo","sudoloop":true}`, false},
		{"yay lua", "yay/init.lua", "yay.sudobin = \"/x\"\n", true},
		{"yay lua comment", "yay/init.lua", "-- sudobin = \"/x\"\n", false},
		{"topgrade", "topgrade.toml", "[misc]\nsudo_command = \"doas\"\n", true},
		{"topgrade commented", "topgrade.toml", "# sudo_command = \"doas\"\n", false},
		{"topgrade nested folder", "topgrade/topgrade.toml", "sudo_command = '/x'\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := t.TempDir()
			path := filepath.Join(cfg, c.file)
			os.MkdirAll(filepath.Dir(path), 0o755)
			if err := os.WriteFile(path, []byte(c.content), 0o644); err != nil {
				t.Fatal(err)
			}
			got := ConfigShadows([]string{"XDG_CONFIG_HOME=" + cfg, "HOME=/nonexistent"}, nil)
			if (len(got) > 0) != c.want {
				t.Errorf("ConfigShadows = %v, want risk %v", got, c.want)
			}
		})
	}
	t.Run("PARU_CONF names the file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "p.conf")
		os.WriteFile(f, []byte("Sudo = /x\n"), 0o644)
		if got := ConfigShadows([]string{"HOME=/nonexistent", "PARU_CONF=" + f}, nil); len(got) != 1 || got[0].Path != f {
			t.Errorf("= %v", got)
		}
	})
	t.Run("a config that is not a regular file", func(t *testing.T) {
		cfg := t.TempDir()
		if err := os.MkdirAll(filepath.Join(cfg, "paru", "paru.conf"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := ConfigShadows([]string{"XDG_CONFIG_HOME=" + cfg}, nil); len(got) != 1 {
			t.Errorf("= %v", got)
		}
	})
	t.Run("no home, no config", func(t *testing.T) {
		if got := ConfigShadows([]string{}, nil); got != nil {
			t.Errorf("= %v", got)
		}
	})
}
