package overview

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const cacheV3 = `{"v":3,"count":10,"repo":9,"aur":1,"aur_held":2,"checked":"2026-11-02T10:00:00+01:00","packages":[]}`

func TestParseUpdates(t *testing.T) {
	u, ok := ParseUpdates([]byte(cacheV3))
	if !ok || u.Count != 10 || u.Repo != 9 || u.AUR != 1 || u.AURHeld != 2 || !u.Known {
		t.Fatalf("%+v %v", u, ok)
	}
	// aur and aur_held are null when yay is missing.
	u, ok = ParseUpdates([]byte(`{"v":3,"count":2,"repo":2,"aur":null,"aur_held":null,"checked":"2026-11-02T10:00:00+01:00"}`))
	if !ok || u.AUR != 0 || u.Repo != 2 {
		t.Fatalf("%+v %v", u, ok)
	}
	for _, bad := range []string{``, `{`, `{"v":3}`, `{"v":3,"count":1,"repo":1,"checked":"yesterday"}`, `{"v":3,"count":1,"repo":-1,"checked":"2026-11-02T10:00:00+01:00"}`} {
		if _, ok := ParseUpdates([]byte(bad)); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestUpdatesFreshness(t *testing.T) {
	u, _ := ParseUpdates([]byte(cacheV3))
	at := u.Checked
	if !u.BlocksInstall(at.Add(time.Hour)) || !u.BlocksInstall(at.Add(24*time.Hour)) {
		t.Error("a fresh cache with repo updates must block")
	}
	if u.BlocksInstall(at.Add(24*time.Hour + time.Second)) {
		t.Error("a cache over 24 h old blocked")
	}
	if u.BlocksInstall(at.Add(-time.Hour)) {
		t.Error("a cache dated in the future blocked")
	}
	u.Repo = 0
	if u.BlocksInstall(at) {
		t.Error("AUR-only updates blocked")
	}
	if (Updates{}).BlocksInstall(at) {
		t.Error("no cache blocked")
	}
}

func TestReadUpdatesFromStateDir(t *testing.T) {
	dir := t.TempDir()
	if u := ReadUpdates(dir); u.Known {
		t.Fatal("a missing file was read")
	}
	if err := os.WriteFile(filepath.Join(dir, "updates-cache.json"), []byte(cacheV3), 0o600); err != nil {
		t.Fatal(err)
	}
	if u := ReadUpdates(dir); !u.Known || u.Repo != 9 {
		t.Fatalf("%+v", u)
	}
	if err := os.WriteFile(filepath.Join(dir, "updates-cache.json"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if u := ReadUpdates(dir); u.Known {
		t.Fatal("garbage was read")
	}
}
