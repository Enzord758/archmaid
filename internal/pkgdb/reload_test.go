package pkgdb

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixturePaths(dir string) Paths {
	return Paths{Sync: filepath.Join(dir, "sync"), Local: filepath.Join(dir, "local"), Conf: filepath.Join(dir, "pacman.conf")}
}

// TestReload: nothing is re-read until a sync file, the local directory or
// pacman.conf changes; a failed reload keeps the old data.
func TestReload(t *testing.T) {
	dir := fixture(t)
	db, err := openDir(dir, []string{"core", "extra"})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.Reload(); changed || err != nil {
		t.Fatalf("untouched: changed %v err %v", changed, err)
	}

	// A sync DB replaced (a new package, a new mtime).
	raw := tarBytes(t, []tent{{"newpkg-1", desc("newpkg", "1-1", "%DESC%\nbrand new\n\n")}})
	core := filepath.Join(dir, "sync", "core.db")
	os.WriteFile(core, gz(t, raw), 0o644)
	later := time.Now().Add(time.Hour)
	os.Chtimes(core, later, later)
	if changed, err := db.Reload(); !changed || err != nil {
		t.Fatalf("sync changed: changed %v err %v", changed, err)
	}
	if _, ok := db.Lookup("newpkg"); !ok {
		t.Fatal("the new sync DB was not read")
	}
	if changed, _ := db.Reload(); changed {
		t.Fatal("a second Reload re-read unchanged files")
	}

	// A package installed (a new directory in local/).
	os.MkdirAll(filepath.Join(dir, "local", "newpkg-1-1"), 0o755)
	os.WriteFile(filepath.Join(dir, "local", "newpkg-1-1", "desc"), []byte(desc("newpkg", "1-1", "")), 0o644)
	later = later.Add(time.Minute)
	os.Chtimes(filepath.Join(dir, "local"), later, later)
	if changed, err := db.Reload(); !changed || err != nil {
		t.Fatalf("local changed: changed %v err %v", changed, err)
	}
	if p, _ := db.Lookup("newpkg"); !p.Installed {
		t.Fatal("the new install was not read")
	}

	// Everything gone: the reload fails and the old data stays.
	os.RemoveAll(filepath.Join(dir, "sync"))
	os.RemoveAll(filepath.Join(dir, "local"))
	if changed, err := db.Reload(); changed || err == nil {
		t.Fatalf("nothing readable: changed %v err %v", changed, err)
	}
	if _, ok := db.Lookup("newpkg"); !ok {
		t.Fatal("a failed reload dropped the old data")
	}
}

// TestReloadWhileSearching: readers always see one whole snapshot (run with
// -race in CI where cgo is available).
func TestReloadWhileSearching(t *testing.T) {
	dir := fixture(t)
	db, err := openDir(dir, []string{"core", "extra"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if len(db.Search("vim", 10)) == 0 || len(db.Installed(ViewExplicit)) == 0 {
					t.Error("a reader saw an empty snapshot")
					return
				}
			}
		}()
	}
	core := filepath.Join(dir, "sync", "core.db")
	for i := 0; i < 20; i++ {
		m := time.Now().Add(time.Duration(i+1) * time.Hour)
		os.Chtimes(core, m, m)
		if changed, err := db.Reload(); !changed || err != nil {
			t.Errorf("reload %d: %v %v", i, changed, err)
		}
	}
	close(stop)
	wg.Wait()
}

// TestOrphansForeignUpdatesMatchPacman ties pkgdb's rules to pacman's answers
// for the same fixture. The shim below is the pacman of this test (first on
// PATH, ahead of testenv's trap): it answers -Qdtq, -Qmq and -Qu from
// testdata/pacman-*.txt, which hold what `pacman -b <fixture> -Qdtq` etc.
// print for testdata/db (derived by hand from pacman's rules: -Qdt is a
// dependency nothing requires or optionally requires, counting provides; -Qm a
// package in no sync DB; -Qu a newer version in the first repo that has it).
// Any other argument fails the test.
func TestOrphansForeignUpdatesMatchPacman(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
"-Qdtq") cat "$ANSWERS/pacman-qdtq.txt";;
"-Qmq") cat "$ANSWERS/pacman-qmq.txt";;
"-Qu") cat "$ANSWERS/pacman-qu.txt";;
*) echo "unexpected pacman $*" >&2; exit 99;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "pacman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs("testdata")
	t.Setenv("ANSWERS", abs)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ask := func(arg string) []string {
		out, err := exec.Command("pacman", arg).Output()
		if err != nil {
			t.Fatalf("pacman %s: %v", arg, err)
		}
		f := strings.Fields(string(out))
		sort.Strings(f)
		return f
	}

	db := openFixture(t)
	eq(t, "-Qdtq", names(db.Installed(ViewOrphans)), ask("-Qdtq"))
	eq(t, "-Qmq", names(db.Installed(ViewForeign)), ask("-Qmq"))
	// -Qu prints "name old -> new"; keep the names.
	var want []string
	out, _ := exec.Command("pacman", "-Qu").Output()
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		want = append(want, strings.Fields(l)[0])
	}
	var got []string
	for _, p := range db.installedAll() {
		if p.Update {
			got = append(got, p.Name)
		}
	}
	eq(t, "-Qu", got, want)
}
