package actions

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	// The record: the failure alone, what was tried, and the error in the log.
	if !strings.Contains(n.Err, "Failed to connect to bus") || strings.Contains(n.Err, "\n") ||
		len(n.Details) != 1 || !strings.Contains(n.Details[0], "systemctl list-units --type=service") ||
		!strings.Contains(n.Log, "error: ") {
		t.Fatalf("record of a failed look: %#v", n)
	}
}

// TestDiscoverServicesNoneFound: a system with no enabled-but-inactive
// service is the nothing-found notice, not an error.
func TestDiscoverServicesNoneFound(t *testing.T) {
	shimSystemctl(t, "exit 0\n")
	d := discoverServices(context.Background(), Env{})
	if n, ok := d.(Notice); !ok || n.Text != "No enabled-but-inactive services found. No stopped services to check." || n.Err != "" {
		t.Fatalf("got %#v", d)
	}
}

// TestDiscoverServicesSaysWhatItChecked: the nothing-found notice says how
// many stopped services were checked and how many were skipped by which
// rule; its Details list every unit with its is-enabled state or its skip
// rule, and its Log the commands with their raw output. With an enabled
// one the pick list carries the same record.
func TestDiscoverServicesSaysWhatItChecked(t *testing.T) {
	listing := "testdata/systemctl_list_units.txt"
	abs, err := filepath.Abs(listing)
	if err != nil {
		t.Fatal(err)
	}
	states := t.TempDir()
	stateFile := filepath.Join(states, "state")
	os.WriteFile(stateFile, []byte("disabled"), 0o600)
	shimSystemctl(t, `case "$1" in
list-units) cat '`+abs+`' ;;
is-enabled) shift; shift; st=$(cat '`+stateFile+`'); for u in "$@"; do if [ "$u" = cups.service ]; then echo "$st"; else echo disabled; fi; done; exit 1 ;;
esac
`)
	d := discoverServices(context.Background(), Env{})
	n, ok := d.(Notice)
	want := "Checked 7 stopped services: none are enabled. Skipped 6 (instances, not loaded, systemd-*)."
	if !ok || n.Text != want || n.Err != "" {
		t.Fatalf("got %#v\nwant the text %q", d, want)
	}
	for _, line := range []string{
		"cups.service: disabled",
		"tlp.service: disabled",
		"ghost.service: skipped, not loaded (not-found)",
		"masked-thing.service: skipped, not loaded (masked)",
		"getty@tty2.service: skipped, an instance or template",
		"systemd-timesyncd.service: skipped, excluded: systemd-",
	} {
		if !slices.Contains(n.Details, line) {
			t.Errorf("Details lack %q: %q", line, n.Details)
		}
	}
	if len(n.Details) != 13 {
		t.Errorf("%d detail lines, want one per listed unit (13): %q", len(n.Details), n.Details)
	}
	for _, s := range []string{"$ systemctl list-units --type=service --all --state=inactive --plain --no-legend\n",
		"Avahi mDNS/DNS-SD Stack", "$ systemctl is-enabled -- avahi-daemon.service bluetooth.service"} {
		if !strings.Contains(n.Log, s) {
			t.Errorf("Log lacks %q:\n%s", s, n.Log)
		}
	}

	os.WriteFile(stateFile, []byte("enabled"), 0o600)
	d = discoverServices(context.Background(), Env{})
	sel, ok := d.(NeedsSelection)
	if !ok || !slices.Equal(sel.Items, []string{"cups.service"}) ||
		sel.Checked.Summary != "Checked 7 stopped services: 1 is enabled. Skipped 6 (instances, not loaded, systemd-*)." ||
		!slices.Contains(sel.Checked.Details, "cups.service: enabled") || sel.Checked.Log == "" {
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
