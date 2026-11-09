package tui

import (
	"errors"
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// last-run.json is the engine's: it classifies the outcome and asks for
// WriteLastRun (internal/engine's tests), the Driver writes it, logstore
// keeps the dry-run slot apart (internal/logstore). These run whole
// scenarios through the TUI and read the record they left.

func readLastRun(t *testing.T) logstore.RunRecord {
	t.Helper()
	r, err := logs().ReadLastRun()
	if err != nil {
		t.Fatalf("ReadLastRun: %v", err)
	}
	return r
}

// End to end: three wrong passwords give up on sudo, the user-level steps
// still run, so Full Cleanup is incomplete with every sudo step refused (7:
// the AUR cache step is one since #194).
func TestLastRunThreeWrongPasswordsIsIncomplete(t *testing.T) {
	d := runScenario(t, "fullcleanup_three_wrong_passwords.shim")
	refused := 0
	for _, q := range d.m.queue {
		if q.status == steps.AuthRefused {
			refused++
		}
	}
	if refused != 7 {
		t.Fatalf("%d steps refused, want 7 (did the scenario change?)", refused)
	}
	r := readLastRun(t)
	if r.Action != "fullcleanup" || r.Outcome != "incomplete" || r.StepsRefused != refused ||
		r.StepsSkipped != refused || r.StepsFailed != 0 {
		t.Fatalf("record = %+v, want incomplete with %d refused", r, refused)
	}
}

// runScenario runs the transcript scenario name through the TUI
// (tuiEngineTarget), for a test that checks more than its golden does (the
// model it left behind, d.m).
func runScenario(t *testing.T, name string) *Driver {
	t.Helper()
	for _, sc := range transcriptScenarios {
		if sc.name != name {
			continue
		}
		var tg *tuiEngineTarget
		r := sc.run(t, func(t *testing.T, shimLog string) tt.Target {
			tg = newTUIEngineTarget(t, shimLog)
			return tg
		})
		return &Driver{r, tg}
	}
	t.Fatalf("no transcript scenario %q", name)
	return nil
}

// End to end: aborting topgrade in Full Cleanup records every step from it
// on as skipped.
func TestLastRunAbortedFullCleanup(t *testing.T) {
	d := runScenario(t, "fullcleanup_abort_topgrade.shim")
	if st := d.m.queue[1].status; st != steps.Aborted {
		t.Fatalf("topgrade status = %d, want Aborted", st)
	}
	if r := readLastRun(t); r.Outcome != "aborted" || r.StepsSkipped != len(d.m.queue)-1 {
		t.Fatalf("record = %+v, want aborted with %d skipped", r, len(d.m.queue)-1)
	}
}

// End to end: a declined gate is a cancelled run, and nothing ran: it leaves
// no last-run.json (the previous run's record stays, #276).
func TestLastRunDeclinedGateLeavesNoRecord(t *testing.T) {
	runScenario(t, "fullcleanup_gate_declined.shim")
	if r, err := logs().ReadLastRun(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record = %+v (%v), want none: nothing ran", r, err)
	}
}

func TestLastRunAbsentBeforeAnyRun(t *testing.T) {
	useLogDir(t, t.TempDir())
	if _, err := logs().ReadLastRun(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
}
