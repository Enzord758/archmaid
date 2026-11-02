package aurbuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

func planFor(base, commit string, names ...string) steps.AURPlan {
	return steps.AURPlan{Base: base, Commit: commit, Names: names}
}

// built is the shim's package file name for a pkgname.
func built(name string) string { return name + "-1.0-1-x86_64.pkg.tar.zst" }

// TestQueueHappyPath runs a two-base queue through the shims: dependency
// first, pinned checkout, makepkg with the right argv and environment,
// collect, then the root install inserted with the files and their hash,
// and the reviewed commits remembered only by the last step.
func TestQueueHappyPath(t *testing.T) {
	s, f := newShims(t)
	for _, b := range []string{"lib", "app"} {
		os.MkdirAll(filepath.Join(f.Root, b), 0o700)
	}
	s.put("make_outputs", built("lib")+"\n"+built("app")+"\n")
	commit := strings.Repeat("a", 40)
	plans := []steps.AURPlan{planFor("lib", commit, "lib"), planFor("app", commit, "app")}
	reviewed := &Reviewed{Path: filepath.Join(t.TempDir(), "state", "aur-reviewed.json")}
	deps := &steps.Step{ID: steps.IDPkgInstall, Verb: "pkg-install", NeedsPriv: true}
	q := f.Queue(plans, deps, reviewed)

	var ids []string
	for _, st := range q {
		ids = append(ids, st.ID)
	}
	want := "pkg-install,aur-fetch:lib,aur-build:lib,aur-fetch:app,aur-build:app,aur-collect,aur-remember"
	if got := strings.Join(ids, ","); got != want {
		t.Fatalf("queue = %s\nwant    %s", got, want)
	}
	if !q[0].NeedsPriv {
		t.Fatal("the repo dependencies step is the caller's root step, unchanged")
	}
	for _, st := range q[1:] {
		if st.NeedsPriv || st.Verb != "" {
			t.Errorf("%s: the user-side steps need no root", st.ID)
		}
	}

	// the build step: exact argv, no forbidden flag, environment under the base's folder
	b := q[2]
	wantArgv := []string{"env", "-C", filepath.Join(f.Root, "lib"), "makepkg", "--clean", "--cleanbuild", "--noprogressbar", "--noconfirm"}
	if strings.Join(b.Argv, " ") != strings.Join(wantArgv, " ") {
		t.Fatalf("argv = %v", b.Argv)
	}
	for _, bad := range []string{"-s", "-i", "-r", "-d", "--syncdeps", "--install", "--rmdeps", "--nodeps", "--skipinteg", "--skippgpcheck", "--skipchecksums", "sudo"} {
		for _, a := range b.Argv {
			if a == bad {
				t.Errorf("makepkg argv has %s", bad)
			}
		}
	}
	wantEnv := []string{"PKGDEST=" + filepath.Join(f.Root, "lib", "out"), "SRCDEST=" + filepath.Join(f.Root, "lib", "src"), "BUILDDIR=" + filepath.Join(f.Root, "lib", "build")}
	if strings.Join(b.Env, " ") != strings.Join(wantEnv, " ") {
		t.Fatalf("env = %v", b.Env)
	}
	if !b.AlwaysLog {
		t.Error("a build is always logged")
	}

	// run the user-side steps in order, as the queue runner does
	var install *steps.Step
	for _, st := range q[1:] {
		res := runStep(t, st)
		if res.Err != nil {
			t.Fatalf("%s: %v\n%s", st.ID, res.Err, res.Output)
		}
		if st.ID == "aur-remember" {
			continue
		}
		if reviewed.Get("app") != "" {
			t.Fatalf("%s: the commit was remembered before the install", st.ID)
		}
		if res.InsertNext != nil {
			install = res.InsertNext
			if st.ID != "aur-collect" {
				t.Fatalf("%s inserted a step", st.ID)
			}
		}
	}
	if install == nil {
		t.Fatal("aur-collect inserted no install step")
	}
	if !strings.Contains(s.log("make.log"), "PWD: "+filepath.Join(f.Root, "lib")) || !strings.Contains(s.log("make.log"), "PWD: "+filepath.Join(f.Root, "app")) {
		t.Fatalf("makepkg ran in the wrong folder:\n%s", s.log("make.log"))
	}
	if install.ID != steps.IDPkgInstallLocal || install.Verb != steps.VerbPkgInstallLocal || !install.NeedsPriv || !install.AlwaysLog {
		t.Fatalf("install = %+v", install)
	}
	if got := strings.Join(install.PkgLocal.Files, " "); got != "lib/out/"+built("lib")+" app/out/"+built("app") {
		t.Fatalf("files (build order, relative to the cache root) = %s", got)
	}
	if strings.Join(install.Items, " ") != "lib app" {
		t.Fatalf("items = %v", install.Items)
	}
	// the hash is over the very files on disk
	var bf []BuiltFile
	for _, n := range []string{"lib", "app"} {
		b, err := os.ReadFile(filepath.Join(f.Root, n, "out", built(n)))
		if err != nil {
			t.Fatal(err)
		}
		bf = append(bf, BuiltFile{Name: built(n), Size: int64(len(b)), Sha256: sha(b)})
	}
	if install.PkgLocal.Hash != LocalHash(bf) {
		t.Fatalf("hash = %s, want %s", install.PkgLocal.Hash, LocalHash(bf))
	}

	// the last step remembers both commits
	last := q[len(q)-1]
	if res := runStep(t, last); res.Err != nil {
		t.Fatal(res.Err)
	}
	if reviewed.Get("lib") != commit || reviewed.Get("app") != commit {
		t.Fatalf("remembered lib %q app %q", reviewed.Get("lib"), reviewed.Get("app"))
	}
}

