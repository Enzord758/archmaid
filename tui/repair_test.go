package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// shimRepairScan puts a fake `sudo` + `pacman` pair on PATH for the Repair
// scan. With sudoOK the sudo shim drops -n and execs its argv (so pacman runs
// "as root"); otherwise it fails the way `sudo -n` does without a ticket.
// pacman -Qk prints qkOutput and exits qkExit (1 when files are missing).
func shimRepairScan(t *testing.T, sudoOK bool, qkOutput string, qkExit int) {
	t.Helper()
	dir := t.TempDir()
	sudo := "#!/bin/sh\necho 'sudo: a password is required' >&2\nexit 1\n"
	if sudoOK {
		sudo = "#!/bin/sh\n[ \"$1\" = \"-n\" ] && shift\nexec \"$@\"\n"
	}
	payload := strings.ReplaceAll(qkOutput, "\n", "\\n")
	pacman := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"-Qk\" ]; then printf '%s'; exit %d; fi\n", payload, qkExit)
	for name, script := range map[string]string{"sudo": sudo, "pacman": pacman} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
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

// TestParseBrokenPackages: only packages with a non-zero missing count are
// broken — including "10 missing files", which the old `grep -v '0 missing'`
// silently dropped — and warning lines are ignored.
func TestParseBrokenPackages(t *testing.T) {
	out := "a52dec: 22 total files, 0 missing files\n" +
		"warning: amd-ucode: /boot/amd-ucode.img (No such file or directory)\n" +
		"amd-ucode: 17 total files, 1 missing file\n" +
		"big: 300 total files, 10 missing files\n" +
		"nfs-utils: 124 total files, 0 missing files\n"
	got := parseBrokenPackages(out)
	want := []string{"amd-ucode", "big"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestBrokenPackagesRunsAsRoot: the scan goes through `sudo -n pacman -Qk`,
// and pacman's exit 1 (missing files found) is a result, not a failure.
func TestBrokenPackagesRunsAsRoot(t *testing.T) {
	shimRepairScan(t, true, "ok: 3 total files, 0 missing files\nbad: 3 total files, 2 missing files\n", 1)
	got, err := brokenPackages()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"bad"}) {
		t.Fatalf("got %v, want [bad]", got)
	}
}

// TestBrokenPackagesSudoUnavailable: without a sudo ticket the scan reports an
// error instead of a misleading "no broken packages".
func TestBrokenPackagesSudoUnavailable(t *testing.T) {
	shimRepairScan(t, false, "", 0)
	if _, err := brokenPackages(); err == nil || !strings.Contains(err.Error(), "password is required") {
		t.Fatalf("want sudo's error, got %v", err)
	}
	msg := discoverRepair()().(discoveryMsg)
	if !strings.Contains(msg.notice, "Couldn't scan") {
		t.Fatalf("want a couldn't-scan notice, got %q", msg.notice)
	}
}

// TestRepairAsksForSudoFirst: Repair probes sudo before scanning; uncached, it
// shows the password prompt (as Full Cleanup does), and a successful auth runs
// the scan rather than an empty queue.
func TestRepairAsksForSudoFirst(t *testing.T) {
	m := repairModel(t)
	next, cmd := m.Update(key("enter"))
	m = next.(model)
	if cmd == nil || m.scanAfterAuth == nil {
		t.Fatal("repair should probe sudo with the scan held back")
	}
	m = update(m, sudoCachedMsg{cached: false})
	if m.screen != screenPassword {
		t.Fatalf("uncached sudo should prompt, got screen=%d", m.screen)
	}
	next, cmd = m.Update(sudoResultMsg{})
	m = next.(model)
	if m.screen != screenDiscovering || cmd == nil || m.scanAfterAuth != nil {
		t.Fatalf("auth should start the scan, got screen=%d", m.screen)
	}
}

// TestRepairCachedSudoScansDirectly: with a ticket already cached there's no
// prompt; the scan starts straight away.
func TestRepairCachedSudoScansDirectly(t *testing.T) {
	m := update(repairModel(t), key("enter"))
	next, cmd := m.Update(sudoCachedMsg{cached: true})
	m = next.(model)
	if m.screen != screenDiscovering || cmd == nil {
		t.Fatalf("cached sudo should scan at once, got screen=%d", m.screen)
	}
}

// TestRepairPromptEscCancels: esc on the password prompt drops the held scan,
// so a later unrelated auth can't trigger it.
func TestRepairPromptEscCancels(t *testing.T) {
	m := update(repairModel(t), key("enter"))
	m = update(m, sudoCachedMsg{cached: false})
	m = update(m, key("esc"))
	if m.screen != screenMenu || m.scanAfterAuth != nil {
		t.Fatalf("esc should cancel the scan, got screen=%d", m.screen)
	}
}

// TestRepairAuthGiveUpShowsNotice: after the last wrong password there's no
// queue to skip through, so Repair says why it stopped.
func TestRepairAuthGiveUpShowsNotice(t *testing.T) {
	m := update(repairModel(t), key("enter"))
	m = update(m, sudoCachedMsg{cached: false})
	for i := 0; i < maxSudoAttempts; i++ {
		m = update(m, sudoResultMsg{err: fmt.Errorf("wrong")})
	}
	if m.screen != screenNotice || !strings.Contains(m.notice, "can't scan") || m.scanAfterAuth != nil {
		t.Fatalf("want a give-up notice, got screen=%d notice=%q", m.screen, m.notice)
	}
}

// TestRepairDiscoveryDoesNotReprobeSudo: the scan only runs once the ticket is
// primed, so its result starts the queue directly instead of probing again.
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
