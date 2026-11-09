package healthview

import "testing"

func TestShown(t *testing.T) {
	long := ""
	for i := 0; i < 50; i++ {
		long += "abcdefghij "
	}
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"journalctl -b -u bluetooth.service", 60, "journalctl -b -u bluetooth.service"},
		{"a\n  b\tc", 60, "a b c"},
		{"abcdefghij", 10, "abcdefghij"},
		{"abcdefghijk", 10, "abcdefghi…"},
		{"åäöåäöåäöåäö", 5, "åäöå…"},
		{"x", 0, "x"},
	} {
		if got := Shown(c.in, c.max); got != c.want {
			t.Errorf("Shown(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
	if got := []rune(Shown(long, 100)); len(got) != 100 || got[99] != '…' {
		t.Errorf("a long command is cut to 100 runes ending in …, got %d", len(got))
	}
}
