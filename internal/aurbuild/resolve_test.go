package aurbuild

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

func baseNames(r Resolution) []string {
	var out []string
	for _, b := range r.Bases {
		out = append(out, b.Name)
	}
	return out
}

func wantRefusal(t *testing.T, err error, reason Reason, contains string) {
	t.Helper()
	r, ok := IsRefusal(err)
	if !ok {
		t.Fatalf("err = %v, want a *Refusal(%s)", err, reason)
	}
	if r.Reason != reason || !strings.Contains(r.Detail, contains) {
		t.Fatalf("refusal = %s %q, want %s containing %q", r.Reason, r.Detail, reason, contains)
	}
}

// TestResolveClosureAndOrder: a runtime dependency that only the AUR has is
// built first; repo dependencies and makedepends go to RepoDeps; a dependency
// an installed package meets is skipped and listed as skipped.
func TestResolveClosureAndOrder(t *testing.T) {
	db := fixtureDB(t,
		[]pkgdb.Package{syncPkg("cmake", "3.30-1"), syncPkg("libfoo", "2.0-1"), syncPkg("glibc", "2.40-1")},
		[]pkgdb.Package{instPkg("glibc", "2.40-1")})
	aur := aurClient(t, nil,
		aurPkg{Name: "app", Version: "1-1", Depends: []string{"applib", "libfoo>=1.5", "glibc>=2.38"}, Makedepends: []string{"cmake"}},
		aurPkg{Name: "applib", Version: "3-1", Depends: []string{"libfoo"}})
	res, err := Resolve(context.Background(), []string{"app"}, db, aur)
	if err != nil {
		t.Fatal(err)
	}
	if got := baseNames(res); !reflect.DeepEqual(got, []string{"applib", "app"}) {
		t.Fatalf("build order = %v, want applib before app", got)
	}
	if !reflect.DeepEqual(res.RepoDeps, []string{"cmake", "libfoo"}) {
		t.Fatalf("RepoDeps = %v", res.RepoDeps)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != (Skip{For: "app", Dep: "glibc>=2.38", By: "glibc"}) {
		t.Fatalf("Skipped = %+v", res.Skipped)
	}
	if b := res.Bases[1]; !reflect.DeepEqual(b.After, []string{"applib"}) || !reflect.DeepEqual(b.Targets, []string{"app"}) {
		t.Fatalf("app = %+v", b)
	}
	if b := res.Bases[0]; len(b.Targets) != 0 || !reflect.DeepEqual(b.Names, []string{"applib"}) {
		t.Fatalf("applib is a dependency, not a target: %+v", b)
	}
}

// TestResolveSkipsProvidedDeps: an installed package that provides the
// dependency meets it (the version of the provides entry counts); a provides
// without a version does not meet a versioned dependency.
func TestResolveSkipsProvidedDeps(t *testing.T) {
	db := fixtureDB(t, nil, []pkgdb.Package{
		instPkg("jdk21", "21-1", "java-runtime=21", "java-environment"),
	})
	aur := aurClient(t, nil,
		aurPkg{Name: "ok", Version: "1-1", Depends: []string{"java-runtime>=17"}},
		aurPkg{Name: "bare", Version: "1-1", Depends: []string{"java-environment"}},
		aurPkg{Name: "versioned", Version: "1-1", Depends: []string{"java-environment>=17"}})
	res, err := Resolve(context.Background(), []string{"ok", "bare"}, db, aur)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 2 || res.Skipped[0].By != "jdk21" || len(res.RepoDeps) != 0 {
		t.Fatalf("res = %+v", res)
	}
	_, err = Resolve(context.Background(), []string{"versioned"}, db, aur)
	wantRefusal(t, err, ReasonUnmet, "java-environment")
}

// TestResolveUnmetVirtual: nothing installed or in a repo or the AUR by that
// name is a refusal that says virtual dependencies are not supported.
func TestResolveUnmetVirtual(t *testing.T) {
	db := fixtureDB(t, []pkgdb.Package{syncPkg("a", "1-1")}, nil)
	aur := aurClient(t, nil, aurPkg{Name: "app", Version: "1-1", Depends: []string{"some-virtual"}})
	_, err := Resolve(context.Background(), []string{"app"}, db, aur)
	wantRefusal(t, err, ReasonUnmet, "some-virtual")
	if r, _ := IsRefusal(err); !strings.Contains(r.Detail, "virtual") || r.Dep != "some-virtual" {
		t.Fatalf("refusal = %+v", r)
	}
}

// TestResolveAURMakedependsRefused: a makedepends (or checkdepends) only the
// AUR has is refused with the reason, in v1.
func TestResolveAURMakedependsRefused(t *testing.T) {
	db := fixtureDB(t, nil, nil)
	for _, p := range []aurPkg{
		{Name: "app", Version: "1-1", Makedepends: []string{"buildtool"}},
		{Name: "app", Version: "1-1", Checkdepends: []string{"buildtool"}},
	} {
		aur := aurClient(t, nil, p, aurPkg{Name: "buildtool", Version: "1-1"})
		_, err := Resolve(context.Background(), []string{"app"}, db, aur)
		wantRefusal(t, err, ReasonAURMakedepends, "buildtool")
	}
	// An installed AUR package meets it (the dependency is not an AUR one then).
	db = fixtureDB(t, nil, []pkgdb.Package{instPkg("buildtool", "1-1")})
	aur := aurClient(t, nil, aurPkg{Name: "app", Version: "1-1", Makedepends: []string{"buildtool"}})
	if _, err := Resolve(context.Background(), []string{"app"}, db, aur); err != nil {
		t.Fatalf("installed makedepends: %v", err)
	}
}

// TestResolveCycle: a circle of runtime dependencies between bases is refused;
// two pkgnames of one base needing each other are not a cycle.
func TestResolveCycle(t *testing.T) {
	db := fixtureDB(t, nil, nil)
	aur := aurClient(t, nil,
		aurPkg{Name: "a", Version: "1-1", Depends: []string{"b"}},
		aurPkg{Name: "b", Version: "1-1", Depends: []string{"c"}},
		aurPkg{Name: "c", Version: "1-1", Depends: []string{"a"}})
	_, err := Resolve(context.Background(), []string{"a"}, db, aur)
	wantRefusal(t, err, ReasonCycle, "a, b, c")

	aur = aurClient(t, nil,
		aurPkg{Name: "x", Base: "xbase", Version: "1-1", Depends: []string{"x-lib"}},
		aurPkg{Name: "x-lib", Base: "xbase", Version: "1-1", Depends: []string{"x"}})
	res, err := Resolve(context.Background(), []string{"x"}, db, aur)
	if err != nil || len(res.Bases) != 1 || !reflect.DeepEqual(res.Bases[0].Names, []string{"x", "x-lib"}) {
		t.Fatalf("one base, two names: %+v, %v", res, err)
	}
}

// TestResolveTargets: bad names, duplicates, a name in a repo, and a name the
// AUR does not know are refused before anything is fetched.
func TestResolveTargets(t *testing.T) {
	db := fixtureDB(t, []pkgdb.Package{syncPkg("vim", "9-1")}, nil)
	calls := 0
	aur := aurClient(t, &calls, aurPkg{Name: "real", Version: "1-1"})
	ctx := context.Background()
	for _, tc := range []struct {
		names  []string
		reason Reason
	}{
		{nil, ReasonBadRequest},
		{[]string{"-rf"}, ReasonBadRequest},
		{[]string{"real", "real"}, ReasonBadRequest},
		{make([]string, MaxTargets+1), ReasonBadRequest},
		{[]string{"vim"}, ReasonInRepo},
		{[]string{"ghost"}, ReasonNotFound},
	} {
		_, err := Resolve(ctx, tc.names, db, aur)
		if r, ok := IsRefusal(err); !ok || r.Reason != tc.reason {
			t.Fatalf("%v: err = %v, want %s", tc.names, err, tc.reason)
		}
	}
	if calls != 1 { // only "ghost" reached the AUR
		t.Fatalf("AUR requests = %d, want 1 (the others are refused before any)", calls)
	}
}

// TestResolveVersionConstraints: a repo or AUR dependency whose version does
// not meet the constraint is refused, not installed and found broken later.
func TestResolveVersionConstraints(t *testing.T) {
	db := fixtureDB(t, []pkgdb.Package{syncPkg("libfoo", "1.0-1")}, nil)
	ctx := context.Background()
	aur := aurClient(t, nil, aurPkg{Name: "app", Version: "1-1", Depends: []string{"libfoo>=2"}})
	_, err := Resolve(ctx, []string{"app"}, db, aur)
	wantRefusal(t, err, ReasonVersion, "libfoo>=2")

	aur = aurClient(t, nil,
		aurPkg{Name: "app", Version: "1-1", Depends: []string{"lib>=2"}},
		aurPkg{Name: "lib", Version: "1.5-1"})
	_, err = Resolve(ctx, []string{"app"}, db, aur)
	wantRefusal(t, err, ReasonVersion, "lib>=2")
}

// TestResolveLimits: a closure over MaxAUR packages is refused.
func TestResolveLimits(t *testing.T) {
	db := fixtureDB(t, nil, nil)
	var pkgs []aurPkg
	for i := 0; i <= MaxAUR; i++ {
		p := aurPkg{Name: "p" + string(rune('a'+i)), Version: "1-1"}
		if i < MaxAUR {
			p.Depends = []string{"p" + string(rune('a'+i+1))}
		}
		pkgs = append(pkgs, p)
	}
	_, err := Resolve(context.Background(), []string{"pa"}, db, aurClient(t, nil, pkgs...))
	wantRefusal(t, err, ReasonTooLarge, "AUR packages")
}

// TestResolveAURDown: an unreachable AUR is an error, not a refusal.
func TestResolveAURDown(t *testing.T) {
	db := fixtureDB(t, nil, nil)
	aur := aurClient(t, nil)
	// a client on a closed server
	dead := aurClientClosed(t)
	_ = aur
	_, err := Resolve(context.Background(), []string{"x"}, db, dead)
	if err == nil {
		t.Fatal("no error")
	}
	if _, ok := IsRefusal(err); ok {
		t.Fatalf("a network failure is not a refusal: %v", err)
	}
}

// TestVerify: the fetched .SRCINFO must not list a dependency the AUR index
// did not, nor lack a requested pkgname, nor be another base's.
func TestVerify(t *testing.T) {
	db := fixtureDB(t, []pkgdb.Package{syncPkg("libfoo", "1-1"), syncPkg("make", "4-1")}, nil)
	aur := aurClient(t, nil, aurPkg{Name: "app", Version: "1-1", Depends: []string{"libfoo"}, Makedepends: []string{"make"}})
	res, err := Resolve(context.Background(), []string{"app"}, db, aur)
	if err != nil {
		t.Fatal(err)
	}
	good := "pkgbase = app\n\tpkgver = 1\n\tpkgrel = 1\n\tmakedepends = make\n\tdepends = libfoo>=1\n\npkgname = app\n"
	si, err := ParseSrcinfo(good)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Verify(res.Bases[0], si); err != nil {
		t.Fatalf("matching .SRCINFO: %v", err)
	}
	for name, text := range map[string]string{
		"extra dep":    "pkgbase = app\n\tpkgver = 1\n\tpkgrel = 1\n\tdepends = libfoo\n\tdepends = curl\n\npkgname = app\n",
		"extra make":   "pkgbase = app\n\tpkgver = 1\n\tpkgrel = 1\n\tmakedepends = gcc\n\npkgname = app\n",
		"own depends":  "pkgbase = app\n\tpkgver = 1\n\tpkgrel = 1\n\npkgname = app\n\tdepends = evil\n",
		"other base":   "pkgbase = other\n\tpkgver = 1\n\tpkgrel = 1\n\npkgname = app\n",
		"missing name": "pkgbase = app\n\tpkgver = 1\n\tpkgrel = 1\n\npkgname = another\n",
	} {
		si, err := ParseSrcinfo(text)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		wantRefusal(t, res.Verify(res.Bases[0], si), ReasonSrcinfo, "")
	}
}
