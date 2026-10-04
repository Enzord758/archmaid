package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
)

// repairShim describes the machine the Repair shims fake: the installed
// packages (pacman -Qq), which of them pacman -Qkq flags when run as the user
// and which when run as root, and whether sudo has a ticket.
type repairShim struct {
	installed []string
	asUser    []string // the truly broken ones plus root-only-dir false positives
	asRoot    []string // the truly broken ones
	sudoOK    bool
}

// install puts a fake `pacman` + `sudo` pair first on PATH and returns the
// file both append their argv to ("pacman …" / "sudo …"). pacman -Qkq prints
// one "pkg /path" line per flagged package among its targets and exits 1 if
// it printed any, as the real one does; which list counts depends on whether
// it runs under the sudo shim. With sudoOK, sudo answers the -v probe and
// runs its argv "as root" (dropping -n); otherwise it fails the way `sudo -n`
// does without a ticket.
func (r repairShim) install(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "exec.log")
	sudo := fmt.Sprintf(`#!/bin/sh
echo "sudo $*" >> %[1]s
if [ %[2]t != true ]; then echo 'sudo: a password is required' >&2; exit 1; fi
[ "$1" = "-n" ] && shift
[ "$1" = "-v" ] && exit 0
SHIM_AS_ROOT=1 exec "$@"
`, shQuote(log), r.sudoOK)
	pacman := fmt.Sprintf(`#!/bin/sh
echo "pacman $*" >> %[1]s
case "$1" in
-Qq) printf '%%s\n' %[2]s; exit 0;;
-Qkq)
	shift; [ "$1" = "--" ] && shift
	broken=%[3]s; [ -n "$SHIM_AS_ROOT" ] && broken=%[4]s
	rc=0
	for p in "$@"; do for b in $broken; do
		if [ "$p" = "$b" ]; then echo "$p /usr/share/$p/missing"; rc=1; fi
	done; done
	exit $rc;;
esac
exit 0
`, shQuote(log), strings.Join(r.installed, " "), shQuote(strings.Join(r.asUser, " ")), shQuote(strings.Join(r.asRoot, " ")))
	for name, script := range map[string]string{"sudo": sudo, "pacman": pacman} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// execLog returns the shim invocations logged so far whose program is name.
func execLog(t *testing.T, log, name string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range nonEmptyLines(string(b)) {
		if strings.HasPrefix(l, name+" ") {
			lines = append(lines, l)
		}
	}
	return lines
}

// pinScanWorkers fixes Repair's scan parallelism for one test.
func pinScanWorkers(t *testing.T, n int) {
	t.Helper()
	old := repairScanWorkers
	repairScanWorkers = n
	t.Cleanup(func() { repairScanWorkers = old })
}

// repairScan runs Repair's user scan with workers, as the menu would.
func repairScan(t *testing.T, workers int) actions.Discovery {
	t.Helper()
	return discoverNow(t, actions.IDRepair, func(env *actions.Env) { env.RepairWorkers = workers })
}

// repairCandidates is what repairScan flagged for the root recheck.
func repairCandidates(t *testing.T, workers int) []string {
	t.Helper()
	d := repairScan(t, workers)
	nr, ok := d.(actions.NeedsRoot)
	if !ok {
		t.Fatalf("workers=%d: user scan = %#v, want candidates for the root recheck", workers, d)
	}
	return nr.Items
}

// repairRecheck runs the root recheck of whatever the user scan flags.
func repairRecheck(t *testing.T) actions.Discovery {
	t.Helper()
	nr, ok := repairScan(t, 2).(actions.NeedsRoot)
	if !ok {
		t.Fatal("the user scan flagged nothing to recheck")
	}
	return nr.Rescan(context.Background(), actionEnv())
}

