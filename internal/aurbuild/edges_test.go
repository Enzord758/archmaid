package aurbuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

func TestDefaultsAndRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/cache")
	if r, err := DefaultRoot(); err != nil || r != "/cache/archmaid/aur" {
		t.Fatalf("DefaultRoot = %q, %v", r, err)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	if _, err := DefaultRoot(); err == nil {
		t.Fatal("no cache dir at all must be an error")
	}
	f := Fetcher{Root: "/x"}
	if u := f.url("foo"); u != "https://aur.archlinux.org/foo.git" {
		t.Fatalf("url = %s", u)
	}
	if h := strings.Join(f.hardening(), " "); !strings.Contains(h, "protocol.https.allow=always") || !strings.Contains(h, "protocol.allow=never") {
		t.Fatalf("hardening = %s", h)
	}
	t.Setenv("HTTPS_PROXY", "http://proxy:3128")
	if !strings.Contains(strings.Join(f.env(), " "), "HTTPS_PROXY=http://proxy:3128") {
		t.Fatal("the proxy variable is not handed to git")
	}
	if got := (&Refusal{Reason: ReasonCycle, Detail: "x"}).Error(); !strings.Contains(got, "cycle") {
		t.Fatalf("Error = %q", got)
	}
}

func TestSatisfies(t *testing.T) {
	for _, tc := range []struct {
		raw, ver string
		want     bool
	}{
		{"foo", "", true},
		{"foo>=1", "", false},
		{"foo>=1.0", "1.0-2", true},
		{"foo>=1.1", "1.0-2", false},
		{"foo<=1.0", "1.0-9", true},
		{"foo<=0.9", "1.0-9", false},
		{"foo=1.0", "1.0-3", true},
		{"foo=1.0-2", "1.0-3", false},
		{"foo=1.0-3", "1.0-3", true},
		{"foo>1.0", "1.1-1", true},
		{"foo>1.0", "1.0-1", false},
		{"foo<2", "1.9-1", true},
		{"foo<2", "2.0-1", false},
		{"foo~2", "2.0-1", false},
	} {
		if got := satisfies(tc.raw, "foo", tc.ver); got != tc.want {
			t.Errorf("satisfies(%q, %q) = %v, want %v", tc.raw, tc.ver, got, tc.want)
		}
	}
}

// TestResolveInstalledOnlyAndLookup: an installed package with no sync entry
// (foreign) meets a dependency; a repo package whose name an AUR target
// shares is refused; a dependency installed at the wrong version is refused
// by the repo's version.
func TestResolveInstalledEdges(t *testing.T) {
	ctx := context.Background()
	db := fixtureDB(t, []pkgdb.Package{syncPkg("libold", "1.0-1")}, []pkgdb.Package{instPkg("mylocal", "3-1"), instPkg("libold", "0.5-1")})
	aur := aurClient(t, nil,
		aurPkg{Name: "app", Version: "1-1", Depends: []string{"mylocal>=2"}},
		aurPkg{Name: "needs-new", Version: "1-1", Depends: []string{"libold>=2"}})
	res, err := Resolve(ctx, []string{"app"}, db, aur)
	if err != nil || len(res.Skipped) != 1 || res.Skipped[0].By != "mylocal" {
		t.Fatalf("foreign installed meets the dependency: %+v, %v", res, err)
	}
	_, err = Resolve(ctx, []string{"needs-new"}, db, aur)
	wantRefusal(t, err, ReasonVersion, "libold>=2")
	// the same dependency twice (also reached from another package) is fine
	aur = aurClient(t, nil,
		aurPkg{Name: "a", Version: "1-1", Depends: []string{"c"}},
		aurPkg{Name: "b", Version: "1-1", Depends: []string{"c"}},
		aurPkg{Name: "c", Version: "1-1"})
	res, err = Resolve(ctx, []string{"a", "b"}, db, aur)
	if err != nil || len(res.Bases) != 3 || res.Bases[0].Name != "c" {
		t.Fatalf("shared dependency: %+v, %v", res.Bases, err)
	}
	// AUR down: Info errors
	if _, err := Resolve(ctx, []string{"a"}, db, aurClientClosed(t)); err == nil {
		t.Fatal("no error")
	}
}

