package report

import (
	"fmt"
	"strconv"
	"strings"
)

// HumanBytes writes n the way the summary shows sizes: binary units, three
// significant digits ("0 B", "54 MiB", "212 MiB", "1.83 GiB", "4.2 GiB"),
// no trailing zeros.
func HumanBytes(n uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	var s string
	switch {
	case v >= 99.5:
		s = strconv.FormatFloat(v, 'f', 0, 64)
	case v >= 9.995:
		s = strconv.FormatFloat(v, 'f', 1, 64)
	default:
		s = strconv.FormatFloat(v, 'f', 2, 64)
	}
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "1024" && i < len(units)-1 { // 1023.6 KiB rounds up to 1024: say 1 MiB
		return "1 " + units[i+1]
	}
	return s + " " + units[i]
}
