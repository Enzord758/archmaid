package main

import (
	"encoding/json"
	"errors"
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

// serveAUR points aurRPCURL at a one-off handler for the test's duration.
func serveAUR(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := aurRPCURL
	aurRPCURL = srv.URL
	t.Cleanup(func() { aurRPCURL = old })
}

func TestFetchAURInfoCapsTheResponse(t *testing.T) {
	serveAUR(t, func(w http.ResponseWriter, r *http.Request) {
		// Valid JSON, but one string field alone is past the cap.
		w.Write([]byte(`{"type":"multiinfo","results":[{"Name":"x","Description":"`))
		w.Write([]byte(strings.Repeat("a", aurMaxResponse)))
		w.Write([]byte(`"}]}`))
	})
	if _, err := fetchAURInfo([]string{"x"}); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("err = %v, want an oversized response to be rejected", err)
	}
}

func TestFetchAURInfoDropsUnrequestedNames(t *testing.T) {
	serveAUR(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"type":"multiinfo","results":[{"Name":"x","NumVotes":1},{"Name":"evil","NumVotes":1}]}`))
	})
	got, err := fetchAURInfo([]string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["evil"]; ok || len(got) != 1 {
		t.Fatalf("got %v, want only the requested package", got)
	}
}

func TestAURField(t *testing.T) {
	cases := map[string]string{
		"alice":                             "alice",
		"mal\x1b]0;pwned\x07lory":           "mallory", // OSC title set: the whole sequence dropped
		"bob\x1b[2J\r\n":                    "bob",
		"mallory\ralice":                    "malloryalice", // a CR can't hide the head
		"tab\tnew\nline":                    "tabnewline",
		"‮yrollam":                          "yrollam", // bidi override
		"zero​width":                        "zerowidth",
		"c1\u009bcsi":                       "c1csi",
		"bad\xffutf8":                       "bad�utf8",
		"ålæsø":                             "ålæsø",
		strings.Repeat("x", 100):            strings.Repeat("x", aurFieldMax) + "…",
		strings.Repeat("x", 64):             strings.Repeat("x", 64),
		"\x1b[1m" + strings.Repeat("y", 64): strings.Repeat("y", 64), // escapes don't count toward the cap
	}
	for in, want := range cases {
		if got := aurField(in); got != want {
			t.Errorf("aurField(%q) = %q, want %q", in, got, want)
		}
	}
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

	r := runAuditStep()
	if r.confirmNext == "" || !strings.Contains(r.output, "bob → mallory\n") {
		t.Fatalf("confirm=%q output:\n%s", r.confirmNext, r.output)
	}
	for name, s := range map[string]string{"output": r.output, "confirm": r.confirmNext, "report": runAudit(auditNow).report} {
		if strings.ContainsAny(s, "\x07") || strings.Contains(s, "]52;c;cGF5bG9hZA==\x07") || strings.Contains(s, "\x1b]") {
			t.Errorf("%s still carries the escape: %q", name, s)
		}
	}
	if log, _ := os.ReadFile(logPath(logCategoryAudit)); strings.Contains(string(log), "\x1b]") {
		t.Errorf("audit.log carries the escape: %q", log)
	}
	if err := r.commit(); err != nil {
		t.Fatal(err)
	}
	if snap, _ := loadBaseline(); snap["victim"] != evil {
		t.Fatalf("the snapshot keeps the raw value for the next comparison, got %q", snap["victim"])
	}
}

// ── snapshot ──────────────────────────────────────────────────────────────────

func TestBaselineRoundTripAndKeepsMissing(t *testing.T) {
	auditEnv(t)
	if _, state := loadBaseline(); state != baselineMissing {
		t.Fatalf("no snapshot should exist yet, state = %d", state)
	}
	prev := map[string]string{"gone": "carol", "kept": "old"}
	info := map[string]aurInfo{"kept": {Name: "kept", Maintainer: strp("new")}, "orph": {Name: "orph"}}
	if err := saveBaseline([]string{"kept", "orph", "gone"}, info, prev); err != nil {
		t.Fatal(err)
	}

	got, state := loadBaseline()
	if state != baselineOK {
		t.Fatalf("snapshot not saved, state = %d", state)
	}
	want := map[string]string{"kept": "new", "orph": "", "gone": "carol"} // absent-from-AUR keeps its old value
	for k, v := range want {
		if got[k] != v {
			t.Errorf("baseline[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// TestBaselineCorruptFileIsNotMissing: an unreadable snapshot must never read
// as "first audit" — that would silently re-baseline over a takeover.
func TestBaselineCorruptFileIsNotMissing(t *testing.T) {
	for name, content := range map[string]string{"invalid JSON": "{not json", "JSON null": "null", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			auditEnv(t)
			os.WriteFile(baselinePath(), []byte(content), 0o600)
			if m, state := loadBaseline(); state != baselineCorrupt || m != nil {
				t.Fatalf("state = %d, map = %v, want baselineCorrupt and no data", state, m)
			}
		})
	}
	t.Run("unreadable", func(t *testing.T) {
		auditEnv(t)
		os.Mkdir(baselinePath(), 0o700) // reading a directory fails with EISDIR, not ENOENT
		if _, state := loadBaseline(); state != baselineCorrupt {
			t.Fatalf("state = %d, want baselineCorrupt", state)
		}
	})
}

// TestSaveBaselineIsAtomicAndPrivate: the snapshot is 0600 and no temp file
// is left behind.
func TestSaveBaselineIsAtomicAndPrivate(t *testing.T) {
	auditEnv(t)
	for _, m := range []string{"alice", "bob"} { // create, then replace
		if err := saveBaseline([]string{"p"}, map[string]aurInfo{"p": {Name: "p", Maintainer: strp(m)}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(baselinePath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %v, want 0600", fi.Mode().Perm())
	}
	if got, _ := loadBaseline(); got["p"] != "bob" {
		t.Fatalf("snapshot = %v, want the second save", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(baselinePath()))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// TestSaveBaselineSkipsWhenLocked: while another process holds the lock, a
// save is skipped (errSnapshotLocked) and the snapshot is left alone.
func TestSaveBaselineSkipsWhenLocked(t *testing.T) {
	auditEnv(t)
	saveBaseline([]string{"p"}, map[string]aurInfo{"p": {Name: "p", Maintainer: strp("alice")}}, nil)

	unlock, err := lockBaseline(filepath.Dir(baselinePath())) // a separate fd: flock conflicts in-process too
	if err != nil {
		t.Fatal(err)
	}
	err = saveBaseline([]string{"p"}, map[string]aurInfo{"p": {Name: "p", Maintainer: strp("mallory")}}, nil)
	if !errors.Is(err, errSnapshotLocked) {
		t.Fatalf("err = %v, want errSnapshotLocked", err)
	}
	if got, _ := loadBaseline(); got["p"] != "alice" {
		t.Fatalf("a locked save must not write, snapshot = %v", got)
	}
	unlock()
	if err := saveBaseline([]string{"p"}, map[string]aurInfo{"p": {Name: "p", Maintainer: strp("bob")}}, nil); err != nil {
		t.Fatalf("save after unlock: %v", err)
	}
}

// TestCorruptSnapshotFailsClosed: an unreadable snapshot is a high finding
// that gates the update. Neither the audit nor the menu's partial save touch
// it; accepting replaces it and keeps the bad file aside, not overwritten.
func TestCorruptSnapshotFailsClosed(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "steady\n")
	fakeAUR(t, map[string]aurInfo{"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10}})
	os.WriteFile(baselinePath(), []byte("{garbage"), 0o600)

	r := runAuditStep()
	if r.err != nil || !strings.Contains(r.confirmNext, "snapshot is unreadable") {
		t.Fatalf("err=%v confirmNext=%q, want the unreadable-snapshot gate", r.err, r.confirmNext)
	}
	if !strings.Contains(r.output, "[HIGH] maintainer snapshot") || !strings.Contains(r.output, "1 high, 0 glance, 1 clean") {
		t.Fatalf("output:\n%s", r.output)
	}
	if menu := runAudit(auditNow); menu.commit == nil {
		t.Fatal("the menu audit should offer `a` to replace the corrupt snapshot")
	}
	if b, _ := os.ReadFile(baselinePath()); string(b) != "{garbage" {
		t.Fatalf("an unanswered audit must leave the corrupt snapshot alone, got %q", b)
	}

	if err := r.commit(); err != nil { // the gate is accepted
		t.Fatal(err)
	}
	if got, state := loadBaseline(); state != baselineOK || got["steady"] != "alice" {
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
	if _, state := loadBaseline(); state != baselineOK {
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
	if snap, _ := loadBaseline(); snap["victim"] != "bob" || snap["fresh"] != "carol" {
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
	if snap, _ := loadBaseline(); snap["victim"] != "mallory" {
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
