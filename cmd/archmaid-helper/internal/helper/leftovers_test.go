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
// pacman 7 had left in the cache (its DownloadUser downloads into
// mkdtemp's download-XXXXXX, which libalpm leaves behind after an
// interrupted download and after every transaction that downloaded
// nothing). The part now removes those folders first, as root, only when
// that is provably safe: a direct child of a cache dir pacman-conf names,
// only this uid can write to, named download- plus six mkdtemp
// characters, and, while the helper holds pacman's own db.lck (an
// existing one means nothing is touched), a real directory (fstatat with
// AT_SYMLINK_NOFOLLOW) unchanged for 2 minutes by the later of its mtime
// and ctime, removed with rmdir semantics only (unlinkat AT_REMOVEDIR:
// empty or nothing). Each removal is one line.
//
// A test can't set a ctime back, so every folder a test makes was changed
// "now"; tidyOpts runs the helper's clock an hour ahead instead (clock
// sets another lead).

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

// leftover makes an empty folder name in dir, as pacman leaves it: its
// mtime two hours ago, its ctime now (tidyOpts' clock makes that an hour
// ago).
func leftover(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	age(t, p, 2*time.Hour)
	return p
}

// age sets p's atime and mtime to d ago (a negative d: in the future);
// its ctime becomes now.
func age(t *testing.T, p string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
}

// tidyOpts is rootOpts with the helper's clock an hour ahead of the real
// one, so every folder the test made is an hour old by its ctime.
func tidyOpts(t *testing.T, r Runner) (Options, *bytes.Buffer) {
	t.Helper()
	o, out := rootOpts(t, r)
	clock(&o, time.Hour)
	return o, out
}

