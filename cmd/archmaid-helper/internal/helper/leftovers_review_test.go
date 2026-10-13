package helper

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Tests from the security review of the tidy's pacman lock (review #124
// of #122): each one kills a mutation of leftovers.go that the tests
// before it let through.

// lockWatch records every write to the client and every command the
// helper starts (Query or Stream) while pacman's lock exists.
type lockWatch struct {
	mu   sync.Mutex
	lock string
	seen []string
}

func (w *lockWatch) check(what string) {
	if exists(w.lock) {
		w.mu.Lock()
		w.seen = append(w.seen, what)
		w.mu.Unlock()
	}
}

// lockWatchWriter is the client's pipe: a write while the lock exists can
// block with the lock held.
type lockWatchWriter struct {
	w     io.Writer
	watch *lockWatch
}

func (l lockWatchWriter) Write(p []byte) (int, error) {
	l.watch.check("a write to the client: " + strconv.Quote(string(p)))
	return l.w.Write(p)
}

// lockWatchRunner is a fakeRunner whose Query (pacman-conf) and Stream
// (pacman -Sc and the rest) are checked too: no exec under the lock.
type lockWatchRunner struct {
	*fakeRunner
	watch *lockWatch
}

func (r lockWatchRunner) Query(argv []string) (string, string, int, error) {
	r.watch.check("a command: " + strings.Join(argv, " "))
	return r.fakeRunner.Query(argv)
}

func (r lockWatchRunner) Stream(argv []string, line func(stream, text string)) (int, error) {
	r.watch.check("a command: " + strings.Join(argv, " "))
	return r.fakeRunner.Stream(argv, line)
}

// TestTidyWritesAndRunsNothingUnderPacmanLock (review #124): while the
// helper holds db.lck it writes nothing to the client (a full pipe would
// block it there, every pacman refused meanwhile) and starts no command,
// pacman-conf included. Kills: a line, or an empty write, emitted inside
// the hold; pacman-conf asked again inside it.
func TestTidyWritesAndRunsNothingUnderPacmanLock(t *testing.T) {
	dir, f := fakeCache(t)
	a := leftover(t, dir, "download-WchAa1")
	full := leftover(t, dir, "download-WchBb2")
	writeFile(t, filepath.Join(full, "foo-1-1-x86_64.pkg.tar.zst.part"))
	age(t, full, 2*time.Hour)
	o, out := tidyOpts(t, f)
	watch := &lockWatch{lock: o.PacmanDBLock}
	o.Stdout = lockWatchWriter{w: out, watch: watch}
	o.Runner = lockWatchRunner{fakeRunner: f, watch: watch}
	held := 0
	o.underPacmanLock = func(string) {
		if exists(o.PacmanDBLock) {
			held++
		}
	}
	lines := runCleanUninstalled(t, o, out)
	if held != 2 {
		t.Fatalf("the probe saw the lock %d time(s), want 2: the test proves nothing", held)
	}
	if len(watch.seen) > 0 {
		t.Errorf("while pacman's lock was held:\n%s", strings.Join(watch.seen, "\n"))
	}
	if exists(a) || !exists(full) || len(lines) != 2 {
		t.Errorf("want one removed, one left:\n%s", strings.Join(lines, "\n"))
	}
}

// TestTidyLockFdIsCloseOnExec (review #124): the lock's fd is opened as
// libalpm opens it, O_WRONLY|O_CLOEXEC (O_CREAT and O_EXCL don't stay in
// the file's flags), so no child the helper starts later inherits it.
func TestTidyLockFdIsCloseOnExec(t *testing.T) {
	dir, f := fakeCache(t)
	leftover(t, dir, "download-Clx123")
	o, out := tidyOpts(t, f)
	var flags []int64
	o.underPacmanLock = func(string) {
		ents, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Errorf("read /proc/self/fd: %v", err)
			return
		}
		for _, e := range ents {
			if l, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err != nil || l != o.PacmanDBLock {
				continue
			}
			info, err := os.ReadFile(filepath.Join("/proc/self/fdinfo", e.Name()))
			if err != nil {
				t.Errorf("fdinfo of the lock's fd: %v", err)
				return
			}
			for _, l := range strings.Split(string(info), "\n") {
				if v, ok := strings.CutPrefix(l, "flags:"); ok {
					n, err := strconv.ParseInt(strings.TrimSpace(v), 8, 64)
					if err != nil {
						t.Errorf("fdinfo flags %q: %v", v, err)
					}
					flags = append(flags, n)
				}
			}
		}
	}
	runCleanUninstalled(t, o, out)
	if len(flags) != 1 {
		t.Fatalf("found %d open fd(s) on the lock under it, want 1", len(flags))
	}
	if flags[0]&unix.O_CLOEXEC == 0 || flags[0]&unix.O_ACCMODE != unix.O_WRONLY {
		t.Errorf("the lock's fd flags are %#o, want O_WRONLY with O_CLOEXEC", flags[0])
	}
}

