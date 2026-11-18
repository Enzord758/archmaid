package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/health"
)

// A health run in the window also writes health.json for the widget, into the
// store's folder (a temp one here, never the real state), through fake Deps
// that reach nothing.
func TestRealHealthRunWritesHealthJSON(t *testing.T) {
	dir := t.TempDir()
	src := realHealthAt(dir).(realHealthSource)
	src.d = health.Deps{IgnoreLoad: src.store.Load}
	if r := src.Run(context.Background()); len(r.Checks) != 11 {
		t.Fatalf("%d checks", len(r.Checks))
	}
	b, err := os.ReadFile(filepath.Join(dir, health.ReportFile))
	if err != nil {
		t.Fatalf("no health.json: %v", err)
	}
	if !strings.HasPrefix(string(b), `{"v":1,`) || !strings.Contains(string(b), `"id":"failed-units"`) {
		t.Fatalf("%s", b)
	}
}
