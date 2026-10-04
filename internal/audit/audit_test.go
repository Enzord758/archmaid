package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// ── helpers ───────────────────────────────────────────────────────────────────
//
// Fully offline: the AUR is an httptest server and the foreign-package list
// is a Config.Foreign func, so nothing here runs pacman or reaches the real
// AUR.

func strp(s string) *string { return &s }

var now = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

func aged(h int) int64 { return now.Add(-time.Duration(h) * time.Hour).Unix() }

// serve starts a one-off AUR for the test and returns its URL.
func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// fakeAUR serves the RPC "info" endpoint from db (name → record), counting
// requests and recording the arg[] batches it was asked for.
func fakeAUR(t *testing.T, db map[string]Info) (rpcURL string, requests *atomic.Int32, batches *[][]string) {
	t.Helper()
	var n atomic.Int32
	var mu sync.Mutex
	var seen [][]string
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST (chunked lookups must not depend on URL length)", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		args := r.PostForm["arg[]"]
		mu.Lock()
		seen = append(seen, args)
		mu.Unlock()
		var results []Info
		for _, a := range args {
			if rec, ok := db[a]; ok {
				results = append(results, rec)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "multiinfo", "resultcount": len(results), "results": results})
	})
	return url, &n, &seen
}

// logged collects what an audit writes to its log.
type logged struct {
	mu      sync.Mutex
	entries []string // "KIND: label\nbody"
}

func (l *logged) log(kind, label, body string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, kind+": "+label+"\n"+body)
}

func (l *logged) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

// config is a Config for pkgs against rpcURL, with the snapshot in a private
// temp dir and the log collected.
func config(t *testing.T, rpcURL string, pkgs ...string) (Config, *logged) {
	t.Helper()
	l := &logged{}
	return Config{
		RPCURL:       rpcURL,
		SnapshotPath: filepath.Join(t.TempDir(), SnapshotFile),
		Deadline:     5 * time.Second,
		Foreign:      func(context.Context) ([]string, error) { return pkgs, nil },
		Log:          l.log,
	}, l
}

// seed writes a snapshot of each package's current maintainer.
func seed(t *testing.T, path string, maintainers map[string]string) {
	t.Helper()
	next := map[string]Entry{}
	for n, m := range maintainers {
		next[n] = Entry{Maintainer: m, HasCo: true}
	}
	if err := WriteSnapshot(path, next); err != nil {
		t.Fatal(err)
	}
}

// ── classify ──────────────────────────────────────────────────────────────────

func TestClassify(t *testing.T) {
	old := aged(24 * 30)
	cases := []struct {
		name     string
		ai       *Info
		prev     string
		hadPrev  bool
		wantHigh bool
		want     []string // substrings, one per expected flag, in order
	}{
		{"clean", &Info{Maintainer: strp("alice"), LastModified: old, NumVotes: 5}, "alice", true, false, nil},
		{"unknown to the AUR", nil, "", false, false, []string{"not on the AUR"}},
		{"adopted after being orphaned", &Info{Maintainer: strp("mallory"), LastModified: old, NumVotes: 5}, "", true, true, []string{"adopted by mallory (was orphaned)"}},
		{"maintainer changed", &Info{Maintainer: strp("bob"), LastModified: old, NumVotes: 5}, "alice", true, true, []string{"maintainer changed: alice → bob"}},
		{"newly orphaned", &Info{LastModified: old, NumVotes: 5}, "alice", true, false, []string{"orphaned (was alice)"}},
		{"orphaned, no history", &Info{LastModified: old, NumVotes: 5}, "", false, false, []string{"orphaned"}},
		{"no baseline never flags a change", &Info{Maintainer: strp("bob"), LastModified: old, NumVotes: 5}, "alice", false, false, nil},
		{"recent push", &Info{Maintainer: strp("alice"), LastModified: aged(5), NumVotes: 5}, "alice", true, false, []string{"modified 5h ago"}},
		{"just past the window is quiet", &Info{Maintainer: strp("alice"), LastModified: aged(73), NumVotes: 5}, "alice", true, false, nil},
		{"out of date", &Info{Maintainer: strp("alice"), LastModified: old, NumVotes: 5, OutOfDate: new(int64)}, "alice", true, false, []string{"flagged out of date"}},
		{"no votes", &Info{Maintainer: strp("alice"), LastModified: old, NumVotes: 0}, "alice", true, false, []string{"0 votes"}},
		{"takeover shape: adopted and pushed", &Info{Maintainer: strp("mallory"), LastModified: aged(2), NumVotes: 0}, "", true, true,
			[]string{"adopted by mallory", "modified 2h ago", "0 votes"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify("pkg", c.ai, Entry{Maintainer: c.prev, HasCo: true}, c.hadPrev, now)
			if got.High != c.wantHigh {
				t.Errorf("high = %v, want %v (flags %v)", got.High, c.wantHigh, got.Flags)
			}
			if len(got.Flags) != len(c.want) {
				t.Fatalf("flags = %v, want %d matching %v", got.Flags, len(c.want), c.want)
			}
			for i, w := range c.want {
				if !strings.Contains(got.Flags[i], w) {
					t.Errorf("flag[%d] = %q, want it to contain %q", i, got.Flags[i], w)
				}
			}
		})
	}
}

