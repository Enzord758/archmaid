package actions

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// shimPacman puts a pacman (and, if sudo is set, a sudo) running the given
// bodies first on PATH, and nothing else.
func shimPacman(t *testing.T, pacman, sudo string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte("#!/bin/sh\n"+pacman), 0o755); err != nil {
		t.Fatal(err)
	}
	if sudo != "" {
		if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte("#!/bin/sh\n"+sudo), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin")
}

// pacmanFour lists four packages and flags the ones in $FLAG for -Qkq.
const pacmanFour = `case "$1" in
-Qq) printf 'amd-ucode\nbaz\nfoo\nglibc\n' ;;
-Qkq) shift 4; rc=0; for p in "$@"; do case " $FLAG " in *" $p "*) echo "$p /usr/lib/$p.so"; rc=1 ;; esac; done; exit $rc ;;
esac
`

// TestRepairRecords: every way Repair's discovery ends says what it checked.
func TestRepairRecords(t *testing.T) {
	ctx := context.Background()
	env := Env{RepairWorkers: 2}

	t.Run("none", func(t *testing.T) {
		shimPacman(t, pacmanFour, "")
		t.Setenv("FLAG", "")
		n, ok := discoverRepair(ctx, env).(Notice)
		want := "No broken packages: checked 4 installed packages for missing files (pacman -Qk, as the user)."
		if !ok || n.Text != want || n.Err != "" {
			t.Fatalf("got %#v, want %q", n, want)
		}
		if len(n.Details) != 1 || !strings.Contains(n.Details[0], "2 parallel chunks: nothing flagged") ||
			!strings.Contains(n.Log, "$ pacman -Qq\n(4 packages)\n") || !strings.Contains(n.Log, "$ pacman -Qkq -- <2 packages, amd-ucode … baz>") {
			t.Fatalf("record: %q\n%s", n.Details, n.Log)
		}
	})

	t.Run("failed", func(t *testing.T) {
		shimPacman(t, "echo 'error: could not open database' >&2\nexit 1\n", "")
		n, ok := discoverRepair(ctx, env).(Notice)
		if !ok || n.Text != "Couldn't scan for broken packages: error: could not open database" ||
			n.Err != "error: could not open database" || len(n.Details) != 1 || !strings.HasPrefix(n.Details[0], "Tried: pacman -Qq") {
			t.Fatalf("got %#v", n)
		}
	})

	t.Run("flagged, then none as root", func(t *testing.T) {
		shimPacman(t, pacmanFour, "exit 0\n")
		t.Setenv("FLAG", "amd-ucode")
		nr, ok := discoverRepair(ctx, env).(NeedsRoot)
		if !ok || nr.Checked.Summary != "Checked 4 installed packages as the user: 1 flagged (amd-ucode), to recheck as root." ||
			!slices.Equal(nr.Checked.Details, []string{"amd-ucode /usr/lib/amd-ucode.so"}) {
			t.Fatalf("got %#v", nr)
		}
		n, ok := nr.Rescan(ctx, env).(Notice)
		if !ok || n.Text != "No broken packages: checked 4 installed packages; the 1 flagged as the user (amd-ucode) are complete as root." ||
			n.Err != "" || !strings.Contains(n.Log, "$ sudo -n pacman -Qkq -- amd-ucode") {
			t.Fatalf("rescan: %#v", n)
		}
	})

	t.Run("broken as root", func(t *testing.T) {
		shimPacman(t, pacmanFour, "echo 'glibc /usr/lib/libc.so.6'\nexit 1\n")
		t.Setenv("FLAG", "amd-ucode glibc")
		nr := discoverRepair(ctx, env).(NeedsRoot)
		p, ok := nr.Rescan(ctx, env).(Pending)
		if !ok || p.Checked.Summary != "Checked 4 installed packages: 1 broken (glibc), confirmed as root." ||
			!slices.Equal(p.Checked.Details, []string{"glibc /usr/lib/libc.so.6"}) || p.Steps[0].ID != steps.IDRepair {
			t.Fatalf("got %#v", p)
		}
	})

	t.Run("root recheck fails", func(t *testing.T) {
		shimPacman(t, pacmanFour, "echo 'sudo: a password is required' >&2\nexit 1\n")
		t.Setenv("FLAG", "amd-ucode")
		n, ok := discoverRepair(ctx, env).(NeedsRoot).Rescan(ctx, env).(Notice)
		if !ok || n.Err != "sudo: a password is required" || n.Details[0] != "Tried: sudo -n pacman -Qkq -- amd-ucode" {
			t.Fatalf("got %#v", n)
		}
	})
}

