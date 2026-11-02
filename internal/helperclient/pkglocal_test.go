package helperclient

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/aurbuild"
	"github.com/Enzord758/archmaid/internal/steps"
)

func localHex(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

func localBuilt() []aurbuild.BuiltFile {
	return []aurbuild.BuiltFile{
		{Pkgname: "app", Name: "app-1.0-1-x86_64.pkg.tar.zst", Rel: "app/out/app-1.0-1-x86_64.pkg.tar.zst", Size: 1234, Sha256: localHex("app")},
		{Pkgname: "lib", Name: "lib-2:3.1_rc1-2-any.pkg.tar.xz", Rel: "lib/out/lib-2:3.1_rc1-2-any.pkg.tar.xz", Size: 99, Sha256: localHex("lib")},
	}
}

// TestPkgLocalHashTiesToAurbuild: the client's hash is aurbuild.LocalHash's
// for the same files, in any order, and changes with a name, size or content.
func TestPkgLocalHashTiesToAurbuild(t *testing.T) {
	built := localBuilt()
	toLocal := func(bs []aurbuild.BuiltFile) []LocalFile {
		var out []LocalFile
		for _, b := range bs {
			out = append(out, LocalFile{Name: b.Name, Size: b.Size, Sha256: b.Sha256})
		}
		return out
	}
	want := aurbuild.LocalHash(built)
	if got := PkgLocalHash(toLocal(built)); got != want {
		t.Fatalf("PkgLocalHash %s\naurbuild   %s", got, want)
	}
	rev := slices.Clone(built)
	slices.Reverse(rev)
	if PkgLocalHash(toLocal(rev)) != want {
		t.Error("the hash depends on the order")
	}
	for name, mut := range map[string]func(*aurbuild.BuiltFile){
		"name":    func(b *aurbuild.BuiltFile) { b.Name = "other-1.0-1-x86_64.pkg.tar.zst" },
		"size":    func(b *aurbuild.BuiltFile) { b.Size++ },
		"content": func(b *aurbuild.BuiltFile) { b.Sha256 = localHex("changed") },
	} {
		c := slices.Clone(built)
		mut(&c[0])
		if PkgLocalHash(toLocal(c)) == want {
			t.Errorf("the hash ignores the %s", name)
		}
	}
	// the text, spelled out: Dev 3's verb must hash exactly this
	one := []LocalFile{{Name: "a-1-1-any.pkg.tar.zst", Size: 7, Sha256: strings.Repeat("ab", 32)}}
	h := sha256.Sum256([]byte("archmaid pkg local v1\nfile a-1-1-any.pkg.tar.zst 7 " + strings.Repeat("ab", 32) + "\n"))
	if got := PkgLocalHash(one); got != fmt.Sprintf("sha256:%x", h) {
		t.Errorf("hash text drifted: %s", got)
	}
}

func localStep(l *steps.PkgLocal) steps.Step {
	return steps.Step{ID: steps.IDPkgInstallLocal, Verb: PkgVerbInstallLocal, PkgLocal: l, Items: []string{"app"}}
}

// TestPkgLocalArgv: the argv is pkexec, the helper, the verb, H, then the
// files in the given (build) order; aurbuild's own install step passes.
func TestPkgLocalArgv(t *testing.T) {
	built := localBuilt()
	st := aurbuild.InstallStep(built)
	got, err := Argv(st)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{Pkexec, DisableInternalAgent, HelperPath, "pkg-install-local", aurbuild.LocalHash(built),
		"app/out/app-1.0-1-x86_64.pkg.tar.zst", "lib/out/lib-2:3.1_rc1-2-any.pkg.tar.xz"}
	if !slices.Equal(got, want) {
		t.Errorf("argv = %q\nwant   %q", got, want)
	}
}

func TestPkgLocalArgvRefuses(t *testing.T) {
	h := aurbuild.LocalHash(localBuilt())
	ok := "app/out/app-1.0-1-x86_64.pkg.tar.zst"
	many := &steps.PkgLocal{Hash: h}
	for i := 0; i <= MaxLocalFiles; i++ {
		many.Files = append(many.Files, fmt.Sprintf("b%d/out/b%d-1-1-any.pkg.tar.zst", i, i))
	}
	other := localStep(&steps.PkgLocal{Hash: h, Files: []string{ok}})
	other.Verb = steps.VerbPaccache
	for name, tc := range map[string]struct {
		st   steps.Step
		want error
	}{
		"no files struct": {localStep(nil), ErrUnsupported},
		"no files":        {localStep(&steps.PkgLocal{Hash: h}), ErrBadItems},
		"too many":        {localStep(many), ErrBadItems},
		"bad hash":        {localStep(&steps.PkgLocal{Hash: "sha256:xyz", Files: []string{ok}}), ErrBadItems},
		"hash w/o prefix": {localStep(&steps.PkgLocal{Hash: strings.TrimPrefix(h, "sha256:"), Files: []string{ok}}), ErrBadItems},
		"twice":           {localStep(&steps.PkgLocal{Hash: h, Files: []string{ok, ok}}), ErrBadItems},
		"option as file":  {localStep(&steps.PkgLocal{Hash: h, Files: []string{"--overwrite=*"}}), ErrBadItems},
		"absolute":        {localStep(&steps.PkgLocal{Hash: h, Files: []string{"/etc/x/out/a-1-1-any.pkg.tar.zst"}}), ErrBadItems},
		"dotdot":          {localStep(&steps.PkgLocal{Hash: h, Files: []string{"../out/a-1-1-any.pkg.tar.zst"}}), ErrBadItems},
		"dotdot base":     {localStep(&steps.PkgLocal{Hash: h, Files: []string{"../x/out/a-1-1-any.pkg.tar.zst"}}), ErrBadItems},
		"not in out":      {localStep(&steps.PkgLocal{Hash: h, Files: []string{"app/build/a-1-1-any.pkg.tar.zst"}}), ErrBadItems},
		"deeper":          {localStep(&steps.PkgLocal{Hash: h, Files: []string{"app/out/x/a-1-1-any.pkg.tar.zst"}}), ErrBadItems},
		"not a package":   {localStep(&steps.PkgLocal{Hash: h, Files: []string{"app/out/a-1-1-any.tar.gz"}}), ErrBadItems},
		"signature":       {localStep(&steps.PkgLocal{Hash: h, Files: []string{"app/out/a-1-1-any.pkg.tar.zst.sig"}}), ErrBadItems},
		"no arch":         {localStep(&steps.PkgLocal{Hash: h, Files: []string{"app/out/a-1.pkg.tar.zst"}}), ErrBadItems},
		"space":           {localStep(&steps.PkgLocal{Hash: h, Files: []string{"app/out/a b-1-1-any.pkg.tar.zst"}}), ErrBadItems},
		"newline":         {localStep(&steps.PkgLocal{Hash: h, Files: []string{"app/out/a-1-1-any.pkg.tar.zst\n"}}), ErrBadItems},
		"files on a verb": {other, ErrBadItems},
		"plan on the verb": {func() steps.Step {
			s := localStep(&steps.PkgLocal{Hash: h, Files: []string{ok}})
			s.PkgPlan = &steps.PkgPlan{}
			return s
		}(), ErrBadPlan},
	} {
		if _, err := Argv(tc.st); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v; want %v", name, err, tc.want)
		}
	}
}
