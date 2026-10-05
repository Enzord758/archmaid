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
