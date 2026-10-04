package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// snapshotPath is a snapshot path in a private temp dir.
func snapshotPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), SnapshotFile)
}

func TestSnapshotRoundTripAndKeepsMissing(t *testing.T) {
	path := snapshotPath(t)
	if _, state := LoadSnapshot(path); state != BaselineMissing {
		t.Fatalf("no snapshot should exist yet, state = %d", state)
	}
	prev := map[string]Entry{
		"gone":     {Maintainer: "carol", CoMaintainers: []string{"dave"}, HasCo: true},
		"old-gone": {Maintainer: "erin"}, // from an old-format snapshot
		"kept":     {Maintainer: "old"},
	}
	info := map[string]Info{
		"kept": {Name: "kept", Maintainer: strp("new"), CoMaintainers: []string{"zed", "amy", "zed"}},
		"orph": {Name: "orph"},
	}
	if err := WriteSnapshot(path, nextSnapshot([]string{"kept", "orph", "gone", "old-gone"}, info, prev)); err != nil {
		t.Fatal(err)
	}

	got, state := LoadSnapshot(path)
	if state != BaselineOK {
		t.Fatalf("snapshot not saved, state = %d", state)
	}
	want := map[string]Entry{
		"kept":     {Maintainer: "new", CoMaintainers: []string{"amy", "zed"}, HasCo: true}, // sorted, deduplicated
		"orph":     {Maintainer: "", HasCo: true},                                           // none recorded is not "unknown"
		"gone":     prev["gone"],                                                            // absent from the AUR: kept as it was
		"old-gone": prev["old-gone"],                                                        // …still without a co-maintainer record
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %+v\nwant       %+v", got, want)
	}
	b, _ := os.ReadFile(path)
	for _, s := range []string{`"comaintainers": []`, `"amy",`} {
		if !strings.Contains(string(b), s) {
			t.Errorf("snapshot file lacks %s:\n%s", s, b)
		}
	}
}

// TestSnapshotLoadsOldFormat: a snapshot from before co-maintainers were
// tracked (name → maintainer) loads as OK, every entry without a co-maintainer
// record; mixed files load too.
func TestSnapshotLoadsOldFormat(t *testing.T) {
	path := snapshotPath(t)
	os.WriteFile(path, []byte(`{"a": "alice", "o": "", "n": {"maintainer": "nina", "comaintainers": ["bob"]}, "x": {"maintainer": "xia"}}`), 0o600)
	got, state := LoadSnapshot(path)
	want := map[string]Entry{
		"a": {Maintainer: "alice"},
		"o": {Maintainer: ""},
		"n": {Maintainer: "nina", CoMaintainers: []string{"bob"}, HasCo: true},
		"x": {Maintainer: "xia"}, // an object without the key is unrecorded too
	}
	if state != BaselineOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %d, snapshot = %+v", state, got)
	}
}

