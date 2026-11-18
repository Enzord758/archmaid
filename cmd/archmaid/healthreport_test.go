package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/health"
)

// TestParseArgsHealthReport: --health-report is a flag of its own; combined
// with any other option it is refused (a --dryrun typo must not look like a
// real report run, and the report is never a TUI run).
func TestParseArgsHealthReport(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want flags
		err  bool
	}{
		{args: []string{"--health-report"}, want: flags{healthReport: true}},
		{args: []string{"--health-report", "--health-report"}, want: flags{healthReport: true}},
		{args: []string{"--health-report", "--dryrun"}, err: true},
		{args: []string{"-fc", "--health-report"}, err: true},
		{args: []string{"--health-report", "--downloads", "/d"}, err: true},
		{args: []string{"--health-report=1"}, err: true},
		{args: []string{"--health"}, err: true},
	} {
		f, err := parseArgs(tc.args)
		if (err != nil) != tc.err || (!tc.err && f != tc.want) {
			t.Fatalf("parseArgs(%q) = %+v, %v; want %+v, err=%v", tc.args, f, err, tc.want, tc.err)
		}
	}
	if !strings.Contains(usage, "--health-report") {
		t.Fatalf("usage does not list --health-report:\n%s", usage)
	}
}

// fakeHealthDeps answers every check from nothing: no seam is filled in, so
// every check is Unknown and nothing reaches the machine.
func fakeHealthDeps() health.Deps { return health.Deps{} }

// TestStartupHealthReport: startup writes health.json (0600) in the state dir
// and nothing else, starts no TUI (start=false), exits 0.
func TestStartupHealthReport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	oldDeps, oldDir := healthDeps, healthStateDir
	healthDeps, healthStateDir = fakeHealthDeps, dir
	t.Cleanup(func() { healthDeps, healthStateDir = oldDeps, oldDir })

	var out, errb bytes.Buffer
	_, code, start := startup([]string{"--health-report"}, &out, &errb)
	if start || code != 0 || out.Len() != 0 || errb.Len() != 0 {
		t.Fatalf("start=%v code=%d stdout=%q stderr=%q", start, code, out.String(), errb.String())
	}
	if got := tree(t, dir); len(got) != 2 || got[1] != "health.json" {
		t.Fatalf("state dir holds %v, want health.json only", got)
	}
	fi, err := os.Stat(filepath.Join(dir, "health.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "health.json"))
	var doc struct {
		V         int
		Attention int
		Checks    []struct{ ID, State string }
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc.V != 1 || doc.Attention != 0 || len(doc.Checks) != 11 {
		t.Fatalf("%v %+v\n%s", err, doc, b)
	}
}

// TestStartupHealthReportCannotWrite: a state dir that is a regular file is
// an error on stderr and exit 1, never a TUI start.
func TestStartupHealthReportCannotWrite(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	oldDeps, oldDir := healthDeps, healthStateDir
	healthDeps, healthStateDir = fakeHealthDeps, file
	t.Cleanup(func() { healthDeps, healthStateDir = oldDeps, oldDir })
	var out, errb bytes.Buffer
	_, code, start := startup([]string{"--health-report"}, &out, &errb)
	if start || code != 1 || !strings.Contains(errb.String(), "--health-report") {
		t.Fatalf("start=%v code=%d stderr=%q", start, code, errb.String())
	}
}