// TestTidyLeavesALockReplacedBySymlink (review #124): the release compares
// the path itself (lstat), never what it points to. A lock renamed away and
// replaced by a symlink to it is not the tidy's file any more: the symlink
// stays, and the line says the lock was replaced.
func TestTidyLeavesALockReplacedBySymlink(t *testing.T) {
	dir, f := fakeCache(t)
	leftover(t, dir, "download-Sym456")
	o, out := tidyOpts(t, f)
	moved := o.PacmanDBLock + ".moved"
	o.underPacmanLock = func(string) {
		if err := os.Rename(o.PacmanDBLock, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, o.PacmanDBLock); err != nil {
			t.Fatal(err)
		}
	}
	lines := runCleanUninstalled(t, o, out)
	if len(lines) == 0 || !strings.Contains(lines[0], "was replaced while the tidy held it; left the new one alone") {
		t.Errorf("want the replaced-lock line first:\n%s", strings.Join(lines, "\n"))
	}
	if fi, err := os.Lstat(o.PacmanDBLock); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink that replaced the lock was removed: %v, %v", fi, err)
	}
}

// TestTidyWithoutTheLockTouchesNothing (review #124): a lock that can't be
// taken for any reason but EEXIST (here its folder is missing) means
// nothing is touched either, never a tidy without the lock.
func TestTidyWithoutTheLockTouchesNothing(t *testing.T) {
	dir, f := fakeCache(t)
	p := leftover(t, dir, "download-Nlk123")
	o, out := tidyOpts(t, f)
	o.PacmanDBLock = filepath.Join(t.TempDir(), "missing", "db.lck")
	o.underPacmanLock = func(name string) { t.Errorf("probed %s without the lock", name) }
	checkNote(t, runCleanUninstalled(t, o, out),
		"Left pacman's download folders alone: busy: cannot take pacman's lock "+o.PacmanDBLock)
	if !exists(p) {
		t.Error("the leftover was removed without pacman's lock")
	}
}

// TestDownloadLeftoverFloorIsTwoMinutes (review #124): the floor is 2
// minutes, measured to within 10 seconds either side (the cases before
// only bound it between 90 s and 5 min).
func TestDownloadLeftoverFloorIsTwoMinutes(t *testing.T) {
	for _, c := range []struct {
		ahead time.Duration
		note  string
	}{
		{110 * time.Second, "alone: changed within the last 2 minutes"},
		{130 * time.Second, "Removed "},
	} {
		t.Run(c.ahead.String(), func(t *testing.T) {
			dir, f := fakeCache(t)
			p := filepath.Join(dir, "download-Flr123")
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatal(err)
			}
			o, out := tidyOpts(t, f)
			clock(&o, c.ahead)
			checkNote(t, runCleanUninstalled(t, o, out), c.note)
		})
	}
}

// TestTidyClockIsTheRealOneByDefault (review #124): with the test seam nil,
// as in production, the tidy's clock is the real one.
func TestTidyClockIsTheRealOneByDefault(t *testing.T) {
	s := &session{o: DefaultOptions()}
	if d := time.Since(s.now()); d < -time.Second || d > time.Second {
		t.Errorf("the default clock is %v off the real one", d)
	}
}

// TestTidyNotEmptyHintNamesTheRealPath (review #124): the hint's command
// is meant to be pasted, so it must name the folder on disk exactly, or
// not be there at all. Every line goes through the sanitizer, which drops
// C1 controls and format characters and replaces invalid UTF-8; for a
// cache dir with one of those in its name the command names another path.
func TestTidyNotEmptyHintNamesTheRealPath(t *testing.T) {
	for _, c := range []struct{ name, dir string }{
		{"bidi override", "pkg\u202e"},
		{"C1 control", "pkg\u009b"},
		{"zero-width space", "pkg\u200b"},
		{"invalid UTF-8", "pkg\xff"},
		{"plain", "pkg"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, f := fakeCache(t)
			dir := filepath.Join(t.TempDir(), c.dir)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			f.on(cacheDirQuery, reply{out: dir + "/\n"})
			p := leftover(t, dir, "download-Hnr123")
			writeFile(t, filepath.Join(p, "foo-1-1-x86_64.pkg.tar.zst.part"))
			age(t, p, 2*time.Hour)
			o, out := tidyOpts(t, f)
			lines := runCleanUninstalled(t, o, out)
			if len(lines) != 1 {
				t.Fatalf("want one line, got %q", lines)
			}
			i := strings.Index(lines[0], "sudo rm")
			if i >= 0 && !strings.HasSuffix(lines[0], " -- "+shellQuote(p)) {
				t.Errorf("the hint's command names another path than %q:\n%q", p, lines[0][i:])
			}
			if plain := c.name == "plain"; plain && i < 0 {
				t.Errorf("a plain path must still get the exact command: %q", lines[0])
			} else if !plain && i >= 0 {
				t.Errorf("a path with unshowable characters must get no command: %q", lines[0])
			}
		})
	}
}
