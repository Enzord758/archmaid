package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// legacyServicesPipeline is the bash pipeline discoverServices used before it
// stopped calling `systemctl list-unit-files` (430 ms): kept here only so the
// equivalence test can prove the new discovery selects the same units.
const legacyServicesPipeline = `comm -12 ` +
	`<(systemctl list-unit-files --type=service --state=enabled --no-legend | awk '{print $1}' | sort) ` +
	`<(systemctl list-units --type=service --state=inactive --no-legend | awk '{print $1}' | sort) ` +
	`| grep -Ev 'systemd-|getty@|emergency|rescue|initrd|plymouth|shadow|pkgstats|man-db|` +
	`mkinitcpio|modprobe@|audit|dirmngr|gpg-agent|keyboxd|dm-event|lm_sensors|nmb|archlinux-keyring-wkd-sync'`

// shimFixtureSystemctl puts a `systemctl` on PATH that answers from the
// testdata fixtures like a real machine: list-unit-files and list-units print
// the captured tables (list-units hides the "●" not-found rows unless --all is
// given), and is-enabled prints one state per known unit, with an error on
// stderr and no stdout line for an unknown one.
func shimFixtureSystemctl(t *testing.T) {
	t.Helper()
	fix, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1" in
list-unit-files)
	case "$*" in
	*--state=enabled*) awk '$2=="enabled"' "` + fix + `/systemctl_list_unit_files.txt" ;;
	*) cat "` + fix + `/systemctl_list_unit_files.txt" ;;
	esac ;;
list-units)
	case "$*" in
	*--all*) cat "` + fix + `/systemctl_list_units.txt" ;;
	*) grep -v '^●' "` + fix + `/systemctl_list_units.txt" ;;
	esac ;;
is-enabled)
	shift
	for u; do
		awk -v u="$u" '$1==u {print $2; f=1} END {exit !f}' "` + fix + `/systemctl_list_unit_files.txt" ||
			echo "Failed to get unit file state for $u: No such file or directory" >&2
	done
	exit 1 ;;
esac
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
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

// TestDiscoverServicesFixture: the real discovery against the fixture system,
// including the one batched is-enabled call and its per-unit fallback for the
// transient unit that has no unit file.
func TestDiscoverServicesFixture(t *testing.T) {
	shimFixtureSystemctl(t)
	msg := discoverServices()().(discoveryMsg)
	want := []string{"avahi-daemon.service", "bluetooth.service", "cups.service", "docker.service"}
	if msg.kind != "services" || !reflect.DeepEqual(msg.services, want) {
		t.Fatalf("got %+v, want %v", msg, want)
	}
}

func TestDiscoverServicesNone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	msg := discoverServices()().(discoveryMsg)
	if msg.notice == "" || len(msg.services) != 0 {
		t.Fatalf("want the nothing-to-do notice, got %+v", msg)
	}
}

// TestServicesDiscoveryMatchesLegacyPipeline: for one fixture system the old
// bash pipeline and the new discovery select exactly the same units.
func TestServicesDiscoveryMatchesLegacyPipeline(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	shimFixtureSystemctl(t)
	out, err := exec.Command("bash", "-c", legacyServicesPipeline).Output()
	if err != nil {
		t.Fatalf("legacy pipeline: %v", err)
	}
	old := nonEmptyLines(string(out))
	if len(old) == 0 {
		t.Fatal("legacy pipeline selected nothing; the fixture proves nothing")
	}
	got := discoverServices()().(discoveryMsg).services
	if !reflect.DeepEqual(got, old) {
		t.Fatalf("new discovery %v != legacy pipeline %v", got, old)
	}
	if strings.Contains(strings.Join(got, " "), "●") {
		t.Fatalf("bullet leaked into %v", got)
	}
}