// TestRepairCheckStepLogsToTheDiscovery: the polkit recheck is part of the
// discovery, so its log entries go to the discovery's log.
func TestRepairCheckStepLogsToTheDiscovery(t *testing.T) {
	if c := RepairCheckStep([]string{"a"}).LogCategory(); c != logstore.Repair {
		t.Fatalf("category %q, want %q", c, logstore.Repair)
	}
	if c := RepairStep([]string{"a"}).LogCategory(); c.Discovery() {
		t.Fatalf("the reinstall logs to %q, a discovery category: its run would be discovery-only", c)
	}
}

// TestEveryDiscoveryHasARecord: each Discover action names a discovery
// category for its record, and no Steps action has one.
func TestEveryDiscoveryHasARecord(t *testing.T) {
	for _, a := range Menu {
		switch {
		case a.Discover != nil && !a.Record.Discovery():
			t.Errorf("%s: Record %q is not a discovery category", a.ID, a.Record)
		case a.Discover == nil && a.Record != "":
			t.Errorf("%s: a Record without a Discover", a.ID)
		}
	}
}

// TestDownloadsRecords: the downloads discovery says which folder, how many
// items, the newest and oldest, and lists each with its age. Tests can't
// set ctimes, so the clock moves instead.
func TestDownloadsRecords(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "Downloads")
	os.MkdirAll(filepath.Join(dir, "keep"), 0o700)
	for _, f := range []string{"a.iso", "b.txt"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600)
	}
	now := time.Now()
	env := func(at time.Time) Env {
		return Env{Downloads: func() downloads.Options {
			return downloads.Options{Flag: dir, Home: home, Now: func() time.Time { return at }}
		}, DeleteDownloads: func(downloads.Plan) func(context.Context, steps.Env) steps.Result { return nil }}
	}

	n, ok := discoverDownloads(context.Background(), env(now.Add(3*time.Hour))).(Notice)
	if !ok || !strings.HasPrefix(n.Text, "Checked 3 items in ") ||
		!strings.HasSuffix(n.Text, ": none older than 30 days (newest 3 h, oldest 3 h).") || n.Err != "" {
		t.Fatalf("got %#v", n)
	}
	if !slices.Contains(n.Details, "a.iso: 3 h old") || len(n.Details) != 3 || !strings.Contains(n.Log, "Cutoff: ctime before ") {
		t.Fatalf("record: %q\n%s", n.Details, n.Log)
	}

	// 40 days on, everything is old: the found shape carries the record.
	later := now.Add(40 * 24 * time.Hour)
	p, ok := discoverDownloads(context.Background(), env(later)).(Pending)
	if !ok || !strings.Contains(p.Checked.Summary, "older than 30 days (oldest 40 days)") ||
		!slices.Contains(p.Checked.Details, "a.iso: 40 days old, older than 30 days") {
		t.Fatalf("got %#v", p)
	}

	// A folder that can't be resolved is a failed look, with what was tried.
	bad := Env{Downloads: func() downloads.Options { return downloads.Options{Flag: home, Home: home} }}
	n, ok = discoverDownloads(context.Background(), bad).(Notice)
	if !ok || n.Err == "" || len(n.Details) != 1 || !strings.HasPrefix(n.Details[0], "Tried: the downloads folder") {
		t.Fatalf("got %#v", n)
	}

	// An empty folder says so.
	empty := filepath.Join(home, "Empty")
	os.MkdirAll(empty, 0o700)
	n, _ = discoverDownloads(context.Background(), Env{Downloads: func() downloads.Options {
		return downloads.Options{Flag: empty, Home: home}
	}}).(Notice)
	if !strings.HasSuffix(n.Text, ": it is empty, nothing older than 30 days.") {
		t.Fatalf("empty folder: %#v", n)
	}
}

// TestAge: the ages a record shows.
func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second: "0 min", 59 * time.Minute: "59 min", 3 * time.Hour: "3 h",
		47 * time.Hour: "47 h", 49 * time.Hour: "2 days", 40 * 24 * time.Hour: "40 days",
	} {
		if got := age(d); got != want {
			t.Errorf("age(%v) = %q, want %q", d, got, want)
		}
	}
}
