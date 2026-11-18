package dms

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// healthRun runs archmaid-updates-check with a fake checkupdates (exit rc) and,
// when archmaidBody is not "", a fake `archmaid` whose script is archmaidBody.
// PATH holds only shims and the few tools the script needs, so the real
// archmaid can never be reached. It returns the argv lines the fake archmaid
// was called with and the script's exit code.
func healthRun(t *testing.T, rc int, archmaidBody string) ([]string, int) {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range []string{"sh", "mktemp", "date", "grep", "chmod", "mkdir", "rm", "mv", "cat", "timeout", "sed", "awk"} {
		src, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("tool %s: %v", tool, err)
		}
		if err := os.Symlink(src, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.MkdirTemp("", "health") // not t.TempDir(): its name holds the subtest name, shell syntax
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(data) })
	calls := filepath.Join(data, "calls")
	shim := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shim("checkupdates", "exit "+strconv.Itoa(rc)+"\n")
	if archmaidBody != "" {
		shim("archmaid", "echo \"$*\" >>"+calls+"\n"+archmaidBody)
	}
	home := t.TempDir()
	cmd := exec.Command(filepath.Join(bin, "sh"), "../systemd/archmaid-updates-check")
	cmd.Env = []string{"PATH=" + bin, "HOME=" + home}
	cmd.Stdin = strings.NewReader("")
	err = cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, code
}

// The updates script ends with `archmaid --health-report`, whatever the
// updates check did, and the report never changes the script's exit status.
func TestUpdatesScriptRunsHealthReport(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rc       int // checkupdates' exit status
		archmaid string
		wantCode int
		wantCall bool
	}{
		{"updates ok", 2, "exit 0\n", 0, true},
		{"updates ok, report fails", 2, "exit 7\n", 0, true},
		{"checkupdates fails (offline): the report still runs, the script still fails", 1, "exit 0\n", 1, true},
		{"checkupdates fails, report fails too", 1, "exit 9\n", 1, true},
		{"no archmaid installed", 2, "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, code := healthRun(t, tc.rc, tc.archmaid)
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d", code, tc.wantCode)
			}
			if tc.wantCall && (len(calls) != 1 || calls[0] != "--health-report") {
				t.Fatalf("archmaid calls = %q, want one --health-report", calls)
			}
			if !tc.wantCall && len(calls) != 0 {
				t.Fatalf("archmaid calls = %q", calls)
			}
		})
	}
}
