package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func strp(s string) *string { return &s }

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

// ── classify ──────────────────────────────────────────────────────────────────

func TestClassify(t *testing.T) {
	old := aged(24 * 30)
	cases := []struct {
		name     string
		ai       *aurInfo
		prev     string
		hadPrev  bool
		wantHigh bool
		want     []string // substrings, one per expected flag, in order
	}{
		{"clean", &aurInfo{Maintainer: strp("alice"), LastModified: old, NumVotes: 5}, "alice", true, false, nil},
		{"unknown to the AUR", nil, "", false, false, []string{"not on the AUR"}},
		{"adopted after being orphaned", &aurInfo{Maintainer: strp("mallory"), LastModified: old, NumVotes: 5}, "", true, true, []string{"adopted by mallory (was orphaned)"}},
		{"maintainer changed", &aurInfo{Maintainer: strp("bob"), LastModified: old, NumVotes: 5}, "alice", true, true, []string{"maintainer changed: alice → bob"}},
		{"newly orphaned", &aurInfo{LastModified: old, NumVotes: 5}, "alice", true, false, []string{"orphaned (was alice)"}},
		{"orphaned, no history", &aurInfo{LastModified: old, NumVotes: 5}, "", false, false, []string{"orphaned"}},
		{"no baseline never flags a change", &aurInfo{Maintainer: strp("bob"), LastModified: old, NumVotes: 5}, "alice", false, false, nil},
		{"recent push", &aurInfo{Maintainer: strp("alice"), LastModified: aged(5), NumVotes: 5}, "alice", true, false, []string{"modified 5h ago"}},
		{"just past the window is quiet", &aurInfo{Maintainer: strp("alice"), LastModified: aged(73), NumVotes: 5}, "alice", true, false, nil},
		{"out of date", &aurInfo{Maintainer: strp("alice"), LastModified: old, NumVotes: 5, OutOfDate: new(int64)}, "alice", true, false, []string{"flagged out of date"}},
		{"no votes", &aurInfo{Maintainer: strp("alice"), LastModified: old, NumVotes: 0}, "alice", true, false, []string{"0 votes"}},
		{"takeover shape: adopted and pushed", &aurInfo{Maintainer: strp("mallory"), LastModified: aged(2), NumVotes: 0}, "", true, true,
			[]string{"adopted by mallory", "modified 2h ago", "0 votes"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify("pkg", c.ai, c.prev, c.hadPrev, auditNow)
			if got.high != c.wantHigh {
				t.Errorf("high = %v, want %v (flags %v)", got.high, c.wantHigh, got.flags)
			}
			if len(got.flags) != len(c.want) {
				t.Fatalf("flags = %v, want %d matching %v", got.flags, len(c.want), c.want)
			}
			for i, w := range c.want {
				if !strings.Contains(got.flags[i], w) {
					t.Errorf("flag[%d] = %q, want it to contain %q", i, got.flags[i], w)
				}
			}
		})
	}
}

func TestClassifyAllSkipsUnknownDebugCompanionAndSortsHighFirst(t *testing.T) {
	// yay-debug: a locally built split package the AUR has never heard of —
	// skipped even though its base (yay) isn't foreign here. foo-debug *is* on
	// the AUR, so it is audited like any other package.
	pkgs := []string{"aaa", "yay-debug", "foo-debug", "zzz"}
	info := map[string]aurInfo{
		"aaa":       {Name: "aaa", LastModified: aged(1000), NumVotes: 0, Maintainer: strp("a")},       // glance (0 votes)
		"foo-debug": {Name: "foo-debug", LastModified: aged(1000), NumVotes: 0, Maintainer: strp("f")}, // glance (0 votes)
		"zzz":       {Name: "zzz", LastModified: aged(1000), NumVotes: 9, Maintainer: strp("new")},     // high (changed)
	}
	prev := map[string]string{"aaa": "a", "foo-debug": "f", "zzz": "old"}
	got := classifyAll(pkgs, info, prev, true, auditNow)

	var names []string
	for _, f := range got {
		names = append(names, f.name)
	}
	want := []string{"zzz", "aaa", "foo-debug"} // high first; yay-debug absent
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("findings = %v, want %v", names, want)
	}
}

// ── RPC ───────────────────────────────────────────────────────────────────────

func TestFetchAURInfoChunksAndPosts(t *testing.T) {
	db := map[string]aurInfo{}
	var pkgs []string
	for i := 0; i < auditChunk*2+5; i++ {
		n := fmt.Sprintf("pkg%03d", i)
		pkgs = append(pkgs, n)
		if i%2 == 0 { // odd ones are unknown to the AUR
			db[n] = aurInfo{Name: n, NumVotes: i}
		}
	}
	reqs, batches := fakeAUR(t, db)

	got, err := fetchAURInfo(pkgs)
	if err != nil {
		t.Fatal(err)
	}
	if reqs.Load() != 3 {
		t.Fatalf("requests = %d, want 3 chunks for %d packages", reqs.Load(), len(pkgs))
	}
	if len((*batches)[0]) != auditChunk || len((*batches)[2]) != 5 {
		t.Fatalf("chunk sizes = %d/%d/%d", len((*batches)[0]), len((*batches)[1]), len((*batches)[2]))
	}
	if len(got) != len(db) {
		t.Fatalf("found %d, want %d (unknown packages must simply be absent)", len(got), len(db))
	}
	if _, ok := got["pkg001"]; ok {
		t.Fatal("unknown package leaked into the result")
	}
}

func TestFetchAURInfoErrors(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"http error", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", 503) }, "HTTP 503"},
		{"rpc error", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"type":"error","error":"Too many requests."}`))
		}, "Too many requests."},
		{"garbage", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html>`)) }, "unreadable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			defer srv.Close()
			old := aurRPCURL
			aurRPCURL = srv.URL
			defer func() { aurRPCURL = old }()

			_, err := fetchAURInfo([]string{"x"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

// ── snapshot ──────────────────────────────────────────────────────────────────

func TestBaselineRoundTripAndKeepsMissing(t *testing.T) {
	auditEnv(t)
	if _, ok := loadBaseline(); ok {
		t.Fatal("no snapshot should exist yet")
	}
	prev := map[string]string{"gone": "carol", "kept": "old"}
	info := map[string]aurInfo{"kept": {Name: "kept", Maintainer: strp("new")}, "orph": {Name: "orph"}}
	saveBaseline([]string{"kept", "orph", "gone"}, info, prev)

	got, ok := loadBaseline()
	if !ok {
		t.Fatal("snapshot not saved")
	}
	want := map[string]string{"kept": "new", "orph": "", "gone": "carol"} // absent-from-AUR keeps its old value
	for k, v := range want {
		if got[k] != v {
			t.Errorf("baseline[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestBaselineCorruptFileIsTreatedAsMissing(t *testing.T) {
	auditEnv(t)
	os.WriteFile(baselinePath(), []byte("{not json"), 0o644)
	if _, ok := loadBaseline(); ok {
		t.Fatal("a corrupt snapshot must read as 'no baseline', not as data")
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
	if _, ok := loadBaseline(); !ok {
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
	if last.id != logCategoryAudit {
		t.Fatalf("last picker entry = %+v, want the audit category", last)
	}
	if categoryLabel(logCategoryAudit) != "AUR audit" {
		t.Fatalf("label = %q", categoryLabel(logCategoryAudit))
	}
}
