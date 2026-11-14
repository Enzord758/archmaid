package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/healthview"
	"github.com/Enzord758/archmaid/internal/health"
)

// The adapter carries the ignore state through (#288): the Ignored state is
// its own (not Unknown), items map over, and Ignore and Undo write only the
// store of the folder it was given (a temp one here, never the real state).
func TestRealHealthSourceIgnoresThroughTheStore(t *testing.T) {
	dir := t.TempDir()
	src := realHealthAt(dir).(realHealthSource)
	if got := mapHealthState(health.Ignored); got != healthview.Ignored {
		t.Errorf("ignored maps to %v", got)
	}
	if got := mapItems([]health.Item{{Key: "unit:a", Label: "a.service"}}); len(got) != 1 || got[0].Key != "unit:a" || got[0].Label != "a.service" {
		t.Errorf("items map to %+v", got)
	}
	if mapItems(nil) != nil {
		t.Error("no items map to something")
	}
	var ig healthview.Ignorer = src
	if err := ig.Ignore("unit:a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := src.store.Load()["unit:a"]; !ok {
		t.Fatal("Ignore did not reach the store")
	}
	if _, err := os.Stat(filepath.Join(dir, "health-ignore.json")); err != nil {
		t.Fatalf("no ignore file in the given folder: %v", err)
	}
	if err := ig.Unignore("unit:a"); err != nil {
		t.Fatal(err)
	}
	if len(src.store.Load()) != 0 {
		t.Fatal("Undo left the key")
	}
	if err := ig.Ignore(""); err == nil {
		t.Error("an empty key was accepted")
	}
	if err := (realHealthSource{}).Ignore("unit:a"); err == nil {
		t.Error("a source without a store ignored something")
	}
}
