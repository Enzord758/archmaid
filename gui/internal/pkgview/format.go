package pkgview

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Size is a byte count as the page shows it: "812 KiB", "1.4 MiB".
func Size(b int64) string {
	if b < 0 {
		b = 0
	}
	f := float64(b)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	switch {
	case i == 0:
		return fmt.Sprintf("%d B", b)
	case f >= 100:
		return fmt.Sprintf("%.0f %s", f, units[i])
	default:
		return fmt.Sprintf("%.1f %s", f, units[i])
	}
}

// Age is "just now", "5 min", "3 h", "2 days" for a duration.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d/time.Hour))
	default:
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
}

// N is "1 package", "2 packages".
func N(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// VerCmp compares two package versions as pacman's vercmp does ([epoch:]
// version[-release], libalpm's rpmvercmp): -1, 0 or 1.
func VerCmp(a, b string) int {
	if a == b {
		return 0
	}
	ea, va, ra := splitVer(a)
	eb, vb, rb := splitVer(b)
	if c := rpmVerCmp(ea, eb); c != 0 {
		return c
	}
	if c := rpmVerCmp(va, vb); c != 0 {
		return c
	}
	if ra == "" || rb == "" {
		return 0
	}
	return rpmVerCmp(ra, rb)
}

func splitVer(v string) (epoch, ver, rel string) {
	epoch = "0"
	if i := strings.IndexByte(v, ':'); i > 0 && digits(v[:i]) {
		epoch, v = v[:i], v[i+1:]
	}
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		return epoch, v[:i], v[i+1:]
	}
	return epoch, v, ""
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isAlnum(c byte) bool { return isDigit(c) || isAlpha(c) }

// rpmVerCmp is libalpm's rpmvercmp.
func rpmVerCmp(a, b string) int {
	if a == b {
		return 0
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		si, sj := i, j
		for i < len(a) && !isAlnum(a[i]) {
			i++
		}
		for j < len(b) && !isAlnum(b[j]) {
			j++
		}
		if i >= len(a) || j >= len(b) {
			break
		}
		if i-si != j-sj {
			if i-si < j-sj {
				return -1
			}
			return 1
		}
		si, sj = i, j
		num := isDigit(a[i])
		if num {
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
		} else {
			for i < len(a) && isAlpha(a[i]) {
				i++
			}
			for j < len(b) && isAlpha(b[j]) {
				j++
			}
		}
		x, y := a[si:i], b[sj:j]
		if y == "" {
			if num {
				return 1
			}
			return -1
		}
		if num {
			x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
			if len(x) != len(y) {
				if len(x) > len(y) {
					return 1
				}
				return -1
			}
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	if i >= len(a) && j >= len(b) {
		return 0
	}
	if (i >= len(a) && !isAlpha(at(b, j))) || isAlpha(at(a, i)) {
		return -1
	}
	return 1
}

func at(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}
