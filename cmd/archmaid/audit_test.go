package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The audit itself (classification, the RPC, the snapshot format and its
// save, cancellation, the prefetch) is tested in internal/audit. These tests
// drive it through the TUI: the menu action, the step that gates topgrade,
// the report screen, and the settings the glue hands it.

// ── helpers ───────────────────────────────────────────────────────────────────

type (
	aurInfo   = audit.Info
	snapEntry = audit.Entry
)

func strp(s string) *string { return &s }

// baselinePath is the snapshot the current settings point at.
func baselinePath() string { return currentAuditConfig().SnapshotPath }

// loadBaseline reads that snapshot.
func loadBaseline() (map[string]snapEntry, audit.Baseline) {
	return audit.LoadSnapshot(baselinePath())
}

// saveBaseline seeds that snapshot: each package's entry for what info
// reports, else its entry in prev.
func saveBaseline(pkgs []string, info map[string]aurInfo, prev map[string]snapEntry) error {
	next := map[string]snapEntry{}
	for _, n := range pkgs {
		if ai, ok := info[n]; ok {
			next[n] = audit.EntryFor(ai)
		} else if p, ok := prev[n]; ok {
			next[n] = p
		}
	}
	return audit.WriteSnapshot(baselinePath(), next)
}

// runAudit is the menu action's audit against the current settings.
func runAudit(now time.Time) discoveryMsg { return menuAudit(now, currentAuditConfig()) }

// runAuditStep is the audit step's run against the current settings.
func runAuditStep(ctx context.Context) steps.Result { return auditStep().Run(ctx, steps.Env{}) }

// shimPacmanForeign installs a fake `pacman` that answers `-Qqm` with out.
func shimPacmanForeign(t *testing.T, out string) {
	t.Helper()
	dir := t.TempDir()
	payload := strings.ReplaceAll(out, "\n", "\\n")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"-Qqm\" ]; then printf '%s'; fi\n", payload)
	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// fakeAUR serves the RPC "info" endpoint from db (name → record), counting
// requests and recording the arg[] batches it was asked for. Pointing
// aurRPCURL at it keeps every audit test offline.
func fakeAUR(t *testing.T, db map[string]aurInfo) (requests *atomic.Int32, batches *[][]string) {
	t.Helper()
	var n atomic.Int32
	var seen [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST (chunked lookups must not depend on URL length)", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		args := r.PostForm["arg[]"]
		seen = append(seen, args)
		var results []aurInfo
		for _, a := range args {
			if rec, ok := db[a]; ok {
				results = append(results, rec)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "multiinfo", "resultcount": len(results), "results": results})
	}))
	t.Cleanup(srv.Close)
	old := aurRPCURL
	aurRPCURL = srv.URL
	t.Cleanup(func() { aurRPCURL = old })
	return &n, &seen
}

// auditEnv gives a test a private log dir (snapshot + audit log) and HOME.
func auditEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	old := logDir
	logDir = t.TempDir()
	t.Cleanup(func() { logDir = old })
}

var auditNow = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

func aged(h int) int64 { return auditNow.Add(-time.Duration(h) * time.Hour).Unix() }

// serveAUR points aurRPCURL at a one-off handler for the test's duration.
func serveAUR(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := aurRPCURL
	aurRPCURL = srv.URL
	t.Cleanup(func() { aurRPCURL = old })
}

// TestAuditReportCarriesNoAURControlBytes: a maintainer name with terminal
// escapes reaches neither the report, the confirm, nor audit.log intact,
// while the raw value still drives the comparison and the snapshot.
func TestAuditReportCarriesNoAURControlBytes(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "victim\n")
	evil := "mallory\x1b]52;c;cGF5bG9hZA==\x07"
	fakeAUR(t, map[string]aurInfo{"victim": {Name: "victim", Maintainer: strp(evil), LastModified: aged(24 * 90), NumVotes: 10}})
	saveBaseline([]string{"victim"}, map[string]aurInfo{"victim": {Name: "victim", Maintainer: strp("bob")}}, nil)

	r := runAuditStep(context.Background())
	if r.ConfirmNext == "" || !strings.Contains(r.Output, "bob → mallory\n") {
		t.Fatalf("confirm=%q output:\n%s", r.ConfirmNext, r.Output)
	}
	for name, s := range map[string]string{"output": r.Output, "confirm": r.ConfirmNext, "report": runAudit(auditNow).report} {
		if strings.ContainsAny(s, "\x07") || strings.Contains(s, "]52;c;cGF5bG9hZA==\x07") || strings.Contains(s, "\x1b]") {
			t.Errorf("%s still carries the escape: %q", name, s)
		}
	}
	if log, _ := os.ReadFile(logPath(logCategoryAudit)); strings.Contains(string(log), "\x1b]") {
		t.Errorf("audit.log carries the escape: %q", log)
	}
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	if snap, _ := loadBaseline(); snap["victim"].Maintainer != evil {
		t.Fatalf("the snapshot keeps the raw value for the next comparison, got %q", snap["victim"].Maintainer)
	}
}

