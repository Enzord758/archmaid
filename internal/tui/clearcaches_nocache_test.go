package tui

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// TestClearCachesNothingToClearNotice (#207): both caches empty ends in the
// notice screen saying "No cache to clear" (no popup in the TUI).
func TestClearCachesNothingToClearNotice(t *testing.T) {
	m, _ := engineModel()
	m = feed(m, engine.Notice{Text: actions.NoCacheText})
	if m.screen != screenNotice {
		t.Fatalf("screen %d, want the notice screen", m.screen)
	}
	v := m.View()
	if !strings.Contains(v, "No cache to clear") || strings.Contains(v, "Nothing to clear") {
		t.Errorf("the notice screen:\n%s", v)
	}
}