// TestQueueBuildFailure: a failing makepkg is a failed step, and collect
// then has nothing to install; nothing is remembered.
func TestQueueBuildFailure(t *testing.T) {
	s, f := newShims(t)
	os.MkdirAll(filepath.Join(f.Root, "app"), 0o700)
	s.put("make_fail", "")
	reviewed := &Reviewed{Path: filepath.Join(t.TempDir(), "r.json")}
	q := f.Queue([]steps.AURPlan{planFor("app", strings.Repeat("a", 40), "app")}, nil, reviewed)
	if q[0].ID != "aur-fetch:app" {
		t.Fatalf("no repo deps: the queue starts with the checkout, got %s", q[0].ID)
	}
	if res := runStep(t, q[0]); res.Err != nil {
		t.Fatal(res.Err)
	}
	res := runStep(t, q[1])
	if res.Err == nil || !strings.Contains(res.Output, "A failure occurred in build()") {
		t.Fatalf("build: err %v output %q", res.Err, res.Output)
	}
	// a runner stops at the failed step; if collect ran anyway it finds nothing
	res = runStep(t, q[2])
	if res.Err == nil || res.InsertNext != nil || !strings.Contains(res.Err.Error(), "made no package file for app") {
		t.Fatalf("collect after a failed build: %+v", res)
	}
	if reviewed.Get("app") != "" {
		t.Fatal("a failed build was remembered")
	}
}

// TestPrepareWipesOldOutput: a package file left in out from an earlier run
// (or planted) is gone before the build, so only what this build made can be
// installed.
func TestPrepareWipesOldOutput(t *testing.T) {
	s, f := newShims(t)
	out := filepath.Join(f.Root, "app", "out")
	os.MkdirAll(out, 0o700)
	stale := filepath.Join(out, "app-0.9-1-x86_64.pkg.tar.zst")
	os.WriteFile(stale, []byte("old, maybe planted"), 0o600)
	s.put("make_outputs", built("app")+"\n")
	q := f.Queue([]steps.AURPlan{planFor("app", strings.Repeat("a", 40), "app")}, nil, nil)
	for _, st := range q[:2] {
		if res := runStep(t, st); res.Err != nil {
			t.Fatalf("%s: %v %s", st.ID, res.Err, res.Output)
		}
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("the old package file is still there: %v", err)
	}
	got, err := f.Collect([]steps.AURPlan{planFor("app", "", "app")})
	if err != nil || len(got) != 1 || got[0].Name != built("app") {
		t.Fatalf("collected %+v, %v", got, err)
	}
	for _, d := range []string{"src", "build", "out"} {
		fi, err := os.Stat(filepath.Join(f.Root, "app", d))
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, %v; want a 0700 folder", d, fi, err)
		}
	}
}