// clock runs o's clock d ahead of the real one.
func clock(o *Options, d time.Duration) {
	o.now = func() time.Time { return time.Now().Add(d) }
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
	o, out := tidyOpts(t, f)
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
		"Removed " + a + ": an empty folder a pacman download left behind",
		"Removed " + b + ": an empty folder a pacman download left behind",
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
		ahead time.Duration                           // the helper's clock ahead of the real one; 0: tidyOpts' hour
	}{
		{"symlink to an empty dir", func(t *testing.T, dir string) []string {
			target := leftover(t, t.TempDir(), "download-Tgt123")
			link := filepath.Join(dir, "download-Sym123")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			return []string{link, target}
		}, false, "download-Sym123 alone: a symlink, not a folder", 0},
		{"dangling symlink", func(t *testing.T, dir string) []string {
			link := filepath.Join(dir, "download-Dng123")
			if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
				t.Fatal(err)
			}
			return []string{link}
		}, false, "download-Dng123 alone: a symlink, not a folder", 0},
		{"non-empty dir", func(t *testing.T, dir string) []string {
			p := leftover(t, dir, "download-Ful123")
			part := filepath.Join(p, "foo-1-1-x86_64.pkg.tar.zst.part")
			writeFile(t, part)
			age(t, p, 2*time.Hour)
			return []string{p, part}
		}, false, "download-Ful123 alone: not empty (an interrupted download's partial files)", 0},
		{"file", func(t *testing.T, dir string) []string {
			p := filepath.Join(dir, "download-Fil123")
			writeFile(t, p)
			age(t, p, 2*time.Hour)
			return []string{p}
		}, false, "download-Fil123 alone: not a folder", 0},
		{"wrong names", func(t *testing.T, dir string) []string {
			var keep []string
			for _, n := range []string{"download-abc12", "download-abc1234", "download-ab_123", "download-ab-123",
				"Download-abc123", "xdownload-abc123", "download-abc123.old", "download-", "download-ab.123",
				"download-äbc123", "download-abc12\u200b", "download-abc12\n"} {
				keep = append(keep, leftover(t, dir, n))
			}
			return keep
		}, false, "", 0},
		{"made within the last 2 minutes", func(t *testing.T, dir string) []string {
			p := filepath.Join(dir, "download-New123")
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatal(err)
			}
			return []string{p}
		}, false, "download-New123 alone: changed within the last 2 minutes, so a download may be starting in it", 90 * time.Second},
		{"mtime set back, ctime within 2 minutes", func(t *testing.T, dir string) []string {
			return []string{leftover(t, dir, "download-Ctm123")} // mtime 2 h ago
		}, false, "download-Ctm123 alone: changed within the last 2 minutes", 90 * time.Second},
		{"changed in the future", func(t *testing.T, dir string) []string {
			p := leftover(t, dir, "download-Fut123")
			age(t, p, -2*time.Hour)
			return []string{p}
		}, false, "download-Fut123 alone: changed within the last 2 minutes", 0},
		{"pacman's lock present", func(t *testing.T, dir string) []string {
			return []string{leftover(t, dir, "download-Lck123")}
		}, true, "Left pacman's download folders alone: busy: pacman is running", 0},
		{"nested one level down", func(t *testing.T, dir string) []string {
			sub := filepath.Join(dir, "sub")
			if err := os.Mkdir(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			return []string{leftover(t, sub, "download-Nst123")}
		}, false, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, f := fakeCache(t)
			keep := c.setup(t, dir)
			o, out := tidyOpts(t, f)
			if c.ahead != 0 {
				clock(&o, c.ahead)
			}
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
			o, out := tidyOpts(t, f)
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
	o, out := tidyOpts(t, f)
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
	o, out := tidyOpts(t, f)
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

// TestDownloadLeftoversCacheDirNotADir (review #118): a cache "dir" that
// is a file is refused by the open itself (O_DIRECTORY), before it is
// stat'ed or read. Without O_DIRECTORY root would open whatever the path
// names: a FIFO blocks the helper until a writer comes, a device node's
// open can have side effects (a tape rewinds). Only the file case is
// tested: a FIFO would hang the mutated build instead of failing it.
func TestDownloadLeftoversCacheDirNotADir(t *testing.T) {
	dir, f := fakeCache(t)
	file := filepath.Join(dir, "not-a-dir")
	writeFile(t, file)
	f.on(cacheDirQuery, reply{out: file + "\n"})
	o, out := tidyOpts(t, f)
	checkNote(t, runCleanUninstalled(t, o, out), "Left "+file+" alone: not a directory (or a symlink)")
}

// TestDownloadLeftoversCacheDirsDeduplicated (review #118): pacman-conf
// naming one dir twice (with and without its trailing slash) tidies it,
// and says so, once.
func TestDownloadLeftoversCacheDirsDeduplicated(t *testing.T) {
	_, f := fakeCache(t)
	missing := filepath.Join(t.TempDir(), "missing")
	f.on(cacheDirQuery, reply{out: missing + "/\n" + missing + "\n"})
	o, out := tidyOpts(t, f)
	checkNote(t, runCleanUninstalled(t, o, out), "Left "+missing+" alone: ")
}

// TestDownloadLeftoversLinesSanitized (review #118): the tidy's lines
// carry a path pacman-conf printed, and cacheDirs refuses only C0
// controls and DEL in it. A C1 control or a bidi override in that path
// never reaches the client: every tidy line goes through the Emitter's
// sanitizer like a child's output line.
func TestDownloadLeftoversLinesSanitized(t *testing.T) {
	_, f := fakeCache(t)
	dir := filepath.Join(t.TempDir(), "pkg‮\u009b31m")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := leftover(t, dir, "download-Bid123")
	f.on(cacheDirQuery, reply{out: dir + "/\n"})
	o, out := tidyOpts(t, f)
	lines := runCleanUninstalled(t, o, out)
	if exists(p) {
		t.Errorf("%q is still there:\n%s", p, out)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Removed ") {
		t.Fatalf("want one Removed line, got %q", lines)
	}
	if strings.ContainsAny(lines[0], "‮\u009b") || !strings.Contains(lines[0], "pkg31m/download-Bid123") {
		t.Errorf("line not sanitized: %q", lines[0])
	}
	if strings.ContainsAny(out.String(), "‮\u009b") {
		t.Errorf("raw output carries the control or bidi character:\n%q", out)
	}
}

// TestDownloadLeftoverFromTheSameRunRemoved (task #122, VM round 8b):
// Full Cleanup runs topgrade before the helper, and libalpm leaves an
// empty download-XXXXXX behind for every transaction that had nothing to
// download (yay's pacman -U of a built AUR package, a -Syu whose
// packages were all cached). A leftover a few minutes old, from this very
// run, is removed: with pacman's lock held no pacman can be using it.
func TestDownloadLeftoverFromTheSameRunRemoved(t *testing.T) {
	dir, f := fakeCache(t)
	p := filepath.Join(dir, "download-ltip4y")
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	o, out := tidyOpts(t, f)
	clock(&o, 5*time.Minute) // made 5 minutes ago, by topgrade's pacman
	lines := runCleanUninstalled(t, o, out)
	if exists(p) {
		t.Errorf("the leftover from this run is still there:\n%s", strings.Join(lines, "\n"))
	}
	checkNote(t, lines, "Removed "+p+": ")
}

// lockSeen is what a probe saw of pacman's lock while the tidy ran.
type lockSeen struct {
	exists bool
	mode   os.FileMode
	size   int64
}

func seeLock(path string) lockSeen {
	fi, err := os.Lstat(path)
	if err != nil {
		return lockSeen{}
	}
	return lockSeen{exists: true, mode: fi.Mode(), size: fi.Size()}
}

// TestTidyHoldsPacmanLock (task #122): every removal runs while the helper
// holds pacman's db.lck, made the way libalpm makes it (a new, empty,
// mode-0000 regular file); it is gone again before pacman -Sc starts
// (which takes the same lock itself) and when the run ends.
func TestTidyHoldsPacmanLock(t *testing.T) {
	dir, f := fakeCache(t)
	a := leftover(t, dir, "download-HldAa1")
	b := leftover(t, dir, "download-HldBb2")
	o, out := tidyOpts(t, f)
	var seen []string
	o.underPacmanLock = func(name string) {
		if l := seeLock(o.PacmanDBLock); l != (lockSeen{exists: true, mode: 0}) {
			t.Errorf("under the lock for %s: %+v, want an empty mode-0000 file", name, l)
		}
		seen = append(seen, name)
	}
	var atExec []bool // did the lock exist when each command started?
	f.onStream = func(argv []string) { atExec = append(atExec, exists(o.PacmanDBLock)) }
	lines := runCleanUninstalled(t, o, out)
	slices.Sort(seen)
	if !slices.Equal(seen, []string{"download-HldAa1", "download-HldBb2"}) {
		t.Errorf("probed under the lock: %q", seen)
	}
	if exists(a) || exists(b) || len(lines) != 2 {
		t.Errorf("the leftovers were not both removed:\n%s", strings.Join(lines, "\n"))
	}
	if slices.Contains(atExec, true) {
		t.Errorf("pacman's lock existed when a command started (%v): the hold ran over an exec", atExec)
	}
	if exists(o.PacmanDBLock) {
		t.Error("pacman's lock is still there after the run")
	}
}

// TestTidyTakesNoLockWithoutCandidates: no folder named like a leftover,
// no lock taken, so an existing lock gets no "pacman is running" line.
func TestTidyTakesNoLockWithoutCandidates(t *testing.T) {
	dir, f := fakeCache(t)
	writeFile(t, filepath.Join(dir, "foo-1-1-x86_64.pkg.tar.zst"))
	o, out := tidyOpts(t, f)
	o.underPacmanLock = func(name string) { t.Errorf("probed %s with nothing to remove", name) }
	f.onStream = func(argv []string) {
		if argv[0] == Paccache {
			writeFile(t, o.PacmanDBLock)
		}
	}
	checkNote(t, runCleanUninstalled(t, o, out), "")
}

// TestTidyLeavesAnExistingLock: pacman's lock (a file, or a symlink, even
// a dangling one: O_EXCL never follows it) existing when the tidy starts
// means pacman is running: nothing is touched, and the lock is left exactly
// as it was.
func TestTidyLeavesAnExistingLock(t *testing.T) {
	for _, kind := range []string{"file", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir, f := fakeCache(t)
			p := leftover(t, dir, "download-Exs123")
			o, out := tidyOpts(t, f)
			target := filepath.Join(t.TempDir(), "target")
			o.underPacmanLock = func(name string) { t.Errorf("probed %s under someone else's lock", name) }
			var before os.FileInfo
			f.onStream = func(argv []string) {
				if argv[0] != Paccache {
					return
				}
				// pacman starts after the helper's own check at its start
				if kind == "file" {
					if err := os.WriteFile(o.PacmanDBLock, []byte("pacman's"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(target, o.PacmanDBLock); err != nil {
					t.Fatal(err)
				}
				var err error
				if before, err = os.Lstat(o.PacmanDBLock); err != nil {
					t.Fatal(err)
				}
			}
			checkNote(t, runCleanUninstalled(t, o, out), "Left pacman's download folders alone: busy: pacman is running ("+o.PacmanDBLock+" exists)")
			if !exists(p) {
				t.Error("the leftover was removed under pacman's lock")
			}
			after, err := os.Lstat(o.PacmanDBLock)
			if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() || after.Mode() != before.Mode() {
				t.Errorf("pacman's lock was changed: before %v, after %v, %v", before, after, err)
			}
			if exists(target) {
				t.Error("the lock's symlink was followed: its target exists")
			}
		})
	}
}

// TestTidyRemovesTheLockOnError: a removal that fails (rmdir refused)
// still ends with the lock removed.
func TestTidyRemovesTheLockOnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may rmdir in a read-only dir")
	}
	dir, f := fakeCache(t)
	p := leftover(t, dir, "download-Err123")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	o, out := tidyOpts(t, f)
	held := false
	o.underPacmanLock = func(string) { held = exists(o.PacmanDBLock) }
	checkNote(t, runCleanUninstalled(t, o, out), "download-Err123 alone: permission denied")
	if !held || !exists(p) {
		t.Errorf("held %v, leftover still there %v", held, exists(p))
	}
	if exists(o.PacmanDBLock) {
		t.Error("pacman's lock is still there after a failed removal")
	}
}

// TestTidyRemovesTheLockOnPanic: a panic under the lock unwinds through
// the release, so the lock is gone even then.
func TestTidyRemovesTheLockOnPanic(t *testing.T) {
	dir, f := fakeCache(t)
	p := leftover(t, dir, "download-Pnc123")
	o, _ := tidyOpts(t, f)
	held := false
	o.underPacmanLock = func(string) {
		held = exists(o.PacmanDBLock)
		panic("under the lock")
	}
	func() {
		defer func() {
			if r := recover(); r != "under the lock" {
				t.Errorf("recovered %v, want the probe's panic", r)
			}
		}()
		Run([]string{"full-cleanup", "--keep", "3", "--uninstalled", "--journal", "2weeks"}, o)
	}()
	if !held {
		t.Error("the probe did not run under the lock")
	}
	if exists(o.PacmanDBLock) {
		t.Error("pacman's lock is still there after a panic under it")
	}
	if !exists(p) {
		t.Error("the leftover was removed although the panic came first")
	}
}

// TestTidyLockReleaseChecksItsOwn: the release removes the lock only
// while the path is still the file the tidy made. Removed by someone else
// (root ran rm on it), it says so; replaced (and a new pacman made its own),
// the new lock stays. Either way the line comes before the removals' lines.
func TestTidyLockReleaseChecksItsOwn(t *testing.T) {
	for _, c := range []struct {
		name, note string
		replace    bool
	}{
		{"removed", "which the tidy took, is gone", false},
		{"replaced", "was replaced while the tidy held it; left the new one alone", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, f := fakeCache(t)
			p := leftover(t, dir, "download-Rel123")
			o, out := tidyOpts(t, f)
			o.underPacmanLock = func(string) {
				if err := os.Remove(o.PacmanDBLock); err != nil {
					t.Fatal(err)
				}
				if c.replace {
					writeFile(t, o.PacmanDBLock)
				}
			}
			var atSc []bool
			f.onStream = func(argv []string) {
				if slices.Contains(argv, "-Sc") {
					atSc = append(atSc, exists(o.PacmanDBLock))
				}
			}
			lines := runCleanUninstalled(t, o, out)
			if len(lines) != 2 || !strings.Contains(lines[0], "pacman's lock "+o.PacmanDBLock) ||
				!strings.Contains(lines[0], c.note) || !strings.HasPrefix(lines[1], "Removed "+p+": ") {
				t.Errorf("want the lock's line, then the removal:\n%s", strings.Join(lines, "\n"))
			}
			if exists(o.PacmanDBLock) != c.replace {
				t.Errorf("lock exists after the run: %v, want %v", exists(o.PacmanDBLock), c.replace)
			}
			if !slices.Equal(atSc, []bool{c.replace}) {
				t.Errorf("lock at pacman -Sc: %v", atSc)
			}
		})
	}
}