// TestClassifyCoMaintainers: an added co-maintainer can push like the
// maintainer, so it is high; a removed one is a glance. An entry without a
// co-maintainer record (an old-format snapshot) is a baseline: no flag.
func TestClassifyCoMaintainers(t *testing.T) {
	old := aged(24 * 30)
	cur := func(co ...string) *Info {
		return &Info{Maintainer: strp("alice"), LastModified: old, NumVotes: 5, CoMaintainers: co}
	}
	rec := func(co ...string) Entry { return Entry{Maintainer: "alice", CoMaintainers: co, HasCo: true} }
	cases := []struct {
		name     string
		ai       *Info
		prev     Entry
		hadPrev  bool
		wantHigh bool
		wantCo   []string
		want     []string
	}{
		{"unchanged", cur("bob"), rec("bob"), true, false, nil, nil},
		{"order doesn't matter", cur("carol", "bob"), rec("bob", "carol"), true, false, nil, nil},
		{"added", cur("bob", "mallory"), rec("bob"), true, true, []string{"mallory"}, []string{"co-maintainer mallory added"}},
		{"first one added", cur("mallory"), rec(), true, true, []string{"mallory"}, []string{"co-maintainer mallory added"}},
		{"several added", cur("x", "w", "v", "u"), rec(), true, true, []string{"u", "v", "w", "x"}, []string{"co-maintainers u, v, w +1 more added"}},
		{"removed", cur(), rec("bob"), true, false, nil, []string{"co-maintainer bob removed"}},
		{"swapped", cur("mallory"), rec("bob"), true, true, []string{"mallory"}, []string{"co-maintainer mallory added", "co-maintainer bob removed"}},
		{"old-format entry is a baseline", cur("mallory"), Entry{Maintainer: "alice"}, true, false, nil, nil},
		{"no baseline never flags", cur("mallory"), rec(), false, false, nil, nil},
		{"with a maintainer change", &Info{Maintainer: strp("eve"), LastModified: old, NumVotes: 5, CoMaintainers: []string{"mallory"}}, rec(), true, true,
			[]string{"mallory"}, []string{"maintainer changed: alice → eve", "co-maintainer mallory added"}},
		{"escapes stripped for display", cur("mal\x1b]0;x\x07lory"), rec(), true, true, []string{"mal\x1b]0;x\x07lory"}, []string{"co-maintainer mallory added"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify("pkg", c.ai, c.prev, c.hadPrev, now)
			if got.High != c.wantHigh || !reflect.DeepEqual(got.CoAdded, c.wantCo) || !reflect.DeepEqual(got.Flags, c.want) {
				t.Fatalf("high=%v coAdded=%q flags=%q, want high=%v coAdded=%q flags=%q", got.High, got.CoAdded, got.Flags, c.wantHigh, c.wantCo, c.want)
			}
		})
	}
}

func TestGateReasonNamesMaintainerChanges(t *testing.T) {
	f := func(names ...string) []Finding {
		var out []Finding
		for _, n := range names {
			out = append(out, Finding{Name: n, High: true, MaintainerChanged: true})
		}
		return append(out, Finding{Name: "glance-only"}) // never named
	}
	cases := map[string][]Finding{
		"": f(),
		"a changed maintainer since the last audit.":                        f("a"),
		"a and b changed maintainer since the last audit.":                  f("a", "b"),
		"3 AUR packages (a, b, …) changed maintainer since the last audit.": f("a", "b", "c"),
	}
	for want, in := range cases {
		if got := (Result{Findings: in, Baseline: BaselineOK}).GateReason(); got != want {
			t.Errorf("GateReason(%d high) = %q, want %q", len(in)-1, got, want)
		}
	}
}

