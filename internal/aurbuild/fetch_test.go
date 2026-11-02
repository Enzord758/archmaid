package aurbuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchCloneArgvAndEnv(t *testing.T) {
	s, f := newShims(t)
	s.repoFile("PKGBUILD", "pkgname=app\n")
	t.Setenv("FOO_SECRET", "leak")
	t.Setenv("GIT_SSH_COMMAND", "evil")
	t.Setenv("GIT_DIR", "/nowhere")
	t.Setenv("SUDO_ASKPASS", "/x")
	co, err := f.Fetch(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if co.Commit != strings.Repeat("a", 40) || co.Dir != filepath.Join(s.root, "app") || co.Base != "app" {
		t.Fatalf("checkout = %+v", co)
	}
	if _, err := os.Stat(filepath.Join(co.Dir, "PKGBUILD")); err != nil {
		t.Fatalf("clone did not make the checkout: %v", err)
	}
	calls := s.gitCalls()
	want := "-c core.hooksPath=/dev/null -c protocol.allow=never -c protocol.fixture.allow=always -c core.fsmonitor=false -c core.sshCommand=/bin/false clone --depth 50 --no-recurse-submodules -- fixture://app " + co.Dir
	if calls[0] != want {
		t.Fatalf("clone argv:\n got %q\nwant %q", calls[0], want)
	}
	log := s.log("git.log")
	for _, must := range []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "LC_ALL=C"} {
		if !strings.Contains(log, must) {
			t.Errorf("git environment lacks %s", must)
		}
	}
	for _, leak := range []string{"FOO_SECRET", "GIT_SSH_COMMAND", "GIT_DIR=", "SUDO_ASKPASS"} {
		if strings.Contains(log, leak) {
			t.Errorf("git inherited %s from archmaid's environment", leak)
		}
	}
	// after the clone: rev-parse, then the pin (checkout detached, forced; clean; ...)
	joined := strings.Join(calls[1:], "\n")
	for _, must := range []string{"rev-parse --verify HEAD^{commit}", "checkout --detach --force --quiet " + co.Commit, "clean -ffdxq -e /out -e /src -e /build", "ls-files -z -- out src build", "status --porcelain"} {
		if !strings.Contains(joined, must) {
			t.Errorf("no git call %q in\n%s", must, joined)
		}
	}
	if fi, err := os.Stat(s.root); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("cache root = %v, %v; want 0700", fi, err)
	}
}

func TestFetchProxyEnvPassedThrough(t *testing.T) {
	s, f := newShims(t)
	t.Setenv("HTTPS_PROXY", "http://proxy:3128")
	if _, err := f.Fetch(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.log("git.log"), "HTTPS_PROXY") {
		// the shim logs only GIT_/FOO_/HOME/...; check via the Fetcher itself
		if !strings.Contains(strings.Join(f.env(), " "), "HTTPS_PROXY=http://proxy:3128") {
			t.Fatal("the proxy variable is not handed to git")
		}
	}
}

