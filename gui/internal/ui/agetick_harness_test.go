package ui

// The Overview's relative times tick (#266): Last run said "just now" for
// ten minutes and showed its age only after a restart. A run that ended
// 88 s ago says "just now"; a few seconds on, with the ticker shortened to
// 400 ms, it says "2 min ago", and the Updates card's "checked" follows.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
)

func init() {
	covScenarios["cov-age-tick"] = covAgeTick
	covOptions["cov-age-tick"] = func(*Options, *report) { ageTickMS = 400 }
}

func covAgeTick(c *covEnv) {
	w := c.w
	c.quiet()
	c.ok(strings.Contains(w.lastTitle.Text(), "just now"), "the Last run starts as %q, want just now", w.lastTitle.Text())
	c.ok(strings.Contains(w.upd.detail.Text(), "just now"), "the Updates card starts as %q, want just now", w.upd.detail.Text())
	c.seq(3500,
		func() {
			c.ok(strings.Contains(w.lastTitle.Text(), "2 min ago"), "after the ticker the Last run says %q, want 2 min ago", w.lastTitle.Text())
			c.ok(strings.Contains(w.upd.detail.Text(), "2 min ago"), "after the ticker the Updates card says %q, want 2 min ago", w.upd.detail.Text())
		},
	)
}

// TestGUIAgeTick: see covAgeTick.
func TestGUIAgeTick(t *testing.T) {
	needDisplay(t)
	state := filepath.Join(t.TempDir(), "state")
	then := time.Now().Add(-88 * time.Second)
	st := logstore.New(logstore.Options{Dir: state, Now: func() time.Time { return then }})
	if err := st.WriteLastRun(logstore.RunRecord{Action: actions.IDTopgrade, Outcome: logstore.OutcomeOK, Started: then.Add(-time.Minute), Finished: then}); err != nil {
		t.Fatal(err)
	}
	cache := `{"v":3,"count":0,"repo":0,"aur":0,"aur_held":0,"checked":"` + then.Format(time.RFC3339) + `"}`
	if err := os.WriteFile(filepath.Join(st.Root(), "updates-cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	child(t, "cov-age-tick", "ARCHMAID_GUI_STATE="+state)
}