func TestGateReasonNamesCoMaintainers(t *testing.T) {
	changed := Finding{Name: "victim", High: true, MaintainerChanged: true}
	co := func(pkg string, names ...string) Finding {
		return Finding{Name: pkg, High: true, CoAdded: names}
	}
	cases := []struct {
		in   []Finding
		want string
	}{
		{[]Finding{co("foo", "bob")}, "Since the last audit: foo: co-maintainer bob added."},
		{[]Finding{co("foo", "bob", "carol")}, "Since the last audit: foo: co-maintainers bob, carol added."},
		{[]Finding{changed, co("foo", "bob")}, "Since the last audit: victim changed maintainer; foo: co-maintainer bob added."},
		{[]Finding{co("foo", "bob"), co("bar", "eve")}, "Since the last audit: foo and bar added co-maintainers."},
		{[]Finding{co("f\x1b[2Joo", "b\x1b]0;x\x07ob")}, "Since the last audit: foo: co-maintainer bob added."},
	}
	for _, c := range cases {
		if got := (Result{Findings: c.in, Baseline: BaselineOK}).GateReason(); got != c.want {
			t.Errorf("GateReason = %q, want %q", got, c.want)
		}
	}
}

// TestGateReasonCorruptSnapshot: an unreadable snapshot gates on its own,
// whatever else was found (fail closed).
func TestGateReasonCorruptSnapshot(t *testing.T) {
	r := Result{Baseline: BaselineCorrupt, Findings: []Finding{snapshotFinding()}}
	if got := r.GateReason(); got != "The AUR maintainer snapshot is unreadable, so maintainer changes can't be checked." {
		t.Fatalf("GateReason = %q", got)
	}
}

func TestClassifyAllSkipsUnknownDebugCompanionAndSortsHighFirst(t *testing.T) {
	// yay-debug: a locally built split package the AUR has never heard of —
	// skipped even though its base (yay) isn't foreign here. foo-debug *is* on
	// the AUR, so it is audited like any other package.
	pkgs := []string{"aaa", "yay-debug", "foo-debug", "zzz"}
	info := map[string]Info{
		"aaa":       {Name: "aaa", LastModified: aged(1000), NumVotes: 0, Maintainer: strp("a")},       // glance (0 votes)
		"foo-debug": {Name: "foo-debug", LastModified: aged(1000), NumVotes: 0, Maintainer: strp("f")}, // glance (0 votes)
		"zzz":       {Name: "zzz", LastModified: aged(1000), NumVotes: 9, Maintainer: strp("new")},     // high (changed)
	}
	prev := map[string]Entry{"aaa": {Maintainer: "a"}, "foo-debug": {Maintainer: "f"}, "zzz": {Maintainer: "old"}}
	got := classifyAll(pkgs, info, prev, true, now)

	var names []string
	for _, f := range got {
		names = append(names, f.Name)
	}
	want := []string{"zzz", "aaa", "foo-debug"} // high first; yay-debug absent
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("findings = %v, want %v", names, want)
	}
}

// ── RPC ───────────────────────────────────────────────────────────────────────

