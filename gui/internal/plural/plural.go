// Package plural writes the GUI's counts: "1 step", "2 steps", never
// "1 steps" or "step(s)".
package plural

import "strconv"

// N is n followed by one when n is 1 (or -1), else by many: N(1, "step",
// "steps") is "1 step", N(0, "step", "steps") is "0 steps".
func N(n int, one, many string) string {
	if n == 1 || n == -1 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + many
}

// Refused is "5 steps not authorised": how every place that reports a run
// (the Run page header, the toast, the Overview, the DMS widget) counts the
// steps polkit or sudo refused. The DMS widget's state.js writes the same
// words.
func Refused(n int) string {
	return N(n, "step", "steps") + " not authorised"
}

// AlsoRefused is Refused after " · ", for a run that ended some other way
// too ("failed (1 step) · 5 steps not authorised"), or "" when none was
// refused.
func AlsoRefused(n int) string {
	if n <= 0 {
		return ""
	}
	return " · " + Refused(n)
}
