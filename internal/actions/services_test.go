package actions

import (
	"os"
	"reflect"
	"testing"
)

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
