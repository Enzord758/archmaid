package actions

import (
	"fmt"
	"regexp"
)

// packageName is the root helper's package-name pattern
// (cmd/archmaid-helper's namePattern; internal/helperclient has a copy too,
// and a test there ties the two to this one by behaviour). It lets no name
// start with '-', hold a space, a control character or an escape.
var packageName = regexp.MustCompile(`^[A-Za-z0-9@_+][A-Za-z0-9@._+-]{0,127}$`)

// PackageNameOK reports whether s is a package name the helper would take.
func PackageNameOK(s string) bool { return packageName.MatchString(s) }

// checkPackageNames refuses a list holding a name that is not one. Names
// parsed out of pacman's output reach a `sudo pacman ... -- <names>` argv
// (the user side has no helper to check them), so each is checked before
// that argv is built. The error quotes the bad name with %q, so an escape
// shows as \x1b and never reaches a terminal.
func checkPackageNames(what string, names []string) error {
	for _, n := range names {
		if !packageName.MatchString(n) {
			return fmt.Errorf("%s: %q is not a valid package name; nothing will be run with it", what, n)
		}
	}
	return nil
}