func TestFetchChunksAndPosts(t *testing.T) {
	db := map[string]Info{}
	var pkgs []string
	for i := 0; i < chunk*2+5; i++ {
		n := fmt.Sprintf("pkg%03d", i)
		pkgs = append(pkgs, n)
		if i%2 == 0 { // odd ones are unknown to the AUR
			db[n] = Info{Name: n, NumVotes: i}
		}
	}
	url, reqs, batches := fakeAUR(t, db)

	got, err := fetch(context.Background(), url, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	if reqs.Load() != 3 {
		t.Fatalf("requests = %d, want 3 chunks for %d packages", reqs.Load(), len(pkgs))
	}
	if len((*batches)[0]) != chunk || len((*batches)[2]) != 5 {
		t.Fatalf("chunk sizes = %d/%d/%d", len((*batches)[0]), len((*batches)[1]), len((*batches)[2]))
	}
	if len(got) != len(db) {
		t.Fatalf("found %d, want %d (unknown packages must simply be absent)", len(got), len(db))
	}
	if _, ok := got["pkg001"]; ok {
		t.Fatal("unknown package leaked into the result")
	}
}

func TestFetchErrors(t *testing.T) {
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
			_, err := fetch(context.Background(), serve(t, c.handler), []string{"x"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestFetchCapsTheResponse(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// Valid JSON, but one string field alone is past the cap.
		w.Write([]byte(`{"type":"multiinfo","results":[{"Name":"x","Description":"`))
		w.Write([]byte(strings.Repeat("a", maxResponse)))
		w.Write([]byte(`"}]}`))
	})
	if _, err := fetch(context.Background(), url, []string{"x"}); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("err = %v, want an oversized response to be rejected", err)
	}
}

func TestFetchDropsUnrequestedNames(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"type":"multiinfo","results":[{"Name":"x","NumVotes":1},{"Name":"evil","NumVotes":1}]}`))
	})
	got, err := fetch(context.Background(), url, []string{"x"})
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
		strings.Repeat("x", 100):            strings.Repeat("x", FieldMax) + "…",
		strings.Repeat("x", 64):             strings.Repeat("x", 64),
		"\x1b[1m" + strings.Repeat("y", 64): strings.Repeat("y", 64), // escapes don't count toward the cap
	}
	for in, want := range cases {
		if got := sanitize.Field(in, FieldMax); got != want {
			t.Errorf("Field(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── Run ───────────────────────────────────────────────────────────────────────

// TestRunFirstThenTakeover: the first audit is a baseline (nothing flagged,
// nothing saved by Run itself); once committed, a maintainer change is a high
// finding that gates, and the report and the log name it.
func TestRunFirstThenTakeover(t *testing.T) {
	db := map[string]Info{
		"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10},
		"victim": {Name: "victim", Maintainer: strp("bob"), LastModified: aged(24 * 90), NumVotes: 10},
	}
	url, _, _ := fakeAUR(t, db)
	cfg, log := config(t, url, "steady", "victim")

	first, err := Run(context.Background(), cfg, now)
	if err != nil || first.Baseline != BaselineMissing || len(first.Findings) != 0 || first.GateReason() != "" {
		t.Fatalf("first: err=%v baseline=%d findings=%v", err, first.Baseline, first.Findings)
	}
	if !strings.Contains(first.Text(), "First audit") {
		t.Fatalf("first report:\n%s", first.Text())
	}
	if _, state := LoadSnapshot(cfg.SnapshotPath); state != BaselineMissing {
		t.Fatal("Run must never save the snapshot itself")
	}
	if err := first.Commit(false, false)(); err != nil {
		t.Fatal(err)
	}

	db["victim"] = Info{Name: "victim", Maintainer: strp("mallory"), LastModified: aged(1), NumVotes: 10}
	second, err := Run(context.Background(), cfg, now)
	if err != nil || second.High() != 1 || second.Clean() != 1 {
		t.Fatalf("second: err=%v high=%d clean=%d", err, second.High(), second.Clean())
	}
	if got := second.GateReason(); got != "victim changed maintainer since the last audit." {
		t.Fatalf("GateReason = %q", got)
	}
	for _, want := range []string{"2 foreign package(s) checked against the AUR: 1 high, 0 glance, 1 clean",
		"[HIGH] victim  maintainer changed: bob → mallory · modified 1h ago",
		"only recorded if you accept the update"} {
		if !strings.Contains(second.Text(), want) {
			t.Fatalf("report lacks %q:\n%s", want, second.Text())
		}
	}
	second.LogSummary()
	if !strings.Contains(log.String(), "AUDIT: AUR audit: 2 checked, 1 flagged\n[HIGH] victim") {
		t.Fatalf("log:\n%s", log)
	}
}

// TestCommitHoldsHighFindings: holdHigh saves everything but the high
// findings (their previous entry stays, or none if they had none); a full
// commit with accepting saves them and logs ACCEPT.
func TestCommitHoldsHighFindings(t *testing.T) {
	url, _, _ := fakeAUR(t, map[string]Info{
		"fresh":  {Name: "fresh", Maintainer: strp("carol"), LastModified: aged(24 * 90), NumVotes: 10},
		"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10},
		"victim": {Name: "victim", Maintainer: strp("mallory"), LastModified: aged(24 * 90), NumVotes: 10},
	})
	cfg, log := config(t, url, "fresh", "steady", "victim")
	seed(t, cfg.SnapshotPath, map[string]string{"steady": "alice", "victim": "bob"})

	res, err := Run(context.Background(), cfg, now)
	if err != nil || res.High() != 1 {
		t.Fatalf("err=%v high=%d", err, res.High())
	}
	if err := res.Commit(true, false)(); err != nil {
		t.Fatal(err)
	}
	if snap, _ := LoadSnapshot(cfg.SnapshotPath); snap["victim"].Maintainer != "bob" || snap["fresh"].Maintainer != "carol" {
		t.Fatalf("snapshot = %v, want victim held at bob and fresh recorded", snap)
	}
	if log.String() != "" {
		t.Fatalf("a partial save logs nothing, got:\n%s", log)
	}
	if err := res.Commit(false, true)(); err != nil {
		t.Fatal(err)
	}
	if snap, _ := LoadSnapshot(cfg.SnapshotPath); snap["victim"].Maintainer != "mallory" {
		t.Fatalf("snapshot = %v, want victim accepted as mallory", snap)
	}
	if !strings.Contains(log.String(), "ACCEPT: AUR maintainer changes accepted; snapshot updated") {
		t.Fatalf("log:\n%s", log)
	}
}

// TestDryRunNeverCommits: a dry run audits against the snapshot but has no
// commit to run, and its report says so.
func TestDryRunNeverCommits(t *testing.T) {
	url, _, _ := fakeAUR(t, map[string]Info{"p": {Name: "p", Maintainer: strp("mallory"), LastModified: aged(24 * 90), NumVotes: 3}})
	cfg, _ := config(t, url, "p")
	seed(t, cfg.SnapshotPath, map[string]string{"p": "bob"})
	cfg.DryRun = true

	res, err := Run(context.Background(), cfg, now)
	if err != nil || res.GateReason() == "" || !res.DryRun() {
		t.Fatalf("err=%v gate=%q dry=%v, a dry run still audits and gates", err, res.GateReason(), res.DryRun())
	}
	if res.Commit(false, true) != nil || res.Commit(true, false) != nil {
		t.Fatal("a dry run must have no commit")
	}
	if !strings.Contains(res.Text(), "Dry run: compared against the saved snapshot") || strings.Contains(res.Text(), "only recorded if you accept") {
		t.Fatalf("report:\n%s", res.Text())
	}
}

// TestCommitLogsAFailedSave: a save that can't happen (here: the lock is
// held) is logged as WARN and returned, so the changes stay flagged.
func TestCommitLogsAFailedSave(t *testing.T) {
	url, _, _ := fakeAUR(t, map[string]Info{"p": {Name: "p", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 3}})
	cfg, log := config(t, url, "p")
	res, err := Run(context.Background(), cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lockSnapshot(filepath.Dir(cfg.SnapshotPath))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := res.Commit(false, true)(); !errors.Is(err, ErrSnapshotLocked) {
		t.Fatalf("err = %v, want ErrSnapshotLocked", err)
	}
	if got := log.String(); !strings.Contains(got, "WARN: AUR snapshot not saved: another archmaid") || strings.Contains(got, "ACCEPT") {
		t.Fatalf("log:\n%s", got)
	}
}

// TestRunForeign: no foreign packages is ErrNoForeign; a lister that fails
// fails the audit; a missing setting is an error, never the real AUR.
func TestRunForeign(t *testing.T) {
	url, reqs, _ := fakeAUR(t, nil)
	cfg, _ := config(t, url)
	if _, err := Run(context.Background(), cfg, now); !errors.Is(err, ErrNoForeign) {
		t.Fatalf("err = %v, want ErrNoForeign", err)
	}
	boom := errors.New("pacman exploded")
	cfg.Foreign = func(context.Context) ([]string, error) { return nil, boom }
	if _, err := Run(context.Background(), cfg, now); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the lister's error", err)
	}
	for name, mod := range map[string]func(*Config){
		"no RPC URL":  func(c *Config) { c.RPCURL = "" },
		"no snapshot": func(c *Config) { c.SnapshotPath = "" },
		"no lister":   func(c *Config) { c.Foreign = nil },
	} {
		c, _ := config(t, url, "p")
		mod(&c)
		if _, err := Run(context.Background(), c, now); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if n := reqs.Load(); n != 0 {
		t.Fatalf("%d AUR requests, want none", n)
	}
}

func TestShortErr(t *testing.T) {
	if got := ShortErr(context.DeadlineExceeded, 250*time.Millisecond); got != "no answer within 250ms" {
		t.Fatalf("deadline: %q", got)
	}
	if got := ShortErr(errors.New(strings.Repeat("x", 100)), time.Second); len(got) > 70 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long error not trimmed: %q", got)
	}
}