// TestPrepareRefusals: the pin step fails when the checkout moved, or a
// build folder is a symlink.
func TestPrepareRefusals(t *testing.T) {
	s, f := newShims(t)
	os.MkdirAll(filepath.Join(f.Root, "app"), 0o700)
	q := f.Queue([]steps.AURPlan{planFor("app", strings.Repeat("a", 40), "app")}, nil, nil)
	s.put("rp_head", strings.Repeat("c", 40)+"\n")
	if res := runStep(t, q[0]); res.Err == nil || !strings.Contains(res.Err.Error(), "not the reviewed") {
		t.Fatalf("moved checkout: %+v", res)
	}
	os.Remove(filepath.Join(s.fix, "rp_head"))
	os.Symlink(t.TempDir(), filepath.Join(f.Root, "app", "out"))
	if res := runStep(t, q[0]); res.Err == nil || !strings.Contains(res.Err.Error(), "symlink") {
		t.Fatalf("symlinked out: %+v", res)
	}
	bad := f.Queue([]steps.AURPlan{planFor("../x", strings.Repeat("a", 40), "x")}, nil, nil)
	if res := runStep(t, bad[0]); res.Err == nil {
		t.Fatal("a bad base name was prepared")
	}
}

// TestQueueDryRun: a dry run changes nothing: no checkout, no wipe, no
// install step, nothing remembered.
func TestQueueDryRun(t *testing.T) {
	s, f := newShims(t)
	out := filepath.Join(f.Root, "app", "out")
	os.MkdirAll(out, 0o700)
	keep := filepath.Join(out, "keep")
	os.WriteFile(keep, []byte("x"), 0o600)
	reviewed := &Reviewed{Path: filepath.Join(t.TempDir(), "r.json")}
	q := f.Queue([]steps.AURPlan{planFor("app", strings.Repeat("a", 40), "app")}, nil, reviewed)
	for _, st := range q {
		if st.Run == nil {
			continue
		}
		res := st.Run(context.Background(), steps.Env{DryRun: true})
		if res.Err != nil || res.InsertNext != nil {
			t.Fatalf("%s: %+v", st.ID, res)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("a dry run wiped the out folder")
	}
	if len(s.gitCalls()) != 0 {
		t.Fatalf("a dry run started git: %v", s.gitCalls())
	}
	if _, err := os.Stat(reviewed.Path); !os.IsNotExist(err) {
		t.Fatal("a dry run wrote the reviewed state")
	}
}

// TestCollectMultiPkgname: a base with several pkgnames is built once; only
// the pkgnames the plan names are collected. A -debug package stays behind.
func TestCollectMultiPkgname(t *testing.T) {
	s, f := newShims(t)
	_ = s
	out := filepath.Join(f.Root, "multi", "out")
	os.MkdirAll(out, 0o700)
	for _, n := range []string{"multi", "multi-extra", "multi-docs", "multi-debug"} {
		os.WriteFile(filepath.Join(out, built(n)), []byte("pkg "+n), 0o600)
	}
	only, err := f.Collect([]steps.AURPlan{planFor("multi", "", "multi-extra")})
	if err != nil || len(only) != 1 || only[0].Pkgname != "multi-extra" || only[0].Rel != "multi/out/"+built("multi-extra") {
		t.Fatalf("requested multi-extra only: %+v, %v", only, err)
	}
	two, err := f.Collect([]steps.AURPlan{planFor("multi", "", "multi", "multi-docs")})
	if err != nil || len(two) != 2 {
		t.Fatalf("two names: %+v, %v", two, err)
	}
	for _, bf := range two {
		if strings.Contains(bf.Name, "debug") || strings.Contains(bf.Name, "extra") {
			t.Fatalf("collected %s, which was not asked for", bf.Name)
		}
	}
	// "multi" must not match "multi-extra"'s file: names are taken from the tail of the file name
	if two[0].Pkgname != "multi" || two[0].Name != built("multi") {
		t.Fatalf("first = %+v", two[0])
	}
}

func TestCollectErrors(t *testing.T) {
	_, f := newShims(t)
	out := filepath.Join(f.Root, "app", "out")
	os.MkdirAll(out, 0o700)
	one := func() []steps.AURPlan { return []steps.AURPlan{planFor("app", "", "app")} }
	if _, err := f.Collect(one()); err == nil || !strings.Contains(err.Error(), "made no package file for app") {
		t.Fatalf("empty out: %v", err)
	}
	os.WriteFile(filepath.Join(out, "app-1.0-1-x86_64.pkg.tar.gz"), []byte("x"), 0o600)
	if _, err := f.Collect(one()); err == nil || !strings.Contains(err.Error(), "PKGEXT") {
		t.Fatalf("a .gz package: %v", err)
	}
	os.WriteFile(filepath.Join(out, built("app")), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(out, "app-2.0-1-x86_64.pkg.tar.xz"), []byte("y"), 0o600)
	if _, err := f.Collect(one()); err == nil || !strings.Contains(err.Error(), "two files for app") {
		t.Fatalf("two files: %v", err)
	}
	os.Remove(filepath.Join(out, "app-2.0-1-x86_64.pkg.tar.xz"))
	// a symlink in place of the file
	os.Remove(filepath.Join(out, built("app")))
	os.Symlink("/etc/hostname", filepath.Join(out, built("app")))
	if _, err := f.Collect(one()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlinked package: %v", err)
	}
	// a directory with that name
	os.Remove(filepath.Join(out, built("app")))
	os.Mkdir(filepath.Join(out, built("app")), 0o700)
	if _, err := f.Collect(one()); err == nil {
		t.Fatal("a directory was collected")
	}
	// out missing, out a symlink
	if _, err := f.Collect([]steps.AURPlan{planFor("nothere", "", "x")}); err == nil || !strings.Contains(err.Error(), "made no packages") {
		t.Fatalf("no out folder: %v", err)
	}
	os.RemoveAll(out)
	os.Symlink(t.TempDir(), out)
	if _, err := f.Collect(one()); err == nil {
		t.Fatal("a symlinked out folder was collected from")
	}
}

// TestLocalHash pins the text the root verb must reproduce: a golden value,
// order independence, and sensitivity to name, size and content.
func TestLocalHash(t *testing.T) {
	a := BuiltFile{Name: "a-1-1-any.pkg.tar.zst", Size: 3, Sha256: strings.Repeat("a", 64)}
	b := BuiltFile{Name: "b-1-1-any.pkg.tar.zst", Size: 5, Sha256: strings.Repeat("b", 64)}
	got := LocalHash([]BuiltFile{a, b})
	if got != LocalHash([]BuiltFile{b, a}) {
		t.Fatal("the hash depends on the order of the files")
	}
	text := "archmaid pkg local v1\nfile " + a.Name + " 3 " + a.Sha256 + "\nfile " + b.Name + " 5 " + b.Sha256 + "\n"
	if want := "sha256:" + sha([]byte(text)); got != want {
		t.Fatalf("hash = %s, want %s (sha256 of %q)", got, want, text)
	}
	for name, mut := range map[string]func(BuiltFile) BuiltFile{
		"name":    func(f BuiltFile) BuiltFile { f.Name = "c" + f.Name; return f },
		"size":    func(f BuiltFile) BuiltFile { f.Size++; return f },
		"content": func(f BuiltFile) BuiltFile { f.Sha256 = strings.Repeat("c", 64); return f },
	} {
		if LocalHash([]BuiltFile{mut(a), b}) == got {
			t.Errorf("changing the %s does not change the hash", name)
		}
	}
	if LocalHash([]BuiltFile{a}) == got || LocalHash(nil) == got {
		t.Error("the file set is not part of the hash")
	}
	if !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
		t.Errorf("hash shape: %s", got)
	}
}

