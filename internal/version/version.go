// Package version says which archmaid build this is. It imports nothing of
// archmaid's, so every binary (the TUI, the helper, the GUI) can link it.
package version

import (
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
)

// Version is set at link time by the packaging
// (-ldflags "-X github.com/Enzord758/archmaid/internal/version.Version=...").
// The PKGBUILD and install.sh give it `git describe --tags --always --dirty`
// ("v1.0.0-3-g6ba8744"), or rN.sha when git has no tags to describe from.
// Empty: String reads the build's VCS stamp instead.
var Version = ""

// String is the build's version as every front end shows it (`archmaid
// --version`, the GUI's About line), in one short form:
//
//	v1.0.0                 -> 1.0.0
//	v0.8.1-beta            -> 0.8.1 beta
//	v1.0.0-3-g6ba8744      -> 1.0.0 + 3 commits (6ba8744)
//	v0.8.1-beta-2-g6ba8744 -> 0.8.1 beta + 2 commits (6ba8744)
//	any of them with -dirty -> the same, then " · modified"
//	r634.6ba8744, 6ba8744  -> as they are (a build with no tag to describe)
//
// The source is Version when the packaging set it, else the commit the Go
// toolchain stamped into the binary (the first 7 characters of vcs.revision,
// "-dirty" after them when the tree had changes), else "unknown" (a build
// outside a git checkout, or `go test`).
func String() string {
	if v := strings.TrimSpace(Version); v != "" {
		return Format(v)
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return Format(fromSettings(bi.Settings))
}

// describeRE is `git describe --tags --dirty` for a release tag vX.Y.Z or
// vX.Y.Z-beta: the tag, the commits since it and the abbreviated commit, and
// "-dirty".
var describeRE = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)(-beta)?(?:-(\d+)-g([0-9a-f]+))?(-dirty)?$`)

// Format words a describe string (see String). A string it does not know is
// shown as it is, without a leading "v", and with a trailing "-dirty" as
// " · modified".
func Format(v string) string {
	const modified = " · modified"
	m := describeRE.FindStringSubmatch(v)
	if m == nil {
		v = strings.TrimPrefix(v, "v")
		if rest, ok := strings.CutSuffix(v, "-dirty"); ok {
			return rest + modified
		}
		return v
	}
	out := m[1]
	if m[2] != "" {
		out += " beta"
	}
	if n, err := strconv.Atoi(m[3]); m[3] != "" && err == nil && n > 0 {
		out += " + " + m[3] + " commit"
		if n != 1 {
			out += "s"
		}
		out += " (" + m[4] + ")"
	}
	if m[5] != "" {
		out += modified
	}
	return out
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
