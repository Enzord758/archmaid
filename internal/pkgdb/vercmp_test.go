package pkgdb

import "testing"

// TestVercmp: cases from libalpm's own vercmp tests (alpm_pkg_vercmp).
func TestVercmp(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.5.0", "1.5.0", 0},
		{"1.5.1", "1.5.0", 1},
		{"1.5.1", "1.5", 1},
		{"1.5.0-1", "1.5.0-1", 0},
		{"1.5.0-1", "1.5.0-2", -1},
		{"1.5.0-1", "1.5.1-1", -1},
		{"1.5.0-2", "1.5.1-1", -1},
		{"1.5-1", "1.5.1-1", -1},
		{"1.5-1", "1.5.0-1", -1},
		{"1.5b-1", "1.5-1", -1},
		{"1.5-1", "1.5b-1", 1},
		{"1.5b-1", "1.5", -1},
		{"1.0a", "1.0alpha", -1},
		{"1.0alpha", "1.0b", -1},
		{"1.0b", "1.0beta", -1},
		{"1.0beta", "1.0rc", -1},
		{"1.0rc", "1.0", -1},
		{"1.0", "1.0a", 1},
		{"1.0.", "1.0", 1},
		{"1.001", "1.1", 0},
		{"1.10", "1.9", 1},
		{"1", "a", 1},
		{"a", "1", -1},
		{"1:1.0", "1.0", 1},
		{"1:1.0", "1:1.1", -1},
		{"0:1.0", "1.0", 0},
		{"1.0", "2:0.1", -1},
		{"1.0-1", "1.0", 0}, // a missing release matches any
		{"2.40-1", "2.40-1", 0},
		{"9.1-2", "9.1-1", 1},
		{"r123.abc-1", "r99.def-1", 1},
		{"a:1", "a:2", -1},
		{"1..", "1.0", -1},
		{"", "", 0},
	}
	for _, c := range cases {
		if got := Vercmp(c.a, c.b); got != c.want {
			t.Errorf("Vercmp(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if c.a != c.b {
			if got := Vercmp(c.b, c.a); got != -c.want {
				t.Errorf("Vercmp(%q, %q) = %d, want %d (the reverse)", c.b, c.a, got, -c.want)
			}
		}
	}
}
