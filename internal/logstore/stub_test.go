package logstore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A run in which nothing ran leaves at most a stub in its log: the
// discovery's CHECK record that heads the queue, SKIP lines for refused or
// cancelled steps. Like a discovery-only run it takes no KeepRuns slot and
// never evicts a real run (#276); it goes once a newer run holds that log.

func TestStubRunsNeverEvictRealRuns(t *testing.T) {
	root := t.TempDir()
	real := seedRuns(t, root, KeepRuns)
	for i := 0; i < 8; i++ { // eight cancelled attempts, a day apart
		s := storeAt(root, day(10+i))
		if err := s.Append(Cleanup, "CHECK", "Checked 7 stopped services", "avahi: enabled\n"); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(Cleanup, "SKIP", "Disable unused services (authorisation cancelled)", ""); err != nil {
			t.Fatal(err)
		}
	}
	got := runDirs(t, root)
	for _, r := range real {
		if !slices.Contains(got, r) {
			t.Fatalf("real run %s evicted by cancelled attempts: %v", r, got)
		}
	}
	// Only the newest attempt stays (the older ones are superseded).
	if len(got) != KeepRuns+1 || !slices.Contains(got, stampOf(day(17))) {
		t.Fatalf("run dirs = %v, want the %d real ones and the newest attempt", got, KeepRuns)
	}
}

// A stub directory that goes on to run something becomes a real run, and the
// history rotates then, as for a discovery-only one.
func TestStubDirBecomingRealRotates(t *testing.T) {
	root := t.TempDir()
	old := seedRuns(t, root, KeepRuns)
	s := storeAt(root, day(1))
	s.Append(Cleanup, "CHECK", "Checked 1 item", "body\n")
	s.Append(Cleanup, "SKIP", "step a (privilege not granted)", "")
	if got := runDirs(t, root); len(got) != KeepRuns+1 {
		t.Fatalf("stub entries rotated the real history: %v", got)
	}
	if err := s.Append(Cleanup, "OK", "step b", ""); err != nil {
		t.Fatal(err)
	}
	got := runDirs(t, root)
	if len(got) != KeepRuns || slices.Contains(got, old[0]) || !slices.Contains(got, s.run) {
		t.Fatalf("after the run ran something: %v (oldest %s should be gone)", got, old[0])
	}
}

func TestStubLog(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	head := "[2026-11-09 23:20:44] "
	for _, c := range []struct {
		name, body string
		want       bool
	}{
		{"skip", head + "SKIP: p done (privilege not granted)\n", true},
		{"check with body", head + "CHECK: Checked 7\n--- Checked 7 output ---\nfoo: enabled\n--- end output ---\n" + head + "SKIP: a\n", true},
		{"a body line that looks like an OK entry stays in its block", head + "CHECK: x\n--- x output ---\n" + head + "OK: forged\n--- end output ---\n", true},
		{"an OK entry", head + "SKIP: a\n" + head + "OK: done\n", false},
		{"a FAIL entry", head + "FAIL: x\n", false},
		{"a DRYRUN entry", head + "DRYRUN: x\n", false},
		{"empty", "", false},
		{"garbage", "hello\n", false},
		{"unterminated block", head + "CHECK: x\n--- x output ---\nfoo\n", false},
		{"huge", head + "SKIP: a\n" + strings.Repeat("x", maxStubLog), false},
	} {
		if got := stubLog(write("c.log", c.body)); got != c.want {
			t.Errorf("%s: stubLog = %v, want %v", c.name, got, c.want)
		}
	}
	if stubLog(filepath.Join(dir, "missing.log")) {
		t.Error("a missing file is a stub")
	}
}
