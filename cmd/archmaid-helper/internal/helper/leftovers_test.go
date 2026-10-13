package helper

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Full Cleanup's clean-uninstalled part (pacman -Sc) printed one red
// "error: could not open file <cache>/download-XXXXXX" per empty folder
// an interrupted pacman 7 download had left in the cache (its DownloadUser
// downloads into mkdtemp's download-XXXXXX). The part now removes those
// folders first, as root, only when that is provably safe: a direct child
// of a cache dir pacman-conf names, only this uid can write to, named
// download- plus six mkdtemp characters, a real directory (fstatat with
// AT_SYMLINK_NOFOLLOW), unchanged for an hour, removed with rmdir
// semantics only (unlinkat AT_REMOVEDIR: empty or nothing), and never
// while pacman's db.lck exists. Each removal is one line.

// cacheDirQuery is how the helper asks pacman which cache dirs it uses.
var cacheDirQuery = cacheDirArgv()

func TestCacheDirArgv(t *testing.T) {
	if got := strings.Join(cacheDirArgv(), " "); got != "/usr/bin/pacman-conf CacheDir" {
		t.Errorf("cacheDirArgv = %q", got)
	}
}

// fakeCache is a package cache in a temp dir, with a fakeRunner whose
// pacman-conf names it (with pacman-conf's trailing slash).
func fakeCache(t *testing.T) (string, *fakeRunner) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pkg")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := newFake().on(cacheDirQuery, reply{out: dir + "/\n"})
	return dir, f
}

// leftover makes an empty folder name in dir, last changed two hours ago,
// as an interrupted download leaves it.
func leftover(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	age(t, p, 2*time.Hour)
	return p
}