// ── snapshot ──────────────────────────────────────────────────────────────────

// TestCorruptSnapshotFailsClosed: an unreadable snapshot is a high finding
// that gates the update. Neither the audit nor the menu's partial save touch
// it; accepting replaces it and keeps the bad file aside, not overwritten.
func TestCorruptSnapshotFailsClosed(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "steady\n")
	fakeAUR(t, map[string]aurInfo{"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10}})
	os.WriteFile(baselinePath(), []byte("{garbage"), 0o600)

	r := runAuditStep(context.Background())
	if r.Err != nil || !strings.Contains(r.ConfirmNext, "snapshot is unreadable") {
		t.Fatalf("err=%v confirmNext=%q, want the unreadable-snapshot gate", r.Err, r.ConfirmNext)
	}
	if !strings.Contains(r.Output, "[HIGH] maintainer snapshot") || !strings.Contains(r.Output, "1 high, 0 glance, 1 clean") {
		t.Fatalf("output:\n%s", r.Output)
	}
	if menu := runAudit(auditNow); menu.commit == nil {
		t.Fatal("the menu audit should offer `a` to replace the corrupt snapshot")
	}
	if b, _ := os.ReadFile(baselinePath()); string(b) != "{garbage" {
		t.Fatalf("an unanswered audit must leave the corrupt snapshot alone, got %q", b)
	}

	if err := r.Commit(); err != nil { // the gate is accepted
		t.Fatal(err)
	}
	if got, state := loadBaseline(); state != audit.BaselineOK || got["steady"].Maintainer != "alice" {
		t.Fatalf("accepting should save a fresh baseline, state=%d snapshot=%v", state, got)
	}
	aside, _ := filepath.Glob(baselinePath() + ".corrupt-*")
	if len(aside) != 1 {
		t.Fatalf("corrupt snapshot should be kept aside, found %v", aside)
	}
	if b, _ := os.ReadFile(aside[0]); string(b) != "{garbage" {
		t.Fatalf("set-aside file = %q", b)
	}
}

// ── the action, end to end ────────────────────────────────────────────────────

func TestRunAuditFirstThenTakeover(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "steady\nvictim\n")
	db := map[string]aurInfo{
		"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10},
		"victim": {Name: "victim", Maintainer: strp("bob"), LastModified: aged(24 * 90), NumVotes: 10},
	}
	fakeAUR(t, db)

	// First audit: everything clean, baseline saved, says so.
	first := runAudit(auditNow)
	if first.notice != "" {
		t.Fatalf("unexpected notice: %q", first.notice)
	}
	if !strings.Contains(first.report, "First audit") || !strings.Contains(first.report, "Nothing flagged") {
		t.Fatalf("first report:\n%s", first.report)
	}
	if _, state := loadBaseline(); state != audit.BaselineOK {
		t.Fatal("first audit must save the baseline")
	}

	// The maintainer of "victim" changes and it gets pushed to.
	db["victim"] = aurInfo{Name: "victim", Maintainer: strp("mallory"), LastModified: aged(1), NumVotes: 10}
	second := runAudit(auditNow)
	if !strings.Contains(second.report, "maintainer changed: bob → mallory") {
		t.Fatalf("takeover not flagged:\n%s", second.report)
	}
	if !strings.Contains(second.report, "1 look closely") {
		t.Fatalf("summary should count the high-severity finding:\n%s", second.report)
	}
	if strings.Contains(second.report, "steady ") {
		t.Fatalf("the clean package must not be listed:\n%s", second.report)
	}

	// Both audits are in the audit log; the takeover is marked HIGH.
	data, err := os.ReadFile(logPath(logCategoryAudit))
	if err != nil {
		t.Fatalf("audit log not written: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "AUR audit: 2 checked, 0 flagged") ||
		!strings.Contains(log, "AUR audit: 2 checked, 1 flagged") ||
		!strings.Contains(log, "[HIGH] victim") {
		t.Fatalf("audit log:\n%s", log)
	}
}

func TestRunAuditDryRunDoesNotAdvanceSnapshot(t *testing.T) {
	auditEnv(t)
	dryRun = true
	defer func() { dryRun = false }()
	shimPacmanForeign(t, "steady\n")
	fakeAUR(t, map[string]aurInfo{"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 3}})

	res := runAudit(auditNow)
	if _, err := os.Stat(baselinePath()); !os.IsNotExist(err) {
		t.Fatal("a dry run must not write the maintainer snapshot")
	}
	if !strings.Contains(res.report, "dry run") {
		t.Fatalf("report should say the snapshot wasn't saved:\n%s", res.report)
	}
}

func TestRunAuditNotices(t *testing.T) {
	auditEnv(t)

	shimPacmanForeign(t, "")
	if n := runAudit(auditNow).notice; !strings.Contains(n, "No foreign") {
		t.Fatalf("empty foreign list notice = %q", n)
	}

	shimPacmanForeign(t, "something\n")
	old := aurRPCURL
	aurRPCURL = "http://127.0.0.1:1/rpc" // nothing listens here
	defer func() { aurRPCURL = old }()
	n := runAudit(auditNow).notice
	if !strings.Contains(n, "Couldn't reach the AUR") || len(n) > 120 {
		t.Fatalf("unreachable notice = %q (must be short enough for a popup)", n)
	}
	if _, err := os.Stat(baselinePath()); !os.IsNotExist(err) {
		t.Fatal("a failed audit must not touch the snapshot")
	}
}

// TestMenuAuditHoldsHighFindingsUntilAccepted: the menu audit records the
// unflagged and glance-level maintainers right away, but keeps a maintainer
// change out of the snapshot until `a` accepts it. Leaving with q keeps it
// flagged.
func TestMenuAuditHoldsHighFindingsUntilAccepted(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "fresh\nsteady\nvictim\n")
	fakeAUR(t, map[string]aurInfo{
		"fresh":  {Name: "fresh", Maintainer: strp("carol"), LastModified: aged(24 * 90), NumVotes: 10},
		"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10},
		"victim": {Name: "victim", Maintainer: strp("mallory"), LastModified: aged(24 * 90), NumVotes: 10},
	})
	saveBaseline([]string{"steady", "victim"}, map[string]aurInfo{
		"steady": {Name: "steady", Maintainer: strp("alice")},
		"victim": {Name: "victim", Maintainer: strp("bob")},
	}, nil)

	msg := runAudit(auditNow)
	if msg.commit == nil || !strings.Contains(msg.report, "press a to accept") {
		t.Fatalf("a high finding should be acceptable with a, commit=%v report:\n%s", msg.commit != nil, msg.report)
	}
	if snap, _ := loadBaseline(); snap["victim"].Maintainer != "bob" || snap["fresh"].Maintainer != "carol" {
		t.Fatalf("snapshot = %v, want victim held at bob and fresh recorded", snap)
	}

	m := initialModel()
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.screen = screenDiscovering
	m = update(m, msg)
	if !strings.Contains(m.View(), "a accept maintainer changes") {
		t.Fatalf("audit screen should hint at a:\n%s", m.View())
	}
	if back := update(m, key("q")); back.auditCommit != nil {
		t.Fatal("leaving the report must drop the pending accept")
	}
	if again := runAudit(auditNow); !strings.Contains(again.report, "bob → mallory") {
		t.Fatalf("an unaccepted change must be flagged again:\n%s", again.report)
	}

	got := update(m, key("a"))
	if got.screen != screenNotice || got.bg != screenAudit || !strings.Contains(got.notice, "accepted") || got.auditCommit != nil {
		t.Fatalf("screen=%d bg=%d notice=%q commit=%v", got.screen, got.bg, got.notice, got.auditCommit != nil)
	}
	if snap, _ := loadBaseline(); snap["victim"].Maintainer != "mallory" {
		t.Fatalf("snapshot = %v, want victim accepted as mallory", snap)
	}
	if log, _ := os.ReadFile(logPath(logCategoryAudit)); !strings.Contains(string(log), "ACCEPT: AUR maintainer changes accepted") {
		t.Fatalf("the accept should be on record:\n%s", log)
	}
	third := runAudit(auditNow)
	if third.commit != nil || strings.Contains(third.report, "maintainer changed") {
		t.Fatalf("an accepted change must not be flagged again:\n%s", third.report)
	}
	if v := update(m, discoveryMsg{kind: "audit", report: third.report}).View(); strings.Contains(v, "a accept") {
		t.Fatalf("no a hint without anything to accept:\n%s", v)
	}
}

// ── co-maintainers ────────────────────────────────────────────────────────────

// coFixture: victim gained mallory as a co-maintainer and steady lost carol,
// both against a snapshot that recorded co-maintainers.
func coFixture(t *testing.T) {
	t.Helper()
	auditEnv(t)
	shimPacmanForeign(t, "steady\nvictim\n")
	fakeAUR(t, map[string]aurInfo{
		"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10},
		"victim": {Name: "victim", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10, CoMaintainers: []string{"mallory", "bob"}},
	})
	saveBaseline([]string{"steady", "victim"}, map[string]aurInfo{
		"steady": {Name: "steady", Maintainer: strp("alice"), CoMaintainers: []string{"carol"}},
		"victim": {Name: "victim", Maintainer: strp("alice"), CoMaintainers: []string{"bob"}},
	}, nil)
}

func coOf(t *testing.T, pkg string) []string {
	t.Helper()
	snap, state := loadBaseline()
	if state != audit.BaselineOK || !snap[pkg].HasCo {
		t.Fatalf("state = %d, %s = %+v, want co-maintainers recorded", state, pkg, snap[pkg])
	}
	return snap[pkg].CoMaintainers
}

// TestCoMaintainerAddedGatesUpdate: an added co-maintainer gates topgrade by
// name, a removed one is only a glance. The step saves nothing itself; only
// the commit (run on "yes") records the new co-maintainer.
func TestCoMaintainerAddedGatesUpdate(t *testing.T) {
	coFixture(t)
	r := runAuditStep(context.Background())
	if r.ConfirmNext != "Since the last audit: victim: co-maintainer mallory added. Update anyway?" {
		t.Fatalf("confirmNext = %q", r.ConfirmNext)
	}
	for _, want := range []string{"[HIGH] victim  co-maintainer mallory added", "[warn] steady  co-maintainer carol removed", "1 high, 1 glance, 0 clean"} {
		if !strings.Contains(r.Output, want) {
			t.Fatalf("output lacks %q:\n%s", want, r.Output)
		}
	}
	if co := coOf(t, "victim"); !reflect.DeepEqual(co, []string{"bob"}) {
		t.Fatalf("the audit alone must not record the change, victim co = %q", co)
	}
	if again := runAuditStep(context.Background()); again.ConfirmNext == "" {
		t.Fatal("an unanswered gate must gate again")
	}
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	if co := coOf(t, "victim"); !reflect.DeepEqual(co, []string{"bob", "mallory"}) {
		t.Fatalf("accepting should record the co-maintainers, victim co = %q", co)
	}
	if after := runAuditStep(context.Background()); after.ConfirmNext != "" {
		t.Fatalf("an accepted co-maintainer must not gate again: %q", after.ConfirmNext)
	}
}

// TestOldFormatSnapshotIsACoMaintainerBaseline: after upgrading from a
// version that only recorded maintainers, co-maintainers are a baseline (no
// gate, like a first audit) and get recorded by the commit; from then on an
// added one gates. The old maintainers are still compared.
func TestOldFormatSnapshotIsACoMaintainerBaseline(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "steady\nvictim\n")
	db := map[string]aurInfo{
		"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10, CoMaintainers: []string{"carol"}},
		"victim": {Name: "victim", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10, CoMaintainers: []string{"bob"}},
	}
	fakeAUR(t, db)
	os.WriteFile(baselinePath(), []byte(`{"steady": "alice", "victim": "alice"}`), 0o600)

	r := runAuditStep(context.Background())
	if r.Err != nil || r.ConfirmNext != "" || !strings.Contains(r.Output, "0 high, 0 glance, 2 clean") {
		t.Fatalf("err=%v confirmNext=%q, want no gate:\n%s", r.Err, r.ConfirmNext, r.Output)
	}
	if err := r.Commit(); err != nil { // nothing gates: handleStepResult commits at once
		t.Fatal(err)
	}
	if co := coOf(t, "victim"); !reflect.DeepEqual(co, []string{"bob"}) {
		t.Fatalf("victim co = %q, want it recorded", co)
	}

	db["victim"] = aurInfo{Name: "victim", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10, CoMaintainers: []string{"bob", "mallory"}}
	if r := runAuditStep(context.Background()); !strings.Contains(r.ConfirmNext, "victim: co-maintainer mallory added") {
		t.Fatalf("confirmNext = %q, want the added co-maintainer to gate once recorded", r.ConfirmNext)
	}

	t.Run("maintainer change in an old-format snapshot still gates", func(t *testing.T) {
		os.WriteFile(baselinePath(), []byte(`{"steady": "alice", "victim": "bob"}`), 0o600)
		if r := runAuditStep(context.Background()); r.ConfirmNext != "victim changed maintainer since the last audit. Update anyway?" {
			t.Fatalf("confirmNext = %q", r.ConfirmNext)
		}
	})
	t.Run("the menu audit records them right away", func(t *testing.T) {
		os.WriteFile(baselinePath(), []byte(`{"steady": "alice", "victim": "alice"}`), 0o600)
		if msg := runAudit(auditNow); msg.commit != nil {
			t.Fatalf("nothing should be high:\n%s", msg.report)
		}
		if co := coOf(t, "steady"); !reflect.DeepEqual(co, []string{"carol"}) {
			t.Fatalf("steady co = %q", co)
		}
	})
}

// TestMenuAuditHoldsCoMaintainerUntilAccepted: like a maintainer change, an
// added co-maintainer stays out of the snapshot until `a`, while the glance
// (a removed co-maintainer) is recorded at once.
func TestMenuAuditHoldsCoMaintainerUntilAccepted(t *testing.T) {
	coFixture(t)
	msg := runAudit(auditNow)
	if msg.commit == nil || !strings.Contains(msg.report, "co-maintainer mallory added") {
		t.Fatalf("commit=%v report:\n%s", msg.commit != nil, msg.report)
	}
	if co := coOf(t, "victim"); !reflect.DeepEqual(co, []string{"bob"}) {
		t.Fatalf("victim co = %q, want it held", co)
	}
	if co := coOf(t, "steady"); len(co) != 0 {
		t.Fatalf("steady co = %q, want the removal recorded", co)
	}
	if again := runAudit(auditNow); again.commit == nil {
		t.Fatal("an unaccepted co-maintainer must be flagged again")
	}

	m := initialModel()
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.screen = screenDiscovering
	m = update(m, msg)
	if got := update(m, key("a")); !strings.Contains(got.notice, "accepted") {
		t.Fatalf("notice = %q", got.notice)
	}
	if co := coOf(t, "victim"); !reflect.DeepEqual(co, []string{"bob", "mallory"}) {
		t.Fatalf("victim co = %q, want mallory accepted", co)
	}
	if third := runAudit(auditNow); third.commit != nil {
		t.Fatalf("an accepted co-maintainer must not be flagged again:\n%s", third.report)
	}
}

// ── UI flow ───────────────────────────────────────────────────────────────────

func TestAuditMenuItemStartsDiscovery(t *testing.T) {
	m := initialModel()
	for i, it := range menu {
		if it.id == "auraudit" {
			m.cursor = i
		}
	}
	if menu[m.cursor].id != "auraudit" {
		t.Fatal("menu has no audit item")
	}
	got := update(m, key("enter"))
	if got.screen != screenDiscovering || !strings.Contains(got.discovering, "AUR") {
		t.Fatalf("screen=%d discovering=%q", got.screen, got.discovering)
	}
}

func TestAuditReportShowsScrollableScreenAndEscReturns(t *testing.T) {
	m := initialModel()
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.screen = screenDiscovering

	got := update(m, discoveryMsg{kind: "audit", report: "line one\nline two"})
	if got.screen != screenAudit {
		t.Fatalf("screen = %d, want screenAudit", got.screen)
	}
	if !strings.Contains(got.View(), "line two") || !strings.Contains(got.View(), "AUR audit") {
		t.Fatalf("audit screen doesn't render the report:\n%s", got.View())
	}
	if back := update(got, key("esc")); back.screen != screenMenu {
		t.Fatalf("esc = screen %d, want screenMenu", back.screen)
	}
	if back := update(got, key("q")); back.screen != screenMenu {
		t.Fatalf("q = screen %d, want screenMenu", back.screen)
	}
}

func TestAuditNoticeRoutesToNoticePopup(t *testing.T) {
	m := initialModel()
	got := update(m, discoveryMsg{kind: "audit", notice: "Couldn't reach the AUR: boom"})
	if got.screen != screenNotice || !strings.Contains(got.notice, "boom") {
		t.Fatalf("screen=%d notice=%q", got.screen, got.notice)
	}
}

func TestAuditLogIsInTheViewLogPicker(t *testing.T) {
	last := logCategoryLabels[len(logCategoryLabels)-1]
	if last.ID != logCategoryAudit {
		t.Fatalf("last picker entry = %+v, want the audit category", last)
	}
	if categoryLabel(logCategoryAudit) != "AUR audit" {
		t.Fatalf("label = %q", categoryLabel(logCategoryAudit))
	}
}
