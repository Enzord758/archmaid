package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/frontend"
)

// TestGUISettingsCards: the Settings cards (P8 row 15). Choose… applies
// the Launcher's folder as a typed one is; Open in editor creates the
// topgrade config with the defaults in a scratch HOME and asks the
// Launcher to open it; the log card asks for the state dir. Nothing is
// opened for real (the harness's recordLauncher).
func TestGUISettingsCards(t *testing.T) {
	needDisplay(t)
	home, cfg, state, typed, chosen := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	rep, _ := child(t, "settings", "HOME="+home, "XDG_CONFIG_HOME="+cfg, "ARCHMAID_GUI_STATE="+state,
		"ARCHMAID_GUI_DL="+typed, "ARCHMAID_GUI_CHOOSE="+chosen)

	if len(rep.DlErrors) != 3 || !strings.Contains(rep.DlErrors[0], "home") || rep.DlErrors[1] != "" || rep.DlErrors[2] != "" {
		t.Errorf("folder errors %q: want home refused, then the typed and the chosen folder kept", rep.DlErrors)
	}
	if want := fmt.Sprintf("dry=true downloads=%q", chosen); len(rep.DriverOpts) != 2 || rep.DriverOpts[1] != want {
		t.Errorf("drivers %q, want the run's with %s", rep.DriverOpts, want)
	}
	conf := filepath.Join(home, ".config", "topgrade.toml")
	want := []string{"choose " + typed, "file " + conf, "file " + conf, "folder " + state}
	if !slices.Equal(rep.Launched, want) {
		t.Errorf("launched\n%q\nwant\n%q", rep.Launched, want)
	}
	if b, err := os.ReadFile(conf); err != nil || string(b) != frontend.DefaultTopgradeConfig {
		t.Errorf("topgrade config %q %v, want the defaults", b, err)
	}
	// The About line names the build: no link-time version in a test
	// binary, so the VCS stamp or "unknown" follows the prefix.
	if !strings.HasPrefix(rep.About, "About: archmaid ") || len(rep.About) <= len("About: archmaid ") {
		t.Errorf("About line %q", rep.About)
	}
	if rep.ConfigError != "" || rep.Problem != "" {
		t.Errorf("config error %q problem %q", rep.ConfigError, rep.Problem)
	}

	// An existing config is opened as it is, never rewritten.
	if err := os.WriteFile(conf, []byte("# mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, _ = child(t, "settings", "HOME="+home, "XDG_CONFIG_HOME="+t.TempDir(), "ARCHMAID_GUI_STATE="+state, "ARCHMAID_GUI_DL="+typed)
	if b, _ := os.ReadFile(conf); string(b) != "# mine\n" {
		t.Errorf("an existing config changed: %q", b)
	}
	if !slices.Contains(rep.Launched, "file "+conf) {
		t.Errorf("launched %q, want the existing config opened", rep.Launched)
	}
}
