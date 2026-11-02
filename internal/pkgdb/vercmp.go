package pkgdb

import "strings"

// Vercmp compares two pacman versions ("epoch:version-release") as libalpm's
// alpm_pkg_vercmp does: -1, 0 or 1.
func Vercmp(a, b string) int {
	if a == b {
		return 0
	}
	ea, va, ra := splitEVR(a)
	eb, vb, rb := splitEVR(b)
	if c := rpmvercmp(ea, eb); c != 0 {
		return c
	}
	if c := rpmvercmp(va, vb); c != 0 {
		return c
	}
	if ra == "" || rb == "" { // a missing release matches any release
		return 0
	}
	return rpmvercmp(ra, rb)
}

// splitEVR splits "e:v-r"; a missing epoch is "0", a missing release "".
func splitEVR(s string) (epoch, ver, rel string) {
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

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlnumByte(c byte) bool {
	return isAlpha(c) || isDigit(c)
}

// rpmvercmp is libalpm's segment comparison: runs of digits compare as
// numbers (and beat letters), runs of letters compare as text, and any other
// character only separates segments.
func rpmvercmp(a, b string) int {
	if a == b {
		return 0
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		for i < len(a) && !isAlnumByte(a[i]) {
			i++
		}
		for j < len(b) && !isAlnumByte(b[j]) {
			j++
		}
		if i >= len(a) || j >= len(b) {
			break
		}
		si, sj := i, j
		var numeric bool
		if isDigit(a[i]) {
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			numeric = true
		} else {
			for i < len(a) && isAlpha(a[i]) {
				i++
			}
			for j < len(b) && isAlpha(b[j]) {
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
	case endA && !isAlpha(b[j]):
		return -1
	case !endA && isAlpha(a[i]):
		return -1
	}
	return 1
}
