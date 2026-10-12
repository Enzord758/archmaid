package realdriver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/settings"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// The real Driver runs here only against fakes: no pkexec, sudo or su is
// reached (testenv's traps would fail the package), the faillock count,
// the orphan plan and the foreign-package list are handed in, and the
// state dir is a t.TempDir. The full GUI paths (pkexec and sudo shims,
// the askpass relay, the dialogs) are the headless harness's
// (gui/internal/ui, TestGUIRealDriver).
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// TestRelay: --askpass <sock> [prompt] is the relay, whatever follows;
// anything else starts the GUI. A socket nobody listens on exits 1.
func TestRelay(t *testing.T) {
	for _, args := range [][]string{nil, {"--stub"}, {"--askpass"}, {"x", "--askpass", "s"}} {
		if _, relayed := Relay(args); relayed {
			t.Errorf("Relay(%q) relayed", args)
		}
	}
	sock := filepath.Join(t.TempDir(), "none.sock")
	if code, relayed := Relay([]string{"--askpass", sock, "[sudo] password:"}); !relayed || code != 1 {
		t.Errorf("Relay to a dead socket: %d %v, want 1 true", code, relayed)
	}
}

func fakes(t *testing.T, cfg Config) Config {
	cfg.StateDir = filepath.Join(t.TempDir(), "state")
	cfg.Faillock = func() engine.FaillockRead { return engine.FaillockRead{Deny: 3} }
	direct, cascade := []string{"oldlib"}, []string{"oldlib 1.0-1"}
	cfg.PlanOrphans = func(context.Context) (steps.OrphanPlan, error) {
		return steps.OrphanPlan{Hash: helperclient.PlanHash(direct, cascade), Direct: direct, Cascade: cascade}, nil
	}
	cfg.Foreign = func(context.Context) ([]string, error) { return nil, nil }
	cfg.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	cfg.RuntimeDir = t.TempDir()
	if cfg.RefreshUpdates == nil {
		cfg.RefreshUpdates = func(context.Context) error { return nil } // never systemctl or the packaged script
	}
	return cfg
}

// TestOptions: the base Options pass Validate (PlanOrphans is required
// under polkit) and are on the polkit path; the Settings page's dry run
// and folder come through New's Env.
func TestOptions(t *testing.T) {
	w := Start(fakes(t, Config{}))
	defer w.Close()
	if !w.Askpass() {
		t.Error("no askpass Server")
	}
	o := w.Options()
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	if o.Auth != engine.AuthPolkit || !o.Env.Polkit {
		t.Errorf("auth %v polkit %v", o.Auth, o.Env.Polkit)
	}
	dl := t.TempDir()
	applied := settings.Settings{DryRun: true, Downloads: dl}.Apply(o)
	e := w.env(applied.Env, applied.DryRun, w.store(applied.DryRun))
	if err := e.Validate(); err != nil || !e.DryRun || e.Downloads().Flag != dl || !e.Downloads().DryRun {
		t.Errorf("env %v dry %v downloads %+v", err, e.DryRun, e.Downloads())
	}
}

// TestDryRunFullCleanup: Full Cleanup as a dry run on the real Driver,
// under polkit: the gate, then every step reports what it would do; no
// helper is started, so no polkit wait and no faillock read.
func TestDryRunFullCleanup(t *testing.T) {
	w := Start(fakes(t, Config{}))
	defer w.Close()
	o := w.Options()
	o.DryRun = true
	d := w.New(o)
	defer d.Close()
	d.Send(engine.Start{Action: actions.IDFullCleanup})
	timeout := time.After(20 * time.Second)
	var waits int
	var rows []string
	for {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				t.Fatal("Events closed early")
			}
			switch ev := ev.(type) {
			case engine.NeedsConfirm:
				d.Send(engine.Confirm{Prompt: ev.ID, Yes: true})
			case engine.AuthWaiting, engine.FaillockState:
				waits++
			case engine.StepDone:
				rows = append(rows, fmt.Sprintf("%s=%d", ev.ID, ev.Status))
			case engine.Finished:
				if ev.Outcome != engine.OutcomeCompleted || waits != 0 {
					t.Errorf("outcome %v, %d auth events; rows %q", ev.Outcome, waits, rows)
				}
				if !slices.Contains(rows, fmt.Sprintf("%s=%d", steps.IDOrphansRemove, steps.OK)) {
					t.Errorf("rows %q: want the planned orphan removal, dry", rows)
				}
				return
			}
		case <-timeout:
			t.Fatalf("no Finished; rows %q", rows)
		}
	}
}

// TestMenuAuditNoForeign: the menu audit without foreign packages is a
// notice, and reaches no network.
func TestMenuAuditNoForeign(t *testing.T) {
	w := Start(fakes(t, Config{NoAskpass: true}))
	defer w.Close()
	d := w.env(actions.Env{}, false, w.store(false)).AuditReport(context.Background())
	if n, ok := d.(actions.Notice); !ok || !strings.Contains(n.Text, "No foreign") {
		t.Errorf("discovery %#v", d)
	}
}
