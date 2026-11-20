package ui

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
)

// The GUI's performance budgets. With ARCHMAID_GUI_PERF=1 a breach fails the
// test; every budget is measured and logged whatever the variable says.
// scripts/gui-perf.sh (repo root) runs them and prints one line per budget.
//
// ARCHMAID_GUI_PERF_SCALE (default 1) loosens every budget by that factor, for
// a slower machine (a ceiling is multiplied by it, a floor divided): the VM CI
// sets it, the owner's desktop does not. ARCHMAID_GUI_PERF_REPORT names a file
// each budget appends one line to (name, measured, unit, limit, ok; tab
// separated); the script reads it for its summary.

var perfReportMu sync.Mutex

func perfScale() float64 {
	if v, err := strconv.ParseFloat(os.Getenv("ARCHMAID_GUI_PERF_SCALE"), 64); err == nil && v >= 1 {
		return v
	}
	return 1
}

// budget checks measured against limit: a ceiling (max true) or a floor.
// name is e.g. "flood/apply-p95"; unit is for the log only.
func budget(t *testing.T, name string, measured, limit float64, unit string, max bool) {
	t.Helper()
	lim := limit * perfScale()
	op := "<="
	if !max {
		lim = limit / perfScale()
		op = ">="
	}
	ok := (max && measured <= lim) || (!max && measured >= lim)
	t.Logf("budget %s: %.4g %s, budget %s %.4g (scale x%.2g)", name, measured, unit, op, lim, perfScale())
	if p := os.Getenv("ARCHMAID_GUI_PERF_REPORT"); p != "" {
		perfReportMu.Lock()
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%s\t%.4g\t%s\t%s%.4g\t%t\n", name, measured, unit, op, lim, ok)
			f.Close()
		}
		perfReportMu.Unlock()
	}
	if perf() && !ok {
		t.Errorf("budget %s: %.4g %s, want %s %.4g", name, measured, unit, op, lim)
	}
}
