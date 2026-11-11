package driver

import (
	"os"
	"path/filepath"
	"testing"
)

// plantConfig makes an XDG config folder holding file (relative path) with
// content, and points the step's environment at it.
func plantConfig(t *testing.T, file, content string) string {
	t.Helper()
	cfg := t.TempDir()
	path := filepath.Join(cfg, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", cfg)
	return path
}

// TestConfigGuardTurnsKeptPasswordOff (#282): paru, yay and topgrade pick the
// program they run sudo with from files the user can write, which a build
// script of an earlier run can plant. A script there that execs the real
// sudo -A is sudo run by a kept caller, so the kept password would answer
// it. Like a shadow on PATH, such a setting turns the kept password off.
func TestConfigGuardTurnsKeptPasswordOff(t *testing.T) {
	for _, c := range []struct{ name, file, content string }{
		{"paru Sudo", "paru/paru.conf", "[options]\nBottomUp\n[bin]\nSudo = /home/me/.cache/x/s\n"},
		{"paru Pacman", "paru/paru.conf", "[bin]\nPacman = /home/me/pacman\n"},
		{"paru PacmanConf", "paru/paru.conf", "[bin]\nPacmanConf = /home/me/pacman.conf\n"},
		{"yay sudobin", "yay/config.json", "{\n  \"sudobin\": \"/home/me/s\",\n  \"sudoloop\": true\n}\n"},
		{"yay pacmanbin", "yay/config.json", "{\"pacmanbin\": \"/home/me/p\"}\n"},
		{"topgrade sudo_command", "topgrade.toml", "[misc]\nsudo_command = \"/home/me/s\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := plantConfig(t, c.file, c.content)
			requireOff(t, newGuardRig(t, nil, nil), path)
		})
	}
}

// TestConfigGuardLeavesOrdinaryConfigAlone: the settings that don't choose a
// program, a commented key, and the system's own value spelled out change
// nothing.
func TestConfigGuardLeavesOrdinaryConfigAlone(t *testing.T) {
	plantConfig(t, "paru/paru.conf", "[options]\nBottomUp\nSudoLoop\n[bin]\n#Sudo = /home/me/s\nSudo = sudo\nPacman = /usr/bin/pacman\n")
	requireOn(t, newGuardRig(t, nil, nil))
}
