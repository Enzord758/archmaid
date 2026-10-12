package realdriver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// TestRefreshUpdatesWired: a real Run topgrade through the GUI's Driver asks
// for the widget's update check exactly once (Config.RefreshUpdates, which
// New hands the Driver); a dry run asks for none.
func TestRefreshUpdatesWired(t *testing.T) {
	shimTopgrade(t)
	var n atomic.Int32
	w := Start(fakes(t, Config{NoAskpass: true, RefreshUpdates: func(context.Context) error { n.Add(1); return nil }}))
	defer w.Close()
	if out := runOn(t, w, w.Options(), actions.IDTopgrade); out != engine.OutcomeCompleted {
		t.Fatalf("outcome %v", out)
	}
	for end := time.Now().Add(5 * time.Second); n.Load() == 0 && time.Now().Before(end); {
		time.Sleep(5 * time.Millisecond)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("real run: %d refreshes, want 1", got)
	}
	runOn(t, w, dryOptions(w), actions.IDTopgrade)
	time.Sleep(50 * time.Millisecond)
	if got := n.Load(); got != 1 {
		t.Errorf("after a dry run: %d refreshes, want still 1", got)
	}
}