// TestTidyNotEmptyHint (#121): a leftover with something in it stays, and
// its line says how to delete it by hand, safely: only while no pacman
// runs, by its exact path, quoted for the shell. The command is printed
// only for a plain-ASCII path ([A-Za-z0-9/._+-]); a name with a space or a
// quote gets the by-hand wording and no command.
// TestTidyNotEmptyHintOddName: see TestTidyNotEmptyHint.
func TestTidyNotEmptyHintOddName(t *testing.T) {
	_, f := fakeCache(t)
	dir := filepath.Join(t.TempDir(), "it's pkg")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.on(cacheDirQuery, reply{out: dir + "/\n"})
	p := leftover(t, dir, "download-Hnt123")
	writeFile(t, filepath.Join(p, "foo-1-1-x86_64.pkg.tar.zst.part"))
	o, out := tidyOpts(t, f)
	lines := runCleanUninstalled(t, o, out)
	if len(lines) != 1 || strings.Contains(lines[0], "sudo rm") || !strings.Contains(lines[0], "no command is given") {
		t.Errorf("lines %q", lines)
	}
}

func TestTidyNotEmptyHint(t *testing.T) {
	_, f := fakeCache(t)
	dir := filepath.Join(t.TempDir(), "pkg+1.0_x-y")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.on(cacheDirQuery, reply{out: dir + "/\n"})
	p := leftover(t, dir, "download-Hnt123")
	writeFile(t, filepath.Join(p, "foo-1-1-x86_64.pkg.tar.zst.part"))
	o, out := tidyOpts(t, f)
	want := "Left " + p + " alone: not empty (an interrupted download's partial files); to delete it by hand, " +
		"first make sure no pacman is running (" + o.PacmanDBLock + " doesn't exist), then: sudo rm -r -- '" +
		strings.ReplaceAll(p, "'", `'\''`) + "'"
	lines := runCleanUninstalled(t, o, out)
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("lines\n got %q\nwant %q", lines, want)
	}
	if !exists(p) {
		t.Error("a non-empty leftover was removed")
	}
}
