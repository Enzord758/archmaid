package helper

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task #125: the lock path is pacman-conf DBPath + db.lck, read once per
// run, for every db.lck check and the tidy's hold.

var dbPathQuery = pacmanDBPathArgv()

func TestDBPathArgv(t *testing.T) {
	if got := strings.Join(pacmanDBPathArgv(), " "); got != "/usr/bin/pacman-conf DBPath" {
		t.Errorf("pacmanDBPathArgv = %q", got)
	}
}

// customDB is a DBPath folder in a temp dir, pacman-conf naming it with
// its trailing slash, and options that resolve the lock from it.
func customDB(t *testing.T, f *fakeRunner) (lock string, o Options, run func(Options) []string) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "db")
	if err := os.Mkdir(db, 0o755); err != nil {
		t.Fatal(err)
	}
	f.on(dbPathQuery, reply{out: db + "/\n"})
	o, out := tidyOpts(t, f)
	o.PacmanDBLock = "" // from pacman-conf
	return filepath.Join(db, "db.lck"), o, func(o Options) []string { return runCleanUninstalled(t, o, out) }
}

func dbPathQueries(f *fakeRunner) int {
	n := 0
	for _, q := range f.queries {
		if strings.Join(q, " ") == strings.Join(dbPathQuery, " ") {
			n++
		}
	}
	return n
}

// lineTexts is the text of every line event in out (no envelope needed).
func lineTexts(t *testing.T, out *bytes.Buffer) []string {
	t.Helper()
	var r []string
	for _, l := range nonEmptyLines(out.String()) {
		var ev map[string]any
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatal(err)
		}
		if ev["t"] == EventLine {
			r = append(r, ev["text"].(string))
		}
	}
	return r
}

// A lock in the custom DBPath is seen as busy by the verb's start check.
func TestCustomDBPathLockIsBusy(t *testing.T) {
	dir, f := fakeCache(t)
	p := leftover(t, dir, "download-Cus123")
	lock, o, _ := customDB(t, f)
	writeFile(t, lock)
	var out bytes.Buffer
	o.Stdout = &out
	if code := Run([]string{"paccache", "3"}, o); code != ExitBusy {
		t.Fatalf("exit %d, want 5:\n%s", code, &out)
	}
	if !strings.Contains(out.String(), lock+" exists") {
		t.Errorf("the busy message doesn't name %s:\n%s", lock, &out)
	}
	if !exists(p) || len(f.streams) != 0 {
		t.Errorf("ran or touched something: %q", join(f.streams))
	}
}

// The verb's check and the tidy's hold use the custom path, and
// pacman-conf is asked for it once.
func TestCustomDBPathHeldByTheTidy(t *testing.T) {
	dir, f := fakeCache(t)
	a := leftover(t, dir, "download-Cus123")
	lock, o, run := customDB(t, f)
	var held []bool
	o.underPacmanLock = func(string) { held = append(held, exists(lock)) }
	lines := run(o)
	if len(held) != 1 || !held[0] {
		t.Errorf("custom lock %s under the hold: %v", lock, held)
	}
	if exists(a) || exists(lock) || len(lines) != 1 {
		t.Errorf("leftover removed %v, lock gone %v, lines %q", !exists(a), !exists(lock), lines)
	}
	if n := dbPathQueries(f); n != 1 {
		t.Errorf("pacman-conf DBPath asked %d times, want 1", n)
	}
}

// A lock that appears at the custom path after the verb's start check
// makes the tidy leave everything alone, naming that path.
func TestCustomDBPathLockSeenByTheTidy(t *testing.T) {
	dir, f := fakeCache(t)
	p := leftover(t, dir, "download-Cus123")
	lock, o, run := customDB(t, f)
	o.underPacmanLock = func(name string) { t.Errorf("probed %s under someone else's lock", name) }
	f.onStream = func(argv []string) {
		if argv[0] == Paccache {
			writeFile(t, lock)
		}
	}
	checkNote(t, run(o), "Left pacman's download folders alone: busy: pacman is running ("+lock+" exists)")
	if !exists(p) {
		t.Error("removed a leftover under pacman's lock")
	}
}

