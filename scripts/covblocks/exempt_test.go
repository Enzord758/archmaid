package main

import (
	"strings"
	"testing"
)

// TestReadExempt: seven tab-separated fields, comments skipped, bad lines refused.
func TestReadExempt(t *testing.T) {
	in := "# header\n\ninternal/a/a.go\t10-12\tF\t3\tkeep-exempt\tyes\twhy\n"
	ex, err := ReadExempt(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := Exempt{File: "internal/a/a.go", Start: 10, End: 12, Func: "F", Stmts: 3, Rec: "keep-exempt", Sec: true, Why: "why"}
	if len(ex) != 1 || ex[0] != want {
		t.Fatalf("got %+v, want [%+v]", ex, want)
	}
	for _, bad := range []string{
		"a\t1-2\tF\t3\tkeep\tyes\n",        // six fields
		"a\tx\tF\t3\tkeep\tyes\twhy\n",     // bad lines
		"a\t1-2\tF\tn\tkeep\tyes\twhy\n",   // bad count
		"a\t1-2\tF\t3\tkeep\tmaybe\twhy\n", // bad security flag
	} {
		if _, err := ReadExempt(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// TestReport: exempt statements leave the denominator per scope; an entry
// that matches no uncovered block (or the wrong count) is stale and leaves
// it alone; a scope the profile lacks is not stale.
func TestReport(t *testing.T) {
	blocks := []Block{
		{File: "m/internal/a/a.go", StartLine: 1, EndLine: 2, Stmts: 4, Covered: true},
		{File: "m/internal/a/a.go", StartLine: 5, EndLine: 6, Stmts: 2},
		{File: "m/internal/a/a.go", StartLine: 8, EndLine: 9, Stmts: 4},
		{File: "m/cmd/archmaid-helper/h.go", StartLine: 1, EndLine: 2, Stmts: 10},
	}
	rel := func(f string) string { return strings.TrimPrefix(f, "m/") }
	ex := []Exempt{
		{File: "internal/a/a.go", Start: 5, End: 6, Stmts: 2},  // matches
		{File: "internal/a/a.go", Start: 8, End: 9, Stmts: 99}, // wrong count: stale
		{File: "internal/a/a.go", Start: 1, End: 2, Stmts: 4},  // covered now: stale
		{File: "gui/x.go", Start: 1, End: 1, Stmts: 1},         // gui not in the profile: ignored
	}
	st, stale := Report(blocks, ex, rel)
	if len(stale) != 2 {
		t.Errorf("stale = %+v, want the wrong-count and the covered entry", stale)
	}
	core := st["core"]
	if core.Total != 10 || core.Covered != 4 || core.Exempt != 2 {
		t.Errorf("core = %+v, want total 10 covered 4 exempt 2", *core)
	}
	if got := core.Raw(); got != "40.0" {
		t.Errorf("core raw = %s, want 40.0", got)
	}
	if got := core.Reachable(); got != "50.0" {
		t.Errorf("core reachable = %s, want 50.0", got)
	}
	if st["helper"].Total != 10 || st["all"].Total != 20 || st["all"].Exempt != 2 {
		t.Errorf("helper/all = %+v / %+v", *st["helper"], *st["all"])
	}
	var sb strings.Builder
	WriteReachable(&sb, st)
	if !strings.Contains(sb.String(), "core    raw 40.0% (4/10)   reachable 50.0% (4/8, 2 exempt)") || !strings.Contains(sb.String(), "gui     raw none (0/0)   reachable none (0/0, 0 exempt)") {
		t.Errorf("report:\n%s", sb.String())
	}
}
