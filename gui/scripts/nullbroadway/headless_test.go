package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// headless runs gui/scripts/headless.sh with args, this test binary as its
// display server, and a TMPDIR of its own, which must be empty after: the
// script removes its runtime dir whatever happened.
func headless(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	tmp := t.TempDir()
	script, err := filepath.Abs("../headless.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script, args...)
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "HEADLESS_TIMEOUT=30",
		"HEADLESS_SERVER="+os.Args[0], envMain+"=1")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if left, _ := os.ReadDir(tmp); len(left) > 0 {
		var names []string
		for _, e := range left {
			names = append(names, e.Name())
		}
		t.Errorf("headless.sh left %v in TMPDIR\n%s", names, out)
	}
	return string(out), err
}

// The command runs on the private display and bus, with nothing of a live
// session left in its environment.
func TestHeadlessDisplay(t *testing.T) {
	check := `test "$GDK_BACKEND" = broadway && test "$BROADWAY_DISPLAY" = :0 &&
		test -S "$XDG_RUNTIME_DIR/broadway1.socket" && test -z "${WAYLAND_DISPLAY:-}" &&
		test -z "${DISPLAY:-}" && test -z "${HYPRLAND_INSTANCE_SIGNATURE:-}" &&
		test "$ARCHMAID_GUI_DISPLAY_TESTS" = 1 &&
		case "$DBUS_SESSION_BUS_ADDRESS" in "unix:path=$XDG_RUNTIME_DIR/"*) ;; *) exit 3;; esac &&
		test "$(stat -c %a "$XDG_RUNTIME_DIR")" = 700 && echo display-ok`
	out, err := headless(t, []string{"WAYLAND_DISPLAY=wayland-1", "DISPLAY=:1", "HYPRLAND_INSTANCE_SIGNATURE=x"},
		"sh", "-c", check)
	if err != nil || !strings.Contains(out, "display-ok") {
		t.Fatalf("headless.sh: %v\n%s", err, out)
	}
}

// A failing command fails the script, and the runtime dir still goes.
func TestHeadlessCommandFails(t *testing.T) {
	out, err := headless(t, nil, "false")
	if err == nil {
		t.Fatalf("headless.sh false passed\n%s", out)
	}
}

// A server that can't start fails the script, which still removes its
// runtime dir: under errexit, the cleanup's kill of the server that's
// already gone used to end the trap before the removal.
func TestHeadlessServerFails(t *testing.T) {
	out, err := headless(t, []string{"HEADLESS_WIDTH=0"}, "true")
	if err == nil || !strings.Contains(out, "nullbroadway failed to start") {
		t.Fatalf("headless.sh with a server that can't start: %v\n%s", err, out)
	}
}