// pacman-conf failing or printing nonsense: the default path, one note,
// asked once. (lockPath only; the default path is never touched on disk.)
func TestDBPathFallsBackToDefault(t *testing.T) {
	for name, r := range map[string]reply{
		"fails":          {fail: errors.New("boom")},
		"exit 1":         {exit: 1, err: "error: nope\n"},
		"empty":          {},
		"relative":       {out: "var/lib/pacman\n"},
		"control char":   {out: "/var/lib/pa\x1bcman\n"},
		"two lines":      {out: "/a\n/b\n"},
		"newline inside": {out: "/var/lib\x0apacman\n"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().on(dbPathQuery, r)
			o, out := rootOpts(t, f)
			o.PacmanDBLock = ""
			s := &session{o: o, em: NewEmitter(out)}
			if got := s.lockPath(); got != DefaultPacmanDBLock {
				t.Errorf("lockPath = %q", got)
			}
			if got := s.lockPath(); got != DefaultPacmanDBLock || dbPathQueries(f) != 1 {
				t.Errorf("second call %q, asked %d times", got, dbPathQueries(f))
			}
			notes := lineTexts(t, out)
			if len(notes) != 1 || !strings.Contains(notes[0], "guarding "+DefaultPacmanDBLock+" instead") {
				t.Errorf("notes %q", notes)
			}
		})
	}
}

func TestDBPathValid(t *testing.T) {
	for in, want := range map[string]string{
		"/var/lib/pacman/\n":   "/var/lib/pacman/db.lck",
		"/srv//pac/../db\n":    "/srv/db/db.lck",
		"\n/opt/pacman-db\n\n": "/opt/pacman-db/db.lck",
		"/odd name/it's\n":     "/odd name/it's/db.lck",
		"/var/lib/pacman":      "/var/lib/pacman/db.lck",
	} {
		f := newFake().on(dbPathQuery, reply{out: in})
		o, out := rootOpts(t, f)
		o.PacmanDBLock = ""
		s := &session{o: o, em: NewEmitter(out)}
		if got := s.lockPath(); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
		if out.Len() != 0 {
			t.Errorf("%q: a note for a valid path: %s", in, out)
		}
	}
}

// A lock folder that isn't root's alone: no hold (nothing created, nothing
// removed), a note; an existing lock is still honoured as busy.
func TestUnsafeLockDirIsNotHeldButHonoured(t *testing.T) {
	for _, existing := range []bool{false, true} {
		dir, f := fakeCache(t)
		p := leftover(t, dir, "download-Uns123")
		lock, o, run := customDB(t, f)
		o.lockDirCheck = func(d string) error { return errors.New(d + " is writable by group or others") }
		o.underPacmanLock = func(name string) { t.Errorf("probed %s without the lock", name) }
		if existing {
			f.onStream = func(argv []string) {
				if argv[0] == Paccache {
					writeFile(t, lock)
				}
			}
		}
		lines := run(o)
		if existing {
			checkNote(t, lines, "Left pacman's download folders alone: busy: pacman is running ("+lock+" exists)")
			if !exists(lock) {
				t.Error("an existing lock was removed")
			}
		} else {
			checkNote(t, lines, "not taking pacman's lock "+lock+": ")
			if exists(lock) {
				t.Error("the lock was taken in an unsafe folder")
			}
		}
		if !exists(p) {
			t.Error("removed a leftover without the lock")
		}
	}
}

func TestCheckLockDir(t *testing.T) {
	d := t.TempDir()
	// The test user's folder is not root's, whatever its mode.
	if err := checkLockDir(d); err == nil || !strings.Contains(err.Error(), "not owned by root") {
		t.Errorf("user-owned dir: %v", err)
	}
	if err := checkLockDir(filepath.Join(d, "missing")); err == nil {
		t.Error("missing dir passed")
	}
	f := filepath.Join(d, "file")
	writeFile(t, f)
	if err := checkLockDir(f); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file: %v", err)
	}
	// /usr is root's and 0755; /tmp is root's but world-writable.
	if err := checkLockDir("/usr"); err != nil {
		t.Errorf("/usr: %v", err)
	}
	if err := checkLockDir("/tmp"); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Errorf("/tmp: %v", err)
	}
}
