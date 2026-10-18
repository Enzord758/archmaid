package ui

import "testing"

// TestRealConfigHasRunReport: the binary's Driver config makes the run
// report (the Run page's summary, the widget's last-run summary): the real
// thing (pacman's log, the commands by name). Building it reads and runs
// nothing; the tests' own configs leave RunReport nil, so no test reads
// the machine's pacman.log or runs paccache.
func TestRealConfigHasRunReport(t *testing.T) {
	cfg := realConfig()
	if cfg.RunReport == nil {
		t.Fatal("archmaid-gui's Driver config has no run report")
	}
	if cfg.RunReport.Query == nil || cfg.TermCols == nil || cfg.Report == nil {
		t.Errorf("config %+v is missing its query, pty width or error report", cfg)
	}
}
