// Package helpertest gives other packages' tests what internal/helperclient
// keeps out of its production API: a stand-in for the root helper's install
// check, so Run gets past it to a pkexec shim when the helper isn't installed
// (CI, a developer's machine). It must only be imported from _test.go files;
// outside a test binary every function panics.
package helpertest

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/helperclient/internal/seam"
)

func testOnly() {
	if !testing.Testing() {
		panic("helpertest is for tests only")
	}
}

// UseHelperFile makes helperclient.Run's install check stat path (a stand-in
// file the caller made) instead of helperclient.HelperPath, until t ends.
// Only the check moves: the command Run starts is still
// pkexec helperclient.HelperPath …, the path polkit's policy pins, so a
// pkexec shim sees the real one.
func UseHelperFile(t testing.TB, path string) {
	t.Helper()
	testOnly()
	if path == "" {
		panic("helpertest.UseHelperFile: empty path")
	}
	t.Cleanup(seam.SetHelperFile(path))
}
