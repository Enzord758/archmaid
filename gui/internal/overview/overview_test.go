package overview

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

var now = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func TestEntries(t *testing.T) {
	es := Entries()
	var ids []string
	for _, e := range es {
		ids = append(ids, e.ID)
		if e.Icon == "" || e.Label == "" {
			t.Errorf("%s: icon %q label %q", e.ID, e.Icon, e.Label)
		}
		if a, _ := actions.ByID(e.ID); a.Busy != e.Busy {
			t.Errorf("%s: busy %q, want the menu's %q", e.ID, e.Busy, a.Busy)
		}
		if (e.Kind == KindLogs) != (e.ID == actions.IDViewLog) {
			t.Errorf("%s: kind %v", e.ID, e.Kind)
		}
	}
	want := []string{actions.IDFullCleanup, actions.IDUserCache, actions.IDRepair, actions.IDDownloads,
		actions.IDTopgrade, actions.IDServices, actions.IDAURAudit, actions.IDViewLog}
	if !slices.Equal(ids, want) {
		t.Errorf("entries %q, want %q (the menu's order, without exit and editconfig)", ids, want)
	}
}

func TestStatusLine(t *testing.T) {
	if s := StatusLine(sysstat.Status{}); s != "" {
		t.Errorf("empty status %q", s)
	}
	if s := StatusLine(sysstat.Status{Free: "120G", RAM: "9.1Gi", Cache: "2.3G"}); s != "Free 120G · RAM available 9.1Gi · Cache 2.3G" {
		t.Errorf("status %q", s)
	}
}

func TestAgo(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{-time.Minute, "just now"}, {89 * time.Second, "just now"}, {5 * time.Minute, "5 min ago"},
		{89 * time.Minute, "89 min ago"}, {3 * time.Hour, "3 h ago"}, {35 * time.Hour, "35 h ago"},
		{36 * time.Hour, "1 day ago"}, {47 * time.Hour, "1 day ago"}, {48 * time.Hour, "2 days ago"}, {12*24*time.Hour + time.Hour, "12 days ago"},
	} {
		if got := Ago(now.Add(-tc.d), now); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.d, got, tc.want)
		}
	}
}

// TestSummarize follows the DMS widget's summarize (state.js): the
// outcome's issue first, the staleness warning past 14 days on top.
func TestSummarize(t *testing.T) {
	day := 24 * time.Hour
	rec := func(outcome string, age time.Duration) logstore.RunRecord {
		return logstore.RunRecord{V: 1, Action: actions.IDFullCleanup, Outcome: outcome, Finished: now.Add(-age), StepsFailed: 2, StepsRefused: 1}
	}
	noRefused := func(r logstore.RunRecord) logstore.RunRecord { r.StepsRefused = 0; return r }
	one := func(r logstore.RunRecord) logstore.RunRecord { r.StepsFailed = 1; return r }
	for _, tc := range []struct {
		r      logstore.RunRecord
		level  Level
		issues []string
	}{
		{rec(logstore.OutcomeOK, 3*time.Hour), LevelOK, nil},
		{rec(logstore.OutcomeOK, 14*day), LevelOK, nil},
		{rec(logstore.OutcomeOK, 15*day), LevelWarn, []string{"No cleanup for 15 days"}},
		{rec(logstore.OutcomeFailed, day), LevelError, []string{"Last run failed (2 steps)"}},
		{rec(logstore.OutcomeFailed, 20*day), LevelError, []string{"Last run failed (2 steps)", "No cleanup for 20 days"}},
		{one(rec(logstore.OutcomeFailed, day)), LevelError, []string{"Last run failed (1 step)"}},
		{rec(logstore.OutcomeIncomplete, day), LevelWarn, []string{"Last run incomplete: root steps refused (1)"}},
		{rec(logstore.OutcomeAborted, day), LevelWarn, []string{"Last run aborted"}},
		{noRefused(rec(logstore.OutcomeCancelled, day)), LevelWarn, []string{"Last run cancelled, nothing ran"}},
		{rec(logstore.OutcomeCancelled, day), LevelWarn, []string{"Last run not authorised · nothing ran (1 step not authorised)"}},
		{func() logstore.RunRecord { r := rec(logstore.OutcomeCancelled, day); r.StepsRefused = 3; return r }(), LevelWarn, []string{"Last run not authorised · nothing ran (3 steps not authorised)"}},
	} {
		got := Summarize(tc.r, now)
		if got.Level != tc.level || !slices.Equal(got.Issues, tc.issues) {
			t.Errorf("%s %v old: level %v issues %q, want %v %q", tc.r.Outcome, now.Sub(tc.r.Finished), got.Level, got.Issues, tc.level, tc.issues)
		}
	}
	if got := Summarize(rec(logstore.OutcomeOK, 3*time.Hour), now).Title; got != "Full Cleanup (Recommended) · 3 h ago" {
		t.Errorf("title %q", got)
	}
}

// A dry run's record says so in its title.
func TestSummarizeDryRun(t *testing.T) {
	r := logstore.RunRecord{V: 1, Action: actions.IDFullCleanup, Outcome: logstore.OutcomeOK, Finished: now.Add(-3 * time.Hour), DryRun: true}
	if got := Summarize(r, now).Title; got != "Full Cleanup (Recommended) · 3 h ago · dry run" {
		t.Errorf("title %q", got)
	}
}

// TestReadLastRunOnlyReads: the record a real run wrote is read back, and
// reading leaves the state dir exactly as it was (nothing created, even
// when the dir doesn't exist).
func TestReadLastRunOnlyReads(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if got := ReadLastRun(dir, now); got.Level != LevelNone || got.Title != "No run recorded yet" {
		t.Errorf("no record: %+v", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("reading created the state dir: %v", err)
	}
	st := logstore.New(logstore.Options{Dir: dir, Now: func() time.Time { return now }})
	if err := st.WriteLastRun(logstore.RunRecord{Action: actions.IDTopgrade, Outcome: logstore.OutcomeOK, Started: now.Add(-time.Hour), Finished: now.Add(-50 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	before := tree(t, dir)
	got := ReadLastRun(dir, now)
	if got.Level != LevelOK || got.Title != "Run topgrade (system update) · 50 min ago" {
		t.Errorf("read %+v", got)
	}
	if after := tree(t, dir); !slices.Equal(before, after) {
		t.Errorf("state dir changed: %q → %q", before, after)
	}
	if err := os.WriteFile(filepath.Join(dir, logstore.LastRunFile), []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadLastRun(dir, now); got.Level != LevelNone || len(got.Issues) != 1 {
		t.Errorf("torn record: %+v", got)
	}
}

// tree lists every path under dir with its size and mtime.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprint(p, fi.ModTime(), fi.Mode(), fi.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
