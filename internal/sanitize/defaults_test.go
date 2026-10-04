package sanitize

import "testing"

// TestZeroModeIsPlain: a caller that leaves the Mode unset gets the strict
// one. Keeping colours (and with them any SGR an untrusted source sends) has
// to be asked for.
func TestZeroModeIsPlain(t *testing.T) {
	var unset Mode
	const in = "\x1b[31mred\x1b[0m"
	if got := String(in, unset); got != "red" {
		t.Fatalf("String(%q, Mode(0)) = %q, want %q (Plain)", in, got, "red")
	}
	if unset != Plain || KeepSGR == Plain {
		t.Fatalf("Mode(0) = %d, Plain = %d, KeepSGR = %d; want the zero value to be Plain", unset, Plain, KeepSGR)
	}
}
