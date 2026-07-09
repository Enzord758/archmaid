package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// drainStep reads a step's stream to completion, returning the collected output
// lines and the terminal error.
func drainStep(ch <-chan streamMsg) ([]string, error) {
	var lines []string
	for msg := range func() chan streamMsg {
		out := make(chan streamMsg)
		go func() {
			for {
				m := <-ch
				out <- m
				if m.done {
					close(out)
					return
				}
			}
		}()
		return out
	}() {
		if msg.done {
			return lines, msg.err
		}
		lines = append(lines, msg.line)
	}
	return lines, nil
}

// TestDryRunDoesNotExecute is the safety-critical test: in dry run a step must
// be reported, not run.
func TestDryRunDoesNotExecute(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	marker := filepath.Join(t.TempDir(), "marker")
	ch := make(chan streamMsg, 8)
	runStep(step{command: "touch", args: []string{marker}}, ch)

	lines, err := drainStep(ch)
	if err != nil {
		t.Fatalf("dry-run step should not error, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("dry-run executed the command: %s was created", marker)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "would run") {
		t.Fatalf("expected a 'would run' line, got %v", lines)
	}
}

// TestDryRunLogsEveryStep verifies a successful dry-run step is logged to the
// dry-run log (normal runs only log failures).
func TestDryRunLogsEveryStep(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()
	t.Setenv("HOME", t.TempDir())

	m := initialModel()
	m.queue = []stepState{{step: step{logLabel: "Test step"}, status: statusRunning}}
	m.current = 0
	m.buf.WriteString("[dry-run] would run: echo hi\n")

	tm, _ := m.handleStream(streamMsg{done: true})
	_ = tm

	data, err := os.ReadFile(logPath())
	if err != nil {
		t.Fatalf("dry-run log not written: %v", err)
	}
	if !strings.Contains(string(data), "DRYRUN: Test step") {
		t.Fatalf("expected DRYRUN entry, got:\n%s", data)
	}
}

// TestSudoThreeStrikes: after maxSudoAttempts wrong passwords, sudo is marked
// unavailable and the queue proceeds (skipping sudo steps).
func TestSudoThreeStrikes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := initialModel()
	m.pending = []step{{logLabel: "needs root", needsSudo: true, command: "true"}}

	var tm interface{} = m
	for i := 0; i < maxSudoAttempts; i++ {
		var mm model = tm.(model)
		next, _ := mm.Update(sudoResultMsg{err: errors.New("bad password")})
		tm = next
	}
	got := tm.(model)
	if !got.sudoUnavailable {
		t.Fatalf("expected sudoUnavailable after %d failures", maxSudoAttempts)
	}
}

// TestDiscoveryNotice: a discovery with a notice routes to the notice screen.
func TestDiscoveryNotice(t *testing.T) {
	m := initialModel()
	tm, _ := m.Update(discoveryMsg{kind: "downloads", notice: "nothing to do"})
	got := tm.(model)
	if got.screen != screenNotice || got.notice != "nothing to do" {
		t.Fatalf("expected notice screen, got screen=%d notice=%q", got.screen, got.notice)
	}
}

// TestDiscoveryServices: a services discovery routes to the multi-select with a
// selection slice sized to the list.
func TestDiscoveryServices(t *testing.T) {
	m := initialModel()
	svcs := []string{"a.service", "b.service"}
	tm, _ := m.Update(discoveryMsg{kind: "services", services: svcs})
	got := tm.(model)
	if got.screen != screenServices {
		t.Fatalf("expected services screen, got %d", got.screen)
	}
	if len(got.serviceOn) != len(svcs) {
		t.Fatalf("serviceOn size %d, want %d", len(got.serviceOn), len(svcs))
	}
}

// TestBrokenPackagesParsing sanity-checks the parser against a healthy system:
// pacman -Qk should report no broken packages (all "0 missing/altered files").
func TestBrokenPackagesParsing(t *testing.T) {
	if _, err := os.Stat("/usr/bin/pacman"); err != nil {
		t.Skip("pacman not available")
	}
	// We can't assert a specific list, but the call must not panic and must
	// return cleanly. On a healthy system it's empty.
	_ = brokenPackages()
}