// TestBrokenCandidatesParallelMatchesSingle: however the packages are split,
// the user scan reports exactly what one pacman -Qkq over all of them does,
// in the same order. Each chunk is its own `pacman -Qkq -- …` run as the
// user — never through sudo — and every installed package is checked once.
func TestBrokenCandidatesParallelMatchesSingle(t *testing.T) {
	var installed []string
	for i := range 37 {
		installed = append(installed, fmt.Sprintf("pkg%02d", i))
	}
	flagged := []string{"pkg00", "pkg07", "pkg08", "pkg19", "pkg36"}
	log := repairShim{installed: installed, asUser: flagged}.install(t)

	single := repairCandidates(t, 1)
	if !reflect.DeepEqual(single, flagged) {
		t.Fatalf("single call: got %v, want %v", single, flagged)
	}
	for _, workers := range []int{2, 4, 16, 100} {
		if err := os.Truncate(log, 0); err != nil {
			t.Fatal(err)
		}
		got := repairCandidates(t, workers)
		if !reflect.DeepEqual(got, single) {
			t.Fatalf("workers=%d: got %v, want %v", workers, got, single)
		}
		if s := execLog(t, log, "sudo"); len(s) != 0 {
			t.Fatalf("workers=%d: the user scan ran sudo: %v", workers, s)
		}
		calls := execLog(t, log, "pacman")
		if calls[0] != "pacman -Qq" || len(calls)-1 != min(workers, len(installed)) {
			t.Fatalf("workers=%d: want pacman -Qq then %d chunks, got %v", workers, min(workers, len(installed)), calls)
		}
		var checked []string
		for _, c := range calls[1:] {
			targets, ok := strings.CutPrefix(c, "pacman -Qkq -- ")
			if !ok {
				t.Fatalf("workers=%d: chunk call %q, want pacman -Qkq -- <pkgs>", workers, c)
			}
			checked = append(checked, strings.Fields(targets)...)
		}
		slices.Sort(checked)
		if !reflect.DeepEqual(checked, installed) {
			t.Fatalf("workers=%d: checked %v, want each installed package once", workers, checked)
		}
	}
}

// TestBrokenCandidatesErrors: a failing package list, or a chunk that fails
// with nothing on stdout, is an error rather than "nothing broken".
func TestBrokenCandidatesErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	write := func(script string) {
		if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("echo 'error: could not open database' >&2; exit 1\n")
	if n := noticeOf(repairScan(t, 4)); !strings.Contains(n, "Couldn't scan") || !strings.Contains(n, "could not open database") {
		t.Fatalf("want the -Qq failure, got %q", n)
	}
	write(`if [ "$1" = -Qq ]; then printf 'a\nb\nc\nd\n'; exit 0; fi
case "$*" in *c*) echo 'error: chunk failed' >&2; exit 1;; esac
exit 0
`)
	if n := noticeOf(repairScan(t, 4)); !strings.Contains(n, "Couldn't scan") || !strings.Contains(n, "chunk failed") {
		t.Fatalf("want the chunk's failure, got %q", n)
	}
}

// TestBrokenPackagesRechecksAsRoot: the recheck is one `sudo -n pacman -Qkq --`
// over the candidates only, and pacman's exit 1 (missing files found) is a
// result, not a failure.
func TestBrokenPackagesRechecksAsRoot(t *testing.T) {
	log := repairShim{
		installed: []string{"amd-ucode", "bad", "z"},
		asUser:    []string{"amd-ucode", "bad"},
		asRoot:    []string{"bad"},
		sudoOK:    true,
	}.install(t)
	p, ok := repairRecheck(t).(actions.Pending)
	if !ok || len(p.Steps) != 1 || !reflect.DeepEqual(p.Steps[0].Args(), []string{"pacman", "-S", "--noconfirm", "--", "bad"}) {
		t.Fatalf("got %+v, want a reinstall of just bad", p)
	}
	if s := execLog(t, log, "sudo"); !reflect.DeepEqual(s, []string{"sudo -n pacman -Qkq -- amd-ucode bad"}) {
		t.Fatalf("sudo calls %v, want one recheck of the candidates", s)
	}
}

// TestBrokenPackagesSudoUnavailable: without a sudo ticket the recheck reports
// an error instead of a misleading "no broken packages".
func TestBrokenPackagesSudoUnavailable(t *testing.T) {
	repairShim{installed: []string{"bad"}, asUser: []string{"bad"}}.install(t)
	if n := noticeOf(repairRecheck(t)); !strings.Contains(n, "Couldn't scan") || !strings.Contains(n, "password is required") {
		t.Fatalf("want a couldn't-scan notice with sudo's error, got %q", n)
	}
}
