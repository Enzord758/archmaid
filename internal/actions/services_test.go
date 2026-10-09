package actions

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// shimSystemctl puts a systemctl running body first on PATH.
func shimSystemctl(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestDiscoverServicesListFails: when systemctl can't list the units, the
// discovery says so, with systemctl's own error, instead of reporting that
// nothing was found.
func TestDiscoverServicesListFails(t *testing.T) {
	shimSystemctl(t, "echo 'Failed to connect to bus: No such file or directory' >&2\nexit 1\n")
	d := discoverServices(context.Background(), Env{})
	n, ok := d.(Notice)
	if !ok || !strings.HasPrefix(n.Text, "Couldn't list services: ") || !strings.Contains(n.Text, "Failed to connect to bus") {
		t.Fatalf("got %#v, want a notice with systemctl's error", d)
	}
}

// TestDiscoverServicesNoneFound: a system with no enabled-but-inactive
// service is the nothing-found notice, not an error.
func TestDiscoverServicesNoneFound(t *testing.T) {
	shimSystemctl(t, "exit 0\n")
	if d := discoverServices(context.Background(), Env{}); d != (Notice{"No enabled-but-inactive services found."}) {
		t.Fatalf("got %#v", d)
	}
}

func TestParseInactiveServices(t *testing.T) {
	data, err := os.ReadFile("testdata/systemctl_list_units.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := parseInactiveServices(string(data))
	// ghost (● not-found), masked-thing (masked), the getty@/openvpn-client@/user@
	// instances and systemd-timesyncd (excluded) are all dropped.
	want := []string{"avahi-daemon.service", "bluetooth.service", "cups.service", "docker.service",
		"fstrim.service", "tlp.service", "transient.service"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestParseInactiveServicesBullet: a leading "●" is a marker, never the name,
// and a loaded unit carrying one is still read from its real column.
func TestParseInactiveServicesBullet(t *testing.T) {
	out := "● ghost.service not-found inactive dead ghost.service\n" +
		"● failed-ish.service loaded inactive dead Odd one\n" +
		"\n"
	got := parseInactiveServices(out)
	if !reflect.DeepEqual(got, []string{"failed-ish.service"}) {
		t.Fatalf("got %v", got)
	}
	if got := parseInactiveServices(""); len(got) != 0 {
		t.Fatalf("empty input: %v", got)
	}
}
