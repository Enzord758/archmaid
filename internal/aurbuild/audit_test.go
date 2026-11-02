package aurbuild

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/audit"
)

// auditServer answers the maintainer audit's RPC info with the given
// maintainers (name -> maintainer), recording the names asked.
func auditServer(t *testing.T, maint map[string]string, asked *[]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		var res []map[string]any
		for _, n := range r.PostForm["arg[]"] {
			*asked = append(*asked, n)
			if m, ok := maint[n]; ok {
				rec := map[string]any{"Name": n, "Maintainer": m, "LastModified": time.Now().Add(-1000 * time.Hour).Unix(), "NumVotes": 5}
				if m == "" {
					rec["Maintainer"] = nil // orphaned
				}
				res = append(res, rec)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "multiinfo", "resultcount": len(res), "results": res})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestRunAudit: the installed foreign packages and the install's AUR names are
// audited together (so the snapshot is not shrunk), and only the names'
// findings come back, high ones first.
func TestRunAudit(t *testing.T) {
	var asked []string
	url := auditServer(t, map[string]string{"app": "mallory", "zeta": "mallory", "lib": "", "old": "carol"}, &asked)
	snap := filepath.Join(t.TempDir(), audit.SnapshotFile)
	// app and zeta changed hands; old (installed, not asked for) did too; lib is orphaned
	if err := audit.WriteSnapshot(snap, map[string]audit.Entry{"app": {Maintainer: "alice", HasCo: true},
		"zeta": {Maintainer: "alice", HasCo: true}, "old": {Maintainer: "bob", HasCo: true}, "lib": {Maintainer: "", HasCo: true}}); err != nil {
		t.Fatal(err)
	}
	cfg := audit.Config{RPCURL: url, SnapshotPath: snap, Deadline: 5 * time.Second,
		Foreign: func(context.Context) ([]string, error) { return []string{"old", "app"}, nil }}
	res, findings, err := RunAudit(context.Background(), cfg, []string{"zeta", "app", "lib"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 4 { // old, app, zeta, lib: the installed ones are kept, a name in both lists once
		t.Fatalf("Total = %d, want 4", res.Total)
	}
	var got []string
	for _, f := range findings {
		got = append(got, f.Name)
		if f.Name == "old" {
			t.Fatal("a finding about a package this install does not touch")
		}
	}
	if strings.Join(got, ",") != "app,zeta,lib" || !findings[0].High || !findings[1].High || findings[2].High {
		t.Fatalf("findings = %+v, want the high ones first, by name", findings)
	}
	if !strings.Contains(findings[0].Reason, "alice") || findings[2].Reason == "" {
		t.Fatalf("reasons = %q / %q", findings[0].Reason, findings[2].Reason)
	}
	if got := strings.Join(asked, ","); !strings.Contains(got, "lib") || !strings.Contains(got, "old") {
		t.Fatalf("asked %q", got)
	}
}

// TestRunAuditNothingInstalled: with no foreign list the names alone are
// audited; a failing lister and a dead AUR are errors.
func TestRunAuditEdges(t *testing.T) {
	var asked []string
	url := auditServer(t, map[string]string{"app": "alice"}, &asked)
	cfg := audit.Config{RPCURL: url, SnapshotPath: filepath.Join(t.TempDir(), audit.SnapshotFile), Deadline: 5 * time.Second}
	res, findings, err := RunAudit(context.Background(), cfg, []string{"app"}, time.Now())
	if err != nil || res.Total != 1 || len(findings) != 0 {
		t.Fatalf("names alone: %+v %+v %v", res, findings, err)
	}
	cfg.Foreign = func(context.Context) ([]string, error) { return nil, errors.New("pacman gone") }
	if _, _, err := RunAudit(context.Background(), cfg, []string{"app"}, time.Now()); err == nil || !strings.Contains(err.Error(), "pacman gone") {
		t.Fatalf("lister error: %v", err)
	}
	cfg.Foreign = nil
	cfg.RPCURL = ""
	if _, _, err := RunAudit(context.Background(), cfg, []string{"app"}, time.Now()); err == nil {
		t.Fatal("an audit without an RPC URL succeeded")
	}
}
