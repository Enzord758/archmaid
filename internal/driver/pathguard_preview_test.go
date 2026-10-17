package driver

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/privilege"
)

// TestPreviewKeepOff (#172): the gate sheet's preview gives the guard's
// verdict and reason without its side effects: a clean PATH, a shadowed
// one, then a scan that hangs.
func TestPreviewKeepOff(t *testing.T) {
	save, saveMax := pathShadows, pathScanMax
	t.Cleanup(func() { pathShadows, pathScanMax = save, saveMax })

	pathShadows = func(string) []privilege.Shadow { return nil }
	if got := PreviewKeepOff(); got != "" {
		t.Errorf("clean PATH: %q, want none", got)
	}
	pathShadows = func(string) []privilege.Shadow { return []privilege.Shadow{{Path: "/home/u/.local/bin/sudo"}} }
	if got, want := PreviewKeepOff(), "the PATH check found something that could stand in for sudo"; got != want {
		t.Errorf("shadowed PATH: %q, want %q", got, want)
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	pathShadows = func(string) []privilege.Shadow { <-release; return nil }
	pathScanMax = 20 * time.Millisecond
	if got, want := PreviewKeepOff(), "the PATH check didn't finish in time"; got != want {
		t.Errorf("hung scan: %q, want %q", got, want)
	}
}
