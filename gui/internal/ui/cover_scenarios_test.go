package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// The cover scenarios' tests: each runs one "cov-" child (cover_harness_test.go),
// which asserts inside the window and reports what failed.

func TestGUICovDialogs(t *testing.T)  { covChild(t, "cov-dialogs") }
func TestGUICovQuit(t *testing.T)     { covChild(t, "cov-quit") }
func TestGUICovWindow(t *testing.T)   { covChild(t, "cov-window") }
func TestGUICovShell(t *testing.T)    { covChild(t, "cov-shell") }
func TestGUICovTimeline(t *testing.T) { covChild(t, "cov-timeline") }

func TestGUICovLogs(t *testing.T) {
	covChild(t, "cov-logs", "ARCHMAID_GUI_STATE="+covLogsFixture(t))
}

// A state dir that is a file: the Logs page says it can't read it.
func TestGUICovLogsUnreadable(t *testing.T) {
	f := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	covChild(t, "cov-logs-unreadable", "ARCHMAID_GUI_STATE="+f)
}

// The Settings page with a HOME/.config and a config folder that are files.
func TestGUICovSettings(t *testing.T) {
	home, cfg := t.TempDir(), t.TempDir()
	for _, f := range []string{filepath.Join(home, ".config"), filepath.Join(cfg, "archmaid")} {
		if err := os.WriteFile(f, []byte("in the way"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	covChild(t, "cov-settings", "HOME="+home, "XDG_CONFIG_HOME="+cfg, "ARCHMAID_GUI_DL="+t.TempDir(), "ARCHMAID_GUI_STATE="+t.TempDir())
}

// No HOME at all.
func TestGUICovNoHome(t *testing.T) {
	covChild(t, "cov-nohome", "HOME=", "XDG_CONFIG_HOME="+t.TempDir(), "ARCHMAID_GUI_STATE="+t.TempDir())
}
