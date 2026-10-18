// Package version says which archmaid build this is. It imports nothing of
// archmaid's, so every binary (the TUI, the helper, the GUI) can link it.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is set at link time by the packaging
// (-ldflags "-X github.com/Enzord758/archmaid/internal/version.Version=...").
// The PKGBUILD and install.sh give it `git describe --tags --always --dirty`
// without the leading "v" ("2.0.0-3-g6ba8744"), or rN.sha when git has no
// tags to describe from. Empty: String reads the build's VCS stamp instead.
var Version = ""

// String is the build's version: Version when the packaging set it, else the
// commit the Go toolchain stamped into the binary (the first 7 characters of
// vcs.revision, "-dirty" after them when the tree had changes), else
// "unknown" (a build outside a git checkout, or `go test`).
func String() string {
	if v := strings.TrimSpace(Version); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return fromSettings(bi.Settings)
}

// fromSettings is the version a build's settings give: vcs.revision, short,
// and "-dirty" when vcs.modified is "true".
func fromSettings(settings []debug.BuildSetting) string {
	var rev, modified string
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev == "" {
		return "unknown"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if modified == "true" {
		rev += "-dirty"
	}
	return rev
}
