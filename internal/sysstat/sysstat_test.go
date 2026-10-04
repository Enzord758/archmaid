package sysstat

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// fixtureRoots points the status line at fixtures under t.TempDir() and
// returns the Paths plus the fake pacman cache dir.
func fixtureRoots(t *testing.T, meminfo string) (Paths, string) {
	t.Helper()
	dir := t.TempDir()
	mem := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(mem, []byte(meminfo), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "pkg")
	if err := os.Mkdir(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	return Paths{Disk: dir, Meminfo: mem, Cache: cache}, cache
}

// TestHumanSizes: the formatters reproduce df -h / du -h (round up, no "i")
// and free -h (round to nearest, "i" suffix).
func TestHumanSizes(t *testing.T) {
	ceil := map[uint64]string{
		0:                  "0",
		1:                  "1",
		4096:               "4.0K",
		729088:             "712K",
		179721728000:       "168G", // df -h Avail of a real 229G disk
		10 * 1024 * 1024:   "10M",
		10*1024*1024 + 1:   "11M",
		9*1024*1024 + 1:    "9.1M",
		1023 * 1024 * 1024: "1023M",
		1<<30 - 1:          "1.0G",
		1 << 30:            "1.0G",
		1<<40 + 1<<30:      "1.1T",
	}
	for n, want := range ceil {
		if got := humanCeil(n); got != want {
			t.Errorf("humanCeil(%d) = %q, want %q", n, got, want)
		}
	}
	round := map[uint64]string{
		512:         "512B",
		181346304:   "173Mi",
		10882617344: "10Gi", // free -h on a 31Gi box
		3 << 29:     "1.5Gi",
	}
	for n, want := range round {
		if got := humanRound(n); got != want {
			t.Errorf("humanRound(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestLine: the rendered text is exactly the header line.
func TestLine(t *testing.T) {
	got := Status{Free: "100G", RAM: "8G", Cache: "512M"}.Line()
	if want := " Free: 100G   RAM avail: 8G   Cache: 512M"; got != want {
		t.Fatalf("Line() = %q, want %q", got, want)
	}
}

// TestReadSpawnsNothing: with an empty PATH any exec of df/free/du/bash
// fails, so real numbers can only have come from the native readers.
func TestReadSpawnsNothing(t *testing.T) {
	p, cache := fixtureRoots(t, "MemTotal:       32557020 kB\nMemFree:        10627556 kB\nMemAvailable:   21546776 kB\n")
	if err := os.WriteFile(filepath.Join(cache, "a.pkg"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	got := Read(p).Line()
	re := regexp.MustCompile(`^ Free: \d+(\.\d)?[KMGTP]?   RAM avail: 21Gi   Cache: (\d+\.\d|\d+)[KMGT]$`)
	if !re.MatchString(got) {
		t.Fatalf("status %q does not match %v", got, re)
	}
}

// TestReadFallbacks: unreadable sources show N/A, never an empty field.
func TestReadFallbacks(t *testing.T) {
	p, _ := fixtureRoots(t, "garbage\n")
	p.Cache = filepath.Join(t.TempDir(), "missing")
	p.Disk = filepath.Join(t.TempDir(), "missing")
	want := " Free: N/A   RAM avail: N/A   Cache: N/A"
	if got := Read(p).Line(); got != want {
		t.Fatalf("status %q, want %q", got, want)
	}
	if got := memAvail(filepath.Join(t.TempDir(), "missing")); got != "N/A" {
		t.Fatalf("memAvail of a missing file = %q", got)
	}
}

func BenchmarkSystemStatus(b *testing.B) {
	p := DefaultPaths()
	for i := 0; i < b.N; i++ {
		Read(p).Line()
	}
}