// age sets p's times to d ago (a negative d: in the future).
func age(t *testing.T, p string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// runCleanUninstalled runs full-cleanup with its pacman -Sc part and
// returns the texts of its line events (the fake commands print none),
// failing the test on a non-zero exit.
func runCleanUninstalled(t *testing.T, o Options, out *bytes.Buffer) []string {
	t.Helper()
	if code := Run([]string{"full-cleanup", "--keep", "3", "--uninstalled", "--journal", "2weeks"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var lines []string
	for _, e := range ofType(events(t, out), EventLine) {
		lines = append(lines, e["text"].(string))
	}
	return lines
}

// checkNote: there is exactly one line and it holds note; with note ""
// there is no line at all.
func checkNote(t *testing.T, lines []string, note string) {
	t.Helper()
	if note == "" {
		if len(lines) != 0 {
			t.Errorf("lines for what is no candidate:\n%s", strings.Join(lines, "\n"))
		}
		return
	}
	if len(lines) != 1 || !strings.Contains(lines[0], note) {
		t.Errorf("want one line with %q, got:\n%s", note, strings.Join(lines, "\n"))
	}
}

func TestFullCleanupRemovesDownloadLeftovers(t *testing.T) {
	dir, f := fakeCache(t)
	a := leftover(t, dir, "download-AbC123")
	b := leftover(t, dir, "download-zz9Q0x")
	pkg := filepath.Join(dir, "foo-1-1-x86_64.pkg.tar.zst")
	writeFile(t, pkg)
	var atSc []bool // were they gone when pacman -Sc started?
	f.onStream = func(argv []string) {
		if slices.Contains(argv, "-Sc") {
			atSc = append(atSc, !exists(a) && !exists(b))
		}
	}
	o, out := rootOpts(t, f)
	lines := runCleanUninstalled(t, o, out)

	if exists(a) || exists(b) {
		t.Fatalf("the empty download leftovers are still there:\n%s", out)
	}
	if !exists(pkg) {
		t.Fatal("a cached package was removed")
	}
	if !slices.Equal(atSc, []bool{true}) {
		t.Fatalf("pacman -Sc ran %d time(s), leftovers gone before it: %v", len(atSc), atSc)
	}
	want := []string{
		"Removed " + a + ": an empty folder an interrupted pacman download left behind",
		"Removed " + b + ": an empty folder an interrupted pacman download left behind",
	}
	slices.Sort(lines)
	if !slices.Equal(lines, want) {
		t.Errorf("lines\n got %q\nwant %q", lines, want)
	}
	// The lines belong to the clean-uninstalled part: after its progress,
	// before its done.
	evs := events(t, out)
	start, end, first := -1, -1, -1
	for i, e := range evs {
		switch {
		case e["t"] == EventProgress && e["part"] == PartCleanUninstalled && start < 0:
			start = i
		case e["t"] == EventDone && e["part"] == PartCleanUninstalled:
			end = i
		case e["t"] == EventLine && first < 0:
			first = i
		}
	}
	if !(start >= 0 && start < first && first < end) {
		t.Errorf("first line at %d, clean-uninstalled part from %d to %d:\n%s", first, start, end, out)
	}
	if len(ofType(evs, EventProgress)) != 5 {
		t.Errorf("want one progress event per part (5):\n%s", out)
	}
	if q := join(f.queries); !slices.Contains(q, strings.Join(cacheDirQuery, " ")) {
		t.Errorf("the cache dirs did not come from pacman-conf: queries %q", q)
	}
}

// TestDownloadLeftoversLeftAlone: everything that is not provably an
// abandoned, empty download folder of pacman's stays, and pacman -Sc still
// runs (its own red line then shows what was left). Each case's note says
// which check left it alone.
func TestDownloadLeftoversLeftAlone(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string) []string // returns paths that must survive
		lock  bool                                    // pacman's db.lck appears once the run is under way
		note  string                                  // the one line saying why; "" no line at all
	}{
		{"symlink to an empty dir", func(t *testing.T, dir string) []string {
			target := leftover(t, t.TempDir(), "download-Tgt123")
			link := filepath.Join(dir, "download-Sym123")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			return []string{link, target}
		}, false, "download-Sym123 alone: a symlink, not a folder"},
		{"dangling symlink", func(t *testing.T, dir string) []string {
			link := filepath.Join(dir, "download-Dng123")
			if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
				t.Fatal(err)
			}
			return []string{link}
		}, false, "download-Dng123 alone: a symlink, not a folder"},
		{"non-empty dir", func(t *testing.T, dir string) []string {
			p := leftover(t, dir, "download-Ful123")
			part := filepath.Join(p, "foo-1-1-x86_64.pkg.tar.zst.part")
			writeFile(t, part)
			age(t, p, 2*time.Hour)
			return []string{p, part}
		}, false, "download-Ful123 alone: not empty"},
		{"file", func(t *testing.T, dir string) []string {
			p := filepath.Join(dir, "download-Fil123")
			writeFile(t, p)
			age(t, p, 2*time.Hour)
			return []string{p}
		}, false, "download-Fil123 alone: not a folder"},
		{"wrong names", func(t *testing.T, dir string) []string {
			var keep []string
			for _, n := range []string{"download-abc12", "download-abc1234", "download-ab_123", "download-ab-123",
				"Download-abc123", "xdownload-abc123", "download-abc123.old", "download-", "download-ab.123",
				"download-äbc123", "download-abc12\u200b", "download-abc12\n"} {
				keep = append(keep, leftover(t, dir, n))
			}
			return keep
		}, false, ""},
		{"changed within the hour", func(t *testing.T, dir string) []string {
			p := leftover(t, dir, "download-New123")
			age(t, p, 50*time.Minute)
			return []string{p}
		}, false, "download-New123 alone: changed within the last hour"},
		{"changed in the future", func(t *testing.T, dir string) []string {
			p := leftover(t, dir, "download-Fut123")
			age(t, p, -2*time.Hour)
			return []string{p}
		}, false, "download-Fut123 alone: changed within the last hour"},
		{"pacman's lock present", func(t *testing.T, dir string) []string {
			return []string{leftover(t, dir, "download-Lck123")}
		}, true, "Left pacman's download folders alone: busy: pacman is running"},
		{"nested one level down", func(t *testing.T, dir string) []string {
			sub := filepath.Join(dir, "sub")
			if err := os.Mkdir(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			return []string{leftover(t, sub, "download-Nst123")}
		}, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, f := fakeCache(t)
			keep := c.setup(t, dir)
			o, out := rootOpts(t, f)
			ran := false
			f.onStream = func(argv []string) {
				if c.lock && argv[0] == Paccache {
					writeFile(t, o.PacmanDBLock) // pacman started after the helper's own lock check
				}
				ran = ran || slices.Contains(argv, "-Sc")
			}
			lines := runCleanUninstalled(t, o, out)
			for _, p := range keep {
				if !exists(p) {
					t.Errorf("%q was removed:\n%s", p, out)
				}
			}
			checkNote(t, lines, c.note)
			if !ran {
				t.Error("pacman -Sc did not run")
			}
		})
	}
}

