package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/audit"
)

// ── audit-state.json: the unacknowledged high findings ───────────────────────

func statePath() string { return currentAuditConfig().StatePath }

func readState(t *testing.T) audit.State {
	t.Helper()
	s, err := audit.ReadState(statePath())
	if err != nil {
		t.Fatalf("audit-state.json: %v", err)
	}
	return s
}

// stateTakeoverFixture: victim was bob's, is now mallory's; steady is unchanged.
func stateTakeoverFixture(t *testing.T) {
	t.Helper()
	auditEnv(t)
	shimPacmanForeign(t, "steady\nvictim\n")
	fakeAUR(t, map[string]aurInfo{
		"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10},
		"victim": {Name: "victim", Maintainer: strp("mallory"), LastModified: aged(24 * 90), NumVotes: 10},
	})
	saveBaseline([]string{"steady", "victim"}, map[string]aurInfo{
		"steady": {Name: "steady", Maintainer: strp("alice")},
		"victim": {Name: "victim", Maintainer: strp("bob")},
	}, nil)
}

func wantHigh(t *testing.T, s audit.State, reasons ...string) {
	t.Helper()
	if len(s.High) != len(reasons) {
		t.Fatalf("high = %+v, want reasons %v", s.High, reasons)
	}
	for i, r := range reasons {
		if s.High[i].Reason != r {
			t.Fatalf("high[%d] = %+v, want reason %q", i, s.High[i], r)
		}
	}
}

func TestAuditStateStepTakeoverDeclineThenAccept(t *testing.T) {
	stateTakeoverFixture(t)
	r := runAuditStep(context.Background())
	if r.ConfirmNext == "" || r.Commit == nil {
		t.Fatalf("the takeover should gate: %+v", r)
	}
	// Declined: the commit never runs and the finding stays listed.
	s := readState(t)
	wantHigh(t, s, "maintainer bob → mallory")
	if s.High[0].Pkg != "victim" || s.Packages != 2 || !s.SnapshotPresent || !s.SnapshotReadable || s.V != 1 {
		t.Fatalf("state = %+v", s)
	}
	// A second audit that is again declined still lists it.
	runAuditStep(context.Background())
	wantHigh(t, readState(t), "maintainer bob → mallory")
	// Accepted: cleared.
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	wantHigh(t, readState(t))
	if fi, _ := os.Stat(statePath()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
}

func TestAuditStateMenuAuditAcceptWithA(t *testing.T) {
	stateTakeoverFixture(t)
	msg := runAudit(auditNow)
	wantHigh(t, readState(t), "maintainer bob → mallory") // held until accepted

	m := initialModel()
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.screen = screenDiscovering
	m = update(m, msg)
	if back := update(m, key("q")); back.auditCommit != nil { // leaving keeps it listed
		t.Fatal("leaving must drop the accept")
	}
	wantHigh(t, readState(t), "maintainer bob → mallory")

	update(m, key("a"))
	wantHigh(t, readState(t))
}

func TestAuditStateDryRunLeavesTheRealFileAlone(t *testing.T) {
	stateTakeoverFixture(t)
	runAudit(auditNow)
	before, _ := os.ReadFile(statePath())
	dryRun = true
	defer func() { dryRun = false }()
	runAudit(auditNow)
	runAuditStep(context.Background())
	after, _ := os.ReadFile(statePath())
	if string(before) != string(after) {
		t.Fatalf("a dry run touched audit-state.json:\n%s\n--\n%s", before, after)
	}
	// And a dry run with no file creates none.
	os.Remove(statePath())
	runAudit(auditNow)
	if _, err := os.Stat(statePath()); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create audit-state.json")
	}
}

func TestAuditStateCorruptSnapshotListed(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "steady\n")
	fakeAUR(t, map[string]aurInfo{"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10}})
	os.WriteFile(baselinePath(), []byte("{garbage"), 0o600)
	runAuditStep(context.Background())
	s := readState(t)
	wantHigh(t, s, "snapshot unreadable")
	if !s.SnapshotPresent || s.SnapshotReadable {
		t.Fatalf("state = %+v", s)
	}
}

func TestAuditStateUnreachableAURKeepsTheFile(t *testing.T) {
	stateTakeoverFixture(t)
	runAudit(auditNow)
	before, _ := os.ReadFile(statePath())
	old := aurRPCURL
	aurRPCURL = "http://127.0.0.1:1/"
	defer func() { aurRPCURL = old }()
	if msg := runAudit(auditNow); msg.notice == "" {
		t.Fatal("expected a notice")
	}
	runAuditStep(context.Background())
	if after, _ := os.ReadFile(statePath()); string(before) != string(after) {
		t.Fatalf("an unreachable AUR changed the state:\n%s", after)
	}
	if filepath.Dir(statePath()) != filepath.Dir(baselinePath()) {
		t.Fatal("the state belongs next to the snapshot")
	}
}
