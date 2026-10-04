package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The audit itself (classification, the RPC, the snapshot format and its
// save, cancellation, the prefetch) is tested in internal/audit, and the
// TUI's use of this glue (the menu action, the gate, the report screen) in
// internal/tui. These tests pin the glue on its own, as the GUI uses it.

func strp(s string) *string { return &s }

// testStore is a log store in a scratch dir.
func testStore(t *testing.T, dry bool) *logstore.Store {
	t.Helper()
	return logstore.New(logstore.Options{Dir: t.TempDir(), DryRun: dry})
}

// fakeAUR serves the RPC "info" endpoint from db and counts requests.
func fakeAUR(t *testing.T, db map[string]audit.Info) (url string, requests *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		var results []audit.Info
		for _, a := range r.PostForm["arg[]"] {
			if rec, ok := db[a]; ok {
				results = append(results, rec)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "multiinfo", "resultcount": len(results), "results": results})
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &n
}

// foreign is an AuditOptions.Foreign listing names.
func foreign(names ...string) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) { return names, nil }
}

// takeover is a config whose one package changed maintainer since the
// snapshot: a high finding.
func takeover(t *testing.T) audit.Config {
	t.Helper()
	url, _ := fakeAUR(t, map[string]audit.Info{"victim": {Name: "victim", Maintainer: strp("mallory"), NumVotes: 10}})
	cfg := AuditConfig(testStore(t, false), AuditOptions{RPCURL: url, Foreign: foreign("victim")})
	if err := audit.WriteSnapshot(cfg.SnapshotPath, map[string]audit.Entry{
		"victim": audit.EntryFor(audit.Info{Name: "victim", Maintainer: strp("bob")}),
	}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestAuditConfigDefaults: the zero AuditOptions is the real thing, the
// files sit in the store's root, and the log entries go to its audit log.
func TestAuditConfigDefaults(t *testing.T) {
	store := testStore(t, false)
	cfg := AuditConfig(store, AuditOptions{})
	if cfg.RPCURL != audit.DefaultRPCURL || cfg.Deadline != audit.DefaultDeadline || cfg.Foreign == nil || cfg.DryRun {
		t.Fatalf("zero options: %+v", cfg)
	}
	if cfg.SnapshotPath != filepath.Join(store.Root(), audit.SnapshotFile) || cfg.StatePath != filepath.Join(store.Root(), audit.StateFile) {
		t.Fatalf("snapshot %q state %q, want them in %q", cfg.SnapshotPath, cfg.StatePath, store.Root())
	}
	cfg.Log("OK", "label", "body")
	if log, _ := store.Read(logstore.Audit); !strings.Contains(log, "label") {
		t.Fatalf("audit log lacks the entry:\n%s", log)
	}

	o := AuditOptions{RPCURL: "http://x/rpc", Deadline: time.Second, DryRun: true, Foreign: foreign("a")}
	got := AuditConfig(store, o)
	if got.RPCURL != o.RPCURL || got.Deadline != o.Deadline || !got.DryRun {
		t.Fatalf("set options not kept: %+v", got)
	}
	if names, _ := got.Foreign(context.Background()); !slices.Equal(names, []string{"a"}) {
		t.Fatalf("Foreign not kept: %v", names)
	}
}

// TestForeignPackages: `pacman -Qqm`'s lines, trimmed, blanks dropped; a
// failing pacman reads as none.
func TestForeignPackages(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = \"-Qqm\" ] || exit 2\nprintf '  a-bin \\n\\n\\tb-git\\n   \\nc\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := ForeignPackages(context.Background())
	if err != nil || !slices.Equal(got, []string{"a-bin", "b-git", "c"}) {
		t.Fatalf("ForeignPackages = %q, %v", got, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := ForeignPackages(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("a failing pacman: %q, %v; want none", got, err)
	}
}

// TestMenuAuditNotices: nothing to audit, an unreachable AUR and a stopped
// audit are notices, and a stopped one writes no snapshot.
func TestMenuAuditNotices(t *testing.T) {
	now := time.Now()
	notice := func(d actions.Discovery) string {
		t.Helper()
		n, ok := d.(actions.Notice)
		if !ok {
			t.Fatalf("discovery %T, want a Notice", d)
		}
		return n.Text
	}
	store := testStore(t, false)

	none := AuditConfig(store, AuditOptions{Foreign: foreign()})
	if n := notice(MenuAudit(context.Background(), none, now, nil)); !strings.Contains(n, "No foreign") {
		t.Errorf("no packages: %q", n)
	}
	down := AuditConfig(store, AuditOptions{RPCURL: "http://127.0.0.1:1/rpc", Foreign: foreign("a")})
	if n := notice(MenuAudit(context.Background(), down, now, nil)); !strings.HasPrefix(n, "Couldn't reach the AUR: ") {
		t.Errorf("unreachable: %q", n)
	}

	url, _ := fakeAUR(t, map[string]audit.Info{"a": {Name: "a", Maintainer: strp("bob")}})
	ok := AuditConfig(store, AuditOptions{RPCURL: url, Foreign: foreign("a")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if n := notice(MenuAudit(ctx, ok, now, nil)); n != "The AUR audit was stopped." {
		t.Errorf("stopped: %q", n)
	}
	if _, err := os.Stat(ok.SnapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a stopped audit wrote its snapshot (%v)", err)
	}
}

// TestMenuAuditReport: the report is drawn by render (nil: the plain
// text), carries the result, and Accept is set exactly when something is
// high; accepting it clears the flag for the next audit.
func TestMenuAuditReport(t *testing.T) {
	cfg := takeover(t)
	now := time.Now()

	r, ok := MenuAudit(context.Background(), cfg, now, nil).(actions.Report)
	if !ok || r.Audit == nil || r.Accept == nil || r.Text != r.Audit.Text() {
		t.Fatalf("report = %+v, want the plain text, the result and an Accept", r)
	}
	if !strings.Contains(r.Text, "bob → mallory") {
		t.Fatalf("the takeover isn't in the report:\n%s", r.Text)
	}
	drawn, _ := MenuAudit(context.Background(), cfg, now, func(audit.Result) string { return "drawn" }).(actions.Report)
	if drawn.Text != "drawn" {
		t.Fatalf("render not used: %q", drawn.Text)
	}
	if err := r.Accept(); err != nil {
		t.Fatal(err)
	}
	if again, _ := MenuAudit(context.Background(), cfg, now, nil).(actions.Report); again.Accept != nil {
		t.Fatalf("an accepted change is flagged again:\n%s", again.Text)
	}
}

// TestAuditStep: the step as the actions queue it, and its run: the gate
// on topgrade for a high finding, the commit only advancing the snapshot
// when run, and the notices as output.
func TestAuditStep(t *testing.T) {
	cfg := takeover(t)
	s := AuditStep(cfg)
	if s.ID != steps.IDAURAudit || s.Prefetch != steps.PrefetchAURAudit || !s.AlwaysLog || s.Category != logstore.Audit || s.NeedsPriv {
		t.Fatalf("step = %+v", s)
	}
	r := s.Run(context.Background(), steps.Env{})
	if r.Err != nil || !strings.HasSuffix(r.ConfirmNext, " Update anyway?") || r.Commit == nil {
		t.Fatalf("result = %+v, want a gate and a commit", r)
	}
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	if r := s.Run(context.Background(), steps.Env{}); r.ConfirmNext != "" {
		t.Fatalf("a committed change gates again: %q", r.ConfirmNext)
	}

	none := AuditStep(AuditConfig(testStore(t, false), AuditOptions{Foreign: foreign()}))
	if r := none.Run(context.Background(), steps.Env{}); r.Err != nil || !strings.Contains(r.Output, "nothing to audit") {
		t.Fatalf("no packages: %+v", r)
	}
	down := AuditStep(AuditConfig(testStore(t, false), AuditOptions{RPCURL: "http://127.0.0.1:1/rpc", Foreign: foreign("a")}))
	if r := down.Run(context.Background(), steps.Env{}); r.Err == nil || !strings.HasPrefix(r.Output, "Couldn't reach the AUR: ") {
		t.Fatalf("unreachable: %+v", r)
	}
}

// TestPrefetcher: only the audit is prefetched, with the config read when
// it starts, and the step awaiting it asks the AUR no second time.
func TestPrefetcher(t *testing.T) {
	url, requests := fakeAUR(t, map[string]audit.Info{"a": {Name: "a", Maintainer: strp("bob")}})
	calls := 0
	start := Prefetcher(func() audit.Config {
		calls++
		return AuditConfig(testStore(t, false), AuditOptions{RPCURL: url, Foreign: foreign("a")})
	})
	if p := start("something-else"); p != nil || calls != 0 {
		t.Fatalf("an unknown prefetch: %v (config read %d times)", p, calls)
	}
	p := start(steps.PrefetchAURAudit)
	if p == nil || calls != 1 {
		t.Fatalf("the audit's prefetch = %v (config read %d times)", p, calls)
	}
	r := p.Run(context.Background())
	if r.Err != nil || !strings.Contains(r.Output, "First audit") {
		t.Fatalf("prefetched run: %+v", r)
	}
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch never finished")
	}
	p.Cancel() // after it finished: harmless
	if n := requests.Load(); n != 1 {
		t.Fatalf("%d AUR requests, want 1", n)
	}
}

// TestAuditFetchCancelled: a fetch cancelled before its step waits on it
// ends the step at once, with an error.
func TestAuditFetchCancelled(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	f := StartAuditFetch(AuditConfig(testStore(t, false), AuditOptions{RPCURL: srv.URL, Foreign: foreign("a")}))
	f.Cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if r := f.Run(ctx); r.Err == nil || ctx.Err() != nil {
		t.Fatalf("cancelled fetch: %+v (ctx %v)", r, ctx.Err())
	}
}