// TestRememberRefusesWithoutAGoodInstall: aur-remember writes only when every
// step before it ended OK and the install step was planned; otherwise it
// fails, says it did not record, and the file stays empty.
func TestRememberRefusesWithoutAGoodInstall(t *testing.T) {
	s, f := newShims(t)
	os.MkdirAll(filepath.Join(f.Root, "app"), 0o700)
	s.put("make_outputs", built("app")+"\n")
	commit := strings.Repeat("a", 40)
	reviewed := &Reviewed{Path: filepath.Join(t.TempDir(), "r.json")}
	q := f.Queue([]steps.AURPlan{planFor("app", commit, "app")}, nil, reviewed)
	remember := q[len(q)-1]
	// an earlier step failed
	res := remember.Run(context.Background(), steps.Env{PriorOK: false})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "not recorded") {
		t.Fatalf("after a failure: %+v", res)
	}
	// every step OK, but collect never planned an install
	res = remember.Run(context.Background(), steps.Env{PriorOK: true})
	if res.Err == nil {
		t.Fatalf("without an install step: %+v", res)
	}
	if reviewed.Get("app") != "" {
		t.Fatal("remembered without a good install")
	}
	// the dry run writes nothing and says so
	if res := remember.Run(context.Background(), steps.Env{DryRun: true, PriorOK: true}); res.Err != nil || !strings.Contains(res.Output, "dry-run") {
		t.Fatalf("dry run: %+v", res)
	}
	for _, st := range q[:len(q)-1] {
		if r := runStep(t, st); r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	res = remember.Run(context.Background(), steps.Env{PriorOK: true})
	if res.Err != nil || !strings.Contains(res.Output, "recorded app") || reviewed.Get("app") != commit {
		t.Fatalf("after a good install: %+v", res)
	}
}