// TestSnapshotCorruptFileIsNotMissing: an unreadable snapshot must never read
// as "first audit" — that would silently re-baseline over a takeover.
func TestSnapshotCorruptFileIsNotMissing(t *testing.T) {
	for name, content := range map[string]string{
		"invalid JSON": "{not json", "JSON null": "null", "empty": "",
		"null entry": `{"p": null}`, "number entry": `{"p": 5}`,
		"entry without maintainer":  `{"p": {"comaintainers": []}}`,
		"co-maintainers not a list": `{"p": {"maintainer": "a", "comaintainers": "bob"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := snapshotPath(t)
			os.WriteFile(path, []byte(content), 0o600)
			if m, state := LoadSnapshot(path); state != BaselineCorrupt || m != nil {
				t.Fatalf("state = %d, map = %v, want BaselineCorrupt and no data", state, m)
			}
		})
	}
	t.Run("unreadable", func(t *testing.T) {
		path := snapshotPath(t)
		os.Mkdir(path, 0o700) // reading a directory fails with EISDIR, not ENOENT
		if _, state := LoadSnapshot(path); state != BaselineCorrupt {
			t.Fatalf("state = %d, want BaselineCorrupt", state)
		}
	})
}

// TestWriteSnapshotIsAtomicAndPrivate: the snapshot is 0600 and no temp file
// is left behind.
func TestWriteSnapshotIsAtomicAndPrivate(t *testing.T) {
	path := snapshotPath(t)
	for _, m := range []string{"alice", "bob"} { // create, then replace
		if err := WriteSnapshot(path, map[string]Entry{"p": {Maintainer: m, HasCo: true}}); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %v, want 0600", fi.Mode().Perm())
	}
	if got, _ := LoadSnapshot(path); got["p"].Maintainer != "bob" {
		t.Fatalf("snapshot = %v, want the second save", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// TestWriteSnapshotSkipsWhenLocked: while another process holds the lock, a
// save is skipped (ErrSnapshotLocked) and the snapshot is left alone.
func TestWriteSnapshotSkipsWhenLocked(t *testing.T) {
	path := snapshotPath(t)
	save := func(m string) error { return WriteSnapshot(path, map[string]Entry{"p": {Maintainer: m, HasCo: true}}) }
	save("alice")

	unlock, err := lockSnapshot(filepath.Dir(path)) // a separate fd: flock conflicts in-process too
	if err != nil {
		t.Fatal(err)
	}
	if err := save("mallory"); !errors.Is(err, ErrSnapshotLocked) {
		t.Fatalf("err = %v, want ErrSnapshotLocked", err)
	}
	if got, _ := LoadSnapshot(path); got["p"].Maintainer != "alice" {
		t.Fatalf("a locked save must not write, snapshot = %v", got)
	}
	unlock()
	if err := save("bob"); err != nil {
		t.Fatalf("save after unlock: %v", err)
	}
}

// TestCorruptSnapshotFailsClosed: an unreadable snapshot is a high finding
// that gates. The holding save (the menu's) leaves it alone; the accepting
// commit replaces it and keeps the bad file aside, not overwritten.
func TestCorruptSnapshotFailsClosed(t *testing.T) {
	url, _, _ := fakeAUR(t, map[string]Info{"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10}})
	cfg, _ := config(t, url, "steady")
	os.WriteFile(cfg.SnapshotPath, []byte("{garbage"), 0o600)

	res, err := Run(context.Background(), cfg, now)
	if err != nil || res.Baseline != BaselineCorrupt || res.GateReason() == "" {
		t.Fatalf("err=%v baseline=%d gate=%q, want the unreadable-snapshot gate", err, res.Baseline, res.GateReason())
	}
	if !strings.Contains(res.Text(), "[HIGH] maintainer snapshot") || !strings.Contains(res.Text(), "1 high, 0 glance, 1 clean") {
		t.Fatalf("report:\n%s", res.Text())
	}
	if err := res.Commit(true, false)(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg.SnapshotPath); string(b) != "{garbage" {
		t.Fatalf("a holding save must leave the corrupt snapshot alone, got %q", b)
	}

	if err := res.Commit(false, true)(); err != nil { // accepted
		t.Fatal(err)
	}
	if got, state := LoadSnapshot(cfg.SnapshotPath); state != BaselineOK || got["steady"].Maintainer != "alice" {
		t.Fatalf("accepting should save a fresh baseline, state=%d snapshot=%v", state, got)
	}
	aside, _ := filepath.Glob(cfg.SnapshotPath + ".corrupt-*")
	if len(aside) != 1 {
		t.Fatalf("corrupt snapshot should be kept aside, found %v", aside)
	}
	if b, _ := os.ReadFile(aside[0]); string(b) != "{garbage" {
		t.Fatalf("set-aside file = %q", b)
	}
}

// TestOldFormatSnapshotIsACoMaintainerBaseline: after upgrading from a
// version that only recorded maintainers, co-maintainers are a baseline (no
// gate) and get recorded by the commit; from then on an added one gates.
func TestOldFormatSnapshotIsACoMaintainerBaseline(t *testing.T) {
	db := map[string]Info{"victim": {Name: "victim", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10, CoMaintainers: []string{"bob"}}}
	url, _, _ := fakeAUR(t, db)
	cfg, _ := config(t, url, "victim")
	os.WriteFile(cfg.SnapshotPath, []byte(`{"victim": "alice"}`), 0o600)

	res, err := Run(context.Background(), cfg, now)
	if err != nil || res.GateReason() != "" || len(res.Findings) != 0 {
		t.Fatalf("err=%v gate=%q findings=%v, want a clean baseline", err, res.GateReason(), res.Findings)
	}
	if err := res.Commit(false, false)(); err != nil {
		t.Fatal(err)
	}
	if snap, _ := LoadSnapshot(cfg.SnapshotPath); !snap["victim"].HasCo || !reflect.DeepEqual(snap["victim"].CoMaintainers, []string{"bob"}) {
		t.Fatalf("victim = %+v, want its co-maintainers recorded", snap["victim"])
	}
	db["victim"] = Info{Name: "victim", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10, CoMaintainers: []string{"bob", "mallory"}}
	if res, _ := Run(context.Background(), cfg, now); res.GateReason() != "Since the last audit: victim: co-maintainer mallory added." {
		t.Fatalf("GateReason = %q, want the added co-maintainer to gate once recorded", res.GateReason())
	}
}
