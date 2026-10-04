package audit

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStateRoundTripAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", StateFile)
	want := State{
		V: StateVersion, Checked: now, Packages: 7, SnapshotPresent: true, SnapshotReadable: true,
		High: []StateFinding{{Pkg: "victim", Reason: "maintainer bob → mallory"}},
	}
	if err := WriteState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Checked.Equal(want.Checked) {
		t.Fatalf("checked = %v, want %v", got.Checked, want.Checked)
	}
	got.Checked = want.Checked
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	for _, k := range []string{`"v": 1`, `"checked"`, `"packages": 7`, `"high"`, `"pkg": "victim"`, `"reason"`, `"snapshot_present": true`, `"snapshot_readable": true`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("file lacks %s:\n%s", k, b)
		}
	}
}

func TestStateEmptyHighIsAnArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFile)
	if err := WriteState(path, State{V: StateVersion}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"high": []`) {
		t.Fatalf("high must be [] not null:\n%s", b)
	}
}

func TestStateWriteIsAtomicAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StateFile)
	if err := WriteState(path, State{V: 1, Packages: 1}); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(path, State{V: 1, Packages: 2}); err != nil {
		t.Fatal(err)
	}
	if s, _ := ReadState(path); s.Packages != 2 {
		t.Fatalf("not replaced: %+v", s)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp files left behind: %v", ents)
	}
	// A write that can't complete leaves the previous file untouched.
	if err := WriteState(filepath.Join(path, "x"), State{}); err == nil {
		t.Fatal("expected an error writing under a file")
	}
	if s, _ := ReadState(path); s.Packages != 2 {
		t.Fatalf("a failed write damaged the file: %+v", s)
	}
}

func TestReadStateMissingAndCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFile)
	if _, err := ReadState(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want not-exist", err)
	}
	os.WriteFile(path, []byte("{nope"), 0o600)
	if _, err := ReadState(path); err == nil {
		t.Fatal("corrupt state should be an error")
	}
}

func stateConfig(t *testing.T, rpcURL string, pkgs ...string) (Config, *logged) {
	t.Helper()
	cfg, l := config(t, rpcURL, pkgs...)
	cfg.StatePath = filepath.Join(filepath.Dir(cfg.SnapshotPath), StateFile)
	return cfg, l
}

func TestRunRecordsHighsAndCommitClearsThem(t *testing.T) {
	url, _, _ := fakeAUR(t, map[string]Info{
		"victim": {Name: "victim", Maintainer: strp("mallory"), LastModified: aged(1), NumVotes: 3, CoMaintainers: []string{"eve"}},
		"quiet":  {Name: "quiet", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 3},
	})
	cfg, _ := stateConfig(t, url, "victim", "quiet")
	if err := WriteSnapshot(cfg.SnapshotPath, map[string]Entry{
		"victim": {Maintainer: "bob", HasCo: true}, "quiet": {Maintainer: "alice", HasCo: true},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ReadState(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	want := []StateFinding{{Pkg: "victim", Reason: "maintainer bob → mallory; co-maintainer eve added"}}
	if !reflect.DeepEqual(s.High, want) || s.Packages != 2 || !s.Checked.Equal(now) || !s.SnapshotPresent || !s.SnapshotReadable {
		t.Fatalf("state = %+v", s)
	}
	// The first, holdHigh commit keeps them listed.
	if err := res.Commit(true, false)(); err != nil {
		t.Fatal(err)
	}
	if s, _ := ReadState(cfg.StatePath); len(s.High) != 1 {
		t.Fatalf("a held commit must keep the highs: %+v", s)
	}
	if err := res.Commit(false, true)(); err != nil {
		t.Fatal(err)
	}
	if s, _ := ReadState(cfg.StatePath); len(s.High) != 0 || s.Packages != 2 {
		t.Fatalf("an accepting commit must clear them: %+v", s)
	}
}

func TestStateCorruptSnapshotAndFirstAudit(t *testing.T) {
	url, _, _ := fakeAUR(t, map[string]Info{"p": {Name: "p", Maintainer: strp("a"), LastModified: aged(24 * 90), NumVotes: 3}})
	cfg, _ := stateConfig(t, url, "p")
	// No snapshot yet: not present, nothing high.
	res, err := Run(context.Background(), cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := ReadState(cfg.StatePath); s.SnapshotPresent || !s.SnapshotReadable || len(s.High) != 0 {
		t.Fatalf("state = %+v", s)
	}
	res.Commit(true, false)()
	if s, _ := ReadState(cfg.StatePath); !s.SnapshotPresent {
		t.Fatalf("the first commit saves the baseline: %+v", s)
	}
	// Corrupt: one high, held through a holdHigh commit, cleared by accepting.
	os.WriteFile(cfg.SnapshotPath, []byte("{garbage"), 0o600)
	res, _ = Run(context.Background(), cfg, now)
	want := []StateFinding{{Pkg: "maintainer snapshot", Reason: "snapshot unreadable"}}
	if s, _ := ReadState(cfg.StatePath); !reflect.DeepEqual(s.High, want) || !s.SnapshotPresent || s.SnapshotReadable {
		t.Fatalf("state = %+v", s)
	}
	res.Commit(true, false)()
	if s, _ := ReadState(cfg.StatePath); len(s.High) != 1 || s.SnapshotReadable {
		t.Fatalf("a held commit leaves the corrupt snapshot flagged: %+v", s)
	}
	res.Commit(false, true)()
	if s, _ := ReadState(cfg.StatePath); len(s.High) != 0 || !s.SnapshotReadable {
		t.Fatalf("accepting replaces it: %+v", s)
	}
}

func TestStateDryRunAndFailedAuditLeaveTheFileAlone(t *testing.T) {
	url, _, _ := fakeAUR(t, map[string]Info{"p": {Name: "p", Maintainer: strp("a"), LastModified: aged(24 * 90), NumVotes: 3}})
	cfg, _ := stateConfig(t, url, "p")
	prev := State{V: StateVersion, Checked: now.Add(-time.Hour), Packages: 9, High: []StateFinding{{Pkg: "old", Reason: "x"}}}
	if err := WriteState(cfg.StatePath, prev); err != nil {
		t.Fatal(err)
	}
	same := func(what string) {
		t.Helper()
		s, err := ReadState(cfg.StatePath)
		if err != nil || s.Packages != 9 || len(s.High) != 1 {
			t.Fatalf("%s changed the state: %+v, %v", what, s, err)
		}
	}
	dry := cfg
	dry.DryRun = true
	if _, err := Run(context.Background(), dry, now); err != nil {
		t.Fatal(err)
	}
	same("a dry run")
	bad := cfg
	bad.RPCURL = "http://127.0.0.1:1/"
	if _, err := Run(context.Background(), bad, now); err == nil {
		t.Fatal("expected an unreachable AUR")
	}
	same("an unreachable AUR")
	// Nothing to audit is a completed audit: stale highs are cleared.
	none := cfg
	none.Foreign = func(context.Context) ([]string, error) { return nil, nil }
	if _, err := Run(context.Background(), none, now); !errors.Is(err, ErrNoForeign) {
		t.Fatal(err)
	}
	if s, _ := ReadState(cfg.StatePath); len(s.High) != 0 || s.Packages != 0 {
		t.Fatalf("state = %+v", s)
	}
}

func TestStateReasonIsSanitized(t *testing.T) {
	url, _, _ := fakeAUR(t, map[string]Info{"v": {Name: "v", Maintainer: strp("mal\x1b[31mlory"), LastModified: aged(24 * 90), NumVotes: 3}})
	cfg, _ := stateConfig(t, url, "v")
	WriteSnapshot(cfg.SnapshotPath, map[string]Entry{"v": {Maintainer: "bob", HasCo: true}})
	if _, err := Run(context.Background(), cfg, now); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg.StatePath)
	if strings.ContainsRune(string(b), 0x1b) || strings.Contains(string(b), `\u001b`) {
		t.Fatalf("control bytes in the state:\n%s", b)
	}
}
