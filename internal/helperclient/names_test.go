package helperclient

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
)

// TestActionsPackageNameIsTheClients: the user side checks the names it
// parses out of pacman with actions.PackageNameOK, which must say what this
// package's namePattern (and so the helper's) says.
func TestActionsPackageNameIsTheClients(t *testing.T) {
	for _, s := range []string{"", "a", "libfoo", "c++", "@x", "_x", "+x", "-x", ".x", "a b", "a\x1b[0m", "a\n", "a\x00", "Foo", "a/b",
		"é", strings.Repeat("a", 128), strings.Repeat("a", 129), "a;b", "a$b", "a=b"} {
		if got, want := actions.PackageNameOK(s), namePattern.MatchString(s); got != want {
			t.Errorf("%q: actions says %v, the client says %v", s, got, want)
		}
	}
}
