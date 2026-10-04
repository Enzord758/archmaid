package actions

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/steps"
)

// testEnv is an Env whose LookPath finds everything when have is true and
// nothing otherwise, with a stand-in audit step.
func testEnv(have bool) Env {
	return Env{
		LookPath: func(file string) (string, error) {
			if have {
				return "/usr/bin/" + file, nil
			}
			return "", exec.ErrNotFound
		},
		AuditStep: func() steps.Step {
			return steps.Step{ID: steps.IDAURAudit, Prefetch: steps.PrefetchAURAudit,
				Run: func(context.Context, steps.Env) steps.Result { return steps.Result{} }}
		},
	}
}

func ids(q []steps.Step) []string {
	var out []string
	for _, s := range q {
		out = append(out, s.ID)
	}
	return out
}

// TestNoPrivilegedStepAfterHyprpm: hyprpm drops the sudo ticket on exit
// (sudo -k), so in every action's queue nothing that needs root may follow
// it. Nor may any in-process step: its result can insert a step behind
// itself (the orphan probe inserts the root removal), which would then run
// after hyprpm too.
func TestNoPrivilegedStepAfterHyprpm(t *testing.T) {
	found := false
	for _, a := range Menu {
		if a.Steps == nil {
			continue
		}
		q := a.Steps(testEnv(true))
		h := slices.IndexFunc(q, func(s steps.Step) bool { return s.ID == steps.IDHyprpm })
		if h < 0 {
			continue
		}
		found = true
		for _, s := range q[h+1:] {
			if s.NeedsPriv {
				t.Errorf("%s: %s needs root but runs after hyprpm", a.ID, s.ID)
			}
			if s.Run != nil {
				t.Errorf("%s: %s is in-process (it may insert a root step) but runs after hyprpm", a.ID, s.ID)
			}
		}
		if q[h].NeedsPriv {
			t.Errorf("%s: hyprpm must not need root (it elevates itself)", a.ID)
		}
	}
	if !found {
		t.Fatal("no action has a hyprpm step: the invariant checked nothing")
	}
}

// TestFullCleanupOrder pins Full Cleanup's queue: the audit directly in front
// of topgrade, the cache and orphan steps, the cleanup, and hyprpm last —
// only when it is installed.
func TestFullCleanupOrder(t *testing.T) {
	a, _ := ByID(IDFullCleanup)
	want := []string{steps.IDAURAudit, steps.IDTopgrade, steps.IDPaccache, steps.IDPaccacheWipe,
		steps.IDOrphansProbe, steps.IDYayCache, steps.IDTmpfiles, steps.IDJournal, steps.IDIntegrity}
	if got := ids(a.Steps(testEnv(false))); !slices.Equal(got, want) {
		t.Fatalf("without hyprpm: %v, want %v", got, want)
	}
	if got := ids(a.Steps(testEnv(true))); !slices.Equal(got, append(want, steps.IDHyprpm)) {
		t.Fatalf("with hyprpm: %v, want %v + hyprpm", got, want)
	}
	tp, _ := ByID(IDTopgrade)
	if got := ids(tp.Steps(testEnv(true))); !slices.Equal(got, []string{steps.IDAURAudit, steps.IDTopgrade}) {
		t.Fatalf("Run topgrade: %v", got)
	}
}

// TestMenuTable: IDs are unique and found by ByID; every action is either a
// queue, a discovery or one of the front end's own entries, never both.
func TestMenuTable(t *testing.T) {
	own := []string{IDEditConfig, IDViewLog, IDExit}
	seen := map[string]bool{}
	for _, a := range Menu {
		if seen[a.ID] {
			t.Fatalf("duplicate action ID %q", a.ID)
		}
		seen[a.ID] = true
		if got, ok := ByID(a.ID); !ok || got.Label != a.Label {
			t.Fatalf("ByID(%q) = %+v, %v", a.ID, got, ok)
		}
		if a.Label == "" || a.Icon == "" {
			t.Fatalf("%s: needs a label and an icon", a.ID)
		}
		switch {
		case a.Steps != nil && a.Discover != nil:
			t.Fatalf("%s: both Steps and Discover", a.ID)
		case a.Discover != nil && a.Busy == "":
			t.Fatalf("%s: a discovery needs a Busy label", a.ID)
		case a.Steps == nil && a.Discover == nil && !slices.Contains(own, a.ID):
			t.Fatalf("%s: neither a queue, a discovery nor a front-end entry", a.ID)
		}
	}
	if _, ok := ByID("nope"); ok {
		t.Fatal("ByID found an unknown action")
	}
}