func TestPrepareAndCollectFilesystemFailures(t *testing.T) {
	t.Run("out cannot be emptied", func(t *testing.T) {
		skipIfRoot(t)
		_, f := newShims(t)
		out := filepath.Join(f.Root, "app", "out")
		os.MkdirAll(filepath.Join(out, "sub"), 0o700)
		os.WriteFile(filepath.Join(out, "sub", "x"), []byte("x"), 0o600)
		os.Chmod(filepath.Join(out, "sub"), 0o500) // a file in it cannot be removed
		defer os.Chmod(filepath.Join(out, "sub"), 0o700)
		err := f.prepare(context.Background(), planFor("app", strings.Repeat("a", 40), "app"))
		if err == nil {
			t.Fatal("prepare succeeded although out could not be emptied")
		}
	})
	t.Run("a build folder cannot be made", func(t *testing.T) {
		skipIfRoot(t)
		_, f := newShims(t)
		dir := filepath.Join(f.Root, "app")
		os.MkdirAll(dir, 0o700)
		os.Chmod(dir, 0o500) // no new folders in it
		defer os.Chmod(dir, 0o700)
		if err := f.prepare(context.Background(), planFor("app", strings.Repeat("a", 40), "app")); err == nil {
			t.Fatal("prepare succeeded in a read-only folder")
		}
	})
	t.Run("a package file cannot be opened", func(t *testing.T) {
		skipIfRoot(t)
		_, f := newShims(t)
		out := filepath.Join(f.Root, "app", "out")
		os.MkdirAll(out, 0o700)
		p := filepath.Join(out, built("app"))
		os.WriteFile(p, []byte("x"), 0o600)
		os.Chmod(p, 0)
		if _, err := f.Collect([]steps.AURPlan{planFor("app", "", "app")}); err == nil {
			t.Fatal("an unreadable package was collected")
		}
	})
	t.Run("a package over 2 GiB", func(t *testing.T) {
		_, f := newShims(t)
		out := filepath.Join(f.Root, "app", "out")
		os.MkdirAll(out, 0o700)
		fd, err := os.Create(filepath.Join(out, built("app")))
		if err != nil {
			t.Fatal(err)
		}
		fd.Truncate(MaxPackageSize + 1) // sparse
		fd.Close()
		if _, err := f.Collect([]steps.AURPlan{planFor("app", "", "app")}); err == nil || !strings.Contains(err.Error(), "2 GiB") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestFetchFilesystemFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("root under a file", func(t *testing.T) {
		_, f := newShims(t)
		blocker := filepath.Join(t.TempDir(), "file")
		os.WriteFile(blocker, []byte("x"), 0o600)
		f.Root = filepath.Join(blocker, "aur")
		if _, err := f.Fetch(ctx, "app"); err == nil {
			t.Fatal("fetched under a file")
		}
	})
	t.Run("a folder that cannot be read", func(t *testing.T) {
		skipIfRoot(t)
		_, f := newShims(t)
		os.MkdirAll(f.Root, 0o700)
		os.Chmod(f.Root, 0)
		defer os.Chmod(f.Root, 0o700)
		if err := f.checkPath(filepath.Join(f.Root, "app", "x")); err == nil {
			t.Fatal("a path under an unreadable folder passed")
		}
	})
	t.Run("pin in a symlinked folder", func(t *testing.T) {
		_, f := newShims(t)
		os.MkdirAll(f.Root, 0o700)
		dir := filepath.Join(f.Root, "app")
		os.Symlink(t.TempDir(), dir)
		if err := f.Pin(ctx, Checkout{Base: "app", Dir: dir, Commit: strings.Repeat("a", 40)}); err == nil {
			t.Fatal("pinned through a symlink")
		}
	})
	t.Run("commit ids", func(t *testing.T) {
		for c, ok := range map[string]bool{strings.Repeat("a", 40): true, strings.Repeat("f", 64): true, strings.Repeat("A", 40): false, strings.Repeat("a", 39): false, strings.Repeat("g", 40): false, "": false} {
			if validCommit(c) != ok {
				t.Errorf("validCommit(%q) = %v", c, !ok)
			}
		}
	})
	t.Run("fetch into a repo where git fails", func(t *testing.T) {
		s, f := newShims(t)
		if _, err := f.Fetch(ctx, "app"); err != nil {
			t.Fatal(err)
		}
		s.put("fetch.rc", "128")
		s.put("fetch.err", "fatal: could not read from remote\n")
		if _, err := f.Fetch(ctx, "app"); err == nil || !strings.Contains(err.Error(), "git fetch") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestReviewFileEdges(t *testing.T) {
	skipIfRoot(t)
	dir := t.TempDir()
	if _, err := readSmallFile(filepath.Join(dir, "nope"), 10); err == nil {
		t.Fatal("missing file read")
	}
	os.WriteFile(filepath.Join(dir, "big"), []byte("0123456789ab"), 0o600)
	if _, err := readSmallFile(filepath.Join(dir, "big"), 10); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("over the cap: %v", err)
	}
	if _, err := readSmallFile(dir, 10); err == nil {
		t.Fatal("a folder was read as a file")
	}
	if b, err := readSmallFile(filepath.Join(dir, "big"), 20); err != nil || string(b) != "0123456789ab" {
		t.Fatalf("read = %q, %v", b, err)
	}
	// a file that cannot be opened, a FIFO (not regular) and a folder inside
	os.WriteFile(filepath.Join(dir, "locked"), []byte("secret"), 0o600)
	os.Chmod(filepath.Join(dir, "locked"), 0)
	files, _, err := readFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]steps.AURFile{}
	for _, af := range files {
		by[af.Path] = af
	}
	if by["locked"].Note != "unreadable" || by["locked"].Text != "" {
		t.Fatalf("locked = %+v", by["locked"])
	}
	// a socket-like special file: not a regular file
	os.Chmod(filepath.Join(dir, "locked"), 0o600)
	if err := mkfifo(filepath.Join(dir, "pipe")); err != nil {
		t.Fatal(err)
	}
	files, _, _ = readFiles(dir)
	for _, af := range files {
		if af.Path == "pipe" && af.Note != "not a regular file" {
			t.Fatalf("fifo = %+v", af)
		}
	}
	// the walk fails on an unreadable folder
	os.Mkdir(filepath.Join(dir, "d"), 0o700)
	os.Chmod(filepath.Join(dir, "d"), 0)
	defer os.Chmod(filepath.Join(dir, "d"), 0o700)
	if _, _, err := readFiles(dir); err == nil {
		t.Fatal("an unreadable folder did not fail the review")
	}
}
