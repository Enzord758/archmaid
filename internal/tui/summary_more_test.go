package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/report"
)

// TestSummaryDowngradedAndSingulars: a downgrade has its own line when there
// is one; one step, one package and a zero width (80 columns) read right.
func TestSummaryDowngradedAndSingulars(t *testing.T) {
	r := &report.Report{
		Elapsed: 52 * time.Second,
		Steps:   report.StepTally{Done: 1},
		Packages: &report.Packages{AURKnown: true,
			Downgraded: []report.Pkg{pk("slowpoke", "2", "1", false)}},
		Cache: &report.CacheHold{Bytes: 100 << 20, Packages: 1},
	}
	out := summaryBlock(r, summaryOpts{}) // width 0: 80
	for _, want := range []string{"took 52 s  ·  1 step: 1 done", "0 upgraded", "Downgraded  1 · slowpoke", "holds 100 MiB (1 package)."} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(0 AUR)") {
		t.Errorf("an AUR count with nothing upgraded:\n%s", out)
	}
}

// TestWrapAndNameListEdges: a word longer than the line is cut, an empty
// list is empty, text with no words wraps to nothing.
func TestWrapAndNameListEdges(t *testing.T) {
	long := strings.Repeat("x", 80)
	got := wrap("a "+long+" b", 20)
	if len(got) != 3 || got[0] != "a" || len([]rune(got[1])) != 20 || got[2] != "b" {
		t.Errorf("wrap = %q", got)
	}
	if nameList(nil, 3, 40) != "" || len(wrap("   ", 20)) != 0 {
		t.Error("empty input must give empty output")
	}
}
