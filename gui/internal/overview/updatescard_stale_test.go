package overview

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A result written before the click is not the answer to it (#266): the
// first check after a GUI start said "Up to date" with updates waiting,
// because a run that was already going (the timer's) finished with a count
// from before the packages changed, and the card took that file. With real
// files and their times: only one written at or after the click counts.
func TestUpdatesCardIgnoresAResultWrittenBeforeTheClick(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "updates-cache.json")
	write := func(count int, mod time.Time) {
		t.Helper()
		n := strconv.Itoa(count)
		body := `{"v":3,"count":` + n + `,"repo":` + n + `,"aur":0,"aur_held":0,"checked":"` + mod.Format(time.RFC3339) + `"}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	read := func() (Updates, time.Time) {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := ParseUpdates(b)
		return u, fi.ModTime()
	}
	click := cardNow
	write(0, click.Add(-time.Hour))
	u0, m0 := read()
	var c UpdatesCard
	c.Begin(u0, m0, click)

	// The in-flight run's file: new content, but written before the click.
	write(0, click.Add(-2*time.Second))
	u1, m1 := read()
	if c.Poll(u1, m1, click.Add(5*time.Second)) || !c.Checking() {
		t.Fatal("a file written before the click ended the check")
	}
	// The run the click started.
	write(4, click.Add(20*time.Second))
	u2, m2 := read()
	if !c.Poll(u2, m2, click.Add(21*time.Second)) || c.Checking() {
		t.Fatal("a file written after the click was not taken")
	}
	if v := c.View(u2, click.Add(21*time.Second)); v.Title != "4 updates waiting" {
		t.Fatalf("%+v", v)
	}
}
