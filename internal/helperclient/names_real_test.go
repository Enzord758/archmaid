package helperclient

import "testing"

// TestRealPackageNamesPassTheClient: names real packages use pass the
// client's copy of the helper's pattern too (the user side checks parsed
// names with actions.PackageNameOK, the client re-checks before sending).
func TestRealPackageNamesPassTheClient(t *testing.T) {
	for _, n := range []string{"lib32-gcc-libs", "python-pyqt5", "gtk+", "xorg-server-xvfb", "r", "0ad", "ttf-font-awesome@5", "qt6.base", "libc++"} {
		if !namePattern.MatchString(n) {
			t.Errorf("real package name %q refused by the client", n)
		}
	}
}
