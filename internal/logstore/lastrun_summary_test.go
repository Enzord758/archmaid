package logstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLastRunSummary: the summary object is written exactly as the widget
// reads it, an unset attention list is written as [], and the schema stays
// v1.
func TestLastRunSummary(t *testing.T) {
	dir := t.TempDir()
	s := New(Options{Dir: dir})
	in := RunRecord{Action: "full-cleanup", Outcome: OutcomeOK,
		Summary: &RunSummary{Upgraded: 47, AUR: 6, FreedBytes: 3328599654}}
	if err := s.WriteLastRun(in); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "last-run.json"))
	if want := `"summary":{"upgraded":47,"aur":6,"freed_bytes":3328599654,"attention":[]}`; !strings.Contains(string(raw), want) || !strings.Contains(string(raw), `"v":1`) {
		t.Fatalf("raw = %s, want %s", raw, want)
	}
	if in.Summary.Attention != nil {
		t.Error("WriteLastRun changed the caller's record")
	}
	got, err := s.ReadLastRun()
	if err != nil || got.Summary == nil || got.Summary.Upgraded != 47 || got.Summary.AUR != 6 || got.Summary.FreedBytes != 3328599654 {
		t.Fatalf("round trip = %+v (%v)", got.Summary, err)
	}
	in.Summary.Attention = []string{"Restart recommended", "1 new .pacnew"}
	if err := s.WriteLastRun(in); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ReadLastRun(); len(got.Summary.Attention) != 2 || got.Summary.Attention[0] != "Restart recommended" {
		t.Errorf("attention = %v", got.Summary.Attention)
	}
}

// TestLastRunOldAndNewCompat: a record written before the summary existed
// reads with a nil Summary; a record without one writes no summary key; a
// reader that knows nothing of summary still decodes the new file.
func TestLastRunOldAndNewCompat(t *testing.T) {
	dir := t.TempDir()
	s := New(Options{Dir: dir})
	old := `{"v":1,"action":"full-cleanup","started":"2026-10-04T17:48:40+02:00","finished":"2026-10-04T17:49:40+02:00","outcome":"ok","steps_failed":0,"steps_skipped":0,"steps_refused":0,"dry_run":false}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "last-run.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadLastRun()
	if err != nil || got.Summary != nil || got.Outcome != OutcomeOK {
		t.Fatalf("old record = %+v (%v)", got, err)
	}
	if err := s.WriteLastRun(RunRecord{Action: "x", Outcome: OutcomeOK}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "last-run.json")); strings.Contains(string(raw), "summary") {
		t.Errorf("a record without a summary wrote one: %s", raw)
	}
	if err := s.WriteLastRun(RunRecord{Action: "x", Outcome: OutcomeOK, Summary: &RunSummary{Upgraded: 1}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "last-run.json"))
	var before struct {
		V       int    `json:"v"`
		Action  string `json:"action"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(raw, &before); err != nil || before.V != 1 || before.Outcome != "ok" {
		t.Errorf("a reader without summary decodes %+v (%v)", before, err)
	}
}
