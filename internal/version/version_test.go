package version

import (
	"os"
	"runtime/debug"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

func TestStringPrefersTheLinkedVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	for in, want := range map[string]string{
		"2.0.0-3-g6ba8744": "2.0.0-3-g6ba8744",
		"v2.0.0":           "2.0.0",
		"  r634.6ba8744 ":  "r634.6ba8744",
	} {
		Version = in
		if got := String(); got != want {
			t.Errorf("Version %q: String() = %q, want %q", in, got, want)
		}
	}
}

// Unset, String falls back to the toolchain's VCS stamp, and never to "".
func TestStringFallsBack(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = ""
	if got := String(); got == "" {
		t.Error("String() is empty")
	}
}

func TestFromSettings(t *testing.T) {
	set := func(kv ...string) []debug.BuildSetting {
		var out []debug.BuildSetting
		for i := 0; i < len(kv); i += 2 {
			out = append(out, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return out
	}
	for _, tc := range []struct {
		name string
		in   []debug.BuildSetting
		want string
	}{
		{"no vcs", set("GOOS", "linux"), "unknown"},
		{"clean", set("vcs.revision", "6ba8744976ec5ad7ebf4d13f9254eb7ceddeddc1", "vcs.modified", "false"), "6ba8744"},
		{"dirty", set("vcs.modified", "true", "vcs.revision", "6ba8744976ec5ad7ebf4d13f9254eb7ceddeddc1"), "6ba8744-dirty"},
		{"short revision", set("vcs.revision", "abc"), "abc"},
	} {
		if got := fromSettings(tc.in); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
