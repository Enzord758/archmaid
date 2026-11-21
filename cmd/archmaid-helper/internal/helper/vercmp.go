package helper

import "strings"

// A copy of internal/pkgdb/vercmp.go: the root helper imports nothing from
// the rest of the module. TestVercmpIsPkgdbs (cmd/archmaid-helper) ties the
// two together.

// vercmp compares two pacman versions ("epoch:version-release") as libalpm's
// alpm_pkg_vercmp does: -1, 0 or 1.
func vercmp(a, b string) int {
	if a == b {
		return 0
	}
	ea, va, ra := vcSplitEVR(a)
	eb, vb, rb := vcSplitEVR(b)
	if c := rpmVercmp(ea, eb); c != 0 {
		return c
	}
	if c := rpmVercmp(va, vb); c != 0 {
		return c
	}
	if ra == "" || rb == "" { // a missing release matches any release
		return 0
	}
	return rpmVercmp(ra, rb)
}

// vcSplitEVR splits "e:v-r"; a missing epoch is "0", a missing release "".
func vcSplitEVR(s string) (epoch, ver, rel string) {
	epoch = "0"
	if i := strings.IndexByte(s, ':'); i >= 0 {
		isDigits := i > 0
		for j := 0; j < i; j++ {
			if s[j] < '0' || s[j] > '9' {
				isDigits = false
			}
		}
		if isDigits {
			epoch, s = s[:i], s[i+1:]
		}
	}
	if i := strings.LastIndexByte(s, '-'); i >= 0 {
		return epoch, s[:i], s[i+1:]
	}
	return epoch, s, ""
}

func vcAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func vcDigit(c byte) bool { return c >= '0' && c <= '9' }
func vcAlnum(c byte) bool { return vcAlpha(c) || vcDigit(c) }

// rpmVercmp is libalpm's segment comparison: runs of digits compare as
// numbers (and beat letters), runs of letters compare as text, and any other
// character only separates segments.
func rpmVercmp(a, b string) int {
	if a == b {
		return 0
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		for i < len(a) && !vcAlnum(a[i]) {
			i++
		}
		for j < len(b) && !vcAlnum(b[j]) {
			j++
		}
		if i >= len(a) || j >= len(b) {
			break
		}
		si, sj := i, j
		var numeric bool
		if vcDigit(a[i]) {
			for i < len(a) && vcDigit(a[i]) {
				i++
			}
			for j < len(b) && vcDigit(b[j]) {
				j++
			}
			numeric = true
		} else {
			for i < len(a) && vcAlpha(a[i]) {
				i++
			}
			for j < len(b) && vcAlpha(b[j]) {
				j++
			}
		}
		sa, sb := a[si:i], b[sj:j]
		if sb == "" { // segments of different kinds: the numeric one is newer
			if numeric {
				return 1
			}
			return -1
		}
		if numeric {
			sa, sb = strings.TrimLeft(sa, "0"), strings.TrimLeft(sb, "0")
			if len(sa) != len(sb) {
				if len(sa) > len(sb) {
					return 1
				}
				return -1
			}
		}
		if c := strings.Compare(sa, sb); c != 0 {
			return c
		}
	}
	// The final showdown, as libalpm: both ended is equal; an ended a loses to
	// anything but a letter tail on b ("1.0" is newer than "1.0a"); a letter
	// tail on a loses; anything else left on a wins.
	endA, endB := i >= len(a), j >= len(b)
	switch {
	case endA && endB:
		return 0
	case endA && !vcAlpha(b[j]):
		return -1
	case !endA && vcAlpha(a[i]):
		return -1
	}
	return 1
}

// Vercmp is vercmp for the tests of the packages that may import both.
func Vercmp(a, b string) int { return vercmp(a, b) }
