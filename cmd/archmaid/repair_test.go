package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

// repairModel returns a model with the cursor on the Repair menu entry.
func repairModel(t *testing.T) model {
	t.Helper()
	m := initialModel()
	for i, it := range menu {
		if it.id == "repair" {
			m.cursor = i
			return m
		}
	}
	t.Fatal("no repair menu item")
	return m
}

// updateRun feeds msg through Update, then runs the cmd it returns and gives
// back the msg that produces (nil when there's no cmd).
func updateRun(t *testing.T, m model, msg tea.Msg) (model, tea.Msg) {
	t.Helper()
	next, cmd := m.Update(msg)
	if cmd == nil {
		return next.(model), nil
	}
	return next.(model), cmd()
}

// repairFlagged is a model whose user scan has flagged candidates and that
// now probes sudo for the root recheck.
func repairFlagged(t *testing.T, candidates ...string) model {
	t.Helper()
	m := update(repairModel(t), key("enter"))
	next, cmd := m.Update(discoveryMsg{kind: "repair", candidates: candidates})
	m = next.(model)
	if cmd == nil || m.scanAfterAuth == nil {
		t.Fatal("candidates should probe sudo with the root recheck held back")
	}
	return m
}

// TestParseBrokenPackages: `pacman -Qkq` prints "pkg /path" per missing file;
// a package with several missing files is listed once, and stray lines (a
// warning, a bare word) are ignored.
func TestParseBrokenPackages(t *testing.T) {
	out := "warning: something odd\n" +
		"amd-ucode /boot/amd-ucode.img\n" +
		"big /usr/share/big/one\n" +
		"big /usr/share/big/two\n" +
		"stray\n"
	got := parseBrokenPackages(out)
	want := []string{"amd-ucode", "big"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := parseBrokenPackages(""); len(got) != 0 {
		t.Fatalf("empty output should be no packages, got %v", got)
	}
}

// TestParseBrokenPackagesFixture parses a capture of real -Qkq output shape.
func TestParseBrokenPackagesFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/pacman_Qkq.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := parseBrokenPackages(string(data))
	want := []string{"amd-ucode", "nfs-utils", "big"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestChunkStrings: at most n contiguous chunks that together are s in order,
// sizes differing by at most one; never an empty chunk.
func TestChunkStrings(t *testing.T) {
	s := strings.Fields("a b c d e f g h i j")
	for _, n := range []int{-1, 0, 1, 3, 4, 10, 16} {
		chunks := chunkStrings(s, n)
		if want := max(1, min(n, len(s))); len(chunks) != want {
			t.Fatalf("n=%d: %d chunks, want %d", n, len(chunks), want)
		}
		lo, hi := len(s), 0
		for _, c := range chunks {
			lo, hi = min(lo, len(c)), max(hi, len(c))
		}
		if lo == 0 || hi-lo > 1 {
			t.Fatalf("n=%d: uneven chunks %v", n, chunks)
		}
		if got := slices.Concat(chunks...); !reflect.DeepEqual(got, s) {
			t.Fatalf("n=%d: chunks %v don't add up to %v", n, chunks, s)
		}
	}
	if got := chunkStrings(nil, 4); len(got) != 0 {
		t.Fatalf("nothing installed should be no chunks, got %v", got)
	}
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

	single, err := brokenCandidates(1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(single, flagged) {
		t.Fatalf("single call: got %v, want %v", single, flagged)
	}
	for _, workers := range []int{2, 4, 16, 100} {
		if err := os.Truncate(log, 0); err != nil {
			t.Fatal(err)
		}
		got, err := brokenCandidates(workers)
		if err != nil {
			t.Fatalf("workers=%d: %v", workers, err)
		}
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
	if _, err := brokenCandidates(4); err == nil || !strings.Contains(err.Error(), "could not open database") {
		t.Fatalf("want the -Qq failure, got %v", err)
	}
	write(`if [ "$1" = -Qq ]; then printf 'a\nb\nc\nd\n'; exit 0; fi
case "$*" in *c*) echo 'error: chunk failed' >&2; exit 1;; esac
exit 0
`)
	if _, err := brokenCandidates(4); err == nil || !strings.Contains(err.Error(), "chunk failed") {
		t.Fatalf("want the chunk's failure, got %v", err)
	}
	msg := scanRepairCandidates()().(discoveryMsg)
	if !strings.Contains(msg.notice, "Couldn't scan") || len(msg.candidates) != 0 {
		t.Fatalf("want a couldn't-scan notice, got %+v", msg)
	}
}

// TestQkqOutput: pacman's exit 1 with a report on stdout is a result; an
// error with an empty stdout is reported with stderr, or the exit status.
func TestQkqOutput(t *testing.T) {
	out, err := qkqOutput("sh", "-c", "echo 'bad /usr/bad'; exit 1")
	if err != nil || out != "bad /usr/bad\n" {
		t.Fatalf("exit 1 with a report: got %q, %v", out, err)
	}
	if _, err := qkqOutput("sh", "-c", "echo nope >&2; exit 1"); err == nil || err.Error() != "nope" {
		t.Fatalf("want stderr as the error, got %v", err)
	}
	if _, err := qkqOutput("sh", "-c", "exit 3"); err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("want the exit status, got %v", err)
	}
}

// TestBrokenPackagesRechecksAsRoot: the recheck is one `sudo -n pacman -Qkq --`
// over the candidates only, and pacman's exit 1 (missing files found) is a
// result, not a failure.
func TestBrokenPackagesRechecksAsRoot(t *testing.T) {
	log := repairShim{installed: []string{"a", "bad", "z"}, asRoot: []string{"bad"}, sudoOK: true}.install(t)
	got, err := brokenPackages([]string{"amd-ucode", "bad"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"bad"}) {
		t.Fatalf("got %v, want [bad]", got)
	}
	if s := execLog(t, log, "sudo"); !reflect.DeepEqual(s, []string{"sudo -n pacman -Qkq -- amd-ucode bad"}) {
		t.Fatalf("sudo calls %v, want one recheck of the candidates", s)
	}
}

// TestBrokenPackagesSudoUnavailable: without a sudo ticket the recheck reports
// an error instead of a misleading "no broken packages".
func TestBrokenPackagesSudoUnavailable(t *testing.T) {
	repairShim{asUser: []string{"bad"}}.install(t)
	if _, err := brokenPackages([]string{"bad"}); err == nil || !strings.Contains(err.Error(), "password is required") {
		t.Fatalf("want sudo's error, got %v", err)
	}
	msg := discoverRepair([]string{"bad"})().(discoveryMsg)
	if !strings.Contains(msg.notice, "Couldn't scan") {
		t.Fatalf("want a couldn't-scan notice, got %q", msg.notice)
	}
}

// TestRepairNothingFlaggedNeverAsksForSudo: Repair scans as the user first;
// with nothing flagged it says so without a probe, a prompt or any sudo.
func TestRepairNothingFlaggedNeverAsksForSudo(t *testing.T) {
	pinScanWorkers(t, 4)
	log := repairShim{installed: []string{"a", "b", "c"}, sudoOK: true}.install(t)
	m, msg := updateRun(t, repairModel(t), key("enter"))
	if m.screen != screenDiscovering || m.scanAfterAuth != nil {
		t.Fatalf("repair should scan straight away, got screen=%d", m.screen)
	}
	m, _ = updateRun(t, m, msg)
	if m.screen != screenNotice || !strings.Contains(m.notice, "No broken packages") {
		t.Fatalf("want the nothing-broken notice, got screen=%d notice=%q", m.screen, m.notice)
	}
	if s := execLog(t, log, "sudo"); len(s) != 0 {
		t.Fatalf("nothing flagged must not touch sudo, got %v", s)
	}
}

// TestRepairRechecksOnlyCandidatesAsRoot: the whole flow with a ticket
// cached. The user scan flags a broken package and two false positives; one
// probe, one root recheck of just those three, and only the confirmed one is
// offered for reinstalling.
func TestRepairRechecksOnlyCandidatesAsRoot(t *testing.T) {
	pinScanWorkers(t, 3)
	log := repairShim{
		installed: []string{"amd-ucode", "bad", "glibc", "nfs-utils", "zlib"},
		asUser:    []string{"amd-ucode", "bad", "nfs-utils"},
		asRoot:    []string{"bad"},
		sudoOK:    true,
	}.install(t)
	m, msg := updateRun(t, repairModel(t), key("enter")) // user scan
	if dm := msg.(discoveryMsg); !reflect.DeepEqual(dm.candidates, []string{"amd-ucode", "bad", "nfs-utils"}) {
		t.Fatalf("candidates %v", dm.candidates)
	}
	if s := execLog(t, log, "sudo"); len(s) != 0 {
		t.Fatalf("the user scan ran sudo: %v", s)
	}
	m, msg = updateRun(t, m, msg) // → probe
	m, msg = updateRun(t, m, msg) // cached → root recheck
	if m.discovering == "" || m.screen != screenDiscovering {
		t.Fatalf("a cached ticket should go straight to the recheck, got screen=%d", m.screen)
	}
	dm := msg.(discoveryMsg)
	if len(dm.pending) != 1 || !slices.Contains(dm.pending[0].args, "bad") ||
		slices.Contains(dm.pending[0].args, "amd-ucode") || slices.Contains(dm.pending[0].args, "nfs-utils") {
		t.Fatalf("want a reinstall of just bad, got %+v", dm.pending)
	}
	m = update(m, msg)
	if m.screen != screenConfirm || !strings.Contains(m.confirmMsg, "Reinstall 1 broken") {
		t.Fatalf("want the reinstall confirm, got screen=%d %q", m.screen, m.confirmMsg)
	}
	want := []string{"sudo -n -v", "sudo -n pacman -Qkq -- amd-ucode bad nfs-utils"}
	if s := execLog(t, log, "sudo"); !reflect.DeepEqual(s, want) {
		t.Fatalf("sudo calls %v, want %v", s, want)
	}
}

// TestRepairFalsePositiveDropped: a package flagged as the user but clean as
// root is a root-only directory, not a broken package: nothing to reinstall.
func TestRepairFalsePositiveDropped(t *testing.T) {
	repairShim{installed: []string{"amd-ucode", "glibc"}, asUser: []string{"amd-ucode"}, sudoOK: true}.install(t)
	m, msg := updateRun(t, repairModel(t), key("enter"))
	m, msg = updateRun(t, m, msg)
	m, msg = updateRun(t, m, msg)
	m = update(m, msg)
	if m.screen != screenNotice || !strings.Contains(m.notice, "No broken packages") || len(m.queue) != 0 {
		t.Fatalf("want the nothing-broken notice, got screen=%d notice=%q", m.screen, m.notice)
	}
}

// TestRepairAsksForSudoOnlyForCandidates: once the user scan has flagged
// something, Repair probes sudo; uncached, it shows the password prompt (as
// Full Cleanup does), and a successful auth runs the root recheck rather than
// an empty queue.
func TestRepairAsksForSudoOnlyForCandidates(t *testing.T) {
	m := update(repairModel(t), key("enter"))
	if m.scanAfterAuth != nil {
		t.Fatal("the user scan must not wait on sudo")
	}
	m = repairFlagged(t, "bad")
	m = update(m, sudoCachedMsg{cached: false})
	if m.screen != screenPassword {
		t.Fatalf("uncached sudo should prompt, got screen=%d", m.screen)
	}
	next, cmd := m.Update(sudoResultMsg{})
	m = next.(model)
	if m.screen != screenDiscovering || cmd == nil || m.scanAfterAuth != nil {
		t.Fatalf("auth should start the recheck, got screen=%d", m.screen)
	}
}

// TestRepairCachedSudoScansDirectly: with a ticket already cached there's no
// prompt; the recheck starts straight away.
func TestRepairCachedSudoScansDirectly(t *testing.T) {
	m := repairFlagged(t, "bad")
	next, cmd := m.Update(sudoCachedMsg{cached: true})
	m = next.(model)
	if m.screen != screenDiscovering || cmd == nil {
		t.Fatalf("cached sudo should recheck at once, got screen=%d", m.screen)
	}
}

// TestRepairPromptEscCancels: esc on the password prompt drops the held
// recheck, so a later unrelated auth can't trigger it.
func TestRepairPromptEscCancels(t *testing.T) {
	m := repairFlagged(t, "bad")
	m = update(m, sudoCachedMsg{cached: false})
	m = update(m, key("esc"))
	if m.screen != screenMenu || m.scanAfterAuth != nil {
		t.Fatalf("esc should cancel the recheck, got screen=%d", m.screen)
	}
}

// TestRepairAuthGiveUpShowsNotice: after the last wrong password there's no
// queue to skip through, so Repair says why it stopped.
func TestRepairAuthGiveUpShowsNotice(t *testing.T) {
	m := repairFlagged(t, "bad")
	m = update(m, sudoCachedMsg{cached: false})
	for i := 0; i < maxSudoAttempts; i++ {
		m = update(m, sudoResultMsg{err: fmt.Errorf("wrong")})
	}
	if m.screen != screenNotice || !strings.Contains(m.notice, "can't scan") || m.scanAfterAuth != nil {
		t.Fatalf("want a give-up notice, got screen=%d notice=%q", m.screen, m.notice)
	}
}

// TestRepairDiscoveryDoesNotReprobeSudo: the recheck only runs once the
// ticket is primed, so its result starts the queue directly instead of
// probing again.
func TestRepairDiscoveryDoesNotReprobeSudo(t *testing.T) {
	m := repairModel(t)
	next, _ := m.Update(discoveryMsg{kind: "repair", pending: []step{
		{title: "fix", logLabel: "fix", command: "sudo", needsSudo: true},
	}})
	m = next.(model)
	if m.screen != screenRunning {
		t.Fatalf("repair result should start the queue, got screen=%d", m.screen)
	}
}
