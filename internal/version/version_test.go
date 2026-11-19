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
		"v1.0.0-3-g6ba8744": "1.0.0 + 3 commits (6ba8744)",
		"v1.0.0":            "1.0.0",
		"  r634.6ba8744 ":   "r634.6ba8744",
	} {
		Version = in
		if got := String(); got != want {
			t.Errorf("Version %q: String() = %q, want %q", in, got, want)
		}
	}
}

// Format covers every form `git describe --tags --dirty` takes for the
// release tags (vX.Y.Z, and vX.Y.Z-beta for the renamed betas), the packaging
// fallback and the VCS stamp, and anything it doesn't know.
func TestFormat(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// on a tag
		{"v1.0.0", "1.0.0"},
		{"1.0.0", "1.0.0"},
		{"v1.0.0-dirty", "1.0.0 · modified"},
		{"v0.8.1-beta", "0.8.1 beta"},
		{"v0.1.0-beta-dirty", "0.1.0 beta · modified"},
		// past a tag
		{"v1.0.0-3-g6ba8744", "1.0.0 + 3 commits (6ba8744)"},
		{"v1.0.0-1-g6ba8744", "1.0.0 + 1 commit (6ba8744)"},
		{"v1.0.0-12-g6ba8744-dirty", "1.0.0 + 12 commits (6ba8744) · modified"},
		{"v1.2.10-3-gabc1234", "1.2.10 + 3 commits (abc1234)"},
		{"v0.8.1-beta-2-g6ba8744", "0.8.1 beta + 2 commits (6ba8744)"},
		{"v0.8.1-beta-2-g6ba8744-dirty", "0.8.1 beta + 2 commits (6ba8744) · modified"},
		// no tag to describe from: the packaging's rN.sha, the VCS stamp
		{"r634.6ba8744", "r634.6ba8744"},
		{"6ba8744", "6ba8744"},
		{"6ba8744-dirty", "6ba8744 · modified"},
		{"unknown", "unknown"},
		// forms it doesn't know are shown, not lost
		{"v1.0.0-rc1", "1.0.0-rc1"},
		{"v1.0.0-rc1-dirty", "1.0.0-rc1 · modified"},
		{"v1.0.0-0-g6ba8744", "1.0.0"},
	} {
		if got := Format(tc.in); got != tc.want {
			t.Errorf("Format(%q) = %q, want %q", tc.in, got, tc.want)
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
