package report

import "testing"

func TestHumanBytes(t *testing.T) {
	for _, c := range []struct {
		n    uint64
		want string
	}{
		{0, "0 B"}, {1, "1 B"}, {1023, "1023 B"}, {1024, "1 KiB"}, {1536, "1.5 KiB"},
		{54 << 20, "54 MiB"}, {212 << 20, "212 MiB"}, {381 << 20, "381 MiB"}, {96 << 20, "96 MiB"},
		{1966080000, "1.83 GiB"}, {3328599654, "3.1 GiB"}, {4509715661, "4.2 GiB"},
		{1048575, "1 MiB"}, {1 << 30, "1 GiB"}, {100 << 30, "100 GiB"}, {1 << 40, "1 TiB"},
	} {
		if got := HumanBytes(c.n); got != c.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
