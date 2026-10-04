package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ── every audit setting is read on the model's goroutine ─────────────────────
//
// An audit runs off the model's goroutine (the menu action's Cmd, the step's
// run, a prefetch), so it must not read logDir, aurRPCURL, auditDeadline or
// dryRun there: it audits the audit.Config captured before it left. Under
// -race these tests fail on any such read; without it, on the wrong values.

// gatedAUR points aurRPCURL at a server that signals each request's arrival,
// then holds it until release is closed and answers it from db.
func gatedAUR(t *testing.T, db map[string]aurInfo) (arrived <-chan struct{}, release chan struct{}) {
	t.Helper()
	ch := make(chan struct{}, 16)
	release = make(chan struct{})
	serveAUR(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		ch <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		var results []aurInfo
		for _, a := range r.PostForm["arg[]"] {
			if rec, ok := db[a]; ok {
				results = append(results, rec)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "multiinfo", "results": results})
	})
	return ch, release
}

// TestAuditsReadNoSettingsOffTheModel: a menu audit, a prefetch and an audit
// step run at once while the test goroutine keeps rewriting every setting
// they could read to values that would break them. Each must finish with
// what was captured when it started: the fake AUR, the original snapshot
// and log, the real deadline, no dry run.
func TestAuditsReadNoSettingsOffTheModel(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "steady\n")
	arrived, release := gatedAUR(t, map[string]aurInfo{
		"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 3},
	})
	oldDeadline, oldDry := auditDeadline, dryRun
	t.Cleanup(func() { auditDeadline, dryRun = oldDeadline, oldDry })
	origLog := logDir
	snapshot := filepath.Join(origLog, "aur-maintainers.json")
	elsewhere := []string{t.TempDir(), t.TempDir()}

	store := logs()                           // the store they all capture
	menu := discoverLater(actions.IDAURAudit) // its Env captured here, as main does
	fetch := startAuditFetch()
	reapAuditFetch(t, fetch)
	step := auditStep()
	fetchDone := fetch.Done()
	menuDone := make(chan actions.Discovery, 1)
	stepDone := make(chan steps.Result, 1)
	go func() { menuDone <- menu() }()
	go func() { stepDone <- step.Run(context.Background(), steps.Env{}) }()

	var msg actions.Discovery
	var sr steps.Result
	gotMenu, gotStep, gotFetch, arrivals, released := false, false, false, 0, false
	deadline := time.Now().Add(10 * time.Second)
	for i := 0; !(gotMenu && gotStep && gotFetch); i++ {
		if time.Now().After(deadline) {
			t.Fatalf("audits still running: menu=%v step=%v fetch=%v, %d of 3 reached the AUR", gotMenu, gotStep, gotFetch, arrivals)
		}
		// No syscalls in here: the race detector treats file I/O as
		// synchronisation, which would hide a racy read.
		logDir = elsewhere[i%2]
		aurRPCURL = "http://127.0.0.1:1/rpc"
		auditDeadline = time.Nanosecond
		dryRun = i%2 == 0
		select {
		case <-arrived:
			if arrivals++; arrivals == 3 && !released {
				close(release)
				released = true
			}
		case msg = <-menuDone:
			gotMenu = true
		case sr = <-stepDone:
			gotStep = true
		case <-fetchDone:
			gotFetch, fetchDone = true, nil // a nil channel never fires again
		default:
			runtime.Gosched()
		}
	}
	logDir, dryRun = origLog, oldDry

	if r, _ := msg.(actions.Report); !strings.Contains(r.Text, "1 foreign package") {
		t.Fatalf("menu audit: %#v", msg)
	}
	if sr.Err != nil || !strings.Contains(sr.Output, "1 foreign package(s) checked") {
		t.Fatalf("audit step: err=%v output=%q", sr.Err, sr.Output)
	}
	if res, err := fetch.Wait(context.Background()); err != nil || res.Total != 1 {
		t.Fatalf("prefetch: err=%v total=%d", err, res.Total)
	}
	// The menu audit saved its baseline and logged where it started.
	if snap, state := audit.LoadSnapshot(snapshot); state != audit.BaselineOK || snap["steady"].Maintainer != "alice" {
		t.Fatalf("snapshot at %s: state=%d %v", snapshot, state, snap)
	}
	// The menu audit logged to the store captured on the model's goroutine.
	if log, _ := os.ReadFile(store.Path(logCategoryAudit)); !strings.Contains(string(log), "AUR audit: 1 checked") {
		t.Fatalf("audit log:\n%s", log)
	}
	for _, d := range elsewhere {
		if entries, _ := os.ReadDir(d); len(entries) != 0 {
			t.Fatalf("%s should be untouched, has %d entries", d, len(entries))
		}
	}
}

// TestAuditCommitWritesTheSnapshotItRead: a commit run after logDir moved
// (the `a` on the audit screen, an accepted gate) still writes the snapshot
// the audit read, and logs beside it, not into the new dir.
func TestAuditCommitWritesTheSnapshotItRead(t *testing.T) {
	for _, path := range []string{"menu", "step"} {
		t.Run(path, func(t *testing.T) {
			takeoverFixture(t) // victim: bob in the snapshot, mallory on the AUR
			snapshot := baselinePath()
			store := logs() // the store the audit captures, until logDir changes

			var commit func() error
			if path == "menu" {
				commit = runAudit(auditNow).commit
			} else {
				commit = auditStep().Run(context.Background(), steps.Env{}).Commit
			}
			if commit == nil {
				t.Fatal("a maintainer change should leave a commit to run")
			}
			moved := t.TempDir()
			logDir = moved
			if err := commit(); err != nil {
				t.Fatal(err)
			}
			if snap, _ := audit.LoadSnapshot(snapshot); snap["victim"].Maintainer != "mallory" {
				t.Fatalf("snapshot the audit read = %v, want victim accepted as mallory", snap)
			}
			if entries, _ := os.ReadDir(moved); len(entries) != 0 {
				t.Fatalf("the commit wrote into the new log dir: %d entries", len(entries))
			}
			if log, _ := os.ReadFile(store.Path(logCategoryAudit)); !strings.Contains(string(log), "ACCEPT") {
				t.Fatalf("the accept should be logged beside the snapshot:\n%s", log)
			}
		})
	}
}