// TestQueueStepsHaveIDs: every step a queue builds has a stable ID, unique in
// its queue, and the root-helper verbs sit on the steps they belong to.
func TestQueueStepsHaveIDs(t *testing.T) {
	verbs := map[string]string{
		steps.IDPaccache: steps.VerbPaccache, steps.IDPaccacheWipe: steps.VerbPaccacheWipe,
		steps.IDOrphansProbe: steps.VerbPlanOrphans, steps.IDTmpfiles: steps.VerbCleanTmp,
		steps.IDJournal: steps.VerbVacuumJournal, steps.IDIntegrity: steps.VerbCheckFiles,
	}
	for _, a := range Menu {
		if a.Steps == nil {
			continue
		}
		q := a.Steps(testEnv(true))
		seen := map[string]bool{}
		for _, s := range q {
			if s.ID == "" || seen[s.ID] {
				t.Fatalf("%s: step %q has a missing or repeated ID", a.ID, s.Title)
			}
			seen[s.ID] = true
			if s.Verb != verbs[s.ID] {
				t.Fatalf("%s: step %s has verb %q, want %q", a.ID, s.ID, s.Verb, verbs[s.ID])
			}
		}
	}
}

func TestDisableUnits(t *testing.T) {
	if q := DisableUnits(nil); q != nil {
		t.Fatalf("nothing picked: %+v, want no queue", q)
	}
	q := DisableUnits([]string{"a.service", "b.service"})
	if len(q) != 1 || q[0].ID != steps.IDDisableUnits || q[0].Verb != steps.VerbDisableUnit || !q[0].NeedsPriv ||
		!slices.Equal(q[0].Argv, []string{"sudo", "systemctl", "disable", "--now", "--", "a.service", "b.service"}) {
		t.Fatalf("got %+v", q)
	}
}

// TestAuditReportIsHandedIn: the AUR-audit action is the front end's report.
func TestAuditReportIsHandedIn(t *testing.T) {
	a, _ := ByID(IDAURAudit)
	env := Env{AuditReport: func(context.Context) Discovery { return Notice{"from the front end"} }}
	if d := a.Discover(context.Background(), env); d != (Notice{"from the front end"}) {
		t.Fatalf("got %#v", d)
	}
}

// TestClipErr: a notice's error text is cut to fit.
func TestClipErr(t *testing.T) {
	if got := clipErr(errors.New("short")); got != "short" {
		t.Fatal(got)
	}
	long := clipErr(errors.New(string(make([]byte, 100))))
	if len(long) != 67+len("…") {
		t.Fatalf("len %d", len(long))
	}
}

// TestEnvValidate: the zero Env is refused, naming every required field; a
// complete one passes, and each required field left out is named alone.
func TestEnvValidate(t *testing.T) {
	err := Env{}.Validate()
	if err == nil {
		t.Fatal("the zero Env validated")
	}
	required := []string{"AuditStep", "AuditReport", "Downloads", "DeleteDownloads"}
	for _, f := range required {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("%v: doesn't name %s", err, f)
		}
	}
	full := func() Env {
		e := testEnv(true)
		e.AuditReport = func(context.Context) Discovery { return Notice{} }
		e.Downloads = func() downloads.Options { return downloads.Options{} }
		e.DeleteDownloads = func(downloads.Plan) func(context.Context, steps.Env) steps.Result { return nil }
		return e
	}
	if err := full().Validate(); err != nil {
		t.Fatalf("a complete Env: %v", err)
	}
	for _, f := range required {
		e := full()
		switch f {
		case "AuditStep":
			e.AuditStep = nil
		case "AuditReport":
			e.AuditReport = nil
		case "Downloads":
			e.Downloads = nil
		case "DeleteDownloads":
			e.DeleteDownloads = nil
		}
		err := e.Validate()
		if err == nil || !strings.Contains(err.Error(), " "+f+" ") {
			t.Errorf("without %s: %v, want it named", f, err)
		}
	}
	// The optional ones may stay zero.
	e := full()
	e.LookPath, e.RepairWorkers, e.DryRun = nil, 0, false
	if err := e.Validate(); err != nil {
		t.Fatalf("optional fields left zero: %v", err)
	}
}

func TestEnvDefaults(t *testing.T) {
	var e Env
	if e.repairWorkers() != DefaultRepairWorkers {
		t.Fatal("RepairWorkers 0 should mean the default")
	}
	if _, err := e.lookPath("sh"); err != nil {
		t.Fatalf("nil LookPath should be exec.LookPath: %v", err)
	}
}
