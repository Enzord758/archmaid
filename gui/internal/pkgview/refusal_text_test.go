package pkgview

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// TestRefusalTextReadsWell (B4): no blank name, a name where the helper's
// detail lacks one, a full stop before "Nothing changed.".
func TestRefusalTextReadsWell(t *testing.T) {
	for _, c := range []struct {
		r    steps.PkgRefusal
		req  []string
		want string
	}{
		{steps.PkgRefusal{Reason: steps.RefuseRequired}, []string{"sudo"},
			"Other installed packages need it. Nothing changed."},
		{steps.PkgRefusal{Reason: steps.RefuseRequired, Names: []string{"a"}},
			nil, "a is still needed by other installed packages. Nothing changed."},
		{steps.PkgRefusal{Reason: steps.RefuseConflict, Names: []string{"vim"}, Detail: "neovim replaces the installed package vim: remove vim first, then install"},
			[]string{"neovim"}, "neovim replaces the installed package vim: remove vim first, then install neovim. Nothing changed."},
		{steps.PkgRefusal{Reason: steps.RefuseProtected, Names: []string{"sudo"}, Detail: "sudo is protected (privilege) and is never removed here"},
			[]string{"sudo"}, "sudo is protected (privilege) and is never removed here. Nothing changed."},
		{steps.PkgRefusal{Reason: steps.RefuseProtected},
			nil, "A protected package would be removed, which could leave the system unusable. Nothing changed."},
		{steps.PkgRefusal{Reason: steps.RefuseNotInstalled}, nil, "It is not installed. Nothing changed."},
	} {
		if got := RefusalTextFor(&c.r, c.req); got != c.want {
			t.Errorf("%v:\n got %q\nwant %q", c.r.Reason, got, c.want)
		}
	}
}