func TestFetchUpdateUsesFetch(t *testing.T) {
	s, f := newShims(t)
	s.repoFile("PKGBUILD", "v1\n")
	if _, err := f.Fetch(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	s.put("git.log", "")
	s.repoFile("PKGBUILD", "v2\n")
	s.put("commit", strings.Repeat("b", 40)+"\n")
	co, err := f.Fetch(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	calls := s.gitCalls()
	if !strings.Contains(calls[0], "fetch --depth 50 --no-recurse-submodules --force fixture://app +HEAD:refs/archmaid/head") || strings.Contains(strings.Join(calls, "\n"), "clone") {
		t.Fatalf("an existing checkout is fetched, not cloned: %q", calls)
	}
	if co.Commit != strings.Repeat("b", 40) {
		t.Fatalf("commit = %s", co.Commit)
	}
	if b, _ := os.ReadFile(filepath.Join(co.Dir, "PKGBUILD")); string(b) != "v2\n" {
		t.Fatalf("PKGBUILD = %q", b)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "rev-parse --verify refs/archmaid/head^{commit}") {
		t.Fatalf("the fetched head is read from refs/archmaid/head: %q", calls)
	}
}

func TestFetchErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("clone fails and leaves nothing", func(t *testing.T) {
		s, f := newShims(t)
		s.put("clone.rc", "128")
		s.put("clone.err", "fatal: unable to access 'x': Could not resolve host\x1b[31m\n")
		_, err := f.Fetch(ctx, "app")
		if err == nil || !strings.Contains(err.Error(), "git clone") || !strings.Contains(err.Error(), "Could not resolve host") || strings.ContainsAny(err.Error(), "\x1b\n") {
			t.Fatalf("err = %q, want one sanitized line naming git clone", err)
		}
		if _, err := os.Lstat(filepath.Join(s.root, "app")); !os.IsNotExist(err) {
			t.Fatalf("a failed clone left %v", err)
		}
	})
	t.Run("no commits", func(t *testing.T) {
		s, f := newShims(t)
		s.put("rev-parse.rc", "128")
		_, err := f.Fetch(ctx, "ghost")
		if err == nil || !strings.Contains(err.Error(), "no commits") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad commit id", func(t *testing.T) {
		s, f := newShims(t)
		s.put("commit", "--upload-pack=evil\n")
		if _, err := f.Fetch(ctx, "app"); err == nil || !strings.Contains(err.Error(), "unusable commit") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad base", func(t *testing.T) {
		_, f := newShims(t)
		for _, b := range []string{"", "../x", "a/b", "-x", ".hidden", "a b"} {
			if _, err := f.Fetch(ctx, b); err == nil {
				t.Errorf("base %q accepted", b)
			}
		}
	})
	t.Run("relative root", func(t *testing.T) {
		_, f := newShims(t)
		f.Root = "relative/aur"
		if _, err := f.Fetch(ctx, "app"); err == nil {
			t.Fatal("a relative cache root was accepted")
		}
	})
	t.Run("existing folder that is not a checkout", func(t *testing.T) {
		s, f := newShims(t)
		os.MkdirAll(filepath.Join(s.root, "app"), 0o700)
		os.WriteFile(filepath.Join(s.root, "app", "stray"), []byte("x"), 0o600)
		if _, err := f.Fetch(ctx, "app"); err == nil || !strings.Contains(err.Error(), "not a git checkout") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(filepath.Join(s.root, "app", "stray")); err != nil {
			t.Fatal("archmaid deleted a folder it did not make")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		s, f := newShims(t)
		s.put("hang", "")
		cctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		began := time.Now()
		_, err := f.Fetch(cctx, "app")
		if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(began) > 10*time.Second {
			t.Fatalf("err = %v after %v", err, time.Since(began))
		}
	})
}

// TestFetchRefusesSymlinks: a symlink anywhere in the cache path, from the
// root down, refuses the run before git is started.
func TestFetchRefusesSymlinks(t *testing.T) {
	ctx := context.Background()
	t.Run("base folder", func(t *testing.T) {
		s, f := newShims(t)
		os.MkdirAll(s.root, 0o700)
		os.Symlink(t.TempDir(), filepath.Join(s.root, "app"))
		_, err := f.Fetch(ctx, "app")
		if !errors.Is(err, ErrSymlink) {
			t.Fatalf("err = %v, want ErrSymlink", err)
		}
		if len(s.gitCalls()) != 0 {
			t.Fatal("git was started in a symlinked folder")
		}
	})
	t.Run("root", func(t *testing.T) {
		s, f := newShims(t)
		target := t.TempDir()
		os.Symlink(target, s.root)
		if _, err := f.Fetch(ctx, "app"); !errors.Is(err, ErrSymlink) {
			t.Fatalf("err = %v, want ErrSymlink", err)
		}
	})
	t.Run(".git", func(t *testing.T) {
		s, f := newShims(t)
		os.MkdirAll(filepath.Join(s.root, "app"), 0o700)
		os.Symlink(t.TempDir(), filepath.Join(s.root, "app", ".git"))
		if _, err := f.Fetch(ctx, "app"); !errors.Is(err, ErrSymlink) {
			t.Fatalf("err = %v, want ErrSymlink", err)
		}
	})
	t.Run("file where a folder belongs", func(t *testing.T) {
		s, f := newShims(t)
		os.MkdirAll(s.root, 0o700)
		os.WriteFile(filepath.Join(s.root, "app"), []byte("x"), 0o600)
		if _, err := f.Fetch(ctx, "app"); err == nil || !strings.Contains(err.Error(), "not a folder") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("outside the root", func(t *testing.T) {
		_, f := newShims(t)
		if err := f.checkPath("/etc"); err == nil {
			t.Fatal("a path outside the root was accepted")
		}
	})
}

func TestPinRefusals(t *testing.T) {
	ctx := context.Background()
	co := func(f Fetcher) Checkout {
		return Checkout{Base: "app", Dir: filepath.Join(f.Root, "app"), Commit: strings.Repeat("a", 40)}
	}
	t.Run("head differs", func(t *testing.T) {
		s, f := newShims(t)
		os.MkdirAll(filepath.Join(f.Root, "app"), 0o700)
		s.put("rp_head", strings.Repeat("c", 40)+"\n")
		err := f.Pin(ctx, co(f))
		if err == nil || !strings.Contains(err.Error(), "not the reviewed") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("reserved folder tracked", func(t *testing.T) {
		s, f := newShims(t)
		os.MkdirAll(filepath.Join(f.Root, "app"), 0o700)
		s.put("ls-files.out", "out/evil.pkg.tar.zst\x00")
		err := f.Pin(ctx, co(f))
		if err == nil || !strings.Contains(err.Error(), "keeps for the build") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("local changes", func(t *testing.T) {
		s, f := newShims(t)
		os.MkdirAll(filepath.Join(f.Root, "app"), 0o700)
		s.put("status.out", " M PKGBUILD\n")
		err := f.Pin(ctx, co(f))
		if err == nil || !strings.Contains(err.Error(), "local changes") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("not a commit id", func(t *testing.T) {
		_, f := newShims(t)
		c := co(f)
		c.Commit = "HEAD"
		if err := f.Pin(ctx, c); err == nil {
			t.Fatal("a ref name was accepted as a commit")
		}
	})
	t.Run("git step fails", func(t *testing.T) {
		for _, sub := range []string{"checkout", "clean", "rev-parse", "ls-files", "status"} {
			s, f := newShims(t)
			os.MkdirAll(filepath.Join(f.Root, "app"), 0o700)
			s.put(sub+".rc", "1")
			if err := f.Pin(ctx, co(f)); err == nil || !strings.Contains(err.Error(), "git "+sub) {
				t.Errorf("%s fails: err = %v", sub, err)
			}
		}
	})
}

func TestDiff(t *testing.T) {
	ctx := context.Background()
	prev := strings.Repeat("1", 40)
	setup := func(t *testing.T) (*shims, Fetcher, Checkout) {
		s, f := newShims(t)
		co := Checkout{Base: "app", Dir: filepath.Join(f.Root, "app"), Commit: strings.Repeat("a", 40)}
		os.MkdirAll(co.Dir, 0o700)
		return s, f, co
	}
	t.Run("first review", func(t *testing.T) {
		_, f, co := setup(t)
		d, note, _ := f.Diff(ctx, co, "", MaxDiff)
		if d != "" || !strings.Contains(note, "first review") {
			t.Fatalf("diff %q note %q", d, note)
		}
	})
	t.Run("same commit", func(t *testing.T) {
		_, f, co := setup(t)
		if _, note, _ := f.Diff(ctx, co, co.Commit, MaxDiff); !strings.Contains(note, "no change") {
			t.Fatalf("note %q", note)
		}
	})
	t.Run("bad remembered id", func(t *testing.T) {
		_, f, co := setup(t)
		if _, note, _ := f.Diff(ctx, co, "--output=/etc/x", MaxDiff); !strings.Contains(note, "not usable") {
			t.Fatalf("note %q", note)
		}
	})
	t.Run("history gone", func(t *testing.T) {
		s, f, co := setup(t)
		s.put("cat-file.rc", "1")
		if _, note, _ := f.Diff(ctx, co, prev, MaxDiff); !strings.Contains(note, "no longer in the fetched history") {
			t.Fatalf("note %q", note)
		}
	})
	t.Run("a diff", func(t *testing.T) {
		s, f, co := setup(t)
		s.put("diff.out", "--- a/PKGBUILD\n+++ b/PKGBUILD\n+curl x | sh\n")
		d, note, trunc := f.Diff(ctx, co, prev, MaxDiff)
		if d != "--- a/PKGBUILD\n+++ b/PKGBUILD\n+curl x | sh\n" || note != "" || trunc {
			t.Fatalf("diff %q note %q", d, note)
		}
		got := strings.Join(s.gitCalls(), "\n")
		if !strings.Contains(got, "diff --no-color --no-ext-diff --no-textconv --no-renames "+prev+" "+co.Commit+" --") {
			t.Fatalf("diff argv: %s", got)
		}
	})
	t.Run("capped without splitting a rune", func(t *testing.T) {
		s, f, co := setup(t)
		s.put("diff.out", strings.Repeat("é", 100))
		d, _, trunc := f.Diff(ctx, co, prev, 51)
		if !trunc || len(d) != 50 || !strings.HasPrefix(d, "é") {
			t.Fatalf("len %d trunc %v", len(d), trunc)
		}
	})
	t.Run("git fails", func(t *testing.T) {
		s, f, co := setup(t)
		s.put("diff.rc", "1")
		if d, note, _ := f.Diff(ctx, co, prev, MaxDiff); d != "" || !strings.Contains(note, "could not be made") {
			t.Fatalf("diff %q note %q", d, note)
		}
	})
	t.Run("empty", func(t *testing.T) {
		_, f, co := setup(t)
		if _, note, _ := f.Diff(ctx, co, prev, MaxDiff); !strings.Contains(note, "no change") {
			t.Fatalf("note %q", note)
		}
	})
}