// TestDownloadLeftoversCacheDirChecked: the cache dir itself must be a
// real directory only this uid (root, in the helper) can write to, named
// by pacman-conf as an absolute path; otherwise nothing in it is touched.
func TestDownloadLeftoversCacheDirChecked(t *testing.T) {
	sameDir := func(t *testing.T, dir string) reply { return reply{out: dir + "/\n"} }
	chmod := func(mode os.FileMode) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		name  string
		conf  func(t *testing.T, dir string) reply
		setup func(t *testing.T, dir string)
		note  string
	}{
		{"group-writable", sameDir, chmod(0o775), "pkg alone: group- or other-writable"},
		{"other-writable", sameDir, chmod(0o757), "pkg alone: group- or other-writable"},
		{"a symlink", func(t *testing.T, dir string) reply {
			link := filepath.Join(t.TempDir(), "cache")
			if err := os.Symlink(dir, link); err != nil {
				t.Fatal(err)
			}
			return reply{out: link + "\n"}
		}, nil, "cache alone: not a directory (or a symlink)"},
		{"relative", func(t *testing.T, dir string) reply {
			wd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(wd, dir)
			if err != nil {
				t.Fatal(err)
			}
			return reply{out: rel + "\n"}
		}, nil, "not an absolute path"},
		{"pacman-conf fails", func(t *testing.T, dir string) reply { return reply{out: dir + "\n", exit: 1} }, nil,
			"pacman-conf CacheDir exited 1"},
		{"pacman-conf can't run", func(t *testing.T, dir string) reply { return reply{fail: errors.New("no such file")} }, nil,
			"pacman-conf CacheDir: no such file"},
		{"pacman-conf names nothing", func(t *testing.T, dir string) reply { return reply{} }, nil,
			"pacman-conf CacheDir named no cache folder"},
		{"a control character", func(t *testing.T, dir string) reply { return reply{out: dir + "\x1b\n"} }, nil,
			"not an absolute path"},
		{"rmdir refused", sameDir, func(t *testing.T, dir string) {
			if os.Geteuid() == 0 {
				t.Skip("root may rmdir in a read-only dir")
			}
			chmod(0o555)(t, dir)
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // before TempDir's removal
		}, "download-Cfg123 alone: permission denied"},
		{"owned by another uid", func(t *testing.T, dir string) reply {
			if os.Geteuid() == 0 {
				t.Skip("needs a dir another uid owns; / is ours as root")
			}
			// Only opened read-only and fstat'ed: the owner check comes
			// before anything in it is read.
			return reply{out: "/\n"}
		}, nil, "Left / alone: owned by uid 0, not " + strconv.Itoa(os.Geteuid())},
		{"too many", func(t *testing.T, dir string) reply {
			out := dir + "\n"
			for i := range maxCacheDirs {
				out += filepath.Join(dir, "x", strings.Repeat("y", i+1)) + "\n"
			}
			return reply{out: out}
		}, nil, "more than 16"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, f := fakeCache(t)
			p := leftover(t, dir, "download-Cfg123")
			f.on(cacheDirQuery, c.conf(t, dir))
			if c.setup != nil {
				c.setup(t, dir)
			}
			o, out := rootOpts(t, f)
			ran := false
			f.onStream = func(argv []string) { ran = ran || slices.Contains(argv, "-Sc") }
			checkNote(t, runCleanUninstalled(t, o, out), c.note)
			if !exists(p) {
				t.Errorf("%s was removed:\n%s", p, out)
			}
			if !ran {
				t.Error("pacman -Sc did not run")
			}
		})
	}
}

// TestDownloadLeftoversEveryCacheDir: pacman -Sc cleans every CacheDir, so
// every one pacman-conf names is tidied, each on its own checks; one that
// can't be opened is said and the others still are.
func TestDownloadLeftoversEveryCacheDir(t *testing.T) {
	one, f := fakeCache(t)
	two := filepath.Join(t.TempDir(), "pkg2")
	if err := os.Mkdir(two, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	gone := []string{leftover(t, one, "download-One123"), leftover(t, two, "download-Two123")}
	f.on(cacheDirQuery, reply{out: one + "/\n" + missing + "/\n" + two + "/\n" + one + "\n"})
	o, out := rootOpts(t, f)
	lines := runCleanUninstalled(t, o, out)
	for _, p := range gone {
		if exists(p) {
			t.Errorf("%s is still there:\n%s", p, out)
		}
	}
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "Left "+missing+" alone: ") {
		t.Errorf("want a removal, the missing dir, a removal (each dir once):\n%s", strings.Join(lines, "\n"))
	}
}

// TestNoTidyWithoutUninstalled: the tidy belongs to pacman -Sc's part; a
// full-cleanup without it doesn't ask pacman-conf or touch the cache.
func TestNoTidyWithoutUninstalled(t *testing.T) {
	dir, f := fakeCache(t)
	p := leftover(t, dir, "download-Off123")
	o, out := rootOpts(t, f)
	if code := Run([]string{"full-cleanup", "--keep", "3", "--journal", "2weeks"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !exists(p) {
		t.Error("the leftover was removed without --uninstalled")
	}
	if q := join(f.queries); slices.Contains(q, strings.Join(cacheDirQuery, " ")) {
		t.Errorf("pacman-conf was asked without --uninstalled: %q", q)
	}
}
