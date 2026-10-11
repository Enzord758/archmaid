package main

import (
	"strings"
	"testing"
)

// TestScopeTestOnly: the test-only packages are their own scope, out of core,
// and in "all".
func TestScopeTestOnly(t *testing.T) {
	for rel, want := range map[string]string{
		"internal/testenv/run.go":               "testonly",
		"internal/testtranscript/t.go":          "testonly",
		"internal/engine/enginetest/e.go":       "testonly",
		"internal/privilege/privtest/p.go":      "testonly",
		"internal/helperclient/helpertest/h.go": "testonly",
		"internal/engine/engine.go":             "core",
		"internal/privilege/askpass.go":         "core",
		"internal/testenvironment/x.go":         "core",
		"cmd/archmaid-helper/main.go":           "helper",
		"gui/internal/ui/x.go":                  "gui",
	} {
		if got := Scope(rel); got != want {
			t.Errorf("Scope(%s) = %s, want %s", rel, got, want)
		}
	}
	blocks := []Block{
		{File: "m/internal/a/a.go", StartLine: 1, EndLine: 2, Stmts: 4, Covered: true},
		{File: "m/internal/testenv/t.go", StartLine: 1, EndLine: 2, Stmts: 3, Covered: true},
		{File: "m/internal/testenv/t.go", StartLine: 5, EndLine: 6, Stmts: 2},
	}
	st, _ := Report(blocks, nil, func(f string) string { return strings.TrimPrefix(f, "m/") })
	if st["core"].Total != 4 || st["testonly"].Total != 5 || st["testonly"].Covered != 3 || st["all"].Total != 9 {
		t.Errorf("core %+v testonly %+v all %+v", *st["core"], *st["testonly"], *st["all"])
	}
	var sb strings.Builder
	WriteReachable(&sb, st)
	if !strings.Contains(sb.String(), "testonly raw 60.0% (3/5)") {
		t.Errorf("report:\n%s", sb.String())
	}
}
